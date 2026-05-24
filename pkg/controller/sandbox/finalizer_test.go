// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func finalizedSandbox(phase string) *lennyv1.Sandbox {
	sb := sandboxCR(phase)
	sb.Finalizers = []string{lennyv1.FinalizerSessionCleanup}
	return sb
}

func getPod(t *testing.T, c client.Client) corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	return pod
}

// spec: §4.6.1 "Sandbox finalizers" — the controller removes the
// session-cleanup finalizer once no active SandboxClaim references the
// Sandbox, allowing Kubernetes to complete the deletion.
func TestReconcileRemovesFinalizerWhenNoActiveClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, finalizedSandbox("claimed"), runtimeCR())

	ctx := context.Background()
	sb := getSandbox(t, c)
	if err := c.Delete(ctx, &sb); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got lennyv1.Sandbox
	err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: testName}, &got)
	if !apierrors.IsNotFound(err) {
		t.Errorf("sandbox still present after finalizer removal (err=%v, finalizers=%v)", err, got.Finalizers)
	}
}

// spec: §4.6.1 — while an active SandboxClaim references the Sandbox the
// finalizer is held, keeping the pod (and its workspace) alive until the
// session is resolved.
func TestReconcileHoldsFinalizerWhileClaimActive(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, finalizedSandbox("claimed"), runtimeCR())

	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-1", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: testName, SessionID: "sess-1"},
	}); err != nil {
		t.Fatalf("create claim: %v", err)
	}
	sb := getSandbox(t, c)
	if err := c.Delete(ctx, &sb); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := getSandbox(t, c)
	found := false
	for _, f := range got.Finalizers {
		if f == lennyv1.FinalizerSessionCleanup {
			found = true
		}
	}
	if !found {
		t.Errorf("finalizer removed while an active claim references the sandbox (finalizers=%v)", got.Finalizers)
	}
}

// spec: §4.6.1 "Disruption protection for agent pods" — the reconciler
// stamps the live §6.2 phase as the lenny.dev/state pod label on
// creation so the warm-pod PDB's idle selector can target the pod.
func TestReconcileStampsPodStateLabelOnCreate(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR(""), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	pod := getPod(t, c)
	if got := pod.Labels[state.LabelState]; got != string(state.Warming) {
		t.Errorf("pod state label = %q, want warming", got)
	}
}

// spec: §4.6.1 — the reconciler keeps the lenny.dev/state pod label in
// sync as the Sandbox advances through §6.2 phases (warming → idle when
// the pod becomes ready).
func TestReconcileSyncsPodStateLabelOnTransition(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := getSandbox(t, c).Status.Phase; got != "idle" {
		t.Fatalf("sandbox phase = %q, want idle (precondition for label sync)", got)
	}
	pod := getPod(t, c)
	if got := pod.Labels[state.LabelState]; got != string(state.Idle) {
		t.Errorf("pod state label = %q, want idle after warming→idle transition", got)
	}
}
