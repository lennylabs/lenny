// SPDX-License-Identifier: MIT

package podscrub

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

// TestDecideVMRestartRetiresAndReprovisions pins the §5.2 step 7 vm-restart
// disposition: a vm-restart pool retires the pod at the occupancy-zero recycle
// boundary (draining) with the non-counting ReasonVMRestartReprovision on a
// clean scrub, rather than reusing it (reserve or re-warm). Reusing the pod
// would return it to cross-tenant service without a fresh guest, the fail-open
// this branch closes, so the non-happy path is a Reserved/SDKConnecting reuse
// disposition on a vm-restart pool. standard and in-place pools keep their
// reuse dispositions.
// spec: spec/05 §5.2 step 7 (retire-and-reprovision), §16.1 (retirement reason vocabulary).
func TestDecideVMRestartRetiresAndReprovisions(t *testing.T) {
	// A clean scrub on a vm-restart pool retires (draining, non-counting).
	in := base()
	in.VMRestart = true
	got := Decide(in)
	if got.NextPhase != state.Draining || !got.Retire || got.Reason != ReasonVMRestartReprovision {
		t.Fatalf("vm-restart clean scrub: got %q retire=%v reason=%q, want draining/true/vm_restart_reprovision",
			got.NextPhase, got.Retire, got.Reason)
	}
	if got.ScrubWarning {
		t.Errorf("vm-restart clean scrub: ScrubWarning = true, want false (no scrub failure)")
	}

	// A preConnect vm-restart pool also retires (no SDK re-warm leg).
	pre := base()
	pre.VMRestart = true
	pre.PreConnect = true
	if got := Decide(pre); got.NextPhase != state.Draining || got.Reason != ReasonVMRestartReprovision {
		t.Errorf("vm-restart preConnect clean scrub: got %q/%q, want draining/vm_restart_reprovision",
			got.NextPhase, got.Reason)
	}

	// A standard (non-vm-restart) pool still reuses (reserve), so the branch is
	// keyed on the profile flag rather than firing for every pool.
	std := base()
	if got := Decide(std); got.NextPhase != state.Reserved || got.Retire {
		t.Errorf("standard pool: got %q retire=%v, want reserved reuse", got.NextPhase, got.Retire)
	}
	// An in-place (non-vm-restart) preConnect pool re-warms.
	inPlace := base()
	inPlace.PreConnect = true
	if got := Decide(inPlace); got.NextPhase != state.SDKConnecting || got.Retire {
		t.Errorf("in-place preConnect pool: got %q retire=%v, want sdk_connecting reuse", got.NextPhase, got.Retire)
	}

	// The vm-restart reprovision is a routine per-recycle-boundary retire, not a
	// §16.1 limit trigger, so it does not count on lenny_gateway_pod_retirement_total.
	if ReasonVMRestartReprovision.CountsOnRetirementTotal() {
		t.Errorf("ReasonVMRestartReprovision counts on the retirement total; it is outside the §16.1 vocabulary")
	}
}

// TestDecideVMRestartWarnFailedRetiresWithWarning pins that a warn-policy scrub
// failure that has NOT exhausted maxScrubFailures on a vm-restart pool still
// retires with ReasonVMRestartReprovision and carries ScrubWarning: true, so
// the scrub_warning annotation reaches the retired pod's audit trail (S2 item
// 6). The non-happy path is a warn-failed vm-restart retire that drops
// ScrubWarning, losing the degraded-state marker.
// spec: spec/05 §5.2 step 7 (retire-and-reprovision), §5.2 (onScrubFailure warn).
func TestDecideVMRestartWarnFailedRetiresWithWarning(t *testing.T) {
	in := base()
	in.VMRestart = true
	in.Scrub = ScrubFailed
	in.OnCleanupFailure = OnCleanupWarn
	in.ScrubFailureCount = 1 // below the default limit of 3: not exhausted.
	in.MaxScrubFailures = 3
	got := Decide(in)
	if got.NextPhase != state.Draining || got.Reason != ReasonVMRestartReprovision {
		t.Fatalf("vm-restart warn-failed: got %q/%q, want draining/vm_restart_reprovision", got.NextPhase, got.Reason)
	}
	if !got.ScrubWarning {
		t.Errorf("vm-restart warn-failed: ScrubWarning = false, want true (annotation carried onto retire)")
	}
}

