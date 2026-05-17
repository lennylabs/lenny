// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// allowAllWarning is the §10.6 advisory the lenny-noenvironmentpolicy-audit
// control attaches when a tenant sets noEnvironmentPolicy to allow-all.
const allowAllWarning = `noEnvironmentPolicy: allow-all grants unrestricted ` +
	`runtime access to all authenticated users with no environment membership ` +
	`in this tenant. Verify this matches the intended security posture.`

// RBACConfigPayload is the §10.6 / §15.1 tenant RBAC-config admin
// payload. v1 carries the noEnvironmentPolicy field; the §10.6
// identityProvider, tokenPolicy, capabilities, and mcpAnnotationMapping
// fields are not yet stored and are added here as they are built.
type RBACConfigPayload struct {
	TenantID            string `json:"tenantId"`
	NoEnvironmentPolicy string `json:"noEnvironmentPolicy"`
}

// effectiveNoEnvironmentPolicy reports a tenant's §10.6
// noEnvironmentPolicy, treating an unset value as the platform default
// deny-all — §10.6 requires a tenant-level omission to be treated as
// deny-all.
func effectiveNoEnvironmentPolicy(t tenantstore.Tenant) string {
	if t.NoEnvironmentPolicy == "" {
		return tenantstore.NoEnvPolicyDenyAll
	}
	return t.NoEnvironmentPolicy
}

// handleGetRBACConfig serves GET /v1/admin/tenants/{id}/rbac-config
// (§10.6 / §15.1).
func (r *Router) handleGetRBACConfig(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.tenants.Get(req.Context(), tenant)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RBACConfigPayload{
		TenantID:            row.ID,
		NoEnvironmentPolicy: effectiveNoEnvironmentPolicy(row),
	})
}

// handlePutRBACConfig serves PUT /v1/admin/tenants/{id}/rbac-config
// (§10.6 / §15.1). A body that omits noEnvironmentPolicy is treated as
// deny-all per §10.6. Setting allow-all succeeds but, as the §10.6
// lenny-noenvironmentpolicy-audit advisory control, attaches a Warning
// response header because allow-all broadens runtime access for
// callers with no environment membership.
func (r *Router) handlePutRBACConfig(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	var body RBACConfigPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	// §10.6: an omitted noEnvironmentPolicy is treated as deny-all.
	policy := body.NoEnvironmentPolicy
	if policy == "" {
		policy = tenantstore.NoEnvPolicyDenyAll
	}
	if policy != tenantstore.NoEnvPolicyDenyAll && policy != tenantstore.NoEnvPolicyAllowAll {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"noEnvironmentPolicy must be deny-all or allow-all",
			map[string]any{"field": "noEnvironmentPolicy"})
		return
	}
	updated, err := r.tenants.Update(req.Context(), tenant, func(t *tenantstore.Tenant) error {
		t.NoEnvironmentPolicy = policy
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
	r.emit(req.Context(), principal, "admin.tenant.rbac_config_updated", tenant, map[string]any{
		"noEnvironmentPolicy": policy,
	})
	if policy == tenantstore.NoEnvPolicyAllowAll {
		w.Header().Set("Warning", `299 - "`+allowAllWarning+`"`)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RBACConfigPayload{
		TenantID:            updated.ID,
		NoEnvironmentPolicy: effectiveNoEnvironmentPolicy(updated),
	})
}
