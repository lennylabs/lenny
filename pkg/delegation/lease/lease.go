// SPDX-License-Identifier: MIT

// Package lease implements the §8.2 LeaseSlice arithmetic and the
// §8.2 "Effective maxDepth resolution (normative)" precedence chain.
//
// The gateway invokes ResolveMaxDepth at lease issuance time to bound
// every delegation chain regardless of cycle-detection mode, and
// invokes ValidateChildSlice on every lenny/delegate_task call to
// reject child slices that exceed the parent's remaining budget.
//
// The package is pure: no Redis, no Postgres. Redis-backed atomic
// reservations (the budget_reserve.lua script) live in later phases
// that import this package.
package lease

import (
	"errors"
	"fmt"
)

// LeaseSlice is the §8.2 lease-slice subset. Zero values mean "no
// limit set at this scope" — the gateway falls through to the parent
// or policy defaults.
type LeaseSlice struct {
	// MaxTokenBudget is the LLM token cap for the entire child
	// subtree. The gateway atomically reserves this from the parent's
	// remaining MaxTokenBudget at delegation time.
	MaxTokenBudget int64

	// MaxChildrenTotal caps the total descendants the child may
	// spawn. A child slice cannot exceed parent.MaxChildrenTotal -
	// already_spawned.
	MaxChildrenTotal int

	// MaxTreeSize caps this branch's contribution to the tree-wide
	// pod cap. Mirrors MaxChildrenTotal semantics for the pod
	// accounting axis.
	MaxTreeSize int

	// MaxParallelChildren caps concurrent in-flight children of the
	// child. Bounded above by the parent's MaxParallelChildren.
	MaxParallelChildren int

	// PerChildMaxAge is the wall-clock seconds budget per descendant.
	// Bounded above by the parent's PerChildMaxAge.
	PerChildMaxAge int
}

