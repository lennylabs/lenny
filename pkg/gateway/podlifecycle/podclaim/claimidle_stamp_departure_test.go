// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity)
//
// Claimer.Claim returns the StampPodTenant error and deletes nothing: the
// claim it created stays `bound`, and the claim-orphan GC reclaims it once
// no sibling reserved onto it and no non-terminal row names the pod. A
// delete here would drain a pod a same-tenant sibling may already have
// joined through the slot path.
func TestClaimIdleStampFailureLeavesBoundClaim_spec_4_6_1(t *testing.T) {
	ns := testNamespace(t)
	base := newEnvtestClient(t, sandboxIn(ns, testPool, "sbx-1", "idle"))
	mustCreate(t, base, agentPod(ns, "sbx-1", ""))
	stampErr := errors.New("injected tenant stamp failure")
	deletes := &atomic.Int32{}
	kube := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*corev1.Pod); ok {
				return stampErr
			}
			return c.Patch(ctx, obj, p, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				deletes.Add(1)
			}
			return c.Delete(ctx, obj, opts...)
		},
	})
	claimer := &podclaim.Claimer{Client: kube, Namespace: ns}

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if !errors.Is(err, stampErr) {
		t.Fatalf("Claim = %v, %v; want the tenant stamp error", claim, err)
	}
	if n := deletes.Load(); n != 0 {
		t.Errorf("claim deletes = %d, want none", n)
	}
	var stored lennyv1.SandboxClaim
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: podclaim.ClaimName("sbx-1")}, &stored); err != nil {
		t.Fatalf("the claim is gone: %v", err)
	}
	if stored.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound for the orphan GC", stored.Status.Phase)
	}
}
