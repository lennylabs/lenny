# Summary: Derive message scope from the address type

## Summary

**Problem statement.** The specification declares each gateway-to-adapter request message's scope in a
§4.1 table with one row per request message, and gates that table against `schemas/lenny-adapter.proto`.
The table exists to accommodate a single message, `CoordinatorFenceRequest`, which declared a session
identifier the specification classified as a guard rather than an address and so stood as the one
counterexample to the rule that a request message is session-scoped exactly when it declares a top-level
`session_id` field of type `SessionId`. That message is now classified session-scoped, so the rule has no
counterexample left and the table and its gate accommodate nothing. Three sentences in §4.1 still ground
the declared classification on the removed counterexample, and a tier-3 comment whose membership rule is
that table states a coverage its suite does not have. Nothing at runtime is repaired. This is a classification
change.

**What changes.**

- `spec/04_system-components.md` §4.1 states the derivation rule, its stream-envelope clause, and the
  constraint the derivation rests on in place of the classification table, and the prose grounding the
  declared classification goes with the table.
- The tier-0 gate file that reconciles the table against the proto is replaced by a gate over that
  constraint, checking that a field named `session_id` is of type `SessionId` and a field of type
  `SessionId` is named `session_id`, and that a stream envelope declares no address of its own while
  exactly one of its frames declares the address.
- `tests/tier0_static/adapter_proto_message_scope_test.go` loses the helper that reads a class word out of
  a table cell, which has no subject once the table is gone.
- The parse the tier-0 gates share (`tests/tier0_static/adapter_proto_parse_test.go`) carries a field's
  type and its `oneof` membership, which the rule gate reads and the parse does not carry today, and the
  parse's other caller moves with any change to the signature it reads.
- `tests/spec-map.json` swaps the retiring gate's registered case names for the replacement gate's under
  section 4.1, and the section 28.5.3 entry that credited the retiring gate is deleted.
- `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` separates the set of
  messages the rule addresses to a session from the table of retired duplicate-address field numbers, the
  session set gains `CoordinatorFenceRequest` and the other session-addressed messages the old set
  excluded, and the clause claiming the fence is covered by an arm that does not reach it goes.
- No proto, generated code, handler, SDK, or reader-facing documentation file is touched.

**Decisions.**

- The derivation rule is stated once: a predicate over a request message's field set, a clause that
  classifies a stream envelope through the frame that addresses it, and the constraint the derivation
  rests on. No per-message exception clause is needed (D1).
- The §4.1 table and its reconciliation gate are retired together, and a gate over the addressing
  convention the rule rests on replaces them (D2).
- `CoordinatorFenceRequest` is session-scoped, and this proposal implements that classification rather
  than re-deriving it (D3).
- No guard wrapper type is introduced for the fence's identifier (D4).
- Proposal 0073 is not reopened; every edit lands in the current text rather than in that document (D5).
- This proposal sequences after proposal 0076 and is written against the text 0076 leaves (D6).

**Watch out for.** Proposal 0073 is converged and is not reopened; every edit here applies to text 0073
introduces. Proposal 0076 rewrites the fence handler's state, and its OD3 answer settles the
classification this proposal derives, so this proposal is sequenced after 0076 rather than independent of
it, and its spec edits are written against 0076's applied text. The value rule in 0073 §4.2, which is what
changes adapter behavior, is untouched.

Two further traps sit inside the staged work. The stream-envelope clause names no message: it selects an
envelope by the `oneof` that carries its frames and classifies it through the one frame that declares the
address, so anything that changes which messages declare a `oneof`, or that gives an envelope's frames
another address between them, changes what the clause selects. The tier-3 session-address suite carries two
populations once TEST-2 splits its single declaration: the session set follows the derivation rule and
takes every message the rule addresses to a session, while the table of retired duplicate-address field
numbers is a closed historical set that no later message joins. A message added to one of them is not
thereby a member of the other.

## Goals

- State a request message's scope as a rule the proto can be checked against, so the classification
  follows from the field the message declares, whose name and type the gate holds to one spelling.
- Retire the §4.1 classification table and its reconciliation gate, and put a gate over the addressing
  convention the rule rests on in their place.
- Land the §4.1 correction that proposal 0076's OD3 Question B assigned to a successor.
- Give `CoordinatorFenceRequest` the tier-3 session-address coverage its file claims and does not have.

## Non-goals

