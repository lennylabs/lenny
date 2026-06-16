// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
)

// TestPgStoreRoundTrip brings up an embedded Postgres, creates the
// platform-scoped bootstrap_seed_snapshot table from migration 0117, and
// exercises the §25.10 desired-state snapshot lifecycle against the
// durable store. It downloads the PostgreSQL bundle, so it is skipped
// under -short.
//
// spec: §25.10 lines 3811-3820.
func TestPgStoreRoundTrip_spec_25_10(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; §17.4 forbids hardcoded ports and they collide under parallel tests
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

	up, err := migrations.FS.ReadFile("0117_bootstrap_seed_snapshot.up.sql")
	if err != nil {
		t.Fatalf("read 0117: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0117: %v", err)
	}

	s := pgstore.New(pool)

	// Cold start: no live row.
	if _, ok, err := s.Get(ctx, driftservice.SnapshotLive); err != nil || ok {
		t.Fatalf("cold-start Get: ok=%v err=%v, want false/nil", ok, err)
	}

	// Put the live snapshot, then read it back with provenance intact.
	written := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	live := driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(5)}}},
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    written,
		WrittenBy:    "helm",
	}
	if err := s.Put(ctx, live); err != nil {
		t.Fatalf("Put live: %v", err)
	}
	got, ok, err := s.Get(ctx, driftservice.SnapshotLive)
	if err != nil || !ok {
		t.Fatalf("Get live: ok=%v err=%v", ok, err)
	}
	if got.Source != driftservice.SourceHelmValues || got.WrittenBy != "helm" {
		t.Errorf("provenance = %s/%s, want helm-values/helm", got.Source, got.WrittenBy)
	}
	if !got.WrittenAt.Equal(written) {
		t.Errorf("writtenAt = %v, want %v", got.WrittenAt, written)
	}
	pools, _ := got.DesiredState["pools"].(map[string]any)
	if pools == nil {
		t.Fatalf("desired state round-trip lost pools: %v", got.DesiredState)
	}

	// Put again upserts in place (a snapshot refresh) rather than erroring
	// on the primary key.
	refreshed := live
	refreshed.Source = driftservice.SourceSnapshotRefresh
	refreshed.WrittenAt = written.Add(time.Hour)
	refreshed.DesiredState = map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(9)}}}
	if err := s.Put(ctx, refreshed); err != nil {
		t.Fatalf("Put refresh: %v", err)
	}
	got, _, _ = s.Get(ctx, driftservice.SnapshotLive)
	if got.Source != driftservice.SourceSnapshotRefresh {
		t.Errorf("post-refresh source = %s, want snapshot-refresh", got.Source)
	}

	// A target row coexists with the live row and carries upgrade_id.
	target := driftservice.Snapshot{
		ID:           driftservice.SnapshotTarget,
		DesiredState: map[string]any{"pools": map[string]any{}},
		Source:       driftservice.SourceHelmValues,
		UpgradeID:    "upgrade-42",
		WrittenAt:    written,
		WrittenBy:    "ops",
	}
	if err := s.Put(ctx, target); err != nil {
		t.Fatalf("Put target: %v", err)
	}
	gotTarget, ok, err := s.Get(ctx, driftservice.SnapshotTarget)
	if err != nil || !ok {
		t.Fatalf("Get target: ok=%v err=%v", ok, err)
	}
	if gotTarget.UpgradeID != "upgrade-42" {
		t.Errorf("target upgradeId = %q, want upgrade-42", gotTarget.UpgradeID)
	}

	// The CHECK constraint rejects a third desired-state row (F-25.10.13).
	bad := live
	bad.ID = "bogus"
	if err := s.Put(ctx, bad); err == nil {
		t.Error("Put with id=bogus succeeded; the id CHECK should reject it")
	}
}
