// SPDX-License-Identifier: MIT

package metrics

import "github.com/prometheus/client_golang/prometheus"

// PromQueryMetrics is the production QueryMetrics adapter: it observes
// the §25.4 lines 1914-1916 lenny_prometheus_query_duration_seconds{kind}
// histogram. Wire it into PrometheusConfig.Metrics so each PromQL query
// records its wall-clock latency.
type PromQueryMetrics struct {
	h *prometheus.HistogramVec
}

// NewPromQueryMetrics registers the lenny_prometheus_query_duration_seconds
// histogram on reg and returns the adapter. The three closed-enum query
// kinds are pre-stamped so the series appear on /metrics at count 0 before
// the first query, matching the closed-enum exposition the §25.4 metrics
// surface uses elsewhere. A nil reg uses prometheus.DefaultRegisterer.
func NewPromQueryMetrics(reg prometheus.Registerer) (*PromQueryMetrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lenny_prometheus_query_duration_seconds",
		Help:    "§25.4 lines 1914-1916 Prometheus query latency by kind (instant, range, alerts).",
		Buckets: prometheus.DefBuckets,
	}, []string{"kind"})
	if err := reg.Register(h); err != nil {
		return nil, err
	}
	for _, k := range []string{QueryKindInstant, QueryKindRange, QueryKindAlerts} {
		h.WithLabelValues(k)
	}
	return &PromQueryMetrics{h: h}, nil
}

// ObserveQuery implements QueryMetrics.
func (m *PromQueryMetrics) ObserveQuery(kind string, seconds float64) {
	m.h.WithLabelValues(kind).Observe(seconds)
}
