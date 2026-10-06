# Deviations: Runtime SDKs assume one session per process

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: `session_start.llm.dialect` declared as a free string

**Status:** proposed

**Reported by:** step S10 (SCHEMA-1: frame schemas, fixtures, gates).

**What the proposal says.** SCHEMA-1 item 1 lists `dialect` among the members of `session_start.llm` and gives no type constraint for it. §28.5.3 states its values as `openai` or `anthropic`.

**What landed instead.** `schemas/lenny-adapter-jsonl.schema.json` declares `session_start.llm.dialect` as `{type: string}`, and the member's description names `openai` and `anthropic`. `deliveryMode` is declared as the enum the proposal names.

**Why.** The adapter passes the credential's `proxyDialect` through verbatim (`pkg/adapter/manifest.go`), so an enum could reject a frame the adapter writes. The proposal names no enum for `dialect`.

**What a later reader would otherwise get wrong.** A reader of §28.5.3 would expect the schema to reject a `dialect` value other than `openai` or `anthropic`. The schema accepts any string, and the two values appear only in the description.

## Proposed: `tests/spec-map.json` credits the new tier-3 cases

**Status:** proposed

**Reported by:** step S10 (SCHEMA-1: frame schemas, fixtures, gates).

**What the proposal says.** SCHEMA-1 item 3 names only the list edits in the gates. It does not name `tests/spec-map.json`.

**What landed instead.** `tests/spec-map.json` credits the two new tier-3 cases to §28.5.3.

**Why.** The tier-0 gate `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` fails unless every annotated case in `session_scoped_frame_address_test.go` is credited to the sections it cites.

**What a later reader would otherwise get wrong.** A reader comparing SCHEMA-1 with the diff would find an edit to `tests/spec-map.json` that the proposal does not stage, and could take it for an unrelated change.

## Proposed: `HandleCheckpointRequest` takes a session ID and stamps it on the failure frame

**Status:** proposed

**Reported by:** step S11 (CODE-3: adapter CH-RUNTIMEOPS sessionId, token sink, Terminate deletion).

**What the proposal says.** CODE-3 item 4 says to update `pkg/runtimekit/lifecycle.go` and its tests in the same commit. It does not say what changes there.

**What landed instead.** `LifecycleClient.HandleCheckpointRequest` takes `sessionID` as its first argument after the context. It rejects an empty session ID, and it puts `sessionId` on the `checkpoint_complete{status:failed}` frame it writes when the handler fails (`pkg/runtimekit/lifecycle.go`, `HandleCheckpointRequest`). The `checkpoint_ready` frame still carries no `sessionId`, because it is correlated by `checkpointId`.

**Why.** The runtimekit helper is the only code in that file that writes a session-scoped frame type. SCHEMA-1 requires `sessionId` on `checkpoint_complete`, so the failure frame would fail schema validation without it.

**What a later reader would otherwise get wrong.** A reader of CODE-3 would not expect a signature change to `HandleCheckpointRequest`, and could take the new `sessionID` parameter or the asymmetry between `checkpoint_complete` and `checkpoint_ready` for an unrelated change or an oversight.

## Proposed: `fullSessionID` deleted in favor of `complianceSessionID`

**Status:** proposed

**Reported by:** step S11 (CODE-3: adapter CH-RUNTIMEOPS sessionId, token sink, Terminate deletion).

**What the proposal says.** CODE-3 item 4 says one shared helper prefixes each check's input with `session_start` for `<complianceSessionID>`. It does not mention the Full fake adapter's manifest session, which was the separate constant `fullSessionID`.

**What landed instead.** The constant `fullSessionID` is deleted. The Full fake adapter's manifest `sessionId` and `taskId`, and its per-session credential path, are keyed by `complianceSessionID` (`cmd/lenny-compliance/full.go`, `newFakeAdapter`). `cmd/lenny-compliance/full_test.go` was updated to match.

**Why.** The Full checks send CH-RUNTIMEOPS frames that name `complianceSessionID`, which is the session the shared prefix opens. Keying the manifest and the credential file by a different session would make `credentials_rotated` name one session while pointing at another session's credential file.

**What a later reader would otherwise get wrong.** A reader of CODE-3 would expect `fullSessionID` to still exist and could reintroduce a second session identifier in the Full fake adapter, which would split the manifest session from the session the checks address.

## Proposed: files updated beyond the CODE-3 target list

**Status:** proposed

