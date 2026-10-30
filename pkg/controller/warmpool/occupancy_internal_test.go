// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// TestProjectOccupancyPhase exercises the §4.6.1 / §4.6.3 claim-driven
// occupancy projection table: the coarse Sandbox.status.phase computed from
// the per-pod SandboxClaim binding state, the rewarm stamp, and the pod's
// current phase. It covers the bound/recycling/reserved projections, the
// preConnect re-warm sdk_connecting leg, the terminal dispositions, the
// claim-deleted return-to-idle (reserved hold expiry) versus drain (claimed,
// one-session-only or orphan reclaim) edges, and the cases the projection
// leaves to the warm-fill writer.
//
// spec: 4.6.1 (occupancy projection), 6.2 (coarse pod state machine, recycle
// edges), 6.14 (SandboxClaim binding-state enumeration), 3.3 (ownership
// decomposition).
func TestProjectOccupancyPhase(t *testing.T) {
	tests := []struct {
		name     string
		in       occupancy
		wantPhse state.State
		wantOK   bool
	}{
		{
			name:     "bound claim projects claimed",
			in:       occupancy{Current: state.Idle, HasClaim: true, Binding: claimstate.Bound},
			wantPhse: state.Claimed,
			wantOK:   true,
		},
		{
			name:     "recycling without rewarm projects claimed (whole-pod scrub leg)",
			in:       occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Recycling},
			wantPhse: state.Claimed,
			wantOK:   true,
		},
		{
			name:     "recycling with rewarm stamp projects sdk_connecting (preConnect re-warm leg)",
			in:       occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Recycling, RewarmStarted: true},
			wantPhse: state.SDKConnecting,
			wantOK:   true,
		},
		{
			name:     "reserved claim projects reserved",
			in:       occupancy{Current: state.SDKConnecting, HasClaim: true, Binding: claimstate.Reserved},
			wantPhse: state.Reserved,
			wantOK:   true,
		},
		{
			name:     "released disposition projects draining",
			in:       occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Released},
			wantPhse: state.Draining,
			wantOK:   true,
		},
		{
			name:     "failed disposition projects draining",
			in:       occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Failed},
			wantPhse: state.Draining,
			wantOK:   true,
		},
		{
			name:   "claim with no binding state yet leaves the warm-fill phase",
			in:     occupancy{Current: state.Idle, HasClaim: true, Binding: ""},
			wantOK: false,
		},
		{
			name:     "claim deleted on a reserved pod returns to idle (recycling hold expiry)",
			in:       occupancy{Current: state.Reserved, HasClaim: false},
			wantPhse: state.Idle,
			wantOK:   true,
		},
		{
			name:     "claim deleted on a claimed (unscrubbed) pod drains (one-session-only / orphan reclaim)",
			in:       occupancy{Current: state.Claimed, HasClaim: false},
			wantPhse: state.Draining,
			wantOK:   true,
		},
		{
			name:   "no claim on an idle pod is the warm-fill writer's phase",
			in:     occupancy{Current: state.Idle, HasClaim: false},
			wantOK: false,
		},
		{
			name:   "no claim on a warming pod is the warm-fill writer's phase",
			in:     occupancy{Current: state.Warming, HasClaim: false},
			wantOK: false,
		},
		{
			name:   "no claim on a pre-idle sdk_connecting pod is the warm-fill writer's phase",
			in:     occupancy{Current: state.SDKConnecting, HasClaim: false},
			wantOK: false,
		},
		{
			name:   "no claim on a terminated pod is left to the teardown writer",
			in:     occupancy{Current: state.Terminated, HasClaim: false},
			wantOK: false,
		},
		{
			name:   "no claim on a draining pod is left to the teardown writer",
			in:     occupancy{Current: state.Draining, HasClaim: false},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPhase, gotOK := ProjectOccupancyPhase(tt.in)
			if gotOK != tt.wantOK {
				t.Fatalf("ProjectOccupancyPhase(%+v) ok = %v, want %v", tt.in, gotOK, tt.wantOK)
			}
			if tt.wantOK && gotPhase != tt.wantPhse {
				t.Errorf("ProjectOccupancyPhase(%+v) phase = %q, want %q", tt.in, gotPhase, tt.wantPhse)
			}
		})
	}
}

// TestProjectOccupancyLeavesTerminalPhases sweeps every claim shape over
// the terminal phases. failed leaves only through failed → draining, so a
// claim that outlives the pod's failure (for example a recycling claim
// whose SDK re-warm watchdog fired) must not pull the pod back into an
// occupancy phase and fight the Sandbox-to-Pod reconciler's reclaim. The
// one projection kept is a terminal claim disposition on a failed pod,
// which is that failed → draining edge. terminated is never re-projected.
//
// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle)).
func TestProjectOccupancyLeavesTerminalPhases(t *testing.T) {
	bindings := []claimstate.State{
		"", claimstate.Bound, claimstate.Recycling, claimstate.Reserved,
		claimstate.Released, claimstate.Failed,
	}
	for _, current := range []state.State{state.Failed, state.Terminated} {
		for _, hasClaim := range []bool{false, true} {
			for _, b := range bindings {
				for _, rewarm := range []bool{false, true} {
					in := occupancy{Current: current, HasClaim: hasClaim, Binding: b, RewarmStarted: rewarm}
					if !hasClaim {
						in.Binding, in.RewarmStarted = "", false
					}
					got, ok := ProjectOccupancyPhase(in)
					terminalDisposition := b == claimstate.Released || b == claimstate.Failed
					if current == state.Failed && hasClaim && terminalDisposition {
						if !ok || got != state.Draining {
							t.Errorf("ProjectOccupancyPhase(%+v) = %q, %v; want draining, true", in, got, ok)
						}
						continue
					}
					if ok {
						t.Errorf("ProjectOccupancyPhase(%+v) = %q, true; want the terminal phase left alone", in, got)
					}
				}
			}
		}
	}
}

// TestProjectedPhasesAreValidTransitions confirms every projected phase the
// table produces is a legal §6.2 coarse transition from the input phase, so
// the occupancy projection never writes an edge the state machine forbids.
//
// spec: 6.2 (coarse pod state machine), 4.6.1 (occupancy projection).
func TestProjectedPhasesAreValidTransitions(t *testing.T) {
	cases := []struct {
		in occupancy
	}{
		{occupancy{Current: state.Idle, HasClaim: true, Binding: claimstate.Bound}},
		{occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Recycling, RewarmStarted: true}},
		{occupancy{Current: state.SDKConnecting, HasClaim: true, Binding: claimstate.Reserved}},
		{occupancy{Current: state.Claimed, HasClaim: true, Binding: claimstate.Released}},
		{occupancy{Current: state.Claimed, HasClaim: false}},
		{occupancy{Current: state.Reserved, HasClaim: false}},
	}
	for _, tc := range cases {
		projected, ok := ProjectOccupancyPhase(tc.in)
		if !ok || projected == tc.in.Current {
			continue
		}
		if err := state.IsValid(tc.in.Current, projected); err != nil {
			t.Errorf("projection %q → %q is not a valid §6.2 transition: %v", tc.in.Current, projected, err)
		}
	}
}
