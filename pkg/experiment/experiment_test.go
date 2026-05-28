// SPDX-License-Identifier: MIT

package experiment

import (
	"errors"
	"fmt"
	"testing"
)

func TestAllStatusesIsExhaustive(t *testing.T) {
	if got := len(AllStatuses()); got != 3 {
		t.Errorf("AllStatuses() returned %d, want 3 per §10.7", got)
	}
}

func TestStatusIsRoutable(t *testing.T) {
	if !StatusActive.IsRoutable() {
		t.Errorf("active must be routable")
	}
	for _, s := range []Status{StatusPaused, StatusConcluded} {
		if s.IsRoutable() {
			t.Errorf("%q must not be routable", s)
		}
	}
}

func TestStatusCanTransitionTo(t *testing.T) {
	valid := []struct{ from, to Status }{
		{StatusActive, StatusPaused},
		{StatusActive, StatusConcluded},
		{StatusPaused, StatusActive},
		{StatusPaused, StatusConcluded},
	}
	for _, c := range valid {
		if !c.from.CanTransitionTo(c.to) {
			t.Errorf("%q → %q should be a valid §10.7 transition", c.from, c.to)
		}
	}
	invalid := []struct{ from, to Status }{
		{StatusConcluded, StatusActive}, // concluded is immutable
		{StatusConcluded, StatusPaused}, // concluded is immutable
		{StatusActive, StatusActive},    // not a transition
		{StatusPaused, StatusPaused},    // not a transition
	}
	for _, c := range invalid {
		if c.from.CanTransitionTo(c.to) {
			t.Errorf("%q → %q must not be a valid transition", c.from, c.to)
		}
	}
}

// routeExp builds a single-variant Definition for the Route tests. A
// weight near 1 routes essentially every key to the variant; a weight
// near 0 routes essentially every key to control.
func routeExp(id string, weight float64, status Status, mode TargetingMode) Definition {
	return Definition{
		ID: id, Status: status, BaseRuntime: "r",
		Variants:      []Variant{{ID: id + "-v", Weight: weight}},
		TargetingMode: mode, Sticky: StickyUser, Propagation: PropagationInherit,
	}
}

func TestRouteFirstMatchWins(t *testing.T) {
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	b := routeExp("exp-b", 0.9999, StatusActive, TargetingPercentage)
	got := Route([]Definition{a, b}, "alice", "sess-1")
	if got.ExperimentID != "exp-a" || got.VariantID != "exp-a-v" {
		t.Errorf("Route = %+v, want the first experiment's enrollment", got)
	}
}

func TestRouteSkipsControlAssignments(t *testing.T) {
	// exp-a routes virtually every key to control; exp-b routes to its variant.
	a := routeExp("exp-a", 0.0001, StatusActive, TargetingPercentage)
	b := routeExp("exp-b", 0.9999, StatusActive, TargetingPercentage)
	got := Route([]Definition{a, b}, "alice", "sess-1")
	if got.ExperimentID != "exp-b" {
		t.Errorf("Route = %+v, want exp-b (a control assignment is skipped)", got)
	}
}

func TestRouteNoMatchReturnsZero(t *testing.T) {
	a := routeExp("exp-a", 0.0001, StatusActive, TargetingPercentage)
	if got := Route([]Definition{a}, "alice", "sess-1"); got != (Assignment{}) {
		t.Errorf("Route = %+v, want a zero Assignment when every experiment yields control", got)
	}
}

func TestRouteSkipsPausedAndExternal(t *testing.T) {
	paused := routeExp("paused", 0.9999, StatusPaused, TargetingPercentage)
	external := routeExp("external", 0.9999, StatusActive, TargetingExternal)
	if got := Route([]Definition{paused, external}, "alice", "sess-1"); got != (Assignment{}) {
		t.Errorf("Route = %+v, want zero — paused and external-mode experiments are not routed", got)
	}
}

func TestSkippedAfterListsLaterRoutableExperiments(t *testing.T) {
	// The session enrolls in exp-a; exp-b and exp-c are routable
	// percentage-mode experiments created after it.
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	b := routeExp("exp-b", 0.9999, StatusActive, TargetingPercentage)
	c := routeExp("exp-c", 0.9999, StatusActive, TargetingPercentage)
	got := SkippedAfter([]Definition{a, b, c}, "exp-a", false)
	if len(got) != 2 || got[0] != "exp-b" || got[1] != "exp-c" {
		t.Errorf("SkippedAfter = %v, want [exp-b exp-c]", got)
	}
}

