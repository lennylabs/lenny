// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

// spec: 25.10 line 3788 (write bootstrap_seed_snapshot_target from the
// rendered Helm values)
//
// TestConfigMapValuesReaderParseError asserts RenderedValues surfaces a
// YAML parse error rather than silently treating an unparseable values
// document as an empty map. A malformed rendered-values document is a
// real configuration fault the new pod must report, not skip: writing a
// nil target snapshot from a values document that failed to parse would
// mask the misconfiguration behind a DRIFT_NO_TARGET_SNAPSHOT 404.
func TestConfigMapValuesReaderParseError(t *testing.T) {
	ctx := context.Background()
	// A YAML sequence cannot unmarshal into map[string]any, so yaml.Unmarshal
	// returns a typed error the reader must propagate.
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "vals", Namespace: "lenny-system"},
		Data:       map[string]string{"values.yaml": "- not\n- a\n- map\n"},
	})
	r := configMapValuesReader{cms: cs.CoreV1(), namespace: "lenny-system", name: "vals", key: "values.yaml"}
	if _, ok, err := r.RenderedValues(ctx); err == nil || ok {
		t.Fatalf("RenderedValues(non-map YAML) = (ok=%v, err=%v), want (false, parse error)", ok, err)
	}
}

// spec: 25.10 line 3788 (read rendered Helm values from the ConfigMap)
//
// TestConfigMapValuesReaderGetError asserts RenderedValues surfaces a
// non-NotFound ConfigMap Get failure (a transient apiserver error) rather
// than collapsing it into the ok=false skip path. A NotFound is the
// designed skip; any other Get error is a real fault the hook must report
// so a transient outage does not silently leave the target snapshot
// unwritten under the guise of an absent ConfigMap.
func TestConfigMapValuesReaderGetError(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})
	r := configMapValuesReader{cms: cs.CoreV1(), namespace: "lenny-system", name: "vals", key: "values.yaml"}
	if _, ok, err := r.RenderedValues(ctx); err == nil || ok {
		t.Fatalf("RenderedValues(Get error) = (ok=%v, err=%v), want (false, get error)", ok, err)
	}
}

// stubHeartbeater is an opsRollHeartbeater whose three methods return
// preset states and errors, so a unit test can drive the run error
// branches the real upgradeservice.Service does not exercise on the
// happy path (a Status read error, a heartbeat write error, and an
// OpsRoll->CRDUpdate self-advance error).
type stubHeartbeater struct {
	status       upgradeservice.State
	statusOK     bool
	statusErr    error
	heartbeatErr error
	advanceErr   error

	heartbeatCalled bool
	advanceCalled   bool
}

func (s *stubHeartbeater) Status(context.Context) (upgradeservice.State, bool, error) {
	return s.status, s.statusOK, s.statusErr
}

func (s *stubHeartbeater) RecordOpsHeartbeat(context.Context) (upgradeservice.State, error) {
	s.heartbeatCalled = true
	return s.status, s.heartbeatErr
}

func (s *stubHeartbeater) AdvanceOpsRoll(context.Context) (upgradeservice.State, error) {
	s.advanceCalled = true
	return s.status, s.advanceErr
}

// inOpsRoll returns a State the hook reads as an in-flight OpsRoll whose
// target_version matches the binary, so run proceeds past both guards.
func inOpsRoll(version string) upgradeservice.State {
	return upgradeservice.State{Phase: upgrade.OpsRoll, TargetVersion: version, OperationID: "op-1"}
}

// spec: 25.8 line 3508 (read upgrade state on startup)
//
// TestOpsRollStartupHookStatusError asserts run wraps and returns a
// Status read error rather than proceeding to mutate the upgrade against
// an unread state. A transient store read failure at startup must abort
// the hook so the operator's next proceed (and the watchdog) governs the
// roll, not a half-run advance off a stale state.
func TestOpsRollStartupHookStatusError(t *testing.T) {
	ctx := context.Background()
	h := opsRollStartupHook{upgrades: &stubHeartbeater{statusErr: errors.New("store read failed")}, version: "1.5.0"}
	advanced, err := h.run(ctx)
	if err == nil || advanced {
		t.Fatalf("run(status error) = (advanced=%v, err=%v), want (false, error)", advanced, err)
	}
}

