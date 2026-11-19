// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// orphanMetrics holds the §8.10 / §10.1 orphan-cap, orphan-session, and pod-state-mirror metrics.
type orphanMetrics struct {
	// maxOrphanTasksPerTenant is the §8.10 deployer-configured
	// orphan-cap exposed as an unlabeled gauge so the §16.5
	// OrphanTasksPerTenantHigh alert resolves
	// `scalar(lenny_max_orphan_tasks_per_tenant)` to the live ceiling.
	// F-8.10.13.
	maxOrphanTasksPerTenant prometheus.Gauge
	// §10.1 orphan-session reconciler observability.
	// orphanSessionReconciliations counts each session the §10.1.4
	// reconciler forces to `failed` after its bound pod terminated with
	// no terminal event. agentPodStateMirrorLag is the per-pool staleness
	// gauge the §16.5 PodStateMirrorStale alert reads; the reconciler
	// publishes it once per pool per pass.
	// spec: §10.1.4; §16.1.
	orphanSessionReconciliations prometheus.Counter
	agentPodStateMirrorLag       *prometheus.GaugeVec
}

// newOrphanMetrics constructs, registers, and materializes the orphan metric subsystem
// against reg. spec: §16 observability metrics.
func newOrphanMetrics(reg *prometheus.Registry) (orphanMetrics, error) {
	var m orphanMetrics
	// spec: §8.10, §16.5 OrphanTasksPerTenantHigh alert reads
	// `scalar(lenny_max_orphan_tasks_per_tenant)` as the cap denominator.
	// Exposing the ceiling as an unlabeled gauge lets the alert resolve
	// without hard-coding a value, so a deployer override flows through
	// to the rule automatically. F-8.10.13.
	maxOrphanTasksPerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_max_orphan_tasks_per_tenant",
		Help: "Configured maxOrphanTasksPerTenant ceiling — drives the OrphanTasksPerTenantHigh alert threshold (§8.10).",
	}, nil)
	if err != nil {
		return m, err
	}
	orphanSessionReconciliations := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_session_reconciliations_total",
		Help: "Orphan sessions forcibly transitioned to failed by the §10.1.4 reconciler (§10.1 / §16.1).",
	})
	agentPodStateMirrorLag, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_agent_pod_state_mirror_lag_seconds",
		Help: "Seconds since the last agent_pod_state mirror update per pool; drives the §16.5 PodStateMirrorStale alert (§10.1.4).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(maxOrphanTasksPerTenant,
		orphanSessionReconciliations,
		agentPodStateMirrorLag)
	m.maxOrphanTasksPerTenant = maxOrphanTasksPerTenant.WithLabelValues()
	m.orphanSessionReconciliations = orphanSessionReconciliations
	m.agentPodStateMirrorLag = agentPodStateMirrorLag
	return m, nil
}
