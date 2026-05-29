// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
)

// TenantID is the §12.6 typed tenant identifier. EventBus.Publish and
// Subscribe take it so a tenant id cannot be transposed with a pool,
// session, or arbitrary string at a call site; the spec frames tenant
// isolation as enforced at the interface boundary, not by caller
// convention. The §12.6 shared platform/store package is the eventual
// single home for this and the sibling ID types; until that lands the
// type is defined here so the EventBus surface carries the spec's typed
// contract.
//
// spec: §12.6 lines 369-373, 658-662, 705.
type TenantID string

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

// Subscription is the handle EventBus.Subscribe returns. Unsubscribe
// detaches the subscriber and waits for its consume loop to exit. It is
// safe to call more than once and always returns nil for the v1
// RedisEventBus; the error is in the signature so a future at-least-once
// backend (NATS, Kafka) can surface an unsubscribe failure without a
// caller-side rewrite.
//
// spec: §12.6 lines 411-414.
type Subscription interface {
	Unsubscribe() error
}

// subscription is the v1 RedisEventBus Subscription. Unsubscribe cancels
// the consume-loop context and waits for the loop to drain.
type subscription struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// Unsubscribe detaches the subscription and waits for its consume loop to
// exit. Safe to call multiple times.
func (s *subscription) Unsubscribe() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.cancel()
		<-s.done
	})
	return nil
}

