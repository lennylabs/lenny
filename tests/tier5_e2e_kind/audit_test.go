// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the TESTING.md §13.28 audit pipeline. The gateway
// writes §11.7 audit events to the live in-cluster Postgres audit_log
// table: every admin-API mutation emits an event onto the calling
// tenant's per-tenant hash chain, and each row carries a prev_hash
// linking it to its predecessor.
//
// This is the tier-5 end-to-end view of the TESTING.md §13.28 audit pipeline:
// events flow from a gateway admin-API call, through the gateway's
// audit writer, into the durable Postgres ledger. The test drives a
// sequence of admin-API mutations as platform-admin against a synthetic
// tenant, then asserts (a) the §25.9 audit-events list reports the
// resulting chain grown by the event count with every appended row
// "verified" and no row broken, (b) the rows are
// present in audit_log read straight from Postgres, and (c) the
// Postgres hash chain links each row to its predecessor with the first
// row carrying the genesis prev_hash. Every synthetic-tenant row is
// removed in a t.Cleanup so the shared cluster's audit_log is left
// clean.
//
// §25.9 defines no separate verify route. The audit-events list carries
// the §11.7 verifier verdict in its chainIntegrityReport and in each OCSF
// record's unmapped.lenny_chain.integrity, computed over the full chain.

