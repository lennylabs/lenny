// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/backup/retention"
)

// pendingReconcileAge is the §25.11 reconciler threshold: an
// ops_backups row still in status:pending older than this never had
// its Kubernetes Job created, so the reconciler marks it failed.
const pendingReconcileAge = 2 * time.Minute

// preRestoreRetainDaysDefault is the §25.11 backups.retention.-
// preRestoreRetainDays default: a pre-restore safety backup is cleaned
// after this many days, independently of the regular retention policy.
const preRestoreRetainDaysDefault = 7

// Config assembles a §25.11 Service.
type Config struct {
	// Store is the durable backup state. Required.
	Store Store
	// Launcher creates the §25.11 backup/restore Kubernetes Jobs.
	// Required.
	Launcher JobLauncher
	// Locker is the §25.11 restore:platform remediation lock. Required
	// for restore operations; CreateBackup and the read paths do not use
	// it.
	Locker RestoreLocker
	// PlatformVersion is the running platform version, recorded on each
	// backup for restore-compatibility checks and compared against a
	// backup's version on restore.
	PlatformVersion string
	// SchemaVersion is the running Postgres schema version, recorded on
	// each backup.
	SchemaVersion int
	// PreRestoreRetainDays overrides the pre-restore retention window; a
	// zero value uses the §25.11 default of 7 days.
	PreRestoreRetainDays int
	// Now supplies the current time; nil uses time.Now in UTC.
	Now func() time.Time
	// NewID generates a backup/restore ID; nil uses a random hex id.
	NewID func(prefix string) string
}

// Service is the production §25.11 BackupService. It orchestrates
// Kubernetes Jobs through a JobLauncher and tracks their state in a
// Store; it does not run backups in-process.
type Service struct {
	store    Store
	launcher JobLauncher
	locker   RestoreLocker
	platVer  string
	schemaV  int
	preDays  int
	now      func() time.Time
	newID    func(prefix string) string
}

var _ BackupService = (*Service)(nil)

// NewService builds a §25.11 Service from cfg. It returns an error when
// a required dependency is missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("backup: Service requires a Store")
	}
	if cfg.Launcher == nil {
		return nil, fmt.Errorf("backup: Service requires a JobLauncher")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	preDays := cfg.PreRestoreRetainDays
	if preDays <= 0 {
		preDays = preRestoreRetainDaysDefault
	}
	return &Service{
		store:    cfg.Store,
		launcher: cfg.Launcher,
		locker:   cfg.Locker,
		platVer:  cfg.PlatformVersion,
		schemaV:  cfg.SchemaVersion,
		preDays:  preDays,
		now:      now,
		newID:    newID,
	}, nil
}

// CreateBackup triggers an on-demand backup. It implements the §25.11
// Insert-Before-Job creation sequence: the ops_backups row is inserted
// with status:pending before any Kubernetes Job is created, so a
// failed insert leaves no orphaned Job, and a failed Job launch leaves
// a pending row the reconciler later fails. The §25.4 confirm gate is
// enforced for a full backup in production.
func (s *Service) CreateBackup(ctx context.Context, req BackupRequest) (*Backup, error) {
	t := Type(req.Type)
	if !ValidType(req.Type) {
		return nil, codedError(ErrCodeRestoreIncompatible, "unknown backup type %q", req.Type)
	}
	if RequiresConfirm(t, req.Production) && !req.Confirm {
		return nil, codedError(ErrCodeRestoreRequiresConfirm,
			"a full backup in production requires confirm:true")
	}

	startedBy := req.StartedBy
	if startedBy == "" {
		startedBy = scheduler
	}
	now := s.now()
	b := Backup{
		ID:              s.newID("bkp"),
		Type:            req.Type,
		Status:          StatusPending,
		StartedAt:       now,
		Components:      componentsFor(t),
		StartedBy:       startedBy,
		OperationID:     req.OperationID,
		PlatformVersion: s.platVer,
		SchemaVersion:   s.schemaV,
	}

	// Step 1: insert the row. A failure here returns to the caller with
	// no Job created.
	if err := s.store.InsertBackup(ctx, b); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "record backup: %v", err)
	}

	// Step 2: create the Kubernetes Job, annotated with the backup id.
	launched, err := s.launcher.Launch(ctx, JobSpec{
		Kind:       JobBackup,
		BackupID:   b.ID,
		BackupType: req.Type,
	})
	if err != nil {
		// The Job was never created. The reconciler will fail the pending
		// row after pendingReconcileAge; report the synchronous error now.
		return nil, codedError(ErrCodeJobCreationFailed, "create backup Job: %v", err)
	}

	// Step 3: the Job was accepted — move the row to running with its
	// job_id.
	b.JobID = launched.JobID
	b.Status = StatusRunning
	if err := s.store.UpdateBackup(ctx, b); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "record backup job: %v", err)
	}
	return &b, nil
}

