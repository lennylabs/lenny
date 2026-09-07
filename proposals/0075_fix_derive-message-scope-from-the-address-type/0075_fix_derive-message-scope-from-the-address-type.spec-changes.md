## 2. Decisions

**D1. The derivation rule is stated once in the specification, as a predicate over a request message's
field set, a clause that classifies a stream envelope through the frame that addresses it, and the
constraint the derivation rests on.** SPEC-1 stages these paragraphs in place of the classification table
and the prose that grounds it:

> Each request message on the gateway-adapter protocol is either session-scoped or pod-scoped, and the
> classification is derived from the message's field set rather than declared per message. A request
> message either service declares that carries no `oneof` of frames is session-scoped exactly when it
> declares a top-level `session_id` field of type `SessionId`, which is the only way a request on this
> protocol addresses a session. One that declares no such field is pod-scoped. A request message that
> does carry a `oneof` of frames is classified by the paragraph below instead.
>
> A request message that carries its frames in a `oneof` is a stream envelope. An envelope declares no
> address of its own. Exactly one of its frames declares the address; that frame is session-scoped, it
> opens the stream, and the envelope takes that frame's scope, so every frame that follows continues a
> stream that is already addressed.
>
> The derivation is sound only while a request addresses a session in the one way stated above. A tier-0
> gate refuses a protocol definition in which a field named `session_id` is not of type `SessionId`, a
> field of type `SessionId` is not named `session_id`, a stream envelope declares an address of its own,
> or a stream envelope's frames declare other than exactly one address. What the gate cannot see is a
> session addressed under a name and a type that are both unconventional. A request message that does
> that is non-conforming, and the first paragraph is what forbids it.

The derivation reproduces every classification the retired table carried. Of the request messages either
service declares, the ones other than the stream envelope that carry the address are exactly the table's
session rows once `CoordinatorFenceRequest` moves under 0076's OD3, and the ones that carry none are
exactly its pod rows. The envelope clause carries the remaining two rows, classifying `CheckpointRequest`
together with the `CheckpointStart` frame that opens it and declares the address
(`schemas/lenny-adapter.proto:1217`). The field-set predicate decides every request message but the
envelope and the envelope clause decides that one, so no per-message exception clause is needed: after
0076's OD3 no request message carries the address and is pod-scoped.

**D2. 0073's §4.1 table and its reconciliation gate are retired, and a gate over what the rule rests on
replaces them.** What the rule states needs no reconciliation, because it is computed from the protocol
definition itself. What the rule rests on is an addressing convention that nothing checks today, so the
convention is what the replacement gate checks: on every message the protocol declares, a field named
`session_id` is of type `SessionId` and a field of type `SessionId` is named `session_id`, and a request
message carrying its frames in a `oneof` declares no top-level address of its own and exactly one of its
frames declares the address. The clause an earlier revision staged, that no pod-scoped message declares a
field of the address type, is dropped. Under a derived classification a pod-scoped message is one that
declares no address, so that clause reduces to a statement that a message without the field does not
carry the field, and it can never fail. The gate's limit is stated with the rule: a session addressed
under both an unconventional name and an unconventional type is invisible to it.

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
The argument the wrapper was meant to serve is that a future author could name a pod-scoped guard
`session_id` and be silently misclassified, and the wrapper was never a complete answer to it. This is
recorded rather than dropped silently because the wrapper was the earlier revision's central mechanism.

**D5. 0073 is not reopened.** Every edit here applies to text 0073 introduces. This proposal is inert until
0073 is applied.

**D6. 0076 sequences first.** 0076 is Implemented. Its CODE-1 rewrote the fence handler's state, which is
what §1.2 records, and its OD3 answer carries the decision this proposal implements. Landing this proposal
before it would have stated a rule against a handler that was about to change and a classification whose
answer had not yet been applied.

## 3. Design overview

The specification loses a table and gains a rule: a predicate over a request message's field set, a clause
that classifies a stream envelope through the frame that addresses it, and the constraint the derivation
rests on. The tier-0 surface loses the gate that reconciled the table against the protocol definition and
gains a gate over that constraint, which reads the protocol definition alone and checks that the session
address is spelled one way in both directions and that a stream envelope carries exactly one addressing
frame. One tier-3 suite gains a message and loses a false comment. No proto or handler changes, and
0073's §4.2 value rule, which is what rejects an unaddressed session-scoped request, is untouched.

## 4. Detailed design

The stream-envelope clause is a structural predicate rather than a named list, and the predicate is
mechanically evaluable. `CheckpointRequest` and `CheckpointResponse` are the only messages in the protocol
definition that declare a `oneof`, and `CheckpointResponse` is the request type of no RPC, so "a request
message that carries its frames in a `oneof`" selects `CheckpointRequest` and nothing else.

