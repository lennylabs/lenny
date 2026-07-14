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

// backupOperationsServer wires GET /v1/admin/operations over a real
// backup.Service (MemStore) projected through the §25.4 BackupSource, so
// the test drives the same source the production Inventory uses.
func backupOperationsServer(t *testing.T, store *backup.MemStore) *opsserver.Server {
	t.Helper()
	svc, err := backup.NewService(backup.Config{Store: store, Launcher: backup.NewFakeLauncher()})
	if err != nil {
		t.Fatalf("build backup service: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Inventory: operations.New(opsinventory.NewBackupSource(svc, "")),
		Audit:     &captureAudit{},
	})
}

// spec §25.4 lines 1707-1811: an ops_backups row in status 'running' is an
// operation of kind "backup" in status in_progress ("backup Job running",
// line 1807) with a decodable "backup-"-prefixed operationId, a resources
// block pointing at the backup's own endpoints, and a progress object
// (line 358 requires backups to carry one). The finding this pins is that
// no backup source was wired into the inventory, so a running backup was
// silently absent from GET /v1/admin/operations.
func TestOperationsInventoryListsRunningBackup(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	store := backup.NewMemStore()
	if err := store.InsertBackup(context.Background(), backup.Backup{
		ID:        "bkp-run01",
		Type:      "full",
		Status:    backup.StatusRunning,
		StartedAt: now,
		StartedBy: "sa-admin",
	}); err != nil {
		t.Fatalf("insert backup: %v", err)
	}
	srv := backupOperationsServer(t, store)
	p := platformAdmin("sa-admin")

	rec, body := getAuthed(t, srv, "/v1/admin/operations", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (the running backup)", len(ops))
	}
	op := ops[0].(map[string]any)
	if op["kind"] != string(operations.KindBackup) {
		t.Errorf("kind = %v, want %q", op["kind"], operations.KindBackup)
	}
	if op["status"] != string(operations.StatusInProgress) {
		t.Errorf("status = %v, want %q", op["status"], operations.StatusInProgress)
	}
	if op["operationId"] != "backup-bkp-run01" {
		t.Errorf("operationId = %v, want backup-bkp-run01", op["operationId"])
	}
	res, _ := op["resources"].(map[string]any)
	if status, _ := res["status"].(string); status != "GET /v1/admin/backups/bkp-run01" {
		t.Errorf("resources.status = %q, want the backup status endpoint", status)
	}
	if op["progress"] == nil {
		t.Error("a backup must carry a progress object in the inventory (spec §25.4 line 358)")
	}

	// The operationId is decodable: GET /v1/admin/operations/{id} resolves it.
	rec, body = getAuthed(t, srv, "/v1/admin/operations/backup-bkp-run01", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("get-by-id status = %d, want 200", rec.Code)
	}
	single, _ := body["operation"].(map[string]any)
	if single["operationId"] != "backup-bkp-run01" {
		t.Errorf("get-by-id operationId = %v, want backup-bkp-run01", single["operationId"])
	}
}

// spec §25.4 lines 1711-1712: an ops_backups row in status 'verifying'
// satisfies both Operation Kinds — kind "backup" (status IN
// ('running','verifying')) and kind "backup_verification" (status
// ='verifying'). It must appear under each kind on GET
// /v1/admin/operations. The finding is that neither kind appeared because
// no backup source was wired.
func TestOperationsInventoryListsVerifyingBackup(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	store := backup.NewMemStore()
	if err := store.InsertBackup(context.Background(), backup.Backup{
		ID:        "bkp-ver01",
		Type:      "full",
		Status:    backup.StatusVerifying,
		StartedAt: now,
		StartedBy: "sa-admin",
	}); err != nil {
		t.Fatalf("insert backup: %v", err)
	}
	srv := backupOperationsServer(t, store)
	p := platformAdmin("sa-admin")

	// Under ?kind=backup, the verifying row appears (status IN running,verifying).
	rec, body := getAuthed(t, srv, "/v1/admin/operations?kind=backup", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("kind=backup status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("kind=backup operations = %d, want 1 (the verifying backup)", len(ops))
	}
	if k := ops[0].(map[string]any)["kind"]; k != string(operations.KindBackup) {
		t.Errorf("kind = %v, want %q", k, operations.KindBackup)
	}

	// Under ?kind=backup_verification, the same verifying row appears.
	rec, body = getAuthed(t, srv, "/v1/admin/operations?kind=backup_verification", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("kind=backup_verification status = %d, want 200", rec.Code)
	}
	ops, _ = body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("kind=backup_verification operations = %d, want 1 (the verifying backup)", len(ops))
	}
	op := ops[0].(map[string]any)
	if op["kind"] != string(operations.KindBackupVerification) {
		t.Errorf("kind = %v, want %q", op["kind"], operations.KindBackupVerification)
	}
	if op["status"] != string(operations.StatusInProgress) {
		t.Errorf("status = %v, want %q", op["status"], operations.StatusInProgress)
	}
}
