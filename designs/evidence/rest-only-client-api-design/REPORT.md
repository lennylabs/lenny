# REST-only Lenny with external protocol adapters

Spike, 2026-11-13. This report examines what Lenny's REST API needs so that MCP and A2A can be served by separate adapter projects. Each adapter sits in front of REST as an ordinary client, forwards the caller's credentials, holds no privilege, and keeps no durable state. The report reads the specification under `spec/` with every design under `designs/` treated as implemented. Several designs were being edited while this spike ran: session lifecycle, message delivery, session isolation, pod layout, runtime protocol, agent-runtimes-only, checkpoint, materialization, placement, tenant security settings, tenant scoping, billing, forensics, and security oversight. Route and state names taken from them may change. The MCP that Lenny speaks toward runtimes is out of scope: platform tools, connector servers, and the reference MCP bridge.

The proof of concept is in `poc/` beside this file. The "Proof of concept" section at the end states what it showed.

## Findings

1. The current MCP version is `2026-07-28`. It removes protocol sessions, the `initialize` handshake, server-initiated requests, and SSE resumption. A `2026-07-28` server keeps no connection state, so an MCP adapter in front of REST needs no state of its own for that version. The `2025-11-25` handshake era, which Lenny's MCP upgrade design also serves, needs an `Mcp-Session-Id`. An adapter can mint a self-contained signed identifier and still need no storage.
2. The current A2A release is v1.0.1, with protocol version `1.0`. A2A 1.0 makes the following changes:
   - The agent card moves to `/.well-known/agent-card.json`.
   - The operations are renamed: `SendMessage`, `SendStreamingMessage`, `GetTask`, `ListTasks`, `CancelTask`, `SubscribeToTask`, the push notification config operations, and `GetExtendedAgentCard`.
   - No event replay is defined on reconnect.

   §21.1 of the specification still describes the pre-1.0 paths and field names.
3. Every MCP client-facing feature Lenny uses, and every A2A feature, has a REST mapping once REST gains these additions:
   - a sequence number on the message receipt and on the session resource;
   - a message resource and a turn resource;
   - a list of pending elicitations and approvals;
   - `Idempotency-Key` on message send.

   The proof of concept implements MCP Tasks and A2A streaming through two adapter replicas using only these additions.
4. A2A push notifications need the one large addition. Each session gets webhook subscriptions with create, read, list, and delete, covering every event kind, with opaque subscriber data that Lenny echoes on each delivery. The adapter relays each delivery to the A2A client with the client's own authentication. The relay holds a key to decrypt the subscriber data and no other state.
5. Authorization decides the deployment. MCP requires a server to accept only tokens issued for it as the audience, and it forbids a server to "accept or transit any other tokens". An adapter that forwards the caller's bearer to Lenny is compliant only when the adapter and the REST API are one protected resource: the same canonical resource URI, served on the same origin through ingress path routing. A separately hosted adapter needs a token exchange, which is a privilege.
6. Two designs conflict with the MCP Tasks extension:
   - The MCP upgrade design ("MP-4") sets an MCP task ID to the session ID for both `create_session` and `send_message`.
   - The session lifecycle design ("SL-11") maps `idle` to MCP Tasks `input_required`.

   Under the Tasks extension, `input_required` carries `inputRequests` that the client answers with `tasks/update`, and an idle session has none. A task per sent message, completed at the end of its turn, matches the extension. This holds whether MCP stays in Lenny or moves out.
7. The recommendation is to move MCP out as well (option b). The project ships it as a first-party adapter that the Helm chart deploys by default on the gateway's origin. The "Recommendation" section states the reasons and conditions.

## Versions verified

All pages were read on 2026-11-13.

| Specification | Version | Source |
| :-- | :-- | :-- |
| MCP | `2026-07-28` is the latest revision. The previous one is `2025-11-25`. | https://modelcontextprotocol.io/specification/latest (cites `schema/2026-07-28/schema.ts`) and https://modelcontextprotocol.io/specification/2026-07-28/changelog |
| MCP Streamable HTTP | `2026-07-28` transport | https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http |
| MCP `2025-11-25` transport | Handshake era, `Mcp-Session-Id`, and resumption with `Last-Event-ID` | https://modelcontextprotocol.io/specification/2025-11-25/basic/transports |
| MCP multi round-trip requests | `2026-07-28` | https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr |
| MCP authorization | `2026-07-28` | https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization |
| MCP Tasks | Official extension `io.modelcontextprotocol/tasks`, specified in the `ext-tasks` repository | https://modelcontextprotocol.io/extensions/tasks/overview and https://github.com/modelcontextprotocol/ext-tasks |
| A2A | Release v1.0.1 on 2026-05-28 (changelog dated 2026-05-26), a fix release on v1.0.0 of 2026-03-12. The specification page lists 1.0.0 as the latest specification version and negotiates by `Major.Minor`, so the protocol version is `1.0`. | https://github.com/a2aproject/A2A/releases and https://a2a-protocol.org/latest/specification/ |
| A2A task lifecycle | A terminal task cannot restart. A follow-up creates a new task in the same `contextId`. | https://a2a-protocol.org/latest/topics/life-of-a-task/ |

The `2026-07-28` changes that matter here:

- **Sessions.** There are no protocol sessions and no `Mcp-Session-Id`. Every request carries its version and client capabilities in `_meta`, and `server/discover` replaces `initialize`.
- **Server-to-client requests.** Elicitation, sampling, and roots become multi round-trip requests. The server returns `resultType: "input_required"` with `inputRequests` and an optional opaque `requestState`. The client retries the original request with `inputResponses`.
- **Streams and cancellation.** A POST response may be an SSE stream that carries notifications related to that request and then the final response. Closing that stream cancels the request. There is no GET stream, and `Last-Event-ID` resumption is removed. Long-lived change notifications use `subscriptions/listen`.
- **Tasks.** Tasks move to the `io.modelcontextprotocol/tasks` extension:
  - `CreateTaskResult` with `resultType: "task"`;
  - polling with `tasks/get`;
  - input through `tasks/update`;
  - `tasks/cancel`;
  - no `tasks/list`;
  - optional `notifications/tasks` through `subscriptions/listen`.
