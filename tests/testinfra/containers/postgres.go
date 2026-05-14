// SPDX-License-Identifier: MIT

// Package containers wraps testcontainers-go primitives so Tier 2 tests
// can spin up real backing services (Postgres in this package, Redis and
// MinIO in subsequent ones) without each suite re-implementing the
// container lifecycle.
//
// See TESTING.md §10 and §12.2.1 for the design rationale.
package containers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file" // file:// source for migrate
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// defaultImage is the Postgres image used by tests. Pinned by major; tests
// that need a different minor inject a PostgresOptions{Image: …}.
const defaultImage = "postgres:16-alpine"

// PostgresOptions configures StartPostgres. Zero value is sensible.
type PostgresOptions struct {
	// Image is the container image. Defaults to defaultImage.
	Image string
	// Database is the initial database name. Defaults to "lenny_test".
	Database string
	// User is the superuser. Defaults to "postgres".
	User string
	// Password defaults to "postgres".
	Password string
	// MigrationsDir is an absolute or repo-relative path to a directory of
	// migrate-style numbered .up.sql / .down.sql files. If non-empty,
	// StartPostgres applies them with golang-migrate before returning.
	MigrationsDir string
	// StartupTimeout caps how long we wait for the container to accept
	// connections. Defaults to 30 seconds.
	StartupTimeout time.Duration
}

// Postgres is the handle returned by StartPostgres.
type Postgres struct {
	// DSN is a libpq-style connection string.
	DSN string
	// Pool is a ready-to-use pgxpool. Closed automatically on cleanup.
	Pool *pgxpool.Pool
	// container is the underlying testcontainers handle.
	container testcontainers.Container
}

// StartPostgres provisions a Postgres container, optionally applies
// migrations, and returns a Postgres handle. The cleanup hook stops the
// container at test end.
func StartPostgres(t *testing.T, opts PostgresOptions) *Postgres {
	t.Helper()
	if opts.Image == "" {
		opts.Image = defaultImage
	}
	if opts.Database == "" {
		opts.Database = "lenny_test"
	}
	if opts.User == "" {
		opts.User = "postgres"
	}
	if opts.Password == "" {
		opts.Password = "postgres"
	}
	if opts.StartupTimeout == 0 {
		opts.StartupTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()

	container, err := tcpostgres.Run(
		ctx,
		opts.Image,
		tcpostgres.WithDatabase(opts.Database),
		tcpostgres.WithUsername(opts.User),
		tcpostgres.WithPassword(opts.Password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(opts.StartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("StartPostgres: %v", err)
	}

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("StartPostgres: dsn: %v", err)
	}

	if opts.MigrationsDir != "" {
		if err := applyMigrations(dsn, opts.MigrationsDir); err != nil {
			t.Fatalf("StartPostgres: apply migrations: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("StartPostgres: pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return &Postgres{
		DSN:       dsn,
		Pool:      pool,
		container: container,
	}
}

// applyMigrations applies every numbered .up.sql under dir using
// golang-migrate. dir may be absolute or repo-relative.
func applyMigrations(dsn, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs(%s): %w", dir, err)
	}
	source := "file://" + abs

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open: %w", err)
	}
	defer db.Close()

	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		return fmt.Errorf("migrate driver: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance(source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate new: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateTo applies migrations up to (and including) the given version.
// Useful for stepwise tests that interleave assertions between migrations.
func (p *Postgres) MigrateTo(t *testing.T, migrationsDir string, version uint) {
	t.Helper()
	abs, err := filepath.Abs(migrationsDir)
	if err != nil {
		t.Fatalf("MigrateTo abs(%s): %v", migrationsDir, err)
	}
	source := "file://" + abs

	db, err := sql.Open("pgx", p.DSN)
	if err != nil {
		t.Fatalf("MigrateTo: sql.Open: %v", err)
	}
	defer db.Close()

	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		t.Fatalf("MigrateTo: migrate driver: %v", err)
	}
	m, err := migrate.NewWithDatabaseInstance(source, "postgres", driver)
	if err != nil {
		t.Fatalf("MigrateTo: migrate new: %v", err)
	}
	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("MigrateTo: %v", err)
	}
}

// MigrateDown rolls every applied migration back to the bottom. Returns
// migrate.ErrNoChange (wrapped) silently if there is nothing to undo.
func (p *Postgres) MigrateDown(t *testing.T, migrationsDir string) {
	t.Helper()
	abs, err := filepath.Abs(migrationsDir)
	if err != nil {
		t.Fatalf("MigrateDown abs(%s): %v", migrationsDir, err)
	}
	source := "file://" + abs

	db, err := sql.Open("pgx", p.DSN)
	if err != nil {
		t.Fatalf("MigrateDown: sql.Open: %v", err)
	}
	defer db.Close()

	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		t.Fatalf("MigrateDown: migrate driver: %v", err)
	}
	m, err := migrate.NewWithDatabaseInstance(source, "postgres", driver)
	if err != nil {
		t.Fatalf("MigrateDown: migrate new: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("MigrateDown: %v", err)
	}
}

// pgx/stdlib must be imported with its side-effecting init so the
// database/sql driver is registered under "pgx".
var _ = stdlib.GetDefaultDriver
