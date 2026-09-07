# Problem: Derive message scope from the address type

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
