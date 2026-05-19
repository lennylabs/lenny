// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §13.28 / §25.11 backup subsystem. The
// chart renders the §25.11 test-restore subsystem from
// charts/lenny/templates/restore-test-cronjob.yaml: a CronJob that runs
// the lenny-backup image in restore-test mode, plus the ServiceAccount,
// Role, and ClusterRole it needs. The CronJob is gated on the
// backups.restoreTest.enabled value.
//
// The test asserts what the dev-mode install genuinely exercises: the
// chart renders the §25.11 restore-test CronJob and its RBAC, both
// referencing the lenny-backup image, and the lenny-backup image is
// loadable on the cluster (a pod scheduled with it runs the binary
// rather than failing the image pull). The full backup-and-restore
// round trip is honest-skipped — see the func-level diagnosis.

package tier5_e2e_kind_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// backupImageRepo is the lenny-backup image repository the e2e values
// overlay pins. install.sh builds and kind-loads lenny-backup:e2e.
const backupImageRepo = "lenny-backup"

// spec: 13.28
// diagnosis: the §25.11 backup subsystem is not rendered or its image
// is not loadable. The test asserts the chart renders the §25.11
// restore-test CronJob and its RBAC referencing the lenny-backup image,
// and that the lenny-backup image is loadable on the cluster. The full
// backup-and-restore round trip is skipped: POST /v1/admin/backups is a
// lenny-ops endpoint the §13.2 NetworkPolicy makes unreachable from an
// in-cluster tier-5 probe, and the dev-mode install leaves
// backups.restoreTest.enabled false, so no backup Job runs.
func TestBackupRestore(t *testing.T) {
	c := kind.InstallLenny(t)

	// --- Assertion 1: the chart renders the §25.11 restore-test
	// subsystem. The CronJob is gated on backups.restoreTest.enabled,
	// which the e2e values overlay leaves at its default (false), so the
	// rendered resources are not on the live cluster. `helm template`
	// with the gate set proves the §25.11 templates render — the
	// CronJob, the ServiceAccount, the Role, and the ClusterRole, all
	// referencing the lenny-backup image.
	rendered, err := helmTemplateRestoreTest(t, c)
	if err != nil {
		t.Skipf("precondition not met: `helm template` did not run (%v); cannot assert the §25.11 "+
			"restore-test subsystem renders", err)
	}
	for _, want := range []string{
		"kind: CronJob",
		"kind: ServiceAccount",
		"kind: Role",
		"kind: ClusterRole",
		"name: lenny-restore-test",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("§25.11 violation: the rendered restore-test template is missing %q; the §25.11 "+
				"backup subsystem did not render.\nrendered:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "--mode=restore-test") {
		t.Errorf("§25.11 violation: the rendered restore-test CronJob does not run the lenny-backup " +
			"image in --mode=restore-test")
	}
	if !strings.Contains(rendered, backupImageRepo+":e2e") {
		t.Errorf("§25.11 violation: the rendered restore-test CronJob does not reference the "+
			"%s:e2e image", backupImageRepo)
	}
	t.Logf("§25.11: the chart renders the restore-test CronJob and its RBAC, referencing %s:e2e",
		backupImageRepo)

	// --- Assertion 2: the restore-test CronJob is correctly absent from
	// the dev-mode install. backups.restoreTest.enabled defaults false
	// and the e2e values overlay does not set it, so no CronJob is on
	// the cluster. A present CronJob would mean the gate leaked.
	cjOut, _ := c.KubectlOut(
		t, "-n", t5SystemNS, "get", "cronjob", "lenny-restore-test",
		"--ignore-not-found", "-o", "name",
	)
	if strings.TrimSpace(cjOut) != "" {
		t.Errorf("§25.11: the lenny-restore-test CronJob is present on the dev-mode cluster (%q); "+
			"backups.restoreTest.enabled defaults false and the e2e overlay does not enable it",
			strings.TrimSpace(cjOut))
	} else {
		t.Logf("§25.11: the restore-test CronJob is absent (backups.restoreTest.enabled is off in the " +
			"dev-mode install, as expected)")
	}

	// --- Assertion 3: the lenny-backup image is loadable on the
	// cluster. install.sh builds lenny-backup:e2e and kind-loads it onto
	// every node. The check schedules a pod that runs the image with
	// imagePullPolicy: Never; a loadable image lets the kubelet start
	// the container, which reaches a terminal state. An unloadable image
	// leaves the container Waiting with ErrImageNeverPull.
	assertBackupImageLoadable(t, c)
}

// helmTemplateRestoreTest runs `helm template` for the Lenny chart with
// the e2e values overlay and backups.restoreTest.enabled=true, showing
// only the restore-test template, and returns the rendered manifest.
func helmTemplateRestoreTest(t *testing.T, c *kind.Cluster) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		return "", err
	}
	root := repoRoot(t)
	cmd := exec.Command(
		"helm", "template", "lenny", root+"/charts/lenny",
		"-f", root+"/tests/testinfra/kind/e2e-values.yaml",
		"--set", "backups.restoreTest.enabled=true",
		"--show-only", "templates/restore-test-cronjob.yaml",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// assertBackupImageLoadable schedules a pod running the lenny-backup
// image with imagePullPolicy: Never, waits for the container to leave
// the Waiting state, and fails the test if the image could not be
// found locally (ErrImageNeverPull / ImagePullBackOff). The container
// is run with --mode=config; the lenny-backup binary exits non-zero on
// missing flags, which is fine — the assertion is that the kubelet
// loaded the image and started the container, not that a backup ran.
func assertBackupImageLoadable(t *testing.T, c *kind.Cluster) {
	t.Helper()
	const pod = "t5-backup-imagecheck"
	const manifest = `apiVersion: v1
kind: Pod
metadata:
  name: t5-backup-imagecheck
  namespace: lenny-system
  labels:
    lenny.dev/test: tier5-backup-image
spec:
  nodeName: lenny-e2e-worker
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: backup
      image: lenny-backup:e2e
      imagePullPolicy: Never
      args: ["--mode", "config"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        capabilities:
          drop: ["ALL"]
`
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to schedule the lenny-backup image-check pod: %v\n%s", err, out)
	}

	// Poll the container state. A loadable image lets the kubelet start
	// the container, so it leaves Waiting for Running or Terminated. An
	// unloadable image keeps the container Waiting with ErrImageNeverPull.
	deadline := time.Now().Add(90 * time.Second)
	for {
		waitReason, _ := c.KubectlOut(
			t, "-n", t5SystemNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.waiting.reason}",
		)
		termReason, _ := c.KubectlOut(
			t, "-n", t5SystemNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.terminated.reason}",
		)
		runStartedAt, _ := c.KubectlOut(
			t, "-n", t5SystemNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.running.startedAt}",
		)
		wr := strings.TrimSpace(waitReason)
		switch {
		case wr == "ErrImageNeverPull" || wr == "ImagePullBackOff" || wr == "ErrImagePull":
			t.Fatalf("§25.11: the lenny-backup:e2e image is not loadable on the cluster — the kubelet "+
				"reports the container Waiting with %q. install.sh builds and kind-loads lenny-backup:e2e; "+
				"a missing image means the build or `kind load` step did not run", wr)
		case strings.TrimSpace(termReason) != "" || strings.TrimSpace(runStartedAt) != "":
			// The container started (Running) or finished (Terminated):
			// the image was found locally and the kubelet ran it.
			t.Logf("§25.11: the lenny-backup:e2e image is loadable — the kubelet started the container "+
				"(terminated reason %q, running startedAt %q)",
				strings.TrimSpace(termReason), strings.TrimSpace(runStartedAt))
			return
		}
		if time.Now().After(deadline) {
			desc, _ := c.KubectlOut(t, "-n", t5SystemNS, "describe", "pod", pod)
			t.Fatalf("§25.11: the lenny-backup image-check pod's container did not leave the Waiting "+
				"state within 90s (last waiting reason %q); cannot confirm the image is loadable\n"+
				"--- describe ---\n%s", wr, desc)
		}
		time.Sleep(3 * time.Second)
	}
}
