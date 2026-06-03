// SPDX-License-Identifier: MIT

package operations_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// spec §25.2: when no inputs support an ETA, EtaMethod is "none" and
// EtaSeconds is null.
func TestComputeZeroRateAndNoPercent(t *testing.T) {
	p := operations.Compute(operations.ETAInputs{
		Now:       time.Now(),
		StartedAt: time.Now(),
	})
	if p.EtaMethod != conventions.EtaNone {
		t.Errorf("EtaMethod = %q, want none", p.EtaMethod)
	}
	if p.EtaSeconds != nil {
		t.Errorf("EtaSeconds = %v, want nil for the none case", p.EtaSeconds)
	}
}

// spec §25.2: HistoricalP50 is the highest-confidence method; the ETA
// is max(0, p50 - elapsed).
func TestComputeHistoricalP50(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 5, 0, 0, time.UTC)
	started := now.Add(-2 * time.Minute)
	p := operations.Compute(operations.ETAInputs{
		Now:           now,
		StartedAt:     started,
		HistoricalP50: 10 * time.Minute,
		SampleSize:    operations.HistoricalP50MinSamples,
	})
	if p.EtaMethod != conventions.EtaHistoricalP50 {
		t.Errorf("EtaMethod = %q, want historical_p50", p.EtaMethod)
	}
	if p.EtaSeconds == nil || *p.EtaSeconds != 8*60 {
		t.Errorf("EtaSeconds = %v, want 480 (10min p50 - 2min elapsed)", p.EtaSeconds)
	}
	if p.EtaConfidence < 0.8 {
		t.Errorf("EtaConfidence = %v, want >= 0.85 for historical_p50", p.EtaConfidence)
	}
}

// spec §25.2: HistoricalP50 clamps to 0 when elapsed >= p50.
func TestComputeHistoricalP50ClampsAtZero(t *testing.T) {
	now := time.Now()
	p := operations.Compute(operations.ETAInputs{
		Now:           now,
		StartedAt:     now.Add(-time.Hour),
		HistoricalP50: 10 * time.Minute,
		SampleSize:    operations.HistoricalP50MinSamples,
	})
	if p.EtaSeconds == nil || *p.EtaSeconds != 0 {
		t.Errorf("EtaSeconds = %v, want 0 when elapsed exceeds p50", p.EtaSeconds)
	}
}

// spec §25.2 line 394: below HistoricalP50MinSamples (3) the historical
// method is skipped even when a p50 is present — etaMethod falls through
// to a lower-confidence method, or "none" when nothing else applies.
func TestComputeHistoricalP50BelowSampleThresholdFallsThrough(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 5, 0, 0, time.UTC)
	p := operations.Compute(operations.ETAInputs{
		Now:           now,
		StartedAt:     now.Add(-2 * time.Minute),
		HistoricalP50: 10 * time.Minute,
		SampleSize:    operations.HistoricalP50MinSamples - 1,
	})
	if p.EtaMethod == conventions.EtaHistoricalP50 {
		t.Errorf("EtaMethod = historical_p50, want fall-through below %d samples", operations.HistoricalP50MinSamples)
	}
	if p.EtaMethod != conventions.EtaNone {
		t.Errorf("EtaMethod = %q, want none (no other method applies)", p.EtaMethod)
	}
}

// spec §25.2: with Rate and RateUnit, the ETA is the remaining units
// divided by the throughput.
func TestComputeRateBasedWithUnit(t *testing.T) {
	p := operations.Compute(operations.ETAInputs{
		Now:            time.Now(),
		StartedAt:      time.Now().Add(-time.Minute),
		Rate:           &conventions.RateMetric{Name: "shards_per_second", Value: 2},
		RateUnit:       100,
		CompletedUnits: 20,
	})
	if p.EtaMethod != conventions.EtaRateBased {
		t.Errorf("EtaMethod = %q, want rate_based", p.EtaMethod)
	}
	// (100 - 20) / 2 = 40 seconds.
	if p.EtaSeconds == nil || *p.EtaSeconds != 40 {
		t.Errorf("EtaSeconds = %v, want 40", p.EtaSeconds)
	}
	if p.RateMetric == nil || p.RateMetric.Name != "shards_per_second" {
		t.Errorf("RateMetric not surfaced: %+v", p.RateMetric)
	}
}

// spec §25.2: with Rate but only Percent (no RateUnit), the
// computation treats Rate as percent/sec.
func TestComputeRateBasedWithPercent(t *testing.T) {
	pct := 40.0
	p := operations.Compute(operations.ETAInputs{
		Now:       time.Now(),
		StartedAt: time.Now().Add(-time.Minute),
		Rate:      &conventions.RateMetric{Value: 0.5}, // percent per second
		Percent:   &pct,
	})
	if p.EtaMethod != conventions.EtaRateBased {
		t.Errorf("EtaMethod = %q, want rate_based", p.EtaMethod)
	}
	// (100 - 40) / 0.5 = 120 seconds.
	if p.EtaSeconds == nil || *p.EtaSeconds != 120 {
		t.Errorf("EtaSeconds = %v, want 120", p.EtaSeconds)
	}
}

