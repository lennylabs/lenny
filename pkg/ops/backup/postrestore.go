// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// gatewayRestartPendingPrefix marks an ops_restore_state row whose
// restore and reconciler succeeded but whose §25.11 step-7 gateway
// rolling restart has not yet completed, so the step-8 lock release is
// still pending. ReconcileRunningRestores retries steps 7-8 for a
// completed restore carrying this marker.
const gatewayRestartPendingPrefix = "GATEWAY_RESTART_PENDING"

// ErasureSubject is one §12.8 erasure receipt the §25.11 step-6
// post-restore reconciler replays: a gdpr.% audit_log row whose
// completed_at is after the backup's snapshot time. Kind is one of
// ErasureSubjectUser or ErasureSubjectTenant.
type ErasureSubject struct {
	// Kind is "user" or "tenant".
	Kind string
	// TenantID scopes the erasure.
	TenantID string
	// SubjectID is the user_id (user erasure) or the tenant_id (tenant
	// erasure) the receipt enumerated.
	SubjectID string
	// ReceiptAt is the receipt's completed_at, used by the suppression
	// rule (a hold set after this timestamp vetoes replay).
	ReceiptAt time.Time
	// SuppressedByHold reports that an active legal hold whose
	// legal_hold.set post-dates the receipt vetoes replaying this erasure.
	// spec: §25.11 line 4147.
	SuppressedByHold bool
}

// §12.8 / §25.11 erasure subject kinds.
const (
	ErasureSubjectUser   = "user"
	ErasureSubjectTenant = "tenant"
)

// ErasureReconciler is the §25.11 step-6 / §12.8 post-restore GDPR
// erasure reconciler seam. The BackupService orchestration owns the
// legal-hold ledger freshness gate decision, the ready-gating, and the
// audit emission; this seam supplies the current ledger watermark and
// performs the data work against the restored databases. A production
// implementation backs it with a dedicated K8s Job (§25.11 line 4147)
// that scans the restored audit_log and replays DeleteByUser /
// DeleteByTenant via the §12.8 erasure orchestrator.
type ErasureReconciler interface {
	// LedgerLatestWriteAt returns the most recent write timestamp of the
	// *current* legal-hold ledger (§12.8 phase 2). The orchestrator
	// compares it against backupTakenAt for the freshness gate.
	LedgerLatestWriteAt(ctx context.Context) (time.Time, error)
	// EnumerateErasures returns the gdpr.% erasure receipts in the
	// restored audit_log whose completed_at is after backupTakenAt, each
	// annotated with whether an active post-receipt hold suppresses
	// replay.
	EnumerateErasures(ctx context.Context, backupTakenAt time.Time) ([]ErasureSubject, error)
	// Replay replays DeleteByUser / DeleteByTenant for one enumerated
	// subject against the restored databases in dependency order.
	Replay(ctx context.Context, subject ErasureSubject) error
}

// GatewayRestarter triggers the §25.11 step-7 rolling restart of the
// gateway Deployment after a successful restore and reconcile. A
// production implementation patches the Deployment's pod-template
// annotations and blocks until the rollout completes
// (status.updatedReplicas == status.replicas), so the orchestrator can
// release the restore:platform lock (step 8). A nil restarter means a
// single-process deployment with no gateway Deployment to roll.
//
// spec: §25.11 lines 4148-4149.
type GatewayRestarter interface {
	RestartGateway(ctx context.Context) error
}

// RestoreProgressInfo is one §25.11 line 4196 operation_progressed
// payload: a shard finished within a restore.
type RestoreProgressInfo struct {
	RestoreID       string
	Shard           string
	CompletedShards int
	// TotalSteps is the §25.11 line 4196 shard count plus one (the
	// post-restore gateway restart).
	TotalSteps int
}

// RestoreProgressEmitter emits the §25.11 line 4196 operation_progressed
// event on each shard completion. A nil emitter drops it.
type RestoreProgressEmitter interface {
	RestoreProgressed(ctx context.Context, info RestoreProgressInfo)
}

