# Proposal: Address a tracing frame by the stream that carries it

- **Status:** **Applied to spec (2026-08-13).** Approved (2026-08-13) by jaf sign-off. Verified
  (2026-08-13). Converged after 5
  adversarial review rounds (5 findings fixed) across two full-pool sweeps, the certifying sweep running
  every lens complete with zero confirmed findings. §9 records each round.
- **Date:** 2026-08-13
- **Scope:** Supersedes proposal 0068. Keeps its specification move, which removes from `spec/15` §15.4.1
  the `CH-MSGSOCK` contract §28.5.3 owns, and replaces its frame-addressing design. 0068 taught the
  adapter its pod's concurrency through a new CRD field and a launch argument; the adapter already holds
  a more precise fact, so this proposal resolves the frame against the Attach stream that delivered it and
  stages no CRD change, no launch argument, and no propagation chain.

This document stages the proposed specification and code changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

0068 converged twice and failed implementation once. §1.3 records what that failure establishes.
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
and `spec/15_external-api-surface.md:1703-1704` lists eight. §28.5.3 defines eight schema blocks while its
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

The attempt asked which session a `set_tracing_context` frame names and answered it six mutually
exclusive ways, three of them twice, terminating on a strategy it had itself refuted eleven commits
earlier. Roughly a third of its commits repair regressions it introduced: a pod-layout record built to
answer the question began refusing §6.4 slot claims and bricked pods, and a per-claim wire field began
refusing `Resume` and `ConfigureWorkspace`.

Every strategy tried to *resolve* the frame's target from state that varies over time (occupancy, claim
order, a latch, and a live slot table), and each died to that state moving: a registry that empties at
teardown while streams still drain, a latch unset before the first claim, and a layout committed before
admission. **Resolving a name from time-varying state can route a frame to the wrong session. Rejecting a
name using it can only drop a frame.** Decision 2 confines the time-varying condition to rejection on that
ground.

## 2. Decisions

1. **The Attach stream is the address.** The adapter resolves the frame against the stream that delivered
   it rather than against the pod's configuration. A stream is bound to `(sessionID, slotID)` at Attach, and
   `checkSlotSession` (`pkg/adapter/slotsession.go:116-128`) validated that binding at bind time.

2. **The rule is two conditions, and only the first resolves.** Address equality names the session;
   live-binding confirmation may only reject. Condition 2 reads state that varies over time, and §1.3 is
   the reason it is confined to rejection.

3. **The design stages no CRD field, launch argument, or propagation chain.** The adapter never needs its pod's declared
   concurrency to decide this frame, because the stream's own `slotID` already carries what "untagged"
   means there. §4.1 states the derivation. 0068's chain (a `SessionPolicy.MaxConcurrentSessions` CRD
   field, regenerated deepcopy and manifests, an extended `sessionPolicyToCRD` mirror, an `Inputs` field,
   a `--max-concurrent-sessions` flag on two deployment models, changes to two embedded reference
   runtimes, and a three-valued undeclared sentinel) is not staged.

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
is trustworthy (`pkg/adapter/platformtoolprovider.go:37` sends the adapter's gateway-assigned session
over the RPC field, and the runtime cannot influence it), so the handler is ignoring a good principal it
already holds. A runtime with MCP access can therefore name any non-terminal sibling session in its tenant
and write tracing context onto it, with no concurrency involved.

That defect is live and more serious than the one this proposal fixes, and it needs a one-line change, so
it should not wait behind a documentation move. It is stated here so a reviewer does not raise it as a gap.

**`Binder.Resume` leaves the field its own gate reads unset.** `pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`
returns a `BindResult` with neither `SlotID` nor `MaxConcurrentSessions`, so the fail-closed check at
`pkg/gateway/session/executor/pod.go:147` evaluates `0 > 1` and does nothing on the resume path. The chain
that would exploit it appears latent, because `pkg/gateway/sessionserver/finalize.go:238` returns early
for a concurrent pool and leaves no snapshot to resume from. The state the gate would have prevented, a
pod holding slots and a pod-global session at once, is what condition 2's `len(s.slots) == 0` term rejects,
so the frame is covered whether or not the chain is reachable. The gate itself is a separate defect.

**The unbound `session_id` on `GatewayControl.CallPlatformTool`.** The SPIFFE `Expect` is left zero at
`cmd/lenny-gateway/main.go:651-656`, so the handlers trust the session id in the request body. This
affects every platform tool and `callerSessionID` does not fix it.

