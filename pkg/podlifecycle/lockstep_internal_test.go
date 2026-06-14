// SPDX-License-Identifier: MIT

package podlifecycle

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// edge is a from→to pair over the coarse §6.2 phase strings, used to compare
// the two authoritative copies of the state machine.
type edge struct{ from, to string }

// allowedTransitionEdges enumerates every (from, to) pair over the coarse
// §6.2 phases that allowedTransition admits. It is the podlifecycle mirror's
// edge set, derived by exhaustive enumeration so the comparison does not
// depend on knowing allowedTransition's internal map shape.
func allowedTransitionEdges() map[edge]struct{} {
	phases := []PodState{
		PodStateWarming, PodStateSDKConnecting, PodStateIdle,
		PodStateReserved, PodStateClaimed, PodStateFailed,
		PodStateDraining, PodStateTerminated,
	}
	out := map[edge]struct{}{}
	for _, f := range phases {
		for _, t := range phases {
			if allowedTransition(f, t) {
				out[edge{string(f), string(t)}] = struct{}{}
			}
		}
	}
	return out
}

// TestPodStateMirrorIsLockstepWithStateMachine_spec_6_2 asserts the two
// authoritative copies of the §6.2 coarse pod-state machine enumerate the
// same edge set: pkg/sandbox/state.ValidTransitions() (the canonical list)
// and the podlifecycle allowedTransition mirror. A drift between them would
// let one layer accept an edge the other rejects.
//
// spec: 6.2 (coarse pod state machine, recycle edges), 6.37 (fine session/
// setup phases move to the Postgres session model)
// diagnosis: the podlifecycle allowedTransition mirror has drifted from
// pkg/sandbox/state.ValidTransitions(). One layer admits a coarse §6.2 edge
// the other rejects; re-key both to the same edge set.
func TestPodStateMirrorIsLockstepWithStateMachine_spec_6_2(t *testing.T) {
	canonical := map[edge]struct{}{}
	for _, tr := range state.ValidTransitions() {
		canonical[edge{string(tr.From), string(tr.To)}] = struct{}{}
	}
	mirror := allowedTransitionEdges()

	for e := range canonical {
		if _, ok := mirror[e]; !ok {
			t.Errorf("allowedTransition rejects %q→%q but ValidTransitions() admits it", e.from, e.to)
		}
	}
	for e := range mirror {
		if _, ok := canonical[e]; !ok {
			t.Errorf("allowedTransition admits %q→%q but ValidTransitions() rejects it", e.from, e.to)
		}
	}
}

// TestPodStateMirrorRejectsRemovedPhases_spec_6_2 asserts that neither the
// removed concurrent-occupancy phase (slot_active) nor the fine session/setup
// phases that moved to the Postgres session model (spec §6.2, §6.37) name any
// edge in the podlifecycle mirror. Every coarse phase the mirror admits must
// be one of the eight §6.2 occupancy phases.
//
// spec: 6.2, 6.37
// diagnosis: the podlifecycle allowedTransition mirror still names a removed
// phase (slot_active, task_cleanup, attached, or another fine session/setup
// state). Those are no longer coarse CRD phases; fold them out of the mirror.
func TestPodStateMirrorRejectsRemovedPhases_spec_6_2(t *testing.T) {
	coarse := map[string]struct{}{
		"warming": {}, "sdk_connecting": {}, "idle": {}, "reserved": {},
		"claimed": {}, "failed": {}, "draining": {}, "terminated": {},
	}
	removed := []PodState{
		"slot_active", "receiving_uploads", "finalizing_workspace",
		"running_setup", "starting_session", "attached", "task_cleanup",
		"resuming", "suspended", "resume_pending", "awaiting_client_action",
		"completed", "cancelled", "expired",
	}
	for e := range allowedTransitionEdges() {
		if _, ok := coarse[e.from]; !ok {
			t.Errorf("allowedTransition source %q is not a coarse §6.2 phase", e.from)
		}
		if _, ok := coarse[e.to]; !ok {
			t.Errorf("allowedTransition target %q is not a coarse §6.2 phase", e.to)
		}
	}
	for _, r := range removed {
		for _, other := range []PodState{PodStateIdle, PodStateClaimed, PodStateReserved, PodStateDraining} {
			if allowedTransition(r, other) {
				t.Errorf("allowedTransition(%q, %q) = true, want false — %q is a removed phase", r, other, r)
			}
			if allowedTransition(other, r) {
				t.Errorf("allowedTransition(%q, %q) = true, want false — %q is a removed phase", other, r, r)
			}
		}
	}
}
