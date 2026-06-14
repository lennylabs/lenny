// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsidem/pgstore"
)

// TestPgStoreRoundTrip brings up an embedded Postgres, creates the
// platform-scoped ops_idempotency_keys table from migration 0116, and
// exercises the §25.4 (key, caller_id) lifecycle against the durable
// store. It downloads the PostgreSQL bundle, so it is skipped under
// -short.
//
// spec: §25.4 lines 2011-2130.
func TestPgStoreRoundTrip_spec_25_4(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15497,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// The ops_idempotency_keys table is platform-scoped (no FK / role
	// deps), so apply just the 0116 up migration directly.
	up, err := migrations.FS.ReadFile("0116_ops_idempotency_keys.up.sql")
	if err != nil {
		t.Fatalf("read 0116: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0116: %v", err)
	}

	s := pgstore.New(pool)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert -> in-progress.
	rec, res, err := s.Claim(ctx, "k", "alice", "POST /v1/admin/backups", time.Hour, now)
	if err != nil || res != opsidem.ClaimInserted {
		t.Fatalf("first claim: res=%v err=%v, want inserted", res, err)
	}
	if rec.Status != opsidem.StatusInProgress {
		t.Errorf("inserted status = %q, want in_progress", rec.Status)
	}

	// Same caller again before completion -> in-progress.
	if _, res, _ = s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(time.Second)); res != opsidem.ClaimInProgress {
		t.Fatalf("second claim res = %v, want in-progress", res)
	}

	// Different caller, same key -> owned-by-other.
	if _, res, _ = s.Claim(ctx, "k", "bob", "x", time.Hour, now.Add(time.Second)); res != opsidem.ClaimOwnedByOther {
		t.Fatalf("bob claim res = %v, want owned-by-other", res)
	}

	// Complete -> replay returns the cached response + status.
	if err := s.Complete(ctx, "k", "alice", 201, []byte(`{"ok":true}`), now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	rec, res, err = s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(2*time.Second))
	if err != nil || res != opsidem.ClaimReplay {
		t.Fatalf("post-complete claim: res=%v err=%v, want replay", res, err)
	}
	if rec.StatusCode != 201 || string(rec.Response) != `{"ok":true}` {
		t.Errorf("replay rec = %+v, want 201/{\"ok\":true}", rec)
	}

	// Prune past expiry removes the row; a fresh claim then inserts.
	if n, err := s.PruneExpired(ctx, now.Add(2*time.Hour)); err != nil || n != 1 {
		t.Fatalf("prune: n=%d err=%v, want 1/nil", n, err)
	}
	if _, res, _ = s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(2*time.Hour)); res != opsidem.ClaimInserted {
		t.Errorf("post-prune claim res = %v, want inserted", res)
	}

	// Fail deletes the row so a retry re-executes.
	s.Claim(ctx, "f", "alice", "x", time.Hour, now)
	if err := s.Fail(ctx, "f", "alice", now); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if _, res, _ = s.Claim(ctx, "f", "alice", "x", time.Hour, now.Add(time.Second)); res != opsidem.ClaimInserted {
		t.Errorf("post-fail claim res = %v, want inserted (retryable)", res)
	}
}
