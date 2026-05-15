// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
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

// authorizedTenantForUser enforces the §10.2 RBAC scoping rules for
// user-management calls: platform-admin sees every tenant;
// tenant-admin is constrained to its own tenant. Returns the tenant
// the operation should target and a nil error on success.
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
	if p.HasRole(auth.RoleTenantAdmin) {
		return p.TenantID, req, nil
	}
	return "", req, errors.New("not authorized")
}

// requireUserAdmin gates user-management endpoints. Platform-admin
// and tenant-admin can manage users (per §11.4 admin endpoints);
// other roles cannot.
func (r *Router) requireUserAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		principal, ok := authmw.FromContext(req.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"endpoint requires authentication", nil)
			return
		}
		if !principal.HasRole(auth.RolePlatformAdmin) && !principal.HasRole(auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"user management requires platform-admin or tenant-admin", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
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
		"tenantId": tenant,
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
