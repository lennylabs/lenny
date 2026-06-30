// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.2 RBAC permission matrix; §15.1 admin-API auth requirements.
//
// These tests reconcile the admin gateway's authorization against the
// §10.2 matrix for the routes whose gate was widened from the
// platform-admin-only requireAdmin to a permission-aware gate:
//
//   - Runtime / pool update: §10.2 "Manage runtimes" / "Manage pools"
//     grants tenant-admin "Yes (own tenant)"; create/delete stay
//     platform-admin only per §15.1.
//   - Delegation-policy CRUD: §10.2 "Manage delegation policies" grants
//     tenant-admin "Yes (own tenant)".

// newReconcileAdmin wires the runtime, pool, delegation-policy,
// tenant-access, and custom-role stores so every reconciled route and
// the custom-role / cross-tenant cases can be exercised.
func newReconcileAdmin(t *testing.T) (*admin.Router, *runtimestore.Memory, *poolstore.Memory, *delegationpolicystore.Memory, *tenantaccessstore.Memory, *customrolestore.Memory) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	access := tenantaccessstore.NewMemory()
	roles := customrolestore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).
		WithRuntimes(runtimes).
		WithPools(pools).
		WithDelegationPolicies(policies).
		WithTenantAccess(access).
		WithCustomRoles(roles)
	return router, runtimes, pools, policies, access, roles
}

// authCase is one (role, route) authorization expectation.
type authCase struct {
	role         string
	as           func(*http.Request) *http.Request
	method, path string
	body         any
	wantStatus   int // exact status, or 0 to assert "not 403"
	wantNot403   bool
}

// TestReconciledRuntimeRouteAuthorization checks every built-in role
// against the runtime mutation routes. The §10.2 matrix grants "Manage
// runtimes" to platform-admin (all tenants) and tenant-admin (own
// tenant) and denies it to tenant-viewer, billing-viewer, and user.
// §15.1 reserves runtime create/delete to platform-admin.
func TestReconciledRuntimeRouteAuthorization(t *testing.T) {
	for _, c := range []authCase{
		// platform-admin: every route runs.
		{"platform-admin", withAdminPrincipal, http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{Name: "fresh", Image: "lenny/fresh@sha256:abc", Labels: map[string]string{"tier": "test"}}, http.StatusCreated, false},
		{"platform-admin", withAdminPrincipal, http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}, http.StatusOK, false},
		{"platform-admin", withAdminPrincipal, http.MethodDelete, "/v1/admin/runtimes/echo", nil, http.StatusNoContent, false},
		// tenant-admin: create/delete forbidden; update reachable
		// (granted runtime — see seeding below).
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{Name: "x"}, http.StatusForbidden, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodDelete, "/v1/admin/runtimes/echo", nil, http.StatusForbidden, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}, 0, true},
		// tenant-viewer / billing-viewer / user: update forbidden (no
		// manage_runtimes permission).
		{"tenant-viewer", withTenantViewerPrincipal, http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}, http.StatusForbidden, false},
		{"billing-viewer", withBillingViewerPrincipal, http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}, http.StatusForbidden, false},
		{"user", withUserPrincipal, http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}, http.StatusForbidden, false},
	} {
		router, runtimes, _, _, access, _ := newReconcileAdmin(t)
		if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		// Grant acme access to "echo" so the tenant-admin PUT is
		// in-scope (an out-of-scope PUT is covered separately).
		if _, err := access.Grant(context.Background(), tenantaccessstore.KindRuntime, "echo", "acme", "admin", time.Time{}); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, c.as)
		assertAuth(t, c, rr.Code, rr.Body.String())
	}
}

// TestReconciledPoolRouteAuthorization mirrors the runtime test for the
// §10.2 "Manage pools / scaling policies" matrix row.
func TestReconciledPoolRouteAuthorization(t *testing.T) {
	for _, c := range []authCase{
		{"platform-admin", withAdminPrincipal, http.MethodPost, "/v1/admin/pools", admin.PoolPayload{Name: "fresh", RuntimeRef: "echo"}, http.StatusCreated, false},
		{"platform-admin", withAdminPrincipal, http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}, http.StatusOK, false},
		{"platform-admin", withAdminPrincipal, http.MethodDelete, "/v1/admin/pools/p", nil, http.StatusNoContent, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPost, "/v1/admin/pools", admin.PoolPayload{Name: "x"}, http.StatusForbidden, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodDelete, "/v1/admin/pools/p", nil, http.StatusForbidden, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}, 0, true},
		{"tenant-viewer", withTenantViewerPrincipal, http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}, http.StatusForbidden, false},
		{"billing-viewer", withBillingViewerPrincipal, http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}, http.StatusForbidden, false},
		{"user", withUserPrincipal, http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}, http.StatusForbidden, false},
	} {
		router, runtimes, pools, _, access, _ := newReconcileAdmin(t)
		if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		if err := pools.Create(context.Background(), poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
			t.Fatalf("seed pool: %v", err)
		}
		if _, err := access.Grant(context.Background(), tenantaccessstore.KindPool, "p", "acme", "admin", time.Time{}); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, c.as)
		assertAuth(t, c, rr.Code, rr.Body.String())
	}
}

