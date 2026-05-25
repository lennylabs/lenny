// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// concurrentSandbox builds a Sandbox in the test pool with the given
// phase, active-slot count, and tenant pin.
func concurrentSandbox(name, phase string, activeSlots int32, tenantID string) *lennyv1.Sandbox {
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{
			Phase:       phase,
			PodIP:       "10.0.0.1",
			ActiveSlots: activeSlots,
			TenantID:    tenantID,
		},
	}
	if tenantID != "" {
		sb.Labels[podclaim.LabelTenant] = tenantID
	}
	return sb
}

// newEnvtestClient boots an envtest API server, creates the test
// namespace, and seeds the supplied objects (Spec via Create, Status
// via SSA Apply under the `lenny-gateway` field manager — matching
// the production SlotClaimer's field manager so the test's pre-seed
// is on the same code path the gateway exercises at runtime).
func newEnvtestClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	env := envtest.Start(t)
	c, err := client.NewWithWatch(env.RESTConfig(), client.Options{Scheme: newScheme(t)})
	if err != nil {
		t.Fatalf("client.NewWithWatch: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", testNS, err)
	}
	for _, o := range objs {
		var (
			sbStatus  lennyv1.SandboxStatus
			seedAfter func()
		)
		if sb, ok := o.(*lennyv1.Sandbox); ok {
			sbStatus = sb.Status
			sb.Status = lennyv1.SandboxStatus{}
			seedAfter = func() {
				if sbStatus.Phase == "" && sbStatus.PodName == "" &&
					sbStatus.NodeName == "" && sbStatus.PodIP == "" &&
					sbStatus.TenantID == "" && sbStatus.ActiveSlots == 0 {
					return
				}
				patch := &lennyv1.Sandbox{
					TypeMeta: metav1.TypeMeta{
						APIVersion: lennyv1.GroupVersion.String(),
						Kind:       "Sandbox",
					},
					ObjectMeta: metav1.ObjectMeta{Name: sb.Name, Namespace: sb.Namespace},
				}
				patch.Status = sbStatus
				if err := c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.Gateway))); err != nil {
					t.Fatalf("seed status Sandbox %s: %v", sb.Name, err)
				}
			}
		}
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
		if seedAfter != nil {
			seedAfter()
		}
	}
	return c
}

func slotClaimerFor(t *testing.T, objs ...client.Object) (*podclaim.SlotClaimer, client.Client) {
	t.Helper()
	c := newEnvtestClient(t, objs...)
	return &podclaim.SlotClaimer{Client: c, Namespace: testNS}, c
}

func slotReq(session, tenant string, style podclaim.ConcurrencyStyle, max int32) podclaim.SlotRequest {
	return podclaim.SlotRequest{
		Pool: testPool, SessionID: session, TenantID: tenant,
		Style: style, MaxConcurrent: max,
	}
}

// spec: 5.2
// diagnosis: SlotClaimer.ClaimSlot did not open the first concurrent
// slot on an idle pod correctly. §5.2: the first slot on a pod
// transitions it idle → slot_active, sets activeSlots to 1, and pins
// the pod to the tenant.
func TestClaimSlotOpensFreshIdlePod(t *testing.T) {
	claimer, c := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", 0, ""))

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-1", "acme", podclaim.StyleWorkspace, 8))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-1" {
		t.Errorf("claimed %q, want sbx-1", res.SandboxName)
	}
	if !res.FreshPod {
		t.Error("first slot on an idle pod should report FreshPod=true")
	}
	if res.ActiveSlots != 1 {
		t.Errorf("ActiveSlots = %d, want 1 after the first slot", res.ActiveSlots)
	}
	if res.SlotID != "sess-1" {
		t.Errorf("SlotID = %q, want the session id", res.SlotID)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "slot_active" {
		t.Errorf("phase = %q, want slot_active", sb.Status.Phase)
	}
	if sb.Status.ActiveSlots != 1 {
		t.Errorf("status.activeSlots = %d, want 1", sb.Status.ActiveSlots)
	}
	if sb.Status.TenantID != "acme" {
		t.Errorf("status.tenantId = %q, want acme (the pod must be pinned)", sb.Status.TenantID)
	}
	if sb.Labels[podclaim.LabelTenant] != "acme" {
		t.Errorf("tenant label = %q, want acme", sb.Labels[podclaim.LabelTenant])
	}

	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: res.Claim.Name}, &stored); err != nil {
		t.Fatalf("the SandboxClaim was not persisted: %v", err)
	}
	if stored.Spec.SlotID != "sess-1" {
		t.Errorf("claim SlotID = %q, want sess-1", stored.Spec.SlotID)
	}
}

