// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// newBackupServer builds an opsserver wired with a §25.11 BackupService
// over in-memory dependencies.
func newBackupServer(t *testing.T, production bool) (*opsserver.Server, *backup.Service, *backup.MemStore) {
	t.Helper()
	store := backup.NewMemStore()
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	srv := opsserver.New(opsserver.Options{Backups: svc, Production: production})
	return srv, svc, store
}

// do issues a request against the server and returns the recorder.
func do(srv *opsserver.Server, method, path, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestCreateBackupEndpoint(t *testing.T) {
	srv, _, _ := newBackupServer(t, false)
	rec := do(srv, http.MethodPost, "/v1/admin/backups", `{"type":"postgres"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\nbody: %s", rec.Code, rec.Body.String())
	}
	var b backup.Backup
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.Type != "postgres" || b.Status != backup.StatusRunning {
		t.Errorf("backup = %+v, want a running postgres backup", b)
	}
}

func TestCreateBackupConfirmGateInProduction(t *testing.T) {
	srv, _, _ := newBackupServer(t, true)
	// §25.11: a full backup in production without confirm is rejected.
	rec := do(srv, http.MethodPost, "/v1/admin/backups", `{"type":"full"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != backup.ErrCodeRestoreRequiresConfirm {
		t.Errorf("error code = %q, want RESTORE_REQUIRES_CONFIRM", resp.Error.Code)
	}
	// With confirm:true it is accepted.
	rec = do(srv, http.MethodPost, "/v1/admin/backups", `{"type":"full","confirm":true}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("confirmed full backup status = %d, want 202", rec.Code)
	}
}

func TestListBackupsEndpoint(t *testing.T) {
	srv, _, _ := newBackupServer(t, false)
	for i := 0; i < 3; i++ {
		do(srv, http.MethodPost, "/v1/admin/backups", `{"type":"postgres"}`)
	}
	rec := do(srv, http.MethodGet, "/v1/admin/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var page backup.BackupPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Backups) != 3 {
		t.Errorf("listed %d backups, want 3", len(page.Backups))
	}
}

func TestGetBackupNotFoundEndpoint(t *testing.T) {
	srv, _, _ := newBackupServer(t, false)
	rec := do(srv, http.MethodGet, "/v1/admin/backups/bkp-missing", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestScheduleEndpoints(t *testing.T) {
	srv, _, _ := newBackupServer(t, false)
	// GET the default schedule.
	rec := do(srv, http.MethodGet, "/v1/admin/backups/schedule", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET schedule status = %d, want 200", rec.Code)
	}
	var sched backup.BackupSchedule
	if err := json.Unmarshal(rec.Body.Bytes(), &sched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sched.Full != "0 2 * * *" {
		t.Errorf("default full schedule = %q, want 0 2 * * *", sched.Full)
	}
	// PUT a new schedule.
	rec = do(srv, http.MethodPut, "/v1/admin/backups/schedule",
		`{"full":"0 4 * * *","postgres":"0 */8 * * *","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT schedule status = %d, want 200", rec.Code)
	}
	// A malformed cron expression is rejected.
	rec = do(srv, http.MethodPut, "/v1/admin/backups/schedule", `{"full":"bad cron"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT malformed schedule status = %d, want 422", rec.Code)
	}
}

func TestPolicyEndpoints(t *testing.T) {
	srv, _, _ := newBackupServer(t, false)
	rec := do(srv, http.MethodPut, "/v1/admin/backups/policy",
		`{"retainDays":90,"retainCount":30,"retainMinFull":7}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT policy status = %d, want 200", rec.Code)
	}
	rec = do(srv, http.MethodGet, "/v1/admin/backups/policy", "")
	var p backup.RetentionPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.RetainDays != 90 {
		t.Errorf("retainDays = %d, want 90", p.RetainDays)
	}
	// A zero-retention policy is rejected.
	rec = do(srv, http.MethodPut, "/v1/admin/backups/policy", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT zero-retention policy status = %d, want 422", rec.Code)
	}
}

func TestRestoreExecuteDryRunWithoutConfirm(t *testing.T) {
	srv, svc, store := newBackupServer(t, false)
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	completed := time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)
	b.Status = backup.StatusCompleted
	b.CompletedAt = &completed
	if err := store.UpdateBackup(ctx, *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}

	// §25.4: without confirm, restore/execute returns a 200 dry run.
	rec := do(srv, http.MethodPost, "/v1/admin/restore/execute", `{"backupId":"`+b.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run restore status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result backup.RestoreResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.DryRun || result.Preview == nil {
		t.Errorf("result = %+v, want a dry-run preview", result)
	}
}

func TestRestoreExecuteAcknowledgeRequired(t *testing.T) {
	srv, svc, store := newBackupServer(t, false)
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	// A backup completed an hour ago is not "safe".
	completed := time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)
	b.Status = backup.StatusCompleted
	b.CompletedAt = &completed
	if err := store.UpdateBackup(ctx, *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}
	// confirm without acknowledgeDataLoss is rejected.
	rec := do(srv, http.MethodPost, "/v1/admin/restore/execute",
		`{"backupId":"`+b.ID+`","confirm":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestRestoreResumeLockRequired(t *testing.T) {
	srv, svc, store := newBackupServer(t, false)
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	completed := time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)
	b.Status = backup.StatusCompleted
	b.CompletedAt = &completed
	if err := store.UpdateBackup(ctx, *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}
	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	// Resuming while the lock is held succeeds.
	rec := do(srv, http.MethodPost, "/v1/admin/restore/resume?restoreId="+result.RestoreID, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, want 202\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestBackupEndpointUnavailableWithoutService(t *testing.T) {
	// An opsserver without a BackupService reports the surface as
	// unavailable rather than 404.
	srv := opsserver.New(opsserver.Options{})
	rec := do(srv, http.MethodGet, "/v1/admin/backups", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the backup service is not configured", rec.Code)
	}
}

func TestRestoreStatusEndpoint(t *testing.T) {
	srv, svc, store := newBackupServer(t, false)
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	completed := time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC)
	b.Status = backup.StatusCompleted
	b.CompletedAt = &completed
	if err := store.UpdateBackup(ctx, *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}
	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true,
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	rec := do(srv, http.MethodGet, "/v1/admin/restore/"+result.RestoreID+"/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200", rec.Code)
	}
	var state backup.RestoreState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.BackupID != b.ID {
		t.Errorf("restore state backup id = %q, want %q", state.BackupID, b.ID)
	}
}
