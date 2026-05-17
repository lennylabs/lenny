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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics holds the registered §16.1 gateway metric vectors.
type Metrics struct {
	reg *prometheus.Registry

	requestsTotal       *prometheus.CounterVec
	requestDuration     *prometheus.HistogramVec
	activeSessions      prometheus.Gauge
	storageQuotaUsed    *prometheus.GaugeVec
	storageQuotaLimit   *prometheus.GaugeVec
	circuitBreakerOpen  *prometheus.GaugeVec
	cbCacheStale        prometheus.Gauge
	cbCacheInitialized  prometheus.Gauge
	elicitationDropped  *prometheus.CounterVec
	experimentIsoRej    *prometheus.CounterVec
	noEnvPolicyAllowAll *prometheus.CounterVec
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
	circuitBreakerOpen, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_open",
		Help: "1 when the named §11.6 circuit breaker is open, 0 when closed.",
	}, []string{"circuit_name"})
	if err != nil {
		return nil, err
	}
	cbCacheStale, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_stale_seconds",
		Help: "Wall seconds since the circuit-breaker cache last refreshed from Redis.",
	}, nil)
	if err != nil {
		return nil, err
	}
	cbCacheInitialized, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_initialized",
		Help: "1 once the circuit-breaker cache has completed its first refresh.",
	}, nil)
	if err != nil {
		return nil, err
	}
	elicitationDropped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_dropped_total",
		Help: "Total elicitations the gateway dropped, labelled by drop reason (§9.1).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	experimentIsoRej, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_isolation_rejections_total",
		Help: "Total sessions the §10.7 ExperimentRouter rejected closed because the variant pool's isolation profile was weaker than the session's.",
	}, []string{"tenant_id", "experiment_id", "variant_id"})
	if err != nil {
		return nil, err
	}
	noEnvPolicyAllowAll, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_noenvironmentpolicy_allowall_total",
		Help: "Total tenant rbac-config writes that set noEnvironmentPolicy to allow-all (§10.6).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}

	reg.MustRegister(requestsTotal, requestDuration, storageQuotaUsed,
		storageQuotaLimit, circuitBreakerOpen, elicitationDropped, experimentIsoRej,
		noEnvPolicyAllowAll)
	gauge := activeSessions.WithLabelValues()
	cbStale := cbCacheStale.WithLabelValues()
	cbInit := cbCacheInitialized.WithLabelValues()
	reg.MustRegister(activeSessions, cbCacheStale, cbCacheInitialized)

	return &Metrics{
		reg:                 reg,
		requestsTotal:       requestsTotal,
		requestDuration:     requestDuration,
		activeSessions:      gauge,
		storageQuotaUsed:    storageQuotaUsed,
		storageQuotaLimit:   storageQuotaLimit,
		circuitBreakerOpen:  circuitBreakerOpen,
		cbCacheStale:        cbStale,
		cbCacheInitialized:  cbInit,
		elicitationDropped:  elicitationDropped,
		experimentIsoRej:    experimentIsoRej,
		noEnvPolicyAllowAll: noEnvPolicyAllowAll,
	}, nil
}

// RecordElicitationDrop increments the §9.1 lenny_elicitation_dropped_total
// counter for the given drop reason (for example `budget_exceeded`).
func (m *Metrics) RecordElicitationDrop(reason string) {
	m.elicitationDropped.WithLabelValues(reason).Inc()
}

// RecordExperimentIsolationRejection increments the §16.1
// lenny_experiment_isolation_rejections_total counter when the §10.7
// ExperimentRouter fails a session closed because the variant pool's
// isolation profile is weaker than the session's.
func (m *Metrics) RecordExperimentIsolationRejection(tenantID, experimentID, variantID string) {
	m.experimentIsoRej.WithLabelValues(tenantID, experimentID, variantID).Inc()
}

// RecordNoEnvironmentPolicyAllowAll increments the §10.6
// lenny_noenvironmentpolicy_allowall_total counter when a tenant's
// rbac-config is written with noEnvironmentPolicy set to allow-all.
func (m *Metrics) RecordNoEnvironmentPolicyAllowAll(tenantID string) {
	m.noEnvPolicyAllowAll.WithLabelValues(tenantID).Inc()
}

// SetCircuitBreakerOpen updates the §16.1 per-breaker open gauge: 1
// when the named breaker is open, 0 when closed.
func (m *Metrics) SetCircuitBreakerOpen(name string, open bool) {
	v := 0.0
	if open {
		v = 1
	}
	m.circuitBreakerOpen.WithLabelValues(name).Set(v)
}

// SetCircuitBreakerCache updates the §16.1 circuit-breaker cache
// gauges: the seconds since the last refresh and whether the cache has
// completed its first refresh.
func (m *Metrics) SetCircuitBreakerCache(staleSeconds float64, initialized bool) {
	m.cbCacheStale.Set(staleSeconds)
	if initialized {
		m.cbCacheInitialized.Set(1)
	} else {
		m.cbCacheInitialized.Set(0)
	}
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
