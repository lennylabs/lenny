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
	// §11.7 — the default test server runs with SIEM and pgaudit configured
	// so the regulated-profile compliance gates (F-11.7.2 / F-11.7.10) do
	// not block tests that exercise unrelated tenant behaviour. The gates'
	// own tests build an unconfigured router explicitly.
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		},
	}).WithSIEMConfigured(true).WithPgauditConfigured(true)
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

// withUserPrincipal authenticates as a plain §10.2 `user` of acme — a
// role that holds no admin-surface permission.
func withUserPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "plain@acme.com",
		TenantID: "acme",
		Roles:    []pkgauth.Role{pkgauth.RoleUser},
	})
	return req.WithContext(ctx)
}

// withBillingViewerPrincipal authenticates as a §10.2 `billing-viewer`
// of acme — a role whose only permission is view_usage.
func withBillingViewerPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "billing@acme.com",
		TenantID: "acme",
		Roles:    []pkgauth.Role{pkgauth.RoleBillingViewer},
	})
	return req.WithContext(ctx)
}

// withTenantViewerPrincipal authenticates as a §10.2 `tenant-viewer` of
// acme — a read-only role that holds no manage permission.
func withTenantViewerPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "viewer@acme.com",
		TenantID: "acme",
		Roles:    []pkgauth.Role{pkgauth.RoleTenantViewer},
	})
	return req.WithContext(ctx)
}

// withCustomRolePrincipal authenticates a caller of acme holding the
// named tenant custom role and no built-in role, so the §10.2
// custom-role resolution path is exercised.
func withCustomRolePrincipal(name string) func(*http.Request) *http.Request {
	return func(req *http.Request) *http.Request {
		ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject:  "custom@acme.com",
			TenantID: "acme",
			Roles:    []pkgauth.Role{pkgauth.Role(name)},
		})
		return req.WithContext(ctx)
	}
}

// withForeignTenantAdminPrincipal authenticates as a `tenant-admin` of
// globex — a different tenant than acme — so cross-tenant rejection can
// be asserted.
func withForeignTenantAdminPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@globex.com",
		TenantID: "globex",
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

// spec: §12.8 line 865 — the admin API exposes the TenantState enum. A
// freshly created tenant reports state=active in the create response and
// on a subsequent GET; a soft-deleted tenant's GET reports the `deleted`
// tombstone state. F-12.8.12.
func TestTenantStateExposedInAdminAPI_spec_12_8_865(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme Corp"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.State != "active" {
		t.Errorf("create response state = %q, want active", created.State)
	}

	greq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
	grr := httptest.NewRecorder()
	router.Handler().ServeHTTP(grr, greq)
	if grr.Code != http.StatusOK {
		t.Fatalf("get status %d, want 200; body=%s", grr.Code, grr.Body.String())
	}
	var got admin.TenantPayload
	_ = json.Unmarshal(grr.Body.Bytes(), &got)
	if got.State != "active" {
		t.Errorf("get response state = %q, want active", got.State)
	}

	// A soft-deleted tenant becomes a deleted-state tombstone, and GET
	// returns 410 Gone (spec §12.8 line 873) rather than the row.
	if err := store.SoftDelete(greq.Context(), "acme", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	row, err := store.Get(greq.Context(), "acme")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if row.State != "deleted" {
		t.Errorf("tombstone state = %q, want deleted", row.State)
	}
	treq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
	trr := httptest.NewRecorder()
	router.Handler().ServeHTTP(trr, treq)
	if trr.Code != http.StatusGone {
		t.Fatalf("tombstone GET status %d, want 410; body=%s", trr.Code, trr.Body.String())
	}
}

