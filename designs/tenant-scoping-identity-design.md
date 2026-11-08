# Tenant scoping and identity

Status: design for owner review, revision 30, 2026-11-08. Nothing here is built or staged, and no test or spike was run for it. It reads against [`egress-and-git-design.md`](egress-and-git-design.md) revision 71, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 24, [`user-git-identity-design.md`](user-git-identity-design.md) revision 32, and [`security-oversight-design.md`](security-oversight-design.md) revision 45. No decision under "Decisions" remains open. Statements about the tree were checked by reading the code and specification. No production component, identity provider, or directory has been exercised. Proposals carry the label TS-n. "Review history" at the end records what each revision changed and which text each review read.

## Problem

A company runs Lenny as a multi-tenant SaaS. One of its customers has several teams and wants each team to have its own egress and git configuration, with single sign-on (SSO) and user lifecycle coming from the customer's own directory.

The following gaps stand in the way:

- In [`egress-and-git-design.md`](egress-and-git-design.md), egress policy, VCS credential pools, and credential bindings are tenant-wide. Any client in a tenant can name any binding with `scope: onRequest` or any allowlisted repository, and any agent in a tenant can fetch any allowlisted repository at run time. The environments of [§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) select runtimes and connectors only.
- Lenny trusts one OpenID Connect (OIDC) issuer per deployment and has no SCIM (System for Cross-domain Identity Management), the protocol through which a customer's directory provisions and deprovisions users. A SaaS operator therefore needs an identity broker for per-customer SSO, which the specification neither says nor bounds with a contract, and a customer cannot bring its own issuer.

## Scope

