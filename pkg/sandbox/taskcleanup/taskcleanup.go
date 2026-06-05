// SPDX-License-Identifier: MIT

// Package taskcleanup implements the §6.2 task_cleanup disposition
// branching (spec: spec/06_warm-pod-model.md lines 142-188). After a
// task-mode pod finishes (or its task is cancelled) the pod enters
// task_cleanup, runs the credential purge, cleanupCommands, and the
// Lenny scrub, and then must advance to exactly one of:
//
//	idle                       (non-preConnect, scrub ok, not retiring)
//	idle [scrub_warning]       (non-preConnect, scrub failed under warn)
//	sdk_connecting             (preConnect, scrub ok, schedulable host)
//	sdk_connecting [scrub_warning] (preConnect, warn-failed, schedulable)
//	draining                   (a retirement limit reached, or cordoned host)
//	draining [scrub_warning]   (preConnect, warn-failed, cordoned host)
//	failed                     (onCleanupFailure: fail)
//
// Decide is a pure function so the branch table can be exhaustively
// unit-tested against the spec edges. The driver that supplies the
// inputs (the scrub outcome, the cumulative counters, and the
// lenny.dev/host-schedulable pod label read at the moment of the
// transition per spec line 181) and writes the resulting phase is the
// gateway task path; this package holds no Kubernetes client and emits
// no metrics, it only decides.
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
// reads. All counts are evaluated AFTER the just-finished task: a pod
// that has run its last permitted task has TasksCompleted ==
// MaxTasksPerPod.
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
	// MaxScrubFailures. spec: §5.2 lenny_task_pod_scrub_failure_count.
	ScrubFailureCount int

	// MaxScrubFailures is taskPolicy.maxScrubFailures. A value <= 0 is
	// treated as DefaultMaxScrubFailures.
	MaxScrubFailures int

	// TasksCompleted is the count of tasks this pod has finished,
	// INCLUDING the task that just completed.
	TasksCompleted int

	// MaxTasksPerPod is taskPolicy.maxTasksPerPod (required for a
	// task-mode pool, >= 1). A value <= 0 disables the count-based cap
	// (defensive; admission rejects such a pool).
	MaxTasksPerPod int

	// PodUptimeSeconds is the pod's wall-clock uptime in seconds.
	PodUptimeSeconds int64

	// MaxPodUptimeSeconds is taskPolicy.maxPodUptimeSeconds. A value
	// <= 0 means no uptime cap. spec: §6.2 line 151.
	MaxPodUptimeSeconds int64

	// HostSchedulable reflects the lenny.dev/host-schedulable pod label:
	// true only when the label reads "true". An absent or "false" label
	// is fail-safe-unschedulable, so the caller passes false. spec: §6.2
	// line 181 — "absent, which is treated as unschedulable".
	HostSchedulable bool
}

// RetireReason is a stable label for the disposition the driver records
// on lenny_task_pod_retirement_total{reason} and in the audit trail.
type RetireReason string

const (
	// ReasonReuse: the pod returns to the warm pool (idle or
	// sdk_connecting). Not a retirement.
	ReasonReuse RetireReason = "reuse"
	// ReasonCleanupFailPolicy: onCleanupFailure: fail terminated the
	// pod on a scrub failure. spec: §6.2 line 156.
	ReasonCleanupFailPolicy RetireReason = "cleanup_fail_policy"
	// ReasonScrubFailuresExhausted: the cumulative scrub-failure count
	// reached maxScrubFailures. spec: §6.2 line 149.
	ReasonScrubFailuresExhausted RetireReason = "scrub_failures_exhausted"
	// ReasonMaxTasksReached: the pod completed maxTasksPerPod tasks.
	// spec: §6.2 line 150.
	ReasonMaxTasksReached RetireReason = "max_tasks_reached"
	// ReasonMaxUptimeExceeded: pod uptime reached maxPodUptimeSeconds.
	// spec: §6.2 line 151.
	ReasonMaxUptimeExceeded RetireReason = "max_uptime_exceeded"
	// ReasonHostUnschedulable: a preConnect pod cannot re-warm because
	// its host node is cordoned, so it drains instead of producing a
	// soon-to-be-evicted idle pod. spec: §6.2 lines 152-153, 181.
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
	// annotation into NextPhase. It is set only on the reuse and
	// cordon-drain paths under a warn-policy scrub failure (spec: §6.2
	// lines 148/153/155, and line 183 — the annotation persists through
	// the re-warm). The limit-based retirement drains (lines 149-151)
	// and the fail-policy termination (line 156) clear it: the pod is
	// leaving the pool for cause, so the warning is superseded and the
	// spec brackets none of those edges with [scrub_warning].
	ScrubWarning bool

	// Retire is true when NextPhase removes the pod from the warm pool
	// (draining or failed). It drives lenny_task_pod_retirement_total.
	Retire bool

	// Reason is the stable observability/audit label for the branch.
	Reason RetireReason
}

// Decide maps the task_cleanup inputs to the single §6.2 disposition.
// The precedence is: pending (wait) → fail-policy termination → scrub
// exhaustion → count/uptime retirement → host-schedulable re-warm gate →
// reuse. Higher-precedence retirement reasons short-circuit lower ones,
// so a pod that has both failed its scrub (under warn, not exhausted)
// and reached maxTasksPerPod retires on max_tasks_reached rather than
// re-entering the pool.
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

	// Line 150: the pod completed its last permitted task.
	if in.MaxTasksPerPod > 0 && in.TasksCompleted >= in.MaxTasksPerPod {
		return Disposition{
			Ready:     true,
			NextPhase: state.Draining,
			Retire:    true,
			Reason:    ReasonMaxTasksReached,
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

	// Not retiring on a limit. Non-preConnect pods return straight to
	// idle (lines 147/148) and never re-warm, so node schedulability
	// does not gate them.
	if !in.PreConnect {
		return Disposition{
			Ready:        true,
			NextPhase:    state.Idle,
			ScrubWarning: warned,
			Reason:       ReasonReuse,
		}
	}

	// preConnect pods must re-warm the SDK before returning to idle, and
	// re-warm only on a schedulable host (line 181 fail-safe): a cordoned
	// host drains the pod instead of handing the next claim a
	// soon-to-be-evicted idle pod (lines 152-153). The scrub_warning
	// annotation persists through both the cordon-drain and the re-warm.
	if !in.HostSchedulable {
		return Disposition{
			Ready:        true,
			NextPhase:    state.Draining,
			ScrubWarning: warned,
			Retire:       true,
			Reason:       ReasonHostUnschedulable,
		}
	}

	// Lines 154/155: re-warm the SDK before idle.
	return Disposition{
		Ready:        true,
		NextPhase:    state.SDKConnecting,
		ScrubWarning: warned,
		Reason:       ReasonReuse,
	}
}