// TestDecideVMRestartSessionCountOnePrecedence pins the C3 branch ordering at
// the maxSessionsPerPod: 1 boundary. A sequential vm-restart pod's served count
// read back at the occupancy-zero boundary equals maxSessionsPerPod (1), so the
// session-count predicate (SessionsServed >= MaxSessionsPerPod) is true, but the
// preceding vm-restart branch retires first with the non-counting
// ReasonVMRestartReprovision. The non-happy path is a maxSessionsPerPod: 1
// vm-restart pool retiring with the counting ReasonSessionCountLimit, which
// would diverge the retire reason and the lenny_gateway_pod_retirement_total increment
// from an otherwise-identical maxSessionsPerPod >= 2 pool. A mis-ordered branch
// (session-count before vm-restart) fails this test.
// spec: spec/05 §5.2 step 7 (retire-and-reprovision precedes session-count), §16.1.
func TestDecideVMRestartSessionCountOnePrecedence(t *testing.T) {
	in := base()
	in.VMRestart = true
	in.SessionsServed = 1
	in.MaxSessionsPerPod = 1
	got := Decide(in)
	if got.NextPhase != state.Draining {
		t.Fatalf("vm-restart maxSessionsPerPod:1: NextPhase = %q, want draining", got.NextPhase)
	}
	if got.Reason != ReasonVMRestartReprovision {
		t.Fatalf("vm-restart maxSessionsPerPod:1: Reason = %q, want vm_restart_reprovision (non-counting), got the session-count limit means the branch is mis-ordered", got.Reason)
	}
	if got.Reason.CountsOnRetirementTotal() {
		t.Errorf("vm-restart maxSessionsPerPod:1 retire counts on the retirement total; the reason diverged to a limit trigger")
	}
}

// TestDecideVMRestartFailPolicyPrecedence pins that a genuine onScrubFailure:
// fail failure on a vm-restart pool keeps its fail-closed Failed terminal with
// ReasonCleanupFailPolicy rather than being masked as the non-counting
// vm-restart Draining reprovision. The fail-policy branch precedes the
// vm-restart branch. The non-happy path is a fail-policy failure masked as the
// vm-restart reprovision, which would drop the fail-closed terminal.
// spec: spec/05 §5.2 (onScrubFailure fail), spec/06 §6.2 line 156.
func TestDecideVMRestartFailPolicyPrecedence(t *testing.T) {
	in := base()
	in.VMRestart = true
	in.Scrub = ScrubFailed
	in.OnCleanupFailure = OnCleanupFail
	got := Decide(in)
	if got.NextPhase != state.Failed || got.Reason != ReasonCleanupFailPolicy {
		t.Fatalf("vm-restart fail-policy: got %q/%q, want failed/cleanup_fail_policy", got.NextPhase, got.Reason)
	}
}

