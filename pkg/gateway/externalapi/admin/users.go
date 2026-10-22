// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// UserPayload is the §15.1 admin-user wire shape.
type UserPayload struct {
	Subject     string      `json:"subject"`
	TenantID    string      `json:"tenantId"`
	Email       string      `json:"email,omitempty"`
	DisplayName string      `json:"displayName,omitempty"`
	Roles       []auth.Role `json:"roles,omitempty"`
	Disabled    bool        `json:"disabled"`
	CreatedAt   string      `json:"createdAt,omitempty"`
	UpdatedAt   string      `json:"updatedAt,omitempty"`
	DeletedAt   string      `json:"deletedAt,omitempty"`
	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. A list consumer reads it per item to supply
	// If-Match on a later PUT without a follow-up GET. spec: §15.1.
	ETag string `json:"etag,omitempty"`
}

// UpdateUserRequest is the §15.1 PUT body.
type UpdateUserRequest struct {
	Email       *string      `json:"email,omitempty"`
	DisplayName *string      `json:"displayName,omitempty"`
	Roles       *[]auth.Role `json:"roles,omitempty"`
	Disabled    *bool        `json:"disabled,omitempty"`
}

// §11.4 user-invalidation modes, in ascending severity. soft_disable
// denies new sessions; hard_disable also blocks new delegated tasks;
// full_revoke additionally terminates active sessions and denies
// reconnects. soft_disable sets the disabled flag; hard_disable and
// full_revoke also raise the deleted_at tombstone.
const (
	InvalidateSoftDisable = "soft_disable"
	InvalidateHardDisable = "hard_disable"
	InvalidateFullRevoke  = "full_revoke"
)

// InvalidateUserRequest is the §11.4 POST
// /v1/admin/users/{userId}/invalidate body.
type InvalidateUserRequest struct {
	TenantID string `json:"tenantId,omitempty"`
	Mode     string `json:"mode"`
	Reason   string `json:"reason,omitempty"`
}

// fromUser maps a stored row to the wire payload.
func fromUser(u userstore.User) UserPayload {
	return UserPayload{
		Subject:     u.Subject,
		TenantID:    u.TenantID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Roles:       append([]auth.Role(nil), u.Roles...),
		Disabled:    u.Disabled,
		CreatedAt:   rfc3339Nano(u.CreatedAt),
		UpdatedAt:   rfc3339Nano(u.UpdatedAt),
		DeletedAt:   rfc3339Nano(u.DeletedAt),
		// spec: §15.1 — the ETag is the quoted decimal version.
		ETag: formatETag(u.Version),
	}
}

// WithUsers wires the §15.1 admin user CRUD handlers onto the Router.
func (r *Router) WithUsers(s userstore.Store) *Router {
	r.users = s
	return r
}

// WithInteractions wires the §9.2 interaction store onto the Router.
// It backs the §11.4 full_revoke step that dismisses a revoked user's
// pending elicitations.
func (r *Router) WithInteractions(s interactionstore.Store) *Router {
	r.interactions = s
	return r
}

// authorizedTenantForUser resolves which tenant a tenant-scoped admin
// call operates on. A platform-admin names the tenant explicitly; every
// other authenticated principal operates within its own tenant.
// Authorization for the operation itself is the route gate's
// responsibility (requireUserAdmin and the requireTenantResourceAdmin
// family); this resolver does not re-decide it.
func (r *Router) authorizedTenantForUser(req *http.Request, requestedTenant string) (string, *http.Request, error) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return "", req, errors.New("not authenticated")
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		if requestedTenant == "" {
			return "", req, errors.New("platform-admin requests must specify tenantId")
		}
		return requestedTenant, req, nil
	}
	return p.TenantID, req, nil
}

