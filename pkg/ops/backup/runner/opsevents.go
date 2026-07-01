// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/events"
)

// backupEventSource is the CloudEvents source attribute the backup
// Job's operational events carry. It names the backup subsystem so an
// ops agent filtering the §25.3 buffer by source can isolate
// backup-lifecycle events. spec: §25.3 line 650 (CloudEvents source).
const backupEventSource = "//lenny.dev/ops/backup"

// emitBackupCompleted publishes the §16.6 backup_completed operational
// event for a successful run. The §25.3 line 692 payload highlights are
// the backup type, status, size, and duration. A nil emitter (Redis
// not configured for the Job) drops the event; the durable audit row
// and the ops_backups status update have already landed.
//
// spec: §25.3 lines 692; §16.6 backup_completed.
func emitBackupCompleted(ctx context.Context, em events.EventEmitter, result Result) {
	if em == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"backupId":   result.BackupID,
		"type":       result.Type,
		"status":     "completed",
		"sizeBytes":  result.SizeBytes,
		"durationMs": result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
	})
	_ = em.Emit(ctx, events.OperationalEvent{
		Source:          backupEventSource,
		Type:            events.EventBackupCompleted.CloudEventsType(),
		Subject:         "backup/" + result.BackupID,
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
}

// emitBackupFailed publishes the §16.6 backup_failed operational event
// for a failed run. The §25.3 line 694 payload highlights are the
// backup type and the error. A nil emitter drops the event; the
// §16.7 backup.failed audit row has already landed.
//
// spec: §25.3 lines 694; §16.6 backup_failed.
func emitBackupFailed(ctx context.Context, em events.EventEmitter, mode, backupID, reason string) {
	if em == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"backupId": backupID,
		"type":     mode,
		"status":   "failed",
		"error":    reason,
	})
	_ = em.Emit(ctx, events.OperationalEvent{
		Source:          backupEventSource,
		Type:            events.EventBackupFailed.CloudEventsType(),
		Subject:         "backup/" + backupID,
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}
