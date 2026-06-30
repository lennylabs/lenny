// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// WithExperiments wires the §10.7 / §15.1 experiment admin endpoints
// onto the Router.
func (r *Router) WithExperiments(s experimentstore.Store) *Router {
	r.experiments = s
	return r
}

// StickyFlusher clears the §10.7 `sticky: user` assignment cache for an
// experiment. The PATCH status-transition handler invokes it when an
// experiment moves to `paused` or `concluded` (§10.7 line 1096).
// *experimentsticky.RedisCache satisfies it. A nil flusher leaves the cache
// untouched (the single-instance / in-memory posture has no Redis cache).
type StickyFlusher interface {
	Flush(ctx context.Context, tenantID, experimentID, transition string) (int, error)
}

// WithStickyFlusher wires the §10.7 sticky-cache invalidation hook onto the
// PATCH status-transition handler.
func (r *Router) WithStickyFlusher(f StickyFlusher) *Router {
	r.stickyFlusher = f
	return r
}

// ExperimentVariant is the wire shape of one §10.7 variant.
type ExperimentVariant struct {
	ID             string  `json:"id"`
	Runtime        string  `json:"runtime,omitempty"`
	Pool           string  `json:"pool,omitempty"`
	Weight         float64 `json:"weight"`
	InitialMinWarm int     `json:"initialMinWarm,omitempty"`
}

// ExperimentTargeting is the §10.7 targeting block.
type ExperimentTargeting struct {
	Mode   string `json:"mode"`
	Sticky string `json:"sticky"`
}

// ExperimentPropagation is the §10.7 propagation block.
type ExperimentPropagation struct {
	ChildSessions string `json:"childSessions"`
}

