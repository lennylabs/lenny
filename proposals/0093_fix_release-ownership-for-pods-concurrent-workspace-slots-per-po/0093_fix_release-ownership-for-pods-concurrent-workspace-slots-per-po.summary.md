# Summary: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write

## Summary

**Problem statement.** Several gateway paths release a pod, the per-pod SandboxClaim `claim-<podName>`, a concurrent-workspace slot, or a credential lease at or after a session's terminal write, and no terminal path has a single owner for those releases. Some releases hit the wrong target. On a concurrent-workspace pool, a DELETE, a terminate, or a watchdog timeout of a session that has not started deletes the shared per-pod claim, and the WarmPoolController then drains the pod and ends every live sibling session. The finalize failure exits and the created sweeper delete the same claim by name, a superseded coordinator releases a pod a peer adopted, and full_revoke sends an unfenced `Shutdown` that a later release repeats. Other releases never happen: a pre-running slot reservation has no slot-aware releaser, a failed slot release is not retried, full_revoke removes lease records without returning pool slots, and a process exit between the terminal commit and the release leaks the claim, the slot, and the leases. Making the pre-running release slot-aware over today's decrement-only slot counter double-decrements on paths the tree already has, and an under-count that reaches zero deletes the shared claim under live siblings. The spec also lists no gateway session-end claim delete in its SandboxClaim writer sets.

**What changes.**
- Slot occupancy register: `REG-SLOTCOUNT` becomes a per-pod set keyed by session, with an idempotent keyed release and an occupancy-zero disposition gated on the removing release (SPEC-1, CODE-1).
- Pre-running releases: one slot-aware `Binder.ReclaimClaimed`; the finalize exits and the deleted created sweeper stop deleting a claim by name; the SandboxClaim writer set in §4.6.3 names the delete by a release that ends the occupancy without reuse, and every other claim-outcome site points at it (SPEC-2, CODE-2, CODE-3, CODE-4). The non-spec **SandboxClaim end-of-occupancy disposition table** states the claim and slot effect of every gateway release path.
- Post-terminal hook: a stale-binding step before the seal, and the slot-accounting lock on the release step (CODE-5, CODE-6).
- full_revoke: the deny-list step first, then the pre-running reclaim and the D6 release body, with §11.4 stating the release and dropping the `USER_REVOKED` reason (SPEC-3, CODE-7).
- Tests across tiers 1, 2, 3, 4, 5, 7a, 8, 9, and 11 (TEST-1 and the per-deliverable tests), and the BUILD-GAPS records (REC-1).