// ListBackups returns a page of backups matching filter. The cursor is
// the ID of the last backup of the previous page; results are ordered
// newest-first.
func (s *Service) ListBackups(ctx context.Context, filter BackupFilter, cursor string, limit int) (*BackupPage, error) {
	if limit <= 0 {
		limit = 50
	}
	all, err := s.store.ListBackups(ctx, filter)
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "list backups: %v", err)
	}
	start := 0
	if cursor != "" {
		for i, b := range all {
			if b.ID == cursor {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	hasMore := end < len(all)
	if end > len(all) {
		end = len(all)
	}
	page := all[start:end]
	out := &BackupPage{Backups: page, HasMore: hasMore}
	if hasMore && len(page) > 0 {
		out.NextCursor = page[len(page)-1].ID
	}
	return out, nil
}

// GetBackup returns one backup's full record.
func (s *Service) GetBackup(ctx context.Context, id string) (*Backup, error) {
	b, err := s.store.GetBackup(ctx, id)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup %q", id)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup: %v", err)
	}
	return &b, nil
}

// VerifyBackup creates a §25.11 verification Job for a backup:
// download the archive, validate the SHA-256 checksum, and run
// pg_restore --list. The backup row moves to status:verifying while the
// Job runs.
func (s *Service) VerifyBackup(ctx context.Context, id string) (*BackupVerification, error) {
	b, err := s.store.GetBackup(ctx, id)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup %q", id)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup: %v", err)
	}
	launched, err := s.launcher.Launch(ctx, JobSpec{
		Kind:       JobVerify,
		BackupID:   b.ID,
		BackupType: b.Type,
	})
	if err != nil {
		return nil, codedError(ErrCodeJobCreationFailed, "create verify Job: %v", err)
	}
	b.Status = StatusVerifying
	if err := s.store.UpdateBackup(ctx, b); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "record verify job: %v", err)
	}
	return &BackupVerification{
		BackupID: b.ID,
		JobID:    launched.JobID,
		Status:   StatusVerifying,
	}, nil
}

// GetJob returns the Kubernetes Job status for a running backup or
// restore operation.
func (s *Service) GetJob(ctx context.Context, jobID string) (*BackupJob, error) {
	j, err := s.launcher.JobStatus(ctx, jobID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup Job %q", jobID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup Job: %v", err)
	}
	return &j, nil
}

// GetSchedule returns the current §25.11 backup schedule.
func (s *Service) GetSchedule(ctx context.Context) (*BackupSchedule, error) {
	sched, err := s.store.GetSchedule(ctx)
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read schedule: %v", err)
	}
	return &sched, nil
}

