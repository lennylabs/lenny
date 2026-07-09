// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §17.8.6 / §25.8 air-gapped / mirrored-
// registry install path. Prior coverage of this path was a single AWS
// ECR image-pull happy path (tests/tier6_e2e_cloud/eks_platform_test.go
// TestCloudECRImagePullSucceeds); nothing installed the chart with a
// single-source mirror override and confirmed a disconnected install
// (no upstream registry reachable) starts every named component.
//
// This test does not attempt to block egress with a NetworkPolicy: a
// Kubernetes NetworkPolicy governs pod-to-pod and pod-to-external
// traffic through the CNI, not the node kubelet's image-pull path, so
// it cannot genuinely gate whether a pull reaches a public registry.
// Instead it reproduces the state a real air-gapped cluster is in once
// its mirror has been populated and each image pulled once: every node
// carries the image under the mirror-composed name in its local
// containerd content store, and every pod runs with imagePullPolicy:
// Never so no live registry access is possible at all. This mirrors
// the technique tests/testinfra/kind/install.sh already uses to run
// the whole e2e cluster without any live registry pull, and the one
// tests/tier5_e2e_kind/backup_test.go already uses to confirm a single
// image is loadable — generalized here to every §17.8.6-named component
// under a genuine platform.registry.url mirror override.
package tier5_e2e_kind_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// airgapMirrorHost stands in for an operator's internal air-gap mirror
// (§17.8.6: "mirror all Lenny-published images into a private
// registry, set platform.registry.url to that registry"). It resolves
// on no real network; every check pod below runs with imagePullPolicy:
// Never, so the hostname is never dialed. The mirror's presence is
// instead simulated by kind-loading each image under this prefix,
// standing in for an operator's `crane copy`/`skopeo copy` mirror
// population step.
const airgapMirrorHost = "airgap-mirror.internal"

// airgapMirrorNS is the scratch namespace this test creates and tears
// down. It is separate from lenny-system so the throwaway image-check
// pods cannot be mistaken for, or interfere with, the real
// control-plane Deployments the shared "lenny" Helm release manages.
const airgapMirrorNS = "lenny-airgap-mirror-test"

// airgapComponents are the five short names §17.8.6 names explicitly:
// "ensuring the gateway, lenny-ops, controllers, lenny-backup, and the
// warm-pool controller all honor the same registry configuration."
// Each short name is also the local image repository
// tests/testinfra/kind/install.sh builds and kind-loads (its BINARIES
// list), so no separate name mapping is needed.
var airgapComponents = []string{
	"lenny-gateway",
	"lenny-ops",
	"lenny-controller",
	"lenny-backup",
	"lenny-pool-scaling-controller",
}

// airgapTemplateFiles are the chart templates that render each of
// airgapComponents' container image via the lenny.componentImage
// helper (charts/lenny/templates/_helpers.tpl).
var airgapTemplateFiles = []string{
	"templates/gateway-deployment.yaml",
	"templates/ops-deployment.yaml",
	"templates/controller-deployment.yaml",
	"templates/backup-job.yaml",
	"templates/pool-scaling-controller-deployment.yaml",
}

// spec: 17.8.6 ("Air-gapped deployments ... mirror all Lenny-published
// images into a private registry, set platform.registry.url to that
// registry ... The chart's ImageResolver shared package
// (pkg/common/registry/resolver.go) composes every image reference
// from platform.registry.*, ensuring the gateway, lenny-ops,
// controllers, lenny-backup, and the warm-pool controller all honor
// the same registry configuration.")
// diagnosis: a failure here means a disconnected install with
// platform.registry.url pointed at a mirror either (a) does not
// compose every named component's image reference from that mirror —
// ImageResolver or the chart's lenny.componentImage helper regressed
// for one of the five components — or (b) a pod carrying the
// mirror-composed reference cannot actually be scheduled and started
// from content already present under that exact name, meaning the
// resolved reference does not match what a real mirror-population step
// would have produced, and a genuinely disconnected install would fail
// closed with ImagePullBackOff for that component.
func TestAirgapRegistryMirrorInstall(t *testing.T) {
	c := kind.InstallLenny(t)
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}

	// --- Part 1: the chart composes every named component's image
	// reference from platform.registry.url when it is set to a mirror.
	rendered := helmTemplateAirgapMirror(t)
	for _, name := range airgapComponents {
		want := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
		if !strings.Contains(rendered, want) {
			t.Errorf("§17.8.6 violation: rendering with platform.registry.url=%s does not compose "+
				"%s's image reference as %q; ImageResolver / lenny.componentImage did not honor the "+
				"mirror override for this component.\nrendered:\n%s",
				airgapMirrorHost, name, want, rendered)
		}
	}
	t.Logf("§17.8.6: the chart composes all %d named components' image references from platform.registry.url",
		len(airgapComponents))

	// --- Part 2: a pod carrying that mirror-composed reference is
	// actually schedulable and startable with zero live registry
	// access (imagePullPolicy: Never), the way a real disconnected
	// cluster behaves once its mirror has been populated and each
	// image pulled once.
	createAirgapNamespace(t, c)
	for _, name := range airgapComponents {
		loadUnderMirrorName(t, c, name)
	}
	for _, name := range airgapComponents {
		createAirgapImageCheckPod(t, c, name)
	}
	for _, name := range airgapComponents {
		waitAirgapImageCheckPod(t, c, name)
	}
	t.Logf("§17.8.6: all %d named components' mirror-composed images start with no live registry access",
		len(airgapComponents))
}

