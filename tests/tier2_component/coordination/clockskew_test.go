//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.4 Postgres-Redis clock-skew sampler, wired
// end to end against a real Postgres (Tier 1, now() clock) and a real
// Redis (Tier 2, TIME clock) through the production pgstore/redisstore
// ClockReaders. The store-level unit tests exercise the sampler only with
// fake clocks, so the wiring the sampler needs — reading the two live
// dependency clocks the §25.4 lease-expiry path is authored from — is
// never covered there. This test reads both real clocks, asserts the
// measured skew is near zero (well under the 10s tolerance), and then
// forces a large offset onto one real reader to confirm the shipped
// OpsClockSkewExceeded alert expression fires against the published gauge.
package coordination_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// gaugeSetter is a coordination.SkewSetter backed by the real
// lenny_ops_clock_skew_seconds{pair} GaugeVec — the same metric name and
// pair label the OpsClockSkewExceeded alert expression selects on — so a
// gathered sample can be evaluated against the shipped rule exactly as
// Prometheus would.
type gaugeSetter struct {
	vec *prometheus.GaugeVec
}

func (g gaugeSetter) SetClockSkew(pair string, seconds float64) {
	g.vec.WithLabelValues(pair).Set(seconds)
}

// offsetReader wraps a real ClockReader and adds a fixed offset to every
// reading, modeling a dependency whose NTP has drifted past the §25.4 10s
// tolerance while still reading the live store round-trip.
type offsetReader struct {
	inner  coordination.ClockReader
	offset time.Duration
}

func (o offsetReader) ServerTime(ctx context.Context) (time.Time, error) {
	t, err := o.inner.ServerTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return t.Add(o.offset), nil
}

// clockSkewRule returns the shipped OpsClockSkewExceeded rule from the
// §16.5 catalog, failing if it is absent so the test tracks the rule the
// platform actually renders rather than a copy.
func clockSkewRule(t *testing.T) rules.Rule {
	t.Helper()
	for _, r := range rules.Catalog() {
		if r.Name == "OpsClockSkewExceeded" {
			return r
		}
	}
	t.Fatal("OpsClockSkewExceeded rule not found in the §16.5 alert catalog")
	return rules.Rule{}
}

// alertThreshold parses the shipped OpsClockSkewExceeded expression and
// returns the numeric threshold from its right-hand side, so the firing
// check keys on the rule's own value rather than a hard-coded 10. It also
// verifies the expression selects the sampler's gauge and pair label, so
// the gauge under test is the one the alert evaluates.
func alertThreshold(t *testing.T, rule rules.Rule) float64 {
	t.Helper()
	expr, err := parser.ParseExpr(rule.Expr)
	if err != nil {
		t.Fatalf("alert expr %q does not parse as PromQL: %v", rule.Expr, err)
	}
	bin, ok := expr.(*parser.BinaryExpr)
	if !ok || bin.Op != parser.GTR {
		t.Fatalf("alert expr %q is not a `> threshold` comparison", rule.Expr)
	}
	sel, ok := bin.LHS.(*parser.VectorSelector)
	if !ok || sel.Name != "lenny_ops_clock_skew_seconds" {
		t.Fatalf("alert LHS %q does not select lenny_ops_clock_skew_seconds", rule.Expr)
	}
	var pairMatched bool
	for _, m := range sel.LabelMatchers {
		if m.Name == "pair" && m.Value == coordination.ClockSkewPair {
			pairMatched = true
		}
	}
	if !pairMatched {
		t.Fatalf("alert expr %q does not match pair=%q", rule.Expr, coordination.ClockSkewPair)
	}
	num, ok := bin.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("alert threshold in %q is not a number literal", rule.Expr)
	}
	return num.Val
}

// gatheredSkew returns the current lenny_ops_clock_skew_seconds value for
// the postgres-redis pair from reg, failing if the sampler never
// published it (the F-SH-1 regression: a gauge stuck at 0 that can never
// fire the alert).
func gatheredSkew(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != "lenny_ops_clock_skew_seconds" {
			continue
		}
		for _, m := range fam.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "pair" && lp.GetValue() == coordination.ClockSkewPair {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatalf("no lenny_ops_clock_skew_seconds{pair=%q} sample published; the OpsClockSkewExceeded alert could never fire", coordination.ClockSkewPair)
	return 0
}

// newGauge registers a fresh lenny_ops_clock_skew_seconds{pair} GaugeVec
// and returns it with its registry.
func newGauge(t *testing.T) (*prometheus.GaugeVec, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lenny_ops_clock_skew_seconds",
		Help: "§25.4 measured clock skew in seconds between dependency clocks.",
	}, []string{"pair"})
	reg.MustRegister(vec)
	return vec, reg
}

