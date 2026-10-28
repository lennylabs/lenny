// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane the newClient calls in this
// package binary reuse, isolated per test by namespace. Starting one per
// test cost about six seconds each (TEST-GAPS T-17.4.22). The reconcilers
// under test list and get only within the namespace of the pool, claim, or
// pod they reconcile; newClient keeps a test that seeds a cluster-scoped
// object on a private control plane.
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

// testNamespace returns the namespace the fixtures, the reconcilers, and
// the assertions of test t use. It only names the namespace; newClient
// creates it. A subtest runs in the namespace of a parent that already built
// a client, and otherwise gets its own (envtest.Namespaces).
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}
