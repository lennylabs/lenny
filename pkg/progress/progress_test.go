// SPDX-License-Identifier: MIT

package progress_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/progress"
)

func TestPercentSteps(t *testing.T) {
	if got := progress.PercentSteps(5, 10); got != 50 {
		t.Errorf("PercentSteps(5,10) = %v, want 50", got)
	}
	if got := progress.PercentSteps(0, 0); got != 0 {
		t.Errorf("PercentSteps(0,0) = %v, want 0 (no basis)", got)
	}
	if got := progress.PercentSteps(12, 10); got != 100 {
		t.Errorf("PercentSteps(12,10) = %v, want 100 (clamped)", got)
	}
}

func TestPercentSize(t *testing.T) {
	if got := progress.PercentSize(250, 1000); got != 25 {
		t.Errorf("PercentSize(250,1000) = %v, want 25", got)
	}
	if got := progress.PercentSize(10, 0); got != 0 {
		t.Errorf("PercentSize with no estimate = %v, want 0", got)
	}
}

func TestPercentRate(t *testing.T) {
	// 200 of a 1000-item peak backlog remain → 80% drained.
	if got := progress.PercentRate(200, 1000); got != 80 {
		t.Errorf("PercentRate(200,1000) = %v, want 80", got)
	}
	if got := progress.PercentRate(0, 1000); got != 100 {
		t.Errorf("PercentRate(0,1000) = %v, want 100", got)
	}
}

func TestLinearETA(t *testing.T) {
	// 25 of 100 done in 10s → 2.5/s → 75 remaining → 30s.
	eta, ok := progress.LinearETA(25, 100, 10*time.Second)
	if !ok || eta != 30*time.Second {
		t.Errorf("LinearETA(25,100,10s) = %v, %v; want 30s, true", eta, ok)
	}
	if _, ok := progress.LinearETA(0, 100, 10*time.Second); ok {
		t.Error("LinearETA with no progress reported ok, want false (no rate yet)")
	}
	if eta, ok := progress.LinearETA(100, 100, 10*time.Second); !ok || eta != 0 {
		t.Errorf("LinearETA of a finished operation = %v, %v; want 0, true", eta, ok)
	}
}

func TestP50(t *testing.T) {
	odd := []time.Duration{30 * time.Second, 10 * time.Second, 20 * time.Second}
	if got := progress.P50(odd); got != 20*time.Second {
		t.Errorf("P50(odd) = %v, want 20s", got)
	}
	even := []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second, 40 * time.Second}
	if got := progress.P50(even); got != 25*time.Second {
		t.Errorf("P50(even) = %v, want 25s", got)
	}
	if got := progress.P50(nil); got != 0 {
		t.Errorf("P50(empty) = %v, want 0", got)
	}
}

func TestStalled(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	cadence := 2 * time.Minute

	// Progress 1 minute ago, within the 2-minute cadence — not stalled.
	if _, stalled := progress.Stalled(now.Add(-1*time.Minute), now, cadence); stalled {
		t.Error("an operation within cadence reported stalled")
	}
	// Progress 5 minutes ago — stalled, 3 minutes past the cadence.
	stalledFor, stalled := progress.Stalled(now.Add(-5*time.Minute), now, cadence)
	if !stalled || stalledFor != 3*time.Minute {
		t.Errorf("Stalled after 5min idle = %v, %v; want 3m, true", stalledFor, stalled)
	}
}
