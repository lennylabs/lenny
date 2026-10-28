# Deviations — 0101_new_gateway-replicas-have-no-way-to-ask-the-replica

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: TEST-7 also changes the §28 staged-text supersession test

**Status:** proposed

**What the proposal says.** TEST-7 in the non-spec changes lists four targets: `spec_28_register_writers_test.go`, `off_holder_matrix_stated_outcome_test.go`, `redis_key_prefix_registry_test.go`, and `rediskeys.go`. It names no other tier-11 file whose pins the staged spec edits break.

**What landed instead.** Step S3 also changed `tests/tier11_docs/spec_28_index_rows_test.go`. The `stagedLineSupersession` type gained a `removes` field, and `applySupersessions` gained a bounded removal path through a new `removalMismatch` helper. A new `assertRemovalsAreBounded` self-test checks that path. The `stagedLineSupersessions` register gained four entries attributed to `proposals/0101_new_gateway-replicas-have-no-way-to-ask-the-replica`. They remove the `LNK-INTERREPLICA` link row, remove the "LNK-INTERREPLICA carries no channel row" paragraph and its trailing blank line, and rewrite the REG-COORDLEASE Store and Semantics cells.

**Why.** The step reported a gap the proposal did not mark. The subtest `TestSection28IndexRowsResolve_spec_28/staged_section_text_matches_the_landed_file` compares the §28 text that proposal 0067 staged with the landed specification. It failed once the SPEC-1 (a), SPEC-1 (b), and REG-COORDLEASE edits landed. Proposal 0067 is implemented and cannot be edited. The test's supersession register is the existing mechanism for recording a later proposal's rewrite of a staged line, but it handled in-line rewrites only and had no form for deletions, so the step added a removal form. The step requires tier 11 to pass against the landed specification.

**What a later reader would otherwise get wrong.** A reader who takes TEST-7's target list as the complete tier-11 footprint of this proposal would miss the change to `spec_28_index_rows_test.go`. That reader would also miss that the supersession register now records removals as well as rewrites, and that four of its entries come from this proposal.

## Proposed: The WIRED CH-BINDRELEASE claim row names the propagator's current type

**Status:** proposed

**What the proposal says.** In the REC-1 claim-register table, the row "Lifecycle drain releases the pod..." takes as its surface the DrainSessions publish through `bindReleaseFanOut.TerminateUserSessions`. The new row "CH-BINDRELEASE binding-release fan-out" takes as its surface the propagator construction in `cmd/lenny-gateway/adminrouter.go`.

**What landed instead.** Step S4 wrote the drain row's surface as the proposal states, naming `bindReleaseFanOut.TerminateUserSessions` in the EXPLICIT drain row of `scripts/seed-claim-register.py`. That type does not exist until CODE-4 renames the propagator package. The drain row has status ABSENT, and ABSENT rows are not symbol-checked. The WIRED CH-BINDRELEASE row names the propagator construction in `cmd/lenny-gateway/adminrouter.go` over `podTerminateFanOut.TerminateUserSessions` in `cmd/lenny-gateway/user_revocation.go`, which is the type's name at the time of this step.

**Why.** The step reported that a WIRED row has to name symbols that exist when the row lands, and the new tier-11 check verifies that those symbols are declared. The step stated that CODE-4's rename must update this surface to `bindReleaseFanOut` when it lands.

**What a later reader would otherwise get wrong.** A reader who compares the claim register with REC-1 would expect the CH-BINDRELEASE row to name `bindReleaseFanOut` and would not know that the row names `podTerminateFanOut` until CODE-4 lands. A reader implementing CODE-4 who does not know this would leave the WIRED row pointing at a type the rename removes, and the tier-11 symbol check would then fail.

## Proposed: Step S4 also changes tests/spec-map.json to credit §28

**Status:** proposed

**What the proposal says.** REC-1 lists `scripts/seed-claim-register.py`, `tests/claim-map.json`, and the tier-11 currency tests as this step's targets.

**What landed instead.** Step S4 also changed `tests/spec-map.json`. Section 28 now credits `tests/tier11_docs/redis_key_prefix_registry_test.go::TestRedisKeyPrefixRegistryCompleteness_0032`.

**Why.** The step reported that the tier-0 test `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` already failed at HEAD before this step. The S3 commit `dae385078` added a §28.5.7 annotation to that test without crediting §28 in the spec map. The fix is one map line, and the step needed it for the tier-0 smoke check to pass.

**What a later reader would otherwise get wrong.** A reader who takes REC-1's target list as the complete footprint of step S4 would miss the `tests/spec-map.json` change. That reader would also attribute the spec-map entry to REC-1 rather than to the gap that step S3 left behind.

## Proposed: CHART-1 fixes the self-peer rule's position and lands its chart tests

**Status:** proposed

**What the proposal says.** CHART-1 adds the mirror `from` rule to `allow-gateway-ingress` and does not say where it sits in the rule list. TEST-6, which step S15 implements, owns the helm-unittest coverage of the chart change.

