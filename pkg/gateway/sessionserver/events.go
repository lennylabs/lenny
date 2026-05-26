// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// handleEvents implements GET /v1/sessions/{id}/events per §15.1 —
// the Server-Sent Events stream of session activity.
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