**Reported by:** step S11 (CODE-3: adapter CH-RUNTIMEOPS sessionId, token sink, Terminate deletion).

**What the proposal says.** The CODE-3 Targets list and the item 4 and item 5 peer lists name specific files.

**What landed instead.** The step also updated the following files, either because the sender and token-sink signatures changed or because they write `llm_request_completed`: `cmd/lenny-gateway/direct_usage_quota_integration_test.go`, `tests/tier4_integration/credential_lifecycle_test.go` (a direct `RotateCredentials` caller), and `tests/tier3_contract/adapter_usage_wired/wired_reportusage_test.go`. It also corrected comments in two tier-7a tests that stated the token fold reads `SoleSessionID`, and it credited the annotated test files in `tests/spec-map.json`, which the tier-0 gate `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` and `lenny-test validate-maps` require.

**Why.** Item 5 requires updating every test that writes an `llm_request_completed` frame, and the build and the tier-0 gates fail without these edits.

**What a later reader would otherwise get wrong.** A reader comparing the CODE-3 target list with the diff would find edits to files the proposal does not name, and could take them for unrelated changes.

## Proposed: delegate examples omit the experiment variant

**Status:** proposed

**Reported by:** step S15 (CODE-5: multi-session Python and TypeScript SDKs).

**What the proposal says.** CODE-5 item 6 says the echo, delegate, and lifecycle examples surface the session's ID and experiment variant for TEST-1's tier-10 cases.

**What landed instead.** The echo and lifecycle examples (`sdks/runtime/python/lenny_runtime/examples/echo.py`, `sdks/runtime/python/lenny_runtime/examples/lifecycle.py`, `sdks/runtime/typescript/examples/echo/main.ts`, and `sdks/runtime/typescript/examples/lifecycle/main.ts`) name both the session ID and the experiment variant. The delegate examples (`examples/delegate.py` and `examples/delegate/main.ts`) name only the session ID and the per-session sequence, and only in their Basic-level echo fallback.

**Why.** The step reports that this mirrors the landed Go SDK, whose delegate example carries no per-session state. At Standard level the delegate example's reply is the child's output, so a variant prefix would appear only on the Basic fallback path. The step describes adding the variant as a small follow-up if TEST-1 needs it.

**What a later reader would otherwise get wrong.** A reader of CODE-5 item 6 would expect the Python and TypeScript delegate examples to report the experiment variant, and a TEST-1 tier-10 case that asserts the variant on the delegate example's output would fail.

## Proposed: files changed beyond the CODE-5 target list

**Status:** proposed

**Reported by:** step S15 (CODE-5: multi-session Python and TypeScript SDKs).

**What the proposal says.** The CODE-5 Targets list names `sdks/runtime/python/lenny_runtime/runtime.py`, `types.py`, `mcp.py`, and `lifecycle.py`, and `sdks/runtime/typescript/src/runtime.ts`, `types.ts`, `mcp.ts`, and `lifecycle.ts`.

**What landed instead.** The step also changed `sdks/runtime/python/lenny_runtime/transport.py` (the `FrameWriter.write_for` and `end_owner` ended mark), `sdks/runtime/python/lenny_runtime/tool.py`, and `sdks/runtime/typescript/src/tool.ts` (the `tool_call` owner, `tool_result` delivery checked against `sessionId`, and cancellation of pending calls on `session_end`). It added the new modules `sdks/runtime/python/lenny_runtime/session.py` and `sdks/runtime/typescript/src/session.ts`, which hold the per-session record and the routing table. `mcp.py` and `mcp.ts` did not change, because they were already process-scoped.

**Why.** CODE-5 item 3 mirrors the CODE-4 item 4 ordering, in which the ended mark is set under the stdout writer's lock, pending `tool_call` waiters are cancelled, and `tool_result` is routed by `sessionId`. That ordering lives in the frame writer and the tool registry. The step placed the session table in its own module to keep `runtime.py` and `runtime.ts` to a single concern, as the Go SDK's `session.go` does.

**What a later reader would otherwise get wrong.** A reader comparing the CODE-5 target list with the diff would find edits to the transport and tool modules and two new session modules that the proposal does not name, and could take them for unrelated changes. The same reader would also expect edits to `mcp.py` and `mcp.ts` that are absent.

## Proposed: manifestInputs.sessionID member deleted

**Status:** proposed

**Reported by:** step S17 (CODE-8: remove per-session manifest fields).

