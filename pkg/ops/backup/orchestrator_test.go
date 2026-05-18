// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// fixedNow is the deterministic clock the orchestrator tests run with.
var fixedNow = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

// newTestService builds a §25.11 Service over in-memory dependencies
// with a deterministic clock and a counting ID generator.
func newTestService(t *testing.T) (*backup.Service, *backup.MemStore, *backup.FakeLauncher, *backup.MemLocker) {
	t.Helper()
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	locker := backup.NewMemLocker()
	seq := 0
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        launcher,
		Locker:          locker,
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return fixedNow },
		NewID: func(prefix string) string {
			seq++
			return prefix + "-" + string(rune('a'+seq-1))
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, launcher, locker
}

func TestCreateBackupInsertBeforeJob(t *testing.T) {
	svc, store, launcher, _ := newTestService(t)
	ctx := context.Background()

	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if b.Status != backup.StatusRunning {
		t.Errorf("status = %q, want running", b.Status)
	}
	if b.JobID == "" {
		t.Error("backup has no job id after a successful launch")
	}
	if b.StartedBy != "alice" {
		t.Errorf("startedBy = %q, want alice", b.StartedBy)
	}
	// §25.11 Insert-Before-Job: the row exists and references the Job.
	stored, err := store.GetBackup(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetBackup: %v", err)
	}
	if stored.Status != backup.StatusRunning || stored.JobID != b.JobID {
		t.Errorf("stored row = %+v, want running with job id %s", stored, b.JobID)
	}
	// One backup Job was launched, annotated with the backup id.
	specs := launcher.LaunchedSpecs()
	if len(specs) != 1 || specs[0].BackupID != b.ID || specs[0].Kind != backup.JobBackup {
		t.Errorf("launched specs = %+v, want one backup Job for %s", specs, b.ID)
	}
}

func TestCreateBackupRejectsUnknownType(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "incremental"})
	if backup.CodeOf(err) != backup.ErrCodeRestoreIncompatible {
		t.Errorf("error code = %q, want RESTORE_INCOMPATIBLE", backup.CodeOf(err))
	}
}

func TestCreateBackupConfirmGate(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	// §25.11: a full backup in production requires confirm:true.
	_, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", Production: true})
	if backup.CodeOf(err) != backup.ErrCodeRestoreRequiresConfirm {
		t.Fatalf("error code = %q, want RESTORE_REQUIRES_CONFIRM", backup.CodeOf(err))
	}
	// With confirm:true the production full backup is accepted.
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{
		Type: "full", Production: true, Confirm: true,
	}); err != nil {
		t.Fatalf("confirmed production full backup rejected: %v", err)
	}
	// A postgres backup in production needs no confirm.
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "postgres", Production: true}); err != nil {
		t.Fatalf("production postgres backup rejected: %v", err)
	}
}

func TestCreateBackupJobLaunchFailureReportsCode(t *testing.T) {
	svc, store, launcher, _ := newTestService(t)
	launcher.LaunchErr = errors.New("k8s API unreachable")
	ctx := context.Background()

	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "postgres"})
	if backup.CodeOf(err) != backup.ErrCodeJobCreationFailed {
		t.Fatalf("error code = %q, want BACKUP_JOB_CREATION_FAILED", backup.CodeOf(err))
	}
	if b != nil {
		t.Error("CreateBackup returned a backup despite the launch failure")
	}
	// §25.11: the pending row is left for the reconciler to fail; it is
	// not deleted.
	all, _ := store.ListBackups(ctx, backup.BackupFilter{})
	if len(all) != 1 || all[0].Status != backup.StatusPending {
		t.Errorf("rows = %+v, want one pending row left for the reconciler", all)
	}
}

func TestComponentsByType(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	cases := []struct {
		backupType string
		want       []string
	}{
		{"full", []string{"postgres", "config", "crds"}},
		{"postgres", []string{"postgres"}},
		{"config", []string{"config", "crds"}},
	}
	for _, tc := range cases {
		b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: tc.backupType})
		if err != nil {
			t.Fatalf("CreateBackup(%s): %v", tc.backupType, err)
		}
		var got []string
		for _, c := range b.Components {
			got = append(got, c.Name)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s components = %v, want %v", tc.backupType, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s components = %v, want %v", tc.backupType, got, tc.want)
				break
			}
		}
	}
}

func TestListBackupsPaginates(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "postgres"}); err != nil {
			t.Fatalf("CreateBackup: %v", err)
		}
	}
	first, err := svc.ListBackups(ctx, backup.BackupFilter{}, "", 2)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(first.Backups) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want 2 backups and a next cursor", first)
	}
	second, err := svc.ListBackups(ctx, backup.BackupFilter{}, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListBackups page 2: %v", err)
	}
	if len(second.Backups) != 2 || !second.HasMore {
		t.Errorf("second page = %+v, want 2 more backups", second)
	}
	// The cursor advances — no overlap between pages.
	if first.Backups[1].ID == second.Backups[0].ID {
		t.Error("the second page repeats the last backup of the first page")
	}
}

func TestListBackupsFiltersByType(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"}); err != nil {
		t.Fatalf("CreateBackup full: %v", err)
	}
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "postgres"}); err != nil {
		t.Fatalf("CreateBackup postgres: %v", err)
	}
	page, err := svc.ListBackups(ctx, backup.BackupFilter{Type: "postgres"}, "", 50)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(page.Backups) != 1 || page.Backups[0].Type != "postgres" {
		t.Errorf("filtered list = %+v, want only the postgres backup", page.Backups)
	}
}

func TestGetBackupNotFound(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.GetBackup(context.Background(), "bkp-missing")
	if backup.CodeOf(err) != backup.ErrCodeBackupNotFound {
		t.Errorf("error code = %q, want BACKUP_NOT_FOUND", backup.CodeOf(err))
	}
}

func TestVerifyBackupMovesToVerifying(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	v, err := svc.VerifyBackup(ctx, b.ID)
	if err != nil {
		t.Fatalf("VerifyBackup: %v", err)
	}
	if v.Status != backup.StatusVerifying || v.JobID == "" {
		t.Errorf("verification = %+v, want verifying with a job id", v)
	}
	stored, _ := store.GetBackup(ctx, b.ID)
	if stored.Status != backup.StatusVerifying {
		t.Errorf("backup status = %q, want verifying", stored.Status)
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	sched, err := svc.GetSchedule(ctx)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if sched.Full != "0 2 * * *" {
		t.Errorf("default full schedule = %q, want 0 2 * * *", sched.Full)
	}
	updated, err := svc.UpdateSchedule(ctx, backup.BackupSchedule{
		Full: "0 3 * * *", Postgres: "0 */4 * * *", Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if updated.Full != "0 3 * * *" {
		t.Errorf("updated full schedule = %q, want 0 3 * * *", updated.Full)
	}
	// A malformed cron expression is rejected.
	if _, err := svc.UpdateSchedule(ctx, backup.BackupSchedule{Full: "not a cron"}); err == nil {
		t.Error("UpdateSchedule accepted a malformed cron expression")
	}
}

func TestPolicyRoundTrip(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	updated, err := svc.UpdatePolicy(ctx, backup.RetentionPolicy{
		RetainDays: 90, RetainCount: 30, RetainMinFull: 7,
	})
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if updated.RetainDays != 90 {
		t.Errorf("retainDays = %d, want 90", updated.RetainDays)
	}
	// A policy that retains nothing is rejected.
	if _, err := svc.UpdatePolicy(ctx, backup.RetentionPolicy{}); err == nil {
		t.Error("UpdatePolicy accepted a zero-retention policy")
	}
}
