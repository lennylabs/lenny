// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name  string
		phase state.State
		pod   lifecycle.PodObservation
		want  lifecycle.Decision
	}{
		{
			name:  "empty phase with no pod creates the pod",
			phase: "",
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionCreatePod},
		},
		{
			name:  "warming with no pod creates the pod",
			phase: state.Warming,
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionCreatePod},
		},
		{
			name:  "warming with a pending pod waits",
			phase: state.Warming,
			pod:   lifecycle.PodPending,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "warming with a not-ready pod waits",
			phase: state.Warming,
			pod:   lifecycle.PodNotReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "warming with a ready pod advances to idle",
			phase: state.Warming,
			pod:   lifecycle.PodReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Idle},
		},
		{
			name:  "warming with a failed pod advances to failed",
			phase: state.Warming,
			pod:   lifecycle.PodFailed,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name:  "warming with a pod that exited 0 advances to failed",
			phase: state.Warming,
			pod:   lifecycle.PodSucceeded,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name:  "idle with a ready pod stays warm",
			phase: state.Idle,
			pod:   lifecycle.PodReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "idle with a transient not-ready pod waits",
			phase: state.Idle,
			pod:   lifecycle.PodNotReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "idle with a vanished pod drains the sandbox",
			phase: state.Idle,
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining},
		},
		{
			name:  "idle with a failed pod drains the sandbox",
			phase: state.Idle,
			pod:   lifecycle.PodFailed,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining},
		},
		{
			name:  "draining with a live pod deletes the pod",
			phase: state.Draining,
			pod:   lifecycle.PodReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionDeletePod},
		},
		{
			name:  "draining with a pending pod deletes the pod",
			phase: state.Draining,
			pod:   lifecycle.PodPending,
			want:  lifecycle.Decision{Action: lifecycle.ActionDeletePod},
		},
		{
			name:  "draining with no pod advances to terminated",
			phase: state.Draining,
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Terminated},
		},
		{
			name:  "failed with no pod is reclaimed through draining",
			phase: state.Failed,
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining},
		},
		{
			name:  "terminated takes no action",
			phase: state.Terminated,
			pod:   lifecycle.PodAbsent,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "a gateway-owned claimed phase takes no action",
			phase: state.Claimed,
			pod:   lifecycle.PodReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
		{
			name:  "a gateway-owned reserved phase takes no action",
			phase: state.Reserved,
			pod:   lifecycle.PodReady,
			want:  lifecycle.Decision{Action: lifecycle.ActionNone},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lifecycle.Decide(tc.phase, tc.pod)
			if got != tc.want {
				t.Errorf("Decide(%q, %v) = %+v, want %+v", tc.phase, tc.pod, got, tc.want)
			}
		})
	}
}

// TestDecideOnlyEmitsValidTransitions sweeps every phase and pod
// observation and confirms each ActionSetPhase decision is a §6.2
// transition the state package recognizes, so the planner can never
// drive the Sandbox along an edge the authoritative machine rejects.
func TestDecideOnlyEmitsValidTransitions(t *testing.T) {
	observations := []lifecycle.PodObservation{
		lifecycle.PodAbsent,
		lifecycle.PodPending,
		lifecycle.PodReady,
		lifecycle.PodNotReady,
		lifecycle.PodFailed,
		lifecycle.PodSucceeded,
		lifecycle.PodTerminating,
	}
	for _, phase := range state.All() {
		for _, obs := range observations {
			d := lifecycle.Decide(phase, obs)
			if d.Action != lifecycle.ActionSetPhase {
				continue
			}
			if err := state.IsValid(phase, d.NextPhase); err != nil {
				t.Errorf("Decide(%q, %v) emits invalid transition %q → %q: %v",
					phase, obs, phase, d.NextPhase, err)
			}
		}
	}
}

// allObservations is every reduced Pod observation the reconciler emits.
var allObservations = []lifecycle.PodObservation{
	lifecycle.PodAbsent,
	lifecycle.PodPending,
	lifecycle.PodReady,
	lifecycle.PodNotReady,
	lifecycle.PodFailed,
	lifecycle.PodSucceeded,
	lifecycle.PodTerminating,
}

