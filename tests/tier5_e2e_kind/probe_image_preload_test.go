// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Bootstrap checks for the container images the tier-5 probe fixtures
// require in the Kind node content store.
//
// The e2e Kind cluster runs without a reachable registry: every workload
// image is side-loaded onto the nodes by tests/testinfra/kind/install.sh
// and referenced with a pull policy that never contacts a registry
// (§17.8.6 air-gapped installs are the production analogue). The shared
// bootstrap probe fixture in gateway_probe_test.go follows the same rule
// — it pins its pod to one node with imagePullPolicy: Never — so a probe
// image the install step does not preload makes the fixture's pod fail
// with ErrImageNeverPull. Because the fixture is shared, that failure
// surfaces in every suite that uses it (audit, diagnostics, eager-claim,
// and others) rather than in the fixture itself, which makes the cause
// hard to read from any single failure.
//
// These checks state the requirement directly: the install script's
// external-image preload list covers every image the probe fixtures use,
// and the probe node actually carries them. They fail with a message
// naming the install step, so a broken bootstrap is diagnosed once here
// instead of once per unrelated suite.

package tier5_e2e_kind_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// t5NeverPullFixtureImages are the prebuilt images the tier-5 probe
// fixtures run with imagePullPolicy: Never. install.sh does not build
// them, so it must pull and side-load each one for the fixture pods to
// start on the registry-less e2e cluster.
var t5NeverPullFixtureImages = []string{t5ProbeImage}

// TestKindInstallPreloadsProbeFixtureImages asserts that the Kind
// install script side-loads every image the tier-5 probe fixtures run
// under a Never pull policy. An image missing from the preload list
// resolves only by accident, when some earlier manual `kind load` or an
// unrelated test happened to deposit it on the node, and disappears on
// the next cluster rebuild.
//
// This check reads the install script rather than the live cluster, so
// it holds even on a host where the image is currently present.
//
// spec: §17.8.6 (image registry and air-gap)
//
// diagnosis: A failure means tests/testinfra/kind/install.sh does not
// pull and `kind load` the named image. Every tier-5 suite that starts a
// bootstrap probe pod will fail with ErrImageNeverPull on a freshly
// built cluster. Add the image to the EXTERNAL_IMAGES list in install.sh.
func TestKindInstallPreloadsProbeFixtureImages(t *testing.T) {
	script := filepath.Join(repoRoot(t), "tests", "testinfra", "kind", "install.sh")
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	preloaded := parseExternalImages(string(body))
	if len(preloaded) == 0 {
		t.Fatalf("no EXTERNAL_IMAGES entries found in %s; the preload list moved or was renamed", script)
	}
	for _, img := range t5NeverPullFixtureImages {
		if !slices.Contains(preloaded, img) {
			t.Errorf("probe fixture image %q is not in the EXTERNAL_IMAGES preload list of %s (found %v); "+
				"a probe pod running with imagePullPolicy: Never cannot start on a freshly built cluster",
				img, script, preloaded)
		}
	}
}

// TestProbeFixtureImagesPresentOnProbeNode asserts that the node the
// probe pods are pinned to carries every Never-pull fixture image in its
// containerd content store. It is the live counterpart to the install
// script check: the script can list an image that the load step failed
// to import, or a prune can remove one from a long-lived cluster.
//
// The gate is the cluster rather than the Helm release: the node image
// store is a property of the cluster bootstrap, so this check stays
// meaningful on a cluster whose control plane is unhealthy, which is
// exactly when an operator needs to tell a broken bootstrap apart from a
// broken install.
//
// spec: §17.8.6 (image registry and air-gap)
//
// diagnosis: A failure means the probe node's containerd store is
// missing an image the shared bootstrap probe fixture needs. Every
// tier-5 suite that calls the fixture will fail with ErrImageNeverPull
// until tests/testinfra/kind/install.sh is re-run.
func TestProbeFixtureImagesPresentOnProbeNode(t *testing.T) {
	kind.SkipUnlessAvailable(t)
	c := kind.EnsureCluster(t)
	for _, img := range t5NeverPullFixtureImages {
		if err := t5NodeHasImage(c, t5ProbeNode, img); err != nil {
			t.Errorf("probe node %s cannot serve fixture image %q: %v; "+
				"re-run tests/testinfra/kind/install.sh to side-load it",
				t5ProbeNode, img, err)
		}
	}
}

// t5NodeHasImage reports whether node's containerd content store holds
// image, returning a descriptive error when it does not or when the
// store cannot be read. The reference the kubelet resolves is the
// registry-qualified form (docker.io/<repo>:<tag>) for a bare Docker Hub
// name, so the match is a suffix check against the listed references.
//
// It first confirms node belongs to c. The probe node name is a
// constant, so a cluster brought up under a different name
// (LENNY_KIND_CLUSTER) would otherwise be reported as missing the image
// when the node itself is what is absent.
func t5NodeHasImage(c *kind.Cluster, node, image string) error {
	nodesOut, err := exec.Command("kind", "get", "nodes", "--name", c.Name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot list the nodes of cluster %s: %w\n%s", c.Name, err, nodesOut)
	}
	if !slices.Contains(strings.Fields(string(nodesOut)), node) {
		return fmt.Errorf("node %s is not part of cluster %s (nodes: %s)",
			node, c.Name, strings.Join(strings.Fields(string(nodesOut)), " "))
	}
	out, err := exec.Command("docker", "exec", node,
		"ctr", "-n", "k8s.io", "images", "ls", "-q").CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot list the containerd images on %s: %w\n%s", node, err, out)
	}
	for _, ref := range strings.Fields(string(out)) {
		if ref == image || strings.HasSuffix(ref, "/"+image) {
			return nil
		}
	}
	return fmt.Errorf("image %s is not present in the containerd content store", image)
}

// parseExternalImages extracts the quoted entries of the
// EXTERNAL_IMAGES array literal in the install script body. It returns
// nil when the array is absent.
func parseExternalImages(body string) []string {
	const marker = "EXTERNAL_IMAGES=("
	start := strings.Index(body, marker)
	if start < 0 {
		return nil
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, ")")
	if end < 0 {
		return nil
	}
	var images []string
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		images = append(images, strings.Trim(line, `"'`))
	}
	return images
}