func TestSkippedAfterExcludesEarlierExperiments(t *testing.T) {
	// exp-a is created before the enrolled exp-b. The first-match rule
	// did not skip exp-a — it was evaluated and lost — so it is not in
	// the audit set.
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	b := routeExp("exp-b", 0.9999, StatusActive, TargetingPercentage)
	c := routeExp("exp-c", 0.9999, StatusActive, TargetingPercentage)
	got := SkippedAfter([]Definition{a, b, c}, "exp-b", false)
	if len(got) != 1 || got[0] != "exp-c" {
		t.Errorf("SkippedAfter = %v, want [exp-c] — exp-a precedes the enrolled experiment", got)
	}
}

func TestSkippedAfterExcludesNonRoutable(t *testing.T) {
	// Paused and concluded experiments are never routed, so the
	// first-match rule did not skip them. With externalEvaluated false
	// the external experiment is excluded too — RouteMixed skipped it
	// regardless of first-match.
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	paused := routeExp("paused", 0.9999, StatusPaused, TargetingPercentage)
	external := routeExp("external", 0.9999, StatusActive, TargetingExternal)
	concluded := routeExp("concluded", 0.9999, StatusConcluded, TargetingPercentage)
	live := routeExp("live", 0.9999, StatusActive, TargetingPercentage)
	got := SkippedAfter([]Definition{a, paused, external, concluded, live}, "exp-a", false)
	if len(got) != 1 || got[0] != "live" {
		t.Errorf("SkippedAfter = %v, want [live] only", got)
	}
}

func TestSkippedAfterIncludesExternalWhenEvaluated(t *testing.T) {
	// When RouteMixed ran with an external evaluator, an external-mode
	// experiment after the enrolled one was a live candidate the
	// first-match rule skipped — it belongs in the audit set.
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	external := routeExp("external", 0.9999, StatusActive, TargetingExternal)
	pct := routeExp("pct", 0.9999, StatusActive, TargetingPercentage)
	got := SkippedAfter([]Definition{a, external, pct}, "exp-a", true)
	if len(got) != 2 || got[0] != "external" || got[1] != "pct" {
		t.Errorf("SkippedAfter(externalEvaluated) = %v, want [external pct]", got)
	}
}

func TestSkippedAfterLastCandidateIsEmpty(t *testing.T) {
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	b := routeExp("exp-b", 0.9999, StatusActive, TargetingPercentage)
	if got := SkippedAfter([]Definition{a, b}, "exp-b", false); got != nil {
		t.Errorf("SkippedAfter = %v, want nil — the enrolled experiment is the last candidate", got)
	}
}

func TestSkippedAfterEmptyOrUnknownEnrollment(t *testing.T) {
	a := routeExp("exp-a", 0.9999, StatusActive, TargetingPercentage)
	if got := SkippedAfter([]Definition{a}, "", false); got != nil {
		t.Errorf("SkippedAfter(\"\") = %v, want nil", got)
	}
	if got := SkippedAfter([]Definition{a}, "absent", false); got != nil {
		t.Errorf("SkippedAfter(unknown) = %v, want nil", got)
	}
}

func TestRouteStickyUserUsesUserID(t *testing.T) {
	e := routeExp("exp", 0.9999, StatusActive, TargetingPercentage) // sticky: user
	// An anonymous session (empty userID) yields control under sticky:user.
	if got := Route([]Definition{e}, "", "sess-1"); got != (Assignment{}) {
		t.Errorf("Route with no userID = %+v, want zero (anonymous → control)", got)
	}
	// A named user routes to the variant.
	if got := Route([]Definition{e}, "alice", "sess-1"); got.ExperimentID != "exp" {
		t.Errorf("Route with a userID = %+v, want enrollment", got)
	}
}

func TestAllTargetingModesIsExhaustive(t *testing.T) {
	if got := len(AllTargetingModes()); got != 2 {
		t.Errorf("AllTargetingModes() returned %d, want 2 per §10.7", got)
	}
}

func TestAllStickyValuesIsExhaustive(t *testing.T) {
	if got := len(AllStickyValues()); got != 3 {
		t.Errorf("AllStickyValues() returned %d, want 3 per §10.7", got)
	}
}

