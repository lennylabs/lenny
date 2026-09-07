# Review log: Derive message scope from the address type

## Standing context

**Changelog (compaction pass 1, 2026-09-07).** First compaction on this file: the standing context was
empty before this pass, so nothing was retired and nothing deleted, and `## Retired` stays empty. Lifted the
durable residue of every entry in the ledger (roughly 140 entries across the `spec.1-4`, `spec-recheck.1-2`,
`f1-f3`, `non-spec-recheck.1-3`, and `non-spec.1-3` rounds), deduplicating the proto census, the anchor
inventories, and the blast-radius sweep, each of which was independently re-derived more than a dozen times
with the same answer. Honoured every `CORRECTS` against the claim it names. Did not reach 200 lines: the
Traps section carries thirty distinct dead ends and re-derivation warnings, each of which cost a round at
least once, and cutting any of them re-opens that round. The target should move up.

### Settled

- **Proto census.** 31 RPC request types across the two service blocks; 25 declare a top-level `SessionId session_id`; the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`.
- **Addressing convention.** No field named `session_id` carries another type and no field of type `SessionId` carries another name, anywhere in `schemas/lenny-adapter.proto` including `oneof` arms. 26 such declarations (the 25 request types plus `CheckpointStart`). D2's replacement gate is green on the shipped proto on day one.
- **The only two `oneof` blocks** are `CheckpointRequest` (`schemas/lenny-adapter.proto:1174`) and `CheckpointResponse` (`:1250`), and `CheckpointResponse` is the request type of no RPC, so the envelope predicate selects `CheckpointRequest` alone.
- **Envelope frames.** `CheckpointStart` (`:1193`) declares `SessionId session_id = 7` (`:1217`); `CheckpointGrant` and `CheckpointAbort` declare no address; `CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4` (`:1186`).
- **Proto anchors, current and verified.** `message SessionId` `:596`, `CheckpointRequest` `:1173`, `CheckpointStart` `:1193`, `CoordinatorFenceRequest` `:1455` with `SessionId session_id = 1` at `:1456` and no `reserved`, `ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar), `ReportPodScrubRequest` `:499-503`.
- **spec/04 §4.1 anchors.** Heading `:149`, introducing paragraph `:151`, table `:153-186` (32 rows), fence row `:175`, grounding paragraph `:188`, `ShutdownRequest` paragraph `:190`, `### 4.2` at `:192`.
- **Table arithmetic.** The 32 rows are the 31 request types plus `CheckpointStart`; 26 session and 6 pod, becoming 27 and 5 once the fence moves, which is exactly what the derivation computes. No row is lost.
- **The derived session class is a strict superset** of the retired table's on the current proto. Only `CoordinatorFenceRequest` moves, pod to session; nothing moves session to pod, so the `spec/05:515` refusal can only be added, never removed.
- **The one behavioral obligation is met and pinned.** `spec/05_runtime-registry-and-pool-model.md:515` refuses a session-scoped request with an empty identifier; `pkg/adapter/coordination.go:109-111` does it before resolving anything; `TestCoordinatorFenceRejectsMissingSessionID` at `pkg/adapter/coordination_test.go:35` asserts it.
- **Post-0076 code anchors.** `pkg/adapter/server.go:302` is `hold holdState` and `Server` carries no `coord` field; `pkg/adapter/slot.go:59` is `coord coordinationState`; `pkg/adapter/coordination.go:17-38` is the per-session struct doc, `:107` the handler, `:116` the `boundSlotState` resolve, `:127-134` the `coordinator_handoff_stale` refusal; `pkg/adapter/checkpoint.go:74-84`.
- **coordfence driver.** `DefaultMaxAttempts = 3` at `pkg/gateway/coordination/coordfence/coordfence.go:52`, transient arm retries at the same generation `:180-183`, stale arm re-reads and relinquishes `:171-179`. A fence that lands but whose ack is lost gives up on the second of three attempts.
- **§4.7.1 survivors.** `spec/04_system-components.md:725` (`ReportSessionScrub` session-scoped) and `:726` (`ReportPodScrub` pod-scoped) are the only per-message scope sentences outside §4.1; both agree with the derivation and both stand unedited.
- **`:726` is the last pod-scope classification.** After SPEC-1, the other `pod-scoped` sites in `spec/` are `spec/10:60` (the hold and its gauge), `spec/04:872` (the rotation in-flight ceiling), and `spec/12:202` (Redis key prefixes), none of which classifies a request message.
- **A tier-11 gate pins `:725`.** `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` (`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`) holds the identical sentence on `spec/04:725` and `docs/reference/adapter-contract.md:81`. Editing `:725` turns tier 11 red.
- **No docs mirror.** `docs/` names RPCs and never request message types. `CoordinatorFence` appears once, at `docs/reference/adapter-contract.md:69`, with no scope word; `grep -rn CoordinatorFence sdks/ charts/` returns nothing.
- **No inbound reference to the table.** Nothing in the tree links `#request-message-scope` or the heading text, and SPEC-1 keeps the heading, so no anchor redirect is owed.
- **spec/28 and spec/10 already read per-session.** `spec/28:314-317` (CH-FENCE), `spec/28:1672-1673` (the unit is the session), `spec/10:38` and `:40`. The §28.3 register row at `spec/28:120` has no scope column. So `spec/04:175` and `:188` contradict two other spec sections as well as the code.
- **spec/10 hold anchors.** `:53` opens §10.1.4, `:57` states the fence is the only way out of hold state (predates 0076, `db8cdc224`), `:60` states the hold and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped (landed by 0076's SPEC-1 under D5, `cf20a0646`). The attribution clause attaches to `:60` alone.
- **Blast radius of the shared parse.** `protoServiceRequests` has one caller (`tests/tier0_static/adapter_proto_message_scope_test.go:87`); `protoFields` has one other (`tests/tier0_static/claim_register_proto_agreement_test.go:64`, with lookups at `:72` and `:82`). The gate's private helpers have no reader outside their file. The two tier-0 files 0076 landed read doc comments and call neither.
- **spec-map registrations.** The retiring gate's two case names appear only at `tests/spec-map.json:156` and `:169` (section 4.1) and `:5670` (section 28.5.3). The tier-3 file is credited whole-file at `:172`, `:600`, `:1110`, `:1391`, `:3678`, so TEST-2 owes no spec-map edit.
- **`validate-maps` is a tier-0 check.** `validateSpecMapTestFuncs` (`cmd/lenny-test/cmd_validate.go:941-998`) fails on a `path::Fn` entry naming no function, and `validate-maps` runs from `cmd/lenny-test/cmd_run.go:761`. That is why TEST-1's re-registration lands inside S2 rather than in its own step.
- **`slotAddressCaseFiles` names files by path**, at `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, `:337`, `:342`, `:409`, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970-989`) is one-directional, refusing missing credits and never surplus ones. No edit is owed while the replacement gate keeps its path.
- **Registers and meta-gates are untouched.** `identifier-senses.yaml`'s eight `spec/04` rows key on retired channel spellings, none of which occurs in that file; `line-citations.yaml` and `line-citation-resolution.yaml` are `files: []`; `pinned-spec-literals.yaml` and `anchor-senses.yaml` name no `spec/04` site; `tests/claim-map.json` carries no scope row; `gate_integrity_test.go`'s `tierZeroGates` omits the gate; `successor_pointer_test.go`'s `reducedSections` is §4.7, §15.4, §29.10.
- **The line-citation ratchet cannot fire.** It matches the retired `§X line L` form only, `proposals/` is outside its read domain, and the `04_system-components.md:NNN` citations that shift when the block shrinks live in `BUILD-GAPS.md` and `TEST-GAPS.md`, both in `readExcludedFiles`.
- **S1's red window is real and bounded.** Deleting `:153-186` leaves `parseMessageScopeTable` with zero rows, because no other line in `spec/04` matches `messageScopeRow`, so the retiring gate reports a missing row for every in-scope message until S2 lands. S1's own line records the disposition.
- **Tier 0 compiles the tier-3 contract package.** `runStaticTier` runs `go vet -tags=contract ./tests/tier3_contract/...` (`cmd/lenny-test/cmd_run.go:503-509`). S3 listing tier 3 alone follows the repo's convention (0076's S8 does the same).
- **TEST-2 split arithmetic.** `sessionScopedMessages`'s 18 current keys are exactly the 18 messages declaring `reserved "slot_id"`; 17 of them are request types and one is `CheckpointStart`; 17 plus the 8 additions is the 25 addressed request types, giving a widened set of 26. `retiredDuplicateNumbers` keeps the 18 verbatim.
- **The 8 additions** are `CoordinatorFenceRequest`, `ExportPathsRequest`, `ConfigureWorkspaceRequest`, `CallConnectorToolRequest`, `CallPlatformToolRequest`, `ListConnectorToolsRequest`, `ListPlatformToolsRequest`, `ListSessionConnectorsRequest`. All declare the address, none declares `slot_id`, so both widened arms pass on the tree as it stands.
- **Commit `040323634`** ("Address a session on the gRPC leg by its session identifier alone", 2026-08-20) is 0073's own implementation commit. It added all 18 `reserved "slot_id"` pairs and names `CoordinatorFence` nowhere, which is the correct ground for the fence's empty retired-field column.
- **`SlotId slot_id` history.** The field entered the proto across eight commits (`4f6e49dea`, `3128fa712`, `72880f767`, `c47b65522`, `4003ee848`, `3c69e3f35`, `01d19af01`, `040323634`). The fence was introduced by `d353a8ef3`, which added none of them.
- **0073's recorded limit lives only in 0073**, at `proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6686-6689` and `:6713`. Nothing in `spec/`, `docs/`, or `BUILD-GAPS.md` restates it, and §4.1 states no gate and no gate limit. 0073 also records a second limit at `:912-913` that applies only under its rejected choice (b).
- **"0073's §4.2 value rule" is 0073's own §4.2**, the heading "### 4.2 The rule" at `proposals/0073_...md:964`, not `spec/04` §4.2 (Session Manager). The rule landed at `spec/05_runtime-registry-and-pool-model.md:515`.
- **Open decisions moved.** The retire-or-withdraw question left `spec-changes.md` §7 and is now summary OD1 with a recommendation; §7's single surviving question is the tier-3 population question, which is summary OD2. Anything citing "§7's first question" for the gate-hole residual is stale.
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
- **Snapshot diffs are routinely empty and that does not mean nothing changed.** The round-N snapshot is taken at round start and has repeatedly been byte-identical to the live directory, so `diff -ru` against it prints nothing and looks like a broken snapshot. Diff against the earlier snapshot in the lane (`spec-r2`, `non-spec-recheck-r1-start`, `non-spec-recheck-r1-prefix`, `non-spec-r1-start`) or against git to see the real delta.
- **Cache keys omit `summary.md` and `review-log.md`.** The key is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no lane component, so a spec-lane and a non-spec-lane run of the same lens share a slot, and a summary-only edit is served a stale hit. Several "empty findings" entries in the ledger are a prior interrupted run's answer replayed, not a review; the operational lens in particular recorded two clean passes that were one cache hit. A lens that owns proposal bookkeeping must not treat a hit as covering a summary rewrite.
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

### Open