**Decisions.**
1. The design is the minimal one: one idempotent slot-release primitive, a slot-aware `ReclaimClaimed`, deletion of the side releasers, a hook stale-binding step, a hook release lock, full_revoke through the existing releasers, and departures for releases no replica can perform. It adds no RPC, proto field, column, table, state, loop, or error code.
2. `REG-SLOTCOUNT` keeps its key and register identifier and changes its value to a Redis set. A parallel `released_slots` key was rejected because it is lost on a Redis restart, after which a duplicate release under-counts toward a false zero, and because it grows by one member per session with no TTL.
3. The occupancy-zero disposition runs only when the release removed the session's member and left the set empty, so a duplicate release while Redis keeps the set, and a release of a member rehydration already dropped, never patch or delete a claim.
4. The pool-kind discriminator is `row.ScrubPolicy == "best-effort-per-slot"`, which is frozen at create and set under the same condition that selects `ClaimSlot`; a live pool lookup was rejected because `maxConcurrentSessions` can change after create.
5. The exclusive pre-running branch stays a by-name delete with no precondition, because that delete always drains the pod and `commitStart` refuses after a terminal write.
6. F-10.1.28 and F-11.4.11 are closed on the gateway side with D6's generation predicate. The pod-side `Shutdown` generation fence stays with R16 under its existing UNWIRED claim-register row, because pulling it in requires restaging `Binder.Release` and taking C2.
7. Missed releases get no durable pending-release record and no reconciler; each is a departure with a bound (non-spec **Edge cases and accepted failure modes**).
8. full_revoke returns each lease at §11.4 step 1: `RevokeUserLeases` calls the credential assigner's `Release` after its deny-list write, because removing the lease record rejects the token on every replica through the lease store, whatever happens to the deny-list publish or the binding replica, and `Release` returns the pool slot that a bare record removal strands.
9. §11.4 step 2 drops `USER_REVOKED`, because the adapter never reads `ShutdownRequest.reason` and threading it through the release path would change four signatures for a value nothing observes.
10. F-5.2.49 is fixed by a bounded detached retry of counter failures only, which is safe because the keyed release is idempotent.
11. N2 gets no code change, because a sibling can reserve onto the freshly created claim before the failed resume reservation, so a by-name delete is unsafe.
12. F-7.3.34(b) gets no code change, because the rebuild-branch drain retires the exclusive pod.
13. `revokeFinalizeLease` and its call sites stay unchanged. F-4.9.35 closes as resolved by 0092's D7, because `finalizing` exits only to the finalize handler's own ready write or to a terminal state.
14. The hook's release step waits on `slotAccountLocks` instead of the funnel deferring to an open terminal claim, because a deferral drops the failure from the §5.2 replacement trigger and leaks the binding when the terminal write loses.
15. The hook's stale-binding step evicts only for `running`, `input_required`, and `suspended`, and releases with the failed disposition for the resume family, because `MarkTerminated`'s `resuming` bump makes the earlier pod's binding look stale and no adopter holds that pod.
16. Standing test clusters get a test-infrastructure cleanup of string-valued `active_slots` keys rather than a key rename, because the platform has no deployments and the project forbids migration shims.
17. Existing spec text does not suffice. A slot-aware reclaim over the decrement-only counter double-decrements after a failed `/start` followed by DELETE and after a terminal write racing a peer's `/start`, and §5.2 specifies the counter's script, so the idempotent keyed release is a spec change.
18. Scope boundaries: P5b owns the full_revoke deny-list granularity (C3), the full_revoke session set and listing, and the remaining halves of F-11.4.10, because the owner split P5 on 2026-10-16 (0094 decision D20): 0094 covers the lifecycle fence, the session drain, the durable erasure job, and the removal of the `DELETE /v1/responses/{id}` route, and changes no full_revoke code. P3 owns the retention of the `failed` rows the watchdog now leaves and the created-expiry state mismatch; R16 owns S16, S17, and the generation checks on operational RPCs; F-7.1.30 owns the session-end order. The edits to `cmd/lenny-gateway/user_revocation.go` and the full_revoke handler land before P5b's.

**Watch out for.**
- The non-spec **SandboxClaim end-of-occupancy disposition table** is the only statement of which release deletes, patches, or leaves the claim. A change to that disposition starts at the table, then SPEC-2c, then the pointer sites. A universal that covers every acquisition undo is wrong for the CREATE loser, whose withdrawal must leave the winner's claim intact (row D8).
- CODE-2 must not land before CODE-1. A slot-aware reclaim over the decrement-only counter double-decrements after a failed `/start` followed by DELETE and after a hook reclaim followed by a peer's D6 release, and an under-count that reaches zero deletes the shared claim under live siblings.
- The leaked arm of the release script adds the `leaked:` member whether or not `SREM` removed one. Gating it on the removal looks tidier and loses the leak after a clean pre-running reclaim, which fails open.
- `SlotClaimer.ReleaseSlot` must gate the disposition on `removed`. A gate on `remaining == 0` alone re-runs the disposition when a duplicate release finds the set already empty.
- A disposition error after the member is removed must not be retried. The retry finds no member and skips the disposition, leaving a bound claim on an empty pod.
- full_revoke's deny-list step must run before the first cancel write, because the coordination Sweeper and the resume tail release a terminal row (CODE-7 item 1).
- The unconditional `revokeFinalizeLease` in `finalizePrepareFailed` looks redundant after CODE-3 and is the only lease return when `failFinalizing` commits and reports a store error.
- Holding `slotAccountLocks` across the seal looks safer and blocks the failure funnel for up to the seal window.
- On a standing cluster, a tier-2-or-higher failure that reports `WRONGTYPE` on `active_slots` is an environment failure until the CODE-1 cleanup has run. Existing tests that `SET` the key to force exhaustion fail the same way and are retargeted in CODE-1's step.

## Goals

