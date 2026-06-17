// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	sandboxcond "github.com/lennylabs/lenny/pkg/sandbox/condition"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// stubSessions is a fake §4.6.1 active-session oracle keyed on the pod
// (sandboxRef). The per-pod claim (§4.6.3) carries no session identifier,
// so the GC keys the active-session check on the pod through the Postgres
// pod_assignment binding.
type stubSessions struct {
	active map[string]bool
	err    error
}

func (s stubSessions) PodHasActiveSession(_ context.Context, sandboxRef string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.active[sandboxRef], nil
}

func claim(name, sandboxRef string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: lennyv1.SandboxClaimSpec{
			SandboxRef: sandboxRef,
			TenantID:   "acme",
		},
	}
}

// seedClaimFullStatus stamps the full binding-state status on a created claim
// via the same gateway SSA status patch the gateway uses, so the GC observes a
// claim in a real binding state with its orphan-key timestamps. The API server
// rejects status on Create, so the status is written as a follow-up patch. It
// keys on the explicit claim name (the GC tests name claims directly) rather
// than deriving claim-<podName>.
func seedClaimFullStatus(t *testing.T, c client.Client, name string, st lennyv1.SandboxClaimStatus) {
	t.Helper()
	patch := &lennyv1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "SandboxClaim",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
	}
	patch.Status = st
	if err := c.Status().Patch(context.Background(), patch, client.Apply, client.FieldOwner(string(ownership.Gateway))); err != nil {
		t.Fatalf("seed claim status %s: %v", name, err)
	}
}

func claimedSandbox(name string) *lennyv1.Sandbox {
	sb := idleSandbox(name)
	sb.Status.Phase = "claimed"
	return sb
}

func reservedSandbox(name string) *lennyv1.Sandbox {
	sb := idleSandbox(name)
	sb.Status.Phase = "reserved"
	return sb
}

func getClaim(t *testing.T, c client.Client, name string) (lennyv1.SandboxClaim, bool) {
	t.Helper()
	var got lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &got)
	if err != nil {
		return lennyv1.SandboxClaim{}, false
	}
	return got, true
}

func gcSweep(t *testing.T, g *warmpool.ClaimGarbageCollector) {
	t.Helper()
	if err := g.SweepForTest(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

// gcForSweep returns a GC pinned an hour ahead so a just-created claim is past
// the orphan window, with no active session for any pod.
func gcForSweep(c client.Client) *warmpool.ClaimGarbageCollector {
	return &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{active: map[string]bool{}},
		Namespaces: []string{testNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
}

// diagnosis: a failure means the binding-state-aware orphan GC reclaims a
// live `bound` claim by draining the pod (not returning it to idle), so an
// unscrubbed occupied pod whose gateway crashed is retired rather than
// re-pooled, preserving the scrub-before-idle invariant.
// spec: 4.6.1 (live binding states reclaimed by draining), 3.3 (drain rather
// than return-to-idle; fail-closed).
func TestGCDrainsOrphanedBoundClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "pod-1"))
	// A bound claim whose binding-state transition is well past the orphan
	// timeout (the GC's Now is pinned an hour ahead).
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	gcSweep(t, gcForSweep(c))

	if _, ok := getClaim(t, c, "claim-1"); ok {
		t.Fatal("expected orphaned bound claim to be deleted")
	}
	if phase := sandboxPhase(t, c, "pod-1"); phase != "draining" {
		t.Errorf("sandbox phase = %q, want draining (live state drains)", phase)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, sandboxcond.OrphanClaimReclaimed); cond == nil {
		t.Errorf("missing %s condition; have %v", sandboxcond.OrphanClaimReclaimed, sb.Status.Conditions)
	}
}

// diagnosis: a failure means a `recycling` claim left with no holdExpiresAt
// (a coordinating-gateway crash during the scrub wait) is not reclaimed by
// draining, so a pod stuck mid-scrub is stranded forever.
// spec: 6.10 (recycling claim with no holdExpiresAt reclaimed by draining),
// 4.6.1 (live binding states), 3.3 (drain, fail-closed on gateway crash).
func TestGCDrainsOrphanedRecyclingClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "pod-1"))
	// recycling with no holdExpiresAt and no rewarmStartedAt: the
	// coordinating-gateway-crash case the reserved predicate cannot reach.
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Recycling),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	gcSweep(t, gcForSweep(c))

	if _, ok := getClaim(t, c, "claim-1"); ok {
		t.Fatal("expected orphaned recycling claim to be deleted")
	}
	if phase := sandboxPhase(t, c, "pod-1"); phase != "draining" {
		t.Errorf("sandbox phase = %q, want draining (recycling drains)", phase)
	}
}

