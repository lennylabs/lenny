// SPDX-License-Identifier: MIT

// Package checkpointretention persists the §4.4 line 234 / §12.5
// latest-2 checkpoints retention catalog. On every successful
// checkpoint, the gateway records a row keyed by (tenant_id,
// session_id, ref). The Rotate method retains the two most recent
// rows for a session and soft-deletes every older row in a single
// transaction; the §12.5 backstop sweep hard-prunes rows whose
// deleted_at is older than the tombstone retention window.
//
// The package owns:
//
//   - Record: one row in the session_checkpoints catalog.
//   - Store: the catalog API (Insert, Rotate, List, sweep helpers).
//   - MemoryStore: in-memory implementation backing unit tests and
//     dev-mode deployments.
//   - Postgres-backed implementation lives in
//     pkg/gateway/checkpointretention/pgstore.
//
// spec: §4.4 line 234, §12.5 latest-2 checkpoints retention.
package checkpointretention

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// RetainedCount is the §12.5 mandated retention count: the two most
// recent checkpoints per session are retained; older rows are
// soft-deleted by Rotate.
//
// spec: §12.5 — "latest 2 checkpoints" retention.
const RetainedCount = 2

// Record is one row in the session_checkpoints catalog.
type Record struct {
	// TenantID and SessionID identify the session this checkpoint
	// belongs to. Both are required on Insert.
	TenantID  string
	SessionID string
	// SlotID is the §5.2 concurrent-mode slot the checkpoint belongs
	// to. The single-workspace (session / task / stateless) path
	// writes the empty string, keeping its rotation per-session; the
	// concurrent-workspace path writes the bound slot id so the
	// "latest 2" cap applies independently per slot.
	//
	// spec: §12.5 lines 313, 326 — rotation operates on
	// (session_id, slot_id) pairs.
	SlotID string
	// Ref is the MinIO object reference for the checkpoint snapshot.
	// The (tenant, session, ref) tuple is the catalog's composite
	// key; a second Insert for the same triple is rejected with
	// ErrDuplicate.
	Ref string
	// CreatedAt is set on Insert and is immutable thereafter. Rotate
	// orders by this column descending to identify the latest two.
	CreatedAt time.Time
	// Retained is true when the row represents one of the two most
	// recent checkpoints for this session. Rotate sets this to false
	// on every other row for the session.
	Retained bool
	// DeletedAt is the §4.4 line 236 soft-delete tombstone. Zero
	// while the row is active; set by Rotate when Retained drops to
	// false. The §12.5 backstop sweep hard-prunes rows whose
	// DeletedAt is older than the tombstone retention window.
	DeletedAt time.Time
	// SchemaVersion is the §15.5 item 7 checkpoint-metadata schema
	// revision. The gateway stamps it at Insert time; a zero value is
	// normalized to SchemaVersion (1). It is immutable once written.
	//
	// spec: §15.5 item 7 — "checkpoint metadata" carries a schemaVersion
	// integer "starting at 1", set at write time by the gateway.
	SchemaVersion int
}

// SchemaVersion is the §15.5 item 7 checkpoint-metadata schema revision
// stamped on every row v1 writes.
const SchemaVersion = 1

// Store persists checkpoint rotation rows.
type Store interface {
	// Insert records a new checkpoint row. A duplicate (tenant,
	// session, ref) returns ErrDuplicate. The row's CreatedAt is
	// stamped at Insert time; the caller's CreatedAt is ignored.
	Insert(ctx context.Context, r Record) error

	// Rotate enforces the §12.5 latest-2 retention policy for the
	// supplied (tenantID, sessionID, slotID): the two most-recent
	// active rows for that slot keep Retained=true; every other
	// active row in the slot is marked Retained=false with
	// DeletedAt=now(). slotID is the empty string for the
	// single-workspace path, so a session's whole-session rotation
	// is the slotID="" case. Returns the slice of rows whose
	// Retained transitioned from true to false in this call, in
	// CreatedAt-ascending order (oldest first), so the caller can
	// correlate with MinIO deletes.
	//
	// spec: §12.5 lines 313, 326 — per-slot "latest 2".
	Rotate(ctx context.Context, tenantID, sessionID, slotID string) ([]Record, error)

	// List returns every row for (tenantID, sessionID, slotID),
	// retained and soft-deleted alike, in CreatedAt-descending
	// order. Tests and ops queries use this to verify the rotation
	// state.
	List(ctx context.Context, tenantID, sessionID, slotID string) ([]Record, error)

	// ListSoftDeletedBefore returns every row whose DeletedAt is
	// older than cutoff. The §12.5 hard-prune sweep walks the
	// result to issue per-row HardDelete calls.
	ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]Record, error)

	// HardDelete removes the row entirely. Called by the §12.5
	// backstop sweep on rows whose DeletedAt is older than the
	// tombstone retention window.
	HardDelete(ctx context.Context, tenantID, sessionID, slotID, ref string) error

	// DeleteByUser removes every row in tenantID whose session_id
	// is in sessionIDs. Called by the §12.8 erasure orchestrator.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant removes every row scoped to tenantID.
	// Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// ErrDuplicate is returned by Insert when (tenant, session, ref) is
