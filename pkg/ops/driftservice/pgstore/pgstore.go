// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.10 driftservice.SnapshotStore.
// It persists the desired-state snapshots to the bootstrap_seed_snapshot
// table (migration 0117) so the live and target rows survive a lenny-ops
// restart and the §25.10 "snapshot updated at OpsRoll" / "target → live
// promotion at Verification completion" behaviors operate against durable
// state rather than an in-process map.
//
// The bootstrap_seed_snapshot table is platform-scoped (the §25 control
// plane is not multi-tenanted at this boundary), so the store does not
// run inside a tenant-scoped transaction and the table carries no RLS
// policy and no tenant column.
//
// spec: §25.10 lines 3811-3820.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
)

// Store is the Postgres-backed §25.10 desired-state snapshot store.
// Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Get returns the bootstrap_seed_snapshot row of the given id ("live" or
// "target"). ok is false when no such row exists (the §25.10 cold-start
// condition). A transport error is returned so the caller surfaces the
// §25.10 line 3866 503 Postgres-down case.
func (s *Store) Get(ctx context.Context, id string) (driftservice.Snapshot, bool, error) {
	var (
		snap    driftservice.Snapshot
		desired []byte
		upgrade *string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, desired_state, source, upgrade_id, written_at, written_by
		 FROM bootstrap_seed_snapshot WHERE id=$1`, id).
		Scan(&snap.ID, &desired, &snap.Source, &upgrade, &snap.WrittenAt, &snap.WrittenBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return driftservice.Snapshot{}, false, nil
	}
	if err != nil {
		return driftservice.Snapshot{}, false, err
	}
	if upgrade != nil {
		snap.UpgradeID = *upgrade
	}
	if len(desired) > 0 {
		if err := json.Unmarshal(desired, &snap.DesiredState); err != nil {
			return driftservice.Snapshot{}, false, err
		}
	}
	snap.WrittenAt = snap.WrittenAt.UTC()
	return snap, true, nil
}

// Put writes (replaces) the snapshot row. §25.10 keeps exactly one row
// per id, so the write is an upsert keyed on the primary key — a refresh
// replaces the live row's desired state, source, and provenance in
// place.
func (s *Store) Put(ctx context.Context, snap driftservice.Snapshot) error {
	desired, err := json.Marshal(snap.DesiredState)
	if err != nil {
		return err
	}
	var upgrade *string
	if snap.UpgradeID != "" {
		upgrade = &snap.UpgradeID
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO bootstrap_seed_snapshot (id, desired_state, source, upgrade_id, written_at, written_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE SET
		   desired_state = EXCLUDED.desired_state,
		   source        = EXCLUDED.source,
		   upgrade_id    = EXCLUDED.upgrade_id,
		   written_at    = EXCLUDED.written_at,
		   written_by    = EXCLUDED.written_by`,
		snap.ID, desired, snap.Source, upgrade, snap.WrittenAt.UTC(), snap.WrittenBy)
	return err
}

// Delete removes the bootstrap_seed_snapshot row of the given id. §25.8
// line 3551 deletes the target row on rollback; a DELETE that matches
// zero rows (rollback during Preflight, when no target was written) is
// not an error.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM bootstrap_seed_snapshot WHERE id=$1`, id)
	return err
}

// PromoteTargetToLive atomically promotes the target row into the live
// row at §25.10 Verification-phase completion (spec line 3789). The whole
// swap runs in one transaction so a concurrent GET reads either the old
// live row or the new one, never a torn state: the live row's desired
// state, source, upgrade id, and provenance are replaced from the target
// row, then the target row is deleted. A missing target row leaves the
// live row untouched (the UPDATE matches zero rows) and is not an error,
// so a defensive double-promote is harmless.
//
// spec: §25.10 line 3789 (target → live promotion at Verification
// completion).
func (s *Store) PromoteTargetToLive(ctx context.Context, _ string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// UPSERT the live row from the target row in a single statement so the
	// live row is created on a cold start (no prior live row) and replaced
	// otherwise. The ON CONFLICT keeps the swap a one-statement upsert.
	if _, err := tx.Exec(ctx,
		`INSERT INTO bootstrap_seed_snapshot (id, desired_state, source, upgrade_id, written_at, written_by)
		 SELECT 'live', desired_state, source, upgrade_id, written_at, written_by
		 FROM bootstrap_seed_snapshot WHERE id='target'
		 ON CONFLICT (id) DO UPDATE SET
		   desired_state = EXCLUDED.desired_state,
		   source        = EXCLUDED.source,
		   upgrade_id    = EXCLUDED.upgrade_id,
		   written_at    = EXCLUDED.written_at,
		   written_by    = EXCLUDED.written_by`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bootstrap_seed_snapshot WHERE id='target'`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Compile-time guard.
var _ driftservice.SnapshotStore = (*Store)(nil)
