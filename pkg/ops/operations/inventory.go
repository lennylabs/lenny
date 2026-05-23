// SPDX-License-Identifier: MIT

// Package operations implements the §4.0 / §25.4 unified Operations
// Inventory: a scatter-gather aggregator over the per-subsystem
// operation sources (platform upgrades, restores, backups, remediation
// locks, escalations, idempotency keys, drift reconciliations, webhook
// deliveries). The aggregator owns no state — each Source projects its
// subsystem's existing records onto the canonical Operation envelope.
//
// The companion §25.2 ETA computation lives in eta.go.
package operations

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// Kind is the §25.4 closed enumeration of operation kinds the Inventory
// surfaces. Each kind has a documented prefix used by the canonical
// operationId format <kind-prefix>-<natural-key>.
type Kind string

const (
	KindPlatformUpgrade        Kind = "platform_upgrade"
	KindRestore                Kind = "restore"
	KindBackup                 Kind = "backup"
	KindBackupVerification     Kind = "backup_verification"
	KindEscalationOpen         Kind = "escalation_open"
	KindEscalationBuffered     Kind = "escalation_buffered"
	KindRemediationLock        Kind = "remediation_lock"
	KindIdempotencyInProgress  Kind = "idempotency_in_progress"
	KindDriftReconciliation    Kind = "drift_reconciliation"
	KindWebhookDeliveryPending Kind = "webhook_delivery_pending"
)

// Status is the §25.4 status enumeration. Multiple subsystem-specific
// states project onto these canonical statuses.
type Status string

const (
	StatusInProgress     Status = "in_progress"
	StatusPaused         Status = "paused"
	StatusHeld           Status = "held"
	StatusAwaitingFlush  Status = "awaiting_flush"
	StatusFailed         Status = "failed"
	StatusCompleted      Status = "completed"
)

// DefaultStatuses are the §25.4 default status filter when the request
// omits ?status=: in_progress, paused, held, awaiting_flush.
var DefaultStatuses = []Status{
	StatusInProgress, StatusPaused, StatusHeld, StatusAwaitingFlush,
}

// CorrelatedOperation is the §25.4 minimal reference to a sibling
// operation (e.g., the remediation lock the upgrade is correlated with).
type CorrelatedOperation struct {
	OperationID string `json:"operationId"`
	Kind        Kind   `json:"kind"`
	Scope       string `json:"scope,omitempty"`
}

// Operation is the §25.4 canonical Operations Inventory record. The
// JSON tags match the §25.4 response schema exactly so the HTTP layer
// can marshal it directly.
type Operation struct {
	OperationID          string                `json:"operationId"`
	Kind                 Kind                  `json:"kind"`
	Status               Status                `json:"status"`
	StartedBy            string                `json:"startedBy"`
	StartedAt            time.Time             `json:"startedAt"`
	TimeoutAt            *time.Time            `json:"timeoutAt,omitempty"`
	Progress             *conventions.Progress `json:"progress"`
	CorrelatedOperations []CorrelatedOperation `json:"correlatedOperations,omitempty"`
	Resources            map[string]string     `json:"resources"`
	Cancellable          bool                  `json:"cancellable"`
	TenantID             string                `json:"tenantId,omitempty"`
	Metadata             json.RawMessage       `json:"metadata,omitempty"`
}

// Source is the §25.4 scatter-gather contract one per subsystem
// implements. The gateway-side and lenny-ops-side Inventory iterate
// every registered Source and merge the results. A Source returning an
// error names a subsystem whose backing store is unreachable; the
// Inventory surfaces a §25.4 degradation warning for it.
type Source interface {
	// Kinds reports the §25.4 operation kinds this source produces. The
	// Inventory uses it to short-circuit a request that filters away
	// every kind this source owns.
	Kinds() []Kind
	// List projects the subsystem's records onto Operation envelopes
	// constrained by filter. A Source MAY ignore filters it does not
	// support; the Inventory applies the canonical filter pass over
	// the merged result.
	List(ctx context.Context, filter Filter) ([]Operation, error)
}

