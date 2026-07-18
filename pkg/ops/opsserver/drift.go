// SPDX-License-Identifier: MIT

package opsserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
)

// driftErrorMap maps each §25.10 canonical error code to its documented
// HTTP status and §25.2 category.
var driftErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	driftservice.ErrCodeDesiredStateMissing:  {http.StatusServiceUnavailable, conventions.CategoryTransient},
	driftservice.ErrCodeNoTargetSnapshot:     {http.StatusNotFound, conventions.CategoryPermanent},
	driftservice.ErrCodeReconcilePartial:     {http.StatusMultiStatus, conventions.CategoryTransient},
	driftservice.ErrCodeReconcileUnavailable: {http.StatusServiceUnavailable, conventions.CategoryTransient},
	driftservice.ErrCodeInvalid:              {http.StatusBadRequest, conventions.CategoryPermanent},
}

// writeDriftError maps a §25.10 drift service error to the §25.2
// canonical error envelope and writes it.
//
// A non-zero driftservice.Error.HTTPStatus overrides the default
// code→status mapping. §25.10 line 3866 maps DRIFT_DESIRED_STATE_MISSING
// to either 404 (cold-start) or 503 (Postgres down); the cold-start
// path sets HTTPStatus=404 so a fresh install reports as "no snapshot
// exists" rather than as a transient outage. F-25.10.10.
func writeDriftError(w http.ResponseWriter, err error) {
	code := driftservice.CodeOf(err)
	mapping, knownCode := driftErrorMap[code]
	status := mapping.status
	category := mapping.category
	if !knownCode {
		status = http.StatusInternalServerError
		code = "INTERNAL"
		category = conventions.CategoryTransient
	}
	var de *driftservice.Error
	if errors.As(err, &de) && de.HTTPStatus != 0 {
		status = de.HTTPStatus
	}
	conventions.WriteError(w, status, code, category, err.Error())
}

// registerDriftRoutes wires the §25.10 configuration-drift endpoints
// onto the Server's mux.
func (s *Server) registerDriftRoutes() {
	s.mux.HandleFunc("GET /v1/admin/drift", s.handleDriftReport)
	s.mux.HandleFunc("POST /v1/admin/drift/validate", s.handleDriftValidate)
	s.mux.HandleFunc("POST /v1/admin/drift/snapshot/refresh", s.handleDriftSnapshotRefresh)
	s.mux.HandleFunc("POST /v1/admin/drift/reconcile", s.handleDriftReconcile)
}

// driftUnavailable reports the §25.10 drift surface as unconfigured.
func (s *Server) driftUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "DRIFT_SERVICE_UNAVAILABLE",
		conventions.CategoryTransient, "the configuration-drift subsystem is not configured")
}

// handleDriftReport serves GET /v1/admin/drift: the §25.10 drift report
// comparing running state against the desired-state snapshot. It
// accepts ?scope=, ?against=, and ?fresh= (§25.10 line 3762).
//
// ?fresh=true bypasses the §25.10 line 3822 running-state cache; the
// HTTP layer parses the standard truthy spellings (true/1/yes/on). An
// unparseable value is treated as not-fresh so a typo does not silently
// thrash the cache. F-25.10.7.
func (s *Server) handleDriftReport(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	q := r.URL.Query()
	fresh := false
	if v := q.Get("fresh"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			fresh = parsed
		}
	}
	// §25.10 (Drift Detection Logic): "Alternatively, the caller can
	// supply a {"desired": {...}} body for ad-hoc comparison." The
	// caller-supplied desired state replaces the bootstrap_seed_snapshot
	// as the desired side, so the report needs no snapshot store — this is
	// the §25.10 Degradation "Postgres down, caller supplies desired body"
	// path that lets GitOps agents keep running drift checks during a
	// Postgres outage, with the response recording desiredStateSource:
	// "caller". An empty body leaves Desired nil so the snapshot side is
	// used; a malformed body is a §25.2 validation error.
	// spec: §25.10
	var reqBody struct {
		Desired map[string]any `json:"desired"`
	}
	if err := readJSONBody(r, &reqBody); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	// §25.10 line 3791: ?against=both returns the live and target diffs in
	// one response. It routes through a distinct service method because
	// the response carries two reports rather than one. F-25.10.6. The
	// two-snapshot diff is defined only against the stored live and target
	// snapshots, so a caller-supplied desired body does not apply here.
	if q.Get("against") == "both" {
		both, err := s.drift.ReportBoth(r.Context(), driftservice.ReportParams{
			Scope: q.Get("scope"),
			Fresh: fresh,
		})
		if err != nil {
			writeDriftError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, both)
		return
	}
	report, err := s.drift.Report(r.Context(), driftservice.ReportParams{
		Scope:   q.Get("scope"),
		Against: q.Get("against"),
		Desired: reqBody.Desired,
		Fresh:   fresh,
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
//
// The optional "stored" body field supplies the snapshot side of the
// diff in place of the stored bootstrap_seed_snapshot — the §25.10
// offline-validation degradation path that lets GitOps agents diff two
// caller-supplied snapshots without consulting Postgres. F-25.10.12.
func (s *Server) handleDriftValidate(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	var body struct {
		Desired map[string]any `json:"desired"`
		Stored  map[string]any `json:"stored,omitempty"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	result, err := s.drift.Validate(r.Context(), driftservice.ValidateParams{
		Desired: body.Desired,
		Stored:  body.Stored,
	})
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
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
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

// handleDriftReconcile serves POST /v1/admin/drift/reconcile: the §25.10
// reconciliation of drifted resources through the gateway admin API.
// It follows the §25.2 dry-run/confirm pattern — without confirm:true a
// preview of the resources that would reconcile is returned and no
// state is changed.
//
// A confirmed reconcile that applies every resource returns 200. A
// reconcile where some resources could not be applied returns the
// §25.10 line 3852 / line 3865 partial result: HTTP 207 with the result
// body carrying the per-resource outcomes and errorCode
// DRIFT_RECONCILE_PARTIAL. F-25.10.1.
func (s *Server) handleDriftReconcile(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		s.driftUnavailable(w)
		return
	}
	var body struct {
		Scope     string         `json:"scope"`
		Resources []string       `json:"resources"`
		Confirm   bool           `json:"confirm"`
		Desired   map[string]any `json:"desired,omitempty"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	result, err := s.drift.Reconcile(r.Context(), driftservice.ReconcileParams{
		Scope:     body.Scope,
		Resources: body.Resources,
		Confirm:   body.Confirm,
		Desired:   body.Desired,
		StartedBy: callerIdentity(r),
	})
	if err != nil {
		writeDriftError(w, err)
		return
	}
	// §25.10 line 3852: a partial reconcile (some resources failed)
	// surfaces as HTTP 207 with the per-resource outcomes in the body.
	status := http.StatusOK
	if result.ErrorCode == driftservice.ErrCodeReconcilePartial {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}
