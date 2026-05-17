// SPDX-License-Identifier: MIT

// Package experiment encodes the §10.7 experiment primitives that are
// pure: the Status / TargetingMode / Sticky / Propagation enums, the
// HMAC-SHA256 bucketing algorithm for percentage-mode assignment, and
// the §10.7 "control" reserved-identifier rule.
//
// External-targeting (OpenFeature / OFREP), the PoolScalingController
// integration that maintains variant pool minWarm, the admin API
// surface, and the ExperimentRouter interceptor wiring all live in
// later phases that import this package.
package experiment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Status is the §10.7 experiment lifecycle state.
type Status string

const (
	StatusActive    Status = "active"
	StatusPaused    Status = "paused"
	StatusConcluded Status = "concluded"
)

// AllStatuses returns the closed §10.7 enum.
func AllStatuses() []Status { return []Status{StatusActive, StatusPaused, StatusConcluded} }

// IsValid reports whether s is one of the three §10.7 statuses.
func (s Status) IsValid() bool {
	for _, v := range AllStatuses() {
		if s == v {
			return true
		}
	}
	return false
}

// IsRoutable reports whether the §10.7 ExperimentRouter consults this
// experiment when picking a variant pool. Only `active` experiments
// participate.
func (s Status) IsRoutable() bool { return s == StatusActive }

// CanTransitionTo reports whether an experiment may move from status s
// to next per the §10.7 lifecycle: active and paused may move to each
// other or to concluded, and a concluded experiment is immutable.
func (s Status) CanTransitionTo(next Status) bool {
	switch s {
	case StatusActive:
		return next == StatusPaused || next == StatusConcluded
	case StatusPaused:
		return next == StatusActive || next == StatusConcluded
	default: // concluded — immutable
		return false
	}
}

// TargetingMode is the §10.7 targeting.mode enum.
type TargetingMode string

const (
	TargetingPercentage TargetingMode = "percentage"
	TargetingExternal   TargetingMode = "external"
)

// AllTargetingModes returns the closed §10.7 enum.
func AllTargetingModes() []TargetingMode {
	return []TargetingMode{TargetingPercentage, TargetingExternal}
}

// IsValid reports whether m is one of the §10.7 targeting modes.
func (m TargetingMode) IsValid() bool {
	return m == TargetingPercentage || m == TargetingExternal
}

// Sticky is the §10.7 targeting.sticky enum.
type Sticky string

const (
	StickyUser    Sticky = "user"
	StickySession Sticky = "session"
	StickyNone    Sticky = "none"
)

// AllStickyValues returns the closed §10.7 enum.
func AllStickyValues() []Sticky { return []Sticky{StickyUser, StickySession, StickyNone} }

// IsValid reports whether s is one of the §10.7 sticky modes.
func (s Sticky) IsValid() bool {
	for _, v := range AllStickyValues() {
		if s == v {
			return true
		}
	}
	return false
}

// Propagation is the §10.7 propagation.childSessions enum that
// controls how children of an experiment-enrolled session are routed.
type Propagation string

const (
	// PropagationInherit: child receives parent's experimentContext
	// verbatim; ExperimentRouter is skipped.
	PropagationInherit Propagation = "inherit"
	// PropagationControl: child is forced to variant "control";
	// ExperimentRouter is skipped.
	PropagationControl Propagation = "control"
	// PropagationIndependent: ExperimentRouter evaluates the child
	// for experiment eligibility independently of the parent.
	PropagationIndependent Propagation = "independent"
)

// AllPropagations returns the closed §10.7 enum.
func AllPropagations() []Propagation {
	return []Propagation{PropagationInherit, PropagationControl, PropagationIndependent}
}

// IsValid reports whether p is one of the §10.7 propagation modes.
func (p Propagation) IsValid() bool {
	for _, v := range AllPropagations() {
		if p == v {
			return true
		}
	}
	return false
}

// ContextPropagation is the outcome of applying a §10.7 propagation
// mode to a recursive-delegation child.
type ContextPropagation struct {
	// UseParentContext is true when the child must adopt the
	// ExperimentID/VariantID below verbatim and the ExperimentRouter is
	// skipped — the inherit and control modes. It is false for the
	// independent mode, where the caller routes the child afresh.
	UseParentContext bool
	// ExperimentID and VariantID are the child's enrollment when
	// UseParentContext is true. The child records this context with
	// inherited=true.
	ExperimentID string
	VariantID    string
}

