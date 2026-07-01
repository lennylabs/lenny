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

// spec: §11.2 line 31 — per-tenant token-quota reset period through the
// admin API.

// TestCreateTenantWithQuotaResetPeriod_spec_11_2_31 round-trips a valid
// per-tenant reset period through create, the response payload, and the
// store.
func TestCreateTenantWithQuotaResetPeriod_spec_11_2_31(t *testing.T) {
	router, store := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{
		ID: "acme", TokenQuotaPerWindow: 1000, QuotaResetPeriod: "monthly",
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.QuotaResetPeriod != "monthly" {
		t.Errorf("response quotaResetPeriod = %q, want monthly", resp.QuotaResetPeriod)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	if row.QuotaResetPeriod != "monthly" {
		t.Errorf("stored quotaResetPeriod = %q, want monthly", row.QuotaResetPeriod)
	}
	if row.TokenQuotaPerWindow != 1000 {
		t.Errorf("stored tokenQuotaPerWindow = %d, want 1000", row.TokenQuotaPerWindow)
	}
}

// TestCreateTenantRejectsBadQuotaResetPeriod_spec_11_2_31 rejects a
// value outside the §11.2 closed enum.
func TestCreateTenantRejectsBadQuotaResetPeriod_spec_11_2_31(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", QuotaResetPeriod: "fortnightly"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad quotaResetPeriod: got %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestUpdateTenantSetsQuotaResetPeriod_spec_11_2_31 verifies the update
// endpoint mutates the per-tenant reset period and persists it.
func TestUpdateTenantSetsQuotaResetPeriod_spec_11_2_31(t *testing.T) {
	router, store := newAdminServer(t)
	create, _ := json.Marshal(admin.TenantPayload{ID: "acme", QuotaResetPeriod: "hourly"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(create)))
	router.Handler().ServeHTTP(httptest.NewRecorder(), req)

	period := "daily"
	update, _ := json.Marshal(admin.UpdateTenantRequest{QuotaResetPeriod: &period})
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
	if row.QuotaResetPeriod != "daily" {
		t.Errorf("stored quotaResetPeriod = %q, want daily", row.QuotaResetPeriod)
	}
}

// TestUpdateTenantRejectsBadQuotaResetPeriod_spec_11_2_31 rejects an
// out-of-enum value on the update path.
func TestUpdateTenantRejectsBadQuotaResetPeriod_spec_11_2_31(t *testing.T) {
	router, _ := newAdminServer(t)
	create, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(create)))
	router.Handler().ServeHTTP(httptest.NewRecorder(), req)

	bad := "weekly"
	update, _ := json.Marshal(admin.UpdateTenantRequest{QuotaResetPeriod: &bad})
	ureq := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(update)))
	urr := httptest.NewRecorder()
	router.Handler().ServeHTTP(urr, ureq)
	if urr.Code != http.StatusBadRequest {
		t.Errorf("bad quotaResetPeriod on update: got %d, want 400", urr.Code)
	}
}
