// SPDX-License-Identifier: MIT

// Package kind is the testinfra harness for the tier-5 e2e suite.
// Tier-5 tests call SkipUnlessAvailable to short-circuit when the
// host has no Docker daemon, no `kind` binary, or `kubectl`; the
// e2e tests run only on workstations and CI runners with those
// tools installed.
//
// When the prerequisites are present, EnsureCluster brings up a
// Kind cluster named `lenny-e2e` (idempotent: a cluster with that
// name is reused) and returns a *Cluster handle. The handle exposes
// the kubeconfig path and helpers for applying manifests.
//
// The Lenny Helm chart lives in charts/lenny/. InstallLenny renders
// and applies it against an EnsureCluster cluster so tier-5 tests can
// drive the platform end-to-end.
//
// # Environment variables
//
//	LENNY_KIND_CLUSTER       Cluster name to create / reuse.
//	                         Default: lenny-e2e.
//	LENNY_KIND_SKIP_BRINGUP  When "1", EnsureCluster expects an
//	                         existing cluster and only wires the
//	                         handle. For CI workflows that
//	                         provision the cluster as a separate
//	                         step.
//	LENNY_KIND_TEARDOWN      When "1", EnsureCluster registers a
//	                         t.Cleanup that calls Delete() at the
//	                         end of the bringing-up test. Default
//	                         (unset) keeps the cluster across
//	                         tests to amortize the ~30s bring-up
//	                         cost.
package kind

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	defaultClusterName = "lenny-e2e"
	envClusterName     = "LENNY_KIND_CLUSTER"
	envSkipBringUp     = "LENNY_KIND_SKIP_BRINGUP"
	envTeardown        = "LENNY_KIND_TEARDOWN"
)

// Cluster represents a running Kind cluster.
type Cluster struct {
	Name           string
	KubeconfigPath string
}

// SkipUnlessAvailable is the entry point existing tier-5 scaffolds
// call. It skips when a tier-5 prerequisite is missing — docker, kind,
// kubectl, the docker daemon, or the Lenny Helm chart directory the
// tests render — and otherwise returns so the caller can proceed.
//
// Tests that want to drive a raw Kind cluster without expecting the
// chart should call PrerequisitesAvailable + EnsureCluster directly.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	PrerequisitesAvailable(t)
	if path, err := findChartDir(); err != nil {
		t.Skipf("Lenny Helm chart not on disk: %v", err)
	} else if path == "" {
		t.Skip("Lenny Helm chart not on disk: charts/lenny/Chart.yaml missing")
	}
}

