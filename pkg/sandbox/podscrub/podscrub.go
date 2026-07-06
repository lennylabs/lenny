// SPDX-License-Identifier: MIT

// Package podscrub implements the §6.2 recycle disposition branching
// for a recycling session-mode pod (spec: spec/06_warm-pod-model.md
// §6.2 recycle disposition, §6.39 host-node schedulability retire). When
// a recycling pod's occupancy reaches zero the gateway patches the claim
// to `recycling`, the adapter runs the credential purge, cleanupCommands,
// and the Lenny whole-pod scrub and reports the binary outcome via
// ReportPodScrub, and the disposition must advance the pod to exactly one
// of:
//
//	reserved                       (non-preConnect, scrub ok, schedulable host, not retiring)
//	reserved [scrub_warning]       (non-preConnect, warn-failed, schedulable host)
//	sdk_connecting                 (preConnect, scrub ok, schedulable host)
//	sdk_connecting [scrub_warning] (preConnect, warn-failed, schedulable host)
//	draining                       (a retirement limit reached, or cordoned host)
//	draining [scrub_warning]       (warn-failed on a cordoned host)
//	failed                         (onScrubFailure: fail)
//
// The non-preConnect reuse path advances to `reserved` because, with the
// per-pod occupancy claim, a successfully scrubbed pod is held for its
// pinned tenant through the claim's `reserved` hold window rather than
// returned straight to idle (spec: §5.2 recycle lifecycle, line 449). The
// host-node-schedulability retire (§6.39) applies to BOTH pool types: a
// recycling pod on a cordoned host node is retired rather than held in
// `reserved` or re-warmed, because either disposition would hand the next
// session a soon-to-be-evicted pod.
//
// Decide is a pure function so the branch table can be exhaustively
// unit-tested against the spec edges. The driver that supplies the
// inputs (the scrub outcome, the cumulative counters, and the
// lenny.dev/host-schedulable pod label read at the moment of the
// transition per §6.39) and writes the resulting binding state is the
// gateway scrub-report handler; this package holds no Kubernetes client
// and emits no metrics, it only decides.
package podscrub

import "github.com/lennylabs/lenny/pkg/sandbox/state"

// DefaultMaxScrubFailures is the §5.2 sessionPolicy.recycle.maxScrubFailures
// default applied when Inputs.MaxScrubFailures is unset (<= 0).
// spec: spec/05_runtime-registry-and-pool-model.md — "default is 3".
const DefaultMaxScrubFailures = 3

// ScrubResult is the outcome of the Lenny scrub (§5.2 steps 1-6) the
// adapter reports after task_complete_acknowledged. Until the adapter
// reports, the result is ScrubPending and Decide returns a not-ready
// disposition so the driver waits rather than guessing.
type ScrubResult int

const (
	// ScrubPending: the adapter has not yet reported a scrub outcome
	// for the just-finished task.
	ScrubPending ScrubResult = iota
	// ScrubSucceeded: every scrub step and the post-scrub stat
	// verification completed.
	ScrubSucceeded
	// ScrubFailed: a scrub step or the post-scrub verification failed.
	ScrubFailed
)

// CleanupFailurePolicy mirrors §5.2 sessionPolicy.recycle.onScrubFailure. An
// empty policy is treated as OnCleanupWarn (the spec default disposition
// returns the pod to the pool with a scrub_warning rather than retiring).
type CleanupFailurePolicy string

const (
	// OnCleanupWarn returns the pod to the pool with a scrub_warning
	// annotation when the scrub fails. spec: §6.2 lines 148/153/155.
	OnCleanupWarn CleanupFailurePolicy = "warn"
	// OnCleanupFail retires and replaces the pod on any scrub failure.
	// spec: §6.2 line 156.
	OnCleanupFail CleanupFailurePolicy = "fail"
)

