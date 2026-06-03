// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// ErrQuotaTenantNotFound is returned by a QuotaReconciler when a
// per-tenant reconcile names a tenant that does not exist. The handler
// maps it to 404 RESOURCE_NOT_FOUND.
var ErrQuotaTenantNotFound = errors.New("admin: quota reconcile tenant not found")

// QuotaReconciler is the §12.4 quota-counter reconciliation seam behind
// `POST /v1/admin/quota/reconcile`. After a Redis outage the operator
// (or the redis-failure / redis-sentinel-failover runbook) invokes this
// endpoint to re-aggregate in-flight session usage from the Postgres
// checkpoint into the Redis counters using the §12.4 line 216 two-source
// MAX rule (`MAX(postgres_checkpoint, in_memory_counter)`).
//
// The reconciler depends on the Postgres token-usage checkpoint store
// described in §11.2 ("Postgres is updated periodically at a configurable
// sync interval … as a durable checkpoint"). That checkpoint store is the
// subject of F-11.2.4 and is not yet wired into the gateway, so a
// production install leaves this seam nil and the endpoint returns 503
// QUOTA_RECONCILE_UNAVAILABLE — the same unavailable-seam convention the
// drift-reconcile endpoint uses. Once F-11.2.4 lands the checkpoint store,
// its reconciler satisfies this interface and the route reconciles for
// real.
//
// spec: §15.1 line 879; §24.6 line 99; §12.4 line 216; §11.2.
type QuotaReconciler interface {
	// Reconcile re-aggregates quota counters for the requested scope and
	// returns a per-tenant summary. It returns ErrQuotaTenantNotFound when
	// a per-tenant scope names a tenant that does not exist.
	Reconcile(ctx context.Context, scope QuotaReconcileScope) (QuotaReconcileResult, error)
}

// QuotaReconcileScope selects what the reconcile pass covers. Exactly one
// of AllTenants or TenantID is set; the handler rejects a request that
// sets neither or both.
type QuotaReconcileScope struct {
	// AllTenants reconciles every tenant with active quota counters. It
	// backs the spec-documented `--all-tenants` form. spec: §24.6 line 99.
	AllTenants bool
	// TenantID reconciles a single tenant. It backs the per-tenant
	// `--tenant <id>` form the redis-sentinel-failover runbook uses to
	// force a checkpoint reload for one tenant after a failover.
	TenantID string
}

// QuotaReconcileResult summarizes a reconcile pass. The per-tenant detail
// rows let an operator confirm that the MAX rule wrote the expected value
// rather than silently resetting a counter to a stale checkpoint.
type QuotaReconcileResult struct {
	TenantsReconciled int                          `json:"tenantsReconciled"`
	CountersWritten   int                          `json:"countersWritten"`
	Tenants           []QuotaTenantReconcileResult `json:"tenants,omitempty"`
}

// QuotaTenantReconcileResult records the §12.4 MAX-rule inputs and the
// authoritative value written back to Redis for one tenant.
type QuotaTenantReconcileResult struct {
	TenantID        string `json:"tenantId"`
	CheckpointValue int64  `json:"checkpointValue"`
	InMemoryValue   int64  `json:"inMemoryValue"`
	WrittenValue    int64  `json:"writtenValue"`
}

// WithQuotaReconciler wires the §12.4 reconciliation seam. A nil seam
// (the default until F-11.2.4 wires the Postgres checkpoint store) leaves
// the route registered but answering 503 QUOTA_RECONCILE_UNAVAILABLE, so
// the §24.6 CLI command always reaches a real endpoint.
func (r *Router) WithQuotaReconciler(qr QuotaReconciler) *Router {
	r.quotaReconciler = qr
	return r
}

// quotaReconcileRequest is the `POST /v1/admin/quota/reconcile` body.
type quotaReconcileRequest struct {
	AllTenants bool   `json:"allTenants"`
	TenantID   string `json:"tenantId"`
}

// handleQuotaReconcile implements `POST /v1/admin/quota/reconcile` — the
// §15.1 line 879 operator-driven quota-counter re-aggregation that the
// §24.6 `lenny-ctl admin quota reconcile` command and the Redis-recovery
// runbooks invoke. spec: §15.1 line 879; §24.6 line 99; §12.4 line 216.
func (r *Router) handleQuotaReconcile(w http.ResponseWriter, req *http.Request) {
	var body quotaReconcileRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}
	body.TenantID = strings.TrimSpace(body.TenantID)

	// Exactly one scope: the platform-wide sweep or a single tenant.
	switch {
	case body.AllTenants && body.TenantID != "":
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"set either allTenants or tenantId, not both", nil)
		return
	case !body.AllTenants && body.TenantID == "":
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"one of allTenants or tenantId is required", nil)
		return
	}

	// The reconciler is the F-11.2.4 Postgres-checkpoint seam. When it is
	// unwired the platform has no durable checkpoint to reconcile from, so
	// the endpoint reports the dependency rather than silently no-op'ing.
	if r.quotaReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, "QUOTA_RECONCILE_UNAVAILABLE",
			"quota reconciliation is unavailable: the Postgres token-usage checkpoint store is not wired on this gateway", nil)
		return
	}

	result, err := r.quotaReconciler.Reconcile(req.Context(), QuotaReconcileScope{
		AllTenants: body.AllTenants,
		TenantID:   body.TenantID,
	})
	if err != nil {
		if errors.Is(err, ErrQuotaTenantNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
