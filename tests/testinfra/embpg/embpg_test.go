// SPDX-License-Identifier: MIT

package embpg

import (
	"context"
	"strings"
	"testing"
	"time"
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
