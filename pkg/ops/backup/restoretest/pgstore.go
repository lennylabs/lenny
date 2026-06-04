// SPDX-License-Identifier: MIT

package restoretest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres-backed §25.11 Test Restore result Store,
// reading and writing ops_restore_test_results (migration 0136). The
// lenny-backup binary Records from inside the Job pod; lenny-ops reads
// Latest and TotalArtifactMissing on each metric scrape.
type PGStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PGStore)(nil)

// NewPGStore returns a Postgres-backed restore-test result store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// Record implements Store.
func (s *PGStore) Record(ctx context.Context, r Result) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_restore_test_results (
			id, backup_id, backup_type, started_at, completed_at, success,
			duration_ms, artifact_checked, artifact_sampled, artifact_present,
			artifact_missing, artifact_success_rate, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		r.ID, r.BackupID, r.BackupType, r.StartedAt, r.CompletedAt, r.Success,
		r.CompletedAt.Sub(r.StartedAt).Milliseconds(), r.ArtifactChecked,
		r.ArtifactSampled, r.ArtifactPresent, r.ArtifactMissing,
		r.ArtifactSuccessRate, r.Error)
	if err != nil {
		return fmt.Errorf("restoretest: insert result %s: %w", r.ID, err)
	}
	return nil
}

// Latest implements Store.
func (s *PGStore) Latest(ctx context.Context) (Result, bool, error) {
	var (
		r          Result
		durationMS int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, backup_id, backup_type, started_at, completed_at, success,
		       duration_ms, artifact_checked, artifact_sampled, artifact_present,
		       artifact_missing, artifact_success_rate, error
		  FROM ops_restore_test_results
		 ORDER BY completed_at DESC
		 LIMIT 1`).Scan(
		&r.ID, &r.BackupID, &r.BackupType, &r.StartedAt, &r.CompletedAt, &r.Success,
		&durationMS, &r.ArtifactChecked, &r.ArtifactSampled, &r.ArtifactPresent,
		&r.ArtifactMissing, &r.ArtifactSuccessRate, &r.Error)
	if err == pgx.ErrNoRows {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("restoretest: read latest result: %w", err)
	}
	return r, true, nil
}

// TotalArtifactMissing implements Store.
func (s *PGStore) TotalArtifactMissing(ctx context.Context) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(artifact_missing), 0) FROM ops_restore_test_results`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("restoretest: sum artifact_missing: %w", err)
	}
	return total, nil
}
