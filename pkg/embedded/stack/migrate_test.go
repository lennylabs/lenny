// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"database/sql"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
)

// TestApplyMigrationsAppliesFullSetWithoutPgvector brings up the stock
// embedded PostgreSQL 16 bundle (which does not ship pgvector), applies
// the full §18.5 schema, and confirms that the pgvector shim path
// applied every migration: the §9.4 agent_memory.embedding column
// (migration 0044) and the later tenants.credential_policy column
// (migration 0077). A prefix-only migration that stopped before 0044
// would strand both columns and the embedded gateway's §12.8 / storage
// preflights would FATAL at startup. It downloads the PostgreSQL bundle,
// so it is skipped under -short.
//
// spec: 17.4 (embedded migrate path), 9.4 (agent_memory embedding),
// 4.9 (tenant credential_policy)
//
// diagnosis: applyMigrations did not apply the full §18.5 migration set
// on the stock embedded Postgres bundle that lacks pgvector. If
// agent_memory.embedding (migration 0044) or tenants.credential_policy
// (migration 0077) is absent, the migration run stranded a prefix and
// the embedded gateway's §12.8 / storage / compliance preflights would
// FATAL at startup. If the vector SQL step errors, the pure-SQL vector
// shim did not supply the type, the casts, or the `<=>` operator the
// production §9.4 memory store needs.
func TestApplyMigrationsAppliesFullSetWithoutPgvector(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pool := bringUpAndMigrate(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// The stock bundle never ships pgvector, so this run exercises the
	// shim path by construction.
	if pgvectorAvailable(poolToSQL(t, pool)) {
		t.Fatal("the stock embedded bundle unexpectedly reports pgvector available; " +
			"this test must exercise the shim path")
	}

	mustColumnExist(ctx, t, pool, "agent_memory", "embedding")
	mustColumnExist(ctx, t, pool, "tenants", "credential_policy")

	// The shim must let the production §9.4 memory-store SQL run: a
	// NULL-embedding insert, a vector-literal insert, and the `<=>`
	// ranked query the pgstore Query path issues.
	mustVectorSQLRun(ctx, t, pool)
}

// TestApplyMigrationsIdempotent confirms a second applyMigrations on the
// same data directory reports no change and leaves the schema intact.
// The shim installer is idempotent, so re-running it does not error.
//
// spec: 17.4 (lenny up idempotency), 9.4 (agent_memory embedding)
//
// diagnosis: a second lenny up against an existing embedded data
// directory did not re-run the schema migrations cleanly. If the second
// applyMigrations errors, either the migrator is not no-op on an
// already-migrated database or the pgvector shim installer is not
// idempotent (it re-created a type, cast, or operator that already
// exists).
func TestApplyMigrationsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := startEmbeddedPostgres(t)
	dsn := pg.DSN()
	if err := applyMigrations(dsn, io.Discard); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if err := applyMigrations(dsn, io.Discard); err != nil {
		t.Fatalf("second applyMigrations (idempotency): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	mustColumnExist(ctx, t, pool, "sessions", "id")
	mustColumnExist(ctx, t, pool, "agent_memory", "embedding")
}

// TestInstallVectorShimIdempotent confirms installVectorShim runs twice
// without error against the same database, so a second lenny up does not
// fail on an already-installed shim. The guarded CREATE TYPE / CREATE
// CAST / CREATE OPERATOR statements must skip the objects that already
// exist.
//
// spec: 17.4 (lenny up idempotency), 9.4 (vector shim)
//
// diagnosis: the pure-SQL vector shim is not idempotent. A second
// install raised an error, which means a CREATE TYPE / CAST / OPERATOR
// guard did not detect the existing object and re-created it.
func TestInstallVectorShimIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := startEmbeddedPostgres(t)
	db, err := openSQL(pg.DSN())
	if err != nil {
		t.Fatalf("openSQL: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := installVectorShim(db); err != nil {
		t.Fatalf("installVectorShim (first): %v", err)
	}
	if err := installVectorShim(db); err != nil {
		t.Fatalf("installVectorShim (second, idempotency): %v", err)
	}
	// The shim type must round-trip a literal through the cast.
	var got string
	if err := db.QueryRow(`SELECT ('[1,2,3]'::vector)::text`).Scan(&got); err != nil {
		t.Fatalf("shim cast round-trip: %v", err)
	}
	if got != "[1,2,3]" {
		t.Errorf("shim cast round-trip = %q, want [1,2,3]", got)
	}
}

// TestInstallVectorShimErrorsOnClosedDB confirms installVectorShim
// returns a wrapped error rather than panicking when the database handle
// is unusable, exercising the fail-closed error path.
//
// spec: 17.4 (embedded migrate path), 9.4 (vector shim)
//
// diagnosis: installVectorShim did not surface a database error from its
// statement loop as a wrapped error. A closed handle must yield a
// non-nil error so applyMigrations aborts the bring-up rather than
// proceeding with a half-installed shim.
func TestInstallVectorShimErrorsOnClosedDB(t *testing.T) {
	// A handle to a bogus DSN never connects; Exec fails on first use, so
	// this needs no Postgres bundle and runs under -short.
	db, err := openSQL("postgres://127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("openSQL: %v", err)
	}
	_ = db.Close()
	if err := installVectorShim(db); err == nil {
		t.Fatal("installVectorShim against a closed handle returned nil; want an error")
	}
}

// TestEmbeddedGatewayMemoryStorePreflightPasses reproduces the §12.8
// gateway boot check that the prefix-only migration broke. The embedded
// gateway constructs the production memorypg.Store over the embedded
// Postgres and runs ValidateMemoryStoreErasure before binding its port;
// that preflight seeds a probe row into agent_memory (which needs the
// migration-0044 embedding column) under the reserved __preflight__
// tenant (which migration 0096 seeds). A migration run that stranded
// either migration made this preflight FATAL and the gateway never came
// up, so the §17.4 smoke test's `lenny up` timed out. With the full
// migration set applied through the vector shim, the exact production
// preflight passes against the embedded Postgres that lacks pgvector.
//
// spec: 12.8 (MemoryStore erasure preflight), 9.4 (agent_memory
// embedding), 17.4 (embedded gateway boots against embedded Postgres)
//
// diagnosis: the embedded gateway's §12.8 MemoryStore erasure preflight
// failed against the embedded Postgres, which is the exact FATAL that
// kept `lenny up` from reaching ready. If the seed Write step errors on
// a missing `embedding` column, migration 0044 was stranded; if it
// errors on the missing `__preflight__` tenant row, migration 0096 was
// stranded. Either means applyMigrations did not apply the full set.
func TestEmbeddedGatewayMemoryStorePreflightPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pool := bringUpAndMigrate(t)
	store := memorypg.New(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := memorystore.ValidateMemoryStoreErasure(ctx, store); err != nil {
		t.Fatalf("§12.8 MemoryStore erasure preflight failed against the embedded Postgres: %v", err)
	}
}

// startEmbeddedPostgres starts a fresh stock embedded Postgres and
// registers its teardown.
func startEmbeddedPostgres(t *testing.T) *embpostgres.Instance {
	t.Helper()
	// Port 0 asks the kernel for a free ephemeral port so parallel test
	// binaries do not collide on a fixed port (§17.4).
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })
	return pg
}

