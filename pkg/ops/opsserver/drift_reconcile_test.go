// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// labelApplier applies every resource, failing the labels in fail.
type labelApplier struct{ fail map[string]bool }

func (a labelApplier) Apply(_ context.Context, rtype, rid string, _ map[string]any) error {
	key := rtype
	if rid != "" {
		key = rtype + ":" + rid
	}
	if a.fail[key] {
		return errors.New("apply failed")
	}
	return nil
}

// reconcileServer wires a drift service (live snapshot drifts on two
// pools) plus the supplied applier onto an opsserver.
func reconcileServer(t *testing.T, applier driftservice.ResourceApplier) *opsserver.Server {
	t.Helper()
	store := driftservice.NewMemSnapshotStore()
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID: driftservice.SnapshotLive,
		DesiredState: map[string]any{"pools": map[string]any{
			"chat":   map[string]any{"minWarm": float64(5)},
			"coding": map[string]any{"image": "coding:1"},
		}},
		Source: driftservice.SourceHelmValues, WrittenAt: time.Now().UTC(), WrittenBy: "helm",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{"pools": map[string]any{
		"chat":   map[string]any{"minWarm": float64(9)},
		"coding": map[string]any{"image": "coding:2"},
	}}})
	if applier != nil {
		svc.SetApplier(applier)
	}
	return opsserver.New(opsserver.Options{Drift: svc})
}

// spec: §25.10 line 3765, 3842 — POST /v1/admin/drift/reconcile with
// confirm:true applies the drifted resources and returns 200.
func TestDriftReconcileConfirm_spec_25_10(t *testing.T) {
	srv := reconcileServer(t, labelApplier{})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/reconcile", nil,
		map[string]any{"scope": "all", "confirm": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if r, _ := body["reconciled"].(float64); r != 2 {
		t.Errorf("reconciled = %v, want 2", body["reconciled"])
	}
	if body["dryRun"] != false {
		t.Errorf("dryRun = %v, want false", body["dryRun"])
	}
}

// spec: §25.10 line 3852, 3865 — a partial reconcile returns HTTP 207
// with errorCode DRIFT_RECONCILE_PARTIAL.
func TestDriftReconcilePartial207_spec_25_10(t *testing.T) {
	srv := reconcileServer(t, labelApplier{fail: map[string]bool{"pools:coding": true}})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/reconcile", nil,
		map[string]any{"scope": "all", "confirm": true})
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%v", rec.Code, body)
	}
	if body["errorCode"] != "DRIFT_RECONCILE_PARTIAL" {
		t.Errorf("errorCode = %v, want DRIFT_RECONCILE_PARTIAL", body["errorCode"])
	}
}

// spec: §25.2 — without confirm the reconcile returns a preview (200,
// dryRun true) and applies nothing.
func TestDriftReconcileDryRun_spec_25_10(t *testing.T) {
	srv := reconcileServer(t, labelApplier{})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/reconcile", nil,
		map[string]any{"scope": "all"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", body["dryRun"])
	}
	if tr, _ := body["totalResources"].(float64); tr != 2 {
		t.Errorf("totalResources = %v, want 2", body["totalResources"])
	}
}

// spec: §25.10 line 3842 — a confirmed reconcile with no applier wired
// fails closed with 503 DRIFT_RECONCILE_UNAVAILABLE.
func TestDriftReconcileNoApplier503_spec_25_10(t *testing.T) {
	srv := reconcileServer(t, nil)
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/drift/reconcile", nil,
		map[string]any{"scope": "all", "confirm": true})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%v", rec.Code, body)
	}
	if errCode(body) != "DRIFT_RECONCILE_UNAVAILABLE" {
		t.Errorf("code = %v, want DRIFT_RECONCILE_UNAVAILABLE", errCode(body))
	}
}

// errCode reads the §25.2 canonical error envelope's nested code field.
func errCode(body map[string]any) any {
	e, _ := body["error"].(map[string]any)
	if e == nil {
		return nil
	}
	return e["code"]
}

// spec: §25.10 line 3791 — GET /v1/admin/drift?against=both returns the
// live and target diffs in one response.
func TestDriftReportAgainstBoth_spec_25_10(t *testing.T) {
	store := driftservice.NewMemSnapshotStore()
	now := time.Now().UTC()
	for id, minWarm := range map[string]float64{driftservice.SnapshotLive: 5, driftservice.SnapshotTarget: 9} {
		if err := store.Put(context.Background(), driftservice.Snapshot{
			ID:           id,
			DesiredState: map[string]any{"pools": map[string]any{"chat": map[string]any{"minWarm": minWarm}}},
			Source:       driftservice.SourceHelmValues, WrittenAt: now, WrittenBy: "helm",
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	svc := driftservice.NewService(store, fixedRunning{state: map[string]any{
		"pools": map[string]any{"chat": map[string]any{"minWarm": float64(9)}}}})
	srv := opsserver.New(opsserver.Options{Drift: svc})

	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/drift?against=both&scope=pools", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["against"] != "both" {
		t.Errorf("against = %v, want both", body["against"])
	}
	live, _ := body["live"].(map[string]any)
	target, _ := body["target"].(map[string]any)
	if live == nil || target == nil {
		t.Fatalf("missing live/target in body=%v", body)
	}
	if dc, _ := live["driftCount"].(float64); dc != 1 {
		t.Errorf("live driftCount = %v, want 1", live["driftCount"])
	}
	if dc, _ := target["driftCount"].(float64); dc != 0 {
		t.Errorf("target driftCount = %v, want 0", target["driftCount"])
	}
}

// spec: §25.10 line 3791 — against=both with no target snapshot returns
// 404 DRIFT_NO_TARGET_SNAPSHOT.
func TestDriftReportAgainstBothNoTarget_spec_25_10(t *testing.T) {
	srv := driftServer(t,
		map[string]any{"pools": map[string]any{}},
		map[string]any{"pools": map[string]any{}})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/drift?against=both", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%v", rec.Code, body)
	}
	if errCode(body) != "DRIFT_NO_TARGET_SNAPSHOT" {
		t.Errorf("code = %v, want DRIFT_NO_TARGET_SNAPSHOT", errCode(body))
	}
}
