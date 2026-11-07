# Connector authorization and external memory

Status: design for owner review, revision 7, 2026-11-06, with no independent review yet. Revision 2 recorded the owner's answers to the open decisions and added "Instructions for reviewers of other designs". Revision 3 settled the last one, the link check before an explicit resume. Revision 4 recorded the Claude Code bridge spike under "CA-9". Revision 5 delivered the bridge to every sidecar `type: agent` pod. Revision 6 recorded the startup measurement and the owner's choice of a separate init container. Revision 7 changes only pointers after the user credential documents split. It reads against `user-credential-delivery-design.md` revision 19, `user-git-identity-design.md` revision 28, `tenant-scoping-identity-design.md` revision 10, `tenant-outbound-connector-design.md` revision 9, and `egress-and-git-design.md` revision 49. Nothing here is built, and nothing in `spec/` changes until a proposal lands it through the change-proposal pipeline.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A probe run on 2026-11-03 against the real package under test, through a Go build overlay with no change to the tree, with a stub as the external MCP server. Also the Claude Code spike and the startup measurement under "CA-9", whose conditions that section states. |
| **[source]** | Read in the Lenny tree or the specification. |
| **[documented]** | Stated on a vendor or MCP specification page under "Sources", read and not tested. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

Lenny carries a built-in memory feature: the `MemoryStore` role, a Postgres and pgvector backend, the `lenny/memory_write` and `lenny/memory_query` platform tools, and `/v1/sessions/{id}/memory` routes. It makes Lenny responsible for retrieval quality, embeddings, capacity, retention, tenant isolation, and mandatory erasure of a data type unrelated to running agent sessions. Its default `HashingEmbedder` measures token overlap, and dedicated memory products exist.

Sessions still need a way to reach state that outlives a pod. MCP connectors are the generic mechanism for this, and a deployer can register any MCP memory server as one. That route does not work today, and the reasons are general to every connector that requires per-user authorization:

- An unlinked user's call fails with `unexpected status 401` and no prompt, although §9.3 and `docs/tutorials/using-connectors.md` both describe a prompt **[measured]**.
- An expired token is sent as it is and never refreshed **[measured]**.
- A session with no delegation policy is offered every active connector in the tenant **[source]**.
- A stock coding CLI cannot dial the adapter's connector sockets **[source]**.
- The OAuth callback is open to an account-link attack, recorded in `user-credential-delivery-design.md` under "Defects found while verifying" **[source]**.
- A delegated child loses its parent's Environment, so a token linked under that Environment is not found **[measured]**.

This design removes built-in memory and fixes the connector path so the external route is usable for any connector.

## Scope

This design covers:

- Linking a user to a connector before or during a session.
- Token refresh and endpoint binding for connector tokens.
- Default-deny connector access.
- A link check at session creation, resume, and delegation.
- Propagating an authorization failure to the runtime.
- A runtime capability for tolerating a late tool list.
- A stdio bridge for stock CLIs.
- Removal of the memory feature, and the documented external pattern that replaces it.

Out of scope:

- The semantic cache. It shares `memorystore.Embedder`, so "CA-10" moves that code if the cache outlives memory.
- Service-identity authorization to connectors. An automated caller defers the connector in this revision.
- A reference memory server. The owner may add one when a customer asks.
- Moving connector tokens out of the gateway (F-4.3.1).

`user-credential-delivery-design.md` owns account links and tokens delivered to pods, and `user-git-identity-design.md` configures them for repository hosts. A connector token stays on the gateway path, and its use by the gateway remains the one way it is spent. "§9.3" means §9.3 Connector Definition and OAuth/OIDC throughout.

## Terms

| Term | Meaning |
| :-- | :-- |
| Connector link | A stored connector token for one tenant, connector, user, and Environment. |
| Linked user | A user who holds a usable connector link for the session's Environment. |
| Unlinked user | A user who holds no usable connector link for that connector and Environment, even though the connector is registered and permitted. |
| Link check | The evaluation in "CA-4" that decides whether a connector needs a link before a runtime starts. |
| Deferred connector | A connector the caller chose to run without for a session and its descendants. |
| Explicit resume | `POST /v1/sessions/{id}/resume`, or the `lenny/resume_session` tool, which posts to the same route. |
| Implicit restart | A new runtime process with no client request waiting on it: the retry driver, message-triggered restoration of a released pod, a credential-rotation restart, and tree recovery. |
| Connector prompt | A URL-mode elicitation with `initiator_type: connector`, raised by the gateway. |

## Owner decisions

These are settled by the owner, and the rationale stays here so a later reader does not reopen them by accident.

| Decision | Text or effect |
| :-- | :-- |
| Remove built-in memory | The owner asked for "Dropping memory store in Lenny + replacing with this generic mechanism". |
| No stored tool catalog | The owner rejected registering tools and arguments apart from the MCP server: "that creates a shadow config that will be out of sync". `tools/list` always asks the server. |
| The authorization failure reaches the runtime | "Shouldn't the auth error be propagated to the runtime so it knows it needs to authenticate?" The failure is translated at the gateway boundary, because the runtime's server is the adapter's local server. See "CA-5". |
| A link check runs regardless of runtime | The owner: "the Coder-style check makes sense no matter what". |
| The trigger is a connector property | The owner replaced a pool-level "required" flag with "requiring auth to list tools". This design detects that property live, for the owner's own reason that a stored flag can drift. |
| The runtime declares tolerance | "The runtime should indicate whether they can gracefully handle failed `tools/list`". See "CA-8". |
| A parent retries after a link | No push notification. The refusal carries `details.retryAfterSeconds`. See "CA-7". |
| Impersonation and linked connectors | "Accept and document the risk." No check is added for this design's sake. See "Limits". |
| Connector tokens stay keyed by Environment | The reason is separation: a token granted in one project context is not the token another context draws. Policy controls access under either key, so policy is not the reason. |
| The capability snapshot stays | "Keep the snapshot and don't change anything else." A deployer who wants enforcement downstream uses OAuth scopes. See "CA-8". |
| An explicit resume checks links first | The check runs before an explicit resume of a released pod starts its new process, so a user who links gets the connector on that resume. See "CA-6". |
| The bridge reaches every runtime | The platform delivers it to every sidecar `type: agent` pod, using the runtime supervisor's delivery pattern. See "CA-9". |
| The bridge keeps its own init container | The owner chose this over sharing `lenny-supervisor-stage`, accepting about one second per pod and leaving the supervisor untouched. See "CA-9". |
| The shared OAuth flow lands first | It is its own proposal, "CA-0", ahead of this design's other proposals and of UC-2 in `user-credential-delivery-design.md`. "Instructions for reviewers of other designs" carries the changes this requires elsewhere. |

## Requirements

Alice and Bob are developers at `acme`, and Carol is its tenant admin. `globex` is another tenant.

- CAR-1. A deployer can give sessions persistent memory by registering an MCP server, with no Lenny code for that server.
- CAR-2. A user can link a connector before a session exists, from any client.
- CAR-3. A runtime learns that a connector needs authorization through a typed error, and Lenny stores no copy of a connector's tools.
- CAR-4. A runtime that cannot recover from a failed `tools/list` never starts with a connector that would fail it, unless Lenny has no way to know.
- CAR-5. An implicit restart never fails because a connector link is missing.
- CAR-6. A delegated child uses its parent's user and Environment for connector tokens.
- CAR-7. No authorization URL or connector token reaches a pod, a runtime, or a model.
- CAR-8. An expired token refreshes without user involvement while its refresh token is valid.
- CAR-9. A session reaches a connector only when a delegation policy grants it.
- CAR-10. A token is spent only at the endpoint it was linked for.

## Threat model

### Security properties

