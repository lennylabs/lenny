// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
	"github.com/lennylabs/lenny/pkg/runtime/upgrade/state"
)

// RuntimeUpgradeManager drives the §10.5 RuntimeUpgrade state machine on a
// durable store. *runtimeupgrade.Manager satisfies it; a nil manager
// leaves the /v1/admin/pools/{name}/upgrade routes unregistered. spec:
// §10.5 lines 466-540, §15.1 lines 869-874. F-10.5.1.
type RuntimeUpgradeManager interface {
	Start(ctx context.Context, pool string, opts runtimeupgrade.StartOptions) (runtimeupgrade.Snapshot, error)
	Proceed(ctx context.Context, pool string) (runtimeupgrade.Snapshot, error)
	Pause(ctx context.Context, pool, reason string) (runtimeupgrade.Snapshot, error)
	Resume(ctx context.Context, pool string) (runtimeupgrade.Snapshot, error)
	Rollback(ctx context.Context, pool string, restoreOldPool bool) (runtimeupgrade.Snapshot, error)
	Status(ctx context.Context, pool string) (runtimeupgrade.Snapshot, bool, error)
}

// WithRuntimeUpgrade wires the §10.5 RuntimeUpgrade admin handlers onto
// the Router.
func (r *Router) WithRuntimeUpgrade(m RuntimeUpgradeManager) *Router {
	r.runtimeUpgrade = m
	return r
}

// UpgradeStatus is the §15.1 line 874 GET /upgrade-status wire payload and
// the body returned by each transition. It mirrors
// runtimeupgrade.Snapshot.
type UpgradeStatus struct {
	Pool                       string  `json:"pool"`
	Phase                      string  `json:"phase"`
	PriorPhase                 string  `json:"priorPhase,omitempty"`
	NewImage                   string  `json:"newImage,omitempty"`
	SchemaVersion              string  `json:"schemaVersion,omitempty"`
	DrainFirst                 bool    `json:"drainFirst"`
	AutoAdvance                bool    `json:"autoAdvance"`
	CanaryPercent              int     `json:"canaryPercent"`
	StabilizationWindowSeconds int64   `json:"stabilizationWindowSeconds"`
	DrainTimeoutSeconds        int64   `json:"drainTimeoutSeconds"`
	PauseReason                string  `json:"pauseReason,omitempty"`
	PausedAt                   string  `json:"pausedAt,omitempty"`
	PhaseEnteredAt             string  `json:"phaseEnteredAt,omitempty"`
	PhaseDurationSeconds       float64 `json:"phaseDurationSeconds"`
	DrainingSessions           int     `json:"drainingSessions"`
	HasPreviousPoolSpec        bool    `json:"hasPreviousPoolSpec"`
	Version                    int64   `json:"version"`
	CreatedAt                  string  `json:"createdAt,omitempty"`
	UpdatedAt                  string  `json:"updatedAt,omitempty"`
}

func upgradeStatusOf(s runtimeupgrade.Snapshot) UpgradeStatus {
	return UpgradeStatus{
		Pool:                       s.Pool,
		Phase:                      s.Phase,
		PriorPhase:                 s.PriorPhase,
		NewImage:                   s.NewImage,
		SchemaVersion:              s.SchemaVersion,
		DrainFirst:                 s.DrainFirst,
		AutoAdvance:                s.AutoAdvance,
		CanaryPercent:              s.CanaryPercent,
		StabilizationWindowSeconds: s.StabilizationWindowSeconds,
		DrainTimeoutSeconds:        s.DrainTimeoutSeconds,
		PauseReason:                s.PauseReason,
		PausedAt:                   rfc3339Nano(s.PausedAt),
		PhaseEnteredAt:             rfc3339Nano(s.PhaseEnteredAt),
		PhaseDurationSeconds:       s.PhaseDurationSeconds,
		DrainingSessions:           s.DrainingSessions,
		HasPreviousPoolSpec:        s.HasPreviousPoolSpec,
		Version:                    s.Version,
		CreatedAt:                  rfc3339Nano(s.CreatedAt),
		UpdatedAt:                  rfc3339Nano(s.UpdatedAt),
	}
}

// StartUpgradeRequest is the §15.1 line 869 POST /upgrade/start body. Only
// newImage is required; the remaining knobs default per §10.5.
type StartUpgradeRequest struct {
	NewImage                   string `json:"newImage"`
	CanaryPercent              int    `json:"canaryPercent"`
	SchemaVersion              string `json:"schemaVersion"`
	DrainFirst                 bool   `json:"drainFirst"`
	AutoAdvance                bool   `json:"autoAdvance"`
	StabilizationWindowSeconds int64  `json:"stabilizationWindowSeconds"`
	DrainTimeoutSeconds        int64  `json:"drainTimeoutSeconds"`
}