// TestReconciledDelegationPolicyRouteAuthorization checks every
// built-in role against the delegation-policy CRUD. The §10.2 matrix
// grants "Manage delegation policies" to platform-admin and
// tenant-admin and denies it to tenant-viewer, billing-viewer, and
// user.
func TestReconciledDelegationPolicyRouteAuthorization(t *testing.T) {
	for _, c := range []authCase{
		{"platform-admin", withAdminPrincipal, http.MethodPost, "/v1/admin/delegation-policies", validDelegationPolicy("np"), 0, true},
		{"platform-admin", withAdminPrincipal, http.MethodGet, "/v1/admin/delegation-policies", nil, http.StatusOK, false},
		{"platform-admin", withAdminPrincipal, http.MethodPut, "/v1/admin/delegation-policies/p1", validDelegationPolicy("p1"), http.StatusOK, false},
		{"platform-admin", withAdminPrincipal, http.MethodDelete, "/v1/admin/delegation-policies/p1", nil, http.StatusNoContent, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPost, "/v1/admin/delegation-policies", validDelegationPolicy("np"), 0, true},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodGet, "/v1/admin/delegation-policies", nil, http.StatusOK, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodPut, "/v1/admin/delegation-policies/p1", validDelegationPolicy("p1"), http.StatusOK, false},
		{"tenant-admin", withTenantAdminPrincipal, http.MethodDelete, "/v1/admin/delegation-policies/p1", nil, http.StatusNoContent, false},
		{"tenant-viewer", withTenantViewerPrincipal, http.MethodGet, "/v1/admin/delegation-policies", nil, http.StatusForbidden, false},
		{"tenant-viewer", withTenantViewerPrincipal, http.MethodPut, "/v1/admin/delegation-policies/p1", validDelegationPolicy("p1"), http.StatusForbidden, false},
		{"billing-viewer", withBillingViewerPrincipal, http.MethodGet, "/v1/admin/delegation-policies", nil, http.StatusForbidden, false},
		{"user", withUserPrincipal, http.MethodGet, "/v1/admin/delegation-policies", nil, http.StatusForbidden, false},
		{"user", withUserPrincipal, http.MethodPost, "/v1/admin/delegation-policies", validDelegationPolicy("np"), http.StatusForbidden, false},
	} {
		router, _, _, policies, _, _ := newReconcileAdmin(t)
		// Seed under both `platform` (platform-admin reads/writes) and
		// `acme` (tenant-admin reads/writes), so the §4.2 line 172
		// per-tenant policy registry has a row resolvable from either
		// principal.
		if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
			t.Fatalf("seed policy (platform): %v", err)
		}
		if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "acme", Name: "p1"}); err != nil {
			t.Fatalf("seed policy (acme): %v", err)
		}
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, c.as)
		assertAuth(t, c, rr.Code, rr.Body.String())
	}
}