// CompleteRestore runs §25.11 Restore Execution steps 5-8 after the
// restore Job's shards have finished. shards reports each shard's
// outcome; an empty slice is treated as a single implicit "platform"
// shard that succeeded (a single-shard deployment).
//
// Step 5 records the per-shard states and emits restore.shard_completed
// + operation_progressed per completed shard, then restore.completed
// (all shards) or restore.failed with failure_phase "restore" (any
// shard failed). Step 6 runs the post-restore GDPR erasure reconciler
// under ready-gating: the legal-hold ledger freshness gate, then the
// DeleteByUser / DeleteByTenant replay. A block or failure aborts with
// RESTORE_ERASURE_RECONCILE_FAILED, sets the row to failed, emits
// restore.failed with failure_phase "erasure_reconcile", holds the lock,
// and skips step 7. On success, step 7 patches the gateway Deployment to
// roll it and step 8 releases the restore:platform lock and expires the
// pre-restore backup.
//
// spec: §25.11 lines 4146-4149.
func (s *Service) CompleteRestore(ctx context.Context, restoreID string, shards []ShardResult) (*RestoreState, error) {
	r, err := s.store.GetRestore(ctx, restoreID)
	if err == ErrNotFound {
		return nil, codedError(ErrCodeRestoreNotFound, "no restore %q", restoreID)
	}
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "read restore: %v", err)
	}

	switch r.Status {
	case RestoreStatusCompleted:
		// A completed restore whose lock is still held finished its shards
		// and reconciler but not its step-7 gateway restart; retry steps
		// 7-8. With the lock released it is fully done. The single
		// restore:platform lock means a held lock on a completed restore
		// belongs to that restore (no concurrent restore could hold it).
		if s.locker != nil {
			if _, held, herr := s.locker.Holder(ctx); herr == nil && !held {
				return &r, nil
			}
		}
		return s.finishRestoreSuccess(ctx, &r)
	case RestoreStatusFailed:
		// Terminal until ResumeRestore moves it back to running.
		return &r, nil
	}

	// §25.11 step 5: record per-shard outcomes.
	if len(shards) == 0 {
		shards = []ShardResult{{Shard: "platform", OK: true}}
	}
	now := s.now()
	if r.ShardStates == nil {
		r.ShardStates = map[string]ShardState{}
	}
	var firstFailed *ShardResult
	completedShards := 0
	totalSteps := len(shards) + 1
	for i := range shards {
		sr := shards[i]
		st := ShardState{Status: RestoreStatusCompleted, CompletedAt: &now}
		if !sr.OK {
			st.Status = RestoreStatusFailed
			st.Error = sr.Error
			if firstFailed == nil {
				firstFailed = &shards[i]
			}
			r.ShardStates[sr.Shard] = st
			continue
		}
		r.ShardStates[sr.Shard] = st
		completedShards++
		// spec: §25.11 line 4146 — per-shard restore_shard_completed event.
		s.emitAudit(AuditEvent{
			Type:      string(audit.EventRestoreShardCompleted),
			RestoreID: r.ID,
			BackupID:  r.BackupID,
			Actor:     r.StartedBy,
			Fields:    map[string]any{"shard": sr.Shard},
		})
		// spec: §25.11 line 4196 — operation_progressed fires on every
		// shard completion.
		s.emitProgress(ctx, RestoreProgressInfo{
			RestoreID:       r.ID,
			Shard:           sr.Shard,
			CompletedShards: completedShards,
			TotalSteps:      totalSteps,
		})
	}

	// Any shard failed: the restore failed in the restore phase. The lock
	// is NOT released (§25.11 line 4149 failure semantics).
	if firstFailed != nil {
		r.Status = RestoreStatusFailed
		r.FailedShard = firstFailed.Shard
		r.Error = firstFailed.Error
		if r.Error == "" {
			r.Error = "RESTORE_SHARD_FAILED"
		}
		r.CompletedAt = &now
		if err := s.store.UpdateRestore(ctx, r); err != nil {
			return &r, codedError(ErrCodeStorageUnreachable, "record restore failure: %v", err)
		}
		// spec: §25.11 line 4146 — restore_failed on any shard failure.
		s.emitAudit(AuditEvent{
			Type:      string(audit.EventRestoreFailed),
			RestoreID: r.ID,
			BackupID:  r.BackupID,
			Actor:     r.StartedBy,
			Outcome:   "failed",
			Detail:    r.Error,
			Fields: map[string]any{
				"failure_phase": FailurePhaseRestore,
				"failed_shard":  firstFailed.Shard,
			},
		})
		return &r, nil
	}

	// All shards completed (§25.11 line 4146 restore_completed).
	if err := s.store.UpdateRestore(ctx, r); err != nil {
		return &r, codedError(ErrCodeStorageUnreachable, "record restore shards: %v", err)
	}
	s.emitAudit(AuditEvent{
		Type:      string(audit.EventRestoreCompleted),
		RestoreID: r.ID,
		BackupID:  r.BackupID,
		Actor:     r.StartedBy,
		Fields:    map[string]any{"shardCount": completedShards},
	})

	// §25.11 step 6: the post-restore GDPR erasure reconciler. A block or
	// failure aborts here with the lock held and the gateway un-rolled.
	if err := s.runErasureReconcile(ctx, &r); err != nil {
		return &r, err
	}

	// §25.11 steps 7-8: gateway restart, then lock release.
	return s.finishRestoreSuccess(ctx, &r)
}

