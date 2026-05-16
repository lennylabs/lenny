// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
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
