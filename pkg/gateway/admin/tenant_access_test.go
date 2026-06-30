// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §4 / §15.1 runtime/pool tenant-access endpoints.

func newTenantAccessAdmin(t *testing.T) (*admin.Router, *tenantaccessstore.Memory, *tenantstore.Memory) {
	t.Helper()
	access := tenantaccessstore.NewMemory()
	tenants := tenantstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithTenantAccess(access)
	return router, access, tenants
}

func grantBody(tenantID string) map[string]string { return map[string]string{"tenantId": tenantID} }

func TestGrantRuntimeTenantAccess(t *testing.T) {
	router, access, _ := newTenantAccessAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/runtimes/claude-code/tenant-access", grantBody("acme"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("grant: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	grants, _ := access.List(context.Background(), tenantaccessstore.KindRuntime, "claude-code")
	if len(grants) != 1 || grants[0].TenantID != "acme" {
		t.Errorf("stored grants = %+v, want one for acme", grants)
	}
	if grants[0].GrantedBy != "admin@acme.com" {
		t.Errorf("GrantedBy = %q, want the granting admin's subject", grants[0].GrantedBy)
	}
}

func TestGrantTenantAccessIdempotent(t *testing.T) {
	router, _, _ := newTenantAccessAdmin(t)
	h := router.Handler()
	first := doAdminReq(t, h, http.MethodPost,
		"/v1/admin/runtimes/rt/tenant-access", grantBody("acme"), withAdminPrincipal)
	if first.Code != http.StatusCreated {
		t.Fatalf("first grant: status %d, want 201", first.Code)
	}
	// §15.1: re-granting an existing grant returns 200, not 201.
	second := doAdminReq(t, h, http.MethodPost,
		"/v1/admin/runtimes/rt/tenant-access", grantBody("acme"), withAdminPrincipal)
	if second.Code != http.StatusOK {
		t.Errorf("re-grant: status %d, want 200", second.Code)
	}
}

func TestGrantTenantAccessRejectsMissingTenantId(t *testing.T) {
	router, _, _ := newTenantAccessAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/runtimes/rt/tenant-access", map[string]string{}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("grant without tenantId: status %d, want 400", rr.Code)
	}
}

func TestListRuntimeTenantAccess(t *testing.T) {
	router, access, tenants := newTenantAccessAdmin(t)
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", DisplayName: "Acme Corp"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, "rt", "acme", "admin", time.Now()); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/runtimes/rt/tenant-access", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		TenantAccess []struct {
			TenantID   string `json:"tenantId"`
			TenantName string `json:"tenantName"`
			GrantedBy  string `json:"grantedBy"`
		} `json:"tenantAccess"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.TenantAccess) != 1 {
		t.Fatalf("list: got %d entries, want 1", len(resp.TenantAccess))
	}
	if resp.TenantAccess[0].TenantID != "acme" || resp.TenantAccess[0].TenantName != "Acme Corp" {
		t.Errorf("entry = %+v, want acme / Acme Corp", resp.TenantAccess[0])
	}
}

func TestRevokeRuntimeTenantAccess(t *testing.T) {
	router, access, _ := newTenantAccessAdmin(t)
	ctx := context.Background()
	if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, "rt", "acme", "admin", time.Now()); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/runtimes/rt/tenant-access/acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, want 204", rr.Code)
	}
	grants, _ := access.List(ctx, tenantaccessstore.KindRuntime, "rt")
	if len(grants) != 0 {
		t.Errorf("after revoke: got %d grants, want 0", len(grants))
	}
}

func TestRevokeTenantAccessMissing(t *testing.T) {
	router, _, _ := newTenantAccessAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/runtimes/rt/tenant-access/ghost", nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("revoke missing grant: status %d, want 404", rr.Code)
	}
}

func TestPoolTenantAccess(t *testing.T) {
	router, access, _ := newTenantAccessAdmin(t)
	grant := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/cw-pool/tenant-access", grantBody("globex"), withAdminPrincipal)
	if grant.Code != http.StatusCreated {
		t.Fatalf("pool grant: status %d, want 201; body %s", grant.Code, grant.Body.String())
	}
	// The grant lands under the pool kind, not the runtime kind.
	pool, _ := access.List(context.Background(), tenantaccessstore.KindPool, "cw-pool")
	if len(pool) != 1 || pool[0].TenantID != "globex" {
		t.Errorf("pool grants = %+v, want one for globex", pool)
	}
	rt, _ := access.List(context.Background(), tenantaccessstore.KindRuntime, "cw-pool")
	if len(rt) != 0 {
		t.Error("a pool grant must not appear under the runtime kind")
	}
}

// newScopedResourceAdmin wires the runtime, pool, and tenant-access
// stores so the §4 tenant-scoped runtime/pool reads can be exercised.
func newScopedResourceAdmin(t *testing.T) (*admin.Router, *runtimestore.Memory, *poolstore.Memory, *tenantaccessstore.Memory) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	access := tenantaccessstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(runtimes).WithPools(pools).WithTenantAccess(access)
	return router, runtimes, pools, access
}

func TestListRuntimesTenantScoped(t *testing.T) {
	router, runtimes, _, access := newScopedResourceAdmin(t)
	ctx := context.Background()
	for _, name := range []string{"rt-a", "rt-b", "rt-c"} {
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: name}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
	}
	for _, name := range []string{"rt-a", "rt-b"} {
		if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, name, "acme", "admin", time.Time{}); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}

	// A tenant-admin of acme sees only the granted runtimes.
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin list: status %d, body %s", rr.Code, rr.Body.String())
	}
	var scoped struct {
		Runtimes []admin.RuntimePayload `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &scoped)
	if len(scoped.Runtimes) != 2 {
		t.Errorf("tenant-admin sees %d runtimes, want 2 (granted only)", len(scoped.Runtimes))
	}

	// A platform-admin sees every runtime, unfiltered.
	rrP := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes", nil, withAdminPrincipal)
	var all struct {
		Runtimes []admin.RuntimePayload `json:"items"`
	}
	_ = json.Unmarshal(rrP.Body.Bytes(), &all)
	if len(all.Runtimes) != 3 {
		t.Errorf("platform-admin sees %d runtimes, want 3 (unfiltered)", len(all.Runtimes))
	}
}