// spec: 6.2 (Pod State Machine)
//
// TestDecideFailedIsReclaimedNotASink pins the §6.2 failed → draining
// edge ("failed pod reclaimed and replaced"): a failed Sandbox drains
// whatever its pod looks like, and the draining arm then deletes the pod
// and reaches terminated. Before the fix the failed phase was a sink and
// a failed pod held its node resources indefinitely.
func TestDecideFailedIsReclaimedNotASink(t *testing.T) {
	want := lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining}
	for _, obs := range allObservations {
		if d := lifecycle.Decide(state.Failed, obs); d != want {
			t.Errorf("Decide(failed, %v) = %+v, want %+v", obs, d, want)
		}
		// The SDK-warm planner shares the reclaim.
		if got := lifecycle.DecideSDKWarm(lifecycle.SDKWarmInputs{Phase: state.Failed, Pod: obs}); got != want {
			t.Errorf("DecideSDKWarm(failed, %v) = %+v, want %+v", obs, got, want)
		}
	}
	// Follow the reclaim to its end: draining deletes a live pod and
	// advances to terminated once the pod is gone; terminated is final.
	if d := lifecycle.Decide(state.Draining, lifecycle.PodFailed); d.Action != lifecycle.ActionDeletePod {
		t.Errorf("Decide(draining, PodFailed) = %+v, want ActionDeletePod", d)
	}
	if d := lifecycle.Decide(state.Draining, lifecycle.PodAbsent); d.NextPhase != state.Terminated {
		t.Errorf("Decide(draining, PodAbsent) = %+v, want terminated", d)
	}
	for _, obs := range allObservations {
		if d := lifecycle.Decide(state.Terminated, obs); d.Action != lifecycle.ActionNone {
			t.Errorf("Decide(terminated, %v) = %+v, want no action", obs, d)
		}
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestHoldFailedReclaim pins the claim gate on the failed → draining
// reclaim: a failed Sandbox whose per-pod SandboxClaim still exists is
// left for the claim's release path, and every other phase and decision
// passes through unchanged whether or not a claim exists.
func TestHoldFailedReclaim(t *testing.T) {
	for _, phase := range state.All() {
		for _, obs := range allObservations {
			for _, claim := range []bool{false, true} {
				d := lifecycle.Decide(phase, obs)
				got := lifecycle.HoldFailedReclaim(phase, claim, d)
				want := d
				if phase == state.Failed && claim {
					want = lifecycle.Decision{}
				}
				if got != want {
					t.Errorf("HoldFailedReclaim(%q, claim=%v, Decide(%q, %v)) = %+v, want %+v",
						phase, claim, phase, obs, got, want)
				}
			}
		}
	}
}

// spec: 6.2 (Pod State Machine)
//
// TestDecideNeverActsOnOccupiedPods pins that the warm-path planner
// never deletes or re-phases a pod in a gateway-owned occupancy phase,
// even when the pod is observed failed (for example its adapter exited):
// a claimed or reserved pod retires through the gateway's release path.
func TestDecideNeverActsOnOccupiedPods(t *testing.T) {
	for _, phase := range []state.State{state.Claimed, state.Reserved} {
		for _, obs := range allObservations {
			if d := lifecycle.Decide(phase, obs); d.Action != lifecycle.ActionNone {
				t.Errorf("Decide(%q, %v) = %+v, want no action", phase, obs, d)
			}
		}
	}
}

// spec: 6.2 (Pod State Machine)
//
// TestDecideTerminatingPodNeverFails pins the deletion-timestamp
// dimension of both planners across every phase, both SDK-warm edges, and
// claim presence. A pod with a deletion timestamp is torn down by
// Kubernetes and receives SIGTERM, which §6.2 treats as an expected
// lifecycle signal: sdk_connecting takes sdk_connecting → terminated on
// both the warm-fill and the recycle re-warm leg; warming holds (no edge
// to idle, sdk_connecting, or failed, and no pod create while the old pod
// lingers); idle retires through draining; draining waits for the pod to
// go without re-issuing the delete; failed keeps its claim-gated reclaim;
// and claimed, reserved, and terminated take no action. No phase emits
// failed, and every emitted edge is a valid §6.2 transition.
func TestDecideTerminatingPodNeverFails(t *testing.T) {
	none := lifecycle.Decision{}
	set := func(p state.State) lifecycle.Decision {
		return lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: p}
	}
	podWarm := map[state.State]lifecycle.Decision{
		"":                  none,
		state.Warming:       none,
		state.SDKConnecting: none,
		state.Idle:          set(state.Draining),
		state.Reserved:      none,
		state.Claimed:       none,
		state.Failed:        set(state.Draining),
		state.Draining:      none,
		state.Terminated:    none,
	}
	sdkWarm := map[state.State]lifecycle.Decision{}
	for k, v := range podWarm {
		sdkWarm[k] = v
	}
	sdkWarm[state.SDKConnecting] = set(state.Terminated)

	phases := append([]state.State{""}, state.All()...)
	for _, phase := range phases {
		for _, claim := range []bool{false, true} {
			check := func(planner string, d, want lifecycle.Decision) {
				t.Helper()
				if claim && phase == state.Failed {
					want = none // the failed reclaim is held while a claim exists
				}
				if d != want {
					t.Errorf("%s(%q, PodTerminating, claim=%v) = %+v, want %+v", planner, phase, claim, d, want)
				}
				if d.Action == lifecycle.ActionSetPhase {
					if d.NextPhase == state.Failed {
						t.Errorf("%s(%q, PodTerminating) routes a deleted pod through failed", planner, phase)
					}
					if phase != "" {
						if err := state.IsValid(phase, d.NextPhase); err != nil {
							t.Errorf("%s(%q, PodTerminating): %v", planner, phase, err)
						}
					}
				}
				if d.Action == lifecycle.ActionCreatePod {
					t.Errorf("%s(%q, PodTerminating) creates a pod while the old one terminates", planner, phase)
				}
			}
			check("Decide", lifecycle.HoldFailedReclaim(phase, claim, lifecycle.Decide(phase, lifecycle.PodTerminating)), podWarm[phase])
			for _, recycle := range []bool{false, true} {
				// The elapsed time is past the watchdog so a terminating pod
				// is shown never to be reported as a watchdog timeout.
				in := lifecycle.SDKWarmInputs{
					Phase: phase, Pod: lifecycle.PodTerminating, Recycle: recycle,
					SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: 60 * time.Second,
				}
				if in.TimedOut() {
					t.Errorf("TimedOut(%q, PodTerminating, recycle=%v) = true, want false", phase, recycle)
				}
				check("DecideSDKWarm", lifecycle.HoldFailedReclaim(phase, claim, lifecycle.DecideSDKWarm(in)), sdkWarm[phase])
			}
		}
	}
}

