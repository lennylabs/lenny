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
