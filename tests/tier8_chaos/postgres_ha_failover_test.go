// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: §17.3 Postgres cross-zone disaster recovery.
//
// The base e2e Kind install runs a single-replica Postgres
// (tests/testinfra/k8s/datastores.yaml), which has nowhere to fail
// over to. This test layers the streaming-replication standby from
// tests/testinfra/kind/datastores-ha-postgres.yaml on top of that
// baseline, drives a real session write onto the primary, kills the
// primary, promotes the standby, rewires the gateway's Postgres DSN to
// the promoted node, and asserts the pre-failover session is still
// readable through the gateway with no data loss. The promotion step
// is operator-managed in v1 (no in-cluster failover controller), which
// is exactly what tests/testinfra/kind/datastores-ha.md documents as
// this test's scope: "the in-Kind exercise pins the primary-down
// recovery half" of the §17.3 exercise, leaving the fully-automatic
// RTO-bounded promotion to the tier-6 cloud suite's managed-Postgres
// run (TestMultiZoneDR).

package tier8_chaos_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// postgresHAOverlayRelPath is the HA overlay this test applies on
	// top of the base e2e datastores.yaml, relative to the repo root.
	postgresHAOverlayRelPath = "tests/testinfra/kind/datastores-ha-postgres.yaml"

	// postgresHATenantPrefix names the synthetic tenant this test
	// bootstraps to carry the pre-failover session. The actual tenant id
	// appends a per-run suffix (see postgresHATenant) rather than
	// reusing a fixed id: tenant deletion (Driver.Close's best-effort
	// cleanup from a prior run) is asynchronous — a bootstrap racing a
	// still-"deleting" tenant from an immediately preceding run gets a
	// 403 TENANT_NOT_ACTIVE rather than a fresh tenant.
	postgresHATenantPrefix = "chaos-postgres-ha-tenant"

	// postgresReplicaDeployment / postgresHABootstrapJob name the
	// resources the overlay creates (tests/testinfra/kind/datastores-ha-postgres.yaml).
	postgresReplicaDeployment = "lenny-postgres-replica"
	postgresHABootstrapJob    = "lenny-postgres-ha-bootstrap"

	// postgresHAPrimarySelector / postgresHAReplicaSelector match the
	// primary (base datastores.yaml) and standby (overlay) Postgres
	// pods.
	postgresHAPrimarySelector = "lenny.dev/e2e-datastore=postgres"
	postgresHAReplicaSelector = "lenny.dev/e2e-datastore=postgres-replica"

	// datastoreConnSecretName is the chart-rendered Secret
	// (charts/lenny/templates/datastore-secret.yaml) the gateway reads
	// LENNY_POSTGRES_DSN from via secretKeyRef. Patching its
	// postgres-dsn key and rolling the gateway is the documented "swap
	// the DSN" operator step the datastores-ha-postgres.yaml overlay
	// describes.
	datastoreConnSecretName = "lenny-datastore-conn"

	// postgresHAPrimaryHost / postgresHAReplicaHost are the Service DNS
	// names datastores.yaml and datastores-ha-postgres.yaml assign the
	// primary and the standby. The DSN swap replaces one with the
	// other in the live secret value rather than hardcoding a full DSN,
	// so the swap preserves whatever credentials the install actually
	// used.
	postgresHAPrimaryHost = "lenny-postgres.lenny-system.svc"
	postgresHAReplicaHost = "lenny-postgres-replica.lenny-system.svc"
)

