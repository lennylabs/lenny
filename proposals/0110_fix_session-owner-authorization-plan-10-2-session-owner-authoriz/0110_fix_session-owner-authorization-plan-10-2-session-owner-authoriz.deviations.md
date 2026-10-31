# Deviations — 0110_fix_session-owner-authorization-plan-10-2-session-owner-authoriz

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Accepted: send_message sender read moves ahead of the per-sender limit and the holder forward

**Status:** accepted

**Reported by:** step S8 (CODE-3: MCP session-naming tools and SSE attach authorizer call the predicate).

**What the proposal says.** CODE-3 step 2 and its table state that the `send_message` sender row runs `lookupCallerSession` "when present (before the per-sender limit)". The proposal does not say where the sender's existing read and its `SCOPE_DENIED` miss mapping go.

**What landed instead.** In the `send_message` handler in `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, the sender read moved ahead of the per-sender limit, and therefore ahead of the `CH-HOLDERFORWARD` forward. That read is now `lookupCallerSession` with `act=true`. A missing or foreign sender answers the existing `SCOPE_DENIED` "sender session ... not found" through the new `errSenderNotFound` helper, and the later scope check reuses the same row. A missing sender is now refused on the serving replica before the forward. Previously it was refused on the coordinator after the forward.

**Why.** The proposal places the sender check before the per-sender limit, and the limit already runs before the forward. Keeping `SCOPE_DENIED` as the sender miss code makes a foreign sender answer exactly as a missing one does, which is rule 7. The holder-forward test now seeds an owned sender that is out of scope, so it still shows that the forward runs before the scope check.

**What a later reader would otherwise get wrong.** A reader of the proposal alone would expect a missing sender to reach the coordinator through `CH-HOLDERFORWARD` and be refused there. The refusal now happens on the serving replica, before the forward and before the per-sender limit.

## Accepted: lookupCallerSession fails closed on a nil Deps.Store

**Status:** accepted

**Reported by:** step S8 (CODE-3: MCP session-naming tools and SSE attach authorizer call the predicate).

**What the proposal says.** CODE-3 step 2.3 names a nil `SessionService` as the only fail-closed case. The proposal does not address a nil `Deps.Store`.

**What landed instead.** In `lookupCallerSession` in `pkg/gateway/mcpfabric/mcptools/mcptools.go`, a nil `Deps.Store` also returns the bare `sessionstore.ErrNotFound`, so the caller takes the miss path rather than panicking on a nil pointer. Both refusal paths return the bare sentinel, so the miss text is byte-identical. `TestDelegateTaskNilStoreStillRunsCheck_spec_8_3`, which assumed a nil store reached the credential pre-check, was replaced by `TestDelegateTaskNilStoreFailsClosedOnOwnership_spec_10_2`. With a nil store, `delegate_task` is now refused with `RESOURCE_NOT_FOUND` and the checker is not called.

**Why.** The ownership check reads the session row and cannot do so without a store. Failing closed follows the `code-best-practices.md` rule. The old test's premise is unreachable once the ownership check precedes every delegation gate.

**What a later reader would otherwise get wrong.** A reader would expect a nil store to be handled only by downstream gates, and would look for `TestDelegateTaskNilStoreStillRunsCheck_spec_8_3`, which no longer exists. A nil store now refuses every session-naming MCP tool at the ownership check with the miss answer.

## Accepted: shared SessionAccess fake in ownerprincipal

**Status:** accepted

**Reported by:** step S8 (CODE-3: MCP session-naming tools and SSE attach authorizer call the predicate).

**What the proposal says.** TEST-1 item 11 (CODE-3 group) states that fixtures wire "a SessionService fake whose SessionAccessible grants the row's owner". The proposal does not say where that fake lives.

**What landed instead.** `tests/testinfra/ownerprincipal/ownerprincipal.go` gains a shared `SessionAccess` type that grants only the row's owner and refuses `ServiceCall`. Every `mcptools` unit fixture and the tier-3, tier-4, tier-7a, tier-8, and tier-9 fixtures use it rather than a per-package copy.

**Why.** A single fake shared by every package avoids duplicate copies, and step S6 already established `ownerprincipal` as the location for owner fixtures.

**What a later reader would otherwise get wrong.** A reader would look for a per-package `SessionAccessible` fake in each test package. There is one, in `ownerprincipal`, and it also refuses `ServiceCall`, which the proposal's description of the fake does not mention.

## Accepted: Open Responses resolve-step check also gates the cold lineage walk

**Status:** accepted

**Reported by:** step S11 (CODE-4: Open Responses resolve-step check and warm condition 4).

**What the proposal says.** CODE-4 step 2 states that the resolve step, after the tenant-scoped `Get`, returns `errContinuationNotFound` when `SessionAccess` refuses a read. The proposal names `GET /v1/responses/{id}` and `previous_response_id` as the paths that pass through this step.

**What landed instead.** The check is in `continuity.resolve` in `pkg/gateway/environment/translator/continuity.go`, in the `readable` helper and the check after `readPrimary`. Every hop of `rehydrate` also calls `resolve`, so the cold path's lineage walk treats an ancestor session the caller may not read as a gap and stops there, in the same way it treats an erased ancestor.

**Why.** `resolve` is the single resolution step that the GET, the continuation, and the lineage walk share. The §15 text states that the walk stops "at an ancestor session it cannot read". Placing the check in that function applies it to all three paths through one code path. `TestContinuityRehydrateStopsAtUnreadableAncestor_spec_10_2` pins the result.

**What a later reader would otherwise get wrong.** A reader of the proposal alone would expect the check to apply only to the GET and the `previous_response_id` continuation. It also applies to every hop of the cold lineage walk, which stops at the first ancestor the caller may not read.

## Accepted: tier-9 Open Responses isolation case lives in open_responses_warm_user_test.go

**Status:** accepted

**Reported by:** step S11 (CODE-4: Open Responses resolve-step check and warm condition 4).

**What the proposal says.** TEST-2 item 6 places the tier-9 isolation case in `tests/tier9_security/session_owner_isolation_test.go`.

**What landed instead.** The case is `TestOpenResponsesNonOwnerNeitherResolvesNorContinuesWarm_spec_10_2` in `tests/tier9_security/open_responses_warm_user_test.go`. It replaces 0108's condition-4 test in that file and reuses that file's holding fixture, `newHeldORFixture`.

**Why.** The step's task text names `open_responses_warm_user_test.go` as the file to change. The case needs the held-session fixture and the forward counter that already live in that file.

**What a later reader would otherwise get wrong.** A reader would look for the case in `session_owner_isolation_test.go` and not find it, and would expect 0108's condition-4 test to remain in `open_responses_warm_user_test.go`.

## Accepted: CODE-4 fixtures use a minimal sessionserver as SessionAccess for read-bypass cases

**Status:** accepted

**Reported by:** step S11 (CODE-4: Open Responses resolve-step check and warm condition 4).

**What the proposal says.** TEST-2 item 7 (CODE-4 group) states that fixtures set `SessionAccess` to the test's `*sessionserver.Server` where one exists, and otherwise to a fake that grants the row's owner.

**What landed instead.** Fixtures that need a non-owner read bypass build a minimal `sessionserver.New(store, sessionserver.Options{})` as `SessionAccess` even when the test had no server. These are the translator warm-conditions fixture, the tier-7a invariant checker (whose auditor is a tenant-admin), and the tier-9 `orFixture` and held fixture. Owner-only fixtures use the shared `ownerprincipal.SessionAccess` fake. `cmd/lenny-gateway/holder_forward_wiring_test.go` is unchanged because it never resolves a response id.

**Why.** The tenant-admin cases from TEST-2 items 3, 5, and 6, and the invariant checker's cold audit continuation, need the real read-bypass predicate. An owner-only fake answers 404 to those callers.

**What a later reader would otherwise get wrong.** A reader would expect the owner-granting fake in every fixture that lacks a server, and would not expect a `sessionserver` to be constructed in fixtures that previously had none. A reader would also expect `holder_forward_wiring_test.go` to have been rewired.

## Accepted: unstaged defects were already filed, so REC-1 annotated them

**Status:** accepted

**Reported by:** step S14 (REC-1: BUILD-GAPS and remediation-plan records).

**What the proposal says.** REC-1 step 3 (non-spec changes) states that every entry under the summary's "Defects in the shipped tree that this proposal does not stage" is filed as a new BUILD-GAPS finding, with the blob-route entry recorded as the successor of closed F-15.1.32.

**What landed instead.** All six entries had been filed on 2026-10-29, after approval, as F-10.2.31, F-10.2.32, F-11.5.26, F-12.9.23, F-15.1.64, and F-15.2.30. Each of them states that 0110's records step updates the entry instead of filing a duplicate. This step added a "Note (proposal 0110, 2026-10-31)" to each entry, re-confirmed each against 5048ad21c, and left each OPEN. The note on F-15.1.64 records it as the successor of closed F-15.1.32, which already carries a note pointing to F-15.1.64.

**Why.** Filing the entries again would have created duplicate findings. The entries were filed early so that no defect was dropped, and each one names this step as its updater.

**What a later reader would otherwise get wrong.** A reader of the proposal alone would expect REC-1 to have created six new BUILD-GAPS findings on 2026-10-31. The findings date from 2026-10-29, and REC-1 added a dated note to each one and left it OPEN.

## Accepted: plan §10.2 entry was already ticked, so REC-1 recorded the position

**Status:** accepted

**Reported by:** step S14 (REC-1: BUILD-GAPS and remediation-plan records).

**What the proposal says.** REC-1 step 2 states that the plan §10.2 entry "Session-owner authorization proposal" is ticked when part B lands.

**What landed instead.** The entry in `gateway-runtime-comms-remediation.md` was already `[x]`, because it was ticked when the proposal was approved. This step left the tick in place and added a "Position (2026-10-31)" paragraph. The paragraph records that the proposal landed whole with no A/B split, gives the spec commit range 3e2325c01..b35dfc0fc and the code commit range a9434a2c8..5048ad21c, and names the closed findings. The step also changed the header of the unstaged-defects block from "implementation pending" to "implemented 2026-10-31".

**Why.** The box was already ticked, so the remaining work was to record the implementation position.

**What a later reader would otherwise get wrong.** A reader would expect the tick to mark the landing of part B. The tick dates from approval, the proposal did not land as separate parts A and B, and the landing is recorded in the "Position (2026-10-31)" paragraph.

## Accepted: commit b35dfc0fc keeps proposal-internal labels in its trailer

**Status:** accepted

**Reported by:** design-conformance fix pass (commit-message hygiene finding).

**What the proposal says.** The implementation's commit messages cite the specification by heading or describe the behavior, and do not carry the proposal's own deliverable or checklist labels.

**What landed instead.** Commit b35dfc0fc ("spec: have the §15 Open Responses read rule and warm condition 4 apply the §10.2 session ownership rule") carries the trailer `Proposal: proposals/0110_.../ (SPEC-4, checklist S5)`. The commit was not reworded, and no commit-message lint or merge gate was added.

**Why.** Committed history is not rewritten in this pipeline, and a commit-message gate is scope the proposal does not stage. The trailer's path still resolves, and the spec sections the commit edits are §15 (Open Responses conversation sessions) and §10.2 (Session ownership).

**What a later reader would otherwise get wrong.** A reader of `git log` would find `SPEC-4` and `S5` in that trailer and could not resolve them against `spec/`. They name the proposal's deliverable index and implementation checklist entries for the §15 read rule and warm condition 4 edits.