- CP-1. A connector token stays in the gateway's connector store and in the gateway's outbound request. §9.3 states this invariant, and this design keeps it.
- CP-2. Only the subject who requested a link can complete it, and the resulting token lands under that subject.
- CP-3. An agent cannot choose the URL of a connector prompt, and it cannot see or answer one.
- CP-4. An anonymous probe sends no credential and no user data, and it reaches only a registered connector endpoint.
- CP-5. A connector grant is explicit, and a missing or unresolved policy layer denies.
- CP-6. A stored token is unusable once the connector's `mcpServerUrl` or token endpoint changes.

### Threats

| Attacker | Can try | Result under this design |
| :-- | :-- | :-- |
| Attacker in the tenant | Send a victim a link URL, so the victim's token lands under the attacker. | Refused by CP-2: the start page requires the flow's own subject, and a cookie binds the browser. |
| Prompt-injected agent | Raise prompts repeatedly to wear a user down. | The gateway raises at most one prompt per tree and connector, and a dismissal stands for that tree. An agent cannot supply the URL. |
| Prompt-injected agent | Read or answer a connector prompt through an intercepting parent. | Refused by CP-3: connector prompts skip interceptors and go to the human-facing edge. |
| Prompt-injected agent | Send source code to a memory server in tool arguments. | Not prevented once granted. This path runs on the gateway and passes no `netagent` or `lenny-egress` check. CP-5 and "Code exposure" state the controls. |
| Malicious content in memory | Plant text in memory that steers later sessions. | Not prevented. Memory returned to a model is untrusted input, and `PostConnectorResponse` is the point where a deployer can scan it. |
| Platform admin impersonating a user | Message Alice's session so its agent writes to her external memory with her token. | Not prevented, by owner decision. See "Limits". |
| Administrator editing a connector | Point a linked connector at a different server. | CP-6 makes the stored tokens unusable, and each user links again. |
| Malicious MCP server | Answer the anonymous probe with 401 to force prompts, or with 200 to skip them. | It affects only its own connector. A 200 followed by a 401 reaches the tool-call path in "CA-5". |

## Current state

| Claim | Truth | Evidence |
| :-- | :-- | :-- |
| Tokens are per user | True. They are keyed on tenant, connector, user, and Environment. Another user's call gets a 401. | `connectorcredstore.go`, **[measured]** |
| A linked user can list and call tools | True. | **[measured]**; `TestExternalMCPToolPerDelegationScoping` and `TestOAuthConnector` pass |
| §9.3's in-session prompt exists | False. `bearerFor` returns an empty string, the client dials with no token, and the error is a generic failure. No code raises a connector-initiated elicitation, and a comment in `mcptools/elicitation.go` calls that path "future". | `invoker.go`, **[measured]** |
| Refresh happens | False. `RefreshAccessToken` and `RotateAccessToken` have no non-test caller, and an expired token with a valid refresh token gets a 401 with zero refresh calls. | **[measured]** |
| A token under another Environment is found | False. The lookup is exact, and a mismatch reads as a 401. | **[measured]** |
| Each connector has its own `tools/list` | True. The adapter opens one `@lenny-connector-<id>` socket per connector, and the provider binds to that connector. | `connectortoolprovider.go`, `connectormcp.go` |
| `initialize` on a connector socket contacts the external server | False. The adapter answers it locally, so the failure first appears at `tools/list`. | `pkg/adapter/mcp/server.go` |
| An external 401 reaches the runtime as such | False. It becomes gRPC `Internal`, then JSON-RPC `-32603`, with the status only in message text. | `leasecontrol/connectortools.go`, `mcp/server.go` |
| The adapter can send `list_changed` | False. It declares `tools: {}`, and its loop only answers requests. | `mcp/server.go` |
| A session with no policy gets no connectors | False. `AuthorizeConnector` permits when no layer resolves. | `connectorauthz.go` |
| A child keeps its parent's user | True. | `buildChildSession`, **[measured]** |
| A child keeps its parent's Environment | False. A parent in `team-a` produces a child with an empty Environment. The child therefore also loses the Environment's default policy and its `connectorSelector` capability filter. | `buildChildSession`, **[measured]** |
| A derived session keeps its source's Environment | No code in `derive.go` copies it. | `derive.go` **[source]** |
| A connector prompt reaches the user through an intercepting parent | False. `WalkChain` stops at an intercepting ancestor for connector and agent initiators alike. | `pkg/elicitation/chain.go`, **[measured]** |
| A gateway-originated elicitation has a precedent | True. §8.6 lease extension raises one through the interaction store. | `leaseelicit.go` |
| A resume reopens connector servers | True. `Resume` calls `sessionConnectors`, rewrites the manifest, and starts the runtime, while a pod-held resume starts no process. | `pkg/adapter/resume.go`, `sessionserver/resume.go` |
| A failed connector resolution aborts a start | False. It leaves the session with no connector servers. | `pkg/adapter/connectormcp.go` |
| Derived sessions lose connector tokens | The specification says so in §7.1, under **Connector state.** The code keys tokens by user, so a derived session by the same user in the same Environment finds them. | §7.1, `connectorcredstore.go` |
| Connector tokens are erased with a user | True. `connectorCreds` is in the `tokens` eraser. | `cmd/lenny-gateway/adminrouter.go`, `tenant_erasers.go` |
| Memory REST routes are in §15.1 | False. `route_table.go` mounts them and §15.1 has no row for them. | **[source]** |
| Each connector tool call has an audit event | False. The audit catalog has events for connector administration and the OAuth flow. A tool call produces a trace span, and `PreConnectorRequest` and `PostConnectorResponse` interceptors see it. | `invoker.go`, `pkg/observability/audit/catalog.go` |
| OAuth scopes reach the provider | True. `BuildAuthorizationURL` sends the connector's `auth.scopes`. The server decides what a token may do. | `pkg/connectoroauth/flow.go` |

## CA-1: Link flow

### CA-0: Shared OAuth link flow

The account-link attack exists in today's code, and tying its fix to either feature would leave it open until that feature ships. The owner therefore decided that the shared flow is its own proposal and lands first. It takes the hardened sequence in `user-credential-delivery-design.md` under "Flow" in UC-2, moves it into `pkg/connectoroauth`, and converts today's §9.3 handlers to it, which closes the defect with no other feature.

| Piece | Owner |
| :-- | :-- |
| One-time flow identifier, with a 10-minute lifetime, naming a subject | Shared |
| Start page: OIDC login, and refusal when the logged-in tenant and subject differ from the flow's | Shared |
| `__Host-` cookie nonce, PKCE, and a `state` signing the flow identifier and the nonce's hash | Shared |
| Callback validation: signature, single consumption, cookie nonce, and `iss` where the provider returns it | Shared |
| A hook that receives the validated code and verifier | Shared interface |
| What the start page displays, the per-provider callback path, and any digest compared on return | Supplied by each caller |
| Code exchange and token storage | Connectors: the gateway, into the connector credential row. UC-2: the Token Service, with identity binding and a confirmation page. |

### Connector routes

The record this flow writes stays the existing connector credential row, because `user-git-identity-design.md` withdrew any route for a link token to reach the connector path, under "User tokens on a VCS host".

| Route | Purpose |
| :-- | :-- |
| `POST /v1/connector-links` | Body `{"connector": "memory", "environment": "team-a"}`. Returns `linkUrl`. |
| `GET /v1/connector-links` | Lists the caller's links with connector, Environment, expiry, and whether a refresh token exists. It returns no token. |
| `DELETE /v1/connector-links/{connector}?environment=` | Removes the caller's link and revokes it at the provider when the provider has a revocation endpoint. |
| `GET /v1/connector-links/start/{flow}` | The browser start page. |
| `GET /v1/connector-links/callback` | The provider's redirect target. |

`lenny-ctl connector link <id> [--environment <name>]` wraps the first route and prints or opens the URL. `POST /v1/admin/connectors/{name}/oauth/authorize` and `GET /v1/admin/connectors/oauth/callback` are removed in "CA-A", since no deployment depends on them.

