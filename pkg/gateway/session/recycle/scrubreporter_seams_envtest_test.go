// SPDX-License-Identifier: MIT

package recycle_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// envtestScheme registers lenny.dev/v1alpha1 and corev1 so the envtest
// client can read/write SandboxClaims and Pods.
func envtestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lennyv1: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	return s
}

// newEnvtestClient boots an envtest API server, creates the agent
// namespace, and seeds the supplied objects.
func newEnvtestClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	env := envtest.Start(t)
	c, err := client.NewWithWatch(env.RESTConfig(), client.Options{Scheme: envtestScheme(t)})
	if err != nil {
		t.Fatalf("client.NewWithWatch: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", testNS, err)
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
	}
	return c
}

// seedRecyclingClaim creates a per-pod SandboxClaim and patches it through
// bound → recycling, the precondition the recycle disposition driver
// patches from.
func seedRecyclingClaim(t *testing.T, c client.Client, podID string) string {
	t.Helper()
	name := podclaim.ClaimName(podID)
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: podID, TenantID: "acme"},
	}
	if err := c.Create(context.Background(), claim); err != nil {
		t.Fatalf("create claim %s: %v", name, err)
	}
	if err := podclaim.WriteBoundStatus(context.Background(), c, testNS, name); err != nil {
		t.Fatalf("seed bound: %v", err)
	}
	if err := podclaim.WriteRecyclingStatus(context.Background(), c, testNS, name, nil); err != nil {
		t.Fatalf("seed recycling: %v", err)
	}
	return name
}

func getClaim(t *testing.T, c client.Client, name string) *lennyv1.SandboxClaim {
	t.Helper()
	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got); err != nil {
		t.Fatalf("get claim %s: %v", name, err)
	}
	return &got
}

// TestClaimDispositionRecycleNonPreConnectReserves verifies the driver
// patches a recycling claim directly to `reserved` on a non-preConnect
// pool, stamping holdExpiresAt at the reservation time plus the hold TTL.
// spec: 3.4 (recycle disposition), 4.6.1 (reserved hold), 5.2 (recycle lifecycle)
//
// diagnosis: a failure means a scrubbed non-preConnect pod is not held for its
// pinned tenant after recycle, so a back-to-back same-tenant session re-acquires
// a pod instead of rebinding the reserved claim.
func TestClaimDispositionRecycleNonPreConnectReserves_spec_3_4(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, HoldTTL: 10 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", false, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	got := getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Reserved) {
		t.Fatalf("phase = %q, want reserved", got.Status.Phase)
	}
	if got.Status.HoldExpiresAt == nil || !got.Status.HoldExpiresAt.Time.Equal(now.Add(10*time.Second)) {
		t.Errorf("holdExpiresAt = %v, want %v", got.Status.HoldExpiresAt, now.Add(10*time.Second))
	}
}

// captureRegistrar records the §3.2 reserved-hold tokens the disposition
// driver hands it, so a test can assert the driver registers the hold timer
// after a non-preConnect reserved patch.
type captureRegistrar struct {
	pods  []string
	holds []podclaim.ReservedHold
}

func (r *captureRegistrar) Hold(podID string, hold podclaim.ReservedHold) {
	r.pods = append(r.pods, podID)
	r.holds = append(r.holds, hold)
}

// TestClaimDispositionRecycleNonPreConnectRegistersHold verifies the driver
// hands the §3.2 reserved-hold token (the pod, and the UID/resourceVersion
// observed at the reserved patch) to the HoldRegistrar so the coordinator
// arms the hold-TTL expiry timer.
// spec: 3.2 (reserved hold timer ownership), 4.6.1 (claimHoldTTLSeconds)
//
// diagnosis: a failure means a recycled non-preConnect pod is reserved but no
// expiry timer is armed, so the pod is held until the slower orphan GC reclaims
// it rather than returning to idle at holdExpiresAt plus the local grace.
func TestClaimDispositionRecycleNonPreConnectRegistersHold_spec_3_2(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	reg := &captureRegistrar{}
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, HoldTTL: 10 * time.Second,
		Now:   func() time.Time { return now },
		Holds: reg,
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", false, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if len(reg.pods) != 1 || reg.pods[0] != "pod-1" {
		t.Fatalf("registrar saw pods %v, want [pod-1]", reg.pods)
	}
	got := getClaim(t, c, name)
	if reg.holds[0].ResourceVersion != got.ResourceVersion {
		t.Errorf("registered hold resourceVersion = %q, want the reserved-patch version %q", reg.holds[0].ResourceVersion, got.ResourceVersion)
	}
	if !reg.holds[0].HoldExpiresAt.Equal(now.Add(10 * time.Second)) {
		t.Errorf("registered holdExpiresAt = %v, want %v", reg.holds[0].HoldExpiresAt, now.Add(10*time.Second))
	}
}

