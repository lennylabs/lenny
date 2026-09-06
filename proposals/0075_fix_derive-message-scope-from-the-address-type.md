# Proposal: Derive message scope from the address type

- **Status:** Draft for review.
- **Date:** 2026-08-19. Rewritten 2026-09-06 against proposal 0076, which removes the ground this
  proposal originally gave for its central exception. §11 records what the rewrite changed.
- **Scope:** Replaces proposal 0073's declared message-scope table with a derivation rule. One request
  message on the gateway-to-adapter contract carries a session identifier the specification classifies as
  a guard rather than an address, and that single message is the whole reason 0073 declares the
  classification in a table and gates the table against the proto. Retiring the table turns 32 rows and a
  gate into a rule the proto can be checked against. Sequences after 0073 and after 0076, and changes
  nothing 0073 states about how the adapter resolves a root.

This document stages the proposed specification, schema, and code changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It records a direction and the measurements behind
it. The design is not settled, the staged edits are indicative rather than final, and the open questions in
§7 are open. Run the change-proposal convergence loop on it before sign-off.

**Two of this proposal's deliverables are conditional on a decision another proposal owns.** Proposal
0076's OD3 asks whether `CoordinatorFenceRequest` is session-scoped after 0076 lands. That answer decides
whether this proposal's exception exists at all, and therefore whether three of its five deliverables have
a subject. §2 D3 states both branches. Do not implement from this text before OD3 is answered.

## Summary

**What changes.** The specification states one derivation rule in place of 0073's classification table, and
the tier-0 gate that reconciles that table against the proto is replaced by a smaller gate. Under one
answer to 0076's OD3 that is the whole change. Under the other, `CoordinatorFenceRequest`'s session field
is additionally renamed and retyped so that the surviving exception is carried by a type rather than by a
table row.

**What is fixed.** Nothing at runtime. This is a classification and documentation change. The defect it
corrects is that 0073 spends 32 rows of specification text and a tier-0 gate accommodating a single
message whose classification disagrees with the rule the other 30 obey.

**Watch out for.** Proposal 0073 is converged and is not reopened by this proposal; every edit here applies
to text 0073 introduces. Proposal 0076 rewrites the fence handler's state and deletes the pod-wide counter
this proposal's earlier revision cited as the ground for its exception, so this proposal is sequenced after
0076 rather than independent of it. The value rule in 0073 §4.2, which is what actually changes adapter
behavior, is untouched.

## Implementation checklist

The lane a step belongs to depends on 0076's OD3. Steps marked **conditional** exist only under a "no".

- [ ] **S0 · gate** — Read 0076's answer to OD3 and record it here. S1, S3, and S5 are dropped under a
      "yes" and the §4.1 row's fate is settled by 0076 rather than here.
      Tiers —. Depends on: proposal 0076 reaching `Approved`
- [ ] **S1 · schema** *(conditional)* — SCHEMA-1. `schemas/lenny-adapter.proto` gains the guard wrapper
      message and `CoordinatorFenceRequest`'s field is renamed and retyped onto it.
      `pkg/proto/adapter/v1/` is regenerated.
      Tiers 0, 3. Depends on: S0
- [ ] **S2 · spec** — SPEC-1. The derivation rule replaces 0073's §4.1 table.
      Tiers 0, 11. Depends on: S0, and S1 where S1 runs
- [ ] **S3 · code** *(conditional)* — CODE-1. The fence handler and its gateway caller read the renamed
      field.
      Tiers 0, 1, 3. Depends on: S1
- [ ] **S4 · test** — TEST-1. The tier-0 table-reconciliation gate 0073 adds is replaced by the rule gate.
      Tiers 0. Depends on: S2
- [ ] **S5 · docs** *(conditional)* — DOCS-1. The two reader-facing pages that name the fence RPC take the
      field rename.
      Tiers 11. Depends on: S1

## 1. Problem

### 1.1 A 32-row table exists to accommodate one message

Proposal 0073 decision D6 states that a request message's scope is declared in the specification rather
than derived from its field set, and stages a table in §4.1 with one row per request message plus a tier-0
gate that reconciles the table against `schemas/lenny-adapter.proto`. 0073's own recorded limits state what
that gate cannot do: it checks that every in-scope message is classified and that the table and the proto
agree on the message set, and it cannot check that a declared scope matches what the handler does.

D6 rests on the derivation rule "a request message is session-scoped exactly when it declares a
`session_id` field" having counterexamples. Parsing the proto's two service blocks for RPC request types
and checking each for a top-level field of that name gives 31 request messages, of which exactly two
disagree with the rule:

| Message | Declared scope | Declares `session_id` | Why it disagrees |
|:--|:--|:--|:--|
| `CoordinatorFenceRequest` | pod | yes | The specification classifies the identifier as a guard |
| `CheckpointRequest` | session | no | Stream envelope; its scope is its opening frame's |

