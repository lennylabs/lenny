// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test extending the §25.11 restore-test coverage in
// backup_test.go. That file proves the chart renders the
// lenny-restore-test CronJob and its RBAC and that the lenny-backup
// image is loadable; it does not drive an actual CronJob run. This file
// closes that residual gap: whether the CronJob's Job actually
// dispatches on a real cluster and records its outcome in
// ops_restore_test_results through the real download-from-MinIO,
// verify, and Postgres-report path lenny-backup performs in
// --mode=restore-test.
//
// The seeded backup is a config-only archive (no postgres.tar entry),
// so this test does not depend on the lenny-backup image packaging
// pg_dump/pg_restore/psql, which is a separate, already-tracked gap
// (the image is built from a distroless base with no Postgres client
// tools). runner.RunRestoreTest's per-shard verify/restore loops are
// no-ops when ExtractPostgresDumps finds no postgres.tar, so a
// config-only backup exercises the CronJob dispatch, the MinIO
// download, and the Postgres result-recording steps without touching
// the packaging gap.

package tier5_e2e_kind_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// restoreTestDispatchBucket is the MinIO bucket this test seeds and the
// restore-test overlay below points backups.onDemand.minioBucket at. It
// is scoped to this test so a concurrent run of the real backup Job
// (also gated off by default in the e2e values) cannot collide with it.
const restoreTestDispatchBucket = "lenny-restore-test-dispatch"

// restoreTestDispatchObjectKey is the seeded backup's object path within
// restoreTestDispatchBucket.
const restoreTestDispatchObjectKey = "seed/config-only.tar.gz"

// restoreTestDispatchBackupID is the ops_backups row id the seeded
// backup targets. backups.restoreTest.backupSelector defaults to
// "latest-full", so the row is typed "full".
const restoreTestDispatchBackupID = "t5-restore-test-dispatch-seed"

