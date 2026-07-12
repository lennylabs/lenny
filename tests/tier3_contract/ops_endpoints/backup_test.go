// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.11 backup-and-restore endpoints
// served by lenny-ops. The suite drives pkg/ops/opsserver over an
// in-memory BackupService and pins the public wire format the spec's
// Response Types and Safety Check JSON define: the Backup field set, the
// RestorePreview field set (including the ArtifactStore replication-lag
// fields), the safety-check body, the RestoreState field set, and the
// §25.2 canonical error envelope carrying each documented canonical
// error code. A field rename in any of these would fail here.
package ops_endpoints_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// backupServer builds a §25.11-wired lenny-ops Server over an in-memory
// BackupService (fake Job launcher, in-memory store and lock) so the
// contract suite exercises every backup/restore endpoint without a
// cluster or Postgres. The clock is fixed so a freshly-created backup is
// a "safe" (< 5 minute old) restore point.
func backupServer(t *testing.T) *opsserver.Server {
	t.Helper()
	svc, err := backup.NewService(backup.Config{
		Store:           backup.NewMemStore(),
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return opsserver.New(opsserver.Options{Backups: svc})
}

// createBackup posts a postgres backup and returns the decoded Backup
// body, failing the test when the create does not return 202.
func createBackup(t *testing.T, srv *opsserver.Server) map[string]any {
	t.Helper()
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/backups", nil,
		map[string]any{"type": "postgres"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create backup status = %d, want 202; body=%v", rec.Code, body)
	}
	return body
}

// assertFields fails the test for every named key absent from body.
func assertFields(t *testing.T, what string, body map[string]any, fields ...string) {
	t.Helper()
	for _, f := range fields {
		if _, ok := body[f]; !ok {
			t.Errorf("%s response is missing the %q field; body=%v", what, f, body)
		}
	}
}

// TestCreateBackupContract pins the §25.11 Backup wire format returned
// by POST /v1/admin/backups.
//
// spec: 25.11 (Response Types — Backup struct; POST /v1/admin/backups)
// diagnosis: Creating a backup returned a body missing a §25.11 Backup
// field. Agents read id to poll, status to gate a restore, and
// storagePath/checksum to verify the archive; a rename breaks them.
func TestCreateBackupContract(t *testing.T) {
	srv := backupServer(t)
	body := createBackup(t, srv)
	assertFields(t, "Backup", body,
		"id", "type", "status", "startedAt", "storagePath", "checksum",
		"components", "startedBy", "jobId")
	if body["type"] != "postgres" {
		t.Errorf("type = %v, want postgres", body["type"])
	}
	// §25.11 Backup.status enumerates pending/running/completed/... — a
	// freshly-launched backup is running under the fake launcher.
	if body["status"] != string(backup.StatusRunning) {
		t.Errorf("status = %v, want %v", body["status"], backup.StatusRunning)
	}
}

// TestGetBackupContract pins the §25.11 Backup detail wire format and
// confirms the id round-trips through GET /v1/admin/backups/{id}.
//
// spec: 25.11 (GET /v1/admin/backups/{id} — Backup details)
// diagnosis: Fetching a backup by id returned a body missing a §25.11
// Backup field or a different id than was created. The restore workflow
// reads the detail body for platformVersion and schemaVersion.
func TestGetBackupContract(t *testing.T) {
	srv := backupServer(t)
	created := createBackup(t, srv)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created backup has no id: %v", created)
	}
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/backups/"+id, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get backup status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	assertFields(t, "Backup", body,
		"id", "type", "status", "startedAt", "storagePath", "checksum",
		"components", "startedBy", "jobId")
	if body["id"] != id {
		t.Errorf("id = %v, want %v", body["id"], id)
	}
}

