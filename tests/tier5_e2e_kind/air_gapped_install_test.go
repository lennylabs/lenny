// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind coverage for the skip-preflight half of the §17.8.6
// air-gapped install path.
//
// The sibling file airgap_registry_mirror_test.go covers the other
// half: that every §17.8.6-named component composes its image
// reference from platform.registry.url and starts from mirror content
// with no live registry access. What that file leaves untested is the
// second thing §17.8.6 says an air-gapped deployment relies on — the
// install running with preflight disabled, because the pre-install
// preflight Job cannot reach the mirrored registry before it is
// populated.
//
// This file drives the operator's actual install invocation
// (`helm install ... --dry-run=client`, which renders the hook
// manifests, the main manifests, and the install NOTES exactly as a
// live install would) with platform.registry.url pointed at a mirror,
// once with preflight enabled and once with it disabled, and confirms
// the live cluster's kubelet does refuse the preflight image when the
// mirror has not been populated. A client-side dry run rather than a
// live second release: the chart's cluster-scoped objects (CRDs,
// ClusterRoles, ValidatingWebhookConfigurations) carry fixed names
// already owned by the standing "lenny" release, so a real second
// install is rejected on ownership before it renders anything, and
// installing over the standing release would destroy the cluster every
// other tier-5 test depends on.
package tier5_e2e_kind_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// airgapUnpopulatedMirrorHost stands in for the state §17.8.6 names as
// the reason the skip exists: a mirror registry that has been
// configured but not yet populated. Nothing in this suite ever loads
// an image under this host, so a pod referencing it with
// imagePullPolicy: Never is guaranteed to find no local content, which
// is what a disconnected cluster sees when it cannot reach the mirror.
const airgapUnpopulatedMirrorHost = "airgap-unpopulated.internal"

// airgapPreflightNS is the scratch namespace the unpopulated-mirror
// check pod runs in. It is separate from the sibling test's namespace
// so the two tests never contend for the same object names.
const airgapPreflightNS = "lenny-airgap-preflight-test"

// skipPreflightWarning is the warning §17.6 requires the chart to
// surface when preflight validation is disabled, verbatim.
const skipPreflightWarning = "Preflight validation skipped — infrastructure misconfigurations may cause runtime failures."

// preflightJobName is the name of the pre-install/pre-upgrade Job the
// chart renders when preflight validation is enabled, and of the
// ServiceAccount, ClusterRole, and ClusterRoleBinding that back it.
const preflightJobName = "lenny-preflight"

// crdValidateJobName is the post-upgrade CRD-validation Job. §17.4
// keeps it rendering independently of the preflight knob so a stale
// CRD is still caught on an install that skipped preflight.
const crdValidateJobName = "lenny-crd-validate"

