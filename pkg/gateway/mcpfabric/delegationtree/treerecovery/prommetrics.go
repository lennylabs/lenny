// SPDX-License-Identifier: MIT

package treerecovery

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the Prometheus implementation of Metrics: the tree
// recovery duration histogram and the timeout counter, which the platform
// tools seam registers on the gateway's metric registry itself.
type PromMetrics struct {
	duration *prometheus.HistogramVec
	timeout  *prometheus.CounterVec
}

var _ Metrics = (*PromMetrics)(nil)

// NewPromMetrics builds the tree recovery collectors and registers them on
// reg. The names, labels, and buckets are the ones the gateway's metric set
// carried before the platform tools seam registered its own collectors.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	duration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_tree_recovery_duration_seconds",
		Help:    "Delegation tree-recovery wall-clock duration by outcome (§8.10 / §16.1).",
		Buckets: []float64{0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1200, 1800},
	}, []string{"pool", "outcome"})
	if err != nil {
		return nil, err
	}
	timeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_recovery_timeout_total",
		Help: "Delegation tree-recovery timeouts by timeout type (`level` | `tree`) (§8.10 / §16.1).",
	}, []string{"pool", "timeout_type"})
	if err != nil {
		return nil, err
	}
	if err := reg.Register(duration); err != nil {
		return nil, err
	}
	if err := reg.Register(timeout); err != nil {
		return nil, err
	}
	return &PromMetrics{duration: duration, timeout: timeout}, nil
}

// ObserveTreeRecoveryDuration records one recovery's wall-clock duration.
// pool is the root session's pool; outcome is full_success,
// partial_failure, or total_timeout.
func (m *PromMetrics) ObserveTreeRecoveryDuration(pool, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(pool, outcome).Observe(seconds)
}

// IncTreeRecoveryTimeout counts one recovery timeout. timeoutType is level
// or tree.
func (m *PromMetrics) IncTreeRecoveryTimeout(pool, timeoutType string) {
	if m == nil {
		return
	}
	m.timeout.WithLabelValues(pool, timeoutType).Inc()
}
