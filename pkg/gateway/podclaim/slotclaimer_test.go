// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// newEnvtestClient boots an envtest API server, creates the test namespace,
// and seeds the supplied objects (spec via Create, Sandbox status via SSA
// Apply under the lenny-gateway field manager so the seed lands on the same
// status code path a controller would use). The gateway no longer writes
// Sandbox.status at runtime; the seed pre-stages the WPC-owned phase the
// idle-pod scan reads.
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
					sbStatus.TenantID == "" {
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
				// The WarmPoolController owns Sandbox.status; seed under its
				// field manager so a test asserting the gateway never writes
				// Sandbox.status is not contaminated by the seed itself.
				if err := c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
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

// concurrentSandbox builds a Sandbox in the test pool with the given phase
// and tenant pin. Occupancy is no longer a Sandbox.status field: the per-pod
// SandboxClaim (claim-<podName>) is the occupancy authority and the Redis
// counter is the intra-pod capacity gate, so a pod's slots are set up with
// seedOccupiedPod rather than a status field.
func concurrentSandbox(name, phase, tenantID string) *lennyv1.Sandbox {
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{
			Phase:    phase,
			PodIP:    "10.0.0.1",
			TenantID: tenantID,
		},
	}
	if tenantID != "" {
		sb.Labels[podclaim.LabelTenant] = tenantID
	}
	return sb
}

// seedOccupiedPod establishes a pod already hosting n slots for tenantID:
// it creates the per-pod SandboxClaim (claim-<podName>) with binding state
// `bound` and drives the Redis counter for the pod up to n. This is the
// per-pod-claim equivalent of the former per-session claim seeding.
func seedOccupiedPod(t *testing.T, c client.Client, counter *slotcounter.Counter, sandboxName, tenantID string, n int32) {
	t.Helper()
	ctx := context.Background()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + sandboxName, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxName, TenantID: tenantID},
	}
	if err := c.Create(ctx, claim); err != nil {
		t.Fatalf("seed per-pod claim for %s: %v", sandboxName, err)
	}
	if err := podclaim.WriteBoundStatus(ctx, c, testNS, claim.Name); err != nil {
		t.Fatalf("seed bound status for %s: %v", sandboxName, err)
	}
	for i := int32(0); i < n; i++ {
		if _, _, err := counter.Reserve(ctx, sandboxName, n+1); err != nil {
			t.Fatalf("seed counter %d on %s: %v", i, sandboxName, err)
		}
	}
}

// podClaimExists reports whether a per-pod SandboxClaim (claim-<podName>)
// exists for sandboxName.
func podClaimExists(t *testing.T, c client.Client, sandboxName string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-" + sandboxName}, &lennyv1.SandboxClaim{})
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get per-pod claim for %s: %v", sandboxName, err)
	return false
}

// assertNotGatewayStatusOwned fails if the gateway field manager owns any
// part of the Sandbox status subresource. Per §4.6.3 the gateway does not
// write Sandbox.status (the WarmPoolController projects the pod phase from
// the claim binding state); the seed in newEnvtestClient writes status under
// the WarmPoolController field manager, so a lenny-gateway status entry can
// only come from a gateway-side write the redesign removed.
func assertNotGatewayStatusOwned(t *testing.T, c client.Client, sandboxName string) {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: sandboxName}, &sb); err != nil {
		t.Fatalf("get sandbox %s: %v", sandboxName, err)
	}
	for _, mf := range sb.ManagedFields {
		if mf.Manager == string(ownership.Gateway) && mf.Subresource == "status" {
			t.Errorf("gateway must not write Sandbox.status (§4.6.3); found managedFields entry %+v on %s", mf, sandboxName)
		}
	}
}

// newCounter wires a miniredis-backed slot counter for the slot-claim
// tests. The Redis counter (with its §12.4 Postgres fallback) is the
// intra-pod capacity gate, so every SlotClaimer test runs with one.
func newCounter(t *testing.T) *slotcounter.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	return slotcounter.New(rc)
}

func slotClaimerFor(t *testing.T, objs ...client.Object) (*podclaim.SlotClaimer, client.Client, *slotcounter.Counter) {
	t.Helper()
	c := newEnvtestClient(t, objs...)
	counter := newCounter(t)
	return &podclaim.SlotClaimer{Client: c, Namespace: testNS, Counter: counter}, c, counter
}

