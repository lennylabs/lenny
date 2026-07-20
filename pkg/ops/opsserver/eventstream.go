// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"net/http"

	opsevents "github.com/lennylabs/lenny/pkg/ops/events"
)

// registerEventStreamRoutes wires the §25.5 GET /v1/admin/events/stream
// (SSE) and GET /v1/admin/events (polling) endpoints onto the
// lenny-ops mux. The Service backs both routes; when it is nil the
// routes are left unmapped so the mux returns 404. Each route resolves the
// caller's tenant scope at this boundary so the read handlers apply the same
// tenant filter as delivery. spec: §25.5.
func (s *Server) registerEventStreamRoutes() {
	if s.eventStream == nil {
		return
	}
	s.mux.HandleFunc("GET /v1/admin/events/stream", s.handleEventStream)
	s.mux.HandleFunc("GET /v1/admin/events", s.handleEventPoll)
}

// handleEventStream resolves the §25.5 read-caller tenant scope from the
// authenticated principal and threads it onto the request context before the
// SSE handler serves, so a tenant-admin observes only its own tenant's events
// and a platform-scoped (no-label) event is dropped for it. The caller is
// resolved with the same subscriptionCaller used by the event-subscription
// endpoints, so the read and write surfaces agree on identity and tenant. The
// filter is a silent drop; SUBSCRIPTION_TENANT_FORBIDDEN stays create-only.
// spec: §25.5 (SSE applies the same tenant filter as delivery).
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	s.eventStream.HandleStream(w, r.WithContext(readerScopeContext(r)))
}

// handleEventPoll threads the resolved §25.5 read-caller tenant scope onto the
// request context before the polling handler serves, so the poll page is
// intersected with the caller's tenant the same way delivery is. spec: §25.5
// (polling applies the same tenant filter as delivery).
func (s *Server) handleEventPoll(w http.ResponseWriter, r *http.Request) {
	s.eventStream.HandlePoll(w, r.WithContext(readerScopeContext(r)))
}

// readerScopeContext resolves the §25.5 read caller and returns a context
// carrying its tenant scope. A platform-admin reads every event; a tenant-admin
// reads only events labeled with its own tenant. spec: §25.5 (read-endpoint
// tenant filter, platform-scoped events reach only platform-admin callers).
func readerScopeContext(r *http.Request) context.Context {
	c := subscriptionCaller(r)
	return opsevents.WithReaderScope(r.Context(), c.TenantID, c.PlatformAdmin)
}
