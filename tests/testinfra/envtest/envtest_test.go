// SPDX-License-Identifier: MIT

package envtest_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: 12.2.4 (the envtest harness skips, rather than fails, when the
//
//	kube-apiserver binaries are unavailable)
//
// diagnosis: SkipUnlessAvailable fired t.Fatalf instead of t.Skip on a
//
//	host where KUBEBUILDER_ASSETS points at a path with no
//	kube-apiserver binary.
func TestSkipUnlessAvailableSkipsOnMissingAssets(t *testing.T) {
	// An explicitly-set KUBEBUILDER_ASSETS is authoritative, so this
	// bad path disables the setup-envtest fallback and forces the
	// skip path regardless of what is installed on the host.
	t.Setenv("KUBEBUILDER_ASSETS", "/nonexistent/envtest/path")

	t.Run("inner", func(inner *testing.T) {
		defer func() {
			if inner.Failed() {
				t.Errorf("SkipUnlessAvailable failed instead of skipping")
			}
			if !inner.Skipped() {
				t.Errorf("SkipUnlessAvailable did not skip on missing assets")
			}
		}()
		envtest.SkipUnlessAvailable(inner)
	})
}

// spec: 12.2.4 (Start skips cleanly when the envtest binaries are
//
//	absent so the controller suites do not hard-fail)
//
// diagnosis: Start did not skip when KUBEBUILDER_ASSETS resolves to a
//
//	directory with no kube-apiserver binary.
func TestStartSkipsOnMissingAssets(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "/nonexistent/envtest/path")

	t.Run("inner", func(inner *testing.T) {
		defer func() {
			if inner.Failed() {
				t.Errorf("Start failed instead of skipping")
			}
			if !inner.Skipped() {
				t.Errorf("Start did not skip on missing assets")
			}
		}()
		envtest.Start(inner)
	})
}
