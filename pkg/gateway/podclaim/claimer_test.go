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
// TestClaimBindsAnIdlePod_spec_4_6 — Claim CREATEs the SandboxClaim
// first (single-claim guard) and then SSA-applies status.phase = claimed
// under the gateway field manager.
func TestClaimBindsAnIdlePod_spec_4_6(t *testing.T) {
	claimer, c := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-1" || claim.Spec.SessionID != "sess-1" || claim.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want a binding of sess-1/acme to sbx-1", claim.Spec)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}

	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: claim.Name}, &stored); err != nil {
		t.Errorf("the SandboxClaim was not persisted: %v", err)
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

// spec: §4.6.1 ADR-007 — the SandboxClaim CREATE with a
// session-deterministic name (claim-<sessionID>) is the §4.6.1
// authoritative single-claim guard. A repeated Claim() for the same
// session collides on AlreadyExists everywhere it tries, surfacing
// ErrNoIdlePod so the caller does not silently bind a second pod to the
// same session.
func TestClaimSameSessionRetryCollidesOnAlreadyExists_spec_4_6(t *testing.T) {
	c := newEnvtestClient(t,
		sandboxIn(testPool, "sbx-1", "idle"),
		sandboxIn(testPool, "sbx-2", "idle"),
	)
	ctx := context.Background()
	// First claim succeeds and creates claim-s bound to one of the pods.
	if _, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-1", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed first claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: c, Namespace: testNS}

	// A retried Claim for the same session sees AlreadyExists on every
	// CREATE attempt (deterministic name claim-s); ErrNoIdlePod surfaces.
	_, err := claimer.Claim(ctx, podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("retry error = %v, want ErrNoIdlePod (the deterministic name collides everywhere)", err)
	}
}

// spec: §4.6.3 ownership table — status.phase under SSA Apply with
// FieldOwner=lenny-gateway and ForceOwnership transfers ownership from
// the WPC default to the gateway. A subsequent SSA Apply by the gateway
// is idempotent on the value (same manager, same field).
// TestClaimSSAOwnershipIsGateway_spec_4_6_3 — verifies the managedFields
// entry for status.phase ends up under the gateway field manager.
func TestClaimSSAOwnershipIsGateway_spec_4_6_3(t *testing.T) {
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
	foundGateway := false
	for _, mf := range sb.ManagedFields {
		if mf.Manager == "lenny-gateway" && mf.Subresource == "status" {
			foundGateway = true
		}
	}
	if !foundGateway {
		t.Errorf("status managedFields did not include lenny-gateway: %+v", sb.ManagedFields)
	}
}

// Defensive: verify the deterministic-name AlreadyExists path returns
// the right error class for apierrors.IsAlreadyExists detection.
func TestClaimCreateClaimAlreadyExistsClass(t *testing.T) {
	c := newEnvtestClient(t)
	ctx := context.Background()
	first, err := podclaim.CreateClaim(ctx, c, testNS, "sbx-x", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("first CreateClaim: %v", err)
	}
	_ = first

	_, err = podclaim.CreateClaim(ctx, c, testNS, "sbx-y", podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme",
	})
	if err == nil {
		t.Fatal("second CreateClaim with same session id should fail")
	}
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("second CreateClaim error = %v, want apierrors.IsAlreadyExists", err)
	}
}
