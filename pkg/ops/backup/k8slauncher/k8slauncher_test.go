// SPDX-License-Identifier: MIT

package k8slauncher

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// newTestLauncher builds a Launcher over a fake clientset with a
// deterministic Job-name suffix so assertions can name the Job.
func newTestLauncher(t *testing.T, objs ...metav1.Object) (*Launcher, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	l, err := New(Config{
		Clientset:       cs,
		Namespace:       "lenny-system",
		Image:           "registry.example/lenny-backup:v1",
		MinIOEndpoint:   "minio:9000",
		MinIOBucket:     "lenny-backups",
		KMSKeyID:        "kms-key-1",
		ReportDSNSecret: "lenny-backup-report",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.cfg.newSuffix = func() string { return "fixed" }
	return l, cs
}

// TestLaunchRendersJobPodSpecification asserts the §25.11 Job Pod
// Specification: restartPolicy Never, backoffLimit 3,
// ttlSecondsAfterFinished 3600, activeDeadlineSeconds 7200, the
// lenny-backup-sa ServiceAccount, the non-root read-only-rootfs security
// context with a writable /tmp emptyDir, the app: lenny-backup label the
// NetworkPolicy selects, and the lenny.dev/backup-id correlation
// annotation.
//
// spec: §25.11 lines 3981-3994.
func TestLaunchRendersJobPodSpecification_spec_25_11(t *testing.T) {
	l, cs := newTestLauncher(t)
	launched, err := l.Launch(context.Background(), backup.JobSpec{
		Kind: backup.JobBackup, BackupID: "bkp-1", BackupType: "full",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launched.JobID != "lenny-backup-fixed" {
		t.Fatalf("JobID = %q, want lenny-backup-fixed", launched.JobID)
	}
	job, err := cs.BatchV1().Jobs("lenny-system").Get(context.Background(), launched.JobID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created Job: %v", err)
	}

	if got := *job.Spec.BackoffLimit; got != 3 {
		t.Errorf("backoffLimit = %d, want 3", got)
	}
	if got := *job.Spec.TTLSecondsAfterFinished; got != 3600 {
		t.Errorf("ttlSecondsAfterFinished = %d, want 3600", got)
	}
	if got := *job.Spec.ActiveDeadlineSeconds; got != 7200 {
		t.Errorf("activeDeadlineSeconds = %d, want 7200", got)
	}
	pod := job.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.ServiceAccountName != "lenny-backup-sa" {
		t.Errorf("serviceAccountName = %q, want lenny-backup-sa", pod.ServiceAccountName)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Errorf("pod securityContext is not runAsNonRoot")
	}
	c := pod.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("container is not readOnlyRootFilesystem")
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("container allows privilege escalation")
	}
	if len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("container does not drop ALL capabilities: %v", c.SecurityContext.Capabilities.Drop)
	}
	if c.Image != "registry.example/lenny-backup:v1" {
		t.Errorf("image = %q", c.Image)
	}
	if !hasVolumeMount(c.VolumeMounts, "tmp", "/tmp") {
		t.Errorf("no writable /tmp mount: %v", c.VolumeMounts)
	}
	if !hasEmptyDir(pod.Volumes, "tmp") {
		t.Errorf("tmp volume is not an emptyDir: %v", pod.Volumes)
	}
	if got := job.Spec.Template.Labels[appLabel]; got != appValue {
		t.Errorf("pod app label = %q, want %s (the NetworkPolicy podSelector)", got, appValue)
	}
	if got := job.Spec.Template.Annotations[backupIDAnnotation]; got != "bkp-1" {
		t.Errorf("backup-id annotation = %q, want bkp-1", got)
	}
}

// TestLaunchEnvAndArgsByKind asserts the §25.11 per-mode CLI args and the
// credential env wiring: the secret-sourced Postgres/MinIO/report keys and
// the run identifiers.
//
// spec: §25.11 lines 3990-3997.
func TestLaunchEnvAndArgsByKind_spec_25_11(t *testing.T) {
	l, cs := newTestLauncher(t)
	cases := []struct {
		kind     backup.JobKind
		wantMode string
	}{
		{backup.JobBackup, "--mode=full"},
		{backup.JobVerify, "--mode=verify"},
		{backup.JobRestore, "--mode=restore"},
		{backup.JobRetention, "--mode=retention"},
	}
	for _, tc := range cases {
		l.cfg.newSuffix = func() string { return string(tc.kind) }
		spec := backup.JobSpec{Kind: tc.kind, BackupID: "bkp-9", RestoreID: "rst-9"}
		launched, err := l.Launch(context.Background(), spec)
		if err != nil {
			t.Fatalf("Launch %s: %v", tc.kind, err)
		}
		job, _ := cs.BatchV1().Jobs("lenny-system").Get(context.Background(), launched.JobID, metav1.GetOptions{})
		c := job.Spec.Template.Spec.Containers[0]
		if c.Args[0] != tc.wantMode {
			t.Errorf("%s args[0] = %q, want %q", tc.kind, c.Args[0], tc.wantMode)
		}
		// Every Job sources the backup credentials from their Secrets.
		assertSecretEnv(t, c.Env, "LENNY_BACKUP_POSTGRES_DSN", "lenny-backup-postgres", "postgres-dsn")
		assertSecretEnv(t, c.Env, "LENNY_BACKUP_MINIO_ACCESS_KEY", "lenny-backup-minio", "minio-access-key")
		assertSecretEnv(t, c.Env, "LENNY_OPS_POSTGRES_DSN", "lenny-backup-report", "report-dsn")
		if tc.kind == backup.JobRestore && !hasEnv(c.Env, "LENNY_RESTORE_ID", "rst-9") {
			t.Errorf("restore Job missing LENNY_RESTORE_ID env")
		}
		// The KMS-key flag and env appear because the launcher was given a key.
		if !hasArg(c.Args, "--kms-key-id=$(LENNY_BACKUP_KMS_KEY_ID)") {
			t.Errorf("%s missing kms-key arg: %v", tc.kind, c.Args)
		}
	}
}

// TestPerRegionJobScopedToRegionEndpoint_spec_12_8_934 asserts a
// per-region backup Job is scoped to its backups.regions.<region> MinIO
// endpoint, bucket, KMS key, and access-credential Secret, so one
// region's Job cannot authenticate to or write into another region's
// MinIO. The region is recorded as a pod annotation and env, and the
// covered shards are passed as env.
//
// spec: §12.8 lines 934-935.
func TestPerRegionJobScopedToRegionEndpoint_spec_12_8_934(t *testing.T) {
	l, cs := newTestLauncher(t)
	l.cfg.newSuffix = func() string { return "eu" }
	launched, err := l.Launch(context.Background(), backup.JobSpec{
		Kind:       backup.JobBackup,
		BackupID:   "bkp-eu",
		BackupType: "full",
		Region:     "eu-west-1",
		Shards:     []string{"shard-eu-a", "shard-eu-b"},
		RegionConfig: backup.RegionBackupConfig{
			MinioEndpoint:          "minio.eu:9000",
			Bucket:                 "backups-eu",
			KMSKeyID:               "kms-eu",
			AccessCredentialSecret: "lenny-backup-minio-eu",
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job, _ := cs.BatchV1().Jobs("lenny-system").Get(context.Background(), launched.JobID, metav1.GetOptions{})
	c := job.Spec.Template.Spec.Containers[0]

	// The MinIO coordinates come from the region entry, not the launcher
	// defaults (minio:9000 / lenny-backups / kms-key-1).
	if !hasEnv(c.Env, "LENNY_BACKUP_MINIO_ENDPOINT", "minio.eu:9000") {
		t.Errorf("region Job endpoint not scoped: %+v", c.Env)
	}
	if !hasEnv(c.Env, "LENNY_BACKUP_MINIO_BUCKET", "backups-eu") {
		t.Errorf("region Job bucket not scoped: %+v", c.Env)
	}
	if !hasEnv(c.Env, "LENNY_BACKUP_KMS_KEY_ID", "kms-eu") {
		t.Errorf("region Job KMS key not scoped: %+v", c.Env)
	}
	if !hasEnv(c.Env, "LENNY_BACKUP_REGION", "eu-west-1") {
		t.Errorf("region Job missing LENNY_BACKUP_REGION: %+v", c.Env)
	}
	if !hasEnv(c.Env, "LENNY_BACKUP_SHARDS", "shard-eu-a,shard-eu-b") {
		t.Errorf("region Job missing/incorrect LENNY_BACKUP_SHARDS: %+v", c.Env)
	}
	// The MinIO credentials source from the region's access-credential
	// Secret, not the default lenny-backup-minio Secret.
	assertSecretEnv(t, c.Env, "LENNY_BACKUP_MINIO_ACCESS_KEY", "lenny-backup-minio-eu", "minio-access-key")
	assertSecretEnv(t, c.Env, "LENNY_BACKUP_MINIO_SECRET_KEY", "lenny-backup-minio-eu", "minio-secret-key")
	// The region is recorded as a pod annotation.
	if got := job.Spec.Template.Annotations[backupRegionAnnotation]; got != "eu-west-1" {
		t.Errorf("backup-region annotation = %q, want eu-west-1", got)
	}
}

// TestRetentionJobCarriesNoBackupIDAnnotation asserts a retention Job (no
// BackupID) renders without the correlation annotation, so the orphan
// reconciler does not treat it as a stray backup Job.
func TestRetentionJobCarriesNoBackupIDAnnotation(t *testing.T) {
	l, cs := newTestLauncher(t)
	launched, err := l.Launch(context.Background(), backup.JobSpec{Kind: backup.JobRetention})
	if err != nil {
		t.Fatalf("Launch retention: %v", err)
	}
	job, _ := cs.BatchV1().Jobs("lenny-system").Get(context.Background(), launched.JobID, metav1.GetOptions{})
	if _, ok := job.Spec.Template.Annotations[backupIDAnnotation]; ok {
		t.Errorf("retention Job should carry no backup-id annotation")
	}
}

// TestJobStatusProjection asserts the K8s Job status → backup.BackupJob
// mapping the §25.11 restore-completion reconciler reads.
//
// spec: §25.11 line 4194.
func TestJobStatusProjection_spec_25_11(t *testing.T) {
	l, cs := newTestLauncher(t)
	// A completed Job.
	complete := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-restore-done", Namespace: "lenny-system"},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{backupIDAnnotation: "rst-1"}}}},
		Status: batchv1.JobStatus{
			Succeeded:  1,
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
		},
	}
	if _, err := cs.BatchV1().Jobs("lenny-system").Create(context.Background(), complete, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed Job: %v", err)
	}
	got, err := l.JobStatus(context.Background(), "lenny-restore-done")
	if err != nil {
		t.Fatalf("JobStatus: %v", err)
	}
	if got.Phase != "Succeeded" || got.Succeeded != 1 || got.BackupID != "rst-1" {
		t.Errorf("JobStatus = %+v, want Succeeded/1/rst-1", got)
	}

	// A failed Job.
	failed := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-restore-fail", Namespace: "lenny-system"},
		Status: batchv1.JobStatus{
			Failed:     1,
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}},
		},
	}
	_, _ = cs.BatchV1().Jobs("lenny-system").Create(context.Background(), failed, metav1.CreateOptions{})
	gotFail, _ := l.JobStatus(context.Background(), "lenny-restore-fail")
	if gotFail.Phase != "Failed" || gotFail.Failed != 1 {
		t.Errorf("failed JobStatus = %+v, want Failed/1", gotFail)
	}

	// A missing Job maps to ErrNotFound.
	if _, err := l.JobStatus(context.Background(), "nope"); err != backup.ErrNotFound {
		t.Errorf("JobStatus(missing) err = %v, want ErrNotFound", err)
	}
}

// TestListAndDeleteManagedJobs asserts the §25.11 orphan-reconciler seam:
// the launcher lists only app: lenny-backup Jobs with their backup-id
// annotation, and DeleteJob is idempotent against an already-swept Job.
//
// spec: §25.11 lines 3976-3978.
func TestListAndDeleteManagedJobs_spec_25_11(t *testing.T) {
	l, _ := newTestLauncher(t)
	if _, err := l.Launch(context.Background(), backup.JobSpec{Kind: backup.JobBackup, BackupID: "bkp-7"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	managed, err := l.ListManagedJobs(context.Background())
	if err != nil {
		t.Fatalf("ListManagedJobs: %v", err)
	}
	if len(managed) != 1 || managed[0].BackupID != "bkp-7" {
		t.Fatalf("ListManagedJobs = %+v, want one bkp-7", managed)
	}
	if err := l.DeleteJob(context.Background(), managed[0].JobID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	// Idempotent: deleting again is a no-op.
	if err := l.DeleteJob(context.Background(), managed[0].JobID); err != nil {
		t.Errorf("DeleteJob(already deleted) = %v, want nil", err)
	}
}

// TestNewValidates asserts the constructor rejects a missing required
// dependency.
func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Namespace: "x", Image: "y"}); err == nil {
		t.Error("New with no Clientset should fail")
	}
	if _, err := New(Config{Clientset: fake.NewSimpleClientset(), Image: "y"}); err == nil {
		t.Error("New with no Namespace should fail")
	}
	if _, err := New(Config{Clientset: fake.NewSimpleClientset(), Namespace: "x"}); err == nil {
		t.Error("New with no Image should fail")
	}
}

func hasVolumeMount(mounts []corev1.VolumeMount, name, path string) bool {
	for _, m := range mounts {
		if m.Name == name && m.MountPath == path {
			return true
		}
	}
	return false
}

func hasEmptyDir(vols []corev1.Volume, name string) bool {
	for _, v := range vols {
		if v.Name == name && v.EmptyDir != nil {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func hasEnv(env []corev1.EnvVar, name, value string) bool {
	for _, e := range env {
		if e.Name == name && e.Value == value {
			return true
		}
	}
	return false
}

func assertSecretEnv(t *testing.T, env []corev1.EnvVar, name, secret, key string) {
	t.Helper()
	for _, e := range env {
		if e.Name != name {
			continue
		}
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Errorf("env %s is not secret-sourced", name)
			return
		}
		ref := e.ValueFrom.SecretKeyRef
		if ref.Name != secret || ref.Key != key {
			t.Errorf("env %s sources %s/%s, want %s/%s", name, ref.Name, ref.Key, secret, key)
		}
		return
	}
	t.Errorf("env %s not found", name)
}
