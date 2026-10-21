// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component coverage for the occupancy-zero disposition of
// SlotClaimer.ReleaseSlot against a real kube-apiserver and a Redis slot set.
//
// The per-pod claim of a concurrent-workspace pod spans the whole occupancy
// episode. Only the release that removes a session's member and leaves the
// set empty disposes of it: a release that leaves a sibling, a duplicate
// release that removes nothing, and a leaked release all leave it in place.
// On a recycling pool that disposition is the `recycling` patch; a release
// that ends the occupancy without reuse, such as the release of a binding
// that was never published, deletes the claim instead.
//
// spec: §5.2 (Slot assignment atomicity; Recycle lifecycle), §4.6.3
// (SandboxClaim: a release that ends the occupancy without reuse), §6.2
// (leaked slot semantics).
package slotrelease_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// slotClaimer returns the claimer over the fixture's apiserver and Redis set,
// as Binder.slotClaimer builds it.
func (f *releaseFixture) slotClaimer() *podclaim.SlotClaimer {
	return &podclaim.SlotClaimer{Client: f.kube, Namespace: releaseNS, Counter: f.binder.SlotCounter}
}

// reserve claims one slot for sessionID on the fixture's pod.
func (f *releaseFixture) reserve(t *testing.T, sessionID string) {
	t.Helper()
	res, err := f.slotClaimer().ClaimSlot(context.Background(), podclaim.SlotRequest{
		Pool: releasePool, SessionID: sessionID, TenantID: "acme", MaxConcurrentSessions: releaseMaxConcurrent,
	})
	if err != nil || res.SandboxName != releasePod {
		t.Fatalf("reserve %s: %+v, %v", sessionID, res, err)
	}
}

// release runs one ReleaseSlot and fails on an error.
func (f *releaseFixture) release(t *testing.T, sessionID string, recycle, leaked bool) bool {
	t.Helper()
	recycled, err := f.slotClaimer().ReleaseSlot(context.Background(), releasePod, sessionID, recycle, leaked)
	if err != nil {
		t.Fatalf("ReleaseSlot %s: %v", sessionID, err)
	}
	return recycled
}

// spec: §5.2 (Slot assignment atomicity), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse), §6.2 (leaked slot semantics)
// diagnosis: the per-pod claim was deleted by a release other than the one
// that removed the last member: a release that left a sibling, a duplicate
// release, or a leaked release deleted the shared claim and the warm pool
// controller drained the pod under live sessions. Or the removing release
// that emptied the set left the claim, so the pod never retires.
func TestReleaseSlotDeletesTheClaimOnlyOnTheEmptyingRemoval_spec_5_2(t *testing.T) {
	t.Run("sibling, duplicate, then the last removal", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.reserve(t, "sess-a")
		f.reserve(t, "sess-b")
		f.release(t, "sess-a", false, false)
		if got := f.claimPhase(t); got != string(claimstate.Bound) {
			t.Fatalf("claim after a release that left a sibling = %q, want bound", got)
		}
		f.release(t, "sess-a", false, false)
		if got := f.claimPhase(t); got != string(claimstate.Bound) {
			t.Fatalf("claim after a duplicate release = %q, want bound", got)
		}
		f.release(t, "sess-b", false, false)
		if f.claimExists(t) {
			t.Error("the claim survived the release that emptied the set")
		}
		// A duplicate of the emptying release finds no member and makes no
		// claim write, so it cannot touch a later occupancy's claim.
		f.reserve(t, "sess-c")
		f.release(t, "sess-b", false, false)
		if got := f.claimPhase(t); got != string(claimstate.Bound) {
			t.Errorf("a later occupancy's claim after a duplicate of the emptying release = %q, want bound", got)
		}
	})

	t.Run("a leaked last release keeps the claim", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.reserve(t, "sess-a")
		f.release(t, "sess-a", false, true)
		if got := f.claimPhase(t); got != string(claimstate.Bound) {
			t.Errorf("claim after a leaked last release = %q, want bound", got)
		}
		if got := f.activeSlots(t); got != 1 {
			t.Errorf("slot set cardinality = %d, want 1 (the leaked: member)", got)
		}
	})
}

// spec: §5.2 (Recycle lifecycle), §4.6.3 (SandboxClaim: a release that ends
// the occupancy without reuse)
// diagnosis: on a recycling concurrent pool, the release that emptied the set
// did not patch the claim `recycling`, so the whole-pod scrub never starts;
// or the release of a binding that was never published patched the claim
// `recycling` and handed an unscrubbed pod to the next session rather than
// retiring it.
func TestReleaseSlotRecyclesOrRetiresAtOccupancyZero_spec_5_2(t *testing.T) {
	t.Run("the last release on a recycling pool patches recycling", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.reserve(t, "sess-a")
		f.reserve(t, "sess-b")
		if f.release(t, "sess-a", true, false) {
			t.Fatal("a release that left a sibling reported the recycle edge")
		}
		if !f.release(t, "sess-b", true, false) {
			t.Error("the release that emptied the set did not report the recycle edge")
		}
		if got := f.claimPhase(t); got != string(claimstate.Recycling) {
			t.Errorf("claim = %q, want recycling", got)
		}
	})

	t.Run("an unpublished binding's release deletes the claim", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.reserve(t, "sess-a")
		// The reservation's release after a failed bind: no binding was
		// published, so the release ends the occupancy without reuse.
		if err := f.binder.ReleaseSlotReservation(context.Background(), releasePod, "sess-a", false); err != nil {
			t.Fatalf("ReleaseSlotReservation: %v", err)
		}
		if f.claimExists(t) {
			t.Errorf("claim = %q, want it deleted", f.claimPhase(t))
		}
	})
}
