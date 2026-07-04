// SPDX-License-Identifier: MIT

// Package sessioncheckpointmeta persists the §10.1 CheckpointBarrier
// protocol's per-session barrier metadata: the barrier_id, the
// checkpoint_ref the barrier flush produced, and the
// workspace_recovery_fraction. The gateway writes a row after
// receiving a CheckpointBarrierAck during a graceful drain, so a later
// coordinator handoff can report the workspace-recovery fraction the
// row records (§10.1 line 393).
//
// The in-memory MemoryStore backs unit tests and the developer-mode
// deployment; the Postgres-backed implementation lives in the pgstore
// subpackage and writes the migration-0148 session_checkpoint_meta
// table under the §12.3 tenant-context RLS guard.
//
// spec: §10.1 line 393.
package sessioncheckpointmeta

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotFound is returned by Get when no row exists for the supplied
// (tenant, session) key.
var ErrNotFound = errors.New("sessioncheckpointmeta: row not found")

// Record is one session_checkpoint_meta row — the latest barrier
// metadata for a session.
type Record struct {
	// TenantID and SessionID identify the session.
	TenantID  string
	SessionID string
	// CoordinationGeneration is the coordinator's fenced generation at
	// BarrierAck time (§10.1 line 165).
	CoordinationGeneration int64
	// BarrierID is the gateway's monotonically-increasing-per-session
	// correlation id for the barrier that produced this row.
	BarrierID string
	// CheckpointRef is the MinIO checkpoint manifest the barrier flush
	// stored; empty when the best-effort flush produced no checkpoint.
	CheckpointRef string
	// WorkspaceRecoveryFraction is the §10.1 line 393 value the
	// coordinator-handoff `session.resumed` event reports. Nil when the
	// session had no prior full checkpoint to compute a fraction
	// against (the event omits the field per §7.2 optional-fraction
	// rules).
	WorkspaceRecoveryFraction *float64
	// UpdatedAt is the wall-clock the row was last written.
	UpdatedAt time.Time
}

// Store persists session_checkpoint_meta rows. The DeleteByUser /
// DeleteByTenant methods carry the §12.8 GDPR erasure contract so a
// substitute backend that omits either method cannot compile into the
// gateway binary (§12.1 line 5).
type Store interface {
	// Upsert inserts or overwrites the single row for (tenant,
	// session). A barrier on a session that already has a row replaces
	// the prior generation's metadata. Returns an error when the
	// tenant or session id is empty.
	Upsert(ctx context.Context, r Record) error

	// Get returns the row for (tenantID, sessionID) or ErrNotFound.
	Get(ctx context.Context, tenantID, sessionID string) (Record, error)

	// DeleteByUser removes every row in tenantID whose session id is in
	// sessionIDs. The §12.8 orchestrator owns the session-id lookup
	// because the table carries no user_id column.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant removes every row scoped to tenantID. Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// MemoryStore is an in-memory Store backing tests and developer-mode.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[recordKey]Record
	now  func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. now selects the
// timestamp source; a nil now uses time.Now in UTC.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[recordKey]Record{}, now: now}
}

var _ Store = (*MemoryStore)(nil)

type recordKey struct {
	tenantID  string
	sessionID string
}

// Upsert inserts or overwrites the row for (tenant, session).
func (m *MemoryStore) Upsert(_ context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return fmt.Errorf("sessioncheckpointmeta: tenant and session ids are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r.UpdatedAt = m.now()
	m.rows[recordKey{r.TenantID, r.SessionID}] = r
	return nil
}

// Get returns the row or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[recordKey{tenantID, sessionID}]
	if !ok {
		return Record{}, ErrNotFound
	}
	return row, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sessID := range sessionIDs {
		delete(m.rows, recordKey{tenantID, sessID})
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
