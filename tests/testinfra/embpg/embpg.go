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
	"os"
	"path/filepath"
	"runtime"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/tests/testinfra/testcache"
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
	binaries, extracted, err := sharedBinaries(filepath.Join(i.cfg.DataDir, "bin"))
	if err != nil {
		return fmt.Errorf("embedded postgres: %w", err)
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(i.cfg.Port).
		Database(i.cfg.Database).
		Username(i.cfg.Username).
		Password(i.cfg.Password).
		// BinariesPath holds the extracted PostgreSQL bundle, which is
		// read-only once extracted and therefore shared between
		// instances. RuntimePath (the password file and other per-run
		// scratch) and DataPath (the cluster) stay under DataDir, so
		// teardown of one instance is still a single directory removal
		// and two instances never write to the same cluster.
		RuntimePath(filepath.Join(i.cfg.DataDir, "runtime")).
		BinariesPath(binaries).
		DataPath(filepath.Join(i.cfg.DataDir, "data")).
		StartTimeout(i.cfg.StartTimeout)
	db := embeddedpostgres.NewDatabase(cfg)
	startErr := db.Start()
	extracted(startErr)
	if startErr != nil {
		return fmt.Errorf("embedded postgres: start: %w", startErr)
	}
	i.db = db
	i.started = true
	return nil
}

// bundleKey names the shared cache entry for the extracted PostgreSQL
// bundle. The bundle is platform-specific, so the key carries the
// target it was extracted for.
func bundleKey() string {
	return filepath.Join("embedded-postgres", fmt.Sprintf("v16-%s-%s", runtime.GOOS, runtime.GOARCH))
}

// sharedBinaries resolves the directory the PostgreSQL bundle is
// extracted into, and returns a callback the caller invokes with the
// outcome of Start.
//
// Extraction is the expensive and fragile part of starting an embedded
// PostgreSQL. It unpacks a 58 MB bundle, and the upstream extractor
// renames each file from a sibling temp directory into place, so two
// processes extracting into the same tree, or a temp-directory sweep
// landing on the tree mid-extraction, produce a rename failure on an
// arbitrary bundle file. Extracting once into the shared test cache
// removes both. Before this, every package that starts an embedded
// PostgreSQL extracted its own copy beside its data directory, and a
// module-wide run has enough of them to put more than a gigabyte under
// the system temp directory, which on a tmpfs /tmp is resident memory.
//
// The callback holds the host-wide lock until Start returns, so exactly
// one process extracts and every other one waits for it rather than
// racing it. A Start that fails leaves no completion marker, so the
// next caller re-extracts from scratch instead of inheriting a partial
// tree. When the cache is unavailable the fallback directory is used
// and no sharing happens, which is the behaviour this had before.
func sharedBinaries(fallback string) (string, func(error), error) {
	noop := func(error) {}
	key := bundleKey()
	dir, err := testcache.Dir(key)
	if err != nil {
		// The shared cache is an optimisation. A host with no writable
		// cache directory keeps the bundle beside the data directory
		// and extracts its own copy, which is what every instance did
		// before, rather than failing the test outright.
		return fallback, noop, nil
	}
	marker := filepath.Join(dir, ".extracted")
	if _, err := os.Stat(marker); err == nil {
		return dir, noop, nil
	}
	unlock, err := testcache.Lock(key)
	if err != nil {
		return "", nil, err
	}
	// Another process may have finished the extraction while this one
	// waited for the lock.
	if _, err := os.Stat(marker); err == nil {
		unlock()
		return dir, noop, nil
	}
	// Whatever is there came from an extraction that did not finish.
	if err := os.RemoveAll(dir); err != nil {
		unlock()
		return "", nil, fmt.Errorf("clear partial postgres bundle %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		unlock()
		return "", nil, fmt.Errorf("create postgres bundle dir %s: %w", dir, err)
	}
	return dir, func(startErr error) {
		defer unlock()
		if startErr != nil {
			return
		}
		if f, err := os.Create(marker); err == nil {
			_ = f.Close()
		}
	}, nil
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
