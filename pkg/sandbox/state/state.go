// SPDX-License-Identifier: MIT

// Package state defines the Sandbox CRD coarse pod-occupancy state machine,
// per spec §6.2.
//
// The State enum and ValidTransitions() form the authoritative contract for
// the values the WarmPoolController writes to Sandbox.status.phase. IsValid
// returns nil for an edge present in ValidTransitions() and an
// InvalidTransitionError otherwise. The fine session-lifecycle states
// (running, suspended, resume_pending, resuming, awaiting_client_action, and
// the session terminals completed/cancelled/expired) live in the Postgres
// session model, not on the CRD (spec: §6.2, §6.37); they are not part of
// this enum. The host-schedulable label check that distinguishes a recycle
// re-warm (claimed → sdk_connecting) from a retirement (claimed → draining)
// on a cordoned node is enforced separately by the WarmPoolController; this
// function reports both edges as valid because they are legal at the
// state-machine layer.
package state

import "fmt"

// State is the Sandbox coarse pod-occupancy phase, written to
// Sandbox.status.phase. spec: §6.2 — the enum is warming, idle, reserved,
// claimed, sdk_connecting, draining, failed, terminated.
type State string

// LabelState is the pod label the Sandbox-to-Pod reconciler keeps in
// sync with the Sandbox's §6.2 phase. The §4.6.1 per-pool
// PodDisruptionBudget selects warm pods by this label
// (lenny.dev/state: idle) so disruption protection targets only
// unclaimed pods. spec: §4.6.1 "Disruption protection for agent pods".
//
// The label carries only the coarse operational value set (see
// CoarseState), not the full §6.2 phase: spec §6.2 reserves the full phase
// for Sandbox.status.phase and restricts the pod label to
// idle/active/draining for selectors, monitoring, and NetworkPolicies.
const LabelState = "lenny.dev/state"

// LabelRuntime is the pod label carrying the runtime name (the Sandbox's
// RuntimeRef). spec: §6.2 — operators select pods by runtime type through
// this coarse label. It is immutable for a pod's lifetime, stamped once at
// pod creation.
const LabelRuntime = "lenny.dev/runtime"

// The coarse pod-state label values. spec: §6.2 — the lenny.dev/state pod
// label carries only these three operational values for kubectl,
// monitoring, and NetworkPolicy selectors. CoarseIdle and CoarseDraining
// deliberately equal the Idle and Draining phase strings so the §4.6.1 PDB
// idle selector (which matches string(Idle)) keeps working unchanged.
const (
	CoarseIdle     = "idle"
	CoarseActive   = "active"
	CoarseDraining = "draining"
)

// CoarseState maps a §6.2 phase to its coarse lenny.dev/state pod-label
// value (spec §6.2: idle, active, or draining) and reports whether the
// phase has a coarse operational value at all. A pod is idle when warm and
// claimable, active once it is claimed and serving one or more sessions,
// and draining when retiring. The reserved hold window also maps to active:
// a recycled pod held for its pinned tenant is excluded from idle
// inventory, so the §4.6.1 PDB idle selector must not match it (spec §6.2
// "reserved hold semantics"). claimed and reserved both map to active, so
// the label stays active across the occupancy-zero recycle boundary and the
// hold and never oscillates as same-tenant sessions turn over. With the
// coarse occupancy enum a pod serving any number of concurrent sessions
// projects claimed, so concurrent occupancy collapses into the claimed →
// active mapping and is observable through the Redis slot counter and
// metrics rather than the pod label (spec §6.2 "concurrent pod lifecycle").
// The pre-ready phase (warming) and the terminal phases (failed,
// terminated) have no coarse value — the pod is either not yet claimable or
// gone — so the second return is false and the reconciler removes the label
// rather than emitting a fourth value the spec does not define.
// sdk_connecting is unmapped on both the warm-fill leg and the recycle SDK
// re-warm leg: the phase is shared between unclaimed pre-idle inventory and
// an occupied recycling pod, so the phase alone cannot distinguish them and
// the reconciler removes the label in both windows (spec §6.2). spec: §6.2.
func CoarseState(s State) (string, bool) {
	switch s {
	case Idle:
		return CoarseIdle, true
	case Draining:
		return CoarseDraining, true
	case Claimed, Reserved:
		return CoarseActive, true
	default:
		return "", false
	}
}

