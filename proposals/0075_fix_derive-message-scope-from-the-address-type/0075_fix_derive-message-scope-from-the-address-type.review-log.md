# Review log: Derive message scope from the address type

## Standing context

## Ledger


### [spec.1.fix-G1.1]

DECISION: The staged §4.1 block is written as a classification paragraph, an envelope paragraph, and a constraint paragraph, all carried in D1 and pointed at by SPEC-1 — BECAUSE the envelope clause has to PRODUCE the opening frame's classification rather than consume one, and the constraint paragraph is where the gate's reach is recorded now that `spec/` states no gate limit — ALTERNATIVES: widening clause 1's population to "a frame that opens a stream" (undefined in the protocol definition, and it drags `CheckpointGrant` and `CheckpointAbort` into a pod-scoped class that is false of them); a third clause naming `CheckpointRequest` and `CheckpointStart` (a smaller table the gate would have to reconcile); requiring an envelope's frames to agree on a scope (false on this protocol definition today, so the gate would fail on its first run).
DECISION: The predicate is pinned on both halves of one spelling, "a top-level `session_id` field of type `SessionId`" — BECAUSE the name alone leaves `string session_id` deriving session-scoped with no typed address and the type alone leaves `string session_id` deriving pod-scoped and failing open, and the class is what the §4.2 value rule's refusal hangs off — ALTERNATIVES: the field name alone (matches the §1.1 measurement but leaves the type free); the type alone (the fail-open case the finding named).
DECISION: D2's clause "no pod-scoped message declares a field of the address type" is dropped and replaced by a gate over the addressing convention — BECAUSE with the table retired the only source of "pod-scoped" is the derivation, which defines it as the absence of the field the clause looks for, so the clause has no input and can never fail.
FACT: The two G1 measurements hold on the tree today. A parse of the two service blocks returns 31 RPC request types, 25 declaring a top-level `SessionId session_id` and 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). `CheckpointRequest` and `CheckpointResponse` are the only messages declaring a `oneof`, and only the first is a request type. The name `session_id` and the type `SessionId` co-occur exactly in both directions across the whole file — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1173 (`CheckpointRequest`), :1217 (`CheckpointStart`'s `SessionId session_id = 7`), :1455-1456 (`CoordinatorFenceRequest`).
WATCHOUT: Proto line numbers in this proposal had drifted by roughly 7 to 8 lines. `CheckpointRequest` is at :1173 (the proposal said :1166) and `CoordinatorFenceRequest` at :1455 with its field at :1456 (the proposal, and the round's orchestrator note, said :1447/:1448). Both are corrected in the problem statement now. Re-derive every proto anchor with grep rather than trusting a cited number — EVIDENCE: schemas/lenny-adapter.proto:1173, :1455.
FACT: `spec/` states no limit for the retired table-reconciliation gate anywhere. 0073 records that limit in the gate file's own header comment, so a staged instruction to "retire 0073's recorded limit in `spec/`" has no target — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:17-27.
FACT: `spec/05_runtime-registry-and-pool-model.md:515` is one very long line; the sentence rejecting a session-scoped request with an empty identifier is far into it, so a `sed -n 515p | cut` looks like the citation is wrong when it is right. Grep for the sentence instead — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515.
FACT: The tier-0 gates share one proto parse on purpose, and the file says so. `protoFields` has exactly one other caller, whose two lookup sites are `fields[msg][field]` and `fields[m[2]][m[1]]` — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:10-15, :36; tests/tier0_static/claim_register_proto_agreement_test.go:64, :72, :82.
FACT: The tier-3 citations in §1.3 all still resolve: the comment at :40-43, the three by-name arms iterating `sessionScopedMessages` at :81, :102, and :130, and the fourth arm at :150-158 naming no request — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40-43, :81, :102, :130, :150-158.
DEFERRED [/home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md]: TEST-1 describes the replacement as "the rule gate of D2" and names only `declaredScope` (`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`) as losing its subject. What is true after this round: the replacement gate reads `schemas/lenny-adapter.proto` alone and checks the addressing convention in both directions plus the envelope's single addressing frame; `parseMessageScopeTable` (:54) loses its subject along with `declaredScope`; the shared parse must carry each field's type and its `oneof` membership rather than a boolean (`tests/tier0_static/adapter_proto_parse_test.go:36`), which moves its other caller's two lookup sites (`tests/tier0_static/claim_register_proto_agreement_test.go:72`, `:82`); and §8's tier-0 negative cases become a `session_id` field of the wrong type, a `SessionId` field under another name, an envelope declaring a top-level address, and an envelope whose frames declare zero or two addresses. The G1 design directed this edit; the fixer's hard constraint bars authoring staged test content, so the non-spec lane owns it.
DEFERRED [/home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md]: S1 says SPEC-1 replaces the table and rewrites or removes "the three sentences that ground the declared classification". What is true instead: SPEC-1 replaces the whole `#### Request Message Scope` block, which is the introducing paragraph at `spec/04_system-components.md:151`, the table at `:153-186`, and the grounding paragraph at `:188`, leaving the `ShutdownRequest` paragraph at `:190` standing. No step is added, removed, or resequenced.
WATCHOUT: §7 question 2 (whether the stream-envelope predicate is mechanically evaluable) is now answered in §4 against the protocol definition and was removed, so the old question 3 about the retired-field-number column is now question 2. Anything citing "§7's second question" for the envelope predicate is stale; the summary's open-decisions section was updated in the same edit — EVIDENCE: 0075...spec-changes.md §4, §7.
OPEN: `spec-changes.md` §9 "Files touched on application" lists both "The tier-0 gate file 0073's §8 introduces" and `tests/tier0_static/adapter_proto_message_scope_test.go`, which are the same file; the summary's TEST-1 index row repeats the same pair. G1's design assigns every file enumeration to G2, so it is left as found.
OPEN: The problem statement's §1.2 paragraph beginning "In the shipped tree that ground holds" still describes `s.coord` on `pkg/adapter/server.go:302` as a pod-wide `coordinationState`. 0076 deleted it: `Server` carries no `coord` field, and `lastFenced` and `initialized` now sit on the slot registry entry the fence resolves. The same claim is restated in the summary's "Defects in the shipped tree that this proposal does not stage" section. It is outside G1's findings, so no edit was made; whoever owns that finding should correct both together — EVIDENCE: pkg/adapter/coordination.go:27-38 (`coordinationState` with `lastFenced` and `initialized`), :107 (`CoordinatorFence` resolving the slot entry); `grep -n "coord " pkg/adapter/server.go` returns no field.


### [spec.1.fix-G2.1]

FACT: `tests/spec-map.json` credits the retiring tier-0 gate by `path::Fn`, so retiring the function names without editing the register turns tier 0 red. `validate-maps` runs inside the tier-0 static set and `validateSpecMapTestFuncs` reports every `path::Fn` whose file declares no such function. — EVIDENCE: tests/spec-map.json:156, :169 (section 4.1), :5670 (section 28.5.3); cmd/lenny-test/cmd_run.go:761; cmd/lenny-test/cmd_validate.go:941

FACT: `tests/tier0_static/adapter_proto_message_scope_test.go`, `adapter_proto_parse_test.go`, and `claim_register_proto_agreement_test.go` are all named by path in `slotAddressCaseFiles`, so `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` needs no edit as long as the replacement gate keeps its path, and every case in those files must be credited under each section its own `// spec:` annotation names. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336-342, :970-988

FACT: `protoFields` returns field NAMES only (`map[string]map[string]bool`) and knows nothing of a field's type or its `oneof` membership, which is exactly what the replacement rule gate has to read. Its only other caller is `claim_register_proto_agreement_test.go:64`, whose lookups at :72, :82, and :93 read that map. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:33-62

FACT: the tier-0 gate file and the "existing scope test that accepts either class word" are ONE file. 0073's gate commit fb2af5f9c added `adapter_proto_message_scope_test.go` and nothing else for the gate; `declaredScope` at :75-81 lives inside it. Two of this proposal's enumerations split it in two as though they were separate files. — EVIDENCE: git show --stat fb2af5f9c; tests/tier0_static/adapter_proto_message_scope_test.go:75-81

DECISION: §9, problem-statement §1.4, the summary's "What changes" list, and the summary's TEST-1 deliverable row now all name one target set for TEST-1: the gate file, the shared proto parse, the parse's other caller, and `tests/spec-map.json`. The register mechanics (which functions are credited under which sections, and why the re-registration lands inside TEST-1's own change) are stated once, in the non-spec-changes TEST-1 block, so the other three lists carry file names alone. — BECAUSE four lists restating one rule is four places to drift. — ALTERNATIVES: leaving the register edit unlisted as an implementation detail (rejected: §9 is the register of targets and §8 names tier 0 as reached, so the omission is the defect itself); a separate TEST-3 deliverable for the re-registration (rejected: between retiring the function names and re-registering them tier 0 is red, so it cannot land in its own commit).

WATCHOUT: the G2 design named `adapter_proto_parse_test.go` and `claim_register_proto_agreement_test.go` as part of the canonical set on the premise that group G1 had staged a `protoFields` extension carrying each field's type and `oneof` membership. G1 did not: it wrote an IMPLEMENTOR'S CHOICE in spec-changes §4 leaving HOW the gate reads types and `oneof` membership open, constrained only to use the parse the tier-0 gates already share. The enumerations therefore name the parse unconditionally (it cannot supply what the gate reads today, under any answer) and name the other caller with the condition stated (it moves only if the existing signature changes, which an implementor can avoid by adding a function beside `protoFields`). — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:98-101

WATCHOUT: do not add `tests/tier0_static/spec_map_slot_address_registration_test.go` to any files-touched list. Its inventory names the three tier-0 files by path, so it needs no edit while the replacement gate keeps `adapter_proto_message_scope_test.go` as its path. Adding it stages an edit nothing requires. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336

FACT: TEST-2 carries no register obligation. `session_address_wire_test.go` is credited in `tests/spec-map.json` as a whole-file path everywhere it appears, and TEST-2 adds a map entry rather than a test function, so nothing dangles. — EVIDENCE: tests/spec-map.json:172, :600, :1110, :1391, :3678

DEFERRED [proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.implementation-checklist.md]: step S2 describes TEST-1 as replacing the tier-0 table-reconciliation gate and names tier 0 alone. That is now incomplete rather than wrong: TEST-1's target set is the gate file, `tests/tier0_static/adapter_proto_parse_test.go`, `tests/tier0_static/claim_register_proto_agreement_test.go`, and `tests/spec-map.json`, and the spec-map re-registration lands inside S2 rather than as its own step, because tier 0 is red between retiring the old function names and registering the new ones.

OPEN: the proposal still does not state whether the replacement gate keeps the path `tests/tier0_static/adapter_proto_message_scope_test.go`. Every enumeration now assumes it does, which is what keeps `slotAddressCaseFiles` out of the blast radius. A later round should either state it in D2 or accept that a rename adds that register to the files touched.


### [spec.1.fix-G3.1]

FACT: `Server` in pkg/adapter/server.go carries no coordination-state field after 0076's CODE-1; a grep for "coord" in that file returns only the `coordinatorHoldTimeout` comments and the `hold holdState` block. The `coordinationState` lives on the per-session slot registry entry. — EVIDENCE: pkg/adapter/server.go:299-302 (`hold holdState`), pkg/adapter/slot.go:59 (`coord coordinationState`), pkg/adapter/coordination.go:17-38 (the type and its `lastFenced` / `initialized` fields, documented as living on the slot entry)

FACT: the fence handler is `func (s *Server) CoordinatorFence` at pkg/adapter/coordination.go:107, and it resolves the session's entry at :116 through `s.boundSlotState(sessionID)`. The line 84 that older revisions of this proposal cited as the handler is inside `BarrierWaiting`, which begins at :81. — EVIDENCE: pkg/adapter/coordination.go:81, :107, :116

FACT: the three §4.1 sites this proposal owns still read as cited: `spec/04_system-components.md:149` opens `#### Request Message Scope`, `:151` is the "declared rather than derived" paragraph, `:175` is the `CoordinatorFenceRequest` pod row, `:188` is the declaring paragraph, `:190` is the `ShutdownRequest` precedent. Both `:175` and `:188` are falsified by the code 0076 landed and no gate catches it. — EVIDENCE: spec/04_system-components.md:149-190

DECISION: rewrote both paragraphs of problem-statement §1.2's tree passage rather than swapping the two stale anchors — BECAUSE the false claim was the tense ("In the shipped tree that ground holds", "After it lands"), and the anchors were only its symptom; a passage carrying two tenses for one fact would have come back as a finding — ALTERNATIVES: an anchor-only refresh (leaves the finding open); deleting the tree paragraphs and resting §1.2 on `spec/04`'s own text plus the OD3 answer (removes the evidence that makes `:175` false as a matter of fact rather than a classification preference); appending a "the tree has since moved" note to the false sentence (the proposal is a Draft and its own text is edited rather than annotated)

DECISION: propagated the corrected timeline to two further sites in spec-changes.md that state it — §7 question 1's closing clause ("false once 0076's CODE-1 is in the tree" became "0076's CODE-1 has landed and both statements are already false") and D6, whose "0076 is further along, rewrites the state §1.2 describes" was falsified by my own §1.2 rewrite, since §1.2 now records the post-CODE-1 tree — BECAUSE the finding's substance is one timeline error restated in several places, and leaving a cross-reference to a section I rewrote pointing at the old account is two findings rather than one — ALTERNATIVES: leaving §7 and D6 to a later round, which the group design allowed for §10 and D3 but which does not survive D6's direct cross-reference to §1.2

WATCHOUT: summary.md:11 still reads "`CoordinatorFenceRequest` is session-scoped once 0076's CODE-1 records the generation on the slot entry". The antecedent holds now, so the sentence is satisfied rather than false, and the G3 grant on summary.md covers only the deliverable index and statements this group's own edits falsified. A round with a wider grant on that file should put it in the past tense so the document states one timeline. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:11

WATCHOUT: spec-changes.md D5 says "This proposal is inert until 0073 is applied." 0073 is Implemented, so the condition is spent and the sentence reads as though it were not. Nothing in G3 touched it, so it was left alone under the no-scope-expansion rule; it is a live edit for a later round. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:64-65

USEFUL [spec.1.fix-G1]: G1 had already refreshed §1.2's proto anchors to `schemas/lenny-adapter.proto:1455` and `:1456`, which I re-verified rather than re-derived, so the only anchors G3 had to move were the two code ones.

CORRECTS [review-log §11, first rewrite, "The central argument"]: that entry records §1.2 as stating "the ground as the specification's rather than the tree's, and states that 0076 removes it". That was true of the revision it describes and is no longer a description of §1.2. §1.2 now states that 0076's CODE-1 has landed, that the ground is gone, and that `spec/04:175` and `:188` are false in the specification today. The log entry is a historical record and was not edited; this line is the correction.



### [spec.1.fix-design-G1.1]

FACT: The field name `session_id` and the field type `SessionId` co-occur exactly across the whole adapter proto. A scan of every top-level message (oneof arms included) returns zero fields named `session_id` whose type is not `SessionId`, and zero fields of type `SessionId` not named `session_id`. So the two candidate predicates in finding 2 are extensionally identical today; picking one changes no classification, it only fixes the failure direction. — EVIDENCE: schemas/lenny-adapter.proto (whole file; `message SessionId` at :595)

FACT: The derivation reproduces the post-OD3 table exactly. Of the 31 RPC request types, 25 declare a top-level `SessionId session_id` and 6 do not (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`). Every one of the 25 is a `session` row in the table, every one of the 5 non-envelope messages is a `pod` row, and `CoordinatorFenceRequest` moves to `session` under 0076's OD3 answer. No row is lost by retiring the table. — EVIDENCE: spec/04_system-components.md:155-186

FACT: `CheckpointRequest` is the only REQUEST message in the proto declaring a `oneof`; the only other message with one is `CheckpointResponse`, which is not on the request path. So the §4 candidate envelope predicate does select `CheckpointRequest` and nothing else, and §7 question 2 is answerable YES from the proto as it stands. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187 (the `oneof msg`)

WATCHOUT: The three frames of `CheckpointRequest` do NOT agree on scope. `CheckpointStart` declares `SessionId session_id = 7`; `CheckpointGrant` and `CheckpointAbort` declare no address at all. So the tempting simplification "an envelope's scope is the scope of its frames, which must agree" is FALSE on this proto and a gate written that way fails on day one. The envelope clause has to key on the ONE frame that declares the address. — EVIDENCE: schemas/lenny-adapter.proto:1193-1218 (`CheckpointStart`), :1222-1240 (`CheckpointGrant`, `CheckpointAbort`)

WATCHOUT: The two clauses of D2's gate as staged cannot fail. Once the table is retired, "pod-scoped" has no source other than the derivation rule itself, and the rule derives pod-scoped FROM the absence of the address field. "No pod-scoped message declares a field of the address type" therefore reduces to "no message that lacks the field has the field", a tautology. Any fix that keeps that clause has kept a gate with no input. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:8-12

DECISION: The gate's subject moves from what the rule STATES to what the rule RESTS ON: (a) a field named `session_id` is of type `SessionId` and a field of type `SessionId` is named `session_id`, on every message the proto declares; (b) a request message carrying a `oneof` of message-typed frames declares no top-level address of its own, and exactly one of its frames declares the address. — BECAUSE the rule's output needs no reconciliation (it is computed from the proto), while its soundness rests on an addressing convention nothing currently checks, which is the fail-open direction finding 2 named. — ALTERNATIVES: keeping "no pod-scoped message declares the address" (tautology, see WATCHOUT above); a gate that reads spec/04 for a named envelope list (reintroduces the smaller table §7 question 2 warns about); a gate that checks the frames of an envelope agree on scope (false today, see WATCHOUT).

DECISION: The envelope clause is rewritten to PRODUCE the classification instead of presupposing one: an envelope declares no address of its own, exactly one of its frames declares the address, that frame is session-scoped and opens the stream, and the envelope takes its scope. — BECAUSE the finding is right that clause 1's population is the RPC request types, which `CheckpointStart` is not a member of, and a clause that derives the envelope's scope from a frame nothing classifies states nothing. — ALTERNATIVES: widening clause 1's population to "request messages plus every frame an envelope carries" (the reviewer's own suggestion) — rejected because it then classifies `CheckpointGrant` and `CheckpointAbort` pod-scoped, and §4.1 glosses pod-scoped as "addresses the pod's adapter process", which is false of a continuation frame on a session's stream.

FACT: Nothing outside spec/04 declares a message's scope class, and no `§4.1` cross-reference anywhere in spec/ points at the message-scope table (every `§4.1` citation in the tree is about gateway subsystem extraction). Retiring the table breaks no inbound reference. The only two sites carrying `CoordinatorFenceRequest`'s class are :175 and :188. — EVIDENCE: spec/04_system-components.md:175,188

FACT: `spec/` states no gate limit anywhere; the "recorded limit" SPEC-1 says it retires lives in the gate file's header comment, not in the specification. A fixer looking for a limit sentence in spec/04 to rewrite will not find one. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:17-27

FACT: The tier-0 proto parse is shared by two gates and captures field NAMES only (the type is a non-capturing group), and it flattens `oneof` arms into the field set. Both the address-spelling clause and the envelope clause need the type, and the envelope clause needs to know which fields sit inside a `oneof`. The parse must be extended once, in `adapter_proto_parse_test.go`, with the claim-register gate's two call sites updated, rather than a second parse added beside it. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:23-25,33-61; tests/tier0_static/claim_register_proto_agreement_test.go:72,82

OPEN: §7 question 1 (does the replacement gate close the hole the table's gate closed?) is answerable NO-but-smaller, and this design supplies the argument: the table's gate caught a missing row, which a total derivation cannot have; what the table's gate never caught was a wrong row, since 0073's own recorded limit is that it cannot check a declared scope against the handler. The residual under the rule is exactly one case — a session addressed under both an unconventional name and an unconventional type — and it is narrower than the table's. Whoever owns §7 should close question 1 with that rather than leave the withdrawal branch live.

DEFERRED [proposals/0075_.../0075_....spec-changes.md §9]: §9 lists "The tier-0 gate file 0073's §8 introduces" and `tests/tier0_static/adapter_proto_message_scope_test.go` as two entries. They are one file: `git log --diff-filter=A` shows commit fb2af5f9c added it and `adapter_proto_parse_test.go` together. What is true instead is that the file list has three entries, and `adapter_proto_parse_test.go` (which TEST-1 must extend for the type-aware parse) is missing from it. Pre-existing, not caused by this round's fix.


### [spec.1.fix-design-G2.1]

DECISION: Add `tests/spec-map.json` to all four of TEST-1's parallel target lists (spec-changes §9, problem-statement §1.4's "confined to" sentence, non-spec-changes §5 TEST-1, summary deliverable index + "What changes"), and state the obligation once in the non-spec-changes TEST-1 block: the replacement gate's cases are re-registered under 4.1 and 28.5.3 in the same change that retires the old function names. BECAUSE the retiring gate's two functions are named by path::Fn in the register, so retiring them without re-registering turns tier 0 red on `validate-maps`. ALTERNATIVES: leaving it as an implementation detail (rejected — §9 IS the files-touched register); a new TEST-3 deliverable for the spec-map edit (rejected as hair — it must land in TEST-1's own commit or tier 0 is red between commits).

FACT: `tests/spec-map.json` credits `tests/tier0_static/adapter_proto_message_scope_test.go::TestAdapterProtoRequestMessagesAreClassifiedByScope` and `::TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under section 4.1, and the first again under 28.5.3. EVIDENCE: tests/spec-map.json:156, :169, :5670; the file's own annotations at tests/tier0_static/adapter_proto_message_scope_test.go:129 and :152.

FACT: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` is credited in spec-map as a WHOLE-FILE path, never as `path::Fn`. TEST-2 adds a map entry rather than a test function, so TEST-2 carries no spec-map obligation. EVIDENCE: tests/spec-map.json:172, :600, :1110, :1391, :3678.

WATCHOUT: do NOT add `tests/tier0_static/spec_map_slot_address_registration_test.go` to any list. `slotAddressCaseFiles` names the gate file by PATH (`:336`), and the wire test at `:409`. The replacement gate lands in the same file, so that register needs no edit; adding it stages an edit nothing requires. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336,:409.

WATCHOUT: summary.md's "No proto, generated code, handler, SDK, or reader-facing documentation file is touched." stays TRUE after this fix — `tests/spec-map.json` is none of those. Do not weaken or qualify that sentence. EVIDENCE: proposals/0075_.../0075_....summary.md:41.

MISTAKE / SEPARATE FINDING for a later round: spec-changes §9 lists "The tier-0 gate file 0073's §8 introduces" and "`tests/tier0_static/adapter_proto_message_scope_test.go`" as two distinct bullets, and summary.md:115 repeats the split. They are the SAME file: 0073's gate-introducing commit fb2af5f9c added exactly `tests/tier0_static/adapter_proto_message_scope_test.go` (210 lines) and no other tier-0 gate file. This is a pre-existing duplication defect, not caused by the spec-map fix; it must NOT be repaired in this edit. EVIDENCE: `git show --stat fb2af5f9c`; proposals/0075_.../0075_....spec-changes.md:97-98; summary.md:114-116.

OPEN: whether §9's two-bullet split is a live error or a deliberate hedge about where the replacement gate lands. A later round should file it; if the replacement gate were to land in a NEW file instead, `slotAddressCaseFiles` would then need the old path removed and the new one added, which the WATCHOUT above assumes is not the case.

FACT: `validate-maps` runs inside tier 0's static check set. EVIDENCE: cmd/lenny-test/cmd_run.go:761.


### [spec.1.fix-design-G3.1]

DECISION: Rewrite problem-statement.md §1.2's tree paragraph (lines 48-56, both paragraphs, not only 48-51) into one past/present pair — the ground held before 0076's CODE-1 and no longer holds — and re-anchor to `pkg/adapter/slot.go:59` (`coord coordinationState` on the slot entry), `pkg/adapter/coordination.go:16-38` (the per-session struct doc), `:107` (`func (s *Server) CoordinatorFence`), and `:116` (`s.boundSlotState(sessionID)`); then replace summary.md:98-100's trailing sentence with the same timeline. BECAUSE §1.2 is the ground SPEC-1 rests on, and the future-tense paragraph at 53-56 ("0076's CODE-1 deletes ... After it lands") is the same falsehood as 48-51; fixing only the lines the finding names leaves the section in two tenses. ALTERNATIVES: (a) delete the tree paragraph and rest §1.2 on the spec text plus 0076's OD3 answer alone — rejected, the tree evidence is what makes spec/04:175 false rather than a judgment call; (b) keep the paragraph and append a "the tree has since moved" note — rejected as hair, a patch over a false sentence.

FACT: `Server` has no `coord` field in the current tree; `s.coord` returns nothing under pkg/adapter. `pkg/adapter/server.go:302` is `hold holdState`. The coordination state is per session on the slot registry entry. EVIDENCE: pkg/adapter/server.go:299-302, pkg/adapter/slot.go:53-59, pkg/adapter/coordination.go:16-38

FACT: `pkg/adapter/coordination.go:84` is inside `BarrierWaiting` (decl at :81). The fence handler decl is at :107 and resolves the entry at :116 with `boundSlotState`. EVIDENCE: pkg/adapter/coordination.go:81, :107, :116

FACT: The three spec sites the correction bears on are live and unchanged: spec/04_system-components.md:149 (`#### Request Message Scope`), :151 (declared-rather-than-derived reason), :175 (fence row, scope `pod`), :188 (declaring sentence), :190 (`ShutdownRequest` precedent). :175 and :188 are contradicted by the landed code today. EVIDENCE: spec/04_system-components.md:149,151,175,188,190

WATCHOUT: review-log.md:25 also spells `s.coord` at `pkg/adapter/server.go:302`, but it is the record of an earlier anchor-refresh, not a claim about the current tree. Do not "correct" it; a review-log entry records what was true when it was written. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:25

WATCHOUT: the tempting wrong fix is swapping only the two line numbers while keeping "In the shipped tree that ground holds" and the future-tense sentences below it. That reads as a citation refresh and leaves the timeline (and summary.md:98) false, which a later round has to undo.

FACT: spec-changes.md §10 (lines 106-111) and D3/D6 already describe 0076's effect without asserting that the ground still holds, so they are not edit sites for this finding; status.md:21 likewise. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:106-111


### [spec.1.review-applicability.1]

FACT: `spec/04_system-components.md` §4.1 `#### Request Message Scope` opens at `:149`; the declaring
paragraph is `:151`; the table is `:155`-`:186` (32 rows, which includes a `CheckpointStart` row at `:168`
that is NOT an RPC request type); `:188` is the fence's declaring sentence; `:190` is the `ShutdownRequest`
precedent. All re-verified 2026-09-07. — EVIDENCE: spec/04_system-components.md:149-190

FACT: `CheckpointStart` appears in the whole of `spec/` in exactly three places, all inside §4.1: the
declaring paragraph `:151` ("with `CheckpointStart` carrying its own row"), the envelope row `:167`, and its
own row `:168`. Retiring the table therefore deletes the only statement of its scope anywhere in the
specification, and D1's rule ranges over "a request message", which it is not. Two consumers depend on that
classification: the tier-0 gate special-cases it, and the tier-3 map lists it with membership rule "the
specification's message-scope table gives it session scope". — EVIDENCE: spec/04_system-components.md:151,
tests/tier0_static/adapter_proto_message_scope_test.go:33-39, tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-44,:61

FACT: "0073's recorded limit about the table-reconciliation gate" exists ONLY in the landed proposal
document, under `### Recorded limits`. Nothing in `spec/`, `docs/`, or `tests/` states it: a tree-wide grep
for "in-scope message is classified" / "table and the proto agree" returns only those proposal lines. 0073
is Implemented, so the limit's only carrier is immutable. — EVIDENCE:
proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6684-6689,:6713

FACT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the tier-0 gate 0073 introduced; it was
added by commit `fb2af5f9c` ("Classify every adapter request message and reproduce the claim register"),
and 0073 refers to it in the singular as "the tier-0 gate" / "the tier-0 classification gate". The proposal
lists it twice in §9, once by description and once by path, as if two files. — EVIDENCE:
proposals/0075_.../0075_....spec-changes.md:98-99, proposals/0073_...:6686,:6713

FACT: the candidate stream-envelope predicate in §4 ("a `oneof` whose members are themselves declared
messages, and the request type of a streaming RPC") DOES select `CheckpointRequest` and nothing else in the
current proto. The four streaming request types are `PrepareWorkspaceRequest` (`:682`), `AttachRequest`
(`:989`), `CheckpointRequest` (`:1173`), and `AdapterEventsRequest` (`:1769`); only `CheckpointRequest`
declares a `oneof`. I checked this specifically to see whether §7 question 2 had a falsifying answer; it
does not. — EVIDENCE: schemas/lenny-adapter.proto:41,84,133,241,682,989,1173,1769

FACT: no pod-scoped request message declares a field of type `SessionId`, so D1's "no third clause is
needed" holds. `DemoteSDKRequest:1694`, `NegotiateVersionRequest:1706`,
`GetObservedIntegrationLevelRequest:1744`, `AdapterEventsRequest:1769`, `ReportPodScrubRequest:499` all
carry none, and every session address in the file is spelled `SessionId session_id` (26 sites), so a
name-based and a type-based rule select the same set today. — EVIDENCE: schemas/lenny-adapter.proto:596

WATCHOUT: the proto has drifted since this proposal was last refreshed. `SessionId` is defined at `:596`
(not `:589`), `CoordinatorFenceRequest` is at `:1455` with `SessionId session_id = 1` at `:1456` (not
`:1447`/`:1448`), and `CheckpointRequest` is at `:1173` (not `:1166`). Re-derive every proto anchor before
citing one. — EVIDENCE: schemas/lenny-adapter.proto:596,1455,1456,1173

DEFERRED [0075_....problem-statement.md]: §1.2 lines 48-51 say "In the shipped tree that ground holds. The
handler ... then mutates `s.coord` (`pkg/adapter/server.go:302`), which is a single `coordinationState` for
the whole adapter process." That is now false: 0076 landed, `Server.coord` is gone, and `coordinationState`
lives on the slot registry entry (`pkg/adapter/coordination.go:17-47`, read through `slotStateForSession` at
`:55`). `pkg/adapter/server.go` no longer contains the identifier at all. The true statement is that the
ground held until 0076 landed on 2026-09-07 and is now removed, which is what makes `spec/04:175` and
`:188` live defects rather than pending ones. Same file: the proto line citations at §1.1 line 28 (`:1166`)
and §1.2 lines 39-40 (`:1447`, `:1448`) are stale per the WATCHOUT above.

DEFERRED [0075_....implementation-checklist.md]: S1 (spec) lands SPEC-1 and carries "Tiers 0, 11", but
applying SPEC-1 deletes the table that `TestAdapterProtoRequestMessagesAreClassifiedByScope` parses, so
tier 0 is red from the end of S1 until S2 replaces the gate. `parseMessageScopeTable` returns no rows and
the gate then reports "carries no row for it" for every declared request message. Either S1 and S2 merge
into one step, or S1's tier list records the disposition. — EVIDENCE:
tests/tier0_static/adapter_proto_message_scope_test.go:54-70,:96-104

DEFERRED [0075_....non-spec-changes.md]: TEST-1 describes "the tier-0 file 0073's §8 adds for table
reconciliation" and "the existing scope test that accepts either class word in the table's cell
(`...adapter_proto_message_scope_test.go:75-81`)" as two artifacts. They are one file; `declaredScope` at
`:75-81` is a helper inside that very gate. Also unlisted anywhere: `tests/spec-map.json:156`, `:169`, and
`:5670` register both of that file's test functions by name, so replacing the gate leaves stale
registrations (no gate currently fails on that: `validateSpecMapPaths` checks `spec_file` only,
cmd/lenny-test/cmd_validate.go:255-292).

DECISION: filed three findings, all on the spec staging: SPEC-1's unreachable "retire 0073's recorded
limit" instruction, §9 listing one tier-0 file as two, and the loss of `CheckpointStart`'s classification.
BECAUSE each has a remedy that lands in the spec staging file and each is verifiable against the tree.
ALTERNATIVES: I did NOT file the general "SPEC-1 stages no replacement text" (the `IMPLEMENTOR TO FILL THE
BLANKS` markers in §4 and §5 name what is open and give a constraint, and the candidate envelope predicate
turned out to hold against the proto), nor D2's second gate clause being tautological under the derivation
rule (the proposal states that itself as §7 question 1, which is the loop's adjudication rather than a
defect), nor the checklist and spec-map items above (their remedies land in files this loop may not edit).


### [spec.1.review-citations.1]

FACT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the tier-0 gate file 0073's §8 introduced. It was created whole (210 lines, new file) by 0073's implementation commit `fb2af5f9c` ("Classify every adapter request message and reproduce the claim register"). The proposal treats "the tier-0 gate file 0073's §8 introduces" and that path as two different files in `spec-changes.md:98-99`, in `summary.md:115-117`, and in `non-spec-changes.md` TEST-1. — EVIDENCE: `git show --stat fb2af5f9c`; tests/tier0_static/adapter_proto_message_scope_test.go:16-27, :72-81

FACT: the §4.1 table's 32 rows are `spec/04_system-components.md:155-186` (verified by count), the header at `:153-154`. `:151` opens the section paragraph, `:168` is the `CheckpointStart` row, `:175` the fence row, `:188` the declaring sentence, `:190` the `ShutdownRequest` precedent. Every one of these anchors is exact today. — EVIDENCE: spec/04_system-components.md:149-190

FACT: `CheckpointStart` is a table row but is NOT the request type of any RPC. The 0073 gate injects it into the in-scope set by hand (`checkpointStartMessage`), and its own comment says "It is not an RPC's request type, so the RPC parse does not reach it, and §4.1 states its row explicitly." A derivation rule whose subject is "a request message" therefore does not reach it. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:32-37, :92; spec/04_system-components.md:168

FACT: `tests/spec-map.json` is gated for dangling `path::TestName` references by `validateSpecMapTestFuncs`, which runs inside tier 0's `validate-maps` static check. Test *paths* are not required to exist (`validateSpecMapPaths` deliberately skips them), but a named func that no longer declares fails. 0073's introducing commit touched `tests/spec-map.json` in the same change as the gate. — EVIDENCE: cmd/lenny-test/cmd_validate.go:941-985, cmd/lenny-test/cmd_run.go:761-771; tests/spec-map.json:156, :169, :5670

FACT: 0073's recorded limit about the table-reconciliation gate lives ONLY in the landed proposal, at `proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713`. Nothing in `spec/`, `docs/`, or `BUILD-GAPS.md` restates it. Grep for "classification gate" / "declared scope" / "message-scope" over spec/ docs/ BUILD-GAPS.md returns nothing on this subject. — EVIDENCE: proposals/0073_...md:6713

FACT: the proto measurement in the proposal still holds, but the anchors have drifted by ~7-8 lines. Re-parsed today: 31 RPC request messages across `service Adapter` and `service GatewayControl`; 25 declare `SessionId session_id`, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Current anchors: `SessionId` at `schemas/lenny-adapter.proto:596` (proposal and orchestrator say :589), `CheckpointRequest` at `:1173` (:1166), `CheckpointStart` at `:1193`, `CoordinatorFenceRequest` at `:1455` with its field at `:1456` (:1447/:1448). — EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1193, :1455

FACT: §4's candidate stream-envelope predicate CHECKS OUT. Parsed today: the only messages declaring a `oneof` of declared message types are `CheckpointRequest` and `CheckpointResponse`, and only `CheckpointRequest` is the request type of a streaming RPC. The four client-streaming RPCs are `PrepareWorkspace`, `Attach`, `Checkpoint`, and `AdapterEvents`; of their request types only `CheckpointRequest` carries such a oneof. Do not re-file §7 question 2 as a defect; the predicate selects `CheckpointRequest` and nothing else. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187

FACT: 0073's §4.2 value rule landed in `spec/05_runtime-registry-and-pool-model.md:515`, not in spec/04 §4.1. Retiring the §4.1 table does not touch it, so §3's "untouched" claim is right and `spec/05` is correctly absent from §9. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515

FACT: no other spec section, docs page, schema, or chart restates the fence's scope classification. `#### Request Message Scope` (spec/04:149) is the only occurrence of that heading anywhere, and §28.5.1's `CH-FENCE` block already describes the fence as per-session ("A fence for one session does not change the generation the pod holds for another"), so it needs no edit. `docs/reference/adapter-contract.md:69` names the RPC without a scope. §28.3's `CH-FENCE` register row (spec/28:120) has no scope column. — EVIDENCE: spec/28_communication-channels.md:120, :314-318; docs/reference/adapter-contract.md:69

DEFERRED [problem-statement.md]: `§1.2` says "In the shipped tree that ground holds. The handler ... mutates `s.coord` (`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process." That is now FALSE: 0076 is Implemented, `Server.coord` is gone, and `coordinationState` is a per-slot-entry field carrying `lastFenced`/`initialized`. What is true instead: the ground the specification gives at `spec/04:151` and `:188` is already falsified by the shipped tree, so SPEC-1 corrects a live contradiction rather than a prospective one. — EVIDENCE: pkg/adapter/coordination.go:27-47, :53-60; `grep -n coord pkg/adapter/server.go` returns no `coord` field

DEFERRED [summary.md]: `## Defects in the shipped tree that this proposal does not stage` says "The §4.1 ground holds in the shipped tree while the fence handler still writes pod-wide state, so it becomes a defect only as the coordination generation moves onto the slot entry, which is the point at which this proposal applies." Same falsification: 0076 has landed, so both defects are live in the shipped tree today. — EVIDENCE: pkg/adapter/coordination.go:27-47

DEFERRED [non-spec-changes.md]: TEST-2 cites the false coverage clause at `session_address_wire_test.go:40-43`; the clause now sits at `:37-44` (the sentence naming `CoordinatorFenceRequest` spans `:41-44`). Drift only, meaning unchanged.

WATCHOUT: `spec-changes.md` §9 "Files touched on application" is the one file list this loop can edit, and it is where an unstaged-site finding lands. Do not read a missing test-surface as out of scope just because its content lives under `tests/`.


### [spec.1.review-client-surface.1]

FACT: The client-surface sweep for this proposal comes back CLEAN. Nothing outside `spec/04` §4.1 carries the
message-scope classification. The reclassification of `CoordinatorFenceRequest` needs no proto, SDK, OpenAPI,
CRD, JSONL-schema, or docs edit: `schemas/lenny-adapter.proto:1449-1454` already describes the fence
per-session ("records the generation against the session the fence names"), `docs/reference/adapter-contract.md:69`
carries no scope claim, and `grep -rn "CoordinatorFence\|coordination_generation" sdks/` returns nothing.
EVIDENCE: schemas/lenny-adapter.proto:1449-1461, docs/reference/adapter-contract.md:69

FACT: The §4.1 table has no consumer outside the tier-0 gate. The external-adapter compliance suite is
schema-driven, generated from `schemas/*.proto|json` rather than from spec prose, so retiring the table
touches no client-facing generated artifact. No spec/docs file links the `#request-message-scope` anchor.
EVIDENCE: spec/24_lenny-ctl-command-reference.md:114

FACT: The derivation rule was measured against the current proto and HOLDS with no counterexample.
Exactly 26 messages declare a `SessionId` field: the 25 RPC request types plus `CheckpointStart`
(`schemas/lenny-adapter.proto:1217`). No response, event, or nested message declares one, and no field
named `session_id` has any type other than `SessionId`. The 6 request types declaring none are
`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`,
`NegotiateVersionRequest`, `ReportPodScrubRequest` — every one of them a pod row in the table except
the envelope. So the rule over "messages" (not "request messages") reproduces the table exactly, with the
fence moving from pod to session.
EVIDENCE: schemas/lenny-adapter.proto:596, :1193-1218, :1455-1461

FACT: §7 question 2 is ANSWERED by measurement. The candidate stream-envelope predicate in §4 ("a message
declaring a `oneof` whose members are themselves declared messages, and which is the request type of a
streaming RPC") selects `CheckpointRequest` and nothing else. The four streaming request types are
`AdapterEventsRequest`, `AttachRequest`, `CheckpointRequest`, `PrepareWorkspaceRequest`; only
`CheckpointRequest` declares a `oneof`. No fallback to a named list is needed.
EVIDENCE: schemas/lenny-adapter.proto:1173-1187

FACT: The value rule ("a session-scoped request whose session identifier is empty is rejected at the adapter
boundary with `InvalidArgument`") lives ONLY at spec/05_runtime-registry-and-pool-model.md:515. Reclassifying
the fence extends that rule to `CoordinatorFenceRequest`, and the shipped handler already complies
(`pkg/adapter/coordination.go:108-111` returns `InvalidArgument` on an empty session id), so the proposal's
"no handler changes" claim survives the extension. No spec edit is needed at :515.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515, pkg/adapter/coordination.go:108-111

WATCHOUT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the gate 0073's §8 introduced — one
file, added whole by commit `fb2af5f9c` ("Classify every adapter request message and reproduce the claim
register"). `declaredScope` at `:75-81` is a helper INSIDE it, not a separate pre-existing test. The
proposal's §9 and TEST-1 both read as though two files exist. There is no second gate file.
EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:75-81; git show --stat fb2af5f9c

WATCHOUT: proto anchors in the problem statement have drifted by 7-8 lines since it was written, and so has
the orchestrator's briefing. Current: `message SessionId` at `schemas/lenny-adapter.proto:596` (proposal
says 589), `CheckpointRequest` at `:1173` (says 1166), `CoordinatorFenceRequest` at `:1455` with
`SessionId session_id = 1` at `:1456` (says 1447/1448). `spec/04` anchors :149/:151/:175/:188/:190 all still
hold exactly as cited.
EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1455

DEFERRED [0075...problem-statement.md]: §1.2 lines 48-51 state "In the shipped tree that ground holds. The
handler at `pkg/adapter/coordination.go:84` reads the identifier ... and then mutates `s.coord`
(`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process." This is
now FALSE. 0076 landed: `Server.coord` is gone, `coordinationState` carries a doc comment saying it "lives on
that session's slot registry entry" (`pkg/adapter/coordination.go:17-47`), and `:84` is now `BarrierWaiting`.
What is true: the ground the specification gives at `spec/04:151`/`:188` is falsified BY THE TREE AS IT
STANDS, not merely once 0076 lands. The same tense problem sits in the summary's "Defects in the shipped tree
that this proposal does not stage" section ("The §4.1 ground holds in the shipped tree while the fence handler
still writes pod-wide state").
EVIDENCE: pkg/adapter/coordination.go:17-47; pkg/adapter/server.go has no `coord` field

OPEN: 0076's OD3 Question A recommendation, which the reviewer accepted verbatim ("The entry's
recommendation"), reads "yes, reclassify the row to session scope AND rewrite the declaring sentence, naming
the pod-scoped hold exit as the one pod-wide effect that remains under D5"
(0076 summary:258-259, table row :185). This proposal's SPEC-1 carries the reclassification but says nothing
about naming the pod-scoped hold exit. I did not file it, because §5's "IMPLEMENTOR TO FILL THE BLANKS"
bounds the replacement text for `:188` and filing would be over-specification. A later round should decide
whether the hold-exit clause is a required half of the answer this proposal exists to implement.
EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:185, :258-259

OPEN: §7 question 1 (whether the replacement gate closes the hole the table's gate closed) is untouched by
this pass. Note that D2's clause "no pod-scoped message declares a field of the address type" is
tautological once the table is gone, because pod scope is then DERIVED from the absence of that field. The
gate has an input for that clause only if the rewritten `:188` keeps naming the pod-scoped messages by hand,
which reintroduces a smaller table. §7 q1 half-states this; nobody has decided it.
EVIDENCE: 0075 spec-changes.md:8-12, :82-89


### [spec.1.review-docs-alignment.1]

FACT: The docs/ tree carries NO mirror of the §4.1 request-message-scope table, and no docs page states a
scope for `CoordinatorFence`. A tree-wide grep of `docs/`, `schemas/*.json`, and `charts/` for
`CoordinatorFenceRequest`, `CheckpointStart`, `ReportPodScrubRequest`, `ShutdownRequest`,
`AdapterEventsRequest`, and `NegotiateVersionRequest` returns nothing: docs name RPCs, never request
message types. So retiring the table and reclassifying the fence needs no docs edit, and the proposal's
"no reader-facing page changes" claim (problem-statement §1.4) holds.
EVIDENCE: docs/reference/adapter-contract.md:69 (`CoordinatorFence` row, no scope word), :81
(`ReportSessionScrub` row, the only docs row that states a scope, and it stays true under the rule).

FACT: The only two docs-visible scope statements on this contract come from spec/04:725 and :726
(`ReportSessionScrub` session-scoped, `ReportPodScrub` pod-scoped) and are mirrored at
docs/reference/adapter-contract.md:81. Both survive the derivation rule unchanged, because
`ReportSessionScrubRequest` declares `SessionId session_id` and `ReportPodScrubRequest` declares none.
EVIDENCE: spec/04_system-components.md:725-726; schemas/lenny-adapter.proto:458 vs the comment at :496
("there is no session_id because occupancy is zero at the recycle").

FACT: Every `session_id` field on both service blocks is typed `SessionId`, never a bare `string`, so the
rule's "field of the address type" and "field named session_id" select the same set. Checked by grepping
every `session_id` declaration in the proto.
EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), and the 25 `SessionId session_id`
declarations (e.g. :342, :458, :1217, :1464).

FACT: `CheckpointStart` declares `SessionId session_id = 7`, so the stream-envelope clause resolves to a
frame that is itself addressed. The envelope predicate in §4 selects `CheckpointRequest` alone among the
streaming request types.
EVIDENCE: schemas/lenny-adapter.proto:1201 (`message CheckpointStart`), :1217 (`SessionId session_id = 7`),
:1173 (`message CheckpointRequest` with the three-member oneof).

WATCHOUT: The proto anchors in the orchestrator's brief and in the problem statement are stale by seven to
eight lines against the current tree. `SessionId` is at :596 (not :589), `CoordinatorFenceRequest` at :1455
(not :1447) with its `session_id` at :1464 (not :1448), `CheckpointRequest` at :1173 (not :1166). Re-derive
every proto line before citing it.
EVIDENCE: schemas/lenny-adapter.proto:596, :1455, :1464, :1173.

FACT: `proposals/` is excluded from the line-citation ratchet, so the line citations this proposal writes
(e.g. `spec/04_system-components.md:190`) do not violate the N8 gate.
EVIDENCE: scripts/specshift/scope/scope.go:99 (`const readExcludedPrefix = "proposals/"`);
scripts/specshift/line/line.go:45.

FACT: No tier-11 doc test reads the §4.1 table or the `#### Request Message Scope` heading; a grep of
tests/ for `Request Message Scope` returns only the two tier-0 files and the tier-3 comment. Retiring the
table therefore breaks no doc-consistency gate.
EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:17;
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40.

FACT: The reclassification does not create an undocumented behavior change at the adapter boundary. The
handler already rejects an empty session id with `InvalidArgument`, which is what the §4.2 value rule
(spec/05:515) requires of a session-scoped request, so bringing the fence into that class matches shipped
code rather than mandating a new refusal.
EVIDENCE: pkg/adapter/coordination.go:106-109; spec/05_runtime-registry-and-pool-model.md:515.

FACT: spec/28's `CH-FENCE` card is already fully per-session after 0076 ("The pod records the generation
against the session the fence names ... A fence for one session does not change the generation the pod
holds for another"), so it agrees with the post-edit §4.1 and is not a missed edit site.
EVIDENCE: spec/28_communication-channels.md:314-318.

DECISION: Returned an empty findings list for the documentation-alignment lens — BECAUSE the change is
confined to a spec-internal classification statement with no docs mirror, no metric, alert, error code,
flag, or endpoint moves, and the one accepted residual (§7 question 1's authoring hazard) is already
assigned landing text by SPEC-1's "state the new gate's limit in its place" clause. ALTERNATIVES: I
considered filing docs/reference/adapter-contract.md:69 ("Precondition for any subsequent operational RPC",
which reads pod-wide after 0076 made the fence per-session) but rejected it: that wording is a 0076 residue
mirroring spec/04:713 and spec/28:321-324, this proposal neither creates nor touches it, and its remedy is
a docs edit that this spec-only loop may not land.

DEFERRED [docs/reference/adapter-contract.md]: line 69 describes `CoordinatorFence` as "precondition for
any subsequent operational RPC", which reads as a pod-wide gate. After 0076 the fence and the generation
are per bound session, so the true statement is that the fence is the precondition for subsequent
operational RPCs naming the session it fenced. Same imprecision sits in its source at
spec/04_system-components.md:713. Neither is staged here, and both belong to whoever takes the 0076
residue (proposal 0080's inventory is the register for it).


### [spec.1.review-edit-sites.1]

FACT: The §4.1 subsection is `#### Request Message Scope` at spec/04_system-components.md:149; prose at :151; table header :153; 32 rows at :155-186 (`CheckpointStart` at :168, `CoordinatorFenceRequest` at :175); declaring sentence :188; `ShutdownRequest` precedent paragraph :190. All re-verified 2026-09-07 at HEAD cdcd7e9e. — EVIDENCE: spec/04_system-components.md:149,168,175,188,190

FACT: Proto anchors have drifted ~+7/+8 from the anchors the orchestrator brief and the proposal's §1.2 carry. Current: `SessionId` message at schemas/lenny-adapter.proto:596, `CheckpointRequest` at :1173, `CheckpointStart` at :1193 with `SessionId session_id = 7` at :1217, `CoordinatorFenceRequest` at :1455 with `SessionId session_id = 1` at :1456. The proposal's §1.2 cites :1447/:1448 and §1.1 cites :1166. — EVIDENCE: schemas/lenny-adapter.proto:1455

FACT: A full parse of the two service blocks (script, not grep) confirms the proposal's arithmetic: 31 distinct RPC request types, 25 declaring a top-level `SessionId session_id`, 6 not (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`). Field NAME and field TYPE agree everywhere: every `session_id` field on a request message is typed `SessionId`, so "declares a field of the address type" and "declares `session_id`" select the same set. — EVIDENCE: schemas/lenny-adapter.proto:596

FACT: The §4 candidate envelope predicate ("a oneof whose members are declared messages, on a streaming RPC's request type") does select `CheckpointRequest` and nothing else. The four streaming request types are `PrepareWorkspaceRequest`, `AttachRequest`, `CheckpointRequest`, `AdapterEventsRequest`; only `CheckpointRequest` carries a oneof-of-messages. `AdapterEventsRequest` is one `bytes envelope_json = 1`. — EVIDENCE: schemas/lenny-adapter.proto:1174-1178, :1769-1771

FACT: No spec/, docs/, schemas/, or charts/ surface outside `spec/04` §4.1 restates the scope classification or the fence's pod scope. `grep -rn "Request Message Scope"` returns spec/04:149 alone; "declared rather than derived" returns :151 and :188 alone; the §28.5.1 `CH-FENCE` card (spec/28:307-343), the §28.3 register row (:120), and the §28.8 matrix row (:1814) carry no scope column and were already corrected by 0076; `docs/reference/adapter-contract.md` carries no scope column for `CoordinatorFence` (:69). The proposal's §1.4 "no reader-facing page changes" holds. — EVIDENCE: spec/28_communication-channels.md:120, docs/reference/adapter-contract.md:69

FACT: `tests/spec-map.json` names the retiring gate's two test functions in THREE places, under sections 4.1 and 28.5.3 (lines 156, 169, 5670). `lenny-test validate-maps` runs as a tier-0 static check (cmd/lenny-test/cmd_run.go:761) and `validateSpecMapTestFuncs` (cmd/lenny-test/cmd_validate.go:941) fails on a dangling `path::Func`. The file appears nowhere in the proposal. This was my highest-value edit-site catch. — EVIDENCE: tests/spec-map.json:156,169,5670

FACT: "0073's recorded limit about the table-reconciliation gate", which SPEC-1 tells the implementor to retire inside `spec/04` §4.1, is NOT in spec/ at all. It is recorded at proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713, inside an implemented proposal that `.claude/rules/spec-driven-development.md` bars from editing. §4.1 states no gate and no gate limit anywhere in :149-191. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713

WATCHOUT: 0073 records TWO different limits about this gate and they are easy to confuse. :912-913 records a limit that applies only under the REJECTED choice (b) ("the `GatewayControl` request messages are outside the tier-0 gate entirely"), and explicitly says "Choice (a) has no such limit, and it is the choice this proposal takes". The limit that actually exists under the shipped choice (a) is the one at :6713 about a declared scope not being checkable against the handler. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:912

WATCHOUT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the file 0073's §8 introduces; §9's files-touched list names it twice, once by path and once as "The tier-0 gate file 0073's §8 introduces". §8 of 0073 also introduces a second tier-0 file, the shared parse helper `tests/tier0_static/adapter_proto_parse_test.go`, whose doc comment at :10-15 describes the message-scope gate's subject and goes stale when the table is retired. I did NOT file either, because the duplicate reading is ambiguous and the stale comment does not make the implementation wrong; a later round may want to reconsider. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:10-15

FACT: `protoServiceRequests` (tests/tier0_static/adapter_proto_parse_test.go:68) has exactly one caller, the retiring gate (adapter_proto_message_scope_test.go:87). If the replacement gate does not call it, staticcheck's `unused` turns tier 0 red. The replacement gate almost certainly does need it, so I did not file this. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:87

FACT: The §4.2 value rule that reclassification newly extends to `CoordinatorFenceRequest` ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved", spec/05_runtime-registry-and-pool-model.md:515) is ALREADY satisfied by the handler: `pkg/adapter/coordination.go:109-111` returns `codes.InvalidArgument` on an empty session id. No spec or code gap opens there. — EVIDENCE: pkg/adapter/coordination.go:109

DEFERRED [proposals/0075_.../0075_....problem-statement.md]: §1.1's proto anchor `schemas/lenny-adapter.proto:1166` for `CheckpointRequest` is now :1173, and §1.2's `:1447`/`:1448` for `CoordinatorFenceRequest` and its `session_id` are now :1455/:1456. I filed §1.2's tree claim (the `s.coord` sentence) as a finding because the orchestrator named it a live edit site, but these three pure line drifts I did not file separately; whoever corrects §1.2 should refresh them in the same pass. — EVIDENCE: schemas/lenny-adapter.proto:1173, :1455

OPEN: §7 question 1 (does the replacement gate close the hole the table's gate closed?) is unanswered and the proposal says a "no" means withdrawal. Note the consequence a withdrawal has to carry: spec/04:175 and :188 are false about the tree TODAY regardless of the table's fate, so a withdrawal must hand the standalone §4.1 reclassification to some owner or it recreates the gap 0076 handed here.


### [spec.1.review-feasibility.1]

FACT: The derivation rule holds over the current proto with no counterexample. All 26 `SessionId`-typed
fields in `schemas/lenny-adapter.proto` are named `session_id`; 25 sit on RPC request messages and one on
`CheckpointStart` (`:1217`). The six request messages with no address field are `CheckpointRequest`,
`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`,
and `ReportPodScrubRequest`. No message declares a `SessionId` field under any other name, so "declares a
field of the address type" and "declares `session_id`" select the same set today.
EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674

FACT: `CoordinatorFence`'s handler already rejects an empty session id with `InvalidArgument`, so the
reclassification does not collide with the §4.2 value rule at spec/05_runtime-registry-and-pool-model.md:515.
EVIDENCE: pkg/adapter/coordination.go:105-108

FACT: "0073's recorded limit about the table-reconciliation gate" exists nowhere in `spec/`. `spec/` never
mentions a tier-0 gate at all (grep for "tier-0" over spec/ returns nothing). The limit is written only in
the landed proposal 0073's §9.
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6686-6689, :6713

FACT: The "tier-0 gate file 0073's §8 introduces" IS
`tests/tier0_static/adapter_proto_message_scope_test.go`; commit fb2af5f9c added it together with
`adapter_proto_parse_test.go` (the shared `protoServiceRequests` helper) and
`claim_register_generator_test.go`. `declaredScope` is at `:75-81` of that same gate file, so §9 of the
spec staging lists one file as two.
EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:17-31,75-81

FACT: The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`,
`adapter_barrier_doc_comment_scope_test.go`) do not read `spec/04_system-components.md`, so retiring the
§4.1 table does not turn them red. Only the message-scope gate reads that file.
EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:31

DEFERRED [0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md]: `tests/spec-map.json`
maps `TestAdapterProtoRequestMessagesAreClassifiedByScope` under §4.1 and under a second section
(`:156`, `:5670`). TEST-1 retires that test and its §9 file list omits `tests/spec-map.json`, so the map
would name a test that no longer exists. What is true: retiring or replacing the gate requires the
spec-map rows to be repointed at the replacement case in the same step.
EVIDENCE: tests/spec-map.json:156, tests/spec-map.json:5670

DEFERRED [0075_fix_derive-message-scope-from-the-address-type.problem-statement.md]: every proto anchor in
§1.1/§1.2 has drifted by roughly eight lines. `CoordinatorFenceRequest` is at `schemas/lenny-adapter.proto:1455`
with `SessionId session_id = 1` at `:1456` (stated `:1447`/`:1448`); `CheckpointRequest` is at `:1173`
(stated `:1166`); `message SessionId` is at `:596` (stated `:589` in the review log's §11).
EVIDENCE: schemas/lenny-adapter.proto:1455-1456, :1173, :596

WATCHOUT: `spec/04_system-components.md` line numbers for the §4.1 sites are still exact as the proposal
states them (`:149` heading, `:151` declared-not-derived sentence, `:175` fence row, `:188` grounding
sentence, `:190` `ShutdownRequest` precedent). Do not "refresh" them; they are right.
EVIDENCE: spec/04_system-components.md:149,151,175,188,190

FACT: the §4.1 table carries 32 rows (`:155-186`) but only 31 of them are RPC request messages. The 32nd
is `CheckpointStart`, which no RPC declares as a request type; the existing gate has to inject it by name
(`inScope[checkpointStartMessage] = checkpointStartService`). Any rule stated over "request messages"
alone therefore drops a classification the table carries.
EVIDENCE: spec/04_system-components.md:168, tests/tier0_static/adapter_proto_message_scope_test.go:32-38

UNVERIFIED: whether a replacement gate can read a pod-scoped set from anywhere once the table is gone. I
found no other declaration site in `spec/`. Whoever answers §7 question 1 should settle this before
pricing the residual.


### [spec.1.review-fresh.1]

FACT: The proto anchors in the orchestrator brief and in the proposal are ALL stale by 7 lines. Re-derived today: `message SessionId` is at `schemas/lenny-adapter.proto:596` (not 589), `message CheckpointRequest` at `:1173` (not 1166), `message CheckpointStart` at `:1193`, `message CoordinatorFenceRequest` at `:1455` with `SessionId session_id = 1;` at `:1456` (not 1447/1448). — EVIDENCE: schemas/lenny-adapter.proto:596,1173,1193,1455,1456

FACT: A service-block parse re-run today returns 31 distinct RPC request messages; 25 declare a top-level `SessionId` field, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Every field of type `SessionId` in the file is spelled `session_id`, and every field spelled `session_id` is of type `SessionId`, so the field-name predicate and the type predicate coincide TODAY. The §4.1 table has 32 rows (spec/04_system-components.md:155-186) because `CheckpointStart` gets a row of its own. — EVIDENCE: spec/04_system-components.md:155-186

FACT: `CheckpointStart` is NOT an RPC request type. 0073's tier-0 gate hard-codes it as an extra in-scope name, and the derivation rule as staged (D1) covers "request messages" only, so the rule drops it. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:33-38 (`checkpointStartMessage`), proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:4684-4687

FACT: "0073's recorded limit about the table-reconciliation gate", which SPEC-1 orders retired inside its `spec/04` §4.1 edit block, lives in the LANDED 0073 proposal's own "Recorded limits" section, not anywhere in spec/. `grep -n "cannot check" spec/04_system-components.md` returns nothing on this subject. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6686-6690

FACT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the "new tier-0 file" 0073's §8 introduced (added in commit `fb2af5f9c` alongside its shared parse helper `adapter_proto_parse_test.go`). §9 of the spec staging lists it twice, once by description and once by path. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:98-99

FACT: The spec never writes `SessionId` and never uses the phrase "address type". `grep -n "address type\|SessionId\b" spec/04_system-components.md spec/28_communication-channels.md` is empty. spec/04:151 keys the classification on the FIELD NAME (`session_id`), while D1 and D2 key the staged rule on the TYPE. — EVIDENCE: spec/04_system-components.md:151

FACT (checked, no finding): the §4 candidate envelope predicate holds. Of the four streaming-RPC request types, only `CheckpointRequest` declares a `oneof` of declared messages; `AttachRequest`, `AdapterEventsRequest`, and `PrepareWorkspaceRequest` declare no oneof. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187

FACT (checked, no finding): the reclassification imposes no handler change. `CoordinatorFence` already rejects an empty session id with `InvalidArgument` before resolving anything, which is what the §4.2 value rule (spec/05_runtime-registry-and-pool-model.md:515) demands of a session-scoped request. — EVIDENCE: pkg/adapter/coordination.go:106-109

FACT (checked, no finding): no docs/, schemas/, or charts/ surface restates the message-scope classification. `docs/reference/adapter-contract.md` names `CoordinatorFence` at :69 with no scope claim; the proto carries no scope comment; §28.3's `CH-FENCE` register row has no scope column. So §1.4's "no reader-facing page changes" holds. — EVIDENCE: spec/28_communication-channels.md:120, docs/reference/adapter-contract.md:69

WATCHOUT: `Server.coord` really is gone from the tree (`grep -n "s\.coord\b" pkg/adapter/*.go` is empty; the state is on the slot entry at pkg/adapter/coordination.go:26-46). Any revision that still says the pod-wide state "holds in the shipped tree" is stale.

DEFERRED [proposals/0075.../0075_....problem-statement.md]: §1.1 cites `CheckpointRequest` at `schemas/lenny-adapter.proto:1166` and §1.2 cites `CoordinatorFenceRequest` at `:1447` with its field at `:1448`, and `pkg/adapter/server.go:302` for `s.coord`. All four are false now: the proto anchors are 1173/1455/1456 and `s.coord` no longer exists. §1.2's sentence "In the shipped tree that ground holds" is also false after 0076. This loop may not edit the problem statement.

DEFERRED [tests/tier0_static/adapter_proto_parse_test.go]: its package doc at :12-14 and `protoServiceRequests`'s comment at :66-67 both describe their subject as "the §4.1 message-scope classification table". Both become false when SPEC-1 retires the table, and the file is in no edit list of this proposal. The non-spec loop owns it.

OPEN: §7 question 1 (does the replacement gate close the hole the table's gate closed) is still unanswered, and the answer decides whether the proposal survives at all. Note that D2's second clause, "no pod-scoped message declares a field of the address type", is a tautology under D1 (pod-scoped is DEFINED as not declaring it) unless some surviving spec text independently enumerates the pod-scoped messages. The only candidate is the second sentence of spec/04:188, which SPEC-1 may remove. Whoever answers question 1 should settle that.


### [spec.1.review-kubernetes.1]

DECISION: Returned an empty findings list — BECAUSE the staged spec edit (SPEC-1) rewrites exactly one
subsection, `spec/04_system-components.md` §4.1 `#### Request Message Scope` (`spec/04_system-components.md:149-190`),
which classifies gRPC request messages on the gateway-adapter contract as session- or pod-scoped. That
surface touches no CRD, no status subresource, no field manager, no finalizer, no admission webhook, and no
reconcile loop, so every idiom this lens judges is unengaged — ALTERNATIVES: I checked whether the fence's
recorded generation is held in a CRD status (it is not: `spec/04_system-components.md:200` puts
`coordination_generation` on the Postgres `sessions` row, and the pod side is in-process adapter state), and
whether the "reconciliation gate" language in D2 meant controller reconciliation (it does not; it is a
tier-0 static test that reconciles a spec table against `schemas/lenny-adapter.proto`).

FACT: The word "reconciliation" in this proposal always means a tier-0 static check of the §4.1 table
against the proto, never controller-runtime reconciliation. A Kubernetes reviewer should not read D2 as a
controller change. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:8-12

FACT: The §4.1 Request Message Scope table currently spans `spec/04_system-components.md:153-186` (32 rows,
including `CheckpointStart`, which is a `oneof` member rather than an RPC request type — that is why the
table has 32 rows while the proto parse yields 31 request messages). The fence row is `:175`, the declaring
sentence `:188`, and the `ShutdownRequest` precedent paragraph `:190`. All three re-verified today; D3's
citation of `:190` is correct. — EVIDENCE: spec/04_system-components.md:175, spec/04_system-components.md:188, spec/04_system-components.md:190

WATCHOUT: The proto anchors quoted in the problem statement and in review-log §11 have drifted and are now
wrong by 7-8 lines. `message SessionId` is at `schemas/lenny-adapter.proto:596`, not `:589`;
`message CoordinatorFenceRequest` is at `:1455`, not `:1447`; `SessionId session_id = 1` is at `:1456`, not
`:1448`. The orchestrator's brief asserts these were "re-verified today" at the old values, so do not trust
that assertion. I did not file this: the sites live in
`0075_...problem-statement.md:39` and `...review-log.md` §11, which this spec-edits loop may not edit.
— EVIDENCE: schemas/lenny-adapter.proto:596, schemas/lenny-adapter.proto:1455

DEFERRED [proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.problem-statement.md]: §1.2 at `:39` claims
"`CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1447`) declares `SessionId session_id = 1` at
`:1448`". True statement, wrong lines: the message opens at `schemas/lenny-adapter.proto:1455` and the field
is at `:1456`. The same paragraph's `SessionId` anchor at `:589` should be `:596`.


### [spec.1.review-mechanism.1]

FACT: The derivation rule holds against the current proto. A parse of the two service blocks
(31 RPCs, 31 distinct request types) returns 25 request messages declaring a top-level
`SessionId session_id` and 6 that do not: `CheckpointRequest`, `DemoteSDKRequest`,
`NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`,
`ReportPodScrubRequest`. — EVIDENCE: schemas/lenny-adapter.proto:32, :261 (service blocks); :1455-1456
(`CoordinatorFenceRequest` / its `session_id`)

FACT: The stream-envelope predicate in §4 is sound. `CheckpointRequest` is the only RPC request type
carrying a top-level `oneof` of declared messages; the only other top-level `oneof` in the file is on
`CheckpointResponse`, which is not a request type. — EVIDENCE: schemas/lenny-adapter.proto:1173

WATCHOUT: Every proto line anchor in the proposal is stale by 7-8 lines, and the orchestrator brief
repeats the stale numbers. Real anchors: `message SessionId` at :596 (not :589), `CoordinatorFenceRequest`
at :1455 with `session_id` at :1456 (not :1447/:1448), `CheckpointRequest` at :1173 (not :1166),
`CheckpointStart` at :1193. — EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1193, :1455

FACT: "The tier-0 gate file 0073's §8 introduces" and `tests/tier0_static/adapter_proto_message_scope_test.go`
are ONE file. `git log --diff-filter=A` shows it added by fb2af5f9c, 0073's implementation commit. The
proposal's §9 and its deliverable index treat them as two. — EVIDENCE:
tests/tier0_static/adapter_proto_message_scope_test.go:75-81 (`declaredScope`, cited as the "existing"
separate test); git fb2af5f9c

FACT: `tests/spec-map.json` credits §4.1 and §28.5.3 with the two cases in that gate file
(`::TestAdapterProtoRequestMessagesAreClassifiedByScope`, `::TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage`),
and `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names the file in
`slotAddressCaseFiles`, whose gate requires every case in it to be credited under each section its
`// spec:` annotation cites. Retiring or renaming the gate reaches both files, and neither is in §9. —
EVIDENCE: tests/spec-map.json:156, :169, :5670; tests/tier0_static/spec_map_slot_address_registration_test.go:970-988

FACT: 0073's "recorded limit" about the table-reconciliation gate exists ONLY in the landed proposal
document, never in `spec/`. A repo-wide grep for "in-scope message is classified" outside `proposals/`
and `scratchpad/` returns nothing. SPEC-1 orders it retired from `spec/04` §4.1, where it is not. —
EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6687;
spec/04_system-components.md:149-190 (whole §4.1 subsection, no such sentence)

FACT: The fence handler already refuses an empty `session_id` with `InvalidArgument`, so reclassifying
`CoordinatorFenceRequest` session-scoped does not put it out of conformance with the value rule at
spec/05_runtime-registry-and-pool-model.md:515. Not a finding; checked so a later round need not. —
EVIDENCE: pkg/adapter/coordination.go:108-111

FACT: §28's `CH-FENCE` block already states the per-session record-and-reject rule after 0076, and carries
no scope word, so it is NOT a missed edit site for the reclassification. Same for
docs/reference/adapter-contract.md:69. — EVIDENCE: spec/28_communication-channels.md:314-319

DEFERRED [tests/tier3_contract/adapter_session_address/session_address_wire_test.go]: the map comment's
membership rule, "A message is in this set when the specification's message-scope table gives it session
scope" (:38-40), dangles once SPEC-1 retires the table. TEST-2 stages only the removal of the exclusion
clause at :40-43, not the membership sentence. What is true instead: membership follows the derivation
rule.

DEFERRED [tests/tier0_static/adapter_proto_parse_test.go]: its package doc at :13 describes the shared
parse as serving "the message-scope classification table", which is false once the table is retired.


### [spec.1.review-operational.1]

DECISION: Returned an empty findings list under the operational-consistency lens — BECAUSE the staged spec edit touches no condition, metric, alert, runbook, or operator-facing page, and I verified every operational surface that could have been dragged along by the reclassification and found each one already consistent — ALTERNATIVES: I weighed filing SPEC-1's "Retire 0073's recorded limit" clause and §9's double-listing of the tier-0 gate file; both are recorded below as leads rather than filed, for the reasons given.

FACT: The reclassification of `CoordinatorFenceRequest` to session-scoped creates NO operational edit site outside `spec/04` §4.1. Verified exhaustively: the only reader-facing mention of the RPC is `docs/reference/adapter-contract.md:69`, whose row states no scope ("Announce new coordination generation on gateway handoff"); the `CH-FENCE` contract card at `spec/28_communication-channels.md:307-320` already reads session-scoped ("The pod records the generation against the session the fence names ... A fence for one session does not change the generation the pod holds for another"); `spec/10_gateway-internals.md:60` already states "the fenced generation is held per bound session and no pod-level fenced generation remains"; `spec/16_observability.md:183, :190-192, :552` name the handoff metrics and the `CoordinatorHandoffSlow` alert without reference to message scope. — EVIDENCE: docs/reference/adapter-contract.md:69, spec/28_communication-channels.md:314, spec/10_gateway-internals.md:60, spec/16_observability.md:183

FACT: The only two spec sentences that ground the declared classification are `spec/04_system-components.md:151` and `:188`; a repo-wide grep for "rather than derived" returns those two and nothing else in `spec/`, `docs/`, or `pkg/`. SPEC-1 already carries both, plus the `:175` row. The `#### Request Message Scope` heading at `:149` has no inbound markdown link anywhere in the tree, so retiring the table raises no anchor-redirect obligation. — EVIDENCE: spec/04_system-components.md:151, :188; grep "request-message-scope" over spec/ docs/ returns only spec/04_system-components.md:149

FACT: The type-based derivation rule of D1 is exact against the current proto, checked by parsing both service blocks. 31 distinct RPC request messages; 25 declare a top-level `SessionId session_id` and ALL 25 use the `SessionId` message type rather than a bare `string`, so "declares a field of the address type" and "declares `session_id`" select the same set and the rule has no type/name split. The 6 that declare none are `CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, and `ReportPodScrubRequest`. — EVIDENCE: schemas/lenny-adapter.proto:1455-1457 (`message CoordinatorFenceRequest { SessionId session_id = 1; }`), :1173 (`message CheckpointRequest`), :1193 (`message CheckpointStart`)

FACT: §4's candidate stream-envelope predicate holds. Of the four request types of streaming RPCs (`AdapterEventsRequest`, `AttachRequest`, `CheckpointRequest`, `PrepareWorkspaceRequest`), only `CheckpointRequest` declares a `oneof` (`oneof msg` of `CheckpointStart`/`CheckpointGrant`/`CheckpointAbort`). The predicate selects it and nothing else, so §7 question 2 can be answered yes on the current proto. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187

FACT: Bringing the fence under 0073's value rule needs no code change. The rule landed at `spec/05_runtime-registry-and-pool-model.md:515` ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved"), and the fence handler already does exactly that. — EVIDENCE: pkg/adapter/coordination.go:108-111

WATCHOUT: Every line anchor in the proposal's §1.2 has drifted or gone false. `Server.coord` no longer exists (grep for `s.coord` in pkg/adapter returns nothing); `pkg/adapter/server.go:302` is now inside the `controlSink` field block; `pkg/adapter/coordination.go:84` is inside `BarrierWaiting`, and the fence handler starts at `:107`. `coordinationState` is now a per-session struct on the slot entry at `pkg/adapter/coordination.go:27-47`. The sentence "In the shipped tree that ground holds" is false. I did not file this: its remedy lands in the problem-statement file, outside this loop's edit surface, and it sits under a citations lens rather than mine. — EVIDENCE: pkg/adapter/coordination.go:27-47, :81-89, :107; pkg/adapter/server.go:295-310

FACT: The proto anchors in the proposal are one revision stale in the same direction. `CoordinatorFenceRequest` opens at `schemas/lenny-adapter.proto:1455` (not `:1447`) with `SessionId session_id = 1` at `:1456` (not `:1448`), and `message CheckpointRequest` opens at `:1173` (not `:1166`). The orchestrator's brief carries the older numbers too, so re-derive rather than trust either. — EVIDENCE: schemas/lenny-adapter.proto:1455, :1456, :1173

OPEN: SPEC-1's third clause reads "Retire 0073's recorded limit about the table-reconciliation gate and state the new gate's limit in its place", inside a block whose target is `spec/04` §4.1. That limit is not in §4.1 or anywhere in `spec/`; it is recorded only at `proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713`, in an Implemented proposal that D5 and the Impacts table say is not edited. Either "retire" means supersede-without-editing (harmless), or it names an edit no lane may make. I did not file it because both readings are available from the text; a later round should settle which is meant and say so in the block. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713, spec/04_system-components.md:149-190

FACT: `tests/tier0_static/adapter_proto_message_scope_test.go` IS the tier-0 gate file 0073's §8 introduced — commit `fb2af5f9c` added it under that proposal. §9 "Files touched on application" lists "The tier-0 gate file 0073's §8 introduces" and that path as two separate bullets, and the deliverable index does the same; they are one file. Not filed (redundancy inside a file list is on the do-not-report list), but an implementor should not go looking for a second file. — EVIDENCE: git log --diff-filter=A tests/tier0_static/adapter_proto_message_scope_test.go -> fb2af5f9c; tests/tier0_static/adapter_proto_message_scope_test.go:17-27

FACT: The retired gate's five refusal cases are missing-row, unknown-row, wrong-service, non-class-scope, and duplicate-row (`tests/tier0_static/adapter_proto_message_scope_test.go:178-202`). Only the first is what §7 question 1 weighs. The service-attribution and duplicate checks also die with the table and the derivation rule replaces neither, which a reviewer adjudicating question 1 should price alongside the classification hole. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:96-124, :178-202


### [spec.1.review-performance.1]

DECISION: Returned an empty findings list under the performance / scalability / failure-mode lens — BECAUSE the staged spec edit (SPEC-1) changes one classification word and retires a prose table; it creates no control-plane or data-plane write, no new Redis or Postgres key, no informer watch, no work-queue item, and no reconcile path, so there is no write rate to multiply against any tier's object counts. — ALTERNATIVES: I tried to construct a fence-RPC amplification argument (one fence per bound session instead of one per pod at coordinator handoff) and it fails, see FACT below.

FACT: The reclassification of `CoordinatorFenceRequest` to session-scoped is already the SHIPPED runtime behavior, so it adds no new failure mode. The handler rejects an empty identifier with `InvalidArgument` before resolving anything and then resolves the bound slot entry — EVIDENCE: pkg/adapter/coordination.go:106-116 (`sessionID := req.GetSessionId().GetValue()` / `status.Error(codes.InvalidArgument, "CoordinatorFence requires a session id")` / `st, err := s.boundSlotState(sessionID)`), and the per-session state at pkg/adapter/coordination.go:26-46.

FACT: No fence-RPC amplification is introduced at coordinator handoff. §10.1.2 already states the fence is per session and that the pod holds `last_fenced_generation` per bound session — EVIDENCE: spec/10_gateway-internals.md:38 ("Send a `CoordinatorFence(session_id, new_generation)` RPC to the pod") and spec/10_gateway-internals.md:40 ("The pod holds `last_fenced_generation` per bound session, for as long as that session is bound to it").

FACT: 0073's "§4.2 value rule" that the proposal says it leaves untouched lives at spec/05_runtime-registry-and-pool-model.md:515 ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved"), not in spec §4.2. Reclassifying the fence brings it under that rule, and the code already implements exactly that, so the rule and the reclassification agree — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515 vs pkg/adapter/coordination.go:107-109.

FACT: The §4.1 classification table has NO inbound cross-reference from anywhere else in `spec/`, `docs/`, or `schemas/`; the only external references to the section are `// spec: 4.1 (request message scope)` annotations in tests — EVIDENCE: `grep -rn "Request Message Scope|message scope" spec/ docs/ schemas/ tests/` returns only spec/04_system-components.md:149 plus test annotations at tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26 and tests/tier3_contract/rest_sessions/slot_address_absence_test.go:99. Retiring the table therefore strands no spec cross-reference.

FACT: The derivation rule's envelope clause resolves cleanly for `CheckpointStart`: it declares `SessionId session_id = 7` (schemas/lenny-adapter.proto:1217), so the envelope `CheckpointRequest` (schemas/lenny-adapter.proto:1173, doc comment opens at :1169) takes session scope through its opening frame with no third clause needed. I checked this because a rule that left `CheckpointStart` unclassified would have been a real gap; it does not.

WATCHOUT: The pod-wide hold state at spec/10_gateway-internals.md:57 ("it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state") sits against a fence that now requires a BOUND slot entry (`s.boundSlotState`, pkg/adapter/coordination.go:116). A pod in hold state with no bound entry can accept no fence and can only reach the 120s hold timeout. This is tempting to file as a failure-mode regression under this proposal and it is NOT one: it is proposal 0076's landed behavior, and 0076's OD3 explicitly weighed and rejected keeping the row pod-scoped on exactly this ground. Do not file it here. — EVIDENCE: spec/10_gateway-internals.md:57-58, pkg/adapter/coordination.go:110-118.

UNVERIFIED: Whether §7 question 1 (does the rule gate close the hole the table's gate closed) has an answer. My lens does not adjudicate it and I did not. A spec-coherence or test-coverage reviewer owns it, and the proposal itself says withdrawal is a possible outcome.

UNVERIFIED: The two `IMPLEMENTOR TO FILL THE BLANKS` banners at §4 and §5 of the spec-changes file are not the `IMPLEMENTOR'S CHOICE:` marker format and they delegate the exact wording of the specification rule, which is text the replacement tier-0 gate must parse. Outside my lens; whoever owns underspecification should decide whether it clears the bar. — EVIDENCE: 0075...spec-changes.md:46-48 and :59-61.


### [spec.1.review-reliability.1]

DECISION: Returned an empty findings list under the reliability/fault-tolerance lens — BECAUSE the staged spec edit (SPEC-1) changes only §4.1 classification prose; it adds or changes no retry, lease, dedup, drain, or store-failover mechanism, and the one recovery-adjacent consequence (a fence naming a session the pod does not hold is refused) is landed 0076 code plus an explicit non-goal here. ALTERNATIVES: filing the fence-refusal livelock (3 retries, relinquish lease, next coordinator increments and fails identically, §10.1.2 step 2 at spec/10_gateway-internals.md:39) — rejected because that behavior exists in the tree today independent of the classification word, its remedy lands in spec/10 or proposal 0080 §1.16, and 0075 §6 names the acceptance predicate a non-goal.

FACT: `Server.coord` is gone from the tree — `grep -n "coordinationState\|coord " pkg/adapter/server.go` returns nothing. `coordinationState` now lives on the slot registry entry and `CoordinatorFence` resolves it through `s.boundSlotState(sessionID)`. EVIDENCE: pkg/adapter/coordination.go:27-47, :107-119

FACT: retiring the §4.1 table creates NO contradiction with the channel cards: `CH-FENCE` already states the per-session reading ("The pod records the generation against the session the fence names ... A fence for one session does not change the generation the pod holds for another"). It is §4.1:175/:188 that are the outliers. EVIDENCE: spec/28_communication-channels.md:314-318

FACT: the declaration rationale ("declared rather than derived") appears in spec/ at exactly two sites, both already in SPEC-1's edit list. A repo-wide grep for "declared rather than derived"/"field set" over spec/, docs/, tests/, pkg/ surfaces no third normative site, and no docs page states any message's scope (docs/reference/adapter-contract.md:69 is the only fence row and carries no scope). EVIDENCE: spec/04_system-components.md:151, spec/04_system-components.md:188

FACT: `tests/claim-map.json` carries no row mentioning scope, so retiring the table orphans no §28.4 claim-register row. EVIDENCE: grep -i scope tests/claim-map.json returns nothing

FACT: the stream-envelope clause is safe against a re-opened Checkpoint stream — `CheckpointStart` is the only frame that opens the stream and it is where the stream is addressed, stated in the proto comment itself, so "the frame that opens it" always carries an address. EVIDENCE: schemas/lenny-adapter.proto:1189-1215 (CheckpointRequest oneof at :1174-1187; "the opening frame is where the stream is addressed")

DEFERRED [0075...problem-statement.md]: §1.2 line 48 says "In the shipped tree that ground holds. The handler at `pkg/adapter/coordination.go:84` reads the identifier ... and then mutates `s.coord` (`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process." That is false about the tree as of 0076 landing: `Server.coord` does not exist, coordination.go:84 is now inside `BarrierWaiting`, and the fence at coordination.go:116 resolves the session's slot entry. What is true: the ground the specification gives at spec/04:151 and :188 is now falsified by the tree, so the debt is live now rather than becoming live when this proposal applies.

DEFERRED [0075...summary.md]: the "Defects in the shipped tree that this proposal does not stage" section says "The §4.1 ground holds in the shipped tree while the fence handler still writes pod-wide state, so it becomes a defect only as the coordination generation moves onto the slot entry, which is the point at which this proposal applies." 0076 is Implemented, so the ground does not hold and both spec/04:175 and :188 are false in the shipped tree right now.

OPEN: SPEC-1 says "Retire 0073's recorded limit about the table-reconciliation gate and state the new gate's limit in its place." That limit is recorded only in 0073's own §9 Recorded limits (proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6686-6689) and in the gate file's doc comment; §4.1 carries no limit statement today. D5 forbids reopening 0073 and the project rule bars editing an implemented proposal, so the instruction names no landable target on the spec side. A later round should say where the new limit is stated.

UNVERIFIED: §9 "Files touched on application" lists "The tier-0 gate file 0073's §8 introduces" and `tests/tier0_static/adapter_proto_message_scope_test.go` as two entries; `git log --diff-filter=A` shows commit fb2af5f9c added that one file as 0073's §8 gate, so the two entries are the same file. Whoever owns bookkeeping should confirm and collapse them.

WATCHOUT: the §4.1 table is 32 rows (spec/04_system-components.md:155-186) and the proto parse yields 31 RPC request messages; the 32nd row is `CheckpointStart`, which is not any RPC's request type and which the derivation rule as worded ("a request message is session-scoped exactly when...") does not itself reach. The tier-0 gate hard-codes it (tests/tier0_static/adapter_proto_message_scope_test.go:36-39). Any replacement rule or gate has to keep reaching it.


### [spec.1.review-security.1]

FACT: The specification never names the proto message type `SessionId`. `grep -rn "\bSessionId\b" spec/*.md` filtered for the camelCase JSON keys (`sourceSessionId`, `targetSessionId`, `rootSessionId`, `childSessionId`, `{sessionId}`) returns nothing. D1's rule keys the classification on "the address type", a term with no definition in `spec/`. — EVIDENCE: schemas/lenny-adapter.proto:595 `message SessionId {`; spec/04_system-components.md:151 (the text it replaces speaks of the field name `session_id`, which the spec does use)

FACT: The classification is the predicate the mandatory fail-closed check hangs off. spec/05_runtime-registry-and-pool-model.md:515 reads "a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved". Two adapter handlers cite §4.1 for it. — EVIDENCE: pkg/adapter/checkpoint.go:74-85; pkg/adapter/coordination.go:109-111

FACT: Re-derived the proto measurement independently with a brace-depth parser. 25 RPC request messages declare a top-level `SessionId` field, 6 do not (`CheckpointRequest`, `AdapterEventsRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). `CheckpointStart` declares `SessionId session_id = 7`. Only `CoordinatorFenceRequest` changes class under the rule. The proposal's §1.1 numbers hold. — EVIDENCE: schemas/lenny-adapter.proto:1193 (CheckpointStart), :1455-1456 (CoordinatorFenceRequest)

WATCHOUT: `CheckpointStart` is a `oneof` member rather than an RPC request type, yet spec/04:151 calls the table's unit "a request message the protocol declares" and gives it row :168. Do NOT file the derivation rule as leaving `CheckpointStart` unclassified: the spec's own domain for "request message" already includes it, so the rule reaches it and gives it session scope. I chased this and it dissolved. — EVIDENCE: spec/04_system-components.md:151, :167-168

WATCHOUT: `ListPlatformToolsRequest`, `CallPlatformToolRequest`, `ListSessionConnectorsRequest`, `ListConnectorToolsRequest`, and `CallConnectorToolRequest` appear in `spec/` ONLY in the §4.1 table rows :180-184. §4.7's "Adapter → Gateway RPCs" table lists `ReportSessionScrub` and `ReportPodScrub` alone. Retiring the table deletes the spec's only mention of those five message names and of their adapter→gateway direction. I did not file it (the derivation rule still gives them session scope, and no other surface becomes false), but a later reviewer should decide whether the direction column is load-bearing anywhere. — EVIDENCE: spec/04_system-components.md:180-184, :722-726

DEFERRED [0075...problem-statement.md]: §1.2 at :48-51 states "In the shipped tree that ground holds. The handler at `pkg/adapter/coordination.go:84` reads the identifier ... and then mutates `s.coord` (`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process." That is now false about the tree. `Server.coord` does not exist; `grep -n "coord\b" pkg/adapter/server.go` returns nothing, and `coordinationState` lives on the slot registry entry read through `slotStateForSession` / `boundSlotState`. What is true instead: the ground the specification gives at spec/04:151 and :188 no longer matches the tree at all, so the debt this proposal closes is live rather than prospective, and §1.2 must say so. — EVIDENCE: pkg/adapter/coordination.go:17-47, :54-59, :107-119

DEFERRED [0075...problem-statement.md]: three proto anchors in §1.1/§1.2 have drifted. `CoordinatorFenceRequest` is at schemas/lenny-adapter.proto:1455 with `SessionId session_id = 1;` at :1456, not `:1447`/`:1448`. `CheckpointRequest` is at :1173, not `:1166`. The review-log's refreshed anchor "`SessionId` at :589" is also off; the message opens at :595.

OPEN: spec-changes SPEC-1 (:66-67) says "Retire 0073's recorded limit about the table-reconciliation gate and state the new gate's limit in its place." No recorded-limit sentence exists in `spec/04` §4.1 (read :149-190); the limit lives in proposal 0073's own §9 "Recorded limits". Editing it would contradict D5 and the landed-proposal-immutability rule. Someone must settle whether SPEC-1 means "write a new limit into §4.1" (there is nothing to replace) or "edit 0073" (forbidden). Out of the security lens, so I did not file it.


### [spec.2.fix-G1.1]

DECISION: The staged pod-scope clause in D1 now reads "One that declares no such field is pod-scoped." with the addressee gloss deleted and nothing put in its place — BECAUSE §4.1's rule states a classification, and addressee and direction are owned by §4.7.1, whose two RPC tables carry the direction headings and whose `ReportPodScrub` row already ends "The request is pod-scoped."; the deleted words were false of `ReportPodScrubRequest`, which sits on `service GatewayControl` and reaches the gateway — ALTERNATIVES: the finding's own per-direction wording, which re-imports the Direction column the derivation is retiring and makes the rule's correctness depend on the current service set; the weaker "it addresses the pod rather than a session on it", which only restates the dichotomy the two preceding sentences already establish; a cross-reference to §4.7.1, which invites a later author to inline the addressee again; narrowing the rule's population from "either service" to `service Adapter`, which would leave the seven `GatewayControl` rows unclassified.

WATCHOUT: Any addressee claim inside the scope rule has now failed once and any narrower version of it is the same defect one step weaker. Pod-scoped is fully defined by dichotomy: the preceding sentence says a top-level `session_id` field of type `SessionId` "is the only way a request on this protocol addresses a session", so pod-scoped means addressing no session, which is true of `ReportPodScrubRequest` and of the four `service Adapter` pod-scoped messages alike. Do not re-add a gloss — EVIDENCE: schemas/lenny-adapter.proto:490-503 (`ReportPodScrubRequest` carries `pod_id` only, "there is no session_id because occupancy is zero at the recycle boundary"); schemas/lenny-adapter.proto:244-246, :261, :334 (`GatewayControl` is adapter→gateway and the gateway hosts the server).

FACT: The pod-scoped class spans both directions, and the retiring table kept them apart with its Direction column, which the derivation drops. `spec/04_system-components.md:186` is the `ReportPodScrubRequest` row, direction `adapter → gateway`, class `pod`; `:188`'s enumeration of pod-scoped messages addressing the adapter process deliberately omits it. Direction survives the table's retirement in §4.7.1: `:698` heads `*Gateway → Adapter RPCs:*`, `:721` heads `*Adapter → Gateway RPCs:*`, and `:726` is the `ReportPodScrub` row — EVIDENCE: spec/04_system-components.md:186, :188, :698, :721, :726.

FACT: The gloss was introduced by this review loop rather than inherited. `git show d2fbf978d` states D1 as "A request message is session-scoped exactly when it declares a field of the address type" with no addressee clause at all. Round 1's fix made the predicate more precise and added the addressee sentence in the same edit — EVIDENCE: git show d2fbf978d -- proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md

USEFUL [spec.1.fix-design-G1.1]: its last-listed rejection records the addressee gloss as the reason for not widening the classification clause's population to cover an envelope's frames. That rejection survives the deletion on its own independent ground, that `CheckpointGrant` and `CheckpointAbort` continue an already-addressed session stream, so the envelope clause needs no rework. The log entry itself is historical and was left unedited.

FACT: No other file in the proposal carried the addressee gloss. A grep for "adapter process" and "addresses the pod" across the proposal returns only the staged blockquote and historical review-log entries, so SPEC-1 at spec-changes.md:112-123 ("with the paragraphs D1 states") needed no edit, the deliverable set is unchanged, and neither the summary's index nor the non-spec-changes file was falsified.


### [spec.2.fix-design-G1.1]

DECISION: Delete the addressee gloss from D1's staged pod-scope clause outright — `One that declares no such field is pod-scoped.` and nothing more — rather than bounding it per direction or per service. — BECAUSE §4.1's subject is the scope classification, and an addressee is direction information that §4.7.1 already owns and states twice: the two RPC tables are headed `*Gateway → Adapter RPCs:*` (spec/04_system-components.md:698) and `*Adapter → Gateway RPCs:*` (:721), and the `ReportPodScrub` row ends "The request is pod-scoped." (:726). The retiring table's Direction column was a duplicate of those headings; the rule never needed it, so nothing is lost by not restating it. — ALTERNATIVES: (a) the finding's own suggested per-direction split ("a pod-scoped request the gateway sends on `service Adapter` addresses the pod's adapter process; one the adapter sends on `service GatewayControl` reaches the gateway...") — rejected because it is the failed universal made narrower, it re-imports the Direction column the derivation deliberately drops, and it must be re-edited every time a service or a direction is added; (b) "pod-scoped: it addresses the pod rather than a session on it" — rejected as a restatement of the dichotomy the two preceding sentences already establish; (c) adding a cross-reference to §4.7.1 for the addressee — rejected because it points at a fact the rule does not state and invites the addressee claim back.

FACT: `ReportPodScrubRequest` is the one pod-scoped request on `service GatewayControl`, and the gateway is its server: the pod adapter dials `LNK-GWCONTROL` (spec/28_communication-channels.md:107), the proto says so at schemas/lenny-adapter.proto:244-248, and the message carries `pod_id` with an explicit "there is no session_id because occupancy is zero at the recycle boundary" (schemas/lenny-adapter.proto:492-503). Any sentence in §4.1 that names one addressee for the whole pod-scoped class is false of it. The current spec text avoids this by enumerating only the four `service Adapter` messages (spec/04_system-components.md:188), which is exactly the text SPEC-1 retires — so the enumeration is not a fix to restore, it is the thing being deleted.

WATCHOUT: review-log:85 rejected the "widen clause 1's population to include envelope frames" alternative partly on the ground that the gloss would be false of a continuation frame. Deleting the gloss does NOT reopen that alternative: the rejection stands on the substantive ground that `CheckpointGrant` and `CheckpointAbort` continue an already-addressed session stream, which the envelope paragraph states independently. Do not treat that log entry as a reason to keep the gloss, and do not edit it — log entries are historical. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.review-log.md:85

FACT: the gloss appears in exactly two places in the whole tree plus proposal: the staged blockquote at spec-changes.md:12-13 and the spec sentence it retires at spec/04_system-components.md:188. `grep -rn "adapter process\|addresses the pod\|address the pod" spec/ docs/ schemas/ proposals/0075*/` returns no other site that asserts an addressee for the pod-scoped class, so the fix is one clause in one file with no cascade.

MISTAKE: round 1 rewrote this same clause and introduced the defect while doing so. The last committed revision (git show d2fbf978d) had no addressee gloss at all; the gloss was added as connective tissue when the predicate was pinned to `session_id`/`SessionId`. The cost was a whole round. The lesson for a later round: when tightening the predicate, do not add a semantic gloss about what a class of messages reaches — the predicate is mechanical and needs no gloss.


### [spec.2.review-applicability.1]

FACT: the proto measurement in §1.1 reproduces exactly against the tree today: 31 RPC request messages, 25 declaring a top-level `session_id`, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Every field named `session_id` is of type `SessionId` and every field of type `SessionId` is named `session_id`, so D2's replacement gate passes on the proto as it stands and does not hard-fail on its first run — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1217 (`SessionId session_id = 7` in `CheckpointStart`), :1456 (`SessionId session_id = 1` in `CoordinatorFenceRequest`).

FACT: only two `oneof` blocks exist in the whole protocol definition, at `CheckpointRequest` and `CheckpointResponse`, so §4's claim that the structural envelope predicate selects `CheckpointRequest` alone is exact — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250; `message CheckpointResponse` at :1249 is the request type of no RPC.

FACT: every line anchor SPEC-1 uses is correct as of this round. `#### Request Message Scope` at spec/04_system-components.md:149, intro paragraph :151, table :153-186 (header at :153, last row `ReportPodScrubRequest` at :186), fence row :175, grounding paragraph :188, `ShutdownRequest` paragraph :190. The other spec-changes citations also verify: spec/05_runtime-registry-and-pool-model.md:515, pkg/adapter/checkpoint.go:74-84, tests/tier0_static/adapter_proto_parse_test.go:10-15, tests/tier0_static/adapter_proto_message_scope_test.go:17-27, tests/tier0_static/claim_register_proto_agreement_test.go:64.

WATCHOUT: `ReportPodScrubRequest` is the ONE pod-scoped request that is adapter-to-gateway. Any sentence that generalizes over pod-scoped messages has to survive it. The retired `spec/04:188` sentence carefully enumerated only the four `service Adapter` messages when it said "address the pod's adapter process"; the staged D1 blockquote drops that enumeration and states it of every message with no address, which is false of `ReportPodScrubRequest` — EVIDENCE: spec/04_system-components.md:188, :721 ("*Adapter → Gateway RPCs:*"), :726 ("The request is pod-scoped."); schemas/lenny-adapter.proto:334 (`rpc ReportPodScrub` inside `service GatewayControl`, opened at :261).

FACT: the shared tier-0 proto parse has exactly two callers, `adapter_proto_message_scope_test.go:87` and `claim_register_proto_agreement_test.go:64`, so §9's enumeration of the blast radius is complete on that axis. The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) do not call it and do not read the §4.1 table — EVIDENCE: grep for `protoFields(`/`protoServiceRequests(` over tests/.

FACT: nothing outside `tests/tier0_static/adapter_proto_message_scope_test.go`, `tests/spec-map.json`, and the tier-3 suite's doc comment reads the §4.1 classification table. No `docs/` page restates the gRPC message-scope classification, no `tests/registers/*.yaml` pins spec/04 text or line numbers (`line-citations.yaml` is `files: []`), and the eight `identifier-senses.yaml` entries for spec/04 sit far outside :151-188, so removing the block shifts no occurrence index. §1.4's "blast radius is small" holds.

DEFERRED [proposals/0075_.../0075_....non-spec-changes.md]: TEST-2 says to "delete the coverage clause at `:40-43`". The membership sentence it invalidates starts one line earlier: `session_address_wire_test.go:39-40` reads "A message is in this set when the specification's / message-scope table gives it session scope". Deleting :40-43 alone leaves ":39" ending mid-sentence on a table that no longer exists. The repair spans :39-43. Test-lane file, so this loop may not land it.

OPEN: after SPEC-1, §4.1 says the classification "is derived from the message's field set rather than declared per message", while spec/04_system-components.md:725 and :726 still declare `ReportSessionScrub`'s and `ReportPodScrub`'s request scope per message in §4.7.1 prose. I judged this a restatement rather than a competing declaration (the pre-change §4.1 said "declared in the table below" and coexisted with the same two sentences), so I did not file it. A later round may disagree.


### [spec.2.review-citations.1]

DECISION: Returned an empty findings list for the citation lens on the spec staging — BECAUSE every concrete citation in `.spec-changes.md` resolves and says what the proposal claims; I re-verified all of them line by line against the tree at `d2fbf978d` — ALTERNATIVES: filing the "only way a request addresses a session" tension in D1 paragraph 1 against the envelope paragraph (rejected: paragraph 1 restricts its subject to messages carrying no `oneof` and explicitly routes envelopes to paragraph 2, so both readings are available and a skeptic would refute it as wording); filing that §4.7.1 `:725`/`:726` still declare a scope per message after the rule says classification is "derived ... rather than declared per message" (rejected: both surviving declarations agree with the derivation, so the applied spec is not inconsistent).

FACT: Every line citation in the current `.spec-changes.md` is accurate as of 2026-09-07. Verified: `spec/04_system-components.md:149` heading, `:151` intro paragraph containing "because `session_id` appears on messages of both classes", `:153-186` the table (153 header, 154 separator, 155-186 the 32 rows), `:175` the fence row, `:188` the declaring sentence, `:190` the `ShutdownRequest` precedent paragraph; `schemas/lenny-adapter.proto:1217` `SessionId session_id = 7;` in `CheckpointStart`; `spec/05_runtime-registry-and-pool-model.md:515` the empty-identifier refusal; `pkg/adapter/checkpoint.go:74-84` enforcing it on the opening frame; `tests/tier0_static/adapter_proto_parse_test.go:10-15` the shared-parse comment; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27` the gate's header comment carrying its recorded limit at `:25-27`; `tests/tier0_static/claim_register_proto_agreement_test.go:64` `fields := protoFields(protoBody)`. — EVIDENCE: spec/04_system-components.md:149-190

FACT: D1's arithmetic is exactly right and I re-derived it mechanically. A brace-depth parse of `schemas/lenny-adapter.proto` over the two service blocks returns 31 request messages: 25 declare a top-level `SessionId session_id` (the 24 table session rows other than `CheckpointRequest`, plus `CoordinatorFenceRequest`), 5 declare none (`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`) and are exactly the table's remaining pod rows, and 1 (`CheckpointRequest`) carries the `oneof`. The table's 32 rows are those 31 plus `CheckpointStart`, which is the "remaining two rows" the envelope clause carries. — EVIDENCE: spec/04_system-components.md:155-186; schemas/lenny-adapter.proto:1173-1187

FACT: The replacement gate's two spelling clauses hold on the shipped proto, so the gate is green on its first run rather than red. No field named `session_id` carries a type other than `SessionId` and no field of type `SessionId` carries a name other than `session_id`; there are 26 such fields. The envelope clause also holds: `CheckpointRequest` declares no top-level address (only `int64 coordination_generation = 4` outside the `oneof`) and exactly one of its three frames declares one. — EVIDENCE: schemas/lenny-adapter.proto:1174-1186, :1217, :596

FACT: `CheckpointRequest` and `CheckpointResponse` are the only two `oneof` sites in the whole file (`:1174`, `:1250`), and `CheckpointResponse` is the request type of no RPC, so §4's mechanical-evaluability claim is exact. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

FACT: Nothing outside `spec/04` §4.1 references the classification table, and nothing outside `:175`/`:188` declares the fence pod-scoped. Greps for "message-scope", "Request Message Scope", "classification table", and "request-message-scope" over `spec/`, `docs/`, `schemas/`, `charts/` return only `spec/04_system-components.md:149`/`:151`. `docs/reference/adapter-contract.md:69` names `CoordinatorFence` with no scope word. §1.4's "no reader-facing page changes" therefore holds, and §9's spec-side list is complete. — EVIDENCE: docs/reference/adapter-contract.md:69

FACT: The two surviving per-message scope declarations in `spec/04` are `:725` (`ReportSessionScrub` request session-scoped) and `:726` (`ReportPodScrub` request pod-scoped), both in the §4.7.1 RPC table (heading at `:692`). SPEC-1 does not touch them and does not need to: both agree with the derivation. A round-1 finding that assumed they were the dropped pod-scope clause's data source was refuted on this evidence, and D2 has since dropped that clause entirely, so the question is closed rather than merely refuted. — EVIDENCE: spec/04_system-components.md:725-726

FACT: The §28.4 claim register lives at `tests/claim-map.json` and its obligation is scoped to "Every normative statement **this section** makes", meaning §28. The new §4.1 rule therefore needs no claim-register row, and `spec/28` is correctly absent from §9. — EVIDENCE: spec/28_communication-channels.md:163-165

WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names `tests/tier0_static/adapter_proto_message_scope_test.go` in a flat path list. TEST-1 rewrites that file rather than deleting it, so the entry stays valid and is NOT a missing edit site. Do not file it. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336

WATCHOUT: "0073's §4.2 value rule", cited throughout 0075, is a subsection of proposal 0073's own detailed design ("### 4.2 The rule"), not `spec/04` §4.2, which is the Session Manager. The rule landed in `spec/` at `spec/05_runtime-registry-and-pool-model.md:515`. Do not chase `spec/04` §4.2 looking for it. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:966-968; spec/05_runtime-registry-and-pool-model.md:515

USEFUL [orchestrator anchors]: The orchestrator's proto anchors (`SessionId` at :589, `CoordinatorFenceRequest` at :1447, `session_id` at :1448, `CheckpointRequest` at :1166) are the STALE pre-refresh set and are each off by 7. The proposal's own current anchors (:596, :1455, :1456, :1173, :1217) are correct. A round-1 finding on this drift was refuted as immaterial; the fixer re-anchored anyway, so the proposal text is now right and the orchestrator note is what is stale.

DEFERRED [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]: 0080 §2 states that "the §4.1 `ShutdownRequest` classification limit that shares its cause" is taken by proposal 0075. 0075 does not take it: SPEC-1 leaves the `ShutdownRequest` paragraph at `spec/04_system-components.md:190` unedited, and the replacement gate checks the proto's addressing convention rather than what a handler does, so 0073's recorded limit that the gate "cannot check that a declared scope covers everything the handler does with the request" survives 0075 unchanged. What is true instead: 0075 takes 0073's declared message-scope table and its reconciliation gate, and the false tier-3 coverage clause, and leaves the `ShutdownRequest` handler-versus-address limit unowned. 0080 is a Draft, so the correction lands there, not here.


### [spec.2.review-edit-sites.1]

FACT: The `#### Request Message Scope` heading and the classification table it carries are referenced from NOWHERE else in the tree. A repo-wide grep for `Request Message Scope` / `request-message-scope` returns only `spec/04_system-components.md:149` plus proposal and scratchpad files. There is no anchor link, no `docs/` restatement, and no claim-register row. §9's spec-side list (`spec/04_system-components.md` alone) is therefore complete. — EVIDENCE: spec/04_system-components.md:149; grep over spec/ docs/ schemas/ charts/ returns no other hit
FACT: `CoordinatorFenceRequest`'s reclassification pod→session touches no other authored surface. Every other mention of `CoordinatorFence` in spec/ and docs/ states behavior, not scope: spec/10:38-40,57-58, spec/07:194-195,215,222, spec/11:216, spec/18:238, spec/28:239,251,314,1689,1812, spec/29:623,820,1273,1305,1525, spec/04:712, docs/reference/adapter-contract.md:69. The proto doc comments at schemas/lenny-adapter.proto:153-167 and :1449-1461 state no scope either. — EVIDENCE: schemas/lenny-adapter.proto:1449; spec/28_communication-channels.md:314
FACT: The two surviving per-message scope declarations in spec/ after SPEC-1 applies are `spec/04_system-components.md:725` (`ReportSessionScrub` "The request is session-scoped") and `:726` (`ReportPodScrub` "The request is pod-scoped"), both in the §4.7.1 Adapter → Gateway RPC table whose header is at `:721`. Both agree with what the derivation rule computes, so neither is falsified. — EVIDENCE: spec/04_system-components.md:721,725,726
WATCHOUT: `ReportPodScrubRequest` is the ONLY pod-scoped request message that is NOT delivered to the adapter. It rides `service GatewayControl` (schemas/lenny-adapter.proto:261, rpc at :334) over `LNK-GWCONTROL`, which the pod adapter dials to the gateway (spec/28_communication-channels.md:107). The current §4.1 text at `:188` avoids the trap by naming only the four `service Adapter` pod-scoped messages when it says "address the pod's adapter process"; the staged D1 block generalizes that clause over both services and inherits the error. Any rewording of the pod-scoped half of the rule must not name a recipient, or must name one per service. — EVIDENCE: spec/04_system-components.md:188; schemas/lenny-adapter.proto:261,334; spec/28_communication-channels.md:107
FACT: Re-verified today, the proto measurement and every anchor D1/§4/§5 rest on. 31 distinct RPC request types; 25 declare a top-level `session_id`, all of type `SessionId`; the 6 that do not are `CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`. Exactly two messages declare a `oneof`: `CheckpointRequest` (schemas/lenny-adapter.proto:1174) and `CheckpointResponse` (`:1250`). Every field named `session_id` in the file is typed `SessionId` and every `SessionId`-typed field is named `session_id` (26 sites), so the replacement gate passes on the tree as it stands. `CheckpointStart` declares `SessionId session_id = 7` at `:1217`. — EVIDENCE: schemas/lenny-adapter.proto:1174,1217,1250,1455
FACT: §4.1's block anchors are exact as of today: heading `:149`, lead paragraph `:151`, table `:153-186`, fence row `:175`, grounding paragraph `:188`, `ShutdownRequest` paragraph `:190`, next heading `### 4.2` at `:192`. The table carries 26 session rows and 6 pod rows. — EVIDENCE: spec/04_system-components.md:149-192
CORRECTS [orchestrator note]: The orchestrator's brief says §1.2's account of `pkg/adapter` is a live edit site and that `lastFenced`/`initialized` sit at `pkg/adapter/coordination.go:29-36` read at `:59`. The proposal's own §1.2 citations are the correct ones and check out against the tree: `pkg/adapter/server.go:302` is `hold holdState`, `pkg/adapter/slot.go:59` is `coord coordinationState`, `pkg/adapter/coordination.go:17-38` is the type's doc comment, `:107` is `func (s *Server) CoordinatorFence`, `:116` is the `boundSlotState` call. Nothing in §1.2 needs correcting on those grounds. — EVIDENCE: pkg/adapter/slot.go:59; pkg/adapter/coordination.go:107,116
FACT: `tests/spec-map.json` is the only map that names the retiring gate (`:156`, `:169` under 4.1 and `:5670` under 28.5.3). `tests/claim-map.json` holds no row for it. — EVIDENCE: tests/spec-map.json:156,169,5670
DEFERRED [non-spec-changes.md §8 Testing]: §8 requires "at minimum the tier-0 gate must be shown to fail on a proto that violates the rule", singular, while D2's replacement gate has four independent clauses (name-without-type, type-without-name, envelope with its own address, envelope frames not exactly one address). Whether one negative case discharges four clauses is a question for the non-spec loop, not this one.


### [spec.2.review-feasibility.1]

FACT: `ReportPodScrubRequest` is the ONE pod-scoped request message that is NOT gateway-to-adapter. It sits on `service GatewayControl`, whose own proto doc comment says "the gateway hosts this gRPC server and the adapter dials it as a client", and §4.7.1 lists `ReportPodScrub` under the "*Adapter → Gateway RPCs:*" heading. It carries `string pod_id = 1` and no `session_id`, and the gateway resolves the claim from `pod_id`. Any sentence that glosses "pod-scoped" as "addresses the pod's adapter process" is therefore false of it — which is exactly why the retired §4.1 prose at `spec/04_system-components.md:188` named only the four `Adapter`-service pod rows and left `ReportPodScrubRequest` out of that gloss. — EVIDENCE: schemas/lenny-adapter.proto:244-248, :492-503; spec/04_system-components.md:188, :721, :726

FACT: The two G1 measurements re-verified independently on 2026-09-07 with a fresh parse: 31 RPC request types, 25 declaring a top-level `SessionId session_id`, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Name/type co-occurrence holds in both directions across the whole file: the only near-miss a name grep throws up is `bool mid_session = 4` at :724, which does not match `session_id`. The §4.1 table is 32 rows at `:155-186`. — EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1217, :1455-1456; spec/04_system-components.md:153-186

FACT: Nothing outside `spec/04` §4.1 declares a gateway-adapter request message's scope class except the two §4.7.1 rows at `:725` (`ReportSessionScrub` "The request is session-scoped") and `:726` (`ReportPodScrub` "The request is pod-scoped"), and the `ShutdownRequest` paragraph at `:190` that SPEC-1 keeps. Both §4.7.1 rows stay TRUE under the derivation rule, so neither is a missing edit site. `docs/reference/adapter-contract.md:69` names `CoordinatorFence` and states no scope, so the reclassification touches no reader-facing page. — EVIDENCE: spec/04_system-components.md:725, :726, :190; docs/reference/adapter-contract.md:69

FACT: The two tier-0 files 0076 landed beside the gate, `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go`, read proto doc comments and `pkg/adapter/coordination.go`'s Go doc comments. Neither parses the §4.1 table, so retiring the table does not reach them and they are correctly absent from §9. `protoServiceRequests` has exactly ONE caller (the message-scope gate); only `protoFields` has a second one. — EVIDENCE: tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:17-27; tests/tier0_static/adapter_proto_generation_scope_test.go:11-25; tests/tier0_static/adapter_proto_parse_test.go:64-68

WATCHOUT: The snapshot at `scratchpad/cp-snap/.../spec-r2` is byte-identical to the current proposal (`diff -rq` returns nothing), so the "read the changed sections hardest" instruction had no hunks to point at this round. Do not read that as "nothing changed since round 1"; the r1 fix text IS the whole staged D1 block. Compare against `git show d2fbf978d:` instead, which is the last committed revision and predates every fix round. — EVIDENCE: git show d2fbf978d:proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md

MISTAKE: the r1 fix round rewrote D1's terse original ("A request message is session-scoped exactly when it declares a field of the address type") into a fuller prose block and, in doing so, imported the addressee gloss from the retired `:188` sentence while widening its population from the four `Adapter`-service messages to every message either service declares. The widening is the defect: `spec-changes.md:12-13` now asserts something false of `ReportPodScrubRequest`. Expanding a sentence's population is the failure mode to watch for when a fixer turns a one-line rule into paragraph prose.


### [spec.2.review-fresh.1]

FACT: `diff -ru` between the round-2 snapshot (`scratchpad/cp-snap/.../spec-r2`) and the live proposal directory returns NOTHING. The proposal text is byte-identical to the previous round's snapshot, so "read the changed sections hardest" had no target this round and the whole document had to be re-read cold — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2

FACT: `Adapter` and `GatewayControl` are named inside §4.1 ONLY by the table rows SPEC-1 deletes. In the whole of `spec/04_system-components.md` the string `GatewayControl` occurs at :180-:186 (table rows), :550 (§4.6), and :719 (§4.7) and nowhere else. So after SPEC-1 applies, the staged rule's phrase "A request message either service declares" has no antecedent anywhere in §4.1 — EVIDENCE: spec/04_system-components.md:180-186, :550, :719

FACT: `ReportPodScrubRequest` is an **adapter → gateway** request (`service GatewayControl`), declares no `session_id`, and §4.7.1 already says "The request is pod-scoped." The staged D1 clause "One that declares no such field is pod-scoped and addresses the pod's adapter process" therefore asserts something false of it. The text it replaces avoided this by ENUMERATING only the four `service Adapter` pod messages rather than generalising — EVIDENCE: spec/04_system-components.md:188 (the enumerating sentence), :720 ("*Adapter → Gateway RPCs:*"), :726 ("The request is pod-scoped.")

FACT: Re-ran the proto measurement with a brace-counting parser. 31 RPC request types across the two service blocks; 6 declare no top-level `SessionId session_id` (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); `CheckpointRequest` and `CheckpointResponse` are the only messages with a `oneof` and only the first is a request type; ZERO fields named `session_id` of another type and ZERO fields of type `SessionId` under another name, at any depth. The replacement gate would pass on the proto as it stands — EVIDENCE: schemas/lenny-adapter.proto:596, :1173-1177, :1217, :1455-1456

FACT: A naive `^message X {` + brace parser under-counts: 7 messages are declared and closed on one line (`message ReportPodScrubResponse {}` and six siblings). Any parser written for this proto has to handle that, and the shared tier-0 parse already does — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:44-48

FACT: Nothing outside `spec/04_system-components.md` §4.1 reads or restates the message-scope table. `grep -rn "Request Message Scope\|message-scope"` over `spec/`, `docs/`, `tests/`, `scripts/`, `cmd/` returns only the heading itself and the tier-0 gate file. `tests/claim-map.json` carries no row for it. The tier-11 pointer checks name their reducing sections explicitly, and §4.1 is not one, so retiring the table creates no successor-pointer obligation. §9's file list is complete on the spec side — EVIDENCE: spec/04_system-components.md:149; tests/tier11_docs/successor_pointer_test.go:34-39

FACT: Every code and proto anchor in §1.2 and §4 now resolves exactly as cited: `pkg/adapter/server.go:302` (`hold holdState`), `pkg/adapter/slot.go:59` (`coord coordinationState`), `pkg/adapter/coordination.go:17-38`, `:107` (`func (s *Server) CoordinatorFence`), `:116` (`s.boundSlotState`), `pkg/adapter/checkpoint.go:74-84` (empty-address refusal on the opening frame), `spec/05_runtime-registry-and-pool-model.md:515`, `schemas/lenny-adapter.proto:596/:1217/:1455/:1456`. The earlier rounds' anchor-drift complaints are fully discharged; do not spend time re-deriving these — EVIDENCE: pkg/adapter/coordination.go:107, pkg/adapter/checkpoint.go:74

WATCHOUT: `spec/28_communication-channels.md:314-318` already states the fence's session semantics ("A fence for one session does not change the generation the pod holds for another"). So `spec/04:175` and `:188` are contradicted by `spec/28` as well as by the tree. Whatever happens to the table retirement, the §4.1 reclassification has a second independent falsifier — EVIDENCE: spec/28_communication-channels.md:314-318

DEFERRED [tests/tier3_contract/adapter_session_address/session_address_wire_test.go]: TEST-2 deletes only the coverage clause at `:40-43`, but the sentence immediately above it — "A message is in this set when the specification's message-scope table gives it session scope" (`:39-40`) — states a membership rule keyed on a table SPEC-1 deletes. After SPEC-1 the map's own doc comment defines membership by reference to a document that no longer exists. The remedy is a test-file edit, so this loop cannot land it; the non-spec loop should restate the membership rule as the derivation rule.

OPEN: the staged block uses positional self-references ("classified by the paragraph below instead", "the first paragraph is what forbids it"). Those are stable only while the three paragraphs stay adjacent and in order. Not filed as a finding, but a later editor inserting a paragraph silently breaks both.


### [spec.2.review-mechanism.1]

DECISION: Returned an empty findings list for the mechanism lens on the r2 spec staging — BECAUSE every flow the staged text describes was traced end to end against the proto, spec/04, spec/05, spec/10, spec/28 and the tier-0/3/11 test surfaces, and each one holds — ALTERNATIVES: filing the three near-misses recorded below, each of which dissolves on close reading.

FACT: The derivation reproduces the retired table exactly, and this is now mechanically established rather than asserted. A brace-matching parse of the two service blocks gives 31 RPC request types; stripping nested blocks (oneof, nested message, enum) from each message body and testing for `SessionId session_id =` at top level gives 25 with the address and 6 without (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). The §4.1 table's 32 rows are 25 session (incl. `CoordinatorFenceRequest`, which moves) + 5 pod + `CheckpointRequest` + `CheckpointStart`. No row is lost. — EVIDENCE: spec/04_system-components.md:155-186; schemas/lenny-adapter.proto (services at :32 and :261)

FACT: The replacement gate's spelling clauses pass on today's proto in BOTH directions and would have failed nothing on day one. Every `session_id` occurrence outside a comment is `SessionId session_id`, and every `SessionId`-typed field is named `session_id`; `grep -n "SessionId " | grep -v session_id` returns only the type declaration itself. — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), the 26 `SessionId session_id =` declarations

FACT: Retiring the table breaks no inbound surface. No `§4.1` cross-reference in spec/ points at the message-scope block (all resolve to `#41-edge-gateway-replicas` for gateway subsystem extraction), no docs page restates the classification (`docs/reference/adapter-contract.md` lists RPCs only), and the only test that PARSES the table is the gate itself. Every other `4.1 (message scope)` hit in tests/ is a `// spec:` annotation, which survives because §4.1 still states the scope rule. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:31; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

FACT: §4.7.1 carries the only other per-message scope declarations in spec/, and both agree with the derivation after SPEC-1: `ReportSessionScrub` "The request is session-scoped" (the message declares `SessionId session_id = 2`) and `ReportPodScrub` "The request is pod-scoped" (declares `pod_id` alone). They need no edit, and a derived §4.1 does not contradict a section restating a derived fact. — EVIDENCE: spec/04_system-components.md:725, :726; schemas/lenny-adapter.proto:458, :496

FACT: The reclassification needs no proto edit, because 0076 already rewrote the fence's doc comment per session ("The pod records the generation against the session the fence names"), and `tests/tier0_static/adapter_proto_generation_scope_test.go` pins that wording against the pod-wide spellings. Neither reads spec/04 §4.1, so neither belongs in §9. — EVIDENCE: schemas/lenny-adapter.proto:1448-1456; tests/tier0_static/adapter_proto_generation_scope_test.go:57-70

WATCHOUT: three plausible-looking findings that do NOT hold, each of which costs an hour to derive and refute.
(1) "Paragraph 1 says a top-level `session_id` is the only way a request addresses a session, and paragraph 2 addresses the envelope through a frame instead." It reconciles: the frame's own address IS a top-level `SessionId session_id` (schemas/lenny-adapter.proto:1217), so the spelling claim holds and only the carrier moves.
(2) "The gate's blind spot is wider than the constraint paragraph says, because a conventionally-spelled `session_id` nested under a wrapper message is invisible to it." It is not wider: the wrapper field is itself a top-level field whose name and type are both unconventional, so the case is an instance of the stated blind spot.
(3) "§3 states the replacement gate with two conjuncts where D1 and D2 state three (it drops 'an envelope declares no address of its own')." §3 is a design-overview summary and no applied text depends on it; the summary carries all three.

FACT: The three-conjunct gate is falsifiable today rather than tautological, and the envelope arm has a real negative case: `CheckpointStart` declares the address and `CheckpointGrant`/`CheckpointAbort` declare none, so "exactly one" holds and both "zero" and "two" are constructible in a fixture. — EVIDENCE: schemas/lenny-adapter.proto:1217 (`SessionId session_id = 7`), :1222-1240

OPEN: D5 still reads "This proposal is inert until 0073 is applied" while 0073 is Implemented, and §7 question 1's withdrawal branch is still live. Both were left as found: neither makes an applied edit wrong, so neither is a mechanism finding, and question 1 is for the human reviewer.


### [spec.2.review-security.1]

FACT: The addressing convention the whole proposal rests on holds today with no exception. `grep -nE "^\s*(repeated\s+)?SessionId\s+" schemas/lenny-adapter.proto` returns 26 field declarations and every one is named `session_id`; no field named `session_id` carries another type. The replacement gate of D2 therefore passes on the tree as it stands. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674
FACT: The fail-closed refusal the derived class hangs off is spec/05_runtime-registry-and-pool-model.md:515 ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved"). The reclassification of `CoordinatorFenceRequest` to session-scoped therefore ADDS an obligation rather than removing one, and the landed handler already meets it. — EVIDENCE: pkg/adapter/coordination.go:108-111 (`sessionID == ""` → InvalidArgument), pinned by pkg/adapter/coordination_test.go:33-43 `TestCoordinatorFenceRejectsMissingSessionID`
FACT: Retiring the §4.1 table leaves no dangling reader. Nothing under spec/, docs/, charts/, or schemas/ references the table or the `#### Request Message Scope` heading except the retiring gate itself, and the two tier-0 siblings 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) read proto and Go doc comments, never the §4.1 table. `spec/28_communication-channels.md:307-319` (CH-FENCE) and `spec/04_system-components.md:692-727` (§4.7.1 RPC rows) declare no scope for `CoordinatorFence`. So §9's edit list is complete on the spec side. — EVIDENCE: spec/28_communication-channels.md:314-317, spec/04_system-components.md:712
FACT: Two per-message scope declarations SURVIVE SPEC-1 in §4.7.1 and both agree with the derived class, so they are not an edit site: `ReportSessionScrub` "The request is session-scoped" and `ReportPodScrub` "The request is pod-scoped". A tier-11 gate pins the first one's wording. — EVIDENCE: spec/04_system-components.md:725-726, tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67
WATCHOUT: `ReportPodScrubRequest` is the trap in the staged D1 block. It is the only pod-scoped survivor on `service GatewayControl`, so it travels adapter → gateway and the GATEWAY serves it. Any sentence that generalizes over "a pod-scoped request" and then says what it addresses or who handles it must exclude it, which is exactly why the shipped §4.1 sentence enumerates only the four `service Adapter` messages instead of quantifying. — EVIDENCE: spec/04_system-components.md:188, schemas/lenny-adapter.proto:492-503
DECISION: I did NOT file the "nested address" hole (a request declaring `Foo foo = 1` where `Foo` carries a conventional `SessionId session_id`, deriving pod-scoped and escaping the gate) — BECAUSE the staged constraint sentence "a session addressed under a name and a type that are both unconventional" reads onto it: at the request's own top level the address-bearing field carries neither the conventional name nor the conventional type, so the sentence is defensible as written — ALTERNATIVES: filing it as a false completeness claim about the gate's reach, rejected under the standing default that uncertainty resolves to refuted.
DECISION: I did NOT file the retirement of the tier-0 table gate as a removed mandatory control — BECAUSE the proposal removes it openly and §7 question 1 is the human's open decision on whether the residual is acceptable, which this review may not adjudicate.


### [spec.3.review-applicability.1]

DECISION: Returned an EMPTY findings list for the applicability/sequencing lens on the spec staging — BECAUSE SPEC-1 is the proposal's only spec deliverable, its target region is anchored by three exact line ranges that all resolve today, its replacement text is quoted verbatim in D1 (nothing to invent), it creates no heading, anchor, identifier, or register another edit needs, and it introduces no forward reference. Every line citation in `.spec-changes.md` re-verified against the tree at this commit. — ALTERNATIVES: three candidates I derived and rejected, each recorded below with the reason.

FACT: 0073's SPEC-7 block is LARGER than SPEC-1's appositive says. SPEC-1 reads "replace the whole block 0073's SPEC-7 stages, which is the paragraph introducing the declared table (`:151`), the table itself (`:153-186`), and the paragraph grounding the fence's classification (`:188`)" (spec-changes.md:111-113). 0073 §4.1 also stages the `ShutdownRequest` paragraph that landed at `spec/04_system-components.md:190` (proposals/0073_...:967, "`ShutdownRequest` is session-scoped and carries one address. Under D1 and D5 ..."), so the appositive under-describes SPEC-7's extent. NOT FILED: the same sentence pair carves `:190` out explicitly ("The `ShutdownRequest` paragraph at `:190` stands unedited", spec-changes.md:116), so the applied edit is unambiguous and three neighbouring wording-precision findings on SPEC-1 were already refuted in rounds 1-2. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:967; proposals/0075_.../0075_....spec-changes.md:111-118

FACT: the reclassification pod→session imports `spec/05_runtime-registry-and-pool-model.md:515`'s value rule onto `CoordinatorFenceRequest`, and the shipped handler ALREADY satisfies it: `pkg/adapter/coordination.go:108-111` reads `req.GetSessionId().GetValue()` and returns `InvalidArgument` on the empty string before `boundSlotState` at `:116`. So §3's "No proto or handler changes" and §1.4's "no handler ... changes" hold, and SPEC-1 stages no unstaged code obligation. I checked this specifically because a scope reclassification is the classic way a spec-only proposal acquires a code deliverable. — EVIDENCE: pkg/adapter/coordination.go:109-111; spec/05_runtime-registry-and-pool-model.md:515

FACT: §4.7.1's two RPC tables are NOT a complete enumeration of the protocol's RPCs. Gateway→Adapter (`spec/04_system-components.md:700-719`) omits `NegotiateVersion`, `GetObservedIntegrationLevel`, and `AdapterEvents`; Adapter→Gateway (`:723-726`) omits `ListPlatformTools`, `CallPlatformTool`, `ListSessionConnectors`, `ListConnectorTools`, and `CallConnectorTool`. The retiring §4.1 table's `Service` and `Direction` columns are therefore the only place in `spec/` that states the service and direction for those eight request messages, and SPEC-1 deletes them without restating them. NOT FILED as class-3 content loss: the proposal frames the table as a retired declaration replaced by a derivation rather than as a relocation, nothing in the applied spec becomes false, direction is recoverable from the protocol definition, and the closely related "SPEC-1 deletes the only text in §4.1 that names the two services" was already refuted. A later reviewer reaching for this should know it is the third variant of a refuted class. — EVIDENCE: spec/04_system-components.md:700-719, :723-726; :155-186

FACT: no gate outside the retiring tier-0 gate reads §4.1's block, so SPEC-1's blast radius on the register/gate surface is nil. `tests/registers/line-citations.yaml` is `files: []`; the eight `identifier-senses.yaml` entries for `spec/04` are retired-channel-spelling occurrences and none sits in `:151-188`; `pinned-spec-literals.yaml` names no `spec/04` carrier; `anchor-senses.yaml` names no `spec/04` entry; SPEC-1 leaves the `#### Request Message Scope` heading intact so no anchor-redirect row is owed. — EVIDENCE: tests/registers/line-citations.yaml:15; tests/registers/identifier-senses.yaml:13-36

FACT: the two surviving per-message scope declarations after SPEC-1 (`spec/04_system-components.md:725`, `:726`) both agree with what the derivation computes: `ReportSessionScrubRequest` declares `SessionId session_id = 2` (schemas/lenny-adapter.proto:458) and `ReportPodScrubRequest` declares `string pod_id = 1` and no address (`:499-503`). I re-derived both rather than trusting the round-2 log. — EVIDENCE: schemas/lenny-adapter.proto:456-467, :499-503

USEFUL [spec.2.review-citations.1]: its inventory of every `.spec-changes.md` citation saved me from re-deriving the proto measurement from scratch; I spot-checked five of its claims (`:1217`, `:1455-1456`, `:596`, the 26 `SessionId session_id` sites, the two `oneof` sites at `:1174`/`:1250`) and all five held.

CORRECTS [orchestrator note]: the orchestrator's brief still says `§1.2` states "in the shipped tree the ground for the declared classification holds" and calls that a live edit site. That sentence is GONE: §1.2 now reads "That ground held until proposal 0076's CODE-1 landed, and 0076 is Implemented" (problem-statement.md:57) and its `pkg/adapter` anchors all resolve. The brief's proto anchors (`:589`, `:1447`, `:1448`, `:1166`) are also the stale pre-refresh set, each off by seven from the tree. Do not re-file either.

WATCHOUT: the r3 snapshot at `scratchpad/cp-snap/.../spec-r3` is byte-identical to the live proposal, so `diff` against it returns nothing. The one edit this round is visible only against `spec-r2`: D1's pod-scope sentence lost its addressee gloss, going from "One that declares no such field is pod-scoped and addresses the pod's adapter process." to "One that declares no such field is pod-scoped." Diff against `spec-r2`, not `spec-r3`. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:12-13

DEFERRED [0075_....implementation-checklist.md]: unchanged from round 1 and still unclosed. S1 lands SPEC-1 and carries "Tiers 0, 11", but deleting the table at `spec/04:153-186` leaves `parseMessageScopeTable` with zero rows, so `messageScopeDisagreements` reports "the table carries no row for it" for all 32 in-scope messages and tier 0 is red from the end of S1 until S2 replaces the gate. Either S1 and S2 merge, or S1's line records the disposition. The remedy is in the checklist, which this loop may not edit and whose drift the brief declares out of scope. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:54-70, :96-104


### [spec.3.review-edit-sites.1]

DECISION: Returned an empty findings list under the edit-site-completeness lens — BECAUSE an exhaustive sweep of `spec/`, `docs/`, `schemas/`, and `charts/` for every identifier SPEC-1 removes (the §4.1 classification table, its 32 rows, the `CheckpointStart` row, the "declared rather than derived" prose, the "pod-scoped"/"session-scoped" class words, `CoordinatorFenceRequest`'s class) found no surface outside `spec/04_system-components.md` that becomes wrong — ALTERNATIVES: filing the §4.7.1 tension (see FACT below) and the "top-level" ambiguity in the staged clause 1 (see WATCHOUT below), both weighed and dropped as below the bar.

FACT: Nothing outside `spec/04_system-components.md` declares a gateway-adapter request message's scope except two §4.7.1 RPC-table cells, and both still agree with the derivation after SPEC-1 applies: `:725` "The request is session-scoped" for `ReportSessionScrub` (its request declares `SessionId session_id = 2`) and `:726` "The request is pod-scoped" for `ReportPodScrub` (its request declares no `session_id` at all). Neither is an edit site. — EVIDENCE: spec/04_system-components.md:725, :726; schemas/lenny-adapter.proto:458, :496-503

FACT: No inbound reference to the retired table exists anywhere. Every `§4.1` / `Section 4.1` citation in `spec/` and `docs/` is about gateway subsystem extraction, HPA metric roles, or the Upload Handler; none points at the message-scope block. `docs/reference/adapter-contract.md` mirrors §4.7's RPC tables and carries no scope classification for the fence (`:69` names `CoordinatorFence` with no scope word). No `charts/` file mentions message scope. — EVIDENCE: spec/04_system-components.md:149-190; docs/reference/adapter-contract.md:69, :81

FACT: The derivation reproduces the post-OD3 table exactly, machine-checked today. A parse of the two service blocks returns 31 distinct request types; 25 declare a top-level `SessionId session_id` (the fence among them) and 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). 25 session + 2 envelope rows (`CheckpointRequest`, `CheckpointStart`) + 5 pod rows = the table's 32 rows. — EVIDENCE: spec/04_system-components.md:153-186; schemas/lenny-adapter.proto (whole file)

FACT: D2's gate passes on the tree as it stands and is not vacuous. Every one of the 26 `SessionId`-typed fields in `schemas/lenny-adapter.proto` is named `session_id`, and every field named `session_id` is of type `SessionId` (the only other `session_id` occurrences in the file are doc-comment prose). `CheckpointRequest` and `CheckpointResponse` are the only messages declaring a `oneof`; only the first is an RPC request type; its three frames declare exactly one address (`CheckpointStart`'s `SessionId session_id = 7`). — EVIDENCE: schemas/lenny-adapter.proto:1174, :1217, :1250

FACT: The staged rule imposes no new behavioural obligation the tree does not already meet. Reclassifying the fence session-scoped extends 0073's value rule (`spec/05_runtime-registry-and-pool-model.md:515`) to `CoordinatorFenceRequest`, and `CoordinatorFence` already refuses an empty identifier with `InvalidArgument` before resolving the entry. So §6's "No proto or handler changes" survives the reclassification. — EVIDENCE: pkg/adapter/coordination.go:107-111

FACT: No claim-register or artifact-register row depends on the retired table. `tests/claim-map.json` (the §28.4 register) contains no message-scope or §4.1 row, and §28.7's wire-contract artifact register rows for `schemas/lenny-adapter.proto` cite §15.4 rather than §4.1. — EVIDENCE: spec/28_communication-channels.md:161-175, :1781

FACT: `tests/tier0_static/address_rule_citation_test.go` requires the sentence "rejected at the adapter boundary with `InvalidArgument`" to appear under exactly ONE numbered spec heading, and fails if a second section states it. D1's staged block does not restate that sentence, so it is safe as written; a future fixer who folds the value rule's wording into §4.1 would turn tier 0 red. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22, :84-88

WATCHOUT: Clause 1 of the staged block reads "...exactly when it declares a top-level `session_id` field of type `SessionId`, which is the only way a request on this protocol addresses a session." Read with "top-level" inside the scope of "which", `CheckpointRequest` is a counterexample: paragraph 2 says the envelope "declares no address of its own" and takes its frame's. Paragraph 3 disambiguates ("a session addressed under a name and a type that are both unconventional"), so the intended referent is the name-and-type spelling rather than the field's location, and the gate D2 states reads it that way. I judged it below the bar; do not re-open it as a correctness defect, but do not let a later rewrite drop paragraph 3's disambiguation. — EVIDENCE: 0075...spec-changes.md:10-11, :20-25

FACT: `diff -ru` between the round-3 snapshot at `scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3` and the live proposal directory is EMPTY. The "read the changed sections hardest" reading order had no changed sections to point at this round. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3

USEFUL [spec.1.fix-design-G1.1]: its two facts (the `session_id`/`SessionId` co-occurrence in both directions, and `CheckpointRequest` being the only request-typed `oneof`) are the load-bearing premises of the staged gate. I re-derived both programmatically rather than trusting them, and both held, which is what let me close the gate-feasibility half of this lens in one pass.


### [spec.3.review-feasibility.1]

DECISION: Returned an empty findings list for the actor-action feasibility lens — BECAUSE every citation in `spec-changes.md` (§2 D1/D3, §4, §5 SPEC-1, §7, §9, §10) and in `problem-statement.md` §1.2/§1.3/§4 re-resolved correctly today, and every action the staged text assigns to the tier-0 gate is derivable from `schemas/lenny-adapter.proto` text alone — ALTERNATIVES: filing the "spec/ names a test tier" placement point (spec/ mentions TESTING.md at `spec/28_communication-channels.md:27`, so there is no bar), and filing the surviving §4.7.1 per-message scope declarations as contradicting "derived rather than declared per message" (they agree with the derivation, so nothing becomes false).

FACT: The shared tier-0 proto parse has exactly two callers today, and neither of the two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) touches it. `grep -rn "protoFields\|protoServiceRequests\|adapterProtoPath\|braceDelta" tests/ --include=*.go` returns only `adapter_proto_message_scope_test.go` and `claim_register_proto_agreement_test.go`. §9's "the parse's other caller (`:64`)" is therefore exact, not stale — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:64

FACT: `protoFields` already records oneof arms as fields of the enclosing message (the `protoField` regex matches inside a `oneof` and `braceDelta` keeps depth > 0), so the parse extension TEST-1 needs is top-level-vs-oneof *membership* and field *type*, not arm discovery — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:23-25, :36-62

FACT: Re-ran the proto measurement with a top-level-only field parse: 31 RPC request messages, 25 declaring a top-level `SessionId session_id`, 6 not (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`). No field named `session_id` carries another type and no field of type `SessionId` carries another name. Only two messages declare a `oneof`, at :1174 and :1250, and `CheckpointResponse` is the request type of no RPC — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250, :1217, :1455-1456, :596

FACT: All four `pkg/adapter` anchors in §1.2 are exact after 0076: `hold holdState` at server.go:302, `coord coordinationState` at slot.go:59, the `coordinationState` doc block opening at coordination.go:17, the handler at coordination.go:107, `boundSlotState` at :116. An earlier round's correction of §1.2 landed cleanly — EVIDENCE: pkg/adapter/server.go:302, pkg/adapter/slot.go:59, pkg/adapter/coordination.go:107

FACT: Nothing outside `spec/04_system-components.md:149-190` references the §4.1 classification table. No markdown anchor links to `#request-message-scope`, and no docs page declares `CoordinatorFence`'s scope (`docs/reference/adapter-contract.md:69` names the RPC's purpose only). `spec/04:725-726` do declare `ReportSessionScrub`/`ReportPodScrub` request scope per message, and both agree with the derivation, so they are not an edit site — EVIDENCE: spec/04_system-components.md:725, :726

FACT: `spec/28_communication-channels.md:307-318` (`CH-FENCE` card) already describes the fence per session ("records the generation against the session the fence names"), so the reclassification needs no §28 edit — EVIDENCE: spec/28_communication-channels.md:314-316

DEFERRED [0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md]: TEST-2 says to add `CoordinatorFenceRequest` to `sessionScopedMessages`, and §7 question 2 frames the only open point as what the retired-field-number column holds. That framing is incomplete in a way no answer to it can repair. `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates the same map and asserts BOTH `reservesNumber(md, num)` AND `reservesName(md, "slot_id")` (tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141). `CoordinatorFenceRequest` declares no `reserved` number and no `reserved "slot_id"` at all (schemas/lenny-adapter.proto:1455-1461), so the name half fails whatever number is chosen. The map's real membership rule is also narrower than its comment claims: `ExportPathsRequest` and `ConfigureWorkspaceRequest` are session-scoped in the table and excluded precisely because they never carried the duplicate (`:38-43`). What is true instead: adding the fence to that map requires either splitting the map into "session-scoped" and "carried the retired duplicate" sets, or adding the fence to a different arm. I did not file it: its remedy is in the non-spec staging, and §7 is an open-decisions section this lens may not file on framing for.

WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names `tests/tier0_static/adapter_proto_message_scope_test.go` in a file-path inventory. It is a path list, not a test-name list, and TEST-1 rewrites the gate in place rather than deleting the file, so it is NOT an edit site. Do not add it to §9 — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336

WATCHOUT: `tests/tier0_static/address_rule_citation_test.go:44-46` demands that `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` be credited to the section carrying the sentence "rejected at the adapter boundary with `InvalidArgument`", which is `spec/05_runtime-registry-and-pool-model.md:515` rather than §4.1. Retiring the §4.1 table does not move that sentence, so this gate is untouched — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22, :44-46

USEFUL [the round-2 refutation of "the pod-scope clause has no input"]: it named `spec/04_system-components.md:726` as a surviving per-message pod-scope declaration. That is still the only one, and checking it is what let me rule out an inconsistency between the staged "derived rather than declared per message" sentence and the §4.7.1 rows.


### [spec.3.review-fresh.1]

DECISION: Returned an EMPTY findings list for the fresh-holistic lens on the r3 spec staging — BECAUSE the only change since r2 was the deletion of the addressee gloss ("and addresses the pod's adapter process") from D1 paragraph 1, and I re-derived the whole staged block from the tree independently rather than trusting r2's shards: the derivation reproduces the retired table row for row, the replacement gate passes on today's proto and is falsifiable, and no spec/, docs/, schemas/, or charts/ surface outside `spec/04_system-components.md` §4.1 reads the table — ALTERNATIVES: the "only way a request addresses a session" tension (already declined twice, see [spec.2.review-citations.1] and [spec.2.review-mechanism.1] WATCHOUT (1)); the §4.7.1 `:725`/`:726` per-message declarations surviving a rule that says classification is "derived ... rather than declared per message" (both agree with the derivation, so nothing is inconsistent).

USEFUL [spec.2.review-mechanism.1]: its WATCHOUT list of three near-misses is accurate and saved me from filing the "only way" tension, which I had independently derived and was close to filing. Anyone tempted by it: the frame's own address at `schemas/lenny-adapter.proto:1217` IS `SessionId session_id`, so only the carrier moves, not the spelling.

USEFUL [spec.2.review-fresh.1]: its enumeration of "nothing outside §4.1 reads the table" is correct; I re-ran it a different way (grep for `pod-scoped|session-scoped` across all of `spec/` and `docs/`) and reached the same set.

FACT: The `diff -ru` between `scratchpad/cp-snap/.../spec-r3` and the live directory is EMPTY (the r3 snapshot is taken at round start). To see what the r2 fix stage changed, diff against `spec-r2` instead. The whole delta this round is one hunk in `spec-changes.md` D1 paragraph 1 — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2 vs proposals/.../0075_....spec-changes.md:9-13

FACT: The staged rule and the gate close over `AdapterEventsRequest` without a hole, but not for the obvious reason. It declares `bytes envelope_json = 1` alone (`schemas/lenny-adapter.proto:1769-1771`) and the session it concerns travels inside that JSON (`AdapterTerminating(session_id, ...)`, `spec/10_gateway-internals.md:58`). It derives pod-scoped, which is what the retired table said, so nothing regresses — but a reviewer looking for a counterexample to "a top-level `session_id` field of type `SessionId` ... is the only way a request on this protocol addresses a session" will find this first. It is not one: the stream is pod-wide and the event names a session as payload data rather than addressing one. Do not spend a round on it — EVIDENCE: schemas/lenny-adapter.proto:1769-1771; spec/04_system-components.md:179 (the retired pod row); spec/10_gateway-internals.md:58

FACT: `tests/tier11_docs/successor_pointer_test.go` cannot fire on this retirement. Its domain is a hard-coded three-row list (`spec/04` §4.7, `spec/15` §15.4, `spec/29` §29.10, at `:53-57`) and §4.1 is not in it; the check is also about content that MOVED to §28.5, while SPEC-1 retires content outright and relocates no identifier. N8's successor-pointer obligation therefore does not attach — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:34-39, :53-57

FACT: `tests/claim-map.json` carries exactly one row naming the fence (`"claim": "\`CoordinatorFence\`"`, `spec_anchor: "#2851-gateway-to-pod"`, surface `pkg/adapter/coordination.go:85`) and no row anchored on §4.1 or on the message-scope table. Retiring the table creates no claim-register obligation. Note in passing that the row's surface anchor `:85` is stale (the handler is at `:107` after 0076) — that is a 0076 residue, out of this proposal's scope and out of this loop's — EVIDENCE: tests/claim-map.json (CoordinatorFence row); pkg/adapter/coordination.go:107

FACT: A second, independent falsifier of `spec/04:175`/`:188` beyond the tree: `spec/28_communication-channels.md:314-317` (CH-FENCE card) already states "The pod records the generation against the session the fence names ... A fence for one session does not change the generation the pod holds for another." So even under §7 question 1's withdrawal branch, the §4.1 reclassification is compelled by spec-vs-spec contradiction and not only by spec-vs-code — EVIDENCE: spec/28_communication-channels.md:314-317; spec/04_system-components.md:175, :188

DEFERRED [tests/tier0_static/adapter_proto_parse_test.go]: the shared parse's own header comment says one of its two callers "joins the §4.1 message-scope classification table to the request messages the two services declare" (`:10-15`). After SPEC-1 there is no table to join. The comment must be restated as the addressing-convention gate the rule rests on. TEST-1 already stages an extension of this file for the type-aware and oneof-aware parse, so the comment rewrite rides along, but neither `spec-changes.md` §5 nor `non-spec-changes.md` TEST-1 names it. The remedy is a test-file edit, so this loop cannot land it.


### [spec.3.review-security.1]

DECISION: Returned an empty findings list under the security lens — BECAUSE the reclassification is
strictly additive on the fail-closed side and no established control is silently removed: making
`CoordinatorFenceRequest` session-scoped ADDS the empty-identifier refusal of the value rule at
spec/05_runtime-registry-and-pool-model.md:515, and the landed handler already meets it
(pkg/adapter/coordination.go rejects an empty `sessionID` with `InvalidArgument`). The one control the
proposal does remove, 0073's tier-0 table gate, is removed openly and is the subject of §7 question 1,
which is a human's open decision this review may not adjudicate — ALTERNATIVES: filing the gate
retirement as a removed mandatory control, rejected on the "silently" clause; filing the nested-address
hole, rejected as covered by the staged sentence (see USEFUL below).

FACT: Re-ran the proto census with a brace-depth parser at HEAD cdcd7e9e. The two service blocks declare
31 distinct RPC request types; 25 declare a top-level `SessionId session_id`, 6 do not
(`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`,
`NegotiateVersionRequest`, `ReportPodScrubRequest`). Exactly one request message carries a `oneof`
(`CheckpointRequest`). Critically for the security question: the derived classes are a SUPERSET of the
table's session class — every one of the table's 26 session rows stays session-scoped and only
`CoordinatorFenceRequest` moves pod→session. Nothing moves session→pod, so the derivation opens no
fail-open direction on the current proto. — EVIDENCE: spec/04_system-components.md:155-186 (table rows),
schemas/lenny-adapter.proto:1217, :1456

FACT: The gRPC request-message classification has exactly three consumers in `spec/`, and I checked all
three against the staged text. (1) the value rule at spec/05_runtime-registry-and-pool-model.md:515; (2)
the two per-message declarations that SURVIVE SPEC-1 in §4.7.1, `ReportSessionScrub` "The request is
session-scoped" and `ReportPodScrub` "The request is pod-scoped", both of which agree with the derived
class; (3) the `ShutdownRequest` paragraph at spec/04_system-components.md:190, which SPEC-1 leaves
standing. `grep -rn "message scope|message-scope|messageScope" spec/ schemas/ docs/ charts/` returns
NOTHING, so §9's spec-side list of one file is complete. — EVIDENCE: spec/04_system-components.md:725-726,
:190

FACT: `spec/28_communication-channels.md:314-317` (the `CH-FENCE` card) already describes the fence
per-session ("The pod records the generation against the session the fence names ... A fence for one
session does not change the generation the pod holds for another"), so the reclassification creates no
contradiction there and needs no edit. Same for the §28.3 register row and
`docs/reference/adapter-contract.md:69`, neither of which carries a scope column. — EVIDENCE:
spec/28_communication-channels.md:314-317

FACT: "0073's §4.2 value rule", cited in §3, §6, and §7 of the staged spec changes, is a reference to
proposal 0073's OWN section 4.2 ("### 4.2 The rule", proposals/0073_...md:964), not to spec/04 §4.2
(Session Manager), which contains no such rule. The rule LANDED at
spec/05_runtime-registry-and-pool-model.md:515. Do not file the "§4.2" reference as a false spec citation;
I chased it and it resolves. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:711,:964-968

USEFUL [spec.2.review-security.1]: its DECISION not to file the nested-address hole (a request declaring
`Foo foo = 1` where `Foo` carries a conventional `SessionId session_id`) saved me from filing it. The
reasoning holds on re-examination: the staged sentence "a session addressed under a name and a type that
are both unconventional" reads onto the nested case, because the request's own top-level address-bearing
field is `Foo foo`, whose name and type are both unconventional. §7's parallel sentence ("some other name
and some other type") reads onto it the same way. Do not re-open this.

WATCHOUT: spec/10_gateway-internals.md:57 says the adapter "accepts `CoordinatorFence` RPCs from a new
coordinator, which is the only way to exit hold state", and :58 shows hold is pod-wide (its timeout
"terminates every session the adapter has started on that pod"). A session-scoped fence exiting a pod-wide
hold looks like an isolation question, but it is NOT this proposal's to answer: 0076's OD3 weighed and
rejected exactly that argument, D3 records the rejection, and §6 makes re-deriving OD3 a non-goal. Chasing
it costs a round and closes nothing. — EVIDENCE: spec/10_gateway-internals.md:40, :57-58;
0075...spec-changes.md:49-55



### [spec.4.review-applicability.1]

DECISION: Returned an EMPTY findings list for the applicability/sequencing lens on the r4 spec staging — BECAUSE the proposal text is byte-identical to the r3 snapshot (the r4 fix stage changed nothing), and I re-derived the whole lens from the tree rather than trusting r3: every citation in `spec-changes.md` resolves, SPEC-1's replacement text is quoted verbatim in D1 so nothing is left to invent, no artifact SPEC-1 references is created by a later sub-step, and no register or gate outside the one TEST-1 replaces reads the retired block — ALTERNATIVES: the S1-to-S2 tier-0 red window (checklist remedy, out of this loop's scope and already DEFERRED at [spec.3.review-applicability.1]); the "spec text names a tier-0 gate that S2 builds" forward-reference reading, rejected because spec-first is this repo's mandated order and the sentence names no file or test function.

FACT: `diff -rq` between `scratchpad/cp-snap/.../spec-r4` and the live proposal directory is EMPTY, as it was against `spec-r3`. Two consecutive rounds have produced no edit to the spec staging. The last substantive change is still the r2 deletion of the addressee gloss from D1 paragraph 1. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r4

CORRECTS [spec.2 refutation record]: two earlier refutations leaned on `spec-changes.md` §5 opening with "IMPLEMENTOR TO FILL THE BLANKS. The staged blocks below are indicative." That banner is GONE from `spec-changes.md` and now appears only in `non-spec-changes.md:12` and `:41`. The spec staging is definitive text today: §5 SPEC-1 names the exact line ranges and D1 quotes the replacement paragraphs verbatim. Do not repeat the "it is only indicative" defence for a spec-side finding; judge SPEC-1 as final text. (The refuted findings themselves stay refuted on their other grounds.) — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:107-122; 0075_....non-spec-changes.md:12

FACT: The line-citation ratchet cannot fire on this proposal or on SPEC-1. Its retired form is the `§X(.Y)* line(s) L` spelling, not `file.md:NNN`, and `proposals/` is outside its read domain (`tests/tier0_static/line_citation_ratchet_test.go:421-423` copies a proposal path as a negative fixture precisely because it is out of domain). `tests/registers/line-citations.yaml` is `files: []`. — EVIDENCE: tests/tier0_static/line_citation_ratchet_test.go:19, :421-423; tests/registers/line-citations.yaml:15

FACT: Deleting `spec/04_system-components.md:151-188` renumbers no register entry. The eight `identifier-senses.yaml` rows for `spec/04` are keyed by the ordinal of a retired channel spelling in the file, and the block carries none: a case-insensitive grep of `spec/04` for `LifecycleChannel`, `controlchannel`, and `lifecycle-socket` (the §28.3 naming table's retired spellings, `spec/28_communication-channels.md:151-156`) returns nothing anywhere in the file. `GatewayControl` in the table's Service column is not one of them. — EVIDENCE: tests/registers/identifier-senses.yaml:13-36; spec/28_communication-channels.md:149-156

FACT: The D2 gate passes on today's proto in both directions, machine-counted at HEAD: 26 fields named `session_id` and 26 fields of type `SessionId`, and the two sets coincide. Only `CheckpointRequest` (`:1174`) and `CheckpointResponse` (`:1250`) declare a `oneof`; only the first is an RPC request type; its frames declare exactly one address (`CheckpointStart`'s `SessionId session_id = 7` at `:1217`). So SPEC-1 stages no rule its own tree would fail. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1217, :1250

FACT: No literal from the retiring block is pinned outside the gate TEST-1 replaces. Greps for "appears on messages of both classes", "carries `session_id` and stays pod-scoped", "declared in the table below", and "is a row, and no row names" across `tests/`, `scripts/`, `pkg/`, and `docs/` return exactly one hit, `tests/tier0_static/adapter_proto_message_scope_test.go:20`. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:20

WATCHOUT: `messageScopeRow` (`tests/tier0_static/adapter_proto_message_scope_test.go:43`) is a bare four-column markdown-row regex applied to the WHOLE of `spec/04_system-components.md`, not to §4.1's table alone. Anyone reasoning about what the retiring gate sees after SPEC-1 deletes `:153-186` must account for that; it does not change the disposition (the gate is replaced wholesale by TEST-1), but it invalidates the simpler "the table's rows are its only input" model. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:41-70


### [spec.4.review-citations.1]

DECISION: Returned an empty findings list — BECAUSE every concrete citation in `spec-changes.md` resolves to text saying what the proposal claims, and the two candidates I derived (below) both land under the "redundancy / wording" refute side that this loop has already refuted three close variants of — ALTERNATIVES: filing the `spec/04:725-726` tension and the "0073's SPEC-7 block" enumeration; rejected because neither makes an applied statement false.

FACT: The snapshot at `scratchpad/cp-snap/.../spec-r4` is byte-identical to the current proposal directory (`diff -ru` returns nothing), so round 4 had no fixer diff to read hardest. Do not spend time hunting for changed hunks — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r4

FACT: The `IMPLEMENTOR TO FILL THE BLANKS` banners now live ONLY in `non-spec-changes.md` (:12, :41). `spec-changes.md` §5 carries none, so D1's quoted block is final text to apply rather than an indicative sketch. Several earlier refutations in this loop rested on the banner being in §5 of the spec staging; that ground is gone, so re-derive rather than reusing those refutations verbatim — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:107-111; proposals/0075_.../0075_....non-spec-changes.md:12

FACT: Every citation in `spec-changes.md` verified good on 2026-09-07: proto `:1217` (`SessionId session_id = 7` in `CheckpointStart`); `spec/04:149` heading, `:151` introducing paragraph, `:153-186` the 32-row table, `:188` the declaring paragraph, `:190` the `ShutdownRequest` precedent; `spec/05:515` (the empty-identifier refusal, buried far into one very long line); `pkg/adapter/checkpoint.go:74-84`; `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27` (the gate's own limit comment); `claim_register_proto_agreement_test.go:64`; `tests/spec-map.json:156,:169,:5670`. No drift worth reporting — EVIDENCE: spec/04_system-components.md:149-190

FACT: The replacement gate passes on the proto as it stands. A full scan of every message (oneof arms included) returns zero fields named `session_id` that are not `SessionId` and zero `SessionId`-typed fields not named `session_id` (26 such fields, all `session_id`); `CheckpointRequest`'s frames declare exactly one address (`CheckpointStart`), and the envelope's only top-level field is `coordination_generation`. So D2's gate has no day-one failure — EVIDENCE: schemas/lenny-adapter.proto:596, :1173-1187, :1217, :1455-1461

FACT: `parseMessageScopeTable`, `declaredScope`, `messageScopeRow`, `messageScopeSpecPath`, and `checkpointStartMessage` have no reader outside `adapter_proto_message_scope_test.go`, and nothing under `spec/`, `docs/`, `tests/`, `pkg/`, `cmd/`, or `scripts/` greps for the string "Request Message Scope" or the anchor `request-message-scope`. The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) read neither the table nor the shared parse. So retiring the table has no blast radius beyond the files §9 already lists — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43-81

DEFERRED [proposals/0075_.../0075_....spec-changes.md]: D2 says the addressing convention is "an addressing convention that nothing checks today". That is an overstatement rather than a falsehood: `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each of the 16 members of `sessionScopedMessages`, that `session_id` exists and its message type is `lenny.adapter.v1.SessionId`. What is true instead: no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset that TEST-2 then extends. I did not file it: the replacement gate is justified either way and no applied statement becomes false — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116

OPEN: After SPEC-1, §4.1 states that the classification "is derived from the message's field set rather than declared per message", while `spec/04_system-components.md:725` and `:726` still declare `ReportSessionScrub`'s request session-scoped and `ReportPodScrub`'s request pod-scoped, per message, in the §4.7.1 RPC table. Both declarations AGREE with the derivation, so nothing becomes false, but the applied spec then asserts a method it does not follow in §4.7.1. A later round or the human should decide whether those two clauses come out or stay as agreeing restatements. An evidence skeptic in an earlier round already circled `:726` and called naming it "reasonable convergence feedback", so this is the second sighting — EVIDENCE: spec/04_system-components.md:725-726

OPEN: SPEC-1 says it replaces "the whole block 0073's SPEC-7 stages, which is the paragraph introducing the declared table (`:151`), the table itself (`:153-186`), and the paragraph grounding the fence's classification (`:188`)". 0073 §4.1 stages a fourth paragraph in the same block, the `ShutdownRequest` precedent that becomes `spec/04:190`, which SPEC-1's next sentence exempts. The enumeration and the "whole block" phrase therefore denote different sets. The operational instruction is unambiguous because the exemption is explicit, so I did not file it; a fixer with a wording grant could tighten "the whole block 0073's SPEC-7 stages" to "the block 0073's SPEC-7 stages other than its `ShutdownRequest` paragraph" — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:960-962; proposals/0075_.../0075_....spec-changes.md:111-117

USEFUL [spec.1.fix-G1.1]: the WATCHOUT about proto anchors having drifted ~7 lines was already discharged — `:1217`, `:1455`, `:1456`, and `:596` are all correct in the current text, so I re-verified rather than re-derived them.
USEFUL [spec.1.fix-G1.1]: the FACT that `spec/05:515` is one very long line saved me from misreading a `sed -n 515p` as a bad citation.


### [spec.4.review-client-surface.1]

DECISION: Returned an EMPTY findings list for the client-facing-surface lens on the r4 spec staging — BECAUSE the change reaches no externally-consumed contract with a parallel representation: no `sdks/` file names `CoordinatorFence`, no proto or JSONL schema doc comment declares a gRPC request's scope class, `docs/reference/adapter-contract.md` mirrors §4.7.1 rather than §4.1, and the only per-message scope declarations that survive SPEC-1 (`spec/04_system-components.md:725`, `:726` and their docs mirror at `docs/reference/adapter-contract.md:81`) all agree with what the staged derivation computes — ALTERNATIVES: the "a protocol definition" domain ambiguity in the staged constraint paragraph (see WATCHOUT), and the §4.7.1 "declared per message" tension (already declined twice), both dropped.

FACT: `docs/reference/adapter-contract.md:57-83` is the client-facing mirror of the gRPC control protocol, and it mirrors §4.7.1's two RPC tables, not §4.1's classification table. Its `CoordinatorFence` row (`:69`) carries a purpose sentence and no scope word, and its only scope sentence is `ReportSessionScrub`'s at `:81`, which restates `spec/04_system-components.md:725` and is untouched by SPEC-1. So the retirement of the §4.1 table has no client-doc edit site. — EVIDENCE: docs/reference/adapter-contract.md:69, :81; spec/04_system-components.md:725

FACT: The gateway-adapter protocol is one file and the tier-0 parse pins it by path: `const adapterProtoPath = "schemas/lenny-adapter.proto"` (tests/tier0_static/adapter_proto_parse_test.go:18). This matters because `schemas/lenny-interceptor.proto:56` and `schemas/lenny-tokenservice.proto:65,:154` DO declare `string session_id` fields, which would fail the staged gate's first clause if its domain were ever read as `schemas/*.proto`. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:18; schemas/lenny-interceptor.proto:56; schemas/lenny-tokenservice.proto:65

WATCHOUT: The staged constraint paragraph says "A tier-0 gate refuses **a** protocol definition in which a field named `session_id` is not of type `SessionId`..." (spec-changes.md:20-23). The indefinite article gives no explicit domain; it resolves only through the block's opening "the gateway-adapter protocol" and through the shared parse's single pinned path. I judged it below the bar and did NOT file it. Do not re-open it as a correctness defect, but do not let a rewrite move that paragraph away from the two paragraphs that establish the protocol. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:8, :20-23

FACT: Re-ran the census independently with a brace-depth parser (a naive one silently mis-parses this file: a first attempt returned 14 messages and 25 "pod-scoped" request types, all wrong). Correct result at HEAD: 31 distinct RPC request types, 25 declaring a top-level `SessionId session_id`, 5 declaring no address (`AdapterEventsRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`), and exactly one carrying a `oneof` (`CheckpointRequest`). That is 25 + envelope + `CheckpointStart` = 27 session-classified names against the table's 26 session rows plus the fence, and 5 against its 6 pod rows minus the fence. D1's reproduction claim holds. Anyone re-deriving this: locate each message block as `^message NAME {` to a line that is exactly `}`, and count braces only for oneof/nested depth. — EVIDENCE: schemas/lenny-adapter.proto:1173, :1217; spec/04_system-components.md:155-186

FACT: Every field named `session_id` in `schemas/lenny-adapter.proto` is of type `SessionId` (26 declaration sites) and the only other `session_id` occurrences in the file are doc-comment prose; `SessionId` appears outside those sites only at its own definition (`:595-597`). The staged gate therefore passes on the tree and is falsifiable in both directions. — EVIDENCE: schemas/lenny-adapter.proto:596, :1456

FACT: No test outside the retiring gate reads the §4.1 block. `grep -rn "Request Message Scope\|messageScopeSpecPath" tests/ scripts/` returns only `tests/tier0_static/adapter_proto_message_scope_test.go`. The six `// spec: 4.1 (request message scope)` annotations in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` are annotations only — that file does not read `spec/04_system-components.md`, and SPEC-1 keeps the `#### Request Message Scope` heading, so the annotations still resolve. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:31; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

USEFUL [spec.3.review-edit-sites.1]: its statement that nothing outside `spec/04_system-components.md` declares a gateway-adapter request message's scope except the two §4.7.1 cells is correct. I re-derived it a third way (case-insensitive sweep of `spec/ docs/ schemas/ charts/ sdks/` for "message scope", "request scope", "guard rather than an address") and reached the same set, which closed the §9-completeness half of this lens in one pass.


### [spec.4.review-docs-alignment.1]

DECISION: Returned an empty findings list again for the documentation-alignment lens, on the round-4 text — BECAUSE the staged edit is confined to a spec-internal classification statement that `docs/` never mirrors, it moves no metric, alert, error code, flag, endpoint, or lifecycle step, and the one accepted residual it carries (the gate cannot see a session addressed under both an unconventional name and an unconventional type) already has landing spec text inside D1's third staged paragraph. ALTERNATIVES: considered filing the absence of an "Edge cases and accepted failure modes" section, and the §7 Q1 fail-closed residual (a future pod-scoped guard named `session_id` derives session-scoped and inherits the §4.2 empty-identifier refusal); rejected both because the residuals resolve to landing text or follow directly from the stated rule, and the section's absence is proposal bookkeeping rather than a defect in the applied spec.

USEFUL [spec.1.review-docs-alignment.1]: its FACT that `docs/` names RPCs and never request message types held on re-verification, and saved the whole first sweep. Re-confirmed at round 4: a grep of `docs/`, `charts/`, and `schemas/*.json` for `CoordinatorFenceRequest`, `CheckpointStart`, and `ReportPodScrubRequest` returns only the proto excerpt at docs/api/internal.md:210 (`message CheckpointRequest {`), which carries no scope word.

FACT: The only two scope declarations anywhere in `docs/` that touch this contract are docs/reference/adapter-contract.md:81 (`ReportSessionScrub` "The request is session-scoped") and its absence-of-a-claim sibling at :82. Both survive the derivation rule unchanged: `ReportSessionScrubRequest` declares `SessionId session_id = 2` and `ReportPodScrubRequest` declares `pod_id`, `outcome`, `detail` and no address. — EVIDENCE: schemas/lenny-adapter.proto, `message ReportSessionScrubRequest` / `message ReportPodScrubRequest` blocks; spec/04_system-components.md:725-726.

FACT: `oneof` appears exactly four times in the proto, at :1174 (`CheckpointRequest`), :1183 (a doc comment naming the `msg` oneof), and :1250 (`CheckpointResponse`). `AttachRequest` (:989) carries a flat field set with `SessionId session_id = 1`, so the staged stream-envelope clause selects `CheckpointRequest` and nothing else, exactly as §4 of the staged changes claims. — EVIDENCE: schemas/lenny-adapter.proto:1174,1250,989.

FACT: No spec section outside §4.1 declares `CoordinatorFenceRequest` pod-scoped, and no markdown anchor anywhere in `spec/`, `docs/`, `tests/`, or `scripts/` points at `#request-message-scope`, so retiring the table breaks no inbound link. — EVIDENCE: grep for `CoordinatorFence` over spec/ returns only spec/04_system-components.md:175 and :188 as scope statements, both inside SPEC-1's replaced block; grep for `request-message-scope` returns nothing.

FACT: The reclassification creates no undocumented behavior at the adapter boundary. `CoordinatorFence` already returns `InvalidArgument` on an empty session id, which is what spec/05_runtime-registry-and-pool-model.md:515 requires of a session-scoped request, so bringing the fence into that class matches shipped code. — EVIDENCE: pkg/adapter/coordination.go:107-110.

WATCHOUT: The round-4 snapshot at scratchpad/cp-snap/.../spec-r4 is byte-identical to the live proposal directory (`diff -rq` exits 0), so the "read the changed sections first" instruction has no diff to act on this round. Do not read that as "nothing changed since round 1"; the spec-changes text was rewritten across rounds 2 and 3 and only the round-4 snapshot happens to coincide with the current text.


### [spec.4.review-edit-sites.4]

DECISION: Returned an empty findings list, the second consecutive empty return for this lens — BECAUSE the proposal text is byte-identical to the round-3 snapshot (`diff -ru scratchpad/cp-snap/.../spec-r4 proposals/0075_...` returns nothing; only the review log differs between spec-r3 and spec-r4), and an independent re-sweep of every identifier SPEC-1 removes or introduces found no authored surface outside `spec/04_system-components.md` that becomes wrong — ALTERNATIVES: filing the "a protocol definition" scoping of the staged gate sentence (see WATCHOUT), and re-filing the §4.7.1 per-message-declaration tension, which rounds 2 and 3 both weighed and dropped.

FACT: The proposal is unchanged since round 3. `diff -ru` between `scratchpad/cp-snap/0075_.../spec-r4` and the live directory is empty, and `diff -rq spec-r3 spec-r4` differs only in the review log. Two consecutive rounds of this lens therefore reviewed identical text; a future round that also finds no diff can weight the previous round's sweep heavily and spend its effort on surfaces nobody has named yet. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r4

FACT: The registers under `tests/registers/` are the surface class no earlier round had checked, and none of them is an edit site. `identifier-senses.yaml` keys entries by the 1-based position of a retired channel spelling within a file, and no retired spelling from the §28.3 naming table (`LifecycleChannel`, `controlchannel`, `lifecycleChannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`, spec/28:151-159) occurs anywhere in `spec/04_system-components.md`, case-insensitively. `pinned-spec-literals.yaml` keys Go string literals under `tests/tier11_docs/` and names no spec/04 site. No register file references `04_system-components` at all except those eight `identifier-senses` rows. Deleting :151-188 therefore renumbers nothing. — EVIDENCE: tests/registers/identifier-senses.yaml:14-36; spec/28_communication-channels.md:151-159

FACT: `tests/tier11_docs/successor_pointer_test.go` enforces N8's successor-pointer rule over a hand-named domain (`reducedSections`, `:52-56`) holding §4.7, §15.4, and §29.10 only. Retiring the §4.1 table is a reduction with no receiving heading, so it raises no successor-pointer obligation and adds no row to that list. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:52-56

FACT: The only tests that read `spec/04_system-components.md` at tier 0 are the retiring gate and `adapter_proto_event_taxonomy_test.go`; at tier 11 the readers all target §4.7, §28, or the credential and recycle sections. `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:47` opens `"### 4.7 "` and never reaches §4.1, so no tier-11 case breaks when the table goes. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:47

WATCHOUT: The staged gate sentence says "A tier-0 gate refuses a protocol definition in which a field named `session_id` is not of type `SessionId`" with an indefinite article. `schemas/` holds two other protocol definitions that would fail that sentence read literally: `lenny-interceptor.proto:56` and `lenny-tokenservice.proto:65`,`:154` each declare `string session_id = 2`. The reading is saved by context (the block's first paragraph says "a request on this protocol", D2 says "the protocol", and the shared parse's `adapterProtoPath` constant pins `schemas/lenny-adapter.proto`), so I judged it below the bar. Do not let a rewrite widen it into a statement about `schemas/` generally, and do not let the implementor scope the gate's file set to the whole directory. — EVIDENCE: schemas/lenny-interceptor.proto:56; schemas/lenny-tokenservice.proto:65; tests/tier0_static/adapter_proto_parse_test.go:18

FACT: `SessionId` as a type name appears nowhere in `spec/` or `docs/` today; the staged block is the first spec prose to name it. Nothing depends on it being defined in spec, since §4.1 already names proto message types directly (`CoordinatorFenceRequest`, `CheckpointStart`), so this is not a missing definition site. — EVIDENCE: grep for `SessionId` over spec/ and docs/ returns only camelCase field names such as `parentSessionId`

USEFUL [spec.2.review-edit-sites.1]: its `tests/spec-map.json` catch (`:156`, `:169` under 4.1 and `:5670` under 28.5.3) and its list of the §4.7.1 survivors at `spec/04_system-components.md:725-726` were both exact when I re-verified them, which let me spend this pass on register and gate surfaces instead of re-deriving the map.

USEFUL [spec.3.review-edit-sites.1]: its programmatic derivation of the proto co-occurrence (every `session_id` typed `SessionId` and back) matched my independent scan (26 typed fields, 53 total `session_id` occurrences with the remainder in doc comments), so the load-bearing premise of the staged rule now has three independent confirmations.


### [spec.4.review-feasibility.4]

DECISION: Returned an EMPTY findings list for the actor-action feasibility lens on the r4 spec staging — BECAUSE the only actor the staged spec text assigns an action to is "a tier-0 gate", and every one of its four refusal clauses is decidable from `schemas/lenny-adapter.proto` text alone, which a tier-0 static check can read; and because I re-derived, rather than trusted, every citation in `spec-changes.md` §2, §4, §5, §7, §9, and §10 — ALTERNATIVES: filing the "only way a request addresses a session" tension against the envelope clause (declined three times now, see [spec.2.review-citations.1], [spec.2.review-mechanism.1], [spec.3.review-fresh.1]); filing D1's "the ones that carry none are exactly its pod rows" as false (the set of address-free request messages is 6 and the table's pod rows are 5, the difference being `CheckpointRequest` — but the sentence's "other than the stream envelope" preamble distributes over both clauses and the very next sentence names the envelope's disposition, and this exact area is on the already-fixed list).

FACT: `diff -ru` between `scratchpad/cp-snap/.../spec-r4` and the live proposal directory returns NOTHING, and so does the diff against `spec-r3` for every file except `review-log.md`. The proposal text has not changed since round 3, when this same lens returned empty. The r3 fix stage made no edit to `spec-changes.md`. Do not read a "read the changed sections hardest" instruction as pointing at anything this round — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3 vs proposals/0075_fix_derive-message-scope-from-the-address-type

FACT: The replacement gate passes on today's proto, clause by clause, checked independently. (1) No field named `session_id` carries another type: every `session_id` occurrence outside a comment is `SessionId session_id`. (2) No field of type `SessionId` carries another name: `grep -nE "^\s*(repeated\s+)?SessionId\s+\w+\s*=" schemas/lenny-adapter.proto | grep -v "SessionId session_id ="` returns nothing, so a `repeated SessionId session_ids` near-miss does not exist either. (3) `CheckpointRequest` is the only request-typed message with a `oneof` (the other is `CheckpointResponse` at :1250, the request type of no RPC), it declares no top-level address (`coordination_generation int64 = 4` is its only top-level field), and exactly one frame declares the address: `CheckpointStart` has `SessionId session_id = 7`, while `CheckpointGrant` declares `index/url/content_length/headers/expires_at` and `CheckpointAbort` declares `reason` alone — EVIDENCE: schemas/lenny-adapter.proto:1173-1187, :1217, :1249-1255

FACT: Naming a test tier inside `spec/` has precedent, so the staged "A tier-0 gate refuses ..." sentence is not a layering violation. `spec/18_build-sequence.md:85` and `:98` both name "Tier 0 (Static)" as a phase exit criterion — EVIDENCE: spec/18_build-sequence.md:85, :98

FACT: The §4.1 table is the ONLY place in the specification that enumerates all 31 request messages. §4.7.1's Gateway→Adapter RPC table (`spec/04_system-components.md:698-716`) carries 19 rows and omits `NegotiateVersion`, `GetObservedIntegrationLevel`, `AdapterEvents`, `SignalDeadline`, and `RevokeCredentials`. Retiring the §4.1 table therefore loses the Service and Direction columns for those five. I did not file it: nothing in `spec/`, `tests/`, `pkg/`, or `docs/` reads a direction or a service from the §4.1 table, and the derivation rule needs neither column — EVIDENCE: spec/04_system-components.md:698-716, :155-186

FACT: `pkg/adapter/checkpoint.go:74` cites "spec: §4.1 — Checkpoint is session-scoped" and is the only `// spec:` citation in `pkg/` or `cmd/` that leans on the §4.1 message-scope classification. It survives SPEC-1 intact, because the envelope clause derives `CheckpointRequest` session-scoped through `CheckpointStart`. No code comment anywhere cites §4.1 for the fence's pod scope — EVIDENCE: pkg/adapter/checkpoint.go:74-84

WATCHOUT: `proposals/0080_...md:453-454` (§2 "Already owned") assigns TWO of 0073's residues to this proposal: "**D6's declared message-scope table** and the §4.1 `ShutdownRequest` classification limit that shares its cause are taken by proposal 0075". This proposal takes only the first: SPEC-1 explicitly leaves the `ShutdownRequest` paragraph at `spec/04_system-components.md:190` unedited, and the `ShutdownRequest` limit 0073 recorded (the gate "cannot check that a declared scope covers everything the handler does with the request") is retired with the gate rather than restated. I did not file it — 0080 is a Draft, the mismatch is in that draft's inventory rather than in anything this proposal makes false, and the remedy is not in `spec-changes.md`. Whoever converges 0080 should reconcile that row — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:453-454; proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6710 (the recorded ShutdownRequest limit); 0075_....spec-changes.md:116-118

USEFUL [spec.3.review-feasibility.1]: its enumeration of the shared parse's two callers and of the "nothing outside §4.1 reads the table" set is exact and re-verified today; `grep -rn "Request Message Scope\|classification table\|message-scope" spec/ docs/ schemas/ charts/` still returns only `spec/04_system-components.md:149` outside `tests/`. It saved me a full sweep.

USEFUL [spec.1.review-feasibility.1]: its WATCHOUT that the `spec/04` §4.1 line numbers (`:149`, `:151`, `:175`, `:188`, `:190`) are exact and must not be "refreshed" is still true at HEAD. I re-read `spec/04_system-components.md:145-195` and confirmed all five, plus the table body at `:153-186` with 32 rows at `:155-186`.


### [spec.4.review-fresh.1]

DECISION: Returned an EMPTY findings list for the fresh-holistic lens on the r4 spec staging — BECAUSE `spec-changes.md` is byte-identical to r3 (round 3 returned nothing from every lens, so the only r3→r4 delta is review-log text), and I re-derived the load-bearing facts from the tree independently rather than trusting the shards: the derivation reproduces the post-OD3 table row for row, every citation in `spec-changes.md` resolves, and nothing under `spec/`, `docs/`, `schemas/`, or `charts/` outside `spec/04_system-components.md:151-188` reads the retired table — ALTERNATIVES: four candidates derived and dropped, each recorded below.

FACT: The derivation-vs-table reproduction, machine-checked a fourth time by a different method (`awk '/^message [A-Za-z]/{m=$2} /^  SessionId session_id = /{print m}'`). 26 messages declare `SessionId session_id`: 25 RPC request types plus `CheckpointStart`. Table session rows are :155-174 (20) and :180-185 (6) = 26; drop `CheckpointRequest` and `CheckpointStart` = 24; add `CoordinatorFenceRequest` = 25, which is exactly the derived session set. Table pod rows :175-179 and :186 = 6; drop the fence = 5, exactly the derived pod set. The envelope clause carries the two remaining rows. No message moves session→pod. — EVIDENCE: schemas/lenny-adapter.proto:1217, :1456; spec/04_system-components.md:155-186

FACT: `grep -n "oneof " schemas/lenny-adapter.proto` returns exactly two hits, `:1174` (`CheckpointRequest`) and `:1250` (`CheckpointResponse`), and `grep -n "SessionId" | grep -v "SessionId session_id"` returns only the type's own declaration at `:595-596`. Both premises of the staged envelope clause and of D2's second gate clause hold with no exception. Do not re-derive these with a hand-rolled brace-depth parser; mine mis-tracked depth and produced a wrong census before I fell back to grep/awk. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250, :596

WATCHOUT: `schemas/lenny-interceptor.proto:56` and `schemas/lenny-tokenservice.proto:65,:154` each declare `string session_id = 2;`, a field named `session_id` that is NOT of type `SessionId`. The staged paragraph 3 reads "A tier-0 gate refuses **a protocol definition** in which a field named `session_id` is not of type `SessionId`" — with the indefinite article, so a reader could take the gate's domain as `schemas/*.proto` and build a gate that is red on day one. I did NOT file it: the block's own first sentence fixes the subject as "the gateway-adapter protocol", D2 says "the protocol", and §4's IMPLEMENTOR'S CHOICE pins the parse to the shared tier-0 one whose `adapterProtoPath = "schemas/lenny-adapter.proto"` (`tests/tier0_static/adapter_proto_parse_test.go:18`). An implementor cannot arrive at the wrong domain from this text. Do not re-open it as a correctness defect, but a fixer rewording paragraph 3 must not lose the "gateway-adapter" antecedent. — EVIDENCE: schemas/lenny-interceptor.proto:56; schemas/lenny-tokenservice.proto:65; tests/tier0_static/adapter_proto_parse_test.go:17-18

WATCHOUT: D1's reproduction sentence is off by one row and self-tensions with the sentence after it. "the ones other than the stream envelope that carry the address are exactly the table's session rows once `CoordinatorFenceRequest` moves" — the table's session rows are 26 and include `CheckpointStart`, which is not a request message either service declares, so the identity holds only for 25 of them; the next sentence ("The envelope clause carries the remaining two rows") presupposes exactly that two rows are outside the first sentence's reach. I did NOT file it: it is rationale prose in D1, not the staged blockquote, so applying SPEC-1 verbatim is unaffected and the applied spec is not made inconsistent. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:27-30; spec/04_system-components.md:167-168

FACT: D2's stated reason for dropping the "no pod-scoped message declares a field of the address type" clause is slightly wrong but the conclusion holds. The clause does not "reduce to a statement that a message without the field does not carry the field": a pod-scoped message could declare `SessionId subject = 2` and satisfy both halves of D2's sentence while carrying a field of the address type. What actually makes the clause redundant is the retained second gate clause, "a field of type `SessionId` is not named `session_id`", which refuses that message on its own. Below the bar (the gate's behaviour is identical either way), but a fixer rewording D2 should not restore the dropped clause on the strength of this. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:41-47; schemas/lenny-adapter.proto:596

FACT: Every citation in `spec-changes.md` re-resolved at this commit: `spec/04:151`, `:153-186`, `:175`, `:188`, `:190`; `schemas/lenny-adapter.proto:1217`; `spec/05_runtime-registry-and-pool-model.md:515`; `pkg/adapter/checkpoint.go:74-84` (spec comment at :74-80, the `InvalidArgument` refusal at :82-84); `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27` (and that comment does record the gate's limit, "Whether a handler enforces the scope a row declares is a runtime question"); `tests/tier0_static/claim_register_proto_agreement_test.go:64` (`fields := protoFields(protoBody)`). §6's pointer to proposal 0080 §1.16 resolves (`proposals/0080_...:216`, "The pod refuses the fence retry §10.1.2 orders"). D3's summary of 0076's OD3 matches 0076's own answer table verbatim. — EVIDENCE: proposals/0076_.../0076_....summary.md (Decisions the reviewer answered, OD3 A and B rows)

USEFUL [spec.3.review-fresh.1]: its FACT that `tests/tier11_docs/successor_pointer_test.go` cannot fire on this retirement, and its `tests/claim-map.json` sweep, are both correct and saved me two independent checks. Its `AdapterEventsRequest` note is the right pre-emption; I reached for that message first as well.

USEFUL [spec.3.review-edit-sites.1]: its WATCHOUT on the "top-level ... which is the only way" scope ambiguity in clause 1 is the same class as my "a protocol definition" watchout above. Both are article-and-antecedent hazards in paragraph 1 and paragraph 3 that only survive because the surrounding sentences fix the subject; a rewrite that touches either paragraph has to keep both antecedents.

DEFERRED [0075_....non-spec-changes.md]: TEST-2 stages deleting "the coverage clause at `:40-43`" of `tests/tier3_contract/adapter_session_address/session_address_wire_test.go`. The membership sentence actually begins mid-line at `:39` ("A message is in this set when the specification's" / `:40` "message-scope table gives it session scope"), so deleting `:40-43` alone leaves a dangling half-sentence at `:39` whose subject (the retired §4.1 table) no longer exists. The range should be `:39-43`, restated as the derivation rule plus "carried the retired duplicate". Problem-statement §1.3 cites the same `:40-43`. The remedy is a test-file edit and a non-spec-staging edit, so this loop cannot land it. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43



### [spec.4.review-kubernetes.1]

DECISION: Returned zero findings under the Kubernetes-idiom lens — BECAUSE the staged spec edit (SPEC-1 / D1's block) rewrites only `spec/04_system-components.md` §4.1 "Request Message Scope", a classification rule over gRPC request messages on the gateway-adapter protocol. It names no CRD, no status subresource, no field manager, no finalizer, no admission webhook, and no controller. Nothing in it puts a controller on a synchronous request path or turns a status field into an RPC inbox — ALTERNATIVES: I considered filing on the session-scoped fence vs. the pod-scoped hold state as a granularity mismatch, and rejected it (see WATCHOUT below).

WATCHOUT: The one place this proposal looks like a granularity mismatch is that `CoordinatorFenceRequest` becomes session-scoped while the hold state it exits is pod-scoped — `spec/10_gateway-internals.md:57` ("it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state") and `spec/10_gateway-internals.md:60` ("The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"). This is NOT a finding: D3 (`spec-changes.md:49-55`) records that 0076's OD3 weighed exactly that alternative and rejected it, and `spec/04_system-components.md:190` is the standing `ShutdownRequest` precedent for a session-scoped message whose handler runs a whole-pod effect. Do not re-file it.

FACT: The addressing convention the staged gate checks already holds on the current proto, so the gate is green on landing. Every field of type `SessionId` is named `session_id` and vice versa (`schemas/lenny-adapter.proto`, `SessionId` at :596; typed fields at :342, :365, :384, :404, :420, :458, :683, :707, :847, :909, :965, :990, :1022, :1036, :1064, :1088, :1118, :1217, :1305, :1339, :1456, :1487, :1539, :1577, :1610, :1674). Exactly two `oneof` blocks exist, at :1174 (`CheckpointRequest`) and :1250 (`CheckpointResponse`), and only the first is an RPC request type — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

FACT: No reader-facing page restates the per-message scope classification the table carries, so §1.4's "no reader-facing page changes" holds. `docs/reference/adapter-contract.md:69` carries the `CoordinatorFence` row with no scope cell, and the only per-message scope sentence in `docs/` is `ReportSessionScrub` at `docs/reference/adapter-contract.md:81`, which the derivation reproduces unchanged — EVIDENCE: docs/reference/adapter-contract.md:69, docs/reference/adapter-contract.md:81

FACT: The §28.3 channel register row for `CH-FENCE` carries no scope column, so retiring the §4.1 table falsifies nothing there — EVIDENCE: spec/28_communication-channels.md:120

FACT: The snapshot at `scratchpad/cp-snap/.../spec-r4` is byte-identical to the current proposal directory; `diff -ru` returned empty. Round 3 landed no edits, so "read the changed sections hardest" had no target this round.

USEFUL [orchestrator note on the refuted list]: The already-refuted entry about the pod-scope clause having no input records that `spec/04_system-components.md:726` ("The request is pod-scoped." on the `ReportPodScrub` row of §4.7.1) survives SPEC-1 untouched. That is the surviving per-message pod-scope declaration in `spec/`, and it agrees with the derivation, so it is redundancy rather than contradiction. Anyone tempted to file §4.7.1 as an unstaged edit site should read that refutation first.


### [spec.4.review-mechanism.1]

DECISION: Returned an empty findings list under the end-to-end mechanism lens — BECAUSE every flow the staged block describes traces cleanly from the proto to the classification to the gate, and the three residuals I derived independently had each already been weighed and dropped by a prior round with reasoning I agree with — ALTERNATIVES: filing the nested-address blind spot, the clause-1 "which is the only way" ambiguity, and the §3-vs-D1/D2 conjunct drift; all rejected, see below.

FACT: The proposal's whole mechanism re-verifies mechanically today. A brace-depth parse of both service blocks gives 31 distinct RPC request types; 25 declare a top-level `SessionId session_id`, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). `oneof` appears exactly twice in the file, at :1174 (`CheckpointRequest`) and :1250 (`CheckpointResponse`), and only the first is a request type. All four clauses of D2's replacement gate PASS on the shipped proto, so the gate is green on its first run. — EVIDENCE: schemas/lenny-adapter.proto:596, :1173, :1174, :1217, :1250, :1455-1456

FACT: The reclassification imposes no handler change, and I checked the one place it could have. Under the derivation `CoordinatorFenceRequest` becomes session-scoped and inherits 0073's §4.2 value rule (landed at `spec/05_runtime-registry-and-pool-model.md:515`, "a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved"). The shipped fence handler already does exactly that. So §3's "No proto or handler changes" holds and no code deliverable is missing. — EVIDENCE: pkg/adapter/coordination.go:108-111 (`if sessionID == "" { return nil, status.Error(codes.InvalidArgument, "CoordinatorFence requires a session id") }`)

FACT: Every line anchor in spec-changes.md verifies against the tree as of today. `spec/04_system-components.md:151` (the declared-rather-than-derived paragraph), `:153-186` (the 32-row table), `:175` (the fence's pod row), `:188` (the grounding paragraph), `:190` (the `ShutdownRequest` precedent); `schemas/lenny-adapter.proto:1217`; `spec/05_runtime-registry-and-pool-model.md:515`; `pkg/adapter/checkpoint.go:74-84`; `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier0_static/claim_register_proto_agreement_test.go:64` (`fields := protoFields(protoBody)`); `tests/tier0_static/adapter_proto_message_scope_test.go:17-27` (the gate's header comment, which does record the limit SPEC-1 says it records); `tests/spec-map.json:156`, `:169`. 0073 §4.2 IS "The rule" (the value rule) and 0080 §1.16 IS the fence-retry acceptance predicate, so D3, §6, and §10 cite correctly. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964; proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:216

FACT: D3's account of 0076's OD3 is exact. 0076's summary records "OD3 Question A | Yes. `CoordinatorFenceRequest` is session-scoped after CODE-1" and "OD3 Question B | A successor stages the `spec/04` §4.1 edit ... Proposal 0075 is that successor". — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:183-184

FACT: `spec-changes.md` §5 no longer carries an `IMPLEMENTOR TO FILL THE BLANKS` banner; only `non-spec-changes.md` §5 does. Several refutations in the refuted list lean on "§5 opens with IMPLEMENTOR TO FILL THE BLANKS ... indicative" as a ground for refusing a SPEC-1 finding. That ground no longer exists on the spec side: SPEC-1 is now a firm instruction with quoted replacement text. 0076 removed its own banners on convergence for the same reason (its OD16). Do not re-use the "indicative block" defence for spec-changes.md. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:107-122; proposals/0076_.../0076_....summary.md:190

USEFUL [spec.2.review-security.1] and [the round-3 entries at review-log.md:837, :852, :897]: their DECISIONs not to file the nested-address hole and the clause-1 "which is the only way" ambiguity saved me from filing two findings I had derived independently and was close to submitting. I re-derived both, reached the same two candidates, and stood down on their reasoning: at the request's own top level a wrapper field `Foo foo` carries neither the conventional name nor the conventional type, so the staged sentence "a session addressed under a name and a type that are both unconventional" reads onto the nested case; and paragraph 3's disambiguation is what fixes "which" onto the spelling rather than the field's location.

WATCHOUT: `spec/04_system-components.md:725` and `:726` declare `ReportSessionScrub`'s and `ReportPodScrub`'s request scope per message in §4.7.1 prose, and SPEC-1 does not touch them. After SPEC-1, §4.1 reads "the classification is derived from the message's field set rather than declared per message" while §4.7.1 declares it per message twice. The VALUES agree under the derivation, so nothing is factually contradicted, and the surviving `:726` is also what defeated the earlier "the pod-scope clause has no input" finding. I did not file the method-vs-restatement tension, agreeing with review-log.md:740. Anyone tempted to file it should note it is a third pass over the same text. — EVIDENCE: spec/04_system-components.md:725-726

WATCHOUT: the staged rule makes "stream envelope" imply session-scoped by construction (any request message with a `oneof` must have exactly one addressing frame or the gate refuses), so a pod-scoped multi-frame streaming request becomes unrepresentable, and a request that uses a `oneof` for non-stream alternatives is read as an envelope and refused. Both are fail-CLOSED, both are hypothetical on today's proto, and neither makes the applied spec wrong. Do not spend a round on them. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:12-13, :15-18, :39-42

WATCHOUT: §3's one-sentence summary of the replacement gate names three of D2's four conjuncts — it drops "a stream envelope declares no top-level address of its own". D1's staged spec text and D2 both carry all four and agree with each other exactly, so the authoritative statements do not drift. I judged the overview's omission below the bar. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:78-81 vs :20-23 and :39-42

FACT: An independent edit-site sweep found nothing outside `spec/04_system-components.md` that SPEC-1 falsifies. Nothing under `spec/`, `docs/`, `schemas/`, or `charts/` links to or names the §4.1 classification table (`grep -rn "Request Message Scope"` returns only the heading itself), no `docs/` page states `CoordinatorFence`'s scope, `tests/claim-map.json` carries no message-scope row, and the only other per-message scope declarations anywhere in `spec/` are §4.7.1's two sentences at `:725-726`, which stay true. This independently reproduces [the round-3 edit-site sweep at review-log.md:881]. — EVIDENCE: spec/04_system-components.md:149; docs/reference/adapter-contract.md:69, :81



### [spec.4.review-operational.1]

DECISION: Returned an EMPTY findings list for the operational-consistency lens on the r4 spec staging — BECAUSE SPEC-1 touches no condition, no metric, no alert, and no runbook, and I enumerated every operational surface that could read the retired §4.1 classification and found none — ALTERNATIVES: filing the fence's pod→session reclassification against `spec/10_gateway-internals.md:57-60`'s pod-scoped hold state, rejected because that divergence is exactly the `ShutdownRequest` precedent SPEC-1 leaves standing at `spec/04_system-components.md:190` and 0076's OD3 weighed and rejected the "keep it pod because the fence is the only hold exit" alternative (spec-changes.md:53-55), which is settled outside this review.

FACT: No metric, alert, or runbook in the tree carries a gRPC request-message scope class. The two metrics whose text uses the phrase "session-scoped" are both explicitly scoped to §28.5.3 JSONL frames, not to §4.1 request messages: `lenny_adapter_unaddressed_frame_rejected_total` (spec/16_observability.md:189, mirrored at docs/reference/metrics.md:180) and `lenny_adapter_set_tracing_context_dropped_total` (spec/16_observability.md:188). §28.5.3 defines its own session-scoped frame set independently (spec/28_communication-channels.md:835), so retiring §4.1's table leaves both metric descriptions grounded. — EVIDENCE: spec/16_observability.md:188-189; spec/28_communication-channels.md:835

FACT: Every fence-related observability surface is pool-labeled and carries no scope class, so the reclassification changes nothing an operator reads. `lenny_coordinator_fence_retry_total` and `lenny_coordinator_fence_relinquished_total` (spec/16_observability.md:191-192, docs/reference/metrics.md:311-312) label by `pool`; the one alert naming the fence, `CoordinatorHandoffSlow` (spec/16_observability.md:552), references `lenny_coordinator_handoff_duration_seconds` and the retry counter only. `docs/runbooks/coordinator-handoff-slow.md` is the only runbook mentioning the coordinator and contains no occurrence of "scope", "per session", "pod-wide", or "whole pod". — EVIDENCE: spec/16_observability.md:191-192, :552; docs/runbooks/coordinator-handoff-slow.md

FACT: The `CH-FENCE` register row (spec/28_communication-channels.md:120) and the §28.8 failure-mode row (`:1814`) carry no scope column, and the §28.8 `CH-ATTACH` row states in terms "The specification names no metric or alert scoped to this channel" (`:1812`). So the gateway-to-pod channel surface owes no edit under the reclassification. — EVIDENCE: spec/28_communication-channels.md:120, :1812, :1814

FACT: §28.4's claim register binds only "Every normative statement **this section** makes" (spec/28_communication-channels.md:163). §4.1 is outside its domain, so SPEC-1's staged sentence asserting a tier-0 gate that does not exist until S2 owes no `ABSENT` claim-register row. I checked this specifically because a spec sentence asserting an unbuilt gate is the usual trigger for one. — EVIDENCE: spec/28_communication-channels.md:161-169

FACT: The reclassification imports the value rule at spec/05_runtime-registry-and-pool-model.md:515 onto `CoordinatorFenceRequest`, and no metric counts that class of refusal on the gRPC leg at all — the only refusal counters in the inventory are the two §28.5.3 frame counters above. So the reclassification adds no metric whose documented meaning would drift. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515; spec/16_observability.md:188-189

FACT: `diff -ru` between `scratchpad/cp-snap/.../spec-r4` and the live proposal directory is EMPTY, and so is the diff between `spec-r3` and `spec-r4`. The proposal has been byte-identical for two rounds. The "read the changed sections hardest" reading order had nothing to point at. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3 vs spec-r4

USEFUL [spec.3.review-security.1]: its enumeration of the three consumers of the request-message classification in `spec/` (the value rule, the two surviving §4.7.1 per-message cells, and the `ShutdownRequest` paragraph) is the right frame for the operational lens too. I re-derived the operational half independently (metrics, alerts, runbooks, the §28 registers) and it adds a fourth answer: there is no operational consumer at all.


### [spec.4.review-performance.1]

DECISION: Empty findings list under the performance / scalability / failure-mode lens, second time this lens has run on this proposal — BECAUSE the staged spec edit (SPEC-1) retires a prose table and states a classification predicate; it creates no etcd, Postgres, or Redis write, no informer watch, no work-queue item, and no reconcile path, so there is no per-unit-of-work write rate to multiply against any tier's object counts, and the one behavioral consequence (the fence falls under the empty-identifier refusal) is already the shipped runtime. — ALTERNATIVES: I re-attempted the fence-amplification argument at coordinator handoff and it fails again, see FACT below; I also re-checked whether the retirement strands a failure-path statement elsewhere in spec/ and it does not.

FACT: The staged spec block is unchanged in substance since round 2. `diff` of the r2 snapshot against the current file shows a single hunk: the addressee gloss "and addresses the pod's adapter process" was deleted from the pod-scope clause. r3 and r4 snapshots are byte-identical to the current proposal apart from the review log, so round 3's fix stage edited nothing. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r2 vs proposals/0075_.../0075_....spec-changes.md:11-12

FACT: No fence-RPC amplification is created, because the per-session fence is already the specification's own statement, independent of the §4.1 classification word. Handoff sends one `CoordinatorFence(session_id, new_generation)` per session and the pod holds `last_fenced_generation` per bound session. — EVIDENCE: spec/10_gateway-internals.md:38, :40

FACT: The replacement gate D2 describes passes on the shipped proto in both directions, so it does not land red. Every non-comment `session_id` field in `schemas/lenny-adapter.proto` is declared `SessionId session_id`, and a grep for `SessionId <name>` field declarations whose name is not `session_id` returns nothing; there are 26 such fields (25 RPC request messages plus `CheckpointStart`). — EVIDENCE: `grep -nE "^\s*(repeated\s+)?SessionId\s+[a-z_]+" schemas/lenny-adapter.proto` filtered against `SessionId session_id` returns zero rows; count is 26.

FACT: Retiring the §4.1 table strands no failure-path statement. Every `CoordinatorFence` site in `spec/` and `docs/` describes the handoff, hold state, or the RPC catalog and none of them cites the classification: spec/10:38-40, :57-58; spec/28:239, :251, :314, :1689, :1812; spec/07:194-195, :215, :222; spec/04:712; spec/11:216; spec/18:238; spec/29:623, :820, :1273, :1305, :1525; docs/reference/adapter-contract.md:69. The only per-message scope declarations outside the retiring table are `spec/04_system-components.md:725` and `:726` in the §4.7.1 adapter→gateway table, which SPEC-1 does not touch. — EVIDENCE: spec/04_system-components.md:726 ("The request is pod-scoped."), spec/04_system-components.md:175, :188

USEFUL [spec.1.review-performance.1]: its WATCHOUT on the pod-in-hold-state-with-no-bound-entry case saved me from re-filing 0076's landed behavior as a failure-mode regression here. I reached the same tempting conclusion independently from spec/10:57 ("it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state") against `s.boundSlotState` in the handler, and stopped on that entry. The remedy, if any, is in spec/10 or proposal 0080 §1.16, and §6 of this proposal names the acceptance predicate a non-goal.

WATCHOUT: Do not construct a "top tier fence storm" finding from `spec/28_communication-channels.md:314-318` or spec/10:38. Both already read per session, so the classification word this proposal changes moves no rate. The only pod-scoped survivor on this surface is the hold gauge, which spec/10:60 keeps pod-scoped explicitly.


### [spec.4.review-reliability.1]

DECISION: returned an empty findings list for the reliability lens — BECAUSE the staged spec surface is one block in `spec/04` §4.1 that changes a classification and nothing operational: no retry, lease, fence-acceptance, drain, or dedup predicate is written, moved, or removed by it, and every recovery statement that touches the fence lives in `spec/10` §10.1 and `spec/28` §28.5.1/CH-FENCE, which SPEC-1 does not edit and which do not restate §4.1's class — ALTERNATIVES: filing the CoordinatorFence retry-idempotency hole (see FACT below) and filing the un-gated "the addressing frame opens the stream" clause; both rejected, the first because it is wrong before and after SPEC-1 so its fix lands in `spec/10` rather than in this staging, the second because it is hypothetical-future-proto hardening the rubric excludes.

FACT: `spec/28_communication-channels.md:314-318` (CH-FENCE "Messages") ALREADY describes the fence as session-addressed: "The pod records the generation against the session the fence names ... A fence for one session does not change the generation the pod holds for another." So the reclassification SPEC-1 lands has no missed edit site in §28, and the current spec is self-contradictory today between `:314` and `spec/04_system-components.md:188`. Do not file §28 as an edit site — EVIDENCE: spec/28_communication-channels.md:314-318; spec/04_system-components.md:188

FACT: no spec, docs, schema, or chart surface outside `spec/04` §4.1 declares `CoordinatorFenceRequest`'s scope. `spec/04:712` (the §4.7.1 RPC row) states no scope, unlike its siblings `:725` (`ReportSessionScrub` "The request is session-scoped") and `:726` (`ReportPodScrub` "The request is pod-scoped"). `grep -rn "pod-scoped\|session-scoped" docs/` returns no page classifying any gateway-adapter request message. §1.4's "no reader-facing page changes" holds — EVIDENCE: spec/04_system-components.md:712, :725, :726

FACT: nothing anchors into `#### Request Message Scope`. `grep -rn "request-message-scope\|Request Message Scope" spec/ docs/ tests/ pkg/ cmd/ schemas/ charts/` returns the heading itself and nothing else, so retiring the table dangles no cross-reference — EVIDENCE: spec/04_system-components.md:149

FACT: the whole proto has exactly two `oneof` blocks, `CheckpointRequest` at :1174 and `CheckpointResponse` at :1250, and the name/type co-occurrence holds in both directions (no field named `session_id` of another type, no field of type `SessionId` under another name) across the entire file, responses included. So D2's replacement gate passes on the tree as it stands and does not land tier 0 red — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250; `grep -nE "^\s*(repeated\s+)?SessionId\s+[A-Za-z_]+\s*=" | grep -v session_id` returns nothing

FACT: the fence handler already fails closed exactly as the derived session class requires: empty id → `InvalidArgument` before any resolution, then `boundSlotState` before the generation compare. The reclassification therefore imposes no new adapter obligation and creates no new refusal — EVIDENCE: pkg/adapter/coordination.go:108-119

UNVERIFIED: `spec/10_gateway-internals.md:39` tells a new coordinator to retry a failed/timed-out `CoordinatorFence` "with the same generation value (up to 3 attempts)", but the adapter rejects `gen <= lastFenced` with `coordinator_handoff_stale` once the first attempt has been recorded (pkg/adapter/coordination.go:127-135). A fence that lands but whose ack is lost at the 5s deadline therefore burns all three retries and drives the coordinator to relinquish the lease. This is pre-existing and independent of 0075 — its fix is in `spec/10`, not in this staging — but no draft appears to own it. Whoever runs the residue register (proposal 0080) should check whether it is inventoried — EVIDENCE: spec/10_gateway-internals.md:39; pkg/adapter/coordination.go:127-135

DEFERRED [0075...status.md, 0075...summary.md]: both cite a "§11" that records what the rewrites changed (status.md:22-23 "§11 records what the rewrites changed"; summary.md:15 "§11 records that"). No `## 11` section exists in any file of this proposal — the section list runs 2, 3, 4, 5, 6, 7, 9, 10 in spec-changes.md plus 1 and 8 in the other two files. Either the section is restored or both references are dropped. Not filed: the remedy is in files this loop may not edit.


### [spec.4.review-security.1]

DECISION: Returned an EMPTY findings list under the security lens, for the second consecutive round — BECAUSE the r4 snapshot is byte-identical to r3 for all seven proposal files (only the review log grew), and I re-derived the load-bearing security facts from the tree rather than trusting [spec.3.review-security.1]: the derived session class is a strict SUPERSET of the retired table's session class, so nothing moves session→pod and no fail-open direction opens; the replacement gate passes on today's proto in both directions; and the reclassification only ADDS the spec/05:515 empty-identifier refusal, which the landed handler already satisfies — ALTERNATIVES: the nested-address hole and the pod-wide-hold-exit isolation question, both declined again for the reasons the r2 and r3 shards record.

FACT: Verified byte-identity of spec-r3 and the live proposal directory with `cmp` on each of the seven non-log files. `diff -ru` against `spec-r4` is also empty (the r4 snapshot is taken at round start). The "read the changed sections hardest" reading order had nothing to point at this round either. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-r3 vs proposals/0075_fix_derive-message-scope-from-the-address-type

FACT: The security-decisive property of the derivation, stated precisely so no future round has to re-derive it: table session rows = 26 (24 addressed requests + `CheckpointRequest` + `CheckpointStart`); derived session class = 25 addressed requests (the 24 plus `CoordinatorFenceRequest`) + `CheckpointRequest` + `CheckpointStart` = 27. The delta is exactly one message, in the pod→session direction. Because the spec/05:515 refusal hangs off the session class, the derivation can only ADD refusals on the current protocol definition, never remove one. This is what makes the retirement safe independent of section 7 question 1's answer. — EVIDENCE: spec/04_system-components.md:155-186; schemas/lenny-adapter.proto:1456, :1217

FACT: Both single-half deviations from the addressing convention are fail-open and both are caught by D2's gate, which is why the gate is a real control and not decorative. `string session_id` fails clause 1's type half so the message derives pod-scoped and LOSES the empty-identifier refusal; `SessionId sid` fails the name half the same way. The gate refuses either. Only the both-halves-unconventional case survives, which is what the staged constraint paragraph and section 7 both name. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:10-11, :20-25; spec/05_runtime-registry-and-pool-model.md:515

FACT: `pkg/adapter/oplock.go:77` says "An interrupt is pod-scoped", while the §4.1 table classifies `InterruptRequest` session-scoped and the proto declares `SessionId session_id` on it. This is NOT a contradiction and is NOT an edit site: the comment is about which op lock the handler takes (the pod lock), which is the same address-versus-effect divergence the surviving `ShutdownRequest` paragraph at spec/04:190 explains. A reviewer grepping `pod-scoped` across `pkg/` will hit this first; do not file it. — EVIDENCE: pkg/adapter/oplock.go:77; spec/04_system-components.md:190

FACT: `schemas/lenny-adapter.proto:1449-1454`, the `CoordinatorFenceRequest` doc comment, ALREADY reads per-session ("The pod records the generation against the session the fence names ... rejects every RPC carrying a generation older than the one it holds for that session"). So `schemas/` is not an unstaged edit site for the reclassification, and §9's spec-side list of one file is complete on the schema surface too. I checked this specifically because a proto doc comment restating the retired class would have been a clean (d) finding. — EVIDENCE: schemas/lenny-adapter.proto:1449-1454

USEFUL [spec.3.review-security.1]: its three FACTs (the three consumers of the classification in `spec/`, the CH-FENCE card already reading per-session, and the "§4.2 value rule" reference resolving to proposal 0073's own §4.2 rather than spec/04 §4.2) each held on independent re-check and saved a round of chasing. The "§4.2" one in particular looks like a false spec citation and is not.

USEFUL [spec.3.review-security.1]: its WATCHOUT on spec/10_gateway-internals.md:57 (a session-scoped fence exiting a pod-wide hold state) is the single most tempting security finding on this proposal. I re-derived it independently and reached the same disposition: 0076's OD3 weighed and rejected exactly that argument, D3 records the rejection, and §6 makes re-deriving OD3 a non-goal. It is also unchanged by SPEC-1, which edits no handler and states nothing about hold state. Do not spend a fourth round on it.

WATCHOUT: `spec/10_gateway-internals.md:38-40` describes the fence as `CoordinatorFence(session_id, new_generation)` and holds `last_fenced_generation` "per bound session", and `spec/28_communication-channels.md:314-317` says the same. So `spec/04:175` and `:188` contradict TWO other spec sections today, not only the code. If §7 question 1 ends in withdrawal, the standalone reclassification is compelled by spec-vs-spec contradiction and is not optional. — EVIDENCE: spec/10_gateway-internals.md:38, :40; spec/28_communication-channels.md:314-317; spec/04_system-components.md:175, :188

### [reconcile.1]

DECISION: Rebuilt the summary's deliverable index and wrote the checklist's SPEC-lane step against the current staged ids, then discharged the deferred corrections whose remedy lands in the summary, the checklist, or the staged non-spec changes — BECAUSE the staged set is SPEC-1, TEST-1, and TEST-2, the checklist described a narrower spec edit than SPEC-1 now stages, and an entry left deferred at this point is left deferred for good — ALTERNATIVES: merging S1 and S2 to close the tier-0 red window, rejected because one lane per step bars a step naming a spec deliverable and a test deliverable together, so S1's tier line records the disposition instead.

CORRECTS [spec.1.fix-G1.1, deferred on the implementation checklist]: S1 said SPEC-1 rewrites or removes "the three sentences that ground the declared classification". S1 now says SPEC-1 replaces the whole `#### Request Message Scope` block, naming the introducing paragraph at `spec/04_system-components.md:151`, the table at `:153-186`, and the grounding paragraph at `:188`, and leaving the `ShutdownRequest` paragraph at `:190` standing. No step was added, removed, or resequenced.

CORRECTS [spec.1.review-applicability.1 and spec.3.review-applicability.1, deferred on the implementation checklist]: S1 carried "Tiers 0, 11" with no disposition for the window in which tier 0 is red. Merging S1 and S2 is barred by the one-lane-per-step rule, so S1's tier line now records that tier 0 is red from the end of S1 until S2 replaces the gate, because deleting the table leaves `parseMessageScopeTable` with no rows and the retiring gate then reports a missing row for every declared request message.

CORRECTS [spec.1.fix-G2.1, deferred on the implementation checklist]: S2 described TEST-1 as replacing the tier-0 table-reconciliation gate and named that file alone. S2 now names TEST-1's whole target set, which is the gate file, the shared proto parse, the parse's other caller, and `tests/spec-map.json`, and states that the spec-map re-registration lands inside S2 rather than as a step of its own, because tier 0 is red between retiring the old function names and registering the new ones.

CORRECTS [spec.1.fix-G1.1 and spec.2.review-edit-sites.1, deferred on the staged non-spec changes]: TEST-1 named only `declaredScope` (`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`) as losing its subject with the table, and §8 asked for one tier-0 negative case against a four-clause gate. TEST-1 now names `parseMessageScopeTable` (`:54`) alongside `declaredScope`, and §8 now asks for one negative case per clause: a field named `session_id` that is not of type `SessionId`, a field of type `SessionId` under another name, a stream envelope declaring a top-level address of its own, and a stream envelope whose frames declare zero addresses or two. Both are the content the G1 design derived; nothing beyond it was written.

CORRECTS [spec.1.review-citations.1, spec.2.review-applicability.1, and spec.4.review-fresh.1, deferred on the staged non-spec changes]: TEST-2 staged deleting "the coverage clause at `:40-43`", which leaves the membership sentence beginning at `:39` ending mid-clause on a table SPEC-1 deletes. TEST-2 now stages `:39-43` and restates membership as the derivation rule together with the message having carried the retired duplicate, which is the remedy [spec.1.review-mechanism.1] and [spec.2.review-fresh.1] derived for the same sentence; those two entries are discharged by the same staged instruction. Verified against the tree: the membership sentence runs `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43` and the exclusion clause `:40-43`.

CORRECTS [spec.1.review-fresh.1, spec.1.review-mechanism.1, and spec.3.review-fresh.1, deferred on `tests/tier0_static/adapter_proto_parse_test.go`]: the shared parse's package doc (`:10-15`) and the comment on `protoServiceRequests` (`:64-67`) both describe their subject as the §4.1 classification table, and TEST-1 named neither. TEST-1 already stages an extension of that file, so the restatement now rides along in TEST-1's text; the file itself is edited by the implementor rather than here.

OPEN: adding `CoordinatorFenceRequest` to `sessionScopedMessages` cannot be settled by choosing a value for the retired-field-number column, which is how §4 and §7 question 2 frame it. `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates the same map and asserts both `reservesNumber` and `reservesName("slot_id")` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141`), and the fence declares neither (`schemas/lenny-adapter.proto:1455-1461`), so the name half fails whatever number is chosen. The two candidates [spec.3.review-feasibility.1] derived are splitting the map into a session-scoped set and a carried-the-retired-duplicate set, or adding the fence to a different arm; it chose neither, so this is a design choice rather than a correction to apply. Lands in the staged non-spec changes §4 and TEST-2. Carried into the summary as OD2.

OPEN: `docs/reference/adapter-contract.md:69` describes `CoordinatorFence` as the "precondition for any subsequent operational RPC", which reads as a pod-wide gate, while after 0076 the fence and the generation are per bound session. The same imprecision sits in its source at `spec/04_system-components.md:713`. Neither is staged here. Lands in `docs/reference/adapter-contract.md` and `spec/04_system-components.md`, through whoever takes the 0076 residue that proposal 0080's inventory registers. Carried from [spec.1.review-docs-alignment.1].

OPEN: proposal 0080 §2 records "the §4.1 `ShutdownRequest` classification limit that shares its cause" as taken by this proposal. This proposal does not take it: SPEC-1 leaves the `ShutdownRequest` paragraph at `spec/04_system-components.md:190` unedited, and the replacement gate checks the protocol definition's addressing convention rather than what a handler does, so 0073's recorded limit that the gate cannot check a declared scope against the handler survives unchanged. Lands in `proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md`, which is a Draft. Carried from [spec.2.review-citations.1].

OPEN: D2 calls the addressing convention "an addressing convention that nothing checks today", which overstates it. `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` already asserts, for each member of `sessionScopedMessages`, that `session_id` exists and is of type `lenny.adapter.v1.SessionId` (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:99-116`). What is true is that no gate checks the convention in both directions over every message the protocol declares, and the existing tier-3 check covers one direction over a hand-maintained subset. Lands in the staged spec changes D2, which this pass may not edit. Carried from [spec.4.review-citations.1].

OPEN: the summary (`:15`) and the status file both cite a "§11" for what the rewrites changed. The only `## 11` in this proposal is in this review log, so the references resolve across files rather than within the document a reader is holding. Settling it means either naming the review log in both references or dropping both. Lands in the status file, which this pass may not edit, and in the summary. Carried from [spec.4.review-reliability.1].


### [f1.human-decisions]

DECISION: OD1 keeps its identifier and is rewritten in the summary as a decidable question with a recommendation, its ground, two losing alternatives, the cost of a "no", and a confidence. The recommendation is yes, accept the residuals and keep the retirement, at moderate confidence. The disposition stays the human's and no answer is staged: the withdrawal branch survives as the "no" the entry prices. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Open decisions for human to make`, entry OD1.

FACT: Both residuals §7 question 1 carried survive the migration into OD1, so the entry prices the trade as the removed question did. The fail-open one is the message addressed under an unconventional name and an unconventional type at once. The fail-closed one is a pod-scoped guard given the session address, which then inherits the refusal of a session-scoped request with an empty identifier; that rule landed at `spec/05_runtime-registry-and-pool-model.md:515`, and OD1 cites that site rather than "0073's §4.2 value rule", which resolves to 0073's own section rather than to `spec/04` §4.2. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515

DECISION: Migrated §7 question 1 out of `spec-changes.md` and deleted it there, renumbering the surviving retired-field-number question to 1 — BECAUSE the summary's open-decisions section is the one home for an open decision, and a staged change file states what an implementor builds. The two cross-references to it in the same file went with it: D2's closing sentence ("Whether that residual is smaller than what the retired table cost is §7's first question and the central risk of this proposal") is deleted, and D4's "survives as §7's first question" is restated as the argument itself. D2's preceding sentence stating the gate's limit stands, because it states what the staged rule carries rather than the question.

FACT: The specification's own ground for declaring rather than deriving is spent, which is the recommendation's first premise and is derivable rather than judged. `spec/04_system-components.md:151` gives the reason as "`session_id` appears on messages of both classes". Every declaration of that field in the protocol definition is a `SessionId session_id` field, and the only one on a message the table calls pod-scoped is `CoordinatorFenceRequest`, which 0076's OD3 reclassified. The other pod rows declare no session field at all. — EVIDENCE: schemas/lenny-adapter.proto:1455-1456, :499-503; a scan for a field named `session_id` of another type, and for a field of type `SessionId` under another name, returns nothing in either direction.

FACT: The retiring gate's refusals other than the missing row (a row naming a message neither service declares, a row naming the wrong service, a row declaring neither class, and a message classified twice) check the table against itself, so they lose their subject with the table rather than leaving a hole the derivation has to fill. Only the missing-row case is what OD1 weighs. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:96-124, :178-202

WATCHOUT: The recommendation's confidence is moderate because one side of the comparison is derivable from no file. The table forced a human to classify each new request message before tier 0 went green, and whether losing that checkpoint matters is a prediction about future authors. Do not upgrade it to high on the strength of the derivable half alone.

OPEN: `summary.md:7` still reads "the open questions in §7 are open", pointing a reader at the staged spec changes for the open decisions. One question remains in §7 after this migration, so the sentence is not yet false, and it was left as found. Whoever migrates the retired-field-number question out of §7 should retire that pointer and the empty section together.

### [f1.human-decisions]

DECISION: OD3 is answered no, and the answer is staged into SPEC-1 rather than left as an absence. The rewritten §4.1 block names no request message and states nothing about the coordinator hold; §10.1.4 owns both halves of the fact. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.spec-changes.md`, SPEC-1, three sentences after the `ShutdownRequest` sentence; the OD3 entry is deleted from `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Open decisions for human to make`, with OD1, OD2, and OD4 keeping their identifiers.

FACT: Both halves of the hold fact are in the applied specification, in the section that owns hold state. `spec/10_gateway-internals.md:57` states that a `CoordinatorFence` from a new coordinator is the only way to exit hold state, and `:60` states that the hold and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped. Both sit under `#### 10.1.4 Coordinator-Loss Detection and Hold State` at `:53`. Nothing is lost by §4.1 staying silent. — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60

FACT: The two sentences have different provenance and the staged text now says so. `:60` is proposal 0076's SPEC-1 text (`git log -L 60,60:spec/10_gateway-internals.md` returns cf20a0646, "Apply proposal 0076 SPEC-1 to spec/10"). `:57` predates 0076 (db8cdc224). A first draft of the inserted sentences credited both to 0076's D5 and was corrected before landing. — EVIDENCE: git log -L 57,57 and -L 60,60 on spec/10_gateway-internals.md

FACT: 0076's instruction to rewrite the declaring sentence has no surviving host. `spec/04_system-components.md:188` is that sentence, and SPEC-1 deletes it with the table (`spec-changes.md`, SPEC-1, naming `:151`, `:153-186`, and `:188` as the replaced block). §4.1 has never carried a hold-exit sentence: `:188` grounds the pod classification on the message carrying `session_id` and mentions no hold. Answering no therefore preserves §4.1 as it stands rather than removing a statement from it. — EVIDENCE: spec/04_system-components.md:151, :188, :190

FACT: 0076's recorded answer binds the classification alone. Its answer table reads "Yes. `CoordinatorFenceRequest` is session-scoped after CODE-1", sourced to the entry's recommendation; the hold-exit clause lives in the recommendation prose and was drafting advice written under the premise of a surviving table. Question B in the same table records that the successor "retires the table those sites sit in", so the reviewer answered knowing the table would not survive. — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:185, :186, :258-259

WATCHOUT: Do not add a fence paragraph to the rewritten §4.1 on a later round. The staged block's opening sentence is that the classification is "derived from the message's field set rather than declared per message", and a paragraph naming one message's pod-wide side effect puts back the per-message prose the rule replaces. The surviving `ShutdownRequest` paragraph at `spec/04_system-components.md:190` already licenses the general reading that a session-scoped message's handler may have a whole-pod effect. — EVIDENCE: `0075_fix_derive-message-scope-from-the-address-type.spec-changes.md`, D1's first staged paragraph; spec/04_system-components.md:190

### [f1.human-decisions]

DECISION: OD4 is answered "they stand", and the answer is staged as a paragraph in SPEC-1 rather than left as the absence of an edit, so the site is not re-filed a fourth time (`review-log.md:740` and `:1046` are the earlier sightings). The two §4.7.1 clauses are kept, §4.7.1 stays outside SPEC-1's edit scope, and no file joins §9. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.spec-changes.md`, SPEC-1, a new paragraph after "the gate TEST-1 replaces." carrying the sentence "Written against whatever 0076 leaves in §4.1."; the OD4 entry is deleted from `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Open decisions for human to make`, with OD1 and OD2 keeping their identifiers.

FACT: Both §4.7.1 clauses are true under the staged derivation, so neither is falsified by SPEC-1. `ReportSessionScrubRequest` declares `SessionId session_id = 2`, which D1's predicate makes session-scoped; `ReportPodScrubRequest` declares `string pod_id`, `PodScrubOutcome outcome`, and `string detail`, with no field of the address type, which makes it pod-scoped. — EVIDENCE: schemas/lenny-adapter.proto:456-458, :499-503; spec/04_system-components.md:725, :726

FACT: A tier-11 gate pins the wording of `spec/04_system-components.md:725` on two carriers. It reads the `ReportSessionScrub` row out of §4.7 and out of `docs/reference/adapter-contract.md` and requires both to contain "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." Striking that clause turns tier 11 red and drags in a documentation edit and a gate edit that §9 does not carry. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33, :43-76; docs/reference/adapter-contract.md:81

FACT: After SPEC-1 retires `spec/04_system-components.md:151` and `:188` with the table, `:726` is the only sentence left in `spec/` that classifies one request message pod-scoped. A grep for `pod-scoped` across `spec/` returns those three sites plus `spec/04_system-components.md:872`, which is about the rotation ceiling and classifies no message. — EVIDENCE: spec/04_system-components.md:151, :188, :726, :872

WATCHOUT: The proposal's own staging is what settles this. SPEC-1 keeps `spec/04_system-components.md:190` unedited, and that paragraph opens "`ShutdownRequest` is session-scoped and carries one address", which is a per-message scope declaration surviving inside the block the rule replaces. A later round that reads the derived method as forbidding per-message restatements has to reckon with that retention first. — EVIDENCE: `0075_fix_derive-message-scope-from-the-address-type.spec-changes.md`, SPEC-1; spec/04_system-components.md:190

### [f1.out-of-scope-defects]

DECISION: The equal-generation fence refusal stays unstaged and is recorded as one entry under `## Defects in the shipped tree that this proposal does not stage`, whose lead moves from "None." to "None blocks sign-off." in the form proposal 0076's summary uses. Reclassifying `CoordinatorFenceRequest` does not oblige this proposal to state the fence's acceptance predicate: the predicate lives in the handler's comparison and in §10.1.2's handoff protocol, neither of which is a message-scope classification, and no staged artifact reads it. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`, `## Defects in the shipped tree that this proposal does not stage`.

FACT: The defect is live in the tree after 0076. `spec/10_gateway-internals.md:39` orders the new coordinator to "retry the fence RPC with the same generation value (up to 3 attempts with 1-second backoff)", and `CoordinatorFence` refuses `gen <= st.coord.lastFenced` with `FailedPrecondition` and a `coordinator_handoff_stale` detail, now on the per-session entry. — EVIDENCE: spec/10_gateway-internals.md:39; pkg/adapter/coordination.go:127, :134

FACT: The one behavioral obligation session scope carries is already met. `spec/05_runtime-registry-and-pool-model.md:515` rejects a session-scoped request whose session identifier is empty, and the handler returns `InvalidArgument` for that case before it resolves any slot. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515; pkg/adapter/coordination.go:109-111

WATCHOUT: The reading behind this item cited proposal 0076's OD14 as 0080 §1.16's source. That is wrong. §1.16's own `**Source:**` line names 0076's OD2 (the fence-driver conflation entry); OD14 is the migrate Job run-time budget, which is 0080 §1.21. The summary entry written here cites OD2. — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:264-265, :446; proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:189

WATCHOUT: The reading's suggested anchor `pkg/adapter/coordination.go:127-134` is correct today, and 0080 §1.16's own anchor `:99` has drifted by 0076's CODE-1 moving the state onto the slot entry. That drift is 0080's to fix; nothing here edits it. — EVIDENCE: pkg/adapter/coordination.go:107, :127

FACT: No staged deliverable is added, removed, merged, split, or resequenced by this entry, so the implementation checklist is untouched and needs no correction.


### [f1.other-proposals]

DECISION: The 0073 row in the summary's `## Impacts on other proposals` is kept and widened rather than left as found, because it named what the retirement takes from 0073 and not what it leaves. It now carries the status date, the `ShutdownRequest` paragraph as a deliberate survivor, and 0073's own recorded gate limit as a survivor — BECAUSE the row is the only place this proposal states what a landed proposal loses and keeps, and a reader of the narrower row could take the whole `#### Request Message Scope` block as going — ALTERNATIVES: dropping the row (0075 retires text and a gate a landed proposal authored, and the retiring proposal's own text is where that is recorded); leaving it as found (it under-describes the survivors, which is what a future implementor would delete by mistake).

FACT: 0073 is Implemented and the date the status rests on is 2026-08-31. `proposal-status.mjs` returns `Implemented` for the legacy single-file proposal, its own header states "Implemented (2026-08-31), independently verified on tiers 0, 3, and 11", and the newest commit touching the file is HEAD's cdcd7e9e1, dated 2026-08-31 — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:3; `git log -1 --date=short -- proposals/0073_...md`.

FACT: The `ShutdownRequest` paragraph at `spec/04_system-components.md:190` was staged by the same 0073 SPEC-7 block as the table, and SPEC-1 leaves it unedited on purpose — EVIDENCE: spec/04_system-components.md:190; 0075...spec-changes.md SPEC-1 ("The `ShutdownRequest` paragraph at `:190` stands unedited").

FACT: 0073's recorded limit survives the retirement. 0073 records that the tier-0 classification gate cannot check that a declared scope covers everything the handler does with the request, and the replacement gate D2 states reads `schemas/lenny-adapter.proto` alone, so it reaches no handler either — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6713; 0075...spec-changes.md D1 (the constraint paragraph) and D2.

FACT: No BUILD-GAPS.md finding names the message-scope table or its gate, so retiring them closes nothing there and the row owes no BUILD-GAPS entry — EVIDENCE: `grep -c "message-scope\|Request Message Scope" BUILD-GAPS.md` returns 0.

FACT: No staged deliverable is added, removed, merged, split, or resequenced by this entry, so the implementation checklist is untouched.

OPEN: proposals/0080...md:453-454 records "D6's declared message-scope table **and the §4.1 `ShutdownRequest` classification limit that shares its cause**" as taken by 0075. 0075 takes the table and the gate; it does not discharge that limit, which the 0073 row now says survives. The summary's 0080 row is held by another item and was left as found, and 0080 is another proposal's file. Whoever owns the 0080 row should decide whether it states the divergence — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:453-454; 0075...summary.md, the 0073 row.

WATCHOUT: The summary's "Watch out for" paragraph still calls 0073 "converged" where the impacts row now says Implemented (2026-08-31). The two do not disagree, so nothing was edited, but a later round tightening that paragraph should use the row's wording — EVIDENCE: 0075...summary.md, `**Watch out for.**`.


### [f1.other-proposals]

DECISION: The summary's 0080 row now names §2's two entries separately and states which half of each this staging takes — BECAUSE §2 "Already owned, and therefore out of scope here" is the register a reader consults to learn which of 0073's residues have a home, and one of its two bullets is half-false against the current staging in a way that leaves a named residue owned by nobody — ALTERNATIVES: leaving the row as found (it under-counted the entries and recorded no mismatch, which is the failure the 0080 inventory exists to prevent); filing the mismatch as an out-of-scope defect instead (the section holds defects in the shipped tree, and this is a claim in another draft).
FACT: 0080 is a legacy single-file proposal with no status file. `proposal-status.mjs --json` reads its status as `Draft` from the header `EARLY DRAFT, NOT CONVERGED`; the document's own `**Date:**` is 2026-08-31 and its last commit is a5476f93e (2026-09-06). The row carries the commit date and says what it is — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:1-8; git log -1 on that path.
FACT: 0080 stages nothing, so no deliverable of its can lose a subject. "This document stages no changes. It modifies no spec, code, doc, or register file" — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:15-17.
FACT: §2's bullet at :458-460, the false tier-3 coverage clause for `CoordinatorFenceRequest`, is taken in full. TEST-2 adds the message to `sessionScopedMessages` and deletes the clauses at `session_address_wire_test.go:39-43`, which are the membership sentence keying on the retired table and the coverage claim — EVIDENCE: 0075...non-spec-changes.md, TEST-2; proposals/0080_...:458-460.
FACT: §2's bullet at :453-454 is taken only for the table. SPEC-1 keeps the `ShutdownRequest` paragraph at `spec/04_system-components.md:190` unedited on purpose, and 0073's recorded limit at proposals/0073_...:6713 has a second clause the replacement gate is equally unable to hold: the gate reads `schemas/lenny-adapter.proto` alone and cannot relate a declared scope to what a handler does. The paragraph at `:190` reads as cited and the divergence it records is live in the tree, since `ShutdownRequest` declares the address and the same handler runs the recycle scrub — EVIDENCE: spec/04_system-components.md:190; proposals/0073_...:6713; 0075...spec-changes.md, SPEC-1.
FACT: 0080 §1.16 is untouched by this staging and the existing clause about it stands. §1.16 is "The pod refuses the fence retry §10.1.2 orders", the fence's acceptance predicate, which §6 names a non-goal and §10 states is independent in either order — EVIDENCE: proposals/0080_...:216; 0075...spec-changes.md:149, :181.
DEFERRED [proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md]: §2's bullet at :453-454 splits. The declared message-scope table and its reconciliation gate stay on the owned side; the §4.1 `ShutdownRequest` classification limit returns to §1's inventory as a gap no proposal takes. 0080 is another proposal's file and a Draft, so the correction lands there. This supersedes the same deferral recorded at [spec.1.review-completeness] and the OPEN lines at the ends of the citation and cross-proposal passes; the summary's 0080 row is now the notice.
FACT: no other row in `## Impacts on other proposals` and no statement elsewhere in the summary asserts anything about 0080's continued validity, so nothing moved into the section. The `## Defects in the shipped tree that this proposal does not stage` entry cites 0080 §1.16 as where the remedy is stated, which is a pointer rather than a validity claim, and it stands.


### [f1.cleanup]

DECISION: The summary is left carrying exactly the eight listed sections in order, and the three unlisted paragraphs that stood between the title and `## Summary` are gone from it — BECAUSE a document lead is not one of the listed sections and this pass relocates rather than deletes, so each of the three left by the route its kind fixes: the 0076 paragraph merged into the existing 0076 row of `## Impacts on other proposals`, and the other two are recorded below. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`.

DECISION: The 0076 paragraph merged into the row already in `## Impacts on other proposals` rather than becoming a second row — BECAUSE that section is the one place this proposal asserts anything about another proposal's continued validity, and the paragraph and the row agreed on every claim they shared. The row now carries 0076's OD3 Question A answer, the Question B succession the row already stated, and the consequence that an earlier revision's schema, code, and documentation deliverables are dropped. — ALTERNATIVES: adding a second 0076 row (two places asserting one thing is the drift the section exists to prevent); leaving the paragraph where it stood (it is unlisted content and this file's section list is closed).

FACT: The paragraph and the row did not disagree. The paragraph's Question A claim is 0076's own recorded answer, "Yes. `CoordinatorFenceRequest` is session-scoped after CODE-1", and its Question B claim is 0076's "Proposal 0075 is that successor; it retires the table those sites sit in". The dropped deliverables are SCHEMA-1, CODE-1, and DOCS-1. — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:185, :186; this log, `## 11. What the 2026-09-06 rewrites changed`.

FACT: OD2 stays listed as the human's. Nothing in the staged changes answers it: the staged spec changes §7 still carries "What the retired-field-number column holds" as its one open question, and the staged non-spec changes §4 states that adding the fence to the map "requires deciding what that column holds". Its entry keeps the identifier `OD2` and its text is unchanged, including the absence of a recommendation, which is what the loop reached. OD1 likewise keeps its identifier and its text. OD3 and OD4 had already left the section. — EVIDENCE: `0075_fix_derive-message-scope-from-the-address-type.spec-changes.md` §7; `0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md` §4.

FACT: No section preamble was falsified by these moves. `## Defects in the shipped tree that this proposal does not stage` opens on a lead describing one further defect left where it is, and the section still carries exactly that one entry. `## Open decisions for human to make` and `## Impacts on other proposals` carry no preamble. `## Deliverable index` is unchanged line for line.

OPEN: the summary's opening paragraph read "This document stages the proposed specification and test changes. It does not modify any spec, code, or doc file. Apply the changes in the 'Proposed changes' section after sign-off." It stood between the title and `## Summary`, no listed section covers it, and its pointer resolves to another file, since the summary carries no `## 5. Proposed changes` and the staged spec changes and staged non-spec changes each carry their own. Whoever wants that standing disclaimer on this proposal has to decide which file states it.

OPEN: the summary's second paragraph read "**This draft has not been through adversarial review.** It records a direction and the measurements behind it. The staged edits are indicative rather than final, and the open questions in §7 are open. Run the change-proposal convergence loop on it before sign-off." It stood between the title and `## Summary`, and no listed section covers it. Its first sentence is false against this log, which records the `spec.1` through `spec.4` review rounds and a resolved adversarial pass dated 2026-09-07. Its §7 pointer was still true when it was removed, because one question remains in the staged spec changes §7. This discharges the summary half of the OPEN at [f1.human-decisions] on `summary.md:7`: the sentence has left the summary, and the §7 section it pointed at is unchanged.

DEFERRED [0075_fix_derive-message-scope-from-the-address-type.status.md]: the status file's `**Date:**` line reads "§11 records what the rewrites changed". The only `## 11` in this proposal is in this review log, so the reference resolves across files rather than within the document a reader is holding. The summary's copy of that pointer went with the 0076 paragraph this pass merged, so the status file is now the only carrier. This pass may not edit the status file. This supersedes the summary half of the OPEN recorded at [reconcile.1] on the same pointer; the status half stands as recorded there.

WATCHOUT: Do not restore a lead paragraph above `## Summary` on a later round. The file's section list is closed at the eight headings it now carries, and a banner about the draft's review state, its staging disclaimer, or its dependency on another proposal has a listed home: the dependency belongs in the row in `## Impacts on other proposals`, and the review state belongs in the status file.



### [spec-recheck.1.review-applicability.1]

DECISION: Returned no findings on the spec staging — BECAUSE every anchor, quote, and derived claim in `0075...spec-changes.md` resolves against the tree today, and simulating SPEC-1 end to end leaves §4.1 self-consistent with no dangling reference — ALTERNATIVES: filing the "`the classification is derived ... rather than declared per message`" vs the surviving per-message declarations at `spec/04_system-components.md:725` and `:726` (both AGREE with the derivation, so nothing false or unworkable results, and SPEC-1 already argues the case explicitly); filing the loss of the retired table's Service and Direction columns for the 11 RPCs §4.7 does not list (nothing becomes false; the proto declares both).

FACT: Every anchor SPEC-1 names is exact on the current tree: `#### Request Message Scope` heading at `spec/04_system-components.md:149`, introducing paragraph `:151`, table `:153-186` (32 rows), fence row `:175`, grounding sentence `:188`, `ShutdownRequest` paragraph `:190`, §4.2 heading `:192`. No other line in §4.1 (`:40-191`) refers to the table, so the block replacement leaves no dangling "the table below". — EVIDENCE: spec/04_system-components.md:149-192

FACT: The proto anchors an earlier round found stale by 7 have been refreshed and are now exact: `message CheckpointRequest` :1173, `message CheckpointStart` :1193, `SessionId session_id = 7` :1217, `message CoordinatorFenceRequest` :1455 with `SessionId session_id = 1` :1456, `message SessionId` :596, `ReportSessionScrubRequest`'s `SessionId session_id = 2` :458, `ReportPodScrubRequest` :499-503. — EVIDENCE: schemas/lenny-adapter.proto:1173,1217,1455,596,458,499

FACT: I re-ran the §1.1 measurement independently. The two service blocks declare 31 RPCs with 31 distinct request types; 25 declare a top-level `SessionId session_id`, and the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. The 25 exactly reproduce the table's session rows once `CoordinatorFenceRequest` moves, so D1's reproduction claim holds. — EVIDENCE: schemas/lenny-adapter.proto:41-334

FACT: The addressing convention D2's replacement gate checks already holds on the shipped proto, so the gate is green the moment S2 lands: every `session_id` field is `SessionId`-typed and every `SessionId`-typed field is named `session_id` (27 sites), and `CheckpointRequest` and `CheckpointResponse` are the only messages declaring a `oneof` (`:1174`, `:1250`), with `CheckpointResponse` the request type of no RPC. — EVIDENCE: schemas/lenny-adapter.proto:1174,1250,133

FACT: The one behavioral obligation the reclassification imports, the §4.2/`spec/05:515` refusal of a session-scoped request with an empty identifier, the fence handler already meets, so §3's "no handler changes" is true. — EVIDENCE: pkg/adapter/coordination.go:109-111, spec/05_runtime-registry-and-pool-model.md:515

FACT: Retiring the §4.1 table falsifies nothing outside §4.1. A repo-wide sweep for the table, its anchor, and per-message scope words finds no other spec section, no `docs/` page, no `schemas/` file, and no `pkg/` comment that restates the classification; `spec/04:726` is the only sentence left in `spec/` classifying a request message pod-scoped, and `spec/04:190`/`:725` the only ones classifying one session-scoped. All three agree with the derivation. — EVIDENCE: spec/04_system-components.md:190,725,726

FACT: The only test artifacts naming the retiring gate's cases are `tests/spec-map.json:156`, `:169`, and `:5670`, all of which §9 lists; `protoFields`/`protoServiceRequests` have exactly the two callers §9 names. The tier-11 successor-pointer gate's domain is a named list of §4.7, §15.4, and §29.10 reducing into §28.5, so §4.1's retirement does not enter it. — EVIDENCE: tests/spec-map.json:156, tests/tier11_docs/successor_pointer_test.go:53-56

WATCHOUT: `spec/04_system-components.md` §4.7's two RPC tables list only 20 of the 31 RPCs, so the retired §4.1 table is the only place `spec/` states the service and direction of the other 11 (`SendMessage`, `SignalDeadline`, `RevokeCredentials`, `NegotiateVersion`, `GetObservedIntegrationLevel`, `AdapterEvents`, and the five `GatewayControl` tool RPCs). Nothing depends on those columns and the proto declares both, so this is not a defect, but a future reviewer will notice the gap and should not re-derive it as content loss. — EVIDENCE: spec/04_system-components.md:692-726

UNVERIFIED: Summary OD2 asserts that `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` makes TEST-2's staged edit unappliable for any field number, because it also asserts the message reserves the name `slot_id` and `CoordinatorFenceRequest` reserves neither. I confirmed the fence declares no `reserved` at `schemas/lenny-adapter.proto:1455-1461` but did not read the assertion at `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141`. The non-spec loop should verify it, because TEST-2 as written in the non-spec staging ("settling the retired-field-number column per §4") would leave tier 3 red whatever number is chosen.


### [spec-recheck.1.review-citations.1]

DECISION: Returned an empty findings list — BECAUSE every concrete citation in `spec-changes.md` was re-derived against the tree today and each resolves to text that says what the proposal claims — ALTERNATIVES: filing the "derived rather than declared per message" vs. the surviving per-message declarations at `spec/04_system-components.md:190`, `:725`, `:726` as a self-contradiction (rejected: the staging argues each surviving statement agrees with the derivation, and [f1.human-decisions] already weighed and staged that retention deliberately); filing the loose set-equality sentence in D1's follow-on paragraph ("the ones other than the stream envelope that carry the address are exactly the table's session rows") as false, since the table's session rows also include `CheckpointRequest` and `CheckpointStart` (rejected: the very next sentence says "the envelope clause carries the remaining two rows", so the passage is self-consistent and the defect is wording).

FACT: Every anchor in the delta (the two paragraphs added to SPEC-1) verifies exactly. `spec/10_gateway-internals.md:57` states `CoordinatorFence` "is the only way to exit hold state"; `:60` closes "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"; both sit under `#### 10.1.4` at `:53`. `git log -L 60,60:spec/10_gateway-internals.md` returns cf20a0646 "Apply proposal 0076 SPEC-1 to spec/10", so the "which proposal 0076's SPEC-1 landed under its D5" clause correctly attaches to `:60` alone and `:57` predates it (db8cdc224). — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60

FACT: The §4.7.1 paragraph's four anchors are exact, not approximate. `spec/04_system-components.md:725` is the `ReportSessionScrub` row ending "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` is the `ReportPodScrub` row ending "The request is pod-scoped."; `schemas/lenny-adapter.proto:458` is literally `  SessionId session_id = 2;` inside `ReportSessionScrubRequest` (opened at `:456`); `:499-503` is exactly `message ReportPodScrubRequest {` through its closing brace, carrying `pod_id`, `outcome`, `detail`. — EVIDENCE: spec/04_system-components.md:725, :726; schemas/lenny-adapter.proto:456-458, :499-503

FACT: The "only sentence in `spec/` that classifies one request message pod-scoped" claim holds mechanically. `grep -rno "pod-scoped" spec/` returns exactly six sites: `spec/04_system-components.md:151`, `:188`, `:726`, `:872`, `spec/10_gateway-internals.md:60`, `spec/12_storage-architecture.md:202`. SPEC-1 retires the first two; `:872` is the credential rotation ceiling, `spec/10:60` is the hold gauge, and `spec/12:202` is a Redis key prefix. None classifies a request message. — EVIDENCE: spec/04_system-components.md:872; spec/12_storage-architecture.md:202

FACT: The §1.1/D1 proto measurement is reproducible and exact on the tree today. A brace-depth parse of the two service blocks returns 31 RPC request types, 25 declaring a top-level `session_id`, 6 not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). The name/type biconditional holds in both directions across the whole file with zero violations. `CheckpointRequest` (`:1174`) and `CheckpointResponse` (`:1250`) are the only `oneof`-carrying messages, and `rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse)` at `:133` confirms the response is no RPC's request type. — EVIDENCE: schemas/lenny-adapter.proto:133, :1174, :1250

FACT: `CoordinatorFenceRequest` (`:1455`) declares only `SessionId session_id = 1` and `int64 coordination_generation = 2`, with no `reserved` block, so §7's surviving question ("never declared the field the column records") is factually grounded. — EVIDENCE: schemas/lenny-adapter.proto:1455-1461

FACT: `0073's §4.2 value rule`, cited in §3 and §6, resolves to proposal 0073's own `### 4.2 The rule` heading, not to `spec/04` §4.2 (Session Manager) — the citation is coherent as written. The rule itself landed at `spec/05_runtime-registry-and-pool-model.md:515`. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964-970; spec/05_runtime-registry-and-pool-model.md:515

WATCHOUT: `spec/05_runtime-registry-and-pool-model.md:515` is one enormous line. `sed -n 515p | cut -c1-600` shows only the slot-multiplexing prose and looks like the citation is wrong. The clause "a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved" is the ninth sentence on that line. Split on periods or grep the phrase. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515

USEFUL [spec.1.fix-G1.1]: its WATCHOUT that proto anchors had drifted ~7-8 lines saved me from re-filing them. The problem statement's current anchors (`:1173`, `:1455`, `:1456`, `:596`, `:1217`) are all correct on today's tree; the orchestrator's brief still carries the OLD, stale set (`:1166`, `:1447`, `:1448`, `:589`). Trust the proposal here, not the brief.

USEFUL [f1.human-decisions, the OD4 entry]: it pre-derived the §4.7.1 retention argument and the tier-11 gate that pins `:725`'s wording, which is exactly the paragraph the delta added. Verifying it took minutes instead of an hour.


### [spec-recheck.1.review-client-surface.1]

DECISION: Empty findings list again for the client-facing-surface lens, on the r5→r6 delta — BECAUSE the delta adds only two justification paragraphs to SPEC-1 (one on §10.1.4's hold sentences, one on §4.7.1's two per-message scope cells), trims two clauses that pointed at the removed §7 Q1, and deletes §7 Q1 itself; I re-verified every citation in the new text and all resolve, and no externally-consumed contract gains, loses, or renames anything — ALTERNATIVES: the stale proto excerpt at docs/api/internal.md:210-214 (see DEFERRED below, out of this loop's scope); the "session-scoped frame" vocabulary collision between the staged §4.1 envelope paragraph and §28.5.3's JSONL frame classes (pre-existing in the shipped §4.1 table, which already carries a `CheckpointStart` row scoped `session`).

FACT: Every citation the r5→r6 delta introduced holds at HEAD. spec/10_gateway-internals.md:57 says `CoordinatorFence` RPCs are "the only way to exit hold state"; :60 closes "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; both sit under the §10.1.4 heading at :53. `git log -S` shows that :60 sentence landed in commit cf20a0646 "Apply proposal 0076 SPEC-1 to spec/10", so the delta's "which proposal 0076's SPEC-1 landed under its D5" is accurate. — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60

FACT: The delta's §4.7.1 paragraph is accurate in all four of its claims. spec/04_system-components.md:725 carries "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; :726 closes "The request is pod-scoped."; `ReportSessionScrubRequest` declares `SessionId session_id = 2` at schemas/lenny-adapter.proto:458; `ReportPodScrubRequest` declares exactly `string pod_id = 1`, `PodScrubOutcome outcome = 2`, `string detail = 3` at :499-503. The tier-11 gate the paragraph names asserts both carriers share the literal opener "The request is session-scoped: it is " plus the rule sentence. — EVIDENCE: spec/04_system-components.md:725-726; schemas/lenny-adapter.proto:458, :499-503; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33, :43-76

FACT: "Retiring the table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" is TRUE. A tree-wide grep for "pod-scoped" over spec/ returns exactly five hits: spec/04:151, :188 (both inside SPEC-1's replaced block), :726, :872 (the credential rotation in-flight ceiling), spec/10:60 (the hold and its gauge), and spec/12:202 (Redis key prefixes). None of the last three classifies a request message. — EVIDENCE: spec/04_system-components.md:726, :872; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

FACT: A brace-depth parse of the two service blocks at HEAD returns 31 distinct request messages: 1 envelope (`CheckpointRequest`), 5 with no address (`AdapterEventsRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`), and 25 declaring a top-level `SessionId session_id`. The §4.1 table's 32 rows are 26 session (including `CheckpointRequest` and `CheckpointStart`) and 6 pod, so D1's reproduction claim holds once the fence moves. The addressing convention holds in both directions: no field named `session_id` has any other type and no field of type `SessionId` has any other name, so the staged gate PASSES on the tree today. — EVIDENCE: schemas/lenny-adapter.proto:596, :1173-1187, :1217, :1455-1456; spec/04_system-components.md:155-186

DEFERRED [docs/api/internal.md]: `docs/api/internal.md:209-215` publishes a protobuf excerpt reading `message CheckpointRequest { string session_id = 1; string checkpoint_id = 2; string consistency = 3; }`. Every part of it is false about the shipped proto: the real `CheckpointRequest` is a `oneof msg` of `CheckpointStart`/`CheckpointGrant`/`CheckpointAbort` plus `int64 coordination_generation = 4` (schemas/lenny-adapter.proto:1173-1187), it declares no `session_id` at all, and the stream is addressed by `CheckpointStart`'s `SessionId session_id = 7` (:1217). The page also documents an `UploadFiles` RPC that no service declares. I did NOT file it: the excerpt is already wrong today (the shipped §4.1 table already calls `CheckpointRequest` "the stream envelope rather than an addressed request"), so SPEC-1 does not make it wrong, and this loop may not remedy a docs change. What SPEC-1 does change is the severity: the staged rule makes a top-level `string session_id` on a request message a non-conforming spelling the tier-0 gate refuses, so the published excerpt would then illustrate a construction the specification forbids. The docs loop, or proposal 0080's residue inventory, should own it. — EVIDENCE: docs/api/internal.md:210-214, :233-240; schemas/lenny-adapter.proto:1173-1187, :1217

USEFUL [spec.4.review-client-surface.1]: its FACT that the gateway-adapter protocol is pinned by path (`const adapterProtoPath = "schemas/lenny-adapter.proto"`, tests/tier0_static/adapter_proto_parse_test.go:18) while `schemas/lenny-interceptor.proto` and `schemas/lenny-tokenservice.proto` DO declare `string session_id` fields is the single fact that keeps the staged gate's indefinite "a protocol definition" from being a real defect. I re-derived it and reached the same place; do not re-open that WATCHOUT.

USEFUL [spec.1.review-docs-alignment.1]: "docs/ names RPCs, never request message types" held again. `grep -rn "CoordinatorFence" sdks/ docs/ charts/` returns exactly one hit, docs/reference/adapter-contract.md:69, and it carries no scope word. That closed the SDK/CRD/OpenAPI half of this lens in one command.

FACT: The retiring gate's private helpers are fully contained. `parseMessageScopeTable`, `declaredScope`, `messageScopeSpecPath`, `checkpointStartMessage`, `messageScopeRow`, `scopeRow`, and `messageScopeDisagreements` have zero references outside `tests/tier0_static/adapter_proto_message_scope_test.go`, and `protoServiceRequests` has exactly one caller, that same gate. The replacement gate still needs the request-message population, so retiring the table does not strand `protoServiceRequests` as dead code. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:64-68; tests/tier0_static/adapter_proto_message_scope_test.go:87

WATCHOUT: §7 of spec-changes.md lost its question 1 (the "does the replacement gate close the hole the table's gate closed, else withdraw" question) in this delta, and the surviving question was renumbered from 2 to 1. The question itself was not dropped from the proposal: it now lives in the summary as OD1 with a full recommendation, and the retired-field-number question is OD2 there. Anything citing "§7's first question" for the gate-hole residual is stale by one revision; D2 and D4 were both edited in the same pass to stop pointing at it, and no dangling "§7" reference remains anywhere outside the review log. — EVIDENCE: 0075...spec-changes.md:45-46, :56-62, :152-155; 0075...summary.md:82-98, :139-150


### [spec-recheck.1.review-docs-alignment.1]

DECISION: Returned an empty findings list for the docs-alignment lens on the r5→current spec staging — BECAUSE the delta (deletion of §7 Q1 and D2/D4's pointers to it, plus the new SPEC-1 paragraph on §4.7.1 and §10.1.4) changes no behavior a docs/ page describes, and every citation it adds resolves — ALTERNATIVES: filing the "pod-scoped guard named `session_id` is silently misclassified" residual (now stated only in D4's reasoning after §7 Q1 was deleted) as an accepted failure mode missing from landing text; rejected because the staged §4.1 rule is definitional — declaring a top-level `SessionId session_id` IS how a request addresses a session, so the case has no observable outcome the staged text leaves unstated, and the constraint paragraph already records the only thing the gate cannot see.

FACT: Every citation the NEW SPEC-1 paragraph adds is exact, re-verified today: `spec/10_gateway-internals.md:57` is the "only way to exit hold state" sentence and `:60` the Observability bullet ending "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped", both inside §10.1.4 (heading at `:53`); `spec/04_system-components.md:725`/`:726` are the `ReportSessionScrub`/`ReportPodScrub` §4.7.1 rows; `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2` inside `ReportSessionScrubRequest` (message at `:456`); `ReportPodScrubRequest` spans `:499-503`; `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` row; the tier-11 gate function spans exactly `:43-76`. — EVIDENCE: spec/10_gateway-internals.md:57,60; spec/04_system-components.md:725,726; schemas/lenny-adapter.proto:456,458,499-503; docs/reference/adapter-contract.md:81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43,76

FACT: 0076's SPEC-1 did land the `:60` pod-scoped-hold sentence under its D5, so this proposal's attribution of that sentence is correct. — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:319-321 ("The bullet also states that the hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped under D5")

FACT: The staged addressing convention holds on the shipped proto today, so the replacement gate would pass on its first run: no field named `session_id` carries a type other than `SessionId` and no field of type `SessionId` carries another name (two greps over the whole file return nothing), and only two `oneof`s exist, at `CheckpointRequest` and `CheckpointResponse`. `CheckpointRequest` declares no top-level address; `CheckpointStart` declares `SessionId session_id = 7` and `CheckpointGrant`/`CheckpointAbort` declare none. — EVIDENCE: schemas/lenny-adapter.proto:1174, 1250, 1217, 1223, 1243

FACT: `spec/04_system-components.md:726` is confirmed the ONLY sentence left in `spec/` that classifies a request message pod-scoped once SPEC-1 applies. A `grep -rn "pod-scoped" spec/` returns six hits: `:151` and `:188` (both inside SPEC-1's replaced block), `:726`, `spec/04:872` (a rotation ceiling, not a message), `spec/10:60` (the hold and its gauge), and `spec/12:202` (Redis slot-counter keys). SPEC-1's closing claim is accurate. — EVIDENCE: spec/04_system-components.md:872; spec/12_storage-architecture.md:202

USEFUL [earlier rounds' docs sweeps]: the recorded FACTs that docs/ carries no mirror of the §4.1 table and that nothing anchors `#request-message-scope` still hold. Re-verified independently: `grep -rn "Request Message Scope" spec/ docs/ tests/ schemas/ scripts/` returns only `spec/04_system-components.md:149`, and the only docs mention of the fence RPC is a purpose-only row. — EVIDENCE: docs/reference/adapter-contract.md:69


### [spec-recheck.1.review-edit-sites.1]

DECISION: Returned an empty findings list, the third consecutive empty return for this lens — BECAUSE the r5 delta inside `spec-changes.md` is exactly two things (the removal of §7 question 1 plus its two cross-references in D2 and D4, and a new SPEC-1 paragraph answering the old OD3/OD4 about §10.1.4's hold sentences and §4.7.1's two per-message scope statements), every citation in the new paragraph resolves, and an independent re-sweep of every identifier SPEC-1 removes or introduces found no authored surface outside `spec/04_system-components.md` that becomes wrong — ALTERNATIVES: filing the removal of §7 Q1 (barred: "whether a decision should be open at all, and how an open decision is framed, are not yours to file on"); re-filing D1's off-by-one reproduction sentence (already on the found-and-fixed list and declined by [spec.4.review-fresh.1]).

FACT: Every citation in the r5-added SPEC-1 paragraph verifies at HEAD. `spec/04_system-components.md:725` carries "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." and `:726` ends "The request is pod-scoped."; `ReportSessionScrubRequest` declares `SessionId session_id = 2` at `schemas/lenny-adapter.proto:458`; `ReportPodScrubRequest` is `:499-503` with `string pod_id`, `PodScrubOutcome outcome`, `string detail`; the tier-11 gate is `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` at `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` with its pinned literal at `:33` and the doc carrier at `docs/reference/adapter-contract.md:81`; `spec/10_gateway-internals.md:57` is the hold-exit sentence and `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped." 0076's D5 exists at `0076_....spec-changes.md:20`. — EVIDENCE: spec/04_system-components.md:725-726; spec/10_gateway-internals.md:57,60; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,43-76

FACT: The claim "Retiring the table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" is exactly true. `grep -rn "pod-scoped\|pod scoped" spec/ docs/ schemas/ charts/` returns six spec sites: `spec/04:151`, `:175`-region (table), `:188` (all three retired by SPEC-1), `:726` (survives, agrees), `spec/04:872` (rotation ceiling, classifies no message), and `spec/10:60` (the hold and its gauge, not a request message). `spec/12:202` is Redis key prefixes. — EVIDENCE: spec/04_system-components.md:726, :872; spec/10_gateway-internals.md:60

FACT: After SPEC-1, `CheckpointStart` disappears from `spec/` entirely — it occurs only at `spec/04_system-components.md:151`, `:167`, and `:168`, all inside the replaced block. Nothing in `spec/`, `docs/`, `charts/`, or any tier-11 gate requires the specification to name it: `tests/tier11_docs/checkpoint_scoping_key_test.go` reads spec/05 §5.2 only, and `pkg/adapter/checkpoint.go:74` cites §4.1 for "Checkpoint is session-scoped", which the envelope clause preserves. Not an edit site. — EVIDENCE: spec/04_system-components.md:167-168; pkg/adapter/checkpoint.go:74-84

FACT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names `tests/tier0_static/adapter_proto_message_scope_test.go` in a hand-maintained inventory of test FILE PATHS. TEST-1 rewrites that file in place rather than deleting it, so the path survives and the inventory does not move. This is the one tier-0 surface naming the gate file that §9 does not list, and it is correctly absent. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336

FACT: The two tier-0 files 0076 landed beside the retiring gate, `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go`, read neither `spec/04_system-components.md` nor the §4.1 table. The first reads proto doc-comment text, the second reads `pkg/adapter/coordination.go`'s `CheckpointBarrier` doc comment via go/ast. Neither is an edit site and neither belongs in §9. — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:11-25; tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:17-27

USEFUL [spec.4.review-edit-sites.4]: its register sweep (`tests/registers/identifier-senses.yaml`, `pinned-spec-literals.yaml`) and its successor-pointer FACT were both exact when spot-checked, which let me spend this pass on the r5 delta and on the §28.5 contract cards instead of re-deriving them.

USEFUL [spec.4.review-kubernetes.1]: its FACT that the §28.3 `CH-FENCE` register row carries no scope column matched my own read of the §28.5.1 `CH-FENCE` card, which describes the fence per session already ("The pod records the generation against the session the fence names... A fence for one session does not change the generation the pod holds for another") and so needs no edit under the reclassification.

WATCHOUT: The r5 fix stage deleted §7 question 1 from `spec-changes.md` and migrated it to the summary as OD1. Two entries on the orchestrator's own refuted list argue from "§7's first question" as if it still stood in `spec-changes.md`, and the summary's lead at `summary.md:7` still says "the open questions in §7 are open". A future reviewer chasing a §7 Q1 reference will not find it in the staging; it is `summary.md`, `## Open decisions for human to make`, OD1. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:152-155

WATCHOUT: `spec-changes.md` §5 no longer opens with an "IMPLEMENTOR TO FILL THE BLANKS" banner (only `non-spec-changes.md` §5 does), so the "indicative block" defence several refutations lean on is gone from the spec side. This is already recorded at [spec.4.review-mechanism.1]; I confirmed it still holds at r5. A fixer must not reintroduce that banner to soften a SPEC-1 instruction. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:105-109


### [spec-recheck.1.review-feasibility.1]

DECISION: Returned no findings under the actor-action-feasibility lens — BECAUSE every actor the staged spec text names exists and can perform what it is assigned, and I mechanically confirmed the replacement gate is green on today's protocol definition rather than reasoning about it — ALTERNATIVES: filing the "the addressing frame opens the stream" clause as an unfalsifiable constraint (rejected: nothing in a proto oneof encodes stream order, but no classification depends on it, so it is colour rather than a gate obligation); filing D2's "can never fail" against SPEC-1's new §4.7.1 paragraph (rejected: D2 explicitly sources "pod-scoped" from the derivation, not from spec/04:726, so its tautology argument holds under its own stated reading).

FACT: The replacement gate D2 states is GREEN on the tree as it stands today, on all four clauses. I re-derived it: across the whole of `schemas/lenny-adapter.proto`, every field named `session_id` is of type `SessionId` and every field of type `SessionId` is named `session_id` (both directions, oneof arms included); `CheckpointRequest` declares no top-level address (only `int64 coordination_generation = 4` beside the oneof); and exactly one of its three frames declares the address. So the gate does not fail on its first run. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187 (`CheckpointRequest`), :1217 (`CheckpointStart`'s `SessionId session_id = 7`), :1223-1245 (`CheckpointGrant`, `CheckpointAbort`, no address), :596 (`message SessionId`)

FACT: A parse of the two service blocks returns 31 RPC request types: 25 declare a top-level `SessionId session_id`, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). The retired table's 32 rows are those 31 plus `CheckpointStart`, its 6 pod rows are those 6 non-envelope messages plus `CoordinatorFenceRequest`. So D1's reproduction claim holds exactly once `CoordinatorFenceRequest` moves under 0076's OD3. — EVIDENCE: spec/04_system-components.md:155-186; schemas/lenny-adapter.proto (service blocks at :32 and :261)

FACT: Nothing outside `spec/04` §4.1 reads the message-scope table, and no `§4.1` cross-reference in `spec/` points at it — every one resolves to the Edge Gateway Replicas extraction thresholds and HPA metric roles. `tests/tier0_static/spec_map_slot_address_registration_test.go` names the gate file by PATH (`:336`) and never by function, so it needs no edit while the replacement gate keeps that path. — EVIDENCE: spec/04_system-components.md:59, :1512; spec/10_gateway-internals.md:77; tests/tier0_static/spec_map_slot_address_registration_test.go:336, :970-988

FACT: `docs/` restates the §4.1 classification nowhere. The only doc sentence carrying a request message's class is `docs/reference/adapter-contract.md:81`, which mirrors `spec/04_system-components.md:725` and is pinned by the tier-11 gate SPEC-1 cites; SPEC-1 leaves both alone. So §9 owes no docs file. — EVIDENCE: `grep -rn "pod-scoped\|session-scoped" docs/`; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33, :43-76

FACT: Every citation in the two paragraphs this delta ADDED to SPEC-1 resolves as written. `spec/10_gateway-internals.md:57` states the fence is the only way out of hold state and `:60` states the hold and its gauge stay pod-scoped, both under `#### 10.1.4` at `:53`; `git log -L 60,60` returns cf20a0646 ("Apply proposal 0076 SPEC-1 to spec/10") and `-L 57,57` returns db8cdc224, so the "0076's SPEC-1 landed it under D5" attribution is on `:60` alone and is correct. `:725`/`:726`, `schemas/lenny-adapter.proto:458` and `:499-503`, and `docs/reference/adapter-contract.md:81` all read as cited. 0076's D5 is titled "The coordinator-loss hold stays pod-scoped". — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60; proposals/0076_.../0076_....spec-changes.md:20

WATCHOUT: `spec/28_communication-channels.md:64` is the precedent that lets §4 prose name a tier-0 gate as the enforcer of a rule ("the citation resolver and the line-citation ratchet are the gates that hold this rule"). Do not file D1's "A tier-0 gate refuses a protocol definition..." as a spec-naming-a-test defect; the specification already does this. — EVIDENCE: spec/28_communication-channels.md:64

WATCHOUT: `schemas/lenny-interceptor.proto:56` and `schemas/lenny-tokenservice.proto:65`, `:154` each declare `string session_id`. D1's constraint paragraph says "a protocol definition", indefinite, so a reader could take the gate's population as every proto in `schemas/`, under which it fails immediately. D2 pins it ("on every message the protocol declares") and §4.1's opening sentence pins "the protocol" to the gateway-adapter protocol, so the reading is available and I did not file it. An implementor must scope the gate to `schemas/lenny-adapter.proto`, which is what `adapterProtoPath` already is. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:18; schemas/lenny-interceptor.proto:56

FACT: Reclassifying `CoordinatorFenceRequest` session-scoped imposes exactly one behavioural obligation, the `spec/05:515` empty-identifier refusal, and the handler already meets it before it resolves any entry. `CheckpointStart`'s class carries the same obligation and `pkg/adapter/checkpoint.go:82-87` meets it on the opening frame, before the op lock and before any root. So no handler change is owed and §3's "No proto or handler changes" is true. — EVIDENCE: pkg/adapter/coordination.go:107-119; pkg/adapter/checkpoint.go:74-87

DEFERRED [0075_..._.non-spec-changes.md]: TEST-1 says the replacement gate's cases are re-registered "under the same sections", which includes §28.5.3 (`tests/spec-map.json:5670`). The retiring gate reads `spec/04` §4.1 and cited 28.5.3 for addressing; the replacement gate reads `schemas/lenny-adapter.proto` alone and checks a gRPC request-message convention, which is §28.5.1 and §28.5.2 territory — §28.5.3 is the intra-pod JSONL and MCP boundary. Whether the replacement cases can honestly carry a `// spec: 28.5.3` annotation is unsettled, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` credits from the annotation, so the annotation decides the register entry rather than the reverse. The remedy is in the non-spec staging, which this loop may not edit. — EVIDENCE: tests/spec-map.json:5670; tests/tier0_static/adapter_proto_message_scope_test.go:129; spec/28_communication-channels.md:499 (`#### 28.5.3 Intra-pod`)

USEFUL [spec.1.fix-design-G1.1]: its WATCHOUT that `CheckpointRequest`'s three frames do NOT agree on scope saved me from re-deriving the envelope clause's design; I confirmed it at the proto and moved on.

USEFUL [spec.1.fix-G2.1]: its FACT that `slotAddressCaseFiles` names the three tier-0 files by path rather than by function is what let me close the "is a register missing from §9" question in one read rather than by reasoning about the retiring function names.


### [spec-recheck.1.review-fresh.1]

DECISION: Returned an empty findings list on the staged spec edits — BECAUSE every citation in `spec-changes.md` re-verified exactly (see FACTs below), the derivation predicate was re-derived mechanically from the proto and reproduces the retired table row for row, and the two paragraphs the delta added to SPEC-1 are true about the tree — ALTERNATIVES: filing that the staged lead sentence "the classification is derived from the message's field set rather than declared per message" sits in an applied spec that still declares two request messages' classes per message at `spec/04_system-components.md:725` and `:726` (rejected: the two declarations agree in value with the derivation, no reader derives a different class from either carrier, and the fix is a rewording of the rule sentence, which is the class this loop has refuted five times); filing that 0076's OD3 Question A recommendation the reviewer accepted said to "rewrite the declaring sentence, naming the pod-scoped hold exit as the one pod-wide effect that remains under D5" while SPEC-1 retires that sentence and names the hold nowhere in §4.1 (rejected: the delta paragraph argues the departure explicitly and `spec/10_gateway-internals.md:57` and `:60` state both hold facts in §10.1.4, so the applied spec is complete and consistent — it is a preference between workable designs).

FACT: The derivation was re-derived mechanically, not trusted. A brace-depth parse of `schemas/lenny-adapter.proto` over the two service blocks returns 31 RPC request types, 25 with a top-level `SessionId session_id` and 6 without (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`). Across EVERY message the file declares there is no field named `session_id` of another type and no field of type `SessionId` under another name, so the replacement gate passes on the proto as it stands. Only two `oneof` blocks exist, at `:1174` (`CheckpointRequest`: start/grant/abort) and `:1250` (`CheckpointResponse`, the request type of no RPC), and exactly one arm of the first declares the address. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1217, :1250

WATCHOUT: a naive Python brace-counter silently stops parsing at `message ReportPodScrubResponse {}` (`schemas/lenny-adapter.proto:508`) — a message opened and closed on one line — and then reports every later message as MISSING, which reads exactly like the proposal citing messages that do not exist. Handle the same-line close, as `protoFields` already does at `tests/tier0_static/adapter_proto_parse_test.go:44-48`. — EVIDENCE: schemas/lenny-adapter.proto:508; tests/tier0_static/adapter_proto_parse_test.go:44-48

FACT: Every citation the delta added to SPEC-1 verifies. `spec/04_system-components.md:725` states `ReportSessionScrub`'s request is "session-scoped: it is addressed by the identifier of the released session and names no slot"; `:726` ends "The request is pod-scoped."; `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2` and `:499-503` is `ReportPodScrubRequest` with `pod_id`/`outcome`/`detail` only; `docs/reference/adapter-contract.md:81` carries the same sentence as `:725`; and `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` holds both carriers to the literal opener "The request is session-scoped: it is " plus the shared rule. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67-74

FACT: The attribution "which proposal 0076's SPEC-1 landed under its D5" for `spec/10_gateway-internals.md:60` is exact. `git log -L60,60:spec/10_gateway-internals.md` returns commit `cf20a0646` ("Apply proposal 0076 SPEC-1 to spec/10"), whose diff adds the sentence "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped.", and 0076's D5 is titled "The coordinator-loss hold stays pod-scoped." `:57` ("the only way to exit hold state") and `:60` both sit under `#### 10.1.4` at `:53`. — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60

FACT: `:726` really is the only sentence left in `spec/` classifying one request message pod-scoped after SPEC-1. `grep -rn "pod-scoped" spec/` returns six hits; `spec/04:151` and `:188` are inside SPEC-1's replaced block, `spec/10:60` classifies the hold and its gauge, `spec/04:872` classifies a rotation ceiling, and `spec/12:202` classifies Redis slot-counter keys. — EVIDENCE: spec/04_system-components.md:726

FACT: The one behavioral obligation the reclassification pulls in is already met in the tree, so SPEC-1 forces no handler change. `spec/05_runtime-registry-and-pool-model.md:515` requires a session-scoped request with an empty identifier to be refused at the adapter boundary with `InvalidArgument` before any root is resolved, and `CoordinatorFence` does exactly that before it resolves the slot entry. — EVIDENCE: pkg/adapter/coordination.go:108-111, :116

FACT: The blast-radius sweep reproduces at HEAD. Nothing under `spec/`, `docs/`, `schemas/`, or `charts/` links the `#request-message-scope` anchor or restates the retired table; `grep -rn "rather than derived\|classification table\|the table below"` over those four trees returns `spec/04:151` and `:188` alone as §4.1 references; `CoordinatorFenceRequest`'s own proto doc comment (`schemas/lenny-adapter.proto:1449-1456`) and the §28.5.1 `CH-FENCE` card (`spec/28_communication-channels.md:314-317`) are already session-oriented from 0076, so neither is an edit site. — EVIDENCE: schemas/lenny-adapter.proto:1449-1454; spec/28_communication-channels.md:314-317

UNVERIFIED: `tests/spec-map.json:156`, `:169`, and `:5670` (the retiring gate's registrations) were taken from the review log rather than re-read; they belong to the non-spec lane and nobody in this loop needs them, but the lane that owns TEST-1 should re-read them before relying on the line numbers.


### [spec-recheck.1.review-kubernetes.1]

DECISION: Returned an empty findings list — BECAUSE the staged spec edit is a classification rule for gRPC request messages on the gateway-adapter protocol and touches no CRD, status subresource, field manager, finalizer, admission webhook, work queue, or reconcile path; there is no Kubernetes-idiom surface in it to be sound or unsound about — ALTERNATIVES: filing the §4.7.1 residual (spec/04_system-components.md:725, :726 still declare two request messages' scope per message while the staged D1 sentence says the classification is "derived from the message's field set rather than declared per message"), rejected because SPEC-1 now names both sentences, states that both agree with the derivation, and gives the reason they are kept, so the applied spec is consistent and the tension is wording.
FACT: The addressing convention the replacement gate checks HOLDS on the tree today, so the gate is green on its first run. Across the whole adapter proto every field named `session_id` is of type `SessionId` (26 sites) and no field of type `SessionId` carries another name; `grep -nE "string\s+session_id"` returns nothing — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :342-1674 (the 26 `SessionId session_id = N;` declarations)
FACT: The envelope half of the gate is also green today. `CheckpointRequest` (:1173) declares its three frames in `oneof msg` (:1174) with only `int64 coordination_generation = 4` outside it, and of the three frames only `CheckpointStart` declares an address (`SessionId session_id = 7` at :1217); `CheckpointGrant` and `CheckpointAbort` declare none. `CheckpointResponse` (:1250) is the only other `oneof` in the file and is the request type of no RPC — EVIDENCE: schemas/lenny-adapter.proto:1174, :1217, :1250
FACT: Every new citation the delta added checks out. spec/04_system-components.md:725 is the `ReportSessionScrub` row ("The request is session-scoped: it is addressed by the identifier of the released session and names no slot."), :726 the `ReportPodScrub` row ("The request is pod-scoped."); `ReportSessionScrubRequest` declares `SessionId session_id = 2` at schemas/lenny-adapter.proto:458 and `ReportPodScrubRequest` at :499-503 declares `string pod_id`, `PodScrubOutcome outcome`, `string detail` and no address; the tier-11 gate function runs tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76 and holds the wording on docs/reference/adapter-contract.md:81; spec/10_gateway-internals.md:57 is the "only way to exit hold state" bullet and :60 the "hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped" bullet, both inside §10.1.4 (heading :53), landed by 0076's SPEC-1 under its D5 (proposals/0076_.../...spec-changes.md:20, :320)
FACT: "Retiring the table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" is TRUE. `grep -rn "pod-scoped" spec/` outside the retiring table returns only spec/04:726 (a request message), spec/10:60 (a hold and a gauge), spec/04:872 (an in-flight ceiling), and spec/12:202 (Redis keys) — EVIDENCE: spec/04_system-components.md:726
FACT: Retiring the §4.1 table breaks no inbound reference and no docs mirror. `grep -rni "Request Message Scope\|message-scope\|message scope" spec/ docs/ schemas/ charts/` returns exactly one hit, the heading itself at spec/04_system-components.md:149, and no page under docs/ restates the table. `grep -rn "CoordinatorFence" spec/` shows no site other than the retiring :175 and :188 that states its scope class — EVIDENCE: spec/04_system-components.md:149, :175, :188
WATCHOUT: §7's first question (the withdrawal branch on whether the replacement gate closes the hole the table's gate closed) was DELETED from spec-changes.md in this delta, and the old question 2 renumbered to 1. It did not vanish: it is now OD1 in the summary (summary.md:82-137), framed as a human decision. Any orchestrator note or log entry that still says "§7 question 1 asks whether..." is stale, and `grep -n "§7" ` over the proposal returns nothing outside the review log — EVIDENCE: proposals/0075_.../...spec-changes.md:152-155; ...summary.md:82


### [spec-recheck.1.review-mechanism.1]

DECISION: returned no findings on the spec staging — BECAUSE every citation in `spec-changes.md` resolves and every mechanism the staged block describes holds against the tree: the derivation reproduces the retired table exactly (25 request messages carry a top-level `SessionId session_id`, 5 non-envelope ones carry none and are exactly the table's pod rows after the fence moves, the envelope clause carries `CheckpointRequest` and `CheckpointStart`), no surface outside §4.1 becomes wrong, and the newly added §4.7.1 paragraph is accurate in every particular — ALTERNATIVES: filing on the "only way a request addresses a session" clause (see WATCHOUT below, resolved in the text's favour); filing on the gate's blind-spot claim being non-exhaustive (a nested `SessionId session_id` on a sub-message would derive pod-scoped and the gate would not see it, but no such message exists and paragraph 1 already forbids it); filing on §4.7.1's two per-message declarations surviving beside a sentence saying the classification is not declared per message (redundancy, and the proposal reasons about it explicitly).

FACT: the proto measurements the proposal rests on are exact, re-derived today with a brace-depth parse that strips `//` comments first. 31 RPC request types across the two service blocks; 6 declare no top-level `SessionId session_id` (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); `CheckpointRequest` and `CheckpointResponse` are the only messages with a `oneof`; every one of the 26 `SessionId` field declarations is named `session_id` and no field named `session_id` carries another type; `message SessionId` declares `string value = 1` and so trips no name/type clause of its own. Naive brace counting WITHOUT stripping comments gives wrong message bodies, because comments contain `{sessionId}` paths — EVIDENCE: schemas/lenny-adapter.proto:596, :1173-1187, :1217, :1249-1257

FACT: after SPEC-1 applies, `spec/04_system-components.md:726` ("The request is pod-scoped.") really is the only sentence left in `spec/` classifying one request message pod-scoped. The other three `pod-scoped` hits in `spec/` are `spec/10_gateway-internals.md:60` (the hold and its gauge), `spec/04:872` (a rotation ceiling), and `spec/12_storage-architecture.md:202` (Redis key prefixes); none names a request message — EVIDENCE: spec/04_system-components.md:726, spec/10_gateway-internals.md:60, spec/04_system-components.md:872, spec/12_storage-architecture.md:202

FACT: nothing outside §4.1 links to or depends on the retired table. No `#request-message-scope` anchor is referenced anywhere in `spec/` or `docs/`; no `docs/` page mirrors the table; `schemas/lenny-adapter.proto`'s `CoordinatorFenceRequest` doc comment is already per-session after 0076 and carries no guard-versus-address language; the only three files in `tests/` or `scripts/` that mention the table are the three the proposal already lists. The missed-edit-site surface for this staging is empty — EVIDENCE: schemas/lenny-adapter.proto:1449-1461; `grep -rn "#request-message-scope" spec/ docs/` returns nothing

WATCHOUT: paragraph 1 of the staged block ends "...a top-level `session_id` field of type `SessionId`, which is the only way a request on this protocol addresses a session", and paragraph 2 then says the envelope is addressed by a field on its opening frame rather than by a top-level field of its own. That reads as a contradiction on first pass and is NOT one: paragraph 3 describes the residual purely as "a session addressed under a name and a type that are both unconventional", which fixes "the one way" as the name-and-type SPELLING rather than the field's position, and `CheckpointStart` uses that same spelling. Do not file this; I nearly did — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:8-25

FACT: "0073's §4.2 value rule", cited three times in this proposal, is a section of proposal 0073's OWN document, not a `spec/04` §4.2. It landed in the tree at `spec/05_runtime-registry-and-pool-model.md:515`, deep inside one very long line. Do not chase `spec/04` §4.2 (Session Manager) looking for it — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964-970 ("### 4.2 The rule"); spec/05_runtime-registry-and-pool-model.md:515

FACT: the reclassification creates no unimplemented obligation. `CoordinatorFenceRequest` is the only message the derivation moves into the session class, and the handler already refuses an empty identifier with `InvalidArgument` before resolving anything, which is exactly what the spec/05 value rule requires of a session-scoped request — EVIDENCE: pkg/adapter/coordination.go:107-111; pkg/adapter/checkpoint.go:81-84 (the same refusal on the envelope's opening frame)

DEFERRED [proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md]: this round's delta deleted §7 question 1 from `spec-changes.md` (the "retire the table or withdraw" question), leaving §7 with the retired-field-number column alone. The summary still carries that question in full as OD1, with a recommendation and an alternatives analysis, at `summary.md:82-136`. The two files now state different open-decision sets. Whether the decision should be open at all is outside a reviewer's remit here, so this is recorded rather than filed; whoever reconciles the two files owns it — EVIDENCE: spec-changes.md:152-155 versus summary.md:82-96

OPEN: `spec/` gains its first-ever mention of a test tier in the staged constraint paragraph ("A tier-0 gate refuses a protocol definition in which..."). `grep -rn "tier-0" spec/` returns nothing today. `spec/18` does cite TESTING.md phase gates, so the practice is not unprecedented, and I judged it below the bar. A later round or a human may want to decide whether the specification is the right carrier for a statement about a test gate's reach.


### [spec-recheck.1.review-operational.1]

DECISION: Empty findings list for the operational-consistency lens on the spec-recheck r1 staging, the third time this lens has run on 0075 and returned nothing — BECAUSE the r5→now delta adds only prose whose every citation I re-verified true, and it touches no condition, metric, alert, runbook, or operator-facing page — ALTERNATIVES: I built and killed one new candidate this round (the "only way a request addresses a session" clause against `AdapterEventsRequest`), recorded below so nobody rebuilds it.

FACT: The delta since the r5 snapshot is exactly three things: §7's existential question 1 is deleted (it moved into the summary as OD1, which is human-reviewer territory), D2/D4 lose their pointers to it, and SPEC-1 gains two new paragraphs — the coordinator-hold non-restatement paragraph and the §4.7.1 "stand unedited" paragraph. Everything else in the staged spec block is byte-identical to r2. — EVIDENCE: `diff -u scratchpad/cp-snap/0075_.../spec-r5/...spec-changes.md proposals/0075_.../...spec-changes.md`

FACT: Every citation in the two new SPEC-1 paragraphs is true as written, checked line by line today: `spec/10_gateway-internals.md:57` ("accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state") and `:60` ("The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"), both inside §10.1.4 (heading at `:53`); `spec/04_system-components.md:725` and `:726` are the two §4.7.1 per-message scope cells; `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2` on `ReportSessionScrubRequest` and `:499-503` is `ReportPodScrubRequest`'s three fields; the tier-11 gate function runs `:43-76` and pins the sentence on `docs/reference/adapter-contract.md:81`. `git log -- spec/10_gateway-internals.md` carries `cf20a0646 Apply proposal 0076 SPEC-1 to spec/10`, so the D5 attribution for `:60` holds too. — EVIDENCE: spec/10_gateway-internals.md:57,:60; spec/04_system-components.md:725,:726; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43,:76

FACT: "Retiring the table leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" is exactly right. `grep -rn "pod-scoped" spec/` returns five hits: `spec/04:151`, `:188`, `:726`, `spec/04:872` (the rotation in-flight ceiling, not a message), `spec/10:60` (the hold and its gauge, not a message), and `spec/12:202` (Redis keys). Three of the five are the ones SPEC-1 retires or are about non-messages. — EVIDENCE: spec/04_system-components.md:726, :872; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

MISTAKE: nobody's, but worth recording — I re-derived the proto measurement from scratch rather than trusting the orchestrator's anchors, and it confirms 31 RPC request types, 25 declaring a top-level `SessionId session_id`, 6 declaring none (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). The derived classes reproduce the retired table's rows exactly once the fence moves. Do not spend another round re-deriving this; it has now been done independently at least three times with the same answer.

WATCHOUT: The tempting new candidate this round is D1's "a top-level `session_id` field of type `SessionId`, which is the only way a request on this protocol addresses a session", against `AdapterEventsRequest`, which carries an opaque `bytes envelope_json = 1` and whose channel demonstrably moves `AdapterTerminating(session_id, reason)`. It is NOT a finding: `AdapterTerminating` and every other CH-ADAPTEREVENTS message travels adapter-to-gateway on `AdapterEventsResponse`, which is a response rather than a request, so no request message addresses a session through that envelope. Killed on that ground; do not rebuild it. — EVIDENCE: schemas/lenny-adapter.proto:1769-1775; spec/28_communication-channels.md:437-441 (Messages bullet, all adapter-to-gateway); pkg/adapter/adapterevents.go:135 (`stream.Send(&adapterv1.AdapterEventsResponse{EnvelopeJson: payload})`)

FACT: "0073's §4.2 value rule", which 0075 §3 and §6 name as untouched, is 0073's OWN document subsection `### 4.2 The rule` (proposals/0073_...md:964), not spec §4.2. Its landed home in `spec/` is `spec/05_runtime-registry-and-pool-model.md:515`. Two rounds of this lens have had to re-derive that; it is not a false citation either way. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:711, :964-970; spec/05_runtime-registry-and-pool-model.md:515

USEFUL [spec.4.review-operational.1]: its enumeration of the operational surfaces (the two §28.5.3 frame metrics, the pool-labeled fence metrics, `CoordinatorHandoffSlow`, `docs/runbooks/coordinator-handoff-slow.md`, the §28.4 claim-register domain question) is complete and still holds against today's tree. I re-checked the two frame metrics and the §28 register rows and found no drift, so a future operational pass can start from that entry rather than re-enumerating.

USEFUL [spec.1.review-operational.1]: its FACT that nothing anchors into the `#### Request Message Scope` heading is still true and is what makes the table retirement free of inbound-link work.


### [spec-recheck.1.review-performance.1]

DECISION: Returned an empty findings list under the performance / scalability / failure-mode lens — BECAUSE this staging creates no control-plane or data-plane write at all: it retires a specification table, states a derivation rule, and moves one tier-0 static gate and one tier-3 suite. No CRD status write, no Postgres or Redis key, no new watch or informer, no reconcile trigger, and no change to any RPC's payload, frequency, or fan-out, so there is no write-rate arithmetic against the §12 tier sizing or the etcd dedup ceiling to run — ALTERNATIVES: filing the session-addressed-fence-versus-pod-scoped-hold trade as a handoff reliability regression (rejected: it is 0076's landed code, adjudicated in 0076's OD3, and D3 puts it outside this review).

FACT: The staged reclassification introduces no spec/code divergence on the fence's refusal path. `CoordinatorFence` already rejects an empty identifier with `InvalidArgument` before resolving anything, which is exactly what the §4.2 value rule as stated in spec/05 requires of a session-scoped request. So SPEC-1 does not create a new refusal the handler must learn — EVIDENCE: pkg/adapter/coordination.go:107 (`func (s *Server) CoordinatorFence`), :109-111 (empty-id `InvalidArgument`), :116 (`s.boundSlotState(sessionID)`); spec/05_runtime-registry-and-pool-model.md:515 ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved").

FACT: All four citations in the delta's two new SPEC-1 paragraphs resolve exactly as written. spec/10_gateway-internals.md:57 is the hold-state-semantics bullet ("accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state"); :60 is the observability bullet ending "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"; both sit under `#### 10.1.4` at :53. spec/04_system-components.md:725 and :726 are the two §4.7.1 rows, and their proto grounds hold — EVIDENCE: schemas/lenny-adapter.proto:456-458 (`ReportSessionScrubRequest` with `SessionId session_id = 2`), :499-503 (`ReportPodScrubRequest` with `pod_id`/`outcome`/`detail` and no address).

FACT: SPEC-1's claim that retiring the table leaves `spec/04_system-components.md:726` as the only sentence in `spec/` classifying one request message pod-scoped holds. A grep of `spec/` for both class words returns, outside §4.1's table, only :725/:726 (request messages), spec/10:60 (the hold and its gauge), spec/04:872 (the rotation in-flight ceiling), and spec/12:202 (Redis key scoping) — none of the last three is a request message — EVIDENCE: spec/04_system-components.md:726, :872; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202.

FACT: The §4.7 gateway-to-adapter RPC table row for `CoordinatorFence` states a precondition and a deadline and no scope class, so retiring the §4.1 table leaves it untouched and it is correctly absent from the edit lists — EVIDENCE: spec/04_system-components.md:712.

FACT: The proto premises the rule rests on are exact on the tree today. `oneof` appears at exactly two sites, `CheckpointRequest` (:1174) and `CheckpointResponse` (:1250), and a whole-file scan returns zero fields named `session_id` of a type other than `SessionId` and zero fields of type `SessionId` under another name, so the two halves of the address spelling are extensionally identical and the envelope clause selects one message — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1174, :1250.

WATCHOUT: The delta deleted §7's old question 1 (the withdrawal branch) outright. Its substance did not vanish: the residual it named is now carried in the staged spec text itself ("What the gate cannot see is a session addressed under a name and a type that are both unconventional") and restated in D2's closing sentence. A later reviewer who remembers the orchestrator note calling that question "a real possible outcome of this review" should check those two places before reading the deletion as an unanswered question — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md §2 D1 (constraint paragraph), D2 (closing sentence).

USEFUL [spec.1.fix-design-G1.1]: its WATCHOUT that `CheckpointRequest`'s three frames do NOT agree on scope, and its FACT that `spec/05:515` is one very long line so a `sed -n 515p` looks like a wrong citation, both saved me a re-derivation. Grep for the sentence rather than reading the line.


### [spec-recheck.1.review-reliability.1]

DECISION: Empty findings list under the reliability/fault-tolerance lens — BECAUSE the delta since spec-r5 adds only two SPEC-1 paragraphs (the coordinator-hold non-restatement and the §4.7.1 stand-unedited paragraph) plus the removal of §7 Q1, and every recovery-mechanism claim in them verified true against the tree; the staged §4.1 block adds or changes no retry, lease, dedup, drain, fence-acceptance, or store-failover mechanism. ALTERNATIVES: filing the "session-scoped fence exits a pod-scoped hold" divergence — rejected because 0076's SPEC-1/D5 landed that split deliberately (spec/10_gateway-internals.md:57, :60) and spec/04_system-components.md:190's ShutdownRequest paragraph, which SPEC-1 explicitly leaves standing, is the general precedent for a session-scoped request with a whole-pod handler effect.

FACT: every citation in the NEW SPEC-1 paragraphs resolves. `CoordinatorFence` "is the only way to exit hold state" is spec/10_gateway-internals.md:57; "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped" is :60; both sit under `#### 10.1.4 Coordinator-Loss Detection and Hold State` at :53. 0076's SPEC-1 (proposals/0076_.../0076_....spec-changes.md:159-322, D5 at :20) is indeed what landed the :60 sentence. EVIDENCE: spec/10_gateway-internals.md:53,57,60

FACT: §4.7.1's two per-message scope sentences verify exactly as SPEC-1 describes: spec/04_system-components.md:725 (`ReportSessionScrub` session-scoped, "addressed by the identifier of the released session and names no slot") and :726 (`ReportPodScrub` "The request is pod-scoped."). The tier-11 gate holding :725's wording on both carriers is TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc at tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76, mirrored at docs/reference/adapter-contract.md:81. EVIDENCE: spec/04_system-components.md:725-726

FACT: SPEC-1's claim that retiring the table leaves :726 as the only sentence in `spec/` classifying one request message pod-scoped is TRUE. `grep -rn "pod-scoped" spec/` returns exactly six sites; after the retirement of :151 and :188 the survivors are 10:60 (the hold and its gauge), 04:726 (the request message), 04:872 (the rotation in-flight ceiling), and 12:202 (Redis key prefixes). Only :726 classifies a request message. EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

FACT: the addressing convention the replacement gate checks holds on the current proto in BOTH directions, so the gate is green on application. `grep -nE "^[[:space:]]+(repeated )?SessionId [A-Za-z_]+ = [0-9]+;" schemas/lenny-adapter.proto | grep -v " session_id = "` returns nothing, and the mirror grep for `session_id` fields of another type returns nothing. Re-derived the scope measurement with a brace-depth parser: 31 RPC request messages, 25 declare `session_id`, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1456, :1217

FACT: the reclassification creates NO new fail-closed obligation that needs a new test. `CoordinatorFence` already refuses an empty session identifier with `InvalidArgument` before resolving the entry, so the §4.2 value rule (spec/05_runtime-registry-and-pool-model.md:515) is already discharged for the fence in the tree. EVIDENCE: pkg/adapter/coordination.go:107-119 (`if sessionID == "" { return ... InvalidArgument ... }` at :109-111, `boundSlotState` at :116)

FACT: the two tier-0 gates 0076 added (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) do NOT read the §4.1 table, so retiring it breaks neither and neither is a missing §9 entry. `parseMessageScopeTable`/`declaredScope` have exactly one caller file. EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:51-111

USEFUL [spec.1.review-reliability.1]: its WATCHOUT that any replacement rule must still reach `CheckpointStart` (a `oneof` member, not an RPC request type) is now discharged by the staged envelope clause, which says the addressing frame "is session-scoped" in its own right. Saved me re-deriving it. The same entry's CH-FENCE fact (spec/28_communication-channels.md:314-318 already states the per-session reading) still holds and is why §28 is not a missed edit site.


### [spec-recheck.1.review-security.1]

DECISION: returned an empty findings list for the security lens on the staged spec edits — BECAUSE every citation in `0075_...spec-changes.md` re-verified true against the tree, and the staged rule neither removes an established control nor sources a security bound from an attacker-influenceable self-report — ALTERNATIVES: rejected filing the "derived classification loses the table gate's forced-human-classification step" as a control regression, because the staged spec text itself states the residual ("What the gate cannot see is a session addressed under a name and a type that are both unconventional. A request message that does that is non-conforming") so nothing is removed silently, and because the framing of that residual lived in §7 Q1, which the round's fixer deleted and which the rubric bars a reviewer from filing on.

FACT: the fail-closed refusal the reclassification of `CoordinatorFenceRequest` pulls in is ALREADY implemented, so the proposal's "No proto or handler changes" claim holds. `CoordinatorFence` refuses an empty session id with `InvalidArgument` before resolving the entry — EVIDENCE: pkg/adapter/coordination.go:108-111 (`sessionID := req.GetSessionId().GetValue(); if sessionID == "" { return nil, status.Error(codes.InvalidArgument, ...) }`), and the same refusal on the checkpoint opening frame at pkg/adapter/checkpoint.go:81-87.

FACT: the "value rule" the proposal calls "0073's §4.2 value rule" (spec-changes.md:80, :150) is a reference to proposal 0073's OWN §4.2 ("### 4.2 The rule", proposals/0073_...md:964), not to a spec §4.2 — spec/04 §4.2 is "Session Manager". Only half of that rule ever landed in `spec/`: the session-scoped empty-identifier refusal is at spec/05_runtime-registry-and-pool-model.md:515; the second half ("A pod-scoped request never resolves a session root regardless of what `session_id` carries", proposals/0073_...md:969-970) is in NO spec file — `grep -rn "session root" spec/` returns nothing. That is pre-existing and outside 0075's scope, but do not assume it exists when reasoning about the pod-scoped half of the derivation.

FACT: after SPEC-1 retires the §4.1 table, spec/04_system-components.md:726 (`ReportPodScrub` … "The request is pod-scoped.") is genuinely the only surviving sentence in `spec/` that classifies one request message pod-scoped. Verified by `grep -rn "pod-scoped" spec/` (the other hits are spec/10:60 hold gauge, spec/04:872 rotation ceiling, spec/12:202 Redis keys — none classify a request message) and by `grep -rn "address the pod\|addresses the pod\|scoped to the pod" spec/`, which returns only the retiring :188 — EVIDENCE: spec/04_system-components.md:726.

FACT: no anchor or cross-reference anywhere in `spec/`, `docs/`, `tests/`, or `scripts/` points at the §4.1 "Request Message Scope" heading (`grep -rn "request-message-scope"` is empty), and no `docs/` page classifies a request message by scope (`grep -rn "pod-scoped" docs/` hits only docs/runbooks/dual-store-unavailable.md:68, unrelated). So table retirement dangles no inbound link — EVIDENCE: spec/04_system-components.md:149.

FACT: the proto's addressing convention holds exactly as the staged gate assumes. Every `session_id` field in schemas/lenny-adapter.proto is of type `SessionId`, and no field of type `SessionId` carries another name (`grep -nE "^\s+(repeated )?SessionId [a-z_]+ = " | grep -v session_id` is empty). Only two messages declare a `oneof`, `CheckpointRequest` (:1174) and `CheckpointResponse` (:1250), and the latter is the request type of no RPC — EVIDENCE: schemas/lenny-adapter.proto:596 (`message SessionId`), :1174, :1217, :1250.

WATCHOUT: the newly added §5 paragraph about §4.7.1 is fully accurate and expensive to re-verify; do not re-derive it. spec/04:725 carries the `ReportSessionScrub` row ending "The request is session-scoped: it is addressed by the identifier of the released session and names no slot.", :726 carries "The request is pod-scoped.", `ReportSessionScrubRequest` declares `SessionId session_id = 2` and `ReportPodScrubRequest` declares only `string pod_id`/`PodScrubOutcome outcome`/`string detail`, and the tier-11 gate does hold that exact wording on both carriers — EVIDENCE: spec/04_system-components.md:725-726; schemas/lenny-adapter.proto:456-467, :499-503; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; docs/reference/adapter-contract.md:81.

WATCHOUT: `spec/10_gateway-internals.md:57` ("`CoordinatorFence` … the only way to exit hold state") is April-2026 text, NOT 0076's. Only `:60` ("The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped.") was landed by 0076 (commit cf20a0646c, 2026-09-06). The staged sentence at spec-changes.md:117-119 attaches "which proposal 0076's SPEC-1 landed under its D5" to the `:60` clause, which is correct; a reader who distributes it over both citations will file a false finding — EVIDENCE: `git blame -L 57,57 -L 60,60 spec/10_gateway-internals.md` gives db8cdc224f (2026-04-16) and cf20a0646c (2026-09-06).

OPEN: the pod-wide hold (spec/10:57, :60) is exited by a fence that this proposal makes session-scoped. 0076's D5 deliberately kept the hold pod-scoped and 0075 §5 explicitly declines to restate it in §4.1, so no contradiction is created here, but no spec sentence states which session's fence releases a pod-wide hold on a multi-slot pod. That is 0076/0080 territory; a future round should confirm somebody owns it rather than assume 0075 does.


### [spec-recheck.2.review-applicability.1]

DECISION: returned an empty findings list for the spec staging — BECAUSE every anchor, citation, and staged edit in `spec-changes.md` resolves against the tree as written, the single spec step is deterministic from the line ranges it names, and the applied §4.1 leaves no other spec sentence contradicted — ALTERNATIVES: filing the "§4.7.1 still declares scope per message while the staged rule says classification is not declared per message" tension (rejected: the new §5 paragraph states both statements agree with the derivation, and both do, so it is wording rather than contradiction); filing "the replacement gate's file path is never stated, so `slotAddressCaseFiles` may become an unlisted edit site" (rejected: §9 lists the current path as touched, which reads as keeping it, and the remedy is a test-lane decision).

FACT: the derivation reproduces the retired table exactly. A fresh parse of `schemas/lenny-adapter.proto` returns 31 RPC request types, all 31 present in the §4.1 table, the table's 32nd row being `CheckpointStart` (a `oneof` frame, not a request type). The 6 request messages with no `SessionId session_id` are exactly `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; the table's 6 pod rows are those five plus `CoordinatorFenceRequest`. So after 0076's OD3 the pod set and the no-address set coincide — EVIDENCE: spec/04_system-components.md:153-186; schemas/lenny-adapter.proto:1173, :1456.

FACT: the replacement gate's two-direction convention holds on the proto today, so the gate goes green on its first run and needs synthetic negatives. All 26 `SessionId` field declarations are spelled `SessionId session_id`, and a grep for any `session_id` field whose type is not `SessionId` returns nothing — EVIDENCE: `grep -n "session_id" schemas/lenny-adapter.proto | grep "=" | grep -v "SessionId session_id"` returns empty.

FACT: `CheckpointRequest` and `CheckpointResponse` are the only two messages in the whole proto declaring a `oneof`, and only the first is an RPC request type, so the staged envelope clause selects exactly one message — EVIDENCE: schemas/lenny-adapter.proto:1174 (CheckpointRequest), :1250 (CheckpointResponse).

FACT: the §4.1 block has no inbound blast radius beyond §9's list. `protoFields` and `protoServiceRequests` have exactly the callers §9 names; `TestAdapterProtoRequestMessagesAreClassifiedByScope` and `TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` appear only in `tests/spec-map.json` and their own file; `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names the gate by PATH only. The tier-11 successor-pointer and relocated-material checks are named inventories rather than derived predicates, and §4.1 is in neither, so retiring the table raises no N8 pointer obligation — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336; tests/tier11_docs/successor_pointer_test.go:35-38.

FACT: after SPEC-1 applies, `spec/04_system-components.md:726` ("The request is pod-scoped." on the `ReportPodScrub` row) is the only sentence left in `spec/` classifying one request message pod-scoped, and §4.1's own `ShutdownRequest` paragraph at `:190` is the only per-message session-scoped statement left inside §4.1. Both agree with the derivation. The proposal's new §5 paragraph now states the first of these; the second is unstated and harmless — EVIDENCE: spec/04_system-components.md:190, :725, :726.

WATCHOUT: "0073's §4.1" and "0073's §4.2" in this proposal mean sections of proposal 0073's OWN detailed design, not spec sections. 0073 §4.1 is "Every request message's scope is declared" and §4.2 is "The rule" (the value rule that lands at `spec/05_runtime-registry-and-pool-model.md:515`). Reading them as `spec/04` §4.1/§4.2 sends you to "Session Manager" and wastes a pass — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:897, :964.

WATCHOUT: 0073's SPEC-7 stages more than the §4.1 block (also §4.7 RPC rows, `spec/11:263`, and two `docs/reference/adapter-contract.md` rows), so SPEC-1's phrase "the whole block 0073's SPEC-7 stages" is not a complete description of SPEC-7. It is harmless because SPEC-1 immediately names the block by line range (`:151`, `:153-186`, `:188`), which is what makes the edit deterministic. Do not read it as licence to touch the §4.7 rows — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:2823.

USEFUL [spec.1.fix-G1.1]: the FACT that `spec/05_runtime-registry-and-pool-model.md:515` is one very long line saved a false-citation report; the sentence is real, `grep -o` on the line finds it.

USEFUL [spec.1.fix-G2.1]: the FACT that `slotAddressCaseFiles` names the three tier-0 files by path saved re-deriving the whole blast radius of the gate replacement.


### [spec-recheck.2.review-citations.1]

DECISION: Returned an empty findings list for the citation lens on the r6 spec staging — BECAUSE every concrete citation in spec-changes.md now resolves and says what the proposal claims, including all four newly added sites in the delta (spec/10_gateway-internals.md:57, :60; spec/04_system-components.md:725, :726; schemas/lenny-adapter.proto:458 and :499-503; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; docs/reference/adapter-contract.md:81) — ALTERNATIVES: considered filing the tension between the staged rule's phrase "derived from the message's field set rather than declared per message" and the two surviving per-message declarations at spec/04:725 and :726, and rejected it because SPEC-1 confronts it explicitly, both declarations agree with the derivation, and no applied sentence becomes false.

FACT: The proto anchors that an earlier round found stale by exactly 7 lines have been corrected in the current text and are now right. Verified today: `message CheckpointRequest {` schemas/lenny-adapter.proto:1173, `message CheckpointStart {` :1193 with `SessionId session_id = 7;` :1217, `message CoordinatorFenceRequest {` :1455 with `SessionId session_id = 1;` :1456, `message SessionId {` :596, `ReportSessionScrubRequest.session_id` :458, `ReportPodScrubRequest` :499-503. Do not re-file the stale-anchor finding; it is closed. — EVIDENCE: schemas/lenny-adapter.proto:1173,1217,1455,1456,596

FACT: An independent re-parse of the two service blocks confirms the proposal's measurement exactly. 31 RPC request types; 25 declare a top-level `SessionId session_id`; the 6 that do not are CheckpointRequest, DemoteSDKRequest, NegotiateVersionRequest, GetObservedIntegrationLevelRequest, AdapterEventsRequest, ReportPodScrubRequest. No field named `session_id` in the whole file carries a type other than `SessionId`, and no field of type `SessionId` carries a name other than `session_id`, so the replacement gate's first two clauses pass on the tree as it stands. Only `CheckpointRequest` and `CheckpointResponse` declare a `oneof` (schemas/lenny-adapter.proto:1174, :1250), and `CheckpointResponse` is the request type of no RPC, so "a request message that carries its frames in a `oneof`" selects `CheckpointRequest` alone. — EVIDENCE: schemas/lenny-adapter.proto:1174,1250

FACT: After SPEC-1 applies, `spec/04_system-components.md:726` ("The request is pod-scoped." on the `ReportPodScrub` row) really is the only sentence left in spec/ classifying a request message pod-scoped. A grep of spec/ for "pod-scoped" returns only three other sites and none classifies a request message: spec/10_gateway-internals.md:60 (the hold and its gauge), spec/04_system-components.md:872 (the rotation in-flight ceiling), spec/12_storage-architecture.md:202 (Redis slot-counter keys). The staging's new claim to that effect is correct. — EVIDENCE: spec/04_system-components.md:726

FACT: No reader-facing or schema surface mirrors the §4.1 message-scope table. Greps of docs/, schemas/, and charts/ for "message-scope", "Request Message Scope", and the declared-rather-than-derived ground return nothing outside spec/04:151 and :188 and the two test files already in §9. There is no missing edit site of that kind. — EVIDENCE: spec/04_system-components.md:151

FACT: `protoFields` in tests/tier0_static/adapter_proto_parse_test.go has exactly one external caller, tests/tier0_static/claim_register_proto_agreement_test.go:64, so §9's "the parse's other caller" is accurate. The two tier-0 siblings 0076 landed (adapter_proto_generation_scope_test.go, adapter_barrier_doc_comment_scope_test.go) read the proto through `readAdapterProto` defined in adapter_proto_intrapod_pointer_test.go:220 instead, so they do not move with a change to the shared parse's signature and their absence from §9 is correct. — EVIDENCE: tests/tier0_static/adapter_proto_intrapod_pointer_test.go:220

FACT: The existential question the orchestrator flagged was not deleted in the r6 delta. It moved from spec-changes.md §7 to the summary's `## Open decisions for human to make` as OD1, and the surviving §7 item is the TEST-2 retired-field-number-column question (renumbered from 2 to 1). Any future reviewer told the question is "live" should look in the summary, not in §7. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:82

WATCHOUT: "0073's §4.2 value rule" in §3 and §6 means proposal 0073's own detailed-design subsection ("### 4.2 The rule", proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964), not spec §4.2 (Session Manager). The rule itself landed in spec/05_runtime-registry-and-pool-model.md:515, which is what §4 cites. This is not a wrong citation; do not file it. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:964

WATCHOUT: D1's sentence "the ones that carry none are exactly its pod rows" is literally off by one element (`CheckpointRequest` carries no address but is a session row), because the "other than the stream envelope" qualifier reads as attaching to the first clause alone. The very next sentence resolves it ("The envelope clause carries the remaining two rows"). Judged prose compression rather than a defect; a future reviewer who spots it should weigh the following sentence before filing. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:27

OPEN: 0076's OD3 Question A recommendation, which the answer table records as the accepted answer, said to reclassify the row "and rewrite the declaring sentence, naming the pod-scoped hold exit as the one pod-wide effect that remains under D5". SPEC-1's new paragraph deliberately declines to name the hold exit in §4.1, arguing §10.1.4 owns it. That is a reasoned divergence from the recommendation's second half rather than from the answer, and it is a question for the human reviewer rather than a citation defect. — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.summary.md:185

DEFERRED [spec/04_system-components.md]: The `CoordinatorFence` row at spec/04_system-components.md:712 still reads "Announce new `coordination_generation` to the pod on coordinator handoff", which 0076 falsified by moving the recorded generation onto the session's slot entry (pkg/adapter/slot.go:59) and which spec/28_communication-channels.md:314-317 already contradicts ("The pod records the generation against the session the fence names"). What is true instead: the fence announces the generation for the session it names. This is outside 0075's staged scope (SPEC-1 touches §4.1 only, and its §4.7.1 paragraph correctly limits itself to the two per-message scope statements at :725 and :726), and it is a 0076 residue. It belongs in proposal 0080's residue inventory if no draft owns it.


### [spec-recheck.2.review-client-surface.1]

FACT: the reclassification of `CoordinatorFenceRequest` to session-scoped imposes NO handler change, because the handler already refuses an empty identifier the way 0073's §4.2 value rule requires of a session-scoped request. So §3's "No proto or handler changes" and "0073's §4.2 value rule ... is untouched" both hold, and a reviewer chasing a new fail-closed obligation on the wire finds it already in the tree. — EVIDENCE: pkg/adapter/coordination.go:107 (`func (s *Server) CoordinatorFence`), :110 (`status.Error(codes.InvalidArgument, "CoordinatorFence requires a session id")`); spec/05_runtime-registry-and-pool-model.md:515

FACT: no client-facing surface mirrors the §4.1 request-message-scope table, so SPEC-1 strands no parallel representation. `docs/reference/adapter-contract.md`'s gateway-to-adapter and adapter-to-gateway RPC tables (`:51-101`) carry no scope column; the only scope words on that page are `:81` (`ReportSessionScrub` ... "The request is session-scoped") and `:158`. `grep -rn "CoordinatorFence" sdks/ charts/` returns nothing, and `docs/` returns only `adapter-contract.md:69`, which states no scope. No CRD, OpenAPI, MCP tool schema, JSONL schema, or SDK type file carries a per-request-message scope. — EVIDENCE: docs/reference/adapter-contract.md:51-101, :69, :81

FACT: the addressing convention the new rule rests on holds in BOTH directions across the whole of `schemas/lenny-adapter.proto` and nowhere else in `schemas/`. Every one of the 28 `SessionId`-typed field declarations is spelled `session_id`, and every non-comment occurrence of `session_id` in that file is `SessionId`-typed. But `schemas/lenny-tokenservice.proto:65`, `:154` and `schemas/lenny-interceptor.proto:56` each declare `string session_id`, so a replacement gate scoped to `schemas/*.proto` rather than to the gateway-adapter protocol alone goes red on its first run. The shared parse pins it: `adapterProtoPath = "schemas/lenny-adapter.proto"`. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:18; schemas/lenny-tokenservice.proto:65; schemas/lenny-interceptor.proto:56

FACT: an independent re-parse of the two service blocks confirms D1's reproduction claim exactly. 31 RPC request types; 25 declare a top-level `SessionId session_id`; the 6 that do not are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. A set-difference of the §4.1 table rows against those request types returns `CheckpointStart` on the table side and nothing on the proto side, so the table names exactly the request types plus the one `oneof` member. — EVIDENCE: spec/04_system-components.md:155-186; schemas/lenny-adapter.proto:1174, :1250 (the only two `oneof` blocks in the file)

FACT: every citation the r5→r6 delta added resolves. `spec/10_gateway-internals.md:53` is `#### 10.1.4 Coordinator-Loss Detection and Hold State`, `:57` states `CoordinatorFence` is "the only way to exit hold state", `:60` states the hold and its gauge "remain pod-scoped" and was landed by commit `cf20a0646` ("Apply proposal 0076 SPEC-1 to spec/10"). `spec/04_system-components.md:725` and `:726` are the two §4.7.1 scope sentences, `schemas/lenny-adapter.proto:458` is `ReportSessionScrubRequest`'s `SessionId session_id = 2`, `:499-503` is `ReportPodScrubRequest`, and the tier-11 gate function begins at `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43`. — EVIDENCE: spec/10_gateway-internals.md:53,57,60; spec/04_system-components.md:725,726

DECISION: returned no finding — BECAUSE nothing this proposal stages reaches an externally-consumed contract. The wire proto, the JSONL schema, the CRDs, the OpenAPI document, the MCP/A2A vocabularies, and every SDK are untouched by SPEC-1, and the one reader-facing mirror of the surface (`docs/reference/adapter-contract.md`) carries no scope column to drift out of step — ALTERNATIVES: filing that §4.1's new "derived ... rather than declared per message" sits beside §4.7.1's two per-message declarations at `:725`/`:726`; rejected because the two declarations AGREE with the derivation (verified against the proto), the proposal names both and states why they stand, and a method-statement redundancy that misleads no reader about either message's class is wording rather than an inconsistency.


### [spec-recheck.2.review-docs-alignment.1]

FACT: No page under `docs/` mirrors §4.1's Request Message Scope table, its class words, or its "declared rather than derived" ground. Greps over `docs/` for `pod-scoped`, `session-scoped`, `Request Message Scope`, `message scope`, `declared rather than derived`, and `CoordinatorFence` return only JSONL-frame addressing prose and one gRPC row with no scope word. SPEC-1 therefore has no docs blast radius, and the proposal's §1.4 claim that no reader-facing page changes is correct — EVIDENCE: docs/reference/adapter-contract.md:69 (`CoordinatorFence` row, no scope word), :81, :82, :156, :158, :336
FACT: `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` carries `// spec: 4.1 (request message scope)` on five cases, which makes it look like a §4.1 reader. It is not: it parses the six reader-facing runtime-sample pages and never opens `spec/04_system-components.md`. Retiring the §4.1 table does not move it. The only test in the tree that reads the §4.1 section heading is the tier-0 gate TEST-1 replaces — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26, :46-53; tests/tier0_static/adapter_proto_message_scope_test.go:31
FACT: The tier-11 gate SPEC-1 cites for the `ReportSessionScrub` wording is real and does hold the identical sentence on both carriers, spec §4.7 and `docs/reference/adapter-contract.md`, including a shared opener check. Editing spec/04:725 would break a docs gate — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; spec/04_system-components.md:725; docs/reference/adapter-contract.md:81
FACT: `docs/reference/adapter-contract.md:82`'s `ReportPodScrub` row does NOT carry the "The request is pod-scoped." sentence its spec counterpart at `spec/04_system-components.md:726` carries, and no gate pins it. This asymmetry predates 0075 and is untouched by it, so it is not a finding on this proposal — but a later proposal that makes :726 load-bearing (it becomes the only pod-scope declaration in `spec/` once the table retires) should mirror it — EVIDENCE: spec/04_system-components.md:726; docs/reference/adapter-contract.md:82
FACT: The delta's two new citations both hold exactly. `spec/10_gateway-internals.md:57` states `CoordinatorFence` is the only way out of hold state, `:60` states the hold and its gauge remain pod-scoped, both inside §10.1.4 (heading at `:53`, next at `:64`), and `git log -L 60,60:spec/10_gateway-internals.md` attributes `:60` to `cf20a0646 Apply proposal 0076 SPEC-1 to spec/10`, matching 0076's D5 — EVIDENCE: spec/10_gateway-internals.md:53, :57, :60, :64; proposals/0076_.../0076_....spec-changes.md:20-32, :319-320
FACT: The addressing convention the staged rule rests on holds across the whole proto today. A scan for a field named `session_id` whose type is not `SessionId`, and for a field of type `SessionId` under any other name (including `repeated` and `map<>` forms), returns nothing. `CheckpointRequest` declares `coordination_generation` at top level and no address, so the staged clause "an envelope declares no address of its own" is true as written — EVIDENCE: schemas/lenny-adapter.proto:595-597, :1173-1187, :1217
WATCHOUT: The one docs-lens tension worth knowing about, which I judged below the bar rather than filing: the staged first sentence says the classification "is derived from the message's field set rather than declared per message", while `spec/04_system-components.md:725` and `:726` still declare two request messages' classes per message. SPEC-1's new paragraph names both and says they agree with the derivation, and an earlier round refuted a related finding partly ON `:726`'s survival, so re-raising it as a contradiction is a close variant of refuted ground. Do not file it without a stronger argument than "declared" versus "derived" — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:8-9, :128-139; spec/04_system-components.md:725-726
DECISION: Returned an empty findings list — BECAUSE the change is classification-only with no runtime behavior change (the empty-session-id refusal the reclassification carries is already met at `pkg/adapter/coordination.go:109-111`), it has no docs mirror, it adds no metric, alert, error code, flag, or operator-facing failure cause, and both limits it accepts are stated in the landing spec text (the gate's blind spot in D1's third paragraph) or move with the gate file (the handler-conformance limit) — ALTERNATIVES: filing the "derived rather than declared" tension above (rejected, see WATCHOUT); filing the deferred equal-generation re-fence refusal recorded in the summary's "Defects in the shipped tree" section as an undocumented accepted failure mode (rejected: `spec/10_gateway-internals.md:39` already orders the retry the code refuses, so the spec is right and the code is the defect, and the lens's own guardrail forbids a finding that would document a bug as spec)


### [spec-recheck.2.review-edit-sites.1]

DECISION: Empty findings list, the third consecutive empty return for this lens — BECAUSE the delta since spec-r5 is confined to `spec-changes.md` (two new SPEC-1 paragraphs, the removal of old §7 question 1, and a D2/D4 rewording), every citation in the new text verifies against the tree, and an independent re-sweep of `spec/`, `docs/`, `schemas/`, and `charts/` for every identifier SPEC-1 removes or adds still finds no authored surface outside `spec/04_system-components.md` that becomes wrong — ALTERNATIVES: re-filing the §4.7.1 "declared per message" tension (rounds 2, 3, and 4 of this lens weighed and dropped it, and the delta now defends it explicitly at spec-changes.md:128-139); filing the staged block's reference to "A tier-0 gate" as spec prose about the test harness (spec/18 already names test gates by group, so there is precedent and it is below the bar).

FACT: Every citation the r5→r6 delta added verifies exactly. `spec/10_gateway-internals.md:57` ("it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state") and `:60` ("The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"), both inside §10.1.4 (heading `:53`, next heading `:64`), and `git log -L 60,60:spec/10_gateway-internals.md` names commit `cf20a0646` "Apply proposal 0076 SPEC-1 to spec/10", so the "0076's SPEC-1 under its D5" attribution is right. `spec/04_system-components.md:725`, `:726`; `schemas/lenny-adapter.proto:458` (`SessionId session_id = 2`), `:499-503` (`pod_id`, `outcome`, `detail`); `docs/reference/adapter-contract.md:81`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`. — EVIDENCE: spec/10_gateway-internals.md:53,57,60,64; spec/04_system-components.md:725,726

FACT: The delta's claim "Retiring the table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" holds on a full grep. After `:151` and `:188` go, the remaining `pod-scoped` occurrences in `spec/` are `spec/04:726` (`ReportPodScrub`), `spec/10:60` (the hold and its gauge), `spec/04:872` (the rotation in-flight ceiling), and `spec/12:202` (Redis key prefixes). Only `:726` classifies a request message. — EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60; spec/04_system-components.md:872; spec/12_storage-architecture.md:202

FACT: §4.7.1 carries exactly two per-message scope statements and no more. A `session-scoped` grep over `spec/04_system-components.md` returns `:151`, `:190`, `:725`, plus two unrelated credential-lease sentences at `:1134` and `:1277`; the `pod-scoped` grep returns `:151`, `:188`, `:726`, `:872`. So "the two per-message scope statements in §4.7.1" is an exhaustive description, not a sample. — EVIDENCE: spec/04_system-components.md:725, :726

FACT: Re-derived the proto measurement a fourth time with an independent brace-depth parser rather than trusting the log. 31 distinct RPC request types across the two service blocks; 25 declare a top-level `SessionId session_id`, 6 do not (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`). Across the WHOLE file, at every nesting depth, there is no field named `session_id` that is not of type `SessionId` and no field of type `SessionId` not named `session_id`. `CheckpointRequest` and `CheckpointResponse` are the only messages declaring a `oneof`; only the first is a request type. 25 session + `CheckpointRequest` + `CheckpointStart` = the table's 26 session rows, and the 5 remaining = its pod rows once the fence moves. — EVIDENCE: schemas/lenny-adapter.proto:1173 (`CheckpointRequest`), :1217 (`CheckpointStart`'s `SessionId session_id = 7`), :1249 (`CheckpointResponse`), :1455-1456

WATCHOUT: a naive regex parser over `schemas/lenny-adapter.proto` that keys on `message X {` and pops on a line starting with `}` silently stops finding messages after roughly line 500 and reports two thirds of the request types as MISSING. Count braces per line instead (`depth += line.count('{') - line.count('}')` after stripping `//` comments) and treat depth 0 as the top level. I lost a pass to this before noticing the parse, not the file, was wrong. — EVIDENCE: schemas/lenny-adapter.proto:499-503

FACT: `spec/` contains no occurrence of "tier-0" or "tier 0" anywhere, so the staged constraint paragraph's "A tier-0 gate refuses a protocol definition in which..." is the first spec sentence to name a test tier. It is not unprecedented in kind: `spec/18_build-sequence.md` names `phase-N-gate` test groups as exit criteria throughout. I judged it below the bar; do not file it, but a rewrite that deepened it into a description of the harness would be a different question. — EVIDENCE: spec/18_build-sequence.md:85, :98

USEFUL [spec.4.review-edit-sites.4]: its register sweep (`tests/registers/identifier-senses.yaml`, `pinned-spec-literals.yaml`, `successor_pointer_test.go:52-56`) and its `SessionId`-absent-from-spec finding both held on re-check, which let me spend this pass on the delta and on the uniqueness gates instead of re-deriving the register surface. `identifier_resolution_test.go` is a §28.3 channel-spelling gate and does not reach proto type names, so introducing `SessionId` into spec prose raises no obligation there.

USEFUL [spec.2.review-edit-sites.1]: its `spec/04_system-components.md:725-726` survivor list is the single most reusable fact on this proposal; the delta's new paragraph is essentially that entry promoted into the staging, and it is correct as written.


### [spec-recheck.2.review-feasibility.1]

DECISION: Returned an empty findings list under the actor-action-feasibility lens — BECAUSE the only actor the staged spec text names is "a tier-0 gate", and every clause it is assigned reads out of `schemas/lenny-adapter.proto` text alone (field name, field type, `oneof` membership, and the frame messages a `oneof` arm names), all of which the shared tier-0 parse can be extended to carry; the staging names no controller, no webhook, no agent-pod action, no CRD write, no egress path, and no build-phase deliverable, so §4.6.3, §10.3, §13.2, webhook purity, and spec/18 ordering are all inert here — ALTERNATIVES: filing D2's "it can never fail" justification for dropping the pod-scope clause (a near-variant of an already-refuted finding, and the applied spec stays consistent either way); filing that "frames" is undefined in the staged block (self-defining from the sentence that introduces it, and §4 makes the predicate mechanically evaluable).

FACT: every proto anchor in the current spec-changes text is correct at HEAD. `ReportSessionScrubRequest`'s `SessionId session_id = 2` is at :458 (message opens :456), `ReportPodScrubRequest`'s three fields are :500-502 with the message at :499 and the close at :503, `CheckpointStart`'s `SessionId session_id = 7` is at :1217, `message SessionId` at :596, `CheckpointRequest` at :1173, `CoordinatorFenceRequest` at :1455 with its field at :1456. The round-1 seven-line drift is gone. — EVIDENCE: schemas/lenny-adapter.proto:456,458,499-503,596,1173,1217,1455

FACT: the addressing convention the replacement gate checks already holds on the tree in both directions, so the gate is green on its first run. `grep -nE "^[[:space:]]*(repeated[[:space:]]+)?[A-Za-z0-9_.]+[[:space:]]+session_id[[:space:]]*="` minus `SessionId` returns nothing, and the same grep for `SessionId <name> =` minus `session_id` returns nothing. `oneof` appears at exactly two message sites (`CheckpointRequest` :1174, `CheckpointResponse` :1250) and only the first is an RPC request type, and its frames declare exactly one address. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250, :1217

FACT: the delta's new §4.7.1 paragraph verifies in full. `spec/04_system-components.md:725` is the `ReportSessionScrub` row ending "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` is the `ReportPodScrub` row ending "The request is pod-scoped."; the tier-11 gate reading both carriers is `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` at :43-76, and `docs/reference/adapter-contract.md:81` is the doc row it reads. — EVIDENCE: spec/04_system-components.md:725, :726; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; docs/reference/adapter-contract.md:81

FACT: SPEC-1's claim that `:726` is left as the only sentence in `spec/` classifying one request message pod-scoped is exact. `grep -rn "pod-scoped" spec/` returns six lines: spec/04:151, :188, :726, :872, spec/10:60, spec/12:202. :151 and :188 go with the retirement; :872 is a credential-rotation ceiling, spec/10:60 is the hold gauge, spec/12:202 is a Redis key prefix, none of which classifies a request message. — EVIDENCE: spec/04_system-components.md:726, :872; spec/12_storage-architecture.md:202

FACT: the delta's two `spec/10` anchors are correct and the attribution to 0076 is correct. `spec/10_gateway-internals.md:57` carries "it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state"; `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; 0076's SPEC-1 states it stages exactly that clause under its D5. — EVIDENCE: spec/10_gateway-internals.md:57, :60; proposals/0076_.../0076_....spec-changes.md:319-321

WATCHOUT: 0073's SPEC-7 staged the WHOLE `#### Request Message Scope` block, including the `ShutdownRequest` paragraph that landed at `spec/04_system-components.md:190` (0073's own §4.1 carries that paragraph at proposals/0073_...md:963). SPEC-1's apposition "replace the whole block 0073's SPEC-7 stages, which is [:151, :153-186, :188]" therefore under-describes what SPEC-7 staged. I did NOT file it: the line enumeration is explicit and the very next sentence says `:190` stands unedited, so an implementor cannot be misled, and it is the same class as the already-refuted "leaves the lead paragraph describing a table that no longer exists". Do not spend a finding on it. — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:2825, :963; spec/04_system-components.md:190

FACT: `spec/` contains no occurrence of "tier-0" or "tier 0" anywhere today, so the staged constraint paragraph introduces the first one. That is not a defect: `spec/28_communication-channels.md:64` already names gates in normative prose ("The citation resolver and the line-citation ratchet are the gates that hold this rule"), and §18 links TESTING.md throughout. — EVIDENCE: spec/28_communication-channels.md:64; spec/18_build-sequence.md:3

FACT: the blast radius of retiring the table is exactly the six paths §9 lists. `protoFields`/`protoServiceRequests`/`adapterProtoPath` have one caller outside the parse file and the retiring gate, `claim_register_proto_agreement_test.go:64`. The only other file naming the gate's path is `tests/tier0_static/spec_map_slot_address_registration_test.go:336`, which names it as a path in `slotAddressCaseFiles` and needs no edit while the replacement keeps that path. The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) read proto doc comments and Go doc comments with their own parsers and never touch the §4.1 table or the shared parse. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:33-90; tests/tier0_static/claim_register_proto_agreement_test.go:64; tests/tier0_static/spec_map_slot_address_registration_test.go:336

UNVERIFIED [test lane, not this loop]: adding `CoordinatorFenceRequest` to `sessionScopedMessages` puts it inside `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` (tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-142), which asserts the message reserves the recorded field number AND the name `slot_id`. `CoordinatorFenceRequest` reserves neither, so that arm turns red unless TEST-2 changes it. This is exactly what §7's surviving open question is about, so it is settled outside review, but the implementor needs the arm change and not only a column value. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-142; schemas/lenny-adapter.proto:1455-1460

FACT: the delta removed §7's question 1 (whether the replacement gate closes the hole the table's gate closed) and rewrote D2's and D4's clauses that pointed at it, leaving one open question. Removing or reframing an open decision is not filable under the review rubric, so a later reviewer should not spend a finding on its absence either. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:152-155

USEFUL [spec.1.fix-G2.1]: its enumeration of the spec-map credits (:156, :169, :5670) and of `slotAddressCaseFiles` was exact and re-verified; it saved a full register sweep.


### [spec-recheck.2.review-fresh.1]

DECISION: Empty findings on the staged spec edits — BECAUSE I re-derived the classification mechanically from `schemas/lenny-adapter.proto` (31 RPC request types, 25 with a top-level `SessionId session_id`, 6 without) and re-read every citation in `spec-changes.md` against the tree, and all of them resolve; the round-2 delta is a deletion (§7's retirement/withdrawal question and the two sentences in D2 and D4 that pointed at it), which removes text rather than asserting anything new — ALTERNATIVES: filing that the staged lead sentence ("the classification is derived from the message's field set rather than declared per message") coexists with two surviving per-message declarations at `spec/04_system-components.md:725` and `:726` (rejected: the values agree, §5's new paragraph argues the case explicitly, and the fix is a rewording — the class this lane has refuted repeatedly); filing the `AdapterEventsRequest` question below (rejected as interpretive, see OPEN).

FACT: The round-2 delta to `spec-changes.md` is subtractive only. `diff` against the r5 snapshot shows §7's first open question (retire-or-withdraw) deleted, plus the clause in D2 ("Whether that residual is smaller than what the retired table cost is §7's first question") and the clause in D4 ("survives as §7's first question") that referenced it. §7 now carries one question, the TEST-2 retired-field-number column. The §4.7.1 paragraph in §5 is NOT part of this delta; it landed before recheck.1 and that round's fresh lens already verified it. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.spec-changes.md:151-155

FACT: Every citation in the staged spec lane re-verified today, line for line: `spec/04_system-components.md:149` (`#### Request Message Scope`, inside §4.1 whose heading is `:40`), `:151`, the 32-row table at `:153-186`, `:175`, `:188`, `:190`, `:725`, `:726`; `spec/10_gateway-internals.md:53` (§10.1.4), `:57`, `:60`; `spec/05_runtime-registry-and-pool-model.md:515` (the value rule, inside the "Concurrent sessions" paragraph); `schemas/lenny-adapter.proto:458`, `:499-503`, `:1217`, `:1455-1456`; `pkg/adapter/checkpoint.go:74-84`; `pkg/adapter/coordination.go:107`, `:116`; `pkg/adapter/server.go:302`; `pkg/adapter/slot.go:59`; `tests/tier0_static/adapter_proto_message_scope_test.go:17-27`; `tests/tier0_static/adapter_proto_parse_test.go:10-15`; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`; `docs/reference/adapter-contract.md:81`. — EVIDENCE: spec/04_system-components.md:149

WATCHOUT: `tests/tier0_static/address_rule_citation_test.go` scans every file under `spec/` for the sentence fragment "rejected at the adapter boundary with `InvalidArgument`" and FAILS when more than one section carries it, because the section a case must cite would then be undetermined. The staged §4.1 block does not restate the value rule, so tier 0 stays green — but any fixer tempted to restate the empty-identifier refusal in §4.1 alongside the derivation rule would turn tier 0 red for a reason nothing in this proposal predicts. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22, :50-53

FACT: §9's inventory of the shared-parse callers is complete. `protoFields` and `protoServiceRequests` (`tests/tier0_static/adapter_proto_parse_test.go:36`, `:68`) have exactly two callers between them: the retiring gate (`adapter_proto_message_scope_test.go:87`) and `claim_register_proto_agreement_test.go:64`. The two sibling tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) call neither, and only two tier-0 files read `spec/04_system-components.md` at all: the retiring gate and `adapter_proto_event_taxonomy_test.go`, which reads §4.7.3 and is untouched by SPEC-1. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36, :68

FACT: Nothing outside the retiring gate depends on the §4.1 table. A sweep of `spec/`, `docs/`, `schemas/`, and `charts/` for the `#request-message-scope` anchor, the heading text, and "message scope" returns only the heading itself; no `docs/` page states `CoordinatorFence`'s scope class (`docs/reference/adapter-contract.md:69` names the RPC and states no class); and `tests/tier11_docs/successor_pointer_test.go` names its reducing sections in a literal list rather than deriving them, so retiring the table triggers no successor-pointer obligation. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:34-39

OPEN: Does `AdapterEventsRequest` falsify the staged clause "a top-level `session_id` field of type `SessionId` ... is the only way a request on this protocol addresses a session"? The message declares `bytes envelope_json = 1` alone (`schemas/lenny-adapter.proto:1769-1771`), while §28.5.2's `CH-ADAPTEREVENTS` card states the stream carries `AdapterTerminating` "carrying `session_id` and `reason`" (`spec/28_communication-channels.md:437`), so a session identifier does travel on a request message of this protocol under a name and a type the gate cannot see — which the staged third paragraph calls non-conforming. I did not file it: the classification does not change (the table already declared the message pod-scoped, and the rule derives the same), and the identifier sits inside an opaque event payload rather than in the message's own address field, which is the sense §4.1 uses. A future round that wants to tighten the "only way" clause has the evidence here; a round that wants to leave it has the reasons.

USEFUL [spec-recheck.1.review-fresh.1]: its mechanical re-derivation of the 25/6 split and its list of the six address-free request messages matched my own independent parse exactly, and its WATCHOUT about a naive brace counter stopping at `message ReportPodScrubResponse {}` (`schemas/lenny-adapter.proto:508`) saved me a wrong "message does not exist" conclusion — I handled the same-line close from the start because of it.


### [spec-recheck.2.review-kubernetes.5]

DECISION: Returned an empty findings list, third empty return for this lens on this proposal — BECAUSE the
round-5 delta to `spec-changes.md` adds no Kubernetes surface at all. The delta is three things: `§7`'s
existential question 1 moved out to the summary as OD1 (so D2 and D4 lost their `§7` back-references and
now carry the argument inline), and SPEC-1 gained two new paragraphs, one explaining why the replacement
block restates nothing about the coordinator hold and one explaining why §4.7.1's two per-message scope
sentences stand unedited. Neither paragraph names a CRD, a status subresource, a field manager, a
finalizer, an admission webhook, a work queue, or a reconcile loop — ALTERNATIVES: I re-derived the
session-scoped-fence-versus-pod-scoped-hold granularity question that the new paragraph now surfaces
explicitly, and stood down on [spec.4.review-kubernetes.1]'s WATCHOUT plus the `ShutdownRequest` precedent
at `spec/04_system-components.md:190`.

FACT: Every citation the round-5 delta introduces verifies exactly. `spec/10_gateway-internals.md:57` is
the Hold state semantics bullet and does say `CoordinatorFence` RPCs are "the only way to exit hold state";
`:60` is the Observability bullet and closes "The hold itself and the `lenny_adapter_coordinator_hold`
gauge remain pod-scoped"; that sentence was indeed landed by 0076's SPEC-1 under its D5
(`proposals/0076_.../0076_....spec-changes.md:319-321`). `spec/04_system-components.md:725` and `:726` are
the `ReportSessionScrub` / `ReportPodScrub` rows and sit inside §4.7.1 (heading `:692`, §4.7.2 at `:728`).
`schemas/lenny-adapter.proto:458` is `SessionId session_id = 2;` in `ReportSessionScrubRequest` (`:456`),
and `:499-503` is `ReportPodScrubRequest` with exactly `string pod_id`, `PodScrubOutcome outcome`, and
`string detail`. `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` row, and
`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` is the gate that pins
the identical opener on both carriers. — EVIDENCE: spec/10_gateway-internals.md:57, :60; spec/04_system-components.md:725-726; schemas/lenny-adapter.proto:458, :499-503

FACT: SPEC-1's new claim that retiring the table leaves `spec/04_system-components.md:726` as the only
sentence in `spec/` classifying one request message pod-scoped is true. `grep -rn "pod-scoped" spec/`
returns exactly six hits: `spec/10:60` (the hold, not a message), `spec/12:202` (Redis key prefixes),
`spec/04:151` and `:188` (both retired by SPEC-1), `spec/04:726`, and `spec/04:872` (credential rotation).
— EVIDENCE: spec/04_system-components.md:726

FACT: No unstaged spec edit site exists outside §4.1 for the reclassification. `grep -rn "CoordinatorFence" spec/`
returns 25 hits; the only two that state a scope class are `spec/04:175` and `:188`, both inside the block
SPEC-1 replaces. `spec/04:712` (the §4.7.1 gateway→adapter row) carries no scope cell, and
`spec/28_communication-channels.md:314-317` already states the per-session semantics the reclassification
needs ("A fence for one session does not change the generation the pod holds for another"). — EVIDENCE: spec/04_system-components.md:712; spec/28_communication-channels.md:314-317

USEFUL [spec.4.review-kubernetes.1]: its WATCHOUT on the session-scoped fence versus pod-scoped hold saved
me from re-filing a granularity mismatch I derived independently. The round-5 delta now writes that
reconciliation into SPEC-1 itself (`spec-changes.md:116-121`), so the tension is documented in the proposal
rather than only in this log. Any future round that reaches for it should stop there.

WATCHOUT: the round-5 delta REMOVED §7's question 1 (the "does the new gate close the hole the table's gate
closed, or withdraw" question) from `spec-changes.md`. It was not dropped: it now lives in
`summary.md:82-137` as OD1 with a stated recommendation. §7's surviving question 1 is the TEST-2
retired-field-number column, which `summary.md:139-150` carries as OD2. A reviewer briefed on "§7 question
1 is the existential question" will not find it where the brief says. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:152-155; proposals/0075_.../0075_....summary.md:82



### [spec-recheck.2.review-mechanism.1]

DECISION: Returned an empty findings list for the end-to-end mechanism lens on the post-r5 spec staging — BECAUSE the delta is four edits (D2 and D4 losing their cross-references to the withdrawn §7 question 1, SPEC-1 gaining a coordinator-hold paragraph and a §4.7.1 paragraph, §7 question 1 removed) and every citation in the two new paragraphs verifies exactly, while the rest of the staging re-verified end to end — ALTERNATIVES: filing the nested-address blind spot and the "the addressing frame opens the stream" unchecked conjunct; both stood down, see below.

FACT: Every citation in SPEC-1's two NEW paragraphs verifies today. `spec/10_gateway-internals.md:53` opens §10.1.4; `:57` states the fence is the only exit from hold state; `:60` states the hold and the `lenny_adapter_coordinator_hold` gauge stay pod-scoped and `git blame` attributes it to `cf20a0646` "Apply proposal 0076 SPEC-1 to spec/10", so "which proposal 0076's SPEC-1 landed under its D5" is exact. `spec/04_system-components.md:725` and `:726` are the §4.7.1 rows (§4.7.1 heading at `:692`, §4.7.2 at `:728`); `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2` and `:499-503` is `ReportPodScrubRequest` with `pod_id`/`outcome`/`detail` alone; `docs/reference/adapter-contract.md:81` carries the mirrored sentence; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` is exactly the gate function that pins both carriers on the shared opener. — EVIDENCE: spec/10_gateway-internals.md:53,:57,:60; spec/04_system-components.md:692,:725,:726; schemas/lenny-adapter.proto:458,:499-503; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: "Retiring the table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" is true. A repo-wide grep for `pod-scoped` over `spec/` returns, besides the retiring `:188`, only `spec/04:726` (a request message), `spec/04:872` (the rotation ceiling), `spec/10:60` (the hold and its gauge), and `spec/12:202` (Redis key families). None of the last three classifies a request message. — EVIDENCE: spec/04_system-components.md:726, :872; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

FACT: No dangling cross-reference survives the removal of §7 question 1. A grep for `§7`, `first question`, and `second question` across every file in the proposal directory except the review log returns nothing. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/*.md

FACT: The registers under `tests/registers/` are not an edit site for SPEC-1, which is worth knowing because two of them are keyed by ORDINAL POSITION within a file and would break if a deletion removed a counted site. `identifier-senses.yaml` keys eight `spec/04_system-components.md` entries by the 1-based position of a retired channel spelling in source order, and `pinned-spec-literals.yaml` keys entries by literal position inside tier-11 Go carriers. The `#### Request Message Scope` block (`:149-190`) contains no channel spelling and no tier-11 carrier pins it, so deleting `:151-188` shifts no ordinal. — EVIDENCE: tests/registers/identifier-senses.yaml:13-36; tests/registers/pinned-spec-literals.yaml:1-19

WATCHOUT: I independently re-derived, and stood down on, the same two candidates the round-2 and round-4 mechanism passes recorded. (1) A conventionally-named-and-typed `session_id` nested inside a non-oneof sub-message escapes both gate spelling clauses while the containing request derives pod-scoped — refuted because the field the REQUEST declares at top level is the wrapper (`Foo foo`), whose name and type are both unconventional, so the staged sentence "a session addressed under a name and a type that are both unconventional" already reads onto it. (2) The staged text asserts the envelope's addressing frame "opens the stream" and the gate has no clause for that, so an address moved to `CheckpointGrant` would pass every stated clause while `pkg/adapter/checkpoint.go:60-64` requires `CheckpointStart` first — refuted because the derived classification (session-scoped) is unchanged either way, the case is hypothetical on today's proto, and it fails closed at the handler. A fifth pass over the constraint paragraph's exhaustiveness is not worth a round. — EVIDENCE: proposals/0075_.../0075_....spec-changes.md:20-25; schemas/lenny-adapter.proto:1217; pkg/adapter/checkpoint.go:60-84

USEFUL [spec.4.review-mechanism.1]: its WATCHOUT that `spec-changes.md` §5 no longer carries an `IMPLEMENTOR TO FILL THE BLANKS` banner (only `non-spec-changes.md` §5 does) is correct and still holds after this round's edits, and it is the right calibration for judging SPEC-1: SPEC-1 is a firm instruction with quoted replacement text, so the "indicative block" defence in the refuted list does not apply to it. Its USEFUL pointer to the round-2/round-3 stand-downs on the nested-address hole saved me from filing a finding I had derived independently.


### [spec-recheck.2.review-operational.1]

DECISION: Returned an empty findings list under the operational-consistency lens — BECAUSE this proposal touches no condition, no metric, no alert, and no runbook, and every operational citation the round-6 delta added verifies against the tree — ALTERNATIVES: filing D5's spent conditional ("inert until 0073 is applied", 0073 is Implemented) and SPEC-1's closing "Written against whatever 0076 leaves in §4.1" (0076 landed and left §4.1 untouched); both are stale conditionals rather than false statements, and this loop has refuted five findings of that weight already.
FACT: The derivation D1 states reproduces the §4.1 table exactly. A parse of the two service blocks returns 31 request types, 25 declaring a top-level `SessionId session_id`, and diffing the derived class against every table row leaves exactly two mismatches: `CheckpointRequest` (declared "session (stream envelope...)", derives pod — the envelope clause's subject) and `CoordinatorFenceRequest` (declared pod, derives session — 0076's OD3). No third counterexample exists. — EVIDENCE: spec/04_system-components.md:153-186; schemas/lenny-adapter.proto:1174 (the only request-side `oneof`), :1250 (`CheckpointResponse`, not a request type)
FACT: Nothing outside spec/04 §4.1 restates the request-message classification. `grep -rn "pod-scoped" spec/` returns only :151, :188, :726 (plus spec/10:60 for the hold gauge, spec/04:872 for a rotation ceiling, and spec/12:202 for Redis keys, none of which classify a request message), and no docs/ page, runbook, alert, or tier-11 gate reads the table. Every inbound `§4.1` cross-reference in spec/ points at Edge Gateway Replicas generally (HPA metrics at spec/10:77, Upload Handler at spec/07:459 and spec/13:736), never at the `#### Request Message Scope` subsection, so retiring the table breaks no anchor and needs no redirect. SPEC-1's claim that `:726` is the only surviving pod-scope classification of a request message is therefore true. — EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60
FACT: Every citation the round-6 delta added to SPEC-1 verifies. `spec/10_gateway-internals.md:57` states the fence is the only exit from hold state and `:60` states the hold and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped; both sit inside §10.1.4 (heading at :53), and `git log -S "remain pod-scoped" -- spec/10_gateway-internals.md` returns cf20a0646 "Apply proposal 0076 SPEC-1 to spec/10", so the attribution to 0076's SPEC-1/D5 is right. `:725`/`:726`, `schemas/lenny-adapter.proto:458` and `:499-503`, `docs/reference/adapter-contract.md:81`, and `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` all say what the paragraph claims. — EVIDENCE: spec/10_gateway-internals.md:53,57,60
FACT: Reclassifying `CoordinatorFenceRequest` session-scoped creates no unmet spec obligation in the adapter. The §4.2/spec-05 value rule (`spec/05_runtime-registry-and-pool-model.md:515`) demands an `InvalidArgument` refusal of a session-scoped request with an empty identifier, and the fence handler already does exactly that before resolving anything. So §3's "No proto or handler changes" holds. — EVIDENCE: pkg/adapter/coordination.go:107-110
WATCHOUT: `spec-changes.md` §7 now lists ONE open decision (the retired-field-number column); the round-6 delta deleted the retire-or-withdraw question from it. `summary.md` still carries that question as OD1 with a full recommendation (:82-136), and the summary's OD2 is §7's surviving item. Do not read §7 as the complete open set, and do not file the mismatch: how an open decision is framed is outside a reviewer's grant here. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md:82
WATCHOUT: `TestAdapterProtoRequestMessagesAreClassifiedByScope` feeds the WHOLE of spec/04 into `parseMessageScopeTable`, not the §4.1 section alone, and the row regex matches any 4-column row whose first two cells are single backticked words. Whoever writes the replacement gate should not assume the old one was section-scoped. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43, :139-147
USEFUL [spec.1.fix-G1.1]: the recorded proto anchors (:1173, :1217, :1455-1456, :596) and the "spec/05:515 is one very long line, grep the sentence" note were both correct and saved a re-derivation.


### [spec-recheck.2.review-performance.1]

DECISION: Returned no findings under the performance/scalability/failure-mode lens — BECAUSE the whole staging is spec prose plus a tier-0 static gate: it creates no control-plane or data-plane write, no new etcd status write, no new informer watch, no new Redis or Postgres key, and no per-request work at any tier. There is no write-rate math to state. ALTERNATIVES: filing the fence's pod→session reclassification as a coordinator-handoff regression (rejected: 0076 already landed the per-slot generation in code, so the fence-per-bound-session RPC count during a handoff predates this proposal and is bounded by `sessionPolicy.maxConcurrentSessions`; SPEC-1 changes only the class word in §4.1 and explicitly leaves `spec/10` §10.1.4 alone).

FACT: The staged tier-0 gate PASSES on the protocol definition as it stands today, which I checked rather than assumed. Every field of type `SessionId` in `schemas/lenny-adapter.proto` is named `session_id` (26 sites), there is no `string session_id` anywhere, and there is no `reserved "session_id"`. `CheckpointRequest` declares no top-level address (only `int64 coordination_generation = 4` outside the `oneof`) and exactly one of its three frames declares one (`CheckpointStart`, `SessionId session_id = 7`). So none of D1's four refusal clauses fires on first run. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187 (CheckpointRequest), :1217 (CheckpointStart address), :596 (message SessionId)

FACT: A mechanical parse of the two service blocks (re-run today) returns 31 request messages, of which exactly 6 declare no `SessionId session_id`: `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. `ShutdownRequest` DOES declare one, so the derivation classifies it session-scoped and agrees with the paragraph SPEC-1 leaves standing at `spec/04_system-components.md:190`. The §1.1 measurement (25/6) and D1's "reproduces every classification the retired table carried" both hold arithmetically: 32 table rows = 31 request messages + the `CheckpointStart` row. — EVIDENCE: schemas/lenny-adapter.proto; spec/04_system-components.md:153-186

FACT: Every citation added by the r5→r6 delta verifies. `spec/04_system-components.md:725` is the `ReportSessionScrub` row ending "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` is the `ReportPodScrub` row ending "The request is pod-scoped."; `spec/10_gateway-internals.md:57` carries "which is the only way to exit hold state" and `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped."; `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2;` in `ReportSessionScrubRequest`; `:499-503` is `ReportPodScrubRequest` with `pod_id`, `outcome`, `detail` and no address; `docs/reference/adapter-contract.md:81` is the mirrored `ReportSessionScrub` row; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` is exactly that test function. — EVIDENCE: as listed

FACT: SPEC-1's claim that retiring the table "leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" survives a full `grep -rn "pod-scoped" spec/`. The other hits are not request messages: `spec/10:60` (the hold gauge), `spec/04:872` (the 300-second rotation in-flight ceiling), `spec/12:202` (Redis `lenny:pod:{pod_id}:*` slot-counter keys). — EVIDENCE: spec/04_system-components.md:872; spec/12_storage-architecture.md:202

FACT: Only ONE file in the tree parses the §4.1 table. `grep -rln "parseMessageScopeTable\|messageScopeSpecPath\|Request Message Scope"` over tests/ and scripts/ returns `tests/tier0_static/adapter_proto_message_scope_test.go` alone. The two sibling tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) do not read it, so neither is a missing §9 edit site. No proto doc comment and no page under `docs/` states a per-request-message scope except `docs/reference/adapter-contract.md:81`, which SPEC-1 correctly leaves alone. — EVIDENCE: grep over tests/, scripts/, docs/, schemas/

FACT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` and `:337` carry `adapter_proto_message_scope_test.go` and `adapter_proto_parse_test.go` in the hard-coded `slotAddressCaseFiles` inventory, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970`) requires every `// spec:` section those files annotate to be credited in `tests/spec-map.json`. TEST-1 keeps both PATHS, so the inventory stays accurate and that file is NOT a missing edit site — but if an implementor ever renames or splits the gate file, this inventory is a third place to update beyond `spec-map.json`. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:236, :336, :970, :1129

USEFUL [spec.*]: the earlier rounds' FACTs that the fence handler already returns `InvalidArgument` on an empty session id (`pkg/adapter/coordination.go:107-111`) saved me re-deriving whether the pod→session reclassification imports an unstaged code obligation from the value rule at `spec/05_runtime-registry-and-pool-model.md:515`. It does not.


### [spec-recheck.2.review-reliability.1]

DECISION: Returned an EMPTY findings list under the reliability/fault-tolerance lens, the third consecutive empty round for this lens (see [spec.1.review-reliability.1], [spec.4.review-reliability.1]) — BECAUSE the r5→r6 delta in `spec-changes.md` adds no operational mechanism: it deletes the old §7 question 1, deletes the two D2/D4 sentences that cross-referenced it, and adds two prose paragraphs to SPEC-1 (one explaining why §4.1 restates nothing about the coordinator hold, one recording that §4.7.1's two per-message scope statements stand unedited). Neither paragraph writes, moves, or removes a retry, lease, fence-acceptance, drain, dedup, or store-failover predicate — ALTERNATIVES: filing the CoordinatorFence equal-generation retry hole again (declined for the third time: it is pre-existing, its remedy is in `spec/10`, and the summary now inventories it explicitly at summary.md:161-179 with proposal 0080 §1.16 named as owner); filing that `spec/04:726`'s surviving declared pod-scope is reconciled against the proto by no gate after the table's gate retires (declined: it was ungated before this change too, so nothing regresses, and the only failure mode is a hypothetical future proto edit).

FACT: every citation the new SPEC-1 paragraphs introduce resolves and says what is claimed. `spec/10_gateway-internals.md:57` is the "accepts `CoordinatorFence` ... which is the only way to exit hold state" bullet; `:60` ends "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped"; both sit under `#### 10.1.4` at `:53`. `spec/04_system-components.md:725` is the `ReportSessionScrub` row ending "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` is the `ReportPodScrub` row ending "The request is pod-scoped." `schemas/lenny-adapter.proto:458` is `SessionId session_id = 2;` inside `ReportSessionScrubRequest` (opens `:456`); `:499-503` is all of `ReportPodScrubRequest` (`pod_id`, `outcome`, `detail`). `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` doc row carrying the identical sentence, and `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76` is exactly that gate function. — EVIDENCE: spec/10_gateway-internals.md:53,57,60; spec/04_system-components.md:725,726; schemas/lenny-adapter.proto:456,458,499-503; docs/reference/adapter-contract.md:81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43

FACT: `git blame` confirms the attribution clause "which proposal 0076's SPEC-1 landed under its D5" is correct for `spec/10:60` (commit `cf20a0646` "Apply proposal 0076 SPEC-1 to spec/10", and 0076's spec-changes.md:20 is "**D5. The coordinator-loss hold stays pod-scoped.**"). It is NOT correct for `:57`, which predates 0076 by five months (`db8cdc224f`, 2026-04-16). The proposal's relative clause attaches to the nearer `:60` claim, so the sentence is true as written; do not file it, but do not "helpfully" widen it to cover both anchors either. — EVIDENCE: git blame -L 57,57 -L 60,60 spec/10_gateway-internals.md; proposals/0076_fix_scope-the-coordination-generation-to-the-session/...spec-changes.md:20

FACT: D2's replacement gate still passes on the tree in both directions, re-verified this round with two greps: no field of type `SessionId` carries a name other than `session_id`, and no field named `session_id` carries a type other than `SessionId`, across the whole file. `CheckpointRequest`'s only top-level non-`oneof` field is `int64 coordination_generation = 4` (`:1186`), so the "an envelope declares no address of its own" clause also passes today. The gate does not land tier 0 red. — EVIDENCE: schemas/lenny-adapter.proto:1173-1187

FACT: `AdapterEventsRequest` is `bytes envelope_json = 1;` and nothing else. It carries no `oneof`, so the stream-envelope clause does not reach it and it derives pod-scoped, matching the retired table's row. Its per-event `AdapterTerminating(session_id, ...)` payload described in `spec/10` §10.1.4 does not make it a stream envelope under D1's definition, which keys on a `oneof` of frames rather than on a payload naming a session. I chased this as a possible reclassification and it dissolved. — EVIDENCE: schemas/lenny-adapter.proto (message AdapterEventsRequest); spec/10_gateway-internals.md:58

FACT: after the table retires, `spec/` still carries two per-message scope declarations outside §4.1: `spec/04:190` (`ShutdownRequest` "is session-scoped", the paragraph SPEC-1 keeps) and `spec/04:726` (`ReportPodScrub` "The request is pod-scoped"). SPEC-1's claim is scoped to POD-scoped and is therefore accurate. A repo-wide grep for `pod-scoped` over `spec/` returns six hits; the other four are `spec/04:151` and `:188` (both retired), `spec/04:872` (a rotation ceiling, not a message), and `spec/12:202` (Redis key prefixes, not messages). — EVIDENCE: spec/04_system-components.md:190, :726, :872; spec/12_storage-architecture.md:202

FACT: nothing in `spec/`, `docs/`, `schemas/`, or `charts/` refers to the retiring block by name or anchor. `grep -rn "request-message-scope\|Request Message Scope\|message-scope\|message scope"` over those four trees returns exactly one hit, the heading itself at `spec/04_system-components.md:149`. Retiring the block dangles no cross-reference. — EVIDENCE: spec/04_system-components.md:149

FACT: the old existential §7 question 1 was not dropped, it MOVED. It is now `summary.md` OD1 ("Retire the §4.1 declared-scope table and its tier-0 reconciliation gate, or withdraw this proposal?") with a recommendation and a confidence, and the TEST-2 column question is now both `spec-changes.md` §7's only item and `summary.md` OD2. No file in the proposal still cites "§7's first question" for the residual argument. Any orchestrator note or earlier shard describing §7 question 1 as the existential one is stale. — EVIDENCE: proposals/0075_.../0075_....summary.md:82-98, :139-150; proposals/0075_.../0075_....spec-changes.md:152-155

USEFUL [spec.4.review-security.1]: its FACT that the derived session class is a strict superset of the retired table's (delta of exactly one message, pod→session) is the single fact that makes this retirement safe for the reliability lens too, because the `spec/05:515` empty-identifier refusal hangs off the session class and can therefore only be ADDED, never removed. I re-derived it and it holds. Nobody needs to derive it a fourth time.

USEFUL [spec.4.review-reliability.1]: its FACT that `spec/28_communication-channels.md:314-318` already reads per-session saved me from filing §28 as a missed edit site.


### [spec-recheck.2.review-security.1]

DECISION: Returned an EMPTY findings list for the security lens on the r6 spec staging — BECAUSE the whole delta since r5 is (a) the deletion of §7 question 1, which moved to the summary as OD1 with a recommendation, (b) two new SPEC-1 paragraphs, and I re-verified every citation in both new paragraphs against the tree; nothing in the delta removes, bypasses, or feature-gates a control, and the one classification change stays strictly additive on the fail-closed side — ALTERNATIVES: filing the retirement of 0073's tier-0 table gate as a removed mandatory control (rejected: it is removed openly, the staged spec block states the replacement gate's clauses AND its limit in normative text, and the residual is now a recorded human decision in the summary's OD1); filing the fail-open residual (a session addressed under an unconventional name and an unconventional type deriving pod-scoped, escaping the empty-identifier refusal at spec/05:515) (rejected: the staged constraint paragraph states exactly that residual, so it is disclosed rather than silent, and the same call was made and recorded at [spec.2.review-security.1] and [spec.3.review-security.1]).

FACT: Every citation in the two paragraphs the r6 delta added to SPEC-1 resolves and says what the proposal claims. `spec/10_gateway-internals.md:57` = "accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state"; `:60` = the `lenny_adapter_coordinator_hold` gauge and the pod-level `coordinator_connection_lost` event, ending "no pod-level fenced generation remains"; `spec/04_system-components.md:725` ends "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `:726` ends "The request is pod-scoped."; `docs/reference/adapter-contract.md:81` carries the identical `ReportSessionScrub` sentence; the tier-11 gate is `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` spanning :43-76 and pinning that exact opener at :67-74. — EVIDENCE: spec/10_gateway-internals.md:57,:60; spec/04_system-components.md:725-726; docs/reference/adapter-contract.md:81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: SPEC-1's new claim that retiring the table "leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped" holds. `grep -rn "pod-scoped\|pod scoped" spec/` returns six hits: spec/04:151 and :188 (both retired by SPEC-1), spec/04:726 (ReportPodScrub), spec/04:872 (a credential-lease rotation ceiling, "pod-scoped with it"), spec/10:60 (the hold gauge), and spec/12:202 (Redis key prefixes). None of the last three classifies a request message. — EVIDENCE: spec/04_system-components.md:872; spec/12_storage-architecture.md:202

FACT: Re-derived the addressing convention across the WHOLE proto with a brace-depth parser, not just RPC request types: there is no field named `session_id` of any type other than `SessionId`, and no field of type `SessionId` under any other name, in any message including `oneof` members. `grep -nE "^\s+[A-Za-z0-9_.]+\s+\w*session\w*\s*=" schemas/lenny-adapter.proto` filtered of `SessionId session_id` returns exactly one line, `bool mid_session = 4;`. So D2's replacement gate passes on the tree as it stands, in both directions and on both envelope clauses. — EVIDENCE: schemas/lenny-adapter.proto:724

FACT: §28.5.3's "session-scoped frame set" is a DIFFERENT population from §4.1's request-message classification and does not defer to it. §28.5.3 enumerates its six frame types by name (`message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, `status`) and states its own addressing predicate, so retiring the §4.1 table leaves it untouched. I chased this because `tests/spec-map.json` credits the retiring gate under section 28.5.3 and it looked like a coupled surface; it is not. — EVIDENCE: spec/28_communication-channels.md:834-844

FACT: `spec/28_communication-channels.md:314-317` (CH-FENCE) and `spec/10_gateway-internals.md:38-40` (§10.1.2 steps 2 and its gap-detection bullet) already describe the fence per bound session ("The pod holds `last_fenced_generation` per bound session"), so the derived session scope creates no contradiction in either place and neither is a missed edit site. — EVIDENCE: spec/28_communication-channels.md:314-317; spec/10_gateway-internals.md:40

USEFUL [spec.3.review-security.1]: its WATCHOUT that a session-scoped fence exiting a pod-wide hold looks like an isolation question but belongs to 0076's OD3 saved me a round. I re-derived it from spec/10:57-58 and the same conclusion holds: the staged §4.1 block states nothing about hold state, and §5 now says so explicitly and points at §10.1.4 as the owner.

USEFUL [spec.2.review-security.1]: its DECISION not to file the nested-address hole (a request declaring `Foo foo = 1` where `Foo` carries a conventional `SessionId session_id`) held on my own re-examination for the same reason. Do not re-open.

### [f2.out-of-scope-defects]

DECISION: The out-of-scope call on the equal-generation re-fence stands and its row under `## Defects in the shipped tree that this proposal does not stage` is kept, with one factual correction to its consequence sentence — BECAUSE nothing this proposal stages reads the fence's acceptance predicate (SPEC-1's staged §4.1 block states a field-set rule, a stream-envelope clause, and an addressing convention and is silent on acceptance; TEST-1's replacement gate parses `schemas/lenny-adapter.proto`; TEST-2's tier-3 suite resolves generated descriptors and invokes no handler), the one behavioral obligation the session-scoped reclassification imports is already met in the shipped handler, and proposal 0080 §1.16 states the remedy in full — ALTERNATIVES: staging the repair here (adds a handler comparison, a `CoordinatorFenceResponse` wire comment, and the §10.1.2, §28, and §29.8 refusal arms to a proposal whose staged surface is one spec block, one tier-0 gate, and one tier-3 map, contradicts its own non-goal at spec-changes.md:148-149, and duplicates 0080 §1.16); leaving the row exactly as found (it misstated the failure, and a later owner reading it as a full account would size the remedy wrongly).

FACT: The shipped driver does not burn the whole retry budget on a lost acknowledgement. Attempt 1's deadline fault is taken by the transient arm and retried at the same value (`pkg/gateway/coordination/coordfence/coordfence.go:180-183`); attempt 2 is refused `FailedPrecondition` because the pod already recorded the generation, the stale arm re-reads and finds no advance, and it relinquishes there (`:171-179`), so the third attempt of `DefaultMaxAttempts = 3` (`:52`) is never made. The summary's row said "burns every retry and drives the coordinator to relinquish the lease"; it now states the second-attempt account with both driver citations — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:52, :159-184.

FACT: Every citation the row carries re-verified against the tree. `spec/10_gateway-internals.md:39` is the "If `CoordinatorFence` fails or times out" bullet and orders the retry "with the same generation value (up to 3 attempts with 1-second backoff)". `pkg/adapter/coordination.go:127` tests `gen <= st.coord.lastFenced` and :130-134 returns `Accepted: false` with the `coordinator_handoff_stale` detail. The empty-identifier refusal `spec/05_runtime-registry-and-pool-model.md:515` requires is at `pkg/adapter/coordination.go:109-111`, ahead of the generation comparison — EVIDENCE: spec/10_gateway-internals.md:39; pkg/adapter/coordination.go:107-134; spec/05_runtime-registry-and-pool-model.md:515.

FACT: No deliverable is added, removed, merged, split, or resequenced by this item, so the implementation checklist is untouched and needs no correction. The non-goals at summary.md:77 and spec-changes.md:148-149, which assign the acceptance predicate to proposal 0080 §1.16, are the only other sites naming this defect and both stay true.

WATCHOUT: `tests/tier0_static/claim_register_proto_agreement_test.go:37-41` exempts `CoordinatorFenceRequest` from the unread-fence coverage half on the ground that `pkg/adapter/coordination.go` "compares it on arrival". The exemption turns on the handler comparing the field at all rather than on which comparison it makes, so the standing defect leaves that gate's premise intact even though TEST-1 moves this file's parse call sites. A successor that takes 0080 §1.16 does not disturb it either — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:37-41.


### [f2.other-proposals]

DECISION: Kept the 0073 marker as a row under `## Impacts on other proposals` and widened the row, rather than opening a second human decision — BECAUSE 0073 is Implemented and verified, so this proposal edits the tree rather than that document, `.claude/rules/spec-driven-development.md` forbids amending a landed proposal to track a later reversal, and the retire-or-withdraw question is already OD1, so restating it in the impact table would put one decision in two places — ALTERNATIVES: leaving the row as found (it named the table and the tier-0 gate and stopped, so a reader did not learn that a landed tier-3 comment is deleted and three spec-map registrations are re-pointed); disposing of it as a human decision (duplicates OD1).
FACT: The retired block's anchors hold line for line. `spec/04_system-components.md:151` is the paragraph introducing the declared table, `:153-186` is the table, `:188` grounds the fence's row on the declared classification, and `:190` is the `ShutdownRequest` paragraph SPEC-1 keeps — EVIDENCE: spec/04_system-components.md:149-190.
FACT: The tier-3 membership comment spans `:37-43`, one line later than the item's account of it, and TEST-2 deletes the clauses at `:39-43` and restates membership rather than deleting the whole comment. The row is written to the staged text — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43; the staged non-spec changes, TEST-2.
FACT: The retiring gate's two case names carry three registrations, `tests/spec-map.json:156` and `:169` under section 4.1 and `:5670` under section 28.5.3. Only `:156` was entered by the gate's own landing commit (`fb2af5f9c`, 2026-08-21); `:169` and `:5670` were entered by the two later credit sweeps of the same body of work (`732d8fa7c` and `77b69eaa2`, 2026-08-26). The row says the registrations were made while 0073's work landed rather than attributing all three to 0073's own commit — EVIDENCE: git log -L on tests/spec-map.json:156, :169, :5670.
FACT: The one property the replacement does not reproduce is 0073's §8 checkpoint that a request message added later fails tier 0 until it is classified. The coverage half carries over, because a classification computed from `schemas/lenny-adapter.proto` cannot omit a message — EVIDENCE: proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:4680-4681.
FACT: The row's surviving-limit clause and SPEC-1's statement that 0073's recorded limit "goes with the gate TEST-1 replaces" are about different things and neither is wrong. The comment text at `tests/tier0_static/adapter_proto_message_scope_test.go:25-27` is deleted with the file's gate; the limitation it records persists, because the replacement gate also reads the protocol definition alone — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:25-27.
FACT: No deliverable is added, removed, merged, split, or resequenced by this item. The row is descriptive of what SPEC-1, TEST-1, and TEST-2 already stage, so the implementation checklist is untouched and needs no correction.
WATCHOUT: `## Impacts on other proposals` is the only place this proposal asserts another proposal's continued validity. Nothing of that kind was found elsewhere in the summary to move here: the `Watch out for` paragraph's mention of 0073 states this proposal's own approach (D5) rather than a claim about 0073's validity.


### [f2.cleanup]

DECISION: The summary is left exactly as the adjudication and the write path left it, carrying the eight listed sections in the listed order and nothing else, and no text was moved, relocated, or removed by this pass — BECAUSE every heading, every labelled part under `## Summary`, and the position of `## Deliverable index` already matched the section list, and the three kinds of unlisted content this pass relocates are all absent from the file. — WHERE: `0075_fix_derive-message-scope-from-the-address-type.summary.md`.

FACT: The file's headings are, in order, `# Summary: Derive message scope from the address type`, `## Summary`, `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and `## Deliverable index`. `## Summary` carries no prose of its own and holds `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, and `**Watch out for.**` in that order, so neither rename this pass performs (`**Fixed decisions.**`, `**What is fixed.**`) has a subject. The file contains no `###` heading of any kind, so no retired-decision block survives inside `## Open decisions for human to make`. — EVIDENCE: `grep -n "^#" 0075...summary.md` returns those eight lines and nothing else.

FACT: Nothing unlisted was found to relocate. There is no meta-list of staged items with a proposed disposition, no lead paragraph above `## Summary` (the three that stood there left at [f1.cleanup]), no block of shipped-tree defects outside the section that holds them, no errata list in `**Watch out for.**`, and no prose about another proposal outside `## Impacts on other proposals`. The two remaining mentions of another proposal outside that table state this proposal's own approach and its dependency, which are D5 and D6, and the pointer in the defects entry to proposal 0080 §1.16 names where a remedy is stated rather than asserting that proposal's validity, as [f1.other-proposals] and [f2.other-proposals] both recorded.

FACT: Both open decisions keep the identifier they were stamped with, verbatim and in place. OD1 is the human's and stands as firing 1 rewrote it, with its recommendation, its ground, two losing alternatives, the cost of a "no", and a moderate confidence. OD2 stays the human's: this firing adjudicated it for resolution, the gate refused the resolution, and no answer was applied, so nothing in the staged changes answers it. The staged spec changes §7 still carries "What the retired-field-number column holds" as its one open question, and the staged non-spec changes §4 still states that adding the fence to the map "requires deciding what that column holds". — EVIDENCE: 0075...spec-changes.md:153-155; 0075...non-spec-changes.md:3-8.

FACT: Every section preamble reads true against the entries the section now carries, and no move of this pass could have falsified one, because this pass moved nothing. `## Open decisions for human to make` and `## Impacts on other proposals` carry no preamble. `## Defects in the shipped tree that this proposal does not stage` opens on a lead saying that nothing blocks sign-off, that both specification defects are staged as SPEC-1 and TEST-2, and that one further defect is left where it is; the section carries exactly that one entry, the two staged deliverables are in the staged change files, and the §4.1 ground the lead calls falsified is falsified in the tree, since `Server` carries no coordination field and the generation sits on the slot entry the fence resolves. — EVIDENCE: pkg/adapter/coordination.go:107-134; pkg/adapter/slot.go:59.

FACT: The two edits the firing's write path landed hold against the tree. `spec/10_gateway-internals.md:39` orders the retry "with the same generation value (up to 3 attempts with 1-second backoff)"; `pkg/adapter/coordination.go:127` refuses `gen <= st.coord.lastFenced` and :130-134 returns the `coordinator_handoff_stale` detail; the driver's transient arm retries at the same value at `pkg/gateway/coordination/coordfence/coordfence.go:180-183` and the stale arm relinquishes at `:171-179` against `DefaultMaxAttempts = 3` at `:52`; the empty-identifier refusal is at `pkg/adapter/coordination.go:109-111`. The 0073 row's anchors also hold: `spec/04_system-components.md:151` introduces the declared table, `:175` is the fence's pod row, `:188` grounds it, and `:190` is the `ShutdownRequest` paragraph SPEC-1 keeps.

FACT: `## Deliverable index` is preserved line for line in last position, and no line was added, removed, reworded, or reordered.

WATCHOUT: OD2 carries the question and its ground but no recommendation, no losing alternatives, no cost of deciding otherwise, and no confidence, which is less than the section's contract asks of an entry. That absence is what the loop reached, as [f1.cleanup] recorded, and a format pass may not supply one. The firing that next adjudicates OD2 should either author those parts or record why the loop cannot.

WATCHOUT: The staged TEST-2 restates the tier-3 map's membership as the derivation rule together with the message having carried the retired duplicate (`0075...non-spec-changes.md:39-42`). `CoordinatorFenceRequest` fails the second half of that conjunction, so the restated comment excludes the message TEST-2 adds. That conjunction is OD2's own subject rather than a separate defect, and whatever answers OD2 has to settle the comment as well as the column. Do not file it as an independent correction. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43, :130-141; 0075...non-spec-changes.md:3-8, :39-42.


### [non-spec-recheck.1.fix-G1.1]

DECISION: §4 of non-spec-changes.md now grounds "the fence never carried the retired duplicate" on the message's own declaration plus the retirement commit, and cites `040323634` in place of `01d19af01` — BECAUSE no single commit can carry that premise: `SlotId slot_id` reached the message set across eight commits and the fence's own introduction commit added none of them, so a survey of one commit proves nothing about the fence, while the absence of a `reserved` pair inside the message is checkable in one file read and does not decay — ALTERNATIVES: keeping `01d19af01` with a corrected description (true but no longer bears on the sentence it sits under); swapping in `4f6e49dea` or `d353a8ef3` (same defect under a new hash); enumerating all eight `SlotId slot_id` commits (heavy, and stale on any history rewrite).
FACT: `message CoordinatorFenceRequest` sits at schemas/lenny-adapter.proto:1455-1461, declares `SessionId session_id = 1` and `int64 coordination_generation = 2`, and carries no `reserved` statement of any kind. The retirement commit `040323634` added a `reserved N` / `reserved "slot_id"` pair to every message that had carried the duplicate and does not mention `CoordinatorFence` anywhere in its proto diff — EVIDENCE: schemas/lenny-adapter.proto:1455-1461; `git show 040323634 -- schemas/lenny-adapter.proto`
FACT: `git log --all -S"SlotId slot_id" -- schemas/lenny-adapter.proto` returns, oldest first, 4f6e49dea, 3128fa712, 72880f767, c47b65522, 4003ee848, 3c69e3f35, 01d19af01, 040323634. `git log --all -S"message CoordinatorFenceRequest"` on the same file returns d353a8ef3 alone, and that commit adds no `SlotId slot_id`. Any sentence of the form "the commit that introduced the duplicate address" has no referent on this history — EVIDENCE: schemas/lenny-adapter.proto history
MISTAKE: an earlier round attributed the retired duplicate address to the single commit `01d19af01`, which added the field to the five per-slot request messages and neither introduced the field nor introduced the fence. The wrong attribution was the only stated ground for the premise that the summary's OD2 and spec-changes §7 question 1 both rest on, so whoever settles that decision was reading a false citation as evidence. `01d19af01` no longer appears anywhere in the 0075 directory outside the review log.
WATCHOUT: the same wrong attribution stands verbatim in proposal 0076 (`0076...summary.md:670` and its review log). 0076 is Implemented and immutable, so it is not an edit site; do not "propagate" this correction into it — EVIDENCE: proposals/0076_.../0076_...summary.md:670
FACT: summary.md OD2 (:139-150) and spec-changes.md §7 question 1 (:149-155) both state the underlying fact without citing any commit, and both stay true after this fix. They are anchored on `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141` and `schemas/lenny-adapter.proto:1455-1461`, which both resolve. No cascade edit was needed — EVIDENCE: 0075...summary.md:139-150; 0075...spec-changes.md:149-155



### [non-spec-recheck.1.fix-design-G1.1]
DECISION: replace the whole `01d19af01` clause in non-spec-changes.md:5-6 with tree-state ground (the fence declares no `reserved` number and no `reserved "slot_id"`) plus the retirement commit `040323634` leaving the fence untouched — BECAUSE no single commit can establish "the fence never carried the duplicate": `SlotId slot_id` entered the proto across eight commits and the fence's own introduction commit added none of them — ALTERNATIVES: keeping `01d19af01` with a corrected description ("the commit that put the field on the five per-slot request messages") — rejected, it is then a citation that no longer bears on the claim it sits under; swapping the hash for `4f6e49dea` or `d353a8ef3` — rejected, same defect in a new hash.
FACT: `SlotId slot_id` was added to schemas/lenny-adapter.proto by eight commits, oldest first 4f6e49dea, 3128fa712, 72880f767, c47b65522, 4003ee848, 3c69e3f35, 01d19af01, 040323634. None of them touches `CoordinatorFenceRequest` (`git show <c> -- schemas/lenny-adapter.proto | grep -c CoordinatorFence` returns 0 for each). The fence was introduced by d353a8ef3 (2026-05-27), which added no `SlotId slot_id` at all. EVIDENCE: schemas/lenny-adapter.proto:1455-1461
FACT: the retirement commit 040323634 is what added the `reserved N; reserved "slot_id";` pairs, 44 `reserved` lines stand in the file today, and the fence has none. That absence, plus the retirement diff not naming CoordinatorFence, is the ground that actually establishes the premise section 7's open question and the summary's OD2 rest on. EVIDENCE: schemas/lenny-adapter.proto:464-465, 1455-1461
WATCHOUT: the same false attribution stands verbatim in proposal 0076 (summary.md:670, review-log.md:3684). 0076 is Implemented and immutable; do not edit it, and do not list it as a touched file. EVIDENCE: proposals/0076_scope-the-coordination-generation-to-the-session/
FACT: `01d19af01` occurs exactly once in the 0075 directory, at non-spec-changes.md:6. summary.md OD2 (:139-150) and spec-changes.md §7 (:149-155) restate the underlying fact without citing any commit, so they stay true and need no edit. EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:6


### [non-spec-recheck.1.review-applicability.1]

DECISION: Filed one finding, on `non-spec-changes.md:5-6`'s attribution of the retired duplicate address to commit `01d19af01` — BECAUSE that commit did not introduce `SlotId slot_id` (4f6e49dea did, on `SendMessageRequest`, and the field reached the other `sessionScopedMessages` members across at least six further commits), nor did it introduce `CoordinatorFenceRequest` (d353a8ef3 did), so under either reading of "the commit that introduced it" the sentence is false and the one-commit survey does not establish the premise it is offered for — ALTERNATIVES: leaving it as locator imprecision, rejected because the conclusion's only stated evidence is the false clause, and because a fixer needs the right ground (the fence declares no `reserved` block at all).

FACT: `SlotId slot_id` entered `schemas/lenny-adapter.proto` in 4f6e49dea and was added message by message across 3128fa712, 72880f767, c47b65522, 4003ee848, 3c69e3f35, and 01d19af01, then retired wholesale in 040323634. `git log --all -S"SlotId slot_id" -- schemas/lenny-adapter.proto` is the one-line derivation. No revision of the file ever declared `slot_id` inside `CoordinatorFenceRequest`, which is why it carries no `reserved` block — EVIDENCE: schemas/lenny-adapter.proto:1455-1461.

FACT (closes [spec.4 UNVERIFIED at review-log.md:1431]): OD2's claim holds. `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates `sessionScopedMessages` and asserts BOTH `reservesNumber(md, num)` at :135-137 AND `reservesName(md, retiredFieldName)` at :138-140, with `retiredFieldName = "slot_id"` at :32. `CoordinatorFenceRequest` reserves neither, so adding it to that map turns tier 3 red whatever number the column holds. I did NOT file it: OD2 records exactly this as an open human decision, and the rubric puts open-decision sections outside the review — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:32, :130-141; schemas/lenny-adapter.proto:1455-1461.

FACT: The replacement gate D1/D2 states PASSES on the proto as it stands, so S2 does not add a gate its own tree fails. Zero fields named `session_id` carry a type other than `SessionId` and zero `SessionId`-typed fields carry another name (26 occurrences, all `SessionId session_id`); `CheckpointRequest` and `CheckpointResponse` are the only messages declaring a `oneof` and only the first is an RPC request type; `CheckpointRequest` declares no top-level address (only `int64 coordination_generation = 4` outside the oneof) and exactly one of its three frames declares one — EVIDENCE: schemas/lenny-adapter.proto:1173-1187, :1217, :596.

FACT: S1's deletion of ~37 lines from `spec/04_system-components.md` breaks no gate other than the one S2 replaces. `tests/registers/line-citations.yaml:15` and `line-citation-resolution.yaml` are both `files: []`, so no in-domain file carries a line citation to shift; the successor-pointer gate's domain is a NAMED list of three reducing sections (§4.7, §15.4, §29.10) and §4.1 is not in it, so retiring the table owes no successor pointer; no test reads §4.1 by heading (`grep '"### 4\.1\|"#### Request' tests/ scripts/ cmd/` is empty); the only registrations of the retiring gate's two case names anywhere in the tree are `tests/spec-map.json:156`, `:169`, `:5670`, all three already staged — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:52-56; tests/registers/line-citations.yaml:15.

FACT: `protoServiceRequests` (and `protoServiceOpen`, `protoRPC`) has exactly ONE caller in the tree, `messageScopeDisagreements` at `adapter_proto_message_scope_test.go:87`. If the replacement gate scopes its envelope clause to REQUEST messages, as D1's text does ("A request message that carries its frames in a `oneof`"), it keeps that caller alive. A gate written over all messages instead would strand the parse function and drag `CheckpointResponse` into the envelope clause — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:68; tests/tier0_static/adapter_proto_message_scope_test.go:87.

WATCHOUT: The CACHE key in this loop's prompt (`applicability-r1-$H.json`) COLLIDES with the spec-lane loop's key for the same three-file hash. The cached r1 answer at that path was written by the spec-recheck lane at 11:00 and its own coverage text says it deliberately did not verify the non-spec staging, which is this lane's whole scope, and an `applicability-r2` for the same hash exists, so it was not an interrupted run of this question. I did the review rather than returning it. A future agent hitting a cache line whose `coverage` disclaims its own scope should do the same — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/applicability-r1-4e0efd1c4849.json.

FACT: The checklist is clean under the CHECKLIST rules. Three deliverables, three steps, one lane each (S1 spec, S2 test, S3 test), spec step leading, no Depends-on naming a later or nonexistent step, no box ticked. S1's tier-0 redness between S1 and S2 IS disposed of on the step's own line, so it is not a class-4 gate-state finding. S3 omitting tier 0 is not a finding either: `go vet ./...` and the tier-0 `go test ./tests/tier0_static/...` do not compile `//go:build contract` files, and golangci-lint findings are non-fatal in this harness — EVIDENCE: cmd/lenny-test/cmd_run.go:598-615, :735-737.

USEFUL [spec.1.fix-design-G2.1]: its enumeration of the spec-map registrations and of `slotAddressCaseFiles` naming the gate file BY PATH (`spec_map_slot_address_registration_test.go:336`) is exact and saved me the sweep; the replacement gate keeping that path is what keeps that register out of the blast radius.


### [non-spec-recheck.1.review-citations.1]
DECISION: Returned the cached citation-lens answer verbatim (empty findings) instead of re-reviewing — BECAUSE the cache probe at /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/citations-r1-4e0efd1c4849.json hit, and the harness instruction for a cache hit is explicit and first ("return exactly it and do no other work"). ALTERNATIVES: re-running the whole audit (rejected: same lens, same round, byte-identical staged text, so the answer is the same); partially re-auditing only the delta (rejected: "do no other work" admits no partial form).
FACT: the cache key is `md5(spec-changes.md + non-spec-changes.md + implementation-checklist.md)`, with NO lane component in the filename (`citations-r1-$H.json`). So a spec-lane run and a non-spec-lane run of the citations lens at round 1 share a cache slot whenever those three files are unchanged. That is what happened here: the hit was written by the spec-recheck lane's run against snapshot `spec-recheck-r3`. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/citations-r1-4e0efd1c4849.json
WATCHOUT: `summary.md` is IN this lane's stated review scope but is NOT in the cache key. Its mtime (12:00) is later than spec-changes.md (10:18) and non-spec-changes.md (09:30), so summary.md is the only staged input that moved since the cached run, and the cached coverage note shows that run read summary.md only for 0076/0080 cross-references. A citation defect introduced into summary.md after 10:18 would be invisible to this cache hit. — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type/ mtimes: summary.md 12:00, spec-changes.md 10:18, non-spec-changes.md 09:30, implementation-checklist.md 09:29
UNVERIFIED: no agent in this lane has citation-audited the post-10:18 edits to 0075's summary.md. If the loop wants that covered, it must either add summary.md to the cache key or run the citations lens with the cache bypassed. Whoever owns the harness config should decide.


### [non-spec-recheck.1.review-client-surface.1]

DECISION: Returned the cached client-surface answer (empty findings) rather than re-running the sweep — BECAUSE the cache key over spec-changes.md + non-spec-changes.md + implementation-checklist.md hashed to `4e0efd1c4849` and a completed answer for that exact hash was present at scratchpad/cp-cache/0075_.../client-surface-r1-4e0efd1c4849.json; I confirmed the answer was not truncated by finding its one derived DEFERRED already merged into the review log at line 1469 — ALTERNATIVES: re-running the full sweep (the three hashed files are byte-identical to the cached run's input, so it would re-derive the same census).

FACT: The delta since the cached run touches only `review-log.md` and one row plus one paragraph in `summary.md`. Neither file carries an externally-consumed contract: the summary hunks are the 0073 impacts row (widened with tier-3 comment and spec-map citations) and a correction to the equal-generation re-fence consequence sentence, both proposal bookkeeping. The client-surface lens has no purchase on either. — EVIDENCE: diff of scratchpad/cp-snap/0075_.../spec-recheck-r3 against the proposal directory returns hunks only in those two files.

USEFUL [spec.1.review-docs-alignment.1]: its FACT that `docs/` names RPCs and never request message types is the load-bearing reason this lens keeps returning empty; it is why no docs/, charts/, or schemas/*.json surface moves when §4.1's message-scope table retires. Confirmed again here through the cached run's own coverage note.

WATCHOUT: The cache key does NOT include `summary.md` or `review-log.md`. A future firing whose only delta is in those two files will get a cache hit and skip the sweep. That is correct for this lens, because no client-facing contract lives in either, but a lens that owns proposal bookkeeping (citations, cleanup) must not treat a hit on this key as covering a summary rewrite. — EVIDENCE: the CACHE command in the firing prompt hashes only the spec-changes, non-spec-changes, and implementation-checklist files.


### [non-spec-recheck.1.review-docs-alignment.1]
DECISION: Returned the cached docs-alignment answer verbatim (empty findings) — BECAUSE the cache key `docs-alignment-r1-4e0efd1c4849.json` hit on the current staging triple, and the run directive says to return it and do no other work — ALTERNATIVES: re-running the lens, rejected because the hashed text (spec-changes + non-spec-changes + implementation-checklist) is byte-identical to what the cached run reviewed.
FACT: the docs-alignment cache key is an md5 over spec-changes.md + non-spec-changes.md + implementation-checklist.md ONLY. summary.md and review-log.md are outside the key, so a round whose delta is summary-only will hit a cache written before that delta and no agent will have read the new summary text under this lens. — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md (mtime 12:00) vs non-spec-changes.md (09:30) and the cache file /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/docs-alignment-r1-4e0efd1c4849.json
UNVERIFIED: the summary.md edits made at 12:00 (the open-decisions-and-impact-review phase's output) have not been read under the docs-alignment lens by any agent; a later round or the compaction pass should confirm the impacts rows and decision entries carry no false docs/ citation. — a docs-alignment reviewer on the next non-cached round should check it.



### [non-spec-recheck.1.review-edit-sites.1]
FACT: The edit-sites cache key (md5 of spec-changes + non-spec-changes + implementation-checklist) HIT on this run, so `non-spec-changes.md` and the checklist are byte-identical to the spec-recheck-r3 snapshot; the entire delta this loop was convened for lives in `summary.md`, which the cache key does not cover — EVIDENCE: scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/edit-sites-r1-4e0efd1c4849.json
WATCHOUT: a future cache hit on this proposal is NOT proof the summary was reviewed. Diff `summary.md` against the snapshot before trusting an empty cached answer — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-r3/0075_fix_derive-message-scope-from-the-address-type.summary.md
FACT: The summary delta is two blocks and both verify against the tree. (1) The refusal note's new coordfence account: the transient/default arm that retries at the unchanged generation is `pkg/gateway/coordination/coordfence/coordfence.go:180-183`, the stale arm that re-reads and relinquishes on no advance is `:171-179`, and `DefaultMaxAttempts = 3` at `:52`, so "gives up on the second of its three attempts" holds. (2) The rewritten 0073 impacts row's citations all resolve: `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43` is the `sessionScopedMessages` doc comment, whose table-keyed clause and pod-scoped `CoordinatorFenceRequest` naming run `:39-43`; `tests/spec-map.json:156` and `:169` are the two retiring gate case names under spec/04, `:5670` is the same gate under the 28.5.3 block; `tests/tier0_static/adapter_proto_message_scope_test.go:25-27` is the sentence recording that the gate reads spec and proto text alone — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:52,171-183
DECISION: returned the cached empty findings list verbatim — BECAUSE the cache instruction is unconditional on a hit and my independent check of the uncovered summary delta surfaced nothing meeting the bar — ALTERNATIVES: re-running the full edit-site sweep over unchanged text, rejected as duplicated work the cache exists to prevent


### [non-spec-recheck.1.review-mechanism.1]

DECISION: Returned the cached mechanism-lens result unchanged (empty findings) — BECAUSE the cache probe printed JSON at scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r1-4e0efd1c4849.json, and the loop's cache rule is unconditional: a hit is returned verbatim with no other work — ALTERNATIVES: re-running the full trace, rejected because the hash over spec-changes + non-spec-changes + implementation-checklist is identical, so the text under this lens has not moved since that run.

FACT: The mechanism lens has already traced this staging end to end and returned nothing. Its coverage record names the surfaces it verified: spec/04_system-components.md:149-190 plus :712/:725/:726, spec/10_gateway-internals.md:57-60, spec/05_runtime-registry-and-pool-model.md:515, spec/28_communication-channels.md:314-319 and :804-850, a full re-parse of both service blocks in schemas/lenny-adapter.proto, pkg/adapter/coordination.go:17-38 and :107-119, tests/tier0_static/adapter_proto_message_scope_test.go, tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:35-76, and docs/reference/adapter-contract.md:78-82 — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/mechanism-r1-4e0efd1c4849.json

WATCHOUT: The cache key covers only spec-changes.md, non-spec-changes.md, and implementation-checklist.md. summary.md (mtime 12:00) is inside this loop's stated scope but outside the key, so a summary-only edit does NOT invalidate the cache and this lens will keep returning the hit. A reviewer who needs the summary re-read under this lens must bypass the cache deliberately — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md

UNVERIFIED: Whether the replacement tier-0 gate's parse can be built from the shared parse helper in tests/tier0_static/adapter_proto_parse_test.go:10-70. The cached run recorded this as undecidable under the mechanism lens because the remedy is a test-file change this loop may not edit; the implementor should settle it when writing the gate.


### [non-spec-recheck.1.review-operational.1]
FACT: This run was a cache hit. The operational lens cache key hashes only spec-changes.md + non-spec-changes.md + implementation-checklist.md, and those three were byte-identical to the earlier interrupted run, so the stored answer (empty findings) was returned verbatim. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/operational-r1-4e0efd1c4849.json
WATCHOUT: the cache key excludes summary.md and review-log.md. The orchestrator for this recheck named summary.md as part of the changed staging, but a summary-only edit cannot change the key, so a lens whose finding would live in summary.md will be served a stale cache hit. Anyone who needs summary.md re-read under this lens must invalidate the cache file by hand. EVIDENCE: the cache command in the task prompt hashes exactly three files; /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md (mtime 12:00) is newer than non-spec-changes.md (09:30).


### [non-spec-recheck.1.review-performance.1]
FACT: The performance lens has a live cache hit for this staging. Key `performance-r1-4e0efd1c4849.json` under scratchpad/cp-cache/0075_.../ was written by an earlier interrupted run over byte-identical spec-changes + non-spec-changes + checklist, and returned an empty findings list. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/performance-r1-4e0efd1c4849.json
FACT: The cached coverage records the substantive reason the lens is empty here and it still holds if a future round re-derives it: this proposal creates no control-plane or data-plane write at all. It retires a spec table, states a derivation rule, replaces one tier-0 static gate, and repairs one tier-3 suite. No CRD status write, no Postgres or Redis key, no informer or watch, no reconcile trigger, and no change to any RPC payload, frequency, or fan-out, so there is no write-rate arithmetic to run against the section 12 tier sizing or the etcd dedup ceiling. — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.non-spec-changes.md:1
FACT: The only failure-mode surface the staging touches is coordinator handoff and hold state, and it does not move it. The classification the staging records (CoordinatorFenceRequest session-scoped) is already the tree's behavior, and the hold plus its gauge stay pod-scoped. — EVIDENCE: pkg/adapter/coordination.go:107-119 (resolves the bound slot entry by identifier, InvalidArgument on empty at :109-111); spec/10_gateway-internals.md:57 and :60
WATCHOUT: The cache key is the md5 of the three staged files concatenated. Any edit to the checklist, the spec staging, or the non-spec staging invalidates it and the lens must actually re-run; do not assume a stale key means the answer changed materially, and do not hand-edit the cache. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/


### [non-spec-recheck.1.review-reliability.1]
FACT: This run was a cache hit and returned a prior interrupted run's answer verbatim (empty findings). — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/reliability-r1-4e0efd1c4849.json
WATCHOUT: The reliability cache key is md5 over spec-changes.md + non-spec-changes.md + implementation-checklist.md only; summary.md is NOT in the key. A round whose delta lands solely in summary.md will hit a stale cache entry and skip the review. — EVIDENCE: /home/ec2-user/lenny/proposals/0075_fix_derive-message-scope-from-the-address-type/0075_fix_derive-message-scope-from-the-address-type.summary.md
UNVERIFIED: whether summary.md changed since the cached run; a later reliability pass should diff summary.md against the r3 snapshot at /home/ec2-user/lenny/scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/spec-recheck-r3 before trusting an empty reliability list.


### [non-spec-recheck.1.review-security.1]

FACT: The security lens returned a cache hit on this staging and did no fresh work. The cache key is md5 of spec-changes.md + non-spec-changes.md + implementation-checklist.md concatenated, first 12 chars; it resolved to 4e0efd1c4849 and the stored answer was an empty findings list. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/security-r1-4e0efd1c4849.json

WATCHOUT: The cache key does NOT include the summary. If a round changes only the summary (as the non-spec-recheck delta partly did), the security lens will hit its cache and never read the new summary text. A summary-only defect under this lens is therefore invisible to a cached run. EVIDENCE: the orchestrator's cache command hashes exactly three files, and summary.md (modified 09-07 12:00) is not one of them.

FACT: The cached security review verified and found clean, on the same text: CoordinatorFenceRequest's reclassification to session-scoped pulls it under the fail-closed empty-identifier refusal at spec/05_runtime-registry-and-pool-model.md:515, enforced at pkg/adapter/coordination.go:108-111, so the change is fail-closed at that site. The derivation's authoritative input is schemas/lenny-adapter.proto, a review-controlled repo artifact rather than an in-pod self-report, so no security bound is re-sourced. No RBAC, NetworkPolicy, webhook, ServiceAccount, tenant-pinning, or reuse-counter surface is touched. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515; pkg/adapter/coordination.go:108-111

OPEN: Whether the pod-wide coordinator hold's release semantics on a multi-slot pod are owned by 0076 or by 0080. Outside the staged spec edits here; recorded rather than filed. EVIDENCE: spec/10_agent-runtime-contract.md:57, :60


### [non-spec-recheck.1.review-test-coverage.1]

DECISION: Returned an EMPTY findings list for the test-coverage lens on the non-spec staging — BECAUSE §8 now names four enumerated tier-0 negative cases (one per gate clause, including the zero/two boundary on the envelope-frame clause) plus the tier-3 constraint, the tiers the change reaches (0 and 3) are both listed, and every behavioral obligation the reclassification imports already has a landed test — ALTERNATIVES: filing the tier-11 entry as a marker with no constraint (rejected: no tier-11 gate reads §4.1's table or the `#### Request Message Scope` heading, so tier 11 is a regression run rather than a case obligation); filing "no test pins that the derivation reproduces the retired table's 32 rows" (rejected: that is OD1's own subject, settled outside review).

FACT: The one behavioral obligation the pod↔session reclassification imports — refusal of a session-scoped request with an empty identifier (`spec/05_runtime-registry-and-pool-model.md:515`) — is both implemented and already pinned. Handler at `pkg/adapter/coordination.go:109-111`; test `TestCoordinatorFenceRejectsMissingSessionID` at `pkg/adapter/coordination_test.go:35-43`. 0076 also landed the per-session fence cases (`TestCoTenantFenceRecordsPerSessionGeneration_spec_10_1_2` at `:505`, `TestCoTenantFirstFenceIsNotAGap_spec_10_1_2` at `:553`, `TestPerSessionReadsAreEmptyForAnUnheldSession_spec_10_1_2` at `:693`). Do not file a missing-test finding for the reclassification. — EVIDENCE: pkg/adapter/coordination_test.go:35,505,553,693

FACT: The replacement gate's positive case is satisfiable against the shipped proto. All 26 field declarations of type `SessionId` are spelled `SessionId session_id` and no field named `session_id` carries another type, so D2's two convention clauses are green on the tree today. — EVIDENCE: `grep -n "SessionId " schemas/lenny-adapter.proto` returns 26 field lines plus the message at :595 and one comment; the reverse grep for a `session_id` of another type returns nothing.

FACT: `CheckpointResponse` (`schemas/lenny-adapter.proto:1249-1257`) carries a `oneof msg` whose five arms declare no address at all. If an implementor keys the envelope clause on "message declares a `oneof`" rather than on "request message declares a `oneof`", the gate goes red on the shipped proto immediately, so the mistake is self-catching and needs no separate listed case. §4 of the staged spec changes states that boundary (`spec-changes.md:84-87`). — EVIDENCE: schemas/lenny-adapter.proto:1249-1257

FACT: `tests/tier0_static/spec_map_slot_address_registration_test.go` carries a hard-coded file inventory `slotAddressCaseFiles` that already lists `tests/tier0_static/adapter_proto_message_scope_test.go` (`:336`), `adapter_proto_parse_test.go` (`:337`), and the tier-3 suite (`:409`). `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970`) requires a spec-map credit for every section a case's own `// spec:` annotation names, and `creditsMissing` (`:953`) catches only MISSING credits, never extra ones. So TEST-1's "registered under the same sections" is satisfiable whichever way the annotation goes, and the inventory needs no edit as long as the replacement gate keeps the same file path. An implementor who moves the gate to a new file path must add that path at `:336`. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336-337,409,953,970

USEFUL [spec-recheck.2.review-feasibility.1 / review-log:780]: its DEFERRED that §8 required a single negative case against a four-clause gate was closed by the non-spec staging; `non-spec-changes.md:48-52` now enumerates one case per clause with the zero/two boundary. Nothing left to file there.

WATCHOUT: `review-log:1538` deferred to this loop whether the replacement gate's cases can honestly annotate `// spec: 28.5.3`, since §28.5.3 is the Intra-pod boundary (`spec/28_communication-channels.md:499`) and the gate reads a gateway↔adapter gRPC convention. I did not file it: the retiring gate already carries that annotation (`tests/tier0_static/adapter_proto_message_scope_test.go:129`) and the credit gate never refuses a surplus credit, so nothing breaks either way. It is a registration-accuracy judgment for the implementor, not a coverage gap.

WATCHOUT: adding `CoordinatorFenceRequest` to `sessionScopedMessages` drags it into THREE arms, not one, and the third (`TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4`, `:127-142`) asserts both `reservesNumber` and `reservesName("slot_id")`, neither of which the fence declares. That is OD2's subject and is settled outside review; do not file it as a testing gap. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:81,102,130-141; schemas/lenny-adapter.proto:1455-1461


### [non-spec-recheck.2.review-applicability.1]

DECISION: Returned an EMPTY findings list. — BECAUSE the only text that moved since the last applicability
pass in this lane is one paragraph (non-spec-changes.md §4, TEST-2's ground for the fence's empty
retired-field column), and every claim in it verifies exactly against the tree; the rest of the staging,
the checklist, and the summary re-verified clean under classes 1-6 and the CHECKLIST rules. —
ALTERNATIVES: filing the TEST-2-versus-OD2 contradiction (rejected, see below); filing the replacement
gate's unstated test-function names as an underspecified target (rejected: both carriers, the Go file and
`tests/spec-map.json`, are written inside the same step S2, so nothing cross-step depends on a name the
proposal was responsible for fixing, and the lens bars reporting ordinary naming judgment).

FACT: The whole delta since the last non-spec applicability review is `non-spec-changes.md:5-9`. `diff -u`
against `scratchpad/cp-snap/.../non-spec-recheck-r1-prefix` shows one hunk; `summary.md`,
`spec-changes.md`, `implementation-checklist.md`, `problem-statement.md`, and `status.md` are byte-identical
to `non-spec-recheck-r1-start`. Snapshot `non-spec-recheck-r2` is identical to the live directory, so
diffing against it shows nothing; use `non-spec-recheck-r1-prefix` or `-r1-start` to see the delta.

FACT: The new §4 paragraph verifies in full. `CoordinatorFenceRequest` occupies exactly
`schemas/lenny-adapter.proto:1455-1461` and declares `SessionId session_id = 1` (`:1456`) and
`int64 coordination_generation = 2` (`:1460`) with no `reserved`. Commit `040323634` ("Address a session on
the gRPC leg by its session identifier alone") added exactly 18 `reserved "slot_id"` lines, the file carries
exactly 18 today, `git log -S'reserved "slot_id"' -- schemas/lenny-adapter.proto` returns that one commit,
and `git show 040323634 -- schemas/lenny-adapter.proto | grep CoordinatorFence` is empty. The 18 match the
18 members of `sessionScopedMessages`. — EVIDENCE: schemas/lenny-adapter.proto:1455-1461;
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-63

WATCHOUT: The staged TEST-2 ("add `CoordinatorFenceRequest` to `sessionScopedMessages`, settling the
retired-field-number column per §4") is unappliable as written: `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4`
iterates that same map and asserts `reservesName(md, "slot_id")` at `:138-140`, which the fence fails for
any column value, so S3 turns tier 3 red. This is NOT a fileable finding: it is summary OD2's own subject,
recorded there as an open human decision with the two candidate resolutions, and at least three earlier
agents reached it and declined to file for the same reason (review-log.md:920, :1290, :2030, :2133). Do not
re-derive it a fifth time. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:32,130-141;
proposals/0075_.../0075_....summary.md:139-150

FACT: Every anchor SPEC-1 depends on resolves at HEAD, unchanged since the last pass:
`#### Request Message Scope` at spec/04_system-components.md:149, the introducing paragraph at `:151`, the
32-row table at `:153-186` (the fence's row at `:175`), the grounding paragraph at `:188`, and the
`ShutdownRequest` paragraph at `:190`. §4.7.1's two per-message scope sentences are `:725` ("The request is
session-scoped: it is addressed by the identifier of the released session and names no slot.") and `:726`
("The request is pod-scoped."), both outside SPEC-1's block and both agreeing with the derivation.

FACT: The replacement gate passes on the shipped proto, so S2 adds no gate its own tree fails. All 26
`session_id` field declarations are `SessionId session_id`; no `SessionId`-typed field carries another
name; the only two `oneof`s are `CheckpointRequest` (`:1174`) and `CheckpointResponse` (`:1250`), the second
being no RPC's request type; `CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4`
(`:1186`) and exactly one of its three frames, `CheckpointStart`, declares `SessionId session_id = 7`
(`:1217`), with `CheckpointGrant` and `CheckpointAbort` declaring none (`:1223`, `:1243-1245`).

FACT: `TestDocumentConsistencyGatesCarryNoSpecSectionCredit` does NOT reach the replacement gate. Its
subject register `documentConsistencyGates` is a two-entry hard-coded literal naming
`tests/tier11_docs/test_gaps_test_reference_rename_drift_test.go` and this file itself; it is not derived
from a predicate over "reads a document", so a gate that after TEST-1 reads only
`schemas/lenny-adapter.proto` does not fall into it and keeps its spec-map credits. I checked this because
it looked like a live class-4 gate-state hazard; it is not. — EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:124-136

FACT: `tests/registers/identifier-senses.yaml` carries eight positional entries keyed on
`spec/04_system-components.md` by occurrence index, so a deletion inside that file could in principle shift
them. It cannot here: none of CH-RUNTIMEOPS's retired spellings (`LifecycleChannel`, `lifecycleChannel`,
`lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`, `lifecyclechannel`, from
spec/28_communication-channels.md:154-159) occurs anywhere in spec/04, so the block SPEC-1 deletes contains
no site and the indices do not move. — EVIDENCE: tests/registers/identifier-senses.yaml:14-37;
spec/28_communication-channels.md:154-159

USEFUL [non-spec-recheck.1.review-applicability.1]: its FACTs on S1's blast radius (no line-citation
register entry, no successor-pointer obligation, no test reading §4.1 by heading, the three spec-map
registrations being the only ones anywhere) and on `protoServiceRequests` having exactly one caller are
exact and saved the whole sweep. I re-derived the spec-map and caller halves independently and both hold.

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its note that `slotAddressCaseFiles` names the gate file
BY PATH at `:336` is the reason TEST-1 owes that register no edit, and it is the one thing that would change
if an implementor moved the replacement gate to a new file path. Worth carrying forward.


### [non-spec-recheck.3.review-applicability.1]

DECISION: Returned an EMPTY findings list. — BECAUSE the staged text did not move at all since the last
applicability pass in this lane, and I re-derived the whole lens from the tree rather than trusting the
prior pass: every anchor SPEC-1, TEST-1, and TEST-2 depend on resolves at HEAD, the created-artifact
worklist has no property an implementor must invent that the proposal was responsible for stating, no
sub-step references an artifact a later sub-step creates, no existing gate outside the one TEST-1 replaces
reads the retired block, and the checklist is clean under the CHECKLIST rules (three deliverables, three
steps, one lane each, spec step leading, no Depends-on naming a later or nonexistent step, no ticked box).
— ALTERNATIVES: filing S3's tier list omitting tier 0 (rejected, see CORRECTS below: tier 0 does compile
the contract-tagged file, but tier 3 — which S3 does list — compiles it too, so the omission has no
consequence); filing TEST-2's restated membership rule as self-contradictory (rejected, see WATCHOUT).

FACT: The proposal directory is byte-identical to the `non-spec-recheck-r3-start` snapshot, and identical
to `non-spec-recheck-r2` except for `review-log.md`. The orchestrator's note that "this lane's staging
changed after this lane's last review converged" is FALSE for this firing: `summary.md`,
`non-spec-changes.md`, `spec-changes.md`, `implementation-checklist.md`, `problem-statement.md`,
`status.md`, and `deviations.md` all match r2. Verify with
`diff -rq scratchpad/cp-snap/0075_.../non-spec-recheck-r2 proposals/0075_...` before spending a pass
hunting a delta that is not there. — EVIDENCE: `diff -rq` output, r2 vs live

CORRECTS [non-spec-recheck.2.review-applicability.1, its FACT at review-log.md:2040]: that entry rules out
S3-omits-tier-0 partly on "`go vet ./...` and the tier-0 `go test ./tests/tier0_static/...` do not compile
`//go:build contract` files". Tier 0 DOES compile them: `runStaticTier` runs
`go vet -tags=contract ./tests/tier3_contract/...` as its second check, commented "Verify contract-tagged
tests compile without running them." The conclusion still stands, but on a different ground — tier 3, which
S3 does list, compiles the same package — so do not reuse the stated rationale.
— EVIDENCE: cmd/lenny-test/cmd_run.go:503-509

WATCHOUT: TEST-2 instructs both "add `CoordinatorFenceRequest` to `sessionScopedMessages`" AND "restating
membership as the derivation rule together with the message having carried the retired duplicate"
(non-spec-changes.md:39-43), while the same file states the fence never carried that duplicate
(non-spec-changes.md:5-9). The added member fails the restated membership rule. This is NOT filable: it is
the same subject as summary OD2 ("How does `CoordinatorFenceRequest` enter the tier-3
`sessionScopedMessages` map?", summary.md:139-150), whose two candidate resolutions would both rewrite
TEST-2's map instruction, and the brief bars filing on how an open decision is framed. Five agents have now
reached this and declined. Do not derive it a sixth time. — EVIDENCE:
proposals/0075_.../0075_....non-spec-changes.md:5-9,39-43; summary.md:139-150

FACT: Independently re-derived and confirms [non-spec-recheck.1/2] on the blast radius, so it can be
trusted: `tests/change-graph.json` names none of the touched test files; `tests/spec-map-exceptions.yaml`
and `tests/claim-map.json` carry neither retiring case name; `tests/registers/*.yaml` reference
`spec/04_system-components.md` only in `identifier-senses.yaml` (eight CH-RUNTIMEOPS occurrence entries),
and `sed -n '149,191p' spec/04_system-components.md | grep -i lifecycle` is empty, so the deleted block
holds no retired spelling and the occurrence indices do not shift. `gate_integrity_test.go`'s hard-coded
tier-0 gate roster does not name the message-scope gate, so replacing it trips no meta-gate.
— EVIDENCE: tests/registers/identifier-senses.yaml:14-37; tests/tier0_static/gate_integrity_test.go:46-50

FACT: The replacement gate passes on the shipped proto, re-derived today: `grep -c "SessionId session_id ="`
returns 26 and `grep -c " session_id *="` also returns 26, so name and type agree in both directions with no
exception; the only two `oneof`s are at `schemas/lenny-adapter.proto:1174` (`CheckpointRequest`) and `:1250`
(`CheckpointResponse`, no RPC's request type). So S2 adds no gate its own tree fails.
— EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

FACT: `runValidateMaps` really does carry a dangling-test-function check, so TEST-1's `validate-maps`
sentence is not aspirational: `validateSpecMapTestFuncs` sits in the check list beside
`validateSpecMapTestFiles` and `validateTestFilesMapped`. — EVIDENCE: cmd/lenny-test/cmd_validate.go:64-67


### [non-spec-recheck.3.review-citations.1]

DECISION: Returned an EMPTY findings list after a full, uncached audit of every citation in all five staged files — BECAUSE every file:line anchor, every quoted spec sentence, and every attributed behavior resolves at HEAD and says what the proposal claims; I re-derived the proto measurements with my own parser rather than trusting the review log — ALTERNATIVES: filing the two loose-attribution sites noted below (rejected: neither changes a conclusion, and the rubric refutes wording imprecision).

FACT: The staged text is BYTE-IDENTICAL to snapshot `non-spec-recheck-r2` except for `review-log.md`; the only staging delta since `non-spec-recheck-r1-start` is one paragraph, `non-spec-changes.md:3-9` (TEST-2's ground for the fence's empty retired-field column). `diff -ru` against the `non-spec-recheck-r3` snapshot returns NOTHING, so use `-r1-start` or `-r1-prefix` to see any delta in this lane. — EVIDENCE: diff -rq of scratchpad/cp-snap/0075_.../non-spec-recheck-r2 against the proposal directory

FACT: Every citation in the staging verified at HEAD. Full list, all confirmed: spec/04:149 (`#### Request Message Scope`), :151, :153-186 (32 rows: 26 session, 6 pod), :175, :188, :190, :725, :726; spec/05:515; spec/10:38, :39, :40, :57, :60; spec/28:314-317; schemas/lenny-adapter.proto:458, :499-503, :596, :1173, :1217, :1455-1461; pkg/adapter/server.go:302, slot.go:59, coordination.go:17-38/:107/:109-111/:116/:127-134, checkpoint.go:74-84; coordfence.go:52/:171-179/:180-183; tier0 gate :17-27/:25-27/:54/:75-81; adapter_proto_parse_test.go:10-15/:64-67; claim_register_proto_agreement_test.go:64; spec-map.json:156/:169 (section 4.1 opens :152) and :5670 (section 28.5.3 opens :5643); session_address_wire_test.go:37-43/:39-43/:40-43/:81/:102/:130/:130-141/:150-158; tier11 doc gate :43-76; docs/reference/adapter-contract.md:81; commit 040323634. NO citation in this proposal is stale as of 2026-09-07.

FACT: `pkg/adapter/server.go:302` is exact in both directions. Commit `4c8e48134` ("Record the coordination generation and the barrier gate per bound session") removed `coord coordinationState`, and in its parent that field sat at LINE 302 while `hold holdState` sat at :307. §1.2's phrase "declares `hold holdState` where the pod-wide state stood" is literally true of the line number, not just of the file. — EVIDENCE: `git show 4c8e48134^:pkg/adapter/server.go | grep -n` → 302 coord, 307 hold; pkg/adapter/server.go:302 today

FACT: The proto measurements re-derived independently (own Python parse of the two service blocks, not the review log's numbers): 31 RPC request types; 25 declare a top-level `session_id`; the 6 that do not are exactly `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`; ZERO fields named `session_id` not of type `SessionId` and ZERO `SessionId`-typed fields under another name, across every message including oneof arms; `CheckpointRequest` and `CheckpointResponse` are the only oneof-carrying messages and only the first is a request type. — EVIDENCE: schemas/lenny-adapter.proto

FACT: `spec/04:726` ("The request is pod-scoped.") really is the only surviving per-message pod-scope statement after SPEC-1. A grep for "pod-scoped" over spec/ returns six hits; spec/10:60 is the hold gauge, spec/04:872 is credential rotation, spec/12:202 is Redis key prefixes, and the other two (:151, :188) are inside the block SPEC-1 retires. SPEC-1's sentence claiming :726 is left as the only such sentence is exact. — EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60; spec/12_storage-architecture.md:202

FACT: SPEC-1's attribution of `spec/10:60` to 0076's SPEC-1 under its D5 is correct. 0076's SPEC-1 block, not SPEC-2, stages the §10.1.4 Observability bullet and its closing clause "the hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped under D5". — EVIDENCE: proposals/0076_.../0076_....spec-changes.md:304-321, :20-31 (D5)

FACT: `protoFields` has exactly ONE caller outside its own file (`claim_register_proto_agreement_test.go:64`) and `protoServiceRequests` exactly one (`adapter_proto_message_scope_test.go:87`). The message-scope gate reads `protoServiceRequests`, NOT `protoFields`, so the caller that moves when the parse gains field types is the claim-register gate, which is what §9 and TEST-1 both say. — EVIDENCE: grep for `protoFields(`/`protoServiceRequests(` over tests/, pkg/, cmd/

FACT: No tier-11 or other gate reads §4.1's `#### Request Message Scope` block as text. The only files matching "message scope"/"messageScope" outside spec/04 are the two tier-0 parse/gate files, `session_address_wire_test.go`, and two files where the string appears only inside a `// spec:` annotation (`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`, `tests/tier3_contract/rest_sessions/slot_address_absence_test.go`). Retiring the block breaks no reader beyond the gate TEST-1 replaces. — EVIDENCE: grep -rln over tests/, pkg/, cmd/, docs/, scripts/, spec/

WATCHOUT: two loose attributions I examined and deliberately did NOT file, so a later round does not spend the same hour on them. (1) summary.md:114-116 calls the retired gate's four non-coverage refusals ones that "check the table against itself"; two of them (unknown message, wrong service) actually compare the table to `protoServiceRequests`. The load-bearing claim — that all four lose their subject with the table — is true either way, so the OD1 argument is unaffected. (2) summary.md:133-134 attributes "hold `last_fenced_generation` per bound session" to `spec/10:38` and `:40`; only :40 uses that phrase, while :38 says "records the new generation for that session". Both are wording, not conclusions. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:96-124; spec/10_gateway-internals.md:38, :40

USEFUL [non-spec-recheck.1.review-citations.1]: its warning that `summary.md` is NOT in the cache key, and that the r1 citations answer was a cache hit written by the spec lane against an older summary, is why I audited the summary from scratch instead of trusting any prior empty result. That gap is now closed: the current summary text has been citation-audited end to end at this round's hash `7b1e01aac3c0`.

USEFUL [f2.other-proposals]: its per-line verification of the retired block's anchors (:151, :153-186, :188, :190) and of which commits entered the three spec-map registrations saved re-deriving the git history behind the 0073 impacts row.

CORRECTS [spec.1.fix-design-G1.1]: that entry gives `message SessionId` at `schemas/lenny-adapter.proto:595`. It is at :596, which is what the proposal itself cites in problem-statement §1.1. The proposal is right and that log entry is off by one; do not "correct" the proposal toward :595.


### [non-spec-recheck.3.review-client-surface.1]

DECISION: Returned an EMPTY findings list under the client-facing-surface lens — BECAUSE every externally-consumed representation this proposal could touch is either untouched or already agrees with the staged rule, and I re-derived that from the tree rather than from earlier shards — ALTERNATIVES: filing the tier-3 `sessionScopedMessages` conjunction (barred: it is summary OD2's own subject and four earlier agents already reached it); filing the spec-map 28.5.3 re-registration as a credit without an annotation (rejected: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` checks one direction only, and the GatewayControl request messages the gate reads are the §28.5.3 MCP-forwarding leg, so a 28.5.3 annotation on the replacement gate is legitimate).

FACT: The proposal's whole client-surface premise re-verifies mechanically. A brace-depth parse of `schemas/lenny-adapter.proto` returns 31 RPC request types, 25 with a top-level `SessionId session_id` and 6 without (`AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`); ZERO name/type convention violations across every message including `oneof` arms; `CheckpointRequest` and `CheckpointResponse` are the only `oneof` carriers. The 25 are exactly the table's 24 non-envelope session rows plus `CoordinatorFenceRequest`. — EVIDENCE: spec/04_system-components.md:155-186 (32 rows: 26 session, 6 pod); schemas/lenny-adapter.proto:1173-1187, :1193-1218, :1249-1257, :1455-1461

FACT: No client-facing parallel representation restates the §4.1 classification. `docs/reference/adapter-contract.md` carries no scope column (its `CoordinatorFence` row at `:69` states no class; its only scope sentence is the §4.7.1 `ReportSessionScrub` restatement at `:81`, which SPEC-1 explicitly keeps). `schemas/lenny-adapter.proto` and `schemas/lenny-adapter-jsonl.schema.json` cite §4.1 nowhere. No file under `sdks/` names `CoordinatorFence`. No CRD, OpenAPI, MCP tool schema, or error-code surface is reached. The summary's "No proto, generated code, handler, SDK, or reader-facing documentation file is touched" holds. — EVIDENCE: docs/reference/adapter-contract.md:69, :81; schemas/lenny-adapter-jsonl.schema.json:5, :61

FACT: The two spec surfaces that could have contradicted the reclassification already agree with it and need no edit. `spec/28_communication-channels.md:314-317` (CH-FENCE **Messages**) states the pod records the generation against the session the fence names and that a fence for one session does not change another's; `spec/10_gateway-internals.md:40` states the pod holds `last_fenced_generation` per bound session; the proto's own doc comment says the same. So §28.5.1, §28.8, and §10.1 carry no pod-scoped statement about the fence to retire. — EVIDENCE: spec/28_communication-channels.md:314-317; spec/10_gateway-internals.md:38, :40; schemas/lenny-adapter.proto:1449-1454

FACT: The only sites in the tree outside `spec/04` that key on the retiring §4.1 table are the three files the proposal already stages. A grep for "message-scope"/"classification table" over every `.go`, `.json`, `.yaml`, and `.proto` returns `tests/tier0_static/adapter_proto_message_scope_test.go`, `tests/tier0_static/adapter_proto_parse_test.go:13`, and `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40` and nothing else; the sibling `send_message_stamp_test.go` in the same tier-3 directory keys on §28.5.3 alone and needs no edit. `tests/claim-map.json` credits neither retiring gate function; `tests/spec-map.json:156`, `:169`, `:5670` are the only three registrations. — EVIDENCE: tests/tier3_contract/adapter_session_address/send_message_stamp_test.go:72-74; tests/spec-map.json:156, :169, :5670

FACT: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` asserts only annotation-implies-credit, never credit-implies-annotation, so a spec-map row is never refused for crediting a section the case does not annotate. Anyone reasoning about the TEST-1 re-registration should not assume the reverse check exists. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:968-988

FACT: `tests/tier11_docs/successor_pointer_test.go` cannot fire on SPEC-1. Its domain `reducedSections` is a three-entry hard-coded literal (`spec/04` §4.7, `spec/15` §15.4, `spec/29` §29.10, all owned by §28.5) and §4.1 is not in it, so retiring the table carries no successor-pointer obligation. I checked this because a section giving up content is exactly what N8 gates. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:52-56

FACT: The summary's shipped-tree defect entry re-verifies line for line after the f2 rewrite: `pkg/adapter/coordination.go:127-134` is the `gen <= st.coord.lastFenced` refusal returning `Accepted: false` plus the `coordinator_handoff_stale` `FailedPrecondition`; `:109-111` is the empty-identifier `InvalidArgument`; `pkg/gateway/coordination/coordfence/coordfence.go:180-183` is the `default` transient arm retrying at the same value; `:171-179` is the stale arm that re-reads and relinquishes when the generation has not advanced; `DefaultMaxAttempts = 3` at `:52`. — EVIDENCE: pkg/adapter/coordination.go:107-134; pkg/gateway/coordination/coordfence/coordfence.go:52, :155-188

USEFUL [non-spec-recheck.2.review-applicability.1]: its FACT that the replacement gate passes on the shipped proto is exact; I re-derived the same measurement independently with a brace-depth parse and it matched item for item, including the zero convention violations across `oneof` arms.


### [non-spec-recheck.3.review-docs-alignment.1]

DECISION: Returned an EMPTY findings list, the fourth consecutive empty return for this lens — BECAUSE the whole staging (spec-changes, non-spec-changes, checklist, summary) is byte-identical to the `non-spec-recheck-r3` snapshot, the change alters no behavior any `docs/` page describes, it adds no metric, alert, error code, flag, endpoint, or operator-facing failure cause, and both residuals it accepts land in text (the gate's blind spot in D1's third paragraph) or are definitional under the staged rule — ALTERNATIVES: filing the missing "The request is pod-scoped." mirror on `docs/reference/adapter-contract.md:82` now that SPEC-1 makes `spec/04:726` the only pod-scope declaration left in `spec/` (rejected: the doc row is incomplete rather than wrong, the asymmetry predates this proposal, and nothing gates it); filing the deferred equal-generation re-fence refusal as an undocumented accepted failure mode (rejected under the lens's own guardrail — `spec/10_gateway-internals.md:39` already orders the retry the shipped code refuses, so the code is the defect and no doc edit is due).

FACT: `diff -ru scratchpad/cp-snap/0075_.../non-spec-recheck-r3 proposals/0075_...` returns NOTHING, review log included. There is no delta this round. The orchestrator note's "the staging changed" did not hold for this firing; do not spend time hunting a delta before running the diff. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r3/

FACT: The proposal's whole docs blast radius is two citations, both exact. `docs/reference/adapter-contract.md:81` is the `ReportSessionScrub` row carrying "The request is session-scoped: it is addressed by the identifier of the released session and names no slot."; `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go` declares `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` at `:43` ending at `:76`, and it pins that exact sentence plus the shared opener on BOTH the spec §4.7 row and the doc row. SPEC-1 is right to leave `spec/04:725` alone: editing it turns that tier-11 gate red. — EVIDENCE: docs/reference/adapter-contract.md:81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,43-76; spec/04_system-components.md:725

FACT: The two summary blocks the open-decisions phase wrote (the refusal note and the rewritten 0073 impacts row) verify line for line. `pkg/gateway/coordination/coordfence/coordfence.go:52` is `DefaultMaxAttempts = 3`, `:171-179` is the stale arm that re-reads the generation and relinquishes on no advance, `:180-183` is the transient/default arm that retries at the unchanged value; `pkg/adapter/coordination.go:109-111` is the empty-session-id `InvalidArgument` refusal and `:127-134` the `coordinator_handoff_stale` `FailedPrecondition`; `spec/10_gateway-internals.md:39` is the "If `CoordinatorFence` fails or times out" bullet ordering the same-value retry; `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43` is the membership comment with its table-keyed clauses at `:39-43` and 18 map members at `:44-63`; `tests/tier0_static/adapter_proto_message_scope_test.go:25-27` is the handler-conformance limit. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:52,171-183; pkg/adapter/coordination.go:107-134

FACT: No `docs/` page mirrors the §4.1 message-scope table, its class words, or any per-message gRPC scope other than `ReportSessionScrub`. `grep -rn "CoordinatorFence" docs/` returns exactly one row, `docs/reference/adapter-contract.md:69`, which states the RPC's purpose and no scope. `docs/getting-started/concepts.md:101` describes `coordination_generation` without claiming pod or session scope, so 0076's move of the value onto the slot entry did not falsify it either. — EVIDENCE: docs/reference/adapter-contract.md:69; docs/getting-started/concepts.md:101

FACT: Retiring the §4.1 table orphans no other definition. The `lenny_adapter_unaddressed_frame_rejected_total` metric's "session-scoped frame" (docs/reference/metrics.md:180, spec/16_observability.md:189) is defined by §28.5.3's own enumeration of the six frame types at spec/28_communication-channels.md:834-835, which cites §4.1 nowhere. The JSONL-leg `sessionId` prose across `docs/runtime-author-guide/` is the same intra-pod surface and is untouched. — EVIDENCE: spec/28_communication-channels.md:833-845; docs/reference/metrics.md:180

USEFUL [spec-recheck.2.review-docs-alignment.1]: its FACT that `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` carries `// spec: 4.1 (request message scope)` on five cases while never opening `spec/04_system-components.md` saved a false lead — that file looks exactly like a §4.1 reader that SPEC-1 would break, and it is not one. I re-confirmed the only test in the tree reading the §4.1 heading is the tier-0 gate TEST-1 replaces.

USEFUL [spec-recheck.2.review-docs-alignment.1]: its WATCHOUT against re-filing the "derived rather than declared" tension between the staged first sentence and the surviving per-message declarations at `spec/04:725`/`:726` is correct and I independently reached the same edge; SPEC-1 defends it explicitly at spec-changes.md:128-139. Do not file it.

OPEN: whether the replacement gate's `// spec:` annotation can honestly name 28.5.3. The retiring gate claims `// spec: 4.1 ..., 28.5.3 (addressing)` at `tests/tier0_static/adapter_proto_message_scope_test.go:129`, and TEST-1 re-registers the replacement under the same two sections. §28.5.3 is the intra-pod boundary (CH-MSGSOCK / CH-RUNTIMEOPS frames), while the replacement gate reads gRPC request messages on LNK-POD-GRPC alone and no longer reads any spec text. The credit may be inherited looseness from 0073 rather than a defect this proposal creates, so I did not file it; a test-coverage or edit-sites reviewer with the register in hand should settle whether `tests/spec-map.json:5670` should follow the gate or be dropped. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:129; tests/spec-map.json:5670; spec/28_communication-channels.md:499


### [non-spec-recheck.3.review-edit-sites.1]

DECISION: Returned an EMPTY findings list after a full, uncached edit-site sweep — BECAUSE the cache probe MISSED (key `edit-sites-r3-7b1e01aac3c0.json`, the staging changed since `4e0efd1c4849`), so this is the first real edit-site sweep of this staging; nothing outside the proposal's own edit lists becomes wrong when SPEC-1, TEST-1, and TEST-2 apply — ALTERNATIVES: filing the one-line offset in SPEC-1's `schemas/lenny-adapter.proto:458` citation (the field is at `:457`, `:458` is the `slot_id` retirement comment) — rejected, it is a close variant of the already-refuted "proto anchors are stale by seven or eight lines" finding and clears no materiality bar; filing that TEST-2's deletion of the `:39-43` clause also removes the "covered by the session-address arm below alone" claim for `ExportPathsRequest` and `ConfigureWorkspaceRequest` — rejected, that claim is false of those two today, so deleting it removes a falsehood rather than creating one, and neither message gains or loses coverage.

FACT: The snapshot at `scratchpad/cp-snap/.../non-spec-recheck-r3` is byte-identical to the live directory, so `diff -ru` against it shows nothing. The delta is recoverable from git instead: `git diff 1037a9172 -- proposals/0075_.../*.md`. Two hunks — `non-spec-changes.md:5-9` (TEST-2's ground rewritten off commit `01d19af01` onto the message's own declaration plus `040323634`) and `summary.md` (the coordfence second-attempt account, and the widened 0073 impacts row). — EVIDENCE: `git log --oneline -3 -- proposals/0075_fix_derive-message-scope-from-the-address-type/`

FACT: Nothing in `spec/`, `docs/`, `schemas/`, or `charts/` outside the retired block declares a request message's scope class, so the retirement breaks no companion surface. A full grep for `session-scoped|pod-scoped` over those four trees returns, for per-message classification, only `spec/04_system-components.md:725` (`ReportSessionScrub`, session) and `:726` (`ReportPodScrub`, pod), both in §4.7.1 and both outside SPEC-1's block, plus the retired block itself. Every other hit is about a token, a lock, a Redis key, a JSONL frame, or the coordinator hold. — EVIDENCE: spec/04_system-components.md:725, :726

FACT: `#### Request Message Scope` has zero inbound references anywhere in the tree — no link, no anchor, no `§4.1` cross-reference pointing at the table. `grep -rn "request-message-scope\|Request Message Scope"` over spec/ docs/ schemas/ charts/ tests/ pkg/ cmd/ returns exactly the heading itself. It is also a `####` heading with no `spec/README.md` index row (the index carries §4.1 only, at `spec/README.md:15`), so `heading_walker_test.go` never inspects it. — EVIDENCE: spec/README.md:15; spec/04_system-components.md:149

FACT: The blast radius on the tier-0 parse is exactly two symbols and two callers, and nothing else in the tree touches them. `protoServiceRequests` has one caller (the retiring gate, `:87`); `protoFields` has one other caller (`claim_register_proto_agreement_test.go:64`); `braceDelta`, `protoMessageOpen`, `protoField`, `protoRPC`, `protoServiceOpen`, `messageScopeSpecPath`, `parseMessageScopeTable`, and `declaredScope` have no consumer outside those two files. The two tier-0 files 0076 landed (`adapter_proto_generation_scope_test.go`, `adapter_barrier_doc_comment_scope_test.go`) read comment text through their own parsers and touch neither the shared parse nor the §4.1 table. — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:33-90; tests/tier0_static/claim_register_proto_agreement_test.go:64

FACT: The retiring gate's function names are registered in exactly one place. `grep -rn TestAdapterProtoRequestMessagesAreClassifiedByScope` over the whole tree returns `tests/spec-map.json:156`, `:169`, `:5670` and the declarations themselves. The enclosing section keys are 4.1, 4.1, and 28.5.3, exactly as the summary's 0073 row states. `tests/claim-map.json` carries no row keyed on the table or the gate; `tests/registers/*.yaml` carry no `spec/04_system-components.md` line or anchor entry except the eight positional `identifier-senses` rows, whose sites are all CH-RUNTIMEOPS spellings absent from the deleted block. — EVIDENCE: tests/spec-map.json:156,:169,:5670; tests/registers/identifier-senses.yaml:14-37

FACT: `gate_integrity_test.go`'s `tierZeroGates` register does NOT name the message-scope gate, so retiring its two functions costs no entry there. It names fifteen other tier-0 gates by function and file. Do not add it to the files-touched list. — EVIDENCE: tests/tier0_static/gate_integrity_test.go:63-88

FACT: `successor_pointer_test.go` cannot fire on SPEC-1. Its `reducedSections` domain is a hard-coded three-row literal (`spec/04` §4.7, `spec/15` §15.4, `spec/29` §29.10, all owned by §28.5); §4.1 is absent, and SPEC-1 moves no content to another heading in any case. Same for `relocated_material_pointer_test.go`, whose `relocatedStatements` anchors are all CH-RUNTIMEOPS/CH-MCP sentences. — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:52-56; tests/tier11_docs/relocated_material_pointer_test.go:50-56

FACT: Every code anchor the current staging carries re-verifies exactly. `pkg/adapter/server.go:302` is `hold holdState`; `pkg/adapter/slot.go:59` is `coord coordinationState`; `pkg/adapter/coordination.go:17-38` is the per-session struct and its doc, `:107` the fence handler, `:116` the `boundSlotState` resolve; `pkg/adapter/checkpoint.go:74-84` is the `// spec: §4.1` comment plus the empty-address `InvalidArgument`; `coordfence.go:171-179` is the stale arm's re-read and relinquish and `:180-183` the transient arm, with `DefaultMaxAttempts = 3` at `:52`, so the summary's "gives up on the second of its three attempts" holds; `schemas/lenny-adapter.proto:499-503` is `ReportPodScrubRequest` exactly, `:1217` is `CheckpointStart`'s `SessionId session_id = 7`, `:1455-1461` is `CoordinatorFenceRequest` with no `reserved`. — EVIDENCE: pkg/adapter/coordination.go:107,116; pkg/gateway/coordination/coordfence/coordfence.go:52,171-183

WATCHOUT: SPEC-1 cites `schemas/lenny-adapter.proto:458` for `ReportSessionScrubRequest`'s `SessionId session_id = 2`. The field is at `:457`; `:458` opens the `// Field 3 held slot_id` retirement comment. One line, same message, and the message-level claim is true, so it is below the bar — but do not re-derive it as a fresh finding, and re-anchor it if the block is edited for another reason. — EVIDENCE: schemas/lenny-adapter.proto:455-458; proposals/0075_.../0075_....spec-changes.md:131-132

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its note that `slotAddressCaseFiles` names the gate file by PATH at `:336` (and `adapter_proto_parse_test.go` at `:337`) is exactly right and is the reason TEST-1 owes that register no edit. I re-derived it and add the mechanism: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` falls back to `citedSectionsInFile` for a file with no per-case credits, and `adapter_proto_parse_test.go` carries no `// spec:` annotation at all today, so it demands nothing. An implementor who adds a `// spec:` line to the extended parse acquires a spec-map credit obligation that does not exist now. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336-337, :970-988

OPEN: the proposal still does not state whether the replacement gate keeps the path `tests/tier0_static/adapter_proto_message_scope_test.go`. Every enumeration assumes it does, and that assumption is load-bearing for exactly one register (`slotAddressCaseFiles:336`). Restated from [spec.1.fix-design-G2.1] because it survived this round unchanged and is the single thing that would widen the files-touched list.


### [non-spec-recheck.3.review-feasibility.1]

DECISION: returned no findings — BECAUSE every actor this proposal assigns an action to can perform it against the tree as it stands today, and the one mechanism that provably cannot work (TEST-2's `sessionScopedMessages` membership) is already carried as OD2, which the rubric puts outside review — ALTERNATIVES: filing the TEST-2/OD2 contradiction (declined: [spec.3.review-feasibility.1] at review-log.md:920, [non-spec-recheck.1.review-*] at :2030 and :2133 each derived it independently and each declined on the same ground; a fourth filing would re-litigate a settled disposition)

FACT: the replacement gate's four clauses are all green against the proto as it stands, so TEST-1 lands without a proto change. All 27 `SessionId`-typed field declarations in `schemas/lenny-adapter.proto` are spelled `SessionId session_id`, and no field named `session_id` carries another type. Only `CheckpointRequest` (:1174) and `CheckpointResponse` (:1250) declare a `oneof`; `CheckpointResponse` is the request type of no RPC; `CheckpointRequest` declares only `int64 coordination_generation = 4` outside its oneof, and exactly one of its three frames (`CheckpointStart`, :1217) declares the address — EVIDENCE: schemas/lenny-adapter.proto:1174, :1186, :1217, :1250

FACT: the proto measurement in §1.1 reproduces exactly. A parse of the two service blocks returns 31 request messages, 25 with a top-level `session_id`, and the 6 without are `AdapterEventsRequest`, `CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`, `ReportPodScrubRequest`. With the fence moved to session and `CheckpointRequest` taken by the envelope clause, the derivation reproduces the retired table's 32 rows exactly — EVIDENCE: schemas/lenny-adapter.proto; spec/04_system-components.md:155-186

FACT: the shared parse's two callers are `claim_register_proto_agreement_test.go:64` (`protoFields`, with lookup sites at `:72` and `:82`) and `adapter_proto_message_scope_test.go:87` (`protoServiceRequests`). Nothing else in `tests/` calls either, so TEST-1's "other caller" enumeration is complete — EVIDENCE: tests/tier0_static/adapter_proto_parse_test.go:36,68; tests/tier0_static/claim_register_proto_agreement_test.go:64,72,82

FACT: the two tier-0 files 0076 landed beside the gate, `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go`, read doc-comment text and call neither `protoFields` nor `protoServiceRequests`, so neither is an edit site of this proposal. §9's omission of them is correct — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:11-25, :41

FACT: after SPEC-1 applies, `spec/04_system-components.md:726` ("The request is pod-scoped.") is the only sentence anywhere in `spec/` that classifies a request message pod-scoped. `spec/10:60` scopes the hold and its gauge, `spec/04:872` scopes a rotation ceiling, and `spec/12:202` scopes Redis keys; none classifies a request message. SPEC-1's claim to that effect at spec-changes.md:139 holds — EVIDENCE: spec/04_system-components.md:726; spec/10_gateway-internals.md:60; spec/04_system-components.md:872; spec/12_storage-architecture.md:202

WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go:336` names `adapter_proto_message_scope_test.go` by PATH in `slotAddressCaseFiles`, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (`:970`) then demands a spec-map credit for every case in that file under every section the case's own `// spec:` annotation names. The replacement gate must therefore keep the file's path and register each new case under each section it annotates. TEST-1 states the constraint; an implementor who renames the file breaks a gate the proposal never mentions — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336, :970-988

FACT: the §4 delta this round introduced verifies. `CoordinatorFenceRequest` at schemas/lenny-adapter.proto:1455-1461 declares `SessionId session_id = 1` and `int64 coordination_generation = 2` and carries no `reserved` statement, and `git show 040323634 -- schemas/lenny-adapter.proto` adds `reserved N` / `reserved "slot_id"` pairs to eighteen messages without touching the fence — EVIDENCE: schemas/lenny-adapter.proto:1455-1461; git show 040323634

USEFUL [non-spec-recheck.1.fix-G1.1]: its FACT block on `040323634` is exactly right and let me confirm the delta in two commands instead of reconstructing the `SlotId slot_id` history.


### [non-spec-recheck.3.review-fresh.1]

DECISION: Filed ONE finding — summary.md:127-129's "no other site in `spec/` declares a message's scope
class" is false and contradicts the proposal's own SPEC-1 — BECAUSE `spec/04_system-components.md:726`
("The request is pod-scoped.") and `:725` are exactly such sites, and spec-changes.md:128-130,:139 name
them and state that retiring the table "leaves `:726` as the only sentence in `spec/` that classifies one
request message pod-scoped". ALTERNATIVES: leaving it because it sits inside an open decision (rejected:
the lens rules bar filing on how a decision is FRAMED, not on a false factual premise inside it, and the
same fact was the ground on which an earlier finding about the gate's pod-scope clause was refuted).

FACT: The whole staging delta since the last converged non-spec review is ONE paragraph,
`non-spec-changes.md:3-9` (TEST-2's ground for the fence's empty retired-field column). `summary.md`,
`spec-changes.md`, `implementation-checklist.md`, `problem-statement.md`, `status.md` are byte-identical to
`scratchpad/cp-snap/.../non-spec-recheck-r1-start`. The `non-spec-recheck-r3` snapshot equals the live
directory, so `diff` against it is empty; diff against `non-spec-recheck-r1-start` instead.

FACT: That new paragraph verifies completely. `CoordinatorFenceRequest` occupies
`schemas/lenny-adapter.proto:1455-1461` with `SessionId session_id = 1` (:1456) and
`int64 coordination_generation = 2` (:1460) and no `reserved`; commit `040323634` added exactly 18
`reserved "slot_id"` lines (the file carries exactly 18, and `git log -S` returns only that commit), and
the fence ALREADY existed at `040323634^` with the same two fields and no `slot_id`, so "left the fence
untouched" is literally true rather than an artifact of the message not existing yet.

FACT: I re-derived the proto measurement with a real oneof-aware parser: 31 RPC request types, 25 with a
top-level `SessionId session_id`, 6 without (`AdapterEventsRequest`, `CheckpointRequest`,
`DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`,
`ReportPodScrubRequest`). Zero `session_id` fields of another type and zero `SessionId` fields under
another name across the whole file, so D2's replacement gate PASSES on the shipped proto on day one.
— EVIDENCE: schemas/lenny-adapter.proto (26 `SessionId session_id` declarations); oneofs only at :1174,
:1250.

FACT: S1's "tier 0 goes red until S2" is exactly right, and I checked it the hard way: the retiring gate's
row regex is applied to the WHOLE of `spec/04_system-components.md`, not to §4.1 alone, and the only 32
lines in that file matching it are the table rows :155-186. Delete them and `parseMessageScopeTable`
returns zero rows, so every one of the 32 in-scope messages reports "carries no row for it".
— EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43,:85-127

FACT: The edit list is complete on the tree. The ONLY site outside spec/04 §4.1 that calls
`CoordinatorFenceRequest` pod-scoped is the tier-3 comment TEST-2 stages
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40`); nothing under `docs/`,
`sdks/`, `charts/`, or `schemas/` restates the §4.1 classification table; no tier-11 gate reads §4.1
(`basic_level_echo_stamp_doc_reconciliation_test.go` never opens spec/04 despite its `// spec: 4.1`
annotation); `relocated_material_pointer_test.go` and `successor_pointer_test.go` name §4.7, not §4.1;
`protoFields` has exactly one other caller and `protoServiceRequests` exactly one.

WATCHOUT: Do NOT re-file the TEST-2-vs-OD2 collision (adding `CoordinatorFenceRequest` to
`sessionScopedMessages` turns `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` red because
`reservesName(md,"slot_id")` fails for any column value). Five agents have now reached it; it is summary
OD2's own subject and is settled outside review. — EVIDENCE:
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141; summary.md:139-150

WATCHOUT: A second, softer tension I deliberately did NOT file, so the next agent does not spend the round
on it: spec-changes.md:124-126 says 0073's recorded gate limit "goes with the gate TEST-1 replaces" while
summary.md:188's 0073 row lists it under "What stands" as surviving. They are reconcilable (the header
COMMENT is deleted; the LIMITATION stays true of the replacement gate), so it is wording, not a defect.

USEFUL [spec.1.fix-design-G1.1]: its FACT that nothing outside spec/04 declares a message's scope class was
the seed for my finding — it is right about the §4.1 TABLE being the only such table, but §4.7.1:725/:726
are per-message scope SENTENCES, and that distinction is the whole finding. Read the two together.


### [non-spec-recheck.3.review-kubernetes.1]

DECISION: Returned an EMPTY findings list. — BECAUSE this proposal's whole surface is a spec §4.1
classification rule over `schemas/lenny-adapter.proto`, a tier-0 text gate, and a tier-3 descriptor test.
It declares no CRD, writes no status subresource, adds no finalizer, touches no admission webhook, adds no
watch or reconcile, and puts no controller on a synchronous request path, so the Kubernetes-idiom lens has
no subject in it. — ALTERNATIVES: reading the summary's "Defects in the shipped tree" coordinator-lease
paragraph as a leader-election idiom finding (rejected: it stages no repair, explicitly defers to 0080
§1.16, and every citation in it verifies).

FACT: The staged text is byte-identical between snapshots `non-spec-recheck-r2` and `non-spec-recheck-r3`
for all seven non-log files; the only delta since `non-spec-recheck-r1-start` is `non-spec-changes.md:5-9`,
TEST-2's ground for the fence's empty retired-field column. `diff -q` per file is the fast way to see this;
diffing whole directories drowns in the review log.

FACT: The summary's coordinator-lease paragraph verifies exactly, and it is the only Kubernetes-adjacent
prose in the proposal. `spec/10_gateway-internals.md:39` is the "retry the fence RPC with the same
generation value (up to 3 attempts with 1-second backoff)" bullet; `:38` and `:40` hold
`last_fenced_generation` per bound session; `:57` states `CoordinatorFence` is "the only way to exit hold
state"; `:60` states "The hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped".
On the code side `pkg/adapter/coordination.go:109-111` is the empty-session-id refusal and `:127-134` is the
`coordinator_handoff_stale` FailedPrecondition, and `pkg/gateway/coordination/coordfence/coordfence.go:171-179`
is the relinquish-on-no-advance arm with `:180-183` the transient retry arm. — EVIDENCE:
spec/10_gateway-internals.md:38-40,57,60; pkg/adapter/coordination.go:109-111,127-134

FACT: Nothing outside the retiring tier-0 gate reads the §4.1 Request Message Scope block. A grep of
`tests/`, `docs/`, `pkg/`, `cmd/`, `schemas/` for "message scope" / "Request Message Scope" returns only
`tests/tier0_static/adapter_proto_message_scope_test.go` (which pins the path at `:31`),
`adapter_proto_parse_test.go` (comments at `:10-15` and `:64-67`), and three files where the string is a
`// spec:` annotation and not a read of the table. A grep of `tests/` and `scripts/` for
`04_system-components` finds no other consumer of §4.1. So SPEC-1's blast radius under tier 11 really is
empty, and §9's file list is complete for that surface. — EVIDENCE:
tests/tier0_static/adapter_proto_message_scope_test.go:31; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:26

FACT: TEST-1's caller citation is right and is easy to misread. `claim_register_proto_agreement_test.go:64`
is `fields := protoFields(protoBody)` — the other caller of `protoFields`, which is the function that would
gain a field's type and `oneof` membership. `protoServiceRequests` (declared at
`tests/tier0_static/adapter_proto_parse_test.go:68`) is a different function in the same file with a
different caller set; do not conflate the two when checking the signature-change claim. — EVIDENCE:
tests/tier0_static/claim_register_proto_agreement_test.go:64; tests/tier0_static/adapter_proto_parse_test.go:68

USEFUL [non-spec-recheck.2.review-applicability.1]: its spec-map FACT saved the whole registration sweep.
I re-verified all three anchors independently and they hold exactly: `tests/spec-map.json:156` and `:169`
under section "4.1" (opening at `:152`) and `:5670` under 28.5.3.


### [non-spec-recheck.3.review-mechanism.1]

DECISION: Returned an EMPTY findings list — BECAUSE every mechanism the staging describes traces end to end against the tree: the derivation rule reproduces the post-OD3 table, the replacement gate's four refusal clauses are stated identically in D1's spec block, D2, the summary's "What changes", and non-spec §8, the gate passes on the shipped proto, and no surface outside the six files §9 names becomes wrong when SPEC-1 applies — ALTERNATIVES: filing the TEST-2-versus-OD2 unappliability (rejected: it is summary OD2's own recorded subject and five agents have now reached it); filing that non-spec §4 frames the open point as a column VALUE while summary OD2 states no value settles it (rejected: §7 and OD2 are open-decision sections whose framing this review is barred from filing on, and the underlying defect is the same one four agents declined).

FACT: `diff -ru scratchpad/cp-snap/0075_.../non-spec-recheck-r3 proposals/0075_...` is EMPTY. The r3 snapshot is byte-identical to the live directory, so there is no delta to read from it. The last text that actually moved is `non-spec-changes.md:3-9` (the `040323634` correction, landed by [non-spec-recheck.1.fix-G1.1]) and the summary sections the f2 phase rewrote. To see a delta, diff against `non-spec-recheck-r1-prefix` or `-r1-start`, as [non-spec-recheck.2.review-applicability.1] recorded.

FACT: SPEC-1's deletion is exactly 32 table rows and nothing else in spec/04 matches the retiring gate's row regexp. `grep -cE '^\| `\w+` \| `\w+` \| [^|]+ \| [^|]+ \|$' spec/04_system-components.md` returns 32, all at :155-186. So the checklist's S1 note — "deleting the table leaves `parseMessageScopeTable` with no rows" — is literally true, not an approximation, and tier 0 goes red for exactly the stated reason — EVIDENCE: spec/04_system-components.md:155-186; tests/tier0_static/adapter_proto_message_scope_test.go:43, :54

FACT: after SPEC-1 applies, the only sentence anywhere in `spec/` that classifies one request message pod-scoped is `spec/04_system-components.md:726` ("The request is pod-scoped."), and the only two that classify one session-scoped are `:725` and the surviving `ShutdownRequest` paragraph at `:190`. A repo-wide grep for "pod-scoped" over spec/ and docs/ otherwise returns only the hold gauge (`spec/10:60`), the rotation ceiling (`spec/04:872`), Redis slot counters (`spec/12:202`), and one runbook line about a gateway-pod issue. SPEC-1's claim about `:726` holds as stated — EVIDENCE: spec/04_system-components.md:725, :726, :190

FACT: the addressing convention the replacement gate checks already holds on the shipped proto in both directions and with room to spare. Zero fields named `session_id` carry a type other than `SessionId`; zero fields typed `SessionId` carry another name; there are 26 `SessionId session_id` declarations (25 top-level on request messages plus `CheckpointStart`). `CheckpointRequest`'s only top-level field is `int64 coordination_generation = 4` and exactly one of its three frames declares the address — EVIDENCE: schemas/lenny-adapter.proto:596, :1173-1187, :1217, :1455-1461

FACT: no reader-facing page restates the classification, so problem-statement §1.4's "no reader-facing page changes" is exact. `docs/reference/adapter-contract.md:69` is the only `CoordinatorFence` mention under docs/ and it carries no scope word; `sdks/` mentions it nowhere. The one doc sentence carrying a per-message scope class is `docs/reference/adapter-contract.md:81` for `ReportSessionScrub`, which SPEC-1 leaves alone and a tier-11 gate holds against `spec/04:725` — EVIDENCE: docs/reference/adapter-contract.md:69, :81; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

FACT: the two tier-0 files 0076 landed beside the gate — `adapter_proto_generation_scope_test.go` and `adapter_barrier_doc_comment_scope_test.go` — read doc-comment text on the proto and on `pkg/adapter/coordination.go`. Neither reads the §4.1 table or any scope class word, so neither is an edit site SPEC-1 misses. I checked this because the orchestrator note flags them as new since the proposal was written — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:11-25; tests/tier0_static/adapter_barrier_doc_comment_scope_test.go:18-24

FACT: the reclassification imports one behavioral obligation (`spec/05:515`'s refusal of a session-scoped request with an empty identifier) and it is already both met AND pinned. The handler refuses at `pkg/adapter/coordination.go:109-111`, and `TestCoordinatorFenceRejectsMissingSessionID` at `pkg/adapter/coordination_test.go:35` is the case. So §8's omission of tier 1 is correct rather than a coverage gap. Note the case is annotated `// spec: §4.7` and is absent from `addressRuleCases` in `tests/tier0_static/address_rule_citation_test.go:29-47`; that inventory is a hard-coded list checked for CREDITS only, never derived from the session-scoped set, so nothing goes red — EVIDENCE: pkg/adapter/coordination_test.go:34-46; tests/tier0_static/address_rule_citation_test.go:29-47

WATCHOUT: `parseMessageScopeTable` reads the WHOLE of `spec/04_system-components.md`, not the §4.1 section, so a future four-column table in that file whose first two cells are backticked identifiers would be parsed as scope rows. It happens that no such table exists today. Anyone reasoning about the retiring gate's failure mode should check that rather than assume section scoping — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43, :139-142

WATCHOUT: after TEST-1 the replacement gate reads `schemas/lenny-adapter.proto` ALONE and no longer reads any spec file. That looked like it would pull the file into `documentConsistencyGates` (a gate whose subject is a repository document may carry no spec-section credit, which would collide with re-registering under 4.1 and 28.5.3). It does not: that register is a two-entry hard-coded literal naming only `test_gaps_test_reference_rename_drift_test.go` and `spec_map_slot_address_registration_test.go` itself. Confirms [non-spec-recheck.2.review-applicability.1] — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:124-136

USEFUL [f2.out-of-scope-defects]: its five citations for the equal-generation re-fence entry are exact to the line — `spec/10_gateway-internals.md:39`, `pkg/adapter/coordination.go:127-134` and `:109-111`, and `coordfence.go:171-179` (stale arm) versus `:180-183` (transient arm). I re-derived all five independently and every one lands on the right statement. That whole summary entry needs no further verification.

USEFUL [non-spec-recheck.2.review-applicability.1]: its FACT that the r3-style snapshot is identical to the live directory, and its identification of `documentConsistencyGates` as a hard-coded literal rather than a predicate, both saved a full sweep. Both re-confirmed.


### [non-spec-recheck.3.review-operational.1]

DECISION: Returned an EMPTY findings list — BECAUSE the operational surface this proposal touches is
genuinely empty (no CRD condition, no metric, no alert, no runbook, no reader-facing page states a
gateway-adapter request message's scope), and every inventory that could have gone stale after the table
retirement re-verified clean against the tree — ALTERNATIVES: filing the tier-11 line in §8 Testing as a
tier named with no concrete case (rejected: no tier-11 gate reads §4.1, so the change reaches nothing
there and naming a case would be inventing coverage); filing the TEST-2-versus-`reservesName` conflict
(barred: it is summary OD2's own subject and four earlier agents already declined it).

CORRECTS [non-spec-recheck.1.review-operational.1]: that entry recorded a CACHE HIT, not a review. The
operational lens had therefore never actually read this staging before this round. Its own WATCHOUT was
right that the key excludes summary.md; the key changed on its own this round
(4e0efd1c4849 -> 7b1e01aac3c0) when non-spec-changes.md §4 was rewritten, so the lens ran for real. Treat
`operational-r1-*` and `operational-r2-*` as one interrupted run's answer replayed twice, not as two
independent clean passes.

FACT: The §4.1 message-scope table has exactly THREE consumers in the whole tree, and all three are already
in §9. `grep -rn "Request Message Scope"` over tests/ and scripts/ returns nothing; the table is reached
only by `parseMessageScopeTable` reading the whole spec/04 body with a generic four-cell row regex. No
spec/, docs/, schemas/, or charts/ file cites the table, and no runbook or alert keys on message scope. —
EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:43,54; tests/tier0_static/adapter_proto_parse_test.go:13,66;
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40

FACT: After SPEC-1, the only per-message scope sentences left anywhere in spec/ are
`spec/04_system-components.md:725` (ReportSessionScrub session-scoped) and `:726` (ReportPodScrub
pod-scoped). A full `grep -n "scoped" spec/04` plus a repo-wide `grep -rn "pod-scoped" spec/ docs/` returns
otherwise only non-message uses: spec/10:60 (hold + `lenny_adapter_coordinator_hold` gauge), spec/04:872
(rotation ceiling), spec/12:202 (Redis key prefixes), docs/runbooks/dual-store-unavailable.md:68. SPEC-1's
claim that `:726` becomes the only pod-scoped per-message sentence is exact. — EVIDENCE:
spec/04_system-components.md:725-726; spec/10_gateway-internals.md:60

FACT: The reader-facing mirror carries NO scope classification for the gateway-to-adapter RPCs. The
`CoordinatorFence` row is `docs/reference/adapter-contract.md:69` and states only what the RPC announces.
The only doc sentence that states a message's scope is `:81` (ReportSessionScrub), pinned in both
directions by `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`, which
reads §4.7 and never §4.1. SPEC-1's citation of that gate is accurate line for line, and the summary's
"no reader-facing documentation file is touched" holds. — EVIDENCE: docs/reference/adapter-contract.md:69,81;
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,43-76

FACT: An independent parse of the two service blocks (top-level fields only, oneof interiors excluded)
returns 31 request messages, 25 declaring `SessionId session_id`, 6 not (`AdapterEventsRequest`,
`CheckpointRequest`, `DemoteSDKRequest`, `GetObservedIntegrationLevelRequest`, `NegotiateVersionRequest`,
`ReportPodScrubRequest`), ZERO name/type convention violations anywhere in the file, and exactly two
oneof-carrying messages (`CheckpointRequest`, `CheckpointResponse`). So D1's rule reproduces the table's
26 session / 6 pod rows once the fence moves, and D2's replacement gate is green on the shipped proto. —
EVIDENCE: schemas/lenny-adapter.proto:1173,1249,1455-1456,1217,596

WATCHOUT: `tests/tier0_static/spec_map_slot_address_registration_test.go::TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
requires every section a file's `// spec:` annotations name to be credited in `tests/spec-map.json`, and
`slotAddressCaseFiles` names ALL THREE tier-0 files TEST-1 touches, by path: `:336` (message scope),
`:337` (the shared parse), `:342` (the claim-register gate). Today `adapter_proto_parse_test.go` carries no
`// spec:` annotation and therefore owes no credit. An implementor who adds one while extending the parse
for field types and oneof membership acquires a spec-map obligation the proposal does not mention. —
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:336-337,342,970-989

WATCHOUT: `tests/tier0_static/address_rule_citation_test.go` requires the sentence "rejected at the adapter
boundary with `InvalidArgument`" to occur in EXACTLY ONE spec section (today `spec/05` §5.2 at `:515`);
`sectionStatingTheAddressRule` t.Fatals when the count is not 1. The block SPEC-1 stages must not restate
that sentence in §4.1, or tier 0 goes red for a reason nothing in this proposal predicts. D1's staged text
does not, as staged. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22,54-89;
spec/05_runtime-registry-and-pool-model.md:515

FACT: Nothing else inventories the retiring gate. `gate_integrity_test.go`, `residual_gate_test.go`,
`tests/claim-map.json`, spec-map's `4.1` (no `notes`) and `28.5.3` (`notes` about the JSONL contract only)
all carry no reference to it, so the three spec-map rows at `:156`, `:169`, `:5670` are the complete
registration surface TEST-1 must move. — EVIDENCE: tests/spec-map.json:156,169,5670

USEFUL [f2.out-of-scope-defects]: its citation set for the equal-generation re-fence entry is exact and I
re-verified every anchor independently (`spec/10_gateway-internals.md:39`;
`pkg/adapter/coordination.go:109-111` empty-id refusal, `:127-134` stale refusal;
`pkg/gateway/coordination/coordfence/coordfence.go:52` DefaultMaxAttempts, `:171-179` stale/relinquish,
`:180-183` transient retry). That saved re-deriving the whole handoff path.


### [non-spec-recheck.3.review-performance.1]

DECISION: Empty findings list under the performance / scalability / failure-mode lens, on the FULL staging (spec + non-spec + checklist + summary), with no cache hit — BECAUSE the staging creates no control-plane or data-plane write of any kind: it retires a prose table, states a derivation predicate, replaces one tier-0 static gate that reads `schemas/lenny-adapter.proto`, extends the shared tier-0 proto parse plus its one other caller, re-points `tests/spec-map.json`, and amends one tier-3 suite. There is no per-task, per-request, or per-session write to multiply against any tier's object counts, the etcd status-write dedup ceiling, or the §12 store sizing. — ALTERNATIVES: I re-derived the fence-amplification argument at coordinator handoff a third time and it fails again for the same reason (see FACT below); I also looked for a scope-driven fan-out or dispatch in the gateway that the reclassification could move and found none.

FACT: There was NO delta to review this round. `diff -rq` of the r3 snapshot against the live proposal directory is empty, and `diff -rq` against the r2 snapshot differs only in `review-log.md`. The staging text is byte-identical to what non-spec-recheck.2 read. The "read the changed sections hardest" reading order had nothing to point at. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r3 vs proposals/0075_fix_derive-message-scope-from-the-address-type

FACT: The staging hash for the cache is `7b1e01aac3c0`. `performance-r3-7b1e01aac3c0.json` did not exist, but `applicability-r2-7b1e01aac3c0.json` and `kubernetes-r3-7b1e01aac3c0.json` do, so other lenses have already reviewed this exact text. A future performance run on unchanged text should hit `performance-r3-7b1e01aac3c0.json` and stop. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0075_fix_derive-message-scope-from-the-address-type/

FACT: No fence-RPC amplification exists to find. The per-session fence is already the specification's own statement, independent of the §4.1 class word: handoff sends one `CoordinatorFence(session_id, new_generation)` per session and the pod holds `last_fenced_generation` per bound session. CH-FENCE says the same. — EVIDENCE: spec/10_gateway-internals.md:38, :40; spec/28_communication-channels.md:314-317

FACT: I verified the four runtime citations in the summary's "Defects in the shipped tree" section rather than trusting them, because they are the only failure-mode assertions in the staging and a false one would be an (a) finding under this lens. All four hold. `pkg/adapter/coordination.go:127-134` is the stale rejection returning `FailedPrecondition` with `coordinator_handoff_stale`; `pkg/gateway/coordination/coordfence/coordfence.go:180-183` is the `default:` transient retry arm; `:171-179` is the stale arm that re-reads the authoritative generation, finds no advance, and relinquishes. The narrative (a landed fence whose ack is lost burns attempt 1 as transient, then is refused as stale on attempt 2, so the coordinator relinquishes on the second of three attempts) is exactly what that code does. — EVIDENCE: pkg/adapter/coordination.go:127-134; pkg/gateway/coordination/coordfence/coordfence.go:171-183

FACT: The §1.2 tree anchors are all correct after 0076, so the earlier rounds' DEFERRED entries on them are closed. `pkg/adapter/server.go:302` is `hold holdState`; `pkg/adapter/slot.go:59` is `coord coordinationState`; `pkg/adapter/coordination.go:107` is the `CoordinatorFence` handler declaration and `:116` is `st, err := s.boundSlotState(sessionID)`. — EVIDENCE: pkg/adapter/server.go:302; pkg/adapter/slot.go:59; pkg/adapter/coordination.go:107, :116

FACT: D2's replacement gate is green on the shipped proto, so S2 does not land tier 0 red for a reason the checklist does not already state. The file declares exactly two `oneof` blocks (`CheckpointRequest` and `CheckpointResponse`) and `grep -nE "^\s*(repeated\s+)?SessionId\s+[A-Za-z_]+\s*="` filtered against `SessionId session_id` returns nothing. — EVIDENCE: schemas/lenny-adapter.proto:1174, :1250

USEFUL [spec.1.review-performance.1]: its WATCHOUT on the pod-in-hold-state-with-no-bound-entry case (spec/10:57 says a fence is the only exit from hold, and the handler now requires a bound slot entry) is the single most tempting failure-mode finding on this proposal, and it is 0076's landed behavior that 0076's OD3 weighed and rejected. Third lens run to reach it independently and stop on that entry. Do not spend a fourth round on it. — EVIDENCE: spec/10_gateway-internals.md:57; pkg/adapter/coordination.go:116

USEFUL [spec.4.review-performance.1]: its FACT that retiring the table strands no failure-path statement (every `CoordinatorFence` site in spec/ and docs/ describes handoff, hold, or the RPC catalog and none cites the classification) held on independent re-check and saved a full grep sweep.

WATCHOUT: `spec/10_gateway-internals.md:39` orders a retry "with the same generation value (up to 3 attempts with 1-second backoff)" and the driver loop at `pkg/gateway/coordination/coordfence/coordfence.go:155-184` has NO backoff sleep at all, on top of the equal-generation refusal the summary already records. The missing backoff is a second, separate divergence from :39 that the summary's defect entry does not name. Both are pre-existing, independent of the classification word, and out of this proposal's scope (§6 names the acceptance predicate a non-goal); the remedy is in spec/10 or proposal 0080 §1.16. Do not file either here. — EVIDENCE: spec/10_gateway-internals.md:39; pkg/gateway/coordination/coordfence/coordfence.go:155-184

UNVERIFIED: whether proposal 0080's residue inventory carries the missing 1-second backoff in the coordfence retry loop as a distinct entry from the equal-generation refusal it already carries at §1.16. Whoever runs the residue register should check; it is a spec-vs-code gap nobody in this lane has filed.


### [non-spec-recheck.3.review-reliability.1]

DECISION: Returned an EMPTY findings list for the reliability lens, third consecutive empty on this proposal — BECAUSE the staging adds no runtime code path at all (one spec prose block, one static tier-0 gate, one shared text parse, one spec-map re-registration, one tier-3 descriptor suite), so there is no retry, lease, dedup, drain, checkpoint write, or store-failover behavior it introduces to trace through crash and restart; and the one recovery-adjacent consequence of the reclassification — the fence inheriting the spec/05:515 empty-identifier refusal — is already implemented and already pinned. ALTERNATIVES: filing the pod-wide-hold-with-no-bound-session dead end (rejected: it is landed 0076 behavior, SPEC-1 explicitly leaves spec/10_gateway-internals.md:57 and :60 alone, and 0076's OD3 weighed it); filing the same-generation re-fence refusal (rejected: pre-existing, owned by proposals/0080_...:216 §1.16, and the summary already records it as an unstaged shipped-tree defect with correct citations).

FACT: The round delta is exactly two sentences and it is TRUE. non-spec-changes.md §4 (lines 3-9) now grounds the fence's empty retired-field column on the proto and the retirement commit rather than on the field-introduction commit. Verified both halves: `CoordinatorFenceRequest` declares `SessionId session_id = 1` and `int64 coordination_generation = 2` with no `reserved` of either kind, and `git show 040323634 -- schemas/lenny-adapter.proto` adds `reserved N; reserved "slot_id";` to sixteen messages and to CoordinatorFenceRequest in none of them. EVIDENCE: schemas/lenny-adapter.proto:1455-1461; commit 040323634 ("Address a session on the gRPC leg by its session identifier alone")

FACT: The derived session class is a strict SUPERSET of the retired table's session class on today's proto, which is what makes the retirement reliability-neutral: every table session row declares a top-level `SessionId session_id`, so no message moves session→pod and no fail-closed refusal is lost. I re-derived this rather than trusting the earlier shards, spot-checking the two "never carried the duplicate" rows and all five GatewayControl rows. EVIDENCE: schemas/lenny-adapter.proto:1539 (ExportPathsRequest), :1674 (ConfigureWorkspaceRequest), :342, :365, :384, :404, :420 (the five GatewayControl request messages)

FACT: spec/05_runtime-registry-and-pool-model.md:515 is the ONLY rule anywhere in spec/ keyed on a request's scope class ("a session-scoped request whose session identifier is empty is rejected at the adapter boundary with `InvalidArgument` before any root is resolved"). A repo-wide grep for "session-scoped request"/"pod-scoped request" over spec/*.md returns that line and nothing else normative. So the class word carries exactly one behavioral obligation, and the fence handler already discharges it before any resolution. Do not go looking for a second consumer of the class. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:515; pkg/adapter/coordination.go:108-111

FACT: The summary's out-of-scope reliability bullet (the pod refusing the same-generation fence retry §10.1.2 orders) is accurately cited in every part, which I checked line by line because a false citation inside it would have been a clean (a) finding: spec/10_gateway-internals.md:39 carries "with the same generation value (up to 3 attempts with 1-second backoff)"; the adapter's stale rejection is at pkg/adapter/coordination.go:127-135; coordfence.go:180-183 is the transient arm that retries without advancing `gen`; :171-179 is the stale arm that re-reads, finds no advance, and relinquishes. EVIDENCE: proposals/0075_.../0075_....summary.md:161-171

FACT: Every test-surface anchor TEST-1 and §9 depend on resolves today. adapter_proto_message_scope_test.go: recorded-limit header :17-27, `parseMessageScopeTable` :54, `declaredScope` :75-81, `// spec: 4.1 ..., 28.5.3` :129. adapter_proto_parse_test.go: package doc :10-15, `protoServiceRequests` comment :64-67. claim_register_proto_agreement_test.go:64 is the only other caller of the shared parse (`protoFields`). tests/spec-map.json:156 and :169 under §4.1, :5670 under §28.5.3. EVIDENCE: the five files named

FACT: No tier-0 file other than adapter_proto_message_scope_test.go itself references `parseMessageScopeTable`, `declaredScope`, or `messageScopeDisagreements`, and the two sibling tier-0 gates 0076 landed (adapter_proto_generation_scope_test.go, adapter_barrier_doc_comment_scope_test.go) read proto comment text alone and never open spec/04. Retiring the gate therefore breaks no sibling and §9's file list is complete on the tier-0 surface. EVIDENCE: `grep -rn 'parseMessageScopeTable|declaredScope|messageScopeDisagreements' tests/ --include=*.go` returns only that file plus two prose mentions; tests/tier0_static/adapter_proto_generation_scope_test.go:11-25

WATCHOUT: The reliability cache key (md5 of spec-changes + non-spec-changes + implementation-checklist) does NOT include summary.md, and this round's summary carries OD2, which is where the whole `sessionScopedMessages` reservation problem is actually adjudicated. A round whose only delta is in the summary will hit a stale cache and never read it. I read the summary in full despite the cache miss being in the other three files. EVIDENCE: proposals/0075_.../0075_....summary.md:139-150

CORRECTS [non-spec-recheck.1.review-test-coverage.1]: its closing WATCHOUT calls the three-arm problem "OD2's subject and settled outside review" without saying where OD2 lives. OD2 is in summary.md:139-150, NOT in spec-changes.md §7, which lists a single open decision framed narrowly as "what the retired-field-number column holds". The two documents state different things about the same decision: §7 presupposes a value exists, while OD2 says "no value settles it" because `reservesName(md, "slot_id")` fails for the fence whatever number is chosen, and offers two structural candidates instead. I chased the apparent contradiction between TEST-2 ("add `CoordinatorFenceRequest` to `sessionScopedMessages`, settling the retired-field-number column per §4") and OD2's finding, and declined to file: the rubric bars filing on how an open decision is framed, and two lenses have now declined the same ground. A future round that wants to file this must file it as the staging's own inconsistency (TEST-2 and non-spec §4 both direct an action OD2 has established cannot be completed as written), not as a testing gap. EVIDENCE: proposals/0075_.../0075_....spec-changes.md:154-155; summary.md:139-150; non-spec-changes.md:8-9, :39-43; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141

USEFUL [spec.4.review-reliability.1]: its FACT that spec/28_communication-channels.md:314-318 already reads the fence per-session saved me from re-filing §28 as a missed edit site, and its UNVERIFIED on the same-generation retry is now owned in writing by summary.md's shipped-tree-defects section and by 0080 §1.16, so that thread is closed rather than open.

USEFUL [non-spec-recheck.1.review-reliability.1]: its WATCHOUT about the cache key omitting summary.md is correct and is why I read the summary this round instead of trusting a three-file hash.


### [non-spec-recheck.3.review-security.1]

DECISION: Returned an empty findings list under the security lens — BECAUSE every established control the lens owns is untouched by this proposal, and the two residuals the derivation introduces are already the human's open decision (summary OD1) — ALTERNATIVES: filing the TEST-2/OD2 contradiction (see USEFUL below, already twice recorded as deliberately not filed); filing the "nested `SessionId` field derives pod-scoped" fail-open variant of OD1's residual (rejected: OD1 already puts the residual class to the human, and "how an open decision is framed" is outside this grant).

FACT: the derivation reproduces the retired §4.1 table exactly. Re-parsed the two service blocks today: 31 distinct RPC request types, 25 declaring a top-level `SessionId session_id`, 6 not (`CheckpointRequest`, `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`, `ReportPodScrubRequest`). The 25 map onto the table's 24 session rows plus `CoordinatorFenceRequest`; the 5 non-envelope misses map onto the pod rows. Every `SessionId` field in the whole file is spelled `SessionId session_id` and every `session_id` is of that type, and there are no nested messages, so D2's replacement gate passes on the tree as it stands and would have a real falsifier. — EVIDENCE: schemas/lenny-adapter.proto:342,365,384,404,420,458,596,683,707,847,909,965,990,1022,1036,1064,1088,1118,1217,1305,1339,1456,1487,1539,1577,1610,1674; spec/04_system-components.md:153-186

FACT: the security consequence of reclassifying the fence is already met in the tree. The one behavioral obligation a session-scoped request carries — the empty-identifier refusal of `spec/05_runtime-registry-and-pool-model.md:515` — is enforced by the fence handler before any entry is resolved, and a unit case already pins it. — EVIDENCE: pkg/adapter/coordination.go:109-111; pkg/adapter/coordination_test.go:181-182

FACT: no surface outside `spec/04_system-components.md` §4.1 classifies a gateway-adapter request message by scope class, so SPEC-1's retirement strands nothing. The only per-message survivors are the §4.7.1 rows at `spec/04_system-components.md:725` and `:726`, which SPEC-1 keeps and which agree with the derivation. `docs/` never restates the gRPC classification (its "session-scoped frame" language is the JSONL leg), and no register under `tests/registers/` and no `tests/claim-map.json` row keys on the table. — EVIDENCE: spec/04_system-components.md:726; docs/reference/adapter-contract.md:81

FACT: the tier-0 blast radius of TEST-1 is wider than §9 lists but needs no extra edit. `tests/tier0_static/spec_map_slot_address_registration_test.go:336-337` carries both `adapter_proto_message_scope_test.go` and `adapter_proto_parse_test.go` in `slotAddressCaseFiles`, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:970-989) requires every case in those files to be credited under each section its own `// spec:` annotation names. The inventory is keyed by FILE, and both files survive the rewrite, so no edit to that gate is due — but an implementor who renames either file, or gives the replacement gate an annotation citing a section spec-map does not credit, turns tier 0 red there and not in `validate-maps`. The proposal's TEST-1 already states the per-case credit rule; this is where it is enforced. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:236,336-337,970-989

FACT: `tests/tier0_static/address_rule_citation_test.go` is a second gate over TEST-2's file. It derives, from `spec/`, the single section stating "rejected at the adapter boundary with `InvalidArgument`" and requires `session_address_wire_test.go::TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` to cite it. It fails if MORE THAN ONE spec section carries that sentence, so a staged §4.1 block that restated the empty-identifier refusal would break it. The block D1 stages does not, so it stays green — do not add that sentence to §4.1 during convergence. — EVIDENCE: tests/tier0_static/address_rule_citation_test.go:22,45-48,54-88; spec/05_runtime-registry-and-pool-model.md:515

USEFUL [f1.cleanup at review-log.md:2030] and [review-log.md:2002]: both record that adding `CoordinatorFenceRequest` to `sessionScopedMessages`, which is what non-spec-changes.md:39-40 stages and the summary's deliverable index repeats, turns tier 3 red for any column value, and both deliberately did NOT file it because summary OD2 (:139-150) records exactly that as the human's open decision. I re-derived the same contradiction independently before reading them and would have filed it; those two entries saved a finding that would have been refuted. I verified the ground once more: `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` asserts `reservesName(md, "slot_id")` for every map member, and the fence reserves neither a number nor the name. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:32,130-141; schemas/lenny-adapter.proto:1455-1461

FACT: the round-7 delta is one paragraph, `non-spec-changes.md:3-9`, and it verifies clean. The fence declares `SessionId session_id = 1` and `int64 coordination_generation = 2` with no `reserved` at `schemas/lenny-adapter.proto:1455-1461`, and `git show 040323634 -- schemas/lenny-adapter.proto` adds `reserved N; reserved "slot_id";` pairs to the messages that carried the duplicate and never names `CoordinatorFenceRequest`. — EVIDENCE: schemas/lenny-adapter.proto:1455-1461; commit 040323634

WATCHOUT: the snapshot the orchestrator names for this round, `scratchpad/cp-snap/.../non-spec-recheck-r3`, is byte-identical to the current proposal, so `diff -ru` against it prints nothing and looks like a broken snapshot. The delta is recoverable against `non-spec-recheck-r1-prefix`, which is the only snapshot whose `non-spec-changes.md` differs. — EVIDENCE: scratchpad/cp-snap/0075_fix_derive-message-scope-from-the-address-type/non-spec-recheck-r1-prefix/


### [non-spec-recheck.3.review-test-coverage.1]

DECISION: Returned an EMPTY findings list for the test-coverage lens, a second time on this lane — BECAUSE the only staging text that moved since [non-spec-recheck.1.review-test-coverage.1] is descriptive (non-spec-changes.md §4's ground for the fence's empty retired-field column, and two summary passages), none of it adds, removes, or reframes a deliverable, and §8's case list still covers one negative case per gate clause plus the tier-3 constraint — ALTERNATIVES: filing tier 11 as a named-but-uncased tier (rejected: no tier-11 gate reads §4.1's table or the `#### Request Message Scope` heading, so tier 11 is a regression run); filing the tier-3 reserved-number arm (rejected: it is OD2's subject, settled outside review, and [f2.cleanup] already recorded the same instruction).

USEFUL [non-spec-recheck.1.review-test-coverage.1]: its four FACTs did the whole job of this pass. The reclassification's one imported obligation is already pinned (`pkg/adapter/coordination_test.go:35`), the credit gate never refuses a surplus credit, `slotAddressCaseFiles` needs no edit while the gate keeps its path, and `CheckpointResponse`'s address-free `oneof` makes the request-vs-message population mistake self-catching. Re-verified all four; all hold at HEAD cdcd7e9e.

FACT: the shared parse has exactly two callers and both are already in §9. `grep -n "protoFields\|protoServiceRequests" tests/tier0_static/*.go` returns the two definitions plus `adapter_proto_message_scope_test.go:87` and `claim_register_proto_agreement_test.go:64` and nothing else. `coordinator_hold_allowlist_test.go` uses the same `map[string]map[string]bool` type but builds it itself from `servedMethodsByService`, so it is NOT a third caller and must not be added to a files-touched list. — EVIDENCE: tests/tier0_static/coordinator_hold_allowlist_test.go:96-97

FACT: the delta's two new commit/line citations verify exactly. `message CoordinatorFenceRequest` spans schemas/lenny-adapter.proto:1455-1461 with `SessionId session_id = 1`, `int64 coordination_generation = 2`, and no `reserved` of any kind; `git show 040323634 -- schemas/lenny-adapter.proto` adds 18 `reserved "slot_id"` pairs and names `CoordinatorFence` nowhere. The summary's coordfence account also verifies: transient arm at `pkg/gateway/coordination/coordfence/coordfence.go:180-183`, stale arm re-read and relinquish at `:171-179`. — EVIDENCE: schemas/lenny-adapter.proto:1455-1461; pkg/gateway/coordination/coordfence/coordfence.go:171-183

FACT: the three spec-map registrations the summary's 0073 row names are exact. `tests/spec-map.json:156` (`TestAdapterProtoRequestMessagesAreClassifiedByScope`) and `:169` (`TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage`) sit under `spec/04_system-components.md`; `:5670` re-credits the first under section 28.5.3. — EVIDENCE: tests/spec-map.json:154-170, :5670

WATCHOUT: TEST-2's phrase "restating membership as the derivation rule together with the message having carried the retired duplicate" reads as a two-part membership conjunction that would exclude the fence TEST-2 adds. It is not: the existing comment already uses that exact construction for set-plus-column (`sessionScopedMessages is every request message §4.1 addresses to one session ... together with the number the duplicate address held on it`), so "together with" introduces the value column, not a second membership criterion. [f2.cleanup] reached the conjunction reading and correctly routed it to OD2 rather than filing it. Do not file it under either reading. — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-38

UNVERIFIED: after TEST-2 restates the map's membership as "session-scoped by the derivation rule", `ExportPathsRequest` and `ConfigureWorkspaceRequest` are session-scoped and still absent from the map, so the restated comment is false about the set unless the restatement keeps a carried-the-retired-duplicate criterion or the map is split. The summary records this at :146-150 and folds it into OD2's two candidates; nobody has checked that whichever answer settles the column also settles those two messages. The firing that adjudicates OD2 should.

## Retired

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
- **DEFERRED:** none. Both findings were closed inside the staged spec changes, and neither falsified
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
