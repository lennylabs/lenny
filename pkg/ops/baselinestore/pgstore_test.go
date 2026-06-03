// SPDX-License-Identifier: MIT

package baselinestore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/ops/baselinestore"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// newTestStore brings up an embedded Postgres, applies the §25.2
// ops_operation_baselines schema (migration 0128), and returns a
// connected Store. It downloads the PostgreSQL bundle, so it is skipped
// under -short.
//
// spec: §25.2 lines 393-394.
func newTestStore(t *testing.T) (*baselinestore.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15523,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	up, err := migrations.FS.ReadFile("0128_ops_operation_baselines.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	return baselinestore.New(pool), pool, ctx
}

// TestBaselineRoundTrip exercises the §25.2 ops_operation_baselines
// upsert/read lifecycle: completions fold into the kind's percentiles and
// the row is persisted on every completion.
func TestBaselineRoundTrip_spec_25_2(t *testing.T) {
	s, pool, ctx := newTestStore(t)

	for i := 1; i <= 5; i++ {
		if err := s.RecordCompletion(ctx, operations.KindRestore, time.Duration(i)*time.Minute); err != nil {
			t.Fatalf("RecordCompletion: %v", err)
		}
	}
	b, ok, err := s.Lookup(ctx, operations.KindRestore)
	if err != nil || !ok {
		t.Fatalf("Lookup = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if b.SampleSize != 5 {
		t.Errorf("SampleSize = %d, want 5", b.SampleSize)
	}
	// nearest-rank p50 of 1..5 min (rank=ceil(0.5*5)=3) is 3 min.
	if b.P50 != 3*time.Minute {
		t.Errorf("P50 = %v, want 3m", b.P50)
	}

	// The row is persisted to the §25.2 table — assert the aggregate
	// landed in Postgres directly so a fresh replica (empty in-memory
	// window) can still serve a historical baseline.
	var p50ms, sample int64
	if err := pool.QueryRow(ctx,
		`SELECT p50_duration_ms, sample_size FROM ops_operation_baselines WHERE kind=$1`,
		string(operations.KindRestore)).Scan(&p50ms, &sample); err != nil {
		t.Fatalf("read persisted row: %v", err)
	}
	if p50ms != (3*time.Minute).Milliseconds() || sample != 5 {
		t.Errorf("persisted (p50ms=%d, sample=%d), want (%d, 5)", p50ms, sample, (3 * time.Minute).Milliseconds())
	}
}

// TestBaselineCrossReplicaFallback verifies the §25.2 cross-replica path:
// a store with an empty in-memory window serves the persisted aggregate a
// peer wrote.
func TestBaselineCrossReplicaFallback_spec_25_2(t *testing.T) {
	leader, pool, ctx := newTestStore(t)
	for i := 0; i < 4; i++ {
		if err := leader.RecordCompletion(ctx, operations.KindPlatformUpgrade, 8*time.Minute); err != nil {
			t.Fatalf("RecordCompletion: %v", err)
		}
	}
	// A second store over the same pool has no in-memory samples and must
	// fall back to the persisted row.
	follower := baselinestore.New(pool)
	b, ok, err := follower.Lookup(ctx, operations.KindPlatformUpgrade)
	if err != nil || !ok {
		t.Fatalf("follower Lookup = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if b.SampleSize != 4 || b.P50 != 8*time.Minute {
		t.Errorf("follower baseline = %+v, want sample 4 / p50 8m", b)
	}
}

// TestBaselineUnknownKind returns no baseline for a kind never recorded.
func TestBaselineUnknownKind_spec_25_2(t *testing.T) {
	s, _, ctx := newTestStore(t)
	if _, ok, err := s.Lookup(ctx, operations.KindBackup); err != nil || ok {
		t.Fatalf("Lookup unknown = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}
