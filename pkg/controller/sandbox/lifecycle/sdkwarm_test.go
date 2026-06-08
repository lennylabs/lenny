// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §6.1 lines 30-69, §6.2 lines 89-98 — the SDK-warm warm path
// routes through sdk_connecting with a watchdog.
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
			name: "sdk_connecting becomes idle when ready",
			in:   lifecycle.SDKWarmInputs{Phase: state.SDKConnecting, Pod: lifecycle.PodReady},
			want: lifecycle.Decision{Action: lifecycle.ActionSetPhase, NextPhase: state.Idle},
		},
		{
			name: "sdk_connecting past watchdog budget fails",
			in: lifecycle.SDKWarmInputs{
				Phase: state.SDKConnecting, Pod: lifecycle.PodNotReady,
				SDKConnectElapsed: 90 * time.Second, SDKConnectTimeout: timeout,
			},
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
}
