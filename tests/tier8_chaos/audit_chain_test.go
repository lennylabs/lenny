// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for §11.7 audit hash-chain gap detection. The audit
// chain is Postgres-backed: every audit_log row carries a prev_hash
// linking it to its predecessor, and the gateway's §11.7 verifier walks
// the per-tenant chain in sequence-number order. §25.9 defines no
// separate verify route: the audit-events list carries the verdict in
// its chainIntegrityReport and in each OCSF record's
// unmapped.lenny_chain.integrity, computed over the full chain.
//
// The test seeds a synthetic tenant's audit_log with a valid chain
// (computed with the canonical pkg/audit hash construction so the
// verifier accepts it), confirms the list reports every row verified,
// then injects a gap by deleting a middle row directly in Postgres and
// asserts the list reports the row after the gap broken. The injected
// rows are removed in a t.Cleanup so the shared cluster's audit_log
// carries no leftover synthetic tenant.
//
// The fault writes to the cluster's Postgres directly (lenny-migrate up,
// the seed INSERTs, and the erasure-mode DELETE), so the test runs only
// on a cluster whose database it may write to.

package tier8_chaos_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/tests/testinfra/auditquery"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditChainTenant is the synthetic tenant whose audit_log chain this
// test seeds and verifies. audit_log.tenant_id is not a foreign key to
// tenants (the §11.7 schema comment is explicit), so a synthetic id
// needs no tenant row.
const auditChainTenant = "chaos-audit-chain-tenant"

