// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §12.5 line 317 — per-tenant gcPriority through the admin API.

// TestCreateTenantWithGCPriority_spec_12_5_317 round-trips a high
// gcPriority through create, the response payload, and the store.
func TestCreateTenantWithGCPriority_spec_12_5_317(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", GCPriority: "high"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.GCPriority != "high" {
		t.Errorf("response gcPriority = %q, want high", resp.GCPriority)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.GCPriority != "high" {
		t.Errorf("stored gcPriority = %q, want high", row.GCPriority)
	}
}

// TestCreateTenantDefaultsGCPriorityNormal_spec_12_5_317 confirms a tenant
// created without gcPriority reports the normal default in the response.
func TestCreateTenantDefaultsGCPriorityNormal_spec_12_5_317(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.GCPriority != "normal" {
		t.Errorf("response gcPriority = %q, want normal", resp.GCPriority)
	}
}

// TestCreateTenantRejectsBadGCPriority_spec_12_5_317 rejects a value
// outside the §12.5 closed enum.
func TestCreateTenantRejectsBadGCPriority_spec_12_5_317(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", GCPriority: "urgent"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad gcPriority: got %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestUpdateTenantSetsGCPriority_spec_12_5_317 verifies the update endpoint
// mutates the per-tenant gcPriority and persists it.
func TestUpdateTenantSetsGCPriority_spec_12_5_317(t *testing.T) {
	router, store := newAdminServer(t)
	create, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(create)))
	router.Handler().ServeHTTP(httptest.NewRecorder(), req)

	prio := "high"
	update, _ := json.Marshal(admin.UpdateTenantRequest{GCPriority: &prio})
	ureq := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(update)))
	injectAdminIfMatch(t, router.Handler(), ureq)
	urr := httptest.NewRecorder()
	router.Handler().ServeHTTP(urr, ureq)
	if urr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", urr.Code, urr.Body.String())
	}
	row, err := store.Get(ureq.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.GCPriority != "high" {
		t.Errorf("stored gcPriority = %q, want high", row.GCPriority)
	}
}

// TestUpdateTenantRejectsBadGCPriority_spec_12_5_317 rejects an out-of-enum
// value on the update path.
func TestUpdateTenantRejectsBadGCPriority_spec_12_5_317(t *testing.T) {
	router, _ := newAdminServer(t)
	create, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(create)))
	router.Handler().ServeHTTP(httptest.NewRecorder(), req)

	bad := "later"
	update, _ := json.Marshal(admin.UpdateTenantRequest{GCPriority: &bad})
	ureq := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(update)))
	urr := httptest.NewRecorder()
	router.Handler().ServeHTTP(urr, ureq)
	if urr.Code != http.StatusBadRequest {
		t.Errorf("bad gcPriority on update: got %d, want 400", urr.Code)
	}
}
