# Proposal: Address a tracing frame by the stream that carries it

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Supersedes proposal 0068. Keeps its specification move, which removes from `spec/15` §15.4.1
  the `CH-MSGSOCK` contract §28.5.3 owns, and replaces its frame-addressing design. 0068 taught the
  adapter its pod's concurrency through a new CRD field and a launch argument; the adapter already holds
  a more precise fact, so this proposal resolves the frame against the Attach stream that delivered it and
  stages no CRD change, no launch argument, and no propagation chain.

This document stages the proposed specification and code changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

0068 converged twice and failed implementation once. The failure is the useful part, and §1.3 records it.
An abandoned attempt is preserved at git tag `attempt/0068-spec2-implementation`; nothing in it should be
restored.

## 1. Problem

### 1.1 §15.4.1 duplicates the contract §28.5.3 owns

`spec/15_external-api-surface.md` §15.4.1 restates the stdin/stdout framing, the inbound and outbound
message tables, and the `slotId` multiplexing rule. `spec/28_communication-channels.md` §28.5.3 states
each of those, in more detail, and claims at `spec/28_communication-channels.md:583` to own "the schema of
each `CH-MSGSOCK` message". The duplication is what proposal 0064's reduction existed to remove and did
not finish removing.

Two contradictions already sit in `spec/15` independently of this proposal:
`spec/15_external-api-surface.md:1463` says the JSONL schema covers all nine `CH-MSGSOCK` message types,
and `spec/15_external-api-surface.md:1702-1707` lists eight. §28.5.3 defines eight schema blocks while its
Messages axis at `spec/28_communication-channels.md:539-541` enumerates nine. `set_tracing_context` is the
missing ninth in both counts.

### 1.2 One untagged frame writes every session's row

`pkg/adapter/socketruntime.go:340` hands every Attach subscriber the same unfiltered stream:
`SocketRuntimeProcess.Output` ignores its session-id argument, and `fanOut`/`broadcast`
(`socketruntime.go:237-256`) deliver every line to every subscriber. Each Attach stream then filters for
itself in `demuxSlotOutput` (`pkg/adapter/attach.go:275-303`), whose predicate at `:289` is

    if fs := frameSlotID(line); fs != "" && fs != slotID { continue }

so a frame carrying **no** `slotId` is delivered to every slot's stream deliberately, because
`heartbeat_ack` is per-connection and each slot's monitor must observe it. `attach.go:114-115` then calls
`handleSetTracingContext(ctx, sessionID, line)` with that stream's own session, and
`pkg/adapter/tracingcontext.go:36-62` registers the identifiers against it.

On a pod serving four slots, one untagged `set_tracing_context` frame therefore writes four session rows
and merges one runtime's tracing identifiers into every sibling's delegation lease. The pass-through is
correct for an idempotent `heartbeat_ack` and wrong for a frame with per-session side effects.

The blast radius is larger than advisory metadata. `pkg/delegation/tracing` merges without overwriting and
the tree has no delete path, so a wrongly written entry is permanent, and a session whose 32-entry bound
is exhausted rejects every later registration on itself and on its children.

### 1.3 What the abandoned attempt proves

The attempt asked one question — which session does this frame name — and answered it six mutually
exclusive ways, three of them twice, terminating on a strategy it had itself refuted eleven commits
earlier. Roughly a third of its commits repair regressions it introduced: a pod-layout record built to
answer the question began refusing §6.4 slot claims and bricked pods, and a per-claim wire field began
refusing `Resume` and `ConfigureWorkspace`.

The pattern is not carelessness. Every strategy tried to *resolve* the frame's target from state that
varies over time — occupancy, claim order, a latch, a live slot table — and each died to that state
moving: a registry that empties at teardown while streams still drain, a latch unset before the first
claim, a layout committed before admission. **Resolving a name from time-varying state can route a frame
to the wrong session. Rejecting a name using it can only drop a frame.** That distinction is the design
below.

