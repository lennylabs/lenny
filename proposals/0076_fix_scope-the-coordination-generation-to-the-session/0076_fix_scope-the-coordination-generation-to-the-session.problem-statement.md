# Problem: Scope the coordination generation to the session

## 1. Problem

### 1.1 The specification scopes the generation per session

`spec/10` §10.1.1 states that each session row carries a `coordination_generation` counter and that a
replica taking over coordination increments it. §10.1.2 step 1 is a compare-and-swap of the form
`UPDATE sessions SET coordination_generation = $expected_generation + 1 WHERE id = $session_id AND
tenant_id = $tenant_id AND coordination_generation = $expected_generation`. The counter is a column on the
`sessions` table, one value per session.

The gateway sends it per session. `Client.CoordinatorFence`
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:48`) takes a session identifier and a generation
and puts both on the request.

### 1.2 The adapter stores it per pod

`Server` holds one `coord coordinationState` field (`pkg/adapter/server.go:304`). `coordinationState`
(`pkg/adapter/coordination.go:25`) carries a single `lastFenced int64` for the whole adapter process.

`CoordinatorFence` (`pkg/adapter/coordination.go:84`) reads the session identifier, verifies the pod is
running that session, takes `coord.mu`, and compares the incoming generation against that one pod-wide
value. It rejects `gen <= lastFenced` with `FailedPrecondition` and a `coordinator_handoff_stale` detail,
treats `gen > lastFenced+1` as a gap, records the new value otherwise, and calls `exitHoldState()`.

The doc comment on `CoordinatorFenceRequest.coordination_generation`
(`schemas/lenny-adapter.proto:1403`) states that the generation is "Strictly monotonic on the pod side per
session". It is monotonic per pod. The comment describes the Postgres design rather than the adapter's.

### 1.3 What breaks on a concurrent pod

A pod hosts session A at generation 5 and session B at generation 3.

1. A's coordinator hands off, mints 6, and fences the pod. `lastFenced` becomes 6.
2. B's coordinator hands off, mints 4, and fences the same pod. The handler compares 4 against 6 and
   rejects it as stale.
3. B's new coordinator does what §10.1.2 instructs on a stale fence: re-read Postgres, re-issue. Postgres
   says 4 is correct. It is rejected again.

B is uncoordinatable on that pod until some unrelated session's generation stops exceeding its own. Four
further consequences follow from the same field:

**The drain barrier is rejected, which costs data rather than availability.** `CheckpointBarrier`
(`pkg/adapter/coordination.go:211`) shares the gate and requires the supplied generation to equal
`lastFenced` exactly. A barrier for B is rejected. The barrier is the §10.1 graceful-drain path that
preserves a partial checkpoint, so a rejected barrier loses the partial manifest that path exists to write.

**Gap detection fires on a handoff that skipped nothing.** B's jump from the pod's view of A's generation
looks like a skipped generation, so the adapter logs `coordinator_generation_gap` and takes the §10.1.2
reset path.

**A hold entered for one session is released by another's fence.** `exitHoldState()` is called
unconditionally on a successful fence, and `holdState` (`pkg/adapter/holdstate.go:39`) carries a single
`session` and `gen`.

**The counter that should surface this instead misleads.** `lenny_coordinator_handoff_stale_total`
increments on legitimate handoffs, so the metric the specification designates for detecting split-brain
reports healthy ones.

### 1.4 What kept this out of sight, and why it is now exposed

Until proposal 0073 landed, `CoordinatorFence` called `checkSession`, which read the pod-global
`Server.sessionID`. Only `claimSession` and `claimSessionForConfigure` wrote that field, and a concurrent
pod's `StartSession` returned early into `startSessionSlot` (`pkg/adapter/slotsession.go`), which recorded
the session on the slot-registry entry and never touched `Server.sessionID`. On a concurrent pod the guard
therefore failed first, the fence never reached the pod-wide counter, and coordinator handoff was already
broken on those pods while **failing closed**.

Proposal 0073 merged `checkSession` and `checkSlotSession` into a single `checkSessionBound`
(`pkg/adapter/slotsession.go:267`) resolving through the slot registry, and removed the `Server.sessionID`
field the old guard read. `CoordinatorFence` now calls `checkSessionBound` (`pkg/adapter/coordination.go:89`),
so the guard is repaired on every pod. 0073 §1.9 records this consequence for the five handlers that called
`checkSession` with no slot branch, of which `CoordinatorFence` and `CheckpointBarrier` are two. The
repaired guard is correct and necessary. Its effect here is to unmask this defect: the fence no longer fails
closed and instead succeeds against a pod-wide counter.

That masking is gone as of 0073's implementation, so the transition below has already happened in the tree
rather than being a consequence this proposal anticipates.

The transition is from *fails closed* to *succeeds incorrectly*. On availability that is an improvement. On
correctness it is a regression, and it is the kind that surfaces as an unexplained stuck session rather
than as an error.
