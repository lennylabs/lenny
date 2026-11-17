// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

const testPool = "claude-worker"

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	// node/v1 carries RuntimeClass, which envtest's apiserver validates
	// against when a pod sets spec.runtimeClassName (the §5.3 Kata path).
	if err := nodev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme nodev1: %v", err)
	}
	return s
}

func agentPodIn(ns, pool, name, phase string) *lennyv1.AgentPod {
	return &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Status: lennyv1.AgentPodStatus{Phase: phase},
	}
}

// claimerFor boots an envtest API server (real SSA support) and returns
// a Claimer wired to it.
func claimerFor(t *testing.T, objs ...client.Object) (*podclaim.Claimer, client.Client) {
	t.Helper()
	ns := testNamespace(t)
	c := newEnvtestClient(t, objs...)
	return &podclaim.Claimer{Client: c, Namespace: ns}, c
}

// spec: §4.6.1, §4.6.3
// TestClaimBindsAnIdlePod_spec_4_6 — Claim CREATEs the per-pod occupancy
// PodClaim (claim-<podName>) with spec only, then writes its first
// `bound` binding-state status patch. The gateway does not write
// AgentPod.status; the WarmPoolController projects the pod's phase from the
// claim binding state.
func TestClaimBindsAnIdlePod_spec_4_6(t *testing.T) {
	ns := testNamespace(t)
	claimer, c := claimerFor(t, agentPodIn(ns, testPool, "sbx-1", "idle"))

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// The per-pod occupancy claim (§4.6.3) is named claim-<podName> and
	// carries agentPodRef and tenantId only; the session-to-pod binding lives
	// on the Postgres session row's pod_assignment column.
	if claim.Name != "claim-sbx-1" {
		t.Errorf("claim name = %q, want claim-sbx-1 (per-pod deterministic name)", claim.Name)
	}
	if claim.Spec.AgentPodRef != "sbx-1" || claim.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want a binding of acme to sbx-1", claim.Spec)
	}

	var stored lennyv1.PodClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: claim.Name}, &stored); err != nil {
		t.Fatalf("the PodClaim was not persisted: %v", err)
	}
	if stored.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound (first status patch)", stored.Status.Phase)
	}

	// The gateway no longer writes AgentPod.status.phase; the WPC projection
	// owns it, so the pod's status stays at the WPC-written idle value here.
	var sb lennyv1.AgentPod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("sandbox phase = %q, want idle (gateway does not write AgentPod.status; WPC projects claimed from the claim binding state)", sb.Status.Phase)
	}
}

func TestClaimReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	claimer, _ := claimerFor(t)

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod for an empty pool", err)
	}
}

func TestClaimSkipsNonIdleAgentPods(t *testing.T) {
	ns := testNamespace(t)
	claimer, _ := claimerFor(
		t,
		agentPodIn(ns, testPool, "sbx-warming", "warming"),
		agentPodIn(ns, testPool, "sbx-claimed", "claimed"),
		agentPodIn(ns, testPool, "sbx-draining", "draining"),
	)
	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when no pod is idle", err)
	}
}

func TestClaimPicksTheIdleAgentPodAmongMixedPhases(t *testing.T) {
	ns := testNamespace(t)
	claimer, _ := claimerFor(
		t,
		agentPodIn(ns, testPool, "sbx-warming", "warming"),
		agentPodIn(ns, testPool, "sbx-idle", "idle"),
		agentPodIn(ns, testPool, "sbx-claimed", "claimed"),
	)
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.AgentPodRef != "sbx-idle" {
		t.Errorf("claimed %q, want the idle sandbox sbx-idle", claim.Spec.AgentPodRef)
	}
}

func TestClaimScopesToTheRequestedPool(t *testing.T) {
	ns := testNamespace(t)
	// The only idle pod belongs to a different pool.
	claimer, _ := claimerFor(t, agentPodIn(ns, "other-pool", "sbx-other", "idle"))

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod; an idle pod in another pool must not be claimed", err)
	}
}