// diagnosis: a failure means a reserved claim whose holder crashed is not
// reclaimed after holdExpiresAt plus the grace, so a scrubbed pod is never
// returned to the pool; or it is reclaimed by draining rather than by the
// precondition-guarded DELETE that returns it to idle.
// spec: 4.6.1 (reserved reclaimed by precondition-guarded DELETE after
// holdExpiresAt plus grace), 3.2 (precondition-guarded DELETE).
func TestGCReclaimsReservedClaimToIdle(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, reservedSandbox("pod-1"), claim("claim-1", "pod-1"))
	// holdExpiresAt in the past; the GC's Now is pinned an hour ahead, so
	// holdExpiresAt plus the default grace has passed.
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Reserved),
		HoldExpiresAt:              &metav1.Time{Time: time.Now()},
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	g := gcForSweep(c)
	g.ReservedHoldGrace = 10 * time.Second
	gcSweep(t, g)

	if _, ok := getClaim(t, c, "claim-1"); ok {
		t.Fatal("expected orphaned reserved claim to be deleted")
	}
	// The reserved → idle edge is the occupancy projection's. The GC only
	// deletes the claim with preconditions; project the resulting phase to
	// confirm the pod returns to idle rather than draining.
	reconcileOccupancy(t, c, "pod-1")
	if phase := sandboxPhase(t, c, "pod-1"); phase != "idle" {
		t.Errorf("sandbox phase = %q, want idle (reserved hold expiry returns to pool)", phase)
	}
}

// diagnosis: a failure means an empty-status claim (the CREATE-before-status
// crash window) older than claimOrphanTimeout is not reclaimed by draining,
// so a gateway that crashed between the claim CREATE and its first status
// patch strands the pod.
// spec: 4.6.1 (unset binding state CREATE-before-status fallback keyed on
// creationTimestamp, reclaimed by draining).
func TestGCDrainsEmptyStatusClaimByCreationTimestamp(t *testing.T) {
	s := newScheme(t)
	// An idle pod is the faithful CREATE-before-status residue: the gateway
	// CREATEd the claim but crashed before the first status patch, so the
	// occupancy projection never moved the pod off its warm-fill idle phase.
	// The fail-closed reclaim drains it anyway, because the controller cannot
	// know whether the crashed gateway scrubbed or occupied the pod.
	c := newClient(t, s, idleSandbox("pod-1"), claim("claim-1", "pod-1"))
	// No status patch: the claim carries empty status. The GC's Now is pinned
	// an hour ahead, so the server-stamped creationTimestamp is past the
	// orphan timeout.

	gcSweep(t, gcForSweep(c))

	if _, ok := getClaim(t, c, "claim-1"); ok {
		t.Fatal("expected orphaned empty-status claim to be deleted")
	}
	if phase := sandboxPhase(t, c, "pod-1"); phase != "draining" {
		t.Errorf("sandbox phase = %q, want draining (empty-status fallback drains, fail-closed from idle)", phase)
	}
}

// diagnosis: a failure means a reserved-claim reclaim does not carry the
// precondition that lets a concurrent rebind win the race, so the GC would
// delete a claim a gateway replica rebound after the sweep listed it.
// spec: 3.2 (rebind-vs-hold-expiry race: a rebind that lands first aborts the
// reclaim), 4.6.1 (precondition-guarded DELETE).
func TestGCReservedReclaimAbortsOnRebindRace(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, reservedSandbox("pod-1"), claim("claim-1", "pod-1"))
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:         string(claimstate.Reserved),
		HoldExpiresAt: &metav1.Time{Time: time.Now()},
	})
	// Simulate a cross-replica rebind landing after the sweep would have
	// listed the claim: re-stamp the claim status, bumping its resourceVersion.
	// The GC's DELETE carries the resourceVersion observed at its own list, so
	// the live resourceVersion differs and the precondition must fail.
	stale, ok := getClaim(t, c, "claim-1")
	if !ok {
		t.Fatal("claim must exist before the race")
	}
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:         string(claimstate.Bound),
		HoldExpiresAt: &metav1.Time{Time: time.Now()},
	})

	g := gcForSweep(c)
	g.ReservedHoldGrace = 10 * time.Second
	// Reclaim using the stale-resourceVersion view the sweep observed.
	if err := g.ReclaimReservedForTest(context.Background(), &stale); err != nil {
		t.Fatalf("reclaim reserved: %v", err)
	}
	if _, ok := getClaim(t, c, "claim-1"); !ok {
		t.Fatal("rebound claim must survive a precondition-failed reclaim")
	}
}

