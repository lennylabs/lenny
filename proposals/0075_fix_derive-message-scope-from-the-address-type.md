# Proposal: Derive message scope from the address type

- **Status:** Draft for review.
- **Date:** 2026-08-19. Rewritten 2026-09-06 against proposal 0076, which removes the ground this proposal
  originally gave for its central exception, and again once 0076's OD3 was answered. §11 records what the
  rewrites changed.
- **Scope:** Replaces proposal 0073's declared message-scope table with a derivation rule. One request
  message on the gateway-to-adapter contract carried a session identifier the specification classified as
  a guard rather than an address, and that single message is the whole reason 0073 declares the
  classification in a table and gates the table against the proto. The reviewer's answer to proposal
  0076's OD3 reclassifies that message as session-scoped, which leaves the rule without a counterexample,
  so retiring the table turns 32 rows and a gate into a rule the proto can be checked against. Sequences
  after 0073 and after 0076, and changes nothing 0073 states about how the adapter resolves a root.

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

**What changes.** The specification states one derivation rule in place of 0073's classification table, and
the tier-0 gate that reconciles that table against the proto is replaced by a smaller gate. A tier-3
comment that claims the fence's address is pinned in its file is corrected in the same change, because the
comment's membership rule is the table this proposal retires. No proto, code, or documentation file is
touched.

**What is fixed.** Nothing at runtime. This is a classification change. The defect it corrects is that 0073
spends 32 rows of specification text and a tier-0 gate accommodating a single message whose classification
disagreed with the rule the other 30 obey, and whose classification 0076's OD3 has since changed to agree
with it.

**Watch out for.** Proposal 0073 is converged and is not reopened; every edit here applies to text 0073
introduces. Proposal 0076 rewrites the fence handler's state, and its OD3 answer settles the
classification this proposal derives, so this proposal is sequenced after 0076 rather than independent of
it, and its spec edits are written against 0076's applied text. The value rule in 0073 §4.2, which is what
actually changes adapter behavior, is untouched.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The derivation rule replaces 0073's §4.1 table, and the three sentences that
      ground the declared classification are rewritten or removed with it.
      Tiers 0, 11. Depends on: proposals 0073 and 0076 applied
- [ ] **S2 · test** — TEST-1. The tier-0 table-reconciliation gate 0073 adds is replaced by the rule gate.
      Tiers 0. Depends on: S1
- [ ] **S3 · test** — TEST-2. The tier-3 session-address suite covers `CoordinatorFenceRequest` and its
      false coverage clause is removed.
      Tiers 3. Depends on: S1

## 1. Problem

### 1.1 A 32-row table exists to accommodate one message

Proposal 0073 decision D6 states that a request message's scope is declared in the specification rather
than derived from its field set, and stages a table in §4.1 with one row per request message plus a tier-0
gate that reconciles the table against `schemas/lenny-adapter.proto`. 0073's own recorded limits state what
that gate cannot do: it checks that every in-scope message is classified and that the table and the proto
agree on the message set, and it cannot check that a declared scope matches what the handler does.

D6 rests on the derivation rule "a request message is session-scoped exactly when it declares a
`session_id` field" having counterexamples. Parsing the proto's two service blocks for RPC request types
and checking each for a top-level field of that name gives 31 request messages. The parse was re-run
against the current proto on 2026-09-06 and returns 25 messages declaring the field and six not declaring
it. Two disagreed with the rule when 0073 was written:

| Message | Declared scope | Declares `session_id` | Status |
|:--|:--|:--|:--|
| `CoordinatorFenceRequest` | pod | yes | Reclassified session-scoped by proposal 0076's OD3; no longer disagrees |
| `CheckpointRequest` | session | no | Stream envelope; its scope is its opening frame's |

The remaining 29 always agreed. Every other pod-scoped message declares no session field at all:
`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, and
`AdapterEventsRequest` on `service Adapter`, and `ReportPodScrubRequest` on `service GatewayControl`.

`CheckpointRequest` (`schemas/lenny-adapter.proto:1166`) is structural rather than a naming accident. It is
a stream envelope carrying a `oneof` of `CheckpointStart`, `CheckpointGrant`, and `CheckpointAbort`, with
`coordination_generation` outside the oneof because the fence applies to every frame on the stream. Its
scope is the scope of the `CheckpointStart` frame that opens it. 0073 handles it with a table row saying
exactly that, and any scheme needs an equivalent clause.

After 0076's OD3, one clause covers everything the table covered. A table of 32 rows and a tier-0 gate
remain in the specification with nothing left to accommodate.

### 1.2 The ground the specification gives for the table has been removed

`CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1447`) declares `SessionId session_id = 1` at
`:1448`. Three sentences in §4.1 ground the declared classification on that message:

- `spec/04_system-components.md:151` states that the classification is declared rather than derived
  "because `session_id` appears on messages of both classes".
- `:175` is the table row declaring the message pod-scoped.
- `:188` states that the message "carries `session_id` and stays pod-scoped, which is why the
  classification is declared rather than derived".

In the shipped tree that ground holds. The handler at `pkg/adapter/coordination.go:84` reads the
identifier, verifies the pod is running that session, and then mutates `s.coord`
(`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process. The
write target is the pod, and the identifier selects nothing.

