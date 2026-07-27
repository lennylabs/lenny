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
	"os"
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
		case isImagePullFailure(wr):
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

// --------------------------------------------------------------------
// The live air-gapped install.
//
// The test above proves two things about a mirror-pointed deployment:
// the chart composes every §17.8.6-named component's image reference
// from platform.registry.url, and the kubelet can start each of those
// images with no registry access. What it does not do is install the
// chart. Its assertions run against `helm template` output and against
// throwaway pods that carry the image and nothing else. A component
// whose real Deployment could not start from the mirrored image, or
// could not become Ready once started, would still pass.
//
// TestAirgapMirrorInstallReachesReady closes that: it installs the
// chart onto a disconnected cluster with platform.registry.url pointed
// at a mirror and preflight validation skipped, and requires every
// §17.8.6-named component to run from the mirror and reach Ready.
//
// The install runs on a cluster of its own, created and deleted by the
// test. The tier-5 suite's cluster carries a standing "lenny" release
// whose cluster-scoped objects have fixed names, so a second release
// cannot be installed beside it, and upgrading the standing release
// would restart the control plane every other tier-5 test depends on.
// --------------------------------------------------------------------

// airgapInstallCluster is the disposable Kind cluster the live install
// runs on. tests/testinfra/kind/cluster-airgap.yaml configures it.
const airgapInstallCluster = "lenny-airgap-e2e"

// airgapInstallNS is the release namespace the chart installs into. It
// is the chart's default and the namespace the Postgres fixture's
// manifests pin.
const airgapInstallNS = "lenny-system"

// airgapInstallImages are the Lenny images a mirror-pointed install of
// the air-gap profile resolves: the five components §17.8.6 names, plus
// the Token Service (a control-plane Deployment the profile leaves on)
// and the schema-migration Job (the pre-install hook that prepares the
// Postgres schema the gateway verifies at startup). Every one is
// mirror-populated before the install, because with imagePullPolicy:
// Never a missing one fails the install closed.
var airgapInstallImages = append(
	append([]string{}, airgapComponents...),
	"lenny-token-service",
	"lenny-migrate",
)

// airgapInstallDeployments are the Deployments the install must bring
// to Available. Four of the five §17.8.6-named components are
// Deployments; lenny-backup is a Job and is checked separately.
var airgapInstallDeployments = []string{
	"lenny-gateway",
	"lenny-ops",
	"lenny-controller",
	"lenny-pool-scaling-controller",
}

// airgapPostgresFixtureObjects are the objects the air-gap install
// takes from the tier-5 datastore fixture
// (tests/testinfra/k8s/datastores.yaml): the Postgres Deployment with
// its claim and Service, and the two NetworkPolicies that admit
// control-plane traffic to it under the chart's namespace-wide
// default-deny. Naming them explicitly keeps this test on the same
// Postgres fixture the rest of the suite runs against, and fails loudly
// if the fixture is renamed. The Deployment and the Service share the
// name lenny-postgres, so each entry names a kind as well.
var airgapPostgresFixtureObjects = []struct{ kind, name string }{
	{"PersistentVolumeClaim", "lenny-postgres-data"},
	{"Deployment", "lenny-postgres"},
	{"Service", "lenny-postgres"},
	{"NetworkPolicy", "allow-e2e-datastores-ingress"},
	{"NetworkPolicy", "allow-egress-to-e2e-datastores"},
}