// spec: 6.2 (Pod State Machine)
//
// TestTerminatingWarmPodLifecycleWalk follows a deleted pod through the
// planner to its terminus. An SDK-warm pod deleted in sdk_connecting ends
// terminated in one step. An idle pod deleted drains, waits while the pod
// terminates, and reaches terminated once it is gone. A warming pod holds
// while terminating and re-creates its pod once the old one is gone. None
// of the walks visits failed.
func TestTerminatingWarmPodLifecycleWalk(t *testing.T) {
	type step struct {
		obs  lifecycle.PodObservation
		want lifecycle.Decision
	}
	walk := func(name string, start state.State, sdk bool, steps []step) {
		t.Helper()
		phase := start
		for i, st := range steps {
			var d lifecycle.Decision
			if sdk {
				d = lifecycle.DecideSDKWarm(lifecycle.SDKWarmInputs{Phase: phase, Pod: st.obs})
			} else {
				d = lifecycle.Decide(phase, st.obs)
			}
			if d != st.want {
				t.Fatalf("%s step %d: decide(%q, %v) = %+v, want %+v", name, i, phase, st.obs, d, st.want)
			}
			if d.Action == lifecycle.ActionSetPhase {
				if d.NextPhase == state.Failed {
					t.Fatalf("%s step %d: walk visits failed", name, i)
				}
				phase = d.NextPhase
			}
		}
	}
	none := lifecycle.Decision{}
	set := func(p state.State) lifecycle.Decision {
		return lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: p}
	}
	walk("sdk_connecting deleted", state.SDKConnecting, true, []step{
		{lifecycle.PodTerminating, set(state.Terminated)},
		{lifecycle.PodTerminating, none},
		{lifecycle.PodAbsent, none},
	})
	walk("idle deleted", state.Idle, false, []step{
		{lifecycle.PodTerminating, set(state.Draining)},
		{lifecycle.PodTerminating, none},
		{lifecycle.PodAbsent, set(state.Terminated)},
		{lifecycle.PodAbsent, none},
	})
	walk("warming deleted", state.Warming, false, []step{
		{lifecycle.PodTerminating, none},
		{lifecycle.PodAbsent, lifecycle.Decision{Action: lifecycle.ActionCreatePod}},
	})
	walk("sdk-warm warming deleted", state.Warming, true, []step{
		{lifecycle.PodTerminating, none},
		{lifecycle.PodAbsent, lifecycle.Decision{Action: lifecycle.ActionCreatePod}},
	})
}