// auditChainBase is the created_at of the seeded chain's first row. The
// list query brackets the seeded rows with since and until, because the
// §25.9 default look-back is the last 24 hours.
var auditChainBase = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// spec: 11.7 (Audit Logging), 25.9 (Audit Log Query API)
// diagnosis: §11.7 audit hash-chain gap detection did not hold. Each
// audit_log row carries a prev_hash; the §11.7 verifier walks the
// per-tenant chain in sequence order, and §25.9 classifies a sequence gap
// whose prev_hash does not link across it as broken. The test seeds a
// valid three-row chain, confirms the §25.9 audit-events list reports
// every row "verified", deletes the middle row directly in Postgres, and
// asserts the list then reports the row after the gap "broken". A
// "verified" verdict means the gap went undetected; a "gap_suspected"
// verdict means the classifier treated a tamper gap as benign (BUILD-GAPS
// F-25.9.21).
func TestAuditChainGapDetection(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, postgresDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); the §11.7 chain is Postgres-backed",
			postgresDeployment, deploymentReadyState(t, c, postgresDeployment))
	}
	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("blocked: %s Deployment is not Ready (%s); the §25.9 audit query is a gateway endpoint, and the route back is a Ready gateway",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}

	// Resolve the Postgres pod IP. A throwaway psql pod in lenny-system
	// cannot resolve Service DNS (the default-deny NetworkPolicy denies
	// its egress to CoreDNS), so the seeding SQL connects to the pod IP.
	pgIP := dataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skipf("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	// Ensure the audit_log schema is present. The e2e Postgres uses
	// emptyDir, so a prior Postgres-outage chaos test that scaled the
	// store leaves the database empty until re-migrated; `lenny-migrate
	// up` is idempotent on an already-migrated database.
	if out, err := runMigrateExpectErr(t, c, pgIP, "up"); err != nil {
		t.Skipf("precondition not met: `lenny-migrate up` failed against the live Postgres (%v); "+
			"cannot establish the audit_log schema for the gap-detection scenario\n%s", err, out)
	}

	probe := "chaos-auditchain-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	// Register the cleanup before reading or seeding: remove every
	// synthetic-tenant row so the shared cluster's audit_log is left
	// clean, including rows a killed earlier run left behind.
	t.Cleanup(func() {
		deleteAuditChainTenant(t, c, pgIP)
	})
	deleteAuditChainTenant(t, c, pgIP)

	// Precondition: the §25.9 list answers for the synthetic tenant.
	pre, err := readAuditChainT8(t, c, probe, gatewayIP)
	if err != nil {
		t.Fatalf("the §25.9 audit-events list for tenant %s could not be read before seeding: %v",
			auditChainTenant, err)
	}
	t.Logf("precondition: the §25.9 audit-events list answers; synthetic-tenant chain has %d rows", len(pre.Events))

	// Build a valid three-row chain with the canonical pkg/audit hash
	// construction: row 1 carries the genesis prev_hash, rows 2 and 3
	// each carry LinkHash(predecessor). The verifier recomputes each
	// row's content hash from the payload and timestamp, so the seeded
	// created_at and payload must match what the hashes were computed
	// over.
	chain := buildAuditChain(auditChainTenant)

	// Seed the valid chain into audit_log.
	seedAuditChain(t, c, pgIP, chain)
	t.Logf("seeded a valid three-row audit chain for tenant %s", auditChainTenant)

	// Assert: the list reports every seeded row verified.
	seeded, err := readAuditChainT8(t, c, probe, gatewayIP)
	if err != nil {
		t.Fatalf("the §25.9 audit-events list could not be read for the seeded chain: %v", err)
	}
	if len(seeded.Events) != len(chain) {
		t.Fatalf("the §25.9 list returned %d rows for the seeded chain, expected %d", len(seeded.Events), len(chain))
	}
	if nv := seeded.NotVerified(); len(nv) != 0 || seeded.Report.Verified != len(chain) {
		t.Fatalf("the §11.7 verifier does not report a freshly-seeded valid chain verified (unverified rows %+v, "+
			"report %+v); the canonical hash construction or the seed is wrong", nv, seeded.Report)
	}
	t.Logf("the §25.9 list reports the seeded three-row chain verified (report %+v)", seeded.Report)

	// Inject the gap: delete the middle row (sequence_number 2)
	// directly in Postgres. The deletion leaves rows 1 and 3, and row
	// 3's prev_hash still links to the now-absent row 2.
	gapSeq := chain[1].Seq
	deleteAuditRow(t, c, pgIP, auditChainTenant, gapSeq)
	t.Logf("injected: audit_log row sequence_number %d deleted, breaking the chain's contiguity", gapSeq)

	// Assert: the row after the gap is broken, because its prev_hash no
	// longer links to its in-order predecessor (now row 1), and row 1
	// still verifies.
	broken, err := readAuditChainT8(t, c, probe, gatewayIP)
	if err != nil {
		t.Fatalf("the §25.9 audit-events list could not be read after the gap injection: %v", err)
	}
	if got, ok := broken.Integrity(chain[0].Seq); !ok || got != "verified" {
		t.Errorf("row %d (before the gap) reports integrity %q (listed %v), expected \"verified\"",
			chain[0].Seq, got, ok)
	}
	got, ok := broken.Integrity(chain[2].Seq)
	switch {
	case !ok:
		t.Errorf("row %d (after the gap) is missing from the §25.9 list after the gap injection", chain[2].Seq)
	case got == "verified":
		t.Errorf("§11.7 violation: row %d still reports integrity \"verified\" after its predecessor was deleted; "+
			"the chain gap was not detected", chain[2].Seq)
	case got != "broken":
		t.Errorf("§25.9 violation: row %d reports integrity %q after its predecessor was deleted, expected "+
			"\"broken\": its prev_hash does not link across the gap, and §25.9 classifies only an intact-link gap "+
			"as gap_suspected (BUILD-GAPS F-25.9.21)", chain[2].Seq, got)
	default:
		t.Logf("gap detected: row %d reports integrity \"broken\" (report %+v)", chain[2].Seq, broken.Report)
	}
	if broken.Report.Broken == 0 && !t.Failed() {
		t.Errorf("the §25.9 chainIntegrityReport counts no broken row after the gap injection (report %+v)",
			broken.Report)
	}
}

