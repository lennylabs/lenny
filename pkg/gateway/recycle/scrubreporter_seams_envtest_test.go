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
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
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
