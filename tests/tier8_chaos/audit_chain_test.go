// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for §11.7 audit hash-chain gap detection. The audit
// chain is Postgres-backed: every audit_log row carries a prev_hash
// linking it to its predecessor, and the gateway's §11.7 verifier walks
// the per-tenant chain in sequence-number order. The verifier is
// reachable through the GET /v1/admin/audit-events/verify admin API.
//
// The test seeds a synthetic tenant's audit_log with a valid chain
// (computed with the canonical pkg/audit hash construction so the
// verifier accepts it), confirms the verifier reports the chain
// verified, then injects a gap by deleting a middle row directly in
// Postgres and asserts the verifier reports the chain broken. The
// injected rows are removed in a t.Cleanup so the shared cluster's
// audit_log carries no leftover synthetic tenant.

package tier8_chaos_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditChainTenant is the synthetic tenant whose audit_log chain this
// test seeds and verifies. audit_log.tenant_id is not a foreign key to
// tenants (the §11.7 schema comment is explicit), so a synthetic id
// needs no tenant row.
const auditChainTenant = "chaos-audit-chain-tenant"

// spec: 11.7
// diagnosis: §11.7 audit hash-chain gap detection did not hold. Each
// audit_log row carries a prev_hash; the §11.7 verifier walks the
// per-tenant chain in sequence order and reports it broken when a row's
// prev_hash does not link to its predecessor. The test seeds a valid
// three-row chain, confirms GET /v1/admin/audit-events/verify reports
// "verified", deletes the middle row directly in Postgres, and asserts
// the verifier then reports the chain broken. A failure means no gap.
func TestAuditChainGapDetection(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, postgresDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); the §11.7 chain is Postgres-backed",
			postgresDeployment, deploymentReadyState(t, c, postgresDeployment))
	}
	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); the verifier is a gateway endpoint",
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

	// Precondition: the §11.7 verifier endpoint is reachable. An empty
	// chain for the synthetic tenant must verify (nothing to break).
	pre := auditVerify(t, c, probe, gatewayIP, auditChainTenant)
	if pre.statusCode != 200 {
		t.Skipf("precondition not met: GET /v1/admin/audit-events/verify is not reachable "+
			"(curl exit %d, status %d, body %q); the §11.7 verifier endpoint is unavailable",
			pre.curlExit, pre.statusCode, pre.body)
	}
	t.Logf("precondition: the §11.7 verify endpoint is reachable; synthetic-tenant chain integrity is %q",
		pre.integrity)

	// Build a valid three-row chain with the canonical pkg/audit hash
	// construction: row 1 carries the genesis prev_hash, rows 2 and 3
	// each carry LinkHash(predecessor). The verifier recomputes each
	// row's content hash from the payload and timestamp, so the seeded
	// created_at and payload must match what the hashes were computed
	// over.
	chain := buildAuditChain(auditChainTenant)

	// Register the cleanup before seeding: remove every synthetic-tenant
	// row so the shared cluster's audit_log is left clean.
	t.Cleanup(func() {
		deleteAuditChainTenant(t, c, pgIP)
	})

	// Seed the valid chain into audit_log.
	seedAuditChain(t, c, pgIP, chain)
	t.Logf("seeded a valid three-row audit chain for tenant %s", auditChainTenant)

	// Assert: the verifier reports the seeded chain verified.
	seeded := auditVerify(t, c, probe, gatewayIP, auditChainTenant)
	if seeded.statusCode != 200 {
		t.Fatalf("the verify endpoint returned status %d for the seeded chain (body %q)",
			seeded.statusCode, seeded.body)
	}
	if seeded.integrity != "verified" {
		t.Fatalf("the §11.7 verifier reports integrity %q for a freshly-seeded valid chain, expected "+
			"\"verified\"; the canonical hash construction or the seed is wrong (detail %q)",
			seeded.integrity, seeded.detail)
	}
	if seeded.rowCount != len(chain) {
		t.Errorf("the verifier reports rowCount %d for the seeded chain, expected %d",
			seeded.rowCount, len(chain))
	}
	t.Logf("the §11.7 verifier reports the seeded three-row chain verified")

	// Inject the gap: delete the middle row (sequence_number 2)
	// directly in Postgres. The deletion leaves rows 1 and 3, and row
	// 3's prev_hash still links to the now-absent row 2.
	gapSeq := chain[1].Seq
	deleteAuditRow(t, c, pgIP, auditChainTenant, gapSeq)
	t.Logf("injected: audit_log row sequence_number %d deleted, breaking the chain's contiguity", gapSeq)

	// Assert: the verifier now reports the chain broken. The §11.7 walk
	// finds row 3's prev_hash no longer links to its in-order
	// predecessor (now row 1).
	broken := auditVerify(t, c, probe, gatewayIP, auditChainTenant)
	if broken.statusCode != 200 {
		t.Fatalf("the verify endpoint returned status %d after the gap injection (body %q)",
			broken.statusCode, broken.body)
	}
	if broken.integrity == "verified" {
		t.Errorf("§11.7 violation: the verifier still reports integrity \"verified\" after a middle " +
			"audit_log row was deleted; the chain gap was not detected")
	} else {
		t.Logf("gap detected: the §11.7 verifier reports integrity %q after the gap injection "+
			"(breakSeq %d, detail %q)", broken.integrity, broken.breakSeq, broken.detail)
	}
	// The break must be reported at the row whose predecessor vanished.
	if broken.integrity != "verified" && broken.breakSeq != 0 && broken.breakSeq != chain[2].Seq {
		t.Errorf("the verifier reports the break at sequence_number %d, expected %d "+
			"(the row whose predecessor was deleted)", broken.breakSeq, chain[2].Seq)
	}
	t.Logf("§11.7 audit-chain gap detection verified end to end")
}

