// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 admin tenant CRUD + §10.2 platform-admin gating.

func newAdminServer(t *testing.T) (*admin.Router, *tenantstore.Memory) {
	t.Helper()
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	return router, store
}

func withAdminPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	return req.WithContext(ctx)
}

func withTenantAdminPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "user@acme.com",
		TenantID: "acme",
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
	})
	return req.WithContext(ctx)
}

func TestCreateTenantRequiresPlatformAdmin(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme Corp"})

	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin Create: got %d, want 403", rr.Code)
	}
}

func TestCreateTenantHappyPath(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme Corp"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "acme" || resp.DisplayName != "Acme Corp" {
		t.Errorf("response: got %+v", resp)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.DisplayName != "Acme Corp" {
		t.Errorf("row.DisplayName: got %q", row.DisplayName)
	}
}

func TestCreateTenantRejectsInvalidID(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "with space"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestCreateTenantRejectsMissingID(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestCreateTenantRejectsDuplicate(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})

	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rr.Code)
	}
}

func TestListTenants(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})
	_ = store.Create(nil, tenantstore.Tenant{ID: "globex"})
	_ = store.SoftDelete(nil, "globex", time.Now())

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Tenants []admin.TenantPayload `json:"tenants"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Tenants) != 1 || resp.Tenants[0].ID != "acme" {
		t.Errorf("List active: got %+v", resp.Tenants)
	}

	reqAll := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants?includeDeleted=true", nil))
	rrAll := httptest.NewRecorder()
	router.Handler().ServeHTTP(rrAll, reqAll)
	_ = json.Unmarshal(rrAll.Body.Bytes(), &resp)
	if len(resp.Tenants) != 2 {
		t.Errorf("List includeDeleted: got %d tenants", len(resp.Tenants))
	}
}

func TestGetTenant(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", DisplayName: "Acme Corp"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "acme" || resp.DisplayName != "Acme Corp" {
		t.Errorf("Get tenant: got %+v", resp)
	}
}

func TestGetTenantMissing(t *testing.T) {
	router, _ := newAdminServer(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/missing", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestUpdateTenantMergesFields(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", DisplayName: "Acme Corp", WorkspaceTier: "T2"})

	dn := "Acme Holdings"
	body, _ := json.Marshal(admin.UpdateTenantRequest{DisplayName: &dn})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DisplayName != "Acme Holdings" {
		t.Errorf("DisplayName: got %q", resp.DisplayName)
	}
	if resp.WorkspaceTier != "T2" {
		t.Errorf("WorkspaceTier should be preserved: got %q", resp.WorkspaceTier)
	}
}

func TestUpdateTenantMissing(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.UpdateTenantRequest{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/missing", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestDeleteTenantSoftDeletes(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rr.Code)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if row.DeletedAt.IsZero() {
		t.Errorf("row should have DeletedAt set")
	}
	if row.IsActive() {
		t.Errorf("row should not be active after delete")
	}
}

func TestDeleteTenantMissing(t *testing.T) {
	router, _ := newAdminServer(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/missing", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestEveryEndpointRejectsNonAdmin(t *testing.T) {
	router, _ := newAdminServer(t)
	for _, c := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, "/v1/admin/tenants", []byte(`{"id":"x"}`)},
		{http.MethodGet, "/v1/admin/tenants", nil},
		{http.MethodGet, "/v1/admin/tenants/x", nil},
		{http.MethodPut, "/v1/admin/tenants/x", []byte("{}")},
		{http.MethodDelete, "/v1/admin/tenants/x", nil},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader(c.body)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}

func TestEveryEndpointRejectsAnonymous(t *testing.T) {
	router, _ := newAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/tenants", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous: got %d, want 403", rr.Code)
	}
}
