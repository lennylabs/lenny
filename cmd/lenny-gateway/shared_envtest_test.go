// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane this package binary reuses, so
// the envtest-backed tests do not pay for a control-plane start each
// (TEST-GAPS T-17.4.25).
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

// sharedEnvtestClient returns a client of the shared control plane and the
// namespace it created for test t. Tests sharing the control plane see each
// other's objects, so each test keeps its objects in that namespace.
func sharedEnvtestClient(t *testing.T, scheme *runtime.Scheme) (client.Client, string) {
	t.Helper()
	cl, err := client.New(sharedEnv.Start(t).RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return cl, testNamespaces.Create(t, cl)
}
