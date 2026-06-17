// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

const claimerInternalNS = "lenny-agents"

// internalScheme registers lenny.dev/v1alpha1 so the fake client reads the
// SandboxClaim the §3.2 rebind eligibility check inspects.
func internalScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// reservedClaim builds a per-pod claim seeded with a binding state, tenant pin,
// and hold deadline, the inputs the §3.2 reserved-hold eligibility check reads.
func reservedClaim(pod, tenant string, phase claimstate.State, holdExpiresAt *time.Time) *lennyv1.SandboxClaim {
	cl := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: ClaimName(pod), Namespace: claimerInternalNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: pod, TenantID: tenant},
	}
	cl.Status.Phase = string(phase)
	if holdExpiresAt != nil {
		cl.Status.HoldExpiresAt = &metav1.Time{Time: *holdExpiresAt}
	}
	return cl
}

// TestReservedClaimForTenantEligibility pins the §3.2 acquisition-path rebind
// eligibility check: a reserved claim pinned to the request tenant with a live
// hold is eligible, while a missing claim, a non-reserved binding state, a
// different tenant pin, and an expired or absent hold deadline are all
// ineligible (found is false), so the caller falls through to normal idle
// acquisition rather than rebinding a pod it must not reuse.
//
// spec: 3.2 (within-hold rebind, reserved pod held for its pinned tenant
// alone), 4.6.1 (reserved hold, holdExpiresAt), 5.2 (tenant pinning)
func TestReservedClaimForTenantEligibility_spec_3_2(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Second)
	past := now.Add(-1 * time.Second)

	cases := []struct {
		name      string
		claim     *lennyv1.SandboxClaim
		wantFound bool
	}{
		{"reserved, same tenant, live hold is eligible", reservedClaim("pod-1", "acme", claimstate.Reserved, &future), true},
		{"non-reserved binding state is ineligible", reservedClaim("pod-1", "acme", claimstate.Bound, &future), false},
		{"different tenant pin is ineligible", reservedClaim("pod-1", "globex", claimstate.Reserved, &future), false},
		{"expired hold is ineligible", reservedClaim("pod-1", "acme", claimstate.Reserved, &past), false},
		{"absent hold deadline is ineligible", reservedClaim("pod-1", "acme", claimstate.Reserved, nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Claimer{
				Client: fake.NewClientBuilder().WithScheme(internalScheme(t)).
					WithObjects(tc.claim).
					WithStatusSubresource(&lennyv1.SandboxClaim{}).
					Build(),
				Namespace: claimerInternalNS,
			}
			// Seed the status subresource, which the builder does not populate from
			// WithObjects for a status-subresource type.
			seedClaimStatus(t, c.Client, tc.claim)
			_, found, err := c.reservedClaimForTenant(context.Background(), "pod-1", "acme", now)
			if err != nil {
				t.Fatalf("reservedClaimForTenant: %v", err)
			}
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
		})
	}
}

// TestReservedClaimForTenantMissingClaimIsNotFound pins the §3.2 gone-claim
// race: when the per-pod claim no longer exists (a concurrent hold-expiry
// DELETE or the orphan GC reclaimed it) the eligibility check reports
// not-found rather than erroring, so acquisition falls through cleanly.
//
// spec: 3.2 (rebind falls through when the claim is gone), 4.6.1 (orphan GC)
func TestReservedClaimForTenantMissingClaimIsNotFound_spec_3_2(t *testing.T) {
	c := &Claimer{
		Client:    fake.NewClientBuilder().WithScheme(internalScheme(t)).Build(),
		Namespace: claimerInternalNS,
	}
	_, found, err := c.reservedClaimForTenant(context.Background(), "ghost", "acme", time.Now())
	if err != nil {
		t.Fatalf("reservedClaimForTenant on a gone claim: %v, want nil", err)
	}
	if found {
		t.Fatal("found = true for a gone claim, want false")
	}
}

// TestClaimByNameReReadsCurrentObject pins the §3.2 post-rebind re-read: after
// the rebind patch the caller re-reads the claim so it dispatches against the
// current object, and a re-read of a vanished claim returns a NotFound the
// caller maps to the fall-through path.
//
// spec: 3.2 (the rebinding replica re-reads the claim after its patch before
// dispatching)
func TestClaimByNameReReadsCurrentObject_spec_3_2(t *testing.T) {
	claim := reservedClaim("pod-2", "acme", claimstate.Bound, nil)
	c := &Claimer{
		Client: fake.NewClientBuilder().WithScheme(internalScheme(t)).
			WithObjects(claim).Build(),
		Namespace: claimerInternalNS,
	}
	got, err := c.claimByName(context.Background(), ClaimName("pod-2"))
	if err != nil {
		t.Fatalf("claimByName: %v", err)
	}
	if got.Spec.SandboxRef != "pod-2" {
		t.Fatalf("re-read claim sandboxRef = %q, want pod-2", got.Spec.SandboxRef)
	}
	if _, err := c.claimByName(context.Background(), ClaimName("ghost")); err == nil {
		t.Fatal("claimByName on a gone claim returned nil error, want NotFound")
	}
}

// seedClaimStatus copies a seeded claim's Status onto the fake client's status
// subresource so reservedClaimForTenant reads the binding state and hold
// deadline the test set, which WithObjects does not project for a
// status-subresource type.
func seedClaimStatus(t *testing.T, cl client.Client, seed *lennyv1.SandboxClaim) {
	t.Helper()
	var cur lennyv1.SandboxClaim
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: seed.Namespace, Name: seed.Name}, &cur); err != nil {
		t.Fatalf("get seeded claim: %v", err)
	}
	cur.Status = seed.Status
	if err := cl.Status().Update(context.Background(), &cur); err != nil {
		t.Fatalf("seed claim status: %v", err)
	}
}