// PropagateContext applies a §10.7 experiment-context propagation mode
// to a delegation child whose parent is enrolled in parentExperimentID
// at parentVariantID. Under `inherit` the child receives the parent's
// enrollment verbatim; under `control` the child is forced onto the
// reserved control variant of the parent's experiment; under
// `independent` the child is routed afresh (UseParentContext false).
func PropagateContext(parentExperimentID, parentVariantID string, mode Propagation) ContextPropagation {
	switch mode {
	case PropagationInherit:
		return ContextPropagation{
			UseParentContext: true,
			ExperimentID:     parentExperimentID,
			VariantID:        parentVariantID,
		}
	case PropagationControl:
		return ContextPropagation{
			UseParentContext: true,
			ExperimentID:     parentExperimentID,
			VariantID:        ControlVariantID,
		}
	default: // independent — the child is routed independently.
		return ContextPropagation{UseParentContext: false}
	}
}

// ControlVariantID is the §10.7 reserved variant identifier the
// gateway auto-assigns to sessions that do not land in any named
// variant. Deployers cannot define a variant with this id; attempts
// are rejected at the admin API as RESERVED_IDENTIFIER (HTTP 422).
const ControlVariantID = "control"

// Variant is one entry in §10.7's variants list.
type Variant struct {
	// ID is the variant identifier. Cannot equal ControlVariantID.
	ID string
	// Weight is the traffic fraction routed to this variant; in (0, 1).
	Weight float64
}

// Definition is the subset of §10.7 ExperimentDefinition that this
// package validates and consumes.
type Definition struct {
	ID            string
	Status        Status
	BaseRuntime   string
	Variants      []Variant
	TargetingMode TargetingMode
	Sticky        Sticky
	Propagation   Propagation
}

// Validate reports the §10.7 admission errors for an
// ExperimentDefinition at admin POST/PUT time.
func (d Definition) Validate() error {
	v := []string{}
	if d.ID == "" {
		v = append(v, "id is required")
	}
	if !d.Status.IsValid() {
		v = append(v, fmt.Sprintf("status %q is not in {active, paused, concluded}", d.Status))
	}
	if d.BaseRuntime == "" {
		v = append(v, "baseRuntime is required")
	}
	if !d.TargetingMode.IsValid() {
		v = append(v, fmt.Sprintf("targeting.mode %q is not in {percentage, external}", d.TargetingMode))
	}
	if !d.Sticky.IsValid() {
		v = append(v, fmt.Sprintf("targeting.sticky %q is not in {user, session, none}", d.Sticky))
	}
	if !d.Propagation.IsValid() {
		v = append(v, fmt.Sprintf("propagation.childSessions %q is not in {inherit, control, independent}", d.Propagation))
	}
	var totalWeight float64
	seen := map[string]bool{}
	for i, vt := range d.Variants {
		if vt.ID == "" {
			v = append(v, fmt.Sprintf("variants[%d].id is required", i))
			continue
		}
		if vt.ID == ControlVariantID {
			v = append(v, fmt.Sprintf("variants[%d].id %q is a reserved identifier", i, vt.ID))
		}
		if seen[vt.ID] {
			v = append(v, fmt.Sprintf("variants[%d].id %q duplicates an earlier variant", i, vt.ID))
		}
		seen[vt.ID] = true
		if vt.Weight <= 0 || vt.Weight >= 1 {
			v = append(v, fmt.Sprintf("variants[%d].weight %g must be in (0, 1)", i, vt.Weight))
		}
		totalWeight += vt.Weight
	}
	if totalWeight >= 1 {
		v = append(v, fmt.Sprintf("Σ variant_weights %g must be < 1 (remainder is the control group)", totalWeight))
	}
	if len(v) == 0 {
		return nil
	}
	return &ValidationError{ID: d.ID, Violations: v}
}

// ValidationError captures Definition.Validate failures. The admin
// API surfaces this as 422 with a `RESERVED_IDENTIFIER` /
// `INVALID_VARIANT_WEIGHTS` error code per §10.7.
type ValidationError struct {
	ID         string
	Violations []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("experiment: %s: %v", e.ID, e.Violations)
}

