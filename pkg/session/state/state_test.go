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

// TestMCPProtocolStateSessionMapping_spec_8_8_871 covers the §8.8
// lines 871-883 supplementary table: pre-running session states
// collapse to `submitted`, `suspended`/`resume_pending`/`resuming` map
// to `working` with the canonical metadata annotation,
// `awaiting_client_action` collapses to `input_required`, and the
// terminal + `input_required` states use the §8.8 line 857 task-level
// table. spec: §8.8 lines 855-883. F-8.8.9.
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
