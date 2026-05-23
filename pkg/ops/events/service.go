// SPDX-License-Identifier: MIT

// Package events implements the §25.5 Operational Event Stream service
// hosted by lenny-ops. It exposes three transports over one source of
// truth — the §25.3 in-memory ring buffer of CloudEvents:
//
//   - GET /v1/admin/events/stream — SSE delivery with Last-Event-ID
//     resume.
//   - GET /v1/admin/events — polling with cursor + the canonical
//     §25.2 pagination envelope.
//   - Webhook fan-out — pushes every published event into an optional
//     callback so the existing pkg/ops/opsservice webhook worker keeps
//     delivering even when the SSE/polling sides are quiet.
//
// The Service is the single owner of the operational-event buffer on
// the lenny-ops side. The gateway emits its events through the §25.3
// pkg/gateway/opsevents.Emitter; the lenny-ops side has its own buffer
// for events that originate in lenny-ops (escalations, drift,
// platform-upgrade lifecycle, ops self-health). Both feed the same
// Redis stream (§25.5 "Both write to the same Redis stream"); the
// Service consumes either side via Publish, which keeps the in-memory
// transport-agnostic.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
)

// DefaultBufferCapacity is the §25.5 in-memory cap for the Service's
// own ring buffer. Matches the gateway-side default so the polling
// envelope behaves identically regardless of which buffer the caller
// landed on.
const DefaultBufferCapacity = opsevents.DefaultBufferCapacity

// WebhookFanOut is the §25.5 callback the Service invokes after every
// Publish so the existing webhook delivery worker fans out the same
// event to its subscriptions. A nil callback disables webhook
// integration; the SSE/polling sides still work.
type WebhookFanOut func(context.Context, opsevents.OperationalEvent)

// Service is the §25.5 event-stream service.
type Service struct {
	buffer *opsevents.EventBuffer
	now    func() time.Time

	subsMu sync.Mutex
	subs   []*subscription

	webhook WebhookFanOut
}

// subscription is one active SSE subscriber.
type subscription struct {
	ch     chan opsevents.BufferedEvent
	filter opsevents.EventFilter
	closed bool
}

// Options configures a Service.
type Options struct {
	// Capacity bounds the in-memory ring buffer the Service maintains.
	// A non-positive value uses DefaultBufferCapacity.
	Capacity int
	// Now overrides the time source; tests use it to anchor timestamps.
	Now func() time.Time
	// Webhook is the §25.5 webhook fan-out callback. A nil callback
	// disables webhook delivery.
	Webhook WebhookFanOut
}

// New returns a Service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		buffer:  opsevents.NewEventBuffer(opts.Capacity),
		now:     now,
		webhook: opts.Webhook,
	}
}

// Publish stamps the §25.3 / §25.5 CloudEvents envelope, records the
// event in the buffer, fans out to live SSE subscribers, and forwards
// to the webhook callback when configured. Returns the assigned buffer
// id (the polling cursor).
func (s *Service) Publish(ctx context.Context, e opsevents.OperationalEvent) uint64 {
	if e.SpecVersion == "" {
		e.SpecVersion = opsevents.CloudEventsSpecVersion
	}
	if e.Time.IsZero() {
		e.Time = s.now().UTC()
	}
	if e.ID == "" {
		e.ID = fmt.Sprintf("ops:%d", e.Time.UnixNano())
	}
	id := s.buffer.Append(e)
	s.fanOutToSubscribers(opsevents.BufferedEvent{ID: id, Event: e})
	if s.webhook != nil {
		s.webhook(ctx, e)
	}
	return id
}

