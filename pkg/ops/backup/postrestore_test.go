// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// recordingAudit captures the §25.11 audit events the Service emits so a
// test can assert the per-shard / reconciler / failure transitions fired.
type recordingAudit struct{ events []backup.AuditEvent }

func (r *recordingAudit) sink() backup.AuditSink {
	return func(ev backup.AuditEvent) { r.events = append(r.events, ev) }
}

func (r *recordingAudit) count(typ audit.EventType) int {
	n := 0
	for _, e := range r.events {
		if e.Type == string(typ) {
			n++
		}
	}
	return n
}

func (r *recordingAudit) last(typ audit.EventType) (backup.AuditEvent, bool) {
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Type == string(typ) {
			return r.events[i], true
		}
	}
	return backup.AuditEvent{}, false
}

// fakeErasure is a §25.11 step-6 ErasureReconciler test double.
type fakeErasure struct {
	ledgerAt  time.Time
	ledgerErr error
	subjects  []backup.ErasureSubject
	enumErr   error
	replayErr map[string]error
	replayed  []string
}

func (f *fakeErasure) LedgerLatestWriteAt(context.Context) (time.Time, error) {
	return f.ledgerAt, f.ledgerErr
}

func (f *fakeErasure) EnumerateErasures(context.Context, time.Time) ([]backup.ErasureSubject, error) {
	return f.subjects, f.enumErr
}

func (f *fakeErasure) Replay(_ context.Context, subj backup.ErasureSubject) error {
	if f.replayErr != nil {
		if e := f.replayErr[subj.SubjectID]; e != nil {
			return e
		}
	}
	f.replayed = append(f.replayed, subj.SubjectID)
	return nil
}

// fakeGateway is a §25.11 step-7 GatewayRestarter test double.
type fakeGateway struct {
	calls int
	err   error
}

func (f *fakeGateway) RestartGateway(context.Context) error {
	f.calls++
	return f.err
}

// fakeProgress captures §25.11 line 4196 operation_progressed payloads.
type fakeProgress struct{ infos []backup.RestoreProgressInfo }

func (f *fakeProgress) RestoreProgressed(_ context.Context, info backup.RestoreProgressInfo) {
	f.infos = append(f.infos, info)
}

// completionSeams bundles the optional CompleteRestore dependencies a
// test injects.
type completionSeams struct {
	audit     *recordingAudit
	erasure   backup.ErasureReconciler
	gateway   *fakeGateway
	progress  *fakeProgress
	reconcile backup.ReconcileMetrics
}

func newCompletionSvc(t *testing.T, sm completionSeams) (*backup.Service, *backup.MemStore, *backup.FakeLauncher, *backup.MemLocker) {
	t.Helper()
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	locker := backup.NewMemLocker()
	seq := 0
	cfg := backup.Config{
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
	}
	if sm.audit != nil {
		cfg.Audit = sm.audit.sink()
	}
	if sm.erasure != nil {
		cfg.Erasure = sm.erasure
	}
	if sm.gateway != nil {
		cfg.GatewayRestart = sm.gateway
	}
	if sm.progress != nil {
		cfg.Progress = sm.progress
	}
	if sm.reconcile != nil {
		cfg.Reconcile = sm.reconcile
	}
	svc, err := backup.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, launcher, locker
}

// startRunningRestore drives ExecuteRestore against an hour-old backup
// (unsafe, so acknowledgeDataLoss is supplied) to leave a restore in the
// running state with the restore:platform lock held and a Job recorded.
func startRunningRestore(t *testing.T, svc *backup.Service, store *backup.MemStore) (restoreID, jobID, preRestoreID string) {
	t.Helper()
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	res, err := svc.ExecuteRestore(context.Background(), backup.RestoreRequest{
		BackupID: b.ID, Confirm: true, AcknowledgeDataLoss: true, StartedBy: "alice",
	})
	if err != nil {
		t.Fatalf("ExecuteRestore: %v", err)
	}
	return res.RestoreID, res.JobID, res.PreRestoreBackupID
}

func assertLockHeld(t *testing.T, locker *backup.MemLocker, want bool) {
	t.Helper()
	_, held, err := locker.Holder(context.Background())
	if err != nil {
		t.Fatalf("Holder: %v", err)
	}
	if held != want {
		t.Errorf("restore:platform lock held=%v, want %v", held, want)
	}
}