// TestReconciledRouteCustomRoleGrant verifies the §10.2 custom-role
// path: a caller holding a tenant custom role whose permission set
// includes the reconciled route's permission is admitted, and a custom
// role that lacks the permission is denied.
func TestReconciledRouteCustomRoleGrant(t *testing.T) {
	router, runtimes, pools, policies, access, roles := newReconcileAdmin(t)
	ctx := context.Background()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pools.Create(ctx, poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed policy (platform): %v", err)
	}
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "acme", Name: "p1"}); err != nil {
		t.Fatalf("seed policy (acme): %v", err)
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, "echo", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed runtime grant: %v", err)
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindPool, "p", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed pool grant: %v", err)
	}
	// A custom role that grants the three reconciled manage permissions.
	if err := roles.Create(ctx, customrolestore.CustomRole{
		TenantID: "acme",
		Name:     "resource-admin",
		Permissions: []pkgauth.Permission{
			pkgauth.PermManageRuntimes,
			pkgauth.PermManagePools,
			pkgauth.PermManageDelegationPolicies,
		},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	// A custom role that grants only an unrelated permission.
	if err := roles.Create(ctx, customrolestore.CustomRole{
		TenantID:    "acme",
		Name:        "usage-only",
		Permissions: []pkgauth.Permission{pkgauth.PermViewUsage},
	}); err != nil {
		t.Fatalf("seed usage-only role: %v", err)
	}

	granting := withCustomRolePrincipal("resource-admin")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}},
		{http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}},
		{http.MethodGet, "/v1/admin/delegation-policies", nil},
		{http.MethodPut, "/v1/admin/delegation-policies/p1", validDelegationPolicy("p1")},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, granting)
		if rr.Code == http.StatusForbidden {
			t.Errorf("custom role resource-admin %s %s: got 403, want the route to run", c.method, c.path)
		}
	}

	// usage-only lacks every reconciled manage permission → 403.
	denying := withCustomRolePrincipal("usage-only")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}},
		{http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}},
		{http.MethodGet, "/v1/admin/delegation-policies", nil},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, denying)
		if rr.Code != http.StatusForbidden {
			t.Errorf("custom role usage-only %s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}

// TestReconciledRouteCrossTenantRejection verifies the §15.1 access-
// table scoping: a tenant-admin of one tenant cannot update a runtime
// or pool granted only to a different tenant. The §4 scoping reports
// the out-of-scope resource as absent (404) so the gate does not
// disclose its existence.
func TestReconciledRouteCrossTenantRejection(t *testing.T) {
	router, runtimes, pools, _, access, _ := newReconcileAdmin(t)
	ctx := context.Background()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pools.Create(ctx, poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	// Both resources are granted to acme only.
	if _, err := access.Grant(ctx, tenantaccessstore.KindRuntime, "echo", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed runtime grant: %v", err)
	}
	if _, err := access.Grant(ctx, tenantaccessstore.KindPool, "p", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("seed pool grant: %v", err)
	}

	// A tenant-admin of globex (no grant) is scoped out → 404.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}},
		{http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, withForeignTenantAdminPrincipal)
		if rr.Code != http.StatusNotFound {
			t.Errorf("foreign tenant-admin %s %s: got %d, want 404 (out of access-table scope)", c.method, c.path, rr.Code)
		}
	}

	// The acme tenant-admin holds the grant → the route runs.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}},
		{http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, withTenantAdminPrincipal)
		if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
			t.Errorf("granted acme tenant-admin %s %s: got %d, want the route to run", c.method, c.path, rr.Code)
		}
	}

	// A platform-admin is unscoped and reaches every resource.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo", admin.UpdateRuntimeRequest{}},
		{http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{}},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, c.body, withAdminPrincipal)
		if rr.Code != http.StatusOK {
			t.Errorf("platform-admin %s %s: got %d, want 200 (unscoped)", c.method, c.path, rr.Code)
		}
	}
}

// TestReconciledRouteRejectsUnauthenticated confirms the reconciled
// gates fail closed for a request with no authenticated principal.
func TestReconciledRouteRejectsUnauthenticated(t *testing.T) {
	router, runtimes, pools, policies, _, _ := newReconcileAdmin(t)
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{Name: "echo"})
	_ = pools.Create(ctx, poolstore.Pool{Name: "p"})
	_ = policies.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"})
	noPrincipal := func(req *http.Request) *http.Request { return req }
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodPut, "/v1/admin/runtimes/echo"},
		{http.MethodPut, "/v1/admin/pools/p"},
		{http.MethodGet, "/v1/admin/delegation-policies"},
		{http.MethodPut, "/v1/admin/delegation-policies/p1"},
	} {
		rr := doAdminReq(t, router.Handler(), c.method, c.path, map[string]any{}, noPrincipal)
		if rr.Code != http.StatusForbidden {
			t.Errorf("unauthenticated %s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}

// assertAuth checks an authCase outcome.
func assertAuth(t *testing.T, c authCase, gotStatus int, body string) {
	t.Helper()
	if c.wantNot403 {
		if gotStatus == http.StatusForbidden {
			t.Errorf("%s %s as %s: got 403, want the route to run; body=%s", c.method, c.path, c.role, body)
		}
		return
	}
	if gotStatus != c.wantStatus {
		t.Errorf("%s %s as %s: got %d, want %d; body=%s", c.method, c.path, c.role, gotStatus, c.wantStatus, body)
	}
}
