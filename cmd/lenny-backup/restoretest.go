// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lennylabs/lenny/pkg/ops/backup/restoretest"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

var _ runner.VerifyReporter = (*pgReporter)(nil)

// pgBackupResolver resolves the §25.11 verify/restore-test target backup
// from the ops_backups table.
type pgBackupResolver struct{ pool *pgxpool.Pool }

var _ runner.BackupResolver = (*pgBackupResolver)(nil)

// Resolve implements runner.BackupResolver: it reads one backup by id.
func (r *pgBackupResolver) Resolve(ctx context.Context, backupID string) (runner.Target, error) {
	var t runner.Target
	err := r.pool.QueryRow(ctx, `
		SELECT id, type, COALESCE(storage_path, ''), COALESCE(checksum, '')
		  FROM ops_backups WHERE id = $1`, backupID).
		Scan(&t.BackupID, &t.BackupType, &t.ObjectPath, &t.Checksum)
	if err == pgx.ErrNoRows {
		return runner.Target{}, fmt.Errorf("no backup %q", backupID)
	}
	if err != nil {
		return runner.Target{}, fmt.Errorf("read backup %q: %w", backupID, err)
	}
	return t, nil
}

// ResolveLatest implements runner.BackupResolver. It interprets the
// §25.11 backups.restoreTest.backupSelector in three forms: "latest-<type>"
// (e.g. "latest-full") restores the most recent successful backup of
// that type; "" or "latest" restores the most recent successful backup
// of any type; any other value is treated as a literal backup id.
func (r *pgBackupResolver) ResolveLatest(ctx context.Context, selector string) (runner.Target, bool, error) {
	switch {
	case selector == "" || selector == "latest":
		return r.latestOfType(ctx, "")
	case strings.HasPrefix(selector, "latest-"):
		return r.latestOfType(ctx, strings.TrimPrefix(selector, "latest-"))
	default:
		// A literal backup id: resolve the row directly, requiring it to
		// be a completed/verified backup with an archive.
		var t runner.Target
		err := r.pool.QueryRow(ctx, `
			SELECT id, type, COALESCE(storage_path, ''), COALESCE(checksum, '')
			  FROM ops_backups
			 WHERE id = $1
			   AND status IN ('completed', 'verified')
			   AND COALESCE(storage_path, '') <> ''`, selector).
			Scan(&t.BackupID, &t.BackupType, &t.ObjectPath, &t.Checksum)
		if err == pgx.ErrNoRows {
			return runner.Target{}, false, nil
		}
		if err != nil {
			return runner.Target{}, false, fmt.Errorf("read backup %q: %w", selector, err)
		}
		return t, true, nil
	}
}

// latestOfType reads the most recent completed/verified backup of the
// given type (empty matches any type).
func (r *pgBackupResolver) latestOfType(ctx context.Context, backupType string) (runner.Target, bool, error) {
	var t runner.Target
	err := r.pool.QueryRow(ctx, `
		SELECT id, type, COALESCE(storage_path, ''), COALESCE(checksum, '')
		  FROM ops_backups
		 WHERE ($1 = '' OR type = $1)
		   AND status IN ('completed', 'verified')
		   AND COALESCE(storage_path, '') <> ''
		 ORDER BY completed_at DESC NULLS LAST
		 LIMIT 1`, backupType).
		Scan(&t.BackupID, &t.BackupType, &t.ObjectPath, &t.Checksum)
	if err == pgx.ErrNoRows {
		return runner.Target{}, false, nil
	}
	if err != nil {
		return runner.Target{}, false, fmt.Errorf("read latest backup: %w", err)
	}
	return t, true, nil
}

// restoreTestParams carries the resolved restore-test flags.
type restoreTestParams struct {
	jobName       string
	selector      string
	sampleSize    int
	scratchDSN    string
	pgRestorePath string
	psqlPath      string
	replication   replicationConfig
}

// replicationConfig points the §25.11 sampled-HEAD ArtifactStore check
// at the off-cluster replication target. An empty Endpoint disables the
// check (the restore test then verifies Postgres only).
type replicationConfig struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
	useTLS    bool
}