## 2. Decisions

1. **The Attach stream is the address.** The adapter resolves the frame against the stream that delivered
   it, not against the pod's configuration. A stream is bound to `(sessionID, slotID)` at Attach, and
   `checkSlotSession` (`pkg/adapter/slotsession.go:116-128`) validated that binding at bind time.

2. **The rule is two conditions, and only the first resolves.** Address equality names the session;
   live-binding confirmation may only reject. Condition 2 reads state that varies over time, and §1.3 is
   the reason it is confined to rejection.

3. **No CRD field, no launch argument, no propagation chain.** The adapter never needs its pod's declared
   concurrency to decide this frame, because the stream's own `slotID` already carries what "untagged"
   means there. §4.1 states the derivation. 0068's chain — a `SessionPolicy.MaxConcurrentSessions` CRD
   field, regenerated deepcopy and manifests, an extended `sessionPolicyToCRD` mirror, an `Inputs` field,
   a `--max-concurrent-sessions` flag on two deployment models, changes to two embedded reference
   runtimes, and a three-valued undeclared sentinel — is not staged.

4. **A `slotId` on a single-session pod is dropped.** No session on such a pod holds a slot id
   (`pkg/gateway/sessionserver/start.go:2111`), and the adapter stamps `slotId` only on concurrent slots
   (`pkg/adapter/slotframe.go:33`), so a conforming runtime there has no value to echo. Accepting the
   frame is exactly the case that would force the adapter to know its own mode. 0068 accepted it; this
   proposal does not, and §5 tests the difference.

5. **The specification states a non-guarantee.** One runtime process serves every slot
   (`pkg/adapter/slotsession.go:19-22`), so the `slotId` is whatever that process stamped. The rule
   removes ambiguity between slots; it cannot detect misaddressing by the process itself. §28.5.3 must not
   read as an isolation guarantee.

6. **The filter lands inside the existing fan-out; routing is a follow-up.** Replacing the broadcast with
   a per-address routing table would make the defect structurally unrepresentable and would close it for
   every session-scoped frame type §28.5.3 later adds. It also requires hoisting the heartbeat, which
   drives the SIGTERM liveness escalation (`pkg/adapter/attach.go:139-146`, `pkg/adapter/heartbeat.go:163`),
   so a mistake there kills healthy agents rather than writing a wrong row. §8 records it as the intended
   end state and this proposal does not stage it.

7. **§15.4.1 keeps its number, and is retitled.** Retiring it would leave §15.4 followed by two unnumbered
   `####` blocks and then §15.4.2, whose number and anchor 0064 keeps. After the deletion the heading no
   longer describes its content.

8. **The four `MessageEnvelope` labels stay at `§15.4`.** The Translation Fidelity Matrix and the
   `MessageEnvelope` format are level-4 siblings of §15.4.1 rather than content inside it
   (`spec/15_external-api-surface.md:1471, 1520, 1575` are all `####`), so the numbered section enclosing
   them is §15.4.

## 3. Out of scope, and why