**What landed instead.** Step S5 placed the ingress self-peer rule last in `allow-gateway-ingress`, after the bootstrap rule, in `charts/lenny/templates/system-network-policies.yaml`. The same step also added the helm-unittest suite `charts/lenny/tests/holder-forward_test.yaml`, together with tier-5 and tier-11 tests.

**Why.** The step reported that a rule placed last leaves the positional `ingress[N]` assertions in `system-network-policies_test.yaml` valid. It also reported that the test-coverage rule requires a chart change to ship with its tests in the step that makes the change, and that step S15 can extend these tests instead of duplicating them.

**What a later reader would otherwise get wrong.** A reader who takes TEST-6 as the only source of chart test coverage would expect step S15 to create `holder-forward_test.yaml` and would miss that it already exists from step S5. A reader who reorders the rules in `allow-gateway-ingress` would not know that the self-peer rule sits last so that the positional `ingress[N]` assertions in `system-network-policies_test.yaml` hold.

## Proposed: The tier-3 holder-forward contract tests enter through a contract-tagged export

**Status:** proposed

**What the proposal says.** TEST-1 item 6 (Tier 3 contract) states that the tier-3 `holderforward` package checks that a forwarded REST request is byte-equal to the client's request, and that the coordinator's status, headers, and body come back unchanged.

**What landed instead.** Step S7 wrote the tier-3 tests to call the REST forward through `Server.ForwardToHolder`. That method exists only in `pkg/gateway/sessionserver/holder_forward_contract.go`, which builds under the `contract` build tag and wraps the unexported `forwardToHolder` with `r.Body`. The tests do not reach the forward through a session route mounted in `Server.Handler()`.

**Why.** The step reported that no session route calls `forwardToHolder` yet, because the proposal wires the forward into the routes in a later build step. The tier-3 package compiles outside `sessionserver`, so it needs an exported entry point, and the `contract` build tag keeps that entry point out of production builds.

**What a later reader would otherwise get wrong.** A reader who takes TEST-1 item 6 as covering the forward end to end would assume the tier-3 tests exercise a mounted session route in `Server.Handler()`. Those tests exercise only `forwardToHolder` through the contract-tagged `Server.ForwardToHolder`, so they do not cover the route wiring. A reader who sees `Server.ForwardToHolder` would not know that it exists only under the `contract` build tag and is absent from production builds.

## D1 · step S10 · 2026-10-27 · accepted
**Status:** accepted
**Proposal says:** The TEST-3 bullet in `0101_new_gateway-replicas-have-no-way-to-ask-the-replica.non-spec-changes.md` (line 290) states that "The invariant checker asserts that no send is delivered twice". The bullet makes no exception for the held-entry redelivery that §7.2 permits across a coordinator change while the adapter's duplicate suppression (BUILD-GAPS F-7.2.41) is unimplemented.
**Implemented instead:** `classifyDeliveries` in `tests/tier7a_load_local/holder_forward_interleaving_test.go:292-301` exempts one case from the double-delivery failure: a send answered `200 queued` that the harness runtime received exactly twice in a round with a completed handoff. `checkDeliveryCount` still fails every other count above one. After `h.Check` runs, a round that contains the exempt case skips as blocked on F-7.2.41 (`holder_forward_interleaving_test.go:414`). `TestForwardCheckerFailsEveryDoubleDeliveryButTheBlockedHeldRedelivery_spec_7_2` pins the classification, and `TestBlockedSkipsNameOpenFindings_spec_7_2` forces the skip's removal once F-7.2.41 closes. On the gateway side, `pkg/gateway/sessionserver/held_delivery.go` re-reads REG-COORDLEASE before each held entry (commit 11a6ef737), which narrows the window to the minimum the gateway reaches without the adapter's suppression set.
**Why:** The judges found that the spec permits the double delivery the TEST-3 sentence forbids. §7.2 path 6 ("Delivery of held messages") removes an entry only after the runtime has consumed it, and the §7.2 Explicit ACK row makes delivery at-least-once, with duplicates suppressed by the adapter's `delivered_message_ids` set. BUILD-GAPS F-7.2.41 (BUILD-GAPS.md:7600) records that no code implements that set, and the finding belongs to remediation step R20, outside proposal 0101. A fixer inside step S10 had three options, and none closes the finding. Failing the case leaves the tier-7a test permanently red against spec-conforming gateway code. Tolerating it silently was the round-2 form, which the reviewers rejected. Skipping it as blocked is the current form, which the reviewers accept as spec-correct while still reporting the wording gap. Implementing F-7.2.41 is outside this step's scope. The reviewers' own fix fields propose no code change and defer to a human.
**Consequence if the proposal is not corrected:** A reader who takes TEST-3 at its word expects the tier-7a interleaving checker to fail every double delivery. That reader would treat the `heldRedelivery` exception and the blocked skip as a weakened test, and could remove them, which turns the step red on behaviour §7.2 permits. An implementor of F-7.2.41 would not learn from the proposal that closing it is the change that lets the checker enforce the TEST-3 invariant without exception.
**Suggested next step:** correct the proposal
**Evidence:** Commit cfff39fbb ("test(tier7a): fail every double delivery of a forwarded send; skip the held redelivery as blocked on F-7.2.41"), preceded by 11a6ef737 and d3c8b3bcb, which moved the same subject. Round 5 produced no new commit, and the reviewers re-raised the finding as a proposal-wording gap. All three judges ruled with high confidence that the finding is unresolvable in code and is a proposal defect: TEST-3 should have scoped the invariant to exclude the F-7.2.41 window. The judges also separated a resolvable finding that this entry does not cover. `checkForwardSends` (`holder_forward_interleaving_test.go:341-345`) accepts a `200 delivered` receipt for a send that the gateway only held in the DLQ, and the fix is to split the accepted branch by receipt status.

