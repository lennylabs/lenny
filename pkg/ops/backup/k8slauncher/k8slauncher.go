// SPDX-License-Identifier: MIT

// Package k8slauncher is the production §25.11 JobLauncher: it creates and
// observes the backup, verify, restore, and retention Kubernetes Jobs in
// the lenny-system namespace using a client-go clientset. lenny-ops does
// not run backups in-process — it orchestrates a Job from the lenny-backup
// image, and this package is the seam between the unit-testable
// backup.Service orchestration and the real Kubernetes API
// (F-25.11.4 / F-17.3.4).
//
// The rendered pod follows the §25.11 Job Pod Specification verbatim:
// restartPolicy Never, backoffLimit 3, ttlSecondsAfterFinished 3600,
// activeDeadlineSeconds 7200, the lenny-backup-sa ServiceAccount, the
// non-root read-only-rootfs security context with a writable /tmp
// emptyDir, and the Postgres/MinIO credentials from the lenny-backup-postgres
// and lenny-backup-minio Secrets. The pod carries the app: lenny-backup
// label the lenny-backup-job NetworkPolicy selects (§25.4 lines 1345-1403)
// and the lenny.dev/backup-id annotation the §25.11 orphan reconciler
// matches against ops_backups rows.
//
// spec: §25.11 lines 3963-3994.
package k8slauncher

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// componentLabel and appLabel are the labels every backup Job pod
// carries: lenny.dev/component for the §17.8 inventory and app for the
// lenny-backup-job NetworkPolicy podSelector.
const (
	componentLabel = "lenny.dev/component"
	appLabel       = "app"
	appValue       = "lenny-backup"
	// backupIDAnnotation is the §25.11 lines 3972/3978 correlation
	// annotation the orphan reconciler reads.
	backupIDAnnotation = "lenny.dev/backup-id"
	// backupRegionAnnotation records the §12.8 data-residency region a
	// per-region backup Job covers, so an operator and the inventory can
	// see which jurisdiction a Job dumped.
	backupRegionAnnotation = "lenny.dev/backup-region"
)

// Config assembles a production Launcher.
type Config struct {
	// Clientset is the Kubernetes client. Required.
	Clientset kubernetes.Interface
	// Namespace is the namespace the Jobs run in (lenny-system). Required.
	Namespace string
	// Image is the resolved lenny-backup image reference
	// ({platform.registry.url}/lenny-backup:{version}). Required.
	Image string
	// ServiceAccount is the §25.11 lenny-backup-sa ServiceAccount. Defaults
	// to "lenny-backup-sa" when empty.
	ServiceAccount string
	// PostgresSecret names the §25.11 lenny-backup-postgres Secret; its
	// postgres-dsn key holds the SELECT-only shard connection string.
	// Defaults to "lenny-backup-postgres".
	PostgresSecret string
	// MinIOSecret names the §25.11 lenny-backup-minio Secret; its
	// minio-access-key / minio-secret-key keys hold the backup-bucket
	// credentials. Defaults to "lenny-backup-minio".
	MinIOSecret string
	// ReportDSNSecret names the Secret whose report-dsn key holds the
	// lenny-ops Postgres DSN the Job pod uses to update the ops_backups /
	// ops_restore_state row on exit (the §25.11 step-8 update). Empty
	// leaves LENNY_OPS_POSTGRES_DSN unset.
	ReportDSNSecret string
	// MinIOEndpoint and MinIOBucket are the backup bucket coordinates the
	// Job uploads to; passed as env so the binary reaches MinIO.
	MinIOEndpoint string
	MinIOBucket   string
	// KMSKeyID, when set, selects §12.9 SSE-KMS for the upload.
	KMSKeyID string
	// newSuffix generates the random Job-name suffix; nil uses
	// k8s.io/apimachinery rand. Injected in tests for determinism.
	newSuffix func() string
}

// Launcher is the production §25.11 JobLauncher and JobReaper.
type Launcher struct {
	cfg Config
}

var (
	_ backup.JobLauncher = (*Launcher)(nil)
	_ backup.JobReaper   = (*Launcher)(nil)
)

// New builds a Launcher from cfg. It returns an error when a required
// dependency is missing.
func New(cfg Config) (*Launcher, error) {
	if cfg.Clientset == nil {
		return nil, fmt.Errorf("k8slauncher: a Clientset is required")
	}
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("k8slauncher: a Namespace is required")
	}
	if cfg.Image == "" {
		return nil, fmt.Errorf("k8slauncher: an Image is required")
	}
	if cfg.ServiceAccount == "" {
		cfg.ServiceAccount = "lenny-backup-sa"
	}
	if cfg.PostgresSecret == "" {
		cfg.PostgresSecret = "lenny-backup-postgres"
	}
	if cfg.MinIOSecret == "" {
		cfg.MinIOSecret = "lenny-backup-minio"
	}
	if cfg.newSuffix == nil {
		cfg.newSuffix = func() string { return rand.String(5) }
	}
	return &Launcher{cfg: cfg}, nil
}

