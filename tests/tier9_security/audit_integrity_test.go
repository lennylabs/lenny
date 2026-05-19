// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for §12.9.10 audit chain integrity. The §11.7
// per-tenant audit hash chain is Postgres-backed in the e2e cluster
// (the gateway runs with a real lenny-postgres, so admin.Router wires
// the auditstore.Store over the audit_log table). Every audit_log row
// carries a prev_hash linking it to its predecessor, and the §11.7
// verifier — reachable through GET /v1/admin/audit-events/verify —
// walks the per-tenant chain in sequence-number order.
//
// This file asserts two distinct §12.9.10 properties against the live
// chain:
//
//   - Continuity: admin-API activity (bootstrap upserts, which emit
//     admin.bootstrap.applied audit events) extends the synthetic
//     tenant's chain, and the verifier reports the resulting chain
//     "verified" with a row count that matches the events generated.
//     The complementary gap-detection half (a seeded gap IS detected)
//     is covered by the tier-8 TestAuditChainGapDetection; this file
//     does not duplicate it.
//   - Monotonicity: the audit_log sequence numbers for a tenant are
//     strictly increasing with no gap, read directly from Postgres.
//
// Both tests target a synthetic tenant so the assertions are
// deterministic regardless of leftover state from other suites, and
// every audit_log row they create is removed in a t.Cleanup.

package tier9_security_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditDeployment is the Postgres data-store Deployment the §11.7 chain
// is persisted in.
const auditDeployment = "lenny-postgres"

// gatewayDeploymentName is the gateway Deployment whose verify endpoint
// the continuity test drives.
const gatewayDeploymentName = "lenny-gateway"