## 4. Detailed design

### 4.1 Why the stream carries what the pod's configuration would

The adapter's per-slot path keys on slot-id presence alone: `useSlot(slotID)` returns `slotID != ""`
(`pkg/adapter/slot.go:43`), and `maxConcurrentSessions` appears in `pkg/adapter/` only inside comments.
The stream's address is therefore the discriminator for every stream that carries a slot id. An empty
stream `slotID` does not by itself mean the pod runs a single session, because the adapter's slot registry
and its pod-global session are independent: `claimSession` (`pkg/adapter/session.go:361-369`) guards only
on `s.sessionID != ""` and never reads `s.slots`, and `startSessionSlot`
(`pkg/adapter/slotsession.go:34-46`) writes only `st.sessionID` and never `s.sessionID`. A pod can hold
both at once, because the adapter imposes no guard against it. `Binder.Resume`
(`pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`) is the gateway-side path that would produce
that state, and §3 records its exploit chain as latent today, because
`pkg/gateway/sessionserver/finalize.go:238` returns early for a concurrent pool and leaves no snapshot to
resume from. Condition 2 rejects the state fail-closed whether or not the chain is reachable. The pod's
slot registry closes the gap:

| Pod state when the frame arrives | Stream's `slotID` | Condition 1 on an untagged frame | Condition 2 |
|:--|:--|:--|:--|
| Holds slots | non-empty | `"" != "s3"`, rejected | not reached |
| Holds slots and a pod-global session | `""` | `"" == ""`, accepted | `len(s.slots) == 0` is false, rejected |
| Holds no slot | `""` | `"" == ""`, accepted | `s.sessionID == sessionID`, accepted |

`len(s.slots) == 0` is the fail-closed reading of "this pod runs one session". `ensureSlotStateLocked`
(`pkg/adapter/slot.go:74-95`) can register a slot whose `sessionID` is still empty, and `releaseSlot`
(`pkg/adapter/slotsession.go:102-112`) is what removes an entry, so a non-empty registry means the pod has
taken the §6.4 per-slot path at least once. A single-session pod never reaches that path, so the predicate
rejects nothing a conforming single-session pod produces.

The addressing rule does not rest on an address being held by one live stream. The adapter enforces no
per-address Attach exclusivity: Attach validates the binding and registers nothing
(`pkg/adapter/attach.go:41-47`), `checkSession` (`pkg/adapter/session.go:346-356`) and `checkSlotSession`
(`pkg/adapter/slotsession.go:116-128`) are comparisons against the recorded session id, and the
`Unavailable` refusal on an occupied slot guards the StartSession claim rather than Attach
(`pkg/adapter/slotsession.go:34-46`). What holds is one content consumer per session per gateway replica,
because `PodExecutor.streamFor` caches a session's stream under `e.mu`
(`pkg/gateway/session/executor/pod.go:125-133`). Across a §10.1 coordinator handoff the stale replica's
adapter-side stream can survive alongside the new one: `EvictStream` only closes the client direction
(`pkg/gateway/session/executor/pod.go:173-180`), the adapter keeps relaying runtime output after a client
half-close (`pkg/adapter/attach.go:132-138`), and `CoordinatorFence` does not cancel in-flight RPCs
(`pkg/adapter/coordination.go:110-113`). Both streams then pass conditions 1 and 2 and one frame is
handled twice for the same session. The outcome is unchanged, because registration is idempotent on a
repeated identical key: `tracing.Merge` (`pkg/delegation/tracing/tracing.go:110-124`) keeps the existing
value on a key collision. The rule stays correct because both deliveries name the same session.

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
2. **Live-binding confirmation.** The registry still binds that address to this stream's session, and the
   address is unambiguous: `s.sessionID == sessionID && len(s.slots) == 0` when `slotID == ""`, otherwise
   `s.slots[slotID].sessionID == sessionID`.

Otherwise the adapter drops the frame, counts it, and logs a protocol error. It relays nothing onward and
returns nothing to the runtime: the inbound set on this channel is closed
(`spec/28_communication-channels.md:539-540`) and its schema `oneOf` admits no report frame
(`schemas/lenny-adapter-jsonl.schema.json:8-19`), which is why the card already states the same outcome
for a `tool_result` whose `id` is unknown (`spec/28_communication-channels.md:545-548`).

