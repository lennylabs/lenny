// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// delegationMetrics holds the §8.x delegation depth, cycle, deadlock, and budget-reconstruction metrics.
type delegationMetrics struct {
	// delegationDepth observes the §16.1 / §8.2 per-session delegation
	// depth at admission. The catalog comment positions it as a
	// session-completion histogram; depth is set at admission and
	// invariant for the session's lifetime, so the distribution is
	// identical whether sampled at admission or terminal. Labelled by
	// `pool` per §16.1.
	delegationDepth *prometheus.HistogramVec
	// delegationWouldHaveBlocked counts §8.2 self-recursion hops where
	// any layer of the three-layer AND gate evaluated `false`. Labels
	// `pool`, `tenant_id`, `layer` (`platform` | `runtime` | `policy`),
	// and `mode` (`enforce` | `warn`). Under `mode: enforce` this is a
	// counter of "rejection causes" (the delegation is rejected, one
	// row per failing layer); under `mode: warn` it counts the same
	// per-layer breakdown for diagnostic rollouts (the delegation is
	// admitted). Not emitted under `mode: permissive`.
	delegationWouldHaveBlocked *prometheus.CounterVec
	// delegationTreeCycleDetected counts §8.9 tree-walker cycle
	// detections. Labels `tenant_id` and `source` (`rest` for the
	// /v1/sessions/{id}/tree handler, `mcp` for the lenny/get_task_tree
	// platform tool and lenny/await_children tree walks). Emission
	// implies a corrupt ParentSessionID lineage that bypassed the §8.2
	// pre-delegation cycle detector — typically a §8.10 recovery write
	// that re-parented a node. spec: §8.9 line 1003; F-8.9.10.
	delegationTreeCycleDetected *prometheus.CounterVec
	// delegationDeadlockDetected counts §8.8 line 981 subtree-deadlock
	// detections, labelled by tenant. The detector increments it once
	// per newly-detected deadlocked subtree root (not per sweep tick).
	// F-8.8.6.
	delegationDeadlockDetected *prometheus.CounterVec
	// delegationDeadlockResolution counts §8.8 deadlock resolutions by
	// `resolution` (`resolved` when the root broke the deadlock before
	// `willTimeoutAt`, `timeout` when the detector applied DEADLOCK_TIMEOUT).
	// F-8.8.6.
	delegationDeadlockResolution *prometheus.CounterVec
	// delegationDeadlockDuration observes the §8.8 time from detection to
	// resolution (seconds), labelled by the same `resolution` outcome.
	// F-8.8.6.
	delegationDeadlockDuration *prometheus.HistogramVec
	// delegationBudgetReconstruction counts §11.2 line 48 delegation tree
	// budget reconstruction events on Redis recovery. Label `outcome` is
	// one of `success` (counters restored via the MAX rule) or
	// `irrecoverable` (checkpoint too stale and live state unenumerable,
	// so the tree root was moved to awaiting_client_action). spec: §11.2
	// line 48; §12.4 line 218; F-11.2.5.
	delegationBudgetReconstruction *prometheus.CounterVec
	// quotaCheckpointReconcile counts §11.2 line 48 / §24.6 token-usage
	// counter reconcile events. Label `outcome` is one of `restored` (the
	// MAX rule was applied to a still-current window) or `skipped` (the
	// checkpoint's window had already rolled over). spec: §11.2 line 48;
	// §24.6 line 99; F-11.2.4 / F-24.6.3.
	quotaCheckpointReconcile *prometheus.CounterVec
	// delegationParallelChildrenHWM observes the §8.3 line 379 maximum
	// simultaneous in-flight children per delegation tree, sampled once
	// when the tree root reaches a terminal state. Labels `pool` and
	// `tenant_id` per §16.1; `root_session_id` is deliberately not a
	// label (unbounded cardinality). F-8.9.6.
	delegationParallelChildrenHWM *prometheus.HistogramVec
}

