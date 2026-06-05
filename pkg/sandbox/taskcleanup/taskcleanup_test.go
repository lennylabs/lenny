// SPDX-License-Identifier: MIT

package taskcleanup

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// base returns an Inputs with a successful scrub, generous limits, and a
// schedulable host: the canonical "reuse" case. Each test mutates the
// fields relevant to its §6.2 edge.
func base() Inputs {
	return Inputs{
		PreConnect:          false,
		Scrub:               ScrubSucceeded,
		OnCleanupFailure:    OnCleanupWarn,
		ScrubFailureCount:   0,
		MaxScrubFailures:    3,
		TasksCompleted:      1,
		MaxTasksPerPod:      100,
		PodUptimeSeconds:    10,
		MaxPodUptimeSeconds: 0, // no cap
		HostSchedulable:     true,
	}
}

// TestDecidePendingWaits covers the not-ready guard: until the adapter
// reports a scrub outcome the driver must not advance the pod.
// spec: §6.2 line 142 (task_complete_acknowledged precedes scrub).
func TestDecidePendingWaits(t *testing.T) {
	in := base()
	in.Scrub = ScrubPending
	got := Decide(in)
	if got.Ready {
		t.Fatalf("pending scrub: Ready = true, want false")
	}
	if got.NextPhase != "" {
		t.Fatalf("pending scrub: NextPhase = %q, want empty", got.NextPhase)
	}
}

// TestDecideSpecEdges drives every enumerated §6.2 lines 147-156
// disposition edge.
func TestDecideSpecEdges(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Inputs)
		wantPhase   state.State
		wantWarning bool
		wantRetire  bool
		wantReason  RetireReason
		specLine    string
	}{
		{
			name:       "line147_non_preconnect_reuse_idle",
			mutate:     func(i *Inputs) {},
			wantPhase:  state.Idle,
			wantReason: ReasonReuse,
			specLine:   "147",
		},
		{
			name:        "line148_non_preconnect_scrub_warning_idle",
			mutate:      func(i *Inputs) { i.Scrub = ScrubFailed },
			wantPhase:   state.Idle,
			wantWarning: true,
			wantReason:  ReasonReuse,
			specLine:    "148",
		},
		{
			name: "line149_scrub_failures_exhausted_draining",
			mutate: func(i *Inputs) {
				i.Scrub = ScrubFailed
				i.ScrubFailureCount = 3
				i.MaxScrubFailures = 3
			},
			wantPhase:   state.Draining,
			wantWarning: false, // retired for cause; spec brackets no warning
			wantRetire:  true,
			wantReason:  ReasonScrubFailuresExhausted,
			specLine:    "149",
		},
		{
			name: "line150_max_tasks_reached_draining",
			mutate: func(i *Inputs) {
				i.TasksCompleted = 100
				i.MaxTasksPerPod = 100
			},
			wantPhase:  state.Draining,
			wantRetire: true,
			wantReason: ReasonMaxTasksReached,
			specLine:   "150",
		},
		{
			name: "line151_max_uptime_exceeded_draining",
			mutate: func(i *Inputs) {
				i.PodUptimeSeconds = 3600
				i.MaxPodUptimeSeconds = 3600
			},
			wantPhase:  state.Draining,
			wantRetire: true,
			wantReason: ReasonMaxUptimeExceeded,
			specLine:   "151",
		},
		{
			name: "line152_preconnect_scrub_ok_unschedulable_draining",
			mutate: func(i *Inputs) {
				i.PreConnect = true
				i.HostSchedulable = false
			},
			wantPhase:  state.Draining,
			wantRetire: true,
			wantReason: ReasonHostUnschedulable,
			specLine:   "152",
		},
		{
			name: "line153_preconnect_warn_failed_unschedulable_draining_warning",
			mutate: func(i *Inputs) {
				i.PreConnect = true
				i.HostSchedulable = false
				i.Scrub = ScrubFailed
			},
			wantPhase:   state.Draining,
			wantWarning: true,
			wantRetire:  true,
			wantReason:  ReasonHostUnschedulable,
			specLine:    "153",
		},
		{
			name: "line154_preconnect_scrub_ok_schedulable_sdk_connecting",
			mutate: func(i *Inputs) {
				i.PreConnect = true
				i.HostSchedulable = true
			},
			wantPhase:  state.SDKConnecting,
			wantReason: ReasonReuse,
			specLine:   "154",
		},
		{
			name: "line155_preconnect_warn_failed_schedulable_sdk_connecting_warning",
			mutate: func(i *Inputs) {
				i.PreConnect = true
				i.HostSchedulable = true
				i.Scrub = ScrubFailed
			},
			wantPhase:   state.SDKConnecting,
			wantWarning: true,
			wantReason:  ReasonReuse,
			specLine:    "155",
		},
		{
			name: "line156_oncleanupfailure_fail_terminates",
			mutate: func(i *Inputs) {
				i.Scrub = ScrubFailed
				i.OnCleanupFailure = OnCleanupFail
			},
			wantPhase:  state.Failed,
			wantRetire: true,
			wantReason: ReasonCleanupFailPolicy,
			specLine:   "156",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mutate(&in)
			got := Decide(in)
			if !got.Ready {
				t.Fatalf("spec line %s: Ready = false, want true", tc.specLine)
			}
			if got.NextPhase != tc.wantPhase {
				t.Errorf("spec line %s: NextPhase = %q, want %q", tc.specLine, got.NextPhase, tc.wantPhase)
			}
			if got.ScrubWarning != tc.wantWarning {
				t.Errorf("spec line %s: ScrubWarning = %v, want %v", tc.specLine, got.ScrubWarning, tc.wantWarning)
			}
			if got.Retire != tc.wantRetire {
				t.Errorf("spec line %s: Retire = %v, want %v", tc.specLine, got.Retire, tc.wantRetire)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("spec line %s: Reason = %q, want %q", tc.specLine, got.Reason, tc.wantReason)
			}
			// Every disposition must be a legal §6.2 task_cleanup edge.
			if err := state.IsValid(state.TaskCleanup, got.NextPhase); err != nil {
				t.Errorf("spec line %s: disposition %q is not a valid task_cleanup edge: %v", tc.specLine, got.NextPhase, err)
			}
		})
	}
}