// principalGrantsPermission reports whether the principal holds a role
// — built-in or tenant custom — that grants perm. A built-in role
// resolves through auth.RolePermissions; a custom role resolves through
// the tenant custom-role registry. With no registry wired, only
// built-in roles are consulted.
func (r *Router) principalGrantsPermission(ctx context.Context, p authmw.Principal, perm auth.Permission) bool {
	if auth.RolesGrant(p.Roles, perm) {
		return true
	}
	if r.customRoles == nil {
		return false
	}
	for _, role := range p.Roles {
		if role.IsValid() {
			continue
		}
		cr, err := r.customRoles.Get(ctx, p.TenantID, string(role))
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

// requireUserAdmin gates the §11.4 / §15.1 user-management endpoints on
// the §10.2 manage-users permission. platform-admin and tenant-admin
// hold it through their permission-matrix rows; a tenant custom role
// holds it when its permission set includes manage_users.
func (r *Router) requireUserAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		principal, ok := authmw.FromContext(req.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"endpoint requires authentication", nil)
			return
		}
		if !r.principalGrantsPermission(req.Context(), principal, auth.PermManageUsers) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"user management requires the manage-users permission", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// validateRoleNames rejects a role assignment that names something
// that is neither a built-in §10.2 role nor an existing custom role
// in the tenant. The userstore validates role-name syntax only; the
// custom-role registry is keyed by tenant and is reachable from the
// admin layer, so existence is checked here. When the registry is
// not wired, only built-in roles are accepted.
func (r *Router) validateRoleNames(ctx context.Context, tenant string, roles []auth.Role) error {
	for _, role := range roles {
		if role.IsValid() {
			continue
		}
		if r.customRoles == nil {
			return errors.New("role " + string(role) + " is not a recognised §10.2 role")
		}
		if _, err := r.customRoles.Get(ctx, tenant, string(role)); err != nil {
			return errors.New("role " + string(role) +
				" is not a built-in role or a custom role in this tenant")
		}
	}
	return nil
}

func (r *Router) handleCreateUser(w http.ResponseWriter, req *http.Request) {
	var body UserPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	body.TenantID = tenant
	if err := userstore.ValidateSubject(body.Subject); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "subject"})
		return
	}
	// §10.2: tenant-admin cannot grant platform-admin.
	principal, _ := authmw.FromContext(req.Context())
	if !principal.HasRole(auth.RolePlatformAdmin) {
		for _, role := range body.Roles {
			if role == auth.RolePlatformAdmin {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"only platform-admin can grant platform-admin role", nil)
				return
			}
		}
	}
	if err := r.validateRoleNames(req.Context(), tenant, body.Roles); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "roles"})
		return
	}

	u := userstore.User{
		Subject:     body.Subject,
		TenantID:    body.TenantID,
		Email:       body.Email,
		DisplayName: body.DisplayName,
		Roles:       body.Roles,
		// spec: §10.2 — a row created through the admin user
		// surface is a platform-managed record whose Roles override the
		// OIDC claim (including the empty downgrade). Mark the assignment
		// present so the §15.1 role resolver honors it; only the
		// dedicated `DELETE .../role` path clears it.
		RoleAssigned: true,
		Disabled:     body.Disabled,
		CreatedAt:    r.clock(),
	}
	u.UpdatedAt = u.CreatedAt
	if err := r.users.Create(req.Context(), u); err != nil {
		if errors.Is(err, userstore.ErrAlreadyExists) {
			// spec: §15.1 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
				"user with this subject already exists in tenant", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.users.Get(req.Context(), body.TenantID, body.Subject)
	r.emit(req.Context(), principal, "admin.user.created", body.Subject, map[string]any{
		"tenantId": body.TenantID,
		"roles":    body.Roles,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromUser(stored))
}

func (r *Router) handleListUsers(w http.ResponseWriter, req *http.Request) {
	requestedTenant := req.URL.Query().Get("tenantId")
	tenant, _, err := r.authorizedTenantForUser(req, requestedTenant)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	filter := userstore.ListFilter{
		IncludeDeleted:  req.URL.Query().Get("includeDeleted") == "true",
		IncludeDisabled: req.URL.Query().Get("includeDisabled") == "true",
	}
	rows, err := r.users.List(req.Context(), tenant, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]UserPayload, 0, len(rows))
	for _, u := range rows {
		out = append(out, fromUser(u))
	}
	// spec: §15.1 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x UserPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.Subject, x.Subject
			case "updated_at":
				return x.UpdatedAt, x.Subject
			default:
				return x.CreatedAt, x.Subject
			}
		})
}

func (r *Router) handleGetUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("userId")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.users.Get(req.Context(), tenant, subject)
	if err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 — GET responses for an admin resource carry the
	// ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromUser(row))
}