**The platform-tool authorization defect.** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go:945`
reads `in.SessionID` from the tool arguments and never compares it to the caller, while eleven sibling
handlers in the same package resolve through `callerSessionID(ctx, …)` (`mcptools.go:1801`). The principal
is trustworthy — `pkg/adapter/platformtoolprovider.go:37` sends the adapter's gateway-assigned session
over the RPC field, and the runtime cannot influence it — so the handler is ignoring a good principal it
already holds. A runtime with MCP access can therefore name any non-terminal sibling session in its tenant
and write tracing context onto it, with no concurrency involved.

That is a live, more serious defect than the one this proposal fixes, it needs a one-line change, and it
should not wait behind a documentation move. It is stated here so a reviewer does not raise it as a gap.

**`Binder.Resume` leaves the field its own gate reads unset.** `pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`
returns a `BindResult` with neither `SlotID` nor `MaxConcurrentSessions`, so the fail-closed check at
`pkg/gateway/session/executor/pod.go:147` evaluates `0 > 1` and does nothing on the resume path. The chain
that would exploit it appears latent, because `pkg/gateway/sessionserver/finalize.go:238` returns early
for a concurrent pool and leaves no snapshot to resume from. Decision 2's condition 2 covers the frame
regardless. The gate itself is a separate defect.

**The unbound `session_id` on `GatewayControl.CallPlatformTool`.** The SPIFFE `Expect` is left zero at
`cmd/lenny-gateway/main.go:651-656`, so the handlers trust the session id in the request body. This
affects every platform tool and `callerSessionID` does not fix it.

## 4. Detailed design

### 4.1 Why the stream carries what the pod's configuration would

The adapter's per-slot path keys on slot-id presence alone: `useSlot(slotID)` returns `slotID != ""`
(`pkg/adapter/slot.go:43`), and `maxConcurrentSessions` appears in `pkg/adapter/` only inside comments.
The stream's address is therefore the discriminator, and it is complete:

| Pod | Stream's `slotID` | Untagged frame under condition 1 |
|:--|:--|:--|
| Serving slots | non-empty | `"" != "s3"`, rejected |
| Single session | `""` | `"" == ""`, accepted |

Two live streams can never share an address. `checkSession` (`pkg/adapter/session.go:346`) admits only the
one pod-global `s.sessionID`, and `startSessionSlot` (`pkg/adapter/slotsession.go:41`) refuses a claim on
an occupied slot with `Unavailable`.

Slot ids are session ids: `pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682` returns
`SlotID: req.SessionID`, documented at `:210-215` as collision-free for the slot's lifetime, and session
ids are `crypto/rand` UUIDv8 (`pkg/api/v1/session/session.go:39-45`). A slot id is never reused, so a
frame carrying one can only ever have belonged to the session that held it, and no second key is needed
against slot re-use.

### 4.2 The rule

The adapter handles a `set_tracing_context` frame arriving on an Attach stream bound to
`(sessionID, slotID)` if and only if both hold:

1. **Address equality.** `frameSlotID(line) == slotID`, as exact string equality, with the empty string
   counting on both sides.
2. **Live-binding confirmation.** The registry still binds that address to this stream's session:
   `s.sessionID == sessionID` when `slotID == ""`, otherwise `s.slots[slotID].sessionID == sessionID`.

Otherwise the adapter drops the frame, counts it, and logs a protocol error. It relays nothing onward and
returns nothing to the runtime: the inbound set on this channel is closed
(`spec/28_communication-channels.md:539-540`) and its schema `oneOf` admits no report frame
(`schemas/lenny-adapter-jsonl.schema.json:8-19`), which is why the card already states the same outcome
for a `tool_result` whose `id` is unknown (`spec/28_communication-channels.md:545-548`).

Condition 2 exists for one case: a pod-global stream (`slotID == ""`) coexisting with slots, which
`Binder.Resume` makes reachable per §3. Condition 1 alone would accept an untagged frame there.

## 5. Testing

**Tier 1, `pkg/adapter`.** Each case drives an Attach stream and asserts the platform call count and its
session id, annotated `// spec: 28.5.3 (set_tracing_context addressing)`.

- Concurrent pod, frame tagged with the stream's slot: exactly one call, against that slot's session.
- Concurrent pod, untagged frame: no call on any of the pod's streams, and a protocol-error log.
- Concurrent pod, frame tagged with another live slot: no call on this stream; the demux already drops it.
- Single-session pod, untagged frame: exactly one call against the pod's session.
- Single-session pod, frame carrying any `slotId`: no call, per decision 4. This is the case 0068 accepted
  and the tier-1 assertion is what records the reversal.
- Condition 2 in isolation: a stream whose registry entry no longer names its session registers nothing,
  driven by releasing the slot under an open stream.

