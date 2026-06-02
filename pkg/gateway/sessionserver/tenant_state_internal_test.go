// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.8 lines 865-873 — requireTenantState rejects new session
// creation for a tenant that has left the `active` TenantState with 403
// TENANT_NOT_ACTIVE, carrying the offending state in the details.
// F-12.8.12.
func TestRequireTenantStateRejectsDisabling_spec_12_8_865(t *testing.T) {
	for _, state := range []string{
		tenantstore.TenantStateDisabling,
		tenantstore.TenantStateDeleting,
		tenantstore.TenantStateDeleted,
	} {
		store := tenantstore.NewMemory()
		_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", State: state})
		s := &Server{tenants: store}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		if ok := s.requireTenantState(rec, req, "acme"); ok {
			t.Fatalf("state %q admitted a session create", state)
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("state %q: status = %d, want 403", state, rec.Code)
		}
		body := decodeEnvelope(t, rec)
		if body.Code != "TENANT_NOT_ACTIVE" {
			t.Errorf("state %q: code = %q, want TENANT_NOT_ACTIVE", state, body.Code)
		}
		if body.Details["state"] != state {
			t.Errorf("state %q: details.state = %v", state, body.Details["state"])
		}
	}
}

// An active tenant — and a row with the empty pre-lifecycle value, read
// as active — admits the create. F-12.8.12.
func TestRequireTenantStateAdmitsActive_spec_12_8_865(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateActive})
	// A second tenant written with no explicit state; Memory.Create
	// defaults it to active, but AcceptsNewWork also treats empty as
	// active so the gate is robust to a legacy row.
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "globex"})
	s := &Server{tenants: store}

	for _, id := range []string{"acme", "globex"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		if ok := s.requireTenantState(rec, req, id); !ok {
			t.Fatalf("active tenant %q rejected: %d %s", id, rec.Code, rec.Body.String())
		}
	}
}

// An unknown tenant carries no state to consult; the create proceeds
// (the §10.2 tenant-claim path governs unknown tenants). F-12.8.12.
func TestRequireTenantStateUnknownTenantAdmits_spec_12_8_865(t *testing.T) {
	s := &Server{tenants: tenantstore.NewMemory()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantState(rec, req, "ghost"); !ok {
		t.Fatalf("unknown tenant must admit, got %d %s", rec.Code, rec.Body.String())
	}
}
