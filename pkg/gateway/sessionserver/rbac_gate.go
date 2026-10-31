// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
// receives 403 FORBIDDEN. The permission gate admits a request that
// carries no authenticated principal, because the minimal gateway runs
// without an OIDC provider (single-tenant dev mode, the
// X-Lenny-Tenant-ID dev header, pre-RBAC service tokens). It also
// admits a role-less principal in a single-tenant deployment. Neither
// admission reaches a session: the ownership step that follows refuses
// a request with no principal subject with 401 UNAUTHORIZED on every
// {id} route, on list, and on create, and it limits a role-less
// principal to its own sessions (requireSessionAccess,
// SessionAccessible, and ownerFromPrincipal).
//
// Multi-tenant fail-closed (F-10.2.4): when the server was constructed
// with `MultiTenant=true` (mirroring auth.multiTenant), an authenticated
// principal that carries no roles is rejected even though it has a
// validated tenant claim. The §10.2 matrix is unconditional in
// multi-tenant deployments, so a Bearer JWT whose `roles` claim is
// absent or empty cannot exercise any session permission. Single-tenant
// deployments retain the historical fall-through at the permission gate
// only.
// spec: §10.2 (Session ownership).

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
// admitted by the §10.2 permission gate for an operation requiring perm.
// The gate admits a request with no authenticated principal, and a
// role-less principal in a single-tenant deployment; the ownership step
// that follows then refuses a request with no principal subject on every
// {id} route, on list, and on create. A principal that carries roles is
// admitted only when principalHolds reports perm.
//
// spec: §10.2 (Session ownership) rule 7.
func (s *Server) sessionPermissionGranted(r *http.Request, perm auth.Permission) bool {
	p, ok := getPrincipal(r)
	if !ok {
		// No authenticated principal on the request: the permission gate
		// admits, and the ownership step refuses the request with 401.
		// The chain's RequireAuth gate already rejected missing-credential
		// requests when the deployment expects auth.
		return true
	}
	if len(p.Roles) == 0 {
		// spec: §10.2. Multi-tenant deployments fail
		// closed for an authenticated principal with an empty roles
		// claim — the permission matrix is unconditional and a
		// no-role caller is outside every row. Single-tenant
		// deployments retain the historical fall-through (single-
		// tenant dev mode, dev-header transport, pre-RBAC service
		// tokens). F-10.2.4.
		return !s.multiTenant
	}
	return s.principalHolds(r.Context(), p, perm)
}

// principalHolds reports whether a role assigned to p, built-in or tenant
// custom, grants perm. It resolves the built-in roles through RolesGrant
// and every other role name against the §10.2 custom-role registry for
// p.TenantID. It has no role-less fall-through: a principal with no roles
// holds no permission, in every tenancy mode. With no registry wired, only
// built-in roles are consulted, so an unknown custom role fails closed.
//
// spec: §10.2 (Custom roles (tenant-scoped)); §10.2 (Session ownership)
// rule 4.
func (s *Server) principalHolds(ctx context.Context, p authmw.Principal, perm auth.Permission) bool {
	if auth.RolesGrant(p.Roles, perm) {
		return true
	}
	if s.customRoles == nil {
		return false
	}
	for _, role := range p.Roles {
		if role.IsValid() {
			continue
		}
		cr, err := s.customRoles.Get(ctx, p.TenantID, string(role))
		if err != nil {
			continue
		}
		if slices.Contains(cr.Permissions, perm) {
			return true
		}
	}
	return false
}

// SessionAccessible reports whether the principal on ctx may read
// (act == false) or act on (act == true) row. An absent principal or
// an empty Subject is never granted. The owner is granted both. Any
// other principal is granted a read only when principalHolds reports
// PermReadTenantSessions, and is never granted an action.
//
// The read bypass calls principalHolds rather than
// sessionPermissionGranted, because the latter admits a role-less
// principal to every permission in a single-tenant deployment, which
// would let every user of an IdP that sends no role claim read every
// session.
//
// spec: §10.2 (Session ownership) rules 4 and 5
func (s *Server) SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool {
	p, ok := authmw.FromContext(ctx)
	if !ok || p.Subject == "" {
		return false
	}
	if row.UserID == p.Subject {
		return true
	}
	if act {
		return false
	}
	return s.principalHolds(ctx, p, auth.PermReadTenantSessions)
}

// requireSessionAccess wraps a /v1/sessions/{id} handler with the §10.2
// ownership check. It runs inside requireSessionPermission, so a caller
// the permission matrix refuses has already received 403. It refuses a
// request with no principal subject with 401, reads the session row named
// by the path, and calls next only when SessionAccessible grants the
// caller a read (act == false) or an action (act == true) on it. A row the
// caller may not read or act on answers exactly as a missing session does,
// so the refusal does not reveal that the session exists, and next is
// never called on a refusal.
//
// spec: §10.2 (Session ownership) rules 3, 5, 6, and 7.
func (s *Server) requireSessionAccess(act bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireOwner(w, r); !ok {
			return
		}
		row, err := s.accessRow(r.Context(), s.resolveTenant(r), r.PathValue("id"))
		switch {
		case errors.Is(err, sessionstore.ErrNotFound):
			s.writeSessionNotFound(w)
			return
		case err != nil:
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if !s.SessionAccessible(r.Context(), row, act) {
			s.writeSessionNotFound(w)
			return
		}
		next(w, r)
	}
}

// accessRow reads the session row the ownership check decides on. It reads
// the read pool first, bounded by the primary-read bound so a hung
// read-pool query cannot hold the request. A replica row decides alone,
// because a session's owner never changes after creation. On any replica
// error, including sessionstore.ErrNotFound and expiry of the bound, it
// re-reads the row once from the primary under ctx, because the read pool
// can lag a row the primary already holds, such as a session created
// moments earlier.
//
// spec: §10.2 (Session ownership) rule 1; §12.3 (Postgres HA
// Requirements).
func (s *Server) accessRow(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if row, err := s.boundedReplicaGet(ctx, tenantID, id); err == nil {
		return row, nil
	}
	return s.readPrimary(ctx, tenantID, id)
}

// boundedReplicaGet issues one store Get under the primary-read bound. The
// bound scopes this Get only, so the primary re-read and the handler run
// under the request's own context.
//
// spec: §12.3 (Postgres HA Requirements).
func (s *Server) boundedReplicaGet(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionstore.PrimaryReadBound(s.primaryReadTimeout))
	defer cancel()
	return s.store.Get(ctx, tenantID, id)
}

// writeSessionNotFound writes the §15.1 RESOURCE_NOT_FOUND envelope for a
// session that does not exist. The ownership check writes the same body
// for a session the caller may not read or act on.
//
// spec: §10.2 (Session ownership) rule 7; §15.1 (RESOURCE_NOT_FOUND).
func (s *Server) writeSessionNotFound(w http.ResponseWriter) {
	s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
}
