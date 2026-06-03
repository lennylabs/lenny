// SPDX-License-Identifier: MIT

package health

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics receives the §25.3 health-probe observations. The Aggregator
// records the per-component probe latency and the derived status code.
// All methods are nil-safe so an Aggregator wired without metrics is a
// no-op. spec: §25.3 lines 538-542.
type Metrics interface {
	// ObserveCheckDuration records a component's probe latency in seconds
	// on lenny_health_check_duration_seconds{component}.
	ObserveCheckDuration(component string, seconds float64)
	// SetStatus records a component's verdict on lenny_health_status
	// {component} (0=healthy, 1=degraded, 2=unhealthy).
	SetStatus(component string, status Status)
}

// PromMetrics is the prometheus-backed Metrics. spec: §25.3 lines 538-542.
type PromMetrics struct {
	duration *prometheus.HistogramVec
	status   *prometheus.GaugeVec
}

var _ Metrics = (*PromMetrics)(nil)

// NewMetrics registers the §25.3 health metrics against reg and returns
// the emitter. spec: §25.3 lines 540-542.
func NewMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	duration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_health_check_duration_seconds",
		Help: "Health probe latency per component (§25.3).",
		// The §25.3 per-probe timeout is 2s; bucket up to and past it so
		// a probe that approaches or exceeds the deadline is visible.
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"component"})
	if err != nil {
		return nil, err
	}
	status, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_health_status",
		Help: "Per-component health verdict: 0=healthy, 1=degraded, 2=unhealthy (§25.3).",
	}, []string{"component"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, duration)
	metrics.MustRegister(reg, status)
	return &PromMetrics{duration: duration, status: status}, nil
}

// ObserveCheckDuration implements Metrics.
func (m *PromMetrics) ObserveCheckDuration(component string, seconds float64) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(component).Observe(seconds)
}

// SetStatus implements Metrics.
func (m *PromMetrics) SetStatus(component string, status Status) {
	if m == nil {
		return
	}
	m.status.WithLabelValues(component).Set(statusValue(status))
}

// statusValue maps the §25.3 status enum to the lenny_health_status
// gauge encoding: 0=healthy, 1=degraded, 2=unhealthy. spec: §25.3
// line 542.
func statusValue(s Status) float64 {
	switch s {
	case StatusDegraded:
		return 1
	case StatusUnhealthy:
		return 2
	default:
		return 0
	}
}