func TestPropagateContextInherit(t *testing.T) {
	got := PropagateContext("exp-1", "treatment", PropagationInherit)
	if !got.UseParentContext || got.ExperimentID != "exp-1" || got.VariantID != "treatment" {
		t.Errorf("PropagateContext(inherit) = %+v, want the parent's enrollment verbatim", got)
	}
}

func TestPropagateContextControl(t *testing.T) {
	got := PropagateContext("exp-1", "treatment", PropagationControl)
	if !got.UseParentContext || got.ExperimentID != "exp-1" || got.VariantID != ControlVariantID {
		t.Errorf("PropagateContext(control) = %+v, want exp-1 forced onto control", got)
	}
}

func TestPropagateContextIndependent(t *testing.T) {
	got := PropagateContext("exp-1", "treatment", PropagationIndependent)
	if got.UseParentContext {
		t.Errorf("PropagateContext(independent) = %+v, want UseParentContext false — the child routes afresh", got)
	}
}

func TestAllPropagationsIsExhaustive(t *testing.T) {
	if got := len(AllPropagations()); got != 3 {
		t.Errorf("AllPropagations() returned %d, want 3 per §10.7", got)
	}
}

func TestDefinitionValidateHappyPath(t *testing.T) {
	d := Definition{
		ID:            "claude-v2-rollout",
		Status:        StatusActive,
		BaseRuntime:   "claude-worker",
		TargetingMode: TargetingPercentage,
		Sticky:        StickyUser,
		Propagation:   PropagationInherit,
		Variants: []Variant{
			{ID: "treatment", Weight: 0.10},
		},
	}
	if err := d.Validate(); err != nil {
		t.Errorf("well-formed definition should validate, got %v", err)
	}
}

