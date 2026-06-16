// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

const reclaimNS = "lenny-agents"

// reclaimScheme registers lenny.dev/v1alpha1 so the fake client reads and
// deletes the SandboxClaim and Sandbox the orphan GC reclaims.
func reclaimScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// orphanClaim builds a per-pod claim in a binding state with its transition or
// hold stamps, the inputs the §4.6.1 reclaim predicate keys on.
func orphanClaim(name, pod string, st lennyv1.SandboxClaimStatus) *lennyv1.SandboxClaim {
	cl := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: reclaimNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: pod, TenantID: "acme"},
	}
	cl.Status = st
	return cl
}

// gcAtHour returns a GC pinned an hour ahead so a freshly stamped claim is past
// the orphan window, with no active session for any pod.
func gcAtHour(c client.Client) *ClaimGarbageCollector {
	return &ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubLookup{},
		Namespaces: []string{reclaimNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
}

// TestEvaluateSkipsClaimBeingDeleted pins the §4.6.1 guard that a claim already
// carrying a deletion timestamp is left untouched: the API server is tearing it
// down, so the GC must not race that teardown with a second reclaim.
//
// spec: 4.6.1 (orphan GC leaves a claim already being deleted)
func TestEvaluateSkipsClaimBeingDeleted_spec_4_6_1(t *testing.T) {
	cl := orphanClaim("claim-1", "pod-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})
	now := metav1.Now()
	cl.DeletionTimestamp = &now
	cl.Finalizers = []string{"lenny.dev/test-hold"} // a deletion timestamp requires a finalizer

	c := ctrlfake.NewClientBuilder().WithScheme(reclaimScheme(t)).
		WithObjects(cl).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).
		Build()
	g := gcAtHour(c)
	// evaluate must return nil and perform no reclaim for a claim being deleted.
	if err := g.evaluate(context.Background(), cl, g.Now()); err != nil {
		t.Fatalf("evaluate of a deleting claim = %v, want nil", err)
	}
}

// TestEvaluateSkipsWhenSessionLookupFails pins the §4.6.1 fail-closed posture:
// when the Postgres active-session oracle errors, the candidate is skipped so a
// transient lookup failure never deletes a claim that might back a live session.
//
// spec: 4.6.1 (the GC reclaims only once Postgres confirms no live session)
func TestEvaluateSkipsWhenSessionLookupFails_spec_4_6_1(t *testing.T) {
	cl := orphanClaim("claim-1", "pod-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})
	c := ctrlfake.NewClientBuilder().WithScheme(reclaimScheme(t)).
		WithObjects(cl).WithStatusSubresource(&lennyv1.SandboxClaim{}).Build()
	g := gcAtHour(c)
	g.Sessions = erroringLookup{}
	if err := g.evaluate(context.Background(), cl, g.Now()); err != nil {
		t.Fatalf("evaluate with a failing lookup = %v, want nil (skip)", err)
	}
	// The claim must survive: the lookup failure must not delete it.
	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: reclaimNS, Name: "claim-1"}, &got); err != nil {
		t.Fatalf("claim must survive a lookup failure: %v", err)
	}
}

// TestReclaimByDrainingPropagatesDrainError pins the §3.3 drain-before-delete
// order: when the Sandbox drain patch fails the claim is left intact and the
// error propagates, so a pod is never orphaned with its claim deleted but the
// pod still un-drained.
//
// spec: 3.3 (drain rather than return-to-idle; drain precedes the claim DELETE),
// 4.6.1 (reclaim by draining)
func TestReclaimByDrainingPropagatesDrainError_spec_3_3(t *testing.T) {
	cl := orphanClaim("claim-1", "pod-1", lennyv1.SandboxClaimStatus{Phase: string(claimstate.Bound)})
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: reclaimNS}}
	drainErr := errors.New("apiserver unreachable")
	c := ctrlfake.NewClientBuilder().WithScheme(reclaimScheme(t)).
		WithObjects(cl, sb).
		WithStatusSubresource(&lennyv1.SandboxClaim{}, &lennyv1.Sandbox{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
				return drainErr
			},
		}).Build()
	g := gcAtHour(c)
	if err := g.reclaimByDraining(context.Background(), cl); !errors.Is(err, drainErr) {
		t.Fatalf("reclaimByDraining error = %v, want it to wrap the drain failure", err)
	}
	// The claim must survive: the drain failed before the DELETE.
	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: reclaimNS, Name: "claim-1"}, &got); err != nil {
		t.Fatalf("claim must survive a drain failure: %v", err)
	}
}

// TestReclaimReservedToleratesAlreadyDeleted pins the §4.6.1 reserved reclaim
// idempotency: a claim the gateway's own hold-expiry DELETE or a prior sweep
// already removed reports no error, so a double reclaim is a no-op.
//
// spec: 4.6.1 (precondition-guarded hold-expiry DELETE), 3.2 (already-deleted
// is not an error)
func TestReclaimReservedToleratesAlreadyDeleted_spec_4_6_1(t *testing.T) {
	cl := orphanClaim("claim-gone", "pod-1", lennyv1.SandboxClaimStatus{Phase: string(claimstate.Reserved)})
	// The claim is not seeded into the client, so the precondition-guarded
	// DELETE hits NotFound.
	c := ctrlfake.NewClientBuilder().WithScheme(reclaimScheme(t)).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).Build()
	g := gcAtHour(c)
	if err := g.reclaimReserved(context.Background(), cl); err != nil {
		t.Fatalf("reclaimReserved of an already-deleted claim = %v, want nil", err)
	}
}

// TestReclaimReservedPropagatesUnexpectedDeleteError pins the §4.6.1 fail-loud
// path: a reserved-claim DELETE that fails for a reason other than NotFound or
// a precondition Conflict propagates, so a genuine API error is surfaced rather
// than silently swallowed.
//
// spec: 4.6.1 (precondition-guarded hold-expiry DELETE)
func TestReclaimReservedPropagatesUnexpectedDeleteError_spec_4_6_1(t *testing.T) {
	cl := orphanClaim("claim-1", "pod-1", lennyv1.SandboxClaimStatus{Phase: string(claimstate.Reserved)})
	internalErr := apierrors.NewInternalError(errors.New("etcd down"))
	c := ctrlfake.NewClientBuilder().WithScheme(reclaimScheme(t)).
		WithObjects(cl).WithStatusSubresource(&lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return internalErr
			},
		}).Build()
	g := gcAtHour(c)
	if err := g.reclaimReserved(context.Background(), cl); err == nil {
		t.Fatal("reclaimReserved returned nil on an internal DELETE error, want propagation")
	}
}

// erroringLookup is a §4.6.1 active-session oracle that always errors, the
// transient-failure case the GC must treat as fail-closed.
type erroringLookup struct{}

func (erroringLookup) PodHasActiveSession(context.Context, string) (bool, error) {
	return false, errors.New("postgres unavailable")
}
