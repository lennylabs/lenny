// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
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

	// §25.10 line 3789: PromoteTargetToLive atomically swaps the target row
	// into the live row and removes the target row. After the promote the
	// live row carries the target's desired state and upgrade id, and the
	// target row is gone, so GET /v1/admin/drift?against=target returns
	// DRIFT_NO_TARGET_SNAPSHOT. F-DR-3.
	if err := s.PromoteTargetToLive(ctx, "upgrade-42"); err != nil {
		t.Fatalf("PromoteTargetToLive: %v", err)
	}
	promotedLive, ok, err := s.Get(ctx, driftservice.SnapshotLive)
	if err != nil || !ok {
		t.Fatalf("Get live after promote: ok=%v err=%v", ok, err)
	}
	if promotedLive.UpgradeID != "upgrade-42" {
		t.Errorf("post-promote live upgradeId = %q, want upgrade-42 (target promoted in)", promotedLive.UpgradeID)
	}
	if _, ok, _ := s.Get(ctx, driftservice.SnapshotTarget); ok {
		t.Error("target row still present after promote; want removed")
	}

	// A second promote with no target row is a no-op (the live row is left
	// untouched), the defensive double-promote the swap must tolerate.
	if err := s.PromoteTargetToLive(ctx, "upgrade-42"); err != nil {
		t.Fatalf("second PromoteTargetToLive (no target): %v", err)
	}
	stillLive, ok, _ := s.Get(ctx, driftservice.SnapshotLive)
	if !ok || stillLive.UpgradeID != "upgrade-42" {
		t.Errorf("no-op promote mutated the live row: %+v", stillLive)
	}

	// The CHECK constraint rejects a third desired-state row (F-25.10.13).
	bad := live
	bad.ID = "bogus"
	if err := s.Put(ctx, bad); err == nil {
		t.Error("Put with id=bogus succeeded; the id CHECK should reject it")
	}
}

// TestPutRejectsUnmarshalableDesiredState covers the §25.10 Put
// JSON-marshal error branch without a database: Put marshals the desired
// state before it touches the pool, so a value the encoder cannot
// serialize (a channel) surfaces the marshal error and the store never
// issues a write. This pins the fail-closed behavior on a malformed
// desired-state document rather than persisting a partial or empty row.
//
// spec: §25.10 lines 3811-3820 (desired-state snapshot persistence).
func TestPutRejectsUnmarshalableDesiredState(t *testing.T) {
	// A nil pool is never reached: the marshal error returns first.
	s := pgstore.New(nil)
	snap := driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: map[string]any{"bad": make(chan int)},
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    time.Now().UTC(),
		WrittenBy:    "helm",
	}
	if err := s.Put(context.Background(), snap); err == nil {
		t.Fatal("Put with an unmarshalable desired state returned nil; want a marshal error before any write")
	}
}
