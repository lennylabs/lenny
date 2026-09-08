# Review log: Derive message scope from the address type

## Standing context

**Changelog (compaction pass 3, 2026-09-08).** Read the whole ledger (about 70 entries across the
`non-spec.1-2`, `non-spec-recheck.1-2`, `spec.1-2`, `reconcile.4`, and the `f1` and `f4` firings). Closed
four `Open` items the window settled: `message SessionId` opens at `:596`, `spec/` does name a test tier in
the capitalised `Tier 0 (Static)` spelling, the replacement gate's case names need not differ from the
retiring ones, and S3's tier list omitting tier 0 is repo convention. Retired the two `Deferred` entries
that were applied, the checklist's TEST-1 step and the summary's `docs/api/internal.md` inventory.
Corrected the `040323634` bullet, which said the commit names `CoordinatorFence` nowhere; it names it 44
times outside the proto and only its `schemas/lenny-adapter.proto` diff is empty of it. Named, in the
`slotAddressCaseFiles` bullet, the case that actually holds the gate file's path. Lifted every new `FACT`,
`DECISION`, `MISTAKE`, and unclosed `DEFERRED`, including the decisions the `f1`, `f4`, fix and reconcile
firings recorded. Left `## Ledger` whole for the round boundary to archive. The section runs past the
200-line target and nothing was dropped to reach it: this window derived more traps than it closed `Open`
items, and every trap below is one a lens recorded as having saved it a round.

### Settled

- **Proto census.** 31 RPC request types across the two service blocks; 25 declare a top-level `SessionId session_id`; the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`.
- **Addressing convention.** No field named `session_id` carries another type and no field of type `SessionId` carries another name, anywhere in `schemas/lenny-adapter.proto` including `oneof` arms. 26 such declarations (the 25 request types plus `CheckpointStart`). D2's replacement gate is green on the shipped proto on day one.
- **The only two `oneof` blocks** are `CheckpointRequest` (`schemas/lenny-adapter.proto:1174`) and `CheckpointResponse` (`message` line `:1249`, `oneof msg` at `:1250`), and `CheckpointResponse` is the request type of no RPC (`rpc Checkpoint` at `:133`), so the envelope predicate selects `CheckpointRequest` alone. The third `oneof` hit at `:1183` is inside a comment.
- **Envelope frames.** `CheckpointStart` (`:1193`) declares `SessionId session_id = 7` (`:1217`); `CheckpointGrant` and `CheckpointAbort` declare no address; `CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4` (`:1186`).
- **Proto anchors, current and verified.** `message SessionId` `:596`, `CheckpointRequest` `:1173`, `CheckpointStart` `:1193`, `CoordinatorFenceRequest` `:1455` with `SessionId session_id = 1` at `:1456` and no `reserved`, `ReportSessionScrubRequest` `:456` with `string pod_id = 1` at `:457` and `SessionId session_id = 2` at `:458`, `ReportPodScrubRequest` `:499-503`. **SPEC-1's `:458` is exact.** Twenty-five entries confirmed this against a false "one line off" note that stood here for two windows; do not re-derive it and do not "fix" SPEC-1 toward `:457`.
- **spec/04 §4.1 anchors.** Heading `:149`, introducing paragraph `:151`, table `:153-186` (32 rows), fence row `:175`, grounding paragraph `:188`, `ShutdownRequest` paragraph `:190`, `### 4.2` at `:192`.
- **Table arithmetic.** The 32 rows are the 31 request types plus `CheckpointStart`; 26 session and 6 pod, becoming 27 and 5 once the fence moves, which is exactly what the derivation computes. No row is lost.
- **The derived session class is a strict superset** of the retired table's on the current proto. Only `CoordinatorFenceRequest` moves, pod to session; nothing moves session to pod, so the `spec/05:515` refusal can only be added, never removed.
- **The one behavioral obligation is met and pinned, on both legs.** `spec/05_runtime-registry-and-pool-model.md:515` refuses a session-scoped request with an empty identifier; `pkg/adapter/coordination.go:109-111` does it before `boundSlotState` at `:116` and before the generation checks at `:121`; `TestCoordinatorFenceRejectsMissingSessionID` at `pkg/adapter/coordination_test.go:35` asserts it. The caller half also holds: `pkg/gateway/runtime/adapterclient/coordinatorfence.go:51-54` builds the request from a `sessionID string` parameter and the coordfence package never constructs an empty one (`coordfence.go:62-63`), so the refusal has no reachable trigger on the handoff path.
- **`spec/05:515` imposes the empty-identifier refusal alone.** It says nothing about a malformed identifier, so the reclassification imports no second arm, `addressRuleCases` owes the fence no entry, and the handler does no format validation either.
- **Post-0076 code anchors.** `pkg/adapter/server.go:302` is `hold holdState` and `Server` carries no `coord` field; `pkg/adapter/slot.go:59` is `coord coordinationState`; `pkg/adapter/coordination.go:17-38` is the per-session struct doc, `:107` the handler, `:116` the `boundSlotState` resolve, `:127-134` the `coordinator_handoff_stale` refusal; `pkg/adapter/checkpoint.go:74-84`.
- **coordfence driver.** `DefaultMaxAttempts = 3` at `pkg/gateway/coordination/coordfence/coordfence.go:52`, transient arm retries at the same generation `:180-183`, stale arm re-reads and relinquishes `:171-179`. A fence that lands but whose ack is lost gives up on the second of three attempts. The 5-second deadline is one layer down, `CoordinatorFenceTimeout` at `pkg/gateway/runtime/adapterclient/coordinatorfence.go:20` applied at `:49`; grepping only `coordfence.go` for a deadline produces a false "unbounded RPC" finding.
- **The driver applies no backoff at all.** `coordfence.go` is the package's only non-test file and `grep -n 'time\.'` over it returns nothing, so the three attempts are issued back to back against `spec/10:39`'s "1-second backoff". A sweep of `proposals/`, `BUILD-GAPS.md`, and `TEST-GAPS.md` for that phrase returns this proposal's own files alone: the backoff half is uninventoried and unowned.
- **§4.7.1 survivors.** `spec/04_system-components.md:725` (`ReportSessionScrub` session-scoped) and `:726` (`ReportPodScrub` pod-scoped) are the only per-message scope sentences outside §4.1; both agree with the derivation and both stand unedited.
- **`:726` is the last pod-scope classification.** After SPEC-1, the other `pod-scoped` sites in `spec/` are `spec/10:60` (the hold and its gauge), `spec/04:872` (the rotation in-flight ceiling), and `spec/12:202` (Redis key prefixes), none of which classifies a request message.
- **A tier-11 gate pins `:725`.** `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` (`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`) holds the identical sentence on `spec/04:725` and `docs/reference/adapter-contract.md:81`. Editing `:725` turns tier 11 red.
- **No docs mirror.** `docs/` names RPCs and never request message types. `CoordinatorFence` appears once, at `docs/reference/adapter-contract.md:69`, with no scope word; `grep -rn CoordinatorFence sdks/ charts/` returns nothing.
- **No inbound reference to the table.** Nothing in the tree links `#request-message-scope` or the heading text, and SPEC-1 keeps the heading, so no anchor redirect is owed.
- **spec/28, spec/29 and spec/10 already read per-session.** `spec/28:314-317` (CH-FENCE), `spec/28:1672-1673` (the unit is the session), `spec/29:1527-1529` ("A successful fence for any one of those sessions exits the hold for the pod, and the generation that fence records is the fenced session's alone"), `spec/10:38` and `:40`. The §28.3 register row at `spec/28:120` has no scope column. So `spec/04:175` and `:188` contradict three other spec sections as well as the code.
- **spec/10 hold anchors.** `:53` opens §10.1.4, `:57` states the fence is the only way out of hold state (predates 0076, `db8cdc224`), `:60` states the hold and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped (landed by 0076's SPEC-1 under D5, `cf20a0646`). The attribution clause attaches to `:60` alone.
- **Blast radius of the shared parse.** `protoServiceRequests` has one caller (`tests/tier0_static/adapter_proto_message_scope_test.go:87`); `protoFields` has one other (`tests/tier0_static/claim_register_proto_agreement_test.go:64`, with lookups at `:72` and `:82`). The gate's private helpers have no reader outside their file. The two tier-0 files 0076 landed read doc comments and call neither.
- **spec-map registrations.** The retiring gate's two case names appear only at `tests/spec-map.json:156` and `:169` (section 4.1, block opens `:150`) and `:5670` (section 28.5.3, block opens `:5643`). The tier-3 file is credited whole-file at `:172`, `:600`, `:1110`, `:1391`, `:3678`, so TEST-2 owes no spec-map edit.
- **Deleting `:5670` empties no section.** Section 28.5.3 keeps dozens of other entries, including the tier-3 frame-resolution cases at `:5673-5678` and the compliance session-echo cases, and section 4.1 holds 19 entries of which only `:156` and `:169` are the retiring gate's. Neither section's `notes` string mentions the gate or the table, so the edit is entries-only.
- **The retiring gate's two cases annotate different sections.** `tests/tier0_static/adapter_proto_message_scope_test.go:129` reads `// spec: 4.1 ..., 28.5.3 (addressing)`; `:152` reads `// spec: 4.1 ...` alone. That single annotation is why `:5670` exists, which is what makes "drop the credit" the mechanical consequence of the replacement gate annotating 4.1 alone.
- **`validate-maps` is a tier-0 check.** `validateSpecMapTestFuncs` (`cmd/lenny-test/cmd_validate.go:941-998`) fails on a `path::Fn` entry naming no function, and `validate-maps` runs from `cmd/lenny-test/cmd_run.go:761`. That is why TEST-1's re-registration lands inside S2 rather than in its own step.
- **`slotAddressCaseFiles` names files by path**, the gate at `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, the shared parse at `:337`, and the tier-3 suite at `:408-409` (TEST-1 edits the first two and TEST-2 the third, and none needs an inventory edit while all three keep their paths). `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970-989`) is one-directional, refusing missing credits and never surplus ones. The case that would catch a renamed gate file is `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1108-1131`), which derives the inventory from the tree and reports omissions, so "keeping the path costs no edit" rests on that case rather than on the credit gate.
- **Four of the five loops over `slotAddressCaseFiles` force the kept path, not five.** `:973`, `:1028`, `:1054`, and `:1096` resolve each entry through `repoFileLines` (`:699-706`), directly or through `citedSectionsInFile` (`:797-805`) and `citedSectionsPerCase` (`:776-779`), and `t.Fatalf` on a file they cannot read. The fifth, `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1108-1131`), builds a set from the inventory and compares it against `addressRuleGateFile`, `addressRuleCases`, and `derivedInventoryCaseFiles` (`:1212-1231`) without opening any listed path. The correction is applied in TEST-1's paragraph; the conclusion stands on the four.
- **No surplus-credit gate covers the gate file.** `TestDocumentConsistencyGatesCarryNoSpecSectionCredit` (`:136`) and `TestConsistencyGatesAreMappedOnlyToSectionsTheyAnnotate` (`:183`) range over `documentConsistencyGates` and `consistencyGateFiles`, neither of which names it. So the `// spec:` annotation and the spec-map entry have to move together by hand and nothing in tier 0 catches it if they do not. That is the live risk in TEST-1's single-step re-registration.
- **Registers and meta-gates are untouched.** `identifier-senses.yaml`'s eight `spec/04` rows key on retired channel spellings, none of which occurs in that file; `line-citations.yaml` and `line-citation-resolution.yaml` are `files: []`; `pinned-spec-literals.yaml` and `anchor-senses.yaml` name no `spec/04` site; `tests/claim-map.json` carries no scope row; `gate_integrity_test.go`'s `tierZeroGates` omits the gate; `successor_pointer_test.go`'s `reducedSections` is §4.7, §15.4, §29.10.
- **The line-citation ratchet cannot fire.** It matches the retired `§X line L` form only, `proposals/` is outside its read domain, and the `04_system-components.md:NNN` citations that shift when the block shrinks live in `BUILD-GAPS.md` and `TEST-GAPS.md`, both in `readExcludedFiles`.
- **S1's red window is real and bounded.** Deleting `:153-186` leaves `parseMessageScopeTable` with zero rows, because no other line in `spec/04` matches `messageScopeRow`, so the retiring gate reports a missing row for every in-scope message until S2 lands. S1's own line records the disposition.
- **Tier 0 compiles the tier-3 contract package.** `runStaticTier` runs `go vet -tags=contract ./tests/tier3_contract/...` (`cmd/lenny-test/cmd_run.go:503-509`). S3 listing tier 3 alone follows the repo's convention (0076's S8 does the same).
- **TEST-2 split arithmetic.** `sessionScopedMessages`'s 18 current keys are exactly the 18 messages declaring `reserved "slot_id"`; 17 of them are request types and one is `CheckpointStart`; 17 plus the 8 additions is the 25 addressed request types, giving a widened set of 26. `retiredDuplicateNumbers` keeps the 18 verbatim.
- **The 8 additions** are `CoordinatorFenceRequest`, `ExportPathsRequest`, `ConfigureWorkspaceRequest`, `CallConnectorToolRequest`, `CallPlatformToolRequest`, `ListConnectorToolsRequest`, `ListPlatformToolsRequest`, `ListSessionConnectorsRequest`. All declare the address, none declares `slot_id`, so both widened arms pass on the tree as it stands.
- **Commit `040323634`** ("Address a session on the gRPC leg by its session identifier alone", 2026-08-20) is 0073's own implementation commit. It added all 18 `reserved "slot_id"` pairs, and its `schemas/lenny-adapter.proto` diff carries no `CoordinatorFence` line at all, which is the correct ground for the fence's empty retired-field column. The commit does name `CoordinatorFence` 44 times outside the proto (`pkg/adapter/coordination.go`, `holdstate.go`, `docs/reference/adapter-contract.md`), so a grep over the whole commit refutes the wider phrasing an earlier entry used. The claim is about the proto diff alone, and `non-spec-changes.md` §4 states it that way.
- **`SlotId slot_id` history.** The field entered the proto across eight commits (`4f6e49dea`, `3128fa712`, `72880f767`, `c47b65522`, `4003ee848`, `3c69e3f35`, `01d19af01`, `040323634`). The fence was introduced by `d353a8ef3`, which added none of them.
- **0073's recorded limit lives only in 0073**, at `proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6686-6689` and `:6713`. Nothing in `spec/`, `docs/`, or `BUILD-GAPS.md` restates it, and §4.1 states no gate and no gate limit. 0073 also records a second limit at `:912-913` that applies only under its rejected choice (b).
- **"0073's §4.2 value rule" is 0073's own §4.2**, the heading "### 4.2 The rule" at `proposals/0073_...md:964`, not `spec/04` §4.2 (Session Manager). The rule landed at `spec/05_runtime-registry-and-pool-model.md:515`.
- **`spec-changes.md` has no §7 at all.** The whole `## 7. Open decisions for review` block was deleted; the file's live section set is §2, §3, §4, §5, §6, §9, §10. `§1.2` resolves into `problem-statement.md:46` and `§8 Testing` lives in `non-spec-changes.md:99`. No landing file outside this log cites a §7, so the deletion stranded nothing. Anything citing "§7 question 1" is stale.
- **The summary's live open decisions are OD1 and OD5.** OD1 is retire-or-withdraw, with a recommendation and moderate confidence. OD5 is the unheld widened tier-3 session set. OD2, OD3, OD4, and OD6 were answered and withdrawn by the open-decisions firings, and the numbering gap is preserved deliberately so successive firings joining on the identifier string do not treat a survivor as fresh.
- **The `IMPLEMENTOR TO FILL THE BLANKS` banner is gone from the spec side.** Only `non-spec-changes.md` §5 carries one. SPEC-1 is final text with quoted replacement paragraphs, so the "it is only indicative" defence no longer applies to a spec-side finding.
- **Neighbouring proposal status.** 0073 Implemented 2026-08-31. 0076 Implemented, approved 2026-09-06, implemented 2026-09-07; its OD3 answers are "Yes, `CoordinatorFenceRequest` is session-scoped after CODE-1" and "Proposal 0075 is that successor". 0080 is a Draft and stages nothing.
- **`address_rule_citation_test.go`** derives from `spec/` the single section carrying "rejected at the adapter boundary with `InvalidArgument`" and fails when two carry it, and separately holds a one-directional hand inventory (`addressRuleCases`, `:29-48`) naming `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` by function name.
- **DECISION: the gate's subject moved** from what the rule states to what the rule rests on, namely the name-and-type biconditional and the envelope's structure, because the rule's output needs no reconciliation once it is computed from the proto.
- **DECISION: the envelope clause produces the classification** rather than presupposing one, since `CheckpointStart` is not a member of the RPC request types clause 1 ranges over.
- **DECISION: D2's "no pod-scoped message declares a field of the address type" clause is dropped**, because pod scope is derived from the field's absence. The retained type-without-name clause refuses the case the dropped clause was meant to catch.
- **DECISION: TEST-1's target set** is the gate file, the shared proto parse, the parse's other caller, and `tests/spec-map.json`, with the spec-map re-registration landing inside S2.
- **DECISION: TEST-2 is a split**, not a map addition. `sessionScopedMessages` becomes a `[]string` populated by the derivation rule, and a new `retiredDuplicateNumbers map[string]protoreflect.FieldNumber` keeps the reservation case's closed historical population.
- **DECISION: OD3 is answered no.** The rewritten §4.1 restates nothing about the coordinator hold; §10.1.4 owns both halves of that fact.
- **DECISION: OD4 is answered "they stand".** §4.7.1's two per-message clauses are kept and the answer is staged as a SPEC-1 paragraph rather than left as the absence of an edit.
- **DECISION: OD1's recommendation** is to accept the residuals and keep the retirement, at moderate confidence, with the withdrawal branch priced as the "no".
- **DECISION: the summary's section list is closed** at eight headings with no lead paragraph above `## Summary`; a banner about review state belongs in the status file and a dependency belongs in the impacts table.
- **DECISION: the equal-generation re-fence stays unstaged**, recorded as the one entry under `## Defects in the shipped tree that this proposal does not stage`, with 0080 §1.16 named as owner.
- **DECISION: the unsatisfiable number column is grounded on the fence's field history**, that reserving a number and the name `slot_id` would record a removal that never happened, rather than on any scope non-goal.
- **0073 attributions all verify.** "0073's §8" is its Testing section at `proposals/0073_...md:4678-4696`, which introduces the tier-0 gate without naming the file (so a grep by filename refutes nothing; grep the §8 prose). "0073's SPEC-7" is "Declare message scope and state the addressing rule" at `:2823`. The whole `spec/04` §4.1 block `:149-190` came in as one commit, `f37e867b8`, 0073's spec application, and no later commit touched it.
- **0073 weighed this trade the other way**, at `proposals/0073_...md:6653-6655`: a machine-derivable rule "would then rest on every future message author choosing the name correctly, which is the same hand-maintained agreement moved into the field name". That is OD1's rejected-alternative half and it is now in the entry.
- **The §4.1 heading is an unnumbered `####` inside `### 4.1 Edge Gateway Replicas`** (`:40`), so a gate resolving sections by number attributes the block to 4.1. `spec/README.md` carries no entry for a bare `####` heading and no `#request-message-scope` fragment exists, so heading-walker and fragment-link gates are unaffected either way.
- **`SessionId` as a bare proto type name occurs nowhere in `spec/` or `docs/` today.** SPEC-1's staged paragraph is the first spec sentence to name that wire type, and no register, gate, or glossary requires one to be declared anywhere.
- **The proto's own doc comments already read per-session.** `CoordinatorFenceRequest`'s comment (`schemas/lenny-adapter.proto:1449-1454`) and `CoordinatorFence`'s (`:152-166`) were rewritten by 0076, and the file carries no `pod-scoped`/`session-scoped` string anywhere, so `schemas/` owes no comment edit.
- **`tests/tier0_static/adapter_proto_parse_test.go` declares no test function.** It is a helper file with a `_test.go` suffix, so there was never a self-test for TEST-1's extension to lose; its whole coverage is what its two callers assert. It appears in no register.
- **The retiring gate's own file already has the shape §8 asks the replacement to have**: one green-baseline assertion at `:174-176` and a subtest per refusal at `:182-201`.
- **The claim register cannot fire on the staged sentence.** `spec/28:161-165` scopes §28.4 to "Every normative statement **this section** makes about a mechanism", and `tests/tier0_static/claim_register_test.go:23-26` implements that, so a §4.1 sentence naming a tier-0 gate owes no `tests/claim-map.json` row. Four rounds re-derived this; stop.
- **No successor or relocated-material pointer is owed.** `successor_pointer_test.go:52-56`'s `reducedSections` is a hand list of §4.7, §15.4, §29.10, and `relocated_material_pointer_test.go:50-60`'s `relocatedStatements` anchors on a CH-RUNTIMEOPS handshake sentence. SPEC-1 moves nothing into §28.
- **DECISION: OD2 (the tier-3 population) is answered "accept the widening"**, stated as a requirement in `non-spec-changes.md` §5 TEST-2 with its eight named additions, and `spec-changes.md` §7 was deleted because that was its last item.
- **DECISION: OD3 is answered "keep the path".** The replacement gate keeps `tests/tier0_static/adapter_proto_message_scope_test.go` and TEST-1 rewrites it in place; the requirement is stated at `non-spec-changes.md:43`, not in D2, because the path is a property of the test staging.
- **DECISION: OD4 is answered "drop the credit".** TEST-1 now requires the replacement gate's cases to carry `// spec: 4.1` alone, to be registered under section 4.1 alone, and `tests/spec-map.json:5670` to be deleted rather than re-pointed. §9's bullet and the 0073 impacts row were reconciled to match.
- **DECISION: OD6 is answered "yes, `spec/` may name a test tier".** No staged text changed; the staged constraint sentence is itself the answer.
- **DECISION: the equal-generation re-fence entry is narrowed.** 0080 §1.16 owns the remedy for the refusal alone; the missing one-second backoff is recorded as owned by nobody.
- **DECISION: the summary carries three unstaged-defect entries**, the equal-generation re-fence (`spec/10:39`), the stale protobuf excerpts (`docs/api/internal.md:209-215`), and the §4.7 `CoordinatorFence` announcement row (`spec/04:712`), each with its ground and each stating why no repair is staged.
- **DECISION: the 0080 impacts row gained two inventory candidates**, the §4.7 fence row with its `docs/reference/adapter-contract.md:69` mirror, and the stale `docs/api/internal.md` excerpts, both recorded as absent from 0080's §1.1-§1.21 and not excluded by its §3.
- **`message SessionId` opens at `schemas/lenny-adapter.proto:596`**, with the doc comment at `:595` and `string value = 1` at `:597`. Settled by `grep -n '^message SessionId'` in three independent entries; the `:595` reading is wrong and the UNVERIFIED it carried is closed.
- **`spec/` names test tiers today**, in the capitalised form: `spec/18_build-sequence.md:85` ("Tier 0 (Static) and Tier 11 (Documentation) pass on the empty repository"), `:98`, `:244`, `:281`, `:349`. The rounds that grepped and reported nothing were searching the lowercase hyphenated `tier-0`, which `spec/` does not use. OD6's "yes" stands on the capitalised form and the precedent claim is settled.
- **The replacement gate's case names need not differ from the retiring ones.** `validateSpecMapTestFuncs` reports only a `path::Fn` entry naming an absent function, and S2 rewrites the gate file and `tests/spec-map.json` in one commit, so `validate-maps` never sees a dangling entry either way. Reusing a name would be misleading rather than red. Four entries reached this; the standing `Open` item is closed.
- **S3's tier list omitting tier 0 is repo convention, not a finding.** A step lists the tiers its own deliverable's cases belong to rather than every tier that compiles the file, and 0076's S8 does the same with "Tiers 1, 4, 7a". Filed once, declined by four later passes; the standing UNVERIFIED is closed.
- **The proto declares no nested messages.** `grep -c "^message \w\+ {"` and `grep -c "^\s*message \w\+ {"` both return 88 and no indented `message` line exists, so the shared parse's column-anchored `protoMessageOpen` (`tests/tier0_static/adapter_proto_parse_test.go:22`) reaches every message and D2's "every message the protocol declares" quantifier is satisfiable by the parse the proposal requires.
- **Two `awk` one-liners are the cheapest correct census**, and they avoid the naive-parser trap because they key on `^message ` alone and never bound a body: `awk '/^message /{m=$2} /SessionId session_id/{print m}'` returns the 26 addressed messages, and the same form over `reserved "slot_id"` returns the 18. Safe only because every message is top-level.
- **`validateSpecMapCoverage` (`cmd/lenny-test/cmd_validate.go:801-834`) fails a section only when its `tests` array is empty**, and honours `tests/spec-map-exceptions.yaml`. There is no per-tier floor anywhere in the validator set, which matters because `tests/spec-map.json:5670` is section 28.5.3's only tier-0 entry; after the deletion that section carries zero tier-0 credits and nothing objects.
- **Four RPCs are client-streaming**, `PrepareWorkspace` (`schemas/lenny-adapter.proto:41`), `Attach` (`:84`), `Checkpoint` (`:133`), and `AdapterEvents` (`:241`), and only `CheckpointRequest` carries a `oneof`. The other three land where the retired table put them (`PrepareWorkspaceRequest` and `AttachRequest` session, `AdapterEventsRequest` pod).
- **A tier-3 case already asserts the envelope clause on the wire.** `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:881-887` holds that the opening frame's `session_id` is the whole stream's address and that a stream opening without a usable one is refused with `InvalidArgument`, and it is credited to section 4.1. The clause states shipped behavior.
- **`tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` excludes the fence on fence-semantics grounds**, not on scope: its package doc (`:20-23`) lists three separate exclusions and `fencedMessages`' comment (`:59-62`) grounds the fence's on it carrying the generation as the announcement being fenced. Correctly absent from §9; the same holds for `tests/tier0_static/claim_register_proto_agreement_test.go:41-43`, which exempts the fence because `pkg/adapter/coordination.go` already compares its generation.
- **`tests/claim-map.json` names `spec/04_system-components.md` exactly once**, under §4.4, so retiring the §4.1 block orphans no claim row.
- **0073's SPEC-7 does carry a §5.2 restatement**, at `spec/05:457` inside SPEC-7's range (`proposals/0073_...md:2823-3116`), so the summary's 0073 row is exact on both halves. `:2843`'s "the specification side takes no edit here" is about a different §5.2 sentence (`spec/05:451`) and reads as a refutation if taken alone.
- **`pkg/adapter/holdstate.go:335`/`:348` are the two interceptor `func` declarations and `:336`/`:349` their `s.inHoldState()` guards.** The summary cites the declarations, this log cites the guards, both are correct, and six entries have declined to file it. Do not reconcile them.
- **DECISION: the checklist, the deliverable index, and the §9 spec-map gloss were moved onto TEST-1's answer** (the two section 4.1 entries re-pointed, the section 28.5.3 entry deleted), and each refers to the entries by gate and section rather than repeating the `:156`/`:169`/`:5670` anchors, because spec-map line numbers move whenever an entry is added above them.
- **DECISION: the fifth-reader sentence in TEST-1 was corrected in place**, to "resolves no listed path through `repoFileLines`; it derives the expected inventory from the tree (`derivedInventoryCaseFiles`, `:1212`) and reports any derived file the list omits", keeping the four-versus-one split because a gate-file rename fails the four by unreadable path and the fifth by derived-but-omitted path, which are two different failure messages.
- **DECISION: the deliverable set is closed at three**, SPEC-1, TEST-1, and TEST-2, each named once in the index with the §9 file lists, against checklist steps S1, S2, and S3, one lane each, each depending on S1. The reconcile pass added, removed, merged, split, renumbered, and resequenced nothing.
- **DECISION: the `marker:0076` and `marker:0080` items stay in the impacts rows** rather than being promoted to `## Open decisions for human to make`, because both are keyed by marker text in another proposal's file rather than by an identifier this proposal stamped, and the impacts table is the only place this proposal may assert anything about another proposal. Five cleanup firings have placed them there.

### Traps

- **Naive proto parsers derail on this file, repeatedly.** `message SendMessageResponse {}` at `schemas/lenny-adapter.proto:984` and `message ReportPodScrubResponse {}` at `:508` open and close on one line; a parser that consumes to the next bare `}` swallows `AttachRequest` whole and mis-attributes its `reserved "slot_id"`. Comments also carry braces and `{sessionId}` paths. Symptoms are a message count of 81/82 instead of 88, a plausible but wrong reserved set, and "message does not exist" for two thirds of the request types. Strip comments, count braces per line, handle the same-line close, as `braceDelta` in `tests/tier0_static/adapter_proto_parse_test.go:44-48` already does. At least four rounds lost time here and one nearly filed a finding on the garbage.
- **Long lines hide the sentences citations point at.** `spec/05_runtime-registry-and-pool-model.md:515`, `spec/10_gateway-internals.md:60`, and `spec/04_system-components.md:725`/`:726` are each one very long line, and the cited clause is the last of several sentences on it. A `sed -n Np | cut` or a truncated grep shows none of it and makes a correct citation look wrong. Grep the sentence instead, or pipe a table row through `tr '|' '\n' | tail -3`.
- **MISTAKE: the addressee gloss cost a whole round.** Round 1, while tightening D1's predicate to `session_id` plus `SessionId`, imported the phrase "and addresses the pod's adapter process" from the retired `:188` and widened its population from the four `service Adapter` pod messages to every message either service declares. That is false of `ReportPodScrubRequest`. The lesson is twofold: when tightening a mechanical predicate, do not add a semantic gloss about what a class of messages reaches, and expanding a sentence's population is the failure mode to watch for whenever a fixer turns a one-line rule into paragraph prose. Any narrower version of the gloss is the same defect one step weaker; do not re-add one.
- **`ReportPodScrubRequest` is the pod-scope trap.** It is the only pod-scoped request that travels adapter to gateway, on `service GatewayControl` (`schemas/lenny-adapter.proto:261`, rpc at `:334`), which the gateway serves. It carries `pod_id` alone. Any sentence quantifying over pod-scoped messages and then naming an addressee or a handler is false of it, which is exactly why the retired `:188` enumerated four messages instead of quantifying.
- **MISTAKE: TEST-2 as a map addition could never be applied, and five rounds declined to say so.** `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141`) asserts both `reservesNumber(md, num)` and `reservesName(md, "slot_id")`, and `CoordinatorFenceRequest` declares neither, so no value of the retired-field-number column applies. Seven agents derived it independently and each declined on the ground that an open-decisions section was outside their lens. The constraint set determined the answer; it was never a preference question. The cost was four log entries, one summary OD, one §7 question, and a staged deliverable an implementor executing it would have turned tier 3 red with. The fix stage finally split the declaration. Do not re-derive and do not re-file.
- **MISTAKE: the `01d19af01` attribution was a false citation propping up a human decision.** An earlier round grounded "the fence never carried the retired duplicate" on that single commit, which introduced neither `SlotId slot_id` nor `CoordinatorFenceRequest`. It was the only stated evidence for the premise summary OD2 and §7 both rest on. Corrected to the message's own declaration plus commit `040323634`. The same wrong attribution stands verbatim in implemented proposal 0076 (`0076...summary.md:670`); 0076 is immutable, so do not propagate the correction there and do not list it as a touched file.
- **MISTAKE: the §6 attribution was misaddressed.** `non-spec-changes.md` §4 said "§6 of the staged spec changes bars the proto edit that would satisfy it". §6's Non-goals bar renaming the RPC, its field, or its field's type, and a `reserved` pair is neither a rename nor a wire change. What actually bars it is §3's "No proto or handler changes" and the summary's no-proto-file claim, and the real reason is the field history. The conclusion always survived; only the pointer was wrong. It is now stated on the field-history ground in exactly two places, `non-spec-changes.md` §4 and summary OD2. Do not add a third copy in the problem statement, the checklist, or deviations.
- **Do not re-file the "only way a request addresses a session" tension.** Paragraph 1 says a top-level `session_id` field of type `SessionId` is the only way a request addresses a session, while paragraph 2 addresses the envelope through a frame. Paragraph 3's "a session addressed under a name and a type that are both unconventional" fixes "the only way" onto the name-and-type spelling rather than the field's position, and `CheckpointStart` uses that same spelling. Declined at least five times, each after an hour of derivation.
- **Do not re-file the nested-address hole.** A request declaring `Foo foo = 1` where `Foo` carries a conventional `SessionId session_id` derives pod-scoped and escapes the gate. At the request's own top level the address-bearing field is `Foo foo`, whose name and type are both unconventional, so the staged constraint sentence already reads onto it. Declined three times.
- **Do not re-file the §4.7.1 "declared per message" tension.** The staged rule says the classification is derived rather than declared per message while `spec/04:725` and `:726` declare it per message. The values agree with the derivation, SPEC-1 names both and states why they stand, and an earlier finding about the pod-scope clause was refuted on `:726`'s survival. Declined at least six times; a new filing needs a stronger argument than "declared" versus "derived".
- **Do not re-file the session-scoped fence exiting a pod-wide hold.** `spec/10:57` makes the fence the only exit from hold state while the handler now requires a bound slot entry, so a pod in hold with no bound entry can only reach the timeout. This is 0076's landed behavior; 0076's OD3 weighed and rejected keeping the row pod-scoped on exactly this ground, D3 records the rejection, and §6 makes re-deriving OD3 a non-goal. `spec/04:190`'s `ShutdownRequest` paragraph is the standing precedent for a session-scoped message whose handler has a whole-pod effect. Four lenses reached it independently and stopped.
- **Do not re-file `AdapterEventsRequest` as a counterexample.** It declares `bytes envelope_json = 1` and nothing else, and the session it concerns travels inside that payload. It derives pod-scoped, matching the retired row. `AdapterTerminating` rides `AdapterEventsResponse`, which is a response, so no request message addresses a session through that stream. It is the first message a reviewer reaches for.
- **Do not re-file `pkg/adapter/oplock.go:77`.** Its "An interrupt is pod-scoped" is about which op lock the handler takes, not about the request's address, which is the same address-versus-effect divergence `spec/04:190` explains. A `grep pod-scoped pkg/` hits it first.
- **The staged gate sentence's "a protocol definition" is indefinite.** `schemas/lenny-interceptor.proto:56` and `schemas/lenny-tokenservice.proto:65`, `:154` each declare `string session_id`, so a gate whose domain is read as `schemas/*.proto` is red on its first run. The reading is saved only by the block's "the gateway-adapter protocol" antecedent, by D2's "the protocol", and by `adapterProtoPath` (`tests/tier0_static/adapter_proto_parse_test.go:18`). A rewrite of that paragraph must keep the antecedent, and the implementor must scope the gate to `schemas/lenny-adapter.proto`.
- **Do not restate the empty-identifier refusal in §4.1.** `tests/tier0_static/address_rule_citation_test.go` calls `t.Fatalf` when more than one numbered spec section carries "rejected at the adapter boundary with `InvalidArgument`". The staged block is clean today. A fixer who folds the value rule's wording into §4.1 turns tier 0 red with a message about the address rule, for a reason nothing in this proposal predicts.
- **`parseMessageScopeTable` reads the whole of `spec/04`**, not the §4.1 section, and `messageScopeRow` matches any four-column row whose first two cells are backticked single words. The simpler "the table's rows are its only input" model is wrong, though no other such table happens to exist. Do not improve the retiring gate on its way out; it is deleted.
- **`CheckpointStart` is not an RPC request type.** It is a `oneof` arm, it is already a `sessionScopedMessages` member, and the retiring gate injects it by hand. Any count that mixes the 26 address-declaring messages with the 31 request types produces a spurious off-by-one that reads as a defect in D1's reproduction claim.
- **`CheckpointRequest` must stay out of the tier-3 session set** even though the envelope clause classifies it session-scoped. It declares no top-level address of its own, so the address arm fails on it; `CheckpointStart` stands in its place.
- **Do not add `tests/tier0_static/spec_map_slot_address_registration_test.go` to any files-touched list.** It names the gate by path and TEST-1 rewrites the file in place, so it needs no edit. Adding it stages an edit nothing requires.
- **Do not correct a review-log entry or an implemented proposal.** A log entry records what was true when it was written; 0073 and 0076 are Implemented and immutable, and the project rule bars amending a landed proposal to track a later reversal.
- **Snapshot diffs are routinely empty and that does not mean nothing changed.** The round-N snapshot is taken at round start and has repeatedly been byte-identical to the live directory, so `diff -ru` against it prints nothing and looks like a broken snapshot. Snapshot round numbers do not order monotonically against the live tree either: `non-spec-recheck-r4` differed on six files while `non-spec-recheck-r2` matched exactly, so a later snapshot is not necessarily the closer one. The reliable delta is `git diff 470cd4e34 HEAD -- <proposal dir>`, where `470cd4e34` is the post-non-spec-loop baseline; otherwise diff against the earlier snapshot in the lane (`spec-r2`, `non-spec-r6`, `spec-recheck-2-r1-start`).
- **Cache keys omit `summary.md` and `review-log.md`, and the cache path carries no lane.** The key is `md5(spec-changes + non-spec-changes + implementation-checklist)` and the filename is `<lens>-r<round>-<hash>.json`, so a spec-lane and a non-spec-lane run of the same lens at the same round number collide on one slot, and a summary-only edit is served a stale hit. This is the highest-value entry in this section by citation count: at least eight lenses recorded opening the cached JSON and reading its `coverage` prose instead of returning it, and each found another lane's scope filter inside. Returning such a hit reports one lane's pass as the other's. Read the `coverage` string first; decline when it names the wrong substrate; and never treat a hit as covering a summary rewrite, which is where OD1, OD5, and the unstaged-defect entries live.
- **Orchestrator briefs carry stale content.** Their proto anchors are the pre-refresh set (`:589`, `:1166`, `:1447`, `:1448`), each off by seven; the proposal's own anchors are the correct ones. The briefs also still say §1.2 claims the pod-wide ground holds, and still describe "§7 question 1" as the existential question. Trust the proposal, not the brief.
- **`sessionScopedMessages` is read at three sites**, `:81`, `:102`, and `:130`. The split repoints `:130` at `retiredDuplicateNumbers` and changes `:81`/`:102` from `for name := range` to `for _, name := range`. Missing the range-form change is a compile error and fails loudly; missing the `:130` repoint is what leaves tier 3 red.
- **Do not rename or add a test function in the tier-3 file.** `tests/tier0_static/address_rule_citation_test.go:45-47` names `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` by function name, and `tests/spec-map.json` credits the file whole-file. Changing declarations and loop populations costs nothing; adding a fifth test function drags that register, and possibly the spec map, into a blast radius §9 does not list.
- **`protoFields` folds `oneof` arms into the enclosing message's field set on purpose**, and `claim_register_proto_agreement_test.go` depends on that. TEST-1's extension must add the field's type and its `oneof` membership without changing which fields `protoFields` reports, or the claim-register agreement gate silently changes meaning.
- **MISTAKE: a round was spent on gates that cannot fire.** Someone chased whether retiring the table would redden `residual_gate_test.go` or `identifier_resolution_test.go`. Both range over §28.3 channel, link, and register identifiers and over line-citation, anchor, generated-artifact, skip-reason, reserved-phrase, and change-graph classes. The deleted block carries no such identifier, no line citation, and no anchor, and neither `tests/change-graph.json` nor `tests/claim-map.json` names any touched test file. Do not re-open.
- **MISTAKE: `sed` with several `p` ranges emits lines in file order, not in the order the ranges are written.** One pass mis-mapped four proto anchors before noticing. Verify one anchor at a time, or use a single range.
- **Do not re-derive the proto census or the blast radius.** Both have been derived independently more than a dozen times, by different methods, with the same answer. Spot-check instead. The same holds for the `spec/04:725`/`:726` survivor list, which is the single most reused fact on this proposal.
- **Two loose characterizations that are not defects.** The summary calls the retiring gate's four non-coverage refusals checks of "the table against itself", but two of them (unknown message, wrong service) compare the table to `protoServiceRequests`; the load-bearing claim, that all four lose their subject with the table, holds either way. And D1's "the ones that carry none are exactly its pod rows" reads as off by one unless the "other than the stream envelope" restrictor distributes, which the next sentence makes plain. Both are wording; both have been examined and dropped more than once.
- **A round that tried to widen clause 1's population and withdrew it.** Widening the classification clause from the RPC request types to "request messages plus every frame an envelope carries" was the reviewer's own suggestion and was rejected: it classifies `CheckpointGrant` and `CheckpointAbort` pod-scoped, which is false of a continuation frame on a session's stream. The rejection survives on that ground alone, independent of the deleted addressee gloss, so the envelope clause needs no rework while the current design stands.
- **A round that tried the "frames must agree on scope" simplification and withdrew it.** `CheckpointStart` declares the address and `CheckpointGrant`/`CheckpointAbort` declare none, so an envelope's frames do not agree on scope and a gate written that way fails on day one. The envelope clause has to key on the one frame that declares the address.
- **MISTAKE: a false "SPEC-1 cites `:458`, one line off" note stood in this section for two windows and cost roughly twenty-five re-derivations.** Every lens in both lanes reached it, checked the proto, and wrote the same `CORRECTS`; several recorded that they had nearly "fixed" a correct citation into a wrong one on its strength. The note's origin is recorded: a `sed -n '450,510p'` whose first output line was miscounted. The lesson generalises past this line. A range-print in `schemas/lenny-adapter.proto` and in `docs/reference/adapter-contract.md` miscounts, because very long wrapped rows make a blank line read as absent; `grep -n` is the only reliable way to anchor in either file. A wrong anchor in a curated section is more expensive than a wrong anchor in a proposal, because every reader trusts it.
- **Do not apply the "per-session precondition" correction to `docs/reference/adapter-contract.md:69`.** An earlier deferral proposed rewriting "precondition for any subsequent operational RPC" to name the fenced session. The tree refutes that. The hold is enforced by pod-level interceptors on `s.inHoldState()` alone with a method-name allowlist (`pkg/adapter/holdstate.go:336`, `:349`), and `s.exitHoldState()` runs after a successful fence for **any** bound session (`pkg/adapter/coordination.go:150-156`), so an RPC naming session B is blocked until some fence lands and is unblocked by a fence for session A. `spec/28:322` states it unqualified. What 0076 made per session is the generation the pod records, not the hold. Keep the two halves apart: the announcement clause is imprecise, the precondition clause is correct and must not be qualified.
- **Do not lean on "direction survives in §4.7.1's two RPC tables" as complete ground.** The `Gateway → Adapter` table (`spec/04:698-719`) omits `SendMessage`, `SignalDeadline`, `RevokeCredentials`, `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`, and the `Adapter → Gateway` table carries only the two scrub RPCs. Retiring the §4.1 table therefore drops the service and direction columns for about eleven request messages with no other spec carrier. The conclusion still holds, because nothing in the tree reads those columns and no applied text becomes wrong, and OD1 owns the judgement. Two rounds have stated the weaker ground and one corrected it; do not restate it as complete.
- **"Stream envelope" keys on the `oneof`, never on the RPC's streaming-ness.** `AttachRequest` (`rpc Attach(stream AttachRequest)` at `schemas/lenny-adapter.proto:84`), `PrepareWorkspace` (`:41`), and `AdapterEvents` (`:241`) are client-streaming and carry no `oneof`, so clause 1 classifies them. A reviewer who reads the predicate as "request type of a streaming RPC" thinks it selects four messages instead of one.
- **Do not widen "request message" to "message" in the envelope paragraph or in D2.** `CheckpointResponse` declares a `oneof` whose frames declare zero addresses, so a sentence quantifying over messages rather than request messages turns the replacement gate red on day one.
- **The ledger is not in chronological order and carries duplicate headings.** The cleanup blocks sit in lane order rather than firing order, and there are two blocks headed `### [f1.cleanup]` and two headed `### [f2.cleanup]`, describing different states of the summary's open-decision identifiers. A join on the heading alone returns both, and taking ledger position for chronology reads them backwards.
- **A bare `OD<n>` match picks up a neighbouring proposal's decision.** This proposal's live identifiers are OD1 and OD5. `OD2` occurs once more in the fence-retry defect entry naming 0076's OD2, and `OD3` occurs five times naming 0076's OD3. The identifiers have also been renumbered between firings, so an entry written against "OD2" may describe what is now OD5.
- **`spec_map_slot_address_registration_test.go:232-234` says "The map validator walks tier 2 through tier 10 only".** That sentence is about the credit and selection walk, a different validator. Reading it as being about `validateSpecMapTestFuncs` produces a false finding that S2's justification for folding the spec-map re-registration into itself is wrong. It is not: `validateSpecMapTestFuncs` walks every section and every `path::TestName` entry regardless of tier.
- **The five `// spec: 4.1 (request message scope)` annotations in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`** (`:26`, `:132`, `:408`, `:582`, `:747`) are the first thing an edit-sites or docs lens hits when it greps for the phrase. The file reads `docs/` pages and §4.7/§5.2 rows and opens no `spec/04` text at all, and SPEC-1 keeps the heading the annotations name, so no edit is owed and the file does not belong in §9. Declined five times.
- **Bash is the wrong tool for reading this log and for repo-wide greps.** Output over roughly 2KB is persisted to a file instead of printed, and re-reading that file persists again, so `awk`/`sed`/`cat` over the standing context costs two wasted calls; use the Read tool with an offset. Twelve separate entries record losing two to four calls each relearning this, which makes it the most-cited trap in the section after the cache key. The recipe that works: `grep -n "^## \|^### "` on the log for the section offsets, then Read with `offset`/`limit`. A `grep -rn` from the repo root routinely produces 100KB or more, because the review logs and the archive quote every anchor under review; scope the grep to `spec/ tests/ pkg/ docs/ schemas/` or append `| grep -v "^./proposals/"`.
- **MISTAKE: "the fifth case reads no file" was the opposite of its mechanism and cost a finding.** An earlier round wrote that `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` reads no file and checks inventory membership alone. It reads every `_test.go` body under the inventory walk roots, through `derivedInventoryCaseFiles` (`:1212`) over `repoTestFiles`/`repoFileBytes` (`:1269`), and it would fail on a renamed gate file by deriving the new path and reporting the inventory as omitting it. The true statement is about which helper it calls: it resolves no listed path through `repoFileLines`. Any future sentence about which of the five loops a rename breaks must say "resolves no entry through `repoFileLines`", never "reads no file". The paragraph's conclusion, that keeping the path leaves the inventory correct, was and stays true.
- **MISTAKE: a fix that changes what a deliverable does must sweep the checklist and the deliverable index in the same pass.** The round answering OD4 rewrote `non-spec-changes.md` §5 TEST-1 and two summary sites but left `implementation-checklist.md:13-14` ordering a section 28.5.3 registration and `summary.md:274-275` saying "under the sections the retiring gate's cases held", both now the opposite of TEST-1. Four lenses filed it independently and the checklist is the document `implement-proposal` executes, so the stale copy was the one that would have cost an implementor a wrong `tests/spec-map.json` edit. Both are now corrected. The lesson is mechanical: grep the checklist and the index for the old wording before returning.
- **Do not file on the section 4.1 credit the replacement gate keeps.** The symmetry is tempting: TEST-1 deletes the §28.5.3 credit quoting "credits that section with coverage no regression in it would break", and the replacement gate reads the proto alone, so no regression in §4.1's prose breaks it either. It is not symmetric. §28.5.3's addressing content is JSON Lines frame equality on a different wire (`spec/28:840-841`), which the gate never touches, whereas §4.1 is where SPEC-1 states the very convention the gate holds over the proto. Credit follows the behavior a case pins rather than the file it opens. Derived and dropped at least three times, an hour each.
- **The credit-doctrine quote's own gate does not cover the gate file.** `tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` is the doc comment of `TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise` (`:94`), whose subject is `slotAddressAbsenceTestFile = "tests/tier3_contract/rest_sessions/slot_address_absence_test.go"` (`:44`) alone. The proposal claims the tier-0 register gates *state* the doctrine, which is true. A fixer who tightens that into "the gate refuses that credit" makes it false, and nothing mechanical would catch the surplus credit if it were kept.
- **`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` takes a case's demand as the union of its `// spec:` annotation and its `_spec_X_Y` name suffix** (`citedSectionsPerCase` through `citedSectionsPerCaseFromLines`, `:806-812`). A replacement case named `..._spec_28_5_3` re-demands the very credit TEST-1 deletes. Name the replacement cases `_spec_4_1` or leave the suffix off.
- **The replacement gate should declare exactly two test functions.** The per-case branch fires whenever a file carries any `::TestName` entry, so every case in the rewritten file that cites 4.1 needs its own `tests/spec-map.json` row, and TEST-1 re-points exactly two. A third function without a row turns tier 0 red inside S2. The failure is loud and the fix is one JSON line, which is why three passes declined to file it, but it is the first thing to check if S2 reddens.
- **The replacement gate must keep a caller for `protoServiceRequests`.** It is the only thing in the package that says which messages are request types, and clause 2 of D2 quantifies over request messages carrying frames in a `oneof`. A gate written to read `protoFields` alone leaves `protoServiceRequests` with zero callers and golangci-lint's `unused` reddens tier 0 on a symbol nobody edited. This is a second, independent reason not to widen "request message" to "message".
- **`claim_register_proto_agreement_test.go` reads `protoFields` at two shapes, not one**: the per-field lookups `fields[msg][field]` at `:72` and `:82`, and a range over the whole map at `:92-95` testing `declared[generationFenceField]`. TEST-1's signature change has to move both; a fixer who patches only the lookups leaves the fence-coverage half broken.
- **Do not write "proposal §5" in the rewritten tier-3 declaration comment.** `TestAddressCaseTextCitesNoProposalSectionNumber` (`:1054`) fails any line in a listed file matching `proposal\s+§\s*\d`, comments included, and the tier-3 suite is a `slotAddressCaseFiles` member. Nothing staged does this, but the new comment states the derivation rule and is exactly where a fixer would reach for a section number.
- **`gate_integrity_test.go`'s `tierZeroGates` (`:63-90`) is a fixed list keyed by test-function name.** The message-scope gate is absent from it, so TEST-1's case renames are free, but the list does name `claim_register_proto_agreement_test.go` / `TestClaimRegisterAgreesWithTheAdapterProto`, which TEST-1 edits without renaming. An edit-sites pass reaching for a tier-0 gate inventory hits this file first; it owes no edit.
- **A naive `grep -c reserved` over the proto returns 19 messages, not 18.** `NegotiateVersionResponse` carries a bare `reserved 5;` with no name, so it is not a `reserved "slot_id"` member. Count the name, not the keyword.
- **`grep -rn <name> --include=*.json tests/ pkg/ cmd/` silently missed `tests/spec-map.json`** in one round, and the agent concluded there was no spec-map dependency at all. The plain per-file grep returned the three hits. Use `grep -n <name> tests/spec-map.json` to confirm a blast radius rather than the `--include` plus multiple-directory form.
- **`docs/reference/adapter-contract.md:69` does not contain the phrase "to the pod".** It reads "Announce new coordination generation on gateway handoff; precondition for any subsequent operational RPC". The summary calls it "the same compression" as `spec/04:712`, which is true at the level claimed, because both drop the per-session qualifying clauses. A reviewer who expects the docs row to mirror `:712`'s wording verbatim reads a correct citation as false.
- **`docs/runbooks/coordinator-handoff-slow.md` is about delegation handoff**, a parent session passing control of a delegated child, not about the `CoordinatorFence` coordinator handoff. It also carries a live pre-existing mismatch: its step-1 query groups `lenny_coordinator_handoff_duration_seconds` by `phase` while `docs/reference/metrics.md:310` gives that histogram's labels as `pool` and `outcome`. That is a residue-register candidate on a mechanism this proposal does not touch. Do not file it here and do not let it pull the fence-retry defect entry wider.
- **The "session-scoped frame" vocabulary on the JSONL leg is a different classification and it is dense.** A grep for `session-scoped` reaches `schemas/lenny-adapter-jsonl.schema.json:61` and `:211`, all three runtime SDKs, `pkg/adapter/attach.go:274`, and five `docs/runtime-author-guide/` pages before anything on the gRPC leg. `spec/28:834-836` defines that set by enumeration (`message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, `status`), independently of the §4.1 table, so retiring the table orphans neither `lenny_adapter_unaddressed_frame_rejected_total` nor any of those. Do not file a "declared set contradicts the derived rule" finding on it.
- **`spec/04_system-components.md:1047` declares `string session_id = 2;` inside a protobuf block.** It is `RequestInterceptor`'s `InterceptRequest` from `schemas/lenny-interceptor.proto`, a different protocol that the staged rule's "the gateway-adapter protocol" antecedent excludes. A reviewer grepping `spec/` for `string session_id` hits this first and it reads as a contradiction of the staged constraint paragraph.
- **Do not re-file the residual escape in TEST-2's hand-entered-set paragraph.** It says the one arm an omitted message escapes is `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1`, adding that the wrapper type that name carried is barred by `TestTheRetiredAddressWrapperIsGone_spec_15_4`. That case bars the message type `lenny.adapter.v1.SlotId` (`:150-158`) and not a future field named `slot_id` of some other type, which is what the escaped arm tests for (`:87-89`). The sentence claims the type is barred and never claims otherwise, and the residual is exactly what OD5 puts to the human. Examined and declined by four lenses under both the coverage and the security bar.

### Open

- **OD1, the human's.** Retire the table and its gate, accepting the two residuals. Recommendation "yes" at moderate confidence, with 0073's rejected-alternative ground now in the entry. [f1.open-decisions.OD1]
- **OD5, the human's.** Nothing keeps `sessionScopedMessages` complete once its rule becomes proto-derivable; a new session-addressed request message falls out silently, which is what let eight messages drift out already. [non-spec.1.fix-G1.1, non-spec.3.review-mechanism.1, f1.cleanup, f2.cleanup]
- **OD5's entry is under-specified.** It states its question, its ground, and both branches, and no recommendation, losing alternative, cost of deciding otherwise, or confidence, which is less than the section's contract asks. Six format firings have observed it and left it, because supplying them is adjudication a format pass may not do. [f1.cleanup, f2.cleanup, f3.cleanup, f4.cleanup]
- **A false historical reservation on the fence.** Whether adding `reserved 3; reserved "slot_id";` to `CoordinatorFenceRequest` would be caught by any gate. No such gate was found; the reason not to do it is the field history. [non-spec.3.review-citations.1]
- **`spec/10:39`'s retry clause is unmet on both halves.** The adapter refuses the same-generation retry it orders, and the driver applies no backoff at all. Both pre-existing; the refusal half is 0080 §1.16 and the backoff half is now settled as owned by nobody. [spec.4.review-reliability.1, f1.open-decisions.out-of-scope.spec-10-39-fence-retry]
- **0073's value rule is half-landed.** Its second clause, "a pod-scoped request never resolves a session root regardless of what `session_id` carries", never landed in any spec file. [spec-recheck.1.review-security.1]
- **Which session's fence releases a pod-wide hold** is stated in no spec sentence, although the code answers it (a fence for any bound session exits the hold). 0076 or 0080 territory. [spec-recheck.1.review-security.1, non-spec-recheck.1.review-security.1]
- **`tests/claim-map.json`'s `CoordinatorFence` row** anchors `pkg/adapter/coordination.go:85` while the handler is at `:107`. Generator-produced, a 0076 residue. [non-spec-recheck.1.review-fresh.1, non-spec.3.review-citations.1]
- **The dangling `§11`.** `status.md:22` cites a "§11" that exists only inside this review log, so the pointer resolves across files. The summary's copy is gone; the status file is the only carrier left. [reconcile.1, reconcile.2, reconcile.3, f1.cleanup]
- **0080 §2 at `:453-454`** records the §4.1 `ShutdownRequest` classification limit as taken by 0075, which does not take it. [spec.2.review-citations.1, reconcile.2, reconcile.3]
- **Positional self-references in the staged block** ("the paragraph below", "the first paragraph") hold only while the three paragraphs stay adjacent and in order. [spec.2.review-fresh.1]
- **SPEC-1's appositive** "the whole block 0073's SPEC-7 stages" under-describes SPEC-7, which also staged the `ShutdownRequest` paragraph and the §4.7 rows. The explicit line enumeration makes the edit unambiguous. [spec.3.review-applicability.1, spec-recheck.2.review-applicability.1]
- **§8's stale framing.** It still opens `**IMPLEMENTOR TO FILL THE BLANKS.**` and says "The specific cases are written during convergence" while convergence has closed with no case named by function name. Seven test-coverage passes judged the five enumerated case subjects sufficient, so this is stale wording rather than a coverage gap. [non-spec-recheck.1.review-test-coverage.6, non-spec-recheck.2.review-test-coverage.1]
- **The summary's defects preamble mis-scopes TEST-2.** "Both defects this proposal confirms in the specification are staged" then names TEST-2, whose subject is a comment in a tier-3 test file rather than a sentence in `spec/`. [f2.cleanup, f3.cleanup, non-spec-recheck.1.review-citations.1]
- **`spec/04:190` survives beside the new rule.** Its closing clause "so neither operation is selected by a field's presence standing in for a scope" sits two paragraphs below a rule that classifies by field presence. Examined and not filed, on the ground that `:190`'s subject is handler dispatch on one instance rather than message classification; recorded because it is fresh rather than declined. [spec-recheck.1.review-citations.1]
- UNVERIFIED: whether the staged limit sentence is exhaustive. "What the gate cannot see is a session addressed under a name and a type that are both unconventional" reads as the whole blind spot, and a `session_id` inside a plain non-`oneof` nested message field would also pass all four conjuncts. No such message exists and D4 declines the adjacent argument, so it was judged hypothetical hardening. [spec-recheck.2.review-performance.1]
- UNVERIFIED: `spec-changes.md:127-129` says 0073's recorded gate limit "goes with the gate TEST-1 replaces" while the summary lists it under what stands. Reconcilable, since the header comment is deleted and the limitation persists against the replacement gate, but the pronoun is ambiguous. [non-spec-recheck.3.review-fresh.1, non-spec.3.review-security.1]
- UNVERIFIED: nobody has run tier 0 or tier 3. Every green-or-red claim in this log is derived from reading files. The three a run would settle are the `validate-maps` green window across S2's single commit, the replacement gate passing on the shipped proto, and the widened tier-3 arms. The implementor should run `lenny-test --tier 0` immediately after S2 rather than after S3. [non-spec.3.review-feasibility.1, non-spec-recheck.2.review-edit-sites.1]
- **Whether the `human` routing of `marker:0076` and `marker:0080` asks for an entry under `## Open decisions for human to make`** rather than an impacts row. Five cleanup firings placed them in the rows and the brief's disposition disagrees with the firings'. A pass with authority to stamp an identifier should settle it, and the numbering gap left by OD2, OD3, OD4, and OD6 is not available to reuse. [f4.cleanup]
- **A pre-existing surplus spec-map credit under the section SPEC-1 rewrites.** The three SDK client cases at `tests/tier3_contract/sdks/{go,python,typescript}_client_test.go::Test*ClientMessageBodyOmitsSlotAddress` are credited to section 4.1 while annotating `// spec: 15.4 (MessageEnvelope), 7.2 (message dispatch)` alone. No gate catches it, because the credit gate is one-directional. Not this proposal's to fix and not in §9. [non-spec.2.review-client-surface.1]
- **`docs/getting-started/concepts.md:101`** ("A separate `coordination_generation` counter tracks gateway replica handoffs") may need the per-session qualification 0076 introduced. It states no scope class, so it is not falsified, but it sits beside the two 0076 residues already recorded as unowned and whoever takes those should look at it in the same pass. [non-spec.1.review-docs-alignment.1]
- UNVERIFIED: whether OD5's entry stands in the summary or was withdrawn. `f1.open-decisions.OD5` records the answer staged at `non-spec-changes.md:94-108` and the entry deleted from `summary.md`, and several later lenses confirmed that paragraph is in the tree; `f4.cleanup`, the later firing, records OD1 at `summary.md:85-151` and OD5 at `:153-159` as the two live entries, saying OD5's proposed resolution was refuted at the gate and never attempted. The two are reconcilable if the paragraph stayed and the summary entry came back, but nobody has said so. `### Settled` and `### Open` here follow the later firing. [f1.open-decisions.OD5, f4.cleanup]
- UNVERIFIED: whether `repoFileLines` opens at `tests/tier0_static/spec_map_slot_address_registration_test.go:699` or `:702`. Most entries cite the `:699-706` range the proposal uses; `non-spec.1.review-mechanism.4` reports the declaration at `:702-710` with `:699` carrying `testFuncRE`. Overlapping and below the bar, judged not a defect twice, and one `grep -n "func repoFileLines"` settles it. [non-spec.1.review-mechanism.4]
- UNVERIFIED: whether any tier-1 or tier-3 case pins the empty-identifier refusal for the other 24 session-addressed request messages, or only for `CoordinatorFenceRequest` (`pkg/adapter/coordination_test.go:35`). Not a finding here, because the derived class adds exactly one message and that one is pinned, but a later proposal that widens the class should check it. [non-spec.1.review-security.1]
- UNVERIFIED: the summary cites the tier-11 code-block walker as `tests/tier11_docs/code_blocks_test.go:103-113` while `case "sql"` and `return checkSQLBlock` sit at `:114-115`, so the range is two lines short of the enumeration it supports. The substance, that no `protobuf` case exists, is exact. Declined once as a range error rather than a claim error. [non-spec.2.review-feasibility.1]

### Deferred

- **DEFERRED [spec/04_system-components.md]:** the `CoordinatorFence` row at `:712` still reads "Announce new `coordination_generation` to the pod on coordinator handoff", which 0076 falsified by moving the recorded generation onto the session's slot entry (`pkg/adapter/slot.go:59`). What is true instead: the fence announces the generation for the session it names. Two corrections to the earlier form of this entry, both derived and both applied to the summary rather than here: `spec/28:314-317` does **not** contradict the row (it carries the row's own words and adds the per-session clause after them, so the row compresses §28 rather than contradicting it), and 0080's inventory does **not** own it (§1.1 through §1.21 name no such entry, so the defect is unowned). The row's reader-facing mirror at `docs/reference/adapter-contract.md:69` carries the same compression and moves with it. The defect is recorded in the summary under `## Defects in the shipped tree that this proposal does not stage`; the edit itself remains unowned. Note that an earlier entry cited the source row as `:713`, which is the `ExportPaths` row.
- **DEFERRED [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]:** §2's bullet at `:453-454` splits. The declared message-scope table and its reconciliation gate stay on the owned side; the §4.1 `ShutdownRequest` classification limit returns to §1's inventory as a gap no proposal takes, because SPEC-1 leaves `spec/04:190` unedited and the replacement gate reads the protocol definition alone. 0080 is another proposal's file and a Draft, so the correction lands there.
- **DEFERRED [0075_....status.md]:** the status file's `**Date:**` line reads "§11 records what the rewrites changed". The only `## 11` in this proposal is inside this review log, so the reference resolves across files rather than within the document a reader is holding. The summary's copy of that pointer has already gone; the status file is now the only carrier. Either name the review log or drop the reference.
- **DEFERRED [docs/api/internal.md]:** the page is a pre-0073 snapshot of a protocol that has since been rewritten, and the deferral covers the whole page rather than one excerpt. It publishes a service named `RuntimeAdapter` (`:74`) that no proto declares, RPCs `StopSession` (`:81`) and `UploadFiles` (`:89`/`:94`) that neither `service Adapter` nor `service GatewayControl` declares, and five request messages carrying a top-level `string session_id`: `StartSessionRequest` (`:111`), `StopSessionRequest` (`:147`), `CheckpointRequest` (`:211`, inside the `:209-215` excerpt), `UploadChunk` (`:245`), and `DemoteSDKRequest` (`:273`). The shipped `CheckpointRequest` is a `oneof msg` of `CheckpointStart`/`CheckpointGrant`/`CheckpointAbort` plus `int64 coordination_generation = 4` and declares no `session_id`; the shipped `DemoteSDKRequest` declares `string reason = 1` alone and derives pod-scoped. SPEC-1 does not make any of this wrong, because it is wrong today, but it changes the severity: a top-level `string session_id` on a request message becomes a spelling the specification's constraint paragraph forbids. Nothing this proposal applies reddens on account of the page (`tests/tier11_docs/code_blocks_test.go:103-113` dispatches on json, yaml, go, bash, and sql, with no `protobuf` arm). The docs loop, or 0080's residue inventory, owns it, and should take all five sites.
- **DEFERRED [0075_....spec-changes.md]:** D2 calls the addressing convention "an addressing convention that nothing checks today", which overstates it. `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each member of `sessionScopedMessages`, that `session_id` exists and is of type `lenny.adapter.v1.SessionId` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`, the type comparison at `:112-114`). What is true instead: no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset. Two spec-lane citation passes filed this as a finding once the loop owned the file, and at least six other rounds re-derived it and declined on the ground that it is decision rationale rather than staged spec text. No ledger entry records it applied. Whoever touches D2 next should land the correction in passing.

## Ledger

### [non-spec-recheck.1.review-applicability.1]

DECISION: Returned the cached applicability answer verbatim rather than re-running the review — BECAUSE the cache probe hit `applicability-r1-b32f3475fbae.json` and the standing instruction is to return a hit and do no other work. ALTERNATIVES: re-deriving the simulation from scratch, rejected because the hash covers spec-changes.md, non-spec-changes.md and implementation-checklist.md, all three unchanged (mtimes 00:15, 00:16, 00:15 on 2026-09-08), and summary.md (mtime 00:15) also predates the cached run, so the cached agent read the identical four-document set.

FACT: the applicability cache key does NOT encode the lane, only the three-file md5. So a lane collision is normal, not a bug: `applicability-r1-b32f3475fbae.json` (written 01:37, spec-recheck lane) and `applicability-r2-b32f3475fbae.json` (written 00:30, non-spec lane) are two independent applicability reviews of the SAME text, and both returned an empty findings list. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/applicability-r1-b32f3475fbae.json, .../applicability-r2-b32f3475fbae.json

WATCHOUT: a future agent on this lane will see the probe print a coverage narrative that reads as spec-side ("Diffed the live directory against .../spec-r3") and may conclude the hit is from the wrong question and re-run. Do not. Check the sibling `-r2-` file at the same hash first: the non-spec lane's own applicability simulation over the identical text is recorded there, and it walks the non-spec surfaces in full (`slotAddressCaseFiles` and its five readers, the tier-3 loops, checklist lanes and Depends-on, all boxes unchecked). Between the two, the whole staging is covered. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/applicability-r2-b32f3475fbae.json

UNVERIFIED: neither cached run executed tier 0 or tier 3, so the predicted red window during S1->S2 and the replacement gate's day-one greenness rest on reading gate source. The implementor is who settles this, at the first run of the new gate.


### [non-spec-recheck.1.review-citations.1]
DECISION: returned an empty findings list — BECAUSE the cache at scratchpad/cp-cache/0075_.../citations-r1-b32f3475fbae.json hit with the exact current text hash, and its single finding is the D2 "an addressing convention that nothing checks today" item this round's prompt lists as already refuted; re-reporting it is barred. ALTERNATIVES: returning the cache verbatim (rejected: it would burn two verification agents on a known-refuted finding and pollute the refuted list).
WATCHOUT: the cache key is md5 over spec-changes + non-spec-changes + implementation-checklist ONLY. summary.md is NOT in the key, so a cache hit does not prove the summary was reviewed at its current text. I re-verified the summary independently for that reason. — EVIDENCE: the H computation in the CACHE stanza of the task prompt
FACT: summary.md and non-spec-changes.md are byte-identical to the spec-recheck-r3 snapshot; the whole delta this loop was told to look at is the spec-r3 -> current diff, which touches only summary.md (the 0073/0076/0080 impact rows, the OD4 deletion, the confidence paragraph, the coordfence backoff half-clause, the deliverable index) and one paragraph pair in non-spec-changes TEST-1 (the fifth slotAddressCaseFiles reader, and the 28.5.3 deletion rationale).
FACT: every concrete citation in that delta verifies against the tree. Checked and holding: proposals/0073_...md:6653-6655 and :6686 (both quotes exact); spec/04_system-components.md:175 (fence row says `pod`), :712 (§4.7 CoordinatorFence announcement row); docs/reference/adapter-contract.md:69 (its mirror); spec/10_gateway-internals.md:39 ("up to 3 attempts with 1-second backoff"); pkg/gateway/coordination/coordfence/coordfence.go:171-179 (stale arm re-read + relinquish) and :180-183 (transient arm), and `grep -n "time\.|Sleep|backoff|Ticker|After("` over that file returns NOTHING, so the "no timer, sleep, or backoff of any kind" claim is exact and it is the package's only non-test file; pkg/adapter/coordination.go:109-111 (empty-session InvalidArgument) and :127-134 (stale rejection); spec/05_runtime-registry-and-pool-model.md:515; docs/api/internal.md:209-215 (CheckpointRequest excerpt) and :272-274 (DemoteSDKRequest excerpt); tests/spec-map.json:156, :169 (section 4.1 block) and :5670 (inside the "28.5.3" block that opens at :5643); tests/tier0_static/spec_map_slot_address_registration_test.go:89-93 (credit-doctrine quote), :699-706 (repoFileLines), :1110 (fifth reader's range), :1212 (derivedInventoryCaseFiles decl); spec/28_communication-channels.md:501-502 (intra-pod boundary sentence) and :840-841 (address equality); tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43 comment, :39-43 deleted clauses, :44 var decl, :45-62 the eighteen entries, :130 the reservation case's range.
FACT: `protoServiceRequests` has exactly one caller, tests/tier0_static/adapter_proto_message_scope_test.go:87; `protoFields` has exactly one caller outside its own file, tests/tier0_static/claim_register_proto_agreement_test.go:64. The two tier-0 files 0076 added call neither: they use `protoCommentBlock` and `adapterProtoRel`, both declared in a third untouched file, tests/tier0_static/adapter_proto_intrapod_pointer_test.go:30 and :71. The summary's "nothing 0076 landed is opened" sentence is therefore exact, and TEST-1's caller list is complete.
FACT: proposals/0080 §1 runs 1.1 through 1.21 and its §3 "Deliberate exclusions" names only the maxConcurrentSessions isolation-gate conditions, the sizing/admission/capacity/draining/recycling arithmetic, the service-mode sense of "slot", and the slotlayout package rename — so the summary's two suggested new §1 sites (the §4.7 announcement row, the docs/api/internal.md excerpts) are genuinely uncarried and genuinely not excluded. — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:463-470
USEFUL [the prompt's refuted list]: it caught that the cached answer was stale in substance while matching in hash. Without it this run would have re-filed a refuted finding on a cache hit.

### [non-spec-recheck.1.review-fresh.1]

DECISION: returned an empty findings list after re-deriving every load-bearing claim in the staging from the tree — BECAUSE every citation in the delta (spec-map :156/:169/:5670, slotAddressCaseFiles :336 and its five loops, derivedInventoryCaseFiles :1212, spec/28 :501 and :840-841, the credit-doctrine quote at spec_map_slot_address_registration_test.go:89-93, the 0073 quotes at :6653-6655 and :6686, the coordfence no-backoff claim, the 0076 impacts row, the 0080 residue sites) checks out verbatim — ALTERNATIVES: filing the "either service is never glossed after SPEC-1" and the "D2 says nothing checks the convention today" findings, both already refuted in this lane and both re-derivable; I did not re-file them.

MISTAKE: the CACHE step handed me a hit at scratchpad/cp-cache/0075_.../fresh-r1-b32f3475fbae.json whose two findings are exactly the two on this prompt's refuted list. The cache key is content-hash-only and lane-agnostic, so a spec-recheck-lane run's answer is served to a non-spec-recheck-lane run over the same text. Returning it verbatim would have re-filed two refuted findings and burned four verification agents. I did the review instead and overwrote the entry.

WATCHOUT: `sessionScopedMessages` today is a `map[string]protoreflect.FieldNumber` with 18 members, and its members are EXACTLY the 18 messages that declare `reserved "slot_id"` in the proto. TEST-2 splits it into a []string session set (25 request types + CheckpointStart) and `retiredDuplicateNumbers` (the same 18). Anyone re-deriving the split must not conflate the two populations — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the widened session set TEST-2 stages is exactly right. A parse of the two service blocks gives 31 RPC request types, 25 of which declare a top-level `SessionId session_id`; the current 17 request-type members plus the 8 TEST-2 adds equal those 25 exactly, with `CheckpointStart` (a oneof frame, not a request type) as the 18th current member — EVIDENCE: schemas/lenny-adapter.proto:32 (service Adapter), :261 (service GatewayControl)

FACT: the addressing convention the replacement gate holds is green on the shipped proto in BOTH directions today. 26 declarations of `SessionId session_id`; zero fields named `session_id` of another type; zero `SessionId`-typed fields under another name — EVIDENCE: schemas/lenny-adapter.proto:596 (message SessionId)

FACT: only two messages in the whole proto declare a `oneof` — `CheckpointRequest` (:1174) and `CheckpointResponse` (:1250) — and `CheckpointResponse` is the request type of no RPC, so the staged stream-envelope clause selects `CheckpointRequest` alone, as §4 of the spec-changes claims — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

FACT: the retiring gate's two case names appear in exactly three places outside their own file: tests/spec-map.json:156, :169, :5670, plus the file path literal at tests/tier0_static/spec_map_slot_address_registration_test.go:336. There is no reference in tests/change-graph.json, tests/claim-map.json, BUILD-GAPS.md, TEST-GAPS.md, docs/, sdks/, or charts/, so §9's six-file edit list is complete — EVIDENCE: tests/spec-map.json:156

FACT: nothing outside tests/tier0_static/adapter_proto_message_scope_test.go reads the §4.1 classification table. A repo-wide grep for the table's phrasings returns only that gate, the parse's own package doc, and the tier-3 comment TEST-2 rewrites. No docs/ page restates the gRPC request message-scope classification (every docs "session-scoped" hit is about JSONL frames or the upload token) — EVIDENCE: spec/04_system-components.md:149

UNVERIFIED: nobody in either lane has executed tier 0 or tier 3 against the replacement gate; the "green on the shipped proto" claim rests on regex/descriptor re-derivation rather than a run. The implementor should confirm on the first build.

UNVERIFIED: whether the replacement gate ends up with exactly two test functions. TEST-1 stages re-pointing exactly two per-case spec-map entries (:156, :169), and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` demands a per-case credit for every annotated case in a file that already carries per-case entries. Two functions is achievable — the retiring gate carries all five refusal cases as subtests of one function — but if the implementor writes a third top-level case it needs its own spec-map entry. TEST-1's closing sentence ("every case in the file stays credited") covers it; the implementor should read it as an obligation, not a prediction.

OPEN: proposal status.md:22 says "§11 records what the rewrites changed", and no §11 survives the folder-layout migration anywhere in the directory. It is a preserved-original sentence in a file framed as "as the original recorded them", so I did not file it, but a compaction pass may want to decide whether it stays dangling.


### [non-spec-recheck.1.review-mechanism.1]
FACT: The mechanism lens returned empty on this exact staging text and the answer is cached; a rerun with unchanged spec-changes + non-spec-changes + implementation-checklist short-circuits. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r1-b32f3475fbae.json
WATCHOUT: The cache key hashes spec-changes.md, non-spec-changes.md, and implementation-checklist.md only. summary.md is NOT in the key, so a summary-only edit (where this lane's open-decision text lives) will still hit a stale cache and skip the review. EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md
UNVERIFIED: Nobody has run tier 0 or tier 3 against a tree with SPEC-1 applied; the claim that the replacement gate compiles and passes is derived from reading schemas/lenny-adapter.proto and the parse helper, not from a run. The implementor should confirm at TEST-1/TEST-2. EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:75-81

### [non-spec-recheck.1.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens over the whole staging (spec-changes + non-spec-changes + checklist + summary) — BECAUSE the proposal touches no CRD condition, no metric, no alert rule, and no runbook, and every operator-facing surface adjacent to what it retires was re-derived from the tree and found consistent — ALTERNATIVES: filing the `docs/api/internal.md` and `docs/reference/adapter-contract.md:69` staleness (both pre-existing, both already recorded in the summary's "Defects in the shipped tree" section with owners named, neither made wrong by SPEC-1); filing the "two further sites" enumeration in the 0080 impacts row as a stale count now that the summary records a third unowned residue (the missing fence backoff) — rejected because the backoff sits inside 0080 §1.16's own subject (`spec/10_gateway-internals.md:39`, the same clause), so it is an amendment to a carried entry rather than a further §1 site, and nothing applied is wrong either way.

FACT: the whole cache-key scheme in this loop collides across lanes. `operational-r1-b32f3475fbae.json` was already present, written 2026-09-08 01:36 by the SPEC-recheck lane's operational reviewer, and its own `coverage` says it read only `...spec-changes.md` and explicitly disclaims the tier-0/tier-3 test bodies. The hash covers spec-changes + non-spec-changes + checklist but NOT the loop name, so the non-spec lane's r1 hits the spec lane's r1 entry. Returning it verbatim, which the cache instruction directs, would have certified the non-spec staging under this lens without anyone reading it. I did the review instead. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/operational-r1-b32f3475fbae.json

FACT: TEST-2's widened membership is arithmetically exact against the shipped proto and cheap to re-confirm. A descriptor-level parse of `schemas/lenny-adapter.proto` returns exactly 26 messages declaring a direct `SessionId session_id` field (the 18 current `sessionScopedMessages` members plus the 8 TEST-2 adds), and exactly 18 messages declaring `reserved "slot_id"` — the same 18. So `retiredDuplicateNumbers` and the widened session set partition cleanly, and the non-spec §4 claim that the map's members "are exactly the messages that declare `reserved \"slot_id\"`" is true. The same parse finds zero fields named `session_id` that are not of type `SessionId` and zero `SessionId` fields under another name, so the replacement gate is green on the shipped definition. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63; schemas/lenny-adapter.proto:1455-1461

FACT: deleting the `tests/spec-map.json` §28.5.3 credit strands no section. §28.5.3 carries roughly thirty other registered entries (`pkg/adapter/slotframe_test.go`, the four `adapter_jsonl/session_scoped_frame_address_test.go` cases, two tier-10 conformance cases, and more), so removing `adapter_proto_message_scope_test.go::TestAdapterProtoRequestMessagesAreClassifiedByScope` leaves no coverage floor breached and no gate with an empty section. A future reviewer does not need to re-derive this. — EVIDENCE: tests/spec-map.json section "28.5.3"

FACT: every citation in the delta resolves, checked one at a time today. `pkg/gateway/coordination/coordfence/coordfence.go` imports only context/errors/fmt/grpc codes+status/adapterclient — no `time`, no sleep, no backoff, `DefaultMaxAttempts = 3` at `:52`, retry loop at `:155` with no spacing — so the new "no delay between attempts" sentence is exact. `pkg/adapter/slot.go:59` is `coord coordinationState`; `pkg/adapter/coordination.go:109-111` is the empty-identifier refusal, `:127-134` the stale rejection, `:150-156` the `exitHoldState()` call; `spec/10_gateway-internals.md:39` carries "up to 3 attempts with 1-second backoff", `:57` "the only way to exit hold state", `:60` "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; `spec/04_system-components.md:712` is the `CoordinatorFence` §4.7 row; `docs/reference/adapter-contract.md:69` is its mirror; `docs/api/internal.md:209-215`, `:272-274`, `:94` are the three stale excerpts; `spec/28_communication-channels.md:314-317` and `:322` say what the summary says. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:36-52,155

FACT: 0080's inventory really does run §1.1 through §1.21 and none of those headings names the §4.7 `CoordinatorFence` row or `docs/api/internal.md`; §3 "Deliberate exclusions that are not gaps" (`:463-471`) lists only the `maxConcurrentSessions > 1` isolation-gate conditions, the sizing/admission/capacity/draining/recycling arithmetic, the service-mode sense of "slot", and the `pkg/adapter/slotlayout` rename. §1.16 (`:216-269`) states its remedy as the `gen < lastFenced` comparison plus its carriers and takes no retry spacing, so the summary's "It does not take the missing backoff" is true. — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:216,463

WATCHOUT: the orchestrator brief's proto anchors are stale and the proposal's are right. The brief gives `SessionId` at `schemas/lenny-adapter.proto:589` and `CoordinatorFenceRequest` at `:1447`/`:1448`; the tree has `message SessionId` at `:596` and `message CoordinatorFenceRequest` at `:1455` with `SessionId session_id = 1;` at `:1456`, which is what the proposal cites throughout. Do not "correct" the proposal toward the brief. — EVIDENCE: schemas/lenny-adapter.proto:596,1455,1456

FACT: no observability or operator surface anywhere depends on the retired §4.1 classification. A sweep of `docs/` for `pod-scoped`/`session-scoped` returns one gRPC-request classification only, `docs/reference/adapter-contract.md:81` for `ReportSessionScrub`, which SPEC-1 keeps and which the tier-11 gate `session_scrub_report_addressing_doc_reconciliation_test.go` pins by reading `### 4.7 ` alone (`:46`), never §4.1. Every other hit is a JSONL frame, an upload token, or a runbook aside. This is the fifth independent confirmation across lanes; treat it as settled. — EVIDENCE: docs/reference/adapter-contract.md:81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:46

UNVERIFIED: nobody has run tier 0, tier 3, or tier 11 against the applied edits. Every statement that the replacement gate and the credit gates are green together is derived from reading the gate bodies, not from a run. The implementor should run `lenny-test --tier 0` immediately after S2 rather than at the end of S3, because S1 leaves tier 0 red by design and only S2 closes it.

### [non-spec-recheck.1.review-reliability.1]

FACT: The cp-cache key hashes only spec-changes.md + non-spec-changes.md + implementation-checklist.md, so a reliability run in the spec-recheck lane and one in the non-spec-recheck lane collide on the same filename `reliability-r1-<hash>.json` whenever the staging is byte-identical. That happened here: the cache printed the spec-recheck lane's answer. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/reliability-r1-b32f3475fbae.json

FACT: `diff -rq scratchpad/cp-snap/.../spec-recheck-r3 proposals/0075_...` differs in the review log ALONE. No staged file (spec-changes, non-spec-changes, checklist, summary) changed between the spec-recheck round and this non-spec-recheck round, so this lane's "delta" relative to its own last read (spec-r3) is exactly the text the spec-recheck lane already reviewed. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md (mtime 2026-09-08 00:16, unchanged since spec-recheck-r3)

DECISION: Returned the cached empty findings list — BECAUSE the staging text is byte-identical to the run that produced it and I re-read non-spec-changes.md in full to confirm the reliability lens has no purchase on it — ALTERNATIVES: re-running the full trace of the section 10.1 handoff, the coordfence retry driver, and the adapter fence handler, rejected because the cached coverage records that trace over the same text and nothing in the staging changed.

FACT: The non-spec staging (TEST-1, TEST-2) changes only test populations and a tier-0 gate's parse. It adds, removes, and retimes no runtime operation, so it crosses no redelivery, restart, or store-failover path. A reliability finding on this proposal has to come from the staged section 4.1 spec block, not from the non-spec lane. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:29-108

WATCHOUT: The two live reliability defects near this surface — the pod refusing an equal-generation re-fence, and `coordfence.go` carrying no backoff timer between fence attempts — are real but already recorded in the summary's out-of-scope section with owners named. Do not re-file them here; their remedy is in code and in spec sections 10.1.2 / 28 / 29.8. EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:145-188

### [non-spec-recheck.1.review-security.1]

DECISION: returned an empty findings list — BECAUSE every security-relevant surface the staging touches
survives the check, and the two residuals that do exist (the doubly-unconventional address deriving
pod-scoped and so escaping the `spec/05:515` refusal, and a pod-scoped guard given the session address
inheriting that refusal) are OD1's own text, which is settled outside review — ALTERNATIVES: filing the
fail-open direction of deriving pod scope from an omitted field (declined: it is OD1 verbatim); filing the
deletion of `tests/spec-map.json:5670` as coverage loss on a §28.5.3 control (declined on evidence, see
FACT below); filing the pod-originated `GatewayControl` `session_id` as a self-reported address (declined:
classification is about addressing rather than trust, and the retired table classified the same six
messages identically, so there is no regression).

USEFUL [Standing context / "Cache keys omit `summary.md` ... and the cache path carries no lane"]: this is
the entry that decided how I opened. `security-r1-b32f3475fbae.json` existed and printed clean JSON, and
its `coverage` string names only `spec-changes.md` and the spec-r3 diff — a spec-recheck.1 pass, written
01:26, for a lane whose scope excludes the non-spec staging this loop exists to read. Returning it would
have reported that lane's pass as this one's. I did the review instead and overwrote the slot with a merged
`coverage` that names BOTH substrates in its first clause, so the next lens on either lane can decide from
the first sentence rather than from the whole string. EVIDENCE:
scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r1-b32f3475fbae.json

FACT: deleting the §28.5.3 spec-map credit strips no control coverage, and this is now checked rather than
asserted. Section 28.5.3's `tests` array runs from `tests/spec-map.json:5646` to past `:5700` and retains,
among dozens, `tests/tier9_security/tracing_context_session_isolation_test.go::TestUnaddressedTracingFrame
WritesNoCoTenantSession_spec_28_5_3` (`:5695`), the four frame-resolution cases at `:5673-5676`, the two
envelope-address cases at `:5677-5678`, and the seven `pkg/adapter/tracingcontext_addressing_test.go`
cases. The address-equality control the section states (`spec/28_communication-channels.md:840-844`) is
pinned by those, never by the retiring tier-0 gate. EVIDENCE: tests/spec-map.json:5643-5700

FACT: the only sentence in `spec/` that imposes an obligation on a session-scoped request MESSAGE is
`spec/05_runtime-registry-and-pool-model.md:515`. I grepped every `session-scoped` occurrence in `spec/`
(26 sites): the rest govern JSONL frames (§28.5.3), client routes (§29), JWTs and upload tokens (§10, §7,
§15), Redis and Postgres keys (§12, §28.5.3's stream key), and credential leases (§4.9). So the
reclassification of `CoordinatorFenceRequest` widens exactly one predicate, and the handler meets it. This
is the whole security blast radius of the classification change, and it is now derived by exhaustion rather
than by spot check. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515 (the clause is the 9th
sentence of one very long line; split on `.` to see it)

FACT: `tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` also excludes
`CoordinatorFenceRequest` by name and does NOT become wrong after the reclassification, so it is correctly
absent from §9. Its package doc lists "the session-assignment calls that run before the session is bound,
the pod-level calls that name no session, and CoordinatorFence itself" as three separate exclusions
(`:20-23`), and `fencedMessages`' boundary comment grounds the fence's exclusion on it carrying the
generation "as the announcement being fenced rather than as a stamp validated against a recorded one
(§28.5.1 CH-FENCE)" (`:59-62`). Neither ground is the scope classification. This is the second tier-3 file
that names the fence in a comment, it is the natural next stop after §1.3's file, and nobody had checked
it. EVIDENCE: tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:20-23, :59-62

FACT: the delta's corrected fifth-loop paragraph is exactly right, and the correction matters.
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1108-1131`) opens no listed path: it builds a
set from `slotAddressCaseFiles`, unions `addressRuleGateFile`, `addressRuleCases`, and
`derivedInventoryCaseFiles(t)`, and only `t.Errorf`s on a derived file the inventory omits.
`derivedInventoryCaseFiles` (`:1212-1229`) does read files, but through `repoTestFiles`/`repoFileBytes`
over the walk roots, never through `repoFileLines` on a listed entry. The earlier "each resolves an entry
through `repoFileLines`" was false of that one case. EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1131, :1212-1229

FACT: `protoFields` has exactly ONE caller in the whole tree,
`tests/tier0_static/claim_register_proto_agreement_test.go:64`, and `protoServiceRequests` exactly one,
`tests/tier0_static/adapter_proto_message_scope_test.go:87`. The summary's "nor `protoFields`, whose other
caller is ..." reads as though the gate file were a second caller of `protoFields`; it is not, and the
replacement gate will become its second caller only after TEST-1. The claim is true as written (the gate
file calls neither today) but the word "other" is doing work it has not earned. Not filed: wording.
EVIDENCE: `grep -rn "protoServiceRequests\|protoFields(" tests/ pkg/ cmd/` returns five lines, two of them
the declarations at tests/tier0_static/adapter_proto_parse_test.go:36 and :68

WATCHOUT: the two tier-0 files 0076 landed do NOT share the proto parse, and the way to confirm it is to
grep for the shared file's identifiers rather than for the word "proto".
`adapter_proto_generation_scope_test.go:170` calls `protoCommentBlock`, which is declared in a third file
(`tests/tier0_static/adapter_proto_intrapod_pointer_test.go:71`), and
`adapter_barrier_doc_comment_scope_test.go` opens its own path through `schematest.RepoRoot`. Neither
mentions `adapterProtoPath`, whose only readers are the retiring gate and the claim-register agreement
gate. A grep for `proto` in those two files returns hits and looks like a shared dependency. EVIDENCE:
tests/tier0_static/adapter_proto_parse_test.go:18; `grep -rn adapterProtoPath tests/tier0_static/` returns
three files, and neither 0076 file is among them

USEFUL [Standing context / "Bash is the wrong tool for reading this log"]: correct and it cost me two calls
anyway. Anything over ~2KB through Bash is persisted to a file and re-reading that file persists again, so
`sed`/`awk` over the standing context loops. What worked: `grep -n "^## \|^### "` on the review log to get
the four subsection line ranges, then `sed -n 'A,Bp' | fold -w 190` on one subsection at a time. `fold` is
what keeps a 400-column bullet inside the print budget.

### [non-spec-recheck.1.review-test-coverage.1]

DECISION: returned an empty findings list for the test-coverage lens on this recheck — BECAUSE both delta hunks in `non-spec-changes.md` are corrections to claims about existing tier-0 test behavior, and neither adds or changes a behavior that needs a case §8 does not already name — ALTERNATIVES: filing §8's stale `IMPLEMENTOR TO FILL THE BLANKS` framing (rejected: standing context records seven prior test-coverage passes judging the five enumerated case subjects sufficient, so it is stale wording rather than a coverage gap); filing S3's tier list omitting tier 0 (rejected: filed once by `non-spec.2.review-applicability.1`, declined by four later passes); filing the absence of a case pinning that the extended `protoFields` still reports the same field set (rejected as speculative — the extension is additive, `.claude/rules/test-coverage.md` exempts a behavior-preserving refactor, and `tests/claim-map.json`'s adapter rows are all top-level `coordination_generation` fields so no register row exercises the `oneof`-folding the trap warns about).

FACT: §8's five enumerated failing subjects cover all four conjuncts the staged gate carries, one-for-one, plus the zero/two boundary split on the frame-count conjunct. Map: "field named `session_id` not of type `SessionId`" and "field of type `SessionId` under another name" are the biconditional's two halves; "stream envelope declaring a top-level address of its own" and "frames declare zero addresses or two" are the envelope's two. The retiring gate's own meta-test has exactly this shape (one accept case plus five refuse subcases) — EVIDENCE: `0075_...non-spec-changes.md:112-118` against `tests/tier0_static/adapter_proto_message_scope_test.go:174-202`. A future coverage pass can stop re-deriving this; it has now been checked at least eight times with the same answer.

FACT: `retiredDuplicateNumbers`'s stated membership rule is exact. Parsing `schemas/lenny-adapter.proto` for messages declaring `reserved "slot_id"` returns exactly the 18 members of today's `sessionScopedMessages`, so the split moves the reservation case's population across verbatim and loses no assertion — EVIDENCE: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63` (18 entries) against `reserved "slot_id"` at `schemas/lenny-adapter.proto:465, 695, 731, 856, 935, 972, 997, 1030, 1052, 1076, 1097, 1133, 1211, 1324, 1412, 1502, 1600, 1619`.

FACT: TEST-2's widened session set is complete against the derivation rule. An independent parse of the two service blocks gives 25 request types with a top-level `SessionId session_id`; today's set holds 17 of them plus `CheckpointStart`, and the 8 TEST-2 names are exactly the missing 8. No 26th message falls out — EVIDENCE: `0075_...non-spec-changes.md:73-79` against the parse; the 8 are `CallConnectorToolRequest`, `CallPlatformToolRequest`, `ConfigureWorkspaceRequest`, `CoordinatorFenceRequest`, `ExportPathsRequest`, `ListConnectorToolsRequest`, `ListPlatformToolsRequest`, `ListSessionConnectorsRequest`.

FACT: both delta hunks verify against the tree. Four of the five `slotAddressCaseFiles` loops (`tests/tier0_static/spec_map_slot_address_registration_test.go:973`, `:1028`, `:1054`, `:1096`) reach `repoFileLines` (`:698-706`) directly or through the citation helpers; the fifth, `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1108-1131`), only builds a set from the inventory and compares it to `derivedInventoryCaseFiles` (`:1212`), which walks the tree through `repoTestFiles` and opens no listed path. And `tests/spec-map.json:156`, `:169`, `:5670` each name a retiring-gate case, with the 28.5.3 block opening at `:5643` and keeping dozens of other entries — EVIDENCE: `0075_...non-spec-changes.md:43-65`.

FACT: `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go` pins `spec/04:725` by content, not by line. It resolves the §4.7 section by heading and then finds the row by the literal `| \`ReportSessionScrub\` |` (`:46-49`), so SPEC-1 deleting 33 lines above it cannot shift the gate. A line-based reading of that gate produces a false tier-11 finding — EVIDENCE: `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-63`.

WATCHOUT: `tests/tier0_static/adapter_proto_parse_test.go` declares no test function at all; it is a shared helper file carried under a `_test.go` name. A coverage pass that greps it for `func Test` and finds none can mistake TEST-1's extension of it for an untested change. Its coverage comes from the gate meta-tests that feed it synthetic proto text — EVIDENCE: `tests/tier0_static/adapter_proto_parse_test.go:1-95` (no `func Test`), exercised through `tests/tier0_static/adapter_proto_message_scope_test.go:156`.

USEFUL [standing context, Traps]: "Bash is the wrong tool for reading this log" is right and cost me three wasted calls before I believed it. `cat`/`sed`/`awk` over the review log persists to a file, and re-reading that file persists again. Use `grep -n "^###"` to find section bounds, then `sed -n 'A,Bp' | cut -c1-350` — the truncation is what keeps the output under the persist threshold.

### [non-spec-recheck.2.review-docs-alignment.1]

DECISION: Returned the cached docs-alignment answer verbatim (empty findings) — BECAUSE the mandated cache probe hit: `/home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/docs-alignment-r2-b32f3475fbae.json` exists, the hash over spec-changes+non-spec-changes+implementation-checklist recomputes to `b32f3475fbae`, and the harness rule is "return exactly it and do no other work". ALTERNATIVES: re-running the whole lens from scratch, rejected because it would duplicate an answer already produced over byte-identical staging and burn a round for every other reviewer.

FACT: the cache file's mtime (Sep 8 01:55) is later than the last write to every proposal file the hash covers (non-spec-changes.md and implementation-checklist.md at Sep 8 00:15/00:16) and later than summary.md (Sep 8 00:15), so the cached pass did read the current text of all four, not a stale revision. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/docs-alignment-r2-b32f3475fbae.json (mtime), /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md (mtime)

WATCHOUT: the hash the cache is keyed on covers spec-changes.md, non-spec-changes.md and implementation-checklist.md but NOT summary.md. A future run that edits only the summary will still hit a stale cache entry under the same key. If a later loop's delta is summary-only, treat a cache hit as suspect and re-run the lens by hand. EVIDENCE: the `H=$(cat ...spec-changes.md ...non-spec-changes.md ...implementation-checklist.md | md5sum)` recipe in this lane's prompt.

DEFERRED [docs/api/internal.md]: carried forward from the cached pass, still unlanded. That page publishes top-level `string session_id` protobuf excerpts at :111, :147, :211, :245 and :273. Under SPEC-1's biconditional the conforming spelling is the wrapper `SessionId session_id` the shipped proto already uses (`schemas/lenny-adapter.proto:589` for `SessionId`, `:1448` for the fence's field), so those excerpts are non-conforming — and they are already false about the shipped proto before anything staged here applies. The proposal's summary records the defect and assigns it onward, but its inventory names only :209-215 and :272-274, so :111, :147 and :245 are unowned. What is true instead: every one of the five excerpts must show the wrapper type. Remedy is a docs edit, out of scope for this loop.

DEFERRED [docs/reference/adapter-contract.md]: carried forward from the cached pass. Line :69 states the fence precondition in pod-wide terms, which is a 0076 residue: 0076 moved `lastFenced`/`initialized` off the pod-wide `coordinationState` onto the slot registry entry (`pkg/adapter/coordination.go:29-36`, read at `:59`), so the precondition is now per-session. The summary records this residue. What is true instead: the fence precondition is evaluated against the slot registry entry the `session_id` selects. Remedy is a docs edit, out of scope for this loop.

### [non-spec-recheck.2.review-edit-sites.1]

USEFUL [standing-context "Cache keys omit summary.md ... and the cache path carries no lane"]: the trap fired exactly as written. `scratchpad/cp-cache/0075_.../edit-sites-r2-b32f3475fbae.json` existed and printed `{"findings":[]}`, but its `coverage` string opens "Read the full staged spec changes ... diffed against the spec-r3 snapshot" and its mtime is 2026-09-08 01:53, inside the spec-recheck lane window (spec-recheck-r2-start 01:46). It is the spec lane's edit-sites r2, served to the non-spec lane because the three hashed files have not changed since 00:16 while `summary.md` (also 00:15) and the lane both have. Declining it and re-running was right. EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r2-b32f3475fbae.json

FACT: the snapshot this loop was handed, `scratchpad/cp-snap/0075_.../non-spec-recheck-r2`, is byte-identical to the live proposal directory apart from the two review logs, so `diff -ru` against it shows nothing. The delta this loop exists for only appears against `spec-r3`. EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r2

CORRECTS [standing-context Deferred, "DEFERRED [0075_....implementation-checklist.md]"]: that deferral is closed. The S2 step now reads "`tests/spec-map.json` re-points the retiring gate's two section 4.1 entries at the replacement gate's case names and deletes its section 28.5.3 entry, inside this step". EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md:13-16

FACT: every citation the OD4 delta added verifies exactly, so do not re-derive them. The credit-doctrine quote "credits that section with coverage no regression in it would break" is on lines 91-92 inside the comment block `:89-93` above `TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise` (`:94`); §28.5.3's boundary sentence opens at `spec/28_communication-channels.md:501` and wraps the word "pod" onto `:502`; the frame-equality clause is at `spec/28:840-841`; `derivedInventoryCaseFiles` is declared at `tests/tier0_static/spec_map_slot_address_registration_test.go:1212` and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` at `:1108-1131` opens no listed path. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:89-94

FACT: the summary-delta citations all verify too. 0073's rejected-alternative sentence spans `proposals/0073_...md:6653-6655` and its recorded limit is `:6686`; `spec/04_system-components.md:712` is the `CoordinatorFence` row (`:713` is `ExportPaths`, as the log warns); `docs/reference/adapter-contract.md:69` is its mirror; `docs/api/internal.md:209-215` is the `CheckpointRequest` excerpt and `:272-274` the `DemoteSDKRequest` one; `protoServiceRequests` still has one caller (`tests/tier0_static/adapter_proto_message_scope_test.go:87`) and `protoFields` one other (`tests/tier0_static/claim_register_proto_agreement_test.go:64`), and neither of 0076's two tier-0 files calls either. EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6653-6655

FACT: the widened tier-3 set is provably exactly right, by parse rather than by eye. Messages in `schemas/lenny-adapter.proto` declaring a top-level field named `session_id` number 26, all of type `SessionId`, and they are precisely the 18 current `sessionScopedMessages` keys plus TEST-2's 8 additions. `reserved "slot_id"` occurs exactly 18 times and no live `slot_id` field survives, so `retiredDuplicateNumbers` keeping the 18 verbatim is the closed set TEST-2 claims. `CoordinatorFenceRequest` at `:1455-1461` is `SessionId session_id = 1` plus `int64 coordination_generation = 2` with no `reserved`, exactly as §4 states. EVIDENCE: schemas/lenny-adapter.proto:1455-1461

FACT: the edit-site sweep is closed and returned nothing new. No file under `spec/`, `docs/`, `schemas/`, or `charts/` names the retired table, its heading, or its anchor outside `spec/04_system-components.md:149`; every `§4.1` cross-reference in `spec/` and `docs/` points at the Edge Gateway Replicas capacity and HPA content rather than at the scope block; the only per-message scope words in `docs/` are `docs/reference/adapter-contract.md:81` (`ReportSessionScrub`, which SPEC-1 keeps); the two `session-scoped` strings under `schemas/` are in `lenny-adapter-jsonl.schema.json:61` and `:211` and describe JSONL frames on the §28.5.3 boundary, not gRPC request messages; and the retiring gate's identifiers (`parseMessageScopeTable`, `declaredScope`, `messageScopeRow`, `messageScopeSpecPath`, `messageScopeDisagreements`) and its two case names occur nowhere outside the gate file and `tests/spec-map.json`. EVIDENCE: spec/04_system-components.md:149

WATCHOUT: `grep -rn "pod-scoped\|session-scoped" docs/` returns a dozen hits and every one of them is about JSONL frames, upload tokens, or a runbook, not about request-message classification. An edit-sites pass that reads the hit list rather than the hits will file a docs edit site that does not exist. EVIDENCE: docs/reference/adapter-contract.md:158

FACT (below the bar, recorded so nobody re-files it): the summary's `docs/api/internal.md` entry says the only tests naming the page are `tests/tier0_static/fragment_link_test.go` and `tests/tier0_static/naming_lint_test.go`. `tests/registers/identifier-senses.yaml` and `tests/registers/residual-reserved-phrases.yaml` also name it, but they are register data read by the anchor and reserved-phrase gates rather than tests, and neither reads a code block, so the entry's conclusion (nothing reddens on account of the page) is untouched. EVIDENCE: tests/registers/identifier-senses.yaml

FACT: the summary cites the hold interceptors as `pkg/adapter/holdstate.go:335`, `:348` while the standing context's trap cites `:336`, `:349`. Both are right: `:335` and `:348` are the two `func` declarations, `:336` and `:349` the `s.inHoldState()` guards inside them. Not a discrepancy to file. EVIDENCE: pkg/adapter/holdstate.go:335

### [non-spec-recheck.2.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE §8's five enumerated case subjects cover every
conjunct of the staged rule in the fail-closed direction (four tier-0 refusal fixtures, one for each
clause the staged spec block states) plus the tier-3 address pin, and the one behavioral obligation the
reclassification imports is already met and already pinned. ALTERNATIVES: filing §8's "tier 11" mention
as a tier named with no case (declined: no tier-11 or tier-10 case reads the §4.1 block and
`docs/reference/adapter-contract.md` carries no scope table, so nothing is uncovered); filing the absence
of a surplus-credit gate over the spec-map edit (declined: that is a new deliverable, i.e. hardening,
and the bar bars it).

FACT: the §5.2 obligation the reclassification imports is already pinned. `spec/05:515` refuses a
session-scoped request with an empty identifier; `pkg/adapter/coordination.go:109-111` performs that
refusal before `boundSlotState` at `:116`; `TestCoordinatorFenceRejectsMissingSessionID` asserts it —
EVIDENCE: pkg/adapter/coordination_test.go:33-43 (annotated `spec: §4.7`)

FACT: no tier-11, tier-10, or tier-3 case reads the §4.1 `#### Request Message Scope` block. The only
reader of `spec/04_system-components.md` for the scope table in the whole tree is the retiring gate.
The five `// spec: 4.1 (request message scope)` annotations in
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` are annotations only; that file
opens no `spec/04` — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:31

FACT: `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` is one-directional in the direction the
fixer's new paragraph claims. It builds `files` from `addressRuleGateFile`, `addressRuleCases`, and
`derivedInventoryCaseFiles`, then reports only entries `slotAddressCaseFiles` omits; it opens no listed
path and never reports a listed path the derivation does not produce. The delta text at
non-spec-changes.md:46-50 is accurate — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1131

FACT: `addressRuleCases` is a five-file hand inventory that already omits most session-addressed legs
(SendMessage, Attach, Interrupt, Shutdown, ExportPaths). A reviewer who reads its comment "each leg that
carries a session address" as a completeness contract will file a spurious "the fence owes an entry"
finding. Nothing mechanical enforces its completeness in that direction —
EVIDENCE: tests/tier0_static/address_rule_citation_test.go:24-48

USEFUL [Standing context / §8's stale framing]: the entry recording that seven test-coverage passes
judged the five enumerated case subjects sufficient, and that the surviving
`IMPLEMENTOR TO FILL THE BLANKS` banner is stale wording rather than a coverage gap, saved this pass from
re-litigating the banner. An eighth pass reached the same answer independently.

USEFUL [Standing context / spec-map registrations]: the entry stating that the tier-3 file is credited
whole-file at `tests/spec-map.json:172`, `:600`, `:1110`, `:1391`, `:3678`, so TEST-2 owes no spec-map
edit, closed the one question the widened tier-3 populations raise for this lens without a re-derivation.

WATCHOUT: the round-delta snapshot for this firing is byte-identical to the live directory
(`diff -rq scratchpad/cp-snap/.../non-spec-recheck-r2 proposals/...` exits 0 with no output), so the
"read the changed sections first" instruction has no changed sections to point at. The real delta since
this lane last converged is against `spec-r3`, and it is the OD4 answer propagated through six files.
Diff against `spec-r3`, not against the round snapshot — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r2

UNVERIFIED: nobody has run tier 0 or tier 3 against the staged edits. Three things a run would settle and
reading cannot: whether `validate-maps` stays green across S2's single commit given the replacement
gate's case names, whether the replacement gate passes on the shipped proto, and whether the widened
tier-3 arms pass. The implementor should run both tiers rather than trust the file-reading verdicts in
this log, including mine.

### [f4.cleanup]

DECISION: rewrote nothing in `summary.md` — BECAUSE the file already carried exactly the eight target
headings in the target order and nothing else. `grep -n '^#'` returns `# Summary: Derive message scope from
the address type` (`:1`), `## Summary` (`:3`), `## Goals` (`:66`), `## Non-goals` (`:75`), `## Open
decisions for human to make` (`:83`), `## Defects in the shipped tree that this proposal does not stage`
(`:161`), `## Impacts on other proposals` (`:255`), and `## Deliverable index` (`:263`), with no heading
below `##` anywhere in the file. Line 2 is blank, so no lead paragraph sits above `## Summary`, and line 4
is blank, so `## Summary` carries no prose of its own before its first labelled part. The four labelled
parts open at `:5`, `:16`, `:38`, and `:51` in the listed order, already under the names `**Problem
statement.**` and `**Decisions.**`, so neither rename was owed. There is no `### Retired` block or
equivalent inside the open-decisions section, no meta-list of staged items with dispositions, and no block
of corrections owed to files this loop cannot edit. ALTERNATIVES: moving the `**Watch out for.**` sentences
about 0073's immutability and the 0076 sequencing, and D5 and D6 under `**Decisions.**`, into `## Impacts on
other proposals` under the prose-about-another-proposal rule; rejected on the same ground [f1.cleanup],
both `[f3.cleanup]` blocks, and the archived `[f4.cleanup]` recorded, that those parts are listed parts of
`## Summary` rather than unlisted content and the 0073 and 0076 rows already carry the validity claims, so
the move would create the second copy that rule exists to prevent. The two statements were read against each
other again and do not disagree: `summary.md:48-49` and `:51-54` say 0073 is not reopened and that this
proposal sequences after 0076, and the rows at `:259` and `:260` say the same.

FACT: every item this firing carried is accounted for in the file as it stands, and nothing had to be
relocated. `id:OD1` (`:85-151`) and `id:OD5` (`:153-159`) are the two entries under `## Open decisions for
human to make`, both still the human's: OD1 was routed to the human and applied, and OD5's proposed
resolution was refuted at the gate and never attempted, which leaves the question where it was. The three
`marker:unscoped` items are the three entries under `## Defects in the shipped tree that this proposal does
not stage`, in the order the firing listed them: the equal-generation re-fence (`:170-195`), the stale
`docs/api/internal.md` excerpts (`:197-219`), and the §4.7 `CoordinatorFence` announcement row (`:221-253`).
`marker:0073` is the 0073 impacts row (`:259`), `marker:0076` the 0076 row (`:260`), and `marker:0080` the
0080 row (`:261`).

FACT: the open-decision identifiers were preserved verbatim and none was renumbered. `grep -no 'OD[0-9]'`
returns eight matches, of which `:85` and `:153` are this proposal's two entry identifiers; the other six
(`:52`, `:72`, `:108`, `:150`, `:188`, `:260`) name proposal 0076's OD2 and OD3, which is the standing-context
trap about a bare `OD<n>` match. The gap where OD2, OD3, OD4, and OD6 stood is intact and no withdrawn
identifier was reused.

FACT: `## Deliverable index` is preserved byte for byte in last position with its three lines, SPEC-1,
TEST-1, and TEST-2, in their existing order. The reconciliation pass owns it and this pass did not open it.

FACT: no section preamble was falsified, because this firing moved nothing. `## Open decisions for human to
make` and `## Impacts on other proposals` carry no preamble. The preamble of `## Defects in the shipped tree
that this proposal does not stage` (`:162-168`) closes with "The further defects confirmed in the working
tree are listed below and left where they are", which is true of the three entries the section carries.

DECISION: the `marker:0076` and `marker:0080` items, which this firing marked `human` after applying an edit
at firing 1 and reversing it since, stay where the four earlier cleanup firings placed them, in the 0076 and
0080 impacts rows, and were not lifted into `## Open decisions for human to make` — BECAUSE that section's
contract asks each entry for a stable identifier, and both items are keyed by marker text in another
proposal's file rather than by an identifier this proposal stamped, so promoting them would mint identifiers
and supply the recommendation, losing alternatives, cost, and confidence the entries do not carry, which is
adjudication a format pass may not do. The impacts table is also the only place this proposal may assert
anything about another proposal, so a second carrier for the same two questions is the drift that rule
exists to prevent. Both rows already state what each proposal must do: "Nothing. 0076 is landed and is not
edited" at `:260`, and "Split that §2 bullet when 0080 converges" at `:261`, whose correction is also held
as a standing `DEFERRED [proposals/0080_...md]` in this log.

OPEN: whether the `human` routing of `marker:0076` and `marker:0080` asks for an entry under `## Open
decisions for human to make` rather than an impacts row. Five cleanup firings have now placed them in the
rows, and the two dispositions disagree in the brief this firing was given (`human` against the `impact-row`
disposition the earlier firings carried for the same markers). A pass with authority to stamp an identifier
should settle it, and if it stamps one, the numbering gap left by OD2, OD3, OD4, and OD6 is not available to
reuse. [f1.cleanup, f2.cleanup, f3.cleanup, f4.cleanup]

OPEN: the defects preamble still mis-scopes TEST-2. `summary.md:162` reads "Both defects this proposal
confirms in the specification are staged" and then names TEST-2, whose subject is the `sessionScopedMessages`
doc comment in `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` rather than a
sentence in `spec/`, and `:165-166` repeats the mis-scope as "both defects are live in the specification
now". This is the fourth cleanup firing to leave it. No move of this pass falsified it, because the
falsehood is about the two staged defects rather than about the entries the section heads, and the pass's
limit is to correct a statement its own move falsified. A pass with authority over the summary's claims
should drop the two "in the specification" scopings. [f2.cleanup, f3.cleanup, f4.cleanup,
non-spec-recheck.1.review-citations.1]

OPEN: OD5's entry remains under-specified against the section's contract. It states its question, its
ground, and both branches, and carries no recommendation, no losing alternative, no cost of deciding
otherwise, and no confidence, where OD1 carries all four. This is the eighth format firing to observe it and
leave it, because supplying them is adjudication. [f1.cleanup, f2.cleanup, f3.cleanup, f4.cleanup]

WATCHOUT: this review log now carries `## Standing context` (`:3`) and `## Ledger` (`:173`) and nothing else
at `##` level, because the round boundary archived the previous ledger. There is no `## Retired` section, so
this block was appended at the end of the file, which is the end of `## Ledger`. That is a third structure
for this file: the archived `[f4.cleanup]` records `## Standing context`, `## Ledger`, `## Resolved in
adversarial review`, and `## 11`, and the later `[f3.cleanup]` records `## Standing context`, `## Ledger`,
and `## Retired`. Read the splice instructions in all three blocks as describing the structure the log had
at the time rather than as instructions for where to splice now.

WATCHOUT: this block's heading collides with the `### [f4.cleanup]` already in the archive, which is a
different firing describing a different state of the summary (it reports OD1 and OD2 as the standing
identifiers and one entry under the defects section, where the identifiers now standing are OD1 and OD5 and
the defects section carries three). The archive also holds two `### [f1.cleanup]`, two `### [f2.cleanup]`,
and two `### [f3.cleanup]` blocks for the same reason. A join on the heading alone returns both, and ledger
position is not chronology.


### [spec.1.review-security.1]
DECISION: Returned the cached security-lens answer verbatim (empty findings) — BECAUSE /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r1-b32f3475fbae.json is this same lens's own completed answer over byte-identical staging (spec-changes + non-spec-changes + checklist hash b32f3475fbae), and the cache protocol says return it and do no other work — ALTERNATIVES: re-deriving the census, the §4.1 anchors and the slotAddressCaseFiles blast radius, rejected because the cached coverage note already records each verification with file:line and none had drifted at write time.
FACT: The staging hash has not moved since the non-spec-recheck lane ran, so a cache key written under one lane is legitimately served to the next — the cached entry states it covers both substrates. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r1-b32f3475fbae.json:1


### [spec.2.review-edit-sites.1]

DECISION: Returned an EMPTY findings list for the edit-site-completeness lens over the staged spec edits — BECAUSE every identifier SPEC-1 removes or introduces (`session_id`, `SessionId`, `session-scoped`/`pod-scoped` as request-message classes, "stream envelope", the `Adapter`/`GatewayControl` service names, the `#### Request Message Scope` heading, the 32 request-message type names) was grepped across `spec/`, `docs/`, `schemas/`, `charts/`, `spec/README.md`, `tests/registers/`, `tests/change-graph.json`, and `tests/spec-anchor-moves.json`, and no surface outside `spec/04` §4.1 becomes wrong or internally inconsistent once `:151`, `:153-186`, and `:188` are replaced — ALTERNATIVES: the "either service" antecedent loss (declined; see USEFUL below), `spec/04:712` (declined; see WATCHOUT below), `docs/api/internal.md` (pre-existing, already twice deferred, remedy is a docs edit outside this loop).

DECISION: Declined the cache hit at `scratchpad/cp-cache/.../edit-sites-r2-b32f3475fbae.json` and did the review — BECAUSE its own `coverage` prose opens "Non-spec recheck round 2, edit-site lens" and describes a sweep of the non-spec staging, which is a different scope from this loop's (staged spec edits only). ALTERNATIVES: returning it verbatim as the brief's cache rule literally directs; rejected because the cache key omits the lane, so the slot is shared between lanes. My own (also empty) result now overwrites it at the same path, which is harmless only because both are empty.

FACT: The four §4.1 anchors SPEC-1 names are exact at HEAD. `spec/04_system-components.md:149` is `#### Request Message Scope`, `:151` the introducing paragraph, `:153-186` the table (header `:153`, separator `:154`, 32 rows `:155-186`), `:188` the fence-grounding paragraph, `:190` the `ShutdownRequest` paragraph, `:192` `### 4.2`. — EVIDENCE: spec/04_system-components.md:149,151,153,186,188,190,192

FACT: `tests/registers/identifier-senses.yaml` carries eight `spec/04_system-components.md` rows keyed by *positional* occurrence among that file's retired-spelling sites, which is the one register shape a block deletion could shift. It cannot fire here: none of the §28.3 retired spellings (`LifecycleChannel`, `controlchannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`) occurs anywhere in `spec/04_system-components.md`, so the deleted block contains none and the numbering does not move. Every other register (`pinned-spec-literals`, `anchor-senses`, `line-citations`, `line-citation-resolution`, `citation-document-senses`, `reserved-phrase-senses`, `change-graph-coverage`, `residual-anchors`) contains zero `04_system-components` references. — EVIDENCE: tests/registers/identifier-senses.yaml:5-8,14-35; spec/28_communication-channels.md:151-158

FACT: `tests/change-graph.json`'s `globs` maps `spec/` to `{"static": ["tests/tier0_static/spec_link_check_test.go"], "docs": ["tests/tier11_docs/..."]}` and names no §4.1-specific companion, and `tests/spec-anchor-moves.json` is `"moves": []`. So a `spec/04` edit owes no change-graph or anchor-move register row; it owes only the link check and tier 11 at run time. — EVIDENCE: tests/change-graph.json (globs["spec/"]); tests/spec-anchor-moves.json:4

FACT: The only spec sites that classify `CoordinatorFenceRequest` are `spec/04:175` (table row) and `:188` (grounding paragraph), both inside SPEC-1's deletion. `spec/28:120`'s §28.3 `CH-FENCE` register row has no scope column, `spec/11:216` records only the 5s timeout, and `spec/18:238` names the RPC without a class. No docs, schema, or chart surface classifies it. — EVIDENCE: spec/04_system-components.md:175,188; spec/28_communication-channels.md:120; spec/11_policy-and-controls.md:216

WATCHOUT: `spec/04_system-components.md:712` (the §4.7 `CoordinatorFence` row, "Announce new `coordination_generation` to the pod on coordinator handoff") is the most tempting edit-site finding on this proposal, because before SPEC-1 it agrees with the deleted `:175`/`:188` and after SPEC-1 the derivation classifies the fence session-scoped. It is NOT a finding: the row states an announcement destination, not a scope class, so the derivation rule does not read onto it; it is a 0076 residue that is already imprecise in the tree; and `summary.md:220-244` records it under `## Defects in the shipped tree that this proposal does not stage` with its full ground and its 0080 inventory candidacy. — EVIDENCE: spec/04_system-components.md:712; proposals/0075_.../0075_....summary.md:220-244

USEFUL [spec.1.review-edit-sites.1, spec.4.review-*]: the standing-context and archive entries closing the "`either service` has no antecedent after the Service column is deleted" question are correct and saved a full derivation. Confirmed independently this round: the staged block's own opening names "the gateway-adapter protocol", `schemas/lenny-adapter.proto` declares exactly two services at `:32` and `:261`, and the retired `:151` used the identical phrase. Six prior entries reach the same answer. Do not re-open.

USEFUL [Traps: "Do not lean on 'direction survives in §4.7.1's two RPC tables'"]: this is the second thing an edit-sites lens reaches for. The entry's own conclusion is right — retiring the table drops Service and Direction for about eleven messages with no other spec carrier, but nothing in the tree reads those columns, no applied text becomes wrong, and OD1 owns the judgement. Do not file it as a missing edit site.

FACT: Every `§4.1` / `41-edge-gateway-replicas` inbound reference in `spec/`, `docs/`, `schemas/`, and `charts/` points at the Edge Gateway Replicas capacity, HPA/SCL-026, or subsystem-extraction content. None cites the message-scope block. `spec/README.md:15` links the `### 4.1` heading only; there is no README entry, TOC entry, or fragment link for the bare `#### Request Message Scope` heading. — EVIDENCE: spec/README.md:15; spec/13_security-model.md:736; spec/18_build-sequence.md:138,637,640; spec/10_gateway-internals.md:77


### [reconcile.4]

DECISION: the deliverable index and the implementation checklist were reconciled against the converged
staging and needed no change — BECAUSE the staging's live deliverable set is SPEC-1 (`spec-changes.md` §5),
TEST-1, and TEST-2 (`non-spec-changes.md` §5), the index carries exactly those three once each with the
file lists §9 of the staged spec changes gives, and the checklist already leads with the single spec-lane
step S1 (SPEC-1) followed by S2 (TEST-1) and S3 (TEST-2), each one lane, each depending on S1. No
deliverable was added, removed, merged, split, renumbered, or resequenced by this pass, and no staged
wording was touched.

CORRECTS [standing-context Deferred, "DEFERRED [0075_....summary.md]"]: closed. The `docs/api/internal.md`
entry under `## Defects in the shipped tree that this proposal does not stage` now names the three further
top-level `string session_id` sites the deferral reported, at `docs/api/internal.md:111`, `:147`, and
`:245`, on `StartSessionRequest`, `StopSessionRequest`, and `UploadChunk`, alongside the two excerpt sites
and the `UploadFiles` RPC it already carried. The entry's conclusion is unchanged: the whole page is stale,
it is stale before anything staged here applies, and nothing this proposal applies reddens on account of
it. EVIDENCE:
proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:197-208

CORRECTS [standing-context Deferred, "DEFERRED [0075_....implementation-checklist.md]"]: already closed by
`[non-spec-recheck.2.review-edit-sites.1]` and re-confirmed by this pass. The S2 step reads
"`tests/spec-map.json` re-points the retiring gate's two section 4.1 entries at the replacement gate's case
names and deletes its section 28.5.3 entry, inside this step". No further edit is owed. EVIDENCE:
proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md:13-16

OPEN: `DEFERRED [spec/04_system-components.md]` is carried forward unclosed. The §4.7 `CoordinatorFence`
row at `:712` reads "Announce new `coordination_generation` to the pod on coordinator handoff", which
proposal 0076 falsified by moving the recorded generation onto the session's slot entry
(`pkg/adapter/slot.go:59`); what is true instead is that the fence announces the generation for the session
it names. The remedy lands in `spec/04_system-components.md`, and in its reader-facing mirror
`docs/reference/adapter-contract.md:69`, neither of which this pass may edit, and neither of which any
deliverable of this proposal stages. The defect is recorded in the summary; the edit is unowned.

OPEN: `DEFERRED [docs/api/internal.md]` is carried forward unclosed. The page is a pre-0073 snapshot: it
publishes a service `RuntimeAdapter` (`:74`) that no proto declares, RPCs `StopSession` (`:81`) and
`UploadFiles` (`:89`, `:94`) that neither `service Adapter` nor `service GatewayControl` declares, and five
request messages carrying a top-level `string session_id` (`:111`, `:147`, `:211`, `:245`, `:273`), a
spelling SPEC-1's constraint paragraph forbids. The remedy lands in `docs/api/internal.md`, which this pass
may not edit and which no deliverable of this proposal stages. The summary's inventory of the page is now
complete; the edit belongs to the documentation loop or to proposal 0080's residue inventory.

OPEN: `DEFERRED [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]` is
carried forward unclosed. 0080's §2 bullet at `:453-454` must split: the declared message-scope table and
its reconciliation gate stay on the side this proposal owns, and the §4.1 `ShutdownRequest` classification
limit returns to 0080's §1 inventory as a gap no proposal takes. The remedy lands in another proposal's
file, which this pass may not edit. The requirement is stated in this proposal's 0080 impacts row.

OPEN: `DEFERRED [0075_....status.md]` is carried forward unclosed. The status file's `**Date:**` line cites
a "§11" that survives only inside this review log, so the pointer resolves across files rather than within
the document a reader is holding. The remedy is to name the review log or drop the reference, and it lands
in
`proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.status.md`,
which this pass may not edit.

OPEN: `DEFERRED [0075_....spec-changes.md]` is carried forward unclosed. D2 calls the addressing convention
"an addressing convention that nothing checks today", which overstates it:
`TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts one direction of it, that
`session_id` exists and is of type `lenny.adapter.v1.SessionId`, over the hand-maintained
`sessionScopedMessages` subset (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`).
What is true instead is that no gate checks the convention in both directions over every message the
protocol declares. The remedy lands in the staged spec changes, which this pass may not edit and may not
reword.

FACT: the `DEFERRED [docs/reference/adapter-contract.md]` entry at the tail of this log's ledger, asking
that `:69`'s fence precondition be restated per session, is not carried forward. Compaction pass 2 deleted
it from the standing context because the tree refutes it: the hold is enforced by pod-level interceptors on
`s.inHoldState()` alone and `s.exitHoldState()` runs after a successful fence for any bound session, so the
precondition is pod-wide. The standing context's Traps section holds the refutation. EVIDENCE:
pkg/adapter/holdstate.go:335,348; pkg/adapter/coordination.go:150-156

FACT: both live open decisions already stand in the summary under `## Open decisions for human to make`, so
this pass promoted nothing. OD1 is retire-or-withdraw, with its recommendation, losing alternatives, and
confidence. OD5 is the unheld widened tier-3 session set, and its entry states in its own words that the
review loop derived no recommendation, which is the record the open-decisions-and-impact-review phase
supplies. No other unclosed `OPEN` in this log is a decision routed to the human: the remainder are
verification questions marked UNVERIFIED, records of unowned pre-existing defects, and format questions
routed to a pass with authority over the summary's claims.

### [f1.open-decisions.OD1]

DECISION: OD1 is already carried exactly as this firing states it, so nothing was written and the diff for
this item is empty. The summary's `## Open decisions for human to make` holds one entry for it, keyed on the
identifier `OD1` (`summary.md:85-151`), stating the question in answerable form, the recommendation ("yes,
accept the residuals and keep the retirement") with moderate confidence, the ground, the two losing
alternatives with why each loses, and the cost of a "no". No answer was staged: the disposition is the
human's. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Open decisions for
human to make`, entry OD1. No edit.

FACT: no migration was owed. Neither staged change file carries an `## Open decisions for review` section
(`spec-changes.md` runs §2, §3, §4, §5, §6, §9, §10; `non-spec-changes.md` runs §4, §5, §8), and `OD1`
occurs in no proposal file outside the summary and this log, so no staged text cites the decision.

FACT: every citation in the OD1 entry re-verifies against the tree today. `spec/04_system-components.md:151`
carries "because `session_id` appears on messages of both classes"; `:175` still declares the fence row
`pod`; `:188` still grounds the declared form on the fence carrying `session_id`.
`schemas/lenny-adapter.proto:1456` is `SessionId session_id = 1` inside `CoordinatorFenceRequest` opening at
`:1455`; `:499-503` is `ReportPodScrubRequest` with `pod_id`, `outcome`, `detail`.
`tests/tier0_static/adapter_proto_message_scope_test.go:25-27` is the tier-1/tier-3 header comment and
`:75-81` is `declaredScope`. `proposals/0073_...md:6653-6655` and `:6686` quote verbatim.
`spec/05_runtime-registry-and-pool-model.md:515` carries the empty-identifier refusal;
`spec/10_gateway-internals.md:38` and `:40` hold the generation per session; `spec/28:314-317` states that a
fence for one session does not change the generation the pod holds for another.

### [f1.open-decisions.OD5]

DECISION: OD5 is answered "accept the widening as a hand-entered set". The answer is staged as a paragraph
in `non-spec-changes.md` §5 under TEST-2 (`:94-108`), beside the eight named additions, stating as a
requirement that no deliverable derives the set from `schemas/lenny-adapter.proto` and that no gate refuses
a session-addressed request message left out of it, with what an omission costs. The entry was deleted from
`summary.md`'s `## Open decisions for human to make`, which now holds OD1 alone. — WHERE:
`0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md` §5 TEST-2, and
`0075_fix_derive-message-scope-from-the-address-type.summary.md` `## Open decisions for human to make`.

FACT: every citation in the staged paragraph re-verifies. `session_address_wire_test.go:32` is `const
retiredFieldName = "slot_id"`, `:78-92` is `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` with
the `slot_id` lookup at `:87-89`, `:99-115` is
`TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` with the presence check at `:107-111` and the
`lenny.adapter.v1.SessionId` comparison at `:112-114`, `:150-158` is
`TestTheRetiredAddressWrapperIsGone_spec_15_4` over `retiredWrapperName` (`:35`).
`spec-changes.md:39-42` carries D2's clause "on every message the protocol declares". The tier-0 inventory
quote is exact at `tests/tier0_static/spec_map_slot_address_registration_test.go:1209-1211`.

FACT: no migration was owed and no cross-file reference was falsified. Neither staged change file carries an
`## Open decisions for review` section, `OD5` occurs in no proposal file outside the summary and this log,
and the summary's remaining sections and the deliverable index name no tier-3 completeness gate. No
deliverable is added, removed, merged, split, or resequenced, so the implementation checklist is untouched
and unaffected.

WATCHOUT: the staged paragraph is a requirement about the set's maintenance, and it sits beside the
membership paragraph it qualifies. A later pass that reads it as decision rationale and deletes it removes
the answer, and the question returns.

### [f1.cleanup]

FACT: `summary.md` already carries exactly the eight required headings in the required order, so this pass
rewrote nothing. `# Summary: Derive message scope from the address type` (`:1`), `## Summary` (`:3`) holding
`**Problem statement.**` (`:5`), `**What changes.**` (`:16`), `**Decisions.**` (`:38`), and `**Watch out
for.**` (`:51`) in that order and carrying no prose of its own above them, then `## Goals` (`:66`),
`## Non-goals` (`:75`), `## Open decisions for human to make` (`:83`), `## Defects in the shipped tree that
this proposal does not stage` (`:153`), `## Impacts on other proposals` (`:249`), and `## Deliverable index`
(`:257`) last. No rename was owed: the two parts already carry their current labels rather than `**What is
fixed.**` and `**Fixed decisions.**`.

FACT: nothing was relocated, because the file carries no unlisted content. There is no meta-list of staged
items, no `### Retired` block or equivalent inside `## Open decisions for human to make`, and no heading
outside the eight. The three-column `## Deliverable index` bullets for SPEC-1, TEST-1, and TEST-2 stand
untouched, line for line, in last position.

FACT: `## Open decisions for human to make` holds OD1 alone, keyed on the verbatim identifier `OD1` at
`:85`, with its question, recommendation and moderate confidence, ground, both losing alternatives, and the
cost of a "no". OD5's entry left the section this firing, its answer staged at `non-spec-changes.md:94-108`,
and no other section of the summary, the deliverable index included, refers to it: `grep -n 'OD[0-9]'` over
`summary.md` returns OD1 at `:85` and references to proposal 0076's own OD2 and OD3 at `:52`, `:72`, `:108`,
`:150`, `:180`, and `:254`, none of which is an open decision of this proposal.

FACT: no prose about another proposal sits outside `## Impacts on other proposals` asserting that
proposal's continued validity, so no merge was owed. The 0073 and 0076 mentions under `**Decisions.**`
(D5, D6) and `**Watch out for.**` state this proposal's own sequencing rather than another proposal's
standing, and the 0080 mentions inside the two unstaged-defect entries agree with the 0080 impacts row
rather than contradicting it: both say §1.1 through §1.21 name neither the §4.7 `CoordinatorFence`
announcement row nor the `docs/api/internal.md` staleness, and the row's "What it must do" column cites
those two sites as the entries record them.

FACT: no `DEFERRED` line was owed by this pass. It landed no correction in any file, so it falsified no
claim in the problem statement, the implementation checklist, or either staged change file, and it added,
removed, merged, split, and resequenced no deliverable.

OPEN: the preamble of `## Defects in the shipped tree that this proposal does not stage` (`:155-160`) still
says "Both defects this proposal confirms in the specification are staged" and then names TEST-2, whose
subject is a comment in a tier-3 test file rather than a sentence in `spec/`. Read against the entries the
section now carries, the preamble's closing sentence ("The further defects confirmed in the working tree are
listed below and left where they are") is true of the three entries below it, and the false clause is a
claim about what is staged elsewhere rather than about those entries. No move of this pass falsified it, so
it is left as the standing `### Open` item records it, now on its third cleanup firing.
[f2.cleanup, f3.cleanup, non-spec-recheck.1.review-citations.1]

WATCHOUT: `### Settled` still reads "The summary's live open decisions are OD1 and OD5", and `### Open`
still carries "OD5, the human's" and "OD5's entry is under-specified" as live items. All three were closed
by `[f1.open-decisions.OD5]`, which answered OD5 and deleted its entry from the summary. The numbering gap
that entry preserves is now OD2, OD3, OD4, OD5, and OD6, and a later firing must not reuse `OD5` for a
fresh decision.

### [non-spec.1.fix-G1.1]

DECISION: Moved the checklist S2 clause, the summary's deliverable-index TEST-1 bullet, and the spec lane's §9 `tests/spec-map.json` gloss onto the staged TEST-1 body's answer (the two section 4.1 entries are re-pointed at the replacement gate's case names, the section 28.5.3 entry is deleted), leaving `non-spec-changes.md:51-56` untouched — BECAUSE that body is the authoritative statement and it decided with ground, and `summary.md:30-31` and `:259` already carried the corrected answer, so this was the last inconsistent pair rather than a live design question — ALTERNATIVES: re-pointing `:5670` at the replacement gate to make the checklist true (rejected: re-creates the surplus credit the credit doctrine at `tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` refuses, and collides with the group holding `non-spec-changes.md` this round); deleting the S2 spec-map clause outright (rejected: the clause is load-bearing for sequencing, it is why the spec-map edit folds into S2 rather than becoming its own step); a hedged "under 4.1, and 28.5.3 if the gate can honestly carry it" (rejected: hands the implementor a judgement the proposal exists to remove).

DECISION: Referred to the entries by gate and section rather than repeating the `:156`/`:169`/`:5670` line anchors in the checklist and the deliverable index — BECAUSE the anchors already live once in the authoritative TEST-1 body, and `tests/spec-map.json` line numbers move whenever an entry is added above them, so three copies is three sites to re-verify — ALTERNATIVES: the reviewer's suggested wording, which carried the anchors verbatim; substantively identical, rejected on drift surface alone.

FACT: `spec-changes.md` was in this loop's editable set, so the §9 `tests/spec-map.json` gloss was tightened in the same pass rather than deferred. It now names both sections and both dispositions. That is the only edit this round makes to the spec lane, and it touches no staged spec text, only the files-touched gloss.

FACT: The tree state the fix depends on, re-verified today: `tests/spec-map.json:156` and `:169` credit `TestAdapterProtoRequestMessagesAreClassifiedByScope` and `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under section 4.1, and `:5670` credits the first again under section 28.5.3. Nothing under `tests/` was edited; that 4.1-and-28.5.3 registration is the correct pre-application state of an unimplemented proposal.

WATCHOUT: A fixer grepping `28.5.3` in `summary.md` gets three hits and only one of them is an edit site. `:30-31` (What changes) and `:259` (the 0073 impacts row) already state the corrected disposition and must be left alone; only the deliverable-index bullet near `:274` was stale. EVIDENCE: `proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:30-31`, `:259`

USEFUL [standing context, line 33]: "The retiring gate's two case names appear only at `tests/spec-map.json:156` and `:169` (section 4.1) and `:5670` (section 28.5.3). The tier-3 file is credited whole-file, so TEST-2 owes no spec-map edit." Confirmed against the tree and it bounded the sweep to one register, which saved re-deriving the whole spec-map surface.

CORRECTS [standing context, `### Open`, line 100]: "Whether the replacement gate can honestly carry a `// spec: 28.5.3` annotation, and whether `tests/spec-map.json:5670` should follow the gate or be dropped" is no longer open. The staged TEST-1 body answers it: the replacement gate's cases carry `// spec: 4.1` alone and the `:5670` entry is deleted, on the ground that section 28.5.3's addressing content is JSON Lines frame equality (`spec/28_communication-channels.md:840-841`), which no gate reading `schemas/lenny-adapter.proto` can regress on. Every restatement in the proposal now agrees with that answer. The compaction pass should retire the bullet.

CORRECTS [review-log.md:3259-3263]: the DEFERRED recorded there, that the checklist's TEST-1 step at `:13-14` ordered a section 28.5.3 registration the staged deliverable deletes, is now applied. The checklist, the deliverable index, and the §9 file gloss all state the deletion.

### [non-spec.1.fix-G2.1]

DECISION: Corrected only the ground TEST-1 gives for the fifth reader of `slotAddressCaseFiles`, keeping the `:973`/`:1028`/`:1054`/`:1096`/`:1110` enumeration and the four-versus-one split — BECAUSE the split carries two distinct failure diagnoses on a gate-file rename (four fail on an unreadable path through `repoFileLines`, the fifth fails by deriving the renamed path and reporting the inventory as omitting it) — ALTERNATIVES: dropping the fifth case from the sentence, which would have hidden the loudest failure and falsified the enumeration; collapsing the split to "all five read files", which would have erased the two diagnoses.

FACT: `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` reads every `_test.go` body under the inventory walk roots. It ranges over `slotAddressCaseFiles` only to build the membership set, then calls `derivedInventoryCaseFiles(t)`, which walks `repoTestFiles(t)` and reads each body with `repoFileBytes`, and errors on any derived file the hand-written list omits — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1131, helper at :1212-1218, `repoFileBytes` at :1269.

WATCHOUT: the fifth case does not resolve a listed path through `repoFileLines`, which is why it looked like it read no file. That is a statement about which helper it calls, rather than about whether it touches the tree, and the two are easy to conflate — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1110 versus :1117.

FACT: the four cases that do resolve each listed path are `:973` (through `specMapCredits`), `:1028` (through `annotatedSectionsPerCase`), `:1054` (calls `repoFileLines` directly at `:1055`), and `:1096` (through `proposalMarkedAnnotationSites`). All five anchors are range-statement lines rather than `func` lines, which is the citation convention the paragraph uses — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:973, :1028, :1054, :1096, :1110.

MISTAKE: an earlier round wrote that the fifth case "reads no file and checks inventory membership alone", which is the opposite of its mechanism and understated the cost of renaming the gate file. It cost this round one finding. The paragraph's conclusion, that keeping the path `tests/tier0_static/adapter_proto_message_scope_test.go` leaves the inventory correct, was and stays true.

CORRECTS [standing context, `### Settled`, the `slotAddressCaseFiles` bullet]: the bullet says "`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970-989`) is one-directional, refusing missing credits and never surplus ones. No edit is owed while the replacement gate keeps its path." The conclusion holds, but the reason is incomplete: `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1108`) is the case that would catch a renamed gate file, because it derives the inventory from the tree and reports omissions. The bullet should name that case as the reason no edit is owed while the path is kept.

CORRECTS [ledger entries near review-log.md:2161 and :3106-3113]: both carry the "reads no file and checks inventory membership alone" wording, the first as a DEFERRED that spotted the discrepancy and declined to file it, the second as the CORRECTS entry that introduced the wording into `non-spec-changes.md`. Not edited, because the review log is a dated round-by-round journal and this repository's convention is a fresh entry rather than a rewrite of a past one. The true statement is in the FACT above.

DEFERRED [review-log.md]: this shard, once merged, is the correction of record for those two ledger entries. Nothing else is owed.

### [non-spec.1.fix-design-G1.1]

DECISION: Correct the two downstream restatements toward the staged TEST-1 body (section 4.1 alone, section 28.5.3 entry deleted), and do not touch the TEST-1 body — BECAUSE non-spec-changes.md:51-56 is the authoritative statement, it carries the ground (28.5.3's addressing content is JSONL frame equality, the gate reads `schemas/lenny-adapter.proto`), and the credit doctrine at tests/tier0_static/spec_map_slot_address_registration_test.go:89-93 refuses a credit under a section the case does not exercise — ALTERNATIVES: (a) reverse the fix, re-pointing :5670 at the replacement gate and leaving the checklist as written — rejected, it re-creates the surplus credit and reverses a settled decision; (b) hedge the checklist ("4.1, and 28.5.3 if the gate can honestly annotate it") — rejected as hair, the proposal decided; (c) repeat the `:156`/`:169`/`:5670` line anchors in the checklist as the reviewer's suggested wording did — rejected, the anchors live in the authoritative TEST-1 body and repeating them creates two more sites that drift when spec-map.json grows; name the entries by their gate and their section instead.

FACT: `tests/spec-map.json:156` credits `TestAdapterProtoRequestMessagesAreClassifiedByScope` and `:169` credits `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under `spec/04_system-components.md`; `:5670` credits the first again under section 28.5.3. Re-verified 2026-09-08 — EVIDENCE: tests/spec-map.json:156, :169, :5670

FACT: a third restatement of the same disposition sits in the spec lane, not the non-spec lane: `spec-changes.md:164-165` (§9 Files touched) says spec-map "is re-pointed at the replacement gate's cases in the same change" and records no deletion. It is incomplete rather than false, so it survives the G1 edit either way; correct it in the same pass if spec-changes.md is editable, otherwise it is the one loose end — EVIDENCE: 0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:164-165

DEFERRED [0075_fix_derive-message-scope-from-the-address-type.review-log.md]: the `## Standing context` → `### Open` bullet "Whether the replacement gate can honestly carry a `// spec: 28.5.3` annotation, and whether `tests/spec-map.json:5670` should follow the gate or be dropped" is no longer open. What is true instead: the staged TEST-1 answers it (annotation `// spec: 4.1` alone, `:5670` deleted, ground stated at non-spec-changes.md:51-56), and the summary's `## Open decisions for human to make` now carries only OD1 and OD5, so the OD that held this question is gone. The compaction pass should retire that bullet.

WATCHOUT: the summary already states the corrected disposition in two other places — the "What changes" bullet at :30-31 and the 0073 impacts row at :259 — so a fixer that greps `28.5.3` in summary.md will find text that must NOT change. Only the deliverable-index TEST-1 bullet at :274-275 is wrong — EVIDENCE: 0075_fix_derive-message-scope-from-the-address-type.summary.md:30-31, :259, :274-275

WATCHOUT: `tests/spec-map.json` in the tree is correctly in its pre-application state (registered under 4.1 and 28.5.3). Nothing in this proposal has been applied. Do not "fix" the tree — EVIDENCE: tests/spec-map.json:5670

### [non-spec.1.fix-design-G2.1]

DECISION: Apply the reviewer's suggested sentence verbatim in `non-spec-changes.md:46-47` — replace "reads no file and checks inventory membership alone" with "resolves no listed path through `repoFileLines`; it derives the expected inventory from the tree (`derivedInventoryCaseFiles`, `:1212`) and reports any derived file the list omits" — BECAUSE the four-versus-one split is real but rests on `repoFileLines` resolution rather than on reading no file, and the corrected ground strengthens the paragraph's conclusion (keeping the gate file's path keeps all five cases green) instead of weakening it. ALTERNATIVES: (a) drop the fifth case from the sentence and say "four cases range over that list" — rejected, it hides the case whose failure mode on a rename is the loudest; (b) collapse to "all five cases read files from the tree" — rejected, it erases the distinction the paragraph needs, since a rename breaks the four by unreadable path and the fifth by derived-but-omitted path, two different failure messages.

FACT: The five ranging cases over `slotAddressCaseFiles` are `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:973`), `TestAddressCaseNamesAgreeWithTheirOwnCitations` (`:1028`), `TestAddressCaseTextCitesNoProposalSectionNumber` (`:1054`), `TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly` (`:1096`), and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1110`). The first four reach `repoFileLines` (directly at `:1054`, via `citedSectionsPerCase` / `annotatedSectionsPerCase` / `proposalMarkedAnnotationSites` in the others). The fifth reads files too, but only through `derivedInventoryCaseFiles` over `repoTestFiles(t)`, never through a listed path. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1131, :1212-1225.

FACT: `derivedInventoryCaseFiles` is declared at `tests/tier0_static/spec_map_slot_address_registration_test.go:1212`, and `:1110` is the `for _, file := range slotAddressCaseFiles` line inside the fifth case (the case's `func` line is `:1108`). The proposal cites the range lines, not the func lines, for all five; keep that convention. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108, :1110, :1212.

WATCHOUT: The wrong wording also appears inside `review-log.md` (the DEFERRED entry near `:2161` and the `CORRECTS` entry that introduced it around `:3106-3113`). Do not edit those. The review log is a dated round-by-round journal; the repo convention is a fresh `CORRECTS` entry rather than rewriting a past one. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:1-12 (compaction changelog framing).

FACT: No other file in the proposal directory and no file under `spec/`, `docs/`, `schemas/`, or `charts/` mentions `slotAddressCaseFiles`, the five-case split, or the gate-file-path argument, so the fix is local to `non-spec-changes.md`. The standing-context bullet on `slotAddressCaseFiles` (review log) states only that no edit is owed while the replacement gate keeps its path, which the corrected sentence still supports.

### [non-spec.1.review-applicability.2]

MISTAKE: the fix round that answered OD4 ("drop the §28.5.3 registration") rewrote
`non-spec-changes.md:51-64` and `summary.md:30-31` and the summary's 0073 impacts row, but left the two
other carriers of the old answer standing. `implementation-checklist.md:13-14` still tells the implementor
to register "under sections 4.1 and 28.5.3", and `summary.md:274-275` still says "under the sections the
retiring gate's cases held". Both are now the opposite of TEST-1. Filed as two findings this round.
The lesson is mechanical: whenever a fix changes what a deliverable does, grep the checklist AND the
deliverable index for the old wording before returning. — EVIDENCE:
0075_...implementation-checklist.md:13-14; 0075_...summary.md:274-275; 0075_...non-spec-changes.md:53-55

FACT: the round-3-to-round-4 delta on the staged files is exactly two hunks, both in
`non-spec-changes.md` (the `repoFileLines` four-of-five correction and the spec-map/28.5.3 paragraph), plus
two hunks in `summary.md` (OD4 deleted, OD1 confidence paragraph widened). `diff -u` per file against
`scratchpad/cp-snap/.../spec-r3` shows it in under 60 lines; the directory-level `diff -ru` buries it under
the review log's compaction hunk. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3

FACT: `validate-maps` is run from `runStaticTier`, which begins at `cmd/lenny-test/cmd_run.go:490`; the
`validate-maps` check is at `:761` and `go vet -tags=contract ./tests/tier3_contract/...` at `:503-509`.
Both are inside tier 0, so S2's single-commit re-registration argument and S3's compile exposure are both
real. — EVIDENCE: cmd/lenny-test/cmd_run.go:490, :503-509, :761

FACT: nothing outside `tests/spec-map.json` names the retiring gate's two case names.
`addressRuleCases` (`tests/tier0_static/address_rule_citation_test.go:29-48`) names only the tier-3 case,
and `slotAddressCaseFiles` names files by path. So TEST-1 may name the replacement cases freely, and the
"case names are unstated" gap is implementor judgment rather than an underspecified target. Third
confirmation; closes the standing OPEN "whether the replacement gate's own case names must differ from the
retiring ones for validate-maps to stay green". They need not: S2 rewrites the file and the map in one
commit either way. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:45-47;
tests/tier0_static/spec_map_slot_address_registration_test.go:336

CORRECTS [Settled: "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one
line off, below the bar)"]: the standing-context entry is wrong and SPEC-1 is right.
`schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. Do not
"correct" SPEC-1's `:458` to `:457`. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: S3's tier list omitting tier 0 follows repo convention and is not a finding. 0076's S8 is a `· test`
step touching Go test files and lists "Tiers 1, 4, 7a" with no 0. The convention is that a step lists the
tiers its deliverable's own cases belong to, not every tier that compiles the file. Fourth agent to reach
this; settles the standing UNVERIFIED. — EVIDENCE:
proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_...implementation-checklist.md:31-33

FACT: deleting `tests/spec-map.json:5670` cannot strand section 28.5.3. That section carries roughly forty
other entries (`:5655-5690` alone), so no "section has no coverage" reading is at risk. — EVIDENCE:
tests/spec-map.json:5655-5690

OPEN: the summary's open-decision numbering now runs OD1 then OD5, because OD2, OD3, and OD4 were answered
and deleted across rounds. Cosmetic and below the review bar, but a human reading the section sees a gap.
### [non-spec.1.review-applicability.2]

DECISION: returned an empty findings list after simulating S1 -> S2 -> S3 against the tree — BECAUSE every
anchor the three steps name resolves, every artifact each step references already exists or is created by
an earlier step, the one gate the sequence reddens (tier 0, S1 to S2) carries its disposition on S1's own
line, and the checklist is three steps, one lane each, three deliverables named once each, all boxes
unchecked. ALTERNATIVES: filing the replacement gate's unnamed case names as an underspecified target
(rejected: the names are written and registered inside the same step S2, nothing outside that step reads
them, and a test function name is the "choosing a variable name" exclusion); filing S3's tier list omitting
tier 0 (rejected: already filed once by non-spec.2 and declined by four later applicability passes).

FACT: no cache hit existed at hash `d91c9fcab1da` for this lens. Sibling lenses had already written
`citations-r1-d91c9fcab1da.json`, `edit-sites-r1-d91c9fcab1da.json`, `kubernetes-r1-...`,
`performance-r1-...`, `security-r1-...` at the same hash, so the hash is the current text and the absence
was real rather than a key mismatch. — EVIDENCE:
/home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/

FACT: the whole staged delta since snapshot `spec-r3` is ONE paragraph, the OD5 answer at
`non-spec-changes.md:95-108` ("The session set stays hand-entered ..."). `summary.md`,
`spec-changes.md`, and `implementation-checklist.md` are byte-identical to that snapshot. Every citation in
the new paragraph verifies: `session_address_wire_test.go:32` (`const retiredFieldName = "slot_id"`),
`:78-92`, `:107-111`, `:112-114`, `:150-158`, and the tier-0 inventory quote at
`spec_map_slot_address_registration_test.go:1209-1211` is exact.

FACT: the widened tier-3 session set is provably exactly right, checked by an independent comment-stripping
brace-counting parse rather than by trusting the census. The 25 RPC request types declaring a top-level
`SessionId session_id` are exactly the 17 request types already in `sessionScopedMessages` plus the eight
additions TEST-2 names; nothing is missing and nothing is extra, and the name/type biconditional has zero
violations across the whole file. — EVIDENCE: schemas/lenny-adapter.proto; the 18 messages declaring
`reserved "slot_id"` are exactly today's 18 map keys, so `retiredDuplicateNumbers` inherits a closed set.

FACT: `protoServiceRequests` survives TEST-1 as a live caller. The replacement gate still needs the request
message set to evaluate the envelope clause ("a REQUEST message carrying its frames in a `oneof`"), so the
function does not go unused. A gate written to read `protoFields` alone would leave `protoServiceRequests`
with zero callers and golangci-lint `unused` would redden tier 0. Worth stating because the retiring gate is
its only caller today (`adapter_proto_message_scope_test.go:87`). — EVIDENCE:
tests/tier0_static/adapter_proto_parse_test.go:68; tests/tier0_static/adapter_proto_message_scope_test.go:87

FACT: the retiring gate's private helpers have no reader anywhere outside their own file. A grep for
`messageScopeSpecPath|checkpointStartMessage|checkpointStartService|messageScopeRow|parseMessageScopeTable|declaredScope|messageScopeDisagreements`
over `tests/ pkg/ cmd/` returns nothing outside `adapter_proto_message_scope_test.go`, so TEST-1's in-place
rewrite compiles without touching a second file for symbol reasons.

FACT: nothing outside the tier-0 gate reads the §4.1 message-scope block. Every `§4.1` inbound reference in
`spec/` points at the Edge Gateway Replicas capacity, HPA, or subsystem-extraction content
(`spec/18:138,637,640,810`, `spec/10:77`, `spec/13:736`, `spec/16:34,106,109-118`); no tier-11 gate opens
that block, and `TEST-GAPS.md`/`BUILD-GAPS.md` name neither gate case nor either touched test file. The
tier-11 coverage-audit gates the tier-0 inventory derives from (`coverageAuditGateDir =
"tests/tier11_docs/"`, `coverageAuditRE = TEST-GAPS\.md`) therefore have no subject here. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1177,1186

WATCHOUT: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` is itself a member of
`slotAddressCaseFiles` (`tests/tier0_static/spec_map_slot_address_registration_test.go:409`), not only the
tier-0 gate file. So TEST-2's rewrite is read by the same four `repoFileLines` loops. It survives only
because TEST-2 renames no test function, adds none, and changes no `// spec:` annotation. A fixer who adds a
fifth test function to that file, or retitles one, drags `tests/spec-map.json` and
`tests/tier0_static/address_rule_citation_test.go:45-47` into a blast radius §9 does not list. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:409, :973, :1028, :1054, :1096

WATCHOUT: the rewritten declaration comment in the tier-3 file passes through
`TestAddressCaseTextCitesNoProposalSectionNumber` (`:1054`), which fails any line matching
`proposal\s+§\s*\d` anywhere in the file, comments included. The new comment states the derivation rule and
must not cite this proposal's own section numbers. Nothing staged does, but the comment is the one place a
fixer would be tempted to write "proposal §5". — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1043-1058

USEFUL [Traps: "Naive proto parsers derail on this file"]: saved the census re-derivation from producing
garbage. Stripping `//` comments and counting braces per line, with same-line open/close handled, reproduced
31 request types and 25 addressed ones first try.

USEFUL [Traps: "Cache keys omit summary.md and review-log.md, and the cache path carries no lane"]: the
correct move at a miss is still to check whether a sibling-lane file at the same hash exists; five did, and
knowing the key is lane-agnostic stopped me from reading one of them as this lens's own prior answer.

OPEN: unchanged from [non-spec.1.review-applicability.1] — whether the replacement gate's case names must
differ from the retiring ones for `validate-maps` to stay green across S2's single commit. I now believe the
answer is no, because S2 rewrites the gate file and `tests/spec-map.json` in one step so
`validateSpecMapTestFuncs` never sees a `path::Fn` naming an absent function, and reusing a name would be
merely misleading rather than red. Nobody has run the sequence, so it stays OPEN rather than settled.

### [non-spec.1.review-citations.1]

DECISION: filed two findings, both in text the OD4-closing fix round wrote or left behind — BECAUSE the only substantive delta since the `spec-r3` snapshot is the §28.5.3 registration decision (TEST-1 now deletes `tests/spec-map.json:5670` instead of re-pointing it) plus the OD1 confidence paragraph and the backoff half of the coordfence defect bullet; the fixer updated `non-spec-changes.md` §5 and two summary sites but not the implementation checklist or the summary's Deliverable index, and it introduced one false attributed behavior about `tests/tier0_static/spec_map_slot_address_registration_test.go:1110` — ALTERNATIVES: re-filing the dangling `§11` pointer in `status.md:22` (declined, as `non-spec.5.review-citations.1` did, and it is already a DEFERRED).

FACT: the whole citation inventory was re-verified against the tree at HEAD `ecfddd860` in this round, not spot-checked. Everything in the Settled block still holds byte-for-byte; the tree has not moved since `c5d35bb05` (the only working-tree modifications are three of this proposal's own files). New citations verified this round, none of which any earlier round had seen: `proposals/0073_...md:6653-6655` (the rejected machine-derivable-rule alternative, quote exact) and `:6686` ("D6 replaces a machine-derivable classification with a declared one", exact); `spec/28_communication-channels.md:501` (the 28.5.3 boundary sentence; the quoted phrase wraps onto `:502`, below the bar) and `:840-841` (Address equality, JSON Lines frame); `tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` (credit-doctrine quote, exact) and `:336`/`:699-706`/`:973`/`:1028`/`:1054`/`:1096`/`:1110`; `pkg/gateway/coordination/coordfence/coordfence.go` imports no `time` package at all and the package holds exactly one non-test file, so "no timer, sleep, or backoff of any kind" is exact.
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6653; tests/tier0_static/spec_map_slot_address_registration_test.go:89

WATCHOUT: the five `for _, file := range slotAddressCaseFiles` loops are NOT five equivalent readers. Four (`:973`, `:1028`, `:1054`, `:1096`) resolve each listed path through `repoFileLines` and die on a path they cannot read. The fifth (`:1110`, `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`) resolves no listed path — but it is not file-free either: it calls `derivedInventoryCaseFiles(t)` (`:1212`), which walks every `_test.go` under the walk roots and reads each body (`repoFileBytes`, `:1216`), then demands the inventory carry every file the derivation matches. So a RENAME of the gate file would fail the fifth case too, by the opposite mechanism (the renamed file is derived into the expected set and reported as omitted). Any future sentence about "which of the five would break" has to say "resolves no entry through `repoFileLines`", never "reads no file".
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1130, :1212-1229

WATCHOUT: `tests/spec-map.json:5670` deletion is now decided in exactly three places and stale in exactly two. Decided: `non-spec-changes.md:53-56`, `summary.md:31`, `summary.md:259` (the 0073 impacts cell). Stale: `implementation-checklist.md:13-14` ("under sections 4.1 and 28.5.3") and `summary.md:274-275` ("under the sections the retiring gate's cases held"). The checklist is the document `implement-proposal` executes, so this is the site that actually costs an implementor a wrong `tests/spec-map.json` edit.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md:13

USEFUL [Traps: cache keys omit `summary.md` and `review-log.md`]: the key is still `md5(spec-changes + non-spec-changes + implementation-checklist)`, so this round's key changed only because `non-spec-changes.md` moved. Had the fixer edited the summary alone, the citation lens would have been served the previous run's empty answer and neither of this round's findings would have been seen.

USEFUL [Traps: long lines hide the sentences citations point at]: `sed -n '515p' spec/05 | tr '.' '\n' | tail -6` and `sed -n '725p' spec/04 | grep -o '<sentence>'` recovered both clauses again. `sed -n 'Np' | cut -c1-N` shows the wrong half of every one of these lines.

FACT: `TestConsistencyGatesAreMappedOnlyToSectionsTheyAnnotate` (`:183`) is the only gate in the tree that mechanically refuses a surplus whole-file credit, and its `consistencyGateFiles` domain (`:171-177`) does NOT include `tests/tier0_static/adapter_proto_message_scope_test.go`. So nothing would have gone red had the `:5670` credit been kept; the ground for deleting it is the doctrine at `:89-93`, which is what `non-spec-changes.md:56-58` correctly says. Do not re-file the deletion as unnecessary, and do not re-file it as gate-forced.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:171-177, :183-204

### [non-spec.1.review-citations.1]

DECISION: returned an empty findings list — BECAUSE I extracted every `file:line` citation and every
attributed behaviour from `spec-changes.md`, `non-spec-changes.md`, `summary.md`, `implementation-checklist.md`
and `problem-statement.md` and verified each one against the tree; all resolve, including the fresh
"session set stays hand-entered" paragraph added since the `spec-r3` snapshot. ALTERNATIVES: I weighed and
dropped one candidate, described under OPEN below.

FACT: the standing-context UNVERIFIED on `message SessionId`'s line is settled at `:596`; `:595` is the
doc comment and `:597` is `string value = 1`. — EVIDENCE: schemas/lenny-adapter.proto:595-597

CORRECTS [Settled: "Commit `040323634` ... names `CoordinatorFence` nowhere"]: the commit names
`CoordinatorFence` 44 times across `pkg/adapter/coordination.go`, `holdstate.go`, `docs/reference/adapter-contract.md`
and others. What is true is narrower and is what the proposal actually asserts: the commit's
`schemas/lenny-adapter.proto` diff contains no `CoordinatorFence` line at all, so it left
`CoordinatorFenceRequest` untouched in the protocol definition while adding all 18 `reserved "slot_id"`
pairs. `non-spec-changes.md` §4's wording ("left the fence untouched") is correct in its proto context; the
log entry's wider phrasing is not, and a lens that greps the whole commit will think the proposal is wrong.
— EVIDENCE: git show 040323634 -- schemas/lenny-adapter.proto | grep -c CoordinatorFence  → 0

FACT: the whole proto census, the 25-message addressed set, the 18-member `reserved "slot_id"` set, and the
TEST-2 split arithmetic can be reproduced in ONE python3 heredoc that strips `//` comments and counts brace
deltas per line, tracking `oneof` depth so `oneof` arms are excluded from the top-level field list. It
returned, first try: 31 request types; 25 declaring a top-level `SessionId session_id`; the 6 that do not
are exactly the names the log records; the 18 `reserved "slot_id"` messages are exactly today's
`sessionScopedMessages` keys (symmetric difference empty); and 17 of those plus TEST-2's 8 named additions
equal the 25. Spot-checking by hand costs several calls and risks the parser trap; the script costs one.
— EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the addressing convention holds in both directions with two greps that both return nothing:
`grep -n "session_id\s*=" schemas/lenny-adapter.proto | grep -v "SessionId session_id"` and
`grep -nE "^\s*(repeated\s+)?SessionId\s+\w+\s*=" schemas/lenny-adapter.proto | grep -v "SessionId session_id"`.
That is the cheapest confirmation that D2's replacement gate is green on day one. — EVIDENCE: schemas/lenny-adapter.proto

FACT: the whole of `non-spec-changes.md`'s TEST-1 anchor set verifies exactly: `slotAddressCaseFiles`
declared at `:236` with the gate path at `:336` and the shared parse at `:337`; the five loops at `:973`,
`:1028`, `:1054`, `:1096`, `:1110`; `repoFileLines` at `:699-706`; the credit-doctrine sentence at `:89-93`;
`derivedInventoryCaseFiles` at `:1212`. The `:1209-1211` quote spans `:1209-1210` in the file, with `:1211`
carrying the following sentence; that is drift of one line and not a defect. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:236,336,337,699-706,89-93,973,1028,1054,1096,1110,1209-1212

FACT: the spec-map anchors are all live. `tests/spec-map.json:156` and `:169` are the retiring gate's two
case names under the section 4.1 block that opens at `:152`; `:5670` is the same first case name under the
section 28.5.3 block that opens at `:5643`; the tier-3 file is credited whole-file at `:172`. — EVIDENCE:
tests/spec-map.json:152,156,169,172,5643,5670

FACT: `protoFields` has exactly one caller (`tests/tier0_static/claim_register_proto_agreement_test.go:64`)
and `protoServiceRequests` exactly one (`tests/tier0_static/adapter_proto_message_scope_test.go:87`). A
repo-wide grep for both call sites returns four lines total, two of which are the definitions. Neither
tier-0 file 0076 landed calls either. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36,68

FACT: `pkg/adapter/holdstate.go:335` and `:348` are the two interceptor function declarations; the
`s.inHoldState()` predicate lines are `:336` and `:349`. Both spellings appear in the log for the same
claim and both are defensible; the summary uses `:335`/`:348` and that is not a defect. — EVIDENCE:
pkg/adapter/holdstate.go:335-336,348-349

OPEN: the fresh paragraph closes with an analogy — "A derived rule beside a hand-entered remainder is the
arrangement tier 0 already records for its own inventory" — quoting
`tests/tier0_static/spec_map_slot_address_registration_test.go:1209-1211`. The quoted words are exact, but
the source's very next sentence, inside the cited range, is "Each rule turns one class of silent omission
into a tier-0 failure", which is the property TEST-2's arrangement lacks: tier 0's derived rules check the
hand list and fail on an omission, while nothing checks `sessionScopedMessages` at all. I declined to file
it, because the paragraph states that gap plainly two sentences earlier ("no gate refuses a session-addressed
request message added later and left out of it"), so nothing is concealed and the analogy is rationale rather
than an applied claim. Recorded so the next citation pass does not spend the derivation again. Whoever next
edits that paragraph could weaken the analogy to "a hand-entered list beside a derived constraint on a
different property" and lose nothing.

WATCHOUT: `summary.md` no longer carries OD5 — the whole entry was deleted at 05:35 on 2026-09-08 and its
answer is now staged as the "session set stays hand-entered" paragraph in `non-spec-changes.md` §5. The
orchestrator brief, `status.md:22`'s review-history paragraph, and the standing context's `### Open` bullet
all still describe OD5 as live. The summary's `## Open decisions for human to make` section now holds OD1
alone. Do not read the brief as evidence of the document's current state. — EVIDENCE:
proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:83-151

USEFUL [Traps: "Bash is the wrong tool for reading this log"]: exactly right, and it compounds. `cat`ting
the persisted overflow file persists it AGAIN under a new name, so `awk`/`sed` over the standing context
costs three wasted calls before you give up. Read tool with `offset`/`limit` on the review log from the
start; the section boundaries are `grep -n "^## \|^### "` on the log, which is small enough to print.

USEFUL [Traps: "Naive proto parsers derail on this file"]: the brace-delta-per-line rule plus comment
stripping is what made the one-shot census script correct. Without it the same script mis-scopes
`AttachRequest`.

### [non-spec.1.review-client-surface.1]

DECISION: returned an empty findings list — BECAUSE the proposal touches no externally-consumed
representation. It edits `spec/04` §4.1 prose, three tier-0 test files, `tests/spec-map.json`, and one
tier-3 contract suite; `schemas/lenny-adapter.proto`, `schemas/*.json`, `pkg/gateway/openapi/openapi.json`,
`sdks/**`, `charts/**/crds`, and every `docs/` page stay byte-identical, and I verified each of those
surfaces independently rather than taking the proposal's word — ALTERNATIVES: filing the stale
`docs/api/internal.md` protobuf excerpts as a missed edit site; rejected because the page is already false
about the shipped proto before anything staged here applies (bar (d) needs a surface that *becomes* wrong),
and the summary already records it with an owner.

FACT: the widened tier-3 session set is exactly complete against the derivation rule, verified
mechanically rather than by re-reading the proposal. Parsing the two service blocks gives 31 RPC request
types, 25 of which declare a top-level `SessionId session_id`; the 17 request-type members of today's
`sessionScopedMessages` plus TEST-2's 8 additions equal that 25 set exactly, with no missing and no extra
member, and `CheckpointStart` is the 26th (non-request) member. All 8 additions declare `SessionId
session_id = 1` and none declares a field named `slot_id`, so both widened address arms pass on the shipped
proto.
EVIDENCE: schemas/lenny-adapter.proto:341,364,383,403,419,1455,1538,1673

FACT: `retiredDuplicateNumbers`'s stated membership rule is exact. A brace-counting parse of
`schemas/lenny-adapter.proto` finds exactly 18 messages declaring `reserved "slot_id"`, and they are
precisely the 18 keys of today's `sessionScopedMessages` map, each with the reserved number the map's value
column carries. The split therefore preserves `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4`'s
population unchanged.
EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-62

FACT: the addressing biconditional the replacement gate enforces is green on the shipped adapter proto in
both directions, over the whole file and not only over request types. Every occurrence of the token
`session_id` outside a comment is a `SessionId session_id` field declaration, and the only other occurrence
of the token `SessionId` is `message SessionId` itself.
EVIDENCE: schemas/lenny-adapter.proto:596

FACT: `spec/04_system-components.md:1047` declares `string session_id = 2;` inside a protobuf block. It is
`RequestInterceptor`'s `InterceptRequest` (`schemas/lenny-interceptor.proto`), a different protocol, so the
staged rule's "the gateway-adapter protocol" antecedent excludes it and it is not a contradiction. A
reviewer grepping `spec/` for `string session_id` hits this first and it looks like one.
EVIDENCE: spec/04_system-components.md:1037-1049

FACT: no client-facing surface mirrors the §4.1 request-message-scope classification. A case-insensitive
grep for "message scope" across `docs/`, `spec/`, `schemas/`, `charts/`, and `sdks/` returns exactly one
hit, the §4.1 heading itself. Every "session-scoped" hit in `docs/` and in `schemas/*.json` is about the
JSONL frame set (§28.5.3) or about tokens and leases, none of which the derivation rule reaches.
EVIDENCE: spec/04_system-components.md:149

FACT: `grep -rn "CoordinatorFence\|coordination_generation\|coordinationGeneration" sdks/ charts/` returns
nothing, so the reclassification reaches no language SDK and no CRD. The proposal's §1.4 blast-radius claim
is exact on this point.

FACT: the five whole-file `tests/spec-map.json` credits for the tier-3 suite resolve to sections 4.1
(`:172`), 4.7 (`:600`), 5.2 (`:1110`), 6.4 (`:1391`), and 15.4 (`:3678`). Widening two loop populations
without renaming or adding a function leaves all five valid, so TEST-2's "registers nothing" holds.
EVIDENCE: tests/spec-map.json:172,600,1110,1391,3678

USEFUL [standing context "Proto census", "Addressing convention", "TEST-2 split arithmetic"]: all three
were re-derived mechanically here and all three are exact. Spot-checking rather than re-deriving is the
right advice; I re-derived anyway and it cost about twenty minutes for zero delta.
### [non-spec.1.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens — BECAUSE the staging touches
no externally-consumed representation at all: no proto, no `schemas/*.json`, no SDK file in any language, no
CRD, no OpenAPI/MCP/A2A schema, no `docs/api/*`, `docs/client-guide/*`, or `docs/runtime-author-guide/*`
page, and no client-visible enum or error code. The three deliverables move one `spec/04` block, one tier-0
gate, one shared tier-0 parse plus its other caller, `tests/spec-map.json` entries, and one tier-3 test's
declarations. ALTERNATIVES: filing `docs/api/internal.md` as a missed edit site, rejected because the page
is wholesale stale before anything staged applies (it publishes a `RuntimeAdapter` service no proto
declares) and the summary inventories all five of its sites with a stated non-repair ground; filing the
retired table's Service/Direction columns, which the standing context already routes to OD1.

FACT: `schemas/lenny-adapter.proto` carries exactly 26 `SessionId session_id` field declarations and the
string `SessionId ` appears on no other field line — `grep -n "SessionId " schemas/lenny-adapter.proto |
grep -v "message SessionId" | grep -v session_id` returns only the doc-comment line. So D2's replacement
gate is green in BOTH directions on the shipped published proto on day one, which is the client-surface
question that matters for a gate over a runtime-author-facing schema. — EVIDENCE:
schemas/lenny-adapter.proto:596, and the 26 declarations including :342, :365, :384, :404, :420, :458,
:1217, :1456, :1539, :1674

FACT: `grep -c 'reserved "slot_id"' schemas/lenny-adapter.proto` is exactly 18, matching
`sessionScopedMessages`'s current key count, and `:1211` is the one inside `CheckpointStart`. That confirms
TEST-2's `retiredDuplicateNumbers` membership rule ("the messages that declare `reserved \"slot_id\"`")
without re-deriving the split arithmetic. — EVIDENCE: schemas/lenny-adapter.proto:465,1211,1619

FACT: the JSONL-leg "session-scoped frame" vocabulary is dense across the client-facing surfaces and is a
DIFFERENT classification from the one SPEC-1 derives. A client-surface or docs lens that greps
`session-scoped` hits `schemas/lenny-adapter-jsonl.schema.json:61` and `:211`, all three runtime SDKs
(`sdks/runtime/go/runtime/types.go:72`, `sdks/runtime/python/lenny_runtime/runtime.py:389`,
`sdks/runtime/typescript/src/types.ts:72`), `pkg/adapter/attach.go:274`
(`sessionScopedFrameTypes`), and five `docs/runtime-author-guide/` pages before it reaches anything on the
gRPC leg. None of them is an edit site: SPEC-1's sentence is bound by "on the gateway-adapter protocol" and
by "either service declares", and the JSONL set is §28.5.3's. Do not file a "declared set contradicts the
derived rule" finding on it. — EVIDENCE: pkg/adapter/attach.go:268-274; schemas/lenny-adapter-jsonl.schema.json:61

FACT: the tier-11 gate that pins `spec/04:725` resolves its spec text with `specSection(t, ...,
"### 4.7 ")` and matches the row by the literal `| \`ReportSessionScrub\` |`, so it reads §4.7 alone and
SPEC-1's `#### Request Message Scope` edit cannot redden it whatever happens to §4.1. This closes the
"editing :725 would move a reader-facing document and a gate" claim in SPEC-1 mechanically rather than by
inference. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:46-48,58

FACT: `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` already asserts SPEC-1's
envelope clause on the wire — "the opening frame's session_id is the whole stream's address, so a stream
that opens without a usable one is refused with InvalidArgument" — and it is credited to section 4.1. The
envelope clause therefore lands beside a tier-3 case that agrees with it and needs no edit; it is also the
best existing evidence that the clause states shipped behavior rather than new behavior. — EVIDENCE:
tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:881-887

USEFUL [Standing context, Traps: "Cache keys omit summary.md and review-log.md, and the cache path carries
no lane"]: the cache slot for `client-surface-r1-<hash>` was empty this run, so no stale-lane hit was
possible, but the entry is what made me check the slot's `coverage` prose intent before trusting it. Kept
the lane name inside my own `coverage` string for the next reader.

USEFUL [Standing context, Settled: "No docs mirror. docs/ names RPCs and never request message types"]:
saved a full docs sweep. I re-verified it narrowly rather than re-deriving it — `grep -rn
"pod-scoped\|session-scoped" docs/ schemas/ sdks/ charts/` returns nothing that classifies a gRPC request
message except `docs/reference/adapter-contract.md:81`, which is the §4.7 `ReportSessionScrub` row SPEC-1
keeps.

CORRECTS [Standing context, Open: "UNVERIFIED: whether `message SessionId` opens at
schemas/lenny-adapter.proto:595 or :596"]: settled at **:596**. `grep -n "^message SessionId"` returns
`596:message SessionId {`; `:595` is the doc comment "SessionId identifies a session. Format: UUIDv8" and
`:597` is `string value = 1;`. The `non-spec.5.review-mechanism.1` reading of `:595` is wrong. This
UNVERIFIED can be retired from the standing context. — EVIDENCE: schemas/lenny-adapter.proto:595-597

### [non-spec.1.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE the docs-alignment lens has no live surface on this
proposal: it stages spec + tests only, touches no metric, alert, flag, endpoint, error code, or lifecycle
step, and the one classification it moves (`CoordinatorFenceRequest`, pod → session) is restated on no
`docs/` page. ALTERNATIVES: filing the `docs/api/internal.md` staleness as a missing edit site, rejected
because the page is already false about the shipped proto before anything staged applies, the summary
records it under `## Defects in the shipped tree that this proposal does not stage`, and the standing
context already carries it as DEFERRED with an owner named.

FACT: re-verified independently this round that `docs/` carries no mirror of the §4.1 classification and
no page states a request message's scope class except one row. `grep -rn "pod-scoped\|session-scoped"
docs/` returns 12 hits, all JSONL-leg or token/credential prose, plus
`docs/reference/adapter-contract.md:81` ("The request is session-scoped: it is addressed by the identifier
of the released session and names no slot") which mirrors `spec/04_system-components.md:725` and which
SPEC-1 keeps. — EVIDENCE: docs/reference/adapter-contract.md:81, spec/04_system-components.md:725

FACT: the tier-11 gate the proposal cites for `:725` reads as described, and it is the only tier-11 case
touching `spec/04` §4.7's scope prose. It compares the `| \`ReportSessionScrub\` |` row of §4.7 against the
same row in `docs/reference/adapter-contract.md` and requires both to carry the exact opener "The request
is session-scoped: it is " plus the shared rule sentence. Editing `:725` reddens it; SPEC-1 does not.
— EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-79

FACT: no tier-11 case reads `spec/04` §4.1. Every tier-11 file naming `04_system-components.md` scopes to
`### 4.7 `, `#### 4.6.1 `, `#### 4.6.3 `, `### 4.4 `, `### 4.9 `, or an anchor link. So the S1 red window
is tier 0 only, and the checklist's "Tiers 0, 11" on S1 is a regression run rather than a case owed.
— EVIDENCE: tests/tier11_docs/budget_extension_trigger_consistency_test.go:132, :174;
tests/tier11_docs/eviction_coordinator_route_consistency_test.go:64, :89;
tests/tier11_docs/spec_47_rpc_row_naming_test.go:46

FACT: `docs/reference/adapter-contract.md:82`'s `ReportPodScrub` row carries no scope word, while
`spec/04_system-components.md:726` ends "The request is pod-scoped." The asymmetry is pre-existing, no gate
holds the pod half the way the tier-11 case holds the session half, and SPEC-1 leaves `:726` standing, so
it creates no obligation here. Do not re-file it as an edit site.
— EVIDENCE: docs/reference/adapter-contract.md:82, spec/04_system-components.md:726

FACT: every docs-side citation the summary's `## Defects in the shipped tree` section makes was checked
against the tree and each holds. `docs/api/internal.md:210-214` publishes `message CheckpointRequest {
string session_id = 1; string checkpoint_id = 2; string consistency = 3; }`; `:270-272` publishes `message
DemoteSDKRequest { string session_id = 1; }`; `:94` publishes `rpc UploadFiles(stream UploadChunk) returns
(UploadResponse);`. The code-block walker's switch does dispatch on `json`, `yaml`/`yml`, `go`,
`bash`/`sh`/`shell`, and `sql` and falls through on `protobuf`. — EVIDENCE: docs/api/internal.md:94,
:210-214, :270-272; tests/tier11_docs/code_blocks_test.go:102-116

WATCHOUT: the summary's clause "the only tests naming the page, `tests/tier0_static/fragment_link_test.go`
and `tests/tier0_static/naming_lint_test.go`" is true of `_test.go` files but two registers also name
`docs/api/internal.md`. Neither reads a code block, so the conclusion ("nothing this proposal applies
reddens on account of the page") survives; do not file it, but do not widen the sentence to "nothing in
`tests/` names the page" if it is ever rewritten. — EVIDENCE: tests/registers/residual-reserved-phrases.yaml:153,
tests/registers/identifier-senses.yaml:227

USEFUL [Standing context, "No docs mirror" and "Traps: long lines hide the sentences citations point at"]:
both saved real time. The long-line warning in particular: `spec/04:725`, `:726`, `spec/05:515`, and
`spec/10:60` each render as one line hundreds of columns wide, and `sed -n Np | cut -c1-N` shows none of
the cited clause. Piping a table row through `tr '|' '\n' | tail` recovers it.

UNVERIFIED: nobody on the docs lens has checked whether `docs/getting-started/concepts.md:101` ("A separate
`coordination_generation` counter tracks gateway replica handoffs") needs the per-session qualification
0076 introduced. It states no scope class, so it is not falsified, but it sits beside the two 0076 residues
(`spec/04:712`, `docs/reference/adapter-contract.md:69`) already recorded as unowned, and whoever takes
those should look at it in the same pass.
### [non-spec.1.review-docs-alignment.1]

DECISION: returned an empty findings list for the documentation-alignment lens — BECAUSE no `docs/` page
carries the §4.1 request-message classification, no page states the fence's scope, and the proposal stages
no metric, alert, endpoint, flag, or error-code change, so nothing in `docs/` becomes wrong when SPEC-1,
TEST-1, and TEST-2 apply — ALTERNATIVES: filing the stale `docs/api/internal.md` protobuf excerpts as a
missed edit site, rejected because the page is false against `schemas/lenny-adapter.proto` before anything
staged here applies (guardrail: a doc that already disagrees with the tree is a pre-existing defect, and
the summary already records it with an owner); filing the deferred equal-generation re-fence as a new
operator-facing cause missing from `docs/runbooks/coordinator-handoff-slow.md`, rejected because this
proposal neither creates nor changes that path.

USEFUL [Settled: "No docs mirror. `docs/` names RPCs and never request message types."]: re-verified
independently and it held on every probe. `grep -rn "Request Message Scope\|request-message-scope"` over
the tree outside `proposals/` and `scratchpad/` returns `spec/04_system-components.md:149` alone;
`grep -rn "session-scoped\|pod-scoped" docs/` returns 13 hits, all of them the JSONL frame set, the
`uploadToken`, a metric row, or `docs/runbooks/dual-store-unavailable.md:68`'s unrelated
"gateway-pod-scoped". This entry saved a full docs sweep from being the finding.

FACT: four tier-11 cases carry `// spec: 4.1 (request message scope)` in
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` (`:26`, `:132`, `:408`, `:582`,
`:747`) and that file opens no file under `spec/` at all — `grep -n "04_system-components\|specSection("`
over it returns nothing. A reviewer grepping tests for `4.1` reaches it first and can read it as a tier-11
gate over the block SPEC-1 retires. It is not one; it reconciles `docs/` against `schemas/`. — EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

FACT: the only tier-11 gates that parse `spec/04_system-components.md` resolve `"### 4.7 "` and read a
row's first column, so retiring the §4.1 block cannot redden them.
`specSectionRPCRows` extracts the §4.7 body and `specTableRowNameRE` matches the leading backticked
identifier only; `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` holds the §4.7
`ReportSessionScrub` row against `docs/reference/adapter-contract.md:81` on the shared sentence
"The request is session-scoped: it is addressed by the identifier of the released session and names no
slot." — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:35,44-46;
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-73

FACT: `docs/api/internal.md`'s top-level `string session_id` sites are exactly `:111`, `:147`, `:211`,
`:245`, and `:273`, which is the whole of what a `grep -rn "session_id" docs/` returns for that page. The
summary's inventory of the page (the two excerpt ranges plus `:111`, `:147`, `:245`) covers all five, so
the `DEFERRED [docs/api/internal.md]` entry needs no widening. The page's `UploadFiles` RPC is absent from
`schemas/` entirely (`grep -rn UploadFiles schemas/` is empty) and `service Adapter` /
`service GatewayControl` are at `schemas/lenny-adapter.proto:32` and `:261`, exactly as cited. — EVIDENCE:
docs/api/internal.md:94; schemas/lenny-adapter.proto:32,261

FACT: `docs/reference/error-catalog.md` is client-facing REST/MCP codes and names no adapter gRPC refusal,
so the reclassification importing `spec/05_runtime-registry-and-pool-model.md:515`'s empty-identifier
`InvalidArgument` onto `CoordinatorFence` owes that page no row. `docs/reference/glossary.md` defines no
message-scope term either. Both were checked as candidate missing-companion sites and both are clean. —
EVIDENCE: docs/reference/error-catalog.md:63,133,163

FACT: `docs/testing/` names neither the retiring gate file nor the tier-3 suite TEST-2 amends; its
`domain-suites.md` path list stops at the workspaceplan, oauth, rest, and sdk suites. TEST-1's spec-map
edit therefore has no docs companion. — EVIDENCE: docs/testing/domain-suites.md:24,30,44


### [non-spec.1.review-edit-sites.1]

Edit-site completeness sweep over both staged change files, the checklist, and the summary. Returned zero
findings. Everything below was derived in this firing rather than taken from the standing context.

DECISION: empty findings list — BECAUSE every identifier the proposal adds, changes, or removes was
grepped tree-wide and every surface that names one is already in the six-file `## Files touched on
application` list, or was individually shown to owe no edit — ALTERNATIVES: filing the `addressRuleCases`
incompleteness (the fence becomes a session-addressed message and no row names
`TestCoordinatorFenceRejectsMissingSessionID`), rejected because the inventory's population is transport
legs and named arms rather than every session-scoped message (it omits Interrupt, SendMessage, Attach and
the rest today) and `TestAddressGuardCasesCiteTheSectionStatingTheRule`
(`tests/tier0_static/address_rule_citation_test.go:126-144`) is one-directional; and filing the
`docs/api/internal.md` excerpt enumeration as incomplete, rejected because the summary already says the
page is stale "elsewhere for the same reason" and names the whole page as the remedy.

CORRECTS [Standing context, "Proto anchors, current and verified": "`ReportSessionScrubRequest` `:456`
with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)"]: SPEC-1's citation is
correct and the log entry is the error. `schemas/lenny-adapter.proto:456` is `message
ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`.
Nothing is owed to `spec-changes.md` here. — EVIDENCE: schemas/lenny-adapter.proto:455-460

FACT: the widened tier-3 session set is exactly right, re-derived mechanically rather than by counting.
Parsing the two service blocks gives 31 request types; 25 declare a top-level session address; adding
`CheckpointStart` gives 26 messages in the whole file that declare one. The 18 current
`sessionScopedMessages` keys plus the 8 the proposal adds is exactly that 26-message set, with empty
difference in both directions, and there are zero name/type convention violations anywhere in the file
(no `session_id` of another type, no `SessionId` under another name), including inside `oneof` arms.
Separately, exactly 18 messages declare `reserved "slot_id"` and they are exactly the 18 current map
keys, so `retiredDuplicateNumbers` keeping them verbatim is closed and correct.
(`NegotiateVersionResponse` reserves a bare number 5 and no name, so it is not a member.)
— EVIDENCE: schemas/lenny-adapter.proto, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: restating the shared parse's prose comments owes no `tests/spec-map.json` edit, and the reason is
mechanical rather than a judgement call. `citedSectionsInFile` resolves through `sectionsFromLines`
(`tests/tier0_static/spec_map_slot_address_registration_test.go:681-693`), which reads `// spec:` tags
alone; the `§4.1` at `tests/tier0_static/adapter_proto_parse_test.go:66` is prose, the file carries no
`// spec:` tag and no test function, and `grep adapter_proto_parse tests/spec-map.json` returns nothing.
That is why the credit gate passes today on a `slotAddressCaseFiles` member with no map entry.
— EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:681-693, :793-801

FACT: S1's red-window claim was re-derived, not assumed. Running `messageScopeRow`'s exact regex over
`spec/04_system-components.md` matches 32 rows and all of them are inside `:153-186`, so deleting the
table does leave `parseMessageScopeTable` with zero rows and no other table in the file feeds it.
— EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43, spec/04_system-components.md:153-186

FACT: four meta-gates and registers re-checked clean, each for its own reason, so a later round need not
re-open them. `tests/registers/identifier-senses.yaml`'s eight `spec/04` rows key on the CH-RUNTIMEOPS
retired spellings from the §28.2 naming table (`LifecycleChannel`, `lifecycleChannel`, `lifecycle-socket`,
`@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`), and grepping all six over
`spec/04_system-components.md` returns nothing, so the occurrence indices the register keys on cannot
shift. `successor_pointer_test.go`'s `reducedSections` is a hand list of sections that gave content up to
§28.5 (`:52-56`); §4.1's table is deleted rather than moved, so no successor pointer is owed.
`gate_integrity_test.go`'s `tierZeroGates` (`:63`) names `claim_register_proto_agreement_test.go` /
`TestClaimRegisterAgreesWithTheAdapterProto`, which TEST-1 edits but neither renames nor moves, and does
not name the retiring gate at all. `tests/change-graph.json`, `tests/claim-map.json`, `TEST-GAPS.md` and
`BUILD-GAPS.md` name none of the touched test files or case names.
— EVIDENCE: spec/28_communication-channels.md:154-159, tests/tier11_docs/successor_pointer_test.go:52-56, tests/tier0_static/gate_integrity_test.go:63-70

FACT: `docs/` really does carry no mirror of the §4.1 classification. The only protobuf request-message
declarations anywhere under `docs/` are the four stale excerpts in `docs/api/internal.md`, and the summary
enumerates two of them. The other two are `message StartSessionRequest { string session_id = 1; ... }` at
`:110-111` and `message StopSessionRequest { string session_id = 1; ... }` at `:146-147`. Both carry the
same `string session_id` spelling SPEC-1 makes forbidden, so whoever takes that page owns four excerpts
rather than two. Not filed: the page is false about the shipped proto before anything staged here applies,
and the summary already names the whole page as the remedy.
— EVIDENCE: docs/api/internal.md:110-111, :146-147, :210-211, :272-273

FACT: TEST-1's newest paragraph, the one deleting the §28.5.3 credit, verifies clean in every citation.
`tests/spec-map.json:156` and `:169` are the two section-4.1 entries, `:5670` is the section-28.5.3 entry
(whose `tests` list holds roughly fifty other entries, so the deletion strands no section),
`spec/28_communication-channels.md:501` reads "between the runtime adapter and the runtime binary inside
one agent pod", `:840-841` is the address-equality clause, and the credit doctrine at
`tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` reads as quoted. The kept-path
paragraph's revised arithmetic is also right: `:973`, `:1028`, `:1054` and `:1096` reach `repoFileLines`,
and `:1110` (`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`) reads no file.
— EVIDENCE: tests/spec-map.json:156,169,5670; spec/28_communication-channels.md:501,840-841; tests/tier0_static/spec_map_slot_address_registration_test.go:89-93,973,1028,1054,1096,1108-1131

USEFUL [Standing context, "Do not add `tests/tier0_static/spec_map_slot_address_registration_test.go` to
any files-touched list"]: saved a round. The file names the gate by path at `:336` and five cases range
over that list, which reads as an owed edit until you notice TEST-1 rewrites the gate in place and
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` is one-directional.

USEFUL [Standing context, "Naive proto parsers derail on this file"]: the one-line `message X {}` forms at
`schemas/lenny-adapter.proto:508` and `:984` do break a next-bare-brace parser. Counting braces per line
and stripping `//` comments first gives 88 messages and 31 request types on the first run.
### [non-spec.1.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the staging adds, changes, or
removes was swept against `spec/`, `docs/`, `schemas/`, `charts/`, `tests/registers/`,
`tests/spec-map.json`, `tests/claim-map.json`, `tests/change-graph.json`, and the tier-0/tier-11 gate
files, and every surface that reads one is already in §9 of the staged spec changes. ALTERNATIVES: filing
the bare `§4.2` in `summary.md:81` (the non-goals bullet writes "The §4.2 value rule" where §6 of the
staged spec changes writes "0073's §4.2 value rule", and `spec/04` §4.2 is Session Manager) — declined as
a summary abbreviation of a correct §6 entry, below the bar and outside this lens.

FACT: the eight tier-3 additions verify exactly on the shipped proto. `awk '/^message /{m=$2}
/SessionId session_id/{print m}' schemas/lenny-adapter.proto` returns 26 names: the 18 current
`sessionScopedMessages` keys plus exactly the 8 the staging names. `grep -c 'reserved "slot_id"'` returns
18 and its enclosing-message set is exactly the 18 current keys, so `retiredDuplicateNumbers` keeps the
whole population and the split loses nothing. Every message-open line and closing brace matches the
staged ranges: 341-343, 364-368, 383-385, 403-406, 419-424, 1455-1461, 1538-1549, 1673-1684, each with
`SessionId session_id = 1` on its second line. — EVIDENCE: schemas/lenny-adapter.proto:341,364,383,403,419,1455,1538,1673

FACT: the two `awk` one-liners above are the cheapest correct census on this file and they do not fall
into the naive-parser trap the standing context warns about, because `awk` keys on `^message ` alone and
never tries to bound a body. Use them instead of writing a brace-counting parser. — EVIDENCE: schemas/lenny-adapter.proto

FACT: `tests/registers/identifier-senses.yaml`'s eight `spec/04_system-components.md` rows are inert
today. Their key is the ordinal of a retired CH-RUNTIMEOPS spelling in that file, and
`grep -i 'LifecycleChannel\|lifecycleChannel\|lifecycle-socket\|@lenny-lifecycle\|lifecycle-events'
spec/04_system-components.md` returns nothing, so the file carries zero such sites and SPEC-1's deletion
renumbers nothing. The retired spellings are the four cells at `spec/28_communication-channels.md:154-159`.
— EVIDENCE: tests/registers/identifier-senses.yaml:14-36; spec/28_communication-channels.md:154-159

FACT: `tests/tier0_static/gate_integrity_test.go`'s `tierZeroGates` (`:63-90`) does name
`claim_register_proto_agreement_test.go`, which TEST-1 edits, but it keys on the test function name and
the file basename, both of which TEST-1 keeps, and `unregisteredGates` is one-directional (it reports a
listed gate that is no longer hard-gated, never an unlisted one). The message-scope gate is absent from
the list, so retiring its two case names drags nothing. An edit-sites pass reaching for a tier-0 gate
inventory hits this file first; it owes no edit. — EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-90,247

FACT: `schemas/lenny-adapter-jsonl.schema.json:61` and `:211` are the only `session-scoped` strings under
`schemas/`, and both describe a JSONL frame addressed by `sessionId` on the §28.5.3 boundary. The staged
§4.1 rule quantifies over request messages on the gateway-adapter gRPC leg, so neither is in its
population and neither owes an edit. A `grep -rl 'session-scoped\|pod-scoped' docs/ schemas/ charts/`
returns eleven files and none of them mirrors the §4.1 classification. — EVIDENCE: schemas/lenny-adapter-jsonl.schema.json:61,211

USEFUL [Traps: "Do not add `tests/tier0_static/spec_map_slot_address_registration_test.go` to any
files-touched list"]: saved a filing. The file names both `adapter_proto_message_scope_test.go` (`:336`)
and `adapter_proto_parse_test.go` (`:337`) as literal strings and five cases range over that list, which
reads as a missing edit site until you notice TEST-1 keeps both paths.

USEFUL [Traps: "Do not lean on 'direction survives in §4.7.1's two RPC tables'"]: the second thing this
lens reaches for, and the entry's conclusion held on re-derivation.

USEFUL [Traps: "Bash is the wrong tool for reading this log"]: correct and expensive to relearn. Reading
`## Standing context` with `sed`/`awk` persisted 53KB to a file twice before switching to the Read tool
with an offset on a `/tmp` copy.

### [non-spec.1.review-feasibility.1]

One finding, on the checklist. Everything else under the actor-action lens verified clean.

FACT: the checklist S2 step is stale against the OD4 answer. `...implementation-checklist.md:13-14` still
says `tests/spec-map.json` "re-registers the replacement gate's cases under sections 4.1 and 28.5.3", while
`...non-spec-changes.md:53-56` (rewritten by `[f1.open-decisions.OD4]`) requires `// spec: 4.1` alone,
registration under 4.1 alone, and deletion of the `:5670` entry. The fix stage saw it and filed
`DEFERRED [...implementation-checklist.md]` at review-log `:3259-3263` because the checklist was outside
that pass's editable set. This loop's fix stage can edit it; the finding exists to make it land.
— EVIDENCE: 0075_....implementation-checklist.md:13-14; 0075_....non-spec-changes.md:53-56;
0075_....summary.md:31; review-log.md:3259-3263

CORRECTS [Standing context `### Settled`, "`ReportSessionScrubRequest` `:456` with its address at `:457`
(SPEC-1 cites `:458`, one line off, below the bar)"]: SPEC-1's citation is CORRECT and the standing-context
note is wrong. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is
`SessionId session_id = 2;`. Do not "fix" SPEC-1's `:458` to `:457`; that would introduce a false citation.
— EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the newest TEST-1 text is accurate everywhere I checked. `slotAddressCaseFiles` at
`tests/tier0_static/spec_map_slot_address_registration_test.go:336`; the five ranges at `:973`, `:1028`,
`:1054`, `:1096`, `:1110`; `repoFileLines` at `:699-706`; `:1110`
(`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`) reads no file, the other four do; the credit
doctrine quote at `:89-93`. `spec/28_communication-channels.md:501` and `:840-841` read as quoted.
`tests/spec-map.json` names the retiring gate at `:156`, `:169`, `:5670` and nowhere else in the tree.
Section 28.5.3 keeps ~30 other credited entries, so deleting `:5670` strands no section.

FACT: the derived-set arithmetic re-verified by an independent comment-stripping parser. 31 RPC request
types; 25 declare a top-level `session_id`, all of type `SessionId`; the widened tier-3 set is exactly
those 25 plus `CheckpointStart` = 26; the 18 messages declaring `reserved "slot_id"` are exactly the 18
current `sessionScopedMessages` keys, so `retiredDuplicateNumbers` is a clean split with no member gained
or lost. All 8 additions declare `SessionId session_id = 1` at the top level and no `slot_id`, so both
widened arms are green on the shipped proto.
— EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,1456,1539,1674

FACT: the replacement gate is buildable from the shared parse. `protoFields`
(`tests/tier0_static/adapter_proto_parse_test.go:36-62`) returns names only and folds `oneof` arms into the
enclosing message, and `protoServiceRequests` (`:68-90`) has exactly one caller
(`adapter_proto_message_scope_test.go:87`), `protoFields` exactly one other
(`claim_register_proto_agreement_test.go:64`, lookups at `:72`, `:82`). Adding the type and the `oneof`
flag makes the envelope arm reachable: the arm's captured type name (`CheckpointStart`) is a top-level
message key in the same map, so no descriptor resolution is needed. This answers the standing `Open` bullet
"whether the implementor can build the envelope arm from the shared text parse".

FACT: no gate outside the listed files fires on this change. `spec/04_system-components.md` is read by
`adapter_proto_event_taxonomy_test.go`, `tier10_conformance/adapter_contract_*`, `tier11_docs/
{artifact_register_supersession,relocated_material_pointer,session_scrub_report_addressing_doc_reconciliation,
successor_pointer,credential_delivery_field_locality}_test.go`; `relocatedStatements` and `reducedSections`
are hand lists that name §4.4.3/§5.1/§9.1/§11 and §4.7/§15.4/§29.10 respectively, none of them §4.1. The
§4.7 RPC tables are two-column, so `messageScopeRow` (four columns) cannot match one — S1's red window is
the retiring gate alone, as the checklist states. `tests/registers/`, `tests/change-graph.json`, and
`tests/claim-map.json` name neither the gate nor `CoordinatorFenceRequest`.

WATCHOUT: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
(`tests/tier0_static/spec_map_slot_address_registration_test.go:970-989`) requires EVERY annotated case in
the replacement gate file to carry a per-case spec-map credit for each section it annotates, and the gate
file has no whole-file credit. TEST-1 re-points exactly two entries, so the replacement gate must declare
exactly two test functions, or the implementor must add a spec-map entry per extra function. The staged
text does not say this. Below the bar as a finding (the gate fails loudly and the fix is one JSON line),
but it is the first thing to check if tier 0 reddens in S2.

FACT: I considered and rejected filing that the surviving §4.1 credit is as unearned as the deleted §28.5.3
one, on the argument that the replacement gate reads the proto alone so no §4.1 regression reddens it. It
does not hold: the gate enforces the constraint §4.1 states about the artifact, which is the ordinary
spec-to-test relation, whereas §28.5.3 is the intra-pod JSONL boundary and states nothing about protobuf
field naming. Do not re-derive this; it costs an hour.
### [non-spec.1.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor the staged work names can perform what it is
assigned, and every layer can see the data its check needs (verified mechanically, not by reading prose) —
ALTERNATIVES: I considered filing on (a) the 4.1 credit the replacement gate keeps, under TEST-1's own
"no regression in it would break" doctrine, and (b) the residual escape in the newest TEST-2 paragraph.
Both were rejected on evidence; see the two entries below so nobody re-derives them.

FACT: `schemas/lenny-adapter.proto` declares **no nested messages**. `grep -c "^message \w\+ {"` returns 88
and `grep -c "^\s*message \w\+ {"` also returns 88. This is the fact that makes D2's gate domain ("every
message the protocol declares") fully reachable by the shared parse's column-anchored `protoMessageOpen`
regex — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:22, :34-35

FACT: a from-scratch re-implementation of the derivation rule reproduces the settled census exactly and
finds the gate green on day one. 31 request types; 25 declare a top-level `SessionId session_id`; the 6 that
do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`,
`GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; **zero**
name↔type biconditional violations over all 88 messages including oneof arms; exactly two messages carry a
`oneof` and only `CheckpointRequest` is a request type, with top-level fields `{int64
coordination_generation}` and arms `{CheckpointStart start, CheckpointGrant grant, CheckpointAbort abort}`.
The parse that produces this in one shot: strip `//` to end-of-line, then apply the four regexes at
`tests/tier0_static/adapter_proto_parse_test.go:22-30` with a per-line brace-depth counter and a separate
`oneof \w+ {` depth marker. Do not write a fresh parser; that is trap #93's cost — EVIDENCE:
schemas/lenny-adapter.proto:1173-1187, :1193-1218, :1249-1257

FACT: the gate file's blast radius is exactly four sites and nothing else. A repo-wide grep for
`adapter_proto_message_scope|AdapterProtoRequestMessagesAreClassifiedByScope|MessageScopeGateRefuses` over
`tests/ scripts/ cmd/ pkg/ docs/ spec/`, excluding the file itself, returns `tests/spec-map.json:156`,
`:169`, `:5670` and `tests/tier0_static/spec_map_slot_address_registration_test.go:336`. §9 of the staged
spec changes is complete — EVIDENCE: tests/spec-map.json:156,169,5670

FACT: the shared parse has exactly the two callers the proposal names, confirmed by one grep for every
identifier the parse file exports (`protoFields|protoServiceRequests|braceDelta|adapterProtoPath|
protoMessageOpen|protoRPC|protoServiceOpen`) across `tests/tier0_static/*.go` with the parse file itself
excluded. `protoServiceRequests` → `adapter_proto_message_scope_test.go:87` only; `protoFields` →
`claim_register_proto_agreement_test.go:64` only, with the value read as `fields[msg][field]` at `:72` and
`:82`. 0076's two tier-0 files appear in neither, so the impacts row on 0076 holds — EVIDENCE:
tests/tier0_static/claim_register_proto_agreement_test.go:64, :72, :82

FACT: `validateSpecMapTestFuncs` is confirmed one-directional. It walks `doc.Sections`, splits each entry on
`::`, and reports only entries whose named function is absent from the file; there is no surplus check and
no tier filter. So TEST-1 may keep or change the replacement gate's case names, provided the spec-map edit
lands in the same commit as S2 — EVIDENCE: cmd/lenny-test/cmd_validate.go:956-984

FACT: no gate outside the retiring one reads `spec/04` §4.1's block. The files that read
`04_system-components.md` resolve `#### 4.6.3 ` (`spec_28_register_writers_test.go:759`), `### 4.7 `
(`spec_47_rpc_row_naming_test.go:46`, `session_scrub_report_addressing_doc_reconciliation_test.go:47`), or
§4.7.3 (`adapter_proto_event_taxonomy_test.go:37`). S1's tier-11 listing is right and no tier-11 case
reddens — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:46

WATCHOUT: do not file on the section 4.1 credit the replacement gate keeps. TEST-1's ground for deleting the
§28.5.3 entry is quoted from `spec_map_slot_address_registration_test.go:89-93` ("credits that section with
coverage no regression in it would break"), and it looks symmetric: the replacement gate reads the proto
alone, so no regression in §4.1's *prose* breaks it either. It is not symmetric. §28.5.3's addressing
content is JSON Lines frame equality on a different wire (`spec/28_communication-channels.md:840-841`),
which the gate never touches, whereas §4.1 is where SPEC-1 states the very convention the gate holds over
the proto. Credit follows the behavior a case pins, not the file it opens; on the wide reading no
schema-reading gate could be credited anywhere. Derived and dropped — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:89-93

WATCHOUT: do not file on the residual escape in the newest TEST-2 paragraph. `non-spec-changes.md:103-105`
says the one arm an omission escapes is `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` and adds
that "the wrapper type that name carried is barred" by `TestTheRetiredAddressWrapperIsGone_spec_15_4`. That
case bars the message type `lenny.adapter.v1.SlotId` alone (`:154`), so a later message declaring
`string slot_id = 2` escapes both it and the tier-0 gate. The sentence never claims otherwise — it says the
*type* is barred — and the residual is exactly what OD5 puts to the human. Filing it is hypothetical
hardening on an open decision — EVIDENCE:
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:150-158

FACT: every citation in the newest paragraph (`non-spec-changes.md:95-108`, the only substantive change since
the `spec-r3` snapshot) verifies exactly: `:107-111` presence check, `:112-114` type comparison, `:78-92`
second-address arm, `:32` `retiredFieldName`, `:150-158` wrapper case, and the quoted sentence at
`spec_map_slot_address_registration_test.go:1209-1210`. The eight added members all declare
`SessionId session_id = 1` at the top level and none declares `slot_id`, at :341-343, :364-368, :383-385,
:403-406, :419-424, :1455-1461, :1538-1549, :1673-1684 — every line range in the proposal is exact.

WATCHOUT: the summary cites `pkg/adapter/holdstate.go:335`, `:348` while standing-context trap #126 cites
`:336`, `:349`. Both are correct and neither is a finding: `:335`/`:348` are the two interceptor `func`
declarations, which is what the summary's sentence names ("enforces the hold with pod-level interceptors"),
and `:336`/`:349` are the `s.inHoldState() && !coordinatorHoldAllowedMethods[...]` guards inside them, which
is what the trap's sentence names. Do not "reconcile" them — EVIDENCE: pkg/adapter/holdstate.go:335, :348

USEFUL [Standing context / Traps #93]: the naive-proto-parser trap saved a round. I wrote the census parser
with comment stripping and per-line brace deltas from the start and got 88/31/25 first try.
USEFUL [Standing context / Traps #94]: the long-line trap saved a wrong finding on `spec/04:725`/`:726`.
`awk 'NR==725' | tr '.' '\n'` recovers the scope sentence that a `sed | cut` hides.


### [non-spec.1.review-fresh.1]

DECISION: returned an empty findings list for the fresh-holistic lens over `spec-changes.md` +
`non-spec-changes.md` + checklist + summary read as one document — BECAUSE I independently re-derived the
proto census, the 18-member reserved-`slot_id` set, the 8 tier-3 additions, every spec/04 anchor, every
tier-0 and tier-3 anchor, the spec-map entries, and the whole blast radius of the shared parse, and each
one matched what the proposal states — ALTERNATIVES: filing D2's "an addressing convention that nothing
checks today" (rejected: already filed once and REFUTED by the material skeptic, review-log.md:2161,
:2716); filing the missing "protoFields must keep reporting the same field set" constraint on the §4
IMPLEMENTOR'S CHOICE (rejected: the only claim-register rows over `schemas/lenny-adapter.proto` name
top-level fields alone, so no register row depends on the oneof folding today).

FACT: independent re-derivation of the proto, run today with a comment-stripping brace-depth parser,
reproduces the standing census exactly: 88 messages, 2 services, 31 distinct RPC request types, 25
declaring a top-level `SessionId session_id`, the 6 that do not being `AdapterEventsRequest`,
`CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`,
`ReportPodScrubRequest`; 26 address declarations file-wide with zero name/type violations; `oneof` only in
`CheckpointRequest` (`:1173`) and `CheckpointResponse` (`:1249`). — EVIDENCE: schemas/lenny-adapter.proto

CORRECTS [Standing context, "Proto anchors": "`ReportSessionScrubRequest` `:456` with its address at
`:457` (SPEC-1 cites `:458`, one line off, below the bar)"]: the standing entry is wrong and SPEC-1 is
right. `message ReportSessionScrubRequest {` is line 456, `string pod_id = 1;` is 457, and
`SessionId session_id = 2;` is 458. SPEC-1's `:458` is exact. Do not "fix" it. — EVIDENCE:
schemas/lenny-adapter.proto:456-458

FACT: the tier-3 split arithmetic is exact and re-derivable. The 18 messages declaring `reserved "slot_id"`
are exactly today's `sessionScopedMessages` keys; 17 of them are RPC request types plus `CheckpointStart`;
the 25 address-declaring request types minus those 17 are exactly the 8 messages TEST-2 adds, and no live
`slot_id` field survives anywhere in the file (every hit is inside a `reserved` line or a comment). —
EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the blast radius of the shared parse is closed and matches §9. `protoServiceRequests` has one caller
(`adapter_proto_message_scope_test.go:87`), `protoFields` one other
(`claim_register_proto_agreement_test.go:64`, with reads at `:72`, `:82`, and a third at `:92-93`),
`braceDelta` and `adapterProtoPath` no callers outside those two files plus the parse itself. No file under
`tests/change-graph.json` or `tests/registers/*.yaml` names any touched test file. — EVIDENCE:
grep over tests/ for the four identifiers

FACT: every claim-register row whose surface is `schemas/lenny-adapter.proto` names a top-level field
(`<Message>.coordination_generation`, thirteen rows). None names a `oneof` arm, so the standing Trap that
"`protoFields` folds `oneof` arms in on purpose and the claim-register gate depends on it" is true of the
mechanism but has no live dependent row today. An implementor who splits arm fields out would not redden
that gate on the current register. — EVIDENCE: tests/claim-map.json; tests/tier0_static/claim_register_proto_agreement_test.go:64-100

FACT: no surface outside `spec/04` §4.1 mentions the classification table. `grep -rn "Request Message
Scope"` over the tree returns exactly one hit, the heading itself, and no file links `#request-message-scope`.
`docs/` never names a gRPC request message type; the only `CoordinatorFence` mention is the §4.7 mirror row
at `docs/reference/adapter-contract.md:69`. `schemas/lenny-adapter.proto` carries no scope word in any
comment. §9's file list is complete. — EVIDENCE: spec/04_system-components.md:149; docs/reference/adapter-contract.md:69

FACT: the newest text (the §28.5.3 spec-map deletion, added since snapshot `spec-r3`) verifies clean.
`spec/28_communication-channels.md:501` carries the quoted boundary sentence verbatim, `:840-841` is the
JSON Lines address-equality condition, and the credit doctrine quote is exact at
`tests/tier0_static/spec_map_slot_address_registration_test.go:89-93`. The `slotAddressCaseFiles`
correction is also exact: the five loops are at `:973`, `:1028`, `:1054`, `:1096`, `:1110`, and only the
fifth (`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`) reads no file it names. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:89-93, :336, :973-1110

WATCHOUT: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` resolves a case's demand as the UNION
of its `// spec:` annotation and its `_spec_X_Y` name suffix (`citedSectionsPerCase` →
`citedSectionsPerCaseFromLines`). A replacement gate case named `..._spec_28_5_3` would re-demand the very
credit TEST-1 deletes. Name the replacement cases `_spec_4_1` or leave the suffix off. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:806-812, :970-989

USEFUL [Standing context, Traps]: the "naive proto parsers derail on this file" and "long lines hide the
sentences citations point at" entries each saved a re-derivation dead end; I wrote the brace-depth parser
and used `tr '.' '\n' | tail` on `spec/04:725`, `:726` and `spec/10:60` from the start and every citation
resolved first try.

USEFUL [Standing context, Traps, "MISTAKE: TEST-2 as a map addition could never be applied"]: told me
immediately why `retiredDuplicateNumbers` exists, so I checked the split's arithmetic rather than
re-deriving the unsatisfiable-column argument.

### [non-spec.1.review-fresh.1]

DECISION: returned an empty findings list after re-verifying, by grep rather than by range-print, every
citation in `spec-changes.md` (D1-D6, §3, §4, §5 SPEC-1, §9, §10), `non-spec-changes.md` (§4, §5 TEST-1,
§5 TEST-2 including the paragraph added this round, §8), the checklist, the problem statement, and the
summary's unstaged-defect entries — BECAUSE every anchor resolved and no clause contradicted another.
ALTERNATIVES: I considered filing on (a) the `protoFields` semantic constraint TEST-1 does not restate,
(b) the summary Non-goals bullet writing "the §4.2 value rule" without the 0073 qualifier, and (c) the
tier-3 package doc at `:5-20` not being named as an edit site. All three fail the bar: (a) is bounded by
the spec-side IMPLEMENTOR'S CHOICE and by a loud compile/gate failure, (b) is wording that the summary
qualifies correctly two paragraphs earlier, (c) stays true after the split because "the removed numbers
and names stay reserved" is still pinned, by `retiredDuplicateNumbers`.

FACT: `protoServiceRequests` has exactly one caller in the whole tree and it is the retiring gate file
(`tests/tier0_static/adapter_proto_message_scope_test.go`). If a fixer ever widens the envelope clause
from "request message" to "message", the replacement gate stops needing that function and staticcheck's
`unused` turns tier 0 red on a symbol nobody edited. This is a second, independent reason the standing
context's "do not widen to message" trap holds. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:68;
grep over tests/ pkg/ cmd/ returns no other caller

FACT: nothing outside `tests/spec-map.json` names the retiring gate's two case names. `TEST-GAPS.md`,
`BUILD-GAPS.md`, `tests/change-graph.json`, `tests/claim-map.json`, `tests/registers/`, and `TESTING.md`
all return nothing for `adapter_proto_message_scope_test`, `adapter_proto_parse_test`, or
`session_address_wire_test`. In particular `tests/tier11_docs/test_gaps_test_reference_rename_drift_test.go`
(the TEST-GAPS.md `path::TestName` drift gate) cannot fire on TEST-1's renames. — EVIDENCE:
tests/spec-map.json:156,169,5670 are the only three hits

FACT: the eighteen messages declaring `reserved "slot_id"` in `schemas/lenny-adapter.proto` are exactly
the eighteen current keys of `sessionScopedMessages`, verified by a brace-depth parse that strips comments.
So `retiredDuplicateNumbers`'s stated membership rule is exact, not approximate. — EVIDENCE:
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the addressing convention is green on the shipped proto in both directions. No field named
`session_id` carries a type other than `SessionId` and no field typed `SessionId` carries another name,
anywhere in the file including `oneof` arms. The census re-runs to 31 request types / 25 declaring the
address / the six named in the standing context. — EVIDENCE: schemas/lenny-adapter.proto, whole-file scan

WATCHOUT: `sed -n 'A,Bp' | cat -n | awk` arithmetic over `schemas/lenny-adapter.proto` and
`docs/api/internal.md` gave me an off-by-one on `message SessionId` (it reported `:595`, `grep -n` says
`:596`) and would have led me to file the exact false "one line off" finding the standing context says
cost twenty-five re-derivations. `awk 'NR>=s&&NR<=e'` and `grep -n` are both reliable; the piped-`cat -n`
form is not, because a wrapped or blank line shifts the count. — EVIDENCE: schemas/lenny-adapter.proto:596

USEFUL [Traps: "MISTAKE: a false 'SPEC-1 cites `:458`, one line off' note"]: this entry is what made me
stop and re-derive with `grep -n` instead of filing. It saved a refuted finding outright. Keep it.

USEFUL [Traps: "Cache keys omit `summary.md` and `review-log.md`"]: the cache slot for
`fresh-r1-d91c9fcab1da` was empty, so no lane collision this run, but the note is why I checked the
`coverage` string convention before writing my own.

FACT: the only staged-text change since snapshot `spec-r3` is the eight-line paragraph at
`non-spec-changes.md:95-108` (OD5's answer) plus the summary edits that deleted OD5's entry and widened
the `docs/api/internal.md` inventory. Every citation in both re-verifies: `session_address_wire_test.go`
`:32`, `:78-92`, `:107-111`, `:112-114`, `:150-158`;
`tests/tier0_static/spec_map_slot_address_registration_test.go:1209-1211` quotes verbatim;
`docs/api/internal.md:111`, `:147`, `:245`, `:94`, `:209-215`, `:272-274` all resolve.

### [non-spec.1.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the Kubernetes-idiom lens has no surface on this
proposal. The whole blast radius is `spec/04` §4.1 prose, `tests/tier0_static/adapter_proto_message_scope_test.go`,
`tests/tier0_static/adapter_proto_parse_test.go`, `tests/tier0_static/claim_register_proto_agreement_test.go`,
`tests/spec-map.json`, and `tests/tier3_contract/adapter_session_address/session_address_wire_test.go`.
Nothing staged touches a CRD, a status subresource, a field manager, a finalizer, an admission webhook, a
reconcile loop, or the apiserver — ALTERNATIVES: I looked for a controller or CRD consumer of the §4.1
classification and there is none (see FACT below), and for a synchronous path newly made to depend on a
reconcile or on leader election, and the proposal introduces no runtime behavior at all.

FACT: the §4.1 request-message scope classification has no consumer in `pkg/` or `cmd/` outside the tests
this proposal names. `grep -rn "session-scoped\|pod-scoped" --include=*.go pkg/ cmd/` returns only doc
comments in gateway subsystems (billing, transcripts, mcpfabric, admission data-residency validator) and
`pkg/gateway/podlifecycle/podclaim/claimer.go:153`; none reads the table, none is a controller keying off
the class, and no CRD type or status field carries a message scope. So retiring the table cannot move a
controller, a status write, or an admission decision. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/claimer.go:153

FACT: `grep -niE "crd|controller|reconcil|finaliz|admission|webhook|apiserver|kubernetes|etcd|leader|field manager|lease"`
over all five staged proposal files returns only the word "reconcile/reconciliation" used for the retiring
tier-0 gate reconciling a markdown table against a `.proto` file, plus one "relinquishes the lease" inside
the unstaged coordfence defect record in the summary. That lease is the gateway's own coordination lease in
`pkg/gateway/coordination/coordfence`, not a `coordination.k8s.io` Lease, and the proposal stages no repair
for it. No K8s-idiom reading of any staged sentence exists. — EVIDENCE:
0075_....summary.md:178, pkg/gateway/coordination/coordfence/coordfence.go:60-63

FACT: the component attribution the proposal relies on is correct and does not cross a posture boundary.
`ReportPodScrub` is an adapter-initiated RPC on `service GatewayControl`, so the agent pod dials the
gateway and never the apiserver, which is what §10.3's zero-RBAC posture requires; `ReportPodScrubRequest`
declares `string pod_id = 1`, `PodScrubOutcome outcome = 2`, `string detail = 3` and no address, matching
what §5 of the staged spec changes says about `spec/04:726`. — EVIDENCE: schemas/lenny-adapter.proto:261,
:334, :499-503; spec/04_system-components.md:186 (the `ReportPodScrubRequest` table row, `GatewayControl`,
adapter → gateway, pod)

FACT: spot-checked four citations that a K8s-adjacent reading would rest on, all sound.
`spec/10_gateway-internals.md:57` is the hold-state-semantics bullet naming `CoordinatorFence` as the only
exit from hold state; `:60` ends with "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain
pod-scoped"; `spec/28_communication-channels.md:501` is "between the runtime adapter and the runtime binary
inside one agent pod"; `:840-841` is the JSON Lines address-equality condition. — EVIDENCE:
spec/10_gateway-internals.md:57, :60; spec/28_communication-channels.md:501, :840-841

WATCHOUT: `sed -n '60p' spec/10_gateway-internals.md` truncates in any terminal — the pod-scoped clause is
the LAST sentence of a very long single line. Pipe through `tr '.' '\n' | tail -6` to see it. This is the
same long-line trap the standing context records for `spec/05:515` and `spec/04:725`/`:726`, and it applies
to `spec/10:60` as well. — EVIDENCE: spec/10_gateway-internals.md:60

USEFUL [Standing context / Traps, "Cache keys omit `summary.md` and `review-log.md`"]: I checked the cache
key before doing any work and it was a miss, so the entry cost me nothing this round, but it is the reason
I did not treat an absent hit as suspicious. Keep it.
### [non-spec.1.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE this proposal's entire staged surface is (a) prose in `spec/04` §4.1 classifying gRPC request messages on the gateway-adapter protocol, (b) a tier-0 static gate that parses `schemas/lenny-adapter.proto` as text, and (c) a tier-3 contract suite that reads generated protoreflect descriptors. Nothing staged reads or writes the apiserver, declares or mutates a CRD spec/status subresource, adds a finalizer, touches an admission webhook, or puts a controller reconcile on a synchronous request path. There is no Kubernetes-idiom surface to judge. ALTERNATIVES: I considered whether the reclassification of `CoordinatorFenceRequest` to session scope carries a Kubernetes-idiom consequence (fencing token / lease handoff); it does not — the coordination generation lives on the adapter's in-process slot registry entry (`pkg/adapter/slot.go:59`) and the gateway-side lease is the coordination lease in `pkg/gateway/coordination/coordfence`, not a `coordination.k8s.io/v1` Lease.

FACT: the "lease" the summary's recorded out-of-scope defect names is `Fencer.relinquish`, a coordination-lease release inside the gateway process, and the stale arm the summary cites is exact: `pkg/gateway/coordination/coordfence/coordfence.go:171-179` is the `FailedPrecondition`/`!res.Accepted` case that re-reads `CoordinationGeneration` and, on no advance, logs "stale fence with no generation advance; relinquishing" and calls `f.relinquish`. No Kubernetes object is involved, so a k8s-idiom lens has nothing to say about that entry either. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:164-179

FACT: the newest text in the non-spec staging (the "The session set stays hand-entered" paragraph added since the `spec-r3` snapshot) verifies clean on every citation I checked: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:107-111` is the `session_id` presence check, `:112-114` the `lenny.adapter.v1.SessionId` type comparison, `:78-92` `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1`, `:32` the `retiredFieldName = "slot_id"` const, `:150-158` `TestTheRetiredAddressWrapperIsGone_spec_15_4`, and `tests/tier0_static/spec_map_slot_address_registration_test.go:1209-1211` carries the quoted "Neither rule reconstructs the whole inventory..." sentence verbatim. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:107, tests/tier0_static/spec_map_slot_address_registration_test.go:1209

CORRECTS [orchestrator brief]: the brief says OD5 "stays open" in the summary. It does not: the diff against `scratchpad/cp-snap/.../spec-r3` shows the whole `**OD5.**` block deleted from `summary.md` and its substance moved into `non-spec-changes.md` §4 as the accepted-residual paragraph described above. The review log's `### Open` section still lists OD5 as the human's, so log and summary now disagree. Not filed as a finding (framing of open decisions is outside a reviewer's remit), but the next round should not assume OD5 is still a summary section.

WATCHOUT: `cat`/`sed`/`awk` over `review-log.md` in Bash persists to a file instead of printing once output exceeds ~2KB, and the standing context's lines are 1-3KB each, so a range read costs two calls per attempt. Reading it in 20-25 line windows without `fold` was what finally worked. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:93

### [non-spec.1.review-mechanism.4]

DECISION: filed two findings, both the unswept residue of this round's OD4 apply, and returned nothing else — BECAUSE the OD4 fix ([f1.open-decisions.OD4]) rewrote `non-spec-changes.md` TEST-1 to "section 4.1 alone / delete `:5670`" and updated `summary.md:31` and the 0073 impacts row, but left the `## Deliverable index` TEST-1 bullet (`summary.md:274-275`, "under the sections the retiring gate's cases held") and the checklist S2 line (`implementation-checklist.md:13-14`, "under sections 4.1 and 28.5.3") saying the opposite — ALTERNATIVES: filing the mechanically-evaluable-`oneof` over-capture (a future request message using a `oneof` for non-stream alternatives would be read as a stream envelope and refused); rejected as hypothetical hardening, since `CheckpointRequest` and `CheckpointResponse` are the only `oneof` blocks in the proto and `spec-changes.md` §4 says so explicitly.

FACT: the round's snapshot diff is NOT empty this time, contrary to the standing-context warning. `diff -u spec-r3/...summary.md` and `.../non-spec-changes.md` both show real hunks; the checklist diff is empty. So the OD4 apply is visible and its unswept sites are provable from the diff alone. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3/

FACT: the whole tier-3 TEST-2 arithmetic was re-derived mechanically and every number in the staging holds. A brace-counting parse of `schemas/lenny-adapter.proto` gives 31 distinct RPC request types, 25 declaring a top-level `SessionId session_id`, 18 messages declaring `reserved "slot_id"` (exactly today's `sessionScopedMessages` keys), and 26 `SessionId session_id` declarations in the whole file with zero name/type convention violations. The 8 additions TEST-2 names are exactly `addr_request_types \ current_17`, and each declares `SessionId session_id = 1` at the top level with no field named `slot_id` (only `reserved` and comments carry that string). The replacement gate is green on the shipped proto on day one. — EVIDENCE: schemas/lenny-adapter.proto:341-343, :364-368, :383-385, :403-406, :419-424, :1455-1461, :1538-1549, :1673-1684

FACT: no cross-file reader of the tier-3 declarations exists. `sessionScopedMessages`, `retiredFieldName`, `retiredWrapperName`, and `messageDescriptors` are read only inside `session_address_wire_test.go`; the sibling `send_message_stamp_test.go` touches none of them, and `reservesNumber`/`reservesName` are re-declared per package in three other tier-3 directories. Changing the map to a `[]string` cannot break a sibling file. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:31-67, :160-175

FACT: `address_rule_citation_test.go` names only the tier-3 file (`addressRuleCases:45-47`), never the tier-0 gate, so TEST-1's case renames drag no hand inventory. The only outside reader of the gate's path is `slotAddressCaseFiles` (`spec_map_slot_address_registration_test.go:336`), and TEST-1's kept-path argument is now correct on the 4-of-5 count. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:29-47, tests/tier0_static/spec_map_slot_address_registration_test.go:970-1131

FACT: deleting `tests/spec-map.json:5670` is safe on coverage grounds. Section 28.5.3 carries 58 entries and section 4.1 carries 19; `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` reports missing credits only, never surplus, so the annotation and the map entry must move together or the surplus stands uncaught. — EVIDENCE: tests/spec-map.json:156, :169, :5670; tests/tier0_static/spec_map_slot_address_registration_test.go:970-989

FACT: TEST-1's closing sentence ("every case in the file stays credited to each section its own `// spec:` annotation names") is load-bearing and correct. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` iterates `citedSectionsPerCase` for a file that has any per-case entry, so a replacement gate carrying a THIRD case with no spec-map row reddens tier 0. The staging names only two re-pointed entries but the closing sentence states the general invariant, so this is not a gap. Do not file it. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:970-988

FACT: no spec/, docs/, or schema surface outside §4.1 references the retired table. `grep` for "message-scope|message scope|Request Message Scope|classification table" across `spec/`, `docs/`, and `tests/` returns only §4.1's own heading, the two parse/gate comments TEST-1 already restates, the tier-3 comment TEST-2 already restates, and unrelated `// spec: 4.1 (request message scope)` annotations that read nothing. `successor_pointer_test.go`'s `reducedSections` is a hand list of §4.7/§15.4/§29.10 and §4.1 cannot enter it, so no successor pointer is owed for the deleted block. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:49-56

WATCHOUT: `message SessionId` is at `schemas/lenny-adapter.proto:595`, not `:596`. The standing context's "Proto anchors, current and verified" bullet says `:596` and the problem statement cites `:596`; `:596` is `string value = 1;`. Judged below the bar and not filed, but do not "confirm" `:596` from the log.
EVIDENCE: schemas/lenny-adapter.proto:594-597

WATCHOUT: `repoFileLines` is at `tests/tier0_static/spec_map_slot_address_registration_test.go:702-710`; `non-spec-changes.md` cites `:699-706`, which starts on `testFuncRE`. Overlapping and below the bar; not filed.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:699-710

USEFUL [Standing context / Traps]: the brace-counting warning saved the parse outright — a naive regex parser on this file mis-attributes `reserved "slot_id"` because `message SendMessageResponse {}` and `message ReportPodScrubResponse {}` open and close on one line. Writing the parse with per-line brace deltas first gave the right census on the first run.

### [non-spec.1.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every mechanism the staged pair describes traces end
to end without a break: the derivation rule's population, the four gate clauses, the S1→S2→S3 red window,
the spec-map re-registration, and the tier-3 split all reconcile against the tree. ALTERNATIVES: I held
three candidates and dropped each on the bar. (1) §3's design-overview sentence states the replacement
gate as "the session address is spelled one way in both directions and that a stream envelope carries
exactly one addressing frame", dropping the "envelope declares no address of its own" conjunct that D2,
the staged spec paragraph, and §8 all carry — a one-sentence overview compression that makes no applied
text wrong, and the summary's own "What changes" bullet carries all four conjuncts. (2) TEST-1 orders the
`protoServiceRequests` doc comment (`tests/tier0_static/adapter_proto_parse_test.go:64-67`) "restated as
the addressing-convention gate", but that comment's second sentence justifies the parse's
service-awareness by "the §4.1 table names the service on every row", and the replacement gate needs the
request-type SET and not the service VALUE, so no truthful restatement of that clause exists — a doc
comment, self-catching, over-specification to file. (3) The number of test functions the replacement gate
declares is unstated while TEST-1 re-points exactly two spec-map entries; a third case would be caught
loudly by `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
(`tests/tier0_static/spec_map_slot_address_registration_test.go:970-989`), so it is self-catching.

FACT: the whole census and the TEST-2 split arithmetic reproduce exactly, by an independent
comment-stripping brace-depth parser: 31 RPC request types, 25 declaring a top-level `SessionId
session_id`, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`,
`GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); exactly 18
messages declare `reserved "slot_id"` and they are exactly the 18 keys of `sessionScopedMessages` today;
the widened set (18 + the 8 additions = 26) is EQUAL as a set to (the 25 address-declaring request types
+ `CheckpointStart`). Set difference in both directions is empty. — EVIDENCE:
schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the addressing convention holds in both directions on the shipped proto by direct grep, so the
replacement gate is green on day one: 26 fields of type `SessionId`, every one named `session_id`, and no
field named `session_id` of any other type. — EVIDENCE: `grep -n "SessionId \w\+ *=" schemas/lenny-adapter.proto`
returns 26 lines, all `SessionId session_id`; the inverse grep returns nothing.

FACT: `spec/` DOES name test tiers today, which closes the standing context's first UNVERIFIED item.
`spec/18_build-sequence.md:85` reads "Tier 0 (Static) and Tier 11 (Documentation) pass on the empty
repository", `:98` "Tier 0 (Static) passes", and `:244`, `:281`, `:349` name Tier 2/3/4/5 suites in exit
criteria. The five later rounds that grepped and reported nothing were grepping the lowercase hyphenated
spelling `tier-0`, which `spec/` does not use; the spelling in `spec/` is `Tier N` capitalised. Both
readings are reconcilable and OD6's "yes" stands on the capitalised form. — EVIDENCE:
spec/18_build-sequence.md:85, :98, :244

USEFUL [Traps: "Bash is the wrong tool for reading this log"]: exactly right, and it cost me three wasted
calls before I switched. `awk`/`sed`/`cat` over the standing context persisted to a file every time, and
re-reading that file persisted again. The Read tool with `limit 172` returned the whole section in one
call. Do this first.

USEFUL [Traps: "Naive proto parsers derail on this file"]: I wrote the parser the trap prescribes
(strip `//` comments, per-line brace delta, handle the same-line close) on the first attempt and it
returned 31/25/6 and the 18 reserved messages with no correction pass. The trap saved a round.

USEFUL [Settled: "Proto anchors, current and verified" and "spec/04 §4.1 anchors"]: I spot-checked
`:1455-1461`, `:1538-1549`, `:1673-1684`, `:341-343`, `:364-368`, `:383-385`, `:403-406`, `:419-424`,
`spec/04:149/:151/:153-186/:175/:188/:190/:192`, `tests/spec-map.json:156/:169/:5670`,
`spec/28:501` and `:840-841`, and `spec_map_slot_address_registration_test.go:89-93/:336/:337/:699/:776/:797/:973/:1028/:1054/:1096/:1110/:1209-1211/:1212`.
Every one is exact. Do not re-derive; spot-check two and move on.

WATCHOUT: `sed -n '85,95p;695,710p;774,806p' <file>` emits the three blocks in FILE order with no
separator between them, so the first block you read is the lowest-numbered range rather than the first one
you wrote. I mis-attributed `repoFileLines` to line 89 for a minute on exactly this. The trap is already
in the standing context under the `sed` MISTAKE entry; it bites on multi-range prints too, not only on
mis-mapped anchors. Use one range per call, or `grep -n` for the declaration. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:92 (the credit-doctrine quote) versus :699
(`func repoFileLines`)

FACT: nothing outside `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` reads
`sessionScopedMessages`; a repo-wide scoped grep over `pkg cmd tests sdks schemas charts scripts docs spec`
returns 5 hits, all in that one file. And `adapter_proto_message_scope_test.go`, its two case names, and
`adapter_proto_parse_test.go` appear in none of `tests/change-graph.json`, `tests/claim-map.json`,
`tests/registers/*.yaml`, `TESTING.md`, `BUILD-GAPS.md`, or `TEST-GAPS.md`. The blast radius §9 states is
closed under both directions.

### [non-spec.1.review-operational.1]

MISTAKE: the OD4 answer ("drop the §28.5.3 credit") was applied to `non-spec-changes.md` §5 TEST-1 and to
the summary's `## Summary` bullet, but not to the two other places that state the same thing. The
implementation checklist S2 still reads "`tests/spec-map.json` re-registers the replacement gate's cases
under sections 4.1 and 28.5.3" and the summary's deliverable index still reads "re-registers the
replacement gate's cases under the sections the retiring gate's cases held". An implementor works from the
checklist. Filed as this run's one finding.
— EVIDENCE: 0075_....implementation-checklist.md:13-14, 0075_....summary.md:274-275 against
0075_....non-spec-changes.md:53-55.

WATCHOUT: `spec-changes.md` §9's `tests/spec-map.json` bullet says the file "is re-pointed at the
replacement gate's cases in the same change", which does not name 28.5.3 and so is only loose rather than
contradictory. If a fixer touches the checklist and the deliverable index, tighten this one in the same
pass so the three carriers state the same disposition. — EVIDENCE:
0075_....spec-changes.md:164-165

FACT: deleting `tests/spec-map.json:5670` leaves section 28.5.3 with 57 remaining credited tests and no
tier-0 credit at all. Nothing in the tree requires a per-tier credit per section, so the deletion reddens
nothing. Derived by loading `tests/spec-map.json` and reading `sections["28.5.3"].tests`. — EVIDENCE:
tests/spec-map.json:5660-5685

FACT: no observability surface moves with this proposal. `lenny_adapter_coordinator_hold`
(`spec/16_observability.md:185`, `docs/reference/metrics.md:309`),
`lenny_coordinator_handoff_stale_total` (`spec/16:183`, `docs/reference/metrics.md:307`), and the
`CoordinatorHandoffSlow` alert (`spec/16:552`, `pkg/alerting/rules/rules.go:1583`) carry no scope label and
no message-scope dependency, and `docs/runbooks/coordinator-handoff-slow.md` names neither the classification
nor the fence's address. The one runbook and the one alert on this surface are untouched by SPEC-1.

FACT: no `docs/` page restates the gRPC request-message scope classification. `grep -rn
"session-scoped\|pod-scoped" docs/` returns only JSONL frame addressing, upload tokens, credential leases,
and `docs/reference/adapter-contract.md:81`'s `ReportSessionScrub` row, which SPEC-1 keeps and a tier-11
gate pins. This independently re-derives the standing "No docs mirror" entry.
— EVIDENCE: docs/reference/adapter-contract.md:81,
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: every operational citation in the summary's `## Defects in the shipped tree` section verifies exactly
as written: `spec/10_gateway-internals.md:39` (same-generation retry, 3 attempts, 1-second backoff), `:57`
(fence is the only hold exit, `UNAVAILABLE` + `coordinator_hold`), `:60` (the hold and its
`lenny_adapter_coordinator_hold` gauge stay pod-scoped), `pkg/adapter/coordination.go:109-111`, `:127-134`,
`:150-156`, `pkg/adapter/holdstate.go:335`, `:348`,
`pkg/gateway/coordination/coordfence/coordfence.go:52`, `:171-179`, `:180-183` (and the package's only
non-test file carries no timer, sleep, or backoff), `spec/04_system-components.md:712`, `:725`, `:726`,
`docs/reference/adapter-contract.md:69`, `docs/api/internal.md:94`, `:209-215`, `:272-274`,
`tests/tier11_docs/code_blocks_test.go:103-113`, `tests/tier11_docs/spec_47_rpc_row_naming_test.go:35`,
`proposals/0073_...md:6653-6655` and `:6686`, and 0080's §1.1-§1.21 inventory with §1.16 not taking the
missing backoff. Spot-check rather than re-derive.

USEFUL [Traps, "Cache keys omit `summary.md` and `review-log.md`"]: this lens's two earlier "clean" passes
were one cache hit, so the summary had not actually been read under this lens. Reading it is where the
finding came from. The trap is real and worth keeping.

USEFUL [Traps, "`sed` with several `p` ranges emits lines in file order"]: hit it on the first
`docs/api/internal.md` read; the entry saved the mis-mapping.
### [non-spec.1.review-operational.1]

DECISION: returned an empty findings list — BECAUSE the proposal declares no CRD condition, emits no metric, defines no alert rule, and edits no reader-facing page, and every operational citation it does make resolves against the tree. ALTERNATIVES: I considered filing on the runbook/metric label mismatch I found (below) and on the `docs/api/internal.md` staleness, and rejected both: neither is made wrong by anything this proposal applies, which is the bar.

FACT: this lens has essentially no surface on this proposal, and the sweeps that establish that are cheap and repeatable. `grep -rn "session-scoped\|pod-scoped" docs/` returns exactly one gRPC-request-scope sentence, `docs/reference/adapter-contract.md:81` (the `ReportSessionScrub` row that SPEC-1 explicitly preserves and a tier-11 gate pins); every other hit is JSONL-frame prose on the runtime leg or an unrelated token/path use. `grep -rn "pod-scoped" spec/` returns six lines: `spec/04:151` and `:188` (both retired by SPEC-1), `spec/04:726` (kept, agrees with the derivation), `spec/10:60`, `spec/04:872`, `spec/12:202`. Nothing else in `spec/` or `docs/` classifies a request message. — EVIDENCE: docs/reference/adapter-contract.md:81; spec/04_system-components.md:726; spec/10_gateway-internals.md:60

FACT: **no tier-11 case reads `spec/04` §4.1, so retiring the block cannot redden tier 11.** `grep -n 'specSection(.*04_system' tests/tier11_docs/*.go` returns nine call sites and every one names `### 4.4 `, `#### 4.6.1 `, `#### 4.6.3 `, or `### 4.7 `. This is the mechanical version of the Traps entry that declines the five `// spec: 4.1 (request message scope)` annotations in `basic_level_echo_stamp_doc_reconciliation_test.go`, and it is one grep rather than a file read. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:47; tests/tier11_docs/spec_47_rpc_row_naming_test.go:46; tests/tier11_docs/eviction_coordinator_route_consistency_test.go:64,89

FACT: the operational anchors the summary's unstaged-defect entries rest on all verify, including the ones an operational lens is most likely to doubt. `spec/10:57` carries both the "only way to exit hold state" clause and the `UNAVAILABLE` + `coordinator_hold` rejection; `spec/10:60` ends with "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; `spec/28:314-317` carries the per-session recording clause and `:322` the pod-wide precondition clause; `pkg/adapter/holdstate.go:335` and `:348` are the two interceptor declarations, both gating on `s.inHoldState()` with a method allowlist. — EVIDENCE: spec/10_gateway-internals.md:57,60; spec/28_communication-channels.md:314-317,322; pkg/adapter/holdstate.go:335,348

FACT: the `coordfence` stale arm the summary's fence-retry defect entry relies on is reached by a `FailedPrecondition` error, not only by `Accepted == false`. The switch case is `case ferr != nil && status.Code(ferr) == codes.FailedPrecondition, ferr == nil && !res.Accepted:`. A reader who checks only the `!res.Accepted` half concludes the adapter's `status.Errorf(codes.FailedPrecondition, "coordinator_handoff_stale: ...")` misses the arm and that the summary's "refused as stale on the second attempt" is wrong. It is not wrong. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:163-164; pkg/adapter/coordination.go:127-134

WATCHOUT: `docs/runbooks/coordinator-handoff-slow.md` is about **delegation** handoff (parent session passing control of a delegated child), not about the `CoordinatorFence` coordinator handoff. Its step-1 query groups `lenny_coordinator_handoff_duration_seconds` by `phase` (`claim`, `materialize`, `warmup`, `attach`) while `docs/reference/metrics.md:310` gives that histogram's labels as `pool` and `outcome`. That is a live pre-existing runbook/metric-label mismatch, it is on a mechanism this proposal does not touch, and it is not in 0080's inventory. It is a residue-register candidate, not a finding against 0075. Do not file it here and do not let it pull the fence-retry defect entry wider. — EVIDENCE: docs/runbooks/coordinator-handoff-slow.md:38-40; docs/reference/metrics.md:310

FACT: the summary's claim that `tests/tier0_static/fragment_link_test.go` and `tests/tier0_static/naming_lint_test.go` are "the only tests naming the page" (`docs/api/internal.md`) is accurate as written — two register YAMLs, `tests/registers/residual-reserved-phrases.yaml` and `tests/registers/identifier-senses.yaml`, also name it, but registers are gate inputs rather than tests and neither is disturbed by a proposal that edits no docs page. Examined and declined; do not re-file as a false citation. — EVIDENCE: tests/registers/residual-reserved-phrases.yaml; tests/registers/identifier-senses.yaml

USEFUL [Traps: "Long lines hide the sentences citations point at"]: this saved a wrong finding on `spec/10:60`. The "hold and its gauge remain pod-scoped" clause is the last sentence of a ~1200-character line; a `sed -n '60p' | cut -c1-200` shows only the gauge emission and reads as though the pod-scope clause is absent. `grep -n "remain pod-scoped"` is the reliable anchor. The same shape applies to `spec/04:725`/`:726` and `spec/28:322`.

USEFUL [Traps: "Bash is the wrong tool for reading this log"]: confirmed the hard way. Four attempts to read the Standing context with `sed`/`awk`/`cut` were all persisted to a tool-results file instead of printed, costing four calls. `Read` with `offset: 93, limit: 80` printed the whole Traps section in one call. Use `grep -n '^### '` on the log to get the section offsets, then `Read`.

### [non-spec.1.review-performance.6]

DECISION: returned an empty findings list, which is the sixth consecutive empty pass for this lens on this
proposal — BECAUSE the only proposal text that has changed since the `spec-r3` snapshot is TEST-1's
spec-map paragraph in `non-spec-changes.md` (the `slotAddressCaseFiles` five-vs-four correction and the
decision to delete `tests/spec-map.json:5670` rather than re-point it), and neither touches a store, an
emission site, a per-request or per-session write, a watch, a lease, a queue, or a retry policy. There is
no top-tier arithmetic to state because the numerator is still zero: §9's files-touched list is
`spec/04_system-components.md`, three tier-0 test files, `tests/spec-map.json`, and one tier-3 test file,
with no `pkg/`, `cmd/`, `charts/`, or `schemas/` entry. ALTERNATIVES: re-filing the coordfence
same-generation-retry refusal and the missing 1-second backoff (both pre-existing, both recorded in
`summary.md` under `## Defects in the shipped tree that this proposal does not stage`, owner named as 0080
§1.16); re-filing the session-scoped fence as the only exit from a pod-wide hold (standing-context Trap,
declined by six lenses). — EVIDENCE: proposals/0075_.../0075_....spec-changes.md §9

FACT: deleting the `tests/spec-map.json:5670` credit cannot leave section 28.5.3 uncovered, so the newest
paragraph creates no gate-firing risk. The `28.5.3` section object in `tests/spec-map.json` carries a long
`tests` array including `pkg/adapter/slotframe_test.go`, `pkg/outputtype/outputtype_test.go`,
`tests/tier3_contract/adapter_jsonl/messages_test.go`, and four named cases in
`tests/tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go`. Removing one entry leaves the
section credited many times over. A future lens worried that the deletion strands a section can stop here.
— EVIDENCE: tests/spec-map.json, section key `28.5.3`

FACT: re-verified independently today rather than replayed from the log — `pkg/adapter/coordination.go:107`
is the handler, `:109-111` is the empty-session-id `InvalidArgument` performed before `boundSlotState`
(`:116`), and `spec/05_runtime-registry-and-pool-model.md:515` (one very long line; the clause is the
second-to-last sentence, recovered with `tr '.' '\n' | tail -5`) is the refusal the reclassification adds.
The one obligation the widened session class carries is already met in the tree, so no shipped failure mode
degrades. — EVIDENCE: pkg/adapter/coordination.go:109-111, spec/05_runtime-registry-and-pool-model.md:515

USEFUL [non-spec.5.review-performance.1]: its set-theory sentence (the derived session class is a strict
superset of the retired table's, so the `spec/05:515` refusal can only be added and never removed) is the
whole reliability argument in one line and it re-verified unchanged. A seventh pass should spot-check that
sentence and the files-touched list and stop, rather than rebuilding the coordfence control-flow trace.

USEFUL [standing-context Traps]: "Long lines hide the sentences citations point at" was again the only
thing standing between a correct citation and a false finding on `spec/05:515`.

UNVERIFIED: still nobody has run tier 0 or tier 3 on this branch. Every green-on-day-one claim in this log,
mine included, is derived from reading files.
### [non-spec.1.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the whole staged change is spec prose plus two test files, with no proto, handler, controller, chart, alert, or metric edit, so there is no per-task/per-request/per-session write onto etcd, Postgres, or Redis to quantify, no net-new informer or watch, no hot key, and no work-queue or reconcile pressure created at any tier. The quantitative half of this lens has no subject on 0075. — ALTERNATIVES: filing the `spec/10:39` retry defect (adapter refuses the same-generation retry it orders; `pkg/gateway/coordination/coordfence/coordfence.go` applies no backoff) was rejected because it is pre-existing, already recorded in the summary's unstaged-defects section, and was filed once by `spec.4.review-reliability.1`; refiling costs two verifiers for a known open item.

FACT: nothing in `pkg/` or `cmd/` consumes the §4.1 classification. `grep -rn "session-scoped\|pod-scoped" --include=*.go pkg/ cmd/` returns only prose doc comments about resource lifetimes and contexts (e.g. `pkg/gateway/environment/transcriptstore/transcriptstore.go:73`, `pkg/admission/data_residency_validator/validator.go:89`), none of which reads the table or the derivation rule. `grep -rn "message scope\|message_scope" spec/ docs/ charts/ pkg/alerting/` returns nothing outside `spec/04`. So retiring the table has no runtime, metric, or alert-catalog consumer to break, and a performance/reliability lens has nothing to trace through §12.4 durable-fallback territory. — EVIDENCE: pkg/gateway/environment/transcriptstore/transcriptstore.go:73

FACT: the two fail-closed refusals this proposal's prose leans on both verify exactly at the cited anchors, and both are in-process argument checks with no store read behind them, so neither degrades under a Postgres failover, a Redis reset, or a coordinator handoff. `pkg/adapter/checkpoint.go:74-84` is the opening-frame empty-and-unsafe address refusal (`sessionID := start.GetSessionId().GetValue()` at `:81`, the `InvalidArgument` at `:83`), and `pkg/adapter/coordination.go:107` is the handler with the empty-identifier refusal at `:109-111` and `boundSlotState` at `:116`. — EVIDENCE: pkg/adapter/checkpoint.go:81, pkg/adapter/coordination.go:109

USEFUL [Standing context / Traps / cache keys omit summary.md]: the cache slot for `performance-r1-<hash>` was empty, but the entry is why I checked the `coverage` string convention before writing. The key is `md5(spec-changes + non-spec-changes + implementation-checklist)`; this round's is `d91c9fcab1da`.

USEFUL [Standing context / Traps / Bash is the wrong tool for reading this log]: reading the standing context with `sed -n 'A,Bp' | cut -c1-2200` in 30-50 line windows is what worked; an unclipped `awk`/`sed` over the section persists to a file and costs a second call. Two calls were lost before clipping.

FACT: the only change to the staged text since the `spec-r3` snapshot is one new paragraph in `non-spec-changes.md` §5 TEST-2 ("The session set stays hand-entered...", the OD5 residual argument) and, in the summary, the deletion of the OD5 entry plus a widened `docs/api/internal.md` site list. Every line citation in that new paragraph verifies against the tier-3 file: `:32` `const retiredFieldName = "slot_id"`, `:78` and `:99` the two address arms, loops at `:81` and `:102`, presence check `:107-111`, type comparison `:112-114`, `:130` the reservation loop, `:150` the wrapper case. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:112

WATCHOUT: the new paragraph's mitigation sentence says "the wrapper type that name carried is barred from the protocol definition outright by `TestTheRetiredAddressWrapperIsGone_spec_15_4`". That case bars the message type `lenny.adapter.v1.SlotId` and does not bar a future field *named* `slot_id` of some other type; neither does D2's replacement gate, whose biconditional is over `session_id`/`SessionId` alone. The sentence is literally true as written (it claims the type is barred, not the name), so it is not a finding, but a reader looking for a stronger claim there will not find one. Do not file it as a false citation; check the two halves apart. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:150-158

### [non-spec.1.review-reliability.4]

Empty findings. The proposal changes no runtime path, and every reliability-adjacent citation it carries
was re-verified against the tree in this firing and holds.

FACT: the summary's `## Defects in the shipped tree that this proposal does not stage` entry on the
equal-generation re-fence is accurate in every anchor. `spec/10_gateway-internals.md:39` orders the retry
"with the same generation value (up to 3 attempts with 1-second backoff)";
`pkg/adapter/coordination.go:127-134` returns `FailedPrecondition` with `coordinator_handoff_stale` on
`gen <= st.coord.lastFenced`; the driver's transient arm is `coordfence.go:180-183` and the stale arm that
re-reads and relinquishes is `:171-179`; the trace is attempt 1 lost ack (transient, `default` arm), attempt
2 refused stale, relinquish on the second of three; and `pkg/gateway/coordination/coordfence/coordfence.go`
is the package's only non-test file and carries no timer, sleep, or backoff (`grep` for all three returns
nothing). — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-189, pkg/adapter/coordination.go:127-134

FACT: 0080 owns the refusal half and does not own the backoff half. `proposals/0080_...md:216-260` is
§1.16 and states the `gen < lastFenced` remedy, the wire-comment restatement, the spec arms, and a test
case; `grep -n "backoff\|jitter\|sleep" proposals/0080_...md` returns nothing, and no other proposal names
the coordfence backoff. So the summary's "no proposal owns it today" holds and there is nothing to correct.
— EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:216-260

FACT: the two refusal claims the reclassification rests on are met in the tree, both before any root is
resolved. `pkg/adapter/coordination.go:109-111` refuses an empty session id on the fence;
`pkg/adapter/checkpoint.go:81-87` refuses an empty or path-unsafe id on the checkpoint opening frame,
before the op lock. `spec/05_runtime-registry-and-pool-model.md:515`'s clause is the last sentence of a very
long line and only a grep for the sentence shows it. — EVIDENCE: pkg/adapter/checkpoint.go:74-87

FACT: the hold-state anchors SPEC-1 cites are current. `spec/10_gateway-internals.md:57` is the "only way to
exit hold state" sentence; `:60`'s LAST sentence is "The hold itself and the `lenny_adapter_coordinator_hold`
gauge remain pod-scoped", which a truncated read of that line misses entirely. `pkg/adapter/holdstate.go:335`
and `:348` are the unary and stream interceptors; `pkg/adapter/coordination.go:150-156` is the
`exitHoldState()` call with its lock-order comment. — EVIDENCE: spec/10_gateway-internals.md:60

FACT: the newest text (the §28.5.3 spec-map deletion, written since the spec-r3 snapshot) checks out on
every anchor a reliability read touches: `tests/spec-map.json:156`, `:169`, `:5670` are exactly and only the
three registrations of the retiring gate's two case names; `spec/28_communication-channels.md:501` is the
intra-pod boundary sentence and `:840-841` is the JSON Lines address-equality condition; the credit doctrine
quoted from `tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` reads as quoted (it sits
above `TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise`, not above the case the paragraph
otherwise discusses, which is a wording nuance below the bar). — EVIDENCE: tests/spec-map.json:5670

WATCHOUT: this lens has now fired four times on this proposal and the only reliability defects on the
surface (the equal-generation re-fence refusal and the absent 1-second backoff) are pre-existing, recorded
in the summary, filed twice already, and owned by 0080 §1.16 for one half and by nobody for the other. Do
not re-file either. The standing context's `## Open` list already carries them.
— EVIDENCE: 0075_....summary.md `## Defects in the shipped tree that this proposal does not stage`
### [non-spec.1.review-reliability.1]

DECISION: returned an empty findings list for the reliability lens on the joint spec+non-spec read — BECAUSE the staged work is three deliverables over spec prose (§4.1 rule), a tier-0 static gate, and a tier-3 contract suite's declarations; it adds and changes no retry, lease, fence, drain, dedup, or store-failover path, and the only recovery mechanism it *describes* (the coordinator fence) it describes accurately — ALTERNATIVES: re-filing the `spec/10:39` retry/backoff gap, rejected because it is pre-existing, already recorded in the summary's unstaged-defects section with correct citations, and already routed (refusal half to 0080 §1.16, backoff half recorded as unowned).

FACT: the summary's fence-retry defect entry verifies end to end, every anchor. `spec/10_gateway-internals.md:39` is exactly the "up to 3 attempts with 1-second backoff" clause (grep, not sed — a `sed -n '35,45p'` display makes it look like `:38`). The adapter's stale refusal is `pkg/adapter/coordination.go:127-134` (`gen <= st.coord.lastFenced` → `FailedPrecondition` `coordinator_handoff_stale`). The driver's transient arm is `coordfence.go:180-183` (default case, `continue`s at the same `gen`) and the stale arm is `:171-179` (re-read `CoordinationGeneration`, no advance → `relinquish`). So a landed-but-unacknowledged fence is refused on attempt 2 and the coordinator relinquishes there rather than exhausting the budget of 3 (`DefaultMaxAttempts` at `:52`). `pkg/gateway/coordination/coordfence/` holds exactly two files, `coordfence.go` and `coordfence_test.go`, and `grep -n 'time\.'` over the non-test file returns nothing, so the "no backoff at all" half is confirmed too — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-186

FACT: problem-statement §1.2's recovery claim holds against the tree. `boundSlotState` returns `FailedPrecondition "session %s is not assigned to this pod"` when the registry holds no bound entry, so "resolving nothing is the refusal" is literally what the recycle guard does — EVIDENCE: pkg/adapter/slotsession.go:274-283

WATCHOUT: the recycle guard's refusal and a stale-generation refusal are the same gRPC code. `coordfence.go:164-165` treats *any* `FailedPrecondition` as a generation-stale rejection, so a fence sent to a pod that was recycled away from the session takes the stale arm, re-reads Postgres, sees no advance, and relinquishes the lease. That is pre-existing tree behavior that this proposal neither creates nor touches, and §1.2 makes no claim about the driver's side, so it is not a finding here. If a later loop wants it inventoried it belongs in proposal 0080, not in 0075 — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:164-179

USEFUL [standing context, "coordfence driver"]: the entry naming `DefaultMaxAttempts = 3` at `:52`, the transient arm at `:180-183`, the stale arm at `:171-179`, and the warning that the 5-second deadline lives one layer down in `adapterclient/coordinatorfence.go` rather than in `coordfence.go` saved a full re-derivation and pre-empted the false "unbounded RPC" finding this lens would otherwise have reached for.

USEFUL [standing context, "Do not re-file the session-scoped fence exiting a pod-wide hold"]: this is the one trap a reliability lens walks straight into (a pod in hold with no bound entry can only reach the timeout, because `CoordinatorFence` now requires `boundSlotState`). It is 0076's landed behavior, weighed and rejected by 0076's OD3, and D3 records the rejection. Confirmed the mechanism in `pkg/adapter/coordination.go:107-156` and stopped, as four earlier lenses did.

### [non-spec.1.review-security.1]

DECISION: Returned an EMPTY findings list, the sixth consecutive empty pass for the security lens on this
proposal — BECAUSE the round's whole delta (spec-r3 diff: TEST-1's 28.5.3 decision plus the `:1110`
refinement, and in the summary the OD1 confidence rationale, OD4's deletion, the coordfence backoff half,
two new unstaged-defect entries, and the impacts-table updates) is either a coverage-attribution change or
an unstaged-defect record, and I re-derived both lens checks from the tree rather than replaying the
standing context — ALTERNATIVES: filing the 28.5.3 spec-map deletion as a removed control (declined,
§28.5.3 keeps roughly twenty other credited cases and the credit gate is one-directional); filing the
retired human classification checkpoint (declined, disclosed in the staged normative text and in OD1);
re-filing the session-scoped fence exiting a pod-wide hold (declined, 0076's landed behavior and the
interceptor allowlist is keyed on gRPC method names).

FACT: deleting `tests/spec-map.json:5670` cannot uncover section 28.5.3. The section's `tests` array runs
roughly twenty other entries including the frame-address contract cases, and the credit gate
`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` refuses missing credits and never surplus ones,
so removing a credit no annotation names is a no-op for every gate — EVIDENCE: tests/spec-map.json:5655-5680;
tests/tier0_static/spec_map_slot_address_registration_test.go:970-989.

FACT: the doctrine sentence TEST-1 now quotes belongs to a gate that does NOT cover the file TEST-1 edits.
`tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` is the doc comment of
`TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise`, whose subject is
`slotAddressAbsenceTestFile = "tests/tier3_contract/rest_sessions/slot_address_absence_test.go"` (`:44`),
not the tier-0 gate file. The proposal's wording ("the credit doctrine the tier-0 register gates state")
cites it as a stated doctrine rather than as an enforcing gate, so it is accurate as written and is not a
finding — but a future fixer who tightens that sentence into "the gate refuses that credit" would make it
false — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:44, :89-94.

FACT: the newly added backoff half of the unstaged-defect entry is exactly right and cheap to confirm.
`pkg/gateway/coordination/coordfence/coordfence.go` is the package's only non-test file and imports no
`time` at all, so the three attempts `spec/10_gateway-internals.md:39` orders "with 1-second backoff" issue
back to back — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-188 (loop with no delay);
`grep -n "Sleep\|time\.\|backoff\|Timer\|Ticker\|After(" ` on that file returns nothing.

FACT: the §4.7 unstaged-defect entry's security-load-bearing clause holds in code. The pod-wide hold is
enforced by method-name allowlist interceptors and is cleared by a fence for ANY bound session, which is
what makes "the hold the fence clears is pod-scoped" true after 0076 — EVIDENCE: pkg/adapter/holdstate.go:336,
:349 (`!coordinatorHoldAllowedMethods[info.FullMethod]`); pkg/adapter/coordination.go:150-156
(`s.exitHoldState()` after recording the per-session generation).

WATCHOUT: `sed -n '69p' docs/reference/adapter-contract.md` and `sed -n '712p' spec/04_system-components.md`
are single table rows hundreds of characters wide; pipe them through `tr '|' '\n'` or the cited clause looks
absent and a correct citation reads as false — EVIDENCE: spec/04_system-components.md:712;
docs/reference/adapter-contract.md:69.

USEFUL [spec.4.review-security.1]: the strict-superset fact (exactly one message moves, pod to session, so
the `spec/05:515` empty-identifier refusal can only be added) is still the single fact that makes the whole
retirement fail-closed-safe. I re-derived it from the proto and the retired table and reached the same
answer; a seventh pass should spot-check it rather than rebuild it.

USEFUL [non-spec.3.review-security.1]: `coordinatorHoldAllowedMethods` being keyed on gRPC method names
rather than on a message's scope class is what closes the only plausible security regression here. Held on
independent re-check at `pkg/adapter/holdstate.go:336`, `:349`.

USEFUL [non-spec.5.review-security.1]: its cache WATCHOUT is the reason I hand-diffed the summary. The key
is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no `summary.md` component, and
this round's summary changes (OD1 rationale, OD4 removal, two new defect entries) would have been invisible
behind a hit. This round's key is `6a5288a20f4a`.

OPEN: nothing new from this lens. Six consecutive empty passes (spec.4, spec-recheck.2, non-spec.1,
non-spec.3, non-spec.5, and this one) with the load-bearing facts independently re-derived each time.
Retiring the lens is safe on the current text; re-run it only if a fix pass edits the staged §4.1 constraint
paragraph, OD1's residual statement, or `pkg/`-facing prose.
### [non-spec.1.review-security.1]

DECISION: returned an empty findings list for the security lens over the joint spec + non-spec staging — BECAUSE neither security check has a subject here. Check (1), regression of an established control: the only mandatory control the staged text touches is the tier-0 gate, and its retirement is OD1's own subject rather than a silent removal; the §10.1.4 hold, the §13.2 egress posture, the §10.3 zero-RBAC posture, admission purity, and every residual-state bound are untouched by a spec-table-to-rule refactor and two test-file edits. Check (2), durability of a security bound: the staging introduces no bound, no counter, and no self-reported value. ALTERNATIVES: filing the fail-open residual named in summary OD1 (a session addressed under both an unconventional name and an unconventional type derives pod-scoped and inherits no empty-identifier refusal) — rejected because the staged spec text names that blind spot explicitly and OD1 routes the acceptance to the human.

FACT: `schemas/lenny-adapter.proto` declares NO nested messages. `grep -c "^message "` and `grep -c "^\s*message "` both return 88, and `grep -n "^\s\+message \w"` returns nothing. This matters for TEST-1: `protoMessageOpen` is anchored at column 0 (`tests/tier0_static/adapter_proto_parse_test.go:22`), so the shared parse reaches every message in the file, and the replacement gate's "on every message the protocol declares" quantifier is satisfiable by the parse the proposal requires it to use. A nested message carrying an unconventional address is not a hole the gate misses, because no such declaration form is in use. — EVIDENCE: schemas/lenny-adapter.proto (88 top-level `message` lines, zero indented ones); tests/tier0_static/adapter_proto_parse_test.go:22

FACT: the §10.1.4 hold-state gate does not read the §4.1 classification, so retiring the table cannot change which RPCs pass while a pod is held. `coordinatorHoldAllowedMethods` is a literal gRPC method-name allowlist of `CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, and the two health methods, consulted by both interceptors on `s.inHoldState()` alone. A reviewer who suspects the reclassification of `CoordinatorFenceRequest` widens or narrows the hold allowlist should stop here. — EVIDENCE: pkg/adapter/holdstate.go:52-58, :335-339, :348-352

FACT: the 18 keys of `sessionScopedMessages` today are exactly the 18 messages declaring `reserved "slot_id"` in the proto, confirmed independently with `awk '/^message [A-Za-z]/{m=$2} /reserved "slot_id"/{print m}'`. TEST-2's `retiredDuplicateNumbers` therefore takes the whole current population and the reservation arm loses no coverage in the split. — EVIDENCE: schemas/lenny-adapter.proto (18 hits); tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: all eight proto ranges TEST-2 cites for the widened session set are exact and each opens `SessionId session_id = 1` on its second line, and none of the eight declares a field named `slot_id` (the only surviving `slot_id` strings in the proto are `reserved` pairs and retirement comments). Both widened address arms are green on the shipped proto. — EVIDENCE: schemas/lenny-adapter.proto:341-343,364-368,383-385,403-406,419-424,1455-1461,1538-1549,1673-1684

USEFUL [Traps: "Cache keys omit `summary.md` and `review-log.md`, and the cache path carries no lane"]: the cache slot `security-r1-d91c9fcab1da.json` was empty, so no stale-lane hit had to be declined, but the entry is why I checked the `coverage` string convention before writing my own.

USEFUL [Settled: "Proto census" and "Proto anchors, current and verified"]: spot-checked `:1455`/`:1456`, `:596`, `:499-503`, and the §4.1 anchors `:149`/`:151`/`:153-186`/`:175`/`:188`/`:190`/`:192` rather than re-deriving. All exact. Saved a full re-derivation.

UNVERIFIED: whether any tier-1 or tier-3 case pins the empty-identifier refusal for the other 24 session-addressed request messages the derivation rule now covers, or only for `CoordinatorFenceRequest` (`pkg/adapter/coordination_test.go:35`). Not a finding for this proposal, because the derived class is a strict superset that adds exactly one message and that one is pinned, but a security lens on a later proposal that widens the class should check it.

### [non-spec.1.review-test-coverage.1]

DECISION: Returned an EMPTY findings list, the eighth empty test-coverage pass on this proposal — BECAUSE
the staging delta since [non-spec-recheck.2.review-test-coverage.1] is confined to TEST-1's spec-map
paragraph and TEST-1's `repoFileLines` correction, neither of which adds a deliverable, a tier, or a
behavior, and an independent re-derivation of §8 against D1's four gate refusals still maps one enumerated
negative case onto each, plus the tier-3 fence-address constraint, with tiers 0, 3, and 11 named —
ALTERNATIVES: filing the new §28.5.3 credit deletion as a coverage regression (rejected, see FACT below);
filing tier 11 as named-but-uncased (rejected a sixth time; re-verified no tier-11 case opens §4.1's
block); filing the absent tier-1 case for the newly session-scoped fence (rejected; the one imported
obligation is already pinned); filing "nothing pins that the extended `protoFields` still reports the same
field set" (rejected; two earlier passes ruled it a mechanism subject and the remedy is a stated
constraint rather than a case); filing §8's "IMPLEMENTOR TO FILL THE BLANKS" banner and its "written
during convergence" clause (rejected; the five case subjects are enumerated, so it is stale wording rather
than a coverage gap, and wording is out of scope).

FACT: deleting `tests/spec-map.json:5670` opens no coverage hole. Section 28.5.3's credit list runs from
`:5655` to past `:5692` and carries roughly thirty other cases across tiers 3, 10, and 11 plus
`pkg/adapter/tracingcontext_addressing_test.go`'s seven cases, so the section keeps coverage after the one
tier-0 entry goes. This is the first pass to check it, because the deletion is new text in this round. —
EVIDENCE: tests/spec-map.json:5655-5692

FACT: `tests/tier0_static/adapter_proto_parse_test.go` is named `_test.go` but declares no test function.
It is a shared helper file in package `tier0_static`, and the parse has no self-test today; its correctness
is exercised only through the two gates that call it. That is why the replacement gate's synthetic-proto
self-test is the only thing that will exercise TEST-1's type and `oneof`-membership extension, and why no
separate parse case is owed. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:1-95

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its FACT that the reclassification's single imported
behavioral obligation is already pinned held a sixth time and again settled the tier-1 question in one
step. Re-verified end to end: `spec/05_runtime-registry-and-pool-model.md:515` ("a session-scoped request
whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any
root is resolved"), `pkg/adapter/coordination.go:108-110` returns it ahead of `boundSlotState`, and
`pkg/adapter/coordination_test.go:35-43` asserts `codes.InvalidArgument`.

USEFUL [non-spec-recheck.1.review-test-coverage.6]: its four rejected candidates remain the complete
candidate set for this lens. The only candidate it could not have carried is the §28.5.3 credit deletion,
which did not exist when it ran; that one is now closed by the FACT above, so a ninth pass has nothing new
to reach for unless a fix pass changes §8 itself.

UNVERIFIED: nobody has executed tier 0 or tier 3 on this branch. Every coverage judgement on this proposal,
including mine, is derived from reading files. Whoever implements S2 should confirm that renaming the two
gate cases and re-pointing `tests/spec-map.json:156`/`:169` inside one commit leaves `validate-maps` green,
which is the one sequencing question the standing context records as unrun.
### [non-spec.1.review-test-coverage.1]

DECISION: returned an empty findings list for the test-coverage lens on the merged spec + non-spec staging — BECAUSE §8 (`non-spec-changes.md:99-108`) names the three tiers the change actually reaches (0, 3, 11) and enumerates one refusal subject for each of the four clauses D2's replacement gate carries, with the fourth split into its two boundaries, plus the tier-3 requirement that the fence's address be pinned like every other member's; and because the one behavioral obligation the reclassification imports is already met and already pinned. ALTERNATIVES: filing §8's stale `**IMPLEMENTOR TO FILL THE BLANKS.**` banner (standing context already records seven test-coverage passes judging it stale wording rather than a coverage gap); filing "no tier-11 case is named" (tier 11 is listed and the change opens no tier-11 surface, so the listing is a regression-run note, not an omission); filing "add a gate deriving the tier-3 session set from the proto" (that is OD5, a decision routed to the human, and hardening beyond what the change requires).

FACT: the four clauses D2 states map one-to-one onto §8's five enumerated refusal subjects. D2 (`spec-changes.md` §2) = (a) a field named `session_id` is of type `SessionId`, (b) a field of type `SessionId` is named `session_id`, (c) an envelope declares no top-level address of its own, (d) exactly one of its frames declares the address. §8 = wrong type, wrong name, envelope with its own address, envelope frames declaring zero, envelope frames declaring two. Nothing in D2 or in SPEC-1's third staged paragraph is left unexercised. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md §2 D2; proposals/0075_.../0075_....non-spec-changes.md:99-108

FACT: the reclassification's one behavioral obligation is met AND pinned, so it owes no new tier-1 case. `pkg/adapter/coordination.go:109-111` returns `InvalidArgument` on an empty session id before `boundSlotState` (`:116`) and before the generation checks (`:121`), and `pkg/adapter/coordination_test.go:35` is `TestCoordinatorFenceRejectsMissingSessionID`. Verified directly this round rather than taken from the standing context. — EVIDENCE: pkg/adapter/coordination.go:108-111; pkg/adapter/coordination_test.go:33-35

FACT: nothing outside the retiring tier-0 gate, the tier-3 suite, and `tests/spec-map.json` reads the §4.1 table. `grep -rln "Request Message Scope\|messageScopeRow\|message-scope\|MessageScope" tests/ cmd/ pkg/ scripts/` returns those three plus two unrelated `pkg/gateway/mcpfabric/mcptools` files (MCP message scope, a different sense). No tier-11 case reads the block, and no `docs/` page restates the request-message classification: the only `session-scoped` sentence in `docs/reference/adapter-contract.md` is `:81`'s `ReportSessionScrub` row, which SPEC-1 keeps and which the tier-11 reconciliation gate holds against `spec/04:725`. So §8's tier-11 listing owes no new case. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go; tests/tier3_contract/adapter_session_address/session_address_wire_test.go; docs/reference/adapter-contract.md:81

FACT: every test-side citation in the OD5 answer paragraph (`non-spec-changes.md:94-108`, the only staged text added since the `spec-r3` snapshot) re-verifies exactly. `session_address_wire_test.go:32` is `const retiredFieldName = "slot_id"`; `:78-92` is `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` with its loop at `:81`; `:107-111` is the presence check and `:112-114` the `lenny.adapter.v1.SessionId` comparison inside `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` (loop at `:102`); `:130` is the reservation loop; `:150-158` is `TestTheRetiredAddressWrapperIsGone_spec_15_4`. The tier-0 inventory quote is verbatim at `tests/tier0_static/spec_map_slot_address_registration_test.go:1209-1211`, `:336-337` are the two gate paths in `slotAddressCaseFiles`, `:699-706` is `repoFileLines`, `:1212` is `derivedInventoryCaseFiles`, and `:89-93` is the credit-doctrine comment. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:32,78-92,99-116,130,150-158; tests/tier0_static/spec_map_slot_address_registration_test.go:89-93,336-337,699-706,1209-1212

WATCHOUT: the OD5 paragraph's residual-coverage argument is one step looser than it reads, and a future reviewer will reach for it. It says the one arm an omitted message escapes is `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1`, "and the wrapper type that name carried is barred from the protocol definition outright by `TestTheRetiredAddressWrapperIsGone_spec_15_4`". That wrapper gate bars the message type `lenny.adapter.v1.SlotId` (`:35`, `:150-158`); it does not bar a future field literally named `slot_id` of some other type, which is what the escaped arm tests for (`:87-89`). The paragraph never claims otherwise, so it is not a false citation, and the residual it describes is exactly the one OD5 routes to the human. Examined and declined; do not re-file it as a coverage gap. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:35,87-89,150-158

FACT: the retiring gate's own self-test is the template the replacement inherits, and it is one green-baseline `t.Fatalf` (`:174-176`) followed by a `t.Run` per refusal (`:182-201`) driven off a synthetic proto plus a synthetic table. §8's "must be shown to fail once for each clause" therefore has a concrete, in-repo shape and needs no case named by function name for the requirement to be executable. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:156-214

USEFUL [standing-context Traps, "Cache keys omit summary.md and review-log.md"]: the cache slot for `test-coverage-r1-d91c9fcab1da` was empty this run, so no stale-lane hit was possible, but the entry is why I checked the hash before trusting anything.

USEFUL [standing-context Open, "§8's stale framing"]: saved a round. Seven earlier test-coverage passes already judged the `IMPLEMENTOR TO FILL THE BLANKS` banner and "written during convergence" as stale wording rather than a coverage gap. Without that entry this is the first thing the lens files.

### [non-spec.2.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE the checklist simulates cleanly end to end and every
staged edit's target, anchor, and created artifact resolves against the tree at HEAD. Three steps, one lane
each, one deliverable each, all boxes unchecked, `Depends-on` naming only earlier steps, the spec step
leading. ALTERNATIVES: I re-examined and rejected filing on the blanket `IMPLEMENTOR TO FILL THE BLANKS`
banner in `non-spec-changes.md` §5 (the blocks below it name target, change, and every member of both
populations, so nothing is actually left to invent), and on the replacement gate's unstated case names (they
are written into the Go file and `tests/spec-map.json` in the same step, so any consistent choice applies).

FACT: the envelope arm IS buildable from the extended shared parse, which closes the standing `## Open`
question "whether the implementor can build the envelope arm without resolving a `oneof` arm's type to
another top-level message". `protoFields` reports `CheckpointRequest`'s arms as the fields `start`, `grant`,
`abort` (their declaration lines are `CheckpointStart start = 1;` and so on), so once the parse carries a
field's TYPE, `start`'s type is the string `CheckpointStart` and `protoFields["CheckpointStart"]` is already
in the same map with its own field types. No descriptor resolution is needed; one extra map lookup does it.
— EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:25 (`protoField`), :36-62; schemas/lenny-adapter.proto:1173-1178, :1217

FACT: the replacement gate still needs `protoServiceRequests`, so retiring the table's readers leaves no
unused-function for staticcheck's `unused` to flag in tier 0. Clause 1 of D2 quantifies over "every message
the protocol declares" and needs `protoFields` alone, but clause 2 quantifies over "a request message
carrying its frames in a `oneof`", and the only thing in the package that says which messages are request
types is `protoServiceRequests`. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:68; 0075_....spec-changes.md D2

FACT: `parseMessageScopeTable` reads the whole of `spec/04`, but no line outside `:153-186` matches
`messageScopeRow`. I ran the exact regex over the file with the §4.1 rows excluded and it returned nothing,
so S1's stated red-window reason ("leaves `parseMessageScopeTable` with no rows") is literally exact rather
than approximately right. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43, :54-70

WATCHOUT: a comment in the register gate claims "The map validator walks tier 2 through tier 10 only", which
reads as though a dangling `tests/tier0_static/...::TestName` entry could not redden tier 0. It can.
`validateSpecMapTestFuncs` iterates every section and every `::`-carrying entry with no tier filter, reads
the named file, and requires a top-level `func <Name>(`. The comment is about a different validator arm. Do
not use it to argue that TEST-1's spec-map re-point could be split out of S2.
— EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:232-235 vs cmd/lenny-test/cmd_validate.go:941-998

FACT: the fifth reader of `slotAddressCaseFiles` is one-directional and reads no listed path.
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` builds a set from the list, derives an expected
inventory by walking `inventoryWalkRoots` with `repoFileBytes`, and reports only files the list OMITS. So a
listed file that stops matching the derivation rules is never reported, which is why keeping the gate's path
costs no edit there. The round-1 correction to TEST-1's paragraph is accurate as applied.
— EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1132, :1212-1230, :1269-1276

FACT: the tier-3 split preserves coverage arithmetic exactly. The 18 current `sessionScopedMessages` keys are
exactly the 18 messages declaring `reserved "slot_id"` in the proto (derived independently with a
comment-stripping brace parser), 17 of them are RPC request types and one is `CheckpointStart`, and 17 plus
the 8 additions is the 25 request types declaring a top-level `SessionId session_id`. The 18 are a subset of
the widened 26, so both address arms keep every message they cover today.
— EVIDENCE: schemas/lenny-adapter.proto (18 `reserved "slot_id"` sites); tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: `documentConsistencyGates` and `consistencyGateFiles` are hand lists and neither names
`adapter_proto_message_scope_test.go`. Nothing derives membership, so the replacement gate's arguable change
of character (its subject moves from a spec table to `schemas/lenny-adapter.proto`) reddens no register gate
and forces no edit to a file outside §9's list.
— EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:124-129, :171-177

USEFUL [Standing context / Traps]: the brace-counting warning (`SendMessageResponse {}` and
`ReportPodScrubResponse {}` closing on one line) and the "long lines hide the cited sentence" warning both
paid off immediately; I wrote the census parser against them and got 31/25/6 on the first run. The "do not
re-derive the proto census or the blast radius" entry is right — I spot-checked instead and every spot-check
agreed.

DECISION: did not re-file the S3-tier-list finding my own lens filed on an earlier round — BECAUSE two later
lenses declined it as repo convention and `runStaticTier` does compile the contract-tagged package, so
listing tier 0 on S3 would be redundant rather than corrective. The standing `UNVERIFIED` bullet asking
someone to settle it can be closed as "declined, three passes to one". ALTERNATIVES: re-filing it, rejected
under the "if unsure, do not report" bar.

### [non-spec.2.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`, `summary.md`, and `problem-statement.md` was re-read against the tree today and each resolves to text that says what the proposal claims; ALTERNATIVES: filing the `status.md:22` "§11" dangling pointer (already a standing DEFERRED, and its remedy is in a file this loop does not stage) and filing the spec-map entry-count risk under TEST-1 (under-specification rather than a false citation, see the WATCHOUT below).

FACT: the round-3 fix text is citation-clean. Every anchor added since the `spec-r3` snapshot verifies: the fifth `slotAddressCaseFiles` case (`:1110` = `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`) really does resolve nothing through `repoFileLines`, deriving its expectation from `derivedInventoryCaseFiles` instead — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1110-1135, :1212-1230; the other four reach `repoFileLines` (`:973` via `citedSectionsInFile`/`citedSectionsPerCase` at `:797`,`:776`; `:1028` via `annotatedSectionsPerCase` at `:714`; `:1054` directly; `:1096` via `proposalMarkedAnnotationSites` at `:1066`).

FACT: the credit-doctrine quote is verbatim and correctly located — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:91-92 "credits that section with coverage no regression in it would break". Note the gate that carries it, `TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise` (`:94`), runs over `slotAddressAbsenceTestFile` = `tests/tier3_contract/rest_sessions/slot_address_absence_test.go` (`:44`) ALONE, so it does not mechanically enforce the doctrine on the message-scope gate file. The proposal only claims the gates *state* the doctrine, which is true; do not re-file this as an over-claim.

FACT: §28.5.3 spans `spec/28_communication-channels.md:499` to `:1258` (next heading is `#### 28.5.4 Inter-replica` at `:1259`), so the new `:840-841` "Address equality" citation is genuinely inside §28.5.3 — EVIDENCE: spec/28_communication-channels.md:499, :840-841, :1259. The `:501` quote "between the runtime adapter and the runtime binary inside one agent pod" wraps onto `:502`; that is drift, not a defect.

FACT: `pkg/gateway/coordination/coordfence/` contains exactly `coordfence.go` and `coordfence_test.go`, and a grep for `time.`, `Sleep`, `backoff`, `Ticker`, `Timer` over the non-test file returns nothing, so the newly added "no timer, sleep, or backoff of any kind" claim holds — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go (whole file), spec/10_gateway-internals.md:39.

FACT: the 18 messages declaring `reserved "slot_id"` are exactly the 18 current keys of `sessionScopedMessages`, computed with a brace-counting parse that strips comments — EVIDENCE: schemas/lenny-adapter.proto (18 hits at :465,:695,:731,:856,:935,:972,:997,:1030,:1052,:1076,:1097,:1133,:1211,:1324,:1412,:1502,:1600,:1619) against tests/tier3_contract/adapter_session_address/session_address_wire_test.go:45-62. TEST-2's `retiredDuplicateNumbers` membership rule is therefore exact, and the 8 additions are all verified to declare `SessionId session_id = 1` and no `slot_id` (proto `:341-343`, `:364-368`, `:383-385`, `:403-406`, `:419-424`, `:1455-1461`, `:1538-1549`, `:1673-1684`).

FACT: `SPEC-1`'s `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s `SessionId session_id = 2` is EXACT, not one line off — EVIDENCE: schemas/lenny-adapter.proto:456-458. CORRECTS [standing context, Settled, "Proto anchors"]: that bullet says the address is at `:457` and calls SPEC-1's `:458` "one line off"; `:457` is `string pod_id = 1;`. The proposal is right and the log entry is wrong.

WATCHOUT: TEST-1 fixes the spec-map change to exactly "re-point `:156` and `:169`, delete `:5670`", i.e. two registered cases. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` takes the per-case branch whenever the file has ANY `::TestName` entry, so EVERY test function the rewritten gate file declares that cites 4.1 needs its own entry — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:970-989. If the implementor splits §8's five refusal clauses into more than two functions, two entries leave the rest uncredited and tier 0 goes red inside S2. Judged under-specification rather than a false citation and not filed; an applicability or feasibility lens should decide whether it needs stating.

WATCHOUT: `docs/api/internal.md` is named by exactly two tests and neither reads its code blocks — `fragment_link_test.go:411-416` builds a synthetic page fixture and `naming_lint_test.go:468` loads `anchor-identifiers.md.txt` — EVIDENCE: tests/tier0_static/fragment_link_test.go:411, tests/tier0_static/naming_lint_test.go:468. The summary's claim is exact; do not re-derive.

DEFERRED [0075_....status.md]: still live. `status.md:22` reads "§11 records what the rewrites changed" and no `## 11` exists in any of the five staged proposal files; the only one is inside the review log. What is true instead: the rewrite record lives in the review log. Confirmed by grep across the proposal directory today — the summary's copy is gone and status.md is the sole carrier.

### [non-spec.2.review-feasibility.2]

Actor-action feasibility over the merged spec + non-spec staging, round 2 of the non-spec loop. Empty
findings list. Every actor the staging names exists under that name, and every check it assigns to a layer
can see the data the check needs.

FACT: the OD4 §28.5.3 question this lens filed in the previous firing is now closed in the staged text and
the close is correct against the tree. `TestAdapterProtoRequestMessagesAreClassifiedByScope` annotates
`// spec: 4.1 ..., 28.5.3 (addressing)` and the map credits it under both; §28.5.3 opens at
`spec/28_communication-channels.md:499` and carries no heading again before `:900`, so the `:840-841`
frame-equality citation TEST-1 leans on is inside it. Deleting `tests/spec-map.json:5670` strands nothing:
that section keeps ~30 other entries, and no gate demands a per-section minimum. — EVIDENCE:
tests/tier0_static/adapter_proto_message_scope_test.go:129; tests/spec-map.json:156,:169,:5670;
spec/28_communication-channels.md:499,:501,:838-842

FACT: the credit direction that matters after TEST-1 is one-way. `TestSlotAddressCasesAreCreditedToEvery
SectionTheyAnnotate` requires, for every case in each `slotAddressCaseFiles` entry, a map credit for each
section its own annotation names, and reports nothing about surplus credits. The bidirectional gate that
would refuse an over-credit (`TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise`, whose comment
TEST-1 quotes as the credit doctrine) runs over `slotAddressAbsenceTestFile` alone and never over the gate
file. So the §28.5.3 deletion is required by doctrine rather than by a gate, which is what the staged text
actually claims. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:89-93, :94-96,
:970-989

FACT: the whole blast radius of the shared-parse signature change is two call sites and nothing else.
`protoServiceRequests(` is called once (the retiring gate, `:87`) and `protoFields(` once outside its own
file (`claim_register_proto_agreement_test.go:64`), which reads the result again at `:72`, `:82`, and at
`:93` inside `for msg, declared := range fields`. The `:93` read is a third lookup the proposal's
"`:72`/`:82`" phrasing does not enumerate, but the file is listed under TEST-1 either way, so nothing is
owed. 0076's two tier-0 files call neither and never name `adapterProtoPath`. — EVIDENCE: grep
`protoFields(|protoServiceRequests(` over tests/; tests/tier0_static/claim_register_proto_agreement_test.go:64,:72,:82,:93

FACT: the addressing convention the replacement gate holds is green on the shipped proto in both
directions, re-derived with a bracket-free regex rather than the `[\w.]` form that silently matches
nothing under POSIX grep. 26 fields of type `SessionId`, all named `session_id`; zero fields named
`session_id` of any other type. — EVIDENCE: `grep -nE "^[[:space:]]*[A-Za-z0-9_.]+[[:space:]]+session_id[[:space:]]*=" schemas/lenny-adapter.proto` returns only `SessionId session_id` lines

FACT: TEST-2's split arithmetic checks out mechanically. The 18 messages declaring `reserved "slot_id"` in
`schemas/lenny-adapter.proto` are exactly the 18 keys of today's `sessionScopedMessages`, so
`retiredDuplicateNumbers` inherits a population that is closed and complete, and all eight widened members
are top-level messages in the same file as `StartSessionRequest` (which is what `messageDescriptors`
resolves against) and each declares `SessionId session_id = 1`. Both widened arms pass. — EVIDENCE:
`awk '/^message /{m=$2} /reserved "slot_id"/{print m}' schemas/lenny-adapter.proto`;
schemas/lenny-adapter.proto:341,:364,:383,:403,:419,:1455,:1538,:1673

FACT: `tests/registers/identifier-senses.yaml` carries eight `spec/04_system-components.md` rows keyed by
the ordinal position of a retired-spelling site in that file, so a block deletion in `spec/04` could in
principle renumber them. It cannot here: none of the six retired spellings in the §28.3 naming table
(`LifecycleChannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`,
`lifecyclechannel`, `controlchannel`) occurs anywhere in `spec/04_system-components.md`. The eight rows are
vestigial. This is the one register whose keying could have made SPEC-1's deletion a missing edit site, and
it does not. — EVIDENCE: tests/registers/identifier-senses.yaml:14-36;
spec/28_communication-channels.md:149-158; case-insensitive grep of those spellings over spec/04 returns nothing

FACT: no reader-facing page classifies a gRPC request message by scope except
`docs/reference/adapter-contract.md:81` (`ReportSessionScrub`, session-scoped), which SPEC-1 leaves
standing and a tier-11 gate pins. A sweep of `docs/` for both class words returns only frame-level and
token-level uses. So "no reader-facing documentation file is touched" survives the reclassification. —
EVIDENCE: `grep -rn "pod-scoped\|session-scoped" docs/`

FACT: `spec/04_system-components.md:458` is `SessionId session_id = 2` inside `ReportSessionScrubRequest`
(message opens `:456`), so SPEC-1's `:458` citation is exact.
CORRECTS [Standing context, "Settled", the parenthetical "(SPEC-1 cites `:458`, one line off, below the
bar)"]: the address is at `:458`, not `:457`, and SPEC-1 is right. Verified with a single-range sed to avoid
the multi-range ordering trap the Traps list records. — EVIDENCE: schemas/lenny-adapter.proto:456-458

USEFUL [Standing context Traps, "Naive proto parsers derail on this file"]: saved a re-derivation of the
census; every spot-check matched the settled numbers, so nothing was re-parsed from scratch.

USEFUL [Standing context Traps, "Cache keys omit summary.md"]: the cache miss here was genuine (key
b32f3475fbae), but the note is why this firing did not treat an earlier lens hit as covering the summary
rewrite that added the OD1 confidence paragraph and the 0076/0080 impacts prose.

WATCHOUT: `gate_integrity_test.go`'s `tierZeroGates` is a fixed list keyed by test-function name, and a
rename of a listed gate's case is a tier-0 failure. The message-scope gate is NOT on that list, so TEST-1's
case renames are free, but a future step renaming any of `TestNamingLintReportsNoBareReservedNounPhrase
InTheTree`, `TestIdentifierResolutionCertifiesTheTree`, `TestFragmentLinkGateCertifiesTheTree`,
`TestClaimRegisterAgreesWithTheAdapterProto`, and the rest of that list must edit it in the same change. —
EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-70, :247

OPEN: the staged text re-points exactly two spec-map entries at "the replacement gate's case names", which
presumes the replacement gate declares exactly two test functions. Nothing forbids a third, and a third
would need its own section-4.1 entry or `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` fails.
Not filed: the failure is loud, at the same tier the step already runs, and the proposal lists
`tests/spec-map.json` as touched. An implementor writing more than two cases registers each one.

### [non-spec.2.review-mechanism.1]

DECISION: empty findings list — BECAUSE the only text that changed since the last mechanism pass
(`non-spec.5.review-mechanism.1`, also empty) is TEST-1's spec-map paragraph, its fifth-`slotAddressCaseFiles`-case
sentence, and the checklist S2 wording, and all three verify exactly against the tree; the rest of the
document traces end to end with no unreachable trigger, no bypassed gate, no granularity mismatch, and no
predicate drift across D2, the staged §4.1 block, §3's overview, §8's negative-case list, and the summary's
gate bullet — ALTERNATIVES: I re-examined §3's overview omitting the envelope's "declares no top-level
address of its own" clause (declined for the same reason the prior pass gave, and it is now a refuted
class), and the "re-points the `:156` and `:169` entries" phrasing presupposing exactly two replacement
cases (declined: the same paragraph states the general rule, "registered under section 4.1 alone").

FACT: the newest TEST-1 sentences are exact. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
(:970, loop :973), `TestAddressCaseNamesAgreeWithTheirOwnCitations` (:1027/:1028),
`TestAddressCaseTextCitesNoProposalSectionNumber` (:1053/:1054),
`TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly` (:1095/:1096) each reach `repoFileLines`
(:699-706) for every listed path; `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108, loop
:1110) reads no listed path and only reports files the hand list OMITS relative to `addressRuleGateFile`,
`addressRuleCases`, and `derivedInventoryCaseFiles` (:1212), so a file dropping OUT of the derived set is
harmless. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:699-706, :970-1131, :1212

FACT: `validateSpecMapTestFuncs` reports dangling `path::Fn` entries ONLY; it never reports a case that is
annotated but unregistered, and it skips a `path::Fn` whose file cannot be read. So TEST-1's claim that
after the change "`validate-maps` finds no dangling `path::Test` entry" is exactly what the function
checks, and the surplus-credit direction is caught by nothing (as the log already records for
`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`). — EVIDENCE:
cmd/lenny-test/cmd_validate.go:941-998

FACT: §28.5.3 carries no protobuf name-or-type convention, which is the ground the OD4 answer (delete the
`tests/spec-map.json:5670` credit rather than re-point it) rests on. Everything under the §28.5.3 heading
(`spec/28_communication-channels.md:499`) that says "session-scoped" is about JSON Lines frames
(:310, :341, :344, :349-350), and nothing in the range cites §4.1. — EVIDENCE:
spec/28_communication-channels.md:499-505, :804-805 region scan

FACT: re-derived the census independently with a comment-stripping brace parser rather than trusting the
standing context: 31 RPC request types, 88 messages, 25 request types declaring a top-level `session_id`,
the 6 without being AdapterEventsRequest, CheckpointRequest, DemoteSDKRequest,
GetObservedIntegrationLevelRequest, NegotiateVersionRequest, ReportPodScrubRequest. TEST-2's widened set
(18 current members ∪ the 8 additions) is exactly the 25 addressed request types plus `CheckpointStart`,
size 26, with no addressed request type left out and no member lacking the address. — EVIDENCE:
schemas/lenny-adapter.proto; derived, not quoted

FACT: the proto has no nested messages and no `oneof` outside `CheckpointRequest` (:1174) and
`CheckpointResponse` (:1250), and `map<...>` fields do not match `protoField`, so the shared parse's
top-level-only assumption holds and the envelope predicate selects `CheckpointRequest` alone. `protoFields`
has exactly one caller today (`claim_register_proto_agreement_test.go:64`, lookups :72 and :82) and
`protoServiceRequests` exactly one (`adapter_proto_message_scope_test.go:87`). — EVIDENCE:
tests/tier0_static/adapter_proto_parse_test.go:22-31, :36-90

CORRECTS [standing context, Settled, "Proto anchors, current and verified"]: the parenthetical "SPEC-1
cites `:458`, one line off" is wrong and `non-spec.5.review-mechanism.1` already corrected it; re-verified
here. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is
`string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact. Whoever compacts next
should delete the parenthetical rather than carry a third correction of it.

USEFUL [standing context, Traps, "Naive proto parsers derail on this file"]: writing the census parser with
comment stripping and per-line brace counting from the start produced 88 messages and 31 request types on
the first run. Without it I would have re-lost the round the trap describes.

USEFUL [non-spec.5.review-mechanism.1]: its envelope-arm feasibility derivation and its "nothing outside
the retiring gate reads the deleted §4.1 block" sweep are both correct and saved me re-deriving them; I
spot-checked each rather than repeating it, per the standing "do not re-derive the blast radius" trap.

WATCHOUT: the cache key is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no lane
component, so this run's key `b32f3475fbae` is shared with any other lens firing over the same three files.
It was empty on entry, so this pass is a real review rather than a replay, but the next agent should check
the key before trusting a hit. — EVIDENCE: scratchpad/cp-cache/0075_.../mechanism-r2-b32f3475fbae.json

### [non-spec.2.review-operational.1]

Operational-consistency lens (conditions, metrics, alerts, operator docs) over the staged spec changes,
the staged non-spec changes, the checklist, and the summary. Returned no findings. What follows is what
that cost and what a future operational pass can skip.

DECISION: returned an empty findings list — BECAUSE every operator-visible surface this proposal names was
re-verified against the tree and each claim held, and the proposal touches no metric, no alert rule, no CRD
condition, and no reader-facing page. ALTERNATIVES: filing the §4.7 `CoordinatorFence` announcement-row
imprecision or the stale `docs/api/internal.md` excerpts, both of which the summary already records under
`## Defects in the shipped tree that this proposal does not stage` and the standing context already carries
as `DEFERRED`; re-filing either is re-filing a recorded defect, not a review finding.

USEFUL [Standing context / Traps, "Cache keys omit `summary.md` and `review-log.md` ... the operational lens
in particular recorded two clean passes that were one cache hit"]: the cache probe for this firing missed
(H=b32f3475fbae, no file), so this pass is a real review rather than a replay. A future operational pass
should still confirm the probe missed before trusting an empty answer attributed to this lens.

FACT: every operator-facing anchor the summary's `## Defects in the shipped tree` section carries was
re-verified today and all of them hold, so a later operational pass does not need to re-derive them.
— EVIDENCE: spec/04_system-components.md:712 (the whole `CoordinatorFence` row, both the "Announce new
`coordination_generation` to the pod on coordinator handoff" clause and the "Precondition for any
subsequent operational RPC" clause, on ONE line); docs/reference/adapter-contract.md:69 (the mirror,
same compression, no session named); docs/reference/adapter-contract.md:81 and
spec/04_system-components.md:725 (the `ReportSessionScrub` session-scoped sentence the tier-11 gate holds
on both carriers); spec/04_system-components.md:726 (ends "The request is pod-scoped.");
spec/10_gateway-internals.md:60 (ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge
remain pod-scoped"); spec/10_gateway-internals.md:57; spec/28_communication-channels.md:314-317 and :322;
pkg/adapter/holdstate.go:335 and :348; pkg/adapter/coordination.go:109-111, :116, :127-134, :150-156.

FACT: the coordfence backoff claim added to the summary this round is exact. `pkg/gateway/coordination/
coordfence/coordfence.go` is the package's only non-test file (`ls` shows it plus `coordfence_test.go`),
it imports no `time` package and contains no `Sleep`, `Timer`, `Ticker`, `backoff`, or `delay` identifier
of any kind, and `DefaultMaxAttempts = 3` sits at :52 with the transient arm at :180-183 and the stale arm
re-read/relinquish at :165-179. So both halves of `spec/10_gateway-internals.md:39` ("up to 3 attempts with
1-second backoff") are unmet: the same-generation retry is refused and the spacing does not exist.
— EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:52,160-188

FACT: the §28.5.3 citations the fix round added to TEST-1 are exact. `spec/28_communication-channels.md:501`
reads "This boundary carries the channels between the runtime adapter and the runtime binary inside one
agent pod", and `:840-841` is the "Address equality" clause over the JSON Lines `sessionId`. The credit
doctrine quoted from `tests/tier0_static/spec_map_slot_address_registration_test.go:89-93` is verbatim.
Deleting `tests/spec-map.json:5670` does not empty section 28.5.3: that section's `tests` array carries
roughly forty other entries, so no coverage-inventory gate loses its only case.
— EVIDENCE: spec/28_communication-channels.md:501,840-841;
tests/tier0_static/spec_map_slot_address_registration_test.go:89-93; tests/spec-map.json:5650-5690

FACT: the operational blast radius of the reclassification is empty in `docs/`. `grep -rn
"session-scoped\|pod-scoped" docs/` returns twelve sites, none of which classifies a gRPC request message
except `docs/reference/adapter-contract.md:81` (`ReportSessionScrub`, which SPEC-1 keeps and which agrees
with the derivation). No runbook, no alert, and no metric row mentions `CoordinatorFence`,
`coordinator_hold`, or `coordination_generation`; the only hold-state operator surface is
`docs/reference/metrics.md:309` (`lenny_adapter_coordinator_hold`, Gauge, no labels, "1 while adapter is
in hold state"), which agrees with `spec/10:60` and which nothing staged here touches.
`docs/runbooks/coordinator-handoff-slow.md` is about delegation handoff latency and names neither the
fence nor the gauge. — EVIDENCE: docs/reference/metrics.md:309; docs/runbooks/coordinator-handoff-slow.md:5-33

FACT: no tier-11 gate reads `spec/04` §4.1, so S1's listed tier 11 is a regression run rather than an edit
site. The sixteen tier-11 files naming `04_system-components` anchor on §4.7, §4.7.3, §4.9, §5.2, §28, or
the relocation/successor registers; `grep -rn "Request Message Scope\|request-message-scope"` over the
whole tree outside `proposals/` and `scratchpad/` returns `spec/04_system-components.md:149` alone, so the
heading has no inbound link and owes no anchor redirect.

FACT: two sites naming `CoordinatorFenceRequest` outside the proposal's edit list were checked and neither
becomes wrong after the reclassification, so do not file them.
`tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:59-69` excludes the fence from
`fencedMessages` because it "carries the generation as the announcement being fenced rather than as a stamp
validated against a recorded one", which is a fence-semantics ground rather than a scope ground, and its
residual clause names "the pod-level calls that name no session", which the fence is not.
`tests/tier0_static/claim_register_proto_agreement_test.go:41-43` exempts the fence because
`pkg/adapter/coordination.go` already compares its generation, again not a scope ground; that file is
already on TEST-1's list for the `protoFields` signature.
— EVIDENCE: tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:59-69;
tests/tier0_static/claim_register_proto_agreement_test.go:41-43,92

WATCHOUT: the summary's `docs/api/internal.md` bullet says "the only tests naming the page,
`tests/tier0_static/fragment_link_test.go` and `tests/tier0_static/naming_lint_test.go`". Two registers
also name it, `tests/registers/identifier-senses.yaml:227` and
`tests/registers/residual-reserved-phrases.yaml:153`, and gates read those registers. The load-bearing
conclusion is unaffected — the page is untouched by every staged edit, so nothing reddens on account of it
either way — and the sentence's own subject is tests rather than registers, which is why this was not filed.
A future pass that re-derives the sentence will hit the same two rows; it is not a new defect.
— EVIDENCE: tests/registers/identifier-senses.yaml:227; tests/registers/residual-reserved-phrases.yaml:153

### [non-spec.2.review-applicability.1]

DECISION: Returned an EMPTY findings list for the applicability-and-sequencing lens over the non-spec staging read together with the spec staging, the checklist and the summary — BECAUSE the checklist is three steps for three deliverables with one deliverable and one lane per step, the spec lane leads, every Depends-on names an earlier existing step, every box is unchecked, S1's tier-0 red window is recorded with the step that closes it, and every artifact TEST-1 and TEST-2 create or rename carries the properties another edit needs; simulating S1 then S2 then S3 against the tree hits no forward reference, no unresolvable anchor and no gate with an unrecorded disposition — ALTERNATIVES: (a) the replacement gate's case names being unstated, declined because TEST-1 states the invariant that governs them ("every case in the file stays credited to each section its own `// spec:` annotation names") and the names are self-consistent between the file and `tests/spec-map.json` whatever the implementor picks; (b) S3's tier list omitting tier 0 although `go vet -tags=contract ./tests/tier3_contract/...` compiles the package at tier 0, declined because the repo convention omits it on test-lane steps (0076's S8 lists "Tiers 1, 4, 7a") and the standing context records the point as already weighed; (c) the §5 "IMPLEMENTOR TO FILL THE BLANKS" banner, declined as framing rather than an unappliable edit, since every TEST-1 and TEST-2 target and content is stated concretely below it.

FACT: an independent recomputation of the proto census with a comment-stripping brace-counting parser reproduces every number the proposal and the standing context assert: 31 RPC request types, 25 with a top-level `SessionId session_id`, the 6 without being `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; the 18 messages declaring `reserved "slot_id"` are exactly today's `sessionScopedMessages` keys; the set difference (addressed request types minus today's 17 request-type members) is exactly the 8 additions TEST-2 names; and the name/type biconditional has zero violations across all 88 messages including `oneof` arms, so D2's replacement gate is green on the shipped file on day one. — EVIDENCE: schemas/lenny-adapter.proto:341,364,383,403,419,1455,1538,1673; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: nothing outside `tests/tier0_static/adapter_proto_message_scope_test.go` itself names that file or any of its identifiers except one line, `slotAddressCaseFiles` at `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, plus the three `tests/spec-map.json` entries. A single `grep -rn "adapter_proto_message_scope\|MessageScope" tests/ cmd/ scripts/` settles the whole blast radius of TEST-1's in-place rewrite in one command. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336

FACT: S1's 36-line deletion from `spec/04` cannot break any line-citation gate, because both ratchet registers are empty: `tests/registers/line-citations.yaml` and `tests/registers/line-citation-resolution.yaml` each end in `files: []`, meaning no file in the tree carries a citation of the retired line form at all. Do not re-derive the line-shift blast radius from the ratchet's matcher; read the two registers. — EVIDENCE: tests/registers/line-citations.yaml:15; tests/registers/line-citation-resolution.yaml:17

FACT: the eight `spec/04_system-components.md` rows in `tests/registers/identifier-senses.yaml` are keyed by the ordinal position of a retired-spelling site in the file, so a naive reading says a deletion in `spec/04` renumbers them. It cannot: a case-insensitive grep of `spec/04` for every retired spelling the §28.3 naming table declares (`LifecycleChannel`, `controlchannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`) returns nothing, so the rows are inert against any edit to that file. — EVIDENCE: tests/registers/identifier-senses.yaml:14-36; spec/28_communication-channels.md:151-158

FACT: the tier-11 TEST-GAPS drift gate (`tests/tier11_docs/test_gaps_test_reference_rename_drift_test.go`) fires only on a `path::TestName` reference whose name carries a trailing `_spec_X_Y` suffix that resolves in the same file under a different suffix. The retiring gate's two case names carry no such suffix, and `grep` over `TEST-GAPS.md`, `BUILD-GAPS.md`, and `TESTING.md` finds neither name nor the file path, so TEST-1's deletions redden nothing at tier 11. — EVIDENCE: tests/tier11_docs/test_gaps_test_reference_rename_drift_test.go:44-51

USEFUL [standing context, "Four of the five loops over `slotAddressCaseFiles` force the kept path, not five"]: the corrected paragraph in TEST-1 now matches the tree exactly. `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` opens at `:1108`, its `slotAddressCaseFiles` loop at `:1110` only fills a set, and `derivedInventoryCaseFiles` is declared at `:1212` with the "Neither rule reconstructs the whole inventory" sentence at `:1209-1211`. Every one of those anchors is exact; the fix stage got this one right and it needs no further checking.

USEFUL [standing context, cache-key trap]: the cache slot for this lens carries no lane, so the round-2 spec-lane and non-spec-lane runs of the same lens collide on one filename. The slot was empty on this run, but I wrote the lane and the substrate as the first words of the `coverage` string so the next reader can decline a wrong-lane hit in one line.

### [non-spec.2.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every file:line and every quoted string in
`spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`, and `summary.md` resolved exactly
as claimed against commit 132a38f0d, including all eight new tier-3 member anchors, the three spec-map
anchors, the five `slotAddressCaseFiles` loop anchors, and every code anchor in the three unstaged-defect
entries — ALTERNATIVES: filing the D2 "an addressing convention that nothing checks today" overstatement,
rejected because it is decision rationale rather than applied text, it is already recorded as a DEFERRED at
review-log `:171` awaiting the between-loops pass, and two spec-lane passes plus six declines have already
settled it; filing the summary's "The one case over these rows" for §4.7, rejected because
`recycle_scrub_trigger_consistency_test.go`, `eviction_coordinator_route_consistency_test.go`, and
`session_scrub_report_addressing_doc_reconciliation_test.go` each anchor on one *named* row while
`spec_47_rpc_row_naming_test.go` is the only one that ranges over all of them, and the load-bearing half
("no other test parses §4.7 prose for the fence") is true: `grep -n CoordinatorFence tests/tier11_docs/*.go`
returns nothing.

USEFUL [Standing context / Settled, Traps]: the census bullet, the proto-anchor bullet, the
`slotAddressCaseFiles` blast-radius bullet, and the spec-map registration bullet were each exact. Verifying
them by spot-check rather than re-derivation cost under ten tool calls where the archive records earlier
rounds spending a round each. The `:458` "one line off" deletion in particular held: `grep -n` puts
`SessionId session_id = 2` on `:458` and SPEC-1 is right.

USEFUL [Traps / naive proto parsers]: writing the census parser with a per-line brace counter and a
comment strip on the first attempt (rather than a to-next-`}` scan) produced 31/25/6 and 18 reserved pairs
first try, matching every earlier derivation.

FACT: the name-and-type biconditional now has an independent mechanical check. A parse over every
`message` block in `schemas/lenny-adapter.proto`, including `oneof` arms, comparing `name == "session_id"`
against `type == "SessionId"`, returns zero violations, so D2's replacement gate is green on the shipped
proto on day one. — EVIDENCE: schemas/lenny-adapter.proto (whole file); the 18 `reserved "slot_id"`
messages it reports are byte-for-byte the 18 keys of `sessionScopedMessages` at
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63.

FACT: `message SessionId` opens at `schemas/lenny-adapter.proto:596`, with `string value = 1` at `:597`.
This closes the UNVERIFIED at review-log `:157`; the `:595` reading in `non-spec.5.review-mechanism.1` is
wrong. — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: `pkg/adapter/holdstate.go:335` is the `holdStateUnaryInterceptor` declaration and `:348` the
`holdStateStreamInterceptor` declaration; `:336` and `:349` are their `s.inHoldState()` guard bodies. The
summary cites the declarations and the standing context cites the bodies. Both are correct and neither is
a drift finding. — EVIDENCE: pkg/adapter/holdstate.go:335, :348

FACT: 0073's SPEC-7 does carry a §5.2 restatement, at `spec/05:457`'s sentence-run in the §5.2 Lenny scrub
procedure, so the summary's 0073 row ("the §4.7 RPC-table row and the §5.2 restatement SPEC-7 carried are
untouched") is exact on both halves. A grep for `§5.2` inside SPEC-7's range (`:2823-3116`) is what settles
it; `:2843`'s "the specification side ... takes no edit here" is about a *different* §5.2 sentence
(`spec/05:451`) and reads as a refutation if taken alone. — EVIDENCE:
proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:2826

FACT: `tests/registers/residual-reserved-phrases.yaml` and `tests/registers/identifier-senses.yaml` also
name `docs/api/internal.md`, beyond the two tier-0 test files the summary lists. Neither is a test, both
key on anchor identifiers, and nothing this proposal applies touches the page, so the summary's conclusion
("nothing reddens on account of the page") stands. Recorded so the next lens does not spend a call on it.
— EVIDENCE: tests/registers/identifier-senses.yaml, tests/registers/residual-reserved-phrases.yaml

WATCHOUT: the round-2 non-spec snapshot at
`scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r2` is byte-identical to
the live directory (all nine files, same mtimes), so `diff -ru` prints nothing and the "read the changed
sections first" instruction has no changed sections to point at. This is the trap already at review-log
`:115`; it fired again. Treat the whole proposal as unexamined text and use
`git diff 470cd4e34 HEAD -- <proposal dir>` if a delta is actually needed. — EVIDENCE:
scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r2

WATCHOUT: the DEFERRED at review-log `:170`, against the checklist's TEST-1 step saying spec-map
"re-registers ... under sections 4.1 and 28.5.3", has already been applied. The checklist now reads
"re-points the retiring gate's two section 4.1 entries at the replacement gate's case names and deletes its
section 28.5.3 entry". Do not re-apply it and do not file it. — EVIDENCE:
proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md:13-16

### [non-spec.2.review-client-surface.1]

DECISION: returned an EMPTY findings list — BECAUSE nothing this proposal stages reaches an externally-consumed contract. `schemas/lenny-adapter.proto` is unchanged, no `sdks/` file names `CoordinatorFence` or any gRPC scope class, `openapi.json`/MCP/A2A/CRDs/JSONL schemas are untouched, and the one client-facing doc that is stale about the protocol (`docs/api/internal.md`) is already recorded in the summary's unstaged-defects section with correct anchors. ALTERNATIVES: filing `docs/api/internal.md` as a missed edit site (rejected: it is false about the shipped proto today, so it does not *become* wrong, and the proposal names it); filing the indefinite "a protocol definition" in the staged gate sentence (rejected: standing-context trap, declined repeatedly, and the antecedent plus `adapterProtoPath` save it); filing that TEST-1's extension of `protoFields` could change which fields it reports and silently reweaken `claim_register_proto_agreement_test.go` (rejected: it is already a standing-context WATCHOUT, the IMPLEMENTOR'S CHOICE marker is bounded, and the risk is mechanism-lens territory).

FACT: `tests/spec-map.json` section 28.5.3 carries 58 test entries and the entry TEST-1 deletes (`:5670`, `adapter_proto_message_scope_test.go::TestAdapterProtoRequestMessagesAreClassifiedByScope`) is its **only** tier-0 entry. Deleting it is still safe: `validateSpecMapCoverage` fails a section only when `len(tests) == 0` and honours `tests/spec-map-exceptions.yaml`; there is no per-tier coverage requirement anywhere in the validator set. — EVIDENCE: cmd/lenny-test/cmd_validate.go:801-834, tests/spec-map.json:5670

FACT: the 18 keys of `sessionScopedMessages` are exactly the 18 messages that declare `reserved "slot_id"` in the proto, and every map value matches that message's reserved number, so TEST-2's `retiredDuplicateNumbers` can be lifted verbatim. Watch the count: a naive `grep -c reserved` returns 19 messages, because `NegotiateVersionResponse` carries a bare `reserved 5;` with no `slot_id` name. — EVIDENCE: schemas/lenny-adapter.proto:1732 (`reserved 5;` on NegotiateVersionResponse), :695, :731, :856, :935, :972, :997, :1030, :1052, :1076, :1097, :1133, :1211, :1324, :1412, :1502, :1600, :1619, :465

FACT: the three SDK client cases credited to spec-map section 4.1 (`tests/tier3_contract/sdks/{go,python,typescript}_client_test.go::Test*ClientMessageBodyOmitsSlotAddress`) annotate `// spec: 15.4 (MessageEnvelope), 7.2 (message dispatch)` and never 4.1. That is a pre-existing surplus credit under the section SPEC-1 rewrites; no gate catches it, because `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` is one-directional. It is not this proposal's to fix and does not belong in §9. — EVIDENCE: tests/tier3_contract/sdks/go_client_test.go:764-769, tests/spec-map.json:150-170

FACT: `message SessionId` opens at `schemas/lenny-adapter.proto:596` (`:595` is its doc comment). This settles the standing-context UNVERIFIED. The addressing biconditional holds on exactly 26 declarations, 25 request types plus `CheckpointStart`, with no `session_id` of another type and no `SessionId` under another name anywhere in the file, so D2's replacement gate is green on the shipped proto. — EVIDENCE: schemas/lenny-adapter.proto:595-596

USEFUL [standing context, Traps, "Naive proto parsers derail on this file"]: I wrote a comment-stripping, brace-counting parser on the first attempt because of it and reproduced the 31/25/6 census cleanly. USEFUL [standing context, Traps, "Bash is the wrong tool for reading this log"]: two `awk`/`sed` reads of the standing context were persisted to files and cost two wasted calls before I switched to small `sed` windows.

USEFUL [standing context, Traps, "Long lines hide the sentences citations point at"] and the range-print miscount note: a `sed -n '108,114p' docs/api/internal.md` printed six lines for a seven-line range and made the summary's `:111`/`:147`/`:245` anchors look one off. `grep -n` resolved every one of them as exact. Every `docs/api/internal.md` anchor in the summary verifies: `:94` UploadFiles, `:111`, `:147`, `:211` (inside `:209-215`), `:245`, `:273` (inside `:272-274`).

WATCHOUT: this lens has now returned empty on this proposal at least eleven times across both lanes. The proposal text is byte-identical to the round-start snapshot for this round (`diff -ru` against `non-spec-r2` and `non-spec-r2-start` prints nothing), so a future client-surface pass on unchanged text should spot-check the four load-bearing facts (no `sdks/`/`charts/` reference to `CoordinatorFence`, no docs mirror of the §4.1 classification, the proto biconditional, the 18-key reservation set) rather than re-derive them. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log-archive.md:3162

### [non-spec.2.review-docs-alignment.1]

DECISION: returned zero findings — BECAUSE the docs lens has no subject on this proposal: no runtime behavior changes, no identifier is renamed, no metric/alert/flag/endpoint moves, and `docs/` carries no mirror of the §4.1 classification. ALTERNATIVES: (1) filing the `docs/api/internal.md` staleness — declined, it is false against the shipped proto today, before anything staged applies, and the summary already records it with its owner; (2) filing the `docs/runbooks/coordinator-handoff-slow.md` cause list against the equal-generation re-fence refusal — declined, that refusal is pre-existing tree behavior 0076 landed, is routed to 0080 §1.16, and applying this proposal does not make the runbook wrong.

FACT: `docs/reference/adapter-contract.md` is a runtime-author page about the JSONL leg plus an RPC-name table. It never describes a gRPC request message's fields or its scope class, except the one `ReportSessionScrub` sentence a tier-11 gate pins. So the derivation rule, the addressing convention, and the retired table have no reader-facing carrier at all. Derived independently this round from the page itself, not from the log. — EVIDENCE: docs/reference/adapter-contract.md:59-81, :156, :282, :336-338

FACT: the tier-11 gate that pins `spec/04:725` against `docs/reference/adapter-contract.md:81` also requires both carriers to open the rule with the identical string `"The request is session-scoped: it is "`, so the wording is pinned character-for-character on both sides, not merely the substance. Any future edit that softens either row breaks it. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-77

FACT: `docs/api/internal.md` is named in two register files as well as the two tier-0 tests the summary lists: `tests/registers/residual-reserved-phrases.yaml:153` (a `lifecycle-channel` reserved-phrase exclusion) and `tests/registers/identifier-senses.yaml:227` (one CH-RUNTIMEOPS occurrence). Both key on channel spellings, neither reads a code block, and the page is not edited, so the summary's conclusion ("nothing this proposal applies reddens on account of the page") stands. Its wording "the only tests naming the page" is loose rather than load-bearing; examined and not filed. — EVIDENCE: tests/registers/residual-reserved-phrases.yaml:153, tests/registers/identifier-senses.yaml:227

FACT: `docs/reference/adapter-contract.md:69` does NOT contain the phrase "to the pod"; it reads "Announce new coordination generation on gateway handoff; precondition for any subsequent operational RPC". The summary calls it "the same compression" as `spec/04:712`, which is true at the level claimed (both drop the two per-session qualifying clauses), but a reviewer who expects the docs row to mirror `:712`'s "to the pod" wording will think the citation is wrong. It is not. — EVIDENCE: docs/reference/adapter-contract.md:69, spec/04_system-components.md:712

FACT: every `docs/api/internal.md` line the summary cites resolves exactly at HEAD: `:94` `rpc UploadFiles`, `:111`/`:147`/`:245`/`:273` the four other top-level `string session_id` declarations, `:210-211` the `CheckpointRequest` excerpt, `:272-274` `DemoteSDKRequest`. A `grep -n` settles all of them in one call; do not re-derive with `sed` ranges. — EVIDENCE: docs/api/internal.md:94,111,147,210,211,245,272,273

USEFUL [standing context, "No docs mirror"]: saved a full docs sweep. I spot-checked it three ways (grep for `session-scoped|pod-scoped` over `docs/`, grep for `Request Message Scope` repo-wide, and a read of the whole adapter-contract page) and it holds. Every `session-scoped` hit in `docs/` is about a JSONL frame or an upload token, never about a gRPC request message.

USEFUL [standing context, "Bash is the wrong tool for reading this log"]: correct and expensive to ignore. `awk`/`sed` over the standing context persisted to a file twice before I extracted it once to /tmp and read that.

### [non-spec.2.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the proposal adds, changes or removes was greppable to a closed set of sites, and every one of those sites is in the staged edit lists — ALTERNATIVES: rejected filing the `docs/api/internal.md` staleness (recorded in the summary with ownership, and wrong today rather than made wrong), the dropped Service/Direction columns (SC "Do not lean on direction survives in §4.7.1" already owns it and OD1 owns the judgement), and the unnamed replacement-gate case names (the coupling to `tests/spec-map.json` is stated and both land in one step, so it is a naming choice inside S2 rather than a cross-site blank).

FACT: the blast radius of the two tier-0 files is exactly five sites and nothing else in the tree. `grep -rn "adapter_proto_message_scope_test\|adapter_proto_parse_test" tests/ scripts/ cmd/ docs/ spec/ TEST-GAPS.md BUILD-GAPS.md` returns only `tests/spec-map.json:156`, `:169`, `:5670` and `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, `:337`. The two retiring case names likewise appear nowhere else. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336-337, tests/spec-map.json:156

FACT: `slotAddressCaseFiles` ALSO carries the tier-3 file at `:408-409`, so `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` reaches `session_address_wire_test.go` as well as the tier-0 gate. TEST-2 renames no function and changes no `// spec:` annotation, so it still owes no spec-map edit — but a future edit that adds or renames a case there drags that gate, not only `address_rule_citation_test.go:45-47`. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:408-409

FACT: the 18 keys of `sessionScopedMessages` are EXACTLY the 18 messages declaring `reserved "slot_id"`, verified set-equal by `awk '/^message [A-Za-z]+ \{/{m=$2} /reserved "slot_id"/{print m}' schemas/lenny-adapter.proto | sort`. That is the mechanical ground for TEST-2's split, and it is one command rather than a re-read of the map. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: a comment-stripping brace-counting Python parse of the proto reproduces the census exactly on the current tree: 31 request types, 25 declaring a top-level `SessionId session_id`, 26 `SessionId` declarations in total, and zero violations of the name/type biconditional in either direction. The six unaddressed are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. Spot-check with this rather than re-deriving by hand. — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: all eight proto range citations the fix stage added to TEST-2 are exact on the current tree, each message opening with `SessionId session_id = 1` and declaring no `slot_id`: `ListPlatformToolsRequest` 341-343, `CallPlatformToolRequest` 364-368, `ListSessionConnectorsRequest` 383-385, `ListConnectorToolsRequest` 403-406, `CallConnectorToolRequest` 419-424, `CoordinatorFenceRequest` 1455-1461, `ExportPathsRequest` 1538-1549, `ConfigureWorkspaceRequest` 1673-1684. Do not re-derive. — EVIDENCE: schemas/lenny-adapter.proto:1455

FACT: the fix stage's three new quote citations all verify verbatim — `spec/28_communication-channels.md:501` ("between the runtime adapter and the runtime binary inside one agent pod"), `:840-841` (address equality as exact string equality), and `tests/tier0_static/spec_map_slot_address_registration_test.go:91-92` ("credits that section with coverage no regression in it would break"). — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:89-93

FACT: `tests/claim-map.json` names `spec/04_system-components.md` exactly once, under §4.4, so retiring the §4.1 block orphans no claim row. One grep settles it. — EVIDENCE: tests/claim-map.json:24

FACT: every `§4.1` / `#41-edge-gateway-replicas` reference in `spec/02`, `spec/10:77`, `spec/16` (many rows) and `spec/18` is about subsystem-extraction thresholds and HPA metric roles. None reads the message-scope block, so the block's deletion moves no cross-reference. — EVIDENCE: spec/10_gateway-internals.md:77

WATCHOUT: `claim_register_proto_agreement_test.go` reads `protoFields` at two shapes, not one: the per-field lookup `fields[msg][field]` at `:72` and `:82`, and a range over the whole map at `:92-95` testing `declared[generationFenceField]`. A signature change that carries a field's type and `oneof` membership has to move BOTH, and a fixer who only patches the lookups leaves the fence-coverage half broken. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:92

WATCHOUT: `awk`-style one-liners that attribute a `reserved` line to the last `^message X {` are safe on this file only because every message is top-level. The same trick applied to a nested message would misattribute; combine it with the trap already recorded about same-line `message Foo {}`. — EVIDENCE: schemas/lenny-adapter.proto:508

USEFUL [Standing context, Traps, "Bash is the wrong tool for reading this log"]: exactly right, and it cost me three wasted calls before I read it. `awk`/`sed`/`cat` over the 168-line Standing context persists to a file, and reading that file persists again. The Read tool with `limit: 172` on the review log returns it in one call. Promote this entry.

USEFUL [Standing context, Settled, "Blast radius of the shared parse"]: saved a full re-derivation. My independent grep for every shared-parse symbol returned exactly the two callers it names and nothing else.

### [non-spec.2.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor the staged text assigns an
action to (the tier-0 rule gate over `schemas/lenny-adapter.proto`, the extended shared text
parse, `tests/spec-map.json`, the tier-3 protoreflect suite, the `spec/04` §4.1 block) can
perform it with data it can already see, and every citation I checked resolved exactly.
ALTERNATIVES: I worked up and then dropped four candidates, each listed below so nobody
re-derives them.

FACT: the adapter proto declares FOUR client-streaming RPCs, not one:
`rpc PrepareWorkspace(stream PrepareWorkspaceRequest)`, `rpc Attach(stream AttachRequest)`,
`rpc Checkpoint(stream CheckpointRequest)`, `rpc AdapterEvents(stream AdapterEventsRequest)`.
Only `CheckpointRequest` carries a `oneof`, so "stream request message" and "stream envelope"
are different populations and the envelope clause reaches one of the four. The other three are
decided by the field-set predicate and land where the retired table put them
(`PrepareWorkspaceRequest`/`AttachRequest` session, `AdapterEventsRequest` pod). A feasibility
reviewer who greps `rpc .*(stream ` gets three false envelope candidates in the first minute —
EVIDENCE: schemas/lenny-adapter.proto:41, :84, :133, :241 against the only `oneof` blocks at
:1174 and :1250

FACT: after TEST-1 deletes `tests/spec-map.json:5670`, section 28.5.3 carries ZERO
`tests/tier0_static/` entries (that line is its only one). Nothing breaks: the only
completeness validator is `validateSpecMapCoverage`
(`cmd/lenny-test/cmd_validate.go:801-834`), which requires one test entry of ANY tier or an
exception, and 28.5.3 keeps ~35 others. There is no per-tier floor anywhere in
`cmd_validate.go`. This refines the standing-context entry "deleting the §28.5.3 credit strands
no section", which is true but did not say the deleted entry is the section's only tier-0 one —
EVIDENCE: tests/spec-map.json:5646-5682, cmd/lenny-test/cmd_validate.go:815-820

FACT: neither section's `notes` string mentions the gate or the table (`4.1` has `notes: null`;
`28.5.3`'s names the JSONL contract), and `blocked_until_phase` is 4 for §4.1 and 2 for
§28.5.3, unchanged by the edit. So the spec-map edit is entries-only and raises no spec/18
phase-ordering question — EVIDENCE: tests/spec-map.json section "4.1" / "28.5.3"

WATCHOUT: `docs/api/internal.md` is not the only stale-looking site a feasibility lens will trip
on. `docs/api/internal.md:94` publishes `rpc UploadFiles(stream UploadChunk)`, which neither
service declares; the summary already records the whole page as an unstaged defect owned by the
docs loop, with the reason nothing reddens (the tier-11 code-block walker dispatches on json,
yaml, go, bash, and sql, never protobuf). Do not re-file it — EVIDENCE:
tests/tier11_docs/code_blocks_test.go:102-115

WATCHOUT: the summary cites that walker as `:103-113`, but `case "sql"` / `return
checkSQLBlock` sit at :114-115, so the cited range is two lines short of the enumeration it
supports. The substance (no `protobuf` case exists) is exact. I declined to file: the range is
off, not the claim. If a later lens files it, expect a refutation on "does not make the applied
spec or implementation wrong" — EVIDENCE: tests/tier11_docs/code_blocks_test.go:114-115 against
0075_...summary.md:214-216

USEFUL [Traps: "Four of the five loops over `slotAddressCaseFiles` force the kept path"] — I
re-verified all five and the entry is exact: :973, :1028, :1054, :1096 reach `repoFileLines`
(:699-706) and `t.Fatalf` on an unreadable file, while
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108-1132) is one-directional
(`if !inventory[file]`), so keeping the gate's path is sufficient and a surplus inventory entry
would not fail either way. That saved a full re-derivation of a 1300-line file.

USEFUL [Traps: "Naive proto parsers derail on this file"] — I stripped comments and counted
braces per line on the first try because of it. My census reproduced the standing one exactly:
31 request types, 25 addressed, the six unaddressed as listed, the 18 `reserved "slot_id"`
messages exactly equal to today's `sessionScopedMessages` keys, and the 8 additions exactly as
`non-spec-changes.md` §5 names them. Nobody needs to run this again.

FACT: the addressing convention the replacement gate holds is green on the shipped proto in both
directions — no field named `session_id` carries a type other than `SessionId` and no field of
type `SessionId` carries another name, 26 declarations in all including the `oneof`-arm message
`CheckpointStart`. So the gate passes on day one and the red window is S1-only, as the checklist
says — EVIDENCE: schemas/lenny-adapter.proto, grep `^\s*(repeated\s+)?SessionId\s+\w+\s*=`


### [non-spec.2.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every citation and mechanism in the staged non-spec text (TEST-1, TEST-2) and in the spec-side text it depends on verified exactly against the tree, by independent derivation rather than by trusting the standing context — ALTERNATIVES: filing the D2 "nothing checks today" overstatement (declined: it is decision rationale in `spec-changes.md`, it never lands in `spec/`, and it has been filed twice and declined six times; it is already carried as a DEFERRED), filing `status.md:22`'s dangling `§11` (declined: proposal metadata, already a DEFERRED, makes no applied text wrong), and filing §8's stale `IMPLEMENTOR TO FILL THE BLANKS` banner (declined: wording, and seven test-coverage passes judged the five enumerated refusal subjects sufficient).

FACT: the two long-standing UNVERIFIED items in the standing context are now settled, both in the proposal's favour. `message SessionId {` is at `schemas/lenny-adapter.proto:596` (`grep -n '^message SessionId'`), so every proposal citation of `:596` is exact. And `spec/` does name a test tier today: `spec/18_build-sequence.md:85` reads "Tier 0 (Static) and Tier 11 (Documentation) pass on the empty repository" and `:98` reads "Tier 0 (Static) passes", so SPEC-1's "A tier-0 gate refuses a protocol definition..." has precedent. The earlier greps that "found nothing in spec/" were searching the lowercase hyphenated spelling; the spec spells it `Tier 0 (Static)`. — EVIDENCE: schemas/lenny-adapter.proto:596, spec/18_build-sequence.md:85, spec/18_build-sequence.md:98

FACT: one Python pass over `schemas/lenny-adapter.proto` (comments stripped, brace-depth per line, oneof depth tracked separately from message depth) reproduces every population claim at once and costs one tool call. It returned: 31 RPC request types; 25 declaring a top-level `SessionId session_id`; the 6 that do not are exactly `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; the 8 additions TEST-2 names are exactly the addressed request types missing from today's 18-key map; `CheckpointStart` is the only current key that is not a request type; the only `oneof` messages are `CheckpointRequest` and `CheckpointResponse`; zero name/type biconditional violations; 26 `SessionId` declarations in the file. A second pass confirmed the 18 messages declaring `reserved "slot_id"` are exactly the 18 current map keys, with no symmetric difference either way. — EVIDENCE: schemas/lenny-adapter.proto:341-343, :364-368, :383-385, :403-406, :419-424, :1455-1461, :1538-1549, :1673-1684

FACT: the whole blast radius of the retiring gate is four sites and nothing else. `grep -rn "TestAdapterProtoRequestMessagesAreClassifiedByScope\|TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage\|adapter_proto_message_scope"` over `tests/ pkg/ cmd/ scripts/ docs/ spec/ charts/ TESTING.md BUILD-GAPS.md TEST-GAPS.md`, excluding the gate file itself, returns `tests/spec-map.json:156`, `:169`, `:5670`, and `tests/tier0_static/spec_map_slot_address_registration_test.go:336`. `tests/change-graph.json` names none of them. The equivalent grep for the tier-3 file returns the five whole-file spec-map credits (`:172`, `:600`, `:1110`, `:1391`, `:3678`), `spec_map_slot_address_registration_test.go:409`, and `address_rule_citation_test.go:45`, none of which TEST-2 disturbs. §9's file list is complete. — EVIDENCE: tests/spec-map.json:156, tests/tier0_static/spec_map_slot_address_registration_test.go:336, tests/tier0_static/spec_map_slot_address_registration_test.go:409

FACT: no `docs/` page and no `spec/` section outside §4.1 classifies a gRPC request message. A grep for `pod-scoped|session-scoped` over `docs/ schemas/ charts/` returns only JSONL-frame and upload-token uses plus `docs/reference/adapter-contract.md:81` (the `ReportSessionScrub` row SPEC-1 keeps). A grep for `§4.1|#41-edge-gateway-replicas` across `spec/` filtered for scope/table/classification words returns HPA-metric and LLM-proxy cross-references alone: nothing in `spec/` points at the message-scope table. No inbound reference is stranded by the retirement. — EVIDENCE: docs/reference/adapter-contract.md:81, spec/04_system-components.md:59

WATCHOUT: the orchestrator brief for this round is stale on OD5. It says OD5 "stays open", but the working tree carries an uncommitted summary edit that deletes the whole OD5 block from `## Open decisions for human to make`, paired with a new `non-spec-changes.md` §5 paragraph ("The session set stays hand-entered...") that stages the answer as a requirement. The only live open decision in the summary now is OD1. Nothing else in the proposal directory still cites OD5, so the deletion stranded no pointer. Check `git status --short` and `git diff` before trusting any brief statement about which decisions are open. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:83

WATCHOUT: `spec/28_communication-channels.md:501` is the line the §28.5.3 boundary sentence *starts* on and the quoted phrase's last word ("pod") wraps to `:502`. A reader who prints only `:501` and looks for the whole quoted phrase will read the citation as false. It is not: the repo's convention cites the line a sentence opens on. The same hazard applies to the `:840-841` "Address equality" citation, which is one numbered item wrapped over two lines. — EVIDENCE: spec/28_communication-channels.md:501, spec/28_communication-channels.md:840

USEFUL [Traps]: the "naive proto parsers derail on this file" entry and the "`sed` with several `p` ranges emits lines in file order" entry between them saved a full round. The brace-delta-with-same-line-close approach the trap prescribes produced a correct census on the first attempt, and printing one `sed` range at a time kept every anchor honest.

USEFUL [Settled]: the `slotAddressCaseFiles` blast-radius entry and the spec-map registration entry were both exactly right and both were cheap to spot-check (two greps). Spot-checking rather than re-deriving them is the right instruction and it held.

OPEN: the D2 correction is still unapplied. `spec-changes.md:38-39` calls the addressing convention "an addressing convention that nothing checks today", while the newest paragraph in `non-spec-changes.md` §5 correctly describes `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1`'s type comparison as an existing check over a hand-maintained subset. The two halves of the document now disagree in wording. Below the finding bar (D2 never lands in `spec/`), but whoever touches D2 next should still land the one-sentence correction the standing context's DEFERRED already states.

### [non-spec.2.review-kubernetes.1]

DECISION: returned empty — BECAUSE the Kubernetes-idiom lens has no surface on this proposal at all: every staged deliverable lands in spec/04 §4.1 prose about gRPC request-message classification, a tier-0 gate parsing `schemas/lenny-adapter.proto`, and a tier-3 protoreflect suite. No CRD, status/spec subresource, field manager, finalizer, controller, reconciler, informer, work queue, leader election, admission webhook, or apiserver client appears in any staged edit or in any file the edit lists name — ALTERNATIVES: rejected filing on the session-scoped fence clearing a pod-wide hold (Standing context bars it, four lenses already stopped there, and it is 0076's landed behavior rather than anything this proposal stages); rejected filing on the loss of the table's service/direction columns (that is content loss, not a surface that becomes wrong, and OD1 owns the judgement).

FACT: the whole Kubernetes-idiom lens can be cleared on this proposal by one keyword sweep plus two spot-checks. `grep -niE "controller|status|CRD|finaliz|apiserver|reconcil|webhook|admission|etcd|informer|watch|leader|RBAC|kubernetes|NetworkPolicy|CustomResource"` over all five proposal files returns only "reconciliation" used for the retiring table-versus-proto gate and "Status" as a metadata heading. The coordination fence never touches the apiserver: the hold is enforced by in-process gRPC interceptors and the generation is recorded on an in-memory slot entry — EVIDENCE: pkg/adapter/holdstate.go:335 (`holdStateUnaryInterceptor`), :348 (`holdStateStreamInterceptor`), both gated on `s.inHoldState()` at :336/:349; pkg/adapter/coordination.go:116 (`boundSlotState`), :156 (`exitHoldState`)

WATCHOUT: the Standing context cites the hold interceptors as `pkg/adapter/holdstate.go:336`/`:349` while `summary.md` cites `:335`/`:348`. Both are correct and neither is a defect: :335/:348 are the `func` declaration lines and :336/:349 are the `s.inHoldState()` predicate lines inside them. Do not file this as an off-by-one, and do not "fix" either citation toward the other — EVIDENCE: pkg/adapter/holdstate.go:335-336, :348-349

USEFUL [Standing context, Traps]: the "do not re-file the session-scoped fence exiting a pod-wide hold" trap is the exact entry this lens would otherwise have spent its whole budget on — a fence classified session-scoped that clears a pod-level interceptor gate is the shape a Kubernetes-idiom reviewer reaches for first. The trap named the ground (0076's OD3 weighed and rejected it, D3 records the rejection, §6 makes re-deriving it a non-goal) and saved a full derivation pass.

USEFUL [Standing context, Traps]: the "snapshot diffs are routinely empty" entry. `diff -ru scratchpad/cp-snap/.../non-spec-r2 proposals/...` printed nothing this round and the snapshot directory is byte-identical to the live one; without that entry I would have spent a call proving the snapshot was not broken.

OPEN: for the orchestrator rather than a later round — this lens is listed "always run", but on a proposal whose entire blast radius is proto prose plus two test files it can only ever return empty. If it fires again on 0075 with the same three deliverables staged, the answer is the cached JSON at `scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/kubernetes-r2-d91c9fcab1da.json`; re-running it costs a round slot for a lens with no subject.

### [non-spec.2.review-mechanism.1]

FACT: The widened tier-3 session set is EXACTLY the derived set, verified mechanically rather than by
enumeration. A brace-counting parse of `schemas/lenny-adapter.proto` (comments stripped, same-line
`{}` handled) gives 31 RPC request types, 25 with a top-level `session_id`. The 17 request types among
today's 18 `sessionScopedMessages` keys plus TEST-2's 8 named additions equals those 25 exactly, with
empty symmetric difference in both directions. The 18 messages declaring `reserved "slot_id"` are
exactly today's 18 keys, so `retiredDuplicateNumbers` is a faithful split and the widened set is a
strict superset of it, meaning no member of the reservation population loses address-arm coverage.
— EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: The addressing-convention biconditional is green on the shipped proto, re-verified two ways.
`grep -c "SessionId session_id"` returns 26; no field named `session_id` carries another type
(`grep session_id | grep '= N;' | grep -v 'SessionId session_id'` is empty) and no field of type
`SessionId` carries another name. D2's replacement gate passes on day one.
— EVIDENCE: schemas/lenny-adapter.proto

FACT: The whole blast radius of TEST-1 is five files and nothing else in the tree names the gate, the
parse, or the two retiring case names. `grep -rn "adapter_proto_message_scope_test\|adapter_proto_parse_test"`
outside `proposals/` and `scratchpad/` returns only `tests/spec-map.json:156`, `:169`, `:5670` and
`tests/tier0_static/spec_map_slot_address_registration_test.go:336`, `:337`. `grep` for the two case
names returns the same three spec-map lines plus their declarations. `tests/change-graph.json` and
`tests/claim-map.json` name none of the touched files. `grep -rn "04_system-components.md:[0-9]"` over
`spec/ docs/ schemas/ charts/ tests/ pkg/ cmd/` is empty, so no line citation shifts when the block
shrinks. §9's file list is complete.

FACT: Every citation added in this window verifies. Checked line by line and each is exact:
`spec_map_slot_address_registration_test.go:336`, the five loops `:973/:1028/:1054/:1096/:1110`,
`repoFileLines` `:699-706`, the credit-doctrine quote `:89-93`, `derivedInventoryCaseFiles` `:1212`
with its "entered by hand" quote at `:1209-1211`; `tests/spec-map.json:156`, `:169`, `:5670` (block
opens `:5643`); `spec/28:501` and `:840-841`; all eight proto ranges for the additions
(`:341-343`, `:364-368`, `:383-385`, `:403-406`, `:419-424`, `:1455-1461`, `:1538-1549`, `:1673-1684`)
land exactly on `message ... {` through the closing brace; the whole summary "Defects in the shipped
tree" block (`docs/api/internal.md:94/:111/:147/:209-215/:245/:272-274`,
`schemas/lenny-adapter.proto:32/:261/:1173-1187/:1694-1698`,
`tests/tier11_docs/code_blocks_test.go:103-113`, `spec_47_rpc_row_naming_test.go:35`, `spec/04:712`,
`spec/28:314-317` and `:322`, `spec/10:39`, `pkg/adapter/coordination.go:109-111/:127-134/:150-156`,
`coordfence.go:171-179/:180-183` with no `time.` in the file, `pkg/adapter/slot.go:59`,
`proposals/0073_...md:6653-6655` and `:6686`, 0080's `§1.1`-`§1.21` and its §2 pair naming 0075).
Spot-check rather than re-derive.

WATCHOUT: `pkg/adapter/holdstate.go:335`/`:348` in the summary and `:336`/`:349` in the review log's
standing context are BOTH correct and are not in conflict. `:335` and `:348` are the two interceptor
function declarations; `:336` and `:349` are the `s.inHoldState()` guards inside them. The summary
cites the interceptors, the log cites the guard. Do not "correct" either toward the other.
— EVIDENCE: pkg/adapter/holdstate.go:335-336, :348-349

WATCHOUT: `grep -rln "docs/api/internal.md" tests/` returns four files, not the two the summary names.
The extra two are `tests/registers/identifier-senses.yaml` (a CH-RUNTIMEOPS occurrence row) and
`tests/registers/residual-reserved-phrases.yaml` (an excluded `lifecycle-channel` anchor row). Both are
registers consumed by the anchor and naming gates, so the summary's load-bearing claim — that nothing
reading the page reads its code blocks — still holds and the "only tests" wording is data, not a
finding. Checked and declined; do not re-file.
— EVIDENCE: tests/registers/identifier-senses.yaml:227-229, tests/registers/residual-reserved-phrases.yaml:152-157

DECISION: filed nothing. BECAUSE every mechanism the proposal describes traces end to end and the
staged non-spec text agrees with the staged spec text on all four gate clauses. Predicate drift was the
main hunt: the spec block's paragraph 3 (four refusal arms), D2's prose, and §8 Testing's four required
failures state the same four conjuncts with the same "request message" restrictor and the same
"top-level" qualifier on the envelope's own address. ALTERNATIVES considered and rejected:
(a) applying the proposal's own credit doctrine to the surviving section 4.1 registration the way it is
applied to delete the 28.5.3 one — the two differ substantively, because a departure from the
convention §4.1 states does redden the gate while nothing §28.5.3 states can, so the asymmetry is
reasoned rather than inconsistent;
(b) the spec text's "that frame ... opens the stream" being unheld by any gate — the derivation keys on
which frame declares the address, never on which opens the stream, so the clause is descriptive and
load-bears nothing;
(c) "the set holds every message that rule addresses to a session" reading as false of `CheckpointRequest`
— the same paragraph's earlier "addresses to a session directly" and the explicit named exception two
paragraphs up settle it; wording, not a defect.

USEFUL [standing context: cache keys omit summary.md]: the cache miss was real (H=d91c9fcab1da, no file),
but the entry is why I did not go looking for a near-miss hit under another lane's hash.

USEFUL [standing context: naive proto parsers derail on this file]: the same-line `message X {}` warning
saved the census parse. Counting braces per line with comment stripping reproduced 31/25/6 on the first
run.

### [non-spec.2.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE the proposal touches no metric, no alert rule, no CRD condition, no runbook, and no operator-facing page, and every operator-adjacent citation it does make verifies exactly — ALTERNATIVES: rejected filing the §4.1 table's lost Service/Direction columns as an operator-facing information loss (OD1 owns it, and nothing in `docs/` or `spec/` reads those columns); rejected filing the §8/§9 tension over "any reader-facing page that restates the classification" (the conditional resolves to none).

FACT: the operational surface of this proposal is empty, and here is the enumeration so nobody re-derives it. `grep -rn "pod-scoped\|session-scoped" docs/` returns 13 hits, none of which restates the §4.1 request-message classification: `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` row SPEC-1 explicitly keeps, `:158` is a JSONL frame sentence, and the rest are glossary/upload-token/runtime-author-guide frame text. `docs/reference/metrics.md:180` and `:309` and `spec/16_observability.md:185`, `:189` are the only metric rows in the area (`lenny_adapter_unaddressed_frame_rejected_total`, `lenny_adapter_coordinator_hold`); both are JSONL-frame or hold-state metrics that no staged edit reaches. `pkg/alerting/rules/rules.go:1582` `CoordinatorHandoffSlow` is the delegation parent→child handoff, a different mechanism from `CoordinatorFence`, and `docs/runbooks/` names no fence, no hold, and no message scope. — EVIDENCE: docs/reference/metrics.md:309, spec/16_observability.md:185, pkg/alerting/rules/rules.go:1582, docs/runbooks/index.md:95

FACT: "session-scoped frame" on the JSONL leg is defined independently of the §4.1 table, by enumeration at `spec/28_communication-channels.md:834-836` ("This rule governs the session-scoped frame types alone, `message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, and `status`"). So retiring the §4.1 table cannot orphan the `lenny_adapter_unaddressed_frame_rejected_total` description in `docs/reference/metrics.md:180` or `spec/16_observability.md:189`. This is the one plausible-looking operational finding on this proposal and it is refuted. — EVIDENCE: spec/28_communication-channels.md:835

FACT: the TEST-2 widened-set arithmetic is mechanically exact, verified by a fresh comment-stripping brace-depth parse of `schemas/lenny-adapter.proto`. 31 RPC request types; 25 declare a top-level `SessionId session_id`; the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. The 17 request types currently in `sessionScopedMessages` plus the 8 additions named at `non-spec-changes.md:74-76` equal that 25 set exactly, with empty difference in both directions. The same parse over every message including `oneof` arms returns zero name/type convention violations, so D2's replacement gate is green on the shipped proto. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:43-63, schemas/lenny-adapter.proto

WATCHOUT: the summary cites `pkg/adapter/holdstate.go:335`, `:348` for the pod-level hold interceptors while the standing context's own trap entry cites `:336`, `:349`. Both are correct: `:335` and `:348` are the `holdStateUnaryInterceptor` and `holdStateStreamInterceptor` function declarations, `:336` and `:349` are their `s.inHoldState()` guard lines. Do not file this as an off-by-one and do not "fix" either number. — EVIDENCE: pkg/adapter/holdstate.go:335, pkg/adapter/holdstate.go:336, pkg/adapter/holdstate.go:348, pkg/adapter/holdstate.go:349

FACT: every operator-adjacent citation in the staged text and in the summary's unstaged-defect entries verifies as written: `spec/10_gateway-internals.md:57` (the fence is the only exit from hold state) and `:60` (the hold and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped) are both the last clause of a very long line; `spec/28_communication-channels.md:314-317` (per-session generation), `:322` (hard precondition), `:501` (the intra-pod boundary sentence), `:840-841` (frame address equality); `spec/04_system-components.md:725`/`:726` (both inside §4.7.1, whose heading is at `:692` and whose successor `#### 4.7.2` is at `:728`); `pkg/adapter/coordination.go:109-111`, `:116`, `:127-134`, `:150-156`; `tests/spec-map.json:156`, `:169`, `:5670`; `tests/tier0_static/adapter_proto_parse_test.go:10-15` and `:64-67` (both name the §4.1 table as their subject). — EVIDENCE: spec/10_gateway-internals.md:60, spec/28_communication-channels.md:322, spec/04_system-components.md:692

FACT: section 4.1 of `tests/spec-map.json` carries no `notes` key at all and section 28.5.3's `notes` names only the JSONL contract, so TEST-1's re-point and delete are entries-only and strand no prose. Confirmed by loading the JSON rather than by grep. — EVIDENCE: tests/spec-map.json:152, tests/spec-map.json:5643

USEFUL [standing context, Traps]: the "Bash is the wrong tool for reading this log" entry is right and under-stated. `sed`/`awk` over the standing context persists to a file, and re-reading that file persists again; the working recipe is `sed -n '20,172p' <log> > /tmp/sc.txt` once, then `sed -n 'A,Bp' /tmp/sc.txt | cut -c1-700` in ~30-line slices.

USEFUL [standing context, Settled: "No docs mirror"]: saved this lens the whole docs sweep; I re-verified it in one grep rather than re-deriving.

### [non-spec.2.review-performance.1]

DECISION: returned an empty findings list, the seventh consecutive empty result for this lens on 0075 — BECAUSE the joint spec+non-spec staging still touches only `spec/04_system-components.md`, three tier-0 test files, `tests/spec-map.json`, and one tier-3 test file (`spec-changes.md` §9), so the numerator of every quantity this lens measures is zero: no per-task, per-request, or per-session write onto etcd, Postgres, or Redis; no net-new informer, watch, lease, or work queue; no hot key; no connection-pool consumer; no reconcile path. ALTERNATIVES: re-filing the `spec/10:39` equal-generation re-fence refusal and the missing 1-second backoff (rejected: pre-existing, recorded in `summary.md` under `## Defects in the shipped tree that this proposal does not stage`, refusal half routed to 0080 §1.16 and backoff half recorded as unowned); re-filing the session-scoped fence exiting a pod-wide hold (rejected: standing-context trap, 0076's landed behavior).

FACT: `diff -rq scratchpad/cp-snap/.../non-spec-r2 proposals/0075_...` exits 0 with no output — the live directory is byte-identical to this round's start snapshot, including `summary.md` and `review-log.md`. There was no fix stage between the r2 snapshot and this read, so "read the changed sections hardest" had no subject this round. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md (mtime 2026-09-08 05:35, older than the snapshot read)

FACT: retiring the §4.1 table orphans no metric or alert. The only `session-scoped` metric in `spec/16` is `lenny_adapter_unaddressed_frame_rejected_total` (`spec/16_observability.md:189`), whose subject is explicitly a §28.5.3 JSON Lines frame ("rejects a session-scoped [§28.5.3] frame carrying no per-session identifier"), defined by `spec/28:835-844`, not by the §4.1 request-message classification. A grep of `spec/` for `session-scoped` returns 30-odd sites and none of the others reads the §4.1 table either. So the classification has no observability consumer and SPEC-1 owes `pkg/alerting/rules`, `docs/reference/metrics.md`, and `docs/runbooks/` nothing. EVIDENCE: spec/16_observability.md:189, spec/28_communication-channels.md:838-844

FACT: the summary's fence-retry defect entry re-verifies exactly, this round, at every anchor. `coordfence.go:155` opens `for attempt := 1; attempt <= f.maxAttempts`, `:160-163` is the accept arm, `:164-179` the `FailedPrecondition`/not-accepted stale arm that re-reads `CoordinationGeneration` and relinquishes when it has not advanced, `:180-183` the `default` transient arm that continues at the same `gen`, and the file carries no timer or sleep. The trace the summary states (landed-but-unacked fence refused stale on attempt 2, relinquish there rather than at the budget of 3) is what the code does. EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-189

FACT: the reclassification imports no store-backed value and therefore no §12.4 durable-fallback obligation. The only behavioral obligation a session-scoped classification carries is `spec/05:515`'s empty-identifier refusal, which the handler takes in-process at `pkg/adapter/coordination.go:109-111` before `boundSlotState` at `:116`, so it neither reads nor writes Postgres, Redis, or etcd and degrades identically under a Postgres failover, a Redis reset, and a coordinator handoff. EVIDENCE: pkg/adapter/coordination.go:109-116

USEFUL [Standing context / Traps / coordfence driver]: naming `DefaultMaxAttempts = 3` at `:52`, the arms at `:171-179` and `:180-183`, and the warning that the 5-second deadline lives in `adapterclient/coordinatorfence.go` rather than in `coordfence.go` again pre-empted the false "unbounded fence RPC" finding this lens reaches for first. Third recorded save on that one entry.

USEFUL [Standing context / Traps / Bash is the wrong tool for reading this log]: reading the standing context as `sed -n 'A,Bp' ... | cut -c1-1500` in 50-line windows is what worked; two unclipped reads persisted to files and cost two wasted calls before I clipped.

WATCHOUT: the cache slot `performance-r2-d91c9fcab1da.json` is shared with any other lane running the `performance` lens at round 2 over the same staging hash, and the key still omits `summary.md`. This round's hash `d91c9fcab1da` is identical to the one `non-spec.1.review-performance.1` recorded, which confirms the staged text has not moved across the whole non-spec loop. EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r2-d91c9fcab1da.json

### [non-spec.2.review-reliability.1]

DECISION: returned no findings — BECAUSE the three staged deliverables change spec prose and two test
files and add no runtime operation, so this lens has no retried, replayed, leased, drained, or
crash-resumed operation to trace; and every reliability-flavoured factual claim the document makes
verifies at the line cited — ALTERNATIVES: re-filing spec/10:39's unmet retry clause (already this lens's
own [spec.4.review-reliability.1], recorded Open and out-of-scope, with the refusal half owned by 0080
§1.16 and the backoff half recorded as unowned) and re-filing the session-scoped fence exiting a pod-wide
hold (an explicit standing-context trap, declined by four lenses).

FACT: the whole fence/recovery anchor set in the summary's unstaged-defects entry re-verified clean this
round, so a later reliability pass should spot-check rather than re-derive it —
EVIDENCE: pkg/adapter/coordination.go:107 (handler), :109-111 (empty-id InvalidArgument), :116
(boundSlotState), :127-134 (`coordinator_handoff_stale` FailedPrecondition on `gen <= lastFenced`),
:150-156 (exitHoldState after a fence for any bound session);
pkg/adapter/slotsession.go:274-283 (boundSlotState returns FailedPrecondition "session %s is not assigned
to this pod" when nothing resolves, which is what problem-statement §1.2's "resolving nothing is the
refusal" asserts);
pkg/gateway/coordination/coordfence/coordfence.go:52 (`DefaultMaxAttempts = 3`), :171-179 (stale arm
re-reads, finds no advance, relinquishes), :180-183 (transient arm retries at the unchanged `gen`), and
`grep -n "time\.\|Sleep\|backoff"` over that file returns nothing;
pkg/gateway/runtime/adapterclient/coordinatorfence.go:20 (`CoordinatorFenceTimeout = 5 * time.Second`)
applied at :49;
spec/10_gateway-internals.md:38 (fence is the hard precondition), :39 (3 attempts, 1-second backoff),
:40 (`last_fenced_generation` per bound session, not surviving an unbind).

FACT: the summary's "Source: proposal 0076's OD2" attribution for 0080 §1.16 is exact —
EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:268-269
("**Source:** proposal 0076's OD2, which derived the remedy, recommended that a successor own it, and left
the successor unnamed"). §1.16 itself opens at :216 and its remedy bullets at :236-248 name the handler
comparison, the `CoordinatorFenceResponse` wire comment, and the §28.5.1/§28.6/§28.8/§29.8 arms, which is
what the summary compresses to "the §10.1.2, §28, and §29.8 arms".

WATCHOUT: the summary cites the hold interceptors as pkg/adapter/holdstate.go:335 and :348 while the
review log's standing context cites :336 and :349. Both are right and neither is a finding: :335/:348 are
the two `func (s *Server) holdState*Interceptor` declarations and :336/:349 are their
`s.inHoldState() && !coordinatorHoldAllowedMethods[...]` predicate lines. Do not file an off-by-one here.

USEFUL [standing context, "coordfence driver"]: the note that the 5-second deadline lives one layer down in
adapterclient rather than in coordfence.go is what stops a false "unbounded outbound RPC" finding; this
lens confirmed it and would have grepped only coordfence.go otherwise.

USEFUL [standing context, "Cache keys omit summary.md"]: the cache slot for this lens/round was empty, but
the warning that a hit can come from the other lane is the reason this run checked the key before trusting
anything; the key computed to d91c9fcab1da and the written file is
scratchpad/cp-cache/0075_.../reliability-r2-d91c9fcab1da.json.

### [non-spec.2.review-security.1]

DECISION: returned an empty findings list — BECAUSE the only security-bearing control this proposal touches is the session-address presence guard, and every change to it is strictly widening: the derived session class is a superset of the retired table's (only `CoordinatorFenceRequest` moves, pod to session), so the `spec/05_runtime-registry-and-pool-model.md:515` empty-identifier refusal can only be added and never removed, and the widened tier-3 arms cover 26 messages where they covered 18 — ALTERNATIVES: rejected filing on the hand-maintained widened session set (that is OD5, routed to the human, and the current set is already hand-maintained, so the change is not a regression); rejected filing on the retired table's human-classification step disappearing (the summary's 0073 impacts row already records "the human classification step does not [carry over]", and under a derived rule a message cannot go unclassified at all).

FACT: the fence's security-relevant refusals are all live in the tree and correctly cited by the summary. `pkg/adapter/coordination.go:109-111` refuses an empty session id with `InvalidArgument`, `:116` resolves the bound slot entry, `:127-134` returns `FailedPrecondition` + `coordinator_handoff_stale` on `gen <= lastFenced`, `:150-156` exits the pod-scoped hold. `pkg/gateway/coordination/coordfence/coordfence.go` has zero `time.` references, confirming the summary's "no backoff of any kind" claim. — EVIDENCE: pkg/adapter/coordination.go:109-156, pkg/gateway/coordination/coordfence/coordfence.go:160-188

FACT: the two retiring gate function names appear NOWHERE in the tracked tree outside their own file except `tests/spec-map.json:156`, `:169`, `:5670`. A `grep -rn <name> --include=*.json tests/ pkg/ cmd/ charts/ docs/ spec/` returned nothing while `grep -rn <name> tests/spec-map.json` returned the three hits; the `--include` plus multiple-dir form silently missed the JSON. Use the plain per-file grep to confirm blast radius, or you will conclude there is no spec-map dependency. — EVIDENCE: tests/spec-map.json:156, tests/spec-map.json:5670

FACT: nothing in `spec/`, `docs/`, `schemas/`, or `charts/` references the §4.1 message-scope table. `grep -rn "message-scope\|message scope\|scope table\|classification table"` over those trees returns only unrelated §12.3/§12.8/§12.9 tables plus the gate file's own comments and the tier-3 comment TEST-2 rewrites. No inbound-reference edit site is missing. — EVIDENCE: spec/04_system-components.md:151, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40

WATCHOUT: `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` tests for a field literally named `slot_id` (`:32`) and nothing else, so a message omitted from the widened hand set that declared `string slot_id` would escape both that arm and the `SlotId`-wrapper bar at `:150-158`. This is a real residual hole, but it is exactly OD5's subject and it is not a regression (the set is hand-maintained today too), so it is not filable under the "merely less strict than it could be is NOT a finding" rule. Do not file it as a security finding. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:32

USEFUL [standing context, Traps]: "Cache keys omit summary.md and review-log.md, and the cache path carries no lane" and "Long lines hide the sentences citations point at" both paid off. Every long-line citation I checked (`spec/28:501`, `:840-841`, `docs/api/internal.md:209-215`) verified once read with `sed -n Np` rather than a truncated grep.

FACT: all 25 proto anchors, all tier-0/tier-3 test anchors, all three `tests/spec-map.json` anchors, and the `docs/api/internal.md` staleness citations in the summary's unstaged-defects section verify exactly as written. `tests/tier11_docs/code_blocks_test.go:102-113` dispatches on json/yaml/go/bash/sql only, so no tier-11 case parses the stale `protobuf` fences. Nothing in the security lens needs re-verifying next round. — EVIDENCE: tests/tier11_docs/code_blocks_test.go:102

### [non-spec.2.review-test-coverage.1]

DECISION: returned an empty findings list for the test-coverage lens on the non-spec staging — BECAUSE every behavior the three deliverables change maps to a tier §8 lists, and §8's five enumerated tier-0 refusal subjects are exactly one per clause of D2/D1¶3 with "other than exactly one address" split into zero and two — ALTERNATIVES: (a) filing that §8 names no green-baseline case for the replacement gate on the shipped proto; rejected because the gate IS the green assertion (it reads `schemas/lenny-adapter.proto` and fails tier 0 on any violation), so the positive case is not a separate test to list. (b) filing that §8 names no test for the surplus-credit direction of the spec-map edit (leaving `tests/spec-map.json:5670` re-pointed instead of deleted is caught by nothing); rejected as new-gate scope and hypothetical hardening rather than coverage the changed behavior requires. (c) filing that §8 names no test for the `spec/05:515` empty-identifier refusal the reclassification imports onto `CoordinatorFenceRequest`; rejected on the tree, see FACT below.

FACT: the fail-closed path the fence's reclassification imports is already implemented and already pinned, so this proposal owes no new test for it. `pkg/adapter/coordination.go:109-111` returns `codes.InvalidArgument` on an empty `session_id` before `boundSlotState` at `:116`, and `TestCoordinatorFenceRejectsMissingSessionID` asserts it. A neighbouring case, `TestCoordinatorFenceRejectsZeroGeneration` (`pkg/adapter/coordination_test.go:48`), pins the positive-generation refusal. — EVIDENCE: pkg/adapter/coordination.go:107-122, pkg/adapter/coordination_test.go:35-42, :48-57

FACT: §8's four enumerated tier-0 refusal subjects line up one-to-one with the four clauses D1 paragraph 3 states and D2 restates, with the fourth ("other than exactly one address") split into a zero-address and a two-address case, giving five subtests. That is the same shape the retiring gate already has: one real-proto case plus one self-test carrying five refusal subtests in a table (`cases` map). An implementor reproducing that file's structure satisfies §8 exactly. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:151-201, non-spec-changes.md:99-107

FACT: the five `slotAddressCaseFiles` loop-site citations in TEST-1's fix-stage paragraph all resolve exactly. `slotAddressCaseFiles` names the gate file at `:336`; the loops are `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` `:973`, `TestAddressCaseNamesAgreeWithTheirOwnCitations` `:1028`, `TestAddressCaseTextCitesNoProposalSectionNumber` `:1054`, `TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly` `:1096`, and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` `:1110`; `repoFileLines` is `:699-706`. The corrected "the fifth resolves no listed path through `repoFileLines`" reading holds: `:1110`'s loop only inserts each path into a set. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336, :699-706, :973, :1028, :1054, :1096, :1108-1115

FACT: `declaredScope` is exactly `:75-81` and `parseMessageScopeTable` opens at `:54`, both as TEST-1 cites them; the gate file's 0073 limit paragraph is exactly `:17-27`. Counted line by line rather than trusted. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:17-27, :54, :75-81

FACT: TEST-2 loses no coverage. The reservation arm's population is unchanged at 18 (`grep -c 'reserved "slot_id"' schemas/lenny-adapter.proto` returns 18, matching the map's 18 keys including `CheckpointStart`, which reserves 6 and the name at `:1210-1211` and declares `SessionId session_id = 7` at `:1217`), and the two address arms only widen, 18 to 26. No message drops out of any arm, so the split introduces no coverage regression a test would have to catch. — EVIDENCE: schemas/lenny-adapter.proto:1210-1217, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

USEFUL [standing context, "§8's stale framing"]: it records that seven earlier test-coverage passes judged §8's five enumerated case subjects sufficient and that the surviving problem is stale wording (`**IMPLEMENTOR TO FILL THE BLANKS.**` plus "written during convergence") rather than a coverage gap. That kept this pass from re-filing the same thing an eighth time, and it is still the right reading: convergence has closed and §8 names no case by function name, but the five subjects plus the tier-3 fence case are concrete enough to implement against.

USEFUL [standing context, "Bash is the wrong tool for reading this log"]: exact. Three `sed`/`awk` reads of the standing context were persisted to files instead of printed and cost three wasted calls before the `sed -n '20,100p'` split under the ~2KB-per-block threshold worked. Read the log in two ranges (`20,100` and `100,172`) or use the Read tool with an offset.

WATCHOUT: the round-2 snapshot at `scratchpad/cp-snap/.../non-spec-r2` is byte-identical to the live directory, so `diff -ru` prints nothing. The real delta for this round is `git diff 470cd4e34 HEAD -- <proposal dir>`, which shows the checklist S2 rewording, TEST-1's new kept-path and 28.5.3-deletion paragraphs, TEST-2's new green-on-the-shipped-proto paragraph, the deletion of `spec-changes.md` §7, and the §9 spec-map bullet rewrite. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r2

WATCHOUT: `§8 Testing` lives in `non-spec-changes.md:99-107`, not in the spec-changes file, and `spec-changes.md` jumps from §6 to §9. A test-coverage pass that greps `spec-changes.md` for a Testing section finds none and can mistake that for a missing Testing section. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:99

OPEN: nothing new. The one thing a run would settle that reading cannot is already recorded: nobody has executed tier 0 or tier 3, so §8's adequacy is judged against what the enumerated cases would assert rather than against a run. The implementor should run `lenny-test --tier 0` after S2 and `--tier 3` after S3 before ticking either box.
