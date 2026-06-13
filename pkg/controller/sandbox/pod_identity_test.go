// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// podWithContainersReady builds a Running pod whose ContainersReady
// condition is True but whose Pod.Ready condition is unset — the state a
// real kubelet reports while the §6.1 readiness gate is still pending.
func podWithContainersReady() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "stub", Image: "k8s.gcr.io/pause"}},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      testPodIP,
			Conditions: []corev1.PodCondition{{Type: corev1.ContainersReady, Status: corev1.ConditionTrue}},
		},
	}
}

func podCondition(pod corev1.Pod, t corev1.PodConditionType) (corev1.PodCondition, bool) {
	for _, c := range pod.Status.Conditions {
		if c.Type == t {
			return c, true
		}
	}
	return corev1.PodCondition{}, false
}

// TestReconcileFlipsReadinessGateWhenContainersReady_spec_6_1 verifies the
// §6.1 line 18 claimability handoff: once the pod's containers are ready,
// the reconciler flips the lenny.dev/sandbox-ready readiness gate to True.
// The Sandbox stays warming this pass because Pod.Ready is not yet True (a
// real kubelet recomputes it from ContainersReady + the now-True gate).
func TestReconcileFlipsReadinessGateWhenContainersReady_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podWithContainersReady())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	pod := getPod(t, c)
	cond, ok := podCondition(pod, corev1.PodConditionType(podspec.ReadinessGateSandboxReady))
	if !ok || cond.Status != corev1.ConditionTrue {
		t.Errorf("readiness gate %q = (%v, present=%v), want True", podspec.ReadinessGateSandboxReady, cond.Status, ok)
	}
	if got := getSandbox(t, c).Status.Phase; got != "warming" {
		t.Errorf("phase = %q, want warming (Pod.Ready not yet True via the gate)", got)
	}
}

// TestReconcileDoesNotFlipGateBeforeContainersReady_spec_6_1 verifies the
// reconciler waits: a pending pod (no ContainersReady) leaves the gate
// unset so claimability is not asserted prematurely.
func TestReconcileDoesNotFlipGateBeforeContainersReady_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodPending, false))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	pod := getPod(t, c)
	if _, ok := podCondition(pod, corev1.PodConditionType(podspec.ReadinessGateSandboxReady)); ok {
		t.Error("readiness gate set while the pod's containers are not yet ready")
	}
}

// TestReconcileStampsCoarseActiveLabel_spec_6_2 verifies an occupied
// coarse phase (claimed) maps to the coarse lenny.dev/state value "active"
// rather than carrying the raw §6.2 phase. The phase itself is gateway-
// owned, so the planner takes no action; only the label sync runs.
func TestReconcileStampsCoarseActiveLabel_spec_6_2(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("claimed"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getPod(t, c).Labels["lenny.dev/state"]; got != "active" {
		t.Errorf("coarse state label = %q, want active for the claimed phase", got)
	}
}

// TestReconcileRemovesCoarseLabelOnTerminal_spec_6_2 verifies a terminal
// coarse phase (terminated) clears the coarse lenny.dev/state label rather
// than leaving a stale value: a terminated pod is neither idle, active, nor
// draining.
func TestReconcileRemovesCoarseLabelOnTerminal_spec_6_2(t *testing.T) {
	s := newScheme(t)
	pod := podCR(corev1.PodRunning, true)
	pod.Labels = map[string]string{"lenny.dev/state": "active"}
	c := newClient(t, s, sandboxCR("terminated"), runtimeCR(), pod)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, ok := getPod(t, c).Labels["lenny.dev/state"]; ok {
		t.Errorf("coarse state label = %q, want removed for the terminal terminated phase", got)
	}
}

// TestReconcileThreadsSAToken_spec_10_3 verifies the §10.3 projected-token
// audience configured on the reconciler reaches the created pod's spec.
func TestReconcileThreadsSAToken_spec_10_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR(""), runtimeCR())

	r := &sandbox.Reconciler{
		Client:                  c,
		Scheme:                  s,
		AdapterImage:            "ghcr.io/lennylabs/lenny-adapter:v1",
		SATokenAudience:         "lenny-gateway-acme",
		AgentServiceAccountName: "lenny-agent",
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	pod := getPod(t, c)
	var found bool
	for _, v := range pod.Spec.Volumes {
		if v.Name != "lenny-sa-token" {
			continue
		}
		found = true
		if v.Projected == nil || len(v.Projected.Sources) != 1 ||
			v.Projected.Sources[0].ServiceAccountToken == nil ||
			v.Projected.Sources[0].ServiceAccountToken.Audience != "lenny-gateway-acme" {
			t.Errorf("projected SA token = %+v, want audience lenny-gateway-acme", v.Projected)
		}
	}
	if !found {
		t.Error("created pod is missing the projected SA token volume despite a configured audience")
	}
	if pod.Spec.ServiceAccountName != "lenny-agent" {
		t.Errorf("ServiceAccountName = %q, want lenny-agent", pod.Spec.ServiceAccountName)
	}
}