// runVerify wires the production seams and performs a §25.11 Backup
// Verification of backupID.
func runVerify(ctx context.Context, d *deps, backupID, pgRestorePath string, stdout, stderr io.Writer) int {
	if backupID == "" {
		fmt.Fprintln(stderr, "lenny-backup: --backup-id is required for a verify run")
		return exitUsage
	}
	downloader, err := runner.NewMinIODownloader(d.minioClient, d.bucket)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: %v\n", err)
		return exitUsage
	}
	err = runner.RunVerify(ctx, runner.VerifyConfig{
		BackupID:   backupID,
		Resolver:   &pgBackupResolver{pool: d.reporter.pool},
		Downloader: downloader,
		Opener:     &runner.TarGzOpener{DataKey: d.dataKey},
		Inspector:  &runner.ExecDumpInspector{PgRestorePath: pgRestorePath},
		Reporter:   d.reporter,
		Audit:      d.audit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: verify %s failed: %v\n", backupID, err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "lenny-backup: verify %s passed (status:verified)\n", backupID)
	return exitOK
}

// runRestoreTest wires the production seams and performs a §25.11 Test
// Restore, recording the outcome for the lenny-ops restore-test gauges.
func runRestoreTest(ctx context.Context, d *deps, p restoreTestParams, stdout, stderr io.Writer) int {
	downloader, err := runner.NewMinIODownloader(d.minioClient, d.bucket)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: %v\n", err)
		return exitUsage
	}

	// The scratch restore is run only when a scratch Postgres DSN is
	// supplied; otherwise the test verifies readability (pg_restore
	// --list) without a live restore.
	var restorer runner.ScratchRestorer
	if p.scratchDSN != "" {
		restorer = &runner.ExecScratchRestorer{PgRestorePath: p.pgRestorePath, PsqlPath: p.psqlPath, ScratchDSN: p.scratchDSN}
	}

	// The sampled-HEAD ArtifactStore check runs only when a replication
	// target is configured; its key source reads the restored
	// artifact_store rows from the scratch Postgres.
	var sampler runner.ArtifactSampler
	var closeSampler func()
	if p.replication.endpoint != "" && p.scratchDSN != "" && p.sampleSize > 0 {
		sampler, closeSampler, err = buildArtifactSampler(ctx, p)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-backup: %v\n", err)
			return exitUsage
		}
		defer closeSampler()
	}

	result, err := runner.RunRestoreTest(ctx, runner.RestoreTestConfig{
		JobID:              p.jobName,
		Selector:           p.selector,
		ArtifactSampleSize: p.sampleSize,
		Resolver:           &pgBackupResolver{pool: d.reporter.pool},
		Downloader:         downloader,
		Opener:             &runner.TarGzOpener{DataKey: d.dataKey},
		Inspector:          &runner.ExecDumpInspector{PgRestorePath: p.pgRestorePath},
		Restorer:           restorer,
		Sampler:            sampler,
		Store:              restoretest.NewPGStore(d.reporter.pool),
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: restore-test failed to run: %v\n", err)
		return exitFailure
	}
	if !result.Success {
		fmt.Fprintf(stderr, "lenny-backup: restore-test FAILED for backup %q: %s\n",
			result.BackupID, result.Error)
		return exitFailure
	}
	fmt.Fprintf(stdout,
		"lenny-backup: restore-test passed for backup %q in %.1fs (artifact-checked=%t, rate=%.4f)\n",
		result.BackupID, result.DurationSeconds(), result.ArtifactChecked, result.ArtifactSuccessRate)
	return exitOK
}

// buildArtifactSampler constructs the §25.11 sampled-HEAD ArtifactStore
// check: a MinIO client for the replication target plus a key source
// that draws object keys from the restored scratch Postgres
// artifact_store rows.
func buildArtifactSampler(ctx context.Context, p restoreTestParams) (runner.ArtifactSampler, func(), error) {
	if p.replication.bucket == "" {
		return nil, nil, errReplicationBucketMissing
	}
	repClient, err := minio.New(p.replication.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(p.replication.accessKey, p.replication.secretKey, ""),
		Secure: p.replication.useTLS,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build replication MinIO client: %w", err)
	}
	scratchPool, err := pgxpool.New(ctx, p.scratchDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the scratch Postgres for artifact sampling: %w", err)
	}
	keys := func(ctx context.Context, sampleSize int) ([]string, error) {
		rows, err := scratchPool.Query(ctx, `
			SELECT uri FROM artifact_store
			 WHERE state = 'live'
			 ORDER BY random()
			 LIMIT $1`, sampleSize)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var uri string
			if err := rows.Scan(&uri); err != nil {
				return nil, err
			}
			out = append(out, uri)
		}
		return out, rows.Err()
	}
	sampler, err := runner.NewMinIOArtifactSampler(repClient, p.replication.bucket, keys)
	if err != nil {
		scratchPool.Close()
		return nil, nil, err
	}
	return sampler, scratchPool.Close, nil
}

// errReplicationBucketMissing flags a replication endpoint configured
// without a bucket.
var errReplicationBucketMissing = errors.New(
	"lenny-backup: --replication-endpoint set without --replication-bucket",
)
