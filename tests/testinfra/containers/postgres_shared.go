// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// sharedTemplateDatabase is the database a SharedPostgres container
// migrates once and clones for every test.
const sharedTemplateDatabase = "lenny_template"

// sharedMaxConnections is the connection limit of a SharedPostgres
// container. Every test's database lives on the one server, so tests that
// run in parallel share its connection limit; the Postgres default of 100
// is below what a package's parallel tests can hold open together.
const sharedMaxConnections = "500"

// SharedPostgres is one Postgres container that every test in a package
// binary reuses. Starting a container and migrating it takes a few seconds,
// so a package that builds a fresh Postgres for each of many tests spends
// much of its wall time there (TEST-GAPS T-17.4.31). The first Database
// call starts the container and migrates a template database; every call
// then clones that template into a database of the test's own, so a test
// starts from a migrated, empty database that no other test sees. A
// package declares one SharedPostgres value and calls Stop from TestMain
// after m.Run returns. A test that stops Postgres to inject an outage keeps
// calling StartPostgres for a container of its own.
//
// Databases on one server share what Postgres keeps per cluster rather
// than per database: roles, the connection limit, and the server process.
// The migrations create their roles (lenny_app, lenny_erasure, and others)
// once, when they migrate the template, and every clone carries the
// template's grants to them. A test that alters a role therefore alters it
// for every other test on the container, and keeps a container of its own.
type SharedPostgres struct {
	once      sync.Once
	container *tcpostgres.PostgresContainer
	admin     *pgxpool.Pool
	base      *url.URL
	migrated  string
	err       error
	// cloneMu serializes the clones: CREATE DATABASE fails while another
	// session is connected to the template, and a concurrent clone is one.
	cloneMu sync.Mutex
	n       atomic.Int64
}

// Database returns a database of t's own on the shared container, cloned
// from the template migrated with migrationsDir. The first call starts the
// container and migrates the template, and every later call must name the
// same migrations directory. The database's pool is closed and the
// database dropped at t's cleanup.
func (s *SharedPostgres) Database(t testing.TB, migrationsDir string) *Postgres {
	t.Helper()
	s.once.Do(func() { s.err = s.start(migrationsDir) })
	if s.err != nil {
		t.Fatalf("SharedPostgres: %v", s.err)
	}
	if migrationsDir != s.migrated {
		t.Fatalf("SharedPostgres: template migrated from %q, test asked for %q", s.migrated, migrationsDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("lenny_t%d", s.n.Add(1))
	if err := s.clone(ctx, name); err != nil {
		t.Fatalf("SharedPostgres: %v", err)
	}
	dsn := s.dsnFor(name)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("SharedPostgres: pool for %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := s.admin.Exec(dropCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("SharedPostgres: drop %s: %v", name, err)
		}
	})
	return &Postgres{DSN: dsn, Pool: pool}
}

// start runs the container, migrates the template, and opens the admin
// pool on the maintenance database. A container that started is kept even
// when a later step fails, so Stop still terminates it.
func (s *SharedPostgres) start(migrationsDir string) error {
	opts := PostgresOptions{Database: sharedTemplateDatabase, MigrationsDir: migrationsDir}.withDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()
	c, err := runPostgres(ctx, opts, testcontainers.WithCmdArgs("-c", "max_connections="+sharedMaxConnections))
	if err != nil {
		return err
	}
	s.container = c
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("dsn: %w", err)
	}
	if s.base, err = url.Parse(dsn); err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	if migrationsDir != "" {
		// applyMigrations closes its connection, so the template has no
		// session left open when the first clone runs.
		if err := applyMigrations(dsn, migrationsDir); err != nil {
			return fmt.Errorf("migrate the template: %w", err)
		}
	}
	s.migrated = migrationsDir
	if s.admin, err = pgxpool.New(ctx, s.dsnFor("postgres")); err != nil {
		return fmt.Errorf("admin pool: %w", err)
	}
	return nil
}

// templateInUse is the SQLSTATE of a CREATE DATABASE refused because a
// session is connected to the template (object_in_use).
const templateInUse = "55006"

// clone creates the database name from the template. A server process
// exits asynchronously after its client disconnects, so the migration's
// session can still be connected to the template for a moment after the
// migration returns; a clone refused for that reason is retried until ctx
// ends.
func (s *SharedPostgres) clone(ctx context.Context, name string) error {
	s.cloneMu.Lock()
	defer s.cloneMu.Unlock()
	stmt := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() + " TEMPLATE " + pgx.Identifier{sharedTemplateDatabase}.Sanitize()
	for {
		_, err := s.admin.Exec(ctx, stmt)
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != templateInUse {
			return fmt.Errorf("clone the template into %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("clone the template into %s: %w", name, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// dsnFor is the container's DSN with the database replaced by name.
func (s *SharedPostgres) dsnFor(name string) string {
	u := *s.base
	u.Path = "/" + name
	return u.String()
}

// Stop terminates the shared container when it was started. It is safe to
// call when no test started it.
func (s *SharedPostgres) Stop() error {
	if s.admin != nil {
		s.admin.Close()
	}
	if s.container == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate the shared postgres container: %w", err)
	}
	return nil
}
