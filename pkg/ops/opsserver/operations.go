// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// OperationsInventory is the §25.4 Operations Inventory read surface the
// lenny-ops Server exposes at /v1/admin/operations and
// /v1/admin/operations/{id}. *operations.Inventory satisfies it; the
// interface keeps the handler testable with a stub.
type OperationsInventory interface {
	List(ctx context.Context, filter operations.Filter, limit int) operations.Page
	Get(ctx context.Context, id string) (*operations.Operation, []string, bool)
}

// §25.4 lines 1772-1775 Operations Inventory metrics. Registered on the
// default registry so the §16.9 lenny-ops /metrics endpoint exposes them.
var (
	inventoryRequestsTotal *prometheus.CounterVec
	inventoryKindsReturned prometheus.Histogram
)

func init() {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_operations_inventory_requests_total",
		Help: "§25.4: calls to the Operations Inventory endpoint, labelled " +
			"by the resolved actor kind (self, other, all).",
	}, []string{"actor_kind"})
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	inventoryRequestsTotal = c

	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_ops_operations_inventory_kinds_returned",
		Help: "§25.4: distribution of the number of distinct operation kinds " +
			"returned per Operations Inventory request.",
		Buckets: prometheus.LinearBuckets(0, 1, 11),
	})
	metrics.MustRegister(prometheus.DefaultRegisterer, h)
	inventoryKindsReturned = h
}

// §25.4 line 1779 audit event + line 1769 error code.
const (
	eventInventoryQueried   = "operations.inventory_queried"
	codeOperationNotFound   = "OPERATION_NOT_FOUND"
	codeInventoryPartial    = "OPERATIONS_INVENTORY_PARTIAL"
)

// registerOperationsRoutes wires the §25.4 Operations Inventory read
// endpoints. They are registered only when an Inventory is configured;
// a nil inventory leaves them unmapped (404), the cold-start posture for
// a deployment whose subsystem sources are not yet wired.
func (s *Server) registerOperationsRoutes() {
	if s.inventory == nil {
		return
	}
	s.mux.HandleFunc("GET /v1/admin/operations", s.handleListOperations)
	s.mux.HandleFunc("GET /v1/admin/operations/{id}", s.handleGetOperation)
}

// handleListOperations serves GET /v1/admin/operations: the §25.4
// scatter-gather Inventory query with the ?actor=, ?status=, ?kind=,
// ?since=, ?until=, ?tenantId=, ?operationId=, ?limit=, ?cursor= filters.
// The §25.4 authorization rules (line 1736) are applied after the
// scatter: tenant-admin callers are auto-restricted to their own
// operations and own-tenant operations; actor=* requires platform-admin.
func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params, err := conventions.ParsePageParams(q, "desc")
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, err.Error())
		return
	}
	var statuses []operations.Status
	if st := q.Get("status"); st != "" {
		statuses = parseOpStatuses(st)
	}
	var kinds []operations.Kind
	if k := q.Get("kind"); k != "" {
		kinds = parseOpKinds(k)
	}
	s.listOperations(w, r, listOptions{
		actor:       q.Get("actor"),
		operationID: q.Get("operationId"),
		tenantID:    q.Get("tenantId"),
		statuses:    statuses,
		kinds:       kinds,
		since:       params.Since,
		until:       params.Until,
		limit:       params.Limit,
	})
}

// listOptions carries the resolved §25.4 inventory query parameters.
type listOptions struct {
	actor       string
	operationID string
	tenantID    string
	statuses    []operations.Status
	kinds       []operations.Kind
	since       time.Time
	until       time.Time
	limit       int
}

