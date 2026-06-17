// SPDX-License-Identifier: MIT

package driftservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// recordingApplier records the resources it applied and optionally fails
// the resources named in failKeys (by "type:id" / "type" label).
type recordingApplier struct {
	applied  []string
	failKeys map[string]bool
}

func (a *recordingApplier) Apply(_ context.Context, rtype, rid string, _ map[string]any) error {
	key := rtype
	if rid != "" {
		key = rtype + ":" + rid
	}
	a.applied = append(a.applied, key)
	if a.failKeys[key] {
		return errors.New("apply failed for " + key)
	}
	return nil
}

// recordingAudit captures emitted §25.10 audit events.
type recordingAudit struct{ events []driftservice.AuditEvent }

func (r *recordingAudit) Emit(ev driftservice.AuditEvent) { r.events = append(r.events, ev) }

func (r *recordingAudit) types() []string {
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

// recordingMetrics captures §25.10 metric increments.
type recordingMetrics struct {
	detected   [][2]string
	reconciled [][2]string
}

func (m *recordingMetrics) DriftDetected(rt, sev string) {
	m.detected = append(m.detected, [2]string{rt, sev})
}

func (m *recordingMetrics) Reconciled(rt, outcome string) {
	m.reconciled = append(m.reconciled, [2]string{rt, outcome})
}

// recordingProgress captures §25.10 operation_progressed emissions.
type recordingProgress struct{ infos []driftservice.ProgressInfo }

func (p *recordingProgress) Progressed(_ context.Context, info driftservice.ProgressInfo) {
	p.infos = append(p.infos, info)
}

// driftedService builds a service whose live snapshot drifts from the
// running state on two pools, so reconcile has two resources.
func driftedService(t *testing.T) (*driftservice.Service, *driftservice.MemSnapshotStore) {
	t.Helper()
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{
		"pools": map[string]any{
			"chat":   map[string]any{"minWarm": float64(5)},
			"coding": map[string]any{"image": "coding:1"},
		},
	}, time.Now().UTC())
	running := fixedRunning{state: map[string]any{
		"pools": map[string]any{
			"chat":   map[string]any{"minWarm": float64(9)},
			"coding": map[string]any{"image": "coding:2"},
		},
	}}
	return driftservice.NewService(store, running), store
}

// spec: §25.10 line 3765, 3842 — a confirmed reconcile applies each
// drifted resource and emits the started/per-resource/completed audit
// events plus operation_progressed per resource.
func TestReconcileConfirmAppliesAllResources_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	applier := &recordingApplier{}
	audit := &recordingAudit{}
	metrics := &recordingMetrics{}
	progress := &recordingProgress{}
	svc.SetApplier(applier)
	svc.SetAuditSink(audit)
	svc.SetMetrics(metrics)
	svc.SetProgressEmitter(progress)

	res, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "all", Confirm: true, StartedBy: "alice"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.DryRun {
		t.Error("confirmed reconcile reported dryRun=true")
	}
	if res.Reconciled != 2 || res.Failed != 0 {
		t.Fatalf("reconciled=%d failed=%d, want 2/0", res.Reconciled, res.Failed)
	}
	if res.ErrorCode != "" {
		t.Errorf("errorCode = %q, want empty on clean reconcile", res.ErrorCode)
	}
	if len(applier.applied) != 2 {
		t.Fatalf("applied %d resources, want 2", len(applier.applied))
	}
	// reconciliation_started, two resource_reconciled, reconciliation_completed.
	wantAudit := []string{
		driftservice.EventReconciliationStarted,
		driftservice.EventResourceReconciled,
		driftservice.EventResourceReconciled,
		driftservice.EventReconciliationComplete,
	}
	got := audit.types()
	if len(got) != len(wantAudit) {
		t.Fatalf("audit events = %v, want %v", got, wantAudit)
	}
	for i := range wantAudit {
		if got[i] != wantAudit[i] {
			t.Errorf("audit[%d] = %q, want %q", i, got[i], wantAudit[i])
		}
	}
	if len(progress.infos) != 2 {
		t.Fatalf("operation_progressed emitted %d times, want 2", len(progress.infos))
	}
	// Progress advances 1 then 2 over total 2.
	if progress.infos[0].CompletedSteps != 1 || progress.infos[1].CompletedSteps != 2 {
		t.Errorf("completedSteps = %d,%d want 1,2", progress.infos[0].CompletedSteps, progress.infos[1].CompletedSteps)
	}
	if progress.infos[1].TotalSteps != 2 {
		t.Errorf("totalSteps = %d, want 2", progress.infos[1].TotalSteps)
	}
	if len(metrics.reconciled) != 2 {
		t.Errorf("reconciled metric incremented %d times, want 2", len(metrics.reconciled))
	}
}

