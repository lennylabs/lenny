// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"net/http"

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

// handleClearExtensionDenial implements §15.1 line 868
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial.
// It clears the §8.6 extension-denied flag on the named subtree,
// immediately re-enabling extension requests regardless of the
// rejection cool-off window. RBAC (platform-admin or tenant-admin) is
// enforced by the requireTenantResourceAdmin gate at registration.
// spec: §8.6 line 735; §15.1 line 868
func (r *Router) handleClearExtensionDenial(w http.ResponseWriter, req *http.Request) {
	rootSessionID := req.PathValue("rootSessionId")
	sessionID := req.PathValue("sessionId")
	if rootSessionID == "" || sessionID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"rootSessionId and sessionId path segments are required", nil)
		return
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
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
