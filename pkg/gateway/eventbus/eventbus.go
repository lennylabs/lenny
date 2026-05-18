// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
)

// PublishState is the §12.3.7 audit_log.eventbus_publish_state enum. It
// records, per audit-bearing row, whether the gateway's EventBus
// publish reached Redis.
type PublishState string

const (
	// PublishPending: the row has not been published yet.
	PublishPending PublishState = "pending"

	// PublishRetryPending: a publish attempt failed and the
	// retranscribe worker will re-attempt it.
	PublishRetryPending PublishState = "retry_pending"

	// PublishPublished: the CloudEvents envelope reached Redis.
	PublishPublished PublishState = "published"

	// PublishFailed: a publish failed after the durable commit and is
	// awaiting the retranscribe worker, or has exhausted its retries.
	PublishFailed PublishState = "failed"
)

// AllPublishStates returns the closed §12.3.7 enum.
func AllPublishStates() []PublishState {
	return []PublishState{PublishPending, PublishRetryPending, PublishPublished, PublishFailed}
}

// IsValid reports whether s is one of the §12.3.7 publish states.
func (s PublishState) IsValid() bool {
	for _, v := range AllPublishStates() {
		if s == v {
			return true
		}
	}
	return false
}

// PublishErrorType is the §12.3.7 error_type label on the
// lenny_event_bus_publish_dropped_total / retranscribe metrics.
type PublishErrorType string

const (
	// ErrBackendUnavailable: Redis was unreachable.
	ErrBackendUnavailable PublishErrorType = "backend_unavailable"

	// ErrSerializationFailed: the envelope could not be JSON-encoded.
	ErrSerializationFailed PublishErrorType = "serialization_failed"

	// ErrTimeout: the publish exceeded its deadline.
	ErrTimeout PublishErrorType = "timeout"
)

// Subscription is the handle a Subscribe caller holds. Closing it
// detaches the subscriber.
type Subscription struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Close detaches the subscription and waits for its consume loop to
// exit. Safe to call multiple times.
func (s *Subscription) Close() {
	if s == nil {
		return
	}
	s.cancel()
	<-s.done
}

// BusMetrics is the §11.7 / §12.3.7 EventBus observability surface.
// Every implementation emits publish, publish-duration, handler, and
// drop counters. A nil BusMetrics is a valid no-op.
type BusMetrics interface {
	// PublishTotal counts a publish attempt for the topic.
	PublishTotal(topic EventTopic)

	// PublishDropped counts a publish that failed after the durable
	// commit, labeled by topic and error_type (§12.3.7).
	PublishDropped(topic EventTopic, errType PublishErrorType)

	// HandlerError counts a subscriber handler returning an error.
	HandlerError(topic EventTopic)
}

// PublishStateStore is the §12.3.7 surface the bus uses to record an
// audit-bearing event's publish state. pkg/gateway/auditstore
// implements it; the in-memory fake in tests implements it too. A nil
// store means the bus does not track publish state (used for
// non-audit-bearing topics or in tests that do not exercise the state
// machine).
type PublishStateStore interface {
	// SetPublishState transitions a row's eventbus_publish_state and
	// sets retry_count. It updates only those two columns; neither is
	// in the §11.7 hash input, so the chain is never re-hashed.
	SetPublishState(ctx context.Context, tenantID string, seq uint64,
		state PublishState, retryCount int) error
}

// RedisEventBus is the §12.3.7 v1 EventBus. It publishes CloudEvents
// v1.0.2 envelopes to tenant-prefixed Redis pub/sub channels over the
// Wave-1 pubsub.Bus substrate. Delivery is at-most-once.
type RedisEventBus struct {
	bus     *pubsub.Bus
	metrics BusMetrics
}

// NewRedisEventBus returns a RedisEventBus over the pubsub substrate. A
// nil pubsub.Bus is the in-process single-replica mode: Publish records
// metrics and returns nil, Subscribe blocks until cancelled.
func NewRedisEventBus(bus *pubsub.Bus, metrics BusMetrics) *RedisEventBus {
	return &RedisEventBus{bus: bus, metrics: metrics}
}

