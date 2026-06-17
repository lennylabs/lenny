// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// preConnectRuntimeCR is a §5.1 preConnect-capable runtime so the
// Sandbox-to-Pod reconciler routes the Sandbox through the SDK-warm path
// (sdk_connecting with the §6.1 watchdog) rather than straight to
// pod-warm idle.
func preConnectRuntimeCR() *lennyv1.Runtime {
	rt := runtimeCR()
	rt.Spec.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}
	return rt
}

// sdkWarmPoolCR is the §6.1 pool whose ScalePolicy sets the
// sdkConnectTimeoutSeconds watchdog budget the Sandbox references through
// PoolRef. The Sandbox-to-Pod reconciler resolves the budget from it.
func sdkWarmPoolCR(timeoutSeconds int64) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNS},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: "claude-template",
			MinWarm:     1,
			MaxWarm:     3,
			ScalePolicy: &lennyv1.ScalePolicy{SDKConnectTimeoutSeconds: timeoutSeconds},
		},
	}
}

// seedRecyclingClaim creates the per-pod SandboxClaim (claim-<podName>) in
// the recycling binding state and stamps rewarmStartedAt on its status, so
// the Sandbox-to-Pod reconciler observes the §6.2 recycle re-warm edge and
// anchors the watchdog clock at the stamp (§6.1). The whole-pod scrub has
// already reported and the disposition is recycle; the SDK re-warm leg is
// in progress.
func seedRecyclingClaim(t *testing.T, c client.Client, rewarmStartedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	cl := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + testName, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: testName, TenantID: "acme"},
	}
	if err := c.Create(ctx, cl); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create recycling claim: %v", err)
	}
	cl.Status.Phase = string(claimstate.Recycling)
	stamp := metav1.NewTime(rewarmStartedAt)
	cl.Status.RewarmStartedAt = &stamp
	if err := c.Status().Update(ctx, cl); err != nil {
		t.Fatalf("seed recycling claim status: %v", err)
	}
}

// TestReconcileRecycleRewarmReadyPodLeavesSDKConnecting is the §6.1/§3.3
// reserved-terminus component case: on the recycle re-warm edge the
// success terminus is reserved, written by the claim projection
// (OccupancyReconciler) when the gateway patches the claim recycling →
// reserved. The Sandbox-to-Pod reconciler must not drive sdk_connecting →
// idle on a ready pod; it makes a clean no-action exit so the two arms do
// not fight over the phase.
//
// spec: §6.1 (reserved terminus), §6.2 (recycle edges), §3.3
//
// diagnosis: the SDK-warm arm raced the claim projection on the recycle
// re-warm edge and wrote idle on a ready pod, breaking the reserved hold
// (a recycled pod would re-enter idle inventory before the gateway's
// reserved patch and could be claimed by another tenant).
func TestReconcileRecycleRewarmReadyPodLeavesSDKConnecting(t *testing.T) {
	s := newScheme(t)
	c := newClient(
		t, s,
		sandboxCR("sdk_connecting"),
		preConnectRuntimeCR(),
		sdkWarmPoolCR(60),
		podCR(corev1.PodRunning, true),
	)
	// Re-warm started 5s ago, well within the 60s budget.
	seedRecyclingClaim(t, c, time.Now().Add(-5*time.Second))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "sdk_connecting" {
		t.Errorf("phase = %q, want sdk_connecting (the reserved terminus is the claim projection's to write, not idle)", got)
	}
}

// TestReconcileRecycleRewarmAnchorsClockAtRewarmStamp is the §6.1/§3.3
// re-warm-start anchor component case: a pod that has been running far
// longer than the watchdog budget must not time out on the recycle
// re-warm edge when the rewarmStartedAt stamp is within budget, because
// neither the prior occupancy episode nor the whole-pod scrub counts
// against the re-warm budget. The clock is measured from the stamp.
//
// spec: §6.1 (re-warm-start anchor), §3.3
//
// diagnosis: the watchdog anchored at pod start on the recycle re-warm
// edge and counted the prior occupancy episode and the whole-pod scrub
// against the re-warm budget, retiring a healthy re-warming pod to failed.
func TestReconcileRecycleRewarmAnchorsClockAtRewarmStamp(t *testing.T) {
	s := newScheme(t)
	pod := podCR(corev1.PodRunning, false) // running but not ready: re-warm in progress
	// The pod has been running for 10 minutes (far over the 60s budget),
	// modeling a long prior occupancy episode plus the scrub.
	started := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	pod.Status.StartTime = &started
	c := newClient(
		t, s,
		sandboxCR("sdk_connecting"),
		preConnectRuntimeCR(),
		sdkWarmPoolCR(60),
		pod,
	)
	// Re-warm started 5s ago: within the 60s budget despite the old pod.
	seedRecyclingClaim(t, c, time.Now().Add(-5*time.Second))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "sdk_connecting" {
		t.Errorf("phase = %q, want sdk_connecting (re-warm within budget; the prior episode must not count)", got)
	}
}

// TestReconcileRecycleRewarmWatchdogRetiresOverBudget is the §6.1/§3.3
// re-warm watchdog component case: a re-warm that exceeds
// sdkConnectTimeoutSeconds measured from rewarmStartedAt retires the pod
// to failed via the sdk_connecting → failed edge.
//
// spec: §6.1 (re-warm watchdog), §6.2 (sdk_connecting → failed), §3.3
//
// diagnosis: the recycle re-warm watchdog did not fire on an over-budget
// re-warm, leaving a pod hung in sdk_connecting holding a warm-pool slot.
func TestReconcileRecycleRewarmWatchdogRetiresOverBudget(t *testing.T) {
	s := newScheme(t)
	c := newClient(
		t, s,
		sandboxCR("sdk_connecting"),
		preConnectRuntimeCR(),
		sdkWarmPoolCR(60),
		podCR(corev1.PodRunning, false), // running but not ready: SDK still connecting
	)
	// Re-warm started 90s ago: past the 60s budget.
	seedRecyclingClaim(t, c, time.Now().Add(-90*time.Second))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "failed" {
		t.Errorf("phase = %q, want failed (the re-warm exceeded sdkConnectTimeoutSeconds from rewarmStartedAt)", got)
	}
}

// TestReconcileWarmFillReadyPodReachesIdle is the §6.1 warm-fill-edge
// terminus component case: with no claim (no recycle re-warm), a ready pod
// in sdk_connecting reaches idle, the warm-fill success terminus the
// Sandbox-to-Pod reconciler owns. This pins the two-terminus split: idle on
// the warm-fill edge, reserved (claim-projection-written) on the recycle
// edge.
//
// spec: §6.1 (idle terminus on the warm-fill edge), §6.2
//
// diagnosis: the warm-fill SDK-warm terminus regressed; a ready
// preConnect pod no longer becomes claimable.
func TestReconcileWarmFillReadyPodReachesIdle(t *testing.T) {
	s := newScheme(t)
	c := newClient(
		t, s,
		sandboxCR("sdk_connecting"),
		preConnectRuntimeCR(),
		sdkWarmPoolCR(60),
		podCR(corev1.PodRunning, true),
	)
	// No SandboxClaim: this is the warm-fill edge, not a recycle re-warm.

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "idle" {
		t.Errorf("phase = %q, want idle (warm-fill SDK-warm terminus)", got)
	}
}