- Whether the replacement gate keeps the path `tests/tier0_static/adapter_proto_message_scope_test.go`. Never stated in D2, assumed by every enumeration, and load-bearing for `slotAddressCaseFiles:336`. [spec.1.fix-design-G2.1, non-spec-recheck.3.review-edit-sites.1, non-spec.1.review-citations.1, non-spec.1.review-feasibility.1]
- Whether the replacement gate can honestly carry a `// spec: 28.5.3` annotation, and whether `tests/spec-map.json:5670` should follow the gate or be dropped. [spec-recheck.1.review-feasibility.1, non-spec-recheck.3.review-docs-alignment.1, non-spec.2.review-feasibility.1, non-spec.3.review-edit-sites.1]
- Whether the replacement gate's own case names must differ from the retiring ones for `validate-maps` to stay green across S2's single commit. Nobody has run the sequence. [non-spec.1.review-applicability.1]
- Whether the implementor can build the envelope arm from the shared text parse without resolving a `oneof` arm's type to another top-level message and reading its fields. [non-spec-recheck.1.review-mechanism.1, non-spec.1.review-mechanism.1]
- Whether adding `reserved 3; reserved "slot_id";` to the fence would be caught by any gate as a false historical reservation. No such gate was found; the reason not to do it is the field history. [non-spec.3.review-citations.1]
- Nothing keeps `sessionScopedMessages` complete once its rule becomes proto-derivable; a new session-addressed request message falls out silently, which is what let eight messages drift out already. [non-spec.1.fix-G1.1, non-spec.3.review-mechanism.1]
- `spec/10:39` orders a same-generation fence retry the adapter refuses, and the coordfence loop has no 1-second backoff at all. Both pre-existing; the backoff half may be an uninventoried residue. [spec.4.review-reliability.1, non-spec.1.review-reliability.1, non-spec.3.review-performance.1]
- The second half of 0073's value rule, "a pod-scoped request never resolves a session root regardless of what `session_id` carries", never landed in any spec file. [spec-recheck.1.review-security.1]
- No spec sentence states which session's fence releases a pod-wide hold on a multi-slot pod. 0076 or 0080 territory. [spec-recheck.1.review-security.1, non-spec-recheck.1.review-security.1]
- `tests/claim-map.json`'s `CoordinatorFence` row anchors `pkg/adapter/coordination.go:85` while the handler is at `:107`. Generator-produced, a 0076 residue. [non-spec-recheck.1.review-fresh.1, non-spec.3.review-citations.1]
- OD2 carries its question and its ground but no recommendation, no losing alternatives, no cost of deciding otherwise, and no confidence, which is less than the section's contract asks. [f1.cleanup, f2.cleanup, f3.cleanup]
- `status.md:22` and the summary cite a "§11" that exists only inside this review log, so the pointer resolves across files. [spec.4.review-reliability.1, reconcile.1, f1.cleanup, non-spec.1.review-edit-sites.1]
- 0080 §2 at `:453-454` records the §4.1 `ShutdownRequest` classification limit as taken by 0075, which does not take it. [spec.2.review-citations.1, f1.other-proposals, reconcile.1]
- The staged block uses positional self-references ("the paragraph below", "the first paragraph") that hold only while the three paragraphs stay adjacent and in order. [spec.2.review-fresh.1]
- SPEC-1's appositive "the whole block 0073's SPEC-7 stages" under-describes SPEC-7, which also staged the `ShutdownRequest` paragraph and the §4.7 rows. The explicit line enumeration makes the edit unambiguous. [spec.3.review-applicability.1, spec-recheck.2.review-applicability.1]
- `spec/` gains its first mention of a test tier in the staged constraint paragraph. `spec/28:64` and `spec/18:85`, `:98` are the precedents; judged below the bar but never decided. [spec-recheck.1.review-mechanism.1, spec-recheck.2.review-edit-sites.1]
- UNVERIFIED: whether S3's tier list omitting tier 0 is a finding. `non-spec.2.review-applicability.1` filed it; `non-spec.1` and `non-spec.3` declined it as repo convention, and `non-spec.3` corrects the stated rationale (tier 0 does compile contract-tagged files). Someone should settle whether the filed finding stands.
- UNVERIFIED: `spec-changes.md:127-129` says 0073's recorded gate limit "goes with the gate TEST-1 replaces" while `summary.md:202` lists it under what stands. Reconcilable, since the header comment is deleted and the limitation persists against the replacement gate, but the pronoun is ambiguous. [non-spec-recheck.3.review-fresh.1, non-spec.3.review-security.1]
- UNVERIFIED: nobody has run tier 0 or tier 3. Every claim in this log is derived from reading files. [non-spec.3.review-feasibility.1]

### Deferred

- **DEFERRED [docs/reference/adapter-contract.md]:** line 69 describes `CoordinatorFence` as "precondition for any subsequent operational RPC", which reads as a pod-wide gate. After 0076 the fence and the generation are per bound session, so the true statement is that the fence is the precondition for subsequent operational RPCs naming the session it fenced. The same imprecision sits in its source at `spec/04_system-components.md:713`. Neither is staged here; both belong to whoever takes the 0076 residue, which proposal 0080's inventory registers.
- **DEFERRED [spec/04_system-components.md]:** the `CoordinatorFence` row at `:712` still reads "Announce new `coordination_generation` to the pod on coordinator handoff", which 0076 falsified by moving the recorded generation onto the session's slot entry (`pkg/adapter/slot.go:59`) and which `spec/28_communication-channels.md:314-317` already contradicts. What is true instead: the fence announces the generation for the session it names. Outside 0075's staged scope, and a 0076 residue for 0080's inventory.
- **DEFERRED [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]:** §2's bullet at `:453-454` splits. The declared message-scope table and its reconciliation gate stay on the owned side; the §4.1 `ShutdownRequest` classification limit returns to §1's inventory as a gap no proposal takes, because SPEC-1 leaves `spec/04:190` unedited and the replacement gate reads the protocol definition alone. 0080 is another proposal's file and a Draft, so the correction lands there.
- **DEFERRED [0075_....status.md]:** the status file's `**Date:**` line reads "§11 records what the rewrites changed". The only `## 11` in this proposal is inside this review log, so the reference resolves across files rather than within the document a reader is holding. The summary's copy of that pointer has already gone; the status file is now the only carrier. Either name the review log or drop the reference.
- **DEFERRED [docs/api/internal.md]:** `:209-215` publishes a protobuf excerpt reading `message CheckpointRequest { string session_id = 1; string checkpoint_id = 2; string consistency = 3; }`. Every part of it is false about the shipped proto, which is a `oneof msg` of `CheckpointStart`/`CheckpointGrant`/`CheckpointAbort` plus `int64 coordination_generation = 4` and declares no `session_id`. The page also documents an `UploadFiles` RPC that no service declares. SPEC-1 does not make it wrong, but it changes the severity: a top-level `string session_id` on a request message becomes a spelling the tier-0 gate refuses, so the excerpt would then illustrate a construction the specification forbids. The docs loop, or 0080's residue inventory, owns it.
- **DEFERRED [0075_....non-spec-changes.md]:** TEST-1 says the replacement gate's cases are re-registered "under the same sections", which includes §28.5.3 (`tests/spec-map.json:5670`). The retiring gate read `spec/04` §4.1 and cited 28.5.3 for addressing; the replacement gate reads `schemas/lenny-adapter.proto` alone and checks a gateway-to-adapter gRPC convention, while §28.5.3 is the intra-pod JSONL and MCP boundary (`spec/28_communication-channels.md:499`). Whether the replacement cases can honestly carry a `// spec: 28.5.3` annotation is unsettled, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` credits from the annotation, so the annotation decides the register entry rather than the reverse.
- **DEFERRED [0075_....spec-changes.md]:** D2 calls the addressing convention "an addressing convention that nothing checks today", which overstates it. `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each member of `sessionScopedMessages`, that `session_id` exists and is of type `lenny.adapter.v1.SessionId` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`). What is true instead: no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset.

## Ledger

### [non-spec.4.review-citations.1]

DECISION: returned an empty findings list — BECAUSE I extracted every file:line citation in the five landing files (problem-statement, spec-changes, non-spec-changes, summary, implementation-checklist) via a mechanical grep for backticked `path:N` and bare `:N` forms, and verified each one against the tree; all resolved and all said what the proposal claims — ALTERNATIVES: filing the "SPEC-1 departs from 0076's OD3 Question A recommendation, which asked the rewritten declaring sentence to name the pod-scoped hold exit" point; rejected because the proposal's own OD3 is recorded as answered ("the rewritten §4.1 restates nothing about the coordinator hold; §10.1.4 owns both halves"), SPEC-1 grounds it on `spec/10:57` and `:60` which both verify, and D3 never claims to implement the recommendation's wording clause — a settled decision, not a false citation.

CORRECTS [standing context "Proto anchors, current and verified"]: the bullet says `ReportSessionScrubRequest` `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is backwards. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact. Do not "fix" it.
EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the full citation inventory of the landing files is 68 distinct `path:N` / `:N` forms and every one is now individually confirmed against the tree at commit c5d35bb05 (spec/04 :149/:151/:153-186/:175/:188/:190/:725/:726; spec/05:515; spec/10 :38/:39/:40/:57/:60; spec/28:314-317; proto :458/:499-503/:596/:1173/:1186/:1193/:1217/:1250/:1455-1461; pkg/adapter server.go:302, slot.go:59, coordination.go:17-38/:107/:109-111/:116/:127-134, checkpoint.go:74-84; coordfence.go:171-179/:180-183; the four tier-0 files; the tier-3 file; tier11 docs gate :43-76; spec-map :156/:169/:172/:5670; docs/reference/adapter-contract.md:81). A future citation lens can spot-check rather than re-derive.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/

FACT: re-derived mechanically and confirming the Settled block: 31 RPC request types; 25 declare a top-level `SessionId session_id`; the 6 that do not are AdapterEventsRequest, CheckpointRequest, DemoteSDKRequest, GetObservedIntegrationLevelRequest, NegotiateVersionRequest, ReportPodScrubRequest; the only two `oneof` messages are CheckpointRequest and CheckpointResponse; zero name/type biconditional violations anywhere in the file including oneof arms; exactly 18 messages declare `reserved "slot_id"` and that set is byte-identical to `sessionScopedMessages`'s 18 keys; each of the 8 staged additions declares `SessionId session_id = 1` and none reserves `slot_id`; the §4.1 table is 32 rows, 26 session and 6 pod.
EVIDENCE: schemas/lenny-adapter.proto; spec/04_system-components.md:153-186; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the two shared-parse functions have exactly one caller each and they are different files — `protoFields` only at tests/tier0_static/claim_register_proto_agreement_test.go:64, `protoServiceRequests` only at tests/tier0_static/adapter_proto_message_scope_test.go:87. TEST-1's phrase "its other caller" is file-level (the two gates share the parse file), which reads as if the scope gate called `protoFields`; it does not. Accurate as written, but the next reader will trip on it.
EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36,68

FACT: 0073 §8 does state, verbatim, "a message added later fails the gate until it is classified", which is the property the summary's 0073 impacts row says is not reproduced. Verified rather than assumed.
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:4680-4682

FACT: 0073's SPEC-7 did stage the `ShutdownRequest` paragraph beside the table, plus a §4.7 RPC-table row and a §5.2 restatement, so the summary's "what stands" clauses about SPEC-7 are all true.
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:2823-2826, :962

WATCHOUT: the cache key omits `summary.md`, and this round's only diff against the snapshot was the review log's compaction — the three keyed files were byte-identical to round 3. A lens whose subject is the summary or the review log gets no signal from `diff -ru` against the snapshot and must diff against git or an earlier snapshot in the lane.
EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4/



### [non-spec.5.review-applicability.1]

DECISION: returned an empty findings list for the applicability-and-sequencing lens — BECAUSE I simulated S1→S2→S3 against the tree at HEAD and every anchor, every created artifact's needed property, and every gate disposition resolved; the three staged deliverables map one-to-one onto three single-lane steps in dependency order with no forward reference and no checked box — ALTERNATIVES: filing the replacement gate's unstated test-function names as an underspecified target (rejected: the names are invented and written into `tests/spec-map.json` by the same implementor inside the same step S2, which is ordinary implementation judgment, and the lens bars reporting that); filing the loss of the retired table's Service and Direction columns as relocation-that-loses-content (rejected: §4.7.1's two RPC tables carry direction, nothing in the tree reads the columns, and no applied text becomes wrong).

CORRECTS [Standing context, "Proto anchors, current and verified"]: the entry says `ReportSessionScrubRequest` is at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". SPEC-1 is RIGHT and the log entry is wrong. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`, which is exactly what `spec-changes.md:134-135` cites. Do not "correct" SPEC-1's `:458` to `:457`. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: TEST-2's split arithmetic re-derived independently with a comment-stripping, brace-counting, oneof-aware parser and confirmed exactly. 31 RPC request types; 25 declare a top-level `SessionId session_id`; 26 messages in the file do (the 25 plus `CheckpointStart`); the 6 unaddressed request types are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; the messages declaring `reserved "slot_id"` are exactly the 18 current `sessionScopedMessages` keys; the current 18 plus the 8 additions TEST-2 names is exactly 26, with no addressed request type left out and no named member that fails to declare the address. Both widened tier-3 arms pass on the tree as it stands. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63; proposals/0075_.../0075_....non-spec-changes.md:55-61

