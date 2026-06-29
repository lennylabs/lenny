// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// fixedRunning is a test RunningStateReader returning a fixed state.
type fixedRunning struct{ state map[string]any }

func (f fixedRunning) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, nil
}

// driftServer returns a Server with a drift service seeded with a live
// snapshot and a fixed running state.
func driftServer(t *testing.T, desired, running map[string]any) *opsserver.Server {
	t.Helper()
	store := driftservice.NewMemSnapshotStore()
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID: driftservice.SnapshotLive, DesiredState: desired,
		Source: driftservice.SourceHelmValues, WrittenAt: time.Now().UTC(), WrittenBy: "helm",
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	svc := driftservice.NewService(store, fixedRunning{state: running})
	return opsserver.New(opsserver.Options{Drift: svc})
}

func TestDriftReportReturnsDrift(t *testing.T) {
	srv := driftServer(t,
		map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}},
		map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(15)}}})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/drift?scope=pools", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if dc, _ := body["driftCount"].(float64); dc != 1 {
		t.Errorf("driftCount = %v, want 1", body["driftCount"])
	}
	if body["desiredStateSource"] != "snapshot" {
		t.Errorf("desiredStateSource = %v, want snapshot", body["desiredStateSource"])
	}
}

// TestDriftReportStoreDownReturns503_spec_25_10_3866 pins the §25.10
// line 3866 503 path: a SnapshotStore.Get error (Postgres down)
// surfaces as DRIFT_DESIRED_STATE_MISSING with HTTP 503, distinct from
// the cold-start 404 path. F-25.10.10.
func TestDriftReportStoreDownReturns503_spec_25_10_3866(t *testing.T) {
	svc := driftservice.NewService(failingStore{}, fixedRunning{state: map[string]any{}})
	srv := opsserver.New(opsserver.Options{Drift: svc})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/drift", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (§25.10 line 3866 store-down)", rec.Code)
	}
}

// failingStore is a SnapshotStore that always returns an error — used to
// pin the §25.10 line 3866 Postgres-down case at the HTTP layer.
type failingStore struct{}

func (failingStore) Get(context.Context, string) (driftservice.Snapshot, bool, error) {
	return driftservice.Snapshot{}, false, errStoreDown
}

func (failingStore) Put(context.Context, driftservice.Snapshot) error {
	return errStoreDown
}

func (failingStore) Delete(context.Context, string) error {
	return errStoreDown
}

func (failingStore) PromoteTargetToLive(context.Context, string) error {
	return errStoreDown
}

var errStoreDown = httpTestError("postgres down")

// httpTestError is a string-typed error so the test doesn't need the
// errors package.
type httpTestError string

func (e httpTestError) Error() string { return string(e) }

func TestDriftValidateReportsDiverged(t *testing.T) {
	srv := driftServer(t,
		map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}},
		map[string]any{})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/validate", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(9)}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["snapshotValidationResult"] != "diverged" {
		t.Errorf("snapshotValidationResult = %v, want diverged", body["snapshotValidationResult"])
	}
}

func TestDriftSnapshotRefreshWithoutConfirmIsPreview(t *testing.T) {
	srv := driftServer(t, map[string]any{"pools": map[string]any{}}, map[string]any{})
	// §25.2 dry-run/confirm: a refresh without confirm:true is a 200
	// preview that does not replace the snapshot.
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"new": map[string]any{}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true on a no-confirm refresh", body["dryRun"])
	}
}

func TestDriftSnapshotRefreshWithConfirmReplaces(t *testing.T) {
	srv := driftServer(t, map[string]any{"pools": map[string]any{}}, map[string]any{})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"new": map[string]any{}}},
		"confirm": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["replaced"] != true {
		t.Errorf("replaced = %v, want true on a confirmed refresh", body["replaced"])
	}
}

func TestDriftUnavailableWithoutService(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/drift", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no drift service configured", rec.Code)
	}
}

// TestDriftReportColdStartReturns404_spec_25_10_3866 pins the §25.10
// line 3866 table: with no snapshot in the store and no caller-supplied
// body, the §25.10 cold-start case returns 404 rather than the 503
// "Postgres down" path. F-25.10.10.
func TestDriftReportColdStartReturns404_spec_25_10_3866(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(),
		fixedRunning{state: map[string]any{}})
	srv := opsserver.New(opsserver.Options{Drift: svc})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/drift", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (§25.10 line 3866 cold-start); body=%v", rec.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "DRIFT_DESIRED_STATE_MISSING" {
		t.Errorf("error code = %v, want DRIFT_DESIRED_STATE_MISSING", errObj["code"])
	}
}

