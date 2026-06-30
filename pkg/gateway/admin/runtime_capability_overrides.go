// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// WithRuntimeCapabilityOverrides wires the §5.1 line 49 per-tenant
// runtime capability override store. A nil store leaves the
// /v1/admin/tenants/{id}/runtime-capability-overrides routes
// unregistered. spec: §5.1 line 49 — F-5.1.20.
func (r *Router) WithRuntimeCapabilityOverrides(store runtimecapoverride.Store) *Router {
	r.capOverrides = store
	return r
}

// handleListRuntimeCapabilityOverrides implements
// GET /v1/admin/tenants/{id}/runtime-capability-overrides — every §5.1
// capability override the tenant has recorded, keyed by runtime name.
func (r *Router) handleListRuntimeCapabilityOverrides(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	overrides, err := r.capOverrides.List(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"overrides": overrides})
}

// handleGetRuntimeCapabilityOverride implements
// GET /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}.
func (r *Router) handleGetRuntimeCapabilityOverride(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	runtime := req.PathValue("runtime")
	ov, ok, err := r.capOverrides.Get(req.Context(), tenant, runtime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"no capability override recorded for this tenant and runtime", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ov)
}

// handlePutRuntimeCapabilityOverride implements
// PUT /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}. The
// body is a §5.1 CapabilityOverride: a subset of the capability axes
// (interaction, injectionSupported, injectionModes, preConnect,
// sdkWarmBlockingPaths). An omitted axis inherits the runtime's platform
// default; a present axis overrides it for the tenant.
func (r *Router) handlePutRuntimeCapabilityOverride(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	runtime := req.PathValue("runtime")
	if verr := runtimestore.ValidateName(runtime); verr != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", verr.Error(),
			map[string]any{"field": "runtime"})
		return
	}
	var body runtimestore.CapabilityOverride
	if derr := json.NewDecoder(req.Body).Decode(&body); derr != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if verr := body.Validate(); verr != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", verr.Error(), nil)
		return
	}
	if body.IsZero() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"override sets no capability axis; PUT must override at least one of interaction, injectionSupported, injectionModes, preConnect, sdkWarmBlockingPaths",
			nil)
		return
	}
	// A per-tenant override targets a platform-global runtime; reject an
	// override for a runtime that does not exist so a typo does not become
	// a silent no-op.
	if r.runtimes != nil {
		if _, gerr := r.runtimes.Get(req.Context(), runtime); gerr != nil {
			if errors.Is(gerr, runtimestore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
					"runtime not found", map[string]any{"runtime": runtime})
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
	}
	if perr := r.capOverrides.Put(req.Context(), tenant, runtime, body); perr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", perr.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.runtime_capability_override_updated", tenant,
		map[string]any{"runtime": runtime})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// handleDeleteRuntimeCapabilityOverride implements
// DELETE /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}.
// Deleting a missing override returns 204 (idempotent).
func (r *Router) handleDeleteRuntimeCapabilityOverride(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	runtime := req.PathValue("runtime")
	if derr := r.capOverrides.Delete(req.Context(), tenant, runtime); derr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", derr.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.runtime_capability_override_deleted", tenant,
		map[string]any{"runtime": runtime})
	w.WriteHeader(http.StatusNoContent)
}
