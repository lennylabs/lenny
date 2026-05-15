// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"testing"

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
			name:  "a terminal phase takes no action",
			phase: state.Failed,
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
			name:  "a gateway-owned attached phase takes no action",
			phase: state.Attached,
			pod:   lifecycle.PodFailed,
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
