// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/compliance"
	"github.com/lennylabs/lenny/pkg/gateway/externaladapterstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// AdapterValidator runs the §24.8 / §15.2.1 conformance suite against a
// registered external adapter and returns the per-test report. The
// production implementation drives the lenny-compliance harness via
// compliance.RunSuite in a subprocess (the §15 line 1414 sandboxed
// environment seam); tests inject a fake.
//
// A non-nil error means the suite could not be executed at all (harness
// missing, adapter unusable). A returned report with a non-zero Failed
// count is a successful run that found conformance violations — the
// handler transitions the adapter to validation_failed in that case.
//
// spec: §24.8 line 113; §15 line 1414.
type AdapterValidator interface {
	Validate(ctx context.Context, binaryPath, level string) (compliance.Report, error)
}

// ComplianceValidator is the production AdapterValidator. It drives the
// lenny-compliance harness via compliance.RunSuite as a subprocess —
// the §15 line 1414 sandboxed-environment seam. HarnessPath, when empty,
// resolves `lenny-compliance` on $PATH; when the harness is absent,
// compliance.RunSuite returns compliance.ErrHarnessNotFound and the
// validate handler reports 503 (the adapter stays pending_validation).
type ComplianceValidator struct {
	HarnessPath string
}

// Validate runs the conformance suite against the adapter binary.
func (c ComplianceValidator) Validate(ctx context.Context, binaryPath, level string) (compliance.Report, error) {
	return compliance.RunSuite(ctx, compliance.NewAdapter(binaryPath, compliance.Level(level)), compliance.Options{HarnessPath: c.HarnessPath})
}

var _ AdapterValidator = ComplianceValidator{}

// ExternalAdapterPayload is the §15.1 admin external-adapter wire shape.
type ExternalAdapterPayload struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	PathPrefix  string `json:"pathPrefix,omitempty"`
	BinaryPath  string `json:"binaryPath,omitempty"`
	Level       string `json:"level,omitempty"`
	// Status is read-only on the wire — the store owns the lifecycle
	// transition (§15 line 1414). A value supplied on create/update is
	// ignored.
	Status         string                    `json:"status,omitempty"`
	LastValidation *ValidationReportPayload  `json:"lastValidation,omitempty"`
	CreatedAt      string                    `json:"createdAt,omitempty"`
	UpdatedAt      string                    `json:"updatedAt,omitempty"`
}

// ValidationReportPayload is the validate-run outcome on the wire.
type ValidationReportPayload struct {
	Level       string                     `json:"level"`
	Total       int                        `json:"total"`
	Passed      int                        `json:"passed"`
	Failed      int                        `json:"failed"`
	Failures    []ValidationFailurePayload `json:"failures,omitempty"`
	ValidatedAt string                     `json:"validatedAt,omitempty"`
}

