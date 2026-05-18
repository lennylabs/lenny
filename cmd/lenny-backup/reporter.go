// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/backup/retention"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// pgReporter is the Postgres-backed §25.11 runner.Reporter and
// runner.RetentionStore. It performs the §25.11 step-8 ops_backups
// update from inside the backup Job pod and supplies the retention-
// enforcement inputs from the ops_backups and ops_retention_policy
// tables.
type pgReporter struct {
	pool *pgxpool.Pool
}

var (
	_ runner.Reporter       = (*pgReporter)(nil)
	_ runner.RetentionStore = (*pgReporter)(nil)
)

// BackupCompleted implements runner.Reporter: the §25.11 step-8
// completion update — size, duration, checksum, storage path,
// components, and status:completed.
func (r *pgReporter) BackupCompleted(ctx context.Context, result runner.Result) error {
	components, err := json.Marshal(result.Components)
	if err != nil {
		return fmt.Errorf("marshal components: %w", err)
	}
	durationMS := result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	_, err = r.pool.Exec(ctx, `
		UPDATE ops_backups
		   SET status       = $2,
		       completed_at = $3,
		       size_bytes   = $4,
		       duration_ms  = $5,
		       storage_path = $6,
		       checksum     = $7,
		       components   = $8
		 WHERE id = $1`,
		result.BackupID, backup.StatusCompleted, result.CompletedAt,
		result.SizeBytes, durationMS, result.StoragePath, result.Checksum, components)
	if err != nil {
		return fmt.Errorf("update ops_backups row %s: %w", result.BackupID, err)
	}
	return nil
}

// BackupFailed implements runner.Reporter: it records a failed run with
// status:failed and the error message.
func (r *pgReporter) BackupFailed(ctx context.Context, backupID, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE ops_backups
		   SET status       = $2,
		       completed_at = $3,
		       error        = $4
		 WHERE id = $1`,
		backupID, backup.StatusFailed, time.Now().UTC(), errMsg)
	if err != nil {
		return fmt.Errorf("update ops_backups row %s: %w", backupID, err)
	}
	return nil
}

// RetentionInputs implements runner.RetentionStore: it reads every
// completed backup and the singleton retention policy for the §25.11
// post-backup retention enforcement.
func (r *pgReporter) RetentionInputs(ctx context.Context) ([]runner.RetentionBackup, backup.RetentionPolicy, error) {
	policy, err := r.readPolicy(ctx)
	if err != nil {
		return nil, backup.RetentionPolicy{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, type, COALESCE(completed_at, started_at), COALESCE(storage_path, '')
		  FROM ops_backups
		 WHERE status IN ($1, $2)`,
		backup.StatusCompleted, backup.StatusVerified)
	if err != nil {
		return nil, backup.RetentionPolicy{}, fmt.Errorf("query ops_backups: %w", err)
	}
	defer rows.Close()

	var backups []runner.RetentionBackup
	for rows.Next() {
		var b runner.RetentionBackup
		if err := rows.Scan(&b.ID, &b.Type, &b.CreatedAt, &b.ObjectPath); err != nil {
			return nil, backup.RetentionPolicy{}, fmt.Errorf("scan ops_backups row: %w", err)
		}
		backups = append(backups, b)
	}
	if err := rows.Err(); err != nil {
		return nil, backup.RetentionPolicy{}, fmt.Errorf("iterate ops_backups: %w", err)
	}
	return backups, policy, nil
}

// MarkExpired implements runner.RetentionStore: it records a pruned
// backup as expired, the §25.11 coordinated Postgres-then-MinIO
// retention sequence.
func (r *pgReporter) MarkExpired(ctx context.Context, backupID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE ops_backups
		   SET status     = $2,
		       expires_at = $3
		 WHERE id = $1`,
		backupID, backup.StatusExpired, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("expire ops_backups row %s: %w", backupID, err)
	}
	return nil
}

// readPolicy reads the §25.11 ops_retention_policy singleton, falling
// back to the Tier-2 defaults when the row is absent.
func (r *pgReporter) readPolicy(ctx context.Context) (backup.RetentionPolicy, error) {
	var p backup.RetentionPolicy
	err := r.pool.QueryRow(ctx, `
		SELECT retain_days, retain_count, retain_min_full
		  FROM ops_retention_policy
		 WHERE id = 'singleton'`).Scan(&p.RetainDays, &p.RetainCount, &p.RetainMinFull)
	if err == pgx.ErrNoRows {
		// §25.11 Tier-2 defaults when the policy row has not been seeded.
		return backup.RetentionPolicy{RetainDays: 30, RetainCount: 10, RetainMinFull: 3}, nil
	}
	if err != nil {
		return backup.RetentionPolicy{}, fmt.Errorf("read ops_retention_policy: %w", err)
	}
	return p, nil
}

// RunRetention runs the §25.11 retention enforcement standalone — the
// daily 03:30 UTC cron Job path that has no preceding backup. It
// computes the prune set with pkg/backup/retention.Plan, marks each
// pruned backup expired, and deletes its MinIO object.
func (r *pgReporter) RunRetention(ctx context.Context, pruner runner.Pruner, preRestoreRetainDays int) ([]string, error) {
	backups, policy, err := r.RetentionInputs(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]runner.RetentionBackup, len(backups))
	records := make([]retention.Backup, 0, len(backups))
	for _, b := range backups {
		byID[b.ID] = b
		records = append(records, retention.Backup{
			ID:        b.ID,
			Kind:      retentionKindFor(b.Type),
			CreatedAt: b.CreatedAt,
		})
	}
	if preRestoreRetainDays <= 0 {
		preRestoreRetainDays = 7
	}
	_, prune := retention.Plan(records, retention.Policy{
		RetainDays:           policy.RetainDays,
		RetainCount:          policy.RetainCount,
		RetainMinFull:        policy.RetainMinFull,
		PreRestoreRetainDays: preRestoreRetainDays,
	}, time.Now().UTC())

	pruned := make([]string, 0, len(prune))
	for _, p := range prune {
		if err := r.MarkExpired(ctx, p.ID); err != nil {
			return pruned, err
		}
		if b, ok := byID[p.ID]; ok && b.ObjectPath != "" {
			if err := pruner.DeleteBackupObject(ctx, b.ObjectPath); err != nil {
				return pruned, fmt.Errorf("delete pruned backup object %s: %w", b.ObjectPath, err)
			}
		}
		pruned = append(pruned, p.ID)
	}
	sort.Strings(pruned)
	return pruned, nil
}

// retentionKindFor maps a backup type to its pkg/backup/retention Kind.
func retentionKindFor(backupType string) retention.Kind {
	switch backupType {
	case "pre-restore":
		return retention.KindPreRestore
	case "postgres":
		return retention.KindPostgres
	default:
		return retention.KindFull
	}
}
