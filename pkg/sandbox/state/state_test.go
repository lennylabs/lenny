// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// TestCoarseState_spec_6_2 covers the §6.2 coarse pod-state mapping: the
// lenny.dev/state pod label carries only idle/active/draining. idle maps to
// idle, draining to draining, the occupied phases (claimed, reserved) to
// active, and the pre-ready (warming, sdk_connecting) and terminal (failed,
// terminated) phases have no coarse value (so the reconciler omits the
// label). reserved maps to active: a recycled pod held for its pinned tenant
// is excluded from idle inventory, so claimed and reserved both project
// active and the label never leaves active across the recycle and hold
// window (spec §6.2 "reserved hold semantics"). sdk_connecting stays
// unmapped on the recycle SDK re-warm leg exactly as it is on the warm-fill
// leg, because the phase cannot distinguish an occupied recycling pod from
// unclaimed pre-idle inventory (spec §6.2).
func TestCoarseState_spec_6_2(t *testing.T) {
	cases := []struct {
		phase state.State
		want  string
		ok    bool
	}{
		{state.Idle, "idle", true},
		{state.Draining, "draining", true},
		// reserved: recycled pod held for its pinned tenant, excluded from
		// idle inventory, so the PDB idle selector must not match it.
		{state.Reserved, "active", true},
		{state.Claimed, "active", true},
		// Pre-ready: not yet claimable, no coarse operational value.
		{state.Warming, "", false},
		// sdk_connecting is unmapped on both the warm-fill leg and the
		// recycle SDK re-warm leg, so the reconciler removes the label in
		// both windows. spec §6.2.
		{state.SDKConnecting, "", false},
		// Terminal: the pod is gone or being reclaimed, no coarse value.
		{state.Failed, "", false},
		{state.Terminated, "", false},
	}
	for _, tc := range cases {
		got, ok := state.CoarseState(tc.phase)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CoarseState(%q) = (%q, %v), want (%q, %v)", tc.phase, got, ok, tc.want, tc.ok)
		}
		// The label must only ever hold one of the three documented values.
		if ok && got != state.CoarseIdle && got != state.CoarseActive && got != state.CoarseDraining {
			t.Errorf("CoarseState(%q) = %q, which is outside the §6.2 value set", tc.phase, got)
		}
	}
}

// TestCoarseStateReservedAndSDKConnecting_spec_6_2 pins the two §6.2
// recycle-window mappings this proposal turns on. reserved projects active
// exactly as claimed does, so the §4.6.1 PDB idle selector never matches a
// held recycled pod and the label does not oscillate as same-tenant
// sessions rebind within the hold; sdk_connecting stays unmapped on the
// recycle SDK re-warm leg exactly as on the warm-fill leg, so the
// reconciler removes the label in that window because the phase cannot
// distinguish an occupied recycling pod from unclaimed pre-idle inventory.
func TestCoarseStateReservedAndSDKConnecting_spec_6_2(t *testing.T) {
	t.Parallel()
	reserved, reservedOK := state.CoarseState(state.Reserved)
	claimed, claimedOK := state.CoarseState(state.Claimed)
	if !reservedOK || reserved != state.CoarseActive {
		t.Errorf("CoarseState(reserved) = (%q, %v), want (%q, true)", reserved, reservedOK, state.CoarseActive)
	}
	if reserved != claimed || reservedOK != claimedOK {
		t.Errorf("CoarseState(reserved) = (%q, %v), want parity with CoarseState(claimed) = (%q, %v)",
			reserved, reservedOK, claimed, claimedOK)
	}
	if got, ok := state.CoarseState(state.SDKConnecting); ok || got != "" {
		t.Errorf("CoarseState(sdk_connecting) = (%q, %v), want (\"\", false) — unmapped on warm-fill and re-warm legs", got, ok)
	}
}

// TestConcurrentOccupancyCollapsesToClaimed_spec_6_2 pins the removal of the
// former slot_active phase. With the coarse occupancy enum the phase carries
// no separate concurrent-occupancy value: a pod serving any number of
// concurrent sessions projects claimed, which maps to active, so concurrency
// is observable through the Redis slot counter and metrics rather than a
// distinct phase or pod-label value. The phase string "slot_active" must
// therefore not appear in All(), must carry no coarse mapping, and must have
// no valid transitions. The claimed → claimed self-edge represents an
// additional or completing session while occupancy stays nonzero.
func TestConcurrentOccupancyCollapsesToClaimed_spec_6_2(t *testing.T) {
	t.Parallel()
	const slotActive state.State = "slot_active"
	for _, s := range state.All() {
		if s == slotActive {
			t.Fatal("state.All() still contains slot_active — concurrent occupancy must collapse to claimed")
		}
	}
	if got, ok := state.CoarseState(slotActive); ok || got != "" {
		t.Errorf("CoarseState(slot_active) = (%q, %v), want (\"\", false) — the phase no longer exists", got, ok)
	}
	// No transition may name slot_active as a source or target.
	for _, tr := range state.ValidTransitions() {
		if tr.From == slotActive || tr.To == slotActive {
			t.Errorf("ValidTransitions() still contains a slot_active edge %q → %q", tr.From, tr.To)
		}
	}
	// Concurrent occupancy projects claimed (active) and cycles through the
	// claimed → claimed self-edge as sessions are added or complete.
	if got, ok := state.CoarseState(state.Claimed); !ok || got != state.CoarseActive {
		t.Errorf("CoarseState(claimed) = (%q, %v), want (%q, true) — concurrent occupancy collapses to claimed→active", got, ok, state.CoarseActive)
	}
	if err := state.IsValid(state.Claimed, state.Claimed); err != nil {
		t.Errorf("IsValid(claimed, claimed) = %v, want nil — concurrent-occupancy self-edge", err)
	}
}