// spec: §25.10 line 3852, 3865 — a reconcile where some resources fail
// returns the partial result with errorCode DRIFT_RECONCILE_PARTIAL.
func TestReconcilePartialFailure_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	applier := &recordingApplier{failKeys: map[string]bool{"pools:coding": true}}
	svc.SetApplier(applier)

	res, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "all", Confirm: true})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 1 || res.Failed != 1 {
		t.Fatalf("reconciled=%d failed=%d, want 1/1", res.Reconciled, res.Failed)
	}
	if res.ErrorCode != driftservice.ErrCodeReconcilePartial {
		t.Errorf("errorCode = %q, want DRIFT_RECONCILE_PARTIAL", res.ErrorCode)
	}
	var failed driftservice.ReconcileResource
	for _, r := range res.Resources {
		if r.Outcome == "failed" {
			failed = r
		}
	}
	if failed.ResourceID != "coding" || failed.Error == "" {
		t.Errorf("failed resource = %+v, want coding with error", failed)
	}
}

// spec: §25.2 / §25.10 line 3842 — without confirm the reconcile returns
// a preview without applying anything.
func TestReconcileDryRun_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	applier := &recordingApplier{}
	audit := &recordingAudit{}
	svc.SetApplier(applier)
	svc.SetAuditSink(audit)

	res, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "all"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !res.DryRun {
		t.Error("dryRun=false on an unconfirmed reconcile")
	}
	if res.TotalResources != 2 {
		t.Errorf("totalResources = %d, want 2", res.TotalResources)
	}
	if len(applier.applied) != 0 {
		t.Errorf("dry-run applied %d resources, want 0", len(applier.applied))
	}
	if len(audit.events) != 0 {
		t.Errorf("dry-run emitted %d audit events, want 0", len(audit.events))
	}
	for _, r := range res.Resources {
		if r.Outcome != "preview" {
			t.Errorf("dry-run resource outcome = %q, want preview", r.Outcome)
		}
	}
}

// spec: §25.10 line 3765 — scope=resources reconciles only the named
// resources.
func TestReconcileResourceScope_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	applier := &recordingApplier{}
	svc.SetApplier(applier)

	res, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{
		Scope:     "resources",
		Resources: []string{"pools:chat"},
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.TotalResources != 1 || res.Reconciled != 1 {
		t.Fatalf("total=%d reconciled=%d, want 1/1", res.TotalResources, res.Reconciled)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "pools:chat" {
		t.Errorf("applied = %v, want [pools:chat]", applier.applied)
	}
}

// spec: §25.10 line 3842 — a confirmed reconcile with no applier wired
// fails closed with DRIFT_RECONCILE_UNAVAILABLE.
func TestReconcileConfirmNoApplier_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	_, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "all", Confirm: true})
	if driftservice.CodeOf(err) != driftservice.ErrCodeReconcileUnavailable {
		t.Errorf("err code = %q, want DRIFT_RECONCILE_UNAVAILABLE", driftservice.CodeOf(err))
	}
}