func slotReq(session, tenant string, maxConcurrentSessions int32) podclaim.SlotRequest {
	return podclaim.SlotRequest{
		Pool: testPool, SessionID: session, TenantID: tenant,
		MaxConcurrentSessions: maxConcurrentSessions,
	}
}

// spec: 5.2
// diagnosis: SlotClaimer.ClaimSlot did not acquire a fresh idle pod for
// the first concurrent slot. §5.2 / §4.6.1: the first slot on a pod CREATEs
// the per-pod claim (claim-<podName>), writes its first `bound` binding
// state, reserves the first Redis slot, and pins the pod to the tenant.
func TestClaimSlotOpensFreshIdlePod(t *testing.T) {
	claimer, c, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", ""))

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-1", "acme", 8))
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

	// The per-pod claim (§4.6.3) carries sandboxRef and tenantId and its
	// first binding state is `bound`.
	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: res.Claim.Name}, &stored); err != nil {
		t.Fatalf("the per-pod SandboxClaim was not persisted: %v", err)
	}
	if stored.Name != "claim-sbx-1" {
		t.Errorf("claim name = %q, want claim-sbx-1 (per-pod deterministic name)", stored.Name)
	}
	if stored.Spec.SandboxRef != "sbx-1" || stored.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want sandboxRef=sbx-1 tenantId=acme", stored.Spec)
	}
	if stored.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound (first status patch)", stored.Status.Phase)
	}
	// The tenant pin is carried on the claim's tenantId; the pod-label pin
	// (the §13.2 NET-003 NetworkPolicy selector) is covered by the
	// tenant-label tests, which create a backing agent pod. The gateway no
	// longer writes Sandbox.status, so no Sandbox-status assertion applies.
}

// spec: 5.2
// diagnosis: a second concurrent slot did not land on the same pod. §5.2:
// further slots multiplex onto a pod already hosting slots for the tenant
// (its per-pod claim exists) while the Redis counter is below the bound,
// sharing the pod rather than acquiring a fresh one.
func TestClaimSlotSecondSlotLandsOnSamePodUpToTheBound(t *testing.T) {
	claimer, c, counter := slotClaimerFor(
		t,
		concurrentSandbox("sbx-busy", "claimed", "acme"),
		concurrentSandbox("sbx-spare", "idle", ""),
	)
	seedOccupiedPod(t, c, counter, "sbx-busy", "acme", 1)

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-2", "acme", 3))
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

	// The spare pod must be untouched: concurrent mode fills a pod's slots
	// before acquiring a fresh one.
	if podClaimExists(t, c, "sbx-spare") {
		t.Error("the spare idle pod must not be acquired while sbx-busy has free capacity")
	}
}

