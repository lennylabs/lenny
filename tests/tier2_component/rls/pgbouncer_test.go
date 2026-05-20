//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 RLS test: §12.2.2 connection-pooler reuse must not leak the
// previous transaction's app.current_tenant. The compose profile
// supplies a PgBouncer in session-pooling mode (compose/default.yml,
// pgbouncer service); when one client session releases a server
// connection back to PgBouncer, the next client to acquire that
// server conn must not inherit the prior SET state.
package rls_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/lennylabs/lenny/tests/testinfra/compose"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// applyMigrationsToCompose runs the production migrations under
// migrations/ against the compose Postgres DSN. The migrate library
// is idempotent: if the schema is already at the latest version it
// returns ErrNoChange.
func applyMigrationsToCompose(t *testing.T, dsn string) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join(schematest.RepoRoot(t), "migrations"))
	if err != nil {
		t.Fatalf("abs migrations: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+dir, "postgres", driver)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

// spec: 12.2.2
// diagnosis: a client connecting through the §12.2.2 connection
// pooler reused a server connection that still carried the previous
// session's app.current_tenant GUC. A leak here would let a second
// client read or write against another tenant's data simply by
// inheriting the GUC. The invariant is: a fresh connect through the
// pooler must not observe a non-empty app.current_tenant nor be
// permitted to write to a tenant-scoped table without setting it.
func TestRLSPoolerReuseDoesNotLeakContext(t *testing.T) {
	compose.SkipUnlessAvailable(t)
	stack := compose.Up(t, compose.ProfileDefault)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Apply production migrations via the direct Postgres DSN. The
	// migrate framework needs a stable conn for the full sequence;
	// PgBouncer's session-pooling mode preserves session state per
	// client, but applying many migrations through the pooler
	// introduces unnecessary variance.
	applyMigrationsToCompose(t, stack.PostgresDSN())

	// Seed a tenant directly so the RLS guard accepts writes under
	// that context.
	bootstrap, err := pgx.Connect(ctx, stack.PostgresDSN())
	if err != nil {
		t.Fatalf("connect direct postgres: %v", err)
	}
	t.Cleanup(func() { _ = bootstrap.Close(context.Background()) })
	tenant := "pooler-leak-" + time.Now().UTC().Format("150405.000000")
	if _, err := bootstrap.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')
		 ON CONFLICT (id) DO NOTHING`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Connection #1 through PgBouncer: SET (session-level) the GUC
	// and confirm reads succeed.
	conn1, err := pgx.Connect(ctx, stack.PgBouncerDSN())
	if err != nil {
		t.Fatalf("connect pgbouncer #1: %v", err)
	}
	if _, err := conn1.Exec(ctx,
		`SET app.current_tenant = `+pgquote(tenant)); err != nil {
		t.Fatalf("SET on conn1: %v", err)
	}
	if _, err := conn1.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		 VALUES (gen_random_uuid(), $1, 'created', 'echo', gen_random_uuid())`,
		tenant); err != nil {
		t.Fatalf("insert under conn1: %v", err)
	}
	// Close conn1 — PgBouncer returns the server conn to its pool
	// and (in session-pooling mode) sends RESET ALL / DISCARD ALL
	// when the client session ends.
	if err := conn1.Close(ctx); err != nil {
		t.Fatalf("close conn1: %v", err)
	}

	// Connection #2 through PgBouncer: do not SET the GUC. Reading a
	// tenant-scoped table without app.current_tenant must be rejected
	// by lenny_tenant_guard — confirming the GUC did not leak.
	conn2, err := pgx.Connect(ctx, stack.PgBouncerDSN())
	if err != nil {
		t.Fatalf("connect pgbouncer #2: %v", err)
	}
	t.Cleanup(func() { _ = conn2.Close(context.Background()) })

	_, err = conn2.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		 VALUES (gen_random_uuid(), $1, 'created', 'echo', gen_random_uuid())`,
		tenant)
	if err == nil {
		t.Fatal("PgBouncer reuse leaked app.current_tenant: " +
			"insert under conn2 with no SET succeeded (RLS bypass)")
	}
	if !strings.Contains(err.Error(), "app.current_tenant is not set") {
		t.Errorf("conn2 rejection should cite missing tenant context: %v", err)
	}

	// Belt-and-braces: query current_setting and confirm it is empty.
	var current string
	if err := conn2.QueryRow(ctx,
		`SELECT current_setting('app.current_tenant', true)`).Scan(&current); err != nil {
		t.Fatalf("query current_setting: %v", err)
	}
	if current != "" {
		t.Errorf("app.current_tenant leaked into conn2: got %q, want empty", current)
	}
}

// pgquote quotes a SQL string literal. The leak test sets a tenant id
// that does not appear in any user-controlled input, so a tiny inline
// quoter is sufficient.
func pgquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
