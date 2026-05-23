// SPDX-License-Identifier: MIT

// Package events is the gateway's in-process session event bus. It
// backs the §15.1 `GET /v1/sessions/{id}/events` SSE stream: session
// activity (message delivered, response produced, state transition)
// is published to the bus and relayed to every subscribed client.
//
// The bus is in-memory and single-replica — production swaps in the
// §10.1 Redis-backed cross-replica EventBus behind the same
// Publish / Subscribe surface. Each event carries a monotonic
// per-session sequence so a reconnecting client can resume with a
// cursor (the §15.1 streaming-reconnect contract).
package sessionevents

import (
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

// Bus is the in-memory session event bus.
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
}

// subscription is one active SSE client.
type subscription struct {
	ch     chan Event
	closed bool
}

// NewBus returns an empty Bus. maxHistory bounds the per-session
// replay buffer; pass 0 for the default of 256.
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

// Publish appends an event to a session's stream, assigns its Seq,
// retains it for cursor replay, and fans it out to every live
// subscriber. A slow subscriber whose channel is full is skipped
// (the event remains in history for a reconnect-with-cursor).
func (b *Bus) Publish(sessionID, eventType, data string, now time.Time) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
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
func (b *Bus) Subscribe(sessionID string, afterSeq uint64, bufferSize int) *Subscription {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := &subscription{ch: make(chan Event, bufferSize)}
	b.subs[sessionID] = append(b.subs[sessionID], sub)

	var backlog []Event
	for _, ev := range b.history[sessionID] {
		if ev.Seq > afterSeq {
			backlog = append(backlog, ev)
		}
	}
	return &Subscription{bus: b, sessionID: sessionID, sub: sub, Backlog: backlog}
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