// UpdateSchedule validates and persists a new §25.11 backup schedule.
// A malformed cron expression is rejected before the row is written.
func (s *Service) UpdateSchedule(ctx context.Context, schedule BackupSchedule) (*BackupSchedule, error) {
	if err := ValidateSchedule(schedule.schedule()); err != nil {
		return nil, codedError(ErrCodeRestoreIncompatible, "invalid schedule: %v", err)
	}
	if err := s.store.PutSchedule(ctx, schedule); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "write schedule: %v", err)
	}
	return &schedule, nil
}

// GetPolicy returns the current §25.11 retention policy.
func (s *Service) GetPolicy(ctx context.Context) (*RetentionPolicy, error) {
	p, err := s.store.GetPolicy(ctx)
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read policy: %v", err)
	}
	return &p, nil
}

// UpdatePolicy validates and persists a new §25.11 retention policy. A
// policy that would retain no backups is rejected.
func (s *Service) UpdatePolicy(ctx context.Context, policy RetentionPolicy) (*RetentionPolicy, error) {
	if err := ValidateRetentionPolicy(policy); err != nil {
		return nil, codedError(ErrCodeRestoreIncompatible, "invalid retention policy: %v", err)
	}
	if err := s.store.PutPolicy(ctx, policy); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "write policy: %v", err)
	}
	return &policy, nil
}

// PreviewRestore analyzes a restore's impact without executing it. The
// preview reports version compatibility and the artifact-replication
// lag the operator weighs when choosing a restore point.
func (s *Service) PreviewRestore(ctx context.Context, backupID string) (*RestorePreview, error) {
	b, err := s.store.GetBackup(ctx, backupID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup %q", backupID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup: %v", err)
	}
	compatible, reason := s.versionCompatible(b)
	preview := &RestorePreview{
		BackupID:          b.ID,
		BackupVersion:     b.PlatformVersion,
		CurrentVersion:    s.platVer,
		Compatible:        compatible,
		AffectedResources: []string{"postgres", "platform-config"},
		EstimatedDowntime: "PT15M",
		RequiresFullStop:  true,
		Warnings:          []string{},
	}
	if !compatible {
		preview.IncompatibleReason = reason
	}
	if b.SchemaVersion != 0 && b.SchemaVersion < s.schemaV {
		preview.Warnings = append(preview.Warnings,
			fmt.Sprintf("schema forward-migration required: backup is at version %d, current is %d",
				b.SchemaVersion, s.schemaV))
	}
	return preview, nil
}

// SafetyCheckRestore compares a backup against current state to
// estimate data loss. §25.11 reports safe:true only when the backup is
// recent (< 5 minutes old) or the platform has been idle; in practice
// most restores are not safe and require an explicit acknowledgement.
func (s *Service) SafetyCheckRestore(ctx context.Context, backupID string) (*RestoreSafetyCheck, error) {
	b, err := s.store.GetBackup(ctx, backupID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup %q", backupID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup: %v", err)
	}
	now := s.now()
	takenAt := b.StartedAt
	if b.CompletedAt != nil {
		takenAt = *b.CompletedAt
	}
	// §25.11: a backup younger than 5 minutes is treated as a safe
	// restore point — negligible data could have been written since.
	safe := now.Sub(takenAt) < 5*time.Minute
	compatible, _ := s.versionCompatible(b)
	check := &RestoreSafetyCheck{
		BackupID:      b.ID,
		BackupTakenAt: takenAt,
		CurrentTime:   now,
		Safe:          safe,
		Compatibility: RestoreCompat{
			BackupSchemaVersion:     b.SchemaVersion,
			CurrentSchemaVersion:    s.schemaV,
			BackupPlatformVersion:   b.PlatformVersion,
			CurrentPlatformVersion:  s.platVer,
			Compatible:              compatible,
			Warnings:                []string{},
			SchemaMigrationsBetween: []string{},
		},
		DataLossEstimate: DataLossEstimate{TablesWithDivergence: []string{}},
	}
	if safe {
		check.RecommendedAction = "restore is safe; execute"
	} else {
		check.RecommendedAction = "review and acknowledgeDataLoss, then execute"
	}
	return check, nil
}

