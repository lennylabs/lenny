// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// spec: §25.14 lenny-ctl operability command groups — backup, restore,
// the lock get/steal verbs, and drift snapshot refresh.
// F-24.15.6, F-24.15.7, F-24.15.11, F-24.15.12.

// --- F-24.15.12: locks get + steal -----------------------------------

func TestLocksGetTargetsOps(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"id":"lock-1"}`, "locks", "get", "lock-1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/remediation-locks/lock-1" {
		t.Errorf("request: %s %s, want GET /v1/admin/remediation-locks/lock-1", got.method, got.path)
	}
}

func TestLocksStealDryRunOmitsConfirm(t *testing.T) {
	// Without --confirm the steal is a §25.2 dry-run preview: the body
	// carries confirm:false and no reason is required.
	code, got := runAgainstOps(t, http.StatusOK, `{"dryRun":true}`, "locks", "steal", "lock-1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/remediation-locks/lock-1/steal" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["confirm"] != false {
		t.Errorf("confirm: got %v, want false", got.body["confirm"])
	}
}

func TestLocksStealConfirmSendsReasonAndTTL(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"id":"lock-1"}`,
		"locks", "steal", "lock-1", "--confirm", "--reason", "higher-severity alert", "--ttl", "300")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["confirm"] != true {
		t.Errorf("confirm: got %v, want true", got.body["confirm"])
	}
	if got.body["reason"] != "higher-severity alert" {
		t.Errorf("reason: got %v", got.body["reason"])
	}
	// JSON numbers decode as float64.
	if got.body["ttlSeconds"] != float64(300) {
		t.Errorf("ttlSeconds: got %v, want 300", got.body["ttlSeconds"])
	}
}

func TestLocksStealConfirmRequiresReason(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "locks", "steal", "lock-1", "--confirm"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("steal --confirm without --reason: exit code %d, want 2", code)
	}
}

func TestLocksStealRejectsBadTTL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "locks", "steal", "lock-1", "--ttl", "abc"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("steal --ttl abc: exit code %d, want 2", code)
	}
}

// --- F-24.15.11: drift snapshot refresh ------------------------------

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestDriftSnapshotRefreshSendsDesired(t *testing.T) {
	file := writeTempJSON(t, `{"tenants":["acme"]}`)
	code, got := runAgainstOps(t, http.StatusOK, `{"dryRun":true}`,
		"drift", "snapshot", "refresh", "--desired", file)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/drift/snapshot/refresh" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	desired, ok := got.body["desired"].(map[string]any)
	if !ok {
		t.Fatalf("desired: got %T, want object", got.body["desired"])
	}
	if _, present := got.body["confirm"]; present {
		t.Errorf("confirm should be absent without --confirm")
	}
	if _, present := desired["tenants"]; !present {
		t.Errorf("desired did not carry the file payload: %v", desired)
	}
}

func TestDriftSnapshotRefreshConfirm(t *testing.T) {
	file := writeTempJSON(t, `{"tenants":["acme"]}`)
	code, got := runAgainstOps(t, http.StatusOK, `{"replaced":true}`,
		"drift", "snapshot", "refresh", "--desired", file, "--confirm")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["confirm"] != true {
		t.Errorf("confirm: got %v, want true", got.body["confirm"])
	}
}

func TestDriftSnapshotRefreshRequiresDesired(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "drift", "snapshot", "refresh"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("snapshot refresh without --desired: exit code %d, want 2", code)
	}
}

func TestDriftSnapshotRejectsUnknownVerb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "drift", "snapshot", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("snapshot bogus: exit code %d, want 2", code)
	}
}

// --- F-24.15.6: backup -----------------------------------------------

func TestBackupListTargetsOps(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"items":[]}`, "backup", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/backups" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestBackupGet(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"id":"b1"}`, "backup", "get", "b1")
	if code != 0 || got.path != "/v1/admin/backups/b1" {
		t.Errorf("backup get: code %d path %q", code, got.path)
	}
}

