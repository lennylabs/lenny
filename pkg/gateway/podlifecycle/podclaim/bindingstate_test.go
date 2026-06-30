// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// fixedClock returns a now() func that always reports t, so a status-stamp
// assertion is deterministic across the SSA round trip.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// seedBoundClaim creates a per-pod SandboxClaim and writes its first `bound`
// binding state, returning a client wired to the envtest API server. It is
// the common precondition for the recycle-path writer tests.
func seedBoundClaim(t *testing.T, name string) (client.WithWatch, string) {
	t.Helper()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "acme"},
	}
	c := newEnvtestClient(t, claim)
	if err := podclaim.WriteBoundStatus(context.Background(), c, testNS, name); err != nil {
		t.Fatalf("seed bound status: %v", err)
	}
	return c, name
}

// getClaim reads the named claim off the envtest API server.
func getClaim(t *testing.T, c client.Client, name string) *lennyv1.SandboxClaim {
	t.Helper()
	var got lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got); err != nil {
		t.Fatalf("get claim %s: %v", name, err)
	}
	return &got
}

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

// TestRecyclePathBindingStateTransitions_spec_4_6_3 walks the recycle-path
// binding-state machine bound → recycling → reserved → bound on the real
// envtest API server (SSA status PATCH, CRD enum validation) and pins each
// transition's status stamps: the binding-state transition time advances on
// every phase change, `rewarmStartedAt` is stamped on the preConnect re-warm
// and dropped at `reserved`, and `holdExpiresAt` is stamped at `reserved` and
// dropped on the rebind.
//
// diagnosis: a failure means a recycle-path binding-state write either
// stamped the wrong phase, skipped the transition-time stamp the orphan GC
// keys on, or left a stale holdExpiresAt/rewarmStartedAt that the §6.14
// projection would misread.
//
// spec: §4.6.1 (recycle binding states, reserved hold), §4.6.3 (binding-state
// enumeration, status stamps), §6.14 (recycling/reserved projection).
func TestRecyclePathBindingStateTransitions_spec_4_6_3(t *testing.T) {
	const name = "claim-sbx-1"
	c, _ := seedBoundClaim(t, name)
	ctx := context.Background()

	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	if err := podclaim.WriteRecyclingStatus(ctx, c, testNS, name, fixedClock(t1)); err != nil {
		t.Fatalf("WriteRecyclingStatus: %v", err)
	}
	got := getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Recycling) {
		t.Fatalf("phase = %q, want recycling", got.Status.Phase)
	}
	if got.Status.BindingStateTransitionTime == nil || !got.Status.BindingStateTransitionTime.Time.Equal(t1) {
		t.Errorf("recycling transition time = %v, want %v", got.Status.BindingStateTransitionTime, t1)
	}

	// preConnect re-warm begins: rewarmStartedAt is stamped, phase stays
	// recycling, the binding-state transition time is not churned.
	t2 := t1.Add(2 * time.Second)
	if err := podclaim.WriteRewarmStartedStatus(ctx, c, testNS, name, fixedClock(t2)); err != nil {
		t.Fatalf("WriteRewarmStartedStatus: %v", err)
	}
	got = getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Recycling) {
		t.Errorf("phase after rewarm stamp = %q, want recycling", got.Status.Phase)
	}
	if got.Status.RewarmStartedAt == nil || !got.Status.RewarmStartedAt.Time.Equal(t2) {
		t.Errorf("rewarmStartedAt = %v, want %v", got.Status.RewarmStartedAt, t2)
	}
	if got.Status.BindingStateTransitionTime == nil || !got.Status.BindingStateTransitionTime.Time.Equal(t1) {
		t.Errorf("rewarm stamp churned the binding-state transition time to %v, want %v", got.Status.BindingStateTransitionTime, t1)
	}

	// Re-warm completes: recycling → reserved stamps holdExpiresAt at
	// reservedAt + TTL, drops rewarmStartedAt, and returns the precondition
	// token.
	t3 := t2.Add(3 * time.Second)
	const holdTTL = 10 * time.Second
	hold, err := podclaim.WriteReservedStatus(ctx, c, testNS, name, holdTTL, fixedClock(t3))
	if err != nil {
		t.Fatalf("WriteReservedStatus: %v", err)
	}
	got = getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Reserved) {
		t.Fatalf("phase = %q, want reserved", got.Status.Phase)
	}
	wantHold := t3.Add(holdTTL)
	if got.Status.HoldExpiresAt == nil || !got.Status.HoldExpiresAt.Time.Equal(wantHold) {
		t.Errorf("holdExpiresAt = %v, want %v (reservedAt + TTL)", got.Status.HoldExpiresAt, wantHold)
	}
	if got.Status.RewarmStartedAt != nil {
		t.Errorf("rewarmStartedAt = %v, want dropped at reserved (re-warm watchdog no longer applies)", got.Status.RewarmStartedAt)
	}
	if !hold.HoldExpiresAt.Equal(wantHold) {
		t.Errorf("returned hold deadline = %v, want %v", hold.HoldExpiresAt, wantHold)
	}
	if hold.UID == "" || string(hold.UID) != string(got.UID) {
		t.Errorf("returned hold UID = %q, want claim UID %q", hold.UID, got.UID)
	}
	if hold.ResourceVersion == "" || hold.ResourceVersion != got.ResourceVersion {
		t.Errorf("returned hold resourceVersion = %q, want %q", hold.ResourceVersion, got.ResourceVersion)
	}

	// Same-tenant rebind within the hold window: reserved → bound drops
	// holdExpiresAt and advances the transition time.
	t4 := t3.Add(4 * time.Second)
	if err := podclaim.WriteRebindStatus(ctx, c, testNS, name, fixedClock(t4)); err != nil {
		t.Fatalf("WriteRebindStatus: %v", err)
	}
	got = getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Bound) {
		t.Errorf("phase after rebind = %q, want bound", got.Status.Phase)
	}
	if got.Status.HoldExpiresAt != nil {
		t.Errorf("holdExpiresAt = %v, want dropped on rebind", got.Status.HoldExpiresAt)
	}
	if got.Status.BindingStateTransitionTime == nil || !got.Status.BindingStateTransitionTime.Time.Equal(t4) {
		t.Errorf("rebind transition time = %v, want %v", got.Status.BindingStateTransitionTime, t4)
	}
}

