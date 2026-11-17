//go:build component

// SPDX-License-Identifier: MIT

// Package migrations_test exercises the migration framework end-to-end
// against a real Postgres container. Phase 1.5 ships the framework; this
// suite verifies round-trip apply, idempotency, rollback, and the
// regression scenario from TESTING.md §13.2 (apply N, seed, apply N+1,
// assert integrity, roll back, assert reversibility).
//
// The fixture migrations under tests/testdata/migrations are
// intentionally minimal — they exist to exercise the framework, not to
// model production schema. Production migrations land under migrations/
// when implementation phases begin.
package migrations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// fixtureMigrations returns the absolute path to the Phase 1.5 fixture
// migrations under tests/testdata/migrations.
func fixtureMigrations(t *testing.T) string {
	t.Helper()
	return filepath.Join(schematest.RepoRoot(t), "tests", "testdata", "migrations")
}

// spec: 12.2, 12.3
// diagnosis: StartPostgres failed or the migration framework could not
//
//	apply tests/testdata/migrations cleanly. Either the Postgres
//	container did not come up (Docker not running?), or the
//	migrate library could not parse the SQL files. Run
//	`docker info` and re-check tests/testdata/migrations/*.sql.
func TestApplyAllMigrationsCreatesExpectedSchema(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: fixtureMigrations(t),
	})

	ctx := context.Background()
	// Both fixture tables exist.
	mustHaveTable(t, ctx, pg, "tenants")
	mustHaveTable(t, ctx, pg, "widgets")
	// widgets has tenant_id as the leading column of the PK (R-01).
	mustHavePKLeadingColumn(t, ctx, pg, "widgets", "tenant_id")
	// The composite index from 0002 exists.
	mustHaveIndex(t, ctx, pg, "widgets", "widgets_tenant_created_idx")
}

// spec: 12.2
// diagnosis: A second migration apply did not return migrate.ErrNoChange.
//
//	The Phase 1.5 framework requires that re-running `up` after
//	a successful apply is a no-op (idempotent). The most likely
//	cause is the migrate library treating a clean schema_version
//	table as a fresh run.
func TestIdempotencyApplyTwiceIsNoOp(t *testing.T) {
	t.Parallel()
	dir := fixtureMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: dir,
	})

	// A second apply through MigrateTo with the highest version should be
	// a no-op (silent inside the framework). The wrapper swallows
	// migrate.ErrNoChange; nothing should fatal here.
	pg.MigrateTo(t, dir, 2)

	// Schema is unchanged.
	ctx := context.Background()
	mustHaveTable(t, ctx, pg, "widgets")
	mustHaveIndex(t, ctx, pg, "widgets", "widgets_tenant_created_idx")
}

// spec: 12.2
// diagnosis: MigrateDown left objects in place. After rolling every
//
//	migration back, the tenants and widgets tables must be gone
//	and the schema_migrations table must show version 0
//	(or no row at all, depending on migrate library behavior).
func TestRollbackRemovesAllObjects(t *testing.T) {
	t.Parallel()
	dir := fixtureMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: dir,
	})

	pg.MigrateDown(t, dir)

	ctx := context.Background()
	mustNotHaveTable(t, ctx, pg, "widgets")
	mustNotHaveTable(t, ctx, pg, "tenants")
}

