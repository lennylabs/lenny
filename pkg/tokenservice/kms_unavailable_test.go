// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// breakerStubSigner wraps an HMACSigner so a test injects a Sign
// failure while the verifier path stays real (it verifies the caller
// token).
type breakerStubSigner struct {
	inner *jwt.HMACSigner
	err   error
}

func (s *breakerStubSigner) Sign(_ context.Context, c jwt.Claims) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.inner.Sign(c)
}

func (s *breakerStubSigner) KeyID() string { return s.inner.KeyID() }

// kmsUnavailableFixture is a Token Service whose signer is a
// BreakerSigner with a fixed clock over a breakerStubSigner.
type kmsUnavailableFixture struct {
	srv     *Server
	stub    *breakerStubSigner
	breaker *jwt.BreakerSigner
	caller  string
}

func newKMSUnavailableFixture(t *testing.T, err error) *kmsUnavailableFixture {
	t.Helper()
	hmac := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	stub := &breakerStubSigner{inner: hmac, err: err}
	now := time.Unix(5_000_000, 0)
	breaker := &jwt.BreakerSigner{
		Inner: stub,
		Now:   func() time.Time { return now },
	}
	srv := mustNewServer(t, Options{
		Signer:   breaker,
		Verifier: hmac,
		Issuer:   "https://lenny.dev.test/token",
	})
	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	caller, signErr := hmac.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: farFuture,
	})
	if signErr != nil {
		t.Fatalf("mint caller: %v", signErr)
	}
	return &kmsUnavailableFixture{srv: srv, stub: stub, breaker: breaker, caller: caller}
}

// exchange runs one exchange and returns the status, Retry-After, and
// the decoded Lenny error envelope.
func (f *kmsUnavailableFixture) exchange(t *testing.T) (int, string, string, bool) {
	t.Helper()
	resp := doExchange(t, f.srv, f.caller, Request{
		GrantType: grantTypeExchange, SubjectToken: f.caller,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, resp.Header.Get("Retry-After"), env.Error.Code, env.Error.Retryable
}

func assertKMSUnavailable(t *testing.T, status int, retryAfter, code string, retryable bool, wantRetryAfter string) {
	t.Helper()
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if code != "KMS_SIGNING_UNAVAILABLE" {
		t.Errorf("error.code = %q, want KMS_SIGNING_UNAVAILABLE", code)
	}
	if !retryable {
		t.Errorf("error.retryable = false, want true")
	}
	if retryAfter != wantRetryAfter {
		t.Errorf("Retry-After = %q, want %q", retryAfter, wantRetryAfter)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: a hard signing failure with the breaker closed did not
// return 503 KMS_SIGNING_UNAVAILABLE with Retry-After: 1. Before every
// Sign error mapped to the signing-failure response, the Token Service
// answered it with 500 server_error.
func TestTokenServiceHardSigningErrorReturns503RetryAfter1_spec_10_2(t *testing.T) {
	f := newKMSUnavailableFixture(t, errors.New("kms outage"))
	status, retryAfter, code, retryable := f.exchange(t)
	assertKMSUnavailable(t, status, retryAfter, code, retryable, "1")
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: a throttled sign did not return 503 KMS_SIGNING_UNAVAILABLE
// with Retry-After: 1, or throttling counted toward the breaker trip.
// Throttling is backpressure, so any number of throttled signs leaves
// the breaker closed and the next unthrottled sign succeeds.
func TestTokenServiceThrottledSignReturns503AndKeepsBreakerClosed_spec_10_2(t *testing.T) {
	f := newKMSUnavailableFixture(t, kms.ErrThrottled)
	for i := 0; i < 6; i++ {
		status, retryAfter, code, retryable := f.exchange(t)
		assertKMSUnavailable(t, status, retryAfter, code, retryable, "1")
	}
	f.stub.err = nil
	if status, _, _, _ := f.exchange(t); status != http.StatusOK {
		t.Fatalf("status after throttling cleared = %d, want 200 (breaker must stay closed)", status)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: once four hard failures opened the breaker, the refusal did
// not carry Retry-After equal to the remaining cooldown. The Token
// Service wrote a fixed Retry-After: 30 before it read the breaker's
// value; this case moves the clock 12 s into the cooldown, so the fixed
// value and the breaker's value differ.
func TestTokenServiceOpenBreakerRetryAfterIsRemainingCooldown_spec_10_2(t *testing.T) {
	f := newKMSUnavailableFixture(t, errors.New("kms outage"))
	for i := 0; i < 4; i++ {
		f.exchange(t)
	}
	opened := f.breaker.Now()
	f.breaker.Now = func() time.Time { return opened.Add(12 * time.Second) }
	status, retryAfter, code, retryable := f.exchange(t)
	assertKMSUnavailable(t, status, retryAfter, code, retryable, "18")
}
