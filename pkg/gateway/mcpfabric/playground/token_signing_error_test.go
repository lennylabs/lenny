// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// signingErrorSigner is a jwt.Signer whose every Sign returns err.
type signingErrorSigner struct{ err error }

func (s signingErrorSigner) Sign(context.Context, jwt.Claims) (string, error) {
	return "", s.err
}

func (signingErrorSigner) KeyID() string { return "fake" }

// mintStatus posts a dev-mode mint against a handler whose signer is
// signer and returns the response status, Retry-After, and error code.
func mintStatus(t *testing.T, signer jwt.Signer) (int, string, string) {
	t.Helper()
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme", CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer:   signer,
		Verifier: devSigner(),
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	var env errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, resp.Header.Get("Retry-After"), env.Error.Code
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 27.3.1
// diagnosis: the playground mint answered a signing error without the
// breaker's Retry-After. A throttled sign carries 1 second and a refusal
// by an open breaker with 2.3 s of cooldown left carries 3 (rounded up);
// the playground set no Retry-After before every signing-failure
// response carried the value.
func TestPlaygroundMintSigningErrorRetryAfter_spec_10_2(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"throttled", &jwt.SigningError{RetryAfter: time.Second, Err: kms.ErrThrottled}, "1"},
		{"breaker open 2.3s", &jwt.SigningError{RetryAfter: 2300 * time.Millisecond, Err: jwt.ErrSigningUnavailable}, "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, retryAfter, code := mintStatus(t, signingErrorSigner{err: tc.err})
			if status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", status)
			}
			if code != "KMS_SIGNING_UNAVAILABLE" {
				t.Errorf("error.code = %q, want KMS_SIGNING_UNAVAILABLE", code)
			}
			if retryAfter != tc.want {
				t.Errorf("Retry-After = %q, want %q", retryAfter, tc.want)
			}
		})
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: a playground handler with no signer answered without
// Retry-After; every KMS_SIGNING_UNAVAILABLE response carries it, at
// 1 second while no breaker is open.
func TestPlaygroundMintNoSignerRetryAfter1_spec_10_2(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme", CapabilityTokenTTL: 900 * time.Second}, Options{
		Verifier: devSigner(),
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
}
