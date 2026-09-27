# Summary: The §6.2 pre-attached retry loop has no stop condition for a terminal session

## Summary

**Problem statement.** The §6.2 **Pre-attached failure retry policy:** re-claims a fresh pod and replays setup after any failure before `attached`, up to 2 retries per client request. It names no seam it is confined to and no stop condition for a session that terminate, DELETE, or a pre-running watchdog ends mid-setup. The policy assumes a request with no persisted session row. That holds for the §7.1 creation of `POST /v1/sessions` and `POST /v1/sessions/start` and fails at finalize, `/{id}/start`, `/resume`, and delegated-child materialization, where a re-claim would run setup for an ended session and leak a pod to orphan GC. Other spec sites promise a fresh-pod recovery or a replacement claim at those seams while §7.1 names only the pre-attached disposition, so the spec reads both ways. The tree runs no §6.2 loop anywhere, so the defect constrains a future implementation.

**What changes.**

- `spec/06_warm-pod-model.md` §6.2: the policy's lead sentence defers to its **Scope:** bullet, and the Scope bullet confines the retry to requests that have persisted no session row (SPEC-1).
- `spec/15_external-api-surface.md` §15.1: the `SETUP_COMMAND_FAILED` row drops its "recovered with a fresh pod per Section 6.2" clause (SPEC-2).
- `spec/04_system-components.md` §4.7 and `spec/29_communication-scenarios.md` §29.2 step 23: the `ConfigureWorkspace`-and-`DemoteSDK` failure drops "a replacement is claimed" (SPEC-3).
- `pkg/gateway/podlifecycle/podsession/binder.go` and `pkg/gateway/sessionserver`: comments and test annotations that the spec edits make false are corrected, with no code token change (CODE-1).
- `pkg/gateway/sessionserver/start_pod_test.go`: the finalize failure test counts SandboxClaim creates (TEST-1).
- `BUILD-GAPS.md` and `TEST-GAPS.md`: a new OPEN finding records the unbuilt combined-route loop, and T-6.2.10 is retargeted (RECORDS-1).

**Decisions.**

- The retry is narrowed to the pre-persist creation unit. Four of the six design stances converged on this, and it touches the fewest sites.
- Narrowing is chosen over deleting the §6.2 gateway retry everywhere. Deletion removes a spec-promised recovery where the validated problem shows no hazard and rewrites more of §6.2, §7.2, and §5.2.
- The seam rule has one home, the §6.2 **Scope:** bullet. §7.1 and §7.2 already defer to the §6.2 policy and are not edited, and the **Exhaustion:** and **Client visibility:** bullets read correctly because SPEC-1 defines the single attempt's failure as the exhaustion outcome.
- "Under this policy" keeps §6.2 from speaking for the §5.2 **Slot retry policy**. §5.2 is not edited, and the reserved-slot exemption stays 0083's separately recorded defect.
- `/resume` is listed among the persisted-row requests with §7.3 named as its recovery, which corrects the old §15.1 row's attribution in the direction the tree already takes.
- SPEC-2 and SPEC-3 delete clauses rather than naming a governing policy. A named policy would restate the Scope rule, and at `/{id}/start` the §6.2 policy is the wrong one because a launch failure in `starting` follows the §7.2 edges and §7.3.
- No production behavior changes. The tree already makes one attempt at every persisted-row seam.
- The `Bind`, `Resume`, and `ErrNoConcurrentSlot` comments in `binder.go` are not edited. `Bind`'s only production caller is the §7.3 resume rebuild, where re-claiming a fresh pod stays correct.
- TEST-1 counts SandboxClaim CREATE calls through a wrapper on the binder's client. A count of remaining claims cannot distinguish "no retry" from "a retry that also failed".
- F-6.2.27 is scoped to `POST /v1/sessions/start`. `POST /v1/sessions` runs only the pre-check and claim before persisting, and §7.1 requires a claim failure there to fail fast.
- T-6.2.10 is retargeted to the combined route, because its current target, a retry at finalize, is excluded by SPEC-1.

**Watch out for.**

- F-6.2.27 cites proposal 0085 and must stay OPEN when 0085 lands. 0085 builds no loop, so the implementation pipeline's step that closes BUILD-GAPS findings referencing the proposal must skip it.
- 0082's code steps edit `start.go`, `sessionserver.go`, and the `start_pod_test.go` fixture family. S5 and S6 land after them, and every site is located by symbol and quoted text.
- The SPEC-1 Scope text lists `POST /v1/sessions` among the pre-persist requests. That text does not license wrapping the create-time claim in `create.go` in a retry loop; §7.1 **Atomicity of session creation (steps 2–8).** requires the claim failure to return the retryable 503 immediately.
- At `/{id}/start`, a runtime-launch failure after the session entered `starting` follows the §7.2 `starting` edges and §7.3. The SPEC-1 Scope text governs only the §6.2 policy and does not override them.