- **Headers.** Requests carry the `MCP-Protocol-Version`, `Mcp-Method`, and `Mcp-Name` headers. A header that does not match the body produces `HeaderMismatch` (`-32020`).
- **Deprecations.** Roots, sampling, and logging are deprecated.

Specification text that is stale against these sources:

- §15.2 MCP API, "Target MCP spec version", names `2025-03-26`. The MCP upgrade design already replaces it ("MP-8").
- §21.1 A2A Full Support and the §15.1 "Discovery and introspection" table use these names:
  - `/.well-known/agent.json`;
  - `POST /a2a/{runtime}/tasks`;
  - the `pushNotification.url` field;
  - the long-poll path `/a2a/{runtime}/tasks/{id}/stream`.

  A2A 1.0 uses `/.well-known/agent-card.json`, the operations listed in finding 2, and `TaskPushNotificationConfig`. §21.1 also cites "A2A spec §3" for discovery, which A2A 1.0 numbers §8.2.
- §21.1 "A2A outbound push" signs webhooks with `X-Lenny-Signature`. In A2A 1.0 the client supplies `AuthenticationInfo` (`scheme` and `credentials`), and the agent sends `Authorization: <scheme> <credentials>`.

## Adapter model

The adapter evaluated here has these properties:

- **Unprivileged.** It holds no Lenny credential. Every REST call carries the bearer the client sent. Lenny therefore authenticates, authorizes, scopes discovery, and applies quotas exactly as it does for a direct REST caller.
- **No durable state.** It has no database and no cache whose loss changes behaviour. Every protocol identifier either encodes the Lenny identifiers it maps to or resolves through a REST lookup. A long-lived HTTP connection is the only thing it holds between events.
- **Configuration.** It may hold a symmetric key in a Kubernetes Secret. The key is used for the `2025-11-25` session identifier and for the push relay, and every replica shares it. Losing the key invalidates outstanding identifiers and changes no Lenny state.
- **Any replica serves any request.** No session affinity is needed.

The model relies on REST features that already exist:

- the session routes in the "Session lifecycle" table of §15.1 REST API;
- the SSE event stream `GET /v1/sessions/{id}/events`, with `Last-Event-ID` resumption and `gap_detected` (§15.1 "Artifacts and introspection");
- the elicitation respond and dismiss routes, and the tool-use approve and deny routes;
- the error envelope of §15.2.1 REST/MCP Consistency Contract, item 3;
- the rate-limit headers of §15.1 "Rate-limit headers";
- `Idempotency-Key` from §11.5 Idempotency;
- the existence-hiding `404` of §10.2 Authentication, **Session ownership.** rule 7;
- the OpenAPI document at `GET /openapi.json`, from which §15.2.1 rule 4 already generates the MCP tool schemas.

The REST additions this report proposes are labelled R1 to R14. The "REST additions" section lists them.

## MCP client-facing features

"Lenny today" describes the specification with the designs applied. The main designs cited are the MCP upgrade design ("MP-n"), the message delivery design ("MD-n"), the session lifecycle design ("SL-n"), and the connector authorization design ("CA-n").

