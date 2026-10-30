// SPDX-License-Identifier: MIT

package podregistry_test

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/podregistry"
)

// The claim happy path (the SSA `bound` status write in claimViaSandboxClaim)
// is exercised against a real API server in the tier-2-component
// crdpodregistry_claim_test.go, because the fake client cannot serve
// Server-Side-Apply patches. The unit-tier tests below cover the two error
// branches the fake client can reach: the AlreadyExists single-claim-guard
// skip and the transient non-race CREATE failure.

// TestClaimPodSkipsPodWhoseClaimAlreadyExists covers the §4.6.1 single-claim
// guard race branch: when the per-pod claim CREATE collides with a claim a
// concurrent writer already created (AlreadyExists), ClaimPod treats the pod
// as taken and advances to the next idle pod rather than failing the whole
// acquisition. Here the only idle pod is already claimed, so the scan
// exhausts.
//
// spec: §4.6.1 (CREATE-collision is the single-claim guard; advance to the
// next idle pod).
func TestClaimPodSkipsPodWhoseClaimAlreadyExists_spec_4_6_1(t *testing.T) {
	// Seed the idle Sandbox and its already-existing per-pod claim, so the
	// CREATE in claimViaSandboxClaim returns AlreadyExists.
	existing := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("alpha"), Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "alpha", TenantID: "acme"},
	}
	r := newRegistry(t, "lenny-agents", seedSandbox("alpha", "echo-pool", "idle"), existing)

	_, err := r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if !errors.Is(err, podregistry.ErrPoolExhausted) {
		t.Errorf("err = %v, want ErrPoolExhausted (the only idle pod was already claimed)", err)
	}
}

// TestClaimPodPropagatesNonRaceClaimError covers the §4.6.1 claim path's
// non-race error branch: a claim CREATE failure that is neither AlreadyExists
// nor Forbidden (a transient API error) aborts the acquisition with the
// wrapped error rather than silently skipping the pod, so the caller retries
// instead of mistaking a transient failure for pool exhaustion.
//
// spec: §4.6.1 (per-pod claim CREATE).
func TestClaimPodPropagatesNonRaceClaimError_spec_4_6_1(t *testing.T) {
	boom := errors.New("apiserver unavailable")
	base := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(seedSandbox("alpha", "echo-pool", "idle")).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		Build()
	cli := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				return boom
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}

	_, err = r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if err == nil {
		t.Fatal("a transient claim CREATE error must abort the claim, got nil")
	}
	if errors.Is(err, podregistry.ErrPoolExhausted) {
		t.Errorf("err = %v, want the wrapped transient error, not ErrPoolExhausted", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error chain = %v, want it to wrap %v", err, boom)
	}
	if apierrors.IsAlreadyExists(err) {
		t.Errorf("a non-race error must not be classified as AlreadyExists: %v", err)
	}
}
