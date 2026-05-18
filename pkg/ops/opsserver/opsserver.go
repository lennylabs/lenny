// SPDX-License-Identifier: MIT

// Package opsserver implements the HTTP surface of the §25 lenny-ops
// operability service. lenny-ops is a separate Deployment from the
// gateway and hosts the operability endpoints that read durable state
// (Postgres, Redis, the Kubernetes API, Prometheus) rather than
// in-process gateway state.
//
// This package carries the service's routing, its own liveness and
// readiness probes, and the §25.6 connectivity diagnostic. The §25.6
// session and pool diagnostics and the §25.4 and later operability
// endpoints register here as they are built.
package opsserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// probeTimeout bounds each §25 dependency probe (§25.6: 2s timeouts).
const probeTimeout = 2 * time.Second

// SelfHealthReporter yields the §25.4 lenny-ops self-health report the
// GET /v1/admin/ops/health endpoint serves. pkg/ops/opsservice's
// SelfHealthMonitor satisfies it.
type SelfHealthReporter interface {
	// SelfHealth returns the current self-health report as a JSON object.
	SelfHealth() map[string]any
}

// LeaderReporter reports this replica's §25.4 leader-election state and
// the background loops it owns. pkg/ops/opsservice's Service satisfies
// it. The readiness endpoint surfaces this so an operator querying any
// replica can see which one is the leader.
type LeaderReporter interface {
	// IsLeader reports whether this replica holds the lenny-ops-leader
	// Lease.
	IsLeader() bool
	// LoopNames returns the names of the background loops.
	LoopNames() []string
	// LeaderLoopsRunning reports whether the leader-only loops are
	// active on this replica.
	LeaderLoopsRunning() bool
}

// Server is the lenny-ops HTTP handler. It routes the operability
// endpoints and the Kubernetes liveness and readiness probes.
type Server struct {
	mux        *http.ServeMux
	probes     map[string]probe.Func
	runbooks   RunbookSource
	selfHealth SelfHealthReporter
	leader     LeaderReporter
}

// Options configures a lenny-ops Server.
type Options struct {
	// Probes are the §25 dependency checks (Postgres, Redis, MinIO, the
	// Kubernetes API, the gateway) the readiness and connectivity
	// endpoints run. A nil map leaves the dependency report empty.
	Probes map[string]probe.Func
	// Runbooks is the §25.7 runbook index source. A nil source leaves
	// the runbook endpoint reporting the index unavailable.
	Runbooks RunbookSource
	// SelfHealth is the §25.4 self-health report source for GET
	// /v1/admin/ops/health. A nil source reports the self-health report
	// unavailable.
	SelfHealth SelfHealthReporter
	// Leader is the §25.4 leader-election state source. A nil source
	// omits the leader-election fields from the readiness report.
	Leader LeaderReporter
}

// New returns a Server with the liveness probe, readiness probe, the
// §25.6 connectivity diagnostic, the §25.7 runbook index, and the
// §25.4 self-health endpoint registered. Operability endpoints are
// added as they are built.
func New(opts Options) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		probes:     opts.Probes,
		runbooks:   opts.Runbooks,
		selfHealth: opts.SelfHealth,
		leader:     opts.Leader,
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /v1/admin/diagnostics/connectivity", s.handleConnectivity)
	s.mux.HandleFunc("GET /v1/admin/runbooks", s.handleListRunbooks)
	s.mux.HandleFunc("GET /v1/admin/runbooks/{name}/steps", s.handleRunbookSteps)
	s.mux.HandleFunc("GET /v1/admin/ops/health", s.handleOpsHealth)
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
	body := map[string]any{
		"status":       "ready",
		"dependencies": dependencyReport(results),
	}
	// §25.4: an operator querying any replica sees which one holds the
	// lenny-ops-leader Lease and runs the singleton background loops.
	if s.leader != nil {
		body["leader"] = s.leader.IsLeader()
		body["leaderLoopsRunning"] = s.leader.LeaderLoopsRunning()
		body["loops"] = s.leader.LoopNames()
	}
	writeJSON(w, http.StatusOK, body)
}

// handleOpsHealth serves the §25.4 GET /v1/admin/ops/health endpoint:
// the structured lenny-ops self-health report (Postgres pool, Redis
// consumer lag, webhook backlog, K8s API connectivity, memory
// pressure) the watchdog polls as a complement to the event stream.
func (s *Server) handleOpsHealth(w http.ResponseWriter, _ *http.Request) {
	if s.selfHealth == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "unknown",
			"detail": "self-health monitoring is not configured",
		})
		return
	}
	writeJSON(w, http.StatusOK, s.selfHealth.SelfHealth())
}

// handleConnectivity serves the §25.6 GET /v1/admin/diagnostics/-
// connectivity endpoint: it runs the dependency probes in parallel and
// returns the per-dependency report together with an overall verdict.
func (s *Server) handleConnectivity(w http.ResponseWriter, r *http.Request) {
	results := probe.Run(r.Context(), s.probes, probeTimeout)
	writeJSON(w, http.StatusOK, map[string]any{
		"dependencies": dependencyReport(results),
		"healthy":      probe.AllOK(results),
	})
}

// dependencyReport projects probe results into the JSON body shape the
// readiness and connectivity endpoints share.
func dependencyReport(results map[string]probe.Result) map[string]map[string]any {
	deps := make(map[string]map[string]any, len(results))
	for name, res := range results {
		entry := map[string]any{
			"ok":         res.OK,
			"durationMs": res.Duration.Milliseconds(),
		}
		if res.Detail != "" {
			entry["detail"] = res.Detail
		}
		deps[name] = entry
	}
	return deps
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
