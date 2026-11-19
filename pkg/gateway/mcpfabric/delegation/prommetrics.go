// SPDX-License-Identifier: MIT

package delegation

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the delegation service's own metric set. The composition
// root registers it on the gateway's metric registry, so the platform tools
// seam holds no value of the replica's gateway metrics, which a seam may
// not import. It implements MetricsRecorder. The metric names, help text,
// labels, and buckets are the ones the gateway metrics registered before
// the delegation service owned them.
type PromMetrics struct {
	depth            *prometheus.HistogramVec
	wouldHaveBlocked *prometheus.CounterVec
}

var _ MetricsRecorder = (*PromMetrics)(nil)

// NewPromMetrics builds the delegation service's metric set and registers
// it on reg. It fails when a collector cannot be built or registered, such
// as when reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: the per-session delegation depth at admission, by pool. The
	// buckets cover the maxDelegationDepth ceiling with head-room.
	depth, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_depth",
		Help:    "Per-session delegation depth observed at delegation admission (§8.2).",
		Buckets: []float64{0, 1, 2, 3, 5, 8, 13, 21},
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §16.1: self-recursion hops where a layer of the three-layer gate
	// evaluated false, one row per failing layer.
	wouldHaveBlocked, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_would_have_blocked_total",
		Help: "Self-recursion would-have-blocked counter by layer and cycle-detection mode (§8.2).",
	}, []string{"pool", "tenant_id", "layer", "mode"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{depth, wouldHaveBlocked} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register delegation metric: %w", err)
		}
	}
	return &PromMetrics{depth: depth, wouldHaveBlocked: wouldHaveBlocked}, nil
}

// ObserveDelegationDepth records the depth of an admitted delegation.
func (m *PromMetrics) ObserveDelegationDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.depth.WithLabelValues(pool).Observe(float64(depth))
}

// IncDelegationWouldHaveBlocked counts one failing layer of the
// self-recursion gate under a cycle-detection mode.
func (m *PromMetrics) IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string) {
	if m == nil {
		return
	}
	m.wouldHaveBlocked.WithLabelValues(pool, tenantID, layer, mode).Inc()
}