Proposal 0076's CODE-1 deletes `Server.coord` and records `lastFenced` and `initialized` on the slot entry
the identifier resolves. After it lands the identifier addresses that entry, which is what §4.1 treats as
an address, and the reviewer's answer to 0076's OD3 reclassifies the row accordingly. `session_id` no
longer appears on messages of both classes, so `:151`'s stated reason is false, `:175` is wrong, and
`:188` grounds a classification that has changed. The precedent for classifying by what a request
addresses rather than by how broad its handler's effect is already sits two lines below, at `:190`, where
`ShutdownRequest` is session-scoped although its handler runs the whole-pod scrub.

The identifier remains load-bearing and this proposal does not remove it. Pods are reused across recycle
boundaries, so a coordinator that still believes it coordinates one session must be prevented from fencing
a pod that has since been recycled onto another. That guard is what an address does on this contract: it
resolves the entry the handler writes, and resolving nothing is the refusal.

### 1.3 A tier-3 comment states a coverage the suite does not have

The comment above `sessionScopedMessages` in the session-address contract suite places
`CoordinatorFenceRequest` outside that map and states that it and the two messages beside it "are covered
by the session-address arm below alone"
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40-43`). No arm covers it. The
three assertions that reach a message by name iterate `sessionScopedMessages` (`:81`, `:102`, `:130`),
which the comment's own exclusion keeps the fence out of, and the fourth walks the file for the retired
wrapper type and names no request at all (`:150-158`).

The exclusion was correct while the fence was pod-scoped, and the comment's membership rule is §4.1's
table. This proposal retires that table and the OD3 answer moves the message into the session-scoped class,
so the exclusion becomes wrong in the same change that removes its basis. Repairing the comment is
therefore an edit site of this proposal rather than a separate defect, and TEST-2 carries it.

### 1.4 The blast radius is small

`CoordinatorFence` is a gateway-to-adapter RPC. No file under `sdks/` references it, so this is not a
runtime-author-facing contract change; runtime authors speak the JSONL leg. Nothing here renames a field,
so no generated code, no handler, and no reader-facing page changes: the edits are confined to
`spec/04_system-components.md`, the tier-0 gate file 0073 introduces, and one tier-3 suite.

## 2. Decisions

**D1. The derivation rule is stated once in the specification, with one clause for stream envelopes.** A
request message is session-scoped exactly when it declares a field of the address type, and a stream
envelope takes the scope of the frame that opens it. Two sentences replace 32 rows. No third clause is
needed, because after 0076's OD3 no message declares the address type and is pod-scoped.

**D2. 0073's §4.1 table and its reconciliation gate are retired, and a smaller gate replaces them.** The
new gate checks that the messages the specification names as stream envelopes are the ones the proto
declares as such, and that no pod-scoped message declares a field of the address type. What the retired
gate bought and whether this one buys the same thing is §7's first open question and the central risk of
this proposal.

**D3. The message is session-scoped, and this proposal does not re-derive that.** Proposal 0076's OD3
Question A settled it, on the ground that after CODE-1 the identifier selects the entry the fence writes
and on the `ShutdownRequest` precedent at `spec/04_system-components.md:190`. Question B assigned the
`spec/04` §4.1 edit to a successor rather than staging it in 0076, so SPEC-1 carries all three sites named
in §1.2 as part of retiring the table. The alternative 0076 weighed and rejected, leaving the row
pod-scoped because accepting a fence is the only exit from the pod-scoped hold state, is recorded there
rather than reopened here.

**D4. No guard wrapper type is introduced.** An earlier revision of this proposal gave
`CoordinatorFenceRequest`'s identifier its own wrapper message so that the surviving exception would be
carried by a type rather than by a table row. With the exception gone, that deliverable has no subject:
the field is an address, and giving an address a guard type would state the opposite of what OD3 decided.
The argument the wrapper was meant to serve, that a future author could name a pod-scoped guard
`session_id` and be silently misclassified, survives as §7's first question, and the wrapper was never a
complete answer to it. This is recorded rather than dropped silently because the wrapper was the earlier
revision's central mechanism.

**D5. 0073 is not reopened.** Every edit here applies to text 0073 introduces. This proposal is inert until
0073 is applied.

**D6. 0076 sequences first.** 0076 is further along, rewrites the state §1.2 describes, and carries the
decision this proposal implements. Landing this proposal first would state a rule against a handler that
is about to change and a classification whose answer had not yet been applied.

## 3. Design overview

The specification loses a table and gains a rule. The tier-0 surface loses one gate and gains a smaller
one. One tier-3 suite gains a message and loses a false comment. No proto or handler changes, and 0073's
§4.2 value rule, which is what rejects an unaddressed session-scoped request, is untouched.

## 4. Detailed design

**IMPLEMENTOR TO FILL THE BLANKS.** This draft states the direction and the constraints. The exact wording
of the specification rule and the gate's parse strategy are not settled here and must be derived during
convergence.

The derivation rule's stream-envelope clause must name which messages are envelopes in a way the gate can
evaluate. The candidate predicate is that a message declaring a `oneof` whose members are themselves
declared messages, and which is the request type of a streaming RPC, is an envelope. Confirm that this
predicate selects `CheckpointRequest` and nothing else before relying on it. Whether it is mechanically
evaluable at all, or must fall back to a named list that reintroduces a smaller table, is §7's second
question.

TEST-2 has one open point of its own. `sessionScopedMessages` records each member's retired field number
alongside its name, and `CoordinatorFenceRequest` never carried the duplicate address the retirement
removed: the commit that introduced it put the field on `InterruptRequest`, `SignalDeadlineRequest`,
`ResumeRequest`, `CheckpointBarrierRequest`, and `ReportUsageRequest` alone (`01d19af01`). Adding the fence
to the map therefore requires deciding what that column holds for a message with no retired number, rather
than copying a neighbouring row.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 and post-0076 state of each
file.

### SPEC-1. State the rule, retire the table

`spec/04` §4.1: replace the classification table 0073's SPEC-7 stages with the derivation rule of D1, and
rewrite or remove the three grounding sentences of §1.2 with it. Retire 0073's recorded limit about the
table-reconciliation gate and state the new gate's limit in its place. Written against whatever 0076
leaves in §4.1.

### TEST-1. Replace the tier-0 gate

The tier-0 file 0073's §8 adds for table reconciliation is replaced by the rule gate of D2. The existing
scope test that accepts either class word in the table's cell
(`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`) loses its subject with the table and is
retired with it.

### TEST-2. Cover the fence in the tier-3 session-address suite

`tests/tier3_contract/adapter_session_address/session_address_wire_test.go`: add
`CoordinatorFenceRequest` to `sessionScopedMessages`, settling the retired-field-number column per §4, and
delete the coverage clause at `:40-43` that claims the fence is pinned by an arm that does not reach it.

## 6. Non-goals

- **Renaming the RPC, its field, or its field's type.** Nothing on the wire changes.
- **Re-deriving 0076's OD3.** The classification is settled and this proposal implements it.
- **The scoping of the coordination generation.** 0076 moves the fence handler's recorded state off the pod
  and onto the slot entry. This proposal depends on that change and does not restate or revisit it.
- **The fence's acceptance predicate.** Whether the pod accepts a re-fence at the recorded generation is
  proposal 0080 §1.16.
- **0073's §4.2 value rule.** Unchanged.

## 7. Open decisions for review

1. **Whether the new gate closes the hole the table's gate closed.** Under a declared table, adding a new
   message fails the gate until a human classifies it, which forces a conscious decision. Under the
   derivation rule the classification is implicit in the type an author picks, and no gate catches a
   pod-scoped message whose author gives its guard the address type in the belief that it is an address.
   D2's clause that no pod-scoped message may declare the address type catches that only once the
   specification says the message is pod-scoped, which is the statement the author has already got wrong.
   Establish whether that residual is smaller than the table's cost. If it is not, this proposal should be
   withdrawn.
2. **Whether the stream-envelope predicate is mechanically evaluable** or must be a named list, which
   would reintroduce a smaller table.
3. **What the retired-field-number column holds** for `CoordinatorFenceRequest` in TEST-2, which never
   declared the field the column records.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers reached are 0 (the new gate, build, vet), 3 (the
session-address suite TEST-2 amends), and 11 (the specification and any reader-facing page that restates
the classification). The specific cases are written during convergence; at minimum the tier-0 gate must be
shown to fail on a proto that violates the rule, and the tier-3 case must pin the fence's address the way
it pins every other session-scoped message's.

## 9. Files touched on application

- `spec/04_system-components.md`
- The tier-0 gate file 0073's §8 introduces
- `tests/tier0_static/adapter_proto_message_scope_test.go`
- `tests/tier3_contract/adapter_session_address/session_address_wire_test.go`

## 10. Dependencies

Applies after proposal 0073, whose §4.1 table and gate this proposal retires.

Applies after proposal 0076, and depends on it rather than merely rebasing onto it. 0076's CODE-1 deletes
`Server.coord` and records the generation on the slot entry the identifier resolves, which removes the
ground §4.1's sentences give for the one exception this proposal existed to accommodate, and 0076's OD3
then reclassifies the row and assigns the resulting `spec/04` edit to this proposal. An earlier revision
called the two proposals independent and the collision a rebase for whichever landed second; that
understated it, because a rebase leaves an argument standing and this deletes one.

Proposal 0080 §1.16 shares the handler but not the classification. It changes the fence's acceptance
predicate and touches neither the table nor the field's type, so the two are independent in either order.

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
