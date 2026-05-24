// SPDX-License-Identifier: MIT

// Package events is the gateway's session event bus. It backs the
// §15.1 `GET /v1/sessions/{id}/events` SSE stream: session activity
// (message delivered, response produced, state transition) is
// published to the bus and relayed to every subscribed client. Each
// event carries a monotonic per-session sequence so a reconnecting
// client can resume with a cursor (the §15.1 streaming-reconnect
// contract).
//
// In single-replica deployments the Bus uses its in-memory state
// alone. In multi-replica deployments the Bus pairs with the §4.4
// line 225 / §12.3.7 RedisRelay (see redisrelay.go): every Publish
// fans out to Redis Streams via `XADD`, and every Subscribe pulls
// the cross-replica history via `XRANGE` so a client that reconnects
// to a different replica sees the prior events.
//
// spec: §4.4 line 225 — durable event cursors / stream offsets across
// replicas.
package sessionevents

import (
	"context"
	"sync"
	"time"
)

// Event is one session event.
type Event struct {
	// Seq is the monotonic per-session sequence, starting at 1.
	Seq uint64 `json:"seq"`

	// SessionID scopes the event.
	SessionID string `json:"sessionId"`

	// Type is the event type (e.g., `message_delivered`,
	// `response`, `state_changed`).
	Type string `json:"type"`

	// Data is the event payload, an already-marshalled JSON object.
	Data string `json:"data"`

	// Timestamp is the UTC instant the event was published.
	Timestamp time.Time `json:"timestamp"`
}

// Bus is the session event bus. The in-memory state (per-session
// sequence counter, replay history, live subscriber list) is the
// authoritative source on the publishing replica. When the optional
// RedisRelay is wired the Bus also fans out every publish to Redis
// Streams so reading replicas can serve a reconnect-with-cursor
// against the cross-replica stream.
type Bus struct {
	mu sync.Mutex
	// seq tracks the next sequence per session.
	seq map[string]uint64
	// history retains recent events per session for cursor replay.
	history map[string][]Event
	// subs maps a session to its active subscriber channels.
	subs map[string][]*subscription
	// maxHistory bounds the per-session retained event count.
	maxHistory int
	// relay is the §4.4 / §12.3.7 cross-replica Redis fan-out. A nil
	// relay reduces the Bus to the single-replica path.
	relay *RedisRelay
}

// subscription is one active SSE client.
type subscription struct {
	ch     chan Event
	closed bool
}

// NewBus returns an empty Bus. maxHistory bounds the per-session
// in-memory replay buffer; pass 0 for the default of 256. The Bus
// runs single-replica until WithRedisRelay attaches the cross-replica
// fan-out.
func NewBus(maxHistory int) *Bus {
	if maxHistory <= 0 {
		maxHistory = 256
	}
	return &Bus{
		seq:        map[string]uint64{},
		history:    map[string][]Event{},
		subs:       map[string][]*subscription{},
		maxHistory: maxHistory,
	}
}

// WithRedisRelay attaches the §4.4 / §12.3.7 cross-replica relay so
// every Publish fans out to Redis Streams and every Subscribe
// consults the cross-replica history. A nil relay disables the
// cross-replica path (the default single-replica behaviour).
//
// The relay attaches at construction time; later changes require a
// fresh Bus so the publish path and history path stay consistent.
//
// spec: §4.4 line 225.
func (b *Bus) WithRedisRelay(relay *RedisRelay) *Bus {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.relay = relay
	return b
}

// Publish appends an event to a session's stream, assigns its Seq,
// retains it for cursor replay, and fans it out to every live
// subscriber. A slow subscriber whose channel is full is skipped
// (the event remains in history for a reconnect-with-cursor).
//
// When the §4.4 / §12.3.7 RedisRelay is wired the published event is
// also XADDed onto the Redis stream so reading replicas can serve a
// reconnect from another replica against the cross-replica history.
// The Redis fan-out is best-effort: a Redis failure logs but does
// not roll back the in-memory publish.
func (b *Bus) Publish(sessionID, eventType, data string, now time.Time) Event {
	b.mu.Lock()
	b.seq[sessionID]++
	ev := Event{
		Seq:       b.seq[sessionID],
		SessionID: sessionID,
		Type:      eventType,
		Data:      data,
		Timestamp: now.UTC(),
	}
	hist := append(b.history[sessionID], ev)
	if len(hist) > b.maxHistory {
		hist = hist[len(hist)-b.maxHistory:]
	}
	b.history[sessionID] = hist
	for _, sub := range b.subs[sessionID] {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// Slow consumer — drop the live delivery; the event is
			// in history for a reconnect-with-cursor.
		}
	}
	relay := b.relay
	b.mu.Unlock()
	if relay != nil {
		// Fan-out outside the lock so a slow Redis client does not
		// stall in-memory subscribers on the same replica.
		relay.PublishEvent(context.Background(), ev)
	}
	return ev
}

