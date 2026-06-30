// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
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

// spec: §25.2 lines 287-300, §25.11 line 3883 — a full backup in
// production without confirm:true returns 200 with a dry-run preview
// (not an error); with confirm:true it is accepted. F-25.2.5.
func TestCreateBackupConfirmGateInProduction_spec_25_2_300(t *testing.T) {
	srv, _, store := newBackupServer(t, true)
	rec := do(srv, http.MethodPost, "/v1/admin/backups", `{"type":"full"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (dry-run preview)\nbody: %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		DryRun  bool `json:"dryRun"`
		Preview struct {
			ResourcesAffected []string `json:"resourcesAffected"`
			EstimatedDowntime string   `json:"estimatedDowntime"`
			Warnings          []string `json:"warnings"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !preview.DryRun {
		t.Errorf("dryRun = false, want true")
	}
	if len(preview.Preview.ResourcesAffected) == 0 {
		t.Errorf("preview.resourcesAffected is empty, want the full-backup components")
	}
	if preview.Preview.EstimatedDowntime != "0s" {
		t.Errorf("preview.estimatedDowntime = %q, want 0s", preview.Preview.EstimatedDowntime)
	}
	if len(preview.Preview.Warnings) == 0 {
		t.Errorf("preview.warnings is empty, want a confirm advisory")
	}
	// A dry run mutates no state: no backup row was created.
	if rows, _ := store.ListBackups(context.Background(), backup.BackupFilter{}); len(rows) != 0 {
		t.Errorf("dry-run created %d backups, want 0", len(rows))
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

// spec: §12.8, §25.11
// diagnosis: POST /v1/admin/restore/{id}/confirm-legal-hold-ledger
// records the platform-admin watermark on a failed restore.
func TestConfirmLegalHoldLedgerEndpointAcceptsConfirmation(t *testing.T) {
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
	state, err := store.GetRestore(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	state.Status = backup.RestoreStatusFailed
	state.Error = "legal_hold_ledger_stale"
	if err := store.UpdateRestore(ctx, state); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/restore/"+result.RestoreID+"/confirm-legal-hold-ledger",
		strings.NewReader(`{"justification":"out-of-band ledger reapplied"}`))
	req.Header.Set("X-Lenny-Caller", "bob")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\nbody: %s", rec.Code, rec.Body.String())
	}
	var confirmed backup.RestoreState
	if err := json.Unmarshal(rec.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if confirmed.LedgerConfirmedBy != "bob" {
		t.Errorf("LedgerConfirmedBy = %q, want bob", confirmed.LedgerConfirmedBy)
	}
	if confirmed.LedgerConfirmedJustification == "" {
		t.Error("LedgerConfirmedJustification must be persisted")
	}
}

// spec: §12.8, §25.11
// diagnosis: confirm-legal-hold-ledger rejects a missing justification.
func TestConfirmLegalHoldLedgerEndpointRequiresJustification(t *testing.T) {
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
	state, err := store.GetRestore(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	state.Status = backup.RestoreStatusFailed
	if err := store.UpdateRestore(ctx, state); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}
	rec := do(srv, http.MethodPost,
		"/v1/admin/restore/"+result.RestoreID+"/confirm-legal-hold-ledger",
		`{"justification":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing justification\nbody: %s", rec.Code, rec.Body.String())
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

// TestBackupUnavailableUsesCanonicalErrorCode_spec_25_11_4335 asserts
// that when lenny-ops has no BackupService wired (s.backups == nil),
// the §25.11 routes return BACKUP_STORAGE_UNREACHABLE — the closest
// catalogued code (Error Codes table line 4335, TRANSIENT 503).
// Returning a non-catalogued code would break the agent-operability
// contract that the response is one of the spec's enumerated set.
func TestBackupUnavailableUsesCanonicalErrorCode_spec_25_11_4335(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Production: false})
	// Every §25.11 backup route should map an unconfigured subsystem
	// to the same spec-canonical code.
	paths := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/backups"},
		{http.MethodGet, "/v1/admin/backups"},
		{http.MethodGet, "/v1/admin/backups/schedule"},
		{http.MethodPost, "/v1/admin/restore/preview"},
		{http.MethodGet, "/v1/admin/restore/safety-check"},
	}
	for _, tc := range paths {
		rec := do(srv, tc.method, tc.path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503\nbody: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			continue
		}
		var resp struct {
			Error struct {
				Code     string `json:"code"`
				Category string `json:"category"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Errorf("%s %s decode: %v", tc.method, tc.path, err)
			continue
		}
		if resp.Error.Code != backup.ErrCodeStorageUnreachable {
			t.Errorf("%s %s code = %q, want %q (spec §25.11 line 4335)",
				tc.method, tc.path, resp.Error.Code, backup.ErrCodeStorageUnreachable)
		}
		if resp.Error.Category != "TRANSIENT" {
			t.Errorf("%s %s category = %q, want TRANSIENT", tc.method, tc.path, resp.Error.Category)
		}
	}
}

// setupFailedRestore drives a backup through to a failed restore so the
// confirm-legal-hold-ledger precondition holds, returning the restore id.
func setupFailedRestore(t *testing.T, svc *backup.Service, store *backup.MemStore) string {
	t.Helper()
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
	state, err := store.GetRestore(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	state.Status = backup.RestoreStatusFailed
	if err := store.UpdateRestore(ctx, state); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}
	return result.RestoreID
}

// TestConfirmLegalHoldLedgerRequiresPlatformAdmin_spec_25_11_3897 covers
// the §25.11 line 3897 narrowing: confirm-legal-hold-ledger requires the
// platform-admin role specifically, not the general admin role gate that
// also admits tenant-admin.
func TestConfirmLegalHoldLedgerRequiresPlatformAdmin_spec_25_11_3897(t *testing.T) {
	srv, svc, store := newBackupServer(t, false)
	restoreID := setupFailedRestore(t, svc, store)
	path := "/v1/admin/restore/" + restoreID + "/confirm-legal-hold-ledger"
	body := `{"justification":"ledger reapplied out-of-band"}`

	// A tenant-admin caller is rejected with 403 FORBIDDEN.
	tenantReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	tenantReq.Header.Set("X-Lenny-Role", "tenant-admin")
	tenantReq.Header.Set("X-Lenny-Caller", "bob")
	tenantRec := httptest.NewRecorder()
	srv.ServeHTTP(tenantRec, tenantReq)
	if tenantRec.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin status = %d, want 403\nbody: %s", tenantRec.Code, tenantRec.Body.String())
	}

	// A platform-admin caller is accepted (202).
	adminReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	adminReq.Header.Set("X-Lenny-Role", "platform-admin")
	adminReq.Header.Set("X-Lenny-Caller", "alice")
	adminRec := httptest.NewRecorder()
	srv.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusAccepted {
		t.Fatalf("platform-admin status = %d, want 202\nbody: %s", adminRec.Code, adminRec.Body.String())
	}
}

// newAuditingBackupServer builds an opsserver whose BackupService routes
// every audit event to the returned captured-events accessor, so a test
// can assert the §25.1/§25.2 operationId field reaches the audit trail.
func newAuditingBackupServer(t *testing.T) (*opsserver.Server, *backup.Service, *backup.MemStore, func() []backup.AuditEvent) {
	t.Helper()
	store := backup.NewMemStore()
	var mu sync.Mutex
	var events []backup.AuditEvent
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
		Audit: func(ev backup.AuditEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	srv := opsserver.New(opsserver.Options{Backups: svc, Production: false})
	captured := func() []backup.AuditEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]backup.AuditEvent(nil), events...)
	}
	return srv, svc, store, captured
}

