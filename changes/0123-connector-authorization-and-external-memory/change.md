# Connector authorization and external memory

Status: `Draft`, revision 21, 2026-11-19. Programs: [v1](../programs/v1.md).

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

> **Revision 17.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15: "Yes, let's drop connectors." This design now keeps two live parts: CA-0, the shared OAuth link flow that account links use, and CA-10, the removal of built-in memory, whose external pattern reaches a memory service through egress. The problem statement, the scope, the terms, and the current state below are kept as the record of the connector design and are not edited further.

Lenny carries a built-in memory feature: the `MemoryStore` role, one of the store interfaces of [§12.2 Storage Roles](../../spec/12_storage-architecture.md#122-storage-roles), a Postgres and pgvector backend (pgvector is the Postgres extension for vector similarity search), the `lenny/memory_write` and `lenny/memory_query` platform tools, and `/v1/sessions/{id}/memory` routes. It makes Lenny responsible for retrieval quality, embeddings, capacity, retention, tenant isolation, and mandatory erasure of a data type unrelated to running agent sessions. Its default embedder, `HashingEmbedder`, measures token overlap, and dedicated memory products exist.

Sessions still need a way to reach state that outlives a pod. MCP connectors are the generic mechanism for this, and a deployer can register any MCP memory server as one. "Terms from other designs and the platform" below defines connectors and the other platform terms this section uses. That route does not work today, and the reasons are general to every connector that requires per-user authorization:

- An unlinked user's call fails with `unexpected status 401` and no prompt, although [§9.3](../../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) and `docs/tutorials/using-connectors.md` both describe a prompt **[measured]**.
- An expired token is sent as it is and never refreshed **[measured]**.
- A session with no delegation policy is offered every active connector in the tenant **[source]**.
- A stock coding CLI cannot dial the adapter's connector sockets **[source]**.
- The OAuth callback is open to an account-link attack, recorded in [`0151-user-credential-delivery`, "Defects found while verifying"](../0151-user-credential-delivery/change.md#defects-found-while-verifying) **[source]**.
- A delegated child loses its parent's environment, so a token linked under that environment is not found **[measured]**.

This design removes built-in memory and fixes the connector path so the external route is usable for any connector.

## Scope

> **Record.** Kept since revision 17 as the record of the connector design, which [`0124-connector-removal`](../0124-connector-removal/change.md) withdraws under the owner's decision of 2026-11-15: "Yes, let's drop connectors." The live scope is CA-0, the shared OAuth link flow that account links use, and CA-10, the removal of built-in memory, with the external memory pattern that reaches a memory service through egress.

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

[`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) owns account links and tokens delivered to pods, and [`0152-user-git-identity`](../0152-user-git-identity/change.md) configures them for repository hosts. A connector token stays on the gateway path, and its use by the gateway remains the one way it is spent. "§9.3" means [§9.3 Connector Definition and OAuth/OIDC](../../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) throughout.

## Terms

Parts of the design carry the labels CA-0 to CA-10, requirements carry the label CAR-n, and security properties carry the label CP-n.

> **Record.** Kept since revision 17 as the record of the connector design, which [`0124-connector-removal`](../0124-connector-removal/change.md) withdraws. This mark covers "Terms from other designs and the platform" and "Labels from other designs" as well. The entries for the shared OAuth link flow, the account-link attack, the subject, user erasure, built-in memory, the semantic cache, the Token Service, the account link, the impersonation bearer, and `netagent` and `lenny-egress` serve the live parts, CA-0, CA-10, and "External memory pattern", apart from their clauses about connectors. [`0124-connector-removal`, "Terms"](../0124-connector-removal/change.md#terms) defines the identity assertion, the deployment key set, the credential binding, and the `userCredential` binding that "External memory pattern" uses.

| Term | Meaning |
| :-- | :-- |
| Connector link | A stored connector token for one tenant, connector, user, and environment. The gateway holds it as a row in its connector credential store, `connectorcredstore`. |
| Linked user | A user who holds a usable connector link for the session's environment. |
| Unlinked user | A user who holds no usable connector link for that connector and environment, even though the connector is registered and permitted. |
| Shared OAuth link flow | The hardened browser flow that links a person's account at an OAuth2 provider. "CA-0" moves it into `pkg/connectoroauth`, where connectors and the account links of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) both use it. |
| Link URL | A Lenny URL that starts the shared OAuth link flow for one subject, connector, and environment. The user opens it in a browser. See "CA-1". |
| Endpoint digest | A digest of a connector's `mcpServerUrl`, `authorizationEndpoint`, and `tokenEndpoint`, recorded on each connector link. A link whose digest no longer matches is treated as absent. See "CA-2". |
| Probe | The call the gateway makes to a connector's server with no token, a `tools/list` with an empty bearer, to learn whether listing tools needs authorization. See "CA-4". |
| Link check | The evaluation in "CA-4" that decides whether a connector needs a link before a runtime starts. |
| Combined missing-links response | The single refusal at session creation, `403 LINKS_REQUIRED`, that reports every connector link and account link the user still needs, which "CA-4" defines under "Combined missing-links response" and [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md#flow) shares. |
| Deferred connector | A connector the caller chose to run without for a session and its descendants. The request field `deferConnectors` names it. |
| `deferredToolDiscovery` | The runtime capability `capabilities.deferredToolDiscovery`, which "CA-8" adds to the Runtime resource of §5.1. A runtime with `deferredToolDiscovery: true` keeps a connector server whose `tools/list` failed and lists again when the server says its tools changed. The default is `false`. |
| Explicit resume | `POST /v1/sessions/{id}/resume`, or the `lenny/resume_session` tool, which posts to the same route. |
| Implicit restart | A new runtime process with no client request waiting on it: the retry driver, message-triggered restoration of a released pod, and tree recovery. |
| Connector prompt | A URL-mode elicitation with `initiator_type: connector`, raised by the gateway. |
| Stdio bridge | The stdio form of the reference MCP bridge of [RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge), `lenny-mcp-bridge --server <server>`: a small static binary that a CLI starts as a stdio MCP server, and which carries one tool server's calls to the adapter as tool frames on a tool connection of its slot. It ships with the runtime SDKs and as a release binary. Earlier revisions called it `lenny-mcp-stdio` and delivered it through a `lenny-tools` init container, which RP O4 (a) withdrew. See "CA-9". |
| Built-in memory | The memory feature Lenny carries today under §9.4 Memory Store, which "CA-10" removes. |
| Account-link attack | The defect in today's §9.3 OAuth handlers that [`0151-user-credential-delivery`, "Defects found while verifying"](../0151-user-credential-delivery/change.md#defects-found-while-verifying) records as F-9.3.18. The gateway returns an authorization URL to any authenticated caller, and the callback checks no bearer and no cookie, so an attacker who sends the URL to a victim receives the victim's token under the attacker's own user. "CA-0" closes it. |
| Subject | The Lenny user a link flow is for. The flow names the subject, the start page requires the logged-in user to be that subject, and the token is stored under it. A token's own `sub` claim, which names the account at the provider, is a different thing. |
| Pool-level `required` flag | A flag, proposed during the design discussion, that would have marked a connector as required on a pool. The owner replaced it with a live check of whether a connector requires authorization to list its tools. The specification has no such field. It is unrelated to a VCS pool with `userAuthorization: required`. |
| User erasure | The `DeleteByUser` erasure job of [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces), which calls each store's `DeleteByUser` and records an erasure receipt in the audit trail. The `tokens` eraser is the step that deletes a user's stored tokens, connector tokens included. "External memory pattern" adds the receipt field `externalDataNotErased`. |
| Lease | A word with three senses here, each qualified where it appears. A refresh lease is a lease on the connector credential row (`refresh_lease_holder` and `refresh_lease_until`) that lets one gateway replica refresh a token at a time. A delegation lease records what a child session receives from its parent. A §8.6 lease extension asks the human to extend a session's budget, and its prompt (`leaseelicit`) is blocking, unlike a connector prompt. The gateway package `leasecontrol` hosts both the lease extension and the gateway's connector tool service for the adapter. |

### Terms from other designs and the platform

This design builds on mechanisms that other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| MCP | The Model Context Protocol, a JSON-RPC protocol between a client, here an agent runtime, and a server that offers tools. `initialize` opens a connection, `tools/list` returns the server's tools, `tools/call` calls one, and `notifications/tools/list_changed` tells the client that the list changed. |
| Connector | An external MCP server that a deployer registers as a `ConnectorDefinition` ([§9.3](../../spec/09_mcp-integration.md#connectordefinition-as-first-class-resource)). Its optional `auth` block names the OAuth2 endpoints and client the gateway uses to obtain a user's token. The gateway makes every call to the server on a session's behalf, through code this document calls the invoker (`invoker.go`). The invoker's function `bearerFor` looks up the user's token for each call. |
| OAuth2, OIDC, and PKCE | OAuth2 is the protocol through which a provider issues a user's access token and refresh token to a client. OpenID Connect (OIDC) is the login layer on OAuth2 that Lenny uses to sign a user in. Proof Key for Code Exchange (PKCE) binds an authorization code to the client that started the flow. |
| Environment | A tenant's named project context ([§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)), with members, selectors for the runtimes and connectors it offers, and a default delegation policy. A session records the environment it runs in. |
| Delegation policy | A `DelegationPolicy` resource ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)) that lists what a session may delegate to or reach, including targets of type `connector`. A policy attaches to a runtime or serves as an environment's default. `AuthorizeConnector` checks a connector against two policy layers: the runtime-level policy and the environment's default policy. A layer resolves when its reference names an active policy, and a connector is permitted only when every resolved layer permits it. |
| Capability filter and capability snapshot | An environment's `connectorSelector` ([§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)) allows or denies connector tools by capability, such as `read` or `write`. This document calls it the §10.6 filter. The capabilities come from a snapshot, `conn.ToolCapabilities`, that §5.1 infers from a server's MCP tool annotations at registration and on refresh. |
| Runtime and pool | A runtime ([§5.1](../../spec/05_runtime-registry-and-pool-model.md#51-runtime)) is a registered agent program that runs agent sessions. Every runtime is an agent runtime, because [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) removes the `type` field, `type: mcp`, and service mode. A pool keeps warm pods for a runtime, ready for a session to claim. |
| Runtime capabilities | The behaviours a runtime declares and reports in its handshake, such as `checkpoint` or `deferredToolDiscovery`, which replace the integration levels Basic, Standard, and Full ([RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies)). |
| Runtime adapter and adapter manifest | The adapter ([§4.7](../../spec/04_system-components.md#47-runtime-adapter)) is Lenny's process in each agent pod. It talks to the gateway and carries the runtime's tool calls to it as frames ([RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). It writes the adapter manifest, `/run/lenny/adapter-manifest.json`, on the slot's run volume, and `session_start` lists the tool servers the session may use. |
| Adapter authentication modes | The adapter authenticates each client on its sockets in one of two modes. By default it checks the connecting process's user ID through `SO_PEERCRED`, the kernel's report of a Unix socket peer's credentials, and requires the manifest nonce. In nonce-only mode the nonce alone authenticates, and the adapter adds a per-connection challenge, `adapterChallenge`, that the client must answer. A runtime with `requireSoPeercred: false`, on a pool that sets `acknowledgeNonceOnlyAuth`, selects nonce-only mode, for an isolation environment in which the adapter's `SO_PEERCRED` self-test fails, which §4.7 expects for gVisor only if validation confirms a divergence ([§4.7](../../spec/04_system-components.md#47-runtime-adapter), [§5.1](../../spec/05_runtime-registry-and-pool-model.md#51-runtime)). |
| Tool servers and tool frames | A session's tool servers are `platform`, which serves Lenny's platform tools such as `start_child_session`, and `connector:<id>` for each connector. A runtime calls one with a `tool_call` frame that names the server, on its runtime connection or on a tool connection of its slot, and the adapter forwards each call to the gateway ([RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). `session_start` lists the session's tool servers in `availability.toolServers` ([RP-4](../0139-runtime-protocol/change.md#rp-4-availability)). These replace the adapter's per-connector abstract sockets and its platform MCP server, whose channels were `CH-MCP-CONNECTOR` and `CH-MCP-PLATFORM` ([§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod)). |
| `availability_changed` and `UpdateAvailability` | The frame the adapter writes on the slot's runtime and tool connections when a session's availability changes, and the gateway's unary RPC on `CH-ATTACH` that triggers it ([RP-4](../0139-runtime-protocol/change.md#rp-4-availability)). |
| `LNK-POD-GRPC` and `CH-HOLDERFORWARD` | `LNK-POD-GRPC` is the gRPC connection from a gateway replica to a pod's adapter. `CH-HOLDERFORWARD` forwards a request that reached one gateway replica to the session's coordinating replica ([§28.3](../../spec/28_communication-channels.md#283-registers)). |
| Gateway replica | One instance of Lenny's API gateway. One replica coordinates each session under a lease. |
| Stock CLI and SDK-based runtime | A stock CLI is an agent command-line tool used as published, such as Claude Code (`claude`) or Codex, with no Lenny code inside it. An SDK-based runtime uses a Lenny runtime SDK, which speaks the adapter's handshake itself. |
| Runtime wrapper, and the Claude Code and Codex integrations | The reference runtimes `claude-code` and `codex` ([§26.3](../../spec/26_reference-runtime-catalog.md#263-claude-code), [§26.5](../../spec/26_reference-runtime-catalog.md#265-codex)) run the stock CLI in a Lenny pod. Each runtime's `command` is a wrapper, such as `lenny-claude-code-adapter`, that prepares the CLI's configuration and launches it. "The Claude Code and Codex integrations" means these runtimes. |
| Agent pod and slot container | An agent pod has the adapter container, `netagent`, and one slot container per slot, each running one session's runtime process under its own UID ([PL-1](../0118-agent-pod-layout/change.md#pl-1-pod-blueprint)). The embedded deployment model is removed. |
| Runtime supervisor | A platform binary that is the first process of every slot container and launches the runtime's command, once per session, at bind. The init container `lenny-bin-stage` copies it into `/lenny/bin`, read-only and exec-only ([PL-1](../0118-agent-pod-layout/change.md#pl-1-pod-blueprint), [PL-3](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout)). |
| Elicitation and URL mode | An MCP request in which a server asks the human for input. A URL-mode elicitation asks the user to open a URL, such as a login page. [§9.2](../../spec/09_mcp-integration.md#92-elicitation-chain) passes an elicitation hop by hop up the delegation tree to the human-facing edge, the client. The interaction store (`interactionstore`) holds pending elicitations, and `maxElicitationWait`, 600 seconds by default and set per pool, bounds how long one stays pending. |
| Intercepting parent | A parent session configured to answer its children's elicitations itself rather than forward them to the human. `WalkChain` in `pkg/elicitation` stops at the first intercepting ancestor. |
| Interceptors | Deployer hooks the gateway calls at named phases ([§4.8](../../spec/04_system-components.md#48-gateway-policy-engine)). `PreConnectorRequest` and `PostConnectorResponse` see each connector call and its response. `PreRoute` runs before a session's runtime and pool are final and can rewrite them. |
| `PreRoute` chain, revision, and post-chain point | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) runs the `PreRoute` interceptors as one chain on every creation path, `kind: create`, `kind: delegate`, and `kind: derive`, and a `MODIFY` from any of them can change the runtime and pool. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-2-selection) then picks a revision of the final runtime: an immutable, numbered copy of the runtime's versioned fields, `capabilities` among them. The post-chain point ([RT-2](../0141-runtime-routing/change.md#post-chain-point)) follows, and every check that depends on the runtime, the revision, or the pool runs there on the chosen revision, before the pod claim. |
| Delegation | A session can start child sessions through the platform tool `start_child_session`, formerly `lenny/delegate_task` ("MD-23" of [`0135-message-delivery`](../0135-message-delivery/change.md)), and they form a delegation tree under a root session. A delegation lease records what a child receives from its parent. |
| Derive | Derive (`POST /v1/sessions/{id}/derive`) starts a new root session from an existing session, from its latest snapshot or from its stored plan, under [`0144-session-derive`](../0144-session-derive/change.md). That design also removes the former replay route, `POST /v1/sessions/{id}/replay`, which it merges into derive. |
| Session states and restart paths | A `suspended` session has paused, either holding its pod or having released it. The retry driver restarts a session's runtime after a retryable failure, through `resume_pending` and `resuming`, and a session whose retries are exhausted waits in `awaiting_client_action` ([§7.3](../../spec/07_session-lifecycle.md#73-retry-and-resume)). Tree recovery restores the sessions of a delegation tree after a failure ([§8.10](../../spec/08_recursive-delegation.md#810-delegation-tree-recovery)). |
| External protocol adapters | Front ends that serve other client protocols, such as MCP at `/mcp` and OpenAI Chat Completions at `/v1/chat/completions`, and create sessions on the client's behalf. Today they run in the gateway (§15), and each declares an `AdapterCapabilities` record, whose `supportsElicitation` says whether the adapter can show an elicitation to its client. Under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-1-the-client-contract), Lenny serves a REST client API alone, each adapter runs from its own repository in front of that API and creates sessions through its REST routes, and `AdapterCapabilities` leaves. A session's creator states whether its client can answer input with `inputPolicy` ([R12](../0138-rest-only-client-api/change.md#ro-2-rest-additions)). |
| Token Service | The Lenny service that stores user and provider credentials and mints short-lived tokens from them ([§4.3](../../spec/04_system-components.md#43-token-service)). Connector tokens are outside it: the gateway stores and decrypts them. |
| Account link | A person's stored authorization at an OAuth2 provider under [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md), kept in the Token Service and created through `/v1/user-links`. It is separate from a connector link. |
| Impersonation bearer | A token a `platform-admin` obtains through `/v1/admin/impersonation` to act as a user ([§13.3](../../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). |
| `netagent` and `lenny-egress` | `netagent` is the egress proxy container that the egress design adds to an agent pod that uses the local egress endpoint, and `lenny-egress` is the central egress proxy through which agent traffic leaves the cluster. Both belong to [`0127-egress-and-git`](../0127-egress-and-git/change.md). Connector calls run on the gateway and pass through neither. |
| `lenny-pod-security` | The admission webhook that refuses an agent pod spec breaking the pod rules of [§13.1](../../spec/13_security-model.md#131-pod-security). |
| `lenny-compliance` | The runtime adapter conformance harness, which runs the battery of [§15.4.6](../../spec/15_external-api-surface.md#1546-conformance-test-suite). |
| `lenny-ctl` | Lenny's command-line client ([§24](../../spec/24_lenny-ctl-command-reference.md#24-lenny-ctl-command-reference)). |
| Semantic cache | An optional, per-pool cache of model responses keyed by embedding similarity ([§4.9, "Semantic Caching"](../../spec/04_system-components.md#semantic-caching)). Its code, `semanticcache`, imports the memory store's embedder. |
| F-n.n.n | An identifier such as F-4.3.1 names a finding filed in `BUILD-GAPS.md` at the repository root, the audit of gaps between the specification and the code. F-4.3.1 is the finding that the gateway holds Key Management Service (KMS) decrypt rights in its own process, so it can decrypt stored tokens itself. |
| Kind, gVisor, and Kata | Kind runs a Kubernetes cluster in Docker containers for testing. gVisor and Kata are sandboxed container runtimes that Lenny's isolation profiles use. |
| Test tiers | The stages of the repository's test suite, which `lenny-test --tier` runs. The "Tests" table names the tiers this design uses. |

### Labels from other designs

This document cites proposals of other designs by label and links each citation to its definition.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| UC-2 | [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) | The `oauth2Link` source and account linking, whose hardened link flow "CA-0" shares. |
| UG-1 and UG-2 | [`0152-user-git-identity`](../0152-user-git-identity/change.md) | [UG-1](../0152-user-git-identity/change.md#ug-1-vcs-link-kinds) version control system (VCS) link kinds, the VCS layer's proposal that depends on UC-2, and [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) repository authorization, whose check refuses a user with no link. |
| RT-2 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | The interceptor contract: the single `PreRoute` chain and the post-chain point. |
| RV-1, RV-2, and RV-6 | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) | [RV-1](../0140-runtime-revisions/change.md#rv-1-revisions) the field classes, under which `capabilities` is versioned, [RV-2](../0140-runtime-revisions/change.md#rv-2-selection) revision selection at session creation, and [RV-6](../0140-runtime-revisions/change.md#rv-6-sessions-over-time) the revision a resumed or delegated session runs. |
| RO-1, RO-4, R4, and R12 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) | [RO-1](../0138-rest-only-client-api/change.md#ro-1-the-client-contract) the client contract, under which Lenny serves REST alone and every protocol adapter creates sessions through the REST routes; [RO-4](../0138-rest-only-client-api/change.md#ro-4-authorization-of-adapter-requests) the authorization of adapter requests; and, among the REST additions of [RO-2](../0138-rest-only-client-api/change.md#ro-2-rest-additions), R4 the pending input lists and R12 the input policy at creation. |
| MP-3, MP-5, and MP-6 | [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md) | [MP-3](../0134-mcp-protocol-upgrade/change.md#mp-3-elicitation) elicitation, including the per-version forms of the combined missing-links response, [MP-5](../0134-mcp-protocol-upgrade/change.md#mp-5-intra-pod-servers) the move of the adapter's intra-pod servers to the supported MCP versions, and [MP-6](../0134-mcp-protocol-upgrade/change.md#mp-6-outbound-clients) the connector client, which requests and forwards a connector's progress notifications. |

## High-level design

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. CA-0 and CA-10 stay, and "External memory pattern" now states the egress pattern.

Lenny sessions can call tools on external MCP servers that a deployer registers as connectors. Many such servers need each user's own authorization, and that path does not work today: a user who has not linked an account gets an unexplained failure, tokens are never refreshed, a session with no policy can reach every connector, and stock agent command-line tools cannot reach connectors at all. Lenny also carries a built-in memory store, which makes it responsible for a data product unrelated to running agent sessions.

This design removes the built-in memory and makes connectors that need per-user authorization usable, so a deployer who wants memory registers an external memory server as a connector. A user links an account on a browser page, before or during a session, and the gateway keeps the token, refreshes it, and spends it only at the server it was linked for. Before a session starts, Lenny checks whether each permitted connector needs a link. It either refuses the session with a link to complete for each such connector, or starts the session and tells the agent's runtime when a connector needs authorization. The main limits are that connector traffic runs on the gateway, outside the pod egress controls, so a granted connector can carry a session's data out, and that a platform admin impersonating a user can spend that user's connector token.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Shared link flow and connector routes (CA-0, CA-1).** CA-0 moves the hardened OAuth flow of [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) into a shared package and converts today's §9.3 handlers to it, which closes the account-link attack. CA-1 adds the `/v1/connector-links` routes and `lenny-ctl connector link`, so a user links a connector for an environment before any session exists, from any client.

**Token lifecycle (CA-2).** The gateway refreshes a connector token near expiry under a lease on the connector credential row, deletes it when a refresh returns `invalid_grant`, the OAuth error that means the refresh token is no longer valid, and treats it as absent once the connector's endpoint digest changes.

**Access and environment (CA-3).** A session reaches a connector only when a delegation policy grants it. A delegated child carries its parent's environment, and a derived session its source's, so each finds the same links. A child whose delegation crosses a sharing path instead carries the target environment, and needs a link there.

**Link check (CA-4).** At session creation, at the post-chain point that follows the `PreRoute` chain and revision selection, the gateway evaluates each permitted connector as `open`, `linked`, `needs_link`, or `unknown`, and sends a probe only for a user with no token. A session whose chosen revision has `deferredToolDiscovery: false` is refused with `403 LINKS_REQUIRED` and a link URL for each connector that needs one, unless the caller defers that connector with `deferConnectors`. The same refusal reports the account links the user lacks, and it carries that code whichever kinds of link are missing.

**Failure propagation and connector prompts (CA-5).** An authorization failure at the external server reaches the runtime as a typed MCP error on `tools/list`, or as an `isError` result on `tools/call`. The gateway raises one connector prompt per delegation tree and connector to the human, with a Lenny link URL that no pod or agent sees.

**Resume, restart, and delegation (CA-6, CA-7).** An explicit resume runs the link check. An implicit restart never fails for a missing link: it leaves the connector out of the manifest and prompts the user. A delegation to a child that would need a link is refused with `details.retryAfterSeconds`, and the parent retries after the user links, or defers the connector.

**Late tools (CA-8).** A runtime that declares `deferredToolDiscovery: true` keeps a connector whose listing failed and lists again on `availability_changed`, which the adapter writes once a link completes, and which the reference bridge turns into `notifications/tools/list_changed` for a stock CLI.

**Stdio bridge (CA-9).** The reference bridge's stdio form, `lenny-mcp-bridge`, which ships with the runtime SDKs and as a release binary inside the runtime image, lets a stock CLI reach the session's tool servers as stdio MCP servers.

**Memory removal and the external pattern (CA-10).** The `MemoryStore` role, its tools, routes, metrics, and table go. Project knowledge stays in the repository, and user-level memory is a deployer-run MCP server registered as a connector.

```
 Browser ──(link URL, CA-0, CA-1)───────────> Gateway ──(code exchange)──> OAuth provider
                                                 │ connector link: tenant, connector, user, environment
 Client ──(create session, link check, CA-4)─> Gateway
                                                 ▲
                                                 │ tool calls, and the typed error back (CA-5)
 Pod: stock CLI ──(stdio)──> lenny-mcp-bridge ──(tool connection)──> adapter

 Gateway ──(user's token attached, refreshed under CA-2)──> external MCP server
```

## Requirements

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. CAR-1 stays, met through egress ("External memory pattern"). CAR-2 to CAR-10 are withdrawn.

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

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The threat model of [`0124-connector-removal`](../0124-connector-removal/change.md#threat-model) covers operator services reached through egress.

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

> **Record.** Kept since revision 17 as the record of the connector code that [`0124-connector-removal`](../0124-connector-removal/change.md) removes. Each row states what the tree held when this design read it.

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

## Detailed design

### CA-0: Shared OAuth link flow

> **Live.** Since revision 17, 2026-11-15, CA-0 stays under [`0124-connector-removal`](../0124-connector-removal/change.md), because account links under UC-2 use the shared flow. Today's §9.3 handlers, which it converts, leave with §9.3, so CA-0 lands the shared flow for UC-2 and closes the account-link defect wherever the §9.3 handlers remain until CR-A of the removal design lands.

The account-link attack exists in today's code, and tying its fix to either feature would leave it open until that feature ships. The owner therefore decided that the shared flow is its own proposal and lands first. It takes the hardened sequence in [`0151-user-credential-delivery`, "Flow"](../0151-user-credential-delivery/change.md#flow) in UC-2, moves it into `pkg/connectoroauth`, and converts today's §9.3 handlers to it, which closes the defect with no other feature.

| Piece | Owner |
| :-- | :-- |
| One-time flow identifier, with a 10-minute lifetime, naming a subject | Shared |
| Start page: OIDC login at the tenant's identity source, through the "Browser login" of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md), whose shared resolver supplies the tenant and subject, and refusal when the logged-in tenant and subject differ from the flow's | Shared |
| A nonce in a `__Host-` cookie (a cookie prefix a browser accepts only over HTTPS, with path `/` and no `Domain`), set `HttpOnly`, `Secure`, and `SameSite=Lax` so the browser sends it on the provider's top-level redirect back, PKCE, and an OAuth `state` parameter, which the provider returns unchanged, signing the flow identifier and the nonce's hash | Shared |
| Callback validation: signature, single consumption, cookie nonce, and the `iss` issuer parameter of RFC 9207 where the provider returns it | Shared |
| A hook that receives the validated code and verifier | Shared interface |
| What the start page displays, the per-provider callback path, and any digest compared on return | Supplied by each caller |
| Code exchange and token storage | UC-2: the Token Service, with identity binding, the UC-2 rule that ties the provider account to the Lenny user, and a confirmation page. Today's §9.3 handlers, until CR-A of the removal design deletes them: the gateway, into the connector credential row. |

**Open decisions on CA-0.** Three questions on CA-0 wait on the owner in `decisions.md`. O1 asks what step 0123.S1 builds and when it lands, because the "Browser login" the start page uses lands in 0149.S6 and 0124.S1 deletes the §9.3 handlers this section converts. O2 asks what the start page does in a deployment with no OIDC browser login. O5 asks how long a link URL stays usable when a link prompt shows it for longer than the flow identifier's 10 minutes.

**Flow state.** The flow record lives in the Redis state store of the shared package. It is written once with a 10-minute TTL and consumed atomically at the callback, and a replayed `state` finds a tombstone. Its key is the §12.4 key that CR-1 of the removal design states for the shared flow in place of the connector row `conn:oauth:state:{state}`, and it keeps that row's platform-scope exception to the tenant prefix rule. Any replica serves the callback, because the record is in Redis rather than on the replica that served the start page. When the store is unreachable at the start page or at the callback, the flow is refused. The package carries the name that CR-1 of the removal design gives it in place of `pkg/connectoroauth`.

**Several flows in one browser.** The cookie's name carries the flow identifier, in the form `__Host-lenny-link-<flow-id>`, and the callback reads the cookie named for the flow identifier that `state` signs. Two link URLs from one combined missing-links response, opened in two tabs, therefore keep separate nonces.

**Consumption.** Opening the start page does not consume the flow identifier. A reload, or a second open after an abandoned provider login, within the 10-minute lifetime runs the login again and sets a new nonce for the flow, which replaces the earlier one, so a provider redirect from the earlier open fails the cookie check. The callback consumes the identifier.

**Refusals.** Each refusal renders an error page that names the reason and writes an audit event. The reasons are a tenant or subject that differs from the flow's, a missing or mismatched cookie, an expired or consumed flow identifier, a failed signature or `iss` check, and an unreachable flow store. The caller names the audit event beside its success event, as UC-2 names `user_link.created`.

### CA-1: Link flow

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. Account links under [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) replace connector links.

#### Connector routes

The record the shared flow of "CA-0" writes for a connector stays the existing connector credential row, because [`0152-user-git-identity`, "User tokens on a VCS host"](../0152-user-git-identity/change.md#user-tokens-on-a-vcs-host) withdrew any route for a link token to reach the connector path. Its revision 2 had proposed `hostLink`, which would have given a connector the token of a user's account link.

| Route | Purpose |
| :-- | :-- |
| `POST /v1/connector-links` | Body `{"connector": "memory", "environment": "team-a"}`. Returns `linkUrl`. |
| `GET /v1/connector-links` | Lists the caller's links with connector, environment, expiry, and whether a refresh token exists. It returns no token. |
| `DELETE /v1/connector-links/{connector}?environment=` | Removes the caller's link and revokes it at the provider when the provider has a revocation endpoint. |
| `GET /v1/connector-links/start/{flow}` | The browser start page. |
| `GET /v1/connector-links/callback` | The provider's redirect target. |

`lenny-ctl connector link <id> [--environment <name>]` wraps the first route and prints or opens the URL. `POST /v1/admin/connectors/{name}/oauth/authorize` and `GET /v1/admin/connectors/oauth/callback` are removed in proposal "CA-A", under "Steps" in `steps.md`, since no deployment depends on them.

1. The link URL is a Lenny URL for a shared flow naming the subject and environment.
2. The shared start page runs its login and subject checks.
3. The start page names the connector and its endpoint hosts, runs `CheckConnectorURLDomain`, which checks that the provider URL's host matches the connector's `expectedDomain`, and redirects.
4. The shared callback validates the return and calls the connector hook. A missing or mismatched cookie aborts the flow.
5. The gateway exchanges the code and stores the token with the endpoint digest from "CA-2".
6. The gateway resolves every pending connector prompt for that tenant, user, connector, and environment, and notifies the affected sessions under "CA-8".

An impersonation bearer is refused on every link route, as [`0151-user-credential-delivery`, "Flow"](../0151-user-credential-delivery/change.md#flow) requires for `/v1/user-links`.

### CA-2: Token lifecycle

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. UC-2 **Refresh** and the token trust record cover account-link tokens.

**Refresh.** `bearerFor` refreshes when a token has fewer than 5 minutes left, and once more after a 401 from the server. Refresh-token rotation, in which a provider issues a new refresh token on each refresh and invalidates the old one, means one writer per credential, so the row carries `refresh_lease_holder` and `refresh_lease_until`, using the lease form in [`0151-user-credential-delivery`, "Refresh"](../0151-user-credential-delivery/change.md#refresh): a 30-second lease set by a conditional update. Other replicas re-read the row for up to 5 seconds. The 5-minute refresh threshold, the lease length, and the 5-second wait are operator-tunable. Both designs use one lease form, so a later change to it considers account links and connector credentials together, as that section notes.

- `invalid_grant` deletes the token, and the user is unlinked from then on.
- A transient refresh failure returns the transient error and keeps the row.

**Endpoint binding.** The credential row records a digest of `mcpServerUrl`, `authorizationEndpoint`, and `tokenEndpoint`. A lookup whose digest differs from the connector's current one treats the row as absent and audits `connector_link.endpoint_changed`. This closes a gap in which an edit to `mcpServerUrl` sends every stored token to the new server **[source]**.

**Resource indicator.** A resource indicator (RFC 8707) is a `resource` parameter that names the server a token is for. `BuildAuthorizationURL` sends no `resource` parameter **[source]**. A proposal checks the MCP authorization page for its requirement on resource indicators and adds the parameter if the page requires it **[pending]**.

### CA-3: Connector access and environment

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. Credential binding `scope` and `environments`, and the tenant's egress allow set, decide what a session reaches.

**Default deny.** `AuthorizeConnector` returns `ErrConnectorNotPermitted` when no policy layer resolves, for every connector, which §8.3 evaluates as a candidate of type `connector`. An error resolving a policy fails closed. Carol grants the connector with a `DelegationPolicy` target of type `connector`, on the runtime or as the environment's default. This reverses the "conservative fall-through" comment in `connectorauthz.go`, which stays correct for other candidate types.

**Environment on a child.** The row `buildChildSession` writes carries the parent's environment, or the target environment when the delegation crosses a sharing path under [decision 14](../0149-tenant-scoping-identity/change.md#cross-environment-sharing-decision-14). [`0149-tenant-scoping-identity`, "TS-3: Session environment context and cross-environment sharing"](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) already requires this under **Delegated child's environment**, and **Effects on existing features** in the same section states that connectors read that field. This design depends on that change and lands it itself if that design has not landed first.

**Environment on a derived session.** A derived session carries its source's environment, as that design also requires under **Derive**. §7.1 **Connector state.** is rewritten: a connector link belongs to a user and an environment, so a derived session reuses it under those keys.

### CA-4: Link check

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. UC-3's check, with `403 LINKS_REQUIRED` and `details.userLinks`, remains. `details.connectors` and `deferConnectors` leave.

#### Evaluation

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

#### Placement

The check runs at the post-chain point of [RT-2](../0141-runtime-routing/change.md#post-chain-point): after the `PreRoute` chain, whose interceptors can change the runtime and pool, after revision selection ([RV-2](../0140-runtime-revisions/change.md#rv-2-selection)), and before the pod claim. It reads `capabilities.deferredToolDiscovery` from the chosen revision, merged with a derived runtime's own fields where one applies, because `capabilities` is a versioned field under [RV-1](../0140-runtime-revisions/change.md#rv-1-revisions) and the revision decides which binary the session runs. The connector set it evaluates comes from the policy and environment of the final row. In `create.go` the post-chain point replaces the split segments `preRouteBeforeExperiment` and `preRouteAfterExperiment`, which runtime routing deletes with the experiment code, and precedes `claimMintPersist`. §7.1's credential pre-check, step 3, which refuses a session when no large language model (LLM) provider credential is available, runs before the chain, and step 3a, as runtime routing rewrites it, describes the chain, revision selection, and the post-chain point, so the link check is part of step 3a and precedes step 4, the warm pod claim. No pod is claimed while a person is in a browser. Like the credential pre-check, it is a point-in-time check rather than a reservation.

It applies to the REST creation paths `POST /v1/sessions`, `POST /v1/sessions/start`, and derive, for every session. A protocol adapter creates its sessions through these routes under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-1-the-client-contract), so the check has no other placement.

#### Outcome

| `deferredToolDiscovery` of the chosen revision | Any connector `needs_link` | Result |
| :-- | :-- | :-- |
| `false` (default) | Yes | `403 LINKS_REQUIRED` (error category `POLICY`) with `details.connectors`, each with `id`, `displayName`, `environment`, and `linkUrl`, and with `details.userLinks`, as "Combined missing-links response" below states. Naming the environment makes a link held under another environment easy to diagnose. |
| `true` | Yes | The session is created, and the response lists the same connectors under `unlinkedConnectors` so a client can link early. |
| Either | No | The session is created. |

The request accepts `deferConnectors: ["memory"]`, stored on the session row. A deferred connector raises no prompt. On a runtime with `deferredToolDiscovery: false` it is left out of the manifest. On a runtime with `deferredToolDiscovery: true` it stays in the manifest, and its `tools/list` returns the typed error. A link that exists when a runtime starts wins over a deferral, so a user who links later gets the connector at the next start.

**Combined missing-links response.** A session can need a connector link and an account link at once. The account link check of [`0151-user-credential-delivery`, "Flow"](../0151-user-credential-delivery/change.md#flow) and the repository check of [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) report a missing account link, and both run at the same post-chain point. The gateway runs all of these checks before it answers and returns one `403 LINKS_REQUIRED` (error category `POLICY`) that reports every missing link, so a user links once and retries once. `details.connectors` lists the connectors as the table above states, and `details.userLinks` lists the account links, each with the link provider's name and its `linkUrl`, as that design states. Both lists are present in every such response, and either can be empty. The code is `LINKS_REQUIRED` whichever kinds of link are missing, so a client handles one code and reads both lists. The rule applies on every creation path that "Placement" names, derive included. A refusal of a single kind outside session creation keeps its own code, such as the `CONNECTOR_LINK_REQUIRED` tool error that "CA-7" returns for a delegated child and the explicit resume refusal of "CA-6".

**Why every permitted connector counts.** The owner replaced the pool-level "required" flag with this property, so a session is refused for a connector it might not use. Default deny keeps the set to what an administrator granted, and `deferConnectors` handles the rest. A workload that cannot run without a connector does not defer it.

**Clients.**

- A client that cannot pass the field, such as a stock OpenAI SDK, shows the error, and its user runs `lenny-ctl connector link`.
- An automated caller with no browser defers.
- Clients that create sessions through Lenny's MCP interface receive the combined missing-links response as a tool error with the same `details`. [MP-3](../0134-mcp-protocol-upgrade/change.md#mp-3-elicitation) of [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md) adds a form per protocol version for a client that declared URL-mode elicitation, with one URL-mode elicitation per missing link, and keeps this tool error for every other client. Each form reaches only the client that called create, so CAR-7 holds.

> **Moves to the MCP adapter.** The third bullet above governs the MCP adapter and no longer governs Lenny, under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which the owner settled on 2026-11-14 in the words "yes. Don't delete anything though, just flag it appropriately or move it to a subfolder or something". It stays here unchanged until that adapter's repository takes it, and stages no edit to Lenny's specification. Lenny answers the MCP adapter's creation with the REST response, `403 LINKS_REQUIRED` with both lists, and the adapter builds its tool error or elicitations from that response.

### CA-5: Propagating the failure

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. An operator service's 401 reaches the harness directly, and connector prompts and `initiator_type: connector` leave. For an account link that lapses during a session, the owner settled O11 of the removal design on 2026-11-15 as "go with (b)": the gateway raises a link prompt under [`0151-user-credential-delivery`, "UC-8: Link prompts"](../0151-user-credential-delivery/change.md#uc-8-link-prompts), which takes the lifetime, budget, suppression, and `inputPolicy` rules of "Connector prompts" below and keys deduplication on the user.

#### Why the raw 401 stops at the gateway

The runtime reaches a connector through `tool_call` frames on its slot's connection to the adapter, directly or through the reference bridge that its harness runs as a stdio MCP server, after the handshake of [RP-1](../0139-runtime-protocol/change.md#rp-1-handshake). External authorization is a separate hop between the gateway and the external server, as §9.1 and §9.2 state. An HTTP 401 cannot cross for three reasons:

- A Unix socket or stdio pipe has no HTTP status, and the MCP authorization page says stdio implementations should not follow its flow **[documented]**.
- A client that ran OAuth itself would hold the token in the pod, against CP-1.
- The person is at the Lenny client. Claude Code's documentation says a `claude -p` run cannot run the OAuth flow **[documented]**.

MCP's `URLElicitationRequiredError` (`-32042`) has the right meaning, since the adapter's server needs third-party authorization that must not transit the client. It also puts the URL in the client's hands, and the client here is the runtime, inside a pod with no browser. CAR-7 keeps the URL out of the pod, and that is the reason this design does not use the error. The error arrives in protocol version `2025-11-25`, and the adapter speaks `2025-03-26` today **[documented]** **[source]**. [MP-5](../0134-mcp-protocol-upgrade/change.md#mp-5-intra-pod-servers) moves the adapter to `2025-11-25` and `2026-07-28`, so the protocol version stops being an obstacle, and CAR-7 still excludes the error.

#### Translation

1. The connector client returns a typed `ErrAuthRequired` on 401, and on 403 with `insufficient_scope`.
2. `connectortools.Bridge`, the gateway type that serves the adapter's connector tool calls and is unrelated to the stdio bridge of "CA-9", passes it, and `leasecontrol` returns `codes.FailedPrecondition` with an `ErrorInfo` reason `CONNECTOR_LINK_REQUIRED`.
3. The adapter answers the runtime on the tool frames of [RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames): `tool_list` carries `error` with `code: "CONNECTOR_LINK_REQUIRED"` and no `tools`, and `tool_result` carries `isError: true` with the same `error`. The message is `Connector memory needs user authorization. The user has been asked.` The reference bridge of [RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge) maps the frames to MCP for a harness:

| MCP method | Harness receives |
| :-- | :-- |
| `tools/list` | JSON-RPC error `-32010`, the message above, and `data: {"connector": "memory", "reason": "link_required"}`. |
| `tools/call` | A result with `isError: true` and the same text, so a model can read it and retry later. |

Both return immediately. A tool listing cannot wait on a person, since Codex's startup timeout defaults to 10 seconds **[documented]**.

**What reaches the model.** Under Claude Code, the `tools/call` text reaches the model verbatim, and the model can retry. The `tools/list` error does not reach it: the session reports the server as `connected` with no tools, and the model said it could see no error **[measured]**. On that path the runtime knows and the agent sees only a missing tool, so the connector prompt to the user carries the message.

#### Connector prompts

On the same failure, the gateway raises a connector prompt unless the connector is deferred.

- **Identity.** It carries `initiator_type: connector`, `connector_id`, `purpose: oauth_login`, and the provider's `expected_domain`.
- **Interceptors.** `WalkChain` skips `Hop.Intercepts` for connector initiators, and a new tier 1 test pins this. CP-3 depends on it, and today's behaviour is the opposite **[measured]**.
- **Suppression.** §9.2 already exempts connector OAuth from depth suppression, the rule that by default suppresses agent-initiated elicitations at delegation depth 3 and deeper, and `DepthBlockAll` already returns false for this initiator **[source]**.
- **Deduplication.** One prompt per `root_session_id` and connector is pending at a time. A dismissal stands for that tree. An expired prompt can be raised again once per `maxElicitationWait`.
- **Lifetime.** It is non-blocking, unlike `leaseelicit`. It expires at `maxElicitationWait` and is dismissed when the tree ends. §9.2's rule that expires a `lenny/request_elicitation` when a session leaves its pod does not apply, since no runtime waits on it.
- **Budget.** It does not count against `maxElicitationsPerSession`, since an agent cannot produce more than the deduplication allows.
- **URL.** The URL is the Lenny link URL. §9.2 **URL domain validation** moves for connector prompts: the domain check runs on the provider URL at the start page, while the prompt's own URL names the gateway. `expected_domain` remains in the elicitation's provenance, the metadata the gateway attaches to every elicitation for the client to display.
- **Completion.** A client's `accept` only means the user will open the link, as in MCP's URL mode **[documented]**. The callback in "CA-1" resolves the prompt.

A session created with `inputPolicy: decline` ([R12](../0138-rest-only-client-api/change.md#ro-2-rest-additions)), as a client or a provider adapter that cannot show an elicitation creates one, still raises the connector prompt, because R12 leaves connector prompts raised by the gateway unaffected. The prompt stays pending in the session's elicitation list (R4) until it expires or the callback resolves it, and a client that cannot show it sees nothing. The error text tells the agent what is needed, and the user links through `lenny-ctl`. This replaces the earlier rule, which read the `supportsElicitation` field of the in-gateway adapter's `AdapterCapabilities`; that record leaves with the adapters under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).

### CA-6: Resume and restart

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour.

These paths exist today **[source]**:

| Path | Code | New runtime process | Client waiting | Behaviour **[design]** |
| :-- | :-- | :-- | :-- | :-- |
| `suspended`, pod held | `resumeHeldPodRequest` | No | Yes | No check. The runtime keeps its tool list. An expired token refreshes on the next call, or reaches "CA-5". |
| Explicit resume, `awaiting_client_action` or `suspended` with pod released | `handleResume` into `enterResumePending` | Yes | Yes | Link check before `enterResumePending`, with the "CA-4" outcomes, except that a refusal answers `403 CONNECTOR_LINK_REQUIRED` with `details.connectors`, because a resume is outside session creation and keeps the code of the check that refused it. The body accepts `deferConnectors`. A refusal leaves the state unchanged, and that state's clocks, such as `maxSuspendedSessionSeconds`, keep running. |
| Explicit resume that finds the held pod gone | `resumeHeldPodRequest` fallback | Yes | Yes | Treated as an implicit restart, since the request has already passed the point of a clean refusal. |
| Message arrives for a released session | `messages_delivery.go`, `suspended_backlog.go` | Yes | Sender only | Implicit restart. |
| Retry driver, `resume_pending` to `resuming` | `failure.go`, resume driver | Yes | No | Implicit restart. |
| Tree recovery | §8.10, §7.3 | Yes | No | Implicit restart. |

Today §4.7.8 also restarts a runtime below the Full integration level so that it picks up a rotated credential. That path has no row, because [`0133-llm-upstreams-and-cost-budgets`, "LU-4 Removal"](../0133-llm-upstreams-and-cost-budgets/change.md#lu-4-removal) removes the rotation of credentials into pods and §4.7.8 with it, and [RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies) has no rotation capability.

**The revision a restart reads.** A resume or restart runs no `PreRoute` chain. Each rule below reads `capabilities.deferredToolDiscovery` from the revision the new process will run: the session's recorded `runtimeRevision`, or the revision the session moves to when that revision's pod set is gone, as [RV-6](../0140-runtime-revisions/change.md#rv-6-sessions-over-time) states under **Retry and resume.**

**Implicit restart.** `ListSessionConnectors` is the one call every start path reaches: start and `Resume` go through `sessionConnectors` **[source]**. Every runtime process launches at bind, so no start path runs before a session exists. It gains the rule below, so a restart needs no path-specific code.

- On a runtime with `deferredToolDiscovery: false`, a connector that evaluates to `needs_link` is left out of `session_start`'s `availability.toolServers`. The session continues without it.
- On a runtime with `deferredToolDiscovery: true`, the connector stays in `toolServers` with `state: needs_link`, and the failure reaches `list_tools`.
- Either way, the gateway raises a connector prompt and emits `session.connector_unavailable` with the connector and reason.

**Omission rather than a failing server on a runtime with `deferredToolDiscovery: false`.** A server that fails `tools/list` can break a stock CLI's start, for example Codex with the server marked `required = true` in its MCP configuration **[documented]**, and an omitted server cannot. A resolution failure already omits connectors today, so runtimes already tolerate an absent entry **[source]**.

**The connector comes back at the next runtime start.** On a runtime with `deferredToolDiscovery: false`, a link completed mid-session has no effect until then, because the runtime configures its harness from `session_start`'s `toolServers` at launch and a tool server that first appears mid-session cannot join a running harness ([RP-4](../0139-runtime-protocol/change.md#rp-4-availability), **A harness's fixed configuration.**). Each runtime process serves one session and receives one `session_start`, so the next start is the next `session_start`. A user who needs it sooner suspends with pod release and resumes, or starts a new session.

**Why no refusal on these paths.**

- A retry driver has no one to answer it, and a refusal would turn a recoverable pod failure into a session failure.
- `maxResumeWindowSeconds` and `maxTreeRecoverySeconds` keep running, so a wait for a person could fail a whole tree.

**Latency.** Probes run in parallel and use the cache, so the added resume time is bounded by the 3-second timeout. A probe that returns `unknown` includes the connector.

**Deferral.** `deferConnectors` persists on the row, so a resume honours the earlier choice without a prompt.

**Confirmed.** The adapter re-resolves connectors on `Resume` before starting the runtime, so this rule takes effect on every restart with no adapter change **[source]**. Under RP-4 the resolved set reaches the runtime as `toolServers` on the new `session_start`. `TestSessionConnectors_spec_9_3_142` covers the resolve path, and a new test pins each row above.

### CA-7: Delegated children

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour.

#### Identity

A child uses `parent.UserID` **[measured]**, so the parent's user's link is the right token, and `TestExternalMCPToolPerDelegationScoping` covers policy scoping on that path. With "CA-3" a child that does not cross also has the parent's environment. Under [`0149-tenant-scoping-identity`, "Cross-environment sharing (decision 14)"](../0149-tenant-scoping-identity/change.md#cross-environment-sharing-decision-14), which the owner settled on 2026-11-03, a child that crosses a sharing path runs in the target environment, uses that environment's `connectorSelector`, and needs a link there, because §4.3 keys connector tokens on the environment.

#### Check at delegation

The link check runs in `buildChildSession` at the post-chain point of [RT-2](../0141-runtime-routing/change.md#post-chain-point) for `kind: delegate`: after the `PreRoute` chain, whose interceptors can change the child's runtime and pool, and after revision selection for the child's runtime ([RV-6](../0140-runtime-revisions/change.md#rv-6-sessions-over-time), **Delegation.**). It reads `capabilities.deferredToolDiscovery` from the child's chosen revision. Today's call site, immediately after `ApplyExperimentRouting`, goes with the experiment code that runtime routing removes. Export materialization, which copies the files the parent exports into the child's workspace ([§8.7](../../spec/08_recursive-delegation.md#87-file-export-model)), runs earlier in that function, and a returned error already undoes it through `undoChildExport`, and the deferred release returns the delegation tree budget, the per-tree counters of nodes, memory, children, and tokens that a delegation reserves **[source]**. The refusal therefore wastes export work but leaves no child row, pod, or budget behind. §8.3's credential pre-check at delegation is the precedent.

| `deferredToolDiscovery` of the child's chosen revision | Child's permitted connector `needs_link` | `start_child_session` result |
| :-- | :-- | :-- |
| `false` | Yes, and not deferred | `CONNECTOR_LINK_REQUIRED` with `details.connectors` naming `id` and `displayName`, and `details.retryAfterSeconds`. The tool error carries no URL. The gateway raises one connector prompt against the parent session. |
| `true` | Yes | The child starts. Its `tools/list` fails, and the prompt is raised from the child. The child lists again after `list_changed`. |

**Deferral.** `start_child_session` accepts `deferConnectors`. A child inherits the union of its parent's deferred connectors and its own, so a root that deferred `memory` causes no prompt or refusal anywhere in its tree. An automated root therefore runs a whole tree with one field.

**Why a refusal rather than silent omission.** An earlier discussion leaned towards leaving the connector out and telling the parent. Refusal is the better default for three reasons:

- It matches the root's behaviour.
- A parent that does nothing special gets a visible failure rather than a child quietly missing a tool.
- The parent can retry with a deferral in a single call.

An LLM parent reads the typed error, and a programmatic orchestrator handles it as it does any other typed refusal of a child's creation, such as `422 LLM_UPSTREAM_NOT_CONFIGURED`, which replaces `CREDENTIAL_POOL_EXHAUSTED` under [`0133-llm-upstreams-and-cost-budgets`, "LU-2 Assignment and resolution"](../0133-llm-upstreams-and-cost-budgets/change.md#lu-1-the-llm-gateway-as-an-egress-destination).

**Where the prompt goes.** The child does not exist yet, so the prompt's origin is the parent. It skips interceptors and goes to the root's human-facing edge. A parent agent never sees the URL, which is why the tool error omits it.

**How the parent learns of completion.** The parent retries `start_child_session`, waiting at least `details.retryAfterSeconds`, which defaults to 30 seconds and is operator-tunable. The interval exists because each retry by an LLM parent can cost a model turn. A retry raises no second prompt, because of the deduplication in "CA-5". The owner decided against a push notification. A message the gateway places in a session's inbox reaches only runtimes with `capabilities.injection.supported`, the runtime capability for message delivery mid-session, which defaults to `false`, and holding the call open risks client tool timeouts.

**Detached clients.** A prompt with no attached client stays pending until `maxElicitationWait`. The parent's retries keep failing and the parent decides whether to defer, so nothing deadlocks. A pending prompt raises no `input_required` state, the session state of waiting on input, since no session blocks on it, and §8.8's deadlock detection is unaffected.

**Children on restart.** A child's restart is an implicit restart under "CA-6", including under tree recovery. An omitted connector on a child is visible to its parent only through events the parent can read under its tree visibility (`treeVisibility`, how much of the task tree a session may see).

#### Confirmed against §8

| Question | Answer | Evidence |
| :-- | :-- | :-- |
| Does the child's token lookup use the right user? | Yes. | **[measured]** |
| Does it use the right environment? | Not today. "CA-3" fixes it. | **[measured]** |
| Does a child's connector set come from its own policy? | Yes. `ListSessionConnectors` and `CallTool` both evaluate the child session's policies. | `connectorauthz.go` |
| Can a child reach a human for OAuth at any depth? | Yes by §9.2 and `ShouldSuppress`. `WalkChain` then stops at an interceptor, which "CA-5" fixes. | **[measured]** |
| Does a refusal leak resources? | No. The error path undoes the export and returns the budget. | `service.go` |

### CA-8: Runtime capability and late tools

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. `deferredToolDiscovery`, `toolProgress`, and `tool_progress` leave. Capability inference and the capability snapshot of **Capability metadata.** leave as well, with `capabilityInferenceMode`, `toolCapabilityOverrides`, and `mcpAnnotationMapping`, under D10 of the removal design.

`capabilities.deferredToolDiscovery` joins the capability catalog of [RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies), defaulting to `false`, and a runtime reports it in its handshake. `true` states both of these:

- The runtime keeps a connector server after its listing returns an error.
- It lists again on `availability_changed` naming that server. For a stock CLI behind the reference bridge, the bridge turns the frame into `notifications/tools/list_changed`, so the CLI's own behaviour on that notification is what the capability claims.

A single field covers both, because one without the other does not help. `lenny-compliance` gains a check that runs a stub connector through a failure and a notification, in the §15.4.6 battery beside `connector_mcp_server_reachability`. A runtime declaring `deferredToolDiscovery: true` must pass it, and the suite reruns on each upstream release. Claude Code 2.1.317 showed both behaviours in the spike under "CA-9" **[measured]**, so it can declare `true` once it passes this check and the adapter changes below exist. It stays `false` until then. Codex stays `false` until it is measured.

Claude Code re-lists on the notification, but in the spike the model called the tool only because its prompt told it to check again, and nothing in the stream showed the model being told the tools had arrived **[measured]**. A runtime with `deferredToolDiscovery: true` therefore gains the tool without any assurance that the agent will notice it.

**Platform changes for a runtime with `deferredToolDiscovery: true`.** The runtime protocol design carries them ([RP-4](../0139-runtime-protocol/change.md#rp-4-availability), [RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge)), and they replace this design's earlier adapter changes, `listChanged` on the adapter's connector servers and a `NotifyConnectorToolsChanged` RPC:

- A completed link makes the gateway call `UpdateAvailability` on `CH-ATTACH` with that connector in `changedServers`.
- The adapter writes `availability_changed` on the slot's runtime connection and on each of its tool connections.
- The reference bridge declares `tools: {"listChanged": true}` and sends `notifications/tools/list_changed` for its server.
- The link callback can land on any replica, so the call uses `CH-HOLDERFORWARD` to reach the session's coordinating replica.

No runtime needs this until one declares `deferredToolDiscovery: true`, so this work can follow the path for `deferredToolDiscovery: false`.

**Progress of a connector call.** A connector can report progress on a long call with MCP's `notifications/progress`. The owner settled on 2026-11-15 ("2.1: ok") that it reaches the runtime, and [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) carries the mechanism as its D11: `toolProgress` joins the catalog of [RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies) beside `deferredToolDiscovery`, the connector client asks a connector for progress only for a session whose effective capabilities include it ([MP-6](../0134-mcp-protocol-upgrade/change.md#mp-6-outbound-clients)), the gateway forwards each notification for a pending call to the adapter, and the adapter writes it as a `tool_progress` frame naming the call, which the reference bridge sends to the harness as `notifications/progress` **[design]**. The gateway holds the connector's credential and talks to the connector, as for the call itself, so nothing about the link changes, and a progress notification carries no token or URL into the pod (CAR-7). A call that a connector reports no progress on reaches the runtime as its result alone, and a runtime stops waiting for any call with `tool_cancel`.

**Capability metadata.** §5.1 derives `conn.ToolCapabilities`, the capability snapshot, at registration and on refresh, and the §10.6 filter reads it **[source]**. This is a smaller version of the snapshot the owner rejected for tool catalogs. The owner decided to keep it and change nothing else, and this design adds no work to it. Its behaviour is:

- A tool with no inferred capability falls to the conservative default, `admin` under the default `strict` inference mode, so a new tool name fails closed under a restrictive filter.
- An existing tool whose behaviour changes keeps its old classification until a refresh.
- A server that requires auth to list tools has no inference until an administrator links and refreshes it, so a restrictive filter denies its tools until then.

**Enforcement downstream.** A deployer who wants permissions enforced by the server uses OAuth scopes. `BuildAuthorizationURL` sends the connector's `auth.scopes` **[source]**, and a server that honours them refuses what the token does not allow. For example, a deployer registers `memory-ro` with `scopes: [memory.read]` alongside a read-write entry. Lenny does not verify that a server enforces scopes, and each entry needs its own link.

### CA-9: Stdio bridge

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The reference bridge of [RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge) stays for the `platform` tool server alone, and a harness reaches an external MCP service over HTTP through `netagent` ("CR-3" of the removal design).

Claude Code documents stdio, HTTP, Server-Sent Events (SSE), and WebSocket, and Codex documents stdio and HTTP, and neither states Unix socket support **[documented]**. A stock CLI therefore reaches the session's tool servers through a stdio process of its own.

The bridge is the stdio form of the reference MCP bridge of [RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge), `lenny-mcp-bridge --server <server>`, where `<server>` is `platform` or `connector:<id>`. Under RP O3 (b), settled on 2026-11-12, it reads its slot's manifest, dials the slot's endpoint named by `LENNY_ADAPTER_SOCKET`, performs the `tools` handshake of RP-1, and carries its server's calls as tool frames on that tool connection. The slot's endpoint admits only the slot's UID and binds the connection to the slot, so the bridge names no session and holds no token. RP-5 owns its MCP mapping, its handshake, and its conformance cases. This design relies on two of its behaviours: it maps one MCP server to one tool server, so a connector's tools never collide with another's, and it declares `tools: {"listChanged": true}` and turns `availability_changed` for its server into `notifications/tools/list_changed` ("CA-8"). `cmd/git-credential-lenny`, the in-pod git credential helper, was the precedent for an in-pod client that performs a Lenny handshake.

#### Delivery

The bridge ships with the runtime SDKs in Go, Python, and TypeScript, and as a static release binary that a runtime image built without an SDK copies in, under RP O4 (a), settled on 2026-11-12 ([`0139-runtime-protocol`](../0139-runtime-protocol/decisions.md#open-decisions)) **[design]**. The platform delivers no copy: this design's earlier `lenny-tools` image, `lenny-tools-stage` init container, `lenny-tools` volume, and `lenny-pod-security` rule for it are withdrawn, and so is the alternative of staging the bridge into `/lenny/bin`.

- **Why the platform no longer delivers it.** The earlier revisions delivered the bridge so that it stayed in step with the adapter's private handshake. Under RP O3 (b) the bridge performs the versioned `tools` handshake itself, so the adapter and any copy of the bridge agree on a version whichever ships it.
- **Cost removed.** Each pod no longer pays the second of startup that one more init container costs ("Startup measurement"), and no per-slot mount of a tools volume is needed in each slot container.
- **Cost added.** A runtime image that runs a stock CLI must carry the bridge. The reference runtimes for Claude Code and Codex carry it in their images.
- **Security.** The bridge adds no access. The slot's endpoint already admits any process of the slot's UID, and a tool server is reachable only when `session_start` lists it.

#### Startup measurement

The measurement below was taken for the init container that RP O4 (a) withdrew. It is kept because it is the evidence for the startup cost that the withdrawal removes.

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

#### CLI configuration

The bridge is universal, and CLI configuration is not. Each runtime's wrapper generates its own CLI's MCP configuration before launch from `session_start`'s `availability.toolServers`, with one stdio server entry per tool server, each running `lenny-mcp-bridge` with that server's name, because the formats differ ([RP-5](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge), **CLI configuration.**). The binary's presence does not grant access: a tool server is reachable only when `toolServers` lists it.

The bridge's mapping is normative in the `CH-MSGSOCK` card of §28.5.3 and in a new §15.7 section, "Reference MCP Bridge", which the runtime protocol design stages. §15.4's sentence "There is no stdio transport for intra-pod MCP" is restated there.

This ships with the Claude Code and Codex integrations, which need it for platform tools regardless.

#### Spike results for Claude Code

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

These results do not cover the tool connection of RP-5, `SO_PEERCRED`, a real pod, or the gateway path. The spike's bridge relayed to an adapter MCP server, which the runtime protocol design replaces with tool frames, so the results stand for the CLI's side of the bridge: its tolerance of a failed listing and its re-listing on `list_changed`. The spike ran on macOS with filesystem sockets. Other CLI versions may differ, which is why the conformance suite reruns on each release.

No Codex result is recorded. The CLI was not logged in for model turns, and the owner decided those runs cannot be trusted.

### CA-10: Removing built-in memory

> **Live.** Since revision 17, 2026-11-15, CA-10 stays under [`0124-connector-removal`](../0124-connector-removal/change.md). A deployer reaches an external memory service through egress, as "External memory pattern" states.

**Removed.** The inventory below comes from a search for the feature's own identifiers. It excludes unrelated `NewMemory` stores and Google Cloud Memorystore tests, which a broader search also matches.

| Area | Items |
| :-- | :-- |
| Store | `pkg/gateway/session/memorystore`, including `pgstore`, `memorystoretest`, `embedder.go`, `preflight.go`, and `observer.go`. |
| Tools | `registerMemoryTools` and its schemas in `pkg/gateway/platformtools/mcptools`. |
| Routes | `pkg/gateway/platformtools/memory.go` and the `/v1/sessions/{id}/memory` and `/v1/sessions/{id}/memory/{memoryId}` routes that `pkg/gateway/platformtools/handler.go` registers, with the paths in `pkg/api/openapi/openapi.json` and the rows of `cmd/lenny-gateway/testdata/route-table.golden`. |
| Seam and layout | The memory routes in "Route registration" of `spec/interfaces/gateway-seams.md`, the memory routes in the `platformtools` row of `pkg/gateway/ARCHITECTURE.md`, the three rows with `removed-by: '0123'` in `tests/registers/gateway-layout.yaml`, and the rows for `memorystore` and `memorystoretest` in `tests/registers/spec-section-citations.yaml`. |
| Gateway | `--memory-enabled`, `--memory-max-per-user`, the record-count sampler, and wiring in `cmd/lenny-gateway`: `flags.go`, `wiring_fields.go`, `session_messaging.go`, `metricsbackfill.go`, `metrics_observers.go`, `workers.go`, `config_parse.go`, `adminrouter.go`, and `erasure_contract.go`, with the flag rows of `cmd/lenny-gateway/testdata/flag-surface.golden`. |
| Erasure | The memory step in `pkg/storage/erasure/order.go` and `contract.go`, `ValidateMemoryStoreErasure`, and the memory store preflight phase of the erasure job in `pkg/storage/erasurejob` (`FailurePhaseMemoryStorePreflight`). The reserved preflight tenant that `migrations/0096_preflight_tenant.up.sql` seeds serves that preflight, and its treatment follows open decision O4. |
| Observability | `lenny_memory_store_*` and their collectors in `pkg/gateway/replica/gatewaymetrics`, the catalog entries in `pkg/observability/metrics/catalog.go`, `MemoryStoreGrowthHigh`, `MemoryStoreErasureDurationHigh`, `pkg/alerting/rules`, `docs/alerting/rules.yaml`, and the chart's `alerting-rules.yaml`. |
| SDKs | `MemoryWrite` and `MemoryQuery` in the Go, Python, and TypeScript runtime SDKs. |
| Chart | Memory values in `charts/lenny/values.yaml` and `values.schema.json`, and `gateway-deployment.yaml` flags, with the chart tests and `pkg/embedded/manifests/manifests.yaml`. |
| Schema | A new migration dropping `agent_memory`. Migrations 0032 and 0044 stay as history, unless the owner rewrites them under open decision O4. |
| Tests | `tests/component/stores/memory*`, and memory cases in the contract and security tiers. |

**Removed settings and routes.** The chart's values schema refuses unknown top-level keys, so a values file that still sets a `memory.*` value fails the chart's schema check once the `memory` property leaves the schema. The gateway parses its flags with the standard flag set, which exits with an error on an unknown flag, so a deployment that still passes `--memory-enabled` or `--memory-max-per-user` does not start. The memory routes are no longer registered and answer `404`.

**The embedder.** `semanticcache` imports `memorystore` for `Embedder`, `HashingEmbedder`, and `CosineDistance` **[source]**. Step 0123.S2 depends on 0132.S1, which removes the semantic cache, so that code leaves with the memory store and nothing moves into `semanticcache`.

**pgvector.** Whether the `vector` extension stays a prerequisite of the migration chain is open decision O4. Migration 0044 runs `ALTER TABLE agent_memory ADD COLUMN embedding vector(256)`, so the chain needs the extension, or the pure-SQL `vector` shim of the embedded stack, for as long as migrations 0032 and 0044 stay as they are. The carriers of the requirement are `tests/testinfra/containers/postgres.go` (`pgvector/pgvector:pg16`), `compose/default.yml`, `deploy/terraform/cloud/azure/managed-services.tf` (the `VECTOR` allowlist entry), and that shim. Some database tests name the pgvector image only as their Postgres container, and "Validation owed" in `steps.md` carries that check.

**Data.** Nothing migrates, since there are no deployments.

### External memory pattern

Revision 17 restates this pattern for the egress path, because [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). The pattern of revisions 1 to 16, which registered a memory server as a connector, follows under "Connector-based pattern, withdrawn" as the record.

**Project knowledge.** Keep it in the repository, in files such as `CLAUDE.md` or `AGENTS.md`, and change it through pull requests. It is reviewed, it follows repository permissions, and it needs no Lenny mechanism. This is the first choice for coding workloads.

**User-level memory [design].** A deployer runs a memory service outside Lenny, typically an MCP memory server, and sessions reach it through egress as "CR-3" of the removal design states:

- The service's host is in the tenant's egress allow set. A service on a private network is reached through an outbound connector route of [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md).
- A credential binding on the host with `delivery: inject` tells the service who is calling. An `identityAssertion` binding ("CR-2" of the removal design) gives the service an identity assertion: a short Lenny-signed token that `netagent` attaches to requests to the service, naming the tenant, user, and session, which the agent never sees. The service scopes memory by the `tenant` and `user` claims. A memory service that serves every tenant verifies the assertion against the deployment key set that the removal design publishes and takes the tenant from the verified key, never from an unverified claim. When the deployment sets `tokenService.identityAssertion.deploymentKeySet.enabled: false`, `https://<lenny>/identity/.well-known/jwks.json` answers `404`, and such a service verifies through the issuer-prefix method of CR-2 **What a verifier checks.** instead: it accepts an `iss` of the form `https://<lenny>/identity/<tenant-id>` under the deployment's identity prefix, fetches that tenant's key set at `https://<lenny>/identity/<tenant-id>/jwks.json`, and takes the tenant from the issuer whose key set held the key. A `userCredential` binding ([UC-3](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding)) gives it the user's own token from an account link instead, and the service scopes by the token's subject.
- The runtime's own configuration names the service URL, through its `env`, a `runtimeOptions` property it declares, or a workspace file such as a project-scoped `.mcp.json`, and the wrapper writes it into the harness's MCP configuration with the HTTP transport.

```yaml
credentialBindings:
  - name: memory-identity
    hosts: [memory.acme.com]
    secret: { kind: identityAssertion }     # lifetimeSeconds defaults to 300
    delivery: inject                        # the only delivery an assertion takes
    scope: always
```

The harness speaks MCP to `https://memory.acme.com/mcp` through `netagent`, which attaches the assertion. The memory server verifies it against `https://<lenny>/identity/acme/jwks.json`.

**Pods.** A session that holds an `identityAssertion` binding is refused with `422 IDENTITY_ASSERTION_UNSUPPORTED_POD` on the pods that **Pods (D11).** under CR-2 of the removal design lists: a pod of a recycling pool, a pod that has served a session, and a pod with several slots on a pool whose attribution status records the union scope or no result yet. A binding with `scope: always`, as in the example above, places that limit on every session of the tenant in the binding's environments. A tenant that runs sessions on recycling pools gives the binding `scope: onRequest` or narrows its `environments`.

**Erasure [design].** Deleting the memory data is a deployer obligation, which the operator guide states. The erasure receipt names, under `externalDataNotErased`, the hosts of the tenant's bindings that carried the user's identity, from `userCredential` links the erasure deletes and from `identityAssertion` bindings, so a receipt never implies the external data was removed. The field has these properties:

- It is a list of host names, sorted and without duplicates. It is an empty list when no such binding or link exists.
- The `DeleteByUser` job reads the hosts before it deletes the user's links, and it keeps the list in the job record, so a job that resumes by phase after a crash still reports them.
- The receipt of a `DeleteByTenant` job names the hosts of every `userCredential` and `identityAssertion` binding of the tenant.
- Which bindings count for a user, including a binding deleted before the erasure, and whether the receipt also names the provider account of each deleted link, are open decision O6 in `decisions.md`.

**Retention, residency, capacity, and retrieval quality.** These move to the deployer.

**Code exposure.** A memory service is a channel from a session to an external store. Its traffic passes `netagent` and `lenny-egress`, so the bytes-out alert, `egressLockHosts`, and credential pinning apply, and `egress.tunnel` records each connection as `enforced` evidence. No Lenny interceptor sees the memory content. Memory returned to a model is untrusted input, and scanning it is the service's job. User-scoped memory can carry content from one repository into a session on another, and Lenny cannot partition memory by repository.

**Impersonation.** UC-3 **Impersonation** refuses impersonated input to a tree holding a `userCredential` binding, and the removal design extends the refusal to `identityAssertion` bindings, so the accepted risk under "Limits" no longer applies to memory reached this way.

#### Connector-based pattern, withdrawn

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** Kept as the record of revisions 1 to 16.

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

**Prerequisite: F-9.3.19.** The example registers a confidential client through `clientSecretRef`. The chart gives the gateway no `get` on that Secret, so under the chart's defaults the token exchange fails at the callback, as [`0151-user-credential-delivery`, "Defects found while verifying"](../0151-user-credential-delivery/change.md#defects-found-while-verifying) records under F-9.3.19. The example works once F-9.3.19 is fixed. Until then, a deployer registers a public client, with no `clientSecretRef`, or grants the gateway read access to the named Secret.

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
- "Instructions for reviewers of other designs" asks [`0127-egress-and-git`](../0127-egress-and-git/change.md) to record this path.

## Limits

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour.

- A connector token is decrypted in the gateway (F-4.3.1). A compromised replica can use any connector token.
- **Impersonation is an accepted risk, by owner decision.** `requireCreatingBearerAllowed` stops an impersonation bearer from creating a session, and `handleMessages` still accepts one **[source]**. The invoker spends the session owner's token with no impersonation check. A platform admin impersonating Alice can therefore message her session, and its agent can write to her external memory as her. The external service records Alice as the actor, the write outlasts the session, and no per-call audit event names the impersonator. The operator guide states this. [`0151-user-credential-delivery`, "Limits under both deliveries"](../0151-user-credential-delivery/change.md#limits-under-both-deliveries) refused built-in memory writes for this reason, under **Impersonation**, and that protection does not carry over to connectors.
- A pod with several slots, one whose pool sets `sessionPolicy.maxConcurrentSessionsPerPod` above one, gives each session its own slot container and runtime process ([`0145-session-isolation`](../0145-session-isolation/change.md)), and the adapter knows each call's session from the connection that carries it ([RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). Omission is therefore per session on every agent pod, and the earlier limit for a shared-process pod no longer exists. A connector call travels on a `tool_call` frame to the gateway rather than through `netagent`, so the egress design's slot attribution and its decision 60 do not change the link check or the call's session.
- The probe tells only whether listing needs auth. A server that lists different tools once authenticated gives a runtime with `deferredToolDiscovery: false` the authenticated list at its next start, since a linked user lists with their token.
- A stock client's rendering of a `tools/list` error or an omitted server is outside Lenny's control. Claude Code does not show the `tools/list` error to the model **[measured]**.
- A user in two environments links twice, which keying tokens by environment under §4.3 scoping implies and the owner accepted for the separation it gives. [`0151-user-credential-delivery`, "Stored links"](../0151-user-credential-delivery/change.md#stored-links) keys account links per user, under its [decision 30](../0151-user-credential-delivery/decisions.md#decisions), so the two kinds of link behave differently.
- The capability snapshot does not notice a changed tool until a refresh, as "CA-8" states.

## Alternatives rejected

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The rows "Keep memory in the gateway behind a flag" and "Ship a reference memory server" stay live under CA-10.

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
| Use `/v1/user-links` tokens for connectors | [`0152-user-git-identity`, "User tokens on a VCS host"](../0152-user-git-identity/change.md#user-tokens-on-a-vcs-host) withdrew `hostLink`, since it moves a link's token onto the gateway path. |
| No link check on an explicit resume | The process would start without the connector, and the connector list of a runtime with `deferredToolDiscovery: false` is fixed at start, so a user who then links waits for another restart. |
| Refuse impersonated messages on sessions with a linked connector | The owner accepted the risk instead, since this blocks support work on those sessions. |
| Block connector calls after impersonated input | The owner accepted the risk instead, and this needs new state carried through delegation. |
| Key connector tokens per user | The owner kept the environment key. It can be loosened later more easily than it could be added. |
| Read tool annotations live for the §10.6 filter | The owner kept the snapshot and asked for no other change. |
| Push a message to a parent when a link completes | See "CA-7". |
| Keep memory in the gateway behind a flag | It keeps the erasure and isolation obligations for every deployment that may enable it. |
| Ship a reference memory server | It adds a component to maintain, with no customer asking. |
| Deliver the bridge to every pod through a platform init container, as revisions 5 to 12 did | RP O4 (a), settled on 2026-11-12, chose shipping with the runtime SDKs: the bridge's own versioned handshake keeps any copy in step with the adapter, and each pod saves the second an init container costs. The earlier objection to images carrying the bridge, that a custom image can carry an old copy, falls with the handshake. |
| Stage the bridge into `/lenny/bin` beside the supervisor | The same decision. The pod layout design leaves `/lenny/bin` to the supervisor, `lenny-materialize`, and git. |
| A local HTTP listener in the adapter | It opens a TCP port, loses the `SO_PEERCRED` check, and changes every pod's listener surface. |

## Instructions for reviewers of other designs

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The removal design's sweep list replaces the connector items below. The items on CA-0 and CA-10 stay live.

The owner instructed that this document edit no other design. These items are for a reviewer agent working on the named design, which applies them there. Other work is editing these designs on this branch, so locate each item by heading and quoted text, check that it still holds against the current text, and report any item that no longer fits rather than forcing it.

### `0151-user-credential-delivery`

[`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 25 applied the items of this subsection.

**Owner decision: the OAuth flow is a shared proposal, "CA-0", which lands before UC-2.** Apply these changes:

1. **"UC-2: The `oauth2Link` source and account linking", "Flow".** Replace the mechanics in steps 2 to 5 with a reference to "CA-0" in this document: the one-time flow identifier, the OIDC login and subject check, the cookie nonce, the signed `state`, and callback validation. Keep what is specific to UC-2: step 1, the token trust record display and digest comparison, the per-provider callback path, and steps 6 and 7, including the hand-off to the Token Service, identity binding, `allowedAccounts`, and the confirmation page. Check that the "CA-0" hook carries everything those steps need, and report any gap back to this document.
2. **"Dependencies" in `steps.md`.** Add "CA-0" to the UC-2 row, with the reason that the flow's mechanics come from it.
3. **"Reuse of §9.3".** This list explains why account links do not reuse §9.3, and its conclusion stands because of `ConnectorDefinition` requiring an MCP server, the per-person link, and F-4.3.1. Three bullets stop being true once this design lands, so restate them as today's state or remove them from the reasons:
   - "The §9.3 flow runs from a tool call inside a session": "CA-A" adds linking before a session.
   - "Connector refresh has no caller, and no store has a refresh lease": "CA-B" adds both.
   - "The callback is open to the account-link attack": "CA-0" closes it.
4. **"Defects found while verifying".**
   - "Account linking in §9.3": state that "CA-0" closes it.
   - "Refresh absent": state that "CA-B" closes it.
   - "Session input under impersonation": remove `handleMemoryWrite` and `lenny/memory_write`, which "CA-G" deletes.
5. **"Refresh".** No change to UC-2's behaviour. Note that "CA-B" adopts the same lease form for connector credentials, so a later change to that form considers both.
6. **Session create.** A session can need an account link and a connector link at once. Adopt the combined missing-links response that "CA-4" defines, `403 LINKS_REQUIRED` with `details.userLinks` and `details.connectors` whichever kinds of link are missing, so a user does not link, retry, and get refused again. Both checks run at the post-chain point of [RT-2](../0141-runtime-routing/change.md#post-chain-point), as "CA-4" states under "Placement", and the refusal of a `userCredential` binding on a pod that can serve another session reads the final pool there.

Leave the link record in the Token Service unchanged, under "Stored links".

**Owner decision: built-in memory goes, and impersonation on linked connectors is an accepted risk.** Apply these changes:

1. **"Limits under both deliveries", the Impersonation paragraph.** Remove the "Memory, unconditionally" bullet with no replacement. Do not extend the session input check to sessions holding a connector link.
2. **"Specification sections affected".** Remove "§9.4: memory writes refuse an impersonation bearer."
3. **"Tests", "Tier 4: integration".** Remove `lenny/memory_write` from the impersonation bearer test.
4. **"Current state".** The row "Other session input refuses an impersonation bearer" names memory writes as a fact about today's tree. Keep it until "CA-G" lands.

### `0152-user-git-identity`

[`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 33 applied the item of this subsection.

**Owner decision: "CA-0" lands before UC-2, on which UG-1 depends.** Apply this change:

1. **"When the check runs".** The repository check refuses a session whose user has no link before the pod claim. That refusal is the combined missing-links response that "CA-4" defines, `403 LINKS_REQUIRED`, and the check runs at the post-chain point of [RT-2](../0141-runtime-routing/change.md#post-chain-point), as "CA-4" states under "Placement".

Leave these unchanged: decision 3 on connectors on the hosts of a VCS pool with `userAuthorization: required`, and the withdrawal of `hostLink`, both under "User tokens on a VCS host".

### `0127-egress-and-git`

**"Residual risks and limits" and "Exfiltration controls".** Record that a §9.3 connector tool call runs on the gateway, so `netagent`, `lenny-egress`, and the bytes-out alert (that design's per-session alert on bytes sent through egress tunnels) do not see it, and that its arguments can carry source code to an external server. Default deny in "CA-3" and connector interceptors are the controls. If that design or a later one adopts a default for sessions holding private repository content, it decides whether connectors stay available there. This document does not settle that.

### `0149-tenant-scoping-identity`

No edit. "CA-3" depends on **Delegated child's environment** and **Derive** under "TS-3: Session environment context and cross-environment sharing". Decision 14 settled on cross-environment sharing, and "CA-3" and "CA-7" follow it. If either rule changes, report it to this document, because a child's token lookup reads that field.

### `0142-security-oversight`

**Owner decision: built-in memory goes.** Apply this change:

1. **"User credentials and request rules" under SO-2.** The **Impersonation** item states that the session input check refuses an impersonation bearer "on every memory write and delete". "CA-G" removes built-in memory and its routes and tools, so remove that clause and keep the refusal on `/v1/credentials` writes. Do not extend the check to sessions holding a connector link, because the owner accepted that risk (see "Limits").

### `0139-runtime-protocol`, `0145-session-isolation`, and `0118-agent-pod-layout`

Revision 13 applied the items that [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) addressed to this design. Under RP O4 (a) no platform delivery of the bridge remains, so these items follow:

1. **`0145-session-isolation`, "Instructions for reviewers of other designs".** The item saying that this design's tools volume mounts into each slot container while it exists has no case left, because the volume is withdrawn.
2. **`0118-agent-pod-layout`, "PL-1 Pod blueprint".** The paragraph that stages `lenny-mcp-stdio` into `lenny-bin` if RP O4 keeps CA-9's delivery has no case left, and `lenny-bin` holds the supervisor, `lenny-materialize`, and git alone.
3. **`0139-runtime-protocol`, "RP-2".** The `deferredToolDiscovery` row of the catalog cites "CA-8" and "CA-6", which state its behaviour in terms of `availability_changed` and `toolServers`.

### Other designs

[`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) and [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) need no change. The first has `§9.3` reader-page work of its own, a sentence and link on §9.3 and `docs/tutorials/using-connectors.md` that separate MCP connectors from outbound connectors, and a connector's probe uses the same dial path as its other calls.

## Specification changes

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The memory removals stay live under CA-10: the memory parts of the §4, §8.2, §8.3, and §8.5, §9.1, §9.4, §12, §15.1 and §15.4, §16, §18, §22.4, §23, and §28.5.3 rows, including `externalDataNotErased` in §12.8, defined as the properties under **Erasure [design].** in "External memory pattern" state. No row is CA-0's: its shared flow lands on the account-link routes of §15.1 with UC-2, and §9.3 becomes a successor pointer under CR-1 of the removal design. Whether step 0123.S1 needs a specification edit of its own, for the converted §9.3 handlers and the property that only the subject who requested a link can complete it, follows open decision O1. §18 and §23 are successor stubs since 0115.S9, so under "Reading the program" of the v1 program the §18 row applies to `docs/about/status.md`, whose row "Memory, semantic caching, guardrail hooks" sits outside the generated markers, and the §23 row applies to `docs/about/comparisons.md`. Every connector statement is withdrawn, and the removal design's "Specification changes" replaces it.

Each lands through `change-proposal`.

| Section | Change |
| :-- | :-- |
| §5.1 Runtime | Add `capabilities.deferredToolDiscovery` to the capability catalog that [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) stages, beside the `toolProgress` capability that design stages under its D11. |
| §7.1 Normal Flow | Add the link check to the post-chain point, which runtime routing's edit of step 3a places between the `PreRoute` chain and the pod claim. Rewrite **Connector state.** |
| §7.2 and §7.3 | Add the explicit resume check and implicit restart rule. |
| §8.2, §8.3, and §8.5 | Add the delegation check, deferral inheritance, and the child's environment. Remove the memory tools and their schemas. |
| §9.1 | Remove the memory tools. Point to the reference bridge's section, which the runtime protocol design adds under §15.7. |
| §9.2 | State connector prompts: gateway origin, interceptor skip, deduplication, lifetime, and URL domain rule. |
| §9.3 | Replace the OAuth flow, add refresh and endpoint binding, and state default deny and the typed error. State that a connector's progress notifications for a pending call reach a runtime that declares `toolProgress`, with the connector client's progress token staged by `0134-mcp-protocol-upgrade` "MP-6" and the frame by `0139-runtime-protocol` "RP-3". |
| §9.4 Memory Store | Replace with a successor pointer to the external pattern, naming the identifiers that left. |
| §4 System Components | Remove memory from the tenant-scoped table, and from local MCP server text if named. No delivery of the bridge is added to §4.7.10, because RP O4 (a) ships it with the runtime SDKs. |
| §12 Storage Architecture, including §12.8 | Remove the role, the erasure preflight and guidance, and the erasure steps. Add `externalDataNotErased`. |
| §15.1 and §15.4 | Add the link routes, `CONNECTOR_LINK_REQUIRED` on an explicit resume and, with `details.retryAfterSeconds`, on delegation, the error code `LINKS_REQUIRED` for the combined missing-links response at session creation and derive, with `details.connectors` and `details.userLinks`, either of which can be empty, `deferConnectors`, and `unlinkedConnectors`. Add the `-32010` error and the compliance check. Remove memory helpers. |
| §16.1, §16.5, and §16.7 | Remove memory metrics and alerts. Add audit events `connector_link.created`, `connector_link.removed`, and `connector_link.endpoint_changed`, and the event `session.connector_unavailable`. |
| §18 Build Sequence | Remove memory from Phase 13 and Phase 17b, and place the connector work. |
| §22.4 No Built-In Memory Extraction | Rewrite, since Lenny no longer provides the `MemoryStore` interface. |
| §23 | Remove the memory claims. |
| §28.5.3 | Remove the memory tools from the platform tool list. The runtime protocol design replaces the `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` cards with tool frames on `CH-MSGSOCK` and places `UpdateAvailability` on `CH-ATTACH`, so this design adds no RPC placement. |

## Documentation

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The memory removal stays live: `docs/tutorials/agent-memory.md` describes the egress pattern of "External memory pattern", as the **External memory.** bullet states, and `docs/runtime-author-guide/platform-tools.md`, the runtime capabilities page, the operator guide, the glossary, and `docs/reference/metrics.md` lose the memory content. Every connector statement is withdrawn, and the removal design's "Changes to Lenny's documentation" replaces it. [`0124-connector-removal`](../0124-connector-removal/change.md) also names `docs/tutorials/agent-memory.md` under "Changes to Lenny's documentation", and which step writes it is open decision O3.

The live pages, each with its reader and what changes for that reader:

| Page | Reader | Change |
| :-- | :-- | :-- |
| `docs/tutorials/agent-memory.md` | Runtime author and operator | Describes the egress pattern of "External memory pattern", with the deployer's erasure obligation and `externalDataNotErased`, in place of the built-in store. |
| `docs/tutorials/index.md` | Runtime author and client | The "Agent Memory" entry describes the external pattern rather than memory persisted by Lenny. |
| `docs/runtime-author-guide/platform-tools.md`, `runtime-sdk.md`, `local-development.md`, and the runtime capabilities page that replaces `integration-levels.md` | Runtime author | `lenny/memory_write`, `lenny/memory_query`, `MemoryWrite`, and `MemoryQuery` leave. |
| `docs/operator-guide/configuration.md` ("Memory Store Configuration") and `docs/reference/configuration.md` ("Memory Store configuration") | Operator | The memory settings leave, and a values file that sets one fails the chart's schema check. |
| `docs/operator-guide/observability.md` and `docs/reference/metrics.md` | Operator | `lenny_memory_store_*`, `MemoryStoreGrowthHigh`, and `MemoryStoreErasureDurationHigh` leave. |
| `docs/getting-started/concepts.md` ("Memory Store") and `docs/getting-started/architecture.md` (the memory store node of the diagram) | Every reader | The memory store leaves the concepts and the architecture diagram. |
| `README.md` and `docs/index.md` | Every reader | The "Memory store" bullet, which states a Postgres and pgvector default, leaves. |
| `docs/reference/glossary.md` | Every reader | The memory store entries leave. |
| `docs/testing/forward-compatibility.md` | Contributor | The statement that `MemoryStore` is pluggable in v1 leaves. |
| `docs/about/status.md` and `docs/about/comparisons.md` | Every reader | The successors of the §18 and §23 rows: the status row "Memory, semantic caching, guardrail hooks" and the memory store rows and bullets of the comparisons drop built-in memory. |

The pages that change are `docs/tutorials/agent-memory.md`, which becomes the external pattern; `docs/tutorials/using-connectors.md`, which gets the link flow; `docs/runtime-author-guide/platform-tools.md`, the runtime capabilities page that replaces `integration-levels.md` under [`0139-runtime-protocol`](../0139-runtime-protocol/change.md), the operator guide, the glossary, and `docs/reference/metrics.md`, which lose the memory content; and `TESTING.md` §13.36. Together they state every setting, limit, failure mode, and runtime-facing rule this design introduces:

- **Settings and defaults.** `deferConnectors` on session creation, derive, and `start_child_session`, inherited as a union down the tree; `capabilities.deferredToolDiscovery`, default `false`, read from the chosen revision; `details.retryAfterSeconds` on a delegation refusal, 30 seconds by default and operator-tunable; `maxElicitationWait`, 600 seconds by default and set per pool, which bounds a connector prompt; and a connector's `auth.scopes`, which the authorization URL sends.
- **Limits.** The anonymous probe's 3-second timeout and its 60-second cache per tenant and connector on each replica; refresh when a token has fewer than 5 minutes left and once after a 401; one pending prompt per root session and connector; a link per user and environment; and a capability snapshot that does not notice a changed tool until a refresh. This design adds no node requirement.
- **Failure modes.** `403 LINKS_REQUIRED` at session creation and derive, with `details.connectors` and `details.userLinks`; `403 CONNECTOR_LINK_REQUIRED` with `details.connectors` on an explicit resume; `CONNECTOR_LINK_REQUIRED` with `details.retryAfterSeconds` from `start_child_session`; the implicit restart that leaves a connector out and raises a prompt with `session.connector_unavailable`; default deny with `ErrConnectorNotPermitted`; an endpoint digest mismatch, which makes a stored token unusable; and the accepted impersonation risk of "Limits".
- **Runtime-facing rules.** A runtime with `deferredToolDiscovery: false` starts without a connector the user has not linked and gains it at its next start. A runtime with `deferredToolDiscovery: true` keeps a server whose listing fails and lists again on `availability_changed`, and must pass the compliance check of "CA-8". A runtime that declares `toolProgress` receives a connector's progress on a pending call as `tool_progress` frames, and one that does not receives the result alone. A tool frame refused for a missing link carries `error.code: "CONNECTOR_LINK_REQUIRED"`, which the reference bridge maps to `-32010` on `tools/list` and to an `isError` result on `tools/call`. A runtime that runs a stock CLI carries `lenny-mcp-bridge` in its image, and its wrapper writes one stdio server entry per tool server from `session_start`'s `toolServers`.
- **External memory.** The pattern of "External memory pattern", including the deployer's erasure obligation and `externalDataNotErased`.

## Tests

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The CA-0 cases of tier 9, a link URL sent to another subject, a missing cookie, and an impersonation bearer, each refused, stay live and run through the account-link routes of UC-2 once CR-A of the removal design deletes the §9.3 handlers. The tier 11 case for the removed memory metrics and alerts stays live. Every connector case is withdrawn, and the removal design's "Tests" replaces it.

### Tests of the live steps

The tiers are named as `lenny-test --tier` names them. Each test carries its `// spec:` annotation.

| Step | Tier | Coverage |
| :-- | :-- | :-- |
| 0123.S1 | unit | The callback refuses a tampered `state` signature, an `iss` that differs from the provider's where the provider returns one (RFC 9207), an expired flow identifier, and a consumed flow identifier. The cookie name carries the flow identifier. |
| 0123.S1 | integration | A replayed `state` finds the tombstone and is refused. A reload of the start page within the lifetime sets a new nonce, and a provider redirect from the earlier open fails the cookie check. Two flows in one browser complete independently. A callback on a replica other than the one that served the start page completes. |
| 0123.S1 | chaos | The flow store unreachable at the start page and at the callback refuses the flow, with the error page and the audit event. |
| 0123.S1 | security | A link URL sent to another subject is refused. A login in another tenant with the same subject is refused. A missing cookie is refused. An impersonation bearer is refused. Each refusal writes its audit event, and no token appears in the error page. The cases of open decision O2 for a deployment with no OIDC browser login join this row once O2 is settled. |
| 0123.S2 | unit | The platform tool list carries no memory tool. The erasure order and contract carry no memory step, and the erasure job has no memory store preflight phase. |
| 0123.S2 | contract | `POST` and `GET /v1/sessions/{id}/memory` and `DELETE /v1/sessions/{id}/memory/{memoryId}` answer `404`, and the route table and flag surface golden files carry no memory row. |
| 0123.S2 | integration | The drop migration applies and reverses on the migration chain. A `DeleteByUser` job runs to completion with the memory step and its preflight gone. |
| 0123.S2 | static | A values file that sets a `memory.*` value fails the chart's schema check, beside the chart render tests under `tests/static/chart`. |
| 0123.S2 | unit | The gateway's flag parsing refuses `--memory-enabled` and `--memory-max-per-user` as unknown flags, beside `cmd/lenny-gateway/flags_test.go`. |
| 0123.S2 | docs | Removed metrics and alerts leave no runbook or documentation reference. |
| The step that lands `externalDataNotErased` under open decision O3 | integration | A `DeleteByUser` receipt names the hosts of the user's `userCredential` links and of the tenant's `identityAssertion` bindings, sorted and without duplicates, and an empty list when there are none. A job that resumes after a crash past the link deletion still reports the hosts. A `DeleteByTenant` receipt names the hosts of every such binding of the tenant. |

### Withdrawn tests

The table below is the record of the connector design and uses the tier numbers of that time.

The tiers are the stages of the repository's test suite, which `lenny-test --tier` runs.

| Tier | Coverage |
| :-- | :-- |
| 1, unit | Link check table, including probe cache expiry and each status. The check reads `deferredToolDiscovery` from the chosen revision, at create, at delegation, and on resume. `WalkChain` skips interception for connectors. `AuthorizeConnector` default deny. Endpoint digest mismatch. Error mapping at each layer. Deferral inheritance. |
| 3, contract | `LINKS_REQUIRED` at create and derive over REST, with a connector link alone, an account link alone, and both missing, each response carrying both lists with an unused list empty. `CONNECTOR_LINK_REQUIRED` on an explicit resume and on `start_child_session`. `-32010` on `tools/list` through the reference bridge, and `isError` on `tools/call`. |
| 4, integration | Each row of the "CA-6" table. Create, link, retry, then call. A `PreRoute` interceptor or route that moves a session or a delegated child to a runtime whose chosen revision has `deferredToolDiscovery: false` is refused at the post-chain point, with no pod claimed. Refresh on expiry. `invalid_grant` unlinks. Delegation refusal with `retryAfterSeconds`, retry, and deferral, with the export undone and budget returned. Child in the parent's environment finds the link. Derived session reuses a link. The stub server moves from 401 to 200 within the cache window. |
| 5, end to end on Kind | A stock CLI in a runtime image that carries `lenny-mcp-bridge` reaches a connector through the bridge on a tool connection of its slot, a second slot's bridge cannot reach the first slot's endpoint, and a completed link makes the CLI list the connector's tools again. The rendered pod has no `lenny-tools` volume or init container. |
| 7a, local load | Concurrent refresh yields one provider call per credential, extending `connector_oauth_refresh_race` to the real store. Prompt deduplication across replicas. |
| 8, chaos | Probe timeout on create and on resume. Token endpoint outage during refresh. Callback landing on a replica that does not hold the session. |
| 9, security | Link URL sent to another subject is refused, through both the converted §9.3 handlers in "CA-0" and the new routes. Missing cookie is refused. Impersonation bearer is refused. No URL or token appears in a pod, in a tool result, or at an intercepting parent. A child with a denied capability stays denied. |
| 10, conformance | The `deferredToolDiscovery` check through the reference bridge. A stub connector that reports progress on a call reaches a runtime that declares `toolProgress` as `tool_progress` frames before the result, through the reference bridge as `notifications/progress`, and carries no token or URL. The bridge's own handshake cases belong to the runtime protocol design's conformance section. |
| 11, documentation | Removed metrics and alerts leave no runbook or doc reference. |

Each test carries its `// spec:` annotation once the specification lands.

## Sources

- MCP specification 2025-11-25: [Authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization), [Elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation), and [Tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).
- [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp).
- [Codex MCP documentation](https://developers.openai.com/codex/mcp), which redirected on 2026-11-03 to `learn.chatgpt.com/docs/extend/mcp`. Most Codex facts here came from a summary of that page.
