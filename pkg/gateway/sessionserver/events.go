// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// eventSortField is the sort key for the JSON list view of the events
// endpoint. Events are written in per-session monotonic `seq` order,
// the only meaningful ordering. spec: §15.1 line 1228 ("events" listed
// alongside the paginated endpoints).
const eventSortField = "seq"

// eventsDefaultSort sorts events in ascending `seq` order so the JSON
// list view reads chronologically, matching the SSE replay order.
var eventsDefaultSort = pagination.Sort{Field: eventSortField, Direction: pagination.DirectionAsc}

// eventEnvelopeItem is the JSON shape of an event in the §15.1 line
// 1228 canonical envelope. The `data` is already-marshalled JSON, kept
// as a RawMessage so the wire shape matches the SSE `data:` payload
// byte-for-byte instead of double-encoding.
type eventEnvelopeItem struct {
	Seq       uint64          `json:"seq"`
	SessionID string          `json:"sessionId"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	Timestamp string          `json:"timestamp"`
}

// wantsJSONEvents returns true when the caller asked for a JSON list
// envelope (per §15.1 line 1228) rather than the SSE stream. The SSE
// stream remains the default — only an explicit `Accept: application/
// json` (and no `text/event-stream`) routes to the JSON path so
// existing SSE clients are unaffected.
func wantsJSONEvents(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if accept == "" {
		return false
	}
	if strings.Contains(accept, "text/event-stream") {
		return false
	}
	return strings.Contains(accept, "application/json")
}

// handleEvents implements GET /v1/sessions/{id}/events per §15.1.
//
// The endpoint is content-negotiated: `Accept: text/event-stream`
// (the default for browsers and `curl -N`) returns the Server-Sent
// Events stream of session activity. `Accept: application/json`
// returns the §15.1 line 1228 canonical list envelope
// `{items, cursor, hasMore}` over the retained replay buffer so a
// polling client that cannot speak SSE still reaches the events
// without sidecar parsing. The SSE branch carries Last-Event-ID
// resume semantics; the JSON branch carries the canonical cursor.
// F-15.1.23.
//
// The client may resume after a disconnect by passing the
// Last-Event-ID header (or ?afterSeq=) with the last sequence it
// saw; the gateway replays the retained backlog before switching to
// live delivery (the §15.1 streaming-reconnect-with-cursor
// contract).
//
// Each SSE frame is:
//
//	id: <seq>
//	event: <type>
//	data: <json>
//
// 404 RESOURCE_NOT_FOUND when the session does not exist or belongs
// to another tenant.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.events == nil {
		s.writeError(w, http.StatusServiceUnavailable, "EVENT_STREAM_UNAVAILABLE",
			"gateway has no event bus configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	if _, err := s.store.Get(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// JSON list view — §15.1 line 1228 canonical envelope over the
	// retained backlog; no SSE keep-alive, no live tail.
	if wantsJSONEvents(r) {
		s.serveEventsJSON(w, r, tenantID, id)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"response writer does not support streaming", nil)
		return
	}

	afterSeq := resumeCursor(r)
	// §7.2 defense-in-depth: the bus enforces the tenant binding even
	// if a future caller drops the store.Get precheck above.
	sub, err := s.events.SubscribeForTenant(tenantID, id, afterSeq, 64)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// spec: §7.2 line 143 (gap_detected) / lines 349-361
	// (checkpoint_boundary). When the requested cursor falls below the
	// oldest retained sequence, the client missed evicted events;
	// surface both spec markers ahead of the backlog so the client can
	// render a data-loss warning. Cursor=0 is the fresh-connect path and
	// is never a gap.
	if afterSeq > 0 {
		if oldestSeq, ok := s.events.OldestRetainedSeq(id); ok && oldestSeq > afterSeq+1 {
			writeGapMarkers(w, afterSeq, oldestSeq, s.clock())
		}
	}

	// Replay the backlog (events the client missed while
	// disconnected) before switching to live delivery.
	for _, ev := range sub.Backlog {
		writeSSEEvent(w, ev)
	}
	flusher.Flush()

	// Live delivery until the client disconnects.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.Events():
			if !open {
				return
			}
			writeSSEEvent(w, ev)
			flusher.Flush()
		}
	}
}

// resumeCursor extracts the §15.1 reconnect cursor from the request,
// preferring the SSE-standard Last-Event-ID header and falling back
// to the ?afterSeq= query parameter.
func resumeCursor(r *http.Request) uint64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// writeSSEEvent writes one event as an SSE frame.
func writeSSEEvent(w http.ResponseWriter, ev sessionevents.Event) {
	fmt.Fprintf(w, "id: %d\n", ev.Seq)
	fmt.Fprintf(w, "event: %s\n", ev.Type)
	fmt.Fprintf(w, "data: %s\n\n", ev.Data)
}