// spec: 17.8.6 ("Air-gapped deployments ... mirror all Lenny-published
// images into a private registry, set platform.registry.url to that
// registry, and rely on --skip-preflight for environments where the
// preflight Job cannot reach the mirrored registry before it is
// populated."), 17.6 ("--skip-preflight: Deployers can disable
// preflight validation by setting preflight.enabled: false in Helm
// values. This is intended for air-gapped or constrained environments
// where the Job cannot reach all backends at install time. A warning
// is logged: \"Preflight validation skipped — infrastructure
// misconfigurations may cause runtime failures.\""), 17.4 ("This
// catch-net ensures that even if preflight was skipped
// (preflight.enabled: false) ... the failure is surfaced
// immediately").
// diagnosis: a failure here means the air-gapped install path is
// broken in one of four ways. Either the preflight knob no longer
// removes the pre-install Job, so a disconnected install stalls on a
// hook whose image cannot be pulled until the mirror it is pointed at
// is populated; or the skip is no longer surfaced to the operator with
// the warning §17.6 requires, so an install silently loses its
// infrastructure validation; or disabling preflight changed how the
// remaining components resolve their images, so a mirror-pointed
// install stops being self-contained; or the §17.4 CRD-validation
// catch-net stopped rendering on a preflight-skipped install, removing
// the only remaining stale-CRD guard from exactly the installs that
// have no preflight.
func TestAirGappedInstallSkipsPreflight(t *testing.T) {
	c := kind.InstallLenny(t)
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}

	// --- Part 1: with preflight enabled, the install carries a
	// pre-install hook Job whose own image is composed from the mirror.
	// This is the condition §17.8.6 names as the reason the skip
	// exists, and it is what makes Part 2's absence assertion a real
	// discriminator rather than a check that never had anything to
	// find.
	enabled := renderAirGappedInstall(t, true)
	job, ok := findRenderedResource(enabled, "Job", preflightJobName)
	if !ok {
		t.Fatalf("§17.6 violation: rendering the air-gapped install with preflight.enabled=true produced "+
			"no %s Job; the preflight knob no longer controls anything, so Part 2 cannot show that "+
			"disabling it removes the hook.\nrendered:\n%s", preflightJobName, enabled)
	}
	if !strings.Contains(job, "pre-install") {
		t.Errorf("§17.6: the %s Job is not annotated as a pre-install hook, so it does not run before "+
			"the install proceeds and cannot be what blocks a disconnected install.\njob:\n%s",
			preflightJobName, job)
	}
	preflightImage := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, preflightJobName)
	if !strings.Contains(job, preflightImage) {
		t.Errorf("§17.8.6: the %s Job's image is not composed from platform.registry.url (expected %q); "+
			"without that, the Job would not depend on the mirror and the §17.8.6 rationale for "+
			"--skip-preflight would not hold.\njob:\n%s", preflightJobName, preflightImage, job)
	}

	// --- Part 2: with preflight disabled — the `--skip-preflight`
	// knob §17.8.6 says air-gapped deployments rely on — the install
	// renders no preflight resource at all, so nothing has to reach the
	// mirrored registry before it is populated.
	disabled := renderAirGappedInstall(t, false)
	for _, resourceKind := range []string{"Job", "ServiceAccount", "ClusterRole", "ClusterRoleBinding"} {
		if doc, found := findRenderedResource(disabled, resourceKind, preflightJobName); found {
			t.Errorf("§17.6 violation: the air-gapped install with preflight.enabled=false still renders "+
				"the %s %s; a disconnected install would still have to pull that image from a mirror "+
				"that is not yet populated.\n%s:\n%s", preflightJobName, resourceKind, resourceKind, doc)
		}
	}

	// --- Part 3: the skip is surfaced to the operator with the
	// warning §17.6 specifies, so an install that silently lost its
	// infrastructure validation is not silent.
	if !strings.Contains(disabled, skipPreflightWarning) {
		t.Errorf("§17.6 violation: the install output for preflight.enabled=false does not carry the "+
			"required warning %q; the operator gets no signal that infrastructure validation was "+
			"skipped.\nrendered:\n%s", skipPreflightWarning, disabled)
	}

	// --- Part 4: skipping preflight changes nothing about how the
	// rest of the install resolves its images. Every §17.8.6-named
	// component still composes from the mirror, and so does every
	// other Lenny component image in the render, so the install stays
	// self-contained against the mirror alone.
	for _, name := range airgapComponents {
		want := fmt.Sprintf("%s/%s:e2e", airgapMirrorHost, name)
		if !strings.Contains(disabled, want) {
			t.Errorf("§17.8.6 violation: with preflight skipped, %s's image reference is not composed "+
				"from the mirror as %q.\nrendered:\n%s", name, want, disabled)
		}
	}
	for _, ref := range lennyImageRefs(disabled) {
		if !strings.HasPrefix(ref, airgapMirrorHost+"/") {
			t.Errorf("§17.8.6 violation: the preflight-skipped air-gapped install references the Lenny "+
				"image %q, which is not composed from platform.registry.url=%s; that image would have "+
				"to be pulled from outside the mirror, which a disconnected cluster cannot do.",
				ref, airgapMirrorHost)
		}
	}

	// --- Part 5: the §17.4 stale-CRD catch-net still renders on an
	// install that skipped preflight, which is the case it exists for.
	if _, found := findRenderedResource(disabled, "Job", crdValidateJobName); !found {
		t.Errorf("§17.4 violation: the %s Job does not render when preflight is skipped; the stale-CRD "+
			"catch-net is missing from exactly the installs that have no preflight to catch it.\n"+
			"rendered:\n%s", crdValidateJobName, disabled)
	}

	// --- Part 6: on the live cluster, confirm the condition §17.8.6
	// describes is real rather than hypothetical: a pod carrying the
	// preflight image composed from a mirror that has not been
	// populated cannot start. That is what an air-gapped install hits
	// when preflight is left enabled, and it is why the skip exists.
	createAirgapPreflightNamespace(t, c)
	requireUnpopulatedMirrorImageRefused(t, c)
}

// renderAirGappedInstall runs the operator's air-gapped install
// invocation as a client-side dry run and returns everything helm
// emits: the hook manifests, the main manifests, and the install
// NOTES. platform.registry.url points at the mirror, preflight is set
// from the argument, and the on-demand backup Job is enabled so
// lenny-backup (one of the five components §17.8.6 names) renders.
func renderAirGappedInstall(t *testing.T, preflightEnabled bool) string {
	t.Helper()
	root := repoRoot(t)
	out, err := exec.Command(
		"helm",
		"install", "lenny", root+"/charts/lenny",
		"--namespace", "lenny-system",
		"--dry-run=client",
		"-f", root+"/tests/testinfra/kind/e2e-values.yaml",
		"--set", "platform.registry.url="+airgapMirrorHost,
		"--set", fmt.Sprintf("preflight.enabled=%t", preflightEnabled),
		"--set", "backups.onDemand.enabled=true",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("helm install --dry-run with platform.registry.url=%s preflight.enabled=%t: %v\n%s",
			airgapMirrorHost, preflightEnabled, err, out)
	}
	return string(out)
}