// spec: §4.6.1 ADR-007 — the per-pod PodClaim CREATE with the
// deterministic name claim-<podName> is the §4.6.1 authoritative
// single-claim guard. A claimed pod (its claim already exists) collides on
// AlreadyExists, so Claim skips it and acquires the next idle pod; a pool
// where every idle pod is already claimed surfaces ErrNoIdlePod.
func TestClaimSkipsAlreadyClaimedPod_spec_4_6(t *testing.T) {
	ns := testNamespace(t)
	c := newEnvtestClient(
		t,
		agentPodIn(ns, testPool, "sbx-1", "idle"),
		agentPodIn(ns, testPool, "sbx-2", "idle"),
	)
	ctx := context.Background()
	// A competing replica already claimed sbx-1: its per-pod claim exists.
	if _, err := podclaim.CreateClaim(ctx, c, ns, "sbx-1", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed first claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: c, Namespace: ns}

	// Claim skips the already-claimed sbx-1 (AlreadyExists on the per-pod
	// name) and acquires the next idle pod, sbx-2.
	claim, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.AgentPodRef != "sbx-2" {
		t.Errorf("acquired %q, want sbx-2 (sbx-1 already has a per-pod claim)", claim.Spec.AgentPodRef)
	}
}

// spec: §4.6.1 — when every idle pod already carries a per-pod claim, a new
// acquisition surfaces ErrNoIdlePod so the caller does not double-claim.
func TestClaimReturnsErrNoIdlePodWhenAllClaimed_spec_4_6(t *testing.T) {
	ns := testNamespace(t)
	c := newEnvtestClient(t, agentPodIn(ns, testPool, "sbx-1", "idle"))
	ctx := context.Background()
	if _, err := podclaim.CreateClaim(ctx, c, ns, "sbx-1", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: c, Namespace: ns}
	_, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when every idle pod is already claimed", err)
	}
}

// spec: §4.6.3 ownership table — the gateway does not write AgentPod.status;
// the WarmPoolController projects the pod phase from the claim binding
// state. Claim must therefore leave AgentPod.status untouched by the
// gateway field manager.
// TestClaimDoesNotWriteAgentPodStatus — verifies no
// lenny-gateway managedFields entry on the AgentPod status subresource.
func TestClaimDoesNotWriteAgentPodStatus(t *testing.T) {
	ns := testNamespace(t)
	claimer, c := claimerFor(t, agentPodIn(ns, testPool, "sbx-1", "idle"))

	if _, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var sb lennyv1.AgentPod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	for _, mf := range sb.ManagedFields {
		if mf.Manager == "lenny-gateway" && mf.Subresource == "status" {
			t.Errorf("gateway must not write AgentPod.status; found managedFields entry %+v", mf)
		}
	}
}

// Defensive: a second CREATE for the same pod (the deterministic
// claim-<podName> name) returns the AlreadyExists error class so the
// claimer's apierrors.IsAlreadyExists skip-to-next-pod path triggers.
func TestClaimCreateClaimPerPodAlreadyExistsClass(t *testing.T) {
	ns := testNamespace(t)
	c := newEnvtestClient(t)
	ctx := context.Background()
	if _, err := podclaim.CreateClaim(ctx, c, ns, "sbx-x", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("first CreateClaim: %v", err)
	}

	_, err := podclaim.CreateClaim(ctx, c, ns, "sbx-x", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if err == nil {
		t.Fatal("second CreateClaim for the same pod should fail")
	}
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("second CreateClaim error = %v, want apierrors.IsAlreadyExists", err)
	}
}

// reservedAgentPod builds an AgentPod projecting the §6.2 `reserved` phase,
// pinned to tenantID, the projection the acquisition-path rebind branch
// scans for.
func reservedAgentPod(ns, name, tenantID string) *lennyv1.AgentPod {
	sb := agentPodIn(ns, testPool, name, "reserved")
	sb.Status.TenantID = tenantID
	if tenantID != "" {
		sb.Labels[podclaim.LabelTenant] = tenantID
	}
	return sb
}

// seedReservedClaim creates the per-pod claim for podID, patches it bound →
// recycling → reserved, and returns it. holdTTL sets holdExpiresAt at
// reservedAt + holdTTL where reservedAt is the supplied now.
func seedReservedClaim(t *testing.T, c client.Client, podID, tenantID string, now time.Time, holdTTL time.Duration) {
	t.Helper()
	ns := testNamespace(t)
	ctx := context.Background()
	name := podclaim.ClaimName(podID)
	claim := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       lennyv1.PodClaimSpec{AgentPodRef: podID, TenantID: tenantID},
	}
	if err := c.Create(ctx, claim); err != nil {
		t.Fatalf("create claim %s: %v", name, err)
	}
	if err := podclaim.WriteBoundStatus(ctx, c, ns, name); err != nil {
		t.Fatalf("seed bound: %v", err)
	}
	clk := func() time.Time { return now }
	if err := podclaim.WriteRecyclingStatus(ctx, c, ns, name, clk); err != nil {
		t.Fatalf("seed recycling: %v", err)
	}
	if _, err := podclaim.WriteReservedStatus(ctx, c, ns, name, holdTTL, clk); err != nil {
		t.Fatalf("seed reserved: %v", err)
	}
}