// BusMetrics is the §12.6 line 709 EventBus observability surface. Every
// implementation emits the publish counter and duration histogram, the
// handler duration histogram and error counter, the §12.3.7 drop
// counter, and the §12.6 replay-buffer utilization gauge. A nil
// BusMetrics is a valid no-op.
//
// spec: §12.6 line 709 (observability contract), §12.6 line 683 (replay
// buffer utilization), §12.3.7 (drop counter).
type BusMetrics interface {
	// PublishTotal counts a publish attempt for the topic
	// (lenny_event_bus_publish_total).
	PublishTotal(topic EventTopic)

	// PublishDuration records the publish latency in seconds
	// (lenny_event_bus_publish_duration_seconds).
	PublishDuration(topic EventTopic, seconds float64)

	// PublishDropped counts a publish that failed after the durable
	// commit, labeled by topic and error_type
	// (lenny_event_bus_publish_dropped_total, §12.3.7).
	PublishDropped(topic EventTopic, errType PublishErrorType)

	// HandlerDuration records the time spent in the caller-supplied
	// handler in seconds (lenny_event_bus_handler_duration_seconds). It
	// measures the handler, not the pub/sub transport.
	HandlerDuration(topic EventTopic, seconds float64)

	// HandlerError counts a subscriber handler returning an error
	// (lenny_event_bus_handler_error_total).
	HandlerError(topic EventTopic)

	// ReplayBufferUtilization reports the in-memory replay-buffer fill
	// ratio in [0,1] (lenny_event_bus_replay_buffer_utilization); 1.0
	// means the buffer is full and oldest-first eviction is occurring.
	ReplayBufferUtilization(ratio float64)
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

// defaultReplayBufferCap is the §12.6 line 683 default replay-buffer
// depth: 10k events per replica with oldest-first eviction.
const defaultReplayBufferCap = 10_000

// EventBus is the §12.6 control-plane publish/subscribe surface. v1 ships
// exactly one implementation, RedisEventBus. The interface exists so a
// Tier-4 swap to NATS JetStream or Kafka is a store-router configuration
// change with no caller edits — callers depend on EventBus, never on the
// concrete RedisEventBus.
//
// spec: §12.6 lines 639, 658-662.
type EventBus interface {
	Publish(ctx context.Context, tenantID TenantID, topic EventTopic, event Event) error
	Subscribe(ctx context.Context, tenantID TenantID, topic EventTopic,
		handler func(context.Context, Event) error) (Subscription, error)
}

// replayEntry is one serialized envelope held in the replay buffer for
// opportunistic re-publish when the backend recovers. The payload is the
// byte-identical CloudEvents envelope, so the original id / time / source
// survive the re-publish and downstream de-duplication by CloudEvents id
// continues to work (§12.6 line 683).
type replayEntry struct {
	channel string
	payload []byte
}

// RedisEventBus is the §12.6 v1 EventBus. It publishes CloudEvents
// v1.0.2 envelopes to tenant-prefixed Redis pub/sub channels over the
// Wave-1 pubsub.Bus substrate. Delivery is at-most-once. When a publish
// fails after the durable commit the envelope is buffered in a bounded
// in-memory ring (§12.6 line 683) for opportunistic re-publish when the
// backend recovers; the retranscribe worker is the durable correctness
// layer that covers a lost buffer.
type RedisEventBus struct {
	// send performs the underlying pub/sub send. It defaults to the
	// pubsub.Bus (nil-safe; a nil substrate is the in-process
	// single-replica no-op) and is overridable in tests to exercise the
	// replay buffer against a flaky backend.
	send    func(ctx context.Context, channel string, payload []byte) error
	bus     *pubsub.Bus
	metrics BusMetrics
	now     func() time.Time

	mu        sync.Mutex
	buffer    []replayEntry
	bufferCap int
	draining  bool
}

// Compile-time assertions that RedisEventBus satisfies the §12.6 EventBus
// interface and the narrow retranscribe-worker publish surface.
var (
	_ EventBus              = (*RedisEventBus)(nil)
	_ RetranscribePublisher = (*RedisEventBus)(nil)
)

// NewRedisEventBus returns a RedisEventBus over the pubsub substrate. A
// nil pubsub.Bus is the in-process single-replica mode: Publish records
// metrics and returns nil, Subscribe blocks until cancelled.
func NewRedisEventBus(bus *pubsub.Bus, metrics BusMetrics) *RedisEventBus {
	b := &RedisEventBus{
		bus:       bus,
		metrics:   metrics,
		now:       time.Now,
		bufferCap: defaultReplayBufferCap,
	}
	// pubsub.Bus.Publish guards a nil receiver, so this closure is safe
	// in the in-process single-replica mode.
	b.send = func(ctx context.Context, channel string, payload []byte) error {
		return bus.Publish(ctx, channel, payload)
	}
	return b
}

// Publish serializes event and publishes it on the §12.4
// tenant-prefixed channel for (tenantID, topic). tenantID is mandatory —
// the channel name is built by prefixing with it, so tenant isolation is
// enforced at the interface boundary, not by caller convention.
//
// Publish classifies a failure into a §12.3.7 error_type and records the
// drop metric. A serialization failure is not retryable and is not
// buffered; a backend or timeout failure after the durable commit appends
// the serialized envelope to the bounded replay buffer (§12.6 line 683).
// On a successful send the backend is healthy, so any envelopes buffered
// during a prior outage are drained in FIFO order. The caller (the
// audit-write path) reacts to the returned error by marking the source
// row's eventbus_publish_state.
func (b *RedisEventBus) Publish(ctx context.Context, tenantID TenantID, topic EventTopic, event Event) error {
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
	start := b.now()
	sendErr := b.send(ctx, channel, payload)
	if b.metrics != nil {
		b.metrics.PublishDuration(topic, b.now().Sub(start).Seconds())
	}
	if sendErr != nil {
		errType := ErrBackendUnavailable
		if ctx.Err() != nil {
			errType = ErrTimeout
		}
		if b.metrics != nil {
			b.metrics.PublishDropped(topic, errType)
		}
		b.bufferAppend(replayEntry{channel: channel, payload: payload})
		return &PublishError{Type: errType, Err: sendErr}
	}
	b.drainReplayBuffer(ctx)
	return nil
}

// Subscribe runs a consume loop on the §12.4 tenant-prefixed channel
// for (tenantID, topic), decoding each message into a CloudEvents
// envelope and passing it to handler. The handler's wall-clock time is
// recorded in lenny_event_bus_handler_duration_seconds; a handler error
// increments lenny_event_bus_handler_error_total. The v1 RedisEventBus
// does not retry (handlers are notifications, not commands — see §12.6
// handler design).
//
// Subscribe returns once the consume loop is attached. Unsubscribe the
// returned Subscription to detach.
func (b *RedisEventBus) Subscribe(ctx context.Context, tenantID TenantID, topic EventTopic,
	handler func(context.Context, Event) error,
) (Subscription, error) {
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
		// pubsub.Bus.Subscribe blocks on ctx.Done() for a nil substrate,
		// so the in-process single-replica mode attaches and waits.
		b.bus.Subscribe(subCtx, channel, func(payload []byte) {
			var ev Event
			if err := json.Unmarshal(payload, &ev); err != nil {
				if b.metrics != nil {
					b.metrics.HandlerError(topic)
				}
				return
			}
			start := b.now()
			herr := handler(subCtx, ev)
			if b.metrics != nil {
				b.metrics.HandlerDuration(topic, b.now().Sub(start).Seconds())
			}
			if herr != nil && b.metrics != nil {
				b.metrics.HandlerError(topic)
			}
		})
	}()
	return &subscription{cancel: cancel, done: done}, nil
}

// bufferAppend appends e to the replay buffer, evicting the oldest entry
// when the buffer is at capacity (§12.6 line 683 oldest-first eviction),
// and reports the new utilization ratio.
func (b *RedisEventBus) bufferAppend(e replayEntry) {
	b.mu.Lock()
	b.buffer = append(b.buffer, e)
	b.evictToCapLocked()
	ratio := b.utilizationLocked()
	b.mu.Unlock()
	if b.metrics != nil {
		b.metrics.ReplayBufferUtilization(ratio)
	}
}

// evictToCapLocked drops the oldest entries until the buffer is within
// bufferCap. The caller holds b.mu.
func (b *RedisEventBus) evictToCapLocked() {
	if b.bufferCap > 0 && len(b.buffer) > b.bufferCap {
		b.buffer = b.buffer[len(b.buffer)-b.bufferCap:]
	}
}

// utilizationLocked returns the buffer fill ratio in [0,1]. The caller
// holds b.mu.
func (b *RedisEventBus) utilizationLocked() float64 {
	if b.bufferCap <= 0 {
		return 0
	}
	return float64(len(b.buffer)) / float64(b.bufferCap)
}

