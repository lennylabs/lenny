// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test driving the production §25.8/§25.10 OpsRoll startup
// hook against durable Postgres-backed drift and upgrade stores. It is the
// component-tier counterpart of the in-memory unit tests in
// opsroll_startup_test.go: the same runOpsRollStartupHook production path,
// wired to the real pgstores rather than the in-memory MemSnapshotStore and
// MemoryStore, so the durable target-snapshot write site is reached only if
// the hook actually calls it. It pins F-DR-3 on the durable path: before
// the writer and promoter were wired into the startup hook, no target row
// was ever written, so GET /v1/admin/drift?against=target returned 404
// through every upgrade and the in-flight target was never promoted into
// live.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	driftpgstore "github.com/lennylabs/lenny/pkg/ops/driftservice/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepgstore "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/upgrade"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// applyDurableMigration execs one numbered migration's .up.sql against pool.
func applyDurableMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	up, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// durableStartupHookFixture wires the real upgradeservice.Service and
// driftservice.Service backed by the durable Postgres pgstores the
// production startup hook drives, plus a fake ConfigMaps source holding
// rendered Helm values. It returns the assembled upgradeStartupHook config
// so the test drives the same runOpsRollStartupHook code path production
// does, with the durable target-snapshot write reached only when the hook
// calls it. The drift service is seeded with a live snapshot so promotion
// at Verification completion has a row to swap.
func durableStartupHookFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version, valuesYAML string) (upgradeStartupHook, *upgradeservice.Service, *driftservice.Service, *driftpgstore.Store) {
	t.Helper()

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
	driftSvc := driftservice.NewService(driftStore, fixedRunningStartup{
		state: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(12)}}},
	})

	// Wire the drift service as the DriftManager exactly as
	// buildUpgradeService does in production, so the Verification->Complete
	// proceed promotes the target snapshot into live.
	upgradeSvc := upgradeservice.New(upgradeservice.Options{
		Store:        upgradepgstore.New(pool),
		DriftManager: driftSvc,
		NewID:        func() string { return "upgrade-durable" },
	})

	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-rendered-values", Namespace: "lenny-system"},
		Data:       map[string]string{"values.yaml": valuesYAML},
	})
	cfg := upgradeStartupHook{
		Upgrades:   upgradeSvc,
		Snapshot:   driftSvc,
		ConfigMaps: cs.CoreV1(),
		Namespace:  "lenny-system",
		Version:    version,
		ValuesCM:   "lenny-rendered-values",
		ValuesKey:  "values.yaml",
		WrittenBy:  "lenny-ops",
	}
	return cfg, upgradeSvc, driftSvc, driftStore
}

// spec: 25.8 line 3508 (new pod self-advances OpsRoll->CRDUpdate), 25.8
// line 3511 (ops_healthy heartbeat), 25.10 line 3788 (write
// bootstrap_seed_snapshot_target on new-pod startup), 25.10 line 3789
// (promote target -> live at Verification completion)
//
// diagnosis: a failure means the production §25.8/§25.10 OpsRoll startup
// hook does not drive the heartbeat, the durable target-snapshot write, and
// the OpsRoll->CRDUpdate self-advance against real Postgres-backed stores,
// so GET /v1/admin/drift?against=target stays 404 through every upgrade and
// the Verification-completion promotion does not swap target into the live
// row. Because the test invokes runOpsRollStartupHook rather than the
// constituent methods, it also fails if the production hook is unwired or
// its durable write site is never called. F-DR-3.
//
// TestOpsRollStartupHookDurable_spec_25_8_3508 wires the durable drift and
// upgrade pgstores through the real services, drives an upgrade to OpsRoll,
// runs the production runOpsRollStartupHook, and asserts the hook wrote the
// heartbeat, wrote the durable target snapshot (so against=target resolves
// through the durable store), and self-advanced OpsRoll->CRDUpdate, then
// drives the upgrade to completion and asserts the target row was promoted
// into the live row and removed.
func TestOpsRollStartupHookDurable_spec_25_8_3508(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{Database: "lenny"})
	pool := pg.Pool

	// Apply the two migrations the durable stores need: 0117
	// (bootstrap_seed_snapshot) and 0124 (platform_upgrade_state).
	applyDurableMigration(t, ctx, pool, "0117_bootstrap_seed_snapshot.up.sql")
	applyDurableMigration(t, ctx, pool, "0124_platform_upgrade.up.sql")

	cfg, upgradeSvc, driftSvc, driftStore := durableStartupHookFixture(t, ctx, pool, "1.6.0",
		"pools:\n  p:\n    minWarm: 8\n")

	// Drive Start -> OpsRoll on the durable upgrade store.
	if _, err := upgradeSvc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := upgradeSvc.Proceed(ctx); err != nil { // Preflight -> OpsRoll
		t.Fatalf("Proceed to OpsRoll: %v", err)
	}

	// Pre-hook: against=target has no durable target row.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Fatalf("pre-hook against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}

	// Run the production §25.8 OpsRoll startup hook. This is the exact code
	// path cmd/lenny-ops runs at startup; the durable target-snapshot write
	// is reached only if the hook calls it, so a regression that drops the
	// write, inverts the version gate, or reorders heartbeat/write/advance
	// fails here rather than passing a hand-reconstructed sequence.
	runOpsRollStartupHook(ctx, cfg)

	// The hook stamped the heartbeat and self-advanced to CRDUpdate against
	// the durable upgrade store.
	st, ok, err := upgradeSvc.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("Status = (%v, %v)", ok, err)
	}
	if st.Phase != upgrade.CRDUpdate {
		t.Errorf("phase = %s after hook, want CRDUpdate (self-advance)", st.Phase)
	}
	if st.OpsHeartbeat.IsZero() {
		t.Error("ops_healthy heartbeat not persisted in platform_upgrade_state")
	}

	// against=target now resolves through the durable drift store, and the
	// durable target row carries the upgrade id and the rendered desired
	// state the hook read from the values ConfigMap.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); err != nil {
		t.Errorf("post-hook against=target Report errored: %v", err)
	}
	target, present, err := driftStore.Get(ctx, driftservice.SnapshotTarget)
	if err != nil || !present {
		t.Fatalf("durable target row Get = (%v, %v), want present", present, err)
	}
	if target.UpgradeID != "upgrade-durable" {
		t.Errorf("durable target upgradeId = %q, want upgrade-durable", target.UpgradeID)
	}

	// Drive to completion: the Verification->Complete proceed promotes the
	// target into live.
	cur := st
	for cur.Phase != upgrade.Complete {
		next, perr := upgradeSvc.Proceed(ctx)
		if perr != nil {
			t.Fatalf("proceed to complete (from %s): %v", cur.Phase, perr)
		}
		cur = next
	}

	// Post-completion: the target row is gone (promoted into live) and the
	// live row carries the promoted desired state.
	if _, stillThere, _ := driftStore.Get(ctx, driftservice.SnapshotTarget); stillThere {
		t.Error("durable target row still present after completion; want promoted into live and removed")
	}
	live, present, err := driftStore.Get(ctx, driftservice.SnapshotLive)
	if err != nil || !present {
		t.Fatalf("durable live row Get after promote = (%v, %v)", present, err)
	}
	pools, _ := live.DesiredState["pools"].(map[string]any)
	p, _ := pools["p"].(map[string]any)
	if p == nil || p["minWarm"] != float64(8) {
		t.Errorf("post-promote live desired state = %v, want promoted target {p:{minWarm:8}}", live.DesiredState)
	}
}
