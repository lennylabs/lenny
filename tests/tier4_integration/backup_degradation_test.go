//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration tests for the §25.11 backup-and-restore Degradation
// contract. The §25.11 "Degradation" subsection states the dependency-down
// failure modes of the backup subsystem:
//
//   - "If Postgres is down: backup creation, listing, and scheduling all
//     fail (503)."
//   - "If K8s API is down: Job creation fails; lenny-ops returns 503
//     BACKUP_JOB_CREATION_FAILED."
//   - "If MinIO is down: backup upload fails; the Job retries 3 times with
//     backoff, then fails."
//
// These tests drive the production surfaces — the Postgres-backed
// backup.Store (pgstore), the orchestrating backup.Service, the production
// k8slauncher, and the lenny-backup runner with its production
// MinIOUploader — against real Postgres and MinIO containers whose backing
// service is then stopped mid-test. Stopping the container is the
// testcontainers analogue of the tier-8 "scale the Deployment to zero"
// injection: it produces the same connection-refused failure a real outage
// would, so the degraded 503 / failed-run behavior is exercised against a
// genuinely unavailable dependency rather than a fake that returns a
// canned error.
//
// The §25.11 Job-level "retries 3 times with backoff" is the static
// backoffLimit:3 on the rendered Job Pod Specification, asserted at tier 2
// in pkg/ops/backup/k8slauncher (renderJob). These tests assert the
// dependency-down error the run surfaces, which the reconciler and the Job
// backoff then act on.
package tier4_integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/k8slauncher"
	"github.com/lennylabs/lenny/pkg/ops/backup/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// stubLauncher is a §25.11 JobLauncher that reports every Launch as
// accepted. It stands in for the Kubernetes API on the Postgres-down test,
// where the store fails before the launcher is ever consulted; a launched
// Job that is never inspected keeps the test focused on the store outage.
type stubLauncher struct{}

func (stubLauncher) Launch(context.Context, backup.JobSpec) (backup.LaunchedJob, error) {
	return backup.LaunchedJob{JobID: "job-stub"}, nil
}

func (stubLauncher) JobStatus(context.Context, string) (backup.BackupJob, error) {
	return backup.BackupJob{}, backup.ErrNotFound
}

// applyBackupSchema applies the §25.11 backup schema migrations (0123
// ops_backups, 0127 ops_restore_state completion columns) to pool, the
// same subset pkg/ops/backup/pgstore exercises against.
func applyBackupSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, mig := range []string{"0123_ops_backups.up.sql", "0127_ops_restore_state_completion.up.sql"} {
		up, err := migrations.FS.ReadFile(mig)
		if err != nil {
			t.Fatalf("read migration %s: %v", mig, err)
		}
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("apply migration %s: %v", mig, err)
		}
	}
}

