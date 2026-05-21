// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §9.2 — tenant elicitation-content-integrity admin endpoints.

type integrityResp struct {
	TenantID string `json:"tenantId"`
	Mode     string `json:"mode"`
}

// seedAdminTenant inserts a tenant directly into the store.
func seedAdminTenant(t *testing.T, store *tenantstore.Memory, id string) {
	t.Helper()
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: id}); err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
}

func TestGetElicitationIntegrityDefaultsToEnforce(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp integrityResp
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Mode != "enforce" {
		t.Errorf("mode = %q, want enforce — the §9.2 tenant default", resp.Mode)
	}
}

func TestPutElicitationIntegrityEnforceNeedsNoJustification(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "enforce"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "enforce" {
		t.Errorf("stored mode = %q, want enforce", row.ElicitationContentIntegrity)
	}
}

func TestPutElicitationIntegrityWeakeningRequiresJustification(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "detect-only"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED" {
		t.Errorf("error code = %q, want ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED", errResp.Error.Code)
	}
	// The mode was not stored.
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "" {
		t.Errorf("stored mode = %q, want unchanged after a rejected weakening", row.ElicitationContentIntegrity)
	}
}

func TestPutElicitationIntegrityWeakensWithJustification(t *testing.T) {
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Audit: audit,
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "staging tenant, integrity tooling offline"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "off" {
		t.Errorf("stored mode = %q, want off", row.ElicitationContentIntegrity)
	}
	// The weakening emitted the §11.7 audit event.
	var found *admin.AuditEvent
	for _, e := range audit.snapshot() {
		if e.Type == "tenant.elicitation_content_integrity_mode_changed" {
			ev := e
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("no tenant.elicitation_content_integrity_mode_changed audit event")
	}
	if found.Detail["newMode"] != "off" || found.Detail["oldMode"] != "enforce" {
		t.Errorf("audit detail = %v, want oldMode=enforce newMode=off", found.Detail)
	}
}

func TestPutElicitationIntegrityRejectsInvalidMode(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "paranoid"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 for an unrecognized mode", rr.Code)
	}
}

func TestElicitationIntegrityRequiresPlatformAdmin(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin GET: got %d, want 403", rr.Code)
	}
}

// spec: §9.2
// diagnosis: the GET response must carry both `storedMode` (the
// tenant's persisted value) and `effectiveMode` (the resolved
// max(platformFloor, stored)). The default platform floor is `off`
// so effectiveMode equals storedMode.
func TestGetElicitationIntegrityReturnsStoredAndEffectiveModes(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		TenantID      string `json:"tenantId"`
		Mode          string `json:"mode"`
		StoredMode    string `json:"storedMode"`
		EffectiveMode string `json:"effectiveMode"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.StoredMode != "enforce" || resp.EffectiveMode != "enforce" {
		t.Errorf("storedMode/effectiveMode = %q/%q, want enforce/enforce — §9.2 tenant default with no floor",
			resp.StoredMode, resp.EffectiveMode)
	}
	if resp.Mode != "enforce" {
		t.Errorf("mode alias = %q, want enforce — v0 response field must remain set", resp.Mode)
	}
}

// spec: §9.2
// diagnosis: the platform floor clamps the tenant's effective mode.
// A tenant configured `off` with a floor of `detect-only` reports
// storedMode=off but effectiveMode=detect-only — the §9.2 ordering
// `off < detect-only < enforce` is honored by the resolver.
func TestGetElicitationIntegrityPlatformFloorClampsEffective(t *testing.T) {
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithElicitationFloor("detect-only")
	seedAdminTenant(t, store, "acme")
	// Write the tenant in `off` via PUT first, with a justification (a
	// floor of detect-only does not block `off` ABOVE the floor; the
	// floor is `detect-only` so `off < detect-only` triggers the
	// platform-floor rejection — this test uses a lower floor for the
	// stored-value path. Set the floor to `off` for this leg).
	router = router.WithElicitationFloor("off")
	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "staging tenant"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body))))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status %d, body %s", rr.Code, rr.Body.String())
	}
	// Now bump the floor to detect-only and assert the effective mode
	// clamps upward on the next GET.
	router = router.WithElicitationFloor("detect-only")
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		StoredMode    string `json:"storedMode"`
		EffectiveMode string `json:"effectiveMode"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.StoredMode != "off" || resp.EffectiveMode != "detect-only" {
		t.Errorf("storedMode/effectiveMode = %q/%q, want off/detect-only — the floor must clamp the effective value",
			resp.StoredMode, resp.EffectiveMode)
	}
}

// spec: §9.2
// diagnosis: a PUT that requests a stored mode strictly below the
// platform floor is rejected with
// ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR so an operator cannot
// disable a deployment-wide control by lowering a single tenant's
// stored mode.
func TestPutElicitationIntegrityRejectsBelowPlatformFloor(t *testing.T) {
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithElicitationFloor("enforce")
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "test"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR" {
		t.Errorf("error code = %q, want ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR", errResp.Error.Code)
	}
}
