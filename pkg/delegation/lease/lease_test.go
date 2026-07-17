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

// spec: §8.4 lines 515-521 — the §8.4 closed enum admits "policy",
// "approval", "deny", and the empty string (which the gateway aliases
// to ApprovalModePolicy at evaluation time per the v1 default).
// F-8.4.1, F-8.4.2.
func TestValidateApprovalModeAcceptsClosedEnum_spec_8_4(t *testing.T) {
	cases := []ApprovalMode{"", ApprovalModePolicy, ApprovalModeApproval, ApprovalModeDeny}
	for _, m := range cases {
		if err := ValidateApprovalMode(m); err != nil {
			t.Errorf("ValidateApprovalMode(%q) = %v, want nil", m, err)
		}
	}
}

// spec: §8.4 — the closed-enum validator MUST reject any value outside
// the three documented modes so an operator authoring a typo
// (approvalMode: "Approval", "polic", "ALLOW") is told immediately
// rather than silently falling through to the default. F-8.4.2.
func TestValidateApprovalModeRejectsUnknownValue_spec_8_4(t *testing.T) {
	cases := []ApprovalMode{"Approval", "polic", "ALLOW", "auto", "Policy"}
	for _, m := range cases {
		err := ValidateApprovalMode(m)
		if err == nil {
			t.Errorf("ValidateApprovalMode(%q) = nil, want InvalidApprovalModeError", m)
			continue
		}
		var ime *InvalidApprovalModeError
		if !errors.As(err, &ime) {
			t.Errorf("ValidateApprovalMode(%q) returned %T, want *InvalidApprovalModeError", m, err)
		}
	}
}

// spec: §8.4 line 520 — `approval` is accepted at registration time
// but the gateway treats it identically to `policy` mode in v1.
// EffectiveApprovalMode encodes the alias so the §8.5 service can
// share the policy auto-approval path for both inputs. F-8.4.1.
func TestEffectiveApprovalModeAliasesApprovalToPolicy_spec_8_4(t *testing.T) {
	if got := EffectiveApprovalMode(ApprovalModeApproval); got != ApprovalModePolicy {
		t.Errorf("EffectiveApprovalMode(approval) = %q, want policy", got)
	}
	if got := EffectiveApprovalMode(""); got != ApprovalModePolicy {
		t.Errorf("EffectiveApprovalMode(\"\") = %q, want policy (default)", got)
	}
	if got := EffectiveApprovalMode(ApprovalModePolicy); got != ApprovalModePolicy {
		t.Errorf("EffectiveApprovalMode(policy) = %q, want policy", got)
	}
	if got := EffectiveApprovalMode(ApprovalModeDeny); got != ApprovalModeDeny {
		t.Errorf("EffectiveApprovalMode(deny) = %q, want deny (no alias)", got)
	}
}

// spec: §8.3 — the credentialPropagation closed enum admits "inherit",
// "independent", "deny", and the empty string (which resolves to the
// independent default when the field is omitted from a delegate_task
// call).
func TestValidateCredentialPropagationAcceptsClosedEnum_spec_8_3(t *testing.T) {
	cases := []CredentialPropagation{
		"",
		CredentialPropagationInherit,
		CredentialPropagationIndependent,
		CredentialPropagationDeny,
	}
	for _, m := range cases {
		if err := ValidateCredentialPropagation(m); err != nil {
			t.Errorf("ValidateCredentialPropagation(%q) = %v, want nil", m, err)
		}
	}
}

// spec: §8.3 — the closed-enum validator MUST reject any value outside
// the three documented modes so a lease authored with a typo
// (credentialPropagation: "Inherit", "share", "none") is rejected at
// ingress rather than silently falling through to the default.
func TestValidateCredentialPropagationRejectsUnknownValue_spec_8_3(t *testing.T) {
	cases := []CredentialPropagation{"Inherit", "share", "none", "Independent", "DENY"}
	for _, m := range cases {
		err := ValidateCredentialPropagation(m)
		if err == nil {
			t.Errorf("ValidateCredentialPropagation(%q) = nil, want InvalidCredentialPropagationError", m)
			continue
		}
		var ipe *InvalidCredentialPropagationError
		if !errors.As(err, &ipe) {
			t.Errorf("ValidateCredentialPropagation(%q) returned %T, want *InvalidCredentialPropagationError", m, err)
			continue
		}
		if ipe.Value != string(m) {
			t.Errorf("InvalidCredentialPropagationError.Value = %q, want %q", ipe.Value, m)
		}
	}
}

// spec: §8.3 line 470 — the provider-intersection primitive returns
// the a-ordered intersection, deduplicated, and preserves the order of
// the first list so a credential can be assigned deterministically
// from the parent pool.
func TestIntersectProvidersOrderStable_spec_8_3(t *testing.T) {
	cases := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{"non-empty intersection preserves a order", []string{"anthropic", "openai", "google"}, []string{"google", "anthropic"}, []string{"anthropic", "google"}},
		{"empty intersection", []string{"anthropic"}, []string{"openai"}, []string{}},
		{"a empty", nil, []string{"openai"}, []string{}},
		{"b empty", []string{"anthropic"}, nil, []string{}},
		{"deduplicates repeats in a", []string{"anthropic", "anthropic", "openai"}, []string{"anthropic", "openai"}, []string{"anthropic", "openai"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IntersectProviders(tc.a, tc.b)
			if len(got) != len(tc.want) {
				t.Fatalf("IntersectProviders(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("IntersectProviders(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
				}
			}
		})
	}
}