The clause keys on the single frame that declares the address rather than on the envelope's frames
agreeing on a scope, because they do not agree. `CheckpointStart` declares `SessionId session_id = 7`
(`schemas/lenny-adapter.proto:1217`) while `CheckpointGrant` and `CheckpointAbort` declare no address, so
a clause requiring agreement would fail on the protocol definition as it stands. Deriving the envelope's
scope from the frame that addresses it is also what classifies that frame. A clause that took the
envelope's scope from an already-classified opening frame would leave the frame itself unclassified,
because `CheckpointStart` is a `oneof` member rather than the request type of an RPC and the predicate's
own population does not reach it. `spec/05_runtime-registry-and-pool-model.md:515` rejects a session-scoped
request whose session identifier is empty, and `pkg/adapter/checkpoint.go:74-84` enforces that on the
opening frame, so the frame's class carries a refusal and has to be stated rather than inferred.

**IMPLEMENTOR'S CHOICE:** how the tier-0 gate reads field types and `oneof` membership out of the protocol
definition. The parse must be the one the tier-0 gates already share, so that a change to the protocol's
spelling moves every gate that reads it together
(`tests/tier0_static/adapter_proto_parse_test.go:10-15`).

## 5. Proposed changes

### SPEC-1. State the rule, retire the table

`spec/04_system-components.md`, under `#### Request Message Scope`: replace the whole block 0073's SPEC-7
stages, which is the paragraph introducing the declared table (`:151`), the table itself (`:153-186`), and
the paragraph grounding the fence's classification (`:188`), with the paragraphs D1 states. Replacing the
block rather than the individual sentences named in §1.2 is what also removes the clause at `:151` that
classifies `CheckpointRequest` for the scope of its `CheckpointStart` frame and gives that frame a row of
its own, which the envelope clause restates without naming either message. The `ShutdownRequest` paragraph
at `:190` stands unedited, because it explains a divergence between what a request addresses and what its
handler touches that the rule does not state and D3 rests on. The replacement block names no request
message and states nothing about the coordinator hold. That a `CoordinatorFence` is the only way out of
hold state is stated at `spec/10_gateway-internals.md:57`, and that the hold and its gauge stay pod-scoped
at `:60`, which proposal 0076's SPEC-1 landed under its D5. Both sentences sit in §10.1.4, the section
that owns hold state, so §4.1 restates neither. The sentence at `:188` that could have hosted a
restatement is retired with the table, and a paragraph naming one message's pod-wide side effect would
put back the per-message prose the rule replaces. The block D1 states carries the constraint the
derivation rests on together with what the gate cannot see, which is where the specification records
the new gate's reach. `spec/` states no limit for the retired gate: 0073 records that limit in the gate
file's header comment (`tests/tier0_static/adapter_proto_message_scope_test.go:17-27`), and it goes with
the gate TEST-1 replaces.

The two per-message scope statements in §4.7.1 stand unedited. `spec/04_system-components.md:725` states
that `ReportSessionScrub`'s request is session-scoped and is addressed by the identifier of the released
session, and `:726` states that `ReportPodScrub`'s request is pod-scoped. Both agree with the derivation
D1 states: `ReportSessionScrubRequest` declares `SessionId session_id = 2`
(`schemas/lenny-adapter.proto:458`), and `ReportPodScrubRequest` declares `string pod_id`,
`PodScrubOutcome outcome`, and `string detail`, with no field of the address type
(`schemas/lenny-adapter.proto:499-503`). The first sentence also states what the request addresses, which
the class word alone does not carry, and a tier-11 gate holds its wording on both the specification and
`docs/reference/adapter-contract.md:81`
(`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76`), so editing it
would move a reader-facing document and a gate that this proposal otherwise leaves alone. Retiring the
table also leaves `:726` as the only sentence in `spec/` that classifies one request message pod-scoped.
Written against whatever 0076 leaves in §4.1.

## 6. Non-goals

- **Renaming the RPC, its field, or its field's type.** Nothing on the wire changes.
- **Re-deriving 0076's OD3.** The classification is settled and this proposal implements it.
- **The scoping of the coordination generation.** 0076 moves the fence handler's recorded state off the pod
  and onto the slot entry. This proposal depends on that change and does not restate or revisit it.
- **The fence's acceptance predicate.** Whether the pod accepts a re-fence at the recorded generation is
  proposal 0080 §1.16.
- **0073's §4.2 value rule.** Unchanged.

## 7. Open decisions for review

1. **What the retired-field-number column holds** for `CoordinatorFenceRequest` in TEST-2, which never
   declared the field the column records.

## 9. Files touched on application

- `spec/04_system-components.md`
- `tests/tier0_static/adapter_proto_message_scope_test.go`, which is the tier-0 gate file 0073's §8
  introduces and also carries the table-cell class reader that goes with the table
- `tests/tier0_static/adapter_proto_parse_test.go`, the parse the tier-0 gates share, which carries
  neither a field's type nor its `oneof` membership today
- `tests/tier0_static/claim_register_proto_agreement_test.go`, the parse's other caller (`:64`), which
  moves with any change to the signature it reads
- `tests/spec-map.json`, which credits the retiring gate's cases and is re-pointed at the replacement
  gate's cases in the same change
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