// ExecuteRestore runs a §25.11 restore. It is a destructive operation:
// confirm:true is required (a dry-run preview is returned otherwise),
// and acknowledgeDataLoss:true is required when the safety check
// reports the restore is not safe. The restore takes the
// restore:platform remediation lock; a competing restore fails with
// REMEDIATION_LOCK_CONFLICT. A pre-restore safety backup is created
// before the restore Job runs.
func (s *Service) ExecuteRestore(ctx context.Context, req RestoreRequest) (*RestoreResult, error) {
	if s.locker == nil {
		return nil, codedError(ErrCodeStorageUnreachable, "restore lock manager is not configured")
	}
	b, err := s.store.GetBackup(ctx, req.BackupID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeBackupNotFound, "no backup %q", req.BackupID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read backup: %v", err)
	}

	// §25.4 dry-run/confirm gate: without confirm, return the preview.
	if !req.Confirm {
		preview, err := s.PreviewRestore(ctx, req.BackupID)
		if err != nil {
			return nil, err
		}
		return &RestoreResult{BackupID: req.BackupID, DryRun: true, Preview: preview}, nil
	}

	// §25.11 pre-validate: a non-safe restore needs acknowledgeDataLoss.
	check, err := s.SafetyCheckRestore(ctx, req.BackupID)
	if err != nil {
		return nil, err
	}
	if !check.Safe && !req.AcknowledgeDataLoss {
		return nil, codedError(ErrCodeRestoreAcknowledge,
			"the safety check reports data loss; resubmit with acknowledgeDataLoss:true")
	}
	if compatible, reason := s.versionCompatible(b); !compatible {
		return nil, codedError(ErrCodeRestoreIncompatible, "%s", reason)
	}

	owner := req.StartedBy
	if owner == "" {
		owner = scheduler
	}
	// §25.11 step 2: acquire the restore:platform lock. A held lock from
	// another operator fails with REMEDIATION_LOCK_CONFLICT.
	if err := s.locker.Acquire(ctx, owner); err != nil {
		return nil, err
	}

	// §25.11 step 3: create the pre-restore safety backup.
	preRestore, err := s.createPreRestoreBackup(ctx, owner)
	if err != nil {
		// Release the lock — the restore never started.
		_ = s.locker.Release(ctx)
		return nil, err
	}

	// §25.11 step 4: create the restore Job and the ops_restore_state
	// row.
	now := s.now()
	restore := RestoreState{
		ID:                 s.newID("rst"),
		BackupID:           req.BackupID,
		StartedAt:          now,
		Status:             RestoreStatusRunning,
		ShardStates:        map[string]ShardState{},
		StartedBy:          owner,
		OperationID:        req.OperationID,
		PreRestoreBackupID: preRestore.ID,
	}
	if err := s.store.InsertRestore(ctx, restore); err != nil {
		_ = s.locker.Release(ctx)
		return nil, codedError(ErrCodeStorageUnreachable, "record restore: %v", err)
	}
	launched, err := s.launcher.Launch(ctx, JobSpec{
		Kind:       JobRestore,
		BackupID:   req.BackupID,
		BackupType: b.Type,
		RestoreID:  restore.ID,
	})
	if err != nil {
		// §25.11: a failed restore keeps the lock held for the operator.
		restore.Status = RestoreStatusFailed
		restore.Error = "BACKUP_JOB_CREATION_FAILED"
		_ = s.store.UpdateRestore(ctx, restore)
		return nil, codedError(ErrCodeJobCreationFailed, "create restore Job: %v", err)
	}
	return &RestoreResult{
		RestoreID:          restore.ID,
		BackupID:           req.BackupID,
		Status:             RestoreStatusRunning,
		JobID:              launched.JobID,
		PreRestoreBackupID: preRestore.ID,
	}, nil
}