// spec: 17.8.6 ("Air-gapped deployments ... mirror all Lenny-published
// images into a private registry, set platform.registry.url to that
// registry, and rely on --skip-preflight for environments where the
// preflight Job cannot reach the mirrored registry before it is
// populated ... The chart's ImageResolver shared package
// (pkg/common/registry/resolver.go) composes every image reference from
// platform.registry.*, ensuring the gateway, lenny-ops, controllers,
// lenny-backup, and the warm-pool controller all honor the same
// registry configuration."), 17.6 ("Deployers can disable preflight
// validation by setting preflight.enabled: false in Helm values. This
// is intended for air-gapped or constrained environments where the Job
// cannot reach all backends at install time.").
// diagnosis: a failure here means an air-gapped install does not work
// end to end. Either the install itself fails against a mirror with
// preflight skipped, so an operator following the air-gap procedure
// cannot deploy at all; or one of the §17.8.6-named components runs an
// image that is not composed from platform.registry.url, so a
// disconnected cluster would have to reach outside the mirror to run
// it; or a component starts from the mirrored image but never becomes
// Ready, which the sibling image-check assertions in this file cannot
// see because they run a pod that carries the image without the
// chart's configuration.
func TestAirgapMirrorInstallReachesReady(t *testing.T) {
	kind.PrerequisitesAvailable(t)
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}
	root := repoRoot(t)
	for _, name := range airgapInstallImages {
		requireLocalImage(t, name+":e2e")
	}
	fixture, fixtureImage := airgapPostgresFixture(t, root)
	requireLocalImage(t, fixtureImage)

	c := kind.CreateDisposableCluster(t, airgapInstallCluster,
		root+"/tests/testinfra/kind/cluster-airgap.yaml")

	// --- Populate the mirror. Every image the install resolves is
	// retagged under the mirror host and loaded onto the node, which is
	// the state a real air-gapped cluster is in after the operator has
	// copied the Lenny images into the private registry and each node
	// has pulled them once. Nothing else is reachable: the profile pins
	// imagePullPolicy: Never on every component.
	for _, name := range airgapInstallImages {
		loadUnderMirrorName(t, c, name)
	}
	c.LoadImage(t, fixtureImage)

	// --- Stand up the Postgres the §4.6.2 PoolScalingController reads
	// its pool definitions from and the gateway persists to. It is a
	// test fixture rather than part of the install; an operator's
	// air-gapped deployment points the same values at their own
	// database.
	airgapCreateNamespace(t, c, airgapInstallNS)
	if out, err := c.ApplyStdin(t, fixture); err != nil {
		t.Fatalf("apply the Postgres fixture into %s: %v\n%s", airgapInstallNS, err, out)
	}
	if out, err := c.KubectlOut(t, "-n", airgapInstallNS, "wait", "--for=condition=Available",
		"deploy/lenny-postgres", "--timeout=240s"); err != nil {
		t.Fatalf("the Postgres fixture did not become Available: %v\n%s", err, out)
	}

	// --- Install the chart the way an operator installs it in an
	// air-gapped environment: images composed from the mirror,
	// preflight validation skipped.
	notes := airgapHelmInstall(t, c, root)

	// --- Part 1: the install ran with preflight validation skipped and
	// says so. §17.8.6 names the skip as what an air-gapped deployment
	// relies on, and §17.6 requires the warning.
	if !strings.Contains(notes, skipPreflightWarning) {
		t.Errorf("§17.6 violation: the air-gapped install output does not carry the required warning "+
			"%q, so an operator gets no signal that infrastructure validation was skipped.\n%s",
			skipPreflightWarning, notes)
	}
	if out, err := c.KubectlOut(t, "-n", airgapInstallNS, "get", "job", preflightJobName,
		"--ignore-not-found", "-o", "name"); err == nil && strings.TrimSpace(out) != "" {
		t.Errorf("§17.6 violation: the install created the %s Job even though preflight is disabled; "+
			"a disconnected install would have to pull that image from a mirror that is not yet "+
			"populated before the install could proceed", preflightJobName)
	}

	// --- Part 2: every §17.8.6-named component that is a Deployment
	// reaches Ready. This is what the image-check pods cannot show: the
	// component starts from the mirrored image with the chart's own
	// configuration and passes its readiness probe.
	for _, name := range airgapInstallDeployments {
		if out, err := c.KubectlOut(t, "-n", airgapInstallNS, "wait", "--for=condition=Available",
			"deploy/"+name, "--timeout=300s"); err != nil {
			t.Fatalf("§17.8.6: %s did not become Available on the air-gapped install: %v\n%s\n%s",
				name, err, out, airgapInstallDiagnostics(t, c))
		}
	}

	// --- Part 3: lenny-backup, the one named component the chart
	// deploys as a Job, runs its binary from the mirrored image. The
	// Job is not expected to complete: this fixture deploys no artifact
	// store for it to write an archive to. What the install must show
	// is that its container starts rather than failing closed on the
	// image.
	airgapRequireBackupStarts(t, c)

	// --- Part 4: every Lenny image running on the cluster is composed
	// from platform.registry.url. A component that slipped a
	// chart-default ghcr.io reference through would have to be pulled
	// from outside the mirror, which a disconnected cluster cannot do.
	running := airgapRunningImages(t, c)
	for _, ref := range running {
		if !isLennyImage(ref) {
			continue
		}
		if !strings.HasPrefix(ref, airgapMirrorHost+"/") {
			t.Errorf("§17.8.6 violation: the air-gapped install runs the Lenny image %q, which is not "+
				"composed from platform.registry.url=%s; a disconnected cluster could not pull it.",
				ref, airgapMirrorHost)
		}
	}
	for _, name := range airgapComponents {
		want := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
		if !containsRef(running, want) {
			t.Errorf("§17.8.6 violation: no pod on the air-gapped install runs %s from the mirror as "+
				"%q; the install does not honor the single registry configuration for this "+
				"component.\nrunning images: %v", name, want, running)
		}
	}
	t.Logf("§17.8.6: an install with platform.registry.url=%s and preflight skipped runs all %d named "+
		"components from the mirror", airgapMirrorHost, len(airgapComponents))
}

