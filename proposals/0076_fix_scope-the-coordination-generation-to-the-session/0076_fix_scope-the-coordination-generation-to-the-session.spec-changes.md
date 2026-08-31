# Spec changes: Scope the coordination generation to the session

The design prose below motivates both the specification and the code, so it is carried here. The staged
edit under `spec/` follows it.

## 2. Decisions

**D1. The fenced generation is per session, matching the specification and Postgres.** The pod side stops
holding one value for the process and holds one per bound session.

**D2. The generation lives on the slot registry entry rather than in a second map.** Proposal 0073
establishes the slot registry as the per-session state the adapter already keys everything else on, and a
parallel map keyed by session identifier would be a second lifetime to get wrong.

**D3. The proto doc comment becomes true rather than being deleted.** It already states the intended
design.

**D4. 0073 is not reopened.** This proposal sequences after it and depends on `checkSessionBound`.

**D5. Hold-state scope is deferred to review.** See §7. It is the substance of this change and is not
decided in this draft.

## 3. Design overview

`coordinationState` moves from `Server` onto the per-session slot registry entry. `CoordinatorFence`
resolves the entry for the named session and compares against that entry's `lastFenced`.
`CheckpointBarrier` does the same. Gap detection becomes per session, so a gap is a real skip in one
session's lineage. Hold state follows to whatever scope §7 settles.

## 4. Detailed design

**IMPLEMENTOR TO FILL THE BLANKS.** The per-entry move is straightforward and is not written out here. What
must be derived during convergence:

- What a fence means for a session the pod holds no bound entry for. Today the guard rejects it. Under
  0073's registry model a released session's entry may be absent, and a fence arriving for it is either a
  stale coordinator (reject) or a race with a bind (retry), and the two must be distinguishable.
- Whether `CheckpointBarrier`'s gate stays equality or becomes "at least the last fenced generation".
  Equality is what makes a barrier from a superseded coordinator fail; confirm that a per-session counter
  does not change which coordinators that catches.
- What the §10.1.2 gap reset resets, once it is per session. Establish from the code what state the reset
  actually touches today before scoping it.
- Whether the pod-wide `coord.mu` becomes per-entry or stays one lock. A single lock is simpler and the
  critical sections are short; per-entry locking is only worth it if fences contend.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** Indicative targets; the text is written during convergence, against the
post-0073 state of each file.

### SPEC-1. State the pod-side scope

`spec/10` §10.1.2 states that the pod holds the fenced generation per session, so a handoff for one session
neither fences nor unfences another. §10.1.4 states what a coordinator-loss hold covers, per §7's decision.

## 6. Non-goals

- **Renaming `CoordinatorFenceRequest`'s session field.** That is proposal 0075's subject. If both land,
  whichever is second rebases onto the first.
- **Changing the gateway or Postgres side.** Both are already per session and are correct.
- **Reopening 0073.**
- **Forced acquisition or any change to the lease protocol.** Proposal 0060 built the lease co-location and
  crash-takeover path; this proposal changes only what the pod does with the generation it is handed.

## 7. Open decisions for review

1. **What a coordinator-loss hold covers on a multi-session pod.** This is the substance of the change.
   Today the hold is pod-wide: `holdState` carries one session and one generation, and while held the
   adapter serves only the §10.1.4 allowlist. Two options, and neither is obviously right.

   - **Pod-wide hold, per-session generation.** Smallest change. A hold entered for one session still stops
     the pod, so three healthy co-tenant sessions are penalized for a fourth's lost coordinator.
   - **Per-session hold.** Correct in principle. Requires per-session admission on every inbound RPC and a
     per-session reading of the allowlist, which is a materially larger change with its own failure modes,
     and it means a pod can be partly available, which the specification does not currently describe.

   Measure both against `pkg/adapter/holdstate.go` and the allowlist before choosing.

2. **Whether the barrier gate stays equality.**
3. **Whether a fence for an unheld session is a rejection or a retryable race.**
4. **Whether `coord.mu` becomes per-entry.**

## 10. Dependencies

Applies after proposal 0073, which supplies `checkSessionBound` and the slot registry this proposal keys
the generation on. Independent of proposal 0075.
