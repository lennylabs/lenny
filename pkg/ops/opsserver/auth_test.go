// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// authedServer returns a Server with the §25.4 OIDC auth + role gate
// wired against an HMAC verifier, plus a fresh remediation-lock store so
// the principal-identity assertion has a real handler to reach. The rate
// limiter is given a high budget so it never trips in these auth tests.
func authedServer() (*opsserver.Server, *jwt.HMACSigner) {
	signer := jwt.NewHMACSigner("ops-test", []byte("ops-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Locks: coordination.NewMemStore(),
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// mintOpsToken signs a bearer carrying sub and the given roles.
func mintOpsToken(t *testing.T, signer *jwt.HMACSigner, sub string, roles ...auth.Role) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{Subject: sub, TenantID: "default", Roles: roles})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// spec: §25.4 line 1562 — "No anonymous access except /healthz." A
// request with no bearer is rejected with 401 UNAUTHORIZED.
func TestOpsAuthRejectsUnauthenticated_spec_25_4_1562(t *testing.T) {
	srv, _ := authedServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1562 — a verified bearer that lacks platform-admin
// and tenant-admin is rejected with 403, even though authentication
// succeeds.
func TestOpsAuthRejectsNonAdminRole_spec_25_4_1562(t *testing.T) {
	srv, signer := authedServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set("Authorization", "Bearer "+mintOpsToken(t, signer, "bob@acme.com", auth.RoleUser))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1562 — a platform-admin bearer passes the gate (the
// request is neither 401 nor 403).
func TestOpsAuthAdmitsPlatformAdmin_spec_25_4_1562(t *testing.T) {
	srv, signer := authedServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set("Authorization", "Bearer "+mintOpsToken(t, signer, "alice@acme.com", auth.RolePlatformAdmin))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("platform-admin blocked: status = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1562 — tenant-admin is also admitted.
func TestOpsAuthAdmitsTenantAdmin_spec_25_4_1562(t *testing.T) {
	srv, signer := authedServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set("Authorization", "Bearer "+mintOpsToken(t, signer, "carol@acme.com", auth.RoleTenantAdmin))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("tenant-admin blocked: status = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1562 — the Kubernetes probe endpoints are exempt so
// the kubelet (which carries no bearer) can probe liveness and
// readiness.
func TestOpsAuthExemptsProbes_spec_25_4_1562(t *testing.T) {
	srv, _ := authedServer()
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("%s blocked by auth: status = %d", path, rec.Code)
		}
	}
}

// spec: §25.4 line 1562 — a malformed/invalid bearer is rejected, not
// silently downgraded to anonymous access.
func TestOpsAuthRejectsInvalidToken_spec_25_4_1562(t *testing.T) {
	srv, _ := authedServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1562 — the started_by / acquired_by identity comes
// from the verified principal's sub claim, not the client-controlled
// X-Lenny-Caller header. A caller cannot spoof another identity by
// setting the header alongside a valid bearer.
func TestOpsAuthIdentityFromPrincipalNotHeader_spec_25_4_1562(t *testing.T) {
	srv, signer := authedServer()
	raw, _ := json.Marshal(map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mintOpsToken(t, signer, "alice@acme.com", auth.RolePlatformAdmin))
	// A spoofed caller header must be ignored in favour of the principal.
	req.Header.Set("X-Lenny-Caller", "attacker")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["acquiredBy"] != "alice@acme.com" {
		t.Errorf("acquiredBy = %v, want alice@acme.com (principal, not header)", body["acquiredBy"])
	}
}

// spec: §25.4 line 1562 — when no AuthConfig is wired (dev / embedded),
// the surface stays open and the dev caller header is honoured. This
// pins the documented fallback so the production wiring is the only
// thing that closes the surface.
func TestOpsNoAuthConfigLeavesSurfaceOpen(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore()})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("dev surface unexpectedly gated: status = %d", rec.Code)
	}
}
