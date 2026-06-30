// SPDX-License-Identifier: MIT

package recommendations

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics receives the §25.3 capacity-recommendation observations. The
// CapacityService increments lenny_recommendations_generated_total for
// every recommendation it emits. All methods are nil-safe so a service
// wired without metrics is a no-op. spec: §25.3 lines 614-618.
type Metrics struct {
	generated *prometheus.CounterVec
}

// NewMetrics registers lenny_recommendations_generated_total against reg
// and returns the emitter. spec: §25.3 line 618.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	generated, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_recommendations_generated_total",
		Help: "Capacity recommendations generated, by category and priority (§25.3).",
	}, []string{"category", "priority"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, generated)
	return &Metrics{generated: generated}, nil
}

// IncGenerated increments lenny_recommendations_generated_total for the
// (category, priority) pair. spec: §25.3 line 618.
func (m *Metrics) IncGenerated(category, priority string) {
	if m == nil {
		return
	}
	m.generated.WithLabelValues(category, priority).Inc()
}

// RegisterRingBufferBytes registers the §25.3 per-replica
// lenny_recommendations_ring_buffer_bytes gauge as a GaugeFunc that
// reports the WindowStore's current approximate memory use on each
// scrape. The §25.13 alert fires when this exceeds 100 MB. spec: §25.3
// line 598.
func RegisterRingBufferBytes(reg prometheus.Registerer, store *WindowStore) error {
	const name = "lenny_recommendations_ring_buffer_bytes"
	if err := metrics.Validate(name, nil); err != nil {
		return err
	}
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: name,
		Help: "Approximate memory used by the per-replica recommendation ring buffers (§25.3).",
	}, func() float64 {
		return float64(store.ApproxBytes())
	})
	metrics.MustRegister(reg, g)
	return nil
}
