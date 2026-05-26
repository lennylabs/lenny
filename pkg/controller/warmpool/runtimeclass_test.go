// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// stubRuntimeClassChecker is an injectable warmpool.RuntimeClassChecker
// for the §5.3 line 675 Degraded-condition tests.
type stubRuntimeClassChecker struct {
	exists bool
	err    error
	calls  []string
}

func (s *stubRuntimeClassChecker) RuntimeClassExists(_ context.Context, name string) (bool, error) {
	s.calls = append(s.calls, name)
	return s.exists, s.err
}

func degradedCondition(t *testing.T, p lennyv1.SandboxWarmPool) (metav1.Condition, bool) {
	t.Helper()
	for _, c := range p.Status.Conditions {
		if c.Type == "Degraded" {
			return c, true
		}
	}
	return metav1.Condition{}, false
}

// TestReconcileRuntimeClassMissing_spec_5_3 verifies that a pool whose
// isolation profile maps to an uninstalled RuntimeClass is marked
// Degraded with the spec-mandated message and that no warm pods are
// created (the API server would reject them).
//
// spec: §5.3 line 675.
func TestReconcileRuntimeClassMissing_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))
	stub := &stubRuntimeClassChecker{exists: false}

	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: stub}
	reconcileWith(t, r)

	if got := poolSandboxes(t, c); len(got) != 0 {
		t.Fatalf("created %d sandboxes for a pool with a missing RuntimeClass, want 0", len(got))
	}
	if len(stub.calls) == 0 || stub.calls[0] != "gvisor" {
		t.Fatalf("RuntimeClassExists calls = %v, want first call for %q", stub.calls, "gvisor")
	}
	cond, ok := degradedCondition(t, getPool(t, c))
	if !ok {
		t.Fatalf("pool carries no Degraded condition")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("Degraded.Status = %q, want True", cond.Status)
	}
	if cond.Reason != "RuntimeClassNotFound" {
		t.Errorf("Degraded.Reason = %q, want RuntimeClassNotFound", cond.Reason)
	}
	want := "RuntimeClass 'gvisor' not found — install gVisor or change the pool's isolation profile."
	if cond.Message != want {
		t.Errorf("Degraded.Message = %q, want %q", cond.Message, want)
	}
}

// TestReconcileRuntimeClassPresent_spec_5_3 verifies that when the
// RuntimeClass exists the pool fills normally and carries Degraded=False.
//
// spec: §5.3 line 675.
func TestReconcileRuntimeClassPresent_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10))
	stub := &stubRuntimeClassChecker{exists: true}

	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: stub}
	reconcileWith(t, r)

	if got := poolSandboxes(t, c); len(got) != 2 {
		t.Fatalf("created %d sandboxes, want 2", len(got))
	}
	cond, ok := degradedCondition(t, getPool(t, c))
	if !ok {
		t.Fatalf("pool carries no Degraded condition")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Degraded.Status = %q, want False", cond.Status)
	}
	if cond.Reason != "RuntimeClassPresent" {
		t.Errorf("Degraded.Reason = %q, want RuntimeClassPresent", cond.Reason)
	}
}

// TestReconcileRuntimeClassRecovers_spec_5_3 verifies the pool recovers
// from Degraded once the operator installs the missing RuntimeClass: a
// second reconcile flips Degraded True→False and the pool fills.
//
// spec: §5.3 line 675.
func TestReconcileRuntimeClassRecovers_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10))
	stub := &stubRuntimeClassChecker{exists: false}
	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: stub}

	reconcileWith(t, r)
	if cond, _ := degradedCondition(t, getPool(t, c)); cond.Status != metav1.ConditionTrue {
		t.Fatalf("after first pass Degraded = %q, want True", cond.Status)
	}

	// The operator installs gVisor; the next reconcile clears Degraded.
	stub.exists = true
	reconcileWith(t, r)

	if cond, _ := degradedCondition(t, getPool(t, c)); cond.Status != metav1.ConditionFalse {
		t.Errorf("after recovery Degraded = %q, want False", cond.Status)
	}
	if got := poolSandboxes(t, c); len(got) != 2 {
		t.Errorf("after recovery created %d sandboxes, want 2", len(got))
	}
}