1. The link URL is a Lenny URL for a shared flow naming the subject and Environment.
2. The shared start page runs its login and subject checks.
3. The start page names the connector and its endpoint hosts, runs `CheckConnectorURLDomain` on the provider URL, and redirects.
4. The shared callback validates the return and calls the connector hook. A missing or mismatched cookie aborts the flow.
5. The gateway exchanges the code and stores the token with the endpoint digest from "CA-2".
6. The gateway resolves every pending connector prompt for that tenant, user, connector, and Environment, and notifies the affected sessions under "CA-8".

An impersonation bearer is refused on every link route, as `user-credential-delivery-design.md` requires for `/v1/user-links` under "Flow".

## CA-2: Token lifecycle

**Refresh.** `bearerFor` refreshes when a token has fewer than 5 minutes left, and once more after a 401 from the server. Refresh rotation means one writer per credential, so the row carries `refresh_lease_holder` and `refresh_lease_until`, using the lease form under "Refresh" in `user-credential-delivery-design.md`. Other replicas re-read the row for up to 5 seconds. Both durations are operator-tunable.

- `invalid_grant` deletes the token, and the user is unlinked from then on.
- A transient refresh failure returns the transient error and keeps the row.

**Endpoint binding.** The credential row records a digest of `mcpServerUrl`, `authorizationEndpoint`, and `tokenEndpoint`. A lookup whose digest differs from the connector's current one treats the row as absent and audits `connector_link.endpoint_changed`. This closes a gap in which an edit to `mcpServerUrl` sends every stored token to the new server **[source]**.

**Resource indicator.** `BuildAuthorizationURL` sends no `resource` parameter **[source]**. A proposal checks the MCP authorization page for its requirement on resource indicators and adds the parameter if the page requires it **[pending]**.

## CA-3: Connector access and Environment

**Default deny.** `AuthorizeConnector` returns `ErrConnectorNotPermitted` when no policy layer resolves, for every candidate of type `connector`. An error resolving a policy fails closed. Carol grants the connector with a `DelegationPolicy` target of type `connector`, on the runtime or as the Environment's default. This reverses the "conservative fall-through" comment in `connectorauthz.go`, which stays correct for other candidate types.

**Environment on a child.** The row `buildChildSession` writes carries the parent's Environment. `tenant-scoping-identity-design.md` already requires this under "Delegated child's Environment" and decision 14. This design depends on that change and lands it itself if that design has not landed first.

**Environment on a derived session.** A derived or replayed session carries its source's Environment, as that design also requires. §7.1 **Connector state.** is rewritten: a connector link belongs to a user and an Environment, so a derived session reuses it under those keys.

## CA-4: Link check

### Evaluation

For each active connector the session's policy permits, in the Environment the session will run in, and excluding deferred connectors:

| Situation | Result |
| :-- | :-- |
| The connector has no `auth` block | `open` |
| A token exists and is valid, or it refreshes | `linked` |
| A refresh returns `invalid_grant`, and the probe says auth is needed | `needs_link` |
| No token, and the probe returns 401 at `initialize` or `tools/list` | `needs_link` |
| No token, and the probe returns 403 with `error="insufficient_scope"` in `WWW-Authenticate` | `needs_link` |
| No token, and the probe succeeds | `open` |
| No token, and the probe times out, fails on the network, or returns anything else | `unknown` |

**The probe.** The probe is `ListTools` with an empty bearer, which is what the invoker does today for an unlinked user **[measured]**. Its result depends on the connector and not on the user, so each replica caches it per tenant and connector for 60 seconds. The cache is short enough to leave no standing copy of a server's rules. The probe has a 3-second timeout. A probe result of `open` or `unknown` never blocks a session. A tool that needs auth despite an open listing reaches "CA-5" at call time, where any client can recover.

A linked user sends no probe, so the cost falls only on users without a token.

**A stored field is refused.** A field such as `requiresAuthToListTools` is the same drift the owner rejected for catalogs.

### Placement

The check runs after experiment routing and the late `PreRoute` chain, because both can rewrite the runtime and pool, and the capability it reads belongs to the final runtime. In `create.go` that is after `routeExperiment` and `preRouteAfterExperiment` and before `claimMintPersist`. §7.1's credential pre-check, step 3, runs before step 3a's routing, so the link check is a new step between 3a and 4. No pod is claimed while a person is in a browser. Like the credential pre-check, it is a point-in-time check rather than a reservation.

It applies to `POST /v1/sessions`, `POST /v1/sessions/start`, derive, replay, and the external protocol adapters' implicit session creation. It applies to sessions with `executionMode: session`.

### Outcome

| Runtime `deferredToolDiscovery` | Any connector `needs_link` | Result |
| :-- | :-- | :-- |
| `false` (default) | Yes | `403 CONNECTOR_LINK_REQUIRED` (`POLICY`) with `details.connectors`, each with `id`, `displayName`, `environment`, and `linkUrl`. Naming the Environment makes a link held under another Environment easy to diagnose. |
| `true` | Yes | The session is created, and the response lists the same connectors under `unlinkedConnectors` so a client can link early. |
| Either | No | The session is created. |

The request accepts `deferConnectors: ["memory"]`, stored on the session row. A deferred connector raises no prompt. On a `false` runtime it is left out of the manifest. On a `true` runtime it stays in the manifest, and its `tools/list` returns the typed error. A link that exists when a runtime starts wins over a deferral, so a user who links later gets the connector at the next start.

**Why every permitted connector counts.** The owner replaced a "required" flag with this property, so a session is refused for a connector it may not use. Default deny keeps the set to what an administrator granted, and `deferConnectors` handles the rest. A workload that cannot run without a connector does not defer it.

**Clients.**

- A client that cannot pass the field, such as a stock OpenAI SDK, shows the error, and its user runs `lenny-ctl connector link`.
- An automated caller with no browser defers.
- MCP clients receive the same `details` in the tool error.

## CA-5: Propagating the failure

### Why the raw 401 stops at the gateway

The runtime is an MCP client, and its server is the adapter's local server, which it has already authenticated to with the manifest nonce. External authorization is a separate hop between the gateway and the external server, as §9.1 and §9.2 state. An HTTP 401 cannot cross for three reasons:

- A Unix socket or stdio pipe has no HTTP status, and the MCP authorization page says stdio implementations should not follow its flow **[documented]**.
- A client that ran OAuth itself would hold the token in the pod, against CP-1.
- The person is at the Lenny client. Claude Code's documentation says a `claude -p` run cannot run the OAuth flow **[documented]**.

MCP's `URLElicitationRequiredError` (`-32042`) has the right meaning, since the adapter's server needs third-party authorization that must not transit the client. It also puts the URL in the client's hands inside a pod with no browser and arrives in protocol version `2025-11-25`, where the adapter speaks `2025-03-26` **[documented]** **[source]**. CAR-7 keeps the URL out of the pod.

### Translation

1. The connector client returns a typed `ErrAuthRequired` on 401, and on 403 with `insufficient_scope`.
2. `connectortools.Bridge` passes it, and `leasecontrol` returns `codes.FailedPrecondition` with an `ErrorInfo` reason `CONNECTOR_LINK_REQUIRED`.
3. The adapter's `mcp.Server` gains a `data` field on errors and maps by reason:

| Method | Runtime receives |
| :-- | :-- |
| `tools/list` | JSON-RPC error `-32010`, message `Connector memory needs user authorization. The user has been asked.`, and `data: {"connector": "memory", "reason": "link_required"}`. |
| `tools/call` | A result with `isError: true` and the same text, so a model can read it and retry later. |

Both return immediately. A tool listing cannot wait on a person, since Codex's startup timeout defaults to 10 seconds **[documented]**.

**What reaches the model.** Under Claude Code, the `tools/call` text reaches the model verbatim, and the model can retry. The `tools/list` error does not reach it: the session reports the server as `connected` with no tools, and the model said it could see no error **[measured]**. On that path the runtime knows and the agent sees only a missing tool, so the connector prompt to the user carries the message.