func (r *Router) handleUpdateUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("userId")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	var body UpdateUserRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	if body.Roles != nil && !principal.HasRole(auth.RolePlatformAdmin) {
		for _, role := range *body.Roles {
			if role == auth.RolePlatformAdmin {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"only platform-admin can grant platform-admin role", nil)
				return
			}
		}
	}
	if body.Roles != nil {
		if err := r.validateRoleNames(req.Context(), tenant, *body.Roles); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "roles"})
			return
		}
	}
	// spec: §15.1 — every admin PUT requires If-Match.
	// Resolve the current user so the entity tag (its version) is known
	// before applying the mutation; a missing user 404s ahead of the
	// precondition.
	current, gerr := r.users.Get(req.Context(), tenant, subject)
	if gerr != nil {
		if errors.Is(gerr, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	updated, err := r.users.Update(req.Context(), tenant, subject, func(u *userstore.User) error {
		if body.Email != nil {
			u.Email = *body.Email
		}
		if body.DisplayName != nil {
			u.DisplayName = *body.DisplayName
		}
		if body.Roles != nil {
			u.Roles = *body.Roles
			// spec: §10.2 — a role change through the generic user
			// surface (re)establishes the platform-managed override.
			u.RoleAssigned = true
		}
		if body.Disabled != nil {
			u.Disabled = *body.Disabled
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "admin.user.updated", subject, map[string]any{
		"tenantId":      tenant,
		"changedFields": changedUserFields(body),
	})
	// spec: §15.1 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromUser(updated))
}

func (r *Router) handleDeleteUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("userId")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// spec: §15.1 — DELETE honours If-Match when present. Resolve
	// the current user so the precondition reads the stored entity tag; a
	// missing user 404s ahead of the precondition.
	current, gerr := r.users.Get(req.Context(), tenant, subject)
	if gerr != nil {
		if errors.Is(gerr, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	if !enforceIfMatchIfPresent(w, req, current.Version) {
		return
	}
	if err := r.users.SoftDelete(req.Context(), tenant, subject, r.clock()); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.user.soft_deleted", subject, map[string]any{
		"tenantId": tenant,
	})
	w.WriteHeader(http.StatusNoContent)
}

// RevokeHook is the session server's side of the §11.4 full_revoke cancel
// writes and the pre-running reclaim. The session server satisfies it.
//
//   - BeginTerminal opens the session's terminal claim on this replica before
//     the cancel write and returns the idempotent function that ends it.
//     While the claim is open the replica's coordination Sweeper and resume
//     tail cannot release the cancelled session's binding, so a binding
//     release never precedes the pre-running reclaim.
//   - AfterRevokeWrite runs right after each write that cancelled a session:
//     it cancels a same-replica resume attempt and drains the session's inbox
//     and DLQ.
//   - ReclaimRevokedPreRunning reclaims the pod or the slot a cancelled
//     session that never started claimed, unless this replica holds the
//     session's binding.
//
// spec: §7.3 (Retry and Resume), §11.4 (User Invalidation), §10.1.5 (Stale
// Replica Behavior)
type RevokeHook interface {
	BeginTerminal(sessionID string) (end func())
	AfterRevokeWrite(ctx context.Context, row sessionstore.Session)
	ReclaimRevokedPreRunning(ctx context.Context, from session.State, row sessionstore.Session)
}

// WithRevokeHook wires the full_revoke per-session hook. Nil leaves it unset.
func (r *Router) WithRevokeHook(h RevokeHook) *Router {
	r.revoked = h
	return r
}

// RevokedSession is one session the full_revoke cancel writes cancelled: the
// updated row, the pre-revoke state read on the locked row, and the end
// function of the terminal claim opened before the write. EndClaim is
// idempotent and never nil.
type RevokedSession struct {
	Row      sessionstore.Session
	From     session.State
	EndClaim func()
}

// revokedIDs returns the session identifiers of revoked, in order.
func revokedIDs(revoked []RevokedSession) []string {
	ids := make([]string, 0, len(revoked))
	for _, rs := range revoked {
		ids = append(ids, rs.Row.ID)
	}
	return ids
}

// endRevokeClaims ends every terminal claim the cancel writes opened. It is
// safe to call more than once.
func endRevokeClaims(revoked []RevokedSession) {
	for _, rs := range revoked {
		rs.EndClaim()
	}
}

// terminateUserSessions runs the full_revoke lease step and the cancel
// writes. It lists the user's sessions and collects the non-terminal ones,
// returns every credential lease they hold through the lease revoker before
// any cancel write, and then cancels each listed session in
// turn (cancelForRevoke). It returns the sessions it cancelled, each with
// its terminal claim still open, and the number of leases the lease step
// enumerated.
//
// The lease step precedes the first cancel write, as §11.4 step 1 requires.
//
// On a store error it returns the sessions cancelled so far with the error,
// and the handler runs the fan-out for them before it answers the error.
//
// spec: §11.4 (User Invalidation); §7.2 (Interactive Session Model).
func (r *Router) terminateUserSessions(ctx context.Context, tenantID, userID string) ([]RevokedSession, int, error) {
	// spec: §11.4 — narrow the SessionStore lookup to the
	// invalidation subject. The Postgres-backed store pushes the filter
	// to `idx_sessions_tenant_user` so a tenant with many sessions does
	// not scan tenant-wide on every full_revoke.
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID})
	if err != nil {
		return nil, 0, err
	}
	var live []sessionstore.Session
	for _, row := range rows {
		if !session.IsTerminal(row.State) {
			live = append(live, row)
		}
	}
	// spec: §11.4 step 1 — the leases are returned before the first cancel
	// write.
	leasesRevoked := 0
	if r.userLeases != nil && len(live) > 0 {
		ids := make([]string, 0, len(live))
		for _, row := range live {
			ids = append(ids, row.ID)
		}
		leasesRevoked = r.userLeases.RevokeUserLeases(tenantID, userID, ids)
	}
	var revoked []RevokedSession
	for _, row := range live {
		rs, ok, err := r.cancelForRevoke(ctx, tenantID, row.ID)
		if err != nil {
			return revoked, leasesRevoked, err
		}
		if ok {
			revoked = append(revoked, rs)
		}
	}
	return revoked, leasesRevoked, nil
}

