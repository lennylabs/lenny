// SPDX-License-Identifier: MIT

// Package partialmanifeststore persists the §4.4 lines 234 / 236
// partial-checkpoint manifest row the gateway writes when an eviction
// checkpoint exceeds the preStop tiered cap and the workspace upload
// is incomplete. The row is the §7.2 resume path's recovery aid: it
// records the MinIO prefix under which the partially-uploaded chunks
// live so the resume coordinator can attempt a partial workspace
// reconstruction and, regardless of outcome, clean up the chunk
// objects and soft-delete the row.
//
// The §10.1 partial-upload pipeline carries a larger column set
// (chunk_count, workspace_bytes_uploaded,
// baseline_full_checkpoint_bytes, etc.); the v1 store implements
// only the §4.4 fields the cleanup path needs. The §10.1 columns
// land in a follow-on phase.
//
// spec: §4.4 lines 234, 236.
package partialmanifeststore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ChunkEncoding is the §10.1 closed enum of chunk wire encodings the
// adapter may write. The resume-time decode pipeline reads this off
// the manifest row rather than inferring from the object-key suffix.
type ChunkEncoding string

const (
	ChunkEncodingTar   ChunkEncoding = "tar"
	ChunkEncodingTarGz ChunkEncoding = "tar.gz"
)

// IsValid reports whether e is one of the spec-defined chunk
// encodings.
func (e ChunkEncoding) IsValid() bool {
	return e == ChunkEncodingTar || e == ChunkEncodingTarGz
}

// Record is one partial-manifest row.
type Record struct {
	// TenantID and SessionID identify the session this manifest
	// belongs to.
	TenantID  string
	SessionID string
	// Generation distinguishes successive partial manifests for the
	// same (tenant, session) — typically the session's recovery
	// generation at the time the partial was captured. The resume
	// path selects the max generation under `deleted_at IS NULL` so
	// late-committed older-generation rows cannot win against a
	// fenced newer-generation writer.
	Generation int64
	// PartialObjectKeyPrefix is the MinIO prefix under which the
	// committed chunks live, of the form
	// `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/`.
	PartialObjectKeyPrefix string
	// ChunkEncoding is the wire encoding the adapter wrote chunks
	// with. Decode pipelines key off this column.
	ChunkEncoding ChunkEncoding
	// CreatedAt is set on first Put for the (tenant, session,
	// generation) triple and is immutable thereafter.
	CreatedAt time.Time
	// DeletedAt is the §4.4 line 236 soft-delete tombstone. Zero
	// while the row is active; set by `SoftDelete` to the cleanup
	// wall-clock and never unset.
	DeletedAt time.Time
}

// ErrNotFound is returned by Get / SoftDelete when no row matches the
// supplied composite key.
var ErrNotFound = errors.New("partialmanifeststore: row not found")

// ErrStaleGeneration is returned by Put when an active manifest with a
// higher generation already exists for the same (tenant, session). The
// write is a fenced stale-coordinator attempt: honoring it would
// downgrade the active manifest below the highest fenced generation,
// which the §10.1 line 155 MAX(coordination_generation) resume selector
// (CPS-006) forbids. The losing writer rolls back and re-reads.
//
// spec: §10.1 lines 137, 155.
var ErrStaleGeneration = errors.New("partialmanifeststore: a higher-generation active manifest already exists")

// Store persists partial-manifest rows. The in-memory MemoryStore
// backs unit tests and the developer-mode deployment; the
// Postgres-backed implementation lives in
// pkg/gateway/partialmanifeststore/pgstore.
//
// The DeleteByUser / DeleteByTenant methods carry the §12.8 GDPR
// erasure contract.
type Store interface {
	// Put inserts a fresh row. The composite key is
	// (tenant_id, session_id, generation). A second Put for the
	// same key is treated as an idempotent re-write: the existing
	// row's prefix / encoding fields are refreshed and CreatedAt is
	// preserved. Returns an error when any required field is empty.
	//
	// Put enforces the §10.1 supersede-on-write invariant: a new
	// active manifest soft-deletes every lower-generation active row
	// for the same (tenant, session) in the same transaction, so the
	// table never holds two `deleted_at IS NULL` rows for one
	// (tenant, session) pair (the `partial_manifest_active_uniq`
	// partial unique index, migration 0150). A write whose generation
	// is below an already-active higher generation is a fenced
	// stale-coordinator write and is rejected with ErrStaleGeneration
	// so it cannot downgrade the active manifest (§10.1 line 155
	// MAX(coordination_generation) fencing, CPS-006).
	//
	// spec: §10.1 lines 137, 143-151, 155.
	Put(ctx context.Context, r Record) error

	// Get returns the row for (tenantID, sessionID, generation) or
	// ErrNotFound. A soft-deleted row is still returned (DeletedAt
	// is non-zero); the caller decides whether to skip.
	Get(ctx context.Context, tenantID, sessionID string, generation int64) (Record, error)

	// LatestActive returns the row with the highest generation for
	// (tenantID, sessionID) whose DeletedAt is zero. The §7.2 resume
	// path uses this to pick the latest active manifest. Returns
	// ErrNotFound when no active row exists.
	LatestActive(ctx context.Context, tenantID, sessionID string) (Record, error)

	// SoftDelete sets `deleted_at = now()` on the row under the
	// `deleted_at IS NULL` predicate. The row is the §4.4 line 236
	// idempotency guard: a stale-leader retry, a crash-resumed
	// resume path, or the §12.5 backstop sweep that races the
	// primary cleanup all observe `rows_affected == 0` on the
	// second writer and the read paths converge to a single state
	// mutation. Returns nil with no error when the row is already
	// soft-deleted or missing — the cleanup path is idempotent.
	SoftDelete(ctx context.Context, tenantID, sessionID string, generation int64) error

	// ListSoftDeletedBefore returns every row whose DeletedAt is
	// older than cutoff. The §12.5 hard-prune sweep walks the
	// result to remove tombstoned rows from the table.
	ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]Record, error)

	// HardDelete removes the row from the table entirely. The
	// §12.5 hard-prune sweep calls this on rows whose DeletedAt is
	// older than the tombstone retention window.
	HardDelete(ctx context.Context, tenantID, sessionID string, generation int64) error

	// DeleteByUser removes every row in tenantID whose session id
	// is in sessionIDs. Called by the §12.8 erasure orchestrator.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant removes every row scoped to tenantID. Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// MemoryStore is an in-memory Store backing tests and the