## Goals

- The spec states in one place which requests the §6.2 re-claim-and-replay retry covers, and no other spec site asserts a fresh-pod re-claim at a persisted-row seam.
- No §6.2 retry runs while a terminal writer can reach the session row, so the policy needs no terminal stop condition.
- No code comment or test annotation claims a §6.2 fresh-pod retry that the amended spec excludes.
- A test pins that a failed finalize claims no replacement pod.
- The unbuilt combined-route loop has a BUILD-GAPS finding, and T-6.2.10 has a target the spec defines.

## Non-goals

- Deleting the §6.2 gateway-internal pre-attached retry at every seam and replacing it with a fail-once disposition (the spec-first and contrarian designs). Both designs resolve the problem. Deletion lost because it removes a spec-promised recovery on the unit where the problem shows no hazard, rewrites the whole §6.2 paragraph, the §7.2 visibility paragraph, and the §5.2 "analogous to" parenthetical, and changes the `POST /v1/sessions/start` product contract without a problem that requires it. Under deletion, `POST /v1/sessions/start` would surface a transient warm-pod or setup failure as a retryable 503 for the client to retry, matching today's tree. The choice changes a product promise, so the review loops confirm it with the reviewer.
- Keeping the retry at a persisted-row seam behind a stop condition (a row re-read under lock before each re-claim, reuse of 0082's 409 and `finalizingPrecondition`, release of the claimed pod and minted lease, and a `pod_assignment` and upload rebind). A read is a check rather than a fence, the rebind conflicts with terminal writers that reclaim by `pod_assignment`, and the design adds a loop, a fence, a release rule, and a rebind rule to a tree with no loop.
- Keeping the retry only at finalize with 0082's `finalizingPrecondition` as the fence. That guard fences only the exit writes and cannot stop a re-claim or a setup replay.
- Keeping the retry at delegated-child materialization. A cascade cancel or the created-TTL sweep reclaims nothing there, so each extra attempt claims a pod that only orphan GC collects.
- Bounding the loop by the remaining `FINALIZE_TIMEOUT` budget. A single attempt is already bounded by `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds`.
- No change. The §6.2 lead and **Scope:**, the §15.1 `SETUP_COMMAND_FAILED` row, the §4.7 `ConfigureWorkspace` row, and §29.2 step 23 read as mandating a fresh-pod re-claim at persisted-row seams.
- Editing the §6.2 **Client visibility:** or **Exhaustion:** bullets. SPEC-1 defines the single attempt's failure as the exhaustion outcome, and an edit would add a second statement of the seam rule.
- Editing §7.1 **Atomicity of session creation (steps 2–8).** or §7.2 **Pre-attached vs. post-attached failure visibility.** Both defer to the §6.2 policy, and a restatement would drift.
- Anchoring the term "pre-attached disposition" in the §6.2 lead. §7.1 and the code already use it, and it resolves to the policy's "marked failed and released" sentence.
- A §5.2 cross-reference exempting a reserved slot at `/{id}/start`, or a deferral clause in §6.2 Scope naming §5.2 and §7.3. "Under this policy" already keeps §5.2 separate, and the reserved-slot exemption belongs to 0083's recorded defect.
- Removing the §5.2 "analogous to the pre-attached failure retry policy" parenthetical. It is needed only under deletion.
- An ordering clause requiring each retry to claim only after the previous attempt's pod is released. It is new normative behavior the problem does not require; F-6.2.27's gap text carries the expectation for the builder.
- Stating in §6.2 that a failed attempt revokes its credential lease, or that `pod_assignment` never changes on failure. `failPhase` and §7.1 step 23 already cover the lease, and "claims no replacement pod" already rules out a rebind.
- Stating in §6.2 that finalize moves the row to `failed`. 0082 governs the finalize exit writes, and §6.2 does not own per-endpoint row outcomes.
- New pinning tests at `/{id}/start`, at delegated-child materialization, and on the combined route, and an assertion that `pod_assignment` is unchanged. Neither persisted seam has code that could claim a replacement, the combined route's single attempt is the unbuilt-loop gap, and the create counter makes the `pod_assignment` assertion redundant.
- Adding `// spec: §6.2 (Scope)` lines at the `handleFinalize`, `handleStart`, and `MaterializeDelegatedChild` failure branches. Those comments stay true, and CODE-1 is limited to comments the spec edits falsify.
- Correcting the `Bind` doc comment's stale description of its callers. The drift predates 0085, and CODE-1 leaves the `Bind` doc comment unchanged.
- Building the §6.2 retry loop on the combined route. It is a new mechanism the spec gap does not require, recorded as F-6.2.27.
- Specifying or implementing an abort of an in-flight attempt by terminate, DELETE, or the pre-running watchdogs, including a cross-replica abort. The problem statement puts it out of scope, and 0082 records the terminate abort.
- The §5.2 slot loop's missing row re-read against §7.2 snapshot-close and its hard-coded `maxSlotRetries = 1`. The problem statement's scope lens cut them.
- Defining the `attached` boundary or replacing the term. It is recorded below as a defect not staged.
- Fixing the exclusive-pool `/{id}/start` stale binding, the unguarded `transitionStart`, the missing `DemoteSDK` fallback, or the delegated-child materialization envelope. Each predates 0085, exists with or without a retry, and is recorded below.
- A new error code, `details` field, state, or endpoint. The existing fallback rows carry the outcome and the client retry.
- Editing `docs/reference/error-catalog.md`. Its rows describe a client retry and claim no internal §6.2 recovery at finalize or `/start`.
- Editing 0082's or 0083's proposal records. 0083 is Implemented and immutable; the resolution is recorded in this proposal's **Impacts on other proposals**.

## Open decisions for human to make

None yet. The review loops write this section.

## Defects in the shipped tree that this proposal does not stage

- **Exclusive-pool `/{id}/start` launch failure leaves a stale binding.** The binder reclaims the pod and lease, but `handleStart` leaves the row `ready` with `pod_assignment` naming the drained pod, and it never moves the row to `starting` before launch. A client `STARTING_FAILED` retry reconnects to the dead binding until `READY_TIMEOUT`, so the §15.1 statement that a retry rebinds a fresh pod does not hold there. It predates 0085 and needs its own proposal.
- **`transitionStart` is unconditional.** A terminal write during a single `/start` or delegated-child attempt can be overwritten by the running commit. 0082 guards only finalize, and `/start` serialization belongs to the holder-claim work 0081 assigned.
- **`Binder.Launch` implements no `DemoteSDK` fallback.** It reclaims immediately on a `ConfigureWorkspace` error, while the §4.7 row and §29.2 step 23 promise a `DemoteSDK` call and pod-warm fallback. SPEC-3 touches only the replacement clause of those sites.
- **`attached` is undefined.** The §6.2 policy and §7.2 still use `attached`, which the pod diagram no longer defines after proposal 0002. Settling the boundary is a terminology change outside this fix.
- **A transient delegated-child materialization failure may surface as `INTERNAL_ERROR`.** This is a lead to verify: `materializeToolError` in `mcptools_register.go` appears to map it that way, and §8.2 states no envelope for it.

## Impacts on other proposals

| Proposal | Status | What 0085 does to it | What it must do |
|:--|:--|:--|:--|
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Approved; SPEC-1 landed on the parent branch, code steps pending | Resolves the defect 0082 handed on ("The §6.2 pre-attached retry loop does not stop when the session becomes terminal") by making persisted-row seams take no retry, so no stop condition is needed. SPEC-2 edits the §15.1 `SETUP_COMMAND_FAILED` row, which is disjoint from the finalize precondition row 0082 SPEC-1 edited. | Nothing. Its code steps land before 0085's S5 and S6. |
| 0083_fix_four-spec-sites-disagree-on-the-finalize-workspace-failure-envelope | Implemented | Resolves its recorded defect "The §6.2 pre-attached retry does not run at finalize": finalize is exempt. Its separate §5.2 reserved-slot defect is unaffected. | Nothing. It is immutable. |
| 0007_fix_eager-pod-claim-at-create-and-finalize-materialization | Implemented | Aligns §6.2 with its fail-once finalize design. | Nothing. It is immutable. |

## Deliverable index

- **SPEC-1** (`spec/06_warm-pod-model.md`): confines the §6.2 pre-attached retry to requests that have persisted no session row.
- **SPEC-2** (`spec/15_external-api-surface.md`): drops the §6.2 fresh-pod recovery clause from the §15.1 `SETUP_COMMAND_FAILED` row.
- **SPEC-3** (`spec/04_system-components.md`, `spec/29_communication-scenarios.md`): drops the replacement-claim clause from the `ConfigureWorkspace`-and-`DemoteSDK` failure.
- **CODE-1** (`pkg/gateway/podlifecycle/podsession/binder.go`, `pkg/gateway/sessionserver/start.go`, `pkg/gateway/sessionserver/podclaimerror_internal_test.go`, `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`): comment and annotation sweep.
- **TEST-1** (`pkg/gateway/sessionserver/start_pod_test.go`): pins that a failed finalize claims no replacement pod.
- **RECORDS-1** (`BUILD-GAPS.md`, `TEST-GAPS.md`): records F-6.2.27 and retargets T-6.2.10.
