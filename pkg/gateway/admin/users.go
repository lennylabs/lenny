// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
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
// /v1/admin/users/{user_id}/invalidate body.
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
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
		Disabled:    body.Disabled,
		CreatedAt:   r.clock(),
	}
	u.UpdatedAt = u.CreatedAt
	if err := r.users.Create(req.Context(), u); err != nil {
		if errors.Is(err, userstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"users": out})
}

func (r *Router) handleGetUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("user_id")
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromUser(row))
}

func (r *Router) handleUpdateUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("user_id")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	var body UpdateUserRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
	updated, err := r.users.Update(req.Context(), tenant, subject, func(u *userstore.User) error {
		if body.Email != nil {
			u.Email = *body.Email
		}
		if body.DisplayName != nil {
			u.DisplayName = *body.DisplayName
		}
		if body.Roles != nil {
			u.Roles = *body.Roles
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromUser(updated))
}

func (r *Router) handleDeleteUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("user_id")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
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

// terminateUserSessions transitions every non-terminal session owned
// by the user to `cancelled` — the §11.4 full_revoke SessionStore
// step. It returns the IDs of the sessions it transitioned. An
// already-terminal session is left untouched, and a concurrent terminal
// transition is honored. The forced transition mirrors the §8.10
// orphan-cleanup sweep: an administrative termination overrides the
// normal state machine. The returned IDs are the input to the §11.4
// pod-RPC `Terminate` fan-out and the credential-lease revocation: a
// session this step cancelled was non-terminal, so its pod may still be
// running and its leases may still be live.
func (r *Router) terminateUserSessions(ctx context.Context, tenantID, userID string) ([]string, error) {
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var terminated []string
	for _, row := range rows {
		if row.UserID != userID || session.IsTerminal(row.State) {
			continue
		}
		updated, err := r.sessions.Update(ctx, tenantID, row.ID, func(s *sessionstore.Session) error {
			if session.IsTerminal(s.State) {
				return nil
			}
			s.State = session.StateCancelled
			return nil
		})
		if err != nil {
			return terminated, err
		}
		if updated.State == session.StateCancelled {
			terminated = append(terminated, row.ID)
		}
	}
	return terminated, nil
}

// handleInvalidateUser implements POST /v1/admin/users/{user_id}/invalidate
// — the §11.4 three-tier user invalidation. soft_disable sets the
// disabled flag so the user is denied new sessions; hard_disable and
// full_revoke also raise the deleted_at tombstone so the user is
// blocked from delegated tasks. full_revoke additionally runs the
// §11.4 propagation: it transitions the user's non-terminal sessions to
// `cancelled` in the SessionStore, sends the §4.7 `Terminate` RPC to
// the pods hosting those sessions, revokes the §4.9 credential leases
// the sessions hold, invalidates the user's §13.3 cached auth, and
// dismisses the user's pending elicitations. The pod, lease, and token
// steps run only when their gateway dependencies are wired; each is
// best-effort and a partial propagation is reported rather than failing
// the request.
func (r *Router) handleInvalidateUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("user_id")
	var body InvalidateUserRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
	// §11.4 full_revoke: cancel the user's active sessions in the
	// SessionStore, fan the §4.7 Terminate RPC out to their pods, revoke
	// the §4.9 leases those sessions hold, invalidate the user's §13.3
	// cached auth, and dismiss their pending elicitations. soft_disable
	// and hard_disable leave running sessions alone — they only gate
	// new work.
	var (
		liveSessions          []string
		elicitationsDismissed int
		fanOut                fullRevokeOutcome
	)
	if body.Mode == InvalidateFullRevoke {
		if r.sessions != nil {
			liveSessions, err = r.terminateUserSessions(req.Context(), tenant, subject)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"session termination failed: "+err.Error(), nil)
				return
			}
		}
		// Pod-RPC Terminate, credential-lease revocation, and cached-auth
		// invalidation. Each step is best-effort and a partial result is
		// reported, so the fan-out never fails the request.
		fanOut = r.runFullRevokeFanOut(req.Context(), tenant, subject, body.Reason, liveSessions)
		if r.interactions != nil {
			elicitationsDismissed, err = r.interactions.DismissByUser(req.Context(), tenant, subject)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"elicitation dismissal failed: "+err.Error(), nil)
				return
			}
		}
	}
	sessionsTerminated := len(liveSessions)
	detail := map[string]any{
		"tenantId":              tenant,
		"mode":                  body.Mode,
		"reason":                body.Reason,
		"sessionsTerminated":    sessionsTerminated,
		"podsTerminated":        fanOut.podsTerminated,
		"leasesRevoked":         fanOut.leasesRevoked,
		"tokensRevoked":         fanOut.tokensRevoked,
		"elicitationsDismissed": elicitationsDismissed,
	}
	if len(fanOut.podTerminateFailed) > 0 {
		detail["podTerminationFailures"] = fanOut.podTerminateFailed
	}
	if fanOut.tokenRevokeError != "" {
		detail["tokenRevocationError"] = fanOut.tokenRevokeError
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.user.invalidated", subject, detail)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"subject":               subject,
		"tenantId":              tenant,
		"mode":                  body.Mode,
		"sessionsTerminated":    sessionsTerminated,
		"podsTerminated":        fanOut.podsTerminated,
		"leasesRevoked":         fanOut.leasesRevoked,
		"tokensRevoked":         fanOut.tokensRevoked,
		"elicitationsDismissed": elicitationsDismissed,
		"user":                  fromUser(updated),
	}
	if len(fanOut.podTerminateFailed) > 0 {
		resp["podTerminationFailures"] = fanOut.podTerminateFailed
	}
	if fanOut.tokenRevokeError != "" {
		resp["tokenRevocationError"] = fanOut.tokenRevokeError
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