// runErasureReconcile runs the §25.11 step-6 reconciler against the
// restored databases. It returns nil when the reconciler reports success
// (or when no reconciler is configured), and a RESTORE_ERASURE_RECONCILE_-
// FAILED error after recording the failure on the restore row when the
// ledger gate blocks or a replay/enumeration step fails. spec: §25.11
// line 4147.
func (s *Service) runErasureReconcile(ctx context.Context, r *RestoreState) error {
	if s.erasure == nil {
		// GDPR erasure reconciliation is not configured for this
		// deployment (§25.11 line 992 enabled:false); step 6 is vacuously
		// satisfied and the gateway rolls without it.
		return nil
	}
	// backupTakenAt is the source backup's snapshot boundary.
	var takenAt time.Time
	if b, err := s.store.GetBackup(ctx, r.BackupID); err == nil {
		takenAt = backupTakenAt(b)
	}

	// Legal-hold ledger freshness gate (§12.8 phase 2). An operator
	// confirmation (ConfirmLegalHoldLedger) overrides the auto-derived
	// watermark so a previously-blocked restore can proceed on resume.
	if r.LedgerConfirmedAt == nil {
		ledgerAt, err := s.erasure.LedgerLatestWriteAt(ctx)
		if err != nil {
			return s.failReconcile(ctx, r, "ledger watermark unavailable: "+err.Error(), "")
		}
		if !ledgerAt.After(takenAt) {
			// The ledger was restored in lockstep and cannot be trusted to
			// reflect post-backup hold transitions. Block replay.
			s.emitAudit(AuditEvent{
				Type:      string(audit.EventGDPRBackupReconcileBlocked),
				RestoreID: r.ID,
				BackupID:  r.BackupID,
				Actor:     r.StartedBy,
				Outcome:   "failed",
				Detail:    BlockReasonLedgerStale,
				Fields: map[string]any{
					"ledgerLatestWriteAt": ledgerAt,
					"backupTakenAt":       takenAt,
				},
			})
			return s.failReconcile(ctx, r,
				"legal-hold ledger restored in lockstep; confirm ledger currency before retrying",
				BlockReasonLedgerStale)
		}
	}

	subjects, err := s.erasure.EnumerateErasures(ctx, takenAt)
	if err != nil {
		return s.failReconcile(ctx, r, "enumerate erasure receipts: "+err.Error(), "")
	}
	reconciled := make([]string, 0, len(subjects))
	suppressed := make([]string, 0)
	for _, subj := range subjects {
		if subj.SuppressedByHold {
			suppressed = append(suppressed, subj.SubjectID)
			// spec: §25.11 line 4147 — a subject under a post-receipt hold
			// is suppressed rather than replayed.
			s.emitAudit(AuditEvent{
				Type:      string(audit.EventGDPRErasureReconciledSuppressedByHold),
				RestoreID: r.ID,
				BackupID:  r.BackupID,
				Actor:     r.StartedBy,
				Fields: map[string]any{
					"kind": subj.Kind, "subjectId": subj.SubjectID, "tenantId": subj.TenantID,
				},
			})
			continue
		}
		if err := s.erasure.Replay(ctx, subj); err != nil {
			return s.failReconcile(ctx, r,
				"replay erasure for "+subj.Kind+" "+subj.SubjectID+": "+err.Error(), "")
		}
		reconciled = append(reconciled, subj.SubjectID)
	}
	// spec: §25.11 line 4147 — a single gdpr.backup_reconcile_completed
	// event carries the reconciled and suppressed subjects.
	s.emitAudit(AuditEvent{
		Type:      string(audit.EventGDPRBackupReconcileCompleted),
		RestoreID: r.ID,
		BackupID:  r.BackupID,
		Actor:     r.StartedBy,
		Fields: map[string]any{
			"reconciledSubjects": reconciled,
			"suppressedSubjects": suppressed,
			"reconciledCount":    len(reconciled),
			"suppressedCount":    len(suppressed),
		},
	})
	return nil
}

