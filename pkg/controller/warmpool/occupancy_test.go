// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// occupancyClaim builds a per-pod occupancy PodClaim (claim-<podName>)
// for the named AgentPod. The status (binding phase, rewarm stamp) is seeded
// separately via seedClaimStatus because the API server rejects status on
// create.
func occupancyClaim(ns, podName, binding string) *lennyv1.PodClaim {
	return &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + podName, Namespace: ns},
		Spec: lennyv1.PodClaimSpec{
			AgentPodRef: podName,
			TenantID:    "acme",
		},
		Status: lennyv1.PodClaimStatus{Phase: binding},
	}
}

// seedClaimStatus writes the claim's binding-state status via SSA Apply under
// the Gateway field manager (the §4.6.3 owner of PodClaim.status), so the
// occupancy projection observes the same status the gateway writes in
// production. rewarm, when true, stamps rewarmStartedAt (the preConnect
// re-warm anchor).
func seedClaimStatus(t *testing.T, c client.Client, podName, binding string, rewarm bool) {
	t.Helper()
	ns := testNamespace(t)
	patch := &lennyv1.PodClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "PodClaim",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + podName, Namespace: ns},
	}
	patch.Status.Phase = binding
	if rewarm {
		now := metav1.Now()
		patch.Status.RewarmStartedAt = &now
	}
	if err := c.Status().Patch(context.Background(), patch, client.Apply,
		client.FieldOwner(string(ownership.Gateway))); err != nil {
		t.Fatalf("seed claim status %s=%s: %v", podName, binding, err)
	}
}

// reconcileOccupancy runs one occupancy-projection reconcile for the named
// AgentPod.
func reconcileOccupancy(t *testing.T, c client.Client, podName string) {
	t.Helper()
	ns := testNamespace(t)
	r := &warmpool.OccupancyReconciler{Client: c}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: ns, Name: podName},
	}); err != nil {
		t.Fatalf("occupancy reconcile %s: %v", podName, err)
	}
}

func agentPodPhase(t *testing.T, c client.Client, podName string) string {
	t.Helper()
	ns := testNamespace(t)
	var sb lennyv1.AgentPod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: podName}, &sb); err != nil {
		t.Fatalf("get sandbox %s: %v", podName, err)
	}
	return sb.Status.Phase
}

// occupiedAgentPod is a AgentPod seeded into the given coarse phase, so a
// claim-deletion projection has an occupied phase to act on.
func occupiedAgentPod(ns, name, phase string) *lennyv1.AgentPod {
	sb := idleAgentPod(ns, name)
	sb.Status.Phase = phase
	return sb
}

// diagnosis: a failure means the WarmPoolController is not projecting a bound
// PodClaim onto AgentPod.status.phase = claimed, so the gateway claim path
// (which reads idle and never re-reads claimed) and every PDB/inventory
// selector keyed on the coarse phase observe a pod still labeled idle while it
// serves a session.
//
// spec: 4.6.1 (occupancy projection), 6.14 (binding-state enumeration), 3.3
// (ownership decomposition).
func TestOccupancyProjectsBoundClaimToClaimed(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, idleAgentPod(ns, "pod-1"), occupancyClaim(ns, "pod-1", "bound"))
	seedClaimStatus(t, c, "pod-1", "bound", false)

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "claimed" {
		t.Errorf("phase = %q, want claimed", got)
	}
}

// diagnosis: a failure means the WarmPoolController is not holding a recycling
// pod at claimed during the whole-pod scrub, so the sdkConnectTimeoutSeconds
// watchdog runs against the scrub time and retires recycling pools to failed
// before they can reach reserved.
//
// spec: 4.6.1 (occupancy projection), 6.2 (recycle edges, scrub leg).
func TestOccupancyProjectsRecyclingScrubToClaimed(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "claimed"), occupancyClaim(ns, "pod-1", "recycling"))
	seedClaimStatus(t, c, "pod-1", "recycling", false)

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "claimed" {
		t.Errorf("phase = %q, want claimed (scrub leg held at claimed)", got)
	}
}

