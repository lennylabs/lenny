// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §25.8/§25.10 drift target-snapshot
// lifecycle against durable Postgres stores: the new-pod OpsRoll startup
// path (heartbeat, target-snapshot write, OpsRoll→CRDUpdate self-advance)
// and the Verification-completion target→live promotion. It pins F-DR-3:
// before the writer and promoter were wired, no target row was ever
// written, so GET /v1/admin/drift?against=target returned 404 through
// every upgrade and the in-flight target was never promoted into live.
package ops_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	driftpgstore "github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepgstore "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/upgrade"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// fixedRunning is a §25.10 RunningStateReader returning a fixed running
// state for the durable drift lifecycle test.
type fixedRunning struct{ state map[string]any }

func (f fixedRunning) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, nil
}

// applyMigration execs one numbered migration's .up.sql against the pool.
func applyMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	up, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// spec: 25.8 line 3508 (new pod self-advances OpsRoll->CRDUpdate), 25.8
// line 3511 (ops_healthy heartbeat), 25.10 line 3788 (write
// bootstrap_seed_snapshot_target on new-pod startup), 25.10 line 3789
// (promote target -> live at Verification completion)
//
// diagnosis: a failure means the durable §25.8/§25.10 target-snapshot
// lifecycle is broken end to end against real Postgres: the new pod's
// OpsRoll startup did not write the bootstrap_seed_snapshot_target row, so
// GET /v1/admin/drift?against=target stays 404 through the upgrade, or the
// Verification-completion promotion did not swap target into the live row.
// F-DR-3.
//
// TestDriftTargetSnapshotLifecycleDurable_spec_25_8_3508 wires the durable
// drift and upgrade stores through the real services, drives an upgrade to
// OpsRoll, performs the §25.8 new-pod startup sequence (heartbeat, target
// write, self-advance), asserts against=target resolves through the
// durable store, then drives the upgrade to completion and asserts the
// target row was promoted into the live row and removed.
func TestDriftTargetSnapshotLifecycleDurable_spec_25_8_3508(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{Database: "lenny"})
	pool := pg.Pool

	// Apply the two migrations the durable stores need: 0117
	// (bootstrap_seed_snapshot) and 0124 (platform_upgrade_state).
	applyMigration(t, ctx, pool, "0117_bootstrap_seed_snapshot.up.sql")
	applyMigration(t, ctx, pool, "0124_platform_upgrade.up.sql")

	// Durable drift service, seeded with a live snapshot.
	driftStore := driftpgstore.New(pool)
	if err := driftStore.Put(ctx, driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}},
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    time.Now().UTC(),
		WrittenBy:    "helm",
	}); err != nil {
		t.Fatalf("seed live snapshot: %v", err)
	}
	driftSvc := driftservice.NewService(driftStore, fixedRunning{
		state: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(12)}}},
	})

	// Durable upgrade service with the drift service wired as the
	// DriftManager (the production wiring), so the Verification-completion
	// proceed promotes the target into live.
	upgradeSvc := upgradeservice.New(upgradeservice.Options{
		Store:        upgradepgstore.New(pool),
		DriftManager: driftSvc,
		NewID:        func() string { return "upgrade-durable" },
	})

	// Drive Start -> OpsRoll.
	if _, err := upgradeSvc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := upgradeSvc.Proceed(ctx); err != nil { // Preflight -> OpsRoll
		t.Fatalf("Proceed to OpsRoll: %v", err)
	}

	// Pre-startup: against=target has no durable target row.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Fatalf("pre-startup against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}

	// §25.8 new-pod OpsRoll startup sequence: heartbeat, target write,
	// self-advance. This is the sequence cmd/lenny-ops runs at startup.
	st, _, err := upgradeSvc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := upgradeSvc.RecordOpsHeartbeat(ctx); err != nil {
		t.Fatalf("RecordOpsHeartbeat: %v", err)
	}
	if err := driftSvc.WriteTargetSnapshot(ctx, st.OperationID, "lenny-ops",
		map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(8)}}}); err != nil {
		t.Fatalf("WriteTargetSnapshot: %v", err)
	}
	advanced, err := upgradeSvc.AdvanceOpsRoll(ctx)
	if err != nil {
		t.Fatalf("AdvanceOpsRoll: %v", err)
	}
	if advanced.Phase != upgrade.CRDUpdate {
		t.Fatalf("post-startup phase = %s, want CRDUpdate", advanced.Phase)
	}

	// Re-read the durable state: the heartbeat was persisted and the phase
	// advanced.
	reread, _, _ := upgradeSvc.Status(ctx)
	if reread.OpsHeartbeat.IsZero() {
		t.Error("ops_healthy heartbeat not persisted in platform_upgrade_state")
	}

	// against=target now resolves through the durable drift store.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); err != nil {
		t.Errorf("post-startup against=target Report errored: %v", err)
	}
	target, ok, err := driftStore.Get(ctx, driftservice.SnapshotTarget)
	if err != nil || !ok {
		t.Fatalf("durable target row Get = (%v, %v), want present", ok, err)
	}
	if target.UpgradeID != "upgrade-durable" {
		t.Errorf("durable target upgradeId = %q, want upgrade-durable", target.UpgradeID)
	}

	// Drive to completion: the Verification->Complete proceed promotes the
	// target into live.
	cur := advanced
	for cur.Phase != upgrade.Complete {
		next, perr := upgradeSvc.Proceed(ctx)
		if perr != nil {
			t.Fatalf("proceed to complete (from %s): %v", cur.Phase, perr)
		}
		cur = next
	}

	// Post-completion: the target row is gone (promoted into live) and the
	// live row carries the promoted desired state.
	if _, ok, _ := driftStore.Get(ctx, driftservice.SnapshotTarget); ok {
		t.Error("durable target row still present after completion; want promoted into live and removed")
	}
	live, ok, err := driftStore.Get(ctx, driftservice.SnapshotLive)
	if err != nil || !ok {
		t.Fatalf("durable live row Get after promote = (%v, %v)", ok, err)
	}
	pools, _ := live.DesiredState["pools"].(map[string]any)
	p, _ := pools["p"].(map[string]any)
	if p == nil || p["minWarm"] != float64(8) {
		t.Errorf("post-promote live desired state = %v, want promoted target {p:{minWarm:8}}", live.DesiredState)
	}
}