// spec: 5.2
// diagnosis: ClaimSlot overran the maxConcurrentSessions bound. §5.2: a pod
// hosts at most maxConcurrentSessions slots; the (bound+1)-th slot request
// acquires a fresh warm pod instead of overrunning the bound.
func TestClaimSlotClaimsFreshPodWhenBoundReached(t *testing.T) {
	claimer, c, counter := slotClaimerFor(
		t,
		concurrentSandbox("sbx-full", "claimed", "acme"),
		concurrentSandbox("sbx-fresh", "idle", ""),
	)
	seedOccupiedPod(t, c, counter, "sbx-full", "acme", 2)

	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-3", "acme", 2))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-fresh" {
		t.Errorf("slot landed on %q, want sbx-fresh; the full pod must not be overrun", res.SandboxName)
	}
	if !res.FreshPod || res.ActiveSlots != 1 {
		t.Errorf("a slot on a fresh pod should report FreshPod=true, ActiveSlots=1; got %+v", res)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot did not return ErrNoConcurrentSlot when every pod is
// at its bound and no idle pod remains. §5.2 maps this to
// WARM_POOL_EXHAUSTED / "concurrent_slots_exhausted".
func TestClaimSlotExhaustedWhenAllPodsFull(t *testing.T) {
	claimer, c, counter := slotClaimerFor(
		t,
		concurrentSandbox("sbx-a", "claimed", "acme"),
		concurrentSandbox("sbx-b", "claimed", "acme"),
	)
	seedOccupiedPod(t, c, counter, "sbx-a", "acme", 4)
	seedOccupiedPod(t, c, counter, "sbx-b", "acme", 4)
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-x", "acme", 4))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Errorf("error = %v, want ErrNoConcurrentSlot when all pods are at the bound", err)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot placed a slot for one tenant on a pod pinned to
// another. §5.2 tenant pinning: a concurrent-session pod is bound to its
// first tenant for its lifetime; a slot for a different tenant must never
// join it. With no fresh pod available the claim reports the tenant
// mismatch distinctly.
func TestClaimSlotRejectsCrossTenantSlotSharing(t *testing.T) {
	claimer, c, counter := slotClaimerFor(
		t,
		concurrentSandbox("sbx-globex", "claimed", "globex"),
	)
	seedOccupiedPod(t, c, counter, "sbx-globex", "globex", 1)
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-acme", "acme", 8))
	if !errors.Is(err, podclaim.ErrTenantMismatch) {
		t.Errorf("error = %v, want ErrTenantMismatch; a slot must not join another tenant's pod", err)
	}
}

// spec: 5.2
// diagnosis: ClaimSlot did not fall through to a fresh pod when the only
// occupied pod is pinned to a different tenant. §5.2: a slot for a new
// tenant acquires a fresh idle pod rather than joining a foreign tenant's
// pod.
func TestClaimSlotFallsThroughToFreshPodOnTenantMismatch(t *testing.T) {
	claimer, c, counter := slotClaimerFor(
		t,
		concurrentSandbox("sbx-globex", "claimed", "globex"),
		concurrentSandbox("sbx-idle", "idle", ""),
	)
	seedOccupiedPod(t, c, counter, "sbx-globex", "globex", 1)
	res, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-acme", "acme", 8))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-idle" {
		t.Errorf("slot landed on %q, want sbx-idle; acme must get a fresh pod", res.SandboxName)
	}
	if !res.FreshPod {
		t.Error("the slot should have acquired a fresh pod")
	}
}

// spec: 5.2
// diagnosis: ClaimSlot accepted a concurrent-session request with an
// invalid per-pod bound. §5.2: a concurrent-session claim requires
// maxConcurrentSessions >= 1.
func TestClaimSlotRejectsInvalidRequests(t *testing.T) {
	claimer, _, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", ""))
	if _, err := claimer.ClaimSlot(context.Background(),
		slotReq("s", "acme", 0)); err == nil {
		t.Error("maxConcurrentSessions=0 should be rejected")
	}
}

// spec: 5.2
// diagnosis: ClaimSlot ran without an intra-pod capacity gate. The Redis
// counter (with its §12.4 Postgres fallback) is the only intra-pod gate now
// that the gateway does not mirror the slot count onto Sandbox.status; a nil
// Counter must fail closed rather than overrun the bound.
func TestClaimSlotRequiresCounter(t *testing.T) {
	c := newEnvtestClient(t, concurrentSandbox("sbx-1", "idle", ""))
	claimer := &podclaim.SlotClaimer{Client: c, Namespace: testNS}
	if _, err := claimer.ClaimSlot(context.Background(), slotReq("s", "acme", 4)); err == nil {
		t.Error("ClaimSlot with no Counter must fail closed")
	}
}

// spec: §4.6.1, §5.2
// diagnosis: a concurrent acquisition of the same idle pod opened two
// per-pod claims. The deterministic claim-<podName> CREATE is the
// acquisition guard: when two replicas race for the same idle pod, the
// loser's CREATE collides on AlreadyExists, its counter reservation is
// undone, and it retries on the next pod.
func TestClaimSlotConcurrentAcquisitionCollidesOnPerPodName(t *testing.T) {
	c := newEnvtestClient(
		t,
		concurrentSandbox("sbx-1", "idle", ""),
		concurrentSandbox("sbx-2", "idle", ""),
	)
	ctx := context.Background()
	// A competing replica already acquired sbx-1: its per-pod claim exists.
	seed := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sbx-1", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "globex"},
	}
	if err := c.Create(ctx, seed); err != nil {
		t.Fatalf("seed competing claim: %v", err)
	}
	if err := podclaim.WriteBoundStatus(ctx, c, testNS, seed.Name); err != nil {
		t.Fatalf("seed competing claim status: %v", err)
	}

	claimer := &podclaim.SlotClaimer{Client: c, Namespace: testNS, Counter: newCounter(t)}
	res, err := claimer.ClaimSlot(ctx, slotReq("sess-1", "acme", 4))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	// acme is a different tenant, so sbx-1 (pinned to globex) is skipped and
	// the fresh acquisition lands on sbx-2.
	if res.SandboxName != "sbx-2" {
		t.Errorf("acquired %q, want sbx-2 (sbx-1 is claimed by globex)", res.SandboxName)
	}
}

// spec: 5.2
// diagnosis: ReleaseSlot did not decrement a pod's Redis counter or did not
// delete the per-pod claim when the last slot drained. §5.2 / §4.6.1: the
// per-pod claim spans the whole occupancy episode; it is deleted only when
// the counter reaches zero, returning the pod to the pool.
func TestReleaseSlotDecrementsAndDeletesClaimOnLastSlot(t *testing.T) {
	claimer, c, counter := slotClaimerFor(t, concurrentSandbox("sbx-1", "claimed", "acme"))
	seedOccupiedPod(t, c, counter, "sbx-1", "acme", 2)

	// Release one of the two slots: the per-pod claim stays.
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-1"); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("the per-pod claim must remain while a sibling slot is held")
	}

	// Release the last slot: the per-pod claim is deleted.
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-2"); err != nil {
		t.Fatalf("ReleaseSlot last: %v", err)
	}
	if podClaimExists(t, c, "sbx-1") {
		t.Error("the per-pod claim must be deleted when the last slot drains")
	}
}