// PauseUpgradeRequest is the optional POST /upgrade/pause body carrying the
// §10.5 line 494 pause reason.
type PauseUpgradeRequest struct {
	Reason string `json:"reason"`
}

// RollbackUpgradeRequest is the POST /upgrade/rollback body. restoreOldPool
// recreates the old pool from previousPoolSpec when rolling back from
// Draining or Contracting (§24 line 70 / §10.5 line 507).
type RollbackUpgradeRequest struct {
	RestoreOldPool bool `json:"restoreOldPool"`
}

func (r *Router) handleUpgradeStart(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	var body StartUpgradeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	snap, err := r.runtimeUpgrade.Start(req.Context(), pool, runtimeupgrade.StartOptions{
		NewImage:                   body.NewImage,
		CanaryPercent:              body.CanaryPercent,
		SchemaVersion:              body.SchemaVersion,
		DrainFirst:                 body.DrainFirst,
		AutoAdvance:                body.AutoAdvance,
		StabilizationWindowSeconds: body.StabilizationWindowSeconds,
		DrainTimeoutSeconds:        body.DrainTimeoutSeconds,
	})
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	r.emitUpgrade(req, "admin.pool.upgrade_started", pool, map[string]any{
		"newImage": body.NewImage, "canaryPercent": body.CanaryPercent, "phase": snap.Phase,
	})
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) handleUpgradeProceed(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	snap, err := r.runtimeUpgrade.Proceed(req.Context(), pool)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	r.emitUpgrade(req, "admin.pool.upgrade_advanced", pool, map[string]any{"phase": snap.Phase})
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) handleUpgradePause(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	var body PauseUpgradeRequest
	// The reason body is optional; an empty/absent body is valid.
	_ = json.NewDecoder(req.Body).Decode(&body)
	snap, err := r.runtimeUpgrade.Pause(req.Context(), pool, body.Reason)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	r.emitUpgrade(req, "admin.pool.upgrade_paused", pool, map[string]any{"reason": body.Reason, "priorPhase": snap.PriorPhase})
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) handleUpgradeResume(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	snap, err := r.runtimeUpgrade.Resume(req.Context(), pool)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	r.emitUpgrade(req, "admin.pool.upgrade_resumed", pool, map[string]any{"phase": snap.Phase})
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) handleUpgradeRollback(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	var body RollbackUpgradeRequest
	_ = json.NewDecoder(req.Body).Decode(&body)
	snap, err := r.runtimeUpgrade.Rollback(req.Context(), pool, body.RestoreOldPool)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	r.emitUpgrade(req, "admin.pool.upgrade_rolled_back", pool, map[string]any{
		"restoreOldPool": body.RestoreOldPool, "phase": snap.Phase,
	})
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) handleUpgradeStatus(w http.ResponseWriter, req *http.Request) {
	pool := req.PathValue("name")
	snap, ok, err := r.runtimeUpgrade.Status(req.Context(), pool)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "no runtime upgrade registered for this pool", nil)
		return
	}
	writeJSON(w, http.StatusOK, upgradeStatusOf(snap))
}

func (r *Router) emitUpgrade(req *http.Request, eventType, pool string, detail map[string]any) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		return
	}
	r.emit(req.Context(), principal, eventType, pool, detail)
}

// writeUpgradeError maps a §10.5 upgrade error to its HTTP status. A
// missing pool or upgrade is 404; an out-of-order transition, paused/
// terminal proceed, disallowed rollback, active-upgrade start, or a
// concurrent version write is 409; a bad argument is 400.
func writeUpgradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtimeupgrade.ErrPoolNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, runtimeupgrade.ErrUpgradeNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, runtimeupgrade.ErrInvalidImage):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	case errors.Is(err, runtimeupgrade.ErrUpgradeActive):
		// spec: §15.1 line 981 — an upgrade-in-progress state conflict is INVALID_STATE_TRANSITION.
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION", err.Error(), nil)
	case errors.Is(err, runtimeupgradestore.ErrConflict):
		writeError(w, http.StatusConflict, "CONCURRENT_MODIFICATION",
			"the upgrade phase changed concurrently; re-read and retry", nil)
	case errors.Is(err, runtimeupgrade.ErrTerminal),
		errors.Is(err, runtimeupgrade.ErrPaused),
		errors.Is(err, runtimeupgrade.ErrRollbackNotAllowed),
		errors.Is(err, state.ErrNotPaused),
		errors.Is(err, state.ErrAlreadyPaused),
		errors.Is(err, state.ErrCannotPauseTerminal),
		errors.Is(err, state.ErrCurrentlyPaused):
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION", err.Error(), nil)
	default:
		var ite *state.InvalidTransitionError
		if errors.As(err, &ite) {
			writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
