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
	injectAdminIfMatch(t, router.Handler(), req)
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

// spec: §9.2 line 66; §16.7 line 675. F-9.2.9.
// Verify the weakening write emits the canonical
// `tenant.elicitation_content_integrity_changed` audit event (NOT the
// legacy `_mode_changed` typo) and that the Detail carries every
// §16.7 line 675 spec-mandated payload field.
func TestPutElicitationIntegrityWeakensWithJustification_spec_9_2_F_9_2_9(t *testing.T) {
	store := tenantstore.NewMemory()
	auditSink := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Audit: auditSink,
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "staging tenant, integrity tooling offline"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "off" {
		t.Errorf("stored mode = %q, want off", row.ElicitationContentIntegrity)
	}
	// spec: §16.7 line 675 — canonical event name and payload.
	var found *admin.AuditEvent
	for _, e := range auditSink.snapshot() {
		if e.Type == "tenant.elicitation_content_integrity_changed" {
			ev := e
			found = &ev
		}
		if e.Type == "tenant.elicitation_content_integrity_mode_changed" {
			t.Fatalf("emitted legacy event name %q; F-9.2.9 requires %q",
				e.Type, "tenant.elicitation_content_integrity_changed")
		}
	}
	if found == nil {
		t.Fatal("no tenant.elicitation_content_integrity_changed audit event")
	}
	if found.Detail["new_stored_mode"] != "off" {
		t.Errorf("new_stored_mode = %v, want off", found.Detail["new_stored_mode"])
	}
	// First write — previous_stored_mode must be null (not the resolved
	// default `enforce`). JSON encoding represents this as a nil any.
	if v, ok := found.Detail["previous_stored_mode"]; !ok || v != nil {
		t.Errorf("previous_stored_mode = %v (present=%v), want nil for the first write", v, ok)
	}
	if found.Detail["effective_mode_at_change"] != "off" {
		t.Errorf("effective_mode_at_change = %v, want off (no floor)", found.Detail["effective_mode_at_change"])
	}
	if found.Detail["platform_floor_at_change"] != "" {
		t.Errorf("platform_floor_at_change = %v, want \"\" (no floor wired)", found.Detail["platform_floor_at_change"])
	}
	if found.Detail["justification"] != "staging tenant, integrity tooling offline" {
		t.Errorf("justification = %v", found.Detail["justification"])
	}
	if found.Detail["changed_by"] == "" || found.Detail["changed_by"] == nil {
		t.Errorf("changed_by must be the operator's sub, got %v", found.Detail["changed_by"])
	}
	if _, ok := found.Detail["changed_by_tenant_id"]; !ok {
		t.Errorf("changed_by_tenant_id missing")
	}
	if _, ok := found.Detail["changed_at"]; !ok {
		t.Errorf("changed_at missing")
	}
	if found.Detail["tenant_id"] != "acme" {
		t.Errorf("tenant_id = %v, want acme", found.Detail["tenant_id"])
	}
}

// spec: §9.2 line 66; §16.7 line 675. F-9.2.9.
// A second write must carry the prior stored mode in previous_stored_mode,
// and a write under a non-empty floor must record the floor value.
func TestPutElicitationIntegrityRecordsPriorModeAndFloor_spec_9_2_F_9_2_9(t *testing.T) {
	store := tenantstore.NewMemory()
	auditSink := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Audit: auditSink,
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithElicitationFloor("detect-only")
	seedAdminTenant(t, store, "acme")

	// First write: from null → enforce, under a detect-only floor.
	body1, _ := json.Marshal(map[string]string{"mode": "enforce"})
	rr1 := httptest.NewRecorder()
	req1 := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body1)))
	injectAdminIfMatch(t, router.Handler(), req1)
	router.Handler().ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first PUT status %d, body %s", rr1.Code, rr1.Body.String())
	}
	// Second write: from enforce → enforce (no-op). Confirms an unchanged
	// write still emits, with previous_stored_mode = "enforce".
	body2, _ := json.Marshal(map[string]string{"mode": "enforce"})
	rr2 := httptest.NewRecorder()
	req2 := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body2)))
	injectAdminIfMatch(t, router.Handler(), req2)
	router.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second PUT status %d", rr2.Code)
	}
	events := auditSink.snapshot()
	var second *admin.AuditEvent
	count := 0
	for i := range events {
		if events[i].Type != "tenant.elicitation_content_integrity_changed" {
			continue
		}
		count++
		ev := events[i]
		second = &ev
	}
	if count != 2 {
		t.Fatalf("expected 2 audit events, got %d", count)
	}
	if second.Detail["previous_stored_mode"] != "enforce" {
		t.Errorf("second-write previous_stored_mode = %v, want enforce",
			second.Detail["previous_stored_mode"])
	}
	if second.Detail["platform_floor_at_change"] != "detect-only" {
		t.Errorf("platform_floor_at_change = %v, want detect-only",
			second.Detail["platform_floor_at_change"])
	}
	if second.Detail["effective_mode_at_change"] != "enforce" {
		t.Errorf("effective_mode_at_change = %v, want enforce (max of floor and stored)",
			second.Detail["effective_mode_at_change"])
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
	putReq := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), putReq)
	router.Handler().ServeHTTP(rr, putReq)
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

// spec: §17.2 line 86 / §9.2 line 64 — F-17.2.9. The platform floor is
// sourced through a dynamic provider so a `helm upgrade` floor change
// (sourced from the phase-stamp ConfigMap reconcile) is observed by the
// admin GET effective-mode resolution and the PUT below-floor guard
// without re-wiring the Router. This test mutates the provider's backing
// value between requests and asserts both surfaces follow it live.
func TestElicitationFloorProviderObservedLive_spec_17_2_9(t *testing.T) {
	store := tenantstore.NewMemory()
	floor := "off"
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithElicitationFloorProvider(func() string { return floor })
	seedAdminTenant(t, store, "acme")

	// Stored mode off, floor off: effective resolves to off and a PUT to
	// off is admitted (no floor to violate).
	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "staging"})
	putReq := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), putReq)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, putReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial PUT status %d, body %s", rr.Code, rr.Body.String())
	}

	// Simulate the §17.2 reconcile raising the platform floor to enforce.
	// The same Router instance must now clamp the effective mode upward.
	floor = "enforce"
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
	if resp.StoredMode != "off" || resp.EffectiveMode != "enforce" {
		t.Fatalf("after floor raise storedMode/effectiveMode = %q/%q, want off/enforce",
			resp.StoredMode, resp.EffectiveMode)
	}

	// With the raised floor in force, a PUT back down to off is now
	// rejected below the platform floor — the dynamic floor gates writes.
	body, _ = json.Marshal(map[string]string{"mode": "off", "justification": "retry"})
	rejReq := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), rejReq)
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, rejReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT-below-raised-floor status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR" {
		t.Fatalf("error code = %q, want ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR", errResp.Error.Code)
	}
}
