// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// restoreOperationsServer wires GET /v1/admin/operations over a real
// backup.Service (MemStore) projected through the §25.4 RestoreSource, so
// the test drives the same source the production Inventory uses.
func restoreOperationsServer(t *testing.T, store *backup.MemStore) *opsserver.Server {
	t.Helper()
	svc, err := backup.NewService(backup.Config{Store: store, Launcher: backup.NewFakeLauncher()})
	if err != nil {
		t.Fatalf("build backup service: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Inventory: operations.New(opsinventory.NewRestoreSource(svc, "")),
		Audit:     &captureAudit{},
	})
}

// spec §25.4 lines 1694, 1707-1711: a running restore (an ops_restore_state
// row) appears in the Operations Inventory under kind "restore" in status
// in_progress, with a decodable "restore-"-prefixed operationId and a
// resources block pointing at the restore's status/resume endpoints. The
// finding this pins is that no restore source was wired into the inventory,
// so a running restore was silently absent from GET /v1/admin/operations.
func TestOperationsInventoryListsRunningRestore(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	store := backup.NewMemStore()
	if err := store.InsertRestore(context.Background(), backup.RestoreState{
		ID:                 "rst-abc123",
		BackupID:           "bkp-xyz",
		StartedAt:          now,
		Status:             backup.RestoreStatusRunning,
		StartedBy:          "sa-admin",
		PreRestoreBackupID: "bkp-pre",
		ShardStates: map[string]backup.ShardState{
			"shard-0": {Status: "completed"},
			"shard-1": {Status: "running"},
		},
	}); err != nil {
		t.Fatalf("insert restore: %v", err)
	}
	srv := restoreOperationsServer(t, store)
	p := platformAdmin("sa-admin")

	rec, body := getAuthed(t, srv, "/v1/admin/operations", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (the running restore)", len(ops))
	}
	op := ops[0].(map[string]any)
	if op["kind"] != string(operations.KindRestore) {
		t.Errorf("kind = %v, want %q", op["kind"], operations.KindRestore)
	}
	if op["status"] != string(operations.StatusInProgress) {
		t.Errorf("status = %v, want %q", op["status"], operations.StatusInProgress)
	}
	if op["operationId"] != "restore-rst-abc123" {
		t.Errorf("operationId = %v, want restore-rst-abc123", op["operationId"])
	}
	res, _ := op["resources"].(map[string]any)
	if status, _ := res["status"].(string); status != "GET /v1/admin/restore/rst-abc123/status" {
		t.Errorf("resources.status = %q, want the restore status endpoint", status)
	}
	if _, ok := res["resume"]; !ok {
		t.Error("resources block must expose the restore/resume endpoint")
	}
	if op["progress"] == nil {
		t.Error("a restore must carry a progress object in the inventory (spec §25.2 line 358)")
	}

	// The operationId is decodable: GET /v1/admin/operations/{id} resolves it.
	rec, body = getAuthed(t, srv, "/v1/admin/operations/restore-rst-abc123", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("get-by-id status = %d, want 200", rec.Code)
	}
	single, _ := body["operation"].(map[string]any)
	if single["operationId"] != "restore-rst-abc123" {
		t.Errorf("get-by-id operationId = %v, want restore-rst-abc123", single["operationId"])
	}
}

// spec §25.4 line 1811: a failed restore is included in the inventory
// (under ?status=failed) because it requires operator resolution via
// restore/resume; it is not silently dropped.
func TestOperationsInventoryListsFailedRestore(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	store := backup.NewMemStore()
	if err := store.InsertRestore(context.Background(), backup.RestoreState{
		ID:          "rst-fail01",
		BackupID:    "bkp-xyz",
		StartedAt:   now,
		Status:      backup.RestoreStatusFailed,
		StartedBy:   "sa-admin",
		FailedShard: "shard-1",
		Error:       "SHARD_RESTORE_FAILED",
	}); err != nil {
		t.Fatalf("insert restore: %v", err)
	}
	srv := restoreOperationsServer(t, store)
	p := platformAdmin("sa-admin")

	// Default status filter excludes failed; the explicit filter includes it.
	rec, body := getAuthed(t, srv, "/v1/admin/operations?status=failed", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (the failed restore)", len(ops))
	}
	op := ops[0].(map[string]any)
	if op["status"] != string(operations.StatusFailed) {
		t.Errorf("status = %v, want %q", op["status"], operations.StatusFailed)
	}
	res, _ := op["resources"].(map[string]any)
	if _, ok := res["resume"]; !ok {
		t.Error("a failed restore must expose the restore/resume endpoint for operator resolution")
	}
}
