# Anthropic Messages API compatibility

Status: design for owner review, revision 1, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`runtime-routing-design.md`](runtime-routing-design.md) revision 11, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 7, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 64, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`security-oversight-design.md`](security-oversight-design.md) revision 47, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) revision 11, and [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 26. "Decisions" lists what the project rules settle and what remains open for the owner. The design stages no specification edit. The changes go through `change-proposal` after review. Parts of the design carry the label AM-n, requirements carry the label AMR-n, and the proposals that land them carry AM-A to AM-E under "Proposal split and order". "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an Anthropic documentation page or in an Anthropic SDK repository on 2026-11-09. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked for client-facing Anthropic Messages API compatibility next to the existing protocols. Lenny's gateway serves clients through MCP at `/mcp`, through its REST API under `/v1`, and through OpenAI-compatible translators at `/v1/chat/completions` and `/v1/responses` ([§15](../spec/15_external-api-surface.md#15-external-api-surface), "Built-in adapter inventory"). A program written against an Anthropic SDK cannot reach a Lenny runtime today. The gateway mounts no `POST /v1/messages` on its client listener, and its `GET /v1/models` answers in the OpenAI format **[source]**.

The premise holds in part. Lenny already parses the Anthropic Messages format, but on a different surface: the LLM proxy of [§4.9](../spec/04_system-components.md#49-credential-leasing-service), "LLM Reverse Proxy", serves `POST /llm-proxy/v1/messages` on its own listener (`--llm-proxy-addr`) to agent pods, authenticated by a credential lease token, and forwards each call to an upstream model provider **[source]**. That surface faces the pod and calls a model. The surface this design adds faces the client and calls a Lenny runtime, which is an agent with its own tools, workspace, and session. The two share wire types and nothing else.

The Messages API and Lenny disagree on these points, which the translator has to resolve:

- The Messages API is stateless. Each request carries the whole conversation, and the server keeps nothing between requests **[external]**. A Lenny session is stateful: a runtime holds its conversation and workspace across turns.
- A Messages client declares tools, and the model returns `tool_use` blocks that the client executes. A Lenny runtime runs its own tools inside a turn, and the runtime contract has no path that sends a tool call to the client ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), "Tool access by level") **[source]**.
- A Messages request carries model parameters such as `max_tokens`, `thinking`, and `stop_sequences` that address one model call. A Lenny turn can make many model calls, or none.

Verifying the existing translators against the tree found defects that this surface would inherit if it copied them ("Defects found while verifying"). The most consequential is that every message in a request's history reaches the runtime as a separate client turn with its role dropped, so a cold continuation re-runs earlier user turns and presents earlier assistant replies as client input.

## Scope

**Added:**

- A built-in external protocol adapter, `AnthropicMessagesAdapter`, serving `POST /v1/messages` on the client listener ("AM-1").
- Acceptance of a Lenny bearer in `x-api-key` on the adapter's routes, and the rules for `anthropic-version` and `anthropic-beta` ("AM-2").
- The mapping of a Messages request to a session creation or a turn, with a disposition for every request field and content block type ("AM-3").
- A conversation input member on the runtime's `message` envelope, which carries prior turns with their roles, client instructions, and per-turn parameters, and a runtime capability that declares it ("AM-4").
- Warm continuation of a conversation by a digest of its history, with a cold fallback that rebuilds the conversation in a new session ("AM-5").
- The response mapping, Anthropic server-sent events, and open turns that end an HTTP response before the runtime's turn ends ("AM-6").
- Client-declared tools, bridged to runtimes that declare them ("AM-7").
- The Anthropic error envelope and the mapping of Lenny error codes into it ("AM-8").
- The Anthropic form of `GET /v1/models` and `GET /v1/models/{model_id}`, and explicit refusals for the Anthropic endpoints Lenny does not serve ("AM-9").
- The rules for tenancy, billing, routing, revisions, and the MCP upgrade on this surface ("AM-10").

**Changed:**

- The OpenAI translators deliver history through the conversation input member of AM-4 in place of one turn per message (O3).
- `GET /v1/models` answers in the format of the protocol the request's headers identify ("AM-9").
- The MessagePart canonical type registry and the translation fidelity matrix gain an Anthropic Messages column.

**Removed:**

- The OpenAI translators' delivery of one turn per history message, and their `echo` default runtime for a request with no `model` (DF-1, DF-2, and DF-4).

**Kept:**

- The shared create-and-start service behind every built-in adapter, so admission, quota, the `PreRoute` chain, revision selection, and the post-chain point apply unchanged.
- The session as the unit of billing, ownership, erasure, and audit.
- The LLM proxy's `anthropic` dialect and its listener.

**Out of scope:**

