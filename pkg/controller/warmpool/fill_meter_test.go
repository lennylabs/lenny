// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// histCount reads the cumulative observation count for the
// fill-duration histogram series labeled by pool.
func histCount(t *testing.T, pool string) uint64 {
	t.Helper()
	obs, err := fillDurationSeconds.GetMetricWithLabelValues(pool)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	m := &dto.Metric{}
	if err := obs.(prometheus.Histogram).Write(m); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// spec: §4.6.1 "Cold-start pool fill" — lenny_warmpool_fill_duration_seconds
// records the time from pool creation to reaching minWarm ready pods, and
// the alert-support grace gauge is active during the grace window.
func TestFillMeterRecordsOnceReachesMinWarm(t *testing.T) {
	const pool = "fill-test-reaches"
	key := "ns/" + pool
	clock := time.Unix(1000, 0)
	m := &fillMeter{now: func() time.Time { return clock }, period: 120 * time.Second}

	// First observation opens the window; ready < minWarm, nothing recorded.
	m.observe(key, pool, 2, 0)
	if c := histCount(t, pool); c != 0 {
		t.Fatalf("fill recorded before reaching minWarm: count=%d", c)
	}
	if g := testutil.ToFloat64(fillGraceActive.WithLabelValues(pool)); g != 1 {
		t.Errorf("grace gauge = %v during grace window, want 1", g)
	}

	// 30s later the pool reaches minWarm: the fill duration is recorded.
	clock = clock.Add(30 * time.Second)
	m.observe(key, pool, 2, 2)
	if c := histCount(t, pool); c != 1 {
		t.Fatalf("fill not recorded on reaching minWarm: count=%d", c)
	}

	// A subsequent observation does not double-count the same fill.
	clock = clock.Add(10 * time.Second)
	m.observe(key, pool, 2, 2)
	if c := histCount(t, pool); c != 1 {
		t.Errorf("fill double-counted: count=%d, want 1", c)
	}
}

// spec: §4.6.1 — the grace gauge falls to 0 once the grace window
// elapses so the WarmPoolExhausted/WarmPoolLow suppression lifts.
func TestFillMeterGraceWindowExpires(t *testing.T) {
	const pool = "fill-test-grace"
	key := "ns/" + pool
	clock := time.Unix(2000, 0)
	m := &fillMeter{now: func() time.Time { return clock }, period: 60 * time.Second}

	m.observe(key, pool, 1, 0)
	if g := testutil.ToFloat64(fillGraceActive.WithLabelValues(pool)); g != 1 {
		t.Errorf("grace gauge = %v inside window, want 1", g)
	}
	clock = clock.Add(61 * time.Second)
	m.observe(key, pool, 1, 0)
	if g := testutil.ToFloat64(fillGraceActive.WithLabelValues(pool)); g != 0 {
		t.Errorf("grace gauge = %v after window, want 0", g)
	}
}

// spec: §4.6.1 "Re-activation grace period" — a minWarm 0→positive
// transition reopens the fill window and re-records a fresh duration.
func TestFillMeterReactivationReopensWindow(t *testing.T) {
	const pool = "fill-test-reactivate"
	key := "ns/" + pool
	clock := time.Unix(3000, 0)
	m := &fillMeter{now: func() time.Time { return clock }, period: 120 * time.Second}

	// Fill to minWarm, recorded once.
	m.observe(key, pool, 1, 1)
	if c := histCount(t, pool); c != 1 {
		t.Fatalf("initial fill not recorded: count=%d", c)
	}
	// Scale to zero: grace inactive, no new record.
	clock = clock.Add(time.Minute)
	m.observe(key, pool, 0, 0)
	if g := testutil.ToFloat64(fillGraceActive.WithLabelValues(pool)); g != 0 {
		t.Errorf("grace gauge active at minWarm 0, want 0")
	}
	// Re-activate (0→positive): window reopens, next reach re-records.
	clock = clock.Add(time.Minute)
	m.observe(key, pool, 2, 0)
	if g := testutil.ToFloat64(fillGraceActive.WithLabelValues(pool)); g != 1 {
		t.Errorf("grace gauge not active on re-activation, want 1")
	}
	clock = clock.Add(10 * time.Second)
	m.observe(key, pool, 2, 2)
	if c := histCount(t, pool); c != 2 {
		t.Errorf("re-activation fill not re-recorded: count=%d, want 2", c)
	}
}

// forget drops the grace series so a deleted pool re-baselines.
func TestFillMeterForget(t *testing.T) {
	const pool = "fill-test-forget"
	key := "ns/" + pool
	clock := time.Unix(4000, 0)
	m := &fillMeter{now: func() time.Time { return clock }, period: 60 * time.Second}
	m.observe(key, pool, 1, 0)
	m.forget(key, pool)
	if _, ok := m.pools[key]; ok {
		t.Errorf("forget left fill state for %q", key)
	}
}
