# Client-defined tools

Status: `Draft`, revision 13, 2026-11-23. Programs: [v1](../programs/v1.md).

> **Adapter project banner.** Under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), Lenny serves a REST client API alone, and the protocol mappings of client tools move to the repositories of the MCP, OpenAI Chat Completions, Open Responses, and Anthropic Messages adapters. The owner settled this as O8 of that design on 2026-11-14, in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something", so nothing here is deleted or moved. The runtime contract, the gateway core, and the REST surface still govern Lenny: "CT-1", "CT-2", "CT-4", "CT-5", "CT-8" to "CT-10", and proposal CT-A apart from its Chat Completions and Responses mappings. The adapter-facing sections carry a banner of their own, which names the parts that move: "CT-3", "CT-6", "CT-7", "Worked example", "Specification changes", "Non-specification changes", "Tests", and "Validation owed" and "Steps" in `steps.md`. Decisions T4, T15, O5, O9, and O10 move with those parts, and every other decision still governs Lenny.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external documentation page or in a public SDK repository on 2026-11-09. "Sources" lists the pages. |
| **[unverified]** | An external claim that the pages read on 2026-11-09 do not state. "Validation owed" in `steps.md` lists each one. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

A client-defined tool is a tool that a client declares in its request. The agent may call it, the client executes the call, and the client returns the result in a later request. The Anthropic Messages API (`tools`, `tool_use`, and `tool_result`) and the OpenAI Chat Completions and Responses APIs (`tools`, function calls, and function call outputs) define this pattern **[external]**. Programs written against those APIs commonly hand the model their own tools, so a Lenny runtime served through those surfaces is only partly usable without it.

Lenny has no client tools today **[source]**:

- The runtime contract carries no tool call from a runtime to a client. The stdin `tool_call` and `tool_result` frames of [§28.5.3 Intra-pod](../../spec/28_communication-channels.md#2853-intra-pod) are reserved for adapter-local tools, and a runtime that emits `tool_call` blocks until the matching `tool_result` arrives.
- The OpenAI translators decode a request without rejecting unknown fields, so a client that sends `tools` receives prose with a `200`. `BUILD-GAPS.md` records this as F-15.1.82.
- [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md) revision 2 proposed client tools for its surface as a bridge that keeps the runtime's turn open across HTTP requests ("AM-7"). The owner has since removed open turns from that design and decided that client tools come from a standalone design.

This design adds client tools as one blocking interface for every client that offers tools: a REST client, a provider adapter of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and a parent session of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) (0166, decision 23). The runtime calls a client tool with a `tool_call` frame that names `server: "client"`, the gateway delivers the call to the client as a `tool_call` event, and the call stays open until the client answers or the hold passes. A result that arrives after the call is gone reaches the runtime on its next message from that client.

Revisions 1 to 12 added client tools as a turn-ending exchange, in which the runtime ended its turn with the calls and the client's next request delivered the results as the next turn. Revision 13 replaces that exchange with the blocking call, under the owner's decisions 23, 24, 29, 35, 42, and 48 of 0166, which `decisions.md` lists against the decisions of this change they replace.

## Scope

**Added:**

- The `tool_call` frame naming `server: "client"` with its `group`, the `toolResults` member on the `message` envelope for late results, and the `clientTools` and `toolChoice` members of the envelope's `conversation` member ("CT-1").
- The runtime capability `capabilities.clientTools` ("CT-2").
- Admission of client tool definitions: names, schemas, OpenAI `custom` tools, OpenAI `allowed_tools`, limits, and refusals ("CT-3").
- Validation of every call and every group a runtime makes, with a do-not-retry failure and a per-revision metric ("CT-4").
- Open calls: the `tool_call` event to the client, parallel calls delivered as a group, the session's turn rule with the provenance on every event, the hold under `clientToolHoldSeconds` with withdrawal by `tool_call_withdrawn`, the answer route, late results, and cancellation ("CT-5").
- The request and response mapping for REST and the provider adapters, including streaming and errors ("CT-6").
- Rules for resume, cold continuation, the order of entries in a history the gateway builds, retries, revisions, and derive ("CT-7").
- Interceptor coverage, reserved names, the trust level of results, the audit of every client tool call with a parent's tool calls included, and ownership ("CT-8").
- The rule that a child never receives its parent's client's tools ("CT-9").
- Runtime SDK types, a blocking SDK helper, and conformance cases ("CT-10").

**Changed:**

