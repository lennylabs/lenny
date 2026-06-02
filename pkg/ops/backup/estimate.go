// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"fmt"
	"time"
)

// ReplicationLagSource reports the §25.11 ArtifactStore off-cluster
// replication state the restore preview surfaces so an operator can
// choose a Postgres restore point that minimizes orphaned artifact
// rows. The production implementation reads the
// lenny_minio_replication_lag_seconds gauge and counts artifact_store
// rows newer than the replication horizon; a Postgres-less deployment
// leaves it nil and the preview reports zero lag and zero orphans.
//
// spec: §25.11 line 4094 — "the POST /v1/admin/restore/preview response
// includes artifactReplicationLagSeconds and estimatedOrphanArtifactRows
// drawn from the current replication-lag gauge so the operator can make
// an informed choice."
type ReplicationLagSource interface {
	// ReplicationLagSeconds returns the current
	// lenny_minio_replication_lag_seconds gauge value in whole seconds.
	ReplicationLagSeconds(ctx context.Context) (int, error)
	// EstimatedOrphanArtifactRows estimates how many artifact_store rows a
	// restore to backupTakenAt would orphan: rows whose object was written
	// after the replication-target horizon (now − lag) and so are not yet
	// guaranteed to exist at the target. spec: §25.11 line 4094.
	EstimatedOrphanArtifactRows(ctx context.Context, backupTakenAt time.Time) (int, error)
}

// DataLossEstimator estimates the §25.11 data a restore to a given
// backup point would lose. The production implementation reads
// Postgres write-transaction state (pg_stat_* views or a pg_wal
// position comparison) to count mutations, sessions, and audit events
// written since the backup; a Postgres-less deployment leaves it nil
// and the safety check reports a zero estimate.
//
// spec: §25.11 line 4225 — "mutationsSinceBackup is computed from
// Postgres's write transaction logs (via pg_stat_* views or pg_wal
// position comparison …)."
type DataLossEstimator interface {
	// EstimateDataLoss compares the database state at backupTakenAt against
	// now and returns the §25.11 data-loss estimate.
	EstimateDataLoss(ctx context.Context, backupTakenAt, now time.Time) (DataLossEstimate, error)
}

// restoreDowntime constants model the §25.11 RestorePreview.-
// estimatedDowntime as a function of backup size and component count
// (line 3957). The base term covers the post-restore gateway restart
// (the +1 step in the restore progress envelope, §25.11 line 4194); the
// per-component term covers each restored unit's schema load and index
// rebuild; the throughput term scales linearly with the archive size.
const (
	restoreBaseDowntime         = 2 * time.Minute
	restorePerComponentDowntime = 1 * time.Minute
	defaultRestoreThroughputBps = int64(50) << 20 // 50 MiB/s pg_restore rate
)

// estimateDowntime computes the §25.11 RestorePreview.estimatedDowntime
// from the backup's size and component count, returning an ISO-8601
// duration string (e.g. "PT15M"). A backup with no recorded size still
// yields the base plus per-component downtime so the estimate is never a
// bare constant. spec: §25.11 line 3957.
func estimateDowntime(b Backup, throughputBps int64) string {
	d := restoreBaseDowntime + restorePerComponentDowntime*time.Duration(len(b.Components))
	if throughputBps <= 0 {
		throughputBps = defaultRestoreThroughputBps
	}
	if b.SizeBytes > 0 {
		d += time.Duration(b.SizeBytes/throughputBps) * time.Second
	}
	return iso8601Duration(d)
}

// iso8601Duration formats a non-negative duration as an ISO-8601
// duration string with hour, minute, and second components, omitting
// any zero component. A zero duration renders as "PT0S".
func iso8601Duration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	total := int64(d / time.Second)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	out := "PT"
	if h > 0 {
		out += fmt.Sprintf("%dH", h)
	}
	if m > 0 {
		out += fmt.Sprintf("%dM", m)
	}
	if s > 0 {
		out += fmt.Sprintf("%dS", s)
	}
	if out == "PT" {
		return "PT0S"
	}
	return out
}
