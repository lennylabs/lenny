// SPDX-License-Identifier: MIT

package eventbus

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §12.6 line 709 / §16.1 — the production BusMetrics +
// RetranscribeMetrics surface registers and increments the catalogued
// EventBus metric family.
func TestPromMetricsRegistersAndCounts_spec_12_6_709(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}

	m.PublishTotal(TopicSessionLifecycle)
	m.PublishTotal(TopicSessionLifecycle)
	m.PublishDuration(TopicSessionLifecycle, 0.002)
	m.PublishDropped(TopicDelegationTree, ErrBackendUnavailable)
	m.HandlerDuration(TopicSessionLifecycle, 0.003)
	m.HandlerError(TopicSessionLifecycle)
	m.Attempt("success", "")
	m.Attempt("failure", ErrTimeout)

	if got := testutil.ToFloat64(m.publishTotal.WithLabelValues(string(TopicSessionLifecycle))); got != 2 {
		t.Errorf("publish_total{session_lifecycle} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.publishDropped.WithLabelValues(string(TopicDelegationTree), string(ErrBackendUnavailable))); got != 1 {
		t.Errorf("publish_dropped_total{delegation_tree,backend_unavailable} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.handlerError.WithLabelValues(string(TopicSessionLifecycle))); got != 1 {
		t.Errorf("handler_error_total = %v, want 1", got)
	}
	// §16.5 EventBusPublishFinalFailure keys on the failure-attempt counter.
	if got := testutil.ToFloat64(m.retranscribeAttempt.WithLabelValues("failure", string(ErrTimeout))); got != 1 {
		t.Errorf("retranscribe_attempts_total{failure,timeout} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.retranscribeAttempt.WithLabelValues("success", "")); got != 1 {
		t.Errorf("retranscribe_attempts_total{success} = %v, want 1", got)
	}

	// The catalogued family names are exposed on the registry.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	want := map[string]bool{
		"lenny_event_bus_publish_total":               false,
		"lenny_event_bus_publish_duration_seconds":    false,
		"lenny_event_bus_publish_dropped_total":       false,
		"lenny_event_bus_handler_duration_seconds":    false,
		"lenny_event_bus_handler_error_total":         false,
		"lenny_event_bus_retranscribe_attempts_total": false,
	}
	for _, mf := range mfs {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("registry is missing %s", name)
		}
	}
}

// spec: §12.6 line 709 — the publish path emits the §16.1 metrics. A
// PromMetrics passed to a RedisEventBus records a real publish against
// the registry, not only the in-memory CountingBusMetrics test double.
func TestRedisEventBusEmitsPromMetrics_spec_12_6_709(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	bus := NewRedisEventBus(nil, m) // nil substrate: in-process no-op send
	if err := bus.Publish(t.Context(), "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := testutil.ToFloat64(m.publishTotal.WithLabelValues(string(TopicSessionLifecycle))); got != 1 {
		t.Errorf("publish_total = %v, want 1", got)
	}
}

// A nil *PromMetrics is a valid no-op for every method, so callers that
// do not wire a registry can pass nil without a panic.
func TestPromMetricsNilSafe(t *testing.T) {
	var m *PromMetrics
	// none of these may panic
	m.PublishTotal(TopicSessionLifecycle)
	m.PublishDuration(TopicSessionLifecycle, 0.1)
	m.PublishDropped(TopicSessionLifecycle, ErrTimeout)
	m.HandlerDuration(TopicSessionLifecycle, 0.1)
	m.HandlerError(TopicSessionLifecycle)
	m.ReplayBufferUtilization(0.5)
	m.Attempt("success", "")
	m.FinalFailure(TopicSessionLifecycle)
}
