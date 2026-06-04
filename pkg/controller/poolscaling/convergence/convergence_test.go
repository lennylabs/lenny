// SPDX-License-Identifier: MIT

package convergence

import (
	"testing"
	"time"
)

// spec: §17.8.2 step 4 — all four criteria must hold to converge.
func TestConverged_AllCriteriaMet_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: true,
		HoursOfData:       50,
		TargetStable:      true,
		WarmPoolLowRecent: false,
		FormulaTarget:     12,
		OverrideMinWarm:   10,
	}
	if !in.Converged() {
		t.Fatalf("expected convergence with all criteria met")
	}
	if in.Underprovisioned() {
		t.Fatalf("target 12 <= 3x override 10, must not be underprovisioned")
	}
}

// spec: §17.8.2 step 4 criterion 1 — fewer than 48h of data blocks
// convergence.
func TestConverged_BlockedByInsufficientData_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: true,
		HoursOfData:       47.9,
		TargetStable:      true,
		FormulaTarget:     12,
		OverrideMinWarm:   10,
	}
	if in.DataSufficient() {
		t.Fatalf("47.9h < 48h must be insufficient")
	}
	if in.Converged() {
		t.Fatalf("must not converge below the 48h window")
	}
}

// spec: §17.8.2 step 4 — Observed=false (no usable sample) never
// satisfies the data criterion even past 48h of wall-clock.
func TestConverged_BlockedByUnobservedDemand_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: false,
		HoursOfData:       100,
		TargetStable:      true,
		FormulaTarget:     12,
		OverrideMinWarm:   10,
	}
	if in.DataSufficient() || in.Converged() {
		t.Fatalf("unobserved demand must not converge")
	}
}

// spec: §17.8.2 step 4 criterion 2 — an unstable target blocks
// convergence.
func TestConverged_BlockedByInstability_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: true,
		HoursOfData:       72,
		TargetStable:      false,
		FormulaTarget:     12,
		OverrideMinWarm:   10,
	}
	if in.Converged() {
		t.Fatalf("unstable target must not converge")
	}
}

// spec: §17.8.2 step 4 criterion 3 — a recent WarmPoolLow blocks
// convergence.
func TestConverged_BlockedByWarmPoolLow_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: true,
		HoursOfData:       72,
		TargetStable:      true,
		WarmPoolLowRecent: true,
		FormulaTarget:     12,
		OverrideMinWarm:   10,
	}
	if in.Converged() {
		t.Fatalf("recent WarmPoolLow must not converge")
	}
}

// spec: §17.8.2 step 4 criterion 4 — a formula target above 3× the
// override flags underprovisioned and blocks convergence.
func TestConverged_BlockedByUnderprovisioned_spec_17_8_2(t *testing.T) {
	in := Inputs{
		HasObservedDemand: true,
		HoursOfData:       72,
		TargetStable:      true,
		WarmPoolLowRecent: false,
		FormulaTarget:     31, // > 3 * 10
		OverrideMinWarm:   10,
	}
	if !in.Underprovisioned() {
		t.Fatalf("31 > 3x10 must be underprovisioned")
	}
	if in.Converged() {
		t.Fatalf("underprovisioned override must not converge")
	}
}

// spec: §17.8.2 step 4 — exactly 3× the override is the boundary and is
// not underprovisioned.
func TestUnderprovisioned_Boundary_spec_17_8_2(t *testing.T) {
	at3x := Inputs{HasObservedDemand: true, FormulaTarget: 30, OverrideMinWarm: 10}
	if at3x.Underprovisioned() {
		t.Fatalf("target == 3x override is the boundary, not underprovisioned")
	}
	just := Inputs{HasObservedDemand: true, FormulaTarget: 31, OverrideMinWarm: 10}
	if !just.Underprovisioned() {
		t.Fatalf("target just above 3x override must be underprovisioned")
	}
}

// Underprovisioned is undefined without a positive override or without
// observed demand; both cases report false.
func TestUnderprovisioned_RequiresOverrideAndDemand(t *testing.T) {
	noOverride := Inputs{HasObservedDemand: true, FormulaTarget: 100, OverrideMinWarm: 0}
	if noOverride.Underprovisioned() {
		t.Fatalf("no override → not underprovisioned")
	}
	noDemand := Inputs{HasObservedDemand: false, FormulaTarget: 100, OverrideMinWarm: 1}
	if noDemand.Underprovisioned() {
		t.Fatalf("no demand → no formula target → not underprovisioned")
	}
}

