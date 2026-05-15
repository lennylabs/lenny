// SPDX-License-Identifier: MIT

// Package admin implements the §15.1 admin API surface
// (`/v1/admin/*`). Each resource lives in its own handler so the
// surface can be wired piecemeal; the package exports a Router that
// composes the active handlers behind a single platform-admin
// authorization check.
//
// All admin endpoints require the `platform-admin` RBAC role on the
// resolved Principal per §10.2. Non-admin callers receive 403
// FORBIDDEN before the resource-specific handler runs.
package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// Router is the §15.1 admin sub-router. The minimal admin API wires
// only the tenant CRUD endpoints; future commits add users,
// runtimes, pools, connectors, circuit breakers, etc.
type Router struct {
	tenants tenantstore.Store
	clock   func() time.Time
}

// NewRouter returns a Router. Pass nil for `clock` to default to
// time.Now.
func NewRouter(tenants tenantstore.Store, clock func() time.Time) *Router {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Router{tenants: tenants, clock: clock}
}

// Handler returns an http.Handler routing the wired admin endpoints.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	if r.tenants != nil {
		mux.Handle("POST /v1/admin/tenants", r.requireAdmin(http.HandlerFunc(r.handleCreateTenant)))
		mux.Handle("GET /v1/admin/tenants", r.requireAdmin(http.HandlerFunc(r.handleListTenants)))
		mux.Handle("GET /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleGetTenant)))
		mux.Handle("PUT /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleUpdateTenant)))
		mux.Handle("DELETE /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleDeleteTenant)))
	}
	return mux
}

// requireAdmin gates every admin endpoint on the §10.2 platform-admin
// role.
func (r *Router) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		principal, ok := authmw.FromContext(req.Context())
		if !ok || !principal.HasRole(auth.RolePlatformAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"admin endpoint requires the platform-admin role", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// TenantPayload is the §15.1 admin-tenant request/response body.
type TenantPayload struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"displayName,omitempty"`
	ComplianceProfile   string `json:"complianceProfile,omitempty"`
	DataResidencyRegion string `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier       string `json:"workspaceTier,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
	DeletedAt           string `json:"deletedAt,omitempty"`
}

// fromTenant maps a stored row to the wire payload.
func fromTenant(t tenantstore.Tenant) TenantPayload {
	out := TenantPayload{
		ID:                  t.ID,
		DisplayName:         t.DisplayName,
		ComplianceProfile:   t.ComplianceProfile,
		DataResidencyRegion: t.DataResidencyRegion,
		WorkspaceTier:       t.WorkspaceTier,
		CreatedAt:           t.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:           t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !t.DeletedAt.IsZero() {
		out.DeletedAt = t.DeletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// handleCreateTenant implements POST /v1/admin/tenants.
func (r *Router) handleCreateTenant(w http.ResponseWriter, req *http.Request) {
	var body TenantPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required",
			map[string]any{"field": "id"})
		return
	}
	if err := auth.ValidateTenantID(body.ID); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "id"})
		return
	}

	t := tenantstore.Tenant{
		ID:                  body.ID,
		DisplayName:         body.DisplayName,
		ComplianceProfile:   body.ComplianceProfile,
		DataResidencyRegion: body.DataResidencyRegion,
		WorkspaceTier:       body.WorkspaceTier,
		CreatedAt:           r.clock(),
	}
	t.UpdatedAt = t.CreatedAt
	if err := r.tenants.Create(req.Context(), t); err != nil {
		if errors.Is(err, tenantstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"tenant with this id already exists",
				map[string]any{"id": body.ID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	row, _ := r.tenants.Get(req.Context(), body.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromTenant(row))
}

// handleListTenants implements GET /v1/admin/tenants.
func (r *Router) handleListTenants(w http.ResponseWriter, req *http.Request) {
	filter := tenantstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
	}
	rows, err := r.tenants.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]TenantPayload, 0, len(rows))
	for _, t := range rows {
		out = append(out, fromTenant(t))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tenants": out})
}

// handleGetTenant implements GET /v1/admin/tenants/{id}.
func (r *Router) handleGetTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	row, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenant(row))
}

// UpdateTenantRequest is the §15.1 admin-tenant update body. Only
// the fields explicitly present are mutated; omitting a field leaves
// the stored value untouched. Empty-string clears the field.
type UpdateTenantRequest struct {
	DisplayName         *string `json:"displayName,omitempty"`
	ComplianceProfile   *string `json:"complianceProfile,omitempty"`
	DataResidencyRegion *string `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier       *string `json:"workspaceTier,omitempty"`
}

// handleUpdateTenant implements PUT /v1/admin/tenants/{id}.
func (r *Router) handleUpdateTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	var body UpdateTenantRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		if body.DisplayName != nil {
			t.DisplayName = *body.DisplayName
		}
		if body.ComplianceProfile != nil {
			t.ComplianceProfile = *body.ComplianceProfile
		}
		if body.DataResidencyRegion != nil {
			t.DataResidencyRegion = *body.DataResidencyRegion
		}
		if body.WorkspaceTier != nil {
			t.WorkspaceTier = *body.WorkspaceTier
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenant(updated))
}

// handleDeleteTenant implements DELETE /v1/admin/tenants/{id}. The
// minimal implementation soft-deletes per §12.8; full hard-delete
// (the tenant-deletion controller) ships in Phase 13.
func (r *Router) handleDeleteTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if err := r.tenants.SoftDelete(req.Context(), id, r.clock()); err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeError shares the §15.1 envelope shape with the rest of the
// gateway.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "details": details},
	})
}
