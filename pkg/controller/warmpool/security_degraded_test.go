// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// nonceOnlyIdleSandbox builds an idle member Sandbox carrying the §4.5
// nonce-only signals the Sandbox reconciler resolves for a rendered pool:
// the Sandbox.spec.requireSoPeercred: false carrier and the
// SOPeercredDisabled=True status condition. The pool-level trigger reads
// both back without resolving the Runtime CR.
func nonceOnlyIdleSandbox(name string) *lennyv1.Sandbox {
	sb := idleSandbox(name)
	sb.Spec.RequireSoPeercred = ptr.To(false)
	sb.Status.Conditions = []metav1.Condition{{
		Type:               lennyv1.SandboxConditionSOPeercredDisabled,
		Status:             metav1.ConditionTrue,
		Reason:             "RenderedNonceOnly",
		Message:            "Pod rendered with --require-so-peercred=false.",
		LastTransitionTime: metav1.Now(),
	}}
	return sb
}

func securityDegradedCondition(t *testing.T, tm lennyv1.SandboxTemplate) (metav1.Condition, bool) {
	t.Helper()
	cond := apimeta.FindStatusCondition(tm.Status.Conditions, lennyv1.SandboxTemplateConditionSecurityDegradedMode)
	if cond == nil {
		return metav1.Condition{}, false
	}
	return *cond, true
}

// spec: §4.5 (pool-level SecurityDegradedMode surfacing from member
// Sandboxes), §4.7 (controller-mediated condition writer + alert gauge),
// §5.3 (acknowledgment gate), §16.5 (alert-support gauge).
// diagnosis: a failure means the WarmPoolController does not surface the
// nonce-only degradation an acknowledged sidecar pool runs in. Either the
// SecurityDegradedMode=True condition is missing from the SandboxTemplate
// status or the lenny_pool_security_degraded gauge does not reflect it, so
// the bundled alert has no live series and operators are blind to a pool
// whose adapter-agent SO_PEERCRED enforcement is disabled.
func TestSecurityDegradedAcknowledgedNonceOnlyPool(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10),
		nonceOnlyIdleSandbox("sb-a"), nonceOnlyIdleSandbox("sb-b"))

	warmpool.ForgetSecurityDegradedForTest(testPool)
	t.Cleanup(func() { warmpool.ForgetSecurityDegradedForTest(testPool) })

	reconcile(t, c, s)

	cond, ok := securityDegradedCondition(t, getTemplate(t, c))
	if !ok {
		t.Fatalf("template carries no SecurityDegradedMode condition")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("SecurityDegradedMode = %s, want True", cond.Status)
	}
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 1 {
		t.Errorf("lenny_pool_security_degraded gauge = %v, want 1", g)
	}
}

// spec: §4.5, §4.7, §5.3 (a pool without acknowledgment renders no flag),
// §16.5.
// diagnosis: a failure means the WarmPoolController marks a pool degraded
// that runs no nonce-only pods. An unacknowledged pool renders no
// --require-so-peercred=false flag, so no member Sandbox carries the
// carrier or the SOPeercredDisabled condition; a spurious
// SecurityDegradedMode=True would false-alarm operators and contradict the
// §5.3 acknowledgment gate.
func TestSecurityDegradedUnacknowledgedPoolNoCondition(t *testing.T) {
	s := newScheme(t)
	// Members carry no nonce-only signal: the unacknowledged render path
	// (and a CR-applied runtime flip without a pool acknowledgment) leaves
	// the carrier nil and writes no SOPeercredDisabled condition.
	c := newClient(t, s, template(), pool(2, 10),
		idleSandbox("sb-a"), idleSandbox("sb-b"))

	warmpool.ForgetSecurityDegradedForTest(testPool)
	t.Cleanup(func() { warmpool.ForgetSecurityDegradedForTest(testPool) })

	reconcile(t, c, s)

	// A pool that never rendered a nonce-only pod gets no SecurityDegradedMode
	// condition at all; the gauge (0) is the only clean-pool signal.
	if _, ok := securityDegradedCondition(t, getTemplate(t, c)); ok {
		t.Errorf("template carries a SecurityDegradedMode condition; want none for an unacknowledged pool")
	}
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 0 {
		t.Errorf("lenny_pool_security_degraded gauge = %v, want 0", g)
	}
}

