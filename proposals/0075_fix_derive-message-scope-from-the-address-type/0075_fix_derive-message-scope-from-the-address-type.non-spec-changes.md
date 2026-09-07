## 4. Detailed design

TEST-2 works under a constraint the tier-3 file's own structure imposes.
`TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates `sessionScopedMessages` and asserts, for
every member, both `reservesNumber(md, num)` and `reservesName(md, "slot_id")`
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:130-141`).
`CoordinatorFenceRequest` never carried the duplicate address the retirement removed: it declares
`SessionId session_id = 1` and `int64 coordination_generation = 2` with no `reserved` number and no
`reserved "slot_id"` (`schemas/lenny-adapter.proto:1455-1461`), and the retirement commit that added those
`reserved` pairs to every message that did carry the duplicate left the fence untouched (`040323634`). The
name half therefore fails whatever number is written into the map's number column, and no proto edit
satisfies it: reserving a number and the name `slot_id` on a message that never carried the duplicate
would record a removal that did not happen. The column has no satisfying value for this message.

The map is doing two jobs, and its working membership rule is that the message carried the retired
duplicate rather than that the message is session-scoped. Its members are exactly the messages that declare
`reserved "slot_id"` in the protocol definition, while `CoordinatorFenceRequest`, `ExportPathsRequest`,
`ConfigureWorkspaceRequest`, `CallConnectorToolRequest`, `CallPlatformToolRequest`,
`ListConnectorToolsRequest`, `ListPlatformToolsRequest`, and `ListSessionConnectorsRequest` each declare a
top-level `SessionId session_id` and sit outside it. TEST-2 therefore separates the two populations, and
the fence enters the suite through the set the reservation case does not iterate.

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

The replacement gate keeps the file path `tests/tier0_static/adapter_proto_message_scope_test.go`, and
TEST-1 rewrites that file in place. The path is read outside the file. `slotAddressCaseFiles` names it as a
literal string (`tests/tier0_static/spec_map_slot_address_registration_test.go:336`), five tier-0 cases
range over that list (`:973`, `:1028`, `:1054`, `:1096`, `:1110`), and four of them resolve each entry
through `repoFileLines` (`:699-706`), which fails the case on a file it cannot read. The fifth (`:1110`)
reads no file and checks inventory membership alone. Keeping the path leaves that inventory correct and
confines the change to the files §9 of the staged spec changes lists.

`tests/spec-map.json` credits `TestAdapterProtoRequestMessagesAreClassifiedByScope` and
`TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage` under section 4.1 (`:156`, `:169`) and the
first again under section 28.5.3 (`:5670`). The replacement gate's cases are registered under the same
sections in the same change, so `validate-maps` finds no dangling `path::Test` entry and every case in the
file stays credited to each section its own `// spec:` annotation names.

### TEST-2. Separate the two populations in the tier-3 session-address suite

`tests/tier3_contract/adapter_session_address/session_address_wire_test.go` holds one declaration that
serves as both the session-scoped population and the table of retired duplicate-address field numbers. The
two are split.

`sessionScopedMessages` becomes a `[]string` naming every message the derivation rule addresses to a
session directly, which is its members today together with `CoordinatorFenceRequest`, `ExportPathsRequest`,
`ConfigureWorkspaceRequest`, `CallConnectorToolRequest`, `CallPlatformToolRequest`,
`ListConnectorToolsRequest`, `ListPlatformToolsRequest`, and `ListSessionConnectorsRequest`. Each of those
declares a top-level `SessionId session_id`. `CheckpointRequest` is not a member, and the declaration's
comment names it as the one exception: the envelope declares no address of its own, and `CheckpointStart`,
which is already a member, carries the address in its place.

`retiredDuplicateNumbers`, a new `map[string]protoreflect.FieldNumber`, keeps today's name-to-number
entries verbatim. Its members are the messages that declare `reserved "slot_id"` in the protocol
definition, and the set is closed: the retirement landed with proposal 0073 and no later message joins it.

The widened membership is what the split leaves. `retiredDuplicateNumbers` takes the population whose rule
is that the message carried the retired duplicate, so the session set's rule is the derivation rule SPEC-1
states, and the set holds every message that rule addresses to a session. Both address arms are green on
the shipped protocol definition for each added member: `CallPlatformToolRequest`
(`schemas/lenny-adapter.proto:364-368`), `ListPlatformToolsRequest` (`:341-343`),
`ListSessionConnectorsRequest` (`:383-385`), `ListConnectorToolsRequest` (`:403-406`),
`CallConnectorToolRequest` (`:419-424`), `CoordinatorFenceRequest` (`:1455-1461`), `ExportPathsRequest`
(`:1538-1549`), and `ConfigureWorkspaceRequest` (`:1673-1684`) each declare `SessionId session_id = 1` at
the top level, and none of them declares a field named `slot_id`.

`TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1` and
`TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` iterate `sessionScopedMessages` (their loops
at `:81` and `:102`). `TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4` iterates
`retiredDuplicateNumbers` alone (`:130`), so the population it asserts over is unchanged.
`TestTheRetiredAddressWrapperIsGone_spec_15_4` (`:150-158`) reads neither declaration and is untouched. No
test function in the file is renamed and none is added, so the whole-file credits `tests/spec-map.json`
carries for it stay valid, the entry naming `TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1` in
the `addressRuleCases` inventory (`tests/tier0_static/address_rule_citation_test.go:45-47`) stays valid,
and TEST-2 registers nothing.

The declaration's comment (`:37-43`) is rewritten. Membership in `sessionScopedMessages` is the derivation
rule, the retired numbers and the rule that they stay reserved belong to the second declaration, and the
clause stating that the messages outside the map "are covered by the session-address arm below alone" goes,
because the two address arms now reach them.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers reached are 0 (the new gate, build, vet), 3 (the
session-address suite TEST-2 amends), and 11 (the specification and any reader-facing page that restates
the classification). The specific cases are written during convergence; the tier-0 gate must be shown
to fail once for each clause it carries, on a field named `session_id` that is not of type `SessionId`, on
a field of type `SessionId` under another name, on a stream envelope declaring a top-level address of its
own, and on a stream envelope whose frames declare zero addresses or two, and the tier-3 case must pin the
fence's address the way it pins every other session-scoped message's.
