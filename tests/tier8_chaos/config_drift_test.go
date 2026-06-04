// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for configuration drift. Each test manually
// mutates a piece of live platform configuration — a SandboxWarmPool
// spec, a rendered NetworkPolicy, or the migration framework's
// schema_migrations table — and asserts the platform detects or
// rejects the drift, then reverts the mutation and asserts recovery.
//
// The drift is injected with `kubectl` against the live cluster.
// Detection is asserted either at admission time (the pool-config
// validator rejects an invariant-violating SandboxWarmPool) or
// behaviorally (a NetworkPolicy edit changes effective connectivity,
// observed from a probe pod). Every test reverts its mutation in a
// t.Cleanup so the shared cluster is left in its rendered state.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// poolConfigValidatorWebhook is the ValidatingWebhookConfiguration that
// gates SandboxWarmPool / SandboxTemplate semantic budget invariants.
const poolConfigValidatorWebhook = "lenny-pool-config-validator"

// poolConfigValidatorDeployment runs the validator webhook backend.
const poolConfigValidatorDeployment = "lenny-pool-config-validator"

// agentNamespace is the namespace agent-scoped CRDs (SandboxWarmPool,
// SandboxClaim, Sandbox) live in on the e2e cluster.
const agentNamespace = "lenny-agents"

// spec: 12.8
// diagnosis: §12.8 / §4.6.2 pool-config drift was not rejected at
// admission. The lenny-pool-config-validator webhook gates the §4.6.2
// SandboxWarmPool budget invariants. The test creates a valid pool,
// then drifts its config with an UPDATE violating minWarm <= maxWarm.
// The webhook must reject the drift with INVALID_POOL_CONFIGURATION and
// leave the persisted spec unchanged; a later valid UPDATE is admitted.
// A failure means the validator admitted an invariant-violating config.
func TestPoolConfigDrift(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, poolConfigValidatorDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			poolConfigValidatorDeployment, deploymentReadyState(t, c, poolConfigValidatorDeployment))
	}
	policy, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", poolConfigValidatorWebhook,
		"-o", "jsonpath={.webhooks[0].failurePolicy}",
	)
	if err != nil || strings.TrimSpace(policy) != "Fail" {
		t.Skipf("precondition not met: %s webhook failurePolicy is %q, not Fail",
			poolConfigValidatorWebhook, strings.TrimSpace(policy))
	}

	// Create a valid SandboxWarmPool: minWarm <= maxWarm, both within
	// the CRD's non-negative bound. This is the known-good baseline the
	// drift mutates away from.
	const poolName = "chaos-drift-pool"
	validPool := warmPoolManifest(poolName, agentNamespace, 1, 3)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, validPool) })
	if out, err := c.ApplyStdin(t, validPool); err != nil {
		t.Fatalf("failed to create the valid baseline SandboxWarmPool: %v\n%s", err, out)
	}
	t.Logf("precondition: valid SandboxWarmPool %s/%s created (minWarm=1, maxWarm=3)",
		agentNamespace, poolName)

	// Inject the drift: UPDATE the pool to minWarm=9, maxWarm=3 — a
	// floor above the ceiling, which §4.6.2 makes unsatisfiable. The
	// validator must reject this UPDATE.
	driftedPool := warmPoolManifest(poolName, agentNamespace, 9, 3)
	out, err := c.ApplyStdin(t, driftedPool)
	if err == nil {
		t.Errorf("§4.6.2 violation: the pool-config validator admitted a SandboxWarmPool UPDATE with "+
			"minWarm=9 > maxWarm=3; the minWarm <= maxWarm invariant must be rejected.\noutput:\n%s", out)
	} else if !strings.Contains(out, "INVALID_POOL_CONFIGURATION") {
		t.Errorf("the drifted SandboxWarmPool UPDATE was rejected, but the message does not carry "+
			"INVALID_POOL_CONFIGURATION; the rejection may be unrelated to the invariant.\noutput:\n%s", out)
	} else {
		t.Logf("drift rejected: the validator returned INVALID_POOL_CONFIGURATION for minWarm > maxWarm")
	}

	// Assert: the persisted pool still carries the pre-drift values. A
	// rejected UPDATE must not partially apply.
	persisted, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxwarmpool", poolName,
		"-o", "jsonpath={.spec.minWarm}/{.spec.maxWarm}",
	)
	if err != nil {
		t.Fatalf("failed to read the SandboxWarmPool after the rejected drift: %v\n%s", err, persisted)
	}
	if strings.TrimSpace(persisted) != "1/3" {
		t.Errorf("after the rejected drift the SandboxWarmPool spec is minWarm/maxWarm=%s, expected 1/3; "+
			"a rejected UPDATE leaked into the persisted object", strings.TrimSpace(persisted))
	}

	// Assert recovery: a valid UPDATE (still minWarm <= maxWarm) is
	// admitted, proving the validator is not blanket-rejecting writes.
	recovered := warmPoolManifest(poolName, agentNamespace, 2, 5)
	if out, err := c.ApplyStdin(t, recovered); err != nil {
		t.Fatalf("a valid SandboxWarmPool UPDATE (minWarm=2, maxWarm=5) was rejected after the drift test: "+
			"%v\n%s", err, out)
	}
	persisted, _ = c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxwarmpool", poolName,
		"-o", "jsonpath={.spec.minWarm}/{.spec.maxWarm}",
	)
	if strings.TrimSpace(persisted) != "2/5" {
		t.Errorf("the valid recovery UPDATE did not persist; spec is minWarm/maxWarm=%s, expected 2/5",
			strings.TrimSpace(persisted))
	}
	t.Logf("recovery: a valid SandboxWarmPool UPDATE was admitted; pool-config drift verified end to end")
}

