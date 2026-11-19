// SPDX-License-Identifier: MIT

package poolstatus_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// testNamespace returns the agent namespace the fake clients hold the
// pool's PodSet in.
func testNamespace(t *testing.T) string {
	t.Helper()
	return "lenny-agents"
}

// newScheme returns a scheme that holds the lenny CRD types.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}
