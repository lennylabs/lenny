//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the rollback half of the §25.8/§25.10
// cross-service target-snapshot lifecycle. It wires the real
// upgradeservice.Service and driftservice.Service over the same durable
// Postgres pool (the upgradeservice pgstore and the driftservice
// pgstore), writes a target snapshot as the new lenny-ops does early in
// OpsRoll, then rolls the upgrade back through the real upgrade service
// and asserts the real drift service deleted the durable
// bootstrap_seed_snapshot_target row so GET /v1/admin/drift?against=target
// resolves to DRIFT_NO_TARGET_SNAPSHOT. It is the rollback-side
// counterpart to the promote-side durable coverage: both sides of the
// boundary run against real Postgres-backed stores rather than fakes, so
// the DeleteTargetSnapshot call the upgrade orchestrator makes on
// rollback is exercised against a real drift store and a real row.
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

// fixedRunningState is a §25.10 running-state reader returning a static
// map. The drift report's running side is irrelevant to the
// target-snapshot lifecycle under test; the reader only needs to return
// without error so Report reaches the snapshot lookup that surfaces
// DRIFT_NO_TARGET_SNAPSHOT.
type fixedRunningState struct{ state map[string]any }

func (f fixedRunningState) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, nil
}

// spec: §25.8 lines 3555-3558 ("Drift snapshot cleanup on rollback. When
// rollback completes (state transitions to RolledBack), lenny-ops deletes
// the bootstrap_seed_snapshot_target row for this upgrade ... Rollback
// during OpsRoll or later phases deletes the target snapshot row. After
// this point, GET /v1/admin/drift?against=target returns 404
// DRIFT_NO_TARGET_SNAPSHOT until a new upgrade starts."); §25.10 line 3792
// (new lenny-ops writes bootstrap_seed_snapshot_target early in OpsRoll).
//
// diagnosis: a failure means the rollback half of the target-snapshot
// lifecycle does not work across the real upgradeservice/driftservice
// boundary over durable Postgres. Either the upgrade orchestrator did not
// invoke the drift service's DeleteTargetSnapshot when the upgrade
// transitioned to RolledBack, the drift service did not delete the
// durable bootstrap_seed_snapshot_target row keyed by upgrade id, or the
// live snapshot row was clobbered by the delete. Any of these leaves
// GET /v1/admin/drift?against=target comparing against an aborted
// upgrade's desired state instead of returning DRIFT_NO_TARGET_SNAPSHOT.
//
// TestUpgradeRollbackDeletesTargetSnapshotDurable wires the real drift
// and upgrade pgstores through the real services over one Postgres pool,
// drives an upgrade to OpsRoll, writes the target snapshot the new
// lenny-ops would write, rolls the upgrade back through the real upgrade
// service, and asserts the real drift service deleted the durable target
// row (against=target resolves to DRIFT_NO_TARGET_SNAPSHOT and the row is
// gone) while leaving the live snapshot row untouched.
func TestUpgradeRollbackDeletesTargetSnapshotDurable(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	pool := pg.Pool

	// Seed the live snapshot so the drift service has a pre-upgrade
	// baseline, mirroring a deployment whose bootstrap seed already ran.
	// Rollback must leave this row in place; it deletes only target.
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
	driftSvc := driftservice.NewService(driftStore, fixedRunningState{
		state: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(12)}}},
	})

	// Wire the drift service as the upgrade orchestrator's DriftManager
	// exactly as production does, so the rollback path calls the real
	// DeleteTargetSnapshot over the durable pool.
	upgradeSvc := upgradeservice.New(upgradeservice.Options{
		Store:        upgradepgstore.New(pool),
		DriftManager: driftSvc,
		NewID:        func() string { return "upgrade-durable-rollback" },
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
	// (§25.10). This is the same drift-service write site the startup
	// hook drives; the upgrade id ties the row to this upgrade so the
	// rollback deletes precisely it.
	if err := driftSvc.WriteTargetSnapshot(ctx, st.OperationID, "lenny-ops",
		map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(8)}}}); err != nil {
		t.Fatalf("WriteTargetSnapshot: %v", err)
	}

	// Precondition: against=target resolves through the durable drift
	// store now that a target row exists.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); err != nil {
		t.Fatalf("pre-rollback against=target Report errored: %v", err)
	}

	// Roll the upgrade back through the real upgrade service. OpsRoll is a
	// rollbackable phase, so this transitions to RolledBack and the
	// orchestrator invokes the drift service's DeleteTargetSnapshot for
	// this upgrade id across the durable pool.
	rb, err := upgradeSvc.Rollback(ctx, "operator abort during OpsRoll")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rb.Phase != upgrade.RolledBack {
		t.Fatalf("phase after Rollback = %s, want RolledBack", rb.Phase)
	}

	// Post-rollback: against=target resolves to DRIFT_NO_TARGET_SNAPSHOT
	// because the durable target row was deleted.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("post-rollback against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}

	// The durable target row itself is gone.
	if _, present, err := driftStore.Get(ctx, driftservice.SnapshotTarget); err != nil {
		t.Fatalf("target row Get after rollback: %v", err)
	} else if present {
		t.Error("durable target row still present after rollback; want deleted")
	}

	// The live snapshot row is untouched: rollback deletes only the target
	// row, so against=live still resolves.
	if _, present, err := driftStore.Get(ctx, driftservice.SnapshotLive); err != nil || !present {
		t.Errorf("live row Get after rollback = (%v, %v), want present and no error", present, err)
	}
}