// failReconcile records a §25.11 step-6 reconciler failure on the restore
// row: status failed, the RESTORE_ERASURE_RECONCILE_FAILED error, the
// restore_failed audit event with failure_phase "erasure_reconcile" (and
// block_reason when the ledger gate fired), and returns the coded error.
// The restore:platform lock is left held (§25.11 line 4149).
func (s *Service) failReconcile(ctx context.Context, r *RestoreState, detail, blockReason string) error {
	now := s.now()
	r.Status = RestoreStatusFailed
	r.CompletedAt = &now
	r.Error = ErrCodeRestoreErasureReconcile
	_ = s.store.UpdateRestore(ctx, *r)
	fields := map[string]any{"failure_phase": FailurePhaseErasureReconcile}
	if blockReason != "" {
		fields["block_reason"] = blockReason
	}
	s.emitAudit(AuditEvent{
		Type:      string(audit.EventRestoreFailed),
		RestoreID: r.ID,
		BackupID:  r.BackupID,
		Actor:     r.StartedBy,
		Outcome:   "failed",
		Detail:    detail,
		Fields:    fields,
	})
	return codedError(ErrCodeRestoreErasureReconcile, "%s", detail)
}

// finishRestoreSuccess runs §25.11 steps 7-8 for a restore whose shards
// and reconciler succeeded: it rolls the gateway Deployment and, once the
// rollout completes, releases the restore:platform lock, expires the
// pre-restore backup, and marks the restore completed. A gateway-restart
// error leaves the restore completed but the lock held and a
// GATEWAY_RESTART_PENDING marker on the row so ReconcileRunningRestores
// retries the restart and release. It is idempotent.
func (s *Service) finishRestoreSuccess(ctx context.Context, r *RestoreState) (*RestoreState, error) {
	// §25.11 step 7: roll the gateway so it picks up the restored schema.
	if s.gatewayRoll != nil {
		if err := s.gatewayRoll.RestartGateway(ctx); err != nil {
			now := s.now()
			r.Status = RestoreStatusCompleted
			if r.CompletedAt == nil {
				r.CompletedAt = &now
			}
			r.Error = gatewayRestartPendingPrefix + ": " + err.Error()
			_ = s.store.UpdateRestore(ctx, *r)
			return r, codedError(ErrCodeStorageUnreachable, "gateway restart: %v", err)
		}
	}
	// §25.11 step 8: release the restore:platform lock.
	if s.locker != nil {
		if err := s.locker.Release(ctx); err != nil {
			return r, codedError(ErrCodeStorageUnreachable, "release restore lock: %v", err)
		}
	}
	// Pre-Restore Backup Lifecycle: the pre-restore backup's row is marked
	// expired (the retention Job removes the MinIO object). spec: §25.11
	// line 4155.
	if r.PreRestoreBackupID != "" {
		s.expirePreRestoreBackup(ctx, r.PreRestoreBackupID)
	}
	now := s.now()
	r.Status = RestoreStatusCompleted
	r.Error = ""
	if r.CompletedAt == nil {
		r.CompletedAt = &now
	}
	if err := s.store.UpdateRestore(ctx, *r); err != nil {
		return r, codedError(ErrCodeStorageUnreachable, "record restore completion: %v", err)
	}
	return r, nil
}

// expirePreRestoreBackup marks the pre-restore safety backup's ops_backups
// row expired so the retention Job removes the MinIO object. It is
// best-effort and idempotent. spec: §25.11 line 4155.
func (s *Service) expirePreRestoreBackup(ctx context.Context, id string) {
	b, err := s.store.GetBackup(ctx, id)
	if err != nil {
		return
	}
	if b.Status == StatusExpired {
		return
	}
	now := s.now()
	b.Status = StatusExpired
	b.ExpiresAt = &now
	_ = s.store.UpdateBackup(ctx, b)
}

