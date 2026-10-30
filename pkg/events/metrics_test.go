// SPDX-License-Identifier: MIT

package events

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newTestMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m
}

// TestEmittedTotalByType_spec_25_3_709 asserts IncEmitted counts each
// event on lenny_ops_events_emitted_total keyed by the short type, with
// the full CloudEvents prefix stripped to the §16.6 catalogue short name.
func TestEmittedTotalByType_spec_25_3_709(t *testing.T) {
	m := newTestMetrics(t)
	for i := 0; i < 3; i++ {
		m.IncEmitted("dev.lenny.alert_fired")
	}
	m.IncEmitted("dev.lenny.session_failed")

	if got := testutil.ToFloat64(m.emitted.WithLabelValues("alert_fired")); got != 3 {
		t.Errorf("emitted{alert_fired} = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.emitted.WithLabelValues("session_failed")); got != 1 {
		t.Errorf("emitted{session_failed} = %v, want 1", got)
	}
}

// TestEmitFailedTotalByType_spec_25_3_703 asserts IncEmitFailed counts a
// failed remote write on lenny_ops_events_emit_failed_total. The §25.5
// Redis-unreachable path through the StreamEmitter is exercised in
// pkg/gateway/eventbuffer; this asserts the metric family the emitter
// drives.
func TestEmitFailedTotalByType_spec_25_3_703(t *testing.T) {
	m := newTestMetrics(t)
	m.IncEmitted("dev.lenny.alert_fired")
	m.IncEmitFailed("dev.lenny.alert_fired")
	if got := testutil.ToFloat64(m.emitFailed.WithLabelValues("alert_fired")); got != 1 {
		t.Errorf("emit_failed{alert_fired} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.emitted.WithLabelValues("alert_fired")); got != 1 {
		t.Errorf("emitted{alert_fired} = %v, want 1 (buffer write always succeeds)", got)
	}
}

// TestBufferLengthGauge_spec_25_3_770 asserts SetBufferLength tracks the
// retained count on lenny_events_buffer_length.
func TestBufferLengthGauge_spec_25_3_770(t *testing.T) {
	m := newTestMetrics(t)
	m.SetBufferLength(3)
	if got := testutil.ToFloat64(m.bufferLen); got != 3 {
		t.Errorf("buffer_length = %v, want 3", got)
	}
	m.SetBufferLength(4)
	if got := testutil.ToFloat64(m.bufferLen); got != 4 {
		t.Errorf("buffer_length = %v, want 4", got)
	}
}

// TestBufferQueryAndGapCounters_spec_25_3_771 asserts IncQuery and IncGap
// drive lenny_events_buffer_queries_total / _gaps_total.
func TestBufferQueryAndGapCounters_spec_25_3_771(t *testing.T) {
	m := newTestMetrics(t)
	m.IncQuery()
	if got := testutil.ToFloat64(m.queries); got != 1 {
		t.Errorf("queries_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.gaps); got != 0 {
		t.Errorf("gaps_total = %v, want 0", got)
	}
	m.IncQuery()
	m.IncGap()
	if got := testutil.ToFloat64(m.queries); got != 2 {
		t.Errorf("queries_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.gaps); got != 1 {
		t.Errorf("gaps_total = %v, want 1", got)
	}
}

// TestNilMetricsAreNoOps asserts every Metrics method is nil-safe so an
// emitter or buffer wired without metrics is a no-op.
func TestNilMetricsAreNoOps(t *testing.T) {
	var m *Metrics
	m.IncEmitted("dev.lenny.alert_fired")
	m.IncEmitFailed("dev.lenny.alert_fired")
	m.SetBufferLength(3)
	m.IncQuery()
	m.IncGap()
}
