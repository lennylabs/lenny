// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §10.1 coordination_lease mirror
// store. It persists barrier-target rows to the coordination_lease table
// from migration 0164. The table is platform-scoped (the §10.1 line 165
// barrier-target query is cross-tenant per replica), so the store reads
// and writes through the pool directly without the §12.3 per-tenant RLS
// guard the tenant-scoped stores use.
//
// spec: §10.1 lines 163-181.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
)

// Store is the Postgres-backed §10.1 coordination_lease mirror store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. now selects the timestamp source;
// a nil now uses time.Now in UTC.
func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, now: now}
}

var _ coordlease.Store = (*Store)(nil)

// Upsert inserts or overwrites the (tenant, session) row, clearing
// released_at and stamping this replica as the holder. A cross-replica
// handoff overwrites coordinator_replica with the new holder.
func (s *Store) Upsert(ctx context.Context, l coordlease.Lease) error {
	if l.TenantID == "" || l.SessionID == "" || l.CoordinatorReplica == "" {
		return errors.New("coordlease: tenant, session, and replica ids are required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO coordination_lease (
			tenant_id, session_id, coordinator_replica, coordinator_address,
			coordination_generation, acquired_at, released_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULL)
		ON CONFLICT (tenant_id, session_id) DO UPDATE SET
			coordinator_replica = EXCLUDED.coordinator_replica,
			coordinator_address = EXCLUDED.coordinator_address,
			coordination_generation = EXCLUDED.coordination_generation,
			acquired_at = EXCLUDED.acquired_at,
			released_at = NULL`,
		l.TenantID, l.SessionID, l.CoordinatorReplica, l.CoordinatorAddress,
		l.CoordinationGeneration, s.now())
	return err
}

// Release marks the session's lease released. Idempotent: the
// released_at IS NULL predicate makes the transition strictly monotonic
// so a repeated release is a no-op.
func (s *Store) Release(ctx context.Context, tenantID, sessionID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE coordination_lease SET released_at = $3
		WHERE tenant_id = $1 AND session_id = $2 AND released_at IS NULL`,
		tenantID, sessionID, s.now())
	return err
}

// ListHeldByReplica returns the §10.1 line 165 barrier-target set: the
// active leases whose coordinator_replica equals replica. The query is
// cross-tenant.
func (s *Store) ListHeldByReplica(ctx context.Context, replica string) ([]coordlease.Lease, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_id, session_id, coordinator_replica, coordination_generation
		FROM coordination_lease
		WHERE coordinator_replica = $1 AND released_at IS NULL`,
		replica)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coordlease.Lease
	for rows.Next() {
		var l coordlease.Lease
		if err := rows.Scan(&l.TenantID, &l.SessionID, &l.CoordinatorReplica, &l.CoordinationGeneration); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetBySession resolves the recorded coordinator for a session — the
// §4.6.1 eviction-drive routing read. It returns the active
// (released_at IS NULL) row's coordinator identity and dialable address,
// with found=false when no active row exists. The released_at IS NULL
// predicate replicates the ListHeldByReplica filter, so a released lease
// resolves no coordinator. A NULL coordinator_address collapses to the
// empty string, which resolves no forward target.
func (s *Store) GetBySession(ctx context.Context, tenantID, sessionID string) (coordlease.Lease, bool, error) {
	l := coordlease.Lease{TenantID: tenantID, SessionID: sessionID}
	err := s.pool.QueryRow(ctx,
		`SELECT coordinator_replica, COALESCE(coordinator_address, ''), coordination_generation
		FROM coordination_lease
		WHERE tenant_id = $1 AND session_id = $2 AND released_at IS NULL`,
		tenantID, sessionID).Scan(&l.CoordinatorReplica, &l.CoordinatorAddress, &l.CoordinationGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return coordlease.Lease{}, false, nil
	}
	if err != nil {
		return coordlease.Lease{}, false, err
	}
	return l, true, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// sessionIDs. The §12.8 orchestrator owns the session-id lookup.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if tenantID == "" {
		return coordlease.ErrEmptyScope
	}
	if len(sessionIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM coordination_lease
		WHERE tenant_id = $1 AND session_id = ANY($2)`,
		tenantID, sessionIDs)
	return err
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return coordlease.ErrEmptyScope
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM coordination_lease WHERE tenant_id = $1`, tenantID)
	return err
}
