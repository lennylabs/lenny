// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// ReconciliationResumer clears the PoolScalingController's in-memory
// admission-denial backoff for one pool, implementing §4.6.2 item 3
// condition (c): an operator-initiated reset of a stuck pool's denial
// counter that does not require a Postgres configuration change. The
// PSC's Reconciler satisfies it through a namespace-binding adapter
// (the admin API addresses pools by name; the PSC keys them by agent
// namespace). The returned count is the number of CRD tuples cleared,
// zero when the pool was not stuck.
type ReconciliationResumer interface {
	ResumePoolReconciliation(ctx context.Context, poolName string) (cleared int, err error)
}

// PoolStatusReader reads a pool's live §5.2 bootstrap status from its
// Kubernetes CRD pair so the admin pool GET can surface poolCondition
// and idlePodCount (§5.2 line 629) without operators inspecting the CR
// status directly. condition is "PoolWarmingUp" while the pool is
// bootstrapping and the empty string otherwise; idlePodCount is the
// ready-pod count. found is false when no SandboxWarmPool exists for
// the pool yet, so the handler omits the live-status fields.
// The gateway satisfies it with a podsession.PoolStatusLookup.
type PoolStatusReader interface {
	PoolStatus(ctx context.Context, poolName string) (condition string, idlePodCount int, found bool, err error)
}

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

	// EgressProfile is the §13.2 per-pool egress profile (`restricted`,
	// `provider-direct`, `internet`). Empty on create resolves to the
	// §13.2 default (`restricted`).
	EgressProfile string `json:"egressProfile,omitempty"`

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

	// PoolCondition and IdlePodCount are the §5.2 line 629 live
	// bootstrap-status fields, populated on GET when the gateway is
	// wired with a PoolStatusReader and the pool's SandboxWarmPool
	// exists. PoolCondition is "PoolWarmingUp" during the bootstrap
	// window. They are pointers so a legitimate idlePodCount of 0 is
	// emitted while an unwired reader or an unreconciled pool omits both.
	PoolCondition *string `json:"poolCondition,omitempty"`
	IdlePodCount  *int    `json:"idlePodCount,omitempty"`

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
	EgressProfile                    *string `json:"egressProfile,omitempty"`
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
		EgressProfile:                    string(p.EgressProfile),
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

// WithReconciliationResumer wires the §4.6.2
// POST /v1/admin/pools/{name}/resume-reconciliation endpoint onto the
// Router. Without it the route is not registered (the gateway has no
// PoolScalingController to address). The resumer is the PSC's denial
// tracker, reachable from the gateway only when both run in the same
// process or through a control channel the deployer supplies.
func (r *Router) WithReconciliationResumer(rr ReconciliationResumer) *Router {
	r.reconciliationResumer = rr
	return r
}

// WithPoolStatusReader wires the §5.2 line 629 live pool-status lookup
// onto the Router. Without it the admin pool GET omits poolCondition
// and idlePodCount (the gateway has no Kubernetes client to read CRD
// status, e.g. the minimal Postgres-only dev posture).
func (r *Router) WithPoolStatusReader(rdr PoolStatusReader) *Router {
	r.poolStatus = rdr
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
	if p.EgressProfile != "" && !egress.IsValid(egress.Profile(p.EgressProfile)) {
		return errors.New("egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)")
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
	// creation. Capture the runtime's §12.9 workspace tier for the §5.2
	// line 396 T4 cross-tenant-reuse check below.
	var runtimeTier runtimestore.WorkspaceTier
	if r.runtimes != nil && body.RuntimeRef != "" {
		rt, err := r.runtimes.Get(req.Context(), body.RuntimeRef)
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"runtimeRef does not resolve to a registered runtime",
				map[string]any{"runtimeRef": body.RuntimeRef})
			return
		}
		runtimeTier = rt.WorkspaceTier
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
		EgressProfile:                    egress.Profile(body.EgressProfile),
		CreatedAt:                        r.clock(),
	}
	pl.UpdatedAt = pl.CreatedAt
	if pl.EgressProfile == "" {
		// spec: §13.2 — an omitted egress profile resolves to the
		// narrowest egress (`restricted`), so the stored record reflects
		// the resolved profile rather than an ambiguous empty value.
		pl.EgressProfile = egress.Default()
	}
	if pl.IsolationProfile == "" {
		// spec: §5.3 line 677 — in dev mode the default isolation profile
		// falls back to `standard` (runc) so a developer can launch pods on
		// a cluster without gVisor. A standard-isolation pool requires the
		// explicit allowStandardIsolation opt-in; dev mode supplies it on
		// the operator's behalf for the default fallback (the warning is
		// logged once at gateway startup).
		pl.IsolationProfile = isolation.DefaultForMode(r.devMode)
		if r.devMode && pl.IsolationProfile == isolation.ProfileStandard {
			pl.AllowStandardIsolation = true
		}
	}
	if pl.ExecutionMode == "" {
		pl.ExecutionMode = runtimestore.ExecutionModeSession
	}
	// spec: §5.2 line 396 — reject cross-tenant reuse on a pool whose
	// runtime is T4 before storage, so a misconfigured pool never enters
	// the registry.
	if err := poolstore.ValidateCrossTenantReuseTier(pl, runtimeTier); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"runtimeRef": body.RuntimeRef, "workspaceTier": string(runtimeTier)})
		return
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
	r.maybeEmitWeakIsolation(req.Context(), principal, stored)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromPool(stored))
}

