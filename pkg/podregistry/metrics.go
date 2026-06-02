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

// Implementation label values for the §12.6 line 484
// lenny_pod_registry_watch_lag_seconds gauge. The label distinguishes
// the v1 CRD-backed registry from a Tier-4 Postgres-backed one so an
// operator can compare watch propagation latency across backends.
const (
	implCRD      = "crd"
	implPostgres = "postgres"
)

// Metrics emits the §12.6 line 478 PodRegistry observability contract:
// lenny_pod_registry_operation_duration_seconds{operation, pool}
// (histogram) and lenny_pod_registry_error_total{operation, pool}
// (counter), plus the §12.6 line 484 watch-lag gauge
// lenny_pod_registry_watch_lag_seconds{pool, implementation}. Every
// PodRegistry implementation MUST emit the first two; an implementation
// whose WatchPods supports change notification MUST also emit the watch
// lag. The registry records them per call when a Metrics is attached.
type Metrics struct {
	duration *prometheus.HistogramVec
	errors   *prometheus.CounterVec
	watchLag *prometheus.GaugeVec
}

// NewMetrics builds and registers the §12.6 line 478 / line 484
// collectors against reg. A nil reg uses the default registerer.
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
	lag, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pod_registry_watch_lag_seconds",
		Help: "PodRegistry watch event delivery lag by pool and implementation (§12.6 line 484).",
	}, []string{"pool", "implementation"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, dur)
	metrics.MustRegister(reg, errs)
	metrics.MustRegister(reg, lag)
	return &Metrics{duration: dur, errors: errs, watchLag: lag}, nil
}

func (m *Metrics) observe(operation, pool string, seconds float64) {
	m.duration.WithLabelValues(operation, pool).Observe(seconds)
}

func (m *Metrics) incError(operation, pool string) {
	m.errors.WithLabelValues(operation, pool).Inc()
}

// observeWatchLag records the §12.6 line 484 delay between a row's
// updated_at and the moment its event reached the watch channel, for the
// given pool and implementation. A negative sample (clock skew) is
// clamped to 0.
func (m *Metrics) observeWatchLag(pool, implementation string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	m.watchLag.WithLabelValues(pool, implementation).Set(seconds)
}
