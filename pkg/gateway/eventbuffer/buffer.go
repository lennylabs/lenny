// SPDX-License-Identifier: MIT

// Package eventbuffer implements the §25.3 Gateway Event Buffer — the
// in-process ring buffer of recent operational events — together with
// the §25.3 local Emitter and the §25.5 Redis-stream StreamEmitter that
// record into it. The buffer is the fallback event source when Redis is
// unavailable; the buffer write is always in-process and never depends
// on Redis.
//
// The neutral operational-event vocabulary (OperationalEvent, the
// EventFilter / Pagination / BufferedEventPage query types, the §16.6
// catalogue, the EventEmitter sink interface, the keyer, and the metric
// families) lives in pkg/events. This package imports it; the dependency
// edge is one-way (eventbuffer -> events). The concrete buffer and
// emitters stay in the gateway tree because §25.3 scopes the buffer to
// the gateway, which reads in-process state.
//
// This package is distinct from pkg/gateway/session/sessionevents, which
// is the session event bus backing the §15.1 session SSE stream.
// Operational events are platform-level (alerts, pool transitions,
// upgrades) and follow the §25.3 / §12.6 CloudEvents envelope.
package eventbuffer

import (
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
)

// DefaultBufferCapacity is the §25.3 ring-buffer size (≈250 KB).
const DefaultBufferCapacity = 500

// EventBuffer is the §25.3 in-process ring buffer of operational
// events. It retains the last DefaultBufferCapacity events; each is
// assigned a 1-based monotonic id used as a polling cursor. The buffer
// is per-gateway-replica and holds no Postgres or Redis state.
type EventBuffer struct {
	mu      sync.RWMutex
	now     func() time.Time
	cap     uint64
	ring    []events.BufferedEvent
	head    uint64 // count of events ever appended; the last id assigned
	metrics *events.Metrics
}

// BufferOption configures NewEventBuffer.
type BufferOption func(*EventBuffer)

// WithBufferMetrics wires the §25.3 buffer metrics
// (lenny_events_buffer_length / _queries_total / _gaps_total). spec:
// §25.3 lines 766-772.
func WithBufferMetrics(m *events.Metrics) BufferOption {
	return func(b *EventBuffer) { b.metrics = m }
}

// NewEventBuffer returns an empty buffer of the given capacity. A
// non-positive capacity falls back to DefaultBufferCapacity.
func NewEventBuffer(capacity int, opts ...BufferOption) *EventBuffer {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	b := &EventBuffer{
		now:  time.Now,
		cap:  uint64(capacity),
		ring: make([]events.BufferedEvent, capacity),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Append records an event and returns its monotonic buffer id. When
// the buffer is full the oldest event is overwritten.
func (b *EventBuffer) Append(e events.OperationalEvent) uint64 {
	b.mu.Lock()
	id := b.head + 1
	b.head = id
	b.ring[id%b.cap] = events.BufferedEvent{ID: id, Event: e}
	length := b.head
	if length > b.cap {
		length = b.cap
	}
	b.mu.Unlock()
	// spec: §25.3 line 770 — the buffer gauge tracks the retained count,
	// which caps at the ring capacity once the buffer has wrapped.
	b.metrics.SetBufferLength(int(length))
	return id
}

// oldestID is the lowest event id still retained. The caller holds the
// lock. It is zero when the buffer is empty.
func (b *EventBuffer) oldestID() uint64 {
	if b.head == 0 {
		return 0
	}
	if b.head <= b.cap {
		return 1
	}
	return b.head - b.cap + 1
}

// Query returns the buffered events with an id strictly greater than
// since, in chronological order, narrowed by filter and capped at
// limit (a non-positive limit defaults to 100). When since predates
// the oldest retained event the page reports a gap.
func (b *EventBuffer) Query(since uint64, filter events.EventFilter, limit int) events.BufferedEventPage {
	// spec: §25.3 line 771 — every buffer-endpoint query is counted; a
	// query whose cursor was evicted additionally increments the gap
	// counter (line 772).
	b.metrics.IncQuery()
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	page := events.BufferedEventPage{
		Events: []events.BufferedEvent{},
		Pagination: events.Pagination{
			Cursor:     since,
			Limit:      limit,
			CursorKind: "buffer-seq",
		},
	}
	if b.head == 0 {
		return page
	}
	page.Pagination.HeadCursor = b.head
	oldest := b.oldestID()
	if since+1 < oldest {
		page.Pagination.GapDetected = true
		page.Pagination.GapReason = "cursor evicted from in-memory ring buffer"
		page.Pagination.OldestAvailableCursor = oldest
		page.Pagination.SuggestedAction = "resync"
		b.metrics.IncGap()
	}
	page.BufferAge = b.now().Sub(b.ring[oldest%b.cap].Event.Time).String()

	start := since + 1
	if start < oldest {
		start = oldest
	}
	for id := start; id <= b.head; id++ {
		entry := b.ring[id%b.cap]
		if entry.ID != id {
			continue
		}
		if !filter.Matches(entry.Event) {
			continue
		}
		page.Events = append(page.Events, entry)
		page.Pagination.Cursor = id
		if len(page.Events) >= limit {
			page.Pagination.HasMore = id < b.head
			break
		}
	}
	return page
}

// Lookup returns the buffer id of the retained event whose CloudEvents
// id (the canonical eventKey) equals key, and whether it was found. It
// backs the §25.5 cross-source cursor translation: a cursor produced by
// any source carries an eventKey, and the buffer resolves it to a local
// position by scanning. spec: §25.5 lines 2666-2675 ("translates by
// scanning for the first event with a matching eventKey").
func (b *EventBuffer) Lookup(key string) (uint64, bool) {
	if key == "" {
		return 0, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	oldest := b.oldestID()
	for id := oldest; id <= b.head; id++ {
		entry := b.ring[id%b.cap]
		if entry.ID == id && entry.Event.ID == key {
			return id, true
		}
	}
	return 0, false
}

// Bounds returns the oldest and newest (head) buffer ids still
// retained, along with their CloudEvents eventKeys. found is false when
// the buffer is empty. It backs the §25.5 opaque headCursor /
// oldestAvailableCursor encoding.
func (b *EventBuffer) Bounds() (oldestID, headID uint64, oldestKey, headKey string, found bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.head == 0 {
		return 0, 0, "", "", false
	}
	oldest := b.oldestID()
	return oldest, b.head, b.ring[oldest%b.cap].Event.ID, b.ring[b.head%b.cap].Event.ID, true
}