- Renaming the RPC, its request field, or that field's type. Nothing on the wire changes.
- Re-deriving the fence's classification. It is settled, and this proposal implements it.
- The scoping of the coordination generation, which this proposal applies after and does not restate.
- The fence's acceptance predicate, meaning whether the pod accepts a re-fence at the recorded generation.
- The §4.2 value rule that rejects an unaddressed session-scoped request, which is unchanged.

## Decisions the reviewer answered

OD1 was answered on 2026-09-08, taking the entry's recommendation: yes, retire the table and its gate,
and accept both residuals. The entry is kept as written below, because it states the ground the answer
rests on, the two residuals the answer accepts, and the 0073 reversal that holds its confidence to
moderate. OD2, OD3, OD4 and OD5 were resolved by the open-decisions-and-impact-review phase across this
proposal's review runs and left this section as they were answered; their records are in the review log.

| Decision | Answer | Source |
|:--|:--|:--|
| OD1 | Yes. Retire the §4.1 declared-scope table and the tier-0 gate that reconciles it, state the derivation rule and the addressing-convention gate in their place, and accept the two residuals the entry names. | The entry's recommendation |

The answer turns on the specification's own reason for declaring rather than deriving being spent.
`spec/04_system-components.md:151` gives that reason as "`session_id` appears on messages of both
classes", and the one pod-scoped message carrying the field was `CoordinatorFenceRequest`, which
proposal 0076's OD3 reclassified to session scope. What the retired gate enforced was coverage alone,
and a classification computed from the protocol definition cannot have a coverage gap.

Two things the answer accepts, recorded so a later reader does not mistake them for oversights. The
fail-open residual: a message that departs from the convention in BOTH the field's name and its type
derives pod-scoped and carries no obligation to refuse an instance naming no session. The fail-closed
residual: a pod-scoped message whose author gives its guard the session address derives session-scoped
and inherits the empty-identifier refusal of `spec/05_runtime-registry-and-pool-model.md:515`.

It also reverses a call proposal 0073 made deliberately, which is why the entry's confidence is moderate
rather than high. 0073 rejected a machine-derivable rule because it "would rest on every future message
author choosing the name correctly, which is the same hand-maintained agreement moved into the field
name". That argument still has force; what changed is its premise, since the counterexample it protected
against no longer exists.


**OD1. Retire the §4.1 declared-scope table and its tier-0 reconciliation gate, or withdraw this
proposal?** `spec/04_system-components.md:153-186` declares each gateway-to-adapter request message's
scope in a table with one row per message, and a tier-0 gate reconciles that table against
`schemas/lenny-adapter.proto` (`tests/tier0_static/adapter_proto_message_scope_test.go`). This proposal
retires both. In their place the specification states a rule, that a request message is session-scoped
exactly when it declares a top-level `session_id` field of type `SessionId` and that a stream envelope
takes the scope of the one frame that declares the address, and a replacement gate checks the addressing
convention the rule rests on rather than any classification. One case is then invisible to the gate: a
message that addresses a session under a field name other than `session_id` and a field type other than
`SessionId`, both departures at once. Such a message derives pod-scoped and carries no obligation to
refuse an instance that names no session, which is the fail-open direction. A second residual runs the
other way: a message whose author gives a pod-scoped guard the session address derives session-scoped and
inherits the refusal of a session-scoped request with an empty identifier
(`spec/05_runtime-registry-and-pool-model.md:515`), which costs a refusal its handler did not intend.
Answering yes accepts both residuals and keeps the proposal as staged. Answering no withdraws the
proposal, and the §4.1 reclassification of `CoordinatorFenceRequest` then has to be re-homed with another
owner.

**Recommendation: yes, accept the residuals and keep the retirement. Confidence: moderate.** The
specification's own reason for declaring rather than deriving is spent. `spec/04_system-components.md:151`
gives that reason as "`session_id` appears on messages of both classes". Every declaration of that field
in the protocol definition is a `SessionId session_id` field, and the only one on a message the table
calls pod-scoped is `CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1456`), which proposal 0076's
OD3 reclassified to session scope. The remaining pod rows declare no session field at all;
`ReportPodScrubRequest` declares `string pod_id` (`schemas/lenny-adapter.proto:499-503`).

