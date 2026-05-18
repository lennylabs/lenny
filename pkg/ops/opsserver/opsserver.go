// SPDX-License-Identifier: MIT

// Package opsserver implements the HTTP surface of the §25 lenny-ops
// operability service. lenny-ops is a separate Deployment from the
// gateway and hosts the operability endpoints that read durable state
// (Postgres, Redis, the Kubernetes API, Prometheus) rather than
// in-process gateway state.
//
// This package carries the service's routing and its own liveness and
// readiness probes. The §25.4 and later operability endpoints —
// diagnostics, drift detection, backup and restore, platform
// lifecycle, the event stream — are registered here as they are built.
package opsserver

import (
	"encoding/json"
	"net/http"
)

// Server is the lenny-ops HTTP handler. It routes the operability
// endpoints and the Kubernetes liveness and readiness probes.
type Server struct {
	mux *http.ServeMux
}

// New returns a Server with the liveness and readiness probes
// registered. Operability endpoints are added as they are built.
func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	return s
}

// ServeHTTP routes a request to the registered operability handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleHealthz is the liveness probe: it reports that the process is
// running and able to serve. It does not check downstream
// dependencies, so a dependency outage does not cause Kubernetes to
// restart an otherwise-healthy lenny-ops pod.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is the readiness probe. lenny-ops degrades gracefully
// when a downstream dependency is transiently unavailable (§25), so the
// process is ready to serve once it is listening; per-endpoint
// dependency health is reported by the endpoints themselves.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