// TestClaimRebindsReservedPodWithinHold verifies the acquisition-path
// rebind branch: a same-tenant session dispatches onto a pod the tenant holds
// in `reserved` within its hold window via a reserved → bound patch, with no
// fresh idle-pod acquisition.
// spec: 3.2 (within-hold rebind, no acquisition round trip), 4.6.1 (reserved hold)
func TestClaimRebindsReservedPodWithinHold_spec_3_2(t *testing.T) {
	ns := testNamespace(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	// The rebind reads the reserved pod before its patch, so the Pod exists.
	c := newEnvtestClient(t, reservedAgentPod(ns, "sbx-held", "acme"), lifetimePod(ns, testPool, "sbx-held", "acme", "keep"))
	seedReservedClaim(t, c, "sbx-held", "acme", now, 30*time.Second)

	var rebound []string
	claimer := &podclaim.Claimer{
		Client: c, Namespace: ns,
		Now:      func() time.Time { return now.Add(5 * time.Second) }, // within the 30s hold
		OnRebind: func(podID string) { rebound = append(rebound, podID) },
	}
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme", ReusesPods: true, RequiresProcessAcknowledgment: true,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Name != podclaim.ClaimName("sbx-held") {
		t.Errorf("rebound onto claim %q, want the held pod's claim", claim.Name)
	}
	if claim.Status.Phase != "bound" {
		t.Errorf("rebound claim phase = %q, want bound", claim.Status.Phase)
	}
	if len(rebound) != 1 || rebound[0] != "sbx-held" {
		t.Errorf("OnRebind fired for %v, want [sbx-held]", rebound)
	}
}

// TestClaimSkipsExpiredReservedHoldAndAcquiresIdle verifies a reserved pod
// whose hold has expired is not rebound (it is left to the holder's expiry
// DELETE or the orphan GC), so the claim falls through to a fresh idle
// acquisition.
// spec: 3.2 (hold-expiry, no rebind of an expired hold)
func TestClaimSkipsExpiredReservedHoldAndAcquiresIdle_spec_3_2(t *testing.T) {
	ns := testNamespace(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	c := newEnvtestClient(
		t,
		reservedAgentPod(ns, "sbx-expired", "acme"),
		agentPodIn(ns, testPool, "sbx-idle", "idle"),
	)
	seedReservedClaim(t, c, "sbx-expired", "acme", now, 10*time.Second)

	claimer := &podclaim.Claimer{
		Client: c, Namespace: ns,
		Now: func() time.Time { return now.Add(time.Minute) }, // past the 10s hold
	}
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-3", TenantID: "acme", ReusesPods: true, RequiresProcessAcknowledgment: true,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.AgentPodRef != "sbx-idle" {
		t.Errorf("acquired %q, want the fresh idle pod sbx-idle (expired hold not rebound)", claim.Spec.AgentPodRef)
	}
}

// TestClaimDoesNotRebindReservedPodOfOtherTenant verifies a reserved pod
// pinned to a different tenant is never rebound; the claim acquires its own
// fresh idle pod instead.
// spec: 3.2 (a reserved pod is held for its pinned tenant alone), 5.2 (tenant pinning)
func TestClaimDoesNotRebindReservedPodOfOtherTenant_spec_3_2(t *testing.T) {
	ns := testNamespace(t)
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	c := newEnvtestClient(
		t,
		reservedAgentPod(ns, "sbx-globex", "globex"),
		agentPodIn(ns, testPool, "sbx-idle", "idle"),
	)
	seedReservedClaim(t, c, "sbx-globex", "globex", now, time.Minute)

	claimer := &podclaim.Claimer{
		Client: c, Namespace: ns, Now: func() time.Time { return now.Add(5 * time.Second) },
	}
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-4", TenantID: "acme", ReusesPods: true, RequiresProcessAcknowledgment: true,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.AgentPodRef != "sbx-idle" {
		t.Errorf("acquired %q, want sbx-idle (cross-tenant reserved pod not rebound)", claim.Spec.AgentPodRef)
	}
}