// diagnosis: a failure means the WarmPoolController is not moving a recycling
// preConnect pod to sdk_connecting once the rewarm stamp lands, so the re-warm
// watchdog clock is never armed at the re-warm-start anchor.
//
// spec: 4.6.1 (occupancy projection), 6.2 (recycle re-warm edge), 6.14 (rewarm
// stamp).
func TestOccupancyProjectsRecyclingRewarmToSDKConnecting(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "claimed"), occupancyClaim(ns, "pod-1", "recycling"))
	seedClaimStatus(t, c, "pod-1", "recycling", true)

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "sdk_connecting" {
		t.Errorf("phase = %q, want sdk_connecting (preConnect re-warm leg)", got)
	}
}

// diagnosis: a failure means the WarmPoolController is not projecting a
// reserved claim onto the reserved phase, so the PoolScalingController counts a
// held pod as idle inventory and the PDB idle selector protects a pod inside
// the hold window.
//
// spec: 4.6.1 (occupancy projection), 6.2 (reserved hold semantics).
func TestOccupancyProjectsReservedClaimToReserved(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "sdk_connecting"), occupancyClaim(ns, "pod-1", "reserved"))
	seedClaimStatus(t, c, "pod-1", "reserved", false)

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "reserved" {
		t.Errorf("phase = %q, want reserved", got)
	}
}

// diagnosis: a failure means a terminal claim disposition (released or failed)
// is not draining the pod, so a retired pod is never replaced and the orphan
// claim accumulates.
//
// spec: 4.6.1 (occupancy projection), 6.14 (terminal dispositions).
func TestOccupancyProjectsTerminalDispositionToDraining(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "claimed"), occupancyClaim(ns, "pod-1", "released"))
	seedClaimStatus(t, c, "pod-1", "released", false)

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "draining" {
		t.Errorf("phase = %q, want draining", got)
	}
}

// diagnosis: a failure means a hold-expiry claim DELETE on a reserved pod is
// not returning the scrubbed, SDK-warm pod to idle, so the pinned tenant's
// recycled pod never re-enters claimable inventory.
//
// spec: 4.6.1 (occupancy projection on claim DELETE), 6.2 (reserved → idle
// hold expiry).
func TestOccupancyClaimDeletedOnReservedPodReturnsToIdle(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "reserved"))

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "idle" {
		t.Errorf("phase = %q, want idle (hold expiry returns the pod to the pool)", got)
	}
}

// diagnosis: a failure means a claim DELETE on a still-claimed (unscrubbed) pod
// is returning it to idle instead of draining, breaking the scrub-before-idle
// invariant — an unscrubbed pod re-enters claimable inventory carrying the
// previous session's residual state.
//
// spec: 4.6.1 (occupancy projection on claim DELETE), 6.2 (claimed → draining),
// 5.2 (scrub-before-idle invariant).
func TestOccupancyClaimDeletedOnClaimedPodDrains(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, occupiedAgentPod(ns, "pod-1", "claimed"))

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "draining" {
		t.Errorf("phase = %q, want draining (unscrubbed occupied pod retires)", got)
	}
}

// diagnosis: a failure means the projection writes a phase for an unclaimed
// warm-fill pod, fighting the AgentPod-to-Pod reconciler that owns the
// warming/idle/sdk_connecting warm-fill legs.
//
// spec: 4.6.1 (occupancy projection), 3.3 (ownership decomposition), 6.2
// (warm-fill legs).
func TestOccupancyLeavesUnclaimedIdlePodUntouched(t *testing.T) {
	ns := testNamespace(t)
	s := newScheme(t)
	c := newClient(t, s, idleAgentPod(ns, "pod-1"))

	reconcileOccupancy(t, c, "pod-1")

	if got := agentPodPhase(t, c, "pod-1"); got != "idle" {
		t.Errorf("phase = %q, want idle (warm-fill writer owns the unclaimed pod)", got)
	}
}

// diagnosis: a failure means the claim→sandbox map function does not enqueue
// the owning AgentPod for a PodClaim event, so a gateway binding-state
// change never re-projects the pod phase and occupancy goes stale.
//
// spec: 4.6.1 (claim-existence occupancy projection via the PodClaim
// watch).
func TestClaimToAgentPodMapsToOwner(t *testing.T) {
	ns := testNamespace(t)
	r := &warmpool.OccupancyReconciler{}
	reqs := r.ClaimToAgentPodForTest(context.Background(), occupancyClaim(ns, "pod-7", "bound"))
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Name != "pod-7" || reqs[0].Namespace != ns {
		t.Errorf("request = %s/%s, want %s/pod-7", reqs[0].Namespace, reqs[0].Name, ns)
	}
}