### Connector prompts

On the same failure, the gateway raises a connector prompt unless the connector is deferred.

- **Identity.** It carries `initiator_type: connector`, `connector_id`, `purpose: oauth_login`, and the provider's `expected_domain`.
- **Interceptors.** `WalkChain` skips `Hop.Intercepts` for connector initiators, and a new tier 1 test pins this. CP-3 depends on it, and today's behaviour is the opposite **[measured]**.
- **Suppression.** §9.2 already exempts connector OAuth from depth suppression, and `DepthBlockAll` already returns false for this initiator **[source]**.
- **Deduplication.** One prompt per `root_session_id` and connector is pending at a time. A dismissal stands for that tree. An expired prompt can be raised again once per `maxElicitationWait`.
- **Lifetime.** It is non-blocking, unlike `leaseelicit`. It expires at `maxElicitationWait` and is dismissed when the tree ends. §9.2's rule that expires a `lenny/request_elicitation` when a session leaves its pod does not apply, since no runtime waits on it.
- **Budget.** It does not count against `maxElicitationsPerSession`, since an agent cannot produce more than the deduplication allows.
- **URL.** The URL is the Lenny link URL. §9.2 **URL domain validation** moves for connector prompts: the domain check runs on the provider URL at the start page, while the prompt's own URL names the gateway. `expected_domain` remains in provenance for display.
- **Completion.** A client's `accept` only means the user will open the link, as in MCP's URL mode **[documented]**. The callback in "CA-1" resolves the prompt.

A client whose adapter reports `supportsElicitation: false` sees nothing, and the error text tells the agent what is needed. Its user links through `lenny-ctl`.

## CA-6: Resume and restart

These paths exist today **[source]**:

| Path | Code | New runtime process | Client waiting | Behaviour **[design]** |
| :-- | :-- | :-- | :-- | :-- |
| `suspended`, pod held | `resumeHeldPodRequest` | No | Yes | No check. The runtime keeps its tool list. An expired token refreshes on the next call, or reaches "CA-5". |
| Explicit resume, `awaiting_client_action` or `suspended` with pod released | `handleResume` into `enterResumePending` | Yes | Yes | Link check before `enterResumePending`, with the "CA-4" outcomes. The body accepts `deferConnectors`. A refusal leaves the state unchanged, and that state's clocks, such as `maxSuspendedSessionSeconds`, keep running. |
| Explicit resume that finds the held pod gone | `resumeHeldPodRequest` fallback | Yes | Yes | Treated as an implicit restart, since the request has already passed the point of a clean refusal. |
| Message arrives for a released session | `messages_delivery.go`, `suspended_backlog.go` | Yes | Sender only | Implicit restart. |
| Retry driver, `resume_pending` to `resuming` | `failure.go`, resume driver | Yes | No | Implicit restart. |
| Credential-rotation restart at Basic and Standard levels | §15.4.3 matrix | Yes | No | Implicit restart. |
| Tree recovery | §8.2, §7.3 | Yes | No | Implicit restart. |

**Implicit restart.** `ListSessionConnectors` is the one call every start path reaches: start, SDK warm, and `Resume` all go through `sessionConnectors` **[source]**. It gains the rule below, so a restart needs no path-specific code.

- On a `false` runtime, a connector that evaluates to `needs_link` is left out of the manifest. The session continues without it.
- On a `true` runtime, the connector stays, and the failure reaches `tools/list`.
- Either way, the gateway raises a connector prompt and emits `session.connector_unavailable` with the connector and reason.

**Omission rather than a failing server on a `false` runtime.** A server that fails `tools/list` can break a stock CLI's start, for example Codex with `required = true` **[documented]**, and an omitted server cannot. A resolution failure already omits connectors today, so runtimes already tolerate an absent entry **[source]**.

**The connector comes back at the next runtime start.** On a `false` runtime, a link completed mid-session has no effect until then, because the manifest and sockets are fixed for the process. A user who needs it sooner suspends with pod release and resumes, or starts a new session.

**Why no refusal on these paths.**

- A retry driver has no one to answer it, and a refusal would turn a recoverable pod failure into a session failure.
- `maxResumeWindowSeconds` and `maxTreeRecoverySeconds` keep running, so a wait for a person could fail a whole tree.

**Latency.** Probes run in parallel and use the cache, so the added resume time is bounded by the 3-second timeout. A probe that returns `unknown` includes the connector.

**Deferral.** `deferConnectors` persists on the row, so a resume honours the earlier choice without a prompt.

**Confirmed.** The adapter re-resolves connectors on `Resume` and writes the manifest before starting the runtime, so this rule takes effect on every restart with no adapter change **[source]**. `TestSessionConnectors_spec_9_3_142` covers the resolve path, and a new test pins each row above.

## CA-7: Delegated children

### Identity

A child uses `parent.UserID` **[measured]**, so the parent's user's link is the right token, and `TestExternalMCPToolPerDelegationScoping` covers policy scoping on that path. With "CA-3" the child also has the parent's Environment. Under the cross-Environment alternative of `tenant-scoping-identity-design.md`, a child in another Environment evaluates under its own Environment and needs a link there.

### Check at delegation

The link check runs in `buildChildSession` immediately after `ApplyExperimentRouting`, because routing can change the child's runtime and the capability belongs to the final runtime. Export materialization runs earlier in that function, and a returned error already undoes it through `undoChildExport`, and the deferred release returns the tree budget **[source]**. The refusal therefore wastes export work but leaves no child row, pod, or budget behind. §8.3's credential pre-check at delegation is the precedent.

| Child runtime `deferredToolDiscovery` | Child's permitted connector `needs_link` | `lenny/delegate_task` result |
| :-- | :-- | :-- |
| `false` | Yes, and not deferred | `CONNECTOR_LINK_REQUIRED` with `details.connectors` naming `id` and `displayName`, and `details.retryAfterSeconds`. The tool error carries no URL. The gateway raises one connector prompt against the parent session. |
| `true` | Yes | The child starts. Its `tools/list` fails, and the prompt is raised from the child. The child lists again after `list_changed`. |

**Deferral.** `delegate_task` accepts `deferConnectors`. A child inherits the union of its parent's deferred connectors and its own, so a root that deferred `memory` causes no prompt or refusal anywhere in its tree. An automated root therefore runs a whole tree with one field.

**Why a refusal rather than silent omission.** An earlier discussion leaned towards leaving the connector out and telling the parent. Refusal is the better default for three reasons:

- It matches the root's behaviour.
- A parent that does nothing special gets a visible failure rather than a child quietly missing a tool.
- The parent can retry with a deferral in a single call.

An LLM parent reads the typed error, and a programmatic orchestrator handles it as it does `CREDENTIAL_POOL_EXHAUSTED`.

**Where the prompt goes.** The child does not exist yet, so the prompt's origin is the parent. It skips interceptors and goes to the root's human-facing edge. A parent agent never sees the URL, which is why the tool error omits it.

**How the parent learns of completion.** The parent retries `delegate_task`, waiting at least `details.retryAfterSeconds`, which defaults to 30 seconds and is operator-tunable. The interval exists because each retry by an LLM parent can cost a model turn. A retry raises no second prompt, because of the deduplication in "CA-5". The owner decided against a push notification. A gateway-origin inbox message reaches only runtimes with `capabilities.injection.supported`, which defaults to `false`, and holding the call open risks client tool timeouts.

**Detached clients.** A prompt with no attached client stays pending until `maxElicitationWait`. The parent's retries keep failing and the parent decides whether to defer, so nothing deadlocks. A pending prompt raises no `input_required` state, since no session blocks on it, and §8.8's deadlock detection is unaffected.

**Children on restart.** A child's restart is an implicit restart under "CA-6", including under tree recovery. An omitted connector on a child is visible to its parent only through events the parent can read under its tree visibility.

### Confirmed against §8

