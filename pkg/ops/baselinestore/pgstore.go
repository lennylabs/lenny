// SPDX-License-Identifier: MIT

// Package baselinestore is the Postgres-backed §25.2 operation-baseline
// store. It persists the ops_operation_baselines table (migration 0128)
// so the canonical Progress Envelope's historical_p50 ETA method survives
// a lenny-ops restart and is visible across replicas.
//
// The store wraps an in-process operations.MemoryBaselineStore that holds
// the recent-sample window and computes exact nearest-rank percentiles;
// every RecordCompletion mirrors the resulting aggregate (p50, p90,
// sample_size) to the table. A Lookup prefers the in-memory exact value
// (on the replica that recorded the completions) and falls back to the
// persisted aggregate when the in-memory window is empty — the
// cross-replica / post-restart path.
//
// The table is platform-scoped (the §25 control plane is not
// multi-tenanted at this boundary; §25.4 line 1492 lists the ops_* tables
// among the PlatformPostgres() set), so the store does not run inside a
// tenant-scoped transaction and the table carries no RLS policy.
//
// spec: §25.2 lines 393-394.
package baselinestore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// Store is the Postgres-backed §25.2 baseline store. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	mem  *operations.MemoryBaselineStore
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, mem: operations.NewMemoryBaselineStore()}
}

var _ operations.BaselineStore = (*Store)(nil)

// RecordCompletion folds dur into the in-memory window and upserts the
// resulting aggregate into ops_operation_baselines. The §25.2 table is
// "updated on each operation completion" (line 393).
func (s *Store) RecordCompletion(ctx context.Context, kind operations.Kind, dur time.Duration) error {
	if err := s.mem.RecordCompletion(ctx, kind, dur); err != nil {
		return err
	}
	b, ok, err := s.mem.Lookup(ctx, kind)
	if err != nil || !ok {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO ops_operation_baselines
			(kind, p50_duration_ms, p90_duration_ms, sample_size, last_updated)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (kind) DO UPDATE SET
			p50_duration_ms = EXCLUDED.p50_duration_ms,
			p90_duration_ms = EXCLUDED.p90_duration_ms,
			sample_size     = EXCLUDED.sample_size,
			last_updated    = EXCLUDED.last_updated`,
		string(kind), b.P50.Milliseconds(), b.P90.Milliseconds(), int64(b.SampleSize))
	return err
}

// Lookup returns kind's baseline, preferring the in-memory exact value
// and falling back to the persisted aggregate.
func (s *Store) Lookup(ctx context.Context, kind operations.Kind) (operations.Baseline, bool, error) {
	if b, ok, err := s.mem.Lookup(ctx, kind); err == nil && ok {
		return b, true, nil
	}
	var p50ms, p90ms, sample int64
	var last time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT p50_duration_ms, p90_duration_ms, sample_size, last_updated
		FROM ops_operation_baselines WHERE kind = $1`, string(kind)).
		Scan(&p50ms, &p90ms, &sample, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.Baseline{}, false, nil
	}
	if err != nil {
		return operations.Baseline{}, false, err
	}
	return operations.Baseline{
		P50:         time.Duration(p50ms) * time.Millisecond,
		P90:         time.Duration(p90ms) * time.Millisecond,
		SampleSize:  int(sample),
		LastUpdated: last,
	}, true, nil
}