Condition 2 covers two cases condition 1 cannot. The first is a pod-global stream (`slotID == ""`)
coexisting with slots, which the adapter does not prevent and which `Binder.Resume` is the gateway-side
path toward, on an exploit chain §3 records as latent today; `len(s.slots) == 0` is the term that rejects
that state fail-closed whether or not the chain is reachable, because the rest of condition 2's pod-global branch (`s.sessionID == sessionID`) repeats at
frame time what `checkSession` (`pkg/adapter/session.go:346-356`) already evaluated at bind time. The
second is the teardown window on either branch, where the binding is released while the stream still
drains: `releaseSession` (`pkg/adapter/session.go:384-386`) clears `s.sessionID` and `releaseSlot`
(`pkg/adapter/slotsession.go:102-112`) deletes the slot entry, and a frame arriving after either drops.
That window is reachable on both branches. `Shutdown` calls `releaseSession` after a `Runtime.Close`
bounded by the grace deadline (`pkg/adapter/session.go:262-265`) while the Attach output relay
(`pkg/adapter/attach.go:93-118`) is still reading the runtime's output, and the resume failure paths
release as well (`pkg/adapter/resume.go:62,78,96,115,120,126`). Today the branch at
`pkg/adapter/attach.go:114-117` calls `handleSetTracingContext` unconditionally, so a frame arriving in
that window registers the identifiers against the just-released session
(`pkg/adapter/tracingcontext.go:47-56` injects the bound `sessionID`). Dropping it is a behavior change
CODE-1 introduces and SPEC-2 writes into §28.5.3, and §5's released-session tier-1 case (`Condition 2,
pod-global branch, released session`) is what pins the `s.sessionID == sessionID` conjunct.

## 5. Testing

**Tier 1, `pkg/adapter`.** Each case drives an Attach stream and asserts the platform call count and its
session id, annotated `// spec: 28.5.3 (set_tracing_context addressing)`.

- Concurrent pod, frame tagged with the stream's slot: exactly one call, against that slot's session.
- Concurrent pod, untagged frame: no call on any of the pod's streams, and a protocol-error log.
- Concurrent pod, frame tagged with another live slot: no call on this stream; the demux already drops it.
- Single-session pod, untagged frame: exactly one call against the pod's session.
- Single-session pod, frame carrying any `slotId`: no call, per decision 4. This is the case 0068 accepted
  and the tier-1 assertion is what records the reversal.
- Condition 2, slot branch: a stream whose slot entry no longer names its session registers nothing,
  driven by releasing the slot under an open stream (`releaseSlot`, `pkg/adapter/slotsession.go:102-112`).
- Condition 2, pod-global branch: a pod holding occupied slots and a pod-global session at once, built by
  claiming slots through `startSessionSlot` and then binding a slotless Attach through `checkSession`, so
  that `len(s.slots) == 0` is the only failing term. An untagged `set_tracing_context` on the slotless
  stream produces no `CallPlatformTool` call and a protocol-error log.
- Condition 2, pod-global branch, released session: a single-session pod holding no slots, with
  `releaseSession` (`pkg/adapter/session.go:384-386`) invoked under an open Attach stream, mirroring the
  grace-deadline overrun in `Shutdown` (`pkg/adapter/session.go:262-265`). An untagged
  `set_tracing_context` delivered on that stream afterwards produces no `CallPlatformTool` call and a
  protocol-error log.

The three condition-2 cases read different state (`s.slots[slotID].sessionID`, `len(s.slots)`, and
`s.sessionID`), so no one of them substitutes for another. The last case is the only one that drives the
pod-global branch's `s.sessionID == sessionID` conjunct to false, so without it an implementor can delete
that conjunct as redundant and no listed test turns red.

**Tier 3, `tests/tier3_contract/adapter_jsonl`.** A `set_tracing_context` example carrying `slotId`
validates against the JSONL schema, and the schema rejects a non-string `slotId`. Adds
`schemas/examples/jsonl.set_tracing_context.json` and its entry in the tier-0 example list
(`tests/tier0_static/schemas_test.go`), which enumerates four files today and would otherwise not read it.

