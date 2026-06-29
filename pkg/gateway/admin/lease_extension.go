// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// LeaseDenialClearer clears the §8.6 extension-denied flag for a
// delegation subtree. The gateway's leasecontrol budget source
// satisfies it. The §15.1 line 868 admin endpoint
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
// calls it to reset a denied subtree to normal extension behaviour,
// bypassing the rejection cool-off window §8.6 line 734 starts after a
// user denies an extension elicitation.
// spec: §8.6 line 735; §15.1 line 868
type LeaseDenialClearer interface {
	// ClearSubtreeDenial clears the extension-denied flag for the subtree
	// rooted at sessionID inside the delegation tree rooted at
	// rootSessionID. found is false when rootSessionID names no known
	// tree, so the handler answers 404; a non-nil err is a storage
	// failure the handler surfaces as 500.
	ClearSubtreeDenial(ctx context.Context, rootSessionID, sessionID string) (found bool, err error)
}

// WithLeaseDenials wires the §8.6 / §15.1 extension-denial clear handler
// onto the Router. Passing a nil clearer leaves the endpoint
// unregistered (the gateway runs no GatewayControl lease-extension
// control plane), so a request returns 404 from the mux.
// spec: §15.1 line 868
func (r *Router) WithLeaseDenials(c LeaseDenialClearer) *Router {
	r.leaseDenials = c
	return r
}

// WithTenantResolver wires the session-to-tenant resolver onto the
// Router so handleClearExtensionDenial can confine a tenant-admin caller
// to its own tenant before clearing the durable extension-denial row.
// The gateway's leasecontrol budget source satisfies it (its TenantOf
// method maps a tree's root session id to the owning tenant). Passing a
// nil resolver fails closed: a non-platform-admin caller is rejected
// because the owner tenant cannot be resolved (§10.2 confinement).
// spec: §10.2 line 261
func (r *Router) WithTenantResolver(t leasecontrol.TenantResolver) *Router {
	r.tenantResolver = t
	return r
}

// handleClearExtensionDenial implements §15.1 line 868
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial.
// It clears the §8.6 extension-denied flag on the named subtree,
// immediately re-enabling extension requests regardless of the
// rejection cool-off window. RBAC (platform-admin or tenant-admin) is
// enforced by the requireTenantResourceAdmin gate at registration, and a
// non-platform-admin caller is additionally confined to its own tenant
// here: the tree is keyed by an opaque session UUID, so the role gate
// alone would let a tenant-admin of one tenant clear another tenant's
// denial row. The clear is therefore gated on the resolved tree owner
// before it runs.
// spec: §8.6 line 735; §15.1 line 868
func (r *Router) handleClearExtensionDenial(w http.ResponseWriter, req *http.Request) {
	rootSessionID := req.PathValue("rootSessionId")
	sessionID := req.PathValue("sessionId")
	if rootSessionID == "" || sessionID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"rootSessionId and sessionId path segments are required", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	// Confine a non-platform-admin caller to its own tenant before the
	// durable clear. A platform-admin clears across tenants; every other
	// admin must own the tree. Resolving the tree's tenant first means a
	// foreign tenant-admin is rejected before the row is touched, so the
	// clear cannot leak across tenants (§10.2 line 261). Fail closed when
	// the resolver is unwired: a misconfigured gateway must not reopen the
	// cross-tenant clear, so a non-platform-admin caller is rejected.
	// spec: §10.2 line 261; §15.1 line 869
	if !principal.HasRole(auth.RolePlatformAdmin) {
		if r.tenantResolver == nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"caller may only manage their own tenant", nil)
			return
		}
		ownerTenant, terr := r.tenantResolver.TenantOf(req.Context(), rootSessionID)
		if errors.Is(terr, leasecontrol.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
				"delegation tree not found", nil)
			return
		}
		if terr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", terr.Error(), nil)
			return
		}
		if ownerTenant != principal.TenantID {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"caller may only manage their own tenant", nil)
			return
		}
	}
	found, err := r.leaseDenials.ClearSubtreeDenial(req.Context(), rootSessionID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"delegation tree not found", nil)
		return
	}
	// Audit the bypass: clearing the denial overrides a user's prior
	// rejection of a budget extension, so the §10.6 admin audit records
	// who cleared which subtree. The event rides the admin AuditSink
	// alongside the other admin.* mutations rather than the §16.7 OCSF
	// stream, matching the existing admin-mutation audit pattern.
	r.emit(req.Context(), principal, "admin.delegation.extension_denial_cleared", sessionID, map[string]any{
		"root_session_id": rootSessionID,
		"session_id":      sessionID,
	})
	w.WriteHeader(http.StatusNoContent)
}