## Proposed: The drain publishes transitioned sessions before it returns a context error

**Status:** proposed

**What the proposal says.** CODE-3 item 2 states that after the transition loop and before the verifying listing, the drain calls `pods.TerminateUserSessions` with the transitioned session ids. The text does not cover the drain's early return when the caller's context ends between sessions.

**What landed instead.** Step S12 changed `DrainSessions` in `pkg/gateway/externalapi/admin/drain.go` so that when the context ends between sessions, the loop breaks instead of returning at once. `publishBindRelease` then publishes the ids already transitioned, and the drain returns the wrapped context error after that publish. The `DrainResult` and the returned error are unchanged.

**Why.** The step reported that §12.8 states that the drain publishes the sessions it moved to a terminal state after its transitions. A drain that its deadline ended has still moved sessions, and a return before the publish would leave the remote bindings of those sessions unreleased. The step also cited TEST-4, which requires that "a cancelled context still delivers the call". `TestDrainSessionsPublishesAfterTheDeadlineEndsTheLoop_spec_12_8` pins this behavior.

**What a later reader would otherwise get wrong.** A reader who takes CODE-3 item 2 as the complete statement of the drain's control flow would expect a cancelled context to return before any publish. That reader could restore the immediate return, which leaves the bindings of already-transitioned sessions held on other replicas and fails the pinning test.

## Proposed: Step S12 adds tier-1 and source-level coverage and defers the tier-4 case to S13

**Status:** proposed

**What the proposal says.** The implementation checklist and TEST-4 place the tier-4 `lifecycleharness` case (a user erasure on replica A releases a binding held on replica B) and the `release_wiring_test` same-fan-out check in the TEST-4 step, S13. The header for step S12 lists tier 4 among its tiers.

**What landed instead.** Step S12 adds no tier-4 test. It adds tier-1 coverage of the publish in `drain_test.go`, `tenant_suspend_test.go`, and `tenantdeletion_test.go`. It also adds a source-level wiring pin, `TestLifecycleDrainsShareTheBindReleaseFanOut_spec_12_8` in `cmd/lenny-gateway/release_wiring_test.go`, which checks that one propagator is built before the erasure runner and reaches all three drains and `WithUserRevocation`. The tier-7a files change only their call signatures.

**Why.** The step reported that the checklist places the tier-4 two-replica case in S13 (TEST-4), and that its hard constraint forbids starting later steps.

**What a later reader would otherwise get wrong.** A reader who trusts the S12 header would assume step S12 landed tier-4 coverage of the binding-release publish. That coverage is owed by step S13. A reader implementing S13 would also not know that `release_wiring_test.go` already holds a same-fan-out pin from step S12, and could add a duplicate check instead of extending that pin.

## Proposed: The TEST-4 subscriber case tests delivery, and the primary-row gate is tested in sessionserver and tier 4

**Status:** proposed

**What the proposal says.** The TEST-4 "Subscriber case" in the non-spec changes adds a case to `pkg/gateway/podlifecycle/bindrelease/propagator_test.go`. That case shows that a CH-BINDRELEASE request releases only the bindings whose primary row is terminal, leaves bindings on a non-terminal primary in place, and returns the released bindings' credential leases.

**What landed instead.** Step S13 changed `TestSubscriberReceivesTheWholeSessionSet_spec_28_5_7` in `propagator_test.go` so that it asserts only that replica B's subscriber receives the whole session set, unfiltered and in order, together with the tenant and the user. Two existing tests pin the primary-row gate against production code. `pkg/gateway/sessionserver/terminal_binding_revoke_internal_test.go::TestReleaseRevokedBindingOutcomes_spec_11_4` checks that a terminal row is released and a non-terminal row is not. The tier-4 test `tests/tier4_integration/drain_bind_release_two_replica_test.go::TestBindReleaseSubscriberReleasesOnlyTerminalBindings_spec_28_5_7` checks the same gate and also covers the credential lease return.

**Why.** The step reported that the `bindrelease` package does not read session state. A tier-1 gate check in that package could test only a fake that implements the gate itself, and the design review found such a test misleading. The `sessionserver` package owns the gate and the lease return, and tier-1 and tier-4 tests cover both.

**What a later reader would otherwise get wrong.** A reader who takes the TEST-4 subscriber case at its word would expect `propagator_test.go` to pin the primary-row gate and the lease return, and would find only a delivery check there. That reader could add a fake-backed gate test to the `bindrelease` package, which tests the fake rather than production code. The same reader would not know that the gate and the lease return are covered in `terminal_binding_revoke_internal_test.go` and in the tier-4 two-replica test.
