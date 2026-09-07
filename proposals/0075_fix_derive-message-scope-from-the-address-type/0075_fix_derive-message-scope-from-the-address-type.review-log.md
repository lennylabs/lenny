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



### [spec.1.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens over the staged spec edits — BECAUSE the whole execution model checks out: S1(spec) leads, S2/S3(test) follow, each carries one lane, every staged deliverable maps to exactly one step, no Depends-on names a later or missing step, no box is ticked, and no edit references an artifact a later sub-step creates. SPEC-1's anchors (`spec/04_system-components.md:151`, `:153-186`, `:188`, `:190`) all resolve uniquely against the current post-0076 tree, and the replacement text is quoted verbatim in D1 so nothing is left to invent. ALTERNATIVES: considered filing the replacement gate's unstated file path and case names (a real class-2 underspecification) and the lost Service/Direction columns (a class-3 content question); both were rejected — the first because its remedy lands in `non-spec-changes.md`, outside this loop's scope and already recorded four times as an OPEN, the second because §4.7.1's two RPC tables (`spec/04_system-components.md:698-720`, `:723-726`) carry direction and service for every RPC, so nothing unique is lost.

CORRECTS [Standing context, Settled, "Proto anchors, current and verified"]: the entry says `ReportSessionScrubRequest` sits at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong in the reader's favour and should be dropped rather than carried: `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact. A future round that "corrects" SPEC-1 to `:457` on the strength of this entry would introduce a false citation.

FACT: after SPEC-1 lands, the string `CheckpointStart` disappears from `spec/` entirely. Today it occurs only at `spec/04_system-components.md:151`, `:167`, and `:168`, all inside the block SPEC-1 replaces, and the staged paragraph 2 classifies the addressing frame structurally without naming it. Nothing depends on the name being in `spec/`: no tier-11 gate reads it, `docs/` never mentions it, and the only tier-0 reader is the retiring gate's own hand-injected constant (`tests/tier0_static/adapter_proto_message_scope_test.go:36`, fixture at `:171`), which TEST-1 deletes. EVIDENCE: `grep -rn CheckpointStart spec/ docs/` returns those three spec lines and nothing under `docs/`.

FACT: re-ran the whole proto census independently with a brace-counting parser (not the naive one the traps warn about) and it reproduces the settled numbers exactly: 31 RPC request types, 25 declaring a top-level `SessionId session_id`, 6 not (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`); the only `oneof` blocks are `CheckpointRequest` and `CheckpointResponse`; and the name/type biconditional has zero violations anywhere in the file, comments included. D1's reproduction claim arithmetic also checks: the retired table's 24 session request-type rows plus the fence is 25, and its 5 remaining pod rows are the 5 non-envelope messages that carry no address. EVIDENCE: `schemas/lenny-adapter.proto`; table at `spec/04_system-components.md:153-186`.

FACT: the §4.1 `#### Request Message Scope` subsection has no inbound reference anywhere in the tree — no `#request-message-scope` link, no `specSection(..., "### 4.1 ")` reader, and no entry in `reducedSections` (`tests/tier11_docs/successor_pointer_test.go:51-55`, which lists §4.7, §15.4, §29.10 only). SPEC-1 keeps the heading, so no anchor redirect and no successor pointer is owed. Verified rather than assumed; this is the check that would have turned a clean retirement into a broken one.

WATCHOUT: the tier-11 gates that read `spec/04_system-components.md` all target other sections — `### 4.4 `, `#### 4.6.1 `, `#### 4.6.3 `, `### 4.7 ` — so S1's "Tiers 0, 11" is a precaution rather than a reached surface. Do not read a green tier 11 after S1 as evidence that the §4.1 edit was checked by anything; the only gate that reads the deleted block is the one S1 deliberately reddens. EVIDENCE: `tests/tier11_docs/eviction_coordinator_route_consistency_test.go:64`, `:89`; `tests/tier11_docs/spec_28_register_writers_test.go:759`; `tests/tier11_docs/external_side_effect_recovery_test.go:136`.



### [spec.1.review-citations.1]

DECISION: returned an empty findings list for the citation lens over `spec-changes.md` — BECAUSE every concrete citation in D1-D6, §3, §4, SPEC-1, §6, §7, §9, and §10 was opened and read at the cited location and each says what the proposal claims — ALTERNATIVES: filing the loose quantifier in D1's "the ones that carry none are exactly its pod rows" (the envelope also carries no top-level address and is a session row); rejected because the sentence's own carve-out "other than the stream envelope" governs the pair and the reading is unambiguous in context, so it is wording rather than a false citation.

FACT: every proto anchor the proposal cites is exact on HEAD. `message CoordinatorFenceRequest` `schemas/lenny-adapter.proto:1455`, `SessionId session_id = 1` `:1456`, `message SessionId` `:596`, `message CheckpointRequest` `:1173` with its `oneof msg` at `:1174`, `message CheckpointStart` `:1193` with `SessionId session_id = 7` at `:1217`, `message ReportSessionScrubRequest` `:456` with `SessionId session_id = 2` at `:458`, `message ReportPodScrubRequest` `:499-503`, `message CheckpointResponse` `:1249`. — EVIDENCE: schemas/lenny-adapter.proto:1455

CORRECTS [standing context / Proto anchors, current and verified]: the entry says `ReportSessionScrubRequest`'s address is at `:457` and that "SPEC-1 cites `:458`, one line off, below the bar". That is backwards. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact and the standing-context anchor is the wrong one. Same entry gives `CheckpointResponse` at `:1250`; the `message` line is `:1249` and `:1250` is its `oneof msg {`. — EVIDENCE: schemas/lenny-adapter.proto:456-458

CORRECTS [orchestrator note "EDIT SITES, ALL RE-VERIFIED TODAY" / "THE PROTO MEASUREMENT HOLDS"]: the proto anchors in the launching note are stale by seven to eight lines — it gives `SessionId` at `:589`, `CoordinatorFenceRequest` at `:1447` with its field at `:1448`, and `CheckpointRequest` at `:1166`. None of those is where the message sits, at HEAD (`3224c6271`) or at the commit the note itself names (`c5d35bb05`, checked with `git show c5d35bb05:schemas/lenny-adapter.proto`). Do not re-anchor the proposal onto them; the proposal's own numbers are the correct ones. — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: the derivation arithmetic reproduces exactly. A brace-counting parse of the two service blocks gives 31 RPC request types, 25 declaring a top-level `SessionId session_id`. Diffing the derived session set against the §4.1 table's 26 session rows leaves `CheckpointRequest` and `CheckpointStart` on the table side (both carried by the envelope clause) and `CoordinatorFenceRequest` on the derived side. Nothing else moves in either direction, so D1's "reproduces every classification the retired table carried" is true rather than approximately true. — EVIDENCE: spec/04_system-components.md:155-186

FACT: the addressing convention the replacement gate checks is green on the shipped proto in both directions and beyond the request types. `grep -n "SessionId" schemas/lenny-adapter.proto | grep -v session_id` returns only the type's own comment and declaration at `:595-596`, and every `session_id` declaration in the file is `SessionId`-typed. Only two `oneof` blocks exist, at `:1174` and `:1250`. — EVIDENCE: schemas/lenny-adapter.proto:595

FACT: no inbound spec, docs, schema, or chart surface depends on the retired table. Grepping `spec/ docs/ schemas/ charts/ tests/ pkg/ cmd/ sdks/` for the heading text, `#request-message-scope`, "classification table", and "declared rather than derived" finds the §4.1 block itself, the retiring gate, and the shared parse's own comment, and nothing else. `pod-scoped` survives in `spec/` only at `spec/04:726`, `spec/04:872`, `spec/10:60`, and `spec/12:202`, none of which classifies a request message except `:726`, so SPEC-1's "only sentence in `spec/`" claim is exact. — EVIDENCE: spec/04_system-components.md:726

FACT: `spec/` uses no test-tier vocabulary outside `spec/18_build-sequence.md`'s exit criteria, so SPEC-1's third replacement paragraph ("A tier-0 gate refuses a protocol definition in which...") introduces the first such sentence into a component section. This is a deliberate choice the proposal states in §5, and it is not a citation defect; recorded only so a later reviewer does not spend a round rediscovering it and does not mistake it for drift. — EVIDENCE: spec/18_build-sequence.md:85

USEFUL [standing context / Traps / "Long lines hide the sentences citations point at"]: `spec/10:60`, `spec/05:515`, and `spec/04:725`/`:726` each hold the cited clause as the last sentence of one very long line. Piping the line through `tr '.' '\n' | cat -n` recovers it in one step and turned four citations that looked unverifiable into four verified ones.


### [spec.1.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens — BECAUSE every externally-consumed representation the staged spec edits touch or constrain was re-derived from the tree and found consistent: no proto change, no SDK/OpenAPI/CRD/charts reference to the affected messages, no `docs/` page that classifies a gRPC request message by scope, and no spec cross-reference into the retiring `#### Request Message Scope` block — ALTERNATIVES: filing the `docs/api/internal.md` staleness (pre-existing, remedy lands in `docs/`, already deferred by an earlier round) and filing the §4.7 RPC-table incompleteness (pre-existing, retirement makes nothing wrong).

FACT: the name/type biconditional the staged constraint paragraph asserts holds exactly on the shipped adapter proto, verified in both directions with anchored regexes: no `SessionId <name>` field is named anything but `session_id`, and no field named `session_id` carries any other type. 26 field declarations plus `message SessionId` plus one comment mention = 28 `SessionId` lines total — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1217 (`CheckpointStart.session_id = 7`), :1456 (`CoordinatorFenceRequest.session_id = 1`), :458 (`ReportSessionScrubRequest.session_id = 2`)

FACT: `schemas/lenny-adapter.proto` is self-contained for this rule — its only import is `google/protobuf/timestamp.proto` (`:18`) and it declares exactly two services, `Adapter` (`:32`) and `GatewayControl` (`:261`), with 31 `rpc` lines and 31 distinct request types. So the staged "either service declares" quantifier and a gate scoped to that one file cover the same population — EVIDENCE: schemas/lenny-adapter.proto:18, :32, :261

FACT: no client-facing artifact outside `spec/` classifies a gateway-adapter request message by scope. `grep -rn "session-scoped\|pod-scoped\|CoordinatorFence" docs/` returns only JSONL-frame and upload-token uses plus one scope-free `CoordinatorFence` RPC row; `grep -rn "CoordinatorFence\|CheckpointStart\|SessionId session_id" sdks/ charts/ pkg/embedded/ pkg/gateway/openapi/` returns nothing; the proto itself carries no `pod-scoped`/`session-scoped` doc comment. §9's files-touched list is complete on the client-surface axis — EVIDENCE: docs/reference/adapter-contract.md:69, :81

FACT: SPEC-1's claim that `:726` is left as the only sentence in `spec/` classifying a request message pod-scoped survives a full sweep. The other surviving `pod-scoped` sites are `spec/10_gateway-internals.md:60` (the hold and its gauge), `spec/04_system-components.md:872` (the rotation in-flight ceiling), and `spec/12_storage-architecture.md:202` (Redis key prefixes); none names a request message — EVIDENCE: spec/04_system-components.md:726, spec/10_gateway-internals.md:60

CORRECTS [standing context, "Proto anchors, current and verified"]: the parenthetical "SPEC-1 cites `:458`, one line off" is wrong. `ReportSessionScrubRequest` opens at `schemas/lenny-adapter.proto:456`, `string pod_id = 1` is `:457`, and `SessionId session_id = 2` is `:458`. SPEC-1's citation is exact. Do not "fix" it.

USEFUL [standing context, Traps]: the recorded declines for the "only way a request addresses a session" tension, the nested-address hole, `AdapterEventsRequest`, and the §4.7.1 "declared per message" tension each saved a full derivation. All four are the first things a client-surface lens reaches for and all four are correctly closed.

DEFERRED [docs/api/internal.md]: `:273` publishes `message DemoteSDKRequest { string session_id = 1; }` as the adapter-author contract, while the shipped `DemoteSDKRequest` declares no address at all and is one of the five messages the derivation rule classifies pod-scoped. This is a second instance of the already-recorded `CheckpointRequest` deferral at `:209-215`: the page is false about the proto today, so SPEC-1 does not make it wrong, but after SPEC-1 the excerpt asserts a classification opposite to the one the rule computes and spells the address in a form the new tier-0 gate refuses. Same owner as the existing deferral (the docs loop, or 0080's residue inventory). Also stale on the same page: `StopSession` and `UploadFiles` RPCs that no service declares.


### [spec.1.review-docs-alignment.1]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE every docs-side consequence I could derive is either absent (no docs/ page mirrors the §4.1 request-message-scope classification) or already recorded as DEFERRED in the Standing context with a docs-side remedy this spec-only loop may not land — ALTERNATIVES: filing `docs/api/internal.md:209-215` (its `CheckpointRequest` excerpt declares `string session_id = 1`, which SPEC-1's biconditional turns from merely-stale into an illustration of a construction the new tier-0 gate refuses) and `docs/reference/adapter-contract.md:69` ("precondition for any subsequent operational RPC", pod-wide reading falsified by 0076); both rejected because the only remedy is a docs edit, which this loop's scope excludes, and both already sit in the Standing context's Deferred block.

USEFUL [standing context "No docs mirror"]: saved the whole sweep. I re-derived it independently anyway (grep `session-scoped|pod-scoped` over docs/ returns only §28.5.3 JSONL statements, the upload-token glossary/guide rows, `docs/reference/metrics.md:180`, `docs/reference/adapter-contract.md:81`, and an unrelated `gateway-pod-scoped` in a runbook) and it holds exactly.

FACT: §4.1 is `### 4.1 Edge Gateway Replicas` (`spec/04_system-components.md:40`, running to `:191`); `#### Request Message Scope` at `:149` is a `####` sub-block inside it. SPEC-1 retires the sub-block, not the section, so the five `// spec: 4.1 (request message scope)` annotations in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` (`:26`, `:132`, `:408`, `:582`, `:747`) and their spec-map credits stay valid. That file reads no spec/04 text at all; its subject is the §28.5.3 JSONL echo obligation. A future docs lens should not re-chase it.
EVIDENCE: spec/04_system-components.md:40,149,192; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

FACT: the one tier-11 gate that pins `docs/reference/adapter-contract.md:81` opens `spec/04` at the literal `"### 4.7 "` and matches the row `| `ReportSessionScrub` |`. It cannot see the §4.1 block SPEC-1 deletes, so SPEC-1's decision to leave `spec/04:725`/`:726` unedited keeps tier 11 green for a mechanical reason rather than an incidental one.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:45-47,60

FACT: `docs/reference/error-catalog.md` contains no `InvalidArgument` entry at all, so the widening of the session-scoped class by one message (and with it the `spec/05_runtime-registry-and-pool-model.md:515` empty-identifier refusal) owes no error-catalog row. Checked because the reclassification is the only behavioral consequence the proposal carries.
EVIDENCE: docs/reference/error-catalog.md

FACT: `sdks/` mentions "session-scoped" ten times and every one cites §28.5.3 or §7.1, never the §4.1 gRPC classification. §1.4's "No file under `sdks/` references it" holds for the reason it gives (runtime authors speak the JSONL leg).
EVIDENCE: sdks/runtime/go/runtime/runtime.go:461; sdks/runtime/python/lenny_runtime/tool.py:146



### [spec.1.review-edit-sites.1]

DECISION: Returned an EMPTY findings list under the edit-site-completeness lens — BECAUSE every identifier SPEC-1 touches (`session_id`, `SessionId`, the two class words, the two service names, the `#### Request Message Scope` heading) was grepped across `spec/`, `docs/`, `schemas/`, `charts/`, `tests/registers/`, and `spec/README.md`, and no surface outside `spec/04` §4.1 becomes wrong or inconsistent once the block is replaced — ALTERNATIVES: filing the "either service" antecedent loss (rejected, see below); filing `docs/api/internal.md:209-215` (rejected, already DEFERRED with reasoning and not made newly wrong by SPEC-1); filing D2's "an addressing convention that nothing checks today" (rejected, already DEFERRED at review-log.md:129 with the exact correction, and it is rationale rather than staged spec text).

FACT: `GatewayControl` occurs NINE times in `spec/04_system-components.md`: the seven table rows SPEC-1 deletes (`:180-186`), plus `:550` ("the adapter has no GatewayControl path to") and `:719` ("on the GatewayControl link via"). It is also named as a service at `spec/28_communication-channels.md:1782` ("the `GatewayControl` service over which the `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` tool calls are forwarded"). So the earlier archive FACT that the table rows are the ONLY naming of the services is too strong — EVIDENCE: spec/04_system-components.md:550, :719; spec/28_communication-channels.md:1782

CORRECTS [spec.2.review-fresh.1]: its FACT "`Adapter` and `GatewayControl` are named inside §4.1 ONLY by the table rows SPEC-1 deletes ... So after SPEC-1 applies, the staged rule's phrase 'A request message either service declares' has no antecedent anywhere in §4.1" is true as stated about §4.1 but reads as an unfiled finding. It is not one: `spec/28_communication-channels.md:1782` names the `GatewayControl` service and the protocol artifact, the staged sentence already names "the gateway-adapter protocol", and `schemas/lenny-adapter.proto` declares exactly two services, so the rule's population stays determinable. I examined it in full this round and declined it. Do not re-derive.

CORRECTS [review-log.md standing context, "Proto anchors, current and verified"]: it says `ReportSessionScrubRequest` sits at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". The address is at `:458` and SPEC-1's citation is EXACT. `:456` is `message ReportSessionScrubRequest {`, `:457` is `  string pod_id = 1;`, `:458` is `  SessionId session_id = 2;`. The [spec-recheck.1.review-citations.1] ledger entry already had this right — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: `spec/` contains no lowercase "tier-0"/"tier 0" anywhere, but `spec/18_build-sequence.md:85` and `:98` DO carry "Tier 0 (Static) and Tier 11 (Documentation)". A case-sensitive grep for "tier-0" therefore returns nothing and makes the staged constraint paragraph's "A tier-0 gate" look unprecedented in `spec/`. It is not; standing-context Open item on the first-mention-of-a-tier question is answered by those two lines — EVIDENCE: spec/18_build-sequence.md:85, :98

FACT: the retired-channel-spelling register (`tests/registers/identifier-senses.yaml`) is ordinal-keyed per file, so a deletion inside `spec/04` could renumber its eight `spec/04` rows. It cannot here: the retired spellings are `LifecycleChannel`, `controlchannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel` (`spec/28_communication-channels.md:150-159`), and a case-insensitive `lifecycle`/`controlchannel` scan of `spec/04` returns hits at `:33`, `:200`, `:284` and beyond, none inside `:151-188`. Verified this round rather than taken on trust — EVIDENCE: spec/28_communication-channels.md:150-159; tests/registers/identifier-senses.yaml:14-37

FACT: no tier-11 or tier-10 gate reads §4.1. `tests/tier11_docs/spec_47_rpc_row_naming_test.go:46` extracts `"### 4.7 "` and nothing extracts `"#### Request Message Scope"` or a literal from `:151`/`:188`. A grep of `tests/`, `scripts/`, `pkg/`, `cmd/` for the deleted sentences' literals returns nothing. §9's spec-side file list is complete — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:46

FACT: `pkg/adapter/checkpoint.go:74` carries `// spec: §4.1 — Checkpoint is session-scoped` and `tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99` carries `// spec: §4.1 (message scope)`. Both survive the retirement because the replacement block still classifies the envelope, so neither is an edit site — EVIDENCE: pkg/adapter/checkpoint.go:74; tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99

WATCHOUT: `grep -rn "GatewayControl" spec/` reports `spec/04:550` and `:719` as hits, and an earlier round read them as table-adjacent. They are not: `:550` is a §4.6 sentence about a claim's binding state and `:719` is the §4.7.1 `Shutdown` row. Read the matched substring, not the line, before concluding where a service name lives — EVIDENCE: spec/04_system-components.md:550, :719


### [spec.1.review-feasibility.1]

DECISION: returned zero findings — BECAUSE the staged block assigns an action to exactly one actor, "a tier-0 gate", which exists, reads `schemas/lenny-adapter.proto` through the parse at `tests/tier0_static/adapter_proto_parse_test.go:36,68`, and whose four stated refusals are each satisfiable and green on the shipped proto. The block touches no controller, no §4.6.3-owned subresource, no RBAC, no NetworkPolicy egress, no agent-pod posture, no webhook, and no build-phase artifact — ALTERNATIVES: filing the "spec/ names a test tier for the first time" item as a feasibility defect (rejected: tier 0 exists and can do the check, so it is a convention question, not an actor-action one); filing D2's "nothing checks today" (rejected, see the note below).

FACT: independent re-derivation of the whole classification, by a comment-stripping brace-depth parse of both service blocks, agrees with the census exactly: 31 distinct RPC request types; 25 declare a top-level `SessionId session_id`; 5 declare no address and carry no `oneof` (`AdapterEventsRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); 1 carries the `oneof` (`CheckpointRequest`). Set against the retired table row by row, `CoordinatorFenceRequest` is the ONLY message whose class changes. Nothing is silently reclassified, so no handler acquires a new `spec/05:515` refusal obligation beyond the fence's, which `pkg/adapter/coordination.go:109-111` already discharges. — EVIDENCE: schemas/lenny-adapter.proto:1173,1217,1455; spec/04_system-components.md:155-186

FACT: the replacement gate is green on the shipped proto in all four of its stated arms, checked directly. Name→type and type→name: 26 `session_id` field declarations in the whole file, every one spelled `SessionId session_id`, and zero fields of type `SessionId` under another name. Envelope declares no address of its own: `CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4`. Exactly one addressing frame: of `CheckpointStart`/`CheckpointGrant`/`CheckpointAbort`, only `CheckpointStart` declares `SessionId session_id = 7`. — EVIDENCE: schemas/lenny-adapter.proto:1174-1187, 1217

FACT: the two tier-0 files 0076 landed are fully decoupled from the retiring gate. `grep -n 'parseMessageScopeTable|messageScopeSpecPath|messageScopeRow|protoServiceRequests|protoFields|04_system-components'` over `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go` returns nothing, and no file outside the gate itself names `parseMessageScopeTable`, `messageScopeRow`, or `messageScopeSpecPath`. §9's omission of both is correct. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:29-31

CORRECTS [standing context, Settled, "Proto anchors"]: the entry says `ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites `:458`, one line off". SPEC-1 is right and the log entry is wrong: `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`. No citation defect exists there; do not "fix" SPEC-1's `:458`. — EVIDENCE: schemas/lenny-adapter.proto:456-458

CORRECTS [standing context, Open, "`spec/` gains its first mention of a test tier ... `spec/28:64` and `spec/18:85`, `:98` are the precedents"]: there are no precedents. `grep -rni tier spec/*.md` returns only scale tiers ("Tier 3"), `workspaceTier`, and `multi-tier`; no spec file names a TEST tier in any spelling. `spec/28_communication-channels.md:64` reads "line-citation ratchet are the gates that hold this rule", which names gates and no tier, and the phased-build file is `spec/18_build-sequence.md` (there is no `18_phased-build-sequence.md`) and names no tier either. The staged constraint paragraph's "A tier-0 gate" would be the first test-tier name in `spec/`. I still judge it below the bar (the actor exists and can perform the check), but the open item's stated ground is gone, so whoever settles it must settle it on convention rather than on precedent.

WATCHOUT: `grep -o ".\{0,90\}word.\{0,90\}"` fails on this repo's long spec lines with "ugrep: exceeds complexity limits". Use a three-line python slice instead. Costs a minute each time. — EVIDENCE: spec/04_system-components.md:872

FACT: SPEC-1's survivor claims all hold verbatim. `spec/04:725` ends "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` ends "The request is pod-scoped."; the tier-11 gate at `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-75` pins that exact opener on both the spec row and `docs/reference/adapter-contract.md:81`. `spec/10_gateway-internals.md:60`'s last sentence is "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped." After SPEC-1, `spec/04:726` is indeed the last sentence in `spec/` classifying a request message pod-scoped: the other surviving `pod-scoped` sites (`spec/04:872` rotation ceiling, `spec/12:202` Redis key prefixes, `spec/10:60` hold gauge, `spec/13:668` advisory lock) classify no request message.

OPEN: the standing context's Deferred entry "D2 calls the addressing convention 'an addressing convention that nothing checks today', which overstates it" names `spec-changes.md` as its remedy file, which IS this loop's editable file, so it is landable now rather than deferred. Verified again: `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`) already asserts `session_id` exists and is `lenny.adapter.v1.SessionId` for each of the 18 hand-listed members. I did not file it: it is a rationale sentence in D2, the staged spec text carries no such claim, and applying the edits leaves `spec/` correct either way. The fixer or the between-loops pass should still land the tightening ("no gate checks the convention in both directions over every message the protocol declares") so the entry stops recurring.


### [spec.1.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every citation in `spec-changes.md` re-verified true against the tree today, the derivation rule reproduces the retired table row for row, and the replacement gate is green on the shipped proto — ALTERNATIVES: filing the D2 "nothing checks today" overstatement (already recorded as a DEFERRED in the review log) was considered and dropped, because D2 is decision prose rather than staged spec text and the overstatement does not make the applied spec wrong.

CORRECTS [Standing context → Settled, review-log.md:20]: the entry says "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong in the proposal's favour. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`, so SPEC-1's `:458` is exact and nothing is off by one. EVIDENCE: schemas/lenny-adapter.proto:456-458.

FACT: every proto anchor in `spec-changes.md`, `problem-statement.md`, and `non-spec-changes.md` is current as of 2026-09-07 at commit c5d35bb05: `message SessionId` :596, `CheckpointRequest` :1173, `CheckpointStart` :1193, `SessionId session_id = 7` :1217, `CoordinatorFenceRequest` :1455 with its address at :1456, `ReportSessionScrubRequest` :456/:458, `ReportPodScrubRequest` :499-503. The orchestrator brief's anchors (:589, :1166, :1447, :1448) are stale by seven lines. EVIDENCE: schemas/lenny-adapter.proto:596,1173,1193,1217,1455.

FACT: the addressing convention the replacement gate checks holds absolutely on the shipped proto in both directions. `grep -n "SessionId " schemas/lenny-adapter.proto | grep -v session_id` returns only the type's own declaration and its doc comment, and `grep -n "session_id *=" | grep -v SessionId` returns nothing. EVIDENCE: schemas/lenny-adapter.proto:595-596.

FACT: `spec/04_system-components.md` §4.1 spans :40-:191 ("### 4.1 Edge Gateway Replicas" at :40, "### 4.2 Session Manager" at :192), and §4.7.1 spans :692-:727, so SPEC-1's placement of the retired block in §4.1 and of `:725`/`:726` in §4.7.1 is right. EVIDENCE: spec/04_system-components.md:40,192,692,728.

FACT: `spec-map.json` is the only register naming the retiring gate. `grep` across `tests/*.json`, `tests/registers/`, `tests/change-graph.json`, and `gate_integrity_test.go` for `adapter_proto_message_scope_test` / `adapter_proto_parse_test` returns only `tests/spec-map.json:156`, `:169`, `:5670`. §9's six-file list is complete under this check. EVIDENCE: tests/spec-map.json:156,169,5670.

WATCHOUT: `parseMessageScopeTable`'s only reader is its own file and `protoServiceRequests` has exactly one caller; `protoFields`'s only other caller is `claim_register_proto_agreement_test.go:64`, which calls `protoFields` (not `protoServiceRequests`). A reviewer checking §9's "the parse's other caller (`:64`)" against the wrong function will read it as a false citation. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:64.

USEFUL [Standing context → Traps]: the thirty-item Traps block saved a full round. Items 73 (the "only way a request addresses a session" tension), 74 (the nested-address hole), 75 (§4.7.1 declared-versus-derived), 76 (a session-scoped fence exiting a pod-wide hold), 79 (the indefinite "a protocol definition"), and 96/97 (the two envelope-clause simplifications) each name a line of inquiry I independently reached and then dropped on the recorded ground.

OPEN: paragraph 2 of the staged block asserts that the frame declaring the address "opens the stream", and the D2 gate does not check it — it checks only that exactly one frame declares an address. The assertion is true of the shipped proto and is enforced in code (`pkg/adapter/checkpoint.go:60-64` refuses a stream whose first frame is not a `CheckpointStart`), and the classification does not depend on it, so I judged it below the bar. Recorded so a later round does not spend an hour re-deriving it.


### [spec.1.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the staged spec block is a classification rule over
request messages on the gateway-adapter gRPC protocol and touches no CRD, status subresource, finalizer,
admission webhook, controller watch, work queue, or leader-elected reconcile. There is no Kubernetes-idiom
surface in `spec/04_system-components.md:149-190` for the lens to bite on. ALTERNATIVES: I checked whether
the reclassification of `CoordinatorFenceRequest` to session-scoped pushes any write onto a CRD status or
onto the agent pod's (zero-RBAC) apiserver path; it does not — the fence is a gateway→adapter RPC whose
recorded state lives on the adapter's in-process slot entry (`pkg/adapter/slot.go:59`), never in etcd.

FACT: the whole `#### Request Message Scope` block has no inbound reference anywhere in the tree.
`grep -rn "Request Message Scope\|request-message-scope" spec/ docs/ tests/ pkg/ schemas/ charts/` returns
exactly one hit, the heading itself. — EVIDENCE: spec/04_system-components.md:149

FACT: SPEC-1's two `spec/10` citations verify. `:57` is the "Hold state semantics" bullet and does say a
`CoordinatorFence` is "the only way to exit hold state"; `:60` is the "Observability" bullet and its LAST
sentence is "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped". Both are
single very long lines, so read them with `awk NR==60 | tr '.' '\n' | tail`, per the standing-context trap.
— EVIDENCE: spec/10_gateway-internals.md:57, spec/10_gateway-internals.md:60

FACT: the §4.1 anchors SPEC-1 enumerates are all current on the tree today: `:149` heading, `:151`
introducing paragraph ("because `session_id` appears on messages of both classes"), `:175` the fence row
declaring `pod`, `:188` the grounding sentence, `:190` the `ShutdownRequest` paragraph that D3 rests on.
— EVIDENCE: spec/04_system-components.md:149-190

USEFUL [standing context / Traps]: the "orchestrator briefs carry stale content" entry saved a wasted
verification pass — the brief's proto anchors (`:589`, `:1447`, `:1448`) are each seven lines off from the
proposal's own, and the brief's claim that §1.2 still asserts the pod-wide ground holds is false of the
current revision.

UNVERIFIED: nothing new. This lens added no claim anyone needs to re-check.


### [spec.1.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every flow the staged spec block describes traces end to end with no unreachable trigger, no bypassed gate, no granularity mismatch, and no predicate drift: paragraph 1's field-set predicate over the 31 RPC request types, paragraph 2's envelope clause over the one `oneof`-carrying request message, and paragraph 3's four gate clauses agree with D2 (`spec-changes.md:38-42`), with §8's negative-case list (`non-spec-changes.md:87-89`), and with the summary's gate bullet (`summary.md:21-24`) — ALTERNATIVES: I considered filing that paragraph 1/2's "oneof of frames" treats *any* `oneof` on a request message as a stream envelope, so a future non-streaming request that used a `oneof` for a variant payload would be forced into the envelope clause and would have to declare exactly one addressing arm; declined as hypothetical hardening, since only `CheckpointRequest` and `CheckpointResponse` declare a `oneof` and the latter is the request type of no RPC.

FACT: re-verified the whole derivation arithmetic independently this round. The §4.1 table is 32 rows (`spec/04_system-components.md:153-186`): 26 session, 6 pod. 26 messages declare `SessionId session_id`, one of which (`CheckpointStart`, `schemas/lenny-adapter.proto:1193`) is a `oneof` arm rather than a request type, leaving 25 request types. Those 25 are the table's 24 non-envelope session request-type rows plus `CoordinatorFenceRequest`. So the derived class reproduces the table exactly once the fence moves, and no row is lost. — EVIDENCE: spec/04_system-components.md:153-186; schemas/lenny-adapter.proto:341,364,383,403,419,456,682,706,846,900,964,989,1021,1035,1063,1087,1117,1193,1304,1333,1455,1486,1538,1576,1609,1673

FACT: the name/type biconditional the replacement gate enforces is green file-wide, checked in both directions with a regex over field declarations rather than by trusting the census: 26 fields named `session_id`, all of type `SessionId`; 26 fields of type `SessionId`, all named `session_id`; zero violations either way, `oneof` arms included. Only two `oneof` blocks exist, at `schemas/lenny-adapter.proto:1174` (`CheckpointRequest`) and `:1250` (`CheckpointResponse`), and only two services are declared, at `:32` and `:261`, so the staged block's "either service" is exact. — EVIDENCE: schemas/lenny-adapter.proto:1174,1250,32,261

FACT: nothing outside §4.1 states a gRPC request message's scope class in a way SPEC-1 falsifies. A full grep of `spec/` and `docs/` for `CoordinatorFence`/`CH-FENCE` returns no other scope classification, and the only inbound `§4.1` links in the tree point at the gateway-replica capacity and extraction material earlier in the section, never at `#### Request Message Scope`. `spec/04:725`/`:726` are the only surviving per-message scope sentences and both agree with the derivation. — EVIDENCE: spec/28_communication-channels.md:120,314-317,1814; spec/10_gateway-internals.md:38,40,57,60; spec/04_system-components.md:725,726

FACT: the two behavioral anchors SPEC-1 §4 leans on both hold in the shipped tree. `pkg/adapter/checkpoint.go:74-84` refuses an empty or unsafe opening-frame address with `InvalidArgument` before any root is resolved, and `pkg/adapter/coordination.go:108-110` does the same for the fence before `boundSlotState` resolves anything, so reclassifying the fence session-scoped adds an obligation the handler already meets. — EVIDENCE: pkg/adapter/checkpoint.go:74-84; pkg/adapter/coordination.go:107-116

USEFUL [standing context, Traps]: the trap list saved this round at least four filings that would each have cost two verifiers — the "only way a request addresses a session" tension against the envelope clause, the nested-address hole, the §4.7.1 declared-per-message tension, and the session-scoped fence exiting a pod-wide hold. Each is reachable within ten minutes of reading the staged block, and each is already refuted. Keep them.

USEFUL [non-spec.5.review-mechanism.1]: its §3-design-overview alternative (the overview omits the envelope's "declares no top-level address of its own" clause that all four normative carriers state) is the same one this lens reached independently. Recording it as an examined-and-declined item stopped a duplicate filing.

WATCHOUT: the orchestrator brief for this round still carries the pre-refresh proto anchors (`:589`, `:1166`, `:1447`, `:1448`), each seven lines off, and still says §1.2 claims the pod-wide ground holds and that "§7 question 1" is the existential question. The proposal's own anchors are the correct ones and the existential question is summary OD1. This is the third round the standing context has had to say so. — EVIDENCE: schemas/lenny-adapter.proto:596,1173,1455,1456


### [spec.1.review-operational.1]

DECISION: returned an empty findings list for the operational lens on the staged spec edits — BECAUSE the observability, alert, condition, and operator-doc surfaces are all independent of the §4.1 classification table, and every operational citation SPEC-1 makes verifies exactly — ALTERNATIVES: rejected filing the `docs/api/internal.md` excerpt (log Deferred entry, remedy is in `docs/`, out of this loop), the `docs/reference/adapter-contract.md:69` fence gloss (same), and the `spec/04:712` fence row (already falsified by 0076, does not *become* wrong under SPEC-1).

CORRECTS [review-log Settled bullet "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites `:458`, one line off, below the bar". SPEC-1 is right and the log is wrong: `:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. Do not "correct" SPEC-1's `:458` toward `:457`. EVIDENCE: schemas/lenny-adapter.proto:456-458.

FACT: no observability surface anywhere depends on the §4.1 request-message-scope classification. `grep -rn "scope" spec/16_observability.md docs/reference/metrics.md` returns no metric, label, or alert keyed on a gRPC request message's scope class; the only "session-scoped" metric, `lenny_adapter_unaddressed_frame_rejected_total`, is about JSONL frames on the Attach stream and is governed by spec/28 §28.5.x, not §4.1. EVIDENCE: spec/16_observability.md:189, docs/reference/metrics.md:180, spec/28_communication-channels.md:835-844.

FACT: SPEC-1's two operational citations into spec/10 both verify verbatim, including the attribution. `spec/10_gateway-internals.md:57` carries "which is the only way to exit hold state"; `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; `git log -S` on that sentence returns exactly `cf20a0646 Apply proposal 0076 SPEC-1 to spec/10`, and 0076's D5 is headed "The coordinator-loss hold stays pod-scoped." EVIDENCE: spec/10_gateway-internals.md:57, :60; proposals/0076_fix_scope-the-coordination-generation-to-the-session/...spec-changes.md:20.

FACT: SPEC-1's §4.7.1 survivor paragraph verifies in full. `spec/04_system-components.md:725` ends "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` ends "The request is pod-scoped."; the tier-11 gate is exactly `:43-76` of its file and pins that opener string on both `spec/04:725` and `docs/reference/adapter-contract.md:81`. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43,66-76.

FACT: SPEC-1's claim that `:726` is left as the only sentence in `spec/` classifying a request message pod-scoped holds. The other post-edit `pod-scoped` sites are `spec/10:60` (hold + gauge), `spec/04:872` (rotation in-flight ceiling), `spec/12:202` (Redis key prefixes), and `spec/13:668` (a *session-scoped* advisory lock, not a message); none classifies a request message. EVIDENCE: spec/04_system-components.md:872, spec/13_security-model.md:668.

FACT: nothing outside `proposals/` references the `#### Request Message Scope` heading or its anchor, and no root document (`TESTING.md`, `BUILD-GAPS.md`, `TEST-GAPS.md`, `README.md`) names the retiring gate or the table. The heading is the sole site. EVIDENCE: spec/04_system-components.md:149.

WATCHOUT: the cache key for this lens is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no lane component, so an "empty findings" result recorded for the operational lens can be a replayed cache hit rather than a review. This round's run found no cached entry at `operational-r1-09a51355eaa8.json` and did the work from files. EVIDENCE: 0075_...review-log.md:87.


### [spec.1.review-performance.1]

DECISION: returned an EMPTY findings list on the spec lane — BECAUSE the staged spec edit is a
classification rule over a protocol definition and carries no store, no emission site, no per-request or
per-session write, no watch, no leader-serialized key, and no reconcile pressure, so the top-tier
arithmetic my lens owes has a numerator of zero. ALTERNATIVES: filing on the coordinator-handoff path
(rejected, see FACT below) and on the tier-0/tier-3 test cost (rejected, both flat or down).

FACT: no runtime code dispatches on the §4.1 scope classification, re-derived today rather than replayed.
`grep -rn "session-scoped\|pod-scoped\|sessionScoped\|podScoped" pkg/ cmd/ --include=*.go` returns only doc
comments and unrelated identifiers (`pkg/gateway/sessionserver/finalize_plan_internal_test.go:48` is a test
helper, `pkg/admission/data_residency_validator/validator.go:89` is prose). Retiring the table therefore has
no runtime blast radius at any tier. EVIDENCE: pkg/adapter/oplock.go:77 (nearest hit, a known decoy)

FACT: nothing anywhere in `spec/`, `docs/`, `schemas/`, or `charts/` references the retiring table.
`grep -rn "Request Message Scope\|request-message-scope\|message scope"` over those four trees returns the
heading itself and nothing else, and SPEC-1 keeps the heading. EVIDENCE: spec/04_system-components.md:149

FACT: SPEC-1's three deletion anchors are exact on the tree today. `:151` is the introducing paragraph
ending "with `CheckpointStart` carrying its own row", `:175` is `| CoordinatorFenceRequest | Adapter |
gateway -> adapter | pod |`, `:186` is the last table row (`ReportPodScrubRequest`), `:188` is the grounding
paragraph, `:190` the `ShutdownRequest` paragraph, `### 4.2` at `:192`. EVIDENCE:
spec/04_system-components.md:175,188,192

USEFUL [non-spec.5.review-performance.1]: its set-theory sentence (the derived session class is a strict
superset of the retired table's, so the one behavioral obligation `spec/05:515` carries can only be added
and never removed, and the single message it is added to already performs it) is the whole failure-mode
answer for this lens. I re-checked the superset claim against the census and stopped there instead of
re-tracing the handoff control flow. It should be promoted if it is not already load-bearing in Settled.

USEFUL [standing-context Traps]: entry 471's warning that `CheckpointRequest` looks like a new
`spec/05:515` violation under the derived rule, and is not, because the retired table already classified it
session-scoped. That is exactly the false finding a reliability-minded lens reaches for on this proposal.

UNVERIFIED: unchanged from round 5 — nobody has run tier 0 or tier 3 on this branch. Every green-on-day-one
claim, including the replacement gate passing on the shipped proto, is derived from reading files.


### [spec.1.review-reliability.1]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE SPEC-1 is a
classification-only replacement of the §4.1 table, and the only class that moves is
`CoordinatorFenceRequest` pod→session, whose one behavioral consequence (the
`spec/05_runtime-registry-and-pool-model.md:515` refusal of an empty identifier) the handler already
meets at `pkg/adapter/coordination.go:109-111`. The derived session class is a strict superset of the
retired table's, so no message loses a refusal and no recovery path gains a fail-open direction.
ALTERNATIVES: filing the hold-state exit (a session-scoped fence as the only exit from a pod-wide hold),
which Traps entry at review-log.md:76 records as declined by four lenses and settled by 0076's OD3;
filing the equal-generation re-fence, which summary.md:176-197 already records as a shipped-tree defect
owned by 0080 §1.16.

FACT: the derived-vs-declared class delta is exactly one message, re-derived independently today with a
brace-counting proto parse: 31 RPC request types, 25 declaring a top-level `SessionId session_id`, and
the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`,
`GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. The table's 26
session rows are the 24 declaring request types other than the fence plus `CheckpointRequest` and
`CheckpointStart`, so session ⊂ derived-session and nothing moves session→pod. That superset property is
the whole reliability argument: a refusal can only be added. — EVIDENCE: spec/04_system-components.md:153-186,
schemas/lenny-adapter.proto:1455-1456

FACT: the staged envelope clause's "it opens the stream" is enforced in code, not merely asserted. The
handler refuses a `Checkpoint` stream whose first frame is not a `CheckpointStart` with `InvalidArgument`
before any root is resolved, so no continuation frame can arrive on an unaddressed stream after a
reconnect. — EVIDENCE: pkg/adapter/checkpoint.go:61-63, and the empty-identifier refusal at :83-85

FACT: SPEC-1's two `spec/10` anchors are correct today. `:57` is the "which is the only way to exit hold
state" clause and `:60` is the observability bullet whose last sentence is "The hold itself and the
`lenny_adapter_coordinator_hold` gauge remain pod-scoped". Both are single very long lines; a truncated
`sed`/`grep` shows neither clause and makes the citation look wrong. — EVIDENCE:
spec/10_gateway-internals.md:57, :60

FACT: nothing in `spec/` outside §4.1 keys a recovery or retry behavior on the gRPC pod/session
classification. A sweep of every `pod-scoped`/`session-scoped` site in `spec/` returns §4.7.1's two
per-message sentences, §10.1.4's hold/gauge sentence, §12.2 Redis key prefixes, §4.9's rotation ceiling,
and §28.5.3's JSONL frame rules, none of which reads the retired table. So retiring it strands no
recovery statement. — EVIDENCE: spec/04_system-components.md:725, :726, :872; spec/10_gateway-internals.md:60;
spec/12_storage-architecture.md:202; spec/28_communication-channels.md:835-844

USEFUL [Standing context / Traps]: the entry declining the session-scoped-fence-versus-pod-wide-hold
finding, and the entry recording the equal-generation re-fence as staged-nowhere-on-purpose, together
covered the only two reliability leads this lens found in the staged text. Without them this round would
have re-derived both from `spec/10:39` and `pkg/gateway/coordination/coordfence/coordfence.go:171-183`.

USEFUL [Standing context / Traps]: the naive-proto-parser warning. Writing the census parse with
per-line brace counting and same-line-close handling from the start produced the settled 31/25/6 answer
on the first run.


### [spec.1.review-security.1]

DECISION: EMPTY findings list under the security lens on the staged spec edits — sixth consecutive empty
pass for this lens on this proposal, and the first in the new spec lane. I re-derived both lens checks from
the tree instead of replaying the standing context. Check (1), regression of an established control: the only
control retired is the tier-0 table-reconciliation gate, retired openly, with its replacement's four refusal
clauses AND its blind spot written into normative spec text (the third staged paragraph) and both residuals
priced in summary OD1. Check (2), trust boundary and durability of a security bound: the staged §4.1 block
contains no residual-state limit, no quota or reuse counter, no Redis-backed role, and no value sourced from
a pod self-report, so there is nothing for the proxy-mode bar to bite on — ALTERNATIVES rejected: the
session-scoped fence exiting a pod-wide hold (0076 OD3 territory, and the hold allowlist is keyed on gRPC
method names, not on the scope class), the nested-address hole (paragraph 3 reads onto it), the fail-open
unconventional-spelling residual (disclosed in the staged text and in OD1).

FACT: I re-derived the proto census with an independent brace-counting parser rather than trusting the log.
31 RPC request types across the two service blocks; 25 declare a top-level `SessionId session_id`; the 6 that
do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`,
`GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. The
name/type biconditional holds with zero violations across every field in the file including `oneof` arms. And
the retired table's own rows, extracted mechanically, give 26 session and 6 pod over 32 rows, so the derived
session class differs from the table's by exactly `CoordinatorFenceRequest`, pod to session. That one-way
delta is what makes the whole retirement fail-closed-safe: the `spec/05:515` empty-identifier refusal hangs
off the session class and can therefore only be added — EVIDENCE: spec/04_system-components.md:153-186;
schemas/lenny-adapter.proto:1455-1461; spec/05_runtime-registry-and-pool-model.md:515.

FACT (CORRECTS the Settled bullet "Proto anchors, current and verified"): SPEC-1's citation of
`schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s `SessionId session_id = 2` is CORRECT,
not "one line off". `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458`
is `SessionId session_id = 2;`. The standing-context bullet claims the field sits at `:457` and that SPEC-1
is off by one; it is the bullet that is wrong. A future round that "fixes" the proposal to `:457` would
introduce a false citation — EVIDENCE: schemas/lenny-adapter.proto:456-458;
0075_...review-log.md standing context, Settled, "Proto anchors, current and verified".

FACT: `spec/16_observability.md:189`'s `lenny_adapter_unaddressed_frame_rejected_total` is the one metric
that sounds like it depends on the §4.1 classification and does not. It counts §28.5.3 intra-pod JSONL
frames rejected in an Attach stream's demultiplexer, a different classification with a different population.
Retiring the §4.1 table changes no metric, no alert, and no audit surface — EVIDENCE:
spec/16_observability.md:189; spec/28_communication-channels.md:835-844.

FACT: §10.1.4's hold-state allowlist is stated in `spec/` by RPC name (`CoordinatorFence`,
`NegotiateVersion`, health probes) and implemented as a map of gRPC full-method strings, so the fence's
reclassification cannot widen or narrow it. Re-derived from both sides rather than taken from the log —
EVIDENCE: spec/10_gateway-internals.md:57; pkg/adapter/holdstate.go:52-58, :336, :349.

FACT: the fail-closed ordering inside the fence handler is intact and is the first branch. The empty-session
refusal at `pkg/adapter/coordination.go:109-111` runs before `boundSlotState` at `:116` and before the
generation comparison at `:121`, so the one behavioral obligation the reclassification imports is met on
entry rather than incidentally by a later resolve failing — EVIDENCE: pkg/adapter/coordination.go:107-134.

FACT: `grep -rn CoordinatorFence spec/ docs/` shows `spec/04_system-components.md:175` and `:188` are the
only two sites in `spec/` that state the message's scope class. Both are inside the block SPEC-1 replaces,
so the reclassification leaves no orphaned pod-scope sentence anywhere in the specification — EVIDENCE:
spec/04_system-components.md:175, :188.

USEFUL [non-spec.5.review-security.1]: its cache WATCHOUT is real and it fired for me in the opposite
direction. My computed key was `09a51355eaa8`, identical to that round's, because the three keyed files are
byte-identical, yet the cache path for this lane/round (`security-r1-09a51355eaa8.json`) did not exist, so I
did the full review. The key carries no lane and no round component in its *content*, only in the filename
prefix, so a same-named lane+round pair across lanes would collide. Worth knowing before someone "fixes" the
prefix.

OPEN: nothing new from this lens. Six consecutive empty passes with the load-bearing facts independently
re-derived each time. Re-run it only if a fix pass edits the third staged paragraph (the gate's clauses and
its blind spot), the OD1 residual statement, or any sentence tying a scope class to a refusal.

### [reconcile.2]

DECISION: Rebuilt the summary's deliverable index against the staging as it now stands, verified the checklist's SPEC-lane block and the non-spec steps' dependencies, discharged the deferred corrections, and carried the loop's unclosed decisions into the summary — BECAUSE the staged set is still SPEC-1, TEST-1, and TEST-2, each already carried once in the index and once in the checklist as a single-lane step in dependency order, so the index needed one correction rather than a rebuild and the checklist needed none. The spec staging did not converge; this pass reconciles to its current text and no further.

DECISION: The deliverable index's TEST-2 entry said the deliverable "deletes the membership and coverage clauses that key on the retired table". The staged non-spec change restates the declaration's comment on the derivation rule and removes only the false coverage clause, so the index now says that. SPEC-1's and TEST-1's entries were re-derived from the staged text and stand as written. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Deliverable index`

FACT: The checklist needed no edit. S1 (spec, SPEC-1), S2 (test, TEST-1), and S3 (test, TEST-2) name every staged deliverable exactly once, name none that does not exist, carry one lane each, put the single spec step first, and give `Depends on: S1` on both non-spec steps, which is the spec step staging the statements their work implements. S1's what-lands line already names the whole `#### Request Message Scope` block with the anchors the current SPEC-1 names.

DECISION: Four items the loop left in `## Open` are decisions rather than unanswered verification questions, and each is now carried in the summary's `## Open decisions for human to make` as OD3 (whether the replacement gate keeps the gate file's path), OD4 (whether the section 28.5.3 registration follows the replacement gate or is dropped), OD5 (whether the widened tier-3 session set is accepted with nothing holding it complete), and OD6 (whether `spec/` may name a test tier). Each states its ground and says that the loop derived no recommendation, because supplying one is adjudication this pass may not do. OD2 now records the same absence, which `## Open` has carried since [f1.cleanup]. The remaining `## Open` items are verification questions, pre-existing defects owned elsewhere, or wording, and none was moved.

OPEN [docs/reference/adapter-contract.md, spec/04_system-components.md]: `docs/reference/adapter-contract.md:69` describes `CoordinatorFence` as the "precondition for any subsequent operational RPC", which reads as a pod-wide gate, and its source at `spec/04_system-components.md:713` carries the same imprecision. After 0076 the fence and the generation are per bound session, so the true statement is that the fence is the precondition for subsequent operational RPCs naming the session it fenced. Neither file is editable by this pass and neither is staged here. Owner: whoever takes the 0076 residue, which proposal 0080's inventory registers.

OPEN [spec/04_system-components.md]: the `CoordinatorFence` row at `:712` reads "Announce new `coordination_generation` to the pod on coordinator handoff", which 0076 falsified by moving the recorded generation onto the session's slot entry (`pkg/adapter/slot.go:59`) and which `spec/28_communication-channels.md:314-317` already contradicts. What is true instead is that the fence announces the generation for the session it names. The remedy lands in `spec/04_system-components.md`, which this pass may not edit and which SPEC-1 does not reach. Owner: the same 0076 residue.

OPEN [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]: §2's bullet at `:453-454` records the §4.1 `ShutdownRequest` classification limit as taken by this proposal, which does not take it. The bullet splits: the declared message-scope table and its reconciliation gate stay on the owned side, and the classification limit returns to §1's inventory as a gap no proposal takes, because SPEC-1 leaves `spec/04_system-components.md:190` unedited and the replacement gate reads the protocol definition alone. The remedy lands in another proposal's file, which this pass may not edit. Owner: 0080, when it converges.

OPEN [0075_fix_derive-message-scope-from-the-address-type.status.md]: the status file's `**Date:**` line reads "§11 records what the rewrites changed". No landing file carries a `## 11`; the only one is in this review log, so the pointer resolves across files rather than within the document a reader is holding. The summary's copy is already gone and the status file is the only carrier left. The remedy is to name the review log or drop the reference, and it lands in the status file, which is outside this pass's editable set.

OPEN [docs/api/internal.md]: two published protobuf excerpts spell a top-level `string session_id` on a request message, which after SPEC-1 is a spelling the replacement tier-0 gate refuses. `:209-215` publishes `message CheckpointRequest { string session_id = 1; ... }` while the shipped message is a `oneof msg` of `CheckpointStart`, `CheckpointGrant`, and `CheckpointAbort` plus `int64 coordination_generation = 4` and declares no address; `:273` publishes `message DemoteSDKRequest { string session_id = 1; }` while the shipped message declares no address and derives pod-scoped. Both are false about the shipped proto today, so SPEC-1 does not make them wrong and they are not missed edit sites; SPEC-1 changes their severity. The page is also stale on `StopSession` and `UploadFiles`, which no service declares. The remedy lands in `docs/api/internal.md`, which this pass may not edit. Owner: the docs loop, or 0080's residue inventory.

OPEN [0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md]: TEST-1 says the replacement gate's cases are re-registered "under the same sections", which includes §28.5.3 (`tests/spec-map.json:5670`). This pass did not close it although the file is editable, because the remedy is to decide which sections the replacement cases annotate rather than to repair a statement the spec staging falsified, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` credits from the annotation, so the annotation decides the register entry. The decision is now carried to the human as summary OD4. The edit to TEST-1 lands once that decision is answered.

OPEN [0075_fix_derive-message-scope-from-the-address-type.spec-changes.md]: D2 calls the addressing convention "an addressing convention that nothing checks today", which overstates it. `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`) already asserts, for each hand-listed member, that `session_id` exists and is of type `lenny.adapter.v1.SessionId`. What is true instead is that no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset. The remedy lands in `spec-changes.md`, which this pass may not edit. Owner: the next spec round or a fix pass.

WATCHOUT: the deferred corrections above were all recorded before this pass and several were already recorded at [reconcile.1]. Every one names a file outside this pass's editable set, or needs a decision the loop routed to the human, so none was closed here. The next loop's first round should read them as work owed rather than as history.

### [f1.open-decisions.OD2]

DECISION: resolved OD2 as "accept the widening" and withdrew it from `## Open decisions for human to make`. The answer is now stated as a requirement in the staged non-spec changes (§5, TEST-2) with its ground, and `spec-changes.md` §7 is deleted, since the tier-3 population question was its only remaining item and the section is empty without it.
EVIDENCE: 0075_....non-spec-changes.md §5 TEST-2; 0075_....spec-changes.md (§7 removed); 0075_....summary.md

FACT: re-verified message by message rather than taken from the standing context. Each of TEST-2's eight additions declares `SessionId session_id = 1` at the top level and none declares a field named `slot_id` or any `reserved`: `ListPlatformToolsRequest` `:341-343`, `CallPlatformToolRequest` `:364-368`, `ListSessionConnectorsRequest` `:383-385`, `ListConnectorToolsRequest` `:403-406`, `CallConnectorToolRequest` `:419-424`, `CoordinatorFenceRequest` `:1455-1461`, `ExportPathsRequest` `:1538-1549`, `ConfigureWorkspaceRequest` `:1673-1684`. Both widened arms are green on the shipped proto on day one.
EVIDENCE: schemas/lenny-adapter.proto

FACT: the authority the staged answer rests on is the split itself rather than a preference. `retiredDuplicateNumbers` takes the "carried the retired duplicate" population verbatim, so the session set needs a membership rule that is not that one, and the derivation rule SPEC-1 states is the rule the set is named for. `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` (`:130-141`) asserts `reservesName(md, retiredFieldName)`, which the fence fails whatever number is chosen, which is what forces the split.
EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141

FACT: the widening costs no register edit and no new test function. `tests/spec-map.json:172` credits the tier-3 file whole-file, and `addressRuleCases` (`tests/tier0_static/address_rule_citation_test.go:45-47`) names `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1`, which TEST-2 neither renames nor adds to.
EVIDENCE: tests/spec-map.json:172; tests/tier0_static/address_rule_citation_test.go:45-47

WATCHOUT: the standing context's `## Open` bullet "OD2 carries its question and its ground but no recommendation, no losing alternatives, no cost of deciding otherwise, and no confidence" is now spent, because OD2 is withdrawn. A round-boundary curation pass should retire that bullet rather than act on it.

WATCHOUT: no staged file names OD2, cites it, or makes a deliverable conditional on it. The staged text states the widened population as TEST-2's requirement in its own terms, so a later pass must not re-add a pointer to the summary's decisions section.


### [f1.open-decisions.OD3]

DECISION: resolved OD3 as "keep the path" and withdrew it from `## Open decisions for human to make`. The answer is stated as a requirement in the staged non-spec changes (§5, TEST-1), as a new paragraph after the one that retires the table readers: the replacement gate keeps `tests/tier0_static/adapter_proto_message_scope_test.go` and TEST-1 rewrites that file in place. No `### Retired` block replaces the entry, and OD1 and OD4 through OD6 keep their identifiers verbatim.
EVIDENCE: 0075_....non-spec-changes.md §5 TEST-1; 0075_....summary.md

FACT: the authority is the tier-0 registration gate rather than a preference. `slotAddressCaseFiles` names the gate file as a literal string at `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, five cases range over that list (`:973`, `:1028`, `:1054`, `:1096`, `:1110`), and each entry resolves through `repoFileLines` (`:699-706`), which calls `t.Fatalf` on a file it cannot read. A rename is therefore a hard tier-0 failure in five cases rather than a surplus entry, and it adds that file to the blast radius.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:236-409, :699-706, :970-989

FACT: re-verified rather than taken from the standing context. `spec-changes.md` §9 lists six files (`spec/04_system-components.md`, the gate file, `adapter_proto_parse_test.go`, `claim_register_proto_agreement_test.go`, `tests/spec-map.json`, the tier-3 file) and does not carry `tests/tier0_static/spec_map_slot_address_registration_test.go`, so the staged blast radius already presupposes the path is kept. No staged text anywhere in the proposal names a replacement filename.
EVIDENCE: 0075_....spec-changes.md:155-168

FACT: the edit adds no deliverable and resequences none. TEST-1's target set, its step in the checklist, and the files-touched list are unchanged, so nothing is deferred to the implementation checklist.
EVIDENCE: 0075_....implementation-checklist.md

WATCHOUT: the standing context's `## Open` bullet "Whether the replacement gate keeps the path ... Never stated in D2, assumed by every enumeration" is now spent. A round-boundary curation pass should retire it rather than act on it. The answer landed in TEST-1 rather than in D2, because the path is a property of the test staging and `non-spec-changes.md` is where the test staging lives.

WATCHOUT: no staged file names OD3, cites it, or makes a deliverable conditional on it. The staged paragraph states the kept path as TEST-1's requirement in its own terms, so a later pass must not re-add a pointer to the summary's decisions section.


### [f1.open-decisions.OD6]

DECISION: resolved OD6 as "yes, `spec/` may name a test tier" and withdrew it from `## Open decisions for human to make`. No staged text changes: `spec-changes.md:20-21` already stages the sentence "A tier-0 gate refuses a protocol definition in which a field named `session_id` is not of type `SessionId` ...", which is the answer stated as a requirement, so SPEC-1, TEST-1, and TEST-2 stand exactly as they were. No `### Retired` block replaces the entry, and OD1, OD4, and OD5 keep their identifiers verbatim.

FACT: `spec/` already names the harness test tiers in normative exit criteria, so the staged sentence is not the first of its kind. `spec/18_build-sequence.md:85` states "Tier 0 (Static) and Tier 11 (Documentation) pass on the empty repository and `lenny-test validate-maps` passes against the empty spec map and change graph", `:98` "Tier 0 (Static) passes", `:244` "Tier 3 contract tests in `tests/tier3_contract/rest_sessions/` pass against the handler driven by `httptest`", `:349` "Tier 2 component tests for `llm_proxy` pass, Tier 3 contract tests against the Anthropic mock pass", and `:781` "Tier 11 documentation is fully exercised". These are the harness tiers rather than the capacity tiers: Tier 0 and Tier 11 have no capacity counterpart, and `:244` names the tier-3 directory outright. — EVIDENCE: spec/18_build-sequence.md:85, :98, :244, :349, :781

FACT: the narrower question, whether a normative contract section may name test machinery, is settled inside the file SPEC-1 edits. `spec/04_system-components.md:438` states that the `lenny-sandboxclaim-guard` webhook "is deployed as part of the Helm chart under `templates/admission-policies/` and is covered by the admission policy integration test suite (`tests/integration/admission_policy_test.go`)", and `:304` states that "The Phase 2 startup benchmark harness ([Section 18]) must include a checkpoint duration benchmark". The same practice runs at `spec/05_runtime-registry-and-pool-model.md:445`, `spec/15_external-api-surface.md:1440`, `spec/17_deployment-topology.md:84`, and `spec/28_communication-channels.md:63-64`. The staged sentence composes two practices the specification already holds. — EVIDENCE: spec/04_system-components.md:304, :438

FACT: no rule creates a countervailing obligation. `.claude/rules/test-coverage.md` states no `spec/` constraint, `spec-driven-development.md` governs the `// spec:` citation direction from code into spec, and the §28.4 claim register is scoped to §28 statements (`tests/tier0_static/claim_register_test.go:24-26`: "The claim register validator reads `tests/claim-map.json` and checks that it says what §28.4 requires a claim to say"), so a §4.1 sentence naming a gate owes no register row. — EVIDENCE: tests/tier0_static/claim_register_test.go:24-26

WATCHOUT: the standing-context `## Open` line at `review-log.md:116` ("`spec/` gains its first mention of a test tier in the staged constraint paragraph. `spec/28:64` and `spec/18:85`, `:98` are the precedents") is now spent and its first clause is false; the `CORRECTS` at `:711` that denied the precedents is itself false, since `spec/18_build-sequence.md:85` and `:98` name Tier 0 and Tier 11. A round-boundary curation pass should retire the line rather than act on it. Standing context is not this firing's to edit.

WATCHOUT: the precedents spell the tier as "Tier 0 (Static)" while the staged sentence spells it "a tier-0 gate", and no `spec/` file uses the lowercase hyphenated form for a harness tier (`spec/04:872`'s "tier-1 compromise-indicator signal" is a severity label, and `spec/04:80-85`, `:504-506` are capacity tiers). The difference is presentational, OD6 asked about "any spelling" by its own wording, and correcting it would be an unstaged edit to SPEC-1's replacement block. Recorded rather than acted on.

WATCHOUT: no staged file names OD6, cites it, or makes a deliverable conditional on it. The staged constraint paragraph states the tier in its own terms, so a later pass must not add a pointer to the summary's decisions section.

FACT: the edit adds, removes, merges, splits, and resequences no deliverable, so nothing is deferred to the implementation checklist. — EVIDENCE: 0075_....implementation-checklist.md

### [f1.open-decisions.out-of-scope.docs-api-internal-md]

DECISION: the disposition `out-of-scope-stands` was applied for the `docs/api/internal.md` staleness. Added one
entry to `## Defects in the shipped tree that this proposal does not stage` in
`0075_....summary.md`, placed after the equal-generation re-fence entry and immediately before
`## Impacts on other proposals`. It names the defect, gives it as file:line, and states why this proposal
stages no repair. No decision is opened and no fix is staged.

FACT: the excerpt is false about the shipped protocol definition today. `docs/api/internal.md:210-213`
declares `string session_id = 1`, `string checkpoint_id = 2`, `string consistency = 3`; the shipped
`CheckpointRequest` is a `oneof msg` of `CheckpointStart`, `CheckpointGrant`, `CheckpointAbort` plus
`int64 coordination_generation = 4` and contains no `session_id` at any depth. — EVIDENCE:
docs/api/internal.md:209-215, schemas/lenny-adapter.proto:1173-1187

FACT: the same staleness recurs twice more on the page, and the entry records both, because the remedy is
page-wide. `docs/api/internal.md:272-274` publishes `message DemoteSDKRequest { string session_id = 1; }`
while the shipped message declares `string reason = 1` alone; `docs/api/internal.md:94` publishes an
`UploadFiles` RPC, and that identifier appears nowhere under `schemas/`, so neither `service Adapter`
(`schemas/lenny-adapter.proto:32`) nor `service GatewayControl` (`:261`) declares it. — EVIDENCE:
schemas/lenny-adapter.proto:1694-1698, grep -rn UploadFiles schemas/ returns nothing

FACT: nothing this proposal applies reddens on account of the page. `tests/tier11_docs/code_blocks_test.go`
dispatches fenced blocks on `json`, `yaml`/`yml`, `go`, `bash`/`sh`/`shell`, and `sql` at `:103-113`, with
no `protobuf` arm, and the only files under `tests/`, `cmd/`, or `scripts/` naming the page are
`tests/tier0_static/fragment_link_test.go` and `tests/tier0_static/naming_lint_test.go`, both of which read
anchor identifiers. — EVIDENCE: tests/tier11_docs/code_blocks_test.go:103-113, grep -rln "api/internal.md"
tests/ cmd/ scripts/

FACT: adding the entry falsified the section preamble's claim that one further defect was confirmed in the
working tree, so that sentence was rewritten to name the list rather than a count. No other sentence in the
summary is falsified: `:36` states that no reader-facing documentation file is touched, and this entry
stages no edit to one, so it stands. — EVIDENCE: 0075_....summary.md:168

WATCHOUT: this entry is a record, not a deliverable. A later pass must not read it as staging a
`docs/api/internal.md` edit, must not add that page to a files-touched list, and must not reopen the
deferral as a finding. The log already carries the two earlier rounds that declined to file it
(review-log.md:219, :396).

FACT: the edit adds, removes, merges, splits, and resequences no deliverable, so nothing is deferred to the
implementation checklist. — EVIDENCE: 0075_....implementation-checklist.md

### [f1.open-decisions.out-of-scope.spec-04-712]

DECISION: the disposition `out-of-scope-stands` was applied for the §4.7 `CoordinatorFence` row at
`spec/04_system-components.md:712`. Added one entry to `## Defects in the shipped tree that this proposal
does not stage` in `0075_....summary.md`, placed after the `docs/api/internal.md` entry and immediately
before `## Impacts on other proposals`. It names the defect, gives it as file:line, and states why this
proposal stages no repair. No decision is opened and no fix is staged.

CORRECTS [standing context "Deferred", the `spec/04_system-components.md` bullet at review-log.md:124]: the
bullet says `spec/28_communication-channels.md:314-317` "already contradicts" the row, and assigns the
residue to 0080's inventory. Both halves are wrong and the summary entry states neither. §28:314-315 reads
"announces the new `coordination_generation` to the pod on coordinator handoff. The pod records the
generation against the session the fence names", so §28 carries the row's own words and adds the per-session
clause after them; the row compresses §28's current post-0076 sentence rather than contradicting it. And
0080's inventory runs §1.1 through §1.21 and none of those entries names the row, so the defect is unowned.
The log entry itself stands as written, under the rule that a log entry records what was true when it was
written.
EVIDENCE: spec/28_communication-channels.md:314-317, proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:37-448

FACT: the row's imprecision is a 0076 residue and predates anything staged here. Commit `284d7904c` ("Apply
proposal 0076 SPEC-2 to spec/28 and spec/29", 2026-09-06) rewrote §28's passage after CODE-1 moved the
recorded generation onto the session's slot entry, and kept "to the pod on coordinator handoff" while adding
the per-session clause. §4.7's row was not rewritten alongside it. — EVIDENCE:
pkg/adapter/slot.go:55-59, spec/28_communication-channels.md:314-317, spec/04_system-components.md:712

FACT: nothing staged reaches the row. SPEC-1's edit is bounded to `spec/04_system-components.md:151`,
`:153-186`, and `:188`, and the replacement block names no RPC, no generation, and no hold. The row states no
scope class, so the derivation rule does not read onto it; the pod-scope classifications in `spec/` are
`:188`, which SPEC-1 retires, and `:726`, which SPEC-1 keeps. — EVIDENCE:
0075_....spec-changes.md:112-129, spec/04_system-components.md:712

FACT: no gate reads the row's description. `tests/tier11_docs/spec_47_rpc_row_naming_test.go` captures the
backticked identifier in each §4.7 row's first column (`specTableRowNameRE`, `:35`) and holds Go doc comments
to that name set, and no other test under `tests/` parses §4.7 prose for the fence. — EVIDENCE:
tests/tier11_docs/spec_47_rpc_row_naming_test.go:33-45

WATCHOUT: correcting the row costs the proposal a claim it makes. `0075_....summary.md:36` states that no
reader-facing documentation file is touched, and the row's mirror at `docs/reference/adapter-contract.md:69`
carries the same compression, so a §4.7 edit pulls the mirror in with it. That is the reason the disposition
is to leave the defect where it is. — EVIDENCE: 0075_....summary.md:36,
docs/reference/adapter-contract.md:69

WATCHOUT: this entry is a record, not a deliverable. A later pass must not read it as staging a `spec/04`
or `docs/reference/adapter-contract.md` edit, must not add either file to a files-touched list, and must not
reopen the deferral as a finding.

FACT: the section preamble at `0075_....summary.md:168` names the list rather than a count, so this entry
falsifies no sentence in the summary and none was rewritten. — EVIDENCE: 0075_....summary.md:168

FACT: the edit adds, removes, merges, splits, and resequences no deliverable, so nothing is deferred to the
implementation checklist. — EVIDENCE: 0075_....implementation-checklist.md

### [f1.open-decisions.out-of-scope.adapter-contract-69-precondition]

DECISION: the disposition `out-of-scope-stands` was applied for the `CoordinatorFence` precondition clause
at `docs/reference/adapter-contract.md:69` and its source at `spec/04_system-components.md:712`. Both
sentences stay in the tree unedited and this proposal stages no repair. No decision is opened and no fix is
staged.

FACT: the item's site already had an entry. The `[f1.open-decisions.out-of-scope.spec-04-712]` apply, which
ran earlier in this firing, wrote the entry for the same §4.7 row in `## Defects in the shipped tree that
this proposal does not stage`, gave both anchors as file:line, named the mirror at
`docs/reference/adapter-contract.md:69`, and stated why no repair is staged. A second entry for the same
row and the same mirror would duplicate it, so none was written. — EVIDENCE: 0075_....summary.md:217-249

CORRECTS [the same entry's first paragraph]: it read "a reader of §4.7 alone takes the announcement and the
precondition to be pod-wide", which sweeps the precondition clause into the recorded defect. The clause is
correct as written. `spec/10_gateway-internals.md:57` rejects every other inbound RPC with `UNAVAILABLE` and
a `coordinator_hold` detail until a new coordinator successfully fences, `:60` keeps the hold and the
`lenny_adapter_coordinator_hold` gauge pod-scoped, and `spec/28_communication-channels.md:322` states that
the fence is the hard precondition for every other operational RPC to the pod. The shipped adapter agrees:
the hold is enforced by pod-level interceptors (`pkg/adapter/holdstate.go:335`, `:348`, registered at
`pkg/adapter/transport.go:46-47`) and a fence for any bound session clears it
(`pkg/adapter/coordination.go:150-156`). The entry now says the precondition clause stands, carries that
ground, and confines the recorded imprecision to the announcement clause. That is the whole edit this item
made. — EVIDENCE: 0075_....summary.md:224-233, spec/10_gateway-internals.md:57, :60,
spec/28_communication-channels.md:322, pkg/adapter/holdstate.go:335, pkg/adapter/coordination.go:150-156

WATCHOUT: the correction the item's reading proposed, that the fence is the precondition for subsequent
operational RPCs naming the session it fenced, is false against the tree. What proposal 0076 made
per-session is the generation the pod records, and the hold the fence clears stayed pod-scoped. A later pass
that reopens this pair must keep the two halves apart: the announcement clause compresses
`spec/28_communication-channels.md:314-317` and is imprecise, and the precondition clause is accurate and
must not be qualified per session. — EVIDENCE: spec/28_communication-channels.md:314-317 against :320-324

FACT: no gate reads either sentence. `tests/tier11_docs/spec_47_rpc_row_naming_test.go` captures the
backticked identifier in each §4.7 row's first column and never the description, and the tier-11
reconciliation gate that does hold a `spec/04`-to-`docs/reference/adapter-contract.md` pair holds the
`ReportSessionScrub` addressing sentence on `spec/04:725` and `docs/reference/adapter-contract.md:81`
instead. — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:33-45,
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: the edit adds, removes, merges, splits, and resequences no deliverable, so nothing is deferred to the
implementation checklist, and it falsifies no other statement in the summary. The section preamble at
`0075_....summary.md:168` names the list rather than a count, and the claim at `:36` that no reader-facing
documentation file is touched is unaffected, because no file outside the proposal directory was edited. —
EVIDENCE: 0075_....summary.md:36, :168

### [f1.cleanup]

DECISION: rewrote nothing in `summary.md` — BECAUSE the file already carried exactly the eight target headings in the target order (`# Summary: ...`, then `## Summary` holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**`, then `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, `## Deliverable index`), with no lead paragraph above `## Summary`, no heading below `##` anywhere in the file, no `### Retired` block or equivalent inside the open-decisions section, no meta-list of staged items with dispositions, and no errata block owed to files this loop cannot edit — ALTERNATIVES: moving the `**Watch out for.**` sentences about 0073's immutability and the 0076 sequencing, and D5 and D6 under `**Decisions.**`, into `## Impacts on other proposals` under the prose-about-another-proposal rule; rejected on the same ground [f4.cleanup] rejected it, that those parts are listed parts of `## Summary` rather than unlisted content, and the 0073 and 0076 rows already carry the validity claims, so a move would create the second copy that rule exists to prevent.

FACT: every item this firing handled is accounted for, checked one at a time rather than taken from the firing list. The three items the write path resolved have left the summary and each is stated as a requirement in a staged change file: OD2's widened tier-3 population is `non-spec-changes.md` §5 TEST-2 (the eight named additions to `sessionScopedMessages`), OD3's kept gate path is `non-spec-changes.md:43`, and OD6's tier-naming answer is the staged constraint sentence at `spec-changes.md:20-21`. The three items still the human's are the three entries the section carries, with their identifiers verbatim: OD1 (retire the table and its gate, accepting the two residuals), OD4 (the section 28.5.3 registration), and OD5 (the unheld widened session set). The four `marker:unscoped` items are the three entries under `## Defects in the shipped tree that this proposal does not stage`, the fourth having been folded into the §4.7 row entry by the apply that ran before it. The three `marker:` proposal items are one impacts row each.
EVIDENCE: 0075_....summary.md:83-159, :161-249, :251-257; 0075_....non-spec-changes.md:43, :56-70; 0075_....spec-changes.md:20-21

FACT: the identifier set carries a gap and the gap is correct. `## Open decisions for human to make` runs OD1, OD4, OD5, because OD2, OD3, and OD6 were withdrawn by this firing's own applies. The gap is preserved rather than closed: successive firings join on the identifier string, and renumbering OD4 to OD2 would have the next adjudication treat both surviving entries as fresh.
EVIDENCE: 0075_....review-log.md — `[f1.open-decisions.OD2]`, `[f1.open-decisions.OD3]`, `[f1.open-decisions.OD6]`

FACT: both section preambles are true of the entries they now head, read against the entries rather than assumed. `## Defects in the shipped tree that this proposal does not stage` opens by stating that both specification defects are staged (SPEC-1 and TEST-2) and that the further defects confirmed in the working tree are listed below; it names the list rather than a count, so the two entries this firing's applies added did not falsify it. `## Open decisions for human to make` and `## Impacts on other proposals` carry no preamble. This pass moved no text, so it falsified no statement and corrected none.
EVIDENCE: 0075_....summary.md:163-168

FACT: `## Deliverable index` is preserved byte for byte in last position, with its three lines (SPEC-1, TEST-1, TEST-2) in their existing order and wording. The reconciliation pass owns it and this pass did not touch it.
EVIDENCE: 0075_....summary.md:259-276

FACT: the summary carries no dangling intra-document pointer. `grep -n "§7\|§11"` over the file is empty, so the retired `spec-changes.md` §7 and the review log's `## 11` appendix are referenced from nowhere in it. The `§11` pointer that survives is `status.md:22`, which is already carried as a deferred correction and is outside this pass's editable set.
EVIDENCE: 0075_....summary.md; 0075_....status.md:22

OPEN: OD4 and OD5 each carry the question, the ground, and what each answer leaves staged, and each says outright that the review loop derived no recommendation. Neither carries a recommendation, losing alternatives, the cost of deciding otherwise, or a confidence, which is less than `## Open decisions for human to make` asks of an entry. OD1 carries all of them. This is the same shortfall four earlier firings observed on the withdrawn OD2 and left; supplying the missing parts is adjudication, which a format pass may not do. The standing-context `## Open` line that tracked it names OD2 and is spent; the shortfall now sits on OD4 and OD5.

WATCHOUT: this review log carries `## Standing context`, `## Ledger`, `## Resolved in adversarial review`, and `## 11. What the 2026-09-06 rewrites changed`, and has no `## Retired` section at all. This block is therefore spliced at the end of `## Ledger`, immediately before `## Resolved in adversarial review`, as [f4.cleanup] recorded. Appending to the end of the file would bury the entry inside the `## 11` appendix.


### [spec-recheck.1.review-applicability.1]

DECISION: returned an empty findings list for the applicability-and-sequencing lens over the spec staging — BECAUSE the delta this loop exists for is a deletion only (the whole `## 7. Open decisions for review` block left `spec-changes.md`), and a deletion cannot create a forward reference, an underspecified target, an unresolvable anchor, or a gate obligation; I re-simulated S1 against the tree anyway and every anchor, every property a later edit needs, and every gate disposition still resolves — ALTERNATIVES: filing the loss of the retired table's Service and Direction columns as class-3 content loss (rejected again: §4.7.1's two RPC tables at `spec/04_system-components.md:700-719` and `:723-726` carry service and direction for every RPC, and nothing in the tree reads the retired columns); filing the indefinite "a protocol definition" in the staged gate sentence as an underspecified target (rejected: paragraph 1's "the gateway-adapter protocol" is the antecedent and `adapterProtoPath` fixes it on the implementation side, and the standing context already records it as a watchout rather than a finding).

FACT: the §7 deletion left no dangling pointer. The only cross-file references into the spec staging are `non-spec-changes.md:37` ("the constraint §4 of the staged spec changes states") and `:48` ("the files §9 of the staged spec changes lists"), both of which still resolve; no file in the proposal directory outside the review log cites a "§7" of the spec staging — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:37,48

FACT: `#### Request Message Scope` sits under `### 4.1 Edge Gateway Replicas` (`spec/04_system-components.md:40`), with `#### Gateway Internal Subsystems` at `:99` between them, and the next `###` is `### 4.2 Session Manager` at `:192`. So the block SPEC-1 replaces is bounded by `:149` and `:192` and the proposal's "§4.1" label is exact — EVIDENCE: spec/04_system-components.md:40,99,149,192

FACT: no gate anywhere reads the §4.1 block except the one TEST-1 replaces. `messageScopeSpecPath`, `parseMessageScopeTable`, and `declaredScope` have no reader outside `adapter_proto_message_scope_test.go`; the tier-11 gates that open `spec/04` all call `specSection(..., "### 4.7 ")`; a repo-wide grep for the heading text and for a `#request-message-scope` fragment returns only the heading's own line — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:31,54,75; tests/tier11_docs/spec_47_rpc_row_naming_test.go:46; tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:57,137; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:47

USEFUL [Standing context / Settled]: the anchor inventory (spec/04 `:149`/`:151`/`:153-186`/`:175`/`:188`/`:190`, spec/10 `:53`/`:57`/`:60`, the proto set, the gate header `:17-27`) was correct in every particular I re-checked, and spot-checking it instead of re-deriving it saved the whole pass. The one place the standing context is itself off: it says SPEC-1 cites `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s address "one line off"; `:458` is exactly `SessionId session_id = 2` and the citation is right.

USEFUL [Traps / "Orchestrator briefs carry stale content"]: the brief for this round still describes "§7 question 1" as the existential question and still gives the pre-refresh proto anchors (`:589`, `:1166`, `:1447`). §7 no longer exists in the staging and the correct proto anchors are `:596`, `:1173`, `:1455`. Trusting the brief would have produced a finding against text that is not there.


### [spec-recheck.1.review-citations.1]

DECISION: returned an empty findings list — BECAUSE the delta this loop exists for is a pure
deletion (spec-changes.md `## 7. Open decisions for review` removed; nothing else in the file
changed), and I independently re-verified every concrete citation still standing in
`spec-changes.md` against the tree at HEAD (`c134918ab`) rather than trusting the standing
context — ALTERNATIVES: D1's "the ones that carry none are exactly its pod rows" reading as
off-by-one (declined: the "other than the stream envelope" restrictor distributes and the next
sentence assigns the two remaining rows to the envelope clause, so the 25+5+2=32 accounting is
exact and complete); "0073's §4.2 value rule" reading as `spec/04` §4.2 (declined: 0073's own
`### 4.2 The rule` at `proposals/0073_...md:964`, and 0073 uses the same self-referring shorthand
for its own §4.1 at `:897`).

FACT: the mechanical citation set of `spec-changes.md` is 18 backticked path forms plus the bare
`:151 :153-186 :188 :190 :726 :60 :64` forms, and every one resolved today. Spot-checks worth
keeping: `spec/04:149` heading / `:151` intro / `:153-186` table (32 rows) / `:175` fence row /
`:188` grounding / `:190` ShutdownRequest / `:192` `### 4.2`; `spec/04:725` and `:726` are the
only per-message scope sentences outside §4.1; `spec/10:57` hold-exit and `:60` pod-scoped hold;
`spec/05:515`; proto `:458` (`SessionId session_id = 2`), `:499-503`, `:596`, `:1173`, `:1174`,
`:1217` (`SessionId session_id = 7`), `:1250`, `:1455-1456`; `checkpoint.go:74-84`;
`adapter_proto_message_scope_test.go:17-27` (header comment, exactly that span);
`adapter_proto_parse_test.go:10-15` (package doc, exactly that span);
`claim_register_proto_agreement_test.go:64` (`fields := protoFields(protoBody)`);
`tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`;
`docs/reference/adapter-contract.md:81`; `spec-map.json:156 :169 :5670`.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md

FACT: re-derived independently, so the census is now confirmed once more by a different method
(`grep -n "rpc "` over the two service blocks plus `grep -n "SessionId "`): 31 RPCs with 31
distinct request types; 26 `SessionId session_id` declarations in the whole file and zero
declarations of `session_id` under another type or of `SessionId` under another name, so both
halves of the replacement gate's biconditional are green today; exactly two `oneof` blocks, at
`:1174` and `:1250`.
EVIDENCE: schemas/lenny-adapter.proto

FACT: `spec-changes.md` §5's claim that `:726` becomes the only `spec/` sentence classifying a
request message pod-scoped holds. `grep -rn "pod-scoped" spec/` outside the retired block returns
`spec/10:60` (the hold and its gauge), `spec/04:872` (the rotation in-flight ceiling), and
`spec/12:202` (Redis key prefixes); none classifies a request message.
EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60

CORRECTS [Standing context → Settled → "Open decisions moved."]: that bullet says "§7's single
surviving question is the tier-3 population question, which is summary OD2." Both halves are now
stale. `spec-changes.md` has no `## 7` section at all (the fix round deleted it), and the summary
carries OD1, OD4, and OD5 with no OD2. Nothing in any landing file references `§7` any more
(`grep -n "§7\|## 7\."` over the six non-log files returns nothing), so the deletion left no
dangling pointer.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:152-155

CORRECTS [Standing context → Open, "OD2 carries its question and its ground but no
recommendation"]: OD2 no longer exists. The successor entry with the same defect is OD5, which
ends "The review loop derived no recommendation; it recorded the drift as a standing risk on two
rounds." OD4 carries the same absence. Anyone acting on the old OD2 entry should retarget it.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:142-161

USEFUL [Traps: orchestrator briefs carry stale content]: the r1 spec-recheck brief still gives the
pre-refresh proto anchors (`:589 :1166 :1447 :1448`, each seven low), still says §1.2 asserts the
pod-wide ground holds (§1.2 already reads post-0076, citing `server.go:302` as `hold holdState`),
and still describes "§7 question 1" as the existential question after §7 was deleted. Four false
findings were available to anyone who reviewed the brief instead of the proposal.

USEFUL [Traps: long lines hide the sentences citations point at]: `spec/04:726` and `spec/10:60`
both hide their cited clause at the end of a very long line; `sed -n '726p' spec/04... | tr '|' '\n'`
recovers "The request is pod-scoped." that a truncated read drops.

FACT: OD4's two new citations verify. `spec/28_communication-channels.md:499` is the
`#### 28.5.3 Intra-pod` heading, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
(`tests/tier0_static/spec_map_slot_address_registration_test.go:970-989`) does credit from each
case's own `// spec:` annotation via `citedSectionsPerCase`. Their remedy is in `summary.md`, so
they were out of this loop's scope either way.
EVIDENCE: spec/28_communication-channels.md:499; tests/tier0_static/spec_map_slot_address_registration_test.go:970-989


### [spec-recheck.1.review-client-surface.1]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE the round's whole delta inside `spec-changes.md` is the deletion of the former `## 7. Open decisions for review` block, which removed text rather than adding a claim, and every client-facing parallel representation of the gateway-adapter wire contract is either untouched by SPEC-1 or already recorded as an unstaged pre-existing defect — ALTERNATIVES: filing `docs/api/internal.md:209-215` and `:272-274` (protobuf excerpts publishing a top-level `string session_id`, which after SPEC-1 illustrate a spelling the replacement gate refuses) and `docs/reference/adapter-contract.md:69`; both rejected because each is already false about the shipped proto before SPEC-1 applies, so neither becomes wrong as a consequence of the edits, and both already sit in the summary's `## Defects in the shipped tree that this proposal does not stage`.

FACT: the SDK trees carry no part of the gateway-adapter proto. `grep -rln "lenny.adapter.v1\|SessionId" sdks/` returns nothing across `sdks/client/{go,python,typescript}` and `sdks/runtime/{go,python,typescript}`, so a change to the §4.1 classification has no language-SDK parallel to mirror. This closes the largest branch of the client-surface lens in one command and is worth reusing rather than re-deriving. — EVIDENCE: `grep -rln "lenny.adapter.v1\|SessionId" sdks/` is empty

FACT: `docs/api/internal.md` carries no scope statement at all. `grep -n "scope\|Scope" docs/api/internal.md` returns nothing over its 566 lines, so the page is not a parallel representation of the §4.1 classification and its staleness is a separate (already-recorded) defect rather than a missed edit site for this proposal. — EVIDENCE: docs/api/internal.md (566 lines, no `scope` match)

FACT: `docs/reference/adapter-contract.md`'s gateway-to-adapter RPC table at `:59-75` lists 17 RPCs and omits `SendMessage`, `SignalDeadline`, `ExtendCredentialLease`, `RevokeCredentials`, `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`, all of which the two service blocks declare. That incompleteness is pre-existing, is not a scope statement, and no gate reads the table's membership, so SPEC-1 neither creates nor worsens it. Recorded so a later client-surface pass does not spend a round on it. — EVIDENCE: docs/reference/adapter-contract.md:57-75 against schemas/lenny-adapter.proto:32, :261

FACT: `SessionId` as a proto type name appears nowhere in `spec/` or `docs/` today (`grep -rn "\`SessionId\`\|SessionId session_id\|of type \`SessionId\`" spec/ docs/` is empty), so SPEC-1's staged block is the first place the specification names that wire type. No register or gate holds spec-side proto type names (`tests/claim-map.json` carries no scope row and the claim-register agreement gate keys on `Surface` strings naming `schemas/lenny-adapter.proto`, which SPEC-1 adds none of), so the introduction owes no bookkeeping. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:64-70

CORRECTS [Standing context, `### Settled` bullet at review-log.md:20]: it says "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That parenthetical is wrong in the current tree: `message ReportSessionScrubRequest {` is at `schemas/lenny-adapter.proto:456`, `string pod_id = 1;` at `:457`, and `SessionId session_id = 2;` at `:458`, which is exactly what SPEC-1 cites. `ReportPodScrubRequest`'s `:499-503` is also exact. A future round should not treat that citation as a known-tolerated inaccuracy. — EVIDENCE: schemas/lenny-adapter.proto:456-458, :499-503

USEFUL [Standing context `### Traps` bullets at review-log.md:66, :73, :74, :82, :83, :95]: the proto-parser trap, the "only way a request addresses a session" tension, the nested-address hole, the `CheckpointStart`-is-not-an-RPC-request-type count trap, and the D1 off-by-one reading each matched something I reached independently. The D1 arithmetic in particular reads as off by one (25 addressed request types plus `CheckpointStart` against 27 session rows after the fence moves) until the "The envelope clause carries the remaining two rows" sentence closes it; without that trap entry I would have spent the round deriving it.


### [spec-recheck.1.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens on the spec staging — BECAUSE the spec-lane delta since spec-r2 is only the deletion of `spec-changes.md` §7, and no surviving text in any landing file cites §7 (`grep -n "§7"` over the five landing files returns nothing), so the deletion strands no pointer; and independently re-derived, `docs/` carries no mirror of the §4.1 classification at all — ALTERNATIVES: filing `docs/api/internal.md:210` and `:272` as a missed edit site (rejected: both excerpts are already false about the shipped proto, the remedy is a docs edit outside this loop, and the summary records the page under `## Defects in the shipped tree that this proposal does not stage`); filing the pod-wide fence readings at `spec/04:712-713`, `spec/28:322-324`, and `docs/reference/adapter-contract.md:69` (rejected: pre-existing 0076 residues, already in the log's Deferred block, four lenses declined them).

USEFUL [Standing context, "No docs mirror"]: the bullet is exactly right and saved a full sweep. Re-verified rather than taken: `grep -rn "pod-scoped\|session-scoped" docs/` returns only JSONL-frame uses plus `docs/reference/adapter-contract.md:81`, and a per-message-name sweep over all 32 table rows returns hits only in `docs/api/internal.md` (`:90`, `:210`, `:99`, `:272`). — EVIDENCE: docs/reference/adapter-contract.md:81, docs/api/internal.md:210,272

FACT: the retirement strands no cross-reference and owes no anchor redirect. `grep -rn "Request Message Scope\|request-message-scope\|message scope\|message-scope" spec/ docs/ schemas/ charts/` returns one line, the heading itself. SPEC-1 keeps the heading. — EVIDENCE: spec/04_system-components.md:149

FACT: the one accepted failure mode in the staging lands in staged spec prose rather than in reasoning alone. The gate's blind spot ("a session addressed under a name and a type that are both unconventional") is the third staged paragraph, so the absence of an `Edge cases and accepted failure modes` section costs nothing under this lens. — EVIDENCE: 0075_....spec-changes.md:20-25

FACT: the tier-11 gate SPEC-1 cites really does hold identical wording on both carriers, and it reads `spec/04` §4.7 rather than §4.1, so the staged block cannot redden it. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` spans exactly `:43-76` and requires the literal opener "The request is session-scoped: it is " on both rows. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76, docs/reference/adapter-contract.md:81

WATCHOUT: `grep -rn "adapter_proto_message_scope" --include=*.md --include=*.json .` is noisy because `scratchpad/cp-snap/0076-run*/` holds dozens of snapshot copies of proposal 0076's review log. Filter out `scratchpad/` and `proposals/` or the real answer (three `tests/spec-map.json` lines) is buried. — EVIDENCE: tests/spec-map.json:156,169,5670

FACT: the cache key computed clean this run (`2d25f5f323c0`, no prior hit), so this shard is a real review rather than a replayed answer. The standing context's warning about cache collisions across lanes did not bite here.


### [spec-recheck.1.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE the only delta in `spec-changes.md` since the `spec-r2` snapshot is the deletion of `## 7. Open decisions for review` (its single tier-3-population question having moved to the summary as an answered OD), and a full independent re-sweep of the edit-site lens over the whole staging found no spec/, docs/, schemas/, or charts/ surface that SPEC-1 leaves wrong and that §9 omits — ALTERNATIVES: filing the "which protocol definition does the gate read" ambiguity in the staged constraint paragraph (declined: the block's own antecedent is "the gateway-adapter protocol", and the standing context already records the trap), and filing the "only way a request addresses a session" tension against the envelope paragraph (declined: standing context records it as refused).

FACT: re-derived the proto census from scratch with an independent parser rather than trusting the standing context, and it agrees exactly. 31 RPC request types across the two service blocks; 25 declare a top-level `SessionId session_id`; the 5 that declare no address are `AdapterEventsRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; `CheckpointRequest` is the sole `oneof`-carrying request. Zero name/type violations across all 26 `SessionId session_id` declarations, at any depth. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

CORRECTS [standing context, "Proto anchors, current and verified"]: the bullet says `ReportSessionScrubRequest` is at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong on the current tree: `:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`, so SPEC-1's `:458` is the correct anchor and nothing is off by one. A future agent should not "fix" SPEC-1's citation on the strength of that bullet.
EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the standing context's parser trap is real and cost me a pass. Two independent brace-counting parsers over `schemas/lenny-adapter.proto` returned 14 and 81 messages instead of 88, because `message SendMessageResponse {}` and `message ReportPodScrubResponse {}` open and close on one line. The parse that works is: anchor a block on `^message (\w+) \{\s*$` and close it on the next `^\}` at column zero, after stripping `//` comments. — EVIDENCE: schemas/lenny-adapter.proto:508, :984

FACT: independently re-confirmed the four edit-site facts the lens turns on, each by grep over the whole tree rather than by reading the standing context. Nothing links `#request-message-scope` or the heading text; `docs/reference/adapter-contract.md` carries no mirror of the table (its only scope sentences are `:81`, which the tier-11 gate SPEC-1 names already holds, and `:158`, a JSONL frame statement); the adapter proto's own doc comments carry no scope classification, so `schemas/` owes no edit; and neither `BUILD-GAPS.md` nor `TEST-GAPS.md` carries a message-scope finding that SPEC-1 would strand. — EVIDENCE: docs/reference/adapter-contract.md:81, :158; grep -rn "pod-scoped\|session-scoped" schemas/lenny-adapter.proto returns nothing

FACT: the two tier-0 files 0076 landed are clear of this blast radius, checked rather than assumed. `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go` both read proto doc-comment text; neither reads `spec/04` §4.1 and neither calls `protoFields` or `protoServiceRequests`. — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:11-25

FACT: `tests/tier0_static/address_rule_citation_test.go` is the one gate that could redden from a `spec/` edit this proposal makes, and the staged block is clean. `sectionStatingTheAddressRule` calls `t.Fatalf` unless exactly one numbered spec section carries the literal "rejected at the adapter boundary with `InvalidArgument`"; today that is `spec/05_runtime-registry-and-pool-model.md:515` alone, and the D1 block adds no second carrier. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22, :54-89

FACT: every file:line citation in `spec-changes.md` resolves and says what the proposal claims, re-verified one anchor at a time. `spec/10:57` (fence is the only exit from hold), `spec/10:60` (hold and gauge stay pod-scoped), both inside §10.1.4 which opens at `spec/10:53`; `spec/04:190` (`ShutdownRequest` precedent), `:725`/`:726` (§4.7.1, heading at `:692`); `spec/05:515`; `pkg/adapter/checkpoint.go:74-84`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`; `adapter_proto_parse_test.go:10-15`; `claim_register_proto_agreement_test.go:64`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`; `schemas/lenny-adapter.proto:458`, `:499-503`, `:1217`.

WATCHOUT: the orchestrator brief for this firing is stale in three ways a reader will trip on. It cites the pre-refresh proto anchors (`:589`, `:1166`, `:1447`, `:1448`), each seven lines low; it says §1.2 still claims the pod-wide ground holds, when the problem statement has already been rewritten to the post-0076 text; and it calls "§7 question 1" the existential question, when §7 is deleted and its surviving question was the tier-3 population one. The standing context already records this and it is still true. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.problem-statement.md:57-63

USEFUL [standing context, "Traps"]: the block pre-empted four candidate findings I had independently derived and would otherwise have filed — the "only way a request addresses a session" tension, the nested-address hole, the indefinite "a protocol definition", and the §4.7.1 "declared per message" tension. Each is recorded with the ground on which it was refused, and each cost an earlier round. This is the single highest-value section on the proposal for a lens like this one.



### [spec-recheck.1.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens over `spec-changes.md`. BECAUSE the only actor the staged spec block assigns an action to is "a tier-0 gate", and every one of its four refusals is mechanically evaluable from `schemas/lenny-adapter.proto` alone by the parse the tier-0 gates already share. ALTERNATIVES: considered filing the dangling "either service" referent (the two service names `Adapter` and `GatewayControl` survive in `spec/` only in the table rows SPEC-1 deletes, at `spec/04_system-components.md:180-186`, so the replacement block's "a request message either service declares" has no antecedent inside §4.1) — declined as documentation polish under the stated bar, since `GatewayControl` still appears at `spec/04:719`, `spec/05:459`, and `spec/28:1782` and "the gateway-adapter protocol" names both endpoints. A doc-content lens may reasonably see it differently.

FACT: the derivation rule and D2's replacement gate were re-derived from the proto with an independent brace-depth parser today, and every number the proposal states is exact. 88 top-level messages, 0 nested messages (only 4 nested enums, at `schemas/lenny-adapter.proto:543`, `:554`, `:1134`, `:1144`), 31 RPC request types, 6 declaring no top-level `SessionId session_id` (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`), ZERO convention violations in either direction over all 88 messages including `oneof` arms, and exactly two `oneof` blocks (`:1174` `CheckpointRequest`, `:1250` `CheckpointResponse`). The replacement gate is green on day one. EVIDENCE: schemas/lenny-adapter.proto:1174, :1250, :1217, :499-503

FACT: the absence of nested messages is what makes the shared text parse sufficient for the new gate. `protoFields` walks top-level messages with a brace counter (`tests/tier0_static/adapter_proto_parse_test.go:36-62`) and would miss a nested message's fields; none exists, and nested enum value lines (`NAME = N;`) do not match `protoField`, which requires a type token before the name. A future nested message would silently escape the convention check. EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:24, schemas/lenny-adapter.proto:543

WATCHOUT: `spec/04_system-components.md`'s §4.7.1 RPC tables do NOT carry every RPC. The Gateway→Adapter table omits `SendMessage`, `SignalDeadline`, `RevokeCredentials`, `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`; the Adapter→Gateway table carries only `ReportSessionScrub` and `ReportPodScrub`, omitting the five MCP-forwarding RPCs. So the standing context's line "direction survives in §4.7's two RPC tables" is only partly true: retiring the §4.1 table drops the service and direction columns for eleven request messages with no other spec carrier. This is information loss rather than an inconsistency, and OD1 already owns the retire-or-withdraw judgement, so it is not a finding — but do not repeat the standing-context claim as though it were complete. EVIDENCE: spec/04_system-components.md:698-726

FACT: `sessionScopedMessages` has exactly the three readers the proposal names and no reader outside its own file. The sibling `tests/tier3_contract/adapter_session_address/send_message_stamp_test.go` does not touch it, so §9's omission of that file is correct rather than a gap. EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:81, :102, :130

FACT: SPEC-1's citation `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s `SessionId session_id = 2` is EXACT. The standing context's "SPEC-1 cites :458, one line off" is wrong: `message ReportSessionScrubRequest {` opens at `:456`, `string pod_id = 1;` is `:457`, and the address is `:458`. EVIDENCE: schemas/lenny-adapter.proto:456-458

CORRECTS [standing context, "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest` sits at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". The address is at `:458` and SPEC-1 is right. A future round should not "fix" SPEC-1's anchor to `:457`.

FACT: the delta this loop exists for is one deletion. `diff -ru` against `spec-r2` shows `spec-changes.md` lost only `## 7. Open decisions for review` and its single remaining question; no other line changed. `grep -n "§7"` over the proposal directory (excluding the review log) returns nothing, so the deletion left no dangling intra-document pointer.

FACT: the one behavioural obligation the reclassification creates is already met and pinned, re-verified rather than taken from the log. `spec/05_runtime-registry-and-pool-model.md:515` refuses a session-scoped request with an empty identifier; `pkg/adapter/coordination.go:108-111` returns `InvalidArgument` before `boundSlotState`; `TestCoordinatorFenceRejectsMissingSessionID` at `pkg/adapter/coordination_test.go:35` asserts it. EVIDENCE: pkg/adapter/coordination.go:108-111

FACT: nothing in `tests/tier0_static/gate_integrity_test.go`'s `tierZeroGates` (`:63-89`) names the retiring gate, and the two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) contain no reference to §4.1, the table, or the shared parse. §9's file list is complete on the tier-0 side. EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-89


### [spec-recheck.1.review-fresh.1]

DECISION: returned an EMPTY findings list on the staged spec edits — BECAUSE the only delta since `spec-r2` is the deletion of the whole `## 7. Open decisions for review` section from `spec-changes.md` (its single tier-3 population question, which summary OD5 still carries), and every remaining claim in §2-§10 re-verified clean against the tree — ALTERNATIVES: I weighed and dropped the "either service" antecedent point (see WATCHOUT below) and the two residual-hole variants the Traps list already declines.

FACT: the deleted §7 leaves NO dangling reference anywhere in the proposal. `grep -n "§7\|section 7\|Open decisions" *.md` over the proposal folder, excluding the review log, returns only `summary.md:83` (`## Open decisions for human to make`), which is the summary's own section rather than a pointer into `spec-changes.md`. The staged file's headings are now 2, 3, 4, 5, 6, 9, 10 — a numbering gap, not a broken cross-reference. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:145,155

CORRECTS [Standing context, review-log.md:20]: the "Proto anchors" bullet says SPEC-1's `ReportSessionScrubRequest` citation is "one line off" (claiming the address sits at `:457`). It is not off. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`, which is exactly what SPEC-1 cites. Do not "fix" that citation to `:457`; that would introduce a false one. — EVIDENCE: schemas/lenny-adapter.proto:457-458 versus proposals/0075_.../0075_....spec-changes.md:136

WATCHOUT: after SPEC-1 lands, the staged paragraph 1's phrase "A request message **either service** declares" has no antecedent left inside §4.1. The retired table's `Service` column (`spec/04_system-components.md:153-186`) was the only place §4.1 named `Adapter` and `GatewayControl`, and grep confirms those two names then survive in `spec/` only at `spec/04:719`, `spec/05:459`, and `spec/28:1782`. I judged this BELOW the bar as wording rather than a defect (the block's own "the gateway-adapter protocol" antecedent carries the domain, and the retired `:151` used the same phrase), and I am recording it so the next agent does not spend a round re-deriving it. Do not file it without a stronger argument than clarity. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:9; spec/04_system-components.md:153-186

FACT: the envelope arm of the replacement gate IS buildable from the shared parse once TEST-1's stated extension lands, which closes review-log Open item at `:104`. `protoFields` folds `oneof` arms into the enclosing message's field set (`tests/tier0_static/adapter_proto_parse_test.go:36-62`), so with each field's type recorded, `CheckpointRequest`'s arm `start` carries type `CheckpointStart`, and `fields["CheckpointStart"]` is already in the same map because the adapter proto declares every message at the top level. No cross-file type resolution is needed. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:24,33-62; schemas/lenny-adapter.proto:1174-1178,1193-1218

FACT: the addressing convention holds in both directions with zero counterexamples, re-derived by two independent greps today. `grep -n "session_id\s*=\s*[0-9]"` and `grep -n "SessionId \w* *="` over `schemas/lenny-adapter.proto` return the SAME 26 lines, so no field named `session_id` carries another type and no field of type `SessionId` carries another name. The replacement gate is green on the shipped proto. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

USEFUL [Standing context Traps, review-log.md:66-98]: the thirty-item Traps list saved this round at least four separate derivations — the `AdapterEventsRequest` counterexample, the nested-address hole, the "only way a request addresses a session" tension, and the §4.7.1 declared-versus-derived tension were each reached independently and dropped on the recorded ground within a minute. Keep this section intact through compaction.

UNVERIFIED: nobody has executed tier 0 or tier 3 against the staged design; every green/red claim on this proposal, mine included, is derived from reading files. The implementor should run `lenny-test --tier 0` immediately after S2 rather than after S3.


### [spec-recheck.1.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the staged spec block touches only the gateway-adapter gRPC request-message classification in `spec/04_system-components.md` §4.1 and names no CRD, no status subresource, no controller, no finalizer, no admission webhook, and no reconcile path, so every idiom this lens owns (single-writer field-manager status ownership, status-as-observed-state, finalizer safety, CRD-as-message-bus, webhook coherence, level-triggered watches, controller-on-hot-path) has no surface here — ALTERNATIVES: filing on the §4.7.1 rows SPEC-1 leaves standing (`:725` `ReportSessionScrub`, `:726` `ReportPodScrub`), which do describe gateway writes to `agent_pod_state` and to `SandboxClaim.status`; rejected because SPEC-1 edits neither, both agree with the derived rule, and neither describes a non-owner writing another component's status.

FACT: the delta this recheck exists for is a deletion only. `diff -u` of `spec-changes.md` against the r2 snapshot shows one hunk: `## 7. Open decisions for review` and its single item (the tier-3 covered population) are removed, resolved into `non-spec-changes.md` §5 TEST-2. No staged spec sentence changed. — EVIDENCE: scratchpad/cp-snap/.../spec-r2/0075_....spec-changes.md vs proposals/0075_.../0075_....spec-changes.md

FACT: the orchestrator brief's line "This proposal's §7 question 1 asks whether the replacement gate closes the hole the retired table's gate closed" is stale on two counts: §7 no longer exists, and while it existed its only item was the tier-3 population question, not a gate-coverage question. Do not go looking for a withdrawal trigger in §7. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md (no `## 7`)

FACT: the derivation rule is green on the shipped protocol definition, checked mechanically rather than read. A brace-depth parse of `schemas/lenny-adapter.proto` over both service blocks returns 31 distinct RPC request messages, 25 declaring a top-level `SessionId session_id` and 6 declaring none (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`), and across every message in the file there is no field named `session_id` of another type and no field of type `SessionId` under another name. So the replacement tier-0 gate's four refusals all pass on day one. — EVIDENCE: schemas/lenny-adapter.proto

FACT: 25 addressed request messages + `CheckpointRequest` + `CheckpointStart` reconciles exactly to the retired table's 32 rows once `CoordinatorFenceRequest` moves to session under 0076's OD3 (20 Adapter session rows including the envelope pair, 5 Adapter pod rows, 6 GatewayControl session rows, 1 GatewayControl pod row). D1's "reproduces every classification the retired table carried" checks out arithmetically. — EVIDENCE: spec/04_system-components.md:153-186

FACT: `oneof` occurs at exactly two message-level sites in the protocol definition, `CheckpointRequest:1174` and `CheckpointResponse:1250`, so §4's claim that the envelope predicate selects `CheckpointRequest` and nothing else is verified rather than asserted. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

FACT: retiring the §4.1 table strands no cross-reference. `grep -rn "Request Message Scope\|message scope"` over `spec/`, `docs/`, `schemas/`, and `charts/` returns exactly one hit, the heading itself at `spec/04_system-components.md:149`. And after the retirement `:726` really is the only sentence in `spec/` classifying a request message pod-scoped: every other `pod-scoped` hit in `spec/` names a hold gauge, a rotation ceiling, or a Redis key rather than a request message. — EVIDENCE: spec/04_system-components.md:149, :726; spec/10_gateway-internals.md:60; spec/04_system-components.md:872; spec/12_storage-architecture.md:202

FACT: every K8s-adjacent anchor SPEC-1 rests on resolves as claimed. `spec/10_gateway-internals.md:57` states `CoordinatorFence` is "the only way to exit hold state"; `:60` states "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"; `spec/05_runtime-registry-and-pool-model.md:515` carries the empty-identifier `InvalidArgument` refusal; `pkg/adapter/checkpoint.go:74-84` enforces it on the opening frame. — EVIDENCE: spec/10_gateway-internals.md:57, :60; spec/05_runtime-registry-and-pool-model.md:515; pkg/adapter/checkpoint.go:74-84

WATCHOUT: the orchestrator brief's proto anchors are stale by 8 lines against this tree (`SessionId` at `:589`, `CoordinatorFenceRequest` at `:1447`/`:1448`). The tree has `message SessionId` at `:596`, `message CoordinatorFenceRequest` at `:1455`, and its `SessionId session_id = 1` at `:1456`, which is what the proposal cites. Trust the proposal's anchors over the brief's here. — EVIDENCE: schemas/lenny-adapter.proto:596, :1455-1456

UNVERIFIED: nobody in this lane has run tier 0 or tier 3; my proto measurement is a Python parse of the `.proto` text, not the shared Go parse the gate will use (`tests/tier0_static/adapter_proto_parse_test.go`). A field type or `oneof` membership the Go parse resolves differently is still possible and belongs to the non-spec loop.


### [spec-recheck.1.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE the only delta in `spec-changes.md` since the `spec-r2` snapshot is the deletion of `## 7. Open decisions for review` (its retire-or-withdraw and tier-3-population questions now live as summary OD1 and OD5), and a full end-to-end mechanism trace of the staged block found no defect — ALTERNATIVES: filing the "the addressing frame opens the stream" clause as an unchecked assertion (rejected: the classification does not depend on which frame opens the stream, and frame ordering is a protocol semantic no proto-text gate could see, so it is hypothetical hardening); filing the ShutdownRequest paragraph's closing clause "neither operation is selected by a field's presence standing in for a scope" as drifting against a rule that now derives scope from field presence (rejected: its subject is the handler's operation selection, not the message's classification, and the §4.7.1 "declared per message" variant of this tension has been declined six times already).

FACT: no dangling `§7` reference survives the deletion. `grep -n "§7"` over every proposal file except the review log returns nothing — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/ (all `*.md`)

FACT: every gate refusal the staged block states can actually fire, and none is vacuous. (a) `session_id` under another type, (b) `SessionId` under another name, (c) an envelope with a top-level address, (d) an envelope whose frames declare zero or two addresses are each satisfiable by a legal proto edit, and all four are green on the shipped tree — EVIDENCE: schemas/lenny-adapter.proto — 26 `SessionId session_id` declarations and no other spelling of either half; the only two `oneof` blocks are `CheckpointRequest` :1174 and `CheckpointResponse` :1250.

FACT: no runtime code keys on the message-scope classification, so the fence's pod→session move has no code-side write path that could bypass the derivation. `grep -rn "podScoped\|sessionScoped\|PodScoped\|SessionScoped" pkg/ cmd/` returns only erasure orderings, admission data-residency tests, and a sessionserver test helper — EVIDENCE: pkg/gateway/storage/erasure/erasure.go:58-60, pkg/admission/data_residency_validator/validator_test.go:167

FACT: predicate agreement across the four carriers of the gate's clause set is exact — the staged spec block, D2, the summary's "What changes" bullet, and non-spec `## 8. Testing` state the same four refusals with the same conjuncts and no drift — EVIDENCE: 0075_...spec-changes.md:20-25 and :39-42, 0075_...summary.md:21-24, 0075_...non-spec-changes.md:103-107

USEFUL [Standing context / Traps]: the trap list saved a full round. Five of the six candidates my lens surfaced independently (the "only way a request addresses a session" tension, the nested-address hole, the §4.7.1 declared-per-message tension, `AdapterEventsRequest` as a counterexample, and the session-scoped fence exiting a pod-wide hold) are each recorded there with the ground on which they were declined, three to six times each — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md `## Standing context` → `### Traps`

USEFUL [Standing context / "Orchestrator briefs carry stale content"]: confirmed again this round. The brief's proto anchors (`:589`, `:1166`, `:1447`, `:1448`) are each seven low; the tree has `message SessionId` at `schemas/lenny-adapter.proto:596`, `CheckpointRequest` at `:1173`, `CoordinatorFenceRequest` at `:1455` with its address at `:1456`. The brief also still describes "§7 question 1" as the existential question after §7 was deleted. The proposal's own anchors are the correct ones.


### [spec-recheck.1.review-operational.1]

DECISION: returned zero findings on the operational lens — BECAUSE the staged spec edits touch no condition writer, no metric, no alert, and no operator-facing page, and every operational citation SPEC-1 makes resolves — ALTERNATIVES: considered filing spec/04:712 (`CoordinatorFence` row, "Announce new `coordination_generation` to the pod") as newly contradicted once §4.1 derives the fence session-scoped; rejected because :712 states the handler's recipient rather than the request's address class, and `spec/04:190`'s `ShutdownRequest` paragraph is the standing precedent that the two may diverge. It is already false about the tree (0076 residue), so SPEC-1 does not make it wrong.

FACT: the whole observability surface is clean for this proposal. `lenny_adapter_coordinator_hold` is inventoried at `spec/16_observability.md:185` and `docs/reference/metrics.md:309` with no scope word, `pkg/alerting/rules/rules.go` declares no alert on it, and `docs/runbooks/coordinator-handoff-slow.md` is the delegation handoff rather than the coordinator hold. No metric, alert, or runbook is keyed on request-message scope. — EVIDENCE: spec/16_observability.md:185, docs/reference/metrics.md:309, docs/runbooks/index.md:95

FACT: no spec-side register owes a row for the staged rule. `tests/tier0_static/claim_register_test.go:23-27` scopes the §28.4 claim register to "every normative statement §28 makes about a mechanism", so a §4.1 rule stating a tier-0 gate needs no claim row; and `tierZeroGates` (`tests/tier0_static/gate_integrity_test.go:63-92`) is the migration's own fixed list and does not carry the retiring gate, so retiring it owes no edit there. Both were checked from scratch this round. — EVIDENCE: tests/tier0_static/claim_register_test.go:23, tests/tier0_static/gate_integrity_test.go:63

CORRECTS [standing-context: Proto anchors bullet]: the standing context says `ReportSessionScrubRequest` is at `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong in the other direction: `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` citation is exact, and there is no off-by-one to carry forward. — EVIDENCE: schemas/lenny-adapter.proto:457-458

USEFUL [standing-context: Traps]: the "Long lines hide the sentences citations point at" trap saved a wrong finding. `spec/04:725`, `:726`, `spec/10:60`, and `spec/05:515` are each one very long line whose cited clause is the last sentence; `sed -n Np | cut -c1-N` shows none of it. Piping a table row through `tr '|' '\n' | tail -3`, and prose through `fold -w 150`, is what made each citation verifiable.

FACT: the tier-11 gate SPEC-1 cites is real and is what keeps §4.7.1's two surviving per-message scope statements out of the edit list. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` (`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`) requires both `spec/04:725` and `docs/reference/adapter-contract.md:81` to carry the identical opener "The request is session-scoped: it is " plus the shared rule constant. Any future round that proposes to reword `:725` must move the doc row and the gate constant in the same change. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76, docs/reference/adapter-contract.md:81

FACT: after SPEC-1 retires `:151`, `:175`, and `:188`, a repo-wide grep for "pod-scoped" over `spec/ docs/ schemas/ charts/` leaves exactly one sentence classifying a request message pod-scoped (`spec/04:726`), one about the hold and its gauge (`spec/10:60`), and two unrelated hits (`spec/04:872` rotation ceiling, `spec/12:202` tenant prefix, plus `docs/runbooks/dual-store-unavailable.md:68` "gateway-pod-scoped"). SPEC-1's claim that `:726` becomes the only such sentence is exact. — EVIDENCE: spec/04_system-components.md:726, spec/10_gateway-internals.md:60

WATCHOUT: the spec-lane delta this round is one deletion only — the old `## 7. Open decisions for review` block left `spec-changes.md`. Nothing else in that file changed. A reviewer who spends the round on the delta finds nothing; the value is in re-verifying SPEC-1's operational citations, which is what this entry records as done. — EVIDENCE: diff of scratchpad/cp-snap/.../spec-r2 against the live directory


### [spec-recheck.1.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the staged spec edits create no store write, no key, no watch, and no reconcile trigger, so there is no capacity-tier arithmetic to state and no failure path that degrades. ALTERNATIVES: re-filing the spec/10:39 same-generation-retry item and the missing coordfence backoff, rejected because both are pre-existing, §6 assigns the fence's acceptance predicate to 0080 §1.16, and SPEC-1 touches neither spec/10 nor anything that makes them newly wrong.

FACT: the spec-lane delta this round is one deletion. `diff -u` of the spec-r2 snapshot against the live `spec-changes.md` shows only the removal of `## 7. Open decisions for review` and its single tier-3-population question. D1-D6, §3, §4, SPEC-1, §6, §9, and §10 are byte-identical to the text the previous spec round converged on. — EVIDENCE: scratchpad/cp-snap/0075_.../spec-r2/0075_....spec-changes.md vs proposals/0075_.../0075_....spec-changes.md

FACT: the performance lens has no subject on this proposal and this is now settled rather than a judgement call. The staged block states a classification predicate, an envelope clause, and a tier-0 gate constraint. Nothing in it names a store, a queue, a leader, or a rate. The classification's runtime consequence, the empty-identifier refusal at `spec/05_runtime-registry-and-pool-model.md:515`, is already executed before any resolve at `pkg/adapter/coordination.go:109-111`, so the derived class adds no per-request work. A future lens firing here should spend its budget on the tier-0/tier-3 lane rather than re-deriving this. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515, pkg/adapter/coordination.go:109-111

FACT: `spec/10` §10.1.4 already reads per-session post-0076 and needs nothing from SPEC-1. `:38` states the pod "records the new generation for that session", `:40` that "The pod accepts only RPCs whose generation matches the value it holds for the session the RPC names", and `:60` that the hold gauge event "carries no generation, because the fenced generation is held per bound session and no pod-level fenced generation remains". The pod-scoped hold at `:57` and the session-scoped fence coexist in the shipped text; the failure path SPEC-1 documents is the one already running. — EVIDENCE: spec/10_gateway-internals.md:38, :40, :57, :60

USEFUL [standing context, Traps]: the "do not re-file the session-scoped fence exiting a pod-wide hold" trap and the "spec/10:39 / coordfence backoff" Open together cover the whole surface this lens would otherwise have re-derived from scratch. They saved a full pass over `pkg/gateway/coordination/coordfence`.

WATCHOUT: the cache key still omits `summary.md`, so a summary-only edit between rounds is served this round's empty answer. The key for this text is `2d25f5f323c0`. If a later round changes only the summary and this lens returns empty in under a minute, it is a replay rather than a review. — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r1-2d25f5f323c0.json


### [spec-recheck.1.review-reliability.1]

DECISION: returned zero findings on the spec staging — BECAUSE the only reliability surface the staged text touches is the fence reclassification, and every recovery-path consequence of it is either already stated per-session elsewhere in `spec/` or explicitly fenced off as another proposal's subject — ALTERNATIVES: filing the "session-scoped fence exits a pod-wide hold" tension (the standing context's Traps records four lenses reaching and declining it, and D3 plus §6 make re-deriving 0076's OD3 a non-goal); filing the fence-retry acceptance predicate (see FACT below); filing that the derived rule silently classifies a future session-scoped message as pod-scoped when its author omits the address (speculative future-authoring, no runtime mechanism breaks, and the retire-or-withdraw question that owns it is summary OD1).

FACT: the reliability lens's one real hook into this proposal is the fence-retry acceptance predicate, and it is correctly owned elsewhere. `spec/10_gateway-internals.md:39` orders a retry "with the same generation value (up to 3 attempts)", while `CoordinatorFenceResponse`'s own comment says `accepted` is false "when the supplied generation is not greater than the generation the pod holds", so a fence whose ack is lost is refused on retry. `spec-changes.md:151-152` lists this as a non-goal and points at proposal 0080 §1.16, which resolves: the heading "### 1.16 The pod refuses the fence retry §10.1.2 orders" — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:216

FACT: `spec/28_communication-channels.md:314-317` already states the fence is per-session ("The pod records the generation against the session the fence names... A fence for one session does not change the generation the pod holds for another"), and `spec/10_gateway-internals.md:40` holds `last_fenced_generation` "per bound session". So SPEC-1's reclassification removes a contradiction from the recovery description rather than creating one; there is no §28 or §10 edit site owed on scope grounds — EVIDENCE: spec/28_communication-channels.md:314, spec/10_gateway-internals.md:40

FACT: the delta this recheck exists for is a deletion only. `diff -u` of `spec-changes.md` against the `spec-r2` snapshot removes `## 7. Open decisions for review` and its single question and changes nothing else. No new spec text was written this round, so the fixer-introduced-error risk this lane warns about does not apply to this delta — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:145-155

USEFUL [standing context / Traps]: "Do not re-file the session-scoped fence exiting a pod-wide hold" and "Do not re-file `AdapterEventsRequest` as a counterexample" each saved a full derivation; both were the first two places this lens went. The Traps section is worth reading before any repository work on this proposal, not after.


### [spec-recheck.1.review-security.2]

DECISION: returned an EMPTY findings list, the sixth consecutive empty security pass on this proposal — BECAUSE the entire delta to the staged spec edits since `spec-r2` is the deletion of `## 7. Open decisions for review`, which removed no normative text and dangled no reference, and both lens checks re-derived clean from the tree — ALTERNATIVES: filing the retired table-reconciliation gate as a removed control (rejected: the removal is disclosed twice, once in normative staged spec text at spec-changes.md:20-25 and once in summary OD1 at summary.md:93-99); filing the fail-open unconventional-spelling residual (rejected: same disclosure, and OD1 prices it); filing the session-scoped fence exiting a pod-wide hold (rejected: standing-context trap, 0076 OD3 territory).

FACT: the §7 deletion is inert. `grep -n "§7\|section 7\|Open decisions for review" *.md` over the non-log proposal files returns nothing, so no surviving sentence in the problem statement, the summary, the checklist, or the non-spec staging points at the deleted section. A future round need not re-check this. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md (no §7)

FACT: no runtime control keys on the §4.1 scope class, re-derived rather than replayed. `coordinatorHoldAllowedMethods` is a map of five gRPC full-method strings (`pkg/adapter/holdstate.go:52-58`) read at `:336` (unary) and `:349` (stream), so retiring the declared table cannot widen or narrow the hold-state allowlist by one method. This is the single fact that closes the only plausible security regression on this proposal and it has now held on four independent derivations. — EVIDENCE: pkg/adapter/holdstate.go:52-58, :336, :349

FACT: the scope column of the retired table, extracted mechanically rather than read, is 26 session rows and 6 pod rows, the pod six being AdapterEventsRequest, CoordinatorFenceRequest, DemoteSDKRequest, GetObservedIntegrationLevelRequest, NegotiateVersionRequest, ReportPodScrubRequest. Only the fence declares a top-level `SessionId session_id`, so the derived session class is a strict superset of the declared one with a delta of exactly one message, and the `spec/05:515` empty-identifier refusal can only be added. Command: `awk 'NR>=153 && NR<=186' spec/04_system-components.md | awk -F'|' 'NF>3 {gsub(/ /,"",$5); gsub(/`/,"",$2); print $5, $2}' | sort`. — EVIDENCE: spec/04_system-components.md:153-186; schemas/lenny-adapter.proto:1455-1456

FACT: no adapter-leg RPC carries a session address in gRPC metadata. That matters because paragraph 1 of the staged block asserts a top-level `SessionId session_id` is "the only way a request on this protocol addresses a session"; a metadata-borne address would falsify it and leave an addressing path outside the derivation. A grep of `spec/04` and `spec/28` for metadata alongside session or gRPC returns only session-record metadata, interceptor inputs, and credential rotation mode. — EVIDENCE: spec/04_system-components.md:194, :1120-1122

FACT: five `// spec: 4.1 (request message scope)` annotations sit in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` (`:26`, `:132`, `:408`, `:582`, `:747`) and one in `tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99`. None reads the table; each cites the subsection, which SPEC-1 keeps by name and heading. They are NOT missed edit sites and none belongs in §9. A future edit-sites or security lens that greps for "message scope" hits them first. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26; tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99

WATCHOUT: `tests/tier0_static/adapter_proto_parse_test.go:13` describes the shared parse in its header comment as joining "the message-scope classification table to the request messages the two services declare". SPEC-1 falsifies that comment. It is not a missed edit site, because §9 already lists that file as touched, but an implementor who extends the parse without rewriting its header leaves a doc comment that names a table the specification no longer carries. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:13; proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:160-161

WATCHOUT: the cache key for this run is `2d25f5f323c0` and it still omits `summary.md`. The fail-open residual this lens must weigh is disclosed in TWO places, the staged spec block and summary OD1. A later pass that rewrites OD1 alone changes no cache input, so a security lens would be served this empty answer without re-reading the disclosure it rests on. Re-read summary.md:83-160 by hand before trusting a hit. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:93-99

USEFUL [non-spec.5.review-security.1]: its DECISION framing, that check (1) finds one control removed openly and check (2) finds no security bound anywhere in the staging, is the correct decomposition of this proposal for this lens and survived a full independent re-derivation. A seventh pass can spot-check the two FACTs above rather than rebuild the argument.

USEFUL [spec.4.review-security.1]: the strict-superset fact remains the load-bearing one. It is what makes the whole retirement fail-closed-safe, and it is cheap to re-verify with the one-line awk above.

OPEN: nothing new. The security lens has now returned empty on six consecutive rounds across both lanes. Re-run it only if a fix pass edits the third staged paragraph (the gate's clauses and its blind spot), summary OD1's residual statement, or any sentence tying a scope class to a refusal.



### [spec-recheck.2.review-applicability.2]

DECISION: returned an empty findings list for the applicability/sequencing lens over the staged spec edits — BECAUSE the delta this loop exists for is a single deletion (the whole `## 7. Open decisions for review` section left `spec-changes.md`; `diff -u scratchpad/cp-snap/.../spec-r2/...spec-changes.md` against the live file shows that hunk and nothing else), and it strands nothing: no surviving line in `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`, or `problem-statement.md` references a `§7`, and the two `§7` consumers named in the standing context (summary OD1, OD2) live in `summary.md`, outside this loop's editable set. Re-walked the whole staging anyway: SPEC-1's four anchors resolve uniquely and verbatim, the replacement text is quoted in full in D1 so nothing is left to author, no edit references an artifact a later sub-step creates, and no created spec artifact exists at all (SPEC-1 adds no heading, anchor, section number, identifier, or register key). ALTERNATIVES: considered filing the "either service" antecedent, which the block reuses from the retired `:151` even though the Service column that gave it force is deleted (rejected: text is quoted verbatim, so the edit is deterministic — an edit-sites/content question already declined at `[spec.1.review-edit-sites.1]`); and considered filing the staged block's reference to "A tier-0 gate" that only S2 builds as a class-1 forward reference (rejected: spec prose naming a gate does not make the S1 edit unappliable, S1's own line records the tier-0 red window, and `spec/28:64`/`spec/18:85` are precedents).

FACT: `spec-changes.md`'s live section set is §2, §3, §4, §5, §6, §9, §10 — there is no §1, §7, §8, or §11 in that file. `§1.2` (cited three times, at `:52`, `:68`, `:115`) resolves into `problem-statement.md:46`, and `§8 Testing` lives in `non-spec-changes.md:99`. A lens that checks "the Testing section is absent" must look in `non-spec-changes.md`, not here. EVIDENCE: 0075_....spec-changes.md:52,68,115; 0075_....problem-statement.md:46; 0075_....non-spec-changes.md:99

FACT: independently re-verified, one anchor at a time, the five citations SPEC-1 and §4 turn on that a stale brief could have moved: `spec/04_system-components.md:149` is `#### Request Message Scope`, `:151` the introducing paragraph, `:153-186` the 32-row table (6 pod rows: `:175`, `:176`, `:177`, `:178`, `:179`, `:186`), `:188` the grounding paragraph, `:190` the `ShutdownRequest` precedent, `### 4.2` at `:192`; `spec/10_gateway-internals.md:57` carries "which is the only way to exit hold state" and `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2;` inside `ReportSessionScrubRequest` (opened `:456`), `:499-503` is the whole of `ReportPodScrubRequest`, and `:1217` is `SessionId session_id = 7;` inside `CheckpointStart`. Every one is exact. The standing context's parenthetical that SPEC-1's `:458` is "one line off" was already corrected at `[spec.1.review-applicability.1]`; this is a second independent confirmation that `:458` is right.

FACT: the biconditional the staged gate paragraph states is green on the shipped proto by direct count, not by inheritance: `grep -c 'SessionId session_id *= *[0-9]' schemas/lenny-adapter.proto` returns 26, and both violation greps (a `session_id`-named field of another type, a `SessionId`-typed field under another name) return nothing. The only `oneof` blocks are `schemas/lenny-adapter.proto:1174` and `:1250`. EVIDENCE: schemas/lenny-adapter.proto:1174,1250

FACT: the deleted `:149-191` block contains no occurrence of the word "channel", so `tests/registers/identifier-senses.yaml`'s eight `spec/04_system-components.md` rows — which key on the 1-based occurrence index of a retired channel spelling within the file — cannot shift when the block is removed. This is the one register in the tree whose keys are positional inside `spec/04`, so it was the only candidate for a silent renumbering break. EVIDENCE: tests/registers/identifier-senses.yaml:13-36

FACT: `tests/tier0_static/adapter_proto_event_taxonomy_test.go` is the only tier-0 file besides the retiring gate that names `spec/04_system-components.md` (`:34`), and its subject is `§4.7.3` (`:37`) read out of proto doc comments. It never opens the spec file. Nothing in tier 0 or tier 11 reads the `#### Request Message Scope` block except the gate TEST-1 replaces. EVIDENCE: tests/tier0_static/adapter_proto_event_taxonomy_test.go:34,37

USEFUL [Traps, "Snapshot diffs are routinely empty"]: `diff -ru` against `spec-recheck-r2` printed nothing, exactly as the trap predicts, and the trap named `spec-r2` as the snapshot to use instead. That saved a round of assuming a broken snapshot. The real delta was one deleted section.

USEFUL [Traps, "Orchestrator briefs carry stale content"]: this run's brief again gave the pre-refresh proto anchors (`:589`, `:1447`, `:1448`, `:1166`) and again called "§7 question 1" the existential question, when §7 no longer exists in `spec-changes.md`. The proposal's own anchors were right in every case.


### [spec-recheck.2.review-citations.1]

FACT: every concrete citation in the staged spec file resolves correctly as of 2026-09-07. Re-verified all of: spec/04_system-components.md :149 heading, :151, :153-186, :175, :188, :190, :712, :725, :726; spec/10_gateway-internals.md :57, :60; spec/05_runtime-registry-and-pool-model.md :515; schemas/lenny-adapter.proto :133, :458, :499-503, :596, :1174, :1217, :1250, :1455-1456; pkg/adapter/checkpoint.go:74-84; pkg/adapter/server.go:302, slot.go:59, coordination.go:17-38/:107/:116; tests/tier0_static/adapter_proto_message_scope_test.go:17-27 and :75-81; tests/tier0_static/adapter_proto_parse_test.go:10-15; tests/tier0_static/claim_register_proto_agreement_test.go:64; tests/spec-map.json :156, :169, :5670; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; docs/reference/adapter-contract.md:81. EVIDENCE: spec/04_system-components.md:151

CORRECTS [orchestrator brief]: two anchors in the orchestrator's own notes are drifted and the proposal's are right. `message SessionId` is at schemas/lenny-adapter.proto:596, not :589, and `CoordinatorFenceRequest` opens at :1455 with `SessionId session_id = 1` at :1456, not :1447/:1448. The proposal's problem-statement.md:38 and :48 already carry the correct numbers. Do not "fix" the proposal toward the brief. EVIDENCE: schemas/lenny-adapter.proto:596, schemas/lenny-adapter.proto:1455

CORRECTS [orchestrator brief]: the brief says "This proposal's Section 7 question 1 asks whether the replacement gate closes the hole the retired table's gate closed". That section no longer exists. The only delta in spec-changes.md since the spec-r2 snapshot is the deletion of the whole `## 7. Open decisions for review` block; the question now lives in summary.md `## Open decisions for human to make` as OD1, which this review may not file on. EVIDENCE: proposals/0075_.../0075_....summary.md:83

FACT: the proto census re-derives exactly as the proposal states, on an independent parse written from scratch. 31 RPC request types across the two service blocks, 25 declaring a top-level `session_id`, and the 6 that do not are AdapterEventsRequest, CheckpointRequest, DemoteSDKRequest, GetObservedIntegrationLevelRequest, NegotiateVersionRequest, ReportPodScrubRequest. The addressing convention holds in both directions: 26 `SessionId session_id` declarations, zero `SessionId` fields under another name, zero `session_id` fields of another type. D1's arithmetic (24 non-envelope session rows + the fence = 25; 5 pod rows + the envelope = 6) reconciles against the 32-row table. EVIDENCE: schemas/lenny-adapter.proto:596

FACT: nothing in the tree outside the tier-0 gate reads the retired table's text, and nothing links to the `#### Request Message Scope` heading. `grep -rln "Request Message Scope|declared in the table below|appears on messages of both classes|carries \`session_id\` and stays pod-scoped" tests/ scripts/ pkg/ cmd/ docs/` returns tests/tier0_static/adapter_proto_message_scope_test.go alone, which Section 9 already lists. The two sibling tier-0 files 0076 landed do not read spec/04 Section 4.1: adapter_proto_generation_scope_test.go and adapter_barrier_doc_comment_scope_test.go carry no spec/04 anchor at all, and adapter_proto_event_taxonomy_test.go anchors on Section 4.7.3. So Section 9's file list is complete on the spec side; do not re-derive this. EVIDENCE: tests/tier0_static/adapter_proto_event_taxonomy_test.go:37

FACT: after SPEC-1 lands, spec/04_system-components.md:726 really is the only sentence left in spec/ that classifies one request message pod-scoped. The other `pod-scoped` hits are spec/10:60 (the hold and its gauge), spec/04:872 (the rotation ceiling), and spec/12:202 (Redis key scoping), none of which classify a request message. SPEC-1's claim checks out. EVIDENCE: spec/04_system-components.md:726

USEFUL [review-log standing context]: the Settled block's proto census, addressing-convention count, and "the only two oneof blocks" entries all held against a fresh derivation. Reading them first turned three separate derivations into one confirmation pass.

DECISION: returned an empty findings list BECAUSE every citation in the staged spec file resolved to text saying what the proposal claims, and the two candidates I weighed did not clear the bar. ALTERNATIVES: (1) D1's sentence at spec-changes.md:27-29, "the ones other than the stream envelope that carry the address are exactly the table's session rows ... and the ones that carry none are exactly its pod rows" — read without carrying the "other than the stream envelope" restriction into the second clause, the second clause is false of CheckpointRequest, which carries no address and is a session row. Rejected because the very next sentence (:30-32) names the envelope clause as carrying CheckpointRequest and CheckpointStart, so the restriction is unambiguous in context, and this is rationale wording rather than staged spec text, which is the exact ground the material skeptic used to refute the D2 wording finding. (2) The staged third paragraph states one blind spot ("a session addressed under a name and a type that are both unconventional") while the gate also cannot check the envelope paragraph's "it opens the stream" clause. Rejected because the envelope's derived scope does not depend on which frame opens the stream, only on which frame declares the address, so the unchecked clause is descriptive and the derivation stays sound without it.

WATCHOUT: `spec/04_system-components.md:725` and `:726` state per-message scope in prose and SPEC-1 deliberately leaves both standing, while the new Section 4.1 text says the classification is "derived from the message's field set rather than declared per message". This reads like a contradiction on a first pass and is not one: both sentences agree with the derivation, and :725's exact wording is held by a tier-11 gate on two carriers. SPEC-1 already reasons this through at spec-changes.md:131-142. Do not file it. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67


### [spec-recheck.2.review-client-surface.1]

DECISION: Returned zero findings under the client-facing-surface lens — BECAUSE the staged spec edit
touches one internal classification block in `spec/04` §4.1 and no parallel client representation carries
that classification: `docs/` names RPCs and never request message types (`docs/reference/adapter-contract.md:69`
gives `CoordinatorFence` a purpose with no scope word), `grep -rn CoordinatorFence sdks/ charts/ schemas/*.json`
returns nothing, no CRD, OpenAPI, MCP tool schema, JSONL schema, or SDK type file mentions message scope, and
the only per-message scope statements outside the retired table are `spec/04:725`/`:726`, both of which agree
with the derivation and are staged as survivors — ALTERNATIVES: filing `docs/api/internal.md:209-215` (its
`CheckpointRequest` excerpt is false about the shipped proto) — rejected, it is already false before the edit
and its fix lands in docs, which this loop may not edit; it is already on the standing-context Deferred list.

FACT: the whole delta since the lane's last converged review is the deletion of `## 7. Open decisions for
review` from `spec-changes.md`; the staged block (D1's quoted paragraphs, D2–D6, §3, §4, SPEC-1, §6, §9, §10)
is byte-identical to `spec-r2`. `diff -u scratchpad/cp-snap/.../spec-r2/...spec-changes.md
proposals/.../...spec-changes.md` shows exactly one hunk. The `spec-recheck-r2` snapshot is byte-identical to
the live directory, so diffing against it prints nothing. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:145-155

FACT: every citation in `spec-changes.md` re-verified today at HEAD: `spec/04_system-components.md:151`
(introducing paragraph), `:153-186` (32-row table, header at `:153`), `:175` (fence row, scope `pod`), `:188`
(grounding sentence), `:190` (`ShutdownRequest`), `:725`/`:726` (§4.7.1 spans `:692-727`, so the `§4.7.1`
attribution is right), `schemas/lenny-adapter.proto:458` (`SessionId session_id = 2` in
`ReportSessionScrubRequest`, whose `message` line is `:456`), `:499-503`, `:1217` (`SessionId session_id = 7`
in `CheckpointStart`), `spec/05_runtime-registry-and-pool-model.md:515`, `spec/10_gateway-internals.md:57`
and `:60`, `pkg/adapter/checkpoint.go:74-84`, `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`,
`tests/tier0_static/adapter_proto_parse_test.go:10-15`, `tests/tier0_static/claim_register_proto_agreement_test.go:64`,
`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` (function opens at `:43`),
`docs/reference/adapter-contract.md:81`. No citation is off. — EVIDENCE: spec/04_system-components.md:692,725,726

CORRECTS [standing context, Settled bullet "Proto anchors, current and verified"]: it says
"`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the
bar)". That is wrong. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and
`:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact and no allowance is needed. Cost: one
re-derivation. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the addressing convention the rule rests on holds absolutely in `schemas/lenny-adapter.proto`, by a
brace-tracked parse of every message including nested ones and `oneof` arms: zero fields named `session_id`
carry another type and zero fields typed `SessionId` carry another name. `grep -n SessionId` minus comment
lines minus `SessionId session_id` leaves only `message SessionId {` at `:596`. The two `oneof` blocks are
`CheckpointRequest` and `CheckpointResponse`, and the RPC-request census is 31 types, 25 with a top-level
address, the 6 without being `CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`,
`GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`. Independently
reproduced; do not re-derive again. — EVIDENCE: schemas/lenny-adapter.proto:596, :1174, :1250

USEFUL [standing context, Traps]: "Do not re-file the §4.7.1 'declared per message' tension", "Do not
re-file the nested-address hole", and the "a protocol definition is indefinite" WATCHOUT each named the
exact three places my lens would otherwise have stopped on. Each is correctly judged below the bar and I
reached the same place independently on the first two.

FACT: the proto file itself carries no comment restating the §4.1 table or the fence's class.
`CoordinatorFenceRequest`'s doc comment (`schemas/lenny-adapter.proto:1449-1454`) already reads per-session
("records the generation against the session the fence names"), so the wire contract's own prose needs no
edit when the table retires. The only `4.1` reference inside the proto is `:645`, which names the §4.1 Upload
Handler subsystem and is unrelated. — EVIDENCE: schemas/lenny-adapter.proto:1449-1456


### [spec-recheck.2.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens on the spec staging — BECAUSE the staged §4.1 block has no `docs/` mirror to fall out of step with, and every docs-side residue this lens would otherwise raise is already recorded under `## Deferred` in the review log and is out of this loop's scope (spec-staging fixes only) — ALTERNATIVES: filing `docs/api/internal.md:209-215` (the stale `CheckpointRequest { string session_id = 1; ... }` excerpt) as a docs edit site, rejected because its remedy is a docs edit and standing-context Deferred entry already assigns it to the docs loop or 0080.

CORRECTS [Standing context, "Proto anchors, current and verified"]: the parenthetical "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)" is wrong. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` citation is exact, and a future round should not "correct" it toward `:457`. EVIDENCE: schemas/lenny-adapter.proto:456-458.

FACT: the whole docs-alignment surface of this proposal is empty and the check is cheap to redo: `grep -rn "pod-scoped" docs/` returns one unrelated hit (`docs/runbooks/dual-store-unavailable.md:68`, "gateway-pod-scoped"), no `docs/` page names a gRPC request message type except the already-stale `docs/api/internal.md`, and `grep -rn "CoordinatorFence" docs/` returns only `docs/reference/adapter-contract.md:69`, which carries no scope word. EVIDENCE: docs/reference/adapter-contract.md:69, docs/runbooks/dual-store-unavailable.md:68.

FACT: the two spec-side sentences whose wording a tier-11 gate holds against `docs/` are `spec/04_system-components.md:725` (`ReportSessionScrub` … "The request is session-scoped: it is addressed by the identifier of the released session and names no slot.") and its twin at `docs/reference/adapter-contract.md:81`; the gate compares them literally, including the opener string. SPEC-1 leaves both alone, so the docs leg stays green. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76.

USEFUL [Standing context, Traps, "Long lines hide the sentences citations point at"]: `spec/04:725`, `:726`, `spec/10:60`, and `spec/05:515` are each one very long line and every citation into them looks wrong under a truncated read. Grepping the sentence rather than `sed | cut` verified four citations in one pass.

USEFUL [Standing context, "No docs mirror" and "No inbound reference to the table"]: these two entries are correct and re-verified today; they collapse this lens's whole mirroring check to a handful of greps.


### [spec-recheck.2.review-edit-sites.1]

DECISION: Returned an empty findings list — BECAUSE the staged spec block adds, changes, and removes no identifier at all (no metric, alert, flag, Helm value, condition type, error string, yaml key, or field name; the only names it uses are `session_id`, `SessionId`, and `oneof`, all pre-existing), so the edit-site inventory this lens builds is structurally empty, and the concept it removes (the declared classification table) has exactly one carrier in the tree. ALTERNATIVES: considered filing on `spec/04:712`'s stale `CoordinatorFence` row, on `docs/api/internal.md:209-215`'s false `CheckpointRequest` excerpt, and on `spec/04:1047`'s `string session_id` interceptor excerpt; all three are already false or already out of domain before SPEC-1 applies, so none becomes wrong *after* the edit, which is what (d) requires.

FACT: The delta this loop exists for is one hunk: §7 "Open decisions for review" was deleted from spec-changes.md and nothing else in that file changed. `diff -u scratchpad/cp-snap/0075_.../spec-recheck-2-r1-start/...spec-changes.md` against the live file is 7 removed lines and nothing else. No other proposal file changed either except summary/review-log. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-2-r1-start/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:152-160

FACT: `#### Request Message Scope` is the ONLY occurrence of that string anywhere in `spec/`, `docs/`, `schemas/`, `charts/`, `tests/`, `scripts/`, `pkg/`, and `cmd/`, and no `(#41-...)` anchor reference in the tree reaches the block (every one of the ~25 `04_system-components.md#41-edge-gateway-replicas` links is about HPA metric roles, subsystem extraction thresholds, or `maxSessionsPerReplica` calibration). The retired block therefore has no inbound cross-reference to repair. — EVIDENCE: spec/04_system-components.md:149; spec/README.md:15; spec/17_deployment-topology.md:1292

FACT: `schemas/lenny-adapter.proto` carries NO scope classification in any doc comment. Grepping it for `pod-scoped`, `session-scoped`, and `§4.1` returns three hits, all unrelated (`:605` schema version, `:645` Upload Handler, `:810` WorkspacePlan). The `CoordinatorFenceRequest` doc comment at `:1449-1454` and the `CoordinatorFenceResponse` comment at `:1464-1471` describe the per-session generation and never call the message pod-scoped, so the reclassification needs no proto comment edit. — EVIDENCE: schemas/lenny-adapter.proto:1449-1454

FACT: `tests/claim-map.json` names no §4.1 claim and no message-scope claim at all (grep for `message_scope|MessageScope|4\.1` returns nothing), and `tests/change-graph.json` names none of the four touched test files. The §28.4 claim register is therefore outside this proposal's blast radius, which is worth knowing because §28.4 requires a register row for every normative §28 statement and a reviewer reaches for it. — EVIDENCE: spec/28_communication-channels.md:161-169; tests/claim-map.json

FACT: `tests/spec-map.json` credits the retiring gate at exactly three sites, two under section `4.1` and one under `28.5.3`, which is exactly what the checklist S2 says it re-registers. Walk the JSON rather than grepping line numbers: `sections.4.1.tests` carries `TestAdapterProtoRequestMessagesAreClassifiedByScope` and `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage`, and `sections.28.5.3.tests` carries the first of those. — EVIDENCE: tests/spec-map.json:156, :169, :5670

CORRECTS [standing-context Settled bullet "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest` `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". SPEC-1 is right and the bullet is wrong: `:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. Confirmed by a comment-stripped brace-counting parse and by a direct `sed -n '450,465p'`. Nobody should "correct" `:458` to `:457`. — EVIDENCE: schemas/lenny-adapter.proto:456-458

USEFUL [standing-context Traps, "Naive proto parsers derail on this file"]: saved a round directly. My first two parse attempts both produced `line=0` for two thirds of the request types, exactly the symptom the trap describes. The working recipe: collect message starts with `^message (\w+) \{$` at column 0, take the body to the next bare `}` line, strip `//` before field matching, and skip fields inside a `oneof` block when you want top-level fields only. With that, the census reproduces: 31 request types, 25 with a top-level `SessionId session_id`, 6 without; `CheckpointRequest` and `CheckpointResponse` the only `oneof` blocks; `CheckpointStart` the only addressed envelope frame; and zero violations of the addressing convention in either direction over every field in the file including `oneof` arms.

USEFUL [standing-context Traps, "Long lines hide the sentences citations point at"]: `spec/04:725`, `:726`, `spec/10:57`, `:60`, and `spec/05:515` are each one very long line and every cited clause is the last sentence on it. `sed -n Np | cut -c1-200` shows none of them and makes correct citations read as false. Grep the sentence.

FACT: `tests/tier0_static/address_rule_citation_test.go` requires exactly one numbered spec section to carry the literal `rejected at the adapter boundary with \`InvalidArgument\``, and today that is §5.2 (`spec/05_runtime-registry-and-pool-model.md:515`). It also names `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` by function name in `addressRuleCases` and demands that case cite §5.2. SPEC-1 neither moves the sentence nor renames the case, so this gate is untouched — but it is the gate that fires if a fixer folds the value rule's wording into the §4.1 block, and it fires with a message about citations rather than about §4.1. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22, :45-47, :85-87

FACT: `0073's §4.2 value rule`, cited in this proposal's §3 and §6, is proposal 0073's OWN section 4.2 ("The rule", at `proposals/0073_...md:964-985`), not spec §4.2 (Session Manager). Reading it as a spec citation makes it look false, because the value rule actually lands at `spec/05:515`. Likewise `0073's §8` in §9 is 0073's Testing section at `:4678`, which does introduce the tier-0 gate (it says "The gate is a new tier-0 file rather than an added check inside `tests/tier0_static/claim_register_proto_agreement_test.go`") without naming the file, and `0073's SPEC-7` is "Declare message scope and state the addressing rule" at `:2823`. All three attributions in the staged text are correct. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964, :2823, :4678, :4690-4691

WATCHOUT: the orchestrator brief handed to this round is stale in three ways and each one wastes time. Its proto anchors (`:589`, `:1166`, `:1447`, `:1448`) are all seven lines short of the tree (`SessionId` is at `:596`, `CheckpointRequest` at `:1173`, `CoordinatorFenceRequest` at `:1455` with its address at `:1456`); it says the snapshot for this lane is `spec-recheck-r2` when the byte-identical one is `spec-recheck-2-r1-start`; and it still describes "§7 question 1" as the existential question when §7 is exactly what this round's delta deleted. The proposal's own anchors are the correct ones. — EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1455



### [spec-recheck.2.review-feasibility.1]

DECISION: Returned an empty findings list for the actor-action feasibility lens on the spec staging — BECAUSE every actor the staged text assigns an action to (the tier-0 gate reading the proto, the adapter refusing an unaddressed session-scoped request) can perform it with data it can see, and every citation in `0075...spec-changes.md` resolves — ALTERNATIVES: filing the D1 sentence "the ones that carry none are exactly its pod rows" (which is false unless "other than the stream envelope" distributes across both clauses, since `CheckpointRequest` carries no address and is a session row); rejected because it is Decisions-section rationale, the next sentence explicitly assigns `CheckpointRequest` to the envelope clause, and the staged spec block quote is unaffected — the same material test that refuted the earlier D2 finding.

FACT: The delta this loop exists for is a pure deletion. `diff -u scratchpad/cp-snap/.../spec-recheck-2-r1-start/...spec-changes.md proposals/.../...spec-changes.md` shows exactly one hunk: `## 7. Open decisions for review` (the tier-3 covered-population question) was removed. `spec-recheck-r2` is byte-identical to the current proposal, so the `spec-recheck-r2` snapshot path in the prompt yields an empty diff — use `spec-recheck-2-r1-start` instead — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-2-r1-start/

FACT: Re-parsed the two service blocks today. 31 distinct RPC request messages; 25 declare a top-level `SessionId session_id`; 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). The addressing convention holds in BOTH directions across the whole file: 26 field declarations named `session_id`, all typed `SessionId`, and no `SessionId`-typed field carries another name — so the replacement gate is green against the tree as it stands — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1456 (`CoordinatorFenceRequest.session_id`), :1217 (`CheckpointStart.session_id = 7`)

FACT: Only two messages in the whole proto declare a `oneof`: `CheckpointRequest` (:1174) and `CheckpointResponse` (:1250), and `CheckpointResponse` is the request type of no RPC (`rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse)` at :133). §4's claim that the envelope predicate mechanically selects `CheckpointRequest` alone is exact — EVIDENCE: schemas/lenny-adapter.proto:133, :1174, :1250

FACT: The reclassification is already consistent with the handler. `CoordinatorFence` rejects an empty `session_id` with `InvalidArgument` before resolving anything, which is what 0073's §4.2 value rule (landed at spec/05_runtime-registry-and-pool-model.md:515) requires of a session-scoped request. No code change is implied by making the fence session-scoped — EVIDENCE: pkg/adapter/coordination.go:108-111

FACT: Nothing outside `spec/04` §4.1 depends on the retired table. `#request-message-scope` has no inbound anchor anywhere in the tree; §28.5.1's CH-FENCE card already describes the fence per-session ("records the generation against the session the fence names ... A fence for one session does not change the generation the pod holds for another"); tests/claim-map.json carries no §4.1 row; no tier-11 gate reads §4.1 (the `specSection` calls into spec/04 reach only §4.4, §4.6.1, §4.6.3, and §4.7). After the edit, spec/04:726 is genuinely the only remaining sentence in `spec/` classifying a request message pod-scoped — EVIDENCE: spec/28_communication-channels.md:314, spec/04_system-components.md:726

FACT: The two tier-0 files 0076 landed beside the gate — `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go` — read Go and proto doc COMMENTS, not the §4.1 table, so retiring the table does not reach them and their absence from §9 is correct — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:11-25, tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:18-25

WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` hardcodes `tests/tier0_static/adapter_proto_message_scope_test.go` in the `slotAddressCaseFiles` inventory, and the gate requires every file there to be credited in `tests/spec-map.json` under every section its `// spec:` annotation names. TEST-1 keeps the same file path, so the entry still resolves — but a fixer or implementor who RENAMES the gate file breaks that gate, and the rename would not be listed in §9 — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:236-336

WATCHOUT: The orchestrator's standing anchors for the proto are off by 7-8 lines (it says `SessionId` at :589 and `CoordinatorFenceRequest` at :1447/:1448). `grep` gives :596 and :1455/:1456, which is what the proposal itself cites. Trust the proposal's numbers here, not the orchestrator note — EVIDENCE: schemas/lenny-adapter.proto:596, :1455

FACT: Every code/test/spec citation in `...spec-changes.md` was re-verified line by line and all resolve: spec/04:151, :153-186, :188, :190, :725, :726; spec/10:57, :60 (the :60 sentence landed under 0076 SPEC-1, commit cf20a0646); spec/05:515; schemas/lenny-adapter.proto:458, :499-503, :1217; pkg/adapter/checkpoint.go:74-84; tests/tier0_static/adapter_proto_message_scope_test.go:17-27; tests/tier0_static/adapter_proto_parse_test.go:10-15; tests/tier0_static/claim_register_proto_agreement_test.go:64; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; docs/reference/adapter-contract.md:81; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40-43, :81, :102, :130, :150-158.

UNVERIFIED: The staged block asserts the addressing frame "opens the stream", and the gate's four refusals do not check that the addressing frame is the FIRST frame — while the same paragraph says "What the gate cannot see is a session addressed under a name and a type that are both unconventional", phrased as the sole limit. I judged this below the bar because classification soundness does not depend on which frame opens the stream, only on which frame addresses it. A later reviewer who disagrees should say why the ordering claim is load-bearing before filing it.


### [spec-recheck.2.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE the only delta in `spec-changes.md` since the `spec-r2` snapshot is the deletion of the whole `## 7. Open decisions for review` section (the tier-3 population question, now summary OD5), and an independent re-derivation of the census, the table arithmetic, the addressing convention, the citation set, and the deleted block's blast radius found nothing that makes the applied spec wrong — ALTERNATIVES: filing the dangling "either service" antecedent (see WATCHOUT below), rejected because the block's own opening sentence establishes the domain; filing D1's set-equality looseness, already in Standing context as examined and dropped.

FACT: the `spec-recheck-r2` snapshot is byte-identical to the live directory; the real delta for this round is `diff spec-r2 <live>` and it is one hunk, the removal of `## 7`. Nothing else in `spec-changes.md` moved. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2/0075_...spec-changes.md:152-161

FACT: re-derived the whole reproduction claim mechanically and it is exact. The §4.1 table rows 155-186 are 32; their message set minus the proto's 31 RPC request types is exactly `{CheckpointStart}` and the reverse difference is empty; 26 session rows and 6 pod rows. The proto gives 25 request types with a top-level `SessionId session_id` and 6 without (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). After the fence moves and the envelope clause classifies `CheckpointRequest`, the derived split is 27 session rows and 5 pod rows, which is the table's split row for row. — EVIDENCE: spec/04_system-components.md:155-186, schemas/lenny-adapter.proto:32, :261

FACT: the name/type biconditional holds with zero violations across all 88 messages including `oneof` arms, checked two ways: a brace-tracking parse, and `grep -n SessionId schemas/lenny-adapter.proto | grep -v session_id` (returns only the `message SessionId` declaration and its doc comment) plus `grep -n session_id | grep -v "SessionId session_id"` (returns only comment lines). Cheaper than the parse and it is a complete check. — EVIDENCE: schemas/lenny-adapter.proto:595-596

FACT: the `identifier-senses.yaml` ordinal question is safely closed rather than merely dismissed. Its eight `spec/04` rows all key on `CH-RUNTIMEOPS` sites, which sit at `spec/04_system-components.md:290-292`, well after the block SPEC-1 deletes (`:151`, `:153-186`, `:188`). Deleting lines before a site does not change that site's position in source order among retired-spelling sites, so no ordinal shifts. — EVIDENCE: tests/registers/identifier-senses.yaml:14-36, spec/04_system-components.md:290

FACT: the line-shift blast radius is genuinely empty. `grep -rn "04_system-components\.md:[0-9]"` over the whole tree, excluding `proposals/`, `scratchpad/`, and `.git/`, returns citations in exactly four files: `BUILD-GAPS.md`, `TEST-GAPS.md`, `gateway-runtime-comms.md`, and `gateway-runtime-comms-remediation.md`. All four are literal members of `readExcludedFiles`. No `docs/`, `schemas/`, `charts/`, `tests/`, or `spec/` file carries a `spec/04:NNN` citation at all. — EVIDENCE: scripts/specshift/scope/scope.go:88-95

FACT: `protoFields` has exactly one caller outside the parse file (`tests/tier0_static/claim_register_proto_agreement_test.go:64`) and `protoServiceRequests` exactly one (`tests/tier0_static/adapter_proto_message_scope_test.go:87`). The two tier-0 files 0076 landed, `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go`, call neither and read no `spec/04` path, so §9's file list is complete. A single `grep -n "parseMessageScopeTable\|declaredScope\|protoServiceRequests\|protoFields\|messageScopeSpecPath" tests/tier0_static/*.go` settles this in one call. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36, :68

WATCHOUT: after SPEC-1 the phrase "A request message either service declares" in the staged paragraph 1 loses the only text that named the two services, because the retired table's `Service` column was it. Nothing else in §4.1 names `Adapter` or `GatewayControl`; the nearest surviving mention in the file is `spec/04_system-components.md:719`, inside §4.7. I examined this as a candidate finding and declined it: the block's own first sentence fixes the domain as "the gateway-adapter protocol", the protocol declares exactly two services, and the restrictor is redundant rather than load-bearing. A later round that re-derives it should reach the same answer rather than filing. — EVIDENCE: spec/04_system-components.md:151, schemas/lenny-adapter.proto:32, :261

USEFUL [Standing context / Traps]: the trap list saved this round at least four hours. Four candidates I reached independently were already recorded as declined with their grounds ("only way a request addresses a session", the nested-address hole, the §4.7.1 declared-versus-derived tension, and the session-scoped fence exiting a pod-wide hold), and the naive-proto-parser warning meant I wrote a brace-tracking parse first rather than debugging a wrong census.


### [spec-recheck.2.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the staged spec edit is confined to `spec/04_system-components.md` `#### Request Message Scope` (`:151`, `:153-186`, `:188`), which classifies gateway-adapter gRPC request messages by field set. It names no CRD, no status subresource, no field manager, no finalizer, no admission webhook, no reconcile loop, and no controller. §9's touched-file list is `spec/04` plus five test files and `tests/spec-map.json`, with no `charts/`, no `pkg/apis/`, and no `pkg/controller/`. There is no surface for the Kubernetes-idiom lens to bite on. — ALTERNATIVES: I looked for a `spec/04` §4.6.3 ownership interaction, a §10.3 zero-RBAC agent-pod interaction, and an admission-purity interaction, and found the staged block touches none of them.

FACT: SPEC-1's proto anchors re-verified today and all correct. `ReportSessionScrubRequest` opens at `schemas/lenny-adapter.proto:456` with `SessionId session_id = 2;` at `:458` (so SPEC-1's `:458` is right); `ReportPodScrubRequest` is `:499-503` with `pod_id`/`outcome`/`detail` and no address; `CheckpointStart`'s `SessionId session_id = 7;` is `:1217`. — EVIDENCE: schemas/lenny-adapter.proto:456-458, :499-503, :1217

CORRECTS [standing-context Settled "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest`'s address is at `:457` and that "SPEC-1 cites `:458`, one line off". The tree disagrees: `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's citation is exact and the standing-context bullet is the off-by-one. Do not "fix" `spec-changes.md:135` to `:457`.

FACT: no inbound reference to the retiring table exists anywhere in `spec/`, `docs/`, `schemas/`, `charts/`, or `tests/`. A grep for `request-message-scope`, `Request Message Scope`, `scope table`, and `classification table` across those trees returns only the heading itself (`spec/04_system-components.md:149`) and unrelated tables in `spec/06`, `spec/12`, `spec/16`, and `spec/25`. Retiring the block owes no anchor redirect and no cross-reference repair. — EVIDENCE: spec/04_system-components.md:149

FACT: this round's spec-lane delta is one deletion. `diff` of the live directory against `scratchpad/cp-snap/.../spec-r2` shows the only change to `spec-changes.md` is the removal of `## 7. Open decisions for review` and its single question (the tier-3 covered-population question), which moved to the summary as OD5. The `spec-recheck-r2` snapshot is byte-identical to the live directory, as the standing context's snapshot trap predicts. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:145-156

USEFUL [standing-context Traps, "Snapshot diffs are routinely empty"]: saved me from treating the empty `spec-recheck-r2` diff as a broken snapshot; diffing against `spec-r2` produced the real delta immediately.

USEFUL [standing-context Traps, "Long lines hide the sentences citations point at"]: `spec/04:188` and `:190` are single long lines; piping through `cut -c1-500` was needed to see the grounding sentence and the `ShutdownRequest` precedent that D3 rests on.


### [spec-recheck.2.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE `spec-changes.md`, `non-spec-changes.md`, and the
implementation checklist are byte-identical to the `spec-recheck-r1-start` snapshot, so this lens is
reviewing text it already cleared, and an independent re-derivation of the whole mechanism (proto census,
row arithmetic, gate refusal satisfiability, every citation in the staged file, the meta-gate blast radius)
reproduced the same result — ALTERNATIVES: filing §3's three-of-four gate refusals (rejected: overview
prose inside the proposal; the D1 block that lands carries all four, as do D2, §5, and non-spec §8);
filing D2's "it can never fail" (rejected: rationale, not staged spec text, refuted precedent on the same
paragraph); filing the `ShutdownRequest` "field's presence standing in for a scope" clause (rejected:
subject is handler dispatch, not classification).

CORRECTS [Standing context / "Proto anchors, current and verified"]: the entry says
`ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites
`:458`, one line off, below the bar". That is backwards. `:457` is `string pod_id = 1;` and `:458` is
`SessionId session_id = 2;`. SPEC-1's citation is exact and no correction is owed — EVIDENCE:
schemas/lenny-adapter.proto:456-458.

FACT: `gate_integrity_test.go`'s meta-gate is one-directional in the direction that matters here.
`unregisteredGates(declared, tierZeroGates)` iterates `tierZeroGates` and reports only entries the tree does
not carry, so TEST-1 adding a new gate function name trips nothing; the entries it could trip are
`TestClaimRegisterAgreesWithTheAdapterProto` in `claim_register_proto_agreement_test.go`, which TEST-1 must
not rename or move — EVIDENCE: tests/tier0_static/gate_integrity_test.go:70, :180-198.

FACT: the staged §4.1 sentence "A tier-0 gate refuses a protocol definition in which..." owes no §28.4
claim-register row. §28.4's domain is "Every normative statement **this section** makes about a mechanism",
which is §28 alone — EVIDENCE: spec/28_communication-channels.md:163.

FACT: no tier-11 test reads spec/04 §4.1. Every `specSection` caller against `04_system-components.md` names
§4.4, §4.6.1, §4.6.3, or §4.7 — EVIDENCE: tests/tier11_docs/*.go (grep `specSection.*04_system-components`).

USEFUL [Standing context / Traps]: five of the ten candidates my lens raised are recorded there with the
ground on which they were declined, several of them three to six times. Reading Traps before deriving saved
the whole round — EVIDENCE: 0075_..._.review-log.md `## Standing context` → `### Traps`.

USEFUL [Standing context / "Cache keys omit summary.md and review-log.md"]: relevant this round. The cache
key `2d25f5f323c0` already had a `mechanism-r1-...json` entry from 19:39 with an empty findings list over
this exact text. I did the review independently rather than replaying it, and reached the same answer, but a
future agent should know that an r1/r2 pair on one hash means the lane text did not move between rounds —
EVIDENCE: scratchpad/cp-cache/0075_.../mechanism-r1-2d25f5f323c0.json.


### [spec-recheck.2.review-operational.1]

DECISION: returned an EMPTY findings list for the operational-consistency lens on the staged spec edits — BECAUSE the staged block touches no metric, no alert, no CRD condition, and no runbook, and every observability surface adjacent to the reclassified message was re-derived from the tree and found untouched by SPEC-1 — ALTERNATIVES: filing `docs/reference/adapter-contract.md:69` ("precondition for any subsequent operational RPC", pod-wide reading falsified by 0076) — rejected because its remedy is a docs edit outside this loop and it is already DEFERRED in the Standing context.

CORRECTS [Standing context / "Proto anchors, current and verified"]: the entry says `ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites `:458`, one line off, below the bar". That is wrong in the proposal's favour. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's citation is EXACT and no correction is owed. A future round that "fixes" the citation to `:457` would introduce a false citation. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: no alert, runbook, or metric anywhere in the tree turns on a gRPC request message's scope class, and none turns on the fence at all. `grep -rn -i fence docs/runbooks/` returns nothing; the three coordinator metrics (`lenny_coordinator_handoff_duration_seconds`, `lenny_coordinator_fence_retry_total`, `lenny_coordinator_fence_relinquished_total`) are gateway-side and labeled by `pool` only; the one alert that reads them, `CoordinatorHandoffSlow`, names no scope. The only adapter-side coordination metric, `lenny_adapter_coordinator_hold`, is an unlabeled gauge and its pod-scope statement sits at `spec/10_gateway-internals.md:60`, which SPEC-1 leaves standing and cites correctly. Reclassifying `CoordinatorFenceRequest` session-scoped therefore misleads no operator about any emitted signal. This is the third independent confirmation of review-log.md:799; treat it as settled and do not re-derive. — EVIDENCE: spec/16_observability.md:185,190-192,552; docs/reference/metrics.md:309-312; pkg/alerting/rules/rules.go:1587; spec/10_gateway-internals.md:57,60

FACT: §28.4's claim register is scoped to "Every normative statement **this section** makes", i.e. §28 alone (`spec/28_communication-channels.md:161-165`). SPEC-1's new §4.1 sentence asserting a tier-0 gate that does not exist until S2 therefore owes NO `tests/claim-map.json` row, and the S1→S2 red window is the whole of its exposure. A lens that reads channel-naming.md N4 ("add the claim-register row for any part of the contract that does not yet hold in code") in isolation will reach for this; the register's own scope sentence closes it. — EVIDENCE: spec/28_communication-channels.md:161-165

FACT: the addressing biconditional SPEC-1's gate paragraph rests on is green on the shipped proto with zero exceptions, verified mechanically rather than from the standing context: `grep -n "^\s*\(repeated \)\?[A-Za-z0-9_.]* session_id\s*=" schemas/lenny-adapter.proto | grep -v "SessionId session_id"` returns nothing, and the reverse grep for a `SessionId`-typed field under another name returns nothing. The replacement gate cannot be red on day one. — EVIDENCE: schemas/lenny-adapter.proto

FACT: every file:line citation in `spec-changes.md` was re-verified in this pass and all are exact: `spec/04_system-components.md:149,151,153-186,175,188,190,725,726`; `spec/10_gateway-internals.md:57,60`; `spec/05_runtime-registry-and-pool-model.md:515`; `schemas/lenny-adapter.proto:458,499-503,1217`; `pkg/adapter/checkpoint.go:74-84`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`; `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`; `docs/reference/adapter-contract.md:81`. `:725`/`:726` do sit inside `#### 4.7.1 Role and Gateway RPC Contract` (`spec/04_system-components.md:692`, next heading `:728`), so SPEC-1's "§4.7.1" label is exact too. A future citation lens can spot-check rather than re-walk all of them.

USEFUL [Standing context / Traps / "Cache keys omit summary.md and review-log.md"]: this shard is a real run, not a cache replay. The cache slot `operational-r2-2d25f5f323c0.json` was empty on entry and the review was done from the tree. The two earlier "clean operational passes" the trap entry flags as possibly one cache hit are now backed by an independent third pass.


### [spec-recheck.2.review-performance.1]

DECISION: returned an empty findings list, the fourth consecutive empty pass by this lens on this
proposal — BECAUSE the staged spec text names no store, key, watch, informer, queue, lease, leader, or
emission site, so the top-tier write arithmetic my lens owes has a numerator of zero, and the one
runtime obligation the reclassification adds is already executed in the shipped handler.
ALTERNATIVES: the session-scoped fence exiting a pod-wide hold (standing-context Trap, declined by five
lenses now, 0076 OD3 territory); the `spec/10:39` same-generation retry and the missing coordfence
backoff (both pre-existing, §6 assigns them to 0080 §1.16); tier-0/tier-3 test cost (down or flat).

FACT: the staged spec text is byte-identical to what `spec-recheck.1.review-performance.1` reviewed.
The cache key is the same `2d25f5f323c0` that entry recorded, `diff -ru` against the `spec-recheck-r2`
snapshot is empty, and `diff -u` against `spec-r2` shows only the `## 7. Open decisions for review`
deletion. My `performance-r2-<H>` slot missed only because the round number is part of the filename,
so a future lens whose slot misses should still check the sibling `performance-r1-<same H>` file before
spending a full pass. EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r1-2d25f5f323c0.json

CORRECTS [standing-context Settled, "Proto anchors, current and verified"]: it says SPEC-1's citation
`schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s address is "one line off, below the
bar". It is not off. `message ReportSessionScrubRequest {` is at `:456`, `string pod_id = 1;` at `:457`,
and `SessionId session_id = 2;` at `:458`, which is exactly what SPEC-1 cites. The `:457` in the log is
the `pod_id` line. Nobody should spend a pass re-checking this or file a citation finding against it.
EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: I re-derived §1.2's post-0076 claims against the tree rather than trusting the orchestrator brief,
which still asserts §1.2 says the pod-wide ground holds. It does not: §1.2 already records that
`Server` declares `hold holdState` where the pod-wide state stood (`pkg/adapter/server.go:301`, one line
off from the proposal's `:302` and below the bar) and that `coordinationState` now lives on the slot
entry (`pkg/adapter/slot.go:59`). SPEC-1's "all three sites named in §1.2" resolves: §1.2 names `:151`,
`:175`, `:188`, and SPEC-1 deletes `:151`, `:153-186` (which contains `:175`), and `:188`.
EVIDENCE: pkg/adapter/server.go:301, pkg/adapter/slot.go:59

FACT: no metric, alert, or chart surface keys on the §4.1 classification, so retiring the table has no
observability blast radius. The only "session-scoped" hits under `spec/16`, `docs/reference/metrics.md`,
and `charts/` are `lenny_adapter_unaddressed_frame_rejected_total`, whose subject is the §28.5.3
intra-pod JSONL frame rather than a gateway-adapter request message, and which SPEC-1 does not touch.
This closes the one edit-site question my lens could have raised and nobody had recorded.
EVIDENCE: spec/16_observability.md:189, docs/reference/metrics.md:180

USEFUL [standing-context Traps]: "Do not re-file the session-scoped fence exiting a pod-wide hold" and
"Long lines hide the sentences citations point at" were both load-bearing. The second is why
`spec/10:57`, `:60`, `spec/05:515`, and `spec/04:725`/`:726` verify only under `sed -n Np | tr '.' '\n'`;
a plain `cut -c1-160` shows none of the cited clause and makes a correct citation look false.

UNVERIFIED: unchanged across four rounds of this lens — nobody has run tier 0 or tier 3 on this branch.
The replacement gate passing on the shipped proto and `validate-maps` staying green across S2's single
commit are the two claims a run would settle and reading cannot. Whoever implements settles both first.


### [spec-recheck.2.review-reliability.1]

DECISION: returned an empty findings list, the fourth consecutive empty reliability pass on this proposal — BECAUSE the staged spec text is byte-identical to what `spec-recheck.1.review-reliability.1` cleared (the only delta since `spec-r2` is the deletion of `## 7. Open decisions for review`, which removes no normative text), and an independent re-derivation from the tree, rather than a replay of the log, found every reliability-bearing citation in the staging exact — ALTERNATIVES: re-filing the coordfence same-generation retry the adapter refuses and the missing 1-second backoff (both pre-existing, both named non-goals at `spec-changes.md:151-152`, owner 0080 §1.16); re-filing the session-scoped fence as the only exit from a pod-wide hold (standing-context Trap, declined by five lenses now); re-filing the unconventional-spelling fail-open as a lost coverage guarantee (disclosed in the staged block itself and priced in summary OD1, and the security lens owns fail-closed anyway).

FACT: `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2;` inside `message ReportSessionScrubRequest` at `:456`. The standing context's Settled entry at review-log.md:20 says "its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That parenthetical is wrong: `:457` is `string pod_id = 1;` and SPEC-1's `:458` is exact. Nothing is owed, but a future round should not "correct" SPEC-1 to `:457` on the strength of that entry. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the gateway never issues an unaddressed fence, so the reclassification cannot strand a pod in hold behind the `spec/05:515` empty-identifier refusal. `FenceClient.CoordinatorFence(ctx, sessionID string, coordinationGeneration int64)` takes a session identifier as a required parameter and there is no pod-level fence entry point; `spec/10_gateway-internals.md:38` orders `CoordinatorFence(session_id, new_generation)` per session. The only stall case is a pod in hold with no bound entry, which is the Trap already declined and 0076's landed behavior. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:62-63, spec/10_gateway-internals.md:38

FACT: the addressing convention holds over the WHOLE proto, re-derived mechanically today rather than over the request types alone: 26 lines match `^\s*SessionId session_id = [0-9]+;`, `grep -nE '^\s*(repeated )?SessionId [a-z_]+ = ' | grep -v 'session_id ='` returns nothing, and `grep -nE '^\s*[A-Za-z0-9_.]+ session_id = ' | grep -v '^[0-9]*:\s*SessionId session_id'` returns nothing. The replacement gate's two biconditional clauses are green on the shipped proto without a proto edit, so S2 introduces no red window of its own beyond the one S1 records. — EVIDENCE: schemas/lenny-adapter.proto:1022, :458, :1217

FACT: `pkg/adapter/checkpoint.go:74-84` is exactly the enforcement §4 cites, and its own `// spec: §4.1` comment reads "Checkpoint is session-scoped, so the opening frame's session identifier is the address the whole stream is served under". SPEC-1's envelope clause restates that comment's model, so the code comment survives the retirement unedited. A reviewer looking for a missed code-comment edit site will find this one and should not file it. — EVIDENCE: pkg/adapter/checkpoint.go:74-84

WATCHOUT: the cache key `2d25f5f323c0` still omits `summary.md` and `review-log.md`, and `spec-changes.md` has not changed since the previous round, so this lens's cache slot and the previous round's differ only because `non-spec-changes.md` moved. A summary-only or log-only edit would serve this empty answer unread. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md (unchanged since the 17:39 `spec-r2` snapshot apart from the §7 deletion)

USEFUL [spec-recheck.1.review-reliability.1]: its two FACTs (the fence-retry predicate is owned by 0080 §1.16, and `spec/28:314-317` plus `spec/10:40` already state the fence per-session so SPEC-1 removes a contradiction rather than creating one) were the two places this lens went first and both re-verified unchanged. A fifth pass can spot-check them instead of rebuilding the argument.

USEFUL [standing context, Traps]: "Long lines hide the sentences citations point at" saved the round again — `spec/05:515`, `spec/10:38`, `:39`, `:40`, `:57`, `:60` are each one very long line and `sed -n Np | tr '.' '\n'` is what makes the cited clause visible.

OPEN: nothing new. The reliability lens has now returned empty on four rounds across both lanes. Re-run it only if a fix pass edits the staged envelope clause, the constraint paragraph's gate refusals, or any sentence tying a scope class to a refusal or to the coordinator hold.


### [spec-recheck.2.review-security.1]

DECISION: returned an empty findings list for the security lens on the staged spec edits — BECAUSE the only delta since the `spec-r2` snapshot is the deletion of `spec-changes.md` §7 (Open decisions), which carries no security content, and the two security checks both come back clean on the surviving text: the retired block carries no control, and the reclassification only widens the class the fail-closed refusal at `spec/05:515` applies to — ALTERNATIVES: filing the loss of the table's Service and Direction columns as a lost trust-boundary record (rejected: §4.7.1's two RPC tables and the §28.3 registers carry direction, and no applied text becomes wrong); filing the nested-address hole and the session-scoped-fence-versus-pod-wide-hold tension (both already declined multiple times, traps 72 and 74).

FACT: the derived session class is a strict superset of the retired table's on the shipped proto, which is the whole security argument. A brace-aware parse (strip `//` comments, count braces per line, handle same-line `{}`) returns 88 messages and 31 RPC request types; 25 declare a top-level `SessionId session_id`; the 6 that do not are AdapterEventsRequest, CheckpointRequest, DemoteSDKRequest, GetObservedIntegrationLevelRequest, NegotiateVersionRequest, ReportPodScrubRequest. Nothing moves session-to-pod, so `spec/05_runtime-registry-and-pool-model.md:515`'s "a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved" can only gain a message, never lose one.
EVIDENCE: schemas/lenny-adapter.proto; spec/05_runtime-registry-and-pool-model.md:515

FACT: the one behavioral obligation the reclassification creates is already met in the tree — `pkg/adapter/coordination.go:108-111` reads `req.GetSessionId().GetValue()` and returns `InvalidArgument` on empty before `boundSlotState` resolves anything at `:116`. So SPEC-1 does not create an unimplemented fail-closed requirement.
EVIDENCE: pkg/adapter/coordination.go:107-119

FACT: no spec, docs, chart, or test surface reads the §4.1 classification table or its heading. `grep -rn "request-message-scope\|Request Message Scope" spec/ docs/ schemas/ tests/ pkg/ charts/` returns only `spec/04_system-components.md:149`, and a sweep of every file mentioning `04_system-components` for a reader of the `### 4.1` / `#### Request Message Scope` section returns nothing beyond the retiring tier-0 gate. The tier-11 file `basic_level_echo_stamp_doc_reconciliation_test.go` and `tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99` cite §4.1 in `// spec:` annotations only and read no §4.1 text; those annotations stay true because §4.1 still classifies after SPEC-1. So the retirement removes no enforcement and owes no unstaged edit site.
EVIDENCE: spec/04_system-components.md:149; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26,132,284

FACT: `pkg/adapter/checkpoint.go:74-84` cites "§4.1 — Checkpoint is session-scoped" in a code comment and enforces the empty-address refusal plus `slotlayout.ValidateSlotID` on the opening frame before the op lock. The envelope clause keeps that citation true after the table goes, so no code comment is falsified by SPEC-1.
EVIDENCE: pkg/adapter/checkpoint.go:74-87

USEFUL [Standing context / Traps 64, 92]: the brace-aware-parser warning and the "spot-check, do not re-derive" instruction between them saved most of a round. The parser recipe in trap 64 reproduces 88/31/25/6 first try; a naive `}`-terminated parser does not.

### [f2.cleanup]

FACT: the summary already carried exactly the eight listed sections, in the listed order, with `## Deliverable index` last: title, `## Summary` holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**` in that order and no prose of its own, `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, `## Deliverable index`. No rename was needed, nothing was relocated, and the file is unchanged by this firing.
EVIDENCE: 0075_fix_derive-message-scope-from-the-address-type.summary.md:1,3,5,16,38,51,66,75,83,161,251,259

FACT: no `### Retired` block, no meta-list of staged items with dispositions, and no errata block sat anywhere in the summary. `grep -n '^###' summary.md` returns nothing, so `## Open decisions for human to make` carries its three entries and no sub-block.
EVIDENCE: 0075_fix_derive-message-scope-from-the-address-type.summary.md:83-159

FACT: OD1, OD4, and OD5 keep their identifiers verbatim and all three remain the human's. OD4's and OD5's adjudicated resolutions were refuted at the gate and never applied, and the change files confirm it: `non-spec-changes.md:52` still registers the replacement gate's cases "under the same sections", which includes the section 28.5.3 credit at `tests/spec-map.json:5670`, and `sessionScopedMessages` stays a hand-maintained `[]string` (`non-spec-changes.md:62`) with no gate deriving it from the protocol definition.
EVIDENCE: 0075_...non-spec-changes.md:52,62; tests/spec-map.json:5670; 0075_...summary.md:142,153

FACT: the section 28.5.3 and section 4.1 register anchors OD4 rests on hold in the tree. `tests/spec-map.json:5670` credits `tests/tier0_static/adapter_proto_message_scope_test.go::TestAdapterProtoRequestMessagesAreClassifiedByScope` under 28.5.3, and `:156` and `:169` credit that case and `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under 4.1.
EVIDENCE: tests/spec-map.json:156,169,5670

FACT: the three shipped-tree defect entries (the equal-generation re-fence refusal, the stale `docs/api/internal.md` protocol excerpts, and the §4.7 `CoordinatorFence` row) stand verbatim, and the three impact rows (0073, 0076, 0080) stand as written. The 0080 prose inside the defect entries agrees with the 0080 impact row: the row leaves §1.16 standing as the owner of the acceptance predicate, and the defect entries name §1.16 as the owner of the re-fence remedy and record that 0080's inventory carries neither the docs page nor the §4.7 row today. No second row was added and no row was merged.
EVIDENCE: 0075_...summary.md:170-249,251-257

WATCHOUT: the preamble of `## Defects in the shipped tree that this proposal does not stage` reads "Both defects this proposal confirms in the specification are staged" and then names the tier-3 comment (TEST-2), which sits in `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` rather than in `spec/`. Its closing sentence about the entries below it is true, and no move this firing made falsified any part of it, so the format pass left it as it stands. A later pass that owns the wording should scope "in the specification" to SPEC-1 alone.
EVIDENCE: 0075_...summary.md:162-168


### [non-spec-recheck.1.review-citations.1]

DECISION: returned the cached citation-audit answer verbatim (empty findings) rather than re-deriving — BECAUSE the cache key over spec-changes + non-spec-changes + implementation-checklist matched (H=2d25f5f323c0), and `diff -ru` against the spec-recheck-r3 snapshot shows the ONLY file changed since that snapshot is the review log, which is not staged text. The substrate this lens owns is byte-identical to what the cached run audited. ALTERNATIVES: re-running the ~30 citation verifications; rejected as pure duplication with no new text to examine.

FACT: the cache path `scratchpad/cp-cache/<proposal>/citations-r1-$H.json` is keyed by ROUND NUMBER and not by lane, so a spec-lane r1 and a non-spec-lane r1 collide on the same file. The hit this run served was written by a spec-lane citations pass (its coverage text describes the spec-changes.md delta). It was still the right answer, because the hash covers all three staged files, but a future lane should not assume a hit came from its own lane.
EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/citations-r1-2d25f5f323c0.json

FACT: summary.md (19:18) and non-spec-changes.md (19:07) both predate the spec-recheck-r3 snapshot (20:10), so the "non-spec staging changed after the last convergence" framing in this lane's brief refers to edits already inside the snapshot and already inside the cached audit's scope.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/ mtimes; scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-r3

USEFUL [f2.cleanup WATCHOUT]: the summary's "Both defects this proposal confirms in the specification are staged" preamble mis-scopes TEST-2 (a tier-3 test comment) as a specification defect. It is already logged as a wording issue owned by a later pass, so the citation lens correctly does not re-file it.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:162-168



### [non-spec-recheck.1.review-client-surface.1]
FACT: The client-surface lens cache hit on this run: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/client-surface-r1-2d25f5f323c0.json already held a completed empty-findings answer over the identical spec-changes + non-spec-changes + checklist text, so this pass returned it verbatim without re-investigating. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/client-surface-r1-2d25f5f323c0.json:1
WATCHOUT: The cache key hashes only spec-changes.md, non-spec-changes.md, and implementation-checklist.md. The summary (0075_...summary.md, mtime 19:18) is NOT in the key, so a round whose only delta is in the summary will replay a cached answer rather than review the new summary text. A future lens that must cover summary-only deltas has to bypass or re-key the cache. EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md
FACT: The cached coverage note records the durable client-surface result for this proposal: no docs/, sdks/, charts/, schemas/*.json, or pkg/gateway/openapi/openapi.json surface mirrors the §4.1 request-message-scope classification, so SPEC-1's table retirement has no parallel client representation to keep in sync. grep 'lenny.adapter.v1|SessionId' over sdks/ returns nothing. The one adjacent doc mirror is docs/reference/adapter-contract.md:81, which mirrors §4.7's ReportSessionScrub row that SPEC-1 leaves unedited. EVIDENCE: /home/ec2-user/lenny/docs/reference/adapter-contract.md:81


### [non-spec-recheck.1.review-edit-sites.1]

MISTAKE: the Standing context's proto-anchor bullet is wrong about `ReportSessionScrubRequest`. It records "its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". The tree says the opposite: `:456` opens the message, `:457` is `string pod_id = 1`, and `:458` is `SessionId session_id = 2`. SPEC-1's citation is exact and there is nothing to correct. Anyone acting on that bullet would "fix" a correct citation into a wrong one.
CORRECTS [Standing context / Settled, "Proto anchors, current and verified"]: SPEC-1's `schemas/lenny-adapter.proto:458` is correct, not one line off.
EVIDENCE: schemas/lenny-adapter.proto:456,457,458

USEFUL [Standing context / Traps, "Cache keys omit summary.md and review-log.md"]: this saved the whole round. The cache probe returned a populated `edit-sites-r1-2d25f5f323c0.json` written at 19:37, and the brief says to return it verbatim and stop. Its own `coverage` string ends "Out of my scope by construction: findings whose only remedy is in non-spec-changes.md, the checklist, or the summary", which is a spec-lane answer, and its delta narrative is the §7 deletion in spec-changes.md rather than this lane's non-spec delta. The run was not interrupted either: later files in the same directory (fresh-r2 at 20:09) show that lane finished. I declined the hit and reviewed. Anyone in a non-spec lane who takes the hit reviews nothing.
EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r1-2d25f5f323c0.json; fresh-r2-2d25f5f323c0.json (mtime 20:09)

FACT: the delta this lane owes attention to is exactly two new paragraphs in `non-spec-changes.md` (the "keeps the file path" paragraph at `:43-48` and the "widened membership" paragraph at `:74-82`), plus the OD2→OD4/OD5 replacement and the two new shipped-tree defect entries in `summary.md`. Get it with `git diff 470cd4e34 HEAD -- <proposal dir>`; 470cd4e34 is "baseline before open-decisions firing 4 (post-non-spec-loop)". Diffing against `scratchpad/cp-snap/.../spec-recheck-r3` shows only the review log and is misleading.
EVIDENCE: git 470cd4e34..HEAD

FACT: every anchor in both new non-spec paragraphs verifies. `slotAddressCaseFiles` names the gate file at `spec_map_slot_address_registration_test.go:336` (and the shared parse at `:337`); the five ranges are `:973`, `:1028`, `:1054`, `:1096`, `:1110`; `repoFileLines` is `:699-706`. The eight added tier-3 members verify at `schemas/lenny-adapter.proto:341-343, 364-368, 383-385, 403-406, 419-424, 1455-1461, 1538-1549, 1673-1684`, each `SessionId session_id = 1` at the top level, none declaring `slot_id`.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336,337,699,973,1028,1054,1096,1110

FACT: every citation in the two NEW shipped-tree defect entries verifies. `docs/api/internal.md:210-214` is the false `CheckpointRequest` excerpt, `:272-274` the false `DemoteSDKRequest` one, `:94` the `UploadFiles` RPC that grep finds nowhere under `schemas/`; the shipped messages are at `schemas/lenny-adapter.proto:1173-1187` and `:1694-1698`. `code_blocks_test.go:103-113` dispatches on json/yaml/go/bash/sql only. `spec/04:712` is the fence row; `spec/28:314-317` and `:322`; `pkg/adapter/holdstate.go:335`,`:348`; `pkg/adapter/coordination.go:150-156`; `specTableRowNameRE` at `spec_47_rpc_row_naming_test.go:35` reads the first column only. 0080's inventory is §1.1-§1.21 and its §2 bullet is at `:453-454`.
EVIDENCE: docs/api/internal.md:94,210,272; schemas/lenny-adapter.proto:1173-1187,1694-1698; tests/tier11_docs/code_blocks_test.go:103-113; tests/tier11_docs/spec_47_rpc_row_naming_test.go:35

FACT: the tier-3 split is confined to one file. `send_message_stamp_test.go` is the only sibling in `package adapter_session_address_test` and references none of `sessionScopedMessages`, `retiredFieldName`, `retiredWrapperName`, `reservesNumber`, or `reservesName`. Symmetrically, no file in `package tier0_static` outside the gate file reads `parseMessageScopeTable`, `declaredScope`, `messageScopeRow`, `scopeRow`, `messageScopeSpecPath`, `checkpointStartMessage`, `checkpointStartService`, or `messageScopeDisagreements`, so retiring them compiles.
EVIDENCE: tests/tier3_contract/adapter_session_address/send_message_stamp_test.go:1-122

FACT: a python parse that tracks brace depth per `message` and records each field's declared type, name, number, and top-level-ness reproduces every census number on this proposal in one run: 88 messages, 31 RPC request types, 25 declaring a top-level `SessionId session_id`, the 6 that do not, exactly 18 messages declaring `reserved "slot_id"` (byte-identical to `sessionScopedMessages`'s keys), the 8 additions, and zero name/type biconditional violations across every message including `oneof` arms. Comment-strip with `re.sub(r'//.*','',line)` first; the `{sessionId}` path and one-line empty messages are what break naive parsers.
EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the edit-site sweep is clean and each negative is cheap to re-confirm. No spec/, docs/, schemas/, or charts/ surface cross-references the §4.1 table (`grep "classification table\|scope table\|#request-message-scope"` returns nothing pointing at it); the only reader-facing sentence classifying a request message is `docs/reference/adapter-contract.md:81`, held by the tier-11 gate against `spec/04:725`, which SPEC-1 keeps; `tierZeroGates` in `gate_integrity_test.go:63-89` omits the gate; `derivedInventoryCaseFiles` (`spec_map_slot_address_registration_test.go:1212-1229`) keys on slot-surface calls, file names, and `TEST-GAPS.md`, none of which the replacement gate's content triggers; and the only non-proposal references to the gate file or its two case names are `tests/spec-map.json:156,169,5670` and `spec_map_slot_address_registration_test.go:336`.
EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-89; tests/tier0_static/spec_map_slot_address_registration_test.go:1212-1229; docs/reference/adapter-contract.md:81

DECISION: returned an empty findings list. BECAUSE every identifier the staging adds, changes, or removes (`retiredDuplicateNumbers`, the `sessionScopedMessages` type change, the retiring gate's package-level helpers, the replacement gate's cases) was swept across spec/, docs/, schemas/, charts/, tests/registers/, spec-map, claim-map, and change-graph, and every surface that would become wrong is already in §9's files-touched list. ALTERNATIVES: I considered filing the `tests/registers/identifier-senses.yaml:227` and `residual-reserved-phrases.yaml:153` rows against the summary's "the only tests naming the page" clause, and rejected it: those are registers rather than tests, the clause's load-bearing conclusion (nothing reddens on account of `docs/api/internal.md`) holds either way, and the sentence sits in a defect-record entry that stages nothing.


### [non-spec-recheck.1.review-fresh.1]
DECISION: Returned the cached fresh-lens result (empty findings) instead of re-reviewing — BECAUSE the cache key md5(spec-changes + non-spec-changes + implementation-checklist) hashed to 2d25f5f323c0 and a completed fresh-lens answer already sat at scratchpad/cp-cache/0075_.../fresh-r1-2d25f5f323c0.json — ALTERNATIVES: a full re-read, rejected after confirming the cached run covered exactly this text (see FACT below).
FACT: The staging has not moved since the spec-recheck-r3 snapshot. `diff -u` of both the summary and the non-spec-changes file against scratchpad/cp-snap/0075_.../spec-recheck-r3/ returns EMPTY. So this loop's stated "delta since last convergence" is not visible as a file delta against that snapshot; the only recorded change the cached run saw was the deletion of `## 7. Open decisions for review` from the spec-changes file (delta measured against spec-r2). — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-r3/0075_fix_derive-message-scope-from-the-address-type.summary.md vs proposals/.../0075_fix_derive-message-scope-from-the-address-type.summary.md
WATCHOUT: The orchestrator prompt for this lane still describes `§7 question 1` of the proposal as live ("if it does not, the proposal should be withdrawn"). That section no longer exists in the staged spec file; it was deleted before the spec-recheck-r3 snapshot. Do not open a finding on it and do not go looking for it. — EVIDENCE: proposals/0075_.../0075_fix_derive-message-scope-from-the-address-type.spec-changes.md (no `## 7. Open decisions` heading)
FACT: The cache is keyed only on spec-changes + non-spec-changes + implementation-checklist. summary.md is IN SCOPE for this loop but OUT of the cache key, so a cache hit is only safe after diffing summary.md against the snapshot. It was clean here; check it before trusting a hit next time. — EVIDENCE: prompt's CACHE stanza vs its SCOPE stanza
UNVERIFIED: Nobody has executed tier 0 or tier 3 against the replacement gate; the cached coverage note says the green claim rests on a static proto census (26 `SessionId session_id` declarations, no counterexample either way) rather than on a run. The implementor should run `lenny-test --tier 0` and the tier-3 adapter_session_address suite first. — EVIDENCE: scratchpad/cp-cache/0075_.../fresh-r1-2d25f5f323c0.json coverage field


### [non-spec-recheck.1.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens on the non-spec recheck — BECAUSE the whole staging (SPEC-1 + TEST-1 + TEST-2 + checklist S1-S3) touches only spec/04 §4.1 prose about gRPC request-message classification and five test/registry files; §9's files-touched list names no `charts/`, no `pkg/apis/`, no controller, no webhook, and no CRD schema, so there is no status subresource, finalizer, watch, or admission surface for the lens to bite on — ALTERNATIVES: rejected filing on the two K8s-adjacent sentences the staging leaves standing (`spec/04:725` ReportSessionScrub → `sessionsServed` on `agent_pod_state` + drain-request ledger, `spec/04:726` ReportPodScrub → `rewarmStartedAt` on `SandboxClaim.status`), because both name the GATEWAY as the writer, both are pre-existing text SPEC-1 explicitly does not open, and neither becomes wrong after the edits.

FACT: the cache hit at `scratchpad/cp-cache/0075_.../kubernetes-r1-2d25f5f323c0.json` is real and its hash is still current, but its `coverage` text describes only the SPEC lane (it lists spec-changes sections and the r2 snapshot, and never mentions `non-spec-changes.md`). The cache path encodes lens+round but NOT the loop name, so a spec-lane `kubernetes-r1` answer collides with a non-spec-lane `kubernetes-r1` answer whenever the three hashed files are unchanged. I re-did the review rather than returning it blind; the answer came out the same (empty), and I overwrote the entry with a coverage string that names the non-spec staging. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/kubernetes-r1-2d25f5f323c0.json

WATCHOUT: the r3 snapshot diff is a red herring for this loop. `diff -ru scratchpad/cp-snap/0075_.../spec-recheck-r3 proposals/0075_...` returns exactly ONE changed file, the review log (an appended `[f2.cleanup]` block at :1861-1878). `non-spec-changes.md`, `spec-changes.md`, `summary.md`, and the checklist are byte-identical to that snapshot, so the "delta since last convergence" this loop is pointed at is not visible in that diff. Locate it from the review log's open-decisions phase instead of assuming the diff shows it. — EVIDENCE: proposals/0075_.../0075_fix_derive-message-scope-from-the-address-type.review-log.md:1861

FACT: every code anchor in the summary's `## Defects in the shipped tree that this proposal does not stage` section verifies. `pkg/adapter/slot.go:59` is `coord coordinationState` on the per-session slot entry; `pkg/adapter/coordination.go:109-111` is the empty-session-id `InvalidArgument`; `:127-134` is the `coordinator_handoff_stale` `FailedPrecondition`; `:150-156` is the comment on `s.exitHoldState()` stating the hold is pod-scoped and a fence for any bound session exits it. `spec/10_gateway-internals.md:57` and `:60` carry the pod-scoped hold and gauge as claimed, and `spec/28_communication-channels.md:314-317` carries the per-session recording sentence. No attribution defect in that section. — EVIDENCE: pkg/adapter/coordination.go:150-156; spec/10_gateway-internals.md:60

USEFUL [orchestrator note on §4.7]: the pointer that `spec/04:175` still reads `| CoordinatorFenceRequest | Adapter | gateway → adapter | pod |` and `:188` still reads "carries `session_id` and stays pod-scoped, which is why the classification is declared rather than derived" is exact; both confirmed verbatim today, so the debt SPEC-1 closes is live.



### [non-spec-recheck.1.review-mechanism.1]
DECISION: Returned the cached mechanism-lens answer verbatim (empty findings) — BECAUSE the cache key over spec-changes + non-spec-changes + implementation-checklist hashed to 2d25f5f323c0 and `/home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r1-2d25f5f323c0.json` already held this lens's own completed answer over exactly that text; the prompt's CACHE instruction directs returning it and doing no other work — ALTERNATIVES: re-running the full trace, rejected because the three cache-keyed files are byte-identical to the run that produced the cached answer.
FACT: the mechanism-lens cache for this proposal keys ONLY on spec-changes.md + non-spec-changes.md + implementation-checklist.md; summary.md and the review log are outside the key, so a delta confined to summary.md still returns a cache hit — EVIDENCE: the CACHE command in the non-spec-recheck.1 prompt
WATCHOUT: this recheck's delta was described as landing in non-spec-changes.md and summary.md, but the hash matched a prior run, meaning the non-spec staging did not change since that run; a future agent should not read the cache hit as evidence that summary.md was reviewed under this lens — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r1-2d25f5f323c0.json



### [non-spec-recheck.1.review-performance.1]
DECISION: Returned the cached empty-findings answer verbatim instead of re-reviewing — BECAUSE the cache key over spec-changes + non-spec-changes + implementation-checklist hashed to `2d25f5f323c0`, and `scratchpad/cp-cache/0075_.../performance-r1-2d25f5f323c0.json` (written 19:36) already holds this lens's answer over byte-identical staging text; the harness instruction on a cache hit is to return it and do no other work — ALTERNATIVES: re-running the lens, rejected because the staged text has not changed since that run (non-spec-changes mtime 19:07, spec-changes 19:04, summary 19:18, all older than the cached run).
FACT: this lens has now returned empty over this text at least twice — `performance-r1-2d25f5f323c0.json` (19:36) and `performance-r2-2d25f5f323c0.json` (19:54) both exist for the same hash — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r2-2d25f5f323c0.json
WATCHOUT: the cached coverage prose describes its delta as "the deletion of section 7", which is spec-recheck-lane framing; that narrative is stale relative to a non-spec-recheck run even though the findings verdict over the identical text is not — do not read the cached coverage as a statement about which lane last ran — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r1-2d25f5f323c0.json
FACT: the cache filename pattern is `<lens>-r<round>-<hash>.json` where the hash covers only three files (spec-changes, non-spec-changes, implementation-checklist) and NOT the summary, so a summary-only edit will hit a stale cache entry for every lens — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/


### [non-spec-recheck.1.review-reliability.1]

USEFUL [Standing context / Traps 85]: the cache-collision warning fired verbatim on this run. `md5(spec-changes + non-spec-changes + implementation-checklist)` = `2d25f5f323c0`, and `scratchpad/cp-cache/.../reliability-r1-2d25f5f323c0.json` already held a spec-lane reliability answer whose coverage text names only the spec staging. I declined the hit and re-reviewed. A lens that honours the hit blindly reports a spec-lane pass as a non-spec-lane pass.
EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/reliability-r1-2d25f5f323c0.json

DECISION: returned an empty findings list. BECAUSE the whole staging changes a classification and three test files and touches no recovery, retry, fencing, drain, or store-failover mechanism; the one behavioral obligation the reclassification carries (the `spec/05:515` empty-identifier refusal) is already implemented and pinned, and the one live reliability defect on this surface (the equal-generation re-fence) is now recorded in the summary with 0080 §1.16 named as owner. ALTERNATIVES: filing the missing "no 1-second backoff" half of the re-fence defect entry (rejected: the omission from an unstaged-defect inventory makes neither the applied spec nor the implementation wrong, and Open item 105 already carries it); re-filing the hold-exit-without-a-bound-session tension (rejected by Trap 74 on four prior derivations).

FACT: every citation in the summary's `## Defects in the shipped tree that this proposal does not stage` re-fence entry is exact, checked one anchor at a time. `spec/10_gateway-internals.md:39` carries "with the same generation value (up to 3 attempts with 1-second backoff)"; `:38` carries the 5-second deadline; `pkg/adapter/coordination.go:127-134` is the `coordinator_handoff_stale` FailedPrecondition; `coordfence.go:180-183` is the `default:` transient arm (`f.incRetry()` at `:182`) and `:171-179` is the stale arm's re-read through `return f.relinquish`. The "gives up on the second of three attempts" trace reproduces: attempt 1 deadline-exceeded goes to the transient arm, attempt 2 at the same generation hits `gen <= lastFenced`, the stale arm re-reads, finds no advance, and relinquishes.
EVIDENCE: spec/10_gateway-internals.md:38,39; pkg/adapter/coordination.go:127-134; pkg/gateway/coordination/coordfence/coordfence.go:171-183

FACT: the §4.7-row defect entry's code half also verifies. `pkg/adapter/holdstate.go:335` and `:348` are the unary and stream hold interceptors keyed on `coordinatorHoldAllowedMethods`, and `pkg/adapter/coordination.go:150-156` is the `s.exitHoldState()` call whose own comment states a fence for any bound session is the exit and gives the registry/entry/hold lock order. `spec/28_communication-channels.md:322` is the "hard precondition for every other operational RPC to the pod" sentence.
EVIDENCE: pkg/adapter/holdstate.go:335,348; pkg/adapter/coordination.go:150-156; spec/28_communication-channels.md:322

FACT: `boundSlotState` refuses an unbound or unregistered session with `FailedPrecondition "session %s is not assigned to this pod"`, which is the "resolving nothing is the refusal" claim in the problem statement §1.2. It is at `pkg/adapter/slotsession.go:274-283`, not in `coordination.go`, and no round had pinned that file before.
EVIDENCE: pkg/adapter/slotsession.go:274-283

FACT: both tier-3 address arms are green on the 8 messages TEST-2 adds, checked in the file rather than derived. `TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` asserts only the absence of a field named `slot_id` (`:87-90`), and `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` asserts `session_id` exists and is `lenny.adapter.v1.SessionId` (`:107-114`). Neither touches `reserved`, so the widened set costs nothing. Every one of the 8 proto anchors in `non-spec-changes.md:77-82` resolves exactly.
EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:87-90,107-114; schemas/lenny-adapter.proto:341-343,364-368,383-385,403-406,419-424,1455-1461,1538-1549,1673-1684

WATCHOUT: `diff -ru` against the only available snapshot (`cp-snap/.../spec-recheck-r3`) returns the review log alone, so a non-spec-lane recheck gets no visible delta from it. Trap 84 says this; it is still true and there is no earlier non-spec snapshot left on disk. The real newest text is `non-spec-changes.md` §4 and the TEST-2 split in §5 (the fix stage Trap 68 describes) and the summary's OD/defect sections.
EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-r3


### [non-spec-recheck.1.review-security.1]

DECISION: Returned the cached security-r1 answer for hash `2d25f5f323c0` unchanged (empty findings) — BECAUSE the cache key covers spec-changes + non-spec-changes + implementation-checklist, and `scratchpad/cp-cache/0075_.../security-r1-2d25f5f323c0.json` was written at 19:34, i.e. AFTER the 19:07 non-spec edit, so it hashes today's bytes of all three files. ALTERNATIVES: re-running the whole lens from scratch; rejected after I independently re-checked the two things the non-spec lane could plausibly break under this lens (below) and found the cached conclusion still holds.

FACT: The cache filename carries no lane label, only `<lens>-r<n>-<hash>`, so a spec-recheck run's cache is served to a non-spec-recheck run whenever the three hashed files are byte-identical. The cached coverage text will therefore describe a DIFFERENT lane's reading order. That is not a defect, but a future agent should not read the cached coverage as evidence that the non-spec staging was read. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r1-2d25f5f323c0.json (mtime 19:34; non-spec-changes.md mtime 19:07)

FACT: Nothing in the runtime keys on the §4.1 message-scope classification. A grep for `messageScope`/`MessageScope`/`podScoped` across `pkg/` and `cmd/` returns only prose comments and the unrelated `SendMessage` scope enum in `pkg/gateway/mcpfabric/mcptools/messaging_handler_test.go:66-87`. The adapter's own hold-state allowlist is keyed on gRPC full-method names, not on message class. EVIDENCE: pkg/adapter/holdstate.go:52-58; pkg/adapter/holdstate.go:113
  Consequence for this lens: retiring the §4.1 table (SPEC-1) and replacing its tier-0 gate (TEST-1) cannot widen, weaken, or feature-gate any runtime authorization, isolation, or fail-closed predicate. The retired gate reads spec text and proto text only, and says so itself.
  EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:25-27 ("The gate reads the specification text and the proto text alone. Whether a handler enforces the scope a row declares is a runtime question the tier-1 and tier-3 suites own.")

FACT: TEST-2 only ADDS members to `sessionScopedMessages` and leaves `retiredDuplicateNumbers` with today's population, so both tier-3 assertion arms get strictly wider and the reservation arm is unchanged. No assertion is dropped or narrowed, so there is no control regression on the test side either. EVIDENCE: proposals/0075_.../0075_....non-spec-changes.md ("`retiredDuplicateNumbers` ... keeps today's name-to-number entries verbatim"); tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130

FACT: The staging contains no security bound of the kind check (2) hunts for: no residual-state limit, no quota or reuse counter, no Redis-backed role, and no value sourced from a pod self-report. The single input the derived classification reads is `schemas/lenny-adapter.proto`, a repo artifact under review. A future security reviewer on this proposal can confirm that in one pass and stop; the lens has no purchase on a message-classification rule.

WATCHOUT: Do not file the tier-0 gate retirement as "removing a mandatory control". It is open rather than silent — the replacement gate's four refusals AND its blind spot are stated in normative staged spec text and again in summary OD1 — and it was already examined and cleared on the spec lane. EVIDENCE: proposals/0075_.../0075_....spec-changes.md:20-25


### [non-spec-recheck.1.review-test-coverage.6]

DECISION: Returned an EMPTY findings list for the test-coverage lens, the sixth empty pass on this proposal — BECAUSE the delta since [non-spec.5.review-test-coverage.1] adds no deliverable, no tier, and no case: `spec-changes.md` lost its `## 7. Open decisions for review`; `non-spec-changes.md` gained two purely evidential paragraphs (the gate-file-path retention at `:43-48` and the widened-membership proto anchors at `:74-82`); `summary.md` replaced OD2 with OD4 and OD5 and gained two unstaged-defect entries. §8 still maps one enumerated tier-0 negative case onto each of the four refusals the SPEC-1 block mandates, plus the tier-3 fence-address constraint, with tiers 0, 3, and 11 named — ALTERNATIVES: filing tier 11 as named-but-uncased (rejected a fifth time; re-verified that no tier-11 case opens §4.1's block); filing the absent tier-1 case for the newly session-scoped fence (rejected: `spec/05:515`'s empty-identifier refusal is the only imported obligation and `pkg/adapter/coordination_test.go:35` already pins it); filing S3's tier list omitting tier 0 (rejected: repo convention, 0076's S8 precedent, and two lenses already declined it); filing "nothing pins that the extended `protoFields` still reports the same field set" (rejected: [non-spec.5] already ruled it a mechanism subject, not a testing gap).

FACT: every one of the eight proto anchors the NEW `non-spec-changes.md:74-82` paragraph cites resolves exactly, and each cited message declares `SessionId session_id = 1` at the top level with no `slot_id` field, so TEST-2's claim that both tier-3 address arms are green for each added member is verified rather than asserted. `ListPlatformToolsRequest` `:341-343`, `CallPlatformToolRequest` `:364-368`, `ListSessionConnectorsRequest` `:383-385`, `ListConnectorToolsRequest` `:403-406`, `CallConnectorToolRequest` `:419-424`, `CoordinatorFenceRequest` `:1455-1461`, `ExportPathsRequest` `:1538-1549`, `ConfigureWorkspaceRequest` `:1673-1684`. A future round can spot-check rather than re-derive.
EVIDENCE: schemas/lenny-adapter.proto:341-343,364-368,383-385,403-406,419-424,1455-1461,1538-1549,1673-1684

FACT: the NEW gate-file-path paragraph's five case anchors are exact. `slotAddressCaseFiles` lists `tests/tier0_static/adapter_proto_message_scope_test.go` at `:336` and `adapter_proto_parse_test.go` at `:337`, and the five cases ranging over that list are `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:973`), `TestAddressCaseNamesAgreeWithTheirOwnCitations` (`:1028`), `TestAddressCaseTextCitesNoProposalSectionNumber` (`:1054`), `TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly` (`:1096`), and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`:1110`), each resolving through `repoFileLines` (`:699-706`), which `t.Fatalf`s on an unreadable file.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336,337,699-706,973,1028,1054,1096,1110

FACT: `retiredDuplicateNumbers`'s stated membership rule checks out mechanically. `grep -c 'reserved "slot_id"' schemas/lenny-adapter.proto` returns 18 and `sessionScopedMessages` carries exactly 18 keys today, so "keeps today's entries verbatim" and "its members are the messages that declare `reserved \"slot_id\"`" are the same set rather than two claims that could drift apart.
EVIDENCE: schemas/lenny-adapter.proto:465,695,731,856,935,972,997,1030,1052,1076,1097,1133,1211,1324,1412,1502,1600,1619; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: `spec/05_runtime-registry-and-pool-model.md:515` imposes the empty-identifier refusal alone on a session-scoped request. It says nothing about a malformed identifier, so the reclassification imports no second arm and `addressRuleCases` (`tests/tier0_static/address_rule_citation_test.go:29-48`) owes the fence no entry. The handler does no format validation either: `pkg/adapter/coordination.go:108-110` refuses the empty value and then hands the identifier straight to `boundSlotState` (`:116`). This closes the "the fence is now session-scoped, where is its malformed-address arm" question, which the earlier tier-1 note did not explicitly cover.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515; pkg/adapter/coordination.go:108-116; tests/tier0_static/address_rule_citation_test.go:29-48

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its FACT that the reclassification's single imported behavioral obligation is already pinned held a fifth time and again did the whole tier-1 question in one step.

USEFUL [non-spec.5.review-test-coverage.1]: its FACT that tier 11 carries no gate reading §4.1 saved re-deriving the fourth-most-refiled candidate on this proposal. I re-ran the grep and it holds: `Request Message Scope` occurs at `spec/04_system-components.md:149` and in the two tier-0 files alone, and the tier-11 hits for "request message scope" are `// spec:` annotation text in `basic_level_echo_stamp_doc_reconciliation_test.go`, which reads JSONL frame literals in docs rather than §4.1's block.

WATCHOUT: the cache key is `md5(spec-changes + non-spec-changes + implementation-checklist)`, so the summary's OD2 -> OD4/OD5 rewrite and its two new unstaged-defect entries do NOT change it. This lens ran under key `2d25f5f323c0`, the same key an earlier firing on this staging would have used. Any lens that owns summary text must ignore a cache hit on this key.
EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/test-coverage-r1-2d25f5f323c0.json

OPEN: §8 still says "The specific cases are written during convergence" while convergence has now closed on this lane with no case named by function name. Six test-coverage passes have judged the five enumerated case subjects (four tier-0 refusals plus the tier-3 constraint) sufficient, so this is a stale sentence rather than a coverage gap, but a fix pass that touches §8 should retire the clause.


### [non-spec-recheck.2.review-applicability.1]

DECISION: declined the cache hit at `scratchpad/cp-cache/0075_.../applicability-r2-2d25f5f323c0.json` and re-reviewed — BECAUSE its own `coverage` string ends by naming what it could NOT check as "every remedy there lands in non-spec-changes.md or tests/, **which this loop's scope excludes**", which is a spec-lane answer. This loop's scope is spec-changes + non-spec-changes read as one document plus the summary. Returning it would have reported a spec-lane pass as a non-spec-lane pass. ALTERNATIVES: returning it verbatim as the CACHE stanza directs; rejected because the stanza's premise ("your own answer to this same question") is false when the path carries no lane component. This is the fourth lens to hit this collision (see Traps 87 and the r1 kubernetes/reliability/edit-sites entries).
EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/applicability-r2-2d25f5f323c0.json

DECISION: returned an empty findings list. BECAUSE I simulated S1→S2→S3 against the tree and every anchor, created artifact, lane, Depends-on, tier list, and gate-state transition resolved. ALTERNATIVES: filing S3's tier list omitting tier 0 (rejected a third time, see below); filing the stale `IMPLEMENTOR TO FILL THE BLANKS` banner in `non-spec-changes.md:25-27` and the "written during convergence" clause in §8:102 (rejected: §5's content names every target, identifier, type, and membership rule explicitly, and §8 enumerates five concrete case subjects, so no implementor is forced to invent anything the proposal owed); filing the summary's OD numbering gap (rejected, see below).

FACT: `diff -ru` of the `non-spec-recheck-r2` snapshot against the live proposal directory returns EMPTY, including the review log. Trap 86 predicted this. The newest text in this lane is still `non-spec-changes.md` §4 + the two §5 paragraphs at `:43-48` and `:74-82`, and `summary.md`'s OD4/OD5 and three unstaged-defect entries; get it with `git diff 470cd4e34 HEAD -- <proposal dir>`.
EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r2

FACT: the checklist simulates clean. Three staged deliverables, three steps, one lane each (S1 spec, S2 test, S3 test), spec step leads, `Depends-on` names only earlier steps (S2→S1, S3→S1), every box unchecked, no deliverable in two steps and no step naming an unstaged deliverable. S1's red window (`parseMessageScopeTable` left with zero rows) is the only existing gate S1 reddens and its disposition is on S1's own line.
EVIDENCE: 0075_...implementation-checklist.md:3-22

FACT: the class-2 created-artifact worklist for the non-spec lane is small and fully specified. `retiredDuplicateNumbers` gets its exact spelling, its Go type (`map[string]protoreflect.FieldNumber`), its membership rule, and its file; `sessionScopedMessages`'s new type (`[]string`) and its exact 26-name membership (18 current keys + 8 named additions) are stated; the gate file path is stated and its retention is justified against `slotAddressCaseFiles:336`. The one property left open, the replacement gate's case names, is consumed only inside S2 (the file rewrite and the `tests/spec-map.json` re-registration land in the same step), so nothing cross-step is forced to guess.

FACT: the derivation set arithmetic reconciles EXACTLY, re-derived with a brace-depth proto parse. 31 RPC request types; 25 declare a top-level `SessionId session_id`; the derived session set (those 25 plus `CheckpointStart`) is 26 names, and `sessionScopedMessages`'s 18 current keys plus TEST-2's 8 additions is the same 26 names with zero symmetric difference. Separately, the 18 messages declaring `reserved "slot_id"` are byte-identical to the 18 current keys, so the split's two populations are each exactly stated. A future round can spot-check rather than re-derive.
EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the S1 gate sweep is clean beyond the disclosed red window, checked directly rather than inherited. `#### Request Message Scope` occurs at `spec/04_system-components.md:149` and nowhere else in `spec/` or `docs/`, so nothing links the anchor; the heading walker's domain is `## N` / `### N.M` plus §28.5 cards and `spec/README.md` carries no entry for a bare `####` heading, so the index is untouched; `tests/registers/identifier-senses.yaml`'s eight `spec/04` rows key on retired channel spellings and the deleted block carries none; `gate_integrity_test.go:63-89` omits the gate; and `tests/change-graph.json`, `tests/claim-map.json`, and `TEST-GAPS.md` name none of the five touched test surfaces.
EVIDENCE: tests/tier0_static/heading_walker_test.go:44-47; tests/registers/identifier-senses.yaml:13-36; tests/tier0_static/gate_integrity_test.go:63-89

FACT: the blast radius of the shared parse is exactly two callers, confirmed by one grep over `tests/ pkg/ cmd/`: `protoFields` is called at `claim_register_proto_agreement_test.go:64` alone and `protoServiceRequests` at `adapter_proto_message_scope_test.go:87` alone, and no file outside the gate reads `parseMessageScopeTable`, `declaredScope`, `messageScopeRow`, `scopeRow`, `messageScopeDisagreements`, `messageScopeSpecPath`, `checkpointStartMessage`, or `checkpointStartService`. The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) read proto and Go doc comments and touch neither the shared parse nor `spec/04` §4.1.
EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:1-40; tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:1-40

FACT: the deleted table's Direction column loses no content. §4.7.1 splits its RPC tables under `*Gateway → Adapter RPCs:*` (`spec/04_system-components.md:698`) and `*Adapter → Gateway RPCs:*` (`:721`), so direction survives per RPC, and the Service column is derivable from the proto. I opened this as a class-3 relocation-loses-content candidate and closed it.
EVIDENCE: spec/04_system-components.md:698,721

FACT: 0073's §8 really does add the tier-0 gate and really does extract the shared parse, so `non-spec-changes.md:31-32`'s attribution is exact. The section is at `proposals/0073_...md:4678-4696`; the gate's creating commit is `fb2af5f9c`. `grep adapter_proto_message_scope_test proposals/0073_...md` returns NOTHING, so a grep by file name refutes nothing; grep the §8 prose instead.
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:4680-4696

WATCHOUT: `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`spec_map_slot_address_registration_test.go:1108-1132`) runs in the direction opposite to the one the proposal's argument needs, and reading it the wrong way makes the gate-file-path paragraph look load-bearing where it is not. It fails when `slotAddressCaseFiles` OMITS a file, never when it carries a surplus one. So keeping the path is safe and dropping it would be too; what actually forces the path is the five cases that resolve each entry through `repoFileLines` (`:699-706`), which `t.Fatalf`s on a file it cannot read.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1132,699-706

WATCHOUT: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` takes the per-case branch for the tier-0 gate file (spec-map carries `path::Test` rows at `:156`, `:169`, `:5670` and no whole-file row) and the whole-file branch for the tier-3 file (`:172`, `:600`, `:1110`, `:1391`, `:3678`). That asymmetry is why TEST-1 owes a spec-map edit and TEST-2 owes none, and why OD4's answer decides an annotation rather than the reverse: the gate credits FROM the `// spec:` annotation.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:970-989

UNVERIFIED: S3's tier list omitting tier 0 is now declined by four applicability-class passes and filed by one (`non-spec.2.review-applicability.1`). My ground for declining is narrower than "repo convention": tier 0's only reach into that file is `go vet -tags=contract ./tests/tier3_contract/...` (`cmd/lenny-test/cmd_run.go:503-509`), and S3's own tier-3 run compiles the same package, so the omission costs nothing an implementor would not hit anyway. 0076's S8 is a real precedent but is itself the same omission, so it is convention rather than justification. Someone with authority over the checklist contract should close Open item 117 rather than let a sixth pass re-derive it.
EVIDENCE: cmd/lenny-test/cmd_run.go:503-509; proposals/0076_.../0076_...implementation-checklist.md (S8, "Tiers 1, 4, 7a")

OPEN: `summary.md`'s open-decisions section numbers OD1, OD4, OD5 with no OD2 and no OD3. I checked every non-review-log file in the directory: no text anywhere cross-references this proposal's own OD2 or OD3 (every `OD2`/`OD3` mention is qualified as 0076's), so nothing dangles and I did not file it. It still reads to a human reviewer as two decisions silently dropped. A cleanup pass that touches the summary should renumber or say why the gap stands.
EVIDENCE: 0075_...summary.md:85,142,153

USEFUL [Standing context / Traps, "Cache keys omit summary.md and review-log.md"]: this was the single highest-value entry on the file for me. It is what made me open the cached JSON and read its coverage string instead of returning it, which is the difference between a review and a replay.

USEFUL [Standing context / Traps, "Naive proto parsers derail on this file"]: comment-stripping and per-line brace counting first time out; the census came back correct on the first run rather than after two wrong ones.



### [non-spec-recheck.2.review-citations.1]
DECISION: Returned the cached citations-lens answer verbatim (empty findings) instead of re-running the audit — BECAUSE the cache protocol's hash `2d25f5f323c0` over spec-changes + non-spec-changes + implementation-checklist matched, and the cache file's mtime (19:56) is later than the mtime of every file in the proposal directory except the review log (spec-changes 19:04, non-spec-changes 19:07, summary 19:18), so the cached run read exactly the text this round was asked to review — ALTERNATIVES: re-running the full audit, rejected because the protocol directs returning the cached JSON and no proposal text has changed since it was written.
FACT: cache entries in this directory are keyed by lens+round+content-hash but NOT by lane, so a `citations-r2-<hash>.json` written by the spec-recheck lane is served to the non-spec-recheck lane's r2 when the three hashed files are unchanged. That is intended reuse here because the hash covers both staged documents, but read the cached `coverage` prose carefully: it may name a different lane's snapshot directory — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/citations-r2-2d25f5f323c0.json
WATCHOUT: the hash does NOT cover `...summary.md`, so a cache hit alone does not prove the summary was unchanged. Check mtimes before trusting a hit when the summary is in scope — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md (mtime 2026-09-07 19:18, earlier than the 19:56 cache write)


### [non-spec-recheck.2.review-client-surface.1]
FACT: The client-surface lens returned an empty findings list over this exact staging text (md5 prefix 2d25f5f323c0 over spec-changes + non-spec-changes + implementation-checklist), cached from an interrupted run and replayed verbatim this round. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/client-surface-r2-2d25f5f323c0.json:1
FACT: The retired §4.1 scope table has no mirror on any client-facing surface. docs/, sdks/, charts/, schemas/*.json and the OpenAPI document carry no per-message scope column, so SPEC-1's table retirement creates no unstaged client-facing edit site. EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:1
DEFERRED [docs/api/internal.md]: docs/api/internal.md:209-215 publishes a CheckpointRequest excerpt that does not match the shipped schemas/lenny-adapter.proto. It is already false before this proposal's edits and its remedy is a docs edit outside this loop's scope; it belongs in the residue register (proposal 0080) rather than in 0075. EVIDENCE: docs/api/internal.md:209



### [non-spec-recheck.2.review-docs-alignment.1]
DECISION: Returned the cached docs-alignment result (empty findings) verbatim rather than re-reviewing — BECAUSE the cache probe at scratchpad/cp-cache/0075_.../docs-alignment-r2-2d25f5f323c0.json hit on the current hash of spec-changes+non-spec-changes+implementation-checklist, AND `diff -rq scratchpad/cp-snap/0075_.../non-spec-recheck-r2 proposals/0075_...` reports no differences at all, so this round has no delta for the lens to examine — ALTERNATIVES: a full re-read of the staging, rejected because the cache instruction is unconditional and the text is byte-identical to the snapshot this round was told to diff against.
FACT: The whole live proposal directory is byte-identical to the round's own "before" snapshot, so the non-spec-recheck.2 firing ran with an empty delta. EVIDENCE: `diff -rq scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r2 proposals/0075_fix_derive-message-scope-from-the-address-type` returns nothing.
WATCHOUT: The cache filename does not encode the LANE, only the lens and the round number (`docs-alignment-r2-<hash>.json`), and the hash covers only spec-changes.md, non-spec-changes.md, and implementation-checklist.md — summary.md and problem-statement.md are outside the hash. The entry served to this non-spec-recheck run was written by the SPEC-recheck lane's docs-alignment reviewer: its own coverage text says "snapshot spec-recheck-r2 is byte-identical to the live directory" and it lists the files it read without summary.md. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/docs-alignment-r2-2d25f5f323c0.json. If a future firing changes summary.md alone, the hash will not move and a stale cross-lane cache entry will be served as that lane's answer.
FACT: Snapshot `non-spec-recheck-r4` already exists and DIFFERS from the live tree on six files, while `non-spec-recheck-r2` matches it exactly. Snapshot round numbers here do not order monotonically against the live directory, so do not infer "later snapshot = closer to live". EVIDENCE: `diff -rq scratchpad/cp-snap/0075_.../non-spec-recheck-r4 proposals/0075_...` lists implementation-checklist, non-spec-changes, review-log, spec-changes, and summary as differing.
USEFUL [cache probe]: The cache probe saved a full re-review of ~30 verified citations under this lens; its recorded coverage names every anchor it resolved (spec/04:149/151/153-186/188/190, spec/04:725/726, spec/10:57/60, spec/05:515, schemas/lenny-adapter.proto:458/499-503/1217/1455-1456, docs/reference/adapter-contract.md:81), which is the list a future docs-alignment pass should re-verify rather than rediscover.



### [non-spec-recheck.2.review-edit-sites.1]

WATCHOUT: the orchestrator's CACHE instruction can hand you ANOTHER LANE's answer. `edit-sites-r2-$H.json` carries no lane in its path, so the spec-lane and the non-spec-lane edit-sites reviewers at round 2 collide on one key whenever the three hashed files are byte-identical. The cached JSON I was handed opened "Walked every section of the staged spec edits" and described its delta as "the deletion of §7 (Open decisions)", which is the spec lane's delta, not this lane's. I did the review anyway; it also returned empty, so no harm this time. Read the cached `coverage` before trusting it. — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r2-2d25f5f323c0.json

WATCHOUT: `diff -ru` against the snapshot the prompt names (`non-spec-recheck-r2`) returns NOTHING, because that snapshot is taken at the start of this round. The delta this lane has not read is against `non-spec-recheck-r4` (13:15), the last convergence in this lane. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r4/

FACT: the widened tier-3 session set is EXACTLY complete, and it is checkable in one command. `grep -c '^\s*\(repeated \)\?SessionId ' schemas/lenny-adapter.proto` returns 26, every one of them spelled `session_id` at two-space (top-level) indent. Today's `sessionScopedMessages` holds 18; the 8 TEST-2 adds bring it to 26. So after TEST-2 the set is every message the derivation rule addresses to a session, with nothing left out. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

FACT: the two populations TEST-2 splits are provably disjoint-by-rule and the split is verifiable mechanically. `awk '/^message /{n=$2} /reserved "slot_id"/{print n}' schemas/lenny-adapter.proto` returns exactly the 18 names in today's map, in the same order of membership. The retirement commit is 040323634, which added 18 `reserved "slot_id"` pairs and never touched `CoordinatorFenceRequest`. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63; schemas/lenny-adapter.proto:465,695,731,856,935,972,997,1030,1052,1076,1097,1133,1211,1324,1412,1502,1600,1619

FACT: nothing outside `adapter_proto_message_scope_test.go` reads any identifier TEST-1 retires. `messageScopeSpecPath`, `checkpointStartMessage`, `checkpointStartService`, `messageScopeRow`, `scopeRow`, `parseMessageScopeTable`, `declaredScope`, and `messageScopeDisagreements` each have zero references elsewhere in package `tier0_static`; `protoFields` has exactly one other caller and `protoServiceRequests` has none. The 0076-landed siblings `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go` reference neither the §4.1 table nor the shared parse. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:64; tests/tier0_static/adapter_proto_parse_test.go:68

FACT: the §4.1 message-scope table has exactly one mirror surface in the whole tree, and it is the gate's own fixture. `grep -rn "| Request message" spec/ docs/ schemas/ charts/ tests/` returns spec/04_system-components.md:153 and tests/tier0_static/adapter_proto_message_scope_test.go:167 and nothing else. `Request Message Scope` as a heading or anchor appears only at spec/04_system-components.md:149, so retiring the block breaks no inbound link. The proto carries no scope classification in any doc comment, including `CoordinatorFenceRequest`'s at schemas/lenny-adapter.proto:1449-1454. — EVIDENCE: spec/04_system-components.md:149,153

FACT: after SPEC-1, `spec/04_system-components.md:726` (ReportPodScrub) is the only sentence left in `spec/` classifying a request message pod-scoped. Every other `pod-scoped` hit in spec/ is about something else: spec/10:60 the hold and its gauge, spec/04:872 the rotation ceiling, spec/12:202 Redis key prefixes, spec/13:668 an advisory lock. The staging's claim to that effect holds.

FACT: the address-rule uniqueness gate is a trap SPEC-1 happens to miss. `sectionStatingTheAddressRule` FATALs unless exactly one spec section carries the literal "rejected at the adapter boundary with `InvalidArgument`". That sentence lives in spec/05_runtime-registry-and-pool-model.md:515 and nowhere in §4.1, and the staged block does not introduce it, so the gate stays green. Any future rewrite of §4.1 that restates the refusal in those words turns tier 0 red. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22,85-87

MISTAKE: the orchestrator's brief anchors `SessionId` at `schemas/lenny-adapter.proto:589`. The message opens at :596; :589 is a closing brace in the error-code enum. The proposal's own citation (:596, problem-statement line 38) is the correct one. Do not "correct" the proposal toward the brief. — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: every citation in the delta text resolves, including the ones that are cheapest to get wrong. Verified this round: docs/api/internal.md:209-215 (the stale `CheckpointRequest` protobuf excerpt), :272-274 (`DemoteSDKRequest`), :94 (the `UploadFiles` RPC that exists in no service); schemas/lenny-adapter.proto:1173-1187, :1694-1698, :32, :261, :499-503; tests/tier11_docs/code_blocks_test.go:103-113 (dispatches on json/yaml/go/bash/sql, so no `protobuf` fence is ever parsed); spec/04:712 and docs/reference/adapter-contract.md:69 (the compressed `CoordinatorFence` row and its mirror); tests/tier11_docs/spec_47_rpc_row_naming_test.go:35 (first column only, never the description); pkg/adapter/slot.go:59, holdstate.go:335,:348, coordination.go:109-111,:150-156; spec/10:38,:40,:57,:60; spec/28:314-317,:322; tests/tier0_static/spec_map_slot_address_registration_test.go:336,:699-706,:973,:1028,:1054,:1096,:1110; tests/tier0_static/address_rule_citation_test.go:45-47; tests/spec-map.json:156,:169,:5670.

DECISION: returned an empty findings list — BECAUSE the identifier inventory the delta touches (`sessionScopedMessages` retyped to `[]string`, the new `retiredDuplicateNumbers`, and the eight retiring tier-0 identifiers) has no reader outside the two files §9 already lists, and no spec/, docs/, schemas/, or charts/ surface becomes wrong once the table goes — ALTERNATIVES: I considered filing the widened set as incomplete-on-day-one, and refuted it by counting: 18 + 8 = 26 = every message in the file declaring the address, so the set is exhaustive against its own stated rule the moment TEST-2 lands. I also considered `docs/api/internal.md` as a missed edit site under (d), and refuted it: its excerpts contradict the shipped proto today, before anything staged here applies, so SPEC-1 does not make it wrong.

UNVERIFIED: nothing was executed. That the replacement tier-0 gate and the amended tier-3 suite go green is derived from reading the descriptor source alone. A verifier with a toolchain should run `go test ./tests/tier3_contract/adapter_session_address/ -tags contract` after S3.



### [non-spec-recheck.2.review-feasibility.1]
DECISION: Returned the cached feasibility answer verbatim instead of re-reviewing — BECAUSE /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/feasibility-r2-2d25f5f323c0.json exists for the current staged-text hash, and the cache protocol says an exact-hash hit is this lane's own interrupted answer over identical text — ALTERNATIVES: re-running the whole lens, rejected because the hash covers spec-changes + non-spec-changes + implementation-checklist and none of the three has changed since that run.
FACT: the actor-action lens finds nothing to engage on this proposal: the only actor the staging assigns an action to is the tier-0 gate, and no controller, CRD subresource, RBAC rule, egress rule, webhook, or spec/18 phase deliverable is touched — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/feasibility-r2-2d25f5f323c0.json
WATCHOUT: the cache key hashes only the three staged files, so a summary.md-only edit (which is what this recheck round was launched for) does NOT invalidate it; a future lens whose scope genuinely includes summary.md should not trust a hit from a lens that never read it — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md (mtime 19:18, later than non-spec-changes.md at 19:07)


### [non-spec-recheck.2.review-fresh.1]

USEFUL [standing-context Traps: "Cache keys omit summary.md and review-log.md"]: this saved the round. The cache slot `scratchpad/cp-cache/0075_.../fresh-r2-2d25f5f323c0.json` was already populated by the SPEC-recheck lane's r2 fresh lens (its own `coverage` string says "Delta since the spec-r2 snapshot is one hunk: the deletion of the whole section 7 open-decisions block"). The brief orders "return exactly it and do no other work". Returning it would have skipped this lane's entire delta, because `summary.md` (19:18) changed after `non-spec-changes.md` (19:07) and the key covers neither summary nor lane. I did the review instead and overwrote the slot.

USEFUL [standing-context Traps: "Snapshot diffs are routinely empty"]: `diff -ru scratchpad/cp-snap/.../non-spec-recheck-r2 proposals/...` printed nothing, and so did `non-spec-recheck-r1-start` and `spec-recheck-r3`. The lane's real delta is against `non-spec-r6` (16:35), the last non-spec-lane convergence. That is the snapshot to diff on this lane.

FACT: this lane's delta is exactly four hunks. (1) `spec-changes.md` lost its whole `## 7. Open decisions for review` block. (2) `non-spec-changes.md` gained a paragraph at `:43-48` on keeping the gate's file path, and a paragraph at `:74-82` proving both tier-3 address arms green on the eight added members. (3) `summary.md` replaced OD2 with OD4 (the §28.5.3 registration) and OD5 (the unheld widened set). (4) `summary.md` gained two entries under `## Defects in the shipped tree that this proposal does not stage`: the stale `docs/api/internal.md` protobuf excerpts and the §4.7 `CoordinatorFence` row's pod-wide announcement clause. — EVIDENCE: diff against scratchpad/cp-snap/0075_.../non-spec-r6

FACT: every citation in all four delta hunks verifies. Re-derived independently: `slotAddressCaseFiles` entry (`tests/tier0_static/spec_map_slot_address_registration_test.go:336`) and its five loops (`:973`, `:1028`, `:1054`, `:1096`, `:1110`); all eight added members declare `SessionId session_id = 1` at top level and no `slot_id` (`schemas/lenny-adapter.proto:341-343`, `:364-368`, `:383-385`, `:403-406`, `:419-424`, `:1455-1461`, `:1538-1549`, `:1673-1684`); `docs/api/internal.md:209-215`, `:272-274`, `:94` against `schemas/lenny-adapter.proto:1173-1187`, `:1694-1698`, and the absence of `UploadFiles` under `schemas/`; `tests/tier11_docs/code_blocks_test.go:102-113` dispatches on json/yaml/go/bash/sql only; `spec/04_system-components.md:712` carries both clauses of the fence row and sits in §4.7.1; `spec/28_communication-channels.md:314-317`, `:322-323`, `:499`; `spec/10_gateway-internals.md:38`, `:39`, `:40`, `:57`, `:60`; `pkg/adapter/slot.go:59`, `pkg/adapter/holdstate.go:335`/`:348`, `pkg/adapter/coordination.go:150-156`; `tests/tier11_docs/spec_47_rpc_row_naming_test.go:35`; `tests/spec-map.json:156`/`:169` under 4.1 and `:5670` under 28.5.3 (confirmed by walking back to the enclosing section key rather than trusting the line).

FACT: an independent brace-tracking parse of `schemas/lenny-adapter.proto` reproduces the settled census exactly and, additionally, closes TEST-2's split arithmetic mechanically: the 25 addressed RPC request types are precisely the 17 request types already in `sessionScopedMessages` plus the 8 the proposal adds, with no remainder in either direction. The six unaddressed request types are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. A future round can stop re-deriving this. — EVIDENCE: schemas/lenny-adapter.proto; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: the `SessionId` biconditional over the whole file is 26 declarations, all spelled `SessionId session_id`, at numbers 1 (24 sites), 2, and 7. No field named `session_id` carries another type. The replacement gate is green on the shipped proto on its first run. — EVIDENCE: grep over schemas/lenny-adapter.proto

FACT: `tests/tier3_contract/adapter_session_address/send_message_stamp_test.go`, the sibling in TEST-2's package, references none of `sessionScopedMessages`, `retiredFieldName`, `retiredWrapperName`, `reservesNumber`, `reservesName`, or `messageDescriptors`. Changing `sessionScopedMessages` from a map to a `[]string` cannot break it, so TEST-2's blast radius really is the one file. — EVIDENCE: grep over tests/tier3_contract/adapter_session_address/send_message_stamp_test.go

FACT: `address_rule_citation_test.go`'s "exactly one spec section carries the address-rule sentence" gate is safe against SPEC-1 as staged. The sentence "rejected at the adapter boundary with `InvalidArgument`" occurs in exactly one spec file today and it is not the block SPEC-1 deletes. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515 is the sole hit across spec/

FACT: no tier-11 case reads the `#### Request Message Scope` block. `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` is the only tier-11 file matching "message scope", and every hit is a `// spec: 4.1` annotation rather than a read of the text (`:26`, `:132`, `:284`, `:408`, `:582`, `:747`). S1's spec edit reddens tier 11 nowhere; §8 listing tier 11 is conservative rather than owed.

FACT: nothing in `spec/`, `docs/`, `schemas/`, or `charts/` links or names the §4.1 table, and the only non-`proposals/` mention of `CoordinatorFenceRequest` outside `schemas/` and Go code is `gateway-runtime-comms.md:2316`, a root planning document that states no scope class. No edit site is missed.

DEFERRED [0075_....non-spec-changes.md]: `:46-47` says of the five loops over `slotAddressCaseFiles` that "each resolves an entry through `repoFileLines` (`:699-706`), which fails the case on a file it cannot read". That is false of the fifth. `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (`tests/tier0_static/spec_map_slot_address_registration_test.go:1110`) only builds a set from the inventory and compares it against `addressRuleGateFile`, `addressRuleCases`, and `derivedInventoryCaseFiles`; it never reads the named file. What is true instead: four of the five (`:973`, `:1028`, `:1054`, `:1096`) resolve the entry through `repoFileLines` and fail on a file they cannot read, and the fifth checks inventory membership alone. I did not file it: the paragraph's conclusion, that keeping the path leaves the inventory correct and confines the change to the §9 file list, stands on the four either way, and this is the same shape as the D2 "nothing checks today" finding the material skeptic refuted this round.

UNVERIFIED: still nobody has executed tier 0 or tier 3 against the applied edits. Every greenness claim on this proposal, mine included, is a static read. The three specific unknowns worth a run are whether `validate-maps` stays green across S2's single commit if the replacement cases reuse the retiring case names, whether the shared text parse can build the envelope arm without resolving a `oneof` arm's type to another top-level message, and whether the widened tier-3 arms actually pass. An implementor, not a reviewer, should settle these.




### [non-spec-recheck.2.review-mechanism.1]
FACT: The mechanism lens returned a cache hit on this run. The cache key (md5 of spec-changes + non-spec-changes + implementation-checklist, first 12 chars) is `2d25f5f323c0`, and a completed prior mechanism pass over byte-identical staging is stored at /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r2-2d25f5f323c0.json with an empty findings list. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r2-2d25f5f323c0.json:1
FACT: The three key files the cache hashes have not changed since the previous mechanism pass, so whatever the open-decisions phase applied in this lane landed outside spec-changes.md, non-spec-changes.md, and the implementation-checklist. A future mechanism run that wants to re-examine this lane's delta must look at the summary, status, and review-log instead, because the cache key is blind to those. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r2-2d25f5f323c0.json:2
USEFUL [cache]: The cache directive saved a full re-derivation of the proto measurement (31 request types, 25 with a top-level `session_id`), the 32-row table arithmetic, and the sweep for surfaces the table retirement falsifies; the stored coverage note carries all of it. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r2-2d25f5f323c0.json:2


### [non-spec-recheck.2.review-operational.1]

DECISION: returned an empty findings list, and did NOT honour the cache hit verbatim — BECAUSE the slot `operational-r2-2d25f5f323c0.json` held a SPEC-lane operational answer (its coverage names only "the staged spec file" and describes the delta as "the removal of §7 Open decisions only"), so the CACHE instruction's premise, that the hit is "your own answer to this same question", was false for a lane whose scope is non-spec-changes.md plus summary.md. I re-reviewed and then overwrote the slot with a non-spec-lane coverage text, which is strictly more informative for the next lens that hits it. ALTERNATIVES: returning the hit unchanged (rejected: it would have recorded the summary's three shipped-tree defect bullets and the two new non-spec paragraphs as reviewed under this lens when no operational pass had read them); leaving the old JSON in place (rejected: the harness instruction to write the slot is unconditional).

FACT: five independent operational-lens passes have now returned empty on this proposal (review-log.md:793 spec.1, :1439 spec-recheck.1, :1756 spec-recheck.2, plus the cached slot, plus this one). The surface genuinely has no purchase: nothing in spec/16, docs/reference/metrics.md, pkg/alerting/rules, or docs/runbooks/ keys on a gRPC request message's scope class, and `grep -rn -i fence docs/runbooks/` returns nothing. Treat the metric/alert half of this lens as settled and spend the budget on the docs half instead.
EVIDENCE: spec/10_gateway-internals.md:60; docs/reference/metrics.md:180; tests/tier11_docs/code_blocks_test.go:103-113

FACT: the operator-facing scope vocabulary in docs/ is disjoint from §4.1's subject. `grep -rn -i "session-scoped\|pod-scoped" docs/` returns 12 hits and every one is about a JSONL frame, an upload token, a filesystem path, a NetworkPolicy, or the ReportSessionScrub row at docs/reference/adapter-contract.md:81. No docs page mirrors the §4.1 request-message table, so SPEC-1's retirement strands no reader-facing statement. A future docs-alignment or operational lens can reproduce this in one grep.
EVIDENCE: docs/reference/adapter-contract.md:81; docs/reference/metrics.md:180

FACT: the new TEST-1 paragraph (non-spec-changes.md:43-48) is fully sound, checked anchor by anchor. `slotAddressCaseFiles` carries the literal `"tests/tier0_static/adapter_proto_message_scope_test.go"` at spec_map_slot_address_registration_test.go:336; :973, :1028, :1054, :1096, :1110 are each `for _, file := range slotAddressCaseFiles {`; `repoFileLines` (:699-706) t.Fatalf's on an unreadable file. Crucially `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108-1131) demands derived-set ⊆ hand-list and never the reverse, so a surplus entry is legal and keeping the path owes no edit to that file. The paragraph's conclusion is right for a reason it does not state.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336,699-706,1108-1131

FACT: `addressRuleCases` (tests/tier0_static/address_rule_citation_test.go:29-47) names five files and names NO function in `tests/tier0_static/adapter_proto_message_scope_test.go`. TEST-1 renames the gate's cases freely; only the tier-3 `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` is held by name, and TEST-2 does not rename it. This closes the obvious "TEST-1 renames a function a hand inventory pins" hypothesis without re-deriving it.
EVIDENCE: tests/tier0_static/address_rule_citation_test.go:29-47

FACT: `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` carries five `// spec: 4.1 (request message scope)` annotations (:26, :132, :408, :582, :747) and is credited whole-file at tests/spec-map.json:168, but it reads no spec/04 file at all (`grep -n "04_system-components" ` on it returns nothing). SPEC-1 keeps the `#### Request Message Scope` heading at spec/04:149, so those annotations stay resolvable and no edit is owed. This is the first place an edit-sites or operational lens reaches after "who else cites §4.1"; it is clean.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26,132; tests/spec-map.json:168

WATCHOUT: the cache slot name is `<lens>-r<round>-<hash>` with no lane label, and the hash covers spec-changes + non-spec-changes + implementation-checklist only. summary.md is OUTSIDE the key, so every lens on a summary-only delta gets a stale cross-lane hit. Three round-1 lenses in this lane honoured it and logged that they had; the reliability lens declined it and re-reviewed. Decline it when the cached coverage prose names the wrong lane's substrate.
EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/operational-r2-2d25f5f323c0.json

WATCHOUT: `diff -ru` against cp-snap/.../non-spec-recheck-r2 returns NOTHING for this round — the snapshot was taken at 21:05, the same minute the round started, so it is a copy of the live directory rather than a pre-edit baseline. Trap 84's advice generalises: get the delta from `git diff 470cd4e34 HEAD -- <proposal dir>` as review-log.md:1912 records, not from any cp-snap directory.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:1912


### [non-spec-recheck.2.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE the staged deliverables (SPEC-1 spec prose, TEST-1 tier-0 gate replacement, TEST-2 tier-3 split) change no runtime code, no proto, no handler, no lease, no delivery path, no drain, and no fail-open recovery, so there is nothing for a crash/restart/failover trace to bite on; and every recovery-path claim the newest text makes re-verified against the tree — ALTERNATIVES: re-filing the missing 1-second fence backoff and the equal-generation re-fence refusal, both rejected as pre-existing tree defects this proposal neither creates nor stages (the refusal is already recorded in `summary.md` under `## Defects in the shipped tree that this proposal does not stage`).

CORRECTS [Deferred: docs/reference/adapter-contract.md]: the standing-context DEFERRED entry says line 69's "precondition for any subsequent operational RPC" reads as a pod-wide gate and that "the true statement is that the fence is the precondition for subsequent operational RPCs naming the session it fenced". The tree refutes that per-session qualification for the hold half: `holdStateUnaryInterceptor` / `holdStateStreamInterceptor` gate on `s.inHoldState()` alone with a pod-level allowlist (`pkg/adapter/holdstate.go:336`, `:349`), and `s.exitHoldState()` runs after a successful fence for ANY bound session (`pkg/adapter/coordination.go:156`), so an RPC naming session B is blocked until some fence lands and is unblocked by a fence for session A. `spec/28_communication-channels.md:322` states it unqualified ("hard precondition for every other operational RPC to the pod"). The generation half IS per session (`spec/10_gateway-internals.md:41`), which is what the DEFERRED entry was reaching for. `summary.md:225-233` now states this correctly ("The precondition clause beside it stands... Qualifying that clause per session is what the tree refutes"), so the DEFERRED entry as written is superseded and should not be applied to `docs/reference/adapter-contract.md:69` as stated. — EVIDENCE: pkg/adapter/holdstate.go:336, pkg/adapter/coordination.go:156, spec/28_communication-channels.md:322, spec/10_gateway-internals.md:41

FACT: the equal-generation give-up trace re-derived end to end and holds exactly as `summary.md:170-180` states. Attempt 1 fences, ack lost at the 5s `CoordinatorFenceTimeout` (`pkg/gateway/runtime/adapterclient/coordinatorfence.go:49`), client sees a non-`FailedPrecondition` error so the `default` transient arm retries at the same generation (`pkg/gateway/coordination/coordfence/coordfence.go:180-184`); attempt 2 hits `gen <= st.coord.lastFenced` (`pkg/adapter/coordination.go:127-134`), the stale arm re-reads Postgres, finds `newGen == gen` so does not `continue`, and relinquishes (`:171-179`). The 3-attempt budget is never exhausted. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:155-189

WATCHOUT: the cross-lane cache collision is real and will mislead the next agent. `scratchpad/cp-cache/0075_.../reliability-r2-<hash>.json` is keyed on lane-agnostic round number plus a hash over spec-changes + non-spec-changes + checklist, so the spec-recheck lane's r2 answer (written 19:59, its `coverage` naming the `spec-r2` snapshot) is what a non-spec-recheck r2 run reads back. `summary.md` is NOT in the hash, so a summary-only delta leaves the hash unchanged and the cache looks warm when it is not. Returning it verbatim would have reported a spec-lane coverage statement for a non-spec-lane review. — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/reliability-r2-2d25f5f323c0.json

USEFUL [standing context, "Traps"]: the "do not re-file the session-scoped fence exiting a pod-wide hold" trap and the "spec/10:39 orders a same-generation retry the adapter refuses" Open entry between them account for the entire reliability surface of this proposal. Both were correct and both saved a full derivation.

FACT: `diff -ru scratchpad/cp-snap/0075_.../non-spec-recheck-r2 proposals/0075_...` is byte-identical this round, and `non-spec-recheck-r1-start` differs only in the review log, so round 1 of this loop changed nothing in the staging. The delta this loop exists for predates round 1 and is only visible as `git diff 470cd4e34 HEAD -- <proposal dir>`: the two new `non-spec-changes.md` paragraphs (`:43-48`, `:74-82`) and, in `summary.md`, the OD2→OD4/OD5 replacement plus the two new shipped-tree defect entries. All of it re-verified clean under this lens. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:1912


### [non-spec-recheck.2.review-security.1]
FACT: This round returned a CACHE HIT, not a fresh pass. `security-r2-2d25f5f323c0.json` was written at 19:55 by the previous loop's security lens and the cache key (md5 of spec-changes + non-spec-changes + implementation-checklist) still matches, so none of those three files changed between 19:55 and this round. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r2-2d25f5f323c0.json (mtime 19:55); non-spec-changes.md mtime 19:07; spec-changes.md mtime 19:04
WATCHOUT: The cache path carries no lane token, only lens+round+content hash, so a hit can come from a different lane. The returned coverage prose names spec-lane snapshots (`spec-r2`, `spec-recheck-r3`) because of that. It is not evidence that this lane's non-spec staging went unread by the harness; it is evidence that the hashed text is byte-identical to what that run reviewed. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/ (sibling `edit-sites-r1-2d25f5f323c0.json` written 21:04 in this round against the same hash)
FACT: The only staging file NOT in the cache key is the summary (mtime 19:18). A future round that changes only the summary will re-hit every cached lens shard. If a summary-only edit needs review, the cache must be busted by hand. — EVIDENCE: cache key command in the security lens prompt; 0075..summary.md mtime 19:18 vs cache mtime 19:55
UNVERIFIED: Whether the 19:55 run's own scope statement included the non-spec staging. Its coverage text enumerates only spec-staging sections. A later security pass over a changed non-spec staging should not assume the non-spec deliverables (tier-0 gate replacement, tier-3 suite repair) have had a dedicated security read; re-derive rather than inherit. — who: the next security lens whose hash misses.


### [non-spec-recheck.2.review-test-coverage.1]

DECISION: Returned an EMPTY findings list, the seventh empty test-coverage pass on this proposal — BECAUSE the three hashed staging files are byte-identical to what [non-spec-recheck.1.review-test-coverage.6] read (both hash to `2d25f5f323c0`) and `summary.md` has not been touched since 19:18, before that run's 19:34 cache write, so there is literally no delta on this lane for my lens; I re-derived section 8's tier map against the tree anyway and it still covers every behavior the staging changes — ALTERNATIVES: the four candidates [non-spec-recheck.1.review-test-coverage.6] enumerates, each re-checked and rejected on the same evidence.

FACT: the r1 cache file for this exact key already holds an empty findings list, so a future round can confirm in one command instead of re-reading the staging. `python3 -c "import json;print(json.load(open(P))['findings'])"` on it prints `[]`. — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/test-coverage-r1-2d25f5f323c0.json

FACT: `tests/tier0_static/adapter_proto_parse_test.go` declares NO test function at all; it is a helper file that only happens to carry the `_test.go` suffix. Its whole coverage is whatever its two callers assert. That is the correct ground for declining "nothing pins the extended `protoFields`": there was never a self-test to lose, and the claim-register gate is the only thing that ever exercised it. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:1-95 (no `func Test`)

FACT: the retiring gate's self-test already has the exact shape section 8 asks the replacement to have — one green-baseline assertion followed by a subtest per refusal — so the enumerated five case subjects map onto an existing pattern in the same file rather than onto something an implementor has to invent. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:174-176 (baseline), :182-201 (five refusal cases)

USEFUL [non-spec-recheck.1.review-test-coverage.6]: its four rejected candidates are the complete candidate set for this lens on this staging. I re-derived each independently and reached the same answer; the entry saved the tier-1 question outright.

USEFUL [standing context "The one behavioral obligation is met and pinned"]: sixth confirmation. `pkg/adapter/coordination.go:108-111` returns `InvalidArgument` on an empty session id before `boundSlotState` at `:116`, and `pkg/adapter/coordination_test.go:35-43` asserts it. This single fact disposes of the entire "the fence became session-scoped, where is its tier-1 test" line.

OPEN: section 8 still opens with `**IMPLEMENTOR TO FILL THE BLANKS.**` and says "The specific cases are written during convergence" while convergence has closed. Seven passes have judged the five enumerated case subjects sufficient for the lens, so this is stale framing rather than a coverage gap, but it is the second round to record it and no fix pass has retired the clause. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:101-103

WATCHOUT: this lane's r2 snapshot (`scratchpad/cp-snap/.../non-spec-recheck-r2`) is byte-identical to the live proposal directory, so `diff -ru` prints nothing and looks like a broken snapshot. Diff against `non-spec-r6` to see the real delta; that is the newest snapshot that actually differs. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-r6/

### [f3.cleanup]

DECISION: rewrote nothing in `summary.md` — BECAUSE the file already carried exactly the eight target headings in the target order (`# Summary: ...`, then `## Summary` holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**`, then `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, `## Deliverable index`), with no lead paragraph above `## Summary`, no heading below `##` anywhere in the file, no `### Retired` block or equivalent inside the open-decisions section, no meta-list of staged items with dispositions, and no block of corrections owed to files this loop cannot edit — ALTERNATIVES: moving the `**Watch out for.**` sentences about 0073's immutability and the 0076 sequencing, and D5 and D6 under `**Decisions.**`, into `## Impacts on other proposals` under the prose-about-another-proposal rule; rejected on the ground [f1.cleanup] and [f4.cleanup] both recorded, that those parts are listed parts of `## Summary` rather than unlisted content and the 0073 and 0076 impacts rows already carry the validity claims, so a move would create the second copy that rule exists to prevent. The two statements were read against each other and do not disagree: `summary.md:51-53` and D5/D6 at `:48-49` say 0073 is not reopened and that this proposal sequences after 0076, and the rows at `:255` and `:256` say the same.
EVIDENCE: 0075_....summary.md:1, :3-64, :66, :75, :83, :161, :251, :259

FACT: every item this firing handled is accounted for in the file as it stands, and nothing was left unplaced or dropped. OD1, OD4, and OD5 are the three entries under `## Open decisions for human to make`, each keeping its stable identifier verbatim. The three `marker:unscoped` items are the three entries under `## Defects in the shipped tree that this proposal does not stage`, word for word as the earlier pass confirmed them: the pod's refusal of the equal-generation re-fence (`spec/10_gateway-internals.md:39`), the stale protocol excerpts on `docs/api/internal.md:209-215`, and the §4.7 RPC row at `spec/04_system-components.md:712`. Proposals 0073, 0076, and 0080 hold one impacts row each. The 0073 row stands notwithstanding its gate-refuted, apply-refuted disposition, because the row predates this firing and no item on the list asked for its removal.
EVIDENCE: 0075_....summary.md:85, :142, :153, :170, :193, :217, :255-257

FACT: `## Deliverable index` is preserved byte for byte in last position, with its three lines (SPEC-1, TEST-1, TEST-2) in their existing order. The reconciliation pass owns it and this pass did not touch it.
EVIDENCE: 0075_....summary.md:259-277

FACT: no `DEFERRED` entry was added by this firing, because `summary.md` carries no block of corrections owed to a file this loop cannot edit. The seven `DEFERRED` lines already standing in `## Standing context` are untouched, as is the whole of that section.

FACT: the summary carries no OD2, OD3, or OD6 and no mention of proposal 0036; the identifiers now standing are OD1, OD4, and OD5. [f4.cleanup] describes a summary carrying OD1 and OD2 with a single shipped-tree defect entry, and the standing-context `## Open` line that joins on [f1.cleanup, f2.cleanup, f3.cleanup] is about OD2. Both records describe a state a later loop has since replaced. No withdrawn identifier was reused, and OD1 is the only identifier common to the two states. The cleanup blocks also sit in the ledger in lane order rather than firing order ([f4.cleanup] at the end of the `non-spec.5` run, [f1.cleanup] after `reconcile.2`, [f2.cleanup] after `spec-recheck.2`), so a reader who takes ledger position for chronology reads them backwards.

OPEN: OD4 and OD5 each state the question, its ground, and what each answer costs, and each records that the review loop derived no recommendation. Neither carries a recommendation or a confidence, which is less than `## Open decisions for human to make` asks of an entry. This pass observed the shortfall and left it, because supplying a recommendation is adjudication a format pass may not do. It is the same shortfall the standing-context `## Open` line records against OD2, now standing against the two entries that replaced it, so that line should be re-pointed at OD4 and OD5 rather than retired.

OPEN: the preamble of `## Defects in the shipped tree that this proposal does not stage` says "Both defects this proposal confirms in the specification are staged" and then names TEST-2, whose subject is a comment in `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` rather than a sentence in `spec/`, so the preamble counts a tier-3 test defect as a specification defect. The rest of it is true of the three entries that now follow: it states no count, and all three are working-tree defects confirmed by an earlier pass and left where they are. This pass moved nothing into or out of the section, so no move of its own falsified the preamble; the mis-scoping predates the firing and correcting it is a wording judgement rather than a format move. Already observed once, in `[non-spec-recheck.1.review-citations.1]`.
EVIDENCE: 0075_....summary.md:162-168

WATCHOUT: this review log carries `## Standing context`, `## Ledger`, `## Resolved in adversarial review`, and `## 11. What the 2026-09-06 rewrites changed`, and has no `## Retired` section at all. This block is therefore spliced at the end of `## Ledger`, immediately before `## Resolved in adversarial review`, as [f1.cleanup] and [f4.cleanup] both recorded. A later pass that looks for `## Retired` to find the end of the ledger will not find one, and appending to the end of the file buries the entry inside the `## 11` appendix.





### [spec.1.review-edit-sites.1]
FACT: The edit-site lens returned a cache hit at scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r1-2d25f5f323c0.json and I returned it verbatim (empty findings). Its own coverage string records the full sweep it did. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r1-2d25f5f323c0.json
WATCHOUT: The cache key hashes spec-changes.md + non-spec-changes.md + implementation-checklist.md with no lane in the key, so a spec-lane and a non-spec-lane run of the same lens collide on one file. The cached entry itself notes it declined an earlier hit at that same path for that reason. If a future run's coverage string does not match its own lane, treat the hit as a collision rather than as an interrupted twin. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r1-2d25f5f323c0.json
UNVERIFIED: Nobody has executed tier 0 or tier 3 against the applied spec+non-spec edits; the replacement gate's day-one greenness rests on reading. The implementation lane should run them.


### [spec.1.review-reliability.1]

DECISION: declined the cache hit at `scratchpad/cp-cache/.../reliability-r1-2d25f5f323c0.json` and re-ran the review — BECAUSE the hit's own `coverage` says it read "the spec staging and the non-spec staging as one document", which is a non-spec-lane answer served into a spec-lane slot, exactly the collision the Standing context records ("Cache keys omit `summary.md` and `review-log.md`. The key is `md5(spec-changes + non-spec-changes + implementation-checklist)` with no lane component"). ALTERNATIVES: replaying it verbatim, rejected because a lane-blind replay is how the ledger accumulated "empty findings" entries that were never reviews. The re-run reached the same answer, so the hit was substantively right and only its provenance was wrong.

USEFUL [Standing context / Traps, cache-key entry]: it is the reason the hit was declined rather than replayed, and it cost about two minutes instead of a whole wasted firing. Keep it at the top of the Traps list; it fires on every lens.

FACT: the spec staging is committed and clean at HEAD. `git diff HEAD -- proposals/` shows only `review-log.md` modified, so the text reviewed in this round is the same text the last baseline commit carries. EVIDENCE: `proposals/0075_.../0075_....spec-changes.md` (last touched by `6dcc6e932`)

FACT: every recovery-path request message the reclassification could reach already declares the address, so the derived rule reproduces the retired table on exactly the recovery surface this lens owns. Re-verified message by message today: `ResumeRequest` `SessionId session_id = 1` at `schemas/lenny-adapter.proto:1339`, `CheckpointBarrierRequest` `:1487`, `InterruptRequest` `:1118`, `SignalDeadlineRequest` `:1305`, `ShutdownRequest` `:1610`, `CoordinatorFenceRequest` `:1456`. Nothing on the crash, handoff, or drain path moves class except the fence, and it moves pod to session, which only adds the `spec/05_runtime-registry-and-pool-model.md:515` refusal. EVIDENCE: schemas/lenny-adapter.proto:1339,1487,1118,1305,1610,1456

FACT: `spec/10_gateway-internals.md:38` spells the fence `CoordinatorFence(session_id, new_generation)`, so no spec-stated recovery path ever sends a fence without an identifier. That is what closes the obvious reliability worry about the reclassification: the newly-applicable empty-identifier refusal has no reachable trigger on the handoff path. EVIDENCE: spec/10_gateway-internals.md:38

FACT: after SPEC-1 the surviving `pod-scoped` sites in `spec/` are `spec/10:60` (hold and gauge), `spec/04:726` (`ReportPodScrub`), `spec/04:872` (rotation ceiling), and `spec/12:202` (Redis key prefixes). A full `grep -rn "pod-scoped\|pod scoped" spec/` returns those four plus the two lines SPEC-1 deletes (`spec/04:151`, `:188`). No surviving sentence classifies the fence, so the edit leaves no dangling pod-scope claim about it. EVIDENCE: spec/04_system-components.md:151,188,726,872; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

FACT: nothing anywhere in `spec/`, `docs/`, `tests/`, or `pkg/` references the retired table by heading or anchor. `grep -rn "Request Message Scope\|request-message-scope"` over all four returns the heading itself alone, at `spec/04_system-components.md:149`. No recovery-path section depends on the table as a normative pointer. EVIDENCE: spec/04_system-components.md:149

MISTAKE: none found this round. Seven prior firings of this lens (four spec-lane, three non-spec-lane) returned empty, and this one does too, over a staging whose reliability surface has not changed since. The lens is exhausted on this proposal unless the staged §4.1 block is rewritten; a future firing should spot-check the four FACTs above rather than re-derive them.

OPEN: unchanged and not re-filed — `spec/10_gateway-internals.md:39` orders a same-generation fence retry ("up to 3 attempts with 1-second backoff") that the post-0076 adapter refuses, and `pkg/gateway/coordination/coordfence/coordfence.go` implements no backoff at all. Both halves are pre-existing, neither is touched by this staging, and the remedy lands in `spec/10` or in code, so it is outside this loop. It belongs to 0080's inventory if nobody has taken it.


### [spec.2.review-applicability.1]

DECISION: returned an EMPTY findings list on the staged spec edits — BECAUSE I simulated S1→S2→S3 against the live tree and every anchor SPEC-1 names resolves uniquely, the D1 blockquote is final text needing nothing invented, no staged edit forward-references an artifact a later step creates in a way that would stop the edit, and the only gate S1 reddens (`parseMessageScopeTable` finding zero rows) carries its disposition on S1's own line — ALTERNATIVES: filing the retired table's Service/Direction columns as class-3 content loss (rejected: §4.7.1's two RPC tables carry direction by table and service by direction, and the Scope column is what the rule reproduces); filing "A tier-0 gate refuses…" in the staged §4.1 text as a forward reference to the gate S2 builds (rejected: writing prose cannot fail, spec-first is the project's own order, and S2 Depends-on S1); filing the replacement gate's unstated file path and case names as a class-2 underspecified target (real, but its remedy lands in `non-spec-changes.md` TEST-1, outside this loop's scope, and it already sits in the Standing context's Open block).

CORRECTS [Standing context / Proto anchors]: the entry reading "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)" is itself wrong. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;` (confirmed by `grep -n "SessionId session_id" schemas/lenny-adapter.proto`, which prints `458:`). SPEC-1's citation is exact and no fixer should "correct" it to `:457`.

FACT: every line anchor SPEC-1 depends on is exact on the tree at this commit. `spec/04_system-components.md` `#### Request Message Scope` `:149`, intro `:151` (which does carry the `CheckpointRequest`/`CheckpointStart` clause SPEC-1 says it carries), table `:153-186` (header `:153`, separator `:154`, 32 rows `:155-186`), grounding paragraph `:188`, `ShutdownRequest` `:190`, `### 4.2` `:192`. Blank lines sit at `:150`, `:152`, `:187`, `:189`, `:191`, so the replacement drops in without touching the heading or §4.2. — EVIDENCE: spec/04_system-components.md:149-192

FACT: the two cross-checks SPEC-1 rests on outside §4.1 are exact. `spec/10_gateway-internals.md:57` ends "…it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state"; `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped." Both are single very long lines; `fold -w 150` on one `sed -n Np` is the way to read them. — EVIDENCE: spec/10_gateway-internals.md:57,60

FACT: the addressing convention the staged gate paragraph asserts is green on the shipped proto in both directions, re-derived by two independent greps. 26 `SessionId session_id` declarations; zero fields named `session_id` with another type; zero fields of type `SessionId` with another name (`grep -n "SessionId " schemas/lenny-adapter.proto | grep -v "message SessionId" | grep -v "SessionId session_id"` returns only the `:595` doc comment). — EVIDENCE: schemas/lenny-adapter.proto:595

USEFUL [Standing context / Traps]: the "naive proto parsers derail on this file" and "long lines hide the sentences citations point at" traps each saved a wrong derivation on the first attempt, and the six "do not re-file" traps closed six candidate findings before I spent a Read on them. This shard's only new value over that block is the `:458` correction above.


### [spec.2.review-citations.2]

DECISION: Returned an empty findings list for the citation lens on the spec staging — BECAUSE every concrete citation in `spec-changes.md` resolves and says what the proposal claims; I re-derived all 18 distinct `file:line` refs plus the bare relative anchors from the files rather than from the log — ALTERNATIVES: filing the loose-quantifier wordings in D1 ("the ones that carry none are exactly its pod rows"; "the ones other than the stream envelope ... are exactly the table's session rows"), rejected because the restrictor distributes and the standing context records both as already examined and dropped more than once.

CORRECTS [Standing context, Settled bullet "Proto anchors, current and verified"]: that bullet says "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exactly right and there is no drift at all. EVIDENCE: schemas/lenny-adapter.proto:456-458. A future compaction should drop the parenthetical rather than carry a false "one line off" note that invites someone to "fix" a correct citation.

FACT: every citation in the staged spec edits was re-verified today at commit c5d35bb05 and all hold: spec/04 `:149` heading, `:151` intro, `:153-186` table (32 rows, 26 session / 6 pod), `:175` fence row, `:188` grounding paragraph, `:190` ShutdownRequest, `:192` `### 4.2`; `:152` and `:187` are blank so the enumerated replacement range covers exactly the block. EVIDENCE: spec/04_system-components.md:149-192

FACT: `:725` and `:726` sit under `#### 4.7.1 Role and Gateway RPC Contract` (opened at `:692`, closed by `#### 4.7.2` at `:728`), so SPEC-1's "§4.7.1" attribution is correct rather than a §4.7 approximation. EVIDENCE: spec/04_system-components.md:692, :728

FACT: `git log -S` attributes the `spec/10:60` sentence "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped" to commit `cf20a0646` "Apply proposal 0076 SPEC-1 to spec/10", which confirms SPEC-1's "which proposal 0076's SPEC-1 landed under its D5" attribution. EVIDENCE: spec/10_gateway-internals.md:60

FACT: `#### Request Message Scope` and the table have no inbound reference anywhere in `spec/`, `docs/`, `schemas/`, `charts/`, `pkg/`, or `cmd/`. The only references to the section are `// spec: 4.1` annotations in test files, which survive the retirement untouched. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26, tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99

FACT: after SPEC-1 removes `:151` and `:188`, the surviving `pod-scoped` sites in `spec/` are `spec/10:60` (the hold and its gauge), `spec/04:726` (ReportPodScrub), `spec/04:872` (the rotation ceiling: "...pod-scoped with it."), and `spec/12:202` (Redis key prefixes). Only `:726` classifies a request message, so SPEC-1's "only sentence" claim holds. EVIDENCE: spec/04_system-components.md:872, spec/12_storage-architecture.md:202

WATCHOUT: the round-2 spec snapshot at `scratchpad/cp-snap/.../spec-r2` is byte-identical to the live proposal directory, so `diff -ru` prints nothing. That is the normal case on this proposal, not a broken snapshot; the standing context already warns about it. Do not read the empty diff as "the fixer changed nothing worth reading".

WATCHOUT: reading the review log or the standing context through `cat`/`awk` piped to `cut` fails on this proposal — the Settled and Traps bullets are single lines several hundred characters long and the tool persists the output to a file instead of printing it. Use the Read tool with an explicit `limit` on `review-log.md` (lines 1-120 covers Standing context in full) rather than shell text tools. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:16-119


### [spec.2.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens over the staged spec edits — BECAUSE every externally-consumed representation the staged rule touches or newly constrains was re-derived from the tree and is consistent: no proto edit is staged and the shipped proto already satisfies the name/type biconditional and the one-addressing-frame envelope clause; no `sdks/`, `charts/`, `pkg/embedded/`, or `pkg/gateway/openapi/` artifact names any affected message; no `docs/` page classifies a gateway-adapter gRPC request message by scope except the `ReportSessionScrub` row SPEC-1 explicitly leaves standing; and no spec, schema, or register surface outside §4.1 cross-references the retiring table. ALTERNATIVES: filing `docs/api/internal.md` (pre-existing staleness, remedy is a docs edit outside this loop, already twice deferred) and filing the unbound "either service" quantifier in the staged paragraph 1 (examined, declined — see below).

FACT: the proposal text is byte-identical to the round-1 snapshot. `diff -ru scratchpad/cp-snap/.../spec-r2 proposals/0075_...` prints nothing, and a diff against `non-spec-r6` shows changes in the review log alone. This lens (`spec.1.review-client-surface.1`) already ran on exactly this text and returned empty; this round re-derived its load-bearing facts independently rather than trusting them, and they all reproduce.

FACT: the two service blocks and the RPC census are exactly as the standing context records, re-derived this round from the `service`/`rpc` lines alone: `service Adapter` at `schemas/lenny-adapter.proto:32` with 24 RPCs, `service GatewayControl` at `:261` with 7, giving 31 distinct request types. The only `oneof` blocks in the whole file are `CheckpointRequest`'s at `:1174` and `CheckpointResponse`'s at `:1250`, and `CheckpointResponse` is the request type of no RPC, so the envelope clause selects `CheckpointRequest` alone.
EVIDENCE: schemas/lenny-adapter.proto:32, :261, :1174, :1250

FACT: `AttachRequest` is a *streaming* request type that carries no `oneof` (`rpc Attach(stream AttachRequest)` at `schemas/lenny-adapter.proto:84`), so the staged "stream envelope" predicate keys on the `oneof` rather than on the RPC's streaming-ness. `PrepareWorkspace` (`:41`) and `AdapterEvents` (`:241`) are the other client-streaming RPCs and are likewise classified by clause 1. A reviewer who reads "stream envelope" as "request type of a streaming RPC" will think the predicate selects four messages instead of one; it does not.
EVIDENCE: schemas/lenny-adapter.proto:41, :84, :241

FACT: the only `string session_id` declaration anywhere in `spec/` is `spec/04_system-components.md:1047`, inside the `RequestInterceptor` protobuf block of §4.8 (`schemas/lenny-interceptor.proto`'s surface), not the gateway-adapter protocol. So the staged constraint paragraph creates no contradiction inside `spec/` even though its gate sentence says "a protocol definition" indefinitely. This is the spec-side manifestation of the standing trap about the gate's domain; it is not an additional site.
EVIDENCE: spec/04_system-components.md:1040-1051

FACT: `spec/28_communication-channels.md`'s §28.7 published-artifact register row for `schemas/lenny-adapter.proto` (`:1782`) cites §15.4 as where the specification states the artifact, and names no §4.1 classification table. §28.4's claim register is a pointer to `tests/claim-map.json` for normative statements §28 makes, so retiring the §4.1 table owes no §28.3, §28.4, or §28.7 edit and the compliance-suite consumer clause on that row stays true.
EVIDENCE: spec/28_communication-channels.md:161-174, :1782

USEFUL [spec.1.review-edit-sites.1 CORRECTS of spec.2.review-fresh.1]: the "either service has no antecedent after the table is deleted" question is the first thing this lens reaches for. It is correctly closed: the staged sentence already names "the gateway-adapter protocol", `schemas/lenny-adapter.proto` declares exactly two services, and `spec/28:1782` names `GatewayControl` as a service. Saved a full derivation; do not re-open.

USEFUL [standing context, Traps]: the `AdapterEventsRequest` decline, the nested-address decline, and the "only way a request addresses a session" decline are each the natural client-surface objection and each is correctly closed. Reading the Traps list first saved roughly three derivations.


### [spec.2.review-docs-alignment.1]

DECISION: returned an EMPTY findings list, the seventh consecutive empty return for this lens — BECAUSE the round-2 spec-lane snapshot is byte-identical to the live directory on every file (`diff -rq scratchpad/cp-snap/.../spec-r2 proposals/0075_...` prints nothing), so no staged text is newer than the text this lens already cleared, and an independent re-sweep of `docs/` reconfirmed that no page states the §4.1 gRPC request-message classification — ALTERNATIVES: re-filing `docs/api/internal.md:209-215` and `docs/reference/adapter-contract.md:69`, both declined again on the grounds already recorded in the Standing context Deferred block and, decisively for this loop, because each remedy is a docs edit that a spec-only loop may not land.

FACT: re-verified this round, one anchor at a time, that every docs-facing citation SPEC-1 makes is exact. `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` row and `:82` the `ReportPodScrub` row; the tier-11 gate function opens at `:43` and closes at `:76`, so SPEC-1's `:43-76` is exact rather than approximate. `spec/10_gateway-internals.md:57` carries "which is the only way to exit hold state" and `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped", both exactly as SPEC-1 quotes them.
EVIDENCE: docs/reference/adapter-contract.md:81,:82; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43,:76; spec/10_gateway-internals.md:57,:60

FACT: the five sentences on `spec/04_system-components.md:151` are, in order, the two-class statement, the declared-rather-than-derived reason, the unit-of-the-table sentence, the every-message-is-a-row sentence, and the `CheckpointRequest`/`CheckpointStart` envelope clause. Piping that one long line through `tr '.' '\n'` shows all five in seconds; `sed | cut` shows only the first two and makes SPEC-1's "removing `:151` also removes the envelope clause" look unsupported.
EVIDENCE: spec/04_system-components.md:151

FACT: "stream envelope" is not a term SPEC-1 coins. The retired table already spells it at `spec/04_system-components.md:167` ("session (stream envelope; scope of its `CheckpointStart`)"), and no `docs/` page discusses checkpoint scoping at all, so the staged envelope clause introduces no terminology a reader-facing page contradicts.
EVIDENCE: spec/04_system-components.md:167

FACT: no docs page, `TESTING.md` entry, or register enumerates the retiring tier-0 gate. `grep -rn "message_scope\|message scope\|MessagesAreClassifiedByScope" docs/ TESTING.md spec/ tests/registers/` returns nothing, so retiring the gate and adding the convention gate owes no documentation inventory edit. Checked because a gate-inventory page would have been the one docs surface TEST-1 could strand.

USEFUL [spec.1.review-docs-alignment.1] and [non-spec.5.review-docs-alignment.1]: their two-grep reproduction of the "no docs mirror" fact plus the recorded ground for declining `docs/api/internal.md:209-215` turned what would have been an hour of page-by-page sweeping into about ten minutes. Both entries earn their place in the Standing context; keep them.

WATCHOUT: `sed -n '78,84p' docs/reference/adapter-contract.md` silently misled me into thinking the `ReportSessionScrub` row sat at `:80`, because the printed block has fewer visible lines than the range (a blank line reads as an absent one when the surrounding rows are very long and wrap). Confirm a row's line number with `grep -n` before concluding a proposal citation is off by one.
EVIDENCE: docs/reference/adapter-contract.md:81


### [spec.2.review-edit-sites.3]

DECISION: returned an empty findings list for the edit-site-completeness lens over the staged spec edits — BECAUSE every identifier SPEC-1 adds, changes, or removes was enumerated and grepped across `spec/`, `docs/`, `schemas/`, and `charts/`, and no surface outside `spec/04_system-components.md:151-188` becomes wrong or internally inconsistent once the block is replaced — ALTERNATIVES: filing the loss of the retired table's `Service` and `Direction` columns as an unstaged relocation (rejected: no applied text becomes wrong, and nothing in the tree reads those columns); filing `docs/api/internal.md:209-215` (rejected: already contradicts the shipped proto today, SPEC-1 does not make it wrong, and it is already a standing DEFERRED); filing the introduction of the proto type name `SessionId` into `spec/` (rejected: wording, and the retired table already named proto message types).

FACT: `SessionId` as a bare type name occurs NOWHERE in `spec/` or `docs/` today — `grep -rn SessionId spec/ docs/` returns only `sessionId`/`parentSessionId`-style JSON field names and two Go SDK examples. SPEC-1's staged paragraph is the first spec sentence to name the proto message type. No register, gate, or glossary requires a spec-named proto type to be declared anywhere, so this is below the bar, but a future lens will re-derive it.
EVIDENCE: spec/04_system-components.md:151 (names `session_id` only, never the type); schemas/lenny-adapter.proto:596

FACT: §4.7.1's two RPC tables are NOT a complete replacement for the retired table's Service and Direction columns. The `Gateway → Adapter RPCs` table (`spec/04_system-components.md:700-724`) omits `SendMessage`, `SignalDeadline`, `RevokeCredentials`, `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`, all of which had rows in the §4.1 table. An earlier round's ground for declining the column-loss finding ("§4.7.1's two RPC tables carry direction") is therefore weaker than stated. The conclusion still holds — no applied text becomes wrong, and nothing reads the columns — so do not re-file; just do not lean on that ground again.
EVIDENCE: spec/04_system-components.md:700-724 vs :155-186

FACT: `spec/28_communication-channels.md:163` scopes the §28.4 claim-register obligation to "Every normative statement **this section** makes about a mechanism", so the new "A tier-0 gate refuses a protocol definition in which…" sentence landing in §4.1 owes no claim-register row. This is the cheap answer to the recurring "does the staged gate sentence need a §28.4 row" question.
EVIDENCE: spec/28_communication-channels.md:161-165

USEFUL [Traps: naive proto parsers derail on this file]: a comment-stripping, per-line field-regex pass over the whole proto reproduced the Settled census on the first attempt — zero name-without-type and zero type-without-name violations, 26 `SessionId session_id` declarations, exactly two `oneof` blocks at `:1174` and `:1250`. Spot-checking with that method costs about a minute and settles the "gate is green on day one" premise without re-deriving the 31/25/6 census.

USEFUL [Traps: orchestrator briefs carry stale content]: the r2 spec-lane brief again gives the pre-refresh proto anchors (`:589`, `:1166`, `:1447`, `:1448`, each off by seven) and again says §1.2 asserts the pod-wide ground still holds. §1.2 already reads post-0076 (`spec-changes` §1.2 cites `pkg/adapter/server.go:302` as `hold holdState`). Reviewing the brief against the tree manufactures four false findings.

WATCHOUT: the spec-lane snapshot `scratchpad/cp-snap/.../spec-r2` is byte-identical to the live proposal on all six non-log files, so `diff -q` per file returns SAME and `diff -ru` prints nothing. That is the true state, not a broken snapshot, and it means no fix-stage text is newer than the previous round's — the "read the changed sections hardest" instruction has no target this round.
EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2/


### [spec.2.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens on `spec-changes.md` — BECAUSE every actor the staged text names can perform what it is assigned: the tier-0 gate reads only `schemas/lenny-adapter.proto` (a repo file), the adapter enforces the empty-address refusal the derived class implies on both newly-reached messages, and no §4.6.3 owner, RBAC, §13.2 egress, §10.3 pod posture, webhook-purity, or spec/18 phase constraint is touched — ALTERNATIVES: the session-scoped-fence-exits-a-pod-wide-hold argument (Traps: declined four times, 0076's OD3 weighed and rejected it), the "a protocol definition" indefinite-domain argument (Traps #77, declined), and the envelope-arm-from-a-text-parse feasibility question (Open #102), which is trivially satisfiable because `protoFields` already returns a per-message field map and `CheckpointStart` is a top-level message in the same map.

CORRECTS [Standing context / Settled, "Proto anchors, current and verified"]: the entry says `ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites `:458`, one line off, below the bar". SPEC-1 is right and the log entry is wrong. `:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. EVIDENCE: `schemas/lenny-adapter.proto:456-458` (`message ReportSessionScrubRequest {` / `  string pod_id = 1;` / `  SessionId session_id = 2;`). Do not "fix" SPEC-1's citation to `:457`.

FACT: every line citation in `spec-changes.md` §4 and §5 re-verified today and all hold. `spec/04:149` heading, `:151` introducing paragraph, `:153-186` table (32 rows, header at `:153`, `ReportPodScrubRequest` at `:186`), `:175` fence row, `:188` grounding paragraph, `:190` `ShutdownRequest`; `spec/04:725`/`:726` (`The request is session-scoped: it is addressed by the identifier of the released session and names no slot` / `The request is pod-scoped`); `spec/10:57` (hold-state semantics, fence is the only exit) and `:60` (last clause: `The hold itself and the lenny_adapter_coordinator_hold gauge remain pod-scoped`), both inside §10.1.4 which spans `spec/10:53-63`; `spec/05:515` (last-but-one clause carries the empty-address refusal); `schemas/lenny-adapter.proto:1173`/`:1174`/`:1217`/`:596`/`:1456`/`:458`/`:499-503`; `pkg/adapter/checkpoint.go:74-84`; `pkg/adapter/coordination.go:107`/`:109-111`/`:116`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`; `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier0_static/claim_register_proto_agreement_test.go:64`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`; `docs/reference/adapter-contract.md:81`. EVIDENCE: as listed.

FACT: the addressing convention the replacement gate checks is green on the shipped proto in both directions, re-derived a second way today: `grep -n session_id schemas/lenny-adapter.proto | grep -v "SessionId session_id"` returns only comment lines, and `grep -n "SessionId " | grep -v session_id` returns only `:595` (comment) and `:596` (`message SessionId {`). No `repeated SessionId` and no `map<...,SessionId>` anywhere. EVIDENCE: `schemas/lenny-adapter.proto:595-596`.

FACT: the eight messages TEST-2 adds to the widened tier-3 session set all declare `SessionId session_id = 1` and none declares `slot_id` or any `reserved`, so both address arms pass and none of them belongs in the reservation arm. EVIDENCE: `schemas/lenny-adapter.proto:342` (ListPlatformToolsRequest), `:365` (CallPlatformToolRequest), `:384` (ListSessionConnectorsRequest), `:404` (ListConnectorToolsRequest), `:420` (CallConnectorToolRequest), `:1456` (CoordinatorFenceRequest), `:1539` (ExportPathsRequest), `:1674` (ConfigureWorkspaceRequest).

USEFUL [Standing context / Traps]: the trap list saved a full round. Four candidate findings my lens reaches naturally (the fence exiting a pod-wide hold, "the only way a request addresses a session" versus the envelope clause, the nested-address hole, and §4.7.1's per-message declarations versus a derived classification) are each recorded there with the ground on which they were declined. Reading it before investigating is worth the tokens.

WATCHOUT: the value-rule tension a feasibility reviewer reaches next is the continuation frame. A `CheckpointRequest` carrying `CheckpointGrant` or `CheckpointAbort` is, under the staged paragraph 2, a session-scoped request that declares no address at all, which reads as something `spec/05:515` orders the adapter to refuse with `InvalidArgument`. It is not a defect: the staged paragraph's own closing clause ("so every frame that follows continues a stream that is already addressed") is what exempts it, and `pkg/adapter/checkpoint.go:74-84` refuses only the opening frame. EVIDENCE: `spec/05_runtime-registry-and-pool-model.md:515`; `pkg/adapter/checkpoint.go:60-64` (opening frame must be a `CheckpointStart`) and `:82-84`.

FACT: nothing outside `spec/04` §4.1 depends on the retired table. A repo-wide grep for `Request Message Scope`, `request-message-scope`, `message scope`, and `scope table` over `spec/ docs/ tests/ schemas/ pkg/ cmd/` returns only `spec/04:149` itself, the §12.8 erasure-scope table (unrelated), `// spec:` annotations, and the two test files the proposal already lists. No spec cross-reference resolves to §4.1 for a message's scope, service, or direction: direction survives in §4.7's two RPC tables. EVIDENCE: `spec/04_system-components.md:149`, `spec/04_system-components.md:721-726`.


### [spec.2.review-fresh.2]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE every citation in
`spec-changes.md` §2, §4, §5, §9, §10 re-verified true against the tree at working-tree state on
2026-09-07, the census the derivation rests on re-derived clean with an independent brace-tracking
parser, and no `spec/`, `docs/`, `schemas/`, or `charts/` surface outside the replaced block depends
on the retired table — ALTERNATIVES: considered filing D2's "an addressing convention that nothing
checks today" (overstated, see the standing context's own DEFERRED entry), the "derived from the
message's field set" opening sentence versus the envelope clause, and the "only way a request
addresses a session" tension; all three are wording or already-declined and none makes the applied
spec wrong.

CORRECTS [standing context "Proto anchors, current and verified"]: that bullet says
`ReportSessionScrubRequest` has "its address at `:457` (SPEC-1 cites `:458`, one line off, below the
bar)". That is wrong in the tree's favour. `schemas/lenny-adapter.proto:457` is `string pod_id = 1;`
and `:458` is `SessionId session_id = 2;`. SPEC-1's citation of `:458` is exact, and the "one line
off" note should be dropped so a later round does not "correct" a correct citation.
EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: an independent proto parse (comment-stripped, brace-delta, oneof-aware) run today reproduces
the settled census exactly: 31 RPC request types, 88 messages, 25 declaring a top-level
`session_id`, the 6 non-declarers being `AdapterEventsRequest`, `CheckpointRequest`,
`DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`,
`ReportPodScrubRequest`; zero name/type biconditional violations across every message including
`oneof` arms; `oneof` blocks only in `CheckpointRequest` (`:1174`) and `CheckpointResponse` (`:1250`).
The replacement gate is green on the shipped proto. EVIDENCE: schemas/lenny-adapter.proto:1174,1250

FACT: the table's arithmetic checks out row by row. `spec/04_system-components.md:153-186` is header
plus separator plus 32 rows; the 6 pod rows are `:175` (fence), `:176`, `:177`, `:178`, `:179`, and
`:186`. After the fence moves, 27 session and 5 pod, which is exactly 25 addressed request types
plus `CheckpointRequest` plus `CheckpointStart` on the session side and the 5 unaddressed request
types on the pod side. EVIDENCE: spec/04_system-components.md:175, :186

FACT: `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts the
name-implies-type half of the addressing convention, over the 18-member hand list, at
`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:112-114`
(`f.Message().FullName() != "lenny.adapter.v1.SessionId"`). This is the ground for the standing
DEFERRED against D2's "nothing checks today". I re-verified it and still declined to file: the
sentence is decision rationale rather than staged spec text, so its overstatement makes no applied
spec wrong. The between-loops pass should still land the correction.
EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:112-114

FACT: `git log -- spec/04_system-components.md` shows the whole `#### Request Message Scope` block
was added by one commit, `f37e867b8` ("Declare gateway-adapter message scope and address frames by
session"), which is 0073's. No later commit, 0076's included, touched §4.1's block. SPEC-1's closing
"Written against whatever 0076 leaves in §4.1" therefore has no hidden delta behind it.

FACT: the hand inventories that could have dragged edit sites in are all confirmed non-matching.
`successor_pointer_test.go`'s `reducedSections` is a literal list whose only `spec/04` row is
`{"spec/04_system-components.md", "4.7", "28.5"}` (`:53`); `relocated_material_pointer_test.go`'s
`relocatedStatements` anchors on a CH-RUNTIMEOPS handshake sentence, not on anything in the deleted
block (`:50-56`). Neither is derived from the file, so neither fires on the retirement.

FACT: a repo-wide grep for the deleted block's distinctive literals ("appears on messages of both
classes", "declared in the table below", "The unit of the table", "carries its own row") returns
`spec/04_system-components.md:151` and `tests/tier0_static/adapter_proto_message_scope_test.go:20`
and nothing else outside `proposals/`. The gate file's copy goes with the gate TEST-1 replaces, so
the retirement strands no pinned literal.

WATCHOUT: `grep -rn` over the repo root from this proposal routinely produces 100KB+ of output
because the two review logs and the archive quote every anchor under review. Always append
`| grep -v "^./proposals/"` or scope the grep to `spec/ tests/ pkg/ docs/ schemas/` or the result is
persisted to a file and you pay two extra tool calls to read it back.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log-archive.md

USEFUL [standing context Traps]: the trap list saved this round from re-filing at least five
candidates I independently reached — the addressee gloss, the "only way a request addresses a
session" tension, the nested-address hole, the §4.7.1 "declared per message" tension, and the
session-scoped fence exiting a pod-wide hold. Every one of them looked like a finding on first read.

USEFUL [standing context "Long lines hide the sentences citations point at"]: `spec/10:57`,
`spec/10:60`, `spec/04:725`, and `spec/04:726` each needed `tr '.' '\n' | tail` or
`grep -o "The request is [a-z-]*-scoped[^.]*\."` to see the cited clause. A plain `sed -n Np` shows
none of it.


### [spec.2.review-kubernetes.1]

FACT: this proposal has no Kubernetes-idiom surface at all, and that is now checked rather than assumed. `grep -niE "controller|status subresource|finalizer|CRD|reconcil|admission|etcd|apiserver|field manager|leader elect|work.?queue|watch"` over the four proposal files returns only the word "reconcile/reconciliation" used for the tier-0 table-vs-proto gate and one "Watch out for" heading. No CRD status write, no finalizer, no admission webhook, no controller hot path, no etcd-as-bus. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:36-37

FACT: the staged block's subject is confined to the gateway-adapter gRPC contract. `grep -rn "session-scoped\|pod-scoped" --include=*.go pkg/controller pkg/admission charts/` returns exactly one hit, and it is unrelated: a comment about a session-scoped resource in the data-residency validator. — EVIDENCE: pkg/admission/data_residency_validator/validator.go:89

FACT: `#### Request Message Scope` has no inbound reference anywhere in the tree. `grep -rn "Request Message Scope\|request-message-scope" spec/ docs/ schemas/ charts/ tests/ pkg/` returns the heading itself and nothing else, so retiring the block owes no anchor redirect and drags no kube-facing surface. — EVIDENCE: spec/04_system-components.md:149

DECISION: returned an empty findings list — BECAUSE the Kubernetes-idiom lens (single-writer status ownership, status-as-command-channel, finalizer wedging, CRD-as-bus, admission coherence, level-triggered watches, controller-on-the-hot-path) has no purchase on a change that edits one protocol-classification block in spec/04 §4.1 plus two test files — ALTERNATIVES: filing on the fence reclassification's interaction with the pod-wide hold, rejected because it is not a Kubernetes-idiom question at all and the standing context records four lenses already stopping there (review-log.md Traps).

USEFUL [standing context]: the log's `### Settled` and `### Traps` sections saved the whole re-derivation. In particular the proto census, the anchor set, and the note that the orchestrator brief's proto anchors are off by seven.


### [spec.2.review-mechanism.1]

DECISION: returned an empty findings list for the mechanism lens on the staged spec edits — BECAUSE every
citation in `spec-changes.md` was re-verified true today, the four-check gate predicate is green on the
shipped proto, and every mechanism tension I derived independently was already sitting in the review log's
Traps section with a multi-round refutation — ALTERNATIVES: filing the "the addressing frame opens the
stream is asserted but ungated" gap (rejected: the classification does not depend on which frame opens, the
current proto satisfies it, and it is hypothetical hardening); filing §3's gate summary dropping the
"envelope declares no address of its own" conjunct (rejected: summary prose, makes nothing wrong).

CORRECTS [Standing context / Proto anchors]: the entry says "`ReportSessionScrubRequest` `:456` with its
address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong in the proposal's
favour. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is
`SessionId session_id = 2;`. SPEC-1's `schemas/lenny-adapter.proto:458` is exactly right and no one should
"fix" it. EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the whole staged spec block's gate predicate is green on the shipped proto, verified four ways today.
Every `SessionId` mention outside `message SessionId {` at `:596` and its comment at `:595` is a
`SessionId session_id = N;` field (26 of them); every non-comment `session_id` token is one of those 26;
`CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4` at `:1186`; and of
`CheckpointStart`/`CheckpointGrant`/`CheckpointAbort` only `CheckpointStart` declares an address, at
`:1217`. EVIDENCE: schemas/lenny-adapter.proto:595-596, :1186, :1217

FACT: a fresh independent parse (comment-stripped, brace-depth bounded, top-level fields only) reproduces
the census exactly: 31 rpc declarations, 31 distinct request types, 25 declaring a top-level
`SessionId session_id`, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`,
`GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Table arithmetic
also reconciles: 32 rows = 20 session + 5 pod on `Adapter`, 6 session + 1 pod on `GatewayControl`; after the
fence moves, 27 session / 5 pod, which is what the derivation computes (25 addressed + envelope + frame).
EVIDENCE: spec/04_system-components.md:153-186

FACT: `spec/` mentions no test tier anywhere today — `grep -rn "tier-0\|tier 0" spec/` returns nothing — so
the staged paragraph 3 introduces the first "A tier-0 gate refuses ..." sentence into the specification.
I examined this and did not file: it asserts nothing false and breaks no gate (§28.4's claim-register
obligation is scoped to "Every normative statement this section makes", spec/28_communication-channels.md:163,
so a §4.1 statement owes no `tests/claim-map.json` row). Recording it so the next agent does not re-derive
it as a novel defect. EVIDENCE: spec/28_communication-channels.md:163

FACT: the retirement owes no successor pointer. `reducedSections` in
`tests/tier11_docs/successor_pointer_test.go:52-56` is a hand-maintained three-entry list (§4.7, §15.4,
§29.10) and §4.1 is absent, and the block gives its content up to no other heading in any case.
EVIDENCE: tests/tier11_docs/successor_pointer_test.go:49-56

FACT: nothing outside §4.1 links or names the retired block. `grep -rn "request-message-scope|Request Message Scope"`
over `spec/ docs/ tests/ schemas/ charts/ pkg/ cmd/` returns exactly one hit, the heading itself at
`spec/04_system-components.md:149`. Confirms the standing "no inbound reference" entry by a second method.

FACT: `docs/api/internal.md:210-214` carries a stale hand-written `message CheckpointRequest { string session_id = 1; ... }`
with no `oneof` and a non-streaming `rpc Checkpoint(CheckpointRequest)` at `:90`. It is pre-existing drift
from the proto, it states no scope class, and it is not an edit site this proposal creates. Do not file it
here and do not add it to §9. EVIDENCE: docs/api/internal.md:90, :210-214

USEFUL [Standing context / Traps]: the trap list did the heavy lifting. Five of the six tensions I derived
independently under this lens — "the only way a request addresses a session" versus the envelope clause, the
nested-address hole, the §4.7.1 declared-versus-derived tension, the session-scoped fence exiting a pod-wide
hold, and `AdapterEventsRequest` as a counterexample — were each already there with a recorded refutation.
The Traps section is the reason this pass cost hours rather than rounds.


### [spec.2.review-operational.1]

DECISION: returned an empty findings list for the operational lens on the staged spec edits — BECAUSE SPEC-1 touches no metric, alert, CRD condition, or runbook, and every observability citation it rests on reads as cited — ALTERNATIVES: filing the "session-scoped fence exits a pod-wide hold" tension (declined: Standing-context trap, already refuted four times, §6 makes it a non-goal) and filing `spec/04:712`'s "Announce ... to the pod" row (declined: already false pre-0075 as a 0076 residue, so SPEC-1 does not make it become wrong, and it is already a DEFERRED entry).

CORRECTS [Standing context, Settled, "Proto anchors, current and verified"]: the parenthetical "SPEC-1 cites `:458`, one line off" is wrong. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` citation is exact. Anyone who trusts the log entry will re-derive this and lose the time; the anchor needs no correction.

FACT: the whole metric/alert/runbook surface adjacent to this proposal is one gauge and it is not in §4.1's blast radius. `lenny_adapter_coordinator_hold` is inventoried once at `spec/16_observability.md:185` and stated once at `spec/10_gateway-internals.md:60` ("The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"); `pkg/alerting/rules/rules.go` declares no alert over it and `docs/runbooks/` has no page for it. `docs/runbooks/coordinator-handoff-slow.md` is about delegation handoff latency (`lenny_coordinator_handoff_duration_seconds`) and is unrelated — EVIDENCE: spec/16_observability.md:185, spec/10_gateway-internals.md:60, docs/runbooks/coordinator-handoff-slow.md:28

FACT: no tier-11 doc gate reads the `#### Request Message Scope` block. The tier-11 files that carry `// spec: 4.1 (request message scope)` annotations (`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26,132,408,582,747`) cite the section but read `docs/` and other spec sections, and every `specSection(...)` call against `spec/04_system-components.md` in `tests/tier11_docs/` targets `### 4.7 `, `#### 4.6.1 `, `#### 4.6.3 `, or `### 4.9 `. Retiring the table reddens no tier-11 case — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:47, tests/tier11_docs/spec_47_rpc_row_naming_test.go:46

FACT: no §28.4 claim-register row is owed for the new tier-0 gate sentence. `spec/28_communication-channels.md:163` scopes the obligation to "Every normative statement **this section** makes about a mechanism", and §4.1 is outside it — EVIDENCE: spec/28_communication-channels.md:161-165

FACT: the retired table's `Service` and `Direction` columns are not lost information. Service membership is in the proto (`schemas/lenny-adapter.proto:32` `service Adapter`, `:261` `service GatewayControl`) and direction is carried by §4.7.1's two separately headed RPC tables, so no operator-facing statement depends on the columns the rule drops. A future lens reaching for "the rule drops two columns" should stop here.

USEFUL [Standing context, Traps, entry on the session-scoped fence and the pod-wide hold]: it named the exact argument the operational lens reaches first (an operator seeing `lenny_adapter_coordinator_hold`=1 needs a fence to clear it, and post-0076 the fence needs a bound slot entry), and named the four prior lenses that stopped there. Saved a filing that would have cost two verifiers.


### [spec.2.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the staged spec edit is three paragraphs of
classification prose plus a tier-0 static gate, and it creates no control-plane write, no data-plane
write, no net-new watch or informer, no key, no queue, and no connection. There is no rate to multiply
against a capacity tier, so the quantitative half of this lens has no subject. The qualitative half
(failure-mode reliability across Postgres failover, Redis reset, coordinator handoff) also has no subject:
the staged block names no message, states nothing about the hold, and changes no handler — ALTERNATIVES:
filing the coordinator-handoff stall (a fence for an unbound session cannot exit a pod-wide hold, so the
pod can only reach the 120s `coordinatorHoldTimeoutSeconds`); rejected because it is 0076's landed
behavior, weighed and rejected by 0076's OD3, and the standing context records four lenses reaching it
and stopping.

FACT: the reclassification adds exactly one new fail-closed obligation and the tree already meets it. The
derived session class is the 25 address-declaring request types plus `CheckpointStart` plus the
`CheckpointRequest` envelope = 27, against the retired table's 26; only `CoordinatorFenceRequest` moves.
The obligation the class carries is `spec/05_runtime-registry-and-pool-model.md:515` ("a session-scoped
request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument`
before any root is resolved"), and `pkg/adapter/coordination.go:108-111` refuses the empty identifier
before `boundSlotState`. No other message changes class, so no other handler acquires a refusal it does
not already perform. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515, pkg/adapter/coordination.go:108-111

FACT: the summary's shipped-tree defect entry about the equal-generation re-fence is accurate against the
driver as it stands today. `coordfence.fence` retries transient faults at the same `gen` inside the
`for attempt := 1; attempt <= f.maxAttempts` loop (`pkg/gateway/coordination/coordfence/coordfence.go:180-183`),
and the stale arm re-reads the generation and relinquishes when it has not advanced (`:171-179`), so a
fence whose ack is lost burns attempt 1 and relinquishes on attempt 2. `DefaultMaxAttempts = 3` at `:52`.
There is no `time.Sleep` or backoff anywhere in that loop, which is the second half of the standing
`Open` entry about `spec/10:39`. Both halves are pre-existing and neither is staged here. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:52, :140-183

USEFUL [standing context "Traps"]: the "do not re-file the session-scoped fence exiting a pod-wide hold"
trap and the "do not re-file the nested-address hole" trap each closed a line of inquiry this lens opened
independently, and the proto-parser trap saved a redundant census. The two spot-checks I did run
(26 `SessionId session_id` declarations with no name-or-type violation anywhere in
`schemas/lenny-adapter.proto`; exactly two `oneof` blocks, at `:1174` and `:1250`) both reproduced the
recorded answer, so the census is safe to trust rather than re-derive.


### [spec.2.review-reliability.1]

DECISION: returned zero findings on the spec staging — BECAUSE the staged block is three descriptive paragraphs about a classification rule; it adds no retry, no lease, no drain, and no recovery path, and the single runtime obligation the reclassification carries is already met and pinned. ALTERNATIVES: filing the session-scoped-fence-versus-pod-wide-hold tension, which the standing-context traps record as declined four times and which §6 makes a non-goal; and filing the equal-generation re-fence refusal, which the summary already records under `## Defects in the shipped tree that this proposal does not stage` with 0080 §1.16 named as owner.

FACT: the one behavioral obligation the reclassification adds is met in the shipped tree in the order `spec/05:515` requires — empty identifier refused with `InvalidArgument` BEFORE `boundSlotState` resolves anything. EVIDENCE: pkg/adapter/coordination.go:108-117 (`sessionID == ""` refusal at `:109-111`, `s.boundSlotState(sessionID)` at `:116`); spec/05_runtime-registry-and-pool-model.md:515 "rejected at the adapter boundary with `InvalidArgument` before any root is resolved".

FACT: the staged envelope clause's assertion that the addressing frame "opens the stream" is true of the shipped adapter — the Checkpoint handler refuses any stream whose first frame is not `CheckpointStart`, before the address check. EVIDENCE: pkg/adapter/checkpoint.go:60-63 `"Checkpoint stream must open with a CheckpointStart"`, then the empty-address refusal at `:82-84`. Nothing in `spec/` states that opening requirement today (grep for `CheckpointStart` in `spec/` returns only `spec/04_system-components.md:151`, `:167`, `:168`, all inside the block SPEC-1 retires), so the staged paragraph adds it rather than restating it. This is not a defect; do not file it as an unstated precondition.

FACT: no spec section outside §4.1 and §4.7.1 classifies a gateway-adapter request message by scope word, so retiring the table strands no scope statement. EVIDENCE: `grep -rn "pod-scoped\|session-scoped" spec/` returns only spec/04_system-components.md:151, :188, :190, :725, :726 for request messages; every other hit is a JSONL frame, a token, a route, or a Postgres table.

FACT: `spec/28_communication-channels.md:314-317` already states the fence is recorded per session ("A fence for one session does not change the generation the pod holds for another"), so the staged reclassification moves §4.1 toward §28 rather than away from it. EVIDENCE: spec/28_communication-channels.md:314-317.

WATCHOUT: the cache key omits `summary.md`, and this run's key `09a51355eaa8` was a miss, so the empty answer recorded here is a real pass rather than a replay. EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/reliability-r2-09a51355eaa8.json.

USEFUL [standing context, Traps]: "Do not re-file the session-scoped fence exiting a pod-wide hold" and "Long lines hide the sentences citations point at" each saved a round. The second is exact: `spec/05:515`, `spec/04:725`, and `spec/04:726` are single very long lines and the cited clause is the last sentence; `awk 'NR==N' | fold` or `tr '|' '\n'` is what reads them.


### [spec.2.review-security.1]

DECISION: Returned an EMPTY findings list under the security lens, the sixth consecutive empty pass for
this lens on this proposal — BECAUSE the proposal's six non-log files are byte-identical to the
`non-spec-r6` and `spec-r1-start` snapshots (`diff -rq` reports only `review-log.md` differing against
both), so the text under review is exactly the text the security lens cleared at `non-spec.5`, and I
re-derived the two lens checks from the tree rather than replaying the standing context. Check (1): the
one control the staging removes is the tier-0 table-reconciliation gate, removed openly, with the
replacement gate's four clauses AND its blind spot stated in normative spec text (spec-changes.md D1
paragraph 3) and both residuals, including the loss of the table's Service/Direction reconciliation,
named in summary OD1 (`summary.md:113-119`). Check (2): the staging contains no security bound at all —
no residual-state limit, no quota or reuse counter, no Redis-backed role, no value sourced from a pod
self-report — ALTERNATIVES: filing the loss of the table's `Service`/`Direction` columns as an unstated
control regression (declined: OD1 names the "wrong service" refusal explicitly among the refusals that
lose their subject with the table); filing the "`oneof` of frames" versus "a `oneof`" predicate gap
(declined: hypothetical about a future message, and the gate's envelope clauses fail closed on every
current construction).

FACT: nothing in `spec/` outside §4.1 hangs a security decision on the gateway-adapter REQUEST message
scope class. A repo-wide grep for `session-scoped|pod-scoped` over `spec/` returns 40 sites; every one
outside `spec/04:190`, `:725`, `:726` is about a different classification (JSONL frame types at
`spec/28:835-844`, tokens at `spec/10:221` and `spec/07:56`, Postgres/Redis routing at `spec/12:578`,
`:202`, gateway routes at `spec/29:505-558`). The §28.5.3 frame-address control and its
`lenny_adapter_unaddressed_frame_rejected_total` metric (`spec/16:189`) are a separate classification the
staging does not touch — EVIDENCE: spec/28_communication-channels.md:838-844; spec/16_observability.md:189

FACT: the coordinator-hold allowlist is keyed on the gRPC full method name, so the reclassification of
`CoordinatorFenceRequest` from pod to session scope cannot change which RPCs a pod in hold state accepts.
`coordinatorHoldAllowedMethods` is a `map[string]bool` consulted as
`coordinatorHoldAllowedMethods[info.FullMethod]` in both the unary and the stream interceptor — EVIDENCE:
pkg/adapter/holdstate.go:52, :336, :349

FACT: re-derived on the tree that the one behavioral obligation the reclassification imports is met on the
handler's first branch. `CoordinatorFence` returns `InvalidArgument` on an empty session id BEFORE
`boundSlotState` resolves anything and before the generation checks, so the `spec/05:515` refusal is not
met incidentally by a later resolve failing — EVIDENCE: pkg/adapter/coordination.go:107-122

FACT: no reader-facing surface mirrors the retired table. `grep -rn "request-message-scope|Request Message
Scope"` across `spec/ docs/ tests/ schemas/ charts/` returns exactly one hit, the heading SPEC-1 keeps, and
`docs/` names RPCs rather than request message types, so retiring the table owes no docs edit and no anchor
redirect — EVIDENCE: spec/04_system-components.md:149

USEFUL [non-spec.5.review-security.1]: its two USEFUL pointers are still the whole answer. The
method-name-keyed hold allowlist closes the only plausible control regression, and the strict-superset
property of the derived session class (delta of exactly one message, pod to session) is what makes the
retirement fail-closed-safe, because the `spec/05:515` empty-identifier refusal hangs off the session class
and can therefore only be added. I re-derived both independently and reached the same answer.

WATCHOUT: the cache key remains `09a51355eaa8` and is still `md5(spec-changes + non-spec-changes +
implementation-checklist)` with no lane and no `summary.md` component, but the FILE name carries the lane
and round (`security-r2-<H>.json`), so a lane change produces a miss even on identical text. That is why
this round re-ran a lens that had already cleared this exact text five times. A future orchestrator that
wants to skip a settled lens has to compare snapshots, not rely on the cache — EVIDENCE:
scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/

OPEN: nothing new. Six consecutive empty security passes on text that has not changed since round 4 of the
non-spec lane. The lens should be retired on this proposal unless a fix pass edits the staged §4.1
constraint paragraph, OD1's residual statement, or `pkg/`-facing prose.


### [spec.3.review-applicability.1]

DECISION: returned an empty findings list for the spec staging — BECAUSE I simulated S1→S2→S3 end to end against the tree and every anchor SPEC-1 names resolves uniquely, the replacement text is quoted verbatim so nothing is left to invent, the only forward reference (paragraph 3 naming "a tier-0 gate" S2 creates) is prose that applies cleanly, and the one existing gate S1 reddens has its disposition written on S1's own line. ALTERNATIVES: filing the S1 red window as class-4 gate state (rejected: recorded disposition, and spec-first ordering is mandated by spec-driven-development.md so no other order is legal); filing the retired table's Service and Direction columns as class-3 content loss (rejected: `spec/04_system-components.md:698-726` carries both direction tables over every documented RPC, and the service is in the proto).

CORRECTS [standing context "Proto anchors, current and verified"]: the entry says SPEC-1's `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s address is "one line off". It is exact. `:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. Do not "fix" SPEC-1 to `:457`.

FACT: every citation in `spec-changes.md` re-verified this round and all resolve: `spec/04_system-components.md` `:151`/`:153-186`/`:188`/`:190`/`:725`/`:726`; `spec/10_gateway-internals.md:57` (fence is the only exit from hold) and `:60` (last sentence: "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"); `spec/05_runtime-registry-and-pool-model.md:515`; `schemas/lenny-adapter.proto:458`, `:499-503`, `:1217`; `pkg/adapter/checkpoint.go:74-84`; `tests/tier0_static/adapter_proto_parse_test.go:10-15` and `:64-67`; `tests/tier0_static/claim_register_proto_agreement_test.go:64`; `docs/reference/adapter-contract.md:81`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`; 0080 §1.16 at `proposals/0080_...md:216`. — EVIDENCE: spec/04_system-components.md:149-192

FACT: the replacement gate is green on the shipped proto, re-measured this round by a different method than earlier rounds. `grep -c "SessionId session_id" schemas/lenny-adapter.proto` returns 26, and `grep -n "SessionId " schemas/lenny-adapter.proto | grep -v session_id` returns only `596:message SessionId {`. So the biconditional holds in both directions with no exception, and `oneof` appears at only `:1174` (CheckpointRequest) and `:1250` (CheckpointResponse), the latter being the request type of no RPC (`:133` is `rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse)`). — EVIDENCE: schemas/lenny-adapter.proto:596, :1174, :1250, :133

FACT: nothing outside the retiring gate reads the §4.1 table. `grep -rn "message-scope\|Request Message Scope\|message scope" spec/ docs/ schemas/ charts/ tests/ pkg/ cmd/` returns only `spec/04_system-components.md:149` (the heading SPEC-1 keeps), the two test files §9 already lists, and five `// spec: 4.1 (request message scope)` annotations in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` plus one in `tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99`, all of which stay true because §4.1 still owns request message scope. §9 is complete. — EVIDENCE: tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99

FACT: `pkg/` cites §4.1 for message scope in exactly two places, `pkg/adapter/checkpoint.go:74` and `pkg/adapter/checkpoint_stream_test.go:816`, both "Checkpoint is session-scoped", which the envelope clause preserves. No code comment claims `CoordinatorFenceRequest` is pod-scoped; `pkg/adapter/coordination.go:151` says "the pod-scoped hold state", which is the hold rather than the request. — EVIDENCE: pkg/adapter/coordination.go:151

WATCHOUT: `spec/04_system-components.md:698-719` (the §4.7.1 Gateway→Adapter RPC table) is NOT exhaustive — it omits `SendMessage`, `SignalDeadline`, `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`. After SPEC-1 the §4.1 table is gone, so `spec/` no longer enumerates the full request-message set anywhere. This is not a defect of this proposal (the rule quantifies over every request message by construction) but a later agent measuring "what the spec still enumerates" will hit it and may misread it as content loss. — EVIDENCE: spec/04_system-components.md:700-719

DEFERRED [0075_....spec-changes.md]: re-stating the deferred entry from the non-spec loop that this loop did not close, with my own judgment attached. D2 at `spec-changes.md:38` says the addressing convention is one "that nothing checks today". `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`) already asserts, for each of the 18 members of `sessionScopedMessages`, that `session_id` exists and that `f.Message().FullName() == "lenny.adapter.v1.SessionId"`, which is one direction of the biconditional over a hand-maintained subset. What is true instead: no gate checks the convention in both directions over every message the protocol declares. I did NOT file it as a finding: read with D2's own next clause, "the convention" is defined as the full biconditional over every declared message, and on that reading the sentence survives, so a filing would likely be refuted as wording. A fixer touching D2 for another reason should tighten it; nobody should spend a round on it alone.

USEFUL [standing context / Traps 70, 73, 74, 75, 76, 79, 81, 82]: each of these named a candidate I reached independently this round and would otherwise have spent time deriving — the unsatisfiable retired-number column, the "only way a request addresses a session" tension against the envelope frame, the nested-address hole, the §4.7.1 declared-versus-derived tension, the session-scoped fence exiting a pod-wide hold, the indefinite "a protocol definition" against `schemas/lenny-interceptor.proto`, and `parseMessageScopeTable` reading the whole file. The Traps section is carrying its weight; do not cut it.


### [spec.3.review-citations.1]

DECISION: filed exactly one finding, D2's "an addressing convention that nothing checks today" — BECAUSE the Deferred register already carried it as a derived-but-unlanded correction naming `spec-changes.md`, a file THIS loop owns, and the claim is falsified by a test in the tree — ALTERNATIVES: leaving it to the between-loops pass (it has already survived at least one such pass); filing the `:127-129` vs `summary:203` gate-limit tension (declined, reconcilable wording, two rounds already judged it so).
USEFUL [Deferred entry on spec-changes.md D2]: the "nothing checks today" correction was already derived by a non-spec-lane agent that could not edit the file. It was correct and it is now filed. A DEFERRED naming a file the NEXT loop owns should be re-checked by that loop's reviewers first thing; it is free findings.
CORRECTS [Settled: "Proto anchors, current and verified"]: the entry says `ReportSessionScrubRequest` `:456` "with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is wrong. `:457` is `string pod_id = 1;` and `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` citation is exactly right; nothing is off by one. — EVIDENCE: schemas/lenny-adapter.proto:456-458
FACT: every concrete citation in `spec-changes.md` re-verified against the tree at this commit and all hold: proto `:1173` `:1174` `:1193` `:1217` `:1250` `:1455-1456` `:458` `:499-503`; spec/04 `:149` `:151` `:153-186` `:175` `:188` `:190` `:725` `:726` (§4.7.1 runs `:692-727`); spec/05 `:515`; spec/10 `:57` `:60`; checkpoint.go `:74-84`; parse test `:10-15`; claim-register caller `:64`; gate `:17-27` `:54` `:75-81`; tier-11 gate `:43-76` and docs `:81`; spec-map `:156` `:169` `:5670`; 0076 OD3 A/B and 0080 `§1.16` / `§2:453-454`. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:1-187
FACT: `:151` really does carry the `CheckpointRequest`-envelope clause SPEC-1 says it does; the line is five sentences long and only the last one carries it, so a truncated grep makes SPEC-1 look like a false citation. Pipe it through `tr '.' '\n'`. — EVIDENCE: spec/04_system-components.md:151
FACT: the addressing convention holds in both directions on the shipped proto today: 26 `SessionId session_id` declarations, zero `session_id` of another type, zero `SessionId` under another name. The replacement gate is green on day one. — EVIDENCE: schemas/lenny-adapter.proto (grep census)
WATCHOUT: the round-3 spec snapshot `scratchpad/cp-snap/.../spec-r2` is byte-identical to the live directory, so `diff -ru` prints nothing. That is the documented normal case, not a broken snapshot; use `git log -S` on a phrase to date text instead. `git log -S "nothing checks today"` dates D2's phrase to `96184d83e`, the post-spec-loop baseline, so it is this-run text.


### [spec.3.review-client-surface.1]

DECISION: Returned an EMPTY findings list under the client-facing-surface lens — BECAUSE the staged spec edits touch one `spec/04` block that no client-facing representation mirrors, and I re-verified every proto/docs/schema anchor SPEC-1 cites rather than trusting the standing context — ALTERNATIVES: filing `docs/api/internal.md:209-215`'s false `CheckpointRequest` excerpt (rejected: it is wrong about the shipped proto today, so it does not "become wrong" from these edits, and the review log already carries it as a DEFERRED); filing `spec/04:712` / `docs/reference/adapter-contract.md:69`'s "to the pod" fence wording (rejected: a 0076 residue, and `spec/04:190`'s `ShutdownRequest` precedent makes address-versus-effect divergence legal).

FACT: `spec-changes.md` is BYTE-IDENTICAL to the `spec-r2` snapshot (md5 `ab8f41a19b6745e8681251eac8f33f6c` on both), so `diff -ru` against `spec-r2` prints nothing and there is no "newest text" to read hardest. Diff against `spec-r3`/`spec-r4`/`spec-r5` (md5 `8c0f6294204d1cf1cdd84a3475ebdf0a`) if you need the older delta. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md

CORRECTS [standing context, Settled, "Proto anchors, current and verified"]: that entry says `ReportSessionScrubRequest`'s address is at `schemas/lenny-adapter.proto:457` and that "SPEC-1 cites `:458`, one line off". The proposal is right and the log entry is wrong: `grep -n "SessionId session_id" schemas/lenny-adapter.proto` returns `458:  SessionId session_id = 2;`, and `:457` is the closing line of the preceding doc comment. Do not "fix" SPEC-1's `:458`.

FACT: every anchor SPEC-1 and §4 cite that falls on a client surface re-verifies exactly at HEAD: `schemas/lenny-adapter.proto:1217` (`SessionId session_id = 7` in `CheckpointStart`), `:458` (`ReportSessionScrubRequest`), `:499-503` (`ReportPodScrubRequest` = `string pod_id`/`PodScrubOutcome outcome`/`string detail`, opening and closing brace inclusive), `spec/04:725`/`:726`, `spec/04:190`, `spec/10:57`/`:60`, `spec/05:515`, `pkg/adapter/checkpoint.go:74-84`, `docs/reference/adapter-contract.md:81`, `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`, `tests/tier0_static/adapter_proto_parse_test.go:10-15`, `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`, `tests/tier0_static/claim_register_proto_agreement_test.go:64`. None is off by a line. — EVIDENCE: schemas/lenny-adapter.proto:499-503

FACT: `schemas/lenny-adapter.proto`'s own doc comments for `CoordinatorFence` and `CoordinatorFenceRequest` were already rewritten per-session by 0076 ("The pod records the generation against the session the fence names", "A fence for one session does not change the generation the pod holds for another"). The wire contract's authoring source therefore AGREES with the session-scoped reclassification SPEC-1 lands; there is no proto edit owed and no unmirrored representation. A reviewer expecting to find the old pod-wide wording there will not. — EVIDENCE: schemas/lenny-adapter.proto:152-166, :1449-1461

FACT: five tier-11 test functions carry `// spec: 4.1 (request message scope)` in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` (`:26`, `:132`, `:408`, `:582`, `:747`), which is the largest §4.1 citation cluster outside the retiring gate. None of them reads `spec/04` §4.1; they read `docs/runtime-author-guide/*` and `docs/tutorials/*` for the JSONL echo obligation, and SPEC-1 keeps the `#### Request Message Scope` heading their annotation names. No edit is owed and the file does not belong in §9. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

USEFUL [standing context, Settled, "No docs mirror"] and [spec.4.review-client-surface.1]: their conclusion that no client-facing artifact classifies a gateway-adapter request message by scope held on a fresh independent sweep (`grep -rn "pod-scoped\|session-scoped" docs/ spec/`, `grep -rn "CoordinatorFence\|coordination_generation" docs/ schemas/ sdks/ charts/ pkg/embedded/`). The only docs hits are JSONL-leg prose, the upload-token gloss, and `docs/reference/adapter-contract.md:69` (scope-free) and `:81` (the §4.7.1 sentence SPEC-1 keeps). This lens has now confirmed it a fifth time; spot-check rather than re-derive.

USEFUL [standing context, Traps, entry on naive proto parsers]: `grep -n "^service \|^  rpc "` plus `grep -n "SessionId \|oneof"` reproduces the whole census without a brace parser — 24 `Adapter` RPCs plus 7 `GatewayControl` = 31 request types, 26 `SessionId session_id` field declarations (25 request types plus `CheckpointStart`), zero name/type convention violations, and exactly two `oneof` blocks at `:1174` and `:1250`. That is enough to check every client-surface claim in SPEC-1 without writing a parser.


### [spec.3.review-docs-alignment.1]

DECISION: Returned an EMPTY findings list on the staged spec edits — BECAUSE the whole staged text is byte-identical to the `spec-r1-start` snapshot (`diff -rq spec-r1-start` and `spec-r2-start` against the live directory report nothing but `review-log.md`), so no new staged text exists for this lens, and an independent re-sweep of `docs/` reconfirmed that no page mirrors the §4.1 request-message-scope classification, that this proposal changes no metric, alert, flag, endpoint, error code, or default, and that every docs-facing citation SPEC-1 makes resolves — ALTERNATIVES: re-filing `docs/api/internal.md:209-215`/`:273` (rejected on the same ground `spec.1.review-docs-alignment.1` and `non-spec.5.review-docs-alignment.1` recorded: the page is false about the shipped proto today, so it does not *become* wrong, and its only remedy is a docs edit this spec-only loop may not land); filing `spec/04_system-components.md:712` (the `CoordinatorFence` §4.7 row's "Announce ... to the pod" and "Precondition for any subsequent operational RPC", which the derived rule now classifies the other way) — rejected because 0076 falsified it before SPEC-1 exists and `spec/28_communication-channels.md:314-317` already contradicts it, so SPEC-1 does not create the contradiction; it is already carried as DEFERRED with a named owner.

CORRECTS [Standing context → Settled → "Proto anchors, current and verified"]: the parenthetical "`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)" is wrong. `message ReportSessionScrubRequest {` is line 456, `string pod_id = 1;` is 457, and `SessionId session_id = 2;` is 458. SPEC-1's `schemas/lenny-adapter.proto:458` is exactly right and must not be "corrected" to `:457`. The off-by-one came from a `sed -n '450,510p'` whose first output line was miscounted; `grep -n` is the only reliable way to anchor in this file.
EVIDENCE: schemas/lenny-adapter.proto:456-458; 0075_....spec-changes.md (SPEC-1, "`schemas/lenny-adapter.proto:458`")

FACT: nothing anywhere in the tree references the `#### Request Message Scope` heading or a `#request-message-scope` anchor outside `spec/04_system-components.md:149` itself — a repo-wide grep over `*.md`, `*.go`, `*.json`, and `*.yaml` (excluding `proposals/` and `scratchpad/`) returns that single line. No diagram under `docs/assets/diagrams/` mentions `CoordinatorFence`, `session_id`, or either class word. SPEC-1 therefore owes no anchor redirect and no diagram edit.
EVIDENCE: spec/04_system-components.md:149

USEFUL [spec.1.review-docs-alignment.1]: its FACT that `docs/` names RPCs and never request message types held for the seventh time and is again the sole reason this lens returns empty. Two greps reproduce it (`session-scoped|pod-scoped` and `CoordinatorFence|CheckpointRequest|CheckpointStart` over `docs/`); do not sweep page by page.

USEFUL [non-spec.5.review-docs-alignment.1]: its MISTAKE entry ("I nearly re-filed `docs/api/internal.md:209-215` on the docs-vs-spec reading") is what stopped me reaching the same edge a third time. Keep it if the Traps section is ever trimmed; it is worth more than the DEFERRED row it points at.


### [spec.3.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site-completeness lens — BECAUSE I enumerated every identifier and every fact the staged spec block adds, changes, or removes (the §4.1 declared-scope table's 32 rows, the `:151` introducing paragraph, the `:188` grounding paragraph, the `session_id`/`SessionId` name-and-type biconditional, the `oneof` stream-envelope predicate, and the fence's pod→session reclassification) and swept `spec/`, `docs/`, `schemas/`, `charts/`, and the driving/residual registers for each; every surface that mentions the subject either agrees with the derived classification already or is on the proposal's edit list — ALTERNATIVES: filing `docs/api/internal.md:209-215` as a newly-inconsistent docs surface (rejected: the excerpt is already false about the shipped proto today, its remedy is a docs edit this loop may not own, and it is already carried as a DEFERRED); filing D2's "an addressing convention that nothing checks today" (rejected: it is an overstated rationale sentence, it carries no citation, and applying it leaves neither the spec inconsistent nor the implementation broken, so it fails every clause of the bar — see the DEFERRED note below).

FACT: the whole edit-site sweep now has a mechanical answer, so a later lens can spot-check instead of re-deriving. Nothing outside `spec/04_system-components.md` classifies a gateway-adapter request message: `grep -rn "session-scoped\|pod-scoped" spec/ docs/ schemas/ charts/` returns exactly three classifying sentences outside the retired block, `spec/04:190` (`ShutdownRequest`, session), `:725` (`ReportSessionScrub`, session), `:726` (`ReportPodScrub`, pod), and all three agree with the derivation. `schemas/lenny-adapter.proto` carries no `pod-scoped`/`session-scoped` string at all and no `§4.1` reference on any fence or checkpoint message. `charts/` and `sdks/` name none of the subject. — EVIDENCE: spec/04_system-components.md:190,725,726

FACT: the retired block `spec/04_system-components.md:149-191` carries no markdown link, no `§` reference, and no retired channel spelling, so it owes no anchor redirect, no citation-resolution entry, and no identifier-sense rekey. The retired spellings the naming table lists (`LifecycleChannel`, `controlchannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`, `spec/28_communication-channels.md:151-158`) occur ZERO times in the whole of `spec/04_system-components.md`, even though `tests/registers/identifier-senses.yaml:14-36` carries eight `spec/04` occurrence-keyed rows. Those rows are spent register history; deleting the block shifts no occurrence index. — EVIDENCE: spec/28_communication-channels.md:151-158; tests/registers/identifier-senses.yaml:14-36

FACT: `#### Request Message Scope` is an h4 with no entry in `spec/README.md` and no inbound `#request-message-scope` link anywhere in the tree, and SPEC-1 keeps the heading, so the heading-walker and fragment-link tier-0 gates are unaffected either way. — EVIDENCE: spec/README.md:15; spec/04_system-components.md:149

FACT: no tier-0/11 gate other than the one TEST-1 replaces reads the §4.1 block. `tests/tier0_static/adapter_proto_event_taxonomy_test.go` checks the proto's own doc comments for a `spec/04_system-components.md` pointer rather than reading the spec file; `tests/tier10_conformance/adapter_contract_intrapod_pointer_test.go:128-134` reads `spec/04` only to assert `INTERRUPT_TIMEOUT` is absent from it; the two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) reference neither `spec/04` nor §4.1; `tests/tier0_static/gate_integrity_test.go:63-90`'s `tierZeroGates` does not list the retiring gate; `tests/tier0_static/spec_map_slot_address_registration_test.go:336-337` keys the gate and the parse by PATH, which the replacement keeps. `tests/claim-map.json` carries no §4.1 row. — EVIDENCE: tests/tier0_static/gate_integrity_test.go:63; tests/tier0_static/spec_map_slot_address_registration_test.go:336

FACT: `spec/` contains no reference to a TEST tier anywhere. `grep -rniE "tier[- ]?(0|zero|3|11)\b" spec/*.md` returns only capacity Tiers (Tier 1/2/3 load tiers). The staged third paragraph's sentence "A tier-0 gate refuses a protocol definition in which…" would therefore be the first test-tier reference in `spec/`. I did not file it: it makes no applied text wrong, and §28.4 already has the specification naming `tests/claim-map.json`, so the precedent for spec text naming a test artifact exists. A later reviewer who wants to raise it should raise it as a convention question, not as a defect. — EVIDENCE: spec/28_communication-channels.md:160-166

DEFERRED [0075_....spec-changes.md]: D2's clause "What the rule rests on is an addressing convention that nothing checks today" is false about the tree. `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each of the 18 members of `sessionScopedMessages`, that the message declares `session_id` and that its type is `lenny.adapter.v1.SessionId` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`). What is true instead: no gate checks the convention in BOTH directions over EVERY message the protocol declares, and the existing tier-3 arm covers one direction over a hand-maintained subset. This restates the same DEFERRED already in the review log's standing context; I re-derived it independently and it holds. It is below the finding bar (no citation is false, and applying D2 leaves nothing inconsistent), so it needs the between-loops pass rather than a finding.

USEFUL [Traps: naive proto parsers derail on this file]: the brace-delta-per-line, comment-stripped parser it prescribes reproduced the census on the first run: 31 RPC request types, 25 with a top-level `SessionId session_id`, the 6 without being `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; 26 `SessionId` field declarations in the whole file and zero name/type biconditional violations; the only two `oneof` blocks at `:1174` and `:1250`.

USEFUL [Traps: orchestrator briefs carry stale content]: the r3 brief still gives the pre-refresh proto anchors (`SessionId` at `:589`, `CoordinatorFenceRequest` at `:1447/:1448`, `CheckpointRequest` at `:1166`), each seven lines low. The live anchors are `:596`, `:1455/:1456`, `:1173`, which is what the proposal cites. Verify against the tree, never against the brief.

FACT: the table arithmetic re-derived from the live table rather than from the log: `sed -n '155,186p' spec/04_system-components.md` gives 32 rows, 26 session (including `CheckpointRequest` and the `CheckpointStart` frame row) and 6 pod (`AdapterEventsRequest`, `CoordinatorFenceRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). D1's claim that the derivation reproduces every row decomposes as 24 non-envelope session request types + 5 non-envelope pod request types + the fence + the two envelope rows = 32. — EVIDENCE: spec/04_system-components.md:155-186

WATCHOUT: the proposal's six landing files are BYTE-IDENTICAL to the `spec-r2` snapshot (`diff -q` per file returns nothing for spec-changes, non-spec-changes, implementation-checklist, summary, problem-statement, and status). A lens that reads `diff -ru` for "what the fixer changed" gets nothing, and that is the true state rather than a broken snapshot. Read the whole document instead of the diff. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2/


### [spec.3.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action feasibility lens — BECAUSE every action the staged block and its supporting sections assign has an actor that exists under that name and can perform it: the tier-0 gate reads `schemas/lenny-adapter.proto` text alone (no cross-resource resolution, no apiserver, no RBAC), the adapter already performs the empty-address refusal the reclassification newly obliges, and the gateway already supplies the address the refusal demands — ALTERNATIVES: filing "the gate cannot resolve a `oneof` arm's type to a top-level message from the shared text parse" (rejected: `CheckpointStart` is a top-level message in the same file, so arm-type -> `protoFields[name]` is a map lookup once TEST-1 adds the type column); filing "paragraph 2 asserts the addressing frame opens the stream and no gate can check that" (rejected: the adapter enforces it at `pkg/adapter/checkpoint.go:61-64`, and the claim is a statement of the protocol rather than an obligation on the gate).

FACT: the reclassification's one new runtime obligation is met end to end, verified on both legs. Caller: `pkg/gateway/runtime/adapterclient/coordinatorfence.go:51-54` builds `CoordinatorFenceRequest{SessionId: &adapterv1.SessionId{Value: sessionID}, ...}` from a `sessionID string` parameter, so the gateway always addresses the fence. Callee: `pkg/adapter/coordination.go:108-111` refuses an empty identifier with `InvalidArgument` before `boundSlotState` at `:116` resolves anything, which is exactly what `spec/05_runtime-registry-and-pool-model.md:515` demands of a session-scoped request. Nobody had checked the caller half before.
EVIDENCE: pkg/gateway/runtime/adapterclient/coordinatorfence.go:51-54; pkg/adapter/coordination.go:108-116; spec/05_runtime-registry-and-pool-model.md:515

FACT: the adapter enforces "the address-declaring frame opens the stream", so paragraph 2's "that frame ... opens the stream" is not an unbacked assertion. `pkg/adapter/checkpoint.go:61-64` returns `InvalidArgument "Checkpoint stream must open with a CheckpointStart"` on any other first frame, and `:81-84` then refuses an empty address.
EVIDENCE: pkg/adapter/checkpoint.go:60-64, :74-84

FACT: re-verified mechanically today at HEAD `3224c6271`, all green: zero fields named `session_id` with a type other than `SessionId`, zero fields of type `SessionId` under another name (`grep -n '^\s*\(repeated \)\?SessionId \w' | grep -v 'SessionId session_id'` is empty), and exactly two `oneof` blocks, at `schemas/lenny-adapter.proto:1174` and `:1250`. The replacement gate is green on the shipped proto on day one, so it creates no red window of its own beyond the S1-to-S2 one the checklist already records.
EVIDENCE: schemas/lenny-adapter.proto:1174,1250

FACT: no spec, docs, schema, or chart surface outside `spec/04` §4.1 depends on the retired table. `grep -rn "classification table\|message-scope\|Request Message Scope\|request-message-scope" spec/ docs/ tests/` outside `proposals/` returns only the §4.1 heading itself (`spec/04_system-components.md:149`), the retiring gate file, the shared parse's comment, and the tier-3 comment at `session_address_wire_test.go:40` — every one of which TEST-1 or TEST-2 already owns. `grep -rn "pod-scoped" docs/ charts/ schemas/` returns one unrelated runbook line. This confirms the standing-context "no docs mirror" and "no inbound reference" bullets by a different query than the ones that produced them.
EVIDENCE: spec/04_system-components.md:149; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40; docs/runbooks/dual-store-unavailable.md:68

USEFUL [standing context, Traps]: the "do not re-file" list retired four candidate findings before I spent a round on them — the "only way a request addresses a session" tension, the nested-address hole, the §4.7.1 declared-per-message tension, and the session-scoped fence exiting a pod-wide hold. Each is exactly where a feasibility lens lands first.

WATCHOUT: the `spec-r2` snapshot is byte-identical to the live proposal directory (`diff -rq` prints nothing), so round 3's fixer changed nothing and "read the changed sections first" yields no reading order. Review the whole document.
EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2

UNVERIFIED: still nobody has run tier 0 or tier 3. Every feasibility claim here is derived from reading files.


### [spec.3.review-fresh.1]

DECISION: returned an empty findings list for the spec staging — BECAUSE every citation, anchor, and arithmetic claim in `spec-changes.md` re-verified exact against the tree today, and the two candidate defects I derived independently (see below) both fall under the "does not make the applied spec wrong" exclusion or under a trap already declined six times — ALTERNATIVES: filing the D2 "nothing checks today" overstatement (rejected, see below) and filing the `spec/04:190` "`ShutdownRequest` is session-scoped" declaration-inside-§4.1 tension (rejected: it is the same argument as Trap "§4.7.1 declared per message", the value agrees with the derivation, and SPEC-1 states why `:190` stands).

CORRECTS [Settled bullet "Proto anchors, current and verified"]: that bullet says `ReportSessionScrubRequest` has "its address at `:457` (SPEC-1 cites `:458`, one line off, below the bar)". That is backwards. `schemas/lenny-adapter.proto:457` is `  string pod_id = 1;` and `:458` is `  SessionId session_id = 2;`. SPEC-1's `:458` citation is EXACT. A fixer who "corrects" `:458` to `:457` on the strength of that bullet will introduce a false citation into the staged text. — EVIDENCE: schemas/lenny-adapter.proto:456-458

FACT: the whole proto census, the addressing biconditional, the 18-member `reserved "slot_id"` set, and the 8 additions were re-derived from scratch with a brace-counting comment-stripping parser and match the log exactly: 31 request types, 25 address-declaring, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); 26 `SessionId`-typed fields all named `session_id`, zero `session_id` fields of another type; the 18 `reserved "slot_id"` messages are exactly today's `sessionScopedMessages` keys; `set(addr) - cur18` is exactly the 8 additions the non-spec staging names. — EVIDENCE: schemas/lenny-adapter.proto, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

FACT: `§9 Files touched on application` is complete. A repo-wide grep for the gate file path returns only `tests/spec-map.json:156,169,5670` and `tests/tier0_static/spec_map_slot_address_registration_test.go:336` (path-only, unchanged); the shared parse has exactly two callers (`adapter_proto_message_scope_test.go:87`, `claim_register_proto_agreement_test.go:64`); `tests/change-graph.json` and `tests/claim-map.json` name none of the touched files; no tier-11 gate reads the `#### Request Message Scope` block; `docs/` carries no mirror of the table. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36,68; tests/change-graph.json:399

DEFERRED [proposals/0075_.../0075_....spec-changes.md]: this is the one Deferred entry from the previous loop whose remedy lands in a file THIS loop edits, and it is still unapplied. D2 states "What the rule rests on is an addressing convention that nothing checks today". `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each of the 18 `sessionScopedMessages` members, that `session_id` exists and is of type `lenny.adapter.v1.SessionId` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:107-114`). What is true instead: no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset. I did NOT file it as a finding: it is rationale rather than staged spec text, the replacement gate's justification survives the correction unchanged, and under a whole-conjunction reading of "the convention" the sentence is defensible. The inter-loop pass, not a finding, is where it belongs. — EVIDENCE: 0075_....spec-changes.md D2; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116

WATCHOUT: `spec/10_gateway-internals.md:60` ends with "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped" and `:57` with "which is the only way to exit hold state", but both are the LAST clauses of very long single lines. `sed -n '57p' | cut -c1-200` shows neither and makes SPEC-1's citation look wrong. Pipe through `tr '.' '\n'`. — EVIDENCE: spec/10_gateway-internals.md:57,60

USEFUL [Standing context / Traps]: the trap list is why this pass cost one round instead of four. In particular "Naive proto parsers derail on this file", "Long lines hide the sentences citations point at", and the four "do not re-file" entries each stopped a line of inquiry I had already started.


### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the staged spec edits carry no Kubernetes surface at all: the replaced block is three prose paragraphs classifying gRPC request messages on the gateway-adapter protocol, and they name no CRD, no status subresource, no finalizer, no controller, no admission webhook, and no apiserver access. ALTERNATIVES: I considered filing the loss of the retired table's `Service` and `Direction` columns as a missing edit site, and dropped it — that is not this lens and the columns are re-derivable from the two `service` blocks in `schemas/lenny-adapter.proto`.
FACT: nothing under `pkg/apis/`, `pkg/controller/`, or `charts/` references `CoordinatorFence`, the phrase `Request Message Scope`, or the retired table; the only tree-wide hit for the heading is the heading itself — EVIDENCE: spec/04_system-components.md:149 (sole hit of "Request Message Scope" across spec/, docs/, tests/, pkg/); `grep -rln CoordinatorFence pkg/apis charts/ pkg/controller` returns nothing and `docs/reference/adapter-contract.md` is the only docs hit.
FACT: the §4.1 anchors this proposal cites are exact on the tree today — heading `:149`, introducing paragraph `:151`, table `:153-186` with 32 data rows at `:155-186`, fence row `:175`, grounding paragraph `:188`, `ShutdownRequest` paragraph `:190`, `### 4.2` at `:192` — EVIDENCE: spec/04_system-components.md:149-192.
FACT: no spec section outside §4.1 and §4.7.1 classifies a gRPC request message by scope; every other `session-scoped`/`pod-scoped` hit in `spec/` is about JSONL frames (spec/28:804, :835-844), tokens (spec/10:221, spec/07:56), storage routing (spec/12:578-602), the hold gauge (spec/10:60), or HTTP routes (spec/29:505-558). None is a Kubernetes object surface, so retiring the table reddens no controller-side contract — EVIDENCE: spec/10_gateway-internals.md:60, spec/28_communication-channels.md:838.
WATCHOUT: the round-3 spec-lane snapshot at scratchpad/cp-snap/.../spec-r2 is byte-identical to the live proposal directory, so `diff -ru` prints nothing. The standing context already records this; do not read the empty diff as a broken snapshot or as "nothing to review".


### [spec.3.review-mechanism.1]

DECISION: returned an empty findings list for the staged spec edits — BECAUSE I traced every flow the staged §4.1 block describes from the protocol definition through the derivation, the gate, and the downstream consumers (`spec/05:515` refusal, `pkg/adapter/coordination.go:109-111`, `pkg/adapter/checkpoint.go:82-84`, `spec/04:725`/`:726`, `spec/10:57`/`:60`, `spec/28:314-317`) and found no state where the rule, the gate, and the shipped tree diverge — ALTERNATIVES: filing the "P2 asserts the addressing frame *opens* the stream while the gate only checks exactly-one-address" gap (declined: the classification derives correctly either way, so it is an unenforced descriptive claim, not a mechanism failure); filing the "oneof of frames" undefined-discriminator ambiguity (declined: hypothetical future message, and specifying it would be over-specification).

FACT: the four gate clauses are stated identically in three places with no drift — staged spec P3 (`spec-changes.md:20-25`), D2 (`:38-45`), and the non-spec Testing section (`non-spec-changes.md:86-89`). I checked conjunct by conjunct. A future predicate-drift lens can spot-check rather than re-derive.
EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:20-25

FACT: the gate's failure modes are exhaustive as stated. Address spellings partition into four cases: conventional name + conventional type (derives correctly); conventional name + unconventional type (clause a refuses); unconventional name + conventional type (clause b refuses); both unconventional (invisible, and that is exactly the limit the staged paragraph states). Over-inclusion is impossible because P1 stipulates session scope as declaring the field, so the only failure direction is under-inclusion. This is why the single-limit sentence is accurate and D4's "future author names a pod-scoped guard `session_id`" concern is not a second unstated limit.
EVIDENCE: proposals/0075_.../0075_....spec-changes.md:23-25

FACT: the envelope arm IS buildable from the extended shared parse, which closes a standing OPEN. `protoFields` returns `map[string]map[string]bool` (`tests/tier0_static/adapter_proto_parse_test.go:36-62`) — field names only. With TEST-1's added per-field type and `oneof` membership the gate walks `CheckpointRequest`'s oneof arms to their type names (`CheckpointStart`/`CheckpointGrant`/`CheckpointAbort`), looks each up as a top-level message in the same map, and tests for a `session_id` field of type `SessionId`. No cross-file resolution is needed: `schemas/lenny-adapter.proto` imports only `google/protobuf/timestamp.proto` (`:18`) and declares every message at the top level.
EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:33-62, schemas/lenny-adapter.proto:18

FACT: no message in `schemas/lenny-adapter.proto` declares `session_id` inside a `oneof`. All 26 `SessionId session_id` declarations are top-level fields of top-level messages (25 RPC request types plus `CheckpointStart` at `:1217`). That is why "top-level" in P1 costs the gate nothing extra to evaluate and why `protoFields`'s oneof-folding does not currently produce a false positive.
EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

FACT: the deleted block (`spec/04_system-components.md:149-191`) contains no `CH-`/`LNK-`/`REG-` identifier and no retired channel spelling, so the position-keyed `tests/registers/identifier-senses.yaml` rows for `spec/04_system-components.md` (occurrences 1-8, all `CH-RUNTIMEOPS`) do not renumber when the block shrinks. This closes the "does the deletion shift a positional register" question without re-deriving it.
EVIDENCE: tests/registers/identifier-senses.yaml:13-36

FACT: no other gate reads the §4.1 table. `spec_47_rpc_row_naming_test.go` scopes itself to `### 4.7 ` via `specSection` (`:46`); `adapter_contract_intrapod_pointer_test.go:128-134` reads `spec/04` whole but only for the string `INTERRUPT_TIMEOUT`; `session_scrub_report_addressing_doc_reconciliation_test.go:47` scopes to `### 4.7 `. Only the retiring gate's `parseMessageScopeTable` sees the rows.
EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:46, tests/tier10_conformance/adapter_contract_intrapod_pointer_test.go:128-134

FACT: `spec/28:161-165` scopes the §28.4 claim-register obligation to "Every normative statement **this section** makes", so the new §4.1 gate sentence owes no `tests/claim-map.json` row. A reviewer reaching for channel-naming.md N4 will otherwise re-derive this.
EVIDENCE: spec/28_communication-channels.md:161-165

USEFUL [Standing context, "Traps"]: the "Naive proto parsers derail on this file" and "Do not re-file the 'only way a request addresses a session' tension" entries each saved a full derivation. I used `awk` on `^message X {` plus a direct `SessionId` grep instead of writing a brace parser, and did not re-open the paragraph-1/paragraph-2 tension.

USEFUL [Standing context, "Snapshot diffs are routinely empty"]: `diff -ru` against `spec-r2` printed nothing, and against `non-spec-r6` also printed nothing. The spec-changes and non-spec-changes text has been byte-stable since the non-spec-r6 snapshot; nothing in this round's reading order was actually new.


### [spec.3.review-operational.1]

DECISION: empty findings for the operational lens, third consecutive clean pass on this text — BECAUSE the staged SPEC-1 block touches no metric, no alert, no CRD condition, no runbook, and no operator-facing doc, and every observability citation it rests on re-reads as cited — ALTERNATIVES: none newly considered; the two candidates the lens reaches for (the session-scoped fence exiting a pod-wide hold, and `spec/04:712`'s "Announce ... to the pod" row) are a Standing-context trap and an existing DEFERRED entry respectively.

FACT: the proposal directory is byte-identical to the `spec-r2` snapshot (`diff -ru` prints nothing), so spec.1, spec.2 and spec.3 of this lane reviewed the same text. The operational lens returned empty at r1 and r2 as well, on cache keys `operational-r1-09a51355eaa8.json` and `operational-r2-09a51355eaa8.json`, and those two were real reviews rather than replays (different hash slots from the earlier lanes) — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/operational-r2-09a51355eaa8.json

FACT: the two tier-0 files 0076 landed cannot redden on S1. `adapter_barrier_doc_comment_scope_test.go` reads a Go doc comment (`barrierDocFileRel = "pkg/adapter/coordination.go"`, `:28`) and `adapter_proto_generation_scope_test.go` reads proto comment blocks (`operationalFenceSentence`, `:130-131`, twelve carrier sites at `:137`); neither names `spec/04_system-components.md`. The only tier-0 file besides the retiring gate that names that path is `adapter_proto_event_taxonomy_test.go`, whose subject is §4.7.3's event taxonomy in proto comments (`:34`, `:36`), not §4.1. S1's disclosed red window is therefore the only one — EVIDENCE: tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:28, tests/tier0_static/adapter_proto_generation_scope_test.go:130, tests/tier0_static/adapter_proto_event_taxonomy_test.go:34

FACT: the reclassified fence has no operator-facing consequence to keep consistent. `docs/runbooks/` contains no occurrence of "fence" or "coordinator_hold" at all; the only alert anywhere near the handoff is `lenny_coordinator_handoff_duration_seconds` (`pkg/alerting/rules/rules.go:1583`), a gateway-side latency alert, and `docs/reference/metrics.md:309` carries the hold gauge with no scope word — EVIDENCE: pkg/alerting/rules/rules.go:1583, docs/reference/metrics.md:309

FACT: every `session-scoped`/`pod-scoped` site in `docs/` other than `docs/reference/adapter-contract.md:81` is about the §28.5.3 JSONL frame set (`docs/reference/metrics.md:180`, `docs/runtime-author-guide/*`, `spec/15:1593`, `spec/16:189`), which is a different population from §4.1's gRPC request messages. A lens that greps for the class words will hit those first and must not read them as restatements of the retiring table — EVIDENCE: docs/reference/metrics.md:180, spec/16_observability.md:189

CORRECTS [Standing context, Settled, "Proto anchors, current and verified"]: the parenthetical "SPEC-1 cites `:458`, one line off, below the bar" is still wrong and still in the file. `schemas/lenny-adapter.proto:456` is `message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, `:458` is `SessionId session_id = 2;`. SPEC-1's `:458` is exact. This is the second round the correction has been written; compaction should land it in the Settled bullet rather than leave it in the ledger.

USEFUL [Standing context, Traps]: the trap list is why this pass cost minutes instead of an hour. The entries on the pod-wide-hold tension, on `pkg/adapter/oplock.go:77`, and on `AdapterEventsRequest` each name a candidate the operational lens reaches for and the ground it dies on.


### [spec.3.review-performance.1]

DECISION: empty findings under the performance / scalability / failure-mode lens — BECAUSE the staged spec edit is a single classification-text replacement in `spec/04_system-components.md` §4.1 that creates no runtime write, no new watch, no new Redis or Postgres key, and no serialization point; the only message whose class moves is `CoordinatorFenceRequest`, whose per-session runtime behavior 0076 already landed, so no write rate, connection-pool draw, informer cache, or reconcile queue changes at any tier — ALTERNATIVES: I considered filing the coordinator-handoff fence amplification (N fences for an N-slot pod), and rejected it because `spec/10_gateway-internals.md:38-41` and `:60` already read fully per-session post-0076 and the hold at `:57` still exits on any one accepted fence, so the staged text neither creates nor worsens it.

FACT: the derived classification is a strict superset of the retired table's session class, re-derived independently today with a brace-counting parse of the two service blocks: 31 RPC request types, 25 declaring a top-level `session_id`, the 6 without being `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. No high-rate message (`SendMessageRequest`, `AttachRequest`, `InterruptRequest`) changes class, so the `spec/05_runtime-registry-and-pool-model.md:515` empty-identifier refusal is added on the fence path alone. — EVIDENCE: schemas/lenny-adapter.proto, spec/05_runtime-registry-and-pool-model.md:515

FACT: `spec/04_system-components.md:149` is the only inbound reference to the retired subsection anywhere outside `proposals/`; no `spec/`, `docs/`, `schemas/`, or `charts/` capacity, sizing, or failure-mode section reads the §4.1 table, so retiring it strands no reliability statement. §10.1.2's fence retry budget, §10.1.3's dual-store window, and §10.1.4's hold timeout are all stated in `spec/10` and are untouched. — EVIDENCE: spec/10_gateway-internals.md:38-41, :50, :57-60

USEFUL [Standing context / Traps]: the "do not re-file the session-scoped fence exiting a pod-wide hold" trap and the "equal-generation re-fence is 0080 §1.16" entry each pointed straight at the two reliability questions this lens naturally reaches, and saved a full derivation on both.

FACT: the checkpoint envelope's session-scoped classification carries no per-frame cost. `pkg/adapter/checkpoint.go:74-93` refuses an empty or unsafe address on the opening frame alone, before the op lock is taken, so an unaddressed stream never queues behind a real session's checkpoint. The staged envelope clause ("every frame that follows continues a stream that is already addressed") matches that, and no continuation frame is re-resolved. — EVIDENCE: pkg/adapter/checkpoint.go:73-93


### [spec.3.review-reliability.1]

DECISION: returned an EMPTY findings list on the staged spec edits, the fourth consecutive empty pass for
this lens on this proposal — BECAUSE the seven non-log proposal files are byte-identical to the `spec-r2`
snapshot (`diff -ru` prints nothing), so there is no fix-stage text to scrutinise, and I re-derived the one
load-bearing reliability property from scratch rather than trusting the log: the derived session class is a
strict superset of the retired table's, so the reclassification can only ADD a refusal and never remove one
— ALTERNATIVES: re-filing the session-scoped fence exiting a pod-wide hold (Traps entry, declined by four
lenses, settled by 0076's OD3, §6 non-goal); re-filing the coordfence same-generation retry and the missing
1-second backoff (both pre-existing shipped-tree defects, already in the Open list and in summary.md's
unstaged-defects bullet with 0080 §1.16 named); re-filing `spec/10_gateway-internals.md:40`'s pod-wide gap
detection as a 0076 residue (pre-existing, not made wrong by these edits, remedy outside the staged spec
edits).

FACT: the class delta re-derived today by an independent brace-counting parse of `schemas/lenny-adapter.proto`
against the 32 rows of `spec/04_system-components.md:153-186`. Every row the table marks `session` declares a
top-level `SessionId session_id` EXCEPT `CheckpointRequest`, which the staged envelope clause carries; the only
row that moves is `CoordinatorFenceRequest` pod→session. No message moves session→pod, so no message loses
`spec/05_runtime-registry-and-pool-model.md:515`'s empty-identifier `InvalidArgument` refusal. That monotonicity
is the entire reliability argument for SPEC-1 and it is worth stating once so no future lens re-derives it.
— EVIDENCE: spec/04_system-components.md:175, schemas/lenny-adapter.proto:1455-1456

FACT: nothing in `spec/` outside §4.1 keys a recovery, retry, or fail-closed behaviour on the gRPC
request-message classification, re-swept today over every `session-scoped`/`pod-scoped` site in `spec/`. The
hits are §4.7.1's two per-message sentences, §10.1.4's hold/gauge sentence, §12.2/§12.5 storage prefixes,
§13/§15's token vocabulary, §16:189's JSONL Attach-frame metric, §28.5.3's frame rules, and §29's route
matrix. None reads the retired table, and `grep -rn "request-message-scope\|Request Message Scope"` over
spec/docs/schemas/charts/tests returns only the heading itself. Retiring the table strands no recovery
statement and owes no anchor redirect. — EVIDENCE: spec/28_communication-channels.md:838-844,
spec/16_observability.md:189

FACT: the fence handler's refusal ladder, re-read today, is empty-id `InvalidArgument` (`:108-111`), then
`boundSlotState` (`:116`), then a positive-generation `InvalidArgument` (`:121-123`), then the stale
`FailedPrecondition` under `st.coord.mu` (`:126-134`). The generation state is per bound session on the slot
entry (`pkg/adapter/slot.go:59`, documented `pkg/adapter/coordination.go:17-38`), and `Server` carries only
`hold holdState` at `pkg/adapter/server.go:302`. Every §1.2 citation is exact. — EVIDENCE:
pkg/adapter/coordination.go:107-134, pkg/adapter/server.go:302

FACT: §4's `pkg/adapter/checkpoint.go:74-84` citation is exact to the line. `:60-63` is the
"stream must open with a CheckpointStart" `InvalidArgument`, `:74-80` the `// spec: §4.1` comment, `:81-84`
the empty-identifier refusal, `:85-87` the `ValidateSlotID` refusal, all before any root is resolved and
before the op lock. So the envelope clause's "it opens the stream" is enforced in code and a continuation
frame cannot arrive on an unaddressed stream after a reconnect. — EVIDENCE: pkg/adapter/checkpoint.go:61,81

USEFUL [Standing context / Traps]: "Naive proto parsers derail on this file" — writing the census parse with
per-line brace counting and same-line-close handling from the first attempt produced the settled 31/25/6
answer immediately, and the 88-message count confirmed the parse was not swallowing bodies.

USEFUL [Standing context / Traps]: "Long lines hide the sentences citations point at" — `spec/05:515`,
`spec/10:57` and `:60` are each one very long line; piping through `tr '.' '\n'` is what recovered all three
and stopped a false-citation filing.

USEFUL [Standing context / Traps]: the entries declining the hold-state-exit finding and recording the
equal-generation re-fence as unstaged-on-purpose covered the only two reliability leads in the staged text.


### [spec.3.review-security.1]

DECISION: EMPTY findings list under the security lens on the staged spec edits, the seventh consecutive
empty pass for this lens on this proposal — BECAUSE the whole proposal directory is byte-identical to the
`spec-r2` snapshot (`diff -rq` reports zero differing files, review log included), so the text is exactly
what this lens cleared at `spec.1` and `spec.2`, and I re-derived both lens checks mechanically from the
tree rather than replaying the standing context. Check (1), regression of an established control: the only
control the staging retires is the tier-0 table-reconciliation gate, retired openly, with the replacement
gate's four refusal clauses and its blind spot written into normative spec text and both residuals priced in
summary OD1. Check (2), trust boundary and durability of a security bound: the staged §4.1 block declares no
residual-state limit, no quota or reuse counter, no Redis-backed role, and no value sourced from a pod
self-report, so the proxy-mode bar has nothing to bite on — ALTERNATIVES rejected: the session-scoped fence
exiting a pod-wide hold (0076 OD3 territory and the hold allowlist is keyed on gRPC method names, standing
trap 74); the nested-address hole (paragraph 3 reads onto it, trap 72); the loss of the table's
Service/Direction reconciliation (OD1 names the "wrong service" refusal among those that lose their subject);
the `oneof` of frames versus any `oneof` predicate gap (killed as wording in an earlier pass).

FACT: I re-derived the table-versus-derivation delta with my own brace-counting parser over
`schemas/lenny-adapter.proto` and the 32 extracted table rows, and got 26 session rows and 6 pod rows, with
exactly one session row not declaring a top-level `SessionId session_id` (`CheckpointRequest`, the envelope,
carried by paragraph 2) and exactly one pod row declaring it (`CoordinatorFenceRequest`). No message moves
session to pod. That one-way delta is why the retirement is fail-closed-safe: the `spec/05:515`
empty-identifier refusal hangs off the session class, so it can only be added — EVIDENCE:
spec/04_system-components.md:153-186; spec/05_runtime-registry-and-pool-model.md:515.

FACT: the name/type biconditional the replacement gate enforces holds with zero violations in
`schemas/lenny-adapter.proto`. Every non-comment `session_id` occurrence is `SessionId session_id`, and
`grep -nE '^\s*(repeated\s+)?SessionId\s+\w+\s*='` minus that spelling returns nothing, so the gate is green
on the shipped proto on day one and the derivation's premise holds — EVIDENCE: schemas/lenny-adapter.proto.

FACT: the fail-closed ordering the reclassification imports is the handler's first branch and the hold
allowlist cannot move with the scope class. `CoordinatorFence` returns `InvalidArgument` on an empty session
id at `pkg/adapter/coordination.go:108-111`, before `boundSlotState` at `:116` and before the generation
checks at `:121-125`; `coordinatorHoldAllowedMethods` is a `map[string]bool` of gRPC full-method strings
consulted in both interceptors — EVIDENCE: pkg/adapter/coordination.go:107-134;
pkg/adapter/holdstate.go:52-58, :336, :349.

USEFUL [spec.2.review-security.1]: its two load-bearing facts (method-name-keyed hold allowlist, and the
strict-superset property of the derived session class) are still the whole answer, and both survived
independent re-derivation. Its cache WATCHOUT is also still exact: my key was `09a51355eaa8`, identical to
rounds 1, 2, and `non-spec.5`, yet `security-r3-09a51355eaa8.json` did not exist, so the lens re-ran on text
it has cleared six times.

OPEN: nothing new from this lens. Seven consecutive empty passes, three of them on byte-identical text.
Re-run it only if a fix pass edits the third staged paragraph (the gate's clauses and its blind spot), OD1's
residual statement, or any sentence tying a scope class to a refusal.


### [spec.2.review-applicability.1]

DECISION: returned an empty findings list for the spec-lane applicability-and-sequencing lens — BECAUSE I simulated S1 -> S2 -> S3 against the tree at HEAD (a3482cc95) and every anchor in `spec-changes.md` resolves exactly, the SPEC-1 edit is a determinate three-range deletion plus three quoted paragraphs under a heading that survives, no created artifact is referenced before the sub-step that creates it, and the only red-gate window (tier 0 between S1 and S2) is disclosed on S1's own line — ALTERNATIVES: filing the dangling "either service" antecedent (paragraph 1 quantifies over "a request message either service declares" while the Service column that named `Adapter` and `GatewayControl` is deleted with the table; rejected because both services are defined in spec/04 §4.7.1 and no applied text becomes wrong, so it is wording); filing the §7/§8 numbering gap left by the open-decisions pass (rejected as bookkeeping, and nothing outside the review log still cites `spec-changes.md` §7).

FACT: `spec-changes.md` lost its whole `## 7. Open decisions for review` section between baseline `470cd4e34` and HEAD, so the file now runs §2, §3, §4, §5, §6, §9, §10. I grepped every landing file for `§7`/`§8`: no proposal file outside `review-log.md` references either, and `non-spec-changes.md:37` (`§4 of the staged spec changes`) and `:48` (`§9 of the staged spec changes`) both still resolve. The removal stranded no cross-reference.
EVIDENCE: git diff 470cd4e34 HEAD -- proposals/0075_.../0075_....spec-changes.md

FACT: no test in `tests/tier11_docs/` or `tests/tier0_static/` reads spec/04 §4.1 except the retiring gate. `grep -rn "04_system-components" tests/tier11_docs/*.go tests/tier0_static/*.go` returns only §4.4, §4.6.1, §4.6.3, §4.7, §4.8 and `messageScopeSpecPath`. `static_tier_check_inventory_test.go` inventories the `staticCheck` table in `cmd/lenny-test/cmd_run.go` against TESTING.md §12.0, not individual Go test function names, so replacing a gate function inside `tests/tier0_static` owes it no edit and §9's file list stays complete.
EVIDENCE: tests/tier11_docs/static_tier_check_inventory_test.go:34-46

FACT: `spec/` contains zero occurrences of "tier-0" or "tier 0" today (`grep -rln "tier-0\|tier 0" spec/` is empty), so SPEC-1's constraint paragraph really is the first mention of a test tier in the specification. The standing-context entry that calls this "judged below the bar but never decided" is accurate, and the precedent it cites should be re-checked by whoever decides it, because my grep found no existing tier mention to be a precedent.
EVIDENCE: spec/04_system-components.md:149-190; grep over spec/

FACT: after SPEC-1 the only spec sentence that still classifies one request message pod-scoped is `spec/04_system-components.md:726` (`The request is pod-scoped.`, last sentence of the very long `ReportPodScrub` row). The other `pod-scoped` sites are `spec/10:60` (hold gauge), `spec/04:872` (rotation ceiling), `spec/12:202` (Redis prefixes), none of which classifies a request message. Re-derived rather than inherited.
EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60

USEFUL [Traps: cache keys omit summary.md and carry no lane component]: the cache slot `applicability-r2-2d25f5f323c0.json` held a NON-SPEC-lane answer whose own coverage string records that it declined a SPEC-lane hit at the same slot. Two lanes have now round-tripped through one key. A lens that takes the hit ships another lane's scope filter as its own.
EVIDENCE: scratchpad/cp-cache/0075_.../applicability-r2-2d25f5f323c0.json

USEFUL [Traps: orchestrator briefs carry stale content]: the r2 brief still gives the pre-refresh proto anchors (`:589 :1166 :1447 :1448`, each off by seven), still says §1.2 asserts the pod-wide ground holds (it reads post-0076 and cites `server.go:302` as `hold holdState`), and still calls the retire-or-withdraw question "§7 question 1" when §7 no longer exists. Four false findings are available to anyone who reviews the brief instead of the document.
EVIDENCE: proposals/0075_.../0075_....problem-statement.md:46-70

UNVERIFIED: still nobody has run tier 0 or tier 3 on this proposal. Every disposition in S1's red-window line, mine included, is derived from reading `parseMessageScopeTable` rather than from an execution.


### [spec.2.review-fresh.2]

Fresh holistic lens over the staged spec edits, round 2. Returned an empty findings list after
re-verifying every citation in `spec-changes.md` against the tree at `a3482cc95`. Nothing new met
the bar.

CORRECTS [Standing context / Settled, "Proto anchors, current and verified"]: that bullet says
"`ReportSessionScrubRequest` `:456` with its address at `:457` (SPEC-1 cites `:458`, one line off,
below the bar)". The parenthetical is wrong. `schemas/lenny-adapter.proto:456` is
`message ReportSessionScrubRequest {`, `:457` is `string pod_id = 1;`, and `:458` is
`SessionId session_id = 2;`. SPEC-1's `:458` citation is exactly right. A future fixer must not
"correct" it to `:457`. EVIDENCE: schemas/lenny-adapter.proto:456-458.

FACT: no successor pointer and no relocated-material pointer is owed by retiring the §4.1 table.
Both gates are hand-named lists scoped to material that moved into §28.5:
`tests/tier11_docs/successor_pointer_test.go:52` (`reducedSections`) and
`tests/tier11_docs/relocated_material_pointer_test.go:50` (`relocatedStatements`, each entry an
anchoring phrase plus the CH- identifiers it must name). SPEC-1 moves nothing into §28, so neither
fires. EVIDENCE: tests/tier11_docs/relocated_material_pointer_test.go:25-37, :50-60.

FACT: `spec/04_system-components.md:175` and `:188` are the ONLY sites in `spec/` that classify
`CoordinatorFenceRequest`'s scope; a full `grep -n CoordinatorFence spec/*.md` returns 22 hits and
no other one states a scope. Both are inside the block SPEC-1 replaces, so the reclassification
needs no second spec edit. EVIDENCE: spec/04_system-components.md:175, :188.

USEFUL [Standing context / Traps, "Cache keys omit `summary.md` and `review-log.md`"]: the cache
slot `fresh-r2-2d25f5f323c0.json` printed a populated JSON whose own `coverage` names the
"Non-spec-recheck lane". This lane reviews the staged spec edits. Returning it would have been a
lane swap, not a resumed run. Declined it and did the review. The trap is live and cost me the
decision; the next fresh-lens agent will hit the same slot.

WATCHOUT: I overwrote that slot with this lane's answer at the end of the run, as the brief
instructs. The slot now holds the spec-lane answer; a non-spec-lane agent reading it next hits the
mirror image of the same collision. EVIDENCE:
scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/fresh-r2-2d25f5f323c0.json

DECISION: did not file the D2 "an addressing convention that nothing checks today" imprecision
(`spec-changes.md:38`), even though this loop CAN edit `spec-changes.md` and the standing context's
last `DEFERRED [0075_....spec-changes.md]` entry records the correction unapplied. BECAUSE it is a
decision rationale rather than staged spec text: `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1`
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`) checks one
direction over a hand-maintained subset, which weakens D2's justification but leaves the applied
spec and the replacement gate's design intact. ALTERNATIVES: filing it under (c); rejected because
(c) requires the contradiction to make the applied edits inconsistent, and it does not. The
between-loops pass still owns the DEFERRED entry.

FACT: re-derived the whole census independently with a brace-tracking Python parse rather than
trusting the log. 31 RPC request types; exactly 26 messages declare `SessionId session_id`; the
set difference against the request types is `{CheckpointStart}` one way and
`{AdapterEventsRequest, CheckpointRequest, DemoteSDKRequest, GetObservedIntegrationLevelRequest,
NegotiateVersionRequest, ReportPodScrubRequest}` the other. The biconditional holds in both
directions with zero exceptions across the whole file including `oneof` arms: no field named
`session_id` carries another type, no field of type `SessionId` carries another name. The §4.1
table has 32 rows, 26 session (25 plain plus the envelope row) and 6 pod, which is exactly 27/5
after the fence moves. The replacement gate is green on the shipped proto on day one.
EVIDENCE: schemas/lenny-adapter.proto:1217, :1456, :458; spec/04_system-components.md:153-186.

USEFUL [Standing context / Traps, all thirty]: the traps list is what kept this pass to
verification instead of re-derivation. Four candidates I formed independently
(the "only way a request addresses a session" tension, the nested-address hole, the §4.7.1
"declared per message" tension, and the positional self-references in the staged block) were each
already recorded as declined or open. Do not shrink that section.


### [reconcile.3]

Deliverable-index and checklist reconciliation against the converged spec staging, and the discharge of the
standing `## Deferred` entries.

FACT: `## Deliverable index` needed no rebuild. `spec-changes.md` stages SPEC-1 and `non-spec-changes.md`
stages TEST-1 and TEST-2, and no other deliverable identifier appears in either file (the `CODE-1` and
`SPEC-7` occurrences in `spec-changes.md` name 0076's and 0073's deliverables). The index already carried
those three, each once, each with the files it lands in and one sentence, so the rebuild reproduced it
unchanged. — EVIDENCE: 0075_....spec-changes.md, 0075_....non-spec-changes.md, 0075_....summary.md

FACT: the checklist already held the target arrangement. S1 is the one spec-lane step and leads (SPEC-1),
S2 and S3 follow in the test lane (TEST-1, TEST-2), every staged deliverable appears in exactly one step,
no step names one that does not exist, no step mixes lanes, and no step interleaves ahead of a remaining
spec step. Both non-spec steps already name S1 under `Depends on:`, which is the spec step staging the rule
their gates and sets implement, so the dependency reconciliation changed nothing. Every box stays
unchecked. — EVIDENCE: 0075_....implementation-checklist.md

CORRECTS [DEFERRED [0075_....non-spec-changes.md], the claim that of the five tier-0 cases ranging over
`slotAddressCaseFiles` "each resolves an entry through `repoFileLines`"]: applied to TEST-1's paragraph on
the kept gate path. It now reads that four of the five resolve each entry through `repoFileLines`
(`:699-706`), which fails the case on a file it cannot read, and that the fifth (`:1110`) reads no file and
checks inventory membership alone. Re-derived before applying rather than taken from the entry:
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` builds a set from `slotAddressCaseFiles` and
compares it against `addressRuleGateFile`, `addressRuleCases`, and `derivedInventoryCaseFiles(t)` without
reading any file it names, while the other four reach `repoFileLines` directly or through
`citedSectionsInFile` (`:797-805`) and `citedSectionsPerCase` (`:776-779`). The paragraph's conclusion, that
keeping the path leaves the inventory correct and confines the change to the staged files-touched list,
stands on the four either way and is unedited. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:336, :699-706, :776-805, :1108-1131

OPEN [docs/reference/adapter-contract.md]: carried forward from the standing `DEFERRED` entry. Line 69
calls `CoordinatorFence` the "precondition for any subsequent operational RPC", which reads as a pod-wide
gate, while after 0076 the fence and the generation it announces are per bound session. The remedy is an
edit to that page and to its source at `spec/04_system-components.md:713`, neither of which this pass may
edit, and it belongs to whoever takes the 0076 residue that 0080's inventory registers.

OPEN [spec/04_system-components.md]: carried forward from the standing `DEFERRED` entry. The
`CoordinatorFence` row at `:712` still reads "Announce new `coordination_generation` to the pod on
coordinator handoff", which 0076 falsified by recording the generation on the session's slot entry
(`pkg/adapter/slot.go:59`) and which `spec/28_communication-channels.md:314-317` contradicts. The remedy is
a §4.7 row edit outside SPEC-1's bounded block and outside the files this pass may edit. The summary
records the imprecision under `## Defects in the shipped tree that this proposal does not stage`; the edit
itself remains unowned.

OPEN [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]: carried forward
from the standing `DEFERRED` entry. §2's bullet at `:453-454` pairs the declared message-scope table with
the §4.1 `ShutdownRequest` classification limit and records both as taken by 0075. Only the table half is
taken. The remedy is a split of that bullet in 0080's own file, which this pass may not edit; the summary's
0080 impacts row states it as what 0080 must do when it converges.

OPEN [0075_....status.md]: carried forward from the standing `DEFERRED` entry. The status file's
`**Date:**` line points at a "§11" that exists only inside this review log, so the pointer resolves across
files. The remedy is to name the review log or drop the reference, in the status file, which is not one of
the four files this pass may edit.

OPEN [docs/api/internal.md]: carried forward from the standing `DEFERRED` entry and the second instance
filed against the same page. `:209-215` publishes a `CheckpointRequest` excerpt declaring `string
session_id = 1`, `:272-274` publishes `DemoteSDKRequest` with a `string session_id = 1`, and the page
documents `UploadFiles` and `StopSession` RPCs that neither service declares. The page is false about the
protocol definition before anything staged here applies; after SPEC-1 the excerpts illustrate a spelling
the replacement gate refuses. The remedy is the whole page, which this pass may not edit, and the summary
records it under `## Defects in the shipped tree that this proposal does not stage`.

OPEN [0075_....non-spec-changes.md]: carried forward from the standing `DEFERRED` entry, and not closable
here. TEST-1 stages the replacement gate's cases as re-registered "under the same sections", which includes
§28.5.3 (`tests/spec-map.json:5670`), and whether the replacement cases can honestly annotate 28.5.3 is
unanswered. The edit to this file follows the answer rather than preceding it: "follow the gate" leaves
TEST-1 as staged, and "drop it" adds the removal of the `:5670` entry to TEST-1's step. The question is
carried to the reviewer as OD4 in the summary, so the answer is asked for where the reviewer reads.

OPEN [0075_....spec-changes.md]: carried forward from the standing `DEFERRED` entry, restated by three
later rounds and applied by none. D2's clause "an addressing convention that nothing checks today"
overstates the tree, because `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1`
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`) already asserts the
name-implies-type half over a hand-maintained subset. What is true instead: no gate checks the convention
in both directions over every message the protocol declares. The remedy is a wording correction in
`spec-changes.md`, which is not one of the four files this pass may edit.

FACT: no unclosed `OPEN` in the standing context is a decision the summary does not already carry. The
§28.5.3 registration question is OD4 and the unheld tier-3 session set is OD5, both with the ground the log
gives and both stating that the loop derived no recommendation. The kept-path question and the question of
whether `spec/` may name a test tier were resolved as OD3 and OD6 and withdrawn, and the envelope-arm
question was closed by `spec.2.review-mechanism.1`, so those standing bullets are spent rather than owed to
the summary. What remains on the list is unanswered verification questions and defect records whose
remedies land outside this proposal, and the summary already carries the ones this proposal is the place to
record.
— EVIDENCE: 0075_....summary.md `## Open decisions for human to make`

## Retired

### Resolved in adversarial review

#### Pass 1 (2026-09-07, automated)

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

### 11. What the 2026-09-06 rewrites changed

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
