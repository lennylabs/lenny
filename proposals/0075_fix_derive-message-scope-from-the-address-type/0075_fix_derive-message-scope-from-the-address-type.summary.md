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
  sections 4.1 and 28.5.3.
- `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` gains
  `CoordinatorFenceRequest` in `sessionScopedMessages` and loses the clause claiming the fence is covered
  by an arm that does not reach it.
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
another address between them, changes what the clause selects. The tier-3 map records each member's
retired field number, and `CoordinatorFenceRequest` never carried the field that column records, so its
row cannot be copied from a neighbour.

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

## Open decisions for human to make

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

Confidence is moderate because one side of the comparison is derivable from no file. The declared table
forced a human to classify each new request message before tier 0 went green, and whether losing that
checkpoint matters is a prediction about how future authors behave.

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

**OD2. How does `CoordinatorFenceRequest` enter the tier-3 `sessionScopedMessages` map?** The map records
each member's retired duplicate-address field number, and the fence never carried that duplicate, so the
column has no value to copy from a neighbour. The loop then found that no value settles it:
`TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates the same map and asserts both that the
message reserves the number and that it reserves the name `slot_id`
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141`), and
`CoordinatorFenceRequest` declares neither (`schemas/lenny-adapter.proto:1455-1461`), so the name half
fails whatever number is chosen. The map's working membership rule is "carried the retired duplicate"
rather than "session-scoped", which is why `ExportPathsRequest` and `ConfigureWorkspaceRequest` are
excluded from it while being session-scoped. The loop derived two candidates and chose neither: split the
map into a session-scoped set and a carried-the-retired-duplicate set, or add the fence to a different arm
of the suite.

## Defects in the shipped tree that this proposal does not stage

None blocks sign-off. Both defects this proposal confirms in the specification are staged: the three §4.1
sentences that ground the declared classification on a counterexample that no longer exists (SPEC-1), and
the tier-3 comment stating a coverage the suite does not have (TEST-2). The §4.1 ground was falsified when
proposal 0076's CODE-1 landed and moved the coordination generation onto the slot entry the identifier
resolves, so both defects are live in the specification now rather than becoming defects when this proposal
applies. One further defect was confirmed in the working tree and is left where it is.

- **The pod refuses the equal-generation re-fence that §10.1.2 orders.** `spec/10_gateway-internals.md:39`
  tells a new coordinator whose `CoordinatorFence` fails or times out to retry "with the same generation
  value (up to 3 attempts with 1-second backoff)", and the shipped adapter refuses that retry.
  `CoordinatorFence` rejects a generation at or below the value it holds for the session, returning
  `FailedPrecondition` with a `coordinator_handoff_stale` detail (`pkg/adapter/coordination.go:127-134`, on
  the per-session entry proposal 0076's CODE-1 introduced). A fence that lands but whose acknowledgement is
  lost at the 5-second deadline therefore burns every retry and drives the coordinator to relinquish the
  lease.

  This proposal records the refusal and stages no repair. The remedy is the handler's comparison together
  with the `CoordinatorFenceResponse` wire comment and the §10.1.2, §28, and §29.8 arms that enumerate the
  refusal cases, none of which is a message-scope classification. Proposal 0080 §1.16 states that remedy in
  full and records proposal 0076's OD2 as its source. Nothing this proposal stages depends on the
  acceptance predicate: the §4.1 block SPEC-1 stages carries the field-set rule, the stream-envelope clause,
  and the addressing convention, and says nothing about acceptance; TEST-1's replacement gate reads
  `schemas/lenny-adapter.proto`; and TEST-2 pins the message's address field. The one behavioral obligation
  the reclassification carries, the refusal of a session-scoped request whose session identifier is empty
  (`spec/05_runtime-registry-and-pool-model.md:515`), the handler already meets at
  `pkg/adapter/coordination.go:109-111`.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0073 | Implemented (2026-08-31) | Retires the §4.1 classification table its SPEC-7 staged and the reconciliation gate its §8 added, replacing both with the derivation rule and a gate over the addressing convention that rule rests on. The rest of that block stands. The `ShutdownRequest` paragraph SPEC-7 staged beside the table (`spec/04_system-components.md:190`) is kept unedited, because it records a divergence between what a request addresses and what its handler touches that the derivation rule does not state. Its §4.2 value rule and everything it states about how the adapter resolves a root stand. The limit 0073 recorded against the retiring gate also survives: the replacement gate reads `schemas/lenny-adapter.proto` alone, so it can no more check a message's scope against what its handler does than the table's gate could. | Nothing. A landed proposal keeps the words it was written with, and every edit here lands in `spec/` and in the tests rather than in that document. |
| 0076 | Implemented | Implements the answer to its OD3. Question A was answered yes: `CoordinatorFenceRequest` is session-scoped once 0076's CODE-1 records the generation on the slot entry its identifier resolves. Question B left the `spec/04` §4.1 edit to a successor rather than staging it in 0076, and this proposal is that successor, because SPEC-1 retires the table those edits would have touched. That answer removes the derivation rule's only counterexample, so the schema, code, and documentation deliverables an earlier revision of this proposal carried are dropped. | Nothing. 0076 is landed and is not edited. |
| 0080 | Draft (2026-09-06, the date of its last commit; the document carries no status file and heads itself `EARLY DRAFT, NOT CONVERGED`) | Leaves §1.16 standing. This change moves the classification and retires the table; §1.16 changes the fence's acceptance predicate and touches neither, so the two are independent in either order. Of the two entries §2 assigns to this proposal, one is taken in full and one is taken in part. The false tier-3 coverage clause for `CoordinatorFenceRequest` is taken: TEST-2 deletes it. The bullet pairing D6's declared message-scope table with the §4.1 `ShutdownRequest` classification limit is taken only for the table, which SPEC-1 and TEST-1 retire together with its gate. The `ShutdownRequest` limit stays as 0073 recorded it, because SPEC-1 keeps the paragraph at `spec/04_system-components.md:190` unedited and the replacement gate reads `schemas/lenny-adapter.proto` alone, so it can no more relate a declared scope to what a handler does than the retired gate could. | Split that §2 bullet when 0080 converges. The declared table and its gate belong on the owned side, and the §4.1 `ShutdownRequest` classification limit returns to the inventory as a gap no proposal takes. §1.16 needs nothing while 0080 remains an inventory. A successor that takes §1.16 states the acceptance predicate and does not restate the classification. |

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
  the shared parse and its other caller, and re-registers the replacement gate's cases under the sections
  the retiring gate's cases held.
- TEST-2 — `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` — Adds
  `CoordinatorFenceRequest` to `sessionScopedMessages`, settling its retired-field-number column, and
  deletes the membership and coverage clauses that key on the retired table.
