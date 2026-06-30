// SPDX-License-Identifier: MIT

package eventbus

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the production Prometheus-backed implementation of both
// BusMetrics and RetranscribeMetrics. It registers the §16.1 EventBus
// metric family against the gateway's private registry so the
// RedisEventBus publish path and the §12.6 retranscribe worker emit the
// catalogued series. A nil *PromMetrics is a valid no-op for every
// method, so callers without a registry pass nil.
//
// spec: §12.6 line 709 (EventBus observability surface); §16.1 metric
// catalog entries lenny_event_bus_publish_total /
// _publish_duration_seconds / _publish_dropped_total /
// _handler_duration_seconds / _handler_error_total /
// _retranscribe_attempts_total.
type PromMetrics struct {
	publishTotal        *prometheus.CounterVec
	publishDuration     *prometheus.HistogramVec
	publishDropped      *prometheus.CounterVec
	handlerDuration     *prometheus.HistogramVec
	handlerError        *prometheus.CounterVec
	retranscribeAttempt *prometheus.CounterVec
}

// Compile-time assertions that PromMetrics satisfies both metric
// surfaces the EventBus and the retranscribe worker consume.
var (
	_ BusMetrics          = (*PromMetrics)(nil)
	_ RetranscribeMetrics = (*PromMetrics)(nil)
)

// NewPromMetrics registers the §16.1 EventBus metric family against reg
// (pass nil to use the default registerer) and returns a metrics sink
// usable as both BusMetrics and RetranscribeMetrics. A registration
// failure (duplicate name, bad label) is returned to the caller.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	publishTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_event_bus_publish_total",
		Help: "EventBus publishes per topic (§12.6 line 709).",
	}, []string{"topic"})
	if err != nil {
		return nil, err
	}
	publishDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_event_bus_publish_duration_seconds",
		Help:    "EventBus publish duration per topic in seconds (§12.6 line 709).",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"topic"})
	if err != nil {
		return nil, err
	}
	publishDropped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_event_bus_publish_dropped_total",
		Help: "EventBus publishes dropped after the durable commit, by topic and error_type (§12.6 line 683).",
	}, []string{"topic", "error_type"})
	if err != nil {
		return nil, err
	}
	handlerDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_event_bus_handler_duration_seconds",
		Help:    "EventBus caller-supplied handler duration per topic in seconds (§12.6 line 709).",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"topic"})
	if err != nil {
		return nil, err
	}
	handlerError, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_event_bus_handler_error_total",
		Help: "EventBus caller-supplied handler errors per topic (§12.6 line 709).",
	}, []string{"topic"})
	if err != nil {
		return nil, err
	}
	// spec: §12.6 line 688-689 — labeled by outcome (success | failure)
	// and, for a failure, the error_type. The §16.5
	// EventBusPublishFinalFailure alert reads outcome="failure".
	retranscribeAttempt, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_event_bus_retranscribe_attempts_total",
		Help: "EventBus retranscribe attempts by outcome and error_type (§12.6 line 688-689).",
	}, []string{"outcome", "error_type"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, publishTotal)
	metrics.MustRegister(reg, publishDuration)
	metrics.MustRegister(reg, publishDropped)
	metrics.MustRegister(reg, handlerDuration)
	metrics.MustRegister(reg, handlerError)
	metrics.MustRegister(reg, retranscribeAttempt)
	return &PromMetrics{
		publishTotal:        publishTotal,
		publishDuration:     publishDuration,
		publishDropped:      publishDropped,
		handlerDuration:     handlerDuration,
		handlerError:        handlerError,
		retranscribeAttempt: retranscribeAttempt,
	}, nil
}

// PublishTotal implements BusMetrics.
func (m *PromMetrics) PublishTotal(topic EventTopic) {
	if m != nil {
		m.publishTotal.WithLabelValues(string(topic)).Inc()
	}
}

// PublishDuration implements BusMetrics.
func (m *PromMetrics) PublishDuration(topic EventTopic, seconds float64) {
	if m != nil {
		m.publishDuration.WithLabelValues(string(topic)).Observe(seconds)
	}
}

// PublishDropped implements BusMetrics.
func (m *PromMetrics) PublishDropped(topic EventTopic, errType PublishErrorType) {
	if m != nil {
		m.publishDropped.WithLabelValues(string(topic), string(errType)).Inc()
	}
}

// HandlerDuration implements BusMetrics.
func (m *PromMetrics) HandlerDuration(topic EventTopic, seconds float64) {
	if m != nil {
		m.handlerDuration.WithLabelValues(string(topic)).Observe(seconds)
	}
}

// HandlerError implements BusMetrics.
func (m *PromMetrics) HandlerError(topic EventTopic) {
	if m != nil {
		m.handlerError.WithLabelValues(string(topic)).Inc()
	}
}

// Attempt implements RetranscribeMetrics. errType is empty on success.
func (m *PromMetrics) Attempt(outcome string, errType PublishErrorType) {
	if m != nil {
		m.retranscribeAttempt.WithLabelValues(outcome, string(errType)).Inc()
	}
}

// ReplayBufferUtilization implements BusMetrics. It is intentionally a
// no-op: the catalogued lenny_event_bus_replay_buffer_utilization gauge
// is owned by the §10.4 session-SSE replay-buffer poller (F-10.4.11),
// which samples and sets it on a periodic cadence. Registering it a
// second time here would panic on duplicate, and a second writer would
// race the §10.4 poller for one gauge. The §12.6 EventBus replay buffer
// is inert in v1 until the first-publish path (the audit-write EventBus
// emit) is wired; its utilization can be surfaced separately at that
// point.
func (m *PromMetrics) ReplayBufferUtilization(ratio float64) {}

// FinalFailure implements RetranscribeMetrics. The terminal failure is
// already counted by Attempt("failure", errType); the §16.5
// EventBusPublishFinalFailure alert keys on that failure counter, so
// FinalFailure carries no separate series.
func (m *PromMetrics) FinalFailure(topic EventTopic) {}
