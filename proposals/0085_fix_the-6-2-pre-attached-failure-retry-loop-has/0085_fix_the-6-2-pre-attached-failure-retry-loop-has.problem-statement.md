# Problem: The §6.2 pre-attached retry policy does not state which seams it covers or when it stops for a terminal session

## Statement

The §6.2 **Pre-attached failure retry policy:** in spec/06_warm-pod-model.md states that a failure in any state before `attached` triggers an automatic gateway retry. The pod is marked `failed` and released, the gateway re-claims a new pod, and it replays the setup sequence from the beginning. The policy allows at most 2 retries (3 attempts) with 500ms and 1s backoff, and its **Scope:** bullet says that retries apply per client request and that each retry claims a fresh pod. The paragraph names no lifecycle seam to which it is confined, and it names no stop condition for a session that a terminal writer moves to a terminal state while the loop runs.

The policy text assumes a single atomic client request in which no session row exists yet. That assumption holds on `POST /v1/sessions` and `POST /v1/sessions/start`: §7.1's atomicity paragraph says a failure rolls back without persisting the session row, and the combined path in code claims, prepares, and launches before `store.Create`. On those paths no client request, terminal endpoint, or watchdog can reach the session while setup runs, so the terminal-mid-loop hazard cannot arise there. The assumption fails at every seam where a persisted row exists during pre-attached setup:

- `POST /v1/sessions/{id}/finalize` runs workspace materialization, setup commands, and credential assignment while the row is `finalizing`. It prepares against the pod claimed at create and recorded in `pod_assignment`.
- `POST /v1/sessions/{id}/start` on a concurrent-workspace pool materializes the reserved slot and runs setup while the row is `ready`.
- Delegated-child materialization claims a pod and runs setup against a child row already committed to `created`.

At those seams, `POST /v1/sessions/{id}/terminate` (which admits `created`, `finalizing`, and `ready`), `DELETE /v1/sessions/{id}` (which admits any non-terminal state), and the pre-running watchdogs (including the `maxFinalizingTimeoutSeconds` watchdog that writes `failed` with reason `FINALIZE_TIMEOUT`) can move the row to a terminal state mid-setup. Among the terminal triggers, only the §15.1 terminate row says the gateway "aborts the in-progress setup". The DELETE row says only "Force-cancels the session and releases resources", and the watchdog row states only the transition to `failed`. Under defaults the watchdog can fire mid-loop by construction: `maxFinalizingTimeoutSeconds` (600s) only has to be at least `setupTimeoutSeconds` (300s), and 3 attempts that each use a 300s setup window exceed 600s.

A re-claim at a persisted-row seam also conflicts with the durable binding. At finalize, "each retry claims a fresh pod" would replace the row's `pod_assignment` mid-request. §7.1 says only that a finalize-block failure "reclaims the claimed pod via the §6.2 pre-attached disposition" and does not mention retry, and the spec/29 off-holder matrix finalize row runs the sequence against the pod named on the row. Meanwhile the §6.2 **Client visibility:** bullet names `/finalize` among the endpoints that surface failures "on exhaustion", and the §15.1 `SETUP_COMMAND_FAILED` row says a non-deterministic setup-window failure "is recovered with a fresh pod per Section 6.2" at finalize, `/v1/sessions/start`, `/{id}/start`, and `/resume`. The spec therefore reads both ways. This is the ambiguity proposal 0083 recorded.

The gateway implements no §6.2 pre-attached retry loop on any path. `handleFinalize` calls `prepareAtFinalize` once and moves the row to `failed` on the first error. The create-and-start path runs `startOnPod` once and rolls back. `/start` answers `STARTING_FAILED` on the first `launchOnPod` error and leaves the row `ready`. `runWithQueue` re-enters acquisition only on pool-exhaustion sentinels and holds no pod between attempts. Binder comments say an error is returned "so the gateway retries on a fresh pod", and no caller retries. The defect is therefore a specification gap that constrains a future implementation. It is not observable in the current tree.