// Filter is the §25.4 set of operation-inventory filters. Empty fields
// do not narrow.
type Filter struct {
	// Actor is the §25.4 ?actor= value: "me", "*", or a specific
	// principal subject. The Inventory does not interpret it; sources
	// that key on caller identity (idempotency, restores) read it from
	// the filter to scope their projection.
	Actor string
	// Statuses narrows the kinds of status returned. An empty slice
	// applies the §25.4 default (DefaultStatuses).
	Statuses []Status
	// Kinds narrows the operation kinds. An empty slice returns every
	// kind.
	Kinds []Kind
	// Since / Until bound the operation's startedAt. A zero value
	// disables that bound.
	Since time.Time
	Until time.Time
	// OperationID lookups a specific operation across all sources.
	OperationID string
	// TenantID scopes the response to one tenant. Empty disables.
	TenantID string
}

// HasStatus reports whether s satisfies the filter's Statuses set. An
// empty Statuses slice applies the §25.4 default.
func (f Filter) HasStatus(s Status) bool {
	allowed := f.Statuses
	if len(allowed) == 0 {
		allowed = DefaultStatuses
	}
	for _, v := range allowed {
		if v == s {
			return true
		}
	}
	return false
}

// HasKind reports whether k satisfies the filter's Kinds set. An empty
// Kinds slice admits every kind per §25.4.
func (f Filter) HasKind(k Kind) bool {
	if len(f.Kinds) == 0 {
		return true
	}
	for _, v := range f.Kinds {
		if v == k {
			return true
		}
	}
	return false
}

// Inventory is the §4.0 unified Operations Inventory aggregator. It
// owns no state — sources are registered at construction time and the
// scatter-gather happens on every List call.
type Inventory struct {
	mu      sync.RWMutex
	sources []Source
}

// New returns a §4.0 Inventory with the given sources registered. A nil
// or empty slice yields an empty Inventory whose List returns an empty
// page.
func New(sources ...Source) *Inventory {
	return &Inventory{sources: append([]Source(nil), sources...)}
}

// Register adds src to the Inventory. It is safe to call after New;
// subsequent List calls observe the new source.
func (i *Inventory) Register(src Source) {
	if src == nil {
		return
	}
	i.mu.Lock()
	i.sources = append(i.sources, src)
	i.mu.Unlock()
}

// Page is the §25.4 Inventory response envelope.
type Page struct {
	Operations  []Operation              `json:"operations"`
	Pagination  conventions.Pagination   `json:"pagination"`
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
}

// SourceError pairs a source with the error it returned during List.
// The Inventory turns each entry into a §25.4 degradation warning.
type SourceError struct {
	Kinds []Kind
	Err   error
}

// List runs the §25.4 scatter-gather: every registered Source is asked
// for the operations it owns, the union is filtered against the request
// constraints, and the result is sorted by StartedAt descending. A
// Source returning an error becomes a §25.4 degradation warning rather
// than failing the entire request.
func (i *Inventory) List(ctx context.Context, filter Filter, limit int) Page {
	if limit <= 0 {
		limit = conventions.DefaultPageLimit
	}
	if limit > conventions.MaxPageLimit {
		limit = conventions.MaxPageLimit
	}
	i.mu.RLock()
	sources := append([]Source(nil), i.sources...)
	i.mu.RUnlock()

	var (
		all   []Operation
		errs  []SourceError
		warns []string
	)
	for _, src := range sources {
		if !sourceMatchesFilter(src, filter) {
			continue
		}
		ops, err := src.List(ctx, filter)
		if err != nil {
			errs = append(errs, SourceError{Kinds: src.Kinds(), Err: err})
			warns = append(warns, degradationWarning(src.Kinds(), err))
			continue
		}
		all = append(all, ops...)
	}

	out := filterAndSort(all, filter)
	if len(out) > limit {
		out = out[:limit]
	}
	page := Page{
		Operations: out,
		Pagination: conventions.Pagination{
			Limit:   limit,
			HasMore: len(all) > limit,
		},
	}
	if len(warns) > 0 {
		page.Degradation = &conventions.Degradation{
			Level:    conventions.DegradationDegraded,
			Warnings: warns,
		}
	}
	if page.Operations == nil {
		page.Operations = []Operation{}
	}
	return page
}

