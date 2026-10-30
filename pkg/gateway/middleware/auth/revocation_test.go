// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §13.3 token revocation.

// revokedJTIs is an in-test RevocationChecker.
type revokedJTIs map[string]bool

func (r revokedJTIs) IsRevoked(jti string) bool { return r[jti] }

func revocationToken(t *testing.T, signer *jwt.HMACSigner, jti string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
		JWTID:    jti,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func TestBearerRejectsRevokedToken(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-revoked")

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
		Revocations: revokedJTIs{"jti-revoked": true},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status = %d, want 401; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TOKEN_REVOKED") {
		t.Errorf("revoked-token rejection should carry TOKEN_REVOKED: %s", rr.Body.String())
	}
}

func TestBearerAllowsNonRevokedTokenWhenRevocationsWired(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-live")

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
		// The cache holds a different token's jti.
		Revocations: revokedJTIs{"jti-some-other": true},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("non-revoked token: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.Subject != "alice@acme.com" {
		t.Errorf("principal subject: got %q", got.Subject)
	}
}
