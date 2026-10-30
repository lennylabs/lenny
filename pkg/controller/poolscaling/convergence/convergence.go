// SPDX-License-Identifier: MIT

// Package convergence implements the §17.8.2 cold-start bootstrap
// convergence criteria for the PoolScalingController. A pool with an
// operator-set bootstrapMinWarm override stays pinned to that static
// value (status.scalingMode: bootstrap) until every convergence
// criterion is met, at which point the controller switches to
// formula-driven scaling (status.scalingMode: formula).
//
// The criteria (spec §17.8.2 "Cold-start bootstrap procedure", step 4)
// are evaluated against inputs the controller assembles each reconcile:
// the accumulated traffic-data window, the stability of the
// formula-computed target over time, recent WarmPoolLow alert activity,
// and the ratio of the formula target to the override. The evaluator is
// pure; the stability signal is maintained by Tracker, which the
// controller feeds one formula-target sample per reconcile.
package convergence

import (
	"math"
	"sync"
	"time"
)

const (
	// MinHoursOfData is the §17.8.2 step-4 minimum accumulated traffic
	// window before a pool may converge: "At least 48 hours of traffic
	// data have been accumulated for the pool".
	MinHoursOfData = 48.0

	// MaxFormulaToOverrideRatio is the §17.8.2 step-4 cap: a formula
	// target above 3× the bootstrap override signals the override is
	// significantly undersized, so the controller emits
	// PoolBootstrapUnderprovisioned rather than converging.
	MaxFormulaToOverrideRatio = 3.0

	// DefaultStabilityWindow is the trailing span the formula target
	// must remain stable across before convergence: §17.8.2 step 4
	// "stable ... for at least 2 hours".
	DefaultStabilityWindow = 2 * time.Hour

	// DefaultMaxCoefficientOfVariation is the §17.8.2 step-4 stability
	// bound: "variance < 20% across consecutive reconciliation cycles
	// over a 1-hour rolling window". It is applied as the coefficient
	// of variation (stddev / mean) of the formula-target samples over
	// the stability window.
	DefaultMaxCoefficientOfVariation = 0.20
)

// Inputs are the per-pool signals the convergence evaluator consumes.
// The controller assembles them each reconcile from the demand source,
// the stability Tracker, the WarmPoolLow source, and the strategy's
// formula output.
type Inputs struct {
	// HasObservedDemand is true once the demand source has produced a
	// usable claim-rate sample for the pool.
	HasObservedDemand bool

	// HoursOfData is how many hours of traffic data the demand source
	// has accumulated for the pool. The 48-hour criterion reads it.
	HoursOfData float64

	// TargetStable reports whether the formula target has held steady
	// over the stability window (Tracker.Stable).
	TargetStable bool

	// WarmPoolLowRecent is true when a WarmPoolLow alert has fired for
	// the pool inside the trailing recency window (default 6h).
	WarmPoolLowRecent bool

	// FormulaTarget is the strategy's formula-computed target minWarm.
	// It is only meaningful when HasObservedDemand is true.
	FormulaTarget int

	// OverrideMinWarm is the operator-set bootstrapMinWarm override.
	OverrideMinWarm int
}

// DataSufficient reports the §17.8.2 step-4 first criterion: at least 48
// hours of traffic data accumulated with a usable demand sample.
func (in Inputs) DataSufficient() bool {
	return in.HasObservedDemand && in.HoursOfData >= MinHoursOfData
}

// Underprovisioned reports the §17.8.2 step-4 fourth criterion's failure
// case: the formula target exceeds 3× the bootstrap override, indicating
// the static override is significantly undersized. The controller emits
// PoolBootstrapUnderprovisioned rather than converging to a much larger
// formula value. It is only meaningful when a positive override is set
// and demand has been observed (so a formula target exists).
func (in Inputs) Underprovisioned() bool {
	if in.OverrideMinWarm <= 0 || !in.HasObservedDemand {
		return false
	}
	return float64(in.FormulaTarget) > MaxFormulaToOverrideRatio*float64(in.OverrideMinWarm)
}

// Converged reports whether every §17.8.2 step-4 criterion is met and
// the controller should switch the pool to formula-driven scaling.
func (in Inputs) Converged() bool {
	return in.DataSufficient() &&
		in.TargetStable &&
		!in.WarmPoolLowRecent &&
		!in.Underprovisioned()
}

// EstimatedConvergenceAt projects when a still-bootstrapping pool will
// have accumulated the 48-hour data window, measured from now. It
// returns the zero time once the data criterion is already satisfied
// (the GET bootstrapStatus omits the field in that case). Other criteria
// (stability, WarmPoolLow recency) are not time-projectable, so the
// estimate is a lower bound: the earliest the data gate could clear.
func (in Inputs) EstimatedConvergenceAt(now time.Time) time.Time {
	remaining := MinHoursOfData - in.HoursOfData
	if remaining <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(remaining * float64(time.Hour)))
}