- The OpenAI translators accept function tools and `allowed_tools` for revisions that declare the capability, and `custom` tools for revisions that also declare `customTools`, and refuse them for every other revision, in place of dropping them.
- [`0144-session-derive`](../0144-session-derive/change.md): a derive copies no open client tool call and no late result.
- [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md): AM-7's bridge is replaced by this design. The `server` and `group` members of [`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) take the place of the `target` and `endOfBatch` members AM-7 proposed for `tool_call`.
- The REST event stream gains the session events `tool_call` and `tool_call_withdrawn`, which 0138 lists in RO-2.
- The [§4.8](../../spec/04_system-components.md#48-gateway-policy-engine) `PreToolResult` phase runs on client results, and `PostAgentOutput` covers call arguments.

**Removed:**

- Nothing in the tree. AM-7's open-turn bridge and its proposal AM-E leave the Messages design, which "Instructions for reviewers of other designs" states. The turn-ending exchange of revisions 1 to 12, with `clientToolCalls` on the end-of-turn `message`, the pending set, `abandonPendingToolCalls`, and the hold rule for inbox deliveries, leaves this design before any of it is built.

**Out of scope:**

- A parent's client's tools for its children. A child never receives them ("CT-9").
- What is specific to a parent that offers tools: `tools` on `session.create` and `session.send`, `tools.result`, `offerTools`, the fenced default, `gateway.delegation.parentTools.enabled`, and `toolCallRateLimit`, which [`0166-delegation-primitives`, "DP-8 Parity with a client"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client) states.
- The classes and delivery of child events, the `tool_call` event to a parent among them, which [`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children) states.
- An MCP-native form. MCP defines no construct by which a client exposes tools to a server ("Current state").
- OpenAI hosted tools and Anthropic server tools.
- Checking a custom tool's input against its grammar. Grammar conformance is the runtime's responsibility, and the client verifies it (O9).
- Warm continuation for Chat Completions as this design wrote it (T4). The conversation fingerprint of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-10-the-provider-adapters) provides it in the OpenAI adapter.
- Whether Lenny's reference runtimes declare the capability in v1, which open decision O22 asks.

## Terms

| Term | Meaning |
| :-- | :-- |
| Client | Whoever sends a session its messages and reads its output: a REST client or a provider adapter for a top-level session, and the parent session for a child ([`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children)). |
| Client tool | A tool that a client declares and executes. |
| Runtime tool | A tool a runtime runs inside its turn: a platform tool, reached through `tool_call` frames ([`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)), or a tool inside the runtime's own process. The second kind includes the tools of an operator service, such as an MCP server, that the runtime's harness reaches through egress ([`0124-connector-removal`](../0124-connector-removal/change.md), "CR-3 External services"). |
| Client tool call (call) | A `tool_call` frame that names `server: "client"`: the runtime's request that its client run one client tool with given arguments. The runtime waits for its `tool_result`. |
| Group | The calls a runtime makes at one model step, marked with one `group` member that carries the group's id and size. The gateway delivers a group's calls to the client in one event. A call without `group` is a group of one. |
| Open call | A call that the gateway has neither answered nor withdrawn. |
| Hold | The time an open call stays open, at most the session's `clientToolHoldSeconds` ("CT-5"). |
| Withdrawal | The gateway's end of an open call without the client's answer, because the hold passed, the runtime's pod was lost, the runtime cancelled the call, or the session closed. The gateway emits `tool_call_withdrawn` and answers the runtime's frame with an error result. |
| Client tool result (result) | The client's answer to one call: `content` as `MessagePart` entries and `isError`. |
| Late result | A result for a withdrawn call. It reaches the runtime in `toolResults` on the next message from the call's client. |
| Answer route | `POST /v1/sessions/{id}/tool-calls/{callId}/result`, by which a REST client or a provider adapter answers a call. A parent answers with `tools.result` ([`0166-delegation-primitives`, "DP-1 Session grant"](../0166-delegation-primitives/change.md#dp-1-session-grant)). |
| Turn rule | The session's `clientToolTurns` setting: `own`, under which the client's tools are callable only in turns the client's own messages started, or `all`, under which they are callable in every turn ("CT-5"). |
| Turn provenance | The `turn` member of every `tool_call` event: the sender of the message that started the turn, and the sessions whose content entered the turn before the call ("CT-5"). |
| Turn | One `message` envelope delivered to a runtime and the runtime's output up to its end-of-turn `message` frame. |
| End-of-turn `message` | The runtime-to-adapter `message` frame that ends a turn, which replaces the specification's `response` frame under [`0135-message-delivery`, "MD-24"](../0135-message-delivery/change.md#md-24-runtime-frames-and-leniency). A `message` ends the turn unless it sets `endOfTurn: false`. |
| Warm turn | A turn delivered to a session that already holds the conversation. |
| Cold turn | A turn that creates a new session and delivers the earlier conversation as structured history before the new input. |
| Runtime id | The `id` a runtime gives a call on its `tool_call` frame. It is unique within the session that issued it. |
| External id | The id the gateway gives a call on the client surface: `call_` and an opaque value on REST and to a parent, `toolu_` and an opaque value on the Messages API, and `call_` and an opaque value on Chat Completions and Responses. It is unique within the conversation. |
| Function tool and custom tool | A function tool takes a JSON object as arguments and declares an input schema. A custom tool is the OpenAI tool type `custom`, which takes a string as input and may declare a grammar. The Anthropic tool field `type: "custom"` marks an ordinary function tool and is unrelated. |
| Steer-declaring runtime | A runtime whose session's effective capabilities include the `steer` delivery intent. The effective capabilities are the chosen revision's capabilities narrowed by the per-tenant capability override ([`0139-runtime-protocol`, "RP-2"](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies), RPR-5). |
| Violation | A call or a group that breaks a rule of "CT-4". |
| Labels | Parts of the design carry the label CT-n, requirements carry the label CTR-n, and decisions that began as open questions carry the label O-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../../spec/05_runtime-registry-and-pool-model.md#51-runtime)). Each runtime the caller may use appears as a model on the OpenAI translators and the Messages API. |
| Session | Lenny's unit of work: one runtime instance bound to a pod, with an owner, a tenant, a transcript, and billing events ([§7](../../spec/07_session-lifecycle.md)). |
| `CH-MSGSOCK` | The intra-pod channel between the adapter and the runtime process, which carries the JSONL frames `message`, `message_part`, `tool_call`, `tool_result`, and others ([§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod), with the frames of "MD-24" of the message delivery design). |
| `MessageEnvelope` and `MessagePart` | The runtime-facing message format and its content parts ([§15.4.1](../../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format", and [§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod), "Internal `MessagePart` Format"). |
| Conversation input | The `conversation` member that [AM-4](../0120-anthropic-messages-api/change.md#am-4-conversation-input) adds to the `message` envelope: history entries with roles, client instructions, parameters, client tools, and the tool choice. A runtime receives it when its revision declares `capabilities.conversationInput`. |
| Adapter-local tools | `read_file`, `write_file`, `list_dir`, and `delete_file`, which the adapter runs inside the pod, and any custom tools a custom adapter lists in the manifest's `adapterLocalTools` ([§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod), "Adapter-Local Tool Reference"). The owner retired them, with `adapterLocalTools`, on 2026-11-12 ([`0139-runtime-protocol`](../0139-runtime-protocol/decisions.md#decisions), D5), so no client tool name collides with one. |
| Built-in adapter single-shot compute model | The §15 rule that a Chat Completions request runs an implicit session that claims a warm pod, runs one turn, and releases the pod within one HTTP call ([§15](../../spec/15_external-api-surface.md#15-external-api-surface)). |
| Open Responses conversation sessions | The §15 rules for holding a Responses session after a turn and for serving a `previous_response_id` continuation warm on the session that served the response or cold in a new session, including the response records on the session row and the warm conditions 1 to 4. |
| Provider-adapter session | An ordinary session that a provider adapter creates. It replaces the term "Held session": Lenny keeps no held-session state and no close-after-turn rule, under the simplified O3 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which the owner answered "yes" on 2026-11-14. The adapter may shorten the session's idle timeout, an operator lengthens it through the pool's idle timeout, a `one_shot` runtime ends its own session, and the adapter routes a follow-up request to the session by its conversation fingerprint ([`0120-anthropic-messages-api`, "Session lifetime"](../0120-anthropic-messages-api/change.md#session-lifetime)). |
| Delivery paths | The named paths of [`0135-message-delivery`, "MD-5"](../0135-message-delivery/change.md#md-5-delivery-paths), which replace §7.2's numbered paths: the direct path writes a message when no turn is open, the queue path holds a `queue` message in the session inbox until the open turn's end-of-turn `message`, and the resume path wakes or resumes a waiting or paused session. |
| Open turn | A turn whose end-of-turn `message` has not arrived, which the gateway tracks from the frames it writes and reads. It replaces the specification's `ready_for_input` signal, which leaves the specification under decision O1 of [`0135-message-delivery`](../0135-message-delivery/decisions.md#decisions). |
| `PreRoute` chain and post-chain point | The interceptor phase that chooses a session's runtime and pool, and the single step after it and after revision selection where every check that depends on the runtime, the revision, or the pool runs ([`0141-runtime-routing`, RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract)). |
| Revision | An immutable, numbered copy of a runtime's versioned fields, `capabilities` among them, chosen per session by the runtime's traffic weights ([`0140-runtime-revisions`, RV-1 and RV-2](../0140-runtime-revisions/change.md#rv-1-revisions)). |
| Per-tenant capability override | The §5.1 mechanism that adjusts a runtime's capabilities for one tenant's sessions, which RV-1 restricts to narrowing ([`0140-runtime-revisions`, "Per-tenant capability override"](../0140-runtime-revisions/change.md#per-tenant-capability-override)). |
| Interceptor phases | The [§4.8](../../spec/04_system-components.md#48-gateway-policy-engine) gateway phases at which deployer interceptors inspect, modify, or reject content. `PreToolResult` receives `{id, content, isError}` for a tool result before the agent sees it, and `PostAgentOutput` receives the agent's output parts before the client sees them. |
| Tool-use approval | A platform tool call held at the gateway until an approver resolves it ([`0142-security-oversight`, SO-5a](../0142-security-oversight/change.md#so-5a-tool-use-approvals)), and the §7.2 approval gate the executor runs today. SO-5a's `approvals.parentTools` also holds a call to a parent's tool before it reaches the parent (0166, decision 31). Approvals for the tools of an operator service are the service's. |
| Parent tools | Tools a parent session offers its child with `tools` on `session.create` and `session.send`, answered with `tools.result`, behind the switches of [`0166-delegation-primitives`, "DP-8 Parity with a client"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client): `gateway.delegation.parentTools.enabled`, the security record, `offerTools`, the fenced default, and `toolCallRateLimit`. A parent is the child's client, so a parent's tools are client tools under this design. |
| Child event | A message the gateway writes into a parent's inbox on a child's behalf, sorted into the event classes `update`, `result`, `question`, `tool_call`, and `status` and delivered by the parent's `wakeOn` ([`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children)). |
| Tool servers and the reference bridge | The `server` member of a `tool_call` frame names a tool server ([`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). RP-4's `toolServers` lists the servers a session offers, and the reference MCP bridge ([`0139-runtime-protocol`, "RP-5"](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge)) serves each to a harness as an MCP server and refreshes its list on `availability_changed`. |
| Qualifying activity | The work that keeps an `idle` session from becoming `dormant`, in [`0146-session-lifecycle`, "SL-6"](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy). An open call counts until its hold passes. |
| Impersonation bearer and F-13.3.26 | A token a `platform-admin` obtains to act as a user. F-13.3.26 is the `BUILD-GAPS.md` finding, recorded in [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md), that session input routes admit one. |
| Replay | `POST /v1/sessions/{id}/replay`, which [§15.1](../../spec/15_external-api-surface.md#151-rest-api) "Session Replay Semantics" defines today and which [`0144-session-derive`](../0144-session-derive/change.md) removes. Under that design a client that wants to repeat a session reads its client messages and re-sends them itself. |
| Derive | `POST /v1/sessions/{id}/derive`, which under [`0144-session-derive`](../0144-session-derive/change.md) starts a new session from a source session's latest state (`from: latest`) or from the state it began with (`from: start`). |
| Delivery intent and `steer` | The per-message delivery intent that [`0135-message-delivery`](../0135-message-delivery/change.md) defines. Each runtime declares the intents it supports, and `steer` is one of them. This design relies only on whether a runtime declares `steer`, and that design states what the intent does. |
| Test tiers | The tiers of the test suite that `.claude/rules/test-coverage.md` defines, by name: `static`, `unit`, `contract`, `integration` on the compose stack, `e2e_kind` on Kind, `load_local` for concurrency, `chaos`, `security`, `conformance` for the runtime adapter battery, and `docs`. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| AM-3, AM-4, AM-5, AM-6, AM-7, and AM-8 | [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md) | The Messages API request mapping, conversation input, continuity by conversation digest, responses and streaming, the client-tool bridge this design replaces, and the Anthropic error envelope. |
| AM-A to AM-E | [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/steps.md#steps) | That design's proposals. AM-A lands conversation input and moves the OpenAI translators onto it. AM-E was the client-tools proposal. |
| RT-2 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | The `PreRoute` contract and the post-chain point. |
| RV-1 and RV-2 | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-1-revisions) | Revisions and their field classes, the per-tenant capability override, and revision selection with its rollout key. |
| MP-5 | [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md#mp-5-intra-pod-servers) | The intra-pod MCP servers under the MCP upgrade. |
| SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | Tool-use approval rules, matched on platform tool names, and `approvals.parentTools`, which holds a call to a parent's tool. |
| CR-1 and CR-3 | [`0124-connector-removal`](../0124-connector-removal/change.md) | The removal of connectors, connector tools, and `deferredToolDiscovery`; and the guidance by which a runtime reaches operator services through egress. |
| SD-1 | [`0144-session-derive`](../0144-session-derive/change.md#sd-1-one-route) | The derive route, and "Reading client messages", which returns the messages a client sent to a session. |
| AR O4 and AR O5 | [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) | Two decisions of the design that removes `type: mcp` runtimes and service mode, settled by the owner on 2026-11-13 in the words "O5 (b), yes to all the others". O4 removes `executionMode` and `conversationContinuity` from `sessionIsolationLevel` and from the hold rule of the Open Responses and Messages adapters. O5 removes `sessionId` from every runtime frame except `session_start`. |
| MD O3 | [`0135-message-delivery`](../0135-message-delivery/decisions.md#decisions) | The owner's decision of 2026-11-09 that a message from another session to a session created on a provider surface is delivered on arrival, with its rule that client tools are offered only in turns a client request caused, which this design recorded as T16. Decision 48 of 0166 replaces that rule with the turn rule of "CT-5". |
| MD-22 and MD-24 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) | Child events with their classes and delivery, the `tool_call` and `tool_call_withdrawn` kinds among them, and the runtime frames with the end-of-turn `message`. |
| RP-3, RP-4, and RP-5 | [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) | Tool calls on frames with `server`, `group`, and `tool_cancel`, the session's tool servers with `availability_changed`, and the reference MCP bridge. |
| SL-6, SL-7, and SL-12 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy) | Qualifying activity and dormancy, the close rule, and the recovery of child events with the resend of an open tool call. |
| DP-1, DP-5, DP-8, DPR-13, and DPR-18 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client) | The platform operations with `session.create`, `session.send`, and `tools.result`, the reference tool `answer_tool_call`, parent tools and their switches, the requirement on this design that a client tool call blocks and that a late result reaches the runtime, and the requirement that a parent's tools reach a child only when every switch allows them. Its decisions 23, 24, 29, 31, 35, 42, and 48 are cited as "0166, decision N". |

## High-level design

A client that uses OpenAI's or Anthropic's API with its own tools sends the tool definitions with each request. When the model wants one of them, the response stops and names the call, the client runs the tool, and the next request carries the result. Lenny serves the same pattern to every client that offers tools, a REST client, a provider adapter, and a parent session, through one interface. The runtime receives the client's tools on each message. When its model calls one, the runtime writes a `tool_call` frame that names `server: "client"` and waits for the `tool_result`, as it waits for a platform tool. The gateway checks the call against the client's declarations and delivers it to the client as a `tool_call` event: on a REST client's event stream, and to a parent as a child event. The client runs the tool and answers, and the gateway returns the answer to the runtime as the call's `tool_result`. The turn stays open while the call waits.

A call waits for at most the session's `clientToolHoldSeconds`, 300 seconds by default. When the hold passes, or when the runtime's pod is lost, the gateway withdraws the call: the runtime receives an error result, the client receives `tool_call_withdrawn`, and the session can become `dormant`. A result the client sends afterwards is a late result, which reaches the runtime in `toolResults` on the client's next message, so the runtime continues from it.

A provider adapter returns a group of calls to its caller as the provider's tool use in one response, and posts the results when the caller's next request brings them. A caller that never returns lets the calls' hold pass.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Protocol members (CT-1).** A `tool_call` frame names `server: "client"` and carries `group` for parallel calls. The `message` envelope gains `toolResults` for late results, and the conversation input carries `clientTools` and `toolChoice` on every message that declares them. History entries carry earlier calls and results. No frame type, channel, or link is added.

**Capability (CT-2).** A revision declares `capabilities.clientTools`. Lenny refuses client tools on revisions that do not declare it. The reference bridge serves the client's tools to a harness as the tool server `client`.

**Admission (CT-3).** The gateway checks each tool definition when the message arrives: name rules, reserved names, limits, and a schema that compiles with every external reference refused. An OpenAI `custom` tool is admitted for a revision that declares `customTools`, and its grammar is passed to the runtime unchecked. An OpenAI `allowed_tools` choice becomes a list of allowed names in the tool choice.

**Call validation (CT-4).** The gateway checks each call against the declared names, the tool's input schema or, for a custom tool, that the input is a string, and each group against the tool choice with its allowed names and the parallel limit. A violation answers the group's calls with errors, fails the turn with a do-not-retry error, and counts on a per-revision metric. The gateway never repairs a call.

**Open calls (CT-5).** The gateway delivers each group in one `tool_call` event that carries the turn's provenance. A client declares for the session whether its tools are callable only in turns its own messages started, the default, or in every turn, and a provider adapter always declares the first (0166, decision 48). An open call counts as the session's activity until its hold passes. The client answers through the answer route, or a parent with `tools.result`, and a late result waits for the client's next message.

**Surfaces (CT-6).** REST carries the declarations on a message, the `tool_call` event on the session's event stream, and the answer route. Each provider adapter maps a group to its provider's tool use and the caller's results to the answer route, in plain and streaming responses.

**Continuation (CT-7).** A pod loss withdraws the open calls, and their late results reach the restored runtime. A cold turn delivers earlier calls and results as history. A derive copies no open call. A cold turn of an existing conversation can land on another revision, and a revision without the capability refuses it before a pod is claimed.

**Security (CT-8).** A result has the client's trust level. Every result passes `PreToolResult`, every call's arguments pass `PostAgentOutput`, reserved names are refused, and audit records carry ids, names, the client, the provenance, and the outcome, without arguments or content, for every client tool call, a parent's included.

**Children (CT-9).** A child never receives its parent's client's tools. The only tools a child can call as client tools are the tools its parent offers it under 0166.

**SDKs and conformance (CT-10).** The runtime SDKs model the new members and add a blocking helper for a client tool call, and the conformance suite tests a declaring runtime with live answers, withdrawals, late results, and cold history.

```
 client ──message: clientTools, toolChoice, input──> gateway (REST, adapter over REST, or parent's session.send)
                                                   │
                                                   ▼
                                CT-3 admission: names, reserved names, limits, schema compile
                                                   │
                                                   ▼
                                post-chain point: revision declares capabilities.clientTools?
                                   │ no ──> refused before any pod is claimed
                                   ▼ yes
                     message envelope: input + conversation {clientTools, toolChoice, entries}
                                                   │
                                                   ▼
             runtime turn ──tool_call {server: "client", group}──> CT-4 validation, turn rule
                    ▲                              │                    │ violation ──> error results,
                    │                              │                    │               turn failed, metric
                    │                              ▼
                    │            tool_call event (group, turn provenance) ──> client
                    │                              │
                    │        answer route or tools.result before the hold passes?
                    │             │ yes                          │ no
                    └── tool_result ◄─┘                          ▼
                                             tool_call_withdrawn; runtime gets an error result
                                                                 │
                                       late result ──> toolResults on the client's next message
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| CTR-1 | A client tool call reaches a client only from a session whose effective capabilities include `capabilities.clientTools`, only for a tool that the client declared, and only in a turn that the client's turn rule admits. |
| CTR-2 | Every call the gateway delivers names a declared tool, has arguments that validate against that tool's input schema or, for a custom tool, a string input, and belongs to a group that obeys the tool choice, its allowed names, and its parallel limit. The gateway delivers no call of a group that breaks any of these, and it never alters a call. |
| CTR-3 | A call is answered only by the client of the session that made it, through the answer route or, for a parent, `tools.result`, and the runtime receives at most one result for each call. |
| CTR-4 | A runtime calls a tool its client offers, whether the client is a REST client, a provider adapter, or a parent, as a blocking call that stays open until the client answers or the call is withdrawn, at the latest when `clientToolHoldSeconds` passes. A result that arrives after the call is withdrawn reaches the runtime in `toolResults` on the next message from that client. This is DPR-13 of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#requirements). |
| CTR-5 | Open calls are gateway records that survive replica failover. A pod loss withdraws the session's open calls, and their late results reach the restored runtime. |
| CTR-6 | For every client, every result passes `PreToolResult` and every call's arguments or custom tool input pass `PostAgentOutput` before they cross the boundary. |
| CTR-7 | A child never receives its parent's client's tools. A call from a child reaches its parent only for a tool the parent offered it, and a call to any other tool is a violation. |
| CTR-8 | Calls, results, and late results are erased with the session under [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces), and no audit record of a client tool call carries arguments, custom input, or result content. |
| CTR-9 | Client tools work for every runtime that declares them, because the interface uses `CH-MSGSOCK` frames alone and needs no other capability. |
| CTR-10 | Under the turn rule `own`, the default, no envelope that opens a turn another sender started carries `conversation.clientTools` or `conversation.toolChoice`, and a call in such a turn fails validation. A provider adapter's session always has the turn rule `own`. Every `tool_call` event carries the turn's provenance. |

## Current state

### Lenny

**Runtime contract [source].** The `CH-MSGSOCK` card of §28.5.3 lists the adapter-to-runtime messages as `session_start`, `message`, `tool_result`, `heartbeat`, `session_end`, and `shutdown`, and the runtime-to-adapter messages as `session_started`, `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`. Its "Tool access by level" table limits stdin `tool_call` and `tool_result` to adapter-local tools at every level, and Standard and Full runtimes reach platform and connector tools through their MCP client. "Delivery semantics" states that a runtime that emits `tool_call` keeps reading stdin until the matching `tool_result`, and "Correlation" states that a `tool_result` with an unknown `id` is dropped and logged as a protocol error. The `response` frame carries `output` and an optional `error`, and no member names work the runtime waits on. The "Message schemas" preamble requires runtimes to ignore unrecognized fields and unrecognized frame types.

**Envelope [source].** The `MessageEnvelope` of §15.4.1 has `schemaVersion`, `type`, `id`, `from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `sessionId`, and `input`. It has no member for tools or results. Decision O5 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) removes `sessionId` from the envelope, as from every frame other than `session_start`. `from.kind` is `client`, `agent`, `system`, or `external`, and the adapter overwrites any runtime-supplied `from`.

**Delivery [source].** §7.2 "Message delivery routing" writes a message to a runtime that reports `ready_for_input` (path 2) and buffers it in the session inbox while the runtime is busy (path 5). Its "Delivery of held messages" paragraph delivers a session's held messages before each message delivered under path 2. `input_required` is a sub-state of `running` in which the runtime is blocked inside `lenny/request_input`.

**Executor [source].** `Executor.Send` (`pkg/gateway/core/session/executor/executor.go`) is synchronous and returns the turn's output parts. `PodExecutor.handleFrame` (`podstream.go` in the same package) sends a `tool_call` that carries `approvalRequired` to the approval gate, completes the turn on a `response`, and discards every other frame. `ResponseEnvelope` (`pkg/runtimeexec/subprocess.go`) decodes `type`, `text`, and `output`, and `IngestResponse` (`pkg/runtimeexec/ingest.go`) reads nothing else. `pkg/runtimeexec` holds the executor's message model and the subprocess executor, and the adapter binary also builds it.

**Adapter [source].** The Attach loop in `pkg/adapter/attach.go` answers an adapter-local `tool_call` through `HandleToolCall` (`toolcall.go`) and relays every other frame to the gateway unchanged. `attachRecvLoop` writes every envelope the gateway sends to the runtime's stdin.

**Translators [source].** `OpenAIChatCompletionsRequest` models `model`, `messages`, `stream`, and `user`, and `OpenResponsesRequest` models `model`, `input`, `previous_response_id`, `stream`, and `user`. Neither decoder rejects unknown fields, so `tools`, `tool_choice`, and `parallel_tool_calls` are dropped with a `200` (F-15.1.82). `OpenAIChatHandler` calls `Executor.Send` directly (`openai_chat.go`), and no file in `pkg/gateway/environment/translator` invokes an interceptor phase.

**Response records [source].** The session row type `sessionrow.Session` carries `Responses []ConversationResponse` and `ContinuationParentID` (`pkg/session/sessionrow/sessionrow.go`), which the store in `pkg/session/sessionstore` persists and which implement the §15 "Open Responses conversation sessions" records.

**Interceptors [source].** §4.8 defines `PreToolResult` with the payload `{ id, content: MessagePart[], isError? }` and `PostAgentOutput` with a `MessagePart[]` payload. The gateway runs `PreToolResult` in `buildPreToolResultInterceptor` (`pkg/gateway/platformtools/mcptools/mcptools.go`), on MCP tool results only, and runs `PostAgentOutput` on the REST message path through `runPostAgentOutput` (`pkg/gateway/core/session/sessionserver/route.go`).

**Schema compiler [source].** The gateway compiles `runtimeOptionsSchema` with `github.com/santhosh-tekuri/jsonschema/v5` v5.3.1 in `validateAgainstSchema` (`pkg/gateway/core/session/sessionserver/envelope.go`), with a default compiler. That library's package-level `Loaders` map resolves `file` URLs (`loader.go` in the module), and each `Compiler` has a `LoadURL` field that replaces it.

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

## Detailed design

### CT-1 Protocol members

#### `conversation.clientTools` and `conversation.toolChoice`

The conversation input of AM-4 carries the client's tools and tool choice on every envelope of a turn that has them **[design]**. The example envelope also carries a late result in `toolResults`:

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

- `clientTools` entries carry `type`, `name`, and `description`. `type` is `function`, the default, or `custom` **[design, O9]**. A function entry carries `inputSchema` and `strict`. A custom entry carries `format`, which is `{"type":"text"}`, the default, or `{"type":"grammar","syntax","definition"}` with `syntax` `lark` or `regex`, and the gateway passes it to the runtime as the client sent it. A client may change its tools between messages, so a runtime reads the list from each envelope and keeps none from an earlier turn.
- `toolChoice` carries `type` (`auto`, `any`, `tool`, or `none`), `name` when `type` is `tool`, `parallel`, which is `false` when the client limited each model step to at most one call, and `allowedNames` **[design, O9]**. `allowedNames` is present when the client restricted the calls to a subset of `clientTools`, and lists that subset. The runtime presents every tool in `clientTools` to its model and calls only tools in `allowedNames`.
- A turn with no client tools carries neither member.
- Where the declarations come from **[design]**. A REST client and a provider adapter declare them on a message in `conversation`, under R15 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). A parent declares them with `tools` on `session.create`, for every turn of the child, and with `tools` on `session.send`, for the turn that message opens ([`0166-delegation-primitives`, "DP-1 Session grant"](../0166-delegation-primitives/change.md#dp-1-session-grant)). The gateway writes either form into `conversation.clientTools`, so a runtime reads one member whoever its client is.
- Which turns carry them follows the session's turn rule ("CT-5", "Turn rule"). Under `own`, the default, an envelope that opens a turn another sender started carries neither member, whatever tools the client's last message declared. Under `all`, such an envelope carries the tools and tool choice of the client's most recent message. A client message steered into an open turn on a steer-declaring runtime carries its own members, and the runtime may call those tools from the point it consumes the message.

#### The `tool_call` frame naming `server: "client"`

A runtime calls a client tool with the `tool_call` frame of [`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames), naming the tool server `client` **[design, 0166 decision 23]**:

```json
{ "type": "tool_call", "id": "tc_7", "server": "client", "name": "lookup_order",
  "arguments": { "orderId": "A-1009" }, "group": { "id": "g_3", "size": 2 } }
{ "type": "tool_call", "id": "tc_8", "server": "client", "name": "lookup_order",
  "arguments": { "orderId": "A-1010" }, "group": { "id": "g_3", "size": 2 } }
```

- `id` is the runtime id, unique within the session. A call to a function tool carries `arguments` as a JSON object. A call to a custom tool carries `input` as a string **[design, O9]**.
- `group` marks the calls the runtime makes at one model step, with an id the runtime chooses and the number of calls in the step. A call without `group` is a group of one. The reference bridge marks the calls a harness makes in one step with one group, and a runtime that runs its own model loop marks the tool uses of one model response.
- The runtime waits for each call's `tool_result` on the connection that carried the call, as "Delivery semantics" of the `CH-MSGSOCK` card in [§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod) states for any `tool_call`. The answer carries the client's `content` and `isError`. A withdrawn call is answered with `isError: true` and `error.code: "CLIENT_TOOL_CALL_WITHDRAWN"` with the reason, and a refused call with `error.code: "CLIENT_TOOL_CALL_INVALID"` with the reason ("CT-4").
- A runtime that stops waiting writes RP-3's `tool_cancel` for the call ("CT-5", "Cancellation and close").
- The adapter forwards a `client` call to the gateway as it forwards a `platform` call, and RP-4's `toolServers` lists `client` when the session's client offers tools. 0139 adds the `client` value, `group`, and the `toolServers` entry to its frames.

#### `toolResults`

`toolResults` on the `message` envelope carries late results **[design, 0166 decision 24]**. Each entry has `callId`, `content` as `MessagePart` entries, and `isError`. `input` holds what the client sent with the message, and is empty when the client sent the message only to deliver late results ("CT-5", "Late results"). On a warm turn `callId` is the runtime id. On a cold turn it is the external id, which the runtime treats as an opaque string ("CT-7").

#### History entries

On a cold turn, `conversation.entries`, the ordered entries of AM-4 with `role`, `content`, and `new: true` on the entries of the new input, holds earlier assistant entries with their calls, `{"role":"assistant","content":[...],"clientToolCalls":[...]}`, and earlier user entries with their results, `{"role":"user","content":[...],"toolResults":[...]}` **[design]**. `clientToolCalls` appears only on assistant entries and `toolResults` only on user entries. Each entry of `clientToolCalls` carries `id`, `name`, and `arguments` or `input`, as a `tool_call` frame does. The member keeps the name it had when revisions 1 to 12 also used it on the end-of-turn `message`, which no longer carries it, and [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md#am-4-conversation-input) and R15 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) use it. The final round's results arrive in the envelope's own `toolResults`, so a late result and a cold turn present the newest results in the same member. Under the owner's O3 decision on the Messages design, the conversation input carries ordered entries with the new input marked, and the marked entry's results are the envelope's `toolResults`.

#### Why a blocking call

A blocking call reaches every harness that runs its tool loop inside one turn: the reference bridge serves the client's tools as the `client` tool server, and the harness calls them as it calls any MCP tool. A runtime that runs its own model loop puts the definitions in its model request and writes a `tool_call` for each call its model makes. The turn-ending exchange of revisions 1 to 12 required a runtime to stop its loop at a call and resume it from a later message, which no reference harness does (0166, decision 23). The frame keeps its meaning under "Delivery semantics" and "Correlation": a `tool_call` blocks inside a turn until its `tool_result`, and the hold bounds the wait ("CT-5").

### CT-2 Capability

```yaml
capabilities:
  conversationInput: true
  clientTools:
    supported: true
    customTools: true
    reservedNames: [search_repo, run_tests]
```

**[design, T3, O1, O9, 0166 decision 23]**

- `supported: true` declares that the runtime reads `conversation.clientTools` and `toolChoice`, calls a client tool with a `tool_call` frame naming `server: "client"` and waits for its result, marks the calls of one model step with one `group`, and continues from a late result in `toolResults` warm and from history cold. Registration refuses `supported: true` without `conversationInput: true`.
- `customTools: true` declares that the runtime also accepts custom tools, calls them with frames that carry `input`, and honours a custom tool's `format`. It requires `supported: true`, and its default is `false`. The gateway does not check the grammar, so a declaring runtime is responsible for input that conforms to it (O9).
- `reservedNames` lists the names of the runtime's own model-visible tools. The gateway refuses a client tool whose name is in the list ("CT-3").
- The field is versioned and gateway-read under RV-1, so the chosen revision declares it. A session uses its effective capabilities, the chosen revision's capabilities narrowed by the per-tenant capability override ([`0139-runtime-protocol`, "RP-2"](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies), RPR-5), and every check of this design that reads `supported`, `customTools`, or the `steer` intent reads the effective capabilities. The per-tenant capability override may clear `supported`, may clear `customTools`, and may append to `reservedNames` (O7).
- A derived runtime may not set it, under the §5.1 rule for `capabilities`.
- It is available to every runtime (CTR-9). A runtime whose harness reaches tools through the reference bridge declares it when the harness refreshes its tool list on the bridge's `client` server, because the bridge serves the client's tools there and refreshes the list on `availability_changed` (RP-4 and RP-5). A runtime author who builds a raw model loop declares it and writes the frames. `capabilities.interaction` is unconstrained on the surfaces with a cold path. O4 states the REST rule: client tools on a REST session whose revision declares `capabilities.interaction: one_shot` or `capabilities.injection.supported: false` are refused at creation with `422 CLIENT_TOOLS_UNSUPPORTED`, naming the missing capability. Open decision O13 asks whether O4 is withdrawn, because a blocking call needs no second message unless a call is withdrawn.
- For a parent's tools, the child's revision is the runtime that declares the capability. A child whose revision lacks it receives no parent tools, which is the revision switch of 0166 "DP-8 Parity with a client".
- Whether Lenny's reference runtimes in §26 declare it in v1 is open decision O22. T3 stated that none does, and revision 12's "Limits" gave the reason that the turn-ending exchange needed a harness that parks a call between turns. The blocking call removes that reason.

A request with client tools for a session whose effective capabilities lack the capability is refused at the post-chain point before any pod is claimed: `422 CLIENT_TOOLS_UNSUPPORTED` on REST, and `400 invalid_request_error` on the Chat Completions, Responses, and Messages surfaces, each naming the runtime and revision. A parent's `session.create` or `session.send` with `tools` for a child whose revision lacks the capability is refused with the same code. A request that declares a custom tool on a revision that does not declare `customTools` is refused the same way, naming `customTools`. A request with an empty `tools` array, or with `tool_choice` `none` and no tools, has no client tools.

### CT-3 Admission

> **Adapter-facing section.** **Names.**, **Reserved names.**, **Schemas.**, **Custom tools [O9].**, and **Limits.** still govern Lenny for REST. The Chat Completions name limit of **Names.**, the OpenAI mapping of **Allowed tools [O9].** (T15), the provider forms of **Tool forms.**, and **Streamed custom tools [O10].** move to the OpenAI and Anthropic adapters. The parts that move govern the adapters rather than Lenny, stay here unchanged until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

The surface decodes the tool definitions before session creation or warm dispatch, and the gateway checks them **[design]**. The same checks run on the `tools` a parent declares on `session.create` and `session.send`:

- **Names.** A name matches `^[a-zA-Z0-9_-]{1,128}$`, and Chat Completions applies its own limit of 64 characters. Names are unique within the request.
- **Reserved names.** A name beginning with `lenny` in any letter case, a name in the revision's `clientTools.reservedNames`, or the name of a platform tool the gateway offers a runtime is refused with `400 CLIENT_TOOL_NAME_RESERVED` on REST and `400 invalid_request_error` elsewhere. The platform tools carry no `lenny/` prefix under [`0135-message-delivery`, "MD-23"](../0135-message-delivery/change.md#md-23-tool-and-frame-names), so the prefix rule alone no longer covers them: the gateway reserves the names of the platform tools it serves, such as `send_update` and `ask_question`, and the names of the reference tools of [`0166-delegation-primitives`, "DP-5 Reference extension"](../0166-delegation-primitives/change.md#dp-5-reference-extension), such as `start_session`, `send_message`, `await_sessions`, and `answer_tool_call`, which a harness behind the reference bridge sees beside the client's tools. The reserved-name check runs at the post-chain point, because `reservedNames` belongs to the chosen revision, and on every warm turn against the session's revision.
- **Schemas.** A function tool's `inputSchema` is a JSON Schema whose root has `type: "object"`. The gateway compiles each schema with a compiler whose `LoadURL` refuses every URL, so a `$ref` resolves only inside the schema document, and with the 2020-12 draft as the default. A schema that does not compile is refused with `400 CLIENT_TOOL_DEFINITION_INVALID` naming the tool. The compiled schemas live for the turn and are not stored.
- **Custom tools [O9].** A custom tool's `format` is absent, `{"type":"text"}`, or a grammar whose `syntax` is `lark` or `regex` and whose `definition` is a string. Any other `format` is refused with `400 CLIENT_TOOL_DEFINITION_INVALID` naming the tool. The gateway neither parses nor compiles the grammar, and passes `format` to the runtime unchanged. A custom tool on a revision that does not declare `customTools` is refused at the post-chain point as "CT-2" states.
- **Allowed tools [O9].** An OpenAI `allowed_tools` tool choice becomes `toolChoice.allowedNames`, with `mode` `auto` as `type: auto` and `mode` `required` as `type: any` (T15). Each entry of its `tools` list must name a function or custom tool the request declares. An entry that names an undeclared tool or a hosted tool type is refused with `400 invalid_request_error` naming the entry. On REST, `allowedNames` accompanies `type` `auto` or `any` alone, and every name in it must be declared.
- **Tool forms.** Hosted tool types, Anthropic server tool types, and the `mcp` and hosted-tool `tool_choice` forms are refused with `400 invalid_request_error` naming the form.
- **Streamed custom tools [O10].** A Chat Completions request with `stream: true` that declares a custom tool is refused with `400 invalid_request_error` naming the combination. A plain Chat Completions request and every Responses request may declare custom tools.
- **Limits.** `clientTools.maxTools` (default 128), `clientTools.maxSchemaBytes` per request (default 256 KiB), which counts input schemas and grammar definitions together, `clientTools.maxOpenCalls` per session (default 32), which bounds the calls open at once and therefore a group's size, and `clientTools.maxArgumentsBytes` per call (default 256 KiB), which bounds a function call's serialized arguments and a custom call's input, are Helm values and gateway flags, each operator-tunable. A request over a limit is refused with `413` or `400` as the surface pairs them. Result content follows the §28.5.3 part size rule: a part up to 64 KB is inline, a larger part up to 50 MB is staged as a `lenny-blob://` reference, and a larger one is refused with `413 MESSAGEPART_TOO_LARGE`.

### CT-4 Call validation

The gateway checks each call when its `tool_call` frame arrives, and each group when its last call arrives, before it delivers any call of the group **[design, T6]**:

| Rule | Violation reason |
| :-- | :-- |
| The turn has client tools: the session's turn rule admits the turn ("CT-5", "Turn rule") and the turn's envelope or a client message the turn consumed declared tools. A call from a child to a tool its parent did not offer, and every call in a turn the turn rule excludes, breaks this rule. | `no_tools_declared` |
| Each `name` is one of the turn's `clientTools`. | `undeclared_tool` |
| A call to a function tool carries `arguments` and no `input`, and a call to a custom tool carries `input` and no `arguments`. | `input_form` |
| A function call's `arguments` is a JSON object that validates against that tool's compiled `inputSchema`, whatever the tool's `strict` value. | `schema_mismatch` |
| A custom call's `input` is a string. The gateway does not check it against the tool's grammar. | `input_form` |
| `id` is unused by the session's open calls and by the late results that wait for delivery. | `duplicate_id` |
| Every call of a group carries the same `size`, and a group receives no more calls than its `size`. | `group_mismatch` |
| `toolChoice.type` `none`: no calls. | `tool_choice_none` |
| `toolChoice.type` `tool`: every call names `toolChoice.name`. | `tool_choice_tool` |
| `toolChoice.allowedNames` present: every call names a tool in it. | `tool_choice_allowed` |
| `toolChoice.parallel` `false`: every group has one call. | `parallel_limit` |
| The session's open calls and each call's size are within `maxOpenCalls` and `maxArgumentsBytes`. | `limit_exceeded` |
| `toolChoice.type` `any` or `tool`: the turn makes at least one call before its end-of-turn `message`. The gateway checks this rule at the end of the turn. | `tool_choice_any` or `tool_choice_tool` |

A violation answers every call of the group that the runtime has written with a `tool_result` carrying `isError: true` and `error.code: "CLIENT_TOOL_CALL_INVALID"`, whose details name the reason, the call id, and for `schema_mismatch` the schema validator's first error path, and fails the turn with the error `CLIENT_TOOL_CALL_INVALID` (category `PERMANENT`, `retryable: false`) (T6). Open decision O17 asks whether the turn fails or continues after the error results. The gateway never drops, reorders, coerces, or rewrites a call, and it delivers no call of a violating group, so a client never receives part of one. A group whose calls do not all arrive is withdrawn when the hold of its first call passes ("CT-5").

Grammar conformance of a custom call's `input` is the runtime's responsibility, and the client verifies it before it runs the tool **[design, O9]**. The gateway has no Lark or regex grammar engine, and a check would add one to the dispatch path of every custom call. A call whose `input` breaks the grammar passes the checks of "CT-4", and the client receives it.

The client receives:

| Surface | Failure |
| :-- | :-- |
| REST | `CLIENT_TOOL_CALL_INVALID` in the `error` of the turn resource of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) R3, and an `error` session event. No route returns the code as an HTTP status. The session continues under its ordinary rules for a failed turn. |
| A parent | The child's failed turn as a child event under [`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children). |
| Chat Completions and Responses | `502` in the OpenAI error envelope with `type: "server_error"`, `code: "client_tool_call_invalid"`, and the header `x-should-retry: false`. |
| Messages API | `500 api_error` in the Anthropic error envelope with the Lenny code in `error.details.error_code`, under the owner's decision on AM-8, and `x-should-retry: false`. |
| Streaming, after the stream began | The surface's error event (`error` on the Messages API, and `response.failed` on Responses), or on Chat Completions the mid-stream error the translator's stream writer already emits, and no call of the group is streamed. |

Output parts streamed before the violation stay with the client. On the translators, the session then ends with the `failed` disposition, as §15 states for a failed dispatch.

The counter `lenny_client_tool_call_invalid_total{runtime, runtime_revision, reason}` counts violations, using the `runtime_revision` label RV-2 adds to session metrics. A rollout operator reads it per revision to stop a rollout whose new binary breaks the contract. The audit record of the failed call names the reason and the call ids ("CT-8").

Validation is a gateway check over data the gateway already holds for the turn, so it adds no round trip. Its cost is one schema validation per call, bounded by `maxOpenCalls` and `maxArgumentsBytes`.

### CT-5 Open calls

#### Delivery

When a group passes "CT-4", the gateway records each call as an open call of the session, mints an external id for it, and delivers the group to the client in one `tool_call` event **[design, 0166 decision 23]**:

```json
{
  "kind": "tool_call",
  "group": "g_3",
  "calls": [
    { "callId": "call_8Kq...", "name": "lookup_order", "arguments": { "orderId": "A-1009" } },
    { "callId": "call_8Kr...", "name": "lookup_order", "arguments": { "orderId": "A-1010" } }
  ],
  "holdUntil": "2026-11-23T10:05:00Z",
  "turn": { "startedBy": { "kind": "client", "id": "client_gateway" }, "contentFrom": [] }
}
```

- A REST client and a provider adapter receive the event on the session's event stream, and a webhook subscriber receives it as the webhook kind that 0138 lists in RO-2.
- A parent receives it as a child event of the class `tool_call`, whose sender, content checks, place in the parent's `wakeOn`, and delivery [`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children) states. Before the event reaches the parent, the call counts against the child's `toolCallRateLimit` and passes the parent-tool switches of [`0166-delegation-primitives`, "DP-8 Parity with a client"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client), and a rule of SO-5a's `approvals.parentTools` holds it for a person's approval ([`0142-security-oversight`, "SO-5a"](../0142-security-oversight/change.md#so-5a-tool-use-approvals)).
- The open-call record holds the runtime id, the external id, the group, the name, the arguments or custom input, the client, the turn provenance, and the hold deadline. It is session data in the session store, so a replica that adopts the session after a failover delivers the answer to the runtime (CTR-5).
- `GET /v1/sessions/{id}` reports `openToolCalls` with the external id, name, arguments or input, group, and hold deadline of each open call, so a client that reconnects finds the calls it has not answered. The event stream also replays the event from `Last-Event-ID` under R1 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).
- Delivery is at least once: a stream replay or a resend under SL-12 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-12-recovery-of-child-events) can deliver the same event again. A client keys the side effects of a tool on the external id, and the answer route accepts one answer for each call.

#### Turn rule

A client declares for the session whether its tools are callable only in turns its own messages started, or in every turn **[design, 0166 decision 48]**. The member is `clientToolTurns`, `own` by default or `all`:

- A REST client sets it on the request that creates the session. A parent sets it on `session.create` beside `tools`. A provider adapter always sets `own`, because its caller receives calls only in the response to its own request.
- Under `own`, the client's tools are callable only in a turn that a message from the client started. An envelope that opens a turn another sender started, such as another session's message, a child event, a system message, or an external message, carries no `conversation.clientTools` and no `conversation.toolChoice`, and a call in that turn fails validation with `no_tools_declared` ("CT-4"). A turn the client's message started stays the client's turn when another sender's message is steered into it, and its provenance names that sender.
- Under `all`, the client's tools are callable in every turn. An envelope that opens a turn another sender started carries the tools and tool choice of the client's most recent message, and the gateway delivers the calls of that turn with their provenance.
- Every `tool_call` event carries the turn's provenance in `turn`: `startedBy`, the `from` of the message that started the turn (`client`, `agent` with the sender's session id, `system`, or `external`), and `contentFrom`, the ids of the other sessions whose messages, child events, or file parts the runtime received in the turn before the call. The client runs, confirms, or refuses each call by its own policy with that information.
- A child never receives its parent's client's tools ("CT-9"), so a parent's turn rule governs only the tools the parent itself offers.

#### Hold

A call waits for at most the session's `clientToolHoldSeconds` **[design, 0166 decision 24]**:

- The default is 300 seconds. A client sets the value on the request that creates the session, and a parent on `session.create`, at most the tenant's ceiling. A tenant administrator sets the ceiling, at most the platform's cap, the Helm value `clientTools.maxHoldSeconds` with the gateway flag `--client-tools-max-hold-seconds`, which the operator tunes. When the tenant's ceiling is lower than 300 seconds, the ceiling is the default.
- An open call counts as the session's qualifying activity under [`0146-session-lifecycle`, "SL-6"](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy) until its hold passes, so the pod stays while the client works.
- When the hold passes, the gateway withdraws the call: it answers the runtime's frame with `isError: true` and `error.code: "CLIENT_TOOL_CALL_WITHDRAWN"` with the reason `hold_expired`, and sends the client a `tool_call_withdrawn` event naming the external ids and the reason. The runtime continues its turn from the error result. Once the session has no qualifying activity, it becomes `idle` and then `dormant` under SL-6.
- A group whose calls do not all arrive is withdrawn with the reason `group_incomplete` when the hold of its first call passes. The gateway delivers none of its calls.

#### Answers

- A REST client, or a provider adapter for its caller, answers a call with `POST /v1/sessions/{id}/tool-calls/{callId}/result`, whose body carries `content` as `MessagePart` entries and `isError`, and which session ownership governs. 0138 lists the route in RO-2. A parent answers with `tools.result`, through its own code, the SDK library's `ToolSet`, or the reference tool `answer_tool_call` ([`0166-delegation-primitives`, "DP-5 Reference extension"](../0166-delegation-primitives/change.md#dp-5-reference-extension)).
- An answer to an open call returns `answered`. The gateway runs `PreToolResult` on the result ("CT-8") and writes it to the runtime as the call's `tool_result`. A repeated answer returns the first answer's outcome and reaches the runtime once.
- An answer to a withdrawn call returns `late`, and the result becomes a late result.
- An answer naming no call of the session is refused with `404 CLIENT_TOOL_CALL_NOT_FOUND`. An answer from anyone other than the session's client is refused with `403 CLIENT_TOOL_RESULTS_FORBIDDEN`, so another session's message can never answer a call.
- A client that chooses not to run a tool answers with `isError: true` and a brief explanation, as the provider APIs require of their callers.
- The answers of a group arrive in any order, and each one returns to its own call.

#### Late results

A late result is a result for a withdrawn call **[design, 0166 decision 24, DPR-13]**:

- The gateway keeps the session's late results in the session store, in the order they were answered, after `PreToolResult` has run on each. A late result keeps its call's place under `maxOpenCalls` until it is delivered.
- The next message from the call's client to the session carries the late results in its envelope's `toolResults`, under that message's delivery path of [MD-5](../0135-message-delivery/change.md#md-5-delivery-paths). A turn that another sender started carries none.
- A client with nothing else to send sends a message with no input parts, which the gateway accepts only while late results wait for that client.
- The runtime continues from a late result under the obligations of "CT-7", warm with the runtime ids and cold with the external ids in history, so a runtime that runs its own model loop continues from it and no result the client sends is lost (CTR-4).

#### Cancellation and close

- A runtime that stops waiting for a call writes RP-3's `tool_cancel`. The adapter answers the frame with `CANCELLED` as RP-3 states, and the gateway withdraws the call with the reason `cancelled`. Open decision O14 asks whether an answer that arrives afterwards becomes a late result or is dropped.
- An interrupt of the turn, R19 of 0138 or a parent's `session.interrupt`, withdraws the turn's open calls with the reason `interrupted`.
- A pod loss withdraws the session's open calls with the reason `pod_lost` ("CT-7").
- A close withdraws the session's open calls with the reason `closed` and discards its late results. A close while a call is open records `cancelled` under the close rule of [`0146-session-lifecycle`, "SL-7"](../0146-session-lifecycle/change.md) (its L21 and SLR-8), which reads the open calls.
- A parent that ends answers its child's open calls with an error, and a child that leaves its parent has its open calls to the parent answered with `CLIENT_CHANGED` ([`0166-delegation-primitives`, "DP-8 Parity with a client"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client) and decision 33).

#### Messages while a call is open

The turn stays open while a call waits, so a message that arrives meanwhile follows the paths of [MD-5](../0135-message-delivery/change.md#md-5-delivery-paths) for an open turn **[design, T5]**. The queue path holds it for a runtime that does not declare `steer`, and a steer-declaring runtime receives it. A client's message never answers a call. A steer-declaring runtime that receives a message while its model history holds an unanswered call places the message after that call's result, so the model never sees a call followed by anything other than its result. The gateway carries the matching obligation for every history it builds ("CT-7", "Order of history entries").

### CT-6 Surface mapping

> **Adapter-facing section.** The REST rows of "Requests and results" and "Responses with calls", "Provider adapters", and the REST envelope and codes of "Errors" still govern Lenny. The Chat Completions, Responses, Messages API, and MCP rows, the paragraphs on what those surfaces return, and the provider envelopes of "Errors" move to the matching adapters, as T17 already records. The parts that move govern the adapters rather than Lenny, stay here until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). Revision 13 restates their "New results" column and their calls in terms of the blocking interface: an adapter builds a provider response from a group's `tool_call` event and posts its caller's results through the answer route.

#### Requests and results

| Surface | Tools and tool choice | Earlier calls and results in history | New results |
| :-- | :-- | :-- | :-- |
| Chat Completions | `tools[].function` to function entries of `clientTools` (`parameters` to `inputSchema`), and `tools[].custom` to custom entries with `format`. `tool_choice` `auto`, `none`, `required` (to `any`), a named function or a named custom tool (to `tool`), and `allowed_tools` (to `allowedNames`, "CT-3"). `parallel_tool_calls: false` to `parallel: false`. | `assistant` messages with `tool_calls` become assistant entries with `clientToolCalls`, with a function call's `arguments` parsed from the JSON string and a custom call's `custom.input` as `input`. `tool` messages become user entries with `toolResults`. | The `tool` messages after the last assistant message answer the calls of that message through the answer route, or, on a cold turn, become the envelope's `toolResults`. Each must answer a call of that assistant message, and every call of that message must be answered, or the request is refused with `400 invalid_request_error`. |
| Responses | `type: "function"` and `type: "custom"` tools to `clientTools`. `tool_choice` as Chat Completions, with `{"type":"custom","name"}` to `tool`. `parallel_tool_calls` as Chat Completions. | On a continuation, the cold walk of `R`'s conversation context reads the calls and results from the transcript. A full input list without `previous_response_id` maps its `function_call`, `custom_tool_call`, `function_call_output`, and `custom_tool_call_output` items into history. | `function_call_output` and `custom_tool_call_output` items answer the calls through the answer route, or, on a cold turn, become the envelope's `toolResults`, with a string `output` as a `text` part and an array as `text`, `image`, and `file` parts. |
| REST | `clientTools` and `toolChoice` inside the body's `conversation` member on `POST /v1/sessions/{id}/messages` and on the first message of `POST /v1/sessions/start`, in the CT-1 form, where [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) R15 places conversation input. `clientToolTurns` and `clientToolHoldSeconds` on the request that creates the session ("CT-5"). | `conversation.entries` under R15, which a provider adapter writes for a cold turn. Open decision O13 asks how the marked entry carries the final round's results. | Each result on `POST /v1/sessions/{id}/tool-calls/{callId}/result`, by external id ("CT-5", "Answers"). |
| Messages API | `tools` (client tools only) to function entries of `clientTools` (`input_schema` to `inputSchema`). `tool_choice` `auto`, `any`, `tool`, and `none`, with `disable_parallel_tool_use: true` to `parallel: false`. The Messages API has no custom tool type and no allowed-tools choice. | `tool_use` blocks in assistant messages and `tool_result` blocks in user messages, as AM-4 maps history. | The leading `tool_result` blocks of the final user message answer the calls through the answer route, or, on a cold turn, become the envelope's `toolResults`, with `is_error` to `isError` and string or block content to parts. Text after them becomes the `input` of the adapter's next message. |
| MCP | The REST fields as arguments of `lenny/create_session` and `lenny/send_message`, generated from the OpenAPI definitions under [§15.2.1](../../spec/15_external-api-surface.md#1521-restmcp-consistency-contract) rule 4. | None. | A generated tool over the answer route. |

A client declares its tools on each message, so a message that carries no tools leaves the runtime with no client tools for the turn that message opens.

#### Responses with calls

| Surface | Plain response | Stream |
| :-- | :-- | :-- |
| Chat Completions | `message.content` holds the text of the output parts, or `null` when there is none, and `message.tool_calls` holds one `{id, type: "function", function: {name, arguments}}` per function call of the group, with `arguments` serialized as a JSON string, and one `{id, type: "custom", custom: {name, input}}` per custom call. `finish_reason: "tool_calls"`. | Content deltas for the output parts, then one chunk per call carrying `index`, `id`, `type`, `function.name`, and the whole `function.arguments`, then a separate chunk with `finish_reason: "tool_calls"`, then `[DONE]`. The separate final chunk lets the `openai-python` stream helper emit its arguments-done event. A streaming request that declares a custom tool is refused under O10, so no custom call is streamed. |
| Responses | `message` output items for the output parts, then one `function_call` item per function call of the group with `call_id`, `name`, `arguments` as a JSON string, and `status: "completed"`, and one `custom_tool_call` item per custom call with `call_id`, `name`, `input`, and `status: "completed"`. The response's `status` is `completed` **[unverified]**. | Per function call: `response.output_item.added` with empty `arguments`, one `response.function_call_arguments.delta` with the whole string, `response.function_call_arguments.done`, and `response.output_item.done`. Per custom call: `response.output_item.added` with empty `input`, one `response.custom_tool_call_input.delta` with the whole string, `response.custom_tool_call_input.done`, and `response.output_item.done`. Then `response.completed`. |
| REST | The `tool_call` event carries each group with its external ids and the turn's provenance, `tool_call_withdrawn` names withdrawn calls, and `openToolCalls` on `GET /v1/sessions/{id}` lists the open calls ("CT-5"). The message route returns delivery receipts alone ([`0135-message-delivery`, "MD-7"](../0135-message-delivery/change.md)). | The session event stream carries the `tool_call` and `tool_call_withdrawn` events. |
| Messages API | `text` blocks for the output parts, then one `tool_use` block per call of the group with `id`, `name`, and `input`. `stop_reason: "tool_use"`. | Per call: `content_block_start` with the `tool_use` block and `input: {}`, one `input_json_delta` with the whole input, and `content_block_stop`. Then `message_delta` with `stop_reason: "tool_use"` and `message_stop`. |
| MCP | The tool result's `structuredContent` carries the group's calls. | Not applicable. |

The runtime writes each call whole, so no surface streams partial arguments. Each surface's documented accumulation accepts one delta that carries the whole value.

#### Provider adapters

A provider adapter is a REST client of the session it creates, so every rule of "CT-5" applies to it **[design, 0166 decision 23]**:

- The adapter sets `clientToolTurns: own` on every session it creates, because its caller receives calls only in the response to the caller's own request (0166, decision 48).
- The adapter returns a group's calls to its caller in one provider response as the provider's tool use, in Chat Completions, Responses, or the Messages API, and ends that response. The runtime's turn stays open while the calls wait.
- When the caller's next request brings results, the adapter finds the session through 0138's response identifier or conversation fingerprint, posts each result through the answer route, and answers the caller's request with the output that follows on the session's event stream, up to the next group or the end of the turn.
- A caller that never returns lets the calls' hold pass. The gateway withdraws them, and a result the caller brings later reaches the runtime as a late result: the adapter posts it, receives `late`, and sends the caller's new input, or a message with no input parts, as the session's next message.
- A caller whose request the adapter cannot route to the session continues cold in a new session, whose history carries the earlier calls and results ("CT-7").

#### Errors

Every refusal uses the surface's own error envelope: Lenny's REST envelope with the codes of this design on REST, the OpenAI envelope on Chat Completions and Responses, and the Anthropic envelope of AM-8 on the Messages API, with the Lenny code in `error.details.error_code`.

| Code | Category | REST status | Meaning |
| :-- | :-- | :-- | :-- |
| `CLIENT_TOOLS_UNSUPPORTED` | `PERMANENT` | 422 | The chosen revision does not declare `capabilities.clientTools`, or does not declare `customTools` for a request with a custom tool, or under O4 the session cannot take a further message. |
| `CLIENT_TOOL_DEFINITION_INVALID` | `PERMANENT` | 400 | A tool name, schema, form, or limit is refused at admission, or `clientToolTurns` or `clientToolHoldSeconds` holds a value outside its range. |
| `CLIENT_TOOL_NAME_RESERVED` | `PERMANENT` | 400 | A tool name begins with `lenny`, is in `reservedNames`, or is the name of a platform tool or a reference tool. |
| `CLIENT_TOOL_CALL_NOT_FOUND` | `PERMANENT` | 404 | The answer route names no call of the session. |
| `CLIENT_TOOL_RESULTS_FORBIDDEN` | `POLICY` | 403 | An answer from anyone other than the session's client. |
| `CLIENT_TOOL_CALL_INVALID` | `PERMANENT` | None: an `error.code` on the runtime's `tool_result`, a turn error on the turn resource, and the `error` event ("CT-4") | A call or a group broke a rule of "CT-4". |
| `CLIENT_TOOL_CALL_WITHDRAWN` | `TRANSIENT` | None: an `error.code` on the runtime's `tool_result` ("CT-5") | The gateway withdrew an open call, with the reason `hold_expired`, `group_incomplete`, `cancelled`, `interrupted`, `pod_lost`, or `closed`. |

### CT-7 Continuation

> **Adapter-facing section.** **Runtime obligations**, **Runtime revisions**, **Derive**, **Re-running a session**, and the REST parts of **Resume after pod failure**, **Idle suspension**, and **Retries** still govern Lenny. **Warm and cold** (with T4), the Responses and Messages API parts of the other paragraphs, and the join of O5 move to the provider adapters. **Order of history entries** binds every history the gateway builds and the Open Responses adapter's cold walk, which moves with that adapter. The parts that move govern the adapters rather than Lenny, stay here until each adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

**Warm and cold [design].** A Responses, Messages API, or Chat Completions continuation is warm when the adapter finds the session by response ID or by conversation fingerprint, under R16 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-10-the-provider-adapters). A warm continuation answers the open calls through the answer route, or, after their withdrawal, delivers the results as late results with runtime ids. A cold turn creates a session, delivers the earlier calls and results in `conversation.entries` with external ids, and delivers the final results in `toolResults` with external ids. A Chat Completions round trip is warm or cold under the same rule (T4, as superseded). The Messages API digest covers `tool_use` and `tool_result` blocks, so the continuation of a response that returned calls finds the session that issued them.

**Order of history entries [design, T5].** Whenever the gateway builds `conversation.entries` from a transcript, as the Responses cold walk of `R`'s conversation context does in `continuity.rehydrate` (`pkg/gateway/environment/translator/continuity.go`), it places a message that arrived while a call was open after the entry that carries the call's result. Each assistant entry with `clientToolCalls` is then followed directly by the user entry with its `toolResults`. When those results are the turn's new input and arrive in the envelope's `toolResults`, the message follows the marked new entry. A withdrawn call whose result never arrived is followed by its error result. The rule matters for a steer-declaring runtime, which receives messages while a call is open. A history the client supplies, on Chat Completions, on a Responses request without `previous_response_id`, and on the Messages API, is the client's own, and the gateway does not reorder it.

**Runtime obligations [design, O2].** A declaring runtime accepts a result as the call's `tool_result`, accepts late results in `toolResults` for calls it no longer waits on, accepts results cold from history, and accepts late results for calls it no longer holds in memory after a resume, continuing from its restored state. A runtime that cannot continue answers with an end-of-turn `message` carrying `error`, which fails the turn.

**Resume after pod failure [design].** A pod loss withdraws the session's open calls with the reason `pod_lost`, and the open-call records and late results are session data, so [§7.3](../../spec/07_session-lifecycle.md#73-retry-and-resume) needs no new step. A client's answer after the loss returns `late`, and the late result reaches the restored runtime on the client's next message. For a parent, SL-12 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-12-recovery-of-child-events) resends an open call's `tool_call` event to a parent restored after its own pod loss. On the provider surfaces a session recovered from a checkpoint taken after the response that returned the calls continues warm, and one recovered from an older checkpoint fails R16's condition on the session's state, so the continuation is cold.

**Idle suspension [design, 0166 decision 24].** An open call counts as qualifying activity under [SL-6](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy) until its hold passes, so a session waiting on its client keeps its pod for at most `clientToolHoldSeconds`. After the withdrawal, the session becomes `idle` and then `dormant` as any session does, and the client's next message, carrying any late results, resumes it on the resume path of [MD-5](../0135-message-delivery/change.md#md-5-delivery-paths).

**Retries [design, O5].** A client that retries an answer after a dropped connection receives the first answer's outcome, because the answer route accepts one answer per call, so a result reaches the runtime once. On the Messages API the in-flight join of the owner's M1 decision reattaches a retried request to the running turn. On Responses, the join of O5 reattaches a continuation whose input equals that of the running continuation of the same response. The client reads the turn's outcome from the turn resource of R3. The client executed the tool before it answered, so no retry runs a client tool again.

**Runtime revisions [design].** The capability is read from the chosen revision at the post-chain point. A warm turn stays on its session's revision. A cold turn of an existing conversation is a new create and may land on another revision under RV-2's rollout key, which defaults to the user and keeps a user on one revision while traffic is unchanged. When the new revision lacks the capability, the request is refused before a pod is claimed, with the refusal naming the revision. A rollout that removes the capability therefore breaks conversations in flight, and the rollout documentation states it.

**Derive [design, 0166 "DP-7"].** A derive copies no open call and no late result. A derive under [`0144-session-derive`](../0144-session-derive/change.md) starts a new session from a snapshot, the source's open calls stay with the source, and the derived session's first client message declares its own tools. A derived runtime whose restored state holds a call with no result meets it under the runtime obligation above, as after a resume that lost an open call. The refusal of T11 lapses with the copy.

**Re-running a session [design, O8].** Server-side replay no longer exists, because [`0144-session-derive`](../0144-session-derive/change.md) removes `POST /v1/sessions/{id}/replay`. A client that re-runs a session reads its client messages through SD-1's "Reading client messages", sends them again, and executes its own tools when the new session calls them. The read returns `toolResults` with each message that carried late results, so the client can see what it answered.

### CT-8 Security

**Trust of results [design].** A result comes from the session's own client, so it has the client's trust level: a REST client's or an adapter's result carries `from.kind: client`, and a parent's result is the parent session's output, which the child treats as it treats any message from its client. A runtime treats result content as untrusted input, as it treats a user message. The new exposure is indirect: a client tool may return third-party content, such as a fetched page, and an instruction injected into that content can steer the agent toward its other tools, such as the tools of an operator service, which can act with the user's linked credentials through a `userCredential` binding. Tool-use approvals on platform tools, egress rules, and credential bindings apply to those downstream calls unchanged, and an operator service applies its own tool rules and approvals (CR-3). A parent's handler runs with the parent's authority, so a child steers actions that carry it, which the switches and approvals of 0166 "DP-8 Parity with a client" bound.

**Interceptors [design, O6].** The gateway runs `PreToolResult` over each result before it writes the result to the runtime or stores it as a late result, with the §4.8 payload `{id, content, isError}` and the external id as `id`. A `REJECT` refuses the answer with the interceptor's error, and the call stays open until another answer or its withdrawal. A `MODIFY` replaces `content`. The gateway runs `PostAgentOutput` over each group's calls with their arguments or custom input, in the `clientToolCalls` member of its payload under O6, before it delivers the group. A `MODIFY` that changes a call's arguments or input is validated again under "CT-4", and a modified call that fails validation is a violation. Both phases run for every client. A call to a parent's tool also passes the parent's `contentPolicy` and `PreMessageDelivery` as a child event under MD-22.

**Schema loading [design].** A client's `inputSchema` is untrusted, so the compiler's `LoadURL` refuses every URL. Without it, a `$ref` naming a `file` URL would read a file from the gateway's filesystem, because the library's default loaders resolve that scheme ("Current state").

**Reserved names [design].** The gateway refuses a client tool whose name begins with `lenny`, is in the revision's `reservedNames`, or is the name of a platform tool or a reference tool ("CT-3"), so a client tool never shadows a platform tool. The names of the tools a runtime runs in its own process, such as an operator service's MCP tools, are known only to the runtime, so the gateway cannot check them at admission. The runtime presents its own, platform, and client tools to its model without collision, and a runtime that finds a collision answers with an end-of-turn `message` carrying `error`.

**Approvals [design].** SO-5a rules match platform tool names. A call to a tool of a REST client or a provider adapter never reaches the approval gate, because the platform executes nothing, and the client decides whether to run it. A rule whose name matches such a tool has no effect on it, and the documentation states it. A call to a parent's tool runs with the parent's authority, so SO-5a's `approvals.parentTools` can hold it for a person's approval before it reaches the parent ([`0142-security-oversight`, "SO-5a"](../0142-security-oversight/change.md#so-5a-tool-use-approvals), 0166 decision 31). A deployer who wants no client tools for a tenant clears the capability through the per-tenant override (O7), and the security record's fields of 0142 forbid parent tools.

**Ownership and impersonation [design].** An answer is session input, so session ownership under [§10.2](../../spec/10_gateway-internals.md#102-authentication) **Session ownership.** governs the answer route, and the warm conditions of each surface bind a continuation to its owner. Whatever rule closes F-13.3.26 for session input applies to the answer route in the same change.

**Audit and events [design, 0166 decision 42].** This design owns the audit of every client tool call, a call to a parent's tool included, which 0166 cites. The gateway audits a delivered group, each answer, each withdrawal, each late result's delivery, and each violation. Each record names the session, the call's external id, the group, the tool's name, the client (the REST principal, the adapter's client name, or the parent's session id), the turn's provenance, and the outcome: `answered` with the result's `isError`, `withdrawn` with the reason, `late`, or `invalid` with the reason, and SO-5a's approval outcome when a rule held the call. No record carries arguments, custom input, or result content. The transcript holds them, so the §12.8 erasure of the session removes them (CTR-8). `tool_call` events carry arguments to readers of the session's event stream, who already read the session's output, and to a parent, which offered the tool.

### CT-9 Children

A child never receives its parent's client's tools **[design, T7, 0166 decision 29]**. The gateway writes into a child's `conversation.clientTools` only the tools its parent offers with `tools` on `session.create` and `session.send`, under the switches of [`0166-delegation-primitives`, "DP-8 Parity with a client"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client). A call from a child to any other tool is the violation `undeclared_tool` or `no_tools_declared`. A parent that wants a child to use the result of its own client's tool calls that tool itself and passes the result to the child as input, and its client's turn rule decides whether the parent may call the tool in a turn the child's event started ("CT-5", "Turn rule"). A compromised child therefore cannot direct the root client to execute anything.

### CT-10 Runtime SDKs and conformance

**SDKs [design].** The Go, Python, and TypeScript runtime SDKs add `Message.ToolResults` and the `Conversation` members (`ClientTools`, `ToolChoice`, and history entries with calls and results, with custom tool entries and calls and `ToolChoice.AllowedNames`), in each language's naming convention. Each SDK adds a blocking `ClientToolCall` helper beside its `ToolCall`, which writes a `tool_call` naming `server: "client"`, marks the calls of one model step with one `group`, waits for each result with no timeout of its own, since the hold bounds the wait, and returns `CLIENT_TOOL_CALL_WITHDRAWN` as an error the runtime's loop passes to its model (0166, decision 23).

**Conformance [design].** [§15.4.6](../../spec/15_external-api-surface.md#1546-conformance-test-suite) gains cases for a runtime that declares the capability: it calls a client tool with a frame that passes "CT-4", marks parallel calls with one group, continues from a live answer, continues from a withdrawal's error result and later from the late result in `toolResults`, accepts results cold from history with external ids, honours `toolChoice` `none`, `parallel: false`, and `allowedNames`, accepts a late result it does not hold in memory after a resume, and cancels a call with `tool_cancel`. A runtime that also declares `customTools` calls a custom tool with an `input` string. A runtime that declares `steer` places a message it receives while a call is open after the call's result. The suite runs them against a conformance fixture runtime that declares the capability, and through the reference bridge's `client` server. Open decision O22 asks whether the §26 reference runtimes declare it.

## Worked example

> **Adapter-facing section.** The example runs on the Responses surface, which moves to the Open Responses adapter. Steps 1, 3, and 6, where the adapter decodes a Responses request, returns a `function_call` item, and answers a native `404`, describe the adapter's part. The gateway's part, which still governs Lenny, is the admission, the post-chain check, the validation, the open call with its hold and turn rule, and the delivery of the result, reached through REST. The parts that move govern the adapters rather than Lenny, stay here until the adapter's repository takes them, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

Alice, a user of tenant `acme`, uses the OpenAI Python SDK with the Responses API. The runtime `order-agent` revision 3 declares `conversationInput`, `clientTools.supported: true`, `multi_turn`, and injection, and runs its own model loop. Revision 3 does not declare the `steer` delivery intent. The tenant's ceiling for `clientToolHoldSeconds` is 600 seconds, and the adapter leaves the default of 300.

1. Alice sends `model: "order-agent"`, a function tool `lookup_order`, and "Where is order A-1009?". The adapter creates session `S` with `clientToolTurns: own` and sends the question with the tool in `conversation`. The gateway checks the tool, chooses revision 3, finds the capability at the post-chain point, and delivers the message. The runtime's model calls the tool, and the runtime writes a `tool_call` with `server: "client"`, id `tc_7`, and `arguments: {orderId: "A-1009"}`, and waits.
2. The gateway validates the arguments against the schema, records the open call with external id `call_8Kq...`, and emits a `tool_call` event with the call and the provenance `startedBy: {kind: "client"}`. The adapter returns response `S.1` to Alice with a `function_call` item and ends that HTTP response. The runtime's turn stays open.
3. A parent session sends `S` a message. The turn is open and revision 3 does not declare `steer`, so the queue path holds the message until the turn ends.
4. Alice's program runs `lookup_order` and sends `previous_response_id: "S.1"` with a `function_call_output` for `call_8Kq...` and the tool again. The adapter finds `S`, posts the result through the answer route, and receives `answered`. The gateway runs `PreToolResult` and writes the result to the runtime as the `tool_result` for `tc_7`.
5. The runtime answers "Order A-1009 shipped on 3 November." and ends its turn. The adapter returns that output as response `S.2`. The turn end releases the parent's message, and the turn it opens carries no `conversation.clientTools`, because the parent started it and the turn rule is `own`. Its output reaches the transcript and the session's REST event stream, and no response on the Responses surface, under O3 of [`0135-message-delivery`](../0135-message-delivery/change.md).
6. Bob, also of `acme`, sends `previous_response_id: "S.1"` with a result. He may not read `S` under session ownership, so the adapter answers the continuation with a native `404` before any answer is posted, as §15 states for an id that `GET /v1/responses/{id}` would not return to him.

Had Alice's program taken longer than 300 seconds in step 4, the gateway would have withdrawn `tc_7` with `hold_expired`, and the runtime's model would have received an error result and ended its turn. The result Alice sent afterwards would have returned `late`, and the adapter would have sent it as `toolResults` on the session's next message.

## Limits

- **Runtime cooperation.** A runtime calls a client tool with a blocking frame and continues from a late result on a later message. A harness behind the reference bridge needs only to call the bridge's `client` tools and to refresh its tool list when the list changes, and whether each reference harness does so is owed validation ("Validation owed" in `steps.md`).
- **An open call holds the pod.** An open call counts as activity, so a session waiting on its client keeps its pod for up to `clientToolHoldSeconds`, 300 seconds by default.
- **A slow client receives a withdrawal.** A client that answers after the hold passes finds the call withdrawn, and the runtime has already continued from an error result. The late result reaches the runtime only on the client's next message.
- **Chat Completions creates a session when its history changes.** A round trip whose conversation the fingerprint does not find, because the client edited, summarized, or reordered its history, creates a session, which counts against admission and the session-creation rate limits and claims a warm pod.
- **Forced tool choice depends on the runtime's model.** The Anthropic documentation states that `tool_choice` `any` and `tool` return a 400 error on several current models. A runtime on such a model cannot honour a forced choice, and its turn fails under "CT-4" when it makes no call.
- **Messages wait behind an open call.** For a runtime that does not declare `steer`, a message from another sender waits on the queue path while a call is open, as it waits behind any open turn.
- **Calls in turns another sender started.** Under `clientToolTurns: all`, a client receives calls in turns that another session's content entered, and the provenance on each event is the client's only input for deciding whether to run them.
- **Grammar conformance is unchecked.** The gateway never checks a custom call's input against its grammar, so a defective runtime can return input that breaks it, and the client's own check is the only one (O9).
- **No streamed custom calls on Chat Completions.** Under O10, a streaming Chat Completions request with a custom tool is refused.
- **A rollout can break conversations.** A cold continuation that lands on a revision without the capability is refused.
- **Validation cost.** Each call is validated against a schema the client supplied, bounded by the limits of "CT-3".
- **Arguments in events.** `tool_call` events carry arguments to every reader of the session's event stream.

## Alternatives rejected

- **The turn-ending exchange of revisions 1 to 12.** The runtime ended its turn with `clientToolCalls`, the gateway recorded a pending set on the session, and the client's next request delivered the results as the next turn. It reached no harness that runs its tool loop inside one turn, it needed a pending set, a hold rule for messages from other senders, and an abandon field, and it gave a parent and a REST client different interfaces. Decision 23 of 0166 replaces it with the blocking call.
- **The bridge of AM-7.** A blocking `tool_call` marked for the client kept the runtime's turn open across HTTP requests of the Messages surface alone, with no bound on the wait and no path for a result that arrives after a pod failure. This design keeps the blocking call and adds the hold, the withdrawal, late results, and one interface for every client.
- **A fixed refusal of calls in turns another sender started (T16 as revisions 4 to 12 stated it).** It left no way for a client that accepts such calls to receive them. Decision 48 of 0166 makes the rule the client's choice for the session and adds the provenance a client needs to apply its own policy.
- **An unbounded wait.** A call that waits until the client answers holds the pod for as long as the client takes, and a client that never returns holds it until the session's own limits end it. Decision 24 of 0166 bounds the wait with the hold.
- **`lenny/request_input` or elicitation as the carrier.** Both block inside a turn and carry a question for a person. A tool result is program output with per-call structure, and the Messages and OpenAI adapters declare no elicitation.
- **New `MessagePart` types or frame types.** Unknown part types collapse to `text`, and a new frame type adds a schema and a demultiplexer case for what members of the existing frames express.
- **Interception in the LLM proxy.** The proxy could add the client's tools to a model request, but the runtime's loop would receive calls for tools it does not know.
- **Repairing calls at the gateway.** Coercing arguments or dropping extra calls would hide a runtime defect from its author and deliver calls the runtime never made (T6).

## Specification changes

> **Adapter-facing section.** The §15 row ("Built-in adapter single-shot compute model", "Open Responses conversation sessions", and the provider mappings), the §15.2 and §15.2.1 row, and the per-surface rows of the "Translation Fidelity Matrix" in the §15.4.1 row move to the provider and MCP adapters, and stage no edit to Lenny's specification, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). The `tool_call` and `tool_call_withdrawn` session events in the §15 row still govern Lenny. Every other row still governs Lenny. "Target page" names, by page code, the page of `spec/` that takes each edit; the placeholder pages `CTOOL`, `RTPMSG`, `RTPHAND`, `RTPSDK`, `RTPCONF`, `CAPIMSG`, and `DLG` already list this change.

| Section (by heading) | Target page | Change |
| :-- | :-- | :-- |
| §28.5.3 Intra-pod, `CH-MSGSOCK` card, the `tool_call` and `tool_result` frames, and "Message schemas" | `RTPMSG` | A client tool call as a `tool_call` naming `server: "client"` with `group`, its validation by reference to the external API section, its `tool_result` with the codes `CLIENT_TOOL_CALL_INVALID` and `CLIENT_TOOL_CALL_WITHDRAWN`, and `tool_cancel` withdrawing it. [`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) stages the `server` value and `group` on the frame, and this row states what they mean for a client tool. The JSONL schema references. No channel is added, so the §28.3 registers and the naming law are unaffected. |
| §15.4.1 Message Format and Binary I/O Requirements, "`MessageEnvelope` — Unified Message Format" and "Translation Fidelity Matrix" | `RTPMSG`, `CAPIMSG` | `toolResults` for late results, and the `clientTools` (function and custom entries), `toolChoice` (with `allowedNames`), and history-entry members of the conversation input that AM-4 adds, present on envelopes of the turns the session's turn rule admits. Rows for calls and results per surface, including custom calls. |
| §5.1 Runtime, capability fields and the derived-runtime rules | `RTPHAND` | `capabilities.clientTools` with `supported`, `customTools`, and `reservedNames`, its dependency on `conversationInput`, and its override rule (O7). |
| §7.2 Interactive Session Model, "Message delivery routing" | `CTOOL`, `MSG` | Open calls: the `tool_call` event to the client and, for a parent, as a child event under MD-22; groups; the turn rule `clientToolTurns` with the turn's provenance on every event (0166 decision 48); the hold `clientToolHoldSeconds` with its tenant ceiling and platform cap, and the withdrawal with `tool_call_withdrawn` (0166 decision 24); the answer route and its outcomes; late results on the client's next message (DPR-13 of 0166); cancellation, interrupt, and close; and the steer-declaring runtime's obligation to place a message after an open call's result. |
| §7.3 Retry and Resume | `RESUME`, `CTOOL` | A pod loss withdraws the open calls, open calls and late results survive replica failover as session data, the runtime obligation for late results it does not hold, and the order of entries in a history the gateway builds. |
| §7 Session Lifecycle, qualifying activity and the close rule | `SESS` | An open call counts as qualifying activity until its hold passes, and a close withdraws the open calls. [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy) stages SL-6 and SL-7, and this row supplies the client-tool sentences. |
| §15 External API Surface, "Built-in adapter single-shot compute model", "Open Responses conversation sessions", and "SessionEvent Kind Registry" | None in Lenny for the provider mappings: the provider adapters. The `tool_call` and `tool_call_withdrawn` session events land on `CATEVENT` and `CAPIEVT`. | The Chat Completions and Responses mappings, including custom tools and `allowed_tools` (O9) and the refusal of a streaming Chat Completions request that declares a custom tool (O10), a group returned as one provider response, results posted through the answer route, the cold walk's calls and results in the order of "CT-7", and the retry join (O5). The session events `tool_call`, with the group, the external ids, the hold deadline, and the turn's provenance, and `tool_call_withdrawn`, with the external ids and the reason. |
| §15.1 REST API and its error code catalog | `CAPIMSG`, `CAPISES`, `CATERR` | `clientTools` and `toolChoice` on the message routes, `clientToolTurns` and `clientToolHoldSeconds` on the session-creating request, a message with no input parts while late results wait, `POST /v1/sessions/{id}/tool-calls/{callId}/result` with its outcomes, `openToolCalls` on the session read, and the error codes of "CT-6". 0138 lists the route and the events in RO-2. |
| §15.2 MCP API and §15.2.1 REST/MCP Consistency Contract | None in Lenny: the MCP adapter | The generated arguments and tools over the REST fields and the answer route, and contract-test coverage. |
| §15.4.3 Runtime Integration Levels, §15.4.6 Conformance Test Suite, and §15.7 Runtime Author SDKs | `RTPHAND`, `RTPCONF`, `RTPSDK` | Availability to every runtime, stated in the capability terms that [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#specification-changes) puts in place of the integration levels, the conformance cases, the SDK types, and the blocking `ClientToolCall` helper. |
| §4.8 Gateway Policy Engine, the phase table | `INTCPTIF` | `PreToolResult` on client results, live and late, and the `clientToolCalls` member of the `PostAgentOutput` payload (O6), for every client. |
| §8.2 Delegation Mechanism | `DLG` | A child never receives its parent's client's tools, and the only client tools a child receives are those its parent offers under 0166. |
| §10.2 Authentication, **Session ownership.** | `AUTHZ` | The answer route as session input. |
| §11.7 Audit Logging | `AUDIT` | The audit records of every client tool call, a parent's included, with the fields of "CT-8" and no arguments or content (0166 decision 42). |
| §12.8 Compliance Interfaces | `ERASE` | Open calls and late results in the session erasure. |
| §13 Security Model | `SECMODEL`, `CTOOL` | The trust level of results, a parent's authority behind its tools, reserved names, schema loading, the turn rule, and the approvals statement. |
| §16.1 Metrics | `CATMETRIC` | `lenny_client_tool_call_invalid_total` and `lenny_client_tool_call_withdrawn_total{runtime, reason}`. |
| §26 Reference Runtime Catalog | The successor of §26 that the runtimes area change names | Which reference runtimes declare `capabilities.clientTools`, under open decision O22. |

## Non-specification changes

> **Adapter-facing section.** The generated MCP arguments and tools of the `mcptools` bullet and the whole `pkg/gateway/environment/translator` bullet move to the MCP, OpenAI, and Open Responses adapters, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). No step of this change edits the translators for client tools, and open decision O12 states what 0122.S1 does to them until 0138.S6 removes them. Every other bullet still governs Lenny.

- `pkg/gateway/core/session/executor` and `pkg/runtimeexec`: `Message` gains late results and conversation input, the executor forwards a `tool_call` naming `client` to the open-call service and writes its `tool_result`, and the echo executor gains a client-tool mode for tests.
- `pkg/adapter`: the adapter forwards a `tool_call` naming `client` as it forwards `platform`, and `tool_cancel` for it, under RP-3.
- A new package for the client tool rules, `pkg/gateway/core/session/clienttools`, in the `core/session` directory that `pkg/gateway/ARCHITECTURE.md` assigns to the session operations: definition checks, schema compilation with refused loading, the `format` check of custom tools, the `allowed_tools` mapping, call and group validation, the turn rule, id minting, the open calls with their hold and withdrawal, the answer service, late results, and the violation and withdrawal metrics.
- `pkg/session/sessionrow`, `pkg/session/sessionstore` with its Postgres store, and `migrations/`: open-call records and late results, `clientToolTurns` and `clientToolHoldSeconds` on the session, and `openToolCalls` in the session read.
- `pkg/gateway/core/session/messagerouting`: late results on the client's next message, the message with no input parts while late results wait, and the turn rule's removal of tools from an envelope another sender's message opens.
- `pkg/gateway/core/session/sessionserver`: REST fields, the answer route, validation, error codes, the `tool_call` and `tool_call_withdrawn` events, and the OpenAPI document.
- `pkg/gateway/platformtools/mcptools`: generated arguments and tools over the answer route.
- `pkg/gateway/environment/translator`: tool decoding and refusals, custom tools and `allowed_tools`, history mapping with the entry order of "CT-7" in `continuity.go`, response and stream encoding, id mapping, and the interceptor phases, for Chat Completions, Responses, and later the Messages adapter.
- `pkg/apis/lenny/v1alpha1/runtime_types.go`, `pkg/runtime/runtimestore`, and `pkg/runtime/runtimecapoverride`: the field, its registration check, and its override rule. The post-chain check in the create-and-start service.
- `schemas/lenny-adapter-jsonl.schema.json`: `server: "client"` and `group` on `tool_call` with 0139, the codes on `tool_result`, and `toolResults`.
- `sdks/runtime/go`, `sdks/runtime/python`, and `sdks/runtime/typescript`: the types and the `ClientToolCall` helper of "CT-10".
- `cmd/lenny-gateway` and the chart: flags and Helm values for the limits of "CT-3" and the hold cap of "CT-5".
- Documentation under `docs/api/`, the runtime author guide, the configuration and metrics references, and the error catalog. The pages state every item below:
  - Settings and defaults: `clientTools.maxTools` (default 128), `clientTools.maxSchemaBytes` (default 256 KiB per request, schemas and grammar definitions together), `clientTools.maxOpenCalls` (default 32 per session), `clientTools.maxArgumentsBytes` (default 256 KiB per call), and `clientTools.maxHoldSeconds`, the platform cap on `clientToolHoldSeconds`, each a Helm value and gateway flag that the operator tunes; the tenant ceiling of `clientToolHoldSeconds` and its default of 300 seconds; `clientToolTurns` and its default `own`; and the part size rule that result content follows (64 KB inline, 50 MB staged, `413 MESSAGEPART_TOO_LARGE` above).
  - Client rules: the tool forms REST accepts and refuses, custom tools and their `format`, `allowed_tools` and its mapping, reserved names, the `tool_call` event with its group and provenance, the answer route with its outcomes, the hold and withdrawal, late results, the turn rule, and cold continuations.
  - Failure modes: the codes of "CT-6" with their categories and statuses, the violation reasons of "CT-4", the withdrawal reasons of "CT-5", and the native error each provider adapter returns.
  - Limits: every item of "Limits", including runtime cooperation, a pod held during an open call, a slow client, a session per Chat Completions round trip whose history changed, forced tool choice on models that refuse it, messages behind an open call, calls in turns another sender started, unchecked grammars, rollouts that break conversations, and arguments in events.
  - Runtime-facing rules, in the runtime author guide, the adapter contract page, and the SDK references: `capabilities.clientTools` with `supported`, `customTools`, and `reservedNames` and its dependency on `conversationInput`; `conversation.clientTools`, `conversation.toolChoice`, and `toolResults`; the `tool_call` naming `server: "client"` with `group`, its `tool_result` codes, and `tool_cancel`; the `client` tool server of the reference bridge; the history entries of a cold turn; the obligations of "CT-7" for late results; the steer-declaring runtime's placement of a message after an open call's result; and the absence of client tools in a turn the turn rule excludes.
  - Metrics: `lenny_client_tool_call_invalid_total` and `lenny_client_tool_call_withdrawn_total`.

## Defects found while verifying

These are defects in the current tree, found by reading the code and the specification on 2026-11-09. Nothing was run. The assessment's protocol findings are filed in `BUILD-GAPS.md` under the IDs below. This design carries a client tool call on the `tool_call` frame, so it depends on the fix of CTF-1: the forwarding path of [`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) answers every `tool_call` and forwards a `client` call to the gateway, in place of `handleFrame` discarding it. It does not use the approval relay of CTF-2, because a call to a parent's tool is held under SO-5a before the gateway delivers it to the parent, with no frame sent back to the runtime.

| ID | Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- | :-- |
| CTF-1 | `PodExecutor.handleFrame` discards a relayed `tool_call` that is neither adapter-local nor approval-gated, and writes no `tool_result`. The adapter relays every `tool_call` whose name is not adapter-local, and the gateway executor sends one with `approvalRequired` to the approval gate, completes the turn on `response`, and drops every other frame. A runtime that emits a `tool_call` naming an unknown tool waits out its own timeout, which in the Go SDK is the SDK's dial timeout, 5 seconds by default. §28.5.3 states no outcome for a `tool_call` naming an unknown tool. | `handleFrame` and `maybeGateToolCall` in `pkg/gateway/core/session/executor/podstream.go`. `HandleToolCall` in `pkg/adapter/toolcall.go` returns `handled` false for a name that `localtools.IsLocalTool` rejects, and the Attach loop in `pkg/adapter/attach.go` relays it. `AdapterTools.ToolCall` in `sdks/runtime/go/runtime/tool.go` takes its timeout from `dialTimeout` (`runtime.go`), whose default is 5 seconds (`options.go`). | F-28.5.3.1 [Medium] |
| CTF-2 | On approval, `verdictFrame` re-sends the runtime's own `tool_call` with the approval flag cleared, and the adapter writes it to the runtime's stdin. That is a `tool_call` from the gateway to the runtime, a direction §28.5.3 does not define: the `CH-MSGSOCK` card's adapter-to-runtime list does not include `tool_call`, and `approvalRequired` appears nowhere in `spec/`. The Go SDK's `routeFrame` ignores an inbound `tool_call`, so an approved call never runs. The JSONL schema's `tool_call` description says the adapter publishes the approval event and blocks the call, where the code gates it in the gateway executor, and it names platform MCP tools as `tool_call` targets, which "Tool access by level" excludes. | `verdictFrame` in `podstream.go`. `attachRecvLoop` in `pkg/adapter/attach.go`. The `tool_call` definition and its `approvalRequired` property in `schemas/lenny-adapter-jsonl.schema.json`. `routeFrame` in `sdks/runtime/go/runtime/runtime.go`. SO-5a states that enforcement at `maybeGateToolCall` applies "whatever the runtime's flag says", which relies on this relay. | F-28.5.3.2 [Low] |

Further observations surfaced while verifying. They bear on this design and are recorded for the specification owner without filing:

- The gateway executor does not read the `error` member of a runtime's `response`. `ResponseEnvelope` (`pkg/runtimeexec`) decodes `type`, `text`, and `output`, and §28.5.3 states that `error` maps the task to `failed`. The runtime obligations of "CT-7" depend on the gateway reading it.
- The OpenAI translators invoke no interceptor phase, so `PostAgentOutput` does not run on Chat Completions or Responses output today. CTR-6 requires it for call arguments.
- `validateAgainstSchema` compiles a runtime's `runtimeOptionsSchema` with the library's default loaders, which resolve `file` URLs. A schema written by a runtime author, including a tenant that writes a tenant-derived runtime, can name a file on the gateway's filesystem in a `$ref`. Whether the validation report can disclose its content was not checked.

## Documentation

The pages below state the items that the last bullet of "Non-specification changes" lists. Each line names the page, its reader, and what changes for that reader.

- `docs/api/rest.md` (client): `clientTools` and `toolChoice` inside `conversation`, `clientToolTurns` and `clientToolHoldSeconds` on the session-creating request, the answer route with its outcomes, the message with no input parts that carries late results, `openToolCalls` on the session read, and the refusals.
- `docs/client-guide/streaming.md` (client): the `tool_call` event with its group, external ids, hold deadline, and provenance, the `tool_call_withdrawn` event with its reasons, and at-least-once delivery keyed on the external id.
- `docs/client-guide/webhooks.md` (client): the webhook kinds of `tool_call` and `tool_call_withdrawn`.
- `docs/client-guide/error-handling.md` and `docs/reference/error-catalog.md` (client and operator): the codes of "CT-6" with their categories and statuses, the violation reasons of "CT-4", and the withdrawal reasons of "CT-5".
- `docs/client-guide/wire-format.md` (client): the JSON of the client tool members, the event, and the answer route.
- `docs/reference/adapter-contract.md` (runtime author): the `tool_call` naming `server: "client"` with `group`, its `tool_result` codes, `tool_cancel`, `toolResults` and the conversation members on the envelope, the history entries of a cold turn, and the runtime obligations of "CT-5" and "CT-7".
- `docs/runtime-author-guide/runtime-configuration.md` (runtime author): `capabilities.clientTools` with `supported`, `customTools`, and `reservedNames`, its dependency on `conversationInput`, the bridge's `client` tool server, and the absence of client tools in a turn the turn rule excludes.
- `docs/runtime-author-guide/runtime-sdk.md` (runtime author): the SDK types and the `ClientToolCall` helper of "CT-10".
- `docs/runtime-author-guide/testing.md` (runtime author): the conformance cases of "CT-10".
- `docs/runtime-author-guide/delegation.md` (runtime author): that a parent's tools are client tools of its child under this design, the turn rule a parent sets, the hold, withdrawal, and late results as a parent meets them, and a pointer to 0166's parent controls.
- `docs/reference/configuration.md` and `docs/operator-guide/configuration.md` (operator): the `clientTools.*` limits with their defaults, the cap `clientTools.maxHoldSeconds`, the tenant ceiling of `clientToolHoldSeconds`, and the part size rule for result content.
- `docs/reference/metrics.md` (operator): `lenny_client_tool_call_invalid_total` and `lenny_client_tool_call_withdrawn_total`.
- `docs/operator-guide/upgrades.md` (operator): a rollout to a revision without the capability breaks conversations in flight, and the per-revision violation counter stops a rollout whose binary breaks the contract.
- `docs/operator-guide/security.md` (operator): the trust level of results, a parent's authority behind its tools, reserved names, refused schema loading, the turn rule and provenance, the audit fields with no arguments or content, the statement that tool-use approval rules never match the tool of a REST client or an adapter, and `approvals.parentTools` for a parent's tools.

The provider and MCP pages, `docs/api/openai-completions.md`, `docs/api/open-responses.md`, and `docs/api/mcp.md`, leave Lenny with [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and this change edits none of them.

## Tests

> **Adapter-facing section.** The `contract` row's provider SDK cases and generated MCP tool schemas, the per-surface cases of the `unit` row (provider tool forms, `allowed_tools`, the streaming Chat Completions refusal, and history mapping per surface), the Responses and Chat Completions cases of the `integration` row, the concurrent Responses continuations of the `load_local` row, and the cold Responses continuation of the `chaos` row move to the provider and MCP adapters, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). Every other case still governs Lenny.

| Tier | Coverage |
| :-- | :-- |
| `unit` | Definition checks and refusals per surface, including names, reserved names with the reference tool names, refused tool forms, custom tool `format` values, `allowed_tools` entries, limits, and a schema with a `file` or `https` `$ref`. Each violation reason of "CT-4", including `input_form`, `group_mismatch`, `tool_choice_allowed`, `tool_choice_any` checked at the end of the turn, and `no_tools_declared` for a call in a turn another sender started under `own`, and a custom call whose input breaks its grammar passing. A violating group answered with error results and none of its calls delivered. Id minting and mapping. The turn rule: no `conversation.clientTools` or `conversation.toolChoice` on an envelope that opens a turn another session's message, a child event, a system message, or an external message started under `own`, and the tools of the client's most recent message on such an envelope under `all` (CTR-10). The provenance members `startedBy` and `contentFrom` for each kind of turn. The hold's default, the tenant ceiling, the platform cap, and the ceiling as the default when it is lower than 300 seconds. Each withdrawal reason with its `tool_result` code and event. The answer route's outcomes `answered`, a repeated answer, `late`, `404 CLIENT_TOOL_CALL_NOT_FOUND`, and `403 CLIENT_TOOL_RESULTS_FORBIDDEN`. Late results on the client's next message and on none from another sender, and a message with no input parts accepted only while late results wait. The order of history entries when a message arrived while a call was open, with the results in history and in the envelope. History mapping of calls and results per surface. The capability registration check and the override's narrowing. The `limit_exceeded` reason at each of `maxOpenCalls` and `maxArgumentsBytes`, with a late result holding its call's place. A `PreToolResult` rejection that leaves the call open. The O4 refusal at creation while O4 stands. The labels and values of `lenny_client_tool_call_invalid_total` and `lenny_client_tool_call_withdrawn_total`. The capability checks reading the effective capabilities, with a tenant override that clears `supported` or removes `steer`. |
| `contract` | The Anthropic Python and TypeScript SDKs and the OpenAI Python and TypeScript SDKs against an adapter for a round trip, a group of parallel calls, `is_error`, a forced choice, `allowed_tools`, a custom tool round trip on Chat Completions and Responses, and streaming, with recorded streams checked against each surface's event order. The JSONL schema for `tool_call` with `server: "client"` and `group`, the `tool_result` codes, and `toolResults` against the runtime SDKs. The generated MCP tool schemas against the OpenAPI document. |
| `integration` | A REST round trip on the compose stack: a `tool_call` event on the stream, an answer through the route, and the runtime continuing in the same turn. A group of parallel calls delivered in one event and answered out of order. An inter-session message to a session whose client declared tools under `own`: the envelope of the turn it opens carries no client tools, and a call in that turn fails with `no_tools_declared`, and the same message under `all` with the call delivered and its provenance naming the sender (CTR-10). A hold that passes: the runtime receives `CLIENT_TOOL_CALL_WITHDRAWN`, the client receives `tool_call_withdrawn`, the session becomes `dormant` after SL-6's idle time, and a later answer returns `late` and reaches the runtime in `toolResults` on the client's next message (CTR-4). A parent's tool called by its child: the `tool_call` child event, `tools.result`, and the child continuing; a child's call to a tool its parent did not offer fails (CTR-7); and a fenced child that receives no parent tools. A runtime's `tool_cancel` and an interrupt withdrawing their calls. A message from another sender held on the queue path while a call is open for a runtime without `steer`, and delivered to a steer-declaring runtime and placed after the call's result in a cold continuation's history. A derive from a session with an open call that copies neither the call nor a late result. A close while a call is open records `cancelled`. `PreToolResult` and `PostAgentOutput` with a test interceptor for a REST client and for a parent. The erasure of a session under §12.8 removes its open calls, late results, and the calls and results in its transcript (CTR-8). A retried answer that receives the first outcome. A violation in the `error` of the turn resource. Owner separation of open calls. |
| `e2e_kind` | A provider-adapter session on Kind through several round trips, a caller that returns after the hold passed and whose results arrive as late results, and a cold continuation in a new session. |
| `load_local` | Concurrent answers to one call: one is `answered` and the others return the same outcome. An answer racing the hold's withdrawal: the runtime receives exactly one of the result and the withdrawal, and a result that loses the race becomes a late result. Concurrent Responses continuations under O5. |
| `chaos` | Pod loss while a call is open, with the call withdrawn as `pod_lost` and the client's later answer reaching the restored runtime as a late result in `toolResults` (CTR-4, CTR-5, and DPR-13 of 0166). Replica loss while a call is open, with the answer delivered by the adopting replica. A cold Responses continuation after pod loss. |
| `security` | Reserved names, an answer for another owner's session, an answer from a session other than the call's client, an impersonation bearer under F-13.3.26, refused schema loading, and the absence of arguments and content in audit records for a REST client's call and for a parent's call (CTR-8). A client tool named for a reference tool, such as `start_session`, refused with `400 CLIENT_TOOL_NAME_RESERVED`. A child that never receives its parent's client's tools, under both turn rules (CTR-7). |
| `conformance` | The conformance cases of "CT-10". |
| `docs` | The API pages and the runtime author guide against the gateway. |

## Instructions for reviewers of other designs

- [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/change.md): AM-7's bridge is replaced by this design. Remove AM-7's "Call" and "Result" subsections, the `target` and `endOfBatch` members of `tool_call`, which RP-3's `server` and `group` replace, the client-tool row of "Open turns" under AM-6, and the §28.5.3 row of its "Specification changes" that adds them. AM-7 becomes a pointer to this design's "CT-6" Messages rows and "Provider adapters", and its admission paragraph defers to "CT-2" and "CT-3", including the reserved names and the `RequiresClientTools` flag of the single-shot spec. AM-3's `tools` and `tool_choice` rows point to "CT-6". AM-4's `clientTools` and `toolChoice` take the CT-1 form, and its history entries carry `clientToolCalls` and `toolResults`, with the marked new entry's results in the envelope's `toolResults`. AM-5's digest covers `tool_use` and `tool_result` blocks, and its in-flight join covers a request that carries results. AM-6's `stop_reason` list gains `tool_use` for a response that returns a group, and its streaming of `tool_use` blocks follows "CT-6". AM-8's table gains the codes of "CT-6", and `CLIENT_TOOL_CALL_INVALID` maps to `500 api_error` with `x-should-retry: false`. AMR-7 is restated as CTR-1 and CTR-3. Proposal AM-E is removed, and its client-tool work lands as CT-B. Its "Limits" bullet on client tools, its tier 5, 8, and 10 test rows for client tools, and O4 point to this design, and its link to "CT-4 Response validation" follows the new heading "CT-4 Call validation".
- [`0140-runtime-revisions`](../0140-runtime-revisions/change.md): RV-1's field classes list `capabilities.clientTools` as versioned and gateway-read. "Per-tenant capability override" gains O7's narrowing: clear `clientTools.supported`, clear `clientTools.customTools`, and append to `clientTools.reservedNames`. The session metrics list gains `lenny_client_tool_call_invalid_total` with `runtime_revision`.
- [`0141-runtime-routing`](../0141-runtime-routing/change.md): RT-2's `PreRoute` payload carries no client tools, and an interceptor cannot change them. The capability check is a post-chain check.
- [`0142-security-oversight`](../0142-security-oversight/change.md): SO-5a states that its rules never match the tool of a REST client or a provider adapter, and that `approvals.parentTools` holds a call to a parent's tool before the gateway delivers its `tool_call` event to the parent. Whether the hold of "CT-5" runs while such an approval waits is 0142's to state. Its reliance on `maybeGateToolCall` for relayed frames meets F-28.5.3.1 and F-28.5.3.2 (CTF-1 and CTF-2), which the proposal that lands SO-5a resolves or cites.
- [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md): no change to MP-1 to MP-8. MCP clients use the generated arguments and tools of "CT-6", and MP-5's intra-pod servers carry no client tools.
- [`0144-session-derive`](../0144-session-derive/change.md): SD-1's "Reading client messages" returns `toolResults` with each message that carried late results. A derive copies no open client tool call and no late result, and the refusal of T11 leaves with the copy.
- [`0135-message-delivery`](../0135-message-delivery/change.md): the hold rule for inbox deliveries during pending calls, the client-message rules that ran before the paths, and MD-11's client-call rows leave, because no pending set exists. A message that arrives while a call is open follows MD-5's paths for an open turn, and a steer-declaring runtime places it after the call's result ("CT-5"). MD-22 states the `tool_call` event class with its kinds `tool_call` and `tool_call_withdrawn` for a parent, and this design states the call, the hold, the answers, and late results that the event carries. The rule of its O3 that client tools are offered only in turns a client request caused becomes the turn rule of "CT-5" under decision 48 of 0166. Its terms "Client tool call and pending calls" and "Results message", and its links to "CT-4 Response validation" and "CT-5 Pending calls and the hold rule", follow the new headings "CT-4 Call validation" and "CT-5 Open calls".
- [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md): RO-2 lists the answer route `POST /v1/sessions/{id}/tool-calls/{callId}/result`, the session events `tool_call` and `tool_call_withdrawn` with their webhook kinds, `clientToolTurns` and `clientToolHoldSeconds` on the session-creating request, `openToolCalls` on the session read, and the message with no input parts that carries late results, citing this design for their semantics. RO-10's provider adapters follow "Provider adapters" of "CT-6", and R15's history entries keep `clientToolCalls` and `toolResults`. Its link to "CT-5 Pending calls and the hold rule" follows the new heading "CT-5 Open calls".
- [`0139-runtime-protocol`](../0139-runtime-protocol/change.md): RP-3's `server` takes `client` beside `platform`, a `tool_call` gains `group` with its id and size, and `tool_cancel` withdraws a client call. RP-4's `toolServers` gains `client`, and `availability_changed` also fires when the client's tool list changes. RP-5's bridge serves the `client` server and marks a harness's calls of one step with one group.
- [`0146-session-lifecycle`](../0146-session-lifecycle/change.md): SL-6 counts an open call as qualifying activity until its hold passes. SL-7's close rule reads the open calls in place of the pending set, and a close withdraws them. SL-12 resends an open call's `tool_call` event to a restored parent.
- [`0166-delegation-primitives`](../0166-delegation-primitives/change.md): this design states the interface that 0166 "DP-7" assigns to it and DPR-13 as CTR-4, and 0166 cites it for the call, the hold, late results, the turn rule, and the audit of a parent's tool calls. `session.create` takes `clientToolTurns` and `clientToolHoldSeconds` beside `tools`, and `session.send` with no parts carries late results to a child. 0166 keeps the parent's switches.
- [`0126-delegation-availability`](../0126-delegation-availability/change.md): no change. A child never receives its parent's client's tools, independently of delegation availability.
- [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md): F-13.3.26's list of session input routes gains the answer route.
- [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md): no change. A call is part of the turn that made it.

## Sources

Each page below was read on 2026-11-09. For revision 2, the OpenAI create references for Chat Completions and Responses, both streaming references, the function calling guide, the Open Responses specification, and `chat_completion_chunk.py` were read again on 2026-11-09 for `custom` tools and `allowed_tools`.

- Anthropic: [Define tools](https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use), [Messages: create](https://platform.claude.com/docs/en/api/messages/create), [Handle tool calls](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls), [Parallel tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use), [Handling stop reasons](https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons), and [Streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming).
- OpenAI Chat Completions: [Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) and [Streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events).
- OpenAI Responses: [Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create), [Retrieve a response](https://developers.openai.com/api/reference/resources/responses/methods/retrieve), [Streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [Function calling](https://developers.openai.com/api/docs/guides/function-calling), and [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses).
- Open Responses: [openresponses.org](https://www.openresponses.org/) and [Specification](https://www.openresponses.org/specification).
- MCP: [Versioning](https://modelcontextprotocol.io/specification/versioning), [2026-07-28 changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog), [2025-11-25 changelog](https://modelcontextprotocol.io/specification/2025-11-25/changelog), [Sampling](https://modelcontextprotocol.io/specification/2026-07-28/client/sampling), [Multi round-trip requests](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr), [Tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools), and [Tasks extension](https://modelcontextprotocol.io/extensions/tasks/overview).
- SDK source on `main`: [`openai-python` `_base_client.py`](https://github.com/openai/openai-python/blob/main/src/openai/_base_client.py), [`openai-python` `_constants.py`](https://github.com/openai/openai-python/blob/main/src/openai/_constants.py), [`openai-python` chat stream helper](https://github.com/openai/openai-python/blob/main/src/openai/lib/streaming/chat/_completions.py) and [`_deltas.py`](https://github.com/openai/openai-python/blob/main/src/openai/lib/streaming/_deltas.py), [`openai-python` `chat_completion_chunk.py`](https://github.com/openai/openai-python/blob/main/src/openai/types/chat/chat_completion_chunk.py), and [`anthropic-sdk-python` `_base_client.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/_base_client.py) and [`_constants.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/_constants.py).