// AssignVariant implements the §10.7 percentage-mode bucketing
// algorithm. The returned variant id is one of the entries in
// definition.Variants or ControlVariantID when the bucket falls into
// the remainder (Σ variant_weights to 1.0).
//
// Per §10.7 "Anonymous sessions": if the assignmentKey is empty (i.e.,
// sticky: user but user_id is null), the function returns
// ControlVariantID without invoking the HMAC.
func AssignVariant(assignmentKey, experimentID string, variants []Variant) string {
	if assignmentKey == "" {
		return ControlVariantID
	}
	mac := hmac.New(sha256.New, []byte(experimentID))
	mac.Write([]byte(assignmentKey))
	raw := mac.Sum(nil)
	bucketInt := binary.BigEndian.Uint64(raw[:8])
	bucket := float64(bucketInt) / (1 << 64)
	cumulative := 0.0
	for _, v := range variants {
		cumulative += v.Weight
		if bucket < cumulative {
			return v.ID
		}
	}
	return ControlVariantID
}

// Assignment is the outcome of routing a session through a tenant's
// active experiments. A zero Assignment means no experiment enrolled
// the session — it runs the base runtime with no experiment context.
type Assignment struct {
	ExperimentID string
	VariantID    string
}

// Route applies the §10.7 first-match multi-experiment rule over
// percentage-mode experiments. The experiments are evaluated in the
// given order (the caller sorts by created_at), and the first routable
// percentage-mode experiment that assigns the session to a non-control
// variant wins. Paused and concluded experiments are skipped, as are
// external-mode experiments — their assignment is resolved by an
// OpenFeature provider; use RouteMixed to include them. When no
// experiment produces a non-control variant the returned Assignment is
// zero.
//
// The §10.7 assignment key depends on each experiment's sticky mode:
// `user` keys on userID, `session` and `none` key on sessionID. An
// empty key (sticky:user with an anonymous session) yields control.
func Route(experiments []Definition, userID, sessionID string) Assignment {
	return RouteMixed(experiments, userID, sessionID, nil)
}

// SkippedAfter returns the §16.6 experiment.multi_eligible_skipped
// audit set for a session that RouteMixed enrolled in enrolledID: the
// routable experiments that the first-match rule left unevaluated
// because enrolledID — an earlier candidate in evaluation order —
// already assigned a non-control variant. The experiments slice must
// be in the same evaluation order passed to RouteMixed. Paused and
// concluded experiments are always excluded — RouteMixed never
// evaluates them.
//
// externalEvaluated reports whether RouteMixed ran with a non-nil
// ExternalEvaluator. When false, external-mode experiments are
// excluded because RouteMixed skipped every one of them regardless of
// the first-match rule (the tenant configures no external targeting);
// when true, an external-mode experiment after enrolledID was a live
// candidate that the first-match rule skipped, so it is included.
//
// Returns nil when enrolledID is empty, is not present, or is the last
// routable candidate.
func SkippedAfter(experiments []Definition, enrolledID string, externalEvaluated bool) []string {
	if enrolledID == "" {
		return nil
	}
	var skipped []string
	seenEnrolled := false
	for _, e := range experiments {
		if e.ID == enrolledID {
			seenEnrolled = true
			continue
		}
		if !seenEnrolled || !e.Status.IsRoutable() {
			continue
		}
		if e.TargetingMode == TargetingExternal && !externalEvaluated {
			continue
		}
		skipped = append(skipped, e.ID)
	}
	return skipped
}

// AssignVariantDeterministic verifies the §10.7 determinism
// property: identical inputs always yield the same variant. Returns
// nil when the property holds for the supplied set of test keys.
// Used in tests; production code should not need it.
func AssignVariantDeterministic(assignmentKey, experimentID string, variants []Variant, iterations int) error {
	if iterations < 2 {
		iterations = 2
	}
	first := AssignVariant(assignmentKey, experimentID, variants)
	for i := 1; i < iterations; i++ {
		next := AssignVariant(assignmentKey, experimentID, variants)
		if next != first {
			return fmt.Errorf("experiment: AssignVariant non-deterministic: first=%q, iter %d=%q", first, i, next)
		}
	}
	return nil
}
