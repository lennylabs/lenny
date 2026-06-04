// SPDX-License-Identifier: MIT

// Package events implements the §25.5 Operational Event Stream service
// hosted by lenny-ops. It exposes three transports over one source of
// truth — the §25.3 in-memory ring buffer of CloudEvents:
//
//   - GET /v1/admin/events/stream — SSE delivery with Last-Event-ID
//     resume keyed on the CloudEvents id (the canonical eventKey).
//   - GET /v1/admin/events — polling with the §25.2 canonical
//     pagination envelope and opaque source-kind cursors.
//   - Webhook fan-out — pushes every published event into an optional
//     callback so the existing pkg/ops/opsservice webhook worker keeps
//     delivering even when the SSE/polling sides are quiet.
//
// The Service is the single owner of the operational-event buffer on
// the lenny-ops side. The gateway emits its events through the §25.3
// pkg/gateway/gwevents.Emitter; the lenny-ops side has its own buffer
// for events that originate in lenny-ops (escalations, drift,
// platform-upgrade lifecycle, ops self-health). Both feed the same
// Redis stream (§25.5 "Both write to the same Redis stream"); the
// Service consumes either side via Publish, which keeps the in-memory
// transport-agnostic. The Redis stream source and the Redis-down /
// gateway-down degradation matrix are F-25.5.1 / F-25.5.14; this
// package serves the §25.5 read surface over the buffer source and
// encodes the source kind into every cursor so the surface is
// forward-compatible when the Redis source lands.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// DefaultBufferCapacity is the §25.5 in-memory cap for the Service's
// own ring buffer. Matches the gateway-side default so the polling
// envelope behaves identically regardless of which buffer the caller
// landed on.
const DefaultBufferCapacity = gwevents.DefaultBufferCapacity

// DefaultPollLimit and MaxPollLimit are the §25.2 canonical pagination
// bounds for GET /v1/admin/events. spec: §25.2 line 240 (default 100,
// max 1000).
const (
	DefaultPollLimit = 100
	MaxPollLimit     = 1000
)

// codeInvalidEventFilter is the §25.5 error code for an unparseable
// cursor, an unrecognized filter token, or a malformed query parameter.
// spec: §25.5 line 2795.
const codeInvalidEventFilter = "INVALID_EVENT_FILTER"

// WebhookFanOut is the §25.5 callback the Service invokes after every
// Publish so the existing webhook delivery worker fans out the same
// event to its subscriptions. A nil callback disables webhook
// integration; the SSE/polling sides still work.
type WebhookFanOut func(context.Context, gwevents.OperationalEvent)

// Service is the §25.5 event-stream service.
type Service struct {
	buffer    *gwevents.EventBuffer
	now       func() time.Time
	replicaID string
	nonce     atomic.Uint64

	subsMu sync.Mutex
	subs   []*subscription

	webhook WebhookFanOut
	onGap   func()
	health  SourceHealth
}

// subscription is one active SSE subscriber.
type subscription struct {
	ch     chan gwevents.BufferedEvent
	filter gwevents.EventFilter
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
	// ReplicaID is the per-replica identifier baked into each event's
	// canonical eventKey ({replicaID}:{emittedAt}:{nonce}). An empty
	// value falls back to "ops". spec: §25.5 line 2671.
	ReplicaID string
	// OnGap, when non-nil, is invoked once per stream or poll request that
	// resolves a cursor whose event has been evicted (the response carries
	// pagination.gapDetected: true). It backs the §25.5
	// lenny_ops_events_stream_gaps_total counter. spec: §25.5 line 2788.
	OnGap func()
	// SourceHealth, when non-nil, drives the §25.5 degradation matrix: the
	// poll and stream surfaces consult it to decide whether to attach a
	// degradation envelope (Redis-down fall-back to the gateway buffer),
	// emit the :degradation SSE comment, or return 503
	// EVENT_STREAM_UNAVAILABLE (dual Redis + gateway outage). A nil value
	// is treated as fully healthy. spec: §25.5 lines 2768-2780.
	SourceHealth SourceHealth
}

// New returns a Service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	replicaID := opts.ReplicaID
	if replicaID == "" {
		replicaID = "ops"
	}
	return &Service{
		buffer:    gwevents.NewEventBuffer(opts.Capacity),
		now:       now,
		replicaID: replicaID,
		webhook:   opts.Webhook,
		onGap:     opts.OnGap,
		health:    opts.SourceHealth,
	}
}

// observeGap fires the OnGap hook when configured. spec: §25.5 line 2788.
func (s *Service) observeGap() {
	if s.onGap != nil {
		s.onGap()
	}
}