// spec: 5.2
// diagnosis: a double ReleaseSlot drove the slot counter negative.
// ReleaseSlot must clamp the decrement at zero so a repeated release is a
// no-op that touches no claim.
func TestReleaseSlotIsIdempotentAtZero(t *testing.T) {
	claimer, c, _ := slotClaimerFor(t, concurrentSandbox("sbx-1", "idle", "acme"))
	if err := claimer.ReleaseSlot(context.Background(), "sbx-1", "sess-gone"); err != nil {
		t.Fatalf("ReleaseSlot on a zero-slot pod: %v", err)
	}
	if podClaimExists(t, c, "sbx-1") {
		t.Error("release on a zero-slot pod must not create a claim")
	}
}

// spec: §5.2 line 519
// diagnosis: ClaimSlot conflated "no pods at all" with "pods exist but
// full" — both returned ErrNoConcurrentSlot. §5.2 line 519 distinguishes
// the cause via details.reason: an empty pool is "no_idle_pods", mapped
// from ErrNoIdlePod (the same sentinel session-mode exhaustion uses).
func TestClaimSlotEmptyPoolReturnsErrNoIdlePod(t *testing.T) {
	claimer, _, _ := slotClaimerFor(t) // no pods in the pool at all
	_, err := claimer.ClaimSlot(context.Background(),
		slotReq("sess-x", "acme", 4))
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
// when the atomic Redis counter finds a candidate pod at its bound.
func TestClaimSlotRecordsConflictOnSlotContention(t *testing.T) {
	c := newEnvtestClient(t, concurrentSandbox("sbx-a", "claimed", "acme"))
	counter := newCounter(t)
	seedOccupiedPod(t, c, counter, "sbx-a", "acme", 2)

	var conflicts []string
	claimer := &podclaim.SlotClaimer{
		Client:         c,
		Namespace:      testNS,
		Counter:        counter,
		OnSlotConflict: func(pool string) { conflicts = append(conflicts, pool) },
	}

	_, err := claimer.ClaimSlot(context.Background(), slotReq("sess-x", "acme", 2))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Errorf("error = %v, want ErrNoConcurrentSlot (pods exist but full)", err)
	}
	if len(conflicts) != 1 || conflicts[0] != testPool {
		t.Errorf("OnSlotConflict calls = %v, want one call for pool %q", conflicts, testPool)
	}
}