**Tier 9, `tests/tier9_security`.** Two sessions on one concurrent pod: an untagged frame emitted by one
runtime leaves both sessions' `tracingContext` unchanged. A second case mirrors the pod-global branch, with
a pod-global session coexisting with an occupied slot, constructed the way §5's tier-1 pod-global case
constructs it rather than through a resume, and asserts that the sibling slot's untagged frame leaves the
pod-global session's `tracingContext` unchanged. Both are cross-session isolation assertions and both fail
against the current tree.

**Tier 0 (static).** The citation edits and the retitle are read by
`TestEveryCitationNamesADocumentAReaderCanReach`, `TestFragmentLinkGateCertifiesTheTree`, and the
heading-walker slug case for the retitled §15.4.1.

**Tier 11 (documentation).** `TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent` reads the §15.4
body after the deletion.

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
`docs/reference/adapter-contract.md:310-316` each carry two statements the code contradicts. The first is
that the adapter stores the context and attaches it to subsequent `lenny/delegate_task` requests, and the
adapter stores and attaches nothing. The second is that the gateway validates on delegation
(`spec/08_recursive-delegation.md:540`, `docs/reference/adapter-contract.md:316`, and the clause SPEC-1
deletes at `spec/15_external-api-surface.md:1494`), and validation runs at registration instead:
`tracing.Validate` is called once in the tree, inside the `lenny/set_tracing_context` store update
(`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:964-965`), while the delegation path copies the
recorded map without validating it (`pkg/gateway/mcpfabric/delegation/service.go:1468`). Both statements
are corrected at the two surviving sites: the gateway merges the submitted context into the session's
recorded context and validates the result against the §8.3 rules when the identifiers are registered, and
it attaches the registered context to the child's delegation lease. The validation clause is corrected
alongside the storage clause because §8.3's sensitive-key and URL blocklists
(`spec/08_recursive-delegation.md:296-297`) are a security control whose stated purpose at `:299` is that
a malicious parent cannot redirect a child's tracing to an attacker-controlled endpoint. Leaving the
delegation-time wording in place would contradict the new §28.5.3 block, send a reader looking for an
enforcement point that does not exist, and invite an implementor to drop the registration-time call as the
wrong place.

`schemas/lenny-adapter-jsonl.schema.json` gains an optional `"slotId": { "type": "string" }` on its
`set_tracing_context` definition (`:210-223`), matching the property `message`, `tool_call`, `tool_result`,
and `response` already carry.

Two enumerations gain `set_tracing_context`, with different member sets and different resulting counts.
The §15.4 message-type list at `spec/15_external-api-surface.md:1703-1704` enumerates eight types and goes
to nine, which reconciles it with the "nine message types" claim at
`spec/15_external-api-surface.md:1463`. The §28.5.3 `slotId` sentence at
`spec/28_communication-channels.md:548-552` enumerates the subset that carries `slotId`, which is
`message`, `tool_result`, `response`, and `tool_call`, and gains `set_tracing_context` as a fifth member;
its mirror at `spec/29_communication-scenarios.md:1475-1476` gains the same member. That sentence is not a
count of the message types, and rewriting it as a nine-member list would assert that `heartbeat`,
`shutdown`, `heartbeat_ack`, and `status` carry `slotId`, which their own §28.5.3 schema blocks
(`spec/28_communication-channels.md:606-612`, `:779-783`) and the JSONL schema contradict. The §28 side of
the nine-versus-eight contradiction §1.1 records is closed by the new schema block above, which brings
§28.5.3's schema set to nine and makes it agree with the Messages axis at
`spec/28_communication-channels.md:539-541`.

### CODE-1. Resolve the frame against its stream

In `pkg/adapter/attach.go`, the `set_tracing_context` branch at `:114-117` applies §4.2 before calling
`handleSetTracingContext`, which gains the stream's `slotID` so it can evaluate both conditions. Condition
2 reads `s.sessionID`, `s.slots`, and the slot entry's `sessionID` under `s.mu` in one helper in
`pkg/adapter/tracingcontext.go`, modeled on `checkSession` and `checkSlotSession`
(`pkg/adapter/session.go:346`, `pkg/adapter/slotsession.go:116`), so the registry is sampled once under a
single lock hold. The drop path counts and logs. Nothing
else in the branch changes, and `demuxSlotOutput` is untouched.

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
the deleted paragraph carried.

The Messages axis carries a second `[§15.4]` citation at `:545` closing the framing sentence and the
`MessageEnvelope` sentence together. SPEC-1 deletes the only statement of the framing in §15.4
(`spec/15_external-api-surface.md:1473`), so that half is stated on the card's own authority, while the
`MessageEnvelope` half keeps its `[§15.4]` citation, which decision 8 leaves in place.

