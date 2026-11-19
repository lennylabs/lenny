// SPDX-License-Identifier: MIT

package deadlock

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the deadlock detector's own metric set. The composition
// root registers it on the gateway's metric registry, so the platform
// tools seam holds no value of the replica's gateway metrics, which a seam
// may not import. It implements MetricsSink. The metric names, help text,
// labels, and buckets are the ones the gateway metrics registered before
// the detector owned them.
type PromMetrics struct {
	detected   *prometheus.CounterVec
	resolution *prometheus.CounterVec
	duration   *prometheus.HistogramVec
}

var _ MetricsSink = (*PromMetrics)(nil)

// NewPromMetrics builds the detector's metric set and registers it on reg.
// It fails when a collector cannot be built or registered, such as when
// reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: one detection per newly deadlocked subtree root, by the
	// root's pool.
	detected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_deadlock_detected_total",
		Help: "Subtree deadlock detections (§8.8 heuristic), by the deadlocked root's pool.",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §16.1: deadlock resolutions by pool and resolution.
	resolution, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_deadlock_resolution_total",
		Help: "Subtree deadlock resolutions by pool and resolution (client_input | child_cancel | timeout).",
	}, []string{"pool", "resolution"})
	if err != nil {
		return nil, err
	}
	// §16.1: detection-to-resolution latency, by pool alone.
	duration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_deadlock_duration_seconds",
		Help:    "Time from §8.8 deadlock detection to resolution, by the deadlocked root's pool.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300},
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{detected, resolution, duration} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register deadlock metric: %w", err)
		}
	}
	return &PromMetrics{detected: detected, resolution: resolution, duration: duration}, nil
}

// IncDelegationDeadlockDetected counts a newly detected deadlocked subtree
// root.
func (m *PromMetrics) IncDelegationDeadlockDetected(pool string) {
	if m == nil {
		return
	}
	m.detected.WithLabelValues(pool).Inc()
}

// ObserveDelegationDeadlockResolution counts a deadlock's resolution and
// records its detection-to-resolution latency.
func (m *PromMetrics) ObserveDelegationDeadlockResolution(pool, resolution string, seconds float64) {
	if m == nil {
		return
	}
	m.resolution.WithLabelValues(pool, resolution).Inc()
	m.duration.WithLabelValues(pool).Observe(seconds)
}
