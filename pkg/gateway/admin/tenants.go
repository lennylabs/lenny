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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// rfc3339Nano serialises a time.Time using the shared RFC3339Nano
// format every admin payload uses. Zero times serialise to empty so
// optional `deletedAt` is omitted from the wire when absent.
func rfc3339Nano(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// AuditSink receives §11.7 admin audit events. The router emits one
// event per successful mutation (create / update / soft-delete);
// reads do not emit. Implementations must be non-blocking — the
// admin handler does not wait for delivery.
type AuditSink interface {
	EmitAdminEvent(ctx context.Context, event AuditEvent)
}

// AuditEvent is the §11.7 admin-event payload. Fields match the
// canonical OCSF mapping used by the §11.7 hash-chain audit
// pipeline.
type AuditEvent struct {
	// Type is the §11.7 event type (e.g., `admin.tenant.created`).
	Type string

	// ActorSubject is the JWT `sub` of the calling user.
	ActorSubject string

	// ActorTenantID is the JWT `tenant_id` of the calling user
	// (usually `platform` for platform-admin calls).
	ActorTenantID string

	// TargetResource is the resource the operation affects (e.g.,
	// the tenant id).
	TargetResource string

	// Detail carries event-specific fields the auditor records
	// verbatim in the hash-chain entry.
	Detail map[string]any

	// At is the gateway clock instant the audit event fired.
	At time.Time
}

// Router is the §15.1 admin sub-router. The minimal admin API wires
// only the resources the gateway has stores for; future commits add
// users, pools, connectors, circuit breakers, etc.
type Router struct {
	tenants  tenantstore.Store
	runtimes runtimestore.Store
	users    userstore.Store
	pools    poolstore.Store
	clock    func() time.Time
	audit    AuditSink
}

// Options configures the Router.
type Options struct {
	// Clock overrides time.Now. Pass nil for production.
	Clock func() time.Time

	// Audit, when set, receives one event per successful admin
	// mutation per §11.7. Nil disables emission (the operation still
	// succeeds).
	Audit AuditSink
}

// NewRouter returns a Router. Pass nil for opts to use the defaults.
func NewRouter(tenants tenantstore.Store, opts Options) *Router {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Router{tenants: tenants, clock: clock, audit: opts.Audit}
}

// emit fires an audit event when an AuditSink is wired. Never
// blocks the caller — sinks must do their own async delivery.
func (r *Router) emit(ctx context.Context, p authmw.Principal, eventType, resource string, detail map[string]any) {
	if r.audit == nil {
		return
	}
	r.audit.EmitAdminEvent(ctx, AuditEvent{
		Type:           eventType,
		ActorSubject:   p.Subject,
		ActorTenantID:  p.TenantID,
		TargetResource: resource,
		Detail:         detail,
		At:             r.clock(),
	})
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
	if r.runtimes != nil {
		mux.Handle("POST /v1/admin/runtimes", r.requireAdmin(http.HandlerFunc(r.handleCreateRuntime)))
		mux.Handle("GET /v1/admin/runtimes", r.requireAdmin(http.HandlerFunc(r.handleListRuntimes)))
		mux.Handle("GET /v1/admin/runtimes/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetRuntime)))
		mux.Handle("PUT /v1/admin/runtimes/{name}", r.requireAdmin(http.HandlerFunc(r.handleUpdateRuntime)))
		mux.Handle("DELETE /v1/admin/runtimes/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeleteRuntime)))
	}
	if r.users != nil {
		mux.Handle("POST /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleCreateUser)))
		mux.Handle("GET /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleListUsers)))
		mux.Handle("GET /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleGetUser)))
		mux.Handle("PUT /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleUpdateUser)))
		mux.Handle("DELETE /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleDeleteUser)))
	}
	if r.pools != nil {
		mux.Handle("POST /v1/admin/pools", r.requireAdmin(http.HandlerFunc(r.handleCreatePool)))
		mux.Handle("GET /v1/admin/pools", r.requireAdmin(http.HandlerFunc(r.handleListPools)))
		mux.Handle("GET /v1/admin/pools/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetPool)))
		mux.Handle("PUT /v1/admin/pools/{name}", r.requireAdmin(http.HandlerFunc(r.handleUpdatePool)))
		mux.Handle("DELETE /v1/admin/pools/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeletePool)))
	}
	if r.tenants != nil || r.runtimes != nil || r.users != nil {
		mux.Handle("POST /v1/admin/bootstrap", r.requireAdmin(http.HandlerFunc(r.handleBootstrap)))
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
	return TenantPayload{
		ID:                  t.ID,
		DisplayName:         t.DisplayName,
		ComplianceProfile:   t.ComplianceProfile,
		DataResidencyRegion: t.DataResidencyRegion,
		WorkspaceTier:       t.WorkspaceTier,
		CreatedAt:           rfc3339Nano(t.CreatedAt),
		UpdatedAt:           rfc3339Nano(t.UpdatedAt),
		DeletedAt:           rfc3339Nano(t.DeletedAt),
	}
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.created", body.ID, map[string]any{
		"displayName":         row.DisplayName,
		"complianceProfile":   row.ComplianceProfile,
		"dataResidencyRegion": row.DataResidencyRegion,
		"workspaceTier":       row.WorkspaceTier,
	})
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.updated", id, map[string]any{
		"changedFields": changedFields(body),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenant(updated))
}

// changedFields returns the list of body fields the caller set so the
// audit event records the intent without leaking the new value
// (values are still on the row response; the audit detail captures
// the change list for compact mutation history).
func changedFields(b UpdateTenantRequest) []string {
	var out []string
	if b.DisplayName != nil {
		out = append(out, "displayName")
	}
	if b.ComplianceProfile != nil {
		out = append(out, "complianceProfile")
	}
	if b.DataResidencyRegion != nil {
		out = append(out, "dataResidencyRegion")
	}
	if b.WorkspaceTier != nil {
		out = append(out, "workspaceTier")
	}
	return out
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.soft_deleted", id, nil)
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
