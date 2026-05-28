// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
)

// runtimestoreErrNotFound / poolstoreErrNotFound expose the
// concrete-package sentinel errors for `errors.Is` checks in
// resourceExists. Local indirections keep tenant_access.go decoupled
// from the runtimestore/poolstore package layout if either ErrNotFound
// is later renamed.
func runtimestoreErrNotFound() error { return runtimestore.ErrNotFound }
func poolstoreErrNotFound() error    { return poolstore.ErrNotFound }

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

// requireResourceManage gates a §10.2 manage-* admin route on the
// supplied permission. Per the §10.2 RBAC matrix a `platform-admin`
// holds every manage permission across all tenants, a `tenant-admin`
// holds the tenant-scoped manage permissions for their own tenant, and
// a tenant custom role holds whatever subset its definition grants.
// The gate admits a caller when one of its roles — built-in or custom —
// grants perm, so it widens `requireAdmin` to exactly the matrix-defined
// `tenant-admin` entitlement (plus custom roles) without admitting any
// role the matrix does not.
//
// scopeKind selects the §4 tenant-scoping applied to a non-platform-admin
// caller on a per-resource (`{name}`) route: when it is KindRuntime or
// KindPool the caller must additionally hold a `runtime_tenant_access` /
// `pool_tenant_access` grant for the path resource, mirroring the §15.1
// "tenant-admin restricted to access-table entries for own tenant" rule
// and the read-path scoping in handleGetRuntime / handleGetPool. An
// empty scopeKind disables per-resource scoping (used for routes whose
// resource carries no access table, such as delegation policies).
func (r *Router) requireResourceManage(perm auth.Permission, scopeKind tenantaccessstore.ResourceKind) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p, ok := authmw.FromContext(req.Context())
			if !ok {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"endpoint requires authentication", nil)
				return
			}
			if !r.principalGrantsPermission(req.Context(), p, perm) {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"this endpoint requires the "+string(perm)+" permission", nil)
				return
			}
			// §4 / §15.1 write scoping: a non-platform-admin caller may
			// mutate a runtime/pool only when their tenant holds an
			// access grant for it. A platform-admin is unscoped. A route
			// without a {name} path value (a collection route) carries no
			// resource to scope and falls through.
			if scopeKind.IsValid() && !p.HasRole(auth.RolePlatformAdmin) {
				resource := req.PathValue("name")
				if resource != "" && !r.accessibleSet(req.Context(), scopeKind, p.TenantID)[resource] {
					// A resource the caller's tenant cannot see is
					// reported as absent, matching the read-path 404 so
					// the gate does not disclose existence.
					writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
						string(scopeKind)+" not found", nil)
					return
				}
			}
			next.ServeHTTP(w, req)
		})
	}
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

// resourceExists reports whether a runtime/pool with the given name
// is registered in the corresponding store. The Memory join-table
// path accepts any non-empty resource string (the Postgres path is
// FK-protected); a pre-mutate existence check converts the silent
// dangling-grant into a 404 with the resource kind named in the
// detail map. F-24.3.6. spec: §15.1 line 769 (platform-global
// runtime/pool records; tenant-access tables reference them as
// foreign keys).
func (r *Router) resourceExists(ctx context.Context, kind tenantaccessstore.ResourceKind, name string) (bool, error) {
	switch kind {
	case tenantaccessstore.KindRuntime:
		if r.runtimes == nil {
			return true, nil
		}
		if _, err := r.runtimes.Get(ctx, name); err != nil {
			if errors.Is(err, runtimestoreErrNotFound()) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	case tenantaccessstore.KindPool:
		if r.pools == nil {
			return true, nil
		}
		if _, err := r.pools.Get(ctx, name); err != nil {
			if errors.Is(err, poolstoreErrNotFound()) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	return true, nil
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
		// spec: §15.1 line 779 — the join-table row is FK-referenced;
		// a Memory deployment must mirror that constraint so the
		// list endpoint never returns a dangling grant. F-24.3.6.
		exists, err := r.resourceExists(req.Context(), kind, resource)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
				string(kind)+" not found",
				map[string]any{string(kind): resource})
			return
		}
		principal, _ := authmw.FromContext(req.Context())
		created, err := r.tenantAccess.Grant(req.Context(), kind, resource, body.TenantID, principal.Subject, r.clock())
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		if created {
			// spec: §16.7 audit catalog convention `<domain>.<verb_object>`.
			// The domain is the resource kind (runtime / pool), matching
			// adjacent §15.1 emits such as `pool.reconciliation_resumed`
			// and `pool.sdk_warm_circuit_breaker_override`. F-24.3.7.
			r.emit(req.Context(), principal, string(kind)+".tenant_access_granted", resource,
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
		// spec: §16.7 audit catalog convention `<domain>.<verb_object>`.
		// The domain is the resource kind (runtime / pool). F-24.3.7.
		r.emit(req.Context(), principal, string(kind)+".tenant_access_revoked", resource,
			map[string]any{"tenantId": tenantID})
		w.WriteHeader(http.StatusNoContent)
	}
}
