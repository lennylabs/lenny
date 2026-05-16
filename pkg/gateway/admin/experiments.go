// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// WithExperiments wires the §10.7 / §15.1 experiment admin endpoints
// onto the Router.
func (r *Router) WithExperiments(s experimentstore.Store) *Router {
	r.experiments = s
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
					v.ID, v.Pool, pool.IsolationProfile, exp.BaseRuntime, base.IsolationProfile),
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
		}
	}
}

func (r *Router) handleCreateExperiment(w http.ResponseWriter, req *http.Request) {
	var body ExperimentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
	if err := r.experiments.Create(req.Context(), exp); err != nil {
		if errors.Is(err, experimentstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"experiment with this id already exists in tenant", nil)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.experiments.Get(req.Context(), tenant, exp.ID)
	principal, _ := authmw.FromContext(req.Context())
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"experiments": out})
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
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	desired := toExperiment(body, tenant)
	if conflicts := r.checkVariantIsolation(req.Context(), desired); len(conflicts) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "CONFIGURATION_CONFLICT",
			"a variant pool's isolation profile is weaker than the base runtime",
			map[string]any{"conflicts": conflicts})
		return
	}
	updated, err := r.experiments.Update(req.Context(), tenant, name, func(e *experimentstore.Experiment) error {
		status := e.Status // §10.7: status transitions only via PATCH
		desired.ID = e.ID
		desired.CreatedAt = e.CreatedAt
		desired.Status = status
		*e = desired
		return nil
	})
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.experiment.updated", name, map[string]any{"tenantId": tenant})
	r.checkTenantIsolationFloor(req.Context(), principal, updated)
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
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
