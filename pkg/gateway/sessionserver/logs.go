// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleLogs implements GET /v1/sessions/{id}/logs per §15.1 line 673
// ("Get session logs (paginated, streamable via SSE)") and §24.17 line
// 220 (the target of `lenny session logs <sessionId> [--since <time>]`).
//
// Session logs are sourced from the durable session event store — the
// same §7.2 stream the /events endpoint exposes — so the operational
// record a session produced is reachable through the spec-named logs
// surface. The endpoint is content-negotiated exactly like /events:
// `Accept: application/json` returns the §15.1 line 1228 paginated
// `{items, cursor, hasMore}` envelope; any other Accept returns the
// Server-Sent Events tail. `?since=<RFC3339>` filters the JSON envelope
// to entries at or after the timestamp (the §24.17 `--since` flag).
//
// 404 RESOURCE_NOT_FOUND when the session does not exist or belongs to
// another tenant, matching the §15.1 line 661 terminal-session contract
// that /logs returns 404 once no record exists.
//
// spec: §15.1 line 673; §24.17 line 220; §15.1 line 1228 (pagination).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
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

	if wantsJSONEvents(r) {
		s.serveLogsJSON(w, r, tenantID, id)
		return
	}
	s.streamLogsSSE(w, r, tenantID, id)
}

// serveLogsJSON renders the §15.1 line 1228 canonical envelope
// `{items, cursor, hasMore}` over the retained session event log. It
// mirrors serveEventsJSON and adds the §24.17 `?since=<RFC3339>` filter:
// only entries whose timestamp is at or after `since` appear, applied
// before pagination so the cursor advances over the filtered view.
func (s *Server) serveLogsJSON(w http.ResponseWriter, r *http.Request, tenantID, id string) {
	params, ferr := pagination.ParseRequest(r,
		[]string{eventSortField}, eventsDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}

	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"since must be an RFC3339 timestamp",
				map[string]any{"fields": []map[string]any{{"field": "since", "rule": "invalid_timestamp"}}})
			return
		}
		since = t
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
	// §7.2 tenant-binding check, the same isolation guard serveEventsJSON
	// relies on.
	sub, err := s.events.SubscribeForTenant(tenantID, id, afterSeq, 1)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
		return
	}
	hist := sub.Backlog
	sub.Close()

	envelope := pagination.Envelope[eventEnvelopeItem]{Items: []eventEnvelopeItem{}}
	for _, ev := range hist {
		if !since.IsZero() && ev.Timestamp.Before(since) {
			continue
		}
		if len(envelope.Items) == params.Limit {
			// One filtered entry beyond the limit confirms a further page
			// without emitting it.
			envelope.HasMore = true
			break
		}
		envelope.Items = append(envelope.Items, eventEnvelopeItem{
			Seq:       ev.Seq,
			SessionID: ev.SessionID,
			Type:      ev.Type,
			Data:      json.RawMessage(ev.Data),
			Timestamp: ev.Timestamp.UTC().Format(time.RFC3339Nano),
		})
	}
	if envelope.HasMore && len(envelope.Items) > 0 {
		last := envelope.Items[len(envelope.Items)-1]
		seqStr := strconv.FormatUint(last.Seq, 10)
		envelope.Cursor = pagination.MintCursor(params.Sort, seqStr, seqStr, s.clock())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

// streamLogsSSE serves the §15.1 line 673 Server-Sent Events tail of the
// session log. It replays the retained backlog from the reconnect cursor
// (Last-Event-ID / ?afterSeq=) and then switches to live delivery,
// reusing writeSSEEvent and the resumeCursor / gap-marker helpers the
// /events stream uses. The §10.4 coordinator-handoff reattach synthesis
// is specific to the interactive /events surface and is intentionally
// absent here: the logs tail is a diagnostic record, not the session's
// control stream.
func (s *Server) streamLogsSSE(w http.ResponseWriter, r *http.Request, tenantID, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"response writer does not support streaming", nil)
		return
	}

	afterSeq := resumeCursor(r)
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

	if afterSeq > 0 {
		if oldestSeq, ok := s.events.OldestRetainedSeq(id); ok && oldestSeq > afterSeq+1 {
			writeGapMarkers(w, afterSeq, oldestSeq, s.clock())
		}
	}
	for _, ev := range sub.Backlog {
		writeSSEEvent(w, ev)
	}
	flusher.Flush()

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
