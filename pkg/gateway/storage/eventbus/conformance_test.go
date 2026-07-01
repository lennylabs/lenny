// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingSender is a controllable RedisEventBus.send replacement: it
// fails the next failNext sends and records the channel of every send
// that succeeds, in order, so a test can assert FIFO drain ordering and
// eviction.
type recordingSender struct {
	mu       sync.Mutex
	failNext int
	sent     []string
}

func (s *recordingSender) send(_ context.Context, channel string, _ []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext > 0 {
		s.failNext--
		return errors.New("redis down")
	}
	s.sent = append(s.sent, channel)
	return nil
}

func (s *recordingSender) setFailNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = n
}

func (s *recordingSender) channels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.sent))
	copy(out, s.sent)
	return out
}

// spec: §12.6 lines 658-662 (F-12.6.10)
// diagnosis: §12.6 defines EventBus as an interface with Publish and
// Subscribe so a Tier-4 swap to NATS/Kafka is a configuration change with
// no caller edits. RedisEventBus must satisfy the interface and be usable
// polymorphically through it.
func TestRedisEventBusSatisfiesEventBusInterface_spec_12_6_658(t *testing.T) {
	var eb EventBus = NewRedisEventBus(nil, NewCountingBusMetrics())
	ctx := context.Background()
	if err := eb.Publish(ctx, "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err != nil {
		t.Fatalf("Publish through EventBus interface: %v", err)
	}
	sub, err := eb.Subscribe(ctx, "acme", TopicSessionLifecycle,
		func(context.Context, Event) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe through EventBus interface: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("Unsubscribe: %v", err)
	}
}

// spec: §12.6 lines 411-414 (F-12.6.11)
// diagnosis: §12.6 declares Subscription as an interface whose detach
// method is Unsubscribe() error. The returned handle must satisfy it and
// Unsubscribe must be safe to call more than once (idempotent) without
// panicking or blocking on the second call.
func TestSubscriptionUnsubscribeIsIdempotent_spec_12_6_411(t *testing.T) {
	bus := NewRedisEventBus(nil, nil)
	var sub Subscription
	sub, err := bus.Subscribe(context.Background(), "acme", TopicSessionLifecycle,
		func(context.Context, Event) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("first Unsubscribe = %v, want nil", err)
	}
	done := make(chan error, 1)
	go func() { done <- sub.Unsubscribe() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("second Unsubscribe = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Unsubscribe blocked — it must be idempotent")
	}
}

// spec: §12.6 line 709 (F-12.6.17)
// diagnosis: §12.6 observability contract requires every EventBus to emit
// lenny_event_bus_publish_duration_seconds. A successful Publish must
// record exactly one duration sample equal to the elapsed publish time.
func TestPublishRecordsDurationHistogram_spec_12_6_709(t *testing.T) {
	metrics := NewCountingBusMetrics()
	bus := NewRedisEventBus(nil, metrics)
	base := time.Unix(1_700_000_000, 0)
	var calls int
	bus.now = func() time.Time {
		calls++
		if calls == 1 {
			return base
		}
		return base.Add(7 * time.Millisecond)
	}
	if err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if n := metrics.PublishDurationCount(TopicSessionLifecycle); n != 1 {
		t.Fatalf("publish_duration sample count = %d, want 1", n)
	}
	if got := metrics.PublishDurationSum(TopicSessionLifecycle); got < 0.006 || got > 0.008 {
		t.Errorf("publish_duration sum = %v s, want ~0.007", got)
	}
}

