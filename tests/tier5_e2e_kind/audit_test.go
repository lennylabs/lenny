// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §13.28 audit pipeline. The gateway
// writes §11.7 audit events to the live in-cluster Postgres audit_log
// table: every admin-API mutation emits an event onto the calling
// tenant's per-tenant hash chain, and each row carries a prev_hash
// linking it to its predecessor.
//
// This is the tier-5 end-to-end view of the §13.28 audit pipeline:
// events flow from a gateway admin-API call, through the gateway's
// audit writer, into the durable Postgres ledger. The test drives a
// sequence of admin-API mutations as platform-admin against a synthetic
// tenant, then asserts (a) the §11.7 verifier reports the resulting
// chain "verified" and grown by the event count, (b) the rows are
// present in audit_log read straight from Postgres, and (c) the
// Postgres hash chain links each row to its predecessor with the first
// row carrying the genesis prev_hash. Every synthetic-tenant row is
// removed in a t.Cleanup so the shared cluster's audit_log is left
// clean.

package tier5_e2e_kind_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditGenesisHash is the §11.7 genesis prev_hash: the first row of a
// tenant's chain has no predecessor, so its prev_hash is 32 zero bytes
// (64 hex zeros when read from the bytea column).
const auditGenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// spec: 13.28
// diagnosis: the §13.28 audit pipeline did not carry events end to end.
// The gateway writes §11.7 audit events to the live Postgres audit_log;
// each admin-API mutation emits one event onto the caller's hash chain.
// The test issues N bootstrap upserts as platform-admin, then asserts
// the §11.7 verifier reports the chain "verified" and grown by N, the
// rows are present in audit_log read from Postgres, and the Postgres
// hash chain links each row to its predecessor. A non-verified chain, a
// missing row, or a broken prev_hash link means audit events were
// dropped or the ledger is not durable.
func TestAuditPipeline(t *testing.T) {
	c := kind.InstallLenny(t)

	if !t5DeploymentReady(t, c, "lenny-postgres") {
		t.Skip("precondition not met: lenny-postgres is not Ready; the §11.7 audit ledger is Postgres-backed")
	}
	if !t5DeploymentReady(t, c, "lenny-gateway") {
		t.Skip("precondition not met: lenny-gateway is not Ready; audit events are emitted by the gateway")
	}
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	probe := "t5-audit-probe"
	gatewayIP := t5StartGatewayProbe(t, c, probe)
	admin := t5PlatformAdmin()

	// Synthetic tenant: a dedicated audit chain so the row-count delta
	// is attributable to this test's activity. audit_log.tenant_id is
	// not a foreign key, so the synthetic tenant row and the events it
	// emits both land cleanly.
	tenant := "t5-audit-tenant"
	verifyPath := "/v1/admin/audit-events/verify?tenantId=" + tenant

	// Register cleanup before generating state: delete the synthetic
	// tenant via the admin API and remove its audit_log rows directly
	// in Postgres so the shared cluster's ledger is left clean.
	t.Cleanup(func() {
		_ = t5GatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/tenants/"+tenant, admin, "")
		deleteAuditRows(t, c, pgIP, tenant)
	})

	// Precondition: the §11.7 verifier is reachable and the synthetic
	// tenant's baseline chain (empty, or whatever a prior run left)
	// verifies.
	base := t5GatewayRequestRetry(t, c, probe, gatewayIP, "GET", verifyPath, admin, "")
	if base.curlExit != 0 || base.statusCode != 200 {
		t.Skipf("precondition not met: GET /v1/admin/audit-events/verify is not reachable "+
			"(curl exit %d, status %d, body %q); the §11.7 verifier endpoint is unavailable",
			base.curlExit, base.statusCode, base.body)
	}
	baseVerify := parseAuditVerifyT5(t, base.body)
	if baseVerify.Integrity != "verified" {
		t.Fatalf("§13.28: the §11.7 verifier reports integrity %q for the synthetic tenant's baseline "+
			"chain, expected \"verified\" (rowCount %d)", baseVerify.Integrity, baseVerify.RowCount)
	}
	t.Logf("baseline: synthetic-tenant chain integrity %q, rowCount %d",
		baseVerify.Integrity, baseVerify.RowCount)

	// --- Drive gateway admin-API activity that produces audit events.
	// Each POST /v1/admin/bootstrap upsert of the synthetic tenant emits
	// one admin.bootstrap.applied audit event onto the *caller's* tenant
	// chain. The caller's dev-header tenant is the synthetic tenant, so
	// every event extends that tenant's chain. Bootstrap is idempotent
	// (upsert by id), so the run does not depend on the synthetic tenant
	// being absent — a soft-deleted tenant row from a prior run does not
	// turn the call into a conflict.
	callerRole := t5Role{tenant: tenant, roles: "platform-admin", user: "alice"}
	const wantEvents = 6
	bootstrapBody := fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenant)
	for i := 0; i < wantEvents; i++ {
		res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/bootstrap",
			callerRole, bootstrapBody)
		if res.curlExit != 0 || (res.statusCode != 200 && res.statusCode != 207) {
			t.Fatalf("bootstrap upsert %d did not succeed (curl exit %d, status %d, body %q); "+
				"cannot generate the audit events", i+1, res.curlExit, res.statusCode, res.body)
		}
	}
	t.Logf("drove %d admin-API mutations against tenant %s (bootstrap upserts)", wantEvents, tenant)

	// --- Assertion 1: the §11.7 verifier reports the chain still
	// verified and grown by at least wantEvents rows. A "verified"
	// result over the grown chain proves the new rows extended it
	// without breaking the hash linkage.
	after := t5GatewayRequestRetry(t, c, probe, gatewayIP, "GET", verifyPath, admin, "")
	if after.curlExit != 0 || after.statusCode != 200 {
		t.Fatalf("the verify endpoint returned curl exit %d / status %d after the audit activity (body %q)",
			after.curlExit, after.statusCode, after.body)
	}
	afterVerify := parseAuditVerifyT5(t, after.body)
	if afterVerify.Integrity != "verified" {
		t.Errorf("§13.28 violation: the §11.7 verifier reports integrity %q after %d audit events were "+
			"appended, expected \"verified\"; the hash chain lost continuity (detail %q)",
			afterVerify.Integrity, wantEvents, afterVerify.Detail)
	}
	grew := afterVerify.RowCount - baseVerify.RowCount
	if grew < wantEvents {
		t.Errorf("§13.28 violation: the synthetic tenant's chain grew by %d rows after %d admin-API "+
			"mutations; expected at least %d — audit events were dropped before reaching the ledger",
			grew, wantEvents, wantEvents)
	} else {
		t.Logf("§13.28: §11.7 verifier reports the chain grew %d -> %d rows, integrity still %q",
			baseVerify.RowCount, afterVerify.RowCount, afterVerify.Integrity)
	}

	// --- Assertion 2: the events are durable in Postgres. Read the
	// audit_log rows for the tenant straight from the database and
	// confirm at least wantEvents rows landed, carrying the documented
	// bootstrap event type.
	rows := readAuditRows(t, c, pgIP, tenant)
	if len(rows) < wantEvents {
		t.Fatalf("§13.28 violation: audit_log holds %d rows for tenant %s after %d admin-API mutations; "+
			"expected at least %d — events did not reach the durable Postgres ledger",
			len(rows), tenant, wantEvents, wantEvents)
	}
	for i, row := range rows {
		if row.eventType != "admin.bootstrap.applied" {
			t.Errorf("§13.28: audit_log row %d for tenant %s carries event_type %q, expected "+
				"\"admin.bootstrap.applied\" — the bootstrap upserts emit that event type",
				i, tenant, row.eventType)
		}
	}

	// --- Assertion 3: the Postgres hash chain is intact. The chain's
	// first row carries the §11.7 genesis prev_hash; every later row's
	// prev_hash links it to its predecessor (sequence numbers strictly
	// increase, and no later row carries the genesis hash). The synthetic
	// tenant's audit_log is cleaned in the t.Cleanup, so on a clean run
	// the first row read here is the chain's genesis row.
	if rows[0].prevHash != auditGenesisHash {
		t.Errorf("§13.28: the first audit_log row read for tenant %s carries prev_hash %q rather than "+
			"the §11.7 genesis hash; a prior run's rows were not cleaned before this run", tenant, rows[0].prevHash)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].seq <= rows[i-1].seq {
			t.Fatalf("§13.28 violation: audit_log sequence_number is not strictly increasing for tenant "+
				"%s — row %d carries seq %d, preceded by seq %d; the §11.7 chain forked or duplicated",
				tenant, i, rows[i].seq, rows[i-1].seq)
		}
		if rows[i].prevHash == auditGenesisHash {
			t.Errorf("§13.28 violation: audit_log row %d (seq %d) for tenant %s carries the genesis "+
				"prev_hash; only the chain's first row may — the §11.7 hash chain is broken",
				i, rows[i].seq, tenant)
		}
	}
	t.Logf("§13.28 audit pipeline verified end to end: %d admin-API mutations produced %d durable "+
		"audit_log rows for tenant %s, hash chain intact (genesis row + %d linked rows, seq %d..%d)",
		wantEvents, len(rows), tenant, len(rows)-1, rows[0].seq, rows[len(rows)-1].seq)
}