// buildAuditChain constructs a valid three-row §11.7 chain for the
// tenant. Each row's PrevHash and Hash are computed with the canonical
// pkg/audit construction the gateway verifier uses, so a faithful seed
// of these rows into audit_log verifies clean.
func buildAuditChain(tenantID string) []audit.Row {
	// Fixed timestamps; the verifier hashes Timestamp as RFC3339Nano,
	// so the seeded created_at must equal these exactly.
	base := auditChainBase
	rows := make([]audit.Row, 3)
	for i := range rows {
		r := audit.Row{
			// id is part of the §11.7 item 3 hash tuple, so it must be a
			// fixed value seeded into audit_log.id (not the column
			// default) to match the hash computed here.
			ID:                 fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", i+1),
			Seq:                uint64(i + 1),
			TenantID:           tenantID,
			EventType:          "chaos.audit_probe",
			EventSchemaVersion: audit.DefaultEventSchemaVersion,
			Payload:            json.RawMessage(fmt.Sprintf(`{"step":%d}`, i+1)),
			Timestamp:          base.Add(time.Duration(i) * time.Minute),
		}
		if i == 0 {
			r.PrevHash = audit.GenesisPrevHash
		} else {
			r.PrevHash = audit.LinkHash(rows[i-1])
		}
		r.Hash = audit.ComputeHash(r)
		rows[i] = r
	}
	return rows
}

// readAuditChainT8 reads the synthetic tenant's §11.7 chain verdict
// through the §25.9 audit-events list as platform-admin, with since and
// until bracketing the seeded rows' fixed timestamps.
func readAuditChainT8(t *testing.T, c *kind.Cluster, pod, gatewayIP string) (auditquery.Chain, error) {
	t.Helper()
	since := auditChainBase.Add(-time.Hour).Format(time.RFC3339)
	until := auditChainBase.Add(time.Hour).Format(time.RFC3339)
	return auditquery.ReadChain(auditChainTenant, since, until, func(path string) (int, []byte, error) {
		p := curlGatewayAuth(t, c, pod, gatewayIP, path)
		if p.curlExit != 0 {
			return 0, nil, fmt.Errorf("curl exit %d (body %q)", p.curlExit, p.body)
		}
		return p.statusCode, []byte(p.body), nil
	})
}

// curlGatewayAuth runs curl inside the probe pod against a gateway path
// with the dev-mode platform-admin headers, returning the HTTP probe
// result. The §11.7 audit endpoints require a platform-admin or
// tenant-admin principal; the dev-header auth path supplies one.
func curlGatewayAuth(t *testing.T, c *kind.Cluster, pod, gatewayIP, path string) httpProbe {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080%s", gatewayIP, path)
	// Single-quote the URL so a query string's `&` reaches curl rather
	// than backgrounding it in the shell.
	if strings.Contains(url, "'") {
		t.Fatalf("curlGatewayAuth URL contains a single quote, which the shell-quoting cannot carry: %q", url)
	}
	script := fmt.Sprintf(
		"curl -sS -m 8 -H 'X-Lenny-Tenant-ID: platform' -H 'X-Lenny-Roles: platform-admin' "+
			"-w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' '%s' 2>&1",
		url,
	)
	out, _ := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "exec", pod, "--", "sh", "-c", script,
	)
	return parseHTTPProbe(out)
}