// TestClaimDispositionRecyclePreConnectDoesNotRegisterHold verifies a
// preConnect recycle stamps rewarmStartedAt only and does not register a hold
// synchronously. The reserved patch and the hold registration follow from the
// §3.4 RecycleBoundaryCoordinator's re-warm completion poll once the SDK
// re-warm makes the pod Ready (exercised in the boundary-coordinator tests),
// not from the disposition driver. spec: 3.2 (reserved hold timer ownership),
// 3.4 (re-warm completion drives recycling → reserved), 6.2 (preConnect re-warm)
//
// diagnosis: a failure means a preConnect pod's hold timer is armed before the
// claim has even entered reserved, so the timer would delete a recycling claim
// mid-re-warm.
func TestClaimDispositionRecyclePreConnectDoesNotRegisterHold_spec_3_2(t *testing.T) {
	c := newEnvtestClient(t)
	seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	reg := &captureRegistrar{}
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return now }, Holds: reg,
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", true, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if len(reg.pods) != 0 {
		t.Errorf("preConnect recycle registered holds %v, want none (reserved patch follows the re-warm)", reg.pods)
	}
}

// TestHoldCoordinatorExpiresReservedClaimAgainstAPIServer verifies the real
// §3.2 coordinator (with its podclaim-wired DELETE seam) deletes a reserved
// claim against the envtest API server once the hold TTL elapses, returning
// the pod to idle. It exercises the NewHoldCoordinator constructor closures
// end-to-end rather than the injected unit seams.
// spec: 3.2 (precondition-guarded hold-expiry DELETE), 4.6.1 (reserved hold)
//
// diagnosis: a failure means the coordinator's wired DELETE seam does not
// reclaim a reserved claim on expiry, so a recycled pod never returns to idle
// without the slower orphan GC.
func TestHoldCoordinatorExpiresReservedClaimAgainstAPIServer_spec_3_2(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	// Reserve the claim and capture the real hold token.
	hold, err := podclaim.WriteReservedStatus(context.Background(), c, testNS, name, time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatalf("WriteReservedStatus: %v", err)
	}
	hc, err := recycle.NewHoldCoordinator(recycle.HoldCoordinatorOptions{Client: c, Namespace: testNS})
	if err != nil {
		t.Fatalf("NewHoldCoordinator: %v", err)
	}
	defer hc.Stop()
	// Arm with an already-elapsed deadline so the timer fires at the grace
	// period and the DELETE lands promptly.
	hold.HoldExpiresAt = time.Now().Add(-time.Second)
	hc.Hold("pod-1", hold)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var got lennyv1.SandboxClaim
		err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got)
		if apierrors.IsNotFound(err) {
			return // the claim was deleted: the pod returns to idle
		}
		if err != nil {
			t.Fatalf("get claim: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("reserved claim was not deleted within 5s of hold expiry")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHoldCoordinatorExpiryAbortsAfterRebindAgainstAPIServer verifies the real
// coordinator's expiry DELETE aborts (the claim survives as bound) when a
// rebind changed the resourceVersion before the timer fired, the §3.2
// rebind-vs-hold-expiry precondition race exercised against the API server.
// spec: 3.2 (rebind-vs-hold-expiry precondition race)
//
// diagnosis: a failure means the coordinator's DELETE is not fenced on the
// reserved-patch resourceVersion, so a hold-expiry timer would delete a claim
// a same-tenant session already rebound, dropping a live session's pod.
func TestHoldCoordinatorExpiryAbortsAfterRebindAgainstAPIServer_spec_3_2(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	hold, err := podclaim.WriteReservedStatus(context.Background(), c, testNS, name, time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatalf("WriteReservedStatus: %v", err)
	}
	// A rebind lands first (any replica), changing the resourceVersion the
	// stale hold token carries.
	if err := podclaim.WriteRebindStatus(context.Background(), c, testNS, name, nil); err != nil {
		t.Fatalf("WriteRebindStatus: %v", err)
	}

	hc, err := recycle.NewHoldCoordinator(recycle.HoldCoordinatorOptions{Client: c, Namespace: testNS})
	if err != nil {
		t.Fatalf("NewHoldCoordinator: %v", err)
	}
	defer hc.Stop()
	hold.HoldExpiresAt = time.Now().Add(-time.Second) // fire promptly
	hc.Hold("pod-1", hold)

	// Wait past the grace period; the claim must survive (the DELETE aborted)
	// and remain bound.
	time.Sleep(recycle.HoldExpiryGracePeriod + 500*time.Millisecond)
	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got); err != nil {
		t.Fatalf("rebound claim must survive a stale expiry DELETE, got: %v", err)
	}
	if got.Status.Phase != string(claimstate.Bound) {
		t.Errorf("phase = %q, want bound (rebound claim intact after aborted expiry)", got.Status.Phase)
	}
}

// TestClaimDispositionRecyclePreConnectStampsRewarm verifies the driver
// stamps rewarmStartedAt on a recycling claim on a preConnect pool and
// leaves the phase `recycling` so the projection enters sdk_connecting.
// spec: 3.4 (recycle disposition), 6.2 (preConnect re-warm)
//
// diagnosis: a failure means a preConnect pod is reserved without anchoring
// the SDK re-warm watchdog, breaking the invariant that every reserved pod in
// a preConnect pool is SDK-warm.
func TestClaimDispositionRecyclePreConnectStampsRewarm_spec_6_2(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", true, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	got := getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Recycling) {
		t.Errorf("phase = %q, want recycling (re-warm in progress)", got.Status.Phase)
	}
	if got.Status.RewarmStartedAt == nil || !got.Status.RewarmStartedAt.Time.Equal(now) {
		t.Errorf("rewarmStartedAt = %v, want %v", got.Status.RewarmStartedAt, now)
	}
}

