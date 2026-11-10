# Message delivery

Status: design for owner review, revision 2, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`client-tools-design.md`](client-tools-design.md) revision 4, [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 4, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 7, [`session-derive-design.md`](session-derive-design.md) revision 15, [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 9, [`security-oversight-design.md`](security-oversight-design.md) revision 47, [`delegation-availability-design.md`](delegation-availability-design.md) revision 3, and [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2. It is built from the read-only investigation of 2026-11-09 into messages that arrive while a runtime works, whose recommendations S1 to S10 the owner accepted on 2026-11-09 with the additions recorded under "Decisions". The owner settled every open decision of revision 1 on 2026-11-09, and no decision remains open. This design re-verifies that investigation's tree and specification claims. It stages no specification edit. The changes go through `change-proposal` after review. Parts of the design carry the label MD-n, requirements carry MDR-n, decisions that began as open questions carry O-n, defects carry MDF-n, and the proposals that land the design carry MD-A to MD-C under "Proposal split and order". "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external documentation page or in a public repository on 2026-11-09. "Sources" lists the pages. |
| **[unverified]** | An external claim that the pages read on 2026-11-09 do not state. "Validation owed" lists each one. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

A person who works with a coding agent often types a correction while the agent is busy: "use the staging database instead", or "skip the tests for now". Most agent harnesses accept such a message at any time and fold it into the agent's work at its next model step, after the tool calls it is running finish **[external]**. Claude Code, the Claude Agent SDK, Codex, Cursor, and the Vercel AI SDK behave this way by default. They also offer a mode that holds the message until the agent's turn ends, and a mode that stops the agent and sends the message next.

Lenny offers only the queue and interrupt modes, and the tree offers neither in the form the specification states **[source]**:

- The specification buffers every message that arrives while a tool call is in flight, and the only mid-turn delivery it defines interrupts the runtime ([§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model), "Message delivery routing", paths 4 and 5).
- The gateway executor pairs each message with one `response` through a turn token, discards a `response` that no message caused, and holds the session's delivery lock for the whole turn. A message sent mid-turn waits for the running turn to end before anything answers it, and a parent session's `lenny/send_message` call blocks until the child's turn ends.
- `capabilities.injection.modes` appears in runtime declarations, and no section says what a mode means.

The wire protocol between the adapter and the runtime does not cause any of this: the `CH-MSGSOCK` card already requires a runtime to handle a `message` that arrives before a pending `tool_result`. The alternation comes from the gateway's delivery policy, its executor, and the runtime SDKs.

The owner asked for a general machinery that covers today's cases and later ones. This design gives every message a delivery intent, lets each runtime declare the intents it accepts, gives every message an explicit state from acceptance to consumption, and keeps the gateway's custody of a message until the runtime reports that it consumed it. The non-interrupting mode the harnesses default to becomes the intent `steer`, which a runtime declares when it can take a message at any time.

## Scope

**Added:**

- Delivery intents `steer`, `queue`, and `interrupt` on every message, with a rule for adding intents ("MD-1" and "MD-14").
- The definition of `capabilities.injection.modes` as the intents a runtime accepts, and `capabilities.injection.defaultMode` ("MD-2").
- Inbox states, custody until consumption, at-least-once delivery up to consumption, deduplication by message id, and the `consumed_unrecorded` state that reports a message consumed after the checkpoint a session resumes from ("MD-3").
- Turn accounting on `CH-MSGSOCK`: `response` lists the message ids it consumed, a `consumed` frame reports consumption during a turn, and a `response` that no message caused is accepted ("MD-4").
- Retraction: a runtime may retract a message it generated, by id, and receives `retracted`, `too_late`, or `unknown` ("MD-11"). A client may retract a message it sent through `DELETE /v1/sessions/{id}/messages/{messageId}` and the generated MCP tool ("MD-7", O5).
- A drain call in the runtime SDKs that returns the messages queued for a session between model steps ("MD-12").
- The session events `message_state` and `turn_end`, and per-message delivery receipts on REST and MCP ("MD-7").
- Two rules for sessions created on a provider surface. Client tools are offered only in turns a client request caused, and a turn that no client request caused never appears in a provider-surface response ("MD-8", O3).

**Changed:**

- The [§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model) delivery paths are rewritten as named paths keyed on the turn state and the intent ("MD-5"). The `ready_for_input` term leaves the specification (O1).
- The envelope's `delivery` member takes intents in place of `immediate` and `queued` ("MD-1").
- The executor's turn token and the turn-long delivery lock are replaced by a writer, a frame reader, and a turn waiter ("MD-6").
- `POST /v1/sessions/{id}/messages` and `lenny/send_message` return receipts at once, with consumption and output reported as session events ("MD-7").
- The client-tools hold rule applies only to runtimes that do not declare `steer`, and the cold-history builder gains an ordering rule ("MD-10").
- An inbox delivery to a held session on a provider surface is delivered on arrival under the ordinary intent rules, in place of waiting for the next client turn ("MD-8", O3).
- The reference runtime declarations of [§26](../spec/26_reference-runtime-catalog.md#26-reference-runtime-catalog) ("MD-13").

**Removed:**

- The executor's turn token (`attachConn.token`) and the rule that discards a `response` with no registered turn.
- The synchronous `output` member of the REST message response.
- The delivery timeout of 30 seconds that §7.2 measures against a consumption acknowledgement no frame carries.

**Out of scope:**

- Steering by a provider-surface client. A Chat Completions, Responses, or Messages API client cannot correct its own running turn, because each request is one turn, as the provider APIs define it ("MD-8").
- Scheduled delivery, priority ordering, context-only messages, and the other future cases of "MD-14". The machinery accommodates them, and none ships in v1.
- Retraction of output and session events. Output already streamed cannot be recalled, and output the gateway has not yet sent to a client is ignored for now ("MD-11", "Blast radius").
- Multi-thread sessions, which the owner left out of v1 under G2. `threadId` keeps its v1 meaning.
- The fix of each tree defect as a separate change. The owner decided that the defects under "Defects found while verifying" are fixed later under a holistic plan.

## Terms

| Term | Meaning |
| :-- | :-- |
| Message | One `MessageEnvelope` of type `message` that a sender addresses to a session: a client message, an inter-session message, or a system message. |
| Delivery intent (intent) | The value of a message's `delivery` member, which states when the message reaches the runtime relative to the runtime's current turn: `steer`, `queue`, or `interrupt`. |
| `steer` | Deliver the message now, without interrupting. The runtime reads it at its next model step, after the tool calls it is running finish, and places it after their results. Without a running turn, the message starts one. |
| `queue` | Hold the message until the runtime's current turn ends, then deliver it as the start of a new turn. |
| `interrupt` | Stop the runtime's current work at a safe point, then deliver the message as the start of a new turn. |
| Default intent | The intent a message takes when its sender names none: the runtime's `defaultMode`, which is `steer` when the runtime declares `steer` and `queue` otherwise. |
| Turn | The runtime's work from the delivery of a message to an idle runtime, or from output the runtime starts on its own, until its next `response` frame. |
| Open turn | A turn whose `response` has not arrived. The gateway tracks it per session from the frames it writes and reads ("MD-4"). |
| Unsolicited response | A `response` that closes a turn the runtime started on its own, for example after background work or a child's result. |
| Causing message | The message whose delivery opened a turn. A turn the runtime starts on its own has no causing message. |
| Client request | A message from the session's client on any client surface: REST, MCP, Chat Completions, Responses, or the Messages API. A turn whose causing message is a client request is caused by that request. A turn caused by another session's message, a system message, or an external message is not caused by a client request. |
| Consumption | The runtime's acceptance of a delivered message into its own handling: the message entered its model history, or the runtime acted on it without a model call. The runtime reports it by message id. |
| Inbox | The gateway's per-session store of messages it has accepted and not yet released: the session inbox of §7.2, extended to hold delivered messages until consumption ("MD-3"). |
| Inbox state | One of `pending`, `delivered`, `consumed`, `consumed_unrecorded`, `retracted`, `expired`, `dropped`, and `superseded`, defined in "MD-3". |
| Retraction | A sender's request that the gateway never deliver a message it sent. It succeeds only while the message is `pending`, and for a client tool call only while the call is pending on a REST or MCP session ("MD-11"). |
| Retraction result | The answer to one retracted id: `retracted`, `too_late` (the recipient already received it, or the surface cannot withdraw it), or `unknown` (the id names nothing this sender may retract). |
| Custody | The gateway's obligation to keep a message and redeliver it until the runtime reports its consumption (O4). |
| Drain call | The runtime SDK call that returns the messages queued in the runtime process for a session while a handler runs ("MD-12"). |
| Provider surfaces | The Chat Completions, Responses, and Messages API surfaces, whose clients follow a provider's API. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Session | Lenny's unit of work: one runtime instance bound to a pod, with an owner, a tenant, a transcript, and billing events ([§7](../spec/07_session-lifecycle.md#7-session-lifecycle)). |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). |
| Revision | An immutable, numbered copy of a runtime's versioned fields, `capabilities` among them, chosen per session ([`runtime-revisions-design.md`, RV-1](runtime-revisions-design.md#rv-1-revisions)). |
| Per-tenant capability override | The §5.1 mechanism that adjusts a runtime's capabilities for one tenant's sessions, which RV-1 restricts to narrowing ([`runtime-revisions-design.md`, "Per-tenant capability override"](runtime-revisions-design.md#per-tenant-capability-override)). |
| `CH-MSGSOCK` | The intra-pod channel between the adapter and the runtime process, which carries the JSONL frames `message`, `response`, `tool_call`, `tool_result`, `heartbeat_ack`, and others ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `CH-RUNTIMEOPS` | The intra-pod channel on which the adapter sends `interrupt_request` and a Full-level runtime answers `interrupt_acknowledged` ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `CH-ATTACH` | The per-session stream between the gateway and the adapter that carries envelopes to the pod and relays the runtime's frames back ([§28.5.1](../spec/28_communication-channels.md#2851-gateway-to-pod)). |
| `MessageEnvelope` | The runtime-facing message format, with `id`, `from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `sessionId`, and `input` ([§15.4.1](../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format"). |
| `from.kind` | The sender kind on every envelope, written by the adapter: `client`, `agent`, `system`, or `external`. |
| Integration levels | Basic (stdin and stdout only), Standard (adds the platform MCP server), and Full (adds `CH-RUNTIMEOPS`) ([§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels)). |
| DLQ | The per-session dead-letter queue in Redis that holds messages for a session in a pre-running, recovering, or `suspended` state (§7.2). |
| Durable inbox | The Redis-backed session inbox that `messaging.durableInbox: true` selects, with its "Explicit ACK" and "Crash recovery" rows (§7.2). |
| `lenny/request_input` | The platform tool by which a runtime blocks on a question, answered by a message whose `inReplyTo` matches (§7.2 path 1). |
| Client tool call and pending calls | A call a runtime returns for the client to execute, recorded on the session row until the client's results arrive ([`client-tools-design.md`, CT-5](client-tools-design.md#ct-5-pending-calls-and-the-hold-rule)). |
| Results message | A client message whose `toolResults` answer a session's pending calls ([`client-tools-design.md`](client-tools-design.md#terms)). |
| `abandonPendingToolCalls` | The flag by which a REST or MCP client sends a text message while calls are pending, turning the pending calls into "cancelled by client" error results ([`client-tools-design.md`, T9](client-tools-design.md#decisions)). |
| Conversation input | The `conversation` member of the envelope, with ordered `entries`, that [`anthropic-messages-api-design.md`, AM-4](anthropic-messages-api-design.md#am-4-conversation-input) adds. |
| Held session | A session the Open Responses or Messages adapter keeps after a turn for a warm continuation (§15 "Open Responses conversation sessions", and [AM-5](anthropic-messages-api-design.md#am-5-continuity)). |
| Warm and cold turn | A warm turn continues a held session. A cold turn starts a new session and delivers the earlier conversation as history. |
| Checkpoint | The adapter's snapshot of a session's workspace and runtime state, from which a session resumes on a new pod ([§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume), and [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md)). |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, 10 runtime adapter conformance, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| T5, T9, O3, CT-5, CT-7, CTR-4, and CTR-5 | [`client-tools-design.md`](client-tools-design.md) | The settled rule that inbox deliveries wait while client calls are pending for a runtime that does not declare `steer`, the settled `abandonPendingToolCalls` rule, the settled decision on deliveries held across a tool loop, the pending-calls part with its "Steer-declaring runtimes" subsection, continuation with its "Order of history entries" rule, and the requirements that no inbox delivery reaches a runtime without `steer` while calls are pending and that no call is outstanding inside a runtime. |
| T16 | [`client-tools-design.md`](client-tools-design.md#decisions) | The settled rule that client tools are offered only in turns a client request caused, which that design takes from O3 of this design. |
| CT-A | [`client-tools-design.md`](client-tools-design.md#proposal-split-and-order) | That design's core proposal. |
| AM-4, AM-5, and AM-A | [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) | Conversation input, continuity with the in-flight join, and the proposal that lands conversation input on the OpenAI translators. |
| RV-1 and RV-2 | [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions) | Revisions with their field classes and the per-tenant capability override, and revision selection with the `runtime_revision` metric label. |
| SD-1 | [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) | The derive route, and "Reading client messages", which gives transcript entries a sender and a delivery state. |
| IC-4 | [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md#ic-4-catalog-reference-closure-and-quota) | The checkpoint catalog. |
| SO-5a | [`security-oversight-design.md`](security-oversight-design.md#so-5a-tool-use-approvals) | Tool-use approvals that hold a platform or connector tool call at the gateway. |

## High-level design

Every message a session receives says when it should reach the runtime. A message marked `steer` goes to the runtime at once, and the runtime reads it at its next model step without stopping what it is doing. A message marked `queue` waits until the runtime finishes its current turn. A message marked `interrupt` stops the runtime's work and goes next. Most clients name no intent: the message takes the runtime's preference, which is `steer` for a runtime that declares it. A client that wants another behaviour sets one field on that message.

The gateway keeps each message from the moment it accepts it until the runtime reports that it consumed it, and records the message's state along the way. A send call answers as soon as the message is written to the runtime or held, and the client follows consumption and output on the session's event stream. Because the runtime reports which messages each reply consumed, the gateway no longer assumes one reply per message: one turn can consume several messages, and a runtime can reply without any new message.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Intents (MD-1).** The envelope's `delivery` member carries `steer`, `queue`, or `interrupt`. Absent means the default intent. The values mirror the Claude Agent SDK's `priority` values `next`, `later`, and `now`.

**Declaration (MD-2).** `capabilities.injection.modes` lists the intents a runtime accepts, and must include `queue`. `steer` is available at every integration level, and `interrupt` requires Full. `defaultMode` names the runtime's preference. An explicit intent the runtime does not declare is refused.

**States and custody (MD-3).** A message is `pending` while the gateway holds it, `delivered` once written to the runtime, and `consumed` once the runtime reports it. It may also end `retracted`, `expired`, or `dropped`, and `superseded` is reserved for a later intent. Custody ends when the runtime reports consumption (O4). The gateway redelivers a `delivered` message after a pod loss, and the adapter and the SDK drop a message id they have already consumed. A message consumed after the checkpoint a session resumes from is not redelivered: it becomes `consumed_unrecorded`, and its sender decides whether to send it again.

**Turn accounting (MD-4).** `response` gains `consumed`, a list of message ids. A new `consumed` frame reports consumption during a turn (O2). A `response` with no message to attribute is an unsolicited response and is recorded. The gateway tracks an open turn from the frames it writes and reads, which replaces `ready_for_input` (O1).

**Delivery paths (MD-5).** §7.2's numbered paths become named paths: the reply path, the dead-letter path, the resume path, the direct path, the steer path, the interrupt path, and the queue path.

**Executor (MD-6).** A writer delivers an envelope and returns. A reader routes every frame. A turn waiter lets a caller that needs a turn's output wait for the turn that consumed its message. The delivery lock covers the routing decision and the write, and never a turn.

**REST and MCP (MD-7).** A send returns one receipt per message at once. `message_state` and `turn_end` events report consumption and turn ends.

**Provider surfaces (MD-8).** A request remains one turn. The adapter waits for the turn that consumed its message. An inbox delivery to a held session is delivered on arrival under the ordinary intent rules. A turn that no client request caused carries no client tools, and its output never appears in a provider-surface response (O3).

**Inter-session messages, system messages, and delegation (MD-9).** `lenny/send_message` carries an intent and returns at once. A parent can redirect a running child with `steer`.

**Client tools (MD-10).** A `steer` runtime receives inbox deliveries while client calls are pending and places them after the results. A turn that an inbox delivery causes carries no client tools (O3). The cold-history builder never places an entry between calls and their results.

**Retraction (MD-11).** A runtime writes a `retract` frame naming ids it generated and receives a `retract_result` frame with `retracted`, `too_late`, or `unknown` for each. A pending client tool call on REST or MCP is cancelled and the client receives a retraction event. A returned call on a provider surface cannot be withdrawn, so the answer there is `too_late`. A message the runtime sent to another session is retracted while it is `pending` in the recipient's inbox and is `too_late` once delivered. Output and session events are not retracted in v1.

**SDKs (MD-12).** The drain call returns messages queued behind the running handler. The SDK reports consumption, drops duplicate ids, and can write an unsolicited response.

**Reference runtimes (MD-13).** `claude-code`, `codex`, and `chat` declare `steer`. The others declare `queue` and `interrupt` until their harnesses' steering is verified.

**Extension (MD-14).** A new intent states its gateway behaviour per turn state, its runtime obligation, and its states, and is declared when the runtime has to act on it.

```
 sender (client, agent, system, external)
   │ message {id, delivery: steer | queue | interrupt | absent}
   ▼
 gateway: admission, scope, rate limits, intent check against the revision's injection.modes
   │
   ▼
 inbox entry ──pending──┬─ no open turn, or steer ─────────────> write ──> delivered
                        ├─ interrupt: interrupt_request, wait for the turn's response ─> write
                        └─ queue: wait for turn_end ────────────────────────────────> write
                                                                                        │
 runtime: reads at next model step (steer), places after tool results                   │
   │ consumed {ids}  or  response {output, consumed}                                     │
   ▼                                                                                     ▼
 gateway: delivered ──> consumed: released from custody (O4)
          resume from a checkpoint taken before the consumption ──> consumed_unrecorded (no redelivery)

 sender: retract {ids} ──> pending entry ──> retracted (never delivered)
                           delivered or consumed entry ──> too_late (no change)
   │
   └─> events on the session stream: message_state, output, turn_end
```

## Decisions

Settled by the owner on 2026-11-09. S1 to S10 are the investigation's recommendations, which correspond to its decisions D1 to D10, and the owner accepted them generally with the additions in the rows below:

| ID | Decision | Where |
| :-- | :-- | :-- |
| S1 | Lenny adds a non-interrupting mid-turn delivery mode, `steer`, as an opt-in runtime declaration. | MD-1, MD-2 |
| S2 | A client does nothing by default: a message takes the runtime's preferred intent, `steer` when declared. One optional per-message field overrides it, mirroring the Claude Agent SDK's `priority` (`next`, `later`, and `now`). | MD-1 |
| S3 | REST and MCP return `delivered` once the message is written, and report consumption and output as events. | MD-7 |
| S4 | The gateway keeps custody of a delivered message until the runtime reports consumption, with at-least-once delivery and deduplication by id. | MD-3 |
| S5 | `response` lists the message ids it consumed, and an unsolicited `response` is accepted. | MD-4 |
| S6 | Client tools: for a `steer` runtime, inbox deliveries are not held while calls are pending, and the runtime places them after the results. T5's hold and the client-tools design's O3 apply only to other runtimes. The cold-history builder places a message that arrived while calls were pending after their results. | MD-10 |
| S7 | Client tools: on REST and MCP, a client text message without the pending results is refused unless it sets `abandonPendingToolCalls: true`. Provider surfaces always refuse. The client-tools design records it as its T9. | MD-10 |
| S8 | Retraction, settled on 2026-11-09. The owner wrote: "let's support retraction, including the retraction of any message generated by the runtime. For tool calls, the behavior would be what you described. For other messages, either ignore for now or retract if not delivered to the client yet (assuming that's a small blast radius)." A runtime may retract any message it generated, by id, and receives `retracted`, `too_late`, or `unknown`. On REST and MCP a retraction cancels pending client tool calls and the client receives a retraction event. On provider surfaces a returned call cannot be withdrawn, the client still sends results, the runtime ignores them, and the result is `too_late`. A message to another session that is still `pending` becomes `retracted` and is never delivered, and one already delivered or consumed is `too_late`. Retraction of output and session events is ignored for now, because its blast radius is not small. | MD-11 |
| S9 | The tree's path 4 and path 5 divergences and the executor and lock defects are filed in `BUILD-GAPS.md`, cited here, and fixed later under a holistic plan. | "Defects found while verifying" |
| S10 | `capabilities.injection.modes` is defined in §5.1, and the CRD comment is corrected. | MD-2 |
| G1 | The delivery machinery is a general per-message model: every message carries an intent, each runtime declares the intents it accepts, the inbox gives messages explicit states and keeps custody until consumption, each message records `from.kind`, and a runtime acknowledges consumption by message id. The design names the future cases it covers and states the extension rule. | MD-1 to MD-4, MD-14 |

The owner's decisions on the client-tools design (T1 to T8, and the updates to that design's O3, T5, S7, O8, and O9) and on the Messages design bind this design as that design and the owner's record state them.

Settled by the owner on 2026-11-09 with revision 2, from the open decisions of revision 1. The owner answered "ok" to O1, O2, and O5, which take revision 1's recommendation, and to O4, which takes option (a) in place of revision 1's recommendation of (b). O3 changes revision 1's recommendation on the owner's own answer. G2 records the owner's answer on multi-thread sessions. No decision is open.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| O1 | How the gateway decides that a runtime can take a held message, in place of `ready_for_input`. | Option (a). The term leaves the specification, the gateway tracks an open turn from the frames it writes and reads, and a `queue` message waits for the turn's `response`. The term was introduced to stop a message from being written to stdin while tool calls are in flight. Waiting for the turn's `response` covers that concern and is stricter, because no tool call of a turn is in flight once its `response` has arrived. No frame ever carried the signal (F-7.2.49), so no runtime depends on it. [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod) "Intra-pod" makes the stdout `response` "always required to signal task completion" at every integration level, and a process that exits non-zero before writing one makes the adapter synthesize `RUNTIME_CRASH`, so every turn ends in a frame the gateway observes. Mid-turn delivery becomes opt-in through `steer`. A reply to `lenny/request_input` by `inReplyTo` takes the reply path as before, and a message that is not `steer` still waits while the runtime is blocked in `lenny/request_input`. | MD-4, MD-5 |
| O2 | Whether a runtime reports consumption during a turn. | Option (a). `CH-MSGSOCK` gains the `consumed` frame, which a runtime that declares `steer` writes when it consumes messages, and `response.consumed` reports the rest. A steered message is consumed mid-turn, and a coding turn can run for many minutes after it. With consumption reported on `response` alone, the client's `message_state` event, the transcript position of the message, and the release of custody would all wait for the turn's end. The frame costs a runtime one line per consumption, and the SDK writes it ("MD-12"). | MD-4, MD-12 |
| O3 | Inbox deliveries to a session created on a provider surface. | The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes", which replaces revision 1's recommendation to keep such deliveries `pending` until the next client turn. A message from another session to a session created on the Messages, Chat Completions, or Responses surface is delivered on arrival under the ordinary intent rules, and those surfaces add no handling of their own. Rule 1: client tools are offered only in turns a client request caused, so a turn caused by another session's message carries no `conversation.clientTools`. Rule 2: a turn that no client request caused never appears in a provider-surface response. Its output goes to the transcript and to the session's REST and MCP event streams, and the client's next response is the output of the turn its own message caused, under the `consumed` attribution of "MD-4". A client request that arrives during such a turn is steered into it on a runtime that declares `steer`, and waits for the turn to end on any other runtime. Tool-use approvals work through the approvals API as on any session. Elicitation and `lenny/request_input` stay refused on those surfaces. "MD-8" records the scenarios in which such a message arrives. | MD-8, MD-10 |
| O4 | When custody of a consumed message ends. | Option (a). Custody ends when the runtime reports consumption. A redelivery of a consumed message after a restore can repeat side effects that a checkpoint cannot undo, for example a push, so at-most-once delivery after consumption is the safer default. A runtime that never checkpoints would otherwise keep every consumed message in the inbox until the session ends. In place of the redelivery, when a session resumes from a checkpoint, the gateway emits a `message_state` event with the state `consumed_unrecorded` for each message consumed after that checkpoint, so a client or a parent can decide whether to send it again. | MD-3, MD-7 |
| O5 | Whether a client may retract a message it sent, as S8 lets a runtime do. | Option (a). A client retracts its own message through `DELETE /v1/sessions/{id}/messages/{messageId}` and the generated MCP tool, with the results and rules of "MD-11". S8 builds the operation for runtimes, and the client route reuses it with no further state or store change. A `queue` message can wait for a long turn, and the user who queued it may change their mind, as Claude Code and Cursor let a user remove a queued message **[external]**. Retraction succeeds only while the message is `pending`, so it never races the runtime. | MD-7, MD-11 |
| G2 | Whether a session carries several concurrent conversations, each named by `threadId`. | Left out of v1. The owner wrote: "ok to leave out of v1". No harness keeps separate concurrent conversations in one process. Threads would multiply the per-thread delivery machinery, because intents, open turns, attribution, and custody would each be kept per thread. Separate sessions and delegation cover the use cases. `threadId` and `inReplyTo` already let a later version add threads with no schema change. | "Scope" |

## Requirements

| ID | Requirement |
| :-- | :-- |
| MDR-1 | Every message the gateway accepts has exactly one inbox state at a time, records its sender's `from.kind` and its intent, and changes state only along the transitions of "MD-3". |
| MDR-2 | A message reaches a runtime only with an intent the session's revision, after the per-tenant override, declares. An explicit intent the revision does not declare is refused before the message is held. |
| MDR-3 | The gateway keeps custody of every message until the runtime reports its consumption, or until the message ends `retracted`, `expired`, or `dropped`. Within the durable inbox's guarantee, every message is delivered at least once up to its reported consumption and never redelivered after it, and a runtime that follows "MD-12" sees each message id once. |
| MDR-4 | No send call waits for a runtime's turn, and no code path holds the session's delivery lock across a turn. |
| MDR-5 | A runtime that declares `steer` reads messages at any time, including while tool calls of any kind and client calls are pending, and never places a message between a tool call and its result in its model history. |
| MDR-6 | Every `response` is recorded, including one no message caused, and every consumed message id is attributed to the turn that consumed it. |
| MDR-7 | A history the gateway builds for a cold turn never places an entry between an assistant entry that carries calls and the entry that carries their results. |
| MDR-8 | A runtime that declares only `queue` sees one message per turn with no new frame to write: the gateway attributes a `response` without `consumed` to the oldest delivered message. |
| MDR-9 | An `interrupt` message reaches only a Full-level runtime that declares `interrupt`, and an interrupt that is not acknowledged degrades to `queue`. |
| MDR-10 | A retraction never removes or alters anything a recipient has received. It changes only a `pending` message, or a pending client tool call on a REST or MCP session, and every retracted id receives exactly one result. |
| MDR-11 | A sender retracts only what it sent. A retraction that names another sender's message, or anything else the sender may not retract, answers `unknown` and reveals nothing about the id. |
| MDR-12 | Every retraction that changes state writes an audit record naming the retracting principal, the item, its recipient, and the result. |
| MDR-13 | A turn that no client request caused carries no `conversation.clientTools`, on every surface (O3). |
| MDR-14 | A provider-surface response carries only output of the turn that consumed the request's own message, from the point of that consumption. The output of a turn that no client request caused never appears in a provider-surface response, and reaches the transcript and the session's REST and MCP event streams (O3). |
| MDR-15 | When a session resumes from a checkpoint, every message the runtime reported consumed after that checkpoint is reported `consumed_unrecorded` and is not redelivered (O4). |

## Current state

### Specification

**Envelope [source].** §15.4.1 "`MessageEnvelope` — Unified Message Format" defines `delivery` as an optional closed enum "controlling interrupt behaviour": `"immediate"` ("Interrupt the running agent and deliver now"), `"queued"` ("Buffer for next natural pause"), and absent, which equals `"queued"`. Unknown values are refused with `400 INVALID_DELIVERY_VALUE`. `from.kind` is a closed enum of `client`, `agent`, `system`, and `external`, and the adapter overwrites any runtime-supplied `from`. Sender-supplied ids must be unique within the tenant, and the gateway keeps a deduplication window of seen ids (`messaging.deduplicationWindowSeconds`, 3600 seconds by default). The section's closing paragraph calls the envelope future-proof for "all future conversational patterns without schema changes".

**Receipts [source].** §15.4.1 "`delivery_receipt` acknowledgement schema" gives `status` the values `delivered` ("runtime consumed"), `queued`, `dropped`, `expired`, `rate_limited`, and `error`. `message_expired` reaches the sender session's event stream with the reasons `dlq_ttl_expired`, `durable_inbox_ttl_expired`, and `target_terminated`.

**Wire contract [source].** The `CH-MSGSOCK` card of §28.5.3 lists the runtime-to-adapter frames `session_started`, `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`. Under **Inbound: `tool_result`**, "Delivery semantics" states: "Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` — the agent must handle interleaved delivery." **Outbound: `response`** carries `output` and an optional `error`, and nothing in the card pairs one `response` with one `message`. **Outbound: `heartbeat_ack`** requires `busy: true` while the runtime handles a message, runs a tool call, does background work outside a turn, or waits on children.

**Capabilities [source].** §5.1 "Runtime" shows `injection: {supported: true, modes: [immediate, queued]}` in its YAML and defines only `supported`: "`capabilities.injection` declares whether the runtime supports mid-session message delivery." `multi_turn` requires `injection.supported: true`. No section defines `modes`. The CRD comment on `InjectionCapabilityCRD.Modes` (`pkg/apis/lenny/v1alpha1/runtime_types.go`) reads "`immediate` interrupts a suspended session to deliver, `queued` buffers the message for the next runtime turn", which matches neither §15.4.1 nor §7.2.

**Delivery paths [source].** §7.2 "Message delivery routing — seven paths" evaluates paths in order:

- Path 1 resolves an outstanding `lenny/request_input` by `inReplyTo`.
- Path 2 writes the message when the runtime is available, meaning its adapter reports `ready_for_input`. The receipt is `delivered` "only after confirmed stdin consumption" within a delivery timeout of 30 seconds, and otherwise the message falls through to the inbox. The "`ready_for_input` — normative definition for concurrent tool execution" requires that no tool call is in flight and that the runtime is reading stdin, and routes "a message arriving while any tool call is in flight" to path 5.
- Path 3 buffers while the session is `input_required`, even under `delivery: "immediate"`.
- Path 4 is the only mid-turn delivery: under `delivery: "immediate"` with a tool call in flight, the gateway calls the adapter's `Interrupt`, the adapter writes `interrupt_request` on `CH-RUNTIMEOPS`, and the message follows the acknowledgement at Full level or follows at once at Basic and Standard.
- Path 5 buffers in the session inbox while the runtime is busy and delivers in FIFO order at the next `ready_for_input`.
- Path 6 resumes a `suspended` session, with **Delivery of held messages.**, which delivers every held message of the inbox and the DLQ before each path-2 message, one at a time and oldest first, and removes an entry only after the runtime consumed it.
- Path 7 sends a message for a terminal or recovering session to the dead-letter handling.

The durable inbox's "Explicit ACK" row removes an entry after delivery and requires the adapter to keep a `delivered_message_ids` set of the last 1000 ids in checkpoint state, suppressing redelivered duplicates and counting them on `lenny_inbox_duplicate_suppressed_total`.

**Unstated signals [source].** §29.3 "Interactive message send" step 8 records that "the specification does not state which channel and which message carry the adapter's report that the runtime is `ready_for_input` and its acknowledgement that the runtime consumed the written message".

**SDKs [source].** §15.7 "Runtime Author SDKs" defines `Handler.OnMessage(ctx, Message) (Reply, error)` and busy holds for work after a handler returns, including "the rest of a turn whose Reply is streaming and not final".

**Reference runtimes [source].** §26.2 declares `multi_turn` with `modes: [immediate, queued]` for the coding-agent runtimes, §26.7 `chat` and §26.10 `openai-assistants` declare `[immediate]`, and §26.8, §26.9, and §26.11 declare `[immediate, queued]`. Every reference runtime is Full level (§26.1). §26.3 "Bootstrap behavior" pipes each message to `claude` and states nothing about a message that arrives while `claude` works.

**Client surfaces [source].** §15.1 routes `POST /v1/sessions/{id}/messages` through the §7.2 paths. §15 "Open Responses conversation sessions" serves a continuation warm only when its response is the session's newest completed response, and a concurrent continuation runs cold. §15 "Built-in adapter single-shot compute model" runs one turn per Chat Completions request.

### Tree

- `PodExecutor.Send` (`pkg/gateway/session/executor/pod.go`): "Each message is one turn: Send takes the stream's turn token, writes the envelope, and waits for the reader to hand it the turn's `response`." A batch of messages is a sequence of turns.
- `attachConn` (`pkg/gateway/session/executor/podstream.go`) holds `token`, a channel of capacity 1. `acquireTurn` takes it, and `completeTurn` returns it and documents: "A `response` with no turn registered is discarded."
- `Server.deliverMessageBatch` (`pkg/gateway/sessionserver/messages_delivery.go`) takes `s.deliveryLocks.Lock` and releases it by `defer` after `restDelivery.route`, whose `deliverNow` calls `s.executor.Send` and waits for the turn. `mcpSend.deliver` (`pkg/gateway/mcpfabric/mcptools/send_delivery.go`) takes `LockDelivery` the same way around `sendNow`, which calls `Executor.Send`. A second message to a session whose turn runs waits on the lock until the turn ends, and a parent's `lenny/send_message` returns only when the child's turn ends.
- `messagerouting.Classify` (`pkg/gateway/session/messagerouting/messagerouting.go`) states that "a synchronous in-process executor is ready by construction, so this collapses to direct delivery" for path 5, and that `immediate` "selects path 4 over path 2 when the adapter exposes an in-flight-tool signal, and otherwise does not change the destination". Its body never reads `immediate` for a running session. The REST caller passes the batch's `immediate` flag, and the MCP caller passes `false`.
- `restDelivery.immediate` reads `delivery` on REST, and the MCP send path reads no `delivery` value.
- `runtimestore.InjectionMode` (`pkg/gateway/runtime/runtimestore/runtimestore.go`) has the values `immediate` and `queued`. `validateCapabilities` (`pkg/gateway/externalapi/admin/runtimes.go`) checks them, and the runtime controller mirrors them (`pkg/controller/runtime/controller.go`). No other code reads `injection.modes`. `Server.requireInjectionSupported` (`pkg/gateway/sessionserver/messages.go`) enforces `injection.supported`.
- No file under `pkg/`, `sdks/`, or `schemas/` names `ready_for_input` apart from comments in `messagerouting.go`.
- `MessageResponse` (`pkg/gateway/sessionserver/messages.go`) returns one `DeliveryReceipt` and the turn's `Output`, which the Go client SDK reads (`sdks/client/go/lenny`).
- `sessioninbox.Inbox` (`pkg/gateway/session/sessioninbox/sessioninbox.go`) offers `Enqueue`, `Peek`, `Ack`, `Drain`, `Len`, and `Delete`, in memory and on a Redis list. An entry carries no state.
- No code in `pkg/adapter` keeps a set of delivered message ids. `lenny_inbox_duplicate_suppressed_total` exists only in the metric catalog (`pkg/observability/metrics/catalog.go`).
- The Go runtime SDK's `sessionQueue` (`sdks/runtime/go/runtime/queue.go`) is an unbounded per-session FIFO, and `process.serve` (`session.go`) dispatches one message at a time to `OnMessage`. A handler has no call that reads the messages queued behind it. The Python and TypeScript SDKs follow the same handler model.
- `OpenAIChatHandler` (`pkg/gateway/environment/translator/openai_chat.go`) sends every entry of `messages` as its own executor message, which AM-A replaces with conversation input.
- `pkg/embedded/stack/catalog.go` mirrors the reference runtime declarations, and the tree holds no reference-runtime adapter source.

### Harnesses on 2026-11-09

The investigation surveyed the harnesses, and this design re-read the pages its argument rests on **[external]**:

- The Claude Agent SDK's `SDKUserMessage.priority`: `'next'`, or no `priority`, means "Claude reads the message in the same turn, as soon as the tool calls it is running finish. If the turn ends first, the message starts the next turn"; `'later'` holds the message "until the turn ends and sends it as a new turn"; `'now'` moves background-capable work to the background or interrupts the turn. `shouldQuery: false` appends a message "without triggering an assistant turn". `interrupt()` returns the ids of messages still queued.
- Claude Code cross-session messaging: "The receiving Claude reads the message between tool calls during an active turn. When the receiving session is idle, Claude Code starts a new turn with the message."
- Codex app-server: `turn/steer` will "append user input to the active in-flight turn", requires `expectedTurnId`, and fails "if there is no active turn on the thread". `turn/interrupt` ends the turn with `status: "interrupted"`.
- Cursor Cloud Agents API: `delivery: "steer"` "sends the prompt into the agent's running turn", answered `steered` or `queued`. Without `delivery`, a follow-up to a busy agent answers `409 agent_busy`.
- Gemini CLI queues by default, and its experimental `experimental.modelSteering` setting, off by default, sends typed text as a hint at the next model step.
- Anthropic tool use: "Tool result blocks must immediately follow their corresponding tool use blocks in the message history", and in the user message that carries them "the `tool_result` blocks must come FIRST in the content array. Any text must come AFTER all tool results."

The investigation found no harness that places a message between a tool call and its result.

## MD-1 Delivery intents

**Values [design].** `delivery` on the `MessageEnvelope` takes `steer`, `queue`, or `interrupt`, or is absent. `immediate` and `queued` leave the enum, because Lenny has no deployments to keep. An unknown value is refused with `400 INVALID_DELIVERY_VALUE`, whose message lists the defined values.

| Intent | Meaning | Claude Agent SDK `priority` | Codex | Cursor |
| :-- | :-- | :-- | :-- | :-- |
| `steer` | Deliver now and do not interrupt. The runtime reads the message at its next model step and places it after the results of the tool calls it is running. Without an open turn, the message starts one. | `next`, the default | `turn/steer` | `delivery: "steer"` |
| `queue` | Hold until the open turn ends, then deliver as the start of the next turn. | `later` | Tab-queued input | Tab-queued follow-up |
| `interrupt` | Stop the open turn at a safe point, then deliver as the start of the next turn. | `now` without background work | `turn/interrupt`, then `turn/start` | Second Enter |

Claude Code's `now` first moves background-capable work to the background and lets Claude read the message in the same turn. Lenny's `interrupt` does not, because the gateway cannot move a runtime's work. A runtime that can do so is a future intent ("MD-14").

**Default [design, S2].** A message without `delivery` takes the default intent of the session's revision: its `injection.defaultMode` when set, `steer` when the revision declares it, and `queue` otherwise. The gateway writes the resolved intent into the envelope it delivers and the inbox entry it records, so a runtime and a reader of the transcript see the intent that applied.

**Override [design, S2].** A sender sets `delivery` on one message to override the default. REST takes it per message in the batch, MCP `send_message` and the agent-facing `lenny/send_message` take it in the message, and the `lenny session send` command gains `--delivery`. Provider surfaces take no override ("MD-8").

**Unsupported intents [design].** A message whose explicit intent the revision does not declare is refused before it is held: `422 DELIVERY_MODE_UNSUPPORTED` on REST and MCP, with the declared intents in `details`, and a receipt with `status: "error"` and `reason: "delivery_mode_unsupported"` on `lenny/send_message`. A refusal is explicit, because degrading `steer` to `queue` would delay a correction a person expects the agent to read now, and degrading `interrupt` to `steer` would let work the sender meant to stop continue.

**Ordering [design].** Messages of one intent reach the runtime in the order the gateway accepted them. A `steer` message overtakes `queue` messages that are still `pending`, as the harnesses' steered messages overtake their queued ones.

## MD-2 Runtime declaration

```yaml
capabilities:
  interaction: multi_turn
  injection:
    supported: true
    modes: [steer, queue, interrupt]
    defaultMode: steer
```

**[design, S10]**

- `modes` lists the intents the runtime accepts. When `supported` is true, `modes` must contain `queue`, because every injecting runtime takes a message at the start of a turn. Registration refuses an unknown value, a list without `queue`, and a non-empty list with `supported: false`.
- `steer` declares the obligations of MDR-5: the runtime reads `message` frames at any time, during a turn, while adapter-local, platform, connector, and approval-held tool calls run, while it is blocked in `lenny/request_input`, and while client calls are pending, and it places each message in its model history only at a point the model API accepts. It reports consumption with `consumed` frames and `response.consumed` ("MD-4"). `steer` is available at every integration level, because it uses `CH-MSGSOCK` alone.
- `interrupt` declares that the runtime handles `interrupt_request` on `CH-RUNTIMEOPS`, ends its open turn with a `response` once it acknowledges, and keeps the interrupted work's tool calls paired with results, for example by recording an error result for a call it abandons. Registration refuses `interrupt` on a runtime whose `integrationLevel` is below `full`, and the existing check of the observed level at the first session applies.
- `defaultMode` is optional and must be one of `modes`. A runtime that declares `steer` but prefers `queue` sets it, as Gemini CLI queues by default although it can steer.
- The fields are versioned and gateway-read under RV-1, so the session's revision decides. The per-tenant capability override may keep a subset of `modes` that still contains `queue`, as RV-1 states for `injection.modes`, and may set `defaultMode` to one of the remaining intents. When an override removes the revision's `defaultMode`, the default falls back to the rule of "MD-1".
- The CRD comment on `InjectionCapabilityCRD.Modes` states the meaning above.

## MD-3 Inbox states and custody

**States [design, G1].**

| State | Meaning | Receipt status |
| :-- | :-- | :-- |
| `pending` | Accepted and held by the gateway: in the session inbox or the DLQ, waiting for the turn state its intent requires, for a resume, or for a session to start. | `queued` |
| `delivered` | Written to the session's `CH-ATTACH` stream for the runtime, and not yet reported consumed. | `delivered` |
| `consumed` | Reported consumed by the runtime in a `consumed` frame or a `response`. Custody ends, and the entry leaves the inbox (O4). | Reported by event |
| `consumed_unrecorded` | Reported consumed, after which the session resumed from a checkpoint taken before the consumption, so the restored runtime does not hold the message. It is not redelivered (O4). | Reported by event |
| `expired` | Its TTL elapsed while the session was in a recovering state, or the session ended before consumption. | `expired`, or the `message_expired` event |
| `dropped` | Evicted by inbox or DLQ overflow. | `dropped` |
| `retracted` | Retracted by its sender while `pending` ("MD-11"). It is never delivered. | Reported by event |
| `superseded` | Reserved for a later intent that replaces a `pending` message ("MD-14"). No v1 path produces it. | Reported by event |

Transitions: `pending` to `delivered`, `retracted`, `expired`, or `dropped`; `delivered` to `consumed` or `expired`, and back to `pending` when a redelivery is due; `consumed` to `consumed_unrecorded` on a resume from a checkpoint taken before the consumption. `consumed_unrecorded` is final, and `consumed` is final unless such a resume occurs. A `delivered` message is never `retracted` or `dropped`, because the runtime already holds it, and a redelivery returns it to `pending` only inside the gateway's redelivery step, where no retraction can intervene: the step holds the session's delivery lock.

**Records [design, MDR-1].** Each inbox entry carries the message id, the sender's `from.kind` and `from.id`, the resolved intent, the state, the acceptance time, and the time of each transition. The transcript row of the message, which SD-1 "Reading client messages" gives a sender and a delivery state, carries the same state and intent, so `GET /v1/sessions/{id}/messages` reports them. The transcript row keeps the state after the inbox entry is released, and for a consumed message it also records the consumption's position in the session's sequence of consumption reports, which the gateway assigns in the order the session's stream relays them.

**Custody [design, S4, O4].** Delivery no longer removes an inbox entry. The entry moves to `delivered` and stays until the runtime reports consumption. When the gateway records the consumption, custody ends and the entry leaves the inbox, whether or not a checkpoint has recorded the consumption. In the durable inbox, `pending` entries stay in the Redis list, and an entry moves at delivery into a per-session Redis hash keyed by message id, which the coordinating replica reads on lease acquisition, and leaves the hash at consumption. The in-memory inbox keeps the same states in memory, and its documented loss window on a coordinator crash is unchanged.

**Redelivery [design].** The gateway redelivers every `delivered` entry, and never a consumed message (O4), oldest first and ahead of `pending` entries:

- after a resume onto a new pod (§7.3), once the session is `running` again;
- after a coordinator handoff with the durable inbox, once the new coordinator holds the lease. The runtime process is the same one, so the adapter suppresses ids it has already written to that process.

**Consumption after a checkpoint [design, O4].** A runtime restored from a checkpoint lacks every message it consumed after that checkpoint, and the gateway does not resend those messages, because a consumed message can have caused side effects that the checkpoint does not undo, for example a push. When a session resumes from a checkpoint, the coordinating replica reads the checkpoint's consumed set through the IC-4 catalog and takes the newest id in it that the gateway recorded as consumed. Every message whose consumption the gateway recorded after that id moves to `consumed_unrecorded`, oldest first. When no id in the set qualifies, every message the session consumed moves. Each move emits `message_state` with `state: "consumed_unrecorded"` and `reason: "checkpoint_restore"` on the session's event stream, and, for a message from another session, on the sending session's event stream as well, as `message_expired` reaches a sender today. A client or a parent that reads the event decides whether to send the content again, as a new message with a new id.

**Deduplication [design].** Because only `delivered` entries are redelivered, deduplication covers two cases. After a coordinator handoff, the runtime process is the same one and may already hold a redelivered entry. After a resume, the restored runtime may hold an entry whose consumption report the pod loss cut off, when the checkpoint was taken after the runtime consumed it. The adapter keeps, per session, the ids it has written to the current runtime process for the first case, and for the second the ids the runtime reported consumed, bounded to the last 1000, kept in report order, and carried in the checkpoint. The consumed set replaces the `delivered_message_ids` set of the durable inbox's "Explicit ACK" row, and it no longer filters consumed messages, which are never redelivered. On a redelivery the adapter drops an id in either set and counts it on `lenny_inbox_duplicate_suppressed_total`, and for an id from the consumed set it relays a `consumed` frame, so the entry leaves custody. The runtime SDKs keep the ids each session consumed and drop a repeated id ("MD-12"), which covers a runtime whose own persisted history records a consumption that neither the gateway nor the checkpoint's set records. The gateway's existing deduplication window still refuses a reused sender-supplied id.

**Expiry [design].** A `pending` entry keeps today's TTL rules. A `delivered` entry has no TTL while the session lives, because the runtime holds it. When the session ends, its `pending` and `delivered` entries become `expired` with `reason: "target_terminated"`, and the sender is notified as §15.4.1 states today.

## MD-4 Turn accounting

**`response.consumed` [design, S5].** The `response` frame gains `consumed`, an array of message ids that the runtime consumed in the turn the frame closes and has not reported in a `consumed` frame:

```json
{
  "type": "response",
  "sessionId": "sess_01J9...",
  "output": [{ "type": "text", "inline": "Switched to the staging database and reran the migration." }],
  "consumed": ["msg_01J9A", "msg_01J9B"]
}
```

**`consumed` frame [design, O2].** A runtime that declares `steer` writes `{"type": "consumed", "sessionId": "sess_01J9...", "ids": ["msg_01J9B"]}` when it consumes messages during a turn. The adapter relays it on `CH-ATTACH` as it relays `response`, and `consumed` joins the session-scoped frame types the adapter's demultiplexer routes by `sessionId` (`sessionScopedFrameTypes` in `pkg/adapter/attach.go`). A runtime that does not declare `steer` need not write it. The frame is a `CH-MSGSOCK` message, so no channel is added and the §28.3 registers are unchanged.

**Attribution without `consumed` [design, MDR-8].** A `response` without `consumed` is attributed to the oldest `delivered` message of the session, which then becomes `consumed`. That is today's pairing for a runtime that handles one message per turn, so a Basic runtime writes nothing new. A runtime that declares `steer` must send `consumed`, and a `response` from it without the member consumed nothing.

**Unsolicited responses [design, S5].** A `response` that finds no `delivered` message to attribute, and carries no `consumed` ids, closes a turn the runtime started on its own. The gateway records its output in the transcript, emits its `output` events and a `turn_end` event with `unsolicited: true`, and counts it on `lenny_unsolicited_response_total{runtime, runtime_revision}`. An id in `consumed` that names no `delivered` message of the session is logged as a protocol error and ignored.

**Open turn [design, O1].** The gateway's session state records whether a turn is open:

- A turn opens when the gateway writes a message while no turn is open, and when the runtime emits output, a `tool_call`, or a `consumed` frame while no turn is open. A `retract` frame ("MD-11") opens no turn, because it produces no work for the session.
- A turn closes on the runtime's `response`, and on the end of the runtime process.

The open turn replaces `ready_for_input` as the condition a `queue` message waits for. A runtime that writes no `response` keeps its turn open, as it keeps a turn open today, and the session's idle and heartbeat rules apply to it unchanged. The turn ordinal increments at each opening, and `turn_end` events carry it.

**Errors [design].** A `response` with `error` closes the turn as a failed turn, and the messages it lists in `consumed` are `consumed`. A message the failed turn did not consume stays `delivered`, and the runtime may consume it later. The gateway reads `error`, which the executor ignores today (client-tools design, "Defects found while verifying").

## MD-5 Delivery paths

§7.2 "Message delivery routing" is rewritten as named paths, evaluated in order, where the first match wins **[design]**. Names in place of numbers let a later path join the list without renumbering its neighbours, as the specification's naming law asks of channel identifiers.

| Path | Condition | Action | Receipt |
| :-- | :-- | :-- | :-- |
| Reply path | `inReplyTo` matches an outstanding `lenny/request_input`. | Resolve the call. No write. | `delivered` |
| Dead-letter path | The session is terminal, recovering, or pre-running. | As old path 7 and the dead-letter table state. | As today |
| Resume path | The session is `suspended`. | Hold the message and resume the session as old path 6 states, then apply the rows below to the resumed runtime. | `delivered` on synchronous delivery, otherwise `queued` |
| Direct path | No turn is open, or the message is a results message. | Write now. | `delivered` |
| Steer path | A turn is open and the intent is `steer`. | Write now. | `delivered` |
| Interrupt path | A turn is open, the intent is `interrupt`, and the session is not `input_required`. | Hold the message, call the adapter's `Interrupt`, and write the message when the interrupted turn's `response` arrives. When `interrupt_acknowledged` does not arrive within the `interrupt_request` deadline, the message continues as `queue`. | `queued` |
| Queue path | A turn is open and the intent is `queue`, or the intent is `interrupt` and the session is `input_required`. | Hold until the turn's `response`. | `queued` |

The session stays `running` on the interrupt path. The `POST /v1/sessions/{id}/interrupt` route keeps its meaning and moves the session to `suspended`.

**Delivery of held messages [design].** At each turn end, after each commit to `running`, and after each adoption of a `running` session, the coordinating replica writes the held entries whose intent now permits a write, oldest first by acceptance time across the inbox and the DLQ. For a runtime that does not declare `steer`, it writes one entry and waits for the next turn end before the next, so each held message is its own turn as today. For a `steer` runtime it writes every eligible entry, and the runtime may consume them in one turn. Held deliveries no longer precede every direct-path write: a held entry that is eligible is written before the new message at the same delivery point, which keeps acceptance order.

**Client tools [design, S6].** For a session with pending client calls, the client-message rules of [CT-5](client-tools-design.md#ct-5-pending-calls-and-the-hold-rule) run before the paths. For a runtime that does not declare `steer`, inbox deliveries are held as CT-5 and the client-tools design's O3 state. For a `steer` runtime they take the paths above ("MD-10").

**Successor pointers [design].** Each numbered path keeps a pointer in §7.2 to its named successor: path 1 to the reply path, path 2 to the direct path, path 3 and path 5 to the queue path, path 4 to the interrupt path, path 6 to the resume path, and path 7 to the dead-letter path. The proposal retargets the specification's citations of path numbers, in §7.3, §10.1.2, §11.1, §15, §15.4.1, and §29.3 among others, to the names.

**Removed rules [design, O1].** The `ready_for_input` definition, the delivery timeout of 30 seconds and its fall-through to the inbox, and the `input_required` overlap rule between old paths 3 and 5 leave §7.2. §29.3 step 8 is rewritten: the adapter relays `consumed` and `response`, and the gateway derives the open turn from them.

## MD-6 Executor, lock, and waiting

**Writer [design, S9].** `PodExecutor` gains `Deliver(ctx, sessionID, []Envelope) ([]WriteResult, error)`, which writes each envelope on the session's `CH-ATTACH` stream and returns once the writes succeed or the stream fails. It takes no turn token, and a write is allowed while a turn is open. `attachConn.token`, `acquireTurn`, and the turn registration leave `podstream.go`.

**Reader [design].** The stream's reader routes every frame: `consumed` and `response` update inbox states and the open-turn state through one session-scoped recorder, `response` output goes to the transcript and the event stream, and an unsolicited response is recorded under "MD-4". Nothing is discarded for want of a waiter.

**Turn waiter [design].** `AwaitConsumingTurn(ctx, sessionID, messageID) (TurnResult, error)` returns the `response` of the turn that consumed the message, or the failed turn's error. The provider-surface adapters and replay use it. A waiter that stops waiting leaves nothing registered on the stream, because the reader's routing does not depend on waiters.

**Delivery lock [design, MDR-4].** `deliveryLocks` covers classification, the inbox write, and the `Deliver` call, and is released before any wait for a turn. The lock still serializes held-message delivery against the message paths on one replica, which is the purpose `sessionserver.go` states for it.

**Classification [design].** `messagerouting.Classify` takes the session row, the open-turn state, the input-wait state, the resolved intent, the revision's declared intents, and whether the message is a results message, and returns a named path. Its comment that a synchronous executor is ready by construction leaves with the turn token.

## MD-7 REST and MCP

**Send [design, S3].** `POST /v1/sessions/{id}/messages` answers with `deliveryReceipts`, one per message in the batch, in the order sent, and no `output` member. `delivered` now means that the gateway wrote the message for the runtime. The receipt answers once the message is written or held, without waiting for consumption or output. The client-facing MCP `send_message` tool and the agent-facing `lenny/send_message` return the same receipt. The §15.4.1 description of `delivered` changes from "runtime consumed" to "written for the runtime; consumption is reported by the `message_state` event".

**Events [design].** The `SessionEvent` registry of §15 gains these kinds:

| Kind | Payload | Fires when |
| :-- | :-- | :-- |
| `message_state` | `{ "messageId", "state", "intent", "fromKind", "turn"?, "reason"? }` | A message of the session changes state, including to `retracted` and to `consumed_unrecorded`. A move to `consumed_unrecorded` also reaches the sending session's stream for a message from another session ("MD-3"). The payload carries no content. |
| `turn_end` | `{ "turn", "consumed": [ids], "unsolicited": bool, "error"? }` | A `response` closes a turn. |

The registry is closed, so the addition takes the `SessionEvent` schema revision the registry requires. The session's `output` events carry the turn's output as they do today. Adapters that do not list the kinds in `SupportedEventKinds` do not receive them, under the registry's dispatch-filter rule.

**Client retraction [design, O5].** Under O5, a client retracts its own `pending` message: `DELETE /v1/sessions/{id}/messages/{messageId}` runs the retraction of "MD-11" for a message the caller sent to the session and answers `200` with `{ "messageId", "result" }`, where `result` is `retracted`, `too_late`, or `unknown`. A message the caller did not send answers `unknown`, under MDR-11. The MCP tool is generated from the route under §15.2.1 rule 4.

**Clients [design].** The Go client SDK, the `lenny session send` command, and the web playground read output from the event stream. `lenny session send` without an attached stream prints the receipt and exits, as §24 states today.

## MD-8 Provider surfaces

A Chat Completions, Responses, or Messages API request is one turn, because its client follows the provider's API, where a request is a turn **[design]**:

- The adapter writes the request's message through the paths of "MD-5" with no intent the request can set. A cold turn reaches a new session through the direct path. A warm turn reaches a session whose newest response has completed (§15 "Open Responses conversation sessions" condition 3 and AM-5 condition 3), so the only turn that can be open is one that no request on this surface caused, such as a turn another session's message caused. With no open turn, the message takes the direct path. With such a turn open, it takes the steer path on a runtime that declares `steer` and the queue path, which waits for the turn's `response`, on any other runtime (O3).
- The adapter waits with `AwaitConsumingTurn` for the turn that consumed the request's message, and returns that turn's output from the point the runtime consumed the message. A turn the message opened returns all its output. For a message steered into a running turn, the response carries the output that follows the `consumed` frame listing the message, or, when only the turn's `response` lists it, that `response`'s own output. A `steer` runtime may consume inbox deliveries in the same turn, and their ids appear in the turn's `consumed` list. Only the client's message decides which turn answers the request.
- A second request during a running turn that a request on the same surface caused behaves as it does today: on Responses it runs cold, on the Messages API an identical retry joins the running turn through AM-5's in-flight join and any other request runs cold, and on Chat Completions every request is its own session.
- A Chat Completions session is released after its turn, so its `pending` entries expire with `target_terminated`.
- The Responses cold walk of a conversation's transcript includes consumed inbox deliveries as entries carrying their `from` object, with the output of the turns they caused, so a cold runtime reads what the warm runtime read. The Messages API and Chat Completions take their history from the client, and the gateway adds nothing to it.

**Delivery on arrival [design, O3].** A message from another session to a session created on a provider surface is delivered on arrival under the ordinary intent rules of "MD-1" and "MD-5", and the provider surfaces add no handling of their own. On a held session between client requests no turn is open, so the message takes the direct path and opens a turn. No delivery waits for the client's next request. Two rules keep such a turn away from the provider-surface client:

1. **Client tools only in turns a client request caused.** A turn caused by another session's message, a system message, or an external message carries no `conversation.clientTools` and no `conversation.toolChoice`, and a `response` of such a turn that carries `clientToolCalls` fails the check of [CT-4](client-tools-design.md#ct-4-response-validation) with `no_tools_declared`. A client request steered into such a turn carries its own `conversation.clientTools` in its envelope, so the runtime may call those tools from the point it consumes the request, and the turn's `response` returns the calls to that request's client.
2. **No such turn in a provider-surface response.** A turn that no client request caused never appears in a provider-surface response. Its output goes to the transcript and to the session's REST and MCP event streams. It writes no response record and no conversation digest and takes no response ordinal, so the warm conditions of the client's next request are unaffected. The client's next response is the output of the turn its own message caused, under the `consumed` attribution of "MD-4" and the cut point stated above.

Tool-use approvals in such a turn work through the approvals API as on any session. Elicitation and `lenny/request_input` stay refused on those surfaces, as AM-6 states for the Messages API, whatever caused the turn.

**Scenarios [design, O3].** A message reaches a session created on a provider surface other than through the client's own requests in these cases:

- Asynchronous delegation, the main case. A runtime served on the Messages API or Responses delegates a child, replies to its client, and the child later finishes or sends the parent a message through `lenny/send_message` after that reply. The parent runtime takes the message in a turn of its own. The client sees that turn on the session's REST and MCP event streams, and in a provider response only when the runtime refers to it in a later turn the client caused.
- The same session used from REST or MCP, through the session id the response headers carry. A REST or MCP client message causes a turn whose output reaches the REST and MCP streams and no provider-surface response.
- A sibling's message under the `siblings` messaging scope of §7.2.

## MD-9 Inter-session messages, system messages, and delegation

**Inter-session messages [design].** `lenny/send_message` carries `delivery` in its message and resolves the target's default intent when absent. The cross-tenant check, the messaging scope, the rate limits, and the `PreMessageDelivery` interceptor run before the intent check, as §7.2 orders them today. The call returns its receipt once the message is written or held, so a sender never waits for the target's turn. The sender learns of expiry from `message_expired` as today.

**Delegation [design].** A parent can redirect a running child with `steer`, for example "stop exploring the cache layer and fix the failing test", without cancelling it and losing its work. A parent that wants the child to stop first sends `interrupt`, when the child's runtime declares it. A child's `task` from `lenny/delegate_task` is the child's first message and starts its first turn, so no intent applies to it. A parent answers a child's `lenny/request_input` through the reply path, unchanged. A parent blocked in `lenny/await_children` that declares `steer` receives inter-session messages during the wait. One that does not receives them at its turn end, which matches today's buffering while `await_children` is in flight. A child's message to a parent created on a provider surface is delivered on arrival, and the turn it causes follows the two rules of "MD-8" (O3). The subtree deadlock detector of §8 and the delegation lease's `messagingRateLimit` are unchanged.

**System messages [design].** A message the gateway itself originates carries `from.kind: system` and takes the target's default intent unless the mechanism that sends it names one. v1 defines no new system message. "MD-14" lists the candidates.

**External senders [design].** A message routed through a connector carries `from.kind: external` and follows the same rules as an inter-session message.

**Sender kind [design].** The adapter writes `from` as today, and the inbox entry and the transcript row record it (MDR-1), so a runtime that weighs a client's correction above an agent's suggestion can tell them apart.

## MD-10 Client tools

The client-tools design's revision 2 already scopes its hold rule to runtimes that do not declare `steer` ("Steer-declaring runtimes" under CT-5) and adds the history rule ("Order of history entries" under CT-7). This part states the delivery machinery's side of both.

**Inbox deliveries while calls are pending [design, S6].** For a runtime that declares `steer`, an inbox delivery to a session with pending client calls takes the paths of "MD-5". The session has no open turn after the turn that returned the calls, so the message is written at once and opens a turn. That turn carries no `conversation.clientTools`, because no client request caused it (O3, rule 1 of "MD-8"). The runtime holds the message, places it after the results when they arrive, and reports its consumption then, or acts on it without a model call. CTR-4 of the client-tools design narrows to runtimes that do not declare `steer`, for which T5's hold and that design's O3 stand. CTR-5 holds unchanged, because the calls themselves stay session data and the held message is in the gateway's custody until consumed ("MD-3").

**Client messages [design, S7].** A client message without the pending results is refused on every surface. On REST and MCP a message that sets `abandonPendingToolCalls: true` is a results message whose results are the "cancelled by client" error results, with the client's text as `input`. A results message takes the direct path whatever the open-turn state, because a `steer` runtime may be holding an inbox delivery behind the pending calls.

**Retraction [design, S8].** A runtime that no longer wants some or all of its pending calls retracts them by their runtime ids with the `retract` frame of "MD-11". On REST and MCP the calls are cancelled and the client receives a retraction event. On provider surfaces the answer is `too_late`, the client still sends results, and the runtime ignores them.

**Cold-history ordering [design, S6, MDR-7].** The cold-history builder, which the client-tools design's CT-7 and AM-4 use, places each entry by the turn and the position at which the runtime consumed it, from `consumed` frames and `response.consumed`. An entry consumed while client calls were pending moves to after the entry that carries their results, as CT-7 "Order of history entries" requires, and entries that move keep their relative order. An entry reported only on a `response` is placed after the output that turn streamed before the `response`, and before the `response`'s own output. The builder never places an entry between an assistant entry that carries calls and the entry that carries their results.

## MD-11 Retraction

The owner settled S8 on 2026-11-09: a runtime may retract any message it generated. Retraction is one operation in the delivery machinery, keyed by id, and it changes only what no recipient has received **[design, S8, MDR-10]**.

### Request and result

A runtime writes a `retract` frame on `CH-MSGSOCK`, inside or outside a turn:

```json
{ "type": "retract", "sessionId": "sess_01J9...", "retractId": "rt_1", "ids": ["tc_7", "msg_01J9X"] }
```

The adapter relays it on `CH-ATTACH` to the stream of the session it names, as a session-scoped frame ("Blast radius"). The gateway answers with a `retract_result` frame, which the adapter writes to the runtime as it writes every envelope the gateway sends:

```json
{
  "type": "retract_result",
  "sessionId": "sess_01J9...",
  "retractId": "rt_1",
  "results": [
    { "id": "tc_7", "result": "too_late" },
    { "id": "msg_01J9X", "result": "retracted" }
  ]
}
```

- `results` holds one entry per requested id, in request order, with `result` set to `retracted`, `too_late`, or `unknown`.
- `retracted`: the item will never reach its recipient. A repeated retraction of an id already retracted answers `retracted`, so a runtime may retry after a lost answer.
- `too_late`: the recipient already received the item, the item reached a final state another way, or the surface cannot withdraw it. Nothing changes.
- `unknown`: the id names nothing this session may retract (MDR-11).
- The frames add no channel, so the §28.3 registers and the naming law are unaffected. A runtime that never writes `retract` never receives `retract_result`.

### What a runtime may retract

| Item | Id the runtime names | `retracted` when | `too_late` when | Effect of `retracted` |
| :-- | :-- | :-- | :-- | :-- |
| A pending client tool call on a REST or MCP session | The call's runtime `id` from `clientToolCalls` | The call is still in the session's pending set. | A results message cleared the set first. | The gateway removes the call from the pending set with the conditional write [CT-5](client-tools-design.md#ct-5-pending-calls-and-the-hold-rule) uses for results, and emits a `tool_use` event with `target: "client"` and phase `retracted`. The next results message answers exactly the calls that remain, and one that names a retracted call is refused with `409 CLIENT_TOOL_CALLS_RETRACTED`. When no call remains, the session has no pending calls. |
| A client tool call on a provider surface (Messages, Chat Completions, or Responses) | The call's runtime `id` | Never. | Always. | None. A returned call cannot be withdrawn on these surfaces: the pending set stays, the client sends results as the provider's API requires, and the runtime ignores them. |
| A message the runtime sent to another session through `lenny/send_message`, to its parent, a child, or a sibling | The `messageId` of the receipt, or the id the runtime supplied | The message is `pending` in the recipient's inbox or DLQ. | The message is in any other state: `delivered`, `consumed`, `expired`, or `dropped`. | The entry moves to `retracted` and leaves the store, it is never delivered, and the recipient's event stream receives `message_state` with `retracted` and no content. |
| Output and session events | None, because output parts and events carry no id | Never in v1. | Not applicable. | Ignored for now ("Blast radius"). Output already streamed to a client cannot be recalled. |
| Anything else: a child's `task` from `lenny/delegate_task`, a `tool_call`, another sender's message, or an id the gateway does not know | Any | Never. | Never. | `unknown`. A runtime stops a child with `lenny/cancel_child`. |

A retracted `interrupt` message is never delivered, and an interrupt the gateway already requested for it proceeds, because the runtime may already be stopping.

### Ordering

A message retraction runs on the recipient's coordinating replica under the recipient's delivery lock, which the message paths and the delivery of held messages also take ("MD-6"), so a retraction and a write of the same entry never interleave: the one that takes the lock first wins. The gateway routes a retraction to the recipient as it routes the `lenny/send_message` call that sent the message, including the forwarding to a coordinating replica on another gateway replica. A client-call retraction and a results message race on the session row's conditional write, and exactly one of them takes effect.

### Audit and interceptors

Each retraction that changes state writes the audit event `message.retracted`, or `client_tool_call.retracted` for a call, naming the retracting session and principal, the item's id and kind, the recipient session, the surface, and the result (MDR-12). The counter `lenny_retraction_total{kind, result}` counts every result, including `too_late` and `unknown`, so an operator can see a runtime that retracts often or names ids it does not own. Audit records carry no message content.

No interceptor phase runs on a retraction. A retraction removes content from a recipient's path and adds none, and the retracted content already passed `PreMessageDelivery` for a message, or `PostAgentOutput` for a call's arguments, when it was sent. A deployer who needs to observe retractions reads the audit event.

### Blast radius

The owner asked for retraction of messages other than client calls if its blast radius is small. It is small for messages, because "MD-3" already gives every inbox entry a state and a lock-held transition, and a runtime's message to another session lives in one place:

| Component | Change | Size |
| :-- | :-- | :-- |
| Inbox store (`sessioninbox`, in memory, on the Redis list, and in the DLQ sorted set) | The `retracted` state and one transition from `pending`. Removing a `pending` entry uses the removal the store already performs at delivery and expiry. | Small |
| Outbound queue | None. Lenny has no outbound queue: a runtime's `lenny/send_message` lands in the recipient's inbox or DLQ, so a retraction has one place to look. | None |
| Routing to the recipient | The retraction reuses the send path's classification read and its forwarding to the coordinating replica. | Small |
| Session row (client tools) | Removal of ids from the pending set by the conditional write CT-5 defines. | Small |
| Executor | The reader routes `retract` to a retraction handler, and the writer sends `retract_result`. | Small |
| Adapter | `retract` joins `sessionScopedFrameTypes` in `pkg/adapter/attach.go`, so the demultiplexer relays it to its own session's stream alone. Without the entry, a frame type outside the set passes through to every Attach stream on the pod as a protocol-level frame. `attachRecvLoop` writes `retract_result` as it writes every envelope the gateway sends. | Small |
| Runtime SDKs | A `Retract` call ("MD-12"). | Small |
| Audit, events, and metrics | The audit events `message.retracted` and `client_tool_call.retracted`, the `retracted` value on `message_state` and `tool_use`, and `lenny_retraction_total`. | Small |

Output and session events are not small, so they are ignored for now. Output parts and events carry no id a runtime could name. An `output` event fans out at once to every outbound channel of the session, enters the event store's replay sequence, and enters the transcript, and the provider adapters fold it into response records. Recalling one part would need ids on parts, a tombstone in the replay sequence, and a rule for channels that already received it. "MD-14" lists it as a future case.

## MD-12 Runtime SDKs

**Drain call [design].** The Go, Python, and TypeScript SDKs add a call on the handler's context, `DrainMessages(ctx) []Message` in Go, `drain_messages()` in Python, and `drainMessages()` in TypeScript. It returns, in arrival order, the messages that reached the runtime for the session while the handler runs, and removes them from the session's queue. A model loop calls it between model steps, after the step's tool results are in hand, and appends what it returns after those results. The SDK treats every returned message as consumed, writes a `consumed` frame for their ids, and keeps them out of a later `OnMessage`.

**Consumption reporting [design].** The SDK writes `consumed` on the `response` it writes for an `OnMessage` call, listing that call's message and any drained message not yet reported. A handler that declares `steer` and never calls the drain call degrades to queue-until-turn-end inside the SDK: the queued messages wait for the next `OnMessage`, and the runtime is still correct.

**Deduplication [design].** The SDK keeps the ids each session consumed, bounded to the last 1000, and drops a message whose id is among them. A runtime that persists its own history keeps the set with it, so a restored runtime drops the redeliveries "MD-3" sends.

**Unsolicited responses [design].** The SDK adds `Respond(ctx, sessionID, Reply)`, which writes a `response` outside an `OnMessage` call, for a runtime that finishes background work and wants to report it.

**Retraction [design, S8].** The SDK adds `Retract(ctx, sessionID, ids) ([]RetractResult, error)` in Go, and `retract()` in Python and TypeScript. It writes a `retract` frame with a fresh `retractId`, waits for the matching `retract_result` under the caller's context, and returns one result per id. A context that ends first returns an error, and a retry is safe because retraction is idempotent.

**Busy reporting [design].** A handler that holds a drained message across a wait for client results takes a busy hold only while it works. Holding a message is not work, so the session idles and suspends as §15.7 states.

## MD-13 Reference runtimes

The tree holds no reference-runtime adapter source, so each change is a declaration in §26 and `pkg/embedded/stack/catalog.go`, and an adapter change in the runtime's own repository **[design]**:

| Runtime | Declared intents | Default | Basis |
| :-- | :-- | :-- | :-- |
| `claude-code` | `steer`, `queue`, `interrupt` | `steer` | The adapter runs Claude Code with `--input-format stream-json` and writes each message as an `SDKUserMessage` with `priority` `next` for `steer` and `later` for `queue`. Claude Code reads a `next` message after the running tool calls finish **[external]**. |
| `codex` | `steer`, `queue`, `interrupt` | `steer` | The adapter calls the Codex app-server: `turn/start` without an active turn, `turn/steer` with `expectedTurnId` during one, and `turn/start` again when a steer finds the turn already ended. A review or compaction turn refuses steering, and the adapter holds the message until that turn ends **[external]**. |
| `chat` | `steer`, `queue`, `interrupt` | `steer` | Lenny owns this runtime's loop, which calls the drain call before each model call. |
| `cursor-cli` | `queue`, `interrupt` | `queue` | Cursor documents steering for its interactive CLI and its Cloud Agents API. A steering interface for `cursor-agent` run headless was not found **[unverified]**. |
| `gemini-cli` | `queue`, `interrupt` | `queue` | Gemini CLI's steering is behind an experimental setting that is off by default **[external]**. |
| `langgraph`, `mastra`, `openai-assistants`, and `crewai` | `queue`, `interrupt` | `queue` | Their frameworks' mid-run input was not checked for these runtimes, and the LangGraph open-source framework accepts no input into a running graph **[external]**. |

Each runtime that declares `interrupt` handles `interrupt_request` today, because each declares `immediate`. §26.3 "Bootstrap behavior" gains the stream-json invocation and the mid-turn behaviour, and §26.2 states the default for the coding-agent runtimes.

## MD-14 Future use cases and the extension rule

**Cases the machinery covers [design].** None ships in v1.

| Case | How it fits |
| :-- | :-- |
| Scheduled delivery | A `deliverAt` member keeps a message `pending` until a time. The gateway alone acts on it, so no runtime declares it. |
| Priority ordering | A `priority` member orders `pending` entries of one intent. Gateway-only. |
| Context-only messages | An intent `append` delivers a message the runtime adds to its history without starting a turn, as the Claude Agent SDK's `shouldQuery: false` does. Declared, because the runtime acts on it. |
| Background, then steer | An intent that asks the runtime to move its running work to the background and read the message in the same turn, as Claude Code's `now` does with a human origin. Declared. |
| Child completion and system notices | `system` messages from the gateway, such as a child's completion, a budget warning, or a deadline, delivered with the target's default intent, as Claude Code folds task notifications into a turn. |
| Consumption notices to senders | A `message_state` event on the sending session's stream for every state change, beyond the move to `consumed_unrecorded` that v1 already reports there, and a notice when a target goes idle, as Claude Code's `notify_when_idle` does. |
| Replacement | An intent that supersedes the sender's earlier `pending` message with a new one. It uses the `superseded` state. |
| Retraction of output | Ids on output parts and events, a tombstone in the event replay sequence, and a rule for channels that already received a part, so a runtime can recall output the gateway has not yet sent to a client. S8 leaves it for later ("MD-11"). |
| External participants | Connector messages with `from.kind: external`, steered into a running session. |

**Extension rule [design, G1].** An addition to the machinery satisfies all of these:

1. A behaviour that applies whatever the intent, such as a delivery time or an order, is its own envelope member. A behaviour that changes when the runtime receives a message relative to its turn is an intent.
2. An intent states the gateway's action for each turn state of "MD-5" and the receipt it gives.
3. An intent the runtime must act on is declared in `injection.modes`, and the gateway sends it only to a declaring revision. A gateway-only intent is never declared and is listed in the §15.4.1 enum alone.
4. An intent states the runtime's obligation, including where the message goes in model history and how its consumption is reported.
5. An intent uses the states of "MD-3", and adds a state only when no existing state expresses its outcome. A new state states its transitions and its receipt.
6. The enum stays closed: an unknown intent is refused with `400 INVALID_DELIVERY_VALUE`, and registration refuses an unknown declared intent.
7. A new intent comes with a conformance case in §15.4.6 and a mapping row on each surface that exposes it.

## Worked example

Alice, of tenant `acme`, runs the `claude-code` runtime through REST. Its revision declares `modes: [steer, queue, interrupt]`.

1. Alice sends "Run the migration against the dev database." No turn is open, so the message `msg_A` takes the direct path, its receipt is `delivered`, and a turn opens. Claude Code starts running shell commands.
2. Alice sends "Use the staging database instead." with no `delivery`. The default is `steer`, a turn is open, and the steer path writes `msg_B` at once. The receipt is `delivered`. Claude Code reads it when the running command finishes, and the adapter writes `consumed` with `msg_B`. Alice's event stream shows `message_state` `consumed` for `msg_B`.
3. Alice sends "Afterwards, write a summary." with `delivery: "queue"`. The queue path holds `msg_C`, and the receipt is `queued`.
4. A child session that the agent started sends "Schema check passed." through `lenny/send_message` with no `delivery`, which steers it into the running turn. The child's call returns `delivered` at once.
5. The agent had earlier sent its other child, which is suspended waiting for a resume, "Also run the slow integration suite." as `msg_D`, held `pending` in that child's DLQ. Having switched databases, the agent writes `retract` with `msg_D`. The entry is still `pending`, so it becomes `retracted`, the agent receives `retracted`, and the audit log records `message.retracted`. A second id in the same frame names a message the first child already consumed, and its result is `too_late`.
6. The runtime ends the turn with a `response` whose `consumed` lists `msg_A` and the child's message. Alice receives `turn_end` with turn 1 and those ids. The turn end makes `msg_C` eligible, the gateway writes it, and turn 2 starts.
7. The pod is lost during turn 2 after the runtime consumed `msg_C`, and before a checkpoint recorded it. The session resumes from the checkpoint taken after turn 1, whose consumed set ends with the child's message. The gateway recorded the consumption of `msg_C` after that message, so under O4 `msg_C` is not redelivered and moves to `consumed_unrecorded`. Alice's event stream shows `message_state` with `consumed_unrecorded` for `msg_C`, and she sends "Afterwards, write a summary." again as a new message, which the restored runtime takes as a new turn.

A second example covers a provider surface under O3. Bob, of tenant `acme`, uses the Anthropic SDK against a runtime whose revision declares `steer`, `conversationInput`, and `clientTools`, on a pool with `conversationContinuity: "platform"`.

1. Bob sends a request with a client tool `open_ticket` and "Investigate the failing build." The runtime delegates a child to bisect the failure and replies "I started a bisect." The gateway returns the reply, stores the digest, and holds the session.
2. The child finishes and sends the parent "The regression is in commit 4f2a." No turn is open, so the message takes the direct path and opens a turn. Its envelope carries no `conversation.clientTools`. The runtime writes a summary of the finding, which reaches the transcript and the session's REST and MCP event streams. No Messages response carries it, and the turn writes no response record and no digest.
3. Bob sends his next request while that turn runs. Its history up to the last assistant message matches the stored digest, so it is a warm turn, and its message takes the steer path with its `conversation.clientTools`. The runtime writes `consumed` for the message, answers, and ends the turn with a call to `open_ticket`. Bob's response carries the output that followed the `consumed` frame and a `tool_use` block, with `stop_reason: "tool_use"`.

## Limits

- **Runtime cooperation.** `steer` works only as well as the runtime's placement of messages. The gateway cannot check that a runtime places a message after tool results, and the conformance cases of §15.4.6 test it against recorded model requests.
- **In-memory inbox.** Custody survives a coordinator crash only with the durable inbox. With `messaging.durableInbox: false`, the documented loss window of §7.2 applies to `pending` and `delivered` entries alike.
- **Duplicates after a restore.** Consumed messages are never redelivered, so a duplicate arises only from a redelivered `delivered` entry that the restored runtime already holds: one it consumed before the checkpoint and had not yet reported. A runtime that does not persist its consumed ids with its history can read such a message twice. The SDK's set covers a runtime whose history the SDK holds, and the adapter's checkpointed set covers a consumption the runtime reported before the checkpoint, up to its bound of 1000 ids.
- **Consumption after the last checkpoint is not replayed.** Under O4, a runtime restored from a checkpoint lacks the messages it consumed after that checkpoint, and the gateway reports them `consumed_unrecorded` in place of resending them. A sender that does not read the event loses the message's effect on the runtime's history.
- **Late consumption reports.** A runtime that declares `queue` alone reports consumption only on `response`, so its messages stay `delivered` for the whole turn.
- **Agent interrupts.** Any sender within the messaging scope can send `interrupt` to a target that declares it, so a sibling can stop another's turn. Rate limits and scope bound it, and the audit record names the sender.
- **Provider surfaces do not steer.** A user of a Chat Completions, Responses, or Messages client cannot correct a running turn, because those APIs have no request for it.
- **Other turns are outside provider responses.** Under O3, the output of a turn that no client request caused reaches the transcript and the REST and MCP event streams, and no provider-surface response. A client that reads only its provider responses learns of such a turn only when the runtime refers to it in a later turn the client caused. A cold continuation on the Messages API takes its history from the client, so it lacks such a turn.
- **A client request can wait for another turn.** Under O3, on a runtime that does not declare `steer`, a client request that arrives during a turn another session's message caused waits for that turn's `response`, and a non-streaming client's timeout bounds the wait.
- **Retraction is not recall.** A retraction succeeds only before delivery. A message a recipient received, a client tool call a provider-surface client holds, and output already streamed stay where they are, and the runtime receives `too_late`. A REST or MCP client that already started a retracted call's tool learns of the retraction only from the event, and the tool's side effects stand.
- **Output is not retracted.** Under S8, retraction of output and session events is ignored for now ("MD-11", "Blast radius").

## Alternatives rejected

- **A steer special case.** A boolean `steer` beside `immediate` and `queued` would leave queueing, interruption, and later intents as separate rules. The owner directed a general per-message model.
- **Steer by default for every runtime.** A runtime that does not read its input mid-turn would receive messages its SDK queues, which matches `queue` while reporting `delivered`, and a Basic runtime that reads one message per turn would gain nothing.
- **Delivering mid-turn to every runtime between tool calls.** The adapter cannot observe tool calls inside a runtime's own process, and §29.3 records that no frame carries the signal.
- **One `response` per `message`, with steered messages folded into the next `response`.** A runtime that consumes several messages in one turn would owe several `response` frames, and output with no causing message would still be discarded.
- **A send call that waits for the consuming turn.** A REST caller would wait for a turn that may run many minutes, and an agent's `lenny/send_message` would block its own model loop. The event stream carries the outcome.
- **Holding inbox deliveries to a provider-surface session until the next client turn.** Revision 1 recommended it, so that a delivery would join work a client is waiting for. A message to an idle held session then waited for the client's next request or its TTL, and every provider surface needed a hold of its own. Delivery on arrival with the two rules of "MD-8" keeps the turn away from the client with no surface-specific hold (O3).
- **Custody until a checkpoint records the consumption.** Revision 1 recommended it, so that a restored runtime would receive again a message it consumed after its checkpoint. The redelivery can repeat side effects that the checkpoint does not undo, and a runtime that never checkpoints would keep every consumed message in the inbox until the session ends. The `consumed_unrecorded` report leaves the choice to resend with the sender (O4).
- **Gateway-side ordering of steered messages into model history.** The gateway does not see the runtime's model requests, and only the runtime knows where its model steps fall.

## Specification changes

| Section (by heading) | Change |
| :-- | :-- |
| §15.4.1 Message Format and Binary I/O Requirements, "`MessageEnvelope` — Unified Message Format" and "`delivery_receipt` acknowledgement schema" | `delivery` takes `steer`, `queue`, and `interrupt`, with the default rule and the refusal of an undeclared intent. `delivered` means written for the runtime. `reason: "delivery_mode_unsupported"`. The closing future-proof paragraph points to the extension rule. |
| §5.1 Runtime, the `capabilities` fields | `injection.modes` and `injection.defaultMode` defined, with the registration rules and the level requirement of `interrupt`. |
| §7.2 Interactive Session Model, "Message delivery routing", "Delivery of held messages", the session inbox definition, and the durable inbox rows | The named paths with successor pointers from the numbered ones, the open turn, inbox states and custody until consumption, redelivery of `delivered` entries, the `consumed_unrecorded` state, and the adapter's consumed set. `ready_for_input`, the delivery timeout, and the old overlap rule are removed. |
| §7.3 Retry and Resume | Redelivery of `delivered` entries after a resume, and the move to `consumed_unrecorded` of every message consumed after the checkpoint the session resumes from. |
| §28.5.3 Intra-pod, `CH-MSGSOCK` card | The `consumed` and `retract` frames from the runtime and the `retract_result` frame to it, `consumed` on `response`, the session-scoped frame list, the attribution rule, unsolicited responses, and the `steer` obligation under "Delivery semantics". No channel is added, so the §28.3 registers and the naming law are unaffected. |
| §7.2 Interactive Session Model, a new "Retraction" paragraph | The retraction operation, its results, what a runtime may retract, its ordering under the recipient's delivery lock, and the `retracted` state. |
| §15 External API Surface, "SessionEvent Kind Registry", "Open Responses conversation sessions", and "Built-in adapter single-shot compute model" | `message_state` and `turn_end`, with `consumed_unrecorded` on the recipient's and the sender's streams, `retracted` on `tool_use`, the provider-surface turn rule with `too_late` for a retracted call, delivery on arrival to a session created on a provider surface with the two rules of O3, and inbox deliveries in the Responses cold walk. |
| §15.1 REST API and its error code catalog | Per-message receipts, the removed `output`, the client retraction route of O5, `DELIVERY_MODE_UNSUPPORTED`, and `CLIENT_TOOL_CALLS_RETRACTED`. |
| §15.2 MCP API and §15.2.1 REST/MCP Consistency Contract | `delivery` on `send_message`, the receipt, the generated retraction tool, and contract-test coverage. |
| §8.5 Delegation Tools | `lenny/send_message` carries an intent, returns at once, and its message can be retracted while `pending`. |
| §11.7 Audit Logging | The `message.retracted` and `client_tool_call.retracted` events, without content. |
| §15.4.3 Runtime Integration Levels and §15.4.6 Conformance Test Suite | `steer` at every level, `interrupt` at Full, and conformance cases for placement after tool results, consumption reports, deduplication, and the drain call. |
| §15.7 Runtime Author SDKs | The drain call, consumption reporting, deduplication, `Respond`, and `Retract`. |
| §16.1 Metrics | `lenny_message_state_transitions_total`, `lenny_message_consumption_seconds`, `lenny_unsolicited_response_total`, and `lenny_retraction_total`, with the `runtime_revision` label RV-2 adds where a metric names a runtime. |
| §24 `lenny` command reference | `--delivery` on `lenny session send`. |
| §26 Reference Runtime Catalog, §26.1 to §26.11 | The declarations of "MD-13" and the `claude-code` bootstrap. |
| §29.3 Interactive message send | Step 8 rewritten, the steps that cite path numbers retargeted, and the `deliverNow` wait removed from the scenario. |

## Non-specification changes

- `pkg/gateway/session/executor`: `Deliver`, the frame reader's routing, `AwaitConsumingTurn`, and the removal of the turn token from `attachConn`. `Send` is removed, and every caller moves to `Deliver` and the waiter.
- `pkg/gateway/session/messagerouting`: the named paths, the intent, and the open-turn input to `Classify`.
- `pkg/gateway/session/sessioninbox`: entry states including `retracted`, the delivered hash in Redis, redelivery reads, the release at consumption, and the conditional removal of a `pending` entry. The transcript row records the consumption order and the move to `consumed_unrecorded` on resume.
- `pkg/gateway/sessionserver` and `pkg/gateway/mcpfabric/mcptools`: the lock scope, per-message receipts, the retraction handler and its routing to the recipient's coordinating replica, the client retraction route, intent refusals, the events, and the audit events.
- `pkg/gateway/session/clienttools`, the package the client-tools design adds: removal of retracted calls from the pending set, and `too_late` on provider surfaces.
- `pkg/gateway/environment/translator`: the turn waiter on every provider surface, the steer or queue path for a request that finds another turn open, the response cut at the consumption of the request's message, and no response record or digest for a turn that no client request caused (O3).
- `pkg/adapter`: `consumed` and `retract` in `sessionScopedFrameTypes`, the per-session written and consumed id sets, the consumed set in the checkpoint in report order, and the `consumed` frame it relays for a redelivered id in that set.
- `pkg/apis/lenny/v1alpha1/runtime_types.go`, `pkg/gateway/runtime/runtimestore`, `pkg/gateway/externalapi/admin/runtimes.go`, `pkg/controller/runtime`, and the capability override: the new values, `defaultMode`, the registration rules, and the corrected CRD comment.
- `schemas/lenny-adapter-jsonl.schema.json`: the `consumed`, `retract`, and `retract_result` frames and the `response` member.
- `sdks/runtime/go`, `sdks/runtime/python`, and `sdks/runtime/typescript`: "MD-12".
- `sdks/client/go/lenny` and the CLI: receipts and output from events.
- `pkg/embedded/stack/catalog.go`: the reference declarations.
- `docs/`: the runtime author guide on intents and the drain call, and the REST and MCP pages.

## Defects found while verifying

These defects were found by reading the code and the specification on 2026-11-09. Nothing was run. The owner decided that they are fixed later under a holistic plan rather than one at a time. MD-A replaces the mechanisms they concern, so the plan can close them with it.

| ID | Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- | :-- |
| MDF-1 | The executor pairs each `message` with one `response` through a turn token, and discards a `response` with no registered turn. A runtime cannot consume two messages in one turn or report output that no message caused. | `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go`; `attachConn.token`, `acquireTurn`, and `completeTurn` in `podstream.go`. | Filed in BUILD-GAPS.md as F-28.5.3.4 |
| MDF-2 | The REST and MCP send paths hold the session's delivery lock across the whole turn, so a message sent mid-turn waits for the turn to end instead of being buffered with a `queued` receipt or delivered, and a parent's `lenny/send_message` blocks until the child's turn ends. | `Server.deliverMessageBatch` and `restDelivery.deliverNow` in `pkg/gateway/sessionserver/messages_delivery.go`; `mcpSend.deliver` and `mcpSend.sendNow` in `pkg/gateway/mcpfabric/mcptools/send_delivery.go`. | Filed in BUILD-GAPS.md as F-7.2.47 |
| MDF-3 | `Classify` merges path 5 into path 2 and lacks path 4: a running session always takes direct delivery, and `delivery: "immediate"` is never acted on. The MCP send path passes no `delivery` value at all. | `Classify` in `pkg/gateway/session/messagerouting/messagerouting.go`; its callers in `messages_delivery.go` and `send_delivery.go`. | Filed in BUILD-GAPS.md as F-7.2.48 |
| MDF-4 | `capabilities.injection.modes` is declared in §5.1 and §26 and defined nowhere. The CRD comment gives it a third meaning, and no code reads it beyond validation and the controller mirror. | §5.1 "Runtime"; `InjectionCapabilityCRD.Modes` in `pkg/apis/lenny/v1alpha1/runtime_types.go`; `validateCapabilities` in `pkg/gateway/externalapi/admin/runtimes.go`. | Filed in BUILD-GAPS.md as F-5.1.38 |
| MDF-5 | `ready_for_input` is normative in §7.2 and absent from the `CH-MSGSOCK` frames, so path 2 and path 5 cannot be told apart on the wire. §29.3 step 8 records the gap. | §7.2 "`ready_for_input` — normative definition"; §28.5.3 `CH-MSGSOCK` **Messages.**; §29.3 step 8. | Filed in BUILD-GAPS.md as F-7.2.49 |

Further observations bear on this design and are recorded for the specification owner without filing:

- The durable inbox's "Explicit ACK" row requires the adapter to keep a `delivered_message_ids` set in checkpoint state. No code in `pkg/adapter` keeps one, and `lenny_inbox_duplicate_suppressed_total` has a catalog entry and no producer. "MD-3" replaces the set with the consumed set.
- §26.3 declares `modes: [immediate, queued]` for `claude-code` and states no behaviour for a message that reaches it while `claude` works.
- The REST message response returns the turn's `output` synchronously, which the Go client SDK reads. "MD-7" removes it.
- The §5.1 "Standalone Runtime" example declares `interaction: one_shot` with `injection.modes`, although a `one_shot` runtime consumes one message.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | `Classify` for every named path and turn state, each intent, `input_required`, and results messages. The default-intent resolution, the override narrowing, and the refusal of an undeclared intent. Inbox state transitions, including every refused transition, `pending` to `retracted`, and `consumed` to `consumed_unrecorded` against a checkpoint's consumed set with a recorded newest id, with an unrecorded newest id, and empty. Rule 1 of "MD-8": no `conversation.clientTools` on an envelope that opens a turn no client request caused, and `no_tools_declared` for calls in such a turn. Rule 2: the response cut at the consumption of the request's message, from a `consumed` frame and from `response.consumed`. Attribution of a `response` without `consumed`, unsolicited responses, and unknown consumed ids. The cold-history ordering rule with entries consumed during pending calls. Registration rules for `modes` and `defaultMode`. The retraction result for each row of the "MD-11" table: a pending call, a call answered first, a call on each provider surface, a `pending`, `delivered`, `consumed`, `expired`, and `dropped` message, a child's `task`, another sender's message, an unknown id, a repeated id, and an empty `ids` list. The SDK drain call, consumption reporting, deduplication, and `Retract` in each language. |
| 3 | The JSONL schema for the `consumed`, `retract`, and `retract_result` frames and the `response` member against the runtime SDKs. Per-message receipts and the client retraction route against the OpenAPI document and the generated MCP tools. The `message_state` and `turn_end` payloads, `message_state` with `consumed_unrecorded` and its reason, and `tool_use` with phase `retracted`. |
| 4 | On the compose stack: a steered REST message during a running turn returns `delivered` at once and appears in `turn_end.consumed`; a queued message waits for the turn end; an interrupt with acknowledgement and with a timeout; `lenny/send_message` from a parent returns before the child's turn ends; a provider-surface turn that consumes an inbox delivery. Under O3, on held Messages API and Responses sessions: a child's message between client requests is delivered on arrival, its envelope carries no `conversation.clientTools`, and its output reaches the REST and MCP event streams and no response; the client's next request is answered with its own turn's output, steered into the running turn on a `steer` runtime and waiting for its `response` on a `queue` runtime; a client request with client tools steered into such a turn returns calls to them; a tool-use approval in such a turn resolves through the approvals API; `lenny/request_input` in such a turn is refused; the next request's digest still matches. Retraction: a REST client receives the `tool_use` retraction event and a later results message naming the call is refused with `409 CLIENT_TOOL_CALLS_RETRACTED`; a partial retraction leaves the remaining calls answerable; a Responses, a Chat Completions, and a Messages API session answer `too_late` and still deliver the client's results; a parent's message pending in a child's DLQ is retracted and never delivered, and the child's stream receives `message_state` `retracted`; a delivered message answers `too_late`; each state-changing retraction writes its audit event; a client retraction under O5. |
| 5 | A resume on Kind with `delivered` entries redelivered and deduplicated, including an entry whose consumption report the pod loss cut off, no consumed message redelivered, and `consumed_unrecorded` events on the recipient's and the sender's streams. A coordinator handoff with the durable inbox. A retraction of a message to a recipient coordinated by another gateway replica. |
| 7a | Concurrent steer, queue, and interrupt messages to one session keep acceptance order per intent. A retraction racing the write of the same entry at a turn end, and racing a resume's delivery of held messages: exactly one wins, and the result matches the outcome. A client-call retraction racing a results message: exactly one takes effect. A send racing a resume. No send waits on the delivery lock for a turn. A provider-surface request racing an inbox delivery to an idle held session: whichever is written first opens the turn, and the response carries only output from the consumption of the request's message. |
| 8 | Pod loss after consumption and before a checkpoint, with no redelivery and a `consumed_unrecorded` event under O4. Redis loss with the durable inbox, including a retraction while Redis is unreachable, which answers an error to the SDK call and changes nothing. Coordinator crash with `delivered` entries. |
| 9 | `interrupt` from an agent outside the messaging scope, an intent override that tries to bypass a tenant override, a retraction naming another session's message or another tenant's message (`unknown`, with no difference in timing or result between an existing and a missing id), a client retraction of another user's message, a `retract` frame on a pod with several slots that carries another session's `sessionId`, absence of content in retraction audit records and events, and `from.kind` overwrite by the adapter. |
| 10 | Conformance for a `steer` runtime: it consumes a message during a tool call and places it after the result in its recorded model request, reports consumption, drops a redelivered id, and accepts a message while client calls are pending. A `steer` runtime that receives a client request steered into a turn another session's message caused writes `consumed` for it before the output that answers it. Conformance for `interrupt`, for a `queue`-only Basic runtime, and for a runtime that retracts its pending client calls and then ignores results answered `too_late`. |
| 11 | The runtime author guide's intent table and drain example against the SDKs, and the REST page's receipts against the gateway. |

## Validation owed

- Whether Claude Code run with `--input-format stream-json` honours `priority` on each `SDKUserMessage` written to stdin, and how it places a folded message after `tool_result` blocks. The documentation states the placement for the interactive mode.
- Whether the Python Agent SDK documents `priority`, which the Python pages did not state.
- A steering interface for `cursor-agent` run headless.
- Whether Gemini CLI's model steering leaves its experimental status, and how a hint travels with `functionResponse` parts in a headless run.
- Mid-run input in Mastra, the OpenAI Assistants API, and CrewAI.
- Whether OpenAI's Responses API states an adjacency rule for function call outputs, which the investigation found only for Chat Completions through a third-party error page.

## Instructions for reviewers of other designs

- [`client-tools-design.md`](client-tools-design.md): revision 2 already scopes T5's hold, that design's O3, and CTR-4 to runtimes that do not declare `steer`, adds T9's `abandonPendingToolCalls`, and adds "Order of history entries". What remains: its "Out of scope" entry that calls retraction an open decision of this design changes to the settled S8. CT-5's hold rule cites the queue path in place of path 5, its exception delivers held messages at the turn end in place of "the runtime's next `ready_for_input`", and its "Terms" entries for `ready_for_input` and the delivery paths take the named paths (O1). A T9 message is a results message that takes the direct path. "Order of history entries" takes the consumption positions of "MD-10". Under S8, a runtime retracts pending calls by runtime id with the `retract` frame of "MD-11": CT-5 gains the removal of retracted calls from the pending set and the rule that a results message answers the calls that remain, the `tool_use` event gains phase `retracted`, the error catalog gains `CLIENT_TOOL_CALLS_RETRACTED`, and CT-6 states that a provider surface answers `too_late` and keeps the pending set. The `error` member of `response`, which that design records as unread, is read under "MD-4". Under O3, client tools are offered only in turns a client request caused: a turn caused by another session's message carries no `conversation.clientTools`, and a client request steered into such a turn carries its own. Revision 4 of that design records the rule as T16 and states it where it discusses inbox deliveries during pending calls.
- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): the adapter waits with `AwaitConsumingTurn` and writes through the paths "MD-8" states. AM-5's in-flight join and warm conditions are unchanged. AM-4 entries gain an optional `from` member for a consumed inbox delivery in a cold history built by the gateway, and the Messages API itself adds none because the client holds the history. Under O3, an inbox delivery to a held session is delivered on arrival. Revision 4 of that design states the two rules under AM-5 "Holding a session" and AM-7: a turn that no client request caused carries no client tools and never appears in a Messages response, the next response is the output of the client's own turn, and the conversation digest is unaffected. A retraction of a call returned on this surface answers `too_late`.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): RV-1 lists `injection.defaultMode` as versioned and gateway-read, and its override rule for `injection.modes` keeps `queue` and lets `defaultMode` narrow. The session metrics list gains the metrics of this design with `runtime_revision`.
- [`session-derive-design.md`](session-derive-design.md): SD-1's transcript delivery state takes the states of "MD-3" and records the intent. A derived session inherits no inbox entry of its source.
- [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md): the checkpoint records the adapter's consumed id set in report order, and IC-4's catalog makes it readable to the coordinating replica on resume, which moves every message consumed after the checkpoint to `consumed_unrecorded` (O4). Custody ends at consumption, so no consumed message waits for a checkpoint.
- [`security-oversight-design.md`](security-oversight-design.md): a `steer` runtime receives messages while SO-5a holds one of its tool calls for approval, and a message never resolves an approval. No change to SO-5a. The retraction audit events of "MD-11" join the audit catalog, and no interceptor phase runs on a retraction.
- [`delegation-availability-design.md`](delegation-availability-design.md): no change. `lenny/send_message` keeps its availability row, and its call no longer blocks on the target's turn.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md): no change. The generated `send_message` arguments gain `delivery`, and the client retraction tool of O5 is generated from the REST route.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| MD-A | The delivery core: MD-1 to MD-7, MD-9, MD-12, and MD-14, and the message rows of MD-11 (the `retract` and `retract_result` frames, the `retracted` state, routing, audit, and the SDK call), with the specification changes to §5.1, §7.2, §7.3, §8.5, §11.7, §15.1, §15.2, §15.4, §15.7, §16.1, §24, §28.5.3, and §29.3. It replaces the mechanisms of MDF-1 to MDF-5 under the owner's holistic plan. | None. |
| MD-B | Provider surfaces and client tools: MD-8 and MD-10, the client-call rows of MD-11, and the cold-history ordering rule. | MD-A, AM-A of the Messages design, and CT-A of the client-tools design. |
| MD-C | Reference runtimes: MD-13, with the adapter changes in each runtime's repository. | MD-A. |

## Sources

Each page below was read on 2026-11-09. The investigation of the same date read the remaining harness sources it cites, and this design relies on its reading for Gemini CLI, LangGraph, and the Vercel AI SDK.

- Claude Agent SDK: [TypeScript reference](https://code.claude.com/docs/en/agent-sdk/typescript), `SDKUserMessage.priority`, `shouldQuery`, and `interrupt()`.
- Claude Code: [Cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging) and [Interactive mode](https://code.claude.com/docs/en/interactive-mode).
- Anthropic: [Handle tool calls](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls).
- OpenAI Codex: [App server](https://learn.chatgpt.com/docs/app-server), `turn/steer` and `turn/interrupt`, and [`codex-rs/core/src/session/turn_input.rs`](https://github.com/openai/codex/blob/5b98eaafeb341225d3724a6112a93c993cc09613/codex-rs/core/src/session/turn_input.rs).
- Cursor: [Cloud Agents API endpoints](https://cursor.com/docs/cloud-agent/api/endpoints) and [Agent overview](https://cursor.com/docs/agent/overview).
- Gemini CLI: [Model steering](https://github.com/google-gemini/gemini-cli/blob/1930f25394c4b6338b64ffdb43083fca41614798/docs/cli/model-steering.md).
- LangGraph: [Double texting](https://docs.langchain.com/langgraph-platform/double-texting).

## Review history

No review has read this design.

### Revision notes

- Revision 1, 2026-11-09: first draft, built from the investigation of 2026-11-09 into messages that arrive while a runtime works and the owner's decisions of the same date on it, with the investigation's tree and specification claims re-verified on branch `design/v1-cleanup`. Re-verification added that the MCP send path passes no `delivery` value to `Classify`, that the adapter keeps no `delivered_message_ids` set although §7.2 requires one, and that §29.3 step 8 already records the missing `ready_for_input` and consumption signals. The external claims the design rests on were re-read. The defects are cited as filed in `BUILD-GAPS.md` with IDs pending, because the coordinator was filing them on 2026-11-09. While the draft was written, the owner settled S8, which the draft had carried as open decision O1, as a general retraction operation: the draft gained "MD-11", the `retracted` state, the `retract` and `retract_result` frames, requirements MDR-10 to MDR-12, the retraction audit events, and their tests. The remaining open decisions were renumbered O1 to O5, and the client withdrawal decision became the client form of retraction. The draft then read against [`client-tools-design.md`](client-tools-design.md) revision 2, which had already taken the `steer` scoping, T9, and "Order of history entries", and its references and reviewer instructions were narrowed to what remains. Checking the adapter for the retraction blast radius found that a new runtime frame type has to join `sessionScopedFrameTypes`, or the demultiplexer passes it to every Attach stream on the pod.

- Revision 2, 2026-11-09: applies the owner's answers of 2026-11-09 on revision 1's open decisions, and no decision remains open.
  - O1, "ok": option (a). `ready_for_input` leaves the specification and a `queue` message waits for the turn's `response`. The decision row records why that is safe: the term existed to keep a message off stdin while tool calls are in flight, which the turn boundary covers more strictly; no frame ever carried it (F-7.2.49); §28.5.3 "Intra-pod" makes the stdout `response` "always required to signal task completion" and synthesizes `RUNTIME_CRASH` for a process that exits without one; mid-turn delivery becomes opt-in through `steer`; and replies to `lenny/request_input` and the hold during it are unchanged. "Scope" now cites O1, in place of O2, for the removal of the term.
  - O2, "ok": option (a), the `consumed` frame.
  - O3, a change from revision 1's recommendation. The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes". A message from another session to a session created on a provider surface is delivered on arrival under the ordinary intent rules. Rule 1 offers client tools only in turns a client request caused, and rule 2 keeps a turn that no client request caused out of every provider-surface response. A client request that arrives during such a turn is steered into it on a `steer` runtime and waits for its end on other runtimes. "MD-8" gains "Delivery on arrival" with both rules and "Scenarios", and the pending-until-the-next-client-turn behaviour is removed from "High-level design", "MD-8", "Limits", the specification and non-specification changes, the tests, and the reviewer instructions. "MD-9" and "MD-10" state the rule for a child's message and for a delivery while client calls are pending. Requirements MDR-13 and MDR-14 and the provider-surface worked example are added, and "Alternatives rejected" records revision 1's recommendation. Two points go beyond the owner's words and are stated for review: a client request steered into such a turn carries its own `conversation.clientTools`, so that turn may end with calls to them, and the response to that request carries the turn's output from the point the runtime consumed the request's message.
  - O4, "ok": option (a), in place of revision 1's recommendation of (b). Custody ends when the runtime reports consumption, because redelivering a consumed message after a restore can repeat side effects that a checkpoint cannot undo, for example a push, and because a runtime that never checkpoints would otherwise keep every consumed message until the session ends. The new state `consumed_unrecorded` replaces the redelivery: on a resume, the gateway reports each message consumed after the checkpoint so that a client or a parent can decide whether to resend it. "MD-3" changes custody, redelivery, and deduplication, the adapter's consumed set no longer filters consumed messages and keeps the duty that redelivered `delivered` entries still need, and "MD-7", the worked example, "Limits", requirement MDR-3 and the new MDR-15, the specification changes, the tests, "Validation owed", and the checkpoint reviewer instruction change to match, and "Alternatives rejected" records revision 1's recommendation.
  - O5, "ok": a client retracts its own `pending` message through `DELETE /v1/sessions/{id}/messages/{messageId}` and the generated MCP tool.
  - Multi-thread sessions, "ok to leave out of v1": recorded as G2, because no harness keeps separate concurrent conversations in one process, threads would multiply the per-thread delivery machinery, separate sessions and delegation cover the use cases, and `threadId` and `inReplyTo` already let a later version add threads with no schema change.
  - The design now reads against [`client-tools-design.md`](client-tools-design.md) revision 4, which records rule 1 as T16, and [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 4, which states both rules. The label row for the client-tools design's O3 now calls it settled, as that design's revision 3 records.