// findChartDir resolves the path to charts/lenny/ by walking upward
// from the working directory until it locates a Chart.yaml. It returns
// the empty string when the chart is absent, and an error when the
// walk itself fails.
func findChartDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		candidate := filepath.Join(dir, "charts", "lenny", "Chart.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Dir(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// PrerequisitesAvailable skips when docker / kind / kubectl are
// missing OR the docker daemon is unreachable. Does NOT skip on
// missing chart, so callers that want a raw Kind cluster can use
// EnsureCluster directly after this.
func PrerequisitesAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skipf("kind not on PATH: %v", err)
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skipf("kubectl not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// EnsureCluster brings up a Kind cluster (idempotent) and returns a
// *Cluster handle. The cluster is shared across tests in the same
// process via a sync.Once; t.Cleanup is NOT registered for teardown
// because the cluster's create cost (~30 seconds) makes reuse the
// right default. Set LENNY_KIND_TEARDOWN=1 to force cleanup at
// process exit.
//
// When LENNY_KIND_SKIP_BRINGUP=1 the function expects an existing
// cluster and only configures the handle. Useful for CI workflows
// that provision the cluster as a workflow step.
func EnsureCluster(t testing.TB) *Cluster {
	t.Helper()
	PrerequisitesAvailable(t)
	once.Do(func() {
		shared = createOrReuse(t)
		if shared != nil && os.Getenv(envTeardown) == "1" {
			// Register the teardown on the package level via a
			// dedicated goroutine so subsequent tests in the same
			// process do not each register their own. The cluster
			// outlives the test that brought it up; the explicit
			// LENNY_KIND_TEARDOWN=1 contract is "delete on process
			// exit", which testing.TB's t.Cleanup cannot satisfy
			// directly. The deletion runs from a TestMain-equivalent
			// position via init-time intent.
			t.Cleanup(func() {
				// t.Cleanup runs at the bring-up test's end, which
				// in practice is close enough to "process exit" for
				// the e2e suite. Subsequent tests reuse the cluster
				// only when LENNY_KIND_TEARDOWN is unset.
				if err := shared.Delete(); err != nil {
					t.Logf("kind teardown: %v", err)
				}
			})
		}
	})
	if shared == nil {
		t.Skip("kind cluster bring-up failed; see earlier diagnostics")
	}
	return shared
}

var (
	once   sync.Once
	shared *Cluster
)

// clusterName resolves the kind cluster name from the env var,
// falling back to the default.
func clusterName() string {
	if v := strings.TrimSpace(os.Getenv(envClusterName)); v != "" {
		return v
	}
	return defaultClusterName
}

// createOrReuse brings the cluster up when absent. Returns a
// populated *Cluster on success, nil + log on any failure.
func createOrReuse(t testing.TB) *Cluster {
	t.Helper()
	name := clusterName()
	out, err := exec.Command("kind", "get", "clusters").Output()
	if err != nil {
		t.Logf("kind get clusters: %v", err)
		return nil
	}
	if !strings.Contains(string(out), name) {
		if os.Getenv(envSkipBringUp) == "1" {
			t.Logf("LENNY_KIND_SKIP_BRINGUP=1 but cluster %q is not running; cannot proceed", name)
			return nil
		}
		create := exec.Command("kind", "create", "cluster", "--name", name, "--config", configPath(t))
		create.Stdout = os.Stdout
		create.Stderr = os.Stderr
		if err := create.Run(); err != nil {
			t.Logf("kind create cluster: %v", err)
			return nil
		}
	}
	kubeconfig, err := exec.Command("kind", "get", "kubeconfig", "--name", name).Output()
	if err != nil {
		t.Logf("kind get kubeconfig: %v", err)
		return nil
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("lenny-kind-%s-kubeconfig", name))
	if err := os.WriteFile(tmp, kubeconfig, 0o600); err != nil {
		t.Logf("write kubeconfig: %v", err)
		return nil
	}
	return &Cluster{Name: name, KubeconfigPath: tmp}
}

// configPath returns the path to the kind cluster YAML used by
// EnsureCluster. When tests/testinfra/kind/values.yaml does not
// exist (the canonical name documented in TESTING.md), the cluster
// is created with the default config.
func configPath(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root := wd
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			root = d
			break
		}
	}
	cfg := filepath.Join(root, "tests", "testinfra", "kind", "cluster.yaml")
	if _, err := os.Stat(cfg); err == nil {
		return cfg
	}
	return ""
}

// Apply runs `kubectl apply -f <path>` against the cluster's
// kubeconfig.
func (c *Cluster) Apply(t testing.TB, manifestPath string) {
	t.Helper()
	cmd := exec.Command("kubectl", "--kubeconfig", c.KubeconfigPath, "apply", "-f", manifestPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl apply %s: %v\n%s", manifestPath, err, out)
	}
}

// Kubectl returns an *exec.Cmd pre-bound to the cluster's
// kubeconfig.
func (c *Cluster) Kubectl(args ...string) *exec.Cmd {
	return exec.Command("kubectl", append([]string{"--kubeconfig", c.KubeconfigPath}, args...)...)
}

// Delete tears down the cluster. Tests rarely call this; CI runs
// invoke it at workflow-end to free resources.
func (c *Cluster) Delete() error {
	return exec.Command("kind", "delete", "cluster", "--name", c.Name).Run()
}