// airgapHelmInstall installs the chart onto the disposable cluster with
// the air-gap values profile and the mirror override, and returns
// everything helm printed (including the install NOTES).
//
// The install does not use --wait. The rendered on-demand lenny-backup
// Job never completes on this fixture (see the profile's comment), and
// --wait blocks on a Job's completion; the Deployments are waited for
// individually instead. --server-side=false matches
// tests/testinfra/kind/install.sh: helm 4's server-side apply rejects
// the chart's named http and metrics ports sharing one containerPort.
func airgapHelmInstall(t *testing.T, c *kind.Cluster, root string) string {
	t.Helper()
	out, err := exec.Command(
		"helm", "install", "lenny", root+"/charts/lenny",
		"--kubeconfig", c.KubeconfigPath,
		"--namespace", airgapInstallNS,
		"--create-namespace",
		"-f", root+"/tests/testinfra/kind/airgap-values.yaml",
		"--set", "platform.registry.url="+airgapMirrorHost,
		"--server-side=false",
		"--timeout", "420s",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("§17.8.6: installing the chart with platform.registry.url=%s and preflight disabled "+
			"failed, so the documented air-gapped install procedure does not work: %v\n%s\n%s",
			airgapMirrorHost, err, out, airgapInstallDiagnostics(t, c))
	}
	return string(out)
}

// airgapPostgresFixture returns the objects listed in
// airgapPostgresFixtureObjects, as one multi-document manifest,
// together with the Postgres image the fixture runs. Reading them out
// of the tier-5 datastore fixture keeps one definition of the e2e
// Postgres rather than a second copy that drifts from it.
func airgapPostgresFixture(t *testing.T, root string) (manifest, image string) {
	t.Helper()
	path := root + "/tests/testinfra/k8s/datastores.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the datastore fixture %s: %v", path, err)
	}
	var docs []string
	var deployment string
	for _, want := range airgapPostgresFixtureObjects {
		doc, ok := findRenderedResource(string(raw), want.kind, want.name)
		if !ok {
			t.Fatalf("the datastore fixture %s no longer declares the %s %q; the air-gapped install "+
				"test takes its Postgres from that fixture and cannot proceed without it",
				path, want.kind, want.name)
		}
		if want.kind == "Deployment" {
			deployment = doc
		}
		docs = append(docs, doc)
	}
	for _, line := range strings.Split(deployment, "\n") {
		field := strings.TrimSpace(line)
		if strings.HasPrefix(field, "image:") {
			image = strings.Trim(strings.TrimSpace(strings.TrimPrefix(field, "image:")), `"'`)
			break
		}
	}
	if image == "" {
		t.Fatalf("no container image found in the lenny-postgres Deployment in %s", path)
	}
	return strings.Join(docs, "\n---\n"), image
}

