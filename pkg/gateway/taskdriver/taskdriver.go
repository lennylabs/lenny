// SPDX-License-Identifier: MIT

// Package taskdriver is the gateway-side §5.2 / §6.2 task-mode
// retirement engine. It maintains the cumulative per-pod task counters
// (completed-task count and scrub-failure count), resolves each
// completed task's task_cleanup disposition through the pure
// pkg/sandbox/taskcleanup.Decide branch table, emits the §5.2 retirement
// metrics, and enforces the §6.6 maxTaskRetries per-task crash budget.
//
// spec: spec/05_runtime-registry-and-pool-model.md (taskPolicy),
// spec/06_warm-pod-model.md lines 142-188 (task_cleanup disposition).
//
// This is the gateway-side consumer of the adapter primitives
// (pkg/adapter/scrub, pkg/adapter/lifecyclechannel, taskcleanup.Decide).
// The between-task reuse loop that feeds it — the adapter→gateway task
// transport, the pod-reuse seam, and the /v1/tasks control plane — is the
// remaining F-5.2.30 workstream; this package holds the disposition and
// metric logic that loop drives, so the policy is unit-tested against the
// §6.2 edges independently of the transport.
package taskdriver

import (
	"time"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
)

// DefaultMaxTaskRetries is the §5.2 / §6.6 taskPolicy.maxTaskRetries
// default applied when a pool leaves it unset: one retry, for two total
// attempts of a task that crashes its pod mid-execution.
const DefaultMaxTaskRetries = 1

// Policy is the resolved §5.2 taskPolicy for a task-mode pool, as the
// gateway reads it from the runtimestore. The values are already
// resolved (defaults applied) by the caller.
type Policy struct {
	// PreConnect mirrors the pool runtime's capabilities.preConnect: an
	// idle pod is SDK-warm and re-warms through sdk_connecting between
	// tasks. spec: §6.2 line 76.
	PreConnect bool
	// OnCleanupFailure is taskPolicy.onCleanupFailure (warn or fail).
	OnCleanupFailure taskcleanup.CleanupFailurePolicy
	// MaxScrubFailures is taskPolicy.maxScrubFailures (default applied by
	// taskcleanup when <= 0).
	MaxScrubFailures int
	// MaxTasksPerPod is taskPolicy.maxTasksPerPod (required, >= 1).
	MaxTasksPerPod int
	// MaxPodUptimeSeconds is taskPolicy.maxPodUptimeSeconds (0 = no cap).
	MaxPodUptimeSeconds int64
	// MaxTaskRetries is taskPolicy.maxTaskRetries: the per-task crash
	// re-dispatch budget beyond the first attempt. A negative value is
	// clamped to zero. spec: §6.6 / §5.2.
	MaxTaskRetries int
}

// Metrics is the §5.2 / §16.1 pod-retirement metric sink. A nil sink is
// the no-op default. The production wiring adapts it onto gatewaymetrics:
// the §16.1 lenny_pod_scrub_failure_count gauge and
// lenny_pod_retirement_total counter, plus the §5.2-named aggregate
// lenny_pod_scrub_failure_total (named only in §5.2, outside the §16.1
// catalog).
type Metrics interface {
	// SetPodScrubFailureCount reports the pod's cumulative scrub-failure
	// count (lenny_pod_scrub_failure_count gauge, per k8s_pod_name).
	SetPodScrubFailureCount(pool, podName string, count int)
	// IncPodRetirement records a pod retirement by reason
	// (lenny_pod_retirement_total{reason}).
	IncPodRetirement(pool string, reason taskcleanup.RetireReason)
	// IncScrubFailureTotal increments the aggregate scrub-failure counter
	// (lenny_pod_scrub_failure_total) on each failed scrub.
	IncScrubFailureTotal(pool string)
}

// NoopMetrics discards every retirement metric. It is the default sink
// when no Metrics is supplied.
type NoopMetrics struct{}

// SetPodScrubFailureCount implements Metrics.
func (NoopMetrics) SetPodScrubFailureCount(string, string, int) {}

// IncPodRetirement implements Metrics.
func (NoopMetrics) IncPodRetirement(string, taskcleanup.RetireReason) {}

// IncScrubFailureTotal implements Metrics.
func (NoopMetrics) IncScrubFailureTotal(string) {}

// PodTaskState is the cumulative §5.2 task state of one task-mode pod
// across the tasks it has executed. The driver advances it as each task
// completes; taskcleanup.Decide reads the post-task totals. It is not
// safe for concurrent use — a task-mode pod runs one task at a time, and
// the driver owns the pod's state for the duration.
type PodTaskState struct {
	// PodName is the k8s pod name, the lenny_pod_scrub_failure_count
	// gauge label.
	PodName string
	// Pool is the pool the pod belongs to, the retirement-metric label.
	Pool string
	// BootTime is the pod's first-boot wall clock, used to compute uptime.
	BootTime time.Time
	// TasksCompleted is the count of tasks this pod has finished.
	TasksCompleted int
	// ScrubFailureCount is the cumulative count of failed scrubs.
	ScrubFailureCount int
}

