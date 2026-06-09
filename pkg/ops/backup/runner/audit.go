// SPDX-License-Identifier: MIT

package runner

import (
	"context"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// emitBackupAudit hands a §25.11 / §16.7 backup terminal-state event to
// the run's AuditSink. The backup lifecycle's terminal transitions
// (completed, failed, verified) happen inside the lenny-backup Job pod
// rather than in the lenny-ops Service, so the durable audit rows the
// §16.7 catalog enumerates are written here at the point of the status
// change, mirroring the lenny-ops Service.emitAudit posture. A nil sink
// drops the event (dev / no-durable-store mode); the ops_backups status
// update still lands.
//
// spec: §25.11 line 4343 (every backup transition is audited);
// §16.7 (backup.completed, backup.failed, backup.verified).
func emitBackupAudit(sink backup.AuditSink, ev backup.AuditEvent) {
	if sink == nil {
		return
	}
	if ev.Outcome == "" {
		ev.Outcome = "success"
	}
	sink(ev)
}

// recordBackupFailed records the §25.11 failed terminal state on the
// ops_backups row and emits the paired §16.7 backup.failed audit event.
// A Reporter error is ignored — the run already failed and is returning
// the originating error — but the audit row is still emitted so the
// failure is visible in the §11.7 trail.
//
// spec: §25.11 line 4343; §16.7 backup.failed.
func recordBackupFailed(ctx context.Context, cfg Config, reason string) {
	_ = cfg.Reporter.BackupFailed(ctx, cfg.BackupID, reason)
	emitBackupAudit(cfg.Audit, backup.AuditEvent{
		Type:     string(audit.EventBackupFailed),
		BackupID: cfg.BackupID,
		Outcome:  "failed",
		Detail:   reason,
	})
	// spec: §25.3 line 694 / §16.6 backup_failed — the operational event
	// paired with the §16.7 backup.failed audit row, so an ops agent
	// subscribed to the buffer observes the failure without polling the
	// admin API.
	emitBackupFailed(ctx, cfg.OpsEmitter, string(cfg.Mode), cfg.BackupID, reason)
}
