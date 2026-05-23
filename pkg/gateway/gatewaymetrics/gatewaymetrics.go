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
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics holds the registered §16.1 gateway metric vectors.
type Metrics struct {
	reg *prometheus.Registry

	requestsTotal             *prometheus.CounterVec
	requestDuration           *prometheus.HistogramVec
	activeSessions            prometheus.Gauge
	activeStreams             prometheus.Gauge
	requestQueueDepth         prometheus.Gauge
	rejectionRate             prometheus.Gauge
	maxSessionsPerReplica     *prometheus.GaugeVec
	extractionThreshold       *prometheus.GaugeVec
	storageQuotaUsed          *prometheus.GaugeVec
	storageQuotaLimit         *prometheus.GaugeVec
	circuitBreakerOpen        *prometheus.GaugeVec
	cbCacheStale              prometheus.Gauge
	cbCacheInitialized        prometheus.Gauge
	elicitationDropped        *prometheus.CounterVec
	elicitationTamperDetected *prometheus.CounterVec
	experimentIsoRej          *prometheus.CounterVec
	noEnvPolicyAllowAll       *prometheus.CounterVec
	gcPauseP99Ms              prometheus.Gauge

	// inflight tracks the number of HTTP requests currently being
	// handled by the §16.1 Middleware-wrapped mux. It is the source of
	// the lenny_gateway_request_queue_depth gauge (the §4.1 SCL-026
	// primary HPA scale-out trigger): incremented on entry and
	// decremented on exit so the watchdog poller's SetRequestQueueDepth
	// call reflects the instantaneous concurrent-request count.
	inflight int64
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
	// §10.1 / §4.1 horizontal-scaling signals. activeStreams is the
	// secondary HPA metric (SCL-026); requestQueueDepth is the primary
	// HPA scale-out trigger (SCL-026) and rejectionRate is the second
	// leading indicator that detects saturation before CPU rises. All
	// three surface on /metrics so the §10.1 custom-metrics pipeline
	// (Prometheus Adapter or KEDA) can scrape them.
	activeStreams, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_streams",
		Help: "Open streaming connections on this gateway replica.",
	}, nil)
	if err != nil {
		return nil, err
	}
	requestQueueDepth, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_request_queue_depth",
		Help: "Requests queued on this gateway replica awaiting a handler goroutine.",
	}, nil)
	if err != nil {
		return nil, err
	}
	rejectionRate, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_rejection_rate",
		Help: "Gateway requests rejected with 429/503 per second on this replica.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §4.1 / §16.1: maxSessionsPerReplica is a startup-set gauge. It is
	// the denominator of the §16.5 GatewaySessionBudgetNearExhaustion
	// alert and the §17.8.2 burst-absorption minReplicas formula. The
	// delivery_mode label distinguishes proxy from direct deliveryMode
	// (per spec, two gauge values are always reported per replica).
	maxSessionsPerReplica, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_max_sessions_per_replica",
		Help: "Maximum concurrent sessions this replica can serve under the given delivery mode (§4.1).",
	}, []string{"delivery_mode"})
	if err != nil {
		return nil, err
	}
	// §4.1: surface the configured per-subsystem extraction
	// thresholds as a startup-set gauge so the values used for an
	// extraction decision are auditable against /metrics and the
	// Helm release history. The subsystem and metric labels match
	// the gateway.extractionThresholds.<subsystem>.<metric> Helm
	// keys.
	extractionThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_extraction_threshold",
		Help: "Configured §4.1 per-subsystem extraction threshold by metric.",
	}, []string{"subsystem", "metric"})
	if err != nil {
		return nil, err
	}
	// §4.1 shared-process GC pressure signal. Periodic collector
	// reads runtime/debug.ReadGCStats and pushes the p99 over a
	// sliding window into this gauge so the Tier3GCPressureHigh
	// alert (and the Tier 3 promotion criterion) can evaluate.
	gcPauseP99Ms, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_gc_pause_p99_ms",
		Help: "Process-level GC pause p99 (ms) over the last sliding window (§4.1).",
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
	elicitationTamperDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_content_tamper_detected_total",
		Help: "Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by tenant and enforcement_mode (off | detect-only | enforce) so the §16.5 alert can fire on enforce-mode catches only.",
	}, []string{"tenant_id", "enforcement_mode"})
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

	reg.MustRegister(requestsTotal, requestDuration, maxSessionsPerReplica,
		extractionThreshold,
		storageQuotaUsed, storageQuotaLimit, circuitBreakerOpen, elicitationDropped,
		elicitationTamperDetected, experimentIsoRej,
		noEnvPolicyAllowAll)
	gauge := activeSessions.WithLabelValues()
	streams := activeStreams.WithLabelValues()
	queueDepth := requestQueueDepth.WithLabelValues()
	rejections := rejectionRate.WithLabelValues()
	cbStale := cbCacheStale.WithLabelValues()
	cbInit := cbCacheInitialized.WithLabelValues()
	gcPause := gcPauseP99Ms.WithLabelValues()
	reg.MustRegister(activeSessions, activeStreams, requestQueueDepth,
		rejectionRate, cbCacheStale, cbCacheInitialized, gcPauseP99Ms)

	return &Metrics{
		reg:                       reg,
		requestsTotal:             requestsTotal,
		requestDuration:           requestDuration,
		activeSessions:            gauge,
		activeStreams:             streams,
		requestQueueDepth:         queueDepth,
		rejectionRate:             rejections,
		maxSessionsPerReplica:     maxSessionsPerReplica,
		extractionThreshold:       extractionThreshold,
		storageQuotaUsed:          storageQuotaUsed,
		storageQuotaLimit:         storageQuotaLimit,
		circuitBreakerOpen:        circuitBreakerOpen,
		cbCacheStale:              cbStale,
		cbCacheInitialized:        cbInit,
		elicitationDropped:        elicitationDropped,
		elicitationTamperDetected: elicitationTamperDetected,
		experimentIsoRej:          experimentIsoRej,
		noEnvPolicyAllowAll:       noEnvPolicyAllowAll,
		gcPauseP99Ms:              gcPause,
	}, nil
}

