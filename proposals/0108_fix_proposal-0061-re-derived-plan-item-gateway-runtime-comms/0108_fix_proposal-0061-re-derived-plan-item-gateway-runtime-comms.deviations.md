# Deviations — 0108_fix_proposal-0061-re-derived-plan-item-gateway-runtime-comms

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: S6 tests registered without a §29.3 map key

**Status:** accepted: `tests/spec-map.json` carries no section key under §29, because §29 Communication Scenarios has no leaf entry in the map, so `validate-maps` cannot resolve one. The S6 tests (`TestForwardedResponsesTurnAtANonHolderIsNotDeliveredInAnyState_spec_29_3`, `TestForwardedResponsesTurnAtTheHolderRunsLocally_spec_29_3`, `TestUnforwardedResponsesTurn_spec_29_3`, `TestOtherKindsKeepTheStateGateAheadOfTheForwardedCheck_spec_29_3`, and `TestResponsesTurnForwardStatesAreRunningOnly_spec_29_3` in `pkg/gateway/sessionserver/holder_forward_test.go`, `TestHolderForwardInnerRoutesOnlyTheResponsesCreate_spec_29_3` in `cmd/lenny-gateway/holder_forward_wiring_test.go`, and the two tier-4 cases in `tests/tier4_integration/holder_forward_responses_test.go`) are registered under 28.5.4 and 15, with the non-storage cases also under 11.5 and the rate-limit case also under 11.1, which is how the existing §29.3 forward tests are registered. Each test name keeps the `_spec_29_3` suffix and a `// spec:` annotation naming §29.3, so the coverage stays traceable (2026-10-30, orchestrator under the owner's delegation)

**Reported by:** step S6 (CODE-3: ForwardKindResponsesTurn, forwardTarget order, ForwardResponsesTurn, and the listener route).

**What the proposal says.** The step's map registration (the task header and non-spec-changes TEST-1) lists the new tests against §29.3 Forwarding, §28.5.4, and §15 (**Open Responses conversation sessions.**).

**What landed instead.** `tests/spec-map.json` has no §29 or §29.3 section key, so the new tests are registered under 28.5.4 and 15. The tests that check §11.5 non-storage are also registered under 11.5, and the rate-limit test is also registered under 11.1. The existing §29.3 forward tests are registered under 28.5.4, 10.1.1, and 7.2 in the same way.

**Why.** `validate-maps` resolves only existing section keys, and adding a new §29.3 section entry to the map is outside this step.

**What a later reader would otherwise get wrong.** A reader who searches `tests/spec-map.json` for §29.3 finds none of the S6 forward tests and could conclude that §29.3 Forwarding has no test coverage from this proposal. The coverage exists under the 28.5.4 and 15 keys.

## Proposed: Tier-4 forward test uses a stand-in Responses handler through a harness hook

**Status:** resolved in step S12: `Harness.WireOpenResponses` in `tests/testinfra/lifecycleharness/responses.go` installs the production Open Responses handler through `Replica.SetResponsesHandler` (`tests/testinfra/lifecycleharness/harness.go`), and both `tests/tier4_integration/holder_forward_responses_test.go` and `tests/tier4_integration/open_responses_held_session_test.go` call it, with no stand-in handler left in the tree (2026-10-30, orchestrator under the owner's delegation)

**Reported by:** step S6 (CODE-3: ForwardKindResponsesTurn, forwardTarget order, ForwardResponsesTurn, and the listener route).

**What the proposal says.** TEST-1 tier 4 states that the two-replica forward test sends a warm continuation to the non-holder, has it served on the holder's pod, and receives 409 `TARGET_NOT_READY` with no DLQ entry when the holder's listener is stopped. The Targets for this step do not include the lifecycle harness.

**What landed instead.** The Open Responses handler routing (CODE-5) has not landed yet. The tier-4 test therefore installs a stand-in `POST /v1/responses` handler on each harness replica. The stand-in calls `Server.ForwardResponsesTurn` with a native-envelope refusal and, on a local outcome, delivers the turn through the replica's executor. To route that path on the public mux and on the forward listener, `tests/testinfra/lifecycleharness/harness.go` gains `Replica.SetResponsesHandler` (in `serveReplica` and `SetResponsesHandler`), which is a file outside the step's Targets.

**Why.** Until CODE-5 lands, the real translator handler cannot call `ForwardResponsesTurn`. The step reports the harness hook as the smallest change that lets the tier-4 test cross the real CH-HOLDERFORWARD listeners, and states that the tier-8 step can reuse it.

**What a later reader would otherwise get wrong.** A reader of the tier-4 test could assume it exercises the production Open Responses translator handler end to end. It exercises the real CH-HOLDERFORWARD listeners and `ForwardResponsesTurn` behind a test-installed handler. A reader of the step's Targets would also not expect the edit to `tests/testinfra/lifecycleharness/harness.go`.

**Resolution (step S12).** The stand-in is gone. `tests/testinfra/lifecycleharness/responses.go` adds `Harness.WireOpenResponses(hold)`, which builds the production Open Responses translator handler on every replica, with the replica's create-and-start service as the binder, the replica's session server as the `Terminal`, a holder forwarder built the way `sessionHolderForwarder` in `cmd/lenny-gateway/credentialsurface.go` builds it, and one shared transcript store, and installs it through `Replica.SetResponsesHandler`. `tests/tier4_integration/holder_forward_responses_test.go` uses it with `hold=true`: the first turn on the coordinator binds the held session, and the continuations carry `previous_response_id`. Both tests keep their assertions. The `served_by` and `forwarded` fields the stand-in echoed are replaced by the response id (`<session>.2` on the forwarded turn, `<session>.3` on the local one, which proves a warm turn on the same session), the forward-listener count, the runtime receipt count, the unchanged lease holder, and, on the refused turn, the unchanged response-record count. `tests/tier4_integration/open_responses_held_session_test.go` drops its own copy of the binder, forwarder, and wiring and calls the same helper. `Replica.SetResponsesHandler` stays: the helper installs the production handler through it, because a second `POST /v1/responses` registration on the inner mux panics.

## Proposed: Resolve step stays an unexported method

**Status:** accepted: `continuity.resolve` in `pkg/gateway/environment/translator/continuity.go` returns `(sessionstore.Session, int, error)` and is the first hop of `rehydrate`, and its only other callers are `handleCreate`'s resolve step and `handleGet` in `open_responses.go`, both in the `translator` package. No caller outside the package needs it, so exporting it would widen the package API with no reader (2026-10-30, orchestrator under the owner's delegation)

**Reported by:** step S7 (CODE-4: response-id codec, Seq-bounded rehydrate, required record, GET, and the interim ordinal-1 commit in single-shot handleCreate).

**What the proposal says.** Non-spec changes, CODE-4 item 3, states: "Export the first hop as the resolve step".

**What landed instead.** The first hop is the unexported method `continuity.resolve` in `pkg/gateway/environment/translator/continuity.go`, which returns `(row, n, err)`. `handleGet` calls it in this step, and the CODE-5 callers in the same package are to call it later.

**Why.** The step reports that every caller is inside the `translator` package, so exporting the method would only widen the package API. The step states that the method's behavior matches the proposal.

**What a later reader would otherwise get wrong.** A reader who follows CODE-4 item 3 and searches for an exported resolve function in the `translator` package finds none. The resolve step exists as the unexported `continuity.resolve`.

## Proposed: Tier-9 cross-tenant tests do not assert zero binds

**Status:** resolved in step S10: `TestRejectedHeldResponseIDMakesNoBindAndNoForward_spec_15` in `tests/tier9_security/open_responses_held_resolution_test.go` (`3688f2c71`) sends a cross-tenant and an erased-session `previous_response_id`, asserts the byte-identical native 404 an unknown id gets and a 404 on GET, and asserts zero binder binds, zero executor sends, zero `ForwardResponsesTurn` calls, and zero `Forwarded` checks, which is the no-bind and no-forward half of TEST-1 tier 9 (2026-10-30, orchestrator under the owner's delegation)

**Reported by:** step S7 (CODE-4: response-id codec, Seq-bounded rehydrate, required record, GET, and the interim ordinal-1 commit in single-shot handleCreate).

**What the proposal says.** Testing, TEST-1 tier 9, states that a cross-tenant `previous_response_id` or GET returns the same 404 as an unknown id "with no bind and no forward". The same assertion applies when the head session row is erased.

**What landed instead.** The tier-9 tests assert the byte-identical 404 and no dispatch, measured as zero executor Send calls. They do not assert zero binds.

**Why.** In this step `handleCreate` still binds before it resolves. Resolution before the bind is CODE-5 step 1, which lands in S8. The step reports that the no-bind and no-forward assertions belong with that change.

**What a later reader would otherwise get wrong.** A reader of the tier-9 tests at this commit could assume they pin the "no bind" half of TEST-1 tier 9. They pin only the 404 body and the absence of dispatch, and the no-bind assertion is owed to S8.

## Proposed: Tier-4 multi-protocol test rewritten in S7

**Status:** accepted: `openResponsesCreatePrompt` in `tests/tier4_integration/multi_protocol_same_deployment_test.go` (`05b8052d3`) returns the session-id prefix of the `<session>.<n>` id and asserts ordinal 1, as TEST-1 tier 4 states, and the file is registered in `tests/spec-map.json` under 15 and 15.2.1. Landing it with the step that changed the id kept tier 4 green at every commit (2026-10-30, orchestrator under the owner's delegation)

**Reported by:** step S7 (CODE-4: response-id codec, Seq-bounded rehydrate, required record, GET, and the interim ordinal-1 commit in single-shot handleCreate).

**What the proposal says.** Testing, TEST-1 tier 4, lists the `multi_protocol_same_deployment_test.go` rewrite (`openResponsesCreatePrompt` returns the session-id prefix and asserts ordinal 1) under the later cross-tier test work.

**What landed instead.** `tests/tier4_integration/multi_protocol_same_deployment_test.go` was rewritten in S7 and registered in `tests/spec-map.json` under §15.

**Why.** This step changes the response id to `<S>.1`, which breaks the test's `GET /v1/sessions/{id}`. The step reports that deferring the rewrite would have left a failing tier-4 test on this commit.

**What a later reader would otherwise get wrong.** A reader who follows the checklist expects the rewrite in the later cross-tier test step and could redo it there or look for it in that step's diff. The rewrite landed with S7.
