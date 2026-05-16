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
	cases := []struct {
		name    string
		weights []float64
	}{
		{"zero weight", []float64{0}},
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