// Inputs carries everything the §6.2 lines 147-156 disposition branch
// reads. All counts are evaluated AFTER the just-finished session: a pod
// that has served its last permitted session has SessionsServed ==
// MaxSessionsPerPod.
type Inputs struct {
	// PreConnect is true when the pool's runtime declares
	// capabilities.preConnect: true, so idle pods are SDK-warm and the
	// pod re-warms through sdk_connecting between tasks. spec: §6.2 line 76.
	PreConnect bool

	// VMRestart is true when the pool's recycle policy sets
	// recycle.scrubProfile: vm-restart. On such a pool a pod is retired at the
	// occupancy-zero recycle boundary after the whole-pod scrub reports, and
	// the warm pool provisions a fresh replacement pod (a fresh guest VM). The
	// in-place guest restart the profile name once implied is unimplementable
	// under the agent-pod isolation model, so the achievable mechanism is
	// retire-and-reprovision: the gateway holds the scrub profile in its
	// runtime store and sets this flag so Decide takes the retire branch rather
	// than reusing the pod (which would return it to cross-tenant service
	// without a fresh guest, a fail-open). spec: spec/05 §5.2 step 7
	// (fresh-guest reprovision).
	VMRestart bool

	// Scrub is the reported outcome of this task's Lenny scrub.
	Scrub ScrubResult

	// OnCleanupFailure is sessionPolicy.recycle.onScrubFailure (warn or fail).
	OnCleanupFailure CleanupFailurePolicy

	// ScrubFailureCount is the pod's cumulative scrub-failure count
	// INCLUDING this task's scrub. It is compared against
	// MaxScrubFailures. spec: §5.2 lenny_pod_scrub_failure_count.
	ScrubFailureCount int

	// MaxScrubFailures is sessionPolicy.recycle.maxScrubFailures. A value <= 0 is
	// treated as DefaultMaxScrubFailures.
	MaxScrubFailures int

	// SessionsServed is the count of sessions this pod has served,
	// INCLUDING the session that just ended.
	SessionsServed int

	// MaxSessionsPerPod is recycle.maxSessionsPerPod (required with no
	// default when recycle.enabled: true, >= 1). A value <= 0 disables
	// the count-based cap (defensive; admission rejects a recycling pool
	// that omits it). spec: spec/05 §5.2 (recycle.maxSessionsPerPod).
	MaxSessionsPerPod int

	// PodUptimeSeconds is the pod's wall-clock uptime in seconds.
	PodUptimeSeconds int64

	// MaxPodUptimeSeconds is sessionPolicy.recycle.maxPodUptimeSeconds. A value
	// <= 0 means no uptime cap. spec: §6.2 line 151.
	MaxPodUptimeSeconds int64

	// HostSchedulable reflects the lenny.dev/host-schedulable pod label:
	// true only when the label reads "true". An absent or "false" label
	// is fail-safe-unschedulable, so the caller passes false. The
	// host-node-schedulability retire gates BOTH the preConnect re-warm
	// reuse and the non-preConnect `reserved` reuse: a recycling pod on a
	// cordoned host is retired rather than held or re-warmed. spec: §6.39
	// ("absent, which is treated as unschedulable"; the trigger applies to
	// non-preConnect pools as well).
	HostSchedulable bool
}

// RetireReason is a stable label for the disposition the driver records in
// the audit trail. A reason that reports CountsOnRetirementTotal is a member
// of the frozen §16.1 retirement-reason vocabulary (session_count_limit,
// uptime_limit, scrub_failure_limit), which partitions by process across two
// counters: the gateway's applyDisposition emits session_count_limit and
// scrub_failure_limit on lenny_gateway_pod_retirement_total and suppresses its
// uptime_limit emission, while the WarmPoolController counts uptime_limit on
// lenny_controller_pod_retirement_total. A reason outside that frozen
// vocabulary (the §6.39 cordon-drain, the fail-policy termination) is recorded
// in the audit trail only and never on either retirement counter.
type RetireReason string