// spec: §25.10 line 3852 — reconcile honours a caller-supplied desired
// body (the Postgres-outage path) without a stored snapshot.
func TestReconcileCallerSuppliedDesired_spec_25_10(t *testing.T) {
	running := fixedRunning{state: map[string]any{
		"pools": map[string]any{"chat": map[string]any{"minWarm": float64(9)}},
	}}
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), running)
	applier := &recordingApplier{}
	svc.SetApplier(applier)

	res, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{
		Scope:   "all",
		Confirm: true,
		Desired: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(5)}}},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 1 {
		t.Errorf("reconciled = %d, want 1", res.Reconciled)
	}
}

// spec: §25.10 line 3844 — an in-flight reconciliation surfaces in the
// Operations Inventory with kind drift_reconciliation and the canonical
// progress envelope; it drops out when complete.
func TestReconcileSourceSurfacesInFlight_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	src := svc.ReconcileSource()

	// A blocking applier lets us observe the operation while in flight.
	release := make(chan struct{})
	observed := make(chan []operations.Operation, 1)
	svc.SetApplier(applierFunc(func(context.Context, string, string, map[string]any) error {
		ops, _ := src.List(context.Background(), operations.Filter{})
		observed <- ops
		<-release
		return nil
	}))

	done := make(chan struct{})
	go func() {
		_, _ = svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "all", Confirm: true, StartedBy: "bob"})
		close(done)
	}()

	inflight := <-observed
	close(release)
	<-done

	if len(inflight) != 1 {
		t.Fatalf("in-flight operations = %d, want 1", len(inflight))
	}
	op := inflight[0]
	if op.Kind != operations.KindDriftReconciliation {
		t.Errorf("kind = %q, want drift_reconciliation", op.Kind)
	}
	if op.Status != operations.StatusInProgress {
		t.Errorf("status = %q, want in_progress", op.Status)
	}
	if op.Progress == nil || op.Progress.TotalSteps == nil || *op.Progress.TotalSteps != 2 {
		t.Errorf("progress totalSteps not 2: %+v", op.Progress)
	}
	if op.Progress.EtaMethod != "linear_extrapolation" {
		t.Errorf("etaMethod = %q, want linear_extrapolation", op.Progress.EtaMethod)
	}
	if op.StartedBy != "bob" {
		t.Errorf("startedBy = %q, want bob", op.StartedBy)
	}
	// After completion the inventory shows no in-flight reconciliation.
	after, _ := src.List(context.Background(), operations.Filter{})
	if len(after) != 0 {
		t.Errorf("post-completion in-flight = %d, want 0", len(after))
	}
}

// applierFunc adapts a func to the ResourceApplier interface.
type applierFunc func(context.Context, string, string, map[string]any) error

func (f applierFunc) Apply(ctx context.Context, rtype, rid string, desired map[string]any) error {
	return f(ctx, rtype, rid, desired)
}

// spec: §25.10 line 3791 — against=both returns the live and target
// diffs in one response.
func TestReportBoth_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	now := time.Now().UTC()
	seedLive(t, store, map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(5)}}}, now)
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID:           driftservice.SnapshotTarget,
		DesiredState: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(9)}}},
		Source:       driftservice.SourceHelmValues, WrittenAt: now, WrittenBy: "upgrade",
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	running := fixedRunning{state: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(9)}}}}
	svc := driftservice.NewService(store, running)

	both, err := svc.ReportBoth(context.Background(), driftservice.ReportParams{Scope: "pools"})
	if err != nil {
		t.Fatalf("reportBoth: %v", err)
	}
	if both.Against != "both" {
		t.Errorf("against = %q, want both", both.Against)
	}
	// Live snapshot (minWarm 5) differs from running (9): one drift.
	if both.Live.DriftCount != 1 {
		t.Errorf("live driftCount = %d, want 1", both.Live.DriftCount)
	}
	// Target snapshot (minWarm 9) matches running (9): no drift.
	if both.Target.DriftCount != 0 {
		t.Errorf("target driftCount = %d, want 0", both.Target.DriftCount)
	}
}

// spec: §25.10 line 3791 — against=both with no target snapshot fails
// DRIFT_NO_TARGET_SNAPSHOT (no upgrade in flight).
func TestReportBothNoTarget_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{"pools": map[string]any{}}, time.Now().UTC())
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	_, err := svc.ReportBoth(context.Background(), driftservice.ReportParams{})
	if driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("err code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}
}

