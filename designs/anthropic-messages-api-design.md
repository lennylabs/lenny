# Anthropic Messages API compatibility

Status: design for owner review, revision 5, 2026-11-10, after one adversarial review. Nothing here is built or staged. It reads against [`runtime-routing-design.md`](runtime-routing-design.md) revision 12, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 10, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 65, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`security-oversight-design.md`](security-oversight-design.md) revision 47, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) revision 11, and [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 26. "Decisions" lists what the project rules and the owner settle and what remains open. The design stages no specification edit. The changes go through `change-proposal` after review. Parts of the design carry the label AM-n, requirements carry the label AMR-n, and the proposals that land them carry AM-A to AM-C under "Proposal split and order". "Review history" at the end records the revisions.

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
- A Messages client declares tools, and the model returns `tool_use` blocks that the client executes. A Lenny runtime runs its own tools inside a turn, and the runtime contract has no path that sends a tool call to the client ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod), "Tool access by level") **[source]**. [`client-tools-design.md`](client-tools-design.md) adds that path for every client surface.
- A Messages request carries model parameters such as `max_tokens`, `thinking`, and `stop_sequences` that address one model call. A Lenny turn can make many model calls, or none.

Verifying the existing translators against the tree found defects that this surface would inherit if it copied them ("Defects found while verifying"). The most consequential is that every message in a request's history reaches the runtime as a separate client turn with its role dropped, so a cold continuation re-runs earlier user turns and presents earlier assistant replies as client input.

## Scope

**Added:**

- A built-in external protocol adapter, `AnthropicMessagesAdapter`, serving `POST /v1/messages` on the client listener ("AM-1").
- Acceptance of a Lenny bearer in `Authorization` or `x-api-key` on the adapter's routes, normalized into one credential, a specific refusal of an Anthropic API key, and the rules for `anthropic-version` and `anthropic-beta` ("AM-2").
- The mapping of a Messages request to a session creation or a turn, with a disposition for every request field and content block type, and parameters that apply only where the runtime declares them ("AM-3").
- A conversation input member on the runtime's `message` envelope, which carries the unseen conversation as ordered entries with the new input marked, client instructions, and per-turn parameters, and a runtime capability that declares it ("AM-4").
- Warm continuation of a conversation by a keyed digest of its history, a join that reattaches a retried request to its running turn, and a cold fallback that rebuilds the conversation in a new session ("AM-5").
- A per-request record that links the `request-id` Lenny returns to the session and the turn ("AM-5").
- The response mapping and Anthropic server-sent events ("AM-6").
- The mapping of client tools on this surface onto the mechanism of [`client-tools-design.md`](client-tools-design.md) ("AM-7").
- The Anthropic error envelope with Lenny's code in `error.details.error_code`, and `x-should-retry` on failures a retry would repeat ("AM-8").
- The Anthropic form of `GET /v1/models` and `GET /v1/models/{model_id}`, and explicit refusals for the Anthropic endpoints Lenny does not serve ("AM-9").
- The rules for tenancy, billing, routing, revisions, and the MCP upgrade on this surface ("AM-10").

**Changed:**

- The OpenAI translators deliver history through the conversation input member of AM-4 in place of one turn per message (O3).
- `GET /v1/models` answers in the format that the request's `anthropic-version` header or `?adapter=` parameter selects ("AM-9").
- The MessagePart canonical type registry and the translation fidelity matrix gain an Anthropic Messages column.

**Removed:**

- The OpenAI translators' delivery of one turn per history message, and their `echo` default runtime for a request with no `model` (DF-1, DF-2, and DF-4, filed as F-15.1.80, F-15.1.81, and F-15.1.83).

**Kept:**

- The shared create-and-start service behind every built-in adapter, so admission, quota, the `PreRoute` chain, revision selection, and the post-chain point apply unchanged.
- The session as the unit of billing, ownership, erasure, and audit.
- The LLM proxy's `anthropic` dialect and its listener.

**Out of scope:**