- No terminal write of a session that has not started deletes the claim of a live sibling on a concurrent-workspace pool.
- Every slot reservation is returned once, however many paths release it, and every leaked slot stays counted, apart from the Redis-restart departures.
- Each resource on each terminal path has one releaser, as the non-spec resource-to-owner table states.
- The spec states the slot set and its keyed release, the gateway's claim delete by a release that ends the occupancy without reuse, and the full_revoke release.
- Every release no replica can perform is recorded as a departure with its bound.
- The tests cover every writer, path, and state the change touches, with randomized interleavings, fault injection, and invariant checks.

## Non-goals

- **A §7.3 rule naming the only paths that return a session's leases.** Existing text already makes both code defects defects (§6.1, §7.3 **Credential leases on the replacement pod.**, §11.4 step 6), and an exclusive list would forbid the terminal-handling lease release that §7.2 and §6.2 require and the assignment-failure and renewal removals in the tree.
- **A §10.1.1 statement of when a pre-running start acquires `REG-COORDLEASE` (S17).** No release in this design depends on it, a correct statement also has to cover the delegated-child `created` acquisition and the Sweeper renewal of a bound `ready` row and co-edit three §29 rows, and R16 already edits the same §10.1.1 bullet for S16. It goes to R16 with S16, or is filed as a standalone spec gap.
- **A parallel `released_slots` key in front of the counter.** It is lost on a Redis restart and grows without bound; Decision 2 replaces it.
- **A sorted set scored by reservation time, rebuilt when full, with a grace tunable.** A rebuild from rows drops in-flight create and resume reservations unless a timing grace covers them, which is a new tunable and a fail-open risk.
- **Rehydration as the slot backstop**, by clearing the `rehydrated` flag or by a periodic reseed. A reseed drops leaked slots and in-flight reservations, which over-admits, and the flag cannot be cleared when Redis is the failing store.
- **A durable pending-release record** (a column, a table, or a Sweeper adoption of terminal rows). Each adds a migration, a writer at every terminal write, and a completer, while the GC, a counted slot, and lease expiry already bound the cases.
- **The pod-side `Shutdown` generation fence.** It needs `Binder.Release` restaged so `Shutdown` runs before the claim and lease effects, and it is registered UNWIRED with deferral R16.
- **Restaging `Binder.Release` and the lease-before-`Shutdown` half of C2.** C2 is conditional on a restage, which this design does not do, and F-7.1.30 keeps the session-end order.
- **Hoisting `REG-COORDLEASE` to `/start` entry and gating the hook's release on it.** With a keyed release a duplicate is harmless, and the hoist adds a release on every failure exit and a stuck-lease trap on a `ready` row.
- **Keeping the reservation on a failed `/start` and clearing `pod_assignment` when the slot leaks.** It arbitrates ownership that the keyed release makes unnecessary and changes `/start` retry semantics.
- **A UID or `resourceVersion` precondition, or a Postgres owner check, on the exclusive claim delete.** The pre-running exclusive delete always drains the pod, a sibling reservation does not write the claim, and an owner check breaks the bind-failure callers whose row still names the pod.
- **Session identity on the SandboxClaim.** The concurrent claim is shared, and §4.6.3 keeps the session binding on the Postgres row.
- **Exclusive pools on the slot set with a cap of 1.** It adds a Redis dependency and the §12.4 fallback to every exclusive claim and release.
- **Resolving the pool kind by a live lookup or `ResolvePool`.** Decision 4.
- **A pre-running `Shutdown` before the concurrent slot release.** A pre-running concurrent session holds no pod-side workspace unless a `/start` is in flight, and that start is released by D6.
- **Resuming through `SlotClaimer.ClaimSlot` to fix N2, or deleting N2's claim when the reservation fails.** The first changes resume placement onto sibling-occupied pods; the second is unsafe (Decision 11).
- **A `ShutdownReclaim` before the rebuild-branch drain.** Decision 12.
- **Threading `USER_REVOKED` through the release path.** Decision 9.
- **A new §7.1 **Session release.** paragraph, a §4.7.1 gate, and a §10.1.5 item.** §7.2, §7.3, and D6 already state the two release forms, and the fence clauses belong to R16.
- **Keeping the created sweeper with a state predicate.** Two writers for one deadline is the defect, and the watchdog's guarded write and the hook own the deadline.
- **Keeping the finalize store-error reclaim on the ready-write exit, with or without a re-read.** When the ready write's outcome is unknown, the reclaim can end a committed `ready` session's pod.
- **A per-session tombstone key with a TTL.** The TTL bounds idempotency while the duplicate window has no fixed bound, and the key blocks a legitimate re-reservation.
- **Forwarding a release to the binding replica over pub/sub.** 0092's Sweeper already hands every bound terminal row to the holder, and a forward is not durable.
- **Moving every claim delete to the controller GC.** It adds up to `claimOrphanTimeout` to every session end and breaks the gateway-coordinated recycle disposition.
- **Reassigning F-10.1.28, F-11.4.11, and the full_revoke halves to R16 or P5b.** The validated scope keeps the mechanism of release in P1.
- **S16** (the coordination lease and mirror row at a terminal write). R16 owns it.
- **The members the problem statement moved out**: the credential-accounting findings (N4, F-4.9.32, F-4.9.34, F-4.9.37), F-4.9.33, F-10.1.30, F-10.1.32, F-5.2.50, and the deny-list and session-set halves of F-11.4.10 with C3.

