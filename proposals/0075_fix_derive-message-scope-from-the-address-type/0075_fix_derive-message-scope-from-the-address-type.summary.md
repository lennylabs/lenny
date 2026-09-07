# Summary: Derive message scope from the address type

This document stages the proposed specification and test changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It records a direction and the measurements behind
it. The staged edits are indicative rather than final, and the open questions in §7 are open. Run the
change-proposal convergence loop on it before sign-off.

**Proposal 0076's OD3 has been answered, and this proposal is the successor it names.** The reviewer
answered Question A yes: `CoordinatorFenceRequest` is session-scoped once 0076's CODE-1 records the
generation on the slot entry its identifier resolves. Question B leaves the `spec/04` §4.1 edit to a
successor rather than staging it in 0076, and this proposal is that successor, because SPEC-1 retires the
table those edits would have touched. The answer removes the rule's only counterexample, so the schema,
code, and documentation deliverables an earlier revision carried are dropped; §11 records that.

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

**OD1. Is the residual the replacement gate leaves smaller than the one the retired table left?** Under the
declared table, adding a request message failed the tier-0 gate until a human classified it, which forced a
conscious decision. Under the derivation the classification follows from the field the author declares, and
the replacement gate checks the addressing convention the derivation rests on rather than the
classification itself, so the case it cannot see is a session addressed under both an unconventional field
name and an unconventional field type. The review loop derived the argument for answering yes and did not
close the question: the table's gate caught a missing row, which a total derivation cannot have, and it
never caught a wrong row, because proposal 0073 recorded that the gate cannot check a declared scope
against the handler. The staged changes still carry a withdrawal branch for a no. A withdrawal has to name
an owner for the §4.1 reclassification on its own, because `spec/04_system-components.md:175` and `:188`
are false about the tree today and contradict `spec/10_gateway-internals.md:38-40` and
`spec/28_communication-channels.md:314-317`, whether or not the table survives.

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

**OD3. Must the rewritten §4.1 name the pod-scoped hold exit?** Proposal 0076's OD3 Question A
recommendation, which the reviewer accepted verbatim, reads "yes, reclassify the row to session scope and
rewrite the declaring sentence, naming the pod-scoped hold exit as the one pod-wide effect that remains".
SPEC-1 carries the reclassification and says nothing about the hold exit. Decide whether the hold-exit
clause is a required half of the answer this proposal implements or a separate statement the rewritten
block may omit. The loop recorded the gap without a recommendation.

**OD4. Do the two per-message scope declarations in §4.7.1 stay?** After SPEC-1, §4.1 states that the
classification is derived from the message's field set rather than declared per message, while
`spec/04_system-components.md:725` and `:726` still declare `ReportSessionScrub`'s request session-scoped
and `ReportPodScrub`'s request pod-scoped, per message, in the §4.7.1 RPC table. Both declarations agree
with the derivation, so nothing becomes false, but the applied specification then asserts a method it does
not follow in one of its own tables. Decide whether the two clauses come out or stand as agreeing
restatements. Two review lenses reached this site and neither filed it, so the loop left it without a
recommendation.

## Defects in the shipped tree that this proposal does not stage

None. Both defects this proposal confirms are staged: the three §4.1 sentences that ground the declared
classification on a counterexample that no longer exists (SPEC-1), and the tier-3 comment stating a
coverage the suite does not have (TEST-2). The §4.1 ground was falsified when proposal 0076's CODE-1
landed and moved the coordination generation onto the slot entry the identifier resolves, so both defects
are live in the specification now rather than becoming defects when this proposal applies.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0073 | Implemented | Retires the §4.1 classification table its SPEC-7 staged and the reconciliation gate its §8 added, replacing both with the derivation rule and a gate over the addressing convention that rule rests on. Its §4.2 value rule and everything it states about how the adapter resolves a root stand. | Nothing. A landed proposal keeps the words it was written with, and every edit here lands in `spec/` and in the tests rather than in that document. |
| 0076 | Implemented | Implements the answer to its OD3. This proposal is the successor Question B names, and SPEC-1 carries the `spec/04` §4.1 edit 0076 left unstaged. | Nothing. 0076 is landed and is not edited. |
| 0080 | Draft | Leaves §1.16 standing. This change moves the classification and retires the table; §1.16 changes the fence's acceptance predicate and touches neither, so the two are independent in either order. §2 of 0080 already records this proposal as taking one of 0073's residues. | Nothing while 0080 remains an inventory. A successor that takes §1.16 states the acceptance predicate and does not restate the classification. |

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
