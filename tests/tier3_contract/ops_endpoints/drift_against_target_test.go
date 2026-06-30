// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.10 drift report against=target and
// against=both wire contract once a §25.8 OpsRoll target snapshot has been
// written. They pin F-DR-3: with the target-snapshot writer wired, the
// in-flight target comparison resolves with 200 instead of the
// DRIFT_NO_TARGET_SNAPSHOT 404 it returns through an upgrade with no
// target row.
package ops_endpoints_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// opsServerWithTarget builds a §25 lenny-ops Server whose drift service has
// both a live snapshot and a target snapshot written via the §25.10
// WriteTargetSnapshot path, modeling the state after the OpsRoll startup
// hook writes bootstrap_seed_snapshot_target.
func opsServerWithTarget(t *testing.T) *opsserver.Server {
	t.Helper()
	store := driftservice.NewMemSnapshotStore()
	if err := store.Put(context.Background(), driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(5)}}},
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    time.Now().UTC(),
		WrittenBy:    "helm",
	}); err != nil {
		t.Fatalf("seed live snapshot: %v", err)
	}
	svc := driftservice.NewService(store, fixedRunningState{
		state: map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(12)}}},
	})
	// §25.10 line 3788: write the target snapshot the OpsRoll startup hook
	// writes, so against=target and against=both resolve.
	if err := svc.WriteTargetSnapshot(context.Background(), "upgrade-1", "lenny-ops",
		map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(8)}}}); err != nil {
		t.Fatalf("write target snapshot: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Locks:       coordination.NewMemStore(),
		Escalations: escalation.NewService(nil),
		Drift:       svc,
	})
}

// TestDriftAgainstTargetResolvesAfterWrite_spec_25_10_3788 confirms GET
// /v1/admin/drift?against=target returns 200 once the §25.8 target
// snapshot is written, instead of the DRIFT_NO_TARGET_SNAPSHOT 404 it
// returns with no target row. F-DR-3.
//
// spec: 25.10 (against=target — in-flight target comparison)
// diagnosis: against=target returned 404 even after the OpsRoll
// target-snapshot writer ran, so an agent cannot see what an in-flight
// upgrade will change. F-DR-3: the writer and promoter were unbuilt, so no
// target row was ever written.
func TestDriftAgainstTargetResolvesAfterWrite_spec_25_10_3788(t *testing.T) {
	srv := opsServerWithTarget(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/drift?against=target&scope=pools", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("against=target status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if body["against"] != "target" {
		t.Errorf("against = %v, want target", body["against"])
	}
	if _, ok := body["driftCount"]; !ok {
		t.Error("against=target report is missing driftCount")
	}
}

// TestDriftAgainstBothResolvesAfterWrite_spec_25_10_3788 confirms GET
// /v1/admin/drift?against=both returns 200 with both the live and target
// diffs once the target snapshot is written. F-DR-3.
//
// spec: 25.10 (against=both — combined live and target diffs)
// diagnosis: against=both returned 404 or omitted the live/target diffs
// after the target-snapshot writer ran. §25.10 lets an agent compare the
// running state against both snapshots in one response during an upgrade.
func TestDriftAgainstBothResolvesAfterWrite_spec_25_10_3788(t *testing.T) {
	srv := opsServerWithTarget(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/drift?against=both&scope=pools", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("against=both status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if body["against"] != "both" {
		t.Errorf("against = %v, want both", body["against"])
	}
	if _, ok := body["live"].(map[string]any); !ok {
		t.Error("against=both report is missing the live diff")
	}
	if _, ok := body["target"].(map[string]any); !ok {
		t.Error("against=both report is missing the target diff")
	}
}