// spec: §25.11 lines 4146-4149 — all shards complete, no GDPR reconciler
// configured: per-shard events fire, the gateway rolls, and the lock is
// released.
func TestCompleteRestoreHappyPathNoErasure_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	prog := &fakeProgress{}
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, progress: prog})
	restoreID, _, preRestoreID := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, []backup.ShardResult{
		{Shard: "shard-1", OK: true}, {Shard: "shard-2", OK: true},
	})
	if err != nil {
		t.Fatalf("CompleteRestore: %v", err)
	}
	if r.Status != backup.RestoreStatusCompleted {
		t.Errorf("status = %q, want completed", r.Status)
	}
	if n := rec.count(audit.EventRestoreShardCompleted); n != 2 {
		t.Errorf("restore.shard_completed count = %d, want 2", n)
	}
	if n := rec.count(audit.EventRestoreCompleted); n != 1 {
		t.Errorf("restore.completed count = %d, want 1", n)
	}
	if gw.calls != 1 {
		t.Errorf("gateway restarts = %d, want 1", gw.calls)
	}
	assertLockHeld(t, locker, false)
	if len(prog.infos) != 2 {
		t.Fatalf("operation_progressed count = %d, want 2", len(prog.infos))
	}
	if prog.infos[0].TotalSteps != 3 {
		t.Errorf("totalSteps = %d, want shardCount+1 = 3", prog.infos[0].TotalSteps)
	}
	// Pre-Restore Backup Lifecycle: the pre-restore row is expired.
	pre, err := store.GetBackup(context.Background(), preRestoreID)
	if err != nil {
		t.Fatalf("GetBackup pre-restore: %v", err)
	}
	if pre.Status != backup.StatusExpired || pre.ExpiresAt == nil {
		t.Errorf("pre-restore backup status = %q, expiresAt = %v, want expired with a timestamp", pre.Status, pre.ExpiresAt)
	}
}

// spec: §25.11 line 4146 / line 4149 — any shard failing fails the
// restore in the restore phase, holds the lock, and does not roll the
// gateway.
func TestCompleteRestoreShardFailureHoldsLock_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, []backup.ShardResult{
		{Shard: "shard-1", OK: true},
		{Shard: "shard-2", OK: false, Error: "disk full"},
	})
	if err != nil {
		t.Fatalf("CompleteRestore returned err: %v", err)
	}
	if r.Status != backup.RestoreStatusFailed {
		t.Errorf("status = %q, want failed", r.Status)
	}
	if r.FailedShard != "shard-2" {
		t.Errorf("failedShard = %q, want shard-2", r.FailedShard)
	}
	ev, ok := rec.last(audit.EventRestoreFailed)
	if !ok {
		t.Fatal("restore.failed not emitted")
	}
	if ev.Fields["failure_phase"] != backup.FailurePhaseRestore {
		t.Errorf("failure_phase = %v, want %q", ev.Fields["failure_phase"], backup.FailurePhaseRestore)
	}
	if gw.calls != 0 {
		t.Errorf("gateway restarts = %d, want 0 on shard failure", gw.calls)
	}
	assertLockHeld(t, locker, true)
}

// spec: §25.11 line 4147 — a fresh ledger lets the reconciler replay the
// enumerated subjects, suppress held subjects, and emit a single
// gdpr.backup_reconcile_completed event before the gateway rolls.
func TestCompleteRestoreErasureReplayAndSuppress_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	er := &fakeErasure{
		ledgerAt: fixedNow, // after the -1h backupTakenAt: fresh
		subjects: []backup.ErasureSubject{
			{Kind: backup.ErasureSubjectUser, TenantID: "acme", SubjectID: "alice", ReceiptAt: fixedNow.Add(-30 * time.Minute)},
			{Kind: backup.ErasureSubjectUser, TenantID: "acme", SubjectID: "bob", ReceiptAt: fixedNow.Add(-20 * time.Minute), SuppressedByHold: true},
		},
	}
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, erasure: er})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, []backup.ShardResult{{Shard: "platform", OK: true}})
	if err != nil {
		t.Fatalf("CompleteRestore: %v", err)
	}
	if r.Status != backup.RestoreStatusCompleted {
		t.Errorf("status = %q, want completed", r.Status)
	}
	if len(er.replayed) != 1 || er.replayed[0] != "alice" {
		t.Errorf("replayed = %v, want [alice] (bob is suppressed)", er.replayed)
	}
	if n := rec.count(audit.EventGDPRErasureReconciledSuppressedByHold); n != 1 {
		t.Errorf("suppressed-by-hold events = %d, want 1", n)
	}
	ev, ok := rec.last(audit.EventGDPRBackupReconcileCompleted)
	if !ok {
		t.Fatal("gdpr.backup_reconcile_completed not emitted")
	}
	if ev.Fields["reconciledCount"] != 1 || ev.Fields["suppressedCount"] != 1 {
		t.Errorf("reconcile counts = %v/%v, want 1/1", ev.Fields["reconciledCount"], ev.Fields["suppressedCount"])
	}
	if gw.calls != 1 {
		t.Errorf("gateway restarts = %d, want 1", gw.calls)
	}
	assertLockHeld(t, locker, false)
}

