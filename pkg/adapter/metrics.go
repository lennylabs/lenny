// SPDX-License-Identifier: MIT

package adapter

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// §4.7 Adapter-Agent Security Boundary counters
// (spec/04_system-components.md lines 870-888). Both are registered with
// the default Prometheus registry at package init so they appear on the
// adapter's metrics endpoint once a scrape target is wired.
var (
	soPeercredSelftestFailed = mustCounter(prometheus.CounterOpts{
		Name: "lenny_adapter_sopeercred_selftest_failed_total",
		Help: "SO_PEERCRED startup self-test failures (§4.7). A non-zero value " +
			"means the adapter exited before signalling READY.",
	})
	soPeercredDisabled = mustCounter(prometheus.CounterOpts{
		Name: "lenny_adapter_sopeercred_disabled_total",
		Help: "Pod starts in nonce-only mode with --require-so-peercred=false " +
			"(§4.7). Deployers MUST alert on a non-zero value.",
	})
	// §4.7 line 820: per-provider count of outbound LLM requests the
	// runtime reported via llm_request_started without a matching
	// llm_request_completed. The Full-level rotation gate clears when it
	// reaches zero.
	llmInflightRequests = mustGauge(prometheus.GaugeOpts{
		Name: "lenny_llm_inflight_requests",
		Help: "Outbound LLM requests in flight per provider (§4.7 direct-mode " +
			"in-flight gate), tracked from llm_request_started / completed.",
	}, []string{"provider"})
	// §4.7 lines 820-829: Full-level credential-rotation in-flight gate
	// observability. The wait histogram records how long the gate blocked
	// for in-flight LLM requests to drain; the ceiling counter records
	// fault/revocation rotations that hit the 300s cap; the timeout
	// counter records credentials_acknowledged timeouts that fell through
	// to the standard rotation path; the grace histogram records the
	// interval between credentials_rotated and the old credential's
	// release.
	credRotationInflightWait = mustHistogram(prometheus.HistogramOpts{
		Name: "lenny_credential_rotation_inflight_wait_seconds",
		Help: "Time the §4.7 Full-level rotation gate waited for in-flight " +
			"LLM requests to drain, labelled by pool and provider.",
		Buckets: []float64{0.01, 0.1, 1, 5, 15, 30, 60, 120, 300},
	}, []string{"pool", "provider"})
	credRotationCeilingHit = mustCounterVec(prometheus.CounterOpts{
		Name: "lenny_credential_rotation_inflight_ceiling_hit_total",
		Help: "Fault/revocation rotations that hit the §4.7 300s in-flight " +
			"ceiling and sent credentials_rotated regardless, labelled by " +
			"pool and trigger.",
	}, []string{"pool", "trigger"})
	credRotationTimeout = mustCounterVec(prometheus.CounterOpts{
		Name: "lenny_credential_rotation_timeout_total",
		Help: "credentials_acknowledged timeouts (§4.7 60s) that fell through " +
			"to the standard rotation path, labelled by pool, provider, runtime.",
	}, []string{"pool", "provider", "runtime"})
	credRotationGracePeriod = mustHistogram(prometheus.HistogramOpts{
		Name: "lenny_credential_rotation_grace_period_seconds",
		Help: "Interval between credentials_rotated and old-credential " +
			"release (§4.7), labelled by pool and provider.",
		Buckets: []float64{0.01, 0.1, 1, 5, 15, 30, 60},
	}, []string{"pool", "provider"})
	// §4.7 lines 652-662: adapter→gateway control events surfaced over the
	// gRPC LifecycleChannel stream (RATE_LIMITED, AUTH_EXPIRED,
	// PROVIDER_UNAVAILABLE, LEASE_REJECTED, AdapterTerminating,
	// FINAL_USAGE_REPORT). Sent counts the events delivered onto the stream.
	controlEventsSent = mustCounterVec(prometheus.CounterOpts{
		Name: "lenny_adapter_control_events_total",
		Help: "Adapter→gateway control events queued onto the §4.7 " +
			"LifecycleChannel stream, labelled by event type.",
	}, []string{"event"})
	// controlEventsDropped counts events emitted while no gateway stream is
	// attached or the per-stream buffer is full. A non-zero value means a
	// §4.9 fallback trigger or final usage report was lost.
	controlEventsDropped = mustCounterVec(prometheus.CounterOpts{
		Name: "lenny_adapter_control_events_dropped_total",
		Help: "Adapter→gateway control events that could not be delivered " +
			"(§4.7), labelled by event type and drop reason.",
	}, []string{"event", "reason"})
	// §10.1 line 52: 1 while the adapter is in the coordinator-loss hold
	// state awaiting a new coordinator's CoordinatorFence, 0 otherwise.
	coordinatorHold = mustGauge(prometheus.GaugeOpts{
		Name: "lenny_adapter_coordinator_hold",
		Help: "1 while the adapter is in the §10.1 coordinator-loss hold " +
			"state awaiting a new coordinator's CoordinatorFence; 0 otherwise.",
	}, nil)
)