This design covers environment scoping of the objects in [`egress-and-git-design.md`](egress-and-git-design.md), the identity model (a broker by default, or a customer's issuer per tenant), a SCIM server, and the user lifecycle path into Lenny. Migrating a tenant between issuers is out of scope. It treats those egress objects as fixed inputs. [`security-oversight-design.md`](security-oversight-design.md) owns security roles.

## Terms

| Term | Meaning |
| :-- | :-- |
| E | The environment a session runs in. A session whose create named no environment has none, which this document writes as E = ∅. See "A session's environment". |
| Explicit path and transparent path | An explicit path is a session create that names an environment: `/v1/environments/{name}/sessions`, `/mcp/environments/{name}`, an environment-scoped model name on `/v1/responses` or `/v1/chat/completions` (a scoped model namespace), or the `environment` body field. The transparent path is a create that names none, such as `POST /v1/sessions`, on which §10.6's transparent filtering applies: the caller sees the union of the runtimes selected by every environment in which its groups hold a role, and runtimes no such environment selects are hidden. |
| Scoped object | A VCS credential pool, credential binding, user credential type, or link provider whose `environments` list names the environments whose sessions can use it. An object with no list is tenant-wide. |
| `environmentPolicies[E]` | The section of the tenant egress policy that holds environment E's own always-allowed, session-requestable, and denied hosts, and its egress and git routes. |
| `visible`, `always(E)`, `requestable(E)`, `denied(E)`, and `routes(E)` | The sets that combine tenant-wide entries with E's own objects, hosts, and routes. "Effective policy" under TS-1 defines them. |
| Pod with no per-session policy | A pod on a pool that serves concurrent sessions or runs in service mode. Its egress policy cannot differ by session, so it evaluates every session with E = ∅. A one-session pod serves one session at a time and applies that session's policy. |
| Sharing path | A pair of `dangerouslyShare` entries, outbound in one environment and inbound in another, through which a delegated child of a session in the first runs in the second. A path works only while the tenant setting `dangerouslyAllowCrossEnvironmentSharing` is on. See "Cross-environment sharing (decision 14)". |
| Delegation gate | The check the gateway runs on each `lenny/delegate_task` call, and for `lenny/discover_agents`, to decide whether the session may reach the target runtime. Today `runtimeAuthorizedForCaller` and `crossEnvReachable` decide it from the caller's environment memberships. Under TS-3 it reads the session's E and the live sharing declarations. |
| Crossing, target T, and crossing mark | A crossing is a delegation over a sharing path. T is the target environment, and the crossing child runs under T's policy. The crossing mark is the child's `crossed_from_environment` column, which holds the caller's environment and stops a second crossing. |
| Identity source | Where a tenant's users authenticate: the deployment source or a customer source. |
| Deployment issuer and deployment source | The OIDC issuer at the Helm value `auth.oidc.issuerUrl`, which every tenant uses by default. It can be the operator's identity broker. On it, the tenant comes from a token claim. |
| Identity broker | An identity service an operator runs or buys, which federates each customer's own identity provider (IdP) behind one issuer and states the customer's tenant in a claim. "Broker contract" states what Lenny requires of it. |
| Customer source | A row in `tenant_identity_sources` naming a customer's own OIDC issuer, pinned to one tenant. See "Identity source record (TS-6)". |
| Shared issuer | An issuer that serves many customers, such as Google's, so the issuer alone cannot select a tenant. A platform admin lists it in `identity.sharedIssuers`, and each of its rows pins an audience, an organization claim, or both. "Issuer pin and proof" defines the term exactly. |
| Pin (issuer pin), domain pin, and domain-pinned row | A pin is what ties a customer source to one tenant: the exact issuer, and on a shared issuer also the pinned audience or claim value. A domain pin is a pin whose claim value is a DNS domain (`pin_claim_is_domain`), such as Google's `hd`, and a domain-pinned row is a source row that holds one. Two unrelated mechanisms also use the word: the tenant pin, a pod label, and the credential-pinned host rule (`credentialPinned`) of the egress design. |
| Canonical subject | A customer source's user key, `~<source_id>~<value of subject_claim>`. A deployment source's `sub` keeps its form. See "Several IdPs in one tenant, and namespacing". |
| Verifier registry and snapshot | The registry is the member of `pkg/auth/jwt` that selects a token's source by its `iss`, verifies the token, and resolves its tenant and subject. The snapshot is the in-memory copy of active sources it reads. See "Verifier registry". |
| Activation rule, proofs, and activation preflight | The activation rule governs how a source becomes `active` or changes. The proofs are the checks recorded on the row before activation: discovery, sign-in, domain, and attestation. The activation preflight is the read before activation that refuses or lists principals left on old subjects. See "Activation rule" and "Issuer pin and proof". |
| Configuration preflight | The `lenny-preflight` Job, which runs before an install or upgrade and fails it on a misconfigured deployment ([§17.6](../spec/17_deployment-topology.md#176-packaging-and-installation)). Unlike the chart's own validation, it can read stored rows, so it can refuse an `identity.sharedIssuers` entry that conflicts with a source row. |
| `deploymentIssuerAllowed` | A flag on the tenant record that states whether the tenant accepts the deployment issuer. Activating a customer source sets it false. |
| Operator tenant | The one tenant, named by the Helm value `auth.platformAdminTenant`, whose rows can hold `platform-admin`. It is a tenant that exists and holds user rows, so it is a different tenant from the platform tenant, whose ID `platform` [§10.2](../spec/10_gateway-internals.md#102-authentication) reserves. The chart refuses `auth.platformAdminTenant: platform` (decision 27). |
| Directory | A customer's record of its users and groups, such as Entra ID or Okta, which provisions and deprovisions users through SCIM or through a sync worker. |
| Sync worker | A process that receives a directory's changes through a broker or vendor endpoint and calls Lenny's `invalidate` and `reinstate` routes, authenticating per tenant under a role that holds `manage_user_lifecycle`, such as `identity-sync`. |
| Directory row | A row in `directory_users`, written through SCIM. `externalId` is the directory's own identifier for the user, which the directory sends in SCIM. `match_claim` is the source field naming the token claim whose value must equal it. On the deployment source the row binds to a subject when a token's `match_claim` value first equals its `externalId`. See "SCIM server (TS-8)". |
| Hold | An invalidation entry on a user row for one caller class (`admin`, `sync`, or `scim:<source_id>`), with its invalidation level. A user is active only when no hold remains. See "User lifecycle (TS-4)". Two other holds are unrelated to users. An issuer-level hold stops fetches from a shared issuer whose address resolves into an excluded range, and a deployment-level hold pauses the lapse count of every domain-pinned row while many TXT records go missing at once. "Issuer pin and proof" and "Failure behaviour (identity)" state both. |
| `manage_user_lifecycle` | A permission TS-4 adds that covers the `invalidate` and `reinstate` calls alone, so a sync worker or a custom role such as `identity-sync` can deprovision users without assigning roles. |
| Hook | Work that a proposal of the egress design carries for this design, because adding it later would force rework of a stored record format, a wire contract, or a shipped enforcement point. The egress design lists its hooks under "Reserved for other designs". |

### Terms from other designs and the platform

This design builds on mechanisms other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Tenant | Lenny's unit of isolation between customers. Tenants are separated by tenant pinning of pods, row-level security in Postgres, and separate keys. |
| Environment | A resource inside a tenant ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)) that names members, each a subject or an `oidc-group` with a role of `viewer`, `creator`, `operator`, or `admin`, and a `runtimeSelector` and a `connectorSelector` that choose the runtimes and connectors its members reach. |
| `noEnvironmentPolicy` | A tenant setting, with a deployment default, that decides what a user with no environment membership reaches: no runtime under `noEnvironmentPolicy: deny-all`, or every runtime of the tenant under `noEnvironmentPolicy: allow-all`. |
| Runtime and pool | A runtime is a registered agent image and its configuration. A pool is a set of pods for a runtime, kept warm for sessions. |
| Concurrent-session pool, slot, and service mode | A pool with `maxConcurrentSessions` above 1 runs several sessions on one pod, and each session's share of the pod is a slot. A runtime with `executionMode: service` serves requests from a shared pod. |
| Recycling pool and pool recycle scrub | A pool with `recycle.enabled: true` returns a pod to the pool after a session, and the pool recycle scrub of [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) cleans it between sessions. State the scrub does not reach survives into the next session. |
| Derived runtime, owned pool, and `oneSessionPerPod` | Under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), a `tenant-admin` holding a grant derives a runtime from a base runtime and gives it a pool the tenant owns. `oneSessionPerPod: true` makes that pool's pods one-session pods. |
| Derive | Starting a new root session from an existing session's state, under [`session-derive-design.md`](session-derive-design.md), which also absorbed the former replay route. |
| Delegation and delegation lease | A session can start child sessions through the `lenny/delegate_task` tool, and they form a delegation tree under a root session. Children of the same parent are siblings. A delegation lease records what a child receives from its parent, including `maxDepth`, the depth limit, and `maxChildrenTotal`, a cap on children ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism)). |
| Delegation presets, `DelegationPolicy`, and budget slice | Under [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), a delegation preset is a named lease template a deployer defines in `delegationPresets`, with values such as `maxDepth` and `maxChildrenTotal`. A `DelegationPolicy` is a named resource whose tag rules state which runtimes, connectors, and pools a session may delegate to, and `delegationPolicyRef` is a field that names one. A budget slice (`lease_slice`) is the share of the parent's budget a parent allocates to a child, which Redis Lua scripts reserve atomically. |
| Delegation inheritance | What every child takes from its parent's lease, whatever environment it runs in. Isolation monotonicity requires the child's pool isolation to be at least as restrictive as the parent's. `treeVisibility` limits how much of the task tree `lenny/get_task_tree` shows, and each node it shows carries `runtimeRef`, the node's runtime. `contentPolicy` limits the size of a task's input and exported files and names any content interceptor. A child's value of each can only narrow. |
| Session plan fields | Fields of a session's workspace plan ([§14](../spec/14_workspace-plan-schema.md)) and egress request: `gitClone` names a repository and ref to clone into the workspace, `writableRepositories` names repositories the session pushes to without cloning, and `egress.allowedHosts` lists the hosts the session asks for. A runtime's `sharedAssets` are files placed read-only in every pod before it is ready, and a `gitRepository` shared asset is one filled from a repository, which the egress design's warm fill serves. |
| Warm list | The one list per pool of repository stores that the pool's pods fill before they report ready, under one rule set, which [`tenant-derived-runtimes-design.md`, "Warm clones"](tenant-derived-runtimes-design.md#warm-clones) defines. Its entries are a Runtime's public `gitRepository` entries, private entries a platform admin writes on one tenant-owned pool's row, and a tenant's warm default clones and cache-only entries on a tenant-derived runtime. |
| Experiment routing and `PreRoute` | Experiment routing sends a share of a runtime's sessions to a variant runtime or pool ([§10.7](../spec/10_gateway-internals.md#107-experiment-primitives)). A `PreRoute` interceptor can rewrite a session's runtime and pool late in a create. |
| Intra-pod tool surface | The platform MCP server a runtime reaches inside its pod (`@lenny-platform-mcp`), whose calls `platformtools.Bridge` forwards to the gateway. |
| Token Service | The Lenny service that stores user and provider credentials, mints short-lived tokens from them, and answers the pod credential API ([§4.3](../spec/04_system-components.md#43-token-service)). It also serves the token exchange at `/v1/oauth/token`. |
| `netagent` | A container in every agent pod that acts as the pod's egress proxy. It holds the pod's service account token, terminates TLS for hosts that carry a credential, and attaches credentials. |
| `lenny-egress` | The central egress proxy, built on Envoy, through which agent traffic leaves the cluster. |
| Decision service | `lenny-egress-authz`, which decides whether each connection is allowed, from the session's effective policy, and returns the route to use. It is one of two independent checks, beside the Token Service. |
| Git mirror | `lenny-git-mirror`, which serves git fetches from per-tenant stores, fills those stores through the Token Service, and accepts prefetch requests. A warm fill populates a store before any session exists. |
| `pkg/binding` | The package the Token Service, the decision service, and the mirror use to resolve a pod to its session, user, tenant, and bindings. |
| Pod credential API | The gRPC API on which `netagent`, and the git mirror, ask the Token Service for a binding's token or secret. A response carries the binding's hosts and how to attach the credential. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I8. |
| Egress decision API | The API on which a proxy, the git endpoint, or the Token Service asks `lenny-egress-authz` whether a connection is allowed. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I3. |
| Pod binding API | The gRPC API on which `netagent` receives its pod's bindings, covered host set, and base destinations from `lenny-egress-authz`. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I7. |
| Credential binding | An egress-design object that gives sessions a secret for named hosts. Its `delivery` field selects `inject`, under which `netagent` attaches the secret and the runtime holds a placeholder, `file`, under which the runtime reads the secret from a file, or `federation` or `metadata`, which deliver cloud credentials. |
| Binding `scope` | When a binding attaches to a session: `always`, to every session of the tenant; `onRequest`, to a session whose plan names it; or `poolAttach`, to a session on a pool whose add-on attaches it. |
| VCS credential pool | An egress-design object that names repository prefixes, a provider credential such as a GitHub App, and an allowlist of repositories. Under [`user-git-identity-design.md`](user-git-identity-design.md), `userAuthorization: required` makes each repository need the user's own access at the provider, and under `userAuthorization: off` the pool's own token serves any caller. |
| User credential type, link provider, and link | Objects of [`user-credential-delivery-design.md`](user-credential-delivery-design.md). A type names a kind of per-user token and its source: `registered`, which a tenant backend or the user writes, or `oauth2Link`, which the user links at a link provider through a browser flow. A link is one user's stored authorization under one link provider. |
| `userCredential` binding | A credential binding whose `secret` names a user credential type, so each session receives its own user's token. |
| Token trust record | The record of everything that decides where a stored user token can go. A change to it makes the token unusable until the user acts again. |
| Tenant egress policy | The tenant's lists of always-allowed hosts, which every session reaches, session-requestable hosts, which a session plan can add, and denied hosts, with its egress and git routes. |
| Route | A tenant setting that sends traffic for matching hosts to a target: `direct`, a tenant's upstream proxy, a registered private endpoint, or an outbound connector. Egress routes carry API traffic, and git routes carry git traffic. |
| Effective policy | The hosts and bindings a session may use, which the decision service computes from pool, tenant, and session settings within the pool's egress ceiling. [`egress-and-git-design.md`, "Effective policy"](egress-and-git-design.md#effective-policy) gives the formulas this design extends. |
| Egress ceiling and host set | A pool's egress ceiling is the largest set of hosts any pod of the pool can reach through the proxy, whichever tenant or session it serves. A host set is a named list of host patterns, such as `npm`, `pypi`, or `stripe-api`, which ceilings and policies reference. |
| Warm-up policy and tenant warm-up additions | The hosts and repositories a pool's pods can reach before they are bound to a tenant, and the hosts and repositories a tenant adds for its pods that are bound but have no session yet. |
| Tenant pool overlay | A tenant's narrowing or extension of a pool's egress and git settings, within limits the platform admin sets on the pool. |
| Upstream and egress group | An upstream is a deployer's or tenant's proxy that `lenny-egress` chains to. An egress group is an additional `lenny-egress` Deployment, assigned to one tenant, whose traffic leaves the cluster from a dedicated address. |
| Covered host | A git host that a VCS pool or a git route names. `netagent` terminates TLS for it and runs git through itself. |
| `egressProfile` and `gitAccess` | `egressProfile` is a pool setting: `restricted`, `proxied`, or `internet`. A pool with `egressProfile: restricted` reaches no external host, and `gitAccess` lets its pods reach git hosts alone. Under `egressProfile: internet`, the pod also has raw sockets that bypass `netagent` and `lenny-egress`. |
| `egress.allowedHosts`, `lockEgressToHosts`, and `egressLockHosts` | `egress.allowedHosts` is a session plan's list of hosts. `lockEgressToHosts`, a binding option of [UC-5](user-credential-delivery-design.md#uc-5-delivery-file), limits a session to that binding's hosts, and each descendant and derived session records the lock as `egressLockHosts`. |
| Shadow policy | A candidate tenant egress policy that the decision service evaluates beside the live one, recording its outcome without enforcing it. |
| Level 2 proxy | A deployer's own proxy in place of `lenny-egress`, which calls the decision service for each connection. |
| Per-class gates | Per-tenant grants a platform admin gives, one per risk class, before a tenant can opt in to a delivery that places a credential within the agent's reach. They are unrelated to the delegation gate. |
| Claim, bind, and tenant pin | A claim reserves a warm pod for a session, and bind attaches the session and its bindings to that pod. The tenant pin is the `lenny.dev/tenant-id` label that ties a pod to one tenant. |
| Evidence streams and `credential.mint` | Append-only records that [`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams) defines. `credential.mint` records each token release or mint. |
| Security roles and security principal | `security-admin`, `security-approver`, and `security-auditor` are roles of [SO-1](security-oversight-design.md#so-1-security-roles). A security principal is a row in `tenant_security_principals` that grants one. The tenant setting `securityRoleSource` states whether a token claim can carry such a role: never under `securityRoleSource: managed`, and through the roles claim under `securityRoleSource: oidc`. |
| Guardrail, posture, and the weakening delay | A guardrail is a setting a `security-admin` writes in the tenant security record ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)) that limits what a tenant admin can configure. A change that weakens one takes effect after a delay, which a `security-admin` can cancel. Posture is that record's read-only report of a tenant's security-relevant settings. |
| `lifecycleSubjects` | A list in the [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) tenant security record naming the callers, such as `scim:<source_id>`, that may invalidate and reinstate a security principal. |
| Platform roles and permissions | Lenny's roles under [§10.2](../spec/10_gateway-internals.md#102-authentication): `platform-admin` spans every tenant, `tenant-admin` administers one tenant, and `user` creates sessions. A custom role holds named permissions such as `manage_users` or `manage_environments`. |
| Lenny-issued token, Lenny key set, and `subject_token` | A Lenny-issued token is one Lenny mints itself, rather than an IdP: through the token exchange at `/v1/oauth/token` ([§13.3](../spec/13_security-model.md#133-credential-flow)), the playground mint, or the impersonation mint. The Lenny key set is the set of keys those tokens are signed with. In a token exchange, `subject_token` is the token being exchanged. |
| Bootstrap `lenny-admin` credential | The administrator credential the chart seeds in the `lenny-admin` Secret at install ([§17.6](../spec/17_deployment-topology.md#176-packaging-and-installation)), which this design exempts from its identity source checks as a recovery path. |
| Service identity | A Kubernetes service account token, such as that of `lenny-ops-sa` under [§25.4](../spec/25_agent-operability.md#254-the-lenny-ops-service), which the gateway's `serveServiceIdentity` verifies through a Kubernetes `TokenReview` (`pkg/gateway/serviceidentity`). It carries the roles the deployment grants that account, which can include `platform-admin`, and it stays exempt from this design's identity source checks as a recovery path. |
| Request rules and presets | Under [`user-credential-delivery-design.md`](user-credential-delivery-design.md), `requestRules` on a binding with `delivery: inject` refuse requests before the credential is attached, and a preset is a named, versioned set of such rules shipped as data. The egress design allows one binding with `delivery: inject` per host and session (the one-binding-per-host rule). §8.3's delegation presets are unrelated. |
| Impersonation bearer | A token a `platform-admin` obtains through `/v1/admin/impersonation` to act as a user ([§13.3](../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). |
| User invalidation | The levels of [§11.4](../spec/11_policy-and-controls.md#114-user-invalidation), applied through `POST /v1/admin/users/{userId}/invalidate`: `soft_disable` denies new sessions, `hard_disable` also blocks new delegated tasks, and `full_revoke` also ends running sessions and revokes issued tokens. |
| `caller_type` | Whether a token's caller is `human`, `service`, or `agent`. A user token is released only to a session whose caller is `human`. |
| `lenny-ops` | Lenny's operations service, for operators and their automation ([§25](../spec/25_agent-operability.md)). |
| Playground and its mint modes | Lenny's web playground ([§27](../spec/27_web-playground.md)), which logs a browser user in and mints bearers at `/v1/playground/token`. In its `oidc` mode, `mintOIDC` mints from the stored playground record of the user's login. In its `apiKey` mode, `mintAPIKey` takes a gateway bearer the user pastes and validates it. |
| `identityProvider` introspection | Settings in a tenant's RBAC configuration (`rbac-config`) under which the gateway posts each bearer to a tenant-set RFC 7662 introspection endpoint and reads the caller's current groups from the answer. |
| `lenny-ctl` | Lenny's command-line client for operators. |
| Erasure and classification | The erasure job of [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) removes a user's personal data and leaves a tombstone. [§12.9](../spec/12_storage-architecture.md#129-data-classification) assigns each kind of data a tier, such as T3 (confidential, which includes user identifiers) or T4. |
| OIDC token claims and keys | `iss` names the issuer, `aud` the audiences, `azp` the client the token was issued to, `sub` the subject, `iat` the issue time, and `exp` and `nbf` the validity window. An issuer publishes its endpoints in a discovery document and its signing keys at its JSON Web Key Set (JWKS) URL, `jwks_uri`, with each key named by a `kid`. |
| PKCE, nonce, and `state` | Protections of an OAuth2 authorization code login. PKCE (Proof Key for Code Exchange) binds the code to the client that requested it, the nonce binds the ID token to the login, and `state` binds the callback to the browser that started the login. |
| RFC 9728, RFC 9207, and RFC 7662 | RFC 9728 defines the protected resource metadata document that tells a client which authorization servers a resource accepts. RFC 9207 adds `iss` to an authorization response, so a client can detect a response from the wrong IdP. RFC 7662 defines token introspection. |
| SSRF | Server-side request forgery: causing a server to fetch a URL that reaches an internal address. |
| MCP, KMS, CNI, IMDS, and SIEM | The Model Context Protocol, through which clients and runtimes call Lenny's tools; a key management service; the container network interface plugin that enforces NetworkPolicy; a cloud provider's instance metadata service; and a security information and event management system. |

### Labels from other designs

Proposals, properties, and defects in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-1, EG-2, EG-3, EG-4, EG-5, EG-6, EG-8, EG-9, and EG-10 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | Proposals of the egress design: EG-1 git prerequisites, EG-2 the pod network agent and proxied egress, EG-3 the pod git path, EG-4 credential delivery, EG-5 the git mirror and warm fill, EG-6 per-tenant routing and replacement, EG-8 tenant visibility, EG-9 egress policy objects with the single write path for tenant opt-ins, and EG-10 evidence ingest. |
| P4 and P7 | [`egress-and-git-design.md`, "Security properties"](egress-and-git-design.md#security-properties) | Egress security properties: P4 keeps one tenant's data and credentials away from another tenant's pods, and P7 keeps one pod's load from removing another tenant's egress, through quotas per pod and per tenant. |
| UC-1, UC-2, UC-3, and UC-5 | [`user-credential-delivery-design.md`](user-credential-delivery-design.md) | Proposals of the user credential design: [UC-1](user-credential-delivery-design.md#uc-1-credential-types-and-the-registered-source) credential types and the `registered` source, [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) the `oauth2Link` source and account linking, [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding) the `userCredential` binding, and [UC-5](user-credential-delivery-design.md#uc-5-delivery-file) `delivery: file`. |
| UG-1 and UG-2 | [`user-git-identity-design.md`](user-git-identity-design.md) | Proposals of the VCS layer: [UG-1](user-git-identity-design.md#ug-1-vcs-link-kinds) VCS link kinds and [UG-2](user-git-identity-design.md#ug-2-repository-authorization) repository authorization. |
| SO-1, SO-2, SO-4, and SP1 | [`security-oversight-design.md`](security-oversight-design.md) | [SO-1](security-oversight-design.md#so-1-security-roles) security roles, [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) the tenant security record and posture, which holds a tenant's guardrails and delays any change that weakens one, and [SO-4](security-oversight-design.md#so-4-operator-access-transparency) operator access transparency. [SP1](security-oversight-design.md#security-properties) is the property that only an active security principal row, presented on a bearer issued after the row took effect, grants a security role. |
| FE-n | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) | Proposals of the forensics and evidence preservation design. |
| DR-1, DR-8, DF-3, and DF-5 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) | [DR-1](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites) is a prerequisite proposal, and [DR-8](tenant-derived-runtimes-design.md#dr-8-interactions) covers interactions with other features. DF-3 and DF-5 are defects in that design's [current state](tenant-derived-runtimes-design.md#current-state), under which no derived runtime runs a session today. |
| OC-1 and OC-2 | [`tenant-outbound-connector-design.md`, "Proposals"](tenant-outbound-connector-design.md#proposals) | The outbound connector's gateway, agent, and identity (OC-1), and its hosts and policy (OC-2), which reach a private host. |
| F-n.n.n | `BUILD-GAPS.md` at the repository root | Findings filed against the tree, numbered by the specification section they concern. "Defects found while verifying" lists those this design closes. |

## High-level design

A company that runs Lenny for many customers gives each customer its own tenant. Inside one customer, teams often need different network and repository access, and the customer wants its own single sign-on and its own employee directory to decide who can use Lenny. This design adds both. A tenant admin can divide a tenant into environments, each with its own repositories, credentials, allowed and denied hosts, and network routes. The gateway, the egress decision service, the Token Service, and the git mirror each check the session's environment, so an agent running inside a session cannot reach another environment's resources at run time. A tenant can also trust its customer's own identity provider for sign-in, with Lenny checking that one customer's provider cannot speak for another tenant or grant platform authority, and the customer's directory can remove a departing employee's access, including running sessions.

The main limit is that an environment is a policy boundary inside one tenant rather than an isolation boundary. Environments in a tenant share pods, stores, quotas, and egress addresses, so a team that needs isolation uses a tenant of its own. On the identity side, the operator remains inside every tenant's trust through impersonation and through activating sources, and much of the identity design rests on provider behaviour that has not been tested.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Environment scoping (TS-1).** VCS credential pools, credential bindings, user credential types, and link providers gain an `environments` list, and the tenant egress policy gains `environmentPolicies[E]` with host lists and routes per environment. A session in E sees tenant-wide objects and those that list E, and a tenant deny applies in every environment. The gateway checks a session's plan at create, and the decision service, the Token Service, and the git mirror each check again at run time from their own reads of the session row. A pod with no per-session policy cannot apply an environment's entries, so a session whose environment has anything of its own is refused on such a pod.

**Session environment context and cross-environment sharing (TS-3).** A session's environment is fixed at create, and a derived session and a delegated child take it. TS-3 moves the delegation gate from the caller's groups to the session's stored environment, makes the calling session the only delegation parent a runtime can name, limits the intra-pod tool surface to §9.1's platform tool table, which omits the tools that create a root session, and lets a client disable delegation with `maxDepth: 0`. It adds opt-in sharing paths, through which a delegated child runs in another environment under that environment's policy and cannot cross again.

**Identity model and platform boundary (TS-2).** The deployment issuer, which can be an operator's identity broker, stays the default identity source. TS-2 states the broker contract, builds a token's claims from an allowlist, removes `platform-admin` from every IdP token, and confines `platform-admin` rows to one operator tenant.

**Identity sources and the verifier registry (TS-6).** A tenant can instead hold a customer source: the customer's own OIDC issuer pinned to that tenant, or its organization on a listed shared issuer. A `platform-admin` activates a source once its proofs pass, and at a tenant with a `security-admin` the activation also takes [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay and a reachable `security-admin`'s approval. The verifier registry selects the source by the token's `iss`, verifies the token, and takes the tenant from the source record. Subjects and groups carry a source prefix, so two issuers cannot collide.

**Login routing (TS-7).** Per-tenant URLs and metadata documents tell MCP clients and the playground which issuers a tenant accepts.

**User lifecycle (TS-4), SCIM server (TS-8), and directory groups (TS-9).** A directory reaches Lenny through Lenny's SCIM server or through a sync worker, and both end in one invalidation path. TS-4 lets invalidation reach a user known only through an IdP, refuses an invalidated user at authentication, keeps one hold per caller class, and adds `reinstate`. TS-8 serves SCIM, and TS-9 lets a source take groups from SCIM in place of token claims.

TS-5, delegated environment administration, is deferred under decision 7.

```
 Bearer token ──> gateway or Token Service
                    │ verifier registry: unverified iss selects a verifier (unknown iss: refused, no fetch)
                    ├─ deployment issuer: tenant from auth.tenantIdClaim, refused if the tenant disallows it
                    └─ customer source:   tenant from the source record (and pin), canonical subject ~<source_id>~<subject>
                    ▼
                  claim allowlist, role boundary, active-source and invalidation checks ──> principal
                    ▼
 Session create ──> environment E fixed on the session row
                    ▼
 decision service, Token Service, git mirror: each reads E and the object, and serves only visible(objects, E)
```


## Requirements

- R1. A tenant admin can give an environment its own VCS credential pools, credential bindings, host lists, and routes.
- R2. An object with no environment scope keeps the meaning [`egress-and-git-design.md`](egress-and-git-design.md) gives it.
- R3. Scoping holds at run time, against an agent acting inside a session, as well as at session create.
- R4. A narrowing that Lenny cannot enforce on a given pod is refused rather than ignored.
- R5. The specification states the identity model: the deployment issuer by default, a customer issuer per tenant, and what stays with an operator.
- R6. A customer's directory can end a departing user's access to Lenny, including running sessions, within a bound the directory and the operator control, through SCIM or a sync worker.
- R7. No customer-controlled input, including a customer's issuer, can produce authority over another tenant or any platform-wide authority. On a listed shared issuer, this rests on the platform admin's assertion under "Issuer pin and proof".
- R8. One deployment holds tenants on the deployment issuer and tenants on customer issuers at once, and a single-company deployment is unaffected.

## Security properties

- TP1. A session in environment E can use a scoped object only when the object lists E. For credentials, the Token Service and `lenny-egress-authz` each hold this independently, from their own reads of the session row and the object. The mirror's own fetches hold it through the pool and the cause each request names.
- TP2. The gateway sets a session's environment at create and it never changes. A root session's environment is checked against the caller's membership. A delegated child takes its parent's, or under a declared sharing path the target's. Nothing in a pod can set it. A runtime cannot create a root session, because the intra-pod tool surface omits those tools.
- TP3. A pod with no per-session policy applies unscoped objects alone, for every session it serves. Omitting an environment never widens access: a caller cannot leave an environment's denies or routes behind by using the transparent path.
- TP4. A caller receives the same refusal for an object scoped to another environment and for an object that does not exist, so environment object names do not leak across environments.
- TP5. A role claim from an IdP cannot confer `platform-admin`. A `platform-admin` row can exist only in one configured operator tenant, so a subject in any other tenant cannot hold the role. Whichever issuer authenticates the operator tenant can assert a staff subject, so that issuer holds platform authority: with the deployment issuer in that position, a broker compromise reaches every tenant.
- TP6. Environment scoping is a policy boundary inside a tenant. It carries none of the pod pinning or row-level isolation that the tenant boundary has from §12 and from [P4](egress-and-git-design.md#security-properties), the egress design's property that keeps one tenant's data and credentials away from another tenant's pods. "Residual risks" states what follows.
- TP7. A delegated child runs in its parent's environment unless a sharing path applies, so a chain of delegations reaches nothing its root's environment does not offer. Under a sharing path, the child runs under the target environment's policy, the caller's denies and routes do not follow it, and it and its descendants cannot cross again. A runtime can name no other session as a delegation parent.
- TP8. A customer source's tenant comes from the record its verified token selects: by issuer alone on an ordinary issuer, and on an issuer in `identity.sharedIssuers` by the issuer with a verified audience or a claim the platform admin named as provider-set. It never comes from `auth.tenantIdClaim` or a claim a source chooses. The deployment issuer cannot speak for a tenant configured for customer sources.
- TP9. An unverified `iss` selects a verifier and nothing else, an unknown one causes no fetch from that issuer, and pin values are read only after the signature verifies.
- TP11. Every browser login goes through the same resolver as a bearer, an authorization code goes only to the source whose callback received it, and the row its token selects must be that source.
- TP12. A subject whose source is not active is refused on every verifier path, including with a Lenny-issued token.
- TP10. A customer source's subjects and groups are namespaced, so two issuers in a deployment or a tenant cannot collide.

## Current state

Each row was checked against the named source.

| Claim | Status | Source |
| :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.tenantIdClaim` are single Helm values | True. `auth.oidc.clientId` is also single. Each renders to one flag on the gateway and the Token Service. | `charts/lenny/values.yaml`, `gateway-deployment.yaml`, `token-service-deployment.yaml` |
| One IdP verifier exists per process | True. `buildIdPVerifier` and `buildCallerVerifier` build one `OIDCVerifier`, which `TenantClaimVerifier` wraps. | `cmd/lenny-gateway/token_signing.go`, `cmd/lenny-token-service/caller_verifier.go` |
| A per-tenant identity provider exists | Partly. §10.6 **Tenant RBAC Config** lists `identityProvider (OIDC)`. In code, that record holds only `type` (`oidc` or empty) and the RFC 7662 introspection settings. It cannot name an issuer. | `pkg/gateway/externalapi/admin/rbac_config.go` |
| The specification has SCIM | False. No match in `spec/`, `pkg/`, `cmd/`, or `charts/`. | Search |
| The gateway can reach an external issuer | Yes under `allow-gateway-egress-llm-upstream`, the NetworkPolicy that lets the gateway reach external LLM providers on HTTPS, which excludes private ranges and which a CIDR list can narrow. | `gateway-llm-upstream-egress.yaml`, §13.2.4 |
| The Token Service can reach an external issuer | Only through the KMS row. `allow-token-service`, the Token Service's NetworkPolicy, renders `kms.endpointCIDR` on TCP 443 unconditionally. Its default `0.0.0.0/0` opens port 443 with no excepts under the local provider, and a cloud KMS narrows it, which removes the path. | `system-network-policies.yaml`, `_helpers.tpl`, `values.yaml` |
| The playground login uses the verifier chain | False. `buildPlaygroundOIDC` passes the raw `OIDCVerifier`, and `extractSubject` reads the tenant and user from its claims. The authorization code and the PKCE code verifier are posted before the ID token is checked, on one callback path. | `cmd/lenny-gateway/playground_oidc.go`, `pkg/gateway/mcpfabric/playground/oidc.go` |
| `lenny-ctl` logs in | False. It takes `--token` or `LENNY_API_TOKEN`. | §24, `pkg/ctlcli` |
| Groups reach Lenny through tokens or introspection | True. `groups` is a fixed claim name, and introspection replaces the set for a tenant that turns it on. | `pkg/auth/jwt/jwt.go`, `pkg/gateway/middleware/auth/auth.go` |
| IdP tokens can assert roles | True, including `platform-admin`. See "Defects found while verifying". | `Claims.Roles`, `normalizeUserBearer`, §10.2 **Role assignment:** |
| A session records its environment | Yes for a create that names one, in `sessions.environment`. Absent for a transparent-path create. No code sets it on a delegated child or a derived session. | `create.go`, `derive.go`, `replay.go`, `delegation/service.go` |
| A precedent for environment-scoped credentials exists | Yes. User credentials and connector tokens carry an `environment` column in which the empty string means the no-environment scope (§4.3 and migration 0059). | `credentialstore.go` |

## TS-1: Environment scoping

### A session's environment

TS-1 relies on [TS-3](#ts-3-session-environment-context-and-cross-environment-sharing), which makes `sessions.environment` dependable. A session has a value E when its create named an environment through `/v1/environments/{name}/sessions`, `/mcp/environments/{name}`, a scoped model namespace, or the `environment` body field. All of these already run the explicit admission check (membership, at least `creator`, and the runtime selector). A transparent-path session has no E, and the formulas below write that as E = ∅.

A new session with E = ∅ (a root create, a derive, or a delegated child) is refused with `422 ENVIRONMENT_REQUIRED` when any environment in the tenant whose selector admits the runtime has `denied` or route entries. The check reads the tenant's environments and takes no input from the caller's groups, so a token with no `groups` claim, under either `noEnvironmentPolicy` value, cannot shed an environment's policy. A resume is exempt, so a deny added later does not strand a running session. A caller who belongs to no such environment has no path to that runtime, including an administrator who is not a member, and a `tenant-admin` resolves that by adding the member or by giving the environment its own runtime, or by narrowing the selector so that the environment no longer admits the runtime. On a concurrent or service-mode runtime, an added member still meets the rule under **Pods with no per-session policy**, in "Effective policy" below. See decision 3.

Experiment routing and a late `PreRoute` interceptor can rewrite a session's runtime and pool after admission (`routeExperiment` and `preRouteAfterExperiment` in `create.go` and `create_and_start.go`, and `ApplyExperimentRouting` in `buildChildSession`). The runtime-dependent checks in this section therefore run again on the final row: the selector for E, `ENVIRONMENT_REQUIRED`, the rule for pods with no per-session policy, the refusal of `lockEgressToHosts` on a pool with `egressProfile: internet`, and the refusal in [`user-credential-delivery-design.md`](user-credential-delivery-design.md) of a `userCredential` binding on a pod that can serve another session. A miss on the tenant view of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), the read interface through which every session path resolves the runtimes a tenant can see, refuses too, on any name, for a rewrite and for a delegated child. A derive naming a `targetPool`, the derive request's optional field that names the pool for the derived session, runs the same checks, because a child under `lockEgressToHosts` rewritten to a pool with `egressProfile: internet` would otherwise bypass the lock through its raw sockets. A rewrite that fails one refuses the create, the derive, or the delegation, as `VARIANT_ISOLATION_UNAVAILABLE` does, the refusal experiment routing returns today when a variant pool's isolation is weaker than the session's minimum. A pool that is not known until the pod claim, rather than at admission, is checked at claim, before bind.

### What is scoped

Scoping applies to objects the platform evaluates per session.

| Object | Scoped | Reason |
| :-- | :-- | :-- |
| VCS credential pool | Yes, through `environments` | Selected per repository at mint time |
| Credential binding | Yes, through `environments` | Resolved per session on the pod credential API |
| User credential type and link provider | Yes, through `environments` | Resolved per session on the pod credential API, under [`user-credential-delivery-design.md`](user-credential-delivery-design.md). The binding that names one is scoped as above, and a session needs both visible. |
| A user's token (a `registered` row or a link) | The `registered` row has an `environment` column, and §4.3's empty value means no environment. A link has none, and it is keyed on the link provider's name. | The Token Service takes the session's E, then the no-environment row. A link records a fact about a person on a provider, so a member of two environments links once. |
| `requestRules` and presets | No | They are part of a binding, which is scoped. An environment that needs other rules has its own binding on that host, and the one-binding-per-host rule applies per session. |
| Always-allowed, session-requestable, and denied hosts | Yes, through `environmentPolicies` | Read per decision |
| Egress routes and git routes | Yes, through `environmentPolicies` | Read per decision. A route can name a connector once [OC-1](tenant-outbound-connector-design.md#proposals) (the outbound connector's gateway, agent, and identity) lands, with no further change. A token trust record covers every such route on its host, across every environment, as [`user-credential-delivery-design.md`, "Token trust record"](user-credential-delivery-design.md#token-trust-record) states under **Route value**, so one environment's scoped route, a move between environments, or a reorder on a token's host makes that token unusable for all environments, and an asserted route, the expected route that a call carrying a user token names on its CONNECT and that the decision service compares with its own choice, uses `routes(E)` for the session's E. The covered host set reads every git route and VCS pool of the tenant, unfiltered by environment, so a session in E2 on a host only E1 names gets a refused push rather than an opaque tunnel, in which `netagent` would pass the connection through without terminating TLS, as [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) states. |
| Tenant warm-up additions | No | They apply before any session exists. |
| Tenant pool overlay | No | It applies at pod creation or per pool and tenant. |
| Egress group and CA trust bundle | No | Each is fixed at pod creation or dialled by tenant. |
| Client config files, such as `.npmrc` and `pip.conf` | No | `netagent` writes them at tenant binding. See decision 4. |
| Per-class gates | No | They remain platform grants per tenant. |
| Tenant-registered upstreams and git endpoints | No | The registration is tenant-wide, and a scoped route selects it. |

### API

The objects gain these fields through the admin API resources that the egress design's proposals [EG-1](egress-and-git-design.md#proposal-split) (git prerequisites), [EG-4](egress-and-git-design.md#proposal-split) (credential delivery), and [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects) define:

```yaml
vcsCredentialPool:
  name: web-github
  repositoryPrefixes: ["github.com/acme-web/"]
  environments: [web]            # absent: tenant-wide

credentialBinding:
  name: payments-stripe
  scope: onRequest
  hosts: [api.stripe.com]
  environments: [payments]

tenantEgressPolicy:
  alwaysAllowed: [npm, pypi]
  sessionRequestable: [huggingface.co]
  denied: [pastebin.com]
  environmentPolicies:
    payments:
      alwaysAllowed: [stripe-api]
      sessionRequestable: []
      denied: [huggingface.co]
      egressRoutes: [{ hosts: ["*.stripe.com"], upstream: acme-pci-swg }]
      gitRoutes: []
```

The rules for these fields are:

- `environments` is either absent or a non-empty list. An empty list is refused with `422 VALIDATION_ERROR`, because it has no clear reading.
- Every name must be an existing environment in the tenant, or the write fails with `422 ENVIRONMENT_NOT_FOUND`. An `environmentPolicies` key follows the same rule.
- A deployment with no environment registry wired refuses these fields with `422 ENVIRONMENTS_NOT_AVAILABLE`, since §10.6 admission runs open in that posture.
- `DELETE /v1/admin/environments/{name}` fails with `409 RESOURCE_HAS_DEPENDENTS` while any object or policy key names the environment. A recreated environment therefore inherits nothing.
- A tenant warm-up addition, a `gitRepository` warm fill, or a warm-list entry of either declarer, whether it adds a checkout or is cache-only, cannot name a repository whose only covering pool is scoped, because a pod with no session has no E (decision 28). The write is refused unless the repository lies under no VCS pool prefix, or credential selection at E = ∅ resolves it to a VCS pool with no `environments` list whose allowlist names it, which is the predicate the fill evaluates. A tenant's write of a warm-list entry answers `422 INVALID_DERIVED_RUNTIME`, and a platform admin's write of a private entry on a tenant-owned pool's row answers `422 INVALID_POOL_CONFIGURATION`.
- A VCS pool write that makes a warm-list entry fail that predicate, such as one that adds `environments` to the covering pool or deletes or narrows a tenant-wide pool that covered it, passes with a `Warning` naming each runtime and entry it affects. The PoolScalingController applies the same predicate at each render, so the entry leaves the rendered list and is reported as suspended with the reason `environmentScoped`. The drain of [`tenant-derived-runtimes-design.md`, "Warm clones"](tenant-derived-runtimes-design.md#warm-clones) then reaches idle pods holding the store, `coveredStores` refuses such pods at the next claim, and the entry returns once the pool is unscoped again.
- Two pools can hold the same repository prefix only when their `environments` lists are disjoint, and neither may be tenant-wide. Selection takes the longest prefix among the pools visible to E, so exactly one pool answers for any repository and environment. This lets two environments use separate GitHub Apps on one organization.
- A credential binding's `hosts` must lie within the allow set of each environment the binding lists, that is `always(E) ∪ requestable(E)` for each E, or within the tenant's set for a tenant-wide binding. TS-1 extends the "each host must lie within the tenant's effective allow set" validator of [EG-4](egress-and-git-design.md#proposal-split) in this way, or the `payments-stripe` example above would fail.
- A mirror prefetch request names a pool whenever more than one pool covers the repository, and only a tenant admin can name a scoped pool.
- A shadow policy's candidate carries its own `environmentPolicies`, and each decision evaluates the candidate with the session's E. `environments` lists sit on objects and stay outside the candidate.
- A write that adds `denied` or route entries to an environment answers with a `Warning` header and a count when live sessions in it run on pods with no per-session policy, because those sessions cannot adopt the entries.
- A write that adds an environment's first `denied` or route entry answers with a `Warning` header listing the runtimes its selector admits, because transparent creates on those runtimes will now answer `ENVIRONMENT_REQUIRED`. A tenant admin learns this before those clients break. A selector or runtime label change that extends such an environment to new runtimes answers with the same `Warning`.

The effective-policy read gains an optional `environment` parameter and lists `environment` among the layers that decided each host. `GET /v1/egress/decisions` and the evidence export gain an `environment` filter. Both wait for [EG-8](egress-and-git-design.md#proposal-split), the egress design's tenant visibility proposal.

### Data model

- VCS pool and binding rows gain a nullable `environments` text array. NULL means tenant-wide.
- The tenant egress policy document gains `environmentPolicies`, keyed by environment name.
- Neither is a foreign key, following the reasoning in migration 0059. The write check and the delete guard hold the reference.
- The existing `sessions.environment` column holds E.
- All tables are tenant-scoped under the existing row-level security policy.

### Effective policy

Define the following, where E = ∅ selects no environment entries:

- `visible(objects, E)` is the objects with no `environments` list, plus those whose list contains E.
- `always(E)` is tenant always-allowed ∪ `environmentPolicies[E].alwaysAllowed`.
- `requestable(E)` is tenant session-requestable ∪ `environmentPolicies[E].sessionRequestable`.
- `denied(E)` is tenant denied ∪ `environmentPolicies[E].denied`.
- `routes(E)` is `environmentPolicies[E]` routes in order, followed by tenant routes in order.

The formulas from [`egress-and-git-design.md`, "Effective policy"](egress-and-git-design.md#effective-policy) become:

| Case | Formula | Change |
| :-- | :-- | :-- |
| Unbound pod | `ceiling ∩ (pool warm-up policy ∪ warm-list hosts)` | The warm list's hosts, from the egress design's decision 45. No environment entry applies. |
| Tenant-bound pod without a session | `ceiling ∩ (always(∅) ∪ tenant warm-up additions ∪ warm-list hosts) − denied(∅)` | Environment entries do not apply. The warm list's hosts join, from the egress design's decision 45. |
| Pod with one session | `ceiling ∩ session allowed − denied(E)` | `session allowed` is the plan's list, or `always(E)` when the plan sets none. Each list entry must lie within `always(E) ∪ requestable(E)`. |
| Pool with `egressProfile: restricted` and `gitAccess` | `gitAccess hosts ∩ git hosts(E) − denied(E)` | `git hosts(E)` takes hosts from `visible(VCS pools, E)` and from the explicit git routes in `routes(E)`. A session carrying `egress.allowedHosts` or `egressLockHosts`, its own or inherited, intersects this set with that list. |
| Pod with concurrent sessions | `ceiling ∩ always(∅) − denied(∅)` | Environment entries do not apply, for the mechanical reason the original formula states. See **Pods with no per-session policy.** |
| Delegated child in the parent's environment, on a one-session pod or a pod with `egressProfile: restricted` | The one-session formula, or the `restricted` row, with the parent's E, and the parent's `egress.allowedHosts` and named bindings as its inputs | The rule in [`egress-and-git-design.md`](egress-and-git-design.md). |
| Delegated child under a sharing path to T, on a one-session pod or a pod with `egressProfile: restricted` | The one-session formula, or the `restricted` row, with E = T, and `session allowed` = `always(T)` | No input comes from the parent, and no term comes from the caller's environment. `denied(T)` includes the tenant's list. |

Consequent rules:

- **Bound.** The pool ceiling bounds every environment entry. A tenant deny applies in every environment, so an environment cannot re-allow a host the tenant denies.
- **Repositories.** Readable repositories are the session's `gitClone` sources, the allowlists of `visible(VCS pools, E)`, and the public platform entries of the pool's warm list. Before a session binds, a pod's readable repositories are the pool's warm list plus the repositories of the warm-up policy that applies to it, with no environment entry applying, and every entry on the list passes the predicate under "API" at E = ∅. Writable repositories must lie within `visible(VCS pools, E)` at bind and at each mint.
- **Bindings.** A session attaches bindings with `scope: always` from `visible(bindings, E)`, and it can name bindings with `scope: onRequest` from the same set.
- **User credentials.** A `userCredential` binding needs its type visible too, as `visible(types, E)`. It takes `scope: onRequest`, or `scope: poolAttach` under [`user-git-identity-design.md`, "The attach rule"](user-git-identity-design.md#the-attach-rule), whose trigger is a declared writable repository under such a pool in `visible(VCS pools, E)`. The credential-pinned host rule (`credentialPinned`) of a VCS pool with `userAuthorization: required`, under which `netagent` terminates TLS for the pool's hosts and refuses a request when the host has no binding, is outside `visible`, because it reads every VCS pool of the tenant. The pod refusal in [`user-credential-delivery-design.md`](user-credential-delivery-design.md) covers E = ∅ on a concurrent or service-mode pod, because those pods refuse such a binding outright.
- **Pods with no per-session policy.** A concurrent-session pool or a service-mode pool evaluates with E = ∅ for every session on the pod, including at the Token Service and in the readable and writable sets. [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) makes those sets the union over the pod's sessions, because `netagent` cannot attribute a request to a slot, so a scoped repository on such a pod would be open to every slot. The rules follow from this:
  - A plan that names an object visible to E alone (for example a repository whose only covering pool is scoped) is refused with `422 ENVIRONMENT_POLICY_NOT_ENFORCEABLE`.
  - A session whose E has non-empty `denied`, `egressRoutes`, or `gitRoutes` is refused with the same code (R4). The check runs at create, derive, resume, delegated child placement, and each service-mode request that names an environment, on the final runtime and pool.
  - A session whose E has anything of its own that adds access is refused with the same code: an `alwaysAllowed` or `sessionRequestable` entry, or a VCS pool, binding, type, or link provider that lists E. A runtime is not guaranteed to work without access its environment provides (decision 15). The check runs at the same points, except a resume and later service-mode requests, where it runs at session create alone, because a session that began without an entry keeps working without it, and an entry added later does not strand it.
  - A session or delegated child carrying `egress.allowedHosts` or `egressLockHosts`, its own or inherited, is refused with the same code (R4), because the pod cannot apply either to one slot. An injected agent could otherwise delegate to such a pod, losing its parent's lock, and send a token delivered under `delivery: file` to any tenant always-allowed host. Check 2 under "Cross-environment sharing" refuses a locked crossing for the same reason. This check runs at every point above, including a resume.
  - Otherwise E has nothing of its own, and the session proceeds with unscoped objects. A refused caller can still use the transparent path when no environment admitting the runtime has a deny or route. That session has E = ∅ and widens nothing, and it runs without the environment's access. The owner left this allowed, in the words "leave allowed" (decision 15).
  - An environment that needs such a runtime uses a derived runtime and its owned pool under `oneSessionPerPod: true`, under a platform grant, whose pods are one-session pods with E's full policy. That route does not work today, because no derived runtime runs a session and §15.1 reserves creating a runtime or a pool to a `platform-admin`. The owner chose [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) as the design that lets a `tenant-admin` holding a grant do this. It gives a service-mode runtime no route.
  - The `Warning` under "API" above also covers a write that gives an environment its first entry or scoped object, listing the concurrent and service-mode runtimes its selector admits.

### Enforcement points

The session create check alone is insufficient. An agent can run `git fetch` at any time against any repository on the tenant's allowlist, and bindings with `scope: always` attach with no plan entry, so most of the enforcement happens after create.

| Point | Component | Check |
| :-- | :-- | :-- |
| Session create, derive, resume, and delegation | Gateway | Named bindings, `gitClone` sources, `writableRepositories`, and `egress.allowedHosts` lie within the sets above for E, and the refusal rules for transparent creates and for pods with no per-session policy apply. |
| Ref resolution: the gateway's git `ls-refs` request, which resolves a `gitClone` ref to a commit at plan bind | Gateway through the egress decision API | The decision service reads E from the session the request names. No new header reaches a level 2 proxy. |
| CONNECT | `lenny-egress-authz` | The formula for the pod's case, with E from `pkg/binding`. |
| Mint or secret on the pod credential API | Token Service, then the egress decision API | Each component reads E and the object, and both must agree (TP1). Nothing is minted on refusal. |
| Serving from the mirror | Mirror through the egress decision API | The resolver selects the pool for the repository and E, and readable repositories for E are checked on every request. A store's identity in [`egress-and-git-design.md`](egress-and-git-design.md) includes the credential, so same-prefix pools in different environments use different stores. |
| Filling the mirror | Mirror to the Token Service on the pod credential API | The request names the pool and its cause: the calling pod's UID, the session for a gateway `ls-refs`, or the prefetch record. The Token Service reads that pod's binding, that session, or that record itself, and refuses a pool not visible to the resulting E. Warm fills reach unscoped pools alone, so the write and render rules under "API" keep a warm-list entry whose repository only a scoped pool covers off every warm list. |
| Git path and binding hosts | `netagent` | No new logic. It enforces what the pod binding API and the pod credential API return, and it flushes credentials at session end and on each binding change, so a sequentially reused pod does not carry a credential across sessions. |

`netagent` needs no environment concept. The cache key [`egress-and-git-design.md`](egress-and-git-design.md) uses for bindings with `scope: always` (the tenant pin) stays safe because the flush at session end precedes the next session.

### Failure behaviour

| Condition | Result |
| :-- | :-- |
| An object outside E is named at create | `422` with the same code and message as an unknown object (TP4) |
| Environment store or policy read fails at create | `500 INTERNAL_ERROR`, as admission does today, and no session is created |
| `pkg/binding` cannot read the session row | Fails closed, with the existing retryable error for a binding that is not yet visible |
| A tenant admin removes E from a pool or binding during a session | The object's version bumps, the gateway pushes a binding change, `netagent` flushes, and the next request on the pod credential API is refused. The session continues without it. |
| A tenant admin removes a host from `requestable(E)` during a session | Applies to new sessions, as the tenant list does in [`egress-and-git-design.md`](egress-and-git-design.md). Adding the host to `denied(E)` closes it at the next decision. |
| A member leaves the environment during a session | The session keeps E, because membership is a check at create. See "Residual risks". |
| A delegation target with neither the parent's selector nor a live two-sided sharing path | `TARGET_NOT_IN_SCOPE` |
| A child reached by crossing, or its descendant, tries to cross | `TARGET_NOT_IN_SCOPE`, with the audit reason `second_crossing` |
| A resume meets a deny added to E after the session began on a pod with no per-session policy | `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, and the session stays suspended until its owner ends it or the tenant admin changes the policy. |
| An environment vanishes under a live session through a store-level race | `visible` finds no entries for E, which narrows the session to unscoped objects. |

### Who can do what

| Action | `platform-admin` | `tenant-admin` | Environment `admin` | Environment `creator` | `user` with no role in E |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Set `environments` or `environmentPolicies` | Yes | Yes, own tenant | No, unless TS-5 (delegated environment administration) lands | No | No |
| Read the scoped objects | Yes | Yes | No admin read | No | No |
| Create a session in E | No, unless a member | No, unless a member | Yes | Yes | No |
| Name a scoped object in a plan | Only in a session in E | Only in a session in E | Yes, in E | Yes, in E | No |
| Set `dangerouslyAllowCrossEnvironmentSharing` | Yes | Yes, own tenant, subject to [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) | No | No | No |
| Add or remove a `dangerouslyShare` entry | Yes | Yes, own tenant, subject to [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), as does a custom role holding `manage_environments` | No | No | No |

Writes pass through the single write path for tenant opt-ins that the egress design's proposal [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects) adds, so they carry its audit event and any [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) guardrail. A `tenant-admin` remains able to scope any object to any environment and add itself as a member, which matches §10.2's statement of tenant authority.

### Why not separate tenants

A customer can give each team its own tenant today. That yields tenant pinning, row-level isolation, and separate keys, which environments do not provide. It also means no shared policy, no shared admin, no combined billing rollup, and no delegation between environments, since Lenny has no grouping above a tenant. Teams needing an isolation boundary should be tenants, and teams needing separate configuration under one administration should be environments. The documentation states this choice.

### Does TS-1 need a hook

The outline, which was the first version of this document, said no, on the basis that TS-1 adds an optional field and a check at create. That holds for the fields, which are additive. It does not hold overall, because enforcement is at run time in components [EG-2](egress-and-git-design.md#proposal-split) (the pod network agent and proxied egress) ships, evidence records are written from day one, and [EG-1](egress-and-git-design.md#proposal-split) (git prerequisites) fixes the prefix rule. Revision 38 of [`egress-and-git-design.md`](egress-and-git-design.md) carries those hooks. The mirror's request on the pod credential API needs one more, which "Changes requested of egress-and-git-design.md" states.

## TS-3: Session environment context and cross-environment sharing

TS-1 cannot rest on the current propagation. TS-3 lands first and depends on nothing in [`egress-and-git-design.md`](egress-and-git-design.md), apart from the evidence field step the dependency table names.

- **Derive.** The new session takes the source's environment, and the explicit admission check runs against it. A caller who has left the environment gets `403`.
- **Delegated child's environment.** The child takes the parent's E, set on the row `buildChildSession` writes, when `runtimeSelector` of the parent's E admits the final runtime. Otherwise it needs a sharing path under "Cross-environment sharing", and with none it answers `TARGET_NOT_IN_SCOPE`.
- **The gate reads the session.** A call from inside a pod authenticates as a principal with no groups (`WithSessionPrincipal` builds it), and the delegation gate's checks, `runtimeAuthorizedForCaller` and `crossEnvReachable`, resolve environments from that principal. A session whose owner belongs through a group therefore has no environments in the delegation gate. Under `noEnvironmentPolicy: deny-all` its delegations answer `TARGET_NOT_IN_SCOPE`, including within its own environment, and under `noEnvironmentPolicy: allow-all` every runtime is admitted, regardless of selectors. Tests set `Groups` on the principal directly, which hides this. TS-3 gates a session with E on the session's E and the live declarations, with no reference to the principal or to the owner's membership. A session with E = ∅ keeps the existing check, adds `ENVIRONMENT_REQUIRED`, and cannot cross.
- **The parent is the calling session.** The delegation tools take the parent session from their arguments, and the gateway accepts any session with the same owner: `lenny/delegate_task` reads `parentSessionId` and `lenny/await_children` reads `sessionId`, and `lookupCallerSession` admits any session of the same owner. A runtime can therefore name another session as parent and escape its limit, its environment, and its crossing mark, or read another session's child results. When the principal carries `SessionID`, as `WithSessionPrincipal` sets it, TS-3 uses that session on both tools and answers `403 PARENT_SESSION_MISMATCH` to a different value, as `callerSessionID` does for `lenny/get_task_tree`. A client at the gateway edge still names its parent, and the rules read that session's row. F-10.2.29 is separate: it is the finding that the gateway serves a call a pod's runtime adapter forwards under whatever session ID the adapter presents, so a compromised adapter can present another session's ID, and this rule does not stop that. TS-3 does not depend on F-10.2.29, and its tier 9 tests cover a runtime's tool calls and no forged adapter call.
- **The intra-pod tool surface.** `platformtools.Bridge` forwards any registered tool, so a runtime can call `lenny/create_session` or `lenny/create_and_start_session` and start a new root with no limit, another environment, and no crossing mark. TS-3 allow-lists the Bridge's list and call to §9.1's **Platform MCP Server Tools** table, which omits those tools, and any other name answers `ErrPlatformToolNotFound`. The list also holds `lenny/vcs_token` until [EG-3](egress-and-git-design.md#proposal-split), the egress design's pod git path, removes that tool, because `git-credential-lenny` calls it over the platform MCP socket, so TS-3 keeps depending on nothing. The gateway edge is unchanged. A later proposal for F-10.2.32, the finding that the MCP and adapter session surfaces skip §10.2's permission matrix, must not reopen this by exempting in-pod principals.
- **Fail closed.** The session gate refuses on every read error, where `runtimeAuthorizedForCaller` returns true on a `Runtimes.Get` error today. [DR-8](tenant-derived-runtimes-design.md#dr-8-interactions) in [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), which covers that design's interactions with delegation and environments, depends on this delegation gate. `lenny/discover_agents` answers from the session's E and its live sharing paths, in place of the principal's groups, and it omits sharing paths from a session under `lockEgressToHosts`.
- **Effects on existing features.** Billing rollup, retention tier, default delegation policy, §4.3 credential scope, and connectors read the same field, so a child and a derived session follow the session's environment in each.
- **Deletion guard.** §15.1 says deletion of an environment is blocked while sessions are active in it. `handleDeleteEnvironment` and the store's `Delete` contain no such check. TS-3 adds it.
- **Naming.** §10.6 **V1 data model accommodations** describes `environmentId` on session creation as a no-op stub. The code reads a body field named `environment` and enforces it, and `?environmentId=` on `GET /v1/runtimes` narrows the list. The specification changes to match the code.
- **Transparent create.** `requireEnvironmentAdmission` admits any member of any environment on the transparent path, with no role check or selector check. A `viewer` can therefore create a session on a runtime the transparent filter hides from it. No other check was found in `pkg/gateway/sessionserver`. TS-3 requires at least `creator` in an environment whose selector admits the runtime. TS-1 adds the `ENVIRONMENT_REQUIRED` refusal to the same check.
- **Groups on Lenny-issued tokens.** `mintToken` writes no `groups` claim, so a token from `/v1/oauth/token` cannot satisfy an `oidc-group` member, and §13.3's claim structure does not list one. The playground mint already copies the subject's groups. TS-3 copies them on exchange in the same way. No deny or route depends on this, because `ENVIRONMENT_REQUIRED` reads no groups.

### Client delegation limits (decision 26)

The owner settled this on 2026-11-03, in the words "yes, add it with maxDepth: 0". `delegationLease.maxDepth: 0` at session creation means this session cannot delegate. It needs no gate, because it only narrows. TS-3 carries it, because it reworks the delegation gate, and it closes F-8.2.34 apart from `delegationPolicyRef`.

**Today.** The field has no effect. `DelegationLeaseRequest` is validated, stored, and echoed, a root row's `DelegationLease` stays nil, and `mcptools_register.go` leaves `Request.MaxDepth` unset, so `checkDelegationDepth` resolves the Helm fallback on every hop. `sessionstore.DelegationLease` has no `MaxDepth` field, which breaks §8.2's "never re-resolved against live Helm values mid-tree". Zero has four meanings: "no further delegation" on `DelegationLeaseRequest`, unset in `ResolveMaxDepth`, "inherit" on `Request.MaxDepth`, and no limit in `ValidateChildSlice`.

**One reading of `maxDepth`.** Absent means this layer sets nothing, and 0 means none.

- `sessionstore.DelegationLease` gains `MaxDepth` as a pointer, written on every root and child, so no later hop reads Helm. `IsZero` gains a clause for it, with a test, because a stored `{maxDepth: 0}` would otherwise read back as NULL and fail open.
- `MaxDepthInputs.ExplicitClient` becomes a pointer. `Request.MaxDepth` goes, because `delegate_task` has no such input and a child takes its parent's stored value.
- `CheckDepth` answers refusal for 0, in place of `ErrUnresolvedMaxDepth`.
- A nil stored value refuses delegation, since nothing is deployed and every creator writes a value.
- Depth comes from `parent.DelegationDepth`, fixed at admission. `buildLineage`'s `walkCap` reads Helm today, so a lowered value would make depth stick and the stored limit unreachable. The lineage walk gets its own bound.

**`maxChildrenTotal`.** A client's `maxChildrenTotal: 0` becomes a stored `maxDepth: 0` at create, so the two fields agree. The integer, the budget slice arithmetic, the Redis Lua scripts that maintain the counters, and `Dimensions` stay as they are, and 0 in the stored integer keeps meaning no limit. A positive value goes on the root's lease as a per-parent cap, because the counter key is `children_total:<parent>` and a child with no slice stores none, so it does not bound a tree. The documentation says so. `maxDepth` already cannot be extended under §8.6, and the code requests extension of `Tokens` alone, so no extension rule is needed.

**Resolution.** §8.2's "first value present" read as the client's value winning. It becomes a minimum, so a client can only narrow:

- `base` is the minimum of the values present among the preset entry, the Runtime's default lease, and the `DelegationPolicy` ceiling. When none is present, `base` is `gateway.delegation.defaultMaxDepth`.
- The effective value is the smaller of the client's value and `base`, and it is `base` when the client sets none.
- A child stores its parent's value, and the stored value is compared with depth from the root, so 0 refuses every child and no child can widen it.
- Only a client can supply 0. The preset, Runtime, policy, and Helm layers stay at 1 or more, and `gateway.delegation.defaultMaxDepth` must be at least 1. It is not a chart value today and no template passes it to the gateway (F-8.3.12), so TS-3 defines it in `values.yaml` and the schema with a minimum of 1 and renders it to `--delegation-default-max-depth`.

Only the Helm layer is wired today, because `adm.policyCeiling` is 0 and the code comment calls presets and Runtime defaults future work. TS-3 wires the client layer and applies the minimum over whichever layers exist. A preset above a Runtime default used to win under the written order and now loses, though neither layer exists in code.

**Which fields the wiring reaches.** `maxDepth` and `maxChildrenTotal`. `delegationPolicyRef`, the lease field that names a `DelegationPolicy`, stays as it is today: it is accepted, stored, and echoed, and it has no effect, because `detectCycle` reads the target's value and `stampLeasePolicy` overwrites the child's. §27.4's playground sets it, and TS-3 refuses and removes nothing there. That part of F-8.2.34 stays open. The other §8.2 lease fields, such as `maxTokenBudget`, and §8.3's preset forms stay absent from the request.

**Every root creator.** `POST /v1/sessions`, `POST /v1/sessions/start` and `lenny/create_and_start_session`, whose request gains the field, `BindSingleShot`, which creates the session behind a single request on `/v1/responses` or `/v1/chat/completions`, and derive each write a resolved value. A derive starts a new root at depth 0, so it stores the source's value minus the source's depth, with a floor of 0, and it has no field to narrow it. A resume keeps its row. Tree recovery brings a node back on its stored lease record, as §8.10 requires, so it cannot re-issue a wider one.

**Enforcement.** The gateway refuses `lenny/delegate_task` for that session with `403 DELEGATION_DISABLED`, category `POLICY`, not retryable. No such code exists in `spec/`, `pkg/`, `tests/`, or `docs/`. The check runs at the top of the handler and again in `validateDelegation`, and it reads the calling session's stored lease, so a runtime's direct call gets the same answer.

**Hiding the tool.** Hiding is a convenience, and the refusal is the control.

| Surface | Can it be hidden? |
| :-- | :-- |
| Intra-pod `@lenny-platform-mcp` `tools/list` with one session | Yes. `ListPlatformTools` already takes the session identifier, and `Bridge.ListPlatformTools` ignores it today, so it drops `lenny/delegate_task` there. `lenny/discover_agents` answers with no targets. |
| Intra-pod list on a process with no session or several | No. `platformToolProvider.List` refuses it, as it does today. |
| A runtime that cached the list | No. A recycled process, or an SDK-warm one whose agent process is already running before its first prompt (§6), can hold an earlier list, and a direct call gets the refusal. |
| Gateway-edge `/mcp` `tools/list` | No. `Catalog()` is one shared list, and a connection names no parent session until a call does. |

### Cross-environment sharing (decision 14)

The owner settled decision 14 on 2026-11-03: keep cross-environment delegation, under option B with guardrails, in the words "go with B with these guardrails". Option B is opt-in sharing under which a crossing child runs under the target environment's policy unchanged, as "Model" below states. TS-3 carries it, because it needs TS-3's session-based delegation gate and child propagation, which also close F-10.6.15 and F-10.6.16.

**Model.** A crossing child runs under the target environment's policy, unchanged: the target's allow list, denies, routes, bindings, pools, and repositories. A shared runtime has the same egress and credentials for every caller. Nothing in those sets is intersected with the caller's. A child still takes what binds any child from its parent, which "Delegation inheritance" under "Terms from other designs and the platform" describes: isolation monotonicity, `treeVisibility`, `contentPolicy`, and its stored `maxDepth`. What moves across is the `TaskSpec`'s `input` and exported files, `lenny/send_message` between parent and child, and between siblings under that scope, and `runtimeRef` in `lenny/get_task_tree`. Its results return to the caller.

**Opt-ins.** Sharing is off for each tenant by default, and it needs both of these:

```yaml
tenant:
  dangerouslyAllowCrossEnvironmentSharing: false   # default

environment:                     # web
  dangerouslyShare:
    outbound:
      - targetEnvironment: platform
        runtimes: { matchLabels: { shared: "true" } }
environment:                     # platform
  dangerouslyShare:
    inbound:
      - sourceEnvironment: web
        runtimes: { matchLabels: { shared: "true" } }
```

`dangerouslyShare` replaces `crossEnvironmentDelegation`, in the same two-sided form. `runtimes` must be a non-empty selector on both sides, because `Selector.Matches` with no clauses admits every runtime. `ENVIRONMENT_NOT_FOUND` and `RESOURCE_HAS_DEPENDENTS` cover these names, so a recreated environment inherits no path. With the tenant setting off or forbidden (see guardrail 5), a write that adds an entry answers `422 CROSS_ENVIRONMENT_SHARING_DISABLED`, and a removal always succeeds. Existing declarations stay stored and have no effect, and they apply again if the setting returns, which posture lists. A `tenant-admin` writes both sides, so the two-sided form records each environment's intent and does not separate two people. No name in the tree conflicts with these.

**The check.** On each `delegate_task` whose final runtime the parent's selector does not admit, the delegation gate requires all of these, reading them live as §10.6 **Cross-environment check evaluation semantics** states:

1. The session's stored lease permits delegation. A session under `maxDepth: 0` can never cross, whatever the environments declare, and it gets `DELEGATION_DISABLED` first.
2. The session is not under a lock, its own or one inherited from an ancestor, because that lock limits every descendant to its hosts and a crossing child runs under T's list. A child records the lock as `egressLockHosts`, with the inherited inputs the delegated child rule stores on its session row, and each descendant and derived session copies it, so a session holding no binding is still under it. [UC-5](user-credential-delivery-design.md#uc-5-delivery-file), the user credential design's `delivery: file` proposal, owns the field. Otherwise this answers `TARGET_NOT_IN_SCOPE`.
3. The tenant setting is on and not suspended (see guardrail 5).
4. The parent's E is not ∅, and its `crossed_from_environment` is null.
5. An outbound entry in E's list names a target T and its `runtimes` admit the runtime. The first such entry in list order that also passes the next two checks selects T, so the source environment controls the order.
6. T's `runtimeSelector` admits the runtime.
7. An inbound entry in T's list names E, and its `runtimes` admit the runtime.

For a session with a crossing mark, every delegation, including one inside T, also requires that the tenant setting is on and not suspended, that the marked environment's outbound list still names T, and that T's inbound list still names the marked environment. Otherwise it answers `TARGET_NOT_IN_SCOPE`, so a forbid or a removed declaration stops a crossed subtree from delegating further. Sessions already running continue.

`CrossEnvironmentReachable` returns a boolean today, and the child needs T, so it must return T. Its wildcard match on `*` goes. The check runs again on the final row after experiment routing, and a rewrite that leaves the path, changes T, or changes whether the hop crosses refuses the delegation, because §8.3's `inherit` check ran on the earlier answer. `credentialPropagation: inherit` is the §8.3 delegation setting under which a child draws its LLM credential from its parent's credential pool. An in-flight child is not revoked when a declaration or the setting changes.

**Guardrails.**

1. **Tenant denies.** The tenant's `denied` list applies in every environment, because `denied(T)` includes it. `environmentPolicies[E].denied` of the caller does not apply to the child.
2. **User authorization.** The child keeps its parent's user (§8.2 **Child identity.**), so a mint on a VCS pool in T with `userAuthorization: required` checks Alice's own access to that repository, and TS-4's invalidation levels stop it. This covers repository reads and writes on VCS pools with `userAuthorization: required`. It does not cover a pool with `userAuthorization: off`, where the App's token reaches T's allowlist for any caller, a stored API key under `delivery: inject`, a tenant-wide or scoped binding with no user, a binding with `delivery: federation` or `delivery: metadata`, a host on T's allow list, connectors, or LLM credentials. A `userCredential` binding crosses only when the delegation lease names it and T can see the binding and its type, and `caller_type` is copied as [`user-credential-delivery-design.md`](user-credential-delivery-design.md) states.
3. **No second crossing.** A child stores its parent's `maxDepth` across a crossing, so the target's policy does not widen it. The child's row records `crossed_from_environment`, a new nullable column holding the caller's E. A session with a value cannot cross, which closes transitive chains. A same-environment descendant stays in T, copies the value, inherits its parent's inputs under the ordinary rule, and can delegate to runtimes T's selector admits. It cannot cross either. A derived session copies the value and meets the explicit admission check for T.
4. **Named environments only.** `sourceEnvironment: "*"` and any wildcard source or target are refused at write with `422 VALIDATION_ERROR`. The sharing side names who it trusts. This differs from §10.6's example, which shows `sourceEnvironment: "*"`. `runtimes` keeps its label selector.
5. **Security oversight.** A `security-admin` can forbid the tenant setting through the guardrail `crossEnvironmentSharing: forbid` in the tenant security record of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture). A forbid refuses additions and suspends existing paths, so the setting reads as off at the delegation gate. Turning the setting on, and adding a `dangerouslyShare` entry, each take [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s weakening delay at a tenant with a security record, and posture reports the setting and each declared path. A label edit on a runtime or a change to T's selector can add a runtime to a path at once, with no delay, which "Residual risks" states.
6. **Records.** The delegation audit event, which carries `cross_environment` today, gains `caller_environment` and `target_environment`. §11.7 events, `credential.mint`, and evidence for the child carry `environment`, which is T, and `crossedFromEnvironment`, which is the caller's. Billing and retention follow T, through the same field they read today.

**Accepted risk.** The owner accepted these two statements:

- On a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy.
- A non-member's agent can drive the target's credentials and receive results, on any pool with no user check.

The first statement includes anything the caller's agent can read, such as a token delivered under `delivery: file` with no `lockEgressToHosts`. A session under that lock cannot cross.

**Interactions.**

| Surface | Behaviour under this model |
| :-- | :-- |
| Pool with `egressProfile: restricted` | The `restricted` row applies with E = T: `gitAccess hosts ∩ git hosts(T) − denied(T)`. Nothing comes from the caller. |
| Mirror route | T's git routes apply. The resolver selects the pool from `visible(VCS pools, T)`, and that pool's credential selects the store. A fill's cause resolves to the child's session, so E = T. The child can read T's stores on pools with `userAuthorization: off`. |
| Connectors | §10.6 **Connectors are never cross-environment** holds, and "their own environment" means T, because the child is in T. It uses T's `connectorSelector`. §4.3 keys connector tokens on user, connector, tenant, and environment, so Alice's token in `web` does not apply, and a connector needing a user token has none until she authorizes one in T. |
| `credentialPropagation: inherit` | `credentialPropagation` is §8.3's delegation setting for how a child obtains an LLM credential. §8.3's check stays. A crossing hop with `inherit` draws from the parent's origin LLM pool when the providers intersect, so the caller's LLM credential serves the target runtime. `independent` remains the default, and `deny` is available. |
| Concurrent and service-mode pods | The rule under "Pods with no per-session policy" applies with E = T, at placement. The caller's environment is not read. |
| `ENVIRONMENT_REQUIRED` | It does not apply, because the child has E = T. |
| Default delegation policy | T's. It applies only to discovery, because §10.6's step 4, which applies the `DelegationPolicy` to a cross-environment delegation, has no code today. |

## Identity model (TS-2, TS-4, and TS-6 to TS-9)

### Owner's decision

The owner decided decision 8 on 2026-11-03, reversing revision 12's recommendation, in these words: "I think we need to support per-tenant OIDC and serve SCIM. In that model, a deployer can use a broker by default but change to a customer's SSO when needed". The evidence for the need remains vendor documentation with no first-hand complaint found, so the decision rests on the owner's judgment of the product rather than on that evidence.

The model is:

- **A tenant has one or more identity sources.** The default is the deployment source, the issuer at `auth.oidc.issuerUrl`, which can be the operator's broker. It needs no record until the tenant uses SCIM, and the tenant comes from the tenant claim, under "Broker contract".
- **A tenant can instead be configured with a customer source**, the customer's own OIDC issuer, pinned to that tenant, or on a listed shared issuer its organization on that issuer, under "Activation rule". The source is a configuration choice. Lenny has no mechanism for moving a tenant from one to the other, and the owner removed migration from this design.
- **Lenny serves SCIM 2.0**, for a tenant on either kind of source.
- **Both kinds coexist.** One deployment holds tenants on the deployment source and tenants on customer sources at once.
- **A single-company deployment is unaffected.** With the Helm value `auth.multiTenant: false`, which runs the deployment as a single tenant, or no customer source, the registry holds only the deployment issuer, subjects keep their form, and a write of a source is refused.

This table records what each side costs. Column B is now designed below, and column A stays the default.

| Concern | A: broker | B: per-tenant OIDC and SCIM in Lenny |
| :-- | :-- | :-- |
| Lenny code for SSO | The protected resource metadata route and the `WWW-Authenticate` challenge, and the boundary changes in this section | The same route, plus the source record, the verifier registry, per-issuer JWKS caches on the gateway and the Token Service, and per-source claim mapping |
| URLs from tenants | No issuer URLs. `identityProvider.introspectionEndpoint` is already a tenant-supplied URL the gateway calls, and TS-4 adds its missing address check. | Each issuer and `jwks_uri` is also a URL the gateway and the Token Service fetch |
| Issuer ownership | Broker concern | Lenny must establish that a tenant controls what it registers |
| Protocols | Whatever the broker federates, including SAML | OIDC alone. A SAML-only customer stays on the deployment source behind a broker. |
| Login routing | One authorization endpoint named in one metadata document. The broker does home-realm discovery, choosing the customer IdP a user signs in at, for example from the email domain. | Per-tenant URLs and metadata |
| SCIM | Lenny's server, or a broker or vendor endpoint with a sync worker | Lenny's server |
| Group storage | None required. Groups can stay in tokens. | New tables when a source takes groups from SCIM |
| Subject stability | The broker issues `sub`, so a customer changing IdPs behind it keeps its users. | `sub` belongs to each issuer, and Lenny provides no migration (see "Residual risks") |
| Several IdPs in one tenant | Broker concern | Lenny namespaces `sub` and groups |
| Tenant boundary | One claim from one issuer. A broker fault or mapping error crosses tenants. | The issuer selects the tenant, or on a listed shared issuer the issuer with a provider-set audience or claim, so a customer's IdP administrator cannot assert another tenant |
| Outage | A broker outage stops logins for every tenant on it. | A customer IdP outage affects that tenant alone, or every tenant on a shared issuer |
| Operator's work | Run or buy a broker | Run Lenny's new surface, and handle customer IdP support |

No effort estimate was measured. Column B is a new subsystem, and it does not remove the need for a customer-facing portal.

### What comparable systems do

- Keycloak's organizations link customer IdPs to an organization inside one realm, so one issuer serves all customers. Its token claim is an object keyed by organization alias. Keycloak's SCIM is per realm, and its documentation says organization groups cannot have members managed through SCIM ([project documentation](https://www.keycloak.org/docs/latest/server_admin/index.html)). A vendor extension adds [per-organization SCIM as experimental](https://phasetwo.io/blog/orgs-scim-experimental/) (vendor source).
- Auth0 organizations put `org_id` in tokens, and its documentation calls validating that identifier the preferred method ([vendor documentation](https://auth0.com/docs/organizations/using-tokens)). Its [device flow does not natively carry organizations](https://support.auth0.com/center/s/article/missing-org-id-in-auth0-device-authentication-flow) (vendor source), which matters to a CLI login.
- WorkOS sells Directory Sync, which [hides SCIM behind an API and delivers changes through webhooks or an events API](https://workos.com/docs/directory-sync) (vendor documentation).
- Products sold directly to enterprises expose SSO and SCIM themselves: [Devin](https://docs.devin.ai/enterprise/security-access/sso/guide.md) and [Cursor](https://cursor.com/docs/account/teams/scim) (vendor documentation). Those pages do not say whether a broker sits behind them. Devin's page states that without SCIM, groups sync only at login, and that the vendor's team configures the connection.
- [Coder](https://coder.com/docs/admin/users/idp-sync.md) syncs groups, roles, and organizations from claims per organization, and it overwrites membership at login (vendor documentation).
- Microsoft states that [`sub` is pairwise per application, `oid` is immutable across applications, and `email` must never be used for authorization](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference) (provider documentation). Three design choices below follow from those statements: a per-source subject claim, per-issuer `iss` values such as Entra's tenant-specific URI, and no automatic match on email.

### Identity source record (TS-6)

`tenant_identity_sources` is a tenant-scoped table under the existing row-level security policy.

| Column | Meaning |
| :-- | :-- |
| `tenant_id`, `name` | The primary key. `name` is short and tenant-chosen, it cannot contain `~`, and `deployment` is reserved. |
| `source_id` | A random short identifier, fixed at creation, used in canonical subjects, groups, callbacks, and SCIM credentials. |
| `issuer` | The exact issuer string. A partial unique index over rows in any state but `draft` holds the pin, so a draft claims nothing and a retired row still holds its exact pin values. Only a `draft` or `retired` row can be deleted, which for a `retired` row releases the index, and `source_id` is never reused. |
| `pin_audience`, `pin_claim`, `pin_claim_value`, `pin_claim_is_domain`, `pin_client_claim`, `pin_authorization_params`, `pin_domain_unconfirmed_since` | Empty on an ordinary issuer. On an issuer `identity.sharedIssuers` lists, they hold the values its entry requires, under "Issuer pin and proof". They are stored here so that a later list edit cannot change what the row enforces. |
| `require_directory_row` | Refuses a subject with no directory row, under "Issuer pin and proof". A change is a change on the source. |
| `authorization_endpoint`, `token_endpoint` | Captured from discovery at proof, checked under "SSRF and NetworkPolicy", and used for every login. A change is a change on the source. |
| `audiences` | The values accepted in `aud`, never empty. When a token has several audiences and an `azp`, `azp` must be in this list or equal `client_id`. |
| `client_id`, `clientSecret` | The login client. PKCE with no secret is the default, as `buildPlaygroundOIDC` does today. `clientSecret` is optional and write-only, as below. |
| `subject_claim` | The claim holding the user identifier, `sub` by default. An Entra source can name `oid`. "Issuer pin and proof" lists what a row refuses here. |
| `roles_claim`, `role_map` | The claim read for roles, and a map from its values to tenant-scoped role names. An unmapped value grants nothing. |
| `groups_claim`, `group_source` | The claim read for groups, and `token` or `scim`. |
| `service_claim` | An optional claim with the values that mark a token as `caller_type: service` or `agent`, as §25.1's agent callers need. Every other token from this source has `caller_type: human`. |
| `match_claim`, `scim_deactivate_mode` | The claim a directory's `externalId` must equal, fixed to `subject_claim` on a customer source, and what a SCIM deactivation does, under "SCIM server". |
| `email_domains` | Verified domains used for login routing. None is required. |
| `state` | `draft`, `pending_effective`, `active`, `suspended`, or `retired`. A `suspended` row records its reason, which is `address`, `domain_lapse`, `pin_required`, or `issuer_conflict`. `pending_effective` applies only to first activation, and such a row refuses writes. A `retired` or `suspended` row refuses writes too, and a reactivation that changes a field states the changes as a `pending_version` and follows the activation rule. A `retired` row returns to `active` with the same `source_id` under **Reactivating**, so a customer can recover its own issuer, and a `suspended` row returns as "Failure behaviour (identity)" states. |
| `pending_version` | A proposed change to an active source or a deployment settings row (described below the table), or to a reactivating row, under the activation rule. The row stays on its live values until the version takes effect. A pending `clientSecret` waits in its own sealed row keyed on tenant, `source_id`, and version, and the approval digest (the `approved_digest` row) covers a keyed hash of its value. A pending `deploymentIssuerAllowed` waits on the tenant record beside the flag, in the same form. |
| `tokens_not_before` | Set at retirement and carried in the snapshot. Step 7 under "Verifier registry" refuses any token, IdP or Lenny-issued, whose `iat` is earlier. |
| `proof`, `approved_digest` | The results of the checks under "Issuer pin and proof", with who ran them and when. The preview, which the sign-in proof under "Issuer pin and proof" produces, holds the canonical subject, and no name. Any draft edit clears `proof`. Approval records a digest of the first activation's row or of the pending version, which must still match at effect. |
| `activated_by`, `justification`, `ticket_id`, `effective_at` | The activation record. `ticket_id` is optional, for an operator whose process uses tickets. |

**Deployment settings.** A tenant on the deployment source that uses SCIM has one row with `source_id: deployment`, the reserved name, and no `issuer`. It holds `match_claim`, `scim_deactivate_mode`, `group_source`, and its credentials, it has the same states, and it is not a registry entry. A change to it follows the activation rule, and the proofs do not apply, because it names no issuer.

**Client secret.** The same mechanism applies as for a link provider's client secret in [`user-credential-delivery-design.md`](user-credential-delivery-design.md). The Token Service stores the submitted value envelope-encrypted under the tenant's Token-Service-only key, in a row keyed on tenant and `source_id`, sealed with a digest of `tenant_id`, `source_id`, `issuer`, `token_endpoint`, and `client_id`. No route returns it, and no field names a Secret, so a tenant cannot point at another tenant's. The Token Service alone reads it, and a source with a secret has its code exchange done by the Token Service over mTLS, which sends the value only when the digest matches and returns the token response to the gateway. A change to any sealed field leaves the secret unusable until a write re-supplies it.

A tenant holds at most a small number of sources, a Helm value that defaults to 5, so the registry and its caches stay bounded. A write creating a row that names an issuer on `identity.deniedIssuers`, a Helm list that ships with known shared issuers, is refused unless `identity.sharedIssuers` lists it, and a write naming the deployment issuer is always refused. Writes go through a new admin resource, `/v1/admin/tenants/{id}/identity-sources`, with `If-Match`, and through the single write path for tenant opt-ins that the egress design's proposal [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects) adds, once it lands. §10.6's `identityProvider` keeps its introspection settings and gains no issuer.

**Activation rule.** A source can be drafted by a `tenant-admin`, a `security-admin`, or a `platform-admin`. Activation is the power to decide who every user in a tenant is, so it follows a stricter rule (decision 17):

- A `platform-admin` activates a source, with a justification, once the proofs pass. A `tenant-admin` cannot, because a stolen tenant admin credential could otherwise plant a lasting backdoor.
- At a tenant with a `security-admin` row in any state, activation and a change to `deploymentIssuerAllowed` take the weakening delay of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture). Any reachable `security-admin` must also approve and can cancel. Invalidating the security team therefore does not remove the delay. This extends [`security-oversight-design.md` decision 23](security-oversight-design.md#decisions)'s rule for `identityProvider.*`, since an operator able to repoint an issuer silently could regain what the pin removes.
- A deployment can set `identity.allowTenantActivation: true`, default `false`, under which a tenant's `security-admin` activates alone, with the proofs passing and the same delay, and no operator action. A tenant with no `security-admin` always goes through the operator.
- Every change on an active source or a deployment settings row, including `roles_claim`, `groups_claim`, `group_source`, `client_id`, `clientSecret`, endpoints, an added `email_domains` or `audiences` entry, `match_claim`, and `scim_deactivate_mode`, creates a `pending_version` under the same rule. Further widening writes are refused while one exists. A stated narrowing applies at once: removing an audience other than the last, a `role_map` entry, or an email domain. A narrowing, a suspension, or a retirement cancels the pending version, and its proposer can submit it again.
- Before activation, a preflight reads the proof's preview. At a tenant with a `security-admin` row in any state, it refuses unless a `security-admin` row exists on a canonical subject of the new source, so oversight survives activation, and it lists what stays on old subjects: principals, `lifecycleSubjects`, role rows, and owners of running sessions. On a reactivation it also lists role rows, custom roles, and SCIM credentials written by that source's subjects. The preflight refuses or lists, and it migrates nothing.
- **Retiring.** A `security-admin` retires a source at once. A `platform-admin`'s retirement at a tenant with an active `security-admin` takes [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay and can be cancelled, because immediate retirement would lock out the principals who could cancel what follows. At a tenant with none, it applies at once with a justification. An attacker asserting a principal's subject through a compromised issuer could cancel a delayed retirement, so the incident path is invalidation first and retirement second: an invalidated principal does not count as active here, and this path is reversible. Retirement sets `tokens_not_before`, and TS-8 adds the step that revokes the source's SCIM credentials. It ends playground records from the source. Its subjects are refused under "Verifier registry".
- **`invalidateSubjects`.** This optional mode needs a second `security-admin` or [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay, because it cannot be undone, and that confirmation comes before the retirement it accompanies, since a sole source's retirement refuses the second principal too. It runs §11.4's `full_revoke` for each of the source's subjects, as a job that reports partial failure, placing an `admin` hold on each. Reactivation clears none of those holds. An admin clears them per subject, or in bulk by naming the job.
- **Stranded rows.** A security principal row on a subject whose source is not active counts as active for approval and for the **Platform admin limit** of [SO-1](security-oversight-design.md#so-1-security-roles), under which a platform admin assigns `security-admin` only when the tenant has no active one, so a retirement cannot remove the approver by locking it out.
- **Reactivating.** Every return to `active` takes the locked overlap check under "Issuer pin and proof", and a domain-pinned row takes the conditions under "A lapsed domain". Returning the same `source_id` needs a new sign-in proof and a justification recording the customer's request, and it drops cached keys. The row returns on its stored values, and any changed field waits as a `pending_version` under the activation rule. When a reachable principal exists, such as on a second source, it also needs that principal's approval and the delay. A proof does not show that a compromised key was rotated, because such a key can choose its own `iat`, so the customer rotates it first. A different issuer follows the activation rule.
- **A customer that has permanently lost its issuer.** Its principals cannot authenticate, and they count as active, so no customer approval exists. The operator removes those rows under [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay, which nobody can cancel, and assigns a new `security-admin` against the customer's recorded request. The customer's protection in this case is the operator's process and the mirrored audit record.
- `issuer`, the pin fields, and `subject_claim` cannot change on a row, because canonical subjects include `source_id` and a changed issuer's `sub` values could collide with the old ones and inherit their records. A different issuer is a new row with a new `source_id`.

**Pointing an existing tenant at a different issuer.** The write is allowed, under the activation rule and its preflight above, with no further check. Refusing it would need a definition of a tenant that already has users, and an exception for tenants that want this. "Residual risks" states the consequence.

**Which issuer a tenant accepts.** The tenant record holds `deploymentIssuerAllowed`, true by default. Activating a customer source sets it false, and retiring that source leaves it false, so retiring a compromised source does not reopen the tenant to the broker. Every move of a customer source to `active` sets it false. Setting it back to true follows the activation rule, and it is refused while a customer source is `active` or `pending_effective`. Any such move, and a pending reactivation, cancels a pending change to true, and the change is checked again at effect. A tenant therefore accepts either the deployment issuer or its customer sources, and never both.

### Verifier registry

`pkg/auth/jwt` gains a registry member that `composeBearerVerifier` and `buildCallerVerifier` join behind the Lenny key set, so the gateway and the Token Service run one implementation:

1. It refuses a token over 16 KiB, decodes the payload with no verification, and reads `iss`, capped at 512 bytes.
2. It looks `iss` up in an in-memory snapshot of active sources, by exact match. The deployment issuer is the entry that keeps today's path. A shared issuer's entry holds one verifier and its pinned rows, and its `issuerAliases`, the other spellings of its issuer that "Issuer pin and proof" describes, lead to the same entry.
3. An unknown `iss` is refused with `401 TOKEN_INVALID`, with no fetch of that issuer and no store read. The reason `unknown_issuer` appears in logs and counters and never in a response, because a caller could otherwise test which IdPs are customers. The gateway's `serveServiceIdentity`, which admits §25.4's service accounts, still submits a `TokenReview` for a token claiming its audience, which is an existing path and is not an issuer fetch.
4. The entry's own verifier checks the signature, `iss`, `aud`, `exp`, and `nbf`. The unverified `iss` selected a verifier and nothing else, so a token claiming a victim's issuer but signed elsewhere fails against the victim's keys. On a shared issuer, the verifier checks the signature, `iss` against `issuer` and `issuerAliases`, `exp`, and `nbf` here, and `aud` follows in step 5.
5. On a shared issuer, the verified `aud` and pin claim select the row. The claim is read by exact key and must be one non-empty string of at most 256 bytes. A row matches when `pin_audience` and `pin_claim_value`, where non-empty, each equal the token's value, read under `pin_claim`, and an empty field places no condition. Exactly one row must match. The selected row's `audiences` and `azp` rule then apply on every pin form, so a token Acme's user obtained for another application is refused. No match and more than one match are each `401 TOKEN_INVALID`, with the reason in logs and counters alone, so a caller cannot test which organizations are customers. For a customer source, the tenant is the record's tenant, and any tenant claim in the token is ignored. For the deployment issuer, the tenant comes from `auth.tenantIdClaim`, and the request is refused with `401 TENANT_ISSUER_MISMATCH` when that tenant's `deploymentIssuerAllowed` is false. This second check is what keeps a broker out of a pinned tenant.
6. The claim allowlist, `normalizeUserBearer`, and the role boundary apply, and the canonical subject is computed.
7. A subject whose source is not active is refused with `401 TOKEN_INVALID`, by its `~<source_id>~` prefix against the snapshot, and so is a token whose `iat` precedes the source's `tokens_not_before`, or that has no `iat` when that value is set. A bare subject is refused at a tenant whose `deploymentIssuerAllowed` is false. A subject with no directory row is refused here too on a source with `require_directory_row`. This check sits in the auth middleware, in `lenny-ops`, on `/v1/oauth/token` for the caller, subject, and actor tokens, in the browser resolver, in `/v1/playground/token` in both playground mint modes, and in the impersonation mint, so it covers Lenny-issued tokens too. `mintOIDC` has no token to read, so it compares the playground record's issue time with `tokens_not_before`. `mintAPIKey` calls the verifier directly and `completeMint` stamps a new `iat` today, so without this check a mint during retirement would produce a token that survives reactivation. The bootstrap `lenny-admin` credential and §25.4 service identities are exempt, as the recovery path.

The snapshot is rebuilt from Postgres on a version change announced through Redis and on a poll, and it survives a store outage with its last value. It reads a dedicated view exposing `tenant_id`, `source_id`, `issuer`, the pin fields, `audiences`, `client_id`, `require_directory_row`, `state`, `tokens_not_before`, `deploymentIssuerAllowed`, and the mapping fields to a dedicated role, because a pre-authentication read across tenants cannot use §12's `__all__` path, the `app.current_tenant = '__all__'` setting through which a platform-admin path reads across tenants under row-level security. `lenny-ops` keeps its single issuer, and a customer source's user reaches it only through a Lenny-issued token. TS-2 closes F-13.3.27: `/v1/oauth/token` refuses a playground-origin `subject_token`, the token being exchanged, because `mintToken` drops `origin` and `lenny-ops` would otherwise accept the result. It reverses `TestPlaygroundOriginSubjectTokenAccepted_spec_13_3` and the `authenticateCaller` comment that pin today's behaviour. Tenant administration uses the gateway's admin API. Reasons are counted without the `iss` value, because it is attacker-chosen.

### Keys and caches

Each source has its own `OIDCVerifier` on each gateway and Token Service replica, built lazily on the first token for that issuer. A shared issuer has one verifier for all its rows, so key refresh, maximum staleness, and the refetch limit apply per issuer. A tenant's reactivation there fetches keys first and swaps them on success, so it does not interrupt other tenants. `OIDCVerifier.key` holds its mutex across a fetch, so one slow IdP can pile up waiting requests. TS-6 therefore refreshes in the background with no lock held. `MinRefetchInterval` already limits fetches for an unknown `kid`, per issuer. A refresh re-reads discovery, because `jwksURI` is otherwise cached until restart.

These verifiers accept keys that a tenant's issuer chooses, and verification runs before the rate limit in `httpsurface.go`. TS-2 therefore bounds RSA at 4096 bits, a Helm value, and requires `e` of 65537, because `publicKey` enforces only a minimum today. The cost of an oversized key has not been measured.

A refresh failure keeps the previous keys, as `refreshLocked` does today. A replica that restarts during an IdP outage has none, so that source's tokens fail on that replica until the IdP returns. For a customer source this design adds a maximum staleness, a Helm value that defaults to 24 hours, past which that source's tokens are refused with `503` and its alert fires, because an IdP that rotated out a compromised key while unreachable would otherwise stay trusted indefinitely. The deployment issuer keeps today's behaviour. Decision 22 covers the value.

### Claim allowlist and mapping

TS-2 changes the IdP boundary for every issuer. Today an IdP-verified token keeps `caller_type`, `act`, `authorized_tools`, and `scope`, and `normalizeUserBearer` clears only `typ`, `session_id`, `delegation_depth`, and `origin`. The boundary instead builds claims from an allowlist, each read by exact key, because Go's struct decoding also matches other cases: `iss`, `aud`, `azp`, `exp`, `nbf`, `iat`, `jti`, `sub` or `subject_claim`, `scope`, the roles claim, and the groups claim, plus the tenant claim on the deployment issuer. `match_claim`, `service_claim`, `nonce`, `email`, and the provider identity claim that a link provider with `identityBinding: claim` names under [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) ([`user-credential-delivery-design.md`, "Identity binding"](user-credential-delivery-design.md#identity-binding)), are read by exact key and returned only to the purpose that needs them. Everything else is dropped. An absent or empty subject claim refuses the token.

`scope` is filtered to values with the `tools:` prefix, as §25.1's deployer policies at the IdP need. A token with no such value has an absent scope, and a malformed `tools:` value stays `TOKEN_INVALID`, as does a value with any case of `tool` as its prefix that fails to parse, such as `tool:pool:read` or `TOOLS:pool:*`, because reading it as absent would widen the token. Dropped values are counted. No mapping is needed, because scope only narrows, so it is safe from any issuer, and `openid profile` no longer refuses the token. `caller_type` comes only from `service_claim`, or `auth.serviceClaim` on the deployment issuer, which can set `service` or `agent`, and every other token reads `human`. The `security-*` values in the roles claim pass the boundary to the role resolver of [`security-oversight-design.md`](security-oversight-design.md), which removes them under `securityRoleSource: managed` and reads them under `securityRoleSource: oidc`, so `role_map` is not involved.

The tenant has no mapping on a customer source, because the record supplies it. `role_map` is explicit because customer IdPs emit a `roles` claim for their own purposes, as Entra's token reference shows, and today's code would read it as Lenny roles. The mapping can name tenant-scoped built-in roles and custom roles, and a write naming `platform-admin` or a `security-*` role is refused. `groups_claim` must yield an array of strings. Past Entra's overage limit [the claim is absent](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference) (provider documentation), which reads as no groups and refuses the environment, so a tenant with users in many groups uses `group_source: scim`.

### Issuer pin and proof

An issuer pins to one tenant through the partial unique index, checked at activation, unless `identity.sharedIssuers` lists it, and the deployment issuer can never be registered as a customer source (decision 18). A conflict returns a generic refusal, so the answer does not say whether another tenant holds the issuer. `email_domains` are unique in the same way. The harm being prevented is specific. A tenant pointing at an issuer it runs gains nothing outside itself, because the tenant comes from the record. A tenant claiming another customer's issuer can squat it, so the customer that owns it cannot register, and with a claimed email domain it can route that customer's users to its own tenant's login.

These proofs run in order, and each is recorded on the row:

1. **Discovery.** `issuer` in the document must be identical to the URL used, as [OpenID Connect Discovery requires](https://openid.net/specs/openid-connect-discovery-1_0.html), and `jwks_uri` must be `https`. This proves the URL serves a consistent issuer, and no more.
2. **Sign-in.** A named customer administrator completes an authorization code login with PKCE and a nonce against the issuer, through the client the record names. The login starts from the drafting admin's session, or from a one-time token that names the expected source, is short-lived, and is single use. It uses the callback and resolver under "Browser login" and creates no session or bearer. The result previews the mapped subject, roles, and groups. This proves a client is registered at that issuer and the mapping works. It is weak on ownership alone, since some IdPs let an ordinary user register an application.
3. **Domain.** For each entry in `email_domains`, a DNS TXT record at `_lenny-challenge.<domain>` holds a value bound to the tenant and source. This proves control of a domain and is what permits routing by it. It does not bind a domain to a hosted issuer, because no record at `login.microsoftonline.com` is the customer's to write. The record is re-read daily, and a lapsed domain stops routing and affects no token, except on a row with a domain pin, under "A lapsed domain".
4. **Attestation.** The platform admin's justification records the customer's request for this issuer, and the operator keeps whatever further record its own process requires. Under `identity.allowTenantActivation`, the `security-admin`'s approval stands in its place.

**Shared issuers (decision 18).** Some providers use one issuer for many customers, so the issuer alone cannot select a tenant. Google's ID tokens carry `https://accounts.google.com` for every customer and name the organization in `hd` ([provider documentation](https://developers.google.com/identity/openid-connect/openid-connect)), and an Auth0 tenant using organizations names one in `org_id`. An issuer unique to a customer, such as an Okta org URL or Entra's tenant-specific URI, keeps the one-tenant rule above.

`identity.sharedIssuers` is a Helm list that a platform admin writes. Each entry names `issuer` and `pinBy`, which is `audience`, `claim`, or `audienceAndClaim`. An entry using a claim names `claim`, and sets `claimIsDomain: true` when the value is a DNS domain. An entry can also name `issuerAliases`, `authorizationParams`, `subjectClaims`, and `clientClaim`, which the items below describe.

```yaml
identity:
  sharedIssuers:
    - issuer: https://accounts.google.com
      issuerAliases: [accounts.google.com]
      pinBy: claim
      claim: hd
      claimIsDomain: true
      authorizationParams:
        hd: "{pin_claim_value}"
```

A **shared-issuer row** is a row with a `pin_audience` or `pin_claim_value`, whatever the list says now, and a **shared issuer** is one that is listed or that has such a row. Registry step 5, the client claim rule, the issuer-level hold (the stop on a shared issuer's fetches under "Failure behaviour (identity)"), and the lapse check follow that definition.

- **Issuer spellings.** `issuerAliases` lists other exact strings the provider's tokens carry in `iss`. Google's documentation names `https://accounts.google.com` and `accounts.google.com`. Each is an exact match, with no normalization or pattern. Only the Helm entry holds aliases, and a tenant cannot add one. `issuer` is what discovery and every fetch use, so an alias is never fetched and needs no address check. A row's `issuer` must be the entry's `issuer`, and a draft naming an alias is refused. The chart refuses an alias that equals the deployment issuer or another entry's `issuer` or alias.
- **What the entry asserts.** The platform admin asserts two things Lenny cannot check: no customer's administrator can set that claim to a value another customer uses, and the provider issues a token with a pinned audience only to the registered client. A false assertion restores the tenant-claim risk. The chart refuses the deployment issuer, and refuses `auth.tenantIdClaim`'s value, `tenant_id`, `sub`, `email`, `preferred_username`, `name`, `given_name`, `family_name`, `nickname`, `org_name`, `groups`, and `roles` as a `claim`, and it warns on an issuer and claim pair outside a shipped list of known pairs. The claim must be a top-level string, so Keycloak's organization claim, an object, needs a mapper on an attribute users cannot edit. An entry overrides `identity.deniedIssuers` for its issuer. The configuration preflight refuses an alias equal to a row's `issuer`. If one arrives anyway, because the chart cannot see rows, a row whose `issuer` equals `auth.oidc.issuerUrl` or another entry's alias becomes `suspended` with reason `issuer_conflict` and raises its alert, and the snapshot excludes it. The shipped denied list names Entra's personal-account tenant issuer, because many customers' personal accounts share it.
- **`pinBy: audience`.** Lenny then checks no organization, so whoever can obtain that audience from the provider is a user of the tenant. The chart refuses this form on `https://accounts.google.com`, because any Google Cloud service account [can request an ID token with any audience](https://docs.cloud.google.com/docs/authentication/token-types) (provider documentation), whatever the client's login settings, and a client ID is visible in a login redirect. The chart warns on every other such entry, and posture reports each source's pin form. Such a row accepts only tokens whose `aud` is its `client_id`, so a provider's API access token, which carries an API identifier, does not work as a bearer on it.
- **The client claim.** On a shared-issuer row, the claim the entry names in `clientClaim`, `azp` by default, must equal `client_id` or a value in `audiences` when it is present, whatever the number of audiences. Google sets `azp` to the service account's ID on those tokens, so one minted with a victim's client ID as `aud` is refused. A user's token can omit it, so absence is allowed. Providers name the client differently, as Okta's `cid` and Entra v1's `appid` show, and a token that names it nowhere passes this check.
- **The subject claim.** A new owner of a domain can recreate `admin@acme.com`, so an email address as a subject would hand over that person's roles, sessions, and links. Every row refuses `email`, `preferred_username`, `upn`, `name`, `given_name`, `family_name`, and `nickname` as `subject_claim`. A shared-issuer row takes `sub` or a value in the entry's `subjectClaims`, which the platform admin asserts is immutable and never reassigned.
- **Authorization parameters.** `authorizationParams` adds parameters to the login request, each a literal or `{pin_claim_value}`, such as `hd` on Google or `organization` on Auth0. Google's token types page says `hd` appears [only when the client specified the `hd` parameter](https://docs.cloud.google.com/docs/authentication/token-types), while its OpenID Connect page reads absence as an account outside a hosted domain, so the lead case, a Google Workspace customer pinned by `hd`, depends on this. These parameters are hints to the provider, and the verified token alone decides. An MCP client makes its own authorization request and may not send them, which the **Unproven** item under "Residual risks" lists.
- **The row.** A draft on a listed issuer states its audience and claim value, and the row copies `pin_claim`, `pin_claim_is_domain`, `pin_client_claim`, and `pin_authorization_params` from the entry, so a tenant cannot choose them. `pinBy` is the minimum a new row must hold. When `pinBy` includes the audience, `audiences` holds one value, equal to `client_id`, and that value is `pin_audience`. A customer with two Lenny tenants on one provider organization uses `audienceAndClaim`, with a client per tenant.
- **Fixed values.** The pin fields cannot change on a row, and neither can `audiences` and `client_id` on a row pinned by audience. A different value is a new row with a new `source_id`, because another organization's subjects could otherwise collide with the old ones and inherit their records. The one exception is a `pending_version` taking a row from `claim` to `audienceAndClaim`, so that a customer can add a second tenant. Every subject on such a row already came from that organization, so `source_id` stays.
- **Later list edits.** The pin is enforced from the row, so removing an entry leaves a row's pin, its login parameters, and its TXT re-read in place. Removing an entry therefore closes the list to new rows and leaves existing rows working, apart from tokens carrying an alias: an entry's aliases leave with it, so those tokens stop resolving, which can stop logins on a provider that returns the alias. An issuer that has been listed, with its aliases, goes on a stored marker that outlasts the entry, and a `helm rollback` does not clear it. A platform admin clears a marker through the admin API with a justification.
- **A row with no pin on a listed issuer.** Such a row would accept every customer's tokens from that issuer, so it has no path to a pin, which matches the owner's removal of migration. The configuration preflight refuses an entry for an issuer a row with no pin holds, so the operator retires that row first, and the tenant then activates a new pinned source, whose subjects are new users. If such a row meets an entry or a marker anyway, the snapshot excludes it at once, and in one transaction the row becomes `suspended` with reason `pin_required`, `tokens_not_before` is set, its SCIM credentials are revoked, and its playground records end. No list edit clears that state, and its one exit is retirement. A retired row with no pin cannot return while the entry or marker stands, and activation of a row with no pin is refused on such an issuer.
- **Index and activation.** The partial unique index covers `issuer`, `pin_audience`, `pin_claim`, and `pin_claim_value`, with an empty value treated as a value. A row's empty field places no condition on a token, so two rows overlap unless a field non-empty on both differs, and two claim values differ only when both rows name the same claim. The index does not see this. Every entry to `pending_effective` and every move to `active`, whether first activation, reactivation, a return from `suspended`, or a `pending_version` adding a pin field, therefore takes a per-issuer lock and refuses a row that overlaps another row in `pending_effective`, `active`, or `suspended`. A retired row holds only its exact values, so another tenant can activate an overlapping row with a platform admin's justification, and the retired row's return is then refused. A row with no pin overlaps every row on its issuer. The check reads through a dedicated function outside tenant context, as the snapshot's view does, and it answers only conflict or no conflict.
- **Proof.** The sign-in proof's verified token must carry the audience and claim value being claimed, or the proof fails, so a tenant cannot claim an organization without a login from it. The comparison is against the draft on a first activation, the stored pin on a reactivation or a return, and the pending value on a `pending_version`, and proofs 2, 3, and 4 apply to each. Proof 3 is read again when the change takes effect, so a domain that lapses during the delay stops it. Under `claimIsDomain`, proof 3 also covers the pin value, because any one employee could otherwise pass the sign-in proof and hold their company's value from a trial tenant. For the same reason, on any shared-issuer row proof 4 names the pin value and records that the organization's administrator asked for it, and `identity.allowTenantActivation` does not apply to a shared-issuer row with no domain proof, so such a row always takes an operator's activation.
- **A lapsed domain.** Google's `hd` stays the same when a domain changes hands, so a later owner of a defunct company's domain can [obtain tokens with the old value](https://trufflesecurity.com/blog/millions-at-risk-due-to-google-s-oauth-flaw) (vendor source). On a row with `pin_claim_is_domain`, a TXT record found missing at the daily re-read raises an alert, and after `identity.domainPinLapseDays`, default 7, the source becomes `suspended` and `tokens_not_before` is set. Lookups that keep failing raise the same alert and suspend after `identity.domainPinLookupFailureDays`, default 30, so a new owner cannot avoid the check by answering with errors. A sweep that finds at least 5 records and more than a tenth of the deployment's active domain pins missing at once raises a deployment-level hold and alert, and counts toward no row's bound, because that pattern is an operator's resolver fault. While the deployment-level hold stands, no domain-pinned row's lapse count advances. The hold ends at the next sweep below that threshold. A lapse is a sign that the proof may no longer stand, so every return of a domain-pinned row to `active`, whatever state it returns from, needs a new TXT check, a justification, and a sign-in by a subject that held a role row or security principal on that source before `pin_domain_unconfirmed_since`. That column stores the time of the first read that failed to confirm the record, whether missing or failed, ignoring reads during a hold, and a confirming read clears it. A row can move from `suspended` to `retired`, and retiring it does not relax this. A new owner's accounts have a different stable identifier and cannot pass. A tenant with no such subject uses a new source.
- **`require_directory_row`.** A source with SCIM can set this, `false` by default, to refuse a subject with no directory row, under registry step 7, with the read cached and failing closed. A resold domain's new accounts then get nothing, and neither does a user the customer has not provisioned.

No mechanical proof binds a customer to a hosted issuer, so the pin rests on proofs 2 and 3 together with proof 4. A disputed issuer is resolved by a `platform-admin` retiring the wrong row and deleting it, with an audit event, which releases the index. At a tenant with a `security-admin` row in any state, the delete takes [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay. If the tenant's own `security-admin` cancels the retirement, the operator invalidates that principal first, which applies at once, and then retires. "Residual risks" states this.

### SSRF and NetworkPolicy

Every customer issuer, `jwks_uri`, `authorization_endpoint`, and `token_endpoint` passes one checker, shared with TS-4's `introspectionEndpoint` check. The deployment issuer is exempt, because an operator can run a broker in the cluster:

- The URL must be `https`, on port 443 unless the record's port is on a Helm allowlist, with no userinfo or fragment, and with no query except on `authorization_endpoint`, where some IdPs publish one.
- Each resolved address is refused when it falls in the §13.2.5 excluded ranges, in `127.0.0.0/8`, `::1/128`, `0.0.0.0/8`, or `100.64.0.0/10`, which that list omits and which NetworkPolicy does not cover for loopback, or in the cluster's own node and load-balancer addresses. An IPv4-mapped IPv6 address is normalized first.
- The dialer repeats that check on the address it connects to, so a name that re-resolves between check and dial is refused.
- Redirects are refused on every call the checker governs, including the token request, whose client follows them today. The transport ignores proxy environment variables. Responses are capped in size, and the key count is bounded.

The consequences in the chart are these:

- **Gateway.** `allow-gateway-egress-llm-upstream`, the NetworkPolicy that lets the gateway reach external LLM providers, is the gateway's only external HTTPS path, and it already excludes private ranges (§13.2.4). When `gateway.llmUpstreamEgress.enabled` is false or `egressCIDRs.llmProviders` replaces the broad rule with a CIDR list, a customer's issuer is unreachable. The chart refuses `identity.customerSources.enabled` with the rule disabled, and it warns on a CIDR list, which gains an `identity.issuerCIDRs` value for strict deployers. NetworkPolicy cannot match names, so a strict deployer maintains addresses per customer.
- **Token Service.** `allow-token-service`, the Token Service's NetworkPolicy, renders `kms.endpointCIDR` on TCP 443 unconditionally, which is open with no excepts under the local provider's default and closed to issuers once a cloud KMS narrows it. An except is an entry in a NetworkPolicy rule's `except` list, here the private ranges and the instance metadata address that an external HTTPS rule excludes. "Defects found while verifying" records this, and the Token Service should use `lenny.kmsEgressRule`, as the gateway does. That chart helper renders the KMS rule only for a cloud KMS, and it refuses an unrestricted CIDR. Without that change, the excepts of the rule below are void under the local provider, because NetworkPolicy allows are additive: the unrestricted port 443 rule already admits the addresses another rule excepts. Only the dialer's check would then hold. [`egress-and-git-design.md`, "Credentials"](egress-and-git-design.md#credentials) adds a direct rule for provider APIs with the gateway's exclusions, and this design uses the same rule, which also widens the egress of the component that holds every provider credential. TS-6 renders the rule when [EG-1](egress-and-git-design.md#proposal-split), the egress design's git prerequisites, has not.
- **Private-address IdPs.** An issuer on a private address is refused in the first version. The issuer, `jwks_uri`, `authorization_endpoint`, and `token_endpoint` must all pass the checker, so publishing metadata alone is not enough, and that customer stays behind a broker that can reach its IdP or exposes those endpoints (decision 21).

### Browser login (TS-6)

A browser login is part of the same boundary as the registry. The playground, the sign-in proof, and the link start page under [`user-credential-delivery-design.md`, "Flow"](user-credential-delivery-design.md#flow) all use this path, and this design lands it in TS-6 because customers register these URIs at their IdPs and a later change forces them to register again.

1. **One resolver.** The registry exposes one function that takes a source and an ID token and returns the tenant and canonical subject, applying registry step 5, the allowlist, the role boundary, `deploymentIssuerAllowed`, the active-source check, and `require_directory_row`. On a shared issuer it refuses unless the row the token selects is the callback's source, and the proof purpose compares the token against the values "Proof" names instead. `extractSubject`'s read of raw claims goes away, so a bare playground login with `tenant_id: acme` on a broker token gets `TENANT_ISSUER_MISMATCH`. The proof purpose is exempt from the active-source check, since it runs on a row that is not yet active, on a draft, a return, or a pending version, and it creates no session.
2. **One redirect URI per source.** Each source has `/auth/callback/{source_id}`, and the deployment source has `/auth/callback/deployment`. `state` names the purpose, so one URI serves all three uses.
3. **Refusal before any token request.** The state cookie is scoped to that callback's path, with a short lifetime as today. The callback refuses when the cookie is absent, when `state` does not match, or when the state's source differs from the path's source, and none of these sends the authorization code or the PKCE code verifier anywhere. A hostile source that bounces a browser to another IdP therefore delivers the code to the other source's callback, where no state cookie exists.
4. **RFC 9207.** When the response carries `iss`, it must equal the source's issuer or one of its entry's `issuerAliases`. It is not required, because IdPs differ.
5. **Exchange, then resolve.** The authorization code and the PKCE code verifier go only to the source's recorded `token_endpoint`, directly or through the Token Service when the source has a secret. The ID token and nonce then go through the resolver.
6. **Cookies.** The state cookie is scoped per source, as above. The playground session cookie is scoped per tenant in TS-7, by path under `/t/{tenantId}/playground/`, with the mint moved under that prefix, because a browser does not send a cookie scoped to `/playground/` to `/v1/playground/token`. A silent login at a hostile source, one the IdP completes with no user interaction because the browser already has a session there, then cannot replace a session on another tenant.

How each provider behaves on these flows is untested, including silent single sign-on and RFC 9207 support. Which spelling of Google's issuer a given flow returns is also untested, and `issuerAliases` admits both.

### Login routing (TS-7)

Only the deployment source can use the bare URLs, because nothing else identifies a tenant before a token exists.

| Client | How the tenant is known | Behaviour |
| :-- | :-- | :-- |
| MCP client | A per-tenant resource URL, `/t/{tenantId}/mcp` and `/t/{tenantId}/mcp/environments/{name}` | A 401 names the metadata at `/.well-known/oauth-protected-resource/t/{tenantId}/mcp`, which lists that tenant's active issuers in `authorization_servers`. Bare `/mcp` names the deployment issuer alone. |
| REST client | The same prefix, or none, since the bearer identifies the tenant | A token from a customer source also works on the bare path. Under `/t/{tenantId}`, a token that resolves to another tenant gets `403 TENANT_PATH_MISMATCH`. |
| Playground | `/t/{tenantId}/playground`, or an email domain typed on the shared page when the source has verified domains | Login follows "Browser login". A tenant with several sources gets a chooser. |
| `lenny-ctl` | `--token` or `LENNY_API_TOKEN`, as today. It has no login command. | Unchanged. A customer's administrator supplies a bearer from its own tooling. |
| Link start page ([`user-credential-delivery-design.md`, "Flow"](user-credential-delivery-design.md#flow)) | The flow's tenant | Logs in through that tenant's source and "Browser login", in place of `auth.oidc.issuerUrl` alone. |

RFC 9728 metadata names the deployment issuer for the deployment resource, and per-tenant documents name only that tenant's issuers (decision 20). One shared document listing all issuers would disclose every customer, and [the MCP specification leaves selection to the client](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization). The per-tenant document is public, so it discloses a tenant identifier and its IdP host to anyone who guesses the identifier, as the [egress design](egress-and-git-design.md)'s `/oidc/<tenant-id>` path already does for its identifier. The specification also says a client without dynamic client registration, the OAuth mechanism through which a client registers itself at an authorization server, needs a client ID by hand, so a customer IdP lacking that feature needs one configured in each MCP client. How MCP clients behave against these documents is unproven.

### Several IdPs in one tenant, and namespacing

A customer source's user key is the canonical subject `~<source_id>~<value of subject_claim>`. `subjectPattern` in `userstore` already admits `~` and limits a subject to 256 printable characters, and a longer result is refused at the boundary. A deployment source's `sub` keeps its form, so a single-company deployment and every tenant that never moves are unchanged. Once customer sources are enabled, a deployment token whose `sub` begins with `~` is refused, and a deployment group beginning with `~` is dropped, so no presented value can collide. `(tenant_id, subject)` stays the user key throughout.

Groups follow the same form, `~<source_id>~<group>`, because `oidc-group` is a bare string match and two IdPs could both assert `security-engineers`. TS-6 fixes this form, since environment members store it. Under `group_source: scim`, `<group>` is `displayName`, which SCIM holds unique per source. A rename changes the string, so its members stop matching, which fails closed, and posture lists the environment members with no group. An environment member names that string, and the admin API accepts it verbatim. A deployment source's groups keep their form. Two subjects from two sources in one tenant are two users, and Lenny has no account linking between them.

The `~<source_id>~` prefix in `user_id` names which issuer vouched, so §11.7 events and evidence need no new field for attribution. A deployment subject carries no prefix, which names the deployment issuer.

### Role boundary

§10.2 **Role assignment:** allows an OIDC claim to carry any built-in role. In code, an IdP-verified token's `roles` reach `Principal.Roles` unchanged, and `platform-admin` then spans all tenants. TS-2 closes this for every issuer, and TS-6 extends it:

- The IdP boundary, beside `normalizeUserBearer`, removes `platform-admin` from an IdP-verified token's roles on the gateway and on the Token Service. The request continues with the remaining roles. Each removal emits an `auth.platform_role_dropped` audit event and a counter with an alert.
- A customer source goes further, because only values `role_map` names become roles, and that map cannot name a platform role. A customer's IdP therefore has no way to express one.
- `platform-admin` comes from a platform-managed role row, a Lenny-issued token, or a §25.4 service identity. `/v1/admin/users` already allows a `platform-admin` to grant that role, and §15.1 must list that surface, which it omits today.
- **Operators' staff.** `auth.platformAdminTenant` names one operator tenant, and `platform-admin` rows exist only there, on every write path: `POST` and `PUT /v1/admin/users`, and the bootstrap seed, with `tenant_user_roles.go` already refusing the role. The role resolver ignores such a row elsewhere, and chart validation requires `gateway.adminToken.tenant` to match. The chart refuses `auth.platformAdminTenant: platform` at render time, with a message stating that [§10.2](../spec/10_gateway-internals.md#102-authentication) **`tenant_id` format validation.** reserves the tenant ID `platform` for the platform tenant, under which the platform audit chain is written (decision 27). The admin API and the bootstrap seed already refuse to create a tenant with that ID, so the refusal stops an install whose operator tenant could never exist. Staff log in through whichever issuer authenticates that tenant. The broker contract requires that tenant's IdP link to be operator-only.
- **What this does and does not stop.** A customer's IdP can never reach platform authority, on either kind of source. The issuer of the operator tenant can, by asserting a staff `sub`. With the default, a broker compromise reaches platform authority and therefore every tenant, including pinned ones through impersonation. The operator tenant can hold a customer source naming the operator's own IdP, which takes the broker out of that path (decision 24). On a deployment with more than one tenant, preflight and the operator tenant's posture read warn while that tenant's `deploymentIssuerAllowed` is true.
- The specification and the code agree on claim names (decision 10).

**How the tenant-claim risk changes.** For a tenant on the deployment source, the risk is as it was: a broker fault or compromise can issue a token for that tenant. For a tenant on a customer source, the tenant comes from the pin and the check in registry step 5 refuses the deployment issuer, so the broker cannot reach it, and the customer's IdP can reach only its own tenant. A broker and a pinned issuer therefore coexist without either speaking for the other's tenants at the token boundary. On a listed shared issuer, a customer's administrator still reaches only its own tenant while the entry's assertion holds, and a fault at the provider reaches every tenant on that issuer. This does not remove the operator from the pinned tenant's trust. A `platform-admin` retains impersonation and can repoint a source, both audited, and the second takes the delay at a tenant with a `security-admin` row in any state.

**An issuer's power over its principals.** An issuer controls every principal it authenticates. It can assert the subject of a `tenant-admin` or a security principal, and it chooses the `iat` value read by [SP1](security-oversight-design.md#security-properties), the security oversight property under which a security principal row grants its role only on a bearer issued after the row took effect, and the row then grants the role, whatever `securityRoleSource` says. `securityRoleSource: managed` stops a role claim, and it does not separate a principal from its issuer. On a tenant on the deployment source, an operator's broker can therefore assert a `security-approver`'s `sub` and approve the operator's own request. Posture reports each principal's issuer. A tenant that needs separation from its operator moves to a customer source, and decision 25 leaves a second factor out of the first version.

### Broker contract

These go into §10.2 as requirements on the issuer named by `auth.oidc.issuerUrl`, and they are the contract for the default source.

| Claim | What Lenny does | Requirement on the broker |
| :-- | :-- | :-- |
| `iss`, `aud`, `exp`, `nbf` | Verified against `auth.oidc.issuerUrl` and `auth.oidc.clientId` | One issuer and one audience for all tenants on this source |
| Tenant claim (`auth.tenantIdClaim`) | Read as a string. Refused when absent, malformed, unregistered, or naming a tenant that does not allow this issuer. | Set from the broker's organization record. Never derived from a user attribute or a claim a customer can write. A nested claim needs a mapper to a string. |
| `sub` | The user key within a tenant, and session owner | Stable, never reassigned, unique within a tenant across all of that customer's IdPs, and not beginning with `~` |
| `roles` | An array of role names | Tenant-scoped roles only. A customer's own mapping must not reach it unfiltered. |
| `groups` | An array matched exactly against `oidc-group` members | Stable names, namespaced when a tenant has several IdPs. A complete list or none: Entra [limits a JWT to 200 groups](https://docs.azure.cn/en-us/entra/identity/users/directory-service-limits-restrictions) (provider documentation), and past that a broker must resolve the set itself. An absent claim reads as no groups, so the failure is a refused environment. |

The broker must also keep token lifetimes short enough for the operator's offboarding bound, and keep the operator tenant's IdP link operator-only. It delivers lifecycle events through a sync worker, or the customer's directory calls Lenny's SCIM directly.

### Discovery

§10.2 calls the gateway an MCP-standard protected resource server. [The MCP authorization specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization) requires such a server to serve RFC 9728 metadata and to name its location in `WWW-Authenticate` on a 401. No route, handler, or specification text in the tree does this, so a client learns the issuer out of band. TS-2 adds `/.well-known/oauth-protected-resource`, naming `auth.oidc.issuerUrl`, and the challenge header. TS-7 adds the per-tenant documents.

### SCIM server (TS-8)

The server implements RFC 7643 and RFC 7644 at `/scim/v2/{tenantId}/{sourceName}`, on the gateway's ingress behind its own middleware rather than the OIDC chain. A tenant on the deployment source uses the source name `deployment`.

| Surface | First version |
| :-- | :-- |
| Discovery | `ServiceProviderConfig`, `ResourceTypes`, and `Schemas`, stating what is absent |
| Users | `GET` by `id`, list with pagination, `POST`, `PUT`, `PATCH`, and `DELETE`. Core user schema. Extension fields are dropped and not stored. `externalId` cannot change after provisioning. |
| Groups | The same methods, with `PATCH` add and remove on `members`, and `excludedAttributes=members`. `displayName` is unique per source. |
| Filters | `eq` on `userName`, `externalId`, `id`, and `displayName`. Any other filter answers `400 invalidFilter`. |
| `PATCH` | Both the `path: active` form and the pathless `value` form. Vendor blogs report that [identity providers differ on this](https://ssojet.com/blog/okta-scim-provisioning-saas-vendors) (vendor source), and Okta's own documentation was not retrieved. |
| Absent | Bulk, sort, ETags, `/Me`, and password changes |

These follow what [Microsoft's tutorial](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/use-scim-to-provision-users-and-groups) lists for its service (provider documentation): queries by `userName` and `externalId`, groups by `displayName`, `excludedAttributes=members`, and a soft-deleted user still returned with `active=false`.

**Credentials.** A credential is a random 256-bit bearer, bound to one tenant and one source, stored only as a keyed hash, shown once, and rotated with an overlap. A `tenant-admin` holding `manage_user_lifecycle` or a `security-admin` mints one, because the directory is the customer's. It grants the two calls of TS-4, `invalidate` and `reinstate`, and writes to directory tables, and nothing else in the admin API. Its audit subject is `scim:<source_id>`. Disallowing the deployment issuer disables a `deployment` credential.

**Quotas.** Per source, Helm values cap credentials, users, groups, and memberships per user. Requests are rate limited per tenant, and failed authentication per source address, since an invalid bearer has no credential to count against. The body limit is 1 MiB, invalidations are coalesced, and the group cache is bounded per tenant.

**Matching a directory user to a token.** SCIM carries no OIDC subject, so a source names `match_claim`, a claim the source declares immutable and verified, whose value must equal the directory's `externalId`. A declaration is a change on the source, under the activation rule. `userName` and `preferred_username` never bind, because a user may be able to edit them and Microsoft warns that they change.

- On a customer source, `match_claim` is `subject_claim`, so the canonical subject is computed at provisioning. `externalId` is unique per source. An Entra source maps `objectId` to `externalId` and sets `subject_claim: oid`, since Microsoft's default user mapping uses `mailNickname` and `sub` is pairwise.
- On the deployment source, a broker issues its own `sub`, so the directory row stays unbound until a token's `match_claim` equals `externalId`, and it then binds once. Every IdP token from a deployment source with a settings row is checked against the directory, with the read cached, whether or not a user row exists. A match on an inactive row refuses the login and places a hold on that subject under the `lifecycleSubjects` rule, so a security principal is protected as it is on a direct write. A deployment `externalId` beginning with `~` is refused.
- `active: false` on an unbound row has no subject to hold. The SCIM response reports it, and an alert fires. "Residual risks" states what this leaves running.
- Posture reports provisioned rows that never matched a login, and logins with no directory row, because a wrong mapping otherwise deactivates a subject nobody uses while the intended user stays active.

**Deprovisioning into TS-4.**

| SCIM operation | Effect |
| :-- | :-- |
| `active: false` | A hold of this source's class at `scim_deactivate_mode`, `full_revoke` by default (decision 11), through the same code as `POST .../invalidate`, at server time |
| `DELETE /Users/{id}` | The same hold, and the directory row goes. The user row stays held. It is not an erasure, which keeps §12.8's job path. |
| `active: true` | Clears this source's hold alone. Any other class's hold stays, and the response reports `active: false`. |
| Any write on a subject with a completed erasure job | `409` |
| A lifecycle write on a security principal | Refused per user with `403` unless `scim:<source_id>` is in the `lifecycleSubjects` list of the [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) tenant security record, and posture reports it. A group write is not a lifecycle write. |
| Provisioning a user | A `users` row with no role and `RoleAssigned` false, written by its own path, because `handleCreateUser` sets it and would blank the user's token roles. No session, role, or environment follows from it. Decision 13's rule on login rows is unchanged. |

A store failure answers `503` with `Retry-After`. Microsoft's service [retries and eventually quarantines a failing job](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation), so a user stays active during an outage, which "Residual risks" states.

**Conformance.** The test corpus is a set of recorded and hand-written request sequences per directory, because no live directory is available to the suite. Passing it shows agreement with the corpus and no more, and each directory's own validator is a manual step per release. No directory has been tried.

### Directory groups (TS-9)

`directory_users` holds `tenant_id`, `source_id`, the SCIM `id`, `external_id`, `user_name`, `active`, and the bound subject, if any. `directory_groups` holds the SCIM `id`, `external_id`, and `display_name`, and `directory_group_members` joins them. All are tenant-scoped under row-level security.

`group_source` on the source selects where a user's groups come from, and the two never merge (decision 19):

- **`token`**, the default, keeps today's behaviour.
- **`scim`** makes the auth middleware replace `Principal.Groups` from `directory_group_members`, at the same point introspection replaces them today, for IdP tokens and Lenny-issued tokens alike.

A tenant with a customer source refuses `introspectionEnabled` on every source kind, because a tenant-set endpoint would receive every bearer presented for the tenant. Activation refuses while it is on. A deployment-source tenant is left to [`security-oversight-design.md` decision 23](security-oversight-design.md#decisions).

`envaccess`, the environment membership resolver, keeps its `Caller` input, so `Membership` and `AuthorizedRuntimes` are unchanged, and the new reader sits ahead of it. The read is cached for 30 seconds, a SCIM write invalidates it through Redis, and a store failure answers `503`, as `GROUP_INTROSPECTION_UNAVAILABLE` does. `GET /v1/admin/environments/{name}/access-report`, which §15.1 describes as a resolved member list with group expansion, can read these tables for a source with `group_source: scim` and can do no such expansion for a source with `group_source: token`.

**Erasure and classification.** §12.9 adds directory users, groups, and memberships at T3 beside user identifiers, source records at T3, and SCIM credential hashes and the stored client secret at T4. `DeleteByUser` removes the directory row, the memberships, and any proof preview naming that subject, and `DeleteByTenant` removes all of them. An unbound directory row has no subject to find, so an erasure job addresses it by SCIM `id`. §12.8's tombstone refuses re-provisioning an erased subject, with the `409` above. Group display names can carry personal or organizational data, so they are T3 as well.

### When SCIM and token claims disagree

| Disagreement | Which wins | Reason |
| :-- | :-- | :-- |
| A valid token, and SCIM has deactivated the user | SCIM | TS-4's refusal at authentication reads the row on every bearer, so a token's remaining lifetime grants nothing at `full_revoke`. |
| A stale `groups` claim, with `group_source: scim` | SCIM | The claim is ignored. |
| A `groups` claim, with `group_source: token`, and SCIM holds different groups | The token | The source said so. Posture reports a tenant whose SCIM groups are stored and unused, because this is the likely misconfiguration. |
| SCIM says active, and the IdP refuses login | The IdP | No token exists, so nothing reaches Lenny. |
| SCIM says `active: true`, and an administrator holds the user | The administrator | SCIM clears only its own hold. |
| A role in a token and a platform-managed row | The row | §10.2's precedence. SCIM carries no roles in the first version. |

For the TS-1 rules:

- Explicit environment admission and the TS-3 transparent `creator` check use the groups the table above resolves.
- `ENVIRONMENT_REQUIRED` reads the tenant's environments and no groups, so neither source can change its answer.
- A running session keeps E, and `visible`, the delegation gate, and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` read E alone, so a SCIM group removal does not reach a running session. Only a lifecycle event does, under TS-4's levels.
- A Lenny-issued token carries groups copied at exchange under TS-3, and under `group_source: scim` they are replaced at each request, so they cannot outlive a removal. Under `group_source: token`, they last until that token's expiry.

### User lifecycle (TS-4)

A directory reaches Lenny in one of two ways: directly through the SCIM server, or through a broker or vendor endpoint and a sync worker that calls the routes below. Both end in the same invalidation code. Today that code fails in these ways:

- `POST /v1/admin/users/{userId}/invalidate` answers `404` when the user has no row. Rows come only from the admin API, so a user who only ever logged in through an IdP cannot be invalidated, and that user's sessions keep running.
- `requireActiveUserForAction` admits a user with no row, so an absent row also means no gate on a fresh token.
- `hard_disable` and `full_revoke` set `DeletedAt`, and no endpoint clears it, so a returning employee cannot be reinstated. Entra [reactivates users that move from soft-deleted to active](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation).
- Invalidation needs `manage_users`, which also assigns roles. A worker holding it has more authority than deprovisioning requires.
- Invalidation does not stop an administrator. `serveBearer` never reads `IsActive`, `PlatformRoles.ResolveRoles` ignores `Disabled` and `DeletedAt`, and the only callers of `user.IsActive` guard session creation, interaction resolution, and elicitation. An invalidated `tenant-admin` holding a live IdP token keeps the admin API.

TS-4 makes these changes:

- **Upsert on invalidate.** Invalidating an unknown subject creates its row in the invalidated state and runs the §11.4 steps, which list sessions by owner and do not depend on the row having existed.
- **Refusal at authentication.** The row lookup the middleware already performs for `PlatformRoles` also reads the invalidation state, so this adds no read. A `full_revoke` row refuses every bearer with `401 USER_INVALIDATED`, on the gateway, on `lenny-ops`, and for a caller credential at `/v1/oauth/token`. On the gateway this adds no read. The Token Service has no user store today, so it gains a read of the user row through the Postgres connection pooler. A `soft_disable` or `hard_disable` row keeps the §11.4 session behaviour and reduces the principal's roles to `user`, so no disabled user holds admin authority.
- **Holds per caller class.** The user row gains a `holds` column, one entry per class: `admin`, `sync`, and `scim:<source_id>`, each with its mode and stored time. The effective mode is the highest remaining, and a user is active only when none remains. A caller holding `manage_users` or `security-admin` is `admin`, and any other caller with `manage_user_lifecycle` is `sync`. `invalidate` writes its caller's class, and `reinstate` clears only that class, so a directory or a stolen worker credential cannot erase an incident hold. An `admin` caller can name other classes to clear. This is a stored format, and it lands in TS-4 because TS-8 reads it.
- **Deleted rows and store errors.** A `DELETE`d row is refused as a `full_revoke` row is, because `SoftDelete` sets `DeletedAt` and leaves `RoleAssigned` and `Roles` in place, so a deleted administrator keeps its role today. A store error fails closed on the gateway and the Token Service. `lenny-ops` deliberately authorizes on the token's roles when the lookup fails (`claimOnLookupError`, §25.4), and this design leaves that as written, so a Postgres outage suspends the refusal on `lenny-ops` alone. A §25.4 service identity is unaffected, which keeps a recovery path.
- **Impersonation bearers.** `soft_disable` and `hard_disable` run the §13.3 **Platform-admin impersonation** rule 6 cascade, which revokes the impersonation bearers the user started and runs on `full_revoke` alone today, so a disabled user's bearers do not outlive its authority. A caller without `platform-admin` gets the existing skipped report.
- **Reinstate.** `POST /v1/admin/users/{userId}/reinstate` clears a hold and restores nothing else. It refuses a user under an §12.8 erasure, it does not revive a row removed through `DELETE`, and it refuses a caller reinstating itself with `403`. It is the only way back: `PUT /v1/admin/users/{userId}` refuses `disabled: false` on an invalidated row, where it would otherwise skip the self-reinstatement and ordering rules, and the bootstrap seed's force update refuses to reverse a disable. Upserts for unknown subjects are capped per tenant.
- **Ordering.** Both calls accept `observedAt`, and the hold stores min(`observedAt`, server time). A future stamp, whether malicious or from a fast worker clock, therefore cannot make a later call stale, and an emergency call at server time always applies. An absent value takes the server's time. A call whose value is older than its class's stored time answers `200` with `applied: false`, so a retried event cannot reverse a later one, and on a tie an invalidation wins. Both calls are idempotent.
- **Permission.** A new `manage_user_lifecycle` permission covers these calls alone. `tenant-admin` holds it, and a custom role can hold it alone, such as `identity-sync`. A worker authenticates per tenant, through the client credentials path of §10.2, with that role, and `service_claim` or `auth.serviceClaim` marks it as a service. A SCIM credential holds the same two calls for its own class.
- **`identityProvider` hardening.** `GET rbac-config` stops returning `introspectionClientSecret`. The write validator and the client refuse an `introspectionEndpoint` that resolves to the §13.2.5 excluded ranges, through the shared checker. The handler checks only for HTTPS and a host today, and the client uses a default dialer, while the gateway posts bearers to that URL.

**User credentials and links.** A session's `caller_type` now sits on its row, and a delegated child copies its parent's value, so a service or agent caller receives no user token from a read of that row. Each of `soft_disable`, `hard_disable`, and `full_revoke` stops a user's `userCredential` releases, through `pkg/binding`'s invalidation level, and it revokes `oauth2Link` tokens at the provider, under [`user-credential-delivery-design.md`, "Lifecycle and revocation at the provider"](user-credential-delivery-design.md#lifecycle-and-revocation-at-the-provider). A `registered` token stays valid at its provider, because Lenny cannot revoke it, and the tenant is told. Reinstate restores neither. A subject with an invalidation level or a GDPR Article 18 restriction, which §12.8 sets on a user under erasure, cannot be written to through `PUT /v1/admin/users/{userId}/credentials/{type}`. `identityBinding: claim` adds a provider identity claim to the broker contract or to a customer source's mapping. An issuer that cannot supply it uses `identityBinding: directory`, under which a sync worker or an admin writes each user's expected provider account, and that write sits under `manage_user_lifecycle`, so the sync worker can hold it. A SCIM credential does not hold that write in the first version.

When a worker or a directory cannot reach Lenny, the event waits with it, and a user stays active until delivery succeeds. A partial failure reported in the invalidate response is retried, as the idempotent call allows. For a caller without `platform-admin`, §11.4 step 5 reports the impersonation cascade as skipped, which is correct for a customer user, and the operator invalidates its own staff with a `platform-admin` caller.

Offboarding latency is the directory's delivery time, plus the sync worker's on that path. Microsoft documents incremental cycles [at intervals defined per application](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works) (provider documentation), with no general figure on that page. Until delivery, a user can create sessions while a token is valid. Lenny-issued tokens default to 900 seconds and are capped at 24 hours for `lenny-gateway` (§13.3 **`exp` granularity and parent-expiry handling.**), and a running session does not depend on its creator's token, so only invalidation ends it.

### What stays with an operator's portal

Lenny supplies admin APIs and no customer-facing identity UI, which §10.2 lists as a future goal. These remain the operator's: the page where a customer enters issuer details, instructions for registering an application at each IdP and for publishing the DNS record, sign-up and contract records behind the request, SAML bridging through a broker, support for a customer's IdP misconfiguration, and any directory connection other than SCIM.

### Failure behaviour (identity)

| Condition | Result |
| :-- | :-- |
| Unknown `iss` | `401 TOKEN_INVALID`, with the reason logged, and no fetch of that issuer |
| Customer IdP down, keys cached on a replica | That tenant's existing tokens verify on that replica, and fail on a replica that restarted. New logins fail at the IdP. Other tenants are unaffected, apart from those on the same shared issuer. |
| Customer IdP down past maximum staleness | That source's tokens get `503`, and its alert fires. Lenny-issued tokens keep working until they expire. |
| The only source of a tenant is retired | Its users are refused, including with Lenny-issued tokens. It can return under **Reactivating**, or the deployment issuer can be allowed again under the activation rule. |
| A later re-resolution of an issuer lands in an excluded range | The source becomes `suspended`, its subjects are refused, and its alert fires. A `platform-admin` returns it to `active` once the address is fixed, with a justification and no new approval, because the proof and approval still stand. |
| A shared issuer's re-resolution lands in an excluded range, confirmed across several reads | An issuer-level hold stops its fetches and raises its alert. No row changes state and no pending version is cancelled. Cached keys verify up to maximum staleness, and the hold clears itself when addresses return to allowed ranges. |
| A shared issuer's keys pass maximum staleness | Every row's tokens on that issuer get `503`, across tenants, because they share one verifier. No row changes state. |
| A row with no pin meets an entry or a marker | The source becomes `suspended` with reason `pin_required`, as "Issuer pin and proof" states, and it can only be retired. |
| A row's issuer equals the deployment issuer or an alias | The source becomes `suspended` with reason `issuer_conflict`, and its alert fires. A `platform-admin` returns it with a justification once the conflict is gone, with proofs 2, 3, and 4 on a shared-issuer row, and no list edit returns it alone. A row with no pin on an alias is `pin_required` instead, because the marker records aliases. |
| A TXT record for a domain pin is missing past `identity.domainPinLapseDays`, or lookups keep failing past `identity.domainPinLookupFailureDays` | The source becomes `suspended` and `tokens_not_before` is set. Its return follows "A lapsed domain", and the return with no new approval for reason `address` does not apply. |
| Registry snapshot cannot refresh | The last snapshot serves, and an alert fires past a staleness bound. A source retired during the outage stays trusted until refresh on that replica. |
| Row read fails | Fails closed on the gateway and the Token Service, as role resolution does |

### Who can do what (identity)

| Action | `platform-admin` | `security-admin` | `tenant-admin` | Customer IdP admin | SCIM credential or sync worker |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Draft a source | Yes | Yes | Yes | No | No |
| Activate or change a source, or change `deploymentIssuerAllowed` | Yes, with a justification, under the delay at a tenant with a `security-admin` row in any state | Approves or cancels. Activates alone only under `identity.allowTenantActivation`. | No | Completes the sign-in proof | No |
| Retire a source | With a justification, and under the delay at a tenant with an active `security-admin` | At once | No | No | No |
| Use `invalidateSubjects` | Under the delay | With a second `security-admin`, or under the delay | No | No | No |
| Reactivate the same `source_id` | With a justification recording the customer's request and a new proof | Approves, with the delay, when reachable | No | Completes the proof | No |
| Delete a `retired` row to release its issuer | Yes, under the delay at a tenant with a `security-admin` row in any state | No | No | No | No |
| Delete a `draft` row | Yes | Yes | Yes | No | No |
| Connect a customer IdP to the broker | Yes, as broker admin | No | No | Through the broker's portal, if offered | No |
| Create the tenant | Yes | No | No | No | No |
| Set tenant-scoped roles | Through mapping or row | No | Through row | Through its IdP's claim values, as `role_map` maps them | No |
| Grant `platform-admin` | Row, in the operator tenant alone | No | No | No | No |
| Mint a SCIM credential | Yes | Yes | With `manage_user_lifecycle` | No | No |
| Place and clear holds | Yes | Yes | Yes, and no self-reinstatement | No | Yes, own tenant, own class |
| Assign groups | Through broker | No | No | Directory | SCIM writes them |

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `auth.oidc.issuerUrl` and `auth.oidc.clientId` | The deployment issuer and audience, which can be the operator's broker, and the default source of every tenant. | Deployment | Platform admin (Helm) |
| `identity.customerSources.enabled` | Whether a tenant can hold a customer source. Off, the snapshot holds the deployment issuer alone, so no customer source verifies, and no row changes state. | Deployment | Platform admin (Helm) |
| `identity.allowTenantActivation` | Whether a tenant's `security-admin` can activate a source with no operator action. | Deployment | Platform admin (Helm) |
| `identity.issuerCIDRs`, the source limit per tenant, maximum key staleness, the RSA key limit, and SCIM quotas | Issuer addresses for a strict egress list, the number of sources a tenant holds, how long cached keys stay trusted, the largest key accepted, and per-source SCIM counts. | Deployment | Platform admin (Helm) |
| `gateway.delegation.defaultMaxDepth` | The last-resort `maxDepth`, 10 by default and at least 1, rendered to the gateway. | Deployment | Platform admin (Helm) |
| `auth.tenantIdClaim` | The claim holding the tenant identifier on a deployment issuer's token. A customer source has none. | Deployment | Platform admin (Helm) |
| `auth.rolesClaim` and `auth.groupsClaim` (decision 10) | The claims holding roles and groups on a deployment issuer's token, defaulting to `roles` and `groups`. | Deployment | Platform admin (Helm) |
| Identity source (`issuer`, `audiences`, `client_id`, write-only `clientSecret`, captured endpoints, and `email_domains`) | The customer's issuer pinned to this tenant, and how logins reach it. | Tenant | Drafted by a tenant admin, security admin, or platform admin. Activated under "Activation rule". |
| Source mapping (`subject_claim`, `roles_claim`, `role_map`, `groups_claim`, `group_source`, and `service_claim`) | How that issuer's claims become a subject, tenant-scoped roles, and groups. | Source | As above |
| SCIM credential | The directory's bearer for one tenant and source. | Source | Tenant admin with `manage_user_lifecycle`, or security admin, with counts capped per source |
| `scim_deactivate_mode` and `match_claim` | What a deactivation does, and the immutable, verified claim that joins a directory user to a token. | Source, or the tenant's deployment settings row | As the source mapping |
| `auth.platformAdminTenant` | The one tenant whose rows can hold `platform-admin`. The chart refuses the value `platform`, which §10.2 reserves for the platform tenant (decision 27). | Deployment | Platform admin (Helm) |
| `identity.deniedIssuers` | Shared issuers that cannot be a customer source. | Deployment | Platform admin (Helm), with a shipped list |
| `identity.sharedIssuers` | Issuers several tenants can use, each with the audience or claim its pin must include. | Deployment | Platform admin (Helm), empty by default |
| `identity.domainPinLapseDays`, `identity.domainPinLookupFailureDays` | How long a domain pin's TXT record can stay missing, or its lookups keep failing, before the source is suspended. | Deployment | Platform admin (Helm) |
| `auth.serviceClaim` | The claim and value marking a deployment issuer's token as a service caller. | Deployment | Platform admin (Helm) |
| `deploymentIssuerAllowed` | Whether the tenant accepts the deployment issuer. Activating a customer source sets it false. | Tenant | Under the activation rule |
| Broker organization, IdP links, and claim mappers | The per-customer SSO configuration and the source of each contract claim. | Broker, per customer | Operator, or customer through a broker portal |
| Directory connection | The source of user and group changes for a customer, through Lenny's SCIM server, or through a broker or vendor and a sync worker. | Customer's directory | Customer IdP admin with the operator |
| `identityProvider` introspection settings | An RFC 7662 endpoint that supplies current groups for each request. | Tenant, and refused at a tenant with a customer source | Tenant admin (`rbac-config`), subject to SO decision 23 |
| `noEnvironmentPolicy` | Whether a user with no environment can reach runtimes. Such a user's sessions have E = ∅. | Deployment default, tenant | Platform admin, tenant admin |
| Platform-managed role row | A user's role, which overrides the token's roles. | User in a tenant | Tenant admin, and platform admin for `platform-admin` |
| `identity-sync` custom role | A role holding `manage_user_lifecycle` alone, for a sync worker. | Tenant | Tenant admin or operator |
| Environment members | The groups and subjects with a role in an environment. | Environment | Tenant admin |
| `environments` on a VCS credential pool | The environments whose sessions can use the pool. Absent means tenant-wide. | Pool in a tenant | Tenant admin |
| `environments` on a credential binding | The environments whose sessions can use the binding. Absent means tenant-wide. | Binding in a tenant | Tenant admin |
| `environments` on a user credential type or link provider | The environments whose sessions can use it. Absent means tenant-wide. | Type or provider in a tenant | Tenant admin |
| `environmentPolicies[E].alwaysAllowed` | Hosts each session in E reaches by default, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].sessionRequestable` | Hosts a session in E can request, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].denied` | Hosts refused to sessions in E, added to the tenant's. | Environment | Tenant admin |
| `environmentPolicies[E].egressRoutes` and `gitRoutes` | Routes tried for sessions in E before the tenant's routes. | Environment | Tenant admin |
| `dangerouslyAllowCrossEnvironmentSharing` | Whether any environment in the tenant can share runtimes across environments. Off by default. | Tenant | Tenant admin, and a security admin can forbid it. Turning it on takes the weakening delay of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) at a tenant with a security record. |
| `dangerouslyShare.outbound` and `.inbound` | The named environments and non-empty runtime selectors a sharing path allows. Both sides must declare it, and wildcard names are refused. | Environment | Tenant admin or a custom role with `manage_environments`, with an added entry taking [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay |
| `delegationLease.maxDepth` and `.maxChildrenTotal` | The session's depth limit, where 0 means it cannot delegate, and a per-parent child cap, where 0 is stored as `maxDepth: 0`. A client can only narrow. | Session | Client |
| Environment on a session | The environment the session runs in, set at create and fixed thereafter, or the target under a sharing path. | Session | Client, subject to membership or to a declared path |

Every setting in the [`egress-and-git-design.md`, "Scopes and who manages them"](egress-and-git-design.md#scopes-and-who-manages-them) table that this table does not name stays as written there.

## Scenarios

**Two environments in one customer.** Tenant `acme` has environments `web` and `payments`, with members from the groups `acme-web` and `acme-payments`. Pool `web-github` covers `github.com/acme-web/` for `web`, and pool `pay-github` covers `github.com/acme-pay/` for `payments`. The tenant allows `npm`. `payments` adds `stripe-api` and denies `huggingface.co`.

- Alice, in `web`, creates a session through `/v1/environments/web/sessions` that clones `acme-web/site`, and it succeeds.
- A plan from Alice naming `acme-pay/ledger` is rejected with the same answer as a repository outside every pool.
- An agent in Alice's session runs `git fetch https://github.com/acme-pay/ledger`. The scope check refuses it, nothing is minted, and `git` gets 403.
- Bob, in `payments`, reaches `api.stripe.com`, and Alice's session gets 403 for the same host.

**A session on the transparent path.** Alice calls `POST /v1/sessions` with no environment, on a tenant whose `web` runtime she can see, and `web` has no denied or route entries. The session has E = ∅ and reaches `npm`. A plan naming `web-github` fails. The error message tells a caller to use an environment path for environment objects, without naming any object. Bob makes the same call for a runtime `payments` selects, and it fails with `ENVIRONMENT_REQUIRED`, whether or not his token carries groups, so he cannot shed the deny on `huggingface.co` by omitting the environment.

**Two environments and one GitHub organization.** Pools `web-app` and `pay-app` both cover `github.com/acme/`, with separate GitHub Apps and allowlists, for `web` and `payments` respectively. A third, tenant-wide pool with that prefix is refused at write. GitHub enforces each App's own repository selection, so the environments' tokens differ.

**An environment's cloud role.** Binding `payments-aws` has `delivery: federation`, and it lists `payments`. The subject `lenny:acme:payments-aws` is stable. Acme's AWS trust policy names that subject, so only a session able to obtain this binding reaches the role. A session in `web` gets no token from the pod credential API.

**A shared pod on a recycling pool.** Pool `coding` has `recycle.enabled: true`. Alice's `web` session ends, `netagent` flushes its credentials, and Bob's `payments` session claims the pod. Bob's binding set and policy are correct, and any file state the pool recycle scrub does not reach survives from Alice's session.

**A concurrent pool.** Pool `chat` serves concurrent sessions. Bob's create in `payments` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, because the pool cannot apply `payments`' deny or its route to `acme-pci-swg` per session. His retry through `POST /v1/sessions` fails with `ENVIRONMENT_REQUIRED`. Alice's create in `web` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, because `web` has a pool that lists it, so no slot on the pod can reach `acme-web/site` through the union. Carol's create in `support`, which has no entries and no object listing it, proceeds with unscoped objects. To run this runtime for `web`, a `tenant-admin` holding a grant gives `web` a derived runtime with a one-session owned pool, once [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) lands.

**A delegated child.** Alice's `web` session delegates to a runtime that `web` selects, and the child has E = `web` with her session's inputs, as does each further hop. A target `web` neither selects nor shares answers `TARGET_NOT_IN_SCOPE`, whether Alice's group gives her membership or not. A `payments` child placed on `chat` is refused like a create.

**A shared review runtime.** Tenant `acme` turns on `dangerouslyAllowCrossEnvironmentSharing`. `web` declares `shared-review` outbound to `platform`, and `platform` declares it inbound from `web`. Alice, who is not a member of `platform`, has a `web` session delegate to it. The child has E = `platform`, `crossed_from_environment: web`, and `platform`'s hosts, routes, and bindings, and it bills to `platform`. It reads a `platform` repository on a pool with `userAuthorization: off`, and a VCS pool with `userAuthorization: required` refuses it unless Alice has access. Its results return to her session, and it can send what `web` handed it to any host `platform` allows, including one `web` denies. When it delegates to `other-tool`, which `platform` shares with `tools`, the delegation gate answers `TARGET_NOT_IN_SCOPE`. `credential.mint` and the audit event name both `web` and `platform`.

**An experiment variant.** A running experiment sends 10 percent of a runtime's sessions to a variant on pool `chat`. Bob's create in `payments` passes admission against the base runtime, and the check on the final row refuses it, since `chat` cannot apply `payments`' deny. A variant whose runtime `web`'s selector does not admit is refused in the same way for Alice.

**Onboarding a customer on the broker.** The operator creates tenant `globex`, a broker organization whose claim maps to `globex`, and a link to Globex's IdP. Alice, a Globex administrator, logs in. The broker issues `roles: [tenant-admin]` under a mapping the operator set. Alice creates environments with Globex's group names.

**A tenant on its own issuer, beside a tenant on the broker.** The operator creates tenant `acme` with a customer source for Acme's Entra tenant, with `subject_claim: oid`, `role_map` sending Acme's `lenny-admins` value to `tenant-admin`, and `group_source: scim`. The sign-in proof previews Alice's subject as `~k3f9~<oid>`, and the operator activates the source with a justification. Alice's MCP client uses `/t/acme/mcp`, whose metadata names Acme's issuer alone. `globex`, in the same deployment, stays on the broker and is unchanged.

- A broker token with `tenant_id: acme` gets `401 TENANT_ISSUER_MISMATCH`.
- A token from Acme's issuer carrying `tenant_id: globex` resolves to `acme`, because the claim is ignored.
- A token with an unknown `iss` is refused with no fetch.

**A customer's IdP asserts a platform role.** A token from Acme's issuer carries `roles: [platform-admin]`. `role_map` has no such entry, so it grants nothing, and the boundary records the drop. A `platform-admin` trying to grant Alice the role by row is refused, because `acme` is not the operator tenant.

**A stale claim against SCIM.** Bob's Entra group changes, and Acme's directory removes him from `payments-engineers` through SCIM. His token still carries that group. Under `group_source: scim`, his next `/v1/environments/payments/sessions` call gets `403`, because the claim is ignored. His running `payments` session continues. `ENVIRONMENT_REQUIRED` answers as it did before, since it reads no groups. If Acme's directory then deactivates him, his next request gets `401 USER_INVALIDATED`, whatever his token says, and his sessions end under `full_revoke`.

**A squatted issuer.** A trial tenant drafts a source naming Globex's Entra URI. The draft holds no index, so Globex can still register. The trial tenant fails the sign-in proof, since it holds no client there. Were it to pass, it would still need the operator's attestation, or under `identity.allowTenantActivation` its own `security-admin`'s approval, so that option carries the larger squatting risk. A `platform-admin` then retires the wrong row and deletes it to release the index. If the squatter's `security-admin` cancels the retirement, the operator invalidates that principal first.

**A customer on Google Workspace.** The operator lists `https://accounts.google.com` with `pinBy: claim`, `claim: hd`, and `claimIsDomain: true`. Acme's admin drafts a source with `pin_claim_value: acme.com`, and Globex's admin does the same with `globex.com`. Each passes the sign-in proof and the TXT check on its own domain, and both rows activate on one issuer. Alice's token carries `hd: acme.com` and Acme's client as its audience, so it resolves to `acme`. A personal Google account has no `hd` and is refused. A token Alice obtained for another application has `hd: acme.com` and that application's audience, and it is refused too. A service account token naming Acme's client as its audience has no `hd` and a foreign `azp`, so it is refused twice over. A Globex employee who drafts `acme.com` fails the proof, and one who signs in at Acme's callback gets a token selecting Globex's row, which the resolver refuses.

**A mix-up attempt.** Tenant `initech` runs a hostile source on its own IdP, and its admin sends Bob, an operator staff member, a proof link. Bob's browser reaches that source's authorization endpoint, which redirects to the broker with the deployment `client_id` and redirect URI. The broker returns a code to `/auth/callback/deployment`. The state cookie is scoped to the hostile source's callback path, so it is absent there, and Lenny refuses with no token request. The code never reaches `initech`. How a given broker handles that redirect is untested.

**A compromised source is retired.** Acme's IdP is compromised, and the attacker asserts `tenant-admin`, mints a SCIM credential, and opens a playground session. A `security-admin` requests `invalidateSubjects: true`, a second one confirms it, and the first retires the source. Tokens from that issuer stop, Lenny-issued tokens of its subjects are refused, the credential and the playground record are revoked, and a job ends those subjects' sessions and revokes their links. Broker tokens stay refused, because `deploymentIssuerAllowed` stays false. After Acme rotates its key, the source returns to `active` with the same `source_id` on a new proof and a justification, and tokens issued before retirement stay refused through `tokens_not_before`. The activation preflight lists role rows and credentials the attacker's subjects wrote, and an admin clears the job's holds so Acme's users return.

**An incident hold and the directory.** An admin invalidates Carol during an incident, placing an `admin` hold. Acme's directory then sends `active: false`, then `active: true`. The `scim` hold comes and goes, the `admin` hold stays, and SCIM reports `active: false`. A sync worker's `reinstate` behaves the same way.

**Activation with a security team.** Globex, on the broker, has a `security-admin` on a bare subject. A request to activate its own IdP stops at the activation preflight, which lists that principal as stranded. The `security-admin` assigns a new principal on the preview's canonical subject, which takes [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay, and approves. The activation preflight then passes, and the old row stays behind, unreachable, as "Residual risks" states.

**A departure.** Bob leaves Globex. He has logged in but has no user row. Globex's directory deactivates him, the broker receives the SCIM change, and the sync worker calls `invalidate` with `mode: full_revoke`, as `identity-sync` in tenant `globex`. Lenny creates the row, ends his sessions, and revokes his issued tokens. A later retry is idempotent. If Bob returns, the worker calls `reinstate`. A tenant whose directory calls Lenny's SCIM directly gets the same result with no worker.

**An invalidated administrator.** Carol, a `tenant-admin` at Globex, is invalidated with `full_revoke` while holding a valid IdP token. Her next admin call gets `401 USER_INVALIDATED`. She cannot call `reinstate`, and a stale `observedAt` from any caller cannot reverse a newer invalidation. Another `tenant-admin` can reinstate her.

**A mapping error.** A broker change lets Acme's mapping reach `roles`, and a token arrives with `roles: [platform-admin]`. The gateway removes the role, the caller acts with no platform authority, and the alert fires. A separate error placing `tenant_id: globex` on Acme's token is not caught, which "Residual risks" states.

## Residual risks

- **Environment scoping shares pods.** On a recycling pool, state the pool recycle scrub does not reach survives into another environment's session within the tenant, as it does between users today. An environment needing separation uses a pool with `recycle.enabled: false`, or a separate tenant (decision 5).
- **Shared tenant resources.** The mirror's store for a pool used by several environments, the Token Service token cache, egress group, and the quotas of [P7](egress-and-git-design.md#security-properties), the egress property that bounds each pod's and each tenant's egress load, are shared across environments. One environment can consume the tenant's quota (decision 6). One environment's egress address is the same as another's, so a customer firewall rule admits both.
- **Tenant-wide objects stay tenant-wide.** An environment cannot opt out of a tenant-wide binding or host. It can deny a host. The tenant admin must scope a binding to remove it from an environment. Scoping a pool also leaves a repository reachable through any wider tenant-wide pool whose allowlist names it, so the write response warns when a scoped pool's repository appears on such an allowlist.
- **A person's token across environments.** A link has no environment, so a member of two environments has one token. Under `oauth2Link` with no narrowing template, the link provider data that derives a narrower per-session token from the stored one, a session in either environment holds that token's full reach, whichever environment's repositories it was meant for.
- **Policy added after a session began.** A deny or a route added to an environment reaches its one-session pods at the next decision. A transparent session created before the entry existed keeps E = ∅, and a session already running on a concurrent pod keeps running until its next resume, as the write's `Warning` reports.
- **Cross-environment sharing.** On a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy. That data includes anything the agent can read, such as a token delivered under `delivery: file` with no `lockEgressToHosts`. A non-member's agent can drive the target's credentials and receive results, on any pool with no user check. The owner accepted both under decision 14. A `tenant-admin` writes both sides of a path, so its two sides do not separate duties.
- **Label edits on a sharing path.** A label edit through `PUT /v1/admin/derived-runtimes/{name}`, a `platform-admin`'s edit through `PUT /v1/admin/runtimes/{name}`, which [DR-1](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites), a prerequisite proposal in [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), closes to a `tenant-admin`, or a change to T's selector, adds a runtime to a declared path at once, with no delay. Posture lists paths and reports after the fact.
- **Forged adapter calls.** `lenny/delegate_task`'s parent rule trusts the session the adapter presents, so a compromised adapter can still name another session until F-10.2.29 closes.
- **Per-parent child cap.** A positive `maxChildrenTotal` caps each parent's children and does not bound a tree.
- **Shared runtimes run with the caller's objects.** This applies to a runtime several selectors admit, with no sharing path. A runtime that several environments select runs under each session's own E, so that environment's scoped credentials and repositories reach the shared runtime's code. A tenant admin decides this when writing the selector, and no inbound consent exists on the side that owns the runtime.
- **Members of several environments.** A caller in both `web` and `payments` can create in `web` and avoid `payments`' deny. A deny binds an environment's sessions and does not bind a person. Under `noEnvironmentPolicy: allow-all`, a token with no groups can likewise run E = ∅ on a runtime that no environment with denied or route entries selects.
- **Invalidation during a Postgres outage.** `lenny-ops` falls back to token roles, as above.
- **Mirror assertions.** The Token Service verifies the cause a mirror fill names, and a compromised mirror can still name any live cause in a tenant whose route names it. This is the same limit as its tenant assertion, the `x-lenny-tenant` header with which the mirror names a store's tenant on its upstream fetches.
- **Membership at create.** A user removed from a group keeps running sessions, and those sessions keep E's objects, until the session ends or the user is invalidated. Removal from an environment alone has no session effect.
- **Exfiltration between environments.** A session in `web` with a host open can send data anywhere that host accepts. Scoping limits credentials and destinations. It does not stop a user who belongs to two environments from moving data between sessions.
- **Changing a tenant's issuer.** Each issuer has its own subjects. If an operator points an existing tenant at a different issuer, Lenny treats the new subjects as new users, and data keyed on the old ones (sessions, user rows, links, and registered tokens) does not carry over. Lenny provides no migration. That includes security principal rows and `lifecycleSubjects` on old subjects, which the activation preflight lists, and a new subject cannot re-link a provider account an old one holds, because `user_links` is unique on `provider_user_id`.
- **Broker correctness.** For a tenant on the deployment source, the tenant boundary rests on one claim from one issuer. A broker fault or a compromised broker can issue a token for any such tenant, and a second claim from the same issuer adds nothing. A broker that authenticates the operator tenant can also assert a staff subject, which reaches platform authority and, through impersonation, every tenant, including pinned ones (TP5).
- **An issuer's principals.** An issuer controls every principal it authenticates, including a `security-approver`, whatever `securityRoleSource` says. On a tenant on the broker, an operator can therefore act as that approver through its broker. Posture reports the dependency, and decision 25 leaves a second factor out.
- **A customer's IdP.** A compromised or misconfigured customer IdP can assert any subject, mapped role, or token group in its own tenant, including `tenant-admin` through `role_map`. Under `securityRoleSource: managed`, `security-*` claims stay ignored, but it can assert a principal's subject, as above. It cannot reach another tenant or a platform role, subject to "Shared issuers" below for a listed issuer.
- **Hosted issuers.** No mechanical proof binds a customer to an issuer on a shared host, so a pin rests on the sign-in proof, the domain proof, and the operator's attestation. The first activation holds the index until a `platform-admin` resolves a dispute, and `identity.allowTenantActivation` removes the operator's check from that path. The denied list is known shared issuers and cannot be complete.
- **Shared issuers.** Tenants on one listed issuer share its key set and its availability, and a fault or compromise at the provider reaches all of them. A platform admin's wrong entry, naming a claim a customer can write, lets that customer's administrator assert another tenant's value, and Lenny cannot detect this. `pinBy: audience` leaves the decision to the provider, and the `azp` rule stops only what a provider marks. On a non-domain claim, one member of an organization who gets past the operator's checks can hold its value until a dispute is resolved, and until then that organization's members resolve into the wrong tenant. If an entry proves wrong, removing it does not stop existing rows, and the response is to retire those rows, under the delay where it applies, or to turn off `identity.customerSources.enabled`. These follow from decision 18, and a tenant that cannot accept them uses an issuer of its own.
- **Resold domains.** A later owner of a domain pin's value holds tokens with that value, and those subjects are new users of the old tenant for up to `identity.domainPinLapseDays` after the TXT record goes. They have no role row on a provider with no role claim, such as Google as far as the documentation shows, and on a provider with an admin-set role claim and a `role_map` a later owner's administrator can assert a mapped role. Under `noEnvironmentPolicy: allow-all` they still reach the tenant's runtimes and tenant-wide objects. `require_directory_row` stops this. An owner who republishes the old TXT value, which is public, removes the time limit. A [2024 flaw](https://www.techrepublic.com/article/google-workspace-vulnerability-accounts-exposed/) let unverified Workspace accounts sign in to third parties (secondary source), and whether those tokens carried `hd` is unconfirmed. If they did, a token could select a pinned row with no control of the domain. Google [disputes the report's severity](https://www.securityweek.com/google-oauth-flaw-leads-to-account-takeover-when-domain-ownership-changes/amp/) because `sub` differs for the new accounts, and that difference is what keeps them off the old subjects (secondary source).
- **An unlisted shared issuer.** The denied list and `identity.sharedIssuers` cannot be complete. A row with no pin on a shared issuer that neither list names would accept every customer of that provider, and the sign-in proof does not reveal this. Proof 4 and the operator's review carry it.
- **Operator reach into a pinned tenant.** A `platform-admin` keeps impersonation and can activate a source. At a tenant with no `security-admin` row in any state, nothing delays that, and [SO-4](security-oversight-design.md#so-4-operator-access-transparency), the security oversight design's operator access transparency, is what makes it visible. Where the delay applies, a stolen `platform-admin` can first invalidate the principals who could cancel and write its own justifications, so the delay, the SIEM event, and any outage are what remain to warn the customer.
- **Unchanged reactivation.** With no reachable principal, a stolen `platform-admin` with a login at a compromised issuer can undo a `security-admin`'s emergency retirement at once, by returning the row unchanged. This has no delay, because retirement has to stay reversible during an incident. `tokens_not_before` still refuses older tokens.
- **Cached keys.** An IdP rotating out a compromised key while unreachable stays trusted up to the maximum staleness.
- **A stolen `tenant-admin`.** It can mint a SCIM credential, which posture lists, and it can write role rows and custom roles. On a deployment-source tenant with no security principal, it can also set `introspectionEndpoint` and capture bearers, which [`security-oversight-design.md` decision 23](security-oversight-design.md#decisions) governs.
- **A stolen SCIM credential.** It can deactivate any unprotected user in its tenant, within its quotas, and under `group_source: scim` it can change group membership, and so environment membership. It cannot authenticate as anyone, assign a role, or clear another class's hold.
- **Egress for issuers.** The gateway's external rule and the Token Service's are wide, and each customer issuer depends on them. A strict deployer maintains addresses per customer.
- **Published metadata.** A per-tenant metadata document discloses a tenant identifier and its IdP host, and the typed-email page discloses which domains are customers.
- **Declared claims.** `match_claim` is immutable and verified because the source says so. Lenny cannot check that, and a wrong declaration brings back takeover through a directory row.
- **Ongoing proof.** Discovery and domains are re-read, and the sign-in proof and attestation run once.
- **Broker availability.** A broker outage stops new logins and token refresh for all tenants on it. Verification continues on cached keys, and running sessions continue. A customer IdP's outage does the same for its tenant alone, and a shared issuer's outage for all its tenants.
- **Running sessions on unbound rows.** A user on the deployment source whose directory row never bound has no subject to hold, so `active: false` leaves that user's running sessions up until the next IdP token matches. A Lenny-issued bearer carries no `match_claim` either, so it also waits for that. The SCIM response and an alert report the case, and R6 holds only in part here.
- **Lost issuer.** A customer that has permanently lost its issuer cannot approve or cancel its own recovery, and the operator acts under a delay nobody can cancel.
- **Stale snapshots.** A replica with a stale snapshot can mint a Lenny token after retirement with an `iat` that passes `tokens_not_before`, and it survives a later reactivation until it expires.
- **Rotated keys.** `tokens_not_before` cannot stop a compromised key that was not rotated, because such a key chooses its own `iat`.
- **Stale group data.** Under `group_source: token` with no introspection, group changes wait for a new token. With introspection, a failure of the tenant's endpoint fails its requests closed with `503`. Under `group_source: scim`, changes wait for the directory's delivery and the 30-second cache, and a store failure answers `503`.
- **Lifecycle delivery.** Lenny cannot detect a lost event. The directory or the operator's worker owns retry, and a user stays active during an outage of either side.
- **Unproven.** Nothing in the identity design has run. Open questions include whether Google's tokens carry `hd` with and without the request parameter, which spelling of its issuer each flow returns, whether an MCP client's own login yields a token that selects a pinned row, how MCP clients behave against per-tenant metadata and audiences, whether Entra's, Okta's, and other directories' SCIM traffic matches the corpus, the 24-hour staleness value, provider behaviour on the browser login path including silent single sign-on, the CPU cost of oversized keys, the dial-time address check under production resolvers, group read latency on the auth path, and memory for per-issuer verifiers across many tenants and replicas.
- **Secret in `rbac-config`.** See "Defects found while verifying".

## Defects found while verifying

These exist independent of this design. Each is filed in `BUILD-GAPS.md` under the identifier the row names, and the row names the proposal that closes it.

| Severity (proposed) | Defect | Closed by |
| :-- | :-- | :-- |
| High | F-10.2.38: an IdP-verified token's `roles` claim can assert `platform-admin` (`normalizeUserBearer` clears `typ`, `session_id`, `delegation_depth`, and `origin` only), and §10.2 **Role assignment:** permits it. | TS-2 |
| High | F-11.4.19: `invalidate` answers `404` for a user with no row, so a user known only through an IdP cannot be stopped, against §11.4's incident-response purpose. | TS-4 |
| High | F-11.4.20: `serveBearer` never reads a user's invalidation state, so an invalidated administrator with a live IdP token keeps the admin API. | TS-4 |
| High | F-10.6.16: in-pod `lenny/delegate_task` resolves environments from a principal with no groups, so group members cannot delegate under `noEnvironmentPolicy: deny-all`, and `noEnvironmentPolicy: allow-all` admits every runtime regardless of selectors. | TS-3 |
| Medium | F-10.6.17: experiment routing and the late `PreRoute` segment rewrite a session's runtime after §10.6 admission, so the explicit selector check does not cover the runtime that runs. | TS-3 |
| Medium | F-11.4.21: `PUT disabled:false` reverses a disable with no check. F-11.4.20 covers a `DELETE`d user that keeps its assigned role. `soft_disable` and `hard_disable` leave impersonation bearers live, which §13.3 rule 6 states as today's behaviour, so TS-4 changes that part and it is not filed. | TS-4 |
| Medium | F-10.6.18: tokens from `/v1/oauth/token` carry no groups, so they cannot satisfy an `oidc-group` Environment member. | TS-3 |
| Medium | F-10.6.15: delegated, derived, and replayed sessions have no environment, so their billing rollup, retention tier, and credential scope ignore it. | TS-3 |
| Medium | F-10.6.19: the transparent create path admits any member of any environment, with no role or selector check. | TS-3 |
| Medium | F-10.6.20: Environment deletion has no active-session check, against §15.1. | TS-3 |
| Medium | F-10.6.21: `GET /v1/admin/tenants/{id}/rbac-config` returns `introspectionClientSecret`, which the tenant row stores in its JSON configuration with no redaction. | TS-4 |
| Medium | F-10.6.22: `introspectionEndpoint` has no address check, and the gateway posts bearers to it. | TS-4 |
| Medium | F-10.2.39: the gateway serves no RFC 9728 metadata and no `WWW-Authenticate` challenge, against §10.2's statement that it is an MCP-standard protected resource server. | TS-2 |
| Medium | F-11.4.21: no endpoint reverses `hard_disable` or `full_revoke`. | TS-4 |
| Low | F-10.2.40: §10.2 and `docs/client-guide/authentication.md` name a `lenny_role` claim, and the code reads `roles`. `docs/tutorials/multi-tenant-setup.md` and `docs/operator-guide/security.md` show `auth.oidc.issuer`, `tenantClaim`, `roleClaim`, `jwksUri`, `audience`, and `auth.issuerUrl`, none of which the chart defines. | TS-2 |
| Low | F-15.1.74: `/v1/admin/users` exists in code and is absent from the §15.1 endpoint table. | TS-2 |

Reading the tree for revisions 13 and 14 found these more, and the last row came from a separate read. They exist today with a single issuer, and each is filed in `BUILD-GAPS.md` under the identifier the row names.

| Severity (proposed) | Defect | Evidence | Closed by |
| :-- | :-- | :-- | :-- |
| Medium | F-10.2.41: an IdP-verified token keeps Lenny's internal claims, and a non-Lenny `scope` refuses the token. `caller_type`, `act`, `authorized_tools`, and `scope` survive on a signed token, and `serveBearer` copies `CallerType`, `Scope`, and `Act` to the principal. A token with `scope: "openid profile"` gets `401 TOKEN_INVALID`. A claim spelled `TENANT_ID` can fill `tenant_id` and `ROLES` can fill `Roles`, because Go's struct decoding also matches other cases, which an in-process check confirmed. A custom `auth.tenantIdClaim` is read by exact key, so the tenant case arises under the default name. | `normalizeUserBearer` in `pkg/auth/jwt/oidc.go`, `Claims` and `decodeClaimsJSON` in `pkg/auth/jwt/jwt.go`, `serveBearer` in `pkg/gateway/middleware/auth/auth.go`, `Parse` and `ParseScope` in `pkg/common/scopes/scopes.go` | TS-2 |
| Medium, filed Low with one operator-configured issuer | F-10.2.42: the JWKS parser has no upper bound on RSA size and no limit on `e`, verification runs before the rate limit, and the gateway sets no `MaxHeaderBytes`. The CPU cost is an unmeasured estimate. | `oidcMinRSABits` and `publicKey` in `pkg/auth/jwt/oidc.go`, no `MaxHeaderBytes` in `cmd/` or `pkg/`, and the order in `cmd/lenny-gateway/httpsurface.go` | TS-2 |
| Minor | F-13.2.25 already holds the first half, and F-13.2.30 holds the local default's open rule. In production mode or with any cloud KMS, an external issuer's caller tokens fail at `/v1/oauth/token` under an enforcing CNI, while Lenny-issued callers work. `allow-token-service` has no external HTTPS rule apart from `kms.endpointCIDR`, which the chart requires to be narrow in that mode. Under the local provider's default, that row opens TCP 443 to everything, including private ranges and IMDS, with no excepts. The Token Service should use `lenny.kmsEgressRule`, as `allow-gateway-egress` and `lenny-ops-egress` do. | `allow-token-service` in `charts/lenny/templates/system-network-policies.yaml`, `lenny.kmsEgressRule` in `_helpers.tpl`, `kms.endpointCIDR` in `values.yaml`, `buildCallerVerifier` in `cmd/lenny-token-service/caller_verifier.go`, and §13.2.4's Token Service row | TS-6 |
| Medium, filed Low with one operator-configured issuer | F-27.3.17: the playground's token request follows redirects. The rest of this row becomes a defect only under per-tenant sources. It posts the code and verifier before checking the ID token, on one callback path, and tenant and user come from raw claims. With one issuer the harm is limited, and the code is what TS-6 replaces. | `Exchange` and `extractSubject` in `pkg/gateway/mcpfabric/playground/oidc.go`, `buildPlaygroundOIDC` in `cmd/lenny-gateway/playground_oidc.go` | TS-6 |
| High | F-8.2.34: a client's `delegationLease` at session creation has no effect. It is validated, stored, and echoed, and delegation reads the Helm fallback, and zero reads as unset, so `maxChildrenTotal: 0` would mean no limit if connected as it stands. | `DelegationLeaseRequest` references, `checkDelegationDepth` in `pkg/gateway/mcpfabric/delegation/service.go`, `pkg/delegation/lease/lease.go`, and §8.2 **Effective `maxDepth` resolution** | TS-3 |

Revisions 15 and 16 found more in today's tree, and each is filed in `BUILD-GAPS.md`:

- F-8.2.35 (High, TS-3): a runtime can name another session of the same owner as delegation parent.
- F-9.1.13 (High, TS-3): the intra-pod tool surface forwards every registered tool, so a runtime can create a root session.
- F-27.3.18 (High, TS-7): the playground session cookie's path excludes the mint at `/v1/playground/token`.
- F-13.3.27 (Medium, TS-2): a playground-origin `subject_token` loses `origin` on exchange, and `lenny-ops` accepts the result.
- F-10.6.23 (Low, TS-3): `runtimeAuthorizedForCaller` returns true on a `Runtimes.Get` error.
- F-10.6.24 (Low, TS-3): an empty `runtimes` selector on a cross-environment declaration admits every runtime.
- Folded into F-8.2.34: `CreateAndStartRequest` drops `delegationLease`, `BindSingleShot` rows hold a nil lease, `CheckDepth` refuses 0 with `ErrUnresolvedMaxDepth`, and `buildLineage` ties its walk to Helm.
- Folded into F-11.4.21: the bootstrap seed's force update reverses a disable.

`lenny/await_children` also admits any session of the owner through `in.SessionID`, which is folded into F-8.2.35. F-8.3.12 (Medium, TS-3): `gateway.delegation.defaultMaxDepth` is not a chart value, so nothing renders it to the gateway or validates it as at least 1.

## Changes requested of egress-and-git-design.md

Revision 55 of [`egress-and-git-design.md`](egress-and-git-design.md) carries the earlier requests: the `environment` and `crossedFromEnvironment` envelope fields, the `pkg/binding` resolver hook, the prefix overlap rule, the delegated child rule, and the TS-1 dependency row. No hook remains from the retired bound.

| Request | Proposal | Why now |
| :-- | :-- | :-- |
| Largely applied: the mirror's I8 request names the pool and its cause, and the mirror prefetch resource accepts a pool name. Still open: the Token Service reads the cause itself. | EG-5 and EG-2 | I8 is a wire contract and the mirror is a shipped enforcement point. Without this the Token Service has no independent environment check on a mirror fill, and same-prefix pools have no answer for a prefetch. Naming the pool is correct with no environment involved, because a store's identity already includes the credential. |

The `environments` fields, `environmentPolicies`, the effective-policy parameter, and the binding host validator are additive and need nothing else from that design. `netagent` needs no change, because it receives resulting sets. Its credential cache also needs none, for the reason under "Enforcement points". On a pod with several sessions, the resolver receives E = ∅ from TS-1's own rule, which leaves the union in [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) as written.

## Specification sections affected

- §2 Goals and Non-Goals: the identity model, and what stays with an operator.
- §4.3: the environment that a child and a derived session use for credential scope.
- §4.9: `environments` on VCS credential pools and credential bindings, and the overlap rule.
- §5.2 and §6.4: a warm-list entry of any declarer needs a repository that resolves at E = ∅ to an unscoped VCS pool or lies under no prefix, at write and at render, with the suspended reason `environmentScoped`.
- §7.3: the refusal at resume on a pod with no per-session policy.
- §8.2: "Every effective delegation lease MUST carry a positive integer `maxDepth`" allows 0 as an explicit client or parent value, the **Effective `maxDepth` resolution** order becomes a minimum, `maxChildrenTotal: 0` is stored as `maxDepth: 0` and a positive value is a per-parent cap, the parent is the calling session, depth comes from the parent's row, the environment a delegated child takes, refusal at placement, and no second crossing.
- §8.3: `inherit`'s check runs under `dangerouslyShare`.
- §8.10: the stored `maxDepth` on the lease record.
- §9.1: the **Platform MCP Server Tools** table becomes the intra-pod allow-list, with `lenny/vcs_token` until [EG-3](egress-and-git-design.md#proposal-split), the egress design's pod git path, and `lenny/delegate_task` is hidden per session where the surface allows.
- §14: `delegationLease` semantics, including 0 and the create-and-start request, and that `delegationPolicyRef` has no effect.
- §25.1: an IdP token's `scope` is filtered to `tools:` values at the boundary, and `caller_type: "agent"` comes from the source's mapping.
- §10.7: the checks that run on the final row after experiment routing.
- §10.2: the identity model paragraph, the source record, the verifier registry, `tenant_id` extraction by source, canonical subjects, the broker contract, the discovery routes, the claim allowlist, RSA bounds, the operator tenant, **Role assignment:**, the IdP role boundary, refusal of invalidated principals, the `manage_user_lifecycle` row of the permission matrix, and claim names.
- §10.3: the configuration validation for `identity.*` values, and `auth.oidc.issuerUrl` as the default source.
- §13.2.4 and §13.2.5: the Token Service's use of `lenny.kmsEgressRule` and its external rule, and the shared address checker with its added ranges.
- §13.3: the claim allowlist's effect on exchanged tokens.
- §27.3.1: per-source callbacks, the shared resolver, the refusal before a token request, per-tenant cookies, and the mint under the prefix.
- §13.3: refusal of a playground-origin `subject_token`.
- §25.4: `lenny-ops` keeps one issuer.
- §10.6: scoped objects, session environment rules, the session-based delegation gate, `dangerouslyShare` replacing `crossEnvironmentDelegation`, refusal of wildcard names against the example showing `sourceEnvironment: "*"`, the tenant setting, that a crossing child is in the target and uses its connectors, and what its policy is, the `identityProvider` description, namespaced group members, `group_source`, and **V1 data model accommodations**.
- §11.4: invalidation of a subject with no row, the effect of each mode on authentication, holds per caller class, reinstatement, event ordering, and SCIM as a caller.
- §13.3: groups on exchanged tokens, and the rule 6 cascade on `soft_disable` and `hard_disable`.
- §11.7: both environments on events for a crossing child, and the `auth.platform_role_dropped`, reinstatement, identity source, and SCIM credential audit events.
- §12 and §12.8: the source, directory, and SCIM credential tables, with their erasure.
- §12.9: classification of the upserted user row, the source record, directory rows, and SCIM credentials.
- §14: plan validation against the environment.
- §15.1: the environment body field, the new fields and error codes, `/v1/admin/users`, `reinstate`, `identity-sources`, the `/t/{tenantId}` prefix, the SCIM surface, the environment delete guard, and the `environment` filter on evidence reads.
- §16: the dropped-role counter and alert, the refusal counter, per-source key staleness, registry staleness, and SCIM request and failure metrics.
- §17.6: `auth.rolesClaim`, `auth.groupsClaim`, `identity.*`, `gateway.delegation.defaultMaxDepth`, and the render-time refusal of `auth.platformAdminTenant: platform` (decision 27).
- §15.1: `DELEGATION_DISABLED`, `PARENT_SESSION_MISMATCH`, the `dangerouslyShare` field, `CROSS_ENVIRONMENT_SHARING_DISABLED`, and the access report's paths.
- §18.35: the Phase 15 exit criteria, for the opt-ins and the session-based gate.
- §12: `sessions.crossed_from_environment`.
- §24: `lenny-ctl` commands for reinstatement and for scoped fields.

The sections that [`egress-and-git-design.md`](egress-and-git-design.md) lists change first, and the TS-1 edits apply on top of the text those proposals land.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| TS-1 | Environment scoping | The fields, the formulas, enforcement in the gateway, the decision service, the Token Service, and the mirror, the refusal rules including checks after routing, the binding host validator, the delete guard for references, shadow policy evaluation, and the read filters. |
| TS-2 | Identity model and platform boundary | The specification statement, the broker contract, the deployment protected resource metadata route, the claim allowlist, RSA and token size bounds, the IdP role boundary, the operator tenant and the chart's refusal of `auth.platformAdminTenant: platform`, the playground-origin `subject_token` refusal, claim names, and the §15.1 listing of `/v1/admin/users`. Closes F-10.2.38, F-10.2.39, F-10.2.40, F-13.3.27, and F-15.1.74. |
| TS-3 | Session environment context | Derive and delegation propagation, the session-based delegation gate, cross-environment sharing with its opt-ins, the check, no second crossing, both environments on records, the client delegation limits with one reading of `maxDepth` and a stored value on every root creator, depth from the parent's row, the calling-session parent rule, the Bridge allow-list, and `DELEGATION_DISABLED`, groups on exchanged tokens, the active-session delete guard, the transparent create check, and the naming correction. Closes F-8.2.34 apart from `delegationPolicyRef`, F-8.2.35, F-8.3.12, F-9.1.13, F-10.6.15, F-10.6.16, F-10.6.17, F-10.6.18, F-10.6.19, F-10.6.20, F-10.6.23, and F-10.6.24. |
| TS-4 | User lifecycle path | Upsert on invalidate, holds per caller class, refusal at authentication including deleted rows, the impersonation cascade on disables, reinstate as the only reversal, ordering, the permission, and `identityProvider` hardening. Closes F-11.4.19, F-11.4.20, and F-11.4.21, and the read half of F-10.6.21 and the address check of F-10.6.22. |
| TS-5 | Delegated environment administration | Environment `admin` writes to its own environment's scoped objects. Decision 7 defers it. |
| TS-6 | Identity sources and the verifier registry | The operator tenant warning in preflight and posture, `identity.sharedIssuers` with the pin fields, selection after verification, and the domain pin's lapse, the record and its state machine, including `suspended` and reactivation, the activation rule and preflight, retirement with `tokens_not_before`, reactivation, `pending_version` and the approval digest, the deployment settings row, the client secret, the registry and active-source check, per-issuer caches, claim mapping, canonical subjects and the group form, per-source callbacks and the shared resolver, the pin and proofs, the address checker's use on issuers, and the Token Service's egress rules and chart checks. |
| TS-7 | Login routing | The `/t/{tenantId}` prefix, per-tenant metadata and challenges, the playground's per-tenant pages, chooser, cookie scope, and mint under the prefix, and email domain routing. |
| TS-8 | SCIM server | The surface, credentials and quotas, the retirement step that revokes credentials, subject matching, deprovisioning into TS-4, user rows, and the conformance corpus. |
| TS-9 | Directory groups | Group tables, `group_source`, the group reader ahead of `envaccess`, the access report's expansion, and erasure and classification. |

These land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework: the claim allowlist in TS-2, holds in TS-4, and per-source callbacks, the deployment settings row, the group form, `suspended`, and reactivation in TS-6.

Dependencies are stated here and in the other design documents. Proposals from different designs can interleave as long as these hold.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| TS-1 | [EG-1](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split), [EG-3](egress-and-git-design.md#proposal-split), [EG-4](egress-and-git-design.md#proposal-split), [EG-9](egress-and-git-design.md#proposal-split), and TS-3 | The objects it scopes, the resolver, the git inputs, and a dependable session environment |
| TS-1 (mirror steps) | [EG-5](egress-and-git-design.md#proposal-split) | Pool selection and the fill request |
| TS-1 (route step) | [EG-6](egress-and-git-design.md#proposal-split) | The upstreams routes name |
| TS-1 (read filters step) | [EG-8](egress-and-git-design.md#proposal-split), and the envelope field in [EG-10](egress-and-git-design.md#proposal-split) | The reads it extends |
| TS-2 | None | It can land at any point, and it should land early because it closes a High defect |
| TS-6 | TS-2 and TS-4 | The role boundary, and the shared address checker |
| TS-6 (activation under a delay) | [SO-1](security-oversight-design.md#so-1-security-roles) and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), whichever lands second adding the case | `security-admin` approval and the weakening delay |
| TS-7 | TS-2 and TS-6 | The deployment route and the sources it names |
| TS-8 | TS-4 and TS-6 | Invalidation levels, the permission, and source identifiers |
| TS-9 | TS-3 and TS-8 | Groups on exchanged tokens, and directory data |
| TS-3 | None | It can land at any point |
| TS-3 (evidence field step) | The envelope field in [EG-10](egress-and-git-design.md#proposal-split) | Both environments on `credential.mint` and evidence |
| TS-8 (retirement step) | TS-6 | Retirement names the credentials |
| TS-3 (guardrail case) | [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), whichever lands second adding the case | `crossEnvironmentSharing: forbid` and the delay |
| TS-3 (lock case) | [UC-5](user-credential-delivery-design.md#uc-5-delivery-file), whichever lands second adding the case | Check 2 reads the lock UC-5 adds |
| TS-4 | None | TS-2's contract refers to it, and either order works |
| TS-5 | TS-1 | The scoped fields |

No proposal of the egress design (`EG-`), the security oversight design (`SO-`), the forensics design (`FE-`), or the outbound connector design (`OC-`) depends on a TS proposal. The relationships in the other direction are these:

- The hooks sit inside the egress design's proposals [EG-1](egress-and-git-design.md#proposal-split) (git prerequisites), [EG-2](egress-and-git-design.md#proposal-split) (the pod network agent and proxied egress), [EG-5](egress-and-git-design.md#proposal-split) (the git mirror and warm fill), [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects), and [EG-10](egress-and-git-design.md#proposal-split) (evidence ingest), so those proposals do not wait.
- [`security-oversight-design.md`](security-oversight-design.md) narrows the question of [SO-1](security-oversight-design.md#so-1-security-roles) (security roles): under `securityRoleSource: managed`, a `security-*` claim is ignored from any issuer. A customer source's `role_map` cannot name one, so `securityRoleSource: oidc` needs SO's own rule to read such a claim, which TS-6 leaves to whichever lands second.
- The guardrails of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) act on TS-1's writes through the single write path for tenant opt-ins that [EG-9](egress-and-git-design.md#proposal-split) adds, and they can forbid an environment's use of an opt-in with no TS change.
- Routes to an outbound connector under [OC-2](tenant-outbound-connector-design.md#proposals) (the connector's hosts and policy) can appear under `environmentPolicies` with no OC change.
- [UC-1](user-credential-delivery-design.md#uc-1-credential-types-and-the-registered-source) and [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) of the user credential design define types and link providers. TS-1 adds their `environments` field when both have landed, and neither waits. [UG-2](user-git-identity-design.md#ug-2-repository-authorization) (repository authorization) and [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding) (the `userCredential` binding) read TS-4's invalidation level through `pkg/binding`, which returns it from [EG-2](egress-and-git-design.md#proposal-split). UC-2's start page uses TS-6's callback and resolver, so UC-2 depends on TS-6's login path on every source, and a broker's URI is registered once.

## Documentation

Each proposal carries the documentation for its own surface. Pages describe behaviour with no section numbers, and they land after the specification text they describe. The owner pages are these:

| Page | Change |
| :-- | :-- |
| `operator-guide/identity.md` (new) | Owns the identity model, choosing a broker or a customer issuer, the broker contract, broker selection criteria, activating a source and its proofs, claim mapping, the role boundary, and the lifecycle path. It states that a tenant's issuer is a choice to make at onboarding, and that Lenny provides no migration. It includes one worked integration under decision 12. |
| `operator-guide/scim.md` (new) | Owns the SCIM surface, credentials, subject matching per directory, deactivation, directory groups, what Lenny does when SCIM and tokens disagree, and the conformance status per directory. |
| `operator-guide/multi-tenancy.md` | **Environments** gains the choice between a tenant and an environment, a section on cross-environment sharing that states the two accepted risks and what user authorization does not cover, and a link to the scoping page. **Multi-Tenant Mode** links to the identity page. |
| "Tenant configuration" in the [`egress-and-git-design.md`](egress-and-git-design.md#new-chapter-in-the-operator-guide) chapter | Gains environment scoping: the fields, how they combine with tenant settings, and the refusal rule. |
| "Git access", "Credential bindings", and "Egress policy" in that chapter | Each states the scoped form of its objects in a few sentences and links to "Tenant configuration". |
| `operator-guide/security.md` | **Client Authentication** shrinks to a summary with corrected keys and a link to the identity page. The network section gains the issuer egress rules. |
| `operator-guide/web-playground.md` | Per-source login, and a correction to its statement that exchanging a playground bearer succeeds, since TS-2 refuses it. |
| `client-guide/delegation-and-tasks.md` | How a client disables delegation with `maxDepth: 0`, what `maxChildrenTotal` does, and that a client can only narrow. |
| `client-guide/authentication.md` | Corrects the role claim name, explains which sessions see which objects by access path, and states the per-tenant MCP and playground URLs. |
| `tutorials/multi-tenant-setup.md` | Replaces Helm keys the chart does not define, and adds an environment-scoped step. |
| `reference/configuration.md`, the API reference, the concepts page, and the error reference | New values, fields, endpoints, and codes, and the rename of `crossEnvironmentDelegation`. |
| `operator-guide/lenny-ctl.md` | The new commands. |
| `reference/glossary.md` | Entries for identity broker, identity source, canonical subject, and scoped object. |

Tier 11 checks the examples against the chart and the schemas.

## Tests

Each proposal turns this list into tests with `// spec:` annotations at the tiers `test-coverage.md` maps. No test exists yet.

### Tier 0: static

- The schemas of the new fields validate, and an empty `environments` list fails validation.
- The chart renders `auth.rolesClaim`, `auth.groupsClaim`, and the `identity.*` values to both binaries, and the binaries define the flags. It renders `gateway.delegation.defaultMaxDepth` to `--delegation-default-max-depth` and refuses a value below 1.
- The chart refuses `identity.customerSources.enabled` with the gateway's external rule disabled, and renders the Token Service's rule.
- The chart refuses `auth.platformAdminTenant: platform` at render time with a message naming the reservation of the tenant ID `platform` in §10.2, and renders any other valid tenant ID.
- SCIM `Schemas` and `ResourceTypes` documents validate.

### Tier 1: unit

- `visible`, `always`, `requestable`, `denied`, and `routes` cover E = ∅, a matching E, a non-matching E, and each formula case.
- A tenant deny beats an environment allow, and the ceiling bounds an environment entry.
- Pool selection covers the longest prefix, disjoint lists, and refusal of a tenant-wide overlap and of an overlapping list.
- The IdP boundary removes `platform-admin` and keeps other roles, and a Lenny-issued token is unaffected.
- The registry covers an exact match, a near match, an oversized `iss`, an unknown `iss` with a counted zero fetches, and a token whose `iss` names one source and whose signature is another's.
- Tenant resolution covers a customer source ignoring a tenant claim, and `TENANT_ISSUER_MISMATCH`.
- Canonical subjects cover the prefix, the length limit, a deployment `sub` beginning with `~`, and `issuer` and `subject_claim` immutable on a row.
- `role_map` covers unmapped values, and refuses `platform-admin` and `security-*` at write. A `platform-admin` row is refused outside the operator tenant on each write path, and the resolver ignores one found elsewhere. Preflight and posture warn when more than one tenant exists and the operator tenant's `deploymentIssuerAllowed` is true, and stay silent with one tenant or with the flag false.
- The claim allowlist drops `caller_type`, `act`, `authorized_tools`, and `scope`, reads each kept claim by exact key, and a token with `scope: "openid profile"` verifies. `service_claim` sets `caller_type`.
- JWKS parsing refuses an RSA key over the limit and any `e` other than 65537, and a token over 16 KiB is refused before parsing.
- Holds cover each class, the highest remaining mode, and a class clearing only its own.
- Every change on an active source is classified, with only the stated narrowings applying at once, and an empty `audiences` is refused. `azp` is checked.
- The preflight refuses and lists, and it writes nothing else.
- Source and group names refuse `~`, and `deployment` is reserved.
- The address checker covers each excluded range, IPv4-mapped forms, redirects, size caps, and a name that re-resolves at dial.
- SCIM parsing covers each `PATCH` form, each filter, a refused filter, and boolean spellings in the corpus. The disagreement table is covered row by row.
- `observedAt` ordering covers older, equal, and newer calls, with an invalidation winning a tie.
- Lease resolution covers explicit 0 against absent on the request, in `MaxDepthInputs`, and through JSON storage, including `IsZero` with only `MaxDepth` set. A nil stored value refuses delegation. The minimum covers each layer present or absent, with the Helm fallback used only when none is, and a client value above `base` does not raise it. A client's `maxChildrenTotal: 0` is stored as `maxDepth: 0`, the integer keeps 0 as no limit, and a positive value caps one parent's children. Depth reads `parent.DelegationDepth`, unchanged when the Helm value moves.
- A delegated child takes the parent's E when the selector admits the runtime. The check covers each of its conditions alone: a session's own lock, a child holding no binding under a lock inherited from its parent, and a derive from such a child, the setting off or suspended, E = ∅, a set `crossed_from_environment`, an absent outbound or inbound entry, T's selector, and list order selecting T. A session with a crossing mark is refused inside T once the setting or either name goes.
- The crossing formula covers `always(T)`, `denied(T)` with the tenant's entries, no input from the parent, and the `restricted` row. A write with a `*` or wildcard source or target, or an empty selector, is refused. Adding an entry is refused with the setting off or forbidden, and removing one succeeds. Deleting an environment a path names is refused.
- `ENVIRONMENT_REQUIRED` covers denied and route entries, and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` covers those, entries that only add access, objects that list E, and objects visible to E alone. `ENVIRONMENT_REQUIRED` gives the same answer for a token with groups and one without, under both `noEnvironmentPolicy` values, and it does not refuse a resume.
- The checks on the final row cover a variant runtime, a variant pool, and a late `PreRoute` rewrite on a create, and a variant on a delegated child, including a rewrite that changes whether the hop crosses.
- The Bridge's list and call admit the §9.1 table's names and `lenny/vcs_token`, and refuse every other.
- `scope` keeps `tools:` values, drops and counts others, reads absent when none remain, and refuses a malformed `tools:` value and near-misses such as `tool:pool:read` and `TOOLS:pool:*`. `service_claim` sets `service` and `agent`. `caller_type` reads `human` by default. An absent subject claim refuses the token.
- A shadow candidate with `environmentPolicies` records its outcome under the session's E and changes no outcome.
- The binding host validator covers a tenant-wide binding and a binding listing several environments.
- The warm-list predicate at write refuses a warm default clone, a cache-only entry, a `gitRepository` warm fill, and a private entry on a tenant-owned pool's row whose repository only a scoped pool covers, and passes a repository under no prefix and one that a tenant-wide pool and a narrower scoped pool both cover. At render, a VCS pool write that adds `environments` to the covering pool passes with a `Warning`, the entry reads as suspended with `environmentScoped`, and it returns when the pool is unscoped. The unbound and tenant-bound formulas include the warm list's hosts.
- A future `observedAt` is stored as server time, so a later call at server time applies, and repeated future stamps do not extend it.

### Tier 2: component

- Admin handlers refuse an unknown environment name, and refuse deletion while a reference or an active session exists.
- `pkg/binding` returns the environment from the session row, and it fails closed on a read failure.
- Source state transitions in Postgres, with the unique index refusing a second tenant on one issuer and the deployment issuer refused. On a listed issuer, two tenants with different pin values activate and two with one value conflict, concurrent activations of a pinned row and a row with no pin leave one, an overlap such as an empty audience beside a set audience on one claim value is refused, and a pin field write is refused apart from `claim` to `audienceAndClaim`, which keeps `source_id`. The configuration preflight refuses an entry over a row with no pin. Such a row that meets an entry or a marker is suspended in one transaction, its IdP tokens, Lenny-issued tokens, SCIM credentials, and playground records stop, and removing the entry, a rollback, or a later reactivation does not restore it. Removing an entry leaves the pin, the login parameters, and the TXT re-read in force. A row equal to `auth.oidc.issuerUrl` or an alias is suspended and the gateway still starts. A retired row returning into an overlap is refused. A missing TXT record suspends past its bound, lookups that keep failing suspend past theirs, a mass miss holds instead, every return of a domain pin needs a subject already holding a role row, including from `retired`, and the TXT check repeats at effect. `subject_claim: email` and a tenant-chosen `pin_claim` are refused. A shared issuer's excluded address holds the issuer, changes no row, cancels no pending version, and clears itself. `require_directory_row` refuses a subject with no directory row.
- Directory tables under row-level security, with one tenant's credential unable to read or write another's rows.

### Tier 3: contract

- The admin API representation of each scoped object and of `environmentPolicies`.
- The refusal for an out-of-scope object and for an unknown object are byte-identical apart from the request's own name (TP4).
- The `reinstate` and `invalidate` request and response formats, including `applied`.
- `DELEGATION_DISABLED` on REST and MCP envelopes, and `delegationLease` echoing an explicit 0.
- The evidence envelope carries `environment` on each producer's stream.
- The `identity-sources` resource, the `/t/{tenantId}` paths, and RFC 9728 documents with `WWW-Authenticate`.
- SCIM requests and responses against RFC 7643 and RFC 7644 as far as the surface goes, including the error schema, and against the recorded directory corpus.

### Tier 4: integration

- A session create through each explicit path sets E, and through the transparent path leaves it empty.
- Derive and delegation propagate E, and billing rollup and retention follow. A crossing child bills and retains under T, and its records name both environments. Same-environment descendants copy `crossed_from_environment` and cannot cross.
- A session under `maxDepth: 0` gets `DELEGATION_DISABLED` from a runtime's direct `lenny/delegate_task` call through the intra-pod server and at the gateway edge, the intra-pod list omits the tool, and `lenny/discover_agents` is empty. `POST /v1/sessions/start` applies the field, and every root creator writes a value. A different `parentSessionId` or `lenny/await_children` session from an in-pod caller gets `PARENT_SESSION_MISMATCH`. A session with an absent value delegates up to the resolved limit, and that limit stays stored when the Helm value later changes.
- A child cannot widen its parent's value through `delegate_task`. A derive stores the source's value minus its depth, and a resume keeps its row. Tree recovery after a pod failure brings a node back with the same stored value. A non-empty `delegationPolicyRef` is accepted, echoed, and changes nothing, and §27.4's playground payload still creates a session.
- A session under `maxDepth: 0` cannot cross with both sides declared.
- A declaration or setting change applies to the next `delegate_task` and revokes no in-flight child. An experiment rewrite off the path refuses the delegation. A `userCredential` binding crosses only when the lease names it and T can see it. A VCS pool in T with `userAuthorization: required` checks the parent's user, and a pool with `userAuthorization: off` does not. A crossing child uses T's connectors and has no token of the caller's environment. §8.3's `inherit` check runs on a crossing.
- The forbid of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) refuses the write and suspends the paths, turning the setting on waits out the delay, and posture lists the paths.
- A `viewer`, and a member whose environments do not select the runtime, are refused on the transparent create.
- The existing `cross_environment_delegation_test` moves to the opt-ins, with an in-pod caller whose principal has no groups.
- Edit propagation: removing E from a binding refuses the next request on the pod credential API, and adding a host to `denied(E)` refuses the next decision within the policy version rule, under which every policy write increments a version in the decision cache key, so an edit applies at the next decision rather than when the cache entry expires.
- Invalidating a subject with no row creates the row and ends sessions. After `full_revoke`, a fresh IdP token is refused on every path, including the admin API and `/v1/oauth/token`. After `soft_disable` or `hard_disable`, the same token keeps session reads and loses admin authority. Reinstate restores access. A user under erasure cannot be reinstated, and a caller cannot reinstate itself.
- An in-pod delegation with no groups on the principal follows the session's E under both `noEnvironmentPolicy` values. A chain of delegations keeps E's denies and routes at every hop, and the decision service, the Token Service, and the mirror agree two hops down.
- `PUT disabled:false` is refused on an invalidated row. A `DELETE`d administrator's token is refused. `soft_disable` and `hard_disable` by a `platform-admin` revoke the user's impersonation bearers. A token from `/v1/oauth/token` carries the subject's groups and enters an environment by `oidc-group`.
- A mirror fill names a pool and cause. The Token Service refuses a pool not visible to the cause's E, and a prefetch of a scoped pool by a non-admin is refused.
- `introspectionEndpoint` resolving to an excluded range is refused at write and at call.
- The metadata route names the issuer, and a 401 carries the challenge header. A per-tenant document names only that tenant's active issuers.
- A fake OIDC issuer per tenant: login, verification, mapping, and rotation with a new `kid`, on the gateway and on the Token Service at `/v1/oauth/token`.
- A proof login runs on a draft through the resolver. A `platform-admin`'s retirement at a tenant with a `security-admin` waits out the delay and can be cancelled, and a `security-admin`'s applies at once. `invalidateSubjects` needs a second `security-admin` or the delay, and its holds survive reactivation until cleared. A stranded row counts as active for approval. Reactivation needs a new proof and a justification, and drops cached keys. `tokens_not_before` refuses older IdP and Lenny-issued tokens, and tokens with no `iat`, on the gateway, on `/v1/oauth/token` for each token, and on `lenny-ops`. A retired issuer stays held until its row is deleted.
- A `retired` or `suspended` row refuses writes, and a reactivation that changes a field waits as a `pending_version` under the activation rule, while an unchanged return does not. A suspension cancels a pending version.
- A first activation's row refuses writes, a draft edit clears `proof`, and a digest that no longer matches stops the change at effect. A change on an active source or settings row leaves the row `active` on live values during the delay, further widening is refused, and a narrowing or retirement cancels the pending version. Activation and a `deploymentIssuerAllowed` change wait out the delay after every `security-admin` is invalidated. Only a `draft` or `retired` row can be deleted, and a `retired` row's deletion takes the delay where one applies. `introspectionEnabled` is refused at a tenant with a customer source. A playground-origin `subject_token` is refused.
- A deployment token matching an inactive directory row is refused whether or not a user row exists, a login hold on a security principal follows `lifecycleSubjects`, and `active: false` on an unbound row reports in the SCIM response and fires the alert.
- A callback refuses with no token request when its state cookie is absent, when `state` does not match, when the state's source differs from the path's, or when RFC 9207 `iss` differs. A bare playground login and the link start page go through the resolver and answer `TENANT_ISSUER_MISMATCH` for a broker token naming a pinned tenant. `/t/{tenantId}` refuses a token of another tenant.
- A proof login needs the drafting admin's session or a live one-time token, creates no session, and a token cannot be used twice. Retirement refuses Lenny-issued tokens of its subjects, revokes SCIM credentials and playground records, and `invalidateSubjects` reports partial failure. Reactivation keeps `source_id`.
- A secret is returned by no route, is sent only by the Token Service, and is unusable after a sealed field changes.
- A SCIM deactivate reaches each `scim_deactivate_mode`, `active: true` clears only SCIM's own hold, re-provisioning an erased subject is refused, a security principal needs `lifecycleSubjects`, `externalId` cannot change, quotas apply, and a provisioned row leaves token roles in effect.
- Under `group_source: scim`, a stale claim does not admit, a removal takes effect within the cache bound, a store failure answers `503`, and `introspectionEnabled` is refused.
- `match_claim` binds once on the deployment source, a matching login on an inactive row is refused, a deployment `externalId` beginning with `~` is refused, and a `deployment` credential stops when the tenant disallows the deployment issuer.
- Erasure removes directory rows and memberships, and tenant deletion removes sources, credentials, and directory rows.
- `GET rbac-config` does not return the introspection secret.

### Tier 5: end to end on Kind

- Two environments in one tenant run the first scenario in full: clone, push, a refused fetch, a bound host, and a refused host, through `netagent`, `lenny-egress`, the decision service, and the Token Service.
- A sequentially reused pod carries no credential across a change of environment.
- A runtime on Kind under `maxDepth: 0` calls `lenny/delegate_task` directly and is refused, with no child pod claimed, and a pod failure in a tree recovers no wider lease.
- A crossing child on Kind reaches T's hosts, is refused a tenant-denied host, and reaches a host the caller's environment denies, which pins the accepted behaviour.
- A concurrent pool refuses a session in an environment with anything of its own and a plan naming a scoped repository, and it admits an environment with nothing of its own. A session or delegated child carrying `egress.allowedHosts` or `egressLockHosts`, inherited included, is refused. A slot on an admitted pod cannot reach a scoped repository through the union.
- A create, derive, and delegated child each meet the same refusal on that pool, a resume meets it for a deny, a route, or a lock, and a resume or a later service-mode request proceeds with only a later additive entry.
- Tenants on the deployment issuer and on a fake customer issuer run at once in one deployment, each refusing the other's tokens, under the chart's NetworkPolicy with an enforcing CNI, on the gateway and on the Token Service.
- A single-company install with no source behaves as before, and subjects keep their form.

### Tier 6: end to end on cloud

No behaviour needs cloud resources for automation. A manual per-release check runs each named directory's own SCIM validator and a live login against a test tenant, where the operator has one.

### Tier 7: load

- Concurrent writes to an object's `environments` and to session creates produce a consistent decision for each session.
- Repeated identical `invalidate` calls during a `reinstate` converge on the state of the newest stored time.
- The resolver adds no measurable decision latency beyond the existing cached policy read, measured against a stated threshold in the proposal.
- A flood of tokens with unknown `iss` values causes no fetch of those issuers and stays within a stated CPU bound, and refresh holds no lock during a fetch. Tokens for one slow issuer do not delay another's on a shared replica.
- Many sources across replicas, for verifier memory and the group read on the auth path, against stated thresholds. A SCIM burst stays within its rate limit, with one tenant's burst not affecting another.

### Tier 8: chaos

- Postgres loss during a decision or a mint fails closed. During the loss, the gateway refuses every bearer on the role lookup, and `lenny-ops` authorizes on token roles as §25.4 states.
- The issuer's JWKS endpoint goes down during a run, cached keys continue to verify, an unknown `kid` is refused, and past maximum staleness that source alone is refused, or every row on a shared issuer with none changing state.
- A Postgres outage leaves the registry on its last snapshot, a SCIM write answers `503`, and the group read of a source with `group_source: scim` answers `503`.
- A partial `full_revoke` reports its failures, and a retry completes it.

### Tier 9: security

- A token from the configured issuer with `roles: [platform-admin]` cannot reach any platform-only endpoint on the gateway, and cannot exchange into that authority at `/v1/oauth/token`.
- A member of an environment with denied entries cannot reach a denied host through the transparent path, derive, or delegation.
- With no sharing path, a descendant at any depth cannot obtain an object scoped outside its root's environment, or reach a host that environment denies. With one, a child and its descendants cannot obtain the caller's scoped objects or cross a second time, no pod input can set `crossed_from_environment`, and the tenant's denies hold.
- A hostile source cannot obtain another source's code through a redirect at its authorization endpoint, tested with a fake broker, and a silent login there cannot replace another tenant's session cookie.
- A hostile discovery document naming an in-cluster, loopback, or redirecting endpoint is refused for each endpoint.
- A tenant cannot name another tenant's secret, because no field names one.
- A broker token cannot reach a pinned tenant through the bare playground login.
- An invalidated `tenant-admin` with a valid IdP token cannot reach the admin API, reinstate itself, or poison ordering with a future `observedAt`, and a token with no groups cannot shed an environment's deny under `noEnvironmentPolicy: allow-all`.
- A compromised runtime cannot delegate under `maxDepth: 0` or shed its limit, environment, or mark by any tool call: naming another session as parent, creating a root session, or calling any tool outside the §9.1 table. A forged adapter call is out of scope until F-10.2.29 closes, and a test records that gap.
- A session cannot change its environment through any request field, and a body `environment` on a path naming another is overridden.
- An agent in E cannot obtain a credential scoped elsewhere through the pod credential API, a direct call to the Token Service with its own token, the mirror, or `netagent`'s git path.
- A compromised-decision test: a stubbed decision service that allows a scoped object does not produce a mint, since the Token Service's own read refuses it.
- A token with an absent, malformed, or unregistered tenant claim keeps the §10.2 refusals, and a nested tenant claim is refused.
- A customer issuer cannot reach another tenant through any claim, a platform role through any claim or row outside the operator tenant, or a `security-*` role under `securityRoleSource: managed`. A broker token cannot reach a tenant whose `deploymentIssuerAllowed` is false, including after its customer source retires. A token carrying an alias verifies and resolves as one carrying `issuer` does, a near-match of either is refused, and a draft naming an alias is refused. On a shared issuer, these are refused with one indistinguishable answer: a token with another row's pin value and this row's audience, an audience array matching two rows, an absent claim, a claim that is an array or an object, a claim differing only in case, an unsigned token naming a pin value, a token with a matching single audience and a foreign client claim, and any token for a row with no pin on a listed or marked issuer. Both playground mint modes and the impersonation mint refuse a subject of a retired or suspended source, a playground record older than `tokens_not_before` mints nothing, and one with no directory row under `require_directory_row`. The chart refuses `pinBy: audience` on Google's issuer and each refused claim name. A browser login at one source whose token selects another row is refused in both directions, on the playground and the link start page. A sign-in proof whose token differs from the draft's pin fails, and so does a `claimIsDomain` draft with no TXT record.
- A tenant admin cannot activate or widen a source, and at a tenant with a `security-admin`, a platform admin's activation waits out the delay and can be cancelled.
- A new issuer's `sub` equal to an old one's reaches no old row, because the `source_id` differs.
- Issuer URLs that resolve to excluded ranges, redirect, or rebind are refused, with the gateway and the Token Service both tested.
- A SCIM credential reaches only its own tenant and source, and nothing else in the admin API. Its value appears in no log.
- A role holding `manage_user_lifecycle` alone can call `invalidate` and `reinstate` in its own tenant, and nothing else.

### Tier 10: conformance

No runtime adapter contract changes.

### Tier 11: documentation

- Helm keys in identity pages and tutorials exist in `values.yaml`.
- Examples of scoped objects, sources, and SCIM requests validate against the schemas.

## Decisions

Settled in revision 2:

- **Decision 2, tenant-wide objects.** A session in E sees the union of tenant-wide objects and E's objects. An environment cannot exclude a tenant-wide binding, and the tenant admin scopes it instead. Limiting E to its own objects would require every shared object to list every environment, and R2 would no longer hold.
- **Decision 9, an IdP token asserting `platform-admin`.** The role is removed, the request continues, and an audit event and an alert follow. A mapping error stays quiet to the caller, and an operator using claim-based staff roles must add rows.
- **Decision 13, user rows at login.** No row is created at login, and the upsert happens at invalidation or SCIM provisioning. A customer listing users sees only those with rows.

Decided by the owner on 2026-11-03:

- **Decision 3, a transparent session when an environment policy applies.** Refused by runtime with `ENVIRONMENT_REQUIRED`, regardless of the caller's groups or `noEnvironmentPolicy`, with a resume exempt, and a `Warning` on the write that adds an environment's first `denied` or route entry, listing the runtimes it affects. A transparent client breaks when a tenant admin first adds a deny to any environment sharing the runtime. A caller outside every such environment has no path to that runtime until a tenant admin adds the member or separates the runtime, and a member of several environments can choose the environment with fewer denies.
- **Decision 26, disabling delegation.** A client can disable delegation for a session with `delegationLease.maxDepth: 0`, in the owner's words "yes, add it with maxDepth: 0". A client can only narrow, resolution becomes a minimum, `maxChildrenTotal: 0` is stored as `maxDepth: 0`, children never widen, and the gateway enforces it with `DELEGATION_DISABLED`. The cost is a changed §8.2 order, under which a preset above a Runtime default no longer wins, and a nil stored value refusing delegation.
- **Decision 14, cross-environment delegation.** Keep it as opt-in sharing with guardrails, in the owner's words "go with B with these guardrails". A crossing child runs under the target's policy unchanged, with `dangerouslyAllowCrossEnvironmentSharing` and a two-sided `dangerouslyShare`, the tenant's denies, user authorization on VCS pools with `userAuthorization: required`, no second crossing, named environments only, a `security-admin` forbid with the weakening delay and posture, and both environments on records. The accepted risk is that on a declared path, an injected caller can hand data to a target runtime, which can send it anywhere the target allows, skipping the caller's denies and proxy, and that a non-member's agent can drive the target's credentials and receive results, on any pool with no user check. The cost also includes a new session column. The alternative, a stored bound intersecting the caller's and target's sets, is retired.
- **Decision 8, identity model.** Per-tenant OIDC and a SCIM server in Lenny, with a deployer able to use a broker by default and to configure a customer's SSO when needed. This reverses revision 12's recommendation of a broker alone. The evidence for the need is vendor documentation, with no first-hand complaint found, so this is the owner's call and the priority inside it is uncertain. The cost is a new subsystem in Lenny, customer IdP support for the operator, wider token verifier egress, and a tenant boundary with two forms. The owner also removed migration between issuers from this design.
- **Decision 18, how an issuer pins.** One exact issuer pins to one tenant, unless a platform admin lists it in `identity.sharedIssuers`, in which case the pin must include the audience, an organization claim, or both, as the entry states. The owner's words: "platform admins should be able to configure a list of issuers for which pinning has to include audience/organization", because one issuer per tenant "is not a practical solution in many cases", "but we should maintain optionality because clearly unique issuers shouldn't be shared across tenants". This replaces the recommendation that such customers stay behind a broker. The cost is one key set and one availability domain across a listed issuer's tenants, a tenant boundary that rests on the provider and on the platform admin's entry, and the fields and checks under "Issuer pin and proof". Google, Okta, and Entra were read from documentation, and none has run. The owner also accepted the DNS requirement on domain values and the suspension when that record goes, and asked for exact match against several values, recorded as `issuerAliases`. The chart refuses `pinBy: audience` on Google's issuer, because a review showed any service account can obtain that audience.
- **Decision 19, where groups come from.** One of `token` or `scim` per source, never merged, with `scim` excluding introspection, in the owner's words "accept as written". A misconfigured source uses the wrong one, which posture reports, and under `group_source: scim` a directory that lags delays both grants and removals.
- **Decision 21, an issuer on a private address.** Refused in the first version, which the owner accepted. That customer stays behind a broker that can reach its IdP or exposes its endpoints. A later private route would make login depend on that route, and the path of the [outbound connector design](tenant-outbound-connector-design.md) is a candidate, with the lockout that follows when an admin cannot log in to repair it.
- **Decision 4, which objects are scoped.** Pools, bindings, user credential types and link providers, host lists, and routes. Client config files stay tenant-wide, in the owner's words "keep files tenant-wide". An environment cannot have its own `.npmrc` or `pip.conf` through these files, and it uses a tenant file, a runtime image, a setting at run time, or a dedicated derived runtime. A wrong file breaks an install and exposes nothing, because the scoped host lists and bindings are the enforcement. Writing the files at session binding needs a change to `netagent`'s write timing and a clearing step on recycling pools, and it can follow if requested.
- **Decision 5, sequentially reused pods across environments.** The residual is accepted, in the owner's words "go with your recommendation". State the scrub does not reach passes from one environment's session to another's in the tenant, as it does between users today. An environment that needs separation uses a pool with `recycle.enabled: false` or a separate tenant, and an environment pin stays additive later.
- **Decision 7, environment self-service.** TS-5 is deferred, in the owner's words "Accept the TS 7 deferral", and a `tenant-admin` writes scoped objects. Central IT handles each environment's requests. §10.2's statement that an environment `admin` gains management within the environment remains unimplemented: `RoleAdmin` has no use outside `pkg/environment`, and all environment routes need `manage_environments`. TS-5 would add a delegated authority over hosts and secrets, which needs a security review of its own.
- **Decision 1, how an object names its environments.** A list on pools and bindings, and an `environmentPolicies` section for host lists and routes, in the owner's words "go with your recommendation". It differs from §10.6's label selectors for runtimes and connectors, and each new environment needs edits on each shared scoped object. Selectors on the environment cannot express "absent means tenant-wide", and a label edit would silently widen access.
- **Decision 6, a per-environment egress quota.** None for now, with §10.6's billing rollup for cost, in the owner's words "go with your recommendations", which also covers decisions 10 and 12. One environment can exhaust the tenant's quota under [P7](egress-and-git-design.md#security-properties), the egress property that bounds egress load per pod and per tenant, and billing rollup does not cover egress bytes. A per-environment bucket in the decision service is additive later.
- **Decision 10, claim names.** `roles` and `groups` stay the defaults for the deployment issuer, `lenny_role` leaves the specification, and `auth.rolesClaim` and `auth.groupsClaim` are added. A customer source has its own `roles_claim`, `groups_claim`, and `role_map`. The cost is more Helm values and a code path reading a named claim.
- **Decision 12, reference issuers.** No broker is named as required. Selection criteria are published, and one integration runs on Keycloak in test infrastructure, as a broker in one realm and as a customer issuer in another. SCIM conformance runs on a recorded corpus, since Keycloak's SCIM provisions into Keycloak and sends nothing to Lenny, so a manual check per release carries the remainder.
- **Decision 11, what a directory deactivation does.** `scim_deactivate_mode` per source, defaulting to `full_revoke`, and the same value documented as the recommended sync worker action, in the owner's words "go with your recommendation". Sessions end on deactivation, including a mistaken one, reinstating restores neither sessions nor links, and an operator can select a weaker mode by mistake.
- **Decision 15, environments on concurrent and service pods.** These pods evaluate with E = ∅, and a session is refused when its environment has anything of its own, whether a deny, a route, an entry that adds access, or an object that lists it. The owner's words: "go with the refusal, but also refuse sessions whose environment had only entries that add access. We can't guarantee the runtime will work well without the additional access". The owner confirmed that the refusal covers an object that lists E as well as an entry, in the words "cover both". The cost is that any environment with its own policy or objects uses one-session pods through a derived runtime and its owned pool, and a service-mode runtime has no such route. No derived runtime runs a session today ([`tenant-derived-runtimes-design.md`, "Current state"](tenant-derived-runtimes-design.md#current-state), DF-3 and DF-5), so this route depends on that design, which the owner chose on 2026-11-04. A pin of each pod to one environment stays additive later.
- **Decision 16, scoping of user credentials.** `environments` on types and link providers, with a `registered` row's `environment` column honoured and links having none, in the owner's words "go with your recommendation". An admin keeps two lists straight, and one link serves a person in several environments, so a runtime holding that token under `delivery: file` has all of that person's access. A link per environment would not change that for one account. An admin who needs access to differ uses two link providers, each scoped to one environment.
- **Decision 20, MCP discovery for a customer source.** Per-tenant resource URLs and metadata documents. Clients need a tenant URL, the documents disclose a tenant identifier and an IdP host, and client behaviour is unproven. An opaque slug in place of the tenant identifier is additive later. The owner's words for decisions 20, 22, and 23 are "go with your recommendations".
- **Decision 22, maximum staleness of a customer source's keys.** 24 hours, a Helm value, past which that source's tokens are refused. An IdP metadata outage past that value stops a tenant whose IdP might still be signing, and on a shared issuer it stops every tenant on that issuer. The figure is a judgment and is untested.
- **Decision 23, SAML.** Not in Lenny. A SAML-only customer stays behind a broker on the deployment source, so the operator needs a broker for those customers, and such a customer cannot pin.
- **Decision 17, who activates an identity source.** A `platform-admin` activates with a justification and the proofs. At a tenant with a `security-admin` row in any state, activation also takes [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay, with a reachable `security-admin` approving. `identity.allowTenantActivation` lets a tenant's `security-admin` activate alone, and it is off by default. The cost is that a customer waits on the operator, with an operator action per customer IdP, and that a tenant with no `security-admin` has no delay on an operator's repointing. The owner's words for decisions 17, 24, and 25 are "go with your recommendations".
- **Decision 24, where operators' staff authenticate.** The deployment issuer stays the default, and the operator tenant can hold a customer source naming the operator's own IdP. The owner also adopted a warning: on a deployment with more than one tenant, preflight and the operator tenant's posture read report that the operator tenant accepts the deployment issuer. A single-tenant deployment gets no warning, since the issuer has no other tenant to reach. The accepted risk is that an operator who ignores the warning leaves a path from a broker compromise to platform authority. The alternative of requiring a separate issuer is retired, and so is refusing a customer source on that tenant.
- **Decision 25, a second factor for security approvals.** The first version has none, and posture reports each principal's issuer. The accepted risk is that an issuer can act as any principal it authenticates, so a tenant on the broker trusts the broker for its approvals. A tenant that needs separation from its operator moves to a customer source, which refuses broker tokens. A factor on approvals alone would leave the same forged token acting as a `tenant-admin` or a `security-admin`, its recovery would need its own delay so that an operator reset does not undo it, and an operator with database rights goes around it, as [`security-oversight-design.md`, "Residual risks"](security-oversight-design.md#residual-risks) states.

Decided by the owner on 2026-11-08:

- **Decision 27, the chart and the reserved tenant ID.** The chart refuses `auth.platformAdminTenant: platform` at render time, with a message naming the reservation of the tenant ID `platform` in [§10.2](../spec/10_gateway-internals.md#102-authentication) **`tenant_id` format validation.**, in the owner's word "ok". The operator tenant holds user rows, and the admin API and the bootstrap seed refuse to create a tenant with the ID `platform`, so an install naming it would have an operator tenant that cannot exist. The cost is one more chart validation rule.

- **Decision 28, warm-list entries under a scoped VCS pool.** Settled by the owner on 2026-11-08, with [`tenant-derived-runtimes-design.md` decision 36](tenant-derived-runtimes-design.md#decisions). The warm fill rule under "API" names every warm-list entry, of either declarer and whether it adds a checkout or is cache-only, beside tenant warm-up additions and `gitRepository` warm fills. Such an entry is refused at write unless its repository lies under no VCS pool prefix, or credential selection at E = ∅ resolves it to an unscoped VCS pool whose allowlist names it. A later VCS pool write that breaks this passes with a `Warning`, the PoolScalingController applies the same predicate at render, the entry leaves the rendered list as suspended with `environmentScoped`, the existing drain and `coveredStores` reach idle pods and claims, and the entry returns when the pool is unscoped. The cost is that a tenant cannot warm a repository that only a scoped pool covers. The alternative, refusing the VCS pool write while an entry depends on it, would block a security narrowing.

No decision in this document remains open.

## Evidence for the need

- A Claude Code issue asks to "[let users declare additional allowlisted domains at the project or session level](https://github.com/anthropics/claude-code/issues/52982)". It concerns user-owned domains in a managed sandbox, so it relates only in part to environment scoping. It was closed as not planned.
- GitHub lets organization admins [manage the Copilot agent firewall across repositories and control whether repository admins add entries](https://github.blog/changelog/2026-04-03-organization-firewall-settings-for-copilot-cloud-agent/) (vendor source). This is the closest analogue found for a layer below the organization.
- For SSO and SCIM, the evidence is vendor documentation, as listed under "What comparable systems do". No first-hand complaint was found. Decision 8 is the owner's.

## Review history

This section records what each revision changed and the reviews of earlier revisions. The current text above supersedes any statement here that differs from it. "Changes in other design documents" at its end records the edits revisions 13 to 18 made in other designs.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 5 added scoping of user credential types, and revisions 6 to 12 corrected it after review. The reviews of revisions up to 12 ended with a stop check that traced the last round's changes and found no Major or higher issue, and that check covers none of the identity design.
- Revision 13 applied the owner's decision 8, which adopts per-tenant OIDC and a SCIM server beside the broker default.
- Revision 15 applied decisions 14 and 26.
- Revision 16 applied a second review of the identity design, a first review of cross-environment sharing and delegation limits, and the owner's decision 3.
- Revision 17 applied the stop check on revision 16.
- Revision 18 applied the stop check on revision 17, under "Review of revision 17".
- Revision 19 recorded the owner's decisions 1, 4, 5, 6, 7, 10, 11, 12, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, and 25, added the warning decision 24 adopted, and added shared issuers under decision 18, with three reviews' findings applied under "Review of shared issuers". The last of them found no path across tenants, and a narrow check of its fixes found one Major on delegated children, now fixed and not re-read. Apart from those three reviews of shared issuers, no review has read the text of revisions 18 and 19.
- Revision 20 recorded the owner's choice of `tenant-derived-runtimes-design.md` for decision 15's route.
- Revision 21 applied the requests in `tenant-derived-runtimes-design.md`.
- Revision 22 removed replay as a separate route, under `session-derive-design.md`, which merges it into derive.
- Revision 23 named `oneSessionPerPod` as the route to one-session pods, under `tenant-derived-runtimes-design.md` decision 28.
- Revision 24 made a ticket optional on every platform admin action, which takes a justification instead, after `security-oversight-design.md` decision 24.
- Revision 25 changed only pointers after the user credential documents split.
- Revision 26 followed a pass for a reader new to the project. It added "Terms", "Terms from other designs and the platform", "Labels from other designs", and "High-level design", trimmed the status line into these notes, moved the review records and the record of edits in other designs to "Review history" at the end, linked every citation of another design and of its labels, replaced the egress design's interface labels with their names, named the setting behind each configuration value the text cites, and renamed the hostile tenant in the mix-up scenario to `initech`. It changed no rule.
- Revision 27, dated 2026-11-08, corrected the attribution of the provider identity claim to UC-2 of `user-credential-delivery-design.md`, the location of `serveServiceIdentity` in the gateway in the "Service identity" row and in step 3 of "Issuer pin and proof", and the "Sync worker" row, which no longer says the operator runs it, that a validation pass found, and changed no rule.
- Revision 28 applied decision 27, which the owner settled on 2026-11-08. The chart refuses `auth.platformAdminTenant: platform` at render time, with a message naming the reservation of the tenant ID `platform` in §10.2. The rule is stated under TS-2's **Operators' staff.**, in the operator tenant's "Terms" row, in the `auth.platformAdminTenant` row of "Configuration by scope", in the §17.6 entry of "Specification sections affected", in the TS-2 row of "Proposals and dependencies", and as a Tier 0 chart test.
- Revision 29 updated the `lifecycleSubjects` row in "Terms from other designs and the platform" after a re-read of `egress-and-git-design.md`, `user-credential-delivery-design.md`, `user-git-identity-design.md`, and `security-oversight-design.md` at their current revisions, and changed no rule. The row now states that a listed caller may invalidate and reinstate a security principal, as the `lifecycleSubjects` setting of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) states.
- Revision 30, dated 2026-11-08, applies decision 28, which the owner settled that day with `tenant-derived-runtimes-design.md` decisions 31 to 37 and `egress-and-git-design.md` decisions 45 to 48, and changes these rules. The warm fill rule under "API" names warm-list entries of either declarer beside tenant warm-up additions and `gitRepository` warm fills, states the predicate a write must pass, and states that a later VCS pool change passes with a `Warning` and suspends the entry at render with `environmentScoped` until the pool is unscoped. The unbound and tenant-bound formulas under "Effective policy" include the warm list's hosts, the **Repositories.** rule names the warm list's public platform entries and the readable set before a session binds, and the "Filling the mirror" row points at the write and render rules. "Terms from other designs and the platform" gains "Warm list", and "Specification sections affected", the Tier 1 tests, and "Decisions" state these rules.

### Review of revision 5

Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| "It takes `onRequest` alone" against the pool's implicit attach | Holds | `poolAttach`, defined once in `user-git-identity-design.md`. |
| The link's key | Holds | Keyed on the provider's name. |

None was rejected.

### Review of revision 6

Round 2 of the reviews raised N5, the attach trigger, and N13, the pin's reach against `visible`, on this document. Both hold, and the text above points to the attach rule and states the pin's source. None was rejected.

### Review of revision 7

Round 3 of the reviews raised H1, routes per environment against one digest, and m3, the "existing" column. Both hold. The route row points to the record's route value and states its cost, and the column text is corrected. None was rejected.

### Review of revision 8

Round 4 of the reviews raised M1, the route value's scope and order, M2, the covered set under environments, m1, the stale heading, and m21, a stale request. Each holds, and the rows point to the other designs' rules. None was rejected.

### Review of revision 9

Round 5 of the reviews raised the route assertion under `boundRoutes(E)`. It holds, and the row states it. None was rejected.

### Review of revision 13

One adversarial review reported on revision 13. Each finding was checked against the text and the tree by reading, and nothing was run. Provider and runtime statements in it stay untested.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| C1, shared callback and code theft | Holds. `Exchange` posts before validating, on one callback path. | "Browser login": per-source redirect URIs, refusal before any token request, RFC 9207, scoped cookies, and bound proof logins. The confidential broker client is a contract request. |
| H1, playground bypasses the registry | Holds. `extractSubject` reads raw claims. | One resolver for every browser login. |
| H2, `client_secret_ref` | Holds. | A write-only value the Token Service alone reads, sealed. The report's by-reference path for `platform-admin` is not adopted, so no field names a Secret. |
| M1, internal claims | Holds, today too. | The allowlist in TS-2, with `caller_type` through `service_claim`. `scope` stays dropped, with no mapping added. |
| M2, retirement | Holds. | Refusal by active source on every path, revocation, and optional `invalidateSubjects`. |
| M3, incident hold | Holds. | Holds per class in TS-4. |
| M4, `platform-admin` rows | Holds. `tenant_user_roles.go` already refuses the role, so that path needs no change. | One operator tenant. TP5 is corrected, and the residual is plain. Decision 24. |
| M5, unlisted fields | Holds. | Every change takes the rule except stated narrowings. |
| M6, pin index | Holds. | Partial index, reactivation, generic conflict, and a denied list. The shared-issuer entries are from knowledge. |
| M7, `subject_match` | Holds. | `match_claim`, declared immutable and verified, replaces `subject_match`. Administrator confirmation of the first bind is not adopted, as the smaller rule covers it. |
| M8, deployment settings | Holds. | A sentinel row, a reserved name, and the refusals. |
| M9, issuer over security roles | Holds. | Stated here and in SO, with posture. A second factor is decision 25. |
| M10, stranded principals | Holds. | The preflight refuses or lists and migrates nothing. SO's coverage alert counts a stranded row. |
| M11, login endpoints | Holds. The omitted ranges are confirmed in `egressCIDRs.excludePrivate`. | The checker covers each endpoint, with added ranges, and endpoints are captured at proof. |
| M12, CPU | Holds for the missing bound and for `MaxHeaderBytes`. The cost estimate is unmeasured. | Bounds in TS-2, background refresh, and a per-source limit. |
| M13, SCIM quotas | Holds. | Quotas, and extensions are dropped, which is smaller than capping them. |
| Minor, RLS reader | Holds. | A dedicated view. |
| Minor, group namespacing | Holds. | `source_id`, `~` refused, and unique `displayName`. |
| Minor, suspended | Holds. | A state, cleared by a `platform-admin`. |
| Minor, eviction and restart | Holds. | Idle eviction is removed, the restart case is stated, and refresh re-reads discovery. |
| Minor, disclosure | Holds. | The reason stays out of responses, the residual names the email page, and TP9 is corrected. |
| Minor, `lenny-ops` | Holds for the design's sentence, which is corrected. The report's point that the specification and code differ on IdP tokens was not checked further, because this design does not rely on it. | No path in the first version. |
| Minor, audience | Holds. | `azp` and non-empty `audiences`. |
| Minor, case-insensitive decoding | Holds by knowledge of the runtime. | Exact key, and a defect row. |
| Minor, erasure | Holds. | Addressed by SCIM `id`, and the preview holds no name. |
| Minor, SO conflict | Holds. | SO's sentence is corrected, a group write is not a lifecycle write, and `oidc` reads the roles claim directly. |
| Minor, provisioned rows | Holds. | Its own write path. |
| Minor, ongoing proof | Holds in part. | Domains are re-read and `email_domains` are unique. The sign-in proof stays one-time, as a residual. |
| The NetworkPolicy claim | Holds, and it corrects revision 13's claim. | Current state, SSRF section, and the defect row. |
| Proposal order table | Holds. | Each item lands where the table says. |

No finding was rejected outright. The parts not adopted are named above.

### Review of revision 14

A second adversarial review read the identity design at revision 14. Each finding was checked against the text and the tree by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, dropping `scope` widens narrowed tokens | Holds. `Principal.Scopes` treats absent as no limit. | Kept and filtered to `tools:` values, and §25.1 is listed. |
| N2, immediate retirement | Holds. | The delay on a `platform-admin`'s retirement, a second approver or the delay on `invalidateSubjects`, stranded rows active for approval, a ticket on reactivation, and the lost-issuer case stated in both designs. |
| N3, reactivation revives | Holds. | `tokens_not_before`, a new proof, dropped keys, the wider preflight list, and how holds clear. |
| N4, the rule covers less than its purpose | Holds. | Introspection refused with a customer source, the settings row under the rule, no writes in `pending_effective`, proof cleared, and a digest. |
| N5, unbound rows | Holds. | The check on every deployment IdP token, `match_claim` at the boundary, `lifecycleSubjects` on a login hold, the report, and the residual. |
| Minor, proof against resolver | Holds. | The proof purpose is exempt. |
| Minor, allowlist extras | Holds. | Named, by exact key, per purpose. |
| Minor, absent subject claim | Holds. | Refused. |
| Minor, preflight role | Holds. | A `security-admin` row. |
| Minor, `lenny-ops` and step 7 | Holds. `tokenservice.go` checks only the caller's origin. | Step 7 names it and each token at `/v1/oauth/token`, and a playground-origin `subject_token` is refused. |
| Minor, snapshot view | Holds. | Fields added. |
| Minor, per-source bound | Holds. | Removed, and the background refresh stays. |
| Minor, confidential broker client | Holds. | The contract sentence is dropped, which is smaller than adding a value. |
| Minor, per-tenant cookie | Holds by reading the paths, and browser behaviour is from knowledge. | Scoping and the mint move in TS-7. |
| Minor, checker on `authorization_endpoint` | Holds by knowledge of IdPs. | A query is allowed there. |
| Minor, checker on the deployment issuer | Holds. | Exempt. |
| Minor, hold classes | Holds. | Classified by `manage_users` and `security-admin`. |
| Minor, bootstrap seed | Holds. `bootstrap.go` sets `u.Disabled = p.Disabled`. | Refused. |
| Minor, customer `match_claim` | Holds. | Fixed to `subject_claim`, with `externalId` unique. |
| Minor, uncapped writes | Holds. | Upserts capped per tenant. |
| Minor, retired issuer window | Holds. | A retired row holds its issuer until deleted. |
| Minor, stale wording | Holds. | Corrected. |
| Minor, credentials from a stolen admin | Holds. | Residual, and posture lists them. |
| Minor, default `caller_type` | Holds. | `human`. |
| Minor, SO wording | Holds. | SO names the roles claim. |
| Minor, UG step 5 | Holds. | Added to UG. |
| Order, UG-1 and TS-8 | Holds. | UG-1's dependency is unconditional, and TS-8 adds its step. |

No finding was rejected.

### Review of revision 15

A first review read cross-environment sharing and the delegation limits at revision 15. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, a runtime names any session as parent | Holds today. The handler reads `in.ParentSessionID`, and `lookupCallerSession` checks the owner alone. | The calling session is the parent, with `PARENT_SESSION_MISMATCH`. Tier 9 covers a runtime's calls, and a forged adapter call stays with F-10.2.29, as a residual with no dependency. |
| H2, a runtime creates a root | Holds today. `Bridge.CallPlatformTool` dispatches any name. | The Bridge's list and call are allow-listed to §9.1's table. |
| H3, `lockEgressToHosts` | Holds. | A session under the lock cannot cross, and the risk text includes readable tokens. |
| M1, a crossed subtree keeps delegating | Holds. | Every delegation under a mark needs the flag and both names. |
| M2, the delay stops at the flag | Holds. | An added entry is a weakening, label edits are a residual, and the table names custom roles. |
| M3, depth reads Helm | Holds. `walkCap` uses `defaultMaxDepth`. | Depth from `parent.DelegationDepth`, with its own walk bound. |
| M4, `maxChildrenTotal: 0` plumbing | Holds. | Removed: stored as `maxDepth: 0`. Only `MaxDepth` gets a pointer, with `IsZero` and a test, and the extension refusal is dropped. |
| M5, `/v1/sessions/start` drops the field | Holds. | The field is added, every root creator writes a value, and nil refuses delegation. |
| M6, `delegationPolicyRef` has no effect | Holds. | Refused at create in revision 16, and revision 17 reverses that. That part of F-8.2.34 stays open. |
| Minor, check order | Holds. | Top of the handler, and again in `validateDelegation`. |
| Minor, gate read errors | Holds. | Fail closed. |
| Minor, removal while off | Holds. | Additions refused and removals allowed. Paths returning with the flag stay as a posture item, and the flag takes the delay. |
| Minor, empty selector | Holds. | Refused. |
| Minor, name references | Holds. | Both errors cover these names. |
| Minor, experiment rewrite | Holds. | A rewrite that changes crossing status or T refuses. |
| Minor, `PreRoute` on delegation | Holds. | Dropped from the delegation sentence and test. |
| Minor, what moves across | Holds. | Added. |
| Minor, "behaves the same" | Holds. | The exceptions are stated. |
| Minor, derive depth | Holds. | Stored minus source depth, and the narrowing clause is dropped. |
| Minor, parent-supplied 0 | Holds. | Removed, along with `Request.MaxDepth`. |
| Minor, wired layers | Holds. | Only Helm is wired today. |
| Minor, discovery | Holds. | From the session's E and paths. |
| Minor, default policy row and envelope field | Holds. | The row is corrected, and egress revision 53 carries the field. |
| Minor, `BUILD-GAPS.md` and `CheckDepth` | Holds. | `CheckDepth` changes, and the coordinator holds the list of stale entries. |

No finding was rejected.

### Review of revision 16

A stop check read revision 16. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| 1, invalidating the security team removes the delay | Holds. | Activation and a `deploymentIssuerAllowed` change take the delay and an objection at a tenant with a `security-admin` row in any state, in both designs. Retirement after invalidation stays immediate. |
| 2, the allow-list breaks in-pod git before EG-3 | Holds. `lenny/vcs_token` is outside §9.1's table. | The list holds it until EG-3, in both designs. The report's alternative of a dependency is not taken. |
| 3, refusing `delegationPolicyRef` breaks §27.4 | Holds. The refusal in revision 16 was wrong. | Reversed. The report's removal is not taken either: TS-3 leaves the field accepted with no effect, says so, and that part of F-8.2.34 stays open. |
| 4, the lock check misses a descendant | Holds. | Check 2 covers an inherited lock, recorded with the child's stored inputs, with a tier 1 case. |
| 5, `state` cannot hold a pending change | Holds. | `pending_version`, with `pending_effective` for first activation alone. |
| 6, deleting a source row | Holds. | Only a `retired` row, under the delay where one applies, with the dispute path stated and the scenario corrected. |
| Minor, reactivation with a reachable principal | Holds. | Approval and the delay when one exists. |
| Minor, compromised issuer cancels retirement | Holds. | Invalidation first, then retirement. |
| Minor, `invalidateSubjects` order | Holds. | Confirmation first. |
| Minor, absent `iat` | Holds. | Refused when `tokens_not_before` is set. |
| Minor, stale snapshot mint | Holds. | Residual. |
| Minor, scope near-misses | Holds. | Refused, and dropped values are counted. |
| Minor, §25.1 `caller_type: "agent"` | Holds. | `service_claim` sets it. |
| Minor, `await_children` | Holds today. | The same rule. |
| Minor, Helm depth of 0 | Holds. | Chart validation. |
| Minor, discovery under the lock | Holds. | Omitted. |
| Minor, lock check dependency | Holds. | A row. |
| Minor, SO dependency table | Holds. | A row in SO. |
| Minor, "active" in SO | Holds. | Extended to the platform admin limit. |
| Minor, settings row proofs | Holds. | They do not apply. |
| Minor, introspection residual | Holds. | Added. |
| Minor, selector widening | Holds. | The same `Warning`. |

No finding was rejected.

### Review of revision 17

A stop check read revision 17. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Major, a `retired` or `suspended` row has no write rule | Holds. | Such a row refuses writes, a reactivation with changes takes a `pending_version`, and tier 4 covers it. |
| Minor, unchanged reactivation | Holds. | A residual, and the suggested delay is not added, because retirement must stay reversible during an incident. |
| Minor, objection route | Holds. | The objection is removed from activation, because a stolen `platform-admin` can defeat it. SO's objection on assignment stays. |
| Minor, objection under attack | Holds. | A residual. |
| Minor, exchange refusal in the documentation and code | Holds. | The page's row and TS-2 name each. |
| Minor, lock across derive and replay, and the lock's field | Holds. | Both copy `egressLockHosts`, which EG and UC now name. |
| Minor, `suspended` return | Holds. | The `state` row agrees with the failure table. |
| Minor, suspension and a pending version | Holds. | Suspension cancels it. |
| Minor, pending secret and pending `deploymentIssuerAllowed` | Holds. | Where each waits is stated. |
| Minor, draft deletion | Holds. | Allowed. |
| Minor, wording of the retirement row and SO's row | Holds. | Matched. |
| Filing, `lenny/await_children` | Holds. | The parent rule names both tools. |
| Filing, F-8.3.12 | Holds. | TS-3 defines and renders the value before validating it. |

No finding was rejected.

### Review of shared issuers

An independent review read the first text of decision 18. Each finding was checked against the text, and the provider statements in H1 and M6 were confirmed against Google's documentation.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| H1, `pinBy: audience` on Google admits any service account | Holds. | The `azp` rule on every shared-issuer row, a wider assertion, and a chart refusal on Google's issuer. |
| H2, the browser resolver had no pin check | Holds. | The resolver runs step 5 and refuses a row other than the callback's source. TP11 states it. |
| M1, one member can hold a non-domain value | Holds. | Proof 4 names the value, and tenant-only activation does not apply without a domain proof. The remainder is a residual. |
| M2, return from a lapse and avoiding the check | Holds. | `tokens_not_before`, a bound on failing lookups, a return needing an existing role holder, and `require_directory_row`. |
| M3, listing an issuer locks out its tenant | Holds. | Addressed first with a grace period and an in-place pin. The second review showed that fix was unsafe, and the second table below replaces it. |
| M4, list edits and overlapping rows | Holds. | The domain flag on the row, a row-based definition, a matching rule, an overlap refusal, and a check at start. |
| M5, one address answer suspends every tenant | Holds. | An issuer-level hold that changes no row and clears itself. |
| M6, the login request cannot carry the organization | Holds. | `authorizationParams`, and the open questions under "Unproven". |
| Minors | Hold. | Key swap on reactivation, the failed-closed row's tokens and credentials, more refused claims and a known-pair warning, the overlap read path, a start check on the deployment issuer, statements that assumed one issuer per tenant, and the snapshot's fields. |

A second independent review read the result.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| H1, a failed-closed row had no stored state, so a list edit reopened it | Holds. | The stored `pin_required` state and a marker that outlasts the entry. The grace period and the in-place pin are removed, since each added a path, and a row with no pin retires instead. |
| M1, adding a pin left other organizations' subjects active | Holds. | Resolved by the same removal. `claim` to `audienceAndClaim` stays, since its subjects share one organization. |
| M2, retiring a suspended row skipped the lapse conditions | Holds. | The conditions apply to every return of a row with a domain pin. |
| M3, `subject_claim` could name a reassignable claim | Holds. | A refused list on every row, and `sub` or `subjectClaims` on a shared-issuer row. |
| M4, `/v1/playground/token` sat outside registry step 7 | Holds, confirmed in `token.go`. | Step 7 and the directory check run in both mint modes and in the resolver, and a mint can no longer refresh `iat` for a stopped subject. The reviewer's further claim carrying the original `iat` is not needed once the mint refuses. |
| M5, reactivation skipped the overlap check | Holds. | The locked check runs on every move to `active`. |
| Minors | Hold, except as stated. | Pin fields copied from the entry, claim names in the overlap rule and the index, `deploymentIssuerAllowed` refused while a source is active, the proof's comparison target, a mass miss hold, a TXT read at effect, a suspended row in place of a gateway that refuses to start, and `clientClaim`. Adding a claim to an audience row is declined, because that row can hold subjects from any organization. |

A third independent review, run as a stop check, found no path by which a token, administrator, or user of one tenant acts in another while the entry's assertion holds, and no way around the activation rule. Its one Major, which it placed at the lower end, was that removing an entry stopped Google logins, because the login parameters lived on the entry alone. They now live on the row. Its Minors hold and are applied: an exit from `issuer_conflict`, a definition of a shared issuer, the playground record and impersonation mints, the check on entry to `pending_effective`, what a retired row holds, the mass miss hold's scope and end, `deploymentIssuerAllowed` across a return, a role row that must predate the lapse, and the response to a wrong entry.

A narrow check then read the third review's fixes and decision 15. Its one Major, a delegated child on a concurrent pod losing its parent's `egressLockHosts` and `egress.allowedHosts`, predates those edits and is fixed under **Pods with no per-session policy**, with matching text in `egress-and-git-design.md` and `user-credential-delivery-design.md`. Its Minors are applied. No review has read the text after this check.

### Changes in other design documents

Revisions 13 to 18 made these edits where a sentence had become false, and changed revisions as stated.

| Document | Edit | No edit needed |
| :-- | :-- | :-- |
| `security-oversight-design.md`, revision 17 | "Who supplies" text, IdP removal under SCIM, the TS-4 and TS-2 dependency rows, decision 2's wording, the issuer's power over principals, a stranded principal, a lost issuer, a principal's groups, the `crossEnvironmentSharing` guardrail with its weakening and posture entries, adding a `dangerouslyShare` entry as a weakening, the roles claim in place of `lenny_role`, decision 19 settled, activation under a `security-admin` row in any state, and TS-3's dependency row | Decision 23 stays as written, and TS-6 applies its rule to the new record. `securityRoleSource` holds for every issuer. Security principals key on the canonical subject. |
| `user-git-identity-design.md`, revision 17 | The start page's login through the shared resolver, the `claim` binding, the §10.2 note, decision 37's cost, UG-1's unconditional dependency on TS-6, the cross-environment rule, and step 5's refusal | `allowedAccounts` reads the provider and does not change under a tenant's own issuer. `identityBinding: directory` keys on Lenny's subject, and `verifiedEmail` needs the source to emit an authoritative `email`. |
| `user-credential-delivery-design.md`, revision 14 | Its pointers, the sentence on a delegation bound, and the delegation paragraph | The user key is `(tenant_id, subject)`, and a customer source's subject is `~<source_id>~<value>`, with no sentence false. |
| `forensics-evidence-preservation-design.md`, revision 17 | Its pointers, and the withdrawal of the `security-auditor` default under SO decision 19, in the read table, the SO-1 row, and decision 5 | A session's owner is still found on its row, and no sentence on records is false. |
| `tenant-outbound-connector-design.md`, revision 17 | Its pointers | None. |
| `egress-and-git-design.md`, revision 55 | The delegated child rule and the resolver hook, which named the retired intersection and bound, the `crossedFromEnvironment` envelope field and hook, UG-1's row, and the note that TS-3's list carries `lenny/vcs_token` until EG-3 | Its Token Service rule for provider APIs is what TS-6 reuses, and it does not need to name issuers. |
