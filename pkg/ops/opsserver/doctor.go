// SPDX-License-Identifier: MIT

package opsserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// doctorRunRequest is the §25.6 POST /v1/admin/diagnostics/run body: the
// optional set of finding codes to act on. An empty/absent list means
// "every detected fixable finding".
type doctorRunRequest struct {
	Findings []string `json:"findings"`
}

// registerDoctorRoute wires the §25.6 auto-remediation endpoint. It is
// registered unconditionally; the handler reports 503 when no
// orchestrator is configured so an operator gets a clear signal rather
// than a 404. spec: §25.6 lines 2941-2982 — F-25.6.2, F-24.2.3.
func (s *Server) registerDoctorRoute() {
	s.mux.HandleFunc("POST /v1/admin/diagnostics/run", s.handleDiagnosticsRun)
}

// handleDiagnosticsRun serves POST /v1/admin/diagnostics/run. Without
// ?fix=true it returns a read-only remediation report of the detected
// fixable findings; with ?fix=true it applies the §25.6 idempotent
// remediations and returns the §25.2 long-running operation envelope.
func (s *Server) handleDiagnosticsRun(w http.ResponseWriter, r *http.Request) {
	if s.doctor == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, "DOCTOR_UNAVAILABLE",
			conventions.CategoryTransient, "the auto-remediation subsystem is not configured")
		return
	}
	var body doctorRunRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				conventions.CategoryPermanent, "request body is not valid JSON: "+err.Error())
			return
		}
	}
	fix := r.URL.Query().Get("fix") == "true"

	principal := ""
	if p, ok := callerPrincipal(r); ok {
		principal = p.Subject
	}

	report, err := s.doctor.Run(r.Context(), doctor.RunRequest{
		Findings:  body.Findings,
		Fix:       fix,
		Principal: principal,
	})
	if err != nil {
		// A detection failure means the diagnostic data sources (Kubernetes
		// API) were unreachable; report it transiently so an agent retries
		// rather than treating the cluster as healthy.
		conventions.WriteError(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE",
			conventions.CategoryTransient, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}
