// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane the podBindClient calls in this
// package binary reuse. A private control plane per parallel test multiplied
// the etcd and kube-apiserver processes on the host, and under load a start
// failed the unit tier (TEST-GAPS T-17.4.27).
//
// Tests sharing it see each other's objects, so each test keeps its namespaced
// objects (SandboxWarmPool, SandboxTemplate, Sandbox, SandboxClaim, and Pod)
// in its own namespace (testNS), and the server and binder under test take
// that namespace as their agent namespace.
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

// testNS returns the namespace the fixtures, the session server, and the
// assertions of test t use. It only names the namespace; podBindEnvtestClient
// creates it.
func testNS(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}