// developer-mode deployment.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[recordKey]Record
	now  func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. now selects the
// timestamp source; a nil now uses time.Now.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[recordKey]Record{}, now: now}
}

// recordKey is the in-memory composite key.
type recordKey struct {
	tenantID   string
	sessionID  string
	generation int64
}

// Put inserts or refreshes a partial-manifest row, enforcing the §10.1
// supersede-on-write invariant.
func (m *MemoryStore) Put(_ context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return fmt.Errorf("partialmanifeststore: tenant and session ids are required")
	}
	if r.PartialObjectKeyPrefix == "" {
		return fmt.Errorf("partialmanifeststore: partial_object_key_prefix is required")
	}
	if r.ChunkEncoding == "" {
		r.ChunkEncoding = ChunkEncodingTar
	}
	if !r.ChunkEncoding.IsValid() {
		return fmt.Errorf("partialmanifeststore: invalid chunk_encoding %q", r.ChunkEncoding)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	// §10.1 line 155 fencing: reject a write whose generation sits below
	// an already-active higher generation before mutating anything, so a
	// stale-coordinator write never downgrades the active manifest.
	for key, row := range m.rows {
		if key.tenantID != r.TenantID || key.sessionID != r.SessionID {
			continue
		}
		if row.DeletedAt.IsZero() && key.generation > r.Generation {
			return ErrStaleGeneration
		}
	}
	k := recordKey{r.TenantID, r.SessionID, r.Generation}
	existing, exists := m.rows[k]
	if exists && !existing.DeletedAt.IsZero() {
		// Re-Put on a soft-deleted row is rejected: a partial
		// manifest, once cleaned up, is terminal.
		return fmt.Errorf("partialmanifeststore: row already soft-deleted")
	}
	// §10.1 line 137 supersede-on-write: soft-delete every lower-generation
	// active row for this (tenant, session) so at most one row stays
	// `deleted_at IS NULL` (partial_manifest_active_uniq, migration 0150).
	for key, row := range m.rows {
		if key.tenantID != r.TenantID || key.sessionID != r.SessionID {
			continue
		}
		if row.DeletedAt.IsZero() && key.generation < r.Generation {
			row.DeletedAt = now
			m.rows[key] = row
		}
	}
	if exists {
		r.CreatedAt = existing.CreatedAt
	} else {
		r.CreatedAt = now
	}
	m.rows[k] = r
	return nil
}

// Get returns the row or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, tenantID, sessionID string, generation int64) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[recordKey{tenantID, sessionID, generation}]
	if !ok {
		return Record{}, ErrNotFound
	}
	return row, nil
}

// LatestActive returns the highest-generation active row for
// (tenantID, sessionID).
func (m *MemoryStore) LatestActive(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Record
	found := false
	for k, row := range m.rows {
		if k.tenantID != tenantID || k.sessionID != sessionID {
			continue
		}
		if !row.DeletedAt.IsZero() {
			continue
		}
		if !found || row.Generation > best.Generation {
			best = row
			found = true
		}
	}
	if !found {
		return Record{}, ErrNotFound
	}
	return best, nil
}

// SoftDelete sets the tombstone under the `deleted_at IS NULL`
// idempotency guard.
func (m *MemoryStore) SoftDelete(_ context.Context, tenantID, sessionID string, generation int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := recordKey{tenantID, sessionID, generation}
	row, ok := m.rows[k]
	if !ok {
		// The cleanup path is idempotent — a missing row is treated
		// as already-deleted; the gauge metric distinguishes
		// `success` from `gc_collected` upstream.
		return nil
	}
	if !row.DeletedAt.IsZero() {
		// Already soft-deleted; the second writer observes
		// `rows_affected == 0` and skips side effects.
		return nil
	}
	row.DeletedAt = m.now()
	m.rows[k] = row
	return nil
}

// ListSoftDeletedBefore returns every soft-deleted row whose
// DeletedAt is older than cutoff.
func (m *MemoryStore) ListSoftDeletedBefore(_ context.Context, cutoff time.Time) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for _, row := range m.rows {
		if row.DeletedAt.IsZero() {
			continue
		}
		if row.DeletedAt.Before(cutoff) {
			out = append(out, row)
		}
	}
	return out, nil
}

// HardDelete removes the row entirely.
func (m *MemoryStore) HardDelete(_ context.Context, tenantID, sessionID string, generation int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, recordKey{tenantID, sessionID, generation})
	return nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice. The orchestrator owns the session-id lookup
// because session_partial_checkpoint_manifest does not carry a
// user_id column.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.rows {
		if k.tenantID != tenantID {
			continue
		}
		for _, sessID := range sessionIDs {
			if k.sessionID == sessID {
				delete(m.rows, k)
			}
		}
	}
	return nil
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (m *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.rows {
		if k.tenantID == tenantID {
			delete(m.rows, k)
		}
	}
	return nil
}
