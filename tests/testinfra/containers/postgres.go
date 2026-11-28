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

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// defaultImage is the Postgres image used by tests. It is the official
// pgvector image (a postgres:16 base with the pgvector `vector`
// extension preinstalled) so the §9.4 agent_memory.embedding column and
// its ivfflat index, added by migration 0044, apply against the same
// container every other store test uses. Pinned by major; tests that
// need a different image inject a PostgresOptions{Image: …}.
const defaultImage = "pgvector/pgvector:pg16"

// postgresPort is the container port Postgres listens on.
const postgresPort = "5432/tcp"

// defaultPostgresStartupTimeout is the StartupTimeout of StartPostgres and
// SharedPostgres when the caller sets none. It matches the 60 seconds of the
// MinIO and fake GCS fixtures. At 30 seconds, a burst of parallel starts on
// the 4-vCPU gate-main runner, or on a loaded workstation, ran past the bound
// in the Docker API calls or in the wait for the log line, and a rerun on the
// same commit passed (GATE-I16). The wait ends as soon as the container is
// ready, so the bound adds time only to a start that would otherwise fail.
// startContainer gives each attempt it retries its own context of this
// length, so a wait for a stopped session reaper does not shorten it.
const defaultPostgresStartupTimeout = 60 * time.Second

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
	// StartupTimeout caps how long each start attempt waits for the
	// container to be created, to log that it accepts connections, and to
	// publish its port. Defaults to defaultPostgresStartupTimeout.
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

// Router wraps the container's pool in the §12.3 R-03 single-shard
// StoreRouter in Postgres-only mode (no Redis). Billing and audit store
// component and integration tests construct their stores through it so
// the test path matches production: billing/audit writes route through
// StoreRouter rather than holding a raw pool. spec: §12.3 R-03.
func (p *Postgres) Router(t testing.TB) *storerouter.SingleShardRouter {
	t.Helper()
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: p.Pool})
	if err != nil {
		t.Fatalf("containers: store router: %v", err)
	}
	return r
}

// StartPostgres provisions a Postgres container, optionally applies
// migrations, and returns a Postgres handle. The cleanup hook stops the
// container at test end.
func StartPostgres(t testing.TB, opts PostgresOptions) *Postgres {
	t.Helper()
	opts = opts.withDefaults()
	container, err := startContainer(defaultStaleReaperRetry, opts.StartupTimeout, func(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
		return runPostgres(ctx, opts)
	})
	if err != nil {
		t.Fatalf("StartPostgres: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()
	return finishPostgres(t, ctx, container, opts)
}

// withDefaults fills the zero fields of opts with their defaults.
func (opts PostgresOptions) withDefaults() PostgresOptions {
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
		opts.StartupTimeout = defaultPostgresStartupTimeout
	}
	return opts
}

// runPostgres starts a Postgres container configured by opts, with extra
// customizers applied after the defaults, and waits until it accepts
// connections.
func runPostgres(ctx context.Context, opts PostgresOptions, extra ...testcontainers.ContainerCustomizer) (*tcpostgres.PostgresContainer, error) {
	customizers := append([]testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase(opts.Database),
		tcpostgres.WithUsername(opts.User),
		tcpostgres.WithPassword(opts.Password),
		// The mapped-port strategy holds the start until Docker has
		// published 5432/tcp, so a DSN read right after the start does not
		// see `port "5432/tcp" not found`. postgresDSN retries the same
		// error as a second guard.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(opts.StartupTimeout),
			wait.ForMappedPort(postgresPort).
				WithStartupTimeout(opts.StartupTimeout),
		),
	}, extra...)
	c, err := tcpostgres.Run(ctx, opts.Image, customizers...)
	if err != nil {
		return nil, fmt.Errorf("run postgres container: %w", err)
	}
	return c, nil
}

// finishPostgres registers the container's termination with t, applies
// opts.MigrationsDir, and opens the handle's pool.
func finishPostgres(t testing.TB, ctx context.Context, container *tcpostgres.PostgresContainer, opts PostgresOptions) *Postgres {
	t.Helper()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	dsn, err := postgresDSN(ctx, container)
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

// Stop terminates the Postgres container mid-test so a caller can inject a
// genuine Postgres outage: subsequent pool operations fail to reach the
// backend rather than returning stale success. It is the testcontainers
// analogue of the tier-8 "scale the Postgres Deployment to zero" injection,
// used by the §25.11 backup-degradation failure tests. The t.Cleanup
// registered by StartPostgres tolerates a double Terminate.
func (p *Postgres) Stop(t testing.TB) {
	t.Helper()
	if p.container == nil {
		t.Fatal("Postgres.Stop: the database lives on a SharedPostgres container, which one test may not stop; use StartPostgres for an outage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.container.Terminate(ctx); err != nil {
		t.Fatalf("Postgres.Stop: terminate: %v", err)
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
	// The driver holds one connection checked out of db, which db.Close
	// does not close; m.Close returns it, so the database has no session
	// left once the migration returns (SharedPostgres clones it next).
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateTo applies migrations up to (and including) the given version.
// Useful for stepwise tests that interleave assertions between migrations.
func (p *Postgres) MigrateTo(t testing.TB, migrationsDir string, version uint) {
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
func (p *Postgres) MigrateDown(t testing.TB, migrationsDir string) {
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
