// SPDX-License-Identifier: MIT

package driftservice_test

import (
	"context"
	"errors"
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
	match, err := svc.Validate(context.Background(), driftservice.ValidateParams{
		Desired: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}},
	})
	if err != nil {
		t.Fatalf("validate match: %v", err)
	}
	if match.SnapshotValidationResult != "match" {
		t.Errorf("validation result = %q, want match", match.SnapshotValidationResult)
	}
	// A differing desired state validates as diverged.
	diverged, err := svc.Validate(context.Background(), driftservice.ValidateParams{
		Desired: map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(9)}}},
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

// TestRefreshSnapshotCarriesByteSizeSpec25_10_3871 pins the §25.10 line
// 3871 drift.snapshot_refreshed audit-event detail: the RefreshResult
// carries the JSON-encoded byteSize of the new desired state so the
// audit emitter can render the event without re-marshalling. F-25.10.8.
func TestRefreshSnapshotCarriesByteSizeSpec25_10_3871(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	desired := map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(5)}}}
	res, err := svc.RefreshSnapshot(context.Background(), driftservice.RefreshRequest{
		Desired: desired, Confirm: true, WrittenBy: "operator",
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// The byteSize must match the JSON encoding of the desired state —
	// not the in-memory Go map size — because that's the Postgres JSONB
	// row size the audit event reports.
	if res.ByteSize <= 0 {
		t.Fatalf("byteSize = %d, want > 0", res.ByteSize)
	}
	// A larger desired state must produce a larger byteSize.
	bigger := map[string]any{
		"pools": map[string]any{
			"default-gvisor": map[string]any{"minWarm": float64(5), "maxSize": float64(100)},
			"second-pool":    map[string]any{"minWarm": float64(2)},
		},
	}
	res2, err := svc.RefreshSnapshot(context.Background(), driftservice.RefreshRequest{
		Desired: bigger, Confirm: true, WrittenBy: "operator",
	})
	if err != nil {
		t.Fatalf("refresh 2: %v", err)
	}
	if res2.ByteSize <= res.ByteSize {
		t.Errorf("byteSize did not grow with desired state: %d vs %d", res2.ByteSize, res.ByteSize)
	}
}

// TestReportColdStartReturns404Spec25_10_3866 pins the §25.10 line 3866
// table: DRIFT_DESIRED_STATE_MISSING resolves to 404 when no snapshot
// exists at all (cold start). The Error.HTTPStatus override carries the
// signal end-to-end so the HTTP layer can distinguish cold-start (404)
// from store-down (503). F-25.10.10.
func TestReportColdStartReturns404Spec25_10_3866(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	_, err := svc.Report(context.Background(), driftservice.ReportParams{})
	var de *driftservice.Error
	if !errors.As(err, &de) {
		t.Fatalf("err is not *driftservice.Error: %T %v", err, err)
	}
	if de.Code != driftservice.ErrCodeDesiredStateMissing {
		t.Errorf("err code = %q, want DRIFT_DESIRED_STATE_MISSING", de.Code)
	}
	if de.HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want 404 (§25.10 line 3866 cold-start)", de.HTTPStatus)
	}
}

// TestValidateColdStartReturns404Spec25_10_3866 pins the same §25.10
// line 3866 distinction for the validate path. The cold-start case sets
// HTTPStatus=404; the caller can recover by supplying a stored snapshot
// in the request body (the F-25.10.12 offline-validation path).
func TestValidateColdStartReturns404Spec25_10_3866(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	_, err := svc.Validate(context.Background(), driftservice.ValidateParams{
		Desired: map[string]any{"pools": map[string]any{}},
	})
	var de *driftservice.Error
	if !errors.As(err, &de) {
		t.Fatalf("err is not *driftservice.Error: %T %v", err, err)
	}
	if de.Code != driftservice.ErrCodeDesiredStateMissing || de.HTTPStatus != 404 {
		t.Errorf("validate cold-start = code=%q status=%d, want DRIFT_DESIRED_STATE_MISSING/404",
			de.Code, de.HTTPStatus)
	}
}

// TestReportStoreFailureMapsTo503_spec_25_10_3866 pins the §25.10 line
// 3866 "Postgres down" case: a snapshot-store error wraps as
// DRIFT_DESIRED_STATE_MISSING with the default code mapping (HTTPStatus
// unset) so the HTTP layer renders 503. F-25.10.10.
func TestReportStoreFailureMapsTo503_spec_25_10_3866(t *testing.T) {
	svc := driftservice.NewService(failingStore{}, fixedRunning{state: map[string]any{}})
	_, err := svc.Report(context.Background(), driftservice.ReportParams{})
	var de *driftservice.Error
	if !errors.As(err, &de) {
		t.Fatalf("want a *driftservice.Error, got %T %v", err, err)
	}
	if de.Code != driftservice.ErrCodeDesiredStateMissing {
		t.Errorf("code = %q, want DRIFT_DESIRED_STATE_MISSING", de.Code)
	}
	if de.HTTPStatus != 0 {
		t.Errorf("HTTPStatus = %d, want 0 (default-503 mapping)", de.HTTPStatus)
	}
}