func TestDefinitionRejectsReservedControlID(t *testing.T) {
	d := Definition{
		ID:            "x",
		Status:        StatusActive,
		BaseRuntime:   "claude-worker",
		TargetingMode: TargetingPercentage,
		Sticky:        StickyUser,
		Propagation:   PropagationInherit,
		Variants: []Variant{
			{ID: ControlVariantID, Weight: 0.10},
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatalf("variant with id %q must be rejected per §10.7", ControlVariantID)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestDefinitionRejectsBadWeights(t *testing.T) {
	// spec: §10.7 line 694 / line 743 — each weight in [0.0, 1.0); a
	// 1.0 weight always violates the cross-Σ < 1.0 rule.
	cases := []struct {
		name    string
		weights []float64
	}{
		{"weight one", []float64{1.0}},
		{"weight over one", []float64{1.5}},
		{"negative weight", []float64{-0.1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			variants := []Variant{}
			for i, w := range c.weights {
				variants = append(variants, Variant{ID: fmt.Sprintf("v%d", i), Weight: w})
			}
			d := Definition{
				ID: "x", Status: StatusActive, BaseRuntime: "r",
				TargetingMode: TargetingPercentage, Sticky: StickyUser, Propagation: PropagationInherit,
				Variants: variants,
			}
			if err := d.Validate(); err == nil {
				t.Errorf("bad weights %v should be rejected", c.weights)
			}
		})
	}
}

// spec: §10.7 line 694 — weight: 0.0 is operationally a no-op (no
// traffic to the variant) and must be admitted so deployers can stage a
// variant before turning on traffic. F-10.7.16.
func TestDefinitionAcceptsZeroWeightVariant(t *testing.T) {
	d := Definition{
		ID: "x", Status: StatusActive, BaseRuntime: "r",
		TargetingMode: TargetingPercentage, Sticky: StickyUser, Propagation: PropagationInherit,
		Variants: []Variant{
			{ID: "staged", Weight: 0},
			{ID: "live", Weight: 0.1},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("staged variant with weight 0 should be admitted: %v", err)
	}
}

func TestDefinitionRejectsSumWeightsAtOrAboveOne(t *testing.T) {
	d := Definition{
		ID: "x", Status: StatusActive, BaseRuntime: "r",
		TargetingMode: TargetingPercentage, Sticky: StickyUser, Propagation: PropagationInherit,
		Variants: []Variant{
			{ID: "a", Weight: 0.5},
			{ID: "b", Weight: 0.5},
		},
	}
	if err := d.Validate(); err == nil {
		t.Errorf("Σ weights == 1.0 leaves no control group; must be rejected")
	}
}

func TestDefinitionRejectsDuplicateVariantID(t *testing.T) {
	d := Definition{
		ID: "x", Status: StatusActive, BaseRuntime: "r",
		TargetingMode: TargetingPercentage, Sticky: StickyUser, Propagation: PropagationInherit,
		Variants: []Variant{
			{ID: "treatment", Weight: 0.1},
			{ID: "treatment", Weight: 0.2},
		},
	}
	if err := d.Validate(); err == nil {
		t.Errorf("duplicate variant id must be rejected")
	}
}

// §10.7 anonymous handling: empty assignmentKey returns control.
func TestAssignVariantEmptyKeyReturnsControl(t *testing.T) {
	got := AssignVariant("", "exp", []Variant{{ID: "t", Weight: 0.99}})
	if got != ControlVariantID {
		t.Errorf("empty assignmentKey: want %q, got %q", ControlVariantID, got)
	}
}

// §10.7 determinism: identical inputs always yield the same variant.
func TestAssignVariantIsDeterministic(t *testing.T) {
	variants := []Variant{{ID: "treatment", Weight: 0.1}, {ID: "candidate", Weight: 0.2}}
	for _, key := range []string{"alice", "bob", "carol", "dave"} {
		if err := AssignVariantDeterministic(key, "exp-1", variants, 100); err != nil {
			t.Errorf("determinism failed for key %q: %v", key, err)
		}
	}
}

// §10.7 independence: same user, different experiment_id should not
// strictly land in the same variant. Distribution check: across many
// users, each variant gets approximately its weight share.
func TestAssignVariantApproximatesWeightDistribution(t *testing.T) {
	variants := []Variant{{ID: "treatment", Weight: 0.25}}
	const trials = 10_000
	counts := map[string]int{}
	for i := 0; i < trials; i++ {
		key := fmt.Sprintf("user-%d", i)
		v := AssignVariant(key, "exp-1", variants)
		counts[v]++
	}
	// Allow ±2% drift for the 10k-trial chi-square approximation.
	treatmentRatio := float64(counts["treatment"]) / float64(trials)
	if treatmentRatio < 0.23 || treatmentRatio > 0.27 {
		t.Errorf("treatment ratio %.3f outside expected 0.25 ± 0.02 over %d trials", treatmentRatio, trials)
	}
	controlRatio := float64(counts[ControlVariantID]) / float64(trials)
	if controlRatio < 0.73 || controlRatio > 0.77 {
		t.Errorf("control ratio %.3f outside expected 0.75 ± 0.02 over %d trials", controlRatio, trials)
	}
}

// §10.7 ordering sensitivity: the algorithm walks variants in
// definition order; the same user may land in a different variant if
// the order changes.
func TestAssignVariantHonoursDefinitionOrder(t *testing.T) {
	// Three-variant example from §10.7: A=0.10, B=0.20, C=0.15.
	// Bucket boundaries: [0, 0.10) → A; [0.10, 0.30) → B; [0.30, 0.45) → C; remainder → control.
	abc := []Variant{
		{ID: "A", Weight: 0.10},
		{ID: "B", Weight: 0.20},
		{ID: "C", Weight: 0.15},
	}
	// Same definition but with order C, B, A — boundaries differ.
	cba := []Variant{
		{ID: "C", Weight: 0.15},
		{ID: "B", Weight: 0.20},
		{ID: "A", Weight: 0.10},
	}
	// Find at least one user where the assignment differs between
	// the two orderings; if none differ, the algorithm is not honouring
	// definition order.
	differs := 0
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("user-%d", i)
		a := AssignVariant(key, "exp-1", abc)
		b := AssignVariant(key, "exp-1", cba)
		if a != b {
			differs++
		}
	}
	if differs == 0 {
		t.Errorf("reordering variants must change at least some users' assignments")
	}
}

// §10.7 independence-across-experiments: same user, different
// experiment IDs should produce different bucket positions.
func TestAssignVariantExperimentIDPartOfHashKey(t *testing.T) {
	variants := []Variant{{ID: "treatment", Weight: 0.5}}
	// At least one user should assign differently between two
	// experiment ids; otherwise experiment_id is not part of the hash
	// key.
	differs := 0
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("user-%d", i)
		a := AssignVariant(key, "exp-1", variants)
		b := AssignVariant(key, "exp-2", variants)
		if a != b {
			differs++
		}
	}
	if differs == 0 {
		t.Errorf("identical assignment across experiment ids — experiment_id not in HMAC key")
	}
}