// spec: 5.2
// diagnosis: a second concurrent slot did not land on the same pod. The
// §5.2 concurrent-slot assignment rule places further slots on a pod
// already hosting slots while activeSlots < maxConcurrent — sharing the
// pod is the point of concurrent mode — before claiming a fresh pod.
func TestClaimSlotSecondSlotLandsOnSamePodUpToTheBound(t *testing.T) {
	// One pod already hosts a slot for acme; one spare idle pod exists.
	claimer, c := slotClaimerFor(
		t,
		concurrentSandbox("sbx-busy", "slot_active", 1, "acme"),
		concurrentSandbox("sbx-spare", "idle", 0, ""),
	)

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-2", "acme", podclaim.StyleWorkspace, 3))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-busy" {
		t.Errorf("second slot landed on %q, want sbx-busy (the pod already hosting slots)", res.SandboxName)
	}
	if res.FreshPod {
		t.Error("a slot joining an occupied pod must report FreshPod=false")
	}
	if res.ActiveSlots != 2 {
		t.Errorf("ActiveSlots = %d, want 2", res.ActiveSlots)
	}

	// The spare pod must be untouched: concurrent mode fills a pod's
	// slots before claiming a fresh one.
	var spare lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-spare"}, &spare); err != nil {
		t.Fatalf("get spare: %v", err)
	}
	if spare.Status.Phase != "idle" || spare.Status.ActiveSlots != 0 {
		t.Errorf("spare pod was disturbed: phase=%q slots=%d", spare.Status.Phase, spare.Status.ActiveSlots)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot overran the maxConcurrent bound. §5.2: a pod
// hosts at most maxConcurrent slots; the (maxConcurrent+1)-th slot
// request claims a fresh warm pod instead of overrunning the bound.
func TestClaimSlotClaimsFreshPodWhenBoundReached(t *testing.T) {
	// sbx-full is at the maxConcurrent=2 bound; sbx-fresh is idle.
	claimer, c := slotClaimerFor(
		t,
		concurrentSandbox("sbx-full", "slot_active", 2, "acme"),
		concurrentSandbox("sbx-fresh", "idle", 0, ""),
	)

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-3", "acme", podclaim.StyleWorkspace, 2))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-fresh" {
		t.Errorf("slot landed on %q, want sbx-fresh; the full pod must not be overrun", res.SandboxName)
	}
	if !res.FreshPod || res.ActiveSlots != 1 {
		t.Errorf("a slot on a fresh pod should report FreshPod=true, ActiveSlots=1; got %+v", res)
	}

	// sbx-full stays exactly at the bound.
	var full lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-full"}, &full); err != nil {
		t.Fatalf("get full pod: %v", err)
	}
	if full.Status.ActiveSlots != 2 {
		t.Errorf("full pod activeSlots = %d, want 2 (unchanged)", full.Status.ActiveSlots)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot did not return ErrNoConcurrentSlot when every
// pod is at its bound and no idle pod remains. §5.2 maps this to
// WARM_POOL_EXHAUSTED / "concurrent_slots_exhausted".
func TestClaimSlotExhaustedWhenAllPodsFull(t *testing.T) {
	claimer, _ := slotClaimerFor(
		t,
		concurrentSandbox("sbx-a", "slot_active", 4, "acme"),
		concurrentSandbox("sbx-b", "slot_active", 4, "acme"),
	)
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-x", "acme", podclaim.StyleWorkspace, 4))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Errorf("error = %v, want ErrNoConcurrentSlot when all pods are at the bound", err)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot placed a slot for one tenant on a pod pinned to
// another. §5.2 tenant pinning: a concurrent-mode pod is bound to its
// first tenant for its lifetime; a slot for a different tenant must
// never join it. With no fresh pod available the claim reports the
// tenant-mismatch distinctly.
func TestClaimSlotRejectsCrossTenantSlotSharing(t *testing.T) {
	// The only pod with free capacity is pinned to globex; no idle pod.
	claimer, c := slotClaimerFor(
		t,
		concurrentSandbox("sbx-globex", "slot_active", 1, "globex"),
	)
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-acme", "acme", podclaim.StyleWorkspace, 8))
	if !errors.Is(err, podclaim.ErrTenantMismatch) {
		t.Errorf("error = %v, want ErrTenantMismatch; a slot must not join another tenant's pod", err)
	}

	// The globex pod's slot count must be unchanged — no slot was opened.
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-globex"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.ActiveSlots != 1 {
		t.Errorf("globex pod activeSlots = %d, want 1 (unchanged)", sb.Status.ActiveSlots)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot did not fall through to a fresh pod when the
// only occupied pod is pinned to a different tenant. §5.2: a slot for a
// new tenant claims a fresh idle pod rather than joining a foreign
// tenant's pod.
func TestClaimSlotFallsThroughToFreshPodOnTenantMismatch(t *testing.T) {
	claimer, _ := slotClaimerFor(
		t,
		concurrentSandbox("sbx-globex", "slot_active", 1, "globex"),
		concurrentSandbox("sbx-idle", "idle", 0, ""),
	)
	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-acme", "acme", podclaim.StyleWorkspace, 8))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-idle" {
		t.Errorf("slot landed on %q, want sbx-idle; acme must get a fresh pod", res.SandboxName)
	}
	if !res.FreshPod {
		t.Error("the slot should have opened a fresh pod")
	}
}

// spec: 5.2
// diagnosis: stateless-concurrent slot assignment did not behave like
// workspace-concurrent at the claim layer. §5.2: both concurrency
// styles share the per-pod slot bound and the tenant-pinning rule; only
// the workspace materialization differs (handled by the binder).
func TestClaimSlotStatelessStyleHonorsTheBound(t *testing.T) {
	claimer, _ := slotClaimerFor(
		t,
		concurrentSandbox("sbx-1", "slot_active", 1, "acme"),
	)
	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-2", "acme", podclaim.StyleStateless, 4))
	if err != nil {
		t.Fatalf("ClaimSlot stateless: %v", err)
	}
	if res.SandboxName != "sbx-1" || res.ActiveSlots != 2 {
		t.Errorf("stateless slot = %+v, want a second slot on sbx-1", res)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot accepted an invalid concurrent-mode request.
// §5.2: a concurrent-mode claim requires a valid concurrency style and
// maxConcurrent >= 1.
func TestClaimSlotRejectsInvalidRequests(t *testing.T) {
	claimer, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", 0, ""))
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("s", "acme", "bogus", 4)); err == nil {
		t.Error("an invalid concurrency style should be rejected")
	}
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("s", "acme", podclaim.StyleWorkspace, 0)); err == nil {
		t.Error("maxConcurrent=0 should be rejected")
	}
}

// spec: 5.2
// diagnosis: the §5.2 atomic slot-reservation guarantee was not upheld
// under a competing gateway replica. A status-apply conflict on a pod
// (a competing replica reserved a slot there first) must make ClaimSlot
// re-evaluate or move on rather than overrun the maxConcurrent bound.
//
// The test wraps the envtest client with an interceptor that returns
// a 409 conflict on the first SSA Apply targeting sbx-a, forcing the
// claimer to fall through to sbx-b. Subsequent applies pass through
// to the real apiserver.
func TestClaimSlotRetriesOnReservationConflict(t *testing.T) {
	envClient := newEnvtestClient(t,
		concurrentSandbox("sbx-a", "idle", 0, ""),
		concurrentSandbox("sbx-b", "idle", 0, ""),
	)
	conflicted := false
	c := interceptor.NewClient(envClient, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sr string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if obj.GetName() == "sbx-a" && !conflicted {
				conflicted = true
				return apierrors.NewConflict(
					schema.GroupResource{Group: "lenny.dev", Resource: "sandboxes"},
					"sbx-a", errors.New("slot reserved by a competing replica"),
				)
			}
			return cl.Status().Patch(ctx, obj, patch, opts...)
		},
	})
	claimer := &podclaim.SlotClaimer{Client: c, Namespace: testNS}

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-1", "acme", podclaim.StyleWorkspace, 4))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName == "sbx-a" {
		t.Error("ClaimSlot opened sbx-a despite the conflicting status apply")
	}
	if !conflicted {
		t.Error("the conflicting-apply path was not exercised")
	}
}

// spec: 5.2
// diagnosis: ReleaseSlot did not decrement a pod's slot count or did
// not return the pod to idle when its last slot drained. §6.2: a
// concurrent-mode pod returns to idle only when activeSlots reaches 0;
// while sibling slots remain it stays slot_active.
func TestReleaseSlotDecrementsAndReturnsToIdleOnLastSlot(t *testing.T) {
	claimer, c := slotClaimerFor(t, concurrentSandbox("sbx-1", "slot_active", 2, "acme"))
	// A claim object for the slot being released.
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sess-1", Namespace: testNS},
		Spec: lennyv1.SandboxClaimSpec{
			SandboxRef: "sbx-1", SessionID: "sess-1", TenantID: "acme", SlotID: "sess-1",
		},
	}
	if err := c.Create(context.Background(), claim); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	// Release one of the two slots: the pod stays slot_active.
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-1"); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.ActiveSlots != 1 {
		t.Errorf("after one release activeSlots = %d, want 1", sb.Status.ActiveSlots)
	}
	if sb.Status.Phase != "slot_active" {
		t.Errorf("phase = %q, want slot_active while a sibling slot remains", sb.Status.Phase)
	}
	if got := apierrors.IsNotFound(c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: "claim-sess-1"}, &lennyv1.SandboxClaim{})); !got {
		t.Error("the slot's SandboxClaim should have been deleted")
	}

	// Release the last slot: the pod returns to idle.
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-2"); err != nil {
		t.Fatalf("ReleaseSlot last: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.ActiveSlots != 0 {
		t.Errorf("after the last release activeSlots = %d, want 0", sb.Status.ActiveSlots)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("phase = %q, want idle after the last slot drained", sb.Status.Phase)
	}
	// §5.2 tenant pinning persists: an idle-again concurrent pod keeps
	// its pin.
	if sb.Status.TenantID != "acme" {
		t.Errorf("status.tenantId = %q, want acme; the tenant pin must survive an idle-again pod", sb.Status.TenantID)
	}
}

