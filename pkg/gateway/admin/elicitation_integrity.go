// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/elicitation"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// elicitationIntegrityResponse is the §9.2 elicitation-content-integrity
// admin payload. mode is the tenant's stored mode; an unset stored
// value reports the §9.2 tenant default of `enforce`.
type elicitationIntegrityResponse struct {
	TenantID string `json:"tenantId"`
	Mode     string `json:"mode"`
}

// putElicitationIntegrityRequest is the PUT body. justification is
// mandatory when mode is weaker than `enforce` so the §11.7 audit
// trail records why the integrity posture was relaxed.
type putElicitationIntegrityRequest struct {
	Mode          string `json:"mode"`
	Justification string `json:"justification"`
}

// effectiveStoredMode reports the tenant's stored §9.2 elicitation
// content-integrity mode, defaulting an unset value to the `enforce`
// tenant default.
func effectiveStoredMode(t tenantstore.Tenant) string {
	if t.ElicitationContentIntegrity == "" {
		return string(elicitation.ModeEnforce)
	}
	return t.ElicitationContentIntegrity
}

// handleGetElicitationIntegrity serves
// GET /v1/admin/tenants/{id}/elicitation-content-integrity (§9.2).
func (r *Router) handleGetElicitationIntegrity(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	row, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID: row.ID,
		Mode:     effectiveStoredMode(row),
	})
}

// handlePutElicitationIntegrity serves
// PUT /v1/admin/tenants/{id}/elicitation-content-integrity (§9.2). A
// write that relaxes the mode below `enforce` requires a non-empty
// justification; the change emits the
// `tenant.elicitation_content_integrity_mode_changed` audit event.
func (r *Router) handlePutElicitationIntegrity(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	var body putElicitationIntegrityRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	mode := elicitation.EnforcementMode(body.Mode)
	if !mode.IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"mode must be one of off, detect-only, or enforce",
			map[string]any{"field": "mode"})
		return
	}
	// §15.1: relaxing the mode below `enforce` requires a justification
	// so the audit trail captures why the posture was weakened.
	if !mode.AtLeast(elicitation.ModeEnforce) && body.Justification == "" {
		writeError(w, http.StatusBadRequest, "ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED",
			"a justification is required when the mode is weaker than enforce",
			map[string]any{"field": "justification"})
		return
	}

	var oldMode string
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		oldMode = effectiveStoredMode(*t)
		t.ElicitationContentIntegrity = string(mode)
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "tenant.elicitation_content_integrity_mode_changed", id, map[string]any{
		"oldMode":       oldMode,
		"newMode":       string(mode),
		"justification": body.Justification,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID: updated.ID,
		Mode:     string(mode),
	})
}
