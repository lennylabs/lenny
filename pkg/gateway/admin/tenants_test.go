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
	"github.com/lennylabs/lenny/pkg/experiment"
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

func TestCreateTenantWithExperimentTargeting(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{
		ID: "acme",
		ExperimentTargeting: &experiment.TargetingConfig{
			Provider:  experiment.TargetingProviderOFREP,
			TimeoutMs: 250,
			OFREP:     &experiment.OFREPConfig{Endpoint: "https://flags.internal/ofrep"},
		},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ExperimentTargeting == nil || resp.ExperimentTargeting.Provider != experiment.TargetingProviderOFREP {
		t.Fatalf("response experimentTargeting = %+v, want an ofrep config", resp.ExperimentTargeting)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if !row.ExperimentTargeting.Configured() || row.ExperimentTargeting.OFREP == nil ||
		row.ExperimentTargeting.OFREP.Endpoint != "https://flags.internal/ofrep" {
		t.Errorf("stored experimentTargeting = %+v", row.ExperimentTargeting)
	}
}

func TestCreateTenantRejectsInvalidExperimentTargeting(t *testing.T) {
	router, _ := newAdminServer(t)
	// provider ofrep with no ofrep block fails §10.7 validation.
	body, _ := json.Marshal(admin.TenantPayload{
		ID:                  "acme",
		ExperimentTargeting: &experiment.TargetingConfig{Provider: experiment.TargetingProviderOFREP},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for an invalid experimentTargeting; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateTenantSetsExperimentTargeting(t *testing.T) {
	router, store := newAdminServer(t)
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	body, _ := json.Marshal(admin.UpdateTenantRequest{
		ExperimentTargeting: &experiment.TargetingConfig{
			Provider:     experiment.TargetingProviderLaunchDarkly,
			LaunchDarkly: &experiment.LaunchDarklyConfig{SDKKey: "sdk-1"},
		},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(req.Context(), "acme")
	if row.ExperimentTargeting.Provider != experiment.TargetingProviderLaunchDarkly {
		t.Errorf("stored provider = %q, want launchdarkly", row.ExperimentTargeting.Provider)
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

func TestCreateTenantWithMinIsolationProfile(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", MinIsolationProfile: "sandboxed"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.MinIsolationProfile != "sandboxed" {
		t.Errorf("response minIsolationProfile = %q, want sandboxed", resp.MinIsolationProfile)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.MinIsolationProfile != "sandboxed" {
		t.Errorf("stored minIsolationProfile = %q, want sandboxed", row.MinIsolationProfile)
	}
}

func TestCreateTenantRejectsBadMinIsolationProfile(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", MinIsolationProfile: "ultra"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid minIsolationProfile: status %d, want 400", rr.Code)
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

// spec: §12.8 billingErasurePolicy and the
// compliance.billing_erasure_exempt_regulated audit event.

// newAuditedAdminServer builds a tenant admin router with a recording
// audit sink wired.
func newAuditedAdminServer(t *testing.T) (*admin.Router, *recordingAudit) {
	t.Helper()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	})
	return router, audit
}

// findAuditEvent returns the first recorded event of the given type.
func findAuditEvent(events []admin.AuditEvent, eventType string) (admin.AuditEvent, bool) {
	for _, ev := range events {
		if ev.Type == eventType {
			return ev, true
		}
	}
	return admin.AuditEvent{}, false
}

func TestCreateTenantWithBillingErasurePolicy(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", BillingErasurePolicy: "exempt"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.BillingErasurePolicy != "exempt" {
		t.Errorf("response billingErasurePolicy = %q, want exempt", resp.BillingErasurePolicy)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.BillingErasurePolicy != "exempt" {
		t.Errorf("stored billingErasurePolicy = %q, want exempt", row.BillingErasurePolicy)
	}
}

func TestCreateTenantRejectsBadBillingErasurePolicy(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", BillingErasurePolicy: "wipe"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid billingErasurePolicy: status %d, want 400", rr.Code)
	}
}

func TestCreateTenantExemptRegulatedEmitsComplianceEvent(t *testing.T) {
	router, audit := newAuditedAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{
		ID: "acme", BillingErasurePolicy: "exempt", ComplianceProfile: "hipaa",
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	// §12.8: the combination is permitted (a 2xx response).
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	ev, ok := findAuditEvent(audit.snapshot(), "compliance.billing_erasure_exempt_regulated")
	if !ok {
		t.Fatal("an exempt tenant with a regulated compliance profile must emit compliance.billing_erasure_exempt_regulated")
	}
	if ev.Detail["complianceProfile"] != "hipaa" || ev.Detail["billingErasurePolicy"] != "exempt" {
		t.Errorf("event detail = %v, want complianceProfile=hipaa billingErasurePolicy=exempt", ev.Detail)
	}
}

func TestCreateTenantExemptNonRegulatedNoComplianceEvent(t *testing.T) {
	router, audit := newAuditedAdminServer(t)
	// gdpr is not one of the §12.8 regulated profiles (hipaa/fedramp/soc2).
	body, _ := json.Marshal(admin.TenantPayload{
		ID: "acme", BillingErasurePolicy: "exempt", ComplianceProfile: "gdpr",
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201", rr.Code)
	}
	if _, ok := findAuditEvent(audit.snapshot(), "compliance.billing_erasure_exempt_regulated"); ok {
		t.Error("a non-regulated compliance profile must not emit the exempt-regulated event")
	}
}

func TestUpdateTenantToExemptRegulatedEmitsComplianceEvent(t *testing.T) {
	router, audit := newAuditedAdminServer(t)
	createBody, _ := json.Marshal(admin.TenantPayload{ID: "acme", ComplianceProfile: "fedramp"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(createBody)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}

	policy := "exempt"
	updBody, _ := json.Marshal(admin.UpdateTenantRequest{BillingErasurePolicy: &policy})
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(updBody)),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, ok := findAuditEvent(audit.snapshot(), "compliance.billing_erasure_exempt_regulated"); !ok {
		t.Error("updating a fedramp tenant to billingErasurePolicy=exempt must emit the compliance event")
	}
}

func TestUpdateTenantRejectsBadBillingErasurePolicy(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})

	bad := "scramble"
	body, _ := json.Marshal(admin.UpdateTenantRequest{BillingErasurePolicy: &bad})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid billingErasurePolicy on update: status %d, want 400", rr.Code)
	}
}

func TestEmitBillingErasureExemptRegulatedStartup(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "acme", BillingErasurePolicy: "exempt", ComplianceProfile: "hipaa"})
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "umbrella", BillingErasurePolicy: "exempt", ComplianceProfile: "fedramp"})
	// gdpr is not a regulated profile; pseudonymize is not exempt — neither qualifies.
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "globex", BillingErasurePolicy: "exempt", ComplianceProfile: "gdpr"})
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "initech", BillingErasurePolicy: "pseudonymize", ComplianceProfile: "soc2"})
	// A soft-deleted exempt+regulated tenant must be skipped.
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "soylent", BillingErasurePolicy: "exempt", ComplianceProfile: "soc2"})
	if err := tenants.SoftDelete(ctx, "soylent", time.Now()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	audit := &recordingAudit{}
	if err := admin.EmitBillingErasureExemptRegulatedStartup(ctx, tenants, audit, nil); err != nil {
		t.Fatalf("startup scan: %v", err)
	}

	got := map[string]bool{}
	for _, ev := range audit.snapshot() {
		if ev.Type != "compliance.billing_erasure_exempt_regulated" {
			t.Errorf("unexpected event type %q", ev.Type)
			continue
		}
		got[ev.TargetResource] = true
	}
	want := map[string]bool{"acme": true, "umbrella": true}
	if len(got) != len(want) {
		t.Fatalf("startup events emitted for %v, want exactly %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("no startup event for exempt+regulated tenant %q", id)
		}
	}
}

