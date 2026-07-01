// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// eventSortField is the sort key for the JSON list view of the events
// endpoint. Events are written in per-session monotonic `seq` order,
// the only meaningful ordering. spec: §15.1 "Cursor-based pagination"
// (the {items, cursor, hasMore} list envelope every paginated endpoint
// returns; the events endpoint serves that envelope under
// `Accept: application/json`).
const eventSortField = "seq"

// eventsDefaultSort sorts events in ascending `seq` order so the JSON
// list view reads chronologically, matching the SSE replay order.
var eventsDefaultSort = pagination.Sort{Field: eventSortField, Direction: pagination.DirectionAsc}

// eventEnvelopeItem is the JSON form of an event item inside the §15.1
// "Cursor-based pagination" list envelope the events endpoint returns
// under `Accept: application/json`. The `data` is already-marshalled
// JSON, kept as a RawMessage so the serialized field matches the SSE
// `data:` payload byte-for-byte instead of double-encoding.
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
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
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

	// spec: §10.4 lines 391-397 — coordinator-handoff reattach synthesis.
	// When this reconnect spans a coordinator handoff (the client resumes
	// from a cursor it saw under a prior coordinator, the session has
	// durably produced events, but this coordinator has no local replay
	// history and the available backlog does not continue contiguously
	// from the cursor) the new coordinator MUST synthesize one
	// `session.resumed`, an optional `status_change`, and a
	// `children_reattached` frame *before* any gap_detected marker so the
	// client re-establishes session state (the §7.2 STR-007 single-
	// coordinator / post-handoff reconnect symmetry). F-7.2.13, F-10.4.2.
	if s.isCoordinatorHandoffReattach(row, afterSeq, sub.Backlog) {
		s.synthesizeHandoffReattach(r.Context(), w, row, afterSeq)
	}

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

// writeSyntheticSSE writes a synthesized SSE frame with no `id:` line.
// The §10.4 handoff-reattach frames are re-projections of session state
// rather than new SessionEvents: emitting them with the session's next
// monotonic Seq would either advance the durable last_seq counter (a
// rewind risk) or duplicate a seq the client already holds. Writing them
// without an `id:` keeps the client's reconnect cursor anchored at its
// last real event, mirroring how the gap_detected / checkpoint_boundary
// markers are framed. spec: §10.4 lines 391-397; §7.2 line 143.
func writeSyntheticSSE(w http.ResponseWriter, eventType string, data []byte) {
	if len(data) == 0 {
		data = []byte("{}")
	}
	fmt.Fprintf(w, "event: %s\n", eventType)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// isCoordinatorHandoffReattach reports whether this SSE reconnect spans a
// §10.4 coordinator handoff and therefore requires synthesized reattach
// frames. The signal combines three facts:
//
//   - The client is resuming (afterSeq > 0), so it saw events under a
//     prior coordinator.
//   - The session has durably produced events (row.LastSeq > 0), so the
//     prior coordinator's stream is authoritative — this rules out a
//     bogus cursor on a freshly-created session.
//   - This coordinator has no in-memory replay history for the session,
//     so it never published these events: it is the new coordinator
//     taking over (a process restart in single-replica mode, or a
//     subscriber replica that is not the publishing coordinator in the
//     §4.4 Redis-relay topology).
//
// The final guard skips synthesis when the available backlog already
// continues contiguously from the client's cursor (backlog[0].Seq ==
// afterSeq+1). In the Redis-relay topology a non-coordinator replica can
// serve the full relayed backlog, so the client lost nothing and the
// reconnect needs no recovery frames; emitting them would be spurious.
// spec: §10.4 lines 391-397. F-7.2.13, F-10.4.2.
func (s *Server) isCoordinatorHandoffReattach(row sessionstore.Session, afterSeq uint64, backlog []sessionevents.Event) bool {
	if s.events == nil || afterSeq == 0 || row.LastSeq <= 0 {
		return false
	}
	if _, hasLocal := s.events.OldestRetainedSeq(row.ID); hasLocal {
		return false
	}
	if len(backlog) > 0 && backlog[0].Seq == afterSeq+1 {
		return false
	}
	return true
}

// synthesizeHandoffReattach writes the §10.4 lines 391-397 reattach
// frames directly to the reconnecting client's SSE stream. The frames
// are synthesized for this connection only (they are not republished to
// the bus, so other subscribers and the durable log are untouched):
//
//   - session.resumed: always, carrying resumeMode "coordinator_handoff"
//     and workspaceLost: false. A handoff re-attaches the live pod, so
//     the workspace is intact and workspaceRecoveryFraction is 1.0 (the
//     partial-recovery fraction sourced from a durable checkpoint-meta
//     record applies to the partial_workspace resume mode, a distinct
//     path).
//   - status_change: carrying the session's current authoritative state.
//     §10.4 line 393 makes this optional "if the current state differs
//     from the client's last-known state". The standard SSE reconnect
//     transmits only a cursor (Last-Event-ID), never the client's
//     last-known state, so the gateway cannot compute the difference and
//     emits the frame unconditionally; a client whose view already
//     matches no-ops on it.
//   - children_reattached: only when the session is a parent with
//     archived children whose CompletionSeq exceeds the client's cursor
//     (completions missed during the handoff) or with still-active
//     children to re-await.
//
// Best-effort: a nil event bus or store error simply elides the frames.
// spec: §10.4 lines 391-397; §7.2 line 138 (session.resumed), line 137
// (status_change), line 153 (children_reattached). F-7.2.13, F-10.4.2.
func (s *Server) synthesizeHandoffReattach(ctx context.Context, w http.ResponseWriter, row sessionstore.Session, afterSeq uint64) {
	if frame, ok := s.handoffResumedFrame(); ok {
		writeSyntheticSSE(w, "session.resumed", frame)
	}
	stateFrame, _ := json.Marshal(map[string]any{"state": string(row.State)})
	writeSyntheticSSE(w, "status_change", stateFrame)
	if children, ok := s.buildHandoffChildrenReattached(ctx, row.TenantID, row.ID, afterSeq); ok {
		writeSyntheticSSE(w, "children_reattached", children)
	}
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
	// spec: §6.2 lines 273-300 — the agent_output / tool_use events the
	// adapter surfaces (published here as `response`, `response_degraded`,
	// and `tool_use*`) are qualifying activity that resets the §11.3
	// `maxIdleTime` clock so a streaming session is not reaped as idle.
	// Inbound, lifecycle, and warning events (status_change,
	// message_delivered, workspace_plan_warning, session.resumed,
	// session_complete) are not agent activity and do not stamp. F-11.3.7.
	if s.activityStamper != nil && isAgentActivityEvent(eventType) {
		s.activityStamper.Stamp(tenantID, sessionID)
	}
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

// isAgentActivityEvent reports whether a published session event type is a
// §6.2 lines 273-274 qualifying agent activity (agent_output or tool_use
// from the adapter). The gateway publishes agent output as `response` /
// `response_degraded` and tool calls under the `tool_use` prefix. F-11.3.7.
func isAgentActivityEvent(eventType string) bool {
	switch eventType {
	case "response", "response_degraded", "agent_output":
		return true
	}
	return strings.HasPrefix(eventType, "tool_use")
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
