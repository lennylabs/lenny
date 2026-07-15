//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the promote half of the §25.8/§25.10
// cross-service target-snapshot lifecycle. It wires the real
// upgradeservice.Service and driftservice.Service over the same durable
// Postgres pool (the upgradeservice pgstore and the driftservice
// pgstore), writes a target snapshot as the new lenny-ops does early in
// OpsRoll, drives the real upgrade orchestrator through every working
// phase to Verification, then completes the upgrade
// (Verification→Complete) and asserts the real drift service promoted the
// durable bootstrap_seed_snapshot_target row into the live row atomically
// so GET /v1/admin/drift compares against the new desired state by
// default and GET /v1/admin/drift?against=target resolves to
// DRIFT_NO_TARGET_SNAPSHOT. It is the promote-side counterpart to
// upgrade_rollback_drift_cleanup_test.go: both sides of the boundary run
// against real Postgres-backed stores rather than fakes, so the
// PromoteTargetToLive call the upgrade orchestrator makes at Verification
// completion is exercised against a real drift store and real rows.
package tier4_integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	driftpgstore "github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepgstore "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/upgrade"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §25.10 lines 3793-3795 ("At the end of an upgrade
// (Verification phase completion). The target snapshot is promoted to the
// live snapshot atomically. From this point onward, GET /v1/admin/drift
// compares against the new desired state." and "agents can pass
// ?against=target to compare against the in-flight target snapshot, or
// ?against=both to receive both diffs in a single response"); §25.10 line
// 3792 (new lenny-ops writes bootstrap_seed_snapshot_target early in
// OpsRoll).
//
// diagnosis: a failure means the promote half of the target-snapshot
// lifecycle does not work across the real upgradeservice/driftservice
// boundary over durable Postgres. Either the upgrade orchestrator did not
// invoke the drift service's PromoteTargetToLive when the upgrade left
// Verification for Complete, the drift service's promote did not replace
// the durable live row's desired state, source, upgrade id, and
// provenance from the target row, or the swap was not atomic (the target
// row survived, or the live row was left torn). Any of these leaves
// GET /v1/admin/drift comparing against a stale pre-upgrade desired state
// after the upgrade completed, or still surfacing an in-flight target
// snapshot for a finished upgrade.
//
// TestUpgradePromotesTargetSnapshotToLiveDurable wires the real drift and
// upgrade pgstores through the real services over one Postgres pool, seeds
// a pre-upgrade live snapshot, drives the real upgrade orchestrator to
// OpsRoll, writes the target snapshot the new lenny-ops would write,
// confirms both the live and target comparisons resolve mid-upgrade (the
// two diffs against=both returns), drives the upgrade through every
// remaining working phase to Verification, completes it, and asserts the
// durable target row was promoted into the live row atomically (the live
// row now carries the target's desired state and provenance, and the
// target row is gone so against=target returns DRIFT_NO_TARGET_SNAPSHOT).
func TestUpgradePromotesTargetSnapshotToLiveDurable(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	pool := pg.Pool

	// The three states under test, keyed on one pool's minWarm so each diff
	// is unambiguous:
	//   live desired   = 5  (pre-upgrade baseline the bootstrap seed wrote)
	//   running state  = 12 (what the platform is actually running)
	//   target desired = 8  (what this upgrade will change the desired to)
	const (
		liveMinWarm    = float64(5)
		runningMinWarm = float64(12)
		targetMinWarm  = float64(8)
	)
	liveDesired := map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": liveMinWarm}}}
	targetDesired := map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": targetMinWarm}}}

	// Seed the live snapshot so the drift service has a pre-upgrade
	// baseline, mirroring a deployment whose bootstrap seed already ran.
	// Promotion must overwrite this row from the target row.
	driftStore := driftpgstore.New(pool)
	if err := driftStore.Put(ctx, driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: liveDesired,
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    time.Now().UTC().Add(-time.Hour),
		WrittenBy:    "helm",
	}); err != nil {
		t.Fatalf("seed live snapshot: %v", err)
	}
	driftSvc := driftservice.NewService(driftStore, fixedRunningState{
		state: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": runningMinWarm}}},
	})

	// Wire the drift service as the upgrade orchestrator's DriftManager
	// exactly as production does, so the completion path calls the real
	// PromoteTargetToLive over the durable pool.
	upgradeSvc := upgradeservice.New(upgradeservice.Options{
		Store:        upgradepgstore.New(pool),
		DriftManager: driftSvc,
		NewID:        func() string { return "upgrade-durable-promote" },
	})

	// Drive Start -> Preflight -> OpsRoll on the durable upgrade store.
	if _, err := upgradeSvc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, err := upgradeSvc.Proceed(ctx) // Preflight -> OpsRoll
	if err != nil {
		t.Fatalf("Proceed to OpsRoll: %v", err)
	}
	if st.Phase != upgrade.OpsRoll {
		t.Fatalf("phase after Proceed = %s, want OpsRoll", st.Phase)
	}

	// The new lenny-ops writes the target snapshot early in OpsRoll
	// (§25.10). This is the same drift-service write site the OpsRoll
	// startup hook drives; the upgrade id ties the row to this upgrade.
	if err := driftSvc.WriteTargetSnapshot(ctx, st.OperationID, "lenny-ops", targetDesired); err != nil {
		t.Fatalf("WriteTargetSnapshot: %v", err)
	}

	// Mid-upgrade, both comparisons resolve: against=live shows the
	// pre-upgrade drift (running 12 vs live 5) and against=target shows
	// what the upgrade will not fix (running 12 vs target 8). Together
	// these are the two diffs GET /v1/admin/drift?against=both returns in
	// one response; the service exposes them as two Report calls that the
	// HTTP layer composes.
	liveReport, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotLive})
	if err != nil {
		t.Fatalf("mid-upgrade against=live Report errored: %v", err)
	}
	if liveReport.DriftCount == 0 {
		t.Error("mid-upgrade against=live driftCount = 0, want the running-vs-live drift")
	}
	targetReport, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget})
	if err != nil {
		t.Fatalf("mid-upgrade against=target Report errored: %v", err)
	}
	if targetReport.DriftCount == 0 {
		t.Error("mid-upgrade against=target driftCount = 0, want the running-vs-target drift")
	}

	// Drive the real orchestrator through every remaining working phase up
	// to Verification, then record health verification, mirroring a real
	// operator walking the upgrade forward.
	for st.Phase != upgrade.Verification {
		st, err = upgradeSvc.Proceed(ctx)
		if err != nil {
			t.Fatalf("Proceed toward Verification (from prior phase): %v", err)
		}
	}
	if _, err := upgradeSvc.Verify(ctx); err != nil {
		t.Fatalf("Verify at Verification phase: %v", err)
	}

	// Complete the upgrade: the Verification->Complete proceed triggers the
	// orchestrator's PromoteTargetToLive over the durable drift store.
	done, err := upgradeSvc.Proceed(ctx)
	if err != nil {
		t.Fatalf("Proceed Verification->Complete: %v", err)
	}
	if done.Phase != upgrade.Complete {
		t.Fatalf("phase after final Proceed = %s, want Complete", done.Phase)
	}

	// The durable live row was replaced atomically from the target row: its
	// desired state, source, upgrade id, and provenance now come from the
	// target the upgrade wrote.
	live, present, err := driftStore.Get(ctx, driftservice.SnapshotLive)
	if err != nil {
		t.Fatalf("live row Get after promote: %v", err)
	}
	if !present {
		t.Fatal("live row absent after promote; want the promoted row")
	}
	gotLiveMinWarm := live.DesiredState["pools"].(map[string]any)["p"].(map[string]any)["minWarm"]
	if gotLiveMinWarm != targetMinWarm {
		t.Errorf("promoted live minWarm = %v, want %v (the target desired state)", gotLiveMinWarm, targetMinWarm)
	}
	if live.UpgradeID != st.OperationID {
		t.Errorf("promoted live upgradeId = %q, want %q", live.UpgradeID, st.OperationID)
	}
	if live.WrittenBy != "lenny-ops" {
		t.Errorf("promoted live writtenBy = %q, want %q (the target's provenance)", live.WrittenBy, "lenny-ops")
	}
	if live.Source != driftservice.SourceHelmValues {
		t.Errorf("promoted live source = %q, want %q", live.Source, driftservice.SourceHelmValues)
	}

	// The durable target row is gone: promotion is a move, so a finished
	// upgrade leaves no in-flight target and against=target resolves to
	// DRIFT_NO_TARGET_SNAPSHOT.
	if _, targetPresent, err := driftStore.Get(ctx, driftservice.SnapshotTarget); err != nil {
		t.Fatalf("target row Get after promote: %v", err)
	} else if targetPresent {
		t.Error("durable target row still present after promote; want deleted (torn or non-atomic swap)")
	}
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("post-promote against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}

	// From completion onward GET /v1/admin/drift compares against the new
	// desired state by default: running (12) now differs from the promoted
	// live desired (8), so the report still resolves and reflects the new
	// baseline rather than the stale pre-upgrade one.
	postReport, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotLive})
	if err != nil {
		t.Fatalf("post-promote against=live Report errored: %v", err)
	}
	if postReport.DriftCount == 0 {
		t.Error("post-promote against=live driftCount = 0, want running-vs-promoted-live drift")
	}
}
