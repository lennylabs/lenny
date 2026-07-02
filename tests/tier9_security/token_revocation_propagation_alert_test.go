// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for SEC-TS-1 (§16.5 / §16.7). The
// lenny_token_revocation_propagation_seconds histogram had no producer:
// the Token Service's propagateRevocation returned the §16.7
// propagation_mode string for the audit row but never observed a
// latency, so the TokenRevocationPropagationLag alert — which reads the
// histogram's outcome="eventbus" bucket — could never fire. A revoked
// token that propagates slowly to peer replicas is a security-relevant
// staleness window: a revoked credential remains honored on peers past
// the SLO, and an alert that cannot fire hides it.
//
// This test wires the real Prometheus emitter (the production producer)
// into a real Token Service, drives a revoked-token propagation whose
// EventBus publish takes a controllable time, gathers the histogram the
// emitter recorded, and evaluates the shipped
// TokenRevocationPropagationLag alert's P99-over-threshold semantics
// against those samples. The alert's condition holds only because the
// producer now observes the latency; against the pre-fix code the
// emitter recorded no eventbus-outcome buckets at all, so the alert's
// P99 selector has no series and the alert can never fire.
package tier9_security

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/promemit"
)

// The §16.5 SLO the TokenRevocationPropagationLag alert enforces: P99
// eventbus propagation latency must stay under 50ms.
const propagationP99SLOSeconds = 0.05

// mutClock is a mutable clock a test can advance to synthesize a
// controllable propagation latency the emitter observes.
type mutClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mutClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mutClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// revStore satisfies IssuedTokenStore + RevocationStore for the
// recursive-revocation branch without Postgres.
type revStore struct {
	revoked []issuedtokenstore.RevokedToken
}

func (s *revStore) Record(context.Context, issuedtokenstore.IssuedToken) error { return nil }

func (s *revStore) RevokeCascade(_ context.Context, _, _, _, _ string, _ time.Time) ([]issuedtokenstore.RevokedToken, error) {
	return s.revoked, nil
}

// tokenRevocationLagRule returns the shipped TokenRevocationPropagationLag
// alert rule from the §16.5 catalog.
func tokenRevocationLagRule(t *testing.T) rules.Rule {
	t.Helper()
	for _, r := range rules.Catalog() {
		if r.Name == "TokenRevocationPropagationLag" {
			return r
		}
	}
	t.Fatalf("TokenRevocationPropagationLag not present in the §16.5 alert catalog")
	return rules.Rule{}
}

