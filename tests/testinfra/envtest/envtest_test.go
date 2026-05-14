// SPDX-License-Identifier: MIT

package envtest_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: 12.2.4 (envtest harness skips without assets)
// diagnosis: SkipUnlessAvailable fired t.Fatalf instead of t.Skip
//
//	on a host without KUBEBUILDER_ASSETS. The scaffold's
//	soft-skip path is broken.
func TestSkipsWhenAssetsMissing(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "/nonexistent/path")
	// Run as subtest to capture the Skip without short-circuiting
	// the parent test.
	t.Run("inner", func(inner *testing.T) {
		defer func() {
			if inner.Failed() {
				t.Errorf("inner did not skip cleanly")
			}
		}()
		envtest.SkipUnlessAvailable(inner)
	})
}

// spec: 12.2.4 (envtest start short-circuits when LENNY_ENVTEST_REAL is unset)
// diagnosis: Start did not skip in scaffold mode. The opt-in env
//
//	gate is wrong.
func TestStartSkipsInScaffoldMode(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "/nonexistent/path")
	t.Setenv("LENNY_ENVTEST_REAL", "")
	t.Run("inner", func(inner *testing.T) {
		envtest.Start(inner)
	})
}