| Question | Answer | Evidence |
| :-- | :-- | :-- |
| Does the child's token lookup use the right user? | Yes. | **[measured]** |
| Does it use the right Environment? | Not today. "CA-3" fixes it. | **[measured]** |
| Does a child's connector set come from its own policy? | Yes. `ListSessionConnectors` and `CallTool` both evaluate the child session's policies. | `connectorauthz.go` |
| Can a child reach a human for OAuth at any depth? | Yes by §9.2 and `ShouldSuppress`. `WalkChain` then stops at an interceptor, which "CA-5" fixes. | **[measured]** |
| Does a refusal leak resources? | No. The error path undoes the export and returns the budget. | `service.go` |

## CA-8: Runtime capability and late tools

`capabilities.deferredToolDiscovery` joins `preConnect` in §5.1 Runtime, defaulting to `false`. `true` states both of these:

- The runtime keeps a connector server after `tools/list` returns an error.
- It lists again on `notifications/tools/list_changed`.

A single field covers both, because one without the other does not help. `lenny-compliance` gains a check that runs a stub connector through a failure and a notification, in the §15.4.6 battery beside `connector_mcp_server_reachability`. A runtime claiming `true` must pass it, and the suite reruns on each upstream release. Claude Code 2.1.317 showed both behaviours in the spike under "CA-9" **[measured]**, so it can claim `true` once it passes this check and the adapter changes below exist. It stays `false` until then. Codex stays `false` until it is measured.

Claude Code re-lists on the notification, but in the spike the model called the tool only because its prompt told it to check again, and nothing in the stream showed the model being told the tools had arrived **[measured]**. A `true` runtime therefore gains the tool without any assurance that the agent will notice it.

**Adapter changes for a `true` runtime.**

- Declare `tools: {"listChanged": true}` on connector servers.
- Allow the server to write a notification on its open connections, with writes serialized against responses.
- Add a gateway-to-adapter RPC on `LNK-POD-GRPC`, named `NotifyConnectorToolsChanged`, carrying the session and connector. Its channel placement in §28 follows the naming law at proposal time **[pending]**.
- The link callback can land on any replica, so the call uses `CH-HOLDERFORWARD` to reach the session's coordinating replica.

No runtime needs this until one claims `true`, so this work can follow the `false` path.

**Capability metadata.** §5.1 derives `conn.ToolCapabilities` at registration and on refresh, and the §10.6 filter reads it **[source]**. This is a smaller version of the snapshot the owner rejected for tool catalogs. The owner decided to keep it and change nothing else, and this design adds no work to it. Its behaviour is:

- A tool with no inferred capability falls to the conservative default, so a new tool name fails closed under a restrictive filter.
- An existing tool whose behaviour changes keeps its old classification until a refresh.
- A server that requires auth to list tools has no inference until an administrator links and refreshes it, so a restrictive filter denies its tools until then.

**Enforcement downstream.** A deployer who wants permissions enforced by the server uses OAuth scopes. `BuildAuthorizationURL` sends the connector's `auth.scopes` **[source]**, and a server that honours them refuses what the token does not allow. For example, a deployer registers `memory-ro` with `scopes: [memory.read]` alongside a read-write entry. Lenny does not verify that a server enforces scopes, and each entry needs its own link.

## CA-9: Stdio bridge

Connector and platform servers use abstract Unix sockets and a `_lennyNonce` field in `initialize` **[source]**. Claude Code documents stdio, HTTP, SSE, and WebSocket, and Codex documents stdio and HTTP, and neither states Unix socket support **[documented]**.

`lenny-mcp-stdio --connector <id>` or `--platform` is a small static binary. It reads `/run/lenny/adapter-manifest.json`, dials the named socket, adds the nonce to `initialize`, answers `adapterChallenge` in nonce-only mode, and copies JSON-RPC in both directions, notifications included. `cmd/git-credential-lenny` is the precedent for the handshake, although it does not answer the challenge.

### Delivery

The platform delivers the bridge to every sidecar `type: agent` pod, whatever the runtime image contains. It follows the runtime supervisor's pattern in §4.7.10 **Runtime supervisor.** and §13.1 **Runtime supervisor volume.** **[source]**:

| Element | Value **[design]** |
| :-- | :-- |
| Image | `lenny-tools`, digest-pinned, built `FROM scratch` with `CGO_ENABLED=0`, so the binary runs on any libc. |
| Init container | `lenny-tools-stage` copies the binary into an `emptyDir` volume named `lenny-tools`. It runs at a UID that is neither the agent nor the adapter UID. |
| Mount | The `runtime` container mounts the volume read-only at `/lenny-tools`, and the binary is `/lenny-tools/lenny-mcp-stdio`. |
| Enforcement | The `lenny-pod-security` webhook refuses a read-write mount of the volume by any container other than `lenny-tools-stage`. |

- **Why the platform delivers it.** The bridge speaks a Lenny-private handshake, so shipping it with the platform keeps it in step with the adapter, and a custom image cannot carry an old copy. A runtime image needs no change.
- **Why every pod.** One rule has no setting to forget. A runtime opt-in would save the init container on SDK-based runtimes, which handle the handshake themselves, and a missed opt-in would stop a stock CLI from connecting at startup.
- **Cost.** Each pod gains one init container, which adds about one second to pod startup, as "Startup measurement" states, and the binary goes unused on SDK-based runtimes. A warm start does not pay this, because the pod is prepared before a session claims it. Pool refill pays it, and so does a session that arrives when the pool is empty.
- **A separate init container.** The owner chose this over copying the bridge in `lenny-supervisor-stage`, which would likely avoid the added second. The separate container leaves the supervisor's stage step and single-binary image untouched, at the cost of the added second, a new image, a volume, and a webhook rule.
- **Security.** The read-only mount protects the binary's integrity. It adds no access, since an agent can already read the manifest and dial the socket.
- **Embedded mode.** An embedded runtime has no runtime container and receives no bridge, so it handles the handshake itself.

### Startup measurement

A throwaway measurement ran on 2026-11-03 on one arm64 Kind node (v1.31.4) under Docker Desktop, with the default container runtime and images already on the node. Stand-in pods held a runtime and an adapter container running `busybox`. Each single-pod arm ran 30 times in shuffled rounds, timed from pod creation to `Ready` **[measured]**.

| Pod | Mean to `Ready` | Paired difference from the baseline |
| :-- | :-- | :-- |
| No init container | 0.98 s | -0.98 s |
| One stage init container, standing in for the supervisor (baseline) | 1.96 s | n/a |
| Identical control | 1.97 s | +0.003 s |
| Baseline plus an init container running `true` | 2.97 s | +1.01 s |
| Baseline plus `lenny-tools-stage`, copying the 2.8 MB binary | 2.97 s | +1.01 s |

Twenty pods created at once took 4.73 s with the baseline and 5.81 s with `lenny-tools-stage`, over 8 rounds each, so the delay does not accumulate across pods. The `lenny-tools-stage` and `true` arms differ by 0.002 s, so the cost comes from having another init container and the copy adds almost nothing. The step of about one second per init container may come from the kubelet's container status polling, which was not confirmed.

The same run confirmed delivery on the node: the bridge executed from the read-only mount in a `busybox` image, and a write to the mount was refused. The image is 3.99 MB. The measurement does not cover its first pull, gVisor, Kata, a production cluster, or a pod rendered by the Lenny controller, so the share of a real pod's startup is unknown.

### CLI configuration

The bridge is universal, and CLI configuration is not. Each runtime's wrapper generates its own CLI's MCP configuration from the manifest before launch, using the absolute path above, because the formats differ. The binary's presence does not grant access: a connector is reachable only when the manifest lists it.

§15.4 says "There is no stdio transport for intra-pod MCP". The sentence stays true of the adapter's servers, and it gains a note that a runtime may host a stdio process of its own that dials them. `CH-MCP-CONNECTOR` is unchanged.

This ships with the Claude Code and Codex integrations, which need it for platform tools regardless.

