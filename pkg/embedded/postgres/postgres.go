// SPDX-License-Identifier: MIT

// Package postgres runs the PostgreSQL 16 binary bundle that backs
// §17.4 Embedded Mode. It wraps github.com/fergusstrange/embedded-postgres,
// which downloads a PostgreSQL 16 binary distribution on first use and
// starts it as a child process. The data directory lives under the
// Embedded Mode state directory so a lenny down without --purge
// preserves sessions and metadata.
//
// The gateway and controllers connect to this instance through the
// same Go storage interface they use against a production Postgres;
// only the connection string differs.
package postgres

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures the embedded Postgres instance.
type Config struct {
	// DataDir holds the PostgreSQL data directory and the downloaded
	// binary bundle. §17.4 places it at ~/.lenny/postgres/.
	DataDir string
	// Port is the loopback TCP port the instance listens on.
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
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(i.cfg.Port).
		Database(i.cfg.Database).
		Username(i.cfg.Username).
		Password(i.cfg.Password).
		// RuntimePath holds the extracted binaries; DataPath holds the
		// data directory. BinariesPath holds the downloaded archive.
		// Keeping all three under DataDir makes lenny down --purge a
		// single directory removal.
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
// accepts queries. It is used by lenny status.
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
