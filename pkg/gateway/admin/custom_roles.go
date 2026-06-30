// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
)

// CustomRolePayload is the §10.2 / §15.1 admin custom-role wire shape.
type CustomRolePayload struct {
	Name        string            `json:"name"`
	Permissions []auth.Permission `json:"permissions,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	UpdatedAt   string            `json:"updatedAt,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. List and GET responses carry it so a client can
	// supply it as the If-Match header on a later PUT.
	// spec: §15.1 lines 1207-1209.
	ETag string `json:"etag,omitempty"`
}

// UpdateCustomRoleRequest is the §15.1 PUT body — a custom-role update
// replaces the permission set.
type UpdateCustomRoleRequest struct {
	Permissions []auth.Permission `json:"permissions,omitempty"`
}

func fromCustomRole(r customrolestore.CustomRole) CustomRolePayload {
	return CustomRolePayload{
		Name:        r.Name,
		Permissions: r.Permissions,
		CreatedAt:   rfc3339Nano(r.CreatedAt),
		UpdatedAt:   rfc3339Nano(r.UpdatedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version,
		// carried per-item on list responses and in the GET header.
		ETag: formatETag(r.Version),
	}
}

// WithCustomRoles wires the §15.1 custom-role CRUD handlers onto the
// Router.
func (r *Router) WithCustomRoles(s customrolestore.Store) *Router {
	r.customRoles = s
	return r
}

// authorizeTenantPath resolves the tenant id from the request path
// against the caller. A platform-admin may target any tenant; every
// other authenticated principal is confined to its own tenant, and a
// path naming a different tenant is rejected rather than silently
// retargeted. Authorization for the operation itself is the route
// gate's responsibility.
func authorizeTenantPath(req *http.Request, pathTenantID string) (string, error) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return "", errors.New("not authenticated")
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		return pathTenantID, nil
	}
	if p.TenantID != pathTenantID {
		return "", errors.New("caller may only manage their own tenant")
	}
	return pathTenantID, nil
}

func (r *Router) handleCreateCustomRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	var body CustomRolePayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	role := customrolestore.CustomRole{
		TenantID:    tenant,
		Name:        body.Name,
		Permissions: body.Permissions,
		CreatedAt:   r.clock(),
	}
	role.UpdatedAt = role.CreatedAt
	if err := r.customRoles.Create(req.Context(), role); err != nil {
		if errors.Is(err, customrolestore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
				"custom role with this name already exists in tenant", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.customRoles.Get(req.Context(), tenant, body.Name)
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.custom_role.created", body.Name,
		map[string]any{"tenantId": tenant})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromCustomRole(stored))
}

func (r *Router) handleListCustomRoles(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	rows, err := r.customRoles.List(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]CustomRolePayload, 0, len(rows))
	for _, role := range rows {
		out = append(out, fromCustomRole(role))
	}
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x CustomRolePayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.Name, x.Name
			case "updated_at":
				return x.UpdatedAt, x.Name
			default:
				return x.CreatedAt, x.Name
			}
		})
}

func (r *Router) handleGetCustomRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.customRoles.Get(req.Context(), tenant, req.PathValue("name"))
	if err != nil {
		if errors.Is(err, customrolestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "custom role not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 1209 — GET responses for an admin resource carry the
	// ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCustomRole(row))
}

func (r *Router) handleUpdateCustomRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	name := req.PathValue("name")
	var body UpdateCustomRoleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	// Resolve the current role first so the §15.1 If-Match precondition
	// reads the stored version; a missing role 404s ahead of the
	// precondition.
	current, gerr := r.customRoles.Get(req.Context(), tenant, name)
	if gerr != nil {
		if errors.Is(gerr, customrolestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "custom role not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match. The
	// role's entity tag is its version; enforce the optimistic-concurrency
	// precondition before applying the mutation.
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	updated, err := r.customRoles.Update(req.Context(), tenant, name, func(role *customrolestore.CustomRole) error {
		role.Permissions = body.Permissions
		return nil
	})
	if err != nil {
		if errors.Is(err, customrolestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "custom role not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.custom_role.updated", name,
		map[string]any{"tenantId": tenant})
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCustomRole(updated))
}

// customRoleUserDependents builds the §15.1 `details.dependents` entry
// for users in the tenant assigned the role, returning true when at
// least one such user exists. It is a no-op when the user store is not
// wired.
func (r *Router) customRoleUserDependents(req *http.Request, tenant, name string) (map[string]any, bool) {
	if r.users == nil {
		return nil, false
	}
	rows, err := r.users.List(req.Context(), tenant, userstore.ListFilter{})
	if err != nil {
		return nil, false
	}
	var ids []string
	for _, u := range rows {
		for _, role := range u.Roles {
			if string(role) == name {
				ids = append(ids, u.Subject)
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	entry := map[string]any{"type": "user", "count": len(ids)}
	if len(ids) > 20 {
		entry["ids"] = ids[:20]
		entry["truncated"] = true
	} else {
		entry["ids"] = ids
	}
	return entry, true
}

func (r *Router) handleDeleteCustomRole(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	name := req.PathValue("name")
	// §15.1 deletion guard: a custom role assigned to any user cannot
	// be deleted.
	if dep, ok := r.customRoleUserDependents(req, tenant, name); ok {
		writeError(w, http.StatusConflict, "RESOURCE_HAS_DEPENDENTS",
			"custom role is assigned to one or more users",
			map[string]any{"dependents": []map[string]any{dep}})
		return
	}
	// Resolve the current role so the §15.1 DELETE If-Match precondition can
	// compare against its version; a missing role 404s.
	current, gerr := r.customRoles.Get(req.Context(), tenant, name)
	if gerr != nil {
		if errors.Is(gerr, customrolestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "custom role not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412 ETAG_MISMATCH, an absent header proceeds.
	if !enforceIfMatchIfPresent(w, req, current.Version) {
		return
	}
	if err := r.customRoles.Delete(req.Context(), tenant, name); err != nil {
		if errors.Is(err, customrolestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "custom role not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.custom_role.deleted", name,
		map[string]any{"tenantId": tenant})
	w.WriteHeader(http.StatusNoContent)
}
