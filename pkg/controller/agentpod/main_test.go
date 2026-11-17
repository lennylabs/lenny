// SPDX-License-Identifier: MIT

package agentpod_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane the newClient calls in this
// package binary reuse. Starting one per test cost about six seconds each
// and made this package the slowest in the unit tier (TEST-GAPS T-17.4.26).
//
// Tests sharing it see each other's objects, so each test isolates itself
// two ways. Its namespaced objects (AgentPod, Pod, PodSet,
// PodSet, and PodClaim) live in its own namespace
// (testNamespace). Its cluster-scoped Runtime carries a name unique to the
// test (testRuntime), and every AgentPod and PodSet fixture
// references that name. The AgentPod reconciler gets the Runtime by the
// AgentPod's runtimeRef and lists PodClaims only within the AgentPod's
// namespace, so no test reaches another test's objects. The RuntimeClasses
// newClient creates are cluster-scoped too, but they are identical in every
// test and created idempotently. No test in this package needs a private
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

// testNamespace returns the namespace the fixtures, the reconciler, and the
// assertions of test t use. It only names the namespace; newClient creates
// it.
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}

// testRuntime returns the name of the cluster-scoped Runtime test t seeds and
// references. It reuses the test's namespace name, which is unique to the
// test and a valid DNS-1123 label, so it is also a valid Runtime name and a
// valid value for the lenny.dev/runtime pod label the reconciler stamps from
// the AgentPod's runtimeRef.
func testRuntime(t *testing.T) string {
	t.Helper()
	return testNamespace(t)
}
