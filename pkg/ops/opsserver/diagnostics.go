// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

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
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	diag, err := s.diagnostics.DiagnoseSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diag)
}

// handleDiagnosePool serves GET /v1/admin/diagnostics/pools/{name}: the
// §25.6 warm-pool bottleneck analysis.
func (s *Server) handleDiagnosePool(w http.ResponseWriter, r *http.Request) {
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	diag, err := s.diagnostics.DiagnosePool(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diag)
}

// handleDiagnoseCredentialPool serves GET /v1/admin/diagnostics/-
// credential-pools/{name}: the §25.6 credential-pool health diagnosis.
func (s *Server) handleDiagnoseCredentialPool(w http.ResponseWriter, r *http.Request) {
	if s.diagnostics == nil {
		s.diagnosticsUnavailable(w)
		return
	}
	diag, err := s.diagnostics.DiagnoseCredentialPool(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDiagnosticsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diag)
}