// spec: 17.3 (disaster recovery)
// diagnosis: §17.3's cross-zone requirements state "Postgres: primary
// and sync replica in different availability zones" with automatic
// failover, and the zone-failure blast radius promises "No data loss
// for committed transactions". Before this test, TestMultiZoneDR only
// inspected the gateway's configured DSN string and the HA overlays
// were wired into no test, installer, or script (T-DEP.6). This test
// drives the real exercise: create a session (a synchronously-committed
// Postgres write per §12.3's "Write durability categories during
// failover" table), wait for the standby to replay it, kill the
// primary, promote the standby with `pg_ctl promote`, rewire the
// gateway's LENNY_POSTGRES_DSN secret to the promoted node, roll the
// gateway, and assert the session is still readable through the live
// gateway API with its pre-failover id and state intact. A failure
// here means either the standby never caught up with a committed write
// before the primary died (a real RPO gap) or the gateway could not
// resume serving reads/writes against the promoted node (the DSN-swap
// recovery path documented in datastores-ha-postgres.yaml is broken).
func TestPostgresHAFailover(t *testing.T) {
	d := sessiondriver.New(t)
	c := d.Cluster()
	postgresHATenant := fmt.Sprintf("%s-%d", postgresHATenantPrefix, time.Now().UnixNano())

	requirePostgresPersistence(t, c)
	pods := kind.RequireAgentWorkload(t, c)
	if len(pods) == 0 {
		t.Skip("no Ready agent-pod workload; §17.3 failover exercise needs a live session to carry")
	}

	overlay := filepath.Join(schematest.RepoRoot(t), postgresHAOverlayRelPath)
	c.Apply(t, overlay)
	t.Cleanup(func() { deletePostgresHAOverlay(t, c, overlay) })

	// Read the live DSN once, before any chaos, so the cleanup below can
	// restore exactly what was there and the replica DSN can be derived
	// from it (same credentials, host swapped) rather than hardcoded.
	originalDSN := readSecretPostgresDSN(t, c)
	if !strings.Contains(originalDSN, postgresHAPrimaryHost) {
		t.Fatalf("gateway postgres-dsn %q does not reference the expected primary host %q; "+
			"cannot derive the replica DSN by host substitution", originalDSN, postgresHAPrimaryHost)
	}
	var dsnSwapped bool
	t.Cleanup(func() {
		if !dsnSwapped {
			return
		}
		restoreGatewayDSN(t, c, originalDSN)
	})

	waitPostgresHAJobComplete(t, c, postgresHABootstrapJob, 90*time.Second)
	if !pollUntil(120*time.Second, 3*time.Second, func() bool {
		return deploymentReady(t, c, postgresReplicaDeployment)
	}) {
		t.Fatalf("%s did not become Ready within 120s (state %s)",
			postgresReplicaDeployment, deploymentReadyState(t, c, postgresReplicaDeployment))
	}
	if !waitReplicaStreaming(t, c, 60*time.Second) {
		t.Fatalf("%s did not reach streaming replication state within 60s", postgresReplicaDeployment)
	}
	t.Logf("%s is Ready and streaming from the primary", postgresReplicaDeployment)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := d.BootstrapTenant(ctx, postgresHATenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	sess, err := d.CreateAndStart(ctx, postgresHATenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("warm pool never became ready for the §17.3 failover session: %v", err)
		}
		t.Fatalf("create-and-start pre-failover session: %v", err)
	}
	t.Logf("created session %s (state %q) on the primary before the failover", sess.ID, sess.State)

	// Wait for the standby to replay the session-creation transaction
	// before killing the primary — this is what makes the later
	// assertion a genuine test of "no data loss for committed
	// transactions" rather than a race against replication lag.
	if !waitReplicaHasSessionRow(t, c, sess.ID, 30*time.Second) {
		t.Fatalf("standby did not replay session %s within 30s of creation; the primary-kill below "+
			"would then be testing a known RPO gap instead of the §17.3 zero-data-loss contract", sess.ID)
	}
	t.Logf("standby has replayed session %s; safe to kill the primary", sess.ID)

	// Kill the primary. scaleDownAndRestore (tests/tier8_chaos/chaos_helpers_test.go)
	// registers the restore-to-Ready-plus-reapply-schema cleanup; it is
	// called here (rather than earlier) so that cleanup runs BEFORE the
	// DSN-restore cleanup above (t.Cleanup is LIFO), which matters: the
	// gateway must not be rolled back onto the primary until the
	// primary is actually back and re-migrated.
	scaleDownAndRestore(t, c, postgresDeployment, func() { ensurePostgresSchema(t, c) })
	if !waitDeploymentScaledDown(t, c, postgresDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", postgresDeployment)
	}
	killedAt := time.Now()
	t.Logf("primary %s killed", postgresDeployment)

	promoteReplica(t, c)
	if !waitReplicaPromoted(t, c, 30*time.Second) {
		t.Fatalf("%s did not leave recovery mode within 30s of pg_ctl promote", postgresReplicaDeployment)
	}
	t.Logf("%s promoted out of recovery", postgresReplicaDeployment)

	replicaDSN := strings.Replace(originalDSN, postgresHAPrimaryHost, postgresHAReplicaHost, 1)
	setSecretPostgresDSN(t, c, replicaDSN)
	dsnSwapped = true
	if out, err := rolloutRestartDeployment(t, c, gatewayDeployment); err != nil {
		t.Fatalf("rollout restart %s onto the promoted standby: %v\n%s", gatewayDeployment, err, out)
	}
	if !pollUntil(120*time.Second, 2*time.Second, func() bool { return deploymentReady(t, c, gatewayDeployment) }) {
		t.Fatalf("%s did not return to Ready within 120s of the DSN swap", gatewayDeployment)
	}
	t.Logf("gateway resumed against the promoted standby %s after %s",
		postgresReplicaDeployment, time.Since(killedAt))

	// Reconnect (a rollout restart replaces every gateway pod, so the
	// original port-forward from d is stale) and assert the
	// pre-failover session is intact.
	d2 := sessiondriver.New(t)
	got, err := d2.GetSession(ctx, postgresHATenant, sess.ID)
	if err != nil {
		t.Fatalf("§17.3 violation: GET session %s against the promoted standby: %v", sess.ID, err)
	}
	if got.ID != sess.ID {
		t.Errorf("§17.3 violation: session id after failover = %q, want %q (data loss)", got.ID, sess.ID)
	}
	if got.State != sess.State {
		t.Errorf("§17.3 violation: session %s state after failover = %q, want %q (data loss)",
			sess.ID, got.State, sess.State)
	}
	t.Logf("§17.3: session %s survived the Postgres primary failover with no data loss (state %q)",
		got.ID, got.State)
}

