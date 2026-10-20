// SPDX-License-Identifier: MIT

//go:build component

package slotrelease_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// createAgentPod creates the agent pod backing the fixture's Sandbox, which
// the §4.6.1 reconciler names identically, so the gateway's tenant stamp has
// a pod to land on.
func (f *releaseFixture) createAgentPod(t *testing.T) {
	t.Helper()
	if err := f.kube.Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: releasePod, Namespace: releaseNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}}},
	}); err != nil {
		t.Fatalf("create the agent pod: %v", err)
	}
}

// projectOccupancy runs one WarmPoolController occupancy reconcile of the
// fixture's Sandbox and returns the phase it leaves.
func (f *releaseFixture) projectOccupancy(t *testing.T) string {
	t.Helper()
	r := &warmpool.OccupancyReconciler{Client: f.kube}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: releaseNS, Name: releasePod},
	}); err != nil {
		t.Fatalf("occupancy reconcile: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := f.kube.Get(context.Background(), client.ObjectKey{Namespace: releaseNS, Name: releasePod}, &sb); err != nil {
		t.Fatalf("read the Sandbox: %v", err)
	}
	return sb.Status.Phase
}

// podTenant returns the tenant pin on the agent pod.
func (f *releaseFixture) podTenant(t *testing.T) string {
	t.Helper()
	var pod corev1.Pod
	if err := f.kube.Get(context.Background(), client.ObjectKey{Namespace: releaseNS, Name: releasePod}, &pod); err != nil {
		t.Fatalf("read the agent pod: %v", err)
	}
	return pod.Labels[podclaim.LabelTenant]
}

// spec: §4.6.1 (Occupancy projection), §5.2 (Pinned idle inventory.)
// diagnosis: the accepted departure for a claim deleted before the warm pool
// controller projected `claimed` changed. The pod is expected back in `idle`,
// unscrubbed, with its lenny.dev/tenant-id pin, which the pinned-idle rules
// govern. A pod that projects `draining` means the projection now sees the
// deleted claim, which closes the departure and the register row; a pod that
// lost its pin means a cross-tenant reuse of an unscrubbed pod.
func TestReleaseBeforeClaimedProjectionLeavesPinnedIdlePod_spec_4_6_1(t *testing.T) {
	t.Run("released before the projection", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.createAgentPod(t)
		f.reserve(t, "sess-a")
		if err := f.binder.ReleaseSlotReservation(context.Background(), releasePod, "sess-a", false); err != nil {
			t.Fatalf("ReleaseSlotReservation: %v", err)
		}
		if f.claimExists(t) {
			t.Fatal("the release did not delete the claim")
		}
		if got := f.projectOccupancy(t); got != "idle" {
			t.Errorf("Sandbox phase = %q, want idle", got)
		}
		if got := f.podTenant(t); got != "acme" {
			t.Errorf("pod tenant pin = %q, want acme", got)
		}
	})

	// The contrast: once the projection saw the claim, its deletion retires
	// the unscrubbed pod through draining rather than returning it to idle.
	t.Run("released after the projection", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.createAgentPod(t)
		f.reserve(t, "sess-a")
		if got := f.projectOccupancy(t); got != "claimed" {
			t.Fatalf("Sandbox phase with a bound claim = %q, want claimed", got)
		}
		if err := f.binder.ReleaseSlotReservation(context.Background(), releasePod, "sess-a", false); err != nil {
			t.Fatalf("ReleaseSlotReservation: %v", err)
		}
		if got := f.projectOccupancy(t); got != "draining" {
			t.Errorf("Sandbox phase = %q, want draining", got)
		}
	})
}