// bringUpAndMigrate starts a stock embedded Postgres, runs the full
// migration set, and returns a connection pool to the migrated database.
func bringUpAndMigrate(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pg := startEmbeddedPostgres(t)
	dsn := pg.DSN()
	if err := applyMigrations(dsn, io.Discard); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// poolToSQL opens a database/sql handle from a pgxpool's connection
// string so pgvectorAvailable (which takes a *sql.DB) can probe the same
// instance.
func poolToSQL(t *testing.T, pool *pgxpool.Pool) *sql.DB {
	t.Helper()
	db, err := openSQL(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("openSQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// mustColumnExist fails the test when the named column is absent on the
// public-schema table, which is how a stranded migration surfaces.
func mustColumnExist(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`,
		table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("column probe %s.%s: %v", table, column, err)
	}
	if !exists {
		t.Errorf("%s.%s absent after applyMigrations (migration stranded)", table, column)
	}
}

// mustVectorSQLRun exercises the production pgstore SQL surface the shim
// must support: a vector-literal cast on insert, a NULL embedding, the
// embedding::text projection, and the `<=>` ranked ORDER BY. A shim that
// did not supply the type, the casts, or the operator would error here.
func mustVectorSQLRun(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// agent_memory carries the §12.3 tenant guard and RLS, so insert
	// through a tenant row and set app.current_tenant first.
	stmts := []string{
		// genesis_nonce is a NOT-NULL column added by a later migration;
		// the established test seed pattern is a benign zero nonce.
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00') ON CONFLICT DO NOTHING`,
		`SET app.current_tenant = 'acme'`,
		`INSERT INTO agent_memory (tenant_id, user_id, id, content, embedding)
			VALUES ('acme', 'alice', 'mem_1', 'hello', '[1,2,3]'::vector)`,
		`INSERT INTO agent_memory (tenant_id, user_id, id, content, embedding)
			VALUES ('acme', 'alice', 'mem_2', 'world', NULL)`,
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("vector SQL %q: %v", s, err)
		}
	}
	rows, err := conn.Query(ctx,
		`SELECT id, embedding::text FROM agent_memory
		 WHERE tenant_id = 'acme' AND user_id = 'alice'
		 ORDER BY embedding <=> '[1,2,3]'::vector NULLS LAST, created_at DESC`)
	if err != nil {
		t.Fatalf("ranked query against shim: %v", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ranked query iteration: %v", err)
	}
	if n != 2 {
		t.Errorf("ranked query returned %d rows, want 2", n)
	}
}