// requirePostgresPersistence skips the test when the gateway is not
// wired to a Postgres DSN. Mirrors the precondition check
// tests/tier6_e2e_cloud/cluster_assertions_test.go's TestMultiZoneDR
// applies before its own (cloud-only) DSN inspection.
func requirePostgresPersistence(t *testing.T, c *kind.Cluster) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "get", "pods",
		"-l", gatewaySelector, "-o", "jsonpath={.items[0].spec.containers[0].env[*].name}")
	if err != nil || !strings.Contains(out, "LENNY_POSTGRES_DSN") {
		t.Skip("gateway has no LENNY_POSTGRES_DSN; the §17.3 failover exercise needs a Postgres-backed install")
	}
}

// waitPostgresHAJobComplete blocks until the named Job in lenny-system
// reports condition Complete, failing the test on timeout. The
// bootstrap Job (datastores-ha-postgres.yaml) provisions the
// replication role and slot the standby streams over.
func waitPostgresHAJobComplete(t *testing.T, c *kind.Cluster, job string, timeout time.Duration) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "wait",
		"--for=condition=Complete", "job/"+job, fmt.Sprintf("--timeout=%s", timeout))
	if err != nil {
		t.Fatalf("Job %s did not reach Complete within %s: %v\n%s", job, timeout, err, out)
	}
}

// postgresHAExec runs a single-row SQL statement against the Postgres
// pod matching selector via `psql -tAc`, connecting over the
// container's local Unix socket (no -h — the default docker-library
// postgres pg_hba.conf trusts local connections) as the superuser
// account the base install provisions. Returns the last non-empty
// output line, trimmed: `kubectl exec` without an explicit -c prints a
// "Defaulted container ..." advisory on stderr when a pod has more
// than one container (the replica also carries the pg-basebackup init
// container), and CombinedOutput folds that — plus any psql server
// NOTICE/WARNING line — in with the query result on the same
// CombinedOutput stream, so comparing the whole blob against an exact
// expected value would always miss even when the query itself
// succeeded.
func postgresHAExec(t *testing.T, c *kind.Cluster, selector, sql string) (string, error) {
	t.Helper()
	pods := podNames(t, c, selector)
	if len(pods) == 0 {
		return "", fmt.Errorf("no pod matches selector %q", selector)
	}
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", pods[0], "-c", "postgres", "--",
		"psql", "-U", "lenny", "-d", "lenny", "-tAc", sql)
	return lastNonEmptyLine(out), err
}

// lastNonEmptyLine returns the last non-blank line of s, trimmed. -tAc
// output is a single result value on its own line; any advisory or
// NOTICE/WARNING text kubectl or the server emits ahead of it lands on
// earlier lines.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// waitReplicaStreaming polls the primary's pg_stat_replication until it
// reports the standby in the streaming state, meaning the base backup
// completed and the standby is caught up on live WAL.
func waitReplicaStreaming(t *testing.T, c *kind.Cluster, timeout time.Duration) bool {
	t.Helper()
	return pollUntil(timeout, 2*time.Second, func() bool {
		out, err := postgresHAExec(t, c, postgresHAPrimarySelector, "SELECT state FROM pg_stat_replication;")
		return err == nil && out == "streaming"
	})
}

// waitReplicaHasSessionRow polls the standby directly for the given
// session id, proving the specific committed write the test cares
// about has actually replayed there before the primary is killed.
func waitReplicaHasSessionRow(t *testing.T, c *kind.Cluster, sessionID string, timeout time.Duration) bool {
	t.Helper()
	q := fmt.Sprintf("SELECT count(*) FROM sessions WHERE id = '%s'::uuid;", sessionID)
	return pollUntil(timeout, 1*time.Second, func() bool {
		out, err := postgresHAExec(t, c, postgresHAReplicaSelector, q)
		return err == nil && out == "1"
	})
}

// promoteReplica execs `pg_ctl promote` on the standby pod — the
// operator-managed promotion step datastores-ha-postgres.yaml
// documents in place of an in-cluster failover controller.
func promoteReplica(t *testing.T, c *kind.Cluster) {
	t.Helper()
	pods := podNames(t, c, postgresHAReplicaSelector)
	if len(pods) == 0 {
		t.Fatalf("no pod matches selector %q to promote", postgresHAReplicaSelector)
	}
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", pods[0], "-c", "postgres", "--",
		"sh", "-c", `pg_ctl promote -D "$PGDATA"`)
	if err != nil {
		t.Fatalf("pg_ctl promote on %s: %v\n%s", pods[0], err, out)
	}
}

