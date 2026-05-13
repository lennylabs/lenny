package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/task/state"
)

// spec: 8.8
// diagnosis: state.All() does not match the spec §8.8 state list. Phase 1
//
//	added input_required as a sub-state of running; verify it
//	appears in All().
func TestAllStatesIncludeInputRequired(t *testing.T) {
	t.Parallel()
	want := []state.State{
		state.Submitted, state.Running, state.InputRequired,
		state.Completed, state.Failed, state.Cancelled, state.Expired,
	}
	got := state.All()
	if len(got) != len(want) {
		t.Fatalf("state.All() length = %d, want %d", len(got), len(want))
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("state.All()[%d] = %q, want %q", i, got[i], s)
		}
	}
}

// spec: 8.8
// diagnosis: MCPProtocolState mapping diverged from the spec §8.8 table.
//
//	cancelled → "canceled" (MCP spelling); expired collapses to
//	"failed" at the protocol boundary.
func TestMCPProtocolStateMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   state.State
		want string
	}{
		{state.Submitted, "submitted"},
		{state.Running, "working"},
		{state.InputRequired, "input_required"},
		{state.Completed, "completed"},
		{state.Failed, "failed"},
		{state.Cancelled, "canceled"},
		{state.Expired, "failed"},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.in), func(t *testing.T) {
			t.Parallel()
			if got := state.MCPProtocolState(c.in); got != c.want {
				t.Errorf("MCPProtocolState(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// spec: 8.8
// diagnosis: IsValid rejected a transition in ValidTransitions() or
//
//	accepted one not in the list.
//
// Phase 1: Phase 2 implementation makes this pass. Until then, skipped.
func TestIsValidCanonicalTransitions(t *testing.T) {
	t.Parallel()
	for _, tr := range state.ValidTransitions() {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			err := state.IsValid(tr.From, tr.To)
			if errors.Is(err, state.ErrNotImplemented) {
				t.Skipf("not implemented in Phase 1")
			}
			if err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil", tr.From, tr.To, err)
			}
		})
	}
}