const (
	// ReasonReuse: the pod returns to the warm pool (idle or
	// sdk_connecting). Not a retirement.
	ReasonReuse RetireReason = "reuse"
	// ReasonCleanupFailPolicy: onCleanupFailure: fail terminated the
	// pod on a scrub failure. spec: §6.2 line 156.
	ReasonCleanupFailPolicy RetireReason = "cleanup_fail_policy"
	// ReasonScrubFailuresExhausted: the cumulative scrub-failure count
	// reached maxScrubFailures. The emitted lenny_gateway_pod_retirement_total
	// {reason} value is the scrub_failure_limit trigger from the spec/16
	// retirement-reason vocabulary, so the counter and its emitter agree.
	// spec: spec/06 §6.2 line 149 (scrub-failure limit retire), spec/16
	// §16.1 (retirement reason vocabulary).
	ReasonScrubFailuresExhausted RetireReason = "scrub_failure_limit"
	// ReasonSessionCountLimit: the pod's served-session count reached
	// recycle.maxSessionsPerPod. The emitted lenny_gateway_pod_retirement_total
	// {reason} value is the session_count_limit trigger from the spec/16
	// retirement-reason vocabulary. spec: spec/06 §6.2 (recycle
	// disposition), spec/16 §16.1 (retirement reason vocabulary).
	ReasonSessionCountLimit RetireReason = "session_count_limit"
	// ReasonMaxUptimeExceeded: pod uptime reached maxPodUptimeSeconds. Its
	// audit reason is the uptime_limit trigger from the spec/16
	// retirement-reason vocabulary. The uptime_limit retirement is
	// WarmPoolController-owned, counted on lenny_controller_pod_retirement_total
	// by the level-triggered maxPodUptimeSeconds drain; the gateway's
	// applyDisposition suppresses its own uptime_limit emission on
	// lenny_gateway_pod_retirement_total, so an over-uptime pod that reaches
	// occupancy zero is not double-counted. Decide still returns this reason at
	// the occupancy-zero recycle boundary as a draining-state backstop. spec:
	// §6.2 line 151, spec/16 §16.1 (retirement reason vocabulary partitioned by
	// process across the two counters).
	ReasonMaxUptimeExceeded RetireReason = "uptime_limit"
	// ReasonHostUnschedulable: the pod's host node is cordoned, so the
	// recycle disposition retires it instead of producing a
	// soon-to-be-evicted reserved or re-warmed pod. The trigger applies to
	// both preConnect (re-warm) and non-preConnect (reserve) reuse paths.
	// This reason is an operational drain driven by infrastructure rather than
	// one of the three retirement-limit triggers, so it is NOT a member of the
	// lenny_gateway_pod_retirement_total{reason} vocabulary and CountsOnRetirementTotal
	// reports false for it; the disposition still drives the drain and records
	// the reason in the audit trail. spec: §6.2 (recycle disposition), §6.39
	// (host-node schedulability retire), spec/16 §16.1.1 (retirement-reason
	// vocabulary is the three limit triggers only).
	ReasonHostUnschedulable RetireReason = "host_unschedulable"
	// ReasonScrubReportTimeout: the adapter never sent ReportPodScrub within
	// the gateway-side missing-report timeout (cleanupTimeoutSeconds plus a
	// grace) armed at the bound → recycling transition, so the gateway retires
	// the pod rather than leaving it stuck in `recycling` until the much longer
	// orphan-GC window. The scrub never completed, so the pod's residual state
	// is uncleared and the retire is fail-closed (the `failed` terminal). This
	// is a coordinator-driven timeout rather than one of the three
	// retirement-limit triggers, so it is NOT a member of the
	// lenny_gateway_pod_retirement_total{reason} vocabulary and CountsOnRetirementTotal
	// reports false for it. spec: §3.4 (gateway-side missing-report timeout),
	// §4.7 (missing report bounded by cleanupTimeoutSeconds plus a grace),
	// spec/16 §16.1.1 (retirement-reason vocabulary is the three limit triggers
	// only).
	ReasonScrubReportTimeout RetireReason = "scrub_report_timeout"
	// ReasonVMRestartReprovision: the pod ran on a recycle.scrubProfile:
	// vm-restart pool, so the recycle disposition retires it at the
	// occupancy-zero boundary and the warm pool provisions a fresh replacement
	// pod (a fresh guest VM). A fresh microvm pod structurally eliminates every
	// guest-kernel-level residual-state vector the in-guest scrub cannot reach,
	// so this retire, rather than a reuse, is the fail-closed disposition for a
	// vm-restart pool. This is a routine per-recycle-boundary reprovision rather
	// than one of the three retirement-limit triggers, so it is NOT a member of
	// the lenny_gateway_pod_retirement_total{reason} vocabulary and
	// CountsOnRetirementTotal reports false for it (like ReasonHostUnschedulable
	// and ReasonScrubReportTimeout); the disposition still drives the drain and
	// records the reason in the audit trail. spec: spec/05 §5.2 step 7
	// (retire-and-reprovision), spec/16 §16.1.1 (retirement-reason vocabulary is
	// the three limit triggers only).
	ReasonVMRestartReprovision RetireReason = "vm_restart_reprovision"
)

