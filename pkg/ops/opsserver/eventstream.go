// SPDX-License-Identifier: MIT

package opsserver

// registerEventStreamRoutes wires the §25.5 GET /v1/admin/events/stream
// (SSE) and GET /v1/admin/events (polling) endpoints onto the
// lenny-ops mux. The Service backs both routes; when it is nil the
// routes are left unmapped so the mux returns 404.
func (s *Server) registerEventStreamRoutes() {
	if s.eventStream == nil {
		return
	}
	s.mux.HandleFunc("GET /v1/admin/events/stream", s.eventStream.HandleStream)
	s.mux.HandleFunc("GET /v1/admin/events", s.eventStream.HandlePoll)
}
