// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §11.7 lines 374-379 — the pgaudit compliance enforcement gate.
// These routers run with SIEM configured so a rejection is attributable
// to the pgaudit gate rather than the SIEM gate. F-11.7.10.

func pgauditUnconfiguredAdmin(store tenantstore.Store) *admin.Router {
	return admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSIEMConfigured(true).WithPgauditConfigured(false)
}

// spec: §11.7 line 377 — creating a regulated-profile tenant with pgaudit
// not fully configured is rejected with 422 COMPLIANCE_PGAUDIT_REQUIRED.
func TestCreateTenantRegulatedWithoutPgauditRejected_spec_11_7_377(t *testing.T) {
	for _, profile := range []string{"soc2", "fedramp", "hipaa"} {
		t.Run(profile, func(t *testing.T) {
			router := pgauditUnconfiguredAdmin(tenantstore.NewMemory())
			body, _ := json.Marshal(admin.TenantPayload{ID: "acme", ComplianceProfile: profile})
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, withAdminPrincipal(
				httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
			))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			code, msg := errorEnvelope(t, rr)
			if code != "COMPLIANCE_PGAUDIT_REQUIRED" {
				t.Fatalf("code = %q, want COMPLIANCE_PGAUDIT_REQUIRED", code)
			}
			for _, frag := range []string{
				"tenant.complianceProfile '" + profile + "'",
				"requires audit.pgaudit.enabled to be true",
				"audit.pgaudit.sinkEndpoint to be configured",
			} {
				if !strings.Contains(msg, frag) {
					t.Errorf("message missing spec fragment %q:\n%s", frag, msg)
				}
			}
		})
	}
}

// spec: §11.7 line 377 — with pgaudit configured (and SIEM) the create
// succeeds.
func TestCreateTenantRegulatedWithPgauditAllowed_spec_11_7_377(t *testing.T) {
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSIEMConfigured(true).WithPgauditConfigured(true)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", ComplianceProfile: "hipaa"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §11.7 — a non-regulated profile is never gated on pgaudit.
func TestCreateTenantUnregulatedWithoutPgauditAllowed_spec_11_7_377(t *testing.T) {
	for _, profile := range []string{"", "none", "gdpr"} {
		router := pgauditUnconfiguredAdmin(tenantstore.NewMemory())
		body, _ := json.Marshal(admin.TenantPayload{ID: "acme", ComplianceProfile: profile})
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
		))
		if rr.Code != http.StatusCreated {
			t.Fatalf("profile %q: status %d, want 201; body %s", profile, rr.Code, rr.Body.String())
		}
	}
}

// spec: §11.7 line 377 — updating a tenant to a regulated profile with
// pgaudit not configured is rejected, symmetric with create.
func TestUpdateTenantToRegulatedWithoutPgauditRejected_spec_11_7_377(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "soc2"})
	router := pgauditUnconfiguredAdmin(store)
	profile := "hipaa" // soc2 -> hipaa is a tighten, not a downgrade
	body, _ := json.Marshal(admin.UpdateTenantRequest{ComplianceProfile: &profile})
	rr := httptest.NewRecorder()
	req := withAdminPrincipal(
		httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)),
	)
	injectAdminIfMatch(t, router.Handler(), req)
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code, _ := errorEnvelope(t, rr); code != "COMPLIANCE_PGAUDIT_REQUIRED" {
		t.Fatalf("code = %q, want COMPLIANCE_PGAUDIT_REQUIRED", code)
	}
}

// spec: §11.7 line 377 — environment creation under a regulated tenant
// with pgaudit absent is rejected.
func TestCreateEnvironmentUnderRegulatedTenantWithoutPgauditRejected_spec_11_7_377(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	router := pgauditUnconfiguredAdmin(store).WithEnvironments(environmentstore.NewMemory())

	body, _ := json.Marshal(validEnvironmentPayload("prod"))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/environments", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("regulated tenant: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code, _ := errorEnvelope(t, rr); code != "COMPLIANCE_PGAUDIT_REQUIRED" {
		t.Fatalf("code = %q, want COMPLIANCE_PGAUDIT_REQUIRED", code)
	}
}

// spec: §11.7 line 377 — the startup gate flags a regulated tenant when
// pgaudit is not configured and is a no-op once configured or when no
// regulated tenant exists.
func TestValidatePgauditForRegulatedTenants_spec_11_7_377(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "fedramp"})

	// pgaudit configured: skipped entirely.
	if err := admin.ValidatePgauditForRegulatedTenants(context.Background(), store, true); err != nil {
		t.Fatalf("configured: err = %v, want nil", err)
	}
	// pgaudit unconfigured + regulated tenant present: fatal message.
	err := admin.ValidatePgauditForRegulatedTenants(context.Background(), store, false)
	if err == nil || err.Error() != admin.PgauditStartupFatalMessage {
		t.Fatalf("unconfigured+regulated: err = %v, want PgauditStartupFatalMessage", err)
	}

	// No regulated tenant: unconfigured is fine.
	clean := tenantstore.NewMemory()
	_ = clean.Create(context.Background(), tenantstore.Tenant{ID: "bob", ComplianceProfile: "none"})
	if err := admin.ValidatePgauditForRegulatedTenants(context.Background(), clean, false); err != nil {
		t.Fatalf("no regulated tenant: err = %v, want nil", err)
	}
}