**What the proposal says.** CODE-8 item 1 says to keep the `manifestInputs` members the frame builder reads. That wording could be read as keeping `manifestInputs` unchanged.

**What landed instead.** The step deleted the `manifestInputs.sessionID` member (the `manifestInputs` struct in `pkg/adapter/manifest.go`) and its setters in `session.go`, `resume.go`, and `sdkwarm.go`. The frame builder takes the session identifier as a separate argument, and after the removal nothing reads `in.sessionID`. The members the frame builder reads (`experimentContext` and `tracingContext`) and the members `writeSessionManifest` reads (`agentInterface`, `minPlatformVersion`, and `connectors`) remain.

**Why.** After `writeSessionManifest` stopped deriving `taskId`, `credentialsPath`, and `llm` from the member, no code read it, and the dead-code rule requires deleting a member that a removal leaves unread. The step reports that this matches the proposal's wording, which keeps only the members that are read.

**What a later reader would otherwise get wrong.** A reader of CODE-8 item 1 could expect `manifestInputs` to still carry a `sessionID` member and look for the setters in `session.go`, `resume.go`, and `sdkwarm.go`, which no longer exist.

## Proposed: files changed beyond the CODE-8 target list

**Status:** proposed

**Reported by:** step S17 (CODE-8: remove per-session manifest fields).

**What the proposal says.** The CODE-8 Targets list names `manifest.go`, `manifest_test.go`, `manifest_fields_test.go`, `sessionframes_test.go`, `cmd/lenny-compliance/full_test.go`, the tier-7a race test, and the tier-2, tier-7a, tier-8, and tier-9 tests that read a removed field.

**What landed instead.** The step also edited `pkg/gateway/runtime/adapterclient/client_test.go`, which read `Manifest.SessionID`, `TaskID`, `ExperimentContext`, and `TracingContext` and stopped compiling. It edited `cmd/lenny-compliance/full.go` and `cmd/lenny-compliance/standard.go`, where the harness's fake manifests drop `sessionId`, `taskId`, and `credentialsPath`, and where `full.go` gains `version: 1`. It edited `cmd/runtimes/delegation-echo/main_test.go`, whose fixture drops `sessionId` and `taskId`, and `tests/tier7a_load_local/podmcp_arming_handoff_test.go`, where `gatedRuntime` now records `session_start`.

**Why.** The `adapterclient` test is a tier-1 test that read the removed fields, so the step treated it as covered by the proposal's clause on every test reading a removed field, although it is not in a listed tier. The compliance harness plays the adapter, so its manifests must be pod-scoped like the adapter's. The tier-7a race test needed a recording fake to assert `session_start`.

**What a later reader would otherwise get wrong.** A reader comparing the CODE-8 target list with the diff would find edits to the `adapterclient` test, the compliance harness sources, the `delegation-echo` test, and the tier-7a arming handoff test that the proposal does not name, and could take them for unrelated changes.