// listOperations is the shared core of GET /v1/admin/operations and the
// GET /v1/admin/me/operations alias. It resolves the §25.4 effective
// actor, scatters across the inventory sources, applies the per-row
// authorization filter, records the metrics and audit event, and writes
// the page (207 when a source was unreachable).
func (s *Server) listOperations(w http.ResponseWriter, r *http.Request, opt listOptions) {
	p, hasPrincipal := callerPrincipal(r)
	actor := effectiveActor(p, hasPrincipal, opt.actor)
	filter := operations.Filter{
		Actor:       actor,
		OperationID: opt.operationID,
		Statuses:    opt.statuses,
		Kinds:       opt.kinds,
		TenantID:    explicitTenantFilter(p, hasPrincipal, opt.tenantID),
		Since:       opt.since,
		Until:       opt.until,
	}
	page := s.inventory.List(r.Context(), filter, opt.limit)
	page.Operations = authorizeOperationRows(p, hasPrincipal, actor, page.Operations)
	if page.Operations == nil {
		page.Operations = []operations.Operation{}
	}

	inventoryRequestsTotal.WithLabelValues(actorKind(actor)).Inc()
	inventoryKindsReturned.Observe(float64(distinctKinds(page.Operations)))
	s.recordOpsAudit(r, eventInventoryQueried, map[string]any{
		"actor":       actor,
		"resultCount": len(page.Operations),
		"statusFilter": statusFilterLabel(opt.statuses),
	})

	status := http.StatusOK
	if page.Degradation != nil {
		// §25.4 line 1769: OPERATIONS_INVENTORY_PARTIAL — at least one
		// subsystem's backing store was unreachable; the response is
		// partial per degradation.warnings.
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, page)
}

