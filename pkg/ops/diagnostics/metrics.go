// SPDX-License-Identifier: MIT

package diagnostics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// requestDuration is the §25.6 line 2926 per-endpoint diagnostic
// latency histogram. The label values are the four §25.6 endpoint
// short names: "session", "pool", "credential-pool", "connectivity".
// Registered on the default registry at package init so the §16.9
// lenny-ops /metrics endpoint exposes it (F-16.8.1).
var requestDuration *prometheus.HistogramVec

func init() {
	h, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_diagnostics_request_duration_seconds",
		Help: "Per-diagnostic-endpoint latency for the §25.6 diagnostic " +
			"endpoints, labelled by endpoint short name " +
			"(session, pool, credential-pool, connectivity).",
		Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5, 10},
	}, []string{"endpoint"})
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, h)
	requestDuration = h
}

// ObserveRequestDuration records the elapsed time for a §25.6
// diagnostic endpoint call. endpoint is the short name
// ("session", "pool", "credential-pool", "connectivity").
// spec: §25.6 line 2926.
func ObserveRequestDuration(endpoint string, elapsed time.Duration) {
	requestDuration.WithLabelValues(endpoint).Observe(elapsed.Seconds())
}
