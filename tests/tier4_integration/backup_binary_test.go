//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test that builds and runs the compiled
// cmd/lenny-backup binary itself, rather than driving the
// pkg/ops/backup/runner surfaces in-process. TestBackupRestoreRoundTrip
// in backup_restore_test.go calls runner.Run directly with hand-built
// runner.Config; deps_test.go and main_test.go call resolveDeps and run
// in-process. None of those exercises main.go's flag parsing (the real
// os.Args -> flag.FlagSet wiring) or deps.go's resolveDeps assembly (the
// real MinIO client, pgxpool, and pgReporter construction) as a genuine
// subprocess, so a wiring bug there — a flag that main.go reads but
// never threads into depsInput, or a resolveDeps step that only compiles
// because a unit test's fakes tolerate it — would not be caught by
// either.
package tier4_integration_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §25.11 "Backup Execution" — "lenny-ops does not run backups
// in-process. Instead, it orchestrates a K8s Job ... using the
// lenny-backup image"; "Full backup flow inside the Job: 1. Runs pg_dump
// against each Postgres shard ... 7. Uploads to MinIO at
// backups/{type}/{id}/{timestamp}.tar.gz.enc ... 8. Updates the
// ops_backups row with size, checksum, encryption metadata, and
// status: "completed"."
//
// diagnosis: the Job pod's entrypoint is the compiled lenny-backup
// binary invoked with real command-line flags, not the runner package
// called in-process. This test compiles cmd/lenny-backup, runs it as a
// subprocess with --mode=full against a real Postgres shard and a real
// MinIO, and asserts on the subprocess exit code, the uploaded MinIO
// object, and the ops_backups row the binary's own pgReporter writes
// through main.go/deps.go — not on the runner.Result an in-process call
// returns. A regression in main.go's flag parsing (a flag main.go
// declares but never threads into depsInput or runner.Config) or in
// deps.go's resolveDeps assembly (wiring the wrong MinIO bucket, or
// dropping the report DSN) would leave every runner-level test green
// while the real Job pod fails or silently no-ops.
func TestBackupBinaryFullModeAgainstPostgresAndMinIO(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and starts Postgres and MinIO containers; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	// The binary shells out to pg_dump for the §25.11 step-1 dump.
	pgDump := lookTool(t, "pg_dump")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Pin Postgres 15 so the host pg_dump (>= 15) can dump the server;
	// pg_dump refuses a server newer than itself, matching
	// TestBackupRestoreRoundTrip's pin.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		Image:    "postgres:15",
		Database: "lenny_src",
		User:     "lenny",
		Password: "lenny",
	})
	mustExec(t, pg.Pool, ctx,
		`CREATE TABLE backup_probe (id integer PRIMARY KEY, note text NOT NULL)`)
	mustExec(t, pg.Pool, ctx,
		`INSERT INTO backup_probe (id, note) VALUES (1, 'acme-binary-probe')`)

	// A second database on the same container stands in for the
	// lenny-ops Postgres the Job's --report-dsn points at: the ops_backups
	// row the §25.11 step-8 update writes lives here, separate from the
	// shard being dumped, matching the production topology.
	mustExec(t, pg.Pool, ctx, `CREATE DATABASE lenny_ops`)
	reportDSN := swapDatabase(t, pg.DSN, "lenny_ops")
	reportPool, err := pgxpool.New(ctx, reportDSN)
	if err != nil {
		t.Fatalf("connect to the report database: %v", err)
	}
	defer reportPool.Close()
	applyBackupSchema(t, ctx, reportPool)

	// §25.11 Creation Sequence step 1: lenny-ops inserts the ops_backups
	// row with status:"running" before the Job ever starts (this test
	// stands in for the Job creation step, which is exercised elsewhere
	// by the pgstore/k8slauncher tests). The binary itself never inserts
	// this row — it only updates it — so the row must exist for the
	// step-8 completion update to land anywhere.
	const backupID = "bkp-t4-binary"
	mustExec(t, reportPool, ctx, `
		INSERT INTO ops_backups (id, type, status, started_by, job_id, platform_version, schema_version)
		VALUES ($1, 'full', 'running', 'test', 'job-t4-binary', 'test', 1)`, backupID)

	// Real MinIO with the built-in KMS enabled so the §25.11 / §12.9
	// SSE-S3 upload the binary's MinIOUploader always applies is
	// accepted, matching TestBackupRestoreRoundTrip.
	m := containers.StartMinIO(t, containers.MinIOOptions{
		Bucket: "lenny-backups",
		Env: map[string]string{
			"MINIO_KMS_SECRET_KEY": "lenny-test-key:AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		},
	})

	// Compile the real cmd/lenny-backup binary rather than driving the
	// runner package in-process.
	bin := filepath.Join(t.TempDir(), "lenny-backup")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lenny-backup")
	build.Dir = schematest.RepoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/lenny-backup: %v\n%s", err, out)
	}

	runCtx, runCancel := context.WithTimeout(ctx, 90*time.Second)
	defer runCancel()
	cmd := exec.CommandContext(
		runCtx, bin,
		"--mode", "full",
		"--backup-id", backupID,
		"--postgres-shard", pg.DSN,
		"--pg-dump-path", pgDump,
		"--minio-endpoint", m.Endpoint,
		"--minio-bucket", m.Bucket,
		"--minio-access-key", m.AccessKey,
		"--minio-secret-key", m.SecretKey,
		"--minio-tls=false",
		"--report-dsn", reportDSN,
		"--timeout", "60s",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// §25.11 Exit codes: "0  the run succeeded". A nonzero exit (or a
	// process that never started) means the Job pod would report a
	// failed backup even though this is the documented success path.
	if runErr != nil {
		t.Fatalf("lenny-backup --mode full exited with error: %v\nstdout: %s\nstderr: %s",
			runErr, stdout.String(), stderr.String())
	}

	// The subprocess's own stdout carries the §25.11 completion line
	// main.go prints after a successful run.
	if !bytes.Contains(stdout.Bytes(), []byte("backup "+backupID+" completed")) {
		t.Errorf("lenny-backup stdout did not report completion for %s: %s", backupID, stdout.String())
	}

	// The backup artifact must exist in MinIO at the §25.11 step-7
	// canonical path.
	prefix := "backups/full/" + backupID + "/"
	objCh := m.Client.ListObjects(ctx, m.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	found := false
	for obj := range objCh {
		if obj.Err != nil {
			t.Fatalf("list MinIO objects under %s: %v", prefix, obj.Err)
		}
		found = true
	}
	if !found {
		t.Errorf("§25.11 step-7 violation: no MinIO object found under %s after a successful run", prefix)
	}

	// The binary's own pgReporter (deps.go: resolveDeps wires
	// reporter:&pgReporter{pool}) must have performed the §25.11 step-8
	// completion update against the real --report-dsn Postgres, proving
	// deps.go assembled a working reporter rather than a test double.
	var status, checksum, storagePath string
	if err := reportPool.QueryRow(
		ctx,
		`SELECT status, checksum, storage_path FROM ops_backups WHERE id = $1`, backupID,
	).Scan(&status, &checksum, &storagePath); err != nil {
		t.Fatalf("query ops_backups row %s: %v", backupID, err)
	}
	if status != "completed" {
		t.Errorf("§25.11 step-8 violation: ops_backups row status = %q, want completed", status)
	}
	if checksum == "" {
		t.Errorf("§25.11 step-8 violation: ops_backups row recorded no checksum")
	}
	if storagePath == "" {
		t.Errorf("§25.11 step-8 violation: ops_backups row recorded no storage_path")
	}
}
