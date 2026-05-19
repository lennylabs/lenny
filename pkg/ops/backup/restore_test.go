// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// completedBackup creates a backup and marks it completed in the store
// at completedAt, so a restore against it sees a finished backup.
func completedBackup(t *testing.T, svc *backup.Service, store *backup.MemStore, backupType string, completedAt time.Time) *backup.Backup {
	t.Helper()
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: backupType})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	b.Status = backup.StatusCompleted
	b.CompletedAt = &completedAt
	if err := store.UpdateBackup(ctx, *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}
	return b
}

func TestExecuteRestoreDryRunWithoutConfirm(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	result, err := svc.ExecuteRestore(context.Background(), backup.RestoreRequest{BackupID: b.ID})
	if err != nil {
		t.Fatalf("ExecuteRestore dry run: %v", err)
	}
	if !result.DryRun || result.Preview == nil {
		t.Fatalf("result = %+v, want a dry-run preview", result)
	}
	if result.RestoreID != "" {
		t.Error("a dry run created a restore id")
	}
}

func TestExecuteRestoreRequiresAcknowledgeWhenUnsafe(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	// A backup completed an hour ago is not a "safe" restore point.
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	_, err := svc.ExecuteRestore(context.Background(), backup.RestoreRequest{
		BackupID: b.ID, Confirm: true,
	})
	if backup.CodeOf(err) != backup.ErrCodeRestoreAcknowledge {
		t.Fatalf("error code = %q, want RESTORE_ACKNOWLEDGE_REQUIRED", backup.CodeOf(err))
	}
}

func TestExecuteRestoreSafeBackupNeedsNoAcknowledge(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	// §25.11: a backup younger than 5 minutes is a safe restore point.
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-2*time.Minute))

	result, err := svc.ExecuteRestore(context.Background(), backup.RestoreRequest{
		BackupID: b.ID, Confirm: true,
	})
	if err != nil {
		t.Fatalf("ExecuteRestore of a safe backup: %v", err)
	}
	if result.Status != backup.RestoreStatusRunning || result.JobID == "" {
		t.Errorf("result = %+v, want a running restore with a job id", result)
	}
}

func TestExecuteRestoreCreatesPreRestoreBackup(t *testing.T) {
	svc, store, launcher, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	result, err := svc.ExecuteRestore(context.Background(), backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	if result.PreRestoreBackupID == "" {
		t.Fatal("restore created no pre-restore backup")
	}
	// §25.11 step 3: the pre-restore backup is tagged type:"pre-restore".
	pre, err := store.GetBackup(context.Background(), result.PreRestoreBackupID)
	if err != nil {
		t.Fatalf("GetBackup pre-restore: %v", err)
	}
	if pre.Type != "pre-restore" {
		t.Errorf("pre-restore backup type = %q, want pre-restore", pre.Type)
	}
	// The launcher saw a pre-restore backup Job and a restore Job.
	var sawRestore, sawPreRestore bool
	for _, s := range launcher.LaunchedSpecs() {
		if s.Kind == backup.JobRestore {
			sawRestore = true
		}
		if s.Kind == backup.JobBackup && s.BackupType == "pre-restore" {
			sawPreRestore = true
		}
	}
	if !sawRestore || !sawPreRestore {
		t.Errorf("launched specs = %+v, want a pre-restore backup Job and a restore Job",
			launcher.LaunchedSpecs())
	}
}

func TestExecuteRestoreTakesLockAndConflicts(t *testing.T) {
	svc, store, _, locker := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()

	if _, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	}); err != nil {
		t.Fatalf("first ExecuteRestore: %v", err)
	}
	// §25.11: the restore took the restore:platform lock.
	owner, held, _ := locker.Holder(ctx)
	if !held || owner != "alice" {
		t.Errorf("lock holder = (%q, %t), want alice holding it", owner, held)
	}
	// A competing restore by a different operator conflicts.
	_, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "bob",
	})
	if backup.CodeOf(err) != backup.ErrCodeRemediationLockConflict {
		t.Errorf("error code = %q, want REMEDIATION_LOCK_CONFLICT", backup.CodeOf(err))
	}
}