// waitReplicaPromoted polls the standby's pg_is_in_recovery() until it
// reports false, confirming the promotion completed and the node now
// accepts writes.
func waitReplicaPromoted(t *testing.T, c *kind.Cluster, timeout time.Duration) bool {
	t.Helper()
	return pollUntil(timeout, 1*time.Second, func() bool {
		out, err := postgresHAExec(t, c, postgresHAReplicaSelector, "SELECT pg_is_in_recovery();")
		return err == nil && out == "f"
	})
}

// readSecretPostgresDSN reads and base64-decodes the postgres-dsn key
// of the datastore-conn Secret the chart renders
// (charts/lenny/templates/datastore-secret.yaml).
func readSecretPostgresDSN(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "get", "secret", datastoreConnSecretName,
		"-o", "jsonpath={.data.postgres-dsn}")
	if err != nil {
		t.Fatalf("read %s secret: %v\n%s", datastoreConnSecretName, err, out)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("decode postgres-dsn from %s: %v", datastoreConnSecretName, err)
	}
	return string(decoded)
}

// setSecretPostgresDSN merge-patches the datastore-conn Secret's
// postgres-dsn key to dsn. The gateway does not hot-reload the Secret;
// the caller must roll the gateway Deployment for the new value to
// take effect on fresh connections.
func setSecretPostgresDSN(t *testing.T, c *kind.Cluster, dsn string) {
	t.Helper()
	patch := fmt.Sprintf(`{"stringData":{"postgres-dsn":%q}}`, dsn)
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "patch", "secret", datastoreConnSecretName,
		"--type=merge", "-p", patch)
	if err != nil {
		t.Fatalf("patch %s secret postgres-dsn: %v\n%s", datastoreConnSecretName, err, out)
	}
}

// restoreGatewayDSN reverts the datastore-conn Secret's postgres-dsn to
// dsn and rolls the gateway Deployment so it reconnects to the
// restored primary. Called from t.Cleanup, so failures are reported
// with t.Errorf rather than t.Fatalf: a cleanup step must not abort and
// skip the cleanup steps registered before it.
func restoreGatewayDSN(t *testing.T, c *kind.Cluster, dsn string) {
	t.Helper()
	patch := fmt.Sprintf(`{"stringData":{"postgres-dsn":%q}}`, dsn)
	if out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "patch", "secret", datastoreConnSecretName,
		"--type=merge", "-p", patch); err != nil {
		t.Errorf("restore %s secret postgres-dsn: %v\n%s", datastoreConnSecretName, err, out)
		return
	}
	if out, err := rolloutRestartDeployment(t, c, gatewayDeployment); err != nil {
		t.Errorf("rollout restart %s back onto the restored primary: %v\n%s", gatewayDeployment, err, out)
		return
	}
	if !pollUntil(120*time.Second, 2*time.Second, func() bool { return deploymentReady(t, c, gatewayDeployment) }) {
		t.Errorf("%s did not return to Ready within 120s after the DSN was restored (state %s); "+
			"the shared cluster may be left degraded", gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
}

// rolloutRestartDeployment triggers and waits out a rollout restart of
// the named lenny-system Deployment, used to force every replica to
// reconnect after the datastore-conn Secret changes underneath it.
// Returns the kubectl output and any error rather than failing the
// test itself: restoreGatewayDSN calls this from a t.Cleanup, where a
// t.Fatalf would abort the test goroutine and skip cleanups registered
// before it.
func rolloutRestartDeployment(t *testing.T, c *kind.Cluster, deployment string) (string, error) {
	t.Helper()
	if out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "rollout", "restart",
		"deployment/"+deployment); err != nil {
		return out, fmt.Errorf("rollout restart: %w", err)
	}
	out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "rollout", "status",
		"deployment/"+deployment, "--timeout=120s")
	if err != nil {
		return out, fmt.Errorf("rollout status: %w", err)
	}
	return out, nil
}

// deletePostgresHAOverlay removes every resource the overlay created
// (ConfigMap, bootstrap Job, standby Deployment and Service), leaving
// the shared cluster back at its single-replica baseline.
func deletePostgresHAOverlay(t *testing.T, c *kind.Cluster, overlayPath string) {
	t.Helper()
	if out, err := c.KubectlOut(t, "delete", "-f", overlayPath,
		"--ignore-not-found", "--wait=true"); err != nil {
		t.Errorf("delete Postgres HA overlay %s: %v\n%s", overlayPath, err, out)
	}
}
