// SPDX-License-Identifier: MIT

// Package opsserver implements the HTTP surface of the §25 lenny-ops
// operability service. lenny-ops is a separate Deployment from the
// gateway and hosts the operability endpoints that read durable state
// (Postgres, Redis, the Kubernetes API, Prometheus) rather than
// in-process gateway state.
//
// This package carries the service's routing, its own liveness and
// readiness probes, and the dependency-connectivity reporting. The
// §25.4 and later operability endpoints — diagnostics, drift
// detection, backup and restore, platform lifecycle, the event stream
// — are registered here as they are built.
package opsserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// probeTimeout bounds each §25 dependency probe the readiness endpoint
// runs.
const probeTimeout = 2 * time.Second

// Server is the lenny-ops HTTP handler. It routes the operability
// endpoints and the Kubernetes liveness and readiness probes.
type Server struct {
	mux    *http.ServeMux
	probes map[string]probe.Func
}

// New returns a Server with the liveness and readiness probes
// registered. probes are the §25 dependency checks (Postgres, Redis,
// MinIO, the Kubernetes API, the gateway) the readiness endpoint runs;
// a nil or empty map leaves the dependency report empty. Operability
// endpoints are added as they are built.
func New(probes map[string]probe.Func) *Server {
	s := &Server{mux: http.NewServeMux(), probes: probes}
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

// handleReadyz is the readiness probe. §25 has lenny-ops degrade
// gracefully when a downstream dependency is transiently unavailable,
// so the process reports ready (HTTP 200) as long as it is serving;
// the per-dependency probe results are reported in the body so an
// operator or agent reading the endpoint sees which dependency is
// down.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	results := probe.Run(r.Context(), s.probes, probeTimeout)
	deps := make(map[string]map[string]any, len(results))
	for name, res := range results {
		entry := map[string]any{"ok": res.OK}
		if res.Detail != "" {
			entry["detail"] = res.Detail
		}
		deps[name] = entry
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ready",
		"dependencies": deps,
	})
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
