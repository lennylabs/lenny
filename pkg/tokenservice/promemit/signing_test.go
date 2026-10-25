// SPDX-License-Identifier: MIT

package promemit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// scriptedSigner is a jwt.Signer that returns err from every Sign.
type scriptedSigner struct{ err error }

func (s *scriptedSigner) Sign(context.Context, jwt.Claims) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "signed", nil
}

func (*scriptedSigner) KeyID() string { return "scripted" }

// observedSigner binds the Token Service signing observer of a fresh
// emitter to a BreakerSigner over inner that opens after two counted
// failures.
func observedSigner(t *testing.T, inner jwt.Signer) (*jwt.BreakerSigner, *Emitter) {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &jwt.BreakerSigner{Inner: inner, Observer: e.SigningObserver(), Threshold: 1}, e
}

func scrape(t *testing.T, e *Emitter) string {
	t.Helper()
	rr := httptest.NewRecorder()
	e.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rr.Body.String()
}

func assertLine(t *testing.T, body, line string, want bool) {
	t.Helper()
	if got := strings.Contains(body, line+"\n"); got != want {
		t.Errorf("exposition contains %q = %v, want %v", line, got, want)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 16.1 (Metrics)
// diagnosis: the Token Service breaker observer counted a sign outcome on
// the wrong series. A throttled sign counts outcome="throttled" and
// leaves the signing-errors counter alone; a hard failure counts
// outcome="error" and reason="inner"; a refusal by the open breaker
// counts outcome="circuit_open" and reason="rejected"; a success counts
// outcome="ok". Before the Token Service had a breaker observer, its
// signer fed neither counter.
func TestTokenServiceSignMetricsPerOutcome_spec_16_1(t *testing.T) {
	ctx := context.Background()
	inner := &scriptedSigner{err: kms.ErrThrottled}
	signer, e := observedSigner(t, inner)

	_, _ = signer.Sign(ctx, jwt.Claims{})
	body := scrape(t, e)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="token-service",outcome="throttled"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="inner"} 1`, false)

	inner.err = errors.New("kms down")
	_, _ = signer.Sign(ctx, jwt.Claims{})
	body = scrape(t, e)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="token-service",outcome="error"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="inner"} 1`, true)

	_, _ = signer.Sign(ctx, jwt.Claims{}) // second counted failure opens the breaker
	if _, err := signer.Sign(ctx, jwt.Claims{}); !errors.Is(err, jwt.ErrSigningUnavailable) {
		t.Fatalf("Sign after trip = %v, want a breaker refusal", err)
	}
	body = scrape(t, e)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="token-service",outcome="circuit_open"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="rejected"} 1`, true)

	okSigner, okEmitter := observedSigner(t, &scriptedSigner{})
	if _, err := okSigner.Sign(ctx, jwt.Claims{}); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	assertLine(t, scrape(t, okEmitter), `lenny_jwt_sign_requests_total{component="token-service",outcome="ok"} 1`, true)
}
