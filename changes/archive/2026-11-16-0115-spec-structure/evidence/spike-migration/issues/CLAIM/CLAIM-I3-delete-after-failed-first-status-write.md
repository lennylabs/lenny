---
id: CLAIM-I3
kind: question
severity: low
requirements: [CLAIM-R2, CLAIM-R15]
change: null
legacy: null
---

# The idle scan deletes a claim whose first status write failed

CLAIM-R2 lists every step that may delete a claim, and the list has no deletion by the gateway after a failed first status write. The failure-behavior section of `pod-claims.md` says a claim with no binding state is reclaimed by the no-state predicate of CLAIM-R15.

`claimIdle` in `pkg/gateway/core/podclaimer/claimer.go` deletes the claim it created when `writeBoundStatus` fails (`_ = c.Client.Delete(ctx, claim)`), and its comment before this spike said the orphan reclamation reclaims it. The slot path (`acquireFreshClaim`) does not delete the claim directly; it rolls the reservation back through `ReleaseSlot`, which deletes the claim only when the set is empty.

Either CLAIM-R2 gains this deletion, or the idle scan leaves the claim to CLAIM-R15. The deletion releases the pod about `claimOrphanTimeout` sooner. Leaving it to reclamation drains the pod, because a claim with no state that the leader reclaims drains its pod (CLAIM-R15, "No state").