Repoint the `slotId` keying citations in §28.6 (`:1622-1626`) and the §28.8
row (`:1710`) at §28.5.3, and the three in `spec/29_communication-scenarios.md` (`:450`, `:1477`,
`:1499-1501`). At `:450` the framing claim rests on the §28.5.3 citation the step already carries, and the
`[§15.4]` citation is kept for the flushing requirement SPEC-1 leaves in §15.4.

Repoint the `// spec:` annotations on tests citing §15.4.1 for deleted content in
`pkg/gateway/runtime/adapterclient/client_test.go`, `pkg/gateway/session/executor/pod_test.go`,
`tests/tier10_conformance/concurrent_slot_conformance_test.go`, and
`tests/tier3_contract/sdks/runtime_sdk_test.go`.

## 7. Non-goals

- **Renumbering §15.4.2 through §15.4.6.**
- **Widening §28.2's boundary set.**
- **Moving the Translation Fidelity Matrix or `MessageEnvelope`.**
- **Adding `MaxConcurrentSessions` to any CRD.**
- **Restoring anything from `attempt/0068-spec2-implementation`.**

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

## 9. Resolved in adversarial review

### Pass 1 (2026-08-13, automated)

- **Condition 2 could not reject the case §4.2 said it existed for, and no listed test drove it.** As
  written, condition 2's pod-global branch was `s.sessionID == sessionID`, which is the predicate
  `checkSession` (`pkg/adapter/session.go:346-356`) already evaluates at bind time, so it could not
  distinguish a pod-global stream on a pod that also holds slots. The adapter imposes no guard against
  that coexistence: `claimSession` (`pkg/adapter/session.go:361-369`) guards only on
  `s.sessionID != ""` and never reads `s.slots`, `startSessionSlot` (`pkg/adapter/slotsession.go:34-46`)
  writes only `st.sessionID`, and `demuxSlotOutput` is applied only when `s.useSlot(slotID)`
  (`pkg/adapter/attach.go:70-72`), so in that state an untagged frame from a sibling slot's runtime
  arrives on the pod-global stream and satisfies both conditions as they were written, whether or not the
  gateway-side chain that would produce the state is reachable. Condition 2's pod-global branch is now
  `s.sessionID == sessionID && len(s.slots) == 0`, the fail-closed reading of "this pod runs one session",
  justified in §4.1 against `ensureSlotStateLocked` (`pkg/adapter/slot.go:74-95`) and `releaseSlot`
  (`pkg/adapter/slotsession.go:102-112`). §4.1's table now keys its rows on the pod's slot registry rather
  than on the stream's `slotID` alone and carries the previously missing row for a pod holding slots and a
  pod-global session together. §4.2's justification for condition 2 now names both cases it covers, the
  coexistence and the teardown window. §3 states the same predicate as what covers the `Binder.Resume`
  gap. §5 splits the single condition-2 case into a slot-branch case and a pod-global-branch case that
  builds the coexisting state directly and asserts no `CallPlatformTool` call plus a protocol-error log,
  and the tier-9 section gains the matching cross-session isolation case. CODE-1 states that both
  conditions are evaluated from one sampling of the registry under `s.mu`.

### Pass 2 (2026-08-13, automated)

- **§4.2 called the pod-global branch's session term permanently true while naming the case that falsifies
  it, and no listed test drove it false.** `releaseSession` (`pkg/adapter/session.go:384-386`) clears
  `s.sessionID` while an Attach stream is still draining, on the `Shutdown` grace-deadline path
  (`pkg/adapter/session.go:262-265`, with the output relay at `pkg/adapter/attach.go:93-118` still
  reading) and on the resume failure paths (`pkg/adapter/resume.go:62,78,96,115,120,126`). §4.2 no longer
  claims the term stays true for the life of the stream, states that the teardown window is reachable on
  both branches, and records that dropping the frame there is a behavior change over today's
  unconditional `handleSetTracingContext` call (`pkg/adapter/attach.go:114-117`), which registers against
  the just-released session (`pkg/adapter/tracingcontext.go:47-56`). §5 gains a third condition-2 tier-1 case, a
  single-session pod holding no slots whose session is released under an open stream, asserting no
  `CallPlatformTool` call and a protocol-error log, and its closing sentence now states that the three
  condition-2 cases read different state and that this case is the only one pinning the
  `s.sessionID == sessionID` conjunct. The Pass 1 entry's identical wording about the term is corrected.
