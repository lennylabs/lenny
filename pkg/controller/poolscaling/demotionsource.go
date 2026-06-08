// SPDX-License-Identifier: MIT

// This file holds the production DemotionRateSource the §6.1 SDK-warm
// circuit breaker consumes. The PoolScalingController runs in its own
// process, separate from the gateway replicas that emit the demotion
// metrics, so it cannot maintain the rolling window in its own memory
// from the raw counter increments. Instead it queries a
// Prometheus-compatible backend for the rolling rate, the same way the
// §4.6.2 DemandSource reads the claim-rate metrics.
//
// The two §6.1 windows are:
//
//   - 5-minute — the circuit-breaker trip input (§6.1 line 50, hardcoded
//     90% threshold).
//   - 1-hour — the SDKWarmDemotionRateHigh warning-event input (§6.1
//     line 48, demotionRateThreshold default 60%).
//
// Both are computed as the ratio of the SDK-warm demotion rate to the
// claim rate over the window:
//
//	sum(rate(lenny_warmpool_sdk_demotions_total{pool="X"}[W]))
//	  / sum(rate(lenny_warmpool_claims_total{pool="X"}[W]))
//
// The numerator and denominator are queried separately and divided in
// Go so a zero claim rate (no claims in the window) reports
// HasSample=false rather than a NaN or a divide-by-zero PromQL result.

package poolscaling

import (
	"context"
	"fmt"
	"strings"
)

// PromQLQuerier runs a Prometheus instant query and returns the scalar
// result. It is satisfied by pkg/ops/metrics.PrometheusClient. Defining
// the seam here keeps the controller package decoupled from the
// Prometheus client wiring and lets tests supply a fake.
type PromQLQuerier interface {
	Query(ctx context.Context, q string) (float64, error)
}

// PrometheusDemotionSource is the production DemotionRateSource. It
// queries a Prometheus-compatible backend for the rolling 5-minute and
// 1-hour SDK-warm demotion rates per pool. spec: §6.1 lines 48, 50.
type PrometheusDemotionSource struct {
	// Querier runs the instant queries. Required.
	Querier PromQLQuerier
}

var _ DemotionRateSource = (*PrometheusDemotionSource)(nil)

// PoolDemotionSignal computes the rolling 5-minute and 1-hour demotion
// rates for poolName. A query error is returned to the caller so the
// reconcile fails closed (a tripped breaker is held open rather than
// auto-closed on a missing signal).
func (s *PrometheusDemotionSource) PoolDemotionSignal(ctx context.Context, poolName string) (DemotionSignal, error) {
	rate5m, has5m, err := s.windowRate(ctx, poolName, "5m")
	if err != nil {
		return DemotionSignal{}, err
	}
	rate1h, has1h, err := s.windowRate(ctx, poolName, "1h")
	if err != nil {
		return DemotionSignal{}, err
	}
	return DemotionSignal{
		Rate:          rate5m,
		HasSample:     has5m,
		HourRate:      rate1h,
		HourHasSample: has1h,
	}, nil
}

// windowRate runs the demotion- and claim-rate queries for one window and
// returns the ratio clamped to [0,1]. ok is false when the claim rate over
// the window is zero (no usable sample), in which case the demotion rate is
// not meaningful and the breaker decision treats it as a cold window.
func (s *PrometheusDemotionSource) windowRate(ctx context.Context, poolName, window string) (rate float64, ok bool, err error) {
	matcher := fmt.Sprintf("{pool=%q}", escapePromLabel(poolName))
	claims, err := s.Querier.Query(ctx,
		fmt.Sprintf("sum(rate(lenny_warmpool_claims_total%s[%s]))", matcher, window))
	if err != nil {
		return 0, false, fmt.Errorf("query warmpool claim rate (%s): %w", window, err)
	}
	if claims <= 0 {
		return 0, false, nil
	}
	demotions, err := s.Querier.Query(ctx,
		fmt.Sprintf("sum(rate(lenny_warmpool_sdk_demotions_total%s[%s]))", matcher, window))
	if err != nil {
		return 0, false, fmt.Errorf("query warmpool demotion rate (%s): %w", window, err)
	}
	r := demotions / claims
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r, true, nil
}

// escapePromLabel escapes a pool name for safe inclusion in a PromQL
// double-quoted label matcher. Pool names are already constrained by
// poolstore.ValidateName, so this is defense in depth against a malformed
// stored value reaching the query string.
func escapePromLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// demotionRateHighThresholdDefault is the §6.1 line 48
// demotionRateThreshold: when the rolling 1-hour demotion rate exceeds
// it, the PoolScalingController emits the SDKWarmDemotionRateHigh warning
// event unless the pool sets acknowledgeHighDemotionRate. It is a
// platform default rather than a per-pool knob in v1.
const demotionRateHighThresholdDefault = 0.60
