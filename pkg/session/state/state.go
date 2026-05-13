// Package state defines the session state machine.
//
// Phase 1 ships the state enum and the canonical transition table as the
// authoritative contract. The transition-validation function (IsValid) is
// scaffolded as a "not implemented" stub; Phase 2 implements it against
// these tests.
//
// Spec: 07_session-lifecycle.md §7.2.
package state

import "errors"

// State is the session lifecycle state, per spec §7.2.
type State string

// Canonical session states. Order matches the spec.
const (
	Created              State = "created"
	Finalizing           State = "finalizing"
	Ready                State = "ready"
	Starting             State = "starting"
	Running              State = "running"
	InputRequired        State = "input_required" // sub-state of running
	Suspended            State = "suspended"
	ResumePending        State = "resume_pending"
	AwaitingClientAction State = "awaiting_client_action"
	Resuming             State = "resuming"
	Completed            State = "completed"
	Failed               State = "failed"
	Cancelled            State = "cancelled"
	Expired              State = "expired"
)

// All returns every canonical state. Order is stable for golden testing.
func All() []State {
	return []State{
		Created, Finalizing, Ready, Starting, Running, InputRequired,
		Suspended, ResumePending, AwaitingClientAction, Resuming,
		Completed, Failed, Cancelled, Expired,
	}
}

// Terminal returns the terminal states. A session in a terminal state
// cannot transition further.
func Terminal() []State {
	return []State{Completed, Failed, Cancelled, Expired}
}

// IsTerminal reports whether the given state is terminal.
func IsTerminal(s State) bool {
	for _, t := range Terminal() {
		if s == t {
			return true
		}
	}
	return false
}

// Transition is one legal edge in the state machine.
type Transition struct {
	From State
	To   State
}

// ValidTransitions returns every legal edge per spec §7.2. The list is the
// authoritative contract; the IsValid function below reads it.
func ValidTransitions() []Transition {
	return []Transition{
		// derive failure path (only when gateway.persistDeriveFailureRows is true)
		{Created, Failed},
		// happy path
		{Created, Finalizing},
		{Finalizing, Ready},
		{Ready, Starting},
		{Starting, Running},
		// session-start failure paths
		{Starting, ResumePending},
		{Starting, Failed},
		// running: agent-driven transitions
		{Running, InputRequired},
		{Running, Suspended},
		{Running, Completed},
		{Running, ResumePending},
		{Running, Failed},
		{Running, Cancelled},
		{Running, Expired},
		// input_required exits
		{InputRequired, Running},
		{InputRequired, Cancelled},
		{InputRequired, Expired},
		{InputRequired, Failed},
		// suspended exits
		{Suspended, Running},
		{Suspended, ResumePending},
		{Suspended, Completed},
		{Suspended, Cancelled},
		{Suspended, Expired},
		{Suspended, Failed},
		// resume path
		{ResumePending, Resuming},
		{ResumePending, AwaitingClientAction},
		{ResumePending, Cancelled},
		{ResumePending, Completed},
		{Resuming, Running},
		{Resuming, AwaitingClientAction},
		{Resuming, Cancelled},
		{Resuming, Completed},
		// awaiting-client-action exits
		{AwaitingClientAction, ResumePending},
		{AwaitingClientAction, Completed},
		{AwaitingClientAction, Cancelled},
		{AwaitingClientAction, Expired},
	}
}

// ErrNotImplemented is returned by IsValid until Phase 2 implementation
// lands. The diagnosis comment on each failing test explains where the
// implementation belongs.
var ErrNotImplemented = errors.New("session-state IsValid: not implemented in Phase 1 (see TESTING.md §13.1)")

// IsValid reports whether the transition from → to is legal.
//
// Phase 1 stub: always returns ErrNotImplemented.
// Phase 2 implementation: returns nil for transitions in ValidTransitions(),
// a structured error otherwise.
func IsValid(from, to State) error {
	_ = from
	_ = to
	return ErrNotImplemented
}
