// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// TestCoarseState_spec_6_2 covers the §6.2 lines 305-313 coarse pod-state
// mapping: the lenny.dev/state pod label carries only idle/active/draining.
// idle maps to idle, draining to draining, every claimed/serving phase to
// active, and the pre-ready (warming, sdk_connecting) and terminal phases
// have no coarse value (so the reconciler omits the label).
func TestCoarseState_spec_6_2(t *testing.T) {
	cases := []struct {
		phase state.State
		want  string
		ok    bool
	}{
		{state.Idle, "idle", true},
		{state.Draining, "draining", true},
		{state.Claimed, "active", true},
		{state.ReceivingUploads, "active", true},
		{state.FinalizingWorkspace, "active", true},
		{state.RunningSetup, "active", true},
		{state.StartingSession, "active", true},
		{state.Attached, "active", true},
		{state.TaskCleanup, "active", true},
		{state.SlotActive, "active", true},
		{state.Resuming, "active", true},
		{state.Suspended, "active", true},
		{state.ResumePending, "active", true},
		{state.AwaitingClientAction, "active", true},
		// Pre-ready: not yet claimable, no coarse operational value.
		{state.Warming, "", false},
		{state.SDKConnecting, "", false},
		// Terminal: the pod is gone, no coarse value.
		{state.Completed, "", false},
		{state.Failed, "", false},
		{state.Cancelled, "", false},
		{state.Expired, "", false},
		{state.Terminated, "", false},
	}
	for _, tc := range cases {
		got, ok := state.CoarseState(tc.phase)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CoarseState(%q) = (%q, %v), want (%q, %v)", tc.phase, got, ok, tc.want, tc.ok)
		}
		// The label must only ever hold one of the three documented values.
		if ok && got != state.CoarseIdle && got != state.CoarseActive && got != state.CoarseDraining {
			t.Errorf("CoarseState(%q) = %q, which is outside the §6.2 line 309 value set", tc.phase, got)
		}
	}
}

// TestCoarseStateCoversEveryPhase_spec_6_2 guards against a new §6.2 phase
// silently defaulting to "no coarse value": every state in All() must be
// classified by CoarseState (the default branch is intentional only for
// the pre-ready and terminal phases enumerated above).
func TestCoarseStateCoversEveryPhase_spec_6_2(t *testing.T) {
	noCoarse := map[state.State]bool{
		state.Warming: true, state.SDKConnecting: true,
		state.Completed: true, state.Failed: true, state.Cancelled: true,
		state.Expired: true, state.Terminated: true,
	}
	for _, s := range state.All() {
		_, ok := state.CoarseState(s)
		if ok == noCoarse[s] {
			t.Errorf("CoarseState(%q) ok=%v, but noCoarse=%v — reclassify or update the test", s, ok, noCoarse[s])
		}
	}
}

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
// diagnosis: the §6.2 line 87 starting_session phase (between
// running_setup and attached on the pod-warm path) or one of its edges is
// missing from the Sandbox state machine. spec §6.2 lines 102-103 require
// starting_session → failed (retries exhausted) and starting_session →
// resume_pending (post-attached visibility, retries remain); line 87
// requires running_setup → starting_session → attached.
func TestStartingSessionPhaseAndTransitions_spec_6_2(t *testing.T) {
	t.Parallel()
	found := false
	for _, s := range state.All() {
		if s == state.StartingSession {
			found = true
		}
	}
	if !found {
		t.Fatal("state.All() is missing starting_session (spec §6.2 line 87)")
	}
	required := []state.Transition{
		{From: state.RunningSetup, To: state.StartingSession},  // §6.2 line 87
		{From: state.StartingSession, To: state.Attached},      // §6.2 line 87
		{From: state.StartingSession, To: state.Failed},        // §6.2 line 102
		{From: state.StartingSession, To: state.ResumePending}, // §6.2 line 103
	}
	for _, tr := range required {
		if err := state.IsValid(tr.From, tr.To); err != nil {
			t.Errorf("IsValid(%q, %q) = %v, want nil — §6.2 starting_session edge", tr.From, tr.To, err)
		}
	}
	if state.IsTerminal(state.StartingSession) {
		t.Error("starting_session must not be a terminal phase")
	}
}

// TestSessionTerminalDrainEdges_spec_6_2 covers the pod-reclamation drain
// edges from the §6.2 session-terminal phases. A session-mode pod is
// exclusive (§6.2 line 194), so once its session reaches a terminal phase the
// gateway records the disposition and then drains the pod for replacement.
// The terminal phases therefore carry an outgoing pod-cleanup edge to
// draining while remaining session-terminal for the §6.2 line 269 timer
// accounting; the two axes coexist.
func TestSessionTerminalDrainEdges_spec_6_2(t *testing.T) {
	t.Parallel()
	for _, from := range []state.State{state.Completed, state.Failed, state.Cancelled, state.Expired} {
		from := from
		t.Run(string(from), func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(from, state.Draining); err != nil {
				t.Errorf("IsValid(%q, draining) = %v, want nil — session-terminal pod reclamation edge", from, err)
			}
			// The drain edge does not make the phase non-terminal: the
			// session-timer axis is independent of the pod-cleanup axis.
			if !state.IsTerminal(from) {
				t.Errorf("IsTerminal(%q) = false, want true — drain edge must not flip session terminality", from)
			}
			// The drain is the only outgoing edge: a terminal phase still
			// cannot return to the claimable set.
			if err := state.IsValid(from, state.Idle); err == nil {
				t.Errorf("IsValid(%q, idle) = nil, want error — a terminal phase must not return to idle", from)
			}
		})
	}
}

// spec: §6.2 line 146 — cancelled → task_cleanup is the task-mode
// cancellation edge: an acknowledged cancellation runs the same scrub as
// a completed task, then rejoins or retires per the task_cleanup
// disposition. The edge leaves a session-terminal phase (cancelled stays
// terminal), exactly like the cancelled → draining reclamation edge.
func TestCancelledTaskCleanupEdge_spec_6_2(t *testing.T) {
	t.Parallel()
	if err := state.IsValid(state.Cancelled, state.TaskCleanup); err != nil {
		t.Errorf("IsValid(cancelled, task_cleanup) = %v, want nil — §6.2 line 146 task-mode cancellation edge", err)
	}
	if !state.IsTerminal(state.Cancelled) {
		t.Errorf("IsTerminal(cancelled) = false, want true — the task_cleanup edge must not flip session terminality")
	}
	// task_cleanup advances to exactly the four §6.2 disposition phases.
	for _, to := range []state.State{state.Idle, state.SDKConnecting, state.Draining, state.Failed} {
		if err := state.IsValid(state.TaskCleanup, to); err != nil {
			t.Errorf("IsValid(task_cleanup, %q) = %v, want nil", to, err)
		}
	}
}

// spec: 6.2
// diagnosis: state.IsTerminal returned the wrong value. Sandbox terminal
//
//	states per spec §6.2 are the four session-terminal states (line 269:
//	completed, failed, cancelled, expired) plus the pod-lifecycle terminal
//	terminated. completed is terminal (§6.2 lines 96-99 attached/suspended
//	→ completed with no edge back out). Pods in `draining` are not terminal.
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
		{state.Terminated, true},
		{state.Draining, false},
		{state.Idle, false},
		{state.Suspended, false},
		{state.StartingSession, false},
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
		// spec §6.2 line 87: the pod-warm chain routes running_setup →
		// starting_session → attached, so a direct running_setup → attached
		// edge is no longer valid.
		{state.RunningSetup, state.Attached},
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