// TestValidateAcceptsCallerSuppliedStoredSpec25_10_3782 pins the §25.10
// degradation path: when Postgres is unavailable, the caller can supply
// a stored snapshot in the request body and validate against it without
// consulting the snapshot store. F-25.10.12.
func TestValidateAcceptsCallerSuppliedStoredSpec25_10_3782(t *testing.T) {
	// Empty store — no live snapshot. With the stored field set, the
	// validate succeeds and diffs the two caller-supplied bodies.
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	stored := map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}}
	desired := map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(9)}}}
	res, err := svc.Validate(context.Background(), driftservice.ValidateParams{
		Stored: stored, Desired: desired,
	})
	if err != nil {
		t.Fatalf("validate with caller-supplied stored: %v", err)
	}
	if res.SnapshotValidationResult != "diverged" || res.DifferenceCount != 1 {
		t.Errorf("validate result=%q count=%d, want diverged/1",
			res.SnapshotValidationResult, res.DifferenceCount)
	}
}

// TestReportHonorsRunningStateCacheSpec25_10_3822 pins the §25.10 line
// 3822-3824 running-state caching: when a cache is configured the
// second Report call returns the cached state without re-reading from
// the running-state reader. F-25.10.7.
func TestReportHonorsRunningStateCacheSpec25_10_3822(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{"pools": map[string]any{}}, time.Now().UTC())
	reader := &countingRunningState{state: map[string]any{"pools": map[string]any{}}}
	svc := driftservice.NewService(store, reader)
	svc.SetRunningStateCache(driftservice.NewMemRunningStateCache(60 * time.Second))

	for i := 0; i < 3; i++ {
		if _, err := svc.Report(context.Background(), driftservice.ReportParams{Scope: "pools"}); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	if reader.calls != 1 {
		t.Errorf("running-state reader called %d times, want 1 (later calls cached)", reader.calls)
	}
}

// TestReportFreshBypassesCacheSpec25_10_3762 pins the §25.10 line 3762
// ?fresh=true contract: a Fresh=true report bypasses the cache and
// reads from the running-state reader every time. F-25.10.7.
func TestReportFreshBypassesCacheSpec25_10_3762(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	seedLive(t, store, map[string]any{"pools": map[string]any{}}, time.Now().UTC())
	reader := &countingRunningState{state: map[string]any{"pools": map[string]any{}}}
	svc := driftservice.NewService(store, reader)
	svc.SetRunningStateCache(driftservice.NewMemRunningStateCache(60 * time.Second))

	for i := 0; i < 3; i++ {
		if _, err := svc.Report(context.Background(), driftservice.ReportParams{
			Scope: "pools", Fresh: true,
		}); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	if reader.calls != 3 {
		t.Errorf("running-state reader called %d times, want 3 (Fresh=true bypasses cache)", reader.calls)
	}
}

// TestMemRunningStateCacheTTLExpiresSpec25_10_3824 pins the TTL
// expiration behavior: an entry stored at t expires at t+TTL and a
// later Lookup misses. The Mem cache uses an injectable clock so the
// expiry is exercised without real wall time.
func TestMemRunningStateCacheTTLExpiresSpec25_10_3824(t *testing.T) {
	cache := driftservice.NewMemRunningStateCache(60 * time.Second)
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cache.SetClock(func() time.Time { return now })
	if err := cache.Store(context.Background(), "all", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, ok, _ := cache.Lookup(context.Background(), "all"); !ok {
		t.Fatal("cache miss immediately after store")
	}
	// Advance past the TTL.
	now = now.Add(61 * time.Second)
	if _, ok, _ := cache.Lookup(context.Background(), "all"); ok {
		t.Fatal("cache hit after TTL expiry, want miss")
	}
}

// TestMemRunningStateCacheZeroTTLDisablesSpec25_10_3824 pins the §25.10
// line 3824 "0 disables" posture: a zero TTL causes Lookup to always
// miss and Store to be a no-op. F-25.10.7.
func TestMemRunningStateCacheZeroTTLDisablesSpec25_10_3824(t *testing.T) {
	cache := driftservice.NewMemRunningStateCache(0)
	_ = cache.Store(context.Background(), "all", map[string]any{"k": "v"})
	if _, ok, _ := cache.Lookup(context.Background(), "all"); ok {
		t.Error("zero-TTL cache Lookup hit, want miss (caching disabled)")
	}
}

// TestBuildDriftServiceWiresCache verifies the spec-default StaleWarningDays
// and cache TTL constants the binary uses. F-25.10.7, F-25.10.9.
func TestDefaultsMatchSpec25_10(t *testing.T) {
	if driftservice.DefaultStaleWarningDays != 7 {
		t.Errorf("DefaultStaleWarningDays = %d, want 7 (§25.10 line 3801)", driftservice.DefaultStaleWarningDays)
	}
	if driftservice.DefaultRunningStateCacheTTL != 60*time.Second {
		t.Errorf("DefaultRunningStateCacheTTL = %v, want 60s (§25.10 line 3824)", driftservice.DefaultRunningStateCacheTTL)
	}
}

// failingStore is a SnapshotStore that always returns an error — used to
// pin the §25.10 line 3866 "Postgres down" case.
type failingStore struct{}

func (failingStore) Get(context.Context, string) (driftservice.Snapshot, bool, error) {
	return driftservice.Snapshot{}, false, errors.New("postgres down")
}

func (failingStore) Put(context.Context, driftservice.Snapshot) error {
	return errors.New("postgres down")
}

// countingRunningState is a RunningStateReader that counts how many
// times the underlying source was read.
type countingRunningState struct {
	state map[string]any
	calls int
}

func (c *countingRunningState) RunningState(context.Context, string) (map[string]any, error) {
	c.calls++
	return c.state, nil
}