- Message Batches, the Files API, Skills, and the Admin API. AM-9 refuses their paths.
- Token counting (`POST /v1/messages/count_tokens`). A runtime's tokenizer and prompt are unknown to the gateway.
- Token-level streaming. The runtime contract delivers output in parts, and AM-6 streams one delta per part.
- A long-lived API key primitive for Lenny. [§10.2](../spec/10_gateway-internals.md#102-authentication) states that no dedicated API-key primitive exists in v1, and adding one belongs to the identity design.

## Terms

| Term | Meaning |
| :-- | :-- |
| Messages API | Anthropic's `POST /v1/messages` and the endpoints around it, at API version `2026-10-21` unless a statement names `2023-06-01`. |
| Adapter | `AnthropicMessagesAdapter`, the built-in external protocol adapter this design adds. Its protocol identifier is `anthropic-messages`. |
| Conversation | The sequence of `system`, `user`, and `assistant` messages a Messages request carries, together with its top-level `system` instructions and `tools`. |
| Turn | One request's new input delivered to a runtime, and the runtime's output up to its `response` frame. |
| Warm turn | A turn delivered to a session that already holds the conversation, so only the new input reaches the runtime. |
| Cold turn | A turn that creates a new session and delivers the earlier conversation as history before the new input. |
| Conversation digest | A SHA-256 hash over the canonical form of a conversation prefix, which identifies the session that produced it. "AM-5" defines the canonical form. |
| Conversation input | The `conversation` member that AM-4 adds to the runtime's `message` envelope: history, instructions, parameters, and client tools. |
| Transcript rendering | The fallback that renders history and instructions as text parts for a runtime that does not declare conversation input. |
| Open turn | A turn whose HTTP response has ended while the runtime's turn continues: on a client tool call, on `lenny/request_input`, or at the wait limit of a non-streaming request. |
| Client tool | A tool that the request's `tools` array declares and that the client executes. |
| Runtime tool | A tool the runtime calls inside its turn: an adapter-local tool, a platform MCP tool, or a connector tool. |
| Anthropic error envelope | `{"type":"error","error":{"type":...,"message":...},"request_id":...}` with the HTTP status Anthropic pairs with each error type **[external]**. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). Each runtime the caller may use appears as a model on the OpenAI translators today, and this design does the same for the Messages API. |
| Session | Lenny's unit of work: one runtime instance bound to a pod, with an owner, a tenant, a transcript, and billing events ([§7](../spec/07_session-lifecycle.md)). |
| Built-in adapter single-shot compute model | The §15 rule that an OpenAI Chat Completions request runs an implicit session that claims a warm pod, runs one turn, and releases the pod within one HTTP call. |
| Held session | A session the Open Responses adapter keeps after a successful turn, when its `conversationContinuity` is `"platform"` and its runtime declares `capabilities.interaction: multi_turn` with `capabilities.injection.supported: true` (§15, "Open Responses conversation sessions"). |
| `PreRoute` chain and post-chain point | The interceptor phase that chooses a session's runtime and pool, and the single step after it, and after revision selection, where every check that depends on the runtime, the revision, or the pool runs ([`runtime-routing-design.md`, RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract)). |
| Runtime route | A tenant-scoped admin resource that maps a requested runtime name to weighted targets ([`runtime-routing-design.md`, RT-4](runtime-routing-design.md#rt-4-runtime-routes)). |
| Revision | An immutable, numbered copy of a runtime's versioned fields, `capabilities` among them, which the gateway chooses per session by the runtime's traffic weights ([`runtime-revisions-design.md`, RV-2](runtime-revisions-design.md#rv-2-selection)). |
| `runtimeOptions` | The session field that carries runtime-specific options, validated against the chosen revision's `runtimeOptionsSchema` at the post-chain point ([`runtime-revisions-design.md`, RV-7](runtime-revisions-design.md#rv-7-session-options)). |
| `MessageEnvelope` and `MessagePart` | The runtime-facing message format and its content parts ([§15.4.1](../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format", and [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). The envelope names its sender in `from` and carries no role. |
| `tool_call` and `tool_result` frames | The runtime's stdin and stdout frames for adapter-local tools ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `lenny/request_input` | The platform tool a runtime calls to block until a client answers a question ([§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model)). `maxRequestInputWaitSeconds` bounds the wait. |
| Scoped model namespace | A model named `environments/{name}/{model}` on the OpenAI translators, which creates the session in environment `{name}` ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)). |
| Session labels | The client-set `labels` map on a session, used for querying, usage reports, and billing filters. |
| Link check and `LINKS_REQUIRED` | The post-chain check that refuses a session whose user has not linked every connector and account the session would use ([`connector-authorization-and-external-memory-design.md`, CA-4](connector-authorization-and-external-memory-design.md#ca-4-link-check)). |
| Impersonation bearer and F-13.3.26 | A token a `platform-admin` obtains to act as a user ([§13.3](../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). F-13.3.26 is the `BUILD-GAPS.md` finding, recorded in [`user-credential-delivery-design.md`](user-credential-delivery-design.md), that session input routes and the OpenAI translators admit one. |
| Tool-use approval | A platform or connector tool call held at the gateway until an approver resolves it ([`security-oversight-design.md`, SO-5a](security-oversight-design.md#so-5a-tool-use-approvals)), and the §7.2 approval gate the executor runs today. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 1 unit, 3 contract, 4 integration, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, 10 runtime adapter conformance, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| RT-2 and RT-4 | [`runtime-routing-design.md`](runtime-routing-design.md) | The `PreRoute` contract with its post-chain point, and runtime routes. |
| RV-1, RV-2, and RV-7 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | Revisions and their field classes, revision selection, and session options validated against the chosen revision. |
| MP-3 and MP-4 | [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) | Elicitation per MCP version, including URL-mode forms for missing links, and the MCP Tasks extension for session progress. |
| CA-4 | [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-4-link-check) | The link check and the combined `LINKS_REQUIRED` response, with `deferConnectors` on session creation. |
| TS-1 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-1-environment-scoping) | Environment scoping, including the list of explicit paths that name an environment. |
| SO-5a | [`security-oversight-design.md`](security-oversight-design.md#so-5a-tool-use-approvals) | Tool-use approvals held at the gateway. |
| BR-2 | [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) | Session pod time reported per pod-binding window. |

## High-level design

An Anthropic SDK pointed at Lenny's gateway sees each Lenny runtime the caller may use as a model. A `POST /v1/messages` naming `support-agent` starts a session on the `support-agent` runtime, delivers the conversation, and returns the runtime's output as an assistant message. The client authenticates with the same Lenny bearer it would use for REST or MCP, carried in `Authorization: Bearer` or in `x-api-key`. Admission, quota, routing, revisions, link checks, billing, and audit apply as they do for any session, because the adapter creates sessions through the same service as `POST /v1/sessions/start`.

A Messages client resends the whole conversation on every request, and a Lenny runtime remembers it. The gateway reconciles them by recognising a conversation it has served. After each turn it stores a digest of the conversation up to and including the reply it returned. When a later request's history up to its last assistant message has the same digest, and the session that produced it still holds its pod, the gateway delivers only the new input to that session. Any other request starts a new session and gives the runtime the earlier conversation as history, with roles intact. Client-declared tools work on runtimes that declare them: the runtime's call to a client tool ends the HTTP response with `tool_use` blocks, and the client's `tool_result` in the next request resumes the same turn.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Endpoint (AM-1).** The adapter registers in the `ExternalAdapterRegistry` with the path prefix `/v1/messages`. It sits beside the OpenAI translators on the client listener and shares their single-shot binder, their holder forwarding, and their release path.

**Authentication and headers (AM-2).** The auth middleware reads a Lenny bearer from `x-api-key` on the adapter's routes as it reads `Authorization: Bearer`. `anthropic-version` must be `2026-10-21` or `2023-06-01` (O7). Any `anthropic-beta` value is refused.

**Request mapping (AM-3).** `model` names a runtime, or a scoped model namespace. Every request field and content block type has a stated disposition: mapped, forwarded to the runtime as an advisory parameter, accepted with no effect, or refused with `400 invalid_request_error`.

**Conversation input (AM-4).** The runtime's `message` envelope gains a `conversation` member carrying prior turns with roles, the client's instructions, per-turn parameters, and client tools. A runtime that declares `capabilities.conversationInput` receives it. Any other runtime receives the history and instructions as text parts ahead of the input (O3).

**Continuity (AM-5).** The gateway stores a conversation digest per completed turn, keyed by tenant and owner. A request whose history matches a digest of a running session, under the same conditions the Open Responses warm path checks, is a warm turn. Every other request is a cold turn (O2).

**Responses and streaming (AM-6).** Output parts become content blocks. A streaming request receives `message_start` at once, `ping` events while the turn runs, one content block per output part as the runtime emits it, and `message_delta` and `message_stop` at the end. A non-streaming request that waits past a limit receives `stop_reason: "pause_turn"`, and resending the conversation as-is resumes the wait (O8).

**Client tools (AM-7).** A request with client tools needs a runtime that declares `capabilities.clientTools`. The runtime calls a client tool with a `tool_call` frame, the gateway returns `tool_use` blocks with `stop_reason: "tool_use"`, and the next request's `tool_result` blocks reach the runtime as `tool_result` frames on the same turn (O4).

**Errors (AM-8).** Every refusal on the adapter's paths uses the Anthropic error envelope, with Lenny's error code and details as added members.

**Models and refused endpoints (AM-9).** `GET /v1/models` with an `anthropic-version` header answers in the Anthropic list format (O6). Batches, files, and token counting answer `404 not_found_error` in the Anthropic envelope.

**Interactions (AM-10).** Tenancy comes from the bearer, billing follows the session, `metadata.user_id` becomes a session label, the model resolves through runtime routes and revisions, and the MCP-specific features of the MCP upgrade have no counterpart on this surface.

```
 Anthropic SDK ──POST /v1/messages (Authorization: Bearer or x-api-key = Lenny bearer)──> gateway
        │
        ▼
 auth middleware ──> AnthropicMessagesAdapter: headers, field dispositions, content blocks
        │
        ├── history up to last assistant message ──digest──> conversation_turns lookup (tenant, owner)
        │        │
        │        ├── match, session running, newest turn, same generation ──> warm turn on held session
        │        │                                                            (forwarded to its coordinating replica)
        │        └── no match, or any warm condition fails ──> cold turn
        │                                                       │
        │                                                       ▼
        │                     shared create-and-start: admission, PreRoute chain, revision,
        │                     post-chain point (links, runtimeOptions, clientTools capability), pod claim
        ▼
 message envelope: input (new user content) + conversation (history, instructions, parameters, client tools)
        │
        ▼
 runtime turn ──output parts──> content blocks ──> JSON response, or SSE events
        │
        ├── response frame ──> stop_reason end_turn; digest stored; session held or released
        ├── client tool_call ──> stop_reason tool_use; open turn awaits tool_result
        ├── lenny/request_input ──> question as text, end_turn; open turn awaits the answer
        └── wait limit (non-streaming) ──> stop_reason pause_turn; open turn continues
```

"Limits" states what the design costs: a conversation whose history the client edits always runs cold, runtimes without conversation input see history as text, client tools need runtime changes, streams deliver whole parts, and a retried request after a dropped connection runs its turn again.

## Decisions

Settled by project rules:

| ID | Decision | Basis |
| :-- | :-- | :-- |
| D1 | The adapter is a built-in adapter in the `ExternalAdapterRegistry`, beside MCP and the OpenAI translators, and creates sessions through the shared create-and-start service. | §15 "Three tiers of pluggability" and the single-shot compute model. One creation path keeps admission and the post-chain point uniform. |
| D2 | The design adds no compatibility mode, legacy flag, or migration path for Lenny's own surfaces. The OpenAI translators change their history delivery in place (O3). | The project rule against compatibility shims before a first deployment (`feedback_no_backward_compat`). |
| D3 | A request field or content block that Lenny cannot honor is refused with `400 invalid_request_error` naming it, unless it is a hint with no effect on output semantics, such as `cache_control`. The existing translators' practice of decoding a request and dropping unknown fields is not copied. | Fail closed on behaviour a client relies on. A silently ignored `tools` array would return prose to a client that expects `tool_use`. |
| D4 | Every claim about the Messages API in this design comes from the pages under "Sources", read on 2026-11-09. | `feedback_verify_external_standard_versions`. |

Open for the owner. Each carries a recommendation:

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O1 | How the client presents its credential. | (a) `Authorization: Bearer` only. (b) `Authorization: Bearer` or `x-api-key`, each carrying a Lenny bearer. | (b). Anthropic documents `Authorization: Bearer` as the primary header and `x-api-key` as a supported legacy fallback **[external]**, and SDK users commonly configure an API key. A request carrying both with different values is refused with `401 authentication_error`. "AM-2" states the rules. |
| O2 | How conversation state is kept. | (a) Every request is a cold single-shot session that replays the history. (b) Warm continuation by conversation digest, with a cold fallback. (c) Explicit continuation through Anthropic's beta `thread` parameter (`message-threads-2026-08-12`) **[external]**. | (b). It serves every stock client with no Lenny-specific field, keeps the runtime's own state (workspace, tool results, memory) across turns, and falls back to a correct cold turn. (c) is a beta whose parameters can change, and "Alternatives rejected" records when to revisit it. |
| O3 | How earlier turns and client instructions reach the runtime on a cold turn. | (a) The current practice: each message is a separate turn with its role dropped. (b) Structured conversation input for runtimes that declare it, and transcript rendering for others. (c) Structured conversation input only, refusing history for runtimes without it. | (b), applied to the OpenAI translators in the same proposal. (a) re-runs earlier user turns and presents assistant replies as client input. (c) would refuse every cold continuation on most runtimes, including the continuation after an idle hold expires. |
| O4 | Whether client-declared tools work. | (a) Refuse any non-empty `tools` array. (b) Bridge client tools to runtimes that declare `capabilities.clientTools`, and refuse them for other runtimes. | (b), landed as the last proposal (AM-E). Programs that hand a model their own tools are a common use of the Messages API, and the runtime contract already has `tool_call` and `tool_result` frames to extend. Until AM-E lands, (a) holds. |
| O5 | What happens to model parameters (`max_tokens`, `stop_sequences`, `thinking`, `output_config`, and under `2023-06-01` `temperature`, `top_p`, and `top_k`). | (a) Refuse any non-default value. (b) Accept them and drop them. (c) Forward them to the runtime as advisory parameters in the conversation input, with the gateway enforcing none of them. | (c). An agent runtime decides how its model calls use them, and a runtime that cannot use one ignores it. The response header `lenny-dropped-parameters` names the parameters a runtime without conversation input never received. `max_tokens` stays required and validated, as Anthropic requires it. |
| O6 | How `GET /v1/models` serves both OpenAI and Anthropic clients on one path. | (a) Dispatch on the `anthropic-version` request header. (b) One response body carrying the fields of both formats. | (a). The Anthropic SDKs send `anthropic-version` on every request **[external]**, and dispatch lets each adapter publish its own `adapterCapabilities` in its own discovery output, which §9.1 requires. |
| O7 | Which `anthropic-version` values the adapter accepts. | (a) `2026-10-21` only. (b) `2026-10-21` and `2023-06-01`. | (b). Anthropic lists both as current versions, and SDK releases before the `2026-10-21` majors send `2023-06-01` **[external]**. The versions differ in request validation alone ("AM-2"). This mirrors the MCP upgrade's choice to serve the most recent protocol versions ([`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md), D1). |
| O8 | How a non-streaming request handles a turn that runs for minutes. | (a) Hold the HTTP request until the turn ends. (b) Answer `stop_reason: "pause_turn"` with the output so far after `anthropicMessages.maxResponseWaitSeconds`, and let the client resume by resending. | (b), with a default of 300 seconds. Anthropic recommends streaming for requests over 10 minutes **[external]**, intermediaries commonly close idle connections sooner, and `pause_turn` is the Messages API's own value for a paused long-running turn **[external]**. A client that ignores `pause_turn` sees a shorter reply. |
| O9 | Whether a model name that is not a runtime, such as an Anthropic model ID, can reach a runtime. | (a) No: `model` names a runtime or a scoped model namespace. (b) Through a runtime route whose requested name is the alias, when RT-4 allows a requested name that is no runtime. | (a) in this design, with (b) left to RT-4. A separate alias table would duplicate routes. "Instructions for reviewers of other designs" asks RT-4 to state whether a route's requested name must be a runtime. |
| O10 | Whether a request may carry Lenny-specific session fields (`runtimeOptions`, `labels`, and `deferConnectors`). | (a) No: the surface carries only Messages API fields. (b) A top-level `lenny` object, which the Anthropic SDKs can send through their extra-body option, holding those fields. | (b). Without it, a runtime whose revision schema requires options cannot be used through this surface, and a user with an unlinked connector cannot defer it. The object is part of the conversation digest. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| AMR-1 | A program written against an Anthropic SDK reaches a Lenny runtime by changing the base URL, the credential, and the model name. |
| AMR-2 | Every session the adapter creates passes the same admission, `PreRoute` chain, revision selection, and post-chain checks as `POST /v1/sessions/start`. |
| AMR-3 | A runtime never receives an earlier user turn as new input, and never receives an earlier assistant reply as client input with no role. |
| AMR-4 | A warm turn reaches a session only when the request's principal owns the session and the request's history matches the conversation the session produced, under the conditions of "AM-5". |
| AMR-5 | Every request field and content block type has a stated disposition, and a field Lenny cannot honor is refused. |
| AMR-6 | Every refusal on the adapter's paths uses the Anthropic error envelope and an HTTP status that Anthropic pairs with its error type. |
| AMR-7 | A client tool is called only on a runtime whose chosen revision declares `capabilities.clientTools`, and a client tool result reaches only the turn whose call it answers. |
| AMR-8 | The conversation digests and response records of a session are erased with the session under [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces). |
| AMR-9 | The adapter's sessions are billed, audited, and owned as any session is, and no request field changes the billed tenant or owner. |
| AMR-10 | The `model` field of a response is the request's `model` string, so a rollout or a route changes no response field. The session, runtime, and revision appear in response headers. |

## Current state

### Lenny

**Adapter inventory [source].** §15 "Built-in adapter inventory" lists `MCPAdapter` at `/mcp`, `OpenAICompletionsAdapter` at `/v1/chat/completions`, and `OpenResponsesAdapter` at `/v1/responses` for v1, and `A2AAdapter` and `AgentProtocolAdapter` for later. No specification section, document under `docs/api/`, or handler on the client listener names a client-facing Anthropic surface. `cmd/lenny-gateway/httpsurface.go` registers the OpenAI translators through `adapterregistry.NewSimpleAdapter` with their `gwadapter.Capabilities` and mounts the registry on the client mux.

**LLM proxy [source].** `cmd/lenny-gateway/llmproxy_server.go` mounts `POST /llm-proxy/v1/messages` on a separate server bound to `--llm-proxy-addr`. `pkg/gateway/llmproxy/llmproxy/translator.go` defines `DialectAnthropic` and the `anthropic_direct` translator, and `sse.go` reads Anthropic stream events for token usage. The proxy authenticates the pod's lease token and never sees a client bearer. `docs/getting-started/architecture.md` describes the proxy's path as `/v1/messages`, which a reader can mistake for a client surface.

**Translators [source].** `pkg/gateway/environment/translator` holds `OpenAIChatHandler` (`openai_chat.go`) and `OpenResponsesHandler` (`open_responses.go`). Both call `SingleShotBinder.BindSingleShot`, which `sessionSingleShotBinder` in `cmd/lenny-gateway/credentialsurface.go` implements over `sessionserver.CreateAndStartService`, and both release through `releaseSingleShot`. The binder reports `Hold` when the session's `conversationContinuity` is `"platform"` and its runtime declares `multi_turn` with injection. `OpenResponsesHandler` keeps response records on the session row (`Session.Responses`, `ContinuationParentID`), serves warm turns under the reservation and generation rules of §15 "Open Responses conversation sessions", forwards warm turns to the coordinating replica through `HolderForwarder`, and rebuilds a cold continuation's context from the transcript in `continuity.rehydrate`.

**Request handling [source].** `OpenAIChatCompletionsRequest` models `model`, `messages`, `stream`, and `user`, with `content` as a string. `OpenResponsesRequest` models `model`, `input`, `previous_response_id`, `stream`, and `user`. Both decode with `encoding/json` and no `DisallowUnknownFields`, so `tools`, sampling parameters, and multi-part content are dropped without an error. Both cap the body at 1 MiB. A model named `environments/{name}/{model}` scopes the session to an environment (`splitEnvModel`).

**Delivery to the runtime [source].** `executor.Executor.Send(ctx, sessionID, []Message)` is synchronous and returns the turn's output parts. `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go` writes each `Message` as its own `message` envelope with one text part and calls `runTurn` for each, so a request with several messages runs several turns. `Message.Role` is never written to the envelope: `resolveFromBlock` sets `from` to `client`/`client_gateway`, and no code in the executor package reads `Role`. The `MessageEnvelope` of §15.4.1 has no role field.

**Streaming [source].** `writeOpenAIStream` and `writeOpenResponsesStream` run after the turn has completed and split the text on whitespace into synthetic deltas. Usage counts in both translators are zeros.

**Authentication [source].** `pkg/gateway/middleware/auth/auth.go` reads `Authorization: Bearer` and, under dev mode only, the `X-Lenny-*` headers. It reads no `x-api-key`. §10.2 states that no dedicated API-key primitive exists in v1 and that a pasted "API key" in the playground is a Lenny bearer.

**Discovery [source].** `GET /v1/models` has one handler, `handleListModels` in `pkg/gateway/sessionserver/runtimes.go`, which returns OpenAI model objects for the runtimes the caller's environment access authorizes and selects the `adapterCapabilities` it reports by the `?adapter=` query parameter. §9.1 "Runtime Discovery" lists `GET /v1/models` for both OpenAI translators.

**Runtime tools and approvals [source].** §28.5.3 limits stdin `tool_call` and `tool_result` frames to adapter-local tools. Platform and connector tools travel over the runtime's MCP client. `PodExecutor.maybeGateToolCall` holds a runtime `tool_call` that carries `approvalRequired` until the §7.2 approval gate decides, then relays the call or a denial.

**Content types [source].** The §28.5.3 canonical type registry has translation columns for MCP, OpenAI, and A2A. The §15.4.1 translation fidelity matrix has columns for MCP, OpenAI Completions, Open Responses, REST, and A2A.

### Anthropic Messages API on 2026-11-09

**Versions [external].** The versioning page lists `2026-10-21` and `2023-06-01`. Under `2026-10-21`, a request that sends `temperature`, `top_p`, or `top_k` returns 400 on every model, a `document` block in an `assistant` message returns 400, and a `text` block with a non-empty `citations` array in a `user` message returns 400.

**Headers [external].** `Authorization: Bearer <token>` is required unless `x-api-key` is set, and the overview calls `x-api-key` a "Legacy fallback for `Authorization`, still supported". `anthropic-version` is required. `anthropic-beta` takes comma-separated beta names, and an unknown name returns `400 invalid_request_error`. Every response carries a `request-id` header, and error bodies repeat it as `request_id`.

**Request [external].** `model`, `messages`, and `max_tokens` are required. Message roles are `user`, `assistant`, and `system`. A final `assistant` message is a prefill. Optional fields are `cache_control`, `container`, `diagnostics`, `inference_geo`, `metadata` (`user_id`), `output_config` (`effort` and `format`), `service_tier`, `speed`, `stop_sequences`, `stream`, `system`, `thinking`, `tool_choice`, and `tools`. Beta-only fields include `context_management`, `compaction`, `mcp_servers`, and `thread`.

**Content and tools [external].** User-turn blocks are `text`, `image` (base64, URL, or file source), `document` (base64 PDF, plain text, content, URL, or file source), `search_result`, `thinking`, `redacted_thinking`, `tool_use`, `tool_result`, `server_tool_use`, the server tool result blocks, and `container_upload`. A client tool has `name` matching `^[a-zA-Z0-9_-]{1,128}$`, `input_schema`, and optional `description`, `strict`, and other fields. Server tools are typed, such as `web_search_20260318`, `code_execution_20260521`, `bash_20250124`, and `memory_20250818`. `tool_choice` is `auto`, `any`, `tool`, or `none`, with `disable_parallel_tool_use`.

**Response [external].** A message has `id`, `type: "message"`, `role: "assistant"`, `content`, `model`, `stop_reason` (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`, `refusal`, or `model_context_window_exceeded`), `stop_sequence`, `stop_details`, `container`, `diagnostics`, and `usage`. Output blocks include `text`, `thinking`, `redacted_thinking`, `tool_use`, and the server tool blocks. No output block carries an image.

**Streaming [external].** Named server-sent events, each with `data.type` equal to the event name: `message_start`, then per block `content_block_start`, `content_block_delta`, and `content_block_stop`, then `message_delta` with cumulative `usage`, then `message_stop`. `ping` can appear anywhere. A mid-stream error is an `error` event. Delta types are `text_delta`, `input_json_delta`, `citations_delta`, `thinking_delta`, and `signature_delta`. There is no `[DONE]` sentinel, and clients ignore event types they do not know.

**Errors [external].** 400 `invalid_request_error`, 401 `authentication_error`, 402 `billing_error`, 403 `permission_error`, 404 `not_found_error`, 409 `conflict_error`, 413 `request_too_large`, 429 `rate_limit_error`, 500 `api_error`, 504 `timeout_error`, and 529 `overloaded_error`. Other 4xx statuses use `invalid_request_error`. The SDKs retry connection errors, 429, and 5xx responses twice by default, honoring `retry-after`.

**Other endpoints [external].** `POST /v1/messages/count_tokens` returns `{input_tokens}`. `GET /v1/models` takes `after_id`, `before_id`, `limit`, and `lifecycle[]`, and returns `{data, first_id, has_more, last_id}` with `ModelInfo` objects (`type: "model"`, `id`, `display_name`, `created_at`, `capabilities`, and lifecycle fields). Message Batches live under `/v1/messages/batches`, and the Files API under `/v1/files`.

## AM-1 Endpoint and registration

The adapter registers in the `ExternalAdapterRegistry` with `PathPrefix: "/v1/messages"` and `Protocol: "anthropic-messages"` **[design]**. Its capabilities are `SupportsSessionContinuity: true`, `SupportsDelegation: false`, `SupportsElicitation: false`, and `SupportsInterrupt: false`. It declares `OutboundCapabilitySet.SupportedEventKinds` of `output` and `terminated`, which AM-6 reads during a streaming turn.

It serves these routes on the client listener:

| Route | Behaviour |
| :-- | :-- |
| `POST /v1/messages` | Creates a session or runs a turn ("AM-3" to "AM-7"). |
| `POST /v1/messages/count_tokens` | `404 not_found_error` ("AM-9"). |
| `/v1/messages/batches` and every path under it | `404 not_found_error` ("AM-9"). |

The path does not collide with the REST API: §15.1's message route is `POST /v1/sessions/{id}/messages`, and the LLM proxy's `/llm-proxy/v1/messages` is on another listener. The adapter reuses the translator package's single-shot binder, holder forwarder, session terminal surface, and release path, and it lives in the same package as the OpenAI translators, in its own files.

The request body cap is `anthropicMessages.maxRequestBytes`, default 32 MiB, operator-tunable **[design]**. The OpenAI translators' 1 MiB cap would refuse a single base64 screenshot. A larger body answers `413 request_too_large`.

## AM-2 Authentication and headers

**Credential [design, O1].** On the adapter's routes and on `GET /v1/models` and `GET /v1/models/{model_id}`, the auth middleware reads a bearer from `Authorization: Bearer` or from `x-api-key`. The `x-api-key` value is validated exactly as a bearer: a Lenny-issued JWT or an OIDC ID token, with the §10.2 tenant-claim extraction. A request with both headers and different values answers `401 authentication_error`. On every other route the middleware ignores `x-api-key`, so no other surface gains a second credential header. The gateway never logs the `x-api-key` value. A request with no credential answers in the Anthropic error envelope on these routes ("AM-8").

A Lenny bearer expires within `auth.capabilityTokenTtlSeconds` or the cap a `POST /v1/oauth/token` request sets. A client that stores a key in an environment variable needs to refresh it ("Limits").

**`anthropic-version` [design, O7].** The header is required. `2026-10-21` and `2023-06-01` are accepted, and any other value answers `400 invalid_request_error`. Under `2026-10-21` the adapter refuses `temperature`, `top_p`, and `top_k`, a `document` block in an `assistant` message, and a `text` block with a non-empty `citations` array in a `user` message, each with `400 invalid_request_error`, as Anthropic does. Under `2023-06-01` it accepts the sampling parameters and forwards them as advisory parameters (O5).

**`anthropic-beta` [design].** Any value answers `400 invalid_request_error` with a message naming the unsupported beta names. Lenny implements no beta feature, and the beta-only request fields are refused under "AM-3" in any case.

**`anthropic-workspace-id` [design].** A request that carries it answers `400 invalid_request_error`. Lenny has no workspaces, and the tenant comes from the bearer. An environment is named through the scoped model namespace ("AM-3").

**Response headers [design].** Every response carries `request-id: req_<opaque>`, a fresh identifier the gateway logs with the request. A response for a turn carries `lenny-session-id`, `lenny-runtime`, and `lenny-runtime-revision`, so a client or an operator can reach the session through REST or MCP, for example to approve a held tool call. `lenny-dropped-parameters` lists the request parameters a runtime without conversation input did not receive (O5).

## AM-3 Request mapping

### Model

`model` names the runtime **[design]**. A value of the form `environments/{name}/{runtime}` scopes the session to environment `{name}`, as `splitEnvModel` does for the OpenAI translators (§10.6). The bare runtime name is the requested runtime for the `PreRoute` chain, so a runtime route can send the session elsewhere and a revision is chosen on the final runtime (RT-2, RV-2). A name that resolves to no runtime in the caller's view answers `404 not_found_error`, the status Anthropic uses for an unknown model. A missing `model` answers `400 invalid_request_error`, and the adapter has no default runtime: the OpenAI translators' `DefaultRuntime` of `echo` would send a client's mistake to the echo runtime.

### Request fields

| Field | Disposition |
| :-- | :-- |
| `model` | Mapped to the requested runtime and environment, as above. |
| `messages` | Mapped to the conversation. "AM-4" and "AM-5" state how. A final `assistant` message answers `400 invalid_request_error` (prefill), unless it matches an open turn paused by `pause_turn` ("AM-6"). |
| `max_tokens` | Required, an integer of at least 1. Forwarded as `parameters.maxTokens` (O5). |
| `system` | Forwarded as `conversation.instructions`, as text parts (AM-4). |
| `stream` | Selects the streaming response ("AM-6"). |
| `stop_sequences` | Forwarded as `parameters.stopSequences`. |
| `thinking` | Forwarded as `parameters.thinking`. When its `type` is `enabled` or `adaptive`, output parts of type `reasoning_trace` are returned as `thinking` blocks, and otherwise they are omitted ("AM-6"). |
| `output_config.effort` | Forwarded as `parameters.effort`. |
| `output_config.format` | Forwarded as `parameters.outputSchema`. The gateway validates the final text block against the schema and answers `500 api_error` with code `OUTPUT_SCHEMA_MISMATCH` when it does not parse or match. |
| `tools` | Client tools: "AM-7". A tool with a server tool `type` (for example `web_search_20260318`, `code_execution_20260521`, `bash_20250124`, `text_editor_20250728`, `memory_20250818`, or `computer_toolset_20260801`) answers `400 invalid_request_error`. A runtime brings its own tools. |
| `tool_choice` | Forwarded with the client tools ("AM-7"). `{"type":"none"}` is accepted with or without tools. |
| `metadata.user_id` | Recorded as the session label `anthropic-user-id`. It never sets the owner, the tenant, the rollout key, or the billing subject ("AM-10"). |
| `cache_control` (top level and on blocks) | Accepted with no effect. It is a caching hint, and usage reports no cache tokens. |
| `service_tier` | `auto` and `standard_only` are accepted with no effect, because Lenny has one tier. `usage.service_tier` is `standard`. |
| `speed` | `standard` and null are accepted. `fast` answers `400 invalid_request_error`. |
| `inference_geo` | A non-null value answers `400 invalid_request_error`. Data residency in Lenny is a deployment and tenant property that a request cannot select. |
| `container` | A non-null value answers `400 invalid_request_error`. A runtime's workspace is the session's own. |
| `diagnostics` | Accepted. `diagnostics` in the response is null. |
| `temperature`, `top_p`, and `top_k` | Under `2023-06-01`, forwarded as advisory parameters. Under `2026-10-21`, refused as Anthropic refuses them. |
| `lenny` (O10) | An object with `runtimeOptions`, `labels`, and `deferConnectors`, mapped to the fields of the same names on the create-and-start request. It is part of the conversation digest. |
| Beta-only fields (`mcp_servers`, `context_management`, `compaction`, `thread`, `fallbacks`, `output_format`, and others) | `400 invalid_request_error`. |
| Any other field | `400 invalid_request_error` naming the field. |

### Content blocks

Each block maps to a `MessagePart` of the §28.5.3 registry **[design]**. Text parts carry their text inline. A `ref` to a URL is never fetched by the gateway, so the gateway makes no outbound request on a client's behalf, and the runtime fetches it, when it does, under its own egress policy.

| Block | In a `user` or `system` message | In an `assistant` message (history) |
| :-- | :-- | :-- |
| `text` | `text` part. | `text` part. |
| `image`, base64 source | `image` part, inline, with `mimeType`. | Refused, as Anthropic refuses it. |
| `image`, URL source | `image` part with the URL as `ref`. | Refused. |
| `image` or `document`, file source | Refused: Lenny serves no Files API. | Refused. |
| `document`, base64 PDF | `file` part, inline, `mimeType: application/pdf`, with `title` and `context` as annotations. | Refused under `2026-10-21`. |
| `document`, plain text or content source | `file` part with `mimeType: text/plain`, or `text` and `image` parts for a content source. | Refused under `2026-10-21`. |
| `document`, URL source | `file` part with the URL as `ref`. | Refused under `2026-10-21`. |
| `thinking` | Refused. | `reasoning_trace` part. The signature is not checked ("Limits"). |
| `redacted_thinking` | Refused. | Dropped from history. Lenny never issues one. |
| `tool_use` | Refused. | A client tool call in history ("AM-7"). |
| `tool_result` | A client tool result ("AM-7"). | Refused. |
| `search_result`, `server_tool_use`, server tool result blocks, and `container_upload` | Refused. | Refused. |

Large inline parts follow the §28.5.3 rule that stages a large blob to the artifact store and sets a `lenny-blob://` `ref`, so the transcript stays bounded.

## AM-4 Conversation input

### Envelope member

The `MessageEnvelope` of §15.4.1 gains an optional, gateway-written `conversation` member **[design]**:

```json
{
  "schemaVersion": 1,
  "type": "message",
  "id": "msg_01J9...",
  "from": { "kind": "client", "id": "client_gateway" },
  "sessionId": "sess_01J9...",
  "input": [{ "type": "text", "inline": "And the refund window for EU orders?" }],
  "conversation": {
    "instructions": [{ "type": "text", "inline": "Answer as Acme support. Be brief." }],
    "history": [
      { "role": "user", "content": [{ "type": "text", "inline": "What is our refund policy?" }] },
      { "role": "assistant", "content": [{ "type": "text", "inline": "Refunds are accepted within 30 days." }] }
    ],
    "parameters": { "maxTokens": 1024, "stopSequences": [], "thinking": { "type": "adaptive" } },
    "clientTools": [],
    "toolChoice": { "type": "auto" }
  }
}
```

- `history` holds the entries that precede `input` and that the runtime has not seen, in order, each with `role` (`user`, `assistant`, or `system`) and `content` as `MessagePart` entries. On a cold turn it is the whole earlier conversation. On a warm turn it holds the new segment's entries before its final user content, such as a mid-conversation `system` message, and is often absent.
- `instructions` holds the request's top-level `system` text. It is present on the first envelope of a session. The digest includes it, so it does not change on a warm turn.
- `parameters` holds the forwarded parameters of O5. It is present on every envelope the adapter sends.
- `clientTools` and `toolChoice` hold the client tools of AM-7.

A runtime treats every entry of `conversation` as client input. A `history` entry with `role: assistant` is the client's assertion of what an assistant said, because a Messages client controls its whole history. The gateway marks `from.kind: client` on the envelope as for any client message.

Other sources of a `message` envelope, such as REST delivery and MCP `lenny/send_message`, never set `conversation`.

### Capability

A runtime declares `capabilities.conversationInput: true` to receive the member **[design]**. The flag is a versioned field under RV-1, so the chosen revision decides. A per-tenant capability override can only clear it, as RV-1 lets an override narrow `capabilities.interaction`.

### Transcript rendering

For a revision that does not declare the flag, the gateway removes `conversation` and prepends text parts to `input` **[design, O3]**:

- When `instructions` is present, a part `Instructions from the client:` followed by the instruction text.
- When `history` is present, a part `Earlier conversation:` followed by one line per entry, `[user]`, `[assistant]`, or `[system]` and the entry's text, with each non-text part replaced by a line naming its type and `mimeType`.

The parts carry the annotation `lenny.conversationRendering: transcript`. `parameters` is dropped, and `lenny-dropped-parameters` lists the request parameters it held. A request with client tools on such a revision is refused before any pod is claimed ("AM-7").

### OpenAI translators

The same member and rendering replace one-turn-per-message delivery in `OpenAIChatHandler` and in `OpenResponsesHandler`'s cold path (O3). `PodExecutor.Send` and `SubprocessExecutor.Send` write one envelope per turn, carrying the turn's new user content in `input` and every earlier message in `conversation.history`. A Chat Completions request whose final message is not a `user` message answers `400 invalid_request_error`, because the turn has no new user input to deliver.

## AM-5 Continuity

### Conversation digest

The canonical form of a conversation prefix is the JSON serialization, with sorted keys and no insignificant whitespace, of an object with these members **[design]**:

- `tenant`, `owner` (the principal subject), and `model` (the request's `model` string, including any environment prefix).
- `system`, `tools`, `tool_choice`, and `lenny`, as the request carries them, after normalization.
- `messages`, the prefix being hashed, after normalization.

Normalization turns a string `content` into a one-element array of a `text` block, removes `cache_control` and `citations` from every block, removes `thinking` and `redacted_thinking` blocks, and drops fields whose value is null. Clients commonly omit earlier thinking blocks and re-serialize content arrays, and none of those changes alter what the runtime saw.

The digest is SHA-256 over the canonical form. Hashing a request's history to find its session is a lookup on equality, so the digest does not need to be secret. The tenant and owner inside it keep one user's history from matching another user's session.

### Storage

A table `conversation_turns` holds one row per completed turn **[design]**: `tenant_id`, `owner_subject`, `digest`, `session_id`, `ordinal`, and `created_at`, with a unique index on `(tenant_id, owner_subject, digest)` and a foreign key to the session that deletes the rows with it. The session row's `Responses` records of §15 "Open Responses conversation sessions" carry the ordinal, the end sequence, the recovery generation, and the completion flag for each turn, as they do for Open Responses. The §12.8 erasure of a session deletes its rows, and §12.8 names the table (AMR-8).

When a turn completes, the gateway writes the turn's digest, computed over the request's messages followed by the assistant message the gateway returned, in the same write that completes the response record. A unique-index conflict means another session produced an identical conversation for the same owner. The newer row replaces the older, so a continuation reaches the most recent session.

### Warm and cold

For a request whose `messages` contain an `assistant` message, the gateway computes the digest of `messages[0..k]`, where `k` is the index of the last `assistant` message, and looks it up under the request's tenant and owner **[design]**. The request is served as a warm turn on session `S` with ordinal `n` only when all of these hold, which are the conditions of §15 "Open Responses conversation sessions":

1. A row matches, naming `S` and `n`.
2. `S` is `running`.
3. Turn `n` is `S`'s newest response and has completed, or `n` is an open turn ("AM-6").
4. `S`'s `recovery_generation` equals the value recorded for turn `n`.
5. The request's principal is `S`'s owner, and the request's runtime and environment are those of the request that created `S`.

Before a warm dispatch, the gateway reserves ordinal `n+1` on `S` with one conditional write, as the Open Responses path does. A warm turn on a replica other than `S`'s coordinating replica is forwarded under §29.3 "Interactive message send", through the forwarding interface the Open Responses handler uses. Every other request is a cold turn: a request with no `assistant` message, no matching row, a failed condition, a lost reservation, or a warm dispatch that fails before input reaches the runtime. A cold turn creates a session through the shared create-and-start service and delivers `messages[0..m-1]` as `conversation.history` and the final user content as `input`, where `m` is the index of the final `user` message.

A warm turn's `input` is the final user message's content. Messages between `k` and the final user message go into `conversation.history`.

### Holding a session

After a turn completes, the session stays `running` and keeps its pod only under the Open Responses hold rule: `conversationContinuity: "platform"` and a revision that declares `multi_turn` with injection **[design]**. Every other session is released with the `completed` disposition before the response returns, and its next request is a cold turn. A held session is an ordinary session: `maxClientIdleSeconds`, idle suspension, `maxSessionAge`, and the admission limits govern it, and the adapter adds no timer. A session with an open turn is never released by the adapter, because the turn has not ended.

## AM-6 Responses and streaming

### Output mapping

| Output part | Content block |
| :-- | :-- |
| `text`, `code`, `diff`, `citation`, `error`, and `execution_result` (flattened) | `text`, one block per part. A `code` part's language and an `error` part's flag are dropped. |
| `reasoning_trace` | `thinking` when the request's `thinking.type` was `enabled` or `adaptive`, with a `signature` the gateway sets to an opaque value. Otherwise omitted. |
| `image`, `screenshot`, and `file` | A `text` block naming the part's type, `mimeType`, and size, because the Messages API has no output block for images or files. The part is reachable through `GET /v1/sessions/{id}/transcript`. |
| A custom type | `text`, per the registry's fallback rule. |

A `ref` is dereferenced inside the gateway and never returned, as §15.1 requires of external adapters.

**Message fields [design].** `id` is `msg_` followed by the session ID, a `.`, and the turn ordinal, and clients treat it as opaque. `model` is the request's `model` string (AMR-10). `role` is `assistant`. `stop_sequence`, `stop_details`, `container`, and `diagnostics` are null.

**`stop_reason` [design].** `end_turn` when the runtime's `response` frame ends the turn, or when a `lenny/request_input` opens a turn. `tool_use` when a client tool call opens a turn. `pause_turn` when the wait limit opens a turn. A runtime `response` with an `error` member is not a stop reason: it answers `500 api_error` with the runtime's code. The adapter never reports `max_tokens`, `stop_sequence`, `refusal`, or `model_context_window_exceeded`, because a turn's model calls are the runtime's own.

**`usage` [design].** `input_tokens` and `output_tokens` are the session's token counts accrued during the turn: the LLM proxy's extracted counts for a proxy-mode session (§4.9 "LLM Reverse Proxy", item 6), and the runtime's reported usage otherwise. They sum every model call of the turn. `cache_creation_input_tokens` and `cache_read_input_tokens` are 0, and `service_tier` is `standard`.

### Streaming

With `stream: true`, the response is `text/event-stream` with named events whose `data.type` equals the event name **[design]**:

1. `message_start` at once, with the message's `id`, `model`, empty `content`, and zero usage. The gateway sends it after the session is bound or the warm reservation is written, so an admission refusal is still a plain HTTP error.
2. `ping` every `anthropicMessages.pingIntervalSeconds`, default 15, operator-tunable, while no other event is due.
3. For each output part in order: `content_block_start`, one `content_block_delta`, and `content_block_stop`. A text block's delta is a `text_delta` with the part's whole text. A `thinking` block has a `thinking_delta` and then a `signature_delta`. A `tool_use` block has an `input_json_delta` with the whole input.
4. `message_delta` with `stop_reason` and the turn's usage, then `message_stop`.

Parts reach the adapter by either of these paths. Parts a Standard or Full runtime emits through `lenny/output` during the turn reach the adapter as `output` session events, and the adapter writes each as it arrives. Parts in the runtime's final `response` frame are written when the frame arrives. §28.5.3 states that the `response` frame holds only parts not already emitted through `lenny/output`, so no part is written twice. A failure after `message_start` is an `error` event carrying the Anthropic error object, followed by the end of the stream.

The OpenAI translators' practice of splitting finished text on whitespace is not copied. A Basic runtime, which has no `lenny/output`, streams all its blocks at the end of the turn, and the pings keep the connection open until then.

### Open turns

An open turn ends the HTTP response while the runtime's turn continues **[design]**. The session stays bound, the turn's response record stays not completed, and the gateway records the turn's digest over the request's messages followed by the assistant message it returned, marked open. A later request whose history matches an open turn's digest continues that turn under the warm conditions of AM-5.

- **Wait limit (O8).** A non-streaming request whose turn has not ended after `anthropicMessages.maxResponseWaitSeconds`, default 300, operator-tunable, receives the parts emitted so far with `stop_reason: "pause_turn"`. A request that resends the conversation with that assistant message last continues to wait on the same turn, and its response holds the parts emitted since. A streaming request has no wait limit.
- **`lenny/request_input`.** When the runtime calls `lenny/request_input` during a turn, the response carries the request's parts as text with `stop_reason: "end_turn"`. The next request's final user content is delivered as the answer to the pending request, as the §7.2 `input_required` path delivers an answer. `maxRequestInputWaitSeconds` bounds the wait, and its expiry fails the call with `REQUEST_INPUT_TIMEOUT` as §7.2 states.
- **Client tool call.** "AM-7".

When the client does not return, the turn completes on the runtime, the gateway completes the response record, and the session ends under its ordinary rules. A later request whose history matches a completed open turn is served as a warm or cold turn by AM-5, and the output parts the client never received reach it through the transcript only.

### Elicitation

The adapter declares `SupportsElicitation: false`. An elicitation that a session created by the adapter raises toward its client is answered with `decline` at once, and the adapter creates its sessions with `elicitationDepthPolicy: block_all` for delegated children, as §21.1 plans for the A2A adapter **[design]**.

## AM-7 Client tools

### Admission

A request with a non-empty `tools` array of client tools requires a chosen revision that declares `capabilities.clientTools: true` and `capabilities.conversationInput: true` **[design, O4]**. The post-chain point checks it, so a revision without the flags is refused with `400 invalid_request_error` before any pod is claimed. The single-shot spec gains `RequiresClientTools`, which the binder passes to the create-and-start request. A client tool whose `name` equals an adapter-local tool name (`read_file`, `write_file`, `list_dir`, or `delete_file`) or begins with `lenny` answers `400 invalid_request_error`.

### Delivery

`conversation.clientTools` carries each tool's `name`, `description`, `input_schema` as `inputSchema`, and `strict`. `conversation.toolChoice` carries `tool_choice`, including `disable_parallel_tool_use`. The runtime decides when to call a client tool.

### Call

The runtime calls a client tool with a stdin `tool_call` frame whose `name` names a client tool **[design]**. The frame gains `target: "client"`, which the adapter requires on a call to a client tool, and `endOfBatch: true`, which marks the last call of a parallel group. The adapter relays client calls to the gateway on the session's attach stream in place of resolving them locally, and validates `arguments` against the tool's `inputSchema`.

On `endOfBatch`, the gateway opens the turn: the response carries any text parts emitted so far followed by one `tool_use` block per call, with `id` set to `toolu_` and an opaque value the gateway maps to the runtime's call `id` on the session row, and `stop_reason: "tool_use"`.

### Result

The next request continues the open turn under AM-5. Its final `user` message must carry one `tool_result` block per pending `tool_use` id, and each is delivered to the runtime as a stdin `tool_result` frame with the runtime's call `id`, its content as `MessagePart` entries, and `isError` from `is_error`. A missing, extra, or unknown `tool_use_id` answers `400 invalid_request_error`. Text blocks in the same user message after the results are delivered as a `message` envelope once the results are written.

A pending client tool call waits under the session's `maxClientIdleSeconds`. A cold turn for a conversation whose last assistant message ends in `tool_use` delivers the calls and their results in `conversation.history`, so a runtime with conversation input continues from the history in a new session.

### Runtime tools

Runtime tools stay inside the turn. Their calls and results do not appear in the response, because the Messages API reserves `server_tool_use` for Anthropic's typed server tools, and an SDK client would read an unknown server tool name as a defect. They remain visible through the session's transcript and its MCP and REST event streams. A runtime tool call held for approval under §7.2 or SO-5a holds the turn. The approver resolves it through the approvals API with the `lenny-session-id` the response headers carry, and a non-streaming request reaches its wait limit in the meantime.

## AM-8 Errors

Every refusal on the adapter's routes uses the Anthropic error envelope with added members **[design]**:

```json
{
  "type": "error",
  "error": {
    "type": "permission_error",
    "message": "the session needs linked accounts: connector crm",
    "code": "LINKS_REQUIRED",
    "details": { "connectors": ["crm"], "userLinks": [] }
  },
  "request_id": "req_01J9..."
}
```

`code` is the Lenny error code and `details` its details object, both omitted when absent. The SDKs read `type` and `message` and keep unknown members.

| Lenny outcome | HTTP status | `error.type` |
| :-- | :-- | :-- |
| 400 and 422 validation errors | 400 | `invalid_request_error` |
| 401, including no credential and an invalid bearer | 401 | `authentication_error` |
| 403, including `LINKS_REQUIRED`, `TENANT_NOT_FOUND`, and impersonation refusals | 403 | `permission_error` |
| 404, including an unknown runtime | 404 | `not_found_error` |
| 409, including `TARGET_NOT_READY` from a warm forward | 409 | `conflict_error` |
| 413 | 413 | `request_too_large` |
| 429 and the admission rate limits, with `Retry-After` | 429 | `rate_limit_error` |
| 503 `SESSION_CREATION_FAILED` and `POOL_DRAINING`, with `Retry-After` | 503 | `overloaded_error` |
| 503 `CREDENTIAL_POOL_EXHAUSTED` | 503 | `overloaded_error` |
| 504, including `REQUEST_INPUT_TIMEOUT` | 504 | `timeout_error` |
| Any other 5xx | 500 | `api_error` |

The table keeps Lenny's 503 for a claim failure in place of Anthropic's 529 for `overloaded_error`, because 503 is the status other Lenny surfaces and load balancers expect for the same condition, and the SDKs retry both. A link refusal carries `details` as CA-4 defines it, so a client that cannot pass `lenny.deferConnectors` can show the links to make.

The auth middleware answers an unauthenticated request on the adapter's routes in this envelope rather than Lenny's REST envelope, by reading the protocol the route's adapter declares.

## AM-9 Models and refused endpoints

**`GET /v1/models` [design, O6].** A request carrying `anthropic-version` receives the Anthropic list: `data` as `ModelInfo` objects with `type: "model"`, `id` (the runtime name), `display_name` (the runtime's display name, or its name), and `created_at` in RFC 3339, and `first_id`, `has_more`, and `last_id`. `after_id`, `before_id`, and `limit` (default 20, at most 1000) page by runtime name. `lifecycle[]` is accepted, and every runtime is `active`. `capabilities` reports `supported: true` for `image_input`, `pdf_input`, and `structured_outputs`, and `supported: false` for the remaining capabilities, since the gateway cannot state what a runtime's model accepts. The body also carries `adapterCapabilities` of the Anthropic adapter, as §9.1 requires. A request without the header receives the OpenAI list as today. The runtimes listed are the runtimes the caller may use, filtered as `handleListModels` filters them.

**`GET /v1/models/{model_id}` [design].** Returns one `ModelInfo`, or `404 not_found_error` for a runtime outside the caller's view, identical to an unknown name.

**Refused endpoints [design].** `POST /v1/messages/count_tokens`, every path under `/v1/messages/batches`, and every path under `/v1/files` answer `404 not_found_error` in the Anthropic envelope with a message naming the endpoint as not served by Lenny. `/v1/files` is mounted only for requests carrying `anthropic-version`, so no REST route is shadowed.

## AM-10 Tenancy, billing, routing, and other designs

**Tenancy [design].** The tenant comes from the bearer under §10.2. The scoped model namespace selects an environment, and TS-1's list of explicit paths gains it on `/v1/messages`. The digest includes tenant and owner, so continuity never crosses either.

**Billing [design].** The adapter creates ordinary sessions. Billing events, the session's usage, and BR-2's pod windows attribute to the session's tenant and owner. A held session's pod time between turns is billed as any held session's is. `metadata.user_id` becomes the session label `anthropic-user-id`, which usage reports and billing filters can group by, and it changes no attribution. The adapter emits no billing event of its own.

**Routing and revisions [design].** The requested runtime enters the `PreRoute` chain as for any create, and a route or interceptor can change it. A cold turn of an existing conversation is a new create and can land on another target or revision, under the rollout key of RV-2, which defaults to the user and so keeps a user on one revision while the traffic is unchanged. A warm turn stays on its session's runtime and revision. `runtimeOptions` reach the create only through `lenny.runtimeOptions` (O10), so a revision whose schema requires options refuses a request without them at the post-chain point, under RV-7. The flags `conversationInput` and `clientTools` are read from the chosen revision.

**MCP upgrade [design].** The adapter has no MCP features. MP-3's URL-mode elicitation for missing links has no counterpart, and a link refusal is the `permission_error` of AM-8. MP-4's Tasks progress has no counterpart beyond streaming. The Messages API's beta `mcp_servers` and the `mcp_toolset` tool are refused, because connectors are registered by administrators under CA-4. A runtime's own MCP servers and clients are unaffected.

**Connector authorization [design].** The link check runs at the post-chain point of a cold turn, as for any create. A client sends `lenny.deferConnectors` to defer a connector.

**Security oversight and impersonation [design].** Tool-use approvals hold runtime tool calls as on any surface. Today the OpenAI translators admit an impersonation bearer on session input, which [`user-credential-delivery-design.md`](user-credential-delivery-design.md) records as F-13.3.26. Whatever rule closes that finding applies to this adapter in the same change, and the adapter joins the finding's list of surfaces.

## Worked example

Alice, a user of tenant `acme`, uses the Anthropic Python SDK with `base_url` set to the gateway and her Lenny bearer as the API key. The runtime `support-agent` declares `multi_turn` with injection and `conversationInput`, and its pool has `conversationContinuity: "platform"`.

1. Alice sends `model: "support-agent"`, `system: "Answer as Acme support."`, `max_tokens: 1024`, and one user message, "What is our refund policy?". The history holds no assistant message, so the gateway creates session `S`. The `PreRoute` chain leaves the runtime unchanged, revision 7 is chosen, and the link check passes. The runtime receives `input` with the question and `conversation.instructions` with the system text. It answers "Refunds are accepted within 30 days." The gateway stores the digest of the conversation including that reply, keeps `S`, and returns `id: "msg_S.1"` with `lenny-session-id: S` and `lenny-runtime-revision: 7`.
2. Alice sends the question, the reply, and a new user message, "And for EU orders?". The digest of the history up to the reply matches `S`'s turn 1. The gateway reserves turn 2 and delivers only "And for EU orders?". The runtime answers from its own context.
3. Alice is idle past `maxClientIdleSeconds`, and `S` is suspended and released. She sends the conversation with a further question. The digest matches `S`, but `S` is not `running`, so the gateway creates session `S2` and delivers the earlier messages as `conversation.history`. The runtime starts from the history.
4. Bob, also of `acme`, sends the same text as Alice's step 2. His owner differs, so no digest matches, and he receives a cold turn in a session of his own.

## Limits

- **Edited history runs cold.** A client that edits, summarizes, or reorders earlier messages, or changes `system` or `tools`, produces no digest match, and every such request creates a session. A client that compacts its history on each request, as long-running agents do, never reuses a session.
- **Transcript rendering loses structure.** A runtime without conversation input receives history as labelled text and never receives the forwarded parameters. Non-text parts in history appear as a line naming their type.
- **Client tools need runtime work.** A runtime must declare `clientTools`, emit `target: "client"` and `endOfBatch`, and handle results arriving across HTTP requests.
- **Streaming is per part.** A text block arrives as one delta when the runtime emits the part. A Basic runtime streams nothing but pings until its turn ends.
- **Retries repeat work.** The SDKs retry a dropped connection, a 429, and a 5xx twice by default. A retried first request has no assistant message to match, so it creates a second session and runs the turn again, with any side effects the runtime's tools have. Streaming reduces dropped connections.
- **Thinking signatures are not verified.** A `thinking` block in history is accepted with any signature, and a Lenny-issued signature is not valid at Anthropic's API. A conversation started on Lenny cannot continue on Anthropic's API with its thinking blocks.
- **Usage sums model calls.** `usage` reports every model call of the turn, which is the cost a client pays for the turn and differs from one model call's prompt size.
- **Short-lived credentials.** A Lenny bearer expires, so a program that reads a static key from its environment fails at expiry until it obtains a new one.
- **`pause_turn` relies on the client.** A client that treats `pause_turn` as final receives a partial reply, and the turn's later output reaches it only through the transcript.

## Alternatives rejected

- **Pass the request through to Anthropic's API.** The LLM proxy can already call Anthropic for a pod. Exposing it to clients would make Lenny a model gateway with no runtime, workspace, or session, which the project's decision against a gateway LLM sidecar excludes.
- **Explicit continuation through the beta `thread` parameter.** `thread: {"type": "continue", "previous_message_id": ...}` maps onto held sessions directly **[external]**. It is a beta whose fields can change before general availability, and stock clients do not send it. Revisit when Anthropic marks it stable, as an addition to digest matching.
- **A Lenny header naming the session.** Clients would need custom code, and a digest gives the same result for stock clients.
- **Reporting runtime tool calls as `server_tool_use` blocks.** The SDKs type `server_tool_use` names to Anthropic's server tools, and Lenny's tool calls would need translation to fake names.
- **Hosting Message Batches as asynchronous sessions.** It needs results storage, expiry, and cancellation semantics of its own, and no request asked for it.
- **A union body for `GET /v1/models`.** It would carry one `adapterCapabilities` for both adapters.
- **Token counting by estimate.** The gateway does not know the runtime's model, tokenizer, or prompt, and a wrong count misleads cost checks.

## Specification changes

| Section (by heading) | Change |
| :-- | :-- |
| §15 External API Surface, "Built-in adapter inventory" and "Three tiers of pluggability" | Add `AnthropicMessagesAdapter` at `/v1/messages`, protocol `anthropic-messages`, V1. Add a paragraph "Anthropic Messages conversation sessions" stating AM-3 to AM-7, in the form of "Open Responses conversation sessions", and reference that paragraph's warm conditions and reservation write. Add the protocol identifier to the `AdapterCapabilities.Protocol` examples. |
| §15 External API Surface, "Built-in adapter single-shot compute model" | State that a session with an open turn is never released by an adapter. |
| §15.1 REST API | Add `OUTPUT_SCHEMA_MISMATCH` to the error catalog. `GET /v1/models` answers in the Anthropic format when the request carries `anthropic-version`. Add `GET /v1/models/{model_id}`. |
| §15.2.1 REST/MCP Consistency Contract | Add the adapter to the contract-testing list of built-in adapters. |
| §15.4.1 Message Format and Binary I/O Requirements, "Translation Fidelity Matrix" and "`MessageEnvelope` — Unified Message Format" | Add the Anthropic Messages column. Add the `conversation` member and its rules from AM-4. |
| §15.5 API Versioning and Stability | State the accepted `anthropic-version` values and the rule for adopting a new Anthropic version. |
| §5.1 Runtime | Add `capabilities.conversationInput` and `capabilities.clientTools`, as versioned fields. |
| §28.5.3 Intra-pod | Add `target` and `endOfBatch` to `tool_call`, the relay of client tool calls in the tool access table, and the Anthropic column of the canonical type registry. |
| §7.2 Interactive Session Model | State the open-turn answer path for `lenny/request_input` under the adapter. |
| §9.1 Runtime Discovery | Add the Anthropic entry. |
| §10.2 Authentication | State `x-api-key` on the adapter's routes and the models routes, and the conflicting-headers refusal. |
| §10.6 Environment Resource and RBAC Model | Add the scoped model namespace on `/v1/messages`. |
| §12.8 Compliance Interfaces | Name `conversation_turns` in the session erasure. |
| §29.3 Interactive message send | Cover the forwarded warm Messages turn. |
| §15.6 Client SDKs | State that the Anthropic SDKs work against the gateway with a Lenny bearer. |

## Non-specification changes

- `pkg/gateway/environment/translator`: the adapter in its own files, and the shared conversation input builder and transcript renderer that the OpenAI translators use as well.
- `pkg/gateway/session/executor`: one envelope per turn with `conversation`, client tool relay, and open-turn handling.
- `pkg/gateway/middleware/auth`: `x-api-key` on the adapter's routes, and the protocol-aware unauthenticated envelope.
- `pkg/gateway/sessionserver/runtimes.go`: the Anthropic models format and the single-model route.
- The runtime adapter and the runtime SDKs under `sdks/`: the `conversation` member, the capability flags, and the client tool frames.
- `migrations/`: `conversation_turns`.
- `cmd/lenny-gateway`: registration, and flags with Helm values for `anthropicMessages.maxRequestBytes`, `pingIntervalSeconds`, and `maxResponseWaitSeconds`.
- `docs/api/anthropic-messages.md`, a tutorial for the Anthropic SDKs, and a correction to `docs/getting-started/architecture.md` that names the LLM proxy's path as `/llm-proxy/v1/messages`.

## Defects found while verifying

These are defects in the current tree. They are recorded here and are not filed, because this design edits no other file. Each needs a `BUILD-GAPS.md` finding when the owner reviews the design.

| ID | Defect | Evidence |
| :-- | :-- | :-- |
| DF-1 | Every message of a translator request reaches the runtime as its own turn, with its role dropped. A Chat Completions request with a system message and history runs one turn per message, and each prior assistant reply arrives as client input. | `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go` calls `runTurn` per message, and `resolveFromBlock` ignores `Message.Role`. |
| DF-2 | A cold Open Responses continuation re-runs every earlier user turn of the conversation on the new session. | `serveCold` sends `append(prior, t.msgs...)` through `exec.Send`, with `prior` from `continuity.rehydrate`. |
| DF-3 | The OpenAI translators drop `tools`, sampling parameters, and non-string content without an error, so a client that sends tools receives prose. | `OpenAIChatCompletionsRequest` and `OpenResponsesRequest` model neither field, and decode without `DisallowUnknownFields`. |
| DF-4 | A Chat Completions or Open Responses request with an empty `model` runs on the `echo` runtime, in production as in dev mode. | `NewOpenAIChatHandler` and `NewOpenResponsesHandler` default `DefaultRuntime` to `echo`, the handlers use it when `model` is empty, and no file under `cmd/lenny-gateway` sets `DefaultRuntime`. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Field and block dispositions for both API versions, canonical form and digest, transcript rendering, output mapping, error mapping, and `tool_use` id mapping. |
| 3 | The official Anthropic Python, TypeScript, and Go SDKs at their current majors, and one release that sends `2023-06-01`, run against the gateway for create, streaming, tool use, errors, and the models routes. Recorded SSE streams are checked against the event order of AM-6. The envelope's `conversation` member against the runtime SDKs. |
| 4 | Warm and cold turns on the compose stack, a digest after an idle release, owner and tenant separation, `lenny.runtimeOptions`, a link refusal, and the OpenAI translators' history delivery. |
| 5 | A held session on Kind through several turns, an open turn by `pause_turn`, a client tool round trip, and `lenny/request_input` through the adapter. |
| 7a | Concurrent continuations of one conversation: one warm reservation wins and the other goes cold. Concurrent digest writes for identical conversations. |
| 8 | Pod loss during an open turn and during a pending client tool call, with the next request served cold from history. Replica loss during a forwarded warm turn. |
| 9 | `x-api-key` validation, conflicting credential headers, a digest match across owners and tenants, an impersonation bearer, and log redaction of `x-api-key`. |
| 10 | Conformance cases for `conversationInput` and `clientTools`: history roles, instructions, client tool frames, and `endOfBatch`. |
| 11 | The new API page and tutorial against the gateway. |

## Validation owed

- Which header each current Anthropic SDK sends when configured with an API key, and whether the SDKs send `anthropic-version` on the models routes. "Sources" records the documentation only.
- Whether the SDKs' typed parsers accept the `code` and `details` members in an error object, and an empty `signature` value.
- The label key rules of §14 for `anthropic-user-id`.
- Whether the session event bus delivers `output` events to an adapter on the replica serving the request during a forwarded warm turn.
- Whether `runtimecapoverride` can clear a capability flag that a revision declares, as AM-4 requires.

## Instructions for reviewers of other designs

- [`runtime-routing-design.md`](runtime-routing-design.md): RT-4 should state whether a route's requested name must be a registered runtime (O9). The `PreRoute` payload's input for a create from this adapter is the final user content, with history in a separate member that an interceptor cannot change.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): RV-1's field classes should list `capabilities.conversationInput` and `capabilities.clientTools` as versioned, gateway-read fields.
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): TS-1's explicit paths and the list of root creators that write a resolved delegation value gain `/v1/messages`.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): the "Clients" list under CA-4 gains the Anthropic SDKs, which can defer through `lenny.deferConnectors`.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md): no change. The adapter offers no MCP feature.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): no change. Held sessions and their pod windows are ordinary sessions.
- [`security-oversight-design.md`](security-oversight-design.md): SO-5a's approvers act on sessions this adapter creates through the session ID in the response headers.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| AM-A | Conversation input (AM-4) for the runtime contract, and the OpenAI translators moved onto it, closing DF-1 and DF-2. | None. |
| AM-B | The adapter's endpoint, authentication, headers, request mapping, errors, models routes, and refused endpoints (AM-1, AM-2, AM-3, AM-8, and AM-9), with cold single-shot turns and streaming at the end of a turn. | AM-A. |
| AM-C | Continuity (AM-5): digests, warm turns, holding, forwarding, and erasure. | AM-B. |
| AM-D | Incremental streaming and open turns by `pause_turn` and `lenny/request_input` (AM-6). | AM-C. |
| AM-E | Client tools (AM-7). | AM-D. |

AM-A also closes DF-3, by refusing the fields the OpenAI translators cannot honor under D3, and DF-4, by refusing a request with no `model`.

## Sources

Each page below was read on 2026-11-09.

- [Messages: create](https://platform.claude.com/docs/en/api/messages/create), the request, content blocks, tools, and response object. `https://docs.anthropic.com/en/api/messages` redirects to `https://platform.claude.com/docs/en/api/messages`.
- [Beta Messages: create](https://platform.claude.com/docs/en/api/beta/messages/create), for the beta-only fields, `thread`, and `mcp_servers`.
- [Versions](https://platform.claude.com/docs/en/api/versioning) and [Migrating to API version 2026-10-21](https://platform.claude.com/docs/en/api/migration-2026-10-21).
- [API overview](https://platform.claude.com/docs/en/api/overview), for the headers table, and [Authentication](https://platform.claude.com/docs/en/manage-claude/authentication).
- [Beta headers](https://platform.claude.com/docs/en/api/beta-headers).
- [Errors](https://platform.claude.com/docs/en/api/errors), for error types, `request-id`, retries, and long requests.
- [Streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming) and [Citations](https://platform.claude.com/docs/en/build-with-claude/citations), for `citations_delta`.
- [Mid-conversation system messages](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages).
- [Count tokens](https://platform.claude.com/docs/en/api/messages/count_tokens).
- [List models](https://platform.claude.com/docs/en/api/models/list) and [Get a model](https://platform.claude.com/docs/en/api/models/retrieve).
- [Message Batches](https://platform.claude.com/docs/en/api/messages/batches).
- [Files](https://platform.claude.com/docs/en/build-with-claude/files), [Structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs), and [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).
- [`message_create_params.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/message_create_params.py) in `anthropic-sdk-python` on `main`, which confirms that the sampling parameters are gone from the current SDK.

## Review history

No review has read this design.

### Revision notes

- Revision 1, 2026-11-09: first draft, written against the tree and specification on branch `design/v1-cleanup` and the Anthropic documentation of the same date.
