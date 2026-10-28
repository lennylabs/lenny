// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/session/state"
)

// spec: 7.2
// diagnosis: state.All() returned an unexpected list. Either a state was
//
//	added/removed from the constants in pkg/session/state/state.go
//	without being reflected in All(), or the spec §7.2 state list
//	has been updated and this test needs to be revised. Compare
//	against the canonical state list in spec/07_session-lifecycle.md.
func TestAllStatesIncludeSuspendedAndInputRequired(t *testing.T) {
	t.Parallel()

	all := state.All()
	want := map[state.State]bool{
		state.Suspended:     true,
		state.InputRequired: true,
		// resume_pending, awaiting_client_action, resuming were also added
		// in Phase 1 per spec §7.2.
		state.ResumePending:        true,
		state.AwaitingClientAction: true,
		state.Resuming:             true,
	}
	got := map[state.State]bool{}
	for _, s := range all {
		got[s] = true
	}
	for s := range want {
		if !got[s] {
			t.Errorf("state.All() is missing %q (added in Phase 1)", s)
		}
	}
}

// spec: 7.2
// diagnosis: IsTerminal() returned the wrong value. The terminal states
//
//	per spec §7.2 are exactly: completed, failed, cancelled,
//	expired. Non-terminal states (including input_required and
//	suspended) must return false.
func TestIsTerminal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		s    state.State
		want bool
	}{
		{state.Completed, true},
		{state.Failed, true},
		{state.Cancelled, true},
		{state.Expired, true},
		{state.Running, false},
		{state.InputRequired, false},
		{state.Suspended, false},
		{state.ResumePending, false},
		{state.AwaitingClientAction, false},
		{state.Resuming, false},
		{state.Created, false},
	}
	for _, c := range cases {
		t.Run(string(c.s), func(t *testing.T) {
			t.Parallel()
			if got := state.IsTerminal(c.s); got != c.want {
				t.Errorf("IsTerminal(%q) = %v, want %v", c.s, got, c.want)
			}
		})
	}
}

// TestMCPProtocolStateSessionMapping_spec_8_8_871 covers the §8.8 supplementary table: pre-running session states
// collapse to `submitted`, `suspended`/`resume_pending`/`resuming` map
// to `working` with the canonical metadata annotation,
// `awaiting_client_action` collapses to `input_required`, and the
// terminal + `input_required` states use the §8.8 task-level
// table. spec: §8.8. F-8.8.9.
func TestMCPProtocolStateSessionMapping_spec_8_8_871(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       state.State
		want     string
		metaKey  string
		metaWant any
	}{
		{state.Created, "submitted", "", nil},
		{state.Finalizing, "submitted", "", nil},
		{state.Ready, "submitted", "", nil},
		{state.Starting, "submitted", "", nil},
		{state.Running, "working", "", nil},
		{state.InputRequired, "input_required", "", nil},
		{state.Suspended, "working", "suspended", true},
		{state.ResumePending, "working", "resuming", true},
		{state.Resuming, "working", "resuming", true},
		{state.AwaitingClientAction, "input_required", "", nil},
		{state.Completed, "completed", "", nil},
		{state.Failed, "failed", "", nil},
		{state.Cancelled, "canceled", "", nil}, // MCP spelling
		{state.Expired, "failed", "", nil},     // expired collapses to failed
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.in), func(t *testing.T) {
			t.Parallel()
			got, meta := state.MCPProtocolState(c.in)
			if got != c.want {
				t.Errorf("MCPProtocolState(%q) state = %q, want %q", c.in, got, c.want)
			}
			if c.metaKey == "" {
				if meta != nil {
					t.Errorf("MCPProtocolState(%q) meta = %v, want nil", c.in, meta)
				}
				return
			}
			if meta == nil {
				t.Fatalf("MCPProtocolState(%q) meta = nil, want %q=%v", c.in, c.metaKey, c.metaWant)
			}
			if got := meta[c.metaKey]; got != c.metaWant {
				t.Errorf("MCPProtocolState(%q) meta[%q] = %v, want %v", c.in, c.metaKey, got, c.metaWant)
			}
		})
	}
}

// TestMCPProtocolStateUnknownStateReturnsEmpty_spec_8_8_871 verifies an
// out-of-band state value yields ("", nil) so a malformed input does
// not propagate a misleading default. F-8.8.9.
func TestMCPProtocolStateUnknownStateReturnsEmpty_spec_8_8_871(t *testing.T) {
	t.Parallel()
	got, meta := state.MCPProtocolState(state.State("not-a-real-state"))
	if got != "" {
		t.Errorf("MCPProtocolState(unknown) state = %q, want empty", got)
	}
	if meta != nil {
		t.Errorf("MCPProtocolState(unknown) meta = %v, want nil", meta)
	}
}

