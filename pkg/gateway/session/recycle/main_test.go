// SPDX-License-Identifier: MIT

package recycle_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane the newEnvtestClient calls in
// this package binary reuse. Starting one per test cost about six seconds
// each and made this package the slowest in the unit tier (TEST-GAPS
// T-17.4.28).
//
// Tests sharing it see each other's objects, so each test isolates itself in
// its own namespace (testNamespace). The fixtures create only namespaced
// objects (Pods and SandboxClaims), and the coordinators under test read and
// write only the namespace their Namespace option names, by object name, with
// no List, so no test reaches another test's objects. No test in this package
// needs a private control plane.
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

// testNamespace returns the namespace the fixtures, the coordinators, and the
// assertions of envtest-backed test t use. It only names the namespace;
// newEnvtestClient creates it. The fake-client tests keep the fixed testNS,
// because each builds its own in-memory client.
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}
