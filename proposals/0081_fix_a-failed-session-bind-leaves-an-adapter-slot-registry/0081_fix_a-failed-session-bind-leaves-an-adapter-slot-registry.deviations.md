# Deviations: A failed session bind leaves a stale adapter slot registry entry

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: DOCS-4 also edits the tier-11 gate's diagnosis comment

**Status:** proposed

**Reported by:** step S23 (DOCS-4: drop the reporting clause from the per-slot cleanup sentences).

**What the proposal says.** DOCS-4 in the non-spec changes consists of two page edits. On `docs/reference/execution-modes.md` and on `docs/operator-guide/security-principles.md`, the reporting clause ", and the adapter reports its outcome to the gateway" is deleted from the per-slot cleanup sentence. The Testing section lists no test file for DOCS-4.

**What landed instead.** Both page edits landed. The step also removed the same clause ("and the adapter reports its outcome to the gateway") from the `// diagnosis:` comment of `TestPerSlotCleanupStatedOnEverySessionModeRow` in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`. That comment paraphrases the page sentence. Only the comment changed, and no assertion in the test moved.

**Why.** The step reported that the comment repeated the claim SPEC-3 withdraws. Leaving the comment unchanged would mean the gate's diagnosis states behavior the spec no longer defines.

**What a later reader would otherwise get wrong.** A reader comparing DOCS-4 against the tree would expect the DOCS-4 change to touch only the two documentation pages and no test file. That reader would treat the comment edit in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` as unexplained scope, or would conclude that the Testing section's "no file" statement for DOCS-4 is still accurate for the landed tree.

## Proposed: SCHEMA-1 claim-register rows seeded WIRED ahead of their production readers

**Status:** proposed

**Reported by:** step S9 (SCHEMA-1: additive proto window for bind attempt, mid_session, teardown pairing, reclaim outcome, refusal codes).

**What the proposal says.** The SCHEMA-1 "Claim register" paragraph gives both of its rows the WIRED status because a production reader ships in the same change. In that paragraph the adapter compares the token and the teardown fields inside `Server.Shutdown`, and the gateway compensation reads the reclaim outcome. Checklist step S9 lands both rows as WIRED at this step.

**What landed instead.** Following checklist step S9, `scripts/seed-claim-register.py` and `tests/claim-map.json` record both rows as WIRED at commit 695dba330. The rows are "ShutdownRequest bind_attempt and unconditional_teardown teardown precondition" and "bind_attempt carried on the six slot-entry requests and stamped once on create". The production readers that those rows name do not exist yet. The tree has no `bindattempt.go` mint, no Binder compensation path, and no non-generated caller of `GetBindAttempt` or `GetUnconditionalTeardown`. Those readers arrive in later code steps.

**Why.** The proposal contradicts itself. Its WIRED rationale assumes that the readers land in the same change as the rows, while its step ordering places the readers in later code steps. The conformance finding states that this step needs no code change and that a human decides between two options: keep the rows WIRED ahead of their readers, or move each row's seeding into the step that ships its reader. The step made no change so that the decision stays with a human.

**What a later reader would otherwise get wrong.** A reader who takes the SCHEMA-1 paragraph at face value would expect a production reader for each WIRED row to exist at commit 695dba330. That reader would treat the claim register as evidence that the adapter already enforces the teardown precondition and that the gateway already reads the reclaim outcome. Between step S9 and the later code steps that ship those readers, neither behavior exists in the tree.

## Proposed: CODE-9 adds only the finalize-stage error injection to the concurrentAdapter fixture

**Status:** proposed

**Reported by:** step S10 (CODE-9: gateway observability (superseded counter, leaked-slots gauge, SlotReclaim hook, workspace_finalize stage)).

**What the proposal says.** The tier-1 gateway test list in the non-spec changes extends the `concurrentAdapter` fixture with per-stage error injection for the finalize, setup, and credential-assignment stages. The same list adds `PrepareWorkspace` and `AssignCredentials` handlers, typed refusal details, and per-request recording to the fixture.

**What landed instead.** This step adds only the `finalizeErr` injection to `concurrentAdapter`, in the `concurrentAdapter` struct and its `FinalizeWorkspace` method in `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`. The step drives the `stageWorkspace` failure through an `uploadFile` source that has no blob store, which fails before any pod-side RPC is issued.

**Why.** The step reported that its instruction limits it to finalize-stage injection. The remaining fixture extensions serve the later compensation and refusal cases, such as step S19, and the step reported that they land with those cases.