// TestClaimDispositionRetireReleased verifies a non-failed retire writes the
// `released` terminal disposition (a lifecycle-limit or cordon drain).
// spec: 3.4 (retire disposition), 4.6.3 (released terminal)
//
// diagnosis: a failure means a limit-reached or cordon-drain retirement does
// not write the released terminal, so the WarmPoolController never drains the
// pod and it lingers as occupied.
func TestClaimDispositionRetireReleased_spec_3_4(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Retire(context.Background(), "pod-1", false, false, "session_count_limit", ""); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Released) {
		t.Errorf("phase = %q, want released", got.Status.Phase)
	}
}

// TestClaimDispositionRetireFailed verifies a failed retire writes the
// `failed` terminal disposition (the onScrubFailure: fail termination).
// spec: 3.4 (retire disposition), 4.6.3 (failed terminal), 5.2 (onScrubFailure fail)
//
// diagnosis: a failure means a fail-policy termination is recorded as released
// rather than failed, conflating a for-cause termination with a lifecycle
// limit in the projection's drain reason and the audit trail.
func TestClaimDispositionRetireFailed_spec_3_4(t *testing.T) {
	c := newEnvtestClient(t)
	name := seedRecyclingClaim(t, c, "pod-1")
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Retire(context.Background(), "pod-1", true, false, "cleanup_fail_policy", "shred timed out"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Failed) {
		t.Errorf("phase = %q, want failed", got.Status.Phase)
	}
}

// scrubWarningStamped reports whether the agent Pod carries the
// lenny.dev/scrub-warning annotation the driver stamps on a warn-policy
// reuse or cordon-drain.
func scrubWarningStamped(t *testing.T, c client.Client, podID string) bool {
	t.Helper()
	var got corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podID}, &got); err != nil {
		t.Fatalf("get pod %s: %v", podID, err)
	}
	return got.Annotations[lennyv1.AnnotationScrubWarning] != ""
}