### Spike results for Claude Code

A throwaway spike ran on 2026-11-03 with Claude Code 2.1.317 in headless mode (`claude -p`, model Haiku, each scenario once). The bridge was 144 lines of Go using only the standard library. Scenarios marked "real server" used the repository's `pkg/adapter/mcp` server. The others used a stub speaking the same wire, because the real server cannot send `list_changed` or a typed error yet.

| Scenario | Server | Result **[measured]** |
| :-- | :-- | :-- |
| Normal use, manifest nonce | Real | The tool was called and its result returned. The client offered protocol `2025-11-25` and accepted the server's `2025-03-26`. |
| Normal use, nonce-only challenge | Real | Same result. The bridge answered `_lennyChallenge` within the 500 ms limit. |
| Wrong nonce (bridge only, no CLI) | Real | The adapter closed the connection, and the bridge exited with an error. |
| `tools/list` always fails with `-32603`, as it does today | Real | The server stayed `connected` with no tools. The client retried 3 times over about 2 seconds, and the session continued. |
| `tools/list` always fails with `-32010` | Stub | Same behaviour. The model did not see the error text. |
| `tools/list` fails, then `list_changed` after 8 seconds | Stub | The client re-listed at once on the same connection, well after its retries ended, and the tool became callable in the running session. |
| Empty `tools/list`, then `list_changed` | Stub | Same recovery. |
| `tools/call` returns `isError`, then succeeds | Stub | The model read the text verbatim, retried, and succeeded. |
| The bridge cannot reach the socket | None | The server was `failed`, and the session continued. The model quoted `memory (CONNECTION_CLOSED): "Connection closed"`. Retries by the client were not measured. |
| Tool named `lenny/delegate_task` | Stub | Claude Code exposed `mcp__memory__lenny_delegate_task`, and it sent the original name on the call. |

Findings for the bridge:

- It must tolerate a connection that ends before `initialize`. Claude Code started one connection and closed it with nothing sent, immediately before the working one, so the adapter would log a failed read on each start.
- A slash in a platform tool name is not a problem under Claude Code.

These results do not cover abstract sockets, `SO_PEERCRED`, a real pod, `preConnect`, or the gateway path. The spike ran on macOS with filesystem sockets. Other CLI versions may differ, which is why the conformance suite reruns on each release.

No Codex result is recorded. The CLI was not logged in for model turns, and the owner decided those runs cannot be trusted.

## CA-10: Removing built-in memory

**Removed.** The inventory below comes from a search for the feature's own identifiers. It excludes unrelated `NewMemory` stores and Google Cloud Memorystore tests, which a broader search also matches.

| Area | Items |
| :-- | :-- |
| Store | `pkg/gateway/session/memorystore`, including `pgstore`, `memorystoretest`, `preflight.go`, and `observer.go`. |
| Tools | `registerMemoryTools` and its schemas in `mcptools`. |
| Routes | `sessionserver/memory.go` and the `/v1/sessions/{id}/memory` routes. |
| Gateway | `--memory-enabled`, `--memory-max-per-user`, the record-count sampler, and wiring in `wiring_fields.go`, `session_messaging.go`, `metricsbackfill.go`, `workers.go`, `config_parse.go`, and `adminrouter.go`. |
| Erasure | The memory step in `erasure/order.go` and `contract.go`, and `ValidateMemoryStoreErasure`. |
| Observability | `lenny_memory_store_*`, `MemoryStoreGrowthHigh`, `MemoryStoreErasureDurationHigh`, `pkg/alerting/rules`, `docs/alerting/rules.yaml`, and the chart's `alerting-rules.yaml`. |
| SDKs | `MemoryWrite` and `MemoryQuery` in the Go, Python, and TypeScript runtime SDKs. |
| Chart | Memory values and `gateway-deployment.yaml` flags, with the chart tests and `pkg/embedded/manifests/manifests.yaml`. |
| Schema | A new migration dropping `agent_memory`. Migrations 0032 and 0044 stay as history. |
| Tests | `tests/tier2_component/stores/memory*`, and memory cases in the contract and security tiers. |

**The embedder.** `semanticcache` imports `memorystore` for `Embedder`, `HashingEmbedder`, and `CosineDistance` **[source]**. If the cache is removed first or together, that code goes too. Otherwise it moves into `semanticcache`.

**pgvector.** The `vector` extension requirement goes when no other user remains. Some database tests name the pgvector image only as their Postgres container, so a proposal verifies this and changes `tests/testinfra/containers/postgres.go` if appropriate **[pending]**.

**Data.** Nothing migrates, since there are no deployments.

## External memory pattern

**Project knowledge.** Keep it in the repository, in files such as `CLAUDE.md` or `AGENTS.md`, and change it through pull requests. It is reviewed, it follows repository permissions, and it needs no Lenny mechanism. This is the first choice for coding workloads.

**User-level memory.** A deployer runs any MCP memory server and registers it.

```yaml
connectors:
  - id: memory
    displayName: Memory
    mcpServerUrl: https://memory.acme.com/mcp
    transport: streamable_http
    visibility: tenant
    auth:
      type: oauth2
      authorizationEndpoint: https://idp.acme.com/authorize
      tokenEndpoint: https://idp.acme.com/token
      clientId: lenny
      clientSecretRef: lenny-system/memory-client-secret
      expectedDomain: idp.acme.com
      scopes: [memory.read, memory.write]
```

The server scopes data by the token's subject, because Lenny forwards the user's token and adds no user identity of its own. Carol then grants `memory` through a delegation policy.

**Worked example.**

1. Alice creates a Claude Code session. The runtime is `false`, she has no link, and the probe returns 401, so she receives `403 CONNECTOR_LINK_REQUIRED` with a `linkUrl`.
2. She opens the URL, logs in, approves, and the token is stored for `acme`, `memory`, Alice, and the session's Environment.
3. She retries. The check reads `linked`, a pod is claimed, the manifest lists `memory`, the wrapper writes the CLI's MCP configuration, and `tools/list` returns the server's own tools.
4. Hours later the token nears expiry, and the next call refreshes it.
5. Her pod fails. The retry driver restores the session, and the manifest lists `memory` again.
6. The agent delegates to a reviewer runtime with `memory` granted. The child uses Alice's link in the same Environment.

Bob's first session is refused the same way. He passes `deferConnectors: ["memory"]`, and his session runs without it and without further prompts.

**Erasure.** `DeleteByUser` removes the connector tokens through the existing `tokens` eraser **[source]**. Deleting the memory data is a deployer obligation, which the operator guide states. Before deleting a user's tokens, the erasure job reads the connectors they were linked to, and the receipt names them under a new `externalDataNotErased` field, so a receipt never implies the data was removed. A webhook can follow when a customer asks.

**Retention, residency, capacity, and retrieval quality.** These move to the deployer.

**Code exposure.** A memory connector is a channel from a session to an external store. Its traffic runs on the gateway and bypasses `netagent` and `lenny-egress`, and user-scoped memory can carry content from one repository into a session on another. Lenny cannot partition memory by repository.

- A grant is explicit (CP-5).
- `PreConnectorRequest` and `PostConnectorResponse` interceptors see each call, and each call produces a trace span. There is no per-call audit event **[source]**.
- The §10.6 filter can deny write tools, and OAuth scopes can restrict them at the server.
- "Instructions for reviewers of other designs" asks `egress-and-git-design.md` to record this path.

## Limits

