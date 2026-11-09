# Connector authorization and external memory

Status: design for owner review, revision 12, 2026-11-09, with no independent review yet. It reads against [`runtime-routing-design.md`](runtime-routing-design.md) revision 9, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 5, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 26, [`user-git-identity-design.md`](user-git-identity-design.md) revision 34, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) revision 28, and [`egress-and-git-design.md`](egress-and-git-design.md) revision 74. The owner settled the decisions under "Owner decisions". Nothing here is built, and nothing in `spec/` changes until a proposal lands it through the change-proposal pipeline, in which a proposal under `proposals/` stages the specification edits, a human approves it, and the edits land before the code that depends on them. The parts of the design carry the labels CA-0 to CA-10, and the proposals that land them carry CA-0 and CA-A to CA-I, under "Proposal split and order". Requirements carry the label CAR-n and security properties CP-n. "Review history" at the end records how the design reached this form.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A test run on 2026-11-03 against the real package under test, through a Go build overlay with no change to the tree, with a stub as the external MCP server. Also the Claude Code spike and the startup measurement under "CA-9", whose conditions that section states. |
| **[source]** | Read in the Lenny tree or the specification. |
| **[documented]** | Stated on a vendor or MCP specification page under "Sources", read and not tested. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[design]** | What this design requires. Nothing implements it. |

**Evidence sources.** A *test run* is one of the runs of 2026-11-03 that exercised the connector code in the Lenny tree. A Go build overlay, a build option that substitutes source files at compile time, added the test code without changing the tree. A *stub* is a small test server that stands in for an external MCP server and answers as each test needs, for example with a 401. A *spike* is a throwaway hands-on test: the Claude Code spike is recorded under "Spike results for Claude Code". The *startup measurement* is the pod startup timing under "Startup measurement". Elsewhere in this document, the word *probe* means the anonymous tool listing that "CA-4" designs, never a test run.

## Problem