**What a later reader would otherwise get wrong.** A reader comparing the fixture against the tier-1 test list at the commit that lands step S10 would expect setup-stage and credential-assignment-stage injection, the `PrepareWorkspace` and `AssignCredentials` handlers, typed refusal details, and per-request recording to be present. That reader would also expect the workspace-preparation failure case to use fixture injection, when it uses a source with no blob store instead.

## Proposed: CODE-9 adds export_test.go to the podsession package

**Status:** proposed

**Reported by:** step S10 (CODE-9: gateway observability (superseded counter, leaked-slots gauge, SlotReclaim hook, workspace_finalize stage)).

**What the proposal says.** Neither the files-touched list in the non-spec changes nor the target list of this step names `pkg/gateway/podlifecycle/podsession/export_test.go`.

**What landed instead.** The step added `pkg/gateway/podlifecycle/podsession/export_test.go`. The file exports `slotFailureWorkspacePrep` and `slotFailureWorkspaceFinalize` to the external test package as `SlotFailureWorkspacePrepForTest` and `SlotFailureWorkspaceFinalizeForTest`.

**Why.** The proposal requires the workspace-stage case to assert against the stage constants rather than against string literals. `slotbinder_test.go` is in package `podsession_test`, and the step reported that it has no other way to read the unexported constants.

**What a later reader would otherwise get wrong.** A reader checking the tree against the files-touched list would find `export_test.go` with no entry that accounts for it, and would treat the file as unexplained scope.

## Proposed: CODE-9 credits slotbinder_test.go under §16.1 in tests/spec-map.json

**Status:** proposed

**Reported by:** step S10 (CODE-9: gateway observability (superseded counter, leaked-slots gauge, SlotReclaim hook, workspace_finalize stage)).

**What the proposal says.** The test section of the non-spec changes credits the new tier-11 file to §16.1 in `tests/spec-map.json`. It names no `tests/spec-map.json` change for `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`.

**What landed instead.** `tests/spec-map.json` also credits `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` under §16.1.

**Why.** The new slotbinder case carries a `// spec:` annotation that names §16.1. The step reported that the tier-0 gate `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` fails unless every section that a `slotAddressCaseFiles` file annotates is credited in the spec map.

**What a later reader would otherwise get wrong.** A reader comparing `tests/spec-map.json` against the test section would expect §16.1 to credit only the new tier-11 file. That reader would treat the `slotbinder_test.go` entry as an unplanned edit, and could remove it without knowing that the tier-0 gate depends on it.

## Proposed: CODE-8 moves the refusal guard into a shared Binder helper

**Status:** proposed

**Reported by:** step S12 (CODE-8: reclaim closures return a typed refusal without draining the pod).

**What the proposal says.** The CODE-8 section for `binder.go` in the non-spec changes writes the refusal guard inline in each closure body. On a refusal, the Prepare closure calls `cl.Close()` and returns. Otherwise it runs `failPhase` and then calls `cl.Close()`. The Launch closure has the same body and passes the literal `true` in place of `leaseAssigned`.

**What landed instead.** Each closure is a one-line call to a new shared helper, `Binder.reclaimOnFailure(ctx, sb, cl, leaseAssigned, sessionID, cause)`, in `pkg/gateway/podlifecycle/podsession/binder.go`. The helper defers `cl.Close()`. When `IsSlotBindRefusal` reports that the cause is a refusal, the helper logs the refusal and returns without calling `failPhase` or draining the pod. Otherwise it calls `failPhase`. The Prepare closure passes `leaseAssigned` and the Launch closure passes `true`, as the proposal states.

**Why.** The step reported that the behavior is unchanged. The shared helper removes the duplicated guard from the two closures, as `code-best-practices.md` requires. The helper also emits one log line recording that a refusal left the pod undrained.

**What a later reader would otherwise get wrong.** A reader comparing `binder.go` against the CODE-8 section would look for the guard inside the Prepare and Launch closure bodies and would not find it there. That reader would find `reclaimOnFailure` with no entry that accounts for it, and would not expect the refusal log line that the proposal does not mention.

## Proposed: CODE-14 per-member close budget test split into three subtests

**Status:** proposed

**Reported by:** step S15 (CODE-14: per-slot guard and removing-site table (minus Shutdown rows)).