FACT: no sibling file in `tests/tier3_contract/adapter_session_address/` reads `sessionScopedMessages`, `retiredFieldName`, `reservesNumber`, `reservesName`, `messageDescriptors`, or `retiredWrapperName`. `send_message_stamp_test.go` shares the package but touches none of them, so retyping `sessionScopedMessages` to `[]string` has no blast radius outside `session_address_wire_test.go`. This is the one package-level hazard a Go split of this kind usually carries, and it is absent. — EVIDENCE: grep over tests/tier3_contract/adapter_session_address/ returns only session_address_wire_test.go

FACT: the only inbound references to the retiring gate anywhere outside `proposals/` are `tests/spec-map.json:156`, `:169`, `:5670` (by `path::Func`) and `tests/tier0_static/spec_map_slot_address_registration_test.go:336` (by path alone). Nothing else in `tests/`, `cmd/`, `BUILD-GAPS.md`, or `TEST-GAPS.md` names either function or the file. §9's list is complete for TEST-1. — EVIDENCE: tests/spec-map.json:156,169,5670; tests/tier0_static/spec_map_slot_address_registration_test.go:336

FACT: `tests/tier11_docs/successor_pointer_test.go:52-55` scopes its `reducedSections` domain to `spec/04` §4.7, `spec/15` §15.4, and `spec/29` §29.10, all reduced to §28.5, and `inDomain` (`:58-66`) is the whole domain. §4.1 is outside it, so retiring the §4.1 table owes no successor pointer and fires no tier-11 gate. The retired block also carries no markdown link, no `§` reference, and no channel identifier (`sed -n '153,190p' spec/04_system-components.md | grep "](\|§\|#"` is empty), so no anchor-redirect, citation-resolution, or naming-lint obligation follows either. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:49-66; spec/04_system-components.md:153-190

WATCHOUT: the proposal text is BYTE-IDENTICAL to the `non-spec-r4` snapshot. `diff -q` on each of the seven non-log files returns SAME; only `review-log.md` changed, and only by compaction pass 1 adding the standing context. A round-5 reviewer looking for "what the fixers changed since r4" will find nothing, and that is the true state rather than a broken snapshot. — EVIDENCE: scratchpad/cp-snap/0075_.../non-spec-r4/ vs proposals/0075_.../

UNVERIFIED: still nobody has run tier 0 or tier 3. Every applicability claim here, mine included, is derived from reading files and re-parsing the proto. The one sequencing claim that a run would settle is S2's single-commit `validate-maps` green window (already in `## Open`).


### [non-spec.5.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in all six landing files resolves and says what the proposal says it says; I re-derived each one rather than trusting the standing context, and found no drift that changes a meaning — ALTERNATIVES: filing the dangling `§11` pointer in `status.md:22-23` (no `## 11` exists in any of the proposal's landing files; the only `## 11` is inside the review log). I declined it because its remedy changes no applied spec text, no staged code, and no test, which is the rubric's exclusion, and because it is already carried as a DEFERRED in the standing context. A future round that wants it closed should close it as bookkeeping, not as a finding.

FACT: the proposal's landing files are byte-identical to the `non-spec-r4` snapshot; only `review-log.md` changed (the compaction pass). `diff -q` per file is the cheap way to see this — `diff -ru` on the directory buries it under 130 lines of new standing context. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4

FACT: the whole citation set now verified in one pass, so a later round can spot-check instead of re-deriving. spec/04: `:149` heading, `:151` introducing paragraph (5 sentences on one line), `:153-186` table (header `:153-154`, 32 rows `:155-186`), `:175` fence row, `:188` grounding paragraph, `:190` ShutdownRequest, `:692` §4.7.1 heading (next heading `:728`, so `:725`/`:726` are inside it), `:712-713`. spec/05:515. spec/10 `:38 :39 :40 :53 :57 :60`. spec/28:314-317. proto `:458 :499-503 :596 :1173 :1174 :1217 :1250 :1455 :1456`. tier-3 `:37-43 :39-43 :40-43 :81 :102 :130 :130-141 :150-158`. tier-0 gate `:17-27 :25-27 :54 :75-81` plus its two test funcs at `:136` and `:156`; parse `:10-15 :18 :64-67`; `claim_register_proto_agreement_test.go:64`; `address_rule_citation_test.go:45-47`. spec-map `:156 :169 :172 :5670`. tier-11 `:43-76` and `docs/reference/adapter-contract.md:81`. pkg: `server.go:302`, `slot.go:59`, `coordination.go:17-38 :107 :109-111 :116 :127-134`, `checkpoint.go:74-84`, `coordfence.go:52 :171-179 :180-183`. — EVIDENCE: spec/04_system-components.md:149-190

FACT: `git log -L 60,60:spec/10_gateway-internals.md` confirms the "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped" sentence landed in `cf20a0646` "Apply proposal 0076 SPEC-1 to spec/10", so `spec-changes.md:120-122`'s attribution of `:60` to 0076's SPEC-1 under D5 is exact. `git log -L` is the fast way to settle an attribution clause on a one-line spec paragraph. — EVIDENCE: spec/10_gateway-internals.md:60

FACT: 0080's own text backs both impacts-row claims. `:453-454` is the D6-table bullet, `:458-460` the false-tier-3-clause bullet (the two §2 entries the summary counts), `:216` is `### 1.16 The pod refuses the fence retry §10.1.2 orders`, and `:268` is its `**Source:** proposal 0076's OD2` line. — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:453,458,216,268

USEFUL [Traps: naive proto parsers derail on this file]: the brace-delta-per-line parser it prescribes reproduced the 31/25/6 census, the 18 `reserved "slot_id"` messages, and the two `oneof` blocks on the first try. Writing the parser the naive way would have cost the round.

USEFUL [Traps: long lines hide the sentences citations point at]: `sed -n '725p' spec/04 | tr '|' '\n' | tail -4` and `sed -n '60p' spec/10 | tr '.' '\n' | tail -8` both recovered clauses a truncated grep hid. Without this I would have read `spec/04:726` as not carrying "The request is pod-scoped."

USEFUL [Traps: orchestrator briefs carry stale content]: the r5 brief still gives the pre-refresh proto anchors (`:589 :1166 :1447 :1448`, each off by 7) and still says §1.2 claims the pod-wide ground holds. The proposal's own anchors (`:596 :1173 :1455 :1456`) are the correct ones and §1.2 already reads post-0076. Anyone who reviews the brief against the tree instead of the proposal will manufacture four false findings.


### [non-spec.5.review-client-surface.1]

DECISION: Returned an empty findings list — BECAUSE the client-facing sweep came back clean on every parallel representation this change could reach, and the substantive proposal text is byte-identical to the round-4 snapshot (`diff -ru` over the proposal directory excluding the review log returns nothing; only the log's compaction pass 1 changed) — ALTERNATIVES: filing `docs/api/internal.md`'s five `string session_id = N;` protobuf excerpts, rejected because they already contradict `schemas/lenny-adapter.proto` today and SPEC-1 does not make them wrong (an earlier round already DEFERRED that site to the docs loop / 0080); filing the §28.5.3 spec-map credit for the replacement gate, rejected because it is a live `OPEN` in the standing context and is register bookkeeping rather than a client contract.

CORRECTS [Standing context → Settled → "Proto anchors, current and verified."]: that bullet says "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". SPEC-1's citation is exact and the log bullet is the wrong one. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`. Nobody should "fix" spec-changes.md:134-135 down to `:457`.

FACT: Client-surface sweep, run fresh today, all negative. No file under `sdks/` or `charts/` names `CoordinatorFence` or any adapter request message type. `docs/` carries no mirror of the §4.1 classification table; the only per-message scope word in a client-facing doc is `docs/reference/adapter-contract.md:81` (`ReportSessionScrub` ... "The request is session-scoped"), which mirrors `spec/04_system-components.md:725`, is pinned by the tier-11 gate, and SPEC-1 leaves both unedited. `docs/reference/adapter-contract.md:69` names `CoordinatorFence` with no scope word. No REST/OpenAPI, MCP tool schema, CRD, or `schemas/*.json` surface carries a request-message scope class. — EVIDENCE: docs/reference/adapter-contract.md:69, :81; spec/04_system-components.md:725, :726; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: Both TEST-2 population claims are exactly true on the tree, verified by two independent awk passes over `schemas/lenny-adapter.proto`. The messages declaring a top-level `SessionId session_id` number 26 (25 RPC request types plus `CheckpointStart`); the messages declaring `reserved "slot_id"` number 18 and are byte-identical to `sessionScopedMessages`'s current 18 keys; the set difference is exactly the 8 names non-spec-changes.md:56-58 lists. Both widened arms (`:78-92` no second address, `:99-116` the address is `lenny.adapter.v1.SessionId`) pass on all 26. The address is spelled conventionally in both directions on all 26 declarations, with zero exceptions anywhere in the file, so D2's replacement gate is green on day one. Spot-check rather than re-derive. — EVIDENCE: schemas/lenny-adapter.proto (26 `SessionId session_id` declarations, 18 `reserved "slot_id"`); tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: `messageDescriptors` resolves names against `ParentFile().Messages()`, which is the file's TOP-LEVEL message set. All 8 of TEST-2's additions, including the five `GatewayControl` tool requests, are top-level messages of the same file, so `ByName` resolves for every one and no widened member silently hits the `md == nil` continue. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:67-70

