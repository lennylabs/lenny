// SPDX-License-Identifier: MIT

package events

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics holds the §25.3 event-emission and event-buffer metric
// families. The Emitter and StreamEmitter increment the emission
// counters; the EventBuffer maintains the buffer gauge and query
// counters. All methods are nil-safe so an emitter or buffer wired
// without metrics is a no-op.
// spec: §25.3 lines 705-710 (emission) / lines 766-772 (buffer);
// §16.8 line 702.
type Metrics struct {
	emitted    *prometheus.CounterVec
	emitFailed *prometheus.CounterVec
	bufferLen  prometheus.Gauge
	queries    prometheus.Counter
	gaps       prometheus.Counter
}

// NewMetrics registers the §25.3 event metrics against reg and returns
// the emitter. spec: §25.3 lines 705-710, 766-772.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	emitted, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_events_emitted_total",
		Help: "Operational events emitted by type (§25.3).",
	}, []string{"type"})
	if err != nil {
		return nil, err
	}
	emitFailed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_events_emit_failed_total",
		Help: "Operational events that failed to emit because Redis was unreachable, by type (§25.3).",
	}, []string{"type"})
	if err != nil {
		return nil, err
	}
	bufferLen, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_events_buffer_length",
		Help: "Current number of events retained in the gateway event buffer (§25.3).",
	}, nil)
	if err != nil {
		return nil, err
	}
	queries, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_events_buffer_queries_total",
		Help: "Gateway event-buffer endpoint queries (§25.3).",
	}, nil)
	if err != nil {
		return nil, err
	}
	gaps, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_events_buffer_gaps_total",
		Help: "Event-buffer queries whose requested cursor had been evicted (§25.3).",
	}, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{emitted, emitFailed, bufferLen, queries, gaps} {
		metrics.MustRegister(reg, c)
	}
	return &Metrics{
		emitted:    emitted,
		emitFailed: emitFailed,
		// Materialize the unlabelled children so /metrics emits the three
		// buffer series before the first event, mirroring the unlabelled
		// gauges in gatewaymetrics.New.
		bufferLen: bufferLen.WithLabelValues(),
		queries:   queries.WithLabelValues(),
		gaps:      gaps.WithLabelValues(),
	}, nil
}

// IncEmitted increments lenny_ops_events_emitted_total for the event's
// type. spec: §25.3 line 709.
func (m *Metrics) IncEmitted(eventType string) {
	if m == nil {
		return
	}
	m.emitted.WithLabelValues(shortType(eventType)).Inc()
}

// IncEmitFailed increments lenny_ops_events_emit_failed_total for the
// event's type. spec: §25.3 lines 703, 710.
func (m *Metrics) IncEmitFailed(eventType string) {
	if m == nil {
		return
	}
	m.emitFailed.WithLabelValues(shortType(eventType)).Inc()
}

// SetBufferLength sets lenny_events_buffer_length to n. spec: §25.3
// line 770.
func (m *Metrics) SetBufferLength(n int) {
	if m == nil {
		return
	}
	m.bufferLen.Set(float64(n))
}

// IncQuery increments lenny_events_buffer_queries_total. spec: §25.3
// line 771.
func (m *Metrics) IncQuery() {
	if m == nil {
		return
	}
	m.queries.Inc()
}

// IncGap increments lenny_events_buffer_gaps_total. spec: §25.3
// line 772.
func (m *Metrics) IncGap() {
	if m == nil {
		return
	}
	m.gaps.Inc()
}

// shortType strips the §16.6 CloudEvents prefix from a full event type
// so the metric label carries the catalogue short name (e.g.
// "alert_fired" from "dev.lenny.alert_fired"). A type without the prefix
// is used verbatim; an empty type is labelled "unknown" so the series
// stays bounded.
func shortType(fullType string) string {
	if fullType == "" {
		return "unknown"
	}
	return strings.TrimPrefix(fullType, cloudEventsPrefix)
}
