// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §4.6.1 — lenny_warmpool_idle_pod_minutes is a counter, labeled
// by pool and resource class, tracking cumulative idle pod-minutes.

// counterMinutes returns the current idle-pod-minutes counter value for
// a (pool, resource_class) label pair.
func counterMinutes(t *testing.T, pool, resourceClass string) float64 {
	t.Helper()
	return testutil.ToFloat64(idlePodMinutes.WithLabelValues(pool, resourceClass))
}

func TestIdleMeterFirstObservationOnlyBaselines(t *testing.T) {
	clk := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	m := &idleMeter{now: func() time.Time { return clk }}
	before := counterMinutes(t, "pool-baseline", "small")

	m.observe("ns/pool-baseline", "pool-baseline", "small", 5)

	if got := counterMinutes(t, "pool-baseline", "small"); got != before {
		t.Errorf("first observation accrued %v minutes, want 0 (baseline only)", got-before)
	}
}

func TestIdleMeterIntegratesIdlePodMinutes(t *testing.T) {
	clk := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	m := &idleMeter{now: func() time.Time { return clk }}
	before := counterMinutes(t, "pool-integrate", "medium")

	// Baseline at 4 idle pods.
	m.observe("ns/pool-integrate", "pool-integrate", "medium", 4)
	// Two minutes later, still 4 idle: accrue 4 pods * 2 min = 8.
	clk = clk.Add(2 * time.Minute)
	m.observe("ns/pool-integrate", "pool-integrate", "medium", 4)
	// Three more minutes at the new sample (4 idle observed at t=2):
	// accrue 4 * 3 = 12. The count observed at the START of the interval
	// is the rate, so changing to 0 now does not retroactively zero the
	// prior interval.
	clk = clk.Add(3 * time.Minute)
	m.observe("ns/pool-integrate", "pool-integrate", "medium", 0)

	if got := counterMinutes(t, "pool-integrate", "medium") - before; got != 20 {
		t.Errorf("accrued %v idle-pod-minutes, want 20 (4*2 + 4*3)", got)
	}
}

func TestIdleMeterZeroIdleAccruesNothing(t *testing.T) {
	clk := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	m := &idleMeter{now: func() time.Time { return clk }}
	before := counterMinutes(t, "pool-zero", "large")

	m.observe("ns/pool-zero", "pool-zero", "large", 0)
	clk = clk.Add(10 * time.Minute)
	m.observe("ns/pool-zero", "pool-zero", "large", 0)

	if got := counterMinutes(t, "pool-zero", "large") - before; got != 0 {
		t.Errorf("a pool with no idle pods accrued %v minutes, want 0", got)
	}
}

func TestIdleMeterForgetRebaselines(t *testing.T) {
	clk := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	m := &idleMeter{now: func() time.Time { return clk }}
	before := counterMinutes(t, "pool-forget", "small")

	m.observe("ns/pool-forget", "pool-forget", "small", 3)
	m.forget("ns/pool-forget")
	// After forget, the next observation re-baselines: the 10-minute gap
	// across the deletion is not back-filled.
	clk = clk.Add(10 * time.Minute)
	m.observe("ns/pool-forget", "pool-forget", "small", 3)

	if got := counterMinutes(t, "pool-forget", "small") - before; got != 0 {
		t.Errorf("forget did not re-baseline; accrued %v minutes across the deletion gap", got)
	}
}

// spec: §16.1 catalog (lenny_warmpool_idle_pods Gauge) and §17.8.2 line
// 1101 first-week monitoring workflow. setIdlePods must publish the
// current pool's idle-pod count to the §16.1 gauge.
func TestSetIdlePodsPublishesGauge_spec_17_8_2_1101(t *testing.T) {
	const pool = "pool-idle-gauge"
	t.Cleanup(func() { forgetIdlePods(pool) })
	setIdlePods(pool, 7)
	if got := testutil.ToFloat64(idlePods.WithLabelValues(pool)); got != 7 {
		t.Errorf("idlePods gauge for %q = %v, want 7", pool, got)
	}
	// Subsequent reconciles must refresh the gauge to the current count.
	setIdlePods(pool, 0)
	if got := testutil.ToFloat64(idlePods.WithLabelValues(pool)); got != 0 {
		t.Errorf("idlePods gauge for %q after refresh = %v, want 0", pool, got)
	}
}

// spec: §16.1 — a removed pool must not leave a stale gauge series
// behind. forgetIdlePods deletes the labeled series.
func TestForgetIdlePodsClearsSeries_spec_16_1(t *testing.T) {
	const pool = "pool-idle-forget"
	setIdlePods(pool, 4)
	forgetIdlePods(pool)
	// Re-creating the labeled series via Set initializes it to a fresh
	// zero baseline; the previous value of 4 must not survive a forget.
	if got := testutil.ToFloat64(idlePods.WithLabelValues(pool)); got != 0 {
		t.Errorf("idlePods gauge for %q survived forget at %v, want 0", pool, got)
	}
	forgetIdlePods(pool)
}

func TestIdleMeterClockSkewNeverDecrements(t *testing.T) {
	clk := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	m := &idleMeter{now: func() time.Time { return clk }}
	before := counterMinutes(t, "pool-skew", "small")

	m.observe("ns/pool-skew", "pool-skew", "small", 5)
	// Clock jumps backward (NTP correction): elapsed is negative.
	clk = clk.Add(-5 * time.Minute)
	m.observe("ns/pool-skew", "pool-skew", "small", 5)

	if got := counterMinutes(t, "pool-skew", "small") - before; got != 0 {
		t.Errorf("clock skew moved the counter by %v; a counter must never decrement", got)
	}
}