// spec: 5.2
// diagnosis: a double ReleaseSlot drove the slot count negative.
// ReleaseSlot must clamp the decrement at 0 so a repeated release is a
// no-op.
func TestReleaseSlotIsIdempotentAtZero(t *testing.T) {
	claimer, c := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", 0, "acme"))
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-gone"); err != nil {
		t.Fatalf("ReleaseSlot on a zero-slot pod: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.ActiveSlots != 0 {
		t.Errorf("activeSlots = %d, want 0; release must not drive the count negative", sb.Status.ActiveSlots)
	}
}

// seedWPCSandboxStatus writes the WPC-owned subset of
// Sandbox.status (phase, podName, nodeName, podIP) under the
// WarmPoolController field manager. Tests that need to verify
// cross-manager Apply behavior (gateway forcing Phase ownership,
// gateway's unstructured patch leaving WPC-owned fields intact)
// must seed those fields under WPC, not Gateway — otherwise the
// test would pre-claim ownership for the gateway and fail to
// exercise the spec-§4.6.3 dual-ownership tension that the slot-
// claim path resolves with ForceOwnership.
func seedWPCSandboxStatus(t *testing.T, c client.Client, name, phase, podName, nodeName, podIP string) {
	t.Helper()
	wpc := map[string]interface{}{}
	if phase != "" {
		wpc["phase"] = phase
	}
	if podName != "" {
		wpc["podName"] = podName
	}
	if nodeName != "" {
		wpc["nodeName"] = nodeName
	}
	if podIP != "" {
		wpc["podIP"] = podIP
	}
	if len(wpc) == 0 {
		return
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(testNS)
	_ = unstructured.SetNestedField(u.Object, wpc, "status")
	if err := c.Status().Patch(context.Background(), u, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC status %s: %v", name, err)
	}
}

// TestClaimSlotForcesPhaseFromWPCOwnership is the regression for the
// §4.6.3-vs-§5.2 cross-manager Phase write that the spec-aligned
// gateway slot-claim resolves with client.ForceOwnership. The seed
// puts Sandbox.status.phase under the WarmPoolController field
// manager (§4.6.3 owner). The gateway's slot-reservation Apply must
// still transition phase to slot_active per §5.2, which only
// succeeds when ForceOwnership is on the Apply call. Without it,
// the API server returns a 409 Conflict on the Phase field and the
// slot-claim fails — exactly the regression that pinned EKS
// sandboxes in `claimed` instead of `slot_active`.
func TestClaimSlotForcesPhaseFromWPCOwnership(t *testing.T) {
	claimer, c := slotClaimerFor(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sbx-wpc-owned",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
	})
	// Re-seed phase=idle under the WPC field manager so the
	// gateway's slot-claim Apply must force ownership across managers.
	seedWPCSandboxStatus(t, c, "sbx-wpc-owned", string(state.Idle), "", "", "10.0.0.7")

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-force", "acme", podclaim.StyleStateless, 4))
	if err != nil {
		t.Fatalf("ClaimSlot under WPC-owned phase: %v", err)
	}
	if res.SandboxName != "sbx-wpc-owned" {
		t.Errorf("claimed %q, want sbx-wpc-owned", res.SandboxName)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-wpc-owned"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.SlotActive) {
		t.Errorf("phase = %q, want slot_active — the gateway's Phase Apply must force across the §4.6.3 WPC ownership (regression: 409 Conflict left it idle)", sb.Status.Phase)
	}
	if sb.Status.ActiveSlots != 1 {
		t.Errorf("activeSlots = %d, want 1", sb.Status.ActiveSlots)
	}
}