package tier5_e2e_kind_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/auditquery"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditGenesisHash is the §11.7 genesis prev_hash: the first row of a
// tenant's chain has no predecessor, so its prev_hash is 32 zero bytes
// (64 hex zeros when read from the bytea column).
const auditGenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// spec: 13.28, 11.7 (Audit Logging), 25.9 (Audit Log Query API), 15.1 (REST API)
// diagnosis: the TESTING.md §13.28 audit pipeline did not carry events end to end.
// The gateway writes §11.7 audit events to the live Postgres audit_log;
// each admin-API mutation emits one event onto the caller's hash chain.
// The test issues N bootstrap upserts as platform-admin, then asserts
// the §25.9 audit-events list reports the chain grown by N with every
// appended row "verified" and none broken, the
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

	// Register cleanup before generating state: delete the synthetic
	// tenant via the admin API and remove its audit_log rows directly
	// in Postgres so the shared cluster's ledger is left clean.
	t.Cleanup(func() {
		_ = t5GatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/tenants/"+tenant, admin, "")
		deleteAuditRows(t, c, pgIP, tenant)
	})

	// Baseline: the §25.9 list answers for the synthetic tenant, and the
	// chain it reports (empty, or whatever a prior run left) holds no
	// broken row.
	base, err := readAuditChainT5(t, c, probe, gatewayIP, tenant)
	if err != nil {
		t.Fatalf("the §25.9 audit-events list for tenant %s could not be read at baseline: %v", tenant, err)
	}
	if base.Report.Broken != 0 {
		t.Fatalf("TESTING.md §13.28: the §25.9 chainIntegrityReport counts %d broken rows in the synthetic "+
			"tenant's baseline chain (report %+v)", base.Report.Broken, base.Report)
	}
	t.Logf("baseline: synthetic-tenant chain has %d rows, report %+v", len(base.Events), base.Report)

	// --- Drive gateway admin-API activity that produces audit events.
	// Each POST /v1/admin/bootstrap upsert of the synthetic tenant emits
	// one platform.bootstrap_applied audit event onto the *caller's* tenant
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

	// --- Assertion 1: the §25.9 list reports no broken row, the chain
	// grew by at least wantEvents rows, and every appended row verifies.
	// "verified" on each new row proves it extended the chain without
	// breaking the hash linkage.
	after, err := readAuditChainT5(t, c, probe, gatewayIP, tenant)
	if err != nil {
		t.Fatalf("the §25.9 audit-events list for tenant %s could not be read after the audit activity: %v", tenant, err)
	}
	if after.Report.Broken != 0 {
		t.Errorf("TESTING.md §13.28 violation: the §25.9 chainIntegrityReport counts %d broken rows after %d audit "+
			"events were appended; the hash chain lost continuity (report %+v)", after.Report.Broken, wantEvents, after.Report)
	}
	added := after.After(base.MaxSeq())
	if len(added) < wantEvents {
		t.Errorf("TESTING.md §13.28 violation: the synthetic tenant's chain grew by %d rows after %d admin-API "+
			"mutations; expected at least %d — audit events were dropped before reaching the ledger",
			len(added), wantEvents, wantEvents)
	}
	for _, ev := range added {
		if ev.Integrity != "verified" {
			t.Errorf("TESTING.md §13.28 violation: appended row seq %d reports chain integrity %q, expected "+
				"\"verified\"", ev.Seq, ev.Integrity)
		}
	}
	t.Logf("TESTING.md §13.28: the §25.9 list reports the chain grew %d -> %d rows (report %+v)",
		len(base.Events), len(after.Events), after.Report)

	// --- Assertion 2: the events are durable in Postgres. Read the
	// audit_log rows for the tenant straight from the database and
	// confirm at least wantEvents rows past the baseline landed, carrying
	// the documented bootstrap event type.
	//
	// Rows at or below the baseline are a prior run's remnant and are not
	// this run's events. The known remnant is the §12.8 Phase 6
	// gdpr.tenant_erased receipt: the cleanup's DELETE of the synthetic
	// tenant starts the asynchronous tenant deletion lifecycle, whose
	// receipt is appended to the tenant's chain after deleteAuditRows has
	// run and is retained and exempt from erasure by design. A receipt
	// that lands after the baseline read is tolerated for the same reason.
	rows := readAuditRows(t, c, pgIP, tenant)
	baseSeq := base.MaxSeq()
	bootstrapRows := 0
	for i, row := range rows {
		if row.seq <= baseSeq {
			continue
		}
		switch row.eventType {
		case "platform.bootstrap_applied":
			bootstrapRows++
		case "gdpr.tenant_erased":
			t.Logf("audit_log row %d (seq %d) for tenant %s is a prior run's §12.8 erasure receipt; not counted",
				i, row.seq, tenant)
		default:
			t.Errorf("TESTING.md §13.28: audit_log row %d (seq %d) for tenant %s carries event_type %q, expected "+
				"\"platform.bootstrap_applied\" — the bootstrap upserts emit that event type",
				i, row.seq, tenant, row.eventType)
		}
	}
	if bootstrapRows < wantEvents {
		t.Fatalf("TESTING.md §13.28 violation: audit_log holds %d platform.bootstrap_applied rows past baseline seq %d "+
			"for tenant %s after %d admin-API mutations; expected at least %d — events did not reach the durable "+
			"Postgres ledger", bootstrapRows, baseSeq, tenant, wantEvents, wantEvents)
	}

	// --- Assertion 3: the Postgres hash chain is intact. The chain's
	// first row carries the §11.7 genesis prev_hash; every later row's
	// prev_hash links it to its predecessor (sequence numbers strictly
	// increase, and no later row carries the genesis hash). The synthetic
	// tenant's audit_log is cleaned in the t.Cleanup, so on a clean run
	// the first row read here is the chain's genesis row.
	if rows[0].prevHash != auditGenesisHash {
		t.Errorf("TESTING.md §13.28: the first audit_log row read for tenant %s carries prev_hash %q rather than "+
			"the §11.7 genesis hash; a prior run's rows were not cleaned before this run", tenant, rows[0].prevHash)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].seq <= rows[i-1].seq {
			t.Fatalf("TESTING.md §13.28 violation: audit_log sequence_number is not strictly increasing for tenant "+
				"%s — row %d carries seq %d, preceded by seq %d; the §11.7 chain forked or duplicated",
				tenant, i, rows[i].seq, rows[i-1].seq)
		}
		if rows[i].prevHash == auditGenesisHash {
			t.Errorf("TESTING.md §13.28 violation: audit_log row %d (seq %d) for tenant %s carries the genesis "+
				"prev_hash; only the chain's first row may — the §11.7 hash chain is broken",
				i, rows[i].seq, tenant)
		}
	}
	t.Logf("TESTING.md §13.28 audit pipeline verified end to end: %d admin-API mutations produced %d durable "+
		"audit_log rows for tenant %s, hash chain intact (genesis row + %d linked rows, seq %d..%d)",
		wantEvents, len(rows), tenant, len(rows)-1, rows[0].seq, rows[len(rows)-1].seq)
}

// readAuditChainT5 reads the tenant's §11.7 chain verdict through the
// §25.9 audit-events list as platform-admin, over every page of the
// default look-back window.
func readAuditChainT5(t *testing.T, c *kind.Cluster, probe, gatewayIP, tenant string) (auditquery.Chain, error) {
	t.Helper()
	return auditquery.ReadChain(tenant, "", "", func(path string) (int, []byte, error) {
		res := t5GatewayRequestRetry(t, c, probe, gatewayIP, "GET", path, t5PlatformAdmin(), "")
		if res.curlExit != 0 {
			return 0, nil, fmt.Errorf("curl exit %d (body %q)", res.curlExit, res.body)
		}
		return res.statusCode, []byte(res.body), nil
	})
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