func TestResumeRestoreRequiresLock(t *testing.T) {
	svc, store, _, locker := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()

	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	// With the lock still held, resume succeeds.
	if _, err := svc.ResumeRestore(ctx, result.RestoreID); err != nil {
		t.Fatalf("ResumeRestore while holding the lock: %v", err)
	}
	// §25.11: once the lock is released, resume fails RESTORE_LOCK_REQUIRED.
	if err := locker.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	_, err = svc.ResumeRestore(ctx, result.RestoreID)
	if backup.CodeOf(err) != backup.ErrCodeRestoreLockRequired {
		t.Errorf("error code = %q, want RESTORE_LOCK_REQUIRED", backup.CodeOf(err))
	}
}

func TestRestoreStatusReportsState(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()

	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true,
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	state, err := svc.GetRestoreStatus(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestoreStatus: %v", err)
	}
	if state.BackupID != b.ID || state.Status != backup.RestoreStatusRunning {
		t.Errorf("restore state = %+v, want a running restore of %s", state, b.ID)
	}
	if state.PreRestoreBackupID != result.PreRestoreBackupID {
		t.Error("restore state pre-restore backup id does not match the result")
	}
}

func TestRestoreStatusNotFound(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.GetRestoreStatus(context.Background(), "rst-missing")
	if backup.CodeOf(err) != backup.ErrCodeRestoreNotFound {
		t.Errorf("error code = %q, want RESTORE_NOT_FOUND", backup.CodeOf(err))
	}
}

// spec: §12.8, §25.11
// diagnosis: confirm-legal-hold-ledger records the platform-admin watermark
// on a failed restore; the reconciler retry consumes it.
func TestConfirmLegalHoldLedgerRecordsWatermark(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()
	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	// Drive the restore into the §12.8 reconciler-blocked terminal state
	// that confirm-legal-hold-ledger is intended to recover from.
	state, err := store.GetRestore(ctx, result.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	state.Status = backup.RestoreStatusFailed
	state.Error = "gdpr.backup_reconcile_blocked: legal_hold_ledger_stale"
	if err := store.UpdateRestore(ctx, state); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}

	confirmed, err := svc.ConfirmLegalHoldLedger(ctx, result.RestoreID,
		"out-of-band ledger reapplied from external legal-hold registry", "bob")
	if err != nil {
		t.Fatalf("ConfirmLegalHoldLedger: %v", err)
	}
	if confirmed.LedgerConfirmedAt == nil || !confirmed.LedgerConfirmedAt.Equal(fixedNow) {
		t.Errorf("LedgerConfirmedAt = %v, want %v", confirmed.LedgerConfirmedAt, fixedNow)
	}
	if confirmed.LedgerConfirmedBy != "bob" {
		t.Errorf("LedgerConfirmedBy = %q, want bob", confirmed.LedgerConfirmedBy)
	}
	if confirmed.LedgerConfirmedJustification == "" {
		t.Error("LedgerConfirmedJustification must be persisted")
	}
}

// spec: §12.8, §25.11
// diagnosis: confirm-legal-hold-ledger requires a justification string.
func TestConfirmLegalHoldLedgerRequiresJustification(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()
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
	if err := store.UpdateRestore(ctx, state); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}
	_, err = svc.ConfirmLegalHoldLedger(ctx, result.RestoreID, "", "bob")
	if backup.CodeOf(err) != backup.ErrCodeJustificationRequired {
		t.Errorf("error code = %q, want JUSTIFICATION_REQUIRED", backup.CodeOf(err))
	}
}

// spec: §12.8, §25.11
// diagnosis: confirm-legal-hold-ledger refuses a still-running restore.
func TestConfirmLegalHoldLedgerRejectsRunningRestore(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	ctx := context.Background()
	result, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	_, err = svc.ConfirmLegalHoldLedger(ctx, result.RestoreID, "justification", "bob")
	if backup.CodeOf(err) != backup.ErrCodeRestoreNotFailed {
		t.Errorf("error code = %q, want RESTORE_NOT_FAILED", backup.CodeOf(err))
	}
}

