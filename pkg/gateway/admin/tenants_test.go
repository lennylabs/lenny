// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// recordingAudit captures admin audit events so tests can verify
// emission shape per §11.7.
type recordingAudit struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

func (r *recordingAudit) EmitAdminEvent(_ context.Context, ev admin.AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingAudit) snapshot() []admin.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]admin.AuditEvent(nil), r.events...)
}

// spec: §15.1 admin tenant CRUD + §10.2 platform-admin gating.

func newAdminServer(t *testing.T) (*admin.Router, *tenantstore.Memory) {
	t.Helper()
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		},
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

func TestCreateTenantWithConcurrentSessionQuota(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", MaxConcurrentSessions: 10})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.MaxConcurrentSessions != 10 {
		t.Errorf("response maxConcurrentSessions: got %d, want 10", resp.MaxConcurrentSessions)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.MaxConcurrentSessions != 10 {
		t.Errorf("stored maxConcurrentSessions: got %d, want 10", row.MaxConcurrentSessions)
	}
}

func TestCreateTenantRejectsNegativeConcurrentSessionQuota(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", MaxConcurrentSessions: -1})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("negative maxConcurrentSessions: got %d, want 400", rr.Code)
	}
}

func TestCreateTenantWithStorageQuota(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", StorageQuotaBytes: 5 << 30})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.StorageQuotaBytes != 5<<30 {
		t.Errorf("response storageQuotaBytes: got %d, want %d", resp.StorageQuotaBytes, 5<<30)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.StorageQuotaBytes != 5<<30 {
		t.Errorf("stored storageQuotaBytes: got %d, want %d", row.StorageQuotaBytes, 5<<30)
	}
}

func TestCreateTenantRejectsNegativeStorageQuota(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", StorageQuotaBytes: -1})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("negative storageQuotaBytes: got %d, want 400", rr.Code)
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

func TestAuditEmissionOnTenantMutations(t *testing.T) {
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	})

	// Create
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// Update
	dn := "Acme Inc"
	body, _ = json.Marshal(admin.UpdateTenantRequest{DisplayName: &dn})
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// Delete
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil),
	))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}

	events := audit.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events, got %d (%+v)", len(events), events)
	}
	want := []string{"admin.tenant.created", "admin.tenant.updated", "admin.tenant.soft_deleted"}
	for i, w := range want {
		if events[i].Type != w {
			t.Errorf("events[%d].Type: got %q, want %q", i, events[i].Type, w)
		}
		if events[i].TargetResource != "acme" {
			t.Errorf("events[%d].TargetResource: got %q", i, events[i].TargetResource)
		}
		if events[i].ActorSubject != "admin@acme.com" {
			t.Errorf("events[%d].ActorSubject: got %q", i, events[i].ActorSubject)
		}
	}
}

func TestNoAuditEmissionOnReads(t *testing.T) {
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{Audit: audit})
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})

	for _, m := range []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/tenants"},
		{http.MethodGet, "/v1/admin/tenants/acme"},
	} {
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(m.method, m.path, nil)))
	}
	if got := len(audit.snapshot()); got != 0 {
		t.Errorf("audit emission on reads: got %d events, want 0", got)
	}
}

func TestNoAuditEmissionOnFailures(t *testing.T) {
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{Audit: audit})

	// Invalid ID — Create fails.
	body, _ := json.Marshal(admin.TenantPayload{ID: "with space"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create failure: status %d", rr.Code)
	}
	if got := len(audit.snapshot()); got != 0 {
		t.Errorf("audit on failure: got %d events, want 0", got)
	}
}
