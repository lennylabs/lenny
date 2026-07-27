// SPDX-License-Identifier: MIT

// Package coordlease is the §10.1 line 165 CheckpointBarrier
// barrier-target mirror: a Postgres-durable, cross-replica index of
// which gateway replica coordinates which session. The authoritative
// coordination lease lives in Redis (pkg/gateway/leasestore); the
// coordination Sweeper mirrors it into this store on every sweep so the
// preStop graceful-drain barrier can enumerate its barrier-target set
// from Postgres rather than the in-memory lease cache.
//
// §10.1 line 165 sources the barrier-target set from
//
//	SELECT session_id FROM coordination_lease
//	WHERE coordinator_replica = $this_replica_id AND released_at IS NULL
//
// because reading from Postgres observes coordinator handoffs that
// occurred in the seconds before preStop: the in-memory cache can
// include stale entries for sessions just handed off (false-positive
// barriers) and miss entries for sessions just handed in (false-negatives
// that leave a recently-dispatched tool call without a barrier). The
// barrier coordinator falls back to the in-memory lease cache only when
// this Postgres read fails or exceeds its 2-second deadline.
//
// The store is platform-scoped (one replica coordinates sessions across
// every tenant), so ListHeldByReplica is a cross-tenant query and there
// is no per-tenant RLS. The §12.1 mandatory DeleteByUser / DeleteByTenant
// primitives carry the GDPR erasure contract so a substitute backend
// that omits either cannot compile into the gateway binary.
//
// The in-memory MemoryStore backs unit tests and the developer-mode
// deployment; the Postgres-backed implementation lives in the pgstore
// subpackage and writes the migration-0164 coordination_lease table.
//
// spec: §10.1 lines 163-181; §12.1 line 5.
package coordlease

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lease is one coordination_lease row: the session a replica coordinates
// and the fenced generation it observed.
type Lease struct {
	TenantID  string
	SessionID string
	// CoordinatorReplica is the OTel service.instance.id of the holder.
	CoordinatorReplica string
	// CoordinationGeneration is the §4.2 fenced generation carried in
	// the CheckpointBarrier message (§10.1 line 165).
	CoordinationGeneration int64
}

// Store mirrors session-coordination leases for the §10.1 barrier-target
// query. The DeleteByUser / DeleteByTenant methods carry the §12.1 GDPR
// erasure contract.
type Store interface {
	// Upsert records that coordinatorReplica holds the lease for the
	// session at the given generation, with released_at cleared. A
	// cross-replica handoff overwrites coordinator_replica with the new
	// holder. Returns an error when any of the ids is empty.
	Upsert(ctx context.Context, l Lease) error

	// Release marks the session's lease released (released_at set) so the
	// barrier-target query no longer returns it. Idempotent: a missing or
	// already-released row is a no-op.
	Release(ctx context.Context, tenantID, sessionID string) error

	// ListHeldByReplica returns the active (released_at IS NULL) leases
	// whose coordinator_replica equals replica — the §10.1 line 165
	// barrier-target set. The query is cross-tenant.
	ListHeldByReplica(ctx context.Context, replica string) ([]Lease, error)

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
	rows map[leaseKey]memRow
	now  func() time.Time
}

type leaseKey struct {
	tenantID  string
	sessionID string
}

type memRow struct {
	lease    Lease
	released bool
}

// NewMemoryStore returns an empty MemoryStore. now selects the timestamp
// source; a nil now uses time.Now in UTC.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[leaseKey]memRow{}, now: now}
}

var _ Store = (*MemoryStore)(nil)

// Upsert records the lease for (tenant, session), clearing released_at.
func (m *MemoryStore) Upsert(_ context.Context, l Lease) error {
	if l.TenantID == "" || l.SessionID == "" || l.CoordinatorReplica == "" {
		return fmt.Errorf("coordlease: tenant, session, and replica ids are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[leaseKey{l.TenantID, l.SessionID}] = memRow{lease: l, released: false}
	return nil
}

// Release marks the session's lease released. Idempotent.
func (m *MemoryStore) Release(_ context.Context, tenantID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := leaseKey{tenantID, sessionID}
	if row, ok := m.rows[k]; ok {
		row.released = true
		m.rows[k] = row
	}
	return nil
}

// ListHeldByReplica returns the active leases held by replica.
func (m *MemoryStore) ListHeldByReplica(_ context.Context, replica string) ([]Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Lease
	for _, row := range m.rows {
		if row.released || row.lease.CoordinatorReplica != replica {
			continue
		}
		out = append(out, row.lease)
	}
	return out, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in the
// supplied slice.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	if tenantID == "" {
		return ErrEmptyScope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sessID := range sessionIDs {
		delete(m.rows, leaseKey{tenantID, sessID})
	}
	return nil
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (m *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return ErrEmptyScope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.rows {
		if k.tenantID == tenantID {
			delete(m.rows, k)
		}
	}
	return nil
}

// ErrEmptyScope is returned by an erasure primitive called with an empty
// tenant id. An empty scope must never be treated as "delete everything"
// (§12.8 line 753).
var ErrEmptyScope = errors.New("coordlease: erasure requires a non-empty tenant_id")