// TestDecideVMRestartScrubExhaustedPrecedence pins that a cumulative
// scrub-failure-limit retire on a vm-restart pool keeps its counting
// ReasonScrubFailuresExhausted reason rather than being undercounted as the
// non-counting vm-restart reprovision. A scrub-failure limit is a genuine limit
// trigger even on a vm-restart pool, so the scrub-exhausted branch precedes the
// vm-restart branch. The non-happy path is a scrub-failure-limit retire
// undercounted as ReasonVMRestartReprovision.
// spec: spec/06 §6.2 line 149 (scrub-failure limit retire), §16.1 (scrub_failure_limit counts).
func TestDecideVMRestartScrubExhaustedPrecedence(t *testing.T) {
	in := base()
	in.VMRestart = true
	in.Scrub = ScrubFailed
	in.OnCleanupFailure = OnCleanupWarn
	in.ScrubFailureCount = 3
	in.MaxScrubFailures = 3
	got := Decide(in)
	if got.NextPhase != state.Draining || got.Reason != ReasonScrubFailuresExhausted {
		t.Fatalf("vm-restart scrub-exhausted: got %q/%q, want draining/scrub_failure_limit", got.NextPhase, got.Reason)
	}
	if !got.Reason.CountsOnRetirementTotal() {
		t.Errorf("vm-restart scrub-exhausted retire does not count; a scrub-failure limit is a genuine §16.1 trigger")
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

// TestCountsOnRetirementTotalVocabulary verifies the retirement-reason
// membership predicate matches the §16.1 frozen vocabulary exactly: the three
// retirement-limit triggers count (partitioned across
// lenny_gateway_pod_retirement_total for session_count_limit and
// scrub_failure_limit and lenny_controller_pod_retirement_total for
// uptime_limit), and every operational or failure-driven retire (the §6.39
// cordon-drain host_unschedulable, the onScrubFailure: fail termination, and
// the non-retire reuse label) is excluded so the emitter cannot widen the
// declared label set.
// spec: spec/16 §16.1 (retirement reason label set: session_count_limit,
// uptime_limit, scrub_failure_limit), §16.1.1 (reason is reserved for the
// lifecycle limit triggers; failures use error_type), §6.39 (cordon-drain is
// an operational retire outside the counter vocabulary).
func TestCountsOnRetirementTotalVocabulary(t *testing.T) {
	counted := map[RetireReason]bool{
		ReasonSessionCountLimit:      true,
		ReasonMaxUptimeExceeded:      true,
		ReasonScrubFailuresExhausted: true,
		ReasonHostUnschedulable:      false,
		ReasonScrubReportTimeout:     false,
		ReasonVMRestartReprovision:   false,
		ReasonCleanupFailPolicy:      false,
		ReasonReuse:                  false,
	}
	for reason, want := range counted {
		if got := reason.CountsOnRetirementTotal(); got != want {
			t.Errorf("RetireReason(%q).CountsOnRetirementTotal() = %v, want %v", reason, got, want)
		}
	}

	// Guard the frozen vocabulary against drift: the literal values the §16.1
	// inventory declares are exactly these three. A rename of one of the three
	// constants away from its spec value must fail here.
	for _, reason := range []RetireReason{"session_count_limit", "uptime_limit", "scrub_failure_limit"} {
		if !reason.CountsOnRetirementTotal() {
			t.Errorf("spec/16 §16.1 reason %q is not counted; the vocabulary drifted", reason)
		}
	}
	if RetireReason("host_unschedulable").CountsOnRetirementTotal() {
		t.Errorf("host_unschedulable counts on the retirement total; it is outside the §16.1 vocabulary")
	}
}

// TestCountsOnGatewayRetirementTotalExcludesUptime verifies the gateway-scoped
// retirement predicate: lenny_gateway_pod_retirement_total carries only the two
// gateway-decided reasons (session_count_limit, scrub_failure_limit) and
// suppresses uptime_limit, which is WarmPoolController-owned and counted on
// lenny_controller_pod_retirement_total. The predicate is the union-membership
// CountsOnRetirementTotal minus exactly {uptime_limit}, so an over-uptime pod
// that reaches occupancy zero is not double-counted across the two counters.
// spec: spec/16 §16.1 (retirement reason vocabulary partitioned by process; the
// gateway counter carries session_count_limit and scrub_failure_limit),
// spec/05 §5.2 (the maxPodUptimeSeconds retirement is WarmPoolController-owned).
func TestCountsOnGatewayRetirementTotalExcludesUptime(t *testing.T) {
	gatewayCounted := map[RetireReason]bool{
		ReasonSessionCountLimit:      true,
		ReasonScrubFailuresExhausted: true,
		// uptime_limit is a member of the union vocabulary but is
		// controller-owned; the gateway suppresses it.
		ReasonMaxUptimeExceeded:    false,
		ReasonHostUnschedulable:    false,
		ReasonScrubReportTimeout:   false,
		ReasonVMRestartReprovision: false,
		ReasonCleanupFailPolicy:    false,
		ReasonReuse:                false,
	}
	for reason, want := range gatewayCounted {
		if got := reason.CountsOnGatewayRetirementTotal(); got != want {
			t.Errorf("RetireReason(%q).CountsOnGatewayRetirementTotal() = %v, want %v", reason, got, want)
		}
	}

	// The gateway predicate is exactly the union predicate minus uptime_limit:
	// it never counts a reason the union excludes, and it excludes only
	// uptime_limit among the reasons the union counts.
	for _, reason := range []RetireReason{
		ReasonSessionCountLimit, ReasonMaxUptimeExceeded, ReasonScrubFailuresExhausted,
		ReasonHostUnschedulable, ReasonScrubReportTimeout, ReasonVMRestartReprovision,
		ReasonCleanupFailPolicy, ReasonReuse,
	} {
		gateway := reason.CountsOnGatewayRetirementTotal()
		union := reason.CountsOnRetirementTotal()
		if gateway && !union {
			t.Errorf("RetireReason(%q): gateway counts it but the union does not", reason)
		}
		if union && !gateway && reason != ReasonMaxUptimeExceeded {
			t.Errorf("RetireReason(%q): union counts it but the gateway excludes it, and it is not uptime_limit", reason)
		}
	}
}