- **§4.1 claimed two live streams can never share an address, which the cited code does not establish.**
  Attach validates the binding and registers nothing (`pkg/adapter/attach.go:41-47`), `checkSession` and
  `checkSlotSession` are comparisons against the recorded session id (`pkg/adapter/session.go:346-356`,
  `pkg/adapter/slotsession.go:116-128`), and the `Unavailable` refusal guards the StartSession claim
  (`pkg/adapter/slotsession.go:34-46`). The exclusion is the gateway's per-replica stream cache
  (`pkg/gateway/session/executor/pod.go:125-133`), which does not survive a §10.1 coordinator handoff:
  `EvictStream` closes only the client direction (`pkg/gateway/session/executor/pod.go:173-180`), the
  adapter keeps relaying runtime output after a client half-close (`pkg/adapter/attach.go:132-138`), and
  `CoordinatorFence` cancels no in-flight RPC (`pkg/adapter/coordination.go:110-113`). The paragraph now
  states the per-replica property, states that a stale and a fresh stream can briefly share an address and
  deliver one frame twice, and rests correctness on both deliveries naming the same session together with
  the idempotence of `tracing.Merge` (`pkg/delegation/tracing/tracing.go:110-124`) on a repeated key.
- **Correction to this pass: §4.2's closing pointer named the wrong tier-1 case by ordinal.** §5's tier-1
  list carries eight bullets, and its third is the concurrent-pod case whose frame is tagged with another
  live slot, which the demux drops and which reads none of the pod-global branch's state. The case §4.2
  means is the released-session bullet, the same one §5's closing sentence calls out. §4.2 now names that
  case (`Condition 2, pod-global branch, released session`) instead of an ordinal, and this pass's first
  entry calls it the third condition-2 case rather than the third tier-1 case.

### Pass 3 (2026-08-13, automated)

- **§4.1 and §4.2 attributed a reachability claim to §3 that §3 declines to make.** Both sections said
  `Binder.Resume` makes the coexistence of a pod-global session and slot entries reachable "per §3", while
  §3 records the chain as latent, because `pkg/gateway/sessionserver/finalize.go:238` returns early for a
  concurrent pool and leaves no snapshot to resume from, and states the coverage claim independently of
  reachability. Both sections now state what the adapter establishes, that it imposes no guard against the
  coexistence (`claimSession` at `pkg/adapter/session.go:361-369` never reads `s.slots`, and
  `startSessionSlot` at `pkg/adapter/slotsession.go:34-46` never writes `s.sessionID`), name
  `Binder.Resume` (`pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`) as the gateway-side path
  toward that state on a chain §3 records as latent, and rest condition 2 on rejecting the state
  fail-closed whether or not the chain is reachable.
- **SPEC-4 left a §28.5.3 framing citation pointing at content SPEC-1 deletes.** The Messages axis carries
  a second `[§15.4]` citation at `spec/28_communication-channels.md:545` closing the framing sentence
  ("Each message is a single JSON object terminated by `\n`") together with the `MessageEnvelope`
  sentence, and SPEC-1 deletes the only statement of that framing in §15.4
  (`spec/15_external-api-surface.md:1473`). SPEC-4 now names `:545`, stating the framing on the card's own
  authority and keeping the `[§15.4]` citation for the `MessageEnvelope` half that decision 8 leaves in
  §15.4. The same omission at `spec/29_communication-scenarios.md:450` is added to the spec/29 repoint
  list, where the framing claim rests on the §28.5.3 citation the step already carries and the `[§15.4]`
  citation is kept for the flushing requirement SPEC-1 leaves in place.
- **Correction to this pass: the Pass 1 entry still asserted the reachability §4.1 and §4.2 retracted.**
  It read "The coexistence is reachable" and said an untagged sibling frame "reached the resumed
  pod-global session's stream", which is the attribution this pass removed from §4.1 and §4.2. The entry
  now states what the adapter establishes, that it imposes no guard against the coexistence, and describes
  the frame as satisfying both conditions in that state whether or not the gateway-side chain producing it
  is reachable.