func TestGetRuntimeTenantScoped(t *testing.T) {
	router, runtimes, _, access := newScopedResourceAdmin(t)
	ctx := context.Background()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "granted"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "ungranted"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, "granted", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	if rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/granted",
		nil, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Errorf("tenant-admin get granted runtime: status %d, want 200", rr.Code)
	}
	// An ungranted runtime reads as 404 for the tenant-admin.
	if rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/ungranted",
		nil, withTenantAdminPrincipal); rr.Code != http.StatusNotFound {
		t.Errorf("tenant-admin get ungranted runtime: status %d, want 404", rr.Code)
	}
	// The platform-admin reads the ungranted runtime fine.
	if rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/ungranted",
		nil, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Errorf("platform-admin get: status %d, want 200", rr.Code)
	}
}

func TestListPoolsTenantScoped(t *testing.T) {
	router, _, pools, access := newScopedResourceAdmin(t)
	ctx := context.Background()
	for _, name := range []string{"pool-a", "pool-b", "pool-c"} {
		if err := pools.Create(ctx, poolstore.Pool{Name: name}); err != nil {
			t.Fatalf("seed pool: %v", err)
		}
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindPool, "pool-a", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin list: status %d, body %s", rr.Code, rr.Body.String())
	}
	var scoped struct {
		Pools []admin.PoolPayload `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &scoped)
	if len(scoped.Pools) != 1 || scoped.Pools[0].Name != "pool-a" {
		t.Errorf("tenant-admin pools = %+v, want only pool-a", scoped.Pools)
	}
}

func TestGetPoolTenantScoped(t *testing.T) {
	router, _, pools, _ := newScopedResourceAdmin(t)
	ctx := context.Background()
	if err := pools.Create(ctx, poolstore.Pool{Name: "ungranted"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// §15.1: a pool not in the caller's access table reads as 404.
	if rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/ungranted",
		nil, withTenantAdminPrincipal); rr.Code != http.StatusNotFound {
		t.Errorf("tenant-admin get ungranted pool: status %d, want 404", rr.Code)
	}
	if rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/ungranted",
		nil, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Errorf("platform-admin get: status %d, want 200", rr.Code)
	}
}

// TestGrantTenantAccess404OnMissingRuntime asserts the Grant handler
// pre-validates the runtime exists before mutating the join-table
// state; the Memory tenantaccessstore previously accepted any
// non-empty resource name, which let an operator stash a dangling
// grant for a non-existent runtime. The corrected handler rejects the
// request with 404 RESOURCE_NOT_FOUND naming the missing runtime in
// details.runtime. spec: §15.1 line 779 (`runtime_tenant_access`
// FK reference). F-24.3.6.
func TestGrantTenantAccess404OnMissingRuntime_spec_15_1_779(t *testing.T) {
	router, runtimes, _, access := newScopedResourceAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "exists"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/runtimes/ghost/tenant-access", grantBody("acme"), withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("grant for missing runtime: status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("error code = %q, want RESOURCE_NOT_FOUND", env.Error.Code)
	}
	if got := env.Error.Details["runtime"]; got != "ghost" {
		t.Errorf("details.runtime = %v, want ghost", got)
	}
	// And no grant was written under the missing name.
	grants, _ := access.List(context.Background(), tenantaccessstore.KindRuntime, "ghost")
	if len(grants) != 0 {
		t.Errorf("dangling grant persisted: %+v", grants)
	}
	// Existing runtime still admits the grant.
	if rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/runtimes/exists/tenant-access", grantBody("acme"), withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Errorf("grant on existing runtime: status %d, want 201", rr.Code)
	}
}

// TestGrantTenantAccess404OnMissingPool mirrors the runtime guard for
// the pool kind. spec: §15.1 line 802 (`pool_tenant_access` FK
// reference). F-24.3.6.
func TestGrantTenantAccess404OnMissingPool_spec_15_1_802(t *testing.T) {
	router, _, _, _ := newScopedResourceAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/ghost-pool/tenant-access", grantBody("acme"), withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("grant for missing pool: status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("error code = %q, want RESOURCE_NOT_FOUND", env.Error.Code)
	}
	if got := env.Error.Details["pool"]; got != "ghost-pool" {
		t.Errorf("details.pool = %v, want ghost-pool", got)
	}
}

// TestTenantAccessAuditEventsUseSection167Naming asserts the emitted
// audit event names follow the §16.7 `<domain>.<verb_object>`
// convention used by adjacent §15.1 emits such as
// `pool.reconciliation_resumed`. The previous implementation emitted
// `admin.runtime.tenant_access_granted` / `admin.pool.tenant_access_revoked`,
// which a §16.7 domain-based SIEM rule would route into the `admin.*`
// bucket rather than the per-resource domain. spec: §16.7 audit
// catalog conventions. F-24.3.7.
func TestTenantAccessAuditEventsUseSection167Naming_spec_16_7(t *testing.T) {
	tenants := tenantstore.NewMemory()
	access := tenantaccessstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "rt-a"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{Name: "pool-a"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithPools(pools).WithTenantAccess(access)
	h := router.Handler()

	if rr := doAdminReq(t, h, http.MethodPost,
		"/v1/admin/runtimes/rt-a/tenant-access", grantBody("acme"), withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("runtime grant: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doAdminReq(t, h, http.MethodPost,
		"/v1/admin/pools/pool-a/tenant-access", grantBody("acme"), withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("pool grant: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doAdminReq(t, h, http.MethodDelete,
		"/v1/admin/runtimes/rt-a/tenant-access/acme", nil, withAdminPrincipal); rr.Code != http.StatusNoContent {
		t.Fatalf("runtime revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doAdminReq(t, h, http.MethodDelete,
		"/v1/admin/pools/pool-a/tenant-access/acme", nil, withAdminPrincipal); rr.Code != http.StatusNoContent {
		t.Fatalf("pool revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	expected := map[string]bool{
		"runtime.tenant_access_granted": false,
		"pool.tenant_access_granted":    false,
		"runtime.tenant_access_revoked": false,
		"pool.tenant_access_revoked":    false,
	}
	for _, ev := range audit.snapshot() {
		if _, ok := expected[ev.Type]; ok {
			expected[ev.Type] = true
		}
		// The legacy `admin.<kind>.tenant_access_*` form must not be
		// emitted alongside the §16.7 form.
		if ev.Type == "admin.runtime.tenant_access_granted" ||
			ev.Type == "admin.pool.tenant_access_granted" ||
			ev.Type == "admin.runtime.tenant_access_revoked" ||
			ev.Type == "admin.pool.tenant_access_revoked" {
			t.Errorf("legacy admin-prefixed event still emitted: %s", ev.Type)
		}
	}
	for name, seen := range expected {
		if !seen {
			t.Errorf("expected audit event %q not emitted; got %+v", name, audit.snapshot())
		}
	}
}

func TestTenantAccessRequiresPlatformAdmin(t *testing.T) {
	router, _, _ := newTenantAccessAdmin(t)
	h := router.Handler()
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/admin/runtimes/rt/tenant-access"},
		{http.MethodGet, "/v1/admin/runtimes/rt/tenant-access"},
		{http.MethodDelete, "/v1/admin/runtimes/rt/tenant-access/acme"},
		{http.MethodPost, "/v1/admin/pools/p/tenant-access"},
	} {
		rr := doAdminReq(t, h, c.method, c.path, grantBody("acme"), withTenantAdminPrincipal)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s as tenant-admin: status %d, want 403", c.method, c.path, rr.Code)
		}
	}
}