// spec: §25.10 line 3791 — against=both rejects a caller-supplied
// desired body (both mode is defined only over the stored snapshots).
func TestReportBothRejectsCallerDesired_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	_, err := svc.ReportBoth(context.Background(), driftservice.ReportParams{
		Desired: map[string]any{"pools": map[string]any{}},
	})
	if driftservice.CodeOf(err) != driftservice.ErrCodeInvalid {
		t.Errorf("err code = %q, want DRIFT_INVALID", driftservice.CodeOf(err))
	}
}

// spec: §25.10 line 3858 — Report increments lenny_drift_detected_total
// per drifted field and emits drift.report_generated.
func TestReportEmitsMetricsAndAudit_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{
		"pools":    map[string]any{"chat": map[string]any{"image": "chat:1"}},
		"runtimes": map[string]any{"py": map[string]any{"minWarm": float64(2)}},
	}, time.Now().UTC())
	running := fixedRunning{state: map[string]any{
		"pools":    map[string]any{"chat": map[string]any{"image": "chat:2"}},
		"runtimes": map[string]any{"py": map[string]any{"minWarm": float64(4)}},
	}}
	svc := driftservice.NewService(store, running)
	metrics := &recordingMetrics{}
	audit := &recordingAudit{}
	svc.SetMetrics(metrics)
	svc.SetAuditSink(audit)

	if _, err := svc.Report(context.Background(), driftservice.ReportParams{}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(metrics.detected) != 2 {
		t.Fatalf("drift_detected incremented %d times, want 2", len(metrics.detected))
	}
	// resource_type is the top path segment; image drift is high, minWarm medium.
	gotTypes := map[string]string{}
	for _, d := range metrics.detected {
		gotTypes[d[0]] = d[1]
	}
	if gotTypes["pools"] != "high" {
		t.Errorf("pools severity = %q, want high", gotTypes["pools"])
	}
	if gotTypes["runtimes"] != "medium" {
		t.Errorf("runtimes severity = %q, want medium", gotTypes["runtimes"])
	}
	if len(audit.events) != 1 || audit.events[0].Type != driftservice.EventReportGenerated {
		t.Errorf("audit = %v, want one drift.report_generated", audit.types())
	}
}

// spec: §25.10 line 3871 — snapshot refresh emits drift.snapshot_refreshed
// carrying the previous provenance and the byteSize.
func TestRefreshEmitsSnapshotRefreshedAudit_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{"pools": map[string]any{}}, time.Now().Add(-time.Hour).UTC())
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	audit := &recordingAudit{}
	svc.SetAuditSink(audit)

	if _, err := svc.RefreshSnapshot(context.Background(), driftservice.RefreshRequest{
		Desired: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": float64(5)}}}, Confirm: true, WrittenBy: "carol",
	}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	ev := audit.events[0]
	if ev.Type != driftservice.EventSnapshotRefreshed {
		t.Fatalf("event type = %q, want drift.snapshot_refreshed", ev.Type)
	}
	if ev.Details["byteSize"].(int) <= 0 {
		t.Errorf("byteSize = %v, want > 0", ev.Details["byteSize"])
	}
	if _, ok := ev.Details["previous_written_at"]; !ok {
		t.Error("snapshot_refreshed missing previous_written_at")
	}
	if ev.Details["new_source"] != driftservice.SourceSnapshotRefresh {
		t.Errorf("new_source = %v, want snapshot-refresh", ev.Details["new_source"])
	}
}

// spec: §25.10 line 3765 — reconcile rejects an unknown scope.
func TestReconcileInvalidScope_spec_25_10(t *testing.T) {
	svc, _ := driftedService(t)
	_, err := svc.Reconcile(context.Background(), driftservice.ReconcileParams{Scope: "bogus", Confirm: true})
	if driftservice.CodeOf(err) != driftservice.ErrCodeInvalid {
		t.Errorf("err code = %q, want DRIFT_INVALID", driftservice.CodeOf(err))
	}
}