// Publish serializes event and publishes it on the §12.4
// tenant-prefixed channel for (tenantID, topic). tenantID is mandatory
// — the channel name is built by prefixing with it, so tenant
// isolation is enforced at the interface boundary, not by caller
// convention.
//
// Publish classifies a failure into a §12.3.7 error_type and records
// the drop metric. A serialization failure is not retryable; a backend
// failure is. The caller (the audit-write path) reacts to the returned
// error by marking the source row's eventbus_publish_state.
func (b *RedisEventBus) Publish(ctx context.Context, tenantID string, topic EventTopic, event Event) error {
	if tenantID == "" {
		return fmt.Errorf("eventbus: Publish requires a tenantID")
	}
	if !topic.IsValid() {
		return fmt.Errorf("eventbus: %q is not a §12.3.7 topic", topic)
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if b.metrics != nil {
		b.metrics.PublishTotal(topic)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		if b.metrics != nil {
			b.metrics.PublishDropped(topic, ErrSerializationFailed)
		}
		return &PublishError{Type: ErrSerializationFailed, Err: err}
	}
	channel := ChannelName(tenantID, topic)
	if err := b.bus.Publish(ctx, channel, payload); err != nil {
		errType := ErrBackendUnavailable
		if ctx.Err() != nil {
			errType = ErrTimeout
		}
		if b.metrics != nil {
			b.metrics.PublishDropped(topic, errType)
		}
		return &PublishError{Type: errType, Err: err}
	}
	return nil
}

// Subscribe runs a consume loop on the §12.4 tenant-prefixed channel
// for (tenantID, topic), decoding each message into a CloudEvents
// envelope and passing it to handler. A handler error is logged via
// the metric surface; the v1 RedisEventBus does not retry (handlers
// are notifications, not commands — see §12.3.7 handler design).
//
// Subscribe returns once the consume loop is attached. Close the
// returned Subscription to detach.
func (b *RedisEventBus) Subscribe(ctx context.Context, tenantID string, topic EventTopic,
	handler func(context.Context, Event) error) (*Subscription, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("eventbus: Subscribe requires a tenantID")
	}
	if !topic.IsValid() {
		return nil, fmt.Errorf("eventbus: %q is not a §12.3.7 topic", topic)
	}
	subCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	channel := ChannelName(tenantID, topic)
	go func() {
		defer close(done)
		b.bus.Subscribe(subCtx, channel, func(payload []byte) {
			var ev Event
			if err := json.Unmarshal(payload, &ev); err != nil {
				if b.metrics != nil {
					b.metrics.HandlerError(topic)
				}
				return
			}
			if err := handler(subCtx, ev); err != nil && b.metrics != nil {
				b.metrics.HandlerError(topic)
			}
		})
	}()
	return &Subscription{cancel: cancel, done: done}, nil
}

// PublishError carries the §12.3.7 error_type so the audit-write path
// can decide retry vs terminal and label the drop metric.
type PublishError struct {
	Type PublishErrorType
	Err  error
}

func (e *PublishError) Error() string {
	return fmt.Sprintf("eventbus: publish failed (%s): %v", e.Type, e.Err)
}

func (e *PublishError) Unwrap() error { return e.Err }

// CountingBusMetrics is an in-memory BusMetrics for tests and the
// §16.5 EventBusPublishDropped signal. It is goroutine-safe.
type CountingBusMetrics struct {
	mu        sync.Mutex
	published map[EventTopic]int
	dropped   map[string]int // topic|error_type
	handlrErr map[EventTopic]int
}

// NewCountingBusMetrics returns an empty CountingBusMetrics.
func NewCountingBusMetrics() *CountingBusMetrics {
	return &CountingBusMetrics{
		published: map[EventTopic]int{},
		dropped:   map[string]int{},
		handlrErr: map[EventTopic]int{},
	}
}

// PublishTotal records a publish attempt.
func (m *CountingBusMetrics) PublishTotal(topic EventTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published[topic]++
}

// PublishDropped records a dropped publish.
func (m *CountingBusMetrics) PublishDropped(topic EventTopic, errType PublishErrorType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped[string(topic)+"|"+string(errType)]++
}

// HandlerError records a handler error.
func (m *CountingBusMetrics) HandlerError(topic EventTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlrErr[topic]++
}

// Published returns the publish-attempt count for a topic.
func (m *CountingBusMetrics) Published(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.published[topic]
}

// Dropped returns the dropped-publish count for a (topic, error_type).
func (m *CountingBusMetrics) Dropped(topic EventTopic, errType PublishErrorType) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped[string(topic)+"|"+string(errType)]
}

// HandlerErrors returns the handler-error count for a topic.
func (m *CountingBusMetrics) HandlerErrors(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handlrErr[topic]
}