// spec: 12.8
// diagnosis: §12.8 NetworkPolicy config drift had no observable effect.
// Lenny runs no NetworkPolicy reconciler (§13.2 NET-022: the controller
// does not auto-patch NetworkPolicies), so the test exercises drift by
// behavior: it removes the bootstrap ingress rule from
// allow-gateway-ingress and asserts a bootstrap probe pod can no longer
// reach the gateway, then restores the policy and asserts connectivity
// returns. A failure means the edit had no effect or no recovery.
func TestNetworkPolicyConfigDrift(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-npdrift-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	const policyName = "allow-gateway-ingress"
	original := capturePolicyManifest(t, c, policyName)
	if original == "" {
		t.Skipf("precondition not met: NetworkPolicy %s is not present in lenny-system", policyName)
	}

	// Precondition: the bootstrap-labelled probe can reach the gateway,
	// so a later block is attributable to the injected drift.
	if p := curlGateway(t, c, probe, gatewayIP, "/healthz"); !p.ok(200) {
		t.Skipf("precondition not met: the bootstrap probe cannot reach the gateway before the drift "+
			"(curl exit %d, status %d); %s may not admit bootstrap pods", p.curlExit, p.statusCode, policyName)
	}
	t.Logf("precondition: the bootstrap probe reaches the gateway /healthz (200)")

	// Register the restore before mutating: re-applying the captured
	// manifest restores the rendered policy exactly, then kindnet is
	// reprogrammed so its per-node datapath matches the restored set.
	t.Cleanup(func() {
		if out, err := c.ApplyStdin(t, original); err != nil {
			t.Errorf("failed to restore NetworkPolicy %s: %v\n%s", policyName, err, out)
		}
		reprogramKindnet(t, c)
	})

	// Inject the drift: replace the policy with a copy that admits only
	// the agent-namespace ingress and drops every other from-clause —
	// in particular the bootstrap rule the probe depends on.
	drifted := narrowedGatewayIngressPolicy(policyName)
	if out, err := c.ApplyStdin(t, drifted); err != nil {
		t.Fatalf("failed to apply the drifted NetworkPolicy: %v\n%s", err, out)
	}
	t.Logf("injected: %s drifted to drop the bootstrap ingress rule", policyName)

	// Assert: the drift has an observable effect — the bootstrap probe
	// can no longer reach the gateway. The CNI takes a moment to apply
	// the policy change, so poll for the block.
	blocked := pollUntil(60*time.Second, 3*time.Second, func() bool {
		return curlGateway(t, c, probe, gatewayIP, "/healthz").curlExit != 0
	})
	if !blocked {
		t.Errorf("after dropping the bootstrap ingress rule the probe still reaches the gateway; "+
			"the NetworkPolicy drift had no observable effect (the CNI is not enforcing %s)", policyName)
	} else {
		t.Logf("drift detected: the bootstrap probe is blocked from the gateway after the policy edit")
	}

	// Restore the rendered policy (the t.Cleanup also restores).
	if out, err := c.ApplyStdin(t, original); err != nil {
		t.Fatalf("failed to restore NetworkPolicy %s: %v\n%s", policyName, err, out)
	}

	// Assert recovery: with the rendered policy back, the probe reaches
	// the gateway again.
	recovered := pollUntil(60*time.Second, 3*time.Second, func() bool {
		return curlGateway(t, c, probe, gatewayIP, "/healthz").ok(200)
	})
	if !recovered {
		t.Fatalf("the bootstrap probe did not regain gateway access within 60s after %s was restored",
			policyName)
	}
	t.Logf("recovery: %s restored, the bootstrap probe reaches the gateway again; "+
		"NetworkPolicy drift verified end to end", policyName)
}

