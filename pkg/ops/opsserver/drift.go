// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
)

// driftErrorMap maps each §25.10 canonical error code to its documented
// HTTP status and §25.2 category.
var driftErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	driftservice.ErrCodeDesiredStateMissing: {http.StatusServiceUnavailable, conventions.CategoryTransient},
	driftservice.ErrCodeNoTargetSnapshot:    {http.StatusNotFound, conventions.CategoryPermanent},
	driftservice.ErrCodeReconcilePartial:    {http.StatusMultiStatus, conventions.CategoryTransient},
	driftservice.ErrCodeInvalid:             {http.StatusBadRequest, conventions.CategoryPermanent},
}

// writeDriftError maps a §25.10 drift service error to the §25.2
// canonical error envelope and writes it.
func writeDriftError(w http.ResponseWriter, err error) {
	code := driftservice.CodeOf(err)
	if mapping, ok := driftErrorMap[code]; ok {
		conventions.WriteError(w, mapping.status, code, mapping.category, err.Error())
		return
	}
	conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
		conventions.CategoryTransient, err.Error())
}

// registerDriftRoutes wires the §25.10 configuration-drift endpoints
// onto the Server's mux.
func (s *Server) registerDriftRoutes() {
	s.mux.HandleFunc("GET /v1/admin/drift", s.handleDriftReport)
	s.mux.HandleFunc("POST /v1/admin/drift/validate", s.handleDriftValidate)
	s.mux.HandleFunc("POST /v1/admin/drift/snapshot/refresh", s.handleDriftSnapshotRefresh)
}

// driftUnavailable reports the §25.10 drift surface as unconfigured.
func (s *Server) driftUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "DRIFT_SERVICE_UNAVAILABLE",
		conventions.CategoryTransient, "the configuration-drift subsystem is not configured")
}

// handleDriftReport serves GET /v1/admin/drift: the §25.10 drift report
// comparing running state against the desired-state snapshot. It
// accepts ?scope=, ?against=, and ?fresh=.
func (s *Server) handleDriftReport(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	q := r.URL.Query()
	report, err := s.drift.Report(r.Context(), driftservice.ReportParams{
		Scope:   q.Get("scope"),
		Against: q.Get("against"),
	})
	if err != nil {
		writeDriftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleDriftValidate serves POST /v1/admin/drift/validate: the §25.10
// check of a caller-supplied desired state against the stored snapshot.
// It reports differences as warnings and mutates no state.
func (s *Server) handleDriftValidate(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	var body struct {
		Desired map[string]any `json:"desired"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	result, err := s.drift.Validate(r.Context(), body.Desired)
	if err != nil {
		writeDriftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleDriftSnapshotRefresh serves POST /v1/admin/drift/snapshot/-
// refresh: the §25.10 replacement of the stored desired-state snapshot.
// §25.10 keeps refresh an explicit operator action — without confirm:true
// the §25.2 dry-run preview is returned and no snapshot is replaced.
func (s *Server) handleDriftSnapshotRefresh(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	var body struct {
		Desired map[string]any `json:"desired"`
		Confirm bool           `json:"confirm"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	if body.Desired == nil {
		conventions.WriteError(w, http.StatusBadRequest, "DRIFT_INVALID",
			conventions.CategoryPermanent, "a desired-state body is required")
		return
	}
	// §25.2 dry-run/confirm: without confirm:true, return a preview of
	// the snapshot replacement without writing it.
	if !body.Confirm {
		writeJSON(w, http.StatusOK, map[string]any{
			"dryRun": true,
			"preview": map[string]any{
				"resourcesAffected": []string{"bootstrap_seed_snapshot:live"},
				"estimatedDowntime": "0s",
				"warnings": []string{
					"This replaces the stored desired-state snapshot. Re-run with confirm:true to apply.",
				},
			},
		})
		return
	}
	result, err := s.drift.RefreshSnapshot(r.Context(), driftservice.RefreshRequest{
		Desired:   body.Desired,
		Confirm:   true,
		WrittenBy: callerIdentity(r),
	})
	if err != nil {
		writeDriftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
