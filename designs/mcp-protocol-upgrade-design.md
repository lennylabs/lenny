# MCP protocol upgrade

Status: design for owner review, revision 8, 2026-11-15, with no independent review yet. Revision 8 applies the owner's answer of 2026-11-15 on tool progress ("2.1: ok", RP D11) to the sections that still govern Lenny: the connector client requests and forwards a connector's progress notifications ("MP-6"), and the reference bridge sends them to its harness ("MP-5"). Revision 7 applies O4, O5, and O11 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md) to the moving sections. Nothing here is built or staged. Every decision is settled: the owner settled O7 on 2026-11-13. The owner chose on 2026-11-13 to move every client protocol adapter, MCP included, out of Lenny into separate first-party repositories with optional Helm subcharts in front of the REST API (decision D6); [`rest-only-client-api-design.md`](rest-only-client-api-design.md) governs what leaves. Revision 6 applies that design's O8, which the owner settled on 2026-11-14 in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something": the banner below and a mark under each part state which sections still govern Lenny and which move to the MCP adapter project, and nothing is deleted or moved. It reads against [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md), [`user-credential-delivery-design.md`](user-credential-delivery-design.md), [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md), [`runtime-protocol-design.md`](runtime-protocol-design.md) revision 9, and [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) revision 5, all as implemented. Revision 3 applies the owner's decisions of 2026-11-12 and 2026-11-13 that reach this design: the adapter runs no MCP server and the runtime SDKs' reference MCP bridge serves MCP to harnesses (RP D1, RP-5), the adapter-local tools are retired (RP D5), `CH-RUNTIMEOPS` merges into `CH-MSGSOCK` (RP O7), integration levels give way to capabilities (RP D3), and `type: mcp` runtimes are removed (AR D1). "Decisions" lists what the owner has settled and what remains open. The design stages no specification edit; the changes go through `change-proposal` after review. Proposals carry the label MP-n, and requirements carry the label MPR-n. "Review history" at the end records the revisions.

> **Adapter project banner.** Under [`rest-only-client-api-design.md`](rest-only-client-api-design.md), Lenny serves a REST client API alone, and the MCP protocol mapping moves to the MCP adapter's repository, `lenny-adapter-mcp`. Only the sections marked as Lenny's still govern Lenny. The sections marked as moving belong to the MCP adapter project: they stay in this file unchanged until that repository takes them, govern the adapter rather than Lenny, and stage no edit to Lenny's specification. The owner settled this as O8 of that design on 2026-11-14, in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something", so nothing here is deleted or moved.
>
> - **Lenny's:** "MP-5", "MP-6", **Inside the pod.** of "MP-3", the bullets of "MP-7" other than the client SDK bullet, the part of "MP-8" that names the versions of the reference bridge and the connector client, decisions D3, D6, O1, and O3, requirements MPR-5 and MPR-6, and proposals 3 and 4. D1, D2, and O2 govern Lenny for the bridge, the connector client, and the management server.
> - **Moving to the MCP adapter project:** "MP-1", "MP-2", "MP-3" apart from **Inside the pod.**, "MP-4", the compatibility window of "MP-8" for the gateway's `/mcp`, "MP-9", decisions D4, D5, and O4 to O7, requirements MPR-1 to MPR-4, and proposals 1 and 2. D1, D2, and O2 govern the adapter for its client-facing endpoint. Lenny's own playground and client SDKs speak REST under "RO-7" of that design, so the playground transport of "MP-9" and the client SDK bullet of "MP-7" govern neither.
> - **Mixed:** a section without a mark, such as "High-level design", "Limits", "Specification changes", "Documentation", or "Tests", keeps its rows about the reference bridge, the connector client, and the management server in Lenny, and its rows about the gateway's `/mcp` endpoint, the attach stream, Tasks, and the playground move.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09, or on 2026-11-13 for the claims revision 3 adds. |
| **[external]** | Read on modelcontextprotocol.io or in the MCP repositories on 2026-11-09. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