// ValidationFailurePayload is one failed conformance check on the wire.
// Per §24.8 line 113 each failure cites the specific assertion that
// failed.
type ValidationFailurePayload struct {
	Name   string `json:"name"`
	Spec   string `json:"spec,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func fromExternalAdapter(a externaladapterstore.ExternalAdapter) ExternalAdapterPayload {
	out := ExternalAdapterPayload{
		Name:        a.Name,
		DisplayName: a.DisplayName,
		Protocol:    a.Protocol,
		PathPrefix:  a.PathPrefix,
		BinaryPath:  a.BinaryPath,
		Level:       a.Level,
		Status:      string(a.Status),
		CreatedAt:   rfc3339Nano(a.CreatedAt),
		UpdatedAt:   rfc3339Nano(a.UpdatedAt),
	}
	if a.LastValidation != nil {
		out.LastValidation = fromValidationReport(*a.LastValidation)
	}
	return out
}

func fromValidationReport(v externaladapterstore.ValidationReport) *ValidationReportPayload {
	p := &ValidationReportPayload{
		Level:       v.Level,
		Total:       v.Total,
		Passed:      v.Passed,
		Failed:      v.Failed,
		ValidatedAt: rfc3339Nano(v.ValidatedAt),
	}
	for _, f := range v.Failures {
		p.Failures = append(p.Failures, ValidationFailurePayload{Name: f.Name, Spec: f.Spec, Detail: f.Detail})
	}
	return p
}

// WithExternalAdapters wires the §15.1 / §24.8 external-adapter CRUD and
// validate handlers onto the Router. The validator drives the
// conformance suite for the validate gate; when nil, validate returns
// 503 so the adapter stays in pending_validation rather than being
// spuriously failed.
func (r *Router) WithExternalAdapters(s externaladapterstore.Store, v AdapterValidator) *Router {
	r.externalAdapters = s
	r.adapterValidator = v
	return r
}

func (r *Router) handleCreateExternalAdapter(w http.ResponseWriter, req *http.Request) {
	var body ExternalAdapterPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler reached without authenticated principal", nil)
		return
	}
	a := externaladapterstore.ExternalAdapter{
		Name:        body.Name,
		DisplayName: body.DisplayName,
		Protocol:    body.Protocol,
		PathPrefix:  body.PathPrefix,
		BinaryPath:  body.BinaryPath,
		Level:       body.Level,
		CreatedAt:   r.clock(),
	}
	if err := r.externalAdapters.Create(req.Context(), a); err != nil {
		if errors.Is(err, externaladapterstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT", "external adapter with this name already exists", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.externalAdapters.Get(req.Context(), body.Name)
	r.emit(req.Context(), principal, audit.EventAdminExternalAdapterRegistered.String(), body.Name, map[string]any{
		"protocol": stored.Protocol,
		"level":    stored.Level,
		"status":   string(stored.Status),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromExternalAdapter(stored))
}

func (r *Router) handleListExternalAdapters(w http.ResponseWriter, req *http.Request) {
	rows, err := r.externalAdapters.List(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]ExternalAdapterPayload, 0, len(rows))
	for _, a := range rows {
		out = append(out, fromExternalAdapter(a))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"externalAdapters": out})
}

func (r *Router) handleGetExternalAdapter(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	a, err := r.externalAdapters.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, externaladapterstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "external adapter not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromExternalAdapter(a))
}

func (r *Router) handleUpdateExternalAdapter(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body ExternalAdapterPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler reached without authenticated principal", nil)
		return
	}
	updated, err := r.externalAdapters.Update(req.Context(), name, func(a *externaladapterstore.ExternalAdapter) error {
		if body.DisplayName != "" {
			a.DisplayName = body.DisplayName
		}
		if body.Protocol != "" {
			a.Protocol = body.Protocol
		}
		if body.PathPrefix != "" {
			a.PathPrefix = body.PathPrefix
		}
		// A change to the adapter under test (binary or level) invalidates
		// the prior validation. Reset to pending_validation so the gate is
		// re-run before the adapter receives traffic (§15.1 line 1199
		// "Validates update"; §15 line 1414).
		if (body.BinaryPath != "" && body.BinaryPath != a.BinaryPath) ||
			(body.Level != "" && body.Level != a.Level) {
			if body.BinaryPath != "" {
				a.BinaryPath = body.BinaryPath
			}
			if body.Level != "" {
				a.Level = body.Level
			}
			a.Status = externaladapterstore.StatusPendingValidation
			a.LastValidation = nil
		}
		return externaladapterstore.Validate(*a)
	})
	if err != nil {
		if errors.Is(err, externaladapterstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "external adapter not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, audit.EventAdminExternalAdapterUpdated.String(), name, map[string]any{
		"status": string(updated.Status),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromExternalAdapter(updated))
}

func (r *Router) handleDeleteExternalAdapter(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler reached without authenticated principal", nil)
		return
	}
	if err := r.externalAdapters.Delete(req.Context(), name); err != nil {
		if errors.Is(err, externaladapterstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "external adapter not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, audit.EventAdminExternalAdapterDeleted.String(), name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleValidateExternalAdapter runs the §24.8 conformance suite against
// the registered adapter and transitions its status. Per §15 line 1414
// it transitions pending_validation/validation_failed → active on a
// passing run, or → validation_failed (with per-test details) on
// failure. When no validator is wired, or the harness cannot run, the
// adapter is left untouched and a 503 is returned — the gate must not
// fail an adapter it could not actually test.
func (r *Router) handleValidateExternalAdapter(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler reached without authenticated principal", nil)
		return
	}
	adapter, err := r.externalAdapters.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, externaladapterstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "external adapter not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if r.adapterValidator == nil {
		writeError(w, http.StatusServiceUnavailable, "ADAPTER_VALIDATION_UNAVAILABLE",
			"adapter validation is not available in this deployment (no conformance harness wired)", nil)
		return
	}
	report, runErr := r.adapterValidator.Validate(req.Context(), adapter.BinaryPath, adapter.Level)
	if runErr != nil {
		// The suite could not be executed. Leave the adapter untouched.
		code := "ADAPTER_VALIDATION_UNAVAILABLE"
		status := http.StatusServiceUnavailable
		if !errors.Is(runErr, compliance.ErrHarnessNotFound) {
			code = "ADAPTER_VALIDATION_ERROR"
			status = http.StatusInternalServerError
		}
		writeError(w, status, code, runErr.Error(), nil)
		return
	}

	vr := externaladapterstore.ValidationReport{
		Level:       report.Level,
		Total:       report.Summary.Total,
		Passed:      report.Summary.Passed,
		Failed:      report.Summary.Failed,
		ValidatedAt: r.clock(),
	}
	for _, c := range report.Checks {
		if !c.Pass {
			vr.Failures = append(vr.Failures, externaladapterstore.ValidationFailure{
				Name:   c.Name,
				Spec:   c.Spec,
				Detail: c.Detail,
			})
		}
	}
	passed := report.Summary.Total > 0 && report.Summary.Failed == 0

	updated, err := r.externalAdapters.Update(req.Context(), name, func(a *externaladapterstore.ExternalAdapter) error {
		v := vr
		a.LastValidation = &v
		if passed {
			a.Status = externaladapterstore.StatusActive
		} else {
			a.Status = externaladapterstore.StatusValidationFailed
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if passed {
		r.emit(req.Context(), principal, audit.EventAdminExternalAdapterValidated.String(), name, map[string]any{
			"level":  report.Level,
			"checks": report.Summary.Total,
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fromExternalAdapter(updated))
		return
	}

	r.emit(req.Context(), principal, audit.EventAdminExternalAdapterValidationFailed.String(), name, map[string]any{
		"level":  report.Level,
		"failed": report.Summary.Failed,
		"total":  report.Summary.Total,
	})
	// A failing conformance run is reported as 422: the request was
	// well-formed but the adapter does not satisfy the contract. The
	// per-test details ride in the error envelope and on the updated
	// record's lastValidation.
	writeError(w, http.StatusUnprocessableEntity, "ADAPTER_VALIDATION_FAILED",
		"adapter failed the conformance suite; see details", map[string]any{
			"status":         string(updated.Status),
			"lastValidation": fromValidationReport(vr),
		})
}
