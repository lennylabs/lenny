// SPDX-License-Identifier: MIT

// Package runtimeupgradeguard serves the §10.5 GET
// /internal/runtime-upgrade/active endpoint. Two §10.5 cluster-side
// safety controls read it:
//
//   - The lenny-sandboxtemplate-deletion-guard ValidatingAdmissionWebhook
//     queries it before admitting a SandboxTemplate DELETE, so the old
//     template cannot be deleted while a RuntimeUpgrade record that
//     references its pool is still active (§10.5 line 508, the "key
//     safety invariant").
//   - A Phase 3 (contract) schema migration consults the same record's
//     `schemaGated` flag and refuses to run while the upgrade is not
//     Complete for the referenced pool (§10.5 line 502).
//
// The endpoint reads the durable runtimeupgradestore record for one
// pool. An upgrade is active until it reaches the terminal Complete
// phase; a paused upgrade is still active because its old template must
// stay deletable only through the state machine.
package runtimeupgradeguard

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
	"github.com/lennylabs/lenny/pkg/runtime/upgrade/state"
)

// Lookup reads the durable §10.5 RuntimeUpgrade record for one pool.
// Both *runtimeupgradestore.Memory and the Postgres-backed store satisfy
// it, so the gateway swaps the backend without changing the handler.
type Lookup interface {
	Get(ctx context.Context, pool string) (runtimeupgradestore.Record, bool, error)
}

// Handler serves GET /internal/runtime-upgrade/active?pool=<name>.
type Handler struct {
	// Store reads the per-pool RuntimeUpgrade record. Required.
	Store Lookup
}

// activeResponse is the §10.5 HTTP 200 body. Active is true while a
// RuntimeUpgrade record exists for the pool and has not reached the
// terminal Complete phase. SchemaGated is true when, in addition, the
// upgrade carries a schemaVersion (§10.5 line 502): a Phase 3 migration
// for the pool must wait until the upgrade completes.
type activeResponse struct {
	Pool        string `json:"pool"`
	Active      bool   `json:"active"`
	Phase       string `json:"phase,omitempty"`
	SchemaGated bool   `json:"schemaGated"`
}

// ServeHTTP reports whether the pool named in the `pool` query parameter
// has an active RuntimeUpgrade. A store read failure returns HTTP 503 so
// the calling webhook denies the SandboxTemplate deletion fail-closed
// rather than admitting a delete it could not verify against the safety
// invariant.
//
// spec: §10.5 line 508 (deletion guard); §10.5 line 502 (Phase 3 gate).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "runtime-upgrade active endpoint accepts GET", http.StatusMethodNotAllowed)
		return
	}
	pool := r.URL.Query().Get("pool")
	if pool == "" {
		http.Error(w, "pool query parameter is required", http.StatusBadRequest)
		return
	}
	if h.Store == nil {
		http.Error(w, "runtime-upgrade store not configured", http.StatusServiceUnavailable)
		return
	}
	rec, ok, err := h.Store.Get(r.Context(), pool)
	if err != nil {
		http.Error(w, "runtime-upgrade lookup: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	resp := activeResponse{Pool: pool}
	if ok && rec.Phase != string(state.Complete) {
		resp.Active = true
		resp.Phase = rec.Phase
		resp.SchemaGated = rec.SchemaVersion != ""
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