// Publish stamps the §25.3 / §25.5 CloudEvents envelope, records the
// event in the buffer, fans out to live SSE subscribers, and forwards
// to the webhook callback when configured. Returns the assigned buffer
// id (the polling cursor). The error returns satisfies the §4.0
// gwevents.EventEmitter contract so the Service is a drop-in for any
// subsystem that takes an EventEmitter; in v1 the in-memory write
// never errors, so the error is always nil unless ctx is cancelled.
func (s *Service) Publish(ctx context.Context, e gwevents.OperationalEvent) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if e.SpecVersion == "" {
		e.SpecVersion = gwevents.CloudEventsSpecVersion
	}
	if e.Time.IsZero() {
		e.Time = s.now().UTC()
	}
	if e.ID == "" {
		e.ID = s.eventKey(e.Time)
	}
	id := s.buffer.Append(e)
	s.fanOutToSubscribers(gwevents.BufferedEvent{ID: id, Event: e})
	if s.webhook != nil {
		s.webhook(ctx, e)
	}
	return id, nil
}

// eventKey composes the §25.5 canonical cross-source identifier
// {replicaID}:{emittedAt}:{nonce}. The nonce is a per-replica
// monotonic counter so the key is unique even when two events share a
// timestamp; this makes the SSE Last-Event-ID resume and the opaque
// poll cursor unambiguous. spec: §25.5 lines 2666-2675.
func (s *Service) eventKey(at time.Time) string {
	return fmt.Sprintf("%s:%d:%d", s.replicaID, at.UnixNano(), s.nonce.Add(1))
}

// Emit satisfies the §25.3 gwevents.EventEmitter interface (error-only)
// so the Service can be passed to any subsystem that takes an
// EventEmitter. It forwards to Publish and discards the buffer id, which
// the query side reads back via the buffer cursor.
func (s *Service) Emit(ctx context.Context, e gwevents.OperationalEvent) error {
	_, err := s.Publish(ctx, e)
	return err
}

// Compile-time guard that *Service satisfies gwevents.EventEmitter.
var _ gwevents.EventEmitter = (*Service)(nil)

// fanOutToSubscribers delivers ev to every subscriber whose filter
// matches. A slow subscriber whose channel is full is skipped — the
// event remains in the ring buffer so the subscriber can recover via
// the polling cursor on reconnect.
func (s *Service) fanOutToSubscribers(ev gwevents.BufferedEvent) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, sub := range s.subs {
		if sub.closed {
			continue
		}
		if !sub.filter.Matches(ev.Event) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// Query returns the §25.5 polling page: events after the cursor,
// narrowed by filter, capped at limit. Uses the §25.2 pagination
// envelope (Cursor + HasMore + GapDetected). It is the low-level buffer
// query; HandlePoll wraps it with the opaque source-kind cursor.
func (s *Service) Query(since uint64, filter gwevents.EventFilter, limit int) gwevents.BufferedEventPage {
	return s.buffer.Query(since, filter, limit)
}

// subscribe registers a new SSE subscriber. The returned channel
// receives every matching event published after the subscription is
// installed. The caller must call unsubscribe to release the slot.
func (s *Service) subscribe(filter gwevents.EventFilter, buffer int) *subscription {
	if buffer <= 0 {
		buffer = 64
	}
	sub := &subscription{ch: make(chan gwevents.BufferedEvent, buffer), filter: filter}
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
// by the §25.5 lenny_ops_events_sse_active_connections gauge.
func (s *Service) SubscriberCount() int {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	return len(s.subs)
}

// parseFilter builds the §25.5 canonical filter from the query string
// and validates its tokens. It honours ?eventType=, ?severity=,
// ?resourceType=, ?resourceId=, ?since=, and ?until= (the last two
// RFC 3339 timestamps). An unrecognized event type or severity, or a
// malformed timestamp, returns an error the caller maps to
// INVALID_EVENT_FILTER. spec: §25.2 lines 334-345; §25.5 lines 2693,
// 2795.
func parseFilter(q url.Values) (gwevents.EventFilter, error) {
	f := gwevents.EventFilter{
		EventType:    q.Get("eventType"),
		Severity:     q.Get("severity"),
		ResourceType: q.Get("resourceType"),
		ResourceID:   q.Get("resourceId"),
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return gwevents.EventFilter{}, fmt.Errorf("since must be an RFC 3339 timestamp: %w", err)
		}
		f.Since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return gwevents.EventFilter{}, fmt.Errorf("until must be an RFC 3339 timestamp: %w", err)
		}
		f.Until = t
	}
	if err := gwevents.ValidateFilterTokens(f.EventType, f.Severity); err != nil {
		return gwevents.EventFilter{}, err
	}
	return f, nil
}