// TestClaimDispositionRecycleNonPreConnectStampsScrubWarning verifies the
// driver stamps the §5.2 lenny.dev/scrub-warning annotation on the agent Pod
// when a warn-policy scrub failure reserves a non-preConnect pod, so the
// residual-state marker re-enters the pool with the pod.
// spec: 5.2 (warn policy returns the pod with a scrub_warning annotation), 3.4 (recycle disposition)
//
// diagnosis: a failure means a warn-policy non-preConnect pod re-enters the
// pool with no residual-state marker, so an operator inspecting the reused pod
// cannot tell its prior cleanup failed and the §5.2 warn contract is silently
// dropped.
func TestClaimDispositionRecycleNonPreConnectStampsScrubWarning_spec_5_2(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, HoldTTL: 10 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", false, true); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Reserved) {
		t.Errorf("phase = %q, want reserved", got.Status.Phase)
	}
	if !scrubWarningStamped(t, c, "pod-1") {
		t.Error("scrub-warning annotation not stamped on a warn-policy non-preConnect recycle")
	}
}

// TestClaimDispositionRecyclePreConnectStampsScrubWarning verifies the driver
// stamps the §5.2 scrub-warning annotation on a warn-policy preConnect recycle
// before anchoring the re-warm, so the §6.2 marker persists through the
// re-warm while the claim stays `recycling`.
// spec: 5.2 (warn policy returns the pod with a scrub_warning annotation), 6.2 (annotation persists through the preConnect re-warm)
//
// diagnosis: a failure means a preConnect pod re-warms and re-enters the pool
// with no residual-state marker, violating the §6.2 invariant that the
// scrub_warning persists through the re-warm.
func TestClaimDispositionRecyclePreConnectStampsScrubWarning_spec_6_2(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", true, true); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Recycling) {
		t.Errorf("phase = %q, want recycling", got.Status.Phase)
	}
	if !scrubWarningStamped(t, c, "pod-1") {
		t.Error("scrub-warning annotation not stamped on a warn-policy preConnect recycle")
	}
}

// TestClaimDispositionRecycleCleanScrubLeavesNoMarker verifies a clean-scrub
// recycle (scrubWarning false) does not stamp the scrub-warning annotation, so
// a pod whose cleanup succeeded re-enters the pool unmarked.
// spec: 5.2 (only a warn-policy failure marks the pod), 3.4 (recycle disposition)
//
// diagnosis: a failure means every recycled pod is marked scrub_warning even on
// a clean scrub, so the marker no longer distinguishes residual-state risk.
func TestClaimDispositionRecycleCleanScrubLeavesNoMarker_spec_5_2(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, HoldTTL: 10 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", false, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if scrubWarningStamped(t, c, "pod-1") {
		t.Error("scrub-warning annotation stamped on a clean-scrub recycle")
	}
}

// TestClaimDispositionRetireCordonDrainUnderWarnStampsScrubWarning verifies the
// §6.39 cordon-drain-under-warn path stamps the scrub-warning annotation on the
// draining pod (scrubWarning true) so the residual-state marker is retained for
// the audit trail while the claim drains to `released`.
// spec: 6.39 (cordon-drain retains the scrub_warning marker), 5.2 (warn-policy marker)
//
// diagnosis: a failure means a warn-policy pod cordon-drained off an
// unschedulable host loses its residual-state marker, so the audit trail cannot
// tell the drained pod was carrying a failed-cleanup warning.
func TestClaimDispositionRetireCordonDrainUnderWarnStampsScrubWarning_spec_6_39(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	name := seedRecyclingClaim(t, c, "pod-1")
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Retire(context.Background(), "pod-1", false, true, "host_unschedulable", ""); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Released) {
		t.Errorf("phase = %q, want released", got.Status.Phase)
	}
	if !scrubWarningStamped(t, c, "pod-1") {
		t.Error("scrub-warning annotation not stamped on the cordon-drain-under-warn retire")
	}
}