// auditVerifyDocT5 is the parsed §11.7 verify-endpoint response.
type auditVerifyDocT5 struct {
	Integrity string `json:"integrity"`
	Detail    string `json:"detail"`
	RowCount  int    `json:"rowCount"`
}

// parseAuditVerifyT5 decodes a §11.7 verify response body. An
// undecodable body fails the test, since every caller depends on the
// integrity field.
func parseAuditVerifyT5(t *testing.T, body string) auditVerifyDocT5 {
	t.Helper()
	var doc auditVerifyDocT5
	t5DecodeJSON(t, body, &doc)
	return doc
}

// auditRow is one audit_log row read directly from Postgres.
type auditRow struct {
	seq       uint64
	eventType string
	prevHash  string
}

// readAuditRows reads the audit_log rows for the tenant in ascending
// sequence order via a one-shot psql pod connected to the Postgres pod
// IP. The transaction sets app.current_tenant so the §12.3
// lenny_tenant_guard trigger admits the read. prev_hash is a bytea
// column, so it is hex-encoded to a string.
func readAuditRows(t *testing.T, c *kind.Cluster, pgIP, tenant string) []auditRow {
	t.Helper()
	sql := "BEGIN; " +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s'; ", tenant) +
		"SELECT sequence_number || '|' || event_type || '|' || encode(prev_hash, 'hex') " +
		fmt.Sprintf("FROM audit_log WHERE tenant_id = '%s' ORDER BY sequence_number; ", tenant) +
		"COMMIT;"
	out := t5RunPsqlQuery(t, c, pgIP, "t5-audit-read", sql)
	var rows []auditRow
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "BEGIN" || line == "SET" || line == "COMMIT" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		var seq uint64
		if _, err := fmt.Sscanf(parts[0], "%d", &seq); err != nil {
			continue
		}
		rows = append(rows, auditRow{seq: seq, eventType: parts[1], prevHash: parts[2]})
	}
	return rows
}

// deleteAuditRows removes every audit_log row for the synthetic tenant,
// leaving the shared cluster's audit_log clean. The DELETE runs under a
// transaction that sets app.current_tenant (for lenny_tenant_guard) and
// lenny.erasure_mode (the §11.7 lenny_audit_immutability trigger admits
// a DELETE only in erasure mode). It tolerates an empty table.
func deleteAuditRows(t *testing.T, c *kind.Cluster, pgIP, tenant string) {
	t.Helper()
	sql := "BEGIN; " +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s'; ", tenant) +
		"SET LOCAL lenny.erasure_mode = 'true'; " +
		fmt.Sprintf("DELETE FROM audit_log WHERE tenant_id = '%s'; ", tenant) +
		"COMMIT;"
	t5RunPsqlExec(t, c, pgIP, "t5-audit-cleanup", sql)
}
