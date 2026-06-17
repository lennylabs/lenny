// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// reconcileForError runs one occupancy reconcile against the given client and
// returns the error, so the error-path tests can assert on it.
func reconcileForError(c client.Client) error {
	r := &warmpool.OccupancyReconciler{Client: c}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: "pod-err"},
	})
	return err
}

// TestOccupancyReconcileSwallowsSandboxNotFound covers the §4.6.1 occupancy
// reconciler's Sandbox-Get NotFound branch: a deleted Sandbox (the GC removed
// it between the watch event and the reconcile) is a no-op, not an error, so
// the work queue does not retry a vanished pod forever.
//
// spec: 4.6.1 (occupancy projection), 4.6.3 (ownership decomposition).
func TestOccupancyReconcileSwallowsSandboxNotFound(t *testing.T) {
	s := newScheme(t)
	// Empty client: the Sandbox does not exist, so Get returns NotFound.
	c := ctrlfake.NewClientBuilder().WithScheme(s).Build()
	if err := reconcileForError(c); err != nil {
		t.Fatalf("reconcile of a missing Sandbox should be a no-op, got %v", err)
	}
}

// TestOccupancyReconcileSkipsDeletingSandbox covers the deletion-timestamp
// guard: a Sandbox being torn down is the Sandbox-to-Pod reconciler's to drive
// to terminated, so the occupancy projection leaves it untouched rather than
// fighting the teardown.
//
// spec: 4.6.1 (occupancy projection), 6.2 (teardown is the teardown writer's).
func TestOccupancyReconcileSkipsDeletingSandbox(t *testing.T) {
	s := newScheme(t)
	now := metav1.Now()
	sb := occupiedSandbox("pod-err", "claimed")
	sb.DeletionTimestamp = &now
	sb.Finalizers = []string{"lenny.dev/test-hold"} // a deletion timestamp requires a finalizer
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(sb).Build()
	if err := reconcileForError(c); err != nil {
		t.Fatalf("reconcile of a deleting Sandbox should be a no-op, got %v", err)
	}
	// The phase is left as-is; the projection wrote nothing.
	if got := sandboxPhase(t, c, "pod-err"); got != "claimed" {
		t.Errorf("deleting Sandbox phase = %q, want unchanged claimed", got)
	}
}

// TestOccupancyReconcilePropagatesClaimGetError covers observeClaim's
// non-NotFound error branch: a transient API error reading the per-pod
// SandboxClaim is wrapped and returned so the work queue retries, rather than
// being mistaken for the no-claim (NotFound) case that would project an
// incorrect return-to-idle / drain phase.
//
// spec: 4.6.1 (occupancy projection reads the per-pod claim).
func TestOccupancyReconcilePropagatesClaimGetError(t *testing.T) {
	s := newScheme(t)
	sb := occupiedSandbox("pod-err", "claimed")
	boom := errors.New("apiserver unavailable")
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(sb).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*lennyv1.SandboxClaim); ok {
					return boom
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}).Build()
	err := reconcileForError(c)
	if err == nil {
		t.Fatal("a transient claim-Get error must propagate, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error chain = %v, want it to wrap %v", err, boom)
	}
}

// TestOccupancyReconcilePropagatesPatchError covers the patchPhase error
// branch: a non-conflict failure applying the projected coarse phase is
// wrapped with the sandbox name and returned, so the projection failure
// surfaces to the work queue rather than being silently dropped.
//
// spec: 4.6.1 (occupancy projection), 4.6.3 (WPC SSA-patches Sandbox.status).
func TestOccupancyReconcilePropagatesPatchError(t *testing.T) {
	s := newScheme(t)
	// A bound claim on a claimed pod projects claimed; seed an idle pod so the
	// projection differs from the current phase and patchPhase is reached.
	sb := occupiedSandbox("pod-err", "idle")
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-err", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "pod-err", TenantID: "acme"},
		Status:     lennyv1.SandboxClaimStatus{Phase: "bound"},
	}
	boom := errors.New("status patch denied")
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(sb, claim).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*lennyv1.Sandbox); ok {
					return boom
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	err := reconcileForError(c)
	if err == nil {
		t.Fatal("a non-conflict status patch failure must propagate, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error chain = %v, want it to wrap %v", err, boom)
	}
}