// spec: 12.2, 12.3 (TESTING.md §13.2: apply N, seed, apply N+1, assert
//
//	integrity, roll back N+1, assert reversibility)
//
// diagnosis: Data seeded between migrations did not survive the apply
//
//	of the next migration, or the rollback of the second
//	migration disturbed the data from the first. The fixture
//	0002 adds an index; data must be untouched.
func TestRegressionSeedSurvivesIndexUpAndDown(t *testing.T) {
	t.Parallel()
	dir := fixtureMigrations(t)
	// Start with only migration 0001 applied.
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	pg.MigrateTo(t, dir, 1)

	ctx := context.Background()
	// Seed a tenant and a widget.
	mustExec(t, ctx, pg, `INSERT INTO tenants (id, name) VALUES ('acme', 'Acme Corp')`)
	mustExec(t, ctx, pg, `INSERT INTO widgets (tenant_id, id, name) VALUES ('acme', gen_random_uuid(), 'gizmo')`)

	beforeCount := mustQueryInt(t, ctx, pg, `SELECT COUNT(*) FROM widgets WHERE tenant_id = 'acme'`)
	if beforeCount != 1 {
		t.Fatalf("seed: widgets count = %d, want 1", beforeCount)
	}

	// Apply the index migration.
	pg.MigrateTo(t, dir, 2)
	mustHaveIndex(t, ctx, pg, "widgets", "widgets_tenant_created_idx")

	// Seed data must survive the schema change.
	afterUpCount := mustQueryInt(t, ctx, pg, `SELECT COUNT(*) FROM widgets WHERE tenant_id = 'acme'`)
	if afterUpCount != 1 {
		t.Fatalf("after 0002 up: widgets count = %d, want 1", afterUpCount)
	}

	// Roll back the index migration.
	pg.MigrateTo(t, dir, 1)
	mustNotHaveIndex(t, ctx, pg, "widgets", "widgets_tenant_created_idx")

	// Seed data must still be there.
	afterDownCount := mustQueryInt(t, ctx, pg, `SELECT COUNT(*) FROM widgets WHERE tenant_id = 'acme'`)
	if afterDownCount != 1 {
		t.Fatalf("after 0002 down: widgets count = %d, want 1", afterDownCount)
	}
}

// spec: 12.3
// diagnosis: gen_random_uuid() not available. The Phase 1.5 fixture
//
//	implicitly depends on pgcrypto (or PG13+'s built-in UUID
//	generation). Postgres 14+ is the spec's minimum and ships
//	gen_random_uuid() natively, so this should succeed without
//	an explicit CREATE EXTENSION.
func TestPostgresMeetsMinimumVersion(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	ctx := context.Background()
	major := mustQueryInt(t, ctx, pg, "SELECT current_setting('server_version_num')::int / 10000")
	if major < 14 {
		t.Errorf("Postgres major version = %d, want >= 14 (spec §12.3)", major)
	}
}

// --- helpers -------------------------------------------------------------

func mustExec(t *testing.T, ctx context.Context, pg *containers.Postgres, sql string, args ...any) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mustQueryInt(t *testing.T, ctx context.Context, pg *containers.Postgres, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

func mustHaveTable(t *testing.T, ctx context.Context, pg *containers.Postgres, name string) {
	t.Helper()
	var exists bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	if !exists {
		t.Fatalf("expected table %q to exist", name)
	}
}

func mustNotHaveTable(t *testing.T, ctx context.Context, pg *containers.Postgres, name string) {
	t.Helper()
	var exists bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	if exists {
		t.Fatalf("expected table %q to be absent", name)
	}
}

func mustHaveIndex(t *testing.T, ctx context.Context, pg *containers.Postgres, table, index string) {
	t.Helper()
	var exists bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2
		)`, table, index).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %q on %q: %v", index, table, err)
	}
	if !exists {
		t.Fatalf("expected index %q on table %q to exist", index, table)
	}
}

func mustNotHaveIndex(t *testing.T, ctx context.Context, pg *containers.Postgres, table, index string) {
	t.Helper()
	var exists bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2
		)`, table, index).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %q on %q: %v", index, table, err)
	}
	if exists {
		t.Fatalf("expected index %q on table %q to be absent", index, table)
	}
}

func mustHavePKLeadingColumn(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) {
	t.Helper()
	// pg_constraint.conkey lists the column attnums of the PK in order;
	// pg_attribute resolves the first one back to a name.
	var leading string
	err := pg.Pool.QueryRow(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'p' AND t.relname = $1
	`, table).Scan(&leading)
	if err != nil {
		t.Fatalf("read PK of %q: %v", table, err)
	}
	if leading != col {
		t.Errorf("leading column of %q PK = %q, want %q (R-01)", table, leading, col)
	}
}

// Quiet "imported and not used" if pgx becomes unused in a refactor.
var _ = pgx.ErrNoRows
