// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
)

// §10.2 session-endpoint authorization.
//
// The §10.2 RBAC permission matrix splits the session surface into two
// operation categories:
//
//   - "Create / cancel own sessions" — the manage_own_sessions
//     permission. Held by platform-admin, tenant-admin, and user; not
//     held by tenant-viewer or billing-viewer. Every state-mutating
//     session endpoint (create, start, finalize, interrupt, terminate,
//     resume, delete, derive, replay, upload, messages,
//     extend-retention, the tool-use approve/deny and elicitation
//     respond/dismiss control calls) is gated on it. The eval
//     submission endpoint is gated separately on the §10.7
//     session:eval:write capability permission (F-10.7.4).
//   - "Read own session history" — the read_own_sessions permission.
//     Held by platform-admin, tenant-admin, tenant-viewer, and user;
//     not held by billing-viewer. Every session read endpoint (get,
//     list, transcript, tree, the event stream) is gated on it.
//
// A tenant custom role conveys whatever subset of these the tenant
// defined; the gate resolves custom roles against the §10.2 custom-role
// registry so a custom role that grants the permission is admitted.
//
// Enforcement posture: the gate fails closed for an authenticated
// caller whose roles do not grant the permission — a tenant-viewer or
// billing-viewer token presented to a session-mutating endpoint
// receives 403 FORBIDDEN. A request that carries no authenticated
// principal is admitted: the minimal gateway runs without an OIDC
// provider (single-tenant dev mode, the X-Lenny-Tenant-ID dev header,
// pre-RBAC service tokens), and §10.2 RBAC governs callers whose token
// actually carries roles. A caller that does authenticate with a role
// is always held to the matrix.
//
// Multi-tenant fail-closed (F-10.2.4): when the server was constructed
// with `MultiTenant=true` (mirroring auth.multiTenant), an authenticated
// principal that carries no roles is rejected even though it has a
// validated tenant claim. The §10.2 matrix is unconditional in
// multi-tenant deployments, so a Bearer JWT whose `roles` claim is
// absent or empty cannot exercise any session permission. Single-tenant
// deployments retain the historical fall-through so a `lenny up`-style
// no-OIDC posture, the X-Lenny-Tenant-ID dev-header path, and pre-RBAC
// service tokens still reach the handler.
// spec: §10.2 lines 256–264.

// requireSessionPermission wraps a session handler with the §10.2
// permission check for perm. It returns the http.HandlerFunc the mux
// registers in place of the bare handler.
func (s *Server) requireSessionPermission(perm auth.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sessionPermissionGranted(r, perm) {
			s.writeError(w, http.StatusForbidden, "FORBIDDEN",
				"this endpoint requires the "+string(perm)+" permission", nil)
			return
		}
		next(w, r)
	}
}

// sessionPermissionGranted reports whether the request's caller is
// authorized for a §10.2 operation requiring perm. A request with no
// authenticated principal, or a principal that carries no roles, is
// authorized (the minimal gateway's no-OIDC dev posture). A principal
// that carries roles is authorized only when one of its roles —
// built-in or tenant custom — grants perm.
func (s *Server) sessionPermissionGranted(r *http.Request, perm auth.Permission) bool {
	p, ok := getPrincipal(r)
	if !ok {
		// No authenticated principal on the request: the no-OIDC dev
		// posture admits. The chain's RequireAuth gate already rejected
		// missing-credential requests when the deployment expects auth.
		return true
	}
	if len(p.Roles) == 0 {
		// spec: §10.2 lines 256–264. Multi-tenant deployments fail
		// closed for an authenticated principal with an empty roles
		// claim — the permission matrix is unconditional and a
		// no-role caller is outside every row. Single-tenant
		// deployments retain the historical fall-through (single-
		// tenant dev mode, dev-header transport, pre-RBAC service
		// tokens). F-10.2.4.
		return !s.multiTenant
	}
	if auth.RolesGrant(p.Roles, perm) {
		return true
	}
	// A non-built-in role name is a tenant custom role; resolve it
	// against the §10.2 custom-role registry. With no registry wired,
	// only built-in roles are consulted (fail closed for the unknown
	// custom role).
	if s.customRoles == nil {
		return false
	}
	for _, role := range p.Roles {
		if role.IsValid() {
			continue
		}
		cr, err := s.customRoles.Get(r.Context(), p.TenantID, string(role))
		if err != nil {
			continue
		}
		for _, granted := range cr.Permissions {
			if granted == perm {
				return true
			}
		}
	}
	return false
}
