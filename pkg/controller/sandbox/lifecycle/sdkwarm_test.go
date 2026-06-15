// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §6.1 (sdk_connecting watchdog, reserved terminus), §6.2 lines
// 89-123, §3.3 — the SDK-warm warm path routes through sdk_connecting with
// a watchdog. The phase has two non-failure termini: idle on the warm-fill
// edge (this arm writes it) and reserved on the recycle re-warm edge (the
// claim projection writes it, so this arm makes a clean no-action exit).
func TestDecideSDKWarm_spec_6_1(t *testing.T) {
	const timeout = 60 * time.Second
	tests := []struct {
		name string
		in   lifecycle.SDKWarmInputs
		want lifecycle.Decision
	}{
		{
			name: "warming with no pod creates the pod",
			in:   lifecycle.SDKWarmInputs{Phase: state.Warming, Pod: lifecycle.PodAbsent},
			want: lifecycle.Decision{Action: lifecycle.ActionCreatePod},
		},
		{
			name: "warming with pending pod waits",
			in:   lifecycle.SDKWarmInputs{Phase: state.Warming, Pod: lifecycle.PodPending},
			want: lifecycle.Decision{},
		},
		{
			name: "warming with running pod enters sdk_connecting",
			in:   lifecycle.SDKWarmInputs{Phase: state.Warming, Pod: lifecycle.PodNotReady},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.SDKConnecting},
		},
		{
			name: "empty phase with running pod enters sdk_connecting",
			in:   lifecycle.SDKWarmInputs{Phase: "", Pod: lifecycle.PodReady},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.SDKConnecting},
		},
		{
			name: "warming with crashed pod fails",
			in:   lifecycle.SDKWarmInputs{Phase: state.Warming, Pod: lifecycle.PodFailed},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name: "sdk_connecting still connecting within budget waits",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 10 * time.Second, SDKConnectTimeout: timeout,
			},
			want: lifecycle.Decision{},
		},
		{
			name: "sdk_connecting becomes idle when ready (warm-fill edge)",
			in:   lifecycle.SDKWarmInputs{Phase: state.SDKConnecting, Pod: lifecycle.PodReady},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Idle},
		},
		{
			// On the recycle re-warm edge the success terminus is reserved,
			// written by the claim projection (OccupancyReconciler) when the
			// gateway patches the claim recycling → reserved. The warm-fill
			// arm takes no action on a ready pod so the two arms do not fight
			// over the phase: sdk_connecting → reserved is a clean exit here.
			name: "sdk_connecting ready on recycle edge takes no action (reserved terminus owned by claim projection)",
			in:   lifecycle.SDKWarmInputs{Phase: state.SDKConnecting, Pod: lifecycle.PodReady, Recycle: true},
			want: lifecycle.Decision{},
		},
		{
			name: "sdk_connecting past watchdog budget fails (warm-fill edge)",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: timeout,
			},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			// The re-warm watchdog fires on the recycle edge exactly as on
			// the warm-fill edge; the elapsed clock the reconciler supplies
			// is measured from rewarmStartedAt, but the firing decision is
			// the same.
			name: "sdk_connecting past watchdog budget fails on recycle edge (re-warm watchdog)",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: timeout, Recycle: true,
			},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name: "sdk_connecting still connecting within budget on recycle edge waits",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 10 * time.Second, SDKConnectTimeout: timeout, Recycle: true,
			},
			want: lifecycle.Decision{},
		},
		{
			name: "sdk_connecting with dead pod fails on recycle edge",
			in:   lifecycle.SDKWarmInputs{Phase: state.SDKConnecting, Pod: lifecycle.PodFailed, Recycle: true},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name: "sdk_connecting with zero timeout never times out",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 10 * time.Hour, SDKConnectTimeout: 0,
			},
			want: lifecycle.Decision{},
		},
		{
			name: "sdk_connecting with dead pod fails",
			in:   lifecycle.SDKWarmInputs{Phase: state.SDKConnecting, Pod: lifecycle.PodFailed},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Failed},
		},
		{
			name: "idle with dead pod retires through draining (shared pod-warm logic)",
			in:   lifecycle.SDKWarmInputs{Phase: state.Idle, Pod: lifecycle.PodAbsent},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Draining},
		},
		{
			name: "draining with absent pod terminates (shared pod-warm logic)",
			in:   lifecycle.SDKWarmInputs{Phase: state.Draining, Pod: lifecycle.PodAbsent},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Terminated},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lifecycle.DecideSDKWarm(tt.in); got != tt.want {
				t.Errorf("DecideSDKWarm(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// spec: §6.1 line 69 — TimedOut isolates the watchdog transition so the
// reconciler emits the timeout counter only when the SDK hung, not when
// the pod genuinely failed.
func TestSDKWarmInputs_TimedOut_spec_6_1(t *testing.T) {
	timedOut := lifecycle.SDKWarmInputs{
		Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
		SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: 60 * time.Second,
	}
	if !timedOut.TimedOut() {
		t.Fatalf("expected TimedOut for an over-budget sdk_connecting pod")
	}
	// A genuine pod failure is not a watchdog timeout.
	podFailed := lifecycle.SDKWarmInputs{
		Phase: state.SDKConnecting, Pod: lifecycle.PodFailed,
		SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: 60 * time.Second,
	}
	if podFailed.TimedOut() {
		t.Fatalf("a failed pod is not a watchdog timeout")
	}
	// Within budget is not a timeout.
	within := lifecycle.SDKWarmInputs{
		Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
		SDKConnectElapsed: 10 * time.Second, SDKConnectTimeout: 60 * time.Second,
	}
	if within.TimedOut() {
		t.Fatalf("within-budget pod must not report a timeout")
	}
	// The re-warm watchdog fires on the recycle edge identically: TimedOut
	// is edge-agnostic (the reconciler re-anchors the elapsed clock per
	// edge, not the firing predicate). spec: §6.1, §3.3.
	rewarmTimedOut := lifecycle.SDKWarmInputs{
		Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
		SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: 60 * time.Second, Recycle: true,
	}
	if !rewarmTimedOut.TimedOut() {
		t.Fatalf("expected TimedOut for an over-budget recycle re-warm leg")
	}
}

// TestDecideSDKWarmOnlyEmitsValidTransitions sweeps every phase and pod
// observation across both the warm-fill and the recycle re-warm edge and
// confirms each ActionSetPhase decision is a §6.2 transition the state
// package recognizes, so neither leg can drive the Sandbox along an edge
// the authoritative machine rejects (the recycle edge adds the reserved
// terminus and the sdk_connecting → reserved clean exit).
//
// spec: §6.1 (reserved terminus), §6.2 (recycle edges), §3.3
func TestDecideSDKWarmOnlyEmitsValidTransitions(t *testing.T) {
	observations := []lifecycle.PodObservation{
		lifecycle.PodAbsent,
		lifecycle.PodPending,
		lifecycle.PodReady,
		lifecycle.PodNotReady,
		lifecycle.PodFailed,
		lifecycle.PodSucceeded,
	}
	for _, phase := range state.All() {
		for _, obs := range observations {
			for _, recycle := range []bool{false, true} {
				d := lifecycle.DecideSDKWarm(lifecycle.SDKWarmInputs{
					Phase: phase, Pod: obs, Recycle: recycle,
					SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: 60 * time.Second,
				})
				if d.Action != lifecycle.ActionSetPhase {
					continue
				}
				if err := state.IsValid(phase, d.NextPhase); err != nil {
					t.Errorf("DecideSDKWarm(phase=%q, obs=%v, recycle=%v) emits invalid transition %q → %q: %v",
						phase, obs, recycle, phase, d.NextPhase, err)
				}
			}
		}
	}
}