## Open decisions for human to make

None at drafting. The review loops write this section.

## Defects in the shipped tree that this proposal does not stage

- **A retried `/start` materializes an uncounted slot.** `BindReservedSlot` releases the reservation on a failed `/start` while the row stays `ready` with its pod, so a retry binds without a reservation. The fix changes `/start` retry semantics, which is outside release ownership. REC-1 files it.
- **The occupancy-zero claim delete races a concurrent reservation on another replica.** A release that empties the set can delete the claim while a peer's reservation on the same pod is in flight. It predates this proposal, and closing it needs a fence on the claim that release ownership does not provide. REC-1 files it.
- **`Counter.Reset` has no production caller**, so the §10.1.4 whole-pod reset does not run. It belongs with the whole-pod connection-loss reaction. REC-1 files it.
- **Token Service pool counts are per replica (N4).** A release that reaches the wrong replica, or a Token Service restart, drifts the counts. The credential-accounting proposal owns it.
- **Created-state expiry ends `failed` with `CREATED_TIMEOUT`, while §15.1 names `expired` and §7.2 has no timeout edge out of `created`.** The mismatch predates the created sweeper's deletion and belongs to P3. REC-1 files it.
- **§10.1.1 says a `created` or `ready` session holds no coordination lease**, while `/start` acquires the lease on a `ready` row, delegated-child materialization acquires it on a `created` row, and three §29 rows say no acquisition point is stated (S17). It goes to R16 or a standalone spec proposal.
- **full_revoke deny-lists a pool credential without the user** (the deny-list half of F-11.4.10, C3). P5b owns it. After CODE-7, step 1 also returns each such lease to its pool through `Release` while the same step deny-lists the pool credential, so the pool can hand out a lease whose credential every replica rejects; P5b's granularity change settles it.
- **A concurrent slot can land on a pod whose whole-pod scrub is running.** `SlotClaimer.ClaimSlot` Pass 1 skips only a missing claim and the terminal binding states, so it reserves onto a claim in `recycling` after the last release emptied the set. The recycling follow-up owns it. REC-1 files it.
- **A terminal write on a replica without the binding can reclaim a `running` slot as pre-`running`.** `/start` registers the binding before `commitStart`, so the row still reads `ready`, and the writer's hook runs the pre-running reclaim (disposition table, **Races the table does not close**). R16 owns it with S17. REC-1 files it.
- **An exclusive acquisition leaves a `bound` claim when the tenant stamp fails.** `Claimer.claimIdle` returns the `StampPodTenant` error without deleting the claim it created, and no caller of `claimPod` releases on that error (disposition table row D10). The claim-orphan GC deletes it within `claimOrphanTimeout` plus one sweep interval. The non-spec loop decides whether CODE-2 deletes it, as the fallback claim path already does, or REC-1 files it.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0092 | Implemented | Adds a stale-binding step and a release lock to the post-terminal hook, extracts D6's generation predicate, removes `failFinalizing`'s `podReclaimed` flag, adds a synchronous entry over D6's release body, and closes the gaps 0092 handed to P1. | Nothing. The record stands. |
| 0091 | Implemented | Reuses `slotAccountLocks`, `evictStaleBinding`, and `releaseEarlierBinding`, and replaces the predicate copy in `reportSupersededByPeer` with the shared helper. | Nothing. |
| 0079 | Implemented | Adds one occupancy-zero case on a recycling pool: a pre-running release deletes the claim (SPEC-2c; SPEC-2e points to it). | Nothing. |
| 0060 | Implemented | Leaves D1's at-bind lease acquisition unchanged and stages no S17 text. | Nothing. |
| 0085 | Draft | Changes the signatures its pre-attached seam and `/start` slot reservation would call: `ReclaimClaimed` takes a per-slot flag, and the slot counter and `SlotClaimer.ReleaseSlot` take the session identifier. | When re-baselined, release a pre-running claim or slot through the slot-aware `ReclaimClaimed`, key every slot release by session, and stage no by-name claim delete on a concurrent pool. |
| 0094 | Draft | No full_revoke edit remains in 0094: the owner split P5 on 2026-10-16 (0094 decision D20), and 0094 covers the lifecycle fence, the session drain, the durable erasure job, and the removal of the `DELETE /v1/responses/{id}` route. | Nothing for full_revoke. Its own impact row on 0093 covers `SessionAdmin.ForceTerminate`. |
| P5b | Not written | Changes `cmd/lenny-gateway/user_revocation.go` and the full_revoke fan-out first: `RevokeUserLeases` returns each lease through the assigner's `Release` after its deny-list write, `terminateUserSessions` runs the deny-list step before its first cancel write and returns the pre-revoke state with each row, and `podTerminateFanOut` releases through `ReleaseRevokedBinding`. CODE-7 step 1 returns a pool lease to its pool even though the same step deny-lists that pool credential, because `lease.CredentialKey()` carries no user. | Build its full_revoke edits on CODE-7. Settle the deny-list granularity (C3, Decision 18), including the pool lease that step 1 returns while its credential is deny-listed, and own the session set, the listing, and the remaining halves of F-11.4.10. |

