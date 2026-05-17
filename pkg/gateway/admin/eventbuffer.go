// SPDX-License-Identifier: MIT

package admin

import (
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
