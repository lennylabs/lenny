# Client-defined tools

Status: design for owner review, revision 11, 2026-11-15, with no independent review yet. Revision 11 applies the owner's decision of 2026-11-15 to remove connectors ("Yes, let's drop connectors.") under [`connector-removal-design.md`](connector-removal-design.md) revision 2: runtime tools, approvals, and reserved names name platform tools and the runtime's own tools, and connector tools and `deferredToolDiscovery` leave. Revision 10 applies the remaining instructions of [`rest-only-client-api-design.md`](rest-only-client-api-design.md) and its R16 under O12. Nothing here is built or staged. The owner chose on 2026-11-13 to move every client protocol adapter (MCP, A2A, OpenAI Chat Completions, Open Responses, and Anthropic Messages) out of Lenny into separate first-party repositories with optional Helm subcharts in front of the REST API (decision T17); [`rest-only-client-api-design.md`](rest-only-client-api-design.md) governs what leaves. Revision 9 applies two of that design's decisions, which the owner settled on 2026-11-14: under O8, in the owner's words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something", banners mark the adapter-facing sections and the parts of them that move to the adapters, and nothing is deleted or moved; under the simplified O3, which the owner answered "yes", the "Held session" term leaves. It reads against [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 12 together with the owner's decisions of 2026-11-09 on it, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 21, [`runtime-routing-design.md`](runtime-routing-design.md) revision 19, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 10, [`security-oversight-design.md`](security-oversight-design.md) revision 58, [`session-derive-design.md`](session-derive-design.md) revision 22, [`delegation-availability-design.md`](delegation-availability-design.md) revision 12, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 40, [`message-delivery-design.md`](message-delivery-design.md) revision 23, [`runtime-protocol-design.md`](runtime-protocol-design.md) revision 12, [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) revision 7, [`connector-removal-design.md`](connector-removal-design.md) revision 7, and [`rest-only-client-api-design.md`](rest-only-client-api-design.md). It is built from the read-only assessment of client-defined tools of 2026-11-09, and it re-verifies that assessment's claims against the tree, the specification, and the live external documentation. "Decisions" lists what the owner settled on 2026-11-09: T1 to T16, and O1 to O10. No decision is open. The design stages no specification edit. The changes go through `change-proposal` after review. Parts of the design carry the label CT-n, requirements carry CTR-n, decisions that began as open questions carry O-n, and the proposals that land the design carry CT-A and CT-B under "Proposal split and order". "Review history" at the end records the revisions.

> **Adapter project banner.** Under [`rest-only-client-api-design.md`](rest-only-client-api-design.md), Lenny serves a REST client API alone, and the protocol mappings of client tools move to the repositories of the MCP, OpenAI Chat Completions, Open Responses, and Anthropic Messages adapters. The owner settled this as O8 of that design on 2026-11-14, in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something", so nothing here is deleted or moved. The runtime contract, the gateway core, and the REST surface still govern Lenny: "CT-1", "CT-2", "CT-4", "CT-5", "CT-8" to "CT-10", and proposal CT-A apart from its Chat Completions and Responses mappings. The adapter-facing sections carry a banner of their own, which names the parts that move: "CT-3", "CT-6", "CT-7", and "Proposal split and order". Decisions T4, T15, O5, O9, and O10 move with those parts, and every other decision still governs Lenny.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external documentation page or in a public SDK repository on 2026-11-09. "Sources" lists the pages. |
| **[unverified]** | An external claim that the pages read on 2026-11-09 do not state. "Validation owed" lists each one. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

A client-defined tool is a tool that a client declares in its request. The agent may call it, the client executes the call, and the client returns the result in a later request. The Anthropic Messages API (`tools`, `tool_use`, and `tool_result`) and the OpenAI Chat Completions and Responses APIs (`tools`, function calls, and function call outputs) define this pattern **[external]**. Programs written against those APIs commonly hand the model their own tools, so a Lenny runtime served through those surfaces is only partly usable without it.

Lenny has no client tools today **[source]**:

- The runtime contract carries no tool call from a runtime to a client. The stdin `tool_call` and `tool_result` frames of [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod) are reserved for adapter-local tools, and a runtime that emits `tool_call` blocks until the matching `tool_result` arrives.
- The OpenAI translators decode a request without rejecting unknown fields, so a client that sends `tools` receives prose with a `200`. `BUILD-GAPS.md` records this as F-15.1.82.
- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 2 proposed client tools for its surface as a bridge that keeps the runtime's turn open across HTTP requests ("AM-7"). The owner has since removed open turns from that design and decided that client tools come from a standalone design.

This design adds client tools as a turn-ending exchange. The runtime ends its turn with the calls it wants the client to run, the gateway returns them to the client and records them on the session, and the client's next request delivers the results as the next turn. No call is outstanding inside the runtime or the gateway while the client works.

## Scope

**Added:**

- The `clientToolCalls` member on the runtime's end-of-turn `message` frame, the `toolResults` member on the `message` envelope, and the `clientTools` and `toolChoice` members of the envelope's `conversation` member ("CT-1").
- The runtime capability `capabilities.clientTools` ("CT-2").
- Admission of client tool definitions: names, schemas, OpenAI `custom` tools, OpenAI `allowed_tools`, limits, and refusals ("CT-3").
- Validation of every call a runtime returns, with a do-not-retry failure and a per-revision metric ("CT-4").
- Pending calls on the session row, the delivery rule for results, the `abandonPendingToolCalls` request field, the effect of a runtime's retraction on pending calls, and the hold rule for inbox deliveries with its exception for runtimes that declare the `steer` delivery intent ("CT-5").
- The request and response mapping for Chat Completions, Responses, REST, the Messages API, and MCP tool arguments, including streaming and errors ("CT-6").
- Rules for resume, cold continuation, the order of entries in a history the gateway builds, retries, revisions, and derive ("CT-7").
- Interceptor coverage, reserved names, the trust level of results, audit, and ownership ("CT-8").
- The exclusion of client tools from delegated children ("CT-9").
- Runtime SDK types and conformance cases ("CT-10").

**Changed:**

- The OpenAI translators accept function tools and `allowed_tools` for revisions that declare the capability, and `custom` tools for revisions that also declare `customTools`, and refuse them for every other revision, in place of dropping them.
- [`session-derive-design.md`](session-derive-design.md): a `from: latest` derive from a session with pending calls copies them to the derived session. A `from: start` derive copies none.
- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): AM-7's bridge is replaced by this design, and the `target` and `endOfBatch` members it proposed for `tool_call` are withdrawn.
- The `tool_use` session event gains `target: "client"`.
- The [§4.8](../spec/04_system-components.md#48-gateway-policy-engine) `PreToolResult` phase runs on client results, and `PostAgentOutput` covers call arguments.

**Removed:**

- Nothing in the tree. AM-7's open-turn bridge and its proposal AM-E leave the Messages design, which "Instructions for reviewers of other designs" states.

**Out of scope:**

- Client tools for delegated children (T7).
- An MCP-native form. MCP defines no construct by which a client exposes tools to a server ("Current state").
- OpenAI hosted tools and Anthropic server tools.
- Checking a custom tool's input against its grammar. Grammar conformance is the runtime's responsibility, and the client verifies it (O9).
- The retraction operation itself. [`message-delivery-design.md`](message-delivery-design.md) defines it in MD-11 under its settled decision S8, and "CT-5" states its effect on pending calls.
- Warm continuation for Chat Completions as this design wrote it (T4). The conversation fingerprint of [`rest-only-client-api-design.md`](rest-only-client-api-design.md#ro-10-the-provider-adapters) provides it in the OpenAI adapter.
- A change to a reference runtime. Lenny's reference runtimes do not declare the capability in v1 (T3).

## Terms

| Term | Meaning |
| :-- | :-- |
| Client tool | A tool that a request declares and that the client executes. |
| Runtime tool | A tool a runtime runs inside its turn: a platform tool, reached through `tool_call` frames ([`runtime-protocol-design.md`, "RP-3"](runtime-protocol-design.md#rp-3-tool-calls-on-frames)), or a tool inside the runtime's own process. The second kind includes the tools of an operator service, such as an MCP server, that the runtime's harness reaches through egress ([`connector-removal-design.md`](connector-removal-design.md), "CR-3 External services"). |
| Client tool call (call) | One entry of an end-of-turn `message` frame's `clientToolCalls`: the runtime's request that the client run one client tool with given arguments. |
| Client tool result (result) | One entry of a `message` envelope's `toolResults`: the client's answer to one call. |
| Pending calls | The calls of a session's most recent turn whose results have not been delivered. A session has at most one set of pending calls. |
| Results message | A client message whose `toolResults` answer the pending calls. |
| Turn | One `message` envelope delivered to a runtime and the runtime's output up to its end-of-turn `message` frame. |
| End-of-turn `message` | The runtime-to-adapter `message` frame that ends a turn, which replaces the specification's `response` frame under [`message-delivery-design.md`, "MD-24"](message-delivery-design.md#md-24-runtime-frames-and-leniency). A `message` ends the turn unless it sets `endOfTurn: false`. |
| Warm turn | A turn delivered to a session that already holds the conversation. |
| Cold turn | A turn that creates a new session and delivers the earlier conversation as structured history before the new input. |
| Runtime id | The `id` a runtime gives a call. It is unique within the session that issued it. |
| External id | The id the gateway gives a call on the client surface: `toolu_` and an opaque value on the Messages API, and `call_` and an opaque value on Chat Completions and Responses. It is unique within the conversation. |
| Inbox delivery | Any message the gateway delivers to a runtime other than the session's own client's messages: inter-session messages through `lenny/send_message`, and held messages from the session inbox and the dead-letter queue. |
| Function tool and custom tool | A function tool takes a JSON object as arguments and declares an input schema. A custom tool is the OpenAI tool type `custom`, which takes a string as input and may declare a grammar. The Anthropic tool field `type: "custom"` marks an ordinary function tool and is unrelated. |
| Steer-declaring runtime | A runtime whose chosen revision declares the `steer` delivery intent. |
| Violation | An end-of-turn `message` whose calls break a rule of "CT-4". |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). Each runtime the caller may use appears as a model on the OpenAI translators and the Messages API. |
| Session | Lenny's unit of work: one runtime instance bound to a pod, with an owner, a tenant, a transcript, and billing events ([§7](../spec/07_session-lifecycle.md)). |
| `CH-MSGSOCK` | The intra-pod channel between the adapter and the runtime process, which carries the JSONL frames `message`, `message_part`, `tool_call`, `tool_result`, and others ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), with the frames of "MD-24" of the message delivery design). |
| `MessageEnvelope` and `MessagePart` | The runtime-facing message format and its content parts ([§15.4.1](../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format", and [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), "Internal `MessagePart` Format"). |
| Conversation input | The `conversation` member that [AM-4](anthropic-messages-api-design.md#am-4-conversation-input) adds to the `message` envelope: history entries with roles, client instructions, parameters, client tools, and the tool choice. A runtime receives it when its revision declares `capabilities.conversationInput`. |
| Adapter-local tools | `read_file`, `write_file`, `list_dir`, and `delete_file`, which the adapter runs inside the pod, and any custom tools a custom adapter lists in the manifest's `adapterLocalTools` ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), "Adapter-Local Tool Reference"). The owner retired them, with `adapterLocalTools`, on 2026-11-12 ([`runtime-protocol-design.md`](runtime-protocol-design.md#decisions), D5), so no client tool name collides with one. |
| Built-in adapter single-shot compute model | The §15 rule that a Chat Completions request runs an implicit session that claims a warm pod, runs one turn, and releases the pod within one HTTP call ([§15](../spec/15_external-api-surface.md#15-external-api-surface)). |
| Open Responses conversation sessions | The §15 rules for holding a Responses session after a turn and for serving a `previous_response_id` continuation warm on the session that served the response or cold in a new session, including the response records on the session row and the warm conditions 1 to 4. |
| Provider-adapter session | An ordinary session that a provider adapter creates. It replaces the term "Held session": Lenny keeps no held-session state and no close-after-turn rule, under the simplified O3 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), which the owner answered "yes" on 2026-11-14. The adapter may shorten the session's idle timeout, an operator lengthens it through the pool's idle timeout, a `one_shot` runtime ends its own session, and the adapter routes a follow-up request to the session by its conversation fingerprint ([`anthropic-messages-api-design.md`, "Session lifetime"](anthropic-messages-api-design.md#session-lifetime)). |
| Delivery paths | The named paths of [`message-delivery-design.md`, "MD-5"](message-delivery-design.md#md-5-delivery-paths), which replace §7.2's numbered paths: the direct path writes a message when no turn is open or when it is a results message, the queue path holds a `queue` message in the session inbox until the open turn's end-of-turn `message`, and the resume path wakes or resumes a waiting or paused session. |
| Open turn | A turn whose end-of-turn `message` has not arrived, which the gateway tracks from the frames it writes and reads. It replaces the specification's `ready_for_input` signal, which leaves the specification under decision O1 of [`message-delivery-design.md`](message-delivery-design.md#decisions). |
| `PreRoute` chain and post-chain point | The interceptor phase that chooses a session's runtime and pool, and the single step after it and after revision selection where every check that depends on the runtime, the revision, or the pool runs ([`runtime-routing-design.md`, RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract)). |
| Revision | An immutable, numbered copy of a runtime's versioned fields, `capabilities` among them, chosen per session by the runtime's traffic weights ([`runtime-revisions-design.md`, RV-1 and RV-2](runtime-revisions-design.md#rv-1-revisions)). |
| Per-tenant capability override | The §5.1 mechanism that adjusts a runtime's capabilities for one tenant's sessions, which RV-1 restricts to narrowing ([`runtime-revisions-design.md`, "Per-tenant capability override"](runtime-revisions-design.md#per-tenant-capability-override)). |
| Interceptor phases | The [§4.8](../spec/04_system-components.md#48-gateway-policy-engine) gateway phases at which deployer interceptors inspect, modify, or reject content. `PreToolResult` receives `{id, content, isError}` for a tool result before the agent sees it, and `PostAgentOutput` receives the agent's output parts before the client sees them. |
| Tool-use approval | A platform tool call held at the gateway until an approver resolves it ([`security-oversight-design.md`, SO-5a](security-oversight-design.md#so-5a-tool-use-approvals)), and the §7.2 approval gate the executor runs today. Approvals for the tools of an operator service are the service's. |
| Impersonation bearer and F-13.3.26 | A token a `platform-admin` obtains to act as a user. F-13.3.26 is the `BUILD-GAPS.md` finding, recorded in [`user-credential-delivery-design.md`](user-credential-delivery-design.md), that session input routes admit one. |
| Replay | `POST /v1/sessions/{id}/replay`, which [§15.1](../spec/15_external-api-surface.md#151-rest-api) "Session Replay Semantics" defines today and which [`session-derive-design.md`](session-derive-design.md) removes. Under that design a client that wants to repeat a session reads its client messages and re-sends them itself. |
| Derive | `POST /v1/sessions/{id}/derive`, which under [`session-derive-design.md`](session-derive-design.md) starts a new session from a source session's latest state (`from: latest`) or from the state it began with (`from: start`). |
| Delivery intent and `steer` | The per-message delivery intent that [`message-delivery-design.md`](message-delivery-design.md) defines. Each runtime declares the intents it supports, and `steer` is one of them. This design relies only on whether a runtime declares `steer`, and that design states what the intent does. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, 10 runtime adapter conformance, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| AM-3, AM-4, AM-5, AM-6, AM-7, and AM-8 | [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) | The Messages API request mapping, conversation input, continuity by conversation digest, responses and streaming, the client-tool bridge this design replaces, and the Anthropic error envelope. |
| AM-A to AM-E | [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md#proposal-split-and-order) | That design's proposals. AM-A lands conversation input and moves the OpenAI translators onto it. AM-E was the client-tools proposal. |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The `PreRoute` contract and the post-chain point. |
| RV-1 and RV-2 | [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions) | Revisions and their field classes, the per-tenant capability override, and revision selection with its rollout key. |
| MP-5 | [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers) | The intra-pod MCP servers under the MCP upgrade. |
| SO-5a | [`security-oversight-design.md`](security-oversight-design.md#so-5a-tool-use-approvals) | Tool-use approval rules, matched on platform tool names. |
| CR-1 and CR-3 | [`connector-removal-design.md`](connector-removal-design.md) | The removal of connectors, connector tools, and `deferredToolDiscovery`; and the guidance by which a runtime reaches operator services through egress. |
| SD-1 | [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) | The derive route, and "Reading client messages", which returns the messages a client sent to a session. |
| AR O4 and AR O5 | [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) | Two decisions of the design that removes `type: mcp` runtimes and service mode, settled by the owner on 2026-11-13 in the words "O5 (b), yes to all the others". O4 removes `executionMode` and `conversationContinuity` from `sessionIsolationLevel` and from the hold rule of the Open Responses and Messages adapters. O5 removes `sessionId` from every runtime frame except `session_start`. |
| MD O3 | [`message-delivery-design.md`](message-delivery-design.md#decisions) | The owner's decision of 2026-11-09 that a message from another session to a session created on a provider surface is delivered on arrival, with its rule that client tools are offered only in turns a client request caused, which this design records as T16. |
| MD-11 and S8 | [`message-delivery-design.md`](message-delivery-design.md#md-11-retraction) | Retraction: the runtime's `retract` frame, the `retract_result` frame with `retracted`, `too_late`, or `unknown` per id, and the owner's decision S8 that settles it. |

## High-level design

A client that uses OpenAI's or Anthropic's API with its own tools sends the tool definitions with each request. When the model wants one of them, the response stops and names the call, the client runs the tool, and the next request carries the result. Lenny serves the same exchange for runtimes that declare they can take part in it. The runtime receives the client's tools with every turn. When it wants the client to run one, it ends its turn and names the calls in its final `response` frame. The gateway checks the calls against the client's declarations, records them on the session, and returns them to the client in the surface's own form. The client's next request carries the results, and the gateway delivers them to the runtime as the next turn's `message`. Between the two requests nothing waits inside the runtime or the gateway: the session is idle, and its ordinary timers, suspension, and recovery apply.

A runtime that keeps its conversation in memory continues warm on the same session, and a session that became `dormant` after the turn continues warm once it is restored. A session that has ended, or that was recovered after a pod failure from a checkpoint taken before the turn ended, continues cold: a new session receives the earlier calls and results as structured history. On Chat Completions the OpenAI adapter finds the session by the conversation fingerprint of [`rest-only-client-api-design.md`](rest-only-client-api-design.md#ro-10-the-provider-adapters), so a round trip continues warm when the client resends the conversation unchanged and cold otherwise.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Protocol members (CT-1).** The runtime's end-of-turn `message` gains `clientToolCalls`, the `message` envelope gains `toolResults`, and the conversation input carries `clientTools` and `toolChoice` on every turn a client request causes. History entries carry earlier calls and results. No frame type, channel, or link is added.

**Capability (CT-2).** A revision declares `capabilities.clientTools`. Lenny refuses client tools only on revisions that do not declare it, and it prescribes nothing about how a declaring runtime runs its loop.

**Admission (CT-3).** The gateway checks each tool definition when the request arrives: name rules, reserved names, limits, and a schema that compiles with every external reference refused. An OpenAI `custom` tool is admitted for a revision that declares `customTools`, and its grammar is passed to the runtime unchecked. An OpenAI `allowed_tools` choice becomes a list of allowed names in the tool choice.

**Response validation (CT-4).** The gateway checks each call against the declared names, the tool's input schema or, for a custom tool, that the input is a string, the tool choice with its allowed names, and the parallel limit. A violation fails the turn with a do-not-retry error and counts on a per-revision metric. The gateway never repairs a call.

**Pending calls and the hold rule (CT-5).** The pending calls live on the session row. A client message must answer exactly the pending calls. On REST, a client message may instead set `abandonPendingToolCalls: true`, which answers every pending call with a "cancelled by client" error result. On REST a runtime may retract pending calls under MD-11 of the message delivery design, and the client receives a `tool_use` event with phase `retracted`. On the provider surfaces a retraction answers `too_late`. For a runtime without the `steer` delivery intent, inter-session messages and other inbox deliveries wait until a turn ends with no pending calls, and are delivered right after it. A steer-declaring runtime receives them while calls are pending and places them after the results. Client tools are offered only in turns a client request caused, so a turn that an inbox delivery causes carries no client tools (T16).

**Surfaces (CT-6).** Chat Completions, Responses, REST, and the Messages API map tools, tool choice, calls, and results to their own forms, in plain and streaming responses. A streaming Chat Completions request that declares a custom tool is refused. MCP clients use Lenny-specific arguments on `lenny/create_session` and `lenny/send_message`, generated from the REST definitions.

**Continuation (CT-7).** Pending calls survive pod loss, suspension, and failover because they are session data. A cold turn delivers the calls and results as history, and a message that arrived while calls were pending follows their results in that history. A cold turn of an existing conversation can land on another revision, and a revision without the capability refuses it before a pod is claimed. A `from: latest` derive copies the source's pending calls, and the derived session's first client request answers them or, on REST, abandons them. A `from: start` derive copies none.

**Security (CT-8).** A result has the client's trust level. Every result passes `PreToolResult`, every call's arguments pass `PostAgentOutput`, reserved names are refused, and audit records carry ids and names without arguments or content.

**Delegation (CT-9).** A child session never receives client tools.

**SDKs and conformance (CT-10).** The runtime SDKs model the new members, and the conformance suite tests a declaring runtime warm, cold, and after resume.

```
 client ──request: tools, tool_choice, input──> gateway surface (Chat, Responses, REST, Messages, MCP)
                                                   │
                                                   ▼
                                CT-3 admission: names, reserved names, limits, schema compile
                                                   │
                                                   ▼
                                post-chain point: revision declares capabilities.clientTools?
                                   │ no ──> refused before any pod is claimed
                                   ▼ yes
                     message envelope: input + conversation {clientTools, toolChoice, history}
                                                   │
                                                   ▼
                     runtime turn ──message {parts, clientToolCalls}───> CT-4 validation
                                                   │                        │ violation ──> turn failed,
                                                   │                        │               do-not-retry,
                                                   ▼                        │               metric
                     pending calls on the session row; inbox deliveries held (no steer)
                                                   │
                                                   ▼
                      surface response: calls as tool_use blocks, tool_calls, or function_call items
                                                   │
 client runs the tools ──next request with results──┘
                                                   │
                         warm: same session          cold: new session, calls and results in history
                                                   ▼
                     message envelope: toolResults (+ input) ──> runtime continues; held deliveries follow
```

## Decisions

Settled by the owner on 2026-11-09. T1 to T8 came with revision 1, and the owner revised T5 and added T9 with revision 2:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T1 | The mechanism is turn-ending. The runtime's end-of-turn `message` carries `clientToolCalls`, as the `response` frame did before the message delivery design's MD-24 replaced it. The next client request reaches the runtime as a `message` with a `toolResults` member. The tools arrive in `conversation.clientTools` and `conversation.toolChoice` on every turn. | CT-1 |
| T2 | The v1 surfaces are Chat Completions, Responses, REST, and the Messages API. MCP gets client tools only as Lenny-specific tool arguments. | CT-6 |
| T3 | Lenny is not prescriptive. `capabilities.clientTools` is the runtime author's declaration, and Lenny refuses client tools only on runtimes that do not declare it. Lenny's own reference runtimes do not declare it in v1. | CT-2 |
| T4 | On Chat Completions, each tool round trip continues cold with the history. Superseded on 2026-11-14 by the conversation fingerprint rule of [`rest-only-client-api-design.md`](rest-only-client-api-design.md#ro-10-the-provider-adapters): a round trip continues warm on the session the fingerprint finds, under that design's conditional send (R16), and continues cold with the history otherwise. | CT-6, CT-7 |
| T5 | A client message without the pending results is refused, apart from the REST exception of T9. For a runtime that does not declare the `steer` delivery intent of [`message-delivery-design.md`](message-delivery-design.md), inter-session messages and other inbox deliveries wait until the results arrive, and are delivered right after them. For a steer-declaring runtime, inbox deliveries are not held while calls are pending, and the runtime places them after the results. The cold-history builder places a message that arrived while calls were pending after their results, so each call and its result stay adjacent. | CT-5, CT-7 |
| T6 | The gateway passes `tool_choice` and the parallel limit to the runtime. It validates the runtime's response: declared tool names, arguments against `input_schema`, `tool_choice`, and the parallel limit. A violation fails the turn with a do-not-retry error and a per-revision metric. The gateway does not repair the response. The owner confirmed with revision 2 that the JSON arguments of a function tool are still validated against its input schema. | CT-4 |
| T7 | Client tools are not delegated. | CT-9 |
| T8 | This is a standalone design, landing after the Messages design's AM-A and alongside the F-15.1.82 fix. | "Proposal split and order" |
| T9 | On REST, a client message without the pending results is refused unless the request sets `abandonPendingToolCalls: true`, which turns the pending calls into "cancelled by client" error results. MCP clients reach `abandonPendingToolCalls` as a generated argument in the MCP adapter. Chat Completions, Responses, and the Messages API always refuse such a message. | CT-5 |

The owner's decisions of 2026-11-09 on [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) also bind this design: that surface has no open turns, the conversation input carries ordered entries with the new input marked (its O3), warm continuation uses an HMAC-SHA256 conversation digest with an in-flight join that reattaches a retry to the running turn (its O2 and M1), and every failure after dispatch carries `x-should-retry: false`.

Settled by the owner on 2026-11-09 with revision 2, from the open decisions of revision 1. O1, O2, and O4 to O7 take revision 1's recommendation. O8 and O9 take the owner's own answers:

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| O1 | The form of the capability. | An object, `capabilities.clientTools.supported` with `reservedNames`, that requires `capabilities.conversationInput: true`. The cold path delivers earlier calls and results as structured history, which transcript rendering cannot express, so the dependency on conversation input is structural. `reservedNames` lets a runtime protect the names of its own model-visible tools, which the gateway cannot discover. O9 adds `customTools`. | CT-2 |
| O2 | Whether a runtime may declare that it accepts results only warm. | No field. A declaring runtime accepts results cold from history, and for calls it no longer holds in memory, or answers with an end-of-turn `message` carrying `error`. A harness wrapper that parks a call in memory can declare the capability and fail a cold continuation with an error. A `warmOnly` field is revisited when a harness wrapper asks for it. | CT-7 |
| O4 | REST sessions whose revision cannot accept a further message (`capabilities.interaction: one_shot`, or `capabilities.injection.supported: false`). | Client tools on such a session are refused at creation with `422 CLIENT_TOOLS_UNSUPPORTED`, naming the missing capability. On REST a session has no cold path with history, so a session that cannot take a second message can never receive its results. Chat Completions, Responses, and the Messages API are unaffected, because their cold path delivers results in a new session. | CT-2, CT-6 |
| O5 | A retried Responses continuation that carries results. | The Messages API's in-flight join extends to Responses continuations: a continuation of response `R` whose input equals the input of the running continuation of `R` reattaches to that turn. The SDKs retry a dropped connection twice by default **[external]**, and a results request is the most frequent request in a tool loop. Chat Completions stays cold under T4. REST need no join, because a second results message finds no pending calls and is refused. | CT-7 |
| O6 | How a deployer's output interceptors see call arguments. | The `PostAgentOutput` payload gains an optional `clientToolCalls` member beside the output parts, so an existing output filter covers arguments and custom tool input with no change to its registration. | CT-8 |
| O7 | The per-tenant capability override for client tools. | A tenant may clear `clientTools.supported`, may clear `clientTools.customTools`, and may append to `clientTools.reservedNames`. A widening is refused. This is the narrowing form RV-1 gives `capabilities.interaction` and `capabilities.injection`. Clearing `customTools` follows from O9 under the same narrowing rule. | CT-2 |
| O8 | A re-run of a session that used client tools. | Server-side replay no longer exists under [`session-derive-design.md`](session-derive-design.md). A client that re-runs a session re-sends its messages itself and executes its own tools. A derive from a session with pending client calls copies them, and the derived session's first client request must carry the results. Revision 3 narrows the copy to `from: latest` (T10) and lets that request abandon the calls instead (T12). | CT-7 |
| O9 | OpenAI `allowed_tools` and `custom` tools. | `allowed_tools` is accepted as a restriction in `toolChoice`, `allowedNames`, which the T6 check enforces. `custom` tools are accepted for runtimes that declare `clientTools.customTools`. For a custom tool call the gateway checks the name, that the input is a string, `tool_choice`, and the parallel limit, and never checks the grammar. Grammar conformance is the runtime's responsibility, and the client verifies it. | CT-1, CT-2, CT-3, CT-4, CT-6 |

Settled by the owner on 2026-11-09 with revision 3, in the answer "Client tools: ok on everything". T10 changes revision 2's derive rule. T11 to T15 were choices revision 2 made without an owner decision, and the answer confirms them. O3 and O10 take revision 2's recommendation:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T10 | A derive copies the source's pending calls only under `from: latest`. A `from: start` derive copies none, because it restarts from the state before the source's turns, where no call was issued. | CT-7 |
| T11 | A derive whose chosen revision does not declare `capabilities.clientTools`, or does not declare `customTools` while a copied call names a custom tool, is refused at the post-chain point with `422 CLIENT_TOOLS_UNSUPPORTED` before any pod is claimed. | CT-7 |
| T12 | The derived session's first client message may set `abandonPendingToolCalls: true` in place of the results, on REST, as any client message may under T9. | CT-5, CT-7 |
| T13 | A client message that sets `abandonPendingToolCalls` and also carries `toolResults` is refused with `409 CLIENT_TOOL_RESULTS_MISMATCH`. | CT-5 |
| T14 | The "cancelled by client" results that `abandonPendingToolCalls` creates pass through `PreToolResult`, as every result does. | CT-5, CT-8 |
| T15 | An OpenAI `allowed_tools` tool choice with `mode: required` maps to `toolChoice.type` `any`, and `mode: auto` maps to `auto`. | CT-3, CT-6 |

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| O3 | For a runtime that does not declare `steer`, what happens to held inbox deliveries when the results turn itself ends with new calls. A steer-declaring runtime is outside this decision under T5. | Held deliveries stay held until a turn ends with no pending calls, under their ordinary inbox TTL. The histogram `lenny_client_tool_held_delivery_age_seconds` records how long each one waited. T5 exists so that a runtime without `steer` never receives unrelated input while its model history holds an unanswered call, and delivery at the next turn end would break that for every multi-step tool loop. A runtime author who wants inbox deliveries during a tool loop declares `steer`. | CT-5 |
| O10 | The Chat Completions stream form of a custom tool call. | A Chat Completions request with `stream: true` that declares a custom tool is refused with `400 invalid_request_error` naming the combination, until OpenAI documents a stream form. The Chat Completions streaming reference types `delta.tool_calls[].type` as `"function"` alone, stating "Currently, only `function` is supported", and the `openai-python` chunk type `ChoiceDeltaToolCall` declares `type: Literal["function"]` with no `custom` member **[external]**, so a client SDK has no documented field to accumulate the input into. A plain Chat Completions response and every Responses request carry custom tools under O9. | CT-3, CT-6 |

Retraction of pending calls is settled in [`message-delivery-design.md`](message-delivery-design.md) under its decision S8, and "CT-5" and "CT-6" state its effect on this design.

Settled by the owner on 2026-11-09 with revision 4, through O3 of [`message-delivery-design.md`](message-delivery-design.md). The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes", and that decision carries this rule:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T16 | Client tools are offered only in turns caused by a client request. A turn caused by another session's message, a system message, or an external message carries no `conversation.clientTools` and no `conversation.toolChoice`, and a call in it fails validation with `no_tools_declared`. This holds for an inbox delivery during pending calls, for a held delivery released after a turn with no pending calls, and for a message delivered on arrival to a held session on a provider surface. A turn that no client request caused could otherwise return calls that no waiting client receives. | CT-1, CT-4, CT-5 |

Settled by the owner on 2026-11-13, in the answers to the consolidated list of open decisions:

| ID | Decision | Where |
| :-- | :-- | :-- |
| T17 | The owner chose option (b) of the consolidated list, in the words "ok, go with (b)", and confirmed that the OpenAI and Anthropic adapters leave too and that the Lenny project maintains each adapter in a separate repository with an optional Helm subchart. Every client protocol adapter (MCP, A2A, OpenAI Chat Completions, Open Responses, and Anthropic Messages) moves out of Lenny into a separate first-party repository in front of the REST API. `rest-only-client-api-design.md`, which is being written, carries the change and governs which of this design's surfaces leave with the adapters. This design does not apply the removal, and T2's list of v1 surfaces stands until that design lands. | Status |

## Requirements

| ID | Requirement |
| :-- | :-- |
| CTR-1 | A client tool call reaches a client only from a session whose chosen revision declares `capabilities.clientTools`, and only for a tool that the client declared in the request that started the turn. |
| CTR-2 | Every call the gateway returns names a declared tool, has arguments that validate against that tool's input schema or, for a custom tool, a string input, and obeys the turn's tool choice, its allowed names, and its parallel limit. A response that breaks any of these fails the turn, and the gateway never alters a call. |
| CTR-3 | A results message is delivered only when it comes from the session's owner through a client surface and answers exactly the pending calls that remain after any retraction, or on REST sets `abandonPendingToolCalls: true`. |
| CTR-4 | No inter-session message or other inbox delivery reaches a runtime that does not declare `steer` while its session has pending calls, and held deliveries are delivered after a turn ends with no pending calls. A steer-declaring runtime places a delivery it receives while calls are pending after the results, and every history the gateway builds places such a message after the results. |
| CTR-5 | No call is outstanding inside a runtime process or the gateway while the client executes it. Pending calls are session data that survive pod loss, suspension, and replica failover. |
| CTR-6 | On every surface, every result passes `PreToolResult` and every call's arguments or custom tool input pass `PostAgentOutput` before they cross the boundary. |
| CTR-7 | A delegated child session never receives client tools, and a call from a child is a violation. |
| CTR-8 | Pending calls, calls, and results are erased with the session under [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces). |
| CTR-9 | Client tools work for every runtime that declares them, because the exchange uses `CH-MSGSOCK` frames alone and needs no other capability. |
| CTR-10 | Client tools are offered only in turns a client request caused. No envelope that opens a turn caused by another session's message, a system message, or an external message carries `conversation.clientTools` or `conversation.toolChoice`, and a call in such a turn fails validation. |

## Current state

### Lenny

**Runtime contract [source].** The `CH-MSGSOCK` card of §28.5.3 lists the adapter-to-runtime messages as `session_start`, `message`, `tool_result`, `heartbeat`, `session_end`, and `shutdown`, and the runtime-to-adapter messages as `session_started`, `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`. Its "Tool access by level" table limits stdin `tool_call` and `tool_result` to adapter-local tools at every level, and Standard and Full runtimes reach platform and connector tools through their MCP client. "Delivery semantics" states that a runtime that emits `tool_call` keeps reading stdin until the matching `tool_result`, and "Correlation" states that a `tool_result` with an unknown `id` is dropped and logged as a protocol error. The `response` frame carries `output` and an optional `error`, and no member names work the runtime waits on. The "Message schemas" preamble requires runtimes to ignore unrecognized fields and unrecognized frame types.

**Envelope [source].** The `MessageEnvelope` of §15.4.1 has `schemaVersion`, `type`, `id`, `from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `sessionId`, and `input`. It has no member for tools or results. Decision O5 of [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) removes `sessionId` from the envelope, as from every frame other than `session_start`. `from.kind` is `client`, `agent`, `system`, or `external`, and the adapter overwrites any runtime-supplied `from`.

**Delivery [source].** §7.2 "Message delivery routing" writes a message to a runtime that reports `ready_for_input` (path 2) and buffers it in the session inbox while the runtime is busy (path 5). Its "Delivery of held messages" paragraph delivers a session's held messages before each message delivered under path 2. `input_required` is a sub-state of `running` in which the runtime is blocked inside `lenny/request_input`.

**Executor [source].** `Executor.Send` (`pkg/gateway/session/executor/executor.go`) is synchronous and returns the turn's output parts. `PodExecutor.handleFrame` (`podstream.go`) sends a `tool_call` that carries `approvalRequired` to the approval gate, completes the turn on a `response`, and discards every other frame. `responseEnvelope` (`subprocess.go`) decodes `type`, `text`, and `output`, and `ingestResponse` (`ingest.go`) reads nothing else.

**Adapter [source].** The Attach loop in `pkg/adapter/attach.go` answers an adapter-local `tool_call` through `HandleToolCall` (`toolcall.go`) and relays every other frame to the gateway unchanged. `attachRecvLoop` writes every envelope the gateway sends to the runtime's stdin.

**Translators [source].** `OpenAIChatCompletionsRequest` models `model`, `messages`, `stream`, and `user`, and `OpenResponsesRequest` models `model`, `input`, `previous_response_id`, `stream`, and `user`. Neither decoder rejects unknown fields, so `tools`, `tool_choice`, and `parallel_tool_calls` are dropped with a `200` (F-15.1.82). `OpenAIChatHandler` calls `Executor.Send` directly (`openai_chat.go`), and no file in `pkg/gateway/environment/translator` invokes an interceptor phase.

**Response records [source].** `sessionstore.Session` carries `Responses []ConversationResponse` and `ContinuationParentID` (`pkg/gateway/session/sessionstore/sessionstore.go`), which implement the §15 "Open Responses conversation sessions" records.

**Interceptors [source].** §4.8 defines `PreToolResult` with the payload `{ id, content: MessagePart[], isError? }` and `PostAgentOutput` with a `MessagePart[]` payload. The gateway runs `PreToolResult` in `buildPreToolResultInterceptor` (`pkg/gateway/mcpfabric/mcptools/mcptools.go`), on MCP tool results only, and runs `PostAgentOutput` on the REST message path through `runPostAgentOutput` (`pkg/gateway/sessionserver/route.go`).

**Schema compiler [source].** The gateway compiles `runtimeOptionsSchema` with `github.com/santhosh-tekuri/jsonschema/v5` v5.3.1 in `validateAgainstSchema` (`pkg/gateway/sessionserver/envelope.go`), with a default compiler. That library's package-level `Loaders` map resolves `file` URLs (`loader.go` in the module), and each `Compiler` has a `LoadURL` field that replaces it.

**Runtime SDKs [source].** The Go, Python, and TypeScript SDKs under `sdks/runtime` hand each `message` to a handler and write its `Reply` as a `response`, and expose adapter-local tools as a blocking `ToolCall`. The Go SDK's `ToolCall` (`sdks/runtime/go/runtime/tool.go`) waits for the SDK's dial timeout, 5 seconds by default (`options.go`). None models client tools.

**Capabilities [source].** §5.1 defines `capabilities.interaction` and `capabilities.injection` among the runtime's capabilities, and prohibits a derived runtime from setting `capabilities`. RV-1 classifies the `capabilities` entries as versioned, gateway-read fields.

**Reference runtimes [source].** §26 lists `claude-code`, `gemini-cli`, `codex`, and `cursor-cli` (§26.3 to §26.6), `chat` (§26.7), and `langgraph`, `mastra`, `openai-assistants`, and `crewai` (§26.8 to §26.11). None declares client tools, because the capability does not exist.

### External APIs on 2026-11-09

**Anthropic Messages [external].**

- A client tool has `name` matching `^[a-zA-Z0-9_-]{1,128}$`, `description`, and `input_schema`, a JSON Schema object, with optional `type: "custom"`, `strict`, `input_examples`, `cache_control`, `defer_loading`, `allowed_callers`, and `eager_input_streaming`.
- `tool_choice` is `auto`, `any`, `tool` with `name`, or `none`. `disable_parallel_tool_use` sits inside the `tool_choice` object and is "not a top-level request parameter". With it set, `auto` calls at most one tool and `any` or `tool` calls exactly one. Parallel calls are on by default.
- The documentation states that `any` and `tool` return a 400 error on several current models, among them Opus 5.5 and Sonnet 5.5.
- A response that calls tools has `stop_reason: "tool_use"` and one or more `tool_use` blocks with `id`, `name`, and `input` as an object. Text blocks may precede them. The examples' ids begin with `toolu_`, and the documentation states no prefix rule.
- The next request's user message carries one `tool_result` per `tool_use`, "all together in the next user message", with `tool_use_id`, optional `content` (a string, or text, image, document, or search result blocks), and optional `is_error`. The `tool_result` blocks come first, and text follows them. The documentation gives "tool_use ids were found without tool_result blocks immediately after" as an example of the error for a missing result, and states no full message.
- A call the client chose not to run still receives a `tool_result` with `is_error: true` and a brief explanation.
- Streaming sends `content_block_start` with the `tool_use` block and `input: {}`, `content_block_delta` events of type `input_json_delta` whose `partial_json` strings accumulate to the input, `content_block_stop`, and `message_delta` with `stop_reason: "tool_use"`. The documentation states no minimum or maximum number of deltas.
- The stop reasons page states that a response that leaves a client `tool_use` block waiting never has `stop_reason: "pause_turn"`.

**OpenAI Chat Completions [external].**

- A function tool is `{"type":"function","function":{"name","description","parameters","strict"}}`, and `name` uses `a-z`, `A-Z`, `0-9`, underscores, and dashes, at most 64 characters.
- A custom tool is `{"type":"custom","custom":{"name","description","format"}}`. `format` is `{"type":"text"}`, the default, which the reference calls "Unconstrained text format", or `{"type":"grammar","grammar":{"definition","syntax"}}` with `syntax` `lark` or `regex` ([Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), read 2026-11-09).
- `tool_choice` is `none`, `auto`, `required`, a named function `{"type":"function","function":{"name"}}`, a named custom tool `{"type":"custom","custom":{"name"}}`, or `{"type":"allowed_tools","allowed_tools":{"mode","tools"}}`, where `mode` is `auto` or `required` and `tools` is "A list of tool definitions that the model should be allowed to call". The reference's example lists only function entries in `tools` ([Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), read 2026-11-09).
- A custom tool call in an assistant message is `{"id","type":"custom","custom":{"name","input"}}` with `input` a string. The reference documents one tool result message, `{"role":"tool","tool_call_id","content"}`, for both kinds of call ([Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), read 2026-11-09).
- The streaming chunk reference types `delta.tool_calls[].type` as `"function"` alone, stating "Currently, only `function` is supported", and defines no `custom` member ([Streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events), read 2026-11-09). The `openai-python` type `ChoiceDeltaToolCall` declares `type: Literal["function"]` and a `function` member alone ([`chat_completion_chunk.py`](https://github.com/openai/openai-python/blob/main/src/openai/types/chat/chat_completion_chunk.py), read 2026-11-09).
- `parallel_tool_calls` defaults to `true`. The function calling guide states that `false` "ensures exactly zero or one tool is called".
- An assistant message's `tool_calls` entries are `{id, type: "function", function: {name, arguments}}` with `arguments` as a JSON string, and `finish_reason` is `tool_calls`. The example response has `content: null`, and the page does not say whether text may accompany calls.
- A result is `{"role":"tool","tool_call_id","content"}` with string or text-part content. The page does not state that every call needs a result message.
- A stream chunk's `choices[].delta.tool_calls` entries carry `index`, `id`, `type`, and `function.name` and `function.arguments`. The pages do not state how fragments merge. The `openai-python` SDK merges entries by `index`, concatenates string fields, and accepts a single chunk that carries a whole call. It emits its `tool_calls.function.arguments.done` event only when a later chunk closes the call, so a chunk that holds both the call and `finish_reason` produces no such event.

**OpenAI Responses and Open Responses [external].**

- A function tool is `{"type":"function","name","description","parameters","strict"}` at the top level. `tool_choice` adds `allowed_tools`, `mcp`, `custom`, and hosted tool forms. `parallel_tool_calls` defaults to `true`.
- A custom tool is `{"type":"custom","name","description","format"}` at the top level, with `format` `{"type":"text"}` or `{"type":"grammar","syntax","definition"}` and `syntax` `lark` or `regex`. `ToolChoiceAllowed` is `{"type":"allowed_tools","mode","tools"}` with `mode` `auto`, under which the model may call an allowed tool or reply with a message, or `required`, under which it calls one or more allowed tools. `ToolChoiceCustom` is `{"type":"custom","name"}` ([Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create), read 2026-11-09).
- A custom tool call is the item `{"type":"custom_tool_call","id","call_id","name","input"}` with `input` a string, and its result is the input item `{"type":"custom_tool_call_output","call_id","output"}`, where `output` is a string or an array of input text, image, and file parts ([Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create), read 2026-11-09). The guide's example item also carries `status: "completed"` ([Function calling](https://developers.openai.com/api/docs/guides/function-calling), read 2026-11-09).
- Streaming sends `response.custom_tool_call_input.delta` with `delta`, `item_id`, and `output_index`, and `response.custom_tool_call_input.done` with the whole `input` ([Streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), read 2026-11-09).
- The function calling guide states that custom tool sampling "is constrained using LLGuidance" for a grammar, and that "The OpenAI API may return an error if the grammar is too complex". It shows `allowed_tools` with `mode: "auto"` alone, and its parallel-calling section covers function tools alone ([Function calling](https://developers.openai.com/api/docs/guides/function-calling), read 2026-11-09).
- A call is an output item `{"type":"function_call","id","call_id","name","arguments","status"}` with `arguments` as a JSON string and item `status` of `in_progress`, `completed`, or `incomplete`.
- A result is an input item `{"type":"function_call_output","call_id","output"}`, where `call_id` is at most 64 characters and `output` is a string or an array of input text, image, and file parts. The guide's examples send either the whole input list or `previous_response_id` with the `function_call_output` items alone.
- Streaming sends `response.output_item.added` with the item and empty `arguments`, `response.function_call_arguments.delta` events keyed by `item_id` and `output_index`, `response.function_call_arguments.done` with the whole `arguments`, and `response.output_item.done`.
- The status of a response whose output ends with a function call is not stated on the pages read **[unverified]**.
- The Open Responses specification, version 2026-04-24, defines the same `function_call` item, names `response.function_call_arguments.delta` among its delta events, and defines no `.done` event for arguments. It shows `function_call_output` only in a WebSocket example, does not mention `parallel_tool_calls`, and states that a server given `previous_response_id` "MUST load both the input and output associated with that prior response".
- The Open Responses specification defines `allowed_tools` and states "Servers MUST enforce allowed_tools as a hard constraint: If allowed_tools is present, any tool call to a tool not listed MUST be rejected or suppressed by the server". It defines no tool of the OpenAI type `custom`. Its "custom tools" are implementor-hosted tools with a slug-prefixed type ([Specification](https://www.openresponses.org/specification), read 2026-11-09).

**MCP [external].** The current protocol version is `2026-07-28`. Tools flow from server to client through `tools/list` and `tools/call`, and no construct lets a client expose its own tools to a server. Sampling gained `tools` and `toolChoice` in `2025-11-25` and is deprecated as of `2026-07-28`. On `2026-07-28` an `InputRequiredResult` may carry only `elicitation/create`, `sampling/createMessage`, and `roots/list` requests. A tool result carries `structuredContent` and `isError`.

**SDK retries [external].** The `openai-python` and `anthropic-sdk-python` clients retry 408, 409, 429, and every 5xx response twice by default, and both honour a response header `x-should-retry: false` by not retrying.

The assessment stated some external claims more firmly than the pages do, and this design corrects them: the Anthropic missing-result error text is an example, the Chat Completions page states no rule that every call needs a result message, and the MCP Tasks extension page does not list the request kinds an `input_required` task carries.

## CT-1 Protocol members

### `conversation.clientTools` and `conversation.toolChoice`

The conversation input of AM-4 carries the client's tools and tool choice on every envelope of a turn that has them **[design, T1]**:

```json
{
  "schemaVersion": 1,
  "type": "message",
  "id": "msg_01J9...",
  "from": { "kind": "client", "id": "client_gateway" },
  "input": [],
  "toolResults": [
    { "callId": "tc_7", "content": [{ "type": "text", "inline": "{\"status\":\"shipped\"}" }], "isError": false }
  ],
  "conversation": {
    "clientTools": [
      {
        "type": "function",
        "name": "lookup_order",
        "description": "Look up an order by id.",
        "inputSchema": { "type": "object", "properties": { "orderId": { "type": "string" } }, "required": ["orderId"] },
        "strict": false
      }
    ],
    "toolChoice": { "type": "auto", "parallel": true }
  }
}
```

- `clientTools` entries carry `type`, `name`, and `description`. `type` is `function`, the default, or `custom` **[design, O9]**. A function entry carries `inputSchema` and `strict`. A custom entry carries `format`, which is `{"type":"text"}`, the default, or `{"type":"grammar","syntax","definition"}` with `syntax` `lark` or `regex`, and the gateway passes it to the runtime as the client sent it. A client may change its tools between requests, so a runtime reads the list from each envelope and keeps none from an earlier turn.
- `toolChoice` carries `type` (`auto`, `any`, `tool`, or `none`), `name` when `type` is `tool`, `parallel`, which is `false` when the client limited the turn to at most one call, and `allowedNames` **[design, O9]**. `allowedNames` is present when the client restricted the turn's calls to a subset of `clientTools`, and lists that subset. The runtime presents every tool in `clientTools` to its model and calls only tools in `allowedNames`.
- A turn with no client tools carries neither member.
- Client tools are offered only in turns a client request caused **[design, T16]**. An envelope that opens a turn caused by another session's message, a system message, or an external message carries neither member, whatever tools the session's last client request declared. A client request steered into such a turn on a steer-declaring runtime carries its own members, and the runtime may call those tools from the point it consumes the request.

### `toolResults`

`toolResults` on the `message` envelope carries the results that answer the pending calls **[design, T1]**. Each entry has `callId`, `content` as `MessagePart` entries, and `isError`. `input` holds any user content the client sent with the results, such as Messages API text after the `tool_result` blocks, and is empty otherwise. On a warm turn `callId` is the runtime id. On a cold turn it is the external id, which the runtime treats as an opaque string ("CT-7").

### `clientToolCalls`

`clientToolCalls` on the end-of-turn `message` frame ends the turn with calls pending **[design, T1]**:

```json
{
  "type": "message",
  "parts": [{ "type": "text", "inline": "I will look up the order." }],
  "clientToolCalls": [
    { "id": "tc_7", "name": "lookup_order", "arguments": { "orderId": "A-1009" } }
  ]
}
```

- Each call has `id`, unique within the session, and `name`. A call to a function tool carries `arguments` as a JSON object. A call to a custom tool carries `input` as a string **[design, O9]**.
- `parts` holds the parts produced before the calls, and names parts already streamed on `message_part` frames by `id`, under the delivery rules of [`message-delivery-design.md`, "MD-24"](message-delivery-design.md#md-24-runtime-frames-and-leniency).
- An end-of-turn `message` with an empty or absent `clientToolCalls` ends the turn with no calls. A `message` that sets `endOfTurn: false` or `complete: true` carries no calls ("CT-4", `not_end_of_turn`).

Neither example carries `sessionId`. Under decision O5 of [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md), settled by the owner on 2026-11-13, no frame carries `sessionId` except `session_start`, because a frame's session is the session of the slot whose connection carries it.

### History entries

On a cold turn, `conversation.history` holds earlier assistant entries with their calls, `{"role":"assistant","content":[...],"clientToolCalls":[...]}`, and earlier user entries with their results, `{"role":"user","content":[...],"toolResults":[...]}` **[design]**. The final round's results arrive in the envelope's own `toolResults`, so warm and cold turns present the newest results in the same member. Under the owner's O3 decision on the Messages design, the conversation input carries ordered entries with the new input marked, and the marked entry's results are the envelope's `toolResults`.

### Why members on existing frames

The members reach a runtime that does not know them as fields it ignores under the "Message schemas" preamble, and the gateway sends them only to revisions that declare the capability. A new `MessagePart` type would collapse to `text` at every boundary that does not know it. The `tool_call` and `tool_result` frames keep their meaning: a `tool_call` blocks inside a turn, and a turn-ending call followed by an end-of-turn `message` would contradict "Delivery semantics" and "Correlation".

## CT-2 Capability

```yaml
capabilities:
  conversationInput: true
  clientTools:
    supported: true
    customTools: true
    reservedNames: [search_repo, run_tests]
```

**[design, T3, O1, O9]**

- `supported: true` declares that the runtime reads `conversation.clientTools` and `toolChoice`, ends a turn with `clientToolCalls`, and continues from `toolResults` warm and from history cold. Registration refuses `supported: true` without `conversationInput: true`.
- `customTools: true` declares that the runtime also accepts custom tools, ends a turn with calls that carry `input`, and honours a custom tool's `format`. It requires `supported: true`, and its default is `false`. The gateway does not check the grammar, so a declaring runtime is responsible for input that conforms to it (O9).
- `reservedNames` lists the names of the runtime's own model-visible tools. The gateway refuses a client tool whose name is in the list ("CT-3").
- The field is versioned and gateway-read under RV-1, so the chosen revision decides. The per-tenant capability override may clear `supported`, may clear `customTools`, and may append to `reservedNames` (O7).
- A derived runtime may not set it, under the §5.1 rule for `capabilities`.
- It is available to every runtime (CTR-9). `capabilities.interaction` is unconstrained on the surfaces with a cold path. O4 states the REST rule: client tools on a REST session whose revision declares `capabilities.interaction: one_shot` or `capabilities.injection.supported: false` are refused at creation with `422 CLIENT_TOOLS_UNSUPPORTED`, naming the missing capability.
- Lenny's reference runtimes in §26 do not declare it in v1 (T3). A runtime author who builds a raw model loop, uses a framework that pauses a run on a tool call, or wraps a harness that can park a call declares it and takes on the obligations above and in "CT-7".

A request with client tools on a revision that does not declare the capability is refused at the post-chain point before any pod is claimed: `422 CLIENT_TOOLS_UNSUPPORTED` on REST, and `400 invalid_request_error` on the Chat Completions, Responses, and Messages surfaces, each naming the runtime and revision. A request that declares a custom tool on a revision that does not declare `customTools` is refused the same way, naming `customTools`. A request with an empty `tools` array, or with `tool_choice` `none` and no tools, has no client tools.

## CT-3 Admission

> **Adapter-facing section.** **Names.**, **Reserved names.**, **Schemas.**, **Custom tools [O9].**, and **Limits.** still govern Lenny for REST. The Chat Completions name limit of **Names.**, the OpenAI mapping of **Allowed tools [O9].** (T15), the provider forms of **Tool forms.**, and **Streamed custom tools [O10].** move to the OpenAI and Anthropic adapters. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

The surface decodes the tool definitions before session creation or warm dispatch, and the gateway checks them **[design]**:

- **Names.** A name matches `^[a-zA-Z0-9_-]{1,128}$`, and Chat Completions applies its own limit of 64 characters. Names are unique within the request.
- **Reserved names.** A name beginning with `lenny` in any letter case, or in the revision's `clientTools.reservedNames`, is refused with `400 CLIENT_TOOL_NAME_RESERVED` on REST and `400 invalid_request_error` elsewhere. The reserved-name check runs at the post-chain point, because `reservedNames` belongs to the chosen revision, and on every warm turn against the session's revision.
- **Schemas.** A function tool's `inputSchema` is a JSON Schema whose root has `type: "object"`. The gateway compiles each schema with a compiler whose `LoadURL` refuses every URL, so a `$ref` resolves only inside the schema document, and with the 2020-12 draft as the default. A schema that does not compile is refused with `400 CLIENT_TOOL_DEFINITION_INVALID` naming the tool. The compiled schemas live for the turn and are not stored.
- **Custom tools [O9].** A custom tool's `format` is absent, `{"type":"text"}`, or a grammar whose `syntax` is `lark` or `regex` and whose `definition` is a string. Any other `format` is refused with `400 CLIENT_TOOL_DEFINITION_INVALID` naming the tool. The gateway neither parses nor compiles the grammar, and passes `format` to the runtime unchanged. A custom tool on a revision that does not declare `customTools` is refused at the post-chain point as "CT-2" states.
- **Allowed tools [O9].** An OpenAI `allowed_tools` tool choice becomes `toolChoice.allowedNames`, with `mode` `auto` as `type: auto` and `mode` `required` as `type: any` (T15). Each entry of its `tools` list must name a function or custom tool the request declares. An entry that names an undeclared tool or a hosted tool type is refused with `400 invalid_request_error` naming the entry. On REST, `allowedNames` accompanies `type` `auto` or `any` alone, and every name in it must be declared.
- **Tool forms.** Hosted tool types, Anthropic server tool types, and the `mcp` and hosted-tool `tool_choice` forms are refused with `400 invalid_request_error` naming the form.
- **Streamed custom tools [O10].** A Chat Completions request with `stream: true` that declares a custom tool is refused with `400 invalid_request_error` naming the combination. A plain Chat Completions request and every Responses request may declare custom tools.
- **Limits.** `clientTools.maxTools` (default 128), `clientTools.maxSchemaBytes` per request (default 256 KiB), which counts input schemas and grammar definitions together, `clientTools.maxPendingCalls` per turn (default 32), and `clientTools.maxArgumentsBytes` per call (default 256 KiB), which bounds a function call's serialized arguments and a custom call's input, are Helm values and gateway flags, each operator-tunable. A request over a limit is refused with `413` or `400` as the surface pairs them. Result content follows the §28.5.3 part size rule: a part up to 64 KB is inline, a larger part up to 50 MB is staged as a `lenny-blob://` reference, and a larger one is refused with `413 MESSAGEPART_TOO_LARGE`.

## CT-4 Response validation

When an end-of-turn `message` arrives for a turn that had client tools, the gateway checks its calls before it records them or returns any of them **[design, T6]**:

| Rule | Violation reason |
| :-- | :-- |
| Each `name` is one of the turn's `clientTools`. | `undeclared_tool` |
| A call to a function tool carries `arguments` and no `input`, and a call to a custom tool carries `input` and no `arguments`. | `input_form` |
| A function call's `arguments` is a JSON object that validates against that tool's compiled `inputSchema`, whatever the tool's `strict` value. | `schema_mismatch` |
| A custom call's `input` is a string. The gateway does not check it against the tool's grammar. | `input_form` |
| `id` values are unique within the `message` and unused by earlier calls of the session. | `duplicate_id` |
| `toolChoice.type` `none`: no calls. | `tool_choice_none` |
| `toolChoice.type` `any`: at least one call. | `tool_choice_any` |
| `toolChoice.type` `tool`: at least one call, and every call names `toolChoice.name`. | `tool_choice_tool` |
| `toolChoice.allowedNames` present: every call names a tool in it. | `tool_choice_allowed` |
| `toolChoice.parallel` `false`: at most one call. | `parallel_limit` |
| The number of calls and each call's size are within `maxPendingCalls` and `maxArgumentsBytes`. | `limit_exceeded` |
| The `message` carries no `error` together with calls. | `error_with_calls` |
| Calls arrive only on a `message` that ends the turn and does not complete the session: one that sets neither `endOfTurn: false` nor `complete: true`. | `not_end_of_turn` |
| A turn without client tools, including every turn of a delegated child and every turn that no client request caused (T16), has no calls. | `no_tools_declared` |

A violation fails the turn with the error `CLIENT_TOOL_CALL_INVALID` (category `PERMANENT`, `retryable: false`), whose details name the reason, the call id, and for `schema_mismatch` the schema validator's first error path. The gateway never drops, reorders, coerces, or rewrites a call.

Grammar conformance of a custom call's `input` is the runtime's responsibility, and the client verifies it before it runs the tool **[design, O9]**. The gateway has no Lark or regex grammar engine, and a check would add one to the dispatch path of every custom call. A call whose `input` breaks the grammar passes the checks of "CT-4", and the client receives it.

The client receives:

| Surface | Failure |
| :-- | :-- |
| REST | `502 CLIENT_TOOL_CALL_INVALID` on the message route, and an `error` session event. The session continues under its ordinary rules for a failed turn. |
| Chat Completions and Responses | `502` in the OpenAI error envelope with `type: "server_error"`, `code: "client_tool_call_invalid"`, and the header `x-should-retry: false`. |
| Messages API | `500 api_error` in the Anthropic error envelope with the Lenny code in `error.details.error_code`, under the owner's decision on AM-8, and `x-should-retry: false`. |
| Streaming, after the stream began | The surface's error event (`error` on the Messages API, and `response.failed` on Responses), or on Chat Completions the mid-stream error the translator's stream writer already emits, and no call is streamed. |

Calls are validated before the first call is streamed, so a client never receives part of a violating set. Output parts streamed before the end-of-turn `message` arrived stay with the client. On the translators, the session then ends with the `failed` disposition, as §15 states for a failed dispatch.

The counter `lenny_client_tool_call_invalid_total{runtime, runtime_revision, reason}` counts violations, using the `runtime_revision` label RV-2 adds to session metrics. A rollout operator reads it per revision to stop a rollout whose new binary breaks the contract. The audit record of the failed turn names the reason and the call ids.

Validation is a gateway check over data the gateway already holds for the turn, so it adds no round trip. Its cost is one schema validation per call, bounded by `maxPendingCalls` and `maxArgumentsBytes`.

## CT-5 Pending calls and the hold rule

### Recording

After validation, the gateway mints an external id for each call and writes the pending set to the session row in the same write that completes the turn: for each call the runtime id, the external id, the name, and the arguments or custom input **[design]**. The session row holds at most one pending set. The transcript records the assistant entry with its calls and, later, the user entry with its results, so a cold walk of the conversation can rebuild them. `GET /v1/sessions/{id}` reports `pendingClientToolCalls` with external ids, names, and arguments or input.

The `tool_use` session event gains `target: "client"`, fires with phase `requested` when the turn ends with calls, fires with phase `completed`, carrying `isError` and no content, when the results are delivered, and fires with phase `retracted` when a runtime retracts a call on REST or MCP ("Retraction" below).

The turn has ended, so a session with pending calls is `idle` under [`session-lifecycle-design.md`, "SL-1"](session-lifecycle-design.md#sl-1-states), like any session between turns, and it becomes `dormant`, is suspended, resumes, and expires under its ordinary rules. Nothing blocks in the runtime, and pending calls add no session state.

### Delivery rule for client messages

For a session with pending calls, the gateway applies these rules before any §7.2 delivery path **[design, T5, T9]**:

1. A client message whose `toolResults` answer exactly the pending ids that remain after any retraction, by external id, is delivered as the next turn. The gateway clears the pending set with a conditional write that takes effect only while the set it read is still on the row, maps each external id to its runtime id, runs `PreToolResult` on each result ("CT-8"), and writes the envelope under path 2.
2. On REST, a client message without `toolResults` that sets `abandonPendingToolCalls: true` is delivered as the next turn with one result per pending call, `isError: true`, and a single `text` part stating that the client cancelled the call. The gateway clears the pending set with the same conditional write as rule 1, and each result passes `PreToolResult` as any result does. The message's `input` follows the results, and the `tool_use` events fire with phase `completed` and `isError: true`. A message that sets the field and also carries `toolResults` is refused with `409 CLIENT_TOOL_RESULTS_MISMATCH` (T13). The cancelled results pass `PreToolResult` under T14, and the first client message of a derived session may set the field under T12. The field is a member of the REST message body, MCP clients reach it as a generated argument in the MCP adapter, and the Chat Completions, Responses, and Messages API surfaces have no counterpart.
3. A client message whose `toolResults` name a call the runtime retracted is refused with `409 CLIENT_TOOL_CALLS_RETRACTED` on REST, with the retracted and the remaining external ids in `details`.
4. Any other client message without `toolResults`, or with a missing, extra, or unknown id, is refused: `409 CLIENT_TOOL_RESULTS_MISMATCH` on REST, with the pending external ids in `details`, and `400 invalid_request_error` on the other surfaces, as the provider APIs refuse an unanswered call.
5. A client message with `toolResults` for a session with no pending calls is refused with the same error, unless rule 3 applies. A second, concurrent results message loses the conditional write and is refused the same way, so results are delivered once.
6. `toolResults` or `abandonPendingToolCalls` on a message from any sender other than the session's client is refused with `403 CLIENT_TOOL_RESULTS_FORBIDDEN`. That covers `lenny/send_message` from another session, whatever its messaging scope.

Interrupt, cancel, terminate, and resume calls are unaffected. An interrupt finds no work in flight.

### Hold rule for inbox deliveries

The hold rule applies to a session whose chosen revision does not declare the `steer` delivery intent **[design, T5]**. While such a session has pending calls, every inbox delivery is held in the session inbox as the queue path of [MD-5](message-delivery-design.md#md-5-delivery-paths) holds a message while a turn is open, and the gateway delivers none of the session's held messages. "Delivery of held messages" of §7.2 gains an exception: when the next client message is a results message, the gateway delivers the results message first on the direct path, and the held messages after it, in FIFO order, at the end of the turn the results message starts, one per turn end as MD-5 writes held entries for a runtime that does not declare `steer`. When the results turn itself ends with new calls, the held messages wait again, and they are delivered only after a turn ends with no pending calls, under their ordinary inbox TTL (O3).

Each turn that a held delivery causes carries no client tools, because no client request caused it (T16). A resume that commits such a session to `running` while calls are pending delivers no held messages, because the results have not arrived. The histogram `lenny_client_tool_held_delivery_age_seconds{runtime}` records how long each held delivery waited behind pending calls.

### Steer-declaring runtimes

A session whose chosen revision declares `steer` holds no inbox delivery because of pending calls **[design, T5]**. The gateway delivers each inbox delivery under the machinery of [`message-delivery-design.md`](message-delivery-design.md), and pending calls change nothing in it. The runtime takes on one obligation: a message it receives while its model history holds unanswered calls is placed after the results of those calls when they arrive, so the model never sees a call followed by anything other than its result. The turn that such a delivery opens carries no `conversation.clientTools`, because no client request caused it (T16), so the runtime can make no new client call in it. The client-message rules above apply unchanged, so a client message still answers the pending calls first. O3 does not apply to such a session, and `lenny_client_tool_held_delivery_age_seconds` records nothing for it.

The gateway carries the matching obligation for every history it builds ("CT-7", "Order of history entries"), so a cold turn presents the same order the runtime kept warm.

### Retraction

A runtime retracts some or all of its pending calls by their runtime ids with the `retract` frame of [`message-delivery-design.md`](message-delivery-design.md) MD-11, and receives `retracted`, `too_late`, or `unknown` for each id in a `retract_result` frame **[design, S8 of that design]**:

- **REST.** A call still in the pending set is `retracted`. The gateway removes it from the set with the conditional write that rule 1 uses for results, so a retraction and a results message race on the same write and exactly one takes effect. The client receives a `tool_use` event with `target: "client"` and phase `retracted`. The next results message answers exactly the calls that remain, and one that names a retracted call is refused with `409 CLIENT_TOOL_CALLS_RETRACTED` (rule 3). When no call remains, the session has no pending calls, and the hold rule releases held deliveries as at the end of a turn with no calls. A call that a results message cleared first is `too_late`.
- **Chat Completions, Responses, and the Messages API.** A returned call cannot be withdrawn, so every retraction answers `too_late`. The pending set stays, the client sends results as the provider's API requires, and the runtime ignores them.

A client that already started a retracted call's tool learns of the retraction only from the event, and the tool's side effects stand. The audit event `client_tool_call.retracted` and the counter `lenny_retraction_total` belong to MD-11.

### Cold sessions

A provider adapter's session stays open after a turn that ends with calls, as any session does under the simplified O3 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md#ro-10-the-provider-adapters): it waits `idle` with its pending set on the row, becomes `dormant` at its idle timeout, and the next request continues it warm under that design's R16 or cold in a new session ("CT-7"). A session that a `one_shot` runtime completed at the end of such a turn, under MD-22's completion rule, keeps its pending set on the row for reads and audit until the session's retention ends, and its continuation is a cold turn in a new session.

## CT-6 Surface mapping

> **Adapter-facing section.** The REST rows of "Requests and results" and "Responses with calls", and the REST envelope and codes of "Errors", still govern Lenny. The Chat Completions, Responses, Messages API, and MCP rows, the paragraphs on what those surfaces return, and the provider envelopes of "Errors" move to the matching adapters, as T17 already records. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

### Requests and results

| Surface | Tools and tool choice | Earlier calls and results in history | New results |
| :-- | :-- | :-- | :-- |
| Chat Completions | `tools[].function` to function entries of `clientTools` (`parameters` to `inputSchema`), and `tools[].custom` to custom entries with `format`. `tool_choice` `auto`, `none`, `required` (to `any`), a named function or a named custom tool (to `tool`), and `allowed_tools` (to `allowedNames`, "CT-3"). `parallel_tool_calls: false` to `parallel: false`. | `assistant` messages with `tool_calls` become assistant entries with `clientToolCalls`, with a function call's `arguments` parsed from the JSON string and a custom call's `custom.input` as `input`. `tool` messages become user entries with `toolResults`. | The `tool` messages after the last assistant message become `toolResults`. Each must answer a call of that assistant message, and every call of that message must be answered, or the request is refused with `400 invalid_request_error`. |
| Responses | `type: "function"` and `type: "custom"` tools to `clientTools`. `tool_choice` as Chat Completions, with `{"type":"custom","name"}` to `tool`. `parallel_tool_calls` as Chat Completions. | On a continuation, the cold walk of `R`'s conversation context reads the calls and results from the transcript. A full input list without `previous_response_id` maps its `function_call`, `custom_tool_call`, `function_call_output`, and `custom_tool_call_output` items into history. | `function_call_output` and `custom_tool_call_output` items become `toolResults`, with a string `output` as a `text` part and an array as `text`, `image`, and `file` parts. |
| REST | `clientTools` and `toolChoice` on `POST /v1/sessions/{id}/messages` and on the first message of `POST /v1/sessions/start`, in the CT-1 form. | None. A REST session continues on the same session. | `toolResults` on `POST /v1/sessions/{id}/messages`, by external id, or `abandonPendingToolCalls: true` (T9). |
| Messages API | `tools` (client tools only) to function entries of `clientTools` (`input_schema` to `inputSchema`). `tool_choice` `auto`, `any`, `tool`, and `none`, with `disable_parallel_tool_use: true` to `parallel: false`. The Messages API has no custom tool type and no allowed-tools choice. | `tool_use` blocks in assistant messages and `tool_result` blocks in user messages, as AM-4 maps history. | The leading `tool_result` blocks of the final user message become `toolResults`, with `is_error` to `isError` and string or block content to parts. Text after them becomes `input`. |
| MCP | The REST fields as arguments of `lenny/create_session` and `lenny/send_message`, generated from the OpenAPI definitions under [§15.2.1](../spec/15_external-api-surface.md#1521-restmcp-consistency-contract) rule 4. | None. | `toolResults` or `abandonPendingToolCalls` on `lenny/send_message`. |

T1 requires the tools on every turn, so a request that continues a conversation and carries results but no tools delivers the results with no client tools, and the runtime cannot call another client tool in that turn.

### Responses with calls

| Surface | Plain response | Stream |
| :-- | :-- | :-- |
| Chat Completions | `message.content` holds the text of the output parts, or `null` when there is none, and `message.tool_calls` holds one `{id, type: "function", function: {name, arguments}}` per function call, with `arguments` serialized as a JSON string, and one `{id, type: "custom", custom: {name, input}}` per custom call. `finish_reason: "tool_calls"`. | Content deltas for the output parts, then one chunk per call carrying `index`, `id`, `type`, `function.name`, and the whole `function.arguments`, then a separate chunk with `finish_reason: "tool_calls"`, then `[DONE]`. The separate final chunk lets the `openai-python` stream helper emit its arguments-done event. A streaming request that declares a custom tool is refused under O10, so no custom call is streamed. |
| Responses | `message` output items for the output parts, then one `function_call` item per function call with `call_id`, `name`, `arguments` as a JSON string, and `status: "completed"`, and one `custom_tool_call` item per custom call with `call_id`, `name`, `input`, and `status: "completed"`. The response's `status` is `completed` **[unverified]**. | Per function call: `response.output_item.added` with empty `arguments`, one `response.function_call_arguments.delta` with the whole string, `response.function_call_arguments.done`, and `response.output_item.done`. Per custom call: `response.output_item.added` with empty `input`, one `response.custom_tool_call_input.delta` with the whole string, `response.custom_tool_call_input.done`, and `response.output_item.done`. Then `response.completed`. |
| REST | The message route's result and the `tool_use` events with `target: "client"` carry the calls, and `GET /v1/sessions/{id}` reports `pendingClientToolCalls`. A retracted call leaves `pendingClientToolCalls` and fires a `tool_use` event with phase `retracted`. | The session event stream carries the `tool_use` events. |
| Messages API | `text` blocks for the output parts, then one `tool_use` block per call with `id`, `name`, and `input`. `stop_reason: "tool_use"`. | Per call: `content_block_start` with the `tool_use` block and `input: {}`, one `input_json_delta` with the whole input, and `content_block_stop`. Then `message_delta` with `stop_reason: "tool_use"` and `message_stop`. |
| MCP | The tool result's `structuredContent` carries `pendingClientToolCalls`. | Not applicable. |

The runtime delivers calls whole, so no surface streams partial arguments. Each surface's documented accumulation accepts one delta that carries the whole value.

On Chat Completions, Responses, and the Messages API a call returned to the client is never withdrawn. A runtime's retraction of it answers `too_late`, the client's results are delivered as on any results message, and the runtime ignores the results for the retracted calls ("CT-5", "Retraction").

### Errors

Every refusal uses the surface's own error envelope: Lenny's REST envelope with the codes of this design on REST, the OpenAI envelope on Chat Completions and Responses, and the Anthropic envelope of AM-8 on the Messages API, with the Lenny code in `error.details.error_code`.

| Code | Category | REST status | Meaning |
| :-- | :-- | :-- | :-- |
| `CLIENT_TOOLS_UNSUPPORTED` | `PERMANENT` | 422 | The chosen revision does not declare `capabilities.clientTools`, or does not declare `customTools` for a request with a custom tool, or under O4 the session cannot take a further message. |
| `CLIENT_TOOL_DEFINITION_INVALID` | `PERMANENT` | 400 | A tool name, schema, form, or limit is refused at admission. |
| `CLIENT_TOOL_NAME_RESERVED` | `PERMANENT` | 400 | A tool name begins with `lenny` or is in `reservedNames`. |
| `CLIENT_TOOL_RESULTS_MISMATCH` | `PERMANENT` | 409 | A message does not answer exactly the pending calls and does not set `abandonPendingToolCalls`, carries results together with `abandonPendingToolCalls`, or carries results when none are pending. |
| `CLIENT_TOOL_CALLS_RETRACTED` | `PERMANENT` | 409 | A results message names a call the runtime retracted on a REST session. [`message-delivery-design.md`](message-delivery-design.md) MD-11 adds the code. |
| `CLIENT_TOOL_RESULTS_FORBIDDEN` | `POLICY` | 403 | Results or `abandonPendingToolCalls` from a sender other than the session's client. |
| `CLIENT_TOOL_CALL_INVALID` | `PERMANENT` | 502 | The runtime's end-of-turn `message` broke a rule of "CT-4". |

## CT-7 Continuation

> **Adapter-facing section.** **Runtime obligations**, **Runtime revisions**, **Derive**, **Re-running a session**, and the REST parts of **Resume after pod failure**, **Idle suspension**, and **Retries** still govern Lenny. **Warm and cold** (with T4), the Responses and Messages API parts of the other paragraphs, and the join of O5 move to the provider adapters. **Order of history entries** binds the Open Responses adapter's cold walk, which moves with that adapter. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

**Warm and cold [design].** A Responses, Messages API, or Chat Completions continuation is warm under the conditional send, R16 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md#ro-10-the-provider-adapters), which the adapter reaches by response ID or by conversation fingerprint, and a turn that ended with calls is a completed turn for those conditions. A warm turn delivers `toolResults` with runtime ids. A cold turn creates a session, delivers the earlier calls and results in `conversation.history` with external ids, and delivers the final results in `toolResults` with external ids. A Chat Completions round trip is warm or cold under the same rule (T4, as superseded). The Messages API digest covers `tool_use` and `tool_result` blocks, so the continuation of a turn that ended with calls finds the session that issued them.

**Order of history entries [design, T5].** Whenever the gateway builds `conversation.history` from a transcript, as the Responses cold walk of `R`'s conversation context does in `continuity.rehydrate` (`pkg/gateway/environment/translator/continuity.go`), it places a message that arrived while calls were pending after the entry that carries their results. Each assistant entry with `clientToolCalls` is then followed directly by the user entry with its `toolResults`. When those results are the turn's new input and arrive in the envelope's `toolResults`, the message follows the marked new entry. The rule matters for a steer-declaring runtime, whose inbox deliveries reach the session while calls are pending and can be recorded between a call and its result. A history the client supplies, on Chat Completions, on a Responses request without `previous_response_id`, and on the Messages API, is the client's own, and the gateway does not reorder it.

**Runtime obligations [design, O2].** A declaring runtime accepts results warm, accepts results cold from history, and accepts results for calls it no longer holds in memory after a resume, continuing from its restored state. A runtime that cannot continue answers with an end-of-turn `message` carrying `error`, which fails the turn.

**Resume after pod failure [design].** Pending calls are session-row data, so [§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume) needs no new step. On REST the results reach the restored runtime as a `toolResults` turn. On the provider surfaces a session recovered from a checkpoint taken after the turn that ended with calls continues warm, and one recovered from an older checkpoint fails R16's condition on the session's state, so the continuation is cold.

**Idle suspension [design].** A session idle past `maxClientIdleSeconds` with pending calls becomes `dormant` as any session does ([SL-6](session-lifecycle-design.md#sl-6-idle-pods-and-dormancy)). On REST its results message resumes it on the resume path of [MD-5](message-delivery-design.md#md-5-delivery-paths), with the hold rule of "CT-5". On the provider surfaces the session becomes `dormant`, and the continuation wakes it and runs warm under R16.

**Retries [design, O5].** A client SDK that retries a results request after a dropped connection reaches the same session. On the Messages API the in-flight join of the owner's M1 decision reattaches the retry to the running turn. On Responses, the join of O5 reattaches a continuation whose input equals that of the running continuation of the same response. On REST the retry finds no pending calls and is refused with `409 CLIENT_TOOL_RESULTS_MISMATCH`, and the client reads the turn's outcome from the session. The client executed the tool before it sent the results, so no retry runs a client tool again.

**Runtime revisions [design].** The capability is read from the chosen revision at the post-chain point. A warm turn stays on its session's revision. A cold turn of an existing conversation is a new create and may land on another revision under RV-2's rollout key, which defaults to the user and keeps a user on one revision while traffic is unchanged. When the new revision lacks the capability, the request is refused before a pod is claimed, with the refusal naming the revision. A rollout that removes the capability therefore breaks conversations in flight, and the rollout documentation states it.

**Derive [design, O8, T10 to T12].** A `from: latest` derive from a session with pending calls copies them. At create, [`session-derive-design.md`](session-derive-design.md) SD-1 copies the source's pending set to the derived session's row with each call's runtime id, external id, name, and arguments or input. A `from: start` derive copies none, because it restarts from the state before the source's turns, where no call was issued, and the derived session starts with no pending calls (T10). The derived session's first client request must carry the results under the delivery rule of "CT-5", or on REST set `abandonPendingToolCalls: true` in their place (T12). The derived runtime did not issue the calls, so it receives the results under the runtime obligation above for results of calls it does not hold in memory. The results carry the source's runtime ids, which a runtime that restores its conversation from the workspace snapshot recognizes, as after a resume. A `from: latest` derive whose chosen revision does not declare `capabilities.clientTools`, or does not declare `customTools` while a copied call names a custom tool, is refused at the post-chain point with `422 CLIENT_TOOLS_UNSUPPORTED` before any pod is claimed, because the derived session could never deliver the results (T11).

**Re-running a session [design, O8].** Server-side replay no longer exists, because [`session-derive-design.md`](session-derive-design.md) removes `POST /v1/sessions/{id}/replay`. A client that re-runs a session reads its client messages through SD-1's "Reading client messages", sends them again, and executes its own tools when the new session returns calls. The read returns `toolResults` with each message, so the client can see what it answered.

## CT-8 Security

**Trust of results [design].** A result comes from the session's own client, so it has the client's trust level, and the envelope carries `from.kind: client`. A runtime treats result content as untrusted input, as it treats a user message. The new exposure is indirect: a client tool may return third-party content, such as a fetched page, and an instruction injected into that content can steer the agent toward its other tools, such as the tools of an operator service, which can act with the user's linked credentials through a `userCredential` binding. Tool-use approvals on platform tools, egress rules, and credential bindings apply to those downstream calls unchanged, and an operator service applies its own tool rules and approvals (CR-3).

**Interceptors [design, O6].** The gateway runs `PreToolResult` over each result before the envelope is written, including each "cancelled by client" result that `abandonPendingToolCalls` creates (T14), with the §4.8 payload `{id, content, isError}` and the external id as `id`. A `REJECT` refuses the results message with the interceptor's error, and the pending set stays on the row. A `MODIFY` replaces `content`. The gateway runs `PostAgentOutput` over the turn's output parts and, under O6, its calls with their arguments or custom input, before the calls are recorded or returned. A `MODIFY` that changes a call's arguments or input is validated again under "CT-4", and a modified call that fails validation fails the turn. Both phases run on every surface, including the OpenAI translators and the Messages API, which run no interceptor phase today ("Current state").

**Schema loading [design].** A client's `inputSchema` is untrusted, so the compiler's `LoadURL` refuses every URL. Without it, a `$ref` naming a `file` URL would read a file from the gateway's filesystem, because the library's default loaders resolve that scheme ("Current state").

**Reserved names [design].** The gateway refuses a client tool whose name begins with `lenny` or is in the revision's `reservedNames`. The names of the tools a runtime runs in its own process, such as an operator service's MCP tools, are known only to the runtime, so the gateway cannot check them at admission. The runtime presents its own, platform, and client tools to its model without collision, and a runtime that finds a collision answers with an end-of-turn `message` carrying `error`.

**Approvals [design].** SO-5a rules match platform tool names. A client tool call never reaches the approval gate, because the platform executes nothing, and the client decides whether to run it. A rule whose name matches a client tool has no effect on that tool, and the documentation states it. A deployer who wants no client tools for a tenant clears the capability through the per-tenant override (O7).

**Ownership and impersonation [design].** A results message is session input, so session ownership under [§10.2](../spec/10_gateway-internals.md#102-authentication) **Session ownership.** governs it, and the warm conditions of each surface bind a continuation to its owner. Whatever rule closes F-13.3.26 for session input applies to results messages in the same change.

**Audit and events [design].** Audit records carry call ids, names, and each result's `isError`, and omit arguments, custom input, and result content. A results message delivered under `abandonPendingToolCalls` is audited as such. The transcript holds arguments, custom input, and result content, and the §12.8 erasure of the session removes them with the pending set (CTR-8). `tool_use` events carry arguments to readers of the session's event stream, who already read the session's output.

## CT-9 Delegation

A child session never receives client tools **[design, T7]**. The delegation `TaskSpec` of [§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism) has no client-tools field, the gateway writes no `conversation.clientTools` on an envelope addressed to a child, and an end-of-turn `message` from a child that carries calls is the violation `no_tools_declared`. A parent that wants a child to use the result of a client tool calls the tool itself and passes the result to the child as input. A compromised child therefore cannot direct the root client to execute anything.

## CT-10 Runtime SDKs and conformance

**SDKs [design].** The Go, Python, and TypeScript runtime SDKs add `Message.ToolResults` and the `Conversation` members (`ClientTools`, `ToolChoice`, and history entries with calls and results, with custom tool entries and calls and `ToolChoice.AllowedNames`), and `Reply.ClientToolCalls`, in each language's naming convention. The SDKs add no blocking helper: a runtime author who wants to park a model loop's call in memory between turns does so in the runtime, under the obligations of "CT-7".

**Conformance [design].** [§15.4.6](../spec/15_external-api-surface.md#1546-conformance-test-suite) gains cases for a runtime that declares the capability: it ends a turn with calls that pass "CT-4", accepts results warm, accepts results cold from history with external ids, honours `toolChoice` `none`, `parallel: false`, and `allowedNames`, accepts results it does not hold in memory after resume and after a `from: latest` derive, and continues after retracting a pending call. A runtime that also declares `customTools` ends a turn with a custom call whose `input` is a string. A runtime that declares `steer` places an inbox delivery received while calls are pending after their results. The suite runs them against a conformance fixture runtime that declares the capability. The §15.4.4 sample echo runtime and the §26 reference runtimes do not declare it (T3).

## Worked example

Alice, a user of tenant `acme`, uses the OpenAI Python SDK with the Responses API. The runtime `order-agent` revision 3 declares `conversationInput`, `clientTools.supported: true`, `multi_turn`, and injection. Revision 3 does not declare the `steer` delivery intent.

1. Alice sends `model: "order-agent"`, a function tool `lookup_order`, and "Where is order A-1009?". The gateway checks the tool, chooses revision 3, finds the capability at the post-chain point, and creates session `S`. The runtime receives the question with `conversation.clientTools` and answers with an end-of-turn `message` whose `clientToolCalls` holds `{id: "tc_7", name: "lookup_order", arguments: {orderId: "A-1009"}}`. The gateway validates the arguments against the schema, mints `call_8Kq...`, records the pending call on `S`, and returns response `S.1` with a `function_call` item.
2. A parent session sends `S` an inter-session message. `S` has a pending call, so the message is held in `S`'s inbox.
3. Alice's program runs `lookup_order` and sends `previous_response_id: "S.1"` with a `function_call_output` for `call_8Kq...` and the tool again. The warm conditions hold, the gateway reserves turn 2, clears the pending set, runs `PreToolResult`, and delivers a `message` with `toolResults` for `tc_7`. The held message from the parent waits for this turn to end.
4. The runtime answers "Order A-1009 shipped on 3 November." with no calls. The turn end releases the parent's message, and the turn it causes carries no `conversation.clientTools` (T16). Its output reaches the transcript and the session's REST event stream, and no response on the Responses surface, under O3 of [`message-delivery-design.md`](message-delivery-design.md).
5. Bob, also of `acme`, sends `previous_response_id: "S.1"` with a result. He may not read `S` under session ownership, so the gateway answers the continuation with a native `404` before any session is created, as §15 states for an id that `GET /v1/responses/{id}` would not return to him.

## Limits

- **Runtime cooperation.** A runtime must stop its model loop at a client tool call and continue it from results in a later turn, possibly in a new session from history. Harnesses with a fixed loop cannot do this without a wrapper, and Lenny's reference runtimes do not declare the capability in v1.
- **Chat Completions creates a session when its history changes.** A round trip whose conversation the fingerprint does not find, because the client edited, summarized, or reordered its history, creates a session, which counts against admission and the session-creation rate limits and claims a warm pod.
- **Forced tool choice depends on the runtime's model.** The Anthropic documentation states that `tool_choice` `any` and `tool` return a 400 error on several current models. A runtime on such a model cannot honour a forced choice, and its turn fails under "CT-4" when it returns no call.
- **Held deliveries wait.** For a runtime that does not declare `steer`, a long client tool loop delays inter-session messages, and under O3 a held message can expire under its inbox TTL.
- **Grammar conformance is unchecked.** The gateway never checks a custom call's input against its grammar, so a defective runtime can return input that breaks it, and the client's own check is the only one (O9).
- **No streamed custom calls on Chat Completions.** Under O10, a streaming Chat Completions request with a custom tool is refused.
- **Retraction on provider surfaces.** A call returned on Chat Completions, Responses, or the Messages API cannot be withdrawn. The runtime's retraction answers `too_late`, and the client still runs the tool and sends its result.
- **Abandoned calls on provider surfaces.** Chat Completions, Responses, and the Messages API have no `abandonPendingToolCalls`, so a client of those surfaces answers each pending call, with an error result when it chose not to run the tool.
- **A rollout can break conversations.** A cold continuation that lands on a revision without the capability is refused.
- **Validation cost.** Each call is validated against a schema the client supplied, bounded by the limits of "CT-3".
- **Arguments in events.** `tool_use` events carry arguments to every reader of the session's event stream.

## Alternatives rejected

- **The bridge of AM-7.** A blocking `tool_call` marked for the client keeps the runtime's turn open across HTTP requests. It needs open-turn machinery at the gateway and a persisted pending call for failover, and a pod failure or a cold continuation still needs the turn-ending path. The owner removed open turns.
- **Client tools as platform tools through the reference MCP bridge.** A harness that already speaks MCP would see client tools with no change through the bridge of [`runtime-protocol-design.md`, "RP-5"](runtime-protocol-design.md#rp-5-reference-mcp-bridge). A `tools/call` and the `tool_call` frame it becomes both block inside the turn and hold the harness's loop open, so this is the bridge of AM-7 with an MCP carrier, and the MCP `2026-07-28` input-required result cannot carry a tool call. A runtime that drives its model loop without the bridge would also need a second path.
- **`lenny/request_input` or elicitation as the carrier.** Both block inside a turn and carry a question for a person. A tool result is program output with per-call structure, and the Messages and OpenAI adapters declare no elicitation.
- **New `MessagePart` types or frame types.** Unknown part types collapse to `text`, and a new frame type adds a schema and a demultiplexer case for what members express.
- **Interception in the LLM proxy.** The proxy could add the client's tools to a model request, but the runtime's loop would receive calls for tools it does not know.
- **Repairing calls at the gateway.** Coercing arguments or dropping extra calls would hide a runtime defect from its author and return calls the runtime never made (T6).

## Specification changes

| Section (by heading) | Change |
| :-- | :-- |
| §28.5.3 Intra-pod, `CH-MSGSOCK` card, **Outbound: `response`**, the `tool_call` frame, and "Message schemas" | `clientToolCalls` on the end-of-turn `message` that "MD-24" of the message delivery design stages in place of `response`, with its validation by reference to the external API section. A sentence on the `tool_call` frame stating that client tools never use it. "Tool access by level", where revision 5 placed that sentence, leaves under decision D5 of [`runtime-protocol-design.md`](runtime-protocol-design.md#decisions). The JSONL schema references. No channel is added, so the §28.3 registers and the naming law are unaffected. |
| §15.4.1 Message Format and Binary I/O Requirements, "`MessageEnvelope` — Unified Message Format" and "Translation Fidelity Matrix" | `toolResults`, and the `clientTools` (function and custom entries), `toolChoice` (with `allowedNames`), and history-entry members of the conversation input that AM-4 adds, present only on envelopes of turns a client request caused (T16). Rows for calls and results per surface, including custom calls. |
| §5.1 Runtime, capability fields and the derived-runtime rules | `capabilities.clientTools` with `supported`, `customTools`, and `reservedNames`, its dependency on `conversationInput`, and its override rule (O7). |
| §7.2 Interactive Session Model, "Message delivery routing" and "Delivery of held messages" | The delivery rule for a session with pending calls, `abandonPendingToolCalls` (T9, T12 to T14), the refusals, the removal of retracted calls from the pending set, the hold rule with its exception to "Delivery of held messages" for runtimes that do not declare `steer` (T5 and O3), and the steer-declaring runtime's obligation to place a delivery after the results. The text cites the delivery intents that [`message-delivery-design.md`](message-delivery-design.md) adds. |
| §7.3 Retry and Resume | A sentence that pending calls survive resume as session data, the runtime obligation for results it does not hold, and the order of entries in a history the gateway builds. |
| §15 External API Surface, "Built-in adapter single-shot compute model", "Open Responses conversation sessions", and "SessionEvent Kind Registry" | The Chat Completions and Responses mappings, including custom tools and `allowed_tools` (O9) and the refusal of a streaming Chat Completions request that declares a custom tool (O10), `too_late` for a retracted call on these surfaces, a turn ending with calls as a completed turn, the cold walk's calls and results in the order of "CT-7", the retry join (O5), and `target` on the `tool_use` payload. |
| §15.1 REST API and its error code catalog | `clientTools`, `toolChoice`, `toolResults`, and `abandonPendingToolCalls` on the message routes, `pendingClientToolCalls` on the session read, the copy of the pending set on a `from: latest` derive that SD-1 rewrites (T10 and T11), and the error codes of "CT-6", among them `CLIENT_TOOL_CALLS_RETRACTED`, which the message delivery design adds. |
| §15.2 MCP API and §15.2.1 REST/MCP Consistency Contract | The generated arguments and result fields on `lenny/create_session` and `lenny/send_message`, and contract-test coverage. |
| §15.4.3 Runtime Integration Levels, §15.4.6 Conformance Test Suite, and §15.7 Runtime Author SDKs | Availability to every runtime, stated in the capability terms that [`runtime-protocol-design.md`](runtime-protocol-design.md#specification-changes) puts in place of the integration levels, the conformance cases, and the SDK types. |
| §4.8 Gateway Policy Engine, the phase table | `PreToolResult` on client results, and the `clientToolCalls` member of the `PostAgentOutput` payload (O6), on every surface. |
| §8.2 Delegation Mechanism | No client tools on children. |
| §10.2 Authentication, **Session ownership.** | Results messages as session input. |
| §12.8 Compliance Interfaces | The pending set on the session row in the session erasure. |
| §13 Security Model | The trust level of results, reserved names, schema loading, and the approvals statement. |
| §16.1 Metrics | `lenny_client_tool_call_invalid_total` and `lenny_client_tool_held_delivery_age_seconds`. |
| §26 Reference Runtime Catalog | A statement that no reference runtime declares `capabilities.clientTools` in v1. |

## Non-specification changes

- `pkg/gateway/session/executor`: `Message` gains results and conversation input, `Response` gains calls, `responseEnvelope` and `ingestResponse` decode `clientToolCalls` (and `error`, see "Defects found while verifying"), and the echo executor gains a client-tool mode for tests.
- A new package for the client tool rules, `pkg/gateway/session/clienttools`: definition checks, schema compilation with refused loading, the `format` check of custom tools, the `allowed_tools` mapping, response validation, id minting, and the violation metric.
- `pkg/gateway/session/sessionstore`, its Postgres store, and `migrations/`: the pending set on the session row and in the session read.
- `pkg/gateway/session/messagerouting` and `pkg/gateway/session/sessioninbox`: the delivery rule, `abandonPendingToolCalls`, the refusals including `CLIENT_TOOL_CALLS_RETRACTED`, the hold rule and its exception for steer-declaring runtimes, and the held-delivery age metric.
- `pkg/gateway/sessionserver`: REST fields, validation, error codes, and the OpenAPI document. `derive.go`: the copy of the pending set and the post-chain capability check on a `from: latest` derive, in the SD-1 rewrite of the handler.
- `pkg/gateway/mcpfabric/mcptools`: generated arguments and result fields, and the refusal of `toolResults` on the agent-facing `lenny/send_message`.
- `pkg/gateway/environment/translator`: tool decoding and refusals, custom tools and `allowed_tools`, history mapping with the entry order of "CT-7" in `continuity.go`, response and stream encoding, id mapping, and the interceptor phases, for Chat Completions, Responses, and later the Messages adapter.
- `pkg/apis/lenny/v1alpha1/runtime_types.go`, `pkg/gateway/runtime/runtimestore`, and `runtimecapoverride`: the field, its registration check, and its override rule. The post-chain check in the create-and-start service.
- `schemas/lenny-adapter-jsonl.schema.json`: `clientToolCalls` and `toolResults`.
- `sdks/runtime/go`, `sdks/runtime/python`, and `sdks/runtime/typescript`: the types of "CT-10".
- `cmd/lenny-gateway` and the chart: flags and Helm values for the limits of "CT-3".
- Documentation under `docs/api/`, the runtime author guide, the configuration and metrics references, and the error catalog. The pages state every item below:
  - Settings and defaults: `clientTools.maxTools` (default 128), `clientTools.maxSchemaBytes` (default 256 KiB per request, schemas and grammar definitions together), `clientTools.maxPendingCalls` (default 32 per turn), and `clientTools.maxArgumentsBytes` (default 256 KiB per call), each a Helm value and gateway flag that the operator tunes, and the part size rule that result content follows (64 KB inline, 50 MB staged, `413 MESSAGEPART_TOO_LARGE` above).
  - Client rules per surface: the tool forms each surface accepts and refuses, custom tools and their `format`, `allowed_tools` and its mapping, the refusal of a streaming Chat Completions request with a custom tool, reserved names, the delivery rule for results, `abandonPendingToolCalls` on REST, retraction with the `retracted` phase on REST and `too_late` on the provider surfaces, cold continuations, and the copy of pending calls on a `from: latest` derive.
  - Failure modes: the codes of "CT-6" with their categories and statuses, the violation reasons of "CT-4", and the native error each provider surface returns.
  - Limits: every item of "Limits", including runtime cooperation, a session per Chat Completions round trip whose history changed, forced tool choice on models that refuse it, held deliveries, unchecked grammars, rollouts that break conversations, and arguments in events.
  - Runtime-facing rules, in the runtime author guide, the adapter contract page, and the SDK references: `capabilities.clientTools` with `supported`, `customTools`, and `reservedNames` and its dependency on `conversationInput`; `conversation.clientTools`, `conversation.toolChoice`, `toolResults`, and `clientToolCalls`; the history entries of a cold turn; the obligations of "CT-7" for results the runtime does not hold; the steer-declaring runtime's placement of inbox deliveries after the results; and the absence of client tools in a turn that no client request caused (T16).
  - Metrics: `lenny_client_tool_call_invalid_total` and `lenny_client_tool_held_delivery_age_seconds`.

## Defects found while verifying

These are defects in the current tree, found by reading the code and the specification on 2026-11-09. Nothing was run. The assessment's protocol findings are filed in `BUILD-GAPS.md` under the IDs below. The members this design adds (`clientToolCalls` on `response` and `toolResults` on `message`) use neither the `tool_call` frame nor the approval relay, so the design depends on neither defect and on neither fix.

| ID | Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- | :-- |
| CTF-1 | `PodExecutor.handleFrame` discards a relayed `tool_call` that is neither adapter-local nor approval-gated, and writes no `tool_result`. The adapter relays every `tool_call` whose name is not adapter-local, and the gateway executor sends one with `approvalRequired` to the approval gate, completes the turn on `response`, and drops every other frame. A runtime that emits a `tool_call` naming an unknown tool waits out its own timeout, which in the Go SDK is the SDK's dial timeout, 5 seconds by default. §28.5.3 states no outcome for a `tool_call` naming an unknown tool. | `handleFrame` and `maybeGateToolCall` in `pkg/gateway/session/executor/podstream.go`. `HandleToolCall` in `pkg/adapter/toolcall.go` returns `handled` false for a name that `localtools.IsLocalTool` rejects, and the Attach loop in `pkg/adapter/attach.go` relays it. `AdapterTools.ToolCall` in `sdks/runtime/go/runtime/tool.go` takes its timeout from `dialTimeout` (`runtime.go`), whose default is 5 seconds (`options.go`). | F-28.5.3.1 [Medium] |
| CTF-2 | On approval, `verdictFrame` re-sends the runtime's own `tool_call` with the approval flag cleared, and the adapter writes it to the runtime's stdin. That is a `tool_call` from the gateway to the runtime, a direction §28.5.3 does not define: the `CH-MSGSOCK` card's adapter-to-runtime list does not include `tool_call`, and `approvalRequired` appears nowhere in `spec/`. The Go SDK's `routeFrame` ignores an inbound `tool_call`, so an approved call never runs. The JSONL schema's `tool_call` description says the adapter publishes the approval event and blocks the call, where the code gates it in the gateway executor, and it names platform MCP tools as `tool_call` targets, which "Tool access by level" excludes. | `verdictFrame` in `podstream.go`. `attachRecvLoop` in `pkg/adapter/attach.go`. The `tool_call` definition and its `approvalRequired` property in `schemas/lenny-adapter-jsonl.schema.json`. `routeFrame` in `sdks/runtime/go/runtime/runtime.go`. SO-5a states that enforcement at `maybeGateToolCall` applies "whatever the runtime's flag says", which relies on this relay. | F-28.5.3.2 [Low] |

Further observations surfaced while verifying. They bear on this design and are recorded for the specification owner without filing:

- The gateway executor does not read the `error` member of a runtime's `response`. `responseEnvelope` decodes `type`, `text`, and `output`, and §28.5.3 states that `error` maps the task to `failed`. The runtime obligations of "CT-7" and the `error_with_calls` rule of "CT-4" depend on the gateway reading it.
- The OpenAI translators invoke no interceptor phase, so `PostAgentOutput` does not run on Chat Completions or Responses output today. CTR-6 requires it for call arguments.
- `validateAgainstSchema` compiles a runtime's `runtimeOptionsSchema` with the library's default loaders, which resolve `file` URLs. A schema written by a runtime author, including a tenant that writes a tenant-derived runtime, can name a file on the gateway's filesystem in a `$ref`. Whether the validation report can disclose its content was not checked.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Definition checks and refusals per surface, including names, reserved names, refused tool forms, custom tool `format` values, `allowed_tools` entries, limits, and a schema with a `file` or `https` `$ref`. Each violation reason of "CT-4", including `input_form`, `tool_choice_allowed`, and `no_tools_declared` for a call in a turn that another session's message caused, and a custom call whose input breaks its grammar passing. Id minting and mapping. The delivery rule's refusals, `abandonPendingToolCalls` with and without `toolResults`, `PreToolResult` on the cancelled results, and the conditional clear. A retraction on REST removing a call from the pending set, a results message naming a retracted call refused with `409 CLIENT_TOOL_CALLS_RETRACTED`, a partial retraction leaving the remaining calls answerable, and `too_late` on each provider surface. A streaming Chat Completions request that declares a custom tool refused with `400 invalid_request_error`. `allowed_tools` with `mode: required` mapped to `type: any`. The order of history entries when a message arrived while calls were pending, with the results in history and in the envelope. The copy of the pending set on a `from: latest` derive, no copy on a `from: start` derive, and the `422` capability refusal of a `from: latest` derive. History mapping of calls and results per surface. The capability registration check and the override's narrowing. No `conversation.clientTools` or `conversation.toolChoice` on an envelope that opens a turn caused by another session's message, a system message, or an external message (T16). |
| 3 | The Anthropic Python and TypeScript SDKs and the OpenAI Python and TypeScript SDKs against the gateway for a round trip, parallel calls, `is_error`, a forced choice, `allowed_tools`, a custom tool round trip on Chat Completions and Responses, and streaming, with recorded streams checked against each surface's event order. The JSONL schema for `clientToolCalls` and `toolResults` against the runtime SDKs. The generated MCP tool schemas against the OpenAPI document. |
| 4 | An inter-session message to a session whose client declared tools, on a Responses session and on a REST session: the envelope of the turn it causes carries no client tools, and a call in that turn fails with `no_tools_declared` (T16). Warm and cold round trips on the compose stack, a Responses continuation by `previous_response_id`, a Chat Completions round trip as a warm turn found by fingerprint and, after an edited history, as a cold turn, a REST round trip, a REST message with `abandonPendingToolCalls`, an inter-session message held behind pending calls and delivered after the results for a runtime without `steer`, an inter-session message delivered while calls are pending to a steer-declaring runtime followed by a cold continuation whose history places it after the results, a `from: latest` derive from a session with pending calls answered by the derived session's first message and, in a second case, abandoned by it, a `from: start` derive from the same session that accepts an ordinary first message, a held inbox delivery that waits through a results turn ending with new calls and is delivered after a turn with no calls, a REST client that receives the `tool_use` event with phase `retracted`, and owner separation of pending calls. `PreToolResult` and `PostAgentOutput` with a test interceptor on each surface. |
| 5 | A provider-adapter session on Kind through several round trips, and a suspension while calls are pending followed by a REST results message and by a cold continuation. |
| 7a | Concurrent results messages for one pending set: one is delivered and the other is refused. Concurrent Responses continuations under O5. Inbox deliveries arriving during the conditional clear. |
| 8 | Pod loss and replica loss while calls are pending, with results delivered after recovery on REST and cold on Responses. |
| 9 | Reserved names, results or `abandonPendingToolCalls` for another owner's session, results or `abandonPendingToolCalls` through the agent-facing `lenny/send_message`, an impersonation bearer under F-13.3.26, refused schema loading, and redaction of arguments and content in audit records. |
| 10 | The conformance cases of "CT-10". |
| 11 | The API pages and the runtime author guide against the gateway. |

## Validation owed

- The `status` of an OpenAI Responses response whose output ends with a function call, and whether any client SDK reads a value other than `completed` for it.
- Whether the OpenAI Responses API carries `tools` across `previous_response_id`, which decides whether clients already resend tools as T1 requires.
- The Open Responses specification's `function_call_output` item and `parallel_tool_calls`, which the specification page does not define, and whether Open Responses clients accept the `response.function_call_arguments.done` event it does not name.
- Whether the OpenAI TypeScript SDK and the Anthropic SDKs accept one delta carrying a whole call, as `openai-python` does.
- A stream form for a custom tool call on Chat Completions, whose publication would let the refusal of O10 be lifted. The streaming reference and the `openai-python` chunk type read on 2026-11-09 define none.
- Whether an OpenAI `allowed_tools` list may name a custom tool, and in what entry form. The references read on 2026-11-09 type `tools` as a list of tool definitions and show function entries alone.
- Whether `parallel_tool_calls: false` limits custom tool calls as it limits function calls. The function calling guide covers function tools alone.
- Whether Open Responses clients send `custom` tools or `allowed_tools` with a `mode`, which the Open Responses specification does not define.
- The `x-should-retry` handling of the OpenAI and Anthropic TypeScript SDKs, which this design read only for the Python SDKs.
- Whether the session event bus delivers `tool_use` events to an adapter on the replica serving a forwarded warm turn, which the Messages design also lists.

## Instructions for reviewers of other designs

- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): AM-7's bridge is replaced by this design. Remove AM-7's "Call" and "Result" subsections, the `target` and `endOfBatch` members of `tool_call`, the client-tool row of "Open turns" under AM-6, and the §28.5.3 row of its "Specification changes" that adds them. AM-7 becomes a pointer to this design's "CT-6" Messages rows, and its admission paragraph defers to "CT-2" and "CT-3", including the reserved names and the `RequiresClientTools` flag of the single-shot spec. AM-3's `tools` and `tool_choice` rows point to "CT-6". AM-4's `clientTools` and `toolChoice` take the CT-1 form, and its history entries carry `clientToolCalls` and `toolResults`, with the marked new entry's results in the envelope's `toolResults`. AM-5's digest covers `tool_use` and `tool_result` blocks, and its in-flight join covers a results request. AM-6's `stop_reason` list gains `tool_use` for a turn that ended with calls, and its streaming of `tool_use` blocks follows "CT-6". AM-8's table gains the codes of "CT-6", and `CLIENT_TOOL_CALL_INVALID` maps to `500 api_error` with `x-should-retry: false`. AMR-7 is restated as CTR-1 and CTR-3. Proposal AM-E is removed, and its client-tool work lands as CT-B. Its "Limits" bullet on client tools, its tier 5, 8, and 10 test rows for client tools, and O4 point to this design.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): RV-1's field classes list `capabilities.clientTools` as versioned and gateway-read. "Per-tenant capability override" gains O7's narrowing: clear `clientTools.supported`, clear `clientTools.customTools`, and append to `clientTools.reservedNames`. The session metrics list gains `lenny_client_tool_call_invalid_total` with `runtime_revision`.
- [`runtime-routing-design.md`](runtime-routing-design.md): RT-2's `PreRoute` payload carries no client tools, and an interceptor cannot change them. The capability check is a post-chain check.
- [`security-oversight-design.md`](security-oversight-design.md): SO-5a states that its rules never match a client tool. Its reliance on `maybeGateToolCall` for relayed frames meets F-28.5.3.1 and F-28.5.3.2 (CTF-1 and CTF-2), which the proposal that lands SO-5a resolves or cites.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md): no change to MP-1 to MP-8. MCP clients use the generated arguments of "CT-6", and MP-5's intra-pod servers carry no client tools.
- [`session-derive-design.md`](session-derive-design.md): SD-1's "Reading client messages" returns `toolResults` with each message. SD-1 copies the source's pending set to the derived session under `from: latest` alone, and a `from: start` derive copies none (T10). The derived session's first client request must carry the results or, on REST, set `abandonPendingToolCalls` (T12). A `from: latest` derive whose chosen revision lacks `capabilities.clientTools`, or lacks `customTools` for a copied custom call, is refused with `422 CLIENT_TOOLS_UNSUPPORTED` at the post-chain point (T11, O8, and "CT-7").
- [`message-delivery-design.md`](message-delivery-design.md): the hold rule of "CT-5" applies to a runtime without the `steer` intent, and a steer-declaring runtime receives inbox deliveries while client calls are pending and places them after the results (T5). The machinery treats a session with pending calls as any other session for a steer-declaring runtime. A client message on such a session still answers the pending calls first, or sets `abandonPendingToolCalls` on REST (T9). Its MD-11, settled under S8, defines retraction: on REST a retracted call leaves the pending set, the client receives a `tool_use` event with phase `retracted`, and a later results message naming the call is refused with `409 CLIENT_TOOL_CALLS_RETRACTED`. On the provider surfaces the runtime's result is `too_late`, and the runtime ignores the results the client sends. This design states the same rules in "CT-5" and "CT-6" and adds no retraction event of its own. Its O3 rule that client tools are offered only in turns a client request caused is this design's T16, which "CT-1", "CT-4", and "CT-5" state.
- [`delegation-availability-design.md`](delegation-availability-design.md): no change. Client tools never reach a child under T7, independently of delegation availability.
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md): F-13.3.26's list of session input routes gains results messages on every surface.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): no change. Each round trip is a turn of an ordinary session.

## Proposal split and order

> **Adapter-facing section.** CT-A still governs Lenny for the runtime contract, the gateway core, and REST. Its MCP surface and its Chat Completions and Responses mappings of "CT-6", and all of CT-B, move to the adapters. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| CT-A | The runtime contract and the gateway core (CT-1 to CT-5, CT-7 to CT-10), REST, and the Chat Completions and Responses mappings of CT-6. It turns AM-A's refusal of `tools` on the OpenAI translators into the mapping for declaring revisions, and lands alongside the F-15.1.82 fix. | AM-A of the Messages design. |
| CT-B | The Messages API rows of CT-6, replacing AM-E. | CT-A, and the Messages design's continuity proposal AM-C. |

## Sources

Each page below was read on 2026-11-09. For revision 2, the OpenAI create references for Chat Completions and Responses, both streaming references, the function calling guide, the Open Responses specification, and `chat_completion_chunk.py` were read again on 2026-11-09 for `custom` tools and `allowed_tools`.

- Anthropic: [Define tools](https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use), [Messages: create](https://platform.claude.com/docs/en/api/messages/create), [Handle tool calls](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls), [Parallel tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use), [Handling stop reasons](https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons), and [Streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming).
- OpenAI Chat Completions: [Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) and [Streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events).
- OpenAI Responses: [Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create), [Retrieve a response](https://developers.openai.com/api/reference/resources/responses/methods/retrieve), [Streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [Function calling](https://developers.openai.com/api/docs/guides/function-calling), and [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses).
- Open Responses: [openresponses.org](https://www.openresponses.org/) and [Specification](https://www.openresponses.org/specification).
- MCP: [Versioning](https://modelcontextprotocol.io/specification/versioning), [2026-07-28 changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog), [2025-11-25 changelog](https://modelcontextprotocol.io/specification/2025-11-25/changelog), [Sampling](https://modelcontextprotocol.io/specification/2026-07-28/client/sampling), [Multi round-trip requests](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr), [Tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools), and [Tasks extension](https://modelcontextprotocol.io/extensions/tasks/overview).
- SDK source on `main`: [`openai-python` `_base_client.py`](https://github.com/openai/openai-python/blob/main/src/openai/_base_client.py), [`openai-python` `_constants.py`](https://github.com/openai/openai-python/blob/main/src/openai/_constants.py), [`openai-python` chat stream helper](https://github.com/openai/openai-python/blob/main/src/openai/lib/streaming/chat/_completions.py) and [`_deltas.py`](https://github.com/openai/openai-python/blob/main/src/openai/lib/streaming/_deltas.py), [`openai-python` `chat_completion_chunk.py`](https://github.com/openai/openai-python/blob/main/src/openai/types/chat/chat_completion_chunk.py), and [`anthropic-sdk-python` `_base_client.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/_base_client.py) and [`_constants.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/_constants.py).

## Review history

No review has read this design.

### Revision notes

- Revision 1, 2026-11-09: first draft, built from the client-tools assessment of 2026-11-09 and the owner's decisions T1 to T8 of the same date, with the assessment's tree, specification, and external claims re-verified on branch `design/v1-cleanup`. Re-verification corrected some external claims and the Go SDK's tool-call timeout, which is the SDK's 5-second dial timeout. It added the gateway's missing interceptor coverage on the translators and the schema loader's `file` resolution, and changed the assessment's position on argument validation to T6's. The assessment's protocol findings are cited by their `BUILD-GAPS.md` IDs, F-28.5.3.1 and F-28.5.3.2, which the coordinator filed on 2026-11-09.

- Revision 2, 2026-11-09: applies the owner's decisions of 2026-11-09 on revision 1, together with the owner's decisions on [`message-delivery-design.md`](message-delivery-design.md), which this revision reads against at its revision 1. The owner's answers were:
  - "O1-O2, O4-O7: ok". O1, O2, and O4 to O7 move to the settled decisions with revision 1's recommendations. O7's narrowing also clears `customTools`, which O9 adds.
  - "ok on allowed_tools". O9 accepts OpenAI `allowed_tools` as `toolChoice.allowedNames`, which the T6 check enforces under the new violation reason `tool_choice_allowed`.
  - On `custom` tools: "Why does Lenny need to check the arguments? Isn't this something for the client to do?". O9 accepts `custom` tools for revisions that declare `clientTools.customTools`. The gateway checks the name, that the input is a string, `tool_choice`, and the parallel limit, and never checks the grammar. Grammar conformance is the runtime's responsibility and the client's to verify.
  - "O9: ok".
  - "T6: ok". JSON arguments of a function tool are still validated against `input_schema`.
  - For O8: "in the new derive and replay redesign doc, replay is handled by the client so the client can execute the tools again". Server-side replay no longer exists under [`session-derive-design.md`](session-derive-design.md), and a client that re-runs a session executes its own tools. A derive from a session with pending calls copies them, and the derived session's first client request must carry the results.
  - On O3 and T5, through the message delivery decisions: a runtime that declares the `steer` delivery intent receives inbox deliveries while client calls are pending and places them after the results. The hold rule and O3 apply only to other runtimes. The cold-history builder places a message that arrived while calls were pending after their results. O3 stays open with that narrowed scope.
  - On the message delivery investigation's S7: on REST and MCP, a client text message without the pending results is refused unless it sets `abandonPendingToolCalls: true`, which turns the pending calls into "cancelled by client" error results. Provider surfaces always refuse. This design records it as T9.

  The revision verified the OpenAI `custom` tool and `allowed_tools` forms against the live OpenAI references and guide on 2026-11-09 ("Current state" and "Sources"). Because the Chat Completions streaming reference defines no custom call delta, it adds open decision O10, with a recommendation to refuse a streaming Chat Completions request that declares a custom tool. Retraction of pending calls by a runtime stays with the message delivery design, where it is open.

- Revision 3, 2026-11-09: applies the owner's answer of 2026-11-09 on revision 2, "Client tools: ok on everything".
  - A derive copies the source's pending calls only under `from: latest`, and a `from: start` derive copies none, because it restarts before the source's turns (T10). "CT-7", "Scope", the reviewer instructions for the derive design, and the tier 1 and tier 4 tests change to match.
  - The choices revision 2 made without an owner decision become settled decisions T11 to T15: the `422` refusal of a derive onto a revision without `clientTools`, or without `customTools` for a copied custom call; `abandonPendingToolCalls` on the derived session's first message; the `409` refusal of `abandonPendingToolCalls` together with `toolResults`; `PreToolResult` on the cancelled results; and `allowed_tools` with `mode: required` mapped to `type: any`.
  - O3 and O10 are settled with revision 2's recommendations. Held inbox deliveries for a runtime without `steer` stay held until a turn ends with no pending calls, with the wait metric. A streaming Chat Completions request that declares a custom tool is refused. No decision remains open.
  - Retraction is settled in [`message-delivery-design.md`](message-delivery-design.md) under its S8 and MD-11. "CT-5" gains a "Retraction" subsection, the `tool_use` event gains phase `retracted`, the delivery rule refuses a results message naming a retracted call with `409 CLIENT_TOOL_CALLS_RETRACTED`, and "CT-6" states that a provider surface answers `too_late` and the runtime ignores the client's results. The "Out of scope" entry and the reviewer instruction that called retraction open are replaced.

- Revision 4, 2026-11-09: applies the owner's decision of 2026-11-09 on O3 of [`message-delivery-design.md`](message-delivery-design.md), recorded as T16. The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes". Client tools are offered only in turns caused by a client request, so a turn caused by another session's message carries no client tools. "CT-1" states the rule and the case of a client request steered into such a turn, "CT-4" counts a call in such a turn as `no_tools_declared`, and "CT-5" states it for the hold rule and for steer-declaring runtimes that receive inbox deliveries while calls are pending. The high-level design, requirement CTR-10, the worked example, the §15.4.1 specification change, the tier 1 and tier 4 tests, the labels, and the reviewer instruction for the message delivery design change to match. The design now reads against [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 5 and [`message-delivery-design.md`](message-delivery-design.md) revision 2.
- Revision 5, 2026-11-13: applies the owner's decisions of 2026-11-12 that retire the adapter-local tools (D5 of `runtime-protocol-design.md`) and remove pre-launched runtimes with `capabilities.preConnect`, and that design's reviewer instruction on CTR-9. The "Runtime tool" term names platform and connector tools reached through `tool_call` frames, the "Adapter-local tools" term records their retirement, and the reserved-name rule of "CT-3", the `CLIENT_TOOL_NAME_RESERVED` row, and **Reserved names.** of "CT-8" no longer name them, and `reservedNames` of "CT-2" no longer includes `adapterLocalTools`. CTR-9 and "CT-2" state availability to every runtime in place of every integration level. "Current state" no longer names `capabilities.preConnect`. The `output` member of a `response` with calls names parts streamed on `message_part` frames in place of `lenny/output`, which `message-delivery-design.md` removed. The §15.4.3 row of "Specification changes" follows. The design's own rules are unchanged.
- Revision 6, 2026-11-13: the cross-design review of 2026-11-13. It applies decisions O4 and O5 of [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md), which the owner settled on 2026-11-13 in the words "O5 (b), yes to all the others". Under O4 the "Held session" term and the worked example drop the `conversationContinuity: "platform"` condition. Under O5 the `message` and `response` examples drop `sessionId`, and "Current state" and "CT-1" state that no frame other than `session_start` carries it. The alternative of client tools on the intra-pod platform server is restated against the reference MCP bridge of `runtime-protocol-design.md`, because the intra-pod MCP servers and the integration levels it named are gone. The §28.5.3 specification row moves the sentence that client tools never use `tool_call` from "Tool access by level", which that design removes, to the `tool_call` frame. The documentation bullet of "Non-specification changes" lists every setting, default, client rule, failure mode, limit, runtime-facing rule, and metric the design introduces, under the owner's decision D13 of 2026-11-13 in `incremental-checkpoint-design.md`. "Labels from other designs" gains AR O4 and AR O5. The design's own rules are unchanged, and no decision is open.
- Revision 7, 2026-11-13: the read-only checks of the cross-design review of 2026-11-13, and changes these rules. "MD-24" of [`message-delivery-design.md`](message-delivery-design.md#md-24-runtime-frames-and-leniency), settled by the owner as B-O6, replaces the runtime's `response` frame with the end-of-turn `message`. `clientToolCalls` therefore travels on the end-of-turn `message`, whose parts are in `parts` rather than `output`, and the term "End-of-turn `message`", "Scope", "Terms", the high-level design, T1, O2, "CT-1" with its example, "CT-4", "CT-6", "CT-7", "CT-8", the worked example, and the §28.5.3 row follow. "CT-4" gains the violation `not_end_of_turn` for calls on a `message` that sets `endOfTurn: false` or `complete: true`, because the merged frame can carry calls on a mid-turn message or on one that completes the session. A session with pending calls is `idle` under SL-1 of [`session-lifecycle-design.md`](session-lifecycle-design.md#sl-1-states), which removes the `input_required` sub-state. "Current state" and "Defects found while verifying" keep `response`, because they describe today's tree and specification. The status line's reads-against list names the current revision of each cited design.
- Revision 8, 2026-11-13: records the owner's choice of 2026-11-13 on the consolidated list of open decisions, in the words "ok, go with (b)", as decision T17: every client protocol adapter moves out of Lenny into a separate first-party repository with an optional Helm subchart in front of the REST API. `rest-only-client-api-design.md`, which is being written, governs what leaves, and this revision changes no rule and applies no removal. The status line names the choice. The status line's reads-against list names the current revision of each cited design.
  - The last round of the same review, which changes no rule, applies the reviewer instruction of `message-delivery-design.md` on the delivery terms: the "Delivery paths" term names the named paths of MD-5, an "Open turn" term replaces the `ready_for_input` term, CT-5's hold rule holds inbox deliveries as the queue path does and delivers held messages at the end of the results turn in place of the runtime's next `ready_for_input`, O3's reasoning names the turn end, and a suspended session's results message resumes it on the resume path. It also cites the designs this one reads against at their current revisions.
- Revision 9, 2026-11-14: applies two decisions of [`rest-only-client-api-design.md`](rest-only-client-api-design.md) that the owner settled on 2026-11-14.
  - **O8.** The owner answered "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something". A banner after the status line, and a banner on each adapter-facing section, "CT-3", "CT-6", "CT-7", and "Proposal split and order", state which parts still govern Lenny and which move to the adapters, as that design's instruction to this one lists them. Nothing is deleted or moved, and no rule changes.
  - **The simplified O3.** The owner answered "yes": no held-session concept and no close-after-turn rule. The "Held session" term becomes "Provider-adapter session", which states that the adapter creates ordinary sessions, may shorten their idle timeout, which an operator lengthens through the pool's idle timeout, that a `one_shot` runtime ends its own session, and that follow-ups are routed by conversation fingerprint. The "Open Responses conversation sessions" term and the tier 5 test name the session without the hold. CT-5's hold rule, which holds inbox deliveries while client calls are pending, is a different rule and is unchanged.
  - The status line names [`rest-only-client-api-design.md`](rest-only-client-api-design.md) without calling it a design being written, and its reads-against list names the current revision of each cited design that changed in this pass.
- Revision 10, 2026-11-14: the final pass of that day, and changes these rules.
  - **REST.** "REST and MCP" becomes REST in CT-2, CT-5, T5, T9, T12, O4, CTR-3, and the other sections that still govern Lenny; MCP clients reach `abandonPendingToolCalls` as a generated argument in the MCP adapter.
  - **T4.** Superseded by the conversation fingerprint rule of "RO-10": a Chat Completions round trip continues warm on the session the fingerprint finds, under R16, and cold otherwise. "Scope", "High-level design", **Cold sessions** in CT-5, **Warm and cold** in CT-7, "Limits", the documentation, and the tier 4 test follow.
  - **R16 and O12.** A provider session that became `dormant` continues warm once restored, and one recovered from a checkpoint older than the turn continues cold. **Resume after pod failure** and **Idle suspension** in CT-7 follow.
  - The status line's reads-against list names the current revision of each cited design.
- Revision 11, 2026-11-15: applies the owner's decision of 2026-11-15 to remove connectors, in the owner's words "Yes, let's drop connectors.", under [`connector-removal-design.md`](connector-removal-design.md) revision 2, and changes these rules.
  - The "Runtime tool" term names platform tools and the tools a runtime runs in its own process, which include an operator service's tools reached through egress. The "Tool-use approval" term and the SO-5a label row name platform tools alone, and a CR label row is new.
  - CT-8 **Trust of results** names the runtime's other tools and credential bindings in place of connector tools and connector scopes. **Reserved names** drops `deferredToolDiscovery`: the gateway cannot check the names of tools inside the runtime's process. **Approvals** matches platform tool names alone.
  - The status line's reads-against list cites the MCP protocol upgrade design at revision 9, the security oversight design at revision 57, the message delivery design at revision 23, the runtime protocol design at revision 11, and the connector removal design at revision 2.
