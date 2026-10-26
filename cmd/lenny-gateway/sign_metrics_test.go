// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
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

// newObservedGatewaySigner binds a kmsBreakerObserver to a fresh
// gatewaymetrics registry and wraps inner in a BreakerSigner that opens
// after two counted failures.
func newObservedGatewaySigner(t *testing.T, inner jwt.Signer) (*jwt.BreakerSigner, *gatewaymetrics.Metrics) {
	t.Helper()
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	obs := &kmsBreakerObserver{}
	obs.SetMetrics(m)
	return &jwt.BreakerSigner{Inner: inner, Observer: obs, Threshold: 1}, m
}

func assertLine(t *testing.T, body, line string, want bool) {
	t.Helper()
	if got := strings.Contains(body, line+"\n"); got != want {
		t.Errorf("exposition contains %q = %v, want %v", line, got, want)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 16.1 (Metrics)
// diagnosis: the gateway breaker observer counted a sign outcome on the
// wrong series. A throttled sign counts outcome="throttled" and leaves
// the signing-errors counter alone; a hard failure counts outcome="error"
// and reason="inner"; a refusal by the open breaker counts
// outcome="circuit_open" and reason="rejected"; a success counts
// outcome="ok". Before the observer fed the sign-requests counter, no
// gateway sign request was counted at all.
func TestGatewaySignMetricsPerOutcome_spec_16_1(t *testing.T) {
	ctx := context.Background()
	inner := &scriptedSigner{err: kms.ErrThrottled}
	signer, m := newObservedGatewaySigner(t, inner)

	_, _ = signer.Sign(ctx, jwt.Claims{})
	body := scrapeMetrics(t, m)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="gateway",outcome="throttled"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="inner"} 1`, false)

	inner.err = errors.New("kms down")
	_, _ = signer.Sign(ctx, jwt.Claims{})
	body = scrapeMetrics(t, m)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="gateway",outcome="error"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="inner"} 1`, true)

	_, _ = signer.Sign(ctx, jwt.Claims{}) // second counted failure opens the breaker
	_, err := signer.Sign(ctx, jwt.Claims{})
	if !errors.Is(err, jwt.ErrSigningUnavailable) {
		t.Fatalf("Sign after trip = %v, want a breaker refusal", err)
	}
	body = scrapeMetrics(t, m)
	assertLine(t, body, `lenny_jwt_sign_requests_total{component="gateway",outcome="circuit_open"} 1`, true)
	assertLine(t, body, `lenny_gateway_kms_signing_errors_total{reason="rejected"} 1`, true)

	okSigner, okMetrics := newObservedGatewaySigner(t, &scriptedSigner{})
	if _, err := okSigner.Sign(ctx, jwt.Claims{}); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	assertLine(t, scrapeMetrics(t, okMetrics), `lenny_jwt_sign_requests_total{component="gateway",outcome="ok"} 1`, true)
}

// spec: 16.5 (Alerting Rules and SLOs) "KMSSignQuotaApproaching", 17.8.2 (Capacity Tier Reference)
// diagnosis: the gateway did not emit the configured provider Sign quota
// on lenny_kms_sign_quota_per_second, so KMSSignQuotaApproaching has no
// threshold to compare the fleet sign rate against, or an unset quota
// exposed a positive sample and could fire the alert.
func TestGatewayEmitsKMSSignQuotaGauge_spec_16_5(t *testing.T) {
	cases := []struct {
		quota int
		line  string
	}{
		{50, "lenny_kms_sign_quota_per_second 50"},
		{0, "lenny_kms_sign_quota_per_second 0"},
	}
	for _, tc := range cases {
		m, err := gatewaymetrics.New()
		if err != nil {
			t.Fatalf("gatewaymetrics.New: %v", err)
		}
		emitKMSSignQuota(m, tc.quota)
		assertLine(t, scrapeMetrics(t, m), tc.line, true)
	}
}