// spec: 7.2
// diagnosis: IsValid rejected a transition listed in ValidTransitions(),
//
//	or accepted one that is not listed. The canonical list lives
//	in pkg/session/state/state.go's ValidTransitions().
func TestIsValidCanonicalTransitions(t *testing.T) {
	t.Parallel()

	for _, tr := range state.ValidTransitions() {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) returned %v, want nil (this is a legal transition per spec §7.2)", tr.From, tr.To, err)
			}
		})
	}
}

// spec: 7.2
// diagnosis: IsValid accepted a transition that is not in
//
//	ValidTransitions(). Either the spec admits more transitions
//	than the list captures (then update ValidTransitions), or the
//	implementation is over-permissive (then tighten IsValid).
func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	t.Parallel()

	legal := map[string]bool{}
	for _, tr := range state.ValidTransitions() {
		legal[string(tr.From)+"->"+string(tr.To)] = true
	}

	illegal := []state.Transition{
		{state.Completed, state.Running},
		{state.Failed, state.Running},
		{state.Cancelled, state.Running},
		{state.Expired, state.Running},
		{state.Created, state.Running},
		{state.Finalizing, state.Completed},
	}

	for _, tr := range illegal {
		tr := tr
		if legal[string(tr.From)+"->"+string(tr.To)] {
			t.Fatalf("test setup error: %q→%q is in ValidTransitions but the test asserts it is illegal", tr.From, tr.To)
		}
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			err := state.IsValid(tr.From, tr.To)
			if err == nil {
				t.Errorf("IsValid(%q, %q) returned nil, expected an error (illegal per spec §7.2)", tr.From, tr.To)
			}
			var ite *state.InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Errorf("IsValid(%q, %q) returned %T, want *InvalidTransitionError", tr.From, tr.To, err)
			}
		})
	}
}

// sessionStateMachineEdges is the edge list of the §7.2 "Session state
// machine:" block. An edge listed there more than once (one per trigger)
// appears here once.
var sessionStateMachineEdges = []state.Transition{
	{From: state.Created, To: state.Failed},
	{From: state.Ready, To: state.Starting},
	{From: state.Starting, To: state.Running},
	{From: state.Starting, To: state.Failed},
	{From: state.Starting, To: state.Expired},
	{From: state.Running, To: state.Suspended},
	{From: state.Running, To: state.InputRequired},
	{From: state.Running, To: state.Completed},
	{From: state.Running, To: state.ResumePending},
	{From: state.Running, To: state.AwaitingClientAction},
	{From: state.Running, To: state.Failed},
	{From: state.Running, To: state.Cancelled},
	{From: state.Running, To: state.Expired},
	{From: state.InputRequired, To: state.Running},
	{From: state.InputRequired, To: state.Completed},
	{From: state.InputRequired, To: state.Cancelled},
	{From: state.InputRequired, To: state.Expired},
	{From: state.InputRequired, To: state.ResumePending},
	{From: state.InputRequired, To: state.AwaitingClientAction},
	{From: state.InputRequired, To: state.Failed},
	{From: state.Suspended, To: state.Running},
	{From: state.Suspended, To: state.ResumePending},
	{From: state.Suspended, To: state.Completed},
	{From: state.Suspended, To: state.Cancelled},
	{From: state.Suspended, To: state.Expired},
	{From: state.Suspended, To: state.Failed},
	{From: state.Suspended, To: state.AwaitingClientAction},
	{From: state.ResumePending, To: state.Resuming},
	{From: state.ResumePending, To: state.AwaitingClientAction},
	{From: state.ResumePending, To: state.Cancelled},
	{From: state.ResumePending, To: state.Completed},
	{From: state.ResumePending, To: state.Expired},
	{From: state.Resuming, To: state.Running},
	{From: state.Resuming, To: state.ResumePending},
	{From: state.Resuming, To: state.AwaitingClientAction},
	{From: state.Resuming, To: state.Cancelled},
	{From: state.Resuming, To: state.Completed},
	{From: state.Resuming, To: state.Expired},
	{From: state.AwaitingClientAction, To: state.ResumePending},
	{From: state.AwaitingClientAction, To: state.Completed},
	{From: state.AwaitingClientAction, To: state.Cancelled},
	{From: state.AwaitingClientAction, To: state.Expired},
}

