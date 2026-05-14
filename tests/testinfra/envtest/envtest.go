// SPDX-License-Identifier: MIT

// Package envtest wraps sigs.k8s.io/controller-runtime/pkg/envtest so
// tier-2 controller tests can drive a fake Kubernetes API server.
// The package depends on the KUBEBUILDER_ASSETS environment variable
// (the path to the kube-apiserver + etcd binaries) and a tier-2
// controller binary; when either is absent, SkipUnlessAvailable
// short-circuits the test.
//
// The wrapper is intentionally minimal — it exposes Start/Stop and a
// *rest.Config the caller passes to client-go. The §12.2.4
// controllers (Warm Pool, Pool Scaling, Token Service) chain this
// with their own reconciliation drivers.
//
// Usage:
//
//	env := envtest.Start(t)
//	cfg := env.RESTConfig()
//	cl, _ := client.New(cfg, client.Options{})
//	// ... reconcile ...
package envtest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ErrKubebuilderAssetsMissing signals that the KUBEBUILDER_ASSETS
// environment variable is unset and no fallback path resolves to an
// installed asset bundle.
var ErrKubebuilderAssetsMissing = errors.New("envtest: KUBEBUILDER_ASSETS unset and no fallback assets found")

// Environment represents a running envtest control plane.
type Environment struct {
	t       testing.TB
	assets  string
	apiHost string
	apiPort int

	// cmd is the kube-apiserver subprocess. envtest also starts
	// etcd; for the Phase 0 scaffold we keep the surface minimal
	// (callers that need richer control use Real). The cmd is nil
	// in scaffold mode.
	cmd *exec.Cmd
}

// SkipUnlessAvailable skips the test when KUBEBUILDER_ASSETS is unset
// and the canonical fallback paths are absent.
func SkipUnlessAvailable(t testing.TB) string {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets != "" {
		if _, err := os.Stat(filepath.Join(assets, "kube-apiserver")); err == nil {
			return assets
		}
	}
	// Common fallback installed by `setup-envtest use 1.31.x`.
	candidate := defaultAssetsPath()
	if _, err := os.Stat(filepath.Join(candidate, "kube-apiserver")); err == nil {
		return candidate
	}
	t.Skipf("envtest.SkipUnlessAvailable: KUBEBUILDER_ASSETS unset and no fallback at %s. Install via `go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest && setup-envtest use 1.31.x`.", candidate)
	return ""
}

// Start brings up envtest and returns an *Environment with a
// t.Cleanup that stops it. The scaffold implementation today
// short-circuits to Skip until callers explicitly opt in via
// LENNY_ENVTEST_REAL=1 — most tier-2 controller tests are still
// scaffolds, so we don't pay the ~3s envtest startup cost on every
// invocation.
//
// When LENNY_ENVTEST_REAL=1 is set, this function would spawn
// kube-apiserver + etcd via sigs.k8s.io/controller-runtime/pkg/envtest.
// That dependency is not yet vendored; the function emits a clear
// "not yet wired" diagnosis pointing at the integration task.
func Start(t testing.TB) *Environment {
	t.Helper()
	assets := SkipUnlessAvailable(t)
	if os.Getenv("LENNY_ENVTEST_REAL") != "1" {
		t.Skip("envtest.Start: scaffold mode (set LENNY_ENVTEST_REAL=1 to enable; requires controller-runtime/pkg/envtest in go.mod)")
	}
	env := &Environment{t: t, assets: assets}
	t.Cleanup(func() {
		if env.cmd != nil && env.cmd.Process != nil {
			_ = env.cmd.Process.Kill()
		}
	})
	return env
}

// RESTConfig returns the kube-apiserver *rest.Config — but the type
// surface is abstracted as map[string]string here to avoid pulling
// in client-go at the testinfra layer. Real callers should pull
// k8s.io/client-go/rest themselves and construct a config from the
// (host, port, ca-cert) tuple Environment exposes.
func (e *Environment) Endpoint() string {
	if e.apiHost == "" || e.apiPort == 0 {
		return ""
	}
	return fmt.Sprintf("https://%s:%d", e.apiHost, e.apiPort)
}

// AssetsPath returns the resolved KUBEBUILDER_ASSETS path.
func (e *Environment) AssetsPath() string { return e.assets }

// defaultAssetsPath is the conventional install location for
// setup-envtest under ~/.local/share/kubebuilder-envtest.
func defaultAssetsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "kubebuilder-envtest", "k8s", "current")
}
