// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §15.1 line 818 — a suspended tenant rejects new session creation
// and message injection with TENANT_SUSPENDED. F-15.1.3.

// requireTenantState reports the suspension as TENANT_SUSPENDED, and it
// takes precedence over the §12.8 deletion-lifecycle TENANT_NOT_ACTIVE
// gate so an operator suspension is named distinctly.
func TestRequireTenantStateRejectsSuspended_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", State: tenantstore.TenantStateActive, Suspended: true,
	})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantState(rec, req, "acme"); ok {
		t.Fatal("suspended tenant admitted a session create")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if body := decodeEnvelope(t, rec); body.Code != "TENANT_SUSPENDED" {
		t.Errorf("code = %q, want TENANT_SUSPENDED", body.Code)
	}
}

// A suspended tenant that is also leaving the active lifecycle state is
// reported as suspended, not as the deletion-lifecycle TENANT_NOT_ACTIVE.
func TestRequireTenantStateSuspensionBeatsLifecycle_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", State: tenantstore.TenantStateDisabling, Suspended: true,
	})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantState(rec, req, "acme"); ok {
		t.Fatal("suspended tenant admitted a session create")
	}
	if body := decodeEnvelope(t, rec); body.Code != "TENANT_SUSPENDED" {
		t.Errorf("code = %q, want TENANT_SUSPENDED", body.Code)
	}
}

func TestRequireTenantNotSuspendedRejectsSuspended_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", Suspended: true})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/messages", nil)
	if ok := s.requireTenantNotSuspended(rec, req, "acme"); ok {
		t.Fatal("suspended tenant admitted message injection")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if body := decodeEnvelope(t, rec); body.Code != "TENANT_SUSPENDED" {
		t.Errorf("code = %q, want TENANT_SUSPENDED", body.Code)
	}
}

// A non-suspended tenant, an unknown tenant, and an unwired registry all
// admit message injection.
func TestRequireTenantNotSuspendedAdmits_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme"})

	cases := []struct {
		name   string
		server *Server
		tenant string
	}{
		{"active", &Server{tenants: store}, "acme"},
		{"unknown", &Server{tenants: store}, "ghost"},
		{"unwired", &Server{}, "acme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/messages", nil)
			if ok := tc.server.requireTenantNotSuspended(rec, req, tc.tenant); !ok {
				t.Fatalf("admit expected, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