- **Correction to this pass: §5's tier-9 second case specified a resumed session.** It described the case
  as "a resumed pod-global session coexisting with an occupied slot", which requires the `Binder.Resume`
  chain §3 records as latent (`pkg/gateway/sessionserver/finalize.go:238-239` returns early for a
  concurrent pool). The tier-1 pod-global case builds the same state directly through `startSessionSlot`
  (`pkg/adapter/slotsession.go:34-46`) and `checkSession` (`pkg/adapter/session.go:346-356`) for that
  reason. The tier-9 case now names the state it constructs and points at that construction instead of a
  resume.

### Pass 4 (2026-08-13, automated)

- **SPEC-2 moved the §8.3 validation point to registration and left `spec/08_recursive-delegation.md:540`
  stating that the gateway validates on delegation.** The three sites SPEC-2 enumerates each carry a second
  clause naming when validation runs, and SPEC-1 deletes the only one of the three that lives in §15.4.1
  (`spec/15_external-api-surface.md:1494`). With SPEC-2's new §28.5.3 block stating registration-time
  validation, the applied spec would contradict itself about the enforcement point of the §8.3
  sensitive-key and URL blocklists (`spec/08_recursive-delegation.md:296-297`, whose purpose at `:299` is
  that a malicious parent cannot redirect a child's tracing to an attacker-controlled endpoint). The code
  enforces the rules only at registration: `tracing.Validate` is called once in the tree, in the
  `lenny/set_tracing_context` store update (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:964-965`),
  and the delegation path copies the recorded map without validating it
  (`pkg/gateway/mcpfabric/delegation/service.go:1468`). SPEC-2 now corrects both clauses at the two
  surviving sites, `spec/08_recursive-delegation.md:540` and `docs/reference/adapter-contract.md:316`, and
  §10's entries for those two files name both statements.
- **SPEC-2 ordered the §28.5.3 `slotId` sentence "reconciled to nine", and that sentence enumerates the
  `slotId`-carrying types rather than the message types.** The sub-step gave one value for two different
  enumerations. `spec/15_external-api-surface.md:1703-1704` lists the eight message types and does go to
  nine, which reconciles it with `spec/15_external-api-surface.md:1463`. The sentence at
  `spec/28_communication-channels.md:548-552` lists `message`, `tool_result`, `response`, and `tool_call`,
  the four types that carry `slotId`, and its mirror at `spec/29_communication-scenarios.md:1475-1476`
  lists the same four. Rewriting either as a nine-member list would assert that `heartbeat`, `shutdown`,
  `heartbeat_ack`, and `status` carry `slotId`, contradicting their own §28.5.3 schema blocks
  (`spec/28_communication-channels.md:606-612`, `:779-783`), the surviving clause "on a pod that sets it to
  1 no message carries `slotId`", and the JSONL schema, which declares `slotId` on the envelope,
  `tool_call`, `tool_result`, and `response` alone. SPEC-2 now states the two edits separately with their
  own member sets, and records that the §28 side of the nine-versus-eight contradiction §1.1 names is
  closed by the new schema block rather than by an edit at `:548-552`.

## 10. Files touched on application

- `spec/15_external-api-surface.md` — SPEC-1, SPEC-3, the §15.4.5 roadmap item, and the message-type
  enumeration at `:1703-1704`.
- `spec/28_communication-channels.md` — SPEC-2, the `slotId` member list at `:548-552`, and the SPEC-4
  citation edits.
- `spec/29_communication-scenarios.md` — the SPEC-4 citations and the `slotId` member list.
- `spec/08_recursive-delegation.md` — the adapter-stores wording and the validates-on-delegation wording at
  `:540`.
- `docs/reference/adapter-contract.md` — the same two statements at `:316`, plus the addressing rule and
  the drop outcome.
- `schemas/lenny-adapter-jsonl.schema.json` — the optional `slotId`, and
  `schemas/examples/jsonl.set_tracing_context.json` with its entry in `tests/tier0_static/schemas_test.go`.
- `pkg/adapter/attach.go` and `pkg/adapter/tracingcontext.go` — CODE-1.
- `pkg/adapter` tier-1 cases, `tests/tier3_contract/adapter_jsonl`, `tests/tier9_security`, and the
  heading-walker slug case, per §5.
- The `// spec:` annotations SPEC-4 names.
- `proposals/0068_fix_settle-15-4-1-and-remove-the-channel-contract-it-duplicates.md` — a superseded note.