// TestValidTransitionsHoldEverySessionStateMachineEdge_spec_7_2 pins the
// transition table to the §7.2 session state machine block: every edge
// the block lists is in ValidTransitions() and IsValid admits it. The
// retries-exhausted edges into awaiting_client_action, the retryable
// input_required → resume_pending edge, and resuming → resume_pending
// were absent from the table before it was aligned with the block, so
// each of those cases fails against the earlier table.
//
// spec: 7.2 (Interactive Session Model), 6.2 (Pod State Machine)
func TestValidTransitionsHoldEverySessionStateMachineEdge_spec_7_2(t *testing.T) {
	t.Parallel()

	listed := map[state.Transition]bool{}
	for _, tr := range state.ValidTransitions() {
		listed[tr] = true
	}
	for _, tr := range sessionStateMachineEdges {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			if !listed[tr] {
				t.Errorf("ValidTransitions() omits the §7.2 edge %q → %q", tr.From, tr.To)
			}
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil for a §7.2 edge", tr.From, tr.To, err)
			}
		})
	}
}

// TestValidTransitionsHasNoDuplicateEdge_spec_7_2 guards the table against
// an edge listed twice, which would hide a later removal of one copy.
//
// spec: 7.2 (Interactive Session Model)
func TestValidTransitionsHasNoDuplicateEdge_spec_7_2(t *testing.T) {
	t.Parallel()

	seen := map[state.Transition]bool{}
	for _, tr := range state.ValidTransitions() {
		if seen[tr] {
			t.Errorf("ValidTransitions() lists %q → %q twice", tr.From, tr.To)
		}
		seen[tr] = true
	}
}

// TestValidTransitionsOmitTheRemovedStartingResumePendingEdge_spec_7_2 pins
// the removal of starting → resume_pending: a /start failure after the
// ready → starting write takes starting → failed, and no writer produces a
// starting → resume_pending edge. The table listed it before the /start path
// moved through starting.
//
// spec: §7.2 (Session state machine: starting → failed)
func TestValidTransitionsOmitTheRemovedStartingResumePendingEdge_spec_7_2(t *testing.T) {
	t.Parallel()
	if err := state.IsValid(state.Starting, state.ResumePending); err == nil {
		t.Fatal("IsValid(starting, resume_pending) = nil, want an error: the §7.2 block no longer lists the edge")
	}
}

// anyStateWriters are the existing terminal writers the §7.2 "Terminal
// writes from any non-terminal state." paragraph legalizes, each with a
// source state the per-state block lists no edge to its target from.
var anyStateWriters = []struct {
	writer string
	from   state.State
	to     state.State
}{
	{"operator force-terminate", state.ResumePending, state.Failed},
	{"operator force-terminate", state.AwaitingClientAction, state.Failed},
	{"operator force-terminate", state.Resuming, state.Failed},
	{"operator force-terminate", state.Finalizing, state.Failed},
	{"operator force-terminate", state.Ready, state.Failed},
	{"budget terminator", state.Resuming, state.Expired},
	{"budget terminator", state.Suspended, state.Expired},
	{"budget terminator", state.Ready, state.Expired},
	{"orphan cleanup", state.Created, state.Expired},
	{"orphan cleanup", state.Finalizing, state.Expired},
	{"perChildMaxAge expiry", state.ResumePending, state.Expired},
	{"perChildMaxAge expiry", state.AwaitingClientAction, state.Expired},
}

// TestValidTransitionsAdmitEveryAnyStateTerminalWriter_spec_7_2 asserts that
// each existing any-state terminal writer's edge is in ValidTransitions, and
// that every non-terminal state has a → failed and a → expired edge. Before
// the table carried the any-state paragraph, ForceTerminate from
// resume_pending and the budget terminator from resuming were off-table.
//
// spec: §7.2 (Terminal writes from any non-terminal state)
func TestValidTransitionsAdmitEveryAnyStateTerminalWriter_spec_7_2(t *testing.T) {
	t.Parallel()
	for _, c := range anyStateWriters {
		c := c
		t.Run(c.writer+"_"+string(c.from)+"_to_"+string(c.to), func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(c.from, c.to); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil: the %s writes this edge", c.from, c.to, err, c.writer)
			}
		})
	}
	for _, from := range state.All() {
		if state.IsTerminal(from) {
			continue
		}
		for _, to := range []state.State{state.Failed, state.Expired} {
			if err := state.IsValid(from, to); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil for an any-state terminal write", from, to, err)
			}
		}
	}
}