// ExperimentPayload is the §15.1 experiment wire shape.
type ExperimentPayload struct {
	ID          string                `json:"id"`
	TenantID    string                `json:"tenantId,omitempty"`
	Status      string                `json:"status,omitempty"`
	BaseRuntime string                `json:"baseRuntime"`
	Variants    []ExperimentVariant   `json:"variants"`
	Targeting   ExperimentTargeting   `json:"targeting"`
	Propagation ExperimentPropagation `json:"propagation"`
	CreatedAt   string                `json:"createdAt,omitempty"`
	UpdatedAt   string                `json:"updatedAt,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. List and GET responses carry it so a client can
	// supply it as the If-Match header on a later PUT.
	// spec: §15.1 lines 1207-1209.
	ETag string `json:"etag,omitempty"`
}

// PatchExperimentRequest is the §15.1 PATCH body. v1 supports the
// status transition — the canonical use of the PATCH endpoint.
type PatchExperimentRequest struct {
	Status *string `json:"status,omitempty"`
}

// fromExperiment maps a stored experiment to the wire payload.
func fromExperiment(e experimentstore.Experiment) ExperimentPayload {
	vs := make([]ExperimentVariant, len(e.Variants))
	for i, v := range e.Variants {
		vs[i] = ExperimentVariant{
			ID: v.ID, Runtime: v.Runtime, Pool: v.Pool,
			Weight: v.Weight, InitialMinWarm: v.InitialMinWarm,
		}
	}
	return ExperimentPayload{
		ID:          e.ID,
		TenantID:    e.TenantID,
		Status:      string(e.Status),
		BaseRuntime: e.BaseRuntime,
		Variants:    vs,
		Targeting:   ExperimentTargeting{Mode: string(e.TargetingMode), Sticky: string(e.Sticky)},
		Propagation: ExperimentPropagation{ChildSessions: string(e.Propagation)},
		CreatedAt:   rfc3339Nano(e.CreatedAt),
		UpdatedAt:   rfc3339Nano(e.UpdatedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version,
		// carried per-item on list responses and in the GET header.
		ETag: formatETag(e.Version),
	}
}

// toExperiment maps a wire payload to a stored experiment within the
// resolved tenant.
func toExperiment(p ExperimentPayload, tenant string) experimentstore.Experiment {
	vs := make([]experimentstore.Variant, len(p.Variants))
	for i, v := range p.Variants {
		vs[i] = experimentstore.Variant{
			ID: v.ID, Runtime: v.Runtime, Pool: v.Pool,
			Weight: v.Weight, InitialMinWarm: v.InitialMinWarm,
		}
	}
	return experimentstore.Experiment{
		ID:            p.ID,
		TenantID:      tenant,
		Status:        experiment.Status(p.Status),
		BaseRuntime:   p.BaseRuntime,
		Variants:      vs,
		TargetingMode: experiment.TargetingMode(p.Targeting.Mode),
		Sticky:        experiment.Sticky(p.Targeting.Sticky),
		Propagation:   experiment.Propagation(p.Propagation.ChildSessions),
	}
}

// requireTenantResourceAdmin gates the per-tenant admin resources
// (experiments, environments) on platform-admin or tenant-admin
// per §15.1.
func (r *Router) requireTenantResourceAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p, ok := authmw.FromContext(req.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "endpoint requires authentication", nil)
			return
		}
		if !p.HasRole(auth.RolePlatformAdmin) && !p.HasRole(auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"this resource requires platform-admin or tenant-admin", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// isolationConflict is one §10.7 variant-pool isolation-monotonicity
// violation, surfaced in the 422 CONFIGURATION_CONFLICT details.
type isolationConflict struct {
	Fields  []string `json:"fields"`
	Message string   `json:"message"`
}

// checkVariantIsolation runs the §10.7 admission-time isolation
// monotonicity check: every variant pool must be at least as
// restrictive as the experiment's base runtime, since a session that
// hashes to control falls through to the base runtime. It returns one
// conflict per offending variant. The check is best-effort — when the
// pool or runtime stores are not wired, or a referenced pool or
// runtime is unresolvable, the affected variant is skipped.
func (r *Router) checkVariantIsolation(ctx context.Context, exp experimentstore.Experiment) []isolationConflict {
	if r.pools == nil || r.runtimes == nil {
		return nil
	}
	base, err := r.runtimes.Get(ctx, exp.BaseRuntime)
	if err != nil || !isolation.IsValid(base.IsolationProfile) {
		return nil
	}
	var conflicts []isolationConflict
	for _, v := range exp.Variants {
		if v.Pool == "" {
			continue
		}
		pool, err := r.pools.Get(ctx, v.Pool)
		if err != nil || !isolation.IsValid(pool.IsolationProfile) {
			continue
		}
		if !isolation.AtLeastAsRestrictive(pool.IsolationProfile, base.IsolationProfile) {
			conflicts = append(conflicts, isolationConflict{
				Fields: []string{fmt.Sprintf("variants[%s].pool", v.ID), "baseRuntime"},
				Message: fmt.Sprintf(
					"variant %q pool %q has isolationProfile=%s, weaker than base runtime %q's profile %s",
					v.ID, v.Pool, pool.IsolationProfile, exp.BaseRuntime, base.IsolationProfile,
				),
			})
		}
	}
	return conflicts
}

// checkTenantIsolationFloor runs the §10.7 admission-time tenant-floor
// advisory check: when a variant pool's isolation profile is weaker
// than the tenant's configured minIsolationProfile floor, the gateway
// emits an experiment.variant_weaker_than_tenant_floor event. Unlike
// the hard monotonicity check this never rejects the request — a
// tenant floor may be intentionally stricter than the variant mix, so
// the operator is informed rather than blocked.
func (r *Router) checkTenantIsolationFloor(ctx context.Context, principal authmw.Principal, exp experimentstore.Experiment) {
	if r.tenants == nil || r.pools == nil {
		return
	}
	tenant, err := r.tenants.Get(ctx, exp.TenantID)
	if err != nil || tenant.MinIsolationProfile == "" {
		return
	}
	floor := isolation.Profile(tenant.MinIsolationProfile)
	if !isolation.IsValid(floor) {
		return
	}
	for _, v := range exp.Variants {
		if v.Pool == "" {
			continue
		}
		pool, err := r.pools.Get(ctx, v.Pool)
		if err != nil || !isolation.IsValid(pool.IsolationProfile) {
			continue
		}
		if !isolation.AtLeastAsRestrictive(pool.IsolationProfile, floor) {
			r.emit(ctx, principal, "experiment.variant_weaker_than_tenant_floor", exp.ID, map[string]any{
				"tenantId":           exp.TenantID,
				"variantId":          v.ID,
				"variantPool":        v.Pool,
				"variantProfile":     string(pool.IsolationProfile),
				"tenantFloorProfile": string(floor),
			})
			// §16.6: surface the advisory on the operational-event buffer
			// so ops agents observe it through GET /v1/admin/events/buffer.
			// One event per offending variant per admission call.
			r.emitOpsEvent(ctx, events.EventExperimentVariantWeakerThanFloor, "warning", map[string]any{
				"tenant_id":              exp.TenantID,
				"experiment_id":          exp.ID,
				"variant_id":             v.ID,
				"variant_pool_isolation": string(pool.IsolationProfile),
				"tenant_floor":           string(floor),
				"actor_sub":              principal.Subject,
				"emitted_at":             rfc3339Nano(r.clock()),
			})
		}
	}
}

// rejectIfCrossExperimentWeightsExceed runs the §4.6.2 admission-time
// cross-experiment variant-weight aggregate check. The base pool must
// retain a positive control-group remainder, so Σ variant_weights across
// the candidate (when it will be active) plus every other active
// experiment targeting the same base runtime must stay below 1.0. On
// breach it writes 422 INVALID_VARIANT_WEIGHTS and returns false. The
// §10.7 PoolScalingController enforces the same aggregate at reconcile
// time; catching it at admission keeps an over-budget configuration from
// ever activating. A paused or concluded candidate contributes nothing
// because it diverts no traffic. spec: §4.6.2 line 545 / §10.7 line 1102.
// F-10.7.8.
func (r *Router) rejectIfCrossExperimentWeightsExceed(w http.ResponseWriter, ctx context.Context, candidate experimentstore.Experiment) bool {
	if r.experiments == nil {
		return true
	}
	sum := 0.0
	if candidate.Status == experiment.StatusActive {
		for _, v := range candidate.Variants {
			sum += v.Weight
		}
	}
	siblings, err := r.experiments.List(ctx, candidate.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return false
	}
	for _, sib := range siblings {
		if sib.ID == candidate.ID || sib.BaseRuntime != candidate.BaseRuntime ||
			sib.Status != experiment.StatusActive {
			continue
		}
		for _, v := range sib.Variants {
			sum += v.Weight
		}
	}
	if sum >= 1 {
		writeError(w, http.StatusUnprocessableEntity, experiment.CodeInvalidVariantWeights,
			fmt.Sprintf("Σ variant_weights %g across active experiments on base runtime %q must be < 1 "+
				"(the base pool retains the remainder for the control group)", sum, candidate.BaseRuntime),
			map[string]any{"field": "variants", "value": sum, "baseRuntime": candidate.BaseRuntime})
		return false
	}
	return true
}

// writeDryRun emits the §15.1 line 1140 dry-run success response: the
// computed resource representation plus the `X-Dry-Run: true` header,
// with no persistence and no audit emission. The status code mirrors the
// non-dry-run success (201 for create, 200 for update). F-10.7.15.
func writeDryRun(w http.ResponseWriter, status int, body any) {
	w.Header().Set("X-Dry-Run", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (r *Router) handleCreateExperiment(w http.ResponseWriter, req *http.Request) {
	var body ExperimentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	exp := toExperiment(body, tenant)
	if conflicts := r.checkVariantIsolation(req.Context(), exp); len(conflicts) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "CONFIGURATION_CONFLICT",
			"a variant pool's isolation profile is weaker than the base runtime",
			map[string]any{"conflicts": conflicts})
		return
	}
	if !r.rejectIfCrossExperimentWeightsExceed(w, req.Context(), exp) {
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// §15.1 line 1140 — `?dryRun=true` runs full validation but persists
	// nothing and emits no audit event. The §10.7 line 854/856 isolation
	// and tenant-floor checks above already ran in this path; the
	// remaining single-experiment definition validation (the check the
	// store would run on Create) runs here so the dry run is exhaustive.
	// F-10.7.15.
	if req.URL.Query().Get("dryRun") == "true" {
		if err := exp.Validate(); err != nil {
			writeExperimentValidationError(w, err)
			return
		}
		r.checkTenantIsolationFloor(req.Context(), principal, exp)
		writeDryRun(w, http.StatusCreated, fromExperiment(exp))
		return
	}
	if err := r.experiments.Create(req.Context(), exp); err != nil {
		if errors.Is(err, experimentstore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
				"experiment with this id already exists in tenant", nil)
			return
		}
		writeExperimentValidationError(w, err)
		return
	}
	stored, _ := r.experiments.Get(req.Context(), tenant, exp.ID)
	r.emit(req.Context(), principal, "admin.experiment.created", exp.ID, map[string]any{
		"tenantId": tenant,
		"status":   string(stored.Status),
	})
	r.checkTenantIsolationFloor(req.Context(), principal, stored)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromExperiment(stored))
}

func (r *Router) handleListExperiments(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	rows, err := r.experiments.List(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]ExperimentPayload, 0, len(rows))
	for _, e := range rows {
		out = append(out, fromExperiment(e))
	}
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x ExperimentPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.ID, x.ID
			case "updated_at":
				return x.UpdatedAt, x.ID
			default:
				return x.CreatedAt, x.ID
			}
		})
}

func (r *Router) handleGetExperiment(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.experiments.Get(req.Context(), tenant, req.PathValue("name"))
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 1209 — GET responses for an admin resource carry the
	// ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromExperiment(row))
}

// handleUpdateExperiment implements PUT /v1/admin/experiments/{name}.
// It replaces the experiment definition. The §10.7 status is left
// unchanged — status transitions go through PATCH exclusively.
func (r *Router) handleUpdateExperiment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body ExperimentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// §10.7: PUT replaces the definition but never the status (status
	// transitions go through PATCH). Resolve the existing record first so
	// the §4.6.2 cross-experiment weight check and the dry-run preview
	// reflect the experiment's real (unchanged) status, not whatever the
	// body claims.
	existing, err := r.experiments.Get(req.Context(), tenant, name)
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match. The
	// experiment's entity tag is its version; enforce the optimistic-
	// concurrency precondition before applying the mutation. This runs
	// before the dry-run branch so a dry-run with a stale If-Match still
	// returns 412 and a dry-run with no If-Match still returns 428.
	if !enforceIfMatch(w, req, existing.Version) {
		return
	}
	desired := toExperiment(body, tenant)
	desired.ID = existing.ID
	desired.CreatedAt = existing.CreatedAt
	desired.Status = existing.Status
	if conflicts := r.checkVariantIsolation(req.Context(), desired); len(conflicts) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "CONFIGURATION_CONFLICT",
			"a variant pool's isolation profile is weaker than the base runtime",
			map[string]any{"conflicts": conflicts})
		return
	}
	if !r.rejectIfCrossExperimentWeightsExceed(w, req.Context(), desired) {
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// §15.1 line 1140 — `?dryRun=true` validates and previews the merged
	// definition without persisting or emitting an audit event; §10.7
	// line 856 — the tenant-floor advisory still runs. F-10.7.15.
	if req.URL.Query().Get("dryRun") == "true" {
		if err := desired.Validate(); err != nil {
			writeExperimentValidationError(w, err)
			return
		}
		r.checkTenantIsolationFloor(req.Context(), principal, desired)
		writeDryRun(w, http.StatusOK, fromExperiment(desired))
		return
	}
	updated, err := r.experiments.Update(req.Context(), tenant, name, func(e *experimentstore.Experiment) error {
		status := e.Status // §10.7: status transitions only via PATCH
		desired.ID = e.ID
		desired.CreatedAt = e.CreatedAt
		desired.Status = status
		// spec: §15.1 line 1207 — the §15.1 version is store-managed; carry
		// the loaded value through the full-replacement so the store's
		// Update bump lands on the current version rather than resetting it.
		desired.Version = e.Version
		*e = desired
		return nil
	})
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeExperimentValidationError(w, err)
		return
	}
	r.emit(req.Context(), principal, "admin.experiment.updated", name, map[string]any{"tenantId": tenant})
	r.checkTenantIsolationFloor(req.Context(), principal, updated)
	// spec: §15.1 line 1211 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromExperiment(updated))
}

// handlePatchExperiment implements PATCH /v1/admin/experiments/{name}
// — the §10.7 canonical endpoint for status transitions. A transition
// that the §10.7 lifecycle disallows (e.g. out of `concluded`) is
// rejected with 409 INVALID_STATE_TRANSITION.
func (r *Router) handlePatchExperiment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body PatchExperimentRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if body.Status == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"PATCH body must set status", map[string]any{"field": "status"})
		return
	}
	next := experiment.Status(*body.Status)
	if !next.IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"status must be one of active, paused, or concluded",
			map[string]any{"field": "status"})
		return
	}
	var prev experiment.Status
	var transitionErr error
	updated, err := r.experiments.Update(req.Context(), tenant, name, func(e *experimentstore.Experiment) error {
		prev = e.Status
		if next == e.Status {
			return nil // no-op: status unchanged
		}
		if !e.Status.CanTransitionTo(next) {
			transitionErr = errors.New("invalid transition")
			return transitionErr
		}
		e.Status = next
		return nil
	})
	if transitionErr != nil {
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"experiment status cannot transition from "+string(prev)+" to "+string(next), nil)
		return
	}
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §10.7 line 1096 — flush the `sticky: user` assignment cache when
	// the experiment transitions to paused or concluded so re-activated or
	// re-pointed variants are re-evaluated. A `paused → active` transition
	// requires no flush. The flush is best-effort: a Redis error leaves the
	// stale cache to expire under its TTL and does not fail the transition,
	// which is already durably persisted.
	if prev != updated.Status &&
		(updated.Status == experiment.StatusPaused || updated.Status == experiment.StatusConcluded) &&
		r.stickyFlusher != nil {
		_, _ = r.stickyFlusher.Flush(req.Context(), tenant, name, string(updated.Status))
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "experiment.status_changed", name, map[string]any{
		"tenantId":       tenant,
		"previousStatus": string(prev),
		"newStatus":      string(updated.Status),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromExperiment(updated))
}

func (r *Router) handleDeleteExperiment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// spec: §10.7 line 1094 — concluded experiments are immutable; an
	// active or paused experiment owns a variant pool and may have
	// in-flight enrolled sessions whose eval attribution would orphan
	// on delete. Require the operator to PATCH to `concluded` first.
	// F-10.7.17.
	existing, gerr := r.experiments.Get(req.Context(), tenant, name)
	if gerr != nil {
		if errors.Is(gerr, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	if existing.Status != experiment.StatusConcluded {
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"experiment must be in 'concluded' status before delete",
			map[string]any{"currentStatus": string(existing.Status)})
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412 ETAG_MISMATCH, an absent header proceeds.
	if !enforceIfMatchIfPresent(w, req, existing.Version) {
		return
	}
	if err := r.experiments.Delete(req.Context(), tenant, name); err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.experiment.deleted", name, map[string]any{"tenantId": tenant})
	w.WriteHeader(http.StatusNoContent)
}

// writeExperimentValidationError surfaces an experiment.ValidationError
// using the §15.1-distinct error code attached to each violation.
// RESERVED_IDENTIFIER (line 1005) and INVALID_VARIANT_WEIGHTS (§4.6.2
// line 545) take precedence over the generic VALIDATION_ERROR so
// callers can programmatically distinguish them. spec: F-10.7.11.
func writeExperimentValidationError(w http.ResponseWriter, err error) {
	var ve *experiment.ValidationError
	if !errors.As(err, &ve) {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	preference := []string{experiment.CodeReservedIdentifier, experiment.CodeInvalidVariantWeights}
	for _, code := range preference {
		if !ve.HasCode(code) {
			continue
		}
		v := ve.FirstWithCode(code)
		details := map[string]any{"field": v.Field}
		if v.Value != nil {
			details["value"] = v.Value
		}
		writeError(w, http.StatusUnprocessableEntity, code, v.Message, details)
		return
	}
	writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
}