WATCHOUT: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:14` and `:144-145` both describe `schemas/lenny-adapter.proto` as "a schema runtime authors and the generated compliance suite compile against", which sits in tension with problem-statement §1.4's "runtime authors speak the JSONL leg". It is not a defect here, because §1.4's load-bearing claim is that nothing on the wire changes and nothing does; but a future proposal that DOES touch the proto cannot lean on §1.4's sentence to skip the SDK and compliance-suite blast radius. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:14, :144-145; 0075_....problem-statement.md:94

USEFUL [Standing context → Traps → "Naive proto parsers derail on this file"]: saved a wasted census. The `awk '/^message [A-Za-z]/ {m=$2}'` form (track the last top-level `message` header, never try to close a body) sidesteps the same-line-close trap entirely and reproduced both settled counts on the first run.


### [non-spec.5.review-docs-alignment.1]

DECISION: Returned an EMPTY findings list, the sixth consecutive empty return for this lens — BECAUSE the round-5 delta against `non-spec-r4` is the review log alone (`diff -rq` reports only `review-log.md` differing; the compaction pass 1 changelog), so no staged text moved, and an independent re-sweep of `docs/` reconfirmed that no page mirrors the §4.1 classification, no metric/alert/flag/endpoint/error-code/default changes, and every docs-facing citation SPEC-1 makes resolves — ALTERNATIVES: re-filing `docs/api/internal.md:209-215` under the "newly contradicts the post-change spec" reading (see the MISTAKE entry below for why I stopped).

MISTAKE (mine, avoided): I nearly re-filed `docs/api/internal.md:209-215` on a reading `non-spec.3.review-docs-alignment.1` did not consider — that the prior rejection ("already contradicts the shipped proto") answers a docs-vs-proto contradiction, while SPEC-1 creates a *new* docs-vs-spec one, because after SPEC-1 `spec/04` §4.1 forbids a request message declaring a field named `session_id` that is not of type `SessionId`, and that page publishes exactly `message CheckpointRequest { string session_id = 1; ... }` as the adapter protocol. I declined on the (d) bar: the page is wrong today, so it does not *become* wrong, and the remedy is the whole stale excerpt (the page also documents an `UploadFiles` RPC no service declares), which belongs to whoever owns that page rather than to this proposal. The review log already carries it as DEFERRED for 0080's inventory. A future round reaching the same edge should stop here too; the refined argument does not clear the bar either.

FACT: the round-5 proposal directory is byte-identical to the `non-spec-r4` snapshot except for `review-log.md`. `diff -rq scratchpad/cp-snap/0075_.../non-spec-r4 proposals/0075_...` prints exactly one differing file. A lens whose last pass was on r4's text has no new staged text to read. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4/

FACT: `docs/reference/adapter-contract.md:82` is the `ReportPodScrub` row and carries NO scope word, while `:81` is the `ReportSessionScrub` row and carries the pinned sentence "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." This is the asymmetry that makes SPEC-1's "the two §4.7.1 sentences stand unedited" cost-free on the docs side: only one of the two has a docs carrier, and that carrier is the one the tier-11 gate holds. — EVIDENCE: docs/reference/adapter-contract.md:81, :82; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: no operator-facing narrative page owns the `CoordinatorFence` RPC or its scope, so the reclassification adds no cause to any failure narrative. `docs/runbooks/coordinator-handoff-slow.md:28` is about the parent-to-child delegation handoff, a different mechanism that never names the RPC; `docs/operator-guide/upgrades.md:49` and `docs/operator-guide/observability.md:287` name the handoff without a scope. A grep of `docs/operator-guide/` and `docs/runbooks/` for coordinator/handoff/fence returns only those three plus `docs/runbooks/redis-sentinel-failover.md` and `docs/runbooks/index.md`. — EVIDENCE: docs/runbooks/coordinator-handoff-slow.md:28

USEFUL [spec.1.review-docs-alignment.1]: its FACT that `docs/` names RPCs and never request message types held for the sixth time and is still the single reason this lens returns empty. One grep (`grep -rn "session-scoped\|pod-scoped" docs/`) plus one (`grep -rn "CoordinatorFence\|CheckpointRequest\|CheckpointStart" docs/`) reproduces it in seconds; do not sweep page by page.

USEFUL [non-spec.3.review-docs-alignment.1]: its ALTERNATIVES clause naming the `docs/api/internal.md` candidate and the exact ground for rejecting it is what let me price my own version of the same finding in minutes instead of an hour. This is the entry to keep if the Traps section is ever trimmed.


### [non-spec.5.review-edit-sites.1]

DECISION: Returned an empty findings list — BECAUSE every edit-site surface I could construct from the identifiers this proposal adds, changes, or removes was either already staged, already recorded as DEFERRED, or verifiably unaffected; I re-derived rather than trusted the standing context on each one — ALTERNATIVES: filing the `spec-changes.md:127-129` vs `summary.md:203` pronoun ambiguity about 0073's recorded gate limit (rejected: the standing context already carries it as UNVERIFIED and "reconcilable", the applied spec text is the same under either reading, and it is wording); filing `docs/api/internal.md:209-215` (rejected: already a DEFERRED entry, and it is wrong *before* the edits apply, so it does not "become" wrong under criterion (d)).

FACT: The proposal text is byte-identical to the non-spec-r4 snapshot. Only `review-log.md` changed (compaction pass 1). `diff -u` per file across status/deviations/checklist/problem-statement/spec-changes/non-spec-changes/summary is empty. A round-5 reviewer gets no fix-stage text to scrutinise — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4

FACT: SPEC-1's citation `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s `SessionId session_id = 2` is EXACT, not one line off. The message opens at `:456`, `string pod_id = 1` is `:457`, and the address is `:458`. CORRECTS the Settled bullet "SPEC-1 cites `:458`, one line off" — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: The 18 current `sessionScopedMessages` keys are EXACTLY the 18 messages declaring `reserved "slot_id"` in `schemas/lenny-adapter.proto`, verified by a comment-stripped brace-depth parse: set difference is empty in both directions. TEST-2's `retiredDuplicateNumbers` membership claim (`non-spec-changes.md:64-65`) is true — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: The widened session set is exactly 26 = the 25 request types declaring a top-level `SessionId session_id` plus `CheckpointStart`, and no message anywhere in the file declares `session_id` and falls outside it. The 8 named additions close the set with no residue. TEST-2's population is complete and both widened arms pass on the tree as it stands — EVIDENCE: schemas/lenny-adapter.proto (census re-run 2026-09-07)

FACT: The addressing convention is green on day one in the strongest form. `grep -n SessionId schemas/lenny-adapter.proto` minus the exact form `SessionId session_id = N;` returns ONE line, `message SessionId {` at `:596`. There is no `repeated SessionId`, no `SessionId` under another field name, and no `string session_id`. D2's replacement gate cannot be red on the shipped proto — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: `tests/registers/identifier-senses.yaml` keys occurrences POSITIONALLY ("the 1-based position of the site among the retired-spelling sites that file carries in source order", `:5-8`), so a deletion inside `spec/04` could in principle renumber its eight `spec/04` rows. It cannot here: a case-insensitive grep of `spec/04_system-components.md` for every CH-RUNTIMEOPS retired spelling in the §28.3 naming table (`LifecycleChannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`) returns ZERO hits. The eight rows are vestigial. This is the strongest form of the Settled claim and the one an edit-site lens has to check, because the positional key is the hazard, not the file name — EVIDENCE: tests/registers/identifier-senses.yaml:5-8, :13-36; spec/28_communication-channels.md:154-159

FACT: `TestEveryMigrationGateIsRegisteredAtTierZero` is one-directional in the safe direction: `unregisteredGates(declared, tierZeroGates)` checks that every gate NAMED in `tierZeroGates` is still declared and hard-gated, never that every declared tier-0 test is named. The replacement gate therefore owes no registration. `claim_register_proto_agreement_test.go` IS in the list (`:70`) but by test-function name, which TEST-1 does not rename — EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-89, :247

FACT: `tests/spec-map.json` is authored, not generated: its own `description` reads "Maintained alongside code: every PR that touches spec/, pkg/, schemas/, migrations/, or charts/lenny/ updates this file." TEST-1 edits the authoring source, so the authored-vs-generated test passes — EVIDENCE: tests/spec-map.json:3

FACT: The three spec-map anchors resolve to the sections the proposal names. `:156` and `:169` sit under section key `"4.1"`, `:5670` under `"28.5.3"`, and the tier-3 file's whole-file credit `:172` under `"4.1"` — EVIDENCE: tests/spec-map.json:156, :169, :172, :5670

FACT: `tests/change-graph.json`, `tests/claim-map.json`, `tests/groups.yaml`, `tests/groups.subsets.yaml`, `tests/flake-budget.yaml`, `tests/spec-anchor-moves.json`, and `tests/spec-map-exceptions.yaml` name NONE of the six files §9 lists. `change-graph.json` covers `tests/tier0_static` by directory glob (`:464-465`), so no per-file entry drifts — EVIDENCE: tests/change-graph.json:464-465

FACT: The two tier-0 files 0076 landed do not read the §4.1 table, the shared parse, or `spec/04` at all. `adapter_barrier_doc_comment_scope_test.go` returns no hit for any of `spec/04`, `protoFields`, `protoServiceRequests`, `adapterProtoPath`; `adapter_proto_generation_scope_test.go` pins proto DOC COMMENTS only, and its expectations key on `CoordinatorFenceRequest`'s comment text, which this proposal does not touch — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:40-76

FACT: `docs/reference/adapter-contract.md` carries no scope column and no §4.1 mirror. Its only per-message scope sentence is `:81` (`ReportSessionScrub` "The request is session-scoped"), which SPEC-1 explicitly leaves and a tier-11 gate pins. `docs/getting-started/concepts.md:101` already describes `coordination_generation` as a session-record counter, so it is not a surface this reclassification supersedes — EVIDENCE: docs/reference/adapter-contract.md:69, :81; docs/getting-started/concepts.md:101

WATCHOUT: `grep -v 'review-log'` on a `diff -ru` of the proposal directory does NOT exclude the review-log's diff, because the hunk bodies do not contain the filename. Diff each of the seven files by name in a loop instead; that is what showed the round-4-to-5 delta was empty — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4

USEFUL [standing context Settled/Traps]: the compaction pass saved this round several hours. The proto census, the `:725`/`:726` survivor list, the blast-radius sweep, and the `slotAddressCaseFiles`/`spec-map` register facts were all re-derivable in minutes as spot checks precisely because the log said which method to use and which parsers derail. The Traps entry on naive proto parsers (same-line `message X {}`) is what made me write the brace-depth census correctly the first time.

OPEN: nothing new. The two live edit-site questions remain the ones already logged: whether the replacement gate keeps the path `tests/tier0_static/adapter_proto_message_scope_test.go` (load-bearing for `slotAddressCaseFiles:336`), and whether its cases can honestly carry a `// spec: 28.5.3` annotation for `tests/spec-map.json:5670`. Both are answerable only by the implementor or by a human, and neither makes the applied spec wrong as staged.


### [non-spec.5.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor this proposal names can perform the
action assigned to it, and I verified each one against the tree rather than against the log —
ALTERNATIVES: I weighed filing the "nothing keeps `sessionScopedMessages` complete once its rule becomes
proto-derivable" gap under (f) and declined: the tier-0 replacement gate checks the addressing convention
over every message the protocol declares, so the tier-3 hand list is redundant coverage rather than the
only pin, and the log already carries the point as an OPEN raised twice without conversion.

CORRECTS [Standing context / Settled, "Proto anchors, current and verified"]: the parenthetical
"`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below
the bar)" is wrong. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and
`:458` is `SessionId session_id = 2;`. SPEC-1's citation is exact — EVIDENCE:
schemas/lenny-adapter.proto:456-458, spec-changes.md:134-135.

FACT: no gate anywhere in `tests/` reads the §4.1 `#### Request Message Scope` block except the retiring
gate itself. I enumerated every `tests/**/*.go` naming `04_system-components.md`: they target §4.4
(`external_side_effect_recovery_test.go:136`), §4.6.1 (`eviction_coordinator_route_consistency_test.go:64`),
§4.6.3 (`spec_28_register_writers_test.go:759`), §4.7 (`spec_47_rpc_row_naming_test.go:46`,
`recycle_scrub_trigger_consistency_test.go:57`, `budget_extension_trigger_consistency_test.go:174`,
`session_scrub_report_addressing_doc_reconciliation_test.go:47`), §4.8, and §4.9
(`credential_delivery_field_locality_test.go:29`). SPEC-1's deletion reddens no tier-11 or tier-10 gate —
EVIDENCE: tests/tier11_docs/, tests/tier10_conformance/.

WATCHOUT: `grep -rn '4\.1' tests/tier11_docs/*.go` returns five hits in
`basic_level_echo_stamp_doc_reconciliation_test.go` (`:26`, `:132`, `:408`, `:582`, `:747`), each an
annotation reading `// spec: 4.1 (request message scope)`. The file reads only `docs/` pages and never
opens anything under `spec/`, so it is not an edit site — EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:48-53, :420, :503.

FACT: `tests/tier0_static/gate_integrity_test.go`'s `tierZeroGates` (`:63-89`) does not name the retiring
message-scope gate, so TEST-1 may rename its two cases freely. It DOES name
`TestClaimRegisterAgreesWithTheAdapterProto` at `:70`, in the file TEST-1 also edits for the parse's
signature change, so that function must keep its name — EVIDENCE:
tests/tier0_static/gate_integrity_test.go:70, tests/tier0_static/claim_register_proto_agreement_test.go:64.

FACT: the envelope arm is buildable from the shared text parse without a descriptor. The proto declares
every message at the top level (`grep -cE '^message \w+ \{'` = 88, `grep -cE '^\s+message '` = 0), a
`oneof` arm line (`CheckpointStart start = 1;`) matches `protoField` and yields the arm's type in its
first capture group, and `protoFields` already keys the frame message by that same name. The gate resolves
`fields["CheckpointStart"]["session_id"]` with no cross-file lookup. This closes the standing OPEN
"whether the implementor can build the envelope arm from the shared text parse" in the affirmative —
EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:25, :36-62; schemas/lenny-adapter.proto:1173-1178.