// spec: §17.8.2 step 3 — estimatedConvergenceAt projects the 48h data
// gate from now and is zero once the window is already satisfied.
func TestEstimatedConvergenceAt_spec_17_8_2(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	in := Inputs{HoursOfData: 12}
	got := in.EstimatedConvergenceAt(now)
	want := now.Add(36 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("estimate = %v, want %v", got, want)
	}
	done := Inputs{HoursOfData: 48}
	if z := done.EstimatedConvergenceAt(now); !z.IsZero() {
		t.Fatalf("estimate at/after 48h must be zero, got %v", z)
	}
}

// spec: §17.8.2 step 4 — the stability tracker requires the samples to
// span the full window before reporting stable.
func TestTracker_NotStableBeforeWindowElapses_spec_17_8_2(t *testing.T) {
	tr := NewTrackerWithWindow(2*time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	tr.Observe("p", 10, base)
	tr.Observe("p", 10, base.Add(30*time.Minute))
	if tr.Stable("p", base.Add(30*time.Minute)) {
		t.Fatalf("30m of samples must not be stable for a 2h window")
	}
}

// spec: §17.8.2 step 4 — a target held flat across the full window is
// stable.
func TestTracker_StableFlatTarget_spec_17_8_2(t *testing.T) {
	tr := NewTrackerWithWindow(2*time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	for m := 0; m <= 130; m += 10 {
		tr.Observe("p", 20, base.Add(time.Duration(m)*time.Minute))
	}
	if !tr.Stable("p", base.Add(130*time.Minute)) {
		t.Fatalf("flat target over >2h must be stable")
	}
}

// spec: §17.8.2 step 4 — a target swinging beyond the 20% coefficient of
// variation is not stable even after the window elapses.
func TestTracker_UnstableHighVariance_spec_17_8_2(t *testing.T) {
	tr := NewTrackerWithWindow(2*time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	targets := []int{10, 30, 10, 30, 10, 30, 10, 30, 10, 30, 10, 30, 10, 30}
	for i, v := range targets {
		tr.Observe("p", v, base.Add(time.Duration(i*10)*time.Minute))
	}
	if tr.Stable("p", base.Add(time.Duration(len(targets)*10)*time.Minute)) {
		t.Fatalf("a target oscillating 10↔30 must exceed the 20%% CoV bound")
	}
}

// A non-positive target resets the window: the stability clock only runs
// while a real formula target exists.
func TestTracker_NonPositiveTargetResetsWindow(t *testing.T) {
	tr := NewTrackerWithWindow(2*time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	for m := 0; m <= 130; m += 10 {
		tr.Observe("p", 20, base.Add(time.Duration(m)*time.Minute))
	}
	tr.Observe("p", 0, base.Add(140*time.Minute)) // demand lost
	if tr.Stable("p", base.Add(150*time.Minute)) {
		t.Fatalf("a reset window must not be stable")
	}
}

// spec: §17.8.2 step 4 — "for at least 2 hours" restarts after any swing
// past the band, so a pool that stabilized, jumped, then re-stabilized
// must serve a fresh window before it counts as stable again.
func TestTracker_SwingRestartsTheWindow_spec_17_8_2(t *testing.T) {
	tr := NewTrackerWithWindow(time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	// Two hours flat → stable.
	for m := 0; m <= 120; m += 10 {
		tr.Observe("p", 10, base.Add(time.Duration(m)*time.Minute))
	}
	if !tr.Stable("p", base.Add(120*time.Minute)) {
		t.Fatalf("expected stable after 2h flat")
	}
	// A large jump at 130m breaks the band and restarts the run.
	tr.Observe("p", 100, base.Add(130*time.Minute))
	if tr.Stable("p", base.Add(130*time.Minute)) {
		t.Fatalf("a swing must reset the stability run")
	}
	// 30 more minutes flat at the new level is under the 1h window.
	for m := 140; m <= 160; m += 10 {
		tr.Observe("p", 100, base.Add(time.Duration(m)*time.Minute))
	}
	if tr.Stable("p", base.Add(160*time.Minute)) {
		t.Fatalf("only 30m since the swing, under the 1h window")
	}
}

// ForgetNotIn drops series for pools no longer desired.
func TestTracker_ForgetNotIn(t *testing.T) {
	tr := NewTrackerWithWindow(2*time.Hour, 0.20)
	base := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	for m := 0; m <= 130; m += 10 {
		tr.Observe("keep", 20, base.Add(time.Duration(m)*time.Minute))
		tr.Observe("drop", 20, base.Add(time.Duration(m)*time.Minute))
	}
	tr.ForgetNotIn(map[string]struct{}{"keep": {}})
	if tr.Stable("drop", base.Add(130*time.Minute)) {
		t.Fatalf("forgotten pool must have no series")
	}
	if !tr.Stable("keep", base.Add(130*time.Minute)) {
		t.Fatalf("retained pool must keep its series")
	}
}
