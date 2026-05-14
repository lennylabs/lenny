// SPDX-License-Identifier: MIT

// Package state defines the session state machine.
//
// The state enum and ValidTransitions() form the authoritative contract
// for the §7.2 lifecycle. IsValid returns nil for an edge present in
// ValidTransitions() and an InvalidTransitionError otherwise.
//
// Spec: 07_session-lifecycle.md §7.2.
package state

import "fmt"

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

// InvalidTransitionError is returned by IsValid for any edge not present
// in ValidTransitions(). Callers can errors.As to retrieve the typed
// value and read From/To for structured logging.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("session: %q → %q is not a valid transition per spec §7.2", e.From, e.To)
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