FACT: nested enum values do not derail the extended parse. Four nested `enum` blocks exist; an enum value
line (`SESSION_ENDED = 1;`) carries one token before `=` and `protoField` requires two, so no enum member
is ever reported as a field. `map<string, string> tracing_context = 4;` is likewise skipped, because
`[\w.]+` cannot match `map<string,`. Neither omission matters: no such declaration is named `session_id`
or typed `SessionId` — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:25,
schemas/lenny-adapter.proto:1679.

FACT: re-derived, with a comment-stripping brace-counting parse, that the 18 messages declaring
`reserved "slot_id"` are exactly the 18 keys of `sessionScopedMessages`, and that all 8 messages TEST-2
adds declare `SessionId session_id = 1` at top level and reserve nothing. Both widened tier-3 arms pass on
the tree as it stands, and `retiredDuplicateNumbers` keeps a set that is closed — EVIDENCE:
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63; schemas/lenny-adapter.proto:342,
365, 384, 404, 420, 1456, 1539, 1674.

FACT: `protoServiceRequests` survives the retirement with a live caller. The replacement gate needs it to
decide which `oneof`-carrying message is a request type: `CheckpointResponse` also declares a `oneof`
(`schemas/lenny-adapter.proto:1250`) and is the request type of no RPC, so the envelope predicate is only
well-defined against the RPC request set. Nobody should file the function as dead code after TEST-1 —
EVIDENCE: spec-changes.md:87-90, tests/tier0_static/adapter_proto_parse_test.go:68.


### [non-spec.5.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every load-bearing citation in the staged spec and non-spec blocks resolves and says what it is said to say, the derivation rule reproduces the retired table's classification exactly on the current proto, and the replacement gate's constraint is already satisfied by the shipped file — ALTERNATIVES: I built and discarded four candidate findings, each listed below with the evidence that killed it, so a later round does not spend a pass re-deriving them.

FACT: the staged text is byte-identical to the round-4 snapshot. `diff -u` over spec-changes, non-spec-changes, summary, implementation-checklist, problem-statement, and status against `scratchpad/cp-snap/.../non-spec-r4` prints nothing; only `review-log.md` changed (compaction pass 1). A round-5 lens reading "read the changed sections first and hardest" has no changed section to read. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4/