| Feature | Lenny today | REST equivalent | Gap |
| :-- | :-- | :-- | :-- |
| Streamable HTTP, `2026-07-28` | The gateway's `/mcp` serves stateless-era requests. It validates `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`, and `Origin`, and implements `server/discover` ("MP-1"). | None needed. | None. The adapter implements the transport, header validation, `Origin` checks, `resultType`, and `ttlMs` and `cacheScope` on list results. |
| Handshake era, `2025-11-25`: `initialize`, `Mcp-Session-Id`, and `DELETE` | The gateway stores the negotiated version and client capabilities in Redis under the `Mcp-Session-Id` it issues ("MP-1"). | None needed. | The adapter mints the identifier as a signed, self-contained token. The token holds the version, the client capabilities, a digest of the bearer's subject, and an expiry. The `2025-11-25` transport names a JWT as an acceptable session ID. A `DELETE` cannot revoke such a token before it expires, so the adapter answers `405`, which the transport permits. |
| Resumable SSE with `Last-Event-ID` (`2025-11-25` only) | The attach stream carries `SeqNum` as the SSE `id`, accepts `resumeFromSeq`, and emits `gap_detected` (§15.2 "Event-stream resume"). `2026-07-28` removes resumption, so a client resumes by calling `attach_session` again with `resumeFromSeq` ("MP-2"). | `GET /v1/sessions/{id}/events` with `Last-Event-ID`, the replay buffer of §10.4 Gateway Reliability ("Event replay buffer"), and the coordinator-handoff synthesis. | None. The adapter writes each event ID as `<sessionId>:<seq>`. A `GET` with `Last-Event-ID` therefore names its stream, as the `2025-11-25` transport asks. |
| `tools/list` | Lenny generates tools from OpenAPI for overlapping operations (§15.2.1 rule 4). | `GET /openapi.json`. | R7: the session routes carry the MCP tool name, title, annotations, and `outputSchema` as OpenAPI extensions, as the admin routes already carry `x-lenny-mcp-tool` (§15.1 "Admin API MCP extension contract"). Without R7, the adapter maintains the catalogue by hand. |
| `tools/call` for each client tool listed under the table | §15.2 "MCP tools (client-facing)". | Every tool has a §15.1 route, listed under the table. | None for the mapping. Uploads arrive in MCP as base64 in JSON and become multipart REST uploads. The adapter enforces the same size limits that REST states. |
| `attach_session` | An SSE response to the tool call that carries the projections in §15.2 "Per-kind MCP wire projection", activity, and progress ("MD-21"). | `GET /v1/sessions/{id}/events`. | R1: the receipt of `POST /v1/sessions/{id}/messages` and the body of `GET /v1/sessions/{id}` carry the session's current sequence number. The adapter then attaches at a known point, with no race between sending and reading. |
| `delegate_task` | Runtime-facing; renamed `start_child_session` ("MD-22"). It is absent from the §15.2 client tool table. | None needed. | Out of scope. |
| Tasks extension (`2026-07-28`) | `tasks/get`, `tasks/update`, and `tasks/cancel`, with the task ID equal to the session ID ("MP-4"). `idle` and `dormant` map to `input_required` ("SL-11"). | Session state through `GET /v1/sessions/{id}`. No message or turn resource exists. | R2: `GET /v1/sessions/{id}/messages/{messageId}`, returning the delivery state and the turn that consumed the message. R3: `GET /v1/sessions/{id}/turns/{n}`, returning status, end-of-turn message, usage, and error. With R2 and R3, a `send_message` task ID is `<sessionId>.<messageId>`, and the task completes at the end of its turn. Finding 6 explains why a session-ID task conflicts with the extension. |
| Elicitation, form mode | For `2025-11-25`: `elicitation/create` on the attach stream, with the reply consumed ("MP-3"). For `2026-07-28`: the task's `inputRequests`, answered by `tasks/update`. | Elicitation events on the SSE stream. `POST .../elicitations/{elicitationId}/respond` and `/dismiss`. Tool approvals through `.../tool-use/{toolCallId}/approve` and `/deny`. | R4: `GET /v1/sessions/{id}/elicitations?state=pending` and a list of pending approvals, including harness approvals ("MD-19"). Each entry carries schema, mode, URL, provenance, and turn. `tasks/get` must report pending input without replaying the event stream. For `2025-11-25`, the adapter sets the JSON-RPC ID of `elicitation/create` to `elic:<sessionId>:<elicitationId>`. A reply that reaches any replica then routes to the right REST call. |
| Elicitation in URL mode, and the combined missing-links response | For `2025-11-25`: `URLElicitationRequiredError` (`-32042`). For `2026-07-28`: an input-required result with URL-mode elicitations. Otherwise: the tool error ("MP-3"). Mid-session connector prompts are URL-mode elicitations ("CA-5", "Connector prompts"). | `403 LINKS_REQUIRED` with `details.connectors[].linkUrl` and `details.userLinks` ("CA-4"). Connector prompts are elicitations. | None at creation. The adapter converts the `403` into either form. The retry calls REST again, which reruns the link check, so no `requestState` is needed. Mid-session prompts need R4. |
| Sampling and roots | Not used on the client surface. Deprecated in `2026-07-28`. | None needed. | None. |
| Progress | `notifications/progress` from the newest top-level activity, when the attach request carries a `progressToken` ("MD-21"). | `activity` events on the SSE stream. | None. |
| Logging | Not used ("MD-21"). Deprecated in `2026-07-28`. | None needed. | None. |
| Cancellation | For `2026-07-28`, closing the response stream cancels that request, and `tasks/cancel` cancels the session ("MP-4"). For `2025-11-25`, `notifications/cancelled`. | `DELETE /v1/sessions/{id}`, `POST .../suspend`, and message retraction. | None. With per-message tasks, `tasks/cancel` retracts a pending message. Otherwise it suspends or cancels by the adapter's documented rule. |
| `resource_link` and `resources/read` | The attach stream emits `resource_link` content that names the gateway's download URL ("MP-2", "MD-21"). | `GET /v1/sessions/{id}/artifacts/{path}` and `GET /v1/blobs/{ref}`. | None if the adapter serves `resources/read` by proxying the download. R13 (optional): short-lived signed download URLs, for clients that fetch the URI directly without the bearer. |
| List-changed notifications and `subscriptions/listen` | Tool lists change only with a release. `notifications/tasks` reaches a client through `subscriptions/listen` ("MP-4"). | Only per-session SSE streams. | R10 (optional): a per-principal stream of state changes across the caller's sessions. Polling with `tasks/get` is the extension's default, so R10 is not required. |
| Authorization | OAuth protected resource metadata and the `WWW-Authenticate` challenge on `/mcp` ("TS-7"). MCP requires an audience check ("MP-1"). | The gateway validates bearers per identity source, each with an `audiences` list (tenant scoping design, "Identity source record (TS-6)"). | R9. See "Authentication passthrough". |
| Error formats | Tool errors carry Lenny's envelope in `structuredContent` ("MP-2"). | The §15.2.1 item 3 envelope, with `code`, `category`, and `retryable`. | None. |
| Version policy | The two most recent versions, a deprecation header, `MCP_VERSION_UNSUPPORTED`, and `MCP_PROTOCOL_VERSION_RETIRED` ("MP-8"). | None needed. | None. The policy moves with the adapter. |
| Lenny's own MCP clients | The `lenny session` commands use the Go client SDK over MCP (§24, "Session commands"). The client SDKs speak MCP (§15.6 Client SDKs). The playground speaks MCP over Streamable HTTP ("MP-9"). | REST and the SSE event stream. | Under option b these clients move to REST. They need the same additions as the adapter. |

Each client tool in the `tools/call` row maps to a §15.1 route:

| MCP tool | REST route |
| :-- | :-- |
| `create_session` | `POST /v1/sessions` |
| `create_and_start_session` | `POST /v1/sessions/start` |
| `upload_files` | `POST /v1/sessions/{id}/upload` |
| `finalize_workspace` | `POST /v1/sessions/{id}/finalize` |
| `start_session` | `POST /v1/sessions/{id}/start` |
| `send_message` | `POST /v1/sessions/{id}/messages` |
| `suspend_session` (formerly `interrupt_session`, "SL-4") | `POST /v1/sessions/{id}/suspend` |
| `get_session_status` | `GET /v1/sessions/{id}` |
| `get_task_tree` | `GET /v1/sessions/{id}/tree` |
| `get_session_logs` | `GET /v1/sessions/{id}/logs` |
| `get_token_usage` | `GET /v1/sessions/{id}/usage` |
| `list_artifacts` | `GET /v1/sessions/{id}/artifacts` |
| `download_artifact` | `GET /v1/sessions/{id}/artifacts/{path}` |
| `close_session` (formerly `terminate_session`, "SL-7") | `POST /v1/sessions/{id}/close` |
| `cancel_session` | `DELETE /v1/sessions/{id}` |
| `resume_session` | `POST /v1/sessions/{id}/resume` |
| `list_sessions` | `GET /v1/sessions` |
| `list_runtimes` | `GET /v1/runtimes` |
| `respond_to_elicitation` and `dismiss_elicitation` | The elicitation respond and dismiss routes |
| Message retraction ("MD-7") | `DELETE /v1/sessions/{id}/messages/{messageId}` |

