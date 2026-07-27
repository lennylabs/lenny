// SPDX-License-Identifier: MIT

package kind

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// CreateDisposableCluster brings up a Kind cluster that belongs to the
// calling test alone and deletes it when the test ends.
//
// It is the counterpart to EnsureCluster, which reuses one standing
// cluster across the whole tier-5 suite. A test needs its own cluster
// when it installs the Lenny chart itself: the chart's cluster-scoped
// objects carry fixed names, so a second Helm release cannot be
// installed beside the standing one, and installing over the standing
// release would take the cluster every other tier-5 test depends on.
//
// name must differ from the suite cluster's name (see clusterName). A
// leftover cluster under the same name, from an interrupted run, is
// deleted before the new one is created, so a re-run starts clean
// rather than reusing an unknown state. configPath is the absolute path
// to a kind cluster configuration file.
//
// The returned handle carries a kubeconfig written under the test's own
// temporary directory, which the testing package removes with the rest
// of the test's scratch state.
func CreateDisposableCluster(t testing.TB, name, configPath string) *Cluster {
	t.Helper()
	PrerequisitesAvailable(t)
	if name == clusterName() {
		t.Fatalf("disposable cluster name %q collides with the standing tier-5 cluster; "+
			"a disposable cluster is deleted at test end and must not name the shared one", name)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("kind cluster config %s: %v", configPath, err)
	}

	// Delete first so an interrupted earlier run cannot hand this one a
	// half-provisioned cluster that then fails for an unrelated reason.
	deleteCluster(name)
	t.Cleanup(func() { deleteCluster(name) })

	create := exec.Command("kind", "create", "cluster", "--name", name, "--config", configPath)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		t.Fatalf("kind create cluster %s (--config %s): %v", name, configPath, err)
	}

	kubeconfig, err := exec.Command("kind", "get", "kubeconfig", "--name", name).Output()
	if err != nil {
		t.Fatalf("kind get kubeconfig --name %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("%s-kubeconfig", name))
	if err := os.WriteFile(path, kubeconfig, 0o600); err != nil {
		t.Fatalf("write kubeconfig %s: %v", path, err)
	}
	return &Cluster{Name: name, KubeconfigPath: path}
}

// deleteCluster removes the named Kind cluster, ignoring the "no such
// cluster" case. Deletion is best-effort on the cleanup path: a failure
// there leaves a cluster behind but must not mask the test's verdict.
func deleteCluster(name string) {
	_ = exec.Command("kind", "delete", "cluster", "--name", name).Run()
}

// LoadImage copies a locally built image into the cluster's nodes with
// `kind load docker-image`, so a pod can run it with
// imagePullPolicy: Never and no registry access.
func (c *Cluster) LoadImage(t testing.TB, image string) {
	t.Helper()
	out, err := exec.Command("kind", "load", "docker-image", "--name", c.Name, image).CombinedOutput()
	if err != nil {
		t.Fatalf("kind load docker-image %s into %s: %v\n%s", image, c.Name, err, out)
	}
}
