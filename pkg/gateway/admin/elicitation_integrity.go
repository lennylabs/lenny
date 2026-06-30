// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// elicitationIntegrityResponse is the §15.1 line 824
// elicitation-content-integrity GET/PUT body. storedMode is the
// tenant's persisted mode (an unset stored value reports the §9.2
// tenant default of `enforce`). effectiveMode is the resolved
// `max(platformFloor, storedMode)` the gateway actually enforces.
// platformFloor is the deployer-supplied §9.2 floor at the time of the
// response. justification, changedAt, and changedBy carry the §15.1
// provenance of the last write. `mode` is retained as an alias for
// storedMode for compatibility with the v0 response field.
type elicitationIntegrityResponse struct {
	TenantID      string `json:"tenantId"`
	Mode          string `json:"mode"`
	StoredMode    string `json:"storedMode"`
	EffectiveMode string `json:"effectiveMode"`
	PlatformFloor string `json:"platformFloor"`
	Justification string `json:"justification"`
	ChangedAt     string `json:"changedAt"`
	ChangedBy     string `json:"changedBy"`
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
// GET /v1/admin/tenants/{id}/elicitation-content-integrity (§9.2 /
// §15.1). The response carries the tenant's stored mode, the effective
// mode that results from clamping it against the platform-wide floor,
// the platform floor itself, and the provenance (justification,
// changedAt, changedBy) of the last write.
// spec: §15.1 (tenant-admin gate, line 823,824; full GET body, line
// 824), §9.2 (elicitation content integrity).
func (r *Router) handleGetElicitationIntegrity(w http.ResponseWriter, req *http.Request) {
	// §10.2: a tenant-admin reads only its own tenant; a path naming a
	// different tenant is rejected. A platform-admin may read any.
	id, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
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
	// spec: §15.1 line 1209 — the elicitation-content-integrity
	// sub-resource's ETag is the tenant row's version, since the mode is
	// stored on the tenant. GET carries it so the next PUT can supply
	// If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID:      row.ID,
		Mode:          stored,
		StoredMode:    stored,
		EffectiveMode: effective,
		PlatformFloor: r.elicitationFloorValue(),
		Justification: row.ElicitationContentIntegrityJustification,
		ChangedAt:     rfc3339Nano(row.ElicitationContentIntegrityChangedAt),
		ChangedBy:     row.ElicitationContentIntegrityChangedBy,
	})
}

// resolveElicitationEffective returns the §9.2 effective mode the
// gateway enforces for a tenant: `max(platformFloor, stored)`. An
// unset floor (the default) is `off`, so the result equals the
// stored mode; an invalid floor is treated as `off`. The shared
// elicitation.ResolveEffectiveWithDefaults resolver applies the spec
// defaults so this surface and the §16.5 weakened-mode gauge agree on
// the effective mode. spec: §9.2 lines 60, 64. F-9.2.5.
func (r *Router) resolveElicitationEffective(stored string) string {
	return string(elicitation.ResolveEffectiveWithDefaults(r.elicitationFloorValue(), stored))
}

// handlePutElicitationIntegrity serves
// PUT /v1/admin/tenants/{id}/elicitation-content-integrity (§9.2). A
// write that relaxes the mode below `enforce` requires a non-empty
// justification; the change emits the §16.7 line 675
// `tenant.elicitation_content_integrity_changed` audit event with the
// `previous_stored_mode`, `new_stored_mode`, `platform_floor_at_change`,
// `effective_mode_at_change`, `justification`, `changed_by`,
// `changed_by_tenant_id`, `changed_at` payload SIEM consumers index on.
// spec: §9.2 line 66; §16.7 line 675; §15.1 (tenant-admin gate, line
// 823; PUT records mode, justification, changedAt, changedBy). F-9.2.9,
// ADM-3.
func (r *Router) handlePutElicitationIntegrity(w http.ResponseWriter, req *http.Request) {
	// §10.2: a tenant-admin writes only its own tenant; a path naming a
	// different tenant is rejected. A platform-admin may write any.
	id, perr := authorizeTenantPath(req, req.PathValue("id"))
	if perr != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", perr.Error(), nil)
		return
	}
	// Read the principal up front so the Update closure can persist the
	// §15.1 changedBy/changedAt provenance alongside the mode in one
	// write.
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	var body putElicitationIntegrityRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	if floor := elicitation.EnforcementMode(r.elicitationFloorValue()); floor.IsValid() && floor != elicitation.ModeOff {
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

	// spec: §15.1 lines 1207-1211 — the elicitation-content-integrity PUT
	// enforces If-Match against the tenant row's version (the
	// sub-resource's entity tag). A missing tenant 404s ahead of the
	// precondition.
	current, gerr := r.tenants.Get(req.Context(), id)
	if gerr != nil {
		if errors.Is(gerr, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	// changedAt stamps both the persisted §15.1 provenance and the §16.7
	// audit event with one instant so the tenant row and the audit trail
	// agree on when the mode was set.
	changedAt := r.clock().UTC()
	// previousStoredRaw captures the tenant's literal stored value
	// before the update so the audit row can distinguish "first write"
	// (null) from "explicit prior value". The §16.7 line 675 payload
	// requires `previous_stored_mode` ∈ {enforce | detect-only | off |
	// null}; the §9.2 default of `enforce` is computed at resolve-time,
	// not persisted, so an unset stored value emits null.
	var previousStoredRaw string
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		previousStoredRaw = t.ElicitationContentIntegrity
		// spec: §15.1 line 823 — the PUT records mode, justification,
		// changedAt, and changedBy on the tenant in the same write.
		t.ElicitationContentIntegrity = string(mode)
		t.ElicitationContentIntegrityJustification = body.Justification
		t.ElicitationContentIntegrityChangedAt = changedAt
		t.ElicitationContentIntegrityChangedBy = principal.Subject
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
	stored := string(mode)
	effective := r.resolveElicitationEffective(stored)
	// §16.7 line 675 — `platform_floor_at_change` records the floor
	// value rendered at the time of the write so audit analysis can
	// reconstruct the effective mode without re-resolving the floor
	// post-hoc. Empty string when the floor is unset.
	platformFloor := r.elicitationFloorValue()
	// `previous_stored_mode` is null on the first write (no prior
	// stored value); JSON-encodes as `null` rather than `""`. The
	// `justification` field is included unconditionally so a row from
	// a strengthening write is still queryable, even when the field
	// is empty.
	var previousStoredValue any
	if previousStoredRaw != "" {
		previousStoredValue = previousStoredRaw
	}
	r.emit(req.Context(), principal,
		string(audit.EventTenantElicitationContentIntegrityChanged), id,
		map[string]any{
			"tenant_id":                id,
			"previous_stored_mode":     previousStoredValue,
			"new_stored_mode":          stored,
			"platform_floor_at_change": platformFloor,
			"effective_mode_at_change": effective,
			"justification":            body.Justification,
			"changed_by":               principal.Subject,
			"changed_by_tenant_id":     principal.TenantID,
			"changed_at":               rfc3339Nano(changedAt),
		})
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elicitationIntegrityResponse{
		TenantID:      updated.ID,
		Mode:          stored,
		StoredMode:    stored,
		EffectiveMode: effective,
		PlatformFloor: platformFloor,
		Justification: updated.ElicitationContentIntegrityJustification,
		ChangedAt:     rfc3339Nano(updated.ElicitationContentIntegrityChangedAt),
		ChangedBy:     updated.ElicitationContentIntegrityChangedBy,
	})
}