// newDelegationMetrics constructs, registers, and materializes the delegation metric subsystem
// against reg. spec: §16 observability metrics.
func newDelegationMetrics(reg *prometheus.Registry) (delegationMetrics, error) {
	var m delegationMetrics
	// §16.1 / §8.2 — `lenny_delegation_depth` per-session delegation
	// depth histogram, labelled by `pool` (per §16.1). Buckets cover
	// the §8.2.bis maxDelegationDepth ceiling and a head-room margin.
	delegationDepth, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_depth",
		Help:    "Per-session delegation depth observed at delegation admission (§8.2).",
		Buckets: []float64{0, 1, 2, 3, 5, 8, 13, 21},
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §16.1 / §8.2 — `lenny_delegation_would_have_blocked_total` counter
	// of self-recursion hops where any layer of the §8.2 three-layer
	// AND gate evaluated `false`. Labels match the §16.1 catalog row
	// (`pool`, `tenant_id`, `layer`, `mode`).
	delegationWouldHaveBlocked, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_would_have_blocked_total",
		Help: "Self-recursion would-have-blocked counter by layer and cycle-detection mode (§8.2).",
	}, []string{"pool", "tenant_id", "layer", "mode"})
	if err != nil {
		return m, err
	}
	// §16.1 / §8.9 — `lenny_delegation_tree_cycle_detected_total`
	// counts the tree-walker defensive cycle hits. The §8.2 cycle
	// detector prevents cycles at delegation time; a non-zero rate
	// here implies the persistent store has been corrupted (e.g., a
	// §8.10 recovery write re-parented a node). F-8.9.10.
	delegationTreeCycleDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_cycle_detected_total",
		Help: "Tree walker hit a cycle in the §8.2 ParentSessionID lineage (corrupt store).",
	}, []string{"tenant_id", "source"})
	if err != nil {
		return m, err
	}
	// §16.1 / §8.8 line 981 — the subtree deadlock detector counters.
	// `detected_total` bumps once per newly-detected deadlocked subtree;
	// `resolution_total` and `duration_seconds` close out each tracked
	// deadlock when the root resolves it or the detector times it out.
	// F-8.8.6.
	delegationDeadlockDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_deadlock_detected_total",
		Help: "Subtree deadlock detections (§8.8 line 981 heuristic), by tenant.",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	delegationDeadlockResolution, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_deadlock_resolution_total",
		Help: "Subtree deadlock resolutions by resolution (resolved | timeout).",
	}, []string{"resolution"})
	if err != nil {
		return m, err
	}
	delegationDeadlockDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_deadlock_duration_seconds",
		Help:    "Time from §8.8 deadlock detection to resolution, by resolution outcome.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300},
	}, []string{"resolution"})
	if err != nil {
		return m, err
	}
	// §16.1 / §11.2 line 48 — `lenny_delegation_budget_reconstruction_total`
	// counts delegation tree budget reconstruction events on Redis
	// recovery, labelled by outcome (`success` | `irrecoverable`) so
	// operators monitor reconstruction volume and detect trees that could
	// not be reconstructed. F-11.2.5.
	delegationBudgetReconstruction, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_budget_reconstruction_total",
		Help: "Delegation budget reconstruction events by outcome (success | irrecoverable).",
	}, []string{"outcome"})
	if err != nil {
		return m, err
	}
	// §16.1 / §11.2 line 48 — `lenny_quota_checkpoint_reconcile_total`
	// counts token-usage counter reconcile events on Redis recovery and on
	// the §24.6 operator-driven reconcile, labelled by outcome
	// (`restored` | `skipped`) so operators see how many counters the MAX
	// rule restored versus dropped as stale-window. F-11.2.4 / F-24.6.3.
	quotaCheckpointReconcile, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_quota_checkpoint_reconcile_total",
		Help: "Token-usage counter reconcile events by outcome (restored | skipped).",
	}, []string{"outcome"})
	if err != nil {
		return m, err
	}
	// §16.1 / §8.3 line 379 — `lenny_delegation_parallel_children_high_watermark`
	// records the maximum simultaneous in-flight children observed for
	// each delegation tree at tree completion, labelled by `pool` and
	// `tenant_id`. Buckets cover the typical maxParallelChildren range
	// (the §8.2 default is 4) with head-room above it. F-8.9.6.
	delegationParallelChildrenHWM, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_parallel_children_high_watermark",
		Help:    "Maximum simultaneous in-flight children per delegation tree at completion (§8.3).",
		Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64},
	}, []string{"pool", "tenant_id"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(delegationDepth,
		delegationWouldHaveBlocked,
		delegationTreeCycleDetected,
		delegationDeadlockDetected,
		delegationDeadlockResolution,
		delegationDeadlockDuration,
		delegationBudgetReconstruction,
		quotaCheckpointReconcile,
		delegationParallelChildrenHWM)
	m.delegationDepth = delegationDepth
	m.delegationWouldHaveBlocked = delegationWouldHaveBlocked
	m.delegationTreeCycleDetected = delegationTreeCycleDetected
	m.delegationDeadlockDetected = delegationDeadlockDetected
	m.delegationDeadlockResolution = delegationDeadlockResolution
	m.delegationDeadlockDuration = delegationDeadlockDuration
	m.delegationBudgetReconstruction = delegationBudgetReconstruction
	m.quotaCheckpointReconcile = quotaCheckpointReconcile
	m.delegationParallelChildrenHWM = delegationParallelChildrenHWM
	return m, nil
}
