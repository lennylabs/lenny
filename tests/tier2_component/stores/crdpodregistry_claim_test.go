//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.6.1 per-pod occupancy claim model on the v1
// CRD-backed PodRegistry. Unlike the fake-client unit tests in
// pkg/podregistry, this exercises CRDPodRegistry.ClaimPod against a real
// kube-apiserver with the lenny.dev CRDs installed, so the SandboxClaim
// OpenAPI schema (the status.phase enum), the status subresource, and the
// deterministic claim-<podName> name-uniqueness guard are all enforced by
// the API server rather than mocked.
//
// The change under test re-sources the CRD-backed claim onto the per-pod
// SandboxClaim and stops the gateway from writing Sandbox.status: a claim
// is now a SandboxClaim CREATE plus a `bound` status patch, and the
// WarmPoolController projects the coarse pod phase from the claim.
package stores_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/podregistry"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// claimTestEnv brings up an envtest API server with the lenny.dev CRDs and
// the agent namespace, returning a CRDPodRegistry and the raw client.
func claimTestEnv(t *testing.T) (*podregistry.CRDPodRegistry, client.Client, string) {
	t.Helper()
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	const ns = "lenny-agents"
	if err := c.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	r, err := podregistry.New(c, ns)
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	return r, c, ns
}

// seedIdleSandbox creates a Sandbox in the idle phase belonging to pool.
func seedIdleSandbox(t *testing.T, ctx context.Context, c client.Client, ns, name, pool string) {
	t.Helper()
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{podregistry.PoolLabel: pool},
		},
		Spec: lennyv1.SandboxSpec{RuntimeRef: "echo", PoolRef: pool, IsolationProfile: "sandboxed"},
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	sb.Status.Phase = "idle"
	if err := c.Status().Update(ctx, sb); err != nil {
		t.Fatalf("set idle status %s: %v", name, err)
	}
}

// spec: §4.6.1 (per-pod occupancy claim), §4.6.3 (gateway is not a
// Sandbox.status writer), §3.3 (occupancy projection). The CRD-backed
// ClaimPod creates the deterministic per-pod SandboxClaim with a `bound`
// binding state validated by the real CRD OpenAPI schema, and leaves the
// claimed Sandbox.status untouched for the WarmPoolController to project.
//
// diagnosis: a failure means the CRD-backed ClaimPod either still writes
// Sandbox.status (a gateway write the §4.6.3 RBAC boundary denies), does
// not create the per-pod SandboxClaim, or writes a binding-state phase the
// SandboxClaim OpenAPI enum rejects against a real API server.
func TestCRDPodRegistryClaimCreatesPerPodClaim_spec_4_6_1(t *testing.T) {
	r, c, ns := claimTestEnv(t)
	ctx := context.Background()
	const pool = "echo-pool"
	seedIdleSandbox(t, ctx, c, ns, "alpha", pool)

	rec, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
		PoolID: podregistry.PoolID(pool), TenantID: "acme", SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if rec.PodID != "alpha" {
		t.Errorf("PodID = %q, want alpha", rec.PodID)
	}
	if rec.State != "claimed" || rec.TenantID != "acme" || rec.SessionID != "sess-1" {
		t.Errorf("record = %+v, want claimed/acme/sess-1 (projected + echoed)", rec)
	}

	// The per-pod claim exists with the deterministic name, carries
	// sandboxRef + tenantId, and the API server accepted the `bound` enum.
	var claim lennyv1.SandboxClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claim-alpha"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Spec.SandboxRef != "alpha" || claim.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want sandboxRef=alpha tenantId=acme", claim.Spec)
	}
	if claim.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}
	// The `bound` write stamps the binding-state-transition time the §4.6.1
	// orphan GC keys its live-binding-state reclaim on; the real API server
	// round-trips the status.bindingStateTransitionTime field.
	if claim.Status.BindingStateTransitionTime == nil {
		t.Error("claim BindingStateTransitionTime = nil, want a stamp on the bound write")
	}

	// The gateway did not write Sandbox.status: the claimed Sandbox's stored
	// phase stays idle (the WarmPoolController would project claimed), and no
	// session/tenant is stamped on Sandbox.status.
	var sb lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "alpha"}, &sb); err != nil {
		t.Fatalf("get claimed sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("Sandbox.status.phase = %q, want idle (unwritten by the gateway)", sb.Status.Phase)
	}
	if sb.Status.SessionID != "" || sb.Status.TenantID != "" {
		t.Errorf("Sandbox.status session/tenant = %q/%q, want both empty (§4.6.3)",
			sb.Status.SessionID, sb.Status.TenantID)
	}
}

