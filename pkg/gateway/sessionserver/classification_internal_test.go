// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.9 line 1048 — requireTenantClassification rejects a session
// create for a tenant whose workspaceTier is not a recognized §12.9 tier
// with 422 CLASSIFICATION_CONTROL_VIOLATION.
func TestRequireTenantClassificationRejectsStaleTier(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: "T2"})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "acme"); ok {
		t.Fatal("requireTenantClassification admitted a misconfigured tier")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("code = %q, want CLASSIFICATION_CONTROL_VIOLATION", body.Code)
	}
	if body.Details["reason"] != "invalid_workspace_tier" {
		t.Errorf("reason = %v, want invalid_workspace_tier", body.Details["reason"])
	}
	if body.Details["tier"] != "T2" {
		t.Errorf("tier = %v, want T2", body.Details["tier"])
	}
}

// A recognized tier (T4) admits the create.
func TestRequireTenantClassificationAdmitsValidTier(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "acme"); !ok {
		t.Fatalf("requireTenantClassification rejected a valid T4 tenant: %s", rec.Body.String())
	}
}

// An unknown tenant carries no classification to validate; the create
// proceeds (the §10.2 tenant-claim path governs unknown tenants).
func TestRequireTenantClassificationUnknownTenantAdmits(t *testing.T) {
	s := &Server{tenants: tenantstore.NewMemory()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "ghost"); !ok {
		t.Fatalf("unknown tenant must admit, got %d %s", rec.Code, rec.Body.String())
	}
}