// TestClaimSlotPreservesWPCOwnedFieldsAcrossGatewayApply is the
// regression for the SSA Go-zero-value clobbering bug. The gateway
// slot-claim patch is built as *unstructured.Unstructured carrying
// only gateway-owned fields (phase, activeSlots, tenantId).
// A typed-struct patch would serialize unset WPC-owned fields
// (PodName, NodeName, PodIP, ObservedGeneration) as Go zero values
// and the SSA apply would claim those fields and wipe WPC's writes.
// This test seeds full WPC-owned state, runs the gateway's Apply,
// and asserts every WPC-owned field still holds the seeded value.
func TestClaimSlotPreservesWPCOwnedFieldsAcrossGatewayApply(t *testing.T) {
	claimer, c := slotClaimerFor(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sbx-keep-wpc",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
	})
	seedWPCSandboxStatus(t, c, "sbx-keep-wpc",
		string(state.Idle), "pod-keep-wpc", "node-keep-wpc", "10.0.0.42")

	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-keep", "acme", podclaim.StyleStateless, 4)); err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-keep-wpc"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.SlotActive) {
		t.Errorf("phase = %q, want slot_active — the gateway must still transition it", sb.Status.Phase)
	}
	if sb.Status.PodName != "pod-keep-wpc" {
		t.Errorf("podName = %q, want pod-keep-wpc — regression: gateway Apply zeroed a WPC-owned field", sb.Status.PodName)
	}
	if sb.Status.NodeName != "node-keep-wpc" {
		t.Errorf("nodeName = %q, want node-keep-wpc — regression: gateway Apply zeroed a WPC-owned field", sb.Status.NodeName)
	}
	if sb.Status.PodIP != "10.0.0.42" {
		t.Errorf("podIP = %q, want 10.0.0.42 — regression: gateway Apply zeroed a WPC-owned field", sb.Status.PodIP)
	}
}

