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
	// §4.7 line 708: the runtime did not acknowledge a task_complete
	// within the 30s window and the adapter proceeded with cleanup anyway.
	taskCompleteAckTimeout = mustCounter(prometheus.CounterOpts{
		Name: "lenny_adapter_task_complete_ack_timeout_total",
		Help: "Task-mode task_complete acknowledgements that timed out (§4.7); " +
			"the adapter proceeded with scrub without the runtime's ack.",
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

// IncTaskCompleteAckTimeout increments the §4.7 line 708 task-complete
// acknowledgement timeout counter.
func IncTaskCompleteAckTimeout() { taskCompleteAckTimeout.WithLabelValues().Inc() }

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