Lenny carries a built-in memory feature: the `MemoryStore` role, one of the store interfaces of [§12.2 Storage Roles](../spec/12_storage-architecture.md#122-storage-roles), a Postgres and pgvector backend (pgvector is the Postgres extension for vector similarity search), the `lenny/memory_write` and `lenny/memory_query` platform tools, and `/v1/sessions/{id}/memory` routes. It makes Lenny responsible for retrieval quality, embeddings, capacity, retention, tenant isolation, and mandatory erasure of a data type unrelated to running agent sessions. Its default embedder, `HashingEmbedder`, measures token overlap, and dedicated memory products exist.

Sessions still need a way to reach state that outlives a pod. MCP connectors are the generic mechanism for this, and a deployer can register any MCP memory server as one. "Terms from other designs and the platform" below defines connectors and the other platform terms this section uses. That route does not work today, and the reasons are general to every connector that requires per-user authorization:

- An unlinked user's call fails with `unexpected status 401` and no prompt, although [§9.3](../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) and `docs/tutorials/using-connectors.md` both describe a prompt **[measured]**.
- An expired token is sent as it is and never refreshed **[measured]**.
- A session with no delegation policy is offered every active connector in the tenant **[source]**.
- A stock coding CLI cannot dial the adapter's connector sockets **[source]**.
- The OAuth callback is open to an account-link attack, recorded in [`user-credential-delivery-design.md`, "Defects found while verifying"](user-credential-delivery-design.md#defects-found-while-verifying) **[source]**.
- A delegated child loses its parent's environment, so a token linked under that environment is not found **[measured]**.

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
- Service-identity authorization to connectors, in which a caller acting for no human user would hold a connector token of its own. An automated caller defers the connector in this revision.
- A reference memory server. The owner may add one when a customer asks.
- Moving connector tokens out of the gateway (F-4.3.1).

[`user-credential-delivery-design.md`](user-credential-delivery-design.md) owns account links and tokens delivered to pods, and [`user-git-identity-design.md`](user-git-identity-design.md) configures them for repository hosts. A connector token stays on the gateway path, and its use by the gateway remains the one way it is spent. "§9.3" means [§9.3 Connector Definition and OAuth/OIDC](../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) throughout.

## Terms

| Term | Meaning |
| :-- | :-- |
| Connector link | A stored connector token for one tenant, connector, user, and environment. The gateway holds it as a row in its connector credential store, `connectorcredstore`. |
| Linked user | A user who holds a usable connector link for the session's environment. |
| Unlinked user | A user who holds no usable connector link for that connector and environment, even though the connector is registered and permitted. |
| Shared OAuth link flow | The hardened browser flow that links a person's account at an OAuth2 provider. "CA-0" moves it into `pkg/connectoroauth`, where connectors and the account links of [`user-credential-delivery-design.md`](user-credential-delivery-design.md) both use it. |
| Link URL | A Lenny URL that starts the shared OAuth link flow for one subject, connector, and environment. The user opens it in a browser. See "CA-1". |
| Endpoint digest | A digest of a connector's `mcpServerUrl`, `authorizationEndpoint`, and `tokenEndpoint`, recorded on each connector link. A link whose digest no longer matches is treated as absent. See "CA-2". |
| Probe | The call the gateway makes to a connector's server with no token, a `tools/list` with an empty bearer, to learn whether listing tools needs authorization. See "CA-4". |
| Link check | The evaluation in "CA-4" that decides whether a connector needs a link before a runtime starts. |
| Combined missing-links response | The single refusal at session creation, `403 LINKS_REQUIRED`, that reports every connector link and account link the user still needs, which "CA-4" defines under "Combined missing-links response" and [`user-credential-delivery-design.md`](user-credential-delivery-design.md#flow) shares. |
| Deferred connector | A connector the caller chose to run without for a session and its descendants. The request field `deferConnectors` names it. |
| `deferredToolDiscovery` | The runtime capability `capabilities.deferredToolDiscovery`, which "CA-8" adds to the Runtime resource of §5.1. A runtime with `deferredToolDiscovery: true` keeps a connector server whose `tools/list` failed and lists again when the server says its tools changed. The default is `false`. |
| Explicit resume | `POST /v1/sessions/{id}/resume`, or the `lenny/resume_session` tool, which posts to the same route. |
| Implicit restart | A new runtime process with no client request waiting on it: the retry driver, message-triggered restoration of a released pod, a credential-rotation restart, and tree recovery. |
| Connector prompt | A URL-mode elicitation with `initiator_type: connector`, raised by the gateway. |
| Stdio bridge | `lenny-mcp-stdio`, a small binary that a CLI starts as a stdio MCP server, and which relays to one of the adapter's socket servers. See "CA-9". |
| `lenny-tools` | The image, init container (`lenny-tools-stage`), and volume that deliver the stdio bridge to each pod. See "Delivery" under "CA-9". |
| Built-in memory | The memory feature Lenny carries today under §9.4 Memory Store, which "CA-10" removes. |
| Account-link attack | The defect in today's §9.3 OAuth handlers that [`user-credential-delivery-design.md`, "Defects found while verifying"](user-credential-delivery-design.md#defects-found-while-verifying) records as F-9.3.18. The gateway returns an authorization URL to any authenticated caller, and the callback checks no bearer and no cookie, so an attacker who sends the URL to a victim receives the victim's token under the attacker's own user. "CA-0" closes it. |
| Subject | The Lenny user a link flow is for. The flow names the subject, the start page requires the logged-in user to be that subject, and the token is stored under it. A token's own `sub` claim, which names the account at the provider, is a different thing. |
| Pool-level `required` flag | A flag, proposed during the design discussion, that would have marked a connector as required on a pool. The owner replaced it with a live check of whether a connector requires authorization to list its tools. The specification has no such field. It is unrelated to a VCS pool with `userAuthorization: required`. |
| User erasure | The `DeleteByUser` erasure job of [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), which calls each store's `DeleteByUser` and records an erasure receipt in the audit trail. The `tokens` eraser is the step that deletes a user's stored tokens, connector tokens included. "External memory pattern" adds the receipt field `externalDataNotErased`. |
| Lease | A word with three senses here, each qualified where it appears. A refresh lease is a lease on the connector credential row (`refresh_lease_holder` and `refresh_lease_until`) that lets one gateway replica refresh a token at a time. A delegation lease records what a child session receives from its parent. A §8.6 lease extension asks the human to extend a session's budget, and its prompt (`leaseelicit`) is blocking, unlike a connector prompt. The gateway package `leasecontrol` hosts both the lease extension and the gateway's connector tool service for the adapter. |

### Terms from other designs and the platform

This design builds on mechanisms that other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| MCP | The Model Context Protocol, a JSON-RPC protocol between a client, here an agent runtime, and a server that offers tools. `initialize` opens a connection, `tools/list` returns the server's tools, `tools/call` calls one, and `notifications/tools/list_changed` tells the client that the list changed. |
| Connector | An external MCP server that a deployer registers as a `ConnectorDefinition` ([§9.3](../spec/09_mcp-integration.md#connectordefinition-as-first-class-resource)). Its optional `auth` block names the OAuth2 endpoints and client the gateway uses to obtain a user's token. The gateway makes every call to the server on a session's behalf, through code this document calls the invoker (`invoker.go`). The invoker's function `bearerFor` looks up the user's token for each call. |
| OAuth2, OIDC, and PKCE | OAuth2 is the protocol through which a provider issues a user's access token and refresh token to a client. OpenID Connect (OIDC) is the login layer on OAuth2 that Lenny uses to sign a user in. Proof Key for Code Exchange (PKCE) binds an authorization code to the client that started the flow. |
| Environment | A tenant's named project context ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)), with members, selectors for the runtimes and connectors it offers, and a default delegation policy. A session records the environment it runs in. |
| Delegation policy | A `DelegationPolicy` resource ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)) that lists what a session may delegate to or reach, including targets of type `connector`. A policy attaches to a runtime or serves as an environment's default. `AuthorizeConnector` checks a connector against two policy layers: the runtime-level policy and the environment's default policy. A layer resolves when its reference names an active policy, and a connector is permitted only when every resolved layer permits it. |
| Capability filter and capability snapshot | An environment's `connectorSelector` ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)) allows or denies connector tools by capability, such as `read` or `write`. This document calls it the §10.6 filter. The capabilities come from a snapshot, `conn.ToolCapabilities`, that §5.1 infers from a server's MCP tool annotations at registration and on refresh. |
| Runtime, `type: agent`, and pool | A runtime ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)) is a registered agent program, and a `type: agent` runtime runs agent sessions. A pool keeps warm pods for a runtime, ready for a session to claim. |
| Integration level | How much of the adapter protocol a runtime implements: Basic, Standard, or Full ([§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels)). |
| Runtime adapter and adapter manifest | The adapter ([§4.7](../spec/04_system-components.md#47-runtime-adapter)) is Lenny's process in each agent pod. It talks to the gateway and serves MCP to the runtime. It writes the adapter manifest, `/run/lenny/adapter-manifest.json`, which lists the MCP servers the runtime may use and a nonce the runtime presents in `initialize`. |
| Adapter authentication modes | The adapter authenticates each client on its sockets in one of two modes. By default it checks the connecting process's user ID through `SO_PEERCRED`, the kernel's report of a Unix socket peer's credentials, and requires the manifest nonce. In nonce-only mode the nonce alone authenticates, and the adapter adds a per-connection challenge, `adapterChallenge`, that the client must answer. A runtime with `requireSoPeercred: false`, on a pool that sets `acknowledgeNonceOnlyAuth`, selects nonce-only mode, for an isolation environment in which the adapter's `SO_PEERCRED` self-test fails, which §4.7 expects for gVisor only if validation confirms a divergence ([§4.7](../spec/04_system-components.md#47-runtime-adapter), [§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). |
| Connector socket and platform server | The adapter serves each connector on its own abstract Unix socket, `@lenny-connector-<id>`, and serves Lenny's platform tools, such as `lenny/delegate_task`, on a platform server. The adapter forwards each call to the gateway. The specification's channel identifiers for these conversations are `CH-MCP-CONNECTOR` and `CH-MCP-PLATFORM` ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `LNK-POD-GRPC` and `CH-HOLDERFORWARD` | `LNK-POD-GRPC` is the gRPC connection from a gateway replica to a pod's adapter. `CH-HOLDERFORWARD` forwards a request that reached one gateway replica to the session's coordinating replica ([§28.3](../spec/28_communication-channels.md#283-registers)). |
| Gateway replica | One instance of Lenny's API gateway. One replica coordinates each session under a lease. |
| Stock CLI and SDK-based runtime | A stock CLI is an agent command-line tool used as published, such as Claude Code (`claude`) or Codex, with no Lenny code inside it. An SDK-based runtime uses a Lenny runtime SDK, which speaks the adapter's handshake itself. |
| Runtime wrapper, and the Claude Code and Codex integrations | The reference runtimes `claude-code` and `codex` ([§26.3](../spec/26_reference-runtime-catalog.md#263-claude-code), [§26.5](../spec/26_reference-runtime-catalog.md#265-codex)) run the stock CLI in a Lenny pod. Each runtime's `command` is a wrapper, such as `lenny-claude-code-adapter`, that prepares the CLI's configuration and launches it. "The Claude Code and Codex integrations" means these runtimes. |
| Sidecar and embedded models | The adapter deployment models of [§4.7.10](../spec/04_system-components.md#4710-deployment-model). In the sidecar model, the default, the adapter runs in its own container beside a `runtime` container. In the embedded model, a first-party binary embeds the adapter and there is no runtime container. |
| Runtime supervisor | A platform binary that is the first process of the `runtime` container on every sidecar `type: agent` pod and launches the runtime's command. The init container `lenny-supervisor-stage` copies it onto a read-only mount ([§4.7.10](../spec/04_system-components.md#4710-deployment-model), [§13.1](../spec/13_security-model.md#131-pod-security)). |
| SDK-warm pods (`preConnect`) | A runtime with `capabilities.preConnect: true` has its agent process started in each warm pod before a session is bound to the pod ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). |
| Execution mode | A runtime's `executionMode`. `executionMode: session` gives each session its own runtime process. `executionMode: service` serves requests on a shared runtime with no per-session lifecycle ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Elicitation and URL mode | An MCP request in which a server asks the human for input. A URL-mode elicitation asks the user to open a URL, such as a login page. [§9.2](../spec/09_mcp-integration.md#92-elicitation-chain) passes an elicitation hop by hop up the delegation tree to the human-facing edge, the client. The interaction store (`interactionstore`) holds pending elicitations, and `maxElicitationWait`, 600 seconds by default and set per pool, bounds how long one stays pending. |
| Intercepting parent | A parent session configured to answer its children's elicitations itself rather than forward them to the human. `WalkChain` in `pkg/elicitation` stops at the first intercepting ancestor. |
| Interceptors | Deployer hooks the gateway calls at named phases ([§4.8](../spec/04_system-components.md#48-gateway-policy-engine)). `PreConnectorRequest` and `PostConnectorResponse` see each connector call and its response. `PreRoute` runs before a session's runtime and pool are final and can rewrite them. |
| `PreRoute` chain, revision, and post-chain point | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) runs the `PreRoute` interceptors as one chain on every creation path, `kind: create`, `kind: delegate`, and `kind: derive`, and a `MODIFY` from any of them can change the runtime and pool. [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-2-selection) then picks a revision of the final runtime: an immutable, numbered copy of the runtime's versioned fields, `capabilities` among them. The post-chain point ([RT-2](runtime-routing-design.md#post-chain-point)) follows, and every check that depends on the runtime, the revision, or the pool runs there on the chosen revision, before the pod claim. |
| Delegation | A session can start child sessions through `lenny/delegate_task`, and they form a delegation tree under a root session. A delegation lease records what a child receives from its parent. |
| Derive | Derive (`POST /v1/sessions/{id}/derive`) starts a new root session from an existing session, from its latest snapshot or from its stored plan, under [`session-derive-design.md`](session-derive-design.md). That design also removes the former replay route, `POST /v1/sessions/{id}/replay`, which it merges into derive. |
| Session states and restart paths | A `suspended` session has paused, either holding its pod or having released it. The retry driver restarts a session's runtime after a retryable failure, through `resume_pending` and `resuming`, and a session whose retries are exhausted waits in `awaiting_client_action` ([§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume)). Tree recovery restores the sessions of a delegation tree after a failure ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)). A credential-rotation restart restarts a runtime at the Basic and Standard integration levels so that it picks up a rotated credential ([§4.7.8](../spec/04_system-components.md#478-credential-rotation-by-integration-level)). |
| External protocol adapters | Gateway front ends that serve other client protocols, such as `OpenAICompletionsAdapter` at `/v1/chat/completions`, and create sessions on the client's behalf (§15). Each declares an `AdapterCapabilities` record, whose `supportsElicitation` says whether the adapter can show an elicitation to its client. |
| Token Service | The Lenny service that stores user and provider credentials and mints short-lived tokens from them ([§4.3](../spec/04_system-components.md#43-token-service)). Connector tokens are outside it: the gateway stores and decrypts them. |
| Account link | A person's stored authorization at an OAuth2 provider under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), kept in the Token Service and created through `/v1/user-links`. It is separate from a connector link. |
| Impersonation bearer | A token a `platform-admin` obtains through `/v1/admin/impersonation` to act as a user ([§13.3](../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). |
| `netagent` and `lenny-egress` | `netagent` is the egress proxy container that the egress design adds to an agent pod that uses the local egress endpoint, and `lenny-egress` is the central egress proxy through which agent traffic leaves the cluster. Both belong to [`egress-and-git-design.md`](egress-and-git-design.md). Connector calls run on the gateway and pass through neither. |
| `lenny-pod-security` | The admission webhook that refuses an agent pod spec breaking the pod rules of [§13.1](../spec/13_security-model.md#131-pod-security). |
| `lenny-compliance` | The runtime adapter conformance harness, which runs the battery of [§15.4.6](../spec/15_external-api-surface.md#1546-conformance-test-suite). |
| `lenny-ctl` | Lenny's command-line client ([§24](../spec/24_lenny-ctl-command-reference.md#24-lenny-ctl-command-reference)). |
| Semantic cache | An optional, per-pool cache of model responses keyed by embedding similarity ([§4.9, "Semantic Caching"](../spec/04_system-components.md#semantic-caching)). Its code, `semanticcache`, imports the memory store's embedder. |
| F-n.n.n | An identifier such as F-4.3.1 names a finding filed in `BUILD-GAPS.md` at the repository root, the audit of gaps between the specification and the code. F-4.3.1 is the finding that the gateway holds Key Management Service (KMS) decrypt rights in its own process, so it can decrypt stored tokens itself. |
| Kind, gVisor, and Kata | Kind runs a Kubernetes cluster in Docker containers for testing. gVisor and Kata are sandboxed container runtimes that Lenny's isolation profiles use. |
| Test tiers | The stages of the repository's test suite, which `lenny-test --tier` runs. The "Tests" table names the tiers this design uses. |

### Labels from other designs

This document cites proposals of other designs by label and links each citation to its definition.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| UC-2 | [`user-credential-delivery-design.md`](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) | The `oauth2Link` source and account linking, whose hardened link flow "CA-0" shares. |
| UG-1 and UG-2 | [`user-git-identity-design.md`](user-git-identity-design.md) | [UG-1](user-git-identity-design.md#ug-1-vcs-link-kinds) version control system (VCS) link kinds, the VCS layer's proposal that depends on UC-2, and [UG-2](user-git-identity-design.md#ug-2-repository-authorization) repository authorization, whose check refuses a user with no link. |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The interceptor contract: the single `PreRoute` chain and the post-chain point. |
| RV-1, RV-2, and RV-6 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | [RV-1](runtime-revisions-design.md#rv-1-revisions) the field classes, under which `capabilities` is versioned, [RV-2](runtime-revisions-design.md#rv-2-selection) revision selection at session creation, and [RV-6](runtime-revisions-design.md#rv-6-sessions-over-time) the revision a resumed or delegated session runs. |
| MP-3 and MP-5 | [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) | [MP-3](mcp-protocol-upgrade-design.md#mp-3-elicitation) elicitation, including the per-version forms of the combined missing-links response, and [MP-5](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers) the move of the adapter's intra-pod servers to the supported MCP versions. |

## High-level design

Lenny sessions can call tools on external MCP servers that a deployer registers as connectors. Many such servers need each user's own authorization, and that path does not work today: a user who has not linked an account gets an unexplained failure, tokens are never refreshed, a session with no policy can reach every connector, and stock agent command-line tools cannot reach connectors at all. Lenny also carries a built-in memory store, which makes it responsible for a data product unrelated to running agent sessions.

This design removes the built-in memory and makes connectors that need per-user authorization usable, so a deployer who wants memory registers an external memory server as a connector. A user links an account on a browser page, before or during a session, and the gateway keeps the token, refreshes it, and spends it only at the server it was linked for. Before a session starts, Lenny checks whether each permitted connector needs a link. It either refuses the session with a link to complete for each such connector, or starts the session and tells the agent's runtime when a connector needs authorization. The main limits are that connector traffic runs on the gateway, outside the pod egress controls, so a granted connector can carry a session's data out, and that a platform admin impersonating a user can spend that user's connector token.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Shared link flow and connector routes (CA-0, CA-1).** CA-0 moves the hardened OAuth flow of [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) into a shared package and converts today's §9.3 handlers to it, which closes the account-link attack. CA-1 adds the `/v1/connector-links` routes and `lenny-ctl connector link`, so a user links a connector for an environment before any session exists, from any client.

**Token lifecycle (CA-2).** The gateway refreshes a connector token near expiry under a lease on the connector credential row, deletes it when a refresh returns `invalid_grant`, the OAuth error that means the refresh token is no longer valid, and treats it as absent once the connector's endpoint digest changes.

**Access and environment (CA-3).** A session reaches a connector only when a delegation policy grants it. A delegated child carries its parent's environment, and a derived session its source's, so each finds the same links. A child whose delegation crosses a sharing path instead carries the target environment, and needs a link there.

**Link check (CA-4).** At session creation, at the post-chain point that follows the `PreRoute` chain and revision selection, the gateway evaluates each permitted connector as `open`, `linked`, `needs_link`, or `unknown`, and sends a probe only for a user with no token. A session whose chosen revision has `deferredToolDiscovery: false` is refused with `403 LINKS_REQUIRED` and a link URL for each connector that needs one, unless the caller defers that connector with `deferConnectors`. The same refusal reports the account links the user lacks, and it carries that code whichever kinds of link are missing.

**Failure propagation and connector prompts (CA-5).** An authorization failure at the external server reaches the runtime as a typed MCP error on `tools/list`, or as an `isError` result on `tools/call`. The gateway raises one connector prompt per delegation tree and connector to the human, with a Lenny link URL that no pod or agent sees.

**Resume, restart, and delegation (CA-6, CA-7).** An explicit resume runs the link check. An implicit restart never fails for a missing link: it leaves the connector out of the manifest and prompts the user. A delegation to a child that would need a link is refused with `details.retryAfterSeconds`, and the parent retries after the user links, or defers the connector.

**Late tools (CA-8).** A runtime that declares `deferredToolDiscovery: true` keeps a connector whose listing failed and lists again on `notifications/tools/list_changed`, which the adapter sends once a link completes.

**Stdio bridge (CA-9).** `lenny-mcp-stdio`, which a `lenny-tools` init container delivers to every sidecar `type: agent` pod, lets a stock CLI reach the adapter's socket servers as a stdio MCP server.

**Memory removal and the external pattern (CA-10).** The `MemoryStore` role, its tools, routes, metrics, and table go. Project knowledge stays in the repository, and user-level memory is a deployer-run MCP server registered as a connector.

```
 Browser ──(link URL, CA-0, CA-1)───────────> Gateway ──(code exchange)──> OAuth provider
                                                 │ connector link: tenant, connector, user, environment
 Client ──(create session, link check, CA-4)─> Gateway
                                                 ▲
                                                 │ tool calls, and the typed error back (CA-5)
 Pod: stock CLI ──(stdio)──> lenny-mcp-stdio ──(socket)──> adapter

 Gateway ──(user's token attached, refreshed under CA-2)──> external MCP server
```

## Owner decisions

These are settled by the owner, and the rationale stays here so a later reader does not reopen them by accident.

| Decision | Text or effect |
| :-- | :-- |
| Remove built-in memory | The owner asked for "Dropping memory store in Lenny + replacing with this generic mechanism". |
| No stored tool catalog | The owner rejected registering tools and arguments apart from the MCP server: "that creates a shadow config that will be out of sync". `tools/list` always asks the server. |
| The authorization failure reaches the runtime | "Shouldn't the auth error be propagated to the runtime so it knows it needs to authenticate?" The failure is translated at the gateway boundary, because the runtime's server is the adapter's local server. See "CA-5". |
| A link check runs regardless of runtime | The owner: "the Coder-style check makes sense no matter what". Coder, a cloud development environment product, requires a user to link each external account that a workspace template names before it creates or updates the workspace, unless the template marks that account optional. The owner's phrase refers to a check of that kind. |
| The trigger is a connector property | The owner replaced a pool-level "required" flag with "requiring auth to list tools". This design detects that property live, for the owner's own reason that a stored flag can drift. |
| The runtime declares tolerance | "The runtime should indicate whether they can gracefully handle failed `tools/list`". See "CA-8". |
| A parent retries after a link | No push notification. The refusal carries `details.retryAfterSeconds`. See "CA-7". |
| Impersonation and linked connectors | "Accept and document the risk." No check is added for this design's sake. See "Limits". |
| Connector tokens stay keyed by environment | The reason is separation: a token granted in one project context is not the token another context draws. Policy controls access under either key, with or without the environment, so policy is not the reason. |
| The capability snapshot stays | "Keep the snapshot and don't change anything else." A deployer who wants enforcement downstream uses OAuth scopes. See "CA-8". |
| An explicit resume checks links first | The check runs before an explicit resume of a released pod starts its new process, so a user who links gets the connector on that resume. See "CA-6". |
| The bridge reaches every runtime | The platform delivers it to every sidecar `type: agent` pod, using the runtime supervisor's delivery pattern. See "CA-9". |
| The bridge keeps its own init container | The owner chose this over sharing `lenny-supervisor-stage`, accepting about one second per pod and leaving the supervisor untouched. See "CA-9". |
| The shared OAuth flow lands first | It is its own proposal, "CA-0", ahead of this design's other proposals and of [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) in [`user-credential-delivery-design.md`](user-credential-delivery-design.md). "Instructions for reviewers of other designs" carries the changes this requires elsewhere. |

## Requirements

Alice and Bob are developers at `acme`, and Carol is its tenant admin. `globex` is another tenant.

- CAR-1. A deployer can give sessions persistent memory by registering an MCP server, with no Lenny code for that server.
- CAR-2. A user can link a connector before a session exists, from any client.
- CAR-3. A runtime learns that a connector needs authorization through a typed error, and Lenny stores no copy of a connector's tools.
- CAR-4. A runtime that cannot recover from a failed `tools/list` never starts with a connector that would fail it, unless Lenny has no way to know (the probe's `unknown` result in "CA-4").
- CAR-5. An implicit restart never fails because a connector link is missing.
- CAR-6. A delegated child uses its parent's user and environment for connector tokens. When the delegation crosses a sharing path, the child uses the parent's user and the target environment.
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
| Attacker in the tenant | Send a victim a link URL, so the victim's token lands under the attacker. | Refused by CP-2: the link flow's start page (CA-0) requires the flow's own subject, and a cookie binds the browser. |
| Prompt-injected agent | Raise prompts repeatedly to wear a user down. | The gateway raises at most one prompt per tree and connector, and a dismissal stands for that tree. An agent cannot supply the URL. |
| Prompt-injected agent | Read or answer a connector prompt through an intercepting parent. | Refused by CP-3: connector prompts skip interceptors and go to the human-facing edge. |
| Prompt-injected agent | Send source code to a memory server in tool arguments. | Not prevented once granted. This path runs on the gateway and passes no `netagent` or `lenny-egress` check. CP-5 and "Code exposure" under "External memory pattern" state the controls. |
| Malicious content in memory | Plant text in memory that steers later sessions. | Not prevented. Memory returned to a model is untrusted input, and `PostConnectorResponse` is the point where a deployer can scan it. |
| Platform admin impersonating a user | Message Alice's session so its agent writes to her external memory with her token. | Not prevented, by owner decision. See "Limits". |
| Administrator editing a connector | Point a linked connector at a different server. | CP-6 makes the stored tokens unusable, and each user links again. |
| Malicious MCP server | Answer the anonymous probe with 401 to force prompts, or with 200 to skip them. | It affects only its own connector. A 200 followed by a 401 reaches the tool-call path in "CA-5". |

## Current state

| Claim | Truth | Evidence |
| :-- | :-- | :-- |
| Tokens are per user | True. They are keyed on tenant, connector, user, and environment. Another user's call gets a 401. | `connectorcredstore.go`, **[measured]** |
| A linked user can list and call tools | True. | **[measured]**; `TestExternalMCPToolPerDelegationScoping` and `TestOAuthConnector` pass |
| §9.3's in-session prompt exists | False. `bearerFor` returns an empty string, the client dials with no token, and the error is a generic failure. No code raises a connector-initiated elicitation, and a comment in `mcptools/elicitation.go` calls that path "future". | `invoker.go`, **[measured]** |
| Refresh happens | False. `RefreshAccessToken` and `RotateAccessToken` have no non-test caller, and an expired token with a valid refresh token gets a 401 with zero refresh calls. | **[measured]** |
| A token under another environment is found | False. The lookup is exact, and a mismatch reads as a 401. | **[measured]** |
| Each connector has its own `tools/list` | True. The adapter opens one `@lenny-connector-<id>` socket per connector, and the provider binds to that connector. | `connectortoolprovider.go`, `connectormcp.go` |
| `initialize` on a connector socket contacts the external server | False. The adapter answers it locally, so the failure first appears at `tools/list`. | `pkg/adapter/mcp/server.go` |
| An external 401 reaches the runtime as such | False. It becomes gRPC `Internal`, then JSON-RPC `-32603`, with the status only in message text. | `leasecontrol/connectortools.go`, `mcp/server.go` |
| The adapter can send `list_changed` | False. It declares `tools: {}`, and its loop only answers requests. | `mcp/server.go` |
| A session with no policy gets no connectors | False. `AuthorizeConnector` permits when no layer resolves. | `connectorauthz.go` |
| A child keeps its parent's user | True. | `buildChildSession`, **[measured]** |
| A child keeps its parent's environment | False. A parent in `team-a` produces a child with an empty environment. The child therefore also loses the environment's default policy and its `connectorSelector` capability filter. | `buildChildSession`, **[measured]** |
| A derived session keeps its source's environment | No code in `derive.go` copies it. | `derive.go` **[source]** |
| A connector prompt reaches the user through an intercepting parent | False. `WalkChain` stops at an intercepting ancestor for connector and agent initiators alike. | `pkg/elicitation/chain.go`, **[measured]** |
| A gateway-originated elicitation has a precedent | True. §8.6 lease extension raises one through the interaction store. | `leaseelicit.go` |
| A resume reopens connector servers | True. `Resume` calls `sessionConnectors`, rewrites the manifest, and starts the runtime, while a pod-held resume starts no process. | `pkg/adapter/resume.go`, `sessionserver/resume.go` |
| A failed connector resolution aborts a start | False. It leaves the session with no connector servers. | `pkg/adapter/connectormcp.go` |
| Derived sessions lose connector tokens | The specification says so in §7.1, under **Connector state.** The code keys tokens by user, so a derived session by the same user in the same environment finds them. | §7.1, `connectorcredstore.go` |
| Connector tokens are erased with a user | True. `connectorCreds` is in the `tokens` eraser. | `cmd/lenny-gateway/adminrouter.go`, `tenant_erasers.go` |
| Memory REST routes are in §15.1 | False. `route_table.go` mounts them and §15.1 has no row for them. | **[source]** |
| Each connector tool call has an audit event | False. The audit catalog has events for connector administration and the OAuth flow. A tool call produces a trace span, and `PreConnectorRequest` and `PostConnectorResponse` interceptors see it. | `invoker.go`, `pkg/observability/audit/catalog.go` |
| OAuth scopes reach the provider | True. `BuildAuthorizationURL` sends the connector's `auth.scopes`. The server decides what a token may do. | `pkg/connectoroauth/flow.go` |

## CA-0: Shared OAuth link flow

The account-link attack exists in today's code, and tying its fix to either feature would leave it open until that feature ships. The owner therefore decided that the shared flow is its own proposal and lands first. It takes the hardened sequence in [`user-credential-delivery-design.md`, "Flow"](user-credential-delivery-design.md#flow) in UC-2, moves it into `pkg/connectoroauth`, and converts today's §9.3 handlers to it, which closes the defect with no other feature.

| Piece | Owner |
| :-- | :-- |
| One-time flow identifier, with a 10-minute lifetime, naming a subject | Shared |
| Start page: OIDC login at the tenant's identity source, through the "Browser login" of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md), whose shared resolver supplies the tenant and subject, and refusal when the logged-in tenant and subject differ from the flow's | Shared |
| A nonce in a `__Host-` cookie (a cookie prefix a browser accepts only over HTTPS, with path `/` and no `Domain`), set `HttpOnly`, `Secure`, and `SameSite=Lax` so the browser sends it on the provider's top-level redirect back, PKCE, and an OAuth `state` parameter, which the provider returns unchanged, signing the flow identifier and the nonce's hash | Shared |
| Callback validation: signature, single consumption, cookie nonce, and the `iss` issuer parameter of RFC 9207 where the provider returns it | Shared |
| A hook that receives the validated code and verifier | Shared interface |
| What the start page displays, the per-provider callback path, and any digest compared on return | Supplied by each caller |
| Code exchange and token storage | Connectors: the gateway, into the connector credential row. UC-2: the Token Service, with identity binding, the UC-2 rule that ties the provider account to the Lenny user, and a confirmation page. |

## CA-1: Link flow

### Connector routes

The record the shared flow of "CA-0" writes for a connector stays the existing connector credential row, because [`user-git-identity-design.md`, "User tokens on a VCS host"](user-git-identity-design.md#user-tokens-on-a-vcs-host) withdrew any route for a link token to reach the connector path. Its revision 2 had proposed `hostLink`, which would have given a connector the token of a user's account link.

| Route | Purpose |
| :-- | :-- |
| `POST /v1/connector-links` | Body `{"connector": "memory", "environment": "team-a"}`. Returns `linkUrl`. |
| `GET /v1/connector-links` | Lists the caller's links with connector, environment, expiry, and whether a refresh token exists. It returns no token. |
| `DELETE /v1/connector-links/{connector}?environment=` | Removes the caller's link and revokes it at the provider when the provider has a revocation endpoint. |
| `GET /v1/connector-links/start/{flow}` | The browser start page. |
| `GET /v1/connector-links/callback` | The provider's redirect target. |

`lenny-ctl connector link <id> [--environment <name>]` wraps the first route and prints or opens the URL. `POST /v1/admin/connectors/{name}/oauth/authorize` and `GET /v1/admin/connectors/oauth/callback` are removed in proposal "CA-A", under "Proposal split and order", since no deployment depends on them.

1. The link URL is a Lenny URL for a shared flow naming the subject and environment.
2. The shared start page runs its login and subject checks.
3. The start page names the connector and its endpoint hosts, runs `CheckConnectorURLDomain`, which checks that the provider URL's host matches the connector's `expectedDomain`, and redirects.
4. The shared callback validates the return and calls the connector hook. A missing or mismatched cookie aborts the flow.
5. The gateway exchanges the code and stores the token with the endpoint digest from "CA-2".
6. The gateway resolves every pending connector prompt for that tenant, user, connector, and environment, and notifies the affected sessions under "CA-8".

An impersonation bearer is refused on every link route, as [`user-credential-delivery-design.md`, "Flow"](user-credential-delivery-design.md#flow) requires for `/v1/user-links`.

## CA-2: Token lifecycle

**Refresh.** `bearerFor` refreshes when a token has fewer than 5 minutes left, and once more after a 401 from the server. Refresh-token rotation, in which a provider issues a new refresh token on each refresh and invalidates the old one, means one writer per credential, so the row carries `refresh_lease_holder` and `refresh_lease_until`, using the lease form in [`user-credential-delivery-design.md`, "Refresh"](user-credential-delivery-design.md#refresh): a 30-second lease set by a conditional update. Other replicas re-read the row for up to 5 seconds. The 5-minute refresh threshold, the lease length, and the 5-second wait are operator-tunable. Both designs use one lease form, so a later change to it considers account links and connector credentials together, as that section notes.

- `invalid_grant` deletes the token, and the user is unlinked from then on.
- A transient refresh failure returns the transient error and keeps the row.

**Endpoint binding.** The credential row records a digest of `mcpServerUrl`, `authorizationEndpoint`, and `tokenEndpoint`. A lookup whose digest differs from the connector's current one treats the row as absent and audits `connector_link.endpoint_changed`. This closes a gap in which an edit to `mcpServerUrl` sends every stored token to the new server **[source]**.

**Resource indicator.** A resource indicator (RFC 8707) is a `resource` parameter that names the server a token is for. `BuildAuthorizationURL` sends no `resource` parameter **[source]**. A proposal checks the MCP authorization page for its requirement on resource indicators and adds the parameter if the page requires it **[pending]**.

## CA-3: Connector access and environment

**Default deny.** `AuthorizeConnector` returns `ErrConnectorNotPermitted` when no policy layer resolves, for every connector, which §8.3 evaluates as a candidate of type `connector`. An error resolving a policy fails closed. Carol grants the connector with a `DelegationPolicy` target of type `connector`, on the runtime or as the environment's default. This reverses the "conservative fall-through" comment in `connectorauthz.go`, which stays correct for other candidate types.

**Environment on a child.** The row `buildChildSession` writes carries the parent's environment, or the target environment when the delegation crosses a sharing path under [decision 14](tenant-scoping-identity-design.md#cross-environment-sharing-decision-14). [`tenant-scoping-identity-design.md`, "TS-3: Session environment context and cross-environment sharing"](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing) already requires this under **Delegated child's environment**, and **Effects on existing features** in the same section states that connectors read that field. This design depends on that change and lands it itself if that design has not landed first.

**Environment on a derived session.** A derived session carries its source's environment, as that design also requires under **Derive**. §7.1 **Connector state.** is rewritten: a connector link belongs to a user and an environment, so a derived session reuses it under those keys.

## CA-4: Link check

### Evaluation

For each active connector the session's policy permits, in the environment the session will run in, and excluding deferred connectors:

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

The check runs at the post-chain point of [RT-2](runtime-routing-design.md#post-chain-point): after the `PreRoute` chain, whose interceptors can change the runtime and pool, after revision selection ([RV-2](runtime-revisions-design.md#rv-2-selection)), and before the pod claim. It reads `capabilities.deferredToolDiscovery` from the chosen revision, merged with a derived runtime's own fields where one applies, because `capabilities` is a versioned field under [RV-1](runtime-revisions-design.md#rv-1-revisions) and the revision decides which binary the session runs. The connector set it evaluates comes from the policy and environment of the final row. In `create.go` the post-chain point replaces the split segments `preRouteBeforeExperiment` and `preRouteAfterExperiment`, which runtime routing deletes with the experiment code, and precedes `claimMintPersist`. §7.1's credential pre-check, step 3, which refuses a session when no large language model (LLM) provider credential is available, runs before the chain, and step 3a, as runtime routing rewrites it, describes the chain, revision selection, and the post-chain point, so the link check is part of step 3a and precedes step 4, the warm pod claim. No pod is claimed while a person is in a browser. Like the credential pre-check, it is a point-in-time check rather than a reservation.

It applies to `POST /v1/sessions`, `POST /v1/sessions/start`, derive, and the external protocol adapters' implicit session creation. It applies to sessions with `executionMode: session`.

### Outcome

| `deferredToolDiscovery` of the chosen revision | Any connector `needs_link` | Result |
| :-- | :-- | :-- |
| `false` (default) | Yes | `403 LINKS_REQUIRED` (error category `POLICY`) with `details.connectors`, each with `id`, `displayName`, `environment`, and `linkUrl`, and with `details.userLinks`, as "Combined missing-links response" below states. Naming the environment makes a link held under another environment easy to diagnose. |
| `true` | Yes | The session is created, and the response lists the same connectors under `unlinkedConnectors` so a client can link early. |
| Either | No | The session is created. |

The request accepts `deferConnectors: ["memory"]`, stored on the session row. A deferred connector raises no prompt. On a runtime with `deferredToolDiscovery: false` it is left out of the manifest. On a runtime with `deferredToolDiscovery: true` it stays in the manifest, and its `tools/list` returns the typed error. A link that exists when a runtime starts wins over a deferral, so a user who links later gets the connector at the next start.

**Combined missing-links response.** A session can need a connector link and an account link at once. The account link check of [`user-credential-delivery-design.md`, "Flow"](user-credential-delivery-design.md#flow) and the repository check of [UG-2](user-git-identity-design.md#ug-2-repository-authorization) report a missing account link, and both run at the same post-chain point. The gateway runs all of these checks before it answers and returns one `403 LINKS_REQUIRED` (error category `POLICY`) that reports every missing link, so a user links once and retries once. `details.connectors` lists the connectors as the table above states, and `details.userLinks` lists the account links, each with the link provider's name and its `linkUrl`, as that design states. Both lists are present in every such response, and either can be empty. The code is `LINKS_REQUIRED` whichever kinds of link are missing, so a client handles one code and reads both lists. The rule applies on every creation path that "Placement" names, derive included. A refusal of a single kind outside session creation keeps its own code, such as the `CONNECTOR_LINK_REQUIRED` tool error that "CA-7" returns for a delegated child and the explicit resume refusal of "CA-6".

**Why every permitted connector counts.** The owner replaced the pool-level "required" flag with this property, so a session is refused for a connector it might not use. Default deny keeps the set to what an administrator granted, and `deferConnectors` handles the rest. A workload that cannot run without a connector does not defer it.

**Clients.**

- A client that cannot pass the field, such as a stock OpenAI SDK, shows the error, and its user runs `lenny-ctl connector link`.
- An automated caller with no browser defers.
- Clients that create sessions through Lenny's MCP interface receive the combined missing-links response as a tool error with the same `details`. [MP-3](mcp-protocol-upgrade-design.md#mp-3-elicitation) of [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) adds a form per protocol version for a client that declared URL-mode elicitation, with one URL-mode elicitation per missing link, and keeps this tool error for every other client. Each form reaches only the client that called create, so CAR-7 holds.

## CA-5: Propagating the failure

### Why the raw 401 stops at the gateway

The runtime is an MCP client, and its server is the adapter's local server, which it has already authenticated to with the manifest nonce. External authorization is a separate hop between the gateway and the external server, as §9.1 and §9.2 state. An HTTP 401 cannot cross for three reasons:

- A Unix socket or stdio pipe has no HTTP status, and the MCP authorization page says stdio implementations should not follow its flow **[documented]**.
- A client that ran OAuth itself would hold the token in the pod, against CP-1.
- The person is at the Lenny client. Claude Code's documentation says a `claude -p` run cannot run the OAuth flow **[documented]**.

MCP's `URLElicitationRequiredError` (`-32042`) has the right meaning, since the adapter's server needs third-party authorization that must not transit the client. It also puts the URL in the client's hands, and the client here is the runtime, inside a pod with no browser. CAR-7 keeps the URL out of the pod, and that is the reason this design does not use the error. The error arrives in protocol version `2025-11-25`, and the adapter speaks `2025-03-26` today **[documented]** **[source]**. [MP-5](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers) moves the adapter to `2025-11-25` and `2026-07-28`, so the protocol version stops being an obstacle, and CAR-7 still excludes the error.

### Translation

1. The connector client returns a typed `ErrAuthRequired` on 401, and on 403 with `insufficient_scope`.
2. `connectortools.Bridge`, the gateway type that serves the adapter's connector tool calls and is unrelated to the stdio bridge of "CA-9", passes it, and `leasecontrol` returns `codes.FailedPrecondition` with an `ErrorInfo` reason `CONNECTOR_LINK_REQUIRED`.
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
- **Suppression.** §9.2 already exempts connector OAuth from depth suppression, the rule that by default suppresses agent-initiated elicitations at delegation depth 3 and deeper, and `DepthBlockAll` already returns false for this initiator **[source]**.
- **Deduplication.** One prompt per `root_session_id` and connector is pending at a time. A dismissal stands for that tree. An expired prompt can be raised again once per `maxElicitationWait`.
- **Lifetime.** It is non-blocking, unlike `leaseelicit`. It expires at `maxElicitationWait` and is dismissed when the tree ends. §9.2's rule that expires a `lenny/request_elicitation` when a session leaves its pod does not apply, since no runtime waits on it.
- **Budget.** It does not count against `maxElicitationsPerSession`, since an agent cannot produce more than the deduplication allows.
- **URL.** The URL is the Lenny link URL. §9.2 **URL domain validation** moves for connector prompts: the domain check runs on the provider URL at the start page, while the prompt's own URL names the gateway. `expected_domain` remains in the elicitation's provenance, the metadata the gateway attaches to every elicitation for the client to display.
- **Completion.** A client's `accept` only means the user will open the link, as in MCP's URL mode **[documented]**. The callback in "CA-1" resolves the prompt.

A client that connects through an external protocol adapter whose `AdapterCapabilities` report `supportsElicitation: false` sees nothing, and the error text tells the agent what is needed. Its user links through `lenny-ctl`.

## CA-6: Resume and restart

These paths exist today **[source]**:

| Path | Code | New runtime process | Client waiting | Behaviour **[design]** |
| :-- | :-- | :-- | :-- | :-- |
| `suspended`, pod held | `resumeHeldPodRequest` | No | Yes | No check. The runtime keeps its tool list. An expired token refreshes on the next call, or reaches "CA-5". |
| Explicit resume, `awaiting_client_action` or `suspended` with pod released | `handleResume` into `enterResumePending` | Yes | Yes | Link check before `enterResumePending`, with the "CA-4" outcomes, except that a refusal answers `403 CONNECTOR_LINK_REQUIRED` with `details.connectors`, because a resume is outside session creation and keeps the code of the check that refused it. The body accepts `deferConnectors`. A refusal leaves the state unchanged, and that state's clocks, such as `maxSuspendedSessionSeconds`, keep running. |
| Explicit resume that finds the held pod gone | `resumeHeldPodRequest` fallback | Yes | Yes | Treated as an implicit restart, since the request has already passed the point of a clean refusal. |
| Message arrives for a released session | `messages_delivery.go`, `suspended_backlog.go` | Yes | Sender only | Implicit restart. |
| Retry driver, `resume_pending` to `resuming` | `failure.go`, resume driver | Yes | No | Implicit restart. |
| Credential-rotation restart at the Basic and Standard integration levels | §15.4.3 matrix | Yes | No | Implicit restart. |
| Tree recovery | §8.10, §7.3 | Yes | No | Implicit restart. |

**The revision a restart reads.** A resume or restart runs no `PreRoute` chain. Each rule below reads `capabilities.deferredToolDiscovery` from the revision the new process will run: the session's recorded `runtimeRevision`, or the revision the session moves to when that revision's pod set is gone, as [RV-6](runtime-revisions-design.md#rv-6-sessions-over-time) states under **Retry and resume.**

**Implicit restart.** `ListSessionConnectors` is the one call every start path reaches: start, SDK warm, and `Resume` all go through `sessionConnectors` **[source]**. It gains the rule below, so a restart needs no path-specific code.

- On a runtime with `deferredToolDiscovery: false`, a connector that evaluates to `needs_link` is left out of the manifest. The session continues without it.
- On a runtime with `deferredToolDiscovery: true`, the connector stays, and the failure reaches `tools/list`.
- Either way, the gateway raises a connector prompt and emits `session.connector_unavailable` with the connector and reason.

**Omission rather than a failing server on a runtime with `deferredToolDiscovery: false`.** A server that fails `tools/list` can break a stock CLI's start, for example Codex with the server marked `required = true` in its MCP configuration **[documented]**, and an omitted server cannot. A resolution failure already omits connectors today, so runtimes already tolerate an absent entry **[source]**.

**The connector comes back at the next runtime start.** On a runtime with `deferredToolDiscovery: false`, a link completed mid-session has no effect until then, because the manifest and sockets are fixed for the process. A user who needs it sooner suspends with pod release and resumes, or starts a new session.

**Why no refusal on these paths.**

- A retry driver has no one to answer it, and a refusal would turn a recoverable pod failure into a session failure.
- `maxResumeWindowSeconds` and `maxTreeRecoverySeconds` keep running, so a wait for a person could fail a whole tree.

**Latency.** Probes run in parallel and use the cache, so the added resume time is bounded by the 3-second timeout. A probe that returns `unknown` includes the connector.

**Deferral.** `deferConnectors` persists on the row, so a resume honours the earlier choice without a prompt.

**Confirmed.** The adapter re-resolves connectors on `Resume` and writes the manifest before starting the runtime, so this rule takes effect on every restart with no adapter change **[source]**. `TestSessionConnectors_spec_9_3_142` covers the resolve path, and a new test pins each row above.

## CA-7: Delegated children

### Identity

A child uses `parent.UserID` **[measured]**, so the parent's user's link is the right token, and `TestExternalMCPToolPerDelegationScoping` covers policy scoping on that path. With "CA-3" a child that does not cross also has the parent's environment. Under [`tenant-scoping-identity-design.md`, "Cross-environment sharing (decision 14)"](tenant-scoping-identity-design.md#cross-environment-sharing-decision-14), which the owner settled on 2026-11-03, a child that crosses a sharing path runs in the target environment, uses that environment's `connectorSelector`, and needs a link there, because §4.3 keys connector tokens on the environment.

### Check at delegation

The link check runs in `buildChildSession` at the post-chain point of [RT-2](runtime-routing-design.md#post-chain-point) for `kind: delegate`: after the `PreRoute` chain, whose interceptors can change the child's runtime and pool, and after revision selection for the child's runtime ([RV-6](runtime-revisions-design.md#rv-6-sessions-over-time), **Delegation.**). It reads `capabilities.deferredToolDiscovery` from the child's chosen revision. Today's call site, immediately after `ApplyExperimentRouting`, goes with the experiment code that runtime routing removes. Export materialization, which copies the files the parent exports into the child's workspace ([§8.7](../spec/08_recursive-delegation.md#87-file-export-model)), runs earlier in that function, and a returned error already undoes it through `undoChildExport`, and the deferred release returns the delegation tree budget, the per-tree counters of nodes, memory, children, and tokens that a delegation reserves **[source]**. The refusal therefore wastes export work but leaves no child row, pod, or budget behind. §8.3's credential pre-check at delegation is the precedent.

| `deferredToolDiscovery` of the child's chosen revision | Child's permitted connector `needs_link` | `lenny/delegate_task` result |
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

**How the parent learns of completion.** The parent retries `delegate_task`, waiting at least `details.retryAfterSeconds`, which defaults to 30 seconds and is operator-tunable. The interval exists because each retry by an LLM parent can cost a model turn. A retry raises no second prompt, because of the deduplication in "CA-5". The owner decided against a push notification. A message the gateway places in a session's inbox reaches only runtimes with `capabilities.injection.supported`, the runtime capability for message delivery mid-session, which defaults to `false`, and holding the call open risks client tool timeouts.

**Detached clients.** A prompt with no attached client stays pending until `maxElicitationWait`. The parent's retries keep failing and the parent decides whether to defer, so nothing deadlocks. A pending prompt raises no `input_required` state, the session state of waiting on input, since no session blocks on it, and §8.8's deadlock detection is unaffected.

**Children on restart.** A child's restart is an implicit restart under "CA-6", including under tree recovery. An omitted connector on a child is visible to its parent only through events the parent can read under its tree visibility (`treeVisibility`, how much of the task tree a session may see).

### Confirmed against §8

| Question | Answer | Evidence |
| :-- | :-- | :-- |
| Does the child's token lookup use the right user? | Yes. | **[measured]** |
| Does it use the right environment? | Not today. "CA-3" fixes it. | **[measured]** |
| Does a child's connector set come from its own policy? | Yes. `ListSessionConnectors` and `CallTool` both evaluate the child session's policies. | `connectorauthz.go` |
| Can a child reach a human for OAuth at any depth? | Yes by §9.2 and `ShouldSuppress`. `WalkChain` then stops at an interceptor, which "CA-5" fixes. | **[measured]** |
| Does a refusal leak resources? | No. The error path undoes the export and returns the budget. | `service.go` |

## CA-8: Runtime capability and late tools

`capabilities.deferredToolDiscovery` joins `preConnect` in §5.1 Runtime, defaulting to `false`. `true` states both of these:

- The runtime keeps a connector server after `tools/list` returns an error.
- It lists again on `notifications/tools/list_changed`.

A single field covers both, because one without the other does not help. `lenny-compliance` gains a check that runs a stub connector through a failure and a notification, in the §15.4.6 battery beside `connector_mcp_server_reachability`. A runtime declaring `deferredToolDiscovery: true` must pass it, and the suite reruns on each upstream release. Claude Code 2.1.317 showed both behaviours in the spike under "CA-9" **[measured]**, so it can declare `true` once it passes this check and the adapter changes below exist. It stays `false` until then. Codex stays `false` until it is measured.

Claude Code re-lists on the notification, but in the spike the model called the tool only because its prompt told it to check again, and nothing in the stream showed the model being told the tools had arrived **[measured]**. A runtime with `deferredToolDiscovery: true` therefore gains the tool without any assurance that the agent will notice it.

**Adapter changes for a runtime with `deferredToolDiscovery: true`.**

- Declare `tools: {"listChanged": true}` on connector servers.
- Allow the server to write a notification on its open connections, with writes serialized against responses.
- Add a gateway-to-adapter RPC on `LNK-POD-GRPC`, named `NotifyConnectorToolsChanged`, carrying the session and connector. Its channel placement in §28 follows the naming law of §28.1, the rules for naming links and channels, at proposal time **[pending]**.
- The link callback can land on any replica, so the call uses `CH-HOLDERFORWARD` to reach the session's coordinating replica.

No runtime needs this until one declares `deferredToolDiscovery: true`, so this work can follow the path for `deferredToolDiscovery: false`.

**Capability metadata.** §5.1 derives `conn.ToolCapabilities`, the capability snapshot, at registration and on refresh, and the §10.6 filter reads it **[source]**. This is a smaller version of the snapshot the owner rejected for tool catalogs. The owner decided to keep it and change nothing else, and this design adds no work to it. Its behaviour is:

- A tool with no inferred capability falls to the conservative default, `admin` under the default `strict` inference mode, so a new tool name fails closed under a restrictive filter.
- An existing tool whose behaviour changes keeps its old classification until a refresh.
- A server that requires auth to list tools has no inference until an administrator links and refreshes it, so a restrictive filter denies its tools until then.

**Enforcement downstream.** A deployer who wants permissions enforced by the server uses OAuth scopes. `BuildAuthorizationURL` sends the connector's `auth.scopes` **[source]**, and a server that honours them refuses what the token does not allow. For example, a deployer registers `memory-ro` with `scopes: [memory.read]` alongside a read-write entry. Lenny does not verify that a server enforces scopes, and each entry needs its own link.

## CA-9: Stdio bridge

Connector and platform servers use abstract Unix sockets and a `_lennyNonce` field in `initialize` **[source]**. Claude Code documents stdio, HTTP, Server-Sent Events (SSE), and WebSocket, and Codex documents stdio and HTTP, and neither states Unix socket support **[documented]**.

`lenny-mcp-stdio --connector <id>` or `--platform` is a small static binary. It reads `/run/lenny/adapter-manifest.json`, dials the named socket, adds the nonce to `initialize`, answers `adapterChallenge` in nonce-only mode, and copies JSON-RPC in both directions, notifications included. `cmd/git-credential-lenny`, the in-pod git credential helper that obtains a short-lived token from the gateway, is the precedent for the handshake, although it does not answer the challenge.

### Delivery

The platform delivers the bridge to every sidecar `type: agent` pod, whatever the runtime image contains. It follows the runtime supervisor's pattern in §4.7.10 **Runtime supervisor.** and §13.1 **Runtime supervisor volume.** **[source]**:

| Element | Value **[design]** |
| :-- | :-- |
| Image | `lenny-tools`, digest-pinned, built `FROM scratch` with `CGO_ENABLED=0`, so the binary runs on any libc. |
| Init container | `lenny-tools-stage` copies the binary into an `emptyDir` volume named `lenny-tools`. It runs at a user ID (UID) that is neither the agent nor the adapter UID. |
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
    visibility: tenant                    # scoped to one tenant, the default; the other value is platform
    auth:
      type: oauth2
      authorizationEndpoint: https://idp.acme.com/authorize
      tokenEndpoint: https://idp.acme.com/token
      clientId: lenny
      clientSecretRef: lenny-system/memory-client-secret   # Kubernetes Secret holding the OAuth client secret
      expectedDomain: idp.acme.com          # host the provider's login URL must match
      scopes: [memory.read, memory.write]
```

The server scopes data by the token's `sub` claim, because Lenny forwards the user's token and adds no user identity of its own. Carol then grants `memory` through a delegation policy.

**Prerequisite: F-9.3.19.** The example registers a confidential client through `clientSecretRef`. The chart gives the gateway no `get` on that Secret, so under the chart's defaults the token exchange fails at the callback, as [`user-credential-delivery-design.md`, "Defects found while verifying"](user-credential-delivery-design.md#defects-found-while-verifying) records under F-9.3.19. The example works once F-9.3.19 is fixed. Until then, a deployer registers a public client, with no `clientSecretRef`, or grants the gateway read access to the named Secret.

**Worked example.**

1. Alice creates a Claude Code session. The runtime has `deferredToolDiscovery: false`, she has no link, and the probe returns 401, so she receives `403 LINKS_REQUIRED` with `memory` and its `linkUrl` under `details.connectors` and an empty `details.userLinks`.
2. She opens the URL, logs in, approves, and the token is stored for `acme`, `memory`, Alice, and the session's environment.
3. She retries. The check reads `linked`, a pod is claimed, the manifest lists `memory`, the wrapper writes the CLI's MCP configuration, and `tools/list` returns the server's own tools.
4. Hours later the token nears expiry, and the next call refreshes it.
5. Her pod fails. The retry driver restores the session, and the manifest lists `memory` again.
6. The agent delegates to a reviewer runtime with `memory` granted. The child uses Alice's link in the same environment.

Bob's first session is refused the same way. He passes `deferConnectors: ["memory"]`, and his session runs without it and without further prompts.

**Erasure.** `DeleteByUser` removes the connector tokens through the existing `tokens` eraser **[source]**. Deleting the memory data is a deployer obligation, which the operator guide states. Before deleting a user's tokens, the erasure job reads the connectors they were linked to, and the receipt names them under a new `externalDataNotErased` field, so a receipt never implies the data was removed. A webhook can follow when a customer asks.

**Retention, residency, capacity, and retrieval quality.** These move to the deployer.

**Code exposure.** A memory connector is a channel from a session to an external store. Its traffic runs on the gateway and bypasses `netagent` and `lenny-egress`, and user-scoped memory can carry content from one repository into a session on another. Lenny cannot partition memory by repository.

- A grant is explicit (CP-5).
- `PreConnectorRequest` and `PostConnectorResponse` interceptors see each call, and each call produces a trace span. There is no per-call audit event **[source]**.
- The §10.6 filter can deny write tools, and OAuth scopes can restrict them at the server.
- "Instructions for reviewers of other designs" asks [`egress-and-git-design.md`](egress-and-git-design.md) to record this path.

## Limits

- A connector token is decrypted in the gateway (F-4.3.1). A compromised replica can use any connector token.
- **Impersonation is an accepted risk, by owner decision.** `requireCreatingBearerAllowed` stops an impersonation bearer from creating a session, and `handleMessages` still accepts one **[source]**. The invoker spends the session owner's token with no impersonation check. A platform admin impersonating Alice can therefore message her session, and its agent can write to her external memory as her. The external service records Alice as the actor, the write outlasts the session, and no per-call audit event names the impersonator. The operator guide states this. [`user-credential-delivery-design.md`, "Limits under both deliveries"](user-credential-delivery-design.md#limits-under-both-deliveries) refused built-in memory writes for this reason, under **Impersonation**, and that protection does not carry over to connectors.
- A shared-process pod, one whose pool sets `sessionPolicy.maxConcurrentSessions` above one so that one runtime process serves several sessions at once, opens its connector sockets once, from the first session's set. The adapter's `callingSession`, which names the session a call is forwarded under, refuses a call when it cannot name one session **[source]**. Omission cannot be per session there. Offering a link-requiring connector on such a pool is not recommended, and no validator enforces this in this revision.
- `executionMode: service` has no link check. A connector call on it reaches "CA-5".
- A runtime with `capabilities.preConnect: true` starts before a session binds. A stock CLI's MCP configuration written at that point lists no connector for that session **[pending]**.
- The probe tells only whether listing needs auth. A server that lists different tools once authenticated gives a runtime with `deferredToolDiscovery: false` the authenticated list at its next start, since a linked user lists with their token.
- A stock client's rendering of a `tools/list` error or an omitted server is outside Lenny's control. Claude Code does not show the `tools/list` error to the model **[measured]**.
- A user in two environments links twice, which keying tokens by environment under §4.3 scoping implies and the owner accepted for the separation it gives. [`user-credential-delivery-design.md`, "Stored links"](user-credential-delivery-design.md#stored-links) keys account links per user, under its [decision 30](user-credential-delivery-design.md#decisions), so the two kinds of link behave differently.
- The capability snapshot does not notice a changed tool until a refresh, as "CA-8" states.

## Alternatives rejected

| Alternative | Reason |
| :-- | :-- |
| Serve a tool catalog stored at registration | The owner rejected it, since it is a copy that drifts. Triggering the prompt at `tools/list` removes the need. |
| A pool-level `required` flag | It answers a different question from the one that decides the failure, and `deferConnectors` covers the workload case. |
| A stored `requiresAuthToListTools` field | It drifts. The probe asks the server. |
| Pass the 401 through | See "CA-5". |
| `URLElicitationRequiredError` to the runtime | It places the URL in the pod, against CAR-7. |
| An empty `tools/list` instead of an error | It hides the failure from the runtime, which is the opposite of the owner's decision. |
| Block `tools/list` until the user finishes | It exceeds client startup timeouts. |
| Refuse restarts with a missing link | It turns recoverable failures into session and tree failures. |
| Omit by default on delegation | See "CA-7". |
| Use `/v1/user-links` tokens for connectors | [`user-git-identity-design.md`, "User tokens on a VCS host"](user-git-identity-design.md#user-tokens-on-a-vcs-host) withdrew `hostLink`, since it moves a link's token onto the gateway path. |
| No link check on an explicit resume | The process would start without the connector, and the connector list of a runtime with `deferredToolDiscovery: false` is fixed at start, so a user who then links waits for another restart. |
| Refuse impersonated messages on sessions with a linked connector | The owner accepted the risk instead, since this blocks support work on those sessions. |
| Block connector calls after impersonated input | The owner accepted the risk instead, and this needs new state carried through delegation. |
| Key connector tokens per user | The owner kept the environment key. It can be loosened later more easily than it could be added. |
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

[`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 25 applied the items of this subsection.

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
6. **Session create.** A session can need an account link and a connector link at once. Adopt the combined missing-links response that "CA-4" defines, `403 LINKS_REQUIRED` with `details.userLinks` and `details.connectors` whichever kinds of link are missing, so a user does not link, retry, and get refused again. Both checks run at the post-chain point of [RT-2](runtime-routing-design.md#post-chain-point), as "CA-4" states under "Placement", and the refusal of a `userCredential` binding on a pod that can serve another session reads the final pool there.

Leave the link record in the Token Service unchanged, under "Stored links".

**Owner decision: built-in memory goes, and impersonation on linked connectors is an accepted risk.** Apply these changes:

1. **"Limits under both deliveries", the Impersonation paragraph.** Remove the "Memory, unconditionally" bullet with no replacement. Do not extend the session input check to sessions holding a connector link.
2. **"Specification sections affected".** Remove "§9.4: memory writes refuse an impersonation bearer."
3. **"Tests", "Tier 4: integration".** Remove `lenny/memory_write` from the impersonation bearer test.
4. **"Current state".** The row "Other session input refuses an impersonation bearer" names memory writes as a fact about today's tree. Keep it until "CA-G" lands.

### `user-git-identity-design.md`

[`user-git-identity-design.md`](user-git-identity-design.md) revision 33 applied the item of this subsection.

**Owner decision: "CA-0" lands before UC-2, on which UG-1 depends.** Apply this change:

1. **"When the check runs".** The repository check refuses a session whose user has no link before the pod claim. That refusal is the combined missing-links response that "CA-4" defines, `403 LINKS_REQUIRED`, and the check runs at the post-chain point of [RT-2](runtime-routing-design.md#post-chain-point), as "CA-4" states under "Placement".

Leave these unchanged: decision 3 on connectors on the hosts of a VCS pool with `userAuthorization: required`, and the withdrawal of `hostLink`, both under "User tokens on a VCS host".

### `egress-and-git-design.md`

**"Residual risks and limits" and "Exfiltration controls".** Record that a §9.3 connector tool call runs on the gateway, so `netagent`, `lenny-egress`, and the bytes-out alert (that design's per-session alert on bytes sent through egress tunnels) do not see it, and that its arguments can carry source code to an external server. Default deny in "CA-3" and connector interceptors are the controls. If that design or a later one adopts a default for sessions holding private repository content, it decides whether connectors stay available there. This document does not settle that.

### `tenant-scoping-identity-design.md`

No edit. "CA-3" depends on **Delegated child's environment** and **Derive** under "TS-3: Session environment context and cross-environment sharing". Decision 14 settled on cross-environment sharing, and "CA-3" and "CA-7" follow it. If either rule changes, report it to this document, because a child's token lookup reads that field.

### `security-oversight-design.md`

**Owner decision: built-in memory goes.** Apply this change:

1. **"User credentials and request rules" under SO-2.** The **Impersonation** item states that the session input check refuses an impersonation bearer "on every memory write and delete". "CA-G" removes built-in memory and its routes and tools, so remove that clause and keep the refusal on `/v1/credentials` writes. Do not extend the check to sessions holding a connector link, because the owner accepted that risk (see "Limits").

### Other designs

[`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) and [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) need no change. The first has `§9.3` reader-page work of its own, a sentence and link on §9.3 and `docs/tutorials/using-connectors.md` that separate MCP connectors from outbound connectors, and a connector's probe uses the same dial path as its other calls.

## Specification changes

Each lands through `change-proposal`.

| Section | Change |
| :-- | :-- |
| §5.1 Runtime | Add `capabilities.deferredToolDiscovery`. |
| §7.1 Normal Flow | Add the link check to the post-chain point, which runtime routing's edit of step 3a places between the `PreRoute` chain and the pod claim. Rewrite **Connector state.** |
| §7.2 and §7.3 | Add the explicit resume check and implicit restart rule. |
| §8.2, §8.3, and §8.5 | Add the delegation check, deferral inheritance, and the child's environment. Remove the memory tools and their schemas. |
| §9.1 | Remove the memory tools. Add the bridge note. |
| §9.2 | State connector prompts: gateway origin, interceptor skip, deduplication, lifetime, and URL domain rule. |
| §9.3 | Replace the OAuth flow, add refresh and endpoint binding, and state default deny and the typed error. |
| §9.4 Memory Store | Replace with a successor pointer to the external pattern, naming the identifiers that left. |
| §4 System Components | Remove memory from the tenant-scoped table, and from local MCP server text if named. In §4.7.10 Deployment Model, state the `lenny-tools` delivery on sidecar pods and its absence in the embedded model. |
| §13.1 Pod Security | Add a `lenny-tools` volume rule beside **Runtime supervisor volume.**, including the stage container's UID, the read-only mount, and the `lenny-pod-security` enforcement. |
| §17 Deployment Topology | Add the digest-pinned `lenny-tools` image to the images an install renders, following how the chart names the supervisor image. |
| §12 Storage Architecture, including §12.8 | Remove the role, the erasure preflight and guidance, and the erasure steps. Add `externalDataNotErased`. |
| §15.1 and §15.4 | Add the link routes, `CONNECTOR_LINK_REQUIRED` on an explicit resume and, with `details.retryAfterSeconds`, on delegation, the error code `LINKS_REQUIRED` for the combined missing-links response at session creation and derive, with `details.connectors` and `details.userLinks`, either of which can be empty, `deferConnectors`, and `unlinkedConnectors`. Add the `-32010` error, the compliance check, and the stdio note. Remove memory helpers. |
| §16.1, §16.5, and §16.7 | Remove memory metrics and alerts. Add audit events `connector_link.created`, `connector_link.removed`, and `connector_link.endpoint_changed`, and the event `session.connector_unavailable`. |
| §18 Build Sequence | Remove memory from Phase 13 and Phase 17b, and place the connector work. |
| §22.4 No Built-In Memory Extraction | Rewrite, since Lenny no longer provides the `MemoryStore` interface. |
| §23 | Remove the memory claims. |
| §28.5.3 | Remove the memory tools from the `CH-MCP-PLATFORM` card. Update the `CH-MCP-CONNECTOR` card and add any new RPC placement. |

Documentation follows: `docs/tutorials/agent-memory.md` becomes the external pattern, `docs/tutorials/using-connectors.md` gets the real flow, and `docs/runtime-author-guide/platform-tools.md`, `integration-levels.md`, the operator guide, the glossary, `docs/reference/metrics.md`, and `TESTING.md` §13.36 lose the memory content.

## Tests

The tiers are the stages of the repository's test suite, which `lenny-test --tier` runs.

| Tier | Coverage |
| :-- | :-- |
| 1, unit | Link check table, including probe cache expiry and each status. The check reads `deferredToolDiscovery` from the chosen revision, at create, at delegation, and on resume. `WalkChain` skips interception for connectors. `AuthorizeConnector` default deny. Endpoint digest mismatch. Error mapping at each layer. Deferral inheritance. |
| 3, contract | `LINKS_REQUIRED` at create and derive over REST and MCP, with a connector link alone, an account link alone, and both missing, each response carrying both lists with an unused list empty. `CONNECTOR_LINK_REQUIRED` on an explicit resume and on `lenny/delegate_task`. `-32010` on `tools/list`, and `isError` on `tools/call`. |
| 4, integration | Each row of the "CA-6" table. Create, link, retry, then call. A `PreRoute` interceptor or route that moves a session or a delegated child to a runtime whose chosen revision has `deferredToolDiscovery: false` is refused at the post-chain point, with no pod claimed. Refresh on expiry. `invalid_grant` unlinks. Delegation refusal with `retryAfterSeconds`, retry, and deferral, with the export undone and budget returned. Child in the parent's environment finds the link. Derived session reuses a link. The stub server moves from 401 to 200 within the cache window. |
| 5, end to end on Kind | A pod on Kind has `/lenny-tools/lenny-mcp-stdio` on a read-only mount in a minimal runtime image and runs it there. The webhook refuses a read-write mount of the volume. An embedded pod has no such volume. |
| 7a, local load | Concurrent refresh yields one provider call per credential, extending `connector_oauth_refresh_race` to the real store. Prompt deduplication across replicas. |
| 8, chaos | Probe timeout on create and on resume. Token endpoint outage during refresh. Callback landing on a replica that does not hold the session. |
| 9, security | Link URL sent to another subject is refused, through both the converted §9.3 handlers in "CA-0" and the new routes. Missing cookie is refused. Impersonation bearer is refused. No URL or token appears in a pod, in a tool result, or at an intercepting parent. A child with a denied capability stays denied. |
| 10, conformance | The `deferredToolDiscovery` check, and the bridge's handshake in both nonce modes. |
| 11, documentation | Removed metrics and alerts leave no runbook or doc reference. |

Each test carries its `// spec:` annotation once the specification lands.

## Validation owed

| Item | Blocks | How |
| :-- | :-- | :-- |
| Codex behaviour on a failed `tools/list` and `list_changed` | A `deferredToolDiscovery: true` claim for Codex. The path for `deferredToolDiscovery: false` needs no answer. | Run a current, logged-in Codex headless, through the bridge, against a stub that fails `tools/list` and then notifies. Claude Code 2.1.317 is measured under "CA-9". |
| Bridge transport in a pod, with abstract sockets and `SO_PEERCRED` | "CA-9" | Round trip in the conformance suite, for each CLI. Claude Code passed on macOS with filesystem sockets. |
| Generated MCP configuration under `preConnect` | Stock CLI on warm pools | Launch a warm pod and bind a session. |
| Added startup from `lenny-tools-stage` on a real pod | Sizing warm-pool refill. About one second is measured on stand-in pods under "CA-9". | Measure a controller-rendered pod with and without the init container, under gVisor and Kata, including the image's first pull. |
| Interaction store lookup by tenant, user, connector, and environment | "CA-1" step 6 | Read `interactionstore` at proposal time. A new index may be needed. |
| OIDC availability on the start page in `apiKey` and dev modes, where a user authenticates with an API key or, under `global.devMode=true`, with no credential, rather than through an OIDC login | "CA-0" | Settle it in that proposal, since the start page is shared with UC-2. |
| MCP resource indicator requirement | "CA-2" | Read the MCP authorization page, and test a server that enforces audience. |

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| CA-0 | Shared OAuth link flow ([CA-0](#ca-0-shared-oauth-link-flow)) in `pkg/connectoroauth`, with today's §9.3 handlers converted to it. Closes the account-link defect. | Nothing. It lands first, and [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) depends on it. |
| CA-A | Connector routes ([CA-1](#ca-1-link-flow)), and the removal of the admin OAuth routes. | CA-0 |
| CA-B | Refresh, lease, and endpoint binding ([CA-2](#ca-2-token-lifecycle)). | CA-A |
| CA-C | Default deny and environment on children and derived sessions ([CA-3](#ca-3-connector-access-and-environment)). | The [tenant-scoping decision](tenant-scoping-identity-design.md#decisions) |
| CA-D | Typed error and connector prompts ([CA-5](#ca-5-propagating-the-failure)). | CA-A |
| CA-E | Link check at create, derive, and external adapters, plus `deferConnectors` and the combined missing-links response ([CA-4](#ca-4-link-check)). | CA-A, CA-C, and the interceptor contract proposal of [`runtime-routing-design.md`](runtime-routing-design.md#proposal-split-and-order), which adds the post-chain point |
| CA-F | Resume and restart rules ([CA-6](#ca-6-resume-and-restart)), and delegation ([CA-7](#ca-7-delegated-children)). | CA-D, CA-E |
| CA-G | Memory removal ([CA-10](#ca-10-removing-built-in-memory)) and the external pattern documentation. | CA-A through CA-F, so the documented replacement works when memory goes |
| CA-H | Stdio bridge, the `lenny-tools` image, init container, volume, and webhook rule ([CA-9](#ca-9-stdio-bridge)). | Ships with the Claude Code and Codex integrations |
| CA-I | `deferredToolDiscovery` true path and `list_changed` ([CA-8](#ca-8-runtime-capability-and-late-tools)). The field and its `false` default land in CA-E. | The stock CLI spike, or a runtime asking for it |

[`user-credential-delivery-design.md`, "Defects found while verifying"](user-credential-delivery-design.md#defects-found-while-verifying) rates the account-link defect High wherever a connector is a confidential client, which is why CA-0 comes first. CA-C's default deny and the child environment fix close a capability-filter bypass that exists today, and they also stand alone.

## Sources

- MCP specification 2025-11-25: [Authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization), [Elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation), and [Tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).
- [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp).
- [Codex MCP documentation](https://developers.openai.com/codex/mcp), which redirected on 2026-11-03 to `learn.chatgpt.com/docs/extend/mcp`. Most Codex facts here came from a summary of that page.

## Review history

This section records what each revision changed. The current text above supersedes any statement here that differs from it.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 2 recorded the owner's answers to the open decisions and added "Instructions for reviewers of other designs".
- Revision 3 settled the last one, the link check before an explicit resume.
- Revision 4 recorded the Claude Code bridge spike under "CA-9".
- Revision 5 delivered the bridge to every sidecar `type: agent` pod.
- Revision 6 recorded the startup measurement and the owner's choice of a separate init container.
- Revision 7 changed only pointers after the user credential documents split.
- Revision 8, dated 2026-11-07, followed a pass for a reader new to the project. It added "Terms from other designs and the platform", "Labels from other designs", "High-level design", the evidence sources under the tag table, and terms for the probe, the link URL, the endpoint digest, the stdio bridge, `deferredToolDiscovery`, the account-link attack, the subject, the pool-level `required` flag, user erasure, the senses of lease, the adapter authentication modes, and the runtime wrapper. It moved "CA-0" to its own section ahead of "CA-1", moved the revision history from the status line to this section, named the setting behind each `true` and `false` runtime, linked each citation of another design to the file and heading it names, glossed terms used once, such as depth suppression and `connectortools.Bridge`, and named the test tiers. It changed no rule.
- Revision 9, dated 2026-11-08, corrected the citations of the **Delegated child's environment** and **Derive** rules in "TS-3: Session environment context and cross-environment sharing", the treatment of decision 14 as unsettled in "CA-3", "CA-7", and the reviewer instructions, the heading cited for the user credential design's Impersonation paragraph, the point at which Coder checks external account links, and the condition for nonce-only mode, that a validation pass found, and changed no rule. Decision 14 settled on cross-environment sharing, under which a crossing child takes the target Environment and needs a link there, and the revision states that case in "High-level design" and CAR-6 as well.
- Revision 10, dated 2026-11-08, updated the text after a re-read of `user-credential-delivery-design.md`, `user-git-identity-design.md`, `tenant-scoping-identity-design.md`, `tenant-outbound-connector-design.md`, and `egress-and-git-design.md` at their current revisions, and changed no rule. It wrote "environment" in lowercase, as the tenant scoping design and the other designs write it. It removed replay as a session creation path from the "Derive" term, "Environment on a derived session", the paths the link check covers under "Placement", and the CA-E row, because `session-derive-design.md` merges replay into derive, as the tenant scoping design's "Derive" term states. It also stated that `netagent` sits in an agent pod that uses the local egress endpoint, as the egress design's "Terms" defines it.
- Revision 11, dated 2026-11-09, applies the design sync review of 2026-11-08, the owner's answer to its question Q6, and the reviewer instructions of [`runtime-routing-design.md`](runtime-routing-design.md) revision 7, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 3, and [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2. "Placement" under "CA-4" and "Check at delegation" under "CA-7" move the link check to the post-chain point of RT-2, after the single `PreRoute` chain and revision selection, and read `capabilities.deferredToolDiscovery` from the chosen revision. "CA-6" reads it from the revision a resume runs. The "Experiment routing" term and the §7.1 step 3a wording are gone. "CA-4" defines the combined missing-links response, with `details.userLinks` beside `details.connectors`, and names the per-version forms of MP-3 for MCP clients, which settles Q6 as the tool error `details`. "CA-5" and "Alternatives rejected" rest the rejection of `-32042` on CAR-7. "External memory pattern" names F-9.3.19 as a prerequisite of its example. "Instructions for reviewers of other designs" gains a security-oversight item for its memory clause, and the lease cross-note in "CA-2" states the shared form. The tree recovery citation, the test tier gloss, and the "Both durations" sentence are corrected, and CA-E depends on runtime routing's interceptor contract proposal. The "CA-0" table states the start page's login through the tenant scoping design's browser login and the cookie's `HttpOnly` and `SameSite=Lax` attributes, which the user credential design's flow needs from the shared flow.
- Revision 12, dated 2026-11-09, applies the owner's decision N1 of that date, settled in the owner's word "ok", and changes this rule. The combined missing-links response at session creation always carries its own code, `403 LINKS_REQUIRED`, with `details.userLinks` and `details.connectors`, either of which can be empty. This replaces the rule that chose `CONNECTOR_LINK_REQUIRED` or `USER_LINK_REQUIRED` by which kind of link was missing. The rule applies on every creation path, derive included, and MCP clients receive the same code in the tool error `details`. A refusal of a single kind outside creation keeps its code: "CA-6" states `CONNECTOR_LINK_REQUIRED` for an explicit resume, and "CA-7" keeps it for a delegated child. "High-level design", "Terms", "CA-4", the worked example, "Instructions for reviewers of other designs", the §15.1 row of "Specification changes", and the tier 3 tests follow.
