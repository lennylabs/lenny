// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

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
	// Embedded Mode Postgres runs without TLS on loopback.
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("DSN = %q, want sslmode=disable", dsn)
	}
}

func TestNewDefaultsStartTimeout(t *testing.T) {
	i := New(Config{DataDir: "/tmp/pg"})
	if i.cfg.StartTimeout <= 0 {
		t.Errorf("StartTimeout = %s, want a positive default", i.cfg.StartTimeout)
	}
}

func TestStopBeforeStartIsNoOp(t *testing.T) {
	i := New(Config{DataDir: t.TempDir()})
	if err := i.Stop(); err != nil {
		t.Errorf("Stop before Start errored: %v", err)
	}
}

// TestStartStopRoundTrip exercises a full embedded Postgres lifecycle.
// It downloads the PostgreSQL 16 binary bundle on first run, so it is
// skipped under -short.
func TestStartStopRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	i := New(Config{
		DataDir:      t.TempDir(),
		Port:         15499,
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
