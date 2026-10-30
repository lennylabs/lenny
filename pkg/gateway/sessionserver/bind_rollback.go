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
// before the binding was registered: registerBinding failed in /start,
// create-and-start failed its lease or its row create, or delegated-child
// materialization failed. Exclusive session-mode bindings drop through
// Binder.Release with a `failed` disposition, which retires the pod;
// concurrent-slot bindings drop through ReleaseSlot with recycling
// disabled, so the last release deletes the claim rather than recycling a
// pod whose binding was never published. Best-effort: a release error is
// logged and swallowed, and the §4.6.1 orphan-claim GC bounds a claim it
// leaves. The rollback runs detached from the caller's cancellation, so a
// client disconnect or a parent teardown cannot strand the claim; the §7.1
// release allowance bounds it, and the §4.6.1 orphan-claim GC bounds a crash
// mid-rollback.
//
// spec: §7.1 (Atomicity of session creation (steps 2–8)); §7.1 (Terminal
// pipeline context: release allowance bounds the release); §4.6.1 (Orphaned
// SandboxClaim detection); §4.6.3 (SandboxClaim: a release that ends the
// occupancy without reuse); §5.2 (Recycle lifecycle).
func (s *Server) rollbackBinding(ctx context.Context, result *podsession.BindResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.streamFailureReportTimeout)
	defer cancel()
	if result == nil || s.podBinder == nil {
		return
	}
	var err error
	if result.SlotID != "" {
		// spec: §4.6.3 (SandboxClaim: a release that ends the occupancy
		// without reuse), §5.2 (Recycle lifecycle) — the binding was never
		// registered, so the occupancy ends without reuse. Recycle is
		// cleared before the release, so the last release on a recycling
		// pool deletes the claim rather than patching it to `recycling` and
		// sending the whole-pod recycle Shutdown for an unpublished binding.
		// The result is cleared in place rather than copied, because
		// BindResult carries atomic state that must not be copied, and no
		// registry or other caller holds a binding that was never
		// registered.
		result.Recycle = false
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
//     reach BindReservedSlot. BindReservedSlot releases the session's
//     active_slots member on its own failures (slotbinder.go), surfaced as a
//     *podsession.SlotBindError, and a second keyed release would remove
//     nothing. But startOnPod runs ResolvePool, the PoolWarmingUp gate, and
//     runtimeManifestFields BEFORE BindReservedSlot; a failure there (a
//     transient kube-API error on the second ResolvePool of the same request,
//     a pool that warmed down) returns a non-SlotBindError without ever
//     releasing the create-time reservation. Releasing it here keeps the
//     session's active_slots member from leaking to the §4.6.1 orphan-GC backstop,
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
// step fails before the session row is persisted (§7.1 atomic-creation
// rollback). The claim holds no live connection, so the release runs
// against the persisted binding through ReclaimClaimed, the same
// pre-running release the post-terminal hook uses. A §5.2 concurrent-slot
// reservation (SlotID set) releases the session's slot member and deletes
// the shared claim only when that release empties the set; an exclusive
// claim is deleted by name. No lease is assigned at create, so the lease
// revoke inside ReclaimClaimed is a no-op here. Best-effort: the §4.6.1
// orphan-claim GC reclaims a claim a release error leaves behind, so the
// error is logged and swallowed. The rollback runs detached from the
// caller's cancellation, so a client disconnect or a parent teardown cannot
// strand the claim; the §7.1 release allowance bounds it, and the §4.6.1
// orphan-claim GC bounds a crash mid-rollback.
//
// spec: §7.1 (Atomicity of session creation (steps 2–8)); §7.1 (Terminal
// pipeline context: release allowance bounds the release); §4.6.1 (Orphaned
// SandboxClaim detection); §4.6.3 (SandboxClaim: a release that ends the
// occupancy without reuse); §5.2 (Slot assignment atomicity).
func (s *Server) rollbackClaim(ctx context.Context, claim *podsession.ClaimResult, sessionID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.streamFailureReportTimeout)
	defer cancel()
	if claim == nil || s.podBinder == nil {
		return
	}
	if err := s.podBinder.ReclaimClaimed(ctx, claim.SandboxName, sessionID, claim.SlotID != ""); err != nil {
		log.Printf("sessionserver: rollback create-time claim %s for session %s: %v", claim.SandboxName, sessionID, err)
	}
}