// TestClaimDispositionRetireLimitLeavesNoMarker verifies a lifecycle-limit
// retire (scrubWarning false) does not stamp the scrub-warning annotation: the
// pod is leaving the pool for a limit reached, so the marker is superseded.
// spec: 3.4 (limit retire clears the marker), 5.2 (warn marker scoped to reuse and cordon-drain)
//
// diagnosis: a failure means a clean-scrub limit retirement is mislabeled as a
// residual-state warning, conflating a lifecycle limit with a failed cleanup in
// the audit trail.
func TestClaimDispositionRetireLimitLeavesNoMarker_spec_3_4(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	name := seedRecyclingClaim(t, c, "pod-1")
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Retire(context.Background(), "pod-1", false, false, "session_count_limit", ""); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := getClaim(t, c, name); got.Status.Phase != string(claimstate.Released) {
		t.Errorf("phase = %q, want released", got.Status.Phase)
	}
	if scrubWarningStamped(t, c, "pod-1") {
		t.Error("scrub-warning annotation stamped on a clean-scrub limit retire")
	}
}

// TestInspectForRecycleAgainstApiserver verifies the inspector resolves the
// recycle policy and pod facts against a real API server, reading the
// host-schedulable label off the seeded Pod.
// spec: 5.2 (recycle policy resolution), 6.39 (host-node schedulability via Pods get)
//
// diagnosis: a failure means the gateway's recycle pod read does not work
// against a real apiserver — the Pod Get, label decode, or uptime computation
// regressed, so the disposition cannot be computed in production.
func TestInspectForRecycleAgainstApiserver_spec_6_39(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: testNS,
			Labels: map[string]string{
				warmpool.LabelPool:            "agents",
				warmpool.LabelHostSchedulable: "true",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	// The claim must exist for the recycle boundary: the inspector skips
	// (found=false) when the claim is gone, so seed the recycling claim the
	// pod is bound to. spec: §3.4 (skip when the claim is gone).
	seedRecyclingClaim(t, c, "pod-1")
	// envtest stamps the server-side CreationTimestamp at Create; patch a
	// deterministic one is not possible, so anchor uptime off the real
	// creation time by reading it back and asserting a non-negative value.
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client:    c,
		Namespace: testNS,
		Pools: fakePoolReader{pools: map[string]poolstore.Pool{
			"agents": recyclingPool("agents", "rt", isolation.ProfileSandboxed, &runtimestore.RecyclePolicy{
				Enabled: true, MaxSessionsPerPod: 50,
			}),
		}},
		Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{"rt": {Name: "rt"}}},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || !found {
		t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if !policy.HostSchedulable {
		t.Error("HostSchedulable = false, want true")
	}
	if policy.Pool != "agents" || policy.RuntimeClass != "gvisor" {
		t.Errorf("pool/runtime_class = %q/%q, want agents/gvisor", policy.Pool, policy.RuntimeClass)
	}
	if policy.PodUptimeSeconds < 0 {
		t.Errorf("PodUptimeSeconds = %d, want >= 0", policy.PodUptimeSeconds)
	}
}

// TestInspectForRecycleVMRestartAgainstApiserver verifies the §5.2 step 7 C3
// wiring against a real API server: the inspector resolves a
// recycle.scrubProfile: vm-restart pool to PodRecyclePolicy.VMRestart true so
// the recycle disposition retires the pod at the occupancy-zero boundary rather
// than reusing it without a fresh guest. The standard-profile pool resolves to
// VMRestart false. This pins F-5.2.32 at the tier that owns the apiserver read
// path.
// spec: 5.2 (vm-restart step 7 fresh-guest reprovision), 4.6.1 (recycle disposition), 6.2 (occupancy projection)
//
// diagnosis: a failure means the gateway's recycle pod read does not surface the
// vm-restart scrub profile against a real apiserver, so the disposition reuses a
// vm-restart pod after a clean scrub instead of retiring it, returning a
// persisted guest VM to the next tenant.
func TestInspectForRecycleVMRestartAgainstApiserver_spec_5_2(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		profile runtimestore.MicrovmScrubMode
		want    bool
	}{
		{"vm-restart", runtimestore.MicrovmScrubVMRestart, true},
		{"standard", runtimestore.MicrovmScrubStandard, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: testNS,
					Labels: map[string]string{
						warmpool.LabelPool:            "agents",
						warmpool.LabelHostSchedulable: "true",
					},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
			}
			c := newEnvtestClient(t, pod)
			seedRecyclingClaim(t, c, "pod-1")
			insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
				Client:    c,
				Namespace: testNS,
				Pools: fakePoolReader{pools: map[string]poolstore.Pool{
					"agents": recyclingPool("agents", "rt", isolation.ProfileMicrovm, &runtimestore.RecyclePolicy{
						Enabled: true, MaxSessionsPerPod: 50,
						ScrubProfile: tc.profile,
					}),
				}},
				Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{"rt": {Name: "rt"}}},
				Now:      func() time.Time { return now },
			})
			if err != nil {
				t.Fatalf("NewPodInspector: %v", err)
			}
			policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
			if err != nil || !found {
				t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
			}
			if policy.VMRestart != tc.want {
				t.Errorf("VMRestart = %v, want %v for scrubProfile %q", policy.VMRestart, tc.want, tc.profile)
			}
		})
	}
}