// helmTemplateAirgapMirror runs `helm template` for the Lenny chart
// with the e2e values overlay, platform.registry.url set to
// airgapMirrorHost, and backups.onDemand.enabled=true (the backup Job
// only renders when that gate is on), showing only the templates that
// render the five §17.8.6-named components, and returns the rendered
// manifest.
func helmTemplateAirgapMirror(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	args := []string{
		"template", "lenny", root + "/charts/lenny",
		"-f", root + "/tests/testinfra/kind/e2e-values.yaml",
		"--set", "platform.registry.url=" + airgapMirrorHost,
		"--set", "backups.onDemand.enabled=true",
	}
	for _, f := range airgapTemplateFiles {
		args = append(args, "--show-only", f)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template with platform.registry.url=%s: %v\n%s", airgapMirrorHost, err, out)
	}
	return string(out)
}

// createAirgapNamespace applies the scratch namespace the image-check
// pods run in and registers its teardown.
func createAirgapNamespace(t *testing.T, c *kind.Cluster) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    lenny.dev/test: tier5-airgap-registry-mirror
`, airgapMirrorNS)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("create namespace %s: %v\n%s", airgapMirrorNS, err, out)
	}
}

// loadUnderMirrorName retags the already-built <name>:e2e image (built
// and loaded onto every node by tests/testinfra/kind/install.sh's
// BINARIES loop) under the mirror-composed reference
// airgapMirrorHost/<name>:e2e, then kind-loads it onto every cluster
// node. This stands in for an operator's `crane copy`/`skopeo copy`
// mirror-population step: afterward, every node's containerd content
// store has the image available under the exact name the chart's
// mirror-configured render composes, with no dependency on any live
// registry.
func loadUnderMirrorName(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	local := name + ":e2e"
	mirrored := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
	if out, err := exec.Command("docker", "tag", local, mirrored).CombinedOutput(); err != nil {
		t.Fatalf("docker tag %s %s: %v\n%s", local, mirrored, err, out)
	}
	if out, err := exec.Command(
		"kind", "load", "docker-image", "--name", c.Name, mirrored,
	).CombinedOutput(); err != nil {
		t.Fatalf("kind load docker-image %s: %v\n%s", mirrored, err, out)
	}
}

// airgapCheckPodName derives a stable, short pod name from a component
// short name (dropping the common "lenny-" prefix so the generated
// names stay under Kubernetes' 63-character label limit).
func airgapCheckPodName(name string) string {
	return "t5-airgap-" + strings.TrimPrefix(name, "lenny-")
}

// createAirgapImageCheckPod schedules a throwaway pod in airgapMirrorNS
// running the mirror-composed reference for name with imagePullPolicy:
// Never, and registers its teardown. The container is not expected to
// reach a healthy running state (none of these binaries are given
// their real configuration); the assertion this feeds is that the
// kubelet can find and start the image at all, not that the component
// becomes healthy.
func createAirgapImageCheckPod(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	pod := airgapCheckPodName(name)
	mirrored := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: tier5-airgap-registry-mirror
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: check
      image: %s
      imagePullPolicy: Never
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        capabilities:
          drop: ["ALL"]
`, pod, airgapMirrorNS, mirrored)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("schedule airgap image-check pod for %s (%s): %v\n%s", name, mirrored, err, out)
	}
}

// waitAirgapImageCheckPod polls the image-check pod for name until its
// container leaves the Waiting state (Running or Terminated, either of
// which proves the kubelet found and started the image) or the
// deadline expires. A Waiting reason of ImagePullBackOff, ErrImagePull,
// or ErrImageNeverPull fails the test immediately: it means the
// mirror-composed reference is not resolvable from local content, so a
// genuinely disconnected install pointed at this mirror would fail
// closed for this component.
func waitAirgapImageCheckPod(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	pod := airgapCheckPodName(name)
	mirrored := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
	deadline := time.Now().Add(90 * time.Second)
	for {
		waitReason, _ := c.KubectlOut(t, "-n", airgapMirrorNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.waiting.reason}")
		termReason, _ := c.KubectlOut(t, "-n", airgapMirrorNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.terminated.reason}")
		runStartedAt, _ := c.KubectlOut(t, "-n", airgapMirrorNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.running.startedAt}")
		wr := strings.TrimSpace(waitReason)
		switch {
		case wr == "ImagePullBackOff" || wr == "ErrImagePull" || wr == "ErrImageNeverPull":
			t.Fatalf("§17.8.6: the mirror-composed image %s is not loadable on the cluster — the "+
				"kubelet reports the container Waiting with %q; a disconnected install pointed at "+
				"this mirror would fail closed for %s", mirrored, wr, name)
		case strings.TrimSpace(termReason) != "" || strings.TrimSpace(runStartedAt) != "":
			return
		}
		if time.Now().After(deadline) {
			desc, _ := c.KubectlOut(t, "-n", airgapMirrorNS, "describe", "pod", pod)
			t.Fatalf("§17.8.6: the %s image-check pod's container did not leave the Waiting state "+
				"within 90s (last waiting reason %q); cannot confirm the mirror-composed image is "+
				"loadable\n--- describe ---\n%s", name, wr, desc)
		}
		time.Sleep(3 * time.Second)
	}
}
