// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// condTypeConcurrentSharing is the §13.1 line 29 warning-class condition
// the WarmPoolController stamps on a concurrent-workspace pool bound to a
// credential-bearing Runtime. The literal also guards the wire-visible
// condition name. F-13.1.5.
const condTypeConcurrentSharing = "ConcurrentWorkspaceCredentialSharing"

// digest64 is a 64-hex-char digest satisfying the Runtime CRD's
// @sha256:<64hex> image-pattern validation.
const digest64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func runtimeCR(name string, providers []string) *lennyv1.Runtime {
	rt := &lennyv1.Runtime{
		Spec: lennyv1.RuntimeSpec{
			Type:               "agent",
			Image:              "registry.example.com/agent@sha256:" + digest64,
			IntegrationLevel:   "basic",
			ExecutionMode:      "session",
			SupportedProviders: providers,
		},
	}
	rt.Name = name
	return rt
}

func sharingCondition(t *testing.T, conds []metav1.Condition) (metav1.Condition, bool) {
	t.Helper()
	for _, c := range conds {
		if c.Type == condTypeConcurrentSharing {
			return c, true
		}
	}
	return metav1.Condition{}, false
}

// spec: §13.1 line 29 — the cross-slot credential-sharing warning fires
// when a pool runs more than one session per pod
// (`maxConcurrentSessions > 1`). That trigger lives on the gateway-side
// sessionPolicy mirror in the poolstore, which the SandboxTemplate CRD
// does not carry, so the condition is unmanaged from the CRD surface
// until the warmpool step wires the mirror into the reconcile. The
// SandboxTemplate `executionMode` enum no longer admits `concurrent`, so
// no CRD template selects the warning branch. F-13.1.5.
func TestReconcileSessionModePoolLeavesCredentialSharingUnmanaged(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s,
		runtimeCR("cred-runtime", []string{"anthropic_direct"}),
		template(), // default session-mode template
		pool(1, 3),
	)
	reconcile(t, c, s)

	p := getPool(t, c)
	if _, ok := sharingCondition(t, p.Status.Conditions); ok {
		t.Errorf("a session-mode pool carries the %s condition; conditions = %+v",
			condTypeConcurrentSharing, p.Status.Conditions)
	}
}