// spec: §25.11 (Backup and Restore API) — Degradation: "If Postgres is
// down: backup creation, listing, and scheduling all fail (503)."
//
// diagnosis: the §25.11 Postgres-down degraded mode does not hold for the
// backup subsystem. The test builds the production backup.Service over the
// Postgres-backed pgstore against a real Postgres, then stops Postgres. If
// CreateBackup, ListBackups, or GetSchedule return anything other than the
// TRANSIENT BACKUP_STORAGE_UNREACHABLE code (which the HTTP layer maps to
// 503), a real Postgres outage would not surface as the documented 503 —
// the call would hang, succeed against stale state, or return a
// mis-classified error.
func TestBackupCreationFailsWhenPostgresDown(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres container; skipped under -short")
	}
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		Image:    "pgvector/pgvector:pg16",
		Database: "lenny",
		User:     "lenny",
		Password: "lenny",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	applyBackupSchema(t, ctx, pg.Pool)

	svc, err := backup.NewService(backup.Config{
		Store:    pgstore.New(pg.Pool),
		Launcher: stubLauncher{},
		Locker:   backup.NewMemLocker(),
	})
	if err != nil {
		t.Fatalf("build backup.Service: %v", err)
	}

	// Precondition: with Postgres up the backup subsystem is healthy, so
	// the failures below are attributable to the injected outage. A full
	// backup is not confirm-gated outside production.
	if _, err := svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"}); err != nil {
		t.Fatalf("precondition: CreateBackup should succeed while Postgres is up: %v", err)
	}
	if _, err := svc.ListBackups(ctx, backup.BackupFilter{}, "", 50); err != nil {
		t.Fatalf("precondition: ListBackups should succeed while Postgres is up: %v", err)
	}

	// Inject: stop Postgres. Subsequent store operations fail to reach the
	// backend rather than returning stale success.
	pg.Stop(t)

	// A short deadline per call keeps a would-be hang (rather than a prompt
	// connection-refused) from stalling the suite; a deadline error is still
	// a failure the orchestrator classifies as BACKUP_STORAGE_UNREACHABLE.
	assertStorageUnreachable := func(name string, call func(context.Context) error) {
		t.Helper()
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		err := call(cctx)
		if err == nil {
			t.Errorf("%s returned no error while Postgres is down; §25.11 requires it to fail 503", name)
			return
		}
		if code := backup.CodeOf(err); code != backup.ErrCodeStorageUnreachable {
			t.Errorf("%s while Postgres is down returned code %q, want %q (503); err: %v",
				name, code, backup.ErrCodeStorageUnreachable, err)
		}
	}

	assertStorageUnreachable("CreateBackup", func(c context.Context) error {
		_, e := svc.CreateBackup(c, backup.BackupRequest{Type: "full", StartedBy: "alice"})
		return e
	})
	assertStorageUnreachable("ListBackups", func(c context.Context) error {
		_, e := svc.ListBackups(c, backup.BackupFilter{}, "", 50)
		return e
	})
	assertStorageUnreachable("GetSchedule", func(c context.Context) error {
		_, e := svc.GetSchedule(c)
		return e
	})
}

