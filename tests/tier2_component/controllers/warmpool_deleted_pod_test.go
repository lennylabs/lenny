// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// deletionHoldFinalizer keeps a deleted pod present with its deletion
// timestamp set. envtest runs no kubelet, and the API server removes an
// unscheduled pod at once on delete, so the finalizer stands in for the
// graceful-termination window in which the kubelet sends SIGTERM and the
// pod lingers until its containers exit.
const deletionHoldFinalizer = "lenny.dev/test-deletion-hold"

// enablePreConnect turns the seeded runtime into a §6.1 preConnect
// runtime, so the Sandbox-to-Pod reconciler warms through sdk_connecting.
func (e *terminatedAdapterEnv) enablePreConnect() {
	e.t.Helper()
	var rt lennyv1.Runtime
	if err := e.c.Get(e.ctx, client.ObjectKey{Name: "claude-code"}, &rt); err != nil {
		e.t.Fatalf("get runtime: %v", err)
	}
	rt.Spec.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}
	if err := e.c.Update(e.ctx, &rt); err != nil {
		e.t.Fatalf("enable preConnect on runtime: %v", err)
	}
}

// startPodDeletion deletes the pod while the hold finalizer keeps it
// present with a deletion timestamp, then records the agent container
// exiting on SIGTERM, which is what the kubelet reports during graceful
// termination.
func (e *terminatedAdapterEnv) startPodDeletion(name string) {
	e.t.Helper()
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	p.Finalizers = append(p.Finalizers, deletionHoldFinalizer)
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatalf("add hold finalizer to pod %s: %v", name, err)
	}
	if err := e.c.Delete(e.ctx, &p); err != nil {
		e.t.Fatalf("delete pod %s: %v", name, err)
	}
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		e.t.Fatalf("get deleted pod %s: %v", name, err)
	}
	if p.DeletionTimestamp.IsZero() {
		e.t.Fatalf("deleted pod %s carries no deletion timestamp", name)
	}
	e.setPodStatus(name, true, false)
}

// finishPodDeletion removes the hold finalizer so the API server removes
// the pod, as it does once the kubelet confirms the containers stopped.
func (e *terminatedAdapterEnv) finishPodDeletion(name string) {
	e.t.Helper()
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	p.Finalizers = nil
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatalf("remove hold finalizer from pod %s: %v", name, err)
	}
	if e.podExists(name) {
		e.t.Fatalf("pod %s still present after its finalizer was removed", name)
	}
}

// reconcileNeverFailed reconciles the Sandbox and fails the test when the
// resulting phase is failed: a deleted pod must never be recorded as a
// warm-fill failure.
func (e *terminatedAdapterEnv) reconcileNeverFailed(name string) state.State {
	e.t.Helper()
	e.reconcileSandbox(name)
	got := e.phase(name)
	if got == state.Failed {
		e.t.Fatalf("sandbox %s passed through failed after its pod was deleted", name)
	}
	return got
}

// spec: 6.2 (Pod State Machine), 6.1 (What a Pre-Warmed Pod Looks Like)
// diagnosis: A pod deleted while its Sandbox was in sdk_connecting (a node
// drain, an eviction, or a scale-down sending SIGTERM) was recorded as
// failed, or did not reach terminated, so the §6.2 sdk_connecting →
// terminated edge is not followed. Check that observePod reads the pod's
// deletion timestamp before its container states and that
// lifecycle.DecideSDKWarm maps PodTerminating to terminated.
func TestSDKConnectingPodDeletedReachesTerminatedWithoutFailing(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	e.enablePreConnect()
	name := e.fillOne()

	// The pod runs and pre-connects its SDK: warming → sdk_connecting.
	e.setPodStatus(name, false, false)
	if got := e.reconcileNeverFailed(name); got != state.SDKConnecting {
		t.Fatalf("running preConnect pod: phase = %q, want sdk_connecting", got)
	}

	e.startPodDeletion(name)
	if got := e.reconcileNeverFailed(name); got != state.Terminated {
		t.Fatalf("sdk_connecting pod under deletion: phase = %q, want terminated", got)
	}
	// terminated is a sink: neither the lingering pod nor its removal moves it.
	if got := e.reconcileNeverFailed(name); got != state.Terminated {
		t.Fatalf("terminated sandbox with a terminating pod: phase = %q, want terminated", got)
	}
	e.finishPodDeletion(name)
	if got := e.reconcileNeverFailed(name); got != state.Terminated {
		t.Fatalf("terminated sandbox after pod removal: phase = %q, want terminated", got)
	}
	if e.podExists(name) {
		t.Fatalf("terminated sandbox %s re-created its pod", name)
	}

	// The terminated Sandbox is not warm inventory; the pool replaces it.
	e.reconcilePool()
	if got := e.warmCount(); got != 1 {
		t.Fatalf("warm count after the deleted pod terminated = %d, want minWarm 1", got)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: A pod deleted while its Sandbox was still warming was
// recorded as failed, was promoted while terminating, or a new pod was
// created while the old one was still terminating. §6.2 has no warming
// SIGTERM edge, so the reconciler holds the Sandbox in warming until the
// pod is gone and then creates a fresh pod. Check the PodTerminating arm
// of lifecycle.Decide.
func TestWarmingPodDeletedHoldsThenRecreatesWithoutFailing(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()

	e.startPodDeletion(name)
	before := e.phase(name)
	for i := 0; i < 2; i++ {
		if got := e.reconcileNeverFailed(name); got != before {
			t.Fatalf("warming sandbox with a terminating pod: phase = %q, want %q (held)", got, before)
		}
	}
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		t.Fatalf("get terminating pod: %v", err)
	}
	if p.DeletionTimestamp.IsZero() {
		t.Fatalf("the terminating pod was replaced before it was removed")
	}

	e.finishPodDeletion(name)
	if got := e.reconcileNeverFailed(name); got != before {
		t.Fatalf("warming sandbox after pod removal: phase = %q, want %q", got, before)
	}
	if !e.podExists(name) {
		t.Fatalf("warming sandbox %s did not create a fresh pod after the old one was removed", name)
	}
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		t.Fatalf("get fresh pod: %v", err)
	}
	if !p.DeletionTimestamp.IsZero() {
		t.Fatalf("fresh pod already carries a deletion timestamp")
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: An idle pod deleted by Kubernetes was recorded as failed, or
// its Sandbox did not retire through idle → draining → terminated. The
// reconciler must also not re-issue the delete while the pod terminates.
// Check the PodTerminating arms of lifecycle.Decide for idle and draining.
func TestIdlePodDeletedDrainsToTerminatedWithoutFailing(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()

	e.setPodStatus(name, false, true)
	if got := e.reconcileNeverFailed(name); got != state.Idle {
		t.Fatalf("healthy ready pod: phase = %q, want idle", got)
	}

	e.startPodDeletion(name)
	if got := e.reconcileNeverFailed(name); got != state.Draining {
		t.Fatalf("idle pod under deletion: phase = %q, want draining", got)
	}
	if got := e.reconcileNeverFailed(name); got != state.Draining {
		t.Fatalf("draining sandbox with a terminating pod: phase = %q, want draining", got)
	}
	e.finishPodDeletion(name)
	if got := e.reconcileNeverFailed(name); got != state.Terminated {
		t.Fatalf("draining sandbox after pod removal: phase = %q, want terminated", got)
	}

	e.reconcilePool()
	if got := e.warmCount(); got != 1 {
		t.Fatalf("warm count after the idle pod was deleted = %d, want minWarm 1", got)
	}
}
