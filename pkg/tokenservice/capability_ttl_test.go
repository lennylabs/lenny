// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// lifetimeNow is the fixed clock of the capability token lifetime tests. It
// is pinned once per test binary near the wall clock, because the HMAC
// verifier checks the presented tokens' exp against time.Now.
var lifetimeNow = time.Now().UTC().Truncate(time.Second)

// lifetimeServer returns a Server with a 900 s capability token lifetime,
// the production per-dialect caps, and the fixed lifetimeNow clock.
func lifetimeServer(t *testing.T) (*Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	return mustNewServer(t, Options{
		CapabilityTokenTTL: 900 * time.Second,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     time.Hour,
		},
		Now: func() time.Time { return lifetimeNow },
	}), signer
}

// lifetimeToken mints a user bearer for alice that expires at exp and
// carries both the lenny-gateway and lenny-ops audiences.
func lifetimeToken(t *testing.T, signer *jwt.HMACSigner, jti string, exp time.Time) string {
	t.Helper()
	return mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: jti,
		Typ: auth.TokenUserBearer, Scope: "sessions:read sessions:write",
		Audience: []string{"lenny-gateway", "lenny-ops"},
		Expiry:   exp.Unix(), IssuedAt: lifetimeNow.Add(-time.Minute).Unix(),
	})
}

// lifetimeResult is the status, RFC 8693 error code, and response
// expires_in of one exchange.
type lifetimeResult struct {
	status    int
	errCode   string
	expiresIn int64
}

