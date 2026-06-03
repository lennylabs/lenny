// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// diagnosticsErrorMap maps each §25.6 canonical error code to its
// documented HTTP status and §25.2 category.
var diagnosticsErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	diagnostics.ErrCodeSessionNotFound:        {http.StatusNotFound, conventions.CategoryPermanent},
	diagnostics.ErrCodePoolNotFound:           {http.StatusNotFound, conventions.CategoryPermanent},
	diagnostics.ErrCodeCredentialPoolNotFound: {http.StatusNotFound, conventions.CategoryPermanent},
	diagnostics.ErrCodeDiagnosticsPartial:     {http.StatusMultiStatus, conventions.CategoryTransient},
}

// writeDiagnosticsError maps a §25.6 DiagnosticService error to the
// §25.2 canonical error envelope and writes it.
func writeDiagnosticsError(w http.ResponseWriter, err error) {
	code := diagnostics.CodeOf(err)
	if mapping, ok := diagnosticsErrorMap[code]; ok {
		conventions.WriteError(w, mapping.status, code, mapping.category, err.Error())
		return
	}
	conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
		conventions.CategoryTransient, err.Error())
}

// registerDiagnosticsRoutes wires the §25.6 diagnostic endpoints onto
// the Server's mux. The connectivity endpoint is registered separately
// in New because it has a probe-backed fallback when no DiagnosticService
// is configured.
func (s *Server) registerDiagnosticsRoutes() {
	s.mux.HandleFunc("GET /v1/admin/diagnostics/sessions/{id}", s.handleDiagnoseSession)
	s.mux.HandleFunc("GET /v1/admin/diagnostics/pools/{name}", s.handleDiagnosePool)
	s.mux.HandleFunc("GET /v1/admin/diagnostics/credential-pools/{name}", s.handleDiagnoseCredentialPool)
}

// writeDiagnosis writes a §25.6 diagnosis with HTTP 207 Multi-Status
// when the diagnosis carries a degradation envelope (the data source
// served it from a fallback or could not enrich every field), and 200
// OK otherwise. The 207 status mirrors the DIAGNOSTICS_PARTIAL → 207
// mapping the error path already uses, so a degraded-but-successful
// diagnosis and a partial-failure error reach the caller with the same
// status. spec: §25.6 lines 2908-2920 (partial results). F-25.6.1.
func writeDiagnosis(w http.ResponseWriter, diag any, degraded *conventions.Degradation) {
	status := http.StatusOK
	if degraded != nil {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, diag)
}

// diagnosticsUnavailable reports the §25.6 diagnostic surface as
// unconfigured — a deployment without a Postgres or Kubernetes
// connection to read pod and session state.
func (s *Server) diagnosticsUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "DIAGNOSTICS_UNAVAILABLE",
		conventions.CategoryTransient, "the diagnostic subsystem is not configured")
}

// handleDiagnoseSession serves GET /v1/admin/diagnostics/sessions/{id}:
// the §25.6 structured cause chain for a failed session.
func (s *Server) handleDiagnoseSession(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { diagnostics.ObserveRequestDuration("session", time.Since(start)) }()
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	id := r.PathValue("id")
	diag, err := s.diagnostics.DiagnoseSession(r.Context(), id)
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	// spec: §25.9 line 3699 — record the diagnostic access, coalesced
	// per session within a 60s window. F-25.9.15.
	s.recordDiagnosticAudit(r, eventSessionDiagnosed, "session", id)
	writeDiagnosis(w, diag, diag.Degradation)
}

// handleDiagnosePool serves GET /v1/admin/diagnostics/pools/{name}: the
// §25.6 warm-pool bottleneck analysis.
func (s *Server) handleDiagnosePool(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { diagnostics.ObserveRequestDuration("pool", time.Since(start)) }()
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	name := r.PathValue("name")
	diag, err := s.diagnostics.DiagnosePool(r.Context(), name)
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	// spec: §25.9 line 3699 — coalesced per pool within a 60s window.
	s.recordDiagnosticAudit(r, eventPoolDiagnosed, "pool", name)
	writeDiagnosis(w, diag, diag.Degradation)
}

// handleDiagnoseCredentialPool serves GET /v1/admin/diagnostics/-
// credential-pools/{name}: the §25.6 credential-pool health diagnosis.
func (s *Server) handleDiagnoseCredentialPool(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { diagnostics.ObserveRequestDuration("credential-pool", time.Since(start)) }()
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	name := r.PathValue("name")
	diag, err := s.diagnostics.DiagnoseCredentialPool(r.Context(), name)
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	// spec: §25.9 line 3699 — coalesced per credential pool within a 60s window.
	s.recordDiagnosticAudit(r, eventCredentialPoolDiagnosed, "credential-pool", name)
	writeDiagnosis(w, diag, diag.Degradation)
}
