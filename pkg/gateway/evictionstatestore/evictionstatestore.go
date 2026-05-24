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
}

// Store persists §12.2.1 eviction-state records. The production
// implementation is the Postgres backend in pgstore; the in-memory
// MemoryStore in this package backs unit tests and the developer-mode
// deployment.
//
// The DeleteByUser and DeleteByTenant methods carry the §12.8 GDPR
// erasure contract. They are mandatory: the erasure orchestrator
// drives them in the documented dependency order.
type Store interface {
	// Put upserts the record. CreatedAt is set on first insert;
	// UpdatedAt is bumped on every call.
	Put(ctx context.Context, r Record) error

	// Get returns the row for (tenant, session) or ErrNotFound.
	Get(ctx context.Context, tenantID, sessionID string) (Record, error)

	// Delete removes one row. A missing row is not an error so the
	// terminal-state cleanup path is idempotent against partial
	// failures.
	Delete(ctx context.Context, tenantID, sessionID string) error

	// DeleteByUser removes every eviction-state row for the user's
	// sessions in the supplied tenant. The orchestrator looks up
	// the user's session ids upstream; this method removes the rows
	// that match.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant removes every row scoped to the supplied
	// tenant. Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error
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

// Put upserts the record.
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
	m.rows[key] = r
	return nil
}

// Get returns the row or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[compositeKey(tenantID, sessionID)]
	if !ok {
		return Record{}, ErrNotFound
	}
	return row, nil
}

// Delete removes the row. A missing row is not an error.
func (m *MemoryStore) Delete(_ context.Context, tenantID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, compositeKey(tenantID, sessionID))
	return nil
}

// DeleteByUser removes every row in tenantID whose session id is in
// the supplied slice. The orchestrator owns the session-id lookup
// because the EvictionStateStore does not carry a user_id column.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sessID := range sessionIDs {
		delete(m.rows, compositeKey(tenantID, sessID))
	}
	return nil
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (m *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.rows {
		if row.TenantID == tenantID {
			delete(m.rows, k)
		}
	}
	return nil
}

// compositeKey is the in-memory primary key: (tenant_id, session_id).
func compositeKey(tenantID, sessionID string) string {
	return tenantID + "\x00" + sessionID
}