// GetRestoreStatus returns the per-shard status of an in-flight or
// completed restore.
func (s *Service) GetRestoreStatus(ctx context.Context, restoreID string) (*RestoreState, error) {
	r, err := s.store.GetRestore(ctx, restoreID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeRestoreNotFound, "no restore %q", restoreID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read restore: %v", err)
	}
	return &r, nil
}

// ResumeRestore re-creates the restore Job for a partially-completed
// restore, restoring only the shards that have not completed. §25.11
// requires the caller to be the current holder of the restore:platform
// lock; a released or stolen lock fails with RESTORE_LOCK_REQUIRED.
func (s *Service) ResumeRestore(ctx context.Context, restoreID string) (*RestoreResult, error) {
	if s.locker == nil {
		return nil, codedError(ErrCodeStorageUnreachable, "restore lock manager is not configured")
	}
	r, err := s.store.GetRestore(ctx, restoreID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeRestoreNotFound, "no restore %q", restoreID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read restore: %v", err)
	}
	// §25.11: resume requires the caller to hold the restore:platform
	// lock. A released or expired lock fails with RESTORE_LOCK_REQUIRED.
	_, held, err := s.locker.Holder(ctx)
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read restore lock: %v", err)
	}
	if !held {
		return nil, codedError(ErrCodeRestoreLockRequired,
			"the restore:platform lock is not held; re-acquire it before resuming")
	}
	launched, err := s.launcher.Launch(ctx, JobSpec{
		Kind:      JobRestore,
		BackupID:  r.BackupID,
		RestoreID: r.ID,
	})
	if err != nil {
		return nil, codedError(ErrCodeJobCreationFailed, "create resume Job: %v", err)
	}
	r.Status = RestoreStatusRunning
	r.FailedShard = ""
	r.Error = ""
	if err := s.store.UpdateRestore(ctx, r); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "record resume: %v", err)
	}
	return &RestoreResult{
		RestoreID:          r.ID,
		BackupID:           r.BackupID,
		Status:             RestoreStatusRunning,
		JobID:              launched.JobID,
		PreRestoreBackupID: r.PreRestoreBackupID,
	}, nil
}

// ConfirmLegalHoldLedger records the §12.8 / §25.11 platform-admin
// confirmation that the legal-hold ledger is current after a
// gdpr.backup_reconcile_blocked stall. The synthetic watermark is
// persisted on the ops_restore_state row so the post-restore reconciler
// accepts it as ledgerLatestWriteAt on the next ResumeRestore. The
// caller's identity and justification are recorded on the row. The
// emission of the §11.7 legal_hold.ledger_confirmed_current_at audit
// event is the caller's responsibility — opsserver wraps this method
// with the audit hook so the service layer remains free of an audit
// dependency.
//
// Preconditions: the restore row must exist and be in status "failed"
// (the only state in which a reconciler block could have surfaced); a
// non-failed restore returns RESTORE_NOT_FAILED. An empty justification
// returns JUSTIFICATION_REQUIRED.
func (s *Service) ConfirmLegalHoldLedger(ctx context.Context, restoreID, justification, caller string) (*RestoreState, error) {
	if justification == "" {
		return nil, codedError(ErrCodeJustificationRequired,
			"§25.11 confirm-legal-hold-ledger requires a justification")
	}
	r, err := s.store.GetRestore(ctx, restoreID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeRestoreNotFound, "no restore %q", restoreID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read restore: %v", err)
	}
	if r.Status != RestoreStatusFailed {
		return nil, codedError(ErrCodeRestoreNotFailed,
			"restore %q is in status %q; confirm-legal-hold-ledger applies only to a failed restore",
			restoreID, r.Status)
	}
	now := s.now()
	r.LedgerConfirmedAt = &now
	r.LedgerConfirmedBy = caller
	r.LedgerConfirmedJustification = justification
	if err := s.store.UpdateRestore(ctx, r); err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "record ledger confirmation: %v", err)
	}
	return &r, nil
}