## A2A features

Lenny has no A2A implementation yet. The existing plans and mappings are these:

- §21.1 plans an `A2AAdapter` with per-runtime endpoints. Its cards are generated from `agentInterface` (§5.1, "`agentInterface` Field"). It uses `elicitationDepthPolicy: block_all` and pushes webhooks through `OutboundChannel`.
- §8.8 TaskRecord and TaskResult Schema maps states in "Protocol mapping" and its supplementary table. "SL-11" changes those mappings.
- "MD-21" states the A2A projection of activity, output, and files.

The mapping assumed below gives each A2A message from the client its own task. The task ID is `<sessionId>.<messageId>`, and the context ID is the session ID. A2A says a terminal task cannot restart and a follow-up creates a new task in the same context, which matches one turn per message. The alternative is one task per session, as "SL-11" implies. That leaves every answered turn in `input-required`, and the task never reaches `completed` until the session closes.

| Feature | Lenny today | REST equivalent | Gap |
| :-- | :-- | :-- | :-- |
| Agent card discovery at `/.well-known/agent-card.json` | Planned: an aggregated array at `/.well-known/agent.json` and per-runtime cards at `/a2a/runtimes/{name}/.well-known/agent.json` (§21.1). Cards are generated at write time into `publishedMetadata`. | `GET /v1/runtimes` (identity-filtered) and `GET /v1/runtimes/{name}/meta/{key}` (public entries need no authentication). | The card's `supportedInterfaces[].url` and `securitySchemes` must name the adapter and the identity provider. A card that Lenny generates at write time cannot know them, so the adapter composes the card from `agentInterface`, which `AuthorizedRuntime` already carries. R8: an unauthenticated index of runtimes that have public cards, because `GET /v1/runtimes` filters by identity. The adapter serves one card per runtime at a per-runtime base URL. |
| Authenticated extended card (`GetExtendedAgentCard`) | Not planned. | `GET /v1/runtimes` and the tenant-visible `publishedMetadata` entries, called with the caller's bearer. | R8 also adds `GET /v1/runtimes/{name}` for a single runtime. |
| `SendMessage` (blocking by default; `returnImmediately` returns at once) | Planned as `POST /a2a/{runtime}/tasks`. | `POST /v1/sessions` or `POST /v1/sessions/start`, then `POST /v1/sessions/{id}/messages`, with the SSE stream for the outcome. | R1, R2, and R3. The adapter holds the request open on the REST stream until the turn ends or input is needed. |
| `SendStreamingMessage` | Planned as SSE through `OutboundChannel`. | The same routes, with the SSE stream translated. | R1, so the stream starts without a race. The proof of concept implements this operation. |
| `GetTask` with `historyLength` | Planned. | `GET /v1/sessions/{id}` and `GET /v1/sessions/{id}/messages`. | R2 and R3. |
| `ListTasks` with `contextId`, status, and paging | Not planned. | `GET /v1/sessions` with `labels` and status filters. `GET /v1/sessions/{id}/messages?from=client` (session derive design). | Within one context, nothing beyond R2 and R3. Across contexts, the adapter lists the sessions labelled for A2A and then their messages, at one REST call per session. R10, or a cross-session message listing, removes that cost. |
| `CancelTask` | Planned through `DELETE`. | `DELETE /v1/sessions/{id}`, `POST .../suspend`, and message retraction. | None. `409 INVALID_STATE_TRANSITION` maps to `TaskNotCancelableError`. |
| `SubscribeToTask` | Planned as a long-poll stream. | `GET /v1/sessions/{id}` and the SSE stream. | R1. A2A requires the current `Task` as the first event, followed by live updates. The adapter reads the session with its sequence number and streams from that number. A2A defines no replay, and subscribing to a terminal task returns `UnsupportedOperationError`. |
| Push notification configs (`Create`, `Get`, `List`, `Delete`) and webhook delivery with `AuthenticationInfo` | Planned: one callback URL per task, HMAC in `X-Lenny-Signature`, three retries, and SSRF validation (§21.1). | `callbackUrl` and `callbackSecret` on `CreateSessionRequest` (§14 `callbackUrl`). These deliver terminal events only, as CloudEvents signed with HMAC. Undelivered events are listed at `GET /v1/sessions/{id}/webhook-events`. | R6 (large). See "Push delivery". |
| Task states | §8.8 maps `submitted`, `working`, `completed`, `failed`, `canceled`, and `input-required`. "SL-11" maps `idle` and `dormant` to `input-required`, and the paused states to `working` with `metadata.paused`. | Session states, plus turn status with R3. | With per-message tasks, the states map as listed under the table. |
| `auth-required` | Not planned. Mid-session connector prompts exist ("CA-5"). | `403 LINKS_REQUIRED` at creation. Connector prompts appear as pending URL-mode elicitations. | Mid-session, R4 covers it. At creation no session exists, so the adapter has no task to put in `auth-required`. It answers with a `Message` carrying the link URLs, which A2A permits, and the client retries. R11 (optional): a creation mode that commits the session and holds it until the links exist, so that the task can enter `auth-required`. |
| Multi-turn: a message with `taskId` that answers `input-required` | Planned as `POST /v1/sessions/{id}/messages` with `inReplyTo` (§21.1). | The elicitation respond route, and messages. | R4. The adapter checks whether the task has a pending elicitation. If it does, the message's data part answers it. Otherwise the message is a new message to the session. |
| Elicitation | §21.1 suppresses agent elicitations with `block_all`. | None for the policy. | R12 (small; needed only if the adapter keeps §21.1's rule): a creation field that sets the elicitation policy. The proof of concept instead surfaces an elicitation as `input-required`, with the schema in a data part. Once an A2A extension names that convention, suppression is no longer needed. |
| Artifacts and streaming chunks | `TaskArtifactUpdateEvent` with `append` and `lastChunk`. Files become file parts with the gateway's download URL ("MD-21"). | `output` events with part operations ("MD-16"). File parts carry a `ref`, downloaded with `GET /v1/blobs/{ref}` or the artifacts route. | R13 (optional): signed download URLs. An A2A client that fetches a file part's URI may not attach the bearer. |
| `contextId` and `referenceTaskIds` | §21.1 has no context concept. | The session ID, plus session labels for context IDs that a client chooses. | None. A context ID that the client proposes is stored as a session label and found with the `labels` filter of `GET /v1/sessions`. |
| Security schemes | The card advertises `adapterCapabilities` (§21.1). | Lenny accepts OIDC and OAuth 2.1 bearer tokens, client-credentials tokens, and Lenny-issued tokens from `/v1/oauth/token` (§10.2 table; §13.3 Credential Flow). Lenny issues no API keys. The playground's `apiKey` mode takes a pasted gateway bearer. | None for REST. The card declares `OAuth2SecurityScheme` or `OpenIdConnectSecurityScheme`, plus `HTTPAuthSecurityScheme` with `bearer`. With identity providers per tenant, the public card names the deployment's login routing, and the extended card names the tenant's issuer. |
| `A2A-Version`, `A2A-Extensions`, and `VersionNotSupportedError` | Not planned. | None needed. | None. |

With per-message tasks, the A2A task states map as follows:

- `working` while the turn runs.
- `completed` or `failed` at the end of the turn.
- `input-required` for a pending elicitation (R4).
- `canceled` for a cancelled session or a retracted message.
- `rejected` for a policy refusal at creation (`403` or `422`).

## Cross-cutting needs

### Identifiers without adapter state

| Protocol identifier | Encoding | Lookup |
| :-- | :-- | :-- |
| MCP `2025-11-25` `Mcp-Session-Id` | Signed token holding the version, client capabilities, subject digest, and expiry. | None. The adapter verifies it with its key. |
| MCP JSON-RPC ID of a server-sent `elicitation/create` | `elic:<sessionId>:<elicitationId>` | `POST .../elicitations/{elicitationId}/respond` |
| MCP SSE event ID (`2025-11-25`) | `<sessionId>:<seq>` | `GET /v1/sessions/{id}/events` with `Last-Event-ID: <seq>` |
| MCP task ID for `create_session` | `<sessionId>` | `GET /v1/sessions/{id}` |
| MCP task ID for `send_message`, and A2A task ID | `<sessionId>.<messageId>` | R2, then R3, then R4 |
| MCP `inputRequests` key, and A2A input reply | Lenny `elicitationId` or `toolCallId` | R4, then the respond, approve, or deny route |
| A2A `contextId` | The session ID, or a client value stored as a session label | `GET /v1/sessions/{id}`, or `GET /v1/sessions` filtered by `labels` |
| A2A push config ID | Lenny webhook subscription ID | R6 |

None of these identifiers needs signing for authorization. Each lookup carries the caller's bearer. Lenny answers `404 RESOURCE_NOT_FOUND` for a session the caller may not read (§10.2 **Session ownership.** rule 7), so a guessed or borrowed task ID reveals nothing. Step 2 of the proof of concept shows this rule at work. A task ID exposes a session ID only to the client that already owns that session.

### Event stream replay and gaps

The REST stream already does three things:

- it resumes from `Last-Event-ID`;
- it emits `gap_detected` when the replay buffer has evicted the requested range;
- it re-synthesizes reconstructible events after a coordinator handoff (§10.4).

The adapter passes these through. Three points remain:

- An adapter that sends a message and then streams its outcome needs the sequence number at the moment of the send (R1). Without it, the adapter must either open the stream before sending, which holds a connection for every send, or replay the whole buffer and filter it.
- `2026-07-28` and A2A define no resumption. An MCP client resumes by calling `attach_session` again with `resumeFromSeq`, which the adapter turns into `Last-Event-ID`. An A2A client re-subscribes and receives a fresh snapshot.
- Each downstream stream holds one upstream REST stream. The gateway's connection budget for SSE readers covers the adapter's streams as it covers a direct client's. The adapter adds no fan-out of its own.

### Push delivery

Two designs serve A2A push. The first keeps Lenny independent of any protocol, and it is the one recommended.

1. **Relay (recommended).** R6 adds `POST`, `GET`, and `DELETE` on `/v1/sessions/{id}/webhooks`, and `GET /v1/sessions/{id}/webhooks/{webhookId}`. A subscription names:
   - a target URL;
   - the event kinds to deliver;
   - an optional coalescing interval ("MD-21" coalesces activity to one per second);
   - a signing secret;
   - an opaque `subscriberData` string of bounded size, which Lenny stores and echoes in every delivery.

   The A2A adapter registers its own relay endpoint as the target. It puts the A2A client's push config into `subscriberData`, encrypted with its key: the URL, the `AuthenticationInfo`, the token, and the config ID. On each delivery the adapter does the following:
   - verifies Lenny's signature;
   - decrypts `subscriberData`;
   - translates the `SessionEvent` into an A2A `StreamResponse`;
   - posts it to the client with the client's `Authorization` header;
   - returns the client's status to Lenny within the delivery timeout.

   Lenny's retries and its undelivered-event log then cover the whole path. The adapter validates the client URL against SSRF, as A2A 1.0 §13.2 asks. Lenny's own callback URL validation rejects private addresses (§14 `callbackUrl`), so R6 also needs an operator-configured allowance for the in-cluster relay.
2. **Direct delivery.** Lenny posts to the A2A client itself, with a client-supplied `Authorization` header and an A2A body. This puts A2A's body format inside Lenny and stores third-party credentials in Lenny, so this design is rejected.

The relay adds one hop per delivery, and the gateway must be able to reach the relay.

### Discovery with policy scoping

`GET /v1/runtimes` is identity-filtered and policy-scoped. `PublishedMetadataRef` already tells an adapter to re-check visibility before it serves a cached entry to another caller (§15 "Shared Adapter Types"). The adapter forwards the bearer on every discovery call and caches only public entries.

- MCP `list_runtimes` maps to `GET /v1/runtimes` directly.
- A2A public cards need R8.
- A2A extended cards use the caller's bearer.

### Authentication passthrough

Lenny accepts bearer tokens only (§10.2 table):

- OIDC and OAuth 2.1 tokens from configured identity sources;
- client-credentials tokens;
- Lenny-issued tokens.

Each identity source lists the audiences it accepts. On a shared issuer, the audience can select the tenant (tenant scoping design, "Issuer pin and proof").

MCP's authorization page states three requirements. A server must validate that a token "was issued specifically for them as the intended audience". It "MUST only accept tokens that are valid for use with their own resources". It "MUST NOT accept or transit any other tokens". A bearer issued for an adapter at one resource URI and forwarded to Lenny at another is token passthrough. Two deployments address the rule:

- **One protected resource (recommended).** The adapter and the REST API share one canonical resource URI, such as `https://lenny.acme.com`, and one origin. Ingress routes `/mcp` and the A2A paths to the adapter, and `/v1` to the gateway. The protected resource metadata names the shared URI, the identity sources accept it as an audience, and Lenny validates every forwarded token. R9 states what REST must provide:
  - the adapter's paths accepted under the shared resource URI;
  - the protected resource metadata that the gateway already owes under "TS-7", served for those paths;
  - a `WWW-Authenticate` challenge whose `resource_metadata` names the shared document.

  The adapter passes `401` challenges and `403 insufficient_scope` challenges through to the client.
- **Separate resource.** The adapter exchanges the client's token at `/v1/oauth/token` for a token with audience `lenny-gateway` (§13.3, "Token issuance and rotation"). Today the exchange accepts Lenny-issued subject tokens and narrows them. Accepting an IdP token issued for the adapter, and trusting the adapter as the client that presents it, gives the adapter a privilege. This contradicts the unprivileged model.

A2A states no audience rule, and the same deployment serves it.

Lenny's audit records and metrics label requests by adapter today (`AdapterCapabilities.Protocol`). Under REST-only, every request is a REST request. R14 adds an informational header such as `Lenny-Client-Protocol: mcp/2026-07-28`. Lenny records it in audit and metrics and never uses it for a decision.

### Error mapping

The REST envelope carries `code`, `category`, `retryable`, and the HTTP status. That is enough to map errors to both protocols without a REST change.

| REST | MCP | A2A |
| :-- | :-- | :-- |
| `404 RESOURCE_NOT_FOUND` on a task lookup | `-32602`, "task not found" | `TaskNotFoundError` (`404`) |
| `409 INVALID_STATE_TRANSITION` on cancel | `tasks/cancel` acknowledged, with the state unchanged | `TaskNotCancelableError` |
| `409` on a message to a terminal session | Tool error with the envelope in `structuredContent` | `UnsupportedOperationError` |
| `400 VALIDATION_ERROR` on unsupported content | Tool error | `ContentTypeNotSupportedError` |
| `401` and `403` | HTTP `401` or `403` with the challenge | HTTP `401` or `403` |
| `403 LINKS_REQUIRED` | The URL-mode elicitation forms of "MP-3" | A `Message` with the link URLs, or `auth-required` with R11 |
| Other errors | Tool error with the envelope | `application/problem+json`, with the envelope in an extension member |

The §15.2.1 contract tests and the `RegisterAdapterUnderTest` matrix become the adapter project's conformance suite, run against a deployed REST API.

### Rate limits and retries

REST returns `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, and `Retry-After` (§15.1 "Rate-limit headers"). For both protocols, the adapter copies `Retry-After` and the `429` or `503` status onto its HTTP response. For MCP it also puts `retryAfter` in the error data. Lenny's limits key on tenant, user, and session, so many users behind one adapter keep separate limits. An ingress limit keyed on client IP must read `X-Forwarded-For` or exempt the adapter.

### Idempotency

§11.5 covers session creation, finalize, start, child spawn, delegation approval, and resume, with keys of the form `(tenant_id, idempotency_key)`. Two gaps matter to adapters:

- **Message send has no idempotency key.** Under `2026-07-28`, a client re-issues a request after a broken stream with a new JSON-RPC ID. A2A says agents "may utilize the `messageId` to detect duplicate messages". R5 adds `Idempotency-Key` to `POST /v1/sessions/{id}/messages`. The adapter derives the key from the A2A `messageId`, or from the MCP `idempotencyKey` tool argument that §11.5 already defines.
- **Keys are scoped by tenant only.** Two users in one tenant who send the same A2A `messageId` collide. R5 scopes keys by tenant and subject.

### Multiple adapter replicas

Under this model, `2026-07-28` and A2A requests need no affinity. A replica that dies drops only its open streams, and clients reopen them. `2025-11-25` sessions need the shared signing key and no affinity. The proof of concept alternates every step between two replicas.

## REST additions

| ID | Addition | Protocol features that need it | Size |
| :-- | :-- | :-- | :-- |
| R1 | The current sequence number on the message receipt, and as `lastSeq` on `GET /v1/sessions/{id}`. The counter already exists as `sessions.last_seq` (§10.4). | MCP `attach_session` after a send. A2A `SendMessage`, `SendStreamingMessage`, and `SubscribeToTask`. | Small |
| R2 | `GET /v1/sessions/{id}/messages/{messageId}`, returning delivery state, intent, and the turn that consumed the message. The `message_state` event already carries these fields ("MD-7"). | MCP Tasks for `send_message`. A2A `GetTask`, `ListTasks`, and the status of every per-message task. | Small |
| R3 | `GET /v1/sessions/{id}/turns/{n}`, plus a list route, returning status, the causing message, the end-of-turn message, usage, and error. The `turn_end` payload ("MD-20") and the Open Responses response records already hold this data. | MCP Tasks results. A2A task results, `completed`, and `failed`. | Small to medium |
| R4 | Pending input: `GET /v1/sessions/{id}/elicitations?state=pending`, and a list of pending tool-use and harness approvals. Each entry carries schema, mode, URL, provenance, and turn. Pending elicitations are already durable, because a coordinator handoff re-synthesizes them from Postgres (§10.4). | MCP Tasks `input_required` and `tasks/update`. `2025-11-25` elicitation replies. A2A `input-required`, `auth-required`, and replies. | Small to medium |
| R5 | `Idempotency-Key` on `POST /v1/sessions/{id}/messages`, with keys scoped by tenant and subject. | MCP retries after a broken stream. A2A duplicate detection by `messageId`. | Small |
| R6 | Per-session webhook subscriptions, as listed under the table. | A2A push notification configs and delivery. Generic webhook consumers. | Large |
| R7 | MCP tool metadata as OpenAPI extensions on the session routes: tool name, title, annotations, `outputSchema`, and stability. | MCP `tools/list` and tool results. | Small |
| R8 | An unauthenticated index of runtimes that have public cards, and `GET /v1/runtimes/{name}` for one authorized runtime. | A2A public and extended cards. MCP `list_runtimes` detail. | Small |
| R9 | The adapter's paths accepted under the shared canonical resource URI. Protected resource metadata ("TS-7") served for those paths. A `WWW-Authenticate` challenge that names the shared metadata document. | MCP authorization. A2A security schemes. | Small to medium |
| R10 | A per-principal event stream of state changes across the caller's sessions. | MCP `notifications/tasks` through `subscriptions/listen`. A2A `ListTasks` across contexts. | Medium, optional |
| R11 | Session creation that commits the session and holds it until the missing links exist. | A2A `auth-required` at creation. | Medium, optional |
| R12 | An elicitation policy field on session creation. | A2A under the `block_all` rule of §21.1. | Small, optional |
| R13 | Short-lived signed download URLs for artifacts and blobs. | MCP `resource_link` fetched directly. A2A file parts. | Small, optional |
| R14 | An informational client-protocol header, recorded in audit and metrics. | Attribution for every adapter. | Small |

R6 consists of:

- create, get, list, and delete routes;
- an event-kind filter and a coalescing interval;
- a signing secret;
- opaque `subscriberData` that Lenny echoes on each delivery;
- retries and an undelivered-event log;
- an operator allowance for a relay target inside the cluster.

MCP and A2A both need R1 to R5 and R7 to R9. Only A2A push needs R6.

## What stays in the adapter

- **Transports and wire formats:**
  - Streamable HTTP for both MCP eras, JSON-RPC, and SSE framing;
  - A2A's JSON-RPC, gRPC, and HTTP+JSON bindings, and `application/a2a+json`.
- **Version negotiation and policy:**
  - MCP: `server/discover`, `initialize`, `MCP-Protocol-Version`, validation of `Mcp-Method` and `Mcp-Name`, and `HeaderMismatch`;
  - A2A: `A2A-Version` and `VersionNotSupportedError`;
  - the two-version window and the deprecation header ("MP-8").
- **Handshake-era identifiers:** the `2025-11-25` session identifier, server-sent requests encoded as routable IDs, and SSE event IDs.
- **Origin and metadata:** `Origin` validation, and the protected resource metadata document for the adapter's paths, with content taken from Lenny.
- **MCP tools and projections:**
  - the tool catalogue generated from OpenAPI;
  - tool results with `structuredContent`;
  - the projections of §15.2 and "MD-21": `notifications/lenny/*`, progress, activity, and `resource_link`.
- **MCP input and tasks:** the multi round-trip and URL-mode elicitation forms of "MP-3", and the Tasks extension's task handles, polling, and status mapping.
- **A2A:** agent cards composed from `agentInterface`, the state mapping, artifacts and chunking, file parts, `contextId` handling, and the push relay with its SSRF validation.
- **Errors and retries:** error mapping, rate-limit header forwarding, and idempotency keys derived from protocol fields.

## Features that REST serves less well

- **Handshake-era sessions.** A signed `Mcp-Session-Id` cannot be revoked by `DELETE` before it expires, and the adapter cannot enumerate live handshake sessions. Lenny's own design keeps a Redis record for this ("MP-1"). The loss is minor, because that record holds no Lenny session state.
- **Push.** The relay adds a hop and a component that the gateway must reach. The alternative, direct delivery, would make Lenny parse and emit A2A bodies and hold third-party credentials.
- **Authentication across origins.** Forwarding a bearer is compliant only within one protected resource. A separately hosted adapter needs token exchange rights.
- **Creation-time authorization in A2A.** Without R11, a missing link at creation produces a `Message` and a retry, where A2A would expect an `auth-required` task.
- **Latency and load.** Every event crosses one more process and is re-encoded, and every attached client holds two connections. The cost per event is small, and the total grows with the number of streams.
- **Cross-session listing.** Without R10, A2A `ListTasks` across contexts costs one REST call per session.

No feature examined requires gateway internals that REST cannot expose. The features above lose a property or add a hop.

## Recommendation

Move MCP out as well (option b), under three conditions:

1. REST gains R1 to R5 and R7 to R9 first.
2. The project ships the MCP adapter as a first-party component that the Helm chart deploys by default, on the gateway's origin, under one protected resource.
3. The session CLI, the client SDKs, and the playground move to REST.

Reasons:

- `2026-07-28` removed the connection state that made an in-process MCP server attractive. The `2025-11-25` era needs only a signed identifier.
- Every client-facing MCP feature Lenny uses maps onto REST once the additions exist. A2A needs the same additions, so keeping MCP built in saves none of them.
- MCP has published three dated revisions between 2025-06-18 and 2026-07-28, and Lenny's policy serves the two most recent. Moving the adapter out takes the dual-era serializers, the version window, the Tasks extension mapping, and the header rules out of the gateway's release cycle.
- One contract remains for every surface. The §15.2.1 contract tests become a conformance suite that every adapter runs against REST. Rule 1 of §15.2.1 holds by construction, because only REST exists.
- Lenny's own clients use the same REST and SSE contract that third parties use, so that contract is tested continuously.

Reasons to keep MCP built in (option a):

- no extra hop on the primary interactive path;
- no deployment condition on the resource URI;
- handshake sessions that can be revoked;
- an "MCP-native" claim that needs no second component.

These costs are real but bounded, and none of them blocks a feature.

## What leaves Lenny

Under both options, these leave:

- The planned `A2AAdapter` and `AgentProtocolAdapter` (§21.1 and §21.3).
- The `/.well-known/agent.json` endpoints and the per-runtime card endpoints of §15.1 "Discovery and introspection", along with `wellKnownAgentJsonMaxCards`.
- The A2A `OutboundChannel` push contract and its `X-Lenny-Signature` signing.
- The A2A columns of the §8.8 protocol mapping, of the Translation Fidelity Matrix, and of the §28.5.3 type table. They move to the adapter project as its mapping documentation.
- The coupling to `elicitationDepthPolicy: block_all` in §21.1, and the capability-consistency invariant for A2A.
- The OpenAI Chat Completions, Open Responses, and Anthropic Messages adapters, if the owner applies the same rule to every protocol adapter. They are outside this spike. The Open Responses conversation sessions read response records on the session row (§15 "Open Responses conversation sessions"), which R3 would expose.

Under option a, Lenny keeps the `MCPAdapter`, and the `ExternalAdapterRegistry` holds MCP alone. These parts lose their purpose and can be removed or reduced to MCP:

- runtime registration of adapters (`POST /v1/admin/external-adapters`);
- the validation gate;
- the config-driven plugin tier;
- `OutboundCapabilitySet`.

Under option b, the following also leave:

- **MCP endpoint:** the `MCPAdapter` at `/mcp` and `/mcp/environments/{name}`, the dual-era serializers, the Redis handshake record and `gateway.mcp.handshakeSessionIdleTimeoutSeconds`, `server/discover`, and the MCP header and `Origin` validation ("MP-1", "MP-2").
- **Version handling:** the MCP version rule, `X-Lenny-Mcp-Version-Deprecated`, `MCP_VERSION_UNSUPPORTED`, `MCP_PROTOCOL_VERSION_RETIRED`, and `lenny_mcp_deprecated_version_active_sessions` ("MP-8").
- **Elicitation and Tasks:** the MCP elicitation forms and the Tasks extension mapping, including the MCP column of §8.8 ("MP-3", "MP-4").
- **Projections:** the §15.2 per-kind wire projection, the `notifications/lenny/*` namespace, the MCP rows of "MD-21", and MCP progress.
- **Adapter framework:** the `ExternalProtocolAdapter` interface, `BaseAdapter`, `OutboundChannel` and its back-pressure policy, the dispatch-filter rule for adapters, the `AdapterCapabilities` annotations in discovery, the registry, and `RegisterAdapterUnderTest` as a gate inside the gateway. The `SessionEvent` kind registry stays, as the REST event contract.
- **Lenny's own clients:** MCP use in the session CLI, the client SDKs, and the playground.

Under both options, these stay:

- the REST API and its SSE stream;
- the session, message, and elicitation services behind them;
- the outbound connector client and the reference MCP bridge (runtime-facing, out of scope);
- `/mcp/management` in `lenny-ops`. It is operator-facing and also generated from OpenAPI, so the same reasoning could move it later.

## Proof of concept

The proof of concept has three programs:

- `poc/mock_lenny.py` mocks the REST routes that the spike relies on, including R1 to R5. It enforces session ownership as §10.2 rule 7 states, and it uses a small replay buffer so that `gap_detected` occurs.
- `poc/adapter.py` is a stateless adapter. It serves a subset of MCP `2026-07-28` with the Tasks extension at `POST /mcp`, and A2A 1.0 `message:stream` in the HTTP+JSON binding.
- `poc/demo.py` starts the mock and two adapter replicas, and it alternates steps between the replicas. `poc/demo-output.txt` holds its output. To run it, run `python3 -I demo.py` from `poc/`.

What it showed:

1. A `send_message` task with ID `<sessionId>.<messageId>` was created through replica A. Polling through replica B reached `input_required`, with the pending elicitation in `inputRequests`. A `tasks/update` through A answered it, and polling then reached `completed` with the turn's text. The adapter derived each state from R2, R3, and R4 alone.
2. Bob's `tasks/get` on Alice's task returned "task not found", because the forwarded bearer met Lenny's ownership rule. Task IDs need no signature.
3. A retry of `send_message` with the same idempotency key, through the other replica, returned the same task (R5).
4. An attach stream dropped on A after sequence 5 resumed on B from sequence 5 with no loss. A resume from an evicted sequence received one gap frame. Both used the existing REST `Last-Event-ID` behaviour.
5. A2A `message:stream` on an existing context attached at the receipt's sequence number (R1). It emitted the task, a working update, artifact chunks, and `TASK_STATE_COMPLETED`, with no output missed. On a new context with a question, it ended at `TASK_STATE_INPUT_REQUIRED`, with the elicitation schema in a data part.

The proof of concept does not cover the push relay (R6), authorization and audiences (R9), the `2025-11-25` handshake era, uploads, or the A2A JSON-RPC and gRPC bindings.
