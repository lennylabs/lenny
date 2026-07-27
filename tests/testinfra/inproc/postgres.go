// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/embpg"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TESTING.md §12.7.a requires the tier-7a multi-component harness to
// boot "a single-binary Lenny with miniredis, an embedded Postgres
// adapter, and a fake Kubernetes API surface", and to keep each
// scenario inside a 15-second wall-clock budget with the whole tier
// inside 5 minutes.
//
// Starting a PostgreSQL child process costs a couple of seconds
// (initdb plus the first accepting connection), which no scenario can
// absorb once per Env: the tier boots one Env per multi-component
// scenario. The instance is therefore process-wide and started once,
// lazily, on the first Env.Start. Migrations are applied once into a
// template database, and each Env clones that template into its own
// database, which is a file copy of a schema-only cluster and costs
// tens of milliseconds. Every Env keeps a private database, so
// scenarios stay as isolated from each other as they were when each
// held its own in-memory store.
//
// The instance outlives any single Env, so a test binary that boots an
// Env must call ShutdownSharedPostgres before it exits, normally from
// TestMain. Without that the PostgreSQL child process survives the
// test binary.

// templateDatabase holds the migrated schema every per-Env database is
// cloned from.
const templateDatabase = "lenny_tier7a_template"

// pgvectorMigrations are the migrations the embedded PostgreSQL bundle
// cannot apply. The bundle is a stock PostgreSQL 16 distribution with
// no pgvector, and migration 0044 adds a `vector(256)` column whose
// type the server does not carry. Nothing the §4.2 SessionStore reads
// or writes lives in `agent_memory`, so the harness skips it and
// applies every other migration in order.
var pgvectorMigrations = map[string]bool{
	"0044_agent_memory_embedding.up.sql": true,
}

// upMigrations returns the `*.up.sql` files under migrations/ the
// embedded bundle can apply, in migration-number order.
//
// The whole set is applied rather than a curated subset naming the
// tables the §4.2 SessionStore touches: pgstore.New documents that its
// pool "must point at a database that has the migrations/ schema
// applied", and a subset drifts silently the next time a migration
// adds a column the adapter projects. The cost is paid once per test
// binary, into the template database.
func upMigrations(root string) ([]string, error) {
	found, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		return nil, fmt.Errorf("inproc: list migrations: %w", err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("inproc: no migrations found under %s", filepath.Join(root, "migrations"))
	}
	paths := make([]string, 0, len(found))
	for _, p := range found {
		if pgvectorMigrations[filepath.Base(p)] {
			continue
		}
		paths = append(paths, p)
	}
	// Every migration file is prefixed with a zero-padded fixed-width
	// number, so lexical order is migration order.
	sort.Strings(paths)
	return paths, nil
}

// sharedPostgres is the process-wide embedded PostgreSQL every Env in
// this test binary clones its database from.
type sharedPostgres struct {
	once sync.Once
	err  error

	// mu guards inst and dir. Every Env-side read of inst is ordered
	// after once.Do, but ShutdownSharedPostgres runs outside that edge
	// (from TestMain, after m.Run), so the pair needs the lock.
	mu   sync.Mutex
	inst *embpg.Instance
	dir  string

	// seq numbers the per-Env databases cloned from the template.
	seq atomic.Int64
}

var shared sharedPostgres

// startTimeout bounds the wait for the embedded instance to accept
// connections. It covers the one-time PostgreSQL bundle download on a
// host that has never run a store test; a warm host starts in about two
// seconds. spec: TESTING.md §12.7.a.
const startTimeout = 3 * time.Minute

// instance returns the process-wide embedded PostgreSQL, starting it
// and applying the template schema on first call. Every later call
// returns the same instance (or the same start failure).
func (s *sharedPostgres) instance(ctx context.Context) (*embpg.Instance, error) {
	s.once.Do(func() { s.err = s.start(ctx) })
	if s.err != nil {
		return nil, s.err
	}
	return s.inst, nil
}

func (s *sharedPostgres) start(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "inproc-postgres-")
	if err != nil {
		return fmt.Errorf("inproc: postgres data dir: %w", err)
	}
	inst := embpg.New(embpg.Config{
		DataDir:      dir,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: startTimeout,
	})
	if err := inst.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("inproc: start embedded postgres: %w", err)
	}
	s.mu.Lock()
	s.inst, s.dir = inst, dir
	s.mu.Unlock()
	if err := s.buildTemplate(ctx); err != nil {
		_ = inst.Stop()
		s.mu.Lock()
		s.inst, s.dir = nil, ""
		s.mu.Unlock()
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
}