// sample is one (timestamp, formula-target) observation.
type sample struct {
	at     time.Time
	target int
}

// poolStability is the per-pool stability state: the trailing-window
// samples used to compute the coefficient of variation, and the instant
// the current continuous-stable run began (zero when the target is not
// currently within the band).
type poolStability struct {
	samples     []sample
	stableSince time.Time
}

// Tracker maintains the per-pool formula-target stability state the
// §17.8.2 step-4 criterion reads. The controller calls Observe once per
// reconcile with the pool's current formula target; Stable reports
// whether the target has held within the coefficient-of-variation bound
// continuously for at least the stability window. A target swing beyond
// the band restarts the run; a non-positive target (no formula output)
// resets the pool entirely.
//
// The run is tracked by stableSince rather than by sample age so the
// criterion is robust to the reconcile cadence: pruning the sample ring
// to the trailing window bounds the coefficient-of-variation
// computation without capping the measurable run length.
//
// spec: §17.8.2 step 4 — formula-target stability over a rolling window.
type Tracker struct {
	mu                  sync.Mutex
	state               map[string]*poolStability
	window              time.Duration
	maxCoefficientOfVar float64
}

// NewTracker returns a Tracker with the §17.8.2 default stability window
// (2h) and coefficient-of-variation bound (0.20).
func NewTracker() *Tracker {
	return NewTrackerWithWindow(DefaultStabilityWindow, DefaultMaxCoefficientOfVariation)
}

// NewTrackerWithWindow returns a Tracker with an explicit stability
// window and coefficient-of-variation bound. Tests use it to drive the
// stability machine without two hours of wall-clock samples.
func NewTrackerWithWindow(window time.Duration, maxCoVar float64) *Tracker {
	if window <= 0 {
		window = DefaultStabilityWindow
	}
	if maxCoVar <= 0 {
		maxCoVar = DefaultMaxCoefficientOfVariation
	}
	return &Tracker{
		state:               map[string]*poolStability{},
		window:              window,
		maxCoefficientOfVar: maxCoVar,
	}
}

// Observe records the pool's current formula target at now. It prunes
// the sample ring to the trailing window, recomputes the coefficient of
// variation, and advances or resets the stable-run start: the run begins
// the first reconcile the target is within the band and resets whenever
// a swing pushes the trailing-window coefficient of variation past the
// bound. A non-positive target (no formula output yet) clears the pool's
// state so the run restarts once a real target reappears.
func (t *Tracker) Observe(pool string, target int, now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if target <= 0 {
		delete(t.state, pool)
		return
	}
	st := t.state[pool]
	if st == nil {
		st = &poolStability{}
		t.state[pool] = st
	}
	cutoff := now.Add(-t.window)
	kept := st.samples[:0:0]
	for _, s := range st.samples {
		if !s.at.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	st.samples = append(kept, sample{at: now, target: target})
	if coefficientOfVariation(st.samples) < t.maxCoefficientOfVar {
		if st.stableSince.IsZero() {
			st.stableSince = now
		}
	} else {
		st.stableSince = time.Time{}
	}
}

// Stable reports whether the pool's formula target has held within the
// coefficient-of-variation bound continuously for at least the stability
// window.
//
// spec: §17.8.2 step 4 — "stable (variance < 20% ... over a 1-hour
// rolling window) for at least 2 hours".
func (t *Tracker) Stable(pool string, now time.Time) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state[pool]
	if st == nil || st.stableSince.IsZero() {
		return false
	}
	return now.Sub(st.stableSince) >= t.window
}

// coefficientOfVariation returns the stddev / mean of the sample
// targets. A single sample is treated as perfectly stable (0); a
// non-positive mean is treated as unstable (a large value) so the caller
// never converges on a degenerate series.
func coefficientOfVariation(samples []sample) float64 {
	if len(samples) < 2 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s.target)
	}
	mean := sum / float64(len(samples))
	if mean <= 0 {
		return math.Inf(1)
	}
	var variance float64
	for _, s := range samples {
		d := float64(s.target) - mean
		variance += d * d
	}
	variance /= float64(len(samples))
	return math.Sqrt(variance) / mean
}

// Forget drops the pool's stability state. The controller calls it for
// pools removed from the source so a later re-creation restarts the
// stability window from scratch.
func (t *Tracker) Forget(pool string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.state, pool)
	t.mu.Unlock()
}

// ForgetNotIn drops the stability state for every pool not present in
// desired, mirroring the controller's per-reconcile series cleanup.
func (t *Tracker) ForgetNotIn(desired map[string]struct{}) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for pool := range t.state {
		if _, kept := desired[pool]; !kept {
			delete(t.state, pool)
		}
	}
}