// NewPodTaskState returns the zero-task state for a freshly-claimed
// task-mode pod booted at bootTime.
func NewPodTaskState(podName, pool string, bootTime time.Time) *PodTaskState {
	return &PodTaskState{PodName: podName, Pool: pool, BootTime: bootTime}
}

// Evaluator resolves §6.2 task_cleanup dispositions for a task-mode pool
// and emits the §5.2 retirement metrics. One Evaluator serves a pool
// (its Policy); the per-pod counters live in PodTaskState.
type Evaluator struct {
	policy  Policy
	metrics Metrics
	now     func() time.Time
}

// NewEvaluator returns an evaluator for the pool's resolved Policy. A nil
// metrics sink is replaced with NoopMetrics.
func NewEvaluator(policy Policy, metrics Metrics) *Evaluator {
	if metrics == nil {
		metrics = NoopMetrics{}
	}
	return &Evaluator{
		policy:  policy,
		metrics: metrics,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the evaluator clock; tests use it for deterministic
// uptime computation.
func (e *Evaluator) SetClock(now func() time.Time) { e.now = now }

// RecordCompletion advances the pod's task counters for a just-finished
// task whose Lenny scrub reported scrubResult, resolves the §6.2
// task_cleanup disposition, and emits the §5.2 retirement metrics.
//
// When scrubResult is ScrubPending the adapter has not reported a scrub
// outcome yet; the counters are not advanced, no metric is emitted, and
// the returned disposition is not-ready so the driver waits. A
// ScrubFailed result increments the cumulative scrub-failure count and
// the aggregate scrub-failure metric before the disposition is resolved
// (so taskcleanup.Decide sees the post-task count, matching §6.2 line
// 149). hostSchedulable is the lenny.dev/host-schedulable pod label read
// at the moment of the transition (§6.2 line 181); an absent or "false"
// label is fail-safe-unschedulable, so the caller passes false.
func (e *Evaluator) RecordCompletion(st *PodTaskState, scrubResult taskcleanup.ScrubResult, hostSchedulable bool) taskcleanup.Disposition {
	if scrubResult == taskcleanup.ScrubPending {
		// The adapter has not reported; wait without mutating state.
		return taskcleanup.Decide(taskcleanup.Inputs{Scrub: taskcleanup.ScrubPending})
	}

	st.TasksCompleted++
	if scrubResult == taskcleanup.ScrubFailed {
		st.ScrubFailureCount++
		// §5.2 lenny_pod_scrub_failure_total: the aggregate counts every
		// failed scrub across pods.
		e.metrics.IncScrubFailureTotal(st.Pool)
	}
	// §16.1 lenny_pod_scrub_failure_count: the per-pod cumulative
	// gauge tracks the count whether or not this task failed, so a
	// recovering pod's gauge holds its history until replacement.
	e.metrics.SetPodScrubFailureCount(st.Pool, st.PodName, st.ScrubFailureCount)

	d := taskcleanup.Decide(taskcleanup.Inputs{
		PreConnect:          e.policy.PreConnect,
		Scrub:               scrubResult,
		OnCleanupFailure:    e.policy.OnCleanupFailure,
		ScrubFailureCount:   st.ScrubFailureCount,
		MaxScrubFailures:    e.policy.MaxScrubFailures,
		SessionsServed:      st.TasksCompleted,
		MaxSessionsPerPod:   e.policy.MaxTasksPerPod,
		PodUptimeSeconds:    e.uptimeSeconds(st),
		MaxPodUptimeSeconds: e.policy.MaxPodUptimeSeconds,
		HostSchedulable:     hostSchedulable,
	})

	// §16.1 lenny_pod_retirement_total{reason}: one increment per
	// retirement (draining or failed); reuse dispositions are not counted.
	if d.Retire {
		e.metrics.IncPodRetirement(st.Pool, d.Reason)
	}
	return d
}

// uptimeSeconds returns the pod's wall-clock uptime in whole seconds,
// floored at zero so a clock skew never feeds Decide a negative uptime.
func (e *Evaluator) uptimeSeconds(st *PodTaskState) int64 {
	secs := int64(e.now().Sub(st.BootTime).Seconds())
	if secs < 0 {
		return 0
	}
	return secs
}

// ShouldRetryTask reports whether a task that crashed its pod mid-
// execution may be re-dispatched on a fresh pod, given the number of
// attempts already consumed (the first dispatch is attempt 1). The
// budget is taskPolicy.maxTaskRetries beyond the first attempt, so the
// total permitted attempts are 1 + maxTaskRetries. A negative budget is
// clamped to zero (no retry). spec: §6.6 / §5.2 maxTaskRetries.
func (e *Evaluator) ShouldRetryTask(attemptsConsumed int) bool {
	budget := e.policy.MaxTaskRetries
	if budget < 0 {
		budget = 0
	}
	// After attemptsConsumed attempts, a retry is permitted while the
	// consumed count has not exhausted the retry budget.
	return attemptsConsumed >= 1 && attemptsConsumed <= budget
}

// IsRetirePhase reports whether a resolved disposition's NextPhase
// removes the pod from the warm pool. It mirrors Disposition.Retire and
// exists so a caller writing the §6.2 pod phase can branch without
// re-deriving the terminal set.
func IsRetirePhase(s state.State) bool {
	return s == state.Draining || s == state.Failed
}