func TestEmitBillingErasureExemptRegulatedStartupNilSink(t *testing.T) {
	// A nil audit sink is a no-op rather than a panic.
	if err := admin.EmitBillingErasureExemptRegulatedStartup(
		context.Background(), tenantstore.NewMemory(), nil, nil,
	); err != nil {
		t.Errorf("nil sink: got %v, want nil", err)
	}
}

// spec: §11.7 compliance profile downgrade ratchet
// (none < soc2 < fedramp < hipaa).

// putComplianceProfile issues a PUT that sets only complianceProfile.
func putComplianceProfile(t *testing.T, h http.Handler, id, profile string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(admin.UpdateTenantRequest{ComplianceProfile: &profile})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/"+id, bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUpdateTenantRejectsComplianceDowngrade(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})

	rr := putComplianceProfile(t, router.Handler(), "acme", "soc2")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hipaa->soc2 downgrade: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED" {
		t.Errorf("error code = %q, want COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED", env.Error.Code)
	}
	if env.Error.Details["currentProfile"] != "hipaa" || env.Error.Details["requestedProfile"] != "soc2" {
		t.Errorf("details = %v, want currentProfile=hipaa requestedProfile=soc2", env.Error.Details)
	}
	// §11.7: the stored profile is unchanged by a rejected downgrade.
	row, _ := store.Get(context.Background(), "acme")
	if row.ComplianceProfile != "hipaa" {
		t.Errorf("stored complianceProfile = %q, want hipaa (unchanged)", row.ComplianceProfile)
	}
}

func TestUpdateTenantAllowsComplianceUpgrade(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "soc2"})

	rr := putComplianceProfile(t, router.Handler(), "acme", "hipaa")
	if rr.Code != http.StatusOK {
		t.Fatalf("soc2->hipaa upgrade: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ComplianceProfile != "hipaa" {
		t.Errorf("stored complianceProfile = %q, want hipaa", row.ComplianceProfile)
	}
}

