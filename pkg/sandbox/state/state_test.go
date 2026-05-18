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

// spec: 5.2
// diagnosis: the concurrent-mode (§5.2) slot_active phase or one of its
// §6.2 transitions is missing from the Sandbox state machine. Phase 12c
// adds slot_active: a concurrent-mode pod hosts up to maxConcurrent
// slots, entering slot_active on the first slot and returning to idle
// when the last slot drains.
func TestSlotActivePhaseAndTransitions(t *testing.T) {
	t.Parallel()
	found := false
	for _, s := range state.All() {
		if s == state.SlotActive {
			found = true
		}
	}
	if !found {
		t.Fatal("state.All() is missing slot_active (added in Phase 12c)")
	}
	// §6.2 concurrent-mode slot edges.
	required := []state.Transition{
		{From: state.Idle, To: state.SlotActive},       // first slot
		{From: state.SlotActive, To: state.SlotActive}, // further slot / sibling drains
		{From: state.SlotActive, To: state.Idle},       // last slot drains
		{From: state.SlotActive, To: state.Draining},   // unhealthy threshold / uptime
	}
	for _, tr := range required {
		if err := state.IsValid(tr.From, tr.To); err != nil {
			t.Errorf("IsValid(%q, %q) = %v, want nil — concurrent-mode edge", tr.From, tr.To, err)
		}
	}
	// slot_active is not a terminal phase.
	if state.IsTerminal(state.SlotActive) {
		t.Error("slot_active must not be a terminal phase")
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
func TestIsValidCanonicalTransitions(t *testing.T) {
	t.Parallel()
	for _, tr := range state.ValidTransitions() {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil", tr.From, tr.To, err)
			}
		})
	}
}

// spec: 6.2
// diagnosis: IsValid accepted a transition that is not in
//
//	ValidTransitions(). Sandbox state machine forbids these
//	edges per §6.2.
func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	t.Parallel()
	illegal := []state.Transition{
		{state.Idle, state.Attached},
		{state.Failed, state.Idle},
		{state.Terminated, state.Idle},
		{state.Warming, state.Claimed},
	}
	for _, tr := range illegal {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			err := state.IsValid(tr.From, tr.To)
			if err == nil {
				t.Errorf("IsValid(%q, %q) returned nil, expected error", tr.From, tr.To)
			}
			var ite *state.InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Errorf("IsValid(%q, %q) returned %T, want *InvalidTransitionError", tr.From, tr.To, err)
			}
		})
	}
}
