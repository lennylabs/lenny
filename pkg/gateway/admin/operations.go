// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// OperationsInventory is the §4.0 / §25.4 inventory read surface the
// admin Router exposes at /v1/admin/operations and
// /v1/admin/operations/{id}. *operations.Inventory satisfies it.
type OperationsInventory interface {
	List(ctx context.Context, filter operations.Filter, limit int) operations.Page
	Get(ctx context.Context, id string) (*operations.Operation, []string, bool)
}

// WithOperationsInventory wires the §4.0 / §25.4 unified Operations
// Inventory onto the Router. Call before Handler() so the mux picks up
// the operations endpoints.
func (r *Router) WithOperationsInventory(inv OperationsInventory) *Router {
	r.operationsInventory = inv
	return r
}

// handleListOperations implements GET /v1/admin/operations — the §25.4
// scatter-gather Inventory query. Filters: ?actor=, ?status=, ?kind=,
// ?since=, ?until=, ?tenantId=, ?operationId=, ?limit=, ?cursor=.
func (r *Router) handleListOperations(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	params, err := conventions.ParsePageParams(q, "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	filter := operations.Filter{
		Actor:       q.Get("actor"),
		OperationID: q.Get("operationId"),
		TenantID:    q.Get("tenantId"),
		Since:       params.Since,
		Until:       params.Until,
	}
	if s := q.Get("status"); s != "" {
		filter.Statuses = parseStatuses(s)
	}
	if k := q.Get("kind"); k != "" {
		filter.Kinds = parseKinds(k)
	}
	page := r.operationsInventory.List(req.Context(), filter, params.Limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// handleGetOperation implements GET /v1/admin/operations/{id} — the
// §25.4 single-operation lookup that scatters across the same sources
// the inventory uses. A missing id returns OPERATION_NOT_FOUND.
func (r *Router) handleGetOperation(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"operation id is required", nil)
		return
	}
	op, warns, ok := r.operationsInventory.Get(req.Context(), id)
	if !ok {
		if len(warns) > 0 {
			// §25.4 207 partial: at least one source was unreachable, so
			// "not found" cannot be definitively reported. Surface the
			// degradation envelope alongside the not-found body.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMultiStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": conventions.NewError(
					"OPERATIONS_INVENTORY_PARTIAL", conventions.CategoryTransient,
					"operation lookup partial because one or more sources were unreachable").Error,
				"degradation": &conventions.Degradation{
					Level:    conventions.DegradationDegraded,
					Warnings: warns,
				},
			})
			return
		}
		writeError(w, http.StatusNotFound, "OPERATION_NOT_FOUND",
			"no operation found with id "+id, nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"operation": op}
	if len(warns) > 0 {
		resp["degradation"] = &conventions.Degradation{
			Level:    conventions.DegradationDegraded,
			Warnings: warns,
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// parseStatuses parses a CSV of §25.4 statuses, dropping unknown
// entries. "all" expands to every status.
func parseStatuses(csv string) []operations.Status {
	all := []operations.Status{
		operations.StatusInProgress, operations.StatusPaused, operations.StatusHeld,
		operations.StatusAwaitingFlush, operations.StatusFailed, operations.StatusCompleted,
	}
	parts := strings.Split(csv, ",")
	out := make([]operations.Status, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "all" {
			return all
		}
		s := operations.Status(v)
		for _, allowed := range all {
			if s == allowed {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// parseKinds parses a CSV of §25.4 operation kinds, dropping unknown
// entries. "all" expands to every kind.
func parseKinds(csv string) []operations.Kind {
	all := []operations.Kind{
		operations.KindPlatformUpgrade, operations.KindRestore,
		operations.KindBackup, operations.KindBackupVerification,
		operations.KindEscalationOpen, operations.KindEscalationBuffered,
		operations.KindRemediationLock, operations.KindIdempotencyInProgress,
		operations.KindDriftReconciliation, operations.KindWebhookDeliveryPending,
	}
	parts := strings.Split(csv, ",")
	out := make([]operations.Kind, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "all" {
			return all
		}
		k := operations.Kind(v)
		for _, allowed := range all {
			if k == allowed {
				out = append(out, k)
				break
			}
		}
	}
	return out
}
