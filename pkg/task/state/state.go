// SPDX-License-Identifier: MIT

// Package state defines the TaskRecord state machine, per spec §8.8.
//
// Phase 1 ships the state enum and the canonical transition list.
// IsValid is a Phase 2 deliverable; in Phase 1 it returns
// ErrNotImplemented.
package state

import "errors"

// State is the TaskRecord lifecycle state, per spec §8.8.
type State string

const (
	Submitted     State = "submitted"
	Running       State = "running"
	InputRequired State = "input_required" // sub-state of running
	Completed     State = "completed"
	Failed        State = "failed"
	Cancelled     State = "cancelled"
	Expired       State = "expired"
)

// All returns every canonical state in spec order.
func All() []State {
	return []State{Submitted, Running, InputRequired, Completed, Failed, Cancelled, Expired}
}

// Terminal states.
func Terminal() []State {
	return []State{Completed, Failed, Cancelled, Expired}
}

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

// ValidTransitions per spec §8.8.
//
// Note: pod-crash recovery is session-level, not task-level. A task in
// `running` whose session enters `resume_pending` is surfaced to MCP as
// `working + metadata.resuming: true`, but the TaskRecord state itself
// does not change. That is why this list does not contain a
// running → resume_pending edge.
func ValidTransitions() []Transition {
	return []Transition{
		{Submitted, Running},
		{Running, Completed},
		{Running, Failed},
		{Running, Cancelled},
		{Running, Expired},
		{Running, InputRequired},
		{InputRequired, Running},
		{InputRequired, Cancelled},
		{InputRequired, Expired},
		{InputRequired, Failed},
	}
}

// MCPProtocolState maps a TaskRecord state to its MCP Tasks protocol
// equivalent per spec §8.8. Phase 1 ships the mapping table as the
// authoritative contract.
func MCPProtocolState(s State) string {
	switch s {
	case Submitted:
		return "submitted"
	case Running:
		return "working"
	case InputRequired:
		return "input_required"
	case Completed:
		return "completed"
	case Failed:
		return "failed"
	case Cancelled:
		return "canceled" // MCP spelling
	case Expired:
		return "failed" // expired collapses to failed at MCP boundary
	}
	return ""
}

// ErrNotImplemented is returned by IsValid until Phase 2 ships.
var ErrNotImplemented = errors.New("task-state IsValid: not implemented in Phase 1 (see TESTING.md §13.1)")

// IsValid reports whether the transition from → to is legal.
//
// Phase 1 stub: always returns ErrNotImplemented.
func IsValid(from, to State) error {
	_ = from
	_ = to
	return ErrNotImplemented
}