## D1 · step S19 · 2026-10-05 · accepted
**Status:** accepted
**Proposal says:** The TEST-1 tier-7a row (`0090_new_write-the-runtime-sdk-proposal-that-gateway-runtime-comms.non-spec-changes.md:292`) requires every iteration of the session frame pairing race to park attempt 1 in `Runtime.Start` and to run the compensating `Shutdown` both with the guard and after a guard acquisition that expired. In every iteration, attempt 2 then binds and starts with an Attach stream bound to the session, a `SignalDeadline` is issued after attempt 2's bind, and the test asserts that no `deadline_approaching` reaches the peer before the `session_started` that answers attempt 2's `session_start`.
**Implemented instead:** In the expired-guard iterations (the odd iterations), `tests/tier7a_load_local/session_frame_pairing_race_test.go:241-252` (`runPairingIteration`) withholds attempt 1's `session_started` so the open sequence keeps the slot serialization, because the guard is not held during `Runtime.Start` and so cannot expire there. The helper `retryAgainstHeldIdentifier` (around lines 360-400) then requires every bind, `StartSession`, `SignalDeadline`, and Attach of attempt 2 against the held identifier to be refused with `slot_reclaim_in_progress`, including binds repeated across the stale open sequence's `session_end`. The helpers `pollHeldBinds`, `attachRefusal`, and `isReclaimHoldRefusal` carry those assertions.
**Why:** §5.2 forbids the interleaving the row describes for the expired-guard arm. Under the "Slot-identifier reclaim hold" paragraph (`spec/05_runtime-registry-and-pool-model.md:478`), an act performed without the slot serialization counts as an act that did not return without error, and a hold opened while a start's open sequence holds the serialization lasts at least until that sequence ends. The reclaim hold table (`spec/05_runtime-registry-and-pool-model.md:467-472`) holds the identifier for the life of the pod for every failed-cleanup row reclaimed by a `Shutdown`, including the pre-`running` slot at line 470. After an expired-guard compensating `Shutdown`, attempt 2 is never bound, its `session_start` is never written, and no Attach stream can be bound to it. Binding attempt 2 in that arm would break the code's conformance to §5.2. The fixer cannot edit the proposal, so no legal code change closes the finding, and the reviewer recorded the fix as "No code change".
**Consequence if the proposal is not corrected:** A later reader or implementor of TEST-1 would expect the expired-guard iterations to bind and start attempt 2 and to assert `deadline_approaching` ordering against attempt 2's `session_started`. That reader could rewrite the test to admit attempt 2, which would require the adapter to violate the §5.2 reclaim hold, or could treat the refusal assertions in `retryAgainstHeldIdentifier` as a defect.
**Suggested next step:** correct the proposal
**Evidence:** The pairing race test was created in 3e563c847 (round 1), moved to an Attach stream in e0f0d9e32 (round 2), given the bind-outcome assertion `requireReclaimHoldRefusal` in 9b041d819 (round 3), and given the expired-guard refusal arm in 5c0f313a0 (round 5). Round 4 (47353c49b) addressed a different finding. The finding recurred in rounds 3, 4, and 5. After round 5 the reviewer recorded the code as correct against §5.2 and §28.5.3. Three judges ruled the finding unresolvable with high confidence: each found that §5.2 rules out binding attempt 2 in the expired-guard arm, that the landed test follows §5.2, and that only a proposal correction decided by a human resolves the mismatch. The judges separately ruled the outstanding Pod exit matrix finding (`pkg/adapter/sessionframes_test.go`) resolvable and outside this deviation.

## Proposed: CODE-7 client inventory omits sites added by earlier steps

**Status:** proposed

**Reported by:** step S22 (record the CODE-7 client-inventory deviation), for the sites step S20 (CODE-7: Part B connection handshake) covered.

**What the proposal says.** The CODE-7 **Client inventory.** table lists one row for each in-tree program, function, or test helper that dials or accepts `CH-MSGSOCK` or `CH-RUNTIMEOPS`, and the paragraph after it states "No other in-tree code dials or accepts either socket." The last row places the `CH-RUNTIMEOPS` listener of TEST-1's per-SDK `session_started` cases in `tests/tier3_contract/sdks/runtime_sdk_test.go`.

**What landed instead.** Steps S11 (CODE-3), S16 (CODE-6), and S19 (TEST-1) added or extended tests that dial or accept one of the sockets, so the inventory and its closing statement were incomplete by the time S20 landed. S20 updated the following sites, which the inventory does not name, to perform the runtime connection handshake in commit a79d3bfc3:

- `tests/tier7a_load_local/direct_usage_per_session_fold_test.go`
- `tests/tier3_contract/runtimeops_emitted/emitted_frames_test.go`
- `tests/tier3_contract/adapter_jsonl/session_frame_wire_order_test.go`
- `cmd/runtimes/streaming-echo/sessionframes_test.go`
- `cmd/lenny-compliance/stub_test.go`

The per-SDK `session_started` `CH-RUNTIMEOPS` fake listener that the last inventory row names is `opsListener` (`startOpsListener`), in `tests/tier3_contract/sdks/runtime_sdk_sessions_test.go` rather than in `tests/tier3_contract/sdks/runtime_sdk_test.go`.

**Why.** The inventory was written against the tree before the earlier steps of this proposal landed. Every site that dials or accepts either socket must complete the §4.7.11 runtime connection handshake, or its connection is refused, so S20 covered each added site in the same commit as the inventoried rows. TEST-1 placed its per-SDK session cases in a separate file from the existing SDK contract test.

**What a later reader would otherwise get wrong.** A reader relying on the inventory to find every client or listener of `CH-MSGSOCK` and `CH-RUNTIMEOPS` would miss the five tests above and would look for the per-SDK `session_started` listener in the wrong file. A later change to the handshake that updates only the inventoried rows would leave those tests failing.

## Proposed: record-time start confirmation still compares the bind token

**Status:** resolved