// spec: §25.11 (Backup and Restore API) — Degradation: "If K8s API is
// down: Job creation fails; lenny-ops returns 503
// BACKUP_JOB_CREATION_FAILED."
//
// diagnosis: the §25.11 Kubernetes-API-down degraded mode does not hold.
// The test builds the production backup.Service over a healthy Postgres
// store and the production k8slauncher pointed at an unreachable API
// server, then triggers a backup. The store insert succeeds (Postgres is
// up), so the failure isolates the Job-creation step. If CreateBackup
// returns anything other than the TRANSIENT BACKUP_JOB_CREATION_FAILED
// code (HTTP 503), an unreachable control plane would not surface as the
// documented 503.
func TestBackupJobCreationFailsWhenKubernetesAPIDown(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres container; skipped under -short")
	}
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		Image:    "pgvector/pgvector:pg16",
		Database: "lenny",
		User:     "lenny",
		Password: "lenny",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	applyBackupSchema(t, ctx, pg.Pool)

	// The production k8slauncher pointed at a closed port: a Job Create
	// fails with connection-refused, the same failure a down API server
	// surfaces. rest.Config.Timeout bounds the dial so the test cannot hang
	// if the port were instead filtered.
	clientset, err := kubernetes.NewForConfig(&rest.Config{
		Host:    "http://127.0.0.1:1",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	launcher, err := k8slauncher.New(k8slauncher.Config{
		Clientset: clientset,
		Namespace: "lenny-system",
		Image:     "lenny-backup:test",
	})
	if err != nil {
		t.Fatalf("build k8slauncher: %v", err)
	}

	svc, err := backup.NewService(backup.Config{
		Store:    pgstore.New(pg.Pool),
		Launcher: launcher,
		Locker:   backup.NewMemLocker(),
	})
	if err != nil {
		t.Fatalf("build backup.Service: %v", err)
	}

	_, err = svc.CreateBackup(ctx, backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err == nil {
		t.Fatalf("CreateBackup returned no error with the API server unreachable; " +
			"§25.11 requires BACKUP_JOB_CREATION_FAILED (503)")
	}
	if code := backup.CodeOf(err); code != backup.ErrCodeJobCreationFailed {
		t.Fatalf("CreateBackup with the API server unreachable returned code %q, want %q (503); err: %v",
			code, backup.ErrCodeJobCreationFailed, err)
	}
}

// fakeDumper is a §25.11 runner.Dumper that returns fixed component bytes
// so the MinIO-down test exercises the upload step without pg_dump. The
// §25.11 in-Job dump reads Postgres directly; the outage under test is
// MinIO, downstream of the dump.
type fakeDumper struct{}

func (fakeDumper) DumpPostgres(context.Context) (runner.Component, error) {
	return runner.Component{Bytes: []byte("-- pg_dump fixture --\n")}, nil
}

func (fakeDumper) ExportConfig(context.Context) (runner.Component, error) {
	return runner.Component{Bytes: []byte("{}")}, nil
}

func (fakeDumper) ExportCRDs(context.Context) (runner.Component, error) {
	return runner.Component{Bytes: []byte("{}")}, nil
}

// captureBackupReporter records the §25.11 runner terminal transition so
// the test can assert the run recorded the failed status.
type captureBackupReporter struct {
	failedID  string
	failedMsg string
	completed bool
}

func (r *captureBackupReporter) BackupCompleted(context.Context, runner.Result) error {
	r.completed = true
	return nil
}

func (r *captureBackupReporter) BackupFailed(_ context.Context, id, msg string) error {
	r.failedID, r.failedMsg = id, msg
	return nil
}

// spec: §25.11 (Backup and Restore API) — Degradation: "If MinIO is down:
// backup upload fails; the Job retries 3 times with backoff, then fails."
//
// diagnosis: the §25.11 MinIO-down degraded mode does not hold for the
// lenny-backup runner. The test runs the production runner with the
// production MinIOUploader against a real MinIO, then stops MinIO and runs
// again. If the second run does not return an upload error and record the
// failed status through the Reporter (the ops_backups status:failed the
// Job's exit drives), a MinIO outage would not surface as a failed backup
// — the run would appear to succeed with no durable archive. The Job-level
// "retries 3 times with backoff" is the static backoffLimit:3 asserted in
// pkg/ops/backup/k8slauncher.
func TestBackupUploadFailsWhenMinIODown(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a MinIO container; skipped under -short")
	}
	// MinIO's built-in KMS enables the SSE-S3 the production MinIOUploader
	// always applies, so the precondition upload (MinIO up) succeeds.
	m := containers.StartMinIO(t, containers.MinIOOptions{
		Bucket: "lenny-backups",
		Env: map[string]string{
			"MINIO_KMS_SECRET_KEY": "lenny-test-key:AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	uploader, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
		Client: m.Client,
		Bucket: m.Bucket,
	})
	if err != nil {
		t.Fatalf("build MinIOUploader: %v", err)
	}

	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(i + 1)
	}
	runCfg := func(id string, rep *captureBackupReporter) runner.Config {
		return runner.Config{
			BackupID: id,
			Mode:     runner.ModePostgres,
			Dumper:   fakeDumper{},
			Archiver: &runner.TarGzArchiver{DataKey: dataKey},
			Uploader: uploader,
			Reporter: rep,
		}
	}

	// Precondition: with MinIO up the run completes, so the failure below is
	// attributable to the outage rather than a misconfigured uploader.
	okRep := &captureBackupReporter{}
	if _, err := runner.Run(ctx, runCfg("bkp-degr-ok", okRep)); err != nil {
		t.Fatalf("precondition: backup run should succeed while MinIO is up: %v", err)
	}
	if !okRep.completed {
		t.Fatalf("precondition: reporter did not record completion while MinIO is up")
	}

	// Inject: stop MinIO. The next upload fails to connect.
	m.Stop(t)

	failRep := &captureBackupReporter{}
	cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ccancel()
	_, err = runner.Run(cctx, runCfg("bkp-degr-down", failRep))
	if err == nil {
		t.Fatalf("backup run returned no error with MinIO down; §25.11 requires the upload to fail")
	}
	if failRep.completed {
		t.Errorf("reporter recorded completion for a run whose MinIO upload failed")
	}
	if failRep.failedID != "bkp-degr-down" {
		t.Errorf("reporter recorded failed id %q, want the failed run's backup id; err: %v",
			failRep.failedID, err)
	}
	if failRep.failedMsg == "" {
		t.Errorf("reporter recorded an empty failure message for the MinIO-down upload failure")
	}
}
