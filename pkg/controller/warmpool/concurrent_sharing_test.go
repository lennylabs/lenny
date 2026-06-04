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
			ExecutionMode:      "concurrent",
			SupportedProviders: providers,
		},
	}
	rt.Name = name
	return rt
}

func concurrentWorkspaceTemplate(runtimeRef string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       runtimeRef,
			IsolationProfile: "sandboxed",
			DeliveryMode:     "proxy",
			ExecutionMode:    "concurrent",
			ConcurrencyStyle: "workspace",
			MaxConcurrent:    4,
		},
	}
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

// spec: §13.1 line 29 — a concurrent-workspace pool against a Runtime
// with non-empty supportedProviders carries the warning-class condition
// True so the cross-slot credential-read tradeoff is visible in pool
// status. F-13.1.5.
func TestReconcileStampsConcurrentWorkspaceCredentialSharing(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s,
		runtimeCR("cred-runtime", []string{"anthropic_direct"}),
		concurrentWorkspaceTemplate("cred-runtime"),
		pool(1, 3),
	)
	reconcile(t, c, s)

	p := getPool(t, c)
	cond, ok := sharingCondition(t, p.Status.Conditions)
	if !ok {
		t.Fatalf("pool carries no %s condition; conditions = %+v", condTypeConcurrentSharing, p.Status.Conditions)
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("%s = %s, want True", condTypeConcurrentSharing, cond.Status)
	}
	if cond.Reason != "CredentialBearingRuntime" {
		t.Errorf("reason = %q, want CredentialBearingRuntime", cond.Reason)
	}
}

// A concurrent-workspace pool against a Runtime with no supportedProviders
// records the condition False: no credentials are shared across slots.
// F-13.1.5.
func TestReconcileConcurrentWorkspaceNoProvidersConditionFalse(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s,
		runtimeCR("nocred-runtime", nil),
		concurrentWorkspaceTemplate("nocred-runtime"),
		pool(1, 3),
	)
	reconcile(t, c, s)

	p := getPool(t, c)
	cond, ok := sharingCondition(t, p.Status.Conditions)
	if !ok {
		t.Fatalf("pool carries no %s condition; conditions = %+v", condTypeConcurrentSharing, p.Status.Conditions)
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("%s = %s, want False for a non-credential-bearing runtime", condTypeConcurrentSharing, cond.Status)
	}
}

// A non-concurrent pool leaves the credential-sharing condition unmanaged;
// the §13.1 line 29 warning applies only to concurrent-workspace pools.
// F-13.1.5.
func TestReconcileNonConcurrentNoCredentialSharingCondition(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s,
		runtimeCR("cred-runtime", []string{"anthropic_direct"}),
		template(), // default session-mode template
		pool(1, 3),
	)
	reconcile(t, c, s)

	p := getPool(t, c)
	if _, ok := sharingCondition(t, p.Status.Conditions); ok {
		t.Errorf("a non-concurrent pool carries the %s condition; conditions = %+v",
			condTypeConcurrentSharing, p.Status.Conditions)
	}
}