// CountsOnRetirementTotal reports whether this reason is a member of the
// §16.1 retirement-reason vocabulary, which the spec freezes to exactly the
// three retirement-limit triggers: session_count_limit, uptime_limit, and
// scrub_failure_limit. That vocabulary partitions across two counters
// (lenny_gateway_pod_retirement_total for session_count_limit and
// scrub_failure_limit, lenny_controller_pod_retirement_total for uptime_limit);
// this predicate reports membership in the union, and the gateway's
// applyDisposition additionally suppresses its own uptime_limit emission. A
// retire whose reason is not one of the three (the onScrubFailure: fail
// termination, which §16.1.1 classifies as a failure carried on error_type
// rather than reason, and the §6.39 cordon-drain operational retire) drives the
// drain disposition and records its reason in the audit trail but is not
// counted on either retirement counter, so the emitter never widens the frozen
// label set. spec: spec/16 §16.1 (retirement reason label set), §16.1.1 (reason
// is reserved for the lifecycle limit triggers; failures use error_type).
func (r RetireReason) CountsOnRetirementTotal() bool {
	switch r {
	case ReasonSessionCountLimit, ReasonMaxUptimeExceeded, ReasonScrubFailuresExhausted:
		return true
	default:
		return false
	}
}

// Disposition is the resolved §6.2 task_cleanup branch.
type Disposition struct {
	// Ready is false only when the scrub result is still ScrubPending:
	// the driver has nothing to write yet and waits for the adapter.
	Ready bool

	// NextPhase is the §6.2 phase to advance to. It is "" when Ready is
	// false.
	NextPhase state.State

	// ScrubWarning is true when the pod carries the scrub_warning
	// annotation into NextPhase. It is set only on the reuse (reserve or
	// re-warm) and cordon-drain paths under a warn-policy scrub failure
	// (spec: §6.2 recycle disposition — the annotation persists through the
	// reserve and the re-warm). The limit-based retirement drains and the
	// fail-policy termination clear it: the pod is leaving the pool for
	// cause, so the warning is superseded and the spec brackets none of
	// those edges with [scrub_warning].
	ScrubWarning bool

	// Retire is true when NextPhase removes the pod from the warm pool
	// (draining or failed). A retire whose Reason reports
	// CountsOnRetirementTotal is a member of the frozen §16.1 vocabulary; the
	// gateway's applyDisposition drives lenny_gateway_pod_retirement_total for
	// session_count_limit and scrub_failure_limit and suppresses its
	// uptime_limit emission (the controller owns that count). A retire outside
	// that frozen vocabulary (the §6.39 cordon-drain, the fail-policy
	// termination) drains without incrementing either counter.
	Retire bool

	// Reason is the stable observability/audit label for the branch. Whether
	// it is a member of the §16.1 retirement-reason vocabulary (partitioned
	// across lenny_gateway_pod_retirement_total and
	// lenny_controller_pod_retirement_total) is reported by
	// Reason.CountsOnRetirementTotal.
	Reason RetireReason
}

