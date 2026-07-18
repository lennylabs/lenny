// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func getPDB(t *testing.T, c client.Client, name string) (policyv1.PodDisruptionBudget, bool) {
	t.Helper()
	var pdb policyv1.PodDisruptionBudget
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pdb)
	if apierrors.IsNotFound(err) {
		return pdb, false
	}
	if err != nil {
		t.Fatalf("get pdb %s: %v", name, err)
	}
	return pdb, true
}

// spec: §4.6.1 "Disruption protection for agent pods" — the controller
// owns a per-pool PDB for warm pods with an integer minAvailable of
// minWarm-1 selecting idle pods, never maxUnavailable: 1 (which would
// deadlock at disruptionsAllowed: 0 with no scale subresource).
func TestReconcileCreatesWarmPodPDB(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))

	reconcile(t, c, s)

	pdb, ok := getPDB(t, c, testPool+"-warm")
	if !ok {
		t.Fatalf("expected a per-pool PDB to be created")
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 2 {
		t.Errorf("PDB minAvailable = %v, want 2 (minWarm-1)", pdb.Spec.MinAvailable)
	}
	if pdb.Spec.MaxUnavailable != nil {
		t.Errorf("PDB sets maxUnavailable = %v, want unset (spec mandates integer minAvailable)", pdb.Spec.MaxUnavailable)
	}
	sel := pdb.Spec.Selector.MatchLabels
	if sel[warmpool.LabelPool] != testPool || sel[state.LabelState] != string(state.Idle) {
		t.Errorf("PDB selector = %v, want pool=%s state=idle", sel, testPool)
	}
	if len(pdb.OwnerReferences) != 1 || pdb.OwnerReferences[0].Kind != "SandboxWarmPool" {
		t.Errorf("PDB owner references = %+v, want one SandboxWarmPool", pdb.OwnerReferences)
	}
}

// spec: §4.6.1 — a pool with minWarm below 2 gets no PDB, because a
// single warm pod cannot be given one-at-a-time protection by an integer
// minAvailable without deadlocking.
func TestReconcileCreatesNoPDBBelowMinWarmTwo(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 10))

	reconcile(t, c, s)

	if _, ok := getPDB(t, c, testPool+"-warm"); ok {
		t.Errorf("PDB created for a pool with minWarm=1, want no PDB below minWarm 2")
	}
}

// spec: §4.6.1 — a PDB spec carries exactly one of minAvailable and
// maxUnavailable, so the convergence path must clear a maxUnavailable
// that a previously reconciled object still carries. Seeding the pre-fix
// object and reconciling asserts the update replaces maxUnavailable: 1
// with the integer minAvailable of minWarm-1.
func TestReconcileConvergesMaxUnavailableToMinAvailable(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))

	maxUnavailable := intstr.FromInt(1)
	existing := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: testPool + "-warm", Namespace: testNS},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					warmpool.LabelPool: testPool,
					state.LabelState:   string(state.Idle),
				},
			},
		},
	}
	if err := c.Create(context.Background(), existing); err != nil {
		t.Fatalf("seed pre-fix PDB: %v", err)
	}

	reconcile(t, c, s)

	pdb, ok := getPDB(t, c, testPool+"-warm")
	if !ok {
		t.Fatalf("expected the seeded PDB to survive reconciliation")
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 2 {
		t.Errorf("converged PDB minAvailable = %v, want 2 (minWarm-1)", pdb.Spec.MinAvailable)
	}
	if pdb.Spec.MaxUnavailable != nil {
		t.Errorf("converged PDB still sets maxUnavailable = %v, want nil", pdb.Spec.MaxUnavailable)
	}
}

// spec: §4.6.1 — a pool scaled to zero imposes no disruption budget, so
// the PDB is torn down.
func TestReconcileDeletesPDBWhenScaledToZero(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10))

	reconcile(t, c, s)
	pdb, ok := getPDB(t, c, testPool+"-warm")
	if !ok {
		t.Fatalf("expected a PDB for the positive-minWarm pool")
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 1 {
		t.Errorf("PDB minAvailable = %v, want 1 (minWarm-1 for minWarm=2)", pdb.Spec.MinAvailable)
	}

	// Scale the pool to zero and reconcile again.
	var p lennyv1.SandboxWarmPool
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testPool}, &p); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	p.Spec.MinWarm = 0
	if err := c.Update(context.Background(), &p); err != nil {
		t.Fatalf("scale pool to zero: %v", err)
	}
	reconcile(t, c, s)

	if _, ok := getPDB(t, c, testPool+"-warm"); ok {
		t.Errorf("PDB still present after scaling the pool to zero")
	}
}

// spec: §4.6.1 "Sandbox finalizers" — every Sandbox the controller
// creates carries the session-cleanup finalizer.
func TestCreateSandboxStampsFinalizer(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 10))

	reconcile(t, c, s)

	var sandboxes lennyv1.SandboxList
	if err := c.List(context.Background(), &sandboxes, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes.Items) == 0 {
		t.Fatalf("expected the controller to create at least one Sandbox")
	}
	for _, sb := range sandboxes.Items {
		found := false
		for _, f := range sb.Finalizers {
			if f == lennyv1.FinalizerSessionCleanup {
				found = true
			}
		}
		if !found {
			t.Errorf("Sandbox %s missing the session-cleanup finalizer (finalizers=%v)", sb.Name, sb.Finalizers)
		}
	}
}
