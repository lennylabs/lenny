// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/quotabudget"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint/pgstore"
)

// TestAddTenantTotal_AtomicIncrement brings up an embedded Postgres, applies
// the full migration set, and exercises the §12.4 line 268 atomic
// per-tenant rollup increment the in_memory_reconciled budget mode relies
// on. It downloads the PostgreSQL bundle, so it is skipped under -short.
//
// spec: §12.4 line 268; §11.2 line 44.
func TestAddTenantTotal_AtomicIncrement_spec_12_4_268(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15533,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// Apply the prerequisite chain for token_usage_checkpoint: the tenant
	// anchor + RLS roles + lenny_tenant_guard() (0001, 0002) and the table
	// itself (0119). The full migration set is not applied because an
	// unrelated later migration needs the pgvector extension, which the
	// embedded Postgres bundle does not carry.
	applyMigrations(t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0119_token_usage_checkpoint.up.sql",
	)

	insertTenant(t, ctx, pool, "acme")
	insertTenant(t, ctx, pool, "globex")

	s := pgstore.New(pool)
	// Compile-time + runtime confirmation the pgstore satisfies the budget
	// adder seam the in_memory_reconciled mode wires.
	var _ quotabudget.CheckpointAdder = s

	const (
		period = "hourly"
		label  = "hourly-2026060814"
	)

	// A zero-delta call materialises the row and reads the current total
	// (the startup slice draw).
	total, err := s.AddTenantTotal(ctx, "acme", period, label, 0)
	if err != nil {
		t.Fatalf("AddTenantTotal (draw): %v", err)
	}
	if total != 0 {
		t.Fatalf("cold-start total = %d, want 0", total)
	}

	// Sequential increments accumulate and RETURN the post-add total.
	if total, err = s.AddTenantTotal(ctx, "acme", period, label, 300); err != nil {
		t.Fatalf("AddTenantTotal +300: %v", err)
	} else if total != 300 {
		t.Fatalf("total after +300 = %d, want 300", total)
	}
	if total, err = s.AddTenantTotal(ctx, "acme", period, label, 250); err != nil {
		t.Fatalf("AddTenantTotal +250: %v", err)
	} else if total != 550 {
		t.Fatalf("total after +250 = %d, want 550", total)
	}

	// Concurrent increments (simulating several replicas reconciling at
	// once) must not lose updates: the row serializes the adds.
	const replicas = 8
	const per = 100
	var wg sync.WaitGroup
	errs := make(chan error, replicas)
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, aerr := s.AddTenantTotal(ctx, "acme", period, label, per); aerr != nil {
				errs <- aerr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for aerr := range errs {
		t.Fatalf("concurrent AddTenantTotal: %v", aerr)
	}
	final, err := s.AddTenantTotal(ctx, "acme", period, label, 0)
	if err != nil {
		t.Fatalf("AddTenantTotal (final read): %v", err)
	}
	if want := int64(550 + replicas*per); final != want {
		t.Fatalf("total after %d concurrent +%d = %d, want %d (lost update)", replicas, per, final, want)
	}

	// The increment is tenant-scoped: globex's rollup is independent.
	gtotal, err := s.AddTenantTotal(ctx, "globex", period, label, 42)
	if err != nil {
		t.Fatalf("AddTenantTotal globex: %v", err)
	}
	if gtotal != 42 {
		t.Fatalf("globex total = %d, want 42 (cross-tenant leak)", gtotal)
	}

	// The row is the tenant rollup (scope='tenant', subject_id='') the
	// reconcile reads back via the quotacheckpoint Store.
	assertRollupRow(t, ctx, pool, "acme", final)
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, names ...string) {
	t.Helper()
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func insertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	// The tenant FK target. genesis_nonce is NOT NULL with no default; the
	// remaining columns carry defaults. The test connects as the superuser
	// so RLS on the tenants anchor is bypassed.
	nonce := make([]byte, 32)
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`, id, nonce); err != nil {
		t.Fatalf("insert tenant %q: %v", id, err)
	}
}

func assertRollupRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, wantTotal int64) {
	t.Helper()
	if err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		var scope, subject string
		var total int64
		row := tx.QueryRow(ctx,
			`SELECT scope, subject_id, token_total FROM token_usage_checkpoint
			 WHERE tenant_id = $1`, tenantID)
		if err := row.Scan(&scope, &subject, &total); err != nil {
			return err
		}
		if scope != "tenant" {
			t.Errorf("rollup scope = %q, want tenant", scope)
		}
		if subject != "" {
			t.Errorf("rollup subject_id = %q, want empty", subject)
		}
		if total != wantTotal {
			t.Errorf("rollup token_total = %d, want %d", total, wantTotal)
		}
		return nil
	}); err != nil {
		t.Fatalf("read rollup row: %v", err)
	}
}
