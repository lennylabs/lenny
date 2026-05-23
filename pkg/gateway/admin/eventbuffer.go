// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
)

// WithEventBuffer wires the §25.3 GET /v1/admin/events/buffer endpoint
// onto the Router. Call before Handler() so the mux picks it up.
func (r *Router) WithEventBuffer(buf EventBufferQuerier) *Router {
	r.eventBuffer = buf
	return r
}

// WithEventEmitter wires the §25.3 operational-event emitter onto the
// Router so admin handlers record state-change events into the event
// buffer. It is the emit counterpart of WithEventBuffer's query side.
func (r *Router) WithEventEmitter(em opsevents.EventEmitter) *Router {
	r.eventEmitter = em
	return r
}

// emitOpsEvent records a §25.3 operational event when an emitter is
// wired. It is a no-op otherwise, so the admin handlers do not depend
// on the event buffer being configured. eventType is a §16.6 catalogue
// short name; its CloudEvents `type` is derived via CloudEventsType.
func (r *Router) emitOpsEvent(ctx context.Context, eventType opsevents.EventType, severity string, data map[string]any) {
	if r.eventEmitter == nil {
		return
	}
	var payload json.RawMessage
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			payload = b
		}
	}
	_, _ = r.eventEmitter.Emit(ctx, opsevents.OperationalEvent{
		Source:          "/v1/admin",
		Type:            eventType.CloudEventsType(),
		Severity:        severity,
		DataContentType: "application/json",
		Data:            payload,
	})
}

// handleEventBuffer implements GET /v1/admin/events/buffer — the §25.3
// in-memory operational-event buffer query. It supports cursor polling
// via ?since=, narrowing via ?eventType= and ?severity=, and ?limit=
// (default 100, max 500).
func (r *Router) handleEventBuffer(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()

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
	// §25.3: the buffer endpoint caps limit at 500.
	if limit > opsevents.DefaultBufferCapacity {
		limit = opsevents.DefaultBufferCapacity
	}
	page := r.eventBuffer.Query(since, opsevents.EventFilter{
		EventType: q.Get("eventType"),
		Severity:  q.Get("severity"),
	}, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}
