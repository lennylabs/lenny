// SPDX-License-Identifier: MIT

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: §15.1 line 986 — a no-credentials request with RequireAuth=true
// rejects with the canonical UNAUTHORIZED (401) code from the §15.1
// error catalog. Details.reason carries the operational discriminator
// "auth_required" so a caller scripting against the catalog still
// distinguishes the missing-bearer case from a bad-bearer case (which
// would be TOKEN_INVALID).
//
// F-10.2.12 traceback: the prior `AUTH_REQUIRED` code did not appear in
// the §15.1 catalog, breaking client scripts that match on catalog
// codes. The rename to `UNAUTHORIZED` aligns with the published catalog.
func TestRequireAuthEmitsCanonicalUnauthorized(t *testing.T) {
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		RequireAuth: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-credential request: status = %d, want 401", rr.Code)
	}
	body := map[string]any{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	env, _ := body["error"].(map[string]any)
	if env == nil {
		t.Fatalf("body has no error envelope: %v", body)
	}
	if env["code"] != "UNAUTHORIZED" {
		t.Errorf("code = %v, want UNAUTHORIZED", env["code"])
	}
	det, _ := env["details"].(map[string]any)
	if det == nil || det["reason"] != "auth_required" {
		t.Errorf("details.reason = %v, want auth_required", det)
	}
}

// spec: §15.1 line 1016 — gateway misconfiguration (no Verifier wired)
// surfaces as the canonical INTERNAL_ERROR (500); details.reason
// names the misconfiguration. This is the F-10.2.12 sibling case.
func TestBearerWithoutVerifierEmitsCanonicalInternalError(t *testing.T) {
	inner, _ := captureHandler()
	// Wrap with RequireAuth=true so the missing-credential branch is
	// not the gate; then present a bearer header that drives the
	// serveBearer branch where Verifier == nil.
	h := Wrap(inner, Options{
		RequireAuth: true,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer fake.token.here")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("missing verifier: status = %d, want 500", rr.Code)
	}
	body := map[string]any{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	env, _ := body["error"].(map[string]any)
	if env == nil {
		t.Fatalf("body has no error envelope: %v", body)
	}
	if env["code"] != "INTERNAL_ERROR" {
		t.Errorf("code = %v, want INTERNAL_ERROR", env["code"])
	}
	det, _ := env["details"].(map[string]any)
	if det == nil || det["reason"] != "auth_not_configured" {
		t.Errorf("details.reason = %v, want auth_not_configured", det)
	}
}
