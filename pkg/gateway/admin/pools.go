// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// PoolPayload is the §15.1 admin-pool wire shape.
type PoolPayload struct {
	Name                   string `json:"name"`
	RuntimeRef             string `json:"runtimeRef,omitempty"`
	IsolationProfile       string `json:"isolationProfile,omitempty"`
	ExecutionMode          string `json:"executionMode,omitempty"`
	ResourceClass          string `json:"resourceClass,omitempty"`
	WarmCount              int    `json:"warmCount,omitempty"`
	MaxSessionAgeSeconds   int    `json:"maxSessionAgeSeconds,omitempty"`
	AllowStandardIsolation bool   `json:"allowStandardIsolation,omitempty"`

	// ConcurrencyStyle, MaxConcurrent, AcknowledgeProcessLevelIsolation,
	// CleanupTimeoutSeconds, and AllowCrossTenantReuse are the §5.2
	// concurrent-mode (`executionMode: concurrent`) configuration. They
	// are meaningful only on a concurrent-mode pool; the pool store's
	// ValidateConcurrentConfig rejects them on any other pool.
	ConcurrencyStyle                 string `json:"concurrencyStyle,omitempty"`
	MaxConcurrent                    int    `json:"maxConcurrent,omitempty"`
	AcknowledgeProcessLevelIsolation bool   `json:"acknowledgeProcessLevelIsolation,omitempty"`
	CleanupTimeoutSeconds            int    `json:"cleanupTimeoutSeconds,omitempty"`
	AllowCrossTenantReuse            bool   `json:"allowCrossTenantReuse,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	DeletedAt string `json:"deletedAt,omitempty"`
}

// UpdatePoolRequest is the §15.1 PUT body.
type UpdatePoolRequest struct {
	RuntimeRef                       *string `json:"runtimeRef,omitempty"`
	IsolationProfile                 *string `json:"isolationProfile,omitempty"`
	ExecutionMode                    *string `json:"executionMode,omitempty"`
	ResourceClass                    *string `json:"resourceClass,omitempty"`
	WarmCount                        *int    `json:"warmCount,omitempty"`
	MaxSessionAgeSeconds             *int    `json:"maxSessionAgeSeconds,omitempty"`
	AllowStandardIsolation           *bool   `json:"allowStandardIsolation,omitempty"`
	ConcurrencyStyle                 *string `json:"concurrencyStyle,omitempty"`
	MaxConcurrent                    *int    `json:"maxConcurrent,omitempty"`
	AcknowledgeProcessLevelIsolation *bool   `json:"acknowledgeProcessLevelIsolation,omitempty"`
	CleanupTimeoutSeconds            *int    `json:"cleanupTimeoutSeconds,omitempty"`
	AllowCrossTenantReuse            *bool   `json:"allowCrossTenantReuse,omitempty"`
}

func fromPool(p poolstore.Pool) PoolPayload {
	return PoolPayload{
		Name:                             p.Name,
		RuntimeRef:                       p.RuntimeRef,
		IsolationProfile:                 string(p.IsolationProfile),
		ExecutionMode:                    string(p.ExecutionMode),
		ResourceClass:                    p.ResourceClass,
		WarmCount:                        p.WarmCount,
		MaxSessionAgeSeconds:             p.MaxSessionAgeSeconds,
		AllowStandardIsolation:           p.AllowStandardIsolation,
		ConcurrencyStyle:                 string(p.ConcurrencyStyle),
		MaxConcurrent:                    p.MaxConcurrent,
		AcknowledgeProcessLevelIsolation: p.AcknowledgeProcessLevelIsolation,
		CleanupTimeoutSeconds:            p.CleanupTimeoutSeconds,
		AllowCrossTenantReuse:            p.AllowCrossTenantReuse,
		CreatedAt:                        rfc3339Nano(p.CreatedAt),
		UpdatedAt:                        rfc3339Nano(p.UpdatedAt),
		DeletedAt:                        rfc3339Nano(p.DeletedAt),
	}
}

// WithPools wires the §15.1 pool CRUD handlers onto the Router.
func (r *Router) WithPools(s poolstore.Store) *Router {
	r.pools = s
	return r
}

func (p PoolPayload) validateEnums() error {
	if p.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(p.IsolationProfile)) {
		return errors.New("isolationProfile is not a recognised §5.3 profile")
	}
	if p.ExecutionMode != "" && !runtimestore.ExecutionMode(p.ExecutionMode).IsValid() {
		return errors.New("executionMode is not a recognised mode")
	}
	if p.ConcurrencyStyle != "" && !poolstore.ConcurrencyStyle(p.ConcurrencyStyle).IsValid() {
		return errors.New("concurrencyStyle is not a recognised §5.2 style (workspace, stateless)")
	}
	return nil
}

func (r *Router) handleCreatePool(w http.ResponseWriter, req *http.Request) {
	var body PoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if err := poolstore.ValidateName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "name"})
		return
	}
	if err := body.validateEnums(); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	// Cross-resource consistency: if the runtimes store is wired,
	// reject pools that reference a non-existent runtime so
	// misconfigurations surface at admin time rather than at session
	// creation.
	if r.runtimes != nil && body.RuntimeRef != "" {
		if _, err := r.runtimes.Get(req.Context(), body.RuntimeRef); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"runtimeRef does not resolve to a registered runtime",
				map[string]any{"runtimeRef": body.RuntimeRef})
			return
		}
	}

	pl := poolstore.Pool{
		Name:                             body.Name,
		RuntimeRef:                       body.RuntimeRef,
		IsolationProfile:                 isolation.Profile(body.IsolationProfile),
		ExecutionMode:                    runtimestore.ExecutionMode(body.ExecutionMode),
		ConcurrencyStyle:                 poolstore.ConcurrencyStyle(body.ConcurrencyStyle),
		MaxConcurrent:                    body.MaxConcurrent,
		AcknowledgeProcessLevelIsolation: body.AcknowledgeProcessLevelIsolation,
		CleanupTimeoutSeconds:            body.CleanupTimeoutSeconds,
		AllowCrossTenantReuse:            body.AllowCrossTenantReuse,
		ResourceClass:                    body.ResourceClass,
		WarmCount:                        body.WarmCount,
		MaxSessionAgeSeconds:             body.MaxSessionAgeSeconds,
		AllowStandardIsolation:           body.AllowStandardIsolation,
		CreatedAt:                        r.clock(),
	}
	pl.UpdatedAt = pl.CreatedAt
	if pl.IsolationProfile == "" {
		pl.IsolationProfile = isolation.Default()
	}
	if pl.ExecutionMode == "" {
		pl.ExecutionMode = runtimestore.ExecutionModeSession
	}
	if err := r.pools.Create(req.Context(), pl); err != nil {
		if errors.Is(err, poolstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"pool with this name already exists", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.pools.Get(req.Context(), body.Name)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.created", body.Name, map[string]any{
		"runtimeRef":       stored.RuntimeRef,
		"isolationProfile": string(stored.IsolationProfile),
		"executionMode":    string(stored.ExecutionMode),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromPool(stored))
}

func (r *Router) handleListPools(w http.ResponseWriter, req *http.Request) {
	filter := poolstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
		RuntimeRef:     req.URL.Query().Get("runtimeRef"),
	}
	rows, err := r.pools.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin sees only pools granted to their tenant
	// through the pool_tenant_access table; platform-admin is
	// unfiltered.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		allowed := r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)
		kept := rows[:0]
		for _, p := range rows {
			if allowed[p.Name] {
				kept = append(kept, p)
			}
		}
		rows = kept
	}
	out := make([]PoolPayload, 0, len(rows))
	for _, p := range rows {
		out = append(out, fromPool(p))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"pools": out})
}

func (r *Router) handleGetPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4 / §15.1: a tenant-admin may read a pool only when their
	// tenant holds an access grant for it; otherwise it reads as 404.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)[row.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromPool(row))
}

func (r *Router) handleUpdatePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body UpdatePoolRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if body.IsolationProfile != nil && *body.IsolationProfile != "" &&
		!isolation.IsValid(isolation.Profile(*body.IsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile", nil)
		return
	}
	if body.ExecutionMode != nil && *body.ExecutionMode != "" &&
		!runtimestore.ExecutionMode(*body.ExecutionMode).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"executionMode is not a recognised mode", nil)
		return
	}
	if body.ConcurrencyStyle != nil && *body.ConcurrencyStyle != "" &&
		!poolstore.ConcurrencyStyle(*body.ConcurrencyStyle).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"concurrencyStyle is not a recognised §5.2 style (workspace, stateless)", nil)
		return
	}
	// runtimeRef cross-check.
	if body.RuntimeRef != nil && *body.RuntimeRef != "" && r.runtimes != nil {
		if _, err := r.runtimes.Get(req.Context(), *body.RuntimeRef); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"runtimeRef does not resolve to a registered runtime", nil)
			return
		}
	}
	updated, err := r.pools.Update(req.Context(), name, func(p *poolstore.Pool) error {
		if body.RuntimeRef != nil {
			p.RuntimeRef = *body.RuntimeRef
		}
		if body.IsolationProfile != nil {
			p.IsolationProfile = isolation.Profile(*body.IsolationProfile)
		}
		if body.ExecutionMode != nil {
			p.ExecutionMode = runtimestore.ExecutionMode(*body.ExecutionMode)
		}
		if body.ResourceClass != nil {
			p.ResourceClass = *body.ResourceClass
		}
		if body.WarmCount != nil {
			p.WarmCount = *body.WarmCount
		}
		if body.MaxSessionAgeSeconds != nil {
			p.MaxSessionAgeSeconds = *body.MaxSessionAgeSeconds
		}
		if body.AllowStandardIsolation != nil {
			p.AllowStandardIsolation = *body.AllowStandardIsolation
		}
		if body.ConcurrencyStyle != nil {
			p.ConcurrencyStyle = poolstore.ConcurrencyStyle(*body.ConcurrencyStyle)
		}
		if body.MaxConcurrent != nil {
			p.MaxConcurrent = *body.MaxConcurrent
		}
		if body.AcknowledgeProcessLevelIsolation != nil {
			p.AcknowledgeProcessLevelIsolation = *body.AcknowledgeProcessLevelIsolation
		}
		if body.CleanupTimeoutSeconds != nil {
			p.CleanupTimeoutSeconds = *body.CleanupTimeoutSeconds
		}
		if body.AllowCrossTenantReuse != nil {
			p.AllowCrossTenantReuse = *body.AllowCrossTenantReuse
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.updated", name, map[string]any{
		"changedFields": changedPoolFields(body),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromPool(updated))
}

func (r *Router) handleDeletePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if err := r.pools.SoftDelete(req.Context(), name, r.clock()); err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.soft_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func changedPoolFields(b UpdatePoolRequest) []string {
	var out []string
	if b.RuntimeRef != nil {
		out = append(out, "runtimeRef")
	}
	if b.IsolationProfile != nil {
		out = append(out, "isolationProfile")
	}
	if b.ExecutionMode != nil {
		out = append(out, "executionMode")
	}
	if b.ResourceClass != nil {
		out = append(out, "resourceClass")
	}
	if b.WarmCount != nil {
		out = append(out, "warmCount")
	}
	if b.MaxSessionAgeSeconds != nil {
		out = append(out, "maxSessionAgeSeconds")
	}
	if b.AllowStandardIsolation != nil {
		out = append(out, "allowStandardIsolation")
	}
	if b.ConcurrencyStyle != nil {
		out = append(out, "concurrencyStyle")
	}
	if b.MaxConcurrent != nil {
		out = append(out, "maxConcurrent")
	}
	if b.AcknowledgeProcessLevelIsolation != nil {
		out = append(out, "acknowledgeProcessLevelIsolation")
	}
	if b.CleanupTimeoutSeconds != nil {
		out = append(out, "cleanupTimeoutSeconds")
	}
	if b.AllowCrossTenantReuse != nil {
		out = append(out, "allowCrossTenantReuse")
	}
	return out
}
