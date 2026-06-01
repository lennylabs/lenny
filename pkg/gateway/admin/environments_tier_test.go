// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// newEnvironmentAdminWithTenant wires the environment admin endpoints
// against a tenant store seeded with tenant "acme" at the given tier, so
// the §12.9 stricter-only override can be exercised against a real parent
// tenant.
func newEnvironmentAdminWithTenant(t *testing.T, tenantTier string) (*admin.Router, environmentstore.Store) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: tenantTier})
	envs := environmentstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(envs)
	return router, envs
}

// spec: §12.9 line 1033 — an environment may pin a stricter tier than its
// tenant (T4 over a T3 tenant) but never a looser one. The stricter
// override is admitted.
func TestCreateEnvironmentStricterTierOverrideAdmitted(t *testing.T) {
	router, envs := newEnvironmentAdminWithTenant(t, "T3")
	body := validEnvironmentPayload("phi-preprod")
	body.WorkspaceTier = "T4"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("stricter override: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	got, err := envs.Get(context.Background(), "acme", "phi-preprod")
	if err != nil || got.WorkspaceTier != "T4" {
		t.Errorf("stored env = %+v (err=%v), want WorkspaceTier T4", got, err)
	}
}

// A looser override (T3 on a T4 tenant) is rejected with 422
// CLASSIFICATION_CONTROL_VIOLATION carrying reason tier_override_looser.
func TestCreateEnvironmentLooserTierOverrideRejected(t *testing.T) {
	router, envs := newEnvironmentAdminWithTenant(t, "T4")
	body := validEnvironmentPayload("relaxed")
	body.WorkspaceTier = "T3"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("looser override: status %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	env := decodeErr(t, rr)
	if env.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("code = %q, want CLASSIFICATION_CONTROL_VIOLATION", env.Code)
	}
	if env.Details["reason"] != "tier_override_looser" {
		t.Errorf("reason = %v, want tier_override_looser", env.Details["reason"])
	}
	if _, err := envs.Get(context.Background(), "acme", "relaxed"); err == nil {
		t.Error("environment with a looser tier override must not be persisted")
	}
}

// An out-of-enum override value is rejected with 400 VALIDATION_ERROR.
func TestCreateEnvironmentInvalidTierOverrideRejected(t *testing.T) {
	router, _ := newEnvironmentAdminWithTenant(t, "T3")
	body := validEnvironmentPayload("bad")
	body.WorkspaceTier = "T2"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid override: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// An equal-tier override and the empty (inherit) value are both admitted.
func TestCreateEnvironmentEqualAndInheritTierAdmitted(t *testing.T) {
	for _, tier := range []string{"", "T4"} {
		router, _ := newEnvironmentAdminWithTenant(t, "T4")
		body := validEnvironmentPayload("inherit")
		body.WorkspaceTier = tier
		rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
		if rr.Code != http.StatusCreated {
			t.Fatalf("tier %q: status %d, want 201; body=%s", tier, rr.Code, rr.Body.String())
		}
	}
}
