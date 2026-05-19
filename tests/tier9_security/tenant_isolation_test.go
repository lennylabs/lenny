// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for §12.9.1 cross-store tenant isolation. The
// e2e Kind cluster runs the gateway against a real lenny-postgres, so
// the §12.3 row-level-security path (the lenny_tenant_guard trigger and
// the per-tenant RLS policies on every tenant-scoped table) is live.
//
// The full §12.9.1 scenario probes every store and every API surface;
// this test exercises a meaningful subset against the Postgres-backed
// stores reachable through the gateway admin API. It seeds two
// synthetic tenants with distinct state and asserts:
//
//   - The §11.7 Postgres-backed audit chain is tenant-partitioned: the
//     audit-events list for tenant A returns only tenant-A rows, never
//     tenant-B rows, and vice versa. This is the §12.3 RLS / tenant-
//     guard isolation observed end to end through the gateway.
//   - The cross-tenant audit-query path is rejected: a tenant-admin
//     authenticated for tenant A who requests tenant B's chain via
//     ?tenantId= receives the documented 403 FORBIDDEN isolation
//     error rather than tenant B's data.
//
// The tenant-scoped read tier (a tenant-admin sees only their own
// tenant's runtimes/pools) is asserted by the tier-2 RLS suite and the
// admin handler unit tests; this file is the live-cluster adversarial
// overlay for the cross-tenant audit path. Every synthetic tenant and
// every audit_log row created is removed in a t.Cleanup.

package tier9_security_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 12.9.1
// diagnosis: §12.9.1 cross-store tenant isolation did not hold. The
// test seeds two synthetic tenants, generates audit events on each
// tenant's §11.7 Postgres-backed chain, and asserts (a) tenant A's
// audit-events list returns only tenant-A rows — the §12.3 RLS /
// lenny_tenant_guard partition — and (b) a tenant-admin for tenant A
// requesting tenant B's chain via ?tenantId= is rejected with 403
// FORBIDDEN. A cross-tenant row in a list, or a non-403 on the cross-
// tenant query, is a tenant-isolation breach.
func TestTenantIsolationCrossStore(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; §12.3 RLS isolation is Postgres-backed", auditDeployment)
	}
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the admin API is the gateway", gatewayDeploymentName)
	}
	pgIP := dataStorePodIPT9(t, c, "postgres")
	if pgIP == "" {
		t.Skip("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	probe := "t9-tenantiso-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	tenantA := "t9-iso-acme"
	tenantB := "t9-iso-globex"

	// Cleanup first: remove both synthetic tenant rows and their
	// audit_log rows regardless of test outcome.
	t.Cleanup(func() {
		for _, ten := range []string{tenantA, tenantB} {
			_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/tenants/"+ten, admin, "")
			deleteAuditTenantRows(t, c, pgIP, ten)
		}
	})

	// Precondition: the audit-events list endpoint is reachable.
	if r := gatewayRequestRetry(t, c, probe, gatewayIP, "GET",
		"/v1/admin/audit-events?tenantId=platform&limit=1", admin, ""); r.curlExit != 0 || r.statusCode != 200 {
		t.Skipf("precondition not met: GET /v1/admin/audit-events is not reachable "+
			"(curl exit %d, status %d, body %q)", r.curlExit, r.statusCode, r.body)
	}

	// Seed distinct state: each tenant gets a distinct number of
	// bootstrap upserts, so each chain carries a distinct, recognisable
	// row count. The caller's dev-header tenant determines which chain
	// the admin.bootstrap.applied event lands on.
	const eventsA, eventsB = 4, 7
	seedTenantAuditEvents(t, c, probe, gatewayIP, tenantA, eventsA)
	seedTenantAuditEvents(t, c, probe, gatewayIP, tenantB, eventsB)
	t.Logf("seeded %d audit events for %s and %d for %s", eventsA, tenantA, eventsB, tenantB)

	// --- Isolation property 1: the audit chain is tenant-partitioned.
	// A platform-admin querying tenant A's chain must see only tenant-A
	// rows. The §12.3 RLS policy on audit_log filters every read to the
	// transaction's app.current_tenant, which the auditstore sets to the
	// requested tenant.
	rowsA := listAuditEventTenants(t, c, probe, gatewayIP, admin, tenantA)
	rowsB := listAuditEventTenants(t, c, probe, gatewayIP, admin, tenantB)

	for i, ten := range rowsA {
		if ten != tenantA {
			t.Errorf("§12.9.1 violation: tenant %s's audit-events list contains a row (index %d) "+
				"belonging to tenant %q; the §12.3 RLS partition leaked a cross-tenant audit row",
				tenantA, i, ten)
		}
	}
	for i, ten := range rowsB {
		if ten != tenantB {
			t.Errorf("§12.9.1 violation: tenant %s's audit-events list contains a row (index %d) "+
				"belonging to tenant %q; the §12.3 RLS partition leaked a cross-tenant audit row",
				tenantB, i, ten)
		}
	}
	if len(rowsA) < eventsA {
		t.Errorf("tenant %s's audit chain returned %d rows, expected at least %d — "+
			"the seeded events did not reach the Postgres chain", tenantA, len(rowsA), eventsA)
	}
	if len(rowsB) < eventsB {
		t.Errorf("tenant %s's audit chain returned %d rows, expected at least %d — "+
			"the seeded events did not reach the Postgres chain", tenantB, len(rowsB), eventsB)
	}
	if len(rowsA) == len(rowsB) {
		// Distinct seed counts make distinct chain lengths the expected
		// outcome; equal lengths would suggest the two chains are not in
		// fact separate. This is a soft signal, not a hard breach.
		t.Logf("note: tenant A and tenant B chains returned equal row counts (%d); "+
			"distinct seed counts (%d vs %d) expected distinct lengths", len(rowsA), eventsA, eventsB)
	}
	t.Logf("§12.9.1: audit chain partition holds — tenant %s sees %d own rows, tenant %s sees %d own rows, "+
		"no cross-tenant rows in either list", tenantA, len(rowsA), tenantB, len(rowsB))

	// --- Isolation property 2: the cross-tenant audit-query path is
	// rejected. A tenant-admin authenticated for tenant A who asks for
	// tenant B's chain via ?tenantId= must be denied with 403 FORBIDDEN
	// — the documented §10.2 isolation error — and must NOT receive
	// tenant B's audit data.
	tenantAAdmin := gwRole{tenant: tenantA, roles: "tenant-admin", user: "carol"}
	cross := gatewayRequestRetry(t, c, probe, gatewayIP, "GET",
		"/v1/admin/audit-events/verify?tenantId="+tenantB, tenantAAdmin, "")
	if cross.statusCode != 403 {
		t.Errorf("§12.9.1 violation: a tenant-admin for %s requesting %s's audit chain received "+
			"status %d, expected 403 FORBIDDEN; the cross-tenant audit-query guard did not reject it "+
			"(body %q)", tenantA, tenantB, cross.statusCode, cross.body)
	} else if code := cross.errorCode(); code != "FORBIDDEN" {
		t.Errorf("§12.9.1: the cross-tenant audit-query rejection carries error code %q, expected "+
			"\"FORBIDDEN\" (body %q)", code, cross.body)
	} else {
		t.Logf("§12.9.1: cross-tenant audit query rejected — tenant-admin for %s denied %s's chain "+
			"with 403 FORBIDDEN", tenantA, tenantB)
	}

	// A tenant-admin reading their OWN tenant's chain must still succeed:
	// the guard rejects the cross-tenant request specifically, not every
	// tenant-admin audit read. This rules out a false positive where the
	// 403 above came from a blanket denial.
	own := gatewayRequestRetry(t, c, probe, gatewayIP, "GET",
		"/v1/admin/audit-events/verify?tenantId="+tenantA, tenantAAdmin, "")
	if own.statusCode != 200 {
		t.Errorf("a tenant-admin for %s reading its OWN audit chain received status %d, expected 200; "+
			"the audit-query guard is over-broad (body %q)", tenantA, own.statusCode, own.body)
	} else {
		t.Logf("control: tenant-admin for %s reads its own chain with 200 — the guard is scoped to "+
			"cross-tenant requests", tenantA)
	}
}

// seedTenantAuditEvents issues n bootstrap upserts of the tenant as a
// platform-admin authenticated for that same tenant. Each upsert emits
// one admin.bootstrap.applied audit event onto the tenant's §11.7
// chain. The first upsert also creates the tenant row.
func seedTenantAuditEvents(t *testing.T, c *kind.Cluster, probe, gatewayIP, tenant string, n int) {
	t.Helper()
	body := fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenant)
	role := gwRole{tenant: tenant, roles: "platform-admin", user: "alice"}
	for i := 0; i < n; i++ {
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/bootstrap", role, body)
		if res.curlExit != 0 || (res.statusCode != 200 && res.statusCode != 207) {
			t.Fatalf("seeding audit event %d for tenant %s failed (curl exit %d, status %d, body %q)",
				i+1, tenant, res.curlExit, res.statusCode, res.body)
		}
	}
}