// Launch implements backup.JobLauncher: it renders the §25.11 Job Pod
// Specification for spec.Kind and creates the Job. A failure to reach the
// API server surfaces to the caller, which maps it to the §25.11
// BACKUP_JOB_CREATION_FAILED error.
func (l *Launcher) Launch(ctx context.Context, spec backup.JobSpec) (backup.LaunchedJob, error) {
	job := l.renderJob(spec)
	created, err := l.cfg.Clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return backup.LaunchedJob{}, fmt.Errorf("create %s Job: %w", spec.Kind, err)
	}
	return backup.LaunchedJob{JobID: created.Name}, nil
}

// JobStatus implements backup.JobLauncher: it reads the Kubernetes Job
// status and projects it onto a backup.BackupJob. A missing Job maps to
// backup.ErrNotFound.
func (l *Launcher) JobStatus(ctx context.Context, jobID string) (backup.BackupJob, error) {
	job, err := l.cfg.Clientset.BatchV1().Jobs(l.cfg.Namespace).Get(ctx, jobID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return backup.BackupJob{}, backup.ErrNotFound
		}
		return backup.BackupJob{}, fmt.Errorf("get Job %s: %w", jobID, err)
	}
	return projectStatus(job), nil
}

// ListManagedJobs implements backup.JobReaper: it lists every lenny-backup
// Job (selected by the app label) with its lenny.dev/backup-id annotation
// so the §25.11 orphan reconciler can match Jobs to ops_backups rows.
func (l *Launcher) ListManagedJobs(ctx context.Context) ([]backup.OrphanedJob, error) {
	list, err := l.cfg.Clientset.BatchV1().Jobs(l.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: appLabel + "=" + appValue,
	})
	if err != nil {
		return nil, fmt.Errorf("list backup Jobs: %w", err)
	}
	out := make([]backup.OrphanedJob, 0, len(list.Items))
	for i := range list.Items {
		j := &list.Items[i]
		out = append(out, backup.OrphanedJob{
			JobID:    j.Name,
			BackupID: j.Spec.Template.Annotations[backupIDAnnotation],
		})
	}
	return out, nil
}

// DeleteJob implements backup.JobReaper: it deletes a Job and its pods
// (PropagationPolicy Background). A not-found Job is not an error — the
// §25.11 reconciler is idempotent against a Job another reconcile already
// swept.
func (l *Launcher) DeleteJob(ctx context.Context, jobID string) error {
	policy := metav1.DeletePropagationBackground
	err := l.cfg.Clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, jobID, metav1.DeleteOptions{
		PropagationPolicy: &policy,
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// renderJob builds the §25.11 Job Pod Specification for spec.
func (l *Launcher) renderJob(spec backup.JobSpec) *batchv1.Job {
	const (
		backoffLimit        int32 = 3
		ttlSecondsAfterDone int32 = 3600
		activeDeadlineSecs  int64 = 7200
		runAsNonRoot              = true
		readOnlyRootFilesys       = true
		allowPrivEscalation       = false
	)
	correlationID := spec.BackupID
	if spec.Kind == backup.JobRestore {
		correlationID = spec.RestoreID
	}
	name := fmt.Sprintf("lenny-%s-%s", spec.Kind, l.cfg.newSuffix())

	podLabels := map[string]string{appLabel: appValue, componentLabel: "backup"}
	podAnnotations := map[string]string{}
	if correlationID != "" {
		podAnnotations[backupIDAnnotation] = correlationID
	}
	// §12.8 line 935: a per-region Job records the region it dumped.
	if spec.Region != "" {
		podAnnotations[backupRegionAnnotation] = spec.Region
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: l.cfg.Namespace,
			Labels:    podLabels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr(backoffLimit),
			TTLSecondsAfterFinished: ptr(ttlSecondsAfterDone),
			ActiveDeadlineSeconds:   ptr(activeDeadlineSecs),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: l.cfg.ServiceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr(runAsNonRoot),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  "backup",
						Image: l.cfg.Image,
						Args:  l.args(spec),
						Env:   l.env(spec),
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(allowPrivEscalation),
							ReadOnlyRootFilesystem:   ptr(readOnlyRootFilesys),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
					}},
					Volumes: []corev1.Volume{{
						Name:         "tmp",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
				},
			},
		},
	}
}

// jobTarget is the resolved MinIO endpoint, bucket, KMS key, and
// credential Secret a Job uploads with — the launcher defaults for a
// single-region backup, or the §12.8 backups.regions.<region> overrides
// for a per-region Job.
type jobTarget struct {
	minioEndpoint string
	minioBucket   string
	kmsKeyID      string
	minioSecret   string
}