// driveEventBusRevocation runs one recursive revocation whose EventBus
// publish advances the injected clock by publishLatency, so the real
// promemit emitter observes that latency into the eventbus bucket of
// lenny_token_revocation_propagation_seconds. It returns the emitter so
// the caller can gather the recorded histogram.
func driveEventBusRevocation(t *testing.T, publishLatency time.Duration) *promemit.Emitter {
	t.Helper()
	emitter, err := promemit.New()
	if err != nil {
		t.Fatalf("promemit.New: %v", err)
	}
	clk := &mutClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	store := &revStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
	}}
	srv := tokenservice.NewServer(tokenservice.Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: store,
		Metrics:      emitter,
		Now:          clk.now,
		// A non-zero rate limit makes the Server's clockNow() read the
		// injected clock, so the propagation timing reflects the clock
		// the propagator advances. The high limit never rejects.
		RateLimit: tokenservice.RateLimitOptions{CallerPerSecond: 1_000_000, SampleWindow: 10 * time.Second},
		// The EventBus publish "takes" publishLatency by advancing the
		// clock; it succeeds, so the observed outcome is eventbus.
		RevocationPropagator: func(context.Context, string, string) error {
			clk.advance(publishLatency)
			return nil
		},
	})

	caller := mintCaller(t, signer, "alice@acme.com", "acme", "")
	subject := mintCaller(t, signer, "alice@acme.com", "acme", "root-jti")

	body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"requested_token_type":%q}`,
		"urn:ietf:params:oauth:grant-type:token-exchange", subject,
		"urn:ietf:params:oauth:token-type:access_token:revoked")
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revocation status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	return emitter
}

func mintCaller(t *testing.T, signer *jwt.HMACSigner, sub, tenant, jti string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject: sub, TenantID: tenant, JWTID: jti, Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"},
		Expiry:   time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		IssuedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// gatherEventBusHistogram pulls the eventbus-outcome histogram of
// lenny_token_revocation_propagation_seconds from the emitter's
// registry, or fails if the producer recorded no such series.
func gatherEventBusHistogram(t *testing.T, e *promemit.Emitter) *dto.Histogram {
	t.Helper()
	families, err := e.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != "lenny_token_revocation_propagation_seconds" {
			continue
		}
		for _, m := range fam.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "outcome" && lp.GetValue() == "eventbus" {
					return m.GetHistogram()
				}
			}
		}
	}
	t.Fatalf("no eventbus-outcome histogram sample recorded; the SEC-TS-1 producer did not observe")
	return nil
}

// histogramQuantile computes the Prometheus histogram_quantile(q, ...)
// estimate from cumulative bucket counts, matching the interpolation the
// alert's histogram_quantile(0.99, ...) uses at query time. This is the
// same arithmetic the alert expression evaluates in Prometheus; the
// promql engine is not linked here to avoid a transitive dependency
// version conflict, so the quantile math is reproduced faithfully.
func histogramQuantile(q float64, h *dto.Histogram) float64 {
	buckets := append([]*dto.Bucket(nil), h.GetBucket()...)
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].GetUpperBound() < buckets[j].GetUpperBound() })
	total := h.GetSampleCount()
	if total == 0 || len(buckets) == 0 {
		return 0
	}
	rank := q * float64(total)
	var prevCount float64
	var prevBound float64
	for i, b := range buckets {
		count := float64(b.GetCumulativeCount())
		if count < rank {
			prevCount = count
			prevBound = b.GetUpperBound()
			continue
		}
		upper := b.GetUpperBound()
		if upper > 1e30 { // +Inf bucket: return the last finite bound.
			if i == 0 {
				return 0
			}
			return prevBound
		}
		// Linear interpolation within the bucket, as Prometheus does.
		bucketCount := count - prevCount
		if bucketCount <= 0 {
			return upper
		}
		return prevBound + (upper-prevBound)*((rank-prevCount)/bucketCount)
	}
	return buckets[len(buckets)-1].GetUpperBound()
}

// assertAlertExprShape confirms the shipped alert expression keys on the
// eventbus outcome, the 0.99 quantile, and the 50ms threshold, so the
// quantile math below evaluates the same condition the alert does.
func assertAlertExprShape(t *testing.T, rule rules.Rule) {
	t.Helper()
	if _, err := parser.ParseExpr(rule.Expr); err != nil {
		t.Fatalf("alert expr does not parse as PromQL: %v", err)
	}
	for _, frag := range []string{
		"lenny_token_revocation_propagation_seconds_bucket",
		`outcome="eventbus"`,
		"histogram_quantile(0.99",
		"> 0.05",
	} {
		if !strings.Contains(rule.Expr, frag) {
			t.Errorf("alert expr %q missing %q; the P99/threshold semantics under test would diverge from the shipped rule", rule.Expr, frag)
		}
	}
}

// spec: 16.5 (TokenRevocationPropagationLag alert), 16.7 (token.revoked
// propagation_mode). SEC-TS-1.
// diagnosis: the SEC-TS-1 producer for
// lenny_token_revocation_propagation_seconds is unwired or emits the
// wrong outcome label. A failure here means a revoked token's
// slow-propagation SLO breach is invisible: the alert's eventbus-outcome
// series is empty, so it can never fire and a revoked credential can
// remain honored on peer replicas past the 50ms P99 SLO with no operator
// signal.
func TestTokenRevocationPropagationLagAlertFires_SEC_TS_1(t *testing.T) {
	rule := tokenRevocationLagRule(t)
	assertAlertExprShape(t, rule)

	// A 120ms eventbus publish exceeds the 50ms P99 SLO.
	emitter := driveEventBusRevocation(t, 120*time.Millisecond)
	h := gatherEventBusHistogram(t, emitter)

	p99 := histogramQuantile(0.99, h)
	if !(p99 > propagationP99SLOSeconds) {
		t.Fatalf("P99 eventbus propagation = %.4fs, want > %.2fs so TokenRevocationPropagationLag fires; the producer must observe the 120ms latency into the eventbus bucket", p99, propagationP99SLOSeconds)
	}
}

// spec: 16.5 (TokenRevocationPropagationLag alert), 16.7. SEC-TS-1.
// diagnosis: the alert threshold is mis-tuned or the producer records
// the wrong magnitude. A fast (sub-SLO) propagation must not cross the
// alert threshold; a failure here means the alert would page on healthy
// propagation.
func TestTokenRevocationPropagationLagAlertQuietWhenFast_SEC_TS_1(t *testing.T) {
	rule := tokenRevocationLagRule(t)
	assertAlertExprShape(t, rule)

	// A 5ms eventbus publish is well under the 50ms P99 SLO.
	emitter := driveEventBusRevocation(t, 5*time.Millisecond)
	h := gatherEventBusHistogram(t, emitter)

	p99 := histogramQuantile(0.99, h)
	if p99 > propagationP99SLOSeconds {
		t.Fatalf("P99 eventbus propagation = %.4fs for a 5ms publish, crossing the %.2fs SLO; the alert would fire on healthy propagation", p99, propagationP99SLOSeconds)
	}
}
