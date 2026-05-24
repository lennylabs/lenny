// SPDX-License-Identifier: MIT

// Package evictionstatestore is the §12.2.1 EvictionStateStore role.
// It persists the minimal-state record written during the §4.4
// eviction-checkpoint fallback path: when MinIO is unreachable
// mid-checkpoint, the gateway falls back to recording the
// conversation cursor and last-message context here so a resumed
// session can replay its conversation without the workspace bytes.
//
// The §12.5 storage table is migration 0045
// (session_eviction_state). The MinIO storage of large contexts
// (records whose `last_message_context` is a `/{tenant_id}/eviction/`
// object key rather than inline JSON) lives in the existing
// pkg/blobstore; the GC sweep keys off the IsMinIOKey flag to decide
// whether a row removal triggers a MinIO delete.
//
// The Store interface ships with an in-memory backend the unit tests
// drive and the developer-mode deployment uses when no Postgres
// connection is configured. The Postgres-backed
// pkg/gateway/evictionstatestore/pgstore is the v1 production
// backend; it is a follow-on commit that wires the row reader and
// writer to the migrations/0045 table.
package evictionstatestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotFound is returned by Get and Delete when the supplied
// (tenant, session) pair has no eviction-state row.
var ErrNotFound = errors.New("evictionstatestore: row not found")

// Record is one eviction-state row. Fields mirror the §4.4
// lines 265–273 columns the §4.4 fallback writer populates when MinIO
// is unreachable mid-checkpoint and the gateway falls back to the
// Postgres minimal-state path.
type Record struct {
	// TenantID and SessionID together identify the session whose
	// eviction state this row carries.
	TenantID  string
	SessionID string

	// RecoveryGeneration is the §4.2 pod-recovery counter at the
	// moment of the eviction event. The §7.2 resume path reads it to
	// identify which generation of the session this eviction state
	// corresponds to.
	// spec: §4.4 line 268.
	RecoveryGeneration int64

	// CoordinationGeneration is the §4.2 coordinator-handoff counter
	// at the moment of the eviction event. The §7.2 resume path
	// reads it for §10.1 coordinator fencing on resume.
	// spec: §4.4 line 269.
	CoordinationGeneration int64

	// ConversationCursor is the last event cursor from the EventStore,
	// allowing the §7.2 resume path to replay the conversation log
	// from a known offset. Encoded as an opaque string the producer
	// and consumer agree on (typically the EventStore offset id).
	// spec: §4.4 line 270.
	ConversationCursor string

	// LastMessageContext is the inline JSON payload (when IsMinIOKey
	// is false) or the MinIO object key (when IsMinIOKey is true).
	// The §4.4 fallback writer chooses the form based on the §12.5
	// 2 KB threshold.
	LastMessageContext []byte

	// IsMinIOKey reports whether LastMessageContext stores a MinIO
	// object key rather than inline JSON. The §12.5 GC sweep uses
	// this to decide whether to delete the MinIO object alongside
	// the row.
	IsMinIOKey bool

	// EvictedAt is the wall-clock timestamp of the eviction event.
	// Nil / zero when the row was written without an explicit
	// eviction timestamp (legacy producers); production writers
	// always set this.
	// spec: §4.4 line 272.
	EvictedAt time.Time

	// WorkspaceLost reports whether the workspace bytes are gone for
	// this session. Always true for the §4.4 minimal-state record by
	// construction; the column is carried as the canonical signal so
	// the §7.2 session.resumed event can echo `workspaceLost: true`
	// directly from the row.
	// spec: §4.4 line 273.
	WorkspaceLost bool

	// ContextTruncated reports whether the §4.4 fallback writer
	// truncated the context to 2KB and stored it inline because
	// MinIO was unavailable. The §7.2 resume path surfaces this to
	// the runtime so it can detect partial context.
	// spec: §4.4 line 271.
	ContextTruncated bool

	// CreatedAt and UpdatedAt are the row's lifecycle timestamps.
	// CreatedAt is set on first Put for the (tenant, session);
	// UpdatedAt is bumped on every subsequent Put.
	CreatedAt time.Time
	UpdatedAt time.Time

	// DeletedAt is the §4.4 line 281 / line 236 soft-delete
	// tombstone. Zero while the row is active; set by Delete /
	// DeleteByUser / DeleteByTenant to the cleanup wall-clock and
	// never reset. The §12.5 hard-prune sweep (SweepDeletedBefore)
	// removes rows whose DeletedAt is older than the artifact_store
	// tombstone retention window.
	// spec: §4.4 line 281.
	DeletedAt time.Time
}

