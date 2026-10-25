// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// alwaysRevokedPlayground refuses every playground bearer it is asked about.
type alwaysRevokedPlayground struct{}

func (alwaysRevokedPlayground) IsBearerRevoked(context.Context, string, string) (bool, error) {
	return true, nil
}

// scopedOptions returns Options whose freshness gate is closed, whose
// revocation set holds jti, and whose playground checker refuses every
// bearer, with RevocationScope set to scope.
func scopedOptions(signer *jwt.HMACSigner, jti string, scope func(string) bool) Options {
	return Options{
		Verifier:              signer,
		MultiTenant:           true,
		Registry:              permissiveRegistry{},
		Revocations:           revokedJTIs{jti: true},
		RevocationFreshness:   staleFreshness(true),
		PlaygroundRevocations: alwaysRevokedPlayground{},
		RevocationScope:       scope,
	}
}

// serveBearerToken runs tok through h and returns the response status.
func serveBearerToken(h http.Handler, tok string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestRevocationScopeExcludesOutOfScopeBearer pins that a verified bearer
// the scope excludes meets none of the freshness gate, the revocation
// set, or the playground check, so lenny-ops can trust its dev trust-hook
// tokens without applying the Lenny-issued revocation rules to them.
//
// spec: 25.4 (The lenny-ops Service: Authentication)
func TestRevocationScopeExcludesOutOfScopeBearer(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-revoked")

	inner, got := captureHandler()
	h := Wrap(inner, scopedOptions(signer, "jti-revoked", func(string) bool { return false }))
	rr := serveBearerToken(h, tok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("out-of-scope bearer: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.Subject != "alice@acme.com" {
		t.Errorf("principal subject = %q, want alice@acme.com", got.Subject)
	}
}

// TestRevocationScopeAppliesChecksToInScopeBearer pins that a bearer the
// scope admits, and every bearer when the scope is nil, still meets the
// freshness gate first.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow)
func TestRevocationScopeAppliesChecksToInScopeBearer(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-revoked")
	for name, scope := range map[string]func(string) bool{
		"in scope":  func(string) bool { return true },
		"nil scope": nil,
	} {
		t.Run(name, func(t *testing.T) {
			inner, _ := captureHandler()
			rr := serveBearerToken(Wrap(inner, scopedOptions(signer, "jti-revoked", scope)), tok)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body = %s", rr.Code, rr.Body.String())
			}
		})
	}
}
