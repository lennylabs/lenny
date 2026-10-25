// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract case for the POST /v1/oauth/token response when the
// signer cannot obtain a signature: the Token Service signs through a
// jwt.BreakerSigner, as cmd/lenny-token-service wires it, over a signer
// whose every Sign fails with a hard error.

package oauth_token_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// hardFailSigner is a jwt.Signer whose every Sign fails with an error
// other than provider throttling.
type hardFailSigner struct{}

func (hardFailSigner) Sign(context.Context, jwt.Claims) (string, error) {
	return "", errors.New("kms: sign failed")
}

func (hardFailSigner) KeyID() string { return "hard-fail" }

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: a hard signing failure on POST /v1/oauth/token did not
// return 503 KMS_SIGNING_UNAVAILABLE with Retry-After: 1. The Token
// Service answered it with 500 server_error before the signing-failure
// rule covered every Sign error; a different Retry-After means the
// handler no longer reads the breaker's value (the breaker is closed
// after one failure, so the value is 1 second).
func TestOAuthTokenHardSigningErrorReturns503RetryAfter1_spec_10_2(t *testing.T) {
	verifier := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := mustNewServer(t, tokenservice.Options{
		Signer:        &jwt.BreakerSigner{Inner: hardFailSigner{}},
		Verifier:      verifier,
		Issuer:        "https://lenny.dev.test/token",
		PerDialectCap: map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	caller := mint(t, verifier, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read sessions:write",
	})
	resp, body := exchange(t, ts, caller, narrowing(caller))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %v", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "KMS_SIGNING_UNAVAILABLE" {
		t.Errorf("error.code = %v, want KMS_SIGNING_UNAVAILABLE; body %v", envelope["code"], body)
	}
	if envelope["retryable"] != true {
		t.Errorf("error.retryable = %v, want true", envelope["retryable"])
	}
	if _, ok := body["access_token"]; ok {
		t.Errorf("failure body carries an access_token: %v", body)
	}
}