// already in the catalog. Idempotent re-runs of the rotation worker
// rely on the caller to detect this and skip the duplicate.
var ErrDuplicate = errors.New("checkpointretention: row already exists")

// MemoryStore is the in-memory Store implementation backing unit
// tests and dev-mode deployments. It implements every Store method
// with goroutine-safe semantics.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[recordKey]Record
	now  func() time.Time
}

// recordKey is the in-memory composite key. ref is unique per
// checkpoint, so it alone disambiguates a row; slotID is carried on
// the Record value and used by Rotate / List to scope a single slot.
type recordKey struct {
	tenantID  string
	sessionID string
	ref       string
}

// NewMemoryStore returns an empty MemoryStore. now selects the
// timestamp source; a nil now uses time.Now.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[recordKey]Record{}, now: now}
}

// Insert records a new checkpoint row.
func (m *MemoryStore) Insert(_ context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return fmt.Errorf("checkpointretention: tenant and session ids are required")
	}
	if r.Ref == "" {
		return fmt.Errorf("checkpointretention: ref is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := recordKey{r.TenantID, r.SessionID, r.Ref}
	if _, ok := m.rows[k]; ok {
		return ErrDuplicate
	}
	r.CreatedAt = m.now()
	r.Retained = true
	r.DeletedAt = time.Time{}
	if r.SchemaVersion == 0 {
		r.SchemaVersion = SchemaVersion
	}
	m.rows[k] = r
	return nil
}

// Rotate enforces the §12.5 latest-2 retention policy for a single
// (tenant, session, slot) pair.
func (m *MemoryStore) Rotate(_ context.Context, tenantID, sessionID, slotID string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Collect every active row for (tenant, session, slot).
	var active []Record
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID || row.SlotID != slotID {
			continue
		}
		if !row.DeletedAt.IsZero() {
			continue
		}
		active = append(active, row)
	}
	// Sort descending so the latest are first.
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].CreatedAt.After(active[j].CreatedAt)
	})
	var transitioned []Record
	now := m.now()
	for i, row := range active {
		k := recordKey{row.TenantID, row.SessionID, row.Ref}
		if i < RetainedCount {
			// Retained = true is the default; nothing to do.
			continue
		}
		// This row is older than the latest two — drop it.
		row.Retained = false
		row.DeletedAt = now
		m.rows[k] = row
		transitioned = append(transitioned, row)
	}
	// Sort transitioned rows by CreatedAt ascending (oldest first).
	sort.SliceStable(transitioned, func(i, j int) bool {
		return transitioned[i].CreatedAt.Before(transitioned[j].CreatedAt)
	})
	return transitioned, nil
}

// List returns every row for the (session, slot) in
// CreatedAt-descending order.
func (m *MemoryStore) List(_ context.Context, tenantID, sessionID, slotID string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID || row.SlotID != slotID {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// ListSoftDeletedBefore returns rows whose DeletedAt is older than
// cutoff.
func (m *MemoryStore) ListSoftDeletedBefore(_ context.Context, cutoff time.Time) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
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

// HardDelete removes the row. The ref alone identifies the row;
// slotID is accepted for interface symmetry with the per-slot
// rotation surface.
func (m *MemoryStore) HardDelete(_ context.Context, tenantID, sessionID, _ /* slotID */, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, recordKey{tenantID, sessionID, ref})
	return nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.rows {
		if k.tenantID != tenantID {
			continue
		}
		for _, sid := range sessionIDs {
			if k.sessionID == sid {
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
