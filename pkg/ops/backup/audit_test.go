// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// recordingSink captures the §25.11 audit events the Service emits so a
// test can assert which transitions were audited. spec: §25.11 line 4343.
type recordingSink struct {
	mu     sync.Mutex
	events []backup.AuditEvent
}

func (r *recordingSink) sink() backup.AuditSink {
	return func(ev backup.AuditEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, ev)
	}
}

func (r *recordingSink) byType(t string) []backup.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []backup.AuditEvent
	for _, ev := range r.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

func (r *recordingSink) has(t string) bool { return len(r.byType(t)) > 0 }

// fakeDeleter records the §25.11 retention MinIO-object deletions and
// optionally fails them. spec: §25.11 lines 4108-4111.
type fakeDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (f *fakeDeleter) DeleteBackupObject(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, path)
	return nil
}

func (f *fakeDeleter) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

// newAuditedService builds a §25.11 Service over in-memory dependencies
// wired with the supplied audit sink and object deleter.
func newAuditedService(t *testing.T, sink backup.AuditSink, deleter backup.ObjectDeleter) (*backup.Service, *backup.MemStore, *backup.FakeLauncher) {
	t.Helper()
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	seq := 0
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        launcher,
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Audit:           sink,
		ObjectStore:     deleter,
		Now:             func() time.Time { return fixedNow },
		NewID: func(prefix string) string {
			seq++
			return prefix + "-" + string(rune('a'+seq-1))
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, launcher
}

// TestCreateBackupEmitsAuditEvent_spec_25_11_4343 covers the §25.11 line
// 4343 requirement that backup creation is audited.
func TestCreateBackupEmitsAuditEvent_spec_25_11_4343(t *testing.T) {
	rec := &recordingSink{}
	svc, _, _ := newAuditedService(t, rec.sink(), nil)

	b, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	evs := rec.byType(string(audit.EventBackupCreated))
	if len(evs) != 1 {
		t.Fatalf("backup.created events = %d, want 1", len(evs))
	}
	if evs[0].BackupID != b.ID || evs[0].Actor != "alice" || evs[0].Outcome != "success" {
		t.Errorf("event = %+v, want backupID=%s actor=alice outcome=success", evs[0], b.ID)
	}
	if evs[0].At.IsZero() {
		t.Error("audit event time was not stamped from the Service clock")
	}
}

// TestScheduleAndPolicyUpdatesEmitAudit_spec_25_11_4343 covers the
// schedule_updated and policy_updated audit events.
func TestScheduleAndPolicyUpdatesEmitAudit_spec_25_11_4343(t *testing.T) {
	rec := &recordingSink{}
	svc, _, _ := newAuditedService(t, rec.sink(), nil)
	ctx := context.Background()

	if _, err := svc.UpdateSchedule(ctx, backup.BackupSchedule{
		Full: "0 4 * * *", Postgres: "0 */4 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if _, err := svc.UpdatePolicy(ctx, backup.RetentionPolicy{
		RetainDays: 14, RetainCount: 5, RetainMinFull: 2,
	}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if !rec.has(string(audit.EventBackupScheduleUpdated)) {
		t.Error("backup.schedule_updated not emitted")
	}
	if !rec.has(string(audit.EventBackupPolicyUpdated)) {
		t.Error("backup.policy_updated not emitted")
	}
}

// TestRestoreLifecycleEmitsAudit_spec_25_11_4343 walks the restore
// lifecycle (preview, started, resumed, legal-hold confirmation) and
// asserts each transition is audited.
func TestRestoreLifecycleEmitsAudit_spec_25_11_4343(t *testing.T) {
	rec := &recordingSink{}
	svc, store, _ := newAuditedService(t, rec.sink(), nil)
	ctx := context.Background()

	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	if _, err := svc.PreviewRestore(ctx, b.ID); err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if !rec.has(string(audit.EventRestorePreviewGenerated)) {
		t.Error("restore.preview_generated not emitted")
	}

	res, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, StartedBy: "bob",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	started := rec.byType(string(audit.EventRestoreStarted))
	if len(started) != 1 || started[0].RestoreID != res.RestoreID || started[0].Actor != "bob" {
		t.Errorf("restore.started events = %+v, want one for %s actor=bob", started, res.RestoreID)
	}

	if _, err := svc.ResumeRestore(ctx, res.RestoreID, "bob"); err != nil {
		t.Fatalf("ResumeRestore: %v", err)
	}
	if !rec.has(string(audit.EventRestoreResumed)) {
		t.Error("restore.resumed not emitted")
	}

	// Drive the restore to failed so ConfirmLegalHoldLedger's precondition
	// holds, then assert the platform-admin confirmation is audited.
	r, err := store.GetRestore(ctx, res.RestoreID)
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	r.Status = backup.RestoreStatusFailed
	if err := store.UpdateRestore(ctx, r); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}
	if _, err := svc.ConfirmLegalHoldLedger(ctx, res.RestoreID, "ledger verified", "carol"); err != nil {
		t.Fatalf("ConfirmLegalHoldLedger: %v", err)
	}
	conf := rec.byType(string(audit.EventLegalHoldLedgerConfirmedCurrentAt))
	if len(conf) != 1 || conf[0].Actor != "carol" || conf[0].Detail != "ledger verified" {
		t.Errorf("legal_hold.ledger_confirmed_current_at events = %+v, want one actor=carol", conf)
	}
}

// restoreFailLauncher fails only the JobRestore launch so the
// orchestrator reaches its restore-failed path (the pre-restore backup
// Job still succeeds).
type restoreFailLauncher struct{ *backup.FakeLauncher }

func (l restoreFailLauncher) Launch(ctx context.Context, spec backup.JobSpec) (backup.LaunchedJob, error) {
	if spec.Kind == backup.JobRestore {
		return backup.LaunchedJob{}, errors.New("restore job launch failed")
	}
	return l.FakeLauncher.Launch(ctx, spec)
}

// TestRestoreFailureEmitsAudit_spec_25_11_4343 covers the restore.failed
// audit event when the restore Job cannot be created.
func TestRestoreFailureEmitsAudit_spec_25_11_4343(t *testing.T) {
	rec := &recordingSink{}
	store := backup.NewMemStore()
	seq := 0
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        restoreFailLauncher{backup.NewFakeLauncher()},
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		Audit:           rec.sink(),
		Now:             func() time.Time { return fixedNow },
		NewID:           func(prefix string) string { seq++; return prefix + "-" + string(rune('a'+seq-1)) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	b, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if _, err := svc.ExecuteRestore(ctx, backup.RestoreRequest{BackupID: b.ID, Confirm: true, StartedBy: "bob"}); err == nil {
		t.Fatal("ExecuteRestore: want job-creation error, got nil")
	}
	failed := rec.byType(string(audit.EventRestoreFailed))
	if len(failed) != 1 || failed[0].Outcome != "failed" {
		t.Errorf("restore.failed events = %+v, want one with outcome=failed", failed)
	}
}

// TestEnforceRetentionDeletesObjectAndEmitsAudit_spec_25_11_15 covers
// the §25.11 lines 4108-4111 requirement that retention enforcement
// deletes the MinIO object and emits backup.deleted_by_retention.
func TestEnforceRetentionDeletesObjectAndEmitsAudit_spec_25_11_15(t *testing.T) {
	rec := &recordingSink{}
	deleter := &fakeDeleter{}
	svc, store, _ := newAuditedService(t, rec.sink(), deleter)
	ctx := context.Background()

	// A tight policy plus old completed backups forces a prune.
	if _, err := svc.UpdatePolicy(ctx, backup.RetentionPolicy{RetainDays: 1, RetainCount: 10, RetainMinFull: 1}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	old := fixedNow.AddDate(0, 0, -30)
	for i, id := range []string{"bkp-old1", "bkp-old2", "bkp-old3"} {
		completed := old.Add(time.Duration(i) * time.Hour)
		if err := store.InsertBackup(ctx, backup.Backup{
			ID:          id,
			Type:        "full",
			Status:      backup.StatusCompleted,
			StartedAt:   completed,
			CompletedAt: &completed,
			StoragePath: "minio://backups/" + id + ".tar.zst",
		}); err != nil {
			t.Fatalf("InsertBackup: %v", err)
		}
	}

	pruned, err := svc.EnforceRetention(ctx)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if len(pruned) == 0 {
		t.Fatal("EnforceRetention pruned nothing; expected at least one expired backup")
	}
	// Every pruned backup's MinIO object was deleted and audited.
	if got := len(deleter.paths()); got != len(pruned) {
		t.Errorf("deleted objects = %d, want %d (one per pruned)", got, len(pruned))
	}
	if got := len(rec.byType(string(audit.EventBackupDeletedByRetention))); got != len(pruned) {
		t.Errorf("backup.deleted_by_retention events = %d, want %d", got, len(pruned))
	}
}

// TestEnforceRetentionWithoutDeleterStillEmitsAudit_spec_25_11_15
// asserts a deployment with no ObjectDeleter wired still audits the
// retention deletion (the daily Job performs the physical sweep).
func TestEnforceRetentionWithoutDeleterStillEmitsAudit_spec_25_11_15(t *testing.T) {
	rec := &recordingSink{}
	svc, store, _ := newAuditedService(t, rec.sink(), nil)
	ctx := context.Background()
	if _, err := svc.UpdatePolicy(ctx, backup.RetentionPolicy{RetainDays: 1, RetainCount: 10, RetainMinFull: 1}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	old := fixedNow.AddDate(0, 0, -30)
	for _, id := range []string{"bkp-x", "bkp-y"} {
		c := old
		if err := store.InsertBackup(ctx, backup.Backup{
			ID: id, Type: "full", Status: backup.StatusCompleted, StartedAt: c, CompletedAt: &c,
			StoragePath: "minio://backups/" + id,
		}); err != nil {
			t.Fatalf("InsertBackup: %v", err)
		}
	}
	pruned, err := svc.EnforceRetention(ctx)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if len(pruned) == 0 || !rec.has(string(audit.EventBackupDeletedByRetention)) {
		t.Errorf("pruned=%d hasDeletedEvent=%v; want a prune and a deleted_by_retention event",
			len(pruned), rec.has(string(audit.EventBackupDeletedByRetention)))
	}
}

// TestReconcileOrphanedJobs_spec_25_11_3976 covers the §25.11 lines
// 3976-3978 reconciler half that deletes Kubernetes Jobs whose
// lenny.dev/backup-id annotation has no matching ops_backups row.
func TestReconcileOrphanedJobs_spec_25_11_3976(t *testing.T) {
	rec := &recordingSink{}
	svc, _, launcher := newAuditedService(t, rec.sink(), nil)
	ctx := context.Background()

	// A normal backup: the row backs its Job, so it is not orphaned.
	kept, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	// A Job annotated with a backup id that has no ops_backups row (the
	// row insert failed after the Job was created): the orphan.
	launched, err := launcher.Launch(ctx, backup.JobSpec{Kind: backup.JobBackup, BackupID: "bkp-missing"})
	if err != nil {
		t.Fatalf("Launch orphan: %v", err)
	}
	orphanJob := launched.JobID

	deleted, err := svc.ReconcileOrphanedJobs(ctx)
	if err != nil {
		t.Fatalf("ReconcileOrphanedJobs: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != orphanJob {
		t.Fatalf("deleted = %v, want [%s]", deleted, orphanJob)
	}
	// The orphaned Job is gone; the backed Job survives.
	if _, err := launcher.JobStatus(ctx, orphanJob); err != backup.ErrNotFound {
		t.Errorf("orphaned job status err = %v, want ErrNotFound", err)
	}
	if _, err := launcher.JobStatus(ctx, kept.JobID); err != nil {
		t.Errorf("kept job status err = %v, want nil", err)
	}
}