// spec: §25.11 lines 4128-4133 ("Test Restore ... selects the latest
// backup matching the selector ... restores it into the scratch
// Postgres ... and records the outcome") and spec/25_agent-operability.md:4260
// ("POST /v1/admin/backups/{id}/verify?mode=test-restore runs a Job
// that actually restores the backup to a temporary namespace ... and
// reports the outcome").
// diagnosis: a failure here means the lenny-restore-test CronJob either
// did not dispatch a Job on the real cluster, the Job could not
// download and verify the seeded backup from MinIO, or the outcome did
// not land in ops_restore_test_results through the real
// lenny-backup-to-Postgres reporting path.
func TestRestoreTestCronJobDispatchWritesResult(t *testing.T) {
	c := kind.InstallLenny(t)
	if !t5DeploymentReady(t, c, "lenny-postgres") {
		t.Skip("precondition not met: lenny-postgres is not Ready; ops_restore_test_results is Postgres-backed")
	}
	if !t5DeploymentReady(t, c, "lenny-minio") {
		t.Skip("precondition not met: lenny-minio is not Ready; the restore-test Job downloads the seeded backup from it")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("precondition not met: helm is not on PATH; cannot render the §25.11 restore-test overlay")
	}
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("precondition not met: could not resolve the lenny-postgres pod IP")
	}

	seedRestoreTestBackupObject(t, c, restoreTestDispatchBucket, restoreTestDispatchObjectKey)

	t5RunPsqlExec(t, c, pgIP, "t5-restore-test-dispatch-seed-row", fmt.Sprintf(
		"INSERT INTO ops_backups (id, type, status, storage_path, checksum, started_by, job_id, "+
			"platform_version, schema_version, completed_at) VALUES "+
			"('%s', 'full', 'completed', '%s', '', 't5-e2e', 't5-e2e-seed', 'e2e', 1, now()) "+
			"ON CONFLICT (id) DO UPDATE SET status = 'completed', storage_path = EXCLUDED.storage_path, "+
			"completed_at = now();",
		restoreTestDispatchBackupID, restoreTestDispatchObjectKey,
	))
	t.Cleanup(func() {
		t5RunPsqlExec(t, c, pgIP, "t5-restore-test-dispatch-cleanup", fmt.Sprintf(
			"DELETE FROM ops_restore_test_results WHERE backup_id = '%s'; "+
				"DELETE FROM ops_backups WHERE id = '%s';",
			restoreTestDispatchBackupID, restoreTestDispatchBackupID,
		))
	})

	overlay := renderRestoreTestOverlay(t, c)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, overlay) })
	if out, err := c.ApplyStdin(t, overlay); err != nil {
		t.Fatalf("failed to apply the §25.11 restore-test Secrets/CronJob/RBAC overlay: %v\n%s", out, err)
	}

	// The rendered CronJob's pod template carries no CA-bundle mount for
	// its own MinIO TLS client (unlike the chart's minio-bucket-lifecycle
	// Job, which mounts minio.tls.caBundleConfigMap for the identical
	// self-signed-CA e2e MinIO). Patching it in here is a test-harness
	// adaptation to this cluster's self-managed MinIO cert, not a
	// product change; charts/lenny/templates/restore-test-cronjob.yaml
	// itself is untouched.
	patchRestoreTestCronJobForMinIOCA(t, c)

	const jobName = "t5-restore-test-dispatch-run"
	if out, err := c.KubectlOut(t, "-n", t5SystemNS, "delete", "job", jobName, "--ignore-not-found", "--wait=true"); err != nil {
		t.Fatalf("failed to clear a stale %s Job: %v\n%s", jobName, err, out)
	}
	if out, err := c.KubectlOut(t, "-n", t5SystemNS, "create", "job", jobName,
		"--from=cronjob/lenny-restore-test"); err != nil {
		t.Fatalf("failed to trigger a Job from the lenny-restore-test CronJob: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = c.KubectlOut(t, "-n", t5SystemNS, "delete", "job", jobName, "--ignore-not-found")
	})

	podName := waitForJobPodName(t, c, jobName)
	phase := waitForPodTerminalPhase(t, c, podName)
	if phase != "Succeeded" {
		logs, _ := c.KubectlOut(t, "-n", t5SystemNS, "logs", podName)
		t.Fatalf("§25.11: the restore-test Job pod %q did not succeed (phase %q); the CronJob did not "+
			"complete a dispatch run\n--- logs ---\n%s", podName, phase, logs)
	}
	t.Logf("§25.11: the lenny-restore-test CronJob dispatched Job %q, pod %q ran to completion",
		jobName, podName)

	row := t5RunPsqlQuery(t, c, pgIP, "t5-restore-test-dispatch-result", fmt.Sprintf(
		"SELECT success||'|'||backup_id FROM ops_restore_test_results WHERE id = '%s'", podName,
	))
	fields := strings.Split(strings.TrimSpace(row), "|")
	if len(fields) != 2 || fields[0] != "true" || fields[1] != restoreTestDispatchBackupID {
		t.Fatalf("§25.11: ops_restore_test_results has no successful row for restore-test pod %q "+
			"(got %q); the dispatched Job did not report its outcome through the real "+
			"lenny-backup-to-Postgres reporting path", podName, row)
	}
	t.Logf("§25.11: ops_restore_test_results recorded a successful run (id=%s, backup_id=%s) for the "+
		"CronJob-dispatched pod", podName, fields[1])
}

