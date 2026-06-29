// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// startupHookFixture wires the real upgradeservice.Service and
// driftservice.Service the production startup hook drives, plus a fake
// ConfigMaps source holding rendered Helm values. It returns the assembled
// upgradeStartupHook config so a test drives the same code path
// runOpsRollStartupHook does.
func startupHookFixture(t *testing.T, version, valuesCM string, cmData map[string]string) (upgradeStartupHook, *upgradeservice.Service, *driftservice.Service) {
	t.Helper()
	driftSvc := driftservice.NewService(driftservice.NewMemSnapshotStore(),
		fixedRunningStartup{state: map[string]any{"pools": map[string]any{}}})
	// Wire the drift service as the DriftManager exactly as
	// buildUpgradeService does in production, so the Verification->Complete
	// proceed promotes the target snapshot into live.
	upgradeSvc := upgradeservice.New(upgradeservice.Options{
		Store:        upgradeservice.NewMemoryStore(),
		DriftManager: driftSvc,
		NewID:        func() string { return "upgrade-startup" },
	})

	var objs []runtime.Object
	if valuesCM != "" {
		objs = append(objs, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: valuesCM, Namespace: "lenny-system"},
			Data:       cmData,
		})
	}
	cs := fake.NewSimpleClientset(objs...)
	cfg := upgradeStartupHook{
		Upgrades:   upgradeSvc,
		Snapshot:   driftSvc,
		ConfigMaps: cs.CoreV1(),
		Namespace:  "lenny-system",
		Version:    version,
		ValuesCM:   valuesCM,
		ValuesKey:  "values.yaml",
		WrittenBy:  "lenny-ops",
	}
	return cfg, upgradeSvc, driftSvc
}

// driveToOpsRoll starts an upgrade and advances it to OpsRoll, the state
// the new pod observes on startup.
func driveToOpsRoll(t *testing.T, svc *upgradeservice.Service, version string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: version}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Proceed(ctx); err != nil { // Preflight -> OpsRoll
		t.Fatalf("Proceed to OpsRoll: %v", err)
	}
}

// spec: 25.8 line 3508 (self-advance OpsRoll -> CRDUpdate), 25.8 line 3511
// (ops_healthy heartbeat), 25.10 line 3788 (write
// bootstrap_seed_snapshot_target on new-pod startup)
//
// diagnosis: a failure means the production §25.8 OpsRoll startup hook
// does not drive the heartbeat, the target-snapshot write, and the
// OpsRoll->CRDUpdate self-advance against the real upgrade and drift
// services, so GET /v1/admin/drift?against=target stays 404 through every
// upgrade and the new pod never advances itself (F-DR-3).
//
// TestOpsRollStartupHookAdvancesAndWritesTarget_spec_25_8_3508 drives an
// upgrade to OpsRoll, runs the production startup hook, and asserts it
// stamped the heartbeat, wrote the target snapshot (so against=target
// resolves), and self-advanced to CRDUpdate. Pre-fix no startup path
// existed, so the target row was never written and the upgrade never
// advanced itself.
func TestOpsRollStartupHookAdvancesAndWritesTarget_spec_25_8_3508(t *testing.T) {
	ctx := context.Background()
	cfg, upgradeSvc, driftSvc := startupHookFixture(t, "1.5.0", "lenny-rendered-values",
		map[string]string{"values.yaml": "pools:\n  p:\n    minWarm: 5\n"})
	driveToOpsRoll(t, upgradeSvc, "1.5.0")

	// Pre-hook: against=target has no target row.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Fatalf("pre-hook against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}

	runOpsRollStartupHook(ctx, cfg)

	st, ok, err := upgradeSvc.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("Status = (%v, %v)", ok, err)
	}
	if st.Phase != upgrade.CRDUpdate {
		t.Errorf("phase = %s after hook, want CRDUpdate (self-advance)", st.Phase)
	}
	if st.OpsHeartbeat.IsZero() {
		t.Error("hook did not stamp the ops_healthy heartbeat")
	}
	// against=target now resolves because the hook wrote the target row.
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); err != nil {
		t.Errorf("post-hook against=target Report errored: %v", err)
	}

	// Completing the upgrade promotes the target into live: the
	// Verification->Complete proceed runs PromoteTargetToLive.
	for st.Phase != upgrade.Complete {
		next, perr := upgradeSvc.Proceed(ctx)
		if perr != nil {
			t.Fatalf("proceed to complete: %v", perr)
		}
		st = next
	}
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("after completion against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT (target promoted into live)", driftservice.CodeOf(err))
	}
}