// seedAuditChain INSERTs the chain's rows into audit_log via a one-shot
// psql pod connected to the Postgres pod IP. The INSERT runs under a
// transaction that sets app.current_tenant so the lenny_tenant_guard
// trigger admits the synthetic-tenant rows; prev_hash is stored as the
// bytea decode of the hex the canonical construction produced.
func seedAuditChain(t *testing.T, c *kind.Cluster, pgIP string, chain []audit.Row) {
	t.Helper()
	var b strings.Builder
	b.WriteString("BEGIN;\n")
	fmt.Fprintf(&b, "SET LOCAL app.current_tenant = '%s';\n", auditChainTenant)
	for _, r := range chain {
		// payload_canonical_json carries the RFC 8785 JCS bytes the §11.7
		// hash was computed over; id and event_schema_version are hash-
		// input columns and must match the seeded values; created_at must
		// equal the hashed Timestamp (RFC3339Nano).
		payload := string(r.Payload)
		canonical := string(audit.CanonicalPayload(r.Payload))
		fmt.Fprintf(&b,
			"INSERT INTO audit_log (id, tenant_id, sequence_number, prev_hash, event_type, "+
				"event_schema_version, payload, payload_canonical_json, created_at) VALUES "+
				"('%s'::uuid, '%s', %d, decode('%s','hex'), '%s', '%s', '%s'::jsonb, '%s'::jsonb, '%s');\n",
			r.ID, r.TenantID, r.Seq, r.PrevHash, r.EventType,
			r.EventSchemaVersion, payload, canonical, r.Timestamp.UTC().Format(time.RFC3339Nano))
	}
	b.WriteString("COMMIT;\n")
	runPsql(t, c, pgIP, "chaos-audit-seed", b.String())
}

// deleteAuditRow deletes one audit_log row for the tenant at the given
// sequence number. The DELETE runs under a transaction that sets both
// app.current_tenant (for lenny_tenant_guard) and lenny.erasure_mode
// (the §11.7 lenny_audit_immutability trigger admits a DELETE only in
// erasure mode) — the documented direct-write path for removing an
// audit row, used here as the gap injector.
func deleteAuditRow(t *testing.T, c *kind.Cluster, pgIP, tenant string, seq uint64) {
	t.Helper()
	sql := "BEGIN;\n" +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s';\n", tenant) +
		"SET LOCAL lenny.erasure_mode = 'true';\n" +
		fmt.Sprintf("DELETE FROM audit_log WHERE tenant_id = '%s' AND sequence_number = %d;\n",
			tenant, seq) +
		"COMMIT;\n"
	runPsql(t, c, pgIP, "chaos-audit-gap", sql)
}

// deleteAuditChainTenant removes every audit_log row for the synthetic
// tenant, leaving the shared cluster's audit_log clean. It is the
// test's t.Cleanup body and tolerates a partially-seeded chain.
func deleteAuditChainTenant(t *testing.T, c *kind.Cluster, pgIP string) {
	t.Helper()
	sql := "BEGIN;\n" +
		fmt.Sprintf("SET LOCAL app.current_tenant = '%s';\n", auditChainTenant) +
		"SET LOCAL lenny.erasure_mode = 'true';\n" +
		fmt.Sprintf("DELETE FROM audit_log WHERE tenant_id = '%s';\n", auditChainTenant) +
		"COMMIT;\n"
	runPsql(t, c, pgIP, "chaos-audit-cleanup", sql)
}

// runPsql runs a SQL script against the e2e Postgres via a one-shot
// psql pod connected to pgIP, failing the test on a failed statement. It
// delegates to kind.Cluster.RunPsqlExec, which runs the script once
// because the seed INSERTs are not safe to rerun.
func runPsql(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) {
	t.Helper()
	c.RunPsqlExec(t, lennySystemNamespace, pgIP, podName, sql)
}

// dataStorePodIP returns the pod IP of the named e2e data store
// (postgres, redis, or minio). The test process runs kubectl from
// outside the cluster, so it resolves the pod IP without needing
// in-cluster DNS.
//
// A store the outage tests scaled to zero and back keeps its
// predecessor listed, Running and carrying a deletionTimestamp, for as
// long as the graceful termination takes. The API server does not
// order that list by liveness, so reading {.items[0]} returns the
// dying pod's address about half the time and every dial against it is
// refused. The query therefore prints the deletion timestamp beside
// each address and this function takes the first pod that is not
// terminating.
func dataStorePodIP(t *testing.T, c *kind.Cluster, store string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pod",
		"-l", "lenny.dev/e2e-datastore="+store,
		"-o", `jsonpath={range .items[*]}{.metadata.deletionTimestamp}{"|"}{.status.podIP}{"\n"}{end}`,
	)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		terminatingAt, ip, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || terminatingAt != "" || ip == "" {
			continue
		}
		return ip
	}
	return ""
}
