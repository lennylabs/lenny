// SPDX-License-Identifier: MIT

// Package gatewaymetrics registers the §16.1 gateway metrics and
// exposes the Prometheus `/metrics` scrape target. It composes the
// pkg/observability/metrics constructors (which enforce the §16.1.1
// label-hygiene rules) with a private prometheus.Registry so the
// gateway's metrics are isolated from the process-global default
// registry.
package gatewaymetrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the registered §16.1 gateway metric vectors.
type Metrics struct {
	reg *prometheus.Registry

	requestsTotal     *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	activeSessions    prometheus.Gauge
	storageQuotaUsed  *prometheus.GaugeVec
	storageQuotaLimit *prometheus.GaugeVec
}

// New constructs and registers the gateway metric set against a
// fresh private registry.
func New() (*Metrics, error) {
	reg := prometheus.NewRegistry()

	requestsTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_requests_total",
		Help: "Total gateway HTTP requests, labelled by method, route, and status class.",
	}, []string{"method", "route", "status_class"})
	if err != nil {
		return nil, err
	}
	requestDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gateway_request_duration_seconds",
		Help:    "Gateway HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	if err != nil {
		return nil, err
	}
	activeSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_sessions",
		Help: "Current count of non-terminal sessions tracked by the gateway.",
	}, nil)
	if err != nil {
		return nil, err
	}
	storageQuotaUsed, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_storage_quota_bytes_used",
		Help: "Per-tenant artifact storage bytes reserved-plus-committed (§11.2).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	storageQuotaLimit, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_tenant_storage_quota_bytes",
		Help: "Per-tenant configured storage quota in bytes (§11.2 storageQuotaBytes).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}

	reg.MustRegister(requestsTotal, requestDuration, storageQuotaUsed, storageQuotaLimit)
	gauge := activeSessions.WithLabelValues()
	reg.MustRegister(activeSessions)

	return &Metrics{
		reg:               reg,
		requestsTotal:     requestsTotal,
		requestDuration:   requestDuration,
		activeSessions:    gauge,
		storageQuotaUsed:  storageQuotaUsed,
		storageQuotaLimit: storageQuotaLimit,
	}, nil
}

// SetStorageQuota updates the §16.1 per-tenant storage-quota gauges:
// the bytes currently reserved-plus-committed and the tenant's
// configured quota. These drive the §16.5 StorageQuotaHigh alert.
func (m *Metrics) SetStorageQuota(tenantID string, used, limit int64) {
	m.storageQuotaUsed.WithLabelValues(tenantID).Set(float64(used))
	m.storageQuotaLimit.WithLabelValues(tenantID).Set(float64(limit))
}

// Handler returns the Prometheus `/metrics` scrape handler over the
// gateway's private registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// SetActiveSessions updates the active-session gauge. The gateway
// calls this from the watchdog sweep or a dedicated poller.
func (m *Metrics) SetActiveSessions(n int) {
	m.activeSessions.Set(float64(n))
}

// Middleware returns an http.Handler that records the §16.1 request
// metrics for every request to inner. The route label is taken from
// the supplied routeOf function so high-cardinality path segments
// (session ids, blob refs) collapse to a stable route template.
func (m *Metrics) Middleware(inner http.Handler, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeOf(r)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		inner.ServeHTTP(rec, r)
		elapsed := time.Since(start).Seconds()

		m.requestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		m.requestDuration.WithLabelValues(r.Method, route).Observe(elapsed)
	})
}

// statusRecorder captures the response status code so the metrics
// middleware can label by status class.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// statusClass collapses an HTTP status to its §16.1.1 low-cardinality
// class label (2xx, 3xx, 4xx, 5xx) so the metric does not explode
// into one series per status code.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code/100) + "xx"
	}
}