// TestDecideFailPolicyBeatsExhaustion verifies the precedence: a
// fail-policy scrub failure terminates (line 156) before the line 149
// exhaustion drain is evaluated, even when both conditions hold.
func TestDecideFailPolicyBeatsExhaustion(t *testing.T) {
	in := base()
	in.Scrub = ScrubFailed
	in.OnCleanupFailure = OnCleanupFail
	in.ScrubFailureCount = 9
	in.MaxScrubFailures = 3
	got := Decide(in)
	if got.NextPhase != state.Failed {
		t.Fatalf("fail policy + exhaustion: NextPhase = %q, want failed", got.NextPhase)
	}
	if got.Reason != ReasonCleanupFailPolicy {
		t.Fatalf("fail policy + exhaustion: Reason = %q, want %q", got.Reason, ReasonCleanupFailPolicy)
	}
}

// TestDecideExhaustionBeatsRetirement verifies a warn-policy scrub
// failure that has reached maxScrubFailures drains on the scrub reason
// even when the task/uptime caps would also retire it. The scrub
// failure is the more specific cause.
func TestDecideExhaustionBeatsRetirement(t *testing.T) {
	in := base()
	in.Scrub = ScrubFailed
	in.ScrubFailureCount = 3
	in.MaxScrubFailures = 3
	in.TasksCompleted = 100
	in.MaxTasksPerPod = 100
	got := Decide(in)
	if got.Reason != ReasonScrubFailuresExhausted {
		t.Fatalf("exhaustion + max-tasks: Reason = %q, want %q", got.Reason, ReasonScrubFailuresExhausted)
	}
}