// listAuditEventTenants reads the audit-events list for tenant as the
// given role and returns the tenantId field of every returned row. A
// correctly partitioned chain yields a slice in which every element
// equals the requested tenant.
func listAuditEventTenants(t *testing.T, c *kind.Cluster, probe, gatewayIP string, role gwRole, tenant string) []string {
	t.Helper()
	res := gatewayRequestRetry(t, c, probe, gatewayIP, "GET",
		"/v1/admin/audit-events?tenantId="+tenant+"&limit=1000", role, "")
	if res.curlExit != 0 || res.statusCode != 200 {
		t.Fatalf("audit-events list for tenant %s failed (curl exit %d, status %d, body %q)",
			tenant, res.curlExit, res.statusCode, res.body)
	}
	var doc struct {
		AuditEvents []struct {
			TenantID string `json:"tenantId"`
		} `json:"auditEvents"`
	}
	// An empty body would decode to a zero struct and read as "no rows",
	// masking a failed request; reject it explicitly.
	if strings.TrimSpace(res.body) == "" {
		t.Fatalf("the audit-events list for tenant %s returned an empty body", tenant)
	}
	if err := json.Unmarshal([]byte(res.body), &doc); err != nil {
		t.Fatalf("the audit-events list for tenant %s is not valid JSON: %v\nbody: %q",
			tenant, err, res.body)
	}
	out := make([]string, 0, len(doc.AuditEvents))
	for _, ev := range doc.AuditEvents {
		out = append(out, ev.TenantID)
	}
	return out
}
