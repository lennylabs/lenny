// SPDX-License-Identifier: MIT

package taskcleanup

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// base returns an Inputs with a successful scrub, generous limits, and a
// schedulable host: the canonical non-preConnect "reuse" case, which now
// holds the pod in `reserved`. Each test mutates the fields relevant to
// its §6.2 / §6.39 edge.
func base() Inputs {
	return Inputs{
		PreConnect:          false,
		Scrub:               ScrubSucceeded,
		OnCleanupFailure:    OnCleanupWarn,
		ScrubFailureCount:   0,
		MaxScrubFailures:    3,
		SessionsServed:      1,
		MaxSessionsPerPod:   100,
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
// disposition edge, including the session_count_limit retirement reason
// keyed to the spec/16 retirement-reason vocabulary.
// spec: §6.2 lines 147-156 (recycle disposition), §16.1 (retirement reason vocabulary).
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
			name:       "non_preconnect_reuse_reserved",
			mutate:     func(i *Inputs) {},
			wantPhase:  state.Reserved,
			wantReason: ReasonReuse,
			specLine:   "§5.2 line 449 (non-preConnect reserve reuse)",
		},
		{
			name:        "non_preconnect_scrub_warning_reserved",
			mutate:      func(i *Inputs) { i.Scrub = ScrubFailed },
			wantPhase:   state.Reserved,
			wantWarning: true,
			wantReason:  ReasonReuse,
			specLine:    "§5.2 (warn-policy reserve reuse)",
		},
		{
			name: "non_preconnect_unschedulable_retire",
			mutate: func(i *Inputs) {
				i.PreConnect = false
				i.HostSchedulable = false
			},
			wantPhase:  state.Draining,
			wantRetire: true,
			wantReason: ReasonHostUnschedulable,
			specLine:   "§6.39 (non-preConnect cordon retire)",
		},
		{
			name: "non_preconnect_warn_failed_unschedulable_retire_warning",
			mutate: func(i *Inputs) {
				i.PreConnect = false
				i.HostSchedulable = false
				i.Scrub = ScrubFailed
			},
			wantPhase:   state.Draining,
			wantWarning: true,
			wantRetire:  true,
			wantReason:  ReasonHostUnschedulable,
			specLine:    "§6.39 (non-preConnect cordon retire, warn)",
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
			name: "line150_session_count_limit_draining",
			mutate: func(i *Inputs) {
				i.SessionsServed = 100
				i.MaxSessionsPerPod = 100
			},
			wantPhase:  state.Draining,
			wantRetire: true,
			wantReason: ReasonSessionCountLimit,
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
			// Every recycle disposition is one of the coarse §6.2 occupancy
			// phases the recycle edges target (re-warm, reserve reuse, or
			// retire). The former task_cleanup source phase no longer exists
			// (spec §6.2, §6.37); the disposition driver selects the NextPhase
			// directly.
			switch got.NextPhase {
			case state.SDKConnecting, state.Reserved, state.Draining, state.Failed:
			default:
				t.Errorf("spec line %s: disposition NextPhase %q is not a coarse §6.2 recycle outcome", tc.specLine, got.NextPhase)
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
	in.SessionsServed = 100
	in.MaxSessionsPerPod = 100
	got := Decide(in)
	if got.Reason != ReasonScrubFailuresExhausted {
		t.Fatalf("exhaustion + session-count: Reason = %q, want %q", got.Reason, ReasonScrubFailuresExhausted)
	}
}

// TestDecideSessionCountBeatsReuseWithWarning verifies that a warn-policy
// scrub failure that has NOT exhausted maxScrubFailures but HAS reached
// recycle.maxSessionsPerPod retires on session_count_limit (the pod is
// leaving for count, so the warning is superseded).
// spec: §6.2 lines 149-150 (recycle disposition precedence), §16.1 (session_count_limit reason).
func TestDecideSessionCountBeatsReuseWithWarning(t *testing.T) {
	in := base()
	in.Scrub = ScrubFailed
	in.ScrubFailureCount = 1
	in.MaxScrubFailures = 3
	in.SessionsServed = 100
	in.MaxSessionsPerPod = 100
	got := Decide(in)
	if got.NextPhase != state.Draining || got.Reason != ReasonSessionCountLimit {
		t.Fatalf("warn-failed + session-count: got %q/%q, want draining/session_count_limit", got.NextPhase, got.Reason)
	}
	if got.ScrubWarning {
		t.Errorf("warn-failed + session-count: ScrubWarning = true, want false (retired for count)")
	}
}

// TestDecideHostUnschedulableRetiresBothPoolTypes verifies the §6.39
// host-node schedulability retire applies to both preConnect and
// non-preConnect pools: a recycling pod on a cordoned node drains rather
// than reserving (non-preConnect) or re-warming (preConnect), because
// either reuse disposition would hand the next session a
// soon-to-be-evicted pod.
// spec: §6.39 (host-node schedulability retire on both pool types).
func TestDecideHostUnschedulableRetiresBothPoolTypes(t *testing.T) {
	nonPre := base()
	nonPre.PreConnect = false
	nonPre.HostSchedulable = false
	if got := Decide(nonPre); got.NextPhase != state.Draining || got.Reason != ReasonHostUnschedulable {
		t.Errorf("non-preConnect cordoned: got %q/%q, want draining/host_unschedulable", got.NextPhase, got.Reason)
	}

	pre := base()
	pre.PreConnect = true
	pre.HostSchedulable = false
	if got := Decide(pre); got.NextPhase != state.Draining || got.Reason != ReasonHostUnschedulable {
		t.Errorf("preConnect cordoned: got %q/%q, want draining/host_unschedulable", got.NextPhase, got.Reason)
	}

	// A schedulable host re-establishes reuse: non-preConnect reserves,
	// preConnect re-warms.
	okNonPre := base()
	if got := Decide(okNonPre); got.NextPhase != state.Reserved || got.Retire {
		t.Errorf("non-preConnect schedulable: got %q retire=%v, want reserved reuse", got.NextPhase, got.Retire)
	}
	okPre := base()
	okPre.PreConnect = true
	if got := Decide(okPre); got.NextPhase != state.SDKConnecting || got.Retire {
		t.Errorf("preConnect schedulable: got %q retire=%v, want sdk_connecting reuse", got.NextPhase, got.Retire)
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
	if got := Decide(in); got.NextPhase != state.Reserved {
		t.Errorf("2/default-3 failures: NextPhase = %q, want reserved [scrub_warning]", got.NextPhase)
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
	if got := Decide(in); got.NextPhase != state.Reserved || got.Retire {
		t.Errorf("no uptime cap: got %q retire=%v, want reserved reuse", got.NextPhase, got.Retire)
	}
}

// TestDecideScrubbedReuseReserves verifies that a recycling pod that ran
// its whole-pod scrub successfully and is under its limits reuses the pod
// by reserving it for the pinned tenant (non-preConnect path).
// spec: §5.2 (recycle lifecycle, line 449), §6.2 (recycle disposition).
func TestDecideScrubbedReuseReserves(t *testing.T) {
	in := base()
	if got := Decide(in); got.NextPhase != state.Reserved {
		t.Fatalf("scrubbed reuse: NextPhase = %q, want reserved", got.NextPhase)
	}
}