// spec: §25.11 line 4147 — a ledger restored in lockstep (most recent
// write <= backupTakenAt) blocks replay with gdpr.backup_reconcile_blocked,
// aborts with RESTORE_ERASURE_RECONCILE_FAILED + failure_phase
// erasure_reconcile + block_reason legal_hold_ledger_stale, holds the
// lock, and skips the gateway restart.
func TestCompleteRestoreLedgerStaleBlocks_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	er := &fakeErasure{ledgerAt: fixedNow.Add(-2 * time.Hour)} // before -1h backupTakenAt
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, erasure: er})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, nil)
	if backup.CodeOf(err) != backup.ErrCodeRestoreErasureReconcile {
		t.Fatalf("error code = %q, want RESTORE_ERASURE_RECONCILE_FAILED", backup.CodeOf(err))
	}
	if r.Status != backup.RestoreStatusFailed {
		t.Errorf("status = %q, want failed", r.Status)
	}
	if rec.count(audit.EventGDPRBackupReconcileBlocked) != 1 {
		t.Error("gdpr.backup_reconcile_blocked not emitted")
	}
	ev, ok := rec.last(audit.EventRestoreFailed)
	if !ok {
		t.Fatal("restore.failed not emitted")
	}
	if ev.Fields["failure_phase"] != backup.FailurePhaseErasureReconcile {
		t.Errorf("failure_phase = %v, want erasure_reconcile", ev.Fields["failure_phase"])
	}
	if ev.Fields["block_reason"] != backup.BlockReasonLedgerStale {
		t.Errorf("block_reason = %v, want legal_hold_ledger_stale", ev.Fields["block_reason"])
	}
	if gw.calls != 0 {
		t.Errorf("gateway restarts = %d, want 0", gw.calls)
	}
	assertLockHeld(t, locker, true)
}

// fakeReconcileMetrics records §25.11 line 4320
// lenny_backup_reconcile_blocked_total increments by reason.
type fakeReconcileMetrics struct {
	byReason map[string]int
}

func (f *fakeReconcileMetrics) ReconcileBlocked(reason string) {
	if f.byReason == nil {
		f.byReason = map[string]int{}
	}
	f.byReason[reason]++
}

// spec: §25.11 line 4320 — the ledger-stale block increments
// lenny_backup_reconcile_blocked_total{reason="legal_hold_ledger_stale"}
// so the BackupReconcileBlocked alert can fire.
func TestCompleteRestoreLedgerStaleIncrementsReconcileBlocked_spec_25_11_4320(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	er := &fakeErasure{ledgerAt: fixedNow.Add(-2 * time.Hour)}
	rm := &fakeReconcileMetrics{}
	svc, store, _, _ := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, erasure: er, reconcile: rm})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	if _, err := svc.CompleteRestore(context.Background(), restoreID, nil); backup.CodeOf(err) != backup.ErrCodeRestoreErasureReconcile {
		t.Fatalf("error code = %q, want RESTORE_ERASURE_RECONCILE_FAILED", backup.CodeOf(err))
	}
	if got := rm.byReason[backup.BlockReasonLedgerStale]; got != 1 {
		t.Errorf("reconcile-blocked increments for legal_hold_ledger_stale = %d, want 1", got)
	}
}

// spec: §25.11 line 4147 — an operator's ConfirmLegalHoldLedger watermark
// (LedgerConfirmedAt) overrides the stale-ledger gate so the reconciler
// proceeds on the next completion attempt.
func TestCompleteRestoreLedgerConfirmedOverridesGate_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	er := &fakeErasure{ledgerAt: fixedNow.Add(-2 * time.Hour)} // would be stale
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, erasure: er})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	// Operator confirmation persisted on the row.
	rs, _ := store.GetRestore(context.Background(), restoreID)
	confirmed := fixedNow
	rs.LedgerConfirmedAt = &confirmed
	if err := store.UpdateRestore(context.Background(), rs); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}

	r, err := svc.CompleteRestore(context.Background(), restoreID, nil)
	if err != nil {
		t.Fatalf("CompleteRestore: %v", err)
	}
	if r.Status != backup.RestoreStatusCompleted {
		t.Errorf("status = %q, want completed", r.Status)
	}
	if rec.count(audit.EventGDPRBackupReconcileBlocked) != 0 {
		t.Error("reconcile blocked despite operator confirmation")
	}
	if rec.count(audit.EventGDPRBackupReconcileCompleted) != 1 {
		t.Error("reconcile_completed not emitted under operator override")
	}
	assertLockHeld(t, locker, false)
}

