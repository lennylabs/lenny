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
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage/pgstore"
)

const (
	sessAcme1   = "11111111-1111-1111-1111-111111111111"
	sessAcme2   = "22222222-2222-2222-2222-222222222222"
	sessGlobex1 = "33333333-3333-3333-3333-333333333333"
)

// TestSessionUsagePgStore_spec_8_8_897 brings up an embedded Postgres,
// applies the migration prerequisites plus 0158_session_usage, and
// exercises the §8.8 per-session token accumulator: atomic accumulation
// under concurrency, tenant isolation, GetMany batching, and the
// ON DELETE CASCADE erasure that follows a session row. It downloads the
// PostgreSQL bundle, so it is skipped under -short.
//
// spec: §8.8 lines 897-917; §4.9 line 1468; §12.3 (RLS isolation).
func TestSessionUsagePgStore_spec_8_8_897(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15544,
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

	// The tenant anchor + RLS roles + lenny_tenant_guard() (0001, 0002) and
	// the session_usage table itself (0158). The full set is not applied
	// because an unrelated later migration needs the pgvector extension the
	// embedded bundle lacks.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0158_session_usage.up.sql",
	)

	insertTenant(t, ctx, pool, "acme")
	insertTenant(t, ctx, pool, "globex")
	insertSession(t, ctx, pool, sessAcme1, "acme")
	insertSession(t, ctx, pool, sessAcme2, "acme")
	insertSession(t, ctx, pool, sessGlobex1, "globex")

	s := pgstore.New(pool)
	var _ sessionusage.Store = s

	// Sequential accumulation returns the running total via a fresh Get.
	if err := s.Add(ctx, "acme", sessAcme1, 1000, 400); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(ctx, "acme", sessAcme1, 500, 100); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := s.Get(ctx, "acme", sessAcme1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Input != 1500 || got.Output != 500 {
		t.Fatalf("got %+v, want {Input:1500 Output:500}", got)
	}

	// A session with no recorded usage reads back zero, not an error.
	if got, err = s.Get(ctx, "acme", sessAcme2); err != nil || got != (sessionusage.Tokens{}) {
		t.Fatalf("empty Get = %+v, %v; want zero, nil", got, err)
	}

	// Concurrent adds from many goroutines (several replicas recording at
	// once) must not lose updates: the single-statement upsert serializes.
	const goroutines = 8
	const per = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				if aerr := s.Add(ctx, "acme", sessAcme2, 1, 2); aerr != nil {
					errs <- aerr
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for aerr := range errs {
		t.Fatalf("concurrent Add: %v", aerr)
	}
	got, _ = s.Get(ctx, "acme", sessAcme2)
	if want := int64(goroutines * per); got.Input != want || got.Output != 2*want {
		t.Fatalf("concurrent total = %+v, want {Input:%d Output:%d}", got, want, 2*want)
	}

	// Tenant isolation: globex's accumulator is independent.
	_ = s.Add(ctx, "globex", sessGlobex1, 7, 3)
	g, _ := s.Get(ctx, "globex", sessGlobex1)
	if g.Input != 7 || g.Output != 3 {
		t.Fatalf("globex got %+v, want {7,3}", g)
	}

	// GetMany batches a tenant's sessions, omitting those with no usage.
	many, err := s.GetMany(ctx, "acme", []string{sessAcme1, sessAcme2, sessGlobex1})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(many) != 2 {
		t.Fatalf("GetMany len = %d, want 2 (globex session not in acme)", len(many))
	}
	if many[sessAcme1].Input != 1500 {
		t.Fatalf("GetMany[acme1] = %+v", many[sessAcme1])
	}

	// ON DELETE CASCADE: deleting the session row removes its usage row.
	deleteSession(t, ctx, pool, sessAcme1, "acme")
	if got, _ = s.Get(ctx, "acme", sessAcme1); got != (sessionusage.Tokens{}) {
		t.Fatalf("after session delete, usage = %+v, want zero (cascade)", got)
	}
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
	nonce := make([]byte, 32)
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`, id, nonce); err != nil {
		t.Fatalf("insert tenant %q: %v", id, err)
	}
}

// insertSession writes a session row under its tenant's RLS context so
// the lenny_tenant_guard trigger on sessions admits the write.
func insertSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, tenantID string) {
	t.Helper()
	if err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES ($1, $2, 'completed', 'runtime-x', $1)`, id, tenantID)
		return err
	}); err != nil {
		t.Fatalf("insert session %q: %v", id, err)
	}
}

func deleteSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, tenantID string) {
	t.Helper()
	if err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1 AND tenant_id = $2`, id, tenantID)
		return err
	}); err != nil {
		t.Fatalf("delete session %q: %v", id, err)
	}
}
