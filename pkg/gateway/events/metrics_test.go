// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

func newTestMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m
}

// TestEmittedTotalByType_spec_25_3_709 asserts the local emitter counts
// each event on lenny_ops_events_emitted_total keyed by the short type.
func TestEmittedTotalByType_spec_25_3_709(t *testing.T) {
	m := newTestMetrics(t)
	em := NewEmitter(NewEventBuffer(0), "r", WithMetrics(m))
	for i := 0; i < 3; i++ {
		_ = em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	_ = em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.session_failed"})

	if got := testutil.ToFloat64(m.emitted.WithLabelValues("alert_fired")); got != 3 {
		t.Errorf("emitted{alert_fired} = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.emitted.WithLabelValues("session_failed")); got != 1 {
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

// TestEmitFailedTotalOnRedisFailure_spec_25_3_703 asserts a failed Redis
// write increments lenny_ops_events_emit_failed_total while the event
// still counts as emitted (it always lands in the local buffer first).
func TestEmitFailedTotalOnRedisFailure_spec_25_3_703(t *testing.T) {
	m := newTestMetrics(t)
	em := NewStreamEmitter(StreamEmitterOptions{
		Client: metricsFailingRedis{}, Buffer: NewEventBuffer(0), Metrics: m,
	})
	if err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.alert_fired"}); err == nil {
		t.Fatal("Emit should return the Redis error")
	}
	if got := testutil.ToFloat64(m.emitFailed.WithLabelValues("alert_fired")); got != 1 {
		t.Errorf("emit_failed{alert_fired} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.emitted.WithLabelValues("alert_fired")); got != 1 {
		t.Errorf("emitted{alert_fired} = %v, want 1 (buffer write always succeeds)", got)
	}
}

// TestBufferLengthGauge_spec_25_3_770 asserts the buffer-length gauge
// tracks the retained count and caps at the ring capacity once wrapped.
func TestBufferLengthGauge_spec_25_3_770(t *testing.T) {
	m := newTestMetrics(t)
	buf := NewEventBuffer(4, WithBufferMetrics(m))
	for i := 0; i < 3; i++ {
		buf.Append(OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	if got := testutil.ToFloat64(m.bufferLen); got != 3 {
		t.Errorf("buffer_length after 3 appends = %v, want 3", got)
	}
	for i := 0; i < 5; i++ {
		buf.Append(OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	if got := testutil.ToFloat64(m.bufferLen); got != 4 {
		t.Errorf("buffer_length after wrapping = %v, want 4 (capped at capacity)", got)
	}
}

// TestBufferQueryAndGapCounters_spec_25_3_771 asserts every query counts
// and a query whose cursor was evicted additionally counts a gap.
func TestBufferQueryAndGapCounters_spec_25_3_771(t *testing.T) {
	m := newTestMetrics(t)
	buf := NewEventBuffer(4, WithBufferMetrics(m))
	for i := 0; i < 8; i++ { // overflow the 4-slot ring so id 1..4 are evicted
		buf.Append(OperationalEvent{Type: "dev.lenny.alert_fired"})
	}
	buf.Query(buf.head, EventFilter{}, 10) // head cursor: no gap
	if got := testutil.ToFloat64(m.queries); got != 1 {
		t.Errorf("queries_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.gaps); got != 0 {
		t.Errorf("gaps_total after in-range query = %v, want 0", got)
	}
	buf.Query(1, EventFilter{}, 10) // cursor 1 was evicted: gap
	if got := testutil.ToFloat64(m.queries); got != 2 {
		t.Errorf("queries_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.gaps); got != 1 {
		t.Errorf("gaps_total after evicted-cursor query = %v, want 1", got)
	}
}

// TestNilMetricsAreNoOps asserts emitters and buffers wired without
// metrics do not panic.
func TestNilMetricsAreNoOps(t *testing.T) {
	em := NewEmitter(NewEventBuffer(0), "r")
	if err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.alert_fired"}); err != nil {
		t.Fatalf("emit without metrics: %v", err)
	}
	buf := NewEventBuffer(0)
	buf.Append(OperationalEvent{Type: "dev.lenny.alert_fired"})
	buf.Query(0, EventFilter{}, 10)
}
