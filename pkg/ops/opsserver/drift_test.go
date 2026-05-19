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

func TestDriftReportMissingSnapshotReturns503(t *testing.T) {
	svc := driftservice.NewService(driftservice.NewMemSnapshotStore(), fixedRunning{state: map[string]any{}})
	srv := opsserver.New(opsserver.Options{Drift: svc})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/drift", nil, nil)
	// §25.10: no snapshot and no caller body returns 503 DRIFT_DESIRED_STATE_MISSING.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "DRIFT_DESIRED_STATE_MISSING" {
		t.Errorf("error code = %v, want DRIFT_DESIRED_STATE_MISSING", errObj["code"])
	}
}

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