// spec: §24.10 line 127 — `tenants get` must surface the deletion state
// so an operator can monitor progress through disabling → deleting →
// deleted. A tenant mid-lifecycle (DeletedAt still zero) resolves 200
// with the in-progress `state`, distinct from the `deleted` tombstone's
// 410 Gone. F-24.10.4.
func TestTenantGetSurfacesInProgressDeletionState_spec_24_10_127(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	for _, state := range []string{tenantstore.TenantStateDisabling, tenantstore.TenantStateDeleting} {
		if _, err := store.Update(req.Context(), "acme", func(tn *tenantstore.Tenant) error {
			tn.State = state
			return nil
		}); err != nil {
			t.Fatalf("set state %s: %v", state, err)
		}
		greq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
		grr := httptest.NewRecorder()
		router.Handler().ServeHTTP(grr, greq)
		if grr.Code != http.StatusOK {
			t.Fatalf("get during %s: status %d, want 200; body=%s", state, grr.Code, grr.Body.String())
		}
		var got admin.TenantPayload
		_ = json.Unmarshal(grr.Body.Bytes(), &got)
		if got.State != state {
			t.Errorf("get response state = %q, want %q", got.State, state)
		}
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

// spec: §10.2 line 210 — bad-format tenant_id rejects with
// `400 INVALID_TENANT_ID` (not the generic `VALIDATION_ERROR`).
func TestCreateTenantRejectsInvalidID(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "with space"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if env.Error.Code != "INVALID_TENANT_ID" {
		t.Fatalf("code: got %q, want INVALID_TENANT_ID", env.Error.Code)
	}
	if env.Error.Details["field"] != "id" {
		t.Fatalf("details.field: got %v, want id", env.Error.Details["field"])
	}
}

// spec: §10.2 line 210 — a missing tenant_id violates the format regex
// (which requires {1,128} characters) and so rejects with the same
// `400 INVALID_TENANT_ID` code as a malformed one.
func TestCreateTenantRejectsMissingID(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if env.Error.Code != "INVALID_TENANT_ID" {
		t.Fatalf("code: got %q, want INVALID_TENANT_ID", env.Error.Code)
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

// spec: §12.8 line 865; §24.10 row 3 — DELETE initiates the async
// tenant-deletion lifecycle by transitioning the tenant into
// `disabling`. The background controller (not this handler) advances it
// to `deleting` → `deleted` and sets the tombstone. F-12.8.1, F-24.10.3.
func TestDeleteTenantInitiatesLifecycle_spec_24_10_509(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 Accepted", rr.Code)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	// The handler initiates the lifecycle; it does NOT tombstone the row
	// (that is the controller's Phase 6 job). DeletedAt stays unset.
	if !row.DeletedAt.IsZero() {
		t.Errorf("DELETE must not tombstone the row directly; DeletedAt = %v", row.DeletedAt)
	}
	if row.State != tenantstore.TenantStateDisabling {
		t.Errorf("state after delete = %q, want disabling", row.State)
	}
}

// A repeated DELETE on a tenant already mid-lifecycle is idempotent: it
// re-accepts at 202 without rewinding the deletion phase.
func TestDeleteTenantIdempotentMidLifecycle_spec_24_10_509(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDeleting})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", rr.Code)
	}
	row, _ := store.Get(req.Context(), "acme")
	if row.State != tenantstore.TenantStateDeleting {
		t.Errorf("a re-issued delete must not rewind the phase; state = %q, want deleting", row.State)
	}
}

// A DELETE on an already-tombstoned tenant reads as not-found.
func TestDeleteTenantTombstoneNotFound_spec_24_10_509(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme"})
	_ = store.SoftDelete(nil, "acme", time.Now().UTC())

	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status on tombstoned tenant: got %d, want 404", rr.Code)
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

	// Delete — initiates the §12.8 deletion lifecycle (202 Accepted).
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme", nil),
	))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("delete: status %d", rr.Code)
	}

	events := audit.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events, got %d (%+v)", len(events), events)
	}
	want := []string{"admin.tenant.created", "admin.tenant.updated", "admin.tenant.deletion_initiated"}
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
	// §11.7 F-11.7.2 / F-11.7.10 — SIEM and pgaudit configured by default;
	// see newAdminServer.
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithSIEMConfigured(true).WithPgauditConfigured(true)
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

// spec: §12.9 / §15.1 workspaceTier stricter-only ratchet (T3 < T4).

// putWorkspaceTier issues a PUT that sets only workspaceTier.
func putWorkspaceTier(t *testing.T, h http.Handler, id, tier string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(admin.UpdateTenantRequest{WorkspaceTier: &tier})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/"+id, bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUpdateTenantRejectsWorkspaceTierDowngrade(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	rr := putWorkspaceTier(t, router.Handler(), "acme", "T3")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("T4->T3 downgrade: status %d, want 422; body %s", rr.Code, rr.Body.String())
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
	if env.Error.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("error code = %q, want CLASSIFICATION_CONTROL_VIOLATION", env.Error.Code)
	}
	if env.Error.Details["reason"] != "tier_downgrade_prohibited" {
		t.Errorf("details.reason = %v, want tier_downgrade_prohibited", env.Error.Details["reason"])
	}
	// §12.9: the stored tier is unchanged by a rejected downgrade.
	row, _ := store.Get(context.Background(), "acme")
	if row.WorkspaceTier != "T4" {
		t.Errorf("stored workspaceTier = %q, want T4 (unchanged)", row.WorkspaceTier)
	}
}

func TestUpdateTenantAllowsWorkspaceTierUpgrade(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T3"})

	rr := putWorkspaceTier(t, router.Handler(), "acme", "T4")
	if rr.Code != http.StatusOK {
		t.Fatalf("T3->T4 upgrade: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.WorkspaceTier != "T4" {
		t.Errorf("stored workspaceTier = %q, want T4", row.WorkspaceTier)
	}
}

func TestUpdateTenantAllowsSameWorkspaceTier(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	// Re-asserting the same tier is not a downgrade; the §12.5 idempotent
	// T4 re-assert path depends on this.
	if rr := putWorkspaceTier(t, router.Handler(), "acme", "T4"); rr.Code != http.StatusOK {
		t.Errorf("T4->T4 re-assert: status %d, want 200; body %s", rr.Code, rr.Body.String())
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