The remaining 29 agree. Every other pod-scoped message declares no session field at all:
`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, and
`AdapterEventsRequest` on `service Adapter`, and `ReportPodScrubRequest` on `service GatewayControl`. The
parse was re-run against the current proto on 2026-09-06 and returns the same 31 messages and the same
six without a session field, so the counts below have not drifted since this proposal was first written.

`CheckpointRequest` (`schemas/lenny-adapter.proto:1166`) is structural rather than a naming accident. It is
a stream envelope carrying a `oneof` of `CheckpointStart`, `CheckpointGrant`, and `CheckpointAbort`, with
`coordination_generation` outside the oneof because the fence applies to every frame on the stream. Its
scope is the scope of the `CheckpointStart` frame that opens it. 0073 handles it with a table row saying
exactly that, and any scheme needs an equivalent clause.

That leaves one message. A table of 32 rows and a new tier-0 gate exist to accommodate one row of it.

### 1.2 The ground the specification gives for that row is being removed

`CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1447`) declares `SessionId session_id = 1` at
`:1448`. §4.1's table declares the message pod-scoped (`spec/04_system-components.md:175`), and the
paragraph under the table grounds that declaration on the message carrying `session_id` and staying
pod-scoped, "which is why the classification is declared rather than derived"
(`spec/04_system-components.md:188`).

In the shipped tree that ground holds. The handler at `pkg/adapter/coordination.go:84` reads the
identifier, verifies the pod is running that session, and then mutates `s.coord`
(`pkg/adapter/server.go:302`), which is a single `coordinationState` for the whole adapter process. The
write target is the pod, and the identifier selects nothing.

Proposal 0076's CODE-1 deletes `Server.coord` and records `lastFenced` and `initialized` on the slot entry
the identifier resolves. After it lands the identifier addresses that entry, and the sentence at `:188`
states a ground that no longer exists. That is not this proposal's finding to act on unilaterally: 0076's
OD3 puts the reclassification to a reviewer, and §2 D3 below takes both answers.

The identifier is load-bearing under either answer, and this proposal does not remove it. Pods are reused
across recycle boundaries, so a coordinator that still believes it coordinates one session must be
prevented from fencing a pod that has since been recycled onto another. The generation number cannot carry
that check by itself, because generations are minted per session, so a value from one session's lineage
says nothing about another's. The check is irreducibly a session-identity comparison. What is at issue is
only whether that comparison is an address resolution, as it is on the other 24 messages that declare the
field, or something the specification must except from the rule.

### 1.3 The blast radius of correcting it is small

`CoordinatorFence` is a gateway-to-adapter RPC. No file under `sdks/` references it, so this is not a
runtime-author-facing contract change; runtime authors speak the JSONL leg. The Go accessor is generated
into `pkg/proto/adapter/v1/`. Two reader-facing pages name the RPC, `docs/reference/adapter-contract.md`
and `docs/reference/metrics.md`. The handler is `pkg/adapter/coordination.go:84` and the caller is
`pkg/gateway/runtime/adapterclient/coordinatorfence.go:52`.

Under a "yes" to 0076's OD3 the radius is smaller still: no proto, code, or docs file is touched at all,
and the change is confined to `spec/04` and one tier-0 gate file.

## 2. Decisions

**D1. The derivation rule is stated once in the specification, with one clause for stream envelopes.** A
request message is session-scoped exactly when it declares a field of the address type, and a stream
envelope takes the scope of the frame that opens it. Two sentences replace 32 rows. Under a "no" to 0076's
OD3 a third clause is needed, stating that a guard field never uses the address type.

**D2. 0073's §4.1 table and its reconciliation gate are retired, and a smaller gate replaces them.** The
new gate checks that the messages the specification names as stream envelopes are the ones the proto
declares as such, and under a "no" that no message declaring a field of the guard type also declares one of
the address type. What the retired gate bought and whether this one buys the same thing is §7's first open
question and the central risk of this proposal.

**D3. Whether the exception exists at all is proposal 0076's OD3 to answer, and this proposal takes both
branches rather than pre-empting it.**

*Under a "yes"* (`CoordinatorFenceRequest` is session-scoped after 0076), the rule acquires no exception.
The message declares the address type and is session-scoped, which is what the rule says. `CheckpointRequest`
remains the only message the rule cannot classify, and the envelope clause already covers it. SCHEMA-1,
CODE-1, and DOCS-1 are dropped, because their only subject was carrying an exception that no longer exists.
0076's own SPEC edits reclassify the §4.1 row and rewrite the declaring sentence; SPEC-1 then deletes the
table those edits leave behind, which is sequencing waste rather than a conflict, and SPEC-1 must be
written against 0076's applied text rather than against today's.

*Under a "no"* (the row stays pod-scoped), the exception survives, and it survives without the ground
§1.2 quotes, because `Server.coord` is gone. The reviewer who answers "no" owes a restated ground, and this
proposal cannot supply one: every argument it has for the identifier being a guard rather than an address
is the pod-reuse argument of §1.2, and that argument describes what an address does on a session-scoped
message. Under a "no" SCHEMA-1, CODE-1, and DOCS-1 are the mechanism that carries the exception, and D4
states why they carry it as a type.

**D4. Under a "no", the guard gets its own wrapper message type rather than only a new field name.** A
rename alone restores the derivation rule for today's proto and leaves it unenforceable: a future author
who adds a pod-scoped message and names its guard `session_id` is silently misclassified, and no gate can
catch it, because under a derivation rule the naming *is* the classification. The address type stays
`SessionId` and the guard takes a distinct wrapper. A working name is `BoundSession bound_session` and
settling the name is §7's second open question. §7's first question asks whether this mechanism answers the
enforcement problem or only appears to.

**D5. 0073 is not reopened.** Every edit here applies to text 0073 introduces. This proposal is inert until
0073 is applied.

**D6. 0076 sequences first.** 0076 is further along, is `Reviewed`, and rewrites both the state this
proposal describes and the classification this proposal depends on. Landing this proposal first would
stage a rule against a handler that is about to change and an exception whose fate is undecided.

## 3. Design overview

The specification loses a table and gains a rule. The tier-0 surface loses one gate and gains a smaller
one. Under a "no" to 0076's OD3 the proto additionally gains one message type and changes one field, and
no adapter behavior changes: the fence handler reads the same value from a differently-named field.
0073's §4.2 value rule, which is what rejects an unaddressed session-scoped request, is untouched under
either branch.

## 4. Detailed design

**IMPLEMENTOR TO FILL THE BLANKS.** This draft states the direction and the constraints. The exact wording
of the specification rule, the final name of the guard type, and the gate's parse strategy are not settled
here and must be derived during convergence.

The derivation rule's stream-envelope clause must name which messages are envelopes in a way the gate can
evaluate. The candidate predicate is that a message declaring a `oneof` whose members are themselves
declared messages, and which is the request type of a streaming RPC, is an envelope. Confirm that this
predicate selects `CheckpointRequest` and nothing else before relying on it. Whether it is mechanically
evaluable at all, or must fall back to a named list that reintroduces a smaller table, is §7's third open
question.

Under a "no", the guard wrapper mirrors `SessionId`'s structure, a single `string value = 1`, so the wire
encoding of the field is unchanged and only the type name and field name differ. Whether that is the right
call, as against a bare `string`, is §7's fourth question: a bare string is simpler but loses the symmetry
with every other identifier on this contract.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 and post-0076 state of each
file.

### SPEC-1. State the rule, retire the table

`spec/04` §4.1: replace the classification table 0073's SPEC-7 stages with the derivation rule of D1.
Retire 0073's recorded limit about the table-reconciliation gate and state the new gate's limit in its
place. Written against whatever 0076 leaves in §4.1, which under a "yes" is a reclassified row and a
rewritten declaring sentence that this edit then removes.

### TEST-1. Replace the gate

The tier-0 file 0073's §8 adds for table reconciliation is replaced by the rule gate of D2.

### SCHEMA-1. Give the guard its own type *(conditional on a "no" to 0076's OD3)*

`schemas/lenny-adapter.proto`: add the guard wrapper message beside `SessionId` at `:589`, and change
`CoordinatorFenceRequest`'s field at `:1448` from `SessionId session_id = 1` to the guard type and name,
keeping field number 1. Rewrite the field's doc comment to state that it is a staleness guard against pod
reuse and not an address. Regenerate `pkg/proto/adapter/v1/`.

### CODE-1. Read the renamed field *(conditional on a "no" to 0076's OD3)*

`pkg/adapter/coordination.go:84` and `pkg/gateway/runtime/adapterclient/coordinatorfence.go:52` read and
write the renamed accessor. This is a compile-caught rename with no behavior change. Both sites are edited
by 0076, so this applies on top of that.

### DOCS-1. The reader-facing pages *(conditional on a "no" to 0076's OD3)*

`docs/reference/adapter-contract.md` and `docs/reference/metrics.md` take the field rename wherever they
name it.

## 6. Non-goals

- **Renaming the RPC or changing its semantics.** Only the field's name and type change, and only under a
  "no".
- **Answering 0076's OD3.** This proposal takes both branches and decides neither. The reclassification of
  §4.1's row and the rewrite of the sentence that grounds it are 0076's edits.
- **The scoping of the coordination generation.** 0076 moves the fence handler's recorded state off the
  pod and onto the slot entry. This proposal depends on that change and does not restate or revisit it.
- **The fence's acceptance predicate.** Whether the pod accepts a re-fence at the recorded generation is
  proposal 0080 §1.16.
- **0073's §4.2 value rule.** Unchanged.
- **A bare-string guard field.** Considered and left open in §7 rather than decided here.

## 7. Open decisions for review

1. **Whether the new gate closes the hole the table's gate closed.** Under a declared table, adding a new
   message fails the gate until a human classifies it, which forces a conscious decision. Under the
   derivation rule the classification is implicit in the type an author picks, and no gate catches a
   pod-scoped message whose author gives its guard the address type in the belief that it is an address.
   The guard type of D4 does not obviously answer this, because the case the gate must catch is exactly
   the one where the author does not know the distinction the type encodes. Establish whether that
   residual is smaller than the table's cost. If it is not, this proposal should be withdrawn.
2. **The guard type's name**, under a "no". `BoundSession` is a working name. It must not collide with a
   term the specification already binds, and it must read as a guard rather than as an address.
3. **Whether the stream-envelope predicate is mechanically evaluable** or must be a named list, which
   would reintroduce a smaller table.
4. **Wrapper or bare string** for the guard field, under a "no".

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** Under a "yes" the tiers reached are 0 (the new gate, build, vet) and
11 (the specification and the reader-facing pages that restate the classification). Under a "no" the change
additionally reaches 3, because a field name changes on a declared message, and 1, which covers the
handler's unchanged behavior across the rename. The specific cases are written during convergence; at
minimum the tier-0 gate must be shown to fail on a proto that violates the rule, and under a "no" the
tier-3 case must pin that the fence still fences after the rename.

## 9. Files touched on application

Under either branch:

- `spec/04_system-components.md`
- The tier-0 gate file 0073's §8 introduces

Under a "no" to 0076's OD3, additionally:

- `schemas/lenny-adapter.proto`
- `pkg/proto/adapter/v1/` (generated)
- `pkg/adapter/coordination.go`
- `pkg/gateway/runtime/adapterclient/coordinatorfence.go`
- `docs/reference/adapter-contract.md`
- `docs/reference/metrics.md`
- Test files that construct `CoordinatorFenceRequest`

## 10. Dependencies

Applies after proposal 0073, whose §4.1 table and gate this proposal retires.

Applies after proposal 0076, and depends on it rather than merely rebasing onto it. 0076's CODE-1 deletes
`Server.coord` and records the generation on the slot entry the identifier resolves, which removes the
ground §4.1's declaring sentence gives for the one exception this proposal exists to accommodate. Its OD3
then decides whether the exception survives, and with it whether three of this proposal's five deliverables
have a subject. An earlier revision of this document called the two proposals independent and the collision
a rebase for whichever landed second; that understated it, because a rebase leaves an argument standing and
this deletes one.

Proposal 0080 §1.16 shares the handler but not the field. It changes the fence's acceptance predicate and
touches neither the classification nor the field's type, so the two are independent in either order.

## 11. What the 2026-09-06 rewrite changed

Recorded so a reader who saw the earlier revision can tell what moved and why. The rewrite was prompted by
proposal 0076's assessment of this document, which is the only place 0076 states anything about another
proposal's validity.

- **The central argument.** The earlier revision grounded its exception on the handler mutating a pod-wide
  `coordinationState`, so that "the identifier selects nothing". 0076's CODE-1 deletes that state. §1.2 now
  states the ground as the specification's rather than the tree's, and states that 0076 removes it.
- **SCHEMA-1, CODE-1, and DOCS-1 became conditional.** 0076 required that they be restated on the pod-reuse
  guard argument alone or dropped. They are neither restated nor dropped outright, because the pod-reuse
  argument describes what an address does; instead D3 makes them conditional on the answer to 0076's OD3,
  and D4 keeps the type argument for the branch where the exception survives.
- **The dependency was corrected.** §10 previously called the proposals independent and the collision a
  rebase.
- **The non-goal was corrected.** The bullet describing the pod-wide counter as 0076's subject described
  state that no longer exists after 0076; it is now a dependency rather than a non-goal.
- **Anchors were refreshed** against the tree on 2026-09-06: `SessionId` at `schemas/lenny-adapter.proto:589`
  rather than `:580`, `CoordinatorFenceRequest` at `:1447-1448` rather than `:1403-1404`,
  `CheckpointRequest` at `:1166` rather than `:1131`, `s.coord` at `pkg/adapter/server.go:302` rather than
  `:304`, and the gateway caller at `pkg/gateway/runtime/adapterclient/coordinatorfence.go:52` rather than
  `:48`. The §1.1 message counts were re-derived from the current proto and are unchanged.
- **§7 lost a question and gained an argument.** The guard type's ability to close the enforcement hole is
  now stated as doubtful within question 1 rather than assumed by D4.