func TestBackupCreateSendsTypeAndConfirm(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"id":"b1"}`,
		"backup", "create", "--type", "full", "--confirm")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/backups" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["type"] != "full" || got.body["confirm"] != true {
		t.Errorf("body: %v", got.body)
	}
}

func TestBackupCreateRequiresType(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "backup", "create"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("backup create without --type: exit code %d, want 2", code)
	}
}

func TestBackupVerifyWithMode(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"status":"verifying"}`,
		"backup", "verify", "b1", "--mode", "test-restore")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/backups/b1/verify?mode=test-restore" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestBackupScheduleGet(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"cron":"0 2 * * *"}`, "backup", "schedule", "get")
	if code != 0 || got.method != http.MethodGet || got.path != "/v1/admin/backups/schedule" {
		t.Errorf("backup schedule get: code %d %s %s", code, got.method, got.path)
	}
}

func TestBackupPolicySetSendsFile(t *testing.T) {
	file := writeTempJSON(t, `{"retainDays":30}`)
	code, got := runAgainstOps(t, http.StatusOK, `{"retainDays":30}`,
		"backup", "policy", "set", "--from-file", file)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/backups/policy" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["retainDays"] != float64(30) {
		t.Errorf("body: %v", got.body)
	}
}

func TestBackupSubresourceSetRequiresFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "backup", "schedule", "set"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("backup schedule set without --from-file: exit code %d, want 2", code)
	}
}

// --- F-24.15.7: restore ----------------------------------------------

func TestRestoreSafetyCheck(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"dataLoss":"none"}`,
		"restore", "safety-check", "--backup", "b1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/restore/safety-check?backupId=b1" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestRestorePreview(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"shards":[]}`,
		"restore", "preview", "--backup", "b1")
	if code != 0 || got.method != http.MethodPost || got.path != "/v1/admin/restore/preview" {
		t.Errorf("restore preview: code %d %s %s", code, got.method, got.path)
	}
	if got.body["backupId"] != "b1" {
		t.Errorf("body: %v", got.body)
	}
}

func TestRestoreExecuteSendsConfirmAndAcknowledge(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"restoreId":"r1"}`,
		"restore", "execute", "--backup", "b1", "--confirm", "--acknowledge-data-loss")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/restore/execute" {
		t.Errorf("path: %q", got.path)
	}
	if got.body["backupId"] != "b1" || got.body["confirm"] != true || got.body["acknowledgeDataLoss"] != true {
		t.Errorf("body: %v", got.body)
	}
}

func TestRestoreExecuteDryRunOmitsFlags(t *testing.T) {
	// Without --confirm / --acknowledge-data-loss the body carries only
	// the backupId so the server returns the §25.2 dry-run preview.
	code, got := runAgainstOps(t, http.StatusOK, `{"dryRun":true}`,
		"restore", "execute", "--backup", "b1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if _, ok := got.body["confirm"]; ok {
		t.Errorf("confirm should be absent in a dry-run invocation")
	}
	if _, ok := got.body["acknowledgeDataLoss"]; ok {
		t.Errorf("acknowledgeDataLoss should be absent in a dry-run invocation")
	}
}

func TestRestoreStatus(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"state":"running"}`, "restore", "status", "r1")
	if code != 0 || got.method != http.MethodGet || got.path != "/v1/admin/restore/r1/status" {
		t.Errorf("restore status: code %d %s %s", code, got.method, got.path)
	}
}

func TestRestoreResumeUsesQueryParam(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"state":"resuming"}`, "restore", "resume", "r1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/restore/resume?restoreId=r1" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestRestoreConfirmLegalHoldLedger(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"state":"confirmed"}`,
		"restore", "confirm-legal-hold-ledger", "r1", "--justification", "ledger current")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/restore/r1/confirm-legal-hold-ledger" {
		t.Errorf("path: %q", got.path)
	}
	if got.body["justification"] != "ledger current" {
		t.Errorf("body: %v", got.body)
	}
}

func TestRestoreConfirmLegalHoldLedgerRequiresJustification(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "restore", "confirm-legal-hold-ledger", "r1"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("confirm-legal-hold-ledger without --justification: exit code %d, want 2", code)
	}
}

// --- unknown-subcommand usage errors for the new groups --------------

func TestUnknownBackupRestoreSubcommandsAreUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, group := range []string{"backup", "restore"} {
		code := run([]string{"--ops-server", "http://ops:8090", group, "bogus"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s bogus: exit code %d, want 2", group, code)
		}
	}
}
