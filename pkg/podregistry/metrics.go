// SPDX-License-Identifier: MIT

package podregistry

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Operation label values for the §12.6 line 478 registry metrics. The set
// is closed; one value per PodRegistry method.
const (
	opGet         = "get"
	opUpdateState = "update_state"
	opClaim       = "claim"
	opRelease     = "release"
	opList        = "list"
	opCount       = "count"
	opCreate      = "create"
	opDelete      = "delete"
	opWatch       = "watch"
)

// Metrics emits the §12.6 line 478 PodRegistry observability contract:
// lenny_pod_registry_operation_duration_seconds{operation, pool}
// (histogram) and lenny_pod_registry_error_total{operation, pool}
// (counter). Every PodRegistry implementation MUST emit both; the
// CRDPodRegistry records them per call when a Metrics is attached.
type Metrics struct {
	duration *prometheus.HistogramVec
	errors   *prometheus.CounterVec
}

// NewMetrics builds and registers the §12.6 line 478 collectors against
// reg. A nil reg uses the default registerer.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	dur, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_pod_registry_operation_duration_seconds",
		Help: "PodRegistry storage-operation duration by operation and pool (§12.6 line 478).",
	}, []string{"operation", "pool"})
	if err != nil {
		return nil, err
	}
	errs, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pod_registry_error_total",
		Help: "PodRegistry storage-operation errors by operation and pool (§12.6 line 478).",
	}, []string{"operation", "pool"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, dur)
	metrics.MustRegister(reg, errs)
	return &Metrics{duration: dur, errors: errs}, nil
}

func (m *Metrics) observe(operation, pool string, seconds float64) {
	m.duration.WithLabelValues(operation, pool).Observe(seconds)
}

func (m *Metrics) incError(operation, pool string) {
	m.errors.WithLabelValues(operation, pool).Inc()
}