const (
	Warming       State = "warming"
	SDKConnecting State = "sdk_connecting"
	Idle          State = "idle"
	// Reserved is the §6.2 coarse occupancy phase a recycled pod projects
	// while its claim is held for the pinned tenant through the
	// gateway.claimHoldTTLSeconds hold window (claim binding state
	// `reserved`). A pod that reaches reserved is always scrubbed and, on
	// a preConnect pool, SDK-warm; it is excluded from idle inventory and
	// rebinds to claimed when a same-tenant session arrives within the
	// hold, or returns to idle when the hold TTL expires. spec: §6.2
	// (reserved hold semantics).
	Reserved   State = "reserved"
	Claimed    State = "claimed"
	Failed     State = "failed"
	Draining   State = "draining"
	Terminated State = "terminated"
)

// All returns every coarse §6.2 pod-occupancy phase the WarmPoolController
// writes to Sandbox.status.phase.
func All() []State {
	return []State{
		Warming, SDKConnecting, Idle, Reserved, Claimed,
		Failed, Draining, Terminated,
	}
}

// Terminal returns the §6.2 coarse terminal phases. failed is the
// warm-fill / recycle-rewarm failure terminal and terminated is the
// pod-lifecycle terminal (spec: §6.2 sdk_connecting → terminated, draining
// → terminated). These are the phases at which the POD is gone or being
// reclaimed and no edge other than the failed → draining reclamation leaves
// them; terminated has no outgoing edge of any kind. The fine
// session-terminal states (completed, cancelled, expired) live in the
// Postgres session model and are not coarse CRD phases (spec: §6.2, §6.37).
func Terminal() []State {
	return []State{Failed, Terminated}
}

func IsTerminal(s State) bool {
	for _, t := range Terminal() {
		if s == t {
			return true
		}
	}
	return false
}

type Transition struct {
	From State
	To   State
}

// ValidTransitions per spec §6.2. The list captures the warm-fill pod-warm
// path, the SDK-warm path, the claim-driven occupancy projection, and the
// recycle edges (the occupancy-zero recycle re-warm, the same-tenant
// rebind, and the hold-expiry return to idle). With the coarse occupancy
// enum the phase carries no separate concurrent-occupancy value: a pod
// serving any number of concurrent sessions projects claimed, so additional
// and completing sessions are the claimed → claimed self-edge rather than a
// distinct slot phase. The fine session-lifecycle transitions live in the
// Postgres session model and are not CRD edges (spec: §6.2, §6.37).
func ValidTransitions() []Transition {
	return []Transition{
		// Warm fill (pod-warm path).
		{Warming, Idle},
		{Warming, Failed},
		// Warm fill (SDK-warm path, preConnect: true).
		{Warming, SDKConnecting},
		{SDKConnecting, Idle},
		{SDKConnecting, Failed},
		{SDKConnecting, Terminated},
		// Occupancy projection (WarmPoolController-computed from the per-pod
		// SandboxClaim).
		{Idle, Claimed},
		// claimed → claimed is the concurrent-occupancy self-edge: an
		// additional session is assigned or an existing session completes
		// while the Redis-counter occupancy stays nonzero, so the coarse
		// phase remains claimed.
		{Claimed, Claimed},
		{Idle, Draining},
		{Claimed, Draining},
		{Failed, Draining},
		{Draining, Terminated},
		// Recycle edges (recycle.enabled: true; occupancy reaches zero after
		// clean session termination; the claim is patched bound → recycling
		// and the whole-pod scrub runs while the pod projects claimed).
		{Claimed, SDKConnecting},  // preConnect re-warm begins (host node schedulable)
		{Claimed, Reserved},       // non-preConnect: claim patched to reserved, no re-warm leg
		{SDKConnecting, Reserved}, // SDK re-warm completes within the watchdog
		{Reserved, Claimed},       // same-tenant session rebinds within the hold TTL
		{Reserved, Idle},          // hold TTL expires — precondition-guarded claim DELETE
	}
}

// InvalidTransitionError is returned by IsValid for any edge not present
// in ValidTransitions(). Callers can errors.As to retrieve the typed
// value and read From/To for structured logging.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("sandbox: %q → %q is not a valid transition per spec §6.2", e.From, e.To)
}

var validSet = func() map[Transition]struct{} {
	m := make(map[Transition]struct{}, len(ValidTransitions()))
	for _, t := range ValidTransitions() {
		m[t] = struct{}{}
	}
	return m
}()

// IsValid reports whether the transition from → to is legal per the
// canonical list in ValidTransitions(). Returns nil on a legal edge and
// an *InvalidTransitionError on an illegal one.
func IsValid(from, to State) error {
	if _, ok := validSet[Transition{From: from, To: to}]; ok {
		return nil
	}
	return &InvalidTransitionError{From: from, To: to}
}