// decodeLifetime reads a token-endpoint response.
func decodeLifetime(t *testing.T, resp *http.Response) lifetimeResult {
	t.Helper()
	defer resp.Body.Close()
	var body struct {
		Error     string `json:"error"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return lifetimeResult{status: resp.StatusCode, errCode: body.Error, expiresIn: body.ExpiresIn}
}

// formExchange posts the request as application/x-www-form-urlencoded.
func formExchange(t *testing.T, srv *Server, callerTok string, form url.Values) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+callerTok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Result()
}

func int64Ptr(n int64) *int64 { return &n }

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestExchangeDefaultLifetimeIsCapabilityTokenTTL_spec_10_2 pins the
// requested_exp of a request without expires_in to now +
// auth.capabilityTokenTtlSeconds, clamped by the subject's exp. Before the
// default came from Options.CapabilityTokenTTL the Token Service used a
// hard-coded hour, so the far-future case returned 3600.
func TestExchangeDefaultLifetimeIsCapabilityTokenTTL_spec_10_2(t *testing.T) {
	for _, tc := range []struct {
		name       string
		subjectExp time.Time
		scope      string
		want       int64
	}{
		{"rotation, subject outlives the default", lifetimeNow.Add(48 * time.Hour), "sessions:read sessions:write", 900},
		{"scope narrowing, subject outlives the default", lifetimeNow.Add(48 * time.Hour), "sessions:read", 900},
		{"scope narrowing, subject expires first", lifetimeNow.Add(300 * time.Second), "sessions:read", 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, signer := lifetimeServer(t)
			tok := lifetimeToken(t, signer, "tok-1", tc.subjectExp)
			got := decodeLifetime(t, doExchange(t, srv, tok, Request{
				GrantType: grantTypeExchange, SubjectToken: tok,
				Scope: tc.scope, Audience: "lenny-gateway lenny-ops",
			}))
			if got.status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", got.status, got.errCode)
			}
			if got.expiresIn != tc.want {
				t.Errorf("expires_in = %d, want %d", got.expiresIn, tc.want)
			}
		})
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestExchangeRequestedExpiresInAndDialectCap_spec_10_2 pins a present
// expires_in: 3000 for the lenny-ops audience is honored, a value above the
// one-hour lenny-ops cap is clamped to the cap, and a form field of 60 gives
// exp = now + 60, which shows the form branch reads the field.
func TestExchangeRequestedExpiresInAndDialectCap_spec_10_2(t *testing.T) {
	srv, signer := lifetimeServer(t)
	tok := lifetimeToken(t, signer, "tok-1", lifetimeNow.Add(48*time.Hour))

	for _, tc := range []struct {
		name      string
		expiresIn int64
		want      int64
	}{
		{"3000 s for lenny-ops", 3000, 3000},
		{"above the lenny-ops cap", 7200, 3600},
	} {
		got := decodeLifetime(t, doExchange(t, srv, tok, Request{
			GrantType: grantTypeExchange, SubjectToken: tok,
			Scope: "sessions:read", Audience: "lenny-ops", ExpiresIn: int64Ptr(tc.expiresIn),
		}))
		if got.status != http.StatusOK || got.expiresIn != tc.want {
			t.Errorf("%s: status %d expires_in %d, want 200 and %d", tc.name, got.status, got.expiresIn, tc.want)
		}
	}

	got := decodeLifetime(t, formExchange(t, srv, tok, url.Values{
		"grant_type": {grantTypeExchange}, "subject_token": {tok},
		"scope": {"sessions:read"}, "audience": {"lenny-gateway"}, "expires_in": {"60"},
	}))
	if got.status != http.StatusOK || got.expiresIn != 60 {
		t.Errorf("form expires_in=60: status %d expires_in %d, want 200 and 60", got.status, got.expiresIn)
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestExchangeOversizedExpiresInClampsToDialectCap_spec_10_2 pins that an
// expires_in too large for a time.Duration is clamped to the per-dialect
// cap by the §13.3 min() rather than overflowing. Before the fix the
// seconds-to-nanoseconds conversion wrapped: 18446744134 wrapped to about
// now + 60 s, giving a one-minute token, and 1<<62 wrapped to a past
// requested_exp, which the exchange rejected as exp_in_past. Both the JSON
// and the form branches are exercised.
func TestExchangeOversizedExpiresInClampsToDialectCap_spec_10_2(t *testing.T) {
	srv, signer := lifetimeServer(t)
	tok := lifetimeToken(t, signer, "tok-1", lifetimeNow.Add(48*time.Hour))
	const gatewayCap = int64(24 * 60 * 60)

	for _, tc := range []struct {
		name      string
		expiresIn int64
		want      int64
	}{
		{"wraps to a short positive duration", 18446744134, gatewayCap},
		{"wraps negative", 1 << 62, gatewayCap},
		{"largest int64", math.MaxInt64, gatewayCap},
		{"largest representable duration", maxExpiresInSeconds, gatewayCap},
		{"lenny-ops cap applies", 1 << 62, 3600},
	} {
		aud := "lenny-gateway"
		if tc.want == 3600 {
			aud = "lenny-ops"
		}
		got := decodeLifetime(t, doExchange(t, srv, tok, Request{
			GrantType: grantTypeExchange, SubjectToken: tok,
			Scope: "sessions:read", Audience: aud, ExpiresIn: int64Ptr(tc.expiresIn),
		}))
		if got.status != http.StatusOK || got.expiresIn != tc.want {
			t.Errorf("json %s: status %d error %q expires_in %d, want 200 and %d",
				tc.name, got.status, got.errCode, got.expiresIn, tc.want)
		}
		got = decodeLifetime(t, formExchange(t, srv, tok, url.Values{
			"grant_type": {grantTypeExchange}, "subject_token": {tok},
			"scope": {"sessions:read"}, "audience": {aud},
			"expires_in": {strconv.FormatInt(tc.expiresIn, 10)},
		}))
		if got.status != http.StatusOK || got.expiresIn != tc.want {
			t.Errorf("form %s: status %d error %q expires_in %d, want 200 and %d",
				tc.name, got.status, got.errCode, got.expiresIn, tc.want)
		}
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestExpiresInDurationSaturates_spec_10_2 pins the conversion boundary:
// values up to maxExpiresInSeconds convert exactly, and every larger value
// saturates to the largest whole-second duration instead of wrapping.
func TestExpiresInDurationSaturates_spec_10_2(t *testing.T) {
	maxDur := time.Duration(maxExpiresInSeconds) * time.Second
	for _, tc := range []struct {
		in   int64
		want time.Duration
	}{
		{60, time.Minute},
		{maxExpiresInSeconds, maxDur},
		{maxExpiresInSeconds + 1, maxDur},
		{18446744134, maxDur},
		{1 << 62, maxDur},
		{math.MaxInt64, maxDur},
	} {
		if got := expiresInDuration(tc.in); got != tc.want || got <= 0 {
			t.Errorf("expiresInDuration(%d) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestExchangeRejectsExpiresInBelowMinimum_spec_10_2 pins the 400
// invalid_request for a present expires_in below 60. JSON 0 separates
// presence from a zero value, and form 59 shows the form branch reads the
// field; both previously fell through to the default lifetime. A form value
// that is not an integer is also rejected.
func TestExchangeRejectsExpiresInBelowMinimum_spec_10_2(t *testing.T) {
	srv, signer := lifetimeServer(t)
	tok := lifetimeToken(t, signer, "tok-1", lifetimeNow.Add(48*time.Hour))

	cases := map[string]*http.Response{
		"json 0": doExchange(t, srv, tok, Request{
			GrantType: grantTypeExchange, SubjectToken: tok,
			Scope: "sessions:read", Audience: "lenny-gateway", ExpiresIn: int64Ptr(0),
		}),
		"json -5": doExchange(t, srv, tok, Request{
			GrantType: grantTypeExchange, SubjectToken: tok,
			Scope: "sessions:read", Audience: "lenny-gateway", ExpiresIn: int64Ptr(-5),
		}),
		"form 59": formExchange(t, srv, tok, url.Values{
			"grant_type": {grantTypeExchange}, "subject_token": {tok},
			"scope": {"sessions:read"}, "audience": {"lenny-gateway"}, "expires_in": {"59"},
		}),
		"form not an integer": formExchange(t, srv, tok, url.Values{
			"grant_type": {grantTypeExchange}, "subject_token": {tok},
			"scope": {"sessions:read"}, "audience": {"lenny-gateway"}, "expires_in": {"soon"},
		}),
	}
	for name, resp := range cases {
		got := decodeLifetime(t, resp)
		if got.status != http.StatusBadRequest || got.errCode != "invalid_request" {
			t.Errorf("%s: status %d error %q, want 400 invalid_request", name, got.status, got.errCode)
		}
	}
}

// spec: 10.2 (Authentication), 13.3 (Credential Flow)
//
// TestActorExchangeTakesNoDefaultLifetime_spec_10_2 pins an actor_token
// exchange without expires_in to min(subject exp, actor exp, per-dialect
// cap); the capability default never applies. Before the change the
// requested exp was now + 1 hour, so the 2 h actor case returned 3600.
func TestActorExchangeTakesNoDefaultLifetime_spec_10_2(t *testing.T) {
	for _, tc := range []struct {
		name     string
		actorExp time.Time
		want     int64
	}{
		{"actor expires first", lifetimeNow.Add(2 * time.Hour), 7200},
		{"per-dialect cap binds", lifetimeNow.Add(72 * time.Hour), 24 * 3600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, signer := lifetimeServer(t)
			subject := lifetimeToken(t, signer, "tok-subject", lifetimeNow.Add(48*time.Hour))
			actor := lifetimeToken(t, signer, "tok-actor", tc.actorExp)
			got := decodeLifetime(t, doExchange(t, srv, subject, Request{
				GrantType: grantTypeExchange, SubjectToken: subject, ActorToken: actor,
				Scope: "sessions:read", Audience: "lenny-gateway",
			}))
			if got.status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", got.status, got.errCode)
			}
			if got.expiresIn != tc.want {
				t.Errorf("expires_in = %d, want %d", got.expiresIn, tc.want)
			}
		})
	}
}

// spec: 10.2 (Authentication)
//
// TestNewServerRefusesZeroCapabilityTokenTTL_spec_10_2 pins the
// constructor's refusal of a missing default lifetime: a zero or negative
// Options.CapabilityTokenTTL returns ErrNoCapabilityTokenTTL and no Server.
func TestNewServerRefusesZeroCapabilityTokenTTL_spec_10_2(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	for _, ttl := range []time.Duration{0, -time.Second} {
		srv, err := NewServer(Options{Signer: jwtsign.HMAC(signer), Verifier: signer, CapabilityTokenTTL: ttl})
		if !errors.Is(err, ErrNoCapabilityTokenTTL) {
			t.Errorf("NewServer(CapabilityTokenTTL=%s) err = %v, want ErrNoCapabilityTokenTTL", ttl, err)
		}
		if srv != nil {
			t.Errorf("NewServer(CapabilityTokenTTL=%s) returned a server", ttl)
		}
	}
}