// TestCoarseEnumIsTheAuthoritativeSet_spec_6_2 pins that All() is exactly the
// §6.2 coarse pod-occupancy enum and that the fine session/setup states the
// proposal moved to the Postgres session model (spec §6.2, §6.37) are no
// longer coarse CRD phases: they carry no coarse mapping and no transition.
func TestCoarseEnumIsTheAuthoritativeSet_spec_6_2(t *testing.T) {
	t.Parallel()
	want := map[state.State]bool{
		state.Warming: true, state.SDKConnecting: true, state.Idle: true,
		state.Reserved: true, state.Claimed: true, state.Failed: true,
		state.Draining: true, state.Terminated: true,
	}
	got := map[state.State]bool{}
	for _, s := range state.All() {
		got[s] = true
	}
	if len(got) != len(want) {
		t.Fatalf("All() = %v, want exactly the %d coarse §6.2 phases", got, len(want))
	}
	for s := range want {
		if !got[s] {
			t.Errorf("All() is missing coarse phase %q", s)
		}
	}
	// The fine session/setup states moved to the Postgres session model.
	removed := []state.State{
		"receiving_uploads", "finalizing_workspace", "running_setup",
		"starting_session", "attached", "task_cleanup", "resuming",
		"suspended", "resume_pending", "awaiting_client_action",
		"completed", "cancelled", "expired",
	}
	for _, s := range removed {
		if got[s] {
			t.Errorf("All() still contains removed fine state %q — it moved to the Postgres session model", s)
		}
		if _, ok := state.CoarseState(s); ok {
			t.Errorf("CoarseState(%q) is mapped — the fine state must carry no coarse label value", s)
		}
		for _, tr := range state.ValidTransitions() {
			if tr.From == s || tr.To == s {
				t.Errorf("ValidTransitions() still contains a %q edge %q → %q — the fine state must have no CRD edge", s, tr.From, tr.To)
			}
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
		state.Failed: true, state.Terminated: true,
	}
	for _, s := range state.All() {
		_, ok := state.CoarseState(s)
		if ok == noCoarse[s] {
			t.Errorf("CoarseState(%q) ok=%v, but noCoarse=%v — reclassify or update the test", s, ok, noCoarse[s])
		}
	}
}

// TestRecycleEdges_spec_6_2 covers the §6.2 recycle edges this proposal adds:
// the occupancy-zero re-warm (claimed → sdk_connecting on preConnect pools),
// the non-preConnect reserve (claimed → reserved), the re-warm completion
// (sdk_connecting → reserved), the same-tenant rebind (reserved → claimed),
// the hold-expiry return to idle (reserved → idle), and the retire edge
// (claimed → draining). spec §6.2 "Recycle edges".
func TestRecycleEdges_spec_6_2(t *testing.T) {
	t.Parallel()
	edges := []state.Transition{
		{From: state.Claimed, To: state.SDKConnecting},
		{From: state.Claimed, To: state.Reserved},
		{From: state.SDKConnecting, To: state.Reserved},
		{From: state.Reserved, To: state.Claimed},
		{From: state.Reserved, To: state.Idle},
		{From: state.Claimed, To: state.Draining},
	}
	for _, tr := range edges {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil — §6.2 recycle edge", tr.From, tr.To, err)
			}
		})
	}
}

// spec: 6.2
// diagnosis: state.IsTerminal returned the wrong value. The coarse §6.2
//
//	terminal phases are failed (warm-fill / re-warm failure) and terminated
//	(pod-lifecycle terminal). The fine session-terminal states moved to the
//	Postgres session model. Pods in draining or any live occupancy phase are
//	not terminal.
func TestIsTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    state.State
		want bool
	}{
		{state.Failed, true},
		{state.Terminated, true},
		{state.Draining, false},
		{state.Idle, false},
		{state.Claimed, false},
		{state.Reserved, false},
		{state.Warming, false},
		{state.SDKConnecting, false},
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
//	ValidTransitions(). The coarse §6.2 state machine forbids these edges,
//	including any edge naming a removed fine session/setup state.
func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	t.Parallel()
	illegal := []state.Transition{
		{state.Idle, state.Reserved},
		{state.Failed, state.Idle},
		{state.Terminated, state.Idle},
		{state.Warming, state.Claimed},
		// A terminal phase cannot return to the claimable set.
		{state.Terminated, state.Draining},
		// The fine session/setup states are not CRD phases, so no edge names
		// them (spec §6.2, §6.37).
		{state.Claimed, "attached"},
		{state.Claimed, "task_cleanup"},
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