// spec: §6.2 lines 166-167 (uptime placement filter), §4.6.1 (uptime drains
// are WarmPoolController-written).
// diagnosis: ClaimSlot placed a slot on a pod whose wall-clock uptime had
// exceeded the pool's maxPodUptimeSeconds, or it wrote Sandbox.status to
// drain the pod itself. §6.2 line 166: an over-uptime claimed pod accepts no
// new slots, so ClaimSlot must skip it as a candidate. Per §4.6.1 the
// gateway no longer owns the draining transition; the WarmPoolController
// derives it from the pod CreationTimestamp. The gateway therefore skips the
// pod read-only and must not mutate Sandbox.status.
func TestClaimSlotSkipsOverUptimeClaimedPod_spec_6_2(t *testing.T) {
	claimer, c, counter := slotClaimerFor(t,
		concurrentSandbox("sbx-old", "claimed", "acme"))
	seedOccupiedPod(t, c, counter, "sbx-old", "acme", 1)
	// The pod was created ~now; advance the claimer's clock two hours past
	// creation so its uptime exceeds the 60s cap.
	claimer.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	req := slotReq("sess-1", "acme", 8)
	req.MaxPodUptimeSeconds = 60
	_, err := claimer.ClaimSlot(context.Background(), req)
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Fatalf("ClaimSlot err = %v, want ErrNoConcurrentSlot (the only pod was skipped)", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-old"}, &sb); err != nil {
		t.Fatalf("get sbx-old: %v", err)
	}
	// §4.6.1: the gateway does not write Sandbox.status; the seeded
	// (WPC-owned) phase is left untouched. The WarmPoolController owns the
	// claimed → draining uptime transition.
	if sb.Status.Phase != "claimed" {
		t.Errorf("over-uptime claimed pod phase = %q, want claimed unchanged (gateway must not write Sandbox.status, §4.6.1)", sb.Status.Phase)
	}
	assertNotGatewayStatusOwned(t, c, "sbx-old")
}

// spec: §6.2 line 167 (uptime placement filter), §4.6.1 (uptime drains are
// WarmPoolController-written).
// diagnosis: ClaimSlot acquired a fresh slot on an idle pod that was already
// past its uptime cap, or it wrote Sandbox.status to drain the pod. Per
// §4.6.1 the gateway skips an over-uptime idle pod read-only and leaves the
// idle → draining transition to the WarmPoolController.
func TestClaimSlotSkipsOverUptimeIdlePod_spec_6_2(t *testing.T) {
	claimer, c, _ := slotClaimerFor(t,
		concurrentSandbox("sbx-old", "idle", ""))
	claimer.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	req := slotReq("sess-1", "acme", 8)
	req.MaxPodUptimeSeconds = 60
	_, err := claimer.ClaimSlot(context.Background(), req)
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Fatalf("ClaimSlot err = %v, want ErrNoConcurrentSlot (the only pod was skipped)", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-old"}, &sb); err != nil {
		t.Fatalf("get sbx-old: %v", err)
	}
	// §4.6.1: the gateway does not write Sandbox.status; the idle phase is
	// left untouched and no per-pod claim is created for the skipped pod.
	if sb.Status.Phase != string(state.Idle) {
		t.Errorf("over-uptime idle pod phase = %q, want idle unchanged (gateway must not write Sandbox.status, §4.6.1)", sb.Status.Phase)
	}
	if podClaimExists(t, c, "sbx-old") {
		t.Error("over-uptime idle pod was acquired (per-pod claim created); it must be skipped read-only")
	}
	assertNotGatewayStatusOwned(t, c, "sbx-old")
}

// spec: §6.2 lines 166-167.
// diagnosis: ClaimSlot drained a pod still within its uptime budget. A pod
// under maxPodUptimeSeconds must serve the slot normally.
func TestClaimSlotKeepsUnderUptimePod_spec_6_2(t *testing.T) {
	claimer, c, _ := slotClaimerFor(t,
		concurrentSandbox("sbx-fresh", "idle", ""))

	req := slotReq("sess-1", "acme", 8)
	req.MaxPodUptimeSeconds = 86400
	res, err := claimer.ClaimSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-fresh" {
		t.Errorf("acquired %q, want sbx-fresh", res.SandboxName)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-fresh"}, &sb); err != nil {
		t.Fatalf("get sbx-fresh: %v", err)
	}
	if sb.Status.Phase == string(state.Draining) {
		t.Error("under-uptime pod was drained")
	}
	if !podClaimExists(t, c, "sbx-fresh") {
		t.Error("under-uptime pod must be acquired (per-pod claim created)")
	}
}

// spec: §6.2 lines 166-167 — the cap is optional; zero disables retirement.
// diagnosis: ClaimSlot drained a pod even though the pool set no
// maxPodUptimeSeconds, retiring pods that should run indefinitely.
func TestClaimSlotUptimeCapZeroDisablesCheck_spec_6_2(t *testing.T) {
	claimer, c, _ := slotClaimerFor(t,
		concurrentSandbox("sbx-fresh", "idle", ""))
	claimer.Now = func() time.Time { return time.Now().Add(1000 * time.Hour) }

	req := slotReq("sess-1", "acme", 8)
	req.MaxPodUptimeSeconds = 0
	res, err := claimer.ClaimSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-fresh" {
		t.Errorf("acquired %q, want sbx-fresh", res.SandboxName)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-fresh"}, &sb); err != nil {
		t.Fatalf("get sbx-fresh: %v", err)
	}
	if sb.Status.Phase == string(state.Draining) {
		t.Error("pod drained despite maxPodUptimeSeconds=0 (uptime check must be disabled)")
	}
}

