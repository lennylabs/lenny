// SPDX-License-Identifier: MIT

// Package envtest wraps sigs.k8s.io/controller-runtime/pkg/envtest so
// tier-2 controller tests can drive a real kube-apiserver and etcd
// without provisioning a full cluster. Start boots the API server,
// installs the lenny.dev CRDs from charts/lenny/crds, and returns a
// *rest.Config the caller hands to client-go.
//
// The kube-apiserver and etcd binaries are located in this order: the
// KUBEBUILDER_ASSETS environment variable when set (authoritative),
// otherwise the setup-envtest cache. When neither resolves, Start and
// SkipUnlessAvailable skip the test so the suite runs cleanly on hosts
// without the binaries. Install them with scripts/setup-dev.sh or
// directly with `setup-envtest use 1.31.0`.
//
// Usage:
//
//	env := envtest.Start(t)
//	cl, _ := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
//	// ... reconcile ...
package envtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
	crenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// k8sVersion is the envtest Kubernetes version. It tracks the
// Kubernetes line controller-runtime v0.19 is built against.
const k8sVersion = "1.31.0"

// hasAPIServer reports whether dir holds the kube-apiserver binary.
func hasAPIServer(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "kube-apiserver"))
	return err == nil
}

// resolveAssets locates the directory holding the kube-apiserver and
// etcd binaries. An explicitly-set KUBEBUILDER_ASSETS is authoritative:
// when present it is used as-is and no fallback is attempted, so a
// caller can point at a known-bad path to exercise the skip path. When
// KUBEBUILDER_ASSETS is unset, the setup-envtest cache is queried.
func resolveAssets() (string, bool) {
	if v, ok := os.LookupEnv("KUBEBUILDER_ASSETS"); ok {
		return v, hasAPIServer(v)
	}
	dir := setupEnvtestPath()
	return dir, hasAPIServer(dir)
}

// setupEnvtestPath queries the setup-envtest cache for an installed
// asset bundle. It returns "" when setup-envtest is not installed or
// no bundle for k8sVersion is cached.
func setupEnvtestPath() string {
	bin := "setup-envtest"
	if _, err := exec.LookPath(bin); err != nil {
		out, err := exec.Command("go", "env", "GOPATH").Output()
		if err != nil {
			return ""
		}
		cand := filepath.Join(strings.TrimSpace(string(out)), "bin", "setup-envtest")
		if _, err := os.Stat(cand); err != nil {
			return ""
		}
		bin = cand
	}
	// -i restricts the lookup to already-installed versions and errors
	// out rather than reaching the network when none is cached.
	out, err := exec.Command(bin, "use", k8sVersion, "-i", "-p", "path").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SkipUnlessAvailable resolves the envtest assets directory, skipping
// the test when the binaries are not installed. It returns the
// resolved directory.
func SkipUnlessAvailable(t testing.TB) string {
	t.Helper()
	dir, ok := resolveAssets()
	if !ok {
		t.Skipf("envtest: kube-apiserver/etcd binaries unavailable; set KUBEBUILDER_ASSETS or run `setup-envtest use %s` (scripts/setup-dev.sh installs them)", k8sVersion)
	}
	return dir
}

// Environment is a running envtest control plane with the lenny.dev
// CRDs installed.
type Environment struct {
	cfg    *rest.Config
	assets string
}

// RESTConfig returns the rest config addressing the test API server.
func (e *Environment) RESTConfig() *rest.Config { return e.cfg }

// AssetsPath returns the resolved kube-apiserver/etcd binary directory.
func (e *Environment) AssetsPath() string { return e.assets }

// Start boots an envtest API server and etcd, installs the lenny.dev
// CRDs from charts/lenny/crds, and registers a t.Cleanup that stops
// them. It skips the test when the envtest binaries are not installed.
func Start(t testing.TB) *Environment {
	t.Helper()
	assets := SkipUnlessAvailable(t)
	env := &crenvtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(schematest.RepoRoot(t), "charts", "lenny", "crds")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assets,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest: start API server: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("envtest: stop API server: %v", err)
		}
	})
	return &Environment{cfg: cfg, assets: assets}
}