// auditEventOfType returns the first captured audit event of the given
// type, or fails the test when none was emitted.
func auditEventOfType(t *testing.T, events []backup.AuditEvent, eventType string) backup.AuditEvent {
	t.Helper()
	for _, ev := range events {
		if ev.Type == eventType {
			return ev
		}
	}
	t.Fatalf("no %s audit event among %d captured events", eventType, len(events))
	return backup.AuditEvent{}
}

// spec: §25.1 line 121 (operationId on every request audit event),
// §25.2 line 350 (operationId propagated to audit events).
// diagnosis: handleCreateBackup did not propagate the caller
// X-Lenny-Operation-ID onto the BackupRequest, so the §25.17 watchdog
// operation correlation never reached the backup row or the
// backup.created audit event. A failure means the create handler drops
// the caller operationId again.
func TestCreateBackupPropagatesOperationID_spec_25_1_121(t *testing.T) {
	srv, _, store, captured := newAuditingBackupServer(t)
	const opID = "550e8400-e29b-41d4-a716-446655440000"

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/backups",
		strings.NewReader(`{"type":"postgres"}`))
	req.Header.Set("X-Lenny-Operation-ID", opID)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\nbody: %s", rec.Code, rec.Body.String())
	}

	// The persisted backup row carries the caller operationId.
	rows, err := store.ListBackups(context.Background(), backup.BackupFilter{})
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("listed %d backups, want 1", len(rows))
	}
	if rows[0].OperationID != opID {
		t.Errorf("backup row operationId = %q, want %q", rows[0].OperationID, opID)
	}

	// The backup.created audit event carries the caller operationId.
	ev := auditEventOfType(t, captured(), string(audit.EventBackupCreated))
	if got := ev.Fields["operationId"]; got != opID {
		t.Errorf("backup.created audit operationId = %v, want %q", got, opID)
	}
}

// spec: §25.1 line 121 (operationId on every request audit event),
// §25.2 line 350 (operationId propagated to audit events).
// diagnosis: handleRestoreExecute did not propagate the caller
// X-Lenny-Operation-ID onto the RestoreRequest, so the §25.17 watchdog
// operation correlation never reached the ops_restore_state row or the
// restore.started audit event. A failure means the restore handler drops
// the caller operationId again.
func TestRestoreExecutePropagatesOperationID_spec_25_1_121(t *testing.T) {
	srv, svc, store, captured := newAuditingBackupServer(t)
	ctx := context.Background()
	const opID = "550e8400-e29b-41d4-a716-446655440001"

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

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/restore/execute",
		strings.NewReader(`{"backupId":"`+b.ID+`","confirm":true,"acknowledgeDataLoss":true}`))
	req.Header.Set("X-Lenny-Operation-ID", opID)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\nbody: %s", rec.Code, rec.Body.String())
	}
	var result backup.RestoreResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The persisted restore-state row carries the caller operationId.
	state, err := store.GetRestore(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	if state.OperationID != opID {
		t.Errorf("restore-state operationId = %q, want %q", state.OperationID, opID)
	}

	// The restore.started audit event carries the caller operationId.
	ev := auditEventOfType(t, captured(), string(audit.EventRestoreStarted))
	if got := ev.Fields["operationId"]; got != opID {
		t.Errorf("restore.started audit operationId = %v, want %q", got, opID)
	}
}
