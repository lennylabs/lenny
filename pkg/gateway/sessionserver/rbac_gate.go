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
//     resume, delete, derive, replay, upload, messages, eval,
//     extend-retention, the tool-use approve/deny and elicitation
//     respond/dismiss control calls) is gated on it.
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
// principal, or a principal with no roles, is admitted: the minimal
// gateway runs without an OIDC provider (single-tenant dev mode, the
// X-Lenny-Tenant-ID dev header, pre-RBAC service tokens), and §10.2
// RBAC governs callers whose token actually carries roles. This mirrors
// the requireActiveUser admission rule for principals with no
// user-registry row. A caller that does authenticate with a role is
// always held to the matrix.

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
	if !ok || len(p.Roles) == 0 {
		return true
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