**What the proposal says.** The Testing section entry "Tier 1, CODE-14's §10.1.4 per-member close budget" describes a single case. In that case `onHoldTimeout` fires, and the test asserts that each member's close deadline is strictly later than the previous member's and strictly later than the deadline of the context under which the pass acquires guards.

**What landed instead.** `TestHoldTerminationGivesEachMemberItsOwnCloseBudget_spec_10_1_4` in `pkg/adapter/holdstate_test.go` has three subtests. The `onHoldTimeout` subtest asserts a nil `Err()` on every member's close context and strictly increasing deadlines across members. A second subtest drives `terminateHeldSession` with a pass context whose deadline the test knows, and asserts that every member's deadline is later than that deadline. A third subtest hands an already-cancelled pass context to `terminateHeldSession` and asserts that each uncontended member still closes on a live context.

**Why.** The step reported that `onHoldTimeout` mints the pass context internally, so the test cannot read that context's deadline without a seam added to production code only for the test. The step reported that the three subtests together pin the property the single case describes: each member receives its own context, minted after guard acquisition, that no earlier member's guard wait can consume.

**What a later reader would otherwise get wrong.** A reader comparing the test against the Testing section would look for one `onHoldTimeout` case that compares member deadlines with the pass context's deadline. That reader would find that the `onHoldTimeout` subtest makes no comparison against the pass context's deadline, and could conclude that the comparison is untested, when the second subtest makes it through `terminateHeldSession`.

## Proposed: CODE-14 slot_guard_not_acquired warning names the removing site as the caller

**Status:** proposed

**Reported by:** step S15 (CODE-14: per-slot guard and removing-site table (minus Shutdown rows)).

**What the proposal says.** CODE-14 states that the `slot_guard_not_acquired` warning names the slot identifier and "the caller". The proposal gives the signature of `releaseSessionSlot` as `(ctx, sessionID)`, with no parameter that carries a caller.

**What landed instead.** `warnSlotGuardNotAcquired` in `pkg/adapter/bindattempt.go` logs a structured `caller` field whose value is the name of the removing site, either `releaseSessionSlot` or `terminateHeldSession`. The call sites are `releaseSessionSlot` in `pkg/adapter/slotsession.go` and `terminateHeldSession` in `pkg/adapter/holdstate.go`.

**Why.** The step reported that the signature the proposal gives for `releaseSessionSlot` has no caller parameter, so the removing site's own name is the `caller` value, and the test asserts on that value. The warning distinguishes the removing sites from each other. It does not name the handler that invoked `releaseSessionSlot`.

**What a later reader would otherwise get wrong.** A reader taking "the caller" in CODE-14 to mean the code path that triggered the removal would expect the warning to identify the handler that called `releaseSessionSlot`. That reader would find only `releaseSessionSlot` or `terminateHeldSession` in the `caller` field, and could not use the warning alone to tell which handler reached `releaseSessionSlot`.

## Proposed: CODE-1 and CODE-15 Shutdown answer and teardown extracted into Server methods

**Status:** proposed

**Reported by:** step S16 (CODE-1 + CODE-15: Shutdown teardown pairing and attempt comparison; CODE-9 untokened-entry series; CODE-14 Shutdown rows).

**What the proposal says.** CODE-1 writes `answerShutdown` as a closure inside `Shutdown`. CODE-15 writes the removing arm's teardown inline in the handler body after the second decision.

**What landed instead.** `answerShutdown` is a method on `Server` (`func (s *Server) answerShutdown` in `pkg/adapter/session.go`). The removing arm's teardown is a separate method, `tearDownReclaimedSlot`, which takes a `reclaimedSlot` struct holding the predicates captured under `s.mu` and returns `(exitedCleanly, completed)`. `Shutdown` still defers the guard unlock and the `completed`-gated hold release in the stated order, so the guard outlives the hold and the scrub starts before the hold's deferred release runs.

**Why.** The step reported that `code-best-practices.md` sets a function-size limit and that the inline body would have made `Shutdown` roughly 150 lines. The step reported that the predicates, the order of acts, the deferred release, and the response values match the proposal.

**What a later reader would otherwise get wrong.** A reader comparing `Shutdown` against CODE-1 and CODE-15 would look for an `answerShutdown` closure and an inline teardown in the handler body and would find neither. That reader would find `answerShutdown`, `tearDownReclaimedSlot`, and the `reclaimedSlot` struct with no entry that accounts for them, and would have to confirm separately that the deferred unlock and hold release still run in the proposal's order.