// fanOutToSubscribers delivers ev to every subscriber whose filter
// matches. A slow subscriber whose channel is full is skipped — the
// event remains in the ring buffer so the subscriber can recover via
// the polling cursor on reconnect.
func (s *Service) fanOutToSubscribers(ev opsevents.BufferedEvent) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, sub := range s.subs {
		if sub.closed {
			continue
		}
		if !matchFilter(ev.Event, sub.filter) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// matchFilter applies the same rules the gateway-side buffer uses
// (§25.3): both empty-field passes are no-ops, and EventType matches
// the full CloudEvents type or its short-name suffix.
func matchFilter(e opsevents.OperationalEvent, f opsevents.EventFilter) bool {
	if f.Severity != "" && e.Severity != f.Severity {
		return false
	}
	if f.EventType != "" && e.Type != f.EventType &&
		!strings.HasSuffix(e.Type, "."+f.EventType) {
		return false
	}
	return true
}

// Query returns the §25.5 polling page: events after the cursor,
// narrowed by filter, capped at limit. Uses the §25.2 pagination
// envelope (Cursor + HasMore + GapDetected).
func (s *Service) Query(since uint64, filter opsevents.EventFilter, limit int) opsevents.BufferedEventPage {
	return s.buffer.Query(since, filter, limit)
}

// subscribe registers a new SSE subscriber. The returned channel
// receives every matching event published after the subscription is
// installed. The caller must call unsubscribe to release the slot.
func (s *Service) subscribe(filter opsevents.EventFilter, buffer int) *subscription {
	if buffer <= 0 {
		buffer = 64
	}
	sub := &subscription{ch: make(chan opsevents.BufferedEvent, buffer), filter: filter}
	s.subsMu.Lock()
	s.subs = append(s.subs, sub)
	s.subsMu.Unlock()
	return sub
}

// unsubscribe detaches sub from the Service and closes its channel.
// Safe to call multiple times.
func (s *Service) unsubscribe(sub *subscription) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	close(sub.ch)
	kept := s.subs[:0]
	for _, existing := range s.subs {
		if existing != sub {
			kept = append(kept, existing)
		}
	}
	s.subs = kept
}

// SubscriberCount returns the number of active SSE subscribers. Used
// by the §25.5 lenny_ops_events_stream_subscribers gauge.
func (s *Service) SubscriberCount() int {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	return len(s.subs)
}

// HandleStream is the SSE handler for GET /v1/admin/events/stream
// (§25.5). It replays buffered events whose id is > the Last-Event-ID
// header (or ?afterId=) and then streams live events until the client
// disconnects. The handler accepts ?eventType= and ?severity= filters
// matching the polling endpoint's semantics.
func (s *Service) HandleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	filter := opsevents.EventFilter{
		EventType: r.URL.Query().Get("eventType"),
		Severity:  r.URL.Query().Get("severity"),
	}
	since := resumeCursor(r)
	sub := s.subscribe(filter, 64)
	defer s.unsubscribe(sub)

	// Backlog: replay buffered events the client missed (§25.5 "the
	// canonical cross-source identifier is the eventKey"). The
	// subscription is already installed so we never miss a concurrent
	// publish.
	backlog := s.buffer.Query(since, filter, 0)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for _, ev := range backlog.Events {
		writeSSEFrame(w, ev)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			writeSSEFrame(w, ev)
			flusher.Flush()
		}
	}
}

// HandlePoll is the polling handler for GET /v1/admin/events (§25.5).
// Pagination envelope follows §25.2: pagination.cursor +
// pagination.hasMore.
func (s *Service) HandlePoll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since uint64
	if v := q.Get("since"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			since = n
		}
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit > DefaultBufferCapacity {
		limit = DefaultBufferCapacity
	}
	page := s.buffer.Query(since, opsevents.EventFilter{
		EventType: q.Get("eventType"),
		Severity:  q.Get("severity"),
	}, limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// resumeCursor reads the §25.5 SSE resume cursor from the request,
// preferring the SSE-standard Last-Event-ID header and falling back
// to ?afterId=.
func resumeCursor(r *http.Request) uint64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("afterId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// writeSSEFrame writes one BufferedEvent as an SSE record per §25.5.
// The id field is the buffer cursor so a client reconnecting with
// Last-Event-ID picks up exactly where it left off.
func writeSSEFrame(w http.ResponseWriter, ev opsevents.BufferedEvent) {
	body, err := json.Marshal(ev.Event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\n", ev.ID)
	if ev.Event.Type != "" {
		fmt.Fprintf(w, "event: %s\n", ev.Event.Type)
	}
	fmt.Fprintf(w, "data: %s\n\n", body)
}