## Deliverable index

- SPEC-1 — `spec/05_runtime-registry-and-pool-model.md`, `spec/12_storage-architecture.md`, `spec/28_communication-channels.md`, `spec/10_gateway-internals.md`, `spec/06_warm-pod-model.md`, `spec/29_communication-scenarios.md`, `spec/04_system-components.md` — state `REG-SLOTCOUNT` as a per-pod set keyed by session with a keyed, idempotent release, and align the sites that define the register as a counter.
- SPEC-2 — `spec/04_system-components.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`, `spec/28_communication-channels.md`, `spec/29_communication-scenarios.md` — name the gateway's claim delete by a release that ends the occupancy without reuse in the §4.6.3 writer set, and point every other claim-outcome site at it.
- SPEC-3 — `spec/11_policy-and-controls.md` — state full_revoke's release after the cancel write, the pre-running reclaim, and the deny-list ordering, and drop the `USER_REVOKED` reason.
- CODE-1 — `pkg/gateway/storage/slotcounter/`, `pkg/gateway/session/sessionstore/`, `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`, `cmd/lenny-controller/main.go` — the slot set primitive, the keyed callers, the removal-gated disposition, the detached rollbacks, and the bounded retry.
- CODE-2 — `pkg/gateway/podlifecycle/podsession/reclaim.go`, `pkg/gateway/sessionserver/usage.go`, `pkg/gateway/sessionserver/bind_rollback.go` — the slot-aware pre-running reclaim.
- CODE-3 — `pkg/gateway/sessionserver/finalize.go`, `pkg/gateway/sessionserver/session_terminal.go` — the finalize exits release through the applied failure write's hook.
- CODE-4 — `pkg/gateway/session/createdsweeper/`, `cmd/lenny-gateway/` — delete the created sweeper.
- CODE-5 — `pkg/gateway/sessionserver/terminal_hook.go`, `pkg/gateway/sessionserver/terminal_binding.go`, `pkg/gateway/sessionserver/stream_failure.go` — the hook's stale-binding step and the shared generation predicate.
- CODE-6 — `pkg/gateway/sessionserver/terminal_hook.go` — the slot-accounting lock on the hook's release step.
- CODE-7 — `pkg/gateway/externalapi/admin/`, `pkg/gateway/sessionserver/`, `cmd/lenny-gateway/user_revocation.go`, `pkg/gateway/podlifecycle/podterminate/propagator/` — full_revoke releases through the existing releasers after the deny-list step.
- TEST-1 — `tests/testinfra/lifecycleharness/`, `tests/tier2_component/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier9_security/`, `tests/tier11_docs/` — the harness extension and the cross-tier tests.
- REC-1 — `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` — finding closures, annotations, filings, and the remediation-plan tick.