// spec: 12.8
// diagnosis: §12.8 / §13.2 NetworkPolicy over-permissive drift was not
// detectable. §13.2 requires lenny-system NetworkPolicies to be
// least-privilege: no ingress from-clause uses a match-everything
// podSelector. The test patches allow-gateway-ingress to add an
// over-broad podSelector: {} clause, detects the drift by inspecting
// the live policy, then restores it and asserts the clause is gone. A
// failure means the drift was undetectable or the restore left it.
func TestNetworkPolicyDrift(t *testing.T) {
	// The over-permissive direction is asserted by policy inspection
	// rather than by connectivity: the chart renders gateway ingress and
	// egress allow-lists symmetrically, so every pod whose egress can
	// reach the gateway is already ingress-admitted, leaving an
	// ingress-only widening with no isolated connectivity signal.
	c := kind.InstallLenny(t)

	const policyName = "allow-gateway-ingress"
	original := capturePolicyManifest(t, c, policyName)
	if original == "" {
		t.Skipf("precondition not met: NetworkPolicy %s is not present in lenny-system", policyName)
	}

	// Precondition: the rendered policy is least-privilege — no ingress
	// from-clause uses an empty (match-everything) podSelector.
	if hasOverBroadIngressClause(t, c, policyName) {
		t.Skipf("precondition not met: %s already carries an over-broad ingress clause before the "+
			"injection; cannot demonstrate an over-permissive drift", policyName)
	}
	t.Logf("precondition: %s is least-privilege (no match-everything ingress clause)", policyName)

	// Register the restore before mutating. Reprogram kindnet after the
	// restore so its per-node datapath matches the restored policy set.
	t.Cleanup(func() {
		if out, err := c.ApplyStdin(t, original); err != nil {
			t.Errorf("failed to restore NetworkPolicy %s: %v\n%s", policyName, err, out)
		}
		reprogramKindnet(t, c)
	})

	// Inject the drift: replace the policy with one that adds an
	// over-broad `podSelector: {}` ingress clause admitting every pod
	// in lenny-system on port 8080.
	drifted := overBroadGatewayIngressPolicy(policyName)
	if out, err := c.ApplyStdin(t, drifted); err != nil {
		t.Fatalf("failed to apply the over-broad NetworkPolicy drift: %v\n%s", err, out)
	}
	t.Logf("injected: %s drifted with a match-everything ingress clause", policyName)

	// Assert: the drift is detectable by inspecting the live policy —
	// the over-broad clause the least-privilege baseline never contains
	// is now present.
	if !hasOverBroadIngressClause(t, c, policyName) {
		t.Errorf("the over-permissive drift on %s was not detectable: the live policy carries no "+
			"match-everything ingress clause after the injection", policyName)
	} else {
		t.Logf("drift detected: %s now carries an over-broad match-everything ingress clause", policyName)
	}

	// Restore the rendered least-privilege policy (the t.Cleanup also
	// restores).
	if out, err := c.ApplyStdin(t, original); err != nil {
		t.Fatalf("failed to restore NetworkPolicy %s: %v\n%s", policyName, err, out)
	}

	// Assert recovery: the over-broad clause is gone — the rendered
	// least-privilege baseline holds again.
	if hasOverBroadIngressClause(t, c, policyName) {
		t.Fatalf("%s still carries an over-broad ingress clause after the rendered policy was restored; "+
			"the over-permissive drift was not reverted", policyName)
	}
	t.Logf("recovery: %s restored to least-privilege, no match-everything clause; "+
		"over-permissive NetworkPolicy drift verified end to end", policyName)
}