// TestRestorePreviewContract pins the §25.11 RestorePreview wire format
// returned by POST /v1/admin/restore/preview, including the ArtifactStore
// replication-lag fields §25.11 requires the preview to carry so an
// operator can choose a restore point ahead of the replication horizon.
//
// spec: 25.11 (Response Types — RestorePreview; POST /v1/admin/restore/preview;
// "the preview response includes artifactReplicationLagSeconds and
// estimatedOrphanArtifactRows")
// diagnosis: The restore preview omitted a documented field. Agents read
// compatible/affectedResources/estimatedDowntime to gauge impact and the
// two artifact-replication fields to bound orphan-row risk.
func TestRestorePreviewContract(t *testing.T) {
	srv := backupServer(t)
	created := createBackup(t, srv)
	id, _ := created["id"].(string)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/restore/preview", nil,
		map[string]any{"backupId": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore preview status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	assertFields(t, "RestorePreview", body,
		"backupId", "backupVersion", "currentVersion", "compatible",
		"affectedResources", "estimatedDowntime", "requiresFullStop", "warnings",
		"artifactReplicationLagSeconds", "estimatedOrphanArtifactRows")
	if body["backupId"] != id {
		t.Errorf("backupId = %v, want %v", body["backupId"], id)
	}
}

// TestRestoreSafetyCheckContract pins the §25.11 Safety Check JSON body
// returned by GET /v1/admin/restore/safety-check, including the nested
// dataLossEstimate and compatibility objects and their field sets.
//
// spec: 25.11 (Safety Check — GET /v1/admin/restore/safety-check JSON:
// backupId, backupTakenAt, currentTime, safe, dataLossEstimate{...},
// compatibility{...}, recommendedAction)
// diagnosis: The safety-check body dropped a documented field. Agents
// read safe and recommendedAction to gate a restore and read
// dataLossEstimate/compatibility to justify acknowledgeDataLoss.
func TestRestoreSafetyCheckContract(t *testing.T) {
	srv := backupServer(t)
	created := createBackup(t, srv)
	id, _ := created["id"].(string)
	rec, body := request(t, srv, http.MethodGet,
		"/v1/admin/restore/safety-check?backupId="+id, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("safety-check status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	assertFields(t, "RestoreSafetyCheck", body,
		"backupId", "backupTakenAt", "currentTime", "safe",
		"dataLossEstimate", "compatibility", "recommendedAction")
	if _, ok := body["safe"].(bool); !ok {
		t.Errorf("safe = %v, want a bool", body["safe"])
	}
	if body["recommendedAction"] == "" {
		t.Errorf("recommendedAction is empty; want an actionable string")
	}
	dle, ok := body["dataLossEstimate"].(map[string]any)
	if !ok {
		t.Fatalf("dataLossEstimate is not an object: %v", body["dataLossEstimate"])
	}
	assertFields(t, "dataLossEstimate", dle,
		"mutationsSinceBackup", "sessionsAffected", "auditEventsLost", "tablesWithDivergence")
	compat, ok := body["compatibility"].(map[string]any)
	if !ok {
		t.Fatalf("compatibility is not an object: %v", body["compatibility"])
	}
	assertFields(t, "compatibility", compat,
		"backupSchemaVersion", "currentSchemaVersion", "backupPlatformVersion",
		"currentPlatformVersion", "schemaMigrationsBetween", "compatible", "warnings")
}

// TestRestoreStatusContract pins the §25.11 RestoreState wire format
// returned by GET /v1/admin/restore/{id}/status after a restore is
// executed against a safe (recent) backup.
//
// spec: 25.11 (GET /v1/admin/restore/{id}/status — the ops_restore_state
// row: per-shard status; Restore Workflow)
// diagnosis: The restore status body dropped a documented field. Agents
// poll id/status/shardStates for progress and read failedShard to
// diagnose a partial restore.
func TestRestoreStatusContract(t *testing.T) {
	srv := backupServer(t)
	created := createBackup(t, srv)
	id, _ := created["id"].(string)
	// A backup created at the fixed clock is < 5 minutes old, so the
	// safety check reports safe:true and execute needs no acknowledgeDataLoss.
	rec, exec := request(t, srv, http.MethodPost, "/v1/admin/restore/execute", nil,
		map[string]any{"backupId": id, "confirm": true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restore execute status = %d, want 202; body=%v", rec.Code, exec)
	}
	restoreID, _ := exec["restoreId"].(string)
	if restoreID == "" {
		t.Fatalf("execute returned no restoreId: %v", exec)
	}
	rec, body := request(t, srv, http.MethodGet,
		"/v1/admin/restore/"+restoreID+"/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	assertFields(t, "RestoreState", body,
		"id", "backupId", "startedAt", "status", "shardStates",
		"startedBy", "preRestoreBackupId")
	if body["id"] != restoreID {
		t.Errorf("id = %v, want %v", body["id"], restoreID)
	}
}

// TestBackupErrorEnvelopeContract pins the §25.2 canonical error envelope
// and the §25.11 canonical error code for the documented not-found
// failures on the backup and restore endpoints.
//
// spec: 25.2 (Error Response Envelope — code, category, message,
// retryable, documentationUrl), 25.11 (Error Codes — BACKUP_NOT_FOUND 404
// PERMANENT; RESTORE_NOT_FOUND 404 PERMANENT)
// diagnosis: A missing-resource request did not return the §25.2 error
// envelope with the §25.11 canonical code and its documented HTTP status
// and category. Agents branch on error.code and error.category; a rename
// or a wrong status breaks retry and remediation logic.
func TestBackupErrorEnvelopeContract(t *testing.T) {
	srv := backupServer(t)

	cases := []struct {
		name       string
		method     string
		url        string
		wantStatus int
		wantCode   string
	}{
		{"backup detail not found", http.MethodGet, "/v1/admin/backups/bkp-missing", http.StatusNotFound, "BACKUP_NOT_FOUND"},
		{"safety-check backup not found", http.MethodGet, "/v1/admin/restore/safety-check?backupId=bkp-missing", http.StatusNotFound, "BACKUP_NOT_FOUND"},
		{"restore status not found", http.MethodGet, "/v1/admin/restore/rst-missing/status", http.StatusNotFound, "RESTORE_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := request(t, srv, tc.method, tc.url, nil, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%v", rec.Code, tc.wantStatus, body)
			}
			assertJSONContentType(t, rec)
			env := errorEnvelope(t, body)
			assertFields(t, "error envelope", env,
				"code", "category", "message", "retryable", "documentationUrl")
			if env["code"] != tc.wantCode {
				t.Errorf("error.code = %v, want %v", env["code"], tc.wantCode)
			}
			// §25.11 Error Codes: both not-found codes are PERMANENT, so
			// the §25.2 envelope reports retryable:false.
			if env["category"] != "PERMANENT" {
				t.Errorf("error.category = %v, want PERMANENT", env["category"])
			}
			if env["retryable"] != false {
				t.Errorf("error.retryable = %v, want false for a PERMANENT error", env["retryable"])
			}
		})
	}
}

// TestRestorePreviewBackupNotFoundContract confirms the restore-preview
// endpoint returns the §25.11 BACKUP_NOT_FOUND envelope when the
// requested backup does not exist.
//
// spec: 25.11 (POST /v1/admin/restore/preview; Error Codes —
// BACKUP_NOT_FOUND 404 PERMANENT)
// diagnosis: Previewing a restore against an unknown backup id did not
// return the §25.2 envelope with BACKUP_NOT_FOUND. Agents rely on the
// canonical code to distinguish a typo from a transient store failure.
func TestRestorePreviewBackupNotFoundContract(t *testing.T) {
	srv := backupServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/restore/preview", nil,
		map[string]any{"backupId": "bkp-missing"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%v", rec.Code, body)
	}
	env := errorEnvelope(t, body)
	if env["code"] != "BACKUP_NOT_FOUND" {
		t.Errorf("error.code = %v, want BACKUP_NOT_FOUND", env["code"])
	}
}