// parseLimit applies the §25.2 page-size bounds: default 100, max 1000.
// A non-numeric value is treated as the default rather than an error so
// a stray ?limit= does not break a poll. spec: §25.2 line 240.
func parseLimit(q url.Values) int {
	limit := DefaultPollLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > MaxPollLimit {
		limit = MaxPollLimit
	}
	return limit
}

// parseSortOrder reports whether the §25.2 ?sortOrder= parameter
// reverses the default oldest-first ordering. An unrecognized value is
// an error the caller maps to INVALID_EVENT_FILTER. spec: §25.2 line
// 243.
func parseSortOrder(q url.Values) (desc bool, err error) {
	switch q.Get("sortOrder") {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, fmt.Errorf("sortOrder must be 'asc' or 'desc'")
	}
}

// HandlePoll is the polling handler for GET /v1/admin/events (§25.5).
// It returns the §25.2 canonical pagination envelope with an opaque
// source-kind cursor, honours ?cursor=, ?limit=, ?sortOrder=, and the
// canonical filter set, and reports a gap when the cursor's event has
// been evicted. spec: §25.5 lines 2687-2699; §25.2 lines 234-275.
func (s *Service) HandlePoll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// §25.5 degradation case 4: when both the Redis stream and the gateway
	// buffer are unreachable, polling for gateway-originated events cannot
	// be served and cannot partial-serve, so it fails with a transient
	// 503 the agent retries. The SSE surface still serves
	// lenny-ops-originated events from the local buffer. spec: §25.5 lines
	// 2768-2780.
	_, deg, dualDown := s.streamState()
	if dualDown {
		writeStreamUnavailable(w)
		return
	}
	filter, err := parseFilter(q)
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, codeInvalidEventFilter, conventions.CategoryPermanent, err.Error())
		return
	}
	desc, err := parseSortOrder(q)
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, codeInvalidEventFilter, conventions.CategoryPermanent, err.Error())
		return
	}
	kind, eventKey, err := decodeCursor(q.Get("cursor"))
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, codeInvalidEventFilter, conventions.CategoryPermanent, err.Error())
		return
	}
	limit := parseLimit(q)

	page := s.pollPage(kind, eventKey, filter, limit, desc)
	// §25.5 degradation case 1: serving from the gateway-buffer fall-back
	// during a Redis outage is reported as response metadata
	// (EVENT_STREAM_DEGRADED, HTTP 200), not an HTTP error. spec: §25.5
	// lines 2768-2772.
	page.Degradation = deg
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// pollPage resolves the incoming opaque cursor to a buffer position by
// eventKey, runs the buffer query, and wraps the result in the §25.2
// canonical envelope with opaque source-kind cursors. When the cursor's
// eventKey is no longer retained (evicted), the page reports gapDetected
// and serves from the oldest retained event. spec: §25.5 lines
// 2666-2699.
func (s *Service) pollPage(cursorKind, eventKey string, filter gwevents.EventFilter, limit int, desc bool) EventPage {
	var since uint64
	servedKind := SourceKindBuffer
	gap := false
	if eventKey != "" {
		if id, ok := s.buffer.Lookup(eventKey); ok {
			since = id
			servedKind = transitionKind(cursorKind)
		} else {
			// The cursor's event has been evicted (or came from a source
			// this buffer cannot translate). Serve from the oldest event
			// and flag the gap so the agent re-reads platform state.
			gap = true
			servedKind = transitionKind(cursorKind)
		}
	}

	bp := s.buffer.Query(since, filter, limit)
	_, _, oldestKey, headKey, found := s.buffer.Bounds()

	items := bp.Events
	if desc {
		items = reversed(items)
	}

	page := EventPage{
		Items: items,
		Pagination: Pagination{
			HasMore:    bp.Pagination.HasMore,
			CursorKind: servedKind,
		},
	}
	if found {
		page.Pagination.HeadCursor = encodeCursor(SourceKindBuffer, headKey)
	}
	if n := len(bp.Events); n > 0 {
		page.Pagination.Cursor = encodeCursor(SourceKindBuffer, bp.Events[n-1].Event.ID)
	} else if eventKey != "" && !gap {
		// No new events: echo the caller's position so a repeat poll
		// resumes from the same point.
		page.Pagination.Cursor = encodeCursor(SourceKindBuffer, eventKey)
	}
	if gap {
		page.Pagination.GapDetected = true
		page.Pagination.OldestAvailableCursor = encodeCursor(SourceKindBuffer, oldestKey)
		s.observeGap()
	}
	return page
}