**Tier 3, `tests/tier3_contract/adapter_jsonl`.** A `set_tracing_context` example carrying `slotId`
validates against the JSONL schema, and the schema rejects a non-string `slotId`. Adds
`schemas/examples/jsonl.set_tracing_context.json` and its entry in the tier-0 example list
(`tests/tier0_static/schemas_test.go`), which enumerates four files today and would otherwise not read it.

**Tier 9, `tests/tier9_security`.** Two sessions on one concurrent pod: an untagged frame emitted by one
runtime leaves both sessions' `tracingContext` unchanged. This is the cross-session isolation assertion
and it fails against the current tree.

**Tier 0.** `TestEveryCitationNamesADocumentAReaderCanReach`, `TestFragmentLinkGateCertifiesTheTree`, and
the heading-walker slug case for the retitled §15.4.1.

**Tier 11.** `TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent` over the §15.4 body after the
deletion.

## 6. Proposed changes

### SPEC-1. Remove from §15.4.1 what §28.5.3 states

In `spec/15_external-api-surface.md` §15.4.1, delete the framing sentence, the inbound message table and
its lead, the outbound message table and its lead, and the `slotId` multiplexing paragraph. The
subsection's existing successor pointer to §28.5.3 stays and is the sentence N8 requires. The
`input_required` note, the stderr statement, and the flushing requirement with its per-language table
stay, because §28.5.3 states no equivalent. The Translation Fidelity Matrix and the `MessageEnvelope`
format are level-4 siblings rather than content inside the subsection and are not reached.

The `set_tracing_context` row is deleted with the outbound table, and SPEC-2 carries its content. One
fragment has no other destination and must survive: the row is the only place in `spec/` stating the frame
is available at every integration level. `spec/15_external-api-surface.md:2194` states that the binary
protocol is the whole Basic-level wire surface, which is a claim about the codec rather than this frame,
and `spec/08_recursive-delegation.md:540` states the MCP tool row, which a Basic runtime cannot reach.

### SPEC-2. Complete §28.5.3's schema set

Add an `**Outbound: `set_tracing_context`**` block to §28.5.3's Message schemas
(`spec/28_communication-channels.md:585` onward), carrying the payload, the optional `slotId`, the
addressing rule of §4.2, the drop-and-log outcome, the tier availability SPEC-1 orphans, and the §8.3 and
§16.3 pointers. The block states the mechanism as the code implements it: the adapter registers by calling
the platform tool with the named session's id injected (`pkg/adapter/tracingcontext.go:36-62`), and the
gateway merges and validates at registration
(`pkg/gateway/mcpfabric/mcptools/mcptools_register.go`). It states the non-guarantee of decision 5.

The §15.4.1 row, the platform-tool row at `spec/08_recursive-delegation.md:540`, and the entry at
`docs/reference/adapter-contract.md:310-316` all say the adapter stores the context and attaches it to
subsequent `lenny/delegate_task` requests. The adapter stores and attaches nothing. The two surviving
statements are corrected to the registration mechanism.

`schemas/lenny-adapter-jsonl.schema.json` gains an optional `"slotId": { "type": "string" }` on its
`set_tracing_context` definition (`:210-223`), matching the property `message`, `tool_call`, `tool_result`,
and `response` already carry.

The enumerations that count these types are reconciled to nine:
`spec/15_external-api-surface.md:1702-1707` and the §28.5.3 `slotId` sentence
(`spec/28_communication-channels.md:548-552`), whose mirror at
`spec/29_communication-scenarios.md:1475-1476` gains the same member.

### CODE-1. Resolve the frame against its stream

In `pkg/adapter/attach.go`, the `set_tracing_context` branch at `:114-117` applies §4.2 before calling
`handleSetTracingContext`, which gains the stream's `slotID` so it can evaluate both conditions. The drop
path counts and logs. Nothing else in the branch changes, and `demuxSlotOutput` is untouched.

### SPEC-3. Retitle §15.4.1