// Decide maps the recycle-disposition inputs to the single §6.2
// disposition. The precedence is: pending (wait) → fail-policy
// termination → scrub exhaustion → vm-restart reprovision retire →
// count/uptime retirement → host-schedulability retire gate → reuse
// (preConnect re-warm or non-preConnect reserve). Higher-precedence
// retirement reasons short-circuit lower ones, so a pod that has both
// failed its scrub (under warn, not exhausted) and reached
// recycle.maxSessionsPerPod retires on session_count_limit rather than
// re-entering the pool. The vm-restart reprovision retire (§5.2 step 7)
// sits below the fail-policy and scrub-exhaustion retirements but above the
// session-count, uptime, and host-schedulability branches, so every
// occupancy-zero retire on a vm-restart pool uses the non-counting
// vm_restart_reprovision reason. The host-schedulability retire (§6.39)
// sits below the limit retirements but above every reuse path, so it
// preempts both the preConnect re-warm and the non-preConnect reserve when
// the host node is cordoned.
func Decide(in Inputs) Disposition {
	if in.Scrub == ScrubPending {
		return Disposition{} // Ready == false; the driver waits.
	}

	maxScrub := in.MaxScrubFailures
	if maxScrub <= 0 {
		maxScrub = DefaultMaxScrubFailures
	}
	scrubFailed := in.Scrub == ScrubFailed
	failPolicy := in.OnCleanupFailure == OnCleanupFail

	// Line 156: onCleanupFailure: fail terminates the pod on any scrub
	// failure, before any reuse or retirement-limit evaluation.
	if scrubFailed && failPolicy {
		return Disposition{
			Ready:     true,
			NextPhase: state.Failed,
			Retire:    true,
			Reason:    ReasonCleanupFailPolicy,
		}
	}

	// warned is true when this task's scrub failed under the warn
	// policy: the pod carries scrub_warning onto any reuse/cordon-drain
	// path (lines 148/153/155).
	warned := scrubFailed // fail-policy already returned above.

	// Line 149: the cumulative scrub-failure count reached the limit.
	// The pod is retired for repeated residual-state risk; the scrub_warning
	// is superseded by the retirement.
	if scrubFailed && in.ScrubFailureCount >= maxScrub {
		return Disposition{
			Ready:     true,
			NextPhase: state.Draining,
			Retire:    true,
			Reason:    ReasonScrubFailuresExhausted,
		}
	}

	// spec: spec/05 §5.2 step 7 — a vm-restart pool retires the pod at the
	// occupancy-zero recycle boundary and the warm pool provisions a fresh
	// replacement (a fresh guest VM). Reusing the pod here would return it to
	// cross-tenant service without a fresh guest (fail-open), which this branch
	// closes. The branch sits after the fail-policy and scrub-exhausted retire
	// branches so a genuine onScrubFailure: fail failure keeps its fail-closed
	// Failed disposition and a cumulative scrub-failure-limit retire keeps its
	// counting ReasonScrubFailuresExhausted reason (a scrub-failure limit is a
	// genuine limit trigger even on a vm-restart pool). It sits BEFORE the
	// session-count, uptime, and host-unschedulable branches so every
	// occupancy-zero vm-restart retire uses the non-counting
	// ReasonVMRestartReprovision uniformly, including a maxSessionsPerPod: 1 pool
	// whose read-back served-session count equals maxSessionsPerPod at the
	// recycle boundary: placing the branch after the session-count branch would
	// retire that pod with the counting ReasonSessionCountLimit and diverge the
	// retire reason between two otherwise-identical one-session-per-pod pools on
	// whether maxSessionsPerPod is 1 or >= 2. ScrubWarning carries the
	// scrub_warning annotation onto the drain when this task's scrub failed under
	// the warn policy, mirroring the cordon-drain retire below; on a clean scrub
	// warned is false and no annotation is stamped.
	if in.VMRestart {
		return Disposition{
			Ready:        true,
			NextPhase:    state.Draining,
			ScrubWarning: warned,
			Retire:       true,
			Reason:       ReasonVMRestartReprovision,
		}
	}

	// Line 150: the pod served its last permitted session.
	if in.MaxSessionsPerPod > 0 && in.SessionsServed >= in.MaxSessionsPerPod {
		return Disposition{
			Ready:     true,
			NextPhase: state.Draining,
			Retire:    true,
			Reason:    ReasonSessionCountLimit,
		}
	}

	// Line 151: the pod reached its uptime cap.
	if in.MaxPodUptimeSeconds > 0 && in.PodUptimeSeconds >= in.MaxPodUptimeSeconds {
		return Disposition{
			Ready:     true,
			NextPhase: state.Draining,
			Retire:    true,
			Reason:    ReasonMaxUptimeExceeded,
		}
	}

	// Not retiring on a limit. §6.39 host-node schedulability retire: a
	// recycling pod on a cordoned host is retired rather than reused, on
	// BOTH preConnect and non-preConnect pools. Holding a non-preConnect
	// pod in `reserved` or re-warming a preConnect pod on a node whose
	// eviction is imminent would hand the next session a soon-to-be-evicted
	// pod, so the disposition drains instead. The scrub_warning annotation
	// persists onto the cordon-drain. An absent or "false" label reads as
	// unschedulable (the caller passes HostSchedulable false), so this is
	// fail-safe.
	if !in.HostSchedulable {
		return Disposition{
			Ready:        true,
			NextPhase:    state.Draining,
			ScrubWarning: warned,
			Retire:       true,
			Reason:       ReasonHostUnschedulable,
		}
	}

	// Reuse on a schedulable host. A non-preConnect pod is held for its
	// pinned tenant through the claim's `reserved` hold window: with the
	// per-pod occupancy claim a scrubbed pod is not returned straight to
	// idle but reserved so a back-to-back same-tenant session rebinds
	// without re-acquiring the pod (spec: §5.2 recycle lifecycle, line 449).
	if !in.PreConnect {
		return Disposition{
			Ready:        true,
			NextPhase:    state.Reserved,
			ScrubWarning: warned,
			Reason:       ReasonReuse,
		}
	}

	// A preConnect pod re-warms the SDK before the claim enters `reserved`
	// (the re-warm leg projects sdk_connecting); the disposition selects
	// sdk_connecting and the gateway stamps rewarmStartedAt to anchor the
	// re-warm watchdog. The scrub_warning annotation persists through the
	// re-warm.
	return Disposition{
		Ready:        true,
		NextPhase:    state.SDKConnecting,
		ScrubWarning: warned,
		Reason:       ReasonReuse,
	}
}