// Subscription is the caller-facing handle returned by Subscribe.
type Subscription struct {
	bus       *Bus
	sessionID string
	sub       *subscription
	// Backlog holds events with Seq > afterSeq that were already in
	// history at subscribe time; the caller drains it before reading
	// Events.
	Backlog []Event
}

// Events returns the channel of live events. The channel is closed
// when Close is called.
func (s *Subscription) Events() <-chan Event { return s.sub.ch }

// Close detaches the subscription from the bus and closes the event
// channel. Safe to call multiple times.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if s.sub.closed {
		return
	}
	s.sub.closed = true
	close(s.sub.ch)
	subs := s.bus.subs[s.sessionID]
	kept := subs[:0]
	for _, sub := range subs {
		if sub != s.sub {
			kept = append(kept, sub)
		}
	}
	s.bus.subs[s.sessionID] = kept
}

// Subscribe registers a new subscriber for sessionID. Events with
// Seq > afterSeq already in history are returned in Backlog so a
// reconnecting client resumes exactly where it left off; live
// events arrive on Events(). bufferSize bounds the live channel.
//
// When the §4.4 / §12.3.7 RedisRelay is wired the Backlog merges the
// local in-memory history with the Redis stream history so a client
// reconnecting to this replica also sees events originally published
// on a sibling replica.
func (b *Bus) Subscribe(sessionID string, afterSeq uint64, bufferSize int) *Subscription {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	b.mu.Lock()
	sub := &subscription{ch: make(chan Event, bufferSize)}
	b.subs[sessionID] = append(b.subs[sessionID], sub)

	var backlog []Event
	for _, ev := range b.history[sessionID] {
		if ev.Seq > afterSeq {
			backlog = append(backlog, ev)
		}
	}
	relay := b.relay
	b.mu.Unlock()

	// When the cross-replica relay is wired and the local history
	// either has not seen this session yet or starts past the
	// requested cursor, fetch the Redis stream history and merge it
	// in (deduping by Seq).
	if relay != nil {
		remote, err := relay.History(context.Background(), sessionID, afterSeq)
		if err == nil {
			backlog = mergeByCursor(backlog, remote, afterSeq)
		}
	}
	return &Subscription{bus: b, sessionID: sessionID, sub: sub, Backlog: backlog}
}

// mergeByCursor combines a local history slice with a remote history
// slice, returning a single slice sorted by Seq with no duplicates
// (a Seq present in both lists keeps the local copy). The output
// excludes any Seq ≤ afterSeq.
func mergeByCursor(local, remote []Event, afterSeq uint64) []Event {
	// Deduplicate by Seq, preferring local entries (they carry the
	// authoritative payload from the publishing replica before the
	// relay re-encode).
	bySeq := make(map[uint64]Event, len(local)+len(remote))
	for _, ev := range local {
		if ev.Seq > afterSeq {
			bySeq[ev.Seq] = ev
		}
	}
	for _, ev := range remote {
		if ev.Seq <= afterSeq {
			continue
		}
		if _, has := bySeq[ev.Seq]; !has {
			bySeq[ev.Seq] = ev
		}
	}
	out := make([]Event, 0, len(bySeq))
	for _, ev := range bySeq {
		out = append(out, ev)
	}
	sortBySeq(out)
	return out
}

// sortBySeq sorts a slice of Event in ascending Seq order. Used by
// the cross-replica history-merge path so the backlog arrives in
// monotonic-cursor order.
func sortBySeq(out []Event) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Seq > out[j].Seq; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

// History returns the retained events for a session with Seq >
// afterSeq. Used by the polling (non-SSE) read path.
func (b *Bus) History(sessionID string, afterSeq uint64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Event
	for _, ev := range b.history[sessionID] {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out
}

// ActiveSubscribers reports the total number of live SSE subscribers
// across all sessions. The gateway uses this value as the source of
// the §4.1 lenny_gateway_active_streams gauge — the secondary HPA
// metric reflecting in-flight streaming connections on this replica.
// Closed subscriptions are excluded; the count drops as soon as a
// client disconnects and Close runs.
func (b *Bus) ActiveSubscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var n int
	for _, subs := range b.subs {
		for _, sub := range subs {
			if !sub.closed {
				n++
			}
		}
	}
	return n
}
