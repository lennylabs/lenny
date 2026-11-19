// SPDX-License-Identifier: MIT

package orphancleanup

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the orphan-cleanup sweep's own metric set. The
// composition root registers it on the gateway's metric registry, so the
// platform tools seam holds no value of the replica's gateway metrics,
// which a seam may not import. It implements MetricsSink. The metric
// names, help text, and labels are the ones the gateway metrics registered
// before the sweep owned them, and the unlabelled series are exposed at
// zero from registration, as they were there.
type PromMetrics struct {
	runs            prometheus.Counter
	terminated      prometheus.Counter
	active          prometheus.Gauge
	activePerTenant *prometheus.GaugeVec
}

var _ MetricsSink = (*PromMetrics)(nil)

// NewPromMetrics builds the sweep's metric set and registers it on reg. It
// fails when a collector cannot be built or registered, such as when reg
// already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: one run per sweep invocation, and the cumulative count of
	// orphans the sweeps terminated.
	runs := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_cleanup_runs_total",
		Help: "Background orphan cleanup job executions (§8.10 / §16.1).",
	})
	terminated := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_tasks_terminated",
		Help: "Orphan tasks terminated by the §8.10 cleanup job (§16.1).",
	})
	// §16.1: the fleet-wide and per-tenant active orphan counts; the
	// OrphanTasksPerTenantHigh alert reads the per-tenant gauge.
	active, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active",
		Help: "Currently active orphan tasks awaiting cleanup, summed across tenants (§8.10 / §16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	activePerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active_per_tenant",
		Help: "Per-tenant active orphan task count; drives the OrphanTasksPerTenantHigh alert (§8.10 / §16.1).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{runs, terminated, active, activePerTenant} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register orphan cleanup metric: %w", err)
		}
	}
	return &PromMetrics{
		runs:            runs,
		terminated:      terminated,
		active:          active.WithLabelValues(),
		activePerTenant: activePerTenant,
	}, nil
}

// IncOrphanCleanupRun counts one sweep invocation.
func (m *PromMetrics) IncOrphanCleanupRun() {
	if m == nil {
		return
	}
	m.runs.Inc()
}

// AddOrphanTasksTerminated adds a sweep's terminated count. A count of zero
// or less adds nothing.
func (m *PromMetrics) AddOrphanTasksTerminated(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.terminated.Add(float64(n))
}

// SetOrphanTasksActive publishes the fleet-wide active orphan count.
func (m *PromMetrics) SetOrphanTasksActive(value int) {
	if m == nil {
		return
	}
	m.active.Set(float64(value))
}

// SetOrphanTasksActivePerTenant publishes one tenant's active orphan count.
func (m *PromMetrics) SetOrphanTasksActivePerTenant(tenantID string, value int) {
	if m == nil {
		return
	}
	m.activePerTenant.WithLabelValues(tenantID).Set(float64(value))
}
