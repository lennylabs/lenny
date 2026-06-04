// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.9 line 1048; §15.1 line 816 — POST /v1/admin/tenants rejects a
// workspaceTier outside the closed T3/T4 enum with 400 VALIDATION_ERROR,
// so an arbitrary string can never persist and be read downstream as
// "not T4".
func TestCreateTenantRejectsInvalidWorkspaceTier(t *testing.T) {
	for _, tier := range []string{"T2", "T5", "prod", "t4"} {
		router, store := newAdminServer(t)
		body, _ := json.Marshal(map[string]any{"id": "acme", "workspaceTier": tier})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
		rec := httptest.NewRecorder()
		router.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("tier %q: status = %d, want 400; body=%s", tier, rec.Code, rec.Body.String())
		}
		if _, err := store.Get(nil, "acme"); err == nil {
			t.Errorf("tier %q: rejected tenant must not be persisted", tier)
		}
	}
}

// A T3/T4/empty workspaceTier is admitted on create.
func TestCreateTenantAcceptsValidWorkspaceTier(t *testing.T) {
	for _, tier := range []string{"", "T3", "T4"} {
		router, store := newAdminServer(t)
		body, _ := json.Marshal(map[string]any{"id": "acme", "workspaceTier": tier})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
		rec := httptest.NewRecorder()
		router.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("tier %q: status = %d, want 201; body=%s", tier, rec.Code, rec.Body.String())
		}
		row, err := store.Get(nil, "acme")
		if err != nil {
			t.Fatalf("tier %q: tenant not stored: %v", tier, err)
		}
		if row.WorkspaceTier != tier {
			t.Errorf("tier %q: stored = %q", tier, row.WorkspaceTier)
		}
	}
}

// spec: §12.9 line 1048 — PUT /v1/admin/tenants/{id} rejects an
// out-of-enum workspaceTier before the stricter-only ratchet runs, so a
// value like "T2" (off the ratchet ladder) cannot slip through as a
// non-downgrade.
func TestUpdateTenantRejectsInvalidWorkspaceTier(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T3"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T2"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	row, _ := store.Get(nil, "acme")
	if row.WorkspaceTier != "T3" {
		t.Errorf("tier must be unchanged after rejected update, got %q", row.WorkspaceTier)
	}
}

// spec: §12.9 line 1033 — a T4→T3 downgrade through PUT is still rejected
// with 422 CLASSIFICATION_CONTROL_VIOLATION (the ratchet, not the enum
// gate). This guards that the new enum gate does not mask the ratchet.
func TestUpdateTenantT4ToT3StillRatcheted(t *testing.T) {
	router, store := newAdminServer(t)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T3"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if body := decodeErr(t, rec); body.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("code = %q, want CLASSIFICATION_CONTROL_VIOLATION", body.Code)
	}
}

// errEnvelope is the admin error-response shape used by the tier tests.
type errEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// decodeErr extracts the error envelope from an admin error response.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errEnvelope {
	t.Helper()
	var env struct {
		Error errEnvelope `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	return env.Error
}