- A connector token is decrypted in the gateway (F-4.3.1). A compromised replica can use any connector token.
- **Impersonation is an accepted risk, by owner decision.** `requireCreatingBearerAllowed` stops an impersonation bearer from creating a session, and `handleMessages` still accepts one **[source]**. The invoker spends the session owner's token with no impersonation check. A platform admin impersonating Alice can therefore message her session, and its agent can write to her external memory as her. The external service records Alice as the actor, the write outlasts the session, and no per-call audit event names the impersonator. The operator guide states this. `user-credential-delivery-design.md` refused built-in memory writes for this reason, and that protection does not carry over to connectors.
- A shared-process pod with `maxConcurrentSessions` above one arms its connector sockets once, from the first session's set, and `callingSession` refuses a call when it cannot name one session **[source]**. Omission cannot be per session there. Offering a link-requiring connector on such a pool is not recommended, and no validator enforces this in this revision.
- `executionMode: service` has no link check. A connector call on it reaches "CA-5".
- A `preConnect: true` runtime starts before a session binds. A stock CLI's MCP configuration written at that point lists no connector for that session **[pending]**.
- The probe tells only whether listing needs auth. A server that lists different tools once authenticated gives a `false` runtime the authenticated list at its next start, since a linked user lists with their token.
- A stock client's rendering of a `tools/list` error or an omitted server is outside Lenny's control. Claude Code does not show the `tools/list` error to the model **[measured]**.
- A user in two Environments links twice, which §4.3 scoping implies and the owner accepted for the separation it gives. `user-credential-delivery-design.md` keys account links per user, under "Stored links" and its decision 30, so the two kinds of link behave differently.
- The capability snapshot does not notice a changed tool until a refresh, as "CA-8" states.

## Alternatives rejected

| Alternative | Reason |
| :-- | :-- |
| Serve a tool catalog stored at registration | The owner rejected it, since it is a copy that drifts. Triggering the prompt at `tools/list` removes the need. |
| A pool-level `required` flag | It answers a different question from the one that decides the failure, and `deferConnectors` covers the workload case. |
| A stored `requiresAuthToListTools` field | It drifts. The probe asks the server. |
| Pass the 401 through | See "CA-5". |
| `URLElicitationRequiredError` to the runtime | It places the URL in the pod and needs a newer protocol version. |
| An empty `tools/list` instead of an error | It hides the failure from the runtime, which is the opposite of the owner's decision. |
| Block `tools/list` until the user finishes | It exceeds client startup timeouts. |
| Refuse restarts with a missing link | It turns recoverable failures into session and tree failures. |
| Omit by default on delegation | See "CA-7". |
| Use `/v1/user-links` tokens for connectors | `user-git-identity-design.md` withdrew `hostLink`, since it moves a link's token onto the gateway path. |
| No link check on an explicit resume | The process would start without the connector, and a `false` runtime's connector list is fixed at start, so a user who then links waits for another restart. |
| Refuse impersonated messages on sessions with a linked connector | The owner accepted the risk instead, since this blocks support work on those sessions. |
| Block connector calls after impersonated input | The owner accepted the risk instead, and this needs new state carried through delegation. |
| Key connector tokens per user | The owner kept the Environment key. It can be loosened later more easily than it could be added. |
| Read tool annotations live for the §10.6 filter | The owner kept the snapshot and asked for no other change. |
| Push a message to a parent when a link completes | See "CA-7". |
| Keep memory in the gateway behind a flag | It keeps the erasure and isolation obligations for every deployment that may enable it. |
| Ship a reference memory server | It adds a component to maintain, with no customer asking. |
| Put the bridge in each runtime image | It reaches only images Lenny builds, and a custom image can carry a copy older than the adapter. |
| Copy the bridge in `lenny-supervisor-stage` | The owner kept a separate init container, which leaves the supervisor's security-sensitive stage step and single-binary image unchanged. It would likely have avoided the added second. |
| Deliver the bridge only to runtimes that opt in | A missed opt-in stops a stock CLI from connecting at startup, and it adds a setting to save one init container. |
| A local HTTP listener in the adapter | It opens a TCP port, loses the `SO_PEERCRED` check, and changes every pod's listener surface. |

## Instructions for reviewers of other designs

The owner instructed that this document edit no other design. These items are for a reviewer agent working on the named design, which applies them there. Other work is editing these designs on this branch, so locate each item by heading and quoted text, check that it still holds against the current text, and report any item that no longer fits rather than forcing it.

### `user-credential-delivery-design.md`

**Owner decision: the OAuth flow is a shared proposal, "CA-0", which lands before UC-2.** Apply these changes:

1. **"UC-2: The `oauth2Link` source and account linking", "Flow".** Replace the mechanics in steps 2 to 5 with a reference to "CA-0" in this document: the one-time flow identifier, the OIDC login and subject check, the cookie nonce, the signed `state`, and callback validation. Keep what is specific to UC-2: step 1, the token trust record display and digest comparison, the per-provider callback path, and steps 6 and 7, including the hand-off to the Token Service, identity binding, `allowedAccounts`, and the confirmation page. Check that the "CA-0" hook carries everything those steps need, and report any gap back to this document.
2. **"Dependencies".** Add "CA-0" to the UC-2 row, with the reason that the flow's mechanics come from it.
3. **"Reuse of §9.3".** This list explains why account links do not reuse §9.3, and its conclusion stands because of `ConnectorDefinition` requiring an MCP server, the per-person link, and F-4.3.1. Three bullets stop being true once this design lands, so restate them as today's state or remove them from the reasons:
   - "The §9.3 flow runs from a tool call inside a session": "CA-A" adds linking before a session.
   - "Connector refresh has no caller, and no store has a refresh lease": "CA-B" adds both.
   - "The callback is open to the account-link attack": "CA-0" closes it.
4. **"Defects found while verifying".**
   - "Account linking in §9.3": state that "CA-0" closes it.
   - "Refresh absent": state that "CA-B" closes it.
   - "Session input under impersonation": remove `handleMemoryWrite` and `lenny/memory_write`, which "CA-G" deletes.
5. **"Refresh".** No change to UC-2's behaviour. Note that "CA-B" adopts the same lease form for connector credentials, so a later change to that form considers both.
6. **Session create.** A session can need an account link and a connector link at once, and both designs refuse before any pod claim with a `403`: `USER_LINK_REQUIRED` and `CONNECTOR_LINK_REQUIRED`. Agree with this document on one response that reports every missing link, so a user does not link, retry, and get refused again. Both checks must also run after experiment routing, as "CA-4" states under "Placement".

Leave the link record in the Token Service unchanged, under "Stored links".

**Owner decision: built-in memory goes, and impersonation on linked connectors is an accepted risk.** Apply these changes:

1. **"Impersonation".** Remove the "Memory, unconditionally" bullet with no replacement. Do not extend the session input check to sessions holding a connector link.
2. **"Specification sections affected".** Remove "§9.4: memory writes refuse an impersonation bearer."
3. **"Tests", "Tier 4: integration".** Remove `lenny/memory_write` from the impersonation bearer test.
4. **"Current state".** The row "Other session input refuses an impersonation bearer" names memory writes as a fact about today's tree. Keep it until "CA-G" lands.

### `user-git-identity-design.md`

**Owner decision: "CA-0" lands before UC-2, on which UG-1 depends.** Apply this change:

1. **"When the check runs".** The repository check refuses a session with `USER_LINK_REQUIRED` before any pod claim. That refusal joins the one response that the **Session create** item under `user-credential-delivery-design.md` asks for, and the check must also run after experiment routing, as "CA-4" states under "Placement".

Leave these unchanged: decision 3 on connectors on a `required` pool's hosts, and the withdrawal of `hostLink`, both under "User tokens on a VCS host".

### `egress-and-git-design.md`

**"Residual risks and limits" and "Exfiltration controls".** Record that a §9.3 connector tool call runs on the gateway, so `netagent`, `lenny-egress`, and the bytes-out alert do not see it, and that its arguments can carry source code to an external server. Default deny in "CA-3" and connector interceptors are the controls. If that design or a later one adopts a default for sessions holding private repository content, it decides whether connectors stay available there. This document does not settle that.

### `tenant-scoping-identity-design.md`

No edit. "CA-3" depends on "Delegated child's Environment", and on a derived session following its source's Environment. If decision 14 settles on the cross-Environment alternative, or if either rule changes, report it to this document, because a child's token lookup reads that field.

