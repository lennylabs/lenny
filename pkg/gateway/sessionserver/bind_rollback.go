// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// rollbackBinding releases a successful startOnPod result whose
// caller has decided to abort the §7.1 atomic-creation flow
// before the session row was persisted. Exclusive session-mode
// bindings drop through Binder.Release with a `failed` disposition;
// concurrent-slot bindings drop through ReleaseSlot. Best-effort:
// the §6.2 reclaim path runs regardless, so a release error here is
// logged and swallowed. spec: §7.1 atomic-creation rollback.
func (s *Server) rollbackBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil || s.podBinder == nil {
		return
	}
	var err error
	if result.SlotID != "" {
		err = s.podBinder.ReleaseSlot(ctx, result)
	} else {
		err = s.podBinder.Release(ctx, result, "failed")
	}
	if err != nil {
		log.Printf("sessionserver: rollback binding for session %s: %v", result.SessionID, err)
	}
}

// createClaimNeedsRollback reports whether the create-and-start failure
// handler must release the create-time claim itself (via rollbackClaim)
// after a startOnPod error, or whether the binder already released it.
//
// The combined POST /v1/sessions/start path claims the pod at /create and
// then runs prepare/launch against that claim in the same call. On a
// startOnPod error the create-time claim must be released because the row is
// not persisted (§7.1). The disposition turns on the claim kind:
//
//   - No claim (nil) or a claimless service-mode disposition: nothing to
//     release.
//   - Exclusive claim (SlotID == ""): always release. rollbackClaim runs
//     ReclaimClaimed, a DELETE that is idempotent — a no-op when Prepare or
//     Launch already reclaimed the pod, and the only release on a pre-bind
//     ResolvePool/pool-warming/manifest failure inside startOnPod.
//   - Slot reservation (SlotID set): release only when the binder did not
//     reach BindReservedSlot. BindReservedSlot owns the non-idempotent
//     active_slots release on its own failures (slotbinder.go), surfaced as a
//     *podsession.SlotBindError, so releasing again would double-decrement the
//     counter. But startOnPod runs ResolvePool, the PoolWarmingUp gate, and
//     runtimeManifestFields BEFORE BindReservedSlot; a failure there (a
//     transient kube-API error on the second ResolvePool of the same request,
//     a pool that warmed down) returns a non-SlotBindError without ever
//     releasing the create-time reservation. Releasing it here keeps the
//     active_slots increment from leaking to the §4.6.1 orphan-GC backstop,
//     matching the §7.1 atomicity envelope the decomposed createSession path
//     already honors through its own rollbackClaim.
//
// spec: §7.1; §4.7 (the combined path reuses the claim/prepare/launch phases);
// §5.2 (slot reservation release).
func createClaimNeedsRollback(claim *podsession.ClaimResult, err error) bool {
	if claim == nil {
		return false
	}
	if claim.SlotID == "" {
		// Exclusive claim: ReclaimClaimed is idempotent, so release
		// unconditionally regardless of where startOnPod failed.
		return true
	}
	// Slot reservation: BindReservedSlot already released it iff the failure
	// is a *podsession.SlotBindError (the binder ran). A pre-bind failure
	// returns some other error and leaks the reservation unless released here.
	var slotBindErr *podsession.SlotBindError
	return !errors.As(err, &slotBindErr)
}

// rollbackClaim releases a pod claimed at /create when a later create
// step fails before the session row is persisted (§7.1
// atomic-creation rollback). The claim holds no live connection, so the
// release runs against the persisted binding. The disposition follows the
// claim kind: an exclusive Claim is released through ReclaimClaimed, which
// deletes the per-pod SandboxClaim and revokes any lease; a §5.2
// concurrent-slot reservation (SlotID set) is released through
// ReleaseSlotReservation, which decrements the pod's active_slots without
// deleting the per-pod claim or disturbing sibling slots. No lease is
// assigned at create (§4.4 of the proposal), so the lease revoke is a
// no-op here. Best-effort: the §4.6.1 orphan-claim GC reclaims a claim a
// release error leaves behind, so the error is logged and swallowed.
// spec: §7.1; §4.5, §4.6
// (proposal); §5.2 (slot reservation release).
func (s *Server) rollbackClaim(ctx context.Context, claim *podsession.ClaimResult, sessionID string) {
	if claim == nil || s.podBinder == nil {
		return
	}
	if claim.SlotID != "" {
		// No workspace RPC runs at create, so the adapter holds nothing for
		// the slot and the release is not leaked.
		if err := s.podBinder.ReleaseSlotReservation(ctx, claim.SandboxName, claim.SlotID, false); err != nil {
			log.Printf("sessionserver: rollback create-time slot reservation %s on pod %s for session %s: %v",
				claim.SlotID, claim.SandboxName, sessionID, err)
		}
		return
	}
	if err := s.podBinder.ReclaimClaimed(ctx, claim.SandboxName, sessionID); err != nil {
		log.Printf("sessionserver: rollback create-time claim %s for session %s: %v", claim.SandboxName, sessionID, err)
	}
}