// TestDecideMaxTasksBeatsReuseWithWarning verifies that a warn-policy
// scrub failure that has NOT exhausted maxScrubFailures but HAS reached
// maxTasksPerPod retires on max_tasks_reached (the pod is leaving for
// count, so the warning is superseded). spec: §6.2 lines 149-150.
func TestDecideMaxTasksBeatsReuseWithWarning(t *testing.T) {
	in := base()
	in.Scrub = ScrubFailed
	in.ScrubFailureCount = 1
	in.MaxScrubFailures = 3
	in.TasksCompleted = 100
	in.MaxTasksPerPod = 100
	got := Decide(in)
	if got.NextPhase != state.Draining || got.Reason != ReasonMaxTasksReached {
		t.Fatalf("warn-failed + max-tasks: got %q/%q, want draining/max_tasks_reached", got.NextPhase, got.Reason)
	}
	if got.ScrubWarning {
		t.Errorf("warn-failed + max-tasks: ScrubWarning = true, want false (retired for count)")
	}
}

// TestDecideHostUnschedulableOnlyAffectsPreConnect verifies the line 181
// fail-safe applies only to preConnect pods: a non-preConnect pod on a
// cordoned node still returns to idle (it never re-warms), while a
// preConnect pod drains.
func TestDecideHostUnschedulableOnlyAffectsPreConnect(t *testing.T) {
	nonPre := base()
	nonPre.PreConnect = false
	nonPre.HostSchedulable = false
	if got := Decide(nonPre); got.NextPhase != state.Idle {
		t.Errorf("non-preConnect cordoned: NextPhase = %q, want idle", got.NextPhase)
	}

	pre := base()
	pre.PreConnect = true
	pre.HostSchedulable = false
	if got := Decide(pre); got.NextPhase != state.Draining || got.Reason != ReasonHostUnschedulable {
		t.Errorf("preConnect cordoned: got %q/%q, want draining/host_unschedulable", got.NextPhase, got.Reason)
	}
}

// TestDecideMaxScrubFailuresDefault verifies an unset (<=0)
// MaxScrubFailures applies the §5.2 default of 3.
func TestDecideMaxScrubFailuresDefault(t *testing.T) {
	// 2 failures with the default-3 limit: not yet exhausted -> reuse.
	in := base()
	in.Scrub = ScrubFailed
	in.ScrubFailureCount = 2
	in.MaxScrubFailures = 0 // unset -> default 3
	if got := Decide(in); got.NextPhase != state.Idle {
		t.Errorf("2/default-3 failures: NextPhase = %q, want idle [scrub_warning]", got.NextPhase)
	}
	// 3 failures with the default-3 limit: exhausted -> draining.
	in.ScrubFailureCount = 3
	if got := Decide(in); got.Reason != ReasonScrubFailuresExhausted {
		t.Errorf("3/default-3 failures: Reason = %q, want exhausted", got.Reason)
	}
}

// TestDecideNoUptimeCapWhenUnset verifies MaxPodUptimeSeconds == 0 means
// no cap: a long-running pod still reuses. spec: §6.2 line 151 (the cap
// is optional).
func TestDecideNoUptimeCapWhenUnset(t *testing.T) {
	in := base()
	in.PodUptimeSeconds = 1 << 40
	in.MaxPodUptimeSeconds = 0
	if got := Decide(in); got.NextPhase != state.Idle || got.Retire {
		t.Errorf("no uptime cap: got %q retire=%v, want idle reuse", got.NextPhase, got.Retire)
	}
}

// TestDecideCancelledScrubReusesThroughTaskCleanup verifies the §6.2
// line 146 semantics at the disposition layer: a cancelled task that ran
// its scrub successfully and is under its limits reuses the pod, exactly
// like a completed task. The cancelled → task_cleanup edge itself is
// asserted in the state package.
func TestDecideCancelledScrubReusesThroughTaskCleanup(t *testing.T) {
	in := base() // a cancelled task's scrub outcome feeds the same branch
	if got := Decide(in); got.NextPhase != state.Idle {
		t.Fatalf("cancelled-then-scrubbed reuse: NextPhase = %q, want idle", got.NextPhase)
	}
}