[§15.2](../spec/15_external-api-surface.md#152-mcp-api) names MCP `2025-03-26` as Lenny's target version and calls it the "latest stable at time of writing". The sentence entered the specification on 2026-04-04, in commit `10814415b`, and was wrong on that date, because `2025-06-18` and `2025-11-25` had already been published. Nothing checked it against the MCP site afterwards. As of 2026-11-09 the current MCP version is `2026-07-28` **[external]**.

The pinned version is out of step with the rest of the specification and the tree:

- §15.2 lists Tasks and elicitation under "MCP features used". `2025-03-26` defines neither, and the gateway emits both in formats that it defines itself, which match no MCP version. The attach stream also emits `resource_link` content, which first appears in `2025-06-18` **[source]**.
- The MCP surfaces in the tree disagree with each other. The gateway answers `2025-03-26` and `2024-11-05`, the outbound connector client sends `2025-06-18`, and the Python and TypeScript client SDKs request `2025-06-18` and receive `2025-03-26` **[source]**.
- The adapter's MCP servers inside the pod ignore the client's requested version and always answer `2025-03-26`, although §15.4.3 says they also accept `2024-11-05` **[source]**. [`runtime-protocol-design.md`](runtime-protocol-design.md#rp-3-tool-calls-on-frames) removes these servers: tool calls travel as frames, and the reference MCP bridge that the runtime SDKs ship serves MCP to a harness ("RP-3", "RP-5").
- Connector authorization rejected MCP's own signal for "authorize at this URL first" (`URLElicitationRequiredError`, code `-32042`) partly because Lenny speaks `2025-03-26` ("CA-5" under [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-5-propagating-the-failure)).

This design moves every MCP surface Lenny runs to the two most recent MCP versions, `2026-07-28` and `2025-11-25`. It replaces the formats that Lenny defined for elicitation and Tasks with MCP's own, and it states a rule that keeps the version claim checked against its source.

## Scope

**Added:**

- Support for `2026-07-28` and `2025-11-25` on every MCP server and client Lenny ships: the gateway, the reference MCP bridge, the connector client, the management server, and the client SDKs ("MP-1", "MP-5", "MP-6", and "MP-7").
- A dual-era gateway endpoint that serves `2025-11-25` clients through the `initialize` handshake and `2026-07-28` clients statelessly ("MP-1").
- MCP-conformant elicitation in both versions, including URL mode and the combined missing-links response at session create ("MP-3").
- The MCP Tasks extension on `2026-07-28` ("MP-4").
- A specification rule that names the source and check date of the supported versions ("MP-8").

**Changed:**

- Tool results, error results, and tool descriptors, so that they validate against both MCP schemas ("MP-2").
- The client SDKs, the reference MCP bridge in the runtime SDKs, the vendored schemas, and the tests that pin versions ("MP-5" and "MP-7").

**Removed:**

- `2025-03-26` and `2024-11-05` on every surface Lenny serves, with no deprecation window (D2).
- The Lenny notification `notifications/tasks/statusUpdate` and its status vocabulary ("MP-4").
- The `lenny/error` content block type and the top-level `x-lenny-stability` tool field ("MP-2").
- The WebSocket transport at `/mcp/v1/ws`, with the web playground moved to Streamable HTTP ("MP-9").

**Out of scope:**

- OAuth protected resource metadata and the `WWW-Authenticate` challenge on `/mcp`. "Login routing (TS-7)" under [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#login-routing-ts-7) owns them. "MP-1" states only what MCP requires of them.
- The connector link failure inside the pod, which "CA-5" owns. "MP-3" records how its reasoning changes.
- The runtime protocol between the adapter and a runtime process, the tool frames, and the removal of the adapter's intra-pod MCP servers and of the MCP nonce, which [`runtime-protocol-design.md`](runtime-protocol-design.md) owns. "MP-5" covers only the MCP versions the reference bridge serves.

**Removed by another design.** Revision 2 also upgraded the adapter's MCP client to a `type: mcp` runtime and listed the `/mcp/runtimes/{name}` passthrough as out of scope. [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) removed `type: mcp` runtimes with both of these on 2026-11-13 (AR D1, "AR-1"), so this design upgrades neither.

## Terms

| Term | Meaning |
| :-- | :-- |
| MCP version | A dated revision of the Model Context Protocol specification, such as `2025-11-25`. MCP increments it only for changes that break compatibility. |
| Handshake era | The MCP versions up to and including `2025-11-25`. A client opens with an `initialize` request, the server answers with the version it will speak, and an HTTP server may assign a session identifier (`Mcp-Session-Id`). |
| Stateless era | The MCP versions from `2026-07-28`. No handshake or protocol session exists. Every request carries its version and the client's capabilities in `_meta`, under the keys `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities`. |
| Dual-era server | A server that serves both eras on one endpoint. A request that carries the stateless-era `_meta` is served statelessly, and an `initialize` request selects handshake-era behaviour for that session. `2026-07-28` permits this **[external]**. |
| Input-required result | A `2026-07-28` result with `resultType: "input_required"`. It carries `inputRequests`, a map of elicitation, sampling, or roots requests that the client fulfils, and an optional opaque `requestState`. The client then retries the original request with `inputResponses`. MCP calls this pattern a multi round-trip request. It replaces the server-initiated requests of the handshake era. |
| URL-mode elicitation | An elicitation that asks the user to open a URL, such as an authorization page, instead of filling in a form. It exists from `2025-11-25`. |
| `URLElicitationRequiredError` | The `2025-11-25` error, code `-32042`, which a server returns when a request cannot proceed until the user completes one or more URL-mode elicitations. `data.elicitations` lists them. `2026-07-28` retires the code and carries the same content in an input-required result. |
| Tasks extension | The optional `2026-07-28` extension `io.modelcontextprotocol/tasks`. A server returns a task handle in place of a result. The client polls with `tasks/get`, answers input requests with `tasks/update`, and stops a task with `tasks/cancel`. `2025-11-25` has an experimental core form of Tasks with a different wire format. |
| `server/discover` | The `2026-07-28` request that every server implements. It returns the supported versions, the server's capabilities, and optional instructions. |
| Surface | One MCP server or MCP client that Lenny runs, with its transport and version handling. "Current state" lists them. |

### Terms from the platform

| Term | Meaning |
| :-- | :-- |
| Gateway `/mcp` | The client-facing MCP endpoint of `lenny-gateway`, at `POST /mcp` and `POST /mcp/environments/{name}`, with a WebSocket variant at `/mcp/v1/ws` ([§15.2](../spec/15_external-api-surface.md#152-mcp-api)). |
| Harness | An agent program a runtime process wraps, such as Claude Code or Codex, which reaches tools as an MCP client ([`runtime-protocol-design.md`, "Terms"](runtime-protocol-design.md#terms)). |
| Tool frames | The `tool_call`, `tool_result`, `list_tools`, `tool_list`, and `tool_cancel` frames on `CH-MSGSOCK`, through which a runtime process or a bridge calls Lenny's platform tools and connector tools ("RP-3"). They carry no MCP, so they have no MCP version. |
| Reference MCP bridge | The MCP server that the Go, Python, and TypeScript runtime SDKs ship, in process or as the static binary `lenny-mcp-bridge` that a harness launches over stdio. It serves one MCP server per tool server to the harness and turns each MCP request into tool frames ("RP-5"). It is the one MCP server Lenny ships that runs inside an agent pod. |
| Tool server | A named set of tools a session may call: `platform` for Lenny's platform tools, and `connector:<id>` for each connector ("RP-3"). |
| Connector client | The gateway's outbound MCP client that calls a registered connector's MCP server (§9.3). |
| Management MCP server | The MCP server of `lenny-ops` at `/mcp/management` (§25.12). |
| Attach stream | The SSE response stream that `lenny/attach_session` opens when the request sends `Accept: text/event-stream`. Lenny resumes it by sequence number (§15.2, "Event-stream resume"). |
| Combined missing-links response | The single refusal at session create that names every account link and connector link the user still needs. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) and [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-4-link-check) agreed on it. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| CA-2, CA-4, CA-5, and CAR-7 | [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-5-propagating-the-failure) | The connector client's pending OAuth `resource` parameter; the link check at session creation and the combined missing-links response; how a connector link failure reaches a running session; and the requirement that no authorization URL reaches a pod, a runtime, or a model. |
| TS-7 | [`tenant-scoping-identity-design.md`, "Login routing (TS-7)"](tenant-scoping-identity-design.md#login-routing-ts-7) | The protected resource metadata and the `WWW-Authenticate` challenge on `/mcp`. |
| RP D1, RP D3, RP D5, RP D11, RP O7, RP-2, RP-3, RP-5, and RPR-14 | [`runtime-protocol-design.md`](runtime-protocol-design.md#rp-5-reference-mcp-bridge) | The owner's decisions that tools travel as frames and the SDKs ship the bridge, that capabilities replace integration levels, that the adapter-local tools are retired, and that a pending call's progress reaches the runtime as `tool_progress` under the `toolProgress` capability; the merge of `CH-RUNTIMEOPS` into `CH-MSGSOCK`; the capability catalog; tool calls on frames; the reference MCP bridge; and the requirement that a runtime author can write a compatible bridge in another language. |
| SL-11 | [`session-lifecycle-design.md`, "SL-11"](session-lifecycle-design.md#sl-11-external-mappings) | The session states that the Tasks mapping of "MP-4" reads, and the A2A mapping that design keeps. |
| AR D1 and AR-1 | [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md#ar-1-the-runtime-type) | The owner's removal of `type: mcp` runtimes and service mode, and what leaves with `type: mcp`: the dedicated endpoint `/mcp/runtimes/{name}` and the adapter's MCP client to a runtime. |

## High-level design

Every MCP server Lenny runs speaks `2026-07-28` and `2025-11-25` and refuses older versions. The gateway's `/mcp` endpoint decides per request which era a client uses. A `2026-07-28` client sends its version and capabilities on every request, so the gateway keeps no protocol state for it. A `2025-11-25` client opens with `initialize`, and the gateway issues an `Mcp-Session-Id` that names a small record of the negotiated version and the client's capabilities. Both paths reach the same tool handlers. Each version has its own serializer, so a result is written in the format of the version that the request negotiated.

The formats that Lenny defined for itself are replaced with MCP's own. A tool error becomes an MCP tool error with Lenny's error envelope in `structuredContent`, and Lenny metadata moves under `_meta`. An elicitation from a running session reaches a `2025-11-25` client as an `elicitation/create` request, and a `2026-07-28` client as the input requests of a task. A session's progress reaches a `2026-07-28` client through the Tasks extension. When session create needs links the user has not made, a client that declared URL-mode support receives one URL-mode elicitation per missing link and retries the same call afterwards. Any other client receives the tool error that connector authorization defines.

Inside the pod, the adapter runs no MCP server: a runtime calls Lenny's tools with tool frames, which carry no MCP version ("RP-3"). The reference MCP bridge that the runtime SDKs ship serves MCP to a harness, and it serves the same two versions under O1. The connector client, which calls servers Lenny does not control, prefers `2026-07-28` and falls back to older versions that the server answers with (O3).

## Decisions

Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), D4, D5, and O4 to O7 move to the MCP adapter project, and D3, D6, O1, and O3 are Lenny's. D1, D2, and O2 apply to both: the adapter takes the half that governs its client-facing endpoint, and Lenny keeps the half that governs the reference bridge, the connector client, and the management server.

| # | Decision | Status |
| :-- | :-- | :-- |
| D1 | Lenny serves the two most recent MCP versions, `2026-07-28` and `2025-11-25`. | **Settled**, in the owner's words "Only need to keep the last two versions", with the versions checked against modelcontextprotocol.io on 2026-11-09. |
| D2 | `2025-03-26` and `2024-11-05` are removed at once, with no deprecation window and no deprecation header, because nothing is deployed. | **Settled** by the project rule against compatibility shims before a first release. The owner can reopen it. |
| D3 | A separate design covers the upgrade. | **Settled**, in the owner's words "Write a separate design doc for the upgrade". |
| D4 | The combined missing-links response uses the tool error `details` today. This design adds the URL-mode form for clients that declare support for it ("MP-3"). | **Settled** as review question Q6, in the owner's word "ok". |
| O1 | The intra-pod MCP servers serve the same two versions as the gateway. A runtime whose MCP client offers only an older version cannot connect. | **Settled**, in the owner's word "yes". One policy covers every surface Lenny serves. The cost is in "Limits". Since the runtime protocol design removed the adapter's MCP servers (RP D1, 2026-11-11), the decision applies to the reference MCP bridge, the one MCP server Lenny ships inside the pod ("MP-5"). |
| O2 | Whether the MCP layers use the official Go SDK (`github.com/modelcontextprotocol/go-sdk`, v1.7.0 or later), which serves both eras, or stay hand-written. | **Settled** as a spike, in the owner's words "try the SDK". The spike tests the gateway's needs (its auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing), and its result decides one approach for every surface. "Validation owed" lists it. |
| O3 | Which versions the outbound connector client accepts. Lenny does not control connector servers. | **Settled**, in the owner's word "ok". The client offers `2026-07-28`, falls back to `initialize` with `2025-11-25`, and accepts any version the server answers with from `2025-03-26` on. D1 governs what Lenny serves, and third-party servers keep their own versions. Revision 2 also applied O3 to the adapter's client to a `type: mcp` runtime, which [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) removed on 2026-11-13 (AR D1). |
| O4 | Whether the WebSocket transport at `/mcp/v1/ws` stays. MCP defines no WebSocket transport, and `2026-07-28` keeps no connection state that a socket would hold. | **Settled**, in the owner's words "retire websockets and use SSE in the playground". The web playground's chat pane was its only user ([§27](../spec/27_web-playground.md)), and §27 gave no reason for a WebSocket over Streamable HTTP. "MP-9" states the change. |
| O5 | Whether the gateway offers the experimental core Tasks of `2025-11-25`, or offers Tasks on `2026-07-28` only. | **Settled**, in the owner's words "2026-07-28 only". The `2025-11-25` form is experimental, and its wire format differs from the extension's. |
| O6 | The deprecation window for future adoptions. §15.2 says the oldest version enters a six-month window when a new one is adopted, which serves three versions during the window. | **Settled** as a window counted in releases, in the owner's answer "C". The release that adopts a new version keeps the oldest as deprecated, and the first release at least six months later removes it. "MP-8" states the rule. |
| O7 | How a session that waits for its client appears on the Tasks extension ("MP-4"). Under the extension, `input_required` carries `inputRequests`, and an `idle` session has none. | **Settled** on 2026-11-13, in the owner's word "ok" to the recommendation, which also settles SL-Q1 of [`session-lifecycle-design.md`, "Decisions"](session-lifecycle-design.md#decisions). Each message a client sends gets its own task, which completes at the end of the turn that consumes the message. `input_required` appears only while that turn holds an elicitation or approval, so `idle`, `dormant`, and `awaiting_client_action` no longer map to `input_required`. Revision 4's rule, which mapped those states to `input_required` and set `taskId` to the session ID for both `lenny/create_session` and `lenny/send_message`, is retired. This design owns the mapping, which moves to the external MCP adapter under D6. |
| D5 | The handshake-era idle expiry of a session record in Redis, `gateway.mcp.handshakeSessionIdleTimeoutSeconds`, defaults to 3600 seconds ("Documentation"). | **Settled** on 2026-11-13, when the owner confirmed the consolidated list's defaults and names as written ("18–21 ok"). |
| D6 | Every client protocol adapter (MCP, A2A, OpenAI Chat Completions, Open Responses, and Anthropic Messages) moves out of Lenny into a separate first-party repository, maintained by the Lenny project, with an optional Helm subchart, in front of the REST API. | **Settled** on 2026-11-13, in the owner's words "ok, go with (b)", with the OpenAI and Anthropic adapters and the separate repositories with optional subcharts confirmed in the same answers. `rest-only-client-api-design.md`, which is being written, carries the change and governs what leaves. This design does not apply the removal. |

## Requirements

Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), MPR-1 to MPR-4 move to the MCP adapter project, and MPR-5 and MPR-6 are Lenny's.

- MPR-1. Every MCP server Lenny runs serves `2026-07-28` and `2025-11-25`, and refuses every other version with the error that the requested era defines.
- MPR-2. Every result, error, notification, and server request that Lenny emits validates against the published schema of the version the request negotiated.
- MPR-3. Lenny emits elicitations and Tasks in MCP's formats, and only to clients that declared the matching capability.
- MPR-4. The specification names each supported version with the URL and the date it was checked against the MCP site.
- MPR-5. The reference MCP bridge serves `2026-07-28` and `2025-11-25` to a harness, in process and over stdio, and refuses every other version with the error that the requested era defines.
- MPR-6. No authorization URL reaches a pod, a runtime, or a model through any form this design adds. This restates CAR-7 of connector authorization.

## Current state

All findings in this section are **[source]**.

| Surface | Version handling |
| :-- | :-- |
| Gateway `/mcp` (`pkg/gateway/mcpfabric/mcp`) | `CurrentProtocolVersion = "2025-03-26"` and `PreviousProtocolVersion = "2024-11-05"` in `version.go`. `negotiateVersion` answers the current version for an empty, newer, or unknown request, refuses an older one with `MCP_VERSION_UNSUPPORTED`, and sets the deprecation header for the previous version. No negotiated version is stored per connection, and every version is serialized the same way, although §15.2 "Version negotiation" item 4 requires both. `initialize` advertises `capabilities: {tools: {}}` and does not read the client's capabilities. `notifications/initialized` receives a method-not-found error. |
| Gateway WebSocket (`websocket.go`) | The same negotiation, with no deprecation header, under the subprotocol `lenny.mcp.v1`. |
| Intra-pod MCP servers (`pkg/adapter/mcp/server.go`) | `ProtocolVersion = "2025-03-26"`. `initializeResponse` ignores the requested version. `dispatch` handles `tools/list` and `tools/call`. The nonce check reads `_lennyNonce` from `initialize`. The runtime protocol design removes these servers, their sockets, and the nonce inside `initialize` ("RP-3"). |
| Connector client (`pkg/gateway/connectors/connectorinvoke/client.go`) | Sends `2025-06-18`, the `MCP-Protocol-Version` header, and `Mcp-Session-Id`, and records whatever version the server answers. |
| Management MCP server (`pkg/ops/mcp/server.go`) | `ProtocolVersion = "2025-03-26"`, with no negotiation. |
| Client SDKs (`sdks/client/`) | Go requests `2025-03-26`. Python and TypeScript request `2025-06-18`. |
| Runtime SDKs (`sdks/runtime/`) | Go, Python, and TypeScript request `2025-03-26` from the intra-pod servers. Under the runtime protocol design they speak tool frames to the adapter and ship the reference MCP bridge, which serves MCP to a harness ("RP-5"). |

The tree also holds the adapter's MCP client to a `type: mcp` runtime (`pkg/adapter/mcp/client.go`, which sends `2025-03-26`) and the `/mcp/runtimes/{name}` passthrough (`pkg/gateway/mcpfabric/mcpruntimes`, wired with a nil dispatcher). [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md#ar-1-the-runtime-type) removes both ("AR-1"), so this design does not upgrade them.

Formats that Lenny defined for itself:

- **Elicitation.** `projectElicitationCreate` in `pkg/gateway/mcpfabric/mcp/projection.go` emits `elicitation/create` with `message`, an optional `requestedSchema`, and `lenny/` keys under `_meta`, with no `mode` and no `url`. `projectToolUse` emits an approval as `elicitation/create` with no schema. Nothing consumes a client's reply to `elicitation/create` (BUILD-GAPS F-15.2.30). Clients answer through `lenny/respond_to_elicitation` and `lenny/dismiss_elicitation`. The "URL mode" in `pkg/gateway/mcpfabric/mcptools/elicitation.go` is Lenny's allowlist check on a URL argument. It never reaches the wire.
- **Tasks.** `projectStatusUpdate` emits `notifications/tasks/statusUpdate` with `taskId` set to the session ID and the statuses of §8.8, which include `submitted` and `canceled`. No `tasks/*` method exists (BUILD-GAPS 23.1-MED-001).
- **Tool errors and descriptors.** Error results carry a content block of type `lenny/error`, which no MCP schema admits. Tool descriptors carry a top-level `x-lenny-stability` field.
- **Tool names.** Gateway and intra-pod tools are named `lenny/<name>`. On tool frames the platform tools lose the prefix ([`message-delivery-design.md`, "MD-23"](message-delivery-design.md#md-23-tool-and-frame-names)).
- **Batching.** No server accepts a JSON-RPC batch. TEST-GAPS T-META.10 asks for a batch test, which no MCP version from `2025-06-18` permits.
- **Authorization.** `/mcp` accepts a bearer token and publishes no protected resource metadata (BUILD-GAPS F-10.2.39). No server checks `Origin`.

Tests pin the current versions in `pkg/gateway/mcpfabric/mcp/version_internal_test.go`, in `tests/tier3_contract/rest_mcp_consistency/published_schema_test.go`, and in `tests/tier5_e2e_kind/mcp_conformance_test.go`. The vendored schemas are `tests/testdata/mcp/schema/2024-11-05.schema.json` and `2025-03-26.schema.json`.

## MP-1 Gateway versions and negotiation

> **Moves to the MCP adapter project.** Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), this section governs the MCP adapter and no longer governs Lenny. It stays here unchanged until the adapter's repository takes it, and it stages no edit to Lenny's specification.

**Era selection.** The gateway reads each request to `/mcp`:

- A request whose `_meta` carries `io.modelcontextprotocol/protocolVersion` is a stateless-era request. A version other than `2026-07-28` is refused with `UnsupportedProtocolVersionError` (code `-32022`), HTTP 400, and `data.supported` listing both versions **[external]**.
- An `initialize` request selects the handshake era. The gateway answers `2025-11-25` when the client asks for it. For any other requested version it also answers `2025-11-25`, which is the handshake-era version it supports. The `2025-11-25` lifecycle rules have the client disconnect when it cannot speak the answer **[external]**.
- Any other request without stateless-era `_meta` must carry an `Mcp-Session-Id` that the gateway issued. A missing or unknown identifier receives HTTP 400 or 404, as the `2025-11-25` transport states **[external]**.

**Handshake-era sessions.** Under O5 (a) of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), the MCP adapter keeps no Redis record: it mints `Mcp-Session-Id` as a signed, self-contained token that holds the negotiated version, the client's capabilities, a digest of the bearer's subject, and an expiry, and answers `DELETE` with `405`. The rest of this paragraph states revision 6's gateway rule. On `initialize` the gateway issues an `Mcp-Session-Id` and stores the negotiated version and the client's declared capabilities in Redis under that identifier, with an idle expiry that a Helm value sets. Any replica can serve the next request. `DELETE` ends the session. The record holds no Lenny session state; a Lenny session and an MCP session are unrelated. **[design]**

**Headers.** On both eras the gateway requires the `MCP-Protocol-Version` header and refuses a value that differs from the negotiated or `_meta` version. The `2025-11-25` transport tells a server that receives no header to assume `2025-03-26`, which Lenny does not serve, so a missing header is refused with HTTP 400. On the stateless era the gateway also validates the `Mcp-Method` and `Mcp-Name` headers that `2026-07-28` requires. A request with an `Origin` header that the gateway does not accept receives HTTP 403. **[design]**

**Stateless-era methods.** The gateway implements `server/discover`, sets `resultType` on every result, and sets `ttlMs` and `cacheScope` on list results. `ping` and `logging/setLevel` exist only on the handshake era. **[design]**

**Handshake-era notifications.** `notifications/initialized` is accepted with no reply. **[design]**

**Serializers.** Each version has a serializer for results, errors, notifications, and server requests. The tool handlers produce one internal result, and the serializer of the negotiated version writes it. **[design]**

**Errors.** `MCP_VERSION_UNSUPPORTED` stays as Lenny's error code inside `data`. `MCP_PROTOCOL_VERSION_RETIRED` and the `X-Lenny-Mcp-Version-Deprecated` header stay for future retirements under the rule in "MP-8", with no version listed as retired or deprecated now. **[design]**

**Authorization.** MCP requires that `/mcp` publish OAuth protected resource metadata, answer an invalid token with HTTP 401 and a `WWW-Authenticate` challenge, and accept only tokens issued for it as the audience **[external]**. "TS-7" owns the metadata and the challenge. This design asks TS-7 to cite the `2026-07-28` authorization page and to state the audience check ("Instructions for reviewers of other designs"). **[design]**

## MP-2 Wire conformance

> **Moves to the MCP adapter project.** Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), this section governs the MCP adapter and no longer governs Lenny. It stays here unchanged until the adapter's repository takes it, and it stages no edit to Lenny's specification.

**Tool errors.** A failed tool call returns `isError: true`, a `text` content block with the error message, and Lenny's error envelope (code, category, and `details`) in `structuredContent`. Each Lenny tool declares an `outputSchema`, and a successful call returns its result in `structuredContent` as well as in `content`. The `lenny/error` content type is removed. **[design]**

**Descriptors.** `x-lenny-stability` moves to `_meta` under the key `lenny/stability`. Lenny tools carry `title` and annotations. **[design]**

**Tool names.** `2025-11-25` gives guidance on the characters a tool name uses. Whether `/` is admitted is not yet checked ("Validation owed"). When it is not, Lenny's tools are renamed from `lenny/<name>` to `lenny_<name>`, a change that D2 permits before a first release. **[design]**

**Resource links and notifications.** `resource_link` content, which `outputContentBlocks` already emits on the attach stream, becomes valid on both versions without change. The `notifications/lenny/*` namespace stays, because MCP permits notifications that an implementation defines. **[design]**

**Attach stream.** On the handshake era the attach stream keeps SSE event IDs and resumption by `Last-Event-ID`. `2026-07-28` removes SSE resumability and tells servers to ignore `Last-Event-ID`, so a stateless-era client resumes by calling `lenny/attach_session` again with `resumeFromSeq`, which the tool already accepts. Whether a `2026-07-28` POST response may still be an SSE stream is a validation item. **[design]**

## MP-3 Elicitation

> **Mixed.** **Handshake era.**, **Stateless era.**, and **Combined missing-links response at session create.** move to the MCP adapter project under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md). **Inside the pod.** is Lenny's, because it governs the reference MCP bridge.

**Handshake era.** An elicitation from a running session reaches a `2025-11-25` client as an `elicitation/create` request on the attach stream. A form elicitation carries `mode: "form"` and a `requestedSchema`; an approval request carries a schema with a single confirmation field. A URL-mode elicitation carries `mode: "url"`, `url`, and `elicitationId`, after the per-pool allowlist check that `elicitation.go` already makes. The gateway sends an elicitation only to a client that declared the matching mode under its `elicitation` capability, and it consumes the client's reply, which closes F-15.2.30. A client that declared no elicitation capability keeps using `lenny/respond_to_elicitation` and `lenny/dismiss_elicitation`. **[design]**

**Stateless era.** `2026-07-28` has no server-initiated requests. An elicitation from a running session reaches a `2026-07-28` client as an input request of the session's task under "MP-4": `tasks/get` returns `status: "input_required"` with `inputRequests`, and the client answers with `tasks/update`. A client that did not declare the Tasks extension uses the Lenny tools. **[design]**

**Combined missing-links response at session create.** When `lenny/create_session` needs links the user has not made:

| Client | Response |
| :-- | :-- |
| `2025-11-25`, declared URL-mode elicitation | `URLElicitationRequiredError` (`-32042`) with one URL-mode entry in `data.elicitations` per missing link. The client retries `lenny/create_session` once the user has completed them. |
| `2026-07-28`, declared URL-mode elicitation | An input-required result with one URL-mode elicitation in `inputRequests` per missing link. The client retries `lenny/create_session` with `inputResponses`. No session exists until the retry passes the link check. |
| Any other client | The tool error that "CA-4" of connector authorization defines, with every missing link in `details`. This is D4. |

Each form reaches only the client that called create, which is the user's own client, so MPR-6 holds. **[design]**

**Inside the pod.** "CA-5" rejected `-32042` on the intra-pod surface for two reasons: the URL would reach a client inside a pod with no browser, and the adapter spoke `2025-03-26`. The adapter now speaks no MCP: a connector link failure reaches the runtime as a `tool_result` with an `error` member ("RP-3"), and the reference bridge maps it to an MCP tool error for the harness. The first reason still applies under CAR-7, so the bridge never produces `-32042` or a URL-mode elicitation, and CA-5's custom error stands. **[design]**

## MP-4 Tasks

> **Moves to the MCP adapter project.** Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), this section governs the MCP adapter and no longer governs Lenny. It stays here unchanged until the adapter's repository takes it, and it stages no edit to Lenny's specification.

On `2026-07-28` the gateway advertises the Tasks extension in `server/discover`. Each message a client sends to a session gets its own task (O7). A client that declares the extension and sends a message, with `lenny/send_message` or as the first message of `lenny/create_session`, receives a task handle whose `taskId` names the session and the message, so two messages to one session never share a task ID. A create call that delivers no message returns no task. `tasks/get` reports the task with the extension's statuses (`working`, `input_required`, `completed`, `failed`, and `cancelled`) and, once the task ends, the result of the turn that consumed the message. The task completes at the end of that turn. `tasks/update` carries elicitation answers for the task's turn. `tasks/cancel` retracts the task's message while it is unconsumed, and otherwise interrupts the message's turn through R19 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), falling back to the suspend route when R19 answers `INTERRUPT_UNSUPPORTED` or `INTERRUPT_TIMEOUT` (that design's O11); the task reports `cancelled` in each case. This replaces revision 6's rule that `tasks/cancel` cancels the session, under that design's O4. Task notifications reach the client through `subscriptions/listen`. **[design]**

The mapping from Lenny's session states to the extension's statuses replaces the "Protocol mapping" and the MCP column of the "Session-level state mapping (supplementary)" of [§8.8](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema). This design owns the MCP mapping, and [`session-lifecycle-design.md`, "SL-11"](session-lifecycle-design.md#sl-11-external-mappings) defines the session states it reads and keeps the A2A mapping. `submitted` and the spelling `canceled` leave the MCP projection, and A2A keeps `canceled`. Lenny's own markers travel under `_meta` with `lenny/` keys, as all other Lenny metadata does on MCP, rather than in a `metadata` member. The markers are boolean keys under the task's `_meta`: `lenny/paused` carries the paused flag of SL-11's A2A table, and `lenny/resuming` marks a session that is resuming. A dormant session holds no open task, because the turn of its last message has ended, so the dormant flag of SL-11's A2A table has no MCP key. `notifications/tasks/statusUpdate` is removed. **[design]**

The table gives the status of an open task, the task of a message that the session has not finished processing, for each state of its session. A message waiting in the session's inbox has a `working` task. Once the turn that consumes the message ends, its task is `completed`, whatever state the session then enters. **[design]**

| Lenny session state | Status of an open task |
| :-- | :-- |
| `created`, `finalizing`, `ready`, and `starting` | `working` |
| `running` | `working`, or `input_required` with `inputRequests` while the task's turn holds an elicitation or approval |
| `idle`, `dormant`, and `awaiting_client_action` | No open task: the turn of the last message has ended, so its task is `completed` (O7) |
| `suspended` and `blocked` | `working`, with `lenny/paused: true` under `_meta` |
| `resume_pending` and `resuming` | `working`, with `lenny/resuming: true` under `_meta` |
| `completed` | `completed` |
| `failed` and `expired` | `failed`, with the error code in the task's result |
| `cancelled` | `cancelled` |

**Settled (O7, 2026-11-13).** Under the Tasks extension, an `input_required` task carries `inputRequests` that the client answers with `tasks/update`. An `idle`, `dormant`, or `awaiting_client_action` session has no input request, so revision 4's rows, which mapped those states to `input_required`, reported a status whose required content was absent. The finding comes from the spike of 2026-11-13 on a REST-only Lenny with external protocol adapters, finding 6, which also notes that a task ID equal to the session ID for `lenny/send_message` conflicts with the extension. The owner settled O7, which is the same question as SL-Q1 of [`session-lifecycle-design.md`](session-lifecycle-design.md#decisions), on one task per sent message, whose task ID names the session and the message, completed at the end of the turn that consumes that message. `input_required` appears only while the session holds an elicitation or approval for that turn. **[design]**

Under O5, a `2025-11-25` client receives no Tasks and follows a session through the attach stream. **[design]**

README and the documentation say that Lenny implements "MCP Tasks". The claim stays only once this proposal lands, and it then names the extension. **[design]**

## MP-5 Intra-pod servers

> **Lenny's.** This section still governs Lenny under [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

The adapter runs no MCP server inside the pod ("RP-3"). The one MCP server Lenny ships there is the reference MCP bridge of "RP-5", which a harness reaches in process or as the stdio program `lenny-mcp-bridge`. This section states the MCP versions the bridge serves; the runtime protocol design owns everything else about it. **[design]**

**Versions.** Under O1 the bridge serves `2026-07-28` and `2025-11-25` and refuses every other version, with the same era selection as "MP-1" adapted to a stdio stream or an in-process transport. Neither carries HTTP headers, so the version comes from `initialize` on the handshake era and from `_meta` on the stateless era. A stateless-era request at another version receives `UnsupportedProtocolVersionError` (`-32022`) with `data.supported` listing both versions. An `initialize` that asks for another version is answered with `2025-11-25`, and the harness disconnects when it cannot speak it. **[design]**

**No MCP nonce.** Revision 2 moved the intra-pod nonce from `initialize` into the `_meta` of each request. That placement leaves. The bridge proves its slot on its tool connection to the adapter, with the runtime connection handshake and a `hello` with `role: "tools"` ("RP-1"), and a stdio bridge speaks MCP only to the harness that launched it, so no MCP message carries a nonce on either era. **[design]**

**Methods.** The bridge implements `initialize` and `ping` on the handshake era, `server/discover` on the stateless era, `tools/list`, `tools/call`, `notifications/cancelled`, and the no-reply handling of notifications. It declares `tools: { "listChanged": true }` and never advertises `sampling` or `elicitation`. It sends `notifications/progress` to the harness for a `tool_progress` frame of a call whose `tools/call` carried a `progressToken`, with that token ("RP-5", RP D11). On the stateless era it sets `resultType` on every result, and on list results it sets the cache hints `ttlMs: 0` and `cacheScope: "private"` that "DA-5" states. A tool list change reaches a handshake-era harness as `notifications/tools/list_changed` and a stateless-era harness on its `subscriptions/listen` stream ("RP-5"). **[design]**

**Results.** The bridge writes tool results and tool errors in the format of the negotiated version, under the rules of "MP-2": a failed call returns `isError: true` with the error envelope from the `tool_result` frame's `error` member in `structuredContent`. **[design]**

**Conformance.** The bridge section that the runtime protocol design adds to the specification states these versions, so that a bridge written in another language serves the same ones (RPR-14). **[design]**

## MP-6 Outbound clients

> **Lenny's.** This section still governs Lenny under [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

Under O3, the connector client first sends a stateless-era request at `2026-07-28`. When the server answers with an error that marks it as handshake-era, the client sends `initialize` with `2025-11-25` and accepts any answered version from `2025-03-26` on. The client records the negotiated version and uses that version's format for the rest of its session with the server. The connector client sends the `resource` parameter in its OAuth requests, which "CA-2" of connector authorization already marks as pending. **[design]**

**Progress.** For a session whose effective capabilities include `toolProgress` ([`runtime-protocol-design.md`, "RP-2"](runtime-protocol-design.md#rp-2-capabilities-and-their-dependencies)), the connector client sends a `progressToken` unique to the call in the `_meta` of each `tools/call`, and reads the call's response as an SSE stream when the server answers with one, which "Validation owed" still has to confirm for the stateless era. It reads each `notifications/progress` that carries the call's token, with its `progress`, `total`, and `message`, and passes it to the gateway's connector call, which forwards it to the adapter as `tool_progress` under the rules of "RP-3" of that design. For any other session it sends no token, and MCP has a server send progress only for a request that carries one **[external]**. A progress notification changes nothing about the call: the call ends at its result, or at the client's `notifications/cancelled` when the runtime writes `tool_cancel`. The owner settled this on 2026-11-15 ("2.1: ok", RP D11). **[design]**

## MP-7 Management server, SDKs, schemas, and tests

> **Lenny's, apart from one bullet.** The management server, runtime SDK, reference runtime and tool, and schema bullets still govern Lenny under [`rest-only-client-api-design.md`](rest-only-client-api-design.md). The client SDK bullet governs neither Lenny nor the adapter: under "RO-7" of that design, Lenny's client SDKs speak REST and name no MCP version.

- **Management MCP server.** It moves to both versions with the "MP-1" negotiation. §25.12's planned `notifications/subscribe` is replaced with `subscriptions/listen` on the stateless era, which closes the gap that PROPOSAL-QUEUE C-45 tracks.
- **Client SDKs.** The Go, Python, and TypeScript client SDKs speak `2026-07-28`. They talk only to the gateway, which serves that version.
- **Runtime SDKs.** The reference MCP bridge in the Go, Python, and TypeScript runtime SDKs, and the static `lenny-mcp-bridge` binary, serve the versions of "MP-5". The SDKs' own conversation with the adapter is tool frames and has no MCP version.
- **Reference runtimes and tools.** `cmd/lenny-compliance` and the test stubs that act as MCP clients or servers move to the new versions. The reference runtimes `delegation-echo` and `elicitation-echo` call tools with frames under the runtime protocol design, `cmd/git-credential-lenny` leaves under the egress design's EG-3, and `cmd/runtimes/mcp-reference` leaves with `type: mcp` ("AR-1"), so none of them carries an MCP version.
- **Schemas.** The `2025-11-25` and `2026-07-28` schemas replace the vendored `2024-11-05` and `2025-03-26` schemas under `tests/testdata/mcp/schema/`.

## MP-8 Version rule in the specification

> **Mixed.** The rule that names each supported version with its URL and check date is Lenny's for the versions the reference bridge and the connector client serve and offer. The compatibility window for the gateway's `/mcp`, with its deprecation header, error, and metric, moves to the MCP adapter project under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).

The §15.2 "Target MCP spec version" sentence is replaced with a statement of the supported versions, each with the URL of its specification page and the date it was last checked against the MCP versioning page. A version claim without that URL and date is a specification defect. The "Compatibility policy" paragraph states the window that O6 settles. Lenny serves the two most recent MCP versions. The release that adopts a new version keeps the oldest previously served version as deprecated, serves it as a third version, and sends the `X-Lenny-Mcp-Version-Deprecated` header on its connections. The first release dated at least six months after the adopting release removes it. Counting the window in releases means an operator who skips releases still installs the deprecating release's warning before the removal, or reads `lenny_mcp_deprecated_version_active_sessions` before upgrading. The header is sent only for a deprecated version, so the gateway stops sending it for the previous version, as it does today. The example under "Session-lifetime exception for deprecated versions" is updated to the new versions. **[design]**

## MP-9 Web playground transport

> **Moves to the MCP adapter project.** Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), this section governs the MCP adapter and no longer governs Lenny. It stays here unchanged until the adapter's repository takes it, and it stages no edit to Lenny's specification. The web playground speaks REST under "RO-7" of that design, so this section's transport no longer applies to it.

The web playground's chat pane moves from the WebSocket at `/mcp/v1/ws` to Streamable HTTP on `/mcp`, and the WebSocket transport is removed. **[design]**

- **Requests.** The playground sends each MCP request with `fetch`, with its bearer in the `Authorization` header. `EventSource` cannot set that header, so the playground reads an SSE response as a stream from `fetch`.
- **Stream.** The chat pane opens the attach stream through `lenny/attach_session` and reads its SSE frames. Interrupt and Cancel are separate tool calls. A dropped stream resumes as "MP-2" states for the client's era.
- **Version.** The playground speaks `2026-07-28`, so it exercises the stateless era and the Tasks extension as a reference client.
- **Credentials.** The `lenny.bearer.*` entry in `Sec-WebSocket-Protocol`, and §27's rule that redacts it, are removed. Each request carries the current bearer, so a refreshed bearer takes effect on the next request and a revoked bearer fails it with `401` and `details.reason: "bearer_revoked"`. The WebSocket close code `4401` is removed.
- **Closing.** On browser close the playground sends `lenny/cancel_session` with the reason `playground_client_closed` through `fetch` with `keepalive: true`. A request that does not complete falls back to the idle timeout, as §27 states today.
- **Gateway.** `websocket.go` and the connection-takeover handling in the rate limiter, the panic recovery, the metrics, and the correlation middleware are removed. `lenny_playground_ws_connect_total` is removed.

## Limits

- **Older harness clients.** Under O1, a harness whose MCP client offers only `2025-06-18` or older cannot use the reference bridge. Claude Code offered `2025-11-25` in the connector spike, so it is unaffected **[source]**. Other harnesses are not surveyed ("Validation owed"). Such a harness's runtime can still call Lenny's tools with tool frames, or ship its own bridge (RPR-14).
- **Older user clients.** An MCP client that speaks only `2025-06-18` or older cannot connect to the gateway.
- **Tasks on the handshake era.** Under O5, a `2025-11-25` client has no Tasks and follows a session through the attach stream.
- **Stateless-era resumption.** A broken stream on the stateless era loses the in-flight request, and the client calls the tool again with `resumeFromSeq`.

## Alternatives rejected

- **Serving `2025-11-25` only.** It is no longer the current version. A stateless-only client could not connect, and a later move to `2026-07-28` would change the gateway twice.
- **Serving `2026-07-28` only.** Clients that have not adopted the stateless era, which is most of them as of 2026-11-09, could not connect.
- **Keeping Lenny's own elicitation and Tasks formats.** No MCP client renders them, so the "MCP features used" claim in §15.2 would stay untrue.
- **Retiring `2025-03-26` with a six-month window.** Nothing is deployed, so the window would protect no client and would keep a third serializer.

## Specification changes

| Section | Change |
| :-- | :-- |
| [§15.2](../spec/15_external-api-surface.md#152-mcp-api) | The supported versions with source and check date ("MP-8"). Era selection, sessions, headers, and `server/discover` ("MP-1"). The per-version elicitation forms and the combined missing-links response ("MP-3"). The Tasks extension ("MP-4"). The removal of `lenny/error` and of the WebSocket transport ("MP-9"). |
| §15.7 "Reference MCP Bridge", the section the runtime protocol design adds | The MCP versions the bridge serves, its era selection, its version refusal, and its `notifications/progress` for a call that carried a `progressToken` ("MP-5"). §15.4.3's intra-pod MCP servers and nonce wire format leave under the runtime protocol design, so this design adds nothing there. |
| §15.5 | The compatibility policy ("MP-8"). |
| [§27](../spec/27_web-playground.md) | The chat stream on Streamable HTTP, the bearer in the `Authorization` header, and the removal of the WebSocket close codes and the `lenny.bearer.*` carrier ("MP-9"). |
| [§8.8](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema) | The MCP protocol mapping to the Tasks extension's statuses ("MP-4"). |
| §9.2 and §9.3 | URL-mode elicitation in MCP's format, and the connector client's versions, `resource` parameter, and progress token with the forwarding of the connector's progress notifications ("MP-3" and "MP-6"). |
| §15.1 | The error catalog entries that change with "MP-1" and "MP-2". |
| §25.12 | The management server's versions and `subscriptions/listen` ("MP-7"). |
| §23 | Differentiator 6 names the Tasks extension. |

## Non-specification changes

- Code: the gateway `/mcp`, the connector client, the management server, the client SDKs, and the reference bridge in the runtime SDKs, with the per-version serializers, the Redis session record, the reply consumer for `elicitation/create`, and the playground's chat pane in `pkg/gateway/mcpfabric/playground/ui/app.js`. The intra-pod servers of "Current state" are removed by the runtime protocol design rather than upgraded.
- Chart: the Helm value `gateway.mcp.handshakeSessionIdleTimeoutSeconds` ("Documentation").
- `README.md` and the documentation, as "Documentation" lists.
- `BUILD-GAPS.md`: F-15.2.30, F-10.2.39 (cross-referenced to TS-7), 23.1-MED-001, and the stale §25.12 M9.
- `TEST-GAPS.md`: T-META.10 is withdrawn, because batching is gone from every supported version. T-8.8.5 changes its expected vocabulary. T-15.2.7, T-15.2.8, T-15.5.7, and T-15.7.7 are restated against the new versions.

## Documentation

Each proposal carries the documentation for its own surface, and the specification changes land first. Pages describe behaviour without section numbers.

| Location | Change | Proposal |
| :-- | :-- | :-- |
| `docs/api/mcp.md` | The supported versions `2026-07-28` and `2025-11-25`, each with its specification URL and check date; era selection per request; the `Mcp-Session-Id` record of a handshake-era client and its idle expiry; the required `MCP-Protocol-Version` header and, on the stateless era, `Mcp-Method` and `Mcp-Name`; `server/discover`; the refusals: `UnsupportedProtocolVersionError` (`-32022`, HTTP 400) on the stateless era, HTTP 400 for a missing or mismatched `MCP-Protocol-Version` header, HTTP 400 or 404 for a missing or unknown `Mcp-Session-Id`, and HTTP 403 for an `Origin` the gateway does not accept; tool errors in `structuredContent`; `lenny/stability` under `_meta`; the tool name form "MP-2" settles; and the resumption of the attach stream on each era. | 1 |
| `docs/api/mcp.md` and `docs/client-guide/` | Elicitation per era and mode, including the capability a client must declare to receive each form, and the three forms of the combined missing-links response with the retry that creates the session; the Tasks extension on `2026-07-28` with one task per sent message, its task ID, completion at the end of the message's turn, its statuses, `input_required` only during an elicitation or approval, `tasks/update`, and `tasks/cancel`; that a `2025-11-25` client has no Tasks and follows a session through the attach stream. | 2 |
| `docs/tutorials/mcp-client-integration.md`, `docs/client-guide/`, and `docs/getting-started/` | Examples at `2026-07-28` and `2025-11-25`; that a client offering only `2025-06-18` or older cannot connect. | 1, 2 |
| `docs/runtime-author-guide/` | The versions the reference MCP bridge serves and its version refusal; that a harness whose MCP client offers only `2025-06-18` or older cannot use the bridge, and that its runtime can call tools with frames or ship its own bridge; that the bridge never sends a URL-mode elicitation or `-32042`, and maps a connector failure to an MCP tool error; and that it sends `notifications/progress` for a call whose `tools/call` carried a `progressToken`. | 3 |
| `docs/operator-guide/configuration.md` and the Helm values reference | `gateway.mcp.handshakeSessionIdleTimeoutSeconds`, the idle expiry of a handshake-era session record in Redis, default `3600`. | 1 |
| `docs/operator-guide/upgrades.md` | The compatibility window of "MP-8", counted in releases, the `X-Lenny-Mcp-Version-Deprecated` header, `MCP_PROTOCOL_VERSION_RETIRED`, and `lenny_mcp_deprecated_version_active_sessions` as the check before an upgrade. | 1 |
| `docs/operator-guide/` page for the management server | `lenny-ops` `/mcp/management` serves both versions, with `subscriptions/listen` on the stateless era. | 4 |
| `docs/reference/state-machines.md` and the web playground page | The mapping from session states to the status of an open task, with no open task for an `idle`, `dormant`, or `awaiting_client_action` session; the playground's chat stream on Streamable HTTP, its bearer in the `Authorization` header, and the removal of `/mcp/v1/ws`, the `lenny.bearer.*` carrier, and close code `4401`. | 2 |
| Connector pages under `docs/operator-guide/` | The versions the connector client offers and accepts, from `2025-03-26` on, the OAuth `resource` parameter, and that a connector's progress notifications reach a runtime that declares `toolProgress`. | 4 |
| `README.md` | "MCP Tasks" names the Tasks extension once proposal 2 lands. | 2 |

The owner confirmed the handshake-era idle expiry default of 3600 seconds on 2026-11-13 (D5).

## Tests

- Negotiation on `/mcp`: era selection, an unsupported version on each era, a missing or mismatched `MCP-Protocol-Version` header, an unknown `Mcp-Session-Id`, a session served by a second replica, `Origin` refusal, and `server/discover`.
- Schema validation of `initialize`, `tools/list`, `tools/call` results and errors, the attach stream's frames, and `elicitation/create` against both vendored schemas, on tier 3 and on the deployed gateway on tier 5.
- Elicitation: each mode on each era, the capability check, the reply consumer, and every row of the combined missing-links response, including the retry that creates the session.
- Tasks: one task per sent message with distinct task IDs for two messages to one session, a create call with and without a first message, the status of an open task in each session state, `input_required` only while the turn holds an elicitation or approval, completion at the end of the message's turn with no open task on an `idle`, `dormant`, or `awaiting_client_action` session, `tasks/update` with an elicitation answer, `tasks/cancel`, and a client without the extension.
- Reference bridge: both eras in process and over stdio, a request at an unsupported version on each era, `tools/list` cache hints on the stateless era, a `tool_result` error mapped to an MCP tool error, a `tool_progress` frame sent as `notifications/progress` with the harness's token and none for a call without one, and a list change notified on each era.
- Connector client: a stateless-era server, a handshake-era server, and a server that answers `2025-03-26`; a call of a session with `toolProgress` that carries a progress token and forwards the server's progress notifications on each era, and a call of a session without it that carries none.
- Playground: the chat stream over Streamable HTTP, a refreshed bearer on the next request, a revoked bearer, the close-time cancel, and a request to `/mcp/v1/ws` that receives 404.

## Validation owed

- The `2025-11-25` tool name guidance (SEP-986), and whether it admits `/`.
- Whether a `2026-07-28` POST response may be an SSE stream, which the attach stream and the connector client's progress forwarding ("MP-6") need.
- The part of the `2026-07-28` `schema.ts` that was not read: `CacheableResult`, `ServerCapabilities`, and the Tasks types.
- The Tasks extension's stability and its repository's current revision.
- The spike under O2: whether the Go SDK v1.7.0 or later can host the gateway's auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing. Check the release page for the current tag.
- A survey of the MCP versions that common harness clients offer, which sizes the cost of O1 for the reference bridge.

## Instructions for reviewers of other designs

- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): in "Why the raw 401 stops at the gateway" under "CA-5", rest the rejection of `-32042` on CAR-7 alone, since the adapter speaks no MCP and the reference bridge maps a `tool_result` error to an MCP tool error ("MP-3", "MP-5"). In "CA-4", name this design's per-version forms of the combined missing-links response for MCP clients.
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md): in step 1 of "Flow", replace "MCP clients receive the same URL as a URL-mode elicitation" with a reference to "MP-3".
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): in "Discovery" and "Login routing (TS-7)", cite the `2026-07-28` authorization page in place of the `2025-06-18` one, and state the audience check that MCP requires of `/mcp`.

- [`session-lifecycle-design.md`](session-lifecycle-design.md#decisions): record SL-Q1 as settled on 2026-11-13 together with O7 of this design, on one task per sent message completed at the end of its turn. "MP-4" names no `_meta` key for the dormant flag, because a dormant session holds no open task.

## Proposal split and order

Under O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), proposals 1 and 2 move to the MCP adapter project, and proposals 3 and 4 are Lenny's.

1. **Versions and conformance**: "MP-8", "MP-1", and "MP-2", with the vendored schemas. Every later proposal depends on the per-version serializers.
2. **Elicitation, Tasks, and playground**: "MP-3", "MP-4", and "MP-9". The playground moves once the Tasks extension exists. "MP-3"'s combined missing-links response lands with or after the link checks of connector authorization and user credential delivery.
3. **Bridge**: "MP-5", with or after the runtime protocol design's proposal that adds the reference bridge (RP-B), which supersedes revision 2's intra-pod part of "MP-5".
4. **Outbound clients and management server**: "MP-6" and the rest of "MP-7".

## Sources

- MCP versioning: https://modelcontextprotocol.io/specification/versioning
- Changelogs: https://modelcontextprotocol.io/specification/2025-06-18/changelog, https://modelcontextprotocol.io/specification/2025-11-25/changelog, and https://modelcontextprotocol.io/specification/2026-07-28/changelog
- `2025-11-25` lifecycle, transports, elicitation, and Tasks: https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle, https://modelcontextprotocol.io/specification/2025-11-25/basic/transports, https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation, and https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks
- `2026-07-28` versioning, Streamable HTTP, `server/discover`, elicitation, and authorization: https://modelcontextprotocol.io/specification/2026-07-28/basic/lifecycle, https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http, https://modelcontextprotocol.io/specification/2026-07-28/server/discover, https://modelcontextprotocol.io/specification/2026-07-28/client/elicitation, and https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization
- `2026-07-28` schema: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2026-07-28/schema.ts
- Tasks extension: https://modelcontextprotocol.io/extensions/tasks/overview
- Go SDK version table: https://github.com/modelcontextprotocol/go-sdk

## Review history

### Revision notes

- Revision 8, 2026-11-15: applies the owner's answer of that day, "2.1: ok", which [`runtime-protocol-design.md`](runtime-protocol-design.md) revision 9 records as D11, and changes these rules in the sections that still govern Lenny. "MP-6" gains **Progress.**: for a session whose effective capabilities include `toolProgress`, the connector client sends a `progressToken` with each `tools/call`, reads an SSE response when the server sends one, and passes the connector's `notifications/progress` for the call to the gateway, which forwards it as `tool_progress`. "MP-5" **Methods.** has the bridge send `notifications/progress` for a call whose `tools/call` carried a token. The §9.2 and §9.3 and §15.7 rows, the connector and runtime-author documentation rows, the bridge and connector client tests, and the validation item on SSE responses at `2026-07-28` follow. The status line's reads-against list names the current revision of each cited design.
- Revision 7, 2026-11-14: the final pass of that day, and changes these rules in the sections that move to the MCP adapter. Under O4 and O11 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), `tasks/cancel` retracts the task's message while it is unconsumed and otherwise interrupts its turn through R19, falling back to the suspend route when R19 refuses, in place of cancelling the session ("MP-4"). Under that design's O5 (a), the MCP adapter keeps no Redis record for a handshake-era session and mints `Mcp-Session-Id` as a signed, self-contained token ("MP-3"). The status line's reads-against list names the current revision of each cited design.
- Revision 6, 2026-11-14: applies O8 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), which the owner settled on 2026-11-14 in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something". The banner after the status line and a mark under each part, "Decisions", "Requirements", and "Proposal split and order" state which sections still govern Lenny and which move to the MCP adapter project, as that design's instruction to this one lists them. Nothing is deleted or moved, and no rule changes. The status line names that design without calling it a design being written, and its reads-against list names the current revision of `agent-runtimes-only-design.md`.
- Revision 5, 2026-11-13: applies the owner's answers of 2026-11-13 to the consolidated list of open decisions. O7 is settled in the owner's word "ok" on the recommendation, which also settles SL-Q1 of `session-lifecycle-design.md`: each message a client sends gets its own task, whose ID names the session and the message and which completes at the end of the turn that consumes the message, and `idle`, `dormant`, and `awaiting_client_action` no longer map to `input_required`. "MP-4" states the per-message task, gives the status of an open task for each session state, and drops the `lenny/dormant` key, which has no open task to carry it. The tests, "Documentation", and the instruction to the session lifecycle design follow. D5 records the owner's confirmation of the handshake-era idle expiry default of 3600 seconds. D6 records the owner's choice, in the words "ok, go with (b)", to move every client protocol adapter, MCP included, out of Lenny into a separate first-party repository with an optional Helm subchart in front of the REST API; `rest-only-client-api-design.md`, which is being written, governs what leaves, and this revision applies no removal. The status line's reads-against list names the current revision of each cited design.
- Revision 4, 2026-11-13: the read-only checks of the cross-design review of 2026-11-13. "MP-4" now holds the MCP mapping from session states to the Tasks extension's statuses, which [`session-lifecycle-design.md`](session-lifecycle-design.md#sl-11-external-mappings) staged with `submitted`, `metadata` members, and `canceled`. This design owns that mapping, and SL-11 points here for MCP and keeps A2A. The table states the current rule: no `submitted`, the pre-running states as `working`, the spelling `cancelled`, and Lenny's markers under `_meta` with `lenny/` keys. Decision O7 is new and open: the spike of 2026-11-13 on a REST-only Lenny found that `input_required` carries `inputRequests` under the extension, so an `idle` session should not map to it, with the recommendation of one task per sent message, completed at the end of its turn. "Labels from other designs" gains SL-11. The status line's reads-against list names the current revision of each cited design.
  - The last round of the same review, which changes no rule, names the `_meta` keys `lenny/paused`, `lenny/dormant`, and `lenny/resuming` in "MP-4", states in O7 and its recommendation that `idle`, `dormant`, and `awaiting_client_action` would no longer map to `input_required`, and cross-references O7 as the same question as SL-Q1 of `session-lifecycle-design.md`. It also cites the designs this one reads against at their current revisions.
- Revision 3, 2026-11-13: applies the runtime protocol design's decisions of 2026-11-11 and 2026-11-12 and the agent-runtimes-only design's decisions of 2026-11-13, and the cross-design review of 2026-11-13.
  - Tools travel as frames and the adapter runs no MCP server (RP D1, "RP-3"), so "MP-5" states the versions of the reference MCP bridge of "RP-5", and O1 applies to the bridge. The intra-pod nonce in `_meta` leaves, because the bridge proves its slot on its tool connection and carries no MCP nonce. The `CH-MCP-PLATFORM` row of "Specification changes" leaves with the channel, and the §15.4.3 row becomes the bridge section. The adapter-local tools (RP D5) and `CH-RUNTIMEOPS` (RP O7) carried no MCP version, so nothing else changes for them.
  - `type: mcp` runtimes are removed (AR D1, owner, 2026-11-13: "Remove non-agent runtimes altogether"). The adapter's MCP client to a `type: mcp` runtime leaves "Scope", "Terms", the high-level design, O3, "Current state", "MP-6", and the tests, which now govern the connector client alone, and the `/mcp/runtimes/{name}` passthrough leaves "Scope".
  - "MP-3" **Inside the pod.** rests CA-5's custom error on CAR-7 alone. "MP-7" drops the runtime SDKs' nonce and the reference runtimes and tools that no longer speak MCP. "Limits" names harnesses rather than runtimes.
  - "Labels from other designs" and "Documentation" are new. "Documentation" lists every setting, default, refusal, and runtime-facing rule this design introduces, under the owner's decision of 2026-11-13 that every design document all relevant details, and names the Helm value for the handshake-era idle expiry, `gateway.mcp.handshakeSessionIdleTimeoutSeconds`, with a default of 3600 seconds for owner review.
- Revision 2, 2026-11-09: the owner settled O1, O2, O3, O5, and O6, and settled O4 as the retirement of the WebSocket transport. Revision 1 said the specification gave no reason for the WebSocket. §27 uses it for the playground's chat pane, which "MP-9" moves to Streamable HTTP.
- Revision 1, 2026-11-09: first draft. Written after the owner settled Q6 of the design sync review and asked for a separate design that keeps the last two MCP versions.
