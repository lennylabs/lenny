// SPDX-License-Identifier: MIT

package escalation

import (
	"context"
	"errors"
	"time"
)

// Query-path cursorKind values (§25.4 Storage Tiers, Query Path). The
// durable Postgres tier keyset-paginates over the ops_escalations ordering
// and stamps CursorKindPK; the Redis scan and in-memory buffer paginate by
// limit only, with no continuation cursor, and stamp CursorKindNone.
//
// spec: §25.4 lines 2427-2429.
const (
	CursorKindPK   = "pk"
	CursorKindNone = "none"
)

// ListPage is the result of a §25.4 escalation query-path List: one page of
// records newest-first plus the canonical pagination metadata the list
// endpoint returns in its envelope. NextCursor is an opaque continuation
// token, empty on the CursorKindNone paths and on the final Postgres page.
//
// spec: §25.4 lines 2425-2429 (Storage Tiers, Query Path).
type ListPage struct {
	Items      []Escalation
	NextCursor string
	HasMore    bool
	CursorKind string
}

// ErrStoreUnavailable signals that an escalation Store's backend is
// unreachable. The tiered Service treats it as "fall to the next tier"
// on the create path and "skip this store" on the query path, matching
// the §25.4 tiered-storage fallback contract. Any other error from a
// Store is a real failure and propagates.
var ErrStoreUnavailable = errors.New("escalation store unavailable")

// Store is one §25.4 escalation persistence tier. The Service composes
// an ordered set of durable Stores (Postgres, then Redis) over an
// always-present in-memory MemStore, attempting each in order so an
// escalation can always be recorded — including during the storage
// outages most likely to trigger one.
//
// Get and SetStatus return (nil, nil) when the escalation is absent from
// that store. A store whose backend is unreachable returns
// ErrStoreUnavailable from every method.
//
// spec: §25.4 lines 2376-2429.
type Store interface {
	// Tier returns this store's persistence label
	// (PersistenceDurablePostgres, PersistenceDurableRedis, or
	// PersistenceBufferedMemory). It is the value stamped onto a record's
	// Persistence field when this store accepts it.
	Tier() string
	// Put inserts or replaces esc by ID. It is idempotent: re-putting the
	// same ID overwrites the row, so the reconciliation flush is a no-op
	// when the destination store already holds the record (§25.4 line
	// 2413).
	Put(ctx context.Context, esc Escalation) error
	// Get returns the escalation by id, or (nil, nil) when absent.
	Get(ctx context.Context, id string) (*Escalation, error)
	// List returns the escalations matching f, newest-first, as one page
	// capped by limit (a non-positive limit applies no cap). cursor is an
	// opaque continuation token from a prior page's NextCursor; an empty
	// cursor starts at the head. The returned ListPage carries the store's
	// CursorKind and, on the durable Postgres tier, a NextCursor and
	// HasMore that let a caller advance page by page (§25.4 query path).
	List(ctx context.Context, f Filter, cursor string, limit int) (ListPage, error)
	// SetStatus moves the escalation's lifecycle status, stamping the
	// updated/acknowledged/resolved timestamps from now. It returns
	// (nil, nil) when the escalation is absent.
	SetStatus(ctx context.Context, id, status string, now time.Time) (*Escalation, error)
	// SetEmitted flips the escalation's emitted flag to true. It is a
	// no-op when the escalation is absent.
	SetEmitted(ctx context.Context, id string) error
	// PendingEmission returns the escalations whose emitted flag is still
	// false, for the §25.4 background emission retry.
	PendingEmission(ctx context.Context) ([]Escalation, error)
}

// AuditSink receives the §25.4 remediation.escalation_persisted audit
// event emitted on each successful reconciliation flush. The durable
// audit-append destination is the cross-cutting lenny-ops audit-store
// seam (consistent with the backup and drift audit sinks); a nil sink
// drops the event.
//
// spec: §25.4 line 2415.
type AuditSink interface {
	// EscalationPersisted records a flush of escalation id from the
	// sourceTier store up to the destTier store, taking durationMS.
	EscalationPersisted(ctx context.Context, id, sourceTier, destTier string, durationMS int64)
}
