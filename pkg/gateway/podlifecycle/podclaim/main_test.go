// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane every newEnvtestClient call in
// this package binary reuses, isolated per test by namespace. Starting one
// per test cost about six seconds each and made the package the slowest in
// the unit tier (TEST-GAPS T-17.4.22). The tests create only namespaced
// objects, and the claimers list Sandboxes only within their own namespace.
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

// testNamespace returns the namespace the fixtures, the claimers, and the
// assertions of test t use. It only names the namespace; newEnvtestClient
// creates it. A subtest runs in the namespace of a parent that already built
// a client, and otherwise gets its own (envtest.Namespaces).
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}
