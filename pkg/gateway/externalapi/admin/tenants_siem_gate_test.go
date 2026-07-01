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

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §11.7 lines 445-451 — the compliance enforcement gate. These
// tests build a SIEM-unconfigured router explicitly (the shared test
// helpers default to configured). F-11.7.2.

func siemUnconfiguredAdmin(store tenantstore.Store) *admin.Router {
	return admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSIEMConfigured(false)
}

func errorEnvelope(t *testing.T, rr *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Message
}

// spec: §11.7 line 446 — creating a regulated-profile tenant with no SIEM
// endpoint is rejected with 422 COMPLIANCE_SIEM_REQUIRED.
func TestCreateTenantRegulatedWithoutSIEMRejected(t *testing.T) {
	for _, profile := range []string{"soc2", "fedramp", "hipaa"} {
		t.Run(profile, func(t *testing.T) {
			router := siemUnconfiguredAdmin(tenantstore.NewMemory())
			body, _ := json.Marshal(admin.TenantPayload{ID: "acme", ComplianceProfile: profile})
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, withAdminPrincipal(
				httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
			))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			code, msg := errorEnvelope(t, rr)
			if code != "COMPLIANCE_SIEM_REQUIRED" {
				t.Fatalf("code = %q, want COMPLIANCE_SIEM_REQUIRED", code)
			}
			// The spec mandates a specific operator-facing message.
			for _, frag := range []string{
				"tenant.complianceProfile '" + profile + "'",
				"requires audit.siem.endpoint to be configured",
				"independent SIEM copy is mandatory",
			} {
				if !strings.Contains(msg, frag) {
					t.Errorf("message missing spec fragment %q:\n%s", frag, msg)
				}
			}
		})
	}
}

// spec: §11.7 line 446 — with SIEM configured the same create succeeds.
func TestCreateTenantRegulatedWithSIEMAllowed(t *testing.T) {
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

// spec: §11.7 — a non-regulated profile (none / off-ladder) is never
// gated, even with no SIEM.
func TestCreateTenantUnregulatedWithoutSIEMAllowed(t *testing.T) {
	for _, profile := range []string{"", "none", "gdpr"} {
		router := siemUnconfiguredAdmin(tenantstore.NewMemory())
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

// spec: §11.7 line 446 — updating a tenant to a regulated profile with no
// SIEM is rejected with COMPLIANCE_SIEM_REQUIRED (tighten in place still
// needs SIEM).
func TestUpdateTenantToRegulatedWithoutSIEMRejected(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "soc2"})
	router := siemUnconfiguredAdmin(store)
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
	if code, _ := errorEnvelope(t, rr); code != "COMPLIANCE_SIEM_REQUIRED" {
		t.Fatalf("code = %q, want COMPLIANCE_SIEM_REQUIRED", code)
	}
}

// spec: §11.7 line 449 — environment creation under a regulated tenant
// with no SIEM is rejected; under a non-regulated tenant it is allowed.
func TestCreateEnvironmentUnderRegulatedTenantWithoutSIEMRejected(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
	router := siemUnconfiguredAdmin(store).WithEnvironments(environmentstore.NewMemory())

	body, _ := json.Marshal(validEnvironmentPayload("prod"))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/environments", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("regulated tenant: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code, _ := errorEnvelope(t, rr); code != "COMPLIANCE_SIEM_REQUIRED" {
		t.Fatalf("code = %q, want COMPLIANCE_SIEM_REQUIRED", code)
	}

	// A non-regulated parent tenant is not gated.
	store2 := tenantstore.NewMemory()
	_ = store2.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "none"})
	router2 := siemUnconfiguredAdmin(store2).WithEnvironments(environmentstore.NewMemory())
	rr2 := httptest.NewRecorder()
	router2.Handler().ServeHTTP(rr2, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/environments", bytes.NewReader(body)),
	))
	if rr2.Code != http.StatusCreated {
		t.Fatalf("non-regulated tenant: status %d, want 201; body %s", rr2.Code, rr2.Body.String())
	}
}

// spec: §11.7 line 450 — the startup gate returns the verbatim fatal
// message when a regulated tenant exists and SIEM is unconfigured, and is
// a no-op otherwise. F-11.7.2.
func TestValidateSIEMForRegulatedTenants(t *testing.T) {
	ctx := context.Background()

	t.Run("regulated tenant, no SIEM -> fatal", func(t *testing.T) {
		store := tenantstore.NewMemory()
		_ = store.Create(ctx, tenantstore.Tenant{ID: "acme", ComplianceProfile: "fedramp"})
		err := admin.ValidateSIEMForRegulatedTenants(ctx, store, false)
		if err == nil || err.Error() != admin.SIEMStartupFatalMessage {
			t.Fatalf("err = %v, want SIEMStartupFatalMessage", err)
		}
		if !strings.Contains(admin.SIEMStartupFatalMessage, "/compliance-profile/decommission") {
			t.Error("fatal message must point at the decommission endpoint")
		}
	})

	t.Run("regulated tenant, SIEM configured -> ok", func(t *testing.T) {
		store := tenantstore.NewMemory()
		_ = store.Create(ctx, tenantstore.Tenant{ID: "acme", ComplianceProfile: "hipaa"})
		if err := admin.ValidateSIEMForRegulatedTenants(ctx, store, true); err != nil {
			t.Fatalf("configured SIEM: got %v, want nil", err)
		}
	})

	t.Run("no regulated tenant, no SIEM -> ok", func(t *testing.T) {
		store := tenantstore.NewMemory()
		_ = store.Create(ctx, tenantstore.Tenant{ID: "acme", ComplianceProfile: "none"})
		_ = store.Create(ctx, tenantstore.Tenant{ID: "globex", ComplianceProfile: "gdpr"})
		if err := admin.ValidateSIEMForRegulatedTenants(ctx, store, false); err != nil {
			t.Fatalf("no regulated tenant: got %v, want nil", err)
		}
	})
}