// transitionKind reports the §25.5 cursorKind for a page served from
// the buffer in response to a cursor produced by cursorKind. A buffer
// (or empty) cursor stays "buffer"; any other source produced cursor
// served here spans a transition and is reported as "mixed". spec:
// §25.5 lines 2666-2675.
func transitionKind(cursorKind string) string {
	if cursorKind == "" || cursorKind == SourceKindBuffer {
		return SourceKindBuffer
	}
	return SourceKindMixed
}

// reversed returns a newest-first copy of events for ?sortOrder=desc.
// The opaque cursor still tracks chronological progression so paging
// continues to advance oldest-to-newest regardless of display order.
func reversed(events []gwevents.BufferedEvent) []gwevents.BufferedEvent {
	out := make([]gwevents.BufferedEvent, len(events))
	for i, ev := range events {
		out[len(events)-1-i] = ev
	}
	return out
}

// HandleStream is the SSE handler for GET /v1/admin/events/stream
// (§25.5). It replays buffered events the client missed since its
// Last-Event-ID (the CloudEvents id / eventKey) and then streams live
// events until the client disconnects. Each frame's id: line carries
// the CloudEvents id so a reconnecting client resumes from the correct
// position. The handler honours the canonical filter set and emits a
// :gap comment when the resume point has been evicted. spec: §25.5
// lines 2677-2685.
func (s *Service) HandleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	filter, err := parseFilter(r.URL.Query())
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, codeInvalidEventFilter, conventions.CategoryPermanent, err.Error())
		return
	}

	resumeKey := resumeEventKey(r)
	sub := s.subscribe(filter, 64)
	defer s.unsubscribe(sub)

	// Resolve the resume point. The subscription is already installed so
	// no concurrent publish is missed between the backlog scan and live
	// delivery.
	var since uint64
	gap := false
	if resumeKey != "" {
		if id, found := s.buffer.Lookup(resumeKey); found {
			since = id
		} else {
			gap = true
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// §25.5 degradation cases 1 and 4: announce the fall-back source on
	// the stream so the consumer learns it is receiving a degraded view —
	// the gateway buffer during a Redis outage, or only this replica's
	// lenny-ops-originated events during a dual Redis + gateway outage
	// (the local ring buffer holds no gateway-originated events). spec:
	// §25.5 lines 2768-2780.
	if _, deg, _ := s.streamState(); deg != nil {
		writeSSEDegradation(w, deg)
	}

	if gap {
		writeSSEGap(w, resumeKey)
		s.observeGap()
	}
	backlog := s.buffer.Query(since, filter, 0)
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

// resumeEventKey reads the §25.5 SSE resume position from the request,
// preferring the SSE-standard Last-Event-ID header (the CloudEvents id)
// and falling back to ?cursor= (the opaque poll cursor, whose encoded
// eventKey resolves to the same position). spec: §25.5 lines 2679-2680.
func resumeEventKey(r *http.Request) string {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		return v
	}
	if _, key, err := decodeCursor(r.URL.Query().Get("cursor")); err == nil {
		return key
	}
	return ""
}

// writeSSEFrame writes one BufferedEvent as an SSE record per §25.5.
// The id: line carries the CloudEvents id (eventKey) so a client
// reconnecting with Last-Event-ID resumes from the correct position.
func writeSSEFrame(w http.ResponseWriter, ev gwevents.BufferedEvent) {
	body, err := json.Marshal(ev.Event)
	if err != nil {
		return
	}
	if ev.Event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", ev.Event.ID)
	}
	if ev.Event.Type != "" {
		fmt.Fprintf(w, "event: %s\n", ev.Event.Type)
	}
	fmt.Fprintf(w, "data: %s\n\n", body)
}

// writeSSEGap writes the §25.5 :gap comment line when the SSE resume
// point has been evicted from the buffer, so the client knows to
// re-read platform state before assuming continuity. spec: §25.5 line
// 2684 (cursor transition safety, ":gap {...}\n").
func writeSSEGap(w http.ResponseWriter, resumeKey string) {
	payload, err := json.Marshal(map[string]any{
		"gapDetected":     true,
		"resumeKey":       resumeKey,
		"suggestedAction": "resync",
	})
	if err != nil {
		return
	}
	fmt.Fprintf(w, ":gap %s\n", payload)
}