func TestUpdateTenantAllowsSameComplianceProfile(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "fedramp"})

	// Re-asserting the same profile is not a downgrade.
	rr := putComplianceProfile(t, router.Handler(), "acme", "fedramp")
	if rr.Code != http.StatusOK {
		t.Errorf("fedramp->fedramp re-assert: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateTenantComplianceRatchetIgnoresOffLadderProfile(t *testing.T) {
	router, store := newAdminServer(t)
	// gdpr is not on the none<soc2<fedramp<hipaa ladder, so the ratchet
	// does not constrain transitions involving it.
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "gdpr"})
	if rr := putComplianceProfile(t, router.Handler(), "acme", "none"); rr.Code != http.StatusOK {
		t.Errorf("gdpr->none: status %d, want 200 (gdpr is off-ladder)", rr.Code)
	}

	_ = store.Create(nil, tenantstore.Tenant{ID: "globex", ComplianceProfile: "hipaa"})
	if rr := putComplianceProfile(t, router.Handler(), "globex", "gdpr"); rr.Code != http.StatusOK {
		t.Errorf("hipaa->gdpr: status %d, want 200 (gdpr is off-ladder)", rr.Code)
	}
}

// spec: §11.7 / §15.1 compliance-profile decommission endpoint.

func newComplianceAdminServer(t *testing.T) (*admin.Router, *tenantstore.Memory, *recordingAudit) {
	t.Helper()
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	})
	return router, store, audit
}

// validDecommission builds a well-formed decommission request.
func validDecommission(prev, target string) admin.DecommissionComplianceRequest {
	return admin.DecommissionComplianceRequest{
		PreviousProfile:            prev,
		TargetProfile:              target,
		AcknowledgeDataRemediation: true,
		Justification:              "contract end-of-life; tenant migrated off regulated workloads",
		RemediationAttestations:    []string{"audit-range PII purged", "workspace snapshots crypto-shredded"},
	}
}

func postDecommission(t *testing.T, h http.Handler, id string, body admin.DecommissionComplianceRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost,
		"/v1/admin/tenants/"+id+"/compliance-profile/decommission", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestDecommissionComplianceLowersProfile(t *testing.T) {
	router, store, audit := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})

	rr := postDecommission(t, router.Handler(), "acme",
		validDecommission("hipaa", "soc2"), withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("decommission hipaa->soc2: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ComplianceProfile != "soc2" {
		t.Errorf("stored complianceProfile = %q, want soc2", row.ComplianceProfile)
	}
	ev, ok := findAuditEvent(audit.snapshot(), "compliance.profile_decommissioned")
	if !ok {
		t.Fatal("a successful decommission must emit compliance.profile_decommissioned")
	}
	if ev.Detail["previous_profile"] != "hipaa" || ev.Detail["target_profile"] != "soc2" {
		t.Errorf("event detail = %v, want previous_profile=hipaa target_profile=soc2", ev.Detail)
	}
}

func TestDecommissionComplianceRequiresAcknowledgement(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	body := validDecommission("hipaa", "soc2")
	body.AcknowledgeDataRemediation = false
	rr := postDecommission(t, router.Handler(), "acme", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing acknowledgeDataRemediation: status %d, want 400", rr.Code)
	}
}

func TestDecommissionComplianceRequiresJustification(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	body := validDecommission("hipaa", "soc2")
	body.Justification = ""
	rr := postDecommission(t, router.Handler(), "acme", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing justification: status %d, want 400", rr.Code)
	}
}

func TestDecommissionComplianceRequiresAttestation(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	body := validDecommission("hipaa", "soc2")
	body.RemediationAttestations = nil
	rr := postDecommission(t, router.Handler(), "acme", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing remediationAttestations: status %d, want 400", rr.Code)
	}
}

func TestDecommissionComplianceRejectsPreviousProfileMismatch(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	// previousProfile claims fedramp but the tenant is on hipaa.
	rr := postDecommission(t, router.Handler(), "acme",
		validDecommission("fedramp", "soc2"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("previousProfile mismatch: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

func TestDecommissionComplianceRejectsNonLowerTarget(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "soc2"})
	// A target equal to the current profile is not a wind-down.
	if rr := postDecommission(t, router.Handler(), "acme",
		validDecommission("soc2", "soc2"), withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("same-profile target: status %d, want 400", rr.Code)
	}
	// A target above the current profile is also rejected.
	_ = store.Create(nil, tenantstore.Tenant{ID: "globex", ComplianceProfile: "soc2"})
	if rr := postDecommission(t, router.Handler(), "globex",
		validDecommission("soc2", "hipaa"), withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("higher target: status %d, want 400", rr.Code)
	}
}

func TestDecommissionComplianceRequiresPlatformAdmin(t *testing.T) {
	router, store, _ := newComplianceAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	// A tenant-admin cannot self-downgrade the compliance posture.
	rr := postDecommission(t, router.Handler(), "acme",
		validDecommission("hipaa", "soc2"), withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin decommission: status %d, want 403", rr.Code)
	}
}
