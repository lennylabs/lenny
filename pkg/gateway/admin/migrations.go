// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
)

// MigrationManager is the §15.1 / §24.13 schema-migration management
// seam. *schemamigrate.Manager satisfies it. Nil leaves the
// `/v1/admin/schema/migrations` endpoints unregistered.
//
// spec: §15.1 lines 891-892; §24.13 lines 150-151.
type MigrationManager interface {
	// Status returns the expand-contract migration state per
	// `GET /v1/admin/schema/migrations/status`.
	Status(ctx context.Context) (schemamigrate.StatusReport, error)
	// Down reverses the most recently applied migration at version per
	// `POST /v1/admin/schema/migrations/{version}/down`.
	Down(ctx context.Context, version uint) (schemamigrate.DownResult, error)
}

// WithMigrationManager wires the §15.1 schema-migration management
// endpoints onto the Router. Without it the endpoints are not
// registered.
func (r *Router) WithMigrationManager(m MigrationManager) *Router {
	r.migrations = m
	return r
}

// handleMigrationStatus implements
// GET /v1/admin/schema/migrations/status per §15.1 line 891. Requires
// platform-admin (the route is gated by requireAdmin).
func (r *Router) handleMigrationStatus(w http.ResponseWriter, req *http.Request) {
	rep, err := r.migrations.Status(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

// handleMigrationDown implements
// POST /v1/admin/schema/migrations/{version}/down per §15.1 line 892.
// The body `{"confirm": true, "reason": "<free text>"}` is required;
// the call is rejected with 422 CONFIRMATION_REQUIRED when confirm is
// absent or false. On success the §16.6 `platform.schema_migration_
// rolled_back` audit row is written.
func (r *Router) handleMigrationDown(w http.ResponseWriter, req *http.Request) {
	versionStr := req.PathValue("version")
	version, err := strconv.ParseUint(versionStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"version path segment must be a positive integer", nil)
		return
	}

	var body struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if req.ContentLength != 0 {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
			return
		}
	}
	if !body.Confirm {
		// spec: §15.1 line 892 — destructive rollback requires explicit
		// confirmation.
		writeError(w, http.StatusUnprocessableEntity, "CONFIRMATION_REQUIRED",
			`down-migration requires {"confirm": true}`, nil)
		return
	}

	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}

	res, err := r.migrations.Down(req.Context(), uint(version))
	if err != nil {
		switch {
		case errors.Is(err, schemamigrate.ErrNoVersion):
			writeError(w, http.StatusConflict, "NO_MIGRATION_APPLIED",
				"no migration has been applied; nothing to roll back", nil)
		case errors.Is(err, schemamigrate.ErrVersionMismatch):
			writeError(w, http.StatusConflict, "MIGRATION_VERSION_MISMATCH", err.Error(),
				map[string]any{"requestedVersion": version})
		default:
			writeError(w, http.StatusInternalServerError, "MIGRATION_ROLLBACK_FAILED", err.Error(), nil)
		}
		return
	}

	// spec: §16.6 — `platform.schema_migration_rolled_back` payload:
	// version, requester_sub, rollback_reason (free-text),
	// dirty_flag_cleared, advisory_locks_released.
	r.emit(req.Context(), principal, audit.EventPlatformSchemaMigrationRolledBack.String(),
		"schema/migrations/"+versionStr, map[string]any{
			"version":                 res.Version,
			"requester_sub":           principal.Subject,
			"rollback_reason":         body.Reason,
			"dirty_flag_cleared":      res.DirtyFlagCleared,
			"advisory_locks_released": res.AdvisoryLocksReleased,
		})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