// diagnosis: a failure means the GC leaves a non-terminal or empty-status
// orphaned claim unreclaimed, so the per-pod GC does not match the shipped
// GC's phase-agnostic, status-independent coverage and a crash in any live
// binding state can strand a pod.
// spec: 4.6.1 (every non-terminal binding state and the empty-status window
// reclaimed), 3.3 (drain for live states), 3.2 (reserved DELETE).
func TestGCLeavesNoNonTerminalOrEmptyClaimUnreclaimed(t *testing.T) {
	s := newScheme(t)
	c := newClient(
		t, s,
		claimedSandbox("pod-bound"), claim("claim-bound", "pod-bound"),
		claimedSandbox("pod-recycling"), claim("claim-recycling", "pod-recycling"),
		reservedSandbox("pod-reserved"), claim("claim-reserved", "pod-reserved"),
		claimedSandbox("pod-empty"), claim("claim-empty", "pod-empty"),
	)
	now := time.Now()
	seedClaimFullStatus(t, c, "claim-bound", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: now},
	})
	seedClaimFullStatus(t, c, "claim-recycling", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Recycling),
		BindingStateTransitionTime: &metav1.Time{Time: now},
	})
	seedClaimFullStatus(t, c, "claim-reserved", lennyv1.SandboxClaimStatus{
		Phase:         string(claimstate.Reserved),
		HoldExpiresAt: &metav1.Time{Time: now},
	})
	// claim-empty: no status patch (the CREATE-before-status window).

	g := gcForSweep(c)
	g.ReservedHoldGrace = 10 * time.Second
	gcSweep(t, g)

	for _, name := range []string{"claim-bound", "claim-recycling", "claim-reserved", "claim-empty"} {
		if cl, ok := getClaim(t, c, name); ok {
			t.Errorf("claim %s (phase %q) left unreclaimed", name, cl.Status.Phase)
		}
	}
}

// diagnosis: a failure means a terminal-disposition claim (`released` or
// `failed`) is treated as an orphan and deleted by the GC, racing the
// occupancy projection's drain-then-terminate retirement.
// spec: 4.6.1 (terminal dispositions are not orphans; the projection retires
// them).
func TestGCSkipsTerminalDispositionClaims(t *testing.T) {
	s := newScheme(t)
	c := newClient(
		t, s,
		claimedSandbox("pod-released"), claim("claim-released", "pod-released"),
		claimedSandbox("pod-failed"), claim("claim-failed", "pod-failed"),
	)
	now := time.Now()
	seedClaimFullStatus(t, c, "claim-released", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Released),
		BindingStateTransitionTime: &metav1.Time{Time: now},
	})
	seedClaimFullStatus(t, c, "claim-failed", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Failed),
		BindingStateTransitionTime: &metav1.Time{Time: now},
	})

	gcSweep(t, gcForSweep(c))

	for _, name := range []string{"claim-released", "claim-failed"} {
		if _, ok := getClaim(t, c, name); !ok {
			t.Errorf("terminal-disposition claim %s must not be reclaimed by the GC", name)
		}
	}
}

// spec: 4.6.1 — a claim whose pod has an active session is never reclaimed,
// keyed on the pod through the Postgres pod_assignment binding.
func TestGCKeepsClaimWithActiveSession(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "pod-1"))
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	g := &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{active: map[string]bool{"pod-1": true}},
		Namespaces: []string{testNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
	gcSweep(t, g)

	if _, ok := getClaim(t, c, "claim-1"); !ok {
		t.Fatal("claim with an active session must survive")
	}
}

// spec: 4.6.1 — a claim whose orphan key is younger than claimOrphanTimeout
// is not yet a candidate, even with no session.
func TestGCSkipsYoungClaim(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "pod-1"))
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	g := &warmpool.ClaimGarbageCollector{
		Client:        c,
		Sessions:      stubSessions{active: map[string]bool{}},
		Namespaces:    []string{testNS},
		OrphanTimeout: time.Hour,
		Now:           func() time.Time { return time.Now() },
	}
	gcSweep(t, g)

	if _, ok := getClaim(t, c, "claim-1"); !ok {
		t.Fatal("young claim must survive")
	}
}

// spec: 4.6.1 — a session-lookup error must not delete the claim; the sweep
// skips the candidate and retries on the next tick (fail-closed).
func TestGCSkipsOnLookupError(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, claimedSandbox("pod-1"), claim("claim-1", "pod-1"))
	seedClaimFullStatus(t, c, "claim-1", lennyv1.SandboxClaimStatus{
		Phase:                      string(claimstate.Bound),
		BindingStateTransitionTime: &metav1.Time{Time: time.Now()},
	})

	g := &warmpool.ClaimGarbageCollector{
		Client:     c,
		Sessions:   stubSessions{err: context.DeadlineExceeded},
		Namespaces: []string{testNS},
		Now:        func() time.Time { return time.Now().Add(time.Hour) },
	}
	gcSweep(t, g)

	if _, ok := getClaim(t, c, "claim-1"); !ok {
		t.Fatal("claim must survive a lookup error")
	}
}

func TestGCNeedsLeaderElection(t *testing.T) {
	g := &warmpool.ClaimGarbageCollector{}
	if !g.NeedLeaderElection() {
		t.Error("orphan-claim GC must run only on the elected leader")
	}
}