// Get returns the operation matching id from whichever source owns it.
// Returns (nil, false) when no source has the operation. A source that
// errs is reported via warns so the caller can surface the §25.4
// partial-result envelope; a successful match short-circuits the rest.
func (i *Inventory) Get(ctx context.Context, id string) (*Operation, []string, bool) {
	i.mu.RLock()
	sources := append([]Source(nil), i.sources...)
	i.mu.RUnlock()
	filter := Filter{OperationID: id}
	var warns []string
	for _, src := range sources {
		if !sourceMatchesPrefix(src, id) {
			continue
		}
		ops, err := src.List(ctx, filter)
		if err != nil {
			warns = append(warns, degradationWarning(src.Kinds(), err))
			continue
		}
		for _, op := range ops {
			if op.OperationID == id {
				return &op, warns, true
			}
		}
	}
	return nil, warns, false
}

// sourceMatchesFilter reports whether src may produce any operation the
// request would keep. A source owning only filtered-out kinds is
// skipped.
func sourceMatchesFilter(src Source, f Filter) bool {
	if len(f.Kinds) == 0 {
		return true
	}
	for _, k := range src.Kinds() {
		if f.HasKind(k) {
			return true
		}
	}
	return false
}

// sourceMatchesPrefix reports whether src's kinds carry the prefix the
// canonical operationId form encodes (e.g., "upgrade-..." for
// platform_upgrade). A source whose prefix does not match the id can
// short-circuit the Get scatter.
func sourceMatchesPrefix(src Source, id string) bool {
	for _, k := range src.Kinds() {
		if strings.HasPrefix(id, KindPrefix(k)+"-") {
			return true
		}
	}
	return false
}

// KindPrefix maps a §25.4 Kind to its canonical operationId prefix per
// the spec table.
func KindPrefix(k Kind) string {
	switch k {
	case KindPlatformUpgrade:
		return "upgrade"
	case KindRestore:
		return "restore"
	case KindBackup, KindBackupVerification:
		return "backup"
	case KindEscalationOpen, KindEscalationBuffered:
		return "esc"
	case KindRemediationLock:
		return "lock"
	case KindIdempotencyInProgress:
		return "idemp"
	case KindDriftReconciliation:
		return "drift-rec"
	case KindWebhookDeliveryPending:
		return "delivery"
	default:
		return ""
	}
}

// filterAndSort applies the canonical filter pass on the merged source
// output and orders the result by StartedAt descending (ties broken by
// OperationID ascending so the cursor is stable).
func filterAndSort(in []Operation, f Filter) []Operation {
	var out []Operation
	for _, op := range in {
		if f.OperationID != "" && op.OperationID != f.OperationID {
			continue
		}
		if !f.HasKind(op.Kind) {
			continue
		}
		if !f.HasStatus(op.Status) {
			continue
		}
		if !f.Since.IsZero() && op.StartedAt.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && op.StartedAt.After(f.Until) {
			continue
		}
		if f.TenantID != "" && op.TenantID != f.TenantID {
			continue
		}
		out = append(out, op)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].OperationID < out[j].OperationID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// degradationWarning formats the §25.4 partial-result message for an
// unreachable source: "Operations of kind 'X', 'Y' omitted: <reason>."
func degradationWarning(kinds []Kind, err error) string {
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, "'"+string(k)+"'")
	}
	return "Operations of kind " + strings.Join(parts, ", ") + " omitted: " + err.Error() + "."
}