// ValidateChildSlice reports whether child can be admitted given the
// parent's remaining budget. The remainingTokenBudget,
// remainingChildren, and remainingTreeSize parameters are the
// parent's already-debited counters; for a freshly issued parent
// lease they equal the parent's slice values.
//
// Returns nil on admission. Returns *BudgetExceededError when any
// axis exceeds the parent's remaining capacity. Returns nil when
// the corresponding parent axis is zero (no limit set at the parent).
//
// The function does NOT mutate any counters; the gateway is
// responsible for the Redis-backed atomic reservation that records
// the debit.
func ValidateChildSlice(parent, child LeaseSlice, remainingTokenBudget int64, remainingChildren, remainingTreeSize int) error {
	violations := []string{}

	if parent.MaxTokenBudget > 0 && child.MaxTokenBudget > 0 {
		if child.MaxTokenBudget > remainingTokenBudget {
			violations = append(violations, fmt.Sprintf("maxTokenBudget %d exceeds parent remaining %d", child.MaxTokenBudget, remainingTokenBudget))
		}
	}
	if parent.MaxChildrenTotal > 0 && child.MaxChildrenTotal > 0 {
		if child.MaxChildrenTotal > remainingChildren {
			violations = append(violations, fmt.Sprintf("maxChildrenTotal %d exceeds parent remaining %d", child.MaxChildrenTotal, remainingChildren))
		}
	}
	if parent.MaxTreeSize > 0 && child.MaxTreeSize > 0 {
		if child.MaxTreeSize > remainingTreeSize {
			violations = append(violations, fmt.Sprintf("maxTreeSize %d exceeds parent remaining %d", child.MaxTreeSize, remainingTreeSize))
		}
	}
	if parent.MaxParallelChildren > 0 && child.MaxParallelChildren > 0 {
		// MaxParallelChildren is not a debited counter — every child
		// inherits a ceiling at most equal to the parent's
		// MaxParallelChildren.
		if child.MaxParallelChildren > parent.MaxParallelChildren {
			violations = append(violations, fmt.Sprintf("maxParallelChildren %d exceeds parent %d", child.MaxParallelChildren, parent.MaxParallelChildren))
		}
	}
	if parent.PerChildMaxAge > 0 && child.PerChildMaxAge > 0 {
		if child.PerChildMaxAge > parent.PerChildMaxAge {
			violations = append(violations, fmt.Sprintf("perChildMaxAge %d exceeds parent %d", child.PerChildMaxAge, parent.PerChildMaxAge))
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return &BudgetExceededError{Violations: violations}
}

// BudgetExceededError is returned by ValidateChildSlice when a child
// slice exceeds the parent's remaining budget. The gateway surfaces
// this as `BUDGET_EXHAUSTED` per §8.2.
type BudgetExceededError struct {
	Violations []string
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("delegation lease: budget exceeded: %v", e.Violations)
}

// MaxDepthInputs carries the precedence chain inputs for
// ResolveMaxDepth. Zero values mean "not set at this layer" and the
// resolver continues to the next layer.
type MaxDepthInputs struct {
	// ExplicitClient is the explicit delegationLease.maxDepth supplied
	// by the client at session creation, or the value propagated
	// verbatim from the parent's lease for children.
	ExplicitClient int

	// PresetEntry is the maxDepth of the resolved delegationPresets
	// entry referenced by the lease.
	PresetEntry int

	// RuntimeDefault is the delegationLease.maxDepth field of the
	// effective Runtime's defaultPoolConfig / runtime-level default
	// lease.
	RuntimeDefault int

	// PolicyCeiling is the maxDepth declared on the effective
	// DelegationPolicy — a policy-level ceiling that cannot be
	// widened by downstream overrides.
	PolicyCeiling int

	// HelmFallback is the Helm value gateway.delegation.defaultMaxDepth
	// (default 10). This MUST be > 0 because the spec says "Every
	// effective delegation lease MUST carry a positive integer
	// maxDepth".
	HelmFallback int
}

// ErrUnresolvedMaxDepth is returned by ResolveMaxDepth when every
// layer is zero — including the Helm fallback. This is a
// configuration bug; the spec says the Helm fallback defaults to 10
// and the chart enforces > 0.
var ErrUnresolvedMaxDepth = errors.New("delegation lease: no positive maxDepth found in any layer of the §8.2.bis precedence chain")

// ResolveMaxDepth applies the §8.2.bis precedence chain in order:
//
//  1. ExplicitClient
//  2. PresetEntry
//  3. RuntimeDefault
//  4. PolicyCeiling
//  5. HelmFallback
//
// Returns the first positive value. Returns ErrUnresolvedMaxDepth
// when every layer is zero.
//
// The §8.2.bis policy-ceiling note says "a policy-level ceiling that
// cannot be widened by downstream overrides." This function applies
// the precedence rule (first positive wins); the policy-ceiling
// invariant is enforced separately by EnforcePolicyCeiling below.
func ResolveMaxDepth(in MaxDepthInputs) (int, error) {
	for _, v := range []int{in.ExplicitClient, in.PresetEntry, in.RuntimeDefault, in.PolicyCeiling, in.HelmFallback} {
		if v > 0 {
			return v, nil
		}
	}
	return 0, ErrUnresolvedMaxDepth
}

// EnforcePolicyCeiling implements the §8.2.bis rule that the policy
// ceiling cannot be widened by downstream overrides. Given the
// resolved maxDepth and the policy ceiling, this returns
// min(resolved, policyCeiling) when policyCeiling > 0, or resolved
// unchanged when policyCeiling is unset.
func EnforcePolicyCeiling(resolved, policyCeiling int) int {
	if policyCeiling <= 0 {
		return resolved
	}
	if resolved < policyCeiling {
		return resolved
	}
	return policyCeiling
}

// CheckDepth reports whether currentDepth + 1 would exceed
// maxDepth. Returns nil when admissible and *DepthExceededError
// otherwise.
func CheckDepth(currentDepth, maxDepth int) error {
	if maxDepth <= 0 {
		return ErrUnresolvedMaxDepth
	}
	if currentDepth+1 > maxDepth {
		return &DepthExceededError{Current: currentDepth, Max: maxDepth}
	}
	return nil
}

// DepthExceededError is the typed error CheckDepth returns when the
// next hop would exceed maxDepth. The gateway maps this to
// `DELEGATION_DEPTH_EXCEEDED` per §15.1.
type DepthExceededError struct {
	Current int
	Max     int
}

func (e *DepthExceededError) Error() string {
	return fmt.Sprintf("delegation lease: depth %d + 1 exceeds maxDepth %d", e.Current, e.Max)
}
