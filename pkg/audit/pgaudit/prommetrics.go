// SPDX-License-Identifier: MIT

package pgaudit

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the Prometheus-backed Metrics implementation. It
// registers the §4.4 / §11.7 catalog counters (lenny_pgaudit_grant_events_total
// plus delivery-failure and parse-failure siblings) against the
// supplied registerer.
//
// The shipper's PgAuditEvent / PgAuditDeliveryFailed / PgAuditParseFailed
// calls flow through this adapter onto Prometheus.
type PromMetrics struct {
	events    *prometheus.CounterVec
	deliv     *prometheus.CounterVec
	parseFail prometheus.Counter
}

// NewPromMetrics constructs and registers the pgaudit metrics. The
// per-class label is bounded to the closed Class enum so the cardinality
// stays low.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	events, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pgaudit_grant_events_total",
		Help: "pgaudit events forwarded to the sink (§4.4 / §11.7). Labelled by class (DDL, ROLE, READ, WRITE, FUNCTION, MISC).",
	}, []string{"class"})
	if err != nil {
		return nil, err
	}
	deliv, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pgaudit_sink_delivery_failed_total",
		Help: "pgaudit sink-delivery failures, drives the §16.5 PgAuditSinkDeliveryFailed alert.",
	}, []string{"class"})
	if err != nil {
		return nil, err
	}
	parseFail, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pgaudit_parse_failed_total",
		Help: "pgaudit lines the shipper could not parse.",
	}, nil)
	if err != nil {
		return nil, err
	}
	if reg != nil {
		reg.MustRegister(events, deliv, parseFail)
	}
	return &PromMetrics{
		events:    events,
		deliv:     deliv,
		parseFail: parseFail.WithLabelValues(),
	}, nil
}

// PgAuditEvent bumps the per-class events counter.
func (m *PromMetrics) PgAuditEvent(class Class) {
	if m == nil {
		return
	}
	m.events.WithLabelValues(string(class)).Inc()
}

// PgAuditDeliveryFailed bumps the per-class delivery-failure counter.
func (m *PromMetrics) PgAuditDeliveryFailed(class Class) {
	if m == nil {
		return
	}
	m.deliv.WithLabelValues(string(class)).Inc()
}

// PgAuditParseFailed bumps the parse-failure counter.
func (m *PromMetrics) PgAuditParseFailed() {
	if m == nil {
		return
	}
	m.parseFail.Inc()
}

// Ensure PromMetrics satisfies Metrics.
var _ Metrics = (*PromMetrics)(nil)
