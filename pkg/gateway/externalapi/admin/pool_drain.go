// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// PoolDrainMetrics records the §15.1 line 797
// `lenny_pool_draining_sessions_total` gauge — the in-flight session
// count for a pool while it is draining, labelled by pool. A nil seam
// leaves the gauge unset; the drain operation still succeeds.
// *gatewaymetrics.Metrics satisfies it.
type PoolDrainMetrics interface {
	// SetPoolDrainingSessions records the in-flight (non-terminal)
	// session count for a draining pool. A count of 0 means the drain
	// has converged.
	SetPoolDrainingSessions(pool string, count int)
}

// WithPoolDrainMetrics wires the §15.1 line 797 pool-drain gauge onto
// the Router. Without it the drain endpoint still operates; the
// `lenny_pool_draining_sessions_total` gauge is simply not updated.
func (r *Router) WithPoolDrainMetrics(m PoolDrainMetrics) *Router {
	r.poolDrainMetrics = m
	return r
}

// PoolDrainResponse is the §15.1 line 797 drain API response body:
// `{"status": "draining", "activeSessions": <n>, "estimatedDrainSeconds": <n>}`.
type PoolDrainResponse struct {
	Status                string `json:"status"`
	ActiveSessions        int    `json:"activeSessions"`
	EstimatedDrainSeconds int    `json:"estimatedDrainSeconds"`
}

// handleDrainPool implements the §15.1 line 797
// POST /v1/admin/pools/{name}/drain endpoint. It transitions the pool to
// the `draining` phase so the gateway stops admitting new sessions to it
// (session creation that would select the pool is rejected with 503
// POOL_DRAINING by the sessionserver gate), and reports the current
// in-flight session count and the estimated drain-completion seconds.
// Drain is a pool action endpoint and does not support `?dryRun=true`
// (§15.1 line 1205). It is idempotent: draining an already-draining pool
// returns the current stats without resetting the drain clock.
func (r *Router) handleDrainPool(w http.ResponseWriter, req *http.Request) {
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
	if !row.IsActive() {
		// A soft-deleted pool has no pods to drain; treat it as absent so
		// a stale handle cannot toggle drain state on a removed pool.
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
		return
	}
	// Only write when the pool is not already draining so repeated drain
	// calls do not churn pool_config_generation (drain is idempotent per
	// §15.1 line 797).
	if !row.IsDraining() {
		row, err = r.pools.Update(req.Context(), name, func(p *poolstore.Pool) error {
			if p.DrainingSince.IsZero() {
				p.DrainingSince = r.clock()
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, poolstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
	}

	active, est := r.poolDrainStats(req.Context(), row)
	if r.poolDrainMetrics != nil {
		r.poolDrainMetrics.SetPoolDrainingSessions(name, active)
	}
	if principal, ok := authmw.FromContext(req.Context()); ok {
		r.emit(req.Context(), principal, "admin.pool.drained", name, map[string]any{
			"activeSessions":        active,
			"estimatedDrainSeconds": est,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PoolDrainResponse{
		Status:                poolstore.PhaseDraining,
		ActiveSessions:        active,
		EstimatedDrainSeconds: est,
	})
}

// poolDrainStats returns the §15.1 line 797 in-flight session count and
// the estimated drain-completion seconds for a pool. The estimate is the
// longest active session age in the pool capped at maxSessionAgeSeconds
// (poolstore.EstimatedDrainSeconds). With no session store wired, or no
// live sessions, both are zero (drain has already converged).
func (r *Router) poolDrainStats(ctx context.Context, pool poolstore.Pool) (active, estSeconds int) {
	if r.sessions == nil {
		return 0, 0
	}
	count, oldest, err := r.sessions.PoolDrainStats(ctx, pool.Name)
	if err != nil || count == 0 {
		return 0, 0
	}
	ageSeconds := int(r.clock().Sub(oldest).Seconds())
	return count, poolstore.EstimatedDrainSeconds(pool, ageSeconds)
}
