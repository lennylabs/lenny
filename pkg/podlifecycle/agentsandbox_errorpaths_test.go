// SPDX-License-Identifier: MIT

package podlifecycle_test

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
	"github.com/lennylabs/lenny/pkg/podlifecycle"
)

// idlePoolObjects builds an idle pool the §4.6.1 ClaimPod path acquires from.
func idlePoolObjects() []client.Object {
	return []client.Object{
		&lennyv1.SandboxTemplate{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}},
		&lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
			Spec:       lennyv1.SandboxSpec{PoolRef: "p1"},
			Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
		},
	}
}

// TestClaimPodPropagatesNonConflictCreateError pins the §4.6.1 CREATE error
// handling: a claim CREATE that fails for a reason other than AlreadyExists or
// Forbidden propagates rather than being swallowed as a benign race, so a
// genuine API failure surfaces to the caller.
//
// spec: 4.6.1 (per-pod claim CREATE; only AlreadyExists/Forbidden are the race
// branch)
func TestClaimPodPropagatesNonConflictCreateError_spec_4_6_1(t *testing.T) {
	internalErr := apierrors.NewInternalError(errors.New("etcd down"))
	c := fake.NewClientBuilder().
		WithScheme(sandboxScheme(t)).
		WithObjects(idlePoolObjects()...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxTemplate{}, &lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
				if _, ok := obj.(*lennyv1.SandboxClaim); ok {
					return internalErr
				}
				return nil
			},
		}).Build()
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	_, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{TenantID: "acme"})
	if err == nil || errors.Is(err, podlifecycle.ErrClaimConflict) {
		t.Fatalf("ClaimPod on an internal CREATE error = %v, want the error propagated", err)
	}
}

// TestClaimPodDeletesPartialClaimOnBoundStatusFailure pins the §4.6.1
// CREATE-before-status crash mitigation: when the first `bound` status patch
// fails the partial claim is deleted so a retry is not blocked by a claim with
// empty status, and the error propagates.
//
// spec: 4.6.1 (first `bound` status patch; delete the partial claim on failure)
func TestClaimPodDeletesPartialClaimOnBoundStatusFailure_spec_4_6_1(t *testing.T) {
	patchErr := apierrors.NewInternalError(errors.New("status write failed"))
	c := fake.NewClientBuilder().
		WithScheme(sandboxScheme(t)).
		WithObjects(idlePoolObjects()...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxTemplate{}, &lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(_ context.Context, _ client.Client, _ string, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*lennyv1.SandboxClaim); ok {
					return patchErr
				}
				return nil
			},
		}).Build()
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if _, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{TenantID: "acme"}); err == nil {
		t.Fatal("ClaimPod returned nil on a bound-status failure, want propagation")
	}
	// The partial claim must be deleted so a retry is not blocked.
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "claim-pod-1"}, &claim); !apierrors.IsNotFound(err) {
		t.Fatalf("partial claim still present after a bound-status failure: err = %v, want NotFound", err)
	}
}

// TestReleasePodPropagatesNonNotFoundDeleteError pins the §4.6.1 release error
// handling: a claim DELETE that fails for a reason other than NotFound
// propagates, so a transient API failure does not look like a successful
// idempotent release.
//
// spec: 4.6.1 (release deletes the per-pod claim; only NotFound is idempotent)
func TestReleasePodPropagatesNonNotFoundDeleteError_spec_4_6_1(t *testing.T) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "pod-1"},
		Status:     lennyv1.SandboxClaimStatus{Phase: "bound"},
	}
	internalErr := apierrors.NewInternalError(errors.New("etcd down"))
	c := fake.NewClientBuilder().
		WithScheme(sandboxScheme(t)).
		WithObjects(claim).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return internalErr
			},
		}).Build()
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if err := m.ReleasePod(context.Background(), podlifecycle.PodHandle{SandboxName: "pod-1", Namespace: "agents"}); err == nil {
		t.Fatal("ReleasePod returned nil on an internal DELETE error, want propagation")
	}
}
