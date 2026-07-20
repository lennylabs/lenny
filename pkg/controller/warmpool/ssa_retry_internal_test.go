// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
)

// ssaConflict builds an HTTP 409 conflict of the kind the API server returns
// when a controller applies a field owned by another SSA field manager.
func ssaConflict() error {
	return apierrors.NewConflict(
		schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"},
		"claude-worker-small",
		errors.New("Apply failed with 1 conflict: conflict with \"lenny-pool-scaling-controller\""),
	)
}

// TestUpdateStatusReReadsAndNeverForcesOnSSAConflict covers the two ownership
// invariants of the retry policy at a real call site (updateStatus applies the
// WarmPoolController-owned SandboxWarmPool.status fields via SSA): after a 409
// the controller issues a fresh GET before re-applying, and no apply carries
// Force ownership. Forcing would steal the field from the other manager and
// defeat the isolation the SSA boundary enforces.
//
// spec: §4.6 line 605 ("must discard its cached copy of the resource and issue
// a fresh GET"), line 606 ("Controllers must not use --force-conflicts (SSA
// Force: true)").
func TestUpdateStatusReReadsAndNeverForcesOnSSAConflict(t *testing.T) {
	s := reclaimScheme(t)
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: reclaimNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{MinWarm: 2, MaxWarm: 4},
		Status:     lennyv1.SandboxWarmPoolStatus{WarmCount: 0, ReadyCount: 0},
	}

	var events []string
	firstPatch := true
	forced := false
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(pool).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
					events = append(events, "get")
				}
				return cl.Get(ctx, key, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
					events = append(events, "patch")
					po := &client.SubResourcePatchOptions{}
					for _, o := range opts {
						o.ApplyToSubResourcePatch(po)
					}
					if po.Force != nil && *po.Force {
						forced = true
					}
					if firstPatch {
						firstPatch = false
						return ssaConflict()
					}
					// The fake client cannot execute SSA apply patches, so the
					// interceptor stands in for the apiserver and reports the
					// retry apply as accepted.
					return nil
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).Build()

	r := &Reconciler{Client: c, Scheme: s}
	decision := plan.Plan{WarmCount: 2, ReadyCount: 1}
	if err := r.updateStatus(context.Background(), pool, decision); err != nil {
		t.Fatalf("updateStatus = %v, want nil after one conflict then success", err)
	}

	if forced {
		t.Fatal("an SSA apply carried Force ownership; the policy forbids --force-conflicts in reconciliation")
	}
	// Expect: patch (409) -> get (re-read) -> patch (success).
	if len(events) < 3 {
		t.Fatalf("event sequence = %v, want at least one retry (patch, get, patch)", events)
	}
	if events[0] != "patch" {
		t.Fatalf("first event = %q, want %q", events[0], "patch")
	}
	sawReReadBeforeRetry := false
	for i := 1; i < len(events); i++ {
		if events[i] == "patch" && contains(events[:i], "get") {
			sawReReadBeforeRetry = true
			break
		}
	}
	if !sawReReadBeforeRetry {
		t.Fatalf("event sequence = %v, want a GET (re-read) before the retry patch", events)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
