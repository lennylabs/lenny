// SPDX-License-Identifier: MIT

package embpg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/testcache"
)

// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
func TestDSNFormat(t *testing.T) {
	i := New(Config{
		DataDir:  "/tmp/pg",
		Port:     15433,
		Database: "lenny",
		Username: "lenny",
		Password: "lenny",
	})
	dsn := i.DSN()
	if !strings.HasPrefix(dsn, "postgres://lenny:lenny@127.0.0.1:15433/lenny") {
		t.Errorf("DSN = %q, want a loopback postgres URL", dsn)
	}
	// The embedded test Postgres runs without TLS on loopback.
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("DSN = %q, want sslmode=disable", dsn)
	}
}

// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
func TestNewDefaultsStartTimeout(t *testing.T) {
	i := New(Config{DataDir: "/tmp/pg"})
	if i.cfg.StartTimeout <= 0 {
		t.Errorf("StartTimeout = %s, want a positive default", i.cfg.StartTimeout)
	}
}

// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
func TestStopBeforeStartIsNoOp(t *testing.T) {
	i := New(Config{DataDir: t.TempDir()})
	if err := i.Stop(); err != nil {
		t.Errorf("Stop before Start errored: %v", err)
	}
}

// TestStartStopRoundTrip exercises a full embedded Postgres lifecycle.
// It downloads the PostgreSQL 16 binary bundle on first run, so it is
// skipped under -short.
//
// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
func TestStartStopRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	// Port 0 asks the kernel for a free ephemeral port so parallel test
	// binaries do not collide on a fixed port.
	i := New(Config{
		DataDir:      t.TempDir(),
		Port:         0,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := i.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := i.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	// Start resolves the ephemeral port and reflects it on Port() and DSN().
	if got := i.Port(); got == 0 {
		t.Error("Port() = 0 after Start with an ephemeral request; want the resolved port")
	}

	// Start is idempotent within a process.
	if err := i.Start(); err != nil {
		t.Errorf("second Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := i.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// TestSharedBinariesResolvesToTheTestCache pins where the PostgreSQL
// bundle is extracted. Every package that starts an embedded Postgres
// used to extract its own copy beside its data directory, which on a
// module-wide run is a few hundred megabytes per package under the
// system temp directory, and which put two concurrent extractions and
// any sweep of that directory in each other's way. One extraction in
// the shared cache replaces all of them.
//
// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
// diagnosis: The bundle is being extracted per instance again, so a
//
//	module-wide unit run re-extracts it once per package and
//	leaves each copy where a temp-directory sweep can delete
//	it mid-run.
func TestSharedBinariesResolvesToTheTestCache(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache-root")
	t.Setenv(testcache.EnvRoot, root)
	fallback := filepath.Join(t.TempDir(), "instance-bin")

	dir, done, err := sharedBinaries(fallback)
	if err != nil {
		t.Fatalf("sharedBinaries: %v", err)
	}
	if dir == fallback {
		t.Fatalf("sharedBinaries returned the per-instance fallback %q", fallback)
	}
	if !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		t.Errorf("bundle dir = %q, want it under the test cache root %q", dir, root)
	}
	// A failed start publishes nothing, so the next caller re-extracts
	// rather than inheriting a partial tree.
	done(errors.New("start failed"))
	if _, err := os.Stat(filepath.Join(dir, ".extracted")); err == nil {
		t.Error("a failed start marked the bundle as extracted")
	}

	// A successful start publishes the bundle, and the next caller
	// reuses it.
	dir2, done2, err := sharedBinaries(fallback)
	if err != nil {
		t.Fatalf("second sharedBinaries: %v", err)
	}
	done2(nil)
	if dir2 != dir {
		t.Errorf("second call returned %q, want the same bundle dir %q", dir2, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".extracted")); err != nil {
		t.Errorf("a successful start did not mark the bundle as extracted: %v", err)
	}

	// The third call takes the published bundle without waiting on the
	// lock, which is what keeps twenty-odd packages from serialising
	// behind one extraction that already happened.
	dir3, done3, err := sharedBinaries(fallback)
	if err != nil {
		t.Fatalf("third sharedBinaries: %v", err)
	}
	done3(nil)
	if dir3 != dir {
		t.Errorf("third call returned %q, want the published bundle dir %q", dir3, dir)
	}
}

// TestSharedBinariesFallsBackWhenTheCacheIsUnavailable keeps a host
// with no writable cache directory running the same tests, one
// extraction per instance, rather than failing them outright.
//
// spec: 17.4 (C2 store removal; the embedded-postgres test wrapper)
// diagnosis: An unwritable cache root turned into a hard failure
//
//	instead of falling back to a per-instance bundle.
func TestSharedBinariesFallsBackWhenTheCacheIsUnavailable(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv(testcache.EnvRoot, filepath.Join(blocker, "cache"))
	fallback := filepath.Join(t.TempDir(), "instance-bin")

	dir, done, err := sharedBinaries(fallback)
	if err != nil {
		t.Fatalf("sharedBinaries: %v", err)
	}
	done(nil)
	if dir != fallback {
		t.Errorf("bundle dir = %q, want the per-instance fallback %q", dir, fallback)
	}
}