// TestDriftValidateColdStartReturns404_spec_25_10_3866 pins the same
// §25.10 line 3866 contract for the validate endpoint. F-25.10.10.
func TestDriftValidateColdStartReturns404_spec_25_10_3866(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(),
		fixedRunning{state: map[string]any{}})
	srv := opsserver.New(opsserver.Options{Drift: svc})
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/validate", nil, map[string]any{
		"desired": map[string]any{},
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 on validate cold-start", rec.Code)
	}
}

// TestDriftValidateAcceptsStoredBody_spec_25_10_3782 pins the §25.10
// offline-validation degradation path: when the caller supplies a
// stored snapshot in the body, the HTTP layer threads it through to
// driftservice.Validate so two caller-supplied bodies can be diffed
// without consulting the snapshot store. F-25.10.12.
func TestDriftValidateAcceptsStoredBody_spec_25_10_3782(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(),
		fixedRunning{state: map[string]any{}})
	srv := opsserver.New(opsserver.Options{Drift: svc})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/validate", nil, map[string]any{
		"stored":  map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(5)}}},
		"desired": map[string]any{"pools": map[string]any{"p": map[string]any{"minWarm": float64(9)}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["snapshotValidationResult"] != "diverged" {
		t.Errorf("snapshotValidationResult = %v, want diverged", body["snapshotValidationResult"])
	}
}

// TestDriftSnapshotRefreshExposesByteSize_spec_25_10_3871 pins the
// §25.10 line 3871 drift.snapshot_refreshed audit-event detail: the
// HTTP response (which the audit emitter consumes verbatim) carries
// byteSize. F-25.10.8.
func TestDriftSnapshotRefreshExposesByteSize_spec_25_10_3871(t *testing.T) {
	srv := driftServer(t, map[string]any{"pools": map[string]any{}}, map[string]any{})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"new": map[string]any{"minWarm": float64(3)}}},
		"confirm": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	bs, ok := body["byteSize"].(float64)
	if !ok || bs <= 0 {
		t.Errorf("byteSize = %v, want a positive number", body["byteSize"])
	}
}

// TestDriftReportFreshBypassesCache_spec_25_10_3762 pins the §25.10
// line 3762 ?fresh=true contract: the HTTP layer parses the parameter
// and threads Fresh=true into the service so the running-state cache is
// bypassed. F-25.10.7.
func TestDriftReportFreshBypassesCache_spec_25_10_3762(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID: driftservice.SnapshotLive, DesiredState: map[string]any{"pools": map[string]any{}},
		Source: driftservice.SourceHelmValues, WrittenAt: time.Now().UTC(), WrittenBy: "helm",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	reader := &countingReader{state: map[string]any{"pools": map[string]any{}}}
	svc := driftservice.NewService(store, reader)
	svc.SetRunningStateCache(driftservice.NewMemRunningStateCache(60 * time.Second))
	srv := opsserver.New(opsserver.Options{Drift: svc})

	// Three calls without ?fresh — only one underlying read.
	for i := 0; i < 3; i++ {
		rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/drift?scope=pools", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("warm-cache call %d: status %d", i, rec.Code)
		}
	}
	warmCalls := reader.calls
	if warmCalls != 1 {
		t.Errorf("warm-cache calls = %d, want 1", warmCalls)
	}
	// Three calls with ?fresh=true — every one re-reads.
	for i := 0; i < 3; i++ {
		rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/drift?scope=pools&fresh=true", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("fresh call %d: status %d", i, rec.Code)
		}
	}
	if reader.calls-warmCalls != 3 {
		t.Errorf("?fresh=true reads = %d, want 3", reader.calls-warmCalls)
	}
}

// countingReader is a RunningStateReader for the HTTP-layer cache test.
type countingReader struct {
	state map[string]any
	calls int
}

func (c *countingReader) RunningState(context.Context, string) (map[string]any, error) {
	c.calls++
	return c.state, nil
}