// TestClockSkewSamplerRealStoresNearZero_spec_25_4 wires the sampler over
// a real Postgres now() clock and a real Redis TIME clock and asserts the
// measured skew is near zero: two stores on one host share a clock, so
// the sample sits well under the §25.4 10s tolerance and the shipped
// OpsClockSkewExceeded alert does not fire.
//
// diagnosis: a failure means the production pgstore/redisstore
// ClockReaders do not read the two live dependency clocks the §25.4
// lease-expiry path is authored from, or the sampler does not publish the
// lenny_ops_clock_skew_seconds gauge from them — so the split-brain
// bounded-skew assumption is unmonitored and a real NTP divergence would
// be invisible.
//
// spec: §25.4 (Clock Source: "Clock skew between nodes running Postgres
// and Redis is bounded by NTP ...; lenny-ops monitors drift and alerts
// when Postgres ↔ Redis skew exceeds 10s"; Self-Monitoring: "the `>10s`
// skew condition fires the `OpsClockSkewExceeded` warning alert").
func TestClockSkewSamplerRealStoresNearZero_spec_25_4(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	vec, reg := newGauge(t)
	sampler := coordination.NewClockSkewSampler(
		pgstore.New(pg.Pool),
		redisstore.New(rd.Client),
		gaugeSetter{vec: vec},
	)

	skew, err := sampler.Sample(ctx)
	if err != nil {
		t.Fatalf("Sample against real Postgres+Redis: %v", err)
	}

	rule := clockSkewRule(t)
	threshold := alertThreshold(t, rule)

	// Two stores on one host share wall-clock; the sampled skew is the
	// read round-trip floor the §25.4 Clock Source note calls sub-second.
	// Allow generous headroom for container/CI scheduling jitter while
	// still confirming the sample is nowhere near the 10s tolerance.
	const nearZeroTolerance = 5.0
	if skew < 0 || skew > nearZeroTolerance {
		t.Fatalf("real Postgres-Redis skew = %vs, want within %vs of zero", skew, nearZeroTolerance)
	}
	if skew >= threshold {
		t.Fatalf("real skew %vs already meets the %vs alert threshold; the near-zero baseline is broken", skew, threshold)
	}

	published := gatheredSkew(t, reg)
	if published != skew {
		t.Fatalf("published gauge %vs != returned skew %vs", published, skew)
	}
	if published > threshold {
		t.Fatalf("OpsClockSkewExceeded fires at the near-zero baseline (gauge %vs > %vs)", published, threshold)
	}
}

// TestClockSkewSamplerRealStoresAlertFires_spec_25_4 forces a large
// offset onto the real Redis reader so the Postgres-Redis skew crosses
// the §25.4 10s tolerance, then confirms the shipped OpsClockSkewExceeded
// expression evaluates true against the published gauge. This is the case
// the fake-clock unit tests cannot reach: the offset rides on top of a
// live Redis TIME read, so the alert-firing path is exercised through the
// real store.
//
// diagnosis: a failure means a genuine Postgres-Redis NTP divergence past
// 10s does not raise lenny_ops_clock_skew_seconds above the alert
// threshold, so the OpsClockSkewExceeded warning never pages and the
// §25.4 bounded-skew assumption behind lease expiry and outage-epoch
// reconciliation degrades silently.
//
// spec: §25.4 (Self-Monitoring: "the `>10s` skew condition fires the
// `OpsClockSkewExceeded` warning alert").
func TestClockSkewSamplerRealStoresAlertFires_spec_25_4(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	rule := clockSkewRule(t)
	threshold := alertThreshold(t, rule)

	// Offset the live Redis clock by 2s past the shipped threshold so the
	// forced skew is unambiguously over 10s regardless of the sub-second
	// real read floor.
	forced := time.Duration(threshold+2) * time.Second
	vec, reg := newGauge(t)
	sampler := coordination.NewClockSkewSampler(
		pgstore.New(pg.Pool),
		offsetReader{inner: redisstore.New(rd.Client), offset: forced},
		gaugeSetter{vec: vec},
	)

	skew, err := sampler.Sample(ctx)
	if err != nil {
		t.Fatalf("Sample with forced offset: %v", err)
	}
	if skew <= threshold {
		t.Fatalf("forced skew = %vs, want > %vs threshold", skew, threshold)
	}

	published := gatheredSkew(t, reg)
	if published <= threshold {
		t.Fatalf("OpsClockSkewExceeded does not fire: gauge %vs <= %vs threshold", published, threshold)
	}
}