// buildTemplate creates the template database and applies the session
// schema into it. Nothing connects to the template afterwards, which is
// what lets CREATE DATABASE ... TEMPLATE clone it.
func (s *sharedPostgres) buildTemplate(ctx context.Context) error {
	admin, err := pgxpool.New(ctx, s.inst.DSN())
	if err != nil {
		return fmt.Errorf("inproc: connect to embedded postgres: %w", err)
	}
	_, err = admin.Exec(ctx, `CREATE DATABASE `+templateDatabase)
	admin.Close()
	if err != nil {
		return fmt.Errorf("inproc: create template database: %w", err)
	}

	pool, err := pgxpool.New(ctx, s.dsnFor(templateDatabase))
	if err != nil {
		return fmt.Errorf("inproc: connect to template database: %w", err)
	}
	defer pool.Close()
	root, err := schematest.RepoRootCwd()
	if err != nil {
		return fmt.Errorf("inproc: locate migrations: %w", err)
	}
	paths, err := upMigrations(root)
	if err != nil {
		return err
	}
	for _, path := range paths {
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("inproc: read migration %s: %w", filepath.Base(path), err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("inproc: apply migration %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// dsnFor renders the connection string for one database on the shared
// instance.
func (s *sharedPostgres) dsnFor(database string) string {
	return fmt.Sprintf("postgres://lenny:lenny@127.0.0.1:%d/%s?sslmode=disable", s.inst.Port(), database)
}

// cloneDatabase creates a fresh database from the migrated template and
// returns its DSN.
func (s *sharedPostgres) cloneDatabase(ctx context.Context) (string, error) {
	name := fmt.Sprintf("lenny_env_%d_%d", os.Getpid(), s.seq.Add(1))
	admin, err := pgxpool.New(ctx, s.inst.DSN())
	if err != nil {
		return "", fmt.Errorf("inproc: connect to embedded postgres: %w", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE `+templateDatabase); err != nil {
		return "", fmt.Errorf("inproc: clone template database: %w", err)
	}
	return s.dsnFor(name), nil
}

// dropDatabase removes a per-Env database. Callers close their pool
// first; a connection that outlives the pool close blocks the drop, so
// the failure is reported rather than swallowed.
func (s *sharedPostgres) dropDatabase(ctx context.Context, name string) error {
	admin, err := pgxpool.New(ctx, s.inst.DSN())
	if err != nil {
		return fmt.Errorf("inproc: connect to embedded postgres: %w", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("inproc: drop database %s: %w", name, err)
	}
	return nil
}

// ShutdownSharedPostgres stops the process-wide embedded PostgreSQL and
// removes its data directory. A test binary that boots an inproc.Env
// calls it once from TestMain after m.Run; without it the PostgreSQL
// child process outlives the binary. It is a no-op when no Env ever
// started.
func ShutdownSharedPostgres() error {
	shared.mu.Lock()
	inst, dir := shared.inst, shared.dir
	shared.inst, shared.dir = nil, ""
	shared.mu.Unlock()
	if inst == nil {
		return nil
	}
	err := inst.Stop()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	return err
}

// tenantAnchoringStore is the harness's SessionStore: the §4.2 Postgres
// adapter with a test-only pre-insert that materialises the tenant row
// the adapter's `sessions.tenant_id REFERENCES tenants(id)` foreign key
// needs.
//
// Production provisions a tenant through the §24 admin surface before
// any session names it. A tier-7a scenario invents its tenant IDs at
// run time (`acme` and `globex` in the isolation scenario, the §10.2
// `default` elsewhere), so the harness materialises them on first use
// instead of requiring every scenario to declare them up front. Only
// Create is intercepted; every read and write path is the Postgres
// adapter's own.
type tenantAnchoringStore struct {
	sessionstore.Store
	pool *pgxpool.Pool

	mu   sync.Mutex
	seen map[string]struct{}
}

func newTenantAnchoringStore(inner sessionstore.Store, pool *pgxpool.Pool) *tenantAnchoringStore {
	return &tenantAnchoringStore{Store: inner, pool: pool, seen: map[string]struct{}{}}
}

func (s *tenantAnchoringStore) Create(ctx context.Context, sess sessionstore.Session) error {
	if err := s.ensureTenant(ctx, sess.TenantID); err != nil {
		return err
	}
	return s.Store.Create(ctx, sess)
}

// ensureTenant inserts the tenant registry row for tenantID once per
// process. The registry is platform-global, so the insert carries no
// app.current_tenant and is not subject to the §12.3 guard trigger.
func (s *tenantAnchoringStore) ensureTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	s.mu.Lock()
	_, ok := s.seen[tenantID]
	s.mu.Unlock()
	if ok {
		return nil
	}
	const insertSQL = `INSERT INTO tenants (id, display_name, genesis_nonce)
		VALUES ($1, $1, decode(md5($1), 'hex')) ON CONFLICT (id) DO NOTHING`
	if _, err := s.pool.Exec(ctx, insertSQL, tenantID); err != nil {
		return fmt.Errorf("inproc: anchor tenant %s: %w", tenantID, err)
	}
	s.mu.Lock()
	s.seen[tenantID] = struct{}{}
	s.mu.Unlock()
	return nil
}

// maxPoolConns bounds the per-Env connection pool. The load profiles
// drive 8 to 16 concurrent virtual users through the gateway, so the
// pool is sized above the widest profile and every request finds a
// connection rather than queueing behind one. spec: TESTING.md §12.7.a
// (scenario profiles and the 15-second per-scenario budget).
const maxPoolConns = 24

// newSessionPool opens the per-Env connection pool against dsn.
func newSessionPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("inproc: parse session store DSN: %w", err)
	}
	cfg.MaxConns = maxPoolConns
	// A pool that opens every connection on demand pays a handshake on
	// the first requests of a scenario's ramp, which lands inside the
	// measured window. Keeping the pool warm moves that cost into Setup.
	cfg.MinConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("inproc: open session store pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("inproc: ping session store: %w", err)
	}
	return pool, nil
}

// databaseNameFromDSN extracts the database name from a DSN produced by
// dsnFor. Used by Stop to drop the per-Env database.
func databaseNameFromDSN(dsn string) string {
	if i := strings.LastIndex(dsn, "/"); i >= 0 {
		rest := dsn[i+1:]
		if j := strings.Index(rest, "?"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return ""
}
