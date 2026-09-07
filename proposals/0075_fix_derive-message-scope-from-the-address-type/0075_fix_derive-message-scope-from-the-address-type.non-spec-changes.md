## 4. Detailed design

TEST-2 has one open point of its own. `sessionScopedMessages` records each member's retired field number
alongside its name, and `CoordinatorFenceRequest` never carried the duplicate address the retirement
removed: the message declares `SessionId session_id = 1` and `int64 coordination_generation = 2` with no
`reserved` number and no `reserved "slot_id"` (`schemas/lenny-adapter.proto:1455-1461`), and the retirement
commit that added those `reserved` pairs to every message that did carry the duplicate left the fence
untouched (`040323634`). Adding the fence to the map therefore requires deciding what that column holds
for a message with no retired number, rather than copying a neighbouring row.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 and post-0076 state of each
file.

### TEST-1. Replace the tier-0 gate

The tier-0 file 0073's §8 adds for table reconciliation
(`tests/tier0_static/adapter_proto_message_scope_test.go`) is replaced by the rule gate of D2. The two readers
of the table in that same file, the class-word reader for the table's cell (`declaredScope`, `:75-81`) and
the table parse itself (`parseMessageScopeTable`, `:54`), lose their subject with the table and are retired
with it. The gate reads a field's type and its `oneof` membership
through the parse the tier-0 gates share (`tests/tier0_static/adapter_proto_parse_test.go`), which carries
neither today, so that parse is extended under the constraint §4 of the staged spec changes states, and
its other caller (`tests/tier0_static/claim_register_proto_agreement_test.go:64`) moves with any change to
the signature it reads. That parse's own package doc (`:10-15`) and the comment on `protoServiceRequests`
(`:64-67`) each describe their subject as the §4.1 classification table, which SPEC-1 retires, so both are
restated as the addressing-convention gate in the same change.

`tests/spec-map.json` credits `TestAdapterProtoRequestMessagesAreClassifiedByScope` and
`TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under section 4.1 (`:156`, `:169`) and the
first again under section 28.5.3 (`:5670`). The replacement gate's cases are registered under the same
sections in the same change, so `validate-maps` finds no dangling `path::Test` entry and every case in the
file stays credited to each section its own `// spec:` annotation names.

### TEST-2. Cover the fence in the tier-3 session-address suite

`tests/tier3_contract/adapter_session_address/session_address_wire_test.go`: add
`CoordinatorFenceRequest` to `sessionScopedMessages`, settling the retired-field-number column per §4, and
delete the clauses at `:39-43`, which are the membership sentence keying on the retired §4.1 table and the
clause claiming the fence is pinned by an arm that does not reach it, restating membership as the
derivation rule together with the message having carried the retired duplicate.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers reached are 0 (the new gate, build, vet), 3 (the
session-address suite TEST-2 amends), and 11 (the specification and any reader-facing page that restates
the classification). The specific cases are written during convergence; the tier-0 gate must be shown
to fail once for each clause it carries, on a field named `session_id` that is not of type `SessionId`, on
a field of type `SessionId` under another name, on a stream envelope declaring a top-level address of its
own, and on a stream envelope whose frames declare zero addresses or two, and the tier-3 case must pin the
fence's address the way it pins every other session-scoped message's.