### Other designs

`tenant-outbound-connector-design.md`, `security-oversight-design.md`, and `forensics-evidence-preservation-design.md` need no change. The first has `§9.3` reader-page work of its own, and a connector's probe uses the same dial path as its other calls.

## Specification changes

Each lands through `change-proposal`.

| Section | Change |
| :-- | :-- |
| §5.1 Runtime | Add `capabilities.deferredToolDiscovery`. |
| §7.1 Normal Flow | Add the link check after step 3a. Rewrite **Connector state.** |
| §7.2 and §7.3 | Add the explicit resume check and implicit restart rule. |
| §8.2, §8.3, and §8.5 | Add the delegation check, deferral inheritance, and the child's Environment. Remove the memory tools and their schemas. |
| §9.1 | Remove the memory tools. Add the bridge note. |
| §9.2 | State connector prompts: gateway origin, interceptor skip, deduplication, lifetime, and URL domain rule. |
| §9.3 | Replace the OAuth flow, add refresh and endpoint binding, and state default deny and the typed error. |
| §9.4 Memory Store | Replace with a successor pointer to the external pattern, naming the identifiers that left. |
| §4 System Components | Remove memory from the tenant-scoped table, and from local MCP server text if named. In §4.7.10 Deployment Model, state the `lenny-tools` delivery on sidecar pods and its absence in the embedded model. |
| §13.1 Pod Security | Add a `lenny-tools` volume rule beside **Runtime supervisor volume.**, including the stage container's UID, the read-only mount, and the `lenny-pod-security` enforcement. |
| §17 Deployment Topology | Add the digest-pinned `lenny-tools` image to the images an install renders, following how the chart names the supervisor image. |
| §12 Storage Architecture, including §12.8 | Remove the role, the erasure preflight and guidance, and the erasure steps. Add `externalDataNotErased`. |
| §15.1 and §15.4 | Add the link routes, `CONNECTOR_LINK_REQUIRED` with `details.retryAfterSeconds` on delegation, `deferConnectors`, and `unlinkedConnectors`. Add the `-32010` error, the compliance check, and the stdio note. Remove memory helpers. |
| §16.1, §16.5, and §16.7 | Remove memory metrics and alerts. Add audit events `connector_link.created`, `connector_link.removed`, and `connector_link.endpoint_changed`, and the event `session.connector_unavailable`. |
| §18 Build Sequence | Remove memory from Phase 13 and Phase 17b, and place the connector work. |
| §22.4 | Rewrite, since Lenny no longer provides the interface. |
| §23 | Remove the memory claims. |
| §28.5.3 | Remove the memory tools from the `CH-MCP-PLATFORM` card. Update the `CH-MCP-CONNECTOR` card and add any new RPC placement. |

Documentation follows: `docs/tutorials/agent-memory.md` becomes the external pattern, `docs/tutorials/using-connectors.md` gets the real flow, and `docs/runtime-author-guide/platform-tools.md`, `integration-levels.md`, the operator guide, the glossary, `docs/reference/metrics.md`, and `TESTING.md` §13.36 lose the memory content.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Link check table, including probe cache expiry and each status. `WalkChain` skips interception for connectors. `AuthorizeConnector` default deny. Endpoint digest mismatch. Error mapping at each layer. Deferral inheritance. |
| 3 | `CONNECTOR_LINK_REQUIRED` over REST and MCP. `-32010` on `tools/list`, and `isError` on `tools/call`. |
| 4 | Each row of the "CA-6" table. Create, link, retry, then call. Refresh on expiry. `invalid_grant` unlinks. Delegation refusal with `retryAfterSeconds`, retry, and deferral, with the export undone and budget returned. Child in the parent's Environment finds the link. Derived session reuses a link. The stub server moves from 401 to 200 within the cache window. |
| 5 | A pod on Kind has `/lenny-tools/lenny-mcp-stdio` on a read-only mount in a minimal runtime image and runs it there. The webhook refuses a read-write mount of the volume. An embedded pod has no such volume. |
| 7a | Concurrent refresh yields one provider call per credential, extending `connector_oauth_refresh_race` to the real store. Prompt deduplication across replicas. |
| 8 | Probe timeout on create and on resume. Token endpoint outage during refresh. Callback landing on a replica that does not hold the session. |
| 9 | Link URL sent to another subject is refused, through both the converted §9.3 handlers in "CA-0" and the new routes. Missing cookie is refused. Impersonation bearer is refused. No URL or token appears in a pod, in a tool result, or at an intercepting parent. A child with a denied capability stays denied. |
| 10 | The `deferredToolDiscovery` check, and the bridge's handshake in both nonce modes. |
| 11 | Removed metrics and alerts leave no runbook or doc reference. |

Each test carries its `// spec:` annotation once the specification lands.

## Validation owed

| Item | Blocks | How |
| :-- | :-- | :-- |
| Codex behaviour on a failed `tools/list` and `list_changed` | A `true` claim for Codex. The `false` path needs no answer. | Run a current, logged-in Codex headless, through the bridge, against a stub that fails `tools/list` and then notifies. Claude Code 2.1.317 is measured under "CA-9". |
| Bridge transport in a pod, with abstract sockets and `SO_PEERCRED` | "CA-9" | Round trip in the conformance suite, for each CLI. Claude Code passed on macOS with filesystem sockets. |
| Generated MCP configuration under `preConnect` | Stock CLI on warm pools | Launch a warm pod and bind a session. |
| Added startup from `lenny-tools-stage` on a real pod | Sizing warm-pool refill. About one second is measured on stand-in pods under "CA-9". | Measure a controller-rendered pod with and without the init container, under gVisor and Kata, including the image's first pull. |
| Interaction store lookup by tenant, user, connector, and Environment | "CA-1" step 6 | Read `interactionstore` at proposal time. A new index may be needed. |
| OIDC availability on the start page in `apiKey` and dev modes | "CA-0" | Settle it in that proposal, since the start page is shared with UC-2. |
| MCP resource indicator requirement | "CA-2" | Read the MCP authorization page, and test a server that enforces audience. |

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| CA-0 | Shared OAuth link flow in `pkg/connectoroauth`, with today's §9.3 handlers converted to it. Closes the account-link defect. | Nothing. It lands first, and UC-2 depends on it. |
| CA-A | Connector routes (CA-1), and the removal of the admin OAuth routes. | CA-0 |
| CA-B | Refresh, lease, and endpoint binding (CA-2). | CA-A |
| CA-C | Default deny and Environment on children and derived sessions (CA-3). | The tenant-scoping decision |
| CA-D | Typed error and connector prompts (CA-5). | CA-A |
| CA-E | Link check at create, derive, replay, and external adapters, plus `deferConnectors` (CA-4). | CA-A, CA-C |
| CA-F | Resume and restart rules (CA-6), and delegation (CA-7). | CA-D, CA-E |
| CA-G | Memory removal (CA-10) and the external pattern documentation. | CA-A through CA-F, so the documented replacement works when memory goes |
| CA-H | Stdio bridge, the `lenny-tools` image, init container, volume, and webhook rule (CA-9). | Ships with the Claude Code and Codex integrations |
| CA-I | `deferredToolDiscovery` true path and `list_changed` (CA-8). The field and its `false` default land in CA-E. | The stock CLI spike, or a runtime asking for it |

`user-credential-delivery-design.md` rates the account-link defect High under "Defects found while verifying" wherever a connector is a confidential client, which is why CA-0 comes first. CA-C's default deny and the child Environment fix close a capability-filter bypass that exists today, and they also stand alone.

## Sources

- MCP specification 2025-11-25: [Authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization), [Elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation), and [Tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).
- [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp).
- [Codex MCP documentation](https://developers.openai.com/codex/mcp), which redirected on 2026-11-03 to `learn.chatgpt.com/docs/extend/mcp`. Most Codex facts here came from a summary of that page.