// mustCounter builds and registers a label-free counter, panicking on a
// §16.1.1 naming violation. The empty label set keeps the metric a single
// time series; callers use WithLabelValues() with no arguments.
func mustCounter(opts prometheus.CounterOpts) *prometheus.CounterVec {
	c, err := metrics.NewCounter(opts, nil)
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}

// mustCounterVec builds and registers a labeled counter, panicking on a
// §16.1.1 naming violation.
func mustCounterVec(opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	c, err := metrics.NewCounter(opts, labels)
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}

// mustGauge builds and registers a labeled gauge, panicking on a
// §16.1.1 naming violation.
func mustGauge(opts prometheus.GaugeOpts, labels []string) *prometheus.GaugeVec {
	g, err := metrics.NewGauge(opts, labels)
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}

// mustHistogram builds and registers a labeled histogram, panicking on
// a §16.1.1 naming violation.
func mustHistogram(opts prometheus.HistogramOpts, labels []string) *prometheus.HistogramVec {
	h, err := metrics.NewHistogram(opts, labels)
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, h)
	return h
}

// observeRotationInflightWait records the §4.7 in-flight gate wait.
func observeRotationInflightWait(pool, provider string, seconds float64) {
	credRotationInflightWait.WithLabelValues(pool, provider).Observe(seconds)
}

// incRotationCeilingHit records a §4.7 rotation that hit the 300s
// in-flight ceiling.
func incRotationCeilingHit(pool, trigger string) {
	credRotationCeilingHit.WithLabelValues(pool, trigger).Inc()
}

// incRotationTimeout records a §4.7 credentials_acknowledged timeout
// that fell through to the standard rotation path.
func incRotationTimeout(pool, provider, runtime string) {
	credRotationTimeout.WithLabelValues(pool, provider, runtime).Inc()
}

// observeRotationGracePeriod records the §4.7 interval between
// credentials_rotated and old-credential release.
func observeRotationGracePeriod(pool, provider string, seconds float64) {
	credRotationGracePeriod.WithLabelValues(pool, provider).Observe(seconds)
}

// SetLLMInflight publishes the current per-provider in-flight LLM
// request count to the §4.7 gauge.
func SetLLMInflight(provider string, n int) {
	llmInflightRequests.WithLabelValues(provider).Set(float64(n))
}

// IncSoPeercredSelftestFailed increments the §4.7 self-test failure
// counter. The adapter calls it on the fatal self-test path before exit.
func IncSoPeercredSelftestFailed() { soPeercredSelftestFailed.WithLabelValues().Inc() }

// IncSoPeercredDisabled increments the §4.7 nonce-only-mode counter. The
// adapter calls it once on every pod start when --require-so-peercred is
// false.
func IncSoPeercredDisabled() { soPeercredDisabled.WithLabelValues().Inc() }

// incControlEventSent records a §4.7 control event queued onto the
// LifecycleChannel stream.
func incControlEventSent(event string) { controlEventsSent.WithLabelValues(event).Inc() }

// incControlEventDropped records a §4.7 control event that could not be
// delivered, tagging the drop reason ("no_stream" or "buffer_full").
func incControlEventDropped(event, reason string) {
	controlEventsDropped.WithLabelValues(event, reason).Inc()
}

// setCoordinatorHold publishes the §10.1 line 52 coordinator-hold gauge:
// 1 while the adapter is in hold state, 0 once a fence resolves it or the
// hold times out into self-termination.
func setCoordinatorHold(active bool) {
	v := 0.0
	if active {
		v = 1.0
	}
	coordinatorHold.WithLabelValues().Set(v)
}
