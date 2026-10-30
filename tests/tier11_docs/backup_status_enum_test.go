// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for proposal 0021 finding SEC-BACKUP-2: the
// documented ops_backups status enum must match the enum the code emits.
// The authoritative enum is the exported Status* constants in
// pkg/ops/backup (service.go): pending, running, completed, failed,
// verifying, verified, verification_failed, expired. Before this fix the
// §25.11 DDL comment for ops_backups.status listed only running, completed,
// failed, verified, verification_failed (omitting pending, verifying, and
// expired), and the Backup response-struct comment listed running,
// completed, failed, verifying, verified (omitting pending,
// verification_failed, and expired), so the documented lifecycle drifted
// from the states the service persists. Each test below derives the
// expected set from the code constants and asserts the spec enumerates all
// of them, so it fails against the pre-fix spec text and cannot silently
// re-drift when a status is added to or removed from the code.
//
// These tests are NOT under a build tag: they read the repository spec
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// backupStatusEnum is the authoritative ops_backups status set the code
// emits, sourced from the exported constants so the test cannot drift from
// the code. spec: §25.11 (ops_backups status enum).
var backupStatusEnum = []string{
	backup.StatusPending,
	backup.StatusRunning,
	backup.StatusCompleted,
	backup.StatusFailed,
	backup.StatusVerifying,
	backup.StatusVerified,
	backup.StatusVerificationFailed,
	backup.StatusExpired,
}

// lineContaining returns the content of the single spec line that carries
// `substr`, or "" when no such line exists. It lets each assertion scope the
// enum check to the exact DDL or struct comment rather than matching the
// value anywhere on the page.
func lineContaining(body, substr string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, substr) {
			return ln
		}
	}
	return ""
}

// TestOpsBackupsDDLStatusEnumMatchesCode pins the §25.11 ops_backups DDL
// status-column comment to the full code enum. The comment is the SQL-quoted
// form ('pending', 'running', ...), so each value is checked in single
// quotes on the DDL line to avoid matching an unrelated mention elsewhere.
//
// diagnosis: a failure means the ops_backups CREATE TABLE status comment in
// spec/25_agent-operability.md omits a status the backup service persists
// (pkg/ops/backup StatusPending/StatusVerifying/StatusExpired were the
// pre-fix omissions), so an agent reading the schema cannot enumerate the
// states a backup row can hold.
//
// spec: §25.11 (ops_backups DDL status enum)
func TestOpsBackupsDDLStatusEnumMatchesCode(t *testing.T) {
	root := repoRoot(t)
	spec := readRepoFile(t, root, "spec", "25_agent-operability.md")

	// The DDL comment sits on the ops_backups status column line, which is
	// the status line inside the CREATE TABLE ops_backups block. It is the
	// only status DDL comment that enumerates 'verification_failed', so key
	// on that to select it unambiguously.
	line := lineContaining(spec, "'verification_failed'")
	if line == "" || !strings.Contains(line, "TEXT NOT NULL") {
		t.Fatalf("spec/25 has no ops_backups status DDL comment enumerating 'verification_failed'")
	}
	for _, st := range backupStatusEnum {
		if !strings.Contains(line, "'"+st+"'") {
			t.Errorf("spec/25 ops_backups DDL status comment omits code status %q; the enum must match pkg/ops/backup StatusPending..StatusExpired", st)
		}
	}
}

// TestBackupResponseStructStatusEnumMatchesCode pins the §25.11 Backup
// response-struct Status comment to the full code enum. The comment is the
// JSON-quoted form ("pending", "running", ...), so each value is checked in
// double quotes on the struct line.
//
// diagnosis: a failure means the Backup{} Status field comment in
// spec/25_agent-operability.md omits a status the code emits (the pre-fix
// text omitted pending, verification_failed, and expired), so the documented
// API response contract understates the states a backup can report.
//
// spec: §25.11 (Backup response type status enum)
func TestBackupResponseStructStatusEnumMatchesCode(t *testing.T) {
	root := repoRoot(t)
	spec := readRepoFile(t, root, "spec", "25_agent-operability.md")

	line := lineContaining(spec, "Status        string")
	if line == "" || !strings.Contains(line, "json:\"status\"") {
		t.Fatalf("spec/25 has no Backup struct Status field comment to check")
	}
	for _, st := range backupStatusEnum {
		if !strings.Contains(line, "\""+st+"\"") {
			t.Errorf("spec/25 Backup struct Status comment omits code status %q; the enum must match pkg/ops/backup StatusPending..StatusExpired", st)
		}
	}
}

// TestBackupVerifyFlowDocumentsVerifyingTransition pins the §25.11 backup
// verification flow prose to the verifying transition the code makes
// (Service.VerifyBackup sets Status = StatusVerifying before the K8s Job
// runs, then verified/verification_failed on completion). Before this fix
// the prose jumped straight to verified/verification_failed and never
// documented the transient verifying state the Operations Inventory and the
// progress envelope both key on.
//
// diagnosis: a failure means the §25.11 Backup Verification prose no longer
// documents that a verify sets the ops_backups row to "verifying", so the
// documented lifecycle skips the transient state VerifyBackup persists and
// the Operations Inventory backup_verification row queries.
//
// spec: §25.11 (backup verification status transition)
func TestBackupVerifyFlowDocumentsVerifyingTransition(t *testing.T) {
	root := repoRoot(t)
	spec := readRepoFile(t, root, "spec", "25_agent-operability.md")

	verifySection := section(spec, "Backup Verification")
	if verifySection == "" {
		t.Fatalf("spec/25 has no Backup Verification section")
	}
	if !strings.Contains(verifySection, "\""+backup.StatusVerifying+"\"") {
		t.Errorf("spec/25 Backup Verification prose does not document the %q status transition; VerifyBackup sets it before the K8s Job runs", backup.StatusVerifying)
	}
	// The terminal states must remain documented alongside the transient one.
	for _, st := range []string{backup.StatusVerified, backup.StatusVerificationFailed} {
		if !strings.Contains(verifySection, "\""+st+"\"") {
			t.Errorf("spec/25 Backup Verification prose omits terminal status %q", st)
		}
	}
}