// spec: 12.8
// diagnosis: §12.8 / §17.7 schema-migration dirty-flag handling did not
// hold. golang-migrate records a per-version dirty flag in
// schema_migrations and refuses to migrate a dirty database. The test
// runs the live migration framework against the e2e Postgres: it
// confirms the schema is clean, forces the dirty flag on, asserts a
// subsequent `lenny-migrate up` fails, then clears the flag per the
// §17.7 remediation and asserts `lenny-migrate up` succeeds again.
func TestSchemaMigrationDirtyFlag(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, postgresDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); the migration framework needs Postgres",
			postgresDeployment, deploymentReadyState(t, c, postgresDeployment))
	}

	// Resolve the Postgres pod IP. The one-shot migrate and psql pods
	// cannot resolve the lenny-postgres Service DNS name — the
	// lenny-system default-deny NetworkPolicy denies a throwaway pod's
	// egress to CoreDNS — so they connect to the pod IP.
	pgIP := dataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skipf("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	// Precondition: the live schema reports clean. `lenny-migrate
	// version` prints "version N (clean)" or "(dirty)". `lenny-migrate
	// up` is run first so the dirty-flag scenario has an applied schema
	// to target even on a cluster whose migrate Job has not run; up is
	// idempotent (golang-migrate ErrNoChange is treated as success).
	if upOut, upErr := runMigrateExpectErr(t, c, pgIP, "up"); upErr != nil {
		t.Skipf("precondition not met: `lenny-migrate up` failed against the live Postgres (%v); "+
			"cannot establish an applied schema for the dirty-flag scenario\n%s", upErr, upOut)
	}
	versionOut := runMigrate(t, c, pgIP, "version")
	if !strings.Contains(versionOut, "(clean)") {
		t.Skipf("precondition not met: `lenny-migrate version` does not report a clean schema "+
			"(output: %q); the dirty-flag scenario assumes a clean starting state", strings.TrimSpace(versionOut))
	}
	t.Logf("precondition: the live schema reports clean (%s)", strings.TrimSpace(versionOut))

	// Read the current applied version so the test can target the
	// dirty flag at the exact row golang-migrate tracks.
	version := parseMigrateVersion(versionOut)
	if version == "" {
		t.Fatalf("could not parse the applied migration version from %q", versionOut)
	}

	// Register the restore before injecting: clear the dirty flag so
	// the shared cluster's schema is left clean even on a mid-test
	// failure.
	t.Cleanup(func() {
		setMigrationDirty(t, c, pgIP, version, false)
	})

	// Inject: force dirty = true on the applied version row directly,
	// simulating a migration that started but did not complete.
	setMigrationDirty(t, c, pgIP, version, true)
	dirtyVersionOut := runMigrate(t, c, pgIP, "version")
	if !strings.Contains(dirtyVersionOut, "(dirty)") {
		t.Fatalf("after forcing the dirty flag, `lenny-migrate version` still does not report dirty "+
			"(output: %q); the injection did not take", strings.TrimSpace(dirtyVersionOut))
	}
	t.Logf("injected: schema_migrations version %s flagged dirty", version)

	// Assert: `lenny-migrate up` refuses a dirty database. golang-
	// migrate returns a "Dirty database version" error on Up against a
	// dirty schema; the lenny-migrate wrapper surfaces it as a non-zero
	// exit with the word "dirty" in the message.
	upOut, upErr := runMigrateExpectErr(t, c, pgIP, "up")
	if upErr == nil {
		t.Errorf("§17.7 violation: `lenny-migrate up` succeeded against a dirty database; "+
			"golang-migrate must refuse to migrate a dirty schema.\noutput:\n%s", upOut)
	} else if !strings.Contains(strings.ToLower(upOut), "dirty") {
		t.Errorf("`lenny-migrate up` failed against a dirty database, but the error does not mention "+
			"the dirty state; the failure may be unrelated.\noutput:\n%s", upOut)
	} else {
		t.Logf("dirty-flag detected: `lenny-migrate up` refused to migrate the dirty schema")
	}

	// Recover per §17.7 remediation step 2: clear the dirty flag.
	setMigrationDirty(t, c, pgIP, version, false)
	cleanVersionOut := runMigrate(t, c, pgIP, "version")
	if !strings.Contains(cleanVersionOut, "(clean)") {
		t.Fatalf("after clearing the dirty flag, `lenny-migrate version` still does not report clean "+
			"(output: %q)", strings.TrimSpace(cleanVersionOut))
	}

	// Assert recovery: `lenny-migrate up` succeeds again. The e2e
	// schema is already fully migrated, so `up` is a no-op (golang-
	// migrate ErrNoChange) that the wrapper treats as success.
	upOut, upErr = runMigrateExpectErr(t, c, pgIP, "up")
	if upErr != nil {
		t.Fatalf("`lenny-migrate up` still fails after the dirty flag was cleared: %v\n%s", upErr, upOut)
	}
	t.Logf("recovery: dirty flag cleared, `lenny-migrate up` succeeds again; " +
		"schema-migration dirty-flag handling verified end to end")
}