// spec: §25.11 line 4147 — an individual replay failure aborts the
// reconciler with RESTORE_ERASURE_RECONCILE_FAILED (no block_reason) and
// holds the lock.
func TestCompleteRestoreReplayFailureAborts_spec_25_11(t *testing.T) {
	rec := &recordingAudit{}
	gw := &fakeGateway{}
	er := &fakeErasure{
		ledgerAt:  fixedNow,
		subjects:  []backup.ErasureSubject{{Kind: backup.ErasureSubjectUser, SubjectID: "carol"}},
		replayErr: map[string]error{"carol": errors.New("postgres unavailable")},
	}
	svc, store, _, locker := newCompletionSvc(t, completionSeams{audit: rec, gateway: gw, erasure: er})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, nil)
	if backup.CodeOf(err) != backup.ErrCodeRestoreErasureReconcile {
		t.Fatalf("error code = %q, want RESTORE_ERASURE_RECONCILE_FAILED", backup.CodeOf(err))
	}
	if r.Status != backup.RestoreStatusFailed {
		t.Errorf("status = %q, want failed", r.Status)
	}
	ev, _ := rec.last(audit.EventRestoreFailed)
	if ev.Fields["failure_phase"] != backup.FailurePhaseErasureReconcile {
		t.Errorf("failure_phase = %v, want erasure_reconcile", ev.Fields["failure_phase"])
	}
	if _, ok := ev.Fields["block_reason"]; ok {
		t.Error("block_reason set on a replay failure (expected only on the ledger gate)")
	}
	if gw.calls != 0 {
		t.Errorf("gateway restarts = %d, want 0", gw.calls)
	}
	assertLockHeld(t, locker, true)
}

// spec: §25.11 line 4147 — an enumeration error and a ledger-watermark
// error both surface as RESTORE_ERASURE_RECONCILE_FAILED.
func TestCompleteRestoreReconcilerInfraErrors_spec_25_11(t *testing.T) {
	cases := map[string]*fakeErasure{
		"enumeration": {ledgerAt: fixedNow, enumErr: errors.New("scan failed")},
		"ledger":      {ledgerErr: errors.New("ledger query failed")},
	}
	for name, er := range cases {
		t.Run(name, func(t *testing.T) {
			gw := &fakeGateway{}
			svc, store, _, locker := newCompletionSvc(t, completionSeams{erasure: er, gateway: gw})
			restoreID, _, _ := startRunningRestore(t, svc, store)
			_, err := svc.CompleteRestore(context.Background(), restoreID, nil)
			if backup.CodeOf(err) != backup.ErrCodeRestoreErasureReconcile {
				t.Fatalf("error code = %q, want RESTORE_ERASURE_RECONCILE_FAILED", backup.CodeOf(err))
			}
			if gw.calls != 0 {
				t.Errorf("gateway restarts = %d, want 0", gw.calls)
			}
			assertLockHeld(t, locker, true)
		})
	}
}

// spec: §25.11 lines 4148-4149 — a gateway restart that does not complete
// leaves the restore completed but the lock held and a retry marker; a
// later ReconcileRunningRestores retries the restart and releases the
// lock.
func TestCompleteRestoreGatewayRestartRetry_spec_25_11(t *testing.T) {
	gw := &fakeGateway{err: errors.New("rollout stuck")}
	svc, store, _, locker := newCompletionSvc(t, completionSeams{gateway: gw})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	r, err := svc.CompleteRestore(context.Background(), restoreID, nil)
	if err == nil {
		t.Fatal("CompleteRestore succeeded despite a gateway restart error")
	}
	if r.Status != backup.RestoreStatusCompleted {
		t.Errorf("status = %q, want completed (restore + reconcile succeeded)", r.Status)
	}
	assertLockHeld(t, locker, true) // step 8 deferred until the rollout completes

	// The rollout recovers; the completion reconciler retries steps 7-8.
	gw.err = nil
	advanced, err := svc.ReconcileRunningRestores(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRunningRestores: %v", err)
	}
	if len(advanced) != 1 || advanced[0] != restoreID {
		t.Errorf("advanced = %v, want [%s]", advanced, restoreID)
	}
	if gw.calls != 2 {
		t.Errorf("gateway restarts = %d, want 2 (initial + retry)", gw.calls)
	}
	assertLockHeld(t, locker, false)
}

