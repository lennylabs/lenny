// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: 6.2
// diagnosis: A canonical Sandbox state from spec §6.2 is missing from
//
//	state.All(). Phase 1 introduces suspended, resume_pending,
//	awaiting_client_action, resuming, task_cleanup, and the
//	sdk_connecting fork.
func TestAllStatesIncludePhase1Additions(t *testing.T) {
	t.Parallel()
	required := []state.State{
		state.Suspended, state.ResumePending, state.AwaitingClientAction,
		state.Resuming, state.TaskCleanup, state.SDKConnecting,
	}
	got := map[state.State]bool{}
	for _, s := range state.All() {
		got[s] = true
	}
	for _, s := range required {
		if !got[s] {
			t.Errorf("state.All() is missing %q (added/required in Phase 1)", s)
		}
	}
}

// spec: 6.2
// diagnosis: state.IsTerminal returned the wrong value. Sandbox terminal
//
//	states per spec §6.2 are exactly: failed, cancelled, expired,
//	terminated. Pods in `draining` are not terminal.
func TestIsTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    state.State
		want bool
	}{
		{state.Failed, true},
		{state.Cancelled, true},
		{state.Expired, true},
		{state.Terminated, true},
		{state.Draining, false},
		{state.Idle, false},
		{state.Suspended, false},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.s), func(t *testing.T) {
			t.Parallel()
			if got := state.IsTerminal(c.s); got != c.want {
				t.Errorf("IsTerminal(%q) = %v, want %v", c.s, got, c.want)
			}
		})
	}
}

// spec: 6.2
// diagnosis: IsValid rejected or accepted a transition contrary to
//
//	ValidTransitions(). The canonical list is in
//	pkg/sandbox/state/state.go.
//
// Phase 1: Phase 2 implementation makes this pass; skipped here.
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