// spec: 25.8 line 3511 (stamp the ops_healthy heartbeat)
//
// TestOpsRollStartupHookHeartbeatError asserts run aborts and does not
// self-advance when the heartbeat write fails, so a self-advance never
// runs ahead of a recorded heartbeat. The advance must not be reached
// when the heartbeat could not be persisted.
func TestOpsRollStartupHookHeartbeatError(t *testing.T) {
	ctx := context.Background()
	stub := &stubHeartbeater{status: inOpsRoll("1.5.0"), statusOK: true, heartbeatErr: errors.New("heartbeat write failed")}
	h := opsRollStartupHook{upgrades: stub, version: "1.5.0"}
	advanced, err := h.run(ctx)
	if err == nil || advanced {
		t.Fatalf("run(heartbeat error) = (advanced=%v, err=%v), want (false, error)", advanced, err)
	}
	if stub.advanceCalled {
		t.Error("run self-advanced after a failed heartbeat write; the advance must not run")
	}
}

// spec: 25.8 line 3508 (self-advance OpsRoll->CRDUpdate)
//
// TestOpsRollStartupHookAdvanceError asserts run wraps and returns a
// self-advance error after the heartbeat succeeded, so a failed advance
// surfaces to the caller rather than being reported as a successful
// startup. The hook has no values source wired, so writeTarget is a no-op
// and the advance is the only mutating step that can fail here.
func TestOpsRollStartupHookAdvanceError(t *testing.T) {
	ctx := context.Background()
	stub := &stubHeartbeater{status: inOpsRoll("1.5.0"), statusOK: true, advanceErr: errors.New("advance failed")}
	h := opsRollStartupHook{upgrades: stub, version: "1.5.0"}
	advanced, err := h.run(ctx)
	if err == nil || advanced {
		t.Fatalf("run(advance error) = (advanced=%v, err=%v), want (false, error)", advanced, err)
	}
	if !stub.heartbeatCalled || !stub.advanceCalled {
		t.Errorf("run did not reach both heartbeat and advance (heartbeat=%v advance=%v)", stub.heartbeatCalled, stub.advanceCalled)
	}
}

// errValuesReader is a helmValuesReader that returns a read error, so a
// unit test can drive the writeTarget RenderedValues-error branch.
type errValuesReader struct{ err error }

func (e errValuesReader) RenderedValues(context.Context) (map[string]any, bool, error) {
	return nil, false, e.err
}

// spec: 25.10 line 3788 (write bootstrap_seed_snapshot_target from the
// rendered Helm values)
//
// TestOpsRollStartupHookWriteTargetValuesError asserts run returns the
// error from reading the rendered Helm values and does not self-advance:
// a values-read fault must abort the hook before the OpsRoll->CRDUpdate
// transition, so the upgrade is not advanced past a target write that
// could not be computed.
func TestOpsRollStartupHookWriteTargetValuesError(t *testing.T) {
	ctx := context.Background()
	stub := &stubHeartbeater{status: inOpsRoll("1.5.0"), statusOK: true}
	h := opsRollStartupHook{
		upgrades: stub,
		snapshot: recordingSnapshotWriter{},
		values:   errValuesReader{err: errors.New("values read failed")},
		version:  "1.5.0",
	}
	advanced, err := h.run(ctx)
	if err == nil || advanced {
		t.Fatalf("run(values read error) = (advanced=%v, err=%v), want (false, error)", advanced, err)
	}
	if stub.advanceCalled {
		t.Error("run self-advanced after a failed values read; the advance must not run")
	}
}

// recordingSnapshotWriter is a targetSnapshotWriter that records its
// calls, so a test can assert the write site is or is not reached.
type recordingSnapshotWriter struct{ called *bool }

func (w recordingSnapshotWriter) WriteTargetSnapshot(context.Context, string, string, map[string]any) error {
	if w.called != nil {
		*w.called = true
	}
	return nil
}