// configOnlyBackupArchive returns a minimal gzip archive with no
// postgres.tar entry, matching the §25.11 "config-only archive carries
// no Postgres component" case (runner.TarGzOpener.ExtractPostgresDumps
// returns nil, nil when postgres.tar is absent). It lets this test's
// restore-test dispatch complete without depending on the lenny-backup
// image packaging pg_dump/pg_restore/psql.
func configOnlyBackupArchive(t *testing.T) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.Close(); err != nil {
		t.Fatalf("build empty tar: %v", err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("gzip empty tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return gzBuf.Bytes()
}

// seedRestoreTestBackupObject uploads a configOnlyBackupArchive to
// bucket/key on the e2e MinIO via a one-shot mc pod (the same tool, and
// the same lenny-minio-ca CA-bundle mount, as the chart's own
// minio-bucket-lifecycle-job.yaml), creating the bucket first.
func seedRestoreTestBackupObject(t *testing.T, c *kind.Cluster, bucket, key string) {
	t.Helper()
	const pod = "t5-restore-test-seed-upload"
	encoded := base64.StdEncoding.EncodeToString(configOnlyBackupArchive(t))
	manifest := `apiVersion: v1
kind: Pod
metadata:
  name: ` + pod + `
  namespace: ` + t5SystemNS + `
  labels:
    lenny.dev/test: tier5-restore-test-seed
spec:
  nodeName: ` + t5ProbeNode + `
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: mc
      image: minio/mc:latest
      imagePullPolicy: IfNotPresent
      env:
        - name: MINIO_ACCESS_KEY
          value: lennyminio
        - name: MINIO_SECRET_KEY
          value: lennyminio123
        - name: MC_HOST_lenny
          value: "https://$(MINIO_ACCESS_KEY):$(MINIO_SECRET_KEY)@lenny-minio.` + t5SystemNS + `.svc:9000"
        - name: MC_CONFIG_DIR
          value: /tmp/.mc
        - name: SSL_CERT_DIR
          value: /etc/lenny/minio-ca
      command: ["/bin/sh", "-c"]
      args:
        - |
          set -eu
          echo ` + encoded + ` | base64 -d > /tmp/seed.tar.gz
          mc mb --ignore-existing lenny/` + bucket + `
          mc cp /tmp/seed.tar.gz lenny/` + bucket + `/` + key + `
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: 1000
        capabilities:
          drop: ["ALL"]
      volumeMounts:
        - name: tmp
          mountPath: /tmp
        - name: minio-ca
          mountPath: /etc/lenny/minio-ca
          readOnly: true
  volumes:
    - name: tmp
      emptyDir: {}
    - name: minio-ca
      configMap:
        name: lenny-minio-ca
        items:
          - key: ca.crt
            path: ca.crt
`
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to schedule the MinIO seed-upload pod: %v\n%s", err, out)
	}
	if phase := waitForPodTerminalPhase(t, c, pod); phase != "Succeeded" {
		logs, _ := c.KubectlOut(t, "-n", t5SystemNS, "logs", pod)
		t.Fatalf("MinIO seed-upload pod %q did not succeed (phase %q):\n%s", pod, phase, logs)
	}
}

// renderRestoreTestOverlay runs `helm template` for the Lenny chart with
// the e2e values overlay plus the values needed to point the §25.11
// restore-test CronJob at the e2e Postgres and MinIO, and returns the
// rendered Secrets, ServiceAccount, RBAC, and CronJob manifest. The
// stock e2e install leaves backups.restoreTest.enabled false (proven
// absent by TestBackupRestore in backup_test.go), so this is the only
// place in the tier5 suite that renders it with the gate on.
func renderRestoreTestOverlay(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command(
		"helm", "template", "lenny", root+"/charts/lenny",
		"--namespace", t5SystemNS,
		"-f", root+"/tests/testinfra/kind/e2e-values.yaml",
		"--set", "backups.postgres.dsn=postgres://lenny:lenny@lenny-postgres."+t5SystemNS+".svc:5432/lenny?sslmode=disable",
		"--set", "backups.minio.accessKey=lennyminio",
		"--set", "backups.minio.secretKey=lennyminio123",
		"--set", "backups.onDemand.minioEndpoint=lenny-minio."+t5SystemNS+".svc:9000",
		"--set", "backups.onDemand.minioBucket="+restoreTestDispatchBucket,
		"--set", "backups.restoreTest.enabled=true",
		"--set", "backups.restoreTest.backupSelector=latest-full",
		"--show-only", "templates/backup-secrets.yaml",
		"--show-only", "templates/restore-test-cronjob.yaml",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template the §25.11 restore-test overlay: %v\n%s", err, out)
	}
	return string(out)
}

// patchRestoreTestCronJobForMinIOCA adds an SSL_CERT_DIR env var and a
// lenny-minio-ca volume mount to the lenny-restore-test CronJob's pod
// template. charts/lenny/templates/restore-test-cronjob.yaml renders no
// CA-bundle option for the lenny-backup image's own MinIO TLS client
// (unlike minio-bucket-lifecycle-job.yaml's minio.tls.caBundleConfigMap
// handling for mc), so a restore-test Job cannot verify TLS against a
// self-managed MinIO whose serving cert is issued by a private CA — the
// e2e MinIO's cert-manager-issued cert. This patch is scoped to the Job
// this test creates and does not modify the chart template.
func patchRestoreTestCronJobForMinIOCA(t *testing.T, c *kind.Cluster) {
	t.Helper()
	patch := `[
		{"op":"add","path":"/spec/jobTemplate/spec/template/spec/containers/0/env/-",
		 "value":{"name":"SSL_CERT_DIR","value":"/etc/lenny/minio-ca"}},
		{"op":"add","path":"/spec/jobTemplate/spec/template/spec/containers/0/volumeMounts/-",
		 "value":{"name":"minio-ca","mountPath":"/etc/lenny/minio-ca","readOnly":true}},
		{"op":"add","path":"/spec/jobTemplate/spec/template/spec/volumes/-",
		 "value":{"name":"minio-ca","configMap":{"name":"lenny-minio-ca","items":[{"key":"ca.crt","path":"ca.crt"}]}}}
	]`
	if out, err := c.KubectlOut(t, "-n", t5SystemNS, "patch", "cronjob", "lenny-restore-test",
		"--type=json", "-p="+patch); err != nil {
		t.Fatalf("failed to patch the lenny-restore-test CronJob with the e2e MinIO CA bundle: %v\n%s", err, out)
	}
}

// waitForJobPodName polls for the pod a Job created and returns its
// name once one exists, failing the test if none appears within the
// deadline.
func waitForJobPodName(t *testing.T, c *kind.Cluster, jobName string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, _ := c.KubectlOut(t, "-n", t5SystemNS, "get", "pods",
			"-l", "job-name="+jobName, "-o", "jsonpath={.items[0].metadata.name}")
		if name := strings.TrimSpace(out); name != "" {
			return name
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pod appeared for Job %q within 60s", jobName)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForPodTerminalPhase polls pod until it reaches a terminal phase
// (Succeeded or Failed) and returns that phase, failing the test if it
// does not terminate within the deadline.
func waitForPodTerminalPhase(t *testing.T, c *kind.Cluster, pod string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, _ := c.KubectlOut(t, "-n", t5SystemNS, "get", "pod", pod, "-o", "jsonpath={.status.phase}")
		phase := strings.TrimSpace(out)
		if phase == "Succeeded" || phase == "Failed" {
			return phase
		}
		if time.Now().After(deadline) {
			desc, _ := c.KubectlOut(t, "-n", t5SystemNS, "describe", "pod", pod)
			t.Fatalf("pod %q did not reach a terminal phase within 90s (last phase %q)\n--- describe ---\n%s",
				pod, phase, desc)
		}
		time.Sleep(2 * time.Second)
	}
}

// spec: §25.11 lines 4098, 4254-4256 ("emits lenny_restore_test_artifact_success_rate ...
// and lenny_restore_test_artifact_missing_total"); the restoretest.Store
// doc comment (pkg/ops/backup/restoretest/restoretest.go) — "the leader
// lenny-ops replica reads the latest Result on each scrape and
// re-exposes it as the §25.11 / §16.1 restore-test Prometheus series."
// diagnosis: a failure here (once the skip below is lifted) means
// lenny-ops's leader-gated restore-test-metrics sampler did not
// republish the CronJob-recorded ops_restore_test_results row as the
// lenny_restore_test_success / _duration_seconds gauges on /metrics.
func TestRestoreTestResultReachesLennyOpsMetrics(t *testing.T) {
	// Held non-blocking: charts/lenny/templates/ops-deployment.yaml never
	// renders a Postgres DSN for lenny-ops (no --postgres-dsn flag, no
	// LENNY_POSTGRES_DSN env var, under any values combination — grep the
	// template) — a stock chart install always runs lenny-ops in the
	// §25.4 degraded single-process mode, already reviewed and closed as
	// deliberate (F-25.2.16 in BUILD-GAPS.md). The tier5 e2e install
	// follows that stock render, so lenny-ops here never reads
	// ops_restore_test_results and this metric can never appear on its
	// /metrics. Observing it would require patching the shared lenny-ops
	// Deployment to add a Postgres DSN, which is out of place for one
	// test to do to infrastructure every other tier5 test in this binary
	// also depends on.
	t.Skip("charts/lenny/templates/ops-deployment.yaml renders no Postgres DSN for lenny-ops under any " +
		"values combination, so the stock e2e install runs it in the degraded single-process mode " +
		"(already reviewed as deliberate) where it never reads ops_restore_test_results; observing " +
		"the republished gauge would require mutating the shared lenny-ops Deployment for the whole " +
		"test binary run")

	c := kind.InstallLenny(t)
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Fatalf("could not resolve the lenny-postgres pod IP")
	}

	seedRestoreTestBackupObject(t, c, restoreTestDispatchBucket, restoreTestDispatchObjectKey)
	t5RunPsqlExec(t, c, pgIP, "t5-restore-test-metrics-seed-row", fmt.Sprintf(
		"INSERT INTO ops_backups (id, type, status, storage_path, checksum, started_by, job_id, "+
			"platform_version, schema_version, completed_at) VALUES "+
			"('%s', 'full', 'completed', '%s', '', 't5-e2e', 't5-e2e-seed', 'e2e', 1, now());",
		restoreTestDispatchBackupID, restoreTestDispatchObjectKey,
	))

	overlay := renderRestoreTestOverlay(t, c)
	if out, err := c.ApplyStdin(t, overlay); err != nil {
		t.Fatalf("apply restore-test overlay: %v\n%s", err, out)
	}
	patchRestoreTestCronJobForMinIOCA(t, c)

	const jobName = "t5-restore-test-metrics-run"
	if out, err := c.KubectlOut(t, "-n", t5SystemNS, "create", "job", jobName,
		"--from=cronjob/lenny-restore-test"); err != nil {
		t.Fatalf("trigger restore-test Job: %v\n%s", err, out)
	}
	podName := waitForJobPodName(t, c, jobName)
	waitForPodTerminalPhase(t, c, podName)

	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()
	deadline := time.Now().Add(90 * time.Second)
	for {
		body := t5FetchMetrics(t, baseURL)
		if strings.Contains(body, "lenny_restore_test_success 1") {
			t.Logf("§25.11: lenny-ops /metrics republishes lenny_restore_test_success 1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("§25.11: lenny-ops /metrics did not republish lenny_restore_test_success 1 within 90s")
		}
		time.Sleep(3 * time.Second)
	}
}

// t5FetchMetrics performs an HTTP GET against baseURL+"/metrics" and
// returns the response body, failing the test on a transport error.
func t5FetchMetrics(t *testing.T, baseURL string) string {
	t.Helper()
	hc := &http.Client{Timeout: 10 * time.Second}
	res, err := hc.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	return string(body)
}