// TestOpsRollStartupHookSkipsOnVersionMismatch_spec_25_8_3508 asserts the
// version gate: when the persisted target_version does not match this
// binary's version (an old binary rolled into OpsRoll), the hook does not
// self-advance, so the watchdog governs the timeout instead.
func TestOpsRollStartupHookSkipsOnVersionMismatch_spec_25_8_3508(t *testing.T) {
	ctx := context.Background()
	cfg, upgradeSvc, _ := startupHookFixture(t, "1.4.0", "", nil) // binary is 1.4.0
	driveToOpsRoll(t, upgradeSvc, "1.5.0")                        // upgrade targets 1.5.0

	runOpsRollStartupHook(ctx, cfg)

	st, _, _ := upgradeSvc.Status(ctx)
	if st.Phase != upgrade.OpsRoll {
		t.Errorf("phase = %s, want unchanged OpsRoll (version mismatch must not self-advance)", st.Phase)
	}
	if !st.OpsHeartbeat.IsZero() {
		t.Error("hook stamped a heartbeat on a version mismatch; it must not run at all")
	}
}

// TestOpsRollStartupHookNoOpOutsideOpsRoll_spec_25_8_3508 asserts an
// ordinary start (no upgrade, or not in OpsRoll) invokes the hook
// harmlessly: a fresh upgrade at Preflight is not advanced.
func TestOpsRollStartupHookNoOpOutsideOpsRoll_spec_25_8_3508(t *testing.T) {
	ctx := context.Background()
	cfg, upgradeSvc, _ := startupHookFixture(t, "1.5.0", "", nil)
	if _, err := upgradeSvc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runOpsRollStartupHook(ctx, cfg)
	st, _, _ := upgradeSvc.Status(ctx)
	if st.Phase != upgrade.Preflight {
		t.Errorf("phase = %s, want unchanged Preflight (hook is a no-op outside OpsRoll)", st.Phase)
	}
}

// fixedRunningStartup is a RunningStateReader returning a fixed state for
// the startup-hook fixtures.
type fixedRunningStartup struct{ state map[string]any }

func (f fixedRunningStartup) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, nil
}

// TestOpsRollStartupHookAdvancesWithoutValuesSource_spec_25_10_3788 asserts
// the §25.10 line 3788 skip path: with no rendered-values ConfigMap
// configured, the hook still self-advances OpsRoll->CRDUpdate but writes no
// target snapshot, so against=target reports DRIFT_NO_TARGET_SNAPSHOT.
func TestOpsRollStartupHookAdvancesWithoutValuesSource_spec_25_10_3788(t *testing.T) {
	ctx := context.Background()
	cfg, upgradeSvc, driftSvc := startupHookFixture(t, "1.5.0", "", nil) // no values ConfigMap
	driveToOpsRoll(t, upgradeSvc, "1.5.0")

	runOpsRollStartupHook(ctx, cfg)

	st, _, _ := upgradeSvc.Status(ctx)
	if st.Phase != upgrade.CRDUpdate {
		t.Errorf("phase = %s, want CRDUpdate (self-advance even with no values source)", st.Phase)
	}
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT (no values source, no target write)", driftservice.CodeOf(err))
	}
}

// TestOpsRollStartupHookMissingConfigMapSkipsWrite_spec_25_10_3788 asserts
// that a configured-but-absent rendered-values ConfigMap leaves the target
// write skipped (RenderedValues returns ok=false) rather than failing the
// upgrade, so the hook still self-advances.
func TestOpsRollStartupHookMissingConfigMapSkipsWrite_spec_25_10_3788(t *testing.T) {
	ctx := context.Background()
	// Name a ConfigMap that the fixture does not create.
	cfg, upgradeSvc, driftSvc := startupHookFixture(t, "1.5.0", "absent-cm", nil)
	driveToOpsRoll(t, upgradeSvc, "1.5.0")

	runOpsRollStartupHook(ctx, cfg)

	st, _, _ := upgradeSvc.Status(ctx)
	if st.Phase != upgrade.CRDUpdate {
		t.Errorf("phase = %s, want CRDUpdate (missing ConfigMap must not block self-advance)", st.Phase)
	}
	if _, err := driftSvc.Report(ctx, driftservice.ReportParams{Against: driftservice.SnapshotTarget}); driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("against=target code = %q, want DRIFT_NO_TARGET_SNAPSHOT (absent ConfigMap, no write)", driftservice.CodeOf(err))
	}
}

// TestConfigMapsGetterNilClientset asserts configMapsGetter returns an
// untyped nil for a nil clientset, avoiding the typed-nil interface trap.
func TestConfigMapsGetterNilClientset(t *testing.T) {
	if g := configMapsGetter(nil); g != nil {
		t.Errorf("configMapsGetter(nil) = %v, want nil", g)
	}
}

// TestConfigMapValuesReaderMissingKey asserts RenderedValues returns
// ok=false when the configured key is absent from the ConfigMap, so the
// hook skips the write rather than writing an empty target row.
func TestConfigMapValuesReaderMissingKey(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "vals", Namespace: "lenny-system"},
		Data:       map[string]string{"other.yaml": "x: 1\n"},
	})
	r := configMapValuesReader{cms: cs.CoreV1(), namespace: "lenny-system", name: "vals", key: "values.yaml"}
	if _, ok, err := r.RenderedValues(ctx); err != nil || ok {
		t.Fatalf("RenderedValues(missing key) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}
