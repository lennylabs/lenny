// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
)

// tenantScopeFilter reports whether a runtime/pool read should be
// scoped to a tenant's access grants. Per §4 a platform-admin caller
// sees every resource (filtered=false); a tenant-admin caller is
// scoped to their own tenant.
func tenantScopeFilter(req *http.Request) (tenantID string, filtered bool) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return "", false
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		return "", false
	}
	if p.HasRole(auth.RoleTenantAdmin) {
		return p.TenantID, true
	}
	return "", false
}

// accessibleSet returns the set of resource names of the given kind a
// tenant may see, per the §4 tenant-access join tables. When the
// tenantAccess store is not wired the set is empty — a tenant-admin
// sees nothing rather than everything (§4 fail-closed).
func (r *Router) accessibleSet(ctx context.Context, kind tenantaccessstore.ResourceKind, tenantID string) map[string]bool {
	set := map[string]bool{}
	if r.tenantAccess == nil {
		return set
	}
	names, err := r.tenantAccess.ListForTenant(ctx, kind, tenantID)
	if err != nil {
		return set
	}
	for _, n := range names {
		set[n] = true
	}
	return set
}

// tenantAccessGrantPayload is the §15.1 grant request body.
type tenantAccessGrantPayload struct {
	TenantID string `json:"tenantId"`
}

// tenantAccessEntry is one §15.1 tenant-access list entry.
type tenantAccessEntry struct {
	TenantID   string `json:"tenantId"`
	TenantName string `json:"tenantName,omitempty"`
	GrantedAt  string `json:"grantedAt,omitempty"`
	GrantedBy  string `json:"grantedBy,omitempty"`
}

// WithTenantAccess wires the §15.1 runtime/pool tenant-access handlers
// onto the Router.
func (r *Router) WithTenantAccess(s tenantaccessstore.Store) *Router {
	r.tenantAccess = s
	return r
}

// grantAccessHandler builds the POST tenant-access handler for a
// resource kind. The grant is idempotent: a newly created grant
// returns 201, a grant that already existed returns 200 (§15.1).
func (r *Router) grantAccessHandler(kind tenantaccessstore.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		resource := req.PathValue("name")
		var body tenantAccessGrantPayload
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
			return
		}
		if body.TenantID == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId is required",
				map[string]any{"field": "tenantId"})
			return
		}
		principal, _ := authmw.FromContext(req.Context())
		created, err := r.tenantAccess.Grant(req.Context(), kind, resource, body.TenantID, principal.Subject, r.clock())
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		if created {
			r.emit(req.Context(), principal, "admin."+string(kind)+".tenant_access_granted", resource,
				map[string]any{"tenantId": body.TenantID})
		}
		w.Header().Set("Content-Type", "application/json")
		if created {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": resource,
			"tenantId": body.TenantID,
		})
	}
}

// listAccessHandler builds the GET tenant-access handler for a
// resource kind. Each entry carries the granted tenant's display name
// when the tenant registry resolves it.
func (r *Router) listAccessHandler(kind tenantaccessstore.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		resource := req.PathValue("name")
		grants, err := r.tenantAccess.List(req.Context(), kind, resource)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		out := make([]tenantAccessEntry, 0, len(grants))
		for _, g := range grants {
			e := tenantAccessEntry{
				TenantID:  g.TenantID,
				GrantedAt: rfc3339Nano(g.GrantedAt),
				GrantedBy: g.GrantedBy,
			}
			if r.tenants != nil {
				if t, err := r.tenants.Get(req.Context(), g.TenantID); err == nil {
					e.TenantName = t.DisplayName
				}
			}
			out = append(out, e)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tenantAccess": out})
	}
}

// revokeAccessHandler builds the DELETE tenant-access handler for a
// resource kind. A missing grant is 404 (§15.1).
func (r *Router) revokeAccessHandler(kind tenantaccessstore.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		resource := req.PathValue("name")
		tenantID := req.PathValue("tenantId")
		if err := r.tenantAccess.Revoke(req.Context(), kind, resource, tenantID); err != nil {
			if errors.Is(err, tenantaccessstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
					"tenant-access grant not found", nil)
				return
			}
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		principal, _ := authmw.FromContext(req.Context())
		r.emit(req.Context(), principal, "admin."+string(kind)+".tenant_access_revoked", resource,
			map[string]any{"tenantId": tenantID})
		w.WriteHeader(http.StatusNoContent)
	}
}