// buildAuditChain constructs a valid three-row §11.7 chain for the
// tenant. Each row's PrevHash and Hash are computed with the canonical
// pkg/audit construction the gateway verifier uses, so a faithful seed
// of these rows into audit_log verifies clean.
func buildAuditChain(tenantID string) []audit.Row {
	// Fixed timestamps; the verifier hashes Timestamp as RFC3339Nano,
	// so the seeded created_at must equal these exactly.
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
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

// auditVerifyResult is the parsed §11.7 verify-endpoint response.
type auditVerifyResult struct {
	curlExit   int
	statusCode int
	body       string
	integrity  string
	breakSeq   uint64
	detail     string
	rowCount   int
}

// auditVerify calls GET /v1/admin/audit-events/verify for the tenant
// and parses the §11.7 chain-integrity response.
func auditVerify(t *testing.T, c *kind.Cluster, pod, gatewayIP, tenant string) auditVerifyResult {
	t.Helper()
	p := curlGatewayAuth(t, c, pod, gatewayIP,
		"/v1/admin/audit-events/verify?tenantId="+tenant)
	res := auditVerifyResult{curlExit: p.curlExit, statusCode: p.statusCode, body: p.body}
	if p.curlExit != 0 {
		return res
	}
	var doc struct {
		Integrity string `json:"integrity"`
		BreakSeq  uint64 `json:"breakSeq"`
		Detail    string `json:"detail"`
		RowCount  int    `json:"rowCount"`
	}
	if err := json.Unmarshal([]byte(p.body), &doc); err == nil {
		res.integrity = doc.Integrity
		res.breakSeq = doc.BreakSeq
		res.detail = doc.Detail
		res.rowCount = doc.RowCount
	}
	return res
}

// curlGatewayAuth runs curl inside the probe pod against a gateway path
// with the dev-mode platform-admin headers, returning the HTTP probe
// result. The §11.7 audit endpoints require a platform-admin or
// tenant-admin principal; the dev-header auth path supplies one.
func curlGatewayAuth(t *testing.T, c *kind.Cluster, pod, gatewayIP, path string) httpProbe {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080%s", gatewayIP, path)
	script := fmt.Sprintf(
		"curl -sS -m 8 -H 'X-Lenny-Tenant-ID: platform' -H 'X-Lenny-Roles: platform-admin' "+
			"-w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' %s 2>&1",
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
// postgres:16.4 pod connected to pgIP. The pod connects to the pod IP
// rather than the Service DNS name because the lenny-system
// default-deny NetworkPolicy denies a throwaway pod's egress to
// CoreDNS. ON_ERROR_STOP makes any failed statement a non-zero exit.
func runPsql(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", lennySystemNamespace, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "run", podName,
		"--image=postgres:16.4", "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
		"-v", "ON_ERROR_STOP=1", "-c", sql,
	); err != nil {
		t.Fatalf("psql script %q failed: %v\n%s", podName, err, out)
	}
}

// dataStorePodIP returns the pod IP of the named e2e data store
// (postgres, redis, or minio). The test process runs kubectl from
// outside the cluster, so it resolves the pod IP without needing
// in-cluster DNS.
func dataStorePodIP(t *testing.T, c *kind.Cluster, store string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pod",
		"-l", "lenny.dev/e2e-datastore="+store,
		"-o", "jsonpath={.items[0].status.podIP}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