What the retired gate enforces is coverage alone. Its header comment states that whether a handler
enforces the scope a row declares is a tier-1 and tier-3 question
(`tests/tier0_static/adapter_proto_message_scope_test.go:25-27`), and `declaredScope` (`:75-81`) accepts
any cell whose first word is `session` or `pod` without comparing it to anything, so a row a human filled
in wrongly passes. A classification computed from the protocol definition cannot have a coverage gap, so
that half of the table's value is reproduced rather than lost. The gate's other refusals (a row naming a
message neither service declares, a row naming the wrong service, a row declaring neither class, and a
message classified twice) check the table against itself and have no subject once the table is gone. The
case that survives the replacement gate is narrower than the case the table left open, because the gate
refuses each half of an unconventional spelling on its own, and the table reached the same case only when
a human filled the row correctly.

Confidence is moderate for two reasons. The first is that this answer reverses a decision proposal 0073
took the other way. 0073 considered restoring a machine-derivable rule and rejected it, on the ground that
"the rule would then rest on every future message author choosing the name correctly, which is the same
hand-maintained agreement moved into the field name"
(`proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:6653-6655`), and it recorded the
declared form's own cost beside that, "D6 replaces a machine-derivable classification with a declared one"
(`:6686`). Two premises behind that decision have since changed: the counterexample the declared form
accommodated is gone, and the replacement gate refuses each half of an unconventional spelling on its own,
where 0073 took the convention to rest on author discipline alone. The second reason is that one side of
the comparison is derivable from no file. The declared table forced a human to classify each new request
message before tier 0 went green, and whether losing that checkpoint matters is a prediction about how
future authors behave. The row the table existed to accommodate is wrong in the tree today: a human
classified `CoordinatorFenceRequest` pod-scoped (`spec/04_system-components.md:175`) and the gate accepted
the cell.

Two alternatives to the recommendation lose. Keeping the table beside the rule preserves that human
checkpoint, and it also keeps two statements of one classification and the gate that reconciles them,
which is the arrangement this proposal exists to remove, while the declared half still cannot be checked
against a handler. Keeping a shorter table naming the pod-scoped messages, which the replacement gate
could read as an independent input, carries the same drift in smaller form, and no other site in `spec/`
declares a message's scope class for such a table to be reconciled against.

A "no" costs the whole proposal, because SPEC-1, TEST-1, and TEST-2 all go with it.
`spec/04_system-components.md:175` and `:188` are false about the tree today whether or not the table
survives, and they contradict `spec/10_gateway-internals.md:38` and `:40`, which hold
`last_fenced_generation` per bound session, and `spec/28_communication-channels.md:314-317`, which states
that a fence for one session does not change the generation the pod holds for another. Proposal 0076's
OD3 Question B assigned that edit to this proposal, so a withdrawal has to name another owner for it, and
the tier-3 coverage TEST-2 adds for the fence goes unowned with it.

## Defects in the shipped tree that this proposal does not stage

None blocks sign-off. Both defects this proposal confirms are staged: the three §4.1
sentences that ground the declared classification on a counterexample that no longer exists (SPEC-1), and
the tier-3 comment stating a coverage the suite does not have (TEST-2). The §4.1 ground was falsified when
proposal 0076's CODE-1 landed and moved the coordination generation onto the slot entry the identifier
resolves, so both defects are live in the tree now rather than becoming defects when this proposal
applies. The further defects confirmed in the working tree are listed below and left where they are.

