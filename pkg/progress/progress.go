// SPDX-License-Identifier: MIT

// Package progress computes the §25.2 long-running-operation progress
// envelope values: the percent-complete under each operation shape,
// the linear-extrapolation remaining-time estimate, the historical-p50
// baseline, and the stalled-operation signal. The package is pure — no
// I/O — so the backup, restore, upgrade, and drift-reconciliation
// status endpoints share one set of computations.
package progress

import (
	"sort"
	"time"
)

// PercentSteps returns the §25.2 percent-complete for a discrete-step
// operation: completedSteps / totalSteps, scaled to 0-100. A total of
// zero or less has no basis and returns 0.
func PercentSteps(completed, total int) float64 {
	if total <= 0 {
		return 0
	}
	return clampPercent(float64(completed) / float64(total) * 100)
}

// PercentSize returns the §25.2 percent-complete for a size-based
// operation (a backup dump): bytesWritten / bytesEstimated, scaled to
// 0-100.
func PercentSize(written, estimated int64) float64 {
	if estimated <= 0 {
		return 0
	}
	return clampPercent(float64(written) / float64(estimated) * 100)
}

// PercentRate returns the §25.2 percent-complete for a rate-based
// operation (a webhook backlog drain): 1 - remaining / peak, scaled to
// 0-100.
func PercentRate(remaining, peak int64) float64 {
	if peak <= 0 {
		return 0
	}
	return clampPercent((1 - float64(remaining)/float64(peak)) * 100)
}

// LinearETA estimates the remaining time of an operation by linear
// extrapolation from its current rate (§25.2 etaMethod
// "linear_extrapolation"): the rate is done/elapsed and the estimate
// is the remaining work divided by that rate. ok is false when no
// progress has been made yet, so no rate exists.
func LinearETA(done, total int64, elapsed time.Duration) (eta time.Duration, ok bool) {
	if done <= 0 || elapsed <= 0 {
		return 0, false
	}
	if done >= total {
		return 0, true
	}
	return time.Duration(int64(elapsed) * (total - done) / done), true
}

// P50 returns the median of the durations — the §25.2
// "historical_p50" baseline computed from an operation kind's prior
// completions. An empty sample returns zero.
func P50(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Stalled reports the §25.2 stalled signal for an operation whose
// progress last advanced at lastProgressAt. The operation is stalled
// once the time since then exceeds the operation kind's expected
// inter-step cadence; the returned duration is how far past the
// cadence it is. An operation advancing within cadence is not stalled.
func Stalled(lastProgressAt, now time.Time, cadence time.Duration) (stalledFor time.Duration, stalled bool) {
	idle := now.Sub(lastProgressAt)
	if idle <= cadence {
		return 0, false
	}
	return idle - cadence, true
}

func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