// cancelForRevoke is one full_revoke cancel write. It opens the session's
// terminal claim, then cancels the row when the locked row is still
// non-terminal, recording the Terminated fact and the pre-revoke state, and
// then runs AfterRevokeWrite. A write that does not apply or returns an error
// ends the claim at once; a write that applied returns it open in the
// RevokedSession, and the fan-out ends it after the pre-running reclaim.
// This writer runs no post-terminal hook: the fan-out releases each
// cancelled session's binding.
//
// spec: §11.4 (User Invalidation); §7.2 (Interactive Session Model).
func (r *Router) cancelForRevoke(ctx context.Context, tenantID, id string) (RevokedSession, bool, error) {
	end := func() {}
	if r.revoked != nil {
		end = r.revoked.BeginTerminal(id)
	}
	now := r.clock()
	var from session.State
	updated, err := r.sessions.Update(ctx, tenantID, id, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return sessionstore.ErrSuperseded
		}
		from = s.State
		s.State = session.StateCancelled
		sessionstore.MarkTerminated(s, from, now)
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		end()
		return RevokedSession{}, false, nil
	}
	if err != nil {
		end()
		return RevokedSession{}, false, err
	}
	if r.revoked != nil {
		r.revoked.AfterRevokeWrite(ctx, updated)
	}
	return RevokedSession{Row: updated, From: from, EndClaim: end}, true, nil
}

