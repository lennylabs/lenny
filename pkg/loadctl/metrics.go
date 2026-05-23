// SPDX-License-Identifier: MIT

package loadctl

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsBundle holds the Prometheus collectors the control plane
// exposes. Each Server constructs its own bundle against a private
// registry so multiple Servers in the same process (e.g. tests) do
// not collide on metric registration.
type metricsBundle struct {
	registry *prometheus.Registry

	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	runsCreated     prometheus.Counter
	runsTerminal    *prometheus.CounterVec
	runnersGauge    prometheus.Gauge
	hubSubscribers  prometheus.Gauge
	sinkErrors      prometheus.Counter
	progressEvents  prometheus.Counter
}

func newMetricsBundle() *metricsBundle {
	reg := prometheus.NewRegistry()
	m := &metricsBundle{registry: reg}

	m.requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lenny_loadctl_requests_total",
			Help: "HTTP requests processed by the control plane, labelled by route and status class.",
		},
		[]string{"route", "method", "status_class"},
	)
	m.requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "lenny_loadctl_request_duration_seconds",
			Help:    "End-to-end request duration histogram.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"route", "method"},
	)
	m.runsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_loadctl_runs_created_total",
		Help: "Runs created via POST /api/v1/runs.",
	})
	m.runsTerminal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lenny_loadctl_runs_terminal_total",
			Help: "Runs that reached a terminal state, labelled by outcome.",
		},
		[]string{"outcome"},
	)
	m.runnersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_loadctl_runners",
		Help: "Currently registered runners (healthy + stale).",
	})
	m.hubSubscribers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_loadctl_hub_subscribers",
		Help: "Active WebSocket subscribers across all run channels.",
	})
	m.sinkErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_loadctl_sink_errors_total",
		Help: "ProgressSink.Append failures (operational; not request-visible).",
	})
	m.progressEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_loadctl_progress_events_total",
		Help: "RunnerProgress events published through the hub.",
	})

	reg.MustRegister(
		m.requestsTotal,
		m.requestDuration,
		m.runsCreated,
		m.runsTerminal,
		m.runnersGauge,
		m.hubSubscribers,
		m.sinkErrors,
		m.progressEvents,
	)
	return m
}

// handler returns the http.Handler for /metrics scraping.
func (m *metricsBundle) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// instrumentMiddleware wraps an http.Handler with request counter +
// duration histogram. routeForPath collapses path-parameterised
// routes to a stable label value.
func (m *metricsBundle) instrumentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start).Seconds()
		route := routeLabel(r.URL.Path)
		m.requestsTotal.WithLabelValues(route, r.Method, statusClass(rec.code)).Inc()
		m.requestDuration.WithLabelValues(route, r.Method).Observe(elapsed)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack delegates to the underlying ResponseWriter when it supports
// the interface — required so the websocket handshake works through
// this middleware.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// routeLabel collapses dynamic path segments so cardinality stays
// bounded. Per-run-id paths become /api/v1/runs/{id}, etc.
func routeLabel(path string) string {
	switch {
	case path == "/", path == "/healthz", path == "/metrics":
		return path
	case path == "/api/v1/runs":
		return path
	case len(path) > len("/api/v1/runs/") && path[:len("/api/v1/runs/")] == "/api/v1/runs/":
		return "/api/v1/runs/{id}"
	case path == "/api/v1/runners":
		return path
	case path == "/api/v1/runners/register":
		return path
	case len(path) > len("/api/v1/runners/") && path[:len("/api/v1/runners/")] == "/api/v1/runners/":
		return "/api/v1/runners/{id}"
	case len(path) > len("/api/v1/baselines/") && path[:len("/api/v1/baselines/")] == "/api/v1/baselines/":
		return "/api/v1/baselines/{name}"
	case path == "/api/v1/scenarios", path == "/api/v1/ack", path == "/api/v1/progress":
		return path
	case len(path) > len("/runs/") && path[:len("/runs/")] == "/runs/":
		return "/runs/{id}"
	default:
		return "other"
	}
}

func statusClass(code int) string {
	if code >= 500 {
		return "5xx"
	}
	if code >= 400 {
		return "4xx"
	}
	if code >= 300 {
		return "3xx"
	}
	return strconv.Itoa(code / 100) + "xx"
}