// findRenderedResource returns the YAML document in a rendered install
// that declares the named resource of the given kind, and whether it
// was found. Documents are the `---`-separated blocks helm emits for
// both hooks and main manifests.
func findRenderedResource(rendered, kind, name string) (string, bool) {
	for _, doc := range strings.Split(rendered, "\n---\n") {
		if !strings.Contains(doc, "\nkind: "+kind+"\n") {
			continue
		}
		if strings.Contains(doc, "\n  name: "+name+"\n") {
			return doc, true
		}
	}
	return "", false
}

// lennyImageRefs returns every container image reference in a rendered
// install whose repository names a Lenny component, deduplicated. A
// reference counts as a Lenny component when its final path segment
// starts with "lenny-", which is the naming every chart template uses
// for the platform's own images and which no third-party image in the
// chart (the MinIO client Job, for example) matches.
func lennyImageRefs(rendered string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, line := range strings.Split(rendered, "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "image:") {
			continue
		}
		ref := strings.Trim(strings.TrimSpace(strings.TrimPrefix(field, "image:")), `"'`)
		if ref == "" {
			continue
		}
		repo := ref
		if i := strings.LastIndex(repo, "@"); i >= 0 {
			repo = repo[:i]
		}
		if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
			repo = repo[:i]
		}
		segment := repo
		if i := strings.LastIndex(segment, "/"); i >= 0 {
			segment = segment[i+1:]
		}
		if !strings.HasPrefix(segment, "lenny-") || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// createAirgapPreflightNamespace applies the scratch namespace the
// unpopulated-mirror check pod runs in and registers its teardown.
func createAirgapPreflightNamespace(t *testing.T, c *kind.Cluster) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    lenny.dev/test: tier5-airgap-skip-preflight
`, airgapPreflightNS)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("create namespace %s: %v\n%s", airgapPreflightNS, err, out)
	}
}

// requireUnpopulatedMirrorImageRefused schedules a pod carrying the
// preflight image composed from a mirror host nothing has populated,
// with imagePullPolicy: Never so no live registry access is possible,
// and requires the kubelet to refuse it. A cluster that started the
// container instead would mean the check pod found content under a
// name no mirror-population step ever produced, which would make the
// sibling air-gap test's start assertions meaningless as well.
func requireUnpopulatedMirrorImageRefused(t *testing.T, c *kind.Cluster) {
	t.Helper()
	const pod = "t5-airgap-unpopulated-preflight"
	image := fmt.Sprintf("%s/%s:e2e", airgapUnpopulatedMirrorHost, preflightJobName)
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: tier5-airgap-skip-preflight
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
`, pod, airgapPreflightNS, image)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("schedule unpopulated-mirror check pod (%s): %v\n%s", image, err, out)
	}

	deadline := time.Now().Add(90 * time.Second)
	for {
		waitReason, _ := c.KubectlOut(t, "-n", airgapPreflightNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.waiting.reason}")
		runStartedAt, _ := c.KubectlOut(t, "-n", airgapPreflightNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.running.startedAt}")
		termReason, _ := c.KubectlOut(t, "-n", airgapPreflightNS, "get", "pod", pod,
			"-o", "jsonpath={.status.containerStatuses[0].state.terminated.reason}")
		wr := strings.TrimSpace(waitReason)
		switch {
		case wr == "ImagePullBackOff" || wr == "ErrImagePull" || wr == "ErrImageNeverPull":
			t.Logf("§17.8.6: the preflight image %s is refused (%s) while the mirror is unpopulated, "+
				"which is the condition --skip-preflight exists for", image, wr)
			return
		case strings.TrimSpace(runStartedAt) != "" || strings.TrimSpace(termReason) != "":
			t.Fatalf("§17.8.6: the preflight image %s started even though no mirror-population step ever "+
				"produced it; an unpopulated mirror is not actually a blocking condition on this "+
				"cluster, so the air-gap start assertions elsewhere in this suite prove nothing", image)
		}
		if time.Now().After(deadline) {
			desc, _ := c.KubectlOut(t, "-n", airgapPreflightNS, "describe", "pod", pod)
			t.Fatalf("§17.8.6: the unpopulated-mirror check pod reached no terminal image state within "+
				"90s (last waiting reason %q); cannot confirm an unpopulated mirror blocks the "+
				"preflight image\n--- describe ---\n%s", wr, desc)
		}
		time.Sleep(3 * time.Second)
	}
}
