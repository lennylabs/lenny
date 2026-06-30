// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// orphanMetrics holds the §8.10 / §10.1 orphan-cleanup, tree-recovery, and pod-state-mirror metrics.
type orphanMetrics struct {
	// maxOrphanTasksPerTenant is the §8.10 line 1103 deployer-configured
	// orphan-cap exposed as an unlabeled gauge so the §16.5
	// OrphanTasksPerTenantHigh alert resolves
	// `scalar(lenny_max_orphan_tasks_per_tenant)` to the live ceiling.
	// F-8.10.13.
	maxOrphanTasksPerTenant prometheus.Gauge
	// §8.10 / §16.1 orphan-cleanup and tree-recovery observability.
	// orphanCleanupRuns counts the §8.10 line 1091 background sweep
	// invocations; one Inc per Tick regardless of outcome. orphanTasksTerminated
	// counts the per-sweep terminated-orphan count (in lockstep with the
	// existing log line). orphanTasksActive is the fleet-wide active orphan
	// gauge (sum over tenants); orphanTasksActivePerTenant is the per-tenant
	// gauge the OrphanTasksPerTenantHigh alert reads. treeRecoveryDuration
	// observes one wall-clock duration per tree-recovery operation;
	// treeRecoveryTimeout counts the per-timeout-type rollups.
	// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 144-149. F-8.10.7.
	orphanCleanupRuns          prometheus.Counter
	orphanTasksTerminated      prometheus.Counter
	orphanTasksActive          prometheus.Gauge
	orphanTasksActivePerTenant *prometheus.GaugeVec
	treeRecoveryDuration       *prometheus.HistogramVec
	treeRecoveryTimeout        *prometheus.CounterVec
	// §10.1 orphan-session reconciler observability.
	// orphanSessionReconciliations counts each session the §10.1 line 51
	// reconciler forces to `failed` after its bound pod terminated with
	// no terminal event. agentPodStateMirrorLag is the per-pool staleness
	// gauge the §16.5 PodStateMirrorStale alert reads; the reconciler
	// publishes it once per pool per pass.
	// spec: §10.1 line 51; §16.1.
	orphanSessionReconciliations prometheus.Counter
	agentPodStateMirrorLag       *prometheus.GaugeVec
}

// newOrphanMetrics constructs, registers, and materializes the orphan metric subsystem
// against reg. spec: §16 observability metrics.
func newOrphanMetrics(reg *prometheus.Registry) (orphanMetrics, error) {
	var m orphanMetrics
	// spec: §8.10 line 1103, §16.5 OrphanTasksPerTenantHigh alert reads
	// `scalar(lenny_max_orphan_tasks_per_tenant)` as the cap denominator.
	// Exposing the ceiling as an unlabeled gauge lets the alert resolve
	// without hard-coding a value, so a deployer override flows through
	// to the rule automatically. F-8.10.13.
	maxOrphanTasksPerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_max_orphan_tasks_per_tenant",
		Help: "Configured maxOrphanTasksPerTenant ceiling — drives the OrphanTasksPerTenantHigh alert threshold (§8.10 line 1103).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 144-149 — the
	// orphan-cleanup + tree-recovery observability surface. F-8.10.7.
	orphanCleanupRuns := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_cleanup_runs_total",
		Help: "Background orphan cleanup job executions (§8.10 line 1091 / §16.1).",
	})
	orphanTasksTerminated := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_tasks_terminated",
		Help: "Orphan tasks terminated by the §8.10 cleanup job (§16.1).",
	})
	orphanTasksActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active",
		Help: "Currently active orphan tasks awaiting cleanup, summed across tenants (§8.10 / §16.1).",
	}, nil)
	if err != nil {
		return m, err
	}
	orphanTasksActivePerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active_per_tenant",
		Help: "Per-tenant active orphan task count; drives the OrphanTasksPerTenantHigh alert (§8.10 line 1103 / §16.1).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	orphanSessionReconciliations := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_session_reconciliations_total",
		Help: "Orphan sessions forcibly transitioned to failed by the §10.1 reconciler (§10.1 line 51 / §16.1).",
	})
	agentPodStateMirrorLag, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_agent_pod_state_mirror_lag_seconds",
		Help: "Seconds since the last agent_pod_state mirror update per pool; drives the §16.5 PodStateMirrorStale alert (§10.1 line 51).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	treeRecoveryDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_tree_recovery_duration_seconds",
		Help:    "Delegation tree-recovery wall-clock duration by outcome (§8.10 / §16.1 line 144).",
		Buckets: []float64{0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1200, 1800},
	}, []string{"pool", "outcome"})
	if err != nil {
		return m, err
	}
	treeRecoveryTimeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_recovery_timeout_total",
		Help: "Delegation tree-recovery timeouts by timeout type (`level` | `tree`) (§8.10 / §16.1 line 145).",
	}, []string{"pool", "timeout_type"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(maxOrphanTasksPerTenant,
		orphanCleanupRuns,
		orphanTasksTerminated,
		orphanTasksActive,
		orphanTasksActivePerTenant,
		orphanSessionReconciliations,
		agentPodStateMirrorLag,
		treeRecoveryDuration,
		treeRecoveryTimeout)
	m.maxOrphanTasksPerTenant = maxOrphanTasksPerTenant.WithLabelValues()
	m.orphanCleanupRuns = orphanCleanupRuns
	m.orphanTasksTerminated = orphanTasksTerminated
	m.orphanTasksActive = orphanTasksActive.WithLabelValues()
	m.orphanTasksActivePerTenant = orphanTasksActivePerTenant
	m.treeRecoveryDuration = treeRecoveryDuration
	m.treeRecoveryTimeout = treeRecoveryTimeout
	m.orphanSessionReconciliations = orphanSessionReconciliations
	m.agentPodStateMirrorLag = agentPodStateMirrorLag
	return m, nil
}
