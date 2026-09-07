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

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 and post-0076 state of each
file.

### SPEC-1. State the rule, retire the table

`spec/04` §4.1: replace the classification table 0073's SPEC-7 stages with the derivation rule of D1, and
rewrite or remove the three grounding sentences of §1.2 with it. Retire 0073's recorded limit about the
table-reconciliation gate and state the new gate's limit in its place. Written against whatever 0076
leaves in §4.1.

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