## Proposed: CODE-14 tier-7a slot reclaim hold race cases narrowed to the adapter's exported surface

**Status:** proposed

**Reported by:** step S16 (CODE-1 + CODE-15: Shutdown teardown pairing and attempt comparison; CODE-9 untokened-entry series; CODE-14 Shutdown rows).

**What the proposal says.** The Testing section's tier-7a entry for `slot_reclaim_hold_race_test.go` states the following. The mid-session upload arm asserts a gateway-side HTTP 502 `UPSTREAM_ERROR`. The lock-order case records each guarded section's path-work interval. The reclaim-against-an-admitted-section case covers `FinalizeWorkspace`, a multi-frame `PrepareWorkspace`, and a `Resume` parked inside `workspace.ExtractTree`, with a §10.1.4 second run. The fifth arm, the re-decision under the guard, lives in the same file.

**What landed instead.** The tier-7a file drives only the adapter's exported surface.

1. The mid-session arm asserts the adapter's `Aborted` refusal and its wall-time bound.
2. The lock-order case runs `FinalizeWorkspace`, `RunSetup`, `PrepareWorkspace`, `Resume`, and a removing `Shutdown` concurrently and checks that none deadlocks. It checks interval overlap only on the `Runtime.Start` interval (from `Resume`) and the `Runtime.Close` interval (from `Shutdown`).
3. The admitted-section case covers a multi-frame `PrepareWorkspace` parked between frames, with the non-matching superseded fourth arm, and a `Resume` parked inside `Runtime.Start`.
4. The step added a guard-lifetime case.

The fifth arm is a deterministic tier-1 test, `TestTheRemovingShutdownDecidesAgainUnderTheGuard_spec_4_7_1`, in `pkg/adapter/slotsession_test.go`. The `FinalizeWorkspace` arm, the `ExtractTree` park, and the §10.1.4 second run are absent from tier 7a.

**Why.** The step reported that an external test package has no seam inside `workspace.MaterializeWithPolicy` or `ExtractTree`, and that `ExtractTree` needs a chunked checkpoint transport. The step reported that the gateway's HTTP 502 mapping sits outside the adapter package the step changes. The step reported that the fifth arm needs `releaseSessionSlotUnderGuard` to run under a held guard, which only the internal package reaches deterministically.

**What a later reader would otherwise get wrong.** A reader taking the Testing section as the inventory of tier-7a coverage would conclude that the gateway's HTTP 502 `UPSTREAM_ERROR` mapping for the mid-session arm, the per-section path-work intervals, the `FinalizeWorkspace` arm, the `ExtractTree` park, and the §10.1.4 second run are pinned at tier 7a. None of them is. That reader would also look for the fifth arm in `slot_reclaim_hold_race_test.go` and find it in `slotsession_test.go` at tier 1, and would find a guard-lifetime case the proposal does not list.

## Proposed: CODE-1 socket-runtime co-tenancy assertion tested at two levels

**Status:** proposed

**Reported by:** step S16 (CODE-1 + CODE-15: Shutdown teardown pairing and attempt comparison; CODE-9 untokened-entry series; CODE-14 Shutdown rows).

**What the proposal says.** The co-tenancy hazard sibling assertion goes in `socketruntime_test.go` beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`.

**What landed instead.** The step added two tests. `TestSocketRuntimeProcessSurvivesTheReclaimOfAnUnstartedSlot_spec_4_7_1` in `socketruntime_test.go`, an external-package test, drives the reclaim through `adapter.Server.Shutdown` and asserts that the listener still accepts connections. `TestShutdownOfAnUnstartedEntryLeavesTheSocketRuntimeIntact_spec_4_7_1` in `slotsession_test.go` asserts the connected flag and the listener.

**Why.** The step reported that it combined the proposal's `slotsession_test.go` case and its `socketruntime_test.go` sibling into one property and tested that property at both levels.

**What a later reader would otherwise get wrong.** A reader looking for a single sibling assertion beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` would find two tests with different names in two files, both annotated §4.7.1 rather than §5.2, and could mistake the `slotsession_test.go` test for an unrelated addition.

## Proposed: S16 spec-map credits extended to every section a new case annotates

**Status:** proposed

**Reported by:** step S16 (CODE-1 + CODE-15: Shutdown teardown pairing and attempt comparison; CODE-9 untokened-entry series; CODE-14 Shutdown rows).

