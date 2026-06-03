// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// startPG brings up an embedded Postgres with migration 0124 applied and
// returns a connected pool. It downloads the PostgreSQL bundle, so it is
// skipped under -short.
func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15523,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	up, err := migrations.FS.ReadFile("0124_platform_upgrade.up.sql")
	if err != nil {
		t.Fatalf("read 0124: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0124: %v", err)
	}
	return pool
}

// TestUpgradeStateRoundTrip_spec_25_8 exercises the §25.8 durable
// upgrade-state store: a cold Load reports no upgrade, a Save persists the
// State, and a reload reconstructs every field — the durability backbone
// that lets a paused upgrade survive a leader handoff (spec line 3560).
func TestUpgradeStateRoundTrip_spec_25_8(t *testing.T) {
	pool := startPG(t)
	store := pgstore.New(pool)
	ctx := context.Background()

	if _, ok, err := store.Load(ctx); err != nil || ok {
		t.Fatalf("cold Load = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	started := time.Now().UTC().Truncate(time.Millisecond)
	st := upgradeservice.State{
		OperationID:   "upgrade-abc",
		Phase:         upgrade.OpsRoll,
		TargetVersion: "1.6.0",
		ImageDigest:   "sha256:deadbeef",
		StartedBy:     "alice@acme.com",
		StartedAt:     started,
		UpdatedAt:     started.Add(time.Minute),
		Paused:        true,
		Verified:      false,
		Reason:        "awaiting proceed",
	}
	if err := store.Save(ctx, st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after Save = (ok=%v, err=%v)", ok, err)
	}
	if got.OperationID != st.OperationID || got.Phase != st.Phase ||
		got.TargetVersion != st.TargetVersion || got.ImageDigest != st.ImageDigest ||
		got.StartedBy != st.StartedBy || got.Paused != st.Paused || got.Reason != st.Reason {
		t.Fatalf("reloaded state = %+v, want %+v", got, st)
	}
	if !got.UpdatedAt.Equal(st.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, st.UpdatedAt)
	}
}

// TestUpgradeStateTerminalReplaces_spec_25_8 covers that a terminal
// upgrade stamps completed_at and that a new upgrade overwrites the prior
// singleton (the §25.8 single-upgrade-at-a-time model).
func TestUpgradeStateTerminalReplaces_spec_25_8(t *testing.T) {
	pool := startPG(t)
	store := pgstore.New(pool)
	ctx := context.Background()

	done := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Save(ctx, upgradeservice.State{
		OperationID: "upgrade-1", Phase: upgrade.Complete, TargetVersion: "1.6.0",
		StartedBy: "alice", StartedAt: done.Add(-time.Hour), UpdatedAt: done,
	}); err != nil {
		t.Fatalf("Save terminal: %v", err)
	}
	var completedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT completed_at FROM platform_upgrade_state WHERE id='singleton'`).Scan(&completedAt); err != nil {
		t.Fatalf("query completed_at: %v", err)
	}
	if completedAt == nil {
		t.Fatalf("terminal upgrade must stamp completed_at")
	}

	// A new upgrade overwrites the singleton.
	if err := store.Save(ctx, upgradeservice.State{
		OperationID: "upgrade-2", Phase: upgrade.Preflight, TargetVersion: "1.7.0",
		StartedBy: "bob", StartedAt: done, UpdatedAt: done, Paused: true,
	}); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, _, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.OperationID != "upgrade-2" || got.Phase != upgrade.Preflight {
		t.Fatalf("singleton not replaced: %+v", got)
	}
}

// TestCheckCacheRoundTrip_spec_25_8 exercises the §25.8 release-channel
// cache store: a cold Load is empty, a Save persists the manifest, and a
// reload returns it — the durable cache an unreachable channel falls back
// to (spec line 3413).
func TestCheckCacheRoundTrip_spec_25_8(t *testing.T) {
	pool := startPG(t)
	cache := pgstore.NewCheckCache(pool)
	ctx := context.Background()

	if _, ok, err := cache.Load(ctx); err != nil || ok {
		t.Fatalf("cold cache Load = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	checkedAt := time.Now().UTC().Truncate(time.Millisecond)
	in := upgradeservice.CachedCheck{CheckedAt: checkedAt, CurrentVersion: "1.5.0"}
	in.Manifest.Version = "1.6.0"
	if err := cache.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := cache.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load = (ok=%v, err=%v)", ok, err)
	}
	if got.Manifest.Version != "1.6.0" || got.CurrentVersion != "1.5.0" || !got.CheckedAt.Equal(checkedAt) {
		t.Fatalf("reloaded cache = %+v", got)
	}
}
