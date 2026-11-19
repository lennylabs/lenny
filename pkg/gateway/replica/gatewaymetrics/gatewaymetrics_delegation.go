// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// delegationMetrics holds the §8.x delegation tree-cycle, quota-reconcile,
// and parallel-children metrics. The delegation depth, self-recursion,
// deadlock, budget-reconstruction, and lease-extension metrics are
// registered by the platform tools packages that emit them.
type delegationMetrics struct {
	// delegationTreeCycleDetected counts §8.9 tree-walker cycle
	// detections. Labels `tenant_id` and `source` (`rest` for the
	// /v1/sessions/{id}/tree handler, `mcp` for the lenny/get_task_tree
	// platform tool and lenny/await_children tree walks). Emission
	// implies a corrupt ParentSessionID lineage that bypassed the §8.2
	// pre-delegation cycle detector — typically a §8.10 recovery write
	// that re-parented a node. spec: §8.9; F-8.9.10.
	delegationTreeCycleDetected *prometheus.CounterVec
	// quotaCheckpointReconcile counts §11.2 / §24.6 token-usage
	// counter reconcile events. Label `outcome` is one of `restored` (the
	// MAX rule was applied to a still-current window) or `skipped` (the
	// checkpoint's window had already rolled over). spec: §11.2;
	// §24.6; F-11.2.4 / F-24.6.3.
	quotaCheckpointReconcile *prometheus.CounterVec
	// delegationParallelChildrenHWM observes the §8.3 maximum
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
	// §16.1 / §11.2 — `lenny_quota_checkpoint_reconcile_total`
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
	// §16.1 / §8.3 — `lenny_delegation_parallel_children_high_watermark`
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
	reg.MustRegister(delegationTreeCycleDetected,
		quotaCheckpointReconcile,
		delegationParallelChildrenHWM)
	m.delegationTreeCycleDetected = delegationTreeCycleDetected
	m.quotaCheckpointReconcile = quotaCheckpointReconcile
	m.delegationParallelChildrenHWM = delegationParallelChildrenHWM
	return m, nil
}