// RecordElicitationDrop increments the §9.1 lenny_elicitation_dropped_total
// counter for the given drop reason (for example `budget_exceeded`).
func (m *Metrics) RecordElicitationDrop(reason string) {
	m.elicitationDropped.WithLabelValues(reason).Inc()
}

// RecordElicitationContentTamperDetected increments the §9.2 /
// §16.5 lenny_elicitation_content_tamper_detected_total counter
// when the §9.2 chain walk catches a forwarding hop that mutated
// the elicitation payload. Labelled by tenant and enforcement_mode
// so the §16.5 ElicitationContentTamperDetected alert (which
// matches enforcement_mode="enforce") fires only when a tamper
// caused a hard drop; detect-only catches still bump the metric
// for visibility without firing the alert.
func (m *Metrics) RecordElicitationContentTamperDetected(tenantID, enforcementMode string) {
	m.elicitationTamperDetected.WithLabelValues(tenantID, enforcementMode).Inc()
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

// Registerer exposes the gateway's private metric registry so a
// gateway subsystem (for example the §27 playground) can register its
// own metric vectors against the same registry and have them surface
// on the shared `/metrics` scrape target.
func (m *Metrics) Registerer() prometheus.Registerer {
	return m.reg
}

// SetActiveSessions updates the active-session gauge. The gateway
// calls this from the watchdog sweep or a dedicated poller.
func (m *Metrics) SetActiveSessions(n int) {
	m.activeSessions.Set(float64(n))
}

// SetActiveStreams updates the §4.1 lenny_gateway_active_streams gauge,
// the secondary HPA metric (SCL-026): the count of in-flight streaming
// connections on this replica.
func (m *Metrics) SetActiveStreams(n int) {
	m.activeStreams.Set(float64(n))
}

// SetRequestQueueDepth updates the §4.1 lenny_gateway_request_queue_depth
// gauge, the primary HPA scale-out trigger (SCL-026): the number of
// requests queued on this replica awaiting a handler goroutine.
func (m *Metrics) SetRequestQueueDepth(n int) {
	m.requestQueueDepth.Set(float64(n))
}

// SetRejectionRate updates the §10.1 lenny_gateway_rejection_rate gauge,
// the second leading-indicator metric: requests rejected with 429/503
// per second on this replica.
func (m *Metrics) SetRejectionRate(perSecond float64) {
	m.rejectionRate.Set(perSecond)
}

// SetMaxSessionsPerReplica emits the §4.1 capacity ceiling on the
// lenny_gateway_max_sessions_per_replica gauge for the given
// delivery_mode ("proxy" or "direct"). The §16.5
// GatewaySessionBudgetNearExhaustion alert reads this value as the
// denominator of `lenny_gateway_active_sessions / value > 0.90`. The
// gateway calls this once at startup so the gauge is observable as
// soon as the /metrics endpoint serves the first scrape; the spec
// requires both delivery_mode values be reported per replica so a
// capacity-planning dashboard can compute the proxy/direct ratio.
func (m *Metrics) SetMaxSessionsPerReplica(deliveryMode string, value int) {
	m.maxSessionsPerReplica.WithLabelValues(deliveryMode).Set(float64(value))
}

// SetExtractionThreshold emits the §4.1 configured per-subsystem
// extraction threshold on the lenny_gateway_extraction_threshold
// gauge. The subsystem label must be one of `stream_proxy`,
// `upload_handler`, `mcp_fabric`, `llm_proxy`; the metric label
// matches the `gateway.extractionThresholds.<subsystem>.<metric>`
// Helm key in snake_case form (e.g., `queue_depth`,
// `active_concurrent`). The gateway calls this once per configured
// threshold at startup so the values used for an extraction decision
// are auditable against /metrics.
func (m *Metrics) SetExtractionThreshold(subsystem, metric string, value float64) {
	m.extractionThreshold.WithLabelValues(subsystem, metric).Set(value)
}

// SetGCPauseP99Ms updates the §4.1 process-level GC pause p99 gauge
// (milliseconds). The periodic collector in cmd/lenny-gateway/main.go
// reads runtime/debug.ReadGCStats, computes the p99 over a sliding
// window, and pushes the value here so the §16.5 Tier3GCPressureHigh
// alert can evaluate.
func (m *Metrics) SetGCPauseP99Ms(value float64) {
	m.gcPauseP99Ms.Set(value)
}

// Middleware returns an http.Handler that records the §16.1 request
// metrics for every request to inner. The route label is taken from
// the supplied routeOf function so high-cardinality path segments
// (session ids, blob refs) collapse to a stable route template.
//
// The middleware also tracks the in-flight request count exposed via
// InflightRequests so the §16.1 lenny_gateway_request_queue_depth
// gauge — the §4.1 SCL-026 primary HPA scale-out trigger — reflects
// the instantaneous concurrent-request count on this replica.
func (m *Metrics) Middleware(inner http.Handler, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeOf(r)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		atomic.AddInt64(&m.inflight, 1)
		defer atomic.AddInt64(&m.inflight, -1)
		inner.ServeHTTP(rec, r)
		elapsed := time.Since(start).Seconds()

		m.requestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		m.requestDuration.WithLabelValues(r.Method, route).Observe(elapsed)
	})
}

// InflightRequests returns the number of HTTP requests currently
// being handled by the metrics Middleware. The watchdog poller in
// cmd/lenny-gateway/main.go reads this value and pushes it through
// SetRequestQueueDepth to surface it on the
// lenny_gateway_request_queue_depth gauge (§4.1 SCL-026).
func (m *Metrics) InflightRequests() int {
	return int(atomic.LoadInt64(&m.inflight))
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

// Flush forwards to the underlying http.Flusher when the embedded
// ResponseWriter supports streaming. The §15.1 SSE event stream at
// GET /v1/sessions/{id}/events and the §4.9 LLM-proxy streaming
// translators rely on http.Flusher to push partial bytes to the
// client; without this forwarder the metrics middleware hides the
// Flusher and the SSE handler returns 500
// ("response writer does not support streaming"). The §16.1
// request-metrics accounting is unaffected because Write already
// passes through.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if !s.wroteHeader {
			s.wroteHeader = true
		}
		f.Flush()
	}
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