// emitProgress hands a §25.11 line 4196 operation_progressed payload to
// the configured emitter. A nil emitter drops it.
func (s *Service) emitProgress(ctx context.Context, info RestoreProgressInfo) {
	if s.progress == nil {
		return
	}
	s.progress.RestoreProgressed(ctx, info)
}

// ReconcileRunningRestores polls every running restore's Job to
// completion and drives §25.11 steps 5-8 via CompleteRestore. A restore
// whose Job is still active is left untouched. It also retries steps 7-8
// for a completed restore whose step-7 gateway restart did not finish
// (its row carries the GATEWAY_RESTART_PENDING marker). It is leader-only
// (invoked from the lenny-ops reconcile cron) and returns the restore IDs
// it advanced. spec: §25.11 lines 4146-4149.
func (s *Service) ReconcileRunningRestores(ctx context.Context) ([]string, error) {
	advanced := make([]string, 0)

	running, err := s.store.ListRestores(ctx, RestoreFilter{Status: RestoreStatusRunning})
	if err != nil {
		return nil, codedError(ErrCodeStorageUnreachable, "list running restores: %v", err)
	}
	for _, r := range running {
		if r.JobID == "" {
			// No Job to poll (a restore launched before JobID was
			// recorded, or a degraded launch); leave it for the operator.
			continue
		}
		job, err := s.launcher.JobStatus(ctx, r.JobID)
		if err == ErrNotFound {
			continue
		}
		if err != nil {
			return advanced, codedError(ErrCodeStorageUnreachable, "read restore job %q: %v", r.JobID, err)
		}
		shards, done := shardResultsFromJob(job, r)
		if !done {
			continue
		}
		// A reconciler block or failure is an expected restore outcome, not
		// a driver error: CompleteRestore records it on the row and holds
		// the lock. The restore is still "advanced" (its status moved).
		if _, cerr := s.CompleteRestore(ctx, r.ID, shards); cerr != nil &&
			CodeOf(cerr) == ErrCodeStorageUnreachable {
			return advanced, cerr
		}
		advanced = append(advanced, r.ID)
	}

	completed, err := s.store.ListRestores(ctx, RestoreFilter{Status: RestoreStatusCompleted})
	if err != nil {
		return advanced, codedError(ErrCodeStorageUnreachable, "list completed restores: %v", err)
	}
	for _, r := range completed {
		if !strings.HasPrefix(r.Error, gatewayRestartPendingPrefix) {
			continue
		}
		if _, cerr := s.CompleteRestore(ctx, r.ID, nil); cerr == nil {
			advanced = append(advanced, r.ID)
		}
	}
	return advanced, nil
}

// shardResultsFromJob maps a restore Job's status to the per-shard
// outcomes CompleteRestore consumes. A Job with no active runners and no
// failures completed its shards; a Job reporting failures failed. The
// FakeLauncher and a production client report the same BackupJob status
// shape. When the restore row already records shard states, those are
// preserved; otherwise the whole restore is treated as a single
// "platform" shard.
func shardResultsFromJob(job BackupJob, r RestoreState) (shards []ShardResult, done bool) {
	switch {
	case job.Failed > 0:
		failedShard := "platform"
		if len(r.ShardStates) > 0 {
			// Report the first not-completed recorded shard as failed.
			for id, st := range r.ShardStates {
				if st.Status != RestoreStatusCompleted {
					failedShard = id
					break
				}
			}
		}
		return []ShardResult{{Shard: failedShard, OK: false, Error: "RESTORE_SHARD_FAILED"}}, true
	case job.Active == 0 && job.Succeeded > 0:
		if len(r.ShardStates) > 0 {
			out := make([]ShardResult, 0, len(r.ShardStates))
			for id := range r.ShardStates {
				out = append(out, ShardResult{Shard: id, OK: true})
			}
			return out, true
		}
		return []ShardResult{{Shard: "platform", OK: true}}, true
	default:
		// Still active (or a status the launcher has not populated yet).
		return nil, false
	}
}