// handleGetOperation serves GET /v1/admin/operations/{id}: the §25.4
// single-operation lookup. A missing id returns OPERATION_NOT_FOUND; a
// lookup that could not be definitively resolved because a source was
// unreachable returns 207 OPERATIONS_INVENTORY_PARTIAL. The caller must
// pass the §25.4 authorization filter for the resolved operation.
func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "operation id is required")
		return
	}
	p, hasPrincipal := callerPrincipal(r)
	op, warns, ok := s.inventory.Get(r.Context(), id)
	if ok && !canSeeOperation(p, hasPrincipal, *op) {
		// §25.4 line 1745: surfacing an operation the caller may not see
		// is a not-found, not a forbidden — the caller learns nothing about
		// operations outside its visibility.
		ok = false
		op = nil
	}
	if !ok {
		if len(warns) > 0 {
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, http.StatusMultiStatus, map[string]any{
				"error": conventions.NewError(codeInventoryPartial, conventions.CategoryTransient,
					"operation lookup partial because one or more sources were unreachable").Error,
				"degradation": &conventions.Degradation{
					Level:    conventions.DegradationDegraded,
					Warnings: warns,
				},
			})
			return
		}
		conventions.WriteError(w, http.StatusNotFound, codeOperationNotFound,
			conventions.CategoryPermanent, "no operation found with id "+id)
		return
	}
	resp := map[string]any{"operation": op}
	if len(warns) > 0 {
		resp["degradation"] = &conventions.Degradation{
			Level:    conventions.DegradationDegraded,
			Warnings: warns,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// effectiveActor resolves the §25.4 ?actor= value against the caller's
// role. The default is "me". A tenant-admin caller is auto-restricted to
// "me" regardless of the requested value (§25.4 line 1789 — "Tenant-admin
// is auto-restricted to me"). When no principal is present (dev / embedded
// path) the requested value is honored as-is.
func effectiveActor(p authmw.Principal, hasPrincipal bool, requested string) string {
	if requested == "" {
		requested = "me"
	}
	if !hasPrincipal {
		return requested
	}
	if !p.HasRole(auth.RolePlatformAdmin) {
		return "me"
	}
	return requested
}

// explicitTenantFilter resolves the §25.4 ?tenantId= narrowing. A
// platform-admin's explicit value narrows the inventory (an AND filter); a
// tenant-admin's own-tenant visibility is an OR with started_by, so it is
// NOT expressed as an inventory-level TenantID filter (that would drop the
// tenant-admin's own started platform-scoped operations) — it is applied
// per-row in authorizeOperationRows instead.
func explicitTenantFilter(p authmw.Principal, hasPrincipal bool, requested string) string {
	if !hasPrincipal {
		return requested
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		return requested
	}
	return ""
}

// authorizeOperationRows applies the §25.4 line 1736 per-row visibility
// rules to the scattered operations:
//   - platform-admin with actor=*: sees every operation.
//   - platform-admin with actor=<sub>: sees operations started by that sub.
//   - actor=me (default, and the forced value for tenant-admin): sees
//     operations the caller started, plus — for a tenant-admin — operations
//     carrying a tenantId matching the caller's tenant.
//
// The dev / embedded path (no principal) returns the rows unfiltered.
func authorizeOperationRows(p authmw.Principal, hasPrincipal bool, actor string, ops []operations.Operation) []operations.Operation {
	if !hasPrincipal {
		return ops
	}
	platformAdmin := p.HasRole(auth.RolePlatformAdmin)
	if platformAdmin && actor == "*" {
		return ops
	}
	out := make([]operations.Operation, 0, len(ops))
	for _, op := range ops {
		if platformAdmin && actor != "me" {
			// actor is a specific sub.
			if op.StartedBy == actor {
				out = append(out, op)
			}
			continue
		}
		if op.StartedBy == p.Subject {
			out = append(out, op)
			continue
		}
		if !platformAdmin && op.TenantID != "" && op.TenantID == p.TenantID {
			out = append(out, op)
		}
	}
	return out
}

// canSeeOperation reports whether the caller is authorized to see a single
// operation under the §25.4 visibility rules. A platform-admin sees every
// operation; a tenant-admin sees their own and their tenant's.
func canSeeOperation(p authmw.Principal, hasPrincipal bool, op operations.Operation) bool {
	if !hasPrincipal {
		return true
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		return true
	}
	if op.StartedBy == p.Subject {
		return true
	}
	return op.TenantID != "" && op.TenantID == p.TenantID
}

// actorKind maps the resolved §25.4 actor onto the metric label set
// (self, other, all).
func actorKind(actor string) string {
	switch actor {
	case "*":
		return "all"
	case "me", "":
		return "self"
	default:
		return "other"
	}
}

// distinctKinds counts the distinct operation kinds in a result set for
// the §25.4 lenny_ops_operations_inventory_kinds_returned histogram.
func distinctKinds(ops []operations.Operation) int {
	seen := make(map[operations.Kind]struct{}, len(ops))
	for _, op := range ops {
		seen[op.Kind] = struct{}{}
	}
	return len(seen)
}

// statusFilterLabel renders the resolved status set for the audit record.
func statusFilterLabel(statuses []operations.Status) string {
	if len(statuses) == 0 {
		statuses = operations.DefaultStatuses
	}
	parts := make([]string, len(statuses))
	for i, st := range statuses {
		parts[i] = string(st)
	}
	return strings.Join(parts, ",")
}

// emptyOperationsPage is the §25.4 empty Inventory page returned by the
// /me/operations alias when no inventory is wired.
func emptyOperationsPage() operations.Page {
	return operations.Page{
		Operations: []operations.Operation{},
		Pagination: conventions.Pagination{Limit: conventions.DefaultPageLimit},
	}
}

// parseOpStatuses parses a CSV of §25.4 statuses, dropping unknown
// entries. "all" expands to every status.
func parseOpStatuses(csv string) []operations.Status {
	all := []operations.Status{
		operations.StatusInProgress, operations.StatusPaused, operations.StatusHeld,
		operations.StatusAwaitingFlush, operations.StatusFailed, operations.StatusCompleted,
	}
	parts := strings.Split(csv, ",")
	out := make([]operations.Status, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "all" {
			return all
		}
		st := operations.Status(v)
		for _, allowed := range all {
			if st == allowed {
				out = append(out, st)
				break
			}
		}
	}
	return out
}

// parseOpKinds parses a CSV of §25.4 operation kinds, dropping unknown
// entries. "all" expands to every kind.
func parseOpKinds(csv string) []operations.Kind {
	all := []operations.Kind{
		operations.KindPlatformUpgrade, operations.KindRestore,
		operations.KindBackup, operations.KindBackupVerification,
		operations.KindEscalationOpen, operations.KindEscalationBuffered,
		operations.KindRemediationLock, operations.KindIdempotencyInProgress,
		operations.KindDriftReconciliation, operations.KindWebhookDeliveryPending,
	}
	parts := strings.Split(csv, ",")
	out := make([]operations.Kind, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
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
