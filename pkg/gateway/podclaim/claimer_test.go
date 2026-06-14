// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker"
)

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

func sandboxIn(pool, name, phase string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Status: lennyv1.SandboxStatus{Phase: phase},
	}
}

// claimerFor boots an envtest API server (real SSA support) and returns
// a Claimer wired to it.
func claimerFor(t *testing.T, objs ...client.Object) (*podclaim.Claimer, client.Client) {
	t.Helper()
	c := newEnvtestClient(t, objs...)
	return &podclaim.Claimer{Client: c, Namespace: testNS}, c
}

// spec: §4.6.1, §4.6.3
// TestClaimBindsAnIdlePod_spec_4_6 — Claim CREATEs the per-pod occupancy
// SandboxClaim (claim-<podName>) with spec only, then writes its first
// `bound` binding-state status patch. The gateway does not write
// Sandbox.status; the WarmPoolController projects the pod's phase from the
// claim binding state.
func TestClaimBindsAnIdlePod_spec_4_6(t *testing.T) {
	claimer, c := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// The per-pod occupancy claim (§4.6.3) is named claim-<podName> and
	// carries sandboxRef and tenantId only; the session-to-pod binding lives
	// on the Postgres session row's pod_assignment column.
	if claim.Name != "claim-sbx-1" {
		t.Errorf("claim name = %q, want claim-sbx-1 (per-pod deterministic name)", claim.Name)
	}
	if claim.Spec.SandboxRef != "sbx-1" || claim.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want a binding of acme to sbx-1", claim.Spec)
	}

	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: claim.Name}, &stored); err != nil {
		t.Fatalf("the SandboxClaim was not persisted: %v", err)
	}
	if stored.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound (first status patch)", stored.Status.Phase)
	}

	// The gateway no longer writes Sandbox.status.phase; the WPC projection
	// owns it, so the pod's status stays at the WPC-written idle value here.
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("sandbox phase = %q, want idle (gateway does not write Sandbox.status; WPC projects claimed from the claim binding state)", sb.Status.Phase)
	}
}

func TestClaimReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	claimer, _ := claimerFor(t)

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod for an empty pool", err)
	}
}

func TestClaimSkipsNonIdleSandboxes(t *testing.T) {
	claimer, _ := claimerFor(
		t,
		sandboxIn(testPool, "sbx-warming", "warming"),
		sandboxIn(testPool, "sbx-claimed", "claimed"),
		sandboxIn(testPool, "sbx-draining", "draining"),
	)
	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when no pod is idle", err)
	}
}

func TestClaimPicksTheIdleSandboxAmongMixedPhases(t *testing.T) {
	claimer, _ := claimerFor(
		t,
		sandboxIn(testPool, "sbx-warming", "warming"),
		sandboxIn(testPool, "sbx-idle", "idle"),
		sandboxIn(testPool, "sbx-claimed", "claimed"),
	)
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-idle" {
		t.Errorf("claimed %q, want the idle sandbox sbx-idle", claim.Spec.SandboxRef)
	}
}

func TestClaimScopesToTheRequestedPool(t *testing.T) {
	// The only idle pod belongs to a different pool.
	claimer, _ := claimerFor(t, sandboxIn("other-pool", "sbx-other", "idle"))

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod; an idle pod in another pool must not be claimed", err)
	}
}

// spec: §4.6.1 ADR-007 — the per-pod SandboxClaim CREATE with the
// deterministic name claim-<podName> is the §4.6.1 authoritative
// single-claim guard. A claimed pod (its claim already exists) collides on
// AlreadyExists, so Claim skips it and acquires the next idle pod; a pool
// where every idle pod is already claimed surfaces ErrNoIdlePod.
func TestClaimSkipsAlreadyClaimedPod_spec_4_6(t *testing.T) {
	c := newEnvtestClient(
		t,
		sandboxIn(testPool, "sbx-1", "idle"),
		sandboxIn(testPool, "sbx-2", "idle"),
	)
	ctx := context.Background()
	// A competing replica already claimed sbx-1: its per-pod claim exists.
	if _, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-1", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed first claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: c, Namespace: testNS}

	// Claim skips the already-claimed sbx-1 (AlreadyExists on the per-pod
	// name) and acquires the next idle pod, sbx-2.
	claim, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-2" {
		t.Errorf("acquired %q, want sbx-2 (sbx-1 already has a per-pod claim)", claim.Spec.SandboxRef)
	}
}

// spec: §4.6.1 — when every idle pod already carries a per-pod claim, a new
// acquisition surfaces ErrNoIdlePod so the caller does not double-claim.
func TestClaimReturnsErrNoIdlePodWhenAllClaimed_spec_4_6(t *testing.T) {
	c := newEnvtestClient(t, sandboxIn(testPool, "sbx-1", "idle"))
	ctx := context.Background()
	if _, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-1", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: c, Namespace: testNS}
	_, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when every idle pod is already claimed", err)
	}
}

// spec: §4.6.3 ownership table — the gateway does not write Sandbox.status;
// the WarmPoolController projects the pod phase from the claim binding
// state. Claim must therefore leave Sandbox.status untouched by the
// gateway field manager.
// TestClaimDoesNotWriteSandboxStatus_spec_4_6_3 — verifies no
// lenny-gateway managedFields entry on the Sandbox status subresource.
func TestClaimDoesNotWriteSandboxStatus_spec_4_6_3(t *testing.T) {
	claimer, c := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))

	if _, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	for _, mf := range sb.ManagedFields {
		if mf.Manager == "lenny-gateway" && mf.Subresource == "status" {
			t.Errorf("gateway must not write Sandbox.status; found managedFields entry %+v", mf)
		}
	}
}

// Defensive: a second CREATE for the same pod (the deterministic
// claim-<podName> name) returns the AlreadyExists error class so the
// claimer's apierrors.IsAlreadyExists skip-to-next-pod path triggers.
func TestClaimCreateClaimPerPodAlreadyExistsClass(t *testing.T) {
	c := newEnvtestClient(t)
	ctx := context.Background()
	if _, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-x", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("first CreateClaim: %v", err)
	}

	_, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-x", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s2", TenantID: "acme",
	})
	if err == nil {
		t.Fatal("second CreateClaim for the same pod should fail")
	}
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("second CreateClaim error = %v, want apierrors.IsAlreadyExists", err)
	}
}
