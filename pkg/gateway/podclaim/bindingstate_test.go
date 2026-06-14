// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// TestWriteBoundStatusUsesPatchNotUpdate_spec_4_6_3 pins the §4.6.3 design
// contract that the gateway writes the first `bound` binding state on the
// per-pod SandboxClaim via the status-subresource PATCH verb, never a PUT
// (`update`). The chart grants the gateway `get`/`patch` on
// `sandboxclaims/status` and no `update` verb (§6.13 RBAC), so a regression
// to client.Status().Update — which issues an HTTP PUT the API server
// authorizes against `update` — would be denied Forbidden in any real
// cluster while passing the envtest suite (which does not enforce RBAC). The
// write runs against the real envtest apiserver (SSA apply is a PATCH on the
// wire) through an interceptor that records every status-subresource verb, so
// the test asserts a Patch occurred and no Update did.
//
// diagnosis: a failure means the gateway's binding-state write reverted to a
// status PUT/update, which the scoped sandboxclaims/status RBAC grant denies.
//
// spec: §4.6.1 (first `bound` status patch); §4.6.3 (gateway granted
// `patch`, not `update`, on sandboxclaims/status).
func TestWriteBoundStatusUsesPatchNotUpdate_spec_4_6_3(t *testing.T) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sbx-1", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "acme"},
	}
	base := newEnvtestClient(t, claim)

	var patches, updates int
	c := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sr string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if sr == "status" {
				patches++
			}
			return cl.Status().Patch(ctx, obj, patch, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sr string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if sr == "status" {
				updates++
			}
			return cl.Status().Update(ctx, obj, opts...)
		},
	})

	if err := podclaim.WriteBoundStatus(context.Background(), c, testNS, "claim-sbx-1"); err != nil {
		t.Fatalf("WriteBoundStatus: %v", err)
	}

	if patches != 1 {
		t.Errorf("status PATCH count = %d, want 1 (the gateway must write the binding state with a PATCH)", patches)
	}
	if updates != 0 {
		t.Errorf("status UPDATE count = %d, want 0 (a PUT requires the `update` verb the gateway is not granted)", updates)
	}

	var got lennyv1.SandboxClaim
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-1"}, &got); err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound", got.Status.Phase)
	}
	if got.Status.BindingStateTransitionTime == nil {
		t.Error("binding-state transition time not stamped")
	}
}

// TestWriteBoundStatusIdempotentOnAlreadyBound_spec_4_6_1 asserts the
// already-`bound` short-circuit: a second WriteBoundStatus on a claim that is
// already bound issues no further status write, so a retry after a partial
// success neither errors nor re-stamps the transition time.
//
// diagnosis: a failure means the idempotency guard regressed and the gateway
// re-stamps an already-bound claim, churning the binding-transition time the
// orphan GC keys its reclaim predicate on.
//
// spec: §4.6.1 (idempotent first `bound` status patch).
func TestWriteBoundStatusIdempotentOnAlreadyBound_spec_4_6_1(t *testing.T) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sbx-1", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "acme"},
	}
	base := newEnvtestClient(t, claim)
	// Establish the bound state, then count writes on the second call.
	if err := podclaim.WriteBoundStatus(context.Background(), base, testNS, "claim-sbx-1"); err != nil {
		t.Fatalf("seed bound status: %v", err)
	}

	var patches int
	c := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sr string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if sr == "status" {
				patches++
			}
			return cl.Status().Patch(ctx, obj, patch, opts...)
		},
	})

	if err := podclaim.WriteBoundStatus(context.Background(), c, testNS, "claim-sbx-1"); err != nil {
		t.Fatalf("WriteBoundStatus on already-bound claim: %v", err)
	}
	if patches != 0 {
		t.Errorf("status PATCH count = %d, want 0 (an already-bound claim is a no-op)", patches)
	}
}
