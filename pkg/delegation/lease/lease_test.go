// SPDX-License-Identifier: MIT

package lease

import (
	"errors"
	"testing"
)

func TestValidateChildSliceWithinBudget(t *testing.T) {
	parent := LeaseSlice{
		MaxTokenBudget:      100_000,
		MaxChildrenTotal:    10,
		MaxTreeSize:         50,
		MaxParallelChildren: 5,
		PerChildMaxAge:      600,
	}
	child := LeaseSlice{
		MaxTokenBudget:      50_000,
		MaxChildrenTotal:    5,
		MaxTreeSize:         20,
		MaxParallelChildren: 3,
		PerChildMaxAge:      300,
	}
	err := ValidateChildSlice(parent, child, 75_000, 8, 40)
	if err != nil {
		t.Errorf("within-budget child slice should validate, got %v", err)
	}
}

func TestValidateChildSliceRejectsTokenBudgetOverrun(t *testing.T) {
	parent := LeaseSlice{MaxTokenBudget: 100_000}
	child := LeaseSlice{MaxTokenBudget: 50_000}
	err := ValidateChildSlice(parent, child, 30_000, 0, 0)
	var be *BudgetExceededError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BudgetExceededError, got %v", err)
	}
}

func TestValidateChildSliceRejectsParallelOverrun(t *testing.T) {
	parent := LeaseSlice{MaxParallelChildren: 5}
	child := LeaseSlice{MaxParallelChildren: 10}
	err := ValidateChildSlice(parent, child, 0, 0, 0)
	if err == nil {
		t.Errorf("maxParallelChildren over parent must be rejected")
	}
}

func TestValidateChildSlicePerChildAgeBounded(t *testing.T) {
	parent := LeaseSlice{PerChildMaxAge: 300}
	child := LeaseSlice{PerChildMaxAge: 600}
	err := ValidateChildSlice(parent, child, 0, 0, 0)
	if err == nil {
		t.Errorf("perChildMaxAge over parent must be rejected")
	}
}

// Parent slices that don't constrain (zero) admit any child value on
// that axis — the constraint comes from higher policy layers.
func TestValidateChildSliceUnconstrainedParentAdmits(t *testing.T) {
	parent := LeaseSlice{} // every axis is zero
	child := LeaseSlice{
		MaxTokenBudget:      1_000_000,
		MaxChildrenTotal:    1_000,
		MaxParallelChildren: 100,
		PerChildMaxAge:      86_400,
	}
	if err := ValidateChildSlice(parent, child, 0, 0, 0); err != nil {
		t.Errorf("unconstrained parent must admit, got %v", err)
	}
}

func TestResolveMaxDepthApplies82BisPrecedence(t *testing.T) {
	cases := []struct {
		name string
		in   MaxDepthInputs
		want int
	}{
		{"explicit wins", MaxDepthInputs{ExplicitClient: 3, PresetEntry: 5, RuntimeDefault: 7, PolicyCeiling: 9, HelmFallback: 10}, 3},
		{"preset wins when no explicit", MaxDepthInputs{PresetEntry: 5, RuntimeDefault: 7, HelmFallback: 10}, 5},
		{"runtime wins next", MaxDepthInputs{RuntimeDefault: 7, PolicyCeiling: 9, HelmFallback: 10}, 7},
		{"policy ceiling next", MaxDepthInputs{PolicyCeiling: 9, HelmFallback: 10}, 9},
		{"helm fallback last", MaxDepthInputs{HelmFallback: 10}, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveMaxDepth(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("want %d, got %d", c.want, got)
			}
		})
	}
}

func TestResolveMaxDepthErrorsWhenAllZero(t *testing.T) {
	_, err := ResolveMaxDepth(MaxDepthInputs{})
	if !errors.Is(err, ErrUnresolvedMaxDepth) {
		t.Errorf("expected ErrUnresolvedMaxDepth, got %v", err)
	}
}

// The §8.2.bis policy-ceiling rule: a policy ceiling cannot be widened
// by downstream overrides. ExplicitClient wins the precedence chain,
// but EnforcePolicyCeiling caps the result at the policy ceiling.
func TestEnforcePolicyCeilingCapsResolved(t *testing.T) {
	if got := EnforcePolicyCeiling(20, 10); got != 10 {
		t.Errorf("policy ceiling must cap resolved: want 10, got %d", got)
	}
	if got := EnforcePolicyCeiling(5, 10); got != 5 {
		t.Errorf("ceiling not yet reached: want 5, got %d", got)
	}
	if got := EnforcePolicyCeiling(20, 0); got != 20 {
		t.Errorf("zero ceiling means unset: want 20, got %d", got)
	}
}

func TestCheckDepthAdmitsWithinLimit(t *testing.T) {
	if err := CheckDepth(0, 10); err != nil {
		t.Errorf("depth 0 within max 10 should admit, got %v", err)
	}
	if err := CheckDepth(9, 10); err != nil {
		t.Errorf("depth 9 within max 10 should admit (next hop = 10), got %v", err)
	}
}

func TestCheckDepthRejectsAtCeiling(t *testing.T) {
	err := CheckDepth(10, 10)
	if err == nil {
		t.Errorf("depth 10 with max 10 should reject (next hop = 11)")
	}
	var de *DepthExceededError
	if !errors.As(err, &de) {
		t.Errorf("expected *DepthExceededError, got %T", err)
	}
}

func TestCheckDepthRejectsZeroMax(t *testing.T) {
	if err := CheckDepth(0, 0); !errors.Is(err, ErrUnresolvedMaxDepth) {
		t.Errorf("zero maxDepth must error as unresolved, got %v", err)
	}
}
