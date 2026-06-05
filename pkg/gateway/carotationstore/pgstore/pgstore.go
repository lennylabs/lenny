// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed carotationstore.Store over the
// ca_rotation table. It is the durable home for the §10.3 CA-rotation
// stage so an operator-driven rotation survives a gateway restart;
// carotationstore.Memory is the in-memory alternative.
//
// The rotation is a platform-global procedure (one cluster-internal CA),
// so the table holds a single row keyed by a constant id. It is
// platform-operational state rather than tenant-isolated: no
// lenny_tenant_guard trigger and no RLS policy, and this store operates
// on the pool directly. The optimistic version column serializes
// concurrent stage transitions across gateway replicas.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/carotationstore"
)

// singletonID is the constant primary key for the lone ca_rotation row.
// The table's CHECK constraint pins it so a second row cannot be
// inserted.
const singletonID = "singleton"

// Store is the Postgres-backed CA-rotation registry. Construct with New.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ carotationstore.Store = (*Store)(nil)

// New returns a Store over pool. The pool must address a database that
// has the migrations/ schema applied (the ca_rotation table).
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

// NewWithClock returns a Store with an injected clock, for tests that
// need a deterministic UpdatedAt.
func NewWithClock(pool *pgxpool.Pool, clock func() time.Time) *Store {
	s := New(pool)
	if clock != nil {
		s.clock = clock
	}
	return s
}

// Get implements carotationstore.Store.
func (s *Store) Get(ctx context.Context) (carotationstore.Record, bool, error) {
	var (
		rec     carotationstore.Record
		started *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT stage, current_ca_id, new_ca_id, overlap_started_at,
		       overlap_window_secs, version, updated_at
		FROM ca_rotation WHERE id = $1`, singletonID).
		Scan(&rec.Stage, &rec.CurrentCAID, &rec.NewCAID, &started,
			&rec.OverlapWindowSecs, &rec.Version, &rec.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return carotationstore.Record{}, false, nil
	}
	if err != nil {
		return carotationstore.Record{}, false, fmt.Errorf("carotationstore: get: %w", err)
	}
	if started != nil {
		rec.OverlapStartedAt = started.UTC()
	}
	return rec, true, nil
}

// Put implements carotationstore.Store with an optimistic version guard.
// The very first write (expectVersion 0) inserts the row; later writes
// UPDATE ... WHERE version = expectVersion and report ErrConflict when
// no row matched.
func (s *Store) Put(ctx context.Context, rec carotationstore.Record, expectVersion int64) (carotationstore.Record, error) {
	var started *time.Time
	if !rec.OverlapStartedAt.IsZero() {
		t := rec.OverlapStartedAt.UTC()
		started = &t
	}
	now := s.clock()
	next := expectVersion + 1
	if expectVersion == 0 {
		// Initialization: insert the singleton. A unique-violation means
		// a row already exists (a concurrent initializer won the race).
		_, err := s.pool.Exec(ctx, `
			INSERT INTO ca_rotation
			  (id, stage, current_ca_id, new_ca_id, overlap_started_at,
			   overlap_window_secs, version, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			singletonID, rec.Stage, rec.CurrentCAID, rec.NewCAID, started,
			rec.OverlapWindowSecs, next, now)
		if err != nil {
			if isUniqueViolation(err) {
				return carotationstore.Record{}, carotationstore.ErrConflict
			}
			return carotationstore.Record{}, fmt.Errorf("carotationstore: insert: %w", err)
		}
	} else {
		tag, err := s.pool.Exec(ctx, `
			UPDATE ca_rotation
			SET stage = $2, current_ca_id = $3, new_ca_id = $4,
			    overlap_started_at = $5, overlap_window_secs = $6,
			    version = $7, updated_at = $8
			WHERE id = $1 AND version = $9`,
			singletonID, rec.Stage, rec.CurrentCAID, rec.NewCAID, started,
			rec.OverlapWindowSecs, next, now, expectVersion)
		if err != nil {
			return carotationstore.Record{}, fmt.Errorf("carotationstore: update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return carotationstore.Record{}, carotationstore.ErrConflict
		}
	}
	rec.Version = next
	rec.UpdatedAt = now
	return rec, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	type sqlStater interface{ SQLState() string }
	var s sqlStater
	if errors.As(err, &s) {
		return s.SQLState() == "23505"
	}
	return false
}