// TestReleaseSlotPreservesWPCOwnedFieldsOnReturnToIdle is the
// release-side regression for the same Go-zero-value clobbering bug.
// When a concurrent-mode pod returns to idle on its last slot
// release, the gateway's Apply must transition phase back to idle
// without touching the WPC-owned podName / nodeName / podIP fields.
// A typed-struct patch would zero them.
func TestReleaseSlotPreservesWPCOwnedFieldsOnReturnToIdle(t *testing.T) {
	claimer, c := slotClaimerFor(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sbx-release-keep",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
	})
	seedWPCSandboxStatus(t, c, "sbx-release-keep",
		string(state.Idle), "pod-release", "node-release", "10.0.0.99")

	// Claim a single slot to set up the slot_active state, then
	// release it: the release path is what transitions back to idle.
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-release", "acme", podclaim.StyleStateless, 4)); err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if err := claimer.ReleaseSlot(context.Background(), "sbx-release-keep", "sess-release"); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-release-keep"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Idle) {
		t.Errorf("phase = %q, want idle after the last slot drains", sb.Status.Phase)
	}
	if sb.Status.PodName != "pod-release" || sb.Status.NodeName != "node-release" || sb.Status.PodIP != "10.0.0.99" {
		t.Errorf("WPC-owned fields after release = %+v, want podName=pod-release nodeName=node-release podIP=10.0.0.99 — regression: gateway Apply on return-to-idle zeroed WPC fields",
			sb.Status)
	}
}

