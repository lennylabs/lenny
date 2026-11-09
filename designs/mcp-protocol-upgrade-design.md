# MCP protocol upgrade

Status: design for owner review, revision 2, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 11, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 25, and [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 31. "Decisions" lists what the owner has settled and what remains open. The design stages no specification edit; the changes go through `change-proposal` after review. Proposals carry the label MP-n, and requirements carry the label MPR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on modelcontextprotocol.io or in the MCP repositories on 2026-11-09. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

[§15.2](../spec/15_external-api-surface.md#152-mcp-api) names MCP `2025-03-26` as Lenny's target version and calls it the "latest stable at time of writing". The sentence entered the specification on 2026-04-04, in commit `10814415b`, and was wrong on that date, because `2025-06-18` and `2025-11-25` had already been published. Nothing checked it against the MCP site afterwards. As of 2026-11-09 the current MCP version is `2026-07-28` **[external]**.

The pinned version is out of step with the rest of the specification and the tree:

- §15.2 lists Tasks and elicitation under "MCP features used". `2025-03-26` defines neither, and the gateway emits both in formats that it defines itself, which match no MCP version. The attach stream also emits `resource_link` content, which first appears in `2025-06-18` **[source]**.
- The MCP surfaces in the tree disagree with each other. The gateway answers `2025-03-26` and `2024-11-05`, the outbound connector client sends `2025-06-18`, and the Python and TypeScript client SDKs request `2025-06-18` and receive `2025-03-26` **[source]**.
- The adapter's MCP servers inside the pod ignore the client's requested version and always answer `2025-03-26`, although §15.4.3 says they also accept `2024-11-05` **[source]**.
- Connector authorization rejected MCP's own signal for "authorize at this URL first" (`URLElicitationRequiredError`, code `-32042`) partly because Lenny speaks `2025-03-26` ("CA-5" under [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-5-propagating-the-failure)).

This design moves every MCP surface Lenny runs to the two most recent MCP versions, `2026-07-28` and `2025-11-25`. It replaces the formats that Lenny defined for elicitation and Tasks with MCP's own, and it states a rule that keeps the version claim checked against its source.

## Scope

**Added:**

- Support for `2026-07-28` and `2025-11-25` on every MCP server and client in the tree ("MP-1", "MP-5", "MP-6", and "MP-7").
- A dual-era gateway endpoint that serves `2025-11-25` clients through the `initialize` handshake and `2026-07-28` clients statelessly ("MP-1").
- MCP-conformant elicitation in both versions, including URL mode and the combined missing-links response at session create ("MP-3").
- The MCP Tasks extension on `2026-07-28` ("MP-4").
- A specification rule that names the source and check date of the supported versions ("MP-8").

**Changed:**

- Tool results, error results, and tool descriptors, so that they validate against both MCP schemas ("MP-2").
- The intra-pod nonce handshake, which today rides on `initialize` ("MP-5").
- The client SDKs, the runtime SDKs, the vendored schemas, and the tests that pin versions ("MP-7").

**Removed:**

- `2025-03-26` and `2024-11-05` on every surface Lenny serves, with no deprecation window (D2).
- The Lenny notification `notifications/tasks/statusUpdate` and its status vocabulary ("MP-4").
- The `lenny/error` content block type and the top-level `x-lenny-stability` tool field ("MP-2").
- The WebSocket transport at `/mcp/v1/ws`, with the web playground moved to Streamable HTTP ("MP-9").

**Out of scope:**

- OAuth protected resource metadata and the `WWW-Authenticate` challenge on `/mcp`. "Login routing (TS-7)" under [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#login-routing-ts-7) owns them. "MP-1" states only what MCP requires of them.
- The connector link failure inside the pod, which "CA-5" owns. "MP-3" records how its reasoning changes.
- The `/mcp/runtimes/{name}` passthrough, which has no dispatcher in production ("Current state").

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
| Intra-pod MCP servers | The MCP servers the adapter runs inside an agent pod on abstract Unix sockets: the platform server `@lenny-platform-mcp` and one connector server `@lenny-connector-<id>` per connector. The runtime's MCP client connects to them (§15.4.3, and `CH-MCP-PLATFORM` in [§28](../spec/28_communication-channels.md)). |
| Nonce handshake | The check that a client on an intra-pod socket belongs to the session. The client sends `_lennyNonce` in its `initialize` parameters (§15.4.3, "Nonce wire format"). |
| `type: mcp` runtime | A runtime that is itself an MCP server. The adapter connects to it as an MCP client over stdio. |
| Connector client | The gateway's outbound MCP client that calls a registered connector's MCP server (§9.3). |
| Management MCP server | The MCP server of `lenny-ops` at `/mcp/management` (§25.12). |
| Attach stream | The SSE response stream that `lenny/attach_session` opens when the request sends `Accept: text/event-stream`. Lenny resumes it by sequence number (§15.2, "Event-stream resume"). |
| Combined missing-links response | The single refusal at session create that names every account link and connector link the user still needs. [`user-credential-delivery-design.md`](user-credential-delivery-design.md) and [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-4-link-check) agreed on it. |

## High-level design

Every MCP server Lenny runs speaks `2026-07-28` and `2025-11-25` and refuses older versions. The gateway's `/mcp` endpoint decides per request which era a client uses. A `2026-07-28` client sends its version and capabilities on every request, so the gateway keeps no protocol state for it. A `2025-11-25` client opens with `initialize`, and the gateway issues an `Mcp-Session-Id` that names a small record of the negotiated version and the client's capabilities. Both paths reach the same tool handlers. Each version has its own serializer, so a result is written in the format of the version that the request negotiated.

The formats that Lenny defined for itself are replaced with MCP's own. A tool error becomes an MCP tool error with Lenny's error envelope in `structuredContent`, and Lenny metadata moves under `_meta`. An elicitation from a running session reaches a `2025-11-25` client as an `elicitation/create` request, and a `2026-07-28` client as the input requests of a task. A session's progress reaches a `2026-07-28` client through the Tasks extension. When session create needs links the user has not made, a client that declared URL-mode support receives one URL-mode elicitation per missing link and retries the same call afterwards. Any other client receives the tool error that connector authorization defines.

Inside the pod, the adapter's MCP servers move to the same two versions, and the nonce moves from `initialize` into the `_meta` of each request, because `2026-07-28` has no `initialize`. The outbound clients that call servers Lenny does not control, the connector client and the adapter's client to a `type: mcp` runtime, prefer `2026-07-28` and fall back to older versions that the server answers with (O3).

## Decisions

| # | Decision | Status |
| :-- | :-- | :-- |
| D1 | Lenny serves the two most recent MCP versions, `2026-07-28` and `2025-11-25`. | **Settled**, in the owner's words "Only need to keep the last two versions", with the versions checked against modelcontextprotocol.io on 2026-11-09. |
| D2 | `2025-03-26` and `2024-11-05` are removed at once, with no deprecation window and no deprecation header, because nothing is deployed. | **Settled** by the project rule against compatibility shims before a first release. The owner can reopen it. |
| D3 | A separate design covers the upgrade. | **Settled**, in the owner's words "Write a separate design doc for the upgrade". |
| D4 | The combined missing-links response uses the tool error `details` today. This design adds the URL-mode form for clients that declare support for it ("MP-3"). | **Settled** as review question Q6, in the owner's word "ok". |
| O1 | The intra-pod MCP servers serve the same two versions as the gateway. A runtime whose MCP client offers only an older version cannot connect. | **Settled**, in the owner's word "yes". One policy covers every surface Lenny serves. The cost is in "Limits". |
| O2 | Whether the MCP layers use the official Go SDK (`github.com/modelcontextprotocol/go-sdk`, v1.7.0 or later), which serves both eras, or stay hand-written. | **Settled** as a spike, in the owner's words "try the SDK". The spike tests the gateway's needs (its auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing), and its result decides one approach for every surface. "Validation owed" lists it. |
| O3 | Which versions the outbound clients accept: the connector client and the adapter's client to a `type: mcp` runtime. Lenny does not control those servers. | **Settled**, in the owner's word "ok". The clients offer `2026-07-28`, fall back to `initialize` with `2025-11-25`, and accept any version the server answers with from `2025-03-26` on. D1 governs what Lenny serves, and third-party servers keep their own versions. |
| O4 | Whether the WebSocket transport at `/mcp/v1/ws` stays. MCP defines no WebSocket transport, and `2026-07-28` keeps no connection state that a socket would hold. | **Settled**, in the owner's words "retire websockets and use SSE in the playground". The web playground's chat pane was its only user ([§27](../spec/27_web-playground.md)), and §27 gave no reason for a WebSocket over Streamable HTTP. "MP-9" states the change. |
| O5 | Whether the gateway offers the experimental core Tasks of `2025-11-25`, or offers Tasks on `2026-07-28` only. | **Settled**, in the owner's words "2026-07-28 only". The `2025-11-25` form is experimental, and its wire format differs from the extension's. |
| O6 | The deprecation window for future adoptions. §15.2 says the oldest version enters a six-month window when a new one is adopted, which serves three versions during the window. | **Settled** as a window counted in releases, in the owner's answer "C". The release that adopts a new version keeps the oldest as deprecated, and the first release at least six months later removes it. "MP-8" states the rule. |

## Requirements

- MPR-1. Every MCP server Lenny runs serves `2026-07-28` and `2025-11-25`, and refuses every other version with the error that the requested era defines.
- MPR-2. Every result, error, notification, and server request that Lenny emits validates against the published schema of the version the request negotiated.
- MPR-3. Lenny emits elicitations and Tasks in MCP's formats, and only to clients that declared the matching capability.
- MPR-4. The specification names each supported version with the URL and the date it was checked against the MCP site.
- MPR-5. Inside the pod, the nonce check runs on every request in both eras.
- MPR-6. No authorization URL reaches a pod, a runtime, or a model through any form this design adds. This restates CAR-7 of connector authorization.

## Current state

All findings in this section are **[source]**.

| Surface | Version handling |
| :-- | :-- |
| Gateway `/mcp` (`pkg/gateway/mcpfabric/mcp`) | `CurrentProtocolVersion = "2025-03-26"` and `PreviousProtocolVersion = "2024-11-05"` in `version.go`. `negotiateVersion` answers the current version for an empty, newer, or unknown request, refuses an older one with `MCP_VERSION_UNSUPPORTED`, and sets the deprecation header for the previous version. No negotiated version is stored per connection, and every version is serialized the same way, although §15.2 "Version negotiation" item 4 requires both. `initialize` advertises `capabilities: {tools: {}}` and does not read the client's capabilities. `notifications/initialized` receives a method-not-found error. |
| Gateway WebSocket (`websocket.go`) | The same negotiation, with no deprecation header, under the subprotocol `lenny.mcp.v1`. |
| Intra-pod MCP servers (`pkg/adapter/mcp/server.go`) | `ProtocolVersion = "2025-03-26"`. `initializeResponse` ignores the requested version. `dispatch` handles `tools/list` and `tools/call`. The nonce check reads `_lennyNonce` from `initialize`. |
| Adapter client to a `type: mcp` runtime (`pkg/adapter/mcp/client.go`) | Sends `2025-03-26`. |
| Connector client (`pkg/gateway/connectors/connectorinvoke/client.go`) | Sends `2025-06-18`, the `MCP-Protocol-Version` header, and `Mcp-Session-Id`, and records whatever version the server answers. |
| Management MCP server (`pkg/ops/mcp/server.go`) | `ProtocolVersion = "2025-03-26"`, with no negotiation. |
| Client SDKs (`sdks/client/`) | Go requests `2025-03-26`. Python and TypeScript request `2025-06-18`. |
| Runtime SDKs (`sdks/runtime/`) | Go, Python, and TypeScript request `2025-03-26`. |
| `/mcp/runtimes/{name}` (`pkg/gateway/mcpfabric/mcpruntimes`) | Wired with a nil dispatcher in `cmd/lenny-gateway/httpsurface.go`, so every call answers `RUNTIME_UNAVAILABLE`. |

Formats that Lenny defined for itself:

- **Elicitation.** `projectElicitationCreate` in `pkg/gateway/mcpfabric/mcp/projection.go` emits `elicitation/create` with `message`, an optional `requestedSchema`, and `lenny/` keys under `_meta`, with no `mode` and no `url`. `projectToolUse` emits an approval as `elicitation/create` with no schema. Nothing consumes a client's reply to `elicitation/create` (BUILD-GAPS F-15.2.30). Clients answer through `lenny/respond_to_elicitation` and `lenny/dismiss_elicitation`. The "URL mode" in `pkg/gateway/mcpfabric/mcptools/elicitation.go` is Lenny's allowlist check on a URL argument. It never reaches the wire.
- **Tasks.** `projectStatusUpdate` emits `notifications/tasks/statusUpdate` with `taskId` set to the session ID and the statuses of §8.8, which include `submitted` and `canceled`. No `tasks/*` method exists (BUILD-GAPS 23.1-MED-001).
- **Tool errors and descriptors.** Error results carry a content block of type `lenny/error`, which no MCP schema admits. Tool descriptors carry a top-level `x-lenny-stability` field.
- **Tool names.** Gateway and intra-pod tools are named `lenny/<name>`.
- **Batching.** No server accepts a JSON-RPC batch. TEST-GAPS T-META.10 asks for a batch test, which no MCP version from `2025-06-18` permits.
- **Authorization.** `/mcp` accepts a bearer token and publishes no protected resource metadata (BUILD-GAPS F-10.2.39). No server checks `Origin`.

Tests pin the current versions in `pkg/gateway/mcpfabric/mcp/version_internal_test.go`, in `tests/tier3_contract/rest_mcp_consistency/published_schema_test.go`, and in `tests/tier5_e2e_kind/mcp_conformance_test.go`. The vendored schemas are `tests/testdata/mcp/schema/2024-11-05.schema.json` and `2025-03-26.schema.json`.

## MP-1 Gateway versions and negotiation

**Era selection.** The gateway reads each request to `/mcp`:

- A request whose `_meta` carries `io.modelcontextprotocol/protocolVersion` is a stateless-era request. A version other than `2026-07-28` is refused with `UnsupportedProtocolVersionError` (code `-32022`), HTTP 400, and `data.supported` listing both versions **[external]**.
- An `initialize` request selects the handshake era. The gateway answers `2025-11-25` when the client asks for it. For any other requested version it also answers `2025-11-25`, which is the handshake-era version it supports. The `2025-11-25` lifecycle rules have the client disconnect when it cannot speak the answer **[external]**.
- Any other request without stateless-era `_meta` must carry an `Mcp-Session-Id` that the gateway issued. A missing or unknown identifier receives HTTP 400 or 404, as the `2025-11-25` transport states **[external]**.

**Handshake-era sessions.** On `initialize` the gateway issues an `Mcp-Session-Id` and stores the negotiated version and the client's declared capabilities in Redis under that identifier, with an idle expiry that a Helm value sets. Any replica can serve the next request. `DELETE` ends the session. The record holds no Lenny session state; a Lenny session and an MCP session are unrelated. **[design]**

**Headers.** On both eras the gateway requires the `MCP-Protocol-Version` header and refuses a value that differs from the negotiated or `_meta` version. The `2025-11-25` transport tells a server that receives no header to assume `2025-03-26`, which Lenny does not serve, so a missing header is refused with HTTP 400. On the stateless era the gateway also validates the `Mcp-Method` and `Mcp-Name` headers that `2026-07-28` requires. A request with an `Origin` header that the gateway does not accept receives HTTP 403. **[design]**

**Stateless-era methods.** The gateway implements `server/discover`, sets `resultType` on every result, and sets `ttlMs` and `cacheScope` on list results. `ping` and `logging/setLevel` exist only on the handshake era. **[design]**

**Handshake-era notifications.** `notifications/initialized` is accepted with no reply. **[design]**

**Serializers.** Each version has a serializer for results, errors, notifications, and server requests. The tool handlers produce one internal result, and the serializer of the negotiated version writes it. **[design]**

**Errors.** `MCP_VERSION_UNSUPPORTED` stays as Lenny's error code inside `data`. `MCP_PROTOCOL_VERSION_RETIRED` and the `X-Lenny-Mcp-Version-Deprecated` header stay for future retirements under the rule in "MP-8", with no version listed as retired or deprecated now. **[design]**

**Authorization.** MCP requires that `/mcp` publish OAuth protected resource metadata, answer an invalid token with HTTP 401 and a `WWW-Authenticate` challenge, and accept only tokens issued for it as the audience **[external]**. "TS-7" owns the metadata and the challenge. This design asks TS-7 to cite the `2026-07-28` authorization page and to state the audience check ("Instructions for reviewers of other designs"). **[design]**

## MP-2 Wire conformance

**Tool errors.** A failed tool call returns `isError: true`, a `text` content block with the error message, and Lenny's error envelope (code, category, and `details`) in `structuredContent`. Each Lenny tool declares an `outputSchema`, and a successful call returns its result in `structuredContent` as well as in `content`. The `lenny/error` content type is removed. **[design]**

**Descriptors.** `x-lenny-stability` moves to `_meta` under the key `lenny/stability`. Lenny tools carry `title` and annotations. **[design]**

**Tool names.** `2025-11-25` gives guidance on the characters a tool name uses. Whether `/` is admitted is not yet checked ("Validation owed"). When it is not, Lenny's tools are renamed from `lenny/<name>` to `lenny_<name>`, a change that D2 permits before a first release. **[design]**

**Resource links and notifications.** `resource_link` content, which `outputContentBlocks` already emits on the attach stream, becomes valid on both versions without change. The `notifications/lenny/*` namespace stays, because MCP permits notifications that an implementation defines. **[design]**

**Attach stream.** On the handshake era the attach stream keeps SSE event IDs and resumption by `Last-Event-ID`. `2026-07-28` removes SSE resumability and tells servers to ignore `Last-Event-ID`, so a stateless-era client resumes by calling `lenny/attach_session` again with `resumeFromSeq`, which the tool already accepts. Whether a `2026-07-28` POST response may still be an SSE stream is a validation item. **[design]**

## MP-3 Elicitation

**Handshake era.** An elicitation from a running session reaches a `2025-11-25` client as an `elicitation/create` request on the attach stream. A form elicitation carries `mode: "form"` and a `requestedSchema`; an approval request carries a schema with a single confirmation field. A URL-mode elicitation carries `mode: "url"`, `url`, and `elicitationId`, after the per-pool allowlist check that `elicitation.go` already makes. The gateway sends an elicitation only to a client that declared the matching mode under its `elicitation` capability, and it consumes the client's reply, which closes F-15.2.30. A client that declared no elicitation capability keeps using `lenny/respond_to_elicitation` and `lenny/dismiss_elicitation`. **[design]**

**Stateless era.** `2026-07-28` has no server-initiated requests. An elicitation from a running session reaches a `2026-07-28` client as an input request of the session's task under "MP-4": `tasks/get` returns `status: "input_required"` with `inputRequests`, and the client answers with `tasks/update`. A client that did not declare the Tasks extension uses the Lenny tools. **[design]**

**Combined missing-links response at session create.** When `lenny/create_session` needs links the user has not made:

| Client | Response |
| :-- | :-- |
| `2025-11-25`, declared URL-mode elicitation | `URLElicitationRequiredError` (`-32042`) with one URL-mode entry in `data.elicitations` per missing link. The client retries `lenny/create_session` once the user has completed them. |
| `2026-07-28`, declared URL-mode elicitation | An input-required result with one URL-mode elicitation in `inputRequests` per missing link. The client retries `lenny/create_session` with `inputResponses`. No session exists until the retry passes the link check. |
| Any other client | The tool error that "CA-4" of connector authorization defines, with every missing link in `details`. This is D4. |

Each form reaches only the client that called create, which is the user's own client, so MPR-6 holds. **[design]**

**Inside the pod.** "CA-5" rejects `-32042` on the intra-pod surface for two reasons: the URL would reach a client inside a pod with no browser, and the adapter speaks `2025-03-26`. Once "MP-5" lands, the second reason no longer applies. The first still applies under CAR-7, so CA-5's custom error stands. "Instructions for reviewers of other designs" asks connector authorization to restate the reason. **[design]**

## MP-4 Tasks

On `2026-07-28` the gateway advertises the Tasks extension in `server/discover`. A client that declares the extension and calls `lenny/create_session` or `lenny/send_message` receives a task handle whose `taskId` is the session's ID. `tasks/get` reports the session's state with the extension's statuses (`working`, `input_required`, `completed`, `failed`, and `cancelled`) and, once the session ends, its result. `tasks/update` carries elicitation answers, and `tasks/cancel` cancels the session. Task notifications reach the client through `subscriptions/listen`. **[design]**

The mapping from Lenny's session states to the extension's statuses replaces the "Protocol mapping" of [§8.8](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema). `submitted` and the spelling `canceled` leave the MCP projection. A2A keeps its own mapping. `notifications/tasks/statusUpdate` is removed. **[design]**

Under O5, a `2025-11-25` client receives no Tasks and follows a session through the attach stream. **[design]**

README and the documentation say that Lenny implements "MCP Tasks". The claim stays only once this proposal lands, and it then names the extension. **[design]**

## MP-5 Intra-pod servers

The intra-pod MCP servers serve both versions under O1, with the same era selection as "MP-1" adapted to a stream socket. A socket carries no HTTP headers, so the version comes from `initialize` on the handshake era and from `_meta` on the stateless era. **[design]**

**Nonce.** On the handshake era the client sends the nonce in `initialize`, as today. On the stateless era every request carries it in `_meta` under `lenny/nonce`, and the adapter compares it in constant time on every request. The challenge exchange (`_lennyChallenge`) follows the same placement. A request that fails the check is refused with the error §15.4.3 defines. **[design]**

The servers implement `server/discover`, `ping` on the handshake era, and the no-reply handling of notifications. The adapter still never advertises `sampling`. **[design]**

## MP-6 Outbound clients

Under O3, the connector client and the adapter's client to a `type: mcp` runtime first send a stateless-era request at `2026-07-28`. When the server answers with an error that marks it as handshake-era, the client sends `initialize` with `2025-11-25` and accepts any answered version from `2025-03-26` on. The client records the negotiated version and uses that version's format for the rest of its session with the server. The connector client sends the `resource` parameter in its OAuth requests, which "CA-2" of connector authorization already marks as pending. **[design]**

## MP-7 Management server, SDKs, schemas, and tests

- **Management MCP server.** It moves to both versions with the "MP-1" negotiation. §25.12's planned `notifications/subscribe` is replaced with `subscriptions/listen` on the stateless era, which closes the gap that PROPOSAL-QUEUE C-45 tracks.
- **Client SDKs.** The Go, Python, and TypeScript client SDKs speak `2026-07-28`. They talk only to the gateway, which serves that version.
- **Runtime SDKs.** The Go, Python, and TypeScript runtime SDKs speak `2026-07-28` and send the nonce under `lenny/nonce`.
- **Reference runtimes and tools.** `cmd/runtimes/mcp-reference`, `delegation-echo`, `elicitation-echo`, `cmd/git-credential-lenny`, `cmd/lenny-compliance`, and the test stubs move to the new versions.
- **Schemas.** The `2025-11-25` and `2026-07-28` schemas replace the vendored `2024-11-05` and `2025-03-26` schemas under `tests/testdata/mcp/schema/`.

## MP-8 Version rule in the specification

The §15.2 "Target MCP spec version" sentence is replaced with a statement of the supported versions, each with the URL of its specification page and the date it was last checked against the MCP versioning page. A version claim without that URL and date is a specification defect. The "Compatibility policy" paragraph states the window that O6 settles. Lenny serves the two most recent MCP versions. The release that adopts a new version keeps the oldest previously served version as deprecated, serves it as a third version, and sends the `X-Lenny-Mcp-Version-Deprecated` header on its connections. The first release dated at least six months after the adopting release removes it. Counting the window in releases means an operator who skips releases still installs the deprecating release's warning before the removal, or reads `lenny_mcp_deprecated_version_active_sessions` before upgrading. The header is sent only for a deprecated version, so the gateway stops sending it for the previous version, as it does today. The example under "Session-lifetime exception for deprecated versions" is updated to the new versions. **[design]**

## MP-9 Web playground transport

The web playground's chat pane moves from the WebSocket at `/mcp/v1/ws` to Streamable HTTP on `/mcp`, and the WebSocket transport is removed. **[design]**

- **Requests.** The playground sends each MCP request with `fetch`, with its bearer in the `Authorization` header. `EventSource` cannot set that header, so the playground reads an SSE response as a stream from `fetch`.
- **Stream.** The chat pane opens the attach stream through `lenny/attach_session` and reads its SSE frames. Interrupt and Cancel are separate tool calls. A dropped stream resumes as "MP-2" states for the client's era.
- **Version.** The playground speaks `2026-07-28`, so it exercises the stateless era and the Tasks extension as a reference client.
- **Credentials.** The `lenny.bearer.*` entry in `Sec-WebSocket-Protocol`, and §27's rule that redacts it, are removed. Each request carries the current bearer, so a refreshed bearer takes effect on the next request and a revoked bearer fails it with `401` and `details.reason: "bearer_revoked"`. The WebSocket close code `4401` is removed.
- **Closing.** On browser close the playground sends `lenny/cancel_session` with the reason `playground_client_closed` through `fetch` with `keepalive: true`. A request that does not complete falls back to the idle timeout, as §27 states today.
- **Gateway.** `websocket.go` and the connection-takeover handling in the rate limiter, the panic recovery, the metrics, and the correlation middleware are removed. `lenny_playground_ws_connect_total` is removed.

## Limits

- **Older runtime clients.** Under O1, a runtime whose MCP client offers only `2025-06-18` or older cannot connect to the intra-pod servers. Claude Code offered `2025-11-25` in the connector spike, so it is unaffected **[source]**. Other runtimes are not surveyed ("Validation owed").
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
| §15.4.3 and §15.4.4 | The intra-pod versions, the nonce in `_meta`, and the sample runtime's requests ("MP-5"). |
| §15.5 | The compatibility policy ("MP-8"). |
| [§27](../spec/27_web-playground.md) | The chat stream on Streamable HTTP, the bearer in the `Authorization` header, and the removal of the WebSocket close codes and the `lenny.bearer.*` carrier ("MP-9"). |
| [§8.8](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema) | The MCP protocol mapping to the Tasks extension's statuses ("MP-4"). |
| §9.2 and §9.3 | URL-mode elicitation in MCP's format, and the connector client's versions and `resource` parameter ("MP-3" and "MP-6"). |
| §15.1 | The error catalog entries that change with "MP-1" and "MP-2". |
| §25.12 | The management server's versions and `subscriptions/listen` ("MP-7"). |
| `CH-MCP-PLATFORM` in [§28](../spec/28_communication-channels.md) | The intra-pod versions and the nonce placement. |
| §23 | Differentiator 6 names the Tasks extension. |

## Non-specification changes

- Code: every surface in "Current state", the per-version serializers, the Redis session record, the reply consumer for `elicitation/create`, and the playground's chat pane in `pkg/gateway/mcpfabric/playground/ui/app.js`.
- `README.md` and the documentation: `docs/api/mcp.md`, `docs/client-guide/`, `docs/tutorials/mcp-client-integration.md`, `docs/runtime-author-guide/`, `docs/operator-guide/upgrades.md`, `docs/getting-started/`, and `docs/reference/state-machines.md`.
- `BUILD-GAPS.md`: F-15.2.30, F-10.2.39 (cross-referenced to TS-7), 23.1-MED-001, and the stale §25.12 M9.
- `TEST-GAPS.md`: T-META.10 is withdrawn, because batching is gone from every supported version. T-8.8.5 changes its expected vocabulary. T-15.2.7, T-15.2.8, T-15.5.7, and T-15.7.7 are restated against the new versions.

## Tests

- Negotiation on `/mcp`: era selection, an unsupported version on each era, a missing or mismatched `MCP-Protocol-Version` header, an unknown `Mcp-Session-Id`, a session served by a second replica, `Origin` refusal, and `server/discover`.
- Schema validation of `initialize`, `tools/list`, `tools/call` results and errors, the attach stream's frames, and `elicitation/create` against both vendored schemas, on tier 3 and on the deployed gateway on tier 5.
- Elicitation: each mode on each era, the capability check, the reply consumer, and every row of the combined missing-links response, including the retry that creates the session.
- Tasks: each session state's status, `tasks/update` with an elicitation answer, `tasks/cancel`, and a client without the extension.
- Intra-pod: both eras, a nonce missing from one stateless-era request, and a request at an unsupported version.
- Outbound clients: a stateless-era server, a handshake-era server, and a server that answers `2025-03-26`.
- Playground: the chat stream over Streamable HTTP, a refreshed bearer on the next request, a revoked bearer, the close-time cancel, and a request to `/mcp/v1/ws` that receives 404.

## Validation owed

- The `2025-11-25` tool name guidance (SEP-986), and whether it admits `/`.
- Whether a `2026-07-28` POST response may be an SSE stream, which the attach stream needs.
- The part of the `2026-07-28` `schema.ts` that was not read: `CacheableResult`, `ServerCapabilities`, and the Tasks types.
- The Tasks extension's stability and its repository's current revision.
- The spike under O2: whether the Go SDK v1.7.0 or later can host the gateway's auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing. Check the release page for the current tag.
- A survey of the MCP versions that common runtime clients offer, which sizes the cost of O1.

## Instructions for reviewers of other designs

- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): in "Why the raw 401 stops at the gateway" under "CA-5", rest the rejection of `-32042` on CAR-7, since the adapter will speak `2025-11-25` and `2026-07-28`. In "CA-4", name this design's per-version forms of the combined missing-links response for MCP clients.
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md): in step 1 of "Flow", replace "MCP clients receive the same URL as a URL-mode elicitation" with a reference to "MP-3".
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): in "Discovery" and "Login routing (TS-7)", cite the `2026-07-28` authorization page in place of the `2025-06-18` one, and state the audience check that MCP requires of `/mcp`.

## Proposal split and order

1. **Versions and conformance**: "MP-8", "MP-1", and "MP-2", with the vendored schemas. Every later proposal depends on the per-version serializers.
2. **Elicitation, Tasks, and playground**: "MP-3", "MP-4", and "MP-9". The playground moves once the Tasks extension exists. "MP-3"'s combined missing-links response lands with or after the link checks of connector authorization and user credential delivery.
3. **Pod side**: "MP-5" and the runtime SDKs.
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

- Revision 2, 2026-11-09: the owner settled O1, O2, O3, O5, and O6, and settled O4 as the retirement of the WebSocket transport. Revision 1 said the specification gave no reason for the WebSocket. §27 uses it for the playground's chat pane, which "MP-9" moves to Streamable HTTP.
- Revision 1, 2026-11-09: first draft. Written after the owner settled Q6 of the design sync review and asked for a separate design that keeps the last two MCP versions.