**Resolved by:** commit fb61cf13e. `noteRuntimeStarted` takes the entry the claim was admitted against and compares entry identity, as `registryHoldsClaimEntry` does before the `session_start` write, and `slotClaim.attempt` is removed. `TestTheRecordTimeConfirmationComparesEntryIdentity_spec_4_7_1` in `pkg/adapter/slotsession_test.go` covers a successor entry that reuses the bind token and an untokened successor, and fails against the token comparison.

**Reported by:** the stale-code sweep step (code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/adapter/runtimegeneration.go`.

**What landed instead.** `pkg/adapter/runtimegeneration.go` is unchanged. `noteRuntimeStarted` still confirms the start by comparing the session ID and the bind attempt token:

```go
if !ok || st.sessionID != sessionID || st.bindAttempt != attempt {
	return false
}
```

**Why.** The landed §4.7.1 (Role and Gateway RPC Contract), rule 8, states that the adapter "resolves the registry entry for that slot identifier again and confirms that it still holds the entry the request that starts the session was admitted against. The comparison is of entry identity ... A comparison of tokens alone does not conform." The record-time confirmation in `noteRuntimeStarted` compares the token, while `registryHoldsClaimEntry` compares entry identity through `claim.entry`. The sweep left the code unchanged because the line is executable code, and changing it changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/adapter/runtimegeneration.go` would take the token comparison in `noteRuntimeStarted` as the current rule-8 contract, although the landed spec states that a comparison of tokens alone does not conform.

## Proposed: co-tenant teardown test asserts that a session teardown sends the runtime no frame

**Status:** resolved

**Resolved by:** commit fb61cf13e. The failure message now states that a session teardown sends no `CH-RUNTIMEOPS` frame and that its `session_end` goes on `CH-MSGSOCK`. The assertion is unchanged.

**Reported by:** the stale-code sweep step (code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/adapter/slotsession_test.go`.

**What landed instead.** `pkg/adapter/slotsession_test.go` is unchanged. After a `Shutdown` of one session while a co-tenant is still bound, the test still fails with this message when `CH-RUNTIMEOPS` carries a frame:

```go
t.Errorf("CH-RUNTIMEOPS carried a %q frame while a co-tenant was still bound; a session "+
	"teardown sends the runtime no frame", frame.Type)
```

**Why.** The landed §28.5.3 (Intra-pod), `CH-MSGSOCK` **Session frame writes.** table maps "`Shutdown` that removes an entry whose session reached `running`" to `session_end`, and the `Shutdown` row of the §4.7 RPC table states that the adapter "writes the session's `session_end` on `CH-MSGSOCK` when the session reached `running`". A session teardown therefore sends the runtime a frame on `CH-MSGSOCK`, and only `CH-RUNTIMEOPS` stays silent. The assertion checks only `CH-RUNTIMEOPS`, but its failure message states the retired contract. The sweep left the test unchanged because the line is executable code, and changing it changes behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/adapter/slotsession_test.go` would take the failure message as the current contract and conclude that a session teardown sends the runtime no frame on any channel, although the landed spec requires `session_end` on `CH-MSGSOCK`.

## Proposed: earlier commit messages on the branch carry proposal-internal labels

**Status:** proposed

**Reported by:** the design-conformance review of the landed implementation.

**What the proposal says.** The proposal's implementation checklist and non-spec changes name their steps and deliverables with ids that resolve only inside the proposal directory. Those ids are scaffolding for the proposal document and are not meant to appear in the shipped history.

**What landed instead.** Several commits already on the branch name those ids in their subject or body, together with a review round number. Examples are `d0fc3c15d` (a build step id, a test deliverable id, and a round number), `67968d806`, `31d253eaf`, and `38948cd30` (spec deliverable ids in the subject), and the spec commit whose body names a spec deliverable id after the proposal path. The history is not rewritten, and no commit-message lint or tier-0 gate was added. The conformance fix for the session_start ordering rule is committed with a message that names the spec section and the behavior instead.

**Why.** Committed history on a shared branch is not rewritten by a build step. The review suggested a commit-message lint, but a new gate over commit messages is scope the proposal does not stage, and earlier runs had such a gate reverted. If the branch is rebased before it merges, the subjects of the commits above can be reworded at that time, and the merge commit message names the proposal and the behavior without the internal ids.

**What a later reader would otherwise get wrong.** A reader of the first-parent history would find ids such as a step number or a deliverable number that resolve to nothing outside the proposal directory, and could take them for durable identifiers.