// TestReconcileRuntimeClassCheckerError_spec_5_3 verifies a checker
// error fails the reconcile (so it retries) rather than mislabeling a
// transient API error as a missing RuntimeClass.
//
// spec: §5.3 line 675.
func TestReconcileRuntimeClassCheckerError_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 10))
	stub := &stubRuntimeClassChecker{err: errors.New("apiserver unreachable")}

	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: stub}
	if _, err := r.Reconcile(testContext(), reconcileRequest()); err == nil {
		t.Fatal("Reconcile returned nil error on RuntimeClass check failure, want error")
	}
	if got := poolSandboxes(t, c); len(got) != 0 {
		t.Errorf("created %d sandboxes despite check failure, want 0", len(got))
	}
}

// TestReconcileNilRuntimeClassChecker_spec_5_3 verifies that a build
// without the checker wired (nil RuntimeClasses) writes no Degraded
// condition and fills the pool, preserving the prior counts-only status.
//
// spec: §5.3 line 675.
func TestReconcileNilRuntimeClassChecker_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10))

	reconcile(t, c, s) // builds a Reconciler with no RuntimeClasses checker

	if got := poolSandboxes(t, c); len(got) != 2 {
		t.Fatalf("created %d sandboxes, want 2", len(got))
	}
	if _, ok := degradedCondition(t, getPool(t, c)); ok {
		t.Error("pool carries a Degraded condition with no checker wired; want none")
	}
}

// TestReaderRuntimeClassChecker_spec_5_3 covers the production
// reader-backed checker: present and absent RuntimeClasses.
//
// spec: §5.3 line 675.
func TestReaderRuntimeClassChecker_spec_5_3(t *testing.T) {
	s := runtime.NewScheme()
	if err := nodev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme nodev1: %v", err)
	}
	fc := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(
		&nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "gvisor"}, Handler: "runsc"},
	).Build()
	checker := warmpool.NewReaderRuntimeClassChecker(fc)

	if ok, err := checker.RuntimeClassExists(testContext(), "gvisor"); err != nil || !ok {
		t.Errorf("RuntimeClassExists(gvisor) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := checker.RuntimeClassExists(testContext(), "kata"); err != nil || ok {
		t.Errorf("RuntimeClassExists(kata) = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestReconcileUnsetProfileSkipsRuntimeClassCheck_spec_5_3 verifies an
// unset isolation profile (one that maps to no RuntimeClass; a fault the
// pod builder surfaces separately) does not produce a Degraded
// RuntimeClass condition and does not call the checker.
//
// spec: §5.3 line 675.
func TestReconcileUnsetProfileSkipsRuntimeClassCheck_spec_5_3(t *testing.T) {
	s := newScheme(t)
	tmpl := template()
	tmpl.Spec.IsolationProfile = "" // optional; maps to no RuntimeClass
	c := newClient(t, s, tmpl, pool(1, 10))
	stub := &stubRuntimeClassChecker{exists: false}

	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: stub}
	reconcileWith(t, r)

	if len(stub.calls) != 0 {
		t.Errorf("RuntimeClassExists called %v for an unset profile, want no calls", stub.calls)
	}
	if cond, ok := degradedCondition(t, getPool(t, c)); ok {
		t.Errorf("pool carries a Degraded condition %+v for an unset profile, want none", cond)
	}
}

// guard against an accidental message-format regression that would drop
// the actionable remediation half of the §5.3 message.
func TestRuntimeClassMissingMessageActionable_spec_5_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 10))
	r := &warmpool.Reconciler{Client: c, Scheme: s, RuntimeClasses: &stubRuntimeClassChecker{exists: false}}
	reconcileWith(t, r)
	cond, _ := degradedCondition(t, getPool(t, c))
	if !strings.Contains(cond.Message, "install gVisor") {
		t.Errorf("Degraded.Message %q omits the install remediation", cond.Message)
	}
}
