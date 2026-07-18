// SPDX-License-Identifier: MIT

package opsinventory

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// backupLister is the narrow §25.11 enumeration seam the BackupSource
// consumes: the ops_backups rows the §25.4 Operations Inventory projects.
// *backup.Service satisfies it.
type backupLister interface {
	ListBackups(ctx context.Context, filter backup.BackupFilter, cursor string, limit int) (*backup.BackupPage, error)
}

// maxBackups bounds the per-status backup projection per List call. It
// matches the escalation source's ceiling so a single inventory page is
// never starved by the source fetch. Only in-flight (running / verifying)
// backups are projected, so the ceiling is generous.
const maxBackups = 1000

// BackupSource projects §25.11 backups (ops_backups rows) onto the §25.4
// Operations Inventory. Per the §25.4 Operation Kinds table a backup in
// status 'running' or 'verifying' is kind backup, and a backup in status
// 'verifying' is additionally kind backup_verification — the same
// ops_backups row satisfies both rows of the table while it is verifying.
// Without this source a running or verifying backup, and every backup
// verification, is silently absent from GET /v1/admin/operations even
// though §25.4 requires them on that surface alongside the backup's own
// status endpoint. The operationId is the canonical "backup-" prefix
// joined to the backup's natural key (its ops_backups id), so an agent can
// decode it back to GET /v1/admin/backups/{id}.
//
// spec: §25.4 (Operations Inventory, Operation Kinds table).
type BackupSource struct {
	svc        backupLister
	gatewayURL string
}

// NewBackupSource adapts a backup-enumerating service. A nil service
// yields no operations. gatewayURL is the gateway base URL the
// gateway-resident `audit` resource link is joined to (empty on the dev /
// embedded path).
func NewBackupSource(svc backupLister, gatewayURL string) *BackupSource {
	return &BackupSource{svc: svc, gatewayURL: gatewayURL}
}

// Kinds reports the backup and backup_verification kinds.
func (s *BackupSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindBackup, operations.KindBackupVerification}
}

// List projects the in-flight backups. A running backup is one kind
// backup operation; a verifying backup is both a kind backup operation
// (status IN ('running','verifying')) and a kind backup_verification
// operation (status='verifying'). A store outage returns the service
// error, which the Inventory turns into a §25.4 degradation warning. The
// Inventory applies its own status/actor filters over the merged result,
// so every in-flight backup is projected here.
func (s *BackupSource) List(ctx context.Context, _ operations.Filter) ([]operations.Operation, error) {
	if s.svc == nil {
		return nil, nil
	}
	running, err := s.svc.ListBackups(ctx, backup.BackupFilter{Status: backup.StatusRunning}, "", maxBackups)
	if err != nil {
		return nil, err
	}
	verifying, err := s.svc.ListBackups(ctx, backup.BackupFilter{Status: backup.StatusVerifying}, "", maxBackups)
	if err != nil {
		return nil, err
	}
	out := make([]operations.Operation, 0, len(running.Backups)+2*len(verifying.Backups))
	for _, b := range running.Backups {
		out = append(out, s.project(b, operations.KindBackup))
	}
	// spec: §25.4 Operation Kinds — a verifying backup appears under both
	// kind backup (running/verifying) and kind backup_verification
	// (verifying). Project the kind backup view first so a Get by the
	// shared "backup-" operationId resolves to the backup lifecycle view.
	for _, b := range verifying.Backups {
		out = append(out, s.project(b, operations.KindBackup))
		out = append(out, s.project(b, operations.KindBackupVerification))
	}
	return out, nil
}

// project maps one in-flight ops_backups row onto the canonical §25.4
// Operation envelope under the given kind. A running or verifying backup
// is in_progress (§25.4 line 1807: "backup Job running"). The resources
// block surfaces the backup's own status endpoint and its verify path.
// spec: §25.4 (Operations Inventory), §25.11 (backup API).
func (s *BackupSource) project(b backup.Backup, kind operations.Kind) operations.Operation {
	// spec: §25.4 canonical operationId = <kind-prefix>-<natural-key>; both
	// backup kinds share the "backup" prefix and the ops_backups id is the
	// natural key.
	opID := operations.KindPrefix(kind) + "-" + b.ID
	meta := map[string]any{"type": b.Type}
	if b.JobID != "" {
		meta["jobId"] = b.JobID
	}
	return operations.Operation{
		OperationID: opID,
		Kind:        kind,
		// A running or verifying backup is actively executing; §25.4 line
		// 1807 names a running backup Job as in_progress.
		Status:    operations.StatusInProgress,
		StartedBy: b.StartedBy,
		StartedAt: b.StartedAt,
		Progress:  backupProgress(b),
		Resources: map[string]string{
			"status": "GET /v1/admin/backups/" + b.ID,
			"verify": "POST /v1/admin/backups/" + b.ID + "/verify",
			"audit":  auditLink(s.gatewayURL, "operationId="+opID),
		},
		// A backup has no cancel/abort endpoint; it runs to completion or
		// fails, so it is not a cancellable in-flight operation.
		Cancellable: false,
		Metadata:    jsonMetadata(meta),
	}
}

// backupProgress projects a backup's state onto the §25.2 canonical
// progress envelope §25.4 line 358 requires an inventory backup and backup
// verification to carry. A backup dump is size-based, but the size
// estimate is not available on the ops_backups row until the Job reports
// it, so only the startedAt / lastProgressAt anchors are stamped here and
// the ETA fields are left to the Inventory's §25.2 enrichment (the
// cadence-relative stalledForSeconds and any historical_p50 baseline).
func backupProgress(b backup.Backup) *conventions.Progress {
	p := &conventions.Progress{EtaMethod: conventions.EtaNone}
	if !b.StartedAt.IsZero() {
		p.StartedAt = b.StartedAt.UTC().Format(time.RFC3339)
		// No finer-grained progress signal exists on the row; the backup has
		// at least advanced to its start, so lastProgressAt floors there.
		p.LastProgressAt = p.StartedAt
	}
	return p
}

var _ operations.Source = (*BackupSource)(nil)
