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
// admin payload. storedMode is the tenant's persisted mode (an unset
// stored value reports the §9.2 tenant default of `enforce`).
// effectiveMode is the resolved `max(platformFloor, storedMode)`
// the gateway actually enforces. `mode` is retained as an alias for
// storedMode for backward compatibility with the v0 response shape.
type elicitationIntegrityResponse struct {
	TenantID      string `json:"tenantId"`
	Mode          string `json:"mode"`
	StoredMode    string `json:"storedMode"`
	EffectiveMode string `json:"effectiveMode"`
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
// The response carries both the tenant's stored mode and the
// effective mode that results from clamping it against the
// platform-wide floor.
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
	stored := effectiveStoredMode(row)
	effective := r.resolveElicitationEffective(stored)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID:      row.ID,
		Mode:          stored,
		StoredMode:    stored,
		EffectiveMode: effective,
	})
}

// resolveElicitationEffective returns the §9.2 effective mode the
// gateway enforces for a tenant: `max(platformFloor, stored)`. An
// unset floor (the default) is `off`, so the result equals the
// stored mode. An invalid floor is treated as `off` and logged
// elsewhere; this helper never errors so the admin GET stays stable.
func (r *Router) resolveElicitationEffective(stored string) string {
	floor := elicitation.EnforcementMode(r.elicitationFloor)
	if !floor.IsValid() {
		floor = elicitation.ModeOff
	}
	storedMode := elicitation.EnforcementMode(stored)
	if !storedMode.IsValid() {
		storedMode = elicitation.ModeEnforce
	}
	got, err := elicitation.ResolveEffective(floor, storedMode)
	if err != nil {
		return string(storedMode)
	}
	return string(got)
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
	// §9.2 platform floor: a tenant cannot persist a stored mode that
	// would leave them strictly below the platform-wide floor. The
	// floor is the §9.2 deployer-supplied minimum-enforcement posture;
	// the platform operator sets it via the Helm value
	// security.elicitationContentIntegrity.floor (rendered onto the
	// gateway via --elicitation-content-integrity-floor).
	if floor := elicitation.EnforcementMode(r.elicitationFloor); floor.IsValid() && floor != elicitation.ModeOff {
		if floor.Rank() > mode.Rank() {
			writeError(w, http.StatusBadRequest, "ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR",
				"the requested mode is below the platform-wide §9.2 floor",
				map[string]any{
					"field":         "mode",
					"platformFloor": string(floor),
					"requestedMode": string(mode),
				})
			return
		}
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
	stored := string(mode)
	effective := r.resolveElicitationEffective(stored)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID:      updated.ID,
		Mode:          stored,
		StoredMode:    stored,
		EffectiveMode: effective,
	})
}
