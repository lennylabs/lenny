// SPDX-License-Identifier: MIT

package operations

import (
	"math"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// ETAInputs is the §25.2 canonical input to the ETA computation. Each
// owning subsystem fills the fields it has; the computation selects the
// best available method per the §25.2 fallback chain.
type ETAInputs struct {
	// Now is the current wall-clock time.
	Now time.Time
	// StartedAt is when the operation began.
	StartedAt time.Time
	// LastProgressAt is the most recent timestamp at which the
	// operation reported progress. A zero value disables the
	// stalledForSeconds computation.
	LastProgressAt time.Time
	// Percent is the fractional progress in [0, 100]. A nil pointer
	// reports "no percent known" — the rate path may still apply.
	Percent *float64
	// CompletedSteps / TotalSteps report step-count progress. Either
	// or both may be nil; the percent is derived if both are set.
	CompletedSteps *int
	TotalSteps     *int
	// CurrentStep / CurrentStepDetail surface the operation's current
	// phase to the caller; the ETA path does not consume them.
	CurrentStep       string
	CurrentStepDetail string
	// Rate is the throughput metric per §25.2: an agent that distrusts
	// the server ETA can recompute its own from this value. The ETA
	// computation uses it to project completion when no historical p50
	// is available.
	Rate *conventions.RateMetric
	// RateUnit is the unit Rate.Value is measured in per second. For
	// example, if Rate is shards/second, RateUnit is the total number
	// of shards. When set with Rate.Value > 0 the rate-based ETA is
	// (RateUnit - completed) / Rate.Value. A zero RateUnit disables the
	// rate path.
	RateUnit float64
	// CompletedUnits is the units already processed (matches RateUnit's
	// unit). Used alongside RateUnit to compute the rate-based ETA.
	CompletedUnits float64
	// HistoricalP50 is the §25.2 historical p50 duration for an
	// operation of this kind on this deployment. It drives the
	// highest-confidence ETA method only once SampleSize reaches the
	// §25.2 minimum (HistoricalP50MinSamples).
	HistoricalP50 time.Duration
	// SampleSize is the number of completed operations of this kind that
	// fed HistoricalP50. The §25.2 historical_p50 method applies only
	// when SampleSize >= HistoricalP50MinSamples; below the threshold the
	// historical path is skipped (etaMethod falls through to a lower
	// method or "none").
	//
	// spec: §25.2 line 394 ("sample_size >= 3 receive etaMethod
	// historical_p50 ... below that threshold they return etaMethod none").
	SampleSize int
	// ExpectedCadence is the operation kind's expected max inter-step
	// cadence (§25.2 line 396). stalledForSeconds is populated only when
	// now - lastProgressAt exceeds it; a zero cadence disables stall
	// detection (the operation is never reported stalled).
	//
	// spec: §25.2 line 391, line 396.
	ExpectedCadence time.Duration
	// FixedPhaseDuration is the worst-confidence fallback: a constant
	// duration the subsystem documents for the phase. The ETA equals
	// max(0, FixedPhaseDuration - (Now - StartedAt)).
	FixedPhaseDuration time.Duration
}

// HistoricalP50MinSamples is the §25.2 line 394 minimum sample count an
// operation kind needs before the historical_p50 ETA method applies.
const HistoricalP50MinSamples = 3

// Compute applies the §25.2 ETA fallback chain and returns a Progress
// envelope ready for the operation response. The method selection is:
//
//  1. historical_p50 — when HistoricalP50 > 0, SampleSize >=
//     HistoricalP50MinSamples, and StartedAt is non-zero.
//  2. rate_based — when Rate.Value > 0 and either RateUnit > 0 or
//     Percent ∈ (0, 100].
//  3. linear_extrapolation — when Percent ∈ (0, 100] and StartedAt is
//     non-zero, projecting completion from the elapsed wall-clock.
//  4. fixed_phase_durations — when FixedPhaseDuration > 0.
//  5. none — no ETA possible. EtaSeconds is null.
//
// The returned Progress always carries the descriptive fields (current
// step, percent, steps) regardless of whether an ETA was producible.
func Compute(in ETAInputs) conventions.Progress {
	p := conventions.Progress{
		CurrentStep:       in.CurrentStep,
		CurrentStepDetail: in.CurrentStepDetail,
		CompletedSteps:    in.CompletedSteps,
		TotalSteps:        in.TotalSteps,
	}
	if !in.StartedAt.IsZero() {
		p.StartedAt = in.StartedAt.UTC().Format(time.RFC3339)
	}
	if !in.LastProgressAt.IsZero() {
		p.LastProgressAt = in.LastProgressAt.UTC().Format(time.RFC3339)
		// §25.2 line 391/396: stalledForSeconds is populated only when the
		// time since the last progress exceeds the operation kind's expected
		// inter-step cadence, and reports the overrun beyond that cadence. A
		// zero cadence disables stall detection; an operation advancing
		// within its cadence leaves stalledForSeconds null.
		if !in.Now.IsZero() && in.ExpectedCadence > 0 {
			overrun := in.Now.Sub(in.LastProgressAt) - in.ExpectedCadence
			if overrun > 0 {
				stalled := int(overrun.Seconds())
				p.StalledForSeconds = &stalled
			}
		}
	}

	percent := derivePercent(in)
	if percent != nil {
		p.Percent = percent
	}
	if in.Rate != nil {
		p.RateMetric = in.Rate
	}

	switch {
	case in.HistoricalP50 > 0 && in.SampleSize >= HistoricalP50MinSamples && !in.StartedAt.IsZero() && !in.Now.IsZero():
		// §25.2 line 394: a kind with sample_size >= 3 receives
		// historical_p50 with etaConfidence >= 0.5. Below the threshold the
		// historical path is skipped and a lower-confidence method applies.
		eta := historicalETA(in.Now, in.StartedAt, in.HistoricalP50)
		setETA(&p, eta, 0.85, conventions.EtaHistoricalP50)

	case in.Rate != nil && in.Rate.Value > 0 && in.RateUnit > 0:
		remaining := in.RateUnit - in.CompletedUnits
		if remaining < 0 {
			remaining = 0
		}
		eta := int(math.Ceil(remaining / in.Rate.Value))
		setETA(&p, eta, 0.7, conventions.EtaRateBased)

	case in.Rate != nil && in.Rate.Value > 0 && percent != nil && *percent > 0 && *percent < 100:
		// Without explicit RateUnit, treat the rate as percent/sec.
		remaining := 100.0 - *percent
		eta := int(math.Ceil(remaining / in.Rate.Value))
		setETA(&p, eta, 0.6, conventions.EtaRateBased)

	case percent != nil && *percent > 0 && *percent <= 100 && !in.StartedAt.IsZero() && !in.Now.IsZero():
		elapsed := in.Now.Sub(in.StartedAt).Seconds()
		if elapsed > 0 {
			projected := elapsed * (100.0 / *percent)
			remaining := projected - elapsed
			if remaining < 0 {
				remaining = 0
			}
			setETA(&p, int(math.Ceil(remaining)), 0.5, conventions.EtaLinearExtrapolation)
		}

	case in.FixedPhaseDuration > 0:
		var elapsed time.Duration
		if !in.StartedAt.IsZero() && !in.Now.IsZero() {
			elapsed = in.Now.Sub(in.StartedAt)
		}
		remaining := in.FixedPhaseDuration - elapsed
		if remaining < 0 {
			remaining = 0
		}
		setETA(&p, int(math.Ceil(remaining.Seconds())), 0.4, conventions.EtaFixedPhaseDurations)

	default:
		p.EtaMethod = conventions.EtaNone
	}
	return p
}

// derivePercent computes the percent value the canonical envelope
// reports. If the caller supplied a percent explicitly, that wins;
// otherwise the function derives it from completed/total steps. Both
// inputs missing returns nil ("no percent known"), so the caller can
// distinguish unset from zero.
func derivePercent(in ETAInputs) *float64 {
	if in.Percent != nil {
		v := *in.Percent
		switch {
		case v < 0:
			v = 0
		case v > 100:
			v = 100
		}
		return &v
	}
	if in.CompletedSteps != nil && in.TotalSteps != nil && *in.TotalSteps > 0 {
		v := float64(*in.CompletedSteps) * 100.0 / float64(*in.TotalSteps)
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		return &v
	}
	return nil
}

// historicalETA returns the remaining seconds until completion under
// the historical-p50 method: max(0, p50 - elapsed).
func historicalETA(now, started time.Time, p50 time.Duration) int {
	elapsed := now.Sub(started)
	remaining := p50 - elapsed
	if remaining < 0 {
		remaining = 0
	}
	return int(math.Ceil(remaining.Seconds()))
}

// setETA writes the chosen ETA, confidence, and method onto the
// progress envelope.
func setETA(p *conventions.Progress, seconds int, confidence float64, method conventions.EtaMethod) {
	if seconds < 0 {
		seconds = 0
	}
	p.EtaSeconds = &seconds
	p.EtaConfidence = confidence
	p.EtaMethod = method
}