// deleteClaim removes the per-pod SandboxClaim, simulating a concurrent
// reclaim (a racing hold-expiry DELETE or the §4.6.1 orphan GC) that removes
// the claim while the agent Pod object survives.
func deleteClaim(t *testing.T, c client.Client, podID string) {
	t.Helper()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(podID), Namespace: testNS},
	}
	if err := c.Delete(context.Background(), claim); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete claim for %s: %v", podID, err)
	}
}

// newDispositionDriver builds a claim disposition driver against the envtest
// client with a fixed clock, the common construction the gone-claim tests
// share.
func newDispositionDriver(t *testing.T, c client.Client) leasecontrol.ClaimDispositionDriver {
	t.Helper()
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS,
		Now: func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	return d
}

// TestClaimDispositionRecycleNonPreConnectClaimGoneAgainstApiserver verifies a
// non-preConnect Recycle whose claim was concurrently reclaimed (deleted while
// the Pod survives) is a no-op rather than an error against a real API server:
// WriteReservedStatus's status-subresource SSA cannot create the vanished claim
// and returns NotFound, which the driver absorbs. spec: 3.4 (disposition skipped
// when the claim is gone), 4.6.1 (orphan-GC crash recovery)
//
// diagnosis: a failure means a ReportPodScrub racing a hold-expiry DELETE or
// the §4.6.1 orphan GC errors instead of skipping against a real apiserver, so
// the adapter sees a failed ReportPodScrub for a pod whose claim was reclaimed.
func TestClaimDispositionRecycleNonPreConnectClaimGoneAgainstApiserver_spec_3_4(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	seedRecyclingClaim(t, c, "pod-1")
	deleteClaim(t, c, "pod-1")
	d := newDispositionDriver(t, c)
	if err := d.Recycle(context.Background(), "pod-1", false, false); err != nil {
		t.Fatalf("Recycle with gone claim: err = %v, want nil (no-op)", err)
	}
}

// TestClaimDispositionRecyclePreConnectClaimGoneAgainstApiserver verifies a
// preConnect Recycle whose claim was concurrently reclaimed is a no-op against
// a real API server: WriteRewarmStartedStatus does a get-first and returns a
// wrapped NotFound, which the driver absorbs. spec: 6.2 (preConnect re-warm),
// 3.4 (skip when the claim is gone)
//
// diagnosis: a failure means a preConnect ReportPodScrub racing a concurrent
// reclaim errors instead of skipping, mapping to an Internal RPC error for a
// pod whose claim no longer exists.
func TestClaimDispositionRecyclePreConnectClaimGoneAgainstApiserver_spec_3_4(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	seedRecyclingClaim(t, c, "pod-1")
	deleteClaim(t, c, "pod-1")
	d := newDispositionDriver(t, c)
	if err := d.Recycle(context.Background(), "pod-1", true, false); err != nil {
		t.Fatalf("preConnect Recycle with gone claim: err = %v, want nil (no-op)", err)
	}
}