// spec: §25.11 — CompleteRestore is idempotent: a second call on a
// completed-and-released restore does not re-roll the gateway.
func TestCompleteRestoreIdempotent_spec_25_11(t *testing.T) {
	gw := &fakeGateway{}
	svc, store, _, _ := newCompletionSvc(t, completionSeams{gateway: gw})
	restoreID, _, _ := startRunningRestore(t, svc, store)

	if _, err := svc.CompleteRestore(context.Background(), restoreID, nil); err != nil {
		t.Fatalf("CompleteRestore: %v", err)
	}
	if _, err := svc.CompleteRestore(context.Background(), restoreID, nil); err != nil {
		t.Fatalf("CompleteRestore second call: %v", err)
	}
	if gw.calls != 1 {
		t.Errorf("gateway restarts = %d, want 1 (idempotent)", gw.calls)
	}
}

func TestCompleteRestoreNotFound_spec_25_11(t *testing.T) {
	svc, _, _, _ := newCompletionSvc(t, completionSeams{})
	_, err := svc.CompleteRestore(context.Background(), "rst-missing", nil)
	if backup.CodeOf(err) != backup.ErrCodeRestoreNotFound {
		t.Fatalf("error code = %q, want RESTORE_NOT_FOUND", backup.CodeOf(err))
	}
}

// spec: §25.11 lines 4146-4149 — the leader-only completion reconciler
// drives a running restore whose Job has succeeded through steps 5-8,
// leaves an active Job untouched, and fails a restore whose Job failed.
func TestReconcileRunningRestores_spec_25_11(t *testing.T) {
	t.Run("succeeded job completes the restore", func(t *testing.T) {
		gw := &fakeGateway{}
		svc, store, launcher, locker := newCompletionSvc(t, completionSeams{gateway: gw})
		restoreID, jobID, _ := startRunningRestore(t, svc, store)
		launcher.SetJobStatus(jobID, backup.BackupJob{Phase: "Complete", Succeeded: 1, Active: 0})

		advanced, err := svc.ReconcileRunningRestores(context.Background())
		if err != nil {
			t.Fatalf("ReconcileRunningRestores: %v", err)
		}
		if len(advanced) != 1 || advanced[0] != restoreID {
			t.Fatalf("advanced = %v, want [%s]", advanced, restoreID)
		}
		rs, _ := store.GetRestore(context.Background(), restoreID)
		if rs.Status != backup.RestoreStatusCompleted {
			t.Errorf("status = %q, want completed", rs.Status)
		}
		assertLockHeld(t, locker, false)
	})

	t.Run("active job is left running", func(t *testing.T) {
		svc, store, _, locker := newCompletionSvc(t, completionSeams{})
		restoreID, _, _ := startRunningRestore(t, svc, store)
		// FakeLauncher reports a launched Job as Active until overridden.
		advanced, err := svc.ReconcileRunningRestores(context.Background())
		if err != nil {
			t.Fatalf("ReconcileRunningRestores: %v", err)
		}
		if len(advanced) != 0 {
			t.Errorf("advanced = %v, want none for an active Job", advanced)
		}
		rs, _ := store.GetRestore(context.Background(), restoreID)
		if rs.Status != backup.RestoreStatusRunning {
			t.Errorf("status = %q, want still running", rs.Status)
		}
		assertLockHeld(t, locker, true)
	})

	t.Run("failed job fails the restore and holds the lock", func(t *testing.T) {
		svc, store, launcher, locker := newCompletionSvc(t, completionSeams{})
		restoreID, jobID, _ := startRunningRestore(t, svc, store)
		launcher.SetJobStatus(jobID, backup.BackupJob{Phase: "Failed", Failed: 1, Active: 0})

		advanced, err := svc.ReconcileRunningRestores(context.Background())
		if err != nil {
			t.Fatalf("ReconcileRunningRestores: %v", err)
		}
		if len(advanced) != 1 {
			t.Fatalf("advanced = %v, want 1", advanced)
		}
		rs, _ := store.GetRestore(context.Background(), restoreID)
		if rs.Status != backup.RestoreStatusFailed {
			t.Errorf("status = %q, want failed", rs.Status)
		}
		assertLockHeld(t, locker, true)
	})
}
