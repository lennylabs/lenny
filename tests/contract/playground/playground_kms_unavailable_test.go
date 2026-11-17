// SPDX-License-Identifier: MIT

//go:build contract

package playground_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
)

// hardFailSigner is a jwt.Signer whose every Sign fails with an error
// other than provider throttling.
type hardFailSigner struct{}

func (hardFailSigner) Sign(context.Context, jwt.Claims) (string, error) {
	return "", errors.New("kms: sign failed")
}

func (hardFailSigner) KeyID() string { return "hard-fail" }

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: a hard signing failure on POST /v1/playground/token did not
// return 503 KMS_SIGNING_UNAVAILABLE with Retry-After: 1. The playground
// set no Retry-After before every signing-failure response carried the
// breaker's value; a different value means the handler no longer reads
// it from the *jwt.SigningError (the breaker is closed after one
// failure, so the value is 1 second).
func TestPlaygroundHardSigningErrorReturns503RetryAfter1_spec_10_2(t *testing.T) {
	h := playground.New(playground.Config{
		Enabled:            true,
		AuthMode:           playground.AuthModeDev,
		DevTenantID:        "acme",
		CapabilityTokenTTL: 900 * time.Second,
		OIDCSessionTTL:     time.Hour,
	}, playground.Options{
		Signer:   &jwt.BreakerSigner{Inner: hardFailSigner{}},
		Verifier: devSigner(),
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions: playground.NewMemorySessionStore(),
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, raw := postJSON(t, srv.URL+"/v1/playground/token", "{}", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, raw)
	}
	if body.Error.Code != "KMS_SIGNING_UNAVAILABLE" {
		t.Errorf("error.code = %q, want KMS_SIGNING_UNAVAILABLE", body.Error.Code)
	}
	if body.BearerToken != "" {
		t.Errorf("failure body carries a bearer token")
	}
}