// spec §25.2: with only Percent and a known startedAt, the linear
// extrapolation projects completion from elapsed wall-clock.
func TestComputeLinearExtrapolationFromPercent(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 5, 0, 0, time.UTC)
	pct := 25.0
	p := operations.Compute(operations.ETAInputs{
		Now:       now,
		StartedAt: now.Add(-time.Minute),
		Percent:   &pct,
	})
	if p.EtaMethod != conventions.EtaLinearExtrapolation {
		t.Errorf("EtaMethod = %q, want linear_extrapolation", p.EtaMethod)
	}
	// 1 minute = 25% → 4 minutes total → 3 minutes remaining = 180 sec.
	if p.EtaSeconds == nil || *p.EtaSeconds != 180 {
		t.Errorf("EtaSeconds = %v, want 180", p.EtaSeconds)
	}
}

// spec §25.2: with only FixedPhaseDuration, the ETA is the constant
// duration minus elapsed.
func TestComputeFixedPhaseDuration(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	p := operations.Compute(operations.ETAInputs{
		Now:                now,
		StartedAt:          now.Add(-30 * time.Second),
		FixedPhaseDuration: 2 * time.Minute,
	})
	if p.EtaMethod != conventions.EtaFixedPhaseDurations {
		t.Errorf("EtaMethod = %q, want fixed_phase_durations", p.EtaMethod)
	}
	if p.EtaSeconds == nil || *p.EtaSeconds != 90 {
		t.Errorf("EtaSeconds = %v, want 90 (120s fixed - 30s elapsed)", p.EtaSeconds)
	}
}

// spec §25.2: percent is derivable from steps when both are supplied.
func TestComputeDerivesPercentFromSteps(t *testing.T) {
	now := time.Now()
	completed, total := 3, 4
	p := operations.Compute(operations.ETAInputs{
		Now:            now,
		StartedAt:      now.Add(-time.Minute),
		CompletedSteps: &completed,
		TotalSteps:     &total,
	})
	if p.Percent == nil || *p.Percent != 75 {
		t.Errorf("Percent = %v, want 75 (3 of 4 steps)", p.Percent)
	}
}

// spec §25.2: an explicit percent outside [0, 100] is clamped.
func TestComputeClampsPercent(t *testing.T) {
	now := time.Now()
	tooBig := 250.0
	p := operations.Compute(operations.ETAInputs{
		Now:       now,
		StartedAt: now.Add(-time.Minute),
		Percent:   &tooBig,
	})
	if p.Percent == nil || *p.Percent != 100 {
		t.Errorf("Percent = %v, want clamp to 100", p.Percent)
	}
}

// spec §25.2 lines 391/396: stalledForSeconds is the overrun beyond the
// kind's expected cadence — populated only once now-lastProgressAt
// exceeds the cadence, and reporting the excess.
func TestComputeStalledForSeconds(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	p := operations.Compute(operations.ETAInputs{
		Now:             now,
		StartedAt:       now.Add(-time.Hour),
		LastProgressAt:  now.Add(-10 * time.Minute),
		ExpectedCadence: 5 * time.Minute,
	})
	// 10 min since last progress, 5 min cadence -> 5 min (300s) overrun.
	if p.StalledForSeconds == nil || *p.StalledForSeconds != 300 {
		t.Errorf("StalledForSeconds = %v, want 300", p.StalledForSeconds)
	}
}

// spec §25.2 line 391: an operation advancing within its cadence is not
// stalled — stalledForSeconds stays null.
func TestComputeStalledForSecondsNilWithinCadence(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	p := operations.Compute(operations.ETAInputs{
		Now:             now,
		StartedAt:       now.Add(-time.Hour),
		LastProgressAt:  now.Add(-1 * time.Minute),
		ExpectedCadence: 5 * time.Minute,
	})
	if p.StalledForSeconds != nil {
		t.Errorf("StalledForSeconds = %v, want nil within cadence", *p.StalledForSeconds)
	}
}

// spec §25.2 line 396: a zero cadence disables stall detection entirely,
// even when progress has not advanced for a long time.
func TestComputeStalledForSecondsNilWithoutCadence(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	p := operations.Compute(operations.ETAInputs{
		Now:            now,
		StartedAt:      now.Add(-time.Hour),
		LastProgressAt: now.Add(-30 * time.Minute),
	})
	if p.StalledForSeconds != nil {
		t.Errorf("StalledForSeconds = %v, want nil with no cadence", *p.StalledForSeconds)
	}
}