// EnforceRetention evaluates the §25.11 retention policy against every
// backup in the store and deletes the expired ones from the store. It
// is invoked after each successful backup and on the daily 03:30 UTC
// cron. The MinIO object deletion is the Job's responsibility; this
// method records the policy decision by removing the ops_backups rows
// the policy prunes and returns the IDs it pruned.
//
// EnforceRetention reuses pkg/backup/retention.Plan for the policy
// math, so the orchestrator and the retention package share one
// implementation.
func (s *Service) EnforceRetention(ctx context.Context) ([]string, error) {
	all, err := s.store.ListBackups(ctx, BackupFilter{})
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "list backups: %v", err)
	}
	policy, err := s.store.GetPolicy(ctx)
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read policy: %v", err)
	}
	records := make([]retention.Backup, 0, len(all))
	for _, b := range all {
		// A failed or still-running backup is not a retention candidate.
		if b.Status == StatusFailed || b.Status == StatusRunning || b.Status == StatusPending {
			continue
		}
		createdAt := b.StartedAt
		if b.CompletedAt != nil {
			createdAt = *b.CompletedAt
		}
		records = append(records, retention.Backup{
			ID:        b.ID,
			Kind:      b.retentionKind(),
			CreatedAt: createdAt,
		})
	}
	_, prune := retention.Plan(records, retention.Policy{
		RetainDays:           policy.RetainDays,
		RetainCount:          policy.RetainCount,
		RetainMinFull:        policy.RetainMinFull,
		PreRestoreRetainDays: s.preDays,
	}, s.now())

	pruned := make([]string, 0, len(prune))
	for _, p := range prune {
		b, err := s.store.GetBackup(ctx, p.ID)
		if err != nil {
			continue
		}
		// §25.11 retention enforcement records the deletion on the row
		// before the Job removes the MinIO object; mark it expired.
		b.Status = StatusExpired
		expiresAt := s.now()
		b.ExpiresAt = &expiresAt
		if err := s.store.UpdateBackup(ctx, b); err != nil {
			return pruned, codedError(ErrCodeStorageUnreachable, "expire backup %q: %v", p.ID, err)
		}
		pruned = append(pruned, p.ID)
	}
	sort.Strings(pruned)
	return pruned, nil
}

// ReconcilePending implements the §25.11 reconciler step that fails
// ops_backups rows stuck in status:pending: a row older than two
// minutes never had its Kubernetes Job created. It is invoked by the
// lenny-ops reconciler goroutine every 60s. It returns the IDs it
// failed.
func (s *Service) ReconcilePending(ctx context.Context) ([]string, error) {
	ms, ok := s.store.(*MemStore)
	if !ok {
		// A non-MemStore reconciler is the Postgres store's responsibility;
		// it runs the equivalent query server-side.
		return nil, nil
	}
	cutoff := s.now().Add(-pendingReconcileAge)
	stale := ms.pendingOlderThan(cutoff)
	failed := make([]string, 0, len(stale))
	for _, id := range stale {
		b, err := s.store.GetBackup(ctx, id)
		if err != nil {
			continue
		}
		b.Status = StatusFailed
		b.Error = "JOB_CREATE_FAILED"
		if err := s.store.UpdateBackup(ctx, b); err != nil {
			return failed, codedError(ErrCodeStorageUnreachable, "fail pending backup %q: %v", id, err)
		}
		failed = append(failed, id)
	}
	return failed, nil
}