// Store persists §12.2.1 eviction-state records. The production
// implementation is the Postgres backend in pgstore; the in-memory
// MemoryStore in this package backs unit tests and the developer-mode
// deployment.
//
// The DeleteByUser and DeleteByTenant methods carry the §12.8 GDPR
// erasure contract. They are mandatory: the erasure orchestrator
// drives them in the documented dependency order. Per §4.4 line 281
// every delete path is a soft-delete: it stamps `deleted_at = now()`
// under a `deleted_at IS NULL` predicate so a stale-leader retry, a
// crash-resumed terminal-cleanup, or the §12.5 GC backstop racing the
// primary cleanup all observe `rows_affected == 0` on the second
// writer and converge to a single state mutation. SweepDeletedBefore
// is the §12.5 hard-prune surface the backstop sweep walks once the
// tombstone retention window has elapsed.
type Store interface {
	// Put upserts the record. CreatedAt is set on first insert;
	// UpdatedAt is bumped on every call.
	Put(ctx context.Context, r Record) error

	// Get returns the row for (tenant, session) or ErrNotFound.
	// Soft-deleted rows are NOT returned — Get filters by
	// `deleted_at IS NULL` so the §7.2 resume path never observes a
	// tombstoned record.
	Get(ctx context.Context, tenantID, sessionID string) (Record, error)

	// Delete stamps `deleted_at = now()` on the row under the
	// `deleted_at IS NULL` predicate. A missing or already-tombstoned
	// row is not an error so the terminal-state cleanup path is
	// idempotent against partial failures, stale-leader retries, and
	// the §12.5 GC backstop racing the primary cleanup.
	// spec: §4.4 line 281.
	Delete(ctx context.Context, tenantID, sessionID string) error

	// DeleteByUser soft-deletes every eviction-state row for the
	// user's sessions in the supplied tenant. The orchestrator looks
	// up the user's session ids upstream; this method tombstones the
	// rows that match.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant soft-deletes every row scoped to the supplied
	// tenant. Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error

	// SweepDeletedBefore hard-deletes every soft-deleted row whose
	// `deleted_at` is older than `cutoff` and returns the number of
	// rows removed. The §12.5 GC backstop runs once per retention
	// cycle after the tombstone retention window has elapsed so the
	// row can be physically removed in tandem with the
	// `artifact_store` row that mirrors it.
	// spec: §4.4 line 281 / §12.5 GC concurrency model rule 6.
	SweepDeletedBefore(ctx context.Context, cutoff time.Time) (int, error)
}

// MemoryStore is an in-memory Store backing the v1 developer-mode
// deployment and the unit tests. Lookups are O(1) by composite key.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Record
	now  func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. now selects the
// timestamp source; a nil now uses time.Now.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[string]Record{}, now: now}
}

// Put upserts the record. CreatedAt is set on first insert; UpdatedAt
// is bumped on every call. Re-Put on a soft-deleted row clears the
// tombstone so a session that re-enters the eviction-fallback path
// after its prior terminal-state cleanup observes a live row again.
func (m *MemoryStore) Put(_ context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return fmt.Errorf("evictionstatestore: tenant and session ids are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := compositeKey(r.TenantID, r.SessionID)
	now := m.now()
	r.UpdatedAt = now
	if existing, ok := m.rows[key]; ok {
		r.CreatedAt = existing.CreatedAt
	} else {
		r.CreatedAt = now
	}
	// A new Put resurrects a tombstoned row — spec: §4.4 idempotent
	// re-runs of the fallback writer must not surface the prior
	// tombstone to the §7.2 resume path.
	r.DeletedAt = time.Time{}
	m.rows[key] = r
	return nil
}

// Get returns the row or ErrNotFound. Soft-deleted rows are skipped
// per §4.4 line 281 — the §7.2 resume path never observes a
// tombstoned record.
func (m *MemoryStore) Get(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[compositeKey(tenantID, sessionID)]
	if !ok {
		return Record{}, ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return Record{}, ErrNotFound
	}
	return row, nil
}

// Delete stamps `deleted_at = now()` on the row under the
// `deleted_at IS NULL` predicate. A missing or already-tombstoned row
// is a no-op so the terminal-state cleanup path is idempotent.
// spec: §4.4 line 281.
func (m *MemoryStore) Delete(_ context.Context, tenantID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := compositeKey(tenantID, sessionID)
	row, ok := m.rows[key]
	if !ok {
		return nil
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = m.now()
	m.rows[key] = row
	return nil
}

// DeleteByUser soft-deletes every row in tenantID whose session id is
// in the supplied slice. The orchestrator owns the session-id lookup
// because the EvictionStateStore does not carry a user_id column.
// spec: §4.4 line 281.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for _, sessID := range sessionIDs {
		key := compositeKey(tenantID, sessID)
		row, ok := m.rows[key]
		if !ok || !row.DeletedAt.IsZero() {
			continue
		}
		row.DeletedAt = now
		m.rows[key] = row
	}
	return nil
}

// DeleteByTenant soft-deletes every row scoped to tenantID.
// Idempotent. spec: §4.4 line 281.
func (m *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, row := range m.rows {
		if row.TenantID != tenantID {
			continue
		}
		if !row.DeletedAt.IsZero() {
			continue
		}
		row.DeletedAt = now
		m.rows[k] = row
	}
	return nil
}

// SweepDeletedBefore hard-deletes every soft-deleted row whose
// `deleted_at` is strictly before `cutoff` and returns the number of
// rows removed. The §12.5 GC backstop runs this once per retention
// cycle after the tombstone window has elapsed.
// spec: §4.4 line 281 / §12.5 GC concurrency model rule 6.
func (m *MemoryStore) SweepDeletedBefore(_ context.Context, cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int
	for k, row := range m.rows {
		if row.DeletedAt.IsZero() {
			continue
		}
		if row.DeletedAt.Before(cutoff) {
			delete(m.rows, k)
			removed++
		}
	}
	return removed, nil
}

// compositeKey is the in-memory primary key: (tenant_id, session_id).
func compositeKey(tenantID, sessionID string) string {
	return tenantID + "\x00" + sessionID
}
