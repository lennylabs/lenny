// SPDX-License-Identifier: MIT

package eventbuffer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
)

func newTestMetrics(t *testing.T) (*events.Metrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := events.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m, reg
}

// gatherCount reads the value of metric series `name{label=value}` from
// reg, or all series of `name` when label is empty. It asserts on the
// public /metrics surface so the eventbuffer test never reaches into the
// neutral pkg/events Metrics internals.
func gatherCount(t *testing.T, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, fam := range fams {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			if label != "" && !hasLabel(m, label, value) {
				continue
			}
			total += metricValue(m)
		}
	}
	return total
}

func hasLabel(m *dto.Metric, label, value string) bool {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == label && lp.GetValue() == value {
			return true
		}
	}
	return false
}

func metricValue(m *dto.Metric) float64 {
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	return 0
}

// TestEmitterDrivesEmittedTotal_spec_25_3_709 asserts the local Emitter
// increments lenny_ops_events_emitted_total keyed by the §16.6 short
// type when it records an event.
func TestEmitterDrivesEmittedTotal_spec_25_3_709(t *testing.T) {
	m, reg := newTestMetrics(t)
	em := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "r", eventbuffer.WithMetrics(m))
	for i := 0; i < 3; i++ {
		_ = em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	_ = em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.session_failed"})

	if got := gatherCount(t, reg, "lenny_ops_events_emitted_total", "type", "alert_fired"); got != 3 {
		t.Errorf("emitted{alert_fired} = %v, want 3", got)
	}
	if got := gatherCount(t, reg, "lenny_ops_events_emitted_total", "type", "session_failed"); got != 1 {
		t.Errorf("emitted{session_failed} = %v, want 1", got)
	}
}

// metricsFailingRedis returns an error from XAdd so the StreamEmitter
// takes the §25.3 line 703 Redis-unreachable path.
type metricsFailingRedis struct{}

func (metricsFailingRedis) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx, "xadd", args.Stream)
	cmd.SetErr(errors.New("redis unavailable"))
	return cmd
}

// TestStreamEmitterDrivesEmitFailedOnRedisFailure_spec_25_3_703 asserts a
// failed Redis write increments lenny_ops_events_emit_failed_total while
// the event still counts as emitted (it always lands in the local buffer
// first).
func TestStreamEmitterDrivesEmitFailedOnRedisFailure_spec_25_3_703(t *testing.T) {
	m, reg := newTestMetrics(t)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: metricsFailingRedis{}, Buffer: eventbuffer.NewEventBuffer(0), Metrics: m,
	})
	if err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"}); err == nil {
		t.Fatal("Emit should return the Redis error")
	}
	if got := gatherCount(t, reg, "lenny_ops_events_emit_failed_total", "type", "alert_fired"); got != 1 {
		t.Errorf("emit_failed{alert_fired} = %v, want 1", got)
	}
	if got := gatherCount(t, reg, "lenny_ops_events_emitted_total", "type", "alert_fired"); got != 1 {
		t.Errorf("emitted{alert_fired} = %v, want 1 (buffer write always succeeds)", got)
	}
}

// TestBufferDrivesLengthGauge_spec_25_3_770 asserts the buffer-length
// gauge tracks the retained count and caps at the ring capacity once
// wrapped.
func TestBufferDrivesLengthGauge_spec_25_3_770(t *testing.T) {
	m, reg := newTestMetrics(t)
	buf := eventbuffer.NewEventBuffer(4, eventbuffer.WithBufferMetrics(m))
	for i := 0; i < 3; i++ {
		buf.Append(events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	if got := gatherCount(t, reg, "lenny_events_buffer_length", "", ""); got != 3 {
		t.Errorf("buffer_length after 3 appends = %v, want 3", got)
	}
	for i := 0; i < 5; i++ {
		buf.Append(events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	if got := gatherCount(t, reg, "lenny_events_buffer_length", "", ""); got != 4 {
		t.Errorf("buffer_length after wrapping = %v, want 4 (capped at capacity)", got)
	}
}

// TestBufferDrivesQueryAndGapCounters_spec_25_3_771 asserts every query
// counts and a query whose cursor was evicted additionally counts a gap.
func TestBufferDrivesQueryAndGapCounters_spec_25_3_771(t *testing.T) {
	m, reg := newTestMetrics(t)
	buf := eventbuffer.NewEventBuffer(4, eventbuffer.WithBufferMetrics(m))
	for i := 0; i < 8; i++ { // overflow the 4-slot ring so id 1..4 are evicted
		buf.Append(events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	buf.Query(8, events.EventFilter{}, 10) // head cursor: no gap
	if got := gatherCount(t, reg, "lenny_events_buffer_queries_total", "", ""); got != 1 {
		t.Errorf("queries_total = %v, want 1", got)
	}
	if got := gatherCount(t, reg, "lenny_events_buffer_gaps_total", "", ""); got != 0 {
		t.Errorf("gaps_total after in-range query = %v, want 0", got)
	}
	buf.Query(1, events.EventFilter{}, 10) // cursor 1 was evicted: gap
	if got := gatherCount(t, reg, "lenny_events_buffer_queries_total", "", ""); got != 2 {
		t.Errorf("queries_total = %v, want 2", got)
	}
	if got := gatherCount(t, reg, "lenny_events_buffer_gaps_total", "", ""); got != 1 {
		t.Errorf("gaps_total after evicted-cursor query = %v, want 1", got)
	}
}

// TestNilMetricsAreNoOps asserts emitters and buffers wired without
// metrics do not panic.
func TestNilMetricsAreNoOps(t *testing.T) {
	em := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "r")
	if err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"}); err != nil {
		t.Fatalf("emit without metrics: %v", err)
	}
	buf := eventbuffer.NewEventBuffer(0)
	buf.Append(events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	buf.Query(0, events.EventFilter{}, 10)
}