// spec: §4.6.1 (the deterministic claim-<podName> name is the per-pod
// single-claim guard). A second ClaimPod against a pool whose only idle
// pod already carries a claim exhausts: the duplicate claim CREATE collides
// on the API server's name-uniqueness check, the loop skips the held pod,
// and no other idle pod remains.
//
// diagnosis: a failure means the per-pod single-claim guard does not hold
// against a real API server — either the deterministic name is not used or
// an AlreadyExists collision is not skipped — so two claims could bind the
// same pod.
func TestCRDPodRegistryClaimSingleClaimGuard_spec_4_6_1(t *testing.T) {
	r, c, ns := claimTestEnv(t)
	ctx := context.Background()
	const pool = "echo-pool"
	seedIdleSandbox(t, ctx, c, ns, "alpha", pool)

	if _, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
		PoolID: podregistry.PoolID(pool), TenantID: "acme", SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("first ClaimPod: %v", err)
	}
	// The only idle pod is now claimed (its claim-alpha exists), so a second
	// claim finds no claimable idle pod and exhausts.
	_, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
		PoolID: podregistry.PoolID(pool), TenantID: "acme", SessionID: "sess-2",
	})
	if !errors.Is(err, podregistry.ErrPoolExhausted) {
		t.Errorf("second ClaimPod = %v, want ErrPoolExhausted (alpha already claimed)", err)
	}
}

// spec: §4.6.1 (occupancy projection on claim DELETE), §4.6.3 (gateway is not
// a Sandbox.status writer). ReleasePod deletes the deterministic per-pod
// SandboxClaim and writes no Sandbox.status against a real API server: the
// WarmPoolController projects the pod back to idle from the claim's absence.
// A second release of the same pod is idempotent.
//
// diagnosis: a failure means the CRD-backed ReleasePod still writes
// Sandbox.status (a gateway write the §4.6.3 RBAC boundary denies after S18),
// does not delete the per-pod claim, or errors on a missing claim instead of
// treating release as idempotent.
func TestCRDPodRegistryReleaseDeletesPerPodClaim_spec_4_6_3(t *testing.T) {
	r, c, ns := claimTestEnv(t)
	ctx := context.Background()
	const pool = "echo-pool"
	seedIdleSandbox(t, ctx, c, ns, "alpha", pool)

	// Claim alpha so its per-pod claim exists, then release it.
	if _, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
		PoolID: podregistry.PoolID(pool), TenantID: "acme", SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if err := r.ReleasePod(ctx, "alpha", podregistry.ReleaseCompleted); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}

	// The per-pod claim is gone; the API server reports NotFound.
	var claim lennyv1.SandboxClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claim-alpha"}, &claim); !apierrors.IsNotFound(err) {
		t.Errorf("claim still present after release: err = %v, want NotFound", err)
	}

	// Sandbox.status is untouched: the seeded idle phase persists because the
	// gateway is not a Sandbox.status writer and the WarmPoolController owns the
	// projection back to idle.
	var sb lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "alpha"}, &sb); err != nil {
		t.Fatalf("get released sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("Sandbox.status.phase = %q, want idle (unwritten by the gateway)", sb.Status.Phase)
	}
	if sb.Status.SessionID != "" || sb.Status.TenantID != "" {
		t.Errorf("Sandbox.status session/tenant = %q/%q, want both empty (§4.6.3)",
			sb.Status.SessionID, sb.Status.TenantID)
	}

	// A second release is a no-op: the claim is already gone.
	if err := r.ReleasePod(ctx, "alpha", podregistry.ReleaseCompleted); err != nil {
		t.Errorf("second ReleasePod = %v, want nil (idempotent)", err)
	}
}