// spec: §12.6 lines 369-373, 705 (F-12.6.16)
// diagnosis: §12.6 EventBus.Publish/Subscribe take a typed TenantID and
// the channel name is tenant-prefixed, so isolation holds at the interface
// boundary. ChannelName must build t:{tenant}:evt:{topic} and a missing
// tenant must be rejected before any send.
func TestPublishTypedTenantIDChannelIsolation_spec_12_6_705(t *testing.T) {
	if got := ChannelName(TenantID("acme"), TopicSessionLifecycle); got != "t:acme:evt:session_lifecycle" {
		t.Errorf("ChannelName = %q, want t:acme:evt:session_lifecycle", got)
	}
	if ChannelName(TenantID("acme"), TopicSessionLifecycle) == ChannelName(TenantID("globex"), TopicSessionLifecycle) {
		t.Error("different tenants produced the same channel — isolation broken")
	}
	bus := NewRedisEventBus(nil, nil)
	if err := bus.Publish(context.Background(), "", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err == nil {
		t.Error("Publish accepted an empty TenantID")
	}
}

// spec: §12.6 line 683 (F-12.6.12)
// diagnosis: when Publish fails after the durable commit (backend
// unavailable), §12.6 requires the serialized envelope be appended to the
// bounded in-memory replay buffer, the drop counter incremented, and the
// utilization gauge updated. The call still returns a typed PublishError.
func TestReplayBufferBuffersOnBackendFailure_spec_12_6_683(t *testing.T) {
	metrics := NewCountingBusMetrics()
	bus := NewRedisEventBus(nil, metrics)
	sender := &recordingSender{failNext: 1}
	bus.send = sender.send

	err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x"))
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Type != ErrBackendUnavailable {
		t.Fatalf("Publish error = %v, want *PublishError{backend_unavailable}", err)
	}
	if n := bus.ReplayBufferLen(); n != 1 {
		t.Errorf("replay buffer len = %d, want 1 (the dropped envelope was buffered)", n)
	}
	if metrics.Dropped(TopicSessionLifecycle, ErrBackendUnavailable) != 1 {
		t.Errorf("publish_dropped{backend_unavailable} = %d, want 1",
			metrics.Dropped(TopicSessionLifecycle, ErrBackendUnavailable))
	}
	if metrics.ReplayUtilizationSamples() == 0 {
		t.Error("replay_buffer_utilization gauge was never updated on a drop")
	}
}

// spec: §12.6 line 683 (F-12.6.12)
// diagnosis: §12.6 says the replay buffer drains in FIFO order when the
// backend recovers (the next successful publish). Envelopes buffered during
// the outage must be re-published oldest-first after the recovering event.
func TestReplayBufferDrainsFIFOOnRecovery_spec_12_6_683(t *testing.T) {
	bus := NewRedisEventBus(nil, NewCountingBusMetrics())
	sender := &recordingSender{failNext: 3}
	bus.send = sender.send
	ctx := context.Background()
	ev := mustEvent(t, "acme", "x")

	for _, tn := range []TenantID{"a", "b", "c"} {
		if err := bus.Publish(ctx, tn, TopicSessionLifecycle, ev); err == nil {
			t.Fatalf("Publish(%s) during outage unexpectedly succeeded", tn)
		}
	}
	if n := bus.ReplayBufferLen(); n != 3 {
		t.Fatalf("buffer len after 3 drops = %d, want 3", n)
	}
	// The next publish succeeds (backend recovered) and triggers the drain.
	if err := bus.Publish(ctx, "d", TopicSessionLifecycle, ev); err != nil {
		t.Fatalf("Publish(d) after recovery: %v", err)
	}
	if n := bus.ReplayBufferLen(); n != 0 {
		t.Errorf("buffer len after drain = %d, want 0", n)
	}
	want := []string{
		ChannelName("d", TopicSessionLifecycle),
		ChannelName("a", TopicSessionLifecycle),
		ChannelName("b", TopicSessionLifecycle),
		ChannelName("c", TopicSessionLifecycle),
	}
	got := sender.channels()
	if len(got) != len(want) {
		t.Fatalf("sent %d envelopes, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("send[%d] = %q, want %q (recovering event then FIFO drain)", i, got[i], want[i])
		}
	}
}

// spec: §12.6 line 683 (F-12.6.12)
// diagnosis: the replay buffer is bounded with oldest-first eviction
// (default 10k). When it overflows, the oldest envelope is dropped and the
// utilization gauge reports 1.0; a later recovery drains only the retained
// envelopes.
func TestReplayBufferOldestFirstEviction_spec_12_6_683(t *testing.T) {
	metrics := NewCountingBusMetrics()
	bus := NewRedisEventBus(nil, metrics)
	bus.bufferCap = 2
	sender := &recordingSender{failNext: 3} // a, b, c all fail
	bus.send = sender.send
	ctx := context.Background()
	ev := mustEvent(t, "acme", "x")

	for _, tn := range []TenantID{"a", "b", "c"} {
		_ = bus.Publish(ctx, tn, TopicSessionLifecycle, ev)
	}
	if n := bus.ReplayBufferLen(); n != 2 {
		t.Fatalf("buffer len at cap = %d, want 2 (oldest evicted)", n)
	}
	if util := metrics.LastReplayUtilization(); util != 1.0 {
		t.Errorf("utilization at cap = %v, want 1.0", util)
	}
	// Recover: the retained envelopes (b, c) drain; the evicted a never sends.
	sender.setFailNext(0)
	if err := bus.Publish(ctx, "d", TopicSessionLifecycle, ev); err != nil {
		t.Fatalf("Publish(d) after recovery: %v", err)
	}
	for _, ch := range sender.channels() {
		if ch == ChannelName("a", TopicSessionLifecycle) {
			t.Error("evicted envelope a was re-published — oldest-first eviction failed")
		}
	}
	if util := metrics.LastReplayUtilization(); util != 0 {
		t.Errorf("utilization after drain = %v, want 0", util)
	}
}

// spec: §12.6 line 683 (F-12.6.12)
// diagnosis: a serialization failure is not retryable, so it must NOT be
// appended to the replay buffer (re-publishing the same bytes cannot
// succeed). It is recorded as a serialization_failed drop and returned.
func TestReplayBufferSkipsSerializationFailure_spec_12_6_683(t *testing.T) {
	metrics := NewCountingBusMetrics()
	bus := NewRedisEventBus(nil, metrics)
	ev := mustEvent(t, "acme", "x")
	ev.Data = json.RawMessage("{not valid json") // makes MarshalJSON fail

	err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle, ev)
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Type != ErrSerializationFailed {
		t.Fatalf("Publish error = %v, want *PublishError{serialization_failed}", err)
	}
	if n := bus.ReplayBufferLen(); n != 0 {
		t.Errorf("replay buffer len = %d, want 0 (serialization failures are not buffered)", n)
	}
	if metrics.Dropped(TopicSessionLifecycle, ErrSerializationFailed) != 1 {
		t.Errorf("publish_dropped{serialization_failed} = %d, want 1",
			metrics.Dropped(TopicSessionLifecycle, ErrSerializationFailed))
	}
}
