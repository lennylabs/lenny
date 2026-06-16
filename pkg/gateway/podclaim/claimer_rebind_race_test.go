// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// claimResource is the SandboxClaim resource the rebind status patch targets;
// the interceptor returns NotFound for it to simulate a concurrent expiry
// DELETE reclaiming the claim between the eligibility read and the rebind
// patch.
var claimResource = schema.GroupResource{Group: lennyv1.GroupVersion.Group, Resource: "sandboxclaims"}

// TestClaimRebindFallsThroughWhenHoldExpiresMidRebind covers the §3.2
// rebind-vs-hold-expiry race in the acquisition path: a reserved pod looks
// eligible for a within-hold rebind, but its hold-expiry DELETE lands between
// the eligibility read and the rebind status patch, so WriteRebindStatus sees
// NotFound. The claimer treats the vanished claim as not-rebound and falls
// through to a fresh idle acquisition rather than surfacing the race as an
// error.
//
// spec: 3.2 (within-hold rebind aborts cleanly when the hold-expiry DELETE
// wins), 4.6.1 (reserved hold).
//
// diagnosis: a failure means the acquisition-path rebind does not tolerate a
// concurrent hold-expiry DELETE: it either surfaces the NotFound as a hard
// claim error (so a racing client sees a spurious failure) or stalls on the
// vanished reserved pod instead of acquiring a fresh idle one.
func TestClaimRebindFallsThroughWhenHoldExpiresMidRebind_spec_3_2(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	base := newEnvtestClient(
		t,
		reservedSandbox("sbx-held", "acme"),
		sandboxIn(testPool, "sbx-idle", "idle"),
	)
	seedReservedClaim(t, base, "sbx-held", "acme", now, 30*time.Second)

	// Wrap the live client: the rebind status patch on the held pod's claim
	// returns NotFound (the concurrent expiry DELETE won the race). Every other
	// SandboxClaim status patch, including the fresh idle pod's bound-status
	// write, passes through, so the fall-through acquisition succeeds.
	heldClaim := podclaim.ClaimName("sbx-held")
	raced := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok && obj.GetName() == heldClaim {
				return apierrors.NewNotFound(claimResource, obj.GetName())
			}
			return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	})

	claimer := &podclaim.Claimer{
		Client: raced, Namespace: testNS,
		Now: func() time.Time { return now.Add(5 * time.Second) }, // within the hold
	}
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-race", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: the rebind-vs-expiry race must fall through, got %v", err)
	}
	// The held pod's rebind lost to the expiry DELETE, so the claimer advanced
	// to the fresh idle pod rather than surfacing the race as an error.
	if claim.Spec.SandboxRef != "sbx-idle" {
		t.Errorf("acquired %q, want sbx-idle (fall-through past the vanished hold)", claim.Spec.SandboxRef)
	}
}