// requireLocalImage skips the calling test when an image the air-gapped
// install must mirror is not in the host's local image store. The
// images are built by tests/testinfra/kind/install.sh, so their absence
// means the harness has not been prepared on this host rather than that
// the platform is broken.
func requireLocalImage(t *testing.T, ref string) {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", ref).Run(); err != nil {
		t.Skipf("image %s is not built on this host; run tests/testinfra/kind/install.sh, which builds "+
			"every platform image the air-gapped install mirrors", ref)
	}
}

// airgapCreateNamespace creates the release namespace before the
// fixture manifests, which pin it, are applied.
func airgapCreateNamespace(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, name)
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("create namespace %s: %v\n%s", name, err, out)
	}
}

// airgapRequireBackupStarts waits for the on-demand lenny-backup Job's
// pod to leave the Waiting state, and fails when the kubelet reports an
// image failure instead. A Waiting reason of ImagePullBackOff,
// ErrImagePull, or ErrImageNeverPull means the Job's mirror-composed
// image is not resolvable on a disconnected cluster, so an air-gapped
// install would carry a backup workload that can never run.
func airgapRequireBackupStarts(t *testing.T, c *kind.Cluster) {
	t.Helper()
	const selector = "lenny.dev/component=backup"
	deadline := time.Now().Add(180 * time.Second)
	for {
		waiting, _ := c.KubectlOut(t, "-n", airgapInstallNS, "get", "pods", "-l", selector,
			"-o", "jsonpath={.items[*].status.containerStatuses[*].state.waiting.reason}")
		started, _ := c.KubectlOut(t, "-n", airgapInstallNS, "get", "pods", "-l", selector,
			"-o", "jsonpath={.items[*].status.containerStatuses[*].state.running.startedAt}"+
				"{.items[*].status.containerStatuses[*].state.terminated.startedAt}")
		for _, reason := range strings.Fields(waiting) {
			if isImagePullFailure(reason) {
				t.Fatalf("§17.8.6: the lenny-backup Job's mirror-composed image is not runnable on the "+
					"air-gapped install: the kubelet reports the container Waiting with %q, so the "+
					"backup workload could never run on a disconnected cluster\n%s",
					reason, airgapInstallDiagnostics(t, c))
			}
		}
		if strings.TrimSpace(started) != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("§17.8.6: the lenny-backup Job's container did not start within 180s, so the "+
				"install cannot show that lenny-backup runs from the mirror\n%s",
				airgapInstallDiagnostics(t, c))
		}
		time.Sleep(3 * time.Second)
	}
}

// airgapRunningImages returns every container image across the pods in
// the release namespace, deduplicated.
func airgapRunningImages(t *testing.T, c *kind.Cluster) []string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", airgapInstallNS, "get", "pods",
		"-o", "jsonpath={range .items[*]}{range .spec.containers[*]}{.image}{\"\\n\"}{end}{end}")
	if err != nil {
		t.Fatalf("list the images running in %s: %v\n%s", airgapInstallNS, err, out)
	}
	seen := map[string]bool{}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		ref := strings.TrimSpace(line)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// isLennyImage reports whether a container image reference names a
// Lenny component, by the "lenny-" prefix on its final repository
// segment. That is the naming every chart template uses for the
// platform's own images, and no third-party image in this install
// matches it.
func isLennyImage(ref string) bool {
	repo := ref
	if i := strings.LastIndex(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return strings.HasPrefix(repo, "lenny-")
}

// isImagePullFailure reports whether a container's Waiting reason means
// the kubelet could not obtain the image. On a cluster with no registry
// access, every one of these reasons means the image is absent from the
// node's local content store under the exact name the pod asks for.
func isImagePullFailure(reason string) bool {
	switch reason {
	case "ImagePullBackOff", "ErrImagePull", "ErrImageNeverPull":
		return true
	}
	return false
}

// containsRef reports whether refs contains want.
func containsRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

// airgapInstallDiagnostics renders the pod inventory of the release
// namespace for a failure message, so a failing run names which
// component did not come up and why.
func airgapInstallDiagnostics(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	pods, _ := c.KubectlOut(t, "-n", airgapInstallNS, "get", "pods", "-o", "wide")
	events, _ := c.KubectlOut(t, "-n", airgapInstallNS, "get", "events",
		"--sort-by=.lastTimestamp", "--field-selector=type=Warning")
	return "--- pods ---\n" + pods + "\n--- warning events ---\n" + events
}