// TestRewarmStartedRequiresRecyclingClaim_spec_4_6_3 asserts the
// fail-closed guard: stamping rewarmStartedAt on a claim that is not
// `recycling` is a caller bug and returns an error rather than stamping an
// unrelated binding state.
//
// diagnosis: a failure means WriteRewarmStartedStatus stamped the re-warm
// anchor on a non-recycling claim, which would arm the sdkConnectTimeout
// watchdog against a pod that is not re-warming.
//
// spec: §4.6.1 (rewarmStartedAt stamped only when the recycle disposition
// begins the SDK re-warm), §4.6.3.
func TestRewarmStartedRequiresRecyclingClaim_spec_4_6_3(t *testing.T) {
	const name = "claim-sbx-1"
	c, _ := seedBoundClaim(t, name)
	// The claim is `bound`, not `recycling`.
	if err := podclaim.WriteRewarmStartedStatus(context.Background(), c, testNS, name, nil); err == nil {
		t.Fatalf("WriteRewarmStartedStatus on a bound claim returned nil, want error")
	}
}

// TestWriteDispositionTerminalAndGuard_spec_4_6_3 pins the terminal
// disposition writer: it stamps `released`/`failed` on the claim status so
// the §6.14 projection drains then terminates the pod, and it rejects a
// non-terminal disposition rather than writing an out-of-enum value.
//
// diagnosis: a failure means the gateway wrote a non-terminal disposition (a
// pod the projection would never retire) or failed to record the terminal
// retirement the WarmPoolController keys the drain on.
//
// spec: §4.6.1 (terminal disposition), §4.6.3 (released/failed terminal),
// §6.14 (draining/terminated projection).
func TestWriteDispositionTerminalAndGuard_spec_4_6_3(t *testing.T) {
	const name = "claim-sbx-1"
	c, _ := seedBoundClaim(t, name)
	ctx := context.Background()

	tt := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	if err := podclaim.WriteDispositionStatus(ctx, c, testNS, name, claimstate.Released, fixedClock(tt)); err != nil {
		t.Fatalf("WriteDispositionStatus released: %v", err)
	}
	got := getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Released) {
		t.Errorf("phase = %q, want released", got.Status.Phase)
	}
	if got.Status.BindingStateTransitionTime == nil || !got.Status.BindingStateTransitionTime.Time.Equal(tt) {
		t.Errorf("disposition transition time = %v, want %v", got.Status.BindingStateTransitionTime, tt)
	}

	// A non-terminal disposition is a caller bug: fail closed.
	if err := podclaim.WriteDispositionStatus(ctx, c, testNS, name, claimstate.Bound, nil); err == nil {
		t.Error("WriteDispositionStatus(bound) returned nil, want error (bound is not terminal)")
	}
}

