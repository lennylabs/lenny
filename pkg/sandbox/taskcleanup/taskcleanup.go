// SPDX-License-Identifier: MIT

// Package taskcleanup implements the §6.2 recycle disposition branching
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
package taskcleanup

import "github.com/lennylabs/lenny/pkg/sandbox/state"

// DefaultMaxScrubFailures is the §5.2 taskPolicy.maxScrubFailures
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

// CleanupFailurePolicy mirrors §5.1 taskPolicy.onCleanupFailure. An
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

	// Scrub is the reported outcome of this task's Lenny scrub.
	Scrub ScrubResult

	// OnCleanupFailure is taskPolicy.onCleanupFailure (warn or fail).
	OnCleanupFailure CleanupFailurePolicy

	// ScrubFailureCount is the pod's cumulative scrub-failure count
	// INCLUDING this task's scrub. It is compared against
	// MaxScrubFailures. spec: §5.2 lenny_pod_scrub_failure_count.
	ScrubFailureCount int

	// MaxScrubFailures is taskPolicy.maxScrubFailures. A value <= 0 is
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

	// MaxPodUptimeSeconds is taskPolicy.maxPodUptimeSeconds. A value
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

// RetireReason is a stable label for the disposition the driver records
// on lenny_pod_retirement_total{reason} and in the audit trail.
type RetireReason string

const (
	// ReasonReuse: the pod returns to the warm pool (idle or
	// sdk_connecting). Not a retirement.
	ReasonReuse RetireReason = "reuse"
	// ReasonCleanupFailPolicy: onCleanupFailure: fail terminated the
	// pod on a scrub failure. spec: §6.2 line 156.
	ReasonCleanupFailPolicy RetireReason = "cleanup_fail_policy"
	// ReasonScrubFailuresExhausted: the cumulative scrub-failure count
	// reached maxScrubFailures. The emitted lenny_pod_retirement_total
	// {reason} value is the scrub_failure_limit trigger from the spec/16
	// retirement-reason vocabulary, so the counter and its emitter agree.
	// spec: spec/06 §6.2 line 149 (scrub-failure limit retire), spec/16
	// §16.1 (retirement reason vocabulary).
	ReasonScrubFailuresExhausted RetireReason = "scrub_failure_limit"
	// ReasonSessionCountLimit: the pod's served-session count reached
	// recycle.maxSessionsPerPod. The emitted lenny_pod_retirement_total
	// {reason} value is the session_count_limit trigger from the spec/16
	// retirement-reason vocabulary. spec: spec/06 §6.2 (recycle
	// disposition), spec/16 §16.1 (retirement reason vocabulary).
	ReasonSessionCountLimit RetireReason = "session_count_limit"
	// ReasonMaxUptimeExceeded: pod uptime reached maxPodUptimeSeconds.
	// The emitted lenny_pod_retirement_total{reason} value is the
	// uptime_limit trigger from the spec/16 retirement-reason vocabulary,
	// so the counter and its emitter agree. spec: §6.2 line 151, spec/16
	// §16.1 (retirement reason vocabulary).
	ReasonMaxUptimeExceeded RetireReason = "uptime_limit"
	// ReasonHostUnschedulable: the pod's host node is cordoned, so the
	// recycle disposition retires it instead of producing a
	// soon-to-be-evicted reserved or re-warmed pod. The trigger applies to
	// both preConnect (re-warm) and non-preConnect (reserve) reuse paths.
	// spec: §6.2 (recycle disposition), §6.39 (host-node schedulability
	// retire).
	ReasonHostUnschedulable RetireReason = "host_unschedulable"
)

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
	// (draining or failed). It drives lenny_pod_retirement_total.
	Retire bool

	// Reason is the stable observability/audit label for the branch.
	Reason RetireReason
}

// Decide maps the recycle-disposition inputs to the single §6.2
// disposition. The precedence is: pending (wait) → fail-policy
// termination → scrub exhaustion → count/uptime retirement →
// host-schedulability retire gate → reuse (preConnect re-warm or
// non-preConnect reserve). Higher-precedence retirement reasons
// short-circuit lower ones, so a pod that has both failed its scrub
// (under warn, not exhausted) and reached recycle.maxSessionsPerPod
// retires on session_count_limit rather than re-entering the pool. The
// host-schedulability retire (§6.39) sits below the limit retirements but
// above every reuse path, so it preempts both the preConnect re-warm and
// the non-preConnect reserve when the host node is cordoned.
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
