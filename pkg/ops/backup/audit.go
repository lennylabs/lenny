// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// AuditEvent is one §25.11 backup/restore audit event the Service emits
// on a state transition. The §25.11 line 4343 enumeration requires an
// audit row on every backup, restore, and retention transition; the
// Service raises one of these for each transition it owns and hands it
// to the caller-supplied AuditSink so the audit trail integrates with
// the platform's §11.7 append-only audit log without coupling the
// orchestration logic to an audit-store dependency.
//
// spec: §25.11 line 4343.
type AuditEvent struct {
	// Type is the audit.EventType value, e.g. audit.EventBackupCreated.
	Type string
	// BackupID correlates the event with an ops_backups row; empty for a
	// restore-only event.
	BackupID string
	// RestoreID correlates the event with an ops_restore_state row; empty
	// for a backup-only event.
	RestoreID string
	// Actor is the started_by / caller identity recorded on the event.
	Actor string
	// Outcome is "success" or "failed".
	Outcome string
	// Detail carries a failure reason or operator justification.
	Detail string
	// Fields carries event-specific payload (backup type, pruned count,
	// schedule, and so on).
	Fields map[string]any
	// At is the event time; the Service stamps it from its clock when the
	// caller leaves it zero.
	At time.Time
}

// AuditSink receives §25.11 backup/restore audit events. A nil sink
// drops them, which is the cold-start posture for a deployment whose
// audit-append path is not yet wired (the events still fire and are
// observable in tests; only the durable destination is absent).
type AuditSink func(AuditEvent)

// ObjectDeleter removes a backup's stored object (the MinIO archive)
// during §25.11 retention enforcement. §25.11 lines 4108-4111 require
// deletion from both MinIO and Postgres; EnforceRetention marks the
// ops_backups row expired (the Postgres half) and invokes this seam for
// the MinIO half. A nil deleter leaves the physical object for the
// daily retention Job to remove, matching the prior degraded-mode
// behavior, while the row is still expired and the
// backup.deleted_by_retention event still fires.
//
// spec: §25.11 lines 4108-4111.
type ObjectDeleter interface {
	// DeleteBackupObject removes the object at storagePath. A not-found
	// object is not an error (the object may already have been swept).
	DeleteBackupObject(ctx context.Context, storagePath string) error
}

// OrphanedJob is one §25.11 reconciler candidate: a Kubernetes Job the
// launcher created, identified by its lenny.dev/backup-id annotation.
type OrphanedJob struct {
	// JobID is the Kubernetes Job name.
	JobID string
	// BackupID is the value of the Job's lenny.dev/backup-id annotation;
	// empty when the Job carries no such annotation.
	BackupID string
}

// JobReaper is the optional §25.11 reconciler capability a JobLauncher
// implements to support orphaned-Job cleanup: a Job whose
// lenny.dev/backup-id annotation has no matching ops_backups row (its
// row insert failed after the Job was created, or the row was deleted).
// A launcher that does not implement JobReaper makes ReconcileOrphaned-
// Jobs a no-op — the Postgres store runs the equivalent cleanup query
// server-side.
//
// spec: §25.11 lines 3976-3978 ("deleting orphaned Jobs").
type JobReaper interface {
	// ListManagedJobs returns every lenny-backup Job the launcher created
	// together with its lenny.dev/backup-id annotation.
	ListManagedJobs(ctx context.Context) ([]OrphanedJob, error)
	// DeleteJob removes a Kubernetes Job by name.
	DeleteJob(ctx context.Context, jobID string) error
}

// emitAudit hands ev to the configured AuditSink, stamping the event
// time from the Service clock when the caller left it zero. A nil sink
// drops the event.
func (s *Service) emitAudit(ev AuditEvent) {
	if s.audit == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	if ev.Outcome == "" {
		ev.Outcome = "success"
	}
	s.audit(ev)
}

// auditEventTypes returns the §25.11 backup/restore audit event types
// the Service emits, used by the package test to assert every emitted
// type is catalogued in §16.7.
func auditEventTypes() []string {
	return []string{
		string(audit.EventBackupCreated),
		string(audit.EventBackupScheduleUpdated),
		string(audit.EventBackupPolicyUpdated),
		string(audit.EventBackupDeletedByRetention),
		string(audit.EventRestorePreviewGenerated),
		string(audit.EventRestoreStarted),
		string(audit.EventRestoreResumed),
		string(audit.EventRestoreShardCompleted),
		string(audit.EventRestoreCompleted),
		string(audit.EventRestoreFailed),
		string(audit.EventGDPRBackupReconcileCompleted),
		string(audit.EventGDPRBackupReconcileBlocked),
		string(audit.EventGDPRErasureReconciledSuppressedByHold),
		string(audit.EventLegalHoldLedgerConfirmedCurrentAt),
	}
}