**What the proposal says.** The spec-map listing gives `slotsession_test.go` a whole-file 4.7.1 credit and enters `slot_reclaim_hold_race_test.go` under §5.2.

**What landed instead.** In `tests/spec-map.json`, `slotsession_test.go` also gains a 16.1 credit, because the untokened-counter case annotates §16.1. `slot_reclaim_hold_race_test.go` is credited under 5.2 and 4.7.1, because its cases annotate both sections. `bindattempt_orderings_test.go` is credited under 4.7.1, 7.1, and 7.4. `socketruntime_test.go` gains 4.7.1 and 5.2 credits.

**Why.** The step reported that the Testing preamble requires every section a new case annotates to be credited in the spec map.

**What a later reader would otherwise get wrong.** A reader checking `tests/spec-map.json` against the proposal's listing would find credits the listing does not name, for §16.1, §4.7.1, §7.1, §7.4, and §5.2, and could take them for unreviewed additions to remove.

## Proposed: CODE-2 slot-claim result struct and shared start-rollback helpers

**Status:** proposed

**Reported by:** step S18 (CODE-2: start confirms the registry still holds its own attempt's entry).

**What the proposal says.** CODE-2 in the non-spec changes says `claimSessionSlot` and `claimSessionSlotUnderLock` report the token the entry carried. It gives no signature. Its `StartSession` snippet places the rollback body (`Runtime.Close`, `cancelPodMCPIfRuntimeIdle`, and the `Aborted` status) inline at each call site.

**What landed instead.** Both functions return a `slotClaim` struct with the fields `fresh`, `startMCP`, and `attempt` in place of positional return values (`pkg/adapter/slotsession.go`). The rollback body that `StartSession` and `Resume` share lives in one helper, `Server.rollbackUnconfirmedStart` (`pkg/adapter/runtimegeneration.go`). The SDK-warm refusal lives in `Server.refuseUnconfirmedSDKWarmStart` (`pkg/adapter/sdkwarm.go`). The step reported that the behavior matches the behavior CODE-2 states.

**Why.** The step reported that the project rules prefer a shared helper over duplicating the same rollback block at two call sites, and that a struct avoids a five-value return from `claimSessionSlotUnderLock`.

**What a later reader would otherwise get wrong.** A reader following the CODE-2 snippet would look for the rollback inline in `StartSession` and for positional returns from the claim functions. That reader would find neither, and would find the rollback and the SDK-warm refusal in helpers in `runtimegeneration.go` and `sdkwarm.go` that the proposal does not name.

## Proposed: CODE-2 SDK-warm refusal when DemoteSDK fails

**Status:** proposed

**Reported by:** step S18 (CODE-2: start confirms the registry still holds its own attempt's entry).

**What the proposal says.** The SDK-warm arm of CODE-2 calls `SDKWarmRuntime.DemoteSDK` and clears `s.sdkConnected`. It does not state the behavior when `DemoteSDK` returns an error, and it does not mark that case as the implementor's choice.

**What landed instead.** A `DemoteSDK` error is logged under `sdk_warm_unconfirmed_start_demote_failed`. `sdkConnected` is still cleared, and the RPC still answers `codes.Aborted` (`refuseUnconfirmedSDKWarmStart` in `pkg/adapter/sdkwarm.go`).

**Why.** The step reported that the refusal must answer `Aborted` regardless of the demotion outcome, and that the proposal leaves the error case unspecified.

**What a later reader would otherwise get wrong.** A reader of the proposal alone could assume that a `DemoteSDK` failure propagates as the RPC's error or leaves `sdkConnected` set. Neither holds: the failure is logged, the flag is cleared, and the status is `Aborted`.

## Proposed: CODE-2 tier-7a race file annotates §4.7.1 and §7.1 only

**Status:** proposed

**Reported by:** step S18 (CODE-2: start confirms the registry still holds its own attempt's entry).

**What the proposal says.** The tier-1 subsection annotation for the CODE-2 cases in `slotsession_test.go` is `// spec: §4.7.1; §5.2`. The spec-map entry for the tier-7a race file sits under 4.7.1 and 7.1.

**What landed instead.** The `slotsession_test.go` cases carry the `§4.7.1; §5.2` annotation as written. The annotations in `tests/tier7a_load_local/slot_bind_attempt_race_test.go` cite §4.7.1 and §7.1 only. The step reported that an earlier draft of that file also cited §5.2 and failed the tier-0 credit gate.

**Why.** The step reported that `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` requires every annotated section to be credited in `spec-map.json`, and that the proposal's landing table credits the tier-7a file only under 4.7.1 and 7.1.

**What a later reader would otherwise get wrong.** A reader who carries the tier-1 annotation over to the tier-7a file, and adds §5.2 to its cases, would fail the tier-0 credit gate unless the same change also adds a §5.2 credit for that file in `spec-map.json`.

## Proposed: CODE-13 compensation and outcome share a helper

**Status:** proposed

**Reported by:** step S19 (CODE-13 and `noteCompensationOutcome`: the gateway compensates every post-connection failure and failed resume).

**What the proposal says.** CODE-13's staged `materializeSlot` body calls `compensateFailedSlotBind` and then calls `noteCompensationOutcome(outcome, compensationCause(err), ...)` inline. The `Binder.Resume` failure branch makes the same two calls inline.

**What landed instead.** A helper, `compensateAndNote` in `pkg/gateway/podlifecycle/podsession/slotbinder.go`, runs `compensateFailedSlotBind` and then `noteCompensationOutcome`. Both call sites use it: the `materializeSlot` wrapper, and a new helper, `Binder.failResume` in `binder.go`, that holds the resume failure branch.

**Why.** The step reported that the two sites would otherwise repeat the same pair of calls, and that sharing them guarantees that no compensation is sent without its outcome being counted. The step reported that the behavior is unchanged.

**What a later reader would otherwise get wrong.** A reader following the CODE-13 snippet would look for the two calls inline in `materializeSlot` and in `Binder.Resume`. That reader would find them in `compensateAndNote` and `Binder.failResume`, which the proposal does not name.

## Proposed: CODE-13 resume failure always carries a SlotBindError

**Status:** proposed

**Reported by:** step S19 (CODE-13 and `noteCompensationOutcome`: the gateway compensates every post-connection failure and failed resume).

**What the proposal says.** CODE-13 says the `Binder.Resume` failure branch returns its error with a `*SlotBindError` in the chain. It also says: "On an exclusive pool reserveResumeSlot returns an empty slot id, so the error carries one too."

**What landed instead.** The error chain always wraps a `*SlotBindError` at stage `"resume"`. On an exclusive pool, its `SlotID` field is empty (`failResume` in `binder.go`).

**Why.** The step reported that the quoted sentence is ambiguous, and that it read "carries one too" as the error carrying an empty slot id rather than omitting the `SlotBindError`.

**What a later reader would otherwise get wrong.** A reader who takes the sentence to mean that an exclusive-pool resume failure carries no `SlotBindError` would expect `errors.As` to fail on that path. In the landed code, `errors.As` succeeds and yields a `SlotBindError` with an empty `SlotID`.

## Proposed: Tier 4 late-reclaim arm redelivers the first compensation

**Status:** proposed

**Reported by:** step S19 (CODE-13 and `noteCompensationOutcome`: the gateway compensates every post-connection failure and failed resume).

**What the proposal says.** The tier-4 late-reclaim arm (Testing, "Tier 4, the late-reclaim arm") has bob's retry bind and start cleanly, after which the compensation for the first attempt arrives and is answered superseded.

**What landed instead.** The first attempt's compensation is delivered at the failure site and removes its registry entry. The test captures that request and sends it again after the retry has bound and started. The adapter answers `SUPERSEDED`, and the retried session still echoes (`late_reclaim_is_superseded` in `tests/tier4_integration/concurrent_workspace_test.go`).

**Why.** The step reported that the ordering the proposal describes cannot occur. While the first attempt's entry stands, the identity gate refuses the retry, which is the behavior the refused-retry arm pins. The retry therefore binds only after that compensation has landed, and a late arrival can only be a second delivery of the same compensation.

**What a later reader would otherwise get wrong.** A reader of the proposal would expect the test to delay the first compensation until after the retry starts. The test instead delivers it on time and replays a captured copy, so the arm pins the adapter's answer to a duplicate compensation rather than to a delayed first delivery.

## Proposed: Tier 4 main arm injects the unaddressed frame

**Status:** proposed

**Reported by:** step S19 (CODE-13 and `noteCompensationOutcome`: the gateway compensates every post-connection failure and failed resume).

**What the proposal says.** The tier-4 main arm states that "an unaddressed session-scoped frame on alice's Attach stream relays again."

**What landed instead.** A wrapper around the `SocketRuntimeProcess` (`injectingRuntime` in `tests/tier4_integration/concurrent_workspace_test.go`) writes an unaddressed response frame to the runtime output. The test asserts that the frame reaches alice's Attach stream.

**Why.** The step reported that the echo-concurrent runtime stamps a `sessionId` on every frame it writes and so never produces an unaddressed frame. Injecting one is the only way the test observes the slot-count rule of §28.5.3.

**What a later reader would otherwise get wrong.** A reader would expect the runtime under test to emit the unaddressed frame itself. The frame comes from the test's `injectingRuntime` wrapper, and the echo-concurrent runtime does not produce one.

## Proposed: Additional slot-retry disposition test

**Status:** proposed

**Reported by:** step S19 (CODE-13 and `noteCompensationOutcome`: the gateway compensates every post-connection failure and failed resume).

**What the proposal says.** The landing table lists no new case in `pkg/gateway/sessionserver/slotretry_test.go` for this step. It lists only the change to the fake's signature and the fake's carrying of the disposition.

**What landed instead.** The step added `TestSlotRetryReleasesWithTheReclaimDisposition_spec_7_1` and registered it per case in `tests/spec-map.json` under 7.1, 5.2, and 6.2.

**Why.** The step reported that the change to `applySlotRetryPolicy`, which now passes `sbe.Leaked`, needs a test that fails against the call as it stood before the fix.

**What a later reader would otherwise get wrong.** A reader comparing the landing table with the tree would find a test case and three `spec-map.json` credits that the proposal does not list, and could take them for unrelated additions.

## Proposed: Reserved and resume accounting calls go through package functions

**Status:** proposed

**Reported by:** step S20 (CODE-5: shared accounting helper, resume classifier, retryable envelope for both refusals, per-slot leak record).

**What the proposal says.** CODE-5 names the accounting targets. They are the retry-policy tail, `bindConcurrentSlot`'s `BindReservedSlot` branch calling `accountSlotFailure` inline, and `resumeOnPod`'s `Resume` failure branch calling `accountSlotFailure` inline with `s.podBinder` and the server's collaborators.

**What landed instead.** The reserved branch and the resume branch each call a small package function over the `slotBinder` seam. The reserved branch calls `failReservedSlot`, which sits next to `bindConcurrentSlot` in `pkg/gateway/sessionserver/start.go`. The resume branch calls `accountResumeSlotFailure`, which sits above `holdOrFailOnResumeError` in the same file. Each function calls `accountSlotFailure` with the arguments CODE-5 states, and `bindConcurrentSlot` and `resumeOnPod` pass `s.podBinder` and the server's collaborators to them.

**Why.** The step reported that `s.podBinder` is a concrete `*podsession.Binder`. The tier-1 cases "The reserved bind path reaches the accounting" and the `resumeOnPod` accounting arms of "The resume path" need a fake binder, and with the call inline no tier-1 test could drive either branch. The behavior and the `accountSlotFailure` signature are unchanged.

**What a later reader would otherwise get wrong.** A reader of CODE-5 would look for `accountSlotFailure` called directly inside `bindConcurrentSlot` and `resumeOnPod`. The call sits one level down, in `failReservedSlot` and `accountResumeSlotFailure`, and the tier-1 cases exercise those functions through the `slotBinder` seam.

## Proposed: Slot-address inventory row and credits for the resume setup demotion test file

**Status:** proposed

**Reported by:** step S20 (CODE-5: shared accounting helper, resume classifier, retryable envelope for both refusals, per-slot leak record).

**What the proposal says.** The Testing section's tier-0 `slotAddressCaseFiles` row rule states that a row lands for each new or edited file that gains a call into the slot claim surface. The landing table's S20 rows name no inventory row for `resume_setup_demotion_internal_test.go`.

**What landed instead.** `tests/tier0_static/spec_map_slot_address_registration_test.go` gains a `slotAddressCaseFiles` row for `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`. `tests/spec-map.json` gains per-case entries for that file's already-shipped `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` under 7.3, 15.1, 6.2, and 7.2, in addition to the entries for the new cases.

**Why.** The step reported that the new resume accounting case imports `slotstate`. The tier-0 derived-inventory rule therefore requires the file in the inventory, and the credit gate then requires every case in the file, including the already-shipped one, to be credited under the sections its annotation names.

**What a later reader would otherwise get wrong.** A reader comparing the landing table with the tree would find an inventory row and four `spec-map.json` credits for a pre-existing test case that the proposal does not list, and could take them for unrelated additions.

## Proposed: Additional per-slot leak record regression tests

**Status:** proposed

**Reported by:** step S20 (CODE-5: shared accounting helper, resume classifier, retryable envelope for both refusals, per-slot leak record).

**What the proposal says.** The landing table's S20 rows list only the named cases in `slothealth_test.go` and `scrubreporter_seams_test.go`, from "The leak record is per slot." through the distinct identifiers and the `RecordLeak` slot parameter.

**What landed instead.** The step also added `TestRecordLeakIsKeyedBySlot_spec_5_2` in `pkg/gateway/runtime/slothealth/slothealth_test.go` and `TestDrainLedgerCountsARepeatedSlotOnce_spec_5_2` in `pkg/gateway/session/recycle/scrubreporter_seams_test.go`, each with per-case `tests/spec-map.json` entries under 5.2 and 6.2.

**Why.** The step reported that the task requires a regression test at the tracker and at the ledger that fails against the per-pod counter. Without these cases, each package's only per-slot assertion runs through the sessionserver helper.

**What a later reader would otherwise get wrong.** A reader comparing the landing table with the tree would find two test cases and four `spec-map.json` credits that the proposal does not list, and could take them for unrelated additions.

## Proposed: CONF-1 tier-10 battery names registry state from probe outcomes

**Status:** proposed

**Reported by:** step S22 (CONF-1: wire-level and in-process conformance of the published contract).

**What the proposal says.** The non-spec changes entry "Conformance battery for CONF-1, tier 10", together with the CONF-1 note, describes tier 10 as the in-process battery. It states that a tier-10 failure names the internal state that produced it rather than a wire answer, and that the file is worth having for that diagnosis.

**What landed instead.** The tier-10 battery in `tests/tier10_conformance/slot_bind_attempt_conformance_test.go` runs the shared case bodies in `tests/testinfra/bindattempt` in process and reads registry state only through the outcomes of probe `Shutdown` calls. On failure, `wantOutcome` (through `registryState` in `tests/testinfra/bindattempt/requests.go`) names the registry state that the §4.7.1 reclaim-outcome rule fixes for the observed outcome and for the wanted outcome. The named states are no entry, an entry the named attempt owns, and an entry another attempt owns or that carries no token. The battery does not read the entry's stamp value, its started flag, or the reclaim hold directly.

**Why.** The step reported that the tier-10 file lives in an external test package and cannot reach the unexported registry in `pkg/adapter`, and that an in-package `_test.go` companion is invisible to it. The only other route is an exported introspection API. §15.4 does not define one, and earlier commits on this branch removed it from the production `Server`. Rule 15 gives each outcome a single fixed meaning, so the battery can name registry state from the handlers' answers alone. The step asked that a human decide whether the proposal's "names the internal state" wording still stands.

**What a later reader would otherwise get wrong.** A reader of the CONF-1 note would expect a tier-10 failure to report the registry entry's internal fields, such as the stamp, the started flag, or the reclaim hold. The failure message names only the registry state derived from the probe outcome, and the battery never inspects those fields.

## Proposed: CONF-1 removes the tier-0 slot registry export gate

**Status:** proposed

**Reported by:** step S22 (CONF-1: wire-level and in-process conformance of the published contract).

**What the proposal says.** The CONF-1 file list and the landing rows for this step name the tier-3 cases, the tier-10 file with its `tests/spec-map.json` entry and its `slotAddressCaseFiles` row, and the `ABSENT` claim-register row. They name no tier-0 export gate.

**What landed instead.** The step removed `tests/tier0_static/adapter_slot_registry_encapsulation_test.go`, its `tests/spec-map.json` credits under 4.7.1 and 15.4, and its `slotAddressCaseFiles` row. No automated check now prevents a future exported registry reader from being added to `pkg/adapter`.

**Why.** The step reported that the gate's own doc comment stated that neither §4.7.1 nor §15.4 states the rule it enforced, so its spec-map credits claimed coverage the test did not provide. The gate was also outside the scope the proposal stages. The step noted that if the constraint is wanted, it has to go through the proposal pipeline under a section that states it.

**What a later reader would otherwise get wrong.** A reader who saw the export gate in earlier commits on this branch would assume it still guards the adapter's Go surface. A reader who finds no gate would not know that one existed and was removed deliberately, and could reintroduce it with the same unsupported spec-map credits.