// seedReservedSlotPod creates the per-pod claim for sandboxName, patches it
// bound → recycling → reserved with the given hold TTL, so the §3.2 slot-path
// rebind branch sees a reserved claim it can rebind.
func seedReservedSlotPod(t *testing.T, c client.Client, sandboxName, tenantID string, now time.Time, holdTTL time.Duration) {
	t.Helper()
	ctx := context.Background()
	name := podclaim.ClaimName(sandboxName)
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxName, TenantID: tenantID},
	}
	if err := c.Create(ctx, claim); err != nil {
		t.Fatalf("create claim %s: %v", name, err)
	}
	if err := podclaim.WriteBoundStatus(ctx, c, testNS, name); err != nil {
		t.Fatalf("seed bound: %v", err)
	}
	clk := func() time.Time { return now }
	if err := podclaim.WriteRecyclingStatus(ctx, c, testNS, name, clk); err != nil {
		t.Fatalf("seed recycling: %v", err)
	}
	if _, err := podclaim.WriteReservedStatus(ctx, c, testNS, name, holdTTL, clk); err != nil {
		t.Fatalf("seed reserved: %v", err)
	}
}

// TestClaimSlotRebindsReservedSamePodWithinHold verifies the §3.2 slot-path
// rebind: a same-tenant slot request on a pod held in `reserved` within its
// hold window rebinds the claim (reserved → bound) and reserves the first slot
// on it, rather than acquiring a fresh idle pod.
// spec: 3.2 (within-hold rebind, no acquisition round trip), 4.6.1 (reserved hold)
//
// diagnosis: a failure means a concurrent-session slot request re-acquires a
// fresh pod instead of dispatching onto the same tenant's scrubbed, reserved
// pod, so the reserved-hold latency optimization is lost on the slot path and a
// reserved claim could acquire a stuck slot it never rebound to bound.
func TestClaimSlotRebindsReservedSamePodWithinHold_spec_3_2(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	claimer, c, _ := slotClaimerFor(t,
		concurrentSandbox("sbx-held", "reserved", "acme"),
		concurrentSandbox("sbx-idle", "idle", ""),
	)
	claimer.Now = func() time.Time { return now.Add(5 * time.Second) } // within the hold
	seedReservedSlotPod(t, c, "sbx-held", "acme", now, 30*time.Second)

	res, err := claimer.ClaimSlot(context.Background(), slotReq("sess-2", "acme", 8))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-held" {
		t.Errorf("slot landed on %q, want the rebound reserved pod sbx-held", res.SandboxName)
	}
	if res.FreshPod {
		t.Error("rebind onto a reserved pod should not report FreshPod=true (no claim CREATE)")
	}
	stored := &lennyv1.SandboxClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName("sbx-held")}, stored); err != nil {
		t.Fatalf("get rebound claim: %v", err)
	}
	if stored.Status.Phase != "bound" {
		t.Errorf("rebound claim phase = %q, want bound", stored.Status.Phase)
	}
}

// TestClaimSlotSkipsExpiredReservedHold verifies a reserved pod whose hold has
// expired is not rebound on the slot path; the slot lands on a fresh idle pod.
// spec: 3.2 (hold-expiry, no rebind of an expired hold)
func TestClaimSlotSkipsExpiredReservedHold_spec_3_2(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	claimer, c, _ := slotClaimerFor(t,
		concurrentSandbox("sbx-expired", "reserved", "acme"),
		concurrentSandbox("sbx-idle", "idle", ""),
	)
	claimer.Now = func() time.Time { return now.Add(time.Minute) } // past the hold
	seedReservedSlotPod(t, c, "sbx-expired", "acme", now, 10*time.Second)

	res, err := claimer.ClaimSlot(context.Background(), slotReq("sess-3", "acme", 8))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "sbx-idle" {
		t.Errorf("slot landed on %q, want the fresh idle pod sbx-idle (expired hold not rebound)", res.SandboxName)
	}
	if !res.FreshPod {
		t.Error("a fresh idle acquisition should report FreshPod=true")
	}
}
