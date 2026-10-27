// SPDX-License-Identifier: MIT

package podsession_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane every k8sClient call in this
// package binary reuses. Starting one per test cost about six seconds each
// and put the package over the unit-tier budget (TEST-GAPS T-17.4.22). The
// tests create only namespaced objects and every List the binder issues is
// namespace-scoped, so a per-test namespace isolates them without a private
// control plane.
var sharedEnv envtest.Shared

// testNamespaces holds the namespace each test was assigned.
var testNamespaces envtest.Namespaces

func TestMain(m *testing.M) {
	code := m.Run()
	if err := sharedEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop shared API server: %v\n", err)
	}
	os.Exit(code)
}

// testNamespace returns the namespace the fixtures, the binder, and the
// assertions of test t use. It only names the namespace: k8sClient creates
// it on the shared control plane, so a test that never calls k8sClient pays
// nothing. A subtest runs in the namespace of a parent that already built a
// k8sClient, because it shares that client; otherwise it gets its own
// namespace, so a subtest that seeds its own k8sClient starts from an empty
// namespace, as it did when each call started a fresh control plane.
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}
