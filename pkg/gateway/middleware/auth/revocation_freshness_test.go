// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// staleFreshness is an in-test RevocationFreshness.
type staleFreshness bool

func (s staleFreshness) Stale() bool { return bool(s) }

// spec: §13.3 line 601 — a replica that cannot reach Postgres refuses to
// validate tokens with 503 token_validation_unavailable rather than
// honoring a possibly-revoked token from a stale revocation cache.
// F-13.3.4.
func TestBearerRejectedWhenRevocationCacheStale_F1334(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-live")

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
		// The token is not in the revocation set, but the set is stale, so
		// the replica cannot prove the token has not been revoked.
		Revocations:         revokedJTIs{},
		RevocationFreshness: staleFreshness(true),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale revocation cache: status = %d, want 503; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "token_validation_unavailable") {
		t.Errorf("stale-cache rejection should carry token_validation_unavailable: %s", rr.Body.String())
	}
}

// A fresh revocation cache validates normally: the freshness gate does
// not fire when the replica can reach Postgres.
func TestBearerAllowedWhenRevocationCacheFresh_F1334(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := revocationToken(t, signer, "jti-live")

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:            signer,
		MultiTenant:         true,
		Registry:            permissiveRegistry{},
		Revocations:         revokedJTIs{},
		RevocationFreshness: staleFreshness(false),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("fresh revocation cache: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.Subject != "alice@acme.com" {
		t.Errorf("principal subject: got %q", got.Subject)
	}
}