// target resolves the MinIO coordinates for spec. A per-region Job
// (spec.Region set) uses its RegionConfig so one region's Job cannot
// authenticate to or write into another region's MinIO; a single-region
// Job uses the launcher defaults. spec: §12.8 line 934.
func (l *Launcher) target(spec backup.JobSpec) jobTarget {
	t := jobTarget{
		minioEndpoint: l.cfg.MinIOEndpoint,
		minioBucket:   l.cfg.MinIOBucket,
		kmsKeyID:      l.cfg.KMSKeyID,
		minioSecret:   l.cfg.MinIOSecret,
	}
	if spec.Region == "" {
		return t
	}
	rc := spec.RegionConfig
	if rc.MinioEndpoint != "" {
		t.minioEndpoint = rc.MinioEndpoint
	}
	if rc.Bucket != "" {
		t.minioBucket = rc.Bucket
	}
	// A per-region Job's KMS key comes from the region entry, even when the
	// scalar default is unset; an explicit empty per-region key disables
	// client-side encryption for that region just like the scalar default.
	t.kmsKeyID = rc.KMSKeyID
	if rc.AccessCredentialSecret != "" {
		t.minioSecret = rc.AccessCredentialSecret
	}
	return t
}

// args renders the lenny-backup CLI arguments for spec.Kind.
func (l *Launcher) args(spec backup.JobSpec) []string {
	args := []string{"--mode=" + modeFor(spec.Kind)}
	switch spec.Kind {
	case backup.JobBackup, backup.JobVerify:
		args = append(args, "--backup-id=$(LENNY_BACKUP_ID)")
	case backup.JobRestore:
		args = append(args, "--restore-id=$(LENNY_RESTORE_ID)")
	}
	if l.target(spec).kmsKeyID != "" {
		args = append(args, "--kms-key-id=$(LENNY_BACKUP_KMS_KEY_ID)")
	}
	return args
}

// env renders the §25.11 Job container environment: the run identifiers,
// the MinIO bucket coordinates, and the Postgres/MinIO/report credentials
// from their Secrets. A per-region Job is scoped to its
// backups.regions.<region> endpoint, bucket, KMS key, and credential
// Secret (§12.8 line 934).
func (l *Launcher) env(spec backup.JobSpec) []corev1.EnvVar {
	t := l.target(spec)
	env := []corev1.EnvVar{
		{Name: "LENNY_BACKUP_ID", Value: spec.BackupID},
		{Name: "LENNY_BACKUP_MINIO_ENDPOINT", Value: t.minioEndpoint},
		{Name: "LENNY_BACKUP_MINIO_BUCKET", Value: t.minioBucket},
	}
	if spec.Region != "" {
		env = append(env, corev1.EnvVar{Name: "LENNY_BACKUP_REGION", Value: spec.Region})
		if len(spec.Shards) > 0 {
			env = append(env, corev1.EnvVar{Name: "LENNY_BACKUP_SHARDS", Value: strings.Join(spec.Shards, ",")})
		}
	}
	if spec.Kind == backup.JobRestore {
		env = append(env, corev1.EnvVar{Name: "LENNY_RESTORE_ID", Value: spec.RestoreID})
	}
	if t.kmsKeyID != "" {
		env = append(env, corev1.EnvVar{Name: "LENNY_BACKUP_KMS_KEY_ID", Value: t.kmsKeyID})
	}
	env = append(env,
		secretEnv("LENNY_BACKUP_POSTGRES_DSN", l.cfg.PostgresSecret, "postgres-dsn"),
		secretEnv("LENNY_BACKUP_MINIO_ACCESS_KEY", t.minioSecret, "minio-access-key"),
		secretEnv("LENNY_BACKUP_MINIO_SECRET_KEY", t.minioSecret, "minio-secret-key"),
	)
	if l.cfg.ReportDSNSecret != "" {
		env = append(env, secretEnv("LENNY_OPS_POSTGRES_DSN", l.cfg.ReportDSNSecret, "report-dsn"))
	}
	return env
}

// modeFor maps a JobKind to the lenny-backup --mode value.
func modeFor(kind backup.JobKind) string {
	switch kind {
	case backup.JobVerify:
		return "verify"
	case backup.JobRestore:
		return "restore"
	case backup.JobRetention:
		return "retention"
	default:
		return "full"
	}
}

// projectStatus maps a Kubernetes Job status onto a backup.BackupJob.
func projectStatus(job *batchv1.Job) backup.BackupJob {
	out := backup.BackupJob{
		JobID:     job.Name,
		BackupID:  job.Spec.Template.Annotations[backupIDAnnotation],
		Active:    int(job.Status.Active),
		Succeeded: int(job.Status.Succeeded),
		Failed:    int(job.Status.Failed),
	}
	switch {
	case hasCondition(job, batchv1.JobComplete):
		out.Phase = "Succeeded"
	case hasCondition(job, batchv1.JobFailed):
		out.Phase = "Failed"
	case out.Active > 0:
		out.Phase = "Active"
	default:
		out.Phase = "Pending"
	}
	return out
}

// hasCondition reports whether job carries condType with status True.
func hasCondition(job *batchv1.Job, condType batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == condType && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// secretEnv builds an EnvVar sourced from a Secret key.
func secretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secret},
				Key:                  key,
			},
		},
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