// warmPoolManifest renders a SandboxWarmPool manifest with the given
// minWarm/maxWarm. templateRef is required by the CRD; the referenced
// template need not exist for the pool-config validator (it inspects
// only the SandboxWarmPool spec, not the template).
func warmPoolManifest(name, ns string, minWarm, maxWarm int) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: SandboxWarmPool
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-pool-drift
spec:
  minWarm: %d
  maxWarm: %d
  templateRef: chaos-drift-template
`, name, ns, minWarm, maxWarm)
}

// capturePolicyManifest reads the named lenny-system NetworkPolicy as a
// re-appliable YAML manifest, stripping the server-managed metadata
// (resourceVersion, uid, creationTimestamp, status) so `kubectl apply`
// of the result restores the policy cleanly. Returns "" when the
// policy is absent.
func capturePolicyManifest(t *testing.T, c *kind.Cluster, name string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "networkpolicy", name, "-o", "yaml",
	)
	if err != nil {
		return ""
	}
	var b strings.Builder
	skipBlock := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		// Drop the server-managed metadata fields and the status block.
		if strings.HasPrefix(line, "  resourceVersion:") ||
			strings.HasPrefix(line, "  uid:") ||
			strings.HasPrefix(line, "  creationTimestamp:") ||
			strings.HasPrefix(line, "  generation:") ||
			strings.HasPrefix(line, "status:") {
			skipBlock = strings.HasPrefix(line, "status:")
			continue
		}
		// status: is the last top-level key; once seen, drop the rest.
		if skipBlock && (line == "" || strings.HasPrefix(line, " ")) {
			continue
		}
		skipBlock = false
		_ = trimmed
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// narrowedGatewayIngressPolicy renders an allow-gateway-ingress policy
// that admits only agent-namespace ingress on the control port. It
// omits the bootstrap ingress rule, so a bootstrap-labelled probe is
// no longer admitted — the drift the config-drift test detects.
func narrowedGatewayIngressPolicy(name string) string {
	return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
spec:
  podSelector:
    matchLabels:
      lenny.dev/component: gateway
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              lenny.dev/agent-namespace: "true"
      ports:
        - port: 50051
          protocol: TCP
`, name, lennySystemNamespace)
}

