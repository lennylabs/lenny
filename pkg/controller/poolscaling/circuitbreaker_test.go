// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// tPtr returns a pointer to t, for building BreakerState fixtures.
func tPtr(t time.Time) *time.Time { return &t }

func TestEvaluateBreakerOpensWhenDemotionRateExceedsThreshold(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.95,
		HasWindowSample: true,
		Current:         poolscaling.BreakerState{},
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})

	if !d.SDKWarmDisabled {
		t.Fatal("breaker should disable SDK-warm when the demotion rate is 0.95")
	}
	if !d.State.Open {
		t.Fatal("breaker state should be Open after a trip")
	}
	if d.State.OpenedAt == nil || !d.State.OpenedAt.Equal(now) {
		t.Errorf("OpenedAt = %v, want %v", d.State.OpenedAt, now)
	}
	want := now.Add(30 * time.Minute)
	if d.State.MinOpenUntil == nil || !d.State.MinOpenUntil.Equal(want) {
		t.Errorf("MinOpenUntil = %v, want %v", d.State.MinOpenUntil, want)
	}
	if d.State.OpenedReason != "demotion_rate_exceeded" {
		t.Errorf("OpenedReason = %q, want demotion_rate_exceeded", d.State.OpenedReason)
	}
}

func TestEvaluateBreakerTripsExactlyAtThreshold(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    poolscaling.SDKWarmDemotionRateTripThreshold,
		HasWindowSample: true,
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if !d.State.Open {
		t.Fatalf("breaker should trip at exactly the %.2f threshold", poolscaling.SDKWarmDemotionRateTripThreshold)
	}
}

func TestEvaluateBreakerStaysClosedBelowThreshold(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.60,
		HasWindowSample: true,
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if d.SDKWarmDisabled || d.State.Open {
		t.Fatal("breaker should stay closed when the demotion rate is below the trip threshold")
	}
	if d.State.OpenedAt != nil || d.State.MinOpenUntil != nil {
		t.Error("a closed breaker must carry no timestamps")
	}
}

func TestEvaluateBreakerStaysOpenInsideGraceWindow(t *testing.T) {
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened.Add(30 * time.Minute)
	// 10 minutes after the trip the demotion rate has fully recovered,
	// but the grace window has not elapsed: the breaker must hold open.
	now := opened.Add(10 * time.Minute)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.0,
		HasWindowSample: true,
		Current: poolscaling.BreakerState{
			Open:         true,
			OpenedAt:     tPtr(opened),
			OpenedReason: "demotion_rate_exceeded",
			MinOpenUntil: tPtr(minOpenUntil),
		},
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if !d.State.Open || !d.SDKWarmDisabled {
		t.Fatal("breaker must stay open inside the minOpenUntil grace window even at a 0 demotion rate")
	}
	if !d.State.OpenedAt.Equal(opened) {
		t.Errorf("OpenedAt = %v, want the original trip time %v (timestamps must not churn)", d.State.OpenedAt, opened)
	}
	if !d.State.MinOpenUntil.Equal(minOpenUntil) {
		t.Errorf("MinOpenUntil = %v, want the original %v", d.State.MinOpenUntil, minOpenUntil)
	}
}

func TestEvaluateBreakerClosesAfterGraceWindowWhenRateRecovers(t *testing.T) {
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened.Add(30 * time.Minute)
	now := opened.Add(31 * time.Minute)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.10,
		HasWindowSample: true,
		Current: poolscaling.BreakerState{
			Open:         true,
			OpenedAt:     tPtr(opened),
			OpenedReason: "demotion_rate_exceeded",
			MinOpenUntil: tPtr(minOpenUntil),
		},
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if d.State.Open || d.SDKWarmDisabled {
		t.Fatal("breaker must close once the grace window elapses and the demotion rate has recovered")
	}
	if d.State.OpenedAt != nil || d.State.MinOpenUntil != nil {
		t.Error("a closed breaker must clear all persisted timestamps")
	}
}

func TestEvaluateBreakerReTripsAfterGraceWindowWhenRateStillHigh(t *testing.T) {
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened.Add(30 * time.Minute)
	now := opened.Add(31 * time.Minute)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.99,
		HasWindowSample: true,
		Current: poolscaling.BreakerState{
			Open:         true,
			OpenedAt:     tPtr(opened),
			OpenedReason: "demotion_rate_exceeded",
			MinOpenUntil: tPtr(minOpenUntil),
		},
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if !d.State.Open || !d.SDKWarmDisabled {
		t.Fatal("breaker must re-trip when the demotion rate is still high after the grace window")
	}
	if !d.State.OpenedAt.Equal(now) {
		t.Errorf("re-trip OpenedAt = %v, want the re-evaluation time %v", d.State.OpenedAt, now)
	}
	wantUntil := now.Add(30 * time.Minute)
	if !d.State.MinOpenUntil.Equal(wantUntil) {
		t.Errorf("re-trip MinOpenUntil = %v, want %v", d.State.MinOpenUntil, wantUntil)
	}
}

func TestEvaluateBreakerHoldsOpenWithoutWindowSampleAfterFailover(t *testing.T) {
	// §6.1 leader-failover guard: the fresh leader's rolling window has
	// not refilled (HasWindowSample false), so even though the grace
	// window has elapsed, a zero demotion rate must not auto-close the
	// breaker.
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened.Add(30 * time.Minute)
	now := opened.Add(31 * time.Minute)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.0,
		HasWindowSample: false,
		Current: poolscaling.BreakerState{
			Open:         true,
			OpenedAt:     tPtr(opened),
			OpenedReason: "demotion_rate_exceeded",
			MinOpenUntil: tPtr(minOpenUntil),
		},
		MinOpenDuration: 30 * time.Minute,
		Now:             now,
	})
	if !d.State.Open {
		t.Fatal("breaker must stay open when the post-failover rolling window has no usable sample yet")
	}
}

func TestEvaluateBreakerNegativeMinOpenSelectsDefault(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.95,
		HasWindowSample: true,
		MinOpenDuration: -1,
		Now:             now,
	})
	// A negative MinOpenDuration resolves to the §6.1 default of 30m.
	want := now.Add(30 * time.Minute)
	if d.State.MinOpenUntil == nil || !d.State.MinOpenUntil.Equal(want) {
		t.Errorf("MinOpenUntil = %v, want the 30m default %v", d.State.MinOpenUntil, want)
	}
}

func TestEvaluateBreakerZeroMinOpenAllowsImmediateClose(t *testing.T) {
	// With the grace window disabled (MinOpenDuration 0) an open breaker
	// closes on the next reconcile once the rate recovers.
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened // zero grace window: minOpenUntil == openedAt
	now := opened.Add(1 * time.Second)
	d := poolscaling.EvaluateBreaker(poolscaling.BreakerInputs{
		DemotionRate:    0.10,
		HasWindowSample: true,
		Current: poolscaling.BreakerState{
			Open:         true,
			OpenedAt:     tPtr(opened),
			OpenedReason: "demotion_rate_exceeded",
			MinOpenUntil: tPtr(minOpenUntil),
		},
		MinOpenDuration: 0,
		Now:             now,
	})
	if d.State.Open {
		t.Fatal("breaker with a disabled grace window must close as soon as the rate recovers")
	}
}