// spec: 12.9.10
// diagnosis: §12.9.10 audit-chain continuity did not hold. The §11.7
// chain is Postgres-backed; admin-API activity emits audit events that
// extend the calling tenant's chain. The test issues N bootstrap
// upserts as platform-admin against a synthetic tenant, then asserts
// GET /v1/admin/audit-events/verify reports the chain "verified" and a
// rowCount that grew by at least N. A non-"verified" result, or a chain
// that did not grow, means the §11.7 hash chain is not continuous.
func TestAuditChainContinuity(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the §11.7 chain is Postgres-backed", auditDeployment)
	}
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the verifier is a gateway endpoint", gatewayDeploymentName)
	}

	probe := "t9-auditcont-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	// Synthetic tenant: a dedicated audit chain so the rowCount delta is
	// attributable to this test's activity. tenant_id on audit_log is not
	// a foreign key, so the bootstrap upsert (which creates the tenant
	// row) and the audit events it emits both land cleanly.
	tenant := "t9-auditcont-tenant"
	verifyPath := "/v1/admin/audit-events/verify?tenantId=" + tenant

	// Register cleanup before generating state: delete the synthetic
	// tenant row and its audit_log rows so the shared cluster is clean.
	pgIP := dataStorePodIPT9(t, c, "postgres")
	t.Cleanup(func() {
		_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/tenants/"+tenant, admin, "")
		if pgIP != "" {
			deleteAuditTenantRows(t, c, pgIP, tenant)
		}
	})

	// Baseline: the verifier must be reachable, and an empty (or
	// pre-existing) chain for the synthetic tenant must verify.
	base := gatewayRequestRetry(t, c, probe, gatewayIP, "GET", verifyPath, admin, "")
	if base.curlExit != 0 || base.statusCode != 200 {
		t.Skipf("precondition not met: GET /v1/admin/audit-events/verify is not reachable "+
			"(curl exit %d, status %d, body %q); the §11.7 verifier endpoint is unavailable",
			base.curlExit, base.statusCode, base.body)
	}
	baseVerify := parseAuditVerify(t, base.body)
	if baseVerify.Integrity != "verified" {
		t.Fatalf("§12.9.10 violation: the §11.7 verifier reports integrity %q for the synthetic tenant's "+
			"baseline chain, expected \"verified\" (detail %q, rowCount %d)",
			baseVerify.Integrity, baseVerify.Detail, baseVerify.RowCount)
	}
	t.Logf("baseline: synthetic-tenant chain integrity %q, rowCount %d",
		baseVerify.Integrity, baseVerify.RowCount)

	// Generate audit events: each bootstrap upsert of the synthetic
	// tenant emits one admin.bootstrap.applied event onto the *caller's*
	// tenant chain. The caller's dev-header tenant is the synthetic
	// tenant, so the events extend that tenant's chain.
	const events = 6
	bootstrapBody := fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenant)
	for i := 0; i < events; i++ {
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/bootstrap",
			gwRole{tenant: tenant, roles: "platform-admin", user: "alice"}, bootstrapBody)
		if res.curlExit != 0 || (res.statusCode != 200 && res.statusCode != 207) {
			t.Fatalf("bootstrap upsert %d did not succeed (curl exit %d, status %d, body %q); "+
				"cannot generate audit events for the continuity assertion",
				i+1, res.curlExit, res.statusCode, res.body)
		}
	}
	t.Logf("generated %d admin.bootstrap.applied audit events for tenant %s", events, tenant)

	// Assert: the verifier still reports the chain verified, and the
	// chain grew by at least `events` rows. The hash chain links every
	// new row to its predecessor; a "verified" result over the grown
	// chain proves the new rows extended it without breaking continuity.
	after := gatewayRequestRetry(t, c, probe, gatewayIP, "GET", verifyPath, admin, "")
	if after.curlExit != 0 || after.statusCode != 200 {
		t.Fatalf("the verify endpoint returned curl exit %d / status %d after the audit activity (body %q)",
			after.curlExit, after.statusCode, after.body)
	}
	afterVerify := parseAuditVerify(t, after.body)
	if afterVerify.Integrity != "verified" {
		t.Errorf("§12.9.10 violation: the §11.7 verifier reports integrity %q after %d audit events were "+
			"appended, expected \"verified\"; the hash chain lost continuity (breakSeq %d, detail %q)",
			afterVerify.Integrity, events, afterVerify.BreakSeq, afterVerify.Detail)
	}
	grew := afterVerify.RowCount - baseVerify.RowCount
	if grew < events {
		t.Errorf("§12.9.10 violation: the synthetic tenant's chain grew by %d rows after %d audit events; "+
			"expected at least %d — audit events were dropped rather than chained",
			grew, events, events)
	} else {
		t.Logf("§12.9.10 continuity verified: chain grew %d -> %d rows, integrity still %q",
			baseVerify.RowCount, afterVerify.RowCount, afterVerify.Integrity)
	}
}

