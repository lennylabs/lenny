// Package state defines the SandboxClaim CRD state machine.
//
// SandboxClaim represents the gateway's binding of a session to a
// previously idle Sandbox. The state machine is small (three states)
// because the authoritative pod state lives on Sandbox.status.phase;
// SandboxClaim.status.phase tracks the session-binding only.
//
// ADR-007 governs the optimistic-locking and failover-fencing behavior
// that admission-webhook lenny-sandboxclaim-guard enforces. Phase 1
// ships the state enum and transition list as the authoritative
// contract; Phase 3.5 wires the admission webhook.
//
// Spec: 04_system-components.md §4.6.1.
package state

import "errors"

// State is the SandboxClaim phase, written to SandboxClaim.status.phase.
type State string

const (
	// Bound: claim successfully bound to a claimed Sandbox.
	Bound State = "bound"
	// Released: pod released back to the pool. Resource is deleted shortly
	// after this state is observed.
	Released State = "released"
	// Failed: claim cannot be fulfilled. Terminal. Resource is deleted
	// after the session reaches its own terminal state.
	Failed State = "failed"
)

func All() []State {
	return []State{Bound, Released, Failed}
}

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

// ValidTransitions per spec §4.6.1 and ADR-007. Note: the initial
// transition is `<CREATE> → Bound`, modeled here with From = "".
func ValidTransitions() []Transition {
	return []Transition{
		{"", Bound}, // initial create
		{Bound, Released},
		{Bound, Failed},
	}
}

var ErrNotImplemented = errors.New("sandboxclaim-state IsValid: not implemented in Phase 1 (see TESTING.md §13.1)")

// IsValid reports whether the transition from → to is legal.
//
// Phase 1 stub. Phase 3.5 implements alongside the
// lenny-sandboxclaim-guard admission webhook.
func IsValid(from, to State) error {
	_ = from
	_ = to
	return ErrNotImplemented
}