- Message Batches, the Files API, Skills, and the Admin API. AM-9 refuses their paths.
- Token counting (`POST /v1/messages/count_tokens`). A runtime's tokenizer and prompt are unknown to the gateway.
- Token-level streaming. The runtime contract delivers output in parts, and AM-6 streams one delta per part.
- A turn that outlives its HTTP response. Revision 2's open turns are removed (O4). A long turn uses streaming.
- `lenny/request_input` and elicitation toward the client of this surface. Both are refused at once ("AM-6").
- The beta `thread` parameter. It stays refused, and "Alternatives rejected" records how it maps once it is stable.
- A long-lived API key primitive for Lenny. [§10.2](../spec/10_gateway-internals.md#102-authentication) states that no dedicated API-key primitive exists in v1, and adding one belongs to the identity design. "Instructions for reviewers of other designs" asks that design for a `jwt-bearer` grant that the SDKs' workload identity federation can use (M8).

## Terms

| Term | Meaning |
| :-- | :-- |
| Messages API | Anthropic's `POST /v1/messages` and the endpoints around it, at API version `2026-10-21` unless a statement names `2023-06-01`. |
| Adapter | `AnthropicMessagesAdapter`, the built-in external protocol adapter this design adds. Its protocol identifier is `anthropic-messages`. |
| Conversation | The sequence of `system`, `user`, and `assistant` messages a Messages request carries, together with its top-level `system` instructions and `tools`. |
| Turn | One request's new input delivered to a runtime, and the runtime's output up to its `response` frame. A turn ends inside the HTTP request that started it. |
| New input | The entries of a request after its last `assistant` message: the final user content and any `system` message that follows it. |
| Warm turn | A turn delivered to a session that already holds the conversation, so only the new input reaches the runtime. |
| Cold turn | A turn that creates a new session and delivers the earlier conversation as history before the new input. |
| Conversation digest | An HMAC-SHA256 value over the canonical form of a conversation prefix, keyed by a gateway secret, which identifies the session that produced it. "AM-5" defines the canonical form. |
| Request digest | The same keyed digest over a request's whole `messages` array, which identifies a retry of a request whose turn is still running ("AM-5"). |
| Request record | The row that links the `request-id` the gateway returns to the tenant, the owner, the session, the turn, and the request digest ("AM-5"). |
| Conversation input | The `conversation` member that AM-4 adds to the runtime's `message` envelope: ordered entries with the new input marked, instructions, parameters, and the client tool members of [`client-tools-design.md`](client-tools-design.md). |
| Transcript rendering | The fallback that renders history and instructions as text parts for a runtime that does not declare conversation input. |
| Enforced parameter and hint | A request parameter that changes what the client receives, such as `max_tokens` or `stop_sequences`, is an enforced parameter, and reaches only a runtime that declares it. A parameter that only guides the runtime's model calls, such as `output_config.effort` or `thinking.type`, is a hint, and reaches every runtime that declares conversation input ("AM-3"). |
| Client tool | A tool that the request's `tools` array declares and that the client executes, under [`client-tools-design.md`](client-tools-design.md). |
| Runtime tool | A tool the runtime calls inside its turn: an adapter-local tool, a platform MCP tool, or a connector tool. |
| Anthropic error envelope | `{"type":"error","error":{"type":...,"message":...,"details":{...}},"request_id":...}` with the HTTP status Anthropic pairs with each error type **[external]**. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). Each runtime the caller may use appears as a model on the OpenAI translators today, and this design does the same for the Messages API. |
| Alias | A runtime route whose requested name is no runtime, such as an Anthropic model ID ([RT-4](runtime-routing-design.md#rt-4-runtime-routes), **Aliases.**). A request that names the alias runs on the route's target runtimes. |
| Session | Lenny's unit of work: one runtime instance bound to a pod, with an owner, a tenant, a transcript, and billing events ([§7](../spec/07_session-lifecycle.md)). |
| Built-in adapter single-shot compute model | The §15 rule that an OpenAI Chat Completions request runs an implicit session that claims a warm pod, runs one turn, and releases the pod within one HTTP call. |
| Held session | A session the Open Responses adapter keeps after a successful turn, when its `conversationContinuity` is `"platform"` and its runtime declares `capabilities.interaction: multi_turn` with `capabilities.injection.supported: true` ([§15](../spec/15_external-api-surface.md#15-external-api-surface), "Open Responses conversation sessions"). |
| `PreRoute` chain and post-chain point | The interceptor phase that chooses a session's runtime and pool, and the single step after it, and after revision selection, where every check that depends on the runtime, the revision, or the pool runs ([`runtime-routing-design.md`, RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract)). |
| Runtime route | A tenant-scoped admin resource that maps a requested runtime name to weighted targets ([`runtime-routing-design.md`, RT-4](runtime-routing-design.md#rt-4-runtime-routes)). |
| Revision | An immutable, numbered copy of a runtime's versioned fields, `capabilities` among them, which the gateway chooses per session by the runtime's traffic weights ([`runtime-revisions-design.md`, RV-2](runtime-revisions-design.md#rv-2-selection)). |
| `runtimeOptions` | The session field that carries runtime-specific options, validated against the chosen revision's `runtimeOptionsSchema` at the post-chain point ([`runtime-revisions-design.md`, RV-7](runtime-revisions-design.md#rv-7-session-options)). |
| `MessageEnvelope` and `MessagePart` | The runtime-facing message format and its content parts ([§15.4.1](../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format", and [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). The envelope names its sender in `from` and carries no role. |
| Client tools mechanism | The turn-ending mechanism of [`client-tools-design.md`](client-tools-design.md): a runtime that declares `capabilities.clientTools` receives the client's tools in `conversation.clientTools` and `conversation.toolChoice` on every turn, ends its turn with a `response` that carries `clientToolCalls`, and receives the client's results on the next turn as a `message` with a `toolResults` member. The gateway validates the calls against the declared tools, `tool_choice`, and the parallel limit. |
| `lenny/request_input` | The platform tool a runtime calls to block until a client answers a question ([§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model)). `maxRequestInputWaitSeconds` bounds the wait. |
| Scoped model namespace | A model named `environments/{name}/{model}` on the OpenAI translators, which creates the session in environment `{name}` ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)). |
| Session labels | The client-set `labels` map on a session, used for querying, usage reports, and billing filters. |
| Link check and `LINKS_REQUIRED` | The post-chain check that refuses a session whose user has not linked every connector and account the session would use ([`connector-authorization-and-external-memory-design.md`, CA-4](connector-authorization-and-external-memory-design.md#ca-4-link-check)). |
| Impersonation bearer and F-13.3.26 | A token a `platform-admin` obtains to act as a user ([§13.3](../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). F-13.3.26 is the `BUILD-GAPS.md` finding, recorded in [`user-credential-delivery-design.md`](user-credential-delivery-design.md), that session input routes and the OpenAI translators admit one. |
| Tool-use approval | A platform or connector tool call held at the gateway until an approver resolves it ([`security-oversight-design.md`, SO-5a](security-oversight-design.md#so-5a-tool-use-approvals)), and the §7.2 approval gate the executor runs today. |
| `CREDENTIAL_POOL_EXHAUSTED` and its successor | The §15.1 error a session creation returns when no LLM credential can be leased. [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md) removes LLM credential pools and replaces the condition, and AM-8 treats its successor code as it treats this one. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 3 contract, 4 integration, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, 10 runtime adapter conformance, and 11 documentation. |

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
| MD O3, MD-4, and MD-8 | [`message-delivery-design.md`](message-delivery-design.md) | The owner's decision of 2026-11-09 that a message from another session to a session created on a provider surface is delivered on arrival, with its two rules for the turn it causes; turn accounting by consumed message ids; and the provider-surface rules that cut a response at the consumption of the request's message. |
| T1 to T8 | [`client-tools-design.md`](client-tools-design.md) | The owner's settled decisions on client tools: the turn-ending mechanism (T1), the surfaces (T2), the runtime's declaration (T3), cold Chat Completions round trips (T4), refusal of a message without pending results (T5), validation without repair (T6), no delegation of client tools (T7), and the landing order after AM-A (T8). |

## High-level design

An Anthropic SDK pointed at Lenny's gateway sees each Lenny runtime the caller may use as a model. A `POST /v1/messages` naming `support-agent` starts a session on the `support-agent` runtime, delivers the conversation, and returns the runtime's output as an assistant message. The client authenticates with the same Lenny bearer it would use for REST or MCP, carried in `Authorization: Bearer` or in `x-api-key`, which the auth middleware turns into one credential. Admission, quota, routing, revisions, link checks, billing, and audit apply as they do for any session, because the adapter creates sessions through the same service as `POST /v1/sessions/start`.

A Messages client resends the whole conversation on every request, and a Lenny runtime remembers it. The gateway reconciles them by recognising a conversation it has served. After each turn it stores a keyed digest of the conversation up to and including the reply it returned. When a later request's history up to its last assistant message has the same digest, and the session that produced it still holds its pod, the gateway delivers only the new input to that session. Any other request starts a new session and gives the runtime the earlier conversation as ordered entries with roles intact. A retry of a request whose turn is still running joins that turn and receives its result, so an SDK retry after a dropped connection does not run the turn twice. Every turn ends inside its HTTP request, and a long turn uses streaming. Client-declared tools work on runtimes that declare them, through the mechanism of [`client-tools-design.md`](client-tools-design.md): the runtime ends its turn with the calls, the response carries `tool_use` blocks, and the client's `tool_result` blocks reach the runtime on the next turn.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Endpoint (AM-1).** The adapter registers in the `ExternalAdapterRegistry` with the path prefix `/v1/messages`. It sits beside the OpenAI translators on the client listener and shares their single-shot binder, their holder forwarding, and their release path.

**Authentication and headers (AM-2).** The auth middleware accepts a Lenny bearer in `Authorization: Bearer` or in `x-api-key` on the adapter's routes, records it as the request's credential, and strips `x-api-key`. A value that starts with `sk-ant-` receives a specific 401 that never echoes it. `anthropic-version` must be `2026-10-21` or `2023-06-01` (O7). `anthropic-beta` is accepted and ignored, and field-level refusals enforce behaviour (M7).

**Request mapping (AM-3).** `model` names a runtime, an alias that a runtime route defines, or a scoped model namespace. Every request field and content block type has a stated disposition: mapped, forwarded as a hint, applied as an enforced parameter where the runtime declares it, accepted with no effect, or refused with `400 invalid_request_error`.

**Conversation input (AM-4).** The runtime's `message` envelope gains a `conversation` member carrying the unseen conversation as ordered entries, with the entries of the new input marked, the client's instructions, the parameters the runtime takes, and the client tool members. A runtime that declares `capabilities.conversationInput` receives it. Any other runtime receives the history and instructions as text parts ahead of the input (O3).

**Continuity (AM-5).** The gateway stores a keyed conversation digest per completed turn, keyed by tenant and owner. A request whose history matches a digest of a running session, under the conditions the Open Responses warm path checks, is a warm turn. A retry whose request digest matches a running turn joins it. Every other request is a cold turn (O2). A per-request record links each `request-id` to its session and turn. A message from another session to a held session is delivered on arrival, and the turn it causes carries no client tools and never appears in a Messages response (MD O3).

**Responses and streaming (AM-6).** Output parts become content blocks. A streaming request receives `message_start` at once, `ping` events while the turn runs, one content block per output part as the runtime emits it, and `message_delta` and `message_stop` at the end. A non-streaming request waits for the turn to end. `lenny/request_input` and elicitation toward the client fail at once on this surface (O4).

**Client tools (AM-7).** A request with client tools needs a runtime that declares `capabilities.clientTools`. The tools, `tool_choice`, and the parallel limit reach the runtime on every turn a client request causes, the runtime's `clientToolCalls` become `tool_use` blocks with `stop_reason: "tool_use"`, and the next request's `tool_result` blocks reach the runtime as the `toolResults` member of its next `message`, under [`client-tools-design.md`](client-tools-design.md) (O4).

**Errors (AM-8).** Every refusal on the adapter's paths uses the Anthropic error envelope, with Lenny's error code in `error.details.error_code` and Lenny's details beside it. A failure after dispatch carries `x-should-retry: false`.

**Models and refused endpoints (AM-9).** `GET /v1/models` with an `anthropic-version` header, or with `?adapter=anthropic-messages`, answers in the Anthropic list format and varies on `anthropic-version` (O6). Batches, files, and token counting answer `404 not_found_error` in the Anthropic envelope.

**Interactions (AM-10).** Tenancy comes from the bearer, billing follows the session, `metadata.user_id` becomes a session label and enters the digest, the model resolves through runtime routes and revisions, and the MCP-specific features of the MCP upgrade have no counterpart on this surface.

```
 Anthropic SDK ──POST /v1/messages (Authorization: Bearer or x-api-key = Lenny bearer)──> gateway
        │
        ▼
 auth middleware: one credential, x-api-key stripped ──> AnthropicMessagesAdapter: headers, field dispositions, blocks
        │
        ├── request digest matches a running turn (tenant, owner) ──> join the turn, return its result
        │
        ├── history up to last assistant message ──keyed digest──> conversation_turns lookup (tenant, owner)
        │        │
        │        ├── match, session running, newest turn, same generation ──> warm turn on held session
        │        │                                         (forwarded with credential and anthropic-version)
        │        └── no match, or any warm condition fails ──> cold turn
        │                                                       │
        │                                                       ▼
        │                     shared create-and-start: admission, PreRoute chain, revision,
        │                     post-chain point (links, runtimeOptions, clientTools capability), pod claim
        ▼
 message envelope: input (new user content) + conversation (entries with new input marked, instructions,
                   parameters, client tools)
        │
        ▼
 runtime turn ──output parts──> content blocks ──> JSON response, or SSE events
        │
        ├── response frame ──> stop_reason end_turn; digest stored; request record completed; session held or released
        └── response frame with clientToolCalls ──> stop_reason tool_use; next request carries tool_result blocks
```

"Limits" states what the design costs: a conversation whose history the client edits always runs cold, runtimes without conversation input see history as text, client tools need runtime changes, streams deliver whole parts, a non-streaming request cannot outlast the client's and intermediaries' timeouts, and summed usage can trigger a client's compaction early.

## Decisions

Settled by project rules:

| ID | Decision | Basis |
| :-- | :-- | :-- |
| D1 | The adapter is a built-in adapter in the `ExternalAdapterRegistry`, beside MCP and the OpenAI translators, and creates sessions through the shared create-and-start service. | §15 "Three tiers of pluggability" and the single-shot compute model. One creation path keeps admission and the post-chain point uniform. |
| D2 | The design adds no compatibility mode, legacy flag, or migration path for Lenny's own surfaces. The OpenAI translators change their history delivery in place (O3). | The project rule against compatibility shims before a first deployment (`feedback_no_backward_compat`). |
| D3 | A request field or content block that Lenny cannot honor is refused with `400 invalid_request_error` naming it, unless it is a hint with no effect on output semantics, such as `cache_control`. The existing translators' practice of decoding a request and dropping unknown fields is not copied. | Fail closed on behaviour a client relies on. A silently ignored `tools` array would return prose to a client that expects `tool_use`. |
| D4 | Every claim about the Messages API in this design comes from the pages under "Sources", read on 2026-11-09. | `feedback_verify_external_standard_versions`. |

Settled by the owner on 2026-11-09. The M labels are the adversarial review's findings that the owner answered together with an open decision:

| ID | Decision |
| :-- | :-- |
| O1 | The client presents a Lenny bearer in `Authorization: Bearer` or in `x-api-key`. The auth middleware normalizes `x-api-key` into the request's credential and strips the header. A value that starts with `sk-ant-` receives a specific `401 authentication_error` that never echoes the value. Conflicting values in the two headers receive `401 authentication_error` ("AM-2"). |
| O2 and M1 | Conversation state is kept by warm continuation on a keyed digest, with a cold fallback. The digest is HMAC-SHA256 and includes `metadata.user_id`. The canonical form follows RFC 8785. Consecutive messages of one role, and adjacent text blocks, are merged before hashing. `lenny.labels` is excluded from the digest. A retry reattaches to its running turn through an in-flight join. The beta `thread` parameter stays refused, and once it is stable a thread identifier maps to a session as `previous_response_id` does ("AM-5"). |
| O3 | Cold turns deliver the conversation as structured input to runtimes that declare it, and as a transcript to others, and the OpenAI translators move onto the same input. The `conversation` member carries ordered entries with the new input marked, so a trailing system message has a place. The beta per-message fields are refused ("AM-3" and "AM-4"). |
| O4, M3, and M6 | No open turns: revision 2's AM-D is removed, and a turn ends inside its request. A long turn uses streaming. Client tools come from [`client-tools-design.md`](client-tools-design.md), which replaces revision 2's AM-7 bridge. `lenny/request_input` and elicitation toward the client are refused at once on this surface, and the adapter declares `SupportsElicitation: false` ("AM-6" and "AM-7"). |
| O5 and M4 | The runtime declares which parameters it honours. A non-default `stop_sequences` or `thinking.display` for a runtime that does not declare it is refused. `effort` and `thinking` pass as hints. `max_tokens` is per message: it caps the turn's visible output, only for a runtime that declares it, and never maps to the session's budget ("AM-3"). |
| O6 | `GET /v1/models` chooses its format by `anthropic-version` or by `?adapter=anthropic-messages`, sends `Vary: anthropic-version`, dispatches `GET /v1/models/{id}` the same way, and reports a capability it cannot vouch for as false or null ("AM-9"). |
| O7 and M5 | The adapter accepts `2026-10-21` and `2023-06-01`, and refuses an unknown newer version with an error that lists the supported ones ("AM-2"). |
| O8 | Removed with open turns. Revision 2's `pause_turn` wait limit and `anthropicMessages.maxResponseWaitSeconds` go. |
| O10 | The `lenny` object is create-only. Its `labels` are excluded from the digest, and a warm turn ignores the object ("AM-3"). |
| M2 | A held session follows the Open Responses hold rule of §15 "Open Responses conversation sessions", and the adapter adds no timer of its own ("AM-5"). |
| M7 | `anthropic-beta` is accepted and ignored. Field-level refusals enforce behaviour ("AM-2"). |
| MD O3 | Decided on [`message-delivery-design.md`](message-delivery-design.md), and binding here. A message from another session to a held session is delivered on arrival. Client tools are offered only in turns a client request caused, and a turn that no client request caused never appears in a Messages response, whose content is the output of the client's own turn. The conversation digest is unaffected ("AM-5", "Messages from other sessions"). |
| M8 | This design asks [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) for a `jwt-bearer` grant on `/v1/oauth/token`, so the SDKs' workload identity federation can obtain a Lenny bearer ("Instructions for reviewers of other designs"). |

Settled by the owner on 2026-11-10:

| ID | Decision |
| :-- | :-- |
| O9 | A model name that is no runtime, such as an Anthropic model ID, reaches a runtime through a runtime route whose requested name is that alias. [RT-4](runtime-routing-design.md#rt-4-runtime-routes) accepts such a route, and `GET /v1/models` lists the caller's aliases. A separate alias table would duplicate routes. The owner's words were "go with the recommendation" ("AM-3" and "AM-9"). |

No decision is open.

## Requirements

| ID | Requirement |
| :-- | :-- |
| AMR-1 | A program written against an Anthropic SDK reaches a Lenny runtime by changing the base URL, the credential, and the model name. |
| AMR-2 | Every session the adapter creates passes the same admission, `PreRoute` chain, revision selection, and post-chain checks as `POST /v1/sessions/start`. |
| AMR-3 | A runtime never receives an earlier user turn as new input, and never receives an earlier assistant reply as client input with no role. |
| AMR-4 | A warm turn reaches a session only when the request's principal owns the session and the request's history matches the conversation the session produced, under the conditions of "AM-5". |
| AMR-5 | Every request field and content block type has a stated disposition, and a field Lenny cannot honor is refused. An enforced parameter reaches only a runtime that declares it. |
| AMR-6 | Every refusal on the adapter's paths uses the Anthropic error envelope and an HTTP status that Anthropic pairs with its error type, and a failure that a retry would repeat carries `x-should-retry: false`. |
| AMR-7 | A client tool is offered only to a runtime whose chosen revision declares `capabilities.clientTools`, and the calls and results follow [`client-tools-design.md`](client-tools-design.md). |
| AMR-8 | The conversation digests, request records, and response records of a session are erased with the session under [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces). |
| AMR-9 | The adapter's sessions are billed, audited, and owned as any session is, and no request field changes the billed tenant or owner. |
| AMR-10 | The `model` field of a response is the request's `model` string, so a rollout or a route changes no response field. The session, runtime, and revision appear in response headers. |
| AMR-11 | A retry of a request whose turn is running receives that turn's result and starts no second turn, when the retry arrives after the turn is reserved. |
| AMR-12 | The gateway never logs, echoes, or forwards a value presented in `x-api-key`, and never forwards an Anthropic API key to any component. |
| AMR-13 | A turn that no client request caused carries no client tools and never appears in a Messages response. A Messages response carries only output of the turn that consumed the request's own message, from the point of that consumption. |

## Current state

### Lenny

**Adapter inventory [source].** §15 "Built-in adapter inventory" lists `MCPAdapter` at `/mcp`, `OpenAICompletionsAdapter` at `/v1/chat/completions`, and `OpenResponsesAdapter` at `/v1/responses` for v1, and `A2AAdapter` and `AgentProtocolAdapter` for later. No specification section, document under `docs/api/`, or handler on the client listener names a client-facing Anthropic surface. `cmd/lenny-gateway/httpsurface.go` registers the OpenAI translators through `adapterregistry.NewSimpleAdapter` with their `gwadapter.Capabilities` and mounts the registry on the client mux.

**LLM proxy [source].** `cmd/lenny-gateway/llmproxy_server.go` mounts `POST /llm-proxy/v1/messages` on a separate server bound to `--llm-proxy-addr`. `pkg/gateway/llmproxy/llmproxy/translator.go` defines `DialectAnthropic` and the `anthropic_direct` translator, and `sse.go` reads Anthropic stream events for token usage. The proxy authenticates the pod's lease token and never sees a client bearer. `docs/getting-started/architecture.md` describes the proxy's path as `/v1/messages`, which a reader can mistake for a client surface.

**Translators [source].** `pkg/gateway/environment/translator` holds `OpenAIChatHandler` (`openai_chat.go`) and `OpenResponsesHandler` (`open_responses.go`). Both call `SingleShotBinder.BindSingleShot`, which `sessionSingleShotBinder` in `cmd/lenny-gateway/credentialsurface.go` implements over `sessionserver.CreateAndStartService`, and both release through `releaseSingleShot`. The binder reports `Hold` when the session's `conversationContinuity` is `"platform"` and its runtime declares `multi_turn` with injection. `OpenResponsesHandler` keeps response records on the session row (`Session.Responses`, `ContinuationParentID`), serves warm turns under the reservation and generation rules of §15 "Open Responses conversation sessions", forwards warm turns to the coordinating replica through `HolderForwarder`, and rebuilds a cold continuation's context from the transcript in `continuity.rehydrate`. Both handlers run the turn's `exec.Send` on the HTTP request's context, so a dropped client connection cancels the gateway's wait for the turn.

**Forwarding [source].** `HolderForwarder.ForwardResponsesTurn` reaches `Server.ForwardResponsesTurn` in `pkg/gateway/sessionserver/holder_forward.go`. `newForwardRequest` copies only the headers in `forwardRequestHeaders`, `Content-Type` and `Accept`, and `setForwardIdentity` writes the credential that the auth middleware recorded on the request context (`authmw.CredentialFrom`). A header the middleware did not record as the credential, such as `x-api-key`, and every other client header, such as `anthropic-version`, does not reach the coordinating replica.

**Request handling [source].** `OpenAIChatCompletionsRequest` models `model`, `messages`, `stream`, and `user`, with `content` as a string. `OpenResponsesRequest` models `model`, `input`, `previous_response_id`, `stream`, and `user`. Both decode with `encoding/json` and no `DisallowUnknownFields`, so `tools` and sampling parameters are dropped without an error (F-15.1.82). `normalizeInput` reads an Open Responses item's `content` as a string, so multi-part content becomes empty text without an error. A Chat Completions message with multi-part content fails the decode into the string field and is refused with `400 invalid_request_error`, whose message calls the body invalid JSON. Both cap the body at 1 MiB. A model named `environments/{name}/{model}` scopes the session to an environment (`splitEnvModel`).

**Delivery to the runtime [source].** `executor.Executor.Send(ctx, sessionID, []Message)` is synchronous and returns the turn's output parts. `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go` writes each `Message` as its own `message` envelope with one text part and calls `runTurn` for each, so a request with several messages runs several turns (F-15.1.80). `Message.Role` is never written to the envelope: `resolveFromBlock` sets `from` to `client`/`client_gateway`, and no code in the executor package reads `Role`. The `MessageEnvelope` of §15.4.1 has no role field.

**Streaming [source].** `writeOpenAIStream` and `writeOpenResponsesStream` run after the turn has completed and split the text on whitespace into synthetic deltas. Usage counts in both translators are zeros.

**Authentication [source].** `pkg/gateway/middleware/auth/auth.go` reads `Authorization: Bearer` and, under dev mode only, the `X-Lenny-*` headers. It reads no `x-api-key`. §10.2 states that no dedicated API-key primitive exists in v1 and that a pasted "API key" in the playground is a Lenny bearer. §13.3 states that `POST /v1/oauth/token` follows RFC 6749 and RFC 8693, authenticates its caller by `Authorization: Bearer`, and accepts an upstream OIDC ID token as the bootstrap credential. It names no RFC 7523 `jwt-bearer` grant.

**Discovery [source].** `GET /v1/models` has one handler, `handleListModels` in `pkg/gateway/sessionserver/runtimes.go`, which returns OpenAI model objects for the runtimes the caller's environment access authorizes and selects the `adapterCapabilities` it reports by the `?adapter=` query parameter. §9.1 "Runtime Discovery" lists `GET /v1/models` for both OpenAI translators.

**Runtime tools and approvals [source].** §28.5.3 limits stdin `tool_call` and `tool_result` frames to adapter-local tools. Platform and connector tools travel over the runtime's MCP client. `PodExecutor.maybeGateToolCall` holds a runtime `tool_call` that carries `approvalRequired` until the §7.2 approval gate decides, then relays the call or a denial.

**Content types [source].** The §28.5.3 canonical type registry has translation columns for MCP, OpenAI, and A2A. The §15.4.1 translation fidelity matrix has columns for MCP, OpenAI Completions, Open Responses, REST, and A2A.

**Errors [source].** §15.1 defines `REQUEST_INPUT_TIMEOUT`, delivered as a tool-call error to a runtime whose `lenny/request_input` waited too long. It defines no code for a `lenny/request_input` that a surface cannot serve.

### Anthropic Messages API on 2026-11-09

**Versions [external].** The versioning page lists `2026-10-21` and `2023-06-01`. Under `2026-10-21`, a request that sends `temperature`, `top_p`, or `top_k` returns 400 on every model, a `document` block in an `assistant` message returns 400, and a `text` block with a non-empty `citations` array in a `user` message returns 400.

**Headers and credentials [external].** `Authorization: Bearer <token>` is required unless `x-api-key` is set, and the authentication page calls `x-api-key` "the legacy `x-api-key: YOUR_API_KEY` header". An Anthropic API key is a static `sk-ant-api...` secret, and a workload identity federation token is prefixed `sk-ant-oat01-`. `anthropic-version` is required. `anthropic-beta` takes comma-separated beta names, and an unknown name returns `400 invalid_request_error`. Every response carries a `request-id` header, and error bodies repeat it as `request_id`.

**Workload identity federation [external].** A workload exchanges its identity provider's JWT at `POST /v1/oauth/token` with `grant_type` `urn:ietf:params:oauth:grant-type:jwt-bearer` (RFC 7523), an `assertion`, a `federation_rule_id`, an `organization_id`, a `service_account_id`, and an optional `workspace_id`, and receives an OAuth 2.0 token response. The SDK builds the request from `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_SERVICE_ACCOUNT_ID`, and `ANTHROPIC_IDENTITY_TOKEN_FILE` or `ANTHROPIC_IDENTITY_TOKEN`, refreshes the token before it expires, and lets `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` override federation.

**Request [external].** `model`, `messages`, and `max_tokens` are required. Message roles are `user`, `assistant`, and `system`. A final `assistant` message is a prefill, which Claude 4.6 and later models refuse. A `system` message in `messages` needs no beta header, must follow a `user` turn, and may be the last entry. Its beta extensions are `clear_at`, `output_config.effort` on the message, and the `tool_addition` and `tool_removal` blocks. Optional top-level fields are `cache_control`, `container`, `diagnostics`, `inference_geo`, `metadata` (`user_id`), `output_config` (`effort` and `format`), `service_tier`, `speed`, `stop_sequences`, `stream`, `system`, `thinking`, `tool_choice`, and `tools`. `thinking` takes a `display` member, and `display: "omitted"` keeps thinking content out of the response. Beta-only fields include `context_management`, `compaction`, `mcp_servers`, and `thread`.

**Content and tools [external].** User-turn blocks are `text`, `image` (base64, URL, or file source), `document` (base64 PDF, plain text, content, URL, or file source), `search_result`, `thinking`, `redacted_thinking`, `tool_use`, `tool_result`, `server_tool_use`, the server tool result blocks, and `container_upload`. A client tool has `name` matching `^[a-zA-Z0-9_-]{1,128}$`, `input_schema`, and optional `description`, `strict`, and other fields. Server tools are typed, such as `web_search_20260318`, `code_execution_20260521`, `bash_20250124`, and `memory_20250818`. `tool_choice` is `auto`, `any`, `tool`, or `none`, with `disable_parallel_tool_use`.

**Response [external].** A message has `id`, `type: "message"`, `role: "assistant"`, `content`, `model`, `stop_reason` (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`, `refusal`, or `model_context_window_exceeded`), `stop_sequence`, `stop_details`, `container`, `diagnostics`, and `usage`. Output blocks include `text`, `thinking`, `redacted_thinking`, `tool_use`, and the server tool blocks. No output block carries an image.

**Streaming [external].** Named server-sent events, each with `data.type` equal to the event name: `message_start`, then per block `content_block_start`, `content_block_delta`, and `content_block_stop`, then `message_delta` with cumulative `usage`, then `message_stop`. `ping` can appear anywhere. A mid-stream error is an `error` event. Delta types are `text_delta`, `input_json_delta`, `citations_delta`, `thinking_delta`, and `signature_delta`. There is no `[DONE]` sentinel, and clients ignore event types they do not know.

**Errors [external].** 400 `invalid_request_error`, 401 `authentication_error`, 402 `billing_error`, 403 `permission_error`, 404 `not_found_error`, 409 `conflict_error`, 413 `request_too_large`, 429 `rate_limit_error`, 500 `api_error`, 504 `timeout_error`, and 529 `overloaded_error`. Other 4xx statuses use `invalid_request_error`. The error page shows `error` with `type` and `message` and states that "the values within these objects may expand". The Python SDK types an error object with a `details` member, a union discriminated by `error_code`, whose variants carry their own fields beside `error_code` (`types/shared/error_details.py`). The SDKs retry connection errors, 429, and 5xx responses twice by default, honoring `retry-after`. The Python SDK's `_should_retry` obeys a response header `x-should-retry` of `true` or `false` before any status rule (`_base_client.py`). The SDK refuses a non-streaming request that it expects to exceed 10 minutes, and the error page recommends streaming for long requests.

**Other endpoints [external].** `POST /v1/messages/count_tokens` returns `{input_tokens}`. `GET /v1/models` takes `after_id`, `before_id`, `limit`, and `lifecycle[]`, and returns `{data, first_id, has_more, last_id}` with `ModelInfo` objects (`type: "model"`, `id`, `display_name`, `created_at`, `capabilities`, `lifecycle`, `line`, `max_input_tokens`, `max_tokens`, `deprecated_at`, and `retires_at`). `capabilities` is nullable, every capability entry carries a boolean `supported`, and `line`, `max_input_tokens`, and `max_tokens` are nullable. Message Batches live under `/v1/messages/batches`, and the Files API under `/v1/files`.

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

**Credential [design, O1].** On the adapter's routes and on `GET /v1/models` and `GET /v1/models/{model_id}`, the auth middleware reads a bearer from `Authorization: Bearer` or from `x-api-key`, validates it exactly as a bearer (a Lenny-issued JWT or an OIDC ID token, with the §10.2 tenant-claim extraction), records it on the request context as the request's credential, and removes `x-api-key` from the request before any handler or log sees it. Every later step reads the recorded credential, so the holder forward of "AM-5" carries it as `Authorization: Bearer` through `setForwardIdentity`. On every other route the middleware ignores `x-api-key`, so no other surface gains a second credential header.

- **Conflicting values.** A request with both headers and different values answers `401 authentication_error`. Equal values are one credential.
- **Anthropic API key.** A value in either header that starts with `sk-ant-` answers `401 authentication_error` with a message that states that the gateway takes a Lenny credential and that an Anthropic API key was presented. The message, the logs, the audit record, and every metric label omit the value, and the gateway forwards it nowhere. The prefix covers Anthropic API keys and federation tokens **[external]**.
- **No credential.** A request with no credential answers in the Anthropic error envelope on these routes ("AM-8").

A Lenny bearer expires within `auth.capabilityTokenTtlSeconds` or the cap a `POST /v1/oauth/token` request sets. A client that stores a key in an environment variable needs to refresh it ("Limits"). M8 asks for the grant that lets the SDKs' workload identity federation refresh it.

**`anthropic-version` [design, O7].** The header is required. `2026-10-21` and `2023-06-01` are accepted. A date later than `2026-10-21` that the adapter does not know answers `400 invalid_request_error` with a message that lists the supported versions, and `error.details.supported_versions` carries them. Any other value answers `400 invalid_request_error`. Under `2026-10-21` the adapter refuses `temperature`, `top_p`, and `top_k`, a `document` block in an `assistant` message, and a `text` block with a non-empty `citations` array in a `user` message, each with `400 invalid_request_error`, as Anthropic does. Under `2023-06-01` it accepts the sampling parameters as hints (O5).

**`anthropic-beta` [design, M7].** The header is accepted and ignored. Lenny implements no beta feature, and the beta-only request fields and the beta per-message fields are refused under "AM-3", so a request that relies on a beta's fields meets a refusal naming the field.

**`anthropic-workspace-id` [design].** A request that carries it answers `400 invalid_request_error`. Lenny has no workspaces, and the tenant comes from the bearer. An environment is named through the scoped model namespace ("AM-3").

**Response headers [design].** Every response carries `request-id: req_<opaque>`, a fresh identifier that the request record of "AM-5" links to the session and turn. A response for a turn carries `lenny-session-id`, `lenny-runtime`, and `lenny-runtime-revision`, so a client or an operator can reach the session through REST or MCP, for example to approve a held tool call. `lenny-dropped-parameters` lists the hints a runtime without conversation input did not receive (O5).

## AM-3 Request mapping

### Model

`model` names a runtime or an alias **[design, O9]**. A value of the form `environments/{name}/{model}` scopes the session to environment `{name}`, as `splitEnvModel` does for the OpenAI translators (§10.6). The bare name is the requested runtime for the `PreRoute` chain, so a runtime route can send the session elsewhere and a revision is chosen on the final runtime (RT-2, RV-2). An alias is a route whose requested name is no runtime, so a client that keeps `model: "claude-sonnet-5-5"` from its Anthropic configuration reaches the runtimes the route's targets name, and the response's `model` stays the requested name (AMR-10). A name whose final runtime after the chain is no runtime in the caller's view answers `404 not_found_error`, the status Anthropic uses for an unknown model. A missing `model` answers `400 invalid_request_error`, and the adapter has no default runtime: the OpenAI translators' `DefaultRuntime` of `echo` would send a client's mistake to the echo runtime.

### Parameters

A runtime declares the enforced parameters it honours in `capabilities.conversationParameters`, a list drawn from `maxTokens`, `stopSequences`, and `thinkingDisplay` **[design, O5]**. The flag is a versioned field under RV-1, read from the chosen revision. The gateway enforces none of them itself: a runtime that declares one applies it to what the turn returns.

- **`max_tokens`.** Required and validated as an integer of at least 1, as Anthropic requires. It is per message: it caps the visible output of the turn the request starts, and it reaches only a runtime that declares `maxTokens`, as `parameters.maxTokens`. For any other runtime it is accepted and has no effect. It never sets or changes the session's token budget or any lease.
- **`stop_sequences`.** Reaches a runtime that declares `stopSequences` as `parameters.stopSequences`. A non-empty value for any other runtime answers `400 invalid_request_error` naming the field.
- **`thinking.display`.** Reaches a runtime that declares `thinkingDisplay` as `parameters.thinking.display`. A value other than the default for any other runtime answers `400 invalid_request_error` naming the field.
- **Hints.** `thinking.type`, `output_config.effort`, and, under `2023-06-01`, `temperature`, `top_p`, and `top_k` pass as hints in `parameters` to every runtime that declares conversation input. A runtime uses them for its model calls or ignores them.

### Request fields

| Field | Disposition |
| :-- | :-- |
| `model` | Mapped to the requested runtime and environment, as above. |
| `messages` | Mapped to the conversation. "AM-4" and "AM-5" state how. A final `assistant` message answers `400 invalid_request_error` (prefill). A final `system` message after the final `user` message is part of the new input. |
| `max_tokens` | Required. "Parameters" above. |
| `system` | Forwarded as `conversation.instructions`, as text parts (AM-4). |
| `stream` | Selects the streaming response ("AM-6"). |
| `stop_sequences` | "Parameters" above. |
| `thinking` | `type` passes as a hint, and `display` follows "Parameters" above. When `type` is `enabled` or `adaptive`, output parts of type `reasoning_trace` are returned as `thinking` blocks, and otherwise they are omitted ("AM-6"). |
| `output_config.effort` | Passes as `parameters.effort`, a hint. |
| `output_config.format` | Forwarded as `parameters.outputSchema`. The gateway validates the final text block against the schema and answers `500 api_error` with `error_code` `OUTPUT_SCHEMA_MISMATCH` when it does not parse or match. |
| `tools` | Client tools: "AM-7". A tool with a server tool `type` (for example `web_search_20260318`, `code_execution_20260521`, `bash_20250124`, `text_editor_20250728`, `memory_20250818`, or `computer_toolset_20260801`) answers `400 invalid_request_error`. A runtime brings its own tools. |
| `tool_choice` | Forwarded with the client tools ("AM-7"). `{"type":"none"}` is accepted with or without tools. |
| `metadata.user_id` | Recorded as the session label `anthropic-user-id` on a cold turn, and part of the digest. It never sets the owner, the tenant, the rollout key, or the billing subject ("AM-10"). |
| `cache_control` (top level and on blocks) | Accepted with no effect. It is a caching hint, and usage reports no cache tokens. |
| `service_tier` | `auto` and `standard_only` are accepted with no effect, because Lenny has one tier. `usage.service_tier` is `standard`. |
| `speed` | `standard` and null are accepted. `fast` answers `400 invalid_request_error`. |
| `inference_geo` | A non-null value answers `400 invalid_request_error`. Data residency in Lenny is a deployment and tenant property that a request cannot select. |
| `container` | A non-null value answers `400 invalid_request_error`. A runtime's workspace is the session's own. |
| `diagnostics` | Accepted. `diagnostics` in the response is null. |
| `temperature`, `top_p`, and `top_k` | Under `2023-06-01`, hints. Under `2026-10-21`, refused as Anthropic refuses them. |
| `lenny` (O10) | Create-only. An object with `runtimeOptions`, `labels`, and `deferConnectors`, mapped to the fields of the same names on the create-and-start request of a cold turn. `runtimeOptions` and `deferConnectors` are part of the digest, and `labels` is excluded from it. A warm turn ignores the whole object, so a client that changes `labels` keeps its warm session. |
| Beta-only fields (`mcp_servers`, `context_management`, `compaction`, `thread`, `fallbacks`, `output_format`, and others) | `400 invalid_request_error` naming the field. |
| Beta per-message fields (`clear_at`, `output_config` on a message, and the `tool_addition` and `tool_removal` blocks) | `400 invalid_request_error` naming the field and the message index. |
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
| `search_result`, `server_tool_use`, server tool result blocks, `container_upload`, `tool_addition`, and `tool_removal` | Refused. | Refused. |

Large inline parts follow the §28.5.3 rule that stages a large blob to the artifact store and sets a `lenny-blob://` `ref`, so the transcript stays bounded.

## AM-4 Conversation input

### Envelope member

The `MessageEnvelope` of §15.4.1 gains an optional, gateway-written `conversation` member **[design, O3]**:

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
    "entries": [
      { "role": "user", "content": [{ "type": "text", "inline": "What is our refund policy?" }] },
      { "role": "assistant", "content": [{ "type": "text", "inline": "Refunds are accepted within 30 days." }] },
      { "role": "user", "new": true, "content": [{ "type": "text", "inline": "And the refund window for EU orders?" }] },
      { "role": "system", "new": true, "content": [{ "type": "text", "inline": "Answer in one sentence." }] }
    ],
    "parameters": { "maxTokens": 1024, "thinking": { "type": "adaptive" } },
    "clientTools": [],
    "toolChoice": { "type": "auto" }
  }
}
```

- `entries` holds, in order, every entry of the conversation that the runtime has not seen, each with `role` (`user`, `assistant`, or `system`) and `content` as `MessagePart` entries. The entries that form the turn's new input carry `new: true`. On a cold turn the unmarked entries are the whole earlier conversation. On a warm turn every entry is new, and the list holds the final user content and any `system` message around it. A `system` message that follows the final user message, which the Messages API allows as the last entry **[external]**, is a marked entry after the user entry, so it has a position the runtime can read. Revision 2 had no place for it.
- `input` carries the content of the final marked `user` entry, so a runtime that reads only `input` receives the new user content.
- `instructions` holds the request's top-level `system` text. It is present on the first envelope of a session. The digest includes it, so it does not change on a warm turn.
- `parameters` holds the hints of "AM-3", and the enforced parameters the chosen revision declares. It is present on every envelope the adapter sends.
- `clientTools` and `toolChoice` are the members of [`client-tools-design.md`](client-tools-design.md), present on every turn of a request that declares client tools (T1). A `message` that carries client tool results carries them in that design's `toolResults` member.

A runtime treats every entry of `conversation` as client input. An entry with `role: assistant` is the client's assertion of what an assistant said, because a Messages client controls its whole history. The gateway marks `from.kind: client` on the envelope as for any client message.

Other sources of a `message` envelope, such as REST delivery and MCP `lenny/send_message`, never set `conversation`.

### Capability

A runtime declares `capabilities.conversationInput: true` to receive the member, and `capabilities.conversationParameters` to receive enforced parameters **[design]**. Both are versioned fields under RV-1, so the chosen revision decides. A per-tenant capability override can only clear `conversationInput` and narrow `conversationParameters`, as RV-1 lets an override narrow `capabilities.interaction`.

### Transcript rendering

For a revision that does not declare `conversationInput`, the gateway removes `conversation` and prepends text parts to `input` **[design, O3]**:

- When `instructions` is present, a part `Instructions from the client:` followed by the instruction text.
- When unmarked entries are present, a part `Earlier conversation:` followed by one line per entry, `[user]`, `[assistant]`, or `[system]` and the entry's text, with each non-text part replaced by a line naming its type and `mimeType`.
- When a marked `system` entry follows the new user content, a part `Instructions for this turn:` followed by its text, after the user content.

The parts carry the annotation `lenny.conversationRendering: transcript`. `parameters` is dropped, `lenny-dropped-parameters` lists the hints it held, and the enforced-parameter rules of "AM-3" apply because such a revision declares none. A request with client tools on such a revision is refused before any pod is claimed ("AM-7").

### OpenAI translators

The same member and rendering replace one-turn-per-message delivery in `OpenAIChatHandler` and in `OpenResponsesHandler`'s cold path (O3). `PodExecutor.Send` and `SubprocessExecutor.Send` write one envelope per turn, carrying the turn's new user content in `input` and the conversation in `conversation.entries`. A Chat Completions request whose final message is not a `user` message, or a `system` message after a `user` message, answers `400 invalid_request_error`, because the turn has no new user input to deliver.

## AM-5 Continuity

### Conversation digest

The canonical form of a conversation prefix is the JSON Canonicalization Scheme of RFC 8785 applied to an object with these members **[design, O2 and M1]**:

- `tenant`, `owner` (the principal subject), and `model` (the request's `model` string, including any environment prefix).
- `user_id`, the request's `metadata.user_id`, or null.
- `system`, `tools`, `tool_choice`, and `lenny` with its `labels` member removed, as the request carries them, after normalization.
- `messages`, the prefix being hashed, after normalization.

Normalization, in this order, turns a string `content` into a one-element array of a `text` block, removes `cache_control` and `citations` from every block, removes `thinking` and `redacted_thinking` blocks, drops fields whose value is null, merges consecutive messages of one role into one message whose content is their contents in order, and merges adjacent `text` blocks within a message into one block whose text is their texts joined in order. The Messages API treats consecutive messages of one role as one turn, and clients re-serialize content arrays, omit earlier thinking blocks, and split or join text blocks between requests, and none of those changes alter what the runtime saw.

The digest is HMAC-SHA256 over the canonical form, keyed by `anthropicMessages.digestKey`, a gateway secret held in the platform's key store and shared by every replica. The key means that a reader of the digest table cannot test a guessed conversation against a stored digest. Rotating the key makes every stored digest miss, so every conversation's next request runs cold once. The tenant, owner, and user identifier inside the canonical form keep one user's history from matching another user's session, and `user_id` keeps two end users of one application principal apart.

### Storage

A table `conversation_turns` holds one row per completed turn **[design]**: `tenant_id`, `owner_subject`, `digest`, `session_id`, `ordinal`, and `created_at`, with a unique index on `(tenant_id, owner_subject, digest)` and a foreign key to the session that deletes the rows with it. The session row's `Responses` records of §15 "Open Responses conversation sessions" carry the ordinal, the end sequence, the recovery generation, and the completion flag for each turn, as they do for Open Responses. The §12.8 erasure of a session deletes its rows, and §12.8 names the table (AMR-8).

When a turn completes, the gateway writes the turn's digest, computed over the request's messages followed by the assistant message the gateway returned, in the same write that completes the response record. A unique-index conflict means another session produced an identical conversation for the same owner. The newer row replaces the older, so a continuation reaches the most recent session.

### Request records

A table `messages_requests` holds one row per request that reaches a turn **[design]**: the `request-id` the response carries, `tenant_id`, `owner_subject`, `session_id`, the turn `ordinal`, the request digest over the whole normalized `messages` array, the state (`running`, `completed`, or `failed`), and `created_at`, with a foreign key to the session that deletes the rows with it. The adapter writes the row when it reserves the turn, and completes it in the write that completes the response record. An operator or a client support flow finds the session and turn behind a `request-id` from it, and the §12.8 erasure of a session deletes it (AMR-8).

### In-flight join

Before any warm or cold dispatch, the gateway looks up the request digest under the request's tenant and owner among request records in state `running` **[design, M1]**. A match is a retry of a request whose turn is still running, as an SDK sends after a dropped connection or a timeout, and the request joins that turn: a non-streaming request waits for the turn's response record and returns it, and a streaming request sends `message_start` and then the parts of the turn from the response record and the session's `output` events, as "AM-6" streams them. The join starts no turn, reserves no ordinal, and writes no digest. A request whose digest matches a request record in state `completed` is not a retry of a running turn and takes the warm or cold path.

A turn that has reached the runtime completes when the client's connection drops, and writes its response record and its request record, so a joining retry receives its result. The turn's completion therefore cannot depend on the HTTP request's context, which the translators use today ("Current state"). Revision 2 needed a runner detached from the request for open turns, which ran past their HTTP response by design. With open turns removed, a turn outlives its request only when the connection drops, and the response record carries it, so no runner of that kind is needed.

### Warm and cold

For a request whose `messages` contain an `assistant` message, the gateway computes the digest of `messages[0..k]`, where `k` is the index of the last `assistant` message, and looks it up under the request's tenant and owner **[design]**. The request is served as a warm turn on session `S` with ordinal `n` only when all of these hold, which are the conditions of §15 "Open Responses conversation sessions":

1. A row matches, naming `S` and `n`.
2. `S` is `running`.
3. Turn `n` is `S`'s newest response and has completed.
4. `S`'s `recovery_generation` equals the value recorded for turn `n`.
5. The request's principal is `S`'s owner, and the request's runtime and environment are those of the request that created `S`.

Before a warm dispatch, the gateway reserves ordinal `n+1` on `S` with one conditional write, as the Open Responses path does. A warm turn on a replica other than `S`'s coordinating replica is forwarded under §29.3 "Interactive message send", through the forwarding interface the Open Responses handler uses. The forward carries the recorded credential as `Authorization: Bearer` and the request's `anthropic-version`, which the forward adds to the headers it copies for this adapter, because the coordinating replica authenticates the forward and applies the version's validation and response rules. Revision 2 forwarded neither, and the review found it. Every other request is a cold turn: a request with no `assistant` message, no matching row, a failed condition, a lost reservation, or a warm dispatch that fails before input reaches the runtime. A cold turn creates a session through the shared create-and-start service and delivers the messages before the new input as unmarked entries and the new input as marked entries.

A warm turn's `input` is the final user message's content, and its `conversation.entries` hold the new input, including any `system` message after the last `assistant` message.

### Holding a session

After a turn completes, the session stays `running` and keeps its pod only under the hold rule of §15 "Open Responses conversation sessions": `conversationContinuity: "platform"` and a revision that declares `multi_turn` with injection **[design, M2]**. Every other session is released with the `completed` disposition before the response returns, and its next request is a cold turn. A held session is an ordinary session: `maxClientIdleSeconds`, idle suspension, `maxSessionAge`, and the admission limits govern it, and the adapter adds no timer of its own.

### Messages from other sessions

A held session can receive a message from another session between client requests **[design, MD O3]**. The main case is asynchronous delegation: the runtime delegates a child, replies to its client, and the child later finishes or sends the parent a message after that reply. The same session can also be used through REST or MCP with the `lenny-session-id` the response headers carry, and a sibling can send it a message under the `siblings` messaging scope. Under MD O3, such a message is delivered on arrival under the ordinary intent rules of [`message-delivery-design.md`](message-delivery-design.md), and this adapter adds no hold of its own. Two rules keep the turn it causes away from the Messages client:

1. **No client tools.** Client tools are offered only in turns a client request caused. A turn that no client request caused carries no `conversation.clientTools` and no `conversation.toolChoice`, and a call in it fails the check of [`client-tools-design.md`, CT-4](client-tools-design.md#ct-4-response-validation) with `no_tools_declared`.
2. **No appearance in a Messages response.** A turn that no client request caused never appears in a Messages response. Its output reaches the transcript and the session's REST and MCP event streams. The client's next response is the output of the client's own turn, the turn that consumed the request's message, under the consumption attribution of MD-4.

The conversation digest is unaffected, because it covers only what the client sent and what Lenny returned. A turn that no client request caused writes no digest, no response record, and no request record, and reserves no ordinal, so condition 3 of "Warm and cold" still holds for the client's next request.

A request that passes the warm conditions while such a turn runs is written into it on a runtime that declares the `steer` delivery intent, and waits for that turn's `response` on any other runtime, during which a streaming request receives `ping` events. A request written into such a turn carries its own `conversation.clientTools`, so the turn may end with calls to them, which the response returns as `tool_use` blocks. The response carries the turn's output from the point the runtime consumed the request's message, as MD-8 states, and `usage` counts the model calls that start after that point. Tool-use approvals in such a turn resolve through the approvals API with the `lenny-session-id` the response headers carry. `lenny/request_input` and elicitation stay refused as "AM-6" states, whatever caused the turn.

## AM-6 Responses and streaming

### Output mapping

| Output part | Content block |
| :-- | :-- |
| `text`, `code`, `diff`, `citation`, `error`, and `execution_result` (flattened) | `text`, one block per part. A `code` part's language and an `error` part's flag are dropped. |
| `reasoning_trace` | `thinking` when the request's `thinking.type` was `enabled` or `adaptive` and `thinking.display` was not `omitted`, with a `signature` the gateway sets to an opaque value. Otherwise omitted. |
| `image`, `screenshot`, and `file` | A `text` block naming the part's type, `mimeType`, and size, because the Messages API has no output block for images or files. The part is reachable through `GET /v1/sessions/{id}/transcript`. |
| A custom type | `text`, per the registry's fallback rule. |

A `ref` is dereferenced inside the gateway and never returned, as §15.1 requires of external adapters.

**Message fields [design].** `id` is `msg_` followed by the session ID, a `.`, and the turn ordinal, and clients treat it as opaque. `model` is the request's `model` string (AMR-10). `role` is `assistant`. `stop_sequence`, `stop_details`, `container`, and `diagnostics` are null, apart from `stop_sequence` under the rule below.

**`stop_reason` [design].** `end_turn` when the runtime's `response` frame ends the turn. `tool_use` when the `response` carries `clientToolCalls` ("AM-7"). `max_tokens` when a runtime that declares `maxTokens` reports that the cap ended its visible output, and `stop_sequence`, with the matched sequence in `stop_sequence`, when a runtime that declares `stopSequences` reports that a sequence ended it. A runtime `response` with an `error` member is not a stop reason: it answers `500 api_error` with the runtime's code. The adapter never reports `pause_turn`, `refusal`, or `model_context_window_exceeded`, because a turn's model calls are the runtime's own.

**`usage` [design].** `input_tokens` and `output_tokens` are the session's token counts accrued during the turn: the LLM proxy's extracted counts for a proxy-mode session (§4.9 "LLM Reverse Proxy", item 6), and the runtime's reported usage otherwise. They sum every model call of the turn. `cache_creation_input_tokens` and `cache_read_input_tokens` are 0, and `service_tier` is `standard`. "Limits" states the effect of the sum on a client that compacts its history by `input_tokens`.

### Streaming

With `stream: true`, the response is `text/event-stream` with named events whose `data.type` equals the event name **[design]**:

1. `message_start` at once, with the message's `id`, `model`, empty `content`, and zero usage. The gateway sends it after the session is bound, the warm reservation is written, or the join has matched, so an admission refusal is still a plain HTTP error.
2. `ping` every `anthropicMessages.pingIntervalSeconds`, default 15, operator-tunable, while no other event is due.
3. For each output part in order: `content_block_start`, one `content_block_delta`, and `content_block_stop`. A text block's delta is a `text_delta` with the part's whole text. A `thinking` block has a `thinking_delta` and then a `signature_delta`. A `tool_use` block has an `input_json_delta` with the whole input.
4. `message_delta` with `stop_reason` and the turn's usage, then `message_stop`.

Parts reach the adapter by either of these paths. Parts a Standard or Full runtime emits through `lenny/output` during the turn reach the adapter as `output` session events, and the adapter writes each as it arrives. Parts in the runtime's final `response` frame are written when the frame arrives. §28.5.3 states that the `response` frame holds only parts not already emitted through `lenny/output`, so no part is written twice. A failure after `message_start` is an `error` event carrying the Anthropic error object, followed by the end of the stream.

The OpenAI translators' practice of splitting finished text on whitespace is not copied. A Basic runtime, which has no `lenny/output`, streams all its blocks at the end of the turn, and the pings keep the connection open until then.

### Long turns

Every turn ends inside the HTTP request that started it **[design, O4]**. A non-streaming request waits for the turn to end, and the client's timeouts and any intermediary's idle timeout bound it. A client that runs long turns streams, as Anthropic recommends for requests over 10 minutes **[external]**: the pings keep the connection busy, and a client that wants the final message alone uses its SDK's helper that consumes the stream.

### `lenny/request_input` and elicitation

The adapter declares `SupportsElicitation: false` **[design, O4]**. A session that the adapter creates refuses both paths at once:

- A `lenny/request_input` call fails at once with the tool-call error `REQUEST_INPUT_UNAVAILABLE`, `details.surface: "anthropic-messages"`, so the runtime continues its turn without the answer. The call never blocks, and `maxRequestInputWaitSeconds` never runs.
- An elicitation that the session raises toward its client is answered with `decline` at once, and the adapter creates its sessions with `elicitationDepthPolicy: block_all` for delegated children, as §21.1 plans for the A2A adapter.

## AM-7 Client tools

Client tools on this surface use the mechanism of [`client-tools-design.md`](client-tools-design.md), which replaces revision 2's bridge of `tool_call` frames and open turns **[design, O4]**. This section states only the mapping of the Messages API onto it.

| Messages API | Client tools mechanism |
| :-- | :-- |
| `tools` entries without a server tool `type` | `conversation.clientTools` on every turn a client request causes: `name`, `description`, `input_schema` as `inputSchema`, and `strict`. |
| `tool_choice`, with `disable_parallel_tool_use` | `conversation.toolChoice` and the parallel limit, on every turn a client request causes. |
| A runtime `response` with `clientToolCalls` | The turn ends. The response carries any text parts of the turn followed by one `tool_use` block per call, with `id` set to `toolu_` and an opaque value the gateway maps to the runtime's call identifier on the session row, and `stop_reason: "tool_use"`. |
| The next request's final `user` message with `tool_result` blocks | A `message` whose `toolResults` member carries one result per pending call, each with the runtime's call identifier, its content as `MessagePart` entries, and `isError` from `is_error`. Text blocks in the same message after the results are the message's `input`. |

**Admission.** A request with a non-empty `tools` array of client tools requires a chosen revision that declares `capabilities.clientTools` and `capabilities.conversationInput`. The post-chain point checks it, so a revision without the flags is refused with `400 invalid_request_error` before any pod is claimed. Lenny's reference runtimes do not declare `clientTools` in v1 (T3).

**Validation.** The gateway passes `tool_choice` and the parallel limit to the runtime and validates the runtime's calls against the declared tool names, each call's arguments against its `input_schema`, `tool_choice`, and the parallel limit. A violation fails the turn with `500 api_error`, `error_code` from that design, and `x-should-retry: false`, and the gateway counts it per runtime revision. The gateway does not repair a response (T6).

**Pending results.** A request that continues a conversation whose last assistant message ends in `tool_use` must carry a `tool_result` block for every pending `tool_use` id. A missing, extra, or unknown `tool_use_id` answers `400 invalid_request_error`, as T5 states for a client message without the pending results. A warm turn reaches the session by the digest of "AM-5" as for any turn. A cold turn delivers the calls and their results as unmarked entries, so a runtime with conversation input continues from the history in a new session.

**Runtime tools.** Runtime tools stay inside the turn. Their calls and results do not appear in the response, because the Messages API reserves `server_tool_use` for Anthropic's typed server tools, and an SDK client would read an unknown server tool name as a defect. They remain visible through the session's transcript and its MCP and REST event streams. A runtime tool call held for approval under §7.2 or SO-5a holds the turn. The approver resolves it through the approvals API with the `lenny-session-id` the response headers carry, and a client that expects a long hold streams.

**Turns no client request caused.** Client tools are offered only in turns a client request caused. A turn that another session's message caused carries no client tools, and its calls, if any, fail validation with `no_tools_declared` ("AM-5", "Messages from other sessions").

**Delegation.** Client tools are not delegated to children (T7).

## AM-8 Errors

Every refusal on the adapter's routes uses the Anthropic error envelope **[design]**. Lenny's error code sits in `error.details.error_code`, and Lenny's details sit beside it in `error.details`, which is where the SDKs read a typed error's details **[external]**:

```json
{
  "type": "error",
  "error": {
    "type": "permission_error",
    "message": "the session needs linked accounts: connector crm",
    "details": {
      "error_code": "LINKS_REQUIRED",
      "connectors": ["crm"],
      "userLinks": []
    }
  },
  "request_id": "req_01J9..."
}
```

`details` is omitted when Lenny has no code for the outcome. Revision 2 placed `code` and `details` directly in `error`, and the review moved them.

| Lenny outcome | HTTP status | `error.type` |
| :-- | :-- | :-- |
| 400 and 422 validation errors | 400 | `invalid_request_error` |
| 401, including no credential, an invalid bearer, an Anthropic API key, and conflicting credential headers | 401 | `authentication_error` |
| 403, including `LINKS_REQUIRED`, `TENANT_NOT_FOUND`, and impersonation refusals | 403 | `permission_error` |
| 404, including an unknown runtime | 404 | `not_found_error` |
| 409, including `TARGET_NOT_READY` from a warm forward | 409 | `conflict_error` |
| 413 | 413 | `request_too_large` |
| 429 and the admission rate limits, with `Retry-After` | 429 | `rate_limit_error` |
| 503 `SESSION_CREATION_FAILED` and `POOL_DRAINING`, with `Retry-After` | 503 | `overloaded_error` |
| 503 `CREDENTIAL_POOL_EXHAUSTED`, or its successor under [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md) | 503 | `overloaded_error` |
| 504 | 504 | `timeout_error` |
| Any other 5xx | 500 | `api_error` |

The table keeps Lenny's 503 for a claim failure in place of Anthropic's 529 for `overloaded_error`, because 503 is the status other Lenny surfaces and load balancers expect for the same condition, and the SDKs retry both. A link refusal carries its details as CA-4 defines them, so a client that cannot pass `lenny.deferConnectors` can show the links to make.

**`x-should-retry` [design].** The SDKs retry 5xx responses and honour `x-should-retry` before any status rule **[external]**. The gateway sets `x-should-retry: false` on these classes of failure, because a retry would repeat the work or the failure:

- Every failure after dispatch, that is, after the turn's input reached the runtime: a runtime error, an `OUTPUT_SCHEMA_MISMATCH`, a client tool validation failure, and a session that ended during the turn. A retry would run the turn's side effects again in a new session.
- `503 CREDENTIAL_POOL_EXHAUSTED`, or its successor under the upstreams design. No credential becomes available within the SDK's retry window, so the retries add load and delay the error.

A failure before dispatch with `Retry-After`, such as `SESSION_CREATION_FAILED`, `POOL_DRAINING`, or a rate limit, carries no `x-should-retry` and keeps the SDK's default retry. A failure after `message_start` on a stream is an `error` event, whose headers were already sent, and the SDKs do not retry a stream that has started.

The auth middleware answers an unauthenticated request on the adapter's routes in this envelope rather than Lenny's REST envelope, by reading the protocol the route's adapter declares.

## AM-9 Models and refused endpoints

**`GET /v1/models` [design, O6].** A request that carries `anthropic-version`, or that carries `?adapter=anthropic-messages`, receives the Anthropic list. Every response of the route carries `Vary: anthropic-version`, so a cache never serves one format for the other. The list holds `data` as `ModelInfo` objects with `type: "model"`, `id` (the runtime name or the alias), `display_name` (the runtime's display name, or its name, and for an alias the alias), `created_at` in RFC 3339, `lifecycle: "active"`, and `first_id`, `has_more`, and `last_id`. `after_id`, `before_id`, and `limit` (default 20, at most 1000) page by runtime name. `lifecycle[]` is accepted, and every runtime is `active`. The body also carries `adapterCapabilities` of the Anthropic adapter, as §9.1 requires. A request with neither selector receives the OpenAI list as today. The runtimes listed are the runtimes the caller may use, filtered as `handleListModels` filters them, and the aliases listed are those RT-4's "Discovery" shows the caller.

**Unknown capabilities [design].** The gateway does not know what a runtime's model accepts, so it vouches for no model capability. Each `CapabilitySupport` entry in `capabilities` carries `supported: false`, because the schema types `supported` as a boolean, and `line`, `max_input_tokens`, `max_tokens`, `deprecated_at`, and `retires_at` are null **[external]**. Revision 2 reported `supported: true` for `image_input`, `pdf_input`, and `structured_outputs` while stating that the gateway cannot know them, and the review found the contradiction. A client reads the adapter's accepted inputs from AM-3 and the runtime's behaviour from its documentation.

**`GET /v1/models/{model_id}` [design].** Dispatched by the same selectors. Returns one `ModelInfo` for a runtime or a listed alias, or `404 not_found_error` for a name outside the caller's view, identical to an unknown name.

**Refused endpoints [design].** `POST /v1/messages/count_tokens`, every path under `/v1/messages/batches`, and every path under `/v1/files` answer `404 not_found_error` in the Anthropic envelope with a message naming the endpoint as not served by Lenny. `/v1/files` is mounted only for requests carrying `anthropic-version`, so no REST route is shadowed.

## AM-10 Tenancy, billing, routing, and other designs

**Tenancy [design].** The tenant comes from the bearer under §10.2. The scoped model namespace selects an environment, and TS-1's list of explicit paths gains it on `/v1/messages`. The digest includes tenant, owner, and `metadata.user_id`, so continuity never crosses any of them.

**Billing [design].** The adapter creates ordinary sessions. Billing events, the session's usage, and BR-2's pod windows attribute to the session's tenant and owner. A held session's pod time between turns is billed as any held session's is. `metadata.user_id` becomes the session label `anthropic-user-id`, which usage reports and billing filters can group by, and it changes no attribution. The adapter emits no billing event of its own.

**Routing and revisions [design].** The requested runtime or alias enters the `PreRoute` chain as for any create, and a route or interceptor can change it. The per-runtime admission limits judge the final runtime, after the chain (RT-4, "Before the chain"). A cold turn of an existing conversation is a new create and can land on another target or revision, under the rollout key of RV-2, which defaults to the user and so keeps a user on one revision while the traffic is unchanged. A warm turn stays on its session's runtime and revision. `runtimeOptions` reach the create only through `lenny.runtimeOptions` (O10), so a revision whose schema requires options refuses a request without them at the post-chain point, under RV-7. The flags `conversationInput`, `conversationParameters`, and `clientTools` are read from the chosen revision.

**MCP upgrade [design].** The adapter has no MCP features. MP-3's URL-mode elicitation for missing links has no counterpart, and a link refusal is the `permission_error` of AM-8. MP-4's Tasks progress has no counterpart beyond streaming. The Messages API's beta `mcp_servers` and the `mcp_toolset` tool are refused, because connectors are registered by administrators under CA-4. A runtime's own MCP servers and clients are unaffected.

**Connector authorization [design].** The link check runs at the post-chain point of a cold turn, as for any create. A client sends `lenny.deferConnectors` to defer a connector.

**Security oversight and impersonation [design].** Tool-use approvals hold runtime tool calls as on any surface. Today the OpenAI translators admit an impersonation bearer on session input, which [`user-credential-delivery-design.md`](user-credential-delivery-design.md) records as F-13.3.26. Whatever rule closes that finding applies to this adapter in the same change, and the adapter joins the finding's list of surfaces.

## Worked example

Alice, a user of tenant `acme`, uses the Anthropic Python SDK with `base_url` set to the gateway and her Lenny bearer as the API key, which the SDK sends in `x-api-key`. The runtime `support-agent` declares `multi_turn` with injection and `conversationInput`, and its pool has `conversationContinuity: "platform"`.

1. Alice sends `model: "support-agent"`, `system: "Answer as Acme support."`, `max_tokens: 1024`, and one user message, "What is our refund policy?". The middleware records her bearer and strips `x-api-key`. The history holds no assistant message, so the gateway creates session `S`. The `PreRoute` chain leaves the runtime unchanged, revision 7 is chosen, and the link check passes. The runtime receives `input` with the question, one marked entry, and `conversation.instructions` with the system text. It answers "Refunds are accepted within 30 days." The gateway stores the keyed digest of the conversation including that reply, completes the request record, keeps `S`, and returns `id: "msg_S.1"` with `lenny-session-id: S` and `lenny-runtime-revision: 7`.
2. Alice sends the question, the reply, and a new user message, "And for EU orders?". The digest of the history up to the reply matches `S`'s turn 1. The gateway reserves turn 2, writes the request record, and delivers only "And for EU orders?". Her connection drops while the runtime works, and the SDK retries the same request. The request digest matches the running request record, so the retry joins turn 2 and receives its answer.
3. Alice is idle past `maxClientIdleSeconds`, and `S` is suspended and released. She sends the conversation with a further question. The digest matches `S`, but `S` is not `running`, so the gateway creates session `S2` and delivers the earlier messages as unmarked entries. The runtime starts from the history.
4. Bob, also of `acme`, sends the same text as Alice's step 2. His owner differs, so no digest matches, and he receives a cold turn in a session of his own.

## Limits

- **Edited history runs cold.** A client that edits, summarizes, or reorders earlier messages, or changes `system` or `tools`, produces no digest match, and every such request creates a session. A client that compacts its history on each request, as long-running agents do, never reuses a session.
- **Summed usage can trigger client compaction.** `usage.input_tokens` sums the input of every model call of the turn. A client that reads it as the size of its context, as some agent clients do to decide when to compact, sees a value far above its conversation's size and compacts early, after which its requests run cold.
- **Transcript rendering loses structure.** A runtime without conversation input receives history as labelled text and never receives the hints or enforced parameters.
- **Client tools need runtime work.** A runtime must declare `clientTools` and implement [`client-tools-design.md`](client-tools-design.md).
- **Turns from other sessions are not in responses.** A turn that another session's message caused reaches the transcript and the REST and MCP event streams, and no Messages response. A client that reads only its responses learns of such a turn only when the runtime refers to it in a later turn the client caused. A cold turn takes its history from the client, so it lacks such a turn.
- **A request can wait for another session's turn.** On a runtime that does not declare `steer`, a request that arrives while a turn another session's message caused runs waits for that turn's `response`, and a non-streaming client's timeout bounds the wait.
- **Streaming is per part.** A text block arrives as one delta when the runtime emits the part. A Basic runtime streams nothing but pings until its turn ends.
- **Non-streaming turns are bounded by the client.** A non-streaming request holds its connection for the whole turn, so the SDK's 10-minute limit, the client's timeouts, and intermediaries' idle timeouts bound it, and a turn longer than they allow needs streaming.
- **A retry before the reservation repeats work.** The in-flight join matches a request record, which the adapter writes when it reserves the turn. A retry that arrives before the first attempt reserved its turn, for example while admission runs, finds no record and starts a second turn, with any side effects the runtime's tools have.
- **`anthropic-beta` is ignored.** A beta that changes behaviour without a request field the adapter refuses has no effect, and the client receives no error for it.
- **Thinking signatures are not verified.** A `thinking` block in history is accepted with any signature, and a Lenny-issued signature is not valid at Anthropic's API. A conversation started on Lenny cannot continue on Anthropic's API with its thinking blocks.
- **Short-lived credentials.** A Lenny bearer expires, so a program that reads a static key from its environment fails at expiry until it obtains a new one. The grant M8 asks for lets the SDKs' workload identity federation refresh it.

## Alternatives rejected

- **Pass the request through to Anthropic's API.** The LLM proxy can already call Anthropic for a pod. Exposing it to clients would make Lenny a model gateway with no runtime, workspace, or session, which the project's decision against a gateway LLM sidecar excludes.
- **Explicit continuation through the beta `thread` parameter now.** `thread: {"type": "continue", "previous_message_id": ...}` maps onto held sessions directly **[external]**. It is a beta whose fields can change before general availability, and stock clients do not send it. Once Anthropic marks it stable, a thread identifier maps to a session as `previous_response_id` does on the Open Responses adapter, in addition to digest matching (O2).
- **An unkeyed SHA-256 digest.** Revision 2 used one. A reader of the digest table could test a guessed conversation for a known tenant and owner against it. The key costs one secret and a cold turn per conversation at rotation.
- **Open turns.** Revision 2 ended an HTTP response while the turn continued, at a client tool call, at `lenny/request_input`, or at a wait limit with `pause_turn`. It needed a runner detached from the request, a resumption path keyed by digest, and rules for a client that never returned. Streaming serves long turns, and the turn-ending mechanism of [`client-tools-design.md`](client-tools-design.md) serves client tools (O4).
- **Refuse every `anthropic-beta` value.** Revision 2 did. The SDKs and agent clients send beta headers routinely, and the refusal blocked requests whose fields the adapter would have accepted. Field-level refusals enforce the same behaviour (M7).
- **A Lenny header naming the session.** Clients would need custom code, and a digest gives the same result for stock clients.
- **Reporting runtime tool calls as `server_tool_use` blocks.** The SDKs type `server_tool_use` names to Anthropic's server tools, and Lenny's tool calls would need translation to fake names.
- **Hosting Message Batches as asynchronous sessions.** It needs results storage, expiry, and cancellation semantics of its own, and no request asked for it.
- **A union body for `GET /v1/models`.** It would carry one `adapterCapabilities` for both adapters.
- **Token counting by estimate.** The gateway does not know the runtime's model, tokenizer, or prompt, and a wrong count misleads cost checks.

## Specification changes

| Section (by heading) | Change |
| :-- | :-- |
| §15 External API Surface, "Built-in adapter inventory" and "Three tiers of pluggability" | Add `AnthropicMessagesAdapter` at `/v1/messages`, protocol `anthropic-messages`, V1. Add a paragraph "Anthropic Messages conversation sessions" stating AM-3 to AM-7, in the form of "Open Responses conversation sessions", and reference that paragraph's warm conditions, reservation write, and hold rule. Add the protocol identifier to the `AdapterCapabilities.Protocol` examples. |
| §15.1 REST API | Add `OUTPUT_SCHEMA_MISMATCH` and `REQUEST_INPUT_UNAVAILABLE` to the error catalog. `GET /v1/models` and `GET /v1/models/{model_id}` answer in the Anthropic format when the request carries `anthropic-version` or `?adapter=anthropic-messages`, with `Vary: anthropic-version`. |
| §15.2.1 REST/MCP Consistency Contract | Add the adapter to the contract-testing list of built-in adapters. |
| §15.4.1 Message Format and Binary I/O Requirements, "Translation Fidelity Matrix" and "`MessageEnvelope` — Unified Message Format" | Add the Anthropic Messages column. Add the `conversation` member with `entries`, the `new` mark, `instructions`, and `parameters`, and its rules from AM-4, referencing the client tool members of [`client-tools-design.md`](client-tools-design.md). |
| §15.5 API Versioning and Stability | State the accepted `anthropic-version` values, the refusal of an unknown newer version, and the rule for adopting a new Anthropic version. |
| §5.1 Runtime | Add `capabilities.conversationInput` and `capabilities.conversationParameters`, as versioned fields. |
| §28.5.3 Intra-pod | Add the Anthropic column of the canonical type registry. |
| §7.2 Interactive Session Model | State that `lenny/request_input` fails at once with `REQUEST_INPUT_UNAVAILABLE` in a session whose surface cannot carry the question. |
| §9.1 Runtime Discovery | Add the Anthropic entry. |
| §10.2 Authentication | State `x-api-key` on the adapter's routes and the models routes, its normalization into the request's credential and its removal, the refusal of an `sk-ant-` value without echo, and the conflicting-headers refusal. |
| §10.6 Environment Resource and RBAC Model | Add the scoped model namespace on `/v1/messages`. |
| §12.8 Compliance Interfaces | Name `conversation_turns` and `messages_requests` in the session erasure. |
| §29.3 Interactive message send | Cover the forwarded warm Messages turn, with the credential and `anthropic-version` it carries. |
| §15.6 Client SDKs | State that the Anthropic SDKs work against the gateway with a Lenny bearer, and how `x-should-retry` steers their retries. |

## Non-specification changes

- `pkg/gateway/environment/translator`: the adapter in its own files, the shared conversation input builder and transcript renderer that the OpenAI translators use as well, the keyed digest and its canonicalization, the in-flight join, and the request records.
- `pkg/gateway/session/executor`: one envelope per turn with `conversation`, and a turn whose completion does not depend on the HTTP request's context.
- `pkg/gateway/middleware/auth`: `x-api-key` on the adapter's routes, its normalization and removal, the `sk-ant-` refusal, and the protocol-aware unauthenticated envelope.
- `pkg/gateway/sessionserver/holder_forward.go`: the forward of a Messages warm turn adds `anthropic-version` to the copied headers.
- `pkg/gateway/sessionserver/runtimes.go`: the Anthropic models format, the `?adapter=` selector, `Vary`, and the single-model route.
- The runtime adapter and the runtime SDKs under `sdks/`: the `conversation` member and the capability flags.
- `migrations/`: `conversation_turns` and `messages_requests`.
- `cmd/lenny-gateway`: registration, and flags with Helm values for `anthropicMessages.maxRequestBytes`, `pingIntervalSeconds`, and the reference to `digestKey`.
- `docs/api/anthropic-messages.md`, a tutorial for the Anthropic SDKs, and a correction to `docs/getting-started/architecture.md` that names the LLM proxy's path as `/llm-proxy/v1/messages`.

## Defects found while verifying

These are defects in the current tree. Revision 2 files each in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, under the ID in the table's "BUILD-GAPS" column. Verification for filing corrected DF-3 and DF-4, and the rows below state the corrected scope.

| ID | Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- | :-- |
| DF-1 | Every message of a translator request reaches the runtime as its own turn, with its role dropped. A Chat Completions request with a system message and history runs one turn per message, and each prior assistant reply arrives as client input. | `PodExecutor.Send` in `pkg/gateway/session/executor/pod.go` calls `runTurn` per message, and `resolveFromBlock` ignores `Message.Role`. | F-15.1.80 |
| DF-2 | A cold Open Responses continuation re-runs every earlier user turn of the conversation on the new session. | `serveCold` sends `append(prior, t.msgs...)` through `exec.Send`, with `prior` from `continuity.rehydrate`. | F-15.1.81 |
| DF-3 | The OpenAI translators drop `tools` and sampling parameters without an error, so a client that sends tools receives prose. The Open Responses translator also drops non-string content without an error. The Chat Completions translator refuses non-string content with `400 invalid_request_error`, whose message calls the body invalid JSON, so revision 1's claim that both translators drop it was wrong for Chat Completions. | `OpenAIChatCompletionsRequest` and `OpenResponsesRequest` model neither field, and decode without `DisallowUnknownFields`. `normalizeInput` reads `content` as a string. | F-15.1.82 |
| DF-4 | A Chat Completions or Open Responses request with an empty `model` is bound to the runtime named `echo`, in production as in dev mode. It runs there when a runtime of that name is registered, as the chart's commented Day-1 seed registers one, and fails with an error naming `echo` otherwise. | `NewOpenAIChatHandler` and `NewOpenResponsesHandler` default `DefaultRuntime` to `echo`, the handlers use it when `model` is empty, and no file under `cmd/lenny-gateway` sets `DefaultRuntime`. | F-15.1.83 |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Field and block dispositions for both API versions, including the beta per-message fields and the enforced-parameter rules by declaration. The RFC 8785 canonical form, the merging of consecutive same-role messages and adjacent text blocks, the exclusion of `lenny.labels`, the inclusion of `metadata.user_id`, and the keyed digest. The ordered entries with the new input marked, including a trailing `system` message, and transcript rendering. Output mapping, `stop_reason` by declaration, error mapping with `error.details.error_code`, `x-should-retry` by class, and `tool_use` id mapping. The `sk-ant-` refusal and its message without the value. |
| 3 | The official Anthropic Python, TypeScript, and Go SDKs at their current majors, and one release that sends `2023-06-01`, run against the gateway for create, streaming, client tools, errors, and the models routes, with the SDKs' typed parse of `error.details`, their handling of `x-should-retry: false`, and the models routes with `Vary`. Recorded SSE streams are checked against the event order of AM-6. The envelope's `conversation` member against the runtime SDKs. |
| 4 | A child's message to a held session between client requests, delivered on arrival, whose envelope carries no `conversation.clientTools`, whose output reaches the REST and MCP event streams and no response, and after which the next request's digest still matches; that request written into the running turn on a `steer` runtime with its client tools, and waiting for the turn's `response` on another runtime, each answered with the output from the consumption of its own message. Warm and cold turns on the compose stack, a digest after an idle release, owner, tenant, and `metadata.user_id` separation, a warm turn whose `lenny.labels` changed, `lenny.runtimeOptions`, a link refusal, a retried request that joins its running turn, the request record behind a `request-id`, and the OpenAI translators' history delivery. |
| 5 | A held session on Kind through several turns, a warm turn forwarded to the coordinating replica with a bearer sent in `x-api-key` and with `anthropic-version` intact, a client tool round trip under [`client-tools-design.md`](client-tools-design.md), and an immediate `REQUEST_INPUT_UNAVAILABLE` for a runtime that calls `lenny/request_input`. |
| 7a | Concurrent continuations of one conversation: one warm reservation wins and the other goes cold. Concurrent digest writes for identical conversations. Concurrent retries of one request: one turn runs and every retry joins it. |
| 8 | Pod loss during a turn, with the next request served cold from history. Replica loss during a forwarded warm turn. A dropped client connection during a turn, after which the turn completes and a retry joins it. |
| 9 | `x-api-key` validation, its removal before handlers and logs, conflicting credential headers, an `sk-ant-` value refused without echo in the body, logs, audit, and metrics, a digest match across owners, tenants, and user identifiers, the absence of a digest test without the key, and an impersonation bearer. |
| 10 | Conformance cases for `conversationInput` and `conversationParameters`: entry roles and marks, instructions, hints, and the enforced parameters a runtime declares. |
| 11 | The new API page and tutorial against the gateway. |

## Validation owed

- Which header each current Anthropic SDK sends when configured with an API key, and whether the SDKs send `anthropic-version` on the models routes. "Sources" records the documentation only.
- Whether the SDKs' typed parsers accept an `error.details` whose `error_code` is not one of Anthropic's values, and an empty `signature` value. The Python SDK's union is discriminated by `error_code`.
- Whether the TypeScript and Go SDKs honour `x-should-retry` as the Python SDK does.
- What a runtime does with a turn when the gateway's wait is cancelled, so the proposal can make a turn's completion independent of the HTTP request's context.
- Whether common agent clients read `usage.input_tokens` to decide when to compact.
- The label key rules of §14 for `anthropic-user-id`.
- Whether the session event bus delivers `output` events to an adapter on the replica serving the request during a forwarded warm turn or a join.
- Whether `runtimecapoverride` can clear a capability flag that a revision declares, as AM-4 requires.

## Instructions for reviewers of other designs

- [`runtime-routing-design.md`](runtime-routing-design.md): revision 12 states O9 in RT-4: a route's requested name may name no runtime, and the models list shows the caller's aliases. Its reviewer confirms that the OpenAI translators resolve an alias as this adapter does. The `PreRoute` payload's input for a create from this adapter is the final user content, with the earlier entries in a separate member that an interceptor cannot change.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): RV-1's field classes should list `capabilities.conversationInput` and `capabilities.conversationParameters` as versioned, gateway-read fields, and the per-tenant capability override should narrow both.
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): TS-1's explicit paths and the list of root creators that write a resolved delegation value gain `/v1/messages`. M8: its reviewer considers an RFC 7523 `jwt-bearer` grant on `POST /v1/oauth/token` that accepts the body the Anthropic SDKs send under workload identity federation (`assertion`, `federation_rule_id`, `organization_id`, `service_account_id`, and an optional `workspace_id`), maps the federation rule to a tenant and a principal, and returns a Lenny bearer as `access_token`, so an SDK that points its base URL at the gateway refreshes its credential without a static key.
- [`client-tools-design.md`](client-tools-design.md): it replaces revision 2's AM-7 bridge. Its reviewer confirms the Messages mapping of "AM-7", the `x-should-retry: false` on a validation failure, and that its members sit in this design's `conversation` member beside `entries`.
- [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md): its reviewer names the successor of `CREDENTIAL_POOL_EXHAUSTED`, which AM-8 answers with `overloaded_error` and `x-should-retry: false`.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): the "Clients" list under CA-4 gains the Anthropic SDKs, which can defer through `lenny.deferConnectors`.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md): no change. The adapter offers no MCP feature.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): no change. Held sessions and their pod windows are ordinary sessions.
- [`security-oversight-design.md`](security-oversight-design.md): SO-5a's approvers act on sessions this adapter creates through the session ID in the response headers.
- [`message-delivery-design.md`](message-delivery-design.md): no change. This design states that design's O3 under "AM-5", "Messages from other sessions", and AM-7, and relies on its MD-4 attribution and its MD-8 response cut.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| AM-A | Conversation input (AM-4) for the runtime contract, with ordered entries and the new-input mark, and the OpenAI translators moved onto it, closing DF-1 and DF-2. | None. |
| AM-B | The adapter's endpoint, authentication, headers, request mapping, errors, models routes, and refused endpoints (AM-1, AM-2, AM-3, AM-8, and AM-9), with cold single-shot turns, incremental streaming, and the immediate refusal of `lenny/request_input` and elicitation (AM-6). | AM-A. |
| AM-C | Continuity (AM-5): keyed digests, warm turns, holding, forwarding with the credential and `anthropic-version`, the in-flight join, request records, and erasure. | AM-B. |

AM-A also closes DF-3, by refusing the fields the OpenAI translators cannot honor under D3, and DF-4, by refusing a request with no `model`. Revision 2's AM-D, open turns, is removed (O4). Revision 2's AM-E, client tools, is replaced by [`client-tools-design.md`](client-tools-design.md), which lands after AM-A and alongside AM-A's fix of F-15.1.82 (T8). The Messages mapping of "AM-7" lands with that design's proposal once AM-B has landed.

## Sources

Each page below was read on 2026-11-09.

- [Messages: create](https://platform.claude.com/docs/en/api/messages/create), the request, content blocks, tools, and response object. `https://docs.anthropic.com/en/api/messages` redirects to `https://platform.claude.com/docs/en/api/messages`.
- [Beta Messages: create](https://platform.claude.com/docs/en/api/beta/messages/create), for the beta-only fields, `thread`, and `mcp_servers`.
- [Versions](https://platform.claude.com/docs/en/api/versioning) and [Migrating to API version 2026-10-21](https://platform.claude.com/docs/en/api/migration-2026-10-21).
- [API overview](https://platform.claude.com/docs/en/api/overview), for the headers table, and [Authentication](https://platform.claude.com/docs/en/manage-claude/authentication), for the `sk-ant-api...` key format, the legacy `x-api-key` header, and workload identity federation.
- [WIF reference](https://platform.claude.com/docs/en/manage-claude/wif-reference), for the `jwt-bearer` exchange at `POST /v1/oauth/token`, its fields, the `sk-ant-oat01-` token prefix, and the SDK's environment variables and credential precedence.
- [Beta headers](https://platform.claude.com/docs/en/api/beta-headers).
- [Errors](https://platform.claude.com/docs/en/api/errors), for error types, the error shape, `request-id`, retries, the 10-minute limit on non-streaming requests, long requests, prefill on Claude 4.6 and later, and `thinking.display`.
- [Streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming) and [Citations](https://platform.claude.com/docs/en/build-with-claude/citations), for `citations_delta`.
- [Mid-conversation system messages](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages), for the placement of a `system` message as the last entry and the beta per-message fields `clear_at`, `output_config.effort`, `tool_addition`, and `tool_removal`.
- [Count tokens](https://platform.claude.com/docs/en/api/messages/count_tokens).
- [List models](https://platform.claude.com/docs/en/api/models/list) and [Get a model](https://platform.claude.com/docs/en/api/models/retrieve), for the `ModelInfo` schema and its nullable fields.
- [Message Batches](https://platform.claude.com/docs/en/api/messages/batches).
- [Files](https://platform.claude.com/docs/en/build-with-claude/files), [Structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs), and [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).
- [`message_create_params.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/message_create_params.py) in `anthropic-sdk-python` on `main`, which confirms that the sampling parameters are gone from the current SDK.
- [`_base_client.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/_base_client.py) in `anthropic-sdk-python` on `main`, whose `_should_retry` obeys `x-should-retry`, and [`types/shared/error_details.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/shared/error_details.py) with [`types/shared/error_object.py`](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/shared/error_object.py), which type `error.details` as a union discriminated by `error_code`.

## Review history

An adversarial review read revision 2 on 2026-11-09. Revision 3 applies its design fixes and the owner's answers of the same date, which settle every open decision except O9. Revision 4 applies the owner's decision of 2026-11-09 on O3 of [`message-delivery-design.md`](message-delivery-design.md). Revision 5 records the owner's decision of 2026-11-10 on O9.

### Revision notes

- Revision 1, 2026-11-09: first draft, written against the tree and specification on branch `design/v1-cleanup` and the Anthropic documentation of the same date.
- Revision 2, 2026-11-09: files the defects found while writing this design in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, as F-15.1.80 to F-15.1.83. Verification for filing narrowed DF-3's non-string content claim to the Open Responses translator and stated when DF-4's `echo` default runs and when it fails.
- Revision 3, 2026-11-09: applies the owner's answers of 2026-11-09 and the adversarial review of revision 2. The owner's answers, as recorded: "O1: accept a Lenny bearer in `Authorization` or `x-api-key`. The auth middleware normalizes `x-api-key` into the credential and strips the header. An `sk-ant-` value gets a specific 401 that never echoes it. Conflicting values get a 401." "O2 and M1: warm continuation by an HMAC-SHA256 digest that includes `metadata.user_id`; RFC 8785 canonicalization; consecutive same-role messages and adjacent text blocks merged; `lenny.labels` excluded from the digest; an in-flight join so a retry reattaches to the running turn; the `thread` beta stays rejected. When stable, a thread id would map to a session, like `previous_response_id`." "O3: the `conversation` member carries ordered entries with the new input marked, so a trailing system message has a place. The beta per-message fields are refused." "O4, M3, and M6: no open turns (AM-D removed). Long turns use streaming. Client tools come from the new client-tools design, which replaces AM-7's bridge. `lenny/request_input` and elicitation are refused at once on this surface (the adapter declares `SupportsElicitation: false`)." "O5 and M4: the runtime declares which parameters it honours; undeclared non-default `stop_sequences` and `thinking.display` are refused; `effort` and `thinking` pass as hints; `max_tokens` is per message: it caps the turn's visible output, and only for runtimes that declare it. It never maps to the session budget." "O6: `GET /v1/models` chooses its format by `anthropic-version` or `?adapter=anthropic-messages`, sends `Vary: anthropic-version`, dispatches `/v1/models/{id}` the same way, and reports unknown capabilities as false or null." "O7 and M5: accept `2026-10-21` and `2023-06-01`. Refuse an unknown newer version with an error listing the supported ones." "O8: removed with open turns." "O9: unchanged." "O10: `lenny` is create-only. Its `labels` are excluded from the digest, and a warm turn ignores the object." "M2: reuse the Open Responses hold rule of §15.2 'Open Responses conversation sessions'. The adapter adds no timer of its own." "M7: accept `anthropic-beta` and ignore it. Field-level refusals enforce behaviour." "M8: a request to tenant-scoping-identity for a `jwt-bearer` grant on `/v1/oauth/token`, for SDK Workload Identity Federation." The hold rule's paragraph sits in §15 before §15.1 in the tree, so this revision cites it as §15 "Open Responses conversation sessions". The review's design fixes, as the owner listed them: the forwarded warm turn now carries its credential and `anthropic-version`; the trailing system message has a place among the marked entries; the error envelope moves Lenny's code to `error.details.error_code` with Lenny's details beside it; `x-should-retry: false` is set on every failure after dispatch and on `503 CREDENTIAL_POOL_EXHAUSTED` or its successor; the runner detached from the request goes with open turns; and the models route no longer reports capabilities it cannot vouch for. The revision adds the per-request record that links a `request-id` to its session and turn, and the limit that summed `usage.input_tokens` can trigger client compaction. AM-D and AM-E leave "Proposal split and order", and `REQUEST_INPUT_UNAVAILABLE` joins the error catalog changes.
- Revision 4, 2026-11-09: applies the owner's decision of 2026-11-09 on O3 of [`message-delivery-design.md`](message-delivery-design.md). The owner asked "wouldn't it be simpler to just deliver the message whenever it arrives?" and then answered "yes". A message from another session to a held session is delivered on arrival under the ordinary intent rules. "AM-5" gains "Messages from other sessions" with the two rules: client tools are offered only in turns a client request caused, and a turn that no client request caused never appears in a Messages response, whose content is the output of the client's own turn. The conversation digest is unaffected, because it covers only what the client sent and what Lenny returned. AM-7's mapping offers the client's tools on every turn a client request causes. The high-level design, the decisions, requirement AMR-13, "Limits", the tier 4 tests, the labels, and the reviewer instructions change to match. Two points go beyond the owner's words and are stated for review: a request written into a turn another session's message caused brings its own client tools, and `usage` counts the model calls that start after the runtime consumed the request's message.
- Revision 5, 2026-11-10: records the owner's decision on O9, given in the words "go with the recommendation". A model name that is no runtime reaches a runtime through a runtime route whose requested name is the alias, which [`runtime-routing-design.md`](runtime-routing-design.md) revision 12 adds to RT-4. "AM-3" accepts an alias as `model`, "AM-9" lists the caller's aliases, "AM-10" states that the per-runtime admission limits judge the final runtime, and the term "Alias" joins "Terms". No decision is open.
