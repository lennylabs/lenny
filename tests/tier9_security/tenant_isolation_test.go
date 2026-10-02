// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for TESTING.md §12.9.1 cross-store tenant isolation. The
// e2e Kind cluster runs the gateway against a real lenny-postgres, so
// the §12.3 row-level-security path (the lenny_tenant_guard trigger and
// the per-tenant RLS policies on every tenant-scoped table) is live.
//
// The full TESTING.md §12.9.1 scenario probes every store and every API surface;
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: 12.9.1
// diagnosis: TESTING.md §12.9.1 cross-store tenant isolation did not hold. The
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
			t.Errorf("TESTING.md §12.9.1 violation: tenant %s's audit-events list contains a row (index %d) "+
				"belonging to tenant %q; the §12.3 RLS partition leaked a cross-tenant audit row",
				tenantA, i, ten)
		}
	}
	for i, ten := range rowsB {
		if ten != tenantB {
			t.Errorf("TESTING.md §12.9.1 violation: tenant %s's audit-events list contains a row (index %d) "+
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
	t.Logf("TESTING.md §12.9.1: audit chain partition holds — tenant %s sees %d own rows, tenant %s sees %d own rows, "+
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
		t.Errorf("TESTING.md §12.9.1 violation: a tenant-admin for %s requesting %s's audit chain received "+
			"status %d, expected 403 FORBIDDEN; the cross-tenant audit-query guard did not reject it "+
			"(body %q)", tenantA, tenantB, cross.statusCode, cross.body)
	} else if code := cross.errorCode(); code != "FORBIDDEN" {
		t.Errorf("TESTING.md §12.9.1: the cross-tenant audit-query rejection carries error code %q, expected "+
			"\"FORBIDDEN\" (body %q)", code, cross.body)
	} else {
		t.Logf("TESTING.md §12.9.1: cross-tenant audit query rejected — tenant-admin for %s denied %s's chain "+
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

// keptRuntimeHarness is a dedicated recycling pool on the Kind cluster whose
// pods keep their runtime process across the sessions they serve, with its
// own runtime so session resolution is unambiguous. It drives the §5.2
// tenant-pin and process-reuse acquisition rules end to end.
type keptRuntimeHarness struct {
	d       *sessiondriver.Driver
	c       *kind.Cluster
	probe   string
	gwIP    string
	admin   gwRole
	runtime string
	pool    string
	// warmCount is the pool's warmCount, which the PoolScalingController
	// renders as maxWarm.
	warmCount int
}

// keptRuntimeRecyclePolicy is the acknowledged recycling sessionPolicy the
// harness pool starts with.
const keptRuntimeRecyclePolicy = `{"acknowledgeProcessLevelIsolation":true,` +
	`"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,"maxSessionsPerPod":5},` +
	`"cleanupTimeoutSeconds":30}`

// newKeptRuntimeHarness registers a runtime cloned from the task-mode echo
// runtime's image and a recycling pool bound to it, waits for an idle pod,
// and removes both in t.Cleanup.
//
// bootstrapMinWarm, when non-negative, is set on the pool once it is warm,
// and the pool's warmCount is one above it, so the rendered minWarm sits one
// below maxWarm. That leaves room under the §5.2 pinned-idle bound for one
// recycled pod to stay idle and pinned, while a minWarm of at least 2 keeps
// an idle pod after a claim so the pool does not report PoolWarmingUp.
func newKeptRuntimeHarness(t *testing.T, label string, bootstrapMinWarm int) *keptRuntimeHarness {
	t.Helper()
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("blocked: %s is not Ready", gatewayDeploymentName)
	}
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano()%1_000_000_000)
	h := &keptRuntimeHarness{
		d: d, c: c, probe: "t9-kept-" + label, admin: platformAdmin(),
		runtime: "t9-kept-rt-" + label + suffix, pool: "t9-kept-pool-" + label + suffix,
	}
	h.gwIP = startGatewayProbe(t, c, h.probe)
	sweepKeptRuntimeLeftovers(t, c, h.probe, h.gwIP, h.admin)

	src := gatewayRequestRetry(t, c, h.probe, h.gwIP, "GET", "/v1/admin/runtimes/echo-runtime-task-mode", h.admin, "")
	var rt struct {
		Image string `json:"image"`
	}
	if src.statusCode != 200 || json.Unmarshal([]byte(src.body), &rt) != nil || rt.Image == "" {
		t.Skipf("blocked: the task-mode echo runtime is not registered (status %d)", src.statusCode)
	}
	body := fmt.Sprintf(`{"name":%q,"type":"agent","image":%q,"integrationLevel":"basic",`+
		`"executionMode":"session","isolationProfile":"standard","labels":{"lenny.dev/e2e":"t9-kept-runtime"}}`,
		h.runtime, rt.Image)
	if res := gatewayRequestRetry(t, c, h.probe, h.gwIP, "POST", "/v1/admin/runtimes", h.admin, body); res.statusCode != 201 {
		t.Fatalf("register runtime %s: status %d body %q", h.runtime, res.statusCode, res.body)
	}
	t.Cleanup(func() {
		_ = gatewayRequest(t, c, h.probe, h.gwIP, "DELETE", "/v1/admin/runtimes/"+h.runtime, h.admin, "")
	})
	// The Sandbox reconciler renders the agent pod from the cluster-scoped
	// Runtime CR, so the registry row needs its CR counterpart.
	h.applyRuntimeCR(t, rt.Image)
	h.warmCount = 2
	if bootstrapMinWarm >= 0 {
		h.warmCount = bootstrapMinWarm + 1
	}
	poolBody := fmt.Sprintf(`{"name":%q,"runtimeRef":%q,"isolationProfile":"standard","executionMode":"session",`+
		`"warmCount":%d,"allowStandardIsolation":true,"dnsPolicy":"cluster-default","sessionPolicy":%s}`,
		h.pool, h.runtime, h.warmCount, keptRuntimeRecyclePolicy)
	if res := gatewayRequestRetry(t, c, h.probe, h.gwIP, "POST", "/v1/admin/pools", h.admin, poolBody); res.statusCode != 201 {
		t.Fatalf("create pool %s: status %d body %q", h.pool, res.statusCode, res.body)
	}
	t.Cleanup(func() { removeKeptRuntimePool(t, c, h.probe, h.gwIP, h.admin, h.pool) })
	h.waitReadyPods(t, 1, 4*time.Minute)
	if bootstrapMinWarm >= 0 {
		h.setBootstrapMinWarm(t, bootstrapMinWarm)
	}
	return h
}

// setBootstrapMinWarm updates the pool's bootstrapMinWarm with warmCount
// unchanged and waits until the PoolScalingController renders it as the
// SandboxWarmPool's minWarm.
func (h *keptRuntimeHarness) setBootstrapMinWarm(t *testing.T, n int) {
	t.Helper()
	if res := h.putPool(t, fmt.Sprintf(`{"bootstrapMinWarm":%d,"warmCount":%d}`, n, h.warmCount)); res.statusCode != 200 {
		t.Fatalf("pool update to bootstrapMinWarm %d: status %d body %q", n, res.statusCode, res.body)
	}
	h.waitField(t, "sandboxwarmpool", h.pool, "{.spec.minWarm}", fmt.Sprint(n), 2*time.Minute)
}

// keptRuntimeCRLabel marks the Runtime CRs the harness applies, so a run
// can sweep the CRs a killed run left behind.
const keptRuntimeCRLabel = "lenny.dev/e2e=t9-kept-runtime"

// applyRuntimeCR applies the cluster-scoped Runtime CR for the harness
// runtime and deletes it in t.Cleanup.
func (h *keptRuntimeHarness) applyRuntimeCR(t *testing.T, image string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: %s
  labels:
    lenny.dev/e2e: t9-kept-runtime
spec:
  deploymentModel: sidecar
  executionMode: session
  image: %s
  integrationLevel: basic
  isolationProfile: standard
  type: agent
`, h.runtime, image)
	cmd := h.c.Kubectl("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("apply Runtime CR %s: %v\n%s", h.runtime, err, out)
	}
	t.Cleanup(func() {
		_, _ = h.c.KubectlOut(t, "delete", "runtime", h.runtime, "--wait=false", "--ignore-not-found")
	})
}

// removeKeptRuntimePool deletes a harness pool through the admin API and
// then deletes its SandboxWarmPool and SandboxTemplate, so its Sandboxes and
// agent pods are garbage-collected rather than left pinned and idle, where
// later tests waiting on the managed-pod label would wait on them.
func removeKeptRuntimePool(t *testing.T, c *kind.Cluster, probe, gwIP string, admin gwRole, name string) {
	t.Helper()
	cleanupPool(t, c, probe, gwIP, admin, name)
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "sandboxwarmpool,sandboxtemplate", name,
		"--wait=false", "--ignore-not-found")
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "sandboxes", "-l", "lenny.dev/pool="+name,
		"--wait=false", "--ignore-not-found")
}

// sweepKeptRuntimeLeftovers deletes the pools and Runtime CRs an earlier,
// killed run of the kept-runtime harness left behind. t.Cleanup does not run
// when the process is killed, and every name carries a per-run suffix, so a
// leftover is never reclaimed by reuse. The sweep is best-effort and never
// fails the run.
func sweepKeptRuntimeLeftovers(t *testing.T, c *kind.Cluster, probe, gwIP string, admin gwRole) {
	t.Helper()
	out, _ := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxwarmpools", "-o", "name")
	for _, ref := range strings.Fields(out) {
		name := strings.TrimPrefix(ref, "sandboxwarmpool.lenny.dev/")
		if !strings.HasPrefix(name, "t9-kept-pool-") {
			continue
		}
		rt, _ := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxtemplate", name, "-o", "jsonpath={.spec.runtimeRef}")
		removeKeptRuntimePool(t, c, probe, gwIP, admin, name)
		if rt = strings.TrimSpace(rt); strings.HasPrefix(rt, "t9-kept-rt-") {
			_ = gatewayRequest(t, c, probe, gwIP, "DELETE", "/v1/admin/runtimes/"+rt, admin, "")
		}
		t.Logf("swept leftover kept-runtime pool %s (runtime %q)", name, rt)
	}
	if out, err := c.KubectlOut(t, "delete", "runtime", "-l", keptRuntimeCRLabel, "--wait=false", "--ignore-not-found"); err == nil && strings.TrimSpace(out) != "" {
		t.Logf("swept leftover kept-runtime Runtime CRs: %s", strings.TrimSpace(out))
	}
}

// waitReadyPods waits until the pool's SandboxWarmPool reports at least n
// ready pods.
func (h *keptRuntimeHarness) waitReadyPods(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, _ := h.c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxwarmpool", h.pool,
			"-o", "jsonpath={.status.readyCount}")
		var ready int
		_, _ = fmt.Sscanf(strings.TrimSpace(out), "%d", &ready)
		if ready >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("blocked: pool %s did not reach %d ready pods within %v (ready %q)", h.pool, n, timeout, out)
		}
		time.Sleep(3 * time.Second)
	}
}

// tenant bootstraps a per-run tenant that may create sessions with no
// environment.
func (h *keptRuntimeHarness) tenant(t *testing.T, ctx context.Context, base string) string {
	t.Helper()
	tenant := fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%1_000_000_000)
	if err := h.d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant %s: %v", tenant, err)
	}
	if err := h.d.AllowSessionsWithNoEnvironment(ctx, tenant); err != nil {
		t.Fatalf("allow sessions with no environment for %s: %v", tenant, err)
	}
	return tenant
}

// objectField reads one jsonpath field of a namespaced agent object; "" when
// the object is absent.
func (h *keptRuntimeHarness) objectField(t *testing.T, kindName, name, path string) string {
	t.Helper()
	out, err := h.c.KubectlOut(t, "-n", agentNamespace, "get", kindName, name, "-o", "jsonpath="+path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// waitField polls objectField until it equals want.
func (h *keptRuntimeHarness) waitField(t *testing.T, kindName, name, path, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := h.objectField(t, kindName, name, path)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s %s = %q after %v, want %q", kindName, name, path, got, timeout, want)
		}
		time.Sleep(time.Second)
	}
}

// waitDrained polls the Sandbox's phase until the pod is draining or already
// past it (terminated, or the Sandbox removed).
func (h *keptRuntimeHarness) waitDrained(t *testing.T, pod string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		switch got := h.objectField(t, "sandbox", pod, "{.status.phase}"); got {
		case "draining", "terminated", "":
			return
		default:
			if time.Now().After(deadline) {
				t.Fatalf("sandbox %s phase = %q after %v, want draining", pod, got, timeout)
			}
		}
		time.Sleep(time.Second)
	}
}

// waitIdleOrSkip waits for a recycled pod to return to idle after its hold
// ends. A pod whose sidecar runtime ended with its first session fails and
// is retired instead, so the pinned-idle path these cases drive cannot be
// reached; the case is skipped with that precondition rather than failed.
func (h *keptRuntimeHarness) waitIdleOrSkip(t *testing.T, pod string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		switch got := h.objectField(t, "sandbox", pod, "{.status.phase}"); got {
		case "idle":
			return
		case "draining", "terminated", "":
			t.Skipf("blocked: recycled pod %s reached %q instead of idle; the pinned idle pod "+
				"exists only once the sidecar runtime process lives as long as the pod (§4.7.10)", pod, got)
		default:
			if time.Now().After(deadline) {
				t.Fatalf("sandbox %s phase = %q after %v, want idle", pod, got, timeout)
			}
		}
		time.Sleep(time.Second)
	}
}

// sessionAToReserved runs one session for tenant, terminates it, and waits
// until its pod's claim is held `reserved`. It returns the pod.
func (h *keptRuntimeHarness) sessionAToReserved(t *testing.T, ctx context.Context, tenant string) string {
	t.Helper()
	sessA, err := h.d.CreateAndStart(ctx, tenant, h.runtime)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("blocked: pool %s not ready: %v", h.pool, err)
	}
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}
	if sessA.PodAssignment == "" {
		t.Fatalf("session A %s carries no podAssignment", sessA.ID)
	}
	if err := h.d.Terminate(ctx, tenant, sessA.ID); err != nil {
		t.Fatalf("terminate session A: %v", err)
	}
	h.waitField(t, "sandboxclaim", "claim-"+sessA.PodAssignment, "{.status.phase}", "reserved", 90*time.Second)
	return sessA.PodAssignment
}

// putPool sends PUT /v1/admin/pools/{name} with the pool's current ETag.
func (h *keptRuntimeHarness) putPool(t *testing.T, body string) gwResponse {
	t.Helper()
	etag := gatewayResourceETag(t, h.c, h.probe, h.gwIP, "/v1/admin/pools/"+h.pool, h.admin)
	return gatewayRequestIfMatch(t, h.c, h.probe, h.gwIP, "PUT", "/v1/admin/pools/"+h.pool, h.admin, etag, body)
}

// gatewayLogsMention reports whether a gateway replica logged a line
// carrying every substring.
func (h *keptRuntimeHarness) gatewayLogsMention(t *testing.T, substrs ...string) bool {
	t.Helper()
	out, _ := h.c.KubectlOut(t, "-n", lennySystemNS, "logs", "-l", "lenny.dev/component=gateway",
		"--all-containers", "--since=20m", "--tail=-1", "--max-log-requests=10")
	for _, line := range strings.Split(out, "\n") {
		all := true
		for _, s := range substrs {
			if !strings.Contains(line, s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// sessionPodReuse reads sessionIsolationLevel.podReuse for a session.
func (h *keptRuntimeHarness) sessionPodReuse(t *testing.T, ctx context.Context, tenant, id string) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.d.BaseURL()+"/v1/sessions/"+id, nil)
	if err != nil {
		t.Fatalf("build session get: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	defer res.Body.Close()
	var payload struct {
		SessionIsolationLevel struct {
			PodReuse bool `json:"podReuse"`
		} `json:"sessionIsolationLevel"`
	}
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode session %s: %v; body %s", id, err, raw)
	}
	return payload.SessionIsolationLevel.PodReuse
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions)), 4.6.1 (Reserved hold)
// diagnosis: an admitted pool update leaves a kept runtime process reachable
// by a later session: the reserved pod is rebound or survives, the later
// session lands on the pod that served a session, or the refused pod is not
// drained.
func TestPoolEditOutsideRuleNeverDispatchesIntoKeptRuntime(t *testing.T) {
	t.Run("hold ended by the acquisition", func(t *testing.T) {
		h := newKeptRuntimeHarness(t, "hold", -1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		acme := h.tenant(t, ctx, "t9-kept-acme")
		podA := h.sessionAToReserved(t, ctx, acme)

		if res := h.putPool(t, `{"sessionPolicy":{"recycle":{"enabled":false,"maxSessionsPerPod":5},"cleanupTimeoutSeconds":30}}`); res.statusCode != 200 {
			t.Fatalf("pool update to recycle.enabled false: status %d body %q", res.statusCode, res.body)
		}
		sessB, err := h.d.CreateAndStart(ctx, acme, h.runtime)
		if err != nil {
			t.Fatalf("create session B: %v", err)
		}
		t.Cleanup(func() { _ = h.d.Terminate(context.Background(), acme, sessB.ID) })
		if sessB.PodAssignment == podA {
			t.Fatalf("session B landed on pod %s, which served session A on a pool that no longer keeps its runtime", podA)
		}
		h.waitField(t, "sandboxclaim", "claim-"+podA, "{.metadata.name}", "", 60*time.Second)
		h.waitDrained(t, podA, 3*time.Minute)
		if h.sessionPodReuse(t, ctx, acme, sessB.ID) {
			t.Error("session B reports podReuse true on a pool that stopped recycling")
		}
	})

	t.Run("idle pod drained by the acquisition", func(t *testing.T) {
		h := newKeptRuntimeHarness(t, "idle", 2)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		acme := h.tenant(t, ctx, "t9-kept-acme")
		podA := keptRuntimeIdleSoloPod(t, ctx, h, acme)

		if res := h.putPool(t, `{"sessionPolicy":{"acknowledgeProcessLevelIsolation":true,`+
			`"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,"maxSessionsPerPod":1},`+
			`"cleanupTimeoutSeconds":30}}`); res.statusCode != 200 {
			t.Fatalf("pool update to maxSessionsPerPod 1: status %d body %q", res.statusCode, res.body)
		}
		sess, err := h.d.CreateAndStart(ctx, acme, h.runtime)
		if err == nil {
			t.Cleanup(func() { _ = h.d.Terminate(context.Background(), acme, sess.ID) })
			if sess.PodAssignment == podA {
				t.Fatalf("the session landed on pod %s, which served a session on a pool outside the rule", podA)
			}
		}
		h.waitDrained(t, podA, 3*time.Minute)
		if !h.gatewayLogsMention(t, "kept_runtime_outside_rule", podA) {
			t.Errorf("no gateway record with reason kept_runtime_outside_rule names pod %s; the drain was not the gateway's", podA)
		}
	})
}

// keptRuntimeIdleSoloPod leaves acme's session-A pod idle and pinned as the
// pool's only idle Sandbox: it runs session A to the reserved hold on a pool
// rendered with maxWarm one above minWarm, so the pinned-idle bound keeps
// one pinned pod when the hold expires, then sets bootstrapMinWarm 0 with
// warmCount unchanged so the unpinned idle pods drain while the bound keeps
// the pinned pod.
func keptRuntimeIdleSoloPod(t *testing.T, ctx context.Context, h *keptRuntimeHarness, tenant string) string {
	t.Helper()
	podA := h.sessionAToReserved(t, ctx, tenant)
	h.waitIdleOrSkip(t, podA, 2*time.Minute)
	h.setBootstrapMinWarm(t, 0)
	deadline := time.Now().Add(4 * time.Minute)
	for {
		out, _ := h.c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxes", "-l", "lenny.dev/pool="+h.pool,
			"-o", `jsonpath={range .items[?(@.status.phase=="idle")]}{.metadata.name}{"\n"}{end}`)
		idle := strings.Fields(out)
		if len(idle) == 1 && idle[0] == podA {
			return podA
		}
		if time.Now().After(deadline) {
			t.Skipf("blocked: pool %s idle pods %v did not settle to the pinned pod %s alone", h.pool, idle, podA)
		}
		time.Sleep(3 * time.Second)
	}
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Postgres-backed fallback claim)
// diagnosis: a runtime process kept across a recycle boundary can serve a
// second tenant.
func TestKeptRuntimePodNeverBindsSecondTenant(t *testing.T) {
	h := newKeptRuntimeHarness(t, "xtenant", 2)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	acme := h.tenant(t, ctx, "t9-kept-acme")
	globex := h.tenant(t, ctx, "t9-kept-globex")
	podA := keptRuntimeIdleSoloPod(t, ctx, h, acme)

	sess, err := h.d.CreateAndStart(ctx, globex, h.runtime)
	if err == nil {
		t.Cleanup(func() { _ = h.d.Terminate(context.Background(), globex, sess.ID) })
		if sess.PodAssignment == podA {
			t.Fatalf("a %s session bound pod %s, which is pinned to %s", globex, podA, acme)
		}
	}
	if got := h.objectField(t, "sandboxclaim", "claim-"+podA, "{.metadata.name}"); got != "" {
		t.Errorf("a SandboxClaim names pod %s, which is pinned to another tenant", podA)
	}
	if got := h.objectField(t, "sandbox", podA, "{.status.phase}"); got != "idle" {
		t.Errorf("pod %s phase = %q, want idle and still pinned for its tenant", podA, got)
	}
}

// gatewayResourceETag reads the ETag header of a GET on path through the
// probe pod.
func gatewayResourceETag(t *testing.T, c *kind.Cluster, pod, gatewayIP, path string, role gwRole) string {
	t.Helper()
	cmd := fmt.Sprintf("curl -sS -m 10 -o /dev/null -D - -H 'X-Lenny-Tenant-ID: %s' -H 'X-Lenny-Roles: %s' "+
		"-H 'X-Lenny-User-ID: %s' http://%s:8080%s", role.tenant, role.roles, role.user, gatewayIP, path)
	// The probe exec is retried briefly: a gateway replica rolling or a
	// transient exec failure yields no headers rather than a verdict.
	var out string
	for attempt := 0; attempt < 10; attempt++ {
		out, _ = c.KubectlOut(t, "-n", lennySystemNS, "exec", pod, "--", "sh", "-c", cmd)
		for _, line := range strings.Split(out, "\n") {
			if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "etag") {
				return strings.TrimSpace(value)
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("GET %s returned no ETag header:\n%s", path, out)
	return ""
}

// gatewayRequestIfMatch runs a gatewayRequest that carries an If-Match
// precondition.
func gatewayRequestIfMatch(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role gwRole, etag, body string) gwResponse {
	t.Helper()
	if strings.ContainsAny(body+etag, "'") {
		t.Fatalf("gatewayRequestIfMatch arguments contain a single quote")
	}
	cmd := fmt.Sprintf("curl -sS -m 10 -X %s -H 'X-Lenny-Tenant-ID: %s' -H 'X-Lenny-Roles: %s' "+
		"-H 'X-Lenny-User-ID: %s' -H 'If-Match: %s' -H 'Content-Type: application/json' --data '%s' "+
		"-w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' http://%s:8080%s 2>&1",
		method, role.tenant, role.roles, role.user, etag, body, gatewayIP, path)
	out, _ := c.KubectlOut(t, "-n", lennySystemNS, "exec", pod, "--", "sh", "-c", cmd)
	return parseGatewayResponse(out)
}
