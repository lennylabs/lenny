// SPDX-License-Identifier: MIT

//go:build contract

package oauth_token_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// expiresInPtr returns a pointer to n for the optional request expires_in.
func expiresInPtr(n int64) *int64 { return &n }

const tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"

// newLifetimeServer builds a Token Service with the production 900 s
// capability token lifetime, the 24 h lenny-gateway cap, and a clock fixed
// at now.
func newLifetimeServer(t *testing.T, now time.Time) (*httptest.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := mustNewServer(t, tokenservice.Options{
		CapabilityTokenTTL: jwt.DefaultCapabilityTokenTTL,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
		Now:                func() time.Time { return now },
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

// formTokenRequest posts form as application/x-www-form-urlencoded and
// returns the status and the decoded body.
func formTokenRequest(t *testing.T, ts *httptest.Server, callerTok string, form url.Values) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+callerTok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
// diagnosis: a rotation without expires_in and a far-future subject did not
// return expires_in 900. The Token Service no longer takes its default
// lifetime from Options.CapabilityTokenTTL, or the default is clamped by
// something other than the subject's exp and the per-dialect cap. A value
// of 3600 is the former hard-coded hour.
func TestOAuthTokenDefaultLifetimeIsCapabilityTokenTTL_spec_10_2(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ts, signer := newLifetimeServer(t, now)
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})
	resp, body := exchange(t, ts, subject, tokenservice.Request{
		GrantType:        tokenExchangeGrant,
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotation: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	if got, _ := body["expires_in"].(float64); got != 900 {
		t.Errorf("expires_in = %v, want 900", body["expires_in"])
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
// diagnosis: a request carrying expires_in below 60 was not rejected with
// 400 invalid_request. For JSON 0 the decoder no longer separates an absent
// field from a zero value; for form 59 the form branch no longer reads the
// field, so the request fell through to the default lifetime.
func TestOAuthTokenRejectsExpiresInBelowMinimum_spec_10_2(t *testing.T) {
	ts, signer := newLifetimeServer(t, time.Now().UTC())
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})

	resp, body := exchange(t, ts, subject, tokenservice.Request{
		GrantType:    tokenExchangeGrant,
		SubjectToken: subject,
		Scope:        "sessions:read",
		Audience:     "lenny-gateway",
		ExpiresIn:    expiresInPtr(0),
	})
	if resp.StatusCode != http.StatusBadRequest || body["error"] != "invalid_request" {
		t.Errorf("JSON expires_in 0: status %d error %v, want 400 invalid_request", resp.StatusCode, body["error"])
	}

	status, formBody := formTokenRequest(t, ts, subject, url.Values{
		"grant_type":    {tokenExchangeGrant},
		"subject_token": {subject},
		"scope":         {"sessions:read"},
		"audience":      {"lenny-gateway"},
		"expires_in":    {"59"},
	})
	if status != http.StatusBadRequest || formBody["error"] != "invalid_request" {
		t.Errorf("form expires_in 59: status %d error %v, want 400 invalid_request", status, formBody["error"])
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
// diagnosis: an oversized expires_in did not return expires_in 86400 (the
// lenny-gateway cap). A short value such as 60 means the seconds-to-duration
// conversion wrapped to a small positive value; a 400 invalid_request means
// it wrapped to a past requested_exp. The Token Service must saturate the
// value so the §13.3 min() clamps it to the per-dialect cap.
func TestOAuthTokenOversizedExpiresInClampsToDialectCap_spec_10_2(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ts, signer := newLifetimeServer(t, now)
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
		Expiry: now.Add(48 * time.Hour).Unix(),
	})
	for _, expiresIn := range []int64{18446744134, 1 << 62} {
		resp, body := exchange(t, ts, subject, tokenservice.Request{
			GrantType:    tokenExchangeGrant,
			SubjectToken: subject,
			Scope:        "sessions:read",
			Audience:     "lenny-gateway",
			ExpiresIn:    expiresInPtr(expiresIn),
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expires_in %d: status %d (body %v), want 200", expiresIn, resp.StatusCode, body)
			continue
		}
		if got, _ := body["expires_in"].(float64); got != 86400 {
			t.Errorf("expires_in %d: response expires_in = %v, want 86400", expiresIn, body["expires_in"])
		}
	}
}