// TestClaimDispositionRetireClaimGoneAgainstApiserver verifies a Retire whose
// claim was concurrently reclaimed is a no-op against a real API server:
// WriteDispositionStatus's status SSA returns NotFound (a status apply cannot
// create the object), which the driver absorbs. spec: 3.4 (retire disposition
// skipped when the claim is gone), 4.6.1 (orphan-GC crash recovery)
//
// diagnosis: a failure means a retiring ReportPodScrub racing the orphan GC's
// reclaim of a recycling claim errors instead of skipping, so a coordinator
// crash that left a recycling claim for the GC surfaces as a failed report.
func TestClaimDispositionRetireClaimGoneAgainstApiserver_spec_3_4(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	seedRecyclingClaim(t, c, "pod-1")
	deleteClaim(t, c, "pod-1")
	d := newDispositionDriver(t, c)
	if err := d.Retire(context.Background(), "pod-1", true, false, "cleanup_fail_policy", "shred timed out"); err != nil {
		t.Fatalf("Retire with gone claim: err = %v, want nil (no-op)", err)
	}
}

// TestInspectForRecycleClaimGonePodPresentAgainstApiserver verifies the
// inspector reports found=false when the SandboxClaim is gone while the agent
// Pod object survives against a real API server, so the disposition is skipped
// before any counter advances. spec: 3.4 (skip when the claim is gone), 4.6.1
// (orphan GC reclaiming a recycling claim), 4.7 (concurrent-retirement no-op)
//
// diagnosis: a failure means the inspector's claim read does not work against a
// real apiserver, so it advances counters and runs the disposition against a
// claim the orphan GC or a hold-expiry DELETE already reclaimed.
func TestInspectForRecycleClaimGonePodPresentAgainstApiserver_spec_4_7(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: "agents"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	c := newEnvtestClient(t, pod)
	seedRecyclingClaim(t, c, "pod-1")
	deleteClaim(t, c, "pod-1")
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client:    c,
		Namespace: testNS,
		Pools: fakePoolReader{pools: map[string]poolstore.Pool{
			"agents": recyclingPool("agents", "rt", isolation.ProfileSandboxed, &runtimestore.RecyclePolicy{
				Enabled: true, MaxSessionsPerPod: 50,
			}),
		}},
		Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{"rt": {Name: "rt"}}},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	_, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil {
		t.Fatalf("InspectForRecycle with gone claim: err = %v, want nil", err)
	}
	if found {
		t.Error("InspectForRecycle found = true with a gone claim, want false (skip)")
	}
}

// agentPodWithContainer builds a schedulable agent Pod with the pool label.
// The envtest API server validates the Pod spec (a container is required) and
// drops any status set at create (there is no kubelet), so the Ready condition
// is written separately via setPodReady.
func agentPodWithContainer(name, pool string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
}

// setPodReady writes a Ready=True condition on the pod's status subresource so
// the §3.4 re-warm completion poll observes it as ready (envtest has no kubelet
// to flip readiness).
func setPodReady(t *testing.T, c client.Client, name string) {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if err := c.Status().Update(context.Background(), &pod); err != nil {
		t.Fatalf("set pod %s ready: %v", name, err)
	}
}

// boundaryPoolReader resolves a pool with the given cleanupTimeoutSeconds for
// the §3.4 missing-report timeout base.
func boundaryPoolReader(pool string, cleanupTimeoutSeconds int) fakePoolReader {
	return fakePoolReader{pools: map[string]poolstore.Pool{
		pool: {
			Name:          pool,
			RuntimeRef:    "rt",
			ExecutionMode: runtimestore.ExecutionModeSession,
			SessionPolicy: &runtimestore.SessionPolicy{
				CleanupTimeoutSeconds: cleanupTimeoutSeconds,
				Recycle:               &runtimestore.RecyclePolicy{Enabled: true, MaxSessionsPerPod: 50},
			},
		},
	}}
}