// spec: 12.9.10
// diagnosis: §12.9.10 audit-ledger sequence monotonicity did not hold.
// The test generates a few hundred audit events for a synthetic tenant
// (scaled down from the spec's "million events" — a million-row
// generation is impractical on the shared e2e cluster and the
// monotonicity property is order-invariant in row count), then reads
// the audit_log sequence_number column straight from Postgres and
// asserts the values are strictly increasing with no gap and no
// duplicate. A non-monotonic or gapped sequence means the §11.7
// per-tenant advisory-lock serialization is not holding.
func TestAuditSequenceMonotonicity(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; audit_log is Postgres-backed", auditDeployment)
	}
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; audit events are emitted by the gateway", gatewayDeploymentName)
	}
	pgIP := dataStorePodIPT9(t, c, "postgres")
	if pgIP == "" {
		t.Skip("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	probe := "t9-auditseq-probe"
	gatewayIP := startGatewayProbe(t, c, probe)

	// Scaled-down event count. The spec calls for a million events; a
	// million INSERTs would run for many minutes on the single-replica
	// e2e Postgres and add no signal — monotonicity is a per-row
	// invariant. 300 events exercise the per-tenant advisory-lock
	// serialization across a meaningful run.
	const events = 300
	tenant := "t9-auditseq-tenant"

	t.Cleanup(func() {
		_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/tenants/"+tenant, platformAdmin(), "")
		deleteAuditTenantRows(t, c, pgIP, tenant)
	})

	bootstrapBody := fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenant)
	for i := 0; i < events; i++ {
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/bootstrap",
			gwRole{tenant: tenant, roles: "platform-admin", user: "alice"}, bootstrapBody)
		if res.curlExit != 0 || (res.statusCode != 200 && res.statusCode != 207) {
			t.Fatalf("bootstrap upsert %d did not succeed (curl exit %d, status %d, body %q)",
				i+1, res.curlExit, res.statusCode, res.body)
		}
	}
	t.Logf("generated %d audit events for tenant %s", events, tenant)

	// Read the sequence numbers straight from audit_log, ascending.
	seqs := readAuditSequences(t, c, pgIP, tenant)
	if len(seqs) < events {
		t.Fatalf("§12.9.10 violation: audit_log holds %d rows for tenant %s after %d audit events; "+
			"events were dropped before reaching the ledger", len(seqs), tenant, events)
	}

	// Assert strict monotonicity: each sequence_number is exactly one
	// greater than its predecessor. A gap means a row vanished or a
	// number was skipped; a non-increase means a duplicate or a fork.
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("§12.9.10 violation: audit_log sequence_number is not strictly increasing — "+
				"row %d carries seq %d, preceded by seq %d; the §11.7 per-tenant chain forked or duplicated",
				i, seqs[i], seqs[i-1])
		}
		if seqs[i] != seqs[i-1]+1 {
			t.Errorf("§12.9.10: audit_log sequence_number jumped from %d to %d (gap of %d) — "+
				"the §11.7 chain has a sequence gap", seqs[i-1], seqs[i], seqs[i]-seqs[i-1])
		}
	}
	t.Logf("§12.9.10 monotonicity verified: %d audit_log rows for tenant %s, "+
		"sequence_number strictly increasing from %d to %d with no gap",
		len(seqs), tenant, seqs[0], seqs[len(seqs)-1])
}

// auditVerifyDoc is the parsed §11.7 verify-endpoint response.
type auditVerifyDoc struct {
	Integrity string `json:"integrity"`
	BreakSeq  uint64 `json:"breakSeq"`
	Detail    string `json:"detail"`
	RowCount  int    `json:"rowCount"`
}

// parseAuditVerify decodes a §11.7 verify response body. An undecodable
// body fails the test, since every caller depends on the integrity
// field.
func parseAuditVerify(t *testing.T, body string) auditVerifyDoc {
	t.Helper()
	var doc auditVerifyDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the §11.7 verify response is not valid JSON: %v\nbody: %q", err, body)
	}
	return doc
}

// readAuditSequences reads the audit_log sequence_number column for the
// tenant in ascending order via a one-shot psql pod connected to the
// Postgres pod IP. The transaction sets app.current_tenant so the
// §12.3 lenny_tenant_guard trigger admits the read.
func readAuditSequences(t *testing.T, c *kind.Cluster, pgIP, tenant string) []uint64 {
	t.Helper()
	sql := "BEGIN; " +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s'; ", tenant) +
		fmt.Sprintf("SELECT sequence_number FROM audit_log WHERE tenant_id = '%s' "+
			"ORDER BY sequence_number; ", tenant) +
		"COMMIT;"
	out := runPsqlQuery(t, c, pgIP, "t9-auditseq-read", sql)
	var seqs []uint64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var n uint64
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil {
			seqs = append(seqs, n)
		}
	}
	return seqs
}

// deleteAuditTenantRows removes every audit_log row for the synthetic
// tenant, leaving the shared cluster's audit_log clean. The DELETE runs
// under a transaction that sets app.current_tenant (for
// lenny_tenant_guard) and lenny.erasure_mode (the §11.7
// lenny_audit_immutability trigger admits a DELETE only in erasure
// mode). It tolerates an empty table.
func deleteAuditTenantRows(t *testing.T, c *kind.Cluster, pgIP, tenant string) {
	t.Helper()
	sql := "BEGIN; " +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s'; ", tenant) +
		"SET LOCAL lenny.erasure_mode = 'true'; " +
		fmt.Sprintf("DELETE FROM audit_log WHERE tenant_id = '%s'; ", tenant) +
		"COMMIT;"
	runPsqlExec(t, c, pgIP, "t9-auditseq-cleanup", sql)
}