// createPreRestoreBackup creates the §25.11 step-3 pre-restore safety
// backup: a full backup tagged type:"pre-restore" that the retention
// policy keeps on the shorter preRestoreRetainDays schedule.
func (s *Service) createPreRestoreBackup(ctx context.Context, owner string) (Backup, error) {
	now := s.now()
	b := Backup{
		ID:              s.newID("bkp"),
		Type:            string(TypePreRestore),
		Status:          StatusPending,
		StartedAt:       now,
		Components:      componentsFor(TypeFull),
		StartedBy:       owner,
		PlatformVersion: s.platVer,
		SchemaVersion:   s.schemaV,
	}
	if err := s.store.InsertBackup(ctx, b); err != nil {
		return Backup{}, codedError(ErrCodeStorageUnreachable, "record pre-restore backup: %v", err)
	}
	launched, err := s.launcher.Launch(ctx, JobSpec{
		Kind:       JobBackup,
		BackupID:   b.ID,
		BackupType: string(TypePreRestore),
	})
	if err != nil {
		return Backup{}, codedError(ErrCodeJobCreationFailed, "create pre-restore Job: %v", err)
	}
	b.JobID = launched.JobID
	b.Status = StatusRunning
	if err := s.store.UpdateBackup(ctx, b); err != nil {
		return Backup{}, codedError(ErrCodeStorageUnreachable, "record pre-restore job: %v", err)
	}
	return b, nil
}

// versionCompatible reports whether a backup taken under its recorded
// platform version can be restored onto the running version. §25.11
// permits a forward restore (an older backup onto a newer platform);
// restoring a newer backup onto an older platform is incompatible.
func (s *Service) versionCompatible(b Backup) (bool, string) {
	if b.PlatformVersion == "" || s.platVer == "" {
		// Version unknown on one side; treat as compatible and let the
		// preview's warnings flag the uncertainty.
		return true, ""
	}
	if compareVersions(b.PlatformVersion, s.platVer) > 0 {
		return false, fmt.Sprintf(
			"backup platform version %s is newer than the current version %s; restore is forward-only",
			b.PlatformVersion, s.platVer,
		)
	}
	return true, ""
}

// componentsFor returns the §25.11 BackupComponent set a backup type
// covers. A full backup covers Postgres, config, and CRDs; a
// postgres-only backup covers Postgres; a config-only backup covers
// config and CRDs.
func componentsFor(t Type) []BackupComponent {
	comp := func(name string) BackupComponent {
		return BackupComponent{Name: name, Status: StatusPending}
	}
	switch t {
	case TypePostgres:
		return []BackupComponent{comp("postgres")}
	case TypeConfig:
		return []BackupComponent{comp("config"), comp("crds")}
	default: // full and pre-restore
		return []BackupComponent{comp("postgres"), comp("config"), comp("crds")}
	}
}

// compareVersions compares two dotted version strings (e.g. "1.5.0"
// vs "1.5.1"). It returns -1, 0, or 1. Non-numeric components compare
// as zero, which is conservative for a malformed version.
func compareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = atoiSafe(as[i])
		}
		if i < len(bs) {
			y = atoiSafe(bs[i])
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// atoiSafe parses s as a non-negative int, returning 0 for any
// non-numeric input.
func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// randomIDFallbackCounter ensures the crypto/rand-unavailable fallback
// path stays collision-free across same-nanosecond callers. spec: §25.11
// IDs are opaque but unique.
var randomIDFallbackCounter uint64

// randomID generates a backup or restore ID: a prefix and 16 random
// hex characters. When crypto/rand fails (rare; reachable in a degraded
// entropy pool), the fallback composes the current nanosecond with a
// process-local atomic counter so two near-simultaneous IDs do not
// collide on the same encoded timestamp.
func randomID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return randomIDFallback(prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}

// randomIDFallback composes the entropy-unavailable backup-ID format:
// "<prefix>-<nanosHex>-<seqHex>". Exposed for the package's internal
// test so the fallback path is exercised without forcing a crypto/rand
// failure at runtime.
func randomIDFallback(prefix string, nanos int64) string {
	seq := atomic.AddUint64(&randomIDFallbackCounter, 1)
	return prefix + "-" +
		strconv.FormatInt(nanos, 16) + "-" +
		strconv.FormatUint(seq, 16)
}