// TestHoldExpiryDeleteHonorsPreconditions_spec_4_6_1 pins the
// precondition-guarded hold-expiry DELETE against the real API server: a
// DELETE carrying the UID and resourceVersion observed at the `reserved`
// patch succeeds and returns the pod (claim gone), and a repeat DELETE on the
// already-deleted claim is a no-op rather than an error.
//
// diagnosis: a failure means the hold-expiry DELETE dropped its UID/
// resourceVersion preconditions (so it could delete a rebound claim) or
// errored on an already-reclaimed claim.
//
// spec: §4.6.1 (precondition-guarded hold-expiry DELETE).
func TestHoldExpiryDeleteHonorsPreconditions_spec_4_6_1(t *testing.T) {
	const name = "claim-sbx-1"
	c, _ := seedBoundClaim(t, name)
	ctx := context.Background()
	if err := podclaim.WriteRecyclingStatus(ctx, c, testNS, name, nil); err != nil {
		t.Fatalf("recycling: %v", err)
	}
	hold, err := podclaim.WriteReservedStatus(ctx, c, testNS, name, 10*time.Second, nil)
	if err != nil {
		t.Fatalf("reserved: %v", err)
	}

	aborted, err := podclaim.DeleteOnHoldExpiry(ctx, c, testNS, name, hold)
	if err != nil {
		t.Fatalf("DeleteOnHoldExpiry: %v", err)
	}
	if aborted {
		t.Fatal("DeleteOnHoldExpiry reported aborted, want delete (no rebind raced)")
	}
	var got lennyv1.SandboxClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: name}, &got); err == nil {
		t.Fatal("claim still present after hold-expiry delete")
	}
	// Idempotent: a repeat expiry on the already-deleted claim is a no-op.
	aborted, err = podclaim.DeleteOnHoldExpiry(ctx, c, testNS, name, hold)
	if err != nil {
		t.Fatalf("DeleteOnHoldExpiry repeat: %v", err)
	}
	if aborted {
		t.Error("repeat DeleteOnHoldExpiry reported aborted, want no-op on a missing claim")
	}
}

// TestRebindAbortsHoldExpiry_spec_3_2 is the rebind-vs-hold-expiry race: a
// `reserved → bound` rebind lands first (any gateway replica may rebind),
// changing the resourceVersion, so the hold-expiry DELETE fenced on the
// reserved-patch version fails its precondition, aborts, and leaves the
// rebound claim intact. This is the optimistic-concurrency guarantee that a
// rebind that lands first wins the race.
//
// diagnosis: a failure means a hold-expiry DELETE deleted a claim a
// concurrent rebind had already re-bound, dropping the pod out from under a
// live session — the exact double-claim/lost-rebind the precondition exists
// to prevent.
//
// spec: §4.6.1 (precondition-guarded DELETE), §4.6.3 (reserved → bound
// rebind), §3.2 (rebind-vs-hold-expiry race).
func TestRebindAbortsHoldExpiry_spec_3_2(t *testing.T) {
	const name = "claim-sbx-1"
	c, _ := seedBoundClaim(t, name)
	ctx := context.Background()
	if err := podclaim.WriteRecyclingStatus(ctx, c, testNS, name, nil); err != nil {
		t.Fatalf("recycling: %v", err)
	}
	hold, err := podclaim.WriteReservedStatus(ctx, c, testNS, name, 10*time.Second, nil)
	if err != nil {
		t.Fatalf("reserved: %v", err)
	}

	// A same-tenant session rebinds the claim before the hold expires; the
	// rebind status patch changes the resourceVersion the expiry is fenced on.
	if err := podclaim.WriteRebindStatus(ctx, c, testNS, name, nil); err != nil {
		t.Fatalf("WriteRebindStatus: %v", err)
	}

	aborted, err := podclaim.DeleteOnHoldExpiry(ctx, c, testNS, name, hold)
	if err != nil {
		t.Fatalf("DeleteOnHoldExpiry after rebind: %v", err)
	}
	if !aborted {
		t.Fatal("DeleteOnHoldExpiry did not abort after a rebind changed the resourceVersion")
	}
	// The rebound claim is intact and bound.
	got := getClaim(t, c, name)
	if got.Status.Phase != string(claimstate.Bound) {
		t.Errorf("phase after aborted expiry = %q, want bound (rebind preserved)", got.Status.Phase)
	}
}