// handleInvalidateUser implements POST /v1/admin/users/{userId}/invalidate
// — the §11.4 three-tier user invalidation. soft_disable sets the
// disabled flag so the user is denied new sessions; hard_disable and
// full_revoke also raise the deleted_at tombstone so the user is
// blocked from delegated tasks. full_revoke additionally runs the
// §11.4 propagation: it returns the §4.9 credential leases the user's
// non-terminal sessions hold, transitions those sessions to `cancelled` in
// the SessionStore, reclaims the pod or slot of each
// cancelled session that never started, releases the pod bindings of the
// cancelled sessions, invalidates the user's §13.3 cached auth, and
// dismisses the user's pending elicitations. The pod, lease, and token
// steps run only when their gateway dependencies are wired; each is
// best-effort and a partial propagation is reported rather than failing
// the request.
func (r *Router) handleInvalidateUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("userId")
	var body InvalidateUserRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	switch body.Mode {
	case InvalidateSoftDisable, InvalidateHardDisable, InvalidateFullRevoke:
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"mode must be one of soft_disable, hard_disable, or full_revoke",
			map[string]any{"field": "mode"})
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	at := r.clock()
	updated, err := r.users.Update(req.Context(), tenant, subject, func(u *userstore.User) error {
		u.Disabled = true
		if body.Mode != InvalidateSoftDisable && u.DeletedAt.IsZero() {
			u.DeletedAt = at
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §11.4 full_revoke: return the §4.9 leases of the user's active
	// sessions, cancel those sessions in the SessionStore,
	// release their pods, invalidate the user's §13.3 cached auth, and
	// dismiss their pending elicitations. soft_disable and hard_disable
	// leave running sessions alone — they only gate new work.
	var (
		revoked               []RevokedSession
		leasesRevoked         int
		elicitationsDismissed int
		fanOut                fullRevokeOutcome
	)
	// Every terminal claim the cancel writes opened ends on every return
	// path; the fan-out ends them earlier, and the end is idempotent.
	defer func() { endRevokeClaims(revoked) }()
	if body.Mode == InvalidateFullRevoke {
		if r.sessions != nil {
			revoked, leasesRevoked, err = r.terminateUserSessions(req.Context(), tenant, subject)
			if err != nil {
				// spec: §11.4 — the sessions already cancelled still get
				// their reclaim, binding release, and cached-auth
				// invalidation before the error is answered.
				r.runFullRevokeFanOut(req.Context(), tenant, subject, body.Reason, revoked)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"session termination failed: "+err.Error(), nil)
				return
			}
		}
		// Pre-running reclaim, binding release, and cached-auth
		// invalidation. Each step is best-effort and a partial result is
		// reported, so the fan-out never fails the request.
		fanOut = r.runFullRevokeFanOut(req.Context(), tenant, subject, body.Reason, revoked)
		// spec: §11.4 (Note) — the reported lease count is the number of
		// leases step 1 returns.
		fanOut.leasesRevoked = leasesRevoked
		if r.interactions != nil {
			elicitationsDismissed, err = r.interactions.DismissByUser(req.Context(), tenant, subject)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"elicitation dismissal failed: "+err.Error(), nil)
				return
			}
		}
	}
	sessionsTerminated := len(revoked)
	detail := map[string]any{
		"tenantId":                  tenant,
		"mode":                      body.Mode,
		"reason":                    body.Reason,
		"sessionsTerminated":        sessionsTerminated,
		"podsTerminated":            fanOut.podsTerminated,
		"leasesRevoked":             fanOut.leasesRevoked,
		"tokensRevoked":             fanOut.tokensRevoked,
		"playgroundSessionsRevoked": fanOut.playgroundSessionsRevoked,
		"elicitationsDismissed":     elicitationsDismissed,
	}
	if len(fanOut.podTerminateFailed) > 0 {
		detail["podTerminationFailures"] = fanOut.podTerminateFailed
	}
	if fanOut.tokenRevokeError != "" {
		detail["tokenRevocationError"] = fanOut.tokenRevokeError
	}
	if fanOut.playgroundRevokeError != "" {
		detail["playgroundRevocationError"] = fanOut.playgroundRevokeError
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.user.invalidated", subject, detail)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"subject":                   subject,
		"tenantId":                  tenant,
		"mode":                      body.Mode,
		"sessionsTerminated":        sessionsTerminated,
		"podsTerminated":            fanOut.podsTerminated,
		"leasesRevoked":             fanOut.leasesRevoked,
		"tokensRevoked":             fanOut.tokensRevoked,
		"playgroundSessionsRevoked": fanOut.playgroundSessionsRevoked,
		"elicitationsDismissed":     elicitationsDismissed,
		"user":                      fromUser(updated),
	}
	if len(fanOut.podTerminateFailed) > 0 {
		resp["podTerminationFailures"] = fanOut.podTerminateFailed
	}
	if fanOut.tokenRevokeError != "" {
		resp["tokenRevocationError"] = fanOut.tokenRevokeError
	}
	if fanOut.playgroundRevokeError != "" {
		resp["playgroundRevocationError"] = fanOut.playgroundRevokeError
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func changedUserFields(b UpdateUserRequest) []string {
	var out []string
	if b.Email != nil {
		out = append(out, "email")
	}
	if b.DisplayName != nil {
		out = append(out, "displayName")
	}
	if b.Roles != nil {
		out = append(out, "roles")
	}
	if b.Disabled != nil {
		out = append(out, "disabled")
	}
	return out
}