- **The pod refuses the equal-generation re-fence that §10.1.2 orders.** `spec/10_gateway-internals.md:39`
  tells a new coordinator whose `CoordinatorFence` fails or times out to retry "with the same generation
  value (up to 3 attempts with 1-second backoff)", and the shipped adapter refuses that retry.
  `CoordinatorFence` rejects a generation at or below the value it holds for the session, returning
  `FailedPrecondition` with a `coordinator_handoff_stale` detail (`pkg/adapter/coordination.go:127-134`, on
  the per-session entry proposal 0076's CODE-1 introduced). A fence that lands but whose acknowledgement is
  lost at the 5-second deadline is retried at the same value by the gateway driver's transient arm
  (`pkg/gateway/coordination/coordfence/coordfence.go:180-183`) and refused as stale on the second attempt.
  The stale arm re-reads the authoritative generation, finds no advance, and relinquishes the lease
  (`pkg/gateway/coordination/coordfence/coordfence.go:171-179`), so the coordinator gives up on the second
  of its three attempts rather than exhausting the budget. A second half of the same clause is unmet as
  well: the driver applies no delay between attempts, so the three attempts issue back to back rather than
  at the one-second spacing the clause orders. `pkg/gateway/coordination/coordfence/coordfence.go` is the
  package's only non-test file and it carries no timer, sleep, or backoff of any kind.

  This proposal records both and stages no repair. The remedy for the refusal is the handler's comparison
  together with the `CoordinatorFenceResponse` wire comment and the §10.1.2, §28, and §29.8 arms that
  enumerate the refusal cases, none of which is a message-scope classification. Proposal 0080 §1.16 states
  that remedy in full and records proposal 0076's OD2 as its source. It does not take the missing backoff,
  which is a gateway retry policy that no proposal owns today. Nothing this proposal stages depends on the
  acceptance predicate: the §4.1 block SPEC-1 stages carries the field-set rule, the stream-envelope clause,
  and the addressing convention, and says nothing about acceptance; TEST-1's replacement gate reads
  `schemas/lenny-adapter.proto`; and TEST-2 pins the message's address field. The one behavioral obligation
  the reclassification carries, the refusal of a session-scoped request whose session identifier is empty
  (`spec/05_runtime-registry-and-pool-model.md:515`), the handler already meets at
  `pkg/adapter/coordination.go:109-111`.

- **A published protocol page contradicts the shipped protocol definition.** `docs/api/internal.md:209-215`
  publishes a protobuf excerpt for `CheckpointRequest` declaring `string session_id = 1`, `string
  checkpoint_id = 2`, and `string consistency = 3`. The shipped message declares none of the three. It is a
  `oneof msg` of `CheckpointStart`, `CheckpointGrant`, and `CheckpointAbort` plus `int64
  coordination_generation = 4`, and it declares no address of its own
  (`schemas/lenny-adapter.proto:1173-1187`). The same page is stale elsewhere for the same reason.
  `docs/api/internal.md:272-274` publishes `message DemoteSDKRequest { string session_id = 1; }` while the
  shipped message declares `string reason = 1` alone (`schemas/lenny-adapter.proto:1694-1698`),
  `docs/api/internal.md:111`, `:147`, and `:245` publish a top-level `string session_id` on
  `StartSessionRequest`, `StopSessionRequest`, and `UploadChunk`, and `docs/api/internal.md:94` publishes
  an `UploadFiles` RPC that neither service declares (`schemas/lenny-adapter.proto:32`, `:261`; the
  identifier appears nowhere under `schemas/`).

  This proposal records the staleness and stages no repair. The page is false about the protocol definition
  today, before anything staged here applies, so SPEC-1 does not make it wrong. What SPEC-1 changes is the
  severity: after it, a top-level `string session_id` on a request message is a spelling the specification
  forbids and the replacement tier-0 gate refuses, so these excerpts illustrate a construction the
  derivation rule rules out. Nothing this proposal applies reddens on account of the page. The replacement
  gate's domain is `schemas/lenny-adapter.proto` alone; no tier-11 case parses a `protobuf` fence, because
  the code-block walker dispatches on `json`, `yaml`, `go`, `bash`, and `sql`
  (`tests/tier11_docs/code_blocks_test.go:103-113`); and the only tests naming the page,
  `tests/tier0_static/fragment_link_test.go` and `tests/tier0_static/naming_lint_test.go`, read its anchor
  identifiers rather than its code blocks. The remedy is the whole stale page rather than one excerpt, and
  it belongs to whoever owns that page, which is the documentation loop or proposal 0080's residue
  inventory. 0080 does not carry it today.

- **The §4.7 RPC table describes the fence as a pod-wide announcement.** `spec/04_system-components.md:712`
  gives `CoordinatorFence` the description "Announce new `coordination_generation` to the pod on coordinator
  handoff", followed by "Precondition for any subsequent operational RPC", and names no session in either
  clause. After proposal 0076's CODE-1 the recorded generation lives on the session's slot entry
  (`pkg/adapter/slot.go:59`), and `spec/28_communication-channels.md:314-317` states the current behavior in
  full: the fence announces the generation to the pod, the pod records it against the session the fence
  names, and a fence for one session does not change the generation the pod holds for another. The row keeps
  the first clause of that sentence and drops the two that qualify it, so a reader of §4.7 alone takes the
  announcement to be pod-wide. The reader-facing mirror at `docs/reference/adapter-contract.md:69` carries
  the same compression. The precondition clause beside it stands. The hold the fence clears is pod-scoped:
  §10.1.4 rejects every other inbound RPC with `UNAVAILABLE` and a `coordinator_hold` detail until a new
  coordinator successfully fences (`spec/10_gateway-internals.md:57`) and keeps the hold and its gauge
  pod-scoped (`:60`), `spec/28_communication-channels.md:322` calls the fence the hard precondition for
  every other operational RPC to the pod, and the shipped adapter enforces the hold with pod-level
  interceptors (`pkg/adapter/holdstate.go:335`, `:348`) that a fence for any bound session clears
  (`pkg/adapter/coordination.go:150-156`). Qualifying that clause per session is what the tree refutes, so
  the imprecision recorded here is the announcement clause alone.

  This proposal records the imprecision and stages no repair. It is a residue of proposal 0076's move to
  per-session coordination rather than of the classification change, and it is already imprecise in the tree
  before anything staged here applies. SPEC-1's edit is bounded to the `#### Request Message Scope` block,
  which is `spec/04_system-components.md:151`, `:153-186`, and `:188`, so §4.7's RPC table is not opened, and
  the replacement block names no RPC, no generation, and no hold. The row states no scope class, so the
  derivation rule does not read onto it: the spec sentences classifying a request message pod-scoped are
  `:188`, which SPEC-1 retires, and `:726` for `ReportPodScrub`, which SPEC-1 keeps and which agrees with the
  derivation. Nothing this proposal applies reddens on account of the row. The one case over these rows,
  `tests/tier11_docs/spec_47_rpc_row_naming_test.go`, reads the backticked RPC name in each row's first
  column and never the description (`specTableRowNameRE`, `:35`), and no other test parses §4.7 prose for the
  fence. Correcting the row would add a §4.7 table edit and, to keep the mirror honest, a
  `docs/reference/adapter-contract.md` edit, which contradicts this proposal's statement that no
  reader-facing documentation file is touched. The remedy is the row and its mirror together, and no proposal
  owns it today: proposal 0080's inventory runs §1.1 through §1.21 and none of those entries names the §4.7
  `CoordinatorFence` row.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0073 | Implemented (2026-08-31, its own status line) | Retires the §4.1 classification table its SPEC-7 staged (`spec/04_system-components.md:153-186`), the paragraph at `:151` that introduces it, the paragraph at `:188` that grounds the fence's row, and the tier-0 reconciliation gate its §8 added, replacing them with the derivation rule and a gate over the addressing convention that rule rests on. Three further landed artifacts lose their subject with the table. The tier-3 membership comment at `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43` keys `sessionScopedMessages` on that table and names `CoordinatorFenceRequest` pod-scoped; TEST-2 deletes its clauses at `:39-43` and restates membership on the derivation rule. The shared tier-0 proto parse, which 0073 landed in the same commit as the gate (`fb2af5f9c`), names that table as its subject twice, in its package doc (`tests/tier0_static/adapter_proto_parse_test.go:10-15`) and on `protoServiceRequests` (`:64-67`, "the §4.1 table names the service on every row"); TEST-1 restates both as the addressing-convention gate in the same change, and `protoServiceRequests`' only caller is the retiring gate (`tests/tier0_static/adapter_proto_message_scope_test.go:87`). The retiring gate's two case names, registered under section 4.1 (`tests/spec-map.json:156`, `:169`) and section 28.5.3 (`:5670`) while 0073's work landed, are re-pointed at the replacement gate's cases by TEST-1 under section 4.1, and the section 28.5.3 entry is deleted rather than re-pointed. One property 0073's §8 states is not reproduced: that a request message added later fails the gate until it is classified. A classification computed from the protocol definition cannot omit a message, so the coverage half of that gate carries over and the human classification step does not. What stands: the `ShutdownRequest` paragraph SPEC-7 staged beside the table (`spec/04_system-components.md:190`) is kept unedited, because it records a divergence between what a request addresses and what its handler touches that the derivation rule does not state; the §4.7 RPC-table row and the §5.2 restatement SPEC-7 carried are untouched, as is its §4.2 value rule and everything it states about how the adapter resolves a root; 0073's duplicate-address retirement survives the tier-3 split whole, because TEST-2's new `retiredDuplicateNumbers` keeps the eighteen name-to-number entries of the declaration it splits verbatim and `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates that map alone (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130`), so the population the reservation case asserts over is the same after the split as before it and no `reserved` pair is opened; and the limit 0073 recorded against the retiring gate survives, because the replacement gate reads `schemas/lenny-adapter.proto` alone and can no more check a message's scope against what its handler does than the table's gate could (`tests/tier0_static/adapter_proto_message_scope_test.go:25-27`). | Nothing. A landed proposal keeps the words it was written with, and every edit here lands in `spec/` and in the tests rather than in that document. |
