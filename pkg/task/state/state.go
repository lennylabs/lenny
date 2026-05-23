// SPDX-License-Identifier: MIT

// Package state defines the TaskRecord state machine, per spec §8.8.
//
// The state enum and ValidTransitions() form the authoritative contract
// for the §8.8 task lifecycle. IsValid returns nil for an edge present
// in ValidTransitions() and an InvalidTransitionError otherwise.
//
// §4.2 task-record design clarification:
//
// The §4.2 line 157 Session Manager bullet ("Task records and
// parent/child lineage (task tree)") is realised in v1 by the
// sessions table — there is no separate task table. Every task
// record IS a session row identified by sessions.id and linked to
// its parent via sessions.parent_session_id. The §8.8 state values
// declared here govern the externally-visible MCP task lifecycle;
// the persistent record lives on the matching session row. The
// gateway's task-tree handler walks this lineage to assemble the
// §15.1 GET /v1/sessions/{id}/tree response.
// spec: §4.2 line 157.
package state

import "fmt"

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

// InvalidTransitionError is returned by IsValid for any edge not present
// in ValidTransitions(). Callers can errors.As to retrieve the typed
// value and read From/To for structured logging.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("task: %q → %q is not a valid transition per spec §8.8", e.From, e.To)
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