// writeGapMarkers emits the §7.2 line 143 `gap_detected` frame and the
// §7.2 lines 349-361 `checkpoint_boundary` frame on the SSE stream
// when the requested cursor sits below the oldest retained sequence
// (events between the cursor and the buffer head were evicted).
//
// Neither frame is a SessionEvent: per §7.2 line 143 the markers carry
// no SeqNum, so no `id:` line is written. The two frames carry
// different information by design:
//
//   - gap_detected: the bare protocol-level marker
//     `{"lastSeenSeq": N, "nextSeq": M}` so a client can render a gap
//     warning without parsing the structured payload.
//   - checkpoint_boundary: the structured marker
//     `{type, cursor, events_lost, reason, checkpoint_timestamp}` that
//     §7.2 lines 349-361 require so the client can surface the precise
//     data-loss count to its user.
//
// The Bus currently implements count-based eviction only, so `reason`
// is always `replay_window_exceeded`; the `event_store_unavailable`
// branch (§7.2 line 361) lands when the durable EventStore is wired.
func writeGapMarkers(w http.ResponseWriter, afterSeq, oldestSeq uint64, now time.Time) {
	if oldestSeq <= afterSeq+1 {
		return
	}
	eventsLost := oldestSeq - afterSeq - 1
	gap := struct {
		LastSeenSeq uint64 `json:"lastSeenSeq"`
		NextSeq     uint64 `json:"nextSeq"`
	}{LastSeenSeq: afterSeq, NextSeq: oldestSeq}
	gapBytes, err := json.Marshal(gap)
	if err != nil {
		gapBytes = []byte("{}")
	}
	fmt.Fprintf(w, "event: gap_detected\n")
	fmt.Fprintf(w, "data: %s\n\n", gapBytes)

	cb := struct {
		Type                string `json:"type"`
		Cursor              uint64 `json:"cursor"`
		EventsLost          uint64 `json:"events_lost"`
		Reason              string `json:"reason"`
		CheckpointTimestamp string `json:"checkpoint_timestamp"`
	}{
		Type:                "checkpoint_boundary",
		Cursor:              oldestSeq,
		EventsLost:          eventsLost,
		Reason:              "replay_window_exceeded",
		CheckpointTimestamp: now.UTC().Format(time.RFC3339Nano),
	}
	cbBytes, err := json.Marshal(cb)
	if err != nil {
		cbBytes = []byte("{}")
	}
	fmt.Fprintf(w, "event: checkpoint_boundary\n")
	fmt.Fprintf(w, "data: %s\n\n", cbBytes)
}

// publishEvent is the gateway-side helper that publishes a session
// event when the event bus is wired. eventData is marshalled to
// JSON; a marshalling failure publishes an empty object. The tenant id
// is recorded on the bus so SubscribeForTenant enforces the §7.2
// tenant-isolation predicate. A non-empty tenant id is the production
// contract; tests may pass "" for the legacy untenanted code path.
func (s *Server) publishEvent(tenantID, sessionID, eventType string, payload any) {
	if s.events == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	if tenantID == "" {
		s.events.Publish(sessionID, eventType, string(data), s.clock())
		return
	}
	s.events.PublishForTenant(tenantID, sessionID, eventType, string(data), s.clock())
}

// recordTTFTOnce observes the §6.3 line 356 / §16.1 line 15 TTFT
// histogram the first time row receives an agent-originated streaming
// event (the gateway's `response` event type). T0 is the session's
// `CreatedAt` (the POST /v1/sessions admission instant per §15.1).
// Subsequent events for the same session are no-ops so the histogram
// only counts each session once. spec: §6.3 line 356, §16.1 line 15.
func (s *Server) recordTTFTOnce(row sessionstore.Session, eventType string) {
	if s.observeTimeToFirstToken == nil {
		return
	}
	if eventType != "response" {
		return
	}
	if _, loaded := s.firstTokenObserved.LoadOrStore(row.ID, struct{}{}); loaded {
		return
	}
	runtimeClass, ok := isolation.RuntimeClassName(row.IsolationProfile)
	if !ok {
		// A row with no resolved isolation profile would mislabel the
		// histogram; skip rather than emit an empty runtime_class.
		return
	}
	seconds := s.clock().Sub(row.CreatedAt).Seconds()
	if seconds < 0 {
		seconds = 0
	}
	s.observeTimeToFirstToken(row.PoolRef, runtimeClass, string(row.IsolationProfile), seconds)
}

// serveEventsJSON renders the §15.1 line 1228 canonical envelope
// `{items, cursor, hasMore}` over the in-memory replay buffer. The
// JSON path is the cursor-paginated alternative to the SSE stream;
// `?cursor=` (canonical) or `?afterSeq=` (legacy) advance the
// pagination, `?limit=` clamps to [1, 200], and the envelope is the
// same shape as every other §15.1 list endpoint. F-15.1.23.
func (s *Server) serveEventsJSON(w http.ResponseWriter, r *http.Request, tenantID, id string) {
	params, ferr := pagination.ParseRequest(r,
		[]string{eventSortField}, eventsDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	afterSeq := uint64(0)
	if params.Cursor.Tiebreak != "" {
		if n, err := strconv.ParseUint(params.Cursor.Tiebreak, 10, 64); err == nil {
			afterSeq = n
		}
	} else if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}

	// SubscribeForTenant + Close reads the retained backlog under the
	// same §7.2 tenant-binding check the SSE path uses, so a foreign
	// session cannot reach the JSON envelope even if the per-handler
	// store.Get pre-check is bypassed by a future refactor.
	sub, err := s.events.SubscribeForTenant(tenantID, id, afterSeq, 1)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
		return
	}
	hist := sub.Backlog
	sub.Close()

	envelope := pagination.Envelope[eventEnvelopeItem]{Items: []eventEnvelopeItem{}}

	hasMore := false
	if len(hist) > params.Limit {
		hist = hist[:params.Limit]
		hasMore = true
	}
	for _, ev := range hist {
		envelope.Items = append(envelope.Items, eventEnvelopeItem{
			Seq:       ev.Seq,
			SessionID: ev.SessionID,
			Type:      ev.Type,
			Data:      json.RawMessage(ev.Data),
			Timestamp: ev.Timestamp.UTC().Format(time.RFC3339Nano),
		})
	}
	envelope.HasMore = hasMore
	if hasMore && len(envelope.Items) > 0 {
		last := envelope.Items[len(envelope.Items)-1]
		seqStr := strconv.FormatUint(last.Seq, 10)
		envelope.Cursor = pagination.MintCursor(params.Sort, seqStr, seqStr, s.clock())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}