`#### 15.4.1 Adapter↔Binary Protocol` becomes `#### 15.4.1 Message Format and Binary I/O Requirements`.
The one inbound link, the §15.4.5 roadmap item at `spec/15_external-api-surface.md:2035-2036`, is
rewritten by hand: it currently sends a Basic-level author to §15.4.1 for framing SPEC-1 deletes.
`tests/spec-anchor-moves.json` stays empty and this stages no anchor pass; its loader rejects an empty
moves array and derives a retired section number from the anchor slug, so an entry would make
`retiresSection("15.4.1")` true and abort on the two live bare citations in `cmd/runtimes/streaming-echo`.

### SPEC-4. Repoint the references SPEC-1 falsifies

In `spec/28_communication-channels.md`, drop the `[§15.4]` citation from the Endpoint axis (`:525-527`),
from the Messages axis (`:541`), from the `slotId` sentence (`:548-552`), and from the Exclusivity axis
(`:567-570`), and state each on the card's own authority. Add the runtime-side dispatch-loop obligation
the deleted paragraph carried. Repoint the `slotId` keying citations in §28.6 (`:1622-1626`) and the §28.8
row (`:1710`) at §28.5.3, and the two in `spec/29_communication-scenarios.md` (`:1477`, `:1499-1501`).

Repoint the `// spec:` annotations on tests citing §15.4.1 for deleted content in
`pkg/gateway/runtime/adapterclient/client_test.go`, `pkg/gateway/session/executor/pod_test.go`,
`tests/tier10_conformance/concurrent_slot_conformance_test.go`, and
`tests/tier3_contract/sdks/runtime_sdk_test.go`.

## 7. Non-goals

Renumbering §15.4.2 through §15.4.6. Widening §28.2's boundary set. Moving the Translation Fidelity Matrix
or `MessageEnvelope`. Adding `MaxConcurrentSessions` to any CRD. Restoring anything from
`attempt/0068-spec2-implementation`.

## 8. Deferred: route rather than broadcast

The defect's class outlives its instance. Every session-scoped frame is broadcast to N consumers and N−1
discard it, so the next untagged session-scoped type §28.5.3 adds reopens the same hole. The end state is
one reader in `SocketRuntimeProcess` holding an address-to-consumer map recorded at Attach, delivering each
frame to at most one consumer and dropping one that names no live address, with the per-stream
`demuxSlotOutput` goroutine removed.

It is deferred because it requires hoisting the heartbeat, which is per-Attach today
(`pkg/adapter/attach.go:74`, `pkg/adapter/heartbeat.go:152-154`) and is the only consumer of the untagged
pass-through. The heartbeat drives the SIGTERM escalation, so its failure mode is killing a healthy agent
rather than writing a wrong row, and it deserves its own proposal with tier-7a coverage.

## 9. Files touched on application

- `spec/15_external-api-surface.md` — SPEC-1, SPEC-3, the §15.4.5 roadmap item, and the nine-type
  enumeration at `:1702-1707`.
- `spec/28_communication-channels.md` — SPEC-2 and the SPEC-4 citation edits.
- `spec/29_communication-scenarios.md` — the SPEC-4 citations and the `slotId` member list.
- `spec/08_recursive-delegation.md` — the adapter-stores wording at `:540`.
- `docs/reference/adapter-contract.md` — the same wording at `:316`, plus the addressing rule and the drop
  outcome.
- `schemas/lenny-adapter-jsonl.schema.json` — the optional `slotId`, and
  `schemas/examples/jsonl.set_tracing_context.json` with its entry in `tests/tier0_static/schemas_test.go`.
- `pkg/adapter/attach.go` and `pkg/adapter/tracingcontext.go` — CODE-1.
- `pkg/adapter` tier-1 cases, `tests/tier3_contract/adapter_jsonl`, `tests/tier9_security`, and the
  heading-walker slug case, per §5.
- The `// spec:` annotations SPEC-4 names.
- `proposals/0068_fix_settle-15-4-1-and-remove-the-channel-contract-it-duplicates.md` — a superseded note.
