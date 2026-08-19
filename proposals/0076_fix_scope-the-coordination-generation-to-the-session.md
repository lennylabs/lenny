# Proposal: Scope the coordination generation to the session

- **Status:** Draft for review.
- **Date:** 2026-08-19
- **Scope:** The specification scopes the coordination generation to the session and the adapter stores it
  per pod. On a pod running one session at a time the two are indistinguishable. On a pod running several,
  one session's coordinator handoff fences out another session's legitimate coordinator, rejects its
  drain barrier, and releases its coordinator-loss hold. The defect is masked today by a broken session
  guard that fails closed, and proposal 0073 repairs that guard.

This document stages the proposed specification, schema, and code changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It states a defect with its evidence and frames the
design question rather than answering it. The hold-state decision in §7 is genuinely open and is the
substance of this change. Run the change-proposal convergence loop on it before sign-off.

## Summary

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` equality
gate, the gap-detection reset, and the coordinator-loss hold follow it to whatever scope review settles on.
The proto doc comment that already claims per-session monotonicity becomes true.

**What is fixed.** On a concurrent pod: a legitimate coordinator handoff rejected as
`coordinator_handoff_stale`, a drain barrier rejected so a partial checkpoint is lost, a spurious
`coordinator_generation_gap` and its state reset, a coordinator-loss hold released by an unrelated
session's fence, and a split-brain counter that increments on healthy handoffs.

**Watch out for.** The mechanical half, moving the counter onto the registry entry, is not the hard half.
Hold state is pod-wide today and holding one session of four means per-session admission on every inbound
RPC. That decision is open and is the reason this is a proposal rather than a patch. Proposal 0073 is
converged and is not reopened; this proposal sequences after it.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §10.1 states the generation's scope on the pod side and what a hold covers.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · schema** — SCHEMA-1. The proto doc comments state the settled scope.
      Tiers 0, 3. Depends on: S1
- [ ] **S3 · code** — CODE-1. The fenced generation moves onto the slot registry entry.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S4 · code** — CODE-2. `CheckpointBarrier`'s gate reads the per-session generation.
      Tiers 0, 1, 3. Depends on: S3
- [ ] **S5 · code** — CODE-3. Hold state takes the scope §7's decision settles.
      Tiers 0, 1, 8. Depends on: S1, S3
- [ ] **S6 · test** — TEST-1. Two co-tenant sessions handing off independently, on proposal 0060's
      two-replica harness.
      Tiers 7a, 8. Depends on: S3, S4, S5

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

### 1.4 Why this is not visible today, and why it soon will be

`CoordinatorFence` calls `checkSession` (`pkg/adapter/session.go:346`), which reads the pod-global
`Server.sessionID`. Only `claimSession` and `claimSessionForConfigure` write that field, and a concurrent
pod's `StartSession` returns early into `startSessionSlot` (`pkg/adapter/slotsession.go:27`), which records
the session on the slot-registry entry and never touches `Server.sessionID`. On a concurrent pod the guard
therefore fails first, and the fence never reaches the pod-wide counter. Coordinator handoff is already
broken on those pods, but it **fails closed**.

Proposal 0073 merges `checkSession` and `checkSlotSession` into a single `checkSessionBound` resolving
through the slot registry, which repairs the guard on every pod. 0073 §1.9 records this consequence for the
five handlers that call `checkSession` with no slot branch, of which `CoordinatorFence` and
`CheckpointBarrier` are two. The repaired guard is correct and necessary. Its effect here is to unmask this
defect: the fence stops failing closed and starts succeeding against a pod-wide counter.

The transition is from *fails closed* to *succeeds incorrectly*. On availability that is an improvement. On
correctness it is a regression, and it is the kind that surfaces as an unexplained stuck session rather
than as an error.

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

### SCHEMA-1. Make the comments true

`schemas/lenny-adapter.proto`: the `CoordinatorFenceRequest.coordination_generation` and
`CheckpointBarrierRequest.coordination_generation` doc comments state the settled scope.

### CODE-1. Move the state

`pkg/adapter/coordination.go`: `coordinationState` moves onto the slot registry entry;
`pkg/adapter/server.go:304`'s field is removed. `CoordinatorFence` resolves the entry for the named session.

### CODE-2. The barrier reads the same place

`pkg/adapter/coordination.go:211`.

### CODE-3. Hold state

`pkg/adapter/holdstate.go`, per §7.

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

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers this change reaches are 0, 1, 2 (the registry state), 3 (the
wire gate's behavior), 7a (concurrent handoffs), and 8 (crash takeover). Proposal 0060 built a two-replica
gateway harness and tier-8 crash-takeover coverage for §10.1; read what it built before designing here,
because the per-session fencing case probably belongs in that harness rather than in a new one.

The case that pins this defect: two sessions co-tenant on one pod, each handed off independently, asserting
that the second handoff is accepted, that its barrier is accepted, that no gap is logged, and that the
first session's hold is not released by the second's fence. It must fail against the pre-fix code.

## 9. Files touched on application

- `spec/10_gateway-internals.md`
- `schemas/lenny-adapter.proto`
- `pkg/adapter/coordination.go`
- `pkg/adapter/server.go`
- `pkg/adapter/holdstate.go`
- `pkg/adapter/slotsession.go` (the registry entry gains the generation)
- Tests under `pkg/adapter/`, `tests/tier7a_load/`, and `tests/tier8_chaos/`

## 10. Dependencies

Applies after proposal 0073, which supplies `checkSessionBound` and the slot registry this proposal keys
the generation on. Independent of proposal 0075.