// drainReplayBuffer re-publishes buffered envelopes in FIFO order after a
// successful publish signals the backend has recovered (§12.6 line 683).
// Only one drainer runs at a time; a drain that hits a still-failing
// backend restores the in-flight envelope at the front and stops,
// leaving the rest for the next successful publish.
func (b *RedisEventBus) drainReplayBuffer(ctx context.Context) {
	b.mu.Lock()
	if b.draining || len(b.buffer) == 0 {
		b.mu.Unlock()
		return
	}
	b.draining = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.draining = false
		b.mu.Unlock()
	}()
	for {
		b.mu.Lock()
		if len(b.buffer) == 0 {
			b.mu.Unlock()
			return
		}
		e := b.buffer[0]
		b.buffer = b.buffer[1:]
		ratio := b.utilizationLocked()
		b.mu.Unlock()
		if b.metrics != nil {
			b.metrics.ReplayBufferUtilization(ratio)
		}
		if err := b.send(ctx, e.channel, e.payload); err != nil {
			b.mu.Lock()
			b.buffer = append([]replayEntry{e}, b.buffer...)
			b.evictToCapLocked()
			ratio := b.utilizationLocked()
			b.mu.Unlock()
			if b.metrics != nil {
				b.metrics.ReplayBufferUtilization(ratio)
			}
			return
		}
	}
}

// ReplayBufferLen returns the number of envelopes currently buffered. It
// supports observability and tests; production reads the utilization
// gauge.
func (b *RedisEventBus) ReplayBufferLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buffer)
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
	mu          sync.Mutex
	published   map[EventTopic]int
	publishDurN map[EventTopic]int
	publishDurS map[EventTopic]float64
	dropped     map[string]int // topic|error_type
	handlerDurN map[EventTopic]int
	handlerDurS map[EventTopic]float64
	handlrErr   map[EventTopic]int
	replayUtil  float64
	replayUtilN int
}

// NewCountingBusMetrics returns an empty CountingBusMetrics.
func NewCountingBusMetrics() *CountingBusMetrics {
	return &CountingBusMetrics{
		published:   map[EventTopic]int{},
		publishDurN: map[EventTopic]int{},
		publishDurS: map[EventTopic]float64{},
		dropped:     map[string]int{},
		handlerDurN: map[EventTopic]int{},
		handlerDurS: map[EventTopic]float64{},
		handlrErr:   map[EventTopic]int{},
	}
}

// PublishTotal records a publish attempt.
func (m *CountingBusMetrics) PublishTotal(topic EventTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published[topic]++
}

// PublishDuration records a publish-latency sample.
func (m *CountingBusMetrics) PublishDuration(topic EventTopic, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishDurN[topic]++
	m.publishDurS[topic] += seconds
}

// PublishDropped records a dropped publish.
func (m *CountingBusMetrics) PublishDropped(topic EventTopic, errType PublishErrorType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped[string(topic)+"|"+string(errType)]++
}

// HandlerDuration records a handler-latency sample.
func (m *CountingBusMetrics) HandlerDuration(topic EventTopic, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlerDurN[topic]++
	m.handlerDurS[topic] += seconds
}

// HandlerError records a handler error.
func (m *CountingBusMetrics) HandlerError(topic EventTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlrErr[topic]++
}

// ReplayBufferUtilization records a replay-buffer utilization sample.
func (m *CountingBusMetrics) ReplayBufferUtilization(ratio float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replayUtil = ratio
	m.replayUtilN++
}

// Published returns the publish-attempt count for a topic.
func (m *CountingBusMetrics) Published(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.published[topic]
}

// PublishDurationCount returns how many publish-latency samples were
// recorded for a topic.
func (m *CountingBusMetrics) PublishDurationCount(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishDurN[topic]
}

// PublishDurationSum returns the summed publish-latency seconds for a
// topic.
func (m *CountingBusMetrics) PublishDurationSum(topic EventTopic) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishDurS[topic]
}

// Dropped returns the dropped-publish count for a (topic, error_type).
func (m *CountingBusMetrics) Dropped(topic EventTopic, errType PublishErrorType) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped[string(topic)+"|"+string(errType)]
}

// HandlerDurationCount returns how many handler-latency samples were
// recorded for a topic.
func (m *CountingBusMetrics) HandlerDurationCount(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handlerDurN[topic]
}

// HandlerDurationSum returns the summed handler-latency seconds for a
// topic.
func (m *CountingBusMetrics) HandlerDurationSum(topic EventTopic) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handlerDurS[topic]
}

// HandlerErrors returns the handler-error count for a topic.
func (m *CountingBusMetrics) HandlerErrors(topic EventTopic) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handlrErr[topic]
}

// LastReplayUtilization returns the most recent replay-buffer
// utilization sample.
func (m *CountingBusMetrics) LastReplayUtilization() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replayUtil
}

// ReplayUtilizationSamples returns how many utilization samples were
// recorded.
func (m *CountingBusMetrics) ReplayUtilizationSamples() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replayUtilN
}
