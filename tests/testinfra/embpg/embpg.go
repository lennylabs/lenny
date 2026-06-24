// SPDX-License-Identifier: MIT

// Package embpg runs an in-process PostgreSQL 16 binary bundle for
// store-package tests. It wraps github.com/fergusstrange/embedded-postgres,
// which downloads a PostgreSQL 16 binary distribution on first use and
// starts it as a child process, so a Tier 1/2 store test can exercise a
// real Postgres without a container runtime.
//
// This wrapper was relocated out of the §17.4 Embedded Mode stack
// (pkg/embedded/postgres) when Embedded Mode moved to in-cluster
// in-memory stores: it is a test-support surface that the store-package
// tests own, independent of the runtime Embedded Mode topology.
//
// spec: 17.4 (C2 store removal; the embedded stack no longer ships an
// embedded Postgres, so the shared embedded-postgres test wrapper lives
// in test infrastructure)
package embpg

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures the embedded Postgres instance.
type Config struct {
	// DataDir holds the PostgreSQL data directory and the downloaded
	// binary bundle. Tests pass a t.TempDir() so the bundle and data are
	// cleaned up with the test.
	DataDir string
	// Port is the loopback TCP port the instance listens on. Zero asks
	// the kernel for a free ephemeral port at Start time, which lets
	// concurrent embedded instances (for example parallel test binaries)
	// avoid fixed-port collisions; the chosen port is then reflected by
	// Port() and DSN().
	Port uint32
	// Database, Username, and Password name the bootstrap database
	// and superuser role created on first start.
	Database string
	Username string
	Password string
	// StartTimeout bounds the wait for the instance to accept
	// connections. Zero defaults to 90 seconds, which covers the
	// one-time binary download on a first run.
	StartTimeout time.Duration
}

// Instance is a running embedded Postgres process.
type Instance struct {
	cfg     Config
	db      *embeddedpostgres.EmbeddedPostgres
	started bool
}

// New constructs an Instance. The process is not started until Start
// is called.
func New(cfg Config) *Instance {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 90 * time.Second
	}
	return &Instance{cfg: cfg}
}

// Port reports the loopback TCP port the instance listens on. When the
// Config requested an ephemeral port (Port == 0) the value is resolved
// during Start, so Port is meaningful only after a successful Start.
func (i *Instance) Port() uint32 {
	return i.cfg.Port
}

// freePort asks the kernel for an unused ephemeral TCP port on the
// loopback interface and returns it. The listener is closed before the
// port is returned so the embedded Postgres process can bind it; this is
// the free-port pattern adapted to a numeric port the embedded library
// requires up front.
func freePort() (uint32, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve ephemeral port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		return 0, fmt.Errorf("release ephemeral port: %w", err)
	}
	return uint32(port), nil
}

// DSN returns the libpq connection string for the instance. It is
// valid only while the instance is running.
func (i *Instance) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		i.cfg.Username, i.cfg.Password, i.cfg.Port, i.cfg.Database)
}

// Start downloads the PostgreSQL bundle when absent and starts the
// instance. Start is idempotent within a process: a second call on an
// already-started Instance is a no-op.
func (i *Instance) Start() error {
	if i.started {
		return nil
	}
	if i.cfg.Port == 0 {
		port, err := freePort()
		if err != nil {
			return fmt.Errorf("embedded postgres: %w", err)
		}
		i.cfg.Port = port
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(i.cfg.Port).
		Database(i.cfg.Database).
		Username(i.cfg.Username).
		Password(i.cfg.Password).
		// RuntimePath holds the extracted binaries; DataPath holds the
		// data directory. BinariesPath holds the downloaded archive.
		// Keeping all three under DataDir makes teardown a single
		// directory removal.
		RuntimePath(filepath.Join(i.cfg.DataDir, "runtime")).
		BinariesPath(filepath.Join(i.cfg.DataDir, "bin")).
		DataPath(filepath.Join(i.cfg.DataDir, "data")).
		StartTimeout(i.cfg.StartTimeout)
	db := embeddedpostgres.NewDatabase(cfg)
	if err := db.Start(); err != nil {
		return fmt.Errorf("embedded postgres: start: %w", err)
	}
	i.db = db
	i.started = true
	return nil
}

// Stop terminates the instance. The data directory is left in place so
// a subsequent Start reuses it. Stop is idempotent.
func (i *Instance) Stop() error {
	if !i.started || i.db == nil {
		return nil
	}
	if err := i.db.Stop(); err != nil {
		return fmt.Errorf("embedded postgres: stop: %w", err)
	}
	i.started = false
	return nil
}

// Ping opens a short-lived connection pool and verifies the instance
// accepts queries.
func (i *Instance) Ping(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, i.DSN())
	if err != nil {
		return fmt.Errorf("embedded postgres: connect: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("embedded postgres: ping: %w", err)
	}
	return nil
}
