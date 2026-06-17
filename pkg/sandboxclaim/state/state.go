// SPDX-License-Identifier: MIT

// Package state defines the SandboxClaim CRD binding-state machine.
//
// SandboxClaim is the per-pod-occupancy claim a gateway replica creates
// when it acquires an idle Sandbox pod. SandboxClaim.status.phase tracks
// the gateway-owned binding state of that occupancy, distinct from the
// pod lifecycle state machine the WarmPoolController projects onto
// Sandbox.status.phase (§6.2). The gateway is the sole writer of the
// binding state; the WarmPoolController consumes it as projection input
// but never writes it.
//
// The binding state advances bound → recycling → reserved across a
// recycling pod's occupancy episode and back to bound on a same-tenant
// rebind within the hold window; released and failed are the terminal
// retirement dispositions. Returning a held pod to the pool
// (reserved → idle) is the claim DELETE edge rather than a phase value.
//
// ADR-007 governs the optimistic-locking and failover-fencing behavior
// the lenny-sandboxclaim-guard admission webhook backstops.
//
// spec: §4.6.1 (binding states, reserved hold, precondition DELETE),
// §4.6.3 (binding-state enumeration), §6.14 (coarse pod enum projection).
package state

import "fmt"

// State is the SandboxClaim binding state, written to
// SandboxClaim.status.phase.
type State string

const (
	// Bound: the claim is bound to a claimed pod. spec: §4.6.3.
	Bound State = "bound"
	// Recycling: occupancy reached zero on a recycling pool and the
	// whole-pod scrub (and, on preConnect pools, the SDK re-warm that
	// follows it) is running. spec: §4.6.3.
	Recycling State = "recycling"
	// Reserved: the pod is scrubbed (and SDK-warm on preConnect pools) and
	// held for its pinned tenant until holdExpiresAt; it is excluded from
	// idle inventory. spec: §4.6.3.
	Reserved State = "reserved"
	// Released: limit-reached retirement disposition
	// (recycle.maxSessionsPerPod or recycle.maxPodUptimeSeconds reached).
	// Terminal: the WarmPoolController drains then terminates the pod
	// rather than returning it to the pool. spec: §4.6.3.
	Released State = "released"
	// Failed: scrub-failure or crashed-session retirement disposition.
	// Terminal: the WarmPoolController drains then terminates the pod.
	// spec: §4.6.3.
	Failed State = "failed"
)

// All returns the binding-state enumeration in advancement order. It
// mirrors the SandboxClaim.status.phase CRD enum
// (bound;recycling;reserved;released;failed). spec: §4.6.3.
func All() []State {
	return []State{Bound, Recycling, Reserved, Released, Failed}
}

// Terminal returns the retirement dispositions. spec: §4.6.3 — only
// released and failed are terminal; reserved → idle is a claim DELETE.
func Terminal() []State {
	return []State{Released, Failed}
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

// ValidTransitions enumerates the legal binding-state edges. The initial
// CREATE edge is modeled with From = "". A recycling pod advances
// bound → recycling → reserved and rebinds reserved → bound within the
// hold window; every live state can record a terminal retirement
// disposition (released or failed). Returning a held pod to the pool
// (reserved → idle) is a claim DELETE rather than a phase write and so is
// not an edge here. spec: §4.6.1 (binding states, rebind), §4.6.3.
func ValidTransitions() []Transition {
	return []Transition{
		{"", Bound}, // initial create
		{Bound, Recycling},
		{Recycling, Reserved},
		{Reserved, Bound}, // same-tenant rebind within the hold window
		{Bound, Released},
		{Bound, Failed},
		{Recycling, Released},
		{Recycling, Failed},
		{Reserved, Released},
		{Reserved, Failed},
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
	return fmt.Sprintf("sandboxclaim: %q → %q is not a valid transition per spec §4.6.1", e.From, e.To)
}

var validSet = func() map[Transition]struct{} {
	m := make(map[Transition]struct{}, len(ValidTransitions()))
	for _, t := range ValidTransitions() {
		m[t] = struct{}{}
	}
	return m
}()

// IsValid reports whether the transition from → to is legal per the
// canonical list in ValidTransitions(). The initial create edge is
// modeled as From="". Returns nil on a legal edge and an
// *InvalidTransitionError on an illegal one.
func IsValid(from, to State) error {
	if _, ok := validSet[Transition{From: from, To: to}]; ok {
		return nil
	}
	return &InvalidTransitionError{From: from, To: to}
}