// maybeEmitWeakIsolation emits the §5.3 DirectModeWeakIsolation warning
// audit event when a pool runs under `standard` (runc) isolation via the
// explicit `allowStandardIsolation: true` opt-in. The opt-in is the
// deliberately-weak-isolation posture the §5.3 security note targets and
// §4.9 line 1489 requires a warning signal for; the standard
// `admin.pool.created` / `admin.pool.updated` event does not distinguish
// it from a sandboxed pool, so this companion event gives compliance
// teams an audit-trail signal that a runc pool was admitted. A pool that
// is not runc, or one rejected before storage, emits nothing.
func (r *Router) maybeEmitWeakIsolation(ctx context.Context, p authmw.Principal, pool poolstore.Pool) {
	if pool.IsolationProfile != isolation.ProfileStandard || !pool.AllowStandardIsolation {
		return
	}
	r.emit(ctx, p, "pool.direct_mode_weak_isolation", pool.Name, map[string]any{
		"isolationProfile":       string(pool.IsolationProfile),
		"allowStandardIsolation": true,
		"egressProfile":          string(pool.EgressProfile),
		"severity":               "warning",
		"reason":                 "pool admitted under standard (runc) isolation via explicit allowStandardIsolation opt-in (§5.3)",
	})
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
	payload := fromPool(row)
	r.attachPoolStatus(req.Context(), &payload)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// attachPoolStatus populates the §5.2 line 629 live poolCondition and
// idlePodCount on a pool payload when a PoolStatusReader is wired and
// the pool has a reconciled SandboxWarmPool. A reader error is swallowed
// so a transient Kubernetes lookup failure never fails the admin GET;
// the live-status fields are simply omitted. poolCondition is set only
// while the pool is in the PoolWarmingUp bootstrap window.
// spec: §5.2 line 629 ("Operator visibility").
func (r *Router) attachPoolStatus(ctx context.Context, p *PoolPayload) {
	if r.poolStatus == nil {
		return
	}
	condition, idle, found, err := r.poolStatus.PoolStatus(ctx, p.Name)
	if err != nil || !found {
		return
	}
	idleCopy := idle
	p.IdlePodCount = &idleCopy
	if condition != "" {
		condCopy := condition
		p.PoolCondition = &condCopy
	}
}

// handleResumeReconciliation implements §4.6.2 item 3 condition (c):
// an operator resets a stuck pool's in-memory admission-denial counter
// so the PoolScalingController retries the pool on its next tick
// without requiring a Postgres configuration change. The pool must
// exist; the response reports how many CRD tuples were cleared and
// whether the pool was actually stuck.
func (r *Router) handleResumeReconciliation(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if _, err := r.pools.Get(req.Context(), name); err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	cleared, err := r.reconciliationResumer.ResumePoolReconciliation(req.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.reconciliation_resumed", name, map[string]any{
		"clearedTuples": cleared,
		"wasStuck":      cleared > 0,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pool":          name,
		"clearedTuples": cleared,
		"wasStuck":      cleared > 0,
	})
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
	if body.EgressProfile != nil && *body.EgressProfile != "" &&
		!egress.IsValid(egress.Profile(*body.EgressProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)", nil)
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
	// spec: §5.2 line 396 — reject a PUT that would leave the pool with
	// cross-tenant reuse enabled while its runtime is T4. The effective
	// post-update runtimeRef and allowCrossTenantReuse are resolved from
	// the body (when set) or the stored pool, so newly setting either
	// field is caught. The runtime tier is only consulted when reuse
	// would be on.
	if r.runtimes != nil {
		current, gerr := r.pools.Get(req.Context(), name)
		if errors.Is(gerr, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		} else if gerr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
		effCross := current.AllowCrossTenantReuse
		if body.AllowCrossTenantReuse != nil {
			effCross = *body.AllowCrossTenantReuse
		}
		if effCross {
			effRef := current.RuntimeRef
			if body.RuntimeRef != nil {
				effRef = *body.RuntimeRef
			}
			var tier runtimestore.WorkspaceTier
			if effRef != "" {
				rt, rerr := r.runtimes.Get(req.Context(), effRef)
				if rerr != nil {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
						"runtimeRef does not resolve to a registered runtime", nil)
					return
				}
				tier = rt.WorkspaceTier
			}
			if err := poolstore.ValidateCrossTenantReuseTier(
				poolstore.Pool{AllowCrossTenantReuse: true}, tier); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
					map[string]any{"runtimeRef": effRef, "workspaceTier": string(tier)})
				return
			}
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
		if body.EgressProfile != nil {
			p.EgressProfile = egress.Profile(*body.EgressProfile)
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
	r.maybeEmitWeakIsolation(req.Context(), principal, updated)
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
	if b.EgressProfile != nil {
		out = append(out, "egressProfile")
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