// overBroadGatewayIngressPolicy renders an allow-gateway-ingress policy
// that admits every pod in lenny-system on port 8080 — the
// over-permissive drift the network-drift test detects.
func overBroadGatewayIngressPolicy(name string) string {
	return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
spec:
  podSelector:
    matchLabels:
      lenny.dev/component: gateway
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector: {}
      ports:
        - port: 8080
          protocol: TCP
`, name, lennySystemNamespace)
}

// hasOverBroadIngressClause reports whether the named lenny-system
// NetworkPolicy has an ingress from-clause whose podSelector matches
// every pod (an empty matchLabels / matchExpressions selector). A
// least-privilege §13.2 policy never carries such a clause; the
// over-permissive drift test injects one and detects it here. The
// check counts ingress podSelectors with no matchLabels keys: kubectl
// jsonpath emits the matchLabels map's keys, so a clause with an empty
// podSelector contributes an empty line.
func hasOverBroadIngressClause(t *testing.T, c *kind.Cluster, name string) bool {
	t.Helper()
	// For each ingress[].from[] peer, print "P" for a podSelector
	// present and the count of its matchLabels keys. An empty-podSelector
	// peer prints "P0"; a scoped one prints "P1+".
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "networkpolicy", name,
		"-o", "jsonpath={range .spec.ingress[*].from[*]}"+
			"{.podSelector}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("failed to read NetworkPolicy %s ingress peers: %v\n%s", name, err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		peer := strings.TrimSpace(line)
		// An empty podSelector serializes to "{}" (or "map[]"); a scoped
		// one carries a matchLabels object. A peer with no podSelector
		// at all (namespaceSelector-only) serializes to the empty string.
		if peer == "{}" || peer == "map[]" {
			return true
		}
	}
	return false
}

// migrateImage is the lenny-migrate image kind-loaded onto the e2e
// cluster nodes; it runs the §17.7 golang-migrate framework. The image
// is built from the shared Dockerfile, so the binary is at
// /usr/local/bin/lenny and is the image ENTRYPOINT.
const migrateImage = "lenny-migrate:e2e"

// migrateDSN builds the Postgres DSN the one-shot migrate pods use. It
// targets the Postgres pod IP rather than the lenny-postgres Service
// DNS name: a throwaway pod's egress to CoreDNS is denied by the
// lenny-system default-deny NetworkPolicy, so DNS resolution would fail.
func migrateDSN(pgIP string) string {
	return fmt.Sprintf("postgres://lenny:lenny@%s:5432/lenny?sslmode=disable", pgIP)
}

// runMigrate runs `lenny-migrate <subcommand>` as a one-shot pod
// against the e2e Postgres and returns the combined output. It fails
// the test on a non-zero exit; callers that expect a failure use
// runMigrateExpectErr instead.
func runMigrate(t *testing.T, c *kind.Cluster, pgIP, subcommand string) string {
	t.Helper()
	out, err := runMigrateExpectErr(t, c, pgIP, subcommand)
	if err != nil {
		t.Fatalf("`lenny-migrate %s` failed: %v\n%s", subcommand, err, out)
	}
	return out
}

// runMigrateExpectErr runs `lenny-migrate <subcommand>` as a one-shot
// pod and returns the combined output and the run error without
// failing the test. The dirty-flag test uses it for the `up` call it
// expects to fail. The pod runs the image ENTRYPOINT
// (/usr/local/bin/lenny) with the subcommand args.
func runMigrateExpectErr(t *testing.T, c *kind.Cluster, pgIP, subcommand string) (string, error) {
	t.Helper()
	podName := "chaos-migrate-" + subcommand
	_, _ = c.KubectlOut(t, "-n", lennySystemNamespace, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	return c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "run", podName,
		"--image="+migrateImage, "--image-pull-policy=Never",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--command", "--",
		"/usr/local/bin/lenny", "--postgres-dsn", migrateDSN(pgIP), subcommand,
	)
}

// parseMigrateVersion extracts the integer version from `lenny-migrate
// version` output of the form "lenny-migrate: version 42 (clean)".
func parseMigrateVersion(out string) string {
	marker := "version "
	idx := strings.Index(out, marker)
	if idx < 0 {
		return ""
	}
	field := out[idx+len(marker):]
	end := strings.IndexFunc(field, func(r rune) bool { return r < '0' || r > '9' })
	if end <= 0 {
		return ""
	}
	return field[:end]
}

// setMigrationDirty forces the `dirty` column of the schema_migrations
// row for the given version. golang-migrate's pgx driver uses the
// default schema_migrations table. The UPDATE runs via psql in a
// one-shot postgres:16.4 pod connected to the Postgres pod IP (the
// postgres image carries a psql client; the lenny-migrate image is
// distroless and has none).
func setMigrationDirty(t *testing.T, c *kind.Cluster, pgIP, version string, dirty bool) {
	t.Helper()
	dirtyLiteral := "false"
	if dirty {
		dirtyLiteral = "true"
	}
	sql := fmt.Sprintf("UPDATE schema_migrations SET dirty = %s WHERE version = %s;",
		dirtyLiteral, version)
	runPsql(t, c, pgIP, "chaos-migrate-dirty", sql)
}
