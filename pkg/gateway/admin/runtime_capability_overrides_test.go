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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §5.1 line 49 — per-tenant runtime capability override CRUD.

func newCapOverrideAdmin(t *testing.T) (*admin.Router, runtimecapoverride.Store, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code", Type: runtimestore.TypeAgent,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	overrides := runtimecapoverride.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithRuntimeCapabilityOverrides(overrides)
	return router, overrides, audit
}

func boolPtr(b bool) *bool { return &b }

func TestCapOverridePutGetDeleteRoundTrip_spec_5_1_49(t *testing.T) {
	router, store, audit := newCapOverrideAdmin(t)
	interaction := runtimestore.InteractionOneShot
	body, _ := json.Marshal(runtimestore.CapabilityOverride{
		Interaction:        &interaction,
		InjectionSupported: boolPtr(false),
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status %d, body=%s", rr.Code, rr.Body.String())
	}
	// Persisted.
	got, ok, err := store.Get(context.Background(), "acme", "claude-code")
	if err != nil || !ok {
		t.Fatalf("store Get: ok=%v err=%v", ok, err)
	}
	if got.Interaction == nil || *got.Interaction != runtimestore.InteractionOneShot {
		t.Errorf("stored override: %+v", got)
	}
	// Audit emitted.
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "admin.tenant.runtime_capability_override_updated" {
		t.Errorf("audit: %+v", audit.snapshot())
	}

	// GET returns it.
	greq := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", nil))
	grr := httptest.NewRecorder()
	router.Handler().ServeHTTP(grr, greq)
	if grr.Code != http.StatusOK {
		t.Fatalf("GET status %d", grr.Code)
	}

	// LIST returns it.
	lreq := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/runtime-capability-overrides", nil))
	lrr := httptest.NewRecorder()
	router.Handler().ServeHTTP(lrr, lreq)
	var listResp struct {
		Overrides map[string]runtimestore.CapabilityOverride `json:"overrides"`
	}
	_ = json.Unmarshal(lrr.Body.Bytes(), &listResp)
	if len(listResp.Overrides) != 1 {
		t.Errorf("LIST: %+v", listResp.Overrides)
	}

	// DELETE removes it.
	dreq := withAdminPrincipal(httptest.NewRequest(http.MethodDelete,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", nil))
	drr := httptest.NewRecorder()
	router.Handler().ServeHTTP(drr, dreq)
	if drr.Code != http.StatusNoContent {
		t.Fatalf("DELETE status %d", drr.Code)
	}
	if _, ok, _ := store.Get(context.Background(), "acme", "claude-code"); ok {
		t.Error("override present after delete")
	}
	// GET now 404.
	g2 := httptest.NewRecorder()
	router.Handler().ServeHTTP(g2, withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", nil)))
	if g2.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status %d", g2.Code)
	}
}

func TestCapOverridePutRejectsInvalidEnum_spec_5_1_49(t *testing.T) {
	router, _, _ := newCapOverrideAdmin(t)
	bad := runtimestore.RuntimeInteraction("streaming")
	body, _ := json.Marshal(runtimestore.CapabilityOverride{Interaction: &bad})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", bytes.NewReader(body))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestCapOverridePutRejectsEmptyOverride_spec_5_1_49(t *testing.T) {
	router, _, _ := newCapOverrideAdmin(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", bytes.NewReader([]byte(`{}`)))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty override should be 400, got %d", rr.Code)
	}
}

func TestCapOverridePutRejectsUnknownRuntime_spec_5_1_49(t *testing.T) {
	router, _, _ := newCapOverrideAdmin(t)
	body, _ := json.Marshal(runtimestore.CapabilityOverride{PreConnect: boolPtr(true)})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/runtime-capability-overrides/does-not-exist", bytes.NewReader(body))))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown runtime should be 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §5.1 line 49 — a tenant-admin may only manage their own tenant's
// overrides; a cross-tenant write is rejected.
func TestCapOverrideTenantScoping_spec_5_1_49(t *testing.T) {
	router, _, _ := newCapOverrideAdmin(t)
	body, _ := json.Marshal(runtimestore.CapabilityOverride{PreConnect: boolPtr(true)})
	// globex tenant-admin writing acme's override → 403.
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/runtime-capability-overrides/claude-code", bytes.NewReader(body)), "globex")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant write should be 403, got %d", rr.Code)
	}
}
