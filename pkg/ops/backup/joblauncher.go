// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"sync"
)

// JobKind identifies which §25.11 Kubernetes Job a launch request
// creates: a backup run, a verification run, or a restore run.
type JobKind string

const (
	// JobBackup runs the lenny-backup image to perform a backup
	// (Postgres dump plus the config/CRD export) and upload it to MinIO.
	JobBackup JobKind = "backup"
	// JobVerify runs the lenny-backup image in verify mode: download the
	// archive, validate the checksum, and run pg_restore --list.
	JobVerify JobKind = "verify"
	// JobRestore runs the lenny-backup image in restore mode: pg_restore
	// against each shard.
	JobRestore JobKind = "restore"
	// JobRetention runs the lenny-backup image in retention mode: the
	// §25.11 daily 03:30 UTC sweep that deletes expired backups from both
	// MinIO and Postgres in a coordinated sequence (lines 4108-4111). It
	// carries no BackupID — it operates over every expired row.
	JobRetention JobKind = "retention"
)

// JobSpec describes one §25.11 Kubernetes Job the orchestrator asks the
// launcher to create. The launcher renders the §25.11 Job Pod
// Specification — restartPolicy Never, backoffLimit 3,
// ttlSecondsAfterFinished 3600, activeDeadlineSeconds 7200, the
// non-root read-only-rootfs security context, the lenny-backup-sa
// ServiceAccount, and the lenny-backup-job NetworkPolicy — around the
// per-run parameters carried here.
type JobSpec struct {
	// Kind selects the §25.11 Job mode.
	Kind JobKind
	// BackupID is the ops_backups row this Job correlates with. The
	// launcher sets it as the spec.template.metadata.annotations
	// ["lenny.dev/backup-id"] annotation so the §25.11 reconciler can
	// match Jobs to rows.
	BackupID string
	// BackupType is the backup type ("full", "postgres", "config") for a
	// JobBackup, or the type of the backup being restored/verified.
	BackupType string
	// RestoreID is the ops_restore_state row a JobRestore correlates
	// with; empty for a backup or verify Job.
	RestoreID string
}

// LaunchedJob is the launcher's report of an accepted §25.11 Job.
type LaunchedJob struct {
	// JobID is the Kubernetes Job name. The orchestrator records it on
	// the ops_backups or ops_restore_state row.
	JobID string
}

// JobLauncher creates and observes the §25.11 backup/restore Kubernetes
// Jobs. lenny-ops does not run backups in-process — it orchestrates a
// Job in the lenny-system namespace using the lenny-backup image. This
// interface is the seam between the BackupService orchestration logic
// (tested with FakeLauncher) and the real Kubernetes client.
//
// A wire detail the spec leaves to the deployment: the lenny-backup
// image reference is resolved through ImageResolver from
// {platform.registry.url}/lenny-backup:{version}, and the Job's
// Postgres and MinIO credentials come from the lenny-backup-postgres
// and lenny-backup-minio Secrets. A production JobLauncher is
// constructed with those references; this package does not embed a
// Kubernetes client so the orchestration logic stays unit-testable.
type JobLauncher interface {
	// Launch creates the §25.11 Job described by spec and returns its
	// JobID once the API server has accepted it. A failure to reach the
	// Kubernetes API surfaces as the §25.11 BACKUP_JOB_CREATION_FAILED
	// error.
	Launch(ctx context.Context, spec JobSpec) (LaunchedJob, error)
	// JobStatus reads the Kubernetes Job status for a running backup or
	// restore operation (GET /v1/admin/backup-jobs/{id}).
	JobStatus(ctx context.Context, jobID string) (BackupJob, error)
}

// FakeLauncher is an in-memory JobLauncher for tests and a Kubernetes-
// less local deployment. It records every launched Job and reports a
// configurable status, so the BackupService orchestration logic is
// exercised without a cluster.
type FakeLauncher struct {
	mu sync.Mutex
	// LaunchErr, when set, makes Launch fail — the test path for
	// BACKUP_JOB_CREATION_FAILED.
	LaunchErr error
	// nextID numbers the synthetic Job names.
	nextID int
	// jobs records every launched Job keyed by JobID.
	jobs map[string]launchedRecord
}

// launchedRecord pairs a launched JobSpec with the status FakeLauncher
// reports for it.
type launchedRecord struct {
	spec   JobSpec
	status BackupJob
}

// NewFakeLauncher returns an empty FakeLauncher.
func NewFakeLauncher() *FakeLauncher {
	return &FakeLauncher{jobs: make(map[string]launchedRecord)}
}

// Launch implements JobLauncher. It records the spec and reports the
// Job as active (the backup is in-flight) until SetJobStatus overrides
// it.
func (f *FakeLauncher) Launch(_ context.Context, spec JobSpec) (LaunchedJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LaunchErr != nil {
		return LaunchedJob{}, f.LaunchErr
	}
	f.nextID++
	id := jobName(spec.Kind, f.nextID)
	f.jobs[id] = launchedRecord{
		spec:   spec,
		status: BackupJob{JobID: id, BackupID: spec.BackupID, Phase: "Active", Active: 1},
	}
	return LaunchedJob{JobID: id}, nil
}

// JobStatus implements JobLauncher.
func (f *FakeLauncher) JobStatus(_ context.Context, jobID string) (BackupJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.jobs[jobID]
	if !ok {
		return BackupJob{}, ErrNotFound
	}
	return rec.status, nil
}

// SetJobStatus overrides the status FakeLauncher reports for a launched
// Job, so a test can simulate a Job that completed or failed.
func (f *FakeLauncher) SetJobStatus(jobID string, status BackupJob) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec, ok := f.jobs[jobID]; ok {
		status.JobID = jobID
		status.BackupID = rec.spec.BackupID
		rec.status = status
		f.jobs[jobID] = rec
	}
}

// LaunchedSpecs returns the JobSpec of every Job FakeLauncher has
// launched, so a test can assert what was created.
func (f *FakeLauncher) LaunchedSpecs() []JobSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	specs := make([]JobSpec, 0, len(f.jobs))
	for _, rec := range f.jobs {
		specs = append(specs, rec.spec)
	}
	return specs
}

var _ JobReaper = (*FakeLauncher)(nil)

// ListManagedJobs implements JobReaper: it reports every launched Job
// with its lenny.dev/backup-id annotation (the spec's BackupID) so the
// §25.11 orphaned-Job reconciler can match Jobs to ops_backups rows.
func (f *FakeLauncher) ListManagedJobs(_ context.Context) ([]OrphanedJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	jobs := make([]OrphanedJob, 0, len(f.jobs))
	for id, rec := range f.jobs {
		jobs = append(jobs, OrphanedJob{JobID: id, BackupID: rec.spec.BackupID})
	}
	return jobs, nil
}

// DeleteJob implements JobReaper: it removes a launched Job by name.
// Deleting an unknown Job is a no-op (the §25.11 reconciler is
// idempotent against a Job another reconcile already swept).
func (f *FakeLauncher) DeleteJob(_ context.Context, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.jobs, jobID)
	return nil
}

// jobName builds a synthetic Job name from a kind and a sequence
// number.
func jobName(kind JobKind, n int) string {
	return "lenny-" + string(kind) + "-job-" + itoa(n)
}

// itoa renders a small non-negative int without importing strconv into
// this file's hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
