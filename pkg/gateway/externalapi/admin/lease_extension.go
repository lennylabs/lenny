// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// LeaseDenialClearer clears the §8.6 extension-denied flag for a
// delegation subtree. The gateway's leasecontrol.StoreBudgetSource
// satisfies it. The §15.1 admin endpoint
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
// calls it to reset a denied subtree to normal extension behaviour,
// bypassing the rejection cool-off window §8.6 starts after a
// user denies an extension elicitation.
// spec: §8.6; §15.1
type LeaseDenialClearer interface {
	// ClearSubtreeDenial clears the extension-denied flag for the subtree
	// rooted at sessionID inside the delegation tree rooted at
	// rootSessionID, owned by tenantID. A non-nil err is a storage
	// failure the handler surfaces as 500.
	ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, sessionID string) error
}

// WithLeaseDenials wires the §8.6 / §15.1 extension-denial clear handler
// onto the Router. The route registers only when WithSessions is also
// wired, because the handler resolves the root row before the clear.
// Passing a nil clearer leaves the endpoint unregistered, so a request
// returns 404 from the mux.
// spec: §15.1
func (r *Router) WithLeaseDenials(c LeaseDenialClearer) *Router {
	r.leaseDenials = c
	return r
}

// handleClearExtensionDenial implements §15.1
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial.
// It clears the §8.6 extension-denied flag on the named subtree,
// immediately re-enabling extension requests regardless of the
// rejection cool-off window. RBAC (platform-admin or tenant-admin) is
// enforced by the requireTenantResourceAdmin gate at registration. The
// handler then resolves the root row: a tenant-admin reads it under its
// own tenant, so another tenant's tree is a 404, and a platform-admin
// reads it across tenants. The clear runs under the row's tenant.
// spec: §8.6; §15.1; §24.11
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
	root, err := r.readDenialRoot(req.Context(), principal, rootSessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"delegation tree not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := r.leaseDenials.ClearSubtreeDenial(req.Context(), root.TenantID, rootSessionID, sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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

// readDenialRoot reads the tree's root row. A tenant-admin reads under its
// own tenant, so a cross-tenant id is sessionstore.ErrNotFound. A
// platform-admin reads by id alone, which the §24.11 contract permits on a
// platform-admin-gated path.
// spec: §15.1; §24.11
func (r *Router) readDenialRoot(ctx context.Context, principal authmw.Principal, rootSessionID string) (sessionstore.Session, error) {
	if principal.HasRole(auth.RolePlatformAdmin) {
		return r.sessions.GetByID(ctx, rootSessionID)
	}
	return r.sessions.Get(ctx, principal.TenantID, rootSessionID)
}