| 0076 | Implemented (2026-09-07, its status file's `implemented-date`; approved 2026-09-06) | Implements the answer to its OD3. Question A was answered yes: `CoordinatorFenceRequest` is session-scoped, because 0076's CODE-1 moved the coordination generation onto the slot entry its identifier resolves (`pkg/adapter/slot.go:59`). Question B left the `spec/04` §4.1 edit to a successor rather than staging it in 0076, and this proposal is that successor, because SPEC-1 retires the table those edits would have touched. That answer removes the derivation rule's only counterexample, so the schema, code, and documentation deliverables an earlier revision of this proposal carried are dropped (D4). Nothing 0076 landed is opened: TEST-1 changes the signature of the proto parse the tier-0 gates share, and the two tier-0 files 0076 added, `tests/tier0_static/adapter_barrier_doc_comment_scope_test.go` and `tests/tier0_static/adapter_proto_generation_scope_test.go`, read doc comments and call neither `protoServiceRequests`, whose one caller is the retiring gate (`tests/tier0_static/adapter_proto_message_scope_test.go:87`), nor `protoFields`, whose other caller is `tests/tier0_static/claim_register_proto_agreement_test.go:64` and is already listed under TEST-1. | Nothing. 0076 is landed and is not edited. |
| 0080 | Draft (2026-09-06, the date of its last commit; the document carries no status file and heads itself `EARLY DRAFT, NOT CONVERGED`) | Leaves §1.16 standing. This change moves the classification and retires the table; §1.16 changes the fence's acceptance predicate and touches neither, so the two are independent in either order. Of the two entries §2 assigns to this proposal, one is taken in full and one is taken in part. The false tier-3 coverage clause for `CoordinatorFenceRequest` is taken: TEST-2 deletes it. The bullet pairing D6's declared message-scope table with the §4.1 `ShutdownRequest` classification limit is taken only for the table, which SPEC-1 and TEST-1 retire together with its gate. The `ShutdownRequest` limit stays as 0073 recorded it, because SPEC-1 keeps the paragraph at `spec/04_system-components.md:190` unedited and the replacement gate reads `schemas/lenny-adapter.proto` alone, so it can no more relate a declared scope to what a handler does than the retired gate could. | Split that §2 bullet when 0080 converges. The declared table and its gate belong on the owned side, and the §4.1 `ShutdownRequest` classification limit returns to the inventory as a gap no proposal takes. §1.16 needs nothing while 0080 remains an inventory. A successor that takes §1.16 states the acceptance predicate and does not restate the classification. When the inventory is next triaged, weigh two further sites for §1 that this proposal records and that §1.1 through §1.21 do not carry, neither of which §3 excludes by design: the §4.7 `CoordinatorFence` announcement row (`spec/04_system-components.md:712`) with its reader-facing mirror at `docs/reference/adapter-contract.md:69`, which is a residue of proposal 0076's move to per-session coordination rather than a classification defect; and the stale protobuf excerpts at `docs/api/internal.md:209-215` and `:272-274`, whose severity SPEC-1 raises by making a top-level `string session_id` on a request message a spelling the specification forbids, although nothing this proposal applies reddens on account of the page. |

## Deliverable index

- SPEC-1 — `spec/04_system-components.md` — Replaces the whole `#### Request Message Scope` block, which is
  the paragraph introducing the declared table, the table, and the paragraph grounding the fence's
  classification, with the derivation rule, its stream-envelope clause, and the constraint the derivation
  rests on together with what the gate cannot see.
- TEST-1 — `tests/tier0_static/adapter_proto_message_scope_test.go` (the tier-0 gate file proposal 0073's
  §8 introduces), `tests/tier0_static/adapter_proto_parse_test.go`,
  `tests/tier0_static/claim_register_proto_agreement_test.go`, and `tests/spec-map.json` — Replaces the
  table-reconciliation gate with the rule gate over the addressing convention, retires the two table
  readers that lose their subject with the table, carries a field's type and its `oneof` membership through
  the shared parse and its other caller, and registers the replacement gate's cases under section 4.1
  alone, deleting the section 28.5.3 credit the retiring gate's case held.
- TEST-2 — `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` — Separates the set
  of messages the derivation rule addresses to a session from the table of retired duplicate-address field
  numbers, brings `CoordinatorFenceRequest` and the other session-addressed messages the old set excluded
  into the two address arms, restates the declaration's comment on the derivation rule, and removes the
  clause claiming a coverage the suite does not have.