// spec: 5.2
// diagnosis: a repeated slot claim for the same session opened a
// duplicate slot. The SandboxClaim name is deterministic per session,
// so a second claim for a live session must collide at CREATE.
func TestClaimSlotRepeatedSessionCollides(t *testing.T) {
	claimer, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", 0, ""))
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-dup", "acme", podclaim.StyleWorkspace, 8)); err != nil {
		t.Fatalf("first ClaimSlot: %v", err)
	}
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-dup", "acme", podclaim.StyleWorkspace, 8)); err == nil {
		t.Error("a repeated slot claim for the same session should collide at CREATE")
	}
}

// spec: §5.2 line 519
// diagnosis: ClaimSlot conflated "no pods at all" with "pods exist but
// full" — both returned ErrNoConcurrentSlot. §5.2 line 519 distinguishes
// the cause via details.reason: an empty pool is "no_idle_pods", mapped
// from ErrNoIdlePod (the same sentinel session-mode exhaustion uses).
func TestClaimSlotEmptyPoolReturnsErrNoIdlePod(t *testing.T) {
	claimer, _ := slotClaimerFor(t) // no pods in the pool at all
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-x", "acme", podclaim.StyleWorkspace, 4))
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when the pool holds no pods (§5.2 no_idle_pods)", err)
	}
	if errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Error("an empty pool must not report ErrNoConcurrentSlot (that is the pods-exist-but-full case)")
	}
}

// spec: §5.2 line 519
// diagnosis: lenny_slot_assignment_conflict_total had no emitter. The
// SlotClaimer must record a slot-contention conflict via OnSlotConflict
// when the atomic counter finds a candidate pod at its maxConcurrent
// bound. This reproduces the production race: the Sandbox status mirror
// lags below the cap while the authoritative Redis counter is already
// full, so pass-1 reaches reserveSlot and the counter rejects.
func TestClaimSlotRecordsConflictOnSlotContention(t *testing.T) {
	c := newEnvtestClient(t, concurrentSandbox("sbx-a", "slot_active", 1, "acme"))

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	counter := slotcounter.New(rc)
	ctx := context.Background()
	// Drive the authoritative counter for sbx-a up to the cap (2) while
	// the Sandbox status mirror still reads activeSlots=1.
	if _, err := counter.Reserve(ctx, "sbx-a", 2); err != nil {
		t.Fatalf("seed counter (1): %v", err)
	}
	if _, err := counter.Reserve(ctx, "sbx-a", 2); err != nil {
		t.Fatalf("seed counter (2): %v", err)
	}

	var conflicts []string
	claimer := &podclaim.SlotClaimer{
		Client:         c,
		Namespace:      testNS,
		Counter:        counter,
		OnSlotConflict: func(pool string) { conflicts = append(conflicts, pool) },
	}

	_, err := claimer.ClaimSlot(ctx, slotReq("sess-x", "acme", podclaim.StyleWorkspace, 2))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Errorf("error = %v, want ErrNoConcurrentSlot (pods exist but full)", err)
	}
	if len(conflicts) != 1 || conflicts[0] != testPool {
		t.Errorf("OnSlotConflict calls = %v, want one call for pool %q", conflicts, testPool)
	}
}
