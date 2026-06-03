// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.11 ArtifactStore replication
// StateStore. It persists the per-region RegionState rows to the
// ops_artifact_replication_state table (migration 0126) so a fail-closed
// residency suspension survives a restart of the process that runs the
// replication Controller. Without it the controller keeps state in memory
// only, and a residency violation that suspended replication would silently
// re-enable on restart (F-25.11.3).
//
// The table is platform-scoped (§25.4 line 1492), so the store does not run
// inside a tenant-scoped transaction and the table carries no RLS policy.
//
// spec: §25.11 lines 4073-4098.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
)

// Store is the Postgres-backed §25.11 replication StateStore. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ replication.StateStore = (*Store)(nil)

// PutReplicationState upserts a region's replication state row.
func (s *Store) PutReplicationState(ctx context.Context, st replication.RegionState) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_artifact_replication_state
			(region, status, last_preflight_at, last_preflight_result, destination_endpoint,
			 destination_bucket, destination_jurisdiction_tag, replication_lag_seconds, suspended_since)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (region) DO UPDATE SET
			status=EXCLUDED.status, last_preflight_at=EXCLUDED.last_preflight_at,
			last_preflight_result=EXCLUDED.last_preflight_result,
			destination_endpoint=EXCLUDED.destination_endpoint,
			destination_bucket=EXCLUDED.destination_bucket,
			destination_jurisdiction_tag=EXCLUDED.destination_jurisdiction_tag,
			replication_lag_seconds=EXCLUDED.replication_lag_seconds,
			suspended_since=EXCLUDED.suspended_since`,
		st.Region, string(st.State), nullTime(st.LastPreflightAt), st.LastPreflightResult,
		st.DestinationEndpoint, st.DestinationBucket, st.DestinationJurisdictionTag,
		st.ReplicationLagSeconds, nullTime(st.SuspendedSince))
	return err
}

// GetReplicationState reads a region's replication state row. ok is false
// when no row exists yet.
func (s *Store) GetReplicationState(ctx context.Context, region string) (replication.RegionState, bool, error) {
	var (
		st             replication.RegionState
		status         string
		lastPreflight  *time.Time
		suspendedSince *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT region, status, last_preflight_at, last_preflight_result, destination_endpoint,
		       destination_bucket, destination_jurisdiction_tag, replication_lag_seconds, suspended_since
		  FROM ops_artifact_replication_state WHERE region=$1`, region).
		Scan(&st.Region, &status, &lastPreflight, &st.LastPreflightResult,
			&st.DestinationEndpoint, &st.DestinationBucket, &st.DestinationJurisdictionTag,
			&st.ReplicationLagSeconds, &suspendedSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return replication.RegionState{}, false, nil
	}
	if err != nil {
		return replication.RegionState{}, false, err
	}
	st.State = replication.State(status)
	if lastPreflight != nil {
		st.LastPreflightAt = *lastPreflight
	}
	if suspendedSince != nil {
		st.SuspendedSince = *suspendedSince
	}
	return st, true, nil
}

// nullTime returns nil for a zero time so the column stores NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