// spec: §4.1 (embedded runtimes never activate nonce-only mode), §4.5,
// §4.7, §16.5.
// diagnosis: a failure means the WarmPoolController surfaces nonce-only
// degradation for a pool whose embedded runtime can never render the flag.
// The Sandbox reconciler's deployment-model qualifier keeps the carrier off
// embedded-runtime Sandboxes, so the member-Sandbox trigger reads no signal
// and the pool must report a clean posture with gauge 0.
func TestSecurityDegradedEmbeddedRuntimePoolNoCondition(t *testing.T) {
	s := newScheme(t)
	// An embedded runtime never sets the carrier or condition even when the
	// pool is acknowledged, so its member Sandboxes look exactly like an
	// enforcing pool's. The pool runs zero nonce-only pods.
	c := newClient(t, s, template(), pool(2, 10),
		idleSandbox("sb-a"), idleSandbox("sb-b"))

	warmpool.ForgetSecurityDegradedForTest(testPool)
	t.Cleanup(func() { warmpool.ForgetSecurityDegradedForTest(testPool) })

	reconcile(t, c, s)

	// The embedded runtime never sets the carrier or condition, so the pool
	// runs zero nonce-only pods and gets no SecurityDegradedMode condition.
	if _, ok := securityDegradedCondition(t, getTemplate(t, c)); ok {
		t.Errorf("template carries a SecurityDegradedMode condition; want none for an embedded-runtime pool")
	}
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 0 {
		t.Errorf("lenny_pool_security_degraded gauge = %v, want 0", g)
	}
}

// spec: §4.5, §4.7 (revert latch: degraded until the last nonce-only pod is
// replaced; explicit False on full recovery), §16.5.
// diagnosis: a failure means the pool reports a clean posture while a
// nonce-only pod still serves sessions, or never recovers after the last
// one is replaced. The §4.7 latch keeps SecurityDegradedMode=True while any
// member carries the carrier or the SOPeercredDisabled condition, and
// transitions to an explicit False once none remain.
func TestSecurityDegradedRevertLatchTransitionsToFalse(t *testing.T) {
	s := newScheme(t)
	// minWarm 1: a single nonce-only pod plus a fresh enforcing pod that a
	// post-revert reconcile would create. The latch must hold while the
	// pre-revert pod survives.
	c := newClient(t, s, template(), pool(1, 10),
		nonceOnlyIdleSandbox("sb-nonce"), idleSandbox("sb-fresh"))

	warmpool.ForgetSecurityDegradedForTest(testPool)
	t.Cleanup(func() { warmpool.ForgetSecurityDegradedForTest(testPool) })

	reconcile(t, c, s)

	cond, ok := securityDegradedCondition(t, getTemplate(t, c))
	if !ok || cond.Status != metav1.ConditionTrue {
		t.Fatalf("SecurityDegradedMode = %+v, want True while a nonce-only pod remains", cond)
	}
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 1 {
		t.Errorf("gauge = %v, want 1 while latched", g)
	}

	// The pre-revert nonce-only pod is replaced (deleted); only the
	// enforcing pod remains. The next reconcile transitions to False.
	if err := c.Delete(testContext(), &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-nonce", Namespace: testNS},
	}); err != nil {
		t.Fatalf("delete nonce-only sandbox: %v", err)
	}

	reconcile(t, c, s)

	// The explicit False is written only because the pool was previously
	// True: the live SandboxTemplate carried SecurityDegradedMode, so the
	// recovery transition fires. A pool that was never degraded would get no
	// condition at all (TestSecurityDegradedUnacknowledgedPoolNoCondition).
	cond, ok = securityDegradedCondition(t, getTemplate(t, c))
	if !ok || cond.Status != metav1.ConditionFalse {
		t.Fatalf("SecurityDegradedMode = %+v, want explicit False after the last nonce-only pod is replaced", cond)
	}
	if cond.Reason != "SOPeercredEnforced" {
		t.Errorf("recovery reason = %q, want SOPeercredEnforced", cond.Reason)
	}
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 0 {
		t.Errorf("gauge = %v, want 0 after recovery", g)
	}
}

// spec: §4.7 (gauge published every reconcile step that writes the
// condition), §16.5.
// diagnosis: a failure means the WarmPoolController publishes the
// lenny_pool_security_degraded gauge only on a condition change, so a
// controller restart leaves the bundled alert with no live series. The
// gauge must be re-established on every reconcile, like the
// lenny_pool_warming_up precedent.
func TestSecurityDegradedGaugePublishedEveryReconcile(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10),
		nonceOnlyIdleSandbox("sb-a"), nonceOnlyIdleSandbox("sb-b"))

	warmpool.ForgetSecurityDegradedForTest(testPool)
	t.Cleanup(func() { warmpool.ForgetSecurityDegradedForTest(testPool) })

	reconcile(t, c, s)
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 1 {
		t.Fatalf("gauge after first reconcile = %v, want 1", g)
	}

	// Forget the series to simulate a controller restart, then reconcile
	// again with no condition change; the gauge must be re-established.
	warmpool.ForgetSecurityDegradedForTest(testPool)
	reconcile(t, c, s)
	if g := warmpool.SecurityDegradedGaugeForTest(testPool); g != 1 {
		t.Errorf("gauge after re-reconcile = %v, want 1 (published every reconcile)", g)
	}
}