The proposal resolves the question in order. First, it decides which seams the §6.2 re-claim-and-replay retry covers. The lenses converged on the option of confining the retry to the pre-persist atomic create unit and stating that persisted-binding seams (finalize, a reserved slot at `/start`, and delegated-child materialization) take the §6.2 pre-attached disposition (reclaim, fail, and return the endpoint's envelope) without re-claiming. That option matches the tree, needs no code change, resolves 0083's recorded ambiguity, and makes a terminal-state stop condition unnecessary. If the proposal instead keeps the retry at any persisted-row seam, it must state the stop condition for that seam: a re-read of the session row under lock before each re-claim, what the stopped attempt releases (the claimed pod and any minted credential lease), what the client sees (reusing 0082's staged 409 `INVALID_STATE_TRANSITION` outcome at finalize), which fence it relies on (0082's conditional finalize writes or a pre-reclaim row re-read), and how `pod_assignment` and the buffered uploads are rebound. Either answer also settles what TEST-GAPS T-6.2.10 can test.

Aborting an attempt that is already running on another replica stays out of scope. That covers terminate's "aborts the in-progress setup", DELETE's force-cancel of an in-flight attempt, and the watchdog. 0082 records the terminate abort as a separate unimplemented gap.

The §5.2 **Slot retry policy** is outside this proposal. The only retry loop in the gateway is the §5.2 loop `applySlotRetryPolicy`. It never re-reads the session row between attempts, but it runs only when `bindConcurrentSlot` finds no live create-time reservation, which is the §7.3 resume-rebuild path under `resuming` or a slotless row. A reserved slot is bound through `BindReservedSlot` with no retry. On the reachable path, §7.2 **Mid-resume terminal transitions — snapshot-close semantics** already requires aborting re-attach work on `resuming → cancelled` or `resuming → completed`, and the missing abort in the loop is a gap against that existing rule. Whether a reserved slot is exempt from §5.2 retry is already recorded as a separate 0083 defect. At the spec level §5.2 also covers slot failures after `running`, so it has no single pre-attached case to receive the same rule. At most, 0085 may add a one-sentence §5.2 cross-reference if the finalize-seam decision has a direct slot analog.

## Evidence

- (verified) spec/06_warm-pod-model.md §6.2 **Pre-attached failure retry policy:** (Max retries 2, backoff 500ms and 1s, **Scope:** "Retries apply per client request, not per pod. Each retry claims a fresh pod.", **Exhaustion:**, and **Client visibility:**, which names `/finalize`, `POST /v1/sessions`, and `/start`). No stop condition for a terminal session appears.
- (verified) spec/06_warm-pod-model.md §6.2 state table `finalizing` row: the `maxFinalizingTimeoutSeconds` watchdog (default 600s) transitions the session to `failed` with reason `FINALIZE_TIMEOUT`, and `maxFinalizingTimeoutSeconds` must be at least `setupTimeoutSeconds` (300s).
- (verified) spec/15_external-api-surface.md §15.1 preconditions table: the terminate row ("For `finalizing` and `ready`, the gateway aborts the in-progress setup ... releases the pod, and marks the session `completed`"), the DELETE row ("Force-cancels the session and releases resources"), and the finalize row (no mention of retry).
- (verified) spec/15_external-api-surface.md §15.1 error table `SETUP_COMMAND_FAILED` row: a non-deterministic setup-window failure "is recovered with a fresh pod per Section 6.2".
- (verified) spec/07_session-lifecycle.md §7.1 **Atomicity of session creation (steps 2–8).**: the session row is not persisted on failure, and a finalize-block failure "reclaims the claimed pod via the §6.2 pre-attached disposition".
- (verified) spec/07_session-lifecycle.md §7.2 **Pre-attached vs. post-attached failure visibility.** routes pre-attached failures to the §6.2 retry policy. §7.2 **Mid-resume terminal transitions — snapshot-close semantics.** requires aborting re-attach work on a mid-resume terminal transition.
- (verified) spec/05_runtime-registry-and-pool-model.md §5.2 **Slot retry policy** (`sessionPolicy.slotRetries`, default 1, "analogous to" §6.2, no terminal-session stop condition).
- (verified) spec/29_communication-scenarios.md off-holder matrix: the finalize row runs the workspace, setup, and credential sequence against the pod named by the row's `pod_assignment`, and the terminate and DELETE rows state that `created`, `finalizing`, and `ready` have no coordinating replica.
- (verified) spec/04_system-components.md orphaned `SandboxClaim` detection: a bound claim whose pod no active session references is reclaimed after `claimOrphanTimeout` (default 5 minutes).
- (verified) pkg/gateway/sessionserver/sessionserver.go `handleFinalize`: one `prepareAtFinalize` call, then `failSession` and `writePodClaimError` on error, with no loop. `handleDelete` writes `StateCancelled` unconditionally.
- (verified) pkg/gateway/sessionserver/finalize.go `prepareAtFinalize`: a single `podBinder.Prepare` against `row.PodAssignment`, with reclaim-only disposition on failure.
- (verified) pkg/gateway/sessionserver/start.go `mintClaimStartPersist`: mint and claim precede `store.Create`, and a `startOnPod` error rolls back with no row. `handleStart`: one `launchOnPod`, `STARTING_FAILED` on error, row stays `ready`. `MaterializeDelegatedChild`: loads a persisted `StateCreated` child row, then claims and runs `startOnPod`.
- (verified) pkg/gateway/sessionserver/start.go `bindConcurrentSlot`, `failReservedSlot` ("The reserved slot is not re-reserved and retried"), `maxSlotRetries = 1`, and `applySlotRetryPolicy` (loops `BindSlot` with no session-row read). A search for attempt loops in pkg/gateway/sessionserver and pkg/gateway/podlifecycle/podsession finds only this loop and the seal backoff in seal.go.
- (verified) pkg/gateway/podlifecycle/podsession/binder.go: `Prepare` and `Launch` comments say the error is returned so the gateway retries on a fresh pod. `failPhase` drains the pod and revokes an assigned lease.
- (verified) pkg/gateway/runtime/watchdog/watchdog.go: `ReasonFinalizeTimeout`, `DefaultMaxFinalizingStateSeconds = 600`, and a sweep that moves `created`, `finalizing`, `ready`, and `starting` rows to `failed` under a same-state guard, independent of any in-flight request.
- (verified) proposals/0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for summary: status Approved (2026-09-27). It lists the terminate abort as unimplemented and states "handleFinalize implements no such retry today, so the gap is in the specification rather than the tree. Human decision on 2026-09-27: a separate proposal takes this gap". The sign-off text names DELETE and the finalizing watchdog as the terminal writers. Terminate is added here from the spec.
- (verified) proposals/0083_fix_four-spec-sites-disagree-on-the-finalize-workspace-failure-envelope summary: status Implemented. It records "The §6.2 pre-attached retry does not run at finalize ... Whether the finalize seam is meant to be exempt is not stated." and, as a separate defect, §5.2's slot retry against the reserved-slot bind.

## Who observes it

No client, operator, or gateway replica observes the defect today. No shipped path implements the §6.2 pre-attached loop, so no retry runs after a terminal write. The reader who observes it is the implementer who closes 0083's recorded divergence by building the §6.2 retry at finalize, or at another persisted-row seam, from the spec text as written. TEST-GAPS T-6.2.10 already asks for a tier-4 test of the invisible retry-and-recover loop as though that loop exists.

## What breaks if nothing changes

A literal implementation of §6.2 at a persisted-row seam would re-claim a fresh warm pod and replay workspace materialization, setup commands, and credential assignment for a session that DELETE, terminate, or a watchdog had already ended. The terminal writer's reclaim targets the pod named on the row, so the freshly claimed pod is referenced by no active session and is reclaimed only by orphan-claim garbage collection after `claimOrphanTimeout`. Under 0082's approved exit dispositions, a lease minted by the replay is revoked at the handler's exit, but `handleFinalize` issues no pod reclaim after a lost exit write. The leaked pod and the setup commands that execute for an ended session remain the concrete harms. Under defaults the finalize watchdog can fire during a 3-attempt loop, so the window is reachable in normal operation once such a loop exists. The spec also continues to read two ways on whether finalize retries, and the tier-4 test in T-6.2.10 has no settled target.

## Findings this unblocks

- TEST-GAPS T-6.2.10 (the invisible pre-attached retry-and-recover test), whose target depends on which seams the retry covers.
- The 0083 defect "The §6.2 pre-attached retry does not run at finalize", which this proposal resolves by deciding the finalize seam's applicability.

## Prior art considered

- No landed or pending proposal stages a stop condition or a seam-applicability rule for the §6.2 retry. A search of proposals/ for "pre-attached" and "retry loop" finds only 0082 and 0083, and both hand the gap on.
- Proposal 0082 (Approved, not yet applied in this tree) stages SPEC-1: when another request or process moves the session to a terminal state during finalization, the session keeps the terminal state and the finalize call returns 409 `INVALID_STATE_TRANSITION` with `details.currentState`. Its CODE-1 adds `finalizingPrecondition`, which makes the finalize ready write and every failure write admit only `finalizing`. If any persisted-row seam keeps the retry, 0085 reuses this outcome and this guard as its stop check and response. SPEC-1 is neutral on whether terminate aborts setup.
- Proposal 0083 (Implemented) records the finalize-seam exemption as unstated and, as a separate defect, records that §5.2 does not exempt a slot reserved at create from its slot retry. It lists implementing the §6.2 retry at finalize as out of scope.
- Proposal 0007 (Implemented) designed `/finalize` as fail-once: "A failure in any step fails the finalize and surfaces the corresponding error; the pod is reclaimed per the §6.2 pre-attached disposition". It stated that the §6.2 retry policy itself was unchanged and moved only the endpoint enumeration. That combination is the origin of the ambiguity.
- The spec contains no mechanism that already stops the loop. The terminate row's abort statement covers only terminate and has no implementation.
- Orphan-claim garbage collection exists in the spec and in code (`warmpool.ClaimGarbageCollector`, recorded closed in BUILD-GAPS). It is the only backstop for a pod claimed by a replay after the session ended.
- BUILD-GAPS has no finding that the §6.2 pre-attached retry loop is unbuilt. If the proposal keeps the loop at any seam, a build-gap finding for it is owed.

## Validated premises

### Premise lens (verdict: revise)

- **Stands.** §6.2 mandates re-claim and replay up to 2 retries per client request, and no bullet stops the loop when the session becomes terminal.
- **Stands with a nuance.** The terminal triggers are asymmetric. Terminate "aborts the in-progress setup" for `finalizing` and `ready`. DELETE says only "releases resources", which does not name an abort of in-progress setup. The watchdog row states only the transition to `failed`.
- **Strengthened.** The watchdog can fire mid-loop under defaults, because 3 attempts with a 300s setup window exceed the 600s default. In code the watchdog sweeps `finalizing` rows and writes `failed`/`FINALIZE_TIMEOUT` independently of any in-flight request.
- **Stands and goes further.** No §6.2 pre-attached retry loop exists anywhere in the gateway. The only loops are the §5.2 slot loop and the pool-exhaustion queue, which holds no pod between attempts.
- **Refuted for the create paths.** The implicit premise that a persisted row can be moved to a terminal state while pre-attached setup runs does not hold on `POST /v1/sessions/start` or `POST /v1/sessions`, because those paths persist the row only after claim, prepare, and launch succeed. The hazard is confined to seams with a persisted row: `/finalize` (`finalizing`), the two-step `/start` on a concurrent-workspace pool (`ready`), and delegated-child materialization (`created`). Verified against `mintClaimStartPersist`, `handleStart`, and `MaterializeDelegatedChild`.
- **Partially refuted.** The claim that the §5.2 slot retry is a second loop with the same question does not hold. A reserved slot is never retried, and the reachable slot-retry loop runs on the resume-rebuild path under `resuming`, where §7.2's snapshot-close rule already requires aborting re-attach work. The loop does not implement that abort, which is a gap against the existing §7.2 rule. §5.2 also covers post-`running` slot failures.
- **Stands.** The finalize-seam question is real in both directions. **Client visibility:** frames `/finalize` errors as the outcome on exhaustion, while **Scope:** conflicts with the create-time claim bound through `pod_assignment`, and §7.1 mentions only the disposition.
- **Stands.** Only orphan-claim GC would reclaim a pod claimed by a post-terminal retry, after `claimOrphanTimeout` (5 minutes).
- **Stands.** The 0082 and 0083 records say what the statement reported.
- **Context.** The current tree does not yet carry 0082's conditional writes. `handleDelete` and terminate write the terminal state unconditionally, and the finalize ready write does not guard on `finalizing`. Any stop-condition design must name the fence it relies on.

### Evidence lens (verdict: stands)

- **Verified.** Every spec citation in the original statement: the §6.2 retry policy and `finalizing` row, the §15.1 terminate, DELETE, and finalize rows, the §7.1 atomicity paragraph, the §7.2 pre-/post-attached paragraph, the §5.2 slot retry policy, and the spec/29 off-holder rows.
- **Verified.** `handleFinalize` runs no retry loop, and no §6.2 retry loop exists on create-and-start or `/start`. Binder comments promise a caller retry that no caller performs.
- **Verified.** `applySlotRetryPolicy` does not re-check session state, and it is reached only when no live reserved slot exists.
- **Verified.** 0083 is Implemented and contains the quoted defect. 0082 is Approved and assigns the gap to a separate proposal. The 0082 sign-off names DELETE and the watchdog, and terminate is an addition consistent with the spec.
- **Verified.** Orphan-claim GC exists in the spec and in code.
- **Tangential drift.** The slot loop uses the hard-coded `maxSlotRetries = 1` and ignores the configured `sessionPolicy.slotRetries`. This belongs to a §5.2 proposal.

### Prior-art lens (verdict: stands)

- **Stands.** No proposal solves the problem. 0082 and 0083 record it and hand it on.
- **Stands.** The spec has no existing stop mechanism.
- **Partial prior art.** 0082 SPEC-1 and CODE-1 define the terminal-during-finalize response and the `finalizing` guard, which 0085 reuses if a stop condition is needed.
- **Origin.** 0007's fail-once finalize design, combined with an unchanged §6.2 policy, produced the ambiguity.
- **Context.** T-6.2.10 assumes a loop that does not exist, and BUILD-GAPS records no finding for the unbuilt loop.

### Scope lens (verdict: revise)

- **Stands.** The spec gap is real.
- **Revised.** The applicability question is broader than finalize, because no exclusive-pool path runs a §6.2 retry. The decision covers every pre-attached seam, or it states explicitly which seams are out of scope.
- **Stands.** Applicability and the stop condition form one problem in that order, because an exemption removes most of the stop-condition design.
- **Cut.** §5.2's slot retry moves to a separate proposal or to at most a one-sentence cross-reference. It runs at a different lifecycle point with different terminal triggers, and its budget ignores `sessionPolicy.slotRetries`.
- **Stands with an addition.** The cross-replica abort carve-out stays, and it names DELETE and the watchdog as well as terminate.
- **Qualified by verification.** The scope lens described the slot loop as running at launch on create-and-start and `/start`. Code shows a reserved slot on those paths binds through `BindReservedSlot` with no retry, so the loop is reachable only on resume-rebuild or a slotless row.

### Impact lens (verdict: revise)

- **Stands.** No shipped path runs the §6.2 loop, so nothing observes the missing stop condition today.
- **Stands.** The harm lands on a future implementation of the §6.2 retry at a persisted-row seam, which would leak a pod to orphan GC and run setup for an ended session.
- **Narrowed.** Once 0082 lands, a lease minted by such a replay is revoked at exit. The leaked pod and the executed setup commands remain.
- **Revised.** The §5.2 part of the original statement is imprecise. The shipped slot loop has no pre-attached reach, is bounded to one extra slot bind, and its terminal-race question belongs to §7.3 recovery.
- **Stands.** The applicability decision carries more weight than the stop condition and comes first. The stop condition constrains a future implementation.

### Alternatives lens (verdict: revise)

- **Stands.** No re-claim retry loop exists, so the defect is a spec-text gap plus 0083's recorded ambiguity.
- **Stands.** The stop condition depends on applicability. If persisted-binding seams take the §6.2 disposition without re-claiming, no loop runs while a terminal writer can intervene, and no lock re-read, abort-release rule, or new client answer is needed.
- **Stands.** The §6.2 text is written for a one-shot create and conflicts with the durable binding. A finalize re-claim replaces `pod_assignment` mid-request and claims a pod that neither the terminal writer nor 0082's exit dispositions reclaim. An exemption removes the cause, and a stop condition only narrows the window.
- **Stands.** The spec already partly reads as the exemption (§6.2 **Client visibility:** and §7.1's disposition wording), so a clarifying sentence in §6.2, and possibly in §7.1, closes the gap without code change.
- **Stands.** The §5.2 slot retry is misframed as part of this problem. The reserved-slot exemption is recorded separately in 0083.
- **Refuted.** A "no change" reading does not hold for the spec, because §6.2 retries failures "in any state before `attached`" and the §15.1 `SETUP_COMMAND_FAILED` row promises recovery "with a fresh pod per Section 6.2" at finalize and `/start`.
