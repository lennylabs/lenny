# Proposal: Derive message scope from the address type

- **Status:** Draft for review.
- **Date:** 2026-08-19
- **Scope:** Replaces proposal 0073's declared message-scope table with a derivation rule. One request
  message on the gateway-to-adapter contract carries a session identifier that is a staleness guard rather
  than an address, and that single message is the whole reason 0073 declares the classification in a table
  and gates the table against the proto. Giving the guard its own wrapper type makes the classification
  derivable and mechanically checkable, so the table and its gate are retired. Sequences after 0073 and
  changes nothing 0073 states about how the adapter resolves a root.

This document stages the proposed specification, schema, and code changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It records a direction and the measurements behind
it. The design is not settled, the staged edits are indicative rather than final, and the open questions in
§7 are open. Run the change-proposal convergence loop on it before sign-off.

## Summary

**What changes.** `CoordinatorFenceRequest`'s session field is renamed and retyped so that it no longer
looks like an address. The specification states one derivation rule in place of 0073's classification
table, and the tier-0 gate that reconciles that table against the proto is replaced by a smaller gate that
enforces the type rule.

**What is fixed.** Nothing at runtime. This is a classification and documentation change. The defect it
corrects is that `session_id` on a pod-scoped message reads as an address to a human and to any mechanical
rule, and 0073 works around that misreading with 32 rows of specification text rather than correcting it.

**Watch out for.** Proposal 0073 is converged and is not reopened by this proposal. Every edit here applies
to text 0073 introduces, so this proposal cannot be applied before 0073. The value rule in 0073 §4.2, which
is what actually changes adapter behavior, is untouched. The pod-wide coordination generation the fence
handler mutates is a separate defect and is proposal 0076's subject.

## Implementation checklist

- [ ] **S1 · schema** — SCHEMA-1. `schemas/lenny-adapter.proto` gains the guard wrapper message and
      `CoordinatorFenceRequest`'s field is renamed and retyped onto it. `pkg/proto/adapter/v1/` is
      regenerated.
      Tiers 0, 3. Depends on: —
- [ ] **S2 · spec** — SPEC-1. The derivation rule replaces 0073's §4.1 table.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · code** — CODE-1. The fence handler and its gateway caller read the renamed field.
      Tiers 0, 1, 3. Depends on: S1
- [ ] **S4 · test** — TEST-1. The tier-0 table-reconciliation gate 0073 adds is replaced by the type gate.
      Tiers 0. Depends on: S2
- [ ] **S5 · docs** — DOCS-1. The two reader-facing pages that name the fence RPC take the field rename.
      Tiers 11. Depends on: S1

## 1. Problem

### 1.1 A 32-row table exists to accommodate one field name

Proposal 0073 decision D6 states that a request message's scope is declared in the specification rather
than derived from its field set, and stages a table in §4.1 with one row per request message plus a tier-0
gate that reconciles the table against `schemas/lenny-adapter.proto`. 0073's own recorded limits state what
that gate cannot do: it checks that every in-scope message is classified and that the table and the proto
agree on the message set, and it cannot check that a declared scope matches what the handler does.

D6 rests on one counterexample to the derivation rule "a request message is session-scoped exactly when it
declares a `session_id` field". Parsing the proto's two service blocks for RPC request types and checking
each for a top-level field of that name gives 31 request messages, of which exactly two disagree with the
rule:

| Message | Scope | Declares `session_id` | Why it disagrees |
|:--|:--|:--|:--|
| `CoordinatorFenceRequest` | pod | yes | The identifier is a guard rather than an address |
| `CheckpointRequest` | session | no | Stream envelope; its scope is its opening frame's |