// TestRecycleBoundaryPreConnectReachesReserved drives a preConnect recycle all
// the way to the `reserved` claim binding state through the real
// RecycleBoundaryCoordinator against the envtest API server: the disposition
// driver stamps rewarmStartedAt, signals the coordinator, and the coordinator's
// re-warm completion poll patches the claim recycling → reserved once the agent
// pod reports Ready. This is the producer of the recycling → reserved patch that
// was previously absent on preConnect pools, so the reserved-hold optimization
// is functional.
// spec: 3.4 (preConnect re-warm completion drives recycling → reserved), 3.2
// (reserved hold), 6.2 / 6.14 (recycling → reserved binding edge)
//
// diagnosis: a failure means a preConnect recycling pod's claim is stranded in
// `recycling` after the SDK re-warm completes, never reaching `reserved`, so the
// pod is drained by the orphan GC instead of being held for its pinned tenant.
func TestRecycleBoundaryPreConnectReachesReserved_spec_3_4(t *testing.T) {
	c := newEnvtestClient(t, agentPodWithContainer("pod-1", "agents"))
	setPodReady(t, c, "pod-1")
	name := seedRecyclingClaim(t, c, "pod-1")
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	boundary, err := recycle.NewRecycleBoundaryCoordinator(recycle.RecycleBoundaryCoordinatorOptions{
		Client:       c,
		Namespace:    testNS,
		Pools:        boundaryPoolReader("agents", 60),
		HoldTTL:      10 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRecycleBoundaryCoordinator: %v", err)
	}
	defer boundary.Stop()

	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: c, Namespace: testNS, HoldTTL: 10 * time.Second,
		Now: func() time.Time { return now }, Boundary: boundary,
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	// preConnect recycle: stamps rewarmStartedAt and signals the coordinator.
	if err := d.Recycle(context.Background(), "pod-1", true, false); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	// The coordinator polls the pod readiness and reserves the claim.
	if !waitForClaimPhase(t, c, name, claimstate.Reserved) {
		got := getClaim(t, c, name)
		t.Fatalf("claim phase = %q, want reserved (re-warm completion patch)", got.Status.Phase)
	}
	got := getClaim(t, c, name)
	if got.Status.HoldExpiresAt == nil {
		t.Error("reserved claim carries no holdExpiresAt after re-warm completion")
	}
}

// TestRecycleBoundaryMissingReportRetires drives the §3.4 gateway-side
// missing-report timeout against the envtest API server: with the timeout armed
// at the bound → recycling patch and no ReportPodScrub arriving, the coordinator
// retires the pod (claim → `failed`) so it does not linger in `recycling` until
// the much longer orphan-GC window. The cleanupTimeoutSeconds is set to 0 so the
// effective delay is the grace alone, keeping the test fast.
// spec: 3.4 (missing-report timeout retires the pod), 4.7
//
// diagnosis: a failure means an adapter that never sends ReportPodScrub leaves
// the pod stuck in `recycling` (projecting claimed) on a still-running gateway
// until the 5-minute orphan GC rather than being retired at
// cleanupTimeoutSeconds plus a grace.
func TestRecycleBoundaryMissingReportRetires_spec_3_4(t *testing.T) {
	c := newEnvtestClient(t, agentPodWithContainer("pod-1", "agents"))
	name := seedRecyclingClaim(t, c, "pod-1")

	// A tiny GracePeriod and the smallest positive pool cleanupTimeoutSeconds
	// (1s) keep the real timer fast: the missing-report delay is ~1s, well
	// under the test deadline, exercising the production time.AfterFunc path.
	boundary, err := recycle.NewRecycleBoundaryCoordinator(recycle.RecycleBoundaryCoordinatorOptions{
		Client:      c,
		Namespace:   testNS,
		Pools:       boundaryPoolReader("agents", 1),
		GracePeriod: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRecycleBoundaryCoordinator: %v", err)
	}
	defer boundary.Stop()

	// No ReportPodScrub arrives, so the missing-report timeout fires and
	// retires the pod (claim → failed) rather than leaving it in `recycling`.
	boundary.OnRecycling("pod-1")
	if !waitForClaimPhase(t, c, name, claimstate.Failed) {
		got := getClaim(t, c, name)
		t.Fatalf("claim phase = %q, want failed (missing-report retire)", got.Status.Phase)
	}
}

// waitForClaimPhase polls the claim binding state up to a short deadline,
// returning true once it reaches want.
func waitForClaimPhase(t *testing.T, c client.Client, name string, want claimstate.State) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got lennyv1.SandboxClaim
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got); err == nil {
			if got.Status.Phase == string(want) {
				return true
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return false
}