// spec: §12.8, §25.11
// diagnosis: confirm-legal-hold-ledger refuses an unknown restore id.
func TestConfirmLegalHoldLedgerUnknownRestore(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.ConfirmLegalHoldLedger(context.Background(), "rst-missing",
		"justification", "bob")
	if backup.CodeOf(err) != backup.ErrCodeRestoreNotFound {
		t.Errorf("error code = %q, want RESTORE_NOT_FOUND", backup.CodeOf(err))
	}
}

func TestPreviewRestoreReportsCompatibility(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	preview, err := svc.PreviewRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	// The backup and current versions match (1.5.0) so it is compatible.
	if !preview.Compatible {
		t.Errorf("preview = %+v, want compatible", preview)
	}
	if preview.BackupVersion != "1.5.0" || preview.CurrentVersion != "1.5.0" {
		t.Errorf("preview versions = (%q, %q), want both 1.5.0",
			preview.BackupVersion, preview.CurrentVersion)
	}
}

func TestEnforceRetentionPrunesExpired(t *testing.T) {
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	// A retain-count of 2 with no min-full floor.
	if err := store.PutPolicy(context.Background(), backup.RetentionPolicy{
		RetainDays: 30, RetainCount: 2, RetainMinFull: 0,
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	svc, err := backup.NewService(backup.Config{
		Store: store, Launcher: launcher, Locker: backup.NewMemLocker(),
		Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	// Four completed postgres backups of increasing age.
	for i := 1; i <= 4; i++ {
		completed := fixedNow.AddDate(0, 0, -i)
		b := backup.Backup{
			ID:          "bkp-" + string(rune('a'+i-1)),
			Type:        "postgres",
			Status:      backup.StatusCompleted,
			StartedAt:   completed,
			CompletedAt: &completed,
		}
		if err := store.InsertBackup(ctx, b); err != nil {
			t.Fatalf("InsertBackup: %v", err)
		}
	}
	pruned, err := svc.EnforceRetention(ctx)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	// RetainCount 2: the two oldest of four are pruned.
	if len(pruned) != 2 {
		t.Fatalf("pruned %d backups, want 2", len(pruned))
	}
	for _, id := range pruned {
		b, _ := store.GetBackup(ctx, id)
		if b.Status != backup.StatusExpired {
			t.Errorf("pruned backup %s status = %q, want expired", id, b.Status)
		}
		if b.ExpiresAt == nil {
			t.Errorf("pruned backup %s has no expires_at", id)
		}
	}
}

func TestReconcilePendingFailsStaleRows(t *testing.T) {
	svc, store, launcher, _ := newTestService(t)
	ctx := context.Background()
	// A launch failure leaves a pending row.
	launcher.LaunchErr = errToReturn()
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "postgres"}); err == nil {
		t.Fatal("CreateBackup should have failed with the launcher error")
	}
	// The pending row is younger than the 2-minute threshold; not failed.
	if failed, err := svc.ReconcilePending(ctx); err != nil || len(failed) != 0 {
		t.Fatalf("ReconcilePending of a fresh pending row = (%v, %v), want none failed", failed, err)
	}
	// Age the row past the threshold by rebuilding the service with a
	// later clock.
	later := fixedNow.Add(3 * time.Minute)
	aged, err := backup.NewService(backup.Config{
		Store: store, Launcher: launcher, Locker: backup.NewMemLocker(),
		Now: func() time.Time { return later },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	failed, err := aged.ReconcilePending(ctx)
	if err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("ReconcilePending failed %d rows, want 1", len(failed))
	}
	b, _ := store.GetBackup(ctx, failed[0])
	if b.Status != backup.StatusFailed || b.Error != "JOB_CREATE_FAILED" {
		t.Errorf("reconciled row = %+v, want failed with JOB_CREATE_FAILED", b)
	}
}

// errToReturn is a helper returning a non-nil error for the launcher.
func errToReturn() error { return context.DeadlineExceeded }