The remaining 29 agree. Every other pod-scoped message declares no session field at all:
`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, and
`AdapterEventsRequest` on `service Adapter`, and `ReportPodScrubRequest` on `service GatewayControl`.

`CheckpointRequest` (`schemas/lenny-adapter.proto:1131`) is structural rather than a naming accident. It is
a stream envelope carrying a `oneof` of `CheckpointStart`, `CheckpointGrant`, and `CheckpointAbort`, with
`coordination_generation` outside the oneof because the fence applies to every frame on the stream. Its
scope is the scope of the `CheckpointStart` frame that opens it. 0073 handles it with a table row saying
exactly that, and any scheme needs an equivalent clause.

That leaves one message. A table of 32 rows and a new tier-0 gate exist to accommodate one badly-named
field.

### 1.2 The field is misnamed rather than miscarried

`CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1403`) declares `SessionId session_id = 1` at
`:1404`. The handler at `pkg/adapter/coordination.go:84` reads the identifier, verifies the pod is running
that session, and then mutates `s.coord` (`pkg/adapter/server.go:304`), which is a single
`coordinationState` (`pkg/adapter/coordination.go:25`) for the whole adapter process. The write target is
the pod. The identifier selects nothing.

The identifier is nonetheless load-bearing, and this proposal does not remove it. Pods are reused across
recycle boundaries, so a coordinator that still believes it coordinates one session must be prevented from
fencing a pod that has since been recycled onto another. The generation number cannot carry that check by
itself, because generations are minted per session, so a value from one session's lineage says nothing
about another's. The check is irreducibly a session-identity comparison.

What is wrong is that the guard is spelled with the same name and the same wrapper type
(`schemas/lenny-adapter.proto:580`) as the address field on every session-scoped message. A reader reads it
as an address. A mechanical rule reads it as an address. The proposal's answer in 0073 is to write down
that it is not one, 32 rows at a time.

### 1.3 The blast radius of correcting it is small

`CoordinatorFence` is a gateway-to-adapter RPC. No file under `sdks/` references it, so this is not a
runtime-author-facing contract change; runtime authors speak the JSONL leg. The Go accessor is generated
into `pkg/proto/adapter/v1/`. Two reader-facing pages name the RPC, `docs/reference/adapter-contract.md`
and `docs/reference/metrics.md`. The handler is `pkg/adapter/coordination.go:84` and the caller is
`pkg/gateway/runtime/adapterclient/coordinatorfence.go:48`.

## 2. Decisions

**D1. The guard gets its own wrapper message type, rather than only a new field name.** A rename alone
restores the derivation rule for today's proto and leaves it unenforceable: a future author who adds a
pod-scoped message and names its guard `session_id` is silently misclassified, and no gate can catch it,
because under a derivation rule the naming *is* the classification. Making the distinction a type rather
than a name is what makes the rule mechanically checkable. The address type stays `SessionId`; the guard
takes a distinct wrapper. A working name is `BoundSession bound_session` and settling the name is §7's
first open question.

**D2. The derivation rule is stated once in the specification, with one clause for stream envelopes.** A
request message is session-scoped exactly when it declares a field of the address type; a stream envelope
takes the scope of the frame that opens it; a guard field never uses the address type. Three sentences
replace 32 rows.

**D3. 0073's §4.1 table and its reconciliation gate are retired, and a type gate replaces them.** The new
gate checks that no message declaring a field of the guard type also declares one of the address type, and
that the messages the specification names as stream envelopes are the ones the proto declares as such.
What the retired gate bought and whether this one buys the same thing is §7's second open question and the
central risk of this proposal.

**D4. 0073 is not reopened.** Every edit here applies to text 0073 introduces. This proposal is inert until
0073 is applied.

## 3. Design overview

The proto gains one message type and changes one field. The specification loses a table and gains a rule.
The tier-0 surface loses one gate and gains a smaller one. No adapter behavior changes: the fence handler
reads the same value from a differently-named field, and 0073's §4.2 value rule, which is what rejects an
unaddressed session-scoped request, is untouched.

## 4. Detailed design

**IMPLEMENTOR TO FILL THE BLANKS.** This draft states the direction and the constraints. The exact wording
of the specification rule, the final name of the guard type, and the gate's parse strategy are not settled
here and must be derived during convergence.

The guard wrapper mirrors `SessionId`'s structure, a single `string value = 1`, so the wire encoding of the
field is unchanged and only the type name and field name differ. Whether that is the right call, as
against a bare `string`, is open: a bare string is simpler but loses the symmetry with every other
identifier on this contract.

The derivation rule's stream-envelope clause must name which messages are envelopes in a way the gate can
evaluate. The candidate predicate is that a message declaring a `oneof` whose members are themselves
declared messages, and which is the request type of a streaming RPC, is an envelope. Confirm that this
predicate selects `CheckpointRequest` and nothing else before relying on it.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 state of each file.

### SCHEMA-1. Give the guard its own type

`schemas/lenny-adapter.proto`: add the guard wrapper message beside `SessionId` at `:580`, and change
`CoordinatorFenceRequest`'s field at `:1404` from `SessionId session_id = 1` to the guard type and name,
keeping field number 1. Rewrite the field's doc comment to state that it is a staleness guard against pod
reuse and not an address. Regenerate `pkg/proto/adapter/v1/`.

### SPEC-1. State the rule, retire the table

`spec/04` §4.1: replace the classification table 0073's SPEC-7 stages with the derivation rule of D2.
Retire 0073's recorded limit about the table-reconciliation gate and state the type gate's limit in its
place.

### CODE-1. Read the renamed field

`pkg/adapter/coordination.go:84` and `pkg/gateway/runtime/adapterclient/coordinatorfence.go:48` read and
write the renamed accessor. This is a compile-caught rename with no behavior change.

### TEST-1. Replace the gate

The tier-0 file 0073's §8 adds for table reconciliation is replaced by the type gate of D3.

### DOCS-1. The reader-facing pages

`docs/reference/adapter-contract.md` and `docs/reference/metrics.md` take the field rename wherever they
name it.

## 6. Non-goals

- **Renaming the RPC or changing its semantics.** Only the field's name and type change.
- **The pod-wide coordination generation.** The fence handler mutates one pod-wide counter while the
  specification scopes the generation per session. That is a real defect and it is proposal 0076's subject.
  This proposal describes the handler's behavior only where it must, to justify the guard-versus-address
  distinction.
- **0073's §4.2 value rule.** Unchanged.
- **A bare-string guard field.** Considered and left open in §7 rather than decided here.

## 7. Open decisions for review

1. **The guard type's name.** `BoundSession` is a working name. It must not collide with a term the
   specification already binds, and it must read as a guard rather than as an address.
2. **Whether the type gate closes the hole the table's gate closed.** Under a declared table, adding a new
   message fails the gate until a human classifies it, which forces a conscious decision. Under the
   derivation rule the classification is implicit in the type an author picks. The type gate catches a
   message that declares both types and a mismatched envelope, but it cannot catch a pod-scoped message
   whose author gives its guard the address type in the belief that it is an address. Establish whether
   that residual is smaller than the table's cost. If it is not, this proposal should be withdrawn.
3. **Wrapper or bare string** for the guard field.
4. **Whether the stream-envelope predicate is mechanically evaluable** or must be a named list, which
   would reintroduce a smaller table.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers this change reaches are 0 (the new gate, build, vet), 3 (the
wire contract, since a field name changes on a declared message), and 11 (the two reader-facing pages).
Tier 1 covers the handler's unchanged behavior across the rename. The specific cases are written during
convergence; at minimum the tier-3 case must pin that the fence still fences after the rename, and the
tier-0 gate must be shown to fail on a proto that violates the type rule.

## 9. Files touched on application

- `schemas/lenny-adapter.proto`
- `pkg/proto/adapter/v1/` (generated)
- `spec/04_system-components.md`
- `pkg/adapter/coordination.go`
- `pkg/gateway/runtime/adapterclient/coordinatorfence.go`
- `docs/reference/adapter-contract.md`
- `docs/reference/metrics.md`
- The tier-0 gate file 0073's §8 introduces
- Test files that construct `CoordinatorFenceRequest`

## 10. Dependencies

Applies after proposal 0073. Independent of proposal 0076, which changes the same handler's state but not
this field; if both are applied, whichever lands second rebases onto the first.
