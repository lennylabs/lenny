// SPDX-License-Identifier: MIT

package opsinventory

import (
	"context"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// restoreLister is the narrow §25.11 enumeration seam the RestoreSource
// consumes: the ops_restore_state rows the §25.4 Operations Inventory
// projects. *backup.Service satisfies it.
type restoreLister interface {
	ListRestores(ctx context.Context, filter backup.RestoreFilter) ([]backup.RestoreState, error)
}

// RestoreSource projects §25.11 restores (ops_restore_state rows) onto the
// §25.4 Operations Inventory as kind restore. Without this source a running
// or failed restore is silently absent from GET /v1/admin/operations even
// though §25.4 requires a restore to appear on that surface alongside its
// own status endpoint. The operationId is the canonical "restore-" prefix
// joined to the restore's natural key (its ops_restore_state id), so an
// agent can decode it back to the restore status endpoint.
type RestoreSource struct {
	svc        restoreLister
	gatewayURL string
}

// NewRestoreSource adapts a restore-enumerating backup service. A nil
// service yields no operations. gatewayURL is the gateway base URL the
// gateway-resident `audit` resource link is joined to (empty on the dev /
// embedded path).
func NewRestoreSource(svc restoreLister, gatewayURL string) *RestoreSource {
	return &RestoreSource{svc: svc, gatewayURL: gatewayURL}
}

// Kinds reports the restore kind.
func (s *RestoreSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindRestore}
}

// List projects every restore. A store outage returns the service error,
// which the Inventory turns into a §25.4 degradation warning. The
// Inventory applies its own status/actor filters over the merged result,
// so an empty filter (every restore) is projected here.
func (s *RestoreSource) List(ctx context.Context, _ operations.Filter) ([]operations.Operation, error) {
	if s.svc == nil {
		return nil, nil
	}
	restores, err := s.svc.ListRestores(ctx, backup.RestoreFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]operations.Operation, 0, len(restores))
	for _, r := range restores {
		out = append(out, s.project(r))
	}
	return out, nil
}

// project maps one ops_restore_state row onto the canonical §25.4
// Operation envelope. The resources block surfaces the restore's own
// status endpoint and the restore/resume path a failed restore needs
// (§25.4 line 1811 requires a failed restore to be resolvable from the
// inventory). spec: §25.4 (Operations Inventory), §25.11 (restore API).
func (s *RestoreSource) project(r backup.RestoreState) operations.Operation {
	// spec: §25.4 canonical operationId = <kind-prefix>-<natural-key>; the
	// restore's natural key is its ops_restore_state id.
	opID := operations.KindPrefix(operations.KindRestore) + "-" + r.ID
	meta := map[string]any{"backupId": r.BackupID}
	if r.PreRestoreBackupID != "" {
		meta["preRestoreBackupId"] = r.PreRestoreBackupID
	}
	if r.FailedShard != "" {
		meta["failedShard"] = r.FailedShard
	}
	if r.Error != "" {
		meta["error"] = r.Error
	}
	return operations.Operation{
		OperationID: opID,
		Kind:        operations.KindRestore,
		Status:      restoreStatus(r.Status),
		StartedBy:   r.StartedBy,
		StartedAt:   r.StartedAt,
		Progress:    restoreProgress(r),
		Resources: map[string]string{
			"status": "GET /v1/admin/restore/" + r.ID + "/status",
			"resume": "POST /v1/admin/restore/resume?restoreId=" + r.ID,
			"audit":  auditLink(s.gatewayURL, "operationId="+opID),
		},
		// A restore has no cancel/abort endpoint; a failed one is resumed,
		// not cancelled, so it is not a cancellable in-flight operation.
		Cancellable: false,
		Metadata:    jsonMetadata(meta),
	}
}

// restoreStatus maps a §25.11 ops_restore_state status onto the §25.4
// inventory status. A running restore is in_progress; a failed restore is
// a terminal failure operators must resolve; a completed restore is
// completed. An unrecognized value is treated as in_progress so an active
// restore is never silently dropped from the default view. spec: §25.4
// lines 1807, 1811.
func restoreStatus(status string) operations.Status {
	switch status {
	case backup.RestoreStatusCompleted:
		return operations.StatusCompleted
	case backup.RestoreStatusFailed:
		return operations.StatusFailed
	case backup.RestoreStatusPaused:
		return operations.StatusPaused
	default:
		return operations.StatusInProgress
	}
}

// restoreProgress projects a restore's per-shard state onto the §25.2
// canonical progress envelope §25.4 line 358 requires an inventory restore
// to carry. totalSteps is the shard count, completedSteps the shards whose
// pg_restore finished, and currentStep the first shard still in flight. The
// ETA fields are left to the Inventory's §25.2 enrichment (which draws the
// historical_p50 baseline and the per-shard cadence).
func restoreProgress(r backup.RestoreState) *conventions.Progress {
	p := &conventions.Progress{EtaMethod: conventions.EtaNone}
	if !r.StartedAt.IsZero() {
		p.StartedAt = r.StartedAt.UTC().Format(time.RFC3339)
	}
	last := r.StartedAt
	if r.CompletedAt != nil && r.CompletedAt.After(last) {
		last = *r.CompletedAt
	}
	if len(r.ShardStates) > 0 {
		total := len(r.ShardStates)
		completed := 0
		shards := make([]string, 0, total)
		for name := range r.ShardStates {
			shards = append(shards, name)
		}
		sort.Strings(shards)
		current := ""
		for _, name := range shards {
			st := r.ShardStates[name]
			if st.Status == backup.RestoreStatusCompleted {
				completed++
			} else if current == "" {
				current = name
			}
			if st.CompletedAt != nil && st.CompletedAt.After(last) {
				last = *st.CompletedAt
			}
		}
		p.TotalSteps = &total
		p.CompletedSteps = &completed
		p.CurrentStep = current
	}
	if !last.IsZero() {
		p.LastProgressAt = last.UTC().Format(time.RFC3339)
	}
	return p
}

var _ operations.Source = (*RestoreSource)(nil)