FACT: SPEC-1's `ReportSessionScrubRequest` anchor is exactly right, not one line off. `schemas/lenny-adapter.proto:458` IS `  SessionId session_id = 2;` (message opens at :456, `reserved 3;` at :464, `reserved "slot_id";` at :465). — EVIDENCE: schemas/lenny-adapter.proto:456-467
CORRECTS [review-log Settled, "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest` `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". `:457` is `string pod_id = 1;`. The address is at `:458` and SPEC-1's citation is correct. Nobody should "fix" that citation.

FACT: the name/type biconditional holds over EVERY message in the adapter proto, not only the request types. Stripping comments and grepping both directions returns the same 26 lines: every field named `session_id` is `SessionId session_id`, and every field of type `SessionId` is named `session_id`. So D2's replacement gate clauses (a) and (b) are green on the shipped file on day one, and there is no `string session_id` anywhere in `schemas/lenny-adapter.proto` for the gate to trip on. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

FACT: `validateSpecMapTestFuncs` walks EVERY section and every `path::TestName` entry regardless of tier. It unmarshals all of `doc.Sections`, splits on `::`, and requires `hasTestFuncDecl` for any `.go` path not waived in the pending file. So a dangling `tests/tier0_static/...::TestName` IS caught, and the implementation checklist's stated reason for folding the spec-map re-registration into S2 rather than a step of its own holds. — EVIDENCE: cmd/lenny-test/cmd_validate.go:941-998
WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go:232-234` carries a comment saying "The map validator walks tier 2 through tier 10 only". That sentence is about a *different* validator (the credit/selection walk), and reading it as being about `validateSpecMapTestFuncs` produces a false finding that S2's justification is wrong. It is not. — EVIDENCE: cmd/lenny-test/cmd_validate.go:955-983 vs tests/tier0_static/spec_map_slot_address_registration_test.go:232-234

FACT: the reverse spec-map direction is NOT enforced for the gate file, which settles half of the standing OPEN about §28.5.3. `TestConsistencyGatesAreMappedOnlyToSectionsTheyAnnotate` ranges over `consistencyGateFiles` (`:171-176`), a five-entry list that does not contain `adapter_proto_message_scope_test.go`; `TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise` ranges over `slotAddressAbsenceTestFile` alone. So a surplus `tests/spec-map.json:5670` credit under 28.5.3 whose annotation does not name 28.5.3 turns no gate red. Only the missing direction is held, by `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:94,171-183,970-987
WATCHOUT: the live half of that OPEN is the other direction. If the replacement gate keeps a `// spec: ... 28.5.3` annotation, `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` DEMANDS a 28.5.3 credit for that case, and the per-case branch runs for EVERY annotated function in the file, not just the ones spec-map already names. An implementor who adds a second or third test function to the replacement gate must register each one under every section its own annotation cites. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:979-987

FACT: `addressRuleCases` is a purely one-directional hand inventory with no completeness derivation behind it. `TestAddressGuardCasesCiteTheSectionStatingTheRule` iterates the inventory and checks each named case cites the derived section; nothing computes what the inventory should contain. So the reclassification making `CoordinatorFenceRequest` session-scoped creates no obligation to add `pkg/adapter/coordination_test.go` to it, and the proposal's decision to leave that file alone is safe. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:128-149

MISTAKE (candidates I built and killed, so nobody rebuilds them):
1. "The `addressRuleCases` inventory becomes incomplete once the fence is session-addressed, and `address_rule_citation_test.go` is not in §9." Killed by the gate being one-directional (above) and by the inventory's own comment scoping it to one guard per *leg*, not per message.
2. "TEST-1's spec-map re-registration under §28.5.3 turns a reverse gate red." Killed: no reverse gate covers that file.
3. "S2's stated reason for folding the spec-map edit into itself is false because validate-maps skips tier 0." Killed by reading `validateSpecMapTestFuncs` (above).
4. "A message declaring a `oneof` of non-frames escapes both classification paragraphs." Killed as wording only: no such message exists, `CheckpointRequest` and `CheckpointResponse` are the only `oneof` blocks, and §4 of the staged spec changes says so explicitly.

FACT: TEST-2's arithmetic is exact against the tree, re-derived here by an independent parse. 31 RPC request types; 25 declare the top-level address; 18 messages declare `reserved "slot_id"` and they are EXACTLY today's `sessionScopedMessages` keys; 17 of those 18 are request types; the set difference (25 addressed request types minus those 17) is exactly the 8 messages TEST-2 names, in the same order-independent set. Widened session set is 26. — EVIDENCE: schemas/lenny-adapter.proto, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63
FACT: commit `040323634` added exactly 18 `reserved "slot_id"` lines to the proto and mentions `CoordinatorFence` nowhere in its proto diff, which is the correct and verified ground for the fence's empty retired-field column. — EVIDENCE: git show 040323634 -- schemas/lenny-adapter.proto

UNVERIFIED: still nobody has RUN tier 0 or tier 3 for this proposal. Every green/red claim in this log, mine included, is derived from reading gate source. The one that would most repay an actual run is S1's stated red window, and the claim that the replacement gate passes on the shipped proto. The implementor should run `lenny-test --tier 0` immediately after S2 rather than at the end of S3.


### [non-spec.5.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the Kubernetes-idiom lens has no subject in this proposal: spec/04 §4.1 is "Edge Gateway Replicas" (`spec/04_system-components.md:40`), the block SPEC-1 replaces classifies gRPC request messages on the gateway-adapter protocol, and both non-spec deliverables are a tier-0 static gate over `schemas/lenny-adapter.proto` and a tier-3 contract suite. No CRD, status subresource, field manager, finalizer, admission webhook, RBAC rule, NetworkPolicy, chart, or reconcile loop is touched. — ALTERNATIVES: I looked for a status-as-command-channel or controller-on-the-hot-path angle in the fence path and found none (see FACT below).

FACT: the coordination generation the reclassified message carries is a Postgres `sessions` row column guarded by a Redis TTL lease and a Postgres `FOR UPDATE` lock, not a Kubernetes object, so nothing in this proposal can implicate etcd-as-message-bus, two managers racing a status field, or a synchronous path blocking on a reconcile. — EVIDENCE: `spec/10_gateway-internals.md:30` ("Each session row carries a `coordination_generation` counter"), `:37` (the CAS `UPDATE sessions SET coordination_generation = ...`), and `pkg/gateway/coordination/coordfence/coordfence.go:160-188`, whose stale and relinquish arms call `f.generations.CoordinationGeneration` and `f.relinquish` with no client-go call anywhere in the file.

FACT: a grep of the seven non-log proposal files for `crd|controller|status subresource|finalizer|reconcil|apiserver|webhook|kubernetes|helm|chart|rbac|NetworkPolicy` returns only the word "reconciliation" used of the retiring tier-0 table gate (e.g. `0075_...spec-changes.md:36`, `0075_...summary.md:21`). A future Kubernetes-lens agent can stop at that grep. — EVIDENCE: `proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:21`

FACT: nothing in the proposal changed between round 4 and round 5. `diff -u` on each of spec-changes, non-spec-changes, implementation-checklist, summary, problem-statement, status, and deviations against `scratchpad/cp-snap/.../non-spec-r4` is empty; only the review log changed, by the compaction pass. The "read the changed sections hardest" instruction has no target this round. — EVIDENCE: `scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4/`

USEFUL [standing context, Traps]: the compacted `## Standing context` (review-log.md:3-129) is worth reading before any lens work on this proposal. Its Settled bullets on the proto census, the `:725`/`:726` survivors, and the blast radius of the shared parse answered in one read what the ledger says cost a dozen independent re-derivations, and its Traps entry at review-log.md:88 (orchestrator briefs carry stale proto anchors, each off by seven) saved me from chasing `:589`/`:1447`/`:1448`.


### [non-spec.5.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every flow this proposal describes (SPEC-1's rule -> the replacement tier-0 gate -> the tier-3 split) traces end to end against the tree with no unreachable trigger, no bypassed gate, no granularity mismatch, and no predicate drift that survives cross-reading D2, the staged §4.1 block, §8's negative-case list, and the summary's gate bullet — ALTERNATIVES: I considered filing §3's design-overview sentence, which describes the replacement gate as checking only "the session address is spelled one way in both directions and that a stream envelope carries exactly one addressing frame" and omits the envelope's "declares no top-level address of its own" clause that D2 (`spec-changes.md:41-42`), the staged spec paragraph (`spec-changes.md:22-23`), §8 (`non-spec-changes.md:87-89`), and the summary bullet (`summary.md:21-24`) all carry. Declined: §3 is an abbreviated overview, all four normative carriers agree, and this loop has refuted three near-identical "an indicative/overview block states the target at coarser granularity" findings already.

CORRECTS [standing context, Settled, "Proto anchors, current and verified"]: the parenthetical "SPEC-1 cites `:458`, one line off" is wrong. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` (`spec-changes.md:134-135`) is exact. The same bullet's `message SessionId` `:596` is the one that is off by one: `:595` is `message SessionId {` and `:596` is `string value = 1;`, which is what the problem statement cites at `problem-statement.md:38`. Both are below the bar; the point is that the log's own correction was pointed at the wrong citation.

FACT: the envelope arm IS buildable from the shared text parse, which closes the standing Open "whether the implementor can build the envelope arm ... without resolving a `oneof` arm's type to another top-level message and reading its fields". `protoFields` already keys by message name and returns a per-message field set (`tests/tier0_static/adapter_proto_parse_test.go:36-62`), so once the parse captures each field's type, the gate reads `CheckpointRequest`'s oneof arm `CheckpointStart start = 1` (`schemas/lenny-adapter.proto:1175`), looks up `fields["CheckpointStart"]`, and finds `SessionId session_id = 7` (`:1217`). No descriptor resolution and no second parser are needed. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36-62; schemas/lenny-adapter.proto:1173-1176, :1217

FACT: `protoServiceRequests` survives TEST-1 and does not become dead code. The staged envelope clauses are scoped to a *request* message carrying a `oneof` (`spec-changes.md:41`, `:88-90`), and `CheckpointResponse` also declares a `oneof`, so the gate still needs the RPC request-type set to exclude it. `unused` (a tier-0 golangci-lint check) therefore does not fire on the shared parse after the table's readers go. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:68-90; spec-changes.md:88-90

FACT: the sibling file in the tier-3 package cannot be broken by TEST-2's retype. `tests/tier3_contract/adapter_session_address/send_message_stamp_test.go` references none of `sessionScopedMessages`, `retiredFieldName`, `retiredWrapperName`, `messageDescriptors`, `reservesNumber`, or `reservesName`; every reader of those six is inside `session_address_wire_test.go`. Adding it to §9 would stage an edit nothing requires. — EVIDENCE: grep over tests/tier3_contract/adapter_session_address/*.go returns hits only in session_address_wire_test.go:31-181

FACT: the addressing convention D2's gate checks is green file-wide today, re-verified by a fresh two-direction grep rather than by trusting the census. 26 fields are named `session_id` and all 26 are of type `SessionId`; 26 fields are of type `SessionId` and all 26 are named `session_id`; the only other field whose name contains "session" anywhere in the file is `bool mid_session = 4` at `schemas/lenny-adapter.proto:724`, which trips neither half of the biconditional. All eight messages TEST-2 adds to the widened session set declare `SessionId session_id = 1` and none declares `slot_id`, so both widened arms pass on the tree as it stands. — EVIDENCE: schemas/lenny-adapter.proto:341, :364, :383, :403, :420, :1456, :1539, :1674, :724

FACT: nothing outside the retiring gate reads the §4.1 block SPEC-1 deletes. `parseMessageScopeTable` is the only parser of the table anywhere in the tree; the tier-11 and tier-10 readers of `spec/04_system-components.md` take §4.4, §4.6.1, §4.7, and §4.9 by `specSection` and never §4.1; and `address_rule_citation_test.go`'s derived sentence lives at `spec/05_runtime-registry-and-pool-model.md:515`, outside the deleted range. S1's red window is therefore confined to the one gate S2 replaces, exactly as the checklist states. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:54-70; tests/tier11_docs/{external_side_effect_recovery,eviction_coordinator_route_consistency,spec_28_register_writers,recycle_scrub_trigger_consistency}_test.go; tests/tier0_static/address_rule_citation_test.go:22

FACT: the spec-map anchors TEST-1 names resolve to the sections it claims. `tests/spec-map.json:156` and `:169` sit under key `"4.1"`, `:5670` under `"28.5.3"`, and `:172` is the tier-3 file's whole-file credit under `"4.1"`. Checked by scanning backwards for the enclosing numeric key rather than by eye. — EVIDENCE: tests/spec-map.json:156, :169, :172, :5670

WATCHOUT: the checklist's S1-before-S2 order is the one that opens the tier-0 red window, and reversing it (rule gate first, table deletion second) would close it. Do not "fix" that: `.claude/rules/spec-driven-development.md` requires the spec change to land and be verified before the code that depends on it, so S1 first is the governing order and the red window is the price the proposal already prices. — EVIDENCE: implementation-checklist.md:8-10; .claude/rules/spec-driven-development.md "Land and verify any spec change first"

WATCHOUT: `docs/api/internal.md:209-215` publishes a `CheckpointRequest` excerpt with a top-level `string session_id = 1`, which after SPEC-1 illustrates a spelling the new tier-0 gate refuses. It is NOT an edit site this proposal misses under rule (d), because the excerpt is already false about the shipped proto today (the real message is a `oneof msg` plus `int64 coordination_generation = 4` and declares no `session_id`) and the gate reads `schemas/lenny-adapter.proto` alone. It is pre-existing and already carried as a DEFERRED. Do not re-file it as a missed surface. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187

UNVERIFIED: nobody has yet run tier 0 or tier 3 against a working tree with the split applied. Everything above, including the two-direction convention check and the envelope-arm feasibility argument, is derived from reading files. The implementor is the first to execute it.


### [non-spec.5.review-operational.1]

DECISION: Returned zero findings under the operational-consistency lens (conditions, metrics, alerts,
operator docs, observability inventories) — BECAUSE this proposal touches no metric, no alert rule, no CRD
condition, and no reader-facing page, and every operational-flavoured citation it makes resolves and says
what it claims — ALTERNATIVES: rejected filing the §28.5.3 spec-map re-registration honesty question (four
prior lenses recorded it as an Open and nobody filed it; `TestSlotAddressCasesAreCreditedToEverySection
TheyAnnotate` is one-directional so a surplus credit reddens no gate); rejected filing §8's tier-11 blank
as marker-without-constraint (tier 11 here is a regression run over a spec edit, not a new behavior).

CORRECTS [Standing context "Proto anchors, current and verified"]: that bullet says
`ReportSessionScrubRequest` has "its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)".
That is backwards. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is
`string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`. SPEC-1's citation at
`spec-changes.md:134-135` is exact and needs no refresh. Do not "fix" it.
EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: No operator-facing surface mirrors the §4.1 classification. A scope-word sweep over `docs/` returns
only `docs/reference/adapter-contract.md:81` (the `ReportSessionScrub` row pinned to `spec/04:725` by the
tier-11 gate), plus JSONL-frame prose in the runtime-author guide and unrelated uses in glossary/runbooks.
`spec/16_observability.md` and `docs/reference/metrics.md` carry no metric labelled by message scope, and
no alert rule references the classification. So retiring the table cannot mislead an operator.
EVIDENCE: docs/reference/adapter-contract.md:81, docs/reference/metrics.md:180

FACT: No tier-11 test reads §4.1. Every `tests/tier11_docs/*.go` that opens `spec/04_system-components.md`
targets §4.4, §4.6.1, §4.6.3, §4.7, or §4.9. `basic_level_echo_stamp_doc_reconciliation_test.go` and
`tests/tier3_contract/rest_sessions/slot_address_absence_test.go` mention "message scope" only inside
`// spec:` annotations, which survive because SPEC-1 keeps the `#### Request Message Scope` heading.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26,
tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99

FACT: The summary's "Defects in the shipped tree" entry is accurate end to end, re-derived independently.
The transient arm is `pkg/gateway/coordination/coordfence/coordfence.go:180-183` (`default:` through the
transient log) and the stale arm is `:171-179` (re-read through `relinquish`); a lost ack is
DEADLINE_EXCEEDED, which takes the transient arm, and the second attempt hits
`pkg/adapter/coordination.go:126-134`'s `gen <= st.coord.lastFenced` refusal, so the coordinator does give
up on attempt 2 of 3. `spec/10_gateway-internals.md:38`, `:39`, `:40` and
`spec/28_communication-channels.md:314-317` all say what `summary.md:135-138` says they say.
EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:171-183, pkg/adapter/coordination.go:126-134

USEFUL [Standing context, Traps #65 "Long lines hide the sentences citations point at"]: saved a false
filing on `spec/05_runtime-registry-and-pool-model.md:515` and `spec/10_gateway-internals.md:60`. Piping the
line through `tr '.' '\n' | tail` recovers the cited clause immediately.

USEFUL [Standing context, Traps #86 "Orchestrator briefs carry stale content"]: this round's brief again
carried the pre-refresh proto anchors (`:589`, `:1166`, `:1447`, `:1448`) and again described §1.2 as
claiming the pod-wide ground holds. The proposal text is correct on all four.

WATCHOUT: `diff -ru` against the round-4 snapshot shows the staged files byte-identical; only
`review-log.md` changed (compaction pass 1 added the whole `## Standing context`). A reviewer who reads
only the diff sees nothing and may conclude the round was a no-op fix stage; the staged text simply has
not moved since round 4. EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r4


### [non-spec.5.review-performance.1]

DECISION: returned empty. BECAUSE the performance/scalability/failure-mode lens has no subject on this proposal, and I established that mechanically rather than by impression: a grep over both staged files for postgres|redis|etcd|metric|counter|gauge|write|lease|quota|rate returns only incidental prose (the `spec/10:60` gauge sentence SPEC-1 explicitly leaves unedited, and test-function names containing "Reserved"). No store, no emission site, no per-request/per-session/per-task write, no net-new watch or informer, no leader-serialized key, no reconcile or work-queue pressure. There is no top-tier arithmetic to state because the numerator is zero. ALTERNATIVES: I considered filing on the coordinator-handoff failure path, which is the one runtime path the reclassification names, and rejected it — see the FACT below.

FACT: the proposal's two runtime citations under the coordinator-handoff path are both accurate, verified line by line today. `pkg/gateway/coordination/coordfence/coordfence.go:180-183` is the `default:` transient arm (`incRetry` + log, same generation), `:171-179` is the stale arm (re-read `CoordinationGeneration`, no advance, `relinquish`), and `DefaultMaxAttempts = 3` is at `:52`. On the adapter side `pkg/adapter/coordination.go:109-111` is the empty-session-id `InvalidArgument`, `:116` the `boundSlotState` resolve, `:127-134` the `coordinator_handoff_stale` `FailedPrecondition`. The summary's "gives up on the second of its three attempts" is a correct reading of that control flow. EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-188, pkg/adapter/coordination.go:107-134

FACT: the reclassification cannot degrade any shipped failure mode, and the reason is one line of set theory worth keeping. The derived session class is a strict superset of the retired table's on the current proto (only `CoordinatorFenceRequest` moves, pod to session; nothing moves the other way), so the only behavioral obligation the class carries — the `spec/05:515` refusal of a session-scoped request with an empty identifier — can only be ADDED to a message, never removed from one. And the single message it is added to already performs it. A future lens reaching for "does this make handoff less reliable" can stop at that sentence. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515, pkg/adapter/coordination.go:109-111

FACT: tier-0 and tier-3 cost both go DOWN or stay flat, so there is no test-runtime finding either. The replacement gate reads `schemas/lenny-adapter.proto` alone (88 messages, one file) where the retiring gate read that file PLUS the whole of `spec/04_system-components.md` via `parseMessageScopeTable`. The tier-3 suite widens from 18 to 26 in-process protoreflect descriptor lookups. Nobody should file on either number. EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: I spot-checked all eight TEST-2 additions against the proto with a brace-counting parse rather than trusting the standing context, and every one declares `SessionId session_id` and none mentions `slot_id`. Both widened arms (`:78-92` no-second-address, `:99-116` declares-the-address) pass on the tree as it stands, so S3 cannot turn tier 3 red on the additions. This is the third independent derivation of that fact; it is settled and the next agent should spot-check at most one message. EVIDENCE: schemas/lenny-adapter.proto:1455 (fence), :1538 (ExportPaths), :1673 (ConfigureWorkspace), :341/:364/:383/:403/:419 (the five tool/connector requests)

USEFUL [standing-context Traps]: the "Cache keys omit summary.md and review-log.md" trap saved me from mis-reading my own cache slot. My round-5 key was a miss, but the trap told me why a hit would have been suspect: only `review-log.md` changed since r4 (compaction pass 1), so the key is identical across r4 and r5 and a spec-lane run of a sibling lens could have poisoned the slot. I checked `diff -rq` against the snapshot first and confirmed the staged text is byte-identical to what round 4 reviewed, which is the right order of operations for anyone whose cache misses.

USEFUL [standing-context Traps]: "Orchestrator briefs carry stale content" is correct for round 5 as well. My brief's proto anchors were still the pre-refresh set (`:589`, `:1166`, `:1447`, `:1448`, each off by seven) and it still described "§7 question 1" as the existential question when that question is now summary OD1. I trusted the proposal and lost no time.

WATCHOUT: the `oneof` envelope is the one place where a reliability-minded reviewer can talk themselves into a false finding, and the shape of the trap is worth naming. Under the derived rule `CheckpointRequest` is session-scoped, and it declares no top-level `session_id` at all, so `spec/05:515` reads as ordering the adapter to reject every checkpoint envelope. It does not: the envelope clause addresses it through `CheckpointStart`, and `pkg/adapter/checkpoint.go:74-84` enforces the refusal on the opening frame. Crucially this is NOT a change — the retired table already classified `CheckpointRequest` session-scoped (problem-statement.md:22 records the row as `session`/no), so the proposal alters nothing about that path. EVIDENCE: pkg/adapter/checkpoint.go:74-84, proposals/0075_.../0075_....problem-statement.md:22

UNVERIFIED: still nobody has run tier 0 or tier 3 on this branch. Every green-on-day-one claim in this log, mine included, is derived from reading files. The two that would actually be settled by a run are the replacement gate passing on the shipped proto and `validate-maps` staying green across S2's single commit. The implementor settles both in the first ten minutes; no reviewer can.


### [non-spec.5.review-reliability.1]

DECISION: returned zero findings — BECAUSE the proposal changes no runtime code, no proto, and no handler, so it adds no retried operation, no delivery path, no lease, no drain, and no fail-open recovery for this lens to trace; every recovery-mechanism citation it does make I re-verified line by line and all of them hold — ALTERNATIVES: re-filing the coordfence same-generation-retry refusal (recorded already in summary.md:176-197 as an unstaged shipped-tree defect with 0080 §1.16 named as owner, and in the review log's Open list) and re-filing the missing 1-second backoff (pre-existing, same Open entry); both rejected as pre-existing and explicitly non-goal per spec-changes.md:151-152.

FACT: every reliability citation in summary.md's "Defects in the shipped tree" bullet is exact, re-verified 2026-09-07. `spec/10_gateway-internals.md:39` is verbatim "up to 3 attempts with 1-second backoff". `pkg/gateway/coordination/coordfence/coordfence.go:171-179` is exactly the stale arm (re-read at :171, relinquish at :179) and `:180-183` exactly the transient `default:` arm. `pkg/adapter/coordination.go:127-134` is exactly the stale refusal, `:109-111` exactly the empty-session-id refusal, `:116` exactly the `boundSlotState` resolve. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:171, pkg/adapter/coordination.go:127

FACT: the 5-second fence deadline the summary attributes to the driver is real and lives one layer down, not in the coordfence loop. `coordfence.go` sets no timeout at all; `CoordinatorFenceTimeout = 5 * time.Second` is at `pkg/gateway/runtime/adapterclient/coordinatorfence.go:20` and is applied with `context.WithTimeout` at `:49`. A reviewer who greps only coordfence.go for a deadline will wrongly conclude the outbound RPC is unbounded and file a false finding. — EVIDENCE: pkg/gateway/runtime/adapterclient/coordinatorfence.go:20,49

FACT: no runtime code dispatches on the §4.1 scope classification. `grep -rn "session-scoped\|pod-scoped\|sessionScoped\|podScoped" pkg/ cmd/ --include=*.go` returns only doc comments and unrelated identifiers; there is no table of session-scoped methods in an interceptor or in the adapter. That is why retiring the table has no reliability blast radius, and it is worth stating once so no future lens re-derives it. — EVIDENCE: pkg/adapter/oplock.go:77 (the nearest hit, and a known decoy per the log's Traps)

FACT: the reclassification adds exactly one new spec obligation and the code already meets it. The derived session class is the retired table's session class plus `CoordinatorFenceRequest` (26→27 session, 6→5 pod), so `spec/05_runtime-registry-and-pool-model.md:515`'s empty-identifier refusal is only ever added, never removed; the fence's handler performs it before resolving anything. No message loses a refusal it currently carries. — EVIDENCE: pkg/adapter/coordination.go:109-111, spec/05_runtime-registry-and-pool-model.md:515

FACT: `boundSlotState` returns `FailedPrecondition "session %s is not assigned to this pod"` when the registry holds no bound entry, which is the mechanism §1.2's "resolving nothing is the refusal" names. The claim is accurate. — EVIDENCE: pkg/adapter/slotsession.go:274-283

WATCHOUT: the round-5 diff against the r4 snapshot is review-log-only, and the diff against `non-spec-r4-start` is empty. The newest proposal text is actually the r3→now hunk (the two paragraphs re-grounding the unsatisfiable retired-field column on the fence's field history rather than on §6). Diff against `non-spec-r3-start` to see it. — EVIDENCE: proposals/.../0075_....non-spec-changes.md:11-13

USEFUL [standing context, Traps]: the "Naive proto parsers derail on this file" and "Long lines hide the sentences citations point at" entries both saved real time — `spec/05:515`, `spec/10:39`, and `spec/04:725`/`:726` are each one very long line and a truncated grep makes a correct citation look wrong. Piping through `tr '.' '\n' | tail` is what recovered `spec/05:515`.


### [non-spec.5.review-security.1]

DECISION: Returned an EMPTY findings list under the security lens, the fifth consecutive empty pass for this
lens on this proposal — BECAUSE the seven non-log proposal files are byte-identical to the `non-spec-r4`
snapshot (`diff -u` on each of spec-changes, non-spec-changes, implementation-checklist, summary,
problem-statement, status returns nothing; only the review log grew, by the compaction pass), and I
re-derived the two lens checks from the tree rather than replaying the standing context: check (1) finds one
control removed, the tier-0 table-reconciliation gate, removed openly with its replacement's clauses AND its
limit stated in normative spec text and both residuals named in summary OD1; check (2) finds no security
bound anywhere in the staging — no residual-state limit, no quota ceiling, no reuse counter, no Redis-backed
role, and no value sourced from a pod self-report — ALTERNATIVES: the session-scoped fence exiting a pod-wide
hold (declined, 0076's OD3 territory and the allowlist is keyed on method names), the nested-address hole
(declined, paragraph 3 reads onto it), the fail-open unconventional-spelling residual (declined, disclosed in
the staged text and in OD1).

FACT: TEST-2's widened tier-3 population is green on the shipped proto, verified message by message rather
than taken from the log. All eight additions are declared top-level in `schemas/lenny-adapter.proto`, so
`msgs.ByName` resolves each through `ParentFile().Messages()`, each declares `SessionId session_id = 1`, and
none declares any `slot_id` field. So both widened arms
(`TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` at `:78-92`,
`TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` at `:99-116`) pass, and the widening is purely
additive coverage with no assertion weakened — EVIDENCE: schemas/lenny-adapter.proto:342, :365, :384, :404,
:420, :1456, :1539, :1674; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:67-70,
:87-90, :107-114.

FACT: the summary's one unstaged-defect entry is citation-accurate end to end, which matters because a false
citation inside it would be a finding under (a). `pkg/adapter/coordination.go:127-134` is the
`coordinator_handoff_stale` refusal on `gen <= st.coord.lastFenced` reading the per-session entry;
`pkg/gateway/coordination/coordfence/coordfence.go:171-179` is the stale arm that re-reads the authoritative
generation and relinquishes when it has not advanced; `:180-183` is the transient arm that retries at the
same `gen`. The described sequence (a landed fence whose ack is lost is retried at the same value and refused
as stale on attempt two) follows from those three sites as written — EVIDENCE:
pkg/adapter/coordination.go:107-134; pkg/gateway/coordination/coordfence/coordfence.go:160-188.

FACT: `pkg/adapter/coordination.go:109-111` refuses an empty session identifier BEFORE `boundSlotState` at
`:116` and before the generation check at `:121`, so the one behavioral obligation the reclassification
imports (`spec/05_runtime-registry-and-pool-model.md:515`) is met on the first branch of the handler, not
incidentally by a later resolve failing. Re-derived rather than trusted — EVIDENCE:
pkg/adapter/coordination.go:108-122.

FACT: nothing in `pkg/` or `cmd/` branches on the §4.1 message-scope class. A repo-wide grep for
`sessionScoped|session-scoped|podScoped|pod-scoped` over non-test Go returns only prose comments about
stores, frames, contexts, and Redis key prefixes, plus `pkg/adapter/holdstate.go:113`, which states the
opposite direction (the generation is per bound session). The reclassification therefore has no code edit
site at all, which is why §9 lists no `pkg/` path — EVIDENCE: pkg/adapter/holdstate.go:113;
pkg/gateway/storage/rediskeys/rediskeys.go:11.

WATCHOUT: the cache key is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no lane and
no `summary.md` component, and this round's key is `09a51355eaa8`. Because round 4 landed no text change, a
round-6 run of any lens over the same three files gets the same key. If a future fix pass edits only
`summary.md` — which is where OD1, OD2, and the unstaged-defect entry live, and where a security-relevant
claim could plausibly be rewritten — the cache will serve a stale hit and the lens will not actually re-read
the change. Verify the summary by hand before trusting a hit — EVIDENCE:
proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:87.

USEFUL [non-spec.3.review-security.1]: its FACT that `coordinatorHoldAllowedMethods` is keyed on gRPC method
names rather than on the message's scope class is the one that closes the only plausible security regression
here, and it held on independent re-check. Nobody needs a fifth derivation of it.

USEFUL [spec.4.review-security.1]: its FACT that the derived session class is a strict superset of the
retired table's (delta of exactly one message, pod to session) is what makes the whole retirement
fail-closed-safe, because the `spec/05:515` empty-identifier refusal hangs off the session class and can
therefore only be added. I re-derived the delta from the proto and the table and reached the same answer.

OPEN: nothing new. The security lens has now returned empty on five consecutive rounds across both lanes
(spec.4, spec-recheck.2, non-spec.1, non-spec.3, non-spec.5) with the load-bearing facts re-derived
independently each time. Retiring it is safe on the current text; a fix pass that edits the staged §4.1
constraint paragraph, the OD1 residual statement, or `pkg/`-facing prose should re-run it.


### [non-spec.5.review-test-coverage.1]

DECISION: Returned an EMPTY findings list for the test-coverage lens, the fifth empty pass on this lane — BECAUSE `diff -u` of every proposal file except the review log against `scratchpad/cp-snap/.../non-spec-r4` prints NOTHING (spec-changes, non-spec-changes, implementation-checklist, summary, status, problem-statement are all byte-identical to round 4), and an independent re-derivation of §8 against D2's four gate clauses still maps one enumerated negative case to each, plus the tier-3 fence-address constraint, with tiers 0, 3, and 11 all named — ALTERNATIVES: filing tier 11 as named-but-uncased (rejected a fourth time, re-verified below); filing "no case pins that the extended shared parse still reports the same field set to `claim_register_proto_agreement_test.go`" (rejected: the constraint's remedy is a stated constraint, not a case, and the risk is a mechanism/edit-sites subject, not a testing gap); filing "nothing keeps the widened `sessionScopedMessages` complete" (rejected: hardening of a pre-existing weakness, already sitting under `## Open` in the standing context and declined by two lenses).

FACT: The tier-0 gate file carries exactly TWO test functions, `TestAdapterProtoRequestMessagesAreClassifiedByScope` (`tests/tier0_static/adapter_proto_message_scope_test.go:136`) and `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` (`:156`), and BOTH are the ones `tests/spec-map.json` registers (`:156`, `:169`, `:5670`). There is no third case for TEST-1's re-registration to strand. A future round can stop re-deriving this. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:136,156; tests/spec-map.json:156,169,5670

FACT: `tests/tier0_static/adapter_proto_parse_test.go` appears in NO register — not `tests/spec-map.json`, not `tests/change-graph.json`, not `tests/claim-map.json`. Only `spec_map_slot_address_registration_test.go:337` names it, by path. So TEST-1's extension of the shared parse owes no registration edit of its own, and §9's four-file test list is complete for registration purposes. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:337

FACT: Tier 11 carries no gate that reads §4.1. A grep for `Request Message Scope` across the tree returns `spec/04_system-components.md:149` plus the two tier-0 files and this proposal; `tests/tier11_docs/` holds nothing that opens `spec/04` §4.1's block. §8 naming tier 11 without a case is therefore a regression-run declaration, not an uncased tier. Fourth confirmation; treat it as settled. — EVIDENCE: spec/04_system-components.md:149; tests/tier0_static/adapter_proto_message_scope_test.go:41-58

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its FACT that the reclassification's single imported behavioral obligation is already pinned held a fourth time and again did the whole tier-1 question. `pkg/adapter/coordination.go:108-110` returns `InvalidArgument` on an empty session id before `boundSlotState` resolves anything, and `TestCoordinatorFenceRejectsMissingSessionID` (`pkg/adapter/coordination_test.go:35-43`) asserts it. That is why tier 1 is legitimately absent from §8, and it is the answer to any "the fence became session-scoped, where is its new test" filing.

WATCHOUT: The tier-3 suite's four test functions and their loop sites are exactly as TEST-2 describes them, but the LINE numbers TEST-2 quotes for the loops are the `for` statements, not the function declarations: `:81` and `:102` are `for name := range sessionScopedMessages`, `:130` is the third loop, and `:150-158` is `TestTheRetiredAddressWrapperIsGone_spec_15_4`. A reader who expects function declarations at those lines will think the citations are stale. They are not. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:78,81,99,102,127,130,150

### [f4.cleanup]

DECISION: rewrote nothing in `summary.md` — BECAUSE the file already carried exactly the eight target headings in the target order (`# Summary: ...`, `## Summary` holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**`, then `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, `## Deliverable index`), with no lead paragraph above `## Summary`, no `### Retired` block or equivalent inside the open-decisions section, no meta-list of staged items, and no errata block owed to files this loop cannot edit — ALTERNATIVES: moving the `**Watch out for.**` sentences about 0073's immutability and the 0076 sequencing into `## Impacts on other proposals` under the prose-about-another-proposal rule; rejected because that part is a listed part of `## Summary` rather than unlisted content, the sentences are implementor warnings rather than assertions about another proposal's continued validity, and the impacts rows for 0073 and 0076 already carry the validity claims.

FACT: every item this firing handled is accounted for in the file as it stands. OD1 and OD2 are the two entries under `## Open decisions for human to make`, both keeping their identifiers verbatim; the equal-generation re-fence (`marker:unscoped`) is the single entry under `## Defects in the shipped tree that this proposal does not stage`, verbatim as the earlier pass confirmed it; `marker:spec-1` (the retiring gate's declared-scope-versus-handler limit) is carried in the 0073 impacts row's "what stands" clause and again in the 0080 row; and 0073, 0076, and 0080 hold one impacts row each. Nothing was left unplaced and nothing was dropped.
EVIDENCE: 0075_....summary.md:83-206

FACT: no row was added for proposal 0036. The firing recorded `marker:0036` (0036's staged `ExtendCredentialLease` RPC would have been refused by the tier-0 gate this proposal retires) as gate-refuted and apply-refuted, and the summary carries no mention of 0036 anywhere. `grep -n 0036 summary.md` is empty.
EVIDENCE: 0075_....summary.md

FACT: both section preambles are true of the entries they now head, checked rather than assumed. `## Defects in the shipped tree that this proposal does not stage` opens by saying both specification defects are staged (SPEC-1 and TEST-2) and that one further defect was confirmed in the working tree and left where it is; exactly one entry follows it. `## Open decisions for human to make` and `## Impacts on other proposals` carry no preamble. This pass moved nothing, so it falsified no preamble and corrected none.
EVIDENCE: 0075_....summary.md:167-174

FACT: `## Deliverable index` is preserved byte for byte in last position, with its three lines (SPEC-1, TEST-1, TEST-2) in their existing order. The reconciliation pass owns it and this pass did not touch it.
EVIDENCE: 0075_....summary.md:207-225

OPEN: OD2 still carries its question, its ground, one losing alternative, and the cost of answering no, but no explicit recommendation and no confidence, which is less than `## Open decisions for human to make` asks of an entry. This is the fourth firing to observe it and leave it: supplying a recommendation is adjudication, which a format pass may not do. The standing-context `## Open` line joins on [f1.cleanup, f2.cleanup, f3.cleanup] and should now read [f1.cleanup, f2.cleanup, f3.cleanup, f4.cleanup].

WATCHOUT: this review log carries `## Standing context`, `## Ledger`, `## Resolved in adversarial review`, and `## 11. What the 2026-09-06 rewrites changed`, and has no `## Retired` section at all. This block was therefore spliced at the end of `## Ledger`, immediately before `## Resolved in adversarial review`. A later pass that looks for `## Retired` to find the end of the ledger will not find one, and appending to the end of the file would bury an entry inside the `## 11` appendix that `status.md:22` already points at across files.
EVIDENCE: 0075_....review-log.md — `## Standing context`, `## Ledger`, `## Resolved in adversarial review`, `## 11. What the 2026-09-06 rewrites changed`

## Resolved in adversarial review

### Pass 1 (2026-09-07, automated)

- **D1's staged §4.1 block classified `CheckpointRequest` both pod-scoped and session-scoped.** The block's
  first paragraph made a request message session-scoped exactly when it declares a top-level `session_id`
  field and pod-scoped when it declares none, which reaches `CheckpointRequest`: it is the request type of
  `rpc Checkpoint` (`schemas/lenny-adapter.proto:133`) and its only top-level field outside the `oneof` is
  `int64 coordination_generation = 4` (`:1186`), so the field-set predicate classified it pod-scoped while
  the envelope paragraph classified it session-scoped through `CheckpointStart`, which declares
  `SessionId session_id = 7` (`:1217`). The two classes are stated as exclusive, and D2's replacement gate
  enforces the premise that produces the conflict by refusing an envelope that declares an address of its
  own, so nothing in the block resolved the reading. Scoped the field-set predicate to a request message
  that carries no `oneof` of frames, in both directions, and added a sentence sending a message that does
  carry one to the envelope paragraph. Verified `spec/04_system-components.md:167` already classifies the
  envelope for the scope of its `CheckpointStart` and that `pkg/adapter/checkpoint.go:74-84` cites §4.1's
  session scope as the ground for refusing an empty address with `InvalidArgument`, which the unqualified
  predicate would have falsified.
- **D1's reproduction claim was false by the two envelope rows.** The claim that the request messages
  carrying the address are exactly the retired table's session rows and the ones carrying none are exactly
  its pod rows conflated the envelope with both groups, then named it as a third. Counted against the tree:
  the table carries 26 session rows (`spec/04_system-components.md:155-174`, `:180-185`) and 6 pod rows
  (`:175-179`, `:186`), which the fence's move makes 27 and 5; a parse of the two service blocks returns 31
  declared request messages, 25 declaring a top-level `SessionId session_id` and 6 not, the 6 being
  `CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`,
  `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, and `ReportPodScrubRequest`. The
  address-carrying set is therefore the 27 session rows less `CheckpointRequest` and `CheckpointStart`, and
  the address-free set is the 5 pod rows plus `CheckpointRequest`. Restated the claim so the two envelope
  rows are excluded from the first two conjuncts and carried by the envelope clause, and replaced "No
  exception clause is needed" with what is true after the qualification: the field-set predicate decides
  every request message but the envelope, the envelope clause decides that one, and no per-message
  exception remains. Made the same one-word correction to the summary's D1 bullet so the two agree.
- **§3's design overview still stated TEST-2's retired deliverable.** The overview read "One tier-3 suite
  gains a message and loses a false comment", which is the deliverable TEST-2 carried before this pass split
  the tier-3 file's single declaration. Every other statement of it was rewritten
  (`...implementation-checklist.md:17-21`, `...non-spec-changes.md:49-80`, `...summary.md:32-35` and
  `:217-221`, and `...spec-changes.md:154-157`), so §3 was the one site left stating a version the finding
  established cannot be applied, and it named neither the split nor the one identifier the proposal now
  introduces. Restated §3 as the split into the rule's session set and a new `retiredDuplicateNumbers`
  table, the widening of the two address arms, and the comment deletion. Re-verified the widening against
  the tree: 26 messages declare a top-level `SessionId session_id`, the 18 that also declare
  `reserved "slot_id"` are exactly today's map members
  (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63`), and the remaining
  eight are `CoordinatorFenceRequest`, `ExportPathsRequest`, `ConfigureWorkspaceRequest`,
  `CallConnectorToolRequest`, `CallPlatformToolRequest`, `ListConnectorToolsRequest`,
  `ListPlatformToolsRequest`, and `ListSessionConnectorsRequest`, so the arms widen by eight messages rather
  than by one.
- **OD2's rejected alternative named a cost the files-touched list already carries.** The cost sentence said
  the alternative "draws `tests/spec-map.json` and the `addressRuleCases` inventory ... into a blast radius
  the files-touched list does not carry". `tests/spec-map.json` is in that list, at `...spec-changes.md:168-169`
  and again in the summary's own "What changes" bullet at `:30-31`. The map also credits the tier-3 file as a
  whole file rather than per case (`tests/spec-map.json:172`, and again at `:600`, `:1110`, `:1391`, and
  `:3678`, each a bare path beside per-case entries such as `:156`), which is the ground
  `...non-spec-changes.md:71-75` gives for TEST-2 registering nothing, and by that same credit an added
  function needs no entry either. Half of the stated cost therefore did not exist, on the comparison OD2 asks
  the reviewer to answer. Dropped the spec-map half, kept the inventory half, whose citation re-verified
  (`tests/tier0_static/address_rule_citation_test.go:45-47` names
  `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` under that file, and the gate at `:128-147`
  checks only the cases the inventory lists), and recorded that the whole-file credit makes the spec-map
  cost nil either way.
- **DEFERRED:** none. Every finding above was closed in the file that carries it, and none falsified
  anything in the problem statement or the staged non-spec changes. Re-verified while making the edits:
  `SessionId` at `schemas/lenny-adapter.proto:596`, `CoordinatorFenceRequest` at `:1455` with
  `SessionId session_id = 1` at `:1456`, `CheckpointRequest` at `:1173`, `CheckpointStart`'s address at
  `:1217`, and the §4.2 value rule at `spec/05_runtime-registry-and-pool-model.md:515`, all as this
  proposal cites them.

## 11. What the 2026-09-06 rewrites changed

Recorded so a reader who saw an earlier revision can tell what moved and why.

**The first rewrite** answered proposal 0076's assessment of this document, which is the only place 0076
states anything about another proposal's validity.

- **The central argument.** The earlier revision grounded its exception on the handler mutating a pod-wide
  `coordinationState`, so that "the identifier selects nothing". 0076's CODE-1 deletes that state. §1.2 now
  states the ground as the specification's rather than the tree's, and states that 0076 removes it.
- **The dependency was corrected.** §10 previously called the proposals independent and the collision a
  rebase.
- **The non-goal was corrected.** A bullet describing the pod-wide counter as 0076's subject described
  state that no longer exists after 0076.
- **Anchors were refreshed** against the tree: `SessionId` at `schemas/lenny-adapter.proto:589` rather than
  `:580`, `CoordinatorFenceRequest` at `:1447-1448` rather than `:1403-1404`, `CheckpointRequest` at
  `:1166` rather than `:1131`, and `s.coord` at `pkg/adapter/server.go:302` rather than `:304`. The §1.1
  message counts were re-derived from the current proto and are unchanged.

**The second rewrite** applied the reviewer's answer to 0076's OD3.

- **SCHEMA-1, CODE-1, and DOCS-1 were dropped.** They carried an exception that the OD3 answer removes. D4
  records why the guard wrapper is gone rather than deferred, since it was the earlier revision's central
  mechanism. With them go the proto, generated-code, handler, gateway-caller, and reader-facing-page edit
  sites, and tiers 1 and 3 for the rename.
- **The conditional framing was removed.** An intermediate revision made three deliverables conditional on
  OD3 and added a gate step to the checklist; both are gone now that the answer exists.
- **SPEC-1 grew the three §4.1 sites** at `:151`, `:175`, and `:188` that 0076's OD3 Question B assigns to
  a successor, because this proposal is that successor.
- **TEST-2 is new.** The false tier-3 coverage clause was a defect 0076 recorded and left conditional on
  OD3. Under the answer given it is an edit site of this proposal rather than a separate finding, and §4
  records the one point it must settle.
- **§7 lost two questions and gained one.** The guard type's name and the wrapper-versus-bare-string choice
  went with D4. The retired-field-number column arrived with TEST-2.
