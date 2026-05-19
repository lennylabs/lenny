// SPDX-License-Identifier: MIT

package driftservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
)

// fixedRunning is a test RunningStateReader returning a fixed state.
type fixedRunning struct {
	state map[string]any
	err   error
}

func (f fixedRunning) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, f.err
}

// seedLive writes a live snapshot into the store.
func seedLive(t *testing.T, store *driftservice.MemSnapshotStore, desired map[string]any, writtenAt time.Time) {
	t.Helper()
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID: driftservice.SnapshotLive, DesiredState: desired,
		Source: driftservice.SourceHelmValues, WrittenAt: writtenAt, WrittenBy: "helm",
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
}

func TestReportDetectsDriftAgainstSnapshot(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{
		"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(5)}},
	}, time.Now().UTC())
	running := fixedRunning{state: map[string]any{
		"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(15)}},
	}}
	svc := driftservice.NewService(store, running)

	report, err := svc.Report(context.Background(), driftservice.ReportParams{Scope: "pools"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.DriftCount != 1 {
		t.Fatalf("driftCount = %d, want 1", report.DriftCount)
	}
	if report.Drift[0].Path != "pools.default-gvisor.minWarm" {
		t.Errorf("drift path = %q, want pools.default-gvisor.minWarm", report.Drift[0].Path)
	}
	if report.DesiredStateSource != "snapshot" {
		t.Errorf("desiredStateSource = %q, want snapshot", report.DesiredStateSource)
	}
}

func TestReportUsesCallerSuppliedDesiredState(t *testing.T) {
	// §25.10: a caller-supplied desired state needs no snapshot store —
	// the GitOps path that survives a Postgres outage.
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{
		state: map[string]any{"runtimes": map[string]any{"python": map[string]any{"image": "py:2"}}},
	})
	report, err := svc.Report(context.Background(), driftservice.ReportParams{
		Scope:   "runtimes",
		Desired: map[string]any{"runtimes": map[string]any{"python": map[string]any{"image": "py:1"}}},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.DesiredStateSource != "caller" || report.Against != "caller" {
		t.Errorf("desiredStateSource=%q against=%q, want caller/caller", report.DesiredStateSource, report.Against)
	}
	// An image change is high severity per §25.10.
	if report.Drift[0].Severity != "high" {
		t.Errorf("image-drift severity = %q, want high", report.Drift[0].Severity)
	}
}

func TestReportMissingSnapshotIsDesiredStateMissing(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	_, err := svc.Report(context.Background(), driftservice.ReportParams{})
	// §25.10: no snapshot and no caller body fails DRIFT_DESIRED_STATE_MISSING.
	if driftservice.CodeOf(err) != driftservice.ErrCodeDesiredStateMissing {
		t.Errorf("err code = %q, want DRIFT_DESIRED_STATE_MISSING", driftservice.CodeOf(err))
	}
}

func TestReportAgainstTargetWithoutTargetSnapshot(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{}, time.Now().UTC())
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	_, err := svc.Report(context.Background(), driftservice.ReportParams{Against: driftservice.SnapshotTarget})
	// §25.10: against=target with no upgrade in flight fails DRIFT_NO_TARGET_SNAPSHOT.
	if driftservice.CodeOf(err) != driftservice.ErrCodeNoTargetSnapshot {
		t.Errorf("err code = %q, want DRIFT_NO_TARGET_SNAPSHOT", driftservice.CodeOf(err))
	}
}

func TestReportFlagsStaleSnapshot(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	written := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedLive(t, store, map[string]any{}, written)
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	// 17 days later — past the 7-day staleness threshold.
	svc.SetClock(func() time.Time { return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC) })

	report, err := svc.Report(context.Background(), driftservice.ReportParams{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !report.SnapshotStale {
		t.Error("snapshot_stale = false for a 17-day-old snapshot, want true")
	}
	if report.SnapshotStaleWarning == "" {
		t.Error("snapshot_stale_warning is empty for a stale snapshot")
	}
	if report.SnapshotAgeSeconds == nil || *report.SnapshotAgeSeconds < 7*86400 {
		t.Errorf("snapshot_age_seconds = %v, want > 7 days", report.SnapshotAgeSeconds)
	}
}

func TestReportFreshSnapshotIsNotStale(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	written := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	seedLive(t, store, map[string]any{}, written)
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	svc.SetClock(func() time.Time { return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC) })

	report, _ := svc.Report(context.Background(), driftservice.ReportParams{})
	if report.SnapshotStale {
		t.Error("snapshot_stale = true for a 1-day-old snapshot, want false")
	}
}

func TestValidateReportsMatchAndDiverged(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	desired := map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}}
	seedLive(t, store, desired, time.Now().UTC())
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})

	// An identical desired state validates as match.
	match, err := svc.Validate(context.Background(), map[string]any{
		"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}},
	})
	if err != nil {
		t.Fatalf("validate match: %v", err)
	}
	if match.SnapshotValidationResult != "match" {
		t.Errorf("validation result = %q, want match", match.SnapshotValidationResult)
	}
	// A differing desired state validates as diverged.
	diverged, err := svc.Validate(context.Background(), map[string]any{
		"pools": map[string]any{"p": map[string]any{"minWarm": float64(9)}},
	})
	if err != nil {
		t.Fatalf("validate diverged: %v", err)
	}
	if diverged.SnapshotValidationResult != "diverged" || diverged.DifferenceCount != 1 {
		t.Errorf("validation result=%q count=%d, want diverged/1",
			diverged.SnapshotValidationResult, diverged.DifferenceCount)
	}
}

func TestRefreshSnapshotReplacesLiveRow(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedLive(t, store, map[string]any{"pools": map[string]any{}}, old)
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{}})
	svc.SetClock(func() time.Time { return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC) })

	res, err := svc.RefreshSnapshot(context.Background(), driftservice.RefreshRequest{
		Desired: map[string]any{"pools": map[string]any{"new": map[string]any{}}},
		Confirm: true, WrittenBy: "operator",
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !res.Replaced {
		t.Error("refresh did not replace the snapshot")
	}
	// §25.10: the result carries the previous provenance for the
	// drift.snapshot_refreshed audit event.
	if res.PreviousWrittenAt == nil || !res.PreviousWrittenAt.Equal(old) {
		t.Errorf("previousWrittenAt = %v, want the old snapshot timestamp", res.PreviousWrittenAt)
	}
	if res.NewSource != driftservice.SourceSnapshotRefresh {
		t.Errorf("newSource = %q, want snapshot-refresh", res.NewSource)
	}
	// The store now holds the new desired state.
	snap, ok, _ := store.Get(context.Background(), driftservice.SnapshotLive)
	if !ok || snap.DesiredState["pools"] == nil {
		t.Error("the live snapshot was not updated in the store")
	}
}

func TestRefreshSnapshotRejectsEmptyDesiredState(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	_, err := svc.RefreshSnapshot(context.Background(), driftservice.RefreshRequest{Confirm: true})
	if driftservice.CodeOf(err) != driftservice.ErrCodeInvalid {
		t.Errorf("err code = %q, want DRIFT_INVALID for an empty desired state", driftservice.CodeOf(err))
	}
}
