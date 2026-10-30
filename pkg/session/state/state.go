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
// authoritative contract; the IsValid function below reads it. It holds
// every edge of the §7.2 session state machine block, including the /start
// edges ready → starting, starting → running, and starting → failed, the
// maxSessionAge expiry edges from starting, resume_pending, and resuming,
// the terminate edge from input_required, the retries-exhausted failure
// edges into awaiting_client_action, and the resuming → resume_pending edge.
// The pre-running edges created → finalizing → ready come from the §6.2 pod
// state machine. It also holds the → failed and → expired edge from every
// non-terminal state, which the §7.2 "Terminal writes from any non-terminal
// state." paragraph adds for the operator force-terminate, the budget,
// credential-lease, and fallback exhaustion, orphan cleanup, and the
// perChildMaxAge expiry.
//
// spec: §7.2 (Interactive Session Model; Terminal writes from any
// non-terminal state), §6.2 (Pod State Machine)
func ValidTransitions() []Transition {
	edges := []Transition{
		// derive failure path (only when gateway.persistDeriveFailureRows is true)
		{Created, Failed},
		// happy path
		{Created, Finalizing},
		{Finalizing, Ready},
		{Ready, Starting},
		{Starting, Running},
		// /start failure and expiry paths
		{Starting, Failed},
		{Starting, Expired},
		// running: agent-driven transitions
		{Running, InputRequired},
		{Running, Suspended},
		{Running, Completed},
		{Running, ResumePending},
		{Running, AwaitingClientAction},
		{Running, Failed},
		{Running, Cancelled},
		{Running, Expired},
		// input_required exits
		{InputRequired, Running},
		{InputRequired, Completed},
		{InputRequired, Cancelled},
		{InputRequired, Expired},
		{InputRequired, Failed},
		{InputRequired, ResumePending},
		{InputRequired, AwaitingClientAction},
		// suspended exits
		{Suspended, Running},
		{Suspended, ResumePending},
		{Suspended, Completed},
		{Suspended, Cancelled},
		{Suspended, Expired},
		{Suspended, Failed},
		{Suspended, AwaitingClientAction},
		// resume path
		{ResumePending, Resuming},
		{ResumePending, AwaitingClientAction},
		{ResumePending, Cancelled},
		{ResumePending, Completed},
		{ResumePending, Expired},
		{Resuming, Running},
		{Resuming, ResumePending},
		{Resuming, AwaitingClientAction},
		{Resuming, Cancelled},
		{Resuming, Completed},
		{Resuming, Expired},
		// awaiting-client-action exits
		{AwaitingClientAction, ResumePending},
		{AwaitingClientAction, Completed},
		{AwaitingClientAction, Cancelled},
		{AwaitingClientAction, Expired},
	}
	return appendAnyStateTerminalEdges(edges)
}

// anyStateTerminalTargets are the terminal states the §7.2 "Terminal writes
// from any non-terminal state." paragraph reaches from every non-terminal
// state: operator force-terminate writes failed; budget, credential-lease,
// or LLM fallback exhaustion and a perChildMaxAge expiry write expired; and
// orphan cleanup writes cancelled (under a terminal cancel_all parent) or
// expired.
//
// spec: §7.2 (Terminal writes from any non-terminal state); §8.10 (Orphan cleanup)
var anyStateTerminalTargets = []State{Failed, Expired, Cancelled}

// appendAnyStateTerminalEdges adds the → failed, → expired, and → cancelled
// edge from every non-terminal state that edges does not already list, in
// All() order, so the result holds each edge once. The → cancelled edge is
// orphan cleanup's write over an orphan of any non-terminal state.
func appendAnyStateTerminalEdges(edges []Transition) []Transition {
	listed := make(map[Transition]bool, len(edges))
	for _, e := range edges {
		listed[e] = true
	}
	for _, from := range All() {
		if IsTerminal(from) {
			continue
		}
		for _, to := range anyStateTerminalTargets {
			e := Transition{From: from, To: to}
			if !listed[e] {
				edges = append(edges, e)
				listed[e] = true
			}
		}
	}
	return edges
}

// MCPProtocolState projects a §7.2 Lenny session state into the §8.8
// MCP Tasks surface per the supplementary table (spec §8.8). It returns the protocol state string and any metadata
// annotations the §8.8 contract requires.
//
//   - Pre-running states (created, finalizing, ready, starting) collapse
//     to `submitted` per §8.8.
//   - `running` → `working`; `input_required` → `input_required`.
//   - `suspended` → `working` + metadata.suspended=true (§8.8).
//   - `resume_pending` → `working` + metadata.resuming=true (§8.8).
//   - `awaiting_client_action` → `input_required` (§8.8).
//   - `resuming` is internal to the recovery cycle; it is surfaced as
//     `working` + metadata.resuming=true alongside `resume_pending` so
//     external observers do not flicker through a transient state.
//   - Terminal states use the §8.8 task-level mapping
//     (`cancelled` → `canceled`, `expired` → `failed`).
//   - An unknown state returns ("", nil).
//
// The metadata map is nil when the surface needs no annotations.
//
// spec: §8.8. F-8.8.9.
func MCPProtocolState(s State) (string, map[string]any) {
	switch s {
	case Created, Finalizing, Ready, Starting:
		return "submitted", nil
	case Running:
		return "working", nil
	case InputRequired:
		return "input_required", nil
	case Suspended:
		return "working", map[string]any{"suspended": true}
	case ResumePending, Resuming:
		return "working", map[string]any{"resuming": true}
	case AwaitingClientAction:
		return "input_required", nil
	case Completed:
		return "completed", nil
	case Failed:
		return "failed", nil
	case Cancelled:
		return "canceled", nil
	case Expired:
		return "failed", nil
	}
	return "", nil
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
