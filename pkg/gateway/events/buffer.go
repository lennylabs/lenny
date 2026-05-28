// SPDX-License-Identifier: MIT

// Package opsevents implements the §25.3 Gateway Event Buffer — the
// in-process ring buffer of recent operational events. It is the
// fallback event source when Redis is unavailable; the buffer write is
// always in-process and never depends on Redis.
//
// This package is distinct from pkg/gateway/events, which is the
// session event bus backing the §15.1 session SSE stream. Operational
// events are platform-level (alerts, pool transitions, upgrades) and
// follow the §25.3 / §12.6 CloudEvents envelope.
package events

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// DefaultBufferCapacity is the §25.3 ring-buffer size (≈250 KB).
const DefaultBufferCapacity = 500

// OperationalEvent is the §25.3 / §12.6 operational event: a
// CloudEvents v1.0.2 record. v1 models the envelope as a local struct
// that marshals to the CloudEvents JSON wire format.
type OperationalEvent struct {
	// ID is the CloudEvents id — the canonical eventKey.
	ID string `json:"id"`

	// Source identifies the emitting context.
	Source string `json:"source,omitempty"`

	// SpecVersion is the CloudEvents spec version.
	SpecVersion string `json:"specversion"`

	// Type is the CloudEvents type, of the form dev.lenny.<short_name>.
	Type string `json:"type"`

	// Subject is the CloudEvents v1.0.2 subject context attribute. It
	// names the resource the event is about — for the §16.6 catalogue
	// this is the canonical resource identifier (e.g. "pool/default-
	// gvisor", "session/abc123", "credential_pool/openai-prod"). Agents
	// filter related events without parsing the payload by matching
	// CloudEvents subject; the buffer's matchFilter honours the §25.5
	// ?resourceType / ?resourceId filters against this field. spec:
	// §16.6 catalogue / CloudEvents v1.0.2 subject context attribute.
	Subject string `json:"subject,omitempty"`

	// Time is the event timestamp.
	Time time.Time `json:"time"`

	// Severity is the Lenny extension attribute the §25.3 event-buffer
	// query filters on.
	Severity string `json:"severity,omitempty"`

	// DataContentType is the media type of Data.
	DataContentType string `json:"datacontenttype,omitempty"`

	// Data is the event-specific payload.
	Data json.RawMessage `json:"data,omitempty"`
}

// BufferedEvent is one event in the buffer, tagged with its monotonic
// buffer id — the cursor a poller advances through.
type BufferedEvent struct {
	ID    uint64           `json:"id"`
	Event OperationalEvent `json:"event"`
}

// EventFilter narrows a §25.3 buffer query. An empty field does not
// filter on that dimension.
type EventFilter struct {
	// EventType matches the CloudEvents type, either in full
	// (dev.lenny.alert_fired) or by short-name suffix (alert_fired).
	EventType string

	// Severity matches the event's severity extension attribute.
	Severity string
}

// BufferedEventPage is the §25.3 GET /v1/admin/events/buffer response.
type BufferedEventPage struct {
	Events  []BufferedEvent `json:"events"`
	Cursor  uint64          `json:"cursor"`
	HasMore bool            `json:"hasMore"`

	// GapDetected reports that the requested cursor predated the
	// oldest retained event — the buffer wrapped past it (§25.3).
	GapDetected bool `json:"gapDetected,omitempty"`

	// OldestAvailableCursor is the oldest id still in the buffer, set
	// alongside GapDetected so a poller can recover.
	OldestAvailableCursor uint64 `json:"oldestAvailableCursor,omitempty"`

	// BufferAge is the duration since the oldest retained event.
	BufferAge string `json:"bufferAge,omitempty"`
}

// EventBuffer is the §25.3 in-process ring buffer of operational
// events. It retains the last DefaultBufferCapacity events; each is
// assigned a 1-based monotonic id used as a polling cursor. The buffer
// is per-gateway-replica and holds no Postgres or Redis state.
type EventBuffer struct {
	mu   sync.RWMutex
	now  func() time.Time
	cap  uint64
	ring []BufferedEvent
	head uint64 // count of events ever appended; the last id assigned
}

// NewEventBuffer returns an empty buffer of the given capacity. A
// non-positive capacity falls back to DefaultBufferCapacity.
func NewEventBuffer(capacity int) *EventBuffer {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	return &EventBuffer{
		now:  time.Now,
		cap:  uint64(capacity),
		ring: make([]BufferedEvent, capacity),
	}
}

// Append records an event and returns its monotonic buffer id. When
// the buffer is full the oldest event is overwritten.
func (b *EventBuffer) Append(e OperationalEvent) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head++
	id := b.head
	b.ring[id%b.cap] = BufferedEvent{ID: id, Event: e}
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
func (b *EventBuffer) Query(since uint64, filter EventFilter, limit int) BufferedEventPage {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	page := BufferedEventPage{Events: []BufferedEvent{}, Cursor: since}
	if b.head == 0 {
		return page
	}
	oldest := b.oldestID()
	if since+1 < oldest {
		page.GapDetected = true
		page.OldestAvailableCursor = oldest
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
		if !matchFilter(entry.Event, filter) {
			continue
		}
		page.Events = append(page.Events, entry)
		page.Cursor = id
		if len(page.Events) >= limit {
			page.HasMore = id < b.head
			break
		}
	}
	return page
}

// matchFilter reports whether e satisfies the filter. An empty filter
// field does not constrain that dimension.
func matchFilter(e OperationalEvent, f EventFilter) bool {
	if f.Severity != "" && e.Severity != f.Severity {
		return false
	}
	if f.EventType != "" && e.Type != f.EventType &&
		!strings.HasSuffix(e.Type, "."+f.EventType) {
		return false
	}
	return true
}
