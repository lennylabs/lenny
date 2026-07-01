// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// eventUserRoleAssigned / eventUserRoleRemoved are the §15.1 lines
// 827-828 platform-managed role-assignment audit event types. The admin
// emit path serializes event-type strings (see r.emit), so they are
// plain constants. spec: §15.1 lines 827-828.
const (
	eventUserRoleAssigned = "user.role_assigned"
	eventUserRoleRemoved  = "user.role_removed"
)

// tenantUserRolePayload is the §15.1 line 826 tenant-user wire shape:
// the user id and its platform-managed role assignment with provenance.
// `role` is empty when the user has no platform-managed assignment (the
// state in which the OIDC claim is authoritative). createdAt / updatedAt
// back the §15.1 list sort and are not serialized.
type tenantUserRolePayload struct {
	UserID     string `json:"user_id"`
	Role       string `json:"role,omitempty"`
	AssignedAt string `json:"assignedAt,omitempty"`
	AssignedBy string `json:"assignedBy,omitempty"`
	// ETag lets a list consumer supply If-Match on a later role PUT
	// without a follow-up GET. spec: §15.1 line 1209.
	ETag      string `json:"etag,omitempty"`
	createdAt string
	updatedAt string
}

// assignedRole projects the single §15.1 line 826 platform-managed role.
// The dedicated role surface assigns exactly one role; a user given
// several roles through the multi-role `/v1/admin/users` surface projects
// its first. A row with no assignment projects the empty string.
func assignedRole(u userstore.User) string {
	if !u.RoleAssigned || len(u.Roles) == 0 {
		return ""
	}
	return string(u.Roles[0])
}

func tenantUserRoleFromUser(u userstore.User) tenantUserRolePayload {
	return tenantUserRolePayload{
		UserID:     u.Subject,
		Role:       assignedRole(u),
		AssignedAt: rfc3339Nano(u.RoleAssignedAt),
		AssignedBy: u.RoleAssignedBy,
		ETag:       formatETag(u.Version),
		createdAt:  rfc3339Nano(u.CreatedAt),
		updatedAt:  rfc3339Nano(u.UpdatedAt),
	}
}

// handleListTenantUsers implements GET /v1/admin/tenants/{id}/users — the
// §15.1 line 826 tenant user listing with platform-managed role
// assignments. A tenant-admin caller is scoped to its own tenant by the
// path-tenant resolver; a platform-admin reads any tenant. Soft-deleted
// and disabled users are excluded unless `?includeDeleted=true` /
// `?includeDisabled=true` opt them in. The result is the canonical §15.1
// cursor-paginated envelope. spec: §15.1 line 826.
func (r *Router) handleListTenantUsers(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
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
	out := make([]tenantUserRolePayload, 0, len(rows))
	for _, u := range rows {
		out = append(out, tenantUserRoleFromUser(u))
	}
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x tenantUserRolePayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.UserID, x.UserID
			case "updated_at":
				return x.updatedAt, x.UserID
			default:
				return x.createdAt, x.UserID
			}
		})
}

// putTenantUserRoleRequest is the §15.1 line 827 PUT body.
type putTenantUserRoleRequest struct {
	Role string `json:"role"`
}

// handlePutTenantUserRole implements PUT
// /v1/admin/tenants/{id}/users/{userId}/role — the §15.1 line 827
// platform-managed role assignment. The named role replaces the user's
// platform-managed assignment and takes precedence over the OIDC claim
// (§10.2 line 294). Valid roles are the tenant-scoped built-ins
// (tenant-admin, tenant-viewer, billing-viewer, user) and any custom role
// defined in the tenant RBAC config; platform-admin is rejected because
// it is a platform-spanning role the tenant surface does not grant. The
// user must already be a registered user record (the GET above lists the
// assignable users); a missing record is 404. If-Match is required and
// enforced against the user row version. spec: §15.1 line 827.
func (r *Router) handlePutTenantUserRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	userID := strings.TrimSpace(req.PathValue("userId"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "userId is required", nil)
		return
	}
	var body putTenantUserRoleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	role := auth.Role(strings.TrimSpace(body.Role))
	if role == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "role is required",
			map[string]any{"field": "role"})
		return
	}
	// spec: §15.1 line 827 — the valid-role list is the tenant-scoped set;
	// platform-admin is a platform-spanning role this surface cannot grant.
	if role == auth.RolePlatformAdmin {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"platform-admin cannot be assigned through the tenant role surface",
			map[string]any{"field": "role"})
		return
	}
	// A built-in tenant role or an existing custom role in this tenant.
	if err := r.validateRoleNames(req.Context(), tenant, []auth.Role{role}); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "role"})
		return
	}

	current, gerr := r.users.Get(req.Context(), tenant, userID)
	if gerr != nil {
		if errors.Is(gerr, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 line 827 — the role PUT requires If-Match against the
	// user row's entity tag.
	if !enforceIfMatch(w, req, current.Version) {
		return
	}

	p, _ := authmw.FromContext(req.Context())
	at := r.clock()
	updated, err := r.users.Update(req.Context(), tenant, userID, func(u *userstore.User) error {
		u.Roles = []auth.Role{role}
		u.RoleAssigned = true
		u.RoleAssignedBy = p.Subject
		u.RoleAssignedAt = at
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
	r.emit(req.Context(), p, eventUserRoleAssigned, userID, map[string]any{
		"tenant_id": tenant,
		"user_id":   userID,
		"role":      string(role),
	})
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag.
	w.Header().Set("ETag", formatETag(updated.Version))
	writeJSON(w, http.StatusOK, tenantUserRoleFromUser(updated))
}

// handleDeleteTenantUserRole implements DELETE
// /v1/admin/tenants/{id}/users/{userId}/role — the §15.1 line 828
// removal of the platform-managed role assignment. The user record is
// retained (so the user still lists) but its assignment is cleared, so
// the §10.2 role resolver falls through to the OIDC claim. The endpoint
// is idempotent: removing an absent assignment is a 204 no-op that emits
// no event. If-Match is honoured when present. spec: §15.1 line 828.
func (r *Router) handleDeleteTenantUserRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	userID := strings.TrimSpace(req.PathValue("userId"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "userId is required", nil)
		return
	}
	current, gerr := r.users.Get(req.Context(), tenant, userID)
	if gerr != nil {
		if errors.Is(gerr, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match when present.
	if !enforceIfMatchIfPresent(w, req, current.Version) {
		return
	}
	if !current.RoleAssigned {
		// Idempotent no-op: no assignment to remove. The OIDC claim is
		// already authoritative.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := r.users.Update(req.Context(), tenant, userID, func(u *userstore.User) error {
		u.Roles = nil
		u.RoleAssigned = false
		u.RoleAssignedBy = ""
		u.RoleAssignedAt = time.Time{}
		return nil
	}); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	p, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), p, eventUserRoleRemoved, userID, map[string]any{
		"tenant_id": tenant,
		"user_id":   userID,
	})
	w.WriteHeader(http.StatusNoContent)
}
