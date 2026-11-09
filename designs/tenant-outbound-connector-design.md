# Tenant outbound connector

Status: design for owner review, revision 28, 2026-11-09, after two independent reviews of earlier revisions. Nothing here is built or staged, and no production component or live provider write has run. It reads against [`egress-and-git-design.md`](egress-and-git-design.md) revision 73, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 25, [`user-git-identity-design.md`](user-git-identity-design.md) revision 33, and [`github-credential-addon-design.md`](github-credential-addon-design.md) revision 5. The owner has settled every decision, and "Settled decisions" records each one. Only the Envoy spike under "Spikes owed" has run, so every other behaviour below is a design statement, and "Spikes owed" lists what must be measured first. Statements about Lenny were checked against the tree and the specification on 2026-11-05, and statements about grpc-go against the v1.80.0 source `go.mod` names. Proposals carry the label OC-n, requirements OR-n, and security properties C-n. "Review history" at the end records how the design reached this form.

| Tag | Meaning |
| :-- | :-- |
| **(vendor)** | The vendor's own description of its product, read on the page linked beside it and not tested. |

**Evidence sources.** Statements about other products come from the sources linked beside them. A *spike* is a hands-on test of one question, run against a prototype or a *stand-in*, which is a test program that plays one component's part, such as a replica stand-in in place of an outbound gateway replica. Each spike keeps its evidence under `designs/evidence/`. *Round 5 of the egress validation* is one of the hands-on validation rounds that [`egress-and-git-design.md`, "Validation record"](egress-and-git-design.md#validation-record) records.

## Problem

Customers commonly keep git servers and package registries on private networks. In [`egress-and-git-design.md`](egress-and-git-design.md), reaching a private address needs a platform registration, in which a platform admin registers the address as a private endpoint, and a shared egress address makes a customer's firewall rule admit other tenants too. For a SaaS operator each such customer is a support ticket, and a customer may refuse any inbound firewall rule at all. A customer-run agent that dials out to the platform removes both problems.

## Scope

- The agent a customer runs in its own network.
- The in-cluster gateway the agent dials.
- Tenant self-service registration of a connector and the hosts it serves.
- Connector identity, liveness, rotation, revocation, quotas, and evidence from the gateway.

Egress policy, routing, and the existing evidence streams stay in [`egress-and-git-design.md`](egress-and-git-design.md). This design treats the hooks that document lists under ["Proposal split"](egress-and-git-design.md#proposal-split) and ["Reserved for other designs"](egress-and-git-design.md#reserved-for-other-designs) as fixed inputs, and it lists what it needs changed under ["Changes requested of egress-and-git-design.md"](#changes-requested-of-egress-and-git-designmd).

## Terms

| Term | Meaning |
| :-- | :-- |
| Outbound connector | A tenant-owned record naming one customer network. Every instance of it serves the same hosts. |
| `lenny-outbound-agent` (the agent) | The binary or image a customer runs. One running copy is an instance, with its own key. |
| Instance | One running copy of the agent, with its own Ed25519 key and instance identifier. An instance enrolls once and can be revoked alone. |
| Boot identifier | A random value an instance generates each time it starts, which its attestations carry and a grant names, so a restart invalidates earlier grants. |
| Enrollment token | A short-lived token a tenant admin issues for a connector, with an expiry and a use count. An instance presents it once to register its public key. |
| `lenny-outbound-gateway` (the outbound gateway) | The in-cluster Deployment the agent dials. `lenny-gateway` keeps the name "API gateway" here, so the two are never confused. |
| Replica | One pod of the outbound gateway. Each replica has a pod UID, which the agent learns over `CH-SITESTATUS` and names in its attestations. |
| Replica session | One TLS connection an instance dials to a replica and keeps open, carrying `CH-HOSTTUNNEL` and `CH-SITESTATUS`. It is unrelated to a Lenny session, in which an agent runtime works for a user. The agent setting `replicaSessions` sets how many an instance holds. |
| Grant | A short-lived signed statement from `lenny-egress-authz` authorizing one stream to one authority through named instances of one connector. "Grants" states its claims. The word means this statement throughout, except in "outbound grant" below and in a permission assignment to a role. |
| Outbound grant | The platform admin's enablement of the connector feature for one tenant, with its quotas, written through `PUT /v1/admin/tenants/{id}/outbound-grant`. It is unrelated to a grant on a stream. |
| Authority | The `host:port` a CONNECT names, such as `gitlab.acme.internal:443`. |
| Grant signing key and trust set | Each decision service replica signs grants with an Ed25519 key it holds in memory. The trust set is the set of current public grant keys, which agents fetch from the API gateway and replicas read from Postgres. |
| Attestation | A short-lived statement signed by an instance's own key, naming the replicas it is attached to. |
| `LNK-OUTDIAL`, `CH-HOSTTUNNEL`, `CH-SITESTATUS`, and `CH-GRANTEDCONNECT` | The proposed identifiers for this design's link and channels. `LNK-OUTDIAL` is the TLS connection the agent dials to a replica. `CH-HOSTTUNNEL` is the `Tunnel` RPC on it, one per stream, carrying one tunnel's bytes. `CH-SITESTATUS` is the long-lived `Status` RPC on it, carrying attestations, clock readings, the replica's pod UID, and the draining flag. `CH-GRANTEDCONNECT` is the CONNECT from `lenny-egress` to a replica, carrying a grant. |
| Attachment register (`REG-SITEATTACH`) | The append-only Postgres table that holds attestations, from which the decision service learns which replicas can reach which instances. |
| Outbound host | A tenant-registered endpoint whose target kind is the outbound connector. |
| Reserved set | The names a connector host may never cover, because a platform-owned credential can travel to them. "Which hosts a connector may serve" lists its members. |
| Local allowlist | The agent's `allow` setting, which lists the hosts and ports it dials. The customer network owner writes it in the agent's local file, and it starts empty. |
| Customer network owner | Whoever runs the agent and the network behind it, such as Bob in "Scenarios". The role holds no Lenny permission. |
| Exclusive gateway group | A dedicated outbound gateway Deployment, with its own public host, that serves only the tenants a platform admin assigns to it (OC-4). A replica outside any group is a shared replica. |
| Probe | A live test of the path to one host through a connector, which a tenant admin runs through the admin API or `lenny-ctl outbound probe`. |

**Agent.** "The agent" alone in this document means `lenny-outbound-agent`. The AI agent that runs in a Lenny session's pod is called the session's agent, or an agent in a pod.

**Naming.** The bare word "connector" is already bound in the specification: §9.3 Connector Definition and OAuth/OIDC, `/v1/admin/connectors` in §15.1 REST API, and `CH-MCP-CONNECTOR` in §28.3 Registers. §28.1 Naming law says that an identifier stem may not reuse a term the specification already binds to an unrelated mechanism. In the naming law, `LNK-` names a transport connection between two participants, `CH-` a typed conversation carried on one connection, and `REG-` shared state that mediates two participants with no live connection. This design therefore uses `outbound` or `site` in every identifier, API path, Helm value, metric, and route target kind. Proposed identifiers, which OC-1 settles against §28.3, are `LNK-OUTDIAL` for the agent's connection, `CH-HOSTTUNNEL` and `CH-SITESTATUS` for what it carries, `CH-GRANTEDCONNECT` for the hop from `lenny-egress`, and `REG-SITEATTACH` for the attachment register.

### Terms from other designs and the platform

This design builds on mechanisms that other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Operator, deployer, and roles | The operator is the organization that runs a Lenny deployment, such as a SaaS operator selling it to customers. The deployer is whoever installs the Helm chart and sets its values, normally the operator. The platform admin, `platform-admin`, is the Lenny role that administers the whole deployment through the admin API and Helm, and the operator's staff hold it, so this document uses "operator" for the organization and "platform admin" for the role. A tenant admin, `tenant-admin`, administers one tenant, and a `security-admin` writes one tenant's guardrails. |
| API gateway (`lenny-gateway`) | Lenny's API gateway, which serves the client and admin APIs, and here the agent API routes too. Its gateway sweeper is a periodic job that already maintains the `REG-COORDMIRROR` register, the `coordination_lease` table recording which API gateway replica coordinates each session. |
| Resolver and plan bind | At plan bind, the step of session creation before a pod is claimed, the API gateway's resolver turns each git ref the session names into a commit with a bounded `ls-refs` call through `lenny-egress`. A CONNECT attempt it makes must get a reply within 1.5 seconds. Its errors are `GIT_CLONE_REF_UNRESOLVABLE`, which is final, and `GIT_CLONE_REF_RESOLVE_TRANSIENT`, which is worth a retry. |
| Token Service | The Lenny service that stores user and provider credentials, mints short-lived tokens from them, and answers the pod credential API. |
| `netagent` | A container in every agent pod that acts as the pod's egress proxy. It holds the pod's service account token, terminates TLS for hosts that carry a credential, attaches credentials, and applies request rules. |
| Claim and bind | A claim reserves a warm pod for a session, and bind attaches the session and its bindings to that pod. A warm pod that no session has bound has no tenant. |
| Pool and Runtime | A pool keeps warm agent pods for a Runtime, the registered definition of a runtime image and its settings. "Pool" alone means this. A VCS pool, below, is a different object. |
| Tenant-owned pool | A pool whose `ownerTenantId` names one tenant, so every pod belongs to that tenant from creation. Its client proxy profile, the environment variables and file templates the controller adds to its pods, can therefore carry that tenant's values, such as `GITLAB_HOST`, through the platform admin's per-pool profile override. A tenant's own overlay can set environment variables only from the chart's allowlist of proxy variables, and only on a pool with `egressProfile: internet` that runs no `netagent` ([`egress-and-git-design.md`, "Tenant pool overlays"](egress-and-git-design.md#tenant-pool-overlays)). A shared pool serves several tenants. |
| Agent pod, adapter, and agent namespaces | A pod that runs a Lenny session's agent runtime, and the namespaces that hold such pods. The adapter is the Lenny process in the pod that speaks to the API gateway for the runtime. Platform components run in `lenny-system`, and stored credentials in the credentials namespace, `lenny-credentials`. |
| `lenny-egress` | The central egress proxy, built on Envoy, through which agent traffic leaves the cluster. Every platform-side client sends a CONNECT to it, and it asks the decision service through ext_authz, Envoy's external authorization call, whether and where to forward it. `ORIGINAL_DST` is the Envoy cluster type that dials the address the decision returns, and `custom_response` is the Envoy setting that replaces an upstream reply with a fixed one. |
| Decision service | `lenny-egress-authz`, which decides whether each connection is allowed, from the session's effective policy, and returns the route to use, with an address and a credential to inject where the route needs them. Every mint passes two independent gates, the Token Service's own scope check and the decision service's decision. A policy version, which every policy, host set, route, overlay, and binding write increments, is part of its cache key, so an edit takes effect at the next decision. |
| Egress decision API | The API on which a proxy, the git endpoint, or the Token Service asks the decision service whether a connection is allowed, in ext_authz's gRPC or HTTP form. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I3. |
| Egress policy document | The versioned document Lenny publishes for a level 1 proxy, listing each credential identity with its allowed host patterns. It defines a host grammar for the names and patterns it lists. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I4. |
| Pod credential API | The gRPC API on which `netagent`, and the git mirror, ask the Token Service for a binding's token and the CA that validates its host. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I8. |
| Git mirror, store, and store fallback | `lenny-git-mirror`, which serves git fetches from per-tenant stores. "Store" in this document means such a mirror store, and the Postgres tables this design adds are called tables. When the upstream host or `lenny-egress` fails, it can answer a ref from its store if the ref was confirmed within an age limit, 6 hours by default, and the session then reports `resolvedFromStore`. That is the store fallback. A warm fill fetches repositories into a pool's warm pods before any session claims them. |
| `pkg/binding` | The package the Token Service, the decision service, and the mirror use to resolve a pod to its session, user, tenant, and bindings. Its pod informer projects each pod's UID, labels, `podIPs`, and phase. |
| Effective policy | The hosts and bindings a session may use, which the decision service computes from pool, tenant, and session settings. [`egress-and-git-design.md`, "Effective policy"](egress-and-git-design.md#effective-policy) gives the formulas. |
| Route and route target | A tenant setting that sends traffic for matching hosts to a target: `direct`, a tenant's upstream proxy, a registered private endpoint, or an outbound connector. Egress routes carry API traffic. Git routes carry git traffic, to a git endpoint, the mirror, or `direct`. Each target has a kind, and this design's kind is `outboundConnector`. Where no route matches, the platform default route applies. A chained route is one on which `lenny-egress` forwards the CONNECT to a next hop, such as an upstream proxy, and a route to a connector is one. "Route" alone means this tenant setting. The text says "API route" for a path of the admin or agent API, and "Envoy route" for an entry in Envoy's configuration. |
| Tenant-registered endpoint | A tenant's record of a host it reaches, with a kind, a CA, and kind-specific fields, in the endpoint table of [EG-6](egress-and-git-design.md#proposal-split). A validator for each kind checks the record, and the validator for public endpoints refuses a private address. |
| Host set and platform catalog | A host set is a named list of hosts, such as a provider's API hosts. The platform catalog holds the host sets that ship with Lenny and those the deployer adds. A tenant can also define host sets of its own. |
| Pool ceiling and tenant pool overlay | A pool's ceiling is the outer bound of the hosts its sessions may reach: `*` for any host, or a named list. A tenant pool overlay is one tenant's record of changes to a pool's settings for its own pods. The per-pool flag `tenantOverride` decides how far an overlay may go: `none`, `narrow` (the default), or `narrowOrExtend`, which also lets it add hosts. |
| Opaque tunnel | A CONNECT tunnel that `netagent` forwards without terminating TLS, so it sees only the host name and port, and the tool in the pod speaks TLS to the host itself. Such a tool validates the host against the pod's trust bundle, which holds the public roots, any deployer CA, and the per-pod CA with which `netagent` terminates TLS. |
| Tunnel lifetime menu | A list of tunnel duration caps the chart renders. Each pool names one entry, and Envoy carries one route per entry. |
| Credential binding | An egress-design object that gives sessions a secret for named hosts. Its fields include `secret`, which names what is delivered, `hosts`, `delivery`, `attach`, which is the header template the credential is attached as, and `placeholder`. Under `delivery: inject`, `netagent` attaches the credential to requests and the runtime holds only a placeholder. Under `delivery: file`, the runtime reads the credential from a file. |
| Covered host | A git host that a VCS pool or a git route of the tenant names, or an alias host of that provider, as [`egress-and-git-design.md`, "Git on covered hosts"](egress-and-git-design.md#git-on-covered-hosts-netagent) defines it. `netagent` terminates TLS for it and runs git through itself. |
| Pin (`credentialPinned`) | A setting on a host set or a binding, from [EG-12](egress-and-git-design.md#proposal-split), under which `netagent` terminates TLS for the host and refuses a request when the host has no binding. A VCS pool with `userAuthorization: required` pins its hosts. "Pin" in this document means this setting. A replica the decision chooses is the selected replica, and the address the agent checks and dials is the checked address. |
| VCS pool and capability level | A version control system (VCS) pool is a credential pool whose provider serves git hosts, separate from a pool of pods. It names repository prefixes and a provider credential. The provider's capability level decides what Lenny mints: `mint-scoped`, a token limited to named repositories, as GitHub Enterprise Server offers; `mint-coarse`, a short-lived or revocable token wider than one repository, such as a GitLab project access token; or `stored`, a stored credential. Under the VCS layer of [`user-git-identity-design.md`](user-git-identity-design.md), `userAuthorization: required` makes each repository need the user's own access at the provider. |
| Writable set | The repositories a session declared writable and its pool allows. A write token is minted only for one of them. `allowedRefPatterns` limits the refs a push may update. |
| Mint and mint path | A mint is a call in which the Token Service obtains a short-lived token from a provider. The mint path is the route those calls take: through `lenny-egress` under a decision, with bounds on time, response size, and redirects. The pending revocation record is the durable record in which the Token Service retries revoking a `mint-coarse` token it could not revoke at session end. The mint cache is the Token Service's cache of minted tokens, shared across its replicas and keyed on tenant, host, App installation, repository set, and permissions. |
| Tenant assertion | The tenant that the mirror, the API gateway, or the Token Service names when it calls `lenny-egress` on its own identity, so that tenant's routes apply. The decision service honours it only from those identities and under the rules the egress design states. |
| `userCredential` binding, link provider, and token trust record | Terms of [`user-credential-delivery-design.md`](user-credential-delivery-design.md). A `userCredential` binding delivers a person's own token to that person's sessions. A user credential type is a tenant object naming a kind of per-user token, its source, and the hosts where it works. A link provider is a tenant object naming an OAuth2 provider at which a user links an account, and `kind: oauth2` is its generic kind, whose `apiHosts` are the hosts its tokens work on. A narrowing template is data on a link provider describing how to derive a per-session token from the stored one. The token trust record holds everything that decides where a stored user token can go, and any change to it makes the token unusable until the user acts again. |
| `egressProfile` | A pool setting: `restricted`, `proxied`, or `internet`. Under `proxied`, a pod reaches the internet only by CONNECT through `lenny-egress`. |
| Level 1 and level 2 | Ways a deployer can replace `lenny-egress` with its own proxy. A level 2 proxy calls the decision service for each connection, and the deployer asserts what it does through `centralProxy.level2.*` Helm values. `centralProxy.level2.pinnedDestinations: true` asserts that the proxy dials the address the decision returns and never resolves the name itself. A level 1 proxy enforces a host list that Lenny publishes and makes no call per connection. |
| Egress group | A dedicated `lenny-egress` Deployment, with its own source address, that a platform admin assigns to a tenant. |
| Egress PKI | The egress public key infrastructure (PKI): the `egress` certificate authority (CA) and the TLS leaf certificates it issues to the egress components, which [EG-11](egress-and-git-design.md#proposal-split) adds. A leaf's subject alternative name (SAN) is its component's exact Service DNS name. `lenny-egress-ca` is the CA bundle the controller writes for components to trust. A CA key rotation follows a fixed order: every bundle gains the new CA, the Issuer switches to it, and every leaf is renewed before the old CA leaves the bundles. The egress Certificates opt out of the `certManagerExpiring` remedy of `lenny-ctl doctor`, which would otherwise delete their Secrets. |
| Evidence streams and provenance | Append-only records the egress design defines, including `egress.decision` for each connection decision, `egress.tunnel` for each tunnel through Envoy, `egress.local` for refusals `netagent` makes itself, and `git.operation` for git operations. A decision identifier joins them, and a refusal returned to a client carries it. `lenny-evidence` ingests them and admits each producer by its certificate's SAN. A record's provenance is `enforced` when a component in the path produced it, and `self_reported` when it comes from inside a pod or from a component outside the platform's control. A manifest is an index row that ties a batch of evidence records to the tamper-evident chain, and the egress design sizes its evidence store by a bound on manifest rows. |
| `dataResidencyRegion` | A tenant setting naming the region its data must stay in. The mirror keeps a tenant's stores in that region, and a tenant whose region the mirror cannot serve routes `direct`. |
| Tenant egress policy | A tenant's always-allowed hosts, which every session reaches, its session-requestable hosts, which a session can ask for in `egress.allowedHosts`, and its denied hosts. |
| Guardrail | A setting a `security-admin` writes in the tenant security record ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)) that limits what a tenant admin can configure. |
| `lenny-ops` and the controller | `lenny-ops` is Lenny's operations service, whose current rights include reading Secrets in `lenny-system` directly. [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) removes that direct read, and `lenny-ops` stays inside the PKI trust boundary after it, because it can still create or change a workload that mounts a Secret. The controller is Lenny's Kubernetes controller, which the egress design places inside the egress PKI's trust boundary. |
| PgBouncer and the pooler | The connection pooler in front of Postgres. Its auth file holds the database logins it admits. |
| `lenny-ctl` | Lenny's administrative command-line tool. |
| Test tiers | The project's test levels, from tier 0, static checks, up to documentation tests, which `.claude/rules/test-coverage.md` maps. "Tests" states what each tier must establish for this design. |
| NET-047 and NET-050 | The specification's selector requirements for NetworkPolicy rules that target a `lenny-system` component, in [§13.2.4](../spec/13_security-model.md#1324-lenny-system-namespace-networkpolicies). |

### Labels from other designs

Proposals and properties in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-0, EG-1, EG-2, EG-3, EG-4, EG-6, EG-8, EG-9, EG-10, EG-11, and EG-12 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | Proposals of the egress design: EG-0 the service-mode tenant pin, EG-1 git prerequisites, EG-2 the pod network agent and proxied egress, with the decision service, typed route targets, and the mint path, EG-3 the pod git path, EG-4 credential delivery, EG-6 per-tenant routing and replacement, EG-8 tenant visibility, EG-9 egress policy objects with the single write path for tenant opt-ins, EG-10 evidence ingest, EG-11 the egress PKI, and EG-12 exfiltration controls. |
| P2, P3, P4, P5, P6, and P7 | [`egress-and-git-design.md`, "Security properties"](egress-and-git-design.md#security-properties) | Egress security properties: P2 keeps long-lived credentials out of pods, P3 keeps egress and VCS credentials unreadable by the agent in a pod, P4 keeps one tenant's data and credentials away from another tenant's pods, P5 makes every egress connection attributable, P6 makes a failure of any egress component block traffic, and P7 keeps one pod's load from removing another tenant's egress. |
| SO-2, SO-3, and SO-7 | [`security-oversight-design.md`](security-oversight-design.md) | [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) the tenant security record, which holds a tenant's guardrails, [SO-3](security-oversight-design.md#so-3-per-tenant-siem-destination) the per-tenant destination for a security information and event management (SIEM) system, and [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) the narrowing of `lenny-ops` rights. |
| UC-2 | [`user-credential-delivery-design.md`](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) | The `oauth2Link` source and account linking. |
| TS-1 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-1-environment-scoping) | Environment scoping. |
| FE-1 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#fe-1-quarantine) | Quarantine, which sets the `quarantined` binding state. The egress design's [EG-2](egress-and-git-design.md#proposal-split) adds that state to `pkg/binding` as a hook. |

## High-level design

Many customers keep git servers and package registries on private networks that the internet cannot reach. An agent working in a Lenny session needs to clone from those servers, push to them, and call their APIs. Without this design, an operator registers each private address by hand, and the customer opens an inbound firewall rule, which many customers refuse to do. This design adds a small program that the customer runs inside its own network. The program dials out to the Lenny cluster and keeps that connection open, and Lenny sends traffic for the customer's private hosts back along it. The customer opens no inbound rule, and a tenant admin sets the feature up with no operator involved.

The traffic stays encrypted end to end between the Lenny component that makes a request and the customer's host, so neither the program nor the cluster service it dials can read it. Each stream carries a short-lived permission that Lenny's egress decision service signs, which the program checks, and the program also refuses any host the customer has not listed locally. The main limit is that a compromised cluster service in the path can reuse a permission it is carrying to open a connection of its own to that one host, with no credential, and that a compromised decision service can reach every host each customer has listed.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Gateway, agent, and identity (OC-1).** The customer runs `lenny-outbound-agent`. Each instance generates its own key at enrollment and dials `lenny-outbound-gateway` over TLS on port 443. After the handshake the roles reverse: the agent serves gRPC on the connection it dialed and the replica acts as the client, so the agent cannot open a stream or ask for a dial (C4). A platform-side client, which is `netagent`, the API gateway's resolver, the mirror, or the Token Service, sends a CONNECT to `lenny-egress` as for any other host. When the route names an outbound host, the decision service picks a replica from the attestations the instances signed, takes the replica's address from its own pod informer, and signs a grant naming the tenant, the connector, the authority, and up to two instances. The replica and the agent both check the grant, the agent also checks its local allowlist and the resolved address, and both require the first bytes to be a TLS ClientHello naming the granted host (C1, C2, C3, and C6).

**Hosts and policy (OC-2).** A tenant admin registers outbound hosts for a connector and routes them with the tenant routes of the [egress design](egress-and-git-design.md). A connector host cannot cover a name on the reserved set, which is checked at registration and at each decision (C8). The pool flag `tenantOverride.outboundHosts` lets a tenant's overlay add its own connector's hosts to a named pool ceiling. The mirror serves an outbound host only when the host sets `allowMirror: true`, and a credential binding with `secret: {kind: none}` lets `netagent` validate an anonymous host on a private CA, which a tool in an opaque tunnel cannot do on a shared pool (see "Private CAs").

**Operations (OC-3).** The connector read reports liveness from signed attestations. Quotas apply per tenant, connector, instance, and replica session, the replicas write `outbound.*` evidence beside the egress design's streams, and a probe and a local check command locate a failure by stage. The chart refuses to enable the outbound gateway outside development until OC-3 lands.

**Exclusive gateway groups (OC-4).** A platform admin can assign a tenant a dedicated outbound gateway Deployment with its own public host, so a compromise of a shared replica gains that tenant no traffic.

```
 Lenny cluster                                              customer network
 netagent, resolver, mirror, or Token Service
        │ CONNECT host:443
        ▼
 lenny-egress ──ext_authz──> lenny-egress-authz
        │      <── route, replica address, and signed grant
        │ CONNECT host:443 with the grant
        ▼
 lenny-outbound-gateway replica <══ replica session, dialed by the agent on 443 ══ lenny-outbound-agent
        │ Tunnel stream on that replica session ───────────────────────────>  checks the grant and the allowlist
                                                                                  │ dials
                                                                                  ▼
                                                                             private host
 The client's TLS runs end to end to the private host through every hop.
```

## Requirements

- OR-1. A customer opens no inbound firewall rule. The agent needs outbound TCP 443 to two hostnames, the API host and the outbound gateway host, directly or through the customer's outbound HTTP CONNECT proxy.
- OR-2. A tenant admin registers a connector and its hosts without a Helm change, a platform registration per host, or an operator ticket, once the platform admin has granted the tenant the feature.
- OR-3. Every feature of the private destination path, by which pods reach a platform-registered private address through `lenny-egress` today, works on a connector route: clone, push, Git Large File Storage (LFS) on a VCS pool whose provider is `mint-scoped`, which the [egress design](egress-and-git-design.md) refuses on `mint-coarse` and `stored` providers such as GitLab and Gitea until a live GitLab run passes, the API gateway's ref resolution, the Token Service's mints, credential bindings with `delivery: inject`, `allowedRefPatterns`, and `credentialPinned`. This includes a user's link to a private host and a `userCredential` binding on one (see "A user's link to a private host" under ["Scenarios"](#scenarios)).
- OR-4. A connector for `acme` never carries `globex`'s traffic, including under a compromised outbound gateway replica. "Security properties" and "Residual risks" state how far this holds.
- OR-5. The customer limits what the agent reaches, independently of anything the platform asks.
- OR-6. The egress design's properties hold on this path: [P4](egress-and-git-design.md#security-properties) tenant isolation, [P5](egress-and-git-design.md#security-properties) attribution, [P6](egress-and-git-design.md#security-properties) fail closed, and [P7](egress-and-git-design.md#security-properties) quotas.
- OR-7. The loss of one agent instance or one outbound gateway replica ends only the tunnels it carried.
- OR-8. A tenant admin sees liveness and diagnoses a failure without the operator's help.
- OR-9. The feature has no runtime dependency on a third-party service, because Lenny deployments include self-hosted and air-gapped clusters. Its licence terms allow an operator to run it as a paid service, and the agent is distributable to customers. Lenny's Go files carry `SPDX-License-Identifier: MIT`.

## Security properties

- C1. A stream enters a customer network only under a grant that `lenny-egress-authz` signed for that tenant, that connector, that authority (the `host:port` the CONNECT names), and that instance. The agent verifies this, so the outbound gateway cannot create access.
- C2. An instance accepts a grant once. A grant names at most two instances and expires within 90 seconds at the widest clock skew, which is its 30-second lifetime plus the agent's 60-second skew allowance under "Grants", so an honest replica opens one stream with it, and a compromised replica opens at most two. An agent restart invalidates every grant issued before it.
- C3. The agent's local allowlist bounds what it dials, whatever the platform asks. It is empty until the customer fills it.
- C4. The agent initiates nothing toward the platform. It answers streams the replica opens, and it writes bounded status messages and attestations. It cannot ask the outbound gateway to dial anything, and the outbound gateway has no network path to the agent pods, the Token Service, the decision service, or the Kubernetes API.
- C5. An outbound gateway replica holds no tenant secret, no agent private key, no grant signing key, and no service account token. It holds its public certificate key, its egress PKI leaf key, and its restricted database login, whose rights "Deployment and network rules" states. None gains an attacker traffic elsewhere, because the decision takes the replica's address from its own pod informer rather than from anything a replica writes.
- C6. Traffic is TLS end to end between the platform-side client (`netagent`, the API gateway, the mirror, or the Token Service) and the customer's host. Neither the outbound gateway nor the agent holds a key that decrypts it. Both refuse a stream whose first bytes are not a TLS ClientHello naming the granted host.
- C7. A stolen agent key yields a position in the path: availability, and the metadata of `acme`'s connections. It yields no tenant credential, no reach into the cluster, and no reach to another tenant. Its holder can rotate the key, which raises an event and is caught at the honest instance's next signed API call. That call comes within 5 minutes, because the honest instance fetches the trust set every 5 minutes (see "Key reuse" under "Registration, rotation, and revocation").
- C8. A connector host cannot shadow a platform-reserved host, so a tenant cannot route a platform-owned credential through its own network. The check runs at registration and again at each decision, so a host set or pool added later takes effect at once.
- C9. Content arriving from a customer network is treated as content from a hostile upstream. The mirror's fetch sandbox, the resolver's bounds, and the [egress design](egress-and-git-design.md)'s time, response size, and redirect bounds on each mint call apply unchanged.
- C10. A connector failure blocks traffic. The decision never falls back to the platform default route for a host whose route names a connector.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Hostile customer-side agent (the tenant itself, or an attacker on the customer's host) | Refuse or black-hole its tenant's streams. Read ciphertext and the Server Name Indication (SNI) of its tenant's connections. Return hostile content on a stream, which is what a hostile git host returns. Send malformed HTTP/2 frames to a replica. Report false status. Consume its tenant's quotas. | Open a stream the replica's client will serve. Cause a dial from the cluster. Decrypt a client's TLS unless it holds a key for that host's CA. Reach another tenant. Forge `enforced` evidence. | C4 and C6. Status messages are bounded, enumerated, and stored as `self_reported`. Replicas enforce per-connection frame and memory limits and [P7](egress-and-git-design.md#security-properties) quotas. |
| Hostile tenant registering a host | Register an outbound host whose VCS pool copies another tenant's GitHub Enterprise Server App and installation identifiers, so a mint runs against the tenant's own endpoint. | Receive the other tenant's cached token. | The egress design's mint cache, the Token Service's cache of minted tokens, is keyed on tenant and host as well as installation, repositories, and permissions. This was reasoned rather than tested, and tier 9 pins it. |
| Holder of a stolen agent key | The same, from any address, until revocation. It can take a share of the tenant's streams from the honest instances, and it can rotate the key to one it chose. | Mint, read a credential, or use the key against another connector. The private key proves one instance only. | C7. `netagent`, the resolver, the mirror, and the Token Service verify the host's certificate before sending a credential. Rotation raises `instance_key_rotated`, and the honest instance's next signed API call revokes the instance. |
| Holder of a stolen enrollment token | Add an instance to that connector until the token expires or runs out of uses. | Anything beyond a stolen key's reach. | Tokens are short-lived and hashed at rest, and every enrollment raises a tenant event. |
| Compromised outbound gateway replica | Read SNI and authorities of connections through it. Drop or corrupt them. Withhold an attestation, or insert a stale one that a higher sequence number supersedes. Claim to an agent that it is another replica, which sends that replica's streams to a replica with no replica session, so they fail. Use a grant it received once per named instance in place of the pod (see "Residual risks"). Forge `outbound.*` evidence. | Create a grant or an attestation. Attract streams for an instance that is not attached to it. Remove another replica's attestations. Name a dial address. Send `globex`'s grant to `acme`'s agent, which refuses it. Read TLS contents. Change keys or routes. Reach the Token Service or the API server. | C1, C2, C5, and C6, and the database role under "Deployment and network rules". Key rotation and enrollment go to the API gateway and never pass through a replica. |
| Hostile bytes toward the agent | A compromised replica sends crafted HTTP/2 frames, gRPC messages, and grants to the agent's server. A prompt-injected agent in a pod sends crafted bytes down a tunnel to the agent's ClientHello reader. A bug in those parsers runs on the customer's network. | Reach them unauthenticated from the internet, since the agent has no listener. Exploit a valid path without a parser bug. | The agent runs non-root on a read-only root filesystem with no capabilities, sets gRPC stream, message, and header limits, and parses a grant before any other stream content. Its grant, ClientHello, and frame handling have fuzz targets under tier 9, and it keeps the customer's network behind its allowlist even after a logic bug in the replica. |
| Compromised `lenny-egress-authz` | Sign a grant for any connector, which gives TCP reach to anything a customer's allowlist admits. | Mint credentials, or pass the customer's allowlist. Keep a key valid, including one it inserted, more than 25 hours after eviction, or 5 minutes after revoke-all at an agent that reaches the API host. | This extends the bound the egress design states for a compromised decision service, and [decisions 3 and 12](#settled-decisions), authorization at the agent and grant signing key storage, record it. "Keys" and "Emergency rotation" under "Grants" state the time bounds. |
| Compromised platform, seen by the customer | Reach the hosts on the customer's allowlist, at TCP level. | Reach anything else on the customer's network. | C3. The customer can also stop the agent at any time, which ends all access. |

## Build versus adopt

### Requirements an option must meet

An option must dial out from the customer, run its server-side gateway inside the operator's cluster, carry licence terms that allow a hosted service and an MIT project to depend on it, and leave no server-side listener a hostile client controls. Isolation at the customer's end, against a grant, is a Lenny requirement that any transport could carry: a local grant-checking proxy can sit behind a client that forwards to local ports, so this check does not by itself eliminate an option.

### Options

| Option | Model | Licence | Multi-tenant gateway | Result |
| :-- | :-- | :-- | :-- | :-- |
| [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) (vendor) | `cloudflared` "initiates an outbound connection" to Cloudflare's network, on [port 7844](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/) over QUIC or HTTP/2. | [`cloudflared` is Apache 2.0](https://raw.githubusercontent.com/cloudflare/cloudflared/master/LICENSE). The network is a commercial service. | [Virtual networks](https://developers.cloudflare.com/cloudflare-one/networks/virtual-networks/) isolate routes within one account. The search found no documented model for a provider holding many customers' tunnels. | Eliminated. All traffic crosses a third party, which fails OR-9. |
| [Tailscale](https://tailscale.com/blog/multiple-tailnets-alpha) (vendor) | WireGuard mesh with a vendor coordination service. | The [client is open source](https://raw.githubusercontent.com/tailscale/tailscale/main/README.md); the licence name was not retrieved. | A tailnet per customer is described, and creating tailnets by API is in an alpha program. [Headscale](https://raw.githubusercontent.com/juanfont/headscale/main/README.md) implements "a _single_ Tailscale network" and calls itself suitable for personal use or a small open-source organisation. | Eliminated. The multi-tenant control plane is an alpha vendor service, which fails OR-9, and Headscale's scope is one network. |
| [frp](https://raw.githubusercontent.com/fatedier/frp/dev/README.md) | `frpc` forwards to local ports and registers proxies on `frps`, and a TCP proxy opens `remotePort` on the server. Authentication is a shared token or OpenID Connect (OIDC). | [Apache 2.0](https://raw.githubusercontent.com/fatedier/frp/dev/LICENSE). | `allowPorts`, per-proxy keys, and server plugins exist. A [third-party plugin](https://github.com/maybgit/fp-multiuser) adds per-user tokens at login. The README describes no multi-tenant isolation or a client attached to several servers, says it "is currently under development", and says version 2 will not be compatible with version 1. | Workable as a transport, and not chosen. A grant-checking proxy fits behind `frpc`, because CONNECT bytes pass through a TCP proxy. Lenny would still write that proxy, the tenant-to-port mapping, a plugin confining which ports a client may register, high availability across servers, and the attachment register. The server-side listeners a client requests conflict with C4, and bearer tokens cross each server. This is a judgement on fit, and a fork is unnecessary. |
| [chisel](https://raw.githubusercontent.com/jpillora/chisel/master/README.md) | TCP over HTTP, secured by SSH, with reverse forwarding through `--reverse`. | MIT. | Per-user address regular expressions in a `users.json` file, authenticated by password. | Not chosen, for the same reasons as frp: a reverse remote opens a listener on the server, and passwords are bearer secrets. |
| [inlets Uplink](https://docs.inlets.dev/uplink/create-tunnels/) (vendor) | A client dials `wss://` with a per-tunnel token and forwards to `--upstream` addresses. A `Tunnel` resource in a tenant namespace yields ClusterIP Services per port. | [Commercial](https://docs.inlets.dev/), with a [licensed server and an unlicensed client](https://inlets.dev/blog/2022/11/16/service-provider-uplinks.html). | Its documentation recommends one namespace per tenant, and tunnels "run as isolated processes", and the service reach stays inside the cluster. It suggests one server per tunnel without stating it outright. It is the closest product to the requirement. | Eliminated by the licence only: a core path of an MIT project would require a paid server. Technically it is a candidate. A tunnel per tenant also gives OC-4's exclusive model, with a grant-checking proxy behind `--upstream`. If the owner accepts a commercial dependency, this option deserves a spike before building. Its token is a bearer secret, and rotation and revocation were not found in its documents. |
| [Teleport](https://goteleport.com/docs/reference/architecture/agents/) (vendor) | Agents establish "an SSH reverse tunnel to the Proxy Service", and the proxy routes by destination. | [AGPL source](https://raw.githubusercontent.com/gravitational/teleport/master/README.md). [Community builds](https://raw.githubusercontent.com/gravitational/teleport/master/build.assets/LICENSE-community) are limited to organizations with under 100 employees and under $10M revenue, and their security exception forbids embedding in products offered to third parties. | Clusters are the unit. The page describes no tenant model. | Eliminated by licence for a hosted offering, and it is far larger than the need. |
| [Pomerium](https://www.pomerium.com/docs/capabilities/tcp) (vendor) | Tunnels TCP over HTTP CONNECT from a client helper to an upstream the proxy can route to. | [Apache 2.0](https://raw.githubusercontent.com/pomerium/pomerium/main/LICENSE). | Not established. | Eliminated. The search found no outbound agent, so the proxy needs a route to the private host, which is the problem itself. |
| [HashiCorp Boundary](https://developer.hashicorp.com/boundary/tutorials/worker-management/hcp-manage-multi-hop) (vendor) | Workers chain through reverse-proxy connections, and a private worker dials upstream. | [Business Source License (BUSL) 1.1](https://raw.githubusercontent.com/hashicorp/boundary/main/LICENSE). The grant excludes offering it to third parties "on a hosted or embedded basis" to compete with IBM's paid versions. | The search did not establish tenant isolation. The vendor documents that self-managed workers with HashiCorp Cloud Platform (HCP) need the Enterprise binary, and that Community Edition is single-hop. | Eliminated on licence risk and on the Enterprise requirement for the topology needed. |
| [Konnectivity](https://raw.githubusercontent.com/kubernetes-sigs/apiserver-network-proxy/master/README.md) | The agent dials the proxy over gRPC, and traffic is forwarded to it. Examples use mutual TLS, and [Kubernetes setup](https://kubernetes.io/docs/tasks/extend-kubernetes/setup-konnectivity/) uses a service account token. | [Apache 2.0](https://raw.githubusercontent.com/kubernetes-sigs/apiserver-network-proxy/master/LICENSE). | The README makes no multi-tenant statement. It serves one cluster's control plane, whose agents are the operator's own. | Not adopted as a product, and a source of code. A spike reads it. |
| [GitLab agent server (KAS)](https://docs.gitlab.com/administration/clusters/kas/) (vendor) | An agent dials over gRPC or WebSocket, and the server reaches it over a reverse gRPC tunnel on that connection. Replicas [track agents in Redis and route among themselves](https://git.batsense.net/realaravinth/debian-mirror-gitlab/raw/branch/buster-fasttrack/doc/development/agent/routing.md), according to an unofficial mirror of GitLab's development documents. | [MIT](https://gitlab.com/gitlab-org/cluster-integration/gitlab-agent/-/raw/master/LICENSE). | The retrieved documents state no tenant isolation model, and it depends on GitLab Rails through an internal URL, so it is not a free-standing server. | Not adopted, because of the Rails dependency. It is the closest architecture precedent, and a spike reads its tunnel module for reuse. |
| [Envoy reverse tunnels](https://www.envoyproxy.io/docs/envoy/latest/configuration/other_features/reverse_tunnel) | An initiator Envoy dials a responder Envoy, with node, cluster, and tenant identifiers. | Apache 2.0, as Envoy. | A tenant identifier exists for isolation. | Not chosen now. The page calls it "experimental and ... under active development", and it would place Envoy in customer networks. It merits a later look, as agentgateway, a proxy that the [egress design](egress-and-git-design.md)'s comparison of proxies also leaves for later, does in that design. |

### How other agent products reach private networks

- **Cursor** (vendor) [documents](https://cursor.com/docs/cloud-agent/security-network) published egress ranges, which it does not recommend as a primary control, and tunnel clients such as Tailscale userspace networking and Cloudflare Tunnel running in the agent environment. For self-hosted GitHub Enterprise Server and GitLab it [names](https://cursor.com/docs/cloud-agents/self-hosted) AWS PrivateLink or Cloudflare Tunnel. Its self-hosted workers open "a long-lived outbound HTTPS connection" and run tool calls in the customer's network. A [staff reply](https://forum.cursor.com/t/cloud-agents-corporate-network-access/164689) contrasts a tunnel that "keeps the runtime in our cloud and tunnels in" with a pool that "actually puts the runtime inside your network". In these documents the tunnel is the customer's own product, run beside the agent, and they describe no tunnel gateway that Cursor runs.
- **Snyk Broker** (vendor) is the nearest match for the customer side. The [client opens an outbound WebSocket](https://docs.snyk.io/~/changes/TzxJRMARwBuZNPcIpizY/enterprise-setup/snyk-broker/connections-with-snyk-broker), and an accept file limits requests, with [non-matching requests rejected](https://cdn.jsdelivr.net/npm/snyk-broker@5.22.12/README.npm.md). C3 follows this practice.
- **GitLab agent server** appears under "Options". Its registration of replicas in Redis is the precedent for a register of attachments here.
- **Bito** (vendor) [asks the customer to allowlist its gateway addresses](https://docs.bito.ai/bito-dev-agents/ai-code-review-agent/getting-started/install-run-using-bito-cloud/guide-for-gitlab-self-hosted). The search returned no documentation of how Devin, Codex, or Copilot's coding agent reach a private network, so this design states nothing about them.

The model of a tunnel client beside the agent in a pod does not fit Lenny. A tunnel client in the pod holds a tunnel credential within reach of the UID the session's agent runs as, which breaks [P2](egress-and-git-design.md#security-properties) and [P3](egress-and-git-design.md#security-properties). It needs outbound access that a pod with `egressProfile: proxied` lacks, since such a pod reaches the internet only by CONNECT through `lenny-egress`, and `cloudflared` wants port 7844.

### Recommendation

Build a small Lenny-owned outbound gateway and agent on grpc-go, which `go.mod` already carries at v1.80.0.

The evidence eliminates the hosted services and the licence-barred products. It does not eliminate frp as a transport, or Uplink, or the reuse of KAS or Konnectivity code. A grant check at the customer's end fits behind any of them, as a local proxy behind the transport's client, so none of them needs a fork to carry it. Revision 1 of this design stated that adding the check needed a fork, which was wrong. Between building and those options, the recommendation rests on these judgements, which are preference as much as evidence:

- Lenny writes the grant check, the attachment register, per-instance identity, quotas, liveness, and evidence in every case, so a transport saves framing and reconnection, which grpc-go already provides.
- Reversed gRPC has no server-side listener for a client to request, and per-instance keys are proofs of possession rather than bearer tokens.
- A core path of an MIT project should carry no paid dependency, and frp plans an incompatible major version.

The cost of building is that Lenny owns an internet-facing component that parses hostile input, plus a binary it ships to customers, which is why tier 9 carries fuzz targets and "Settled decisions" calls for an external review. If the owner weighs that cost above these judgements, Uplink is the option to spike first.

A customer that already publishes a host through a tunnel product on a public address uses the tenant-registered public endpoint in the egress design, and needs no connector.

## Design

### Architecture

```
 customer network                              |  cluster
                                               |
 gitlab.acme.internal <-- lenny-outbound-agent ==(TLS 1.3, client key, gRPC)==> lenny-outbound-gateway
                            local allowlist    |   LNK-OUTDIAL, agent dials        replica r1, r2, ...
                            local DNS          |   CH-HOSTTUNNEL, CH-SITESTATUS        ^
                                               |                                       | CH-GRANTEDCONNECT
 netagent, API gateway, mirror, Token Service ---- CONNECT gitlab.acme.internal:443 --> lenny-egress (Envoy)
                                               |                                       | ext_authz (route, replica
                                               |                                       v  address, grant)
                                               |                                 lenny-egress-authz
                                               |                                       ^ verifies
                                               |                                 REG-SITEATTACH <-- replicas insert
                                               |                                 agent-signed attestations
```

Grant verification happens at both ends of the tunnel. The replica checks it to refuse garbage cheaply, and the agent checks it because the replica is outside the customer's trust.

### Agent protocol and authentication

- **Transport.** The agent dials the outbound gateway's public host on TCP 443 with TLS 1.3, directly or through `HTTPS_PROXY` by CONNECT. It verifies the server name, against the system roots or a CA bundle from its enrollment response.
- **Identity.** The agent generates an Ed25519 key at enrollment and presents a self-signed client certificate carrying it. The private key never leaves the host. The replica requires a client certificate and looks up the SHA-256 of its public key in a snapshot of `outbound_agent_instance`, the table of enrolled instances, which it refreshes by delta poll each second. The snapshot holds only instances of tenants assigned to the replica's own group, or of tenants with no group on a shared replica, and the lookup runs in the TLS verification callback. Any other key is refused before an HTTP/2 byte is parsed, so `globex`'s agent cannot reach the parsers of `acme`'s dedicated replica. There is no CA for agents, so no CA key can be stolen, and revoking is a row update. A bearer token was rejected because any replica it crossed could replay it elsewhere.
- **Roles reversed after the handshake.** Once TLS completes, the agent serves gRPC on the connection it dialed, and the replica acts as the gRPC client over that connection. Under [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.txt), only a client initiates ordinary streams. C4 rests on that role and on the client's code: grpc-go v1.80.0's client looks up each incoming HEADERS frame by stream and ignores one it did not open, and its reader logs and drops frame types it does not handle, which includes `PUSH_PROMISE`. It sends no `SETTINGS_ENABLE_PUSH`, so a protocol error does not end the replica session, and the replica counts such frames against the replica session's limits. A spike confirms that grpc-go serves this arrangement.
- **Services.** `CH-HOSTTUNNEL` is a bidirectional `Tunnel` RPC per stream, with the grant in metadata, a first reply carrying an outcome class, which is one of the dial results "Failure mapping" lists, and byte frames of at most 64 KiB. `CH-SITESTATUS` is one long-lived bidirectional `Status` RPC. On it the replica sends its pod UID, a minimum version, and a draining flag. The agent sends attestations and clock readings, under the status quota in "Quotas", and it holds a change of replica sessions for 1 second so that a flapping replica session sends one attestation. Free text is absent from both, so a hostile agent has no string to place in a log or a read. A proto under `schemas/` carries the contract, with a row in §28.7 Wire-contract artifact register.
- **Liveness.** Both ends send gRPC keepalive pings at 10 seconds with a 10-second timeout: the replica as a client, and the agent through grpc-go's server parameters, so each detects a dead peer within 20 seconds. The agent's enforcement policy sets `MinTime` to 5 seconds and permits pings without streams, because grpc-go's default of 5 minutes would close the session with `too_many_pings`.
- **Intercepting proxies.** A customer egress proxy that intercepts TLS breaks the client certificate. This design refuses that path, and the outbound gateway uses a dedicated hostname so that the customer can exempt it, as "Settled decisions" states.

### Grants

A grant is `base64url(claims) "." base64url(signature)`, Ed25519-signed, with these claims:

| Claim | Meaning |
| :-- | :-- |
| `ver`, `kid` | Format version and signing key identifier |
| `tid`, `cid` | Tenant and connector |
| `ins` | One or two pairs of instance identifier and boot identifier, in preference order |
| `aut` | Authority, lower-case `host:port` |
| `did`, `sid` | The `egress.decision` identifier, and the session where the decision has one |
| `jti`, `iat`, `exp` | Unique identifier, issue time, and expiry, 30 seconds after issue |

A grant is separate from a Lenny JSON Web Token (JWT), because §10.2 Authentication requires every Lenny-issued JWT to use ES256 through `JWTSigner`, the pluggable signing interface §10.2 names, and `pkg/kms`, Lenny's key management service (KMS) package, signs through a provider call per token, which does not fit a per-CONNECT rate.

- **Issue.** The decision service caches its policy outcome, and on an allow whose route names an outbound host it signs a fresh grant on each ext_authz call, as the [egress design](egress-and-git-design.md#egress-decision-service-lenny-egress-authz)'s cache statement admits. Envoy has no ext_authz response cache (round 5 of the egress validation), so every CONNECT reaches the service. The grant travels as the injected credential, `Proxy-Authorization: Basic base64("grant:" grant)`, replacing the pod's own `Proxy-Authorization` credential, its service account token, which the service already strips on every chained route.
- **Instance binding.** `ins` names the instances the decision chose on the selected replica, from verified attestations. Each instance holds its own replay cache, so a grant valid on every instance would open a stream on each. Naming two bounds that at two, at the cost that [decision 13](#settled-decisions), grant-to-instance binding, states, and it keeps the failover of step 7 under "How a CONNECT reaches the right connector".
- **Boot binding.** An agent generates a random boot identifier each time it starts, and carries it in its attestations. A restart empties the replay cache and changes the boot identifier, so a grant from before the restart fails its `ins` check, with no reliance on clocks.
- **Keys.** "Grant signing key storage" under ["Settled decisions"](#settled-decisions) records this choice. Each decision service replica generates an Ed25519 key at start, keeps it in memory, and replaces it every 24 hours. It inserts the public part and a `notAfter` into `outbound_grant_key`, and it signs only after 3 seconds, by when replicas have seen the key through their snapshot. The table sets `created_at` itself, its check constraint keeps `notAfter` within 25 hours of it, and the decision service's role can neither set `created_at` nor update or delete a row. Agents fetch the set from the API gateway through `GET /v1/outbound-agents/trust` every 5 minutes, at enrollment, and on an unknown `kid`, at most once per 10 seconds. A replica has no path to this fetch, so a compromised replica cannot alter the set. Agents and replicas also treat a key as expiring 25 hours after they first saw it, whatever it claims, and a key past either limit fails verification, so an agent cut off from the API host fails closed within a day at the latest, with the reason `trust_stale`. A decision service rollout during the cut-off introduces an unknown `kid`, so grants can fail sooner, and an agent reports a failed fetch in its attestation as soon as it happens. Recovery needs no action: the next fetch, within 5 minutes or within 10 seconds of a grant naming an unknown `kid`, brings current keys with a fresh window, and the agent's own key is unaffected. The fetch cannot pass through the outbound gateway, because a compromised replica could then alter the set, so a customer's firewall must allow both the API host and the outbound gateway's host, and `lenny-outbound-agent check` tests both. The agent holds the set in memory, so one that starts while the API host is unreachable refuses grants until a fetch succeeds.
- **Emergency rotation.** The API gateway alone writes `outbound_grant_key_revocation`, which holds revoked `kid` values and a revoke-all time. `POST /v1/admin/outbound-grant-keys/{kid}/revoke` revokes one key, and `POST /v1/admin/outbound-grant-keys/revoke-all` revokes every key created before now. Decision service replicas holding a revoked key generate a new one within their poll, replicas drop it within theirs, and agents drop it at their next fetch. After a suspected compromise of the decision service, the operator evicts it first and then calls revoke-all, so 5 minutes bounds every stolen or inserted key at the customer's end for an agent that reaches the API host. An agent cut off from it keeps a revoked key for up to 25 hours, and a compromised replica can still deliver grants to it. Without revocation the bound is 25 hours. Restarting the decision service replaces its in-memory keys and leaves earlier rows valid, so a restart alone revokes nothing.
- **Replica check.** Signature, expiry, tenant and connector known, and authority equal to the CONNECT authority. A failure answers 407, which `custom_response` maps to 502, with the resolver's existing alert on a proxy 502, because it means a platform fault or an attack on the hop.
- **Agent check.** Signature against its trust set, `tid` and `cid` equal to its own, its instance and boot identifiers in `ins`, `iat` and `exp` within a 60-second skew, `jti` unseen, and `aut` within its local allowlist. It holds seen `jti` values until they can no longer pass the expiry check. At the default rate of 50 new streams per second the cache holds about 4,500 entries, and at capacity the agent refuses new grants.
- **Clock.** A skewed agent clock refuses grants. Attestations carry the agent's clock reading, so the liveness read reports `clock_skew` at 30 seconds of offset, before refusals begin.

### How a CONNECT reaches the right connector

1. A platform-side client sends `CONNECT gitlab.acme.internal:443` to `lenny-egress`, with its token, as on any other route.
2. `lenny-egress-authz` computes the effective policy. The first matching tenant route names an outbound host, whose connector the tenant must hold, and the reserved check under "Which hosts a connector may serve" runs. Unbound warm pods have no tenant, and the platform default route cannot name a connector, so neither can reach one.
3. It picks a replica uniformly at random from the connector's verified attestations. Load is not an input, because a replica could misreport it. It takes the replica's address from its own pod informer under the rule below, names up to two instances attested on that replica, signs a grant, and returns the Envoy route for the pool's tunnel cap, the address, and the credential.
4. Envoy dials that address over TLS, verifying the outbound gateway's egress PKI leaf, and forwards the CONNECT.
5. The replica checks the grant and opens `Tunnel` on the first named instance. The agent checks the grant, resolves the name, checks the resolved address against its `denyCIDRs` setting (see "DNS inside the customer network"), dials the checked address, and answers with an outcome class.
6. On success the replica answers 200, and bytes flow. Both ends read the first bytes, require a ClientHello whose server name equals `aut`, and close the tunnel otherwise. Encrypted Client Hello is the same documented limit as in the egress design.
7. If the first instance reports a dial failure, the replica tries the second, because instances can see different parts of a network.

If a connector has no verified attestation, or none naming a live replica, the decision denies with status 503 and the reason `no_live_agent`, so no replica sees that tenant's authority. A resolver attempt must get its CONNECT reply in 1.5 seconds, so step 5 sits inside that budget. A spike measures this over a wide-area path (see "Spikes owed").

**`REG-SITEATTACH`.** A replica's word cannot decide where a grant goes, because a compromised replica would then draw every tenant's grants. The register therefore holds attestations signed by the instances' own keys.

- **Attestation.** An instance signs its tenant, connector, instance and boot identifiers, a sequence number, a time, the pod UIDs of the replicas it holds replica sessions on, its version, its allowlist digest (and host list unless the agent setting `reportAllowlist` is off), its clock reading, whether its trust set is stale and its last fetch failed, and recent dial failure counts by class. It sends a new attestation on all its live replica sessions whenever its set of replica sessions or its trust fetch state changes, and otherwise every 60 seconds. An attestation is valid for 180 seconds.
- **Store.** Postgres through the pooler, in an append-only table with a surrogate key and no uniqueness on instance, boot, or sequence, so a junk row cannot occupy a sequence a later genuine attestation needs. A replica inserts what it receives. Its database role can neither update nor delete, and the gateway sweeper removes expired rows. A compromised replica can therefore withhold an attestation it carries, and it cannot remove one another replica inserted. Redis was not chosen because the egress design gives `lenny-egress-authz` no Redis path, and the write rate is low.
- **Verification.** The decision service polls changes each second with one query per decision service replica, because the egress design records that `LISTEN`/`NOTIFY` is unavailable through the pooler (§12.6 Interface Design). It verifies each signature against `outbound_agent_instance`, and it keeps the highest sequence per boot and the newest boot per instance. A replayed or forged row changes nothing.
- **Address.** A row carries no address. The decision resolves each attested pod UID through the informer the egress design specifies for `pkg/binding`, which projects UID, labels, `podIPs`, and phase. That package is design text and is not in the tree yet. It accepts a pod only if it exists, is `Running`, sits in the outbound gateway's namespace, is labelled `lenny.dev/component: outbound-gateway`, and carries the outbound group label matching the tenant's assignment, or no such label for a tenant without one. A compromised replica cannot name a public address, cannot draw grants for a tenant in another group, and gains nothing from holding its leaf key.
- **Failure.** Losing one replica session changes the instance's attestation, which arrives through its other replica session, and the decision sees it within about a second. An agent losing every replica session stays attested for up to 180 seconds, during which the selected replica answers 503 `no_live_agent`, the same class as an offline connector. A crashed replica leaves the informer at once. A frozen one costs Envoy connection failures until the agents' 20-second keepalive removes it from their attestations.

The `REG-SITEATTACH` table is platform-scoped: it holds every tenant's rows and is read with no tenant context, because a replica's key lookup during the TLS handshake has no tenant context yet. The egress design's service-mode pin record, the durable record that ties a service-mode pod to one tenant and that [EG-0](egress-and-git-design.md#proposal-split) adds, is platform-scoped for the same reason, because its lookup by pod UID has no tenant context.

### Per-tenant isolation at the gateway

OR-4 holds through these layers:

- **Route ownership.** The decision refuses a route naming a connector the tenant does not hold. This is the egress design's rule for upstreams and endpoints.
- **Grant binding.** `acme`'s agent refuses anything but a grant naming `acme` and its own connector. A compromised replica holding `globex`'s grant cannot pass it through `acme`'s agent, and it cannot make a grant.
- **Accounting.** Each replica keeps streams, buffers, and quota counters per tenant, and a replica session belongs to one connector for its life.
- **No shared credential.** A replica presents no credential to the agent that could carry between tenants, since each agent authenticates the replica by its public TLS name and each stream by its grant.
- **End-to-end TLS.** A replica sees no plaintext for any tenant (C6).
- **Exclusive gateway groups (OC-4).** A platform admin can assign a tenant a dedicated `lenny-outbound-gateway` Deployment with its own public host, as egress groups do for `lenny-egress`. Each Deployment knows its group from a Helm value, and its snapshot admits only that group's tenants, so another tenant's agent is refused at the handshake. The trust API route gives each agent its group's host, so a new assignment moves agents within 5 minutes. The decision's group filter keeps grants on that Deployment's replicas. A compromise of a shared replica then gains that tenant no traffic, and it reads only that tenant's instance identifiers and key hashes, because the replica's role reads all of `outbound_agent_instance` and only the snapshot filters by group. A compromise of the tenant's own replica cannot carry another tenant's traffic. It does not protect the tenant against its own dedicated replica, which keeps the residual below for that tenant's grants.

One case remains. A compromised replica holds a valid `acme` grant for the seconds between receiving it and using it. It can use that grant for its own bytes in place of the pod's, on each named instance once, which gives TCP-level reach to that one host and port, with no credential, and only for CONNECTs `acme`'s pods make. On a shared replica it could carry another tenant's bytes on that stream, so OR-4 holds fully only on exclusive groups, and on shared replicas it holds except for this case. Neither the agent nor the customer's host can tell, because binding the inner bytes to the grant would need `netagent`, the resolver, the mirror, and the Token Service to speak a second protocol to the agent. "Residual risks" and [decision 2](#settled-decisions), isolation on shared replicas, record this.

### Which hosts a connector may serve

A host is reachable through a connector only when every party below agrees.

| Party | Decides | Where | Enforced by |
| :-- | :-- | :-- | :-- |
| Platform admin | Whether the tenant may have connectors, its quotas, the reserved host list, and each pool's flag | Admin API and Helm | API validators and the decision service |
| Tenant admin | Which hosts route to which connector, and which of those the tenant's policy allows | Admin API | The decision service |
| Customer network owner | What the agent dials | The agent's local file | The agent |

**Validator for the `outboundConnector` kind.** It replaces the public-address check, per the hook for typed route targets in [EG-2](egress-and-git-design.md#proposal-split). It accepts a lower-case DNS name or `*.suffix`, with ports defaulting to `[443]`. It refuses:

- IP literals, and any name the host grammar of the egress policy document refuses.
- `localhost`, cluster suffixes, and the metadata names in the chart's list.
- Single-label names, unless the platform admin allows them.
- A pattern wider than a registrable suffix, such as `*.com`.
- Any name on the reserved set: every host set in the platform catalog, whether shipped or added by the deployer, every provider's declared mint endpoint, any LLM host a pool names, the outbound gateway's public hosts (`outboundGateway.publicHost` and the public host of each exclusive gateway group under OC-4), and any name in the Helm value `outboundGateway.reservedHosts`. A host set a tenant defines for itself does not count, so a tenant neither reserves names nor escapes the check by defining one.

**At each decision.** The decision service applies the same reserved set when it evaluates a route, in line with the egress design's rule for egress routes. A registration check alone would leave a name shadowable once a host set or pool that names it arrives later. An existing route that now overlaps fails closed with the reason `reserved_host`, the effective-policy read names the set that reserved it, and the tenant receives an event. This keeps a tenant's connector from receiving traffic bound for a host where a platform-owned credential could travel (C8).

No proof of domain control applies. A route affects only the tenant's own pods, so two tenants can both claim `gitlab.internal`, and a claim to a public name in the customer's own domain, such as a split-horizon `git.acme.com`, is ordinary.

**Pool ceilings.** The formulas in the egress design's ["Effective policy"](egress-and-git-design.md#effective-policy) read a ceiling with the tenant's pool overlay applied. On a shared pool with a named ceiling, an outbound host falls outside it, and `narrowOrExtend` would let the tenant extend to any public host as well. This design adds an overlay flag, `tenantOverride.outboundHosts`, which lets the overlay add only hosts routed to the tenant's own connector. It takes `deny`, the default, or `allow`, which lets the overlay add hosts routed to the tenant's own connector. The flag only adds hosts, so it has no narrowing value, and a pool admits no connector host beyond its ceiling unless the platform admin sets `allow`. A ceiling of `*` matches these names as it matches any name, and the egress design's reasoning about server-side request forgery (SSRF) does not apply to them because they are never resolved on the platform side. The tenant's policy must still allow the host, and the effective-policy read names which layer refused.

**TLS only.** Both ends enforce C6, so a plain-HTTP internal host must be fronted with TLS by the customer. Non-TLS protocols and SSH are absent from this design, and the egress design's `sshForms` rewrite, which renders git `insteadOf` rules for each covered host, already turns SSH remotes into smart HTTP.

**Private CAs.** The outbound host record carries `caBundle`. `netagent`, the mirror, the resolver, and the Token Service use it for that host alone, on the existing pod credential API path. Tools in an opaque tunnel use the pod's trust bundle, so a private-CA host works on a shared pool only for traffic `netagent` terminates. An anonymous registry on a private CA therefore uses a credential binding with `secret: {kind: none}`, which makes `netagent` terminate TLS and validate with the host's `caBundle`, and attaches nothing. OC-2 owns it, and it depends on [EG-4](egress-and-git-design.md#proposal-split), the egress design's credential delivery proposal. The binding allows `delivery: inject` alone and refuses `attach` and `placeholder`, so it can hold no literal credential. Its hosts must each route to an outbound host, checked at write and at bind, so a tenant's CA never applies to a `direct` route. It is refused on a covered host and on any host with a pin, from a VCS pool with `userAuthorization: required`, a host set, or a binding's `credentialPinned`, at the write of either object and at bind, reading VCS pools unfiltered as the pin does. A pin refuses a request to a host with no binding, and a binding with `secret: {kind: none}` would otherwise admit anonymous requests there. The `secret` field of [EG-4](egress-and-git-design.md#proposal-split) is a typed union whose reader treats an unknown kind as absent, so this changes no stored format, and a `netagent` image that predates the kind leaves the host an opaque tunnel, which fails closed. Tools pass through the same bundle as git, a client that pins certificates still fails, and a tenant-owned pool whose overlay adds the CA to the pod's trust bundle through `tenantOverride.caBundle` stays the fallback. Whether `delivery: inject` assumes a credential is present, and large downloads through `netagent`, are unmeasured.

### Registration, rotation, and revocation

**API.** All API routes follow §15.1 REST API conventions (`If-Match`, `dryRun` with no outbound calls, and tenant scoping by row-level security). The write path is the single write path for tenant opt-ins that [EG-9](egress-and-git-design.md#proposal-split) adds, so [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s guardrails and audit apply.

| Route | Caller | Purpose |
| :-- | :-- | :-- |
| `POST`, `GET`, `DELETE /v1/admin/outbound-connectors[/{name}]` | Tenant admin | Create, read with liveness, and delete with revocation |
| `POST /v1/admin/outbound-connectors/{name}/enrollment-tokens` | Tenant admin | Issue a token, returned once, with an expiry of 1 hour by default and a use count of 1 by default |
| `GET`, `DELETE /v1/admin/outbound-connectors/{name}/instances[/{id}]` | Tenant admin | List and revoke instances |
| `POST`, `GET`, `DELETE /v1/admin/outbound-hosts[/{name}]` | Tenant admin | Register a host for a connector, with ports and `caBundle` |
| `POST /v1/admin/outbound-connectors/{name}/probe` | Tenant admin | Live path test, limited to 10 per minute per connector, like `/v1/admin/connectors/{name}/test`. It takes a host and an optional instance, and it reports per instance. |
| `PUT /v1/admin/tenants/{id}/outbound-grant` | Platform admin | Grant the feature to the tenant (the outbound grant), and set quotas |
| `POST /v1/outbound-agents/enroll` | Agent, with a token | Register a public key, and receive the connector identity, the host names, and the key set |
| `POST /v1/outbound-agents/rotate` | Agent, signing the request with its current key | Register a new public key |
| `POST /v1/outbound-agents/retire` | Agent, signing the request with its current key | Remove itself on scale-down, freeing its quota |
| `GET /v1/outbound-agents/trust` | Agent, with a signed request | Fetch the grant key set |
| `POST /v1/admin/outbound-grant-keys/{kid}/revoke`, `.../revoke-all` | Platform admin | Revoke one grant signing key, or every current one |

The agent API routes run on `lenny-gateway` behind the existing Ingress, which terminates TLS, so they authenticate by a signature inside the request over the method, the path, a SHA-256 of the body, the instance identifier, a timestamp, and a nonce, and the API gateway remembers nonces for the timestamp's 5-minute window. Because they use the API host, a customer proxy may inspect them. Permissions are named: `outboundConnector.manage` and `outboundConnector.read`, held by `tenant-admin` by default, so giving a new role these permissions needs no handler change.

**Credential lifecycle.**

- **Enrollment.** Each instance enrolls with its own key, so one instance can be revoked and attribution names one instance. A token with a use count above 1 serves an autoscaled Deployment, and it is capped by the instance quota. The token is hashed at rest, and each use raises `outbound_connector.instance_enrolled`.
- **Rotation.** The agent rotates its key at 30 days, overlapping for 1 hour on replica sessions, and each rotation raises `outbound_connector.instance_key_rotated`. It stores the new key before it calls `rotate`, and it retries with the same key, so a crash does not leave it holding only a superseded key. A retry is valid only when the named key is the signing key's immediate successor and is current. It changes no state and raises no event, and a `rotate` naming any other registered key counts as reuse. An agent unsure of an outcome signs its next call with the new key, an unknown key is no reuse, and it retries with the old key only after that answer. Signed calls are serialized during a rotation, so a lost response does not revoke a healthy instance. A key older than 90 days is refused, and the instance must re-enroll. An agent container therefore needs persistent storage for its key, and the agent's Helm chart renders a StatefulSet.
- **Key reuse.** A thief can rotate a stolen key to one it chose. Replicas hold no write right and have no route to the API gateway, so the check runs in the API gateway, on every signed agent API route. A signature from a superseded key revokes the instance and all its keys and raises `outbound_connector.instance_key_reuse`. The honest instance signs a `trust` fetch every 5 minutes, so 5 minutes bounds detection for an instance that reaches the API host, and a thief cannot suppress that call through a replica, since it does not pass through one. A thief who also controls the customer's firewall can block the API host, and only the rotation event then reports it. A replica refuses the superseded key once the overlap ends and takes no other action. Revoking an instance by identifier covers every key it has held.
- **Revocation.** Revoking an instance or a connector updates its row and increments the policy version, which invalidates every cached decision, so the next decision refuses at once and stops accepting its attestations. Each replica sees the row within its poll and closes the revoked instances' replica sessions, which ends their open tunnels. The target is 5 seconds end to end, which tier 4 pins. Removing the tenant's outbound grant, suspending the tenant, and tenant deletion under §12.8 Compliance Interfaces have the same effect.
- **Reaping.** An instance with no attestation for the connector's `instanceReapDays` (30 by default), or whose key has passed the 90-day limit, is deleted by the gateway sweeper with an event, and it stops counting against the instance quota. `retire` does the same at once, so an autoscaled Deployment does not fill its quota through normal churn.
- **Stale snapshot.** A replica whose snapshot is over 60 seconds old refuses new replica sessions, and over 5 minutes old closes all replica sessions, so a Postgres outage bounds how long a revoked key stays attached.
- **Duplicate use.** The gateway sweeper raises `outbound_connector.duplicate_instance` when attestations of two boot identifiers for one instance keep arriving together, because a restarted agent's old boot stops. It does not block, and it reports no source address, since attestations carry none. A holder of a stolen key that sends no attestations receives no grants, and the replica session quota bounds it.
- **Customer control.** Stopping the agent or blocking its outbound rule ends everything, with no platform involvement.

### High availability

- **Agent.** The documentation asks for at least two instances. Each holds replica sessions to two distinct replicas by default (the agent setting `replicaSessions`). It learns a replica's pod UID from `Status`, and it reconnects with jitter until it has distinct ones or has reached a try limit, because a load balancer gives no choice of backend. Attaching to every replica is unnecessary because the decision selects a replica an instance has attested, so replicas do not forward to each other.
- **Outbound gateway.** Three replicas by default, a PodDisruptionBudget, and required anti-affinity against the Token Service, the decision service, and the mirror, on the egress design's reasoning for a component that parses hostile input, relaxed under the chart's development setting `global.devMode`.
- **Replica drain.** The replica is the HTTP/2 client, and grpc-go v1.80.0's server ignores a client's `GOAWAY` (a `TODO` in `http2_server.go`), so `GOAWAY` cannot signal this. The replica sets the draining flag on `Status` and fails readiness. Each agent attaches elsewhere and sends a new attestation omitting the replica, which removes it from selection within about a second, and tunnels finish until the drain budget ends. An agent that ignores the flag loses its replica session when the replica exits.
- **Agent draining.** On `SIGTERM` an instance reports draining, takes no new streams, and lets open tunnels finish for its `drainSeconds` setting.
- **What breaks.** A crash ends its tunnels, as an Envoy crash does in the egress design. The mirror's and the resolver's retry rules apply. A `git` run in a pod fails, and the session's agent retries it or reports it.

### Liveness surfaced to the tenant

`GET /v1/admin/outbound-connectors/{name}` returns a computed state with reasons, and each instance's version, last attestation time, replica sessions, clock offset, allowlist digest, and recent dial failure counts by class, all from signed attestations, plus its source address from `outbound.session`.

| State | Meaning |
| :-- | :-- |
| `pending` | No instance has enrolled. |
| `connected` | At least two instances each have live replica sessions, and no reason below applies. |
| `degraded` | One instance, one replica session, a clock skew at or above 30 seconds, a version below the recommended one, a host in the routes that the agent's reported allowlist omits, a failed trust fetch, `trust_stale`, or a dial failure rate above a threshold. |
| `offline` | No fresh attestation for 120 seconds, which keeps an agent restart from notifying. |
| `revoked` | Revoked by a tenant or platform admin. |

The gateway sweeper, which already runs for `REG-COORDMIRROR`, computes transitions and emits `outbound_connector.offline`, `.restored`, and `.degraded` on §25.5 Operational Event Stream, which is the tenant subscription path the egress design names. The allowlist drift check needs the host list, so the agent reports it by default, and `reportAllowlist: false` limits it to a digest for a customer that prefers not to share internal names.

Operator alerts cover the shared service: replica availability, grant verification failures, capacity, and handshake rate. A tenant's connector going offline raises no operator alert, because the customer owns that agent, and the resolver's existing proxy 502 alert does not fire for it because the failure mapping below uses 503.

### Quotas (P7)

Values are initial defaults, which the load tier confirms.

| Quota | Scope | Default | Enforced by | At the limit |
| :-- | :-- | :-- | :-- | :-- |
| Handshakes per second, before authentication | Source address and replica | 5 and 200 | Replica | Connection refused |
| Connectors | Tenant | 5 | API | 409 |
| Instances, excluding retired and reaped ones | Connector | 10 | API | 409 |
| Replica sessions | Instance | 4 | Replica | Replica session refused, with a retry hint |
| New streams per second | Connector | 50 | Decision service, with its other rates | CONNECT 429 |
| Concurrent streams | Connector | 256 | Replica | CONNECT 429 |
| Throughput | Tenant, per replica | Chart value | Replica | Flow control window held |
| HTTP/2 limits per replica session | Replica session | Header size, frame rate, and buffered bytes | Replica | Replica session closed |
| Status messages, including attestations | Replica session | A burst of 10, refilled at one per 10 seconds, each at most 64 KiB | Replica | Replica session closed |
| Probes | Connector | 10 per minute | API gateway | 429 |

Per-replica limits divide a tenant's share by live replicas, so a total limit stays approximate during a rollout. The chart refuses a configuration whose per-tenant sums exceed a replica's connection and memory limits, as it does for Envoy's breakers. Idle tunnels fall under the pool's tunnel lifetime menu and `stream_idle_timeout`. The mirror's fetches and mints count against the same connector quotas, so a tenant's prefetch cannot harm another tenant, only itself. Bytes per tenant are metered, because the operator pays for this traffic in both directions.

### Evidence from the gateway

These are new streams on the egress design's evidence envelope, the fields every evidence record carries, and they need no change to any existing stream.

| Stream | Producer | Provenance | Payload |
| :-- | :-- | :-- | :-- |
| `outbound.session` | Replica | `enforced` | Connector, instance, replica, source address, key hash, open and close, close reason, and enrollment or rotation references |
| `outbound.stream` | Replica | `enforced` | Decision identifier, grant `jti`, connector, instance, authority, start, end, bytes in each direction, and outcome |
| `outbound.agent` | Replica, relaying the agent | `self_reported` | Decision identifier, dial class, duration, resolved address, and agent version |

`did` in the grant joins `outbound.stream` to `egress.decision` and `egress.tunnel`, whose upstream address here is the replica. The agent's claims are in their own stream, so a consumer knows what only the customer's agent asserts. `lenny-evidence` admits the outbound gateway's SAN for these three streams alone. The agent writes a local JSON log of every grant it accepts or refuses, with `did`, `sid`, `jti`, authority, resolved address, outcome, and bytes in each direction. A customer's security team therefore has a record outside the platform's control and can compare it with the export. A compromised replica can forge `outbound.*` records, which is the same stated limit as a compromised mirror's `git.operation` records. Agent identity changes and administrative writes go to §11.7 Audit Logging, and §16 retention presets apply per stream. The egress design bounds its evidence manifest rows, the index rows that tie each batch of records to the tamper-evident chain, for its own streams, so sizing adds these streams for tenants that hold a connector.

### Failure mapping

The replica chooses its answer so that the egress design's resolver and mirror rules classify each failure correctly, with no change to them.

| Condition | CONNECT reply | Resolver | Mirror store fallback | Reported as |
| :-- | :-- | :-- | :-- | :-- |
| No attested live replica | 503 from the decision | `GIT_CLONE_REF_RESOLVE_TRANSIENT` | Yes | `no_live_agent` |
| Agent lost all replica sessions, attestation still valid | 503 from the replica | Transient | Yes | `no_live_agent` |
| Host on the reserved set | 403 from the decision | Unresolvable, `egress_denied` | No | `reserved_host` |
| Outbound connector or outbound host suspended, such as by the `tenantRegisteredServices` guardrail of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), which sets `suspendedBy` | 403 from the decision, because a route to a suspended registration denies and carries no traffic to the customer's network | Unresolvable, `egress_denied` | No | The effective-policy read, which names the layer that suspended it |
| Envoy cannot reach the selected replica | 503 from Envoy | Transient | Yes | Envoy access log |
| Agent dial failed: DNS, refused, or timeout | 503 | Transient | Yes | `dns_*`, `tcp_refused`, or `tcp_timeout` |
| Agent refused a resolved address under `denyCIDRs` | 403 | `GIT_CLONE_REF_UNRESOLVABLE`, `egress_denied` | No | `address_denied` |
| Host absent from the agent's allowlist | 403 | Unresolvable, `egress_denied` | No | `not_in_local_allowlist` |
| Agent refused the grant for clock skew, an unknown key, or a stale boot | 503 | Transient | Yes | `grant_refused_by_agent` |
| Replica refused the grant | 407, mapped to 502 | Transient, with the alert | No | `grant_refused_by_gateway` |
| Quota | 429 | Transient | No | `quota_*` |
| Certificate failure after 200 | TLS failure in the client | Transient, under a request to the egress design, because its resolver error list omits this case (see "Changes requested of egress-and-git-design.md") | Yes, under its "failed connection or TLS handshake" cause | `egress.local` and the client's error |

The bodies are fixed and carry no agent-supplied content. The decision's own 503 keeps its body and reason header only in ext_authz gRPC mode, which `lenny-egress` uses. In HTTP mode Envoy treats a 5xx from the decision service as an authorization failure and replies with an empty 503, on 1.40.2 and 1.39.6, so a level 2 proxy calling the egress decision API in its HTTP form reports `no_live_agent` only in its own records, and the decision identifier reaches the client only in gRPC mode. That result holds under `failure_mode_allow: false`, which the egress design's level 2 suite requires. The decision identifier travels in the existing refusal, so a session's agent or a user who sees a failure has a value to give the tenant admin.

### What happens to the git mirror

The egress design's routing rule settles most cases, because a `mint-coarse` or `stored` provider is never served through the mirror. A self-managed GitLab, Gitea, Forgejo, or generic host behind a connector therefore always routes `direct` through `lenny-egress` and the connector, and the mirror refuses it.

That leaves GitHub Enterprise Server, which is `mint-scoped`. The mirror can fetch it through a tenant route, into a store keyed by the tenant, under the existing rule that any tenant route gets a tenant-keyed store. The recommendation is that it stays off until the tenant asks: a git route naming the mirror is refused for an outbound host unless the outbound host record sets `allowMirror: true`, because the customer chose a connector to keep its code on its own network, and the mirror holds fetched contents centrally. With the opt-in, the store follows the tenant's `dataResidencyRegion`, the region its data must stay in, and §12.8 purge, its fetches count against the connector's quotas, and an offline connector takes the store fallback within its age limit.

Consequences without the mirror: a tenant-owned pool's warm fill comes from the pod through the connector, each pod fetch crosses the customer's link, and an offline connector stops clones.

### DNS inside the customer network

The agent resolves names on the customer's network. The platform never sees or resolves them, and it never reads the customer's zone. By default the agent uses its host's resolver, so split-horizon and search-domain behaviour match its host. The agent setting `dns.servers` overrides the servers, and its `hosts` setting maps a name to a fixed address for a host DNS does not cover. After resolving, the agent checks each address against its `denyCIDRs` setting, which defaults to loopback, link-local (including `169.254.169.254` and `fd00:ec2::254`), and the agent host's own addresses, and dials the address it checked, so a rebinding answer cannot change the target. Private ranges are allowed, since reaching them is the agent's purpose.

The customer owns name resolution, routing, and the host's certificate, and Lenny owns reporting which of them failed. Instances can answer differently, so a probe and the dial failure counts report per instance.

### How a tenant admin debugs a failure

The refusal the user saw carries a decision identifier. From it, or from a host name:

1. `lenny-ctl outbound status acme-dc1` reads the liveness state and reasons.
2. `lenny-ctl outbound probe acme-dc1 gitlab.acme.internal:443` runs the path live and reports each stage, once per attested instance unless `--instance` names one. The API gateway sends the probe through `lenny-egress` on its own identity with the tenant asserted, as the resolver does, and the grant names that instance alone, so the replica does not fail over. The API gateway then performs a TLS handshake against the host's CA with no credential.
3. `GET /v1/egress/decisions?host=gitlab.acme.internal` and the effective-policy read show policy outcomes and which layer decided.
4. On the customer's host, `lenny-outbound-agent check gitlab.acme.internal:443` runs the local stages with no platform involved, and the agent's log has the `did`.

| Stage | Failure | Who fixes it |
| :-- | :-- | :-- |
| Effective policy | Host outside the ceiling, tenant policy, or session list | Tenant admin, or the platform admin for the pool flag |
| Route | No outbound host for the name, or a route to `direct` | Tenant admin |
| Attachment | `no_live_agent` | Whoever runs the agent. The status read shows whether it ever attached, and its source address. |
| Grant | `grant_refused_by_agent` with `clock_skew` or `trust_stale` | Whoever runs the agent's host |
| Reserved host | `reserved_host`, naming the host set | Tenant admin, or the platform admin if the host set is wrong |
| Local allowlist | `not_in_local_allowlist` | Customer network owner |
| Name | `dns_nxdomain` or `dns_timeout`, per instance | Customer network owner |
| Connection | `tcp_refused`, `tcp_timeout`, or `address_denied` | Customer network owner |
| Certificate | Chain or name failure, with the issuer and names presented | Tenant admin for `caBundle`, or the host's owner |
| Server name | ClientHello name differs from the authority | The client's owner; a tool sending another name is refused by design |

The probe records `egress.decision` like any other CONNECT. It needs the decision service to honour the API gateway's tenant assertion for an outbound host that is not a git host, and an instance named by the same identity, which is additive with OC-3.

### Deployment and network rules

- **Disabled by default.** `outboundGateway.enabled: false` renders nothing, and the validators refuse the `outboundConnector` kind. Until OC-3 lands, the chart refuses `true` outside `global.devMode`, so no public surface runs without tenant quotas and evidence.
- **Public exposure.** A dedicated hostname on a Layer 4 `LoadBalancer` Service. The existing Ingress terminates TLS, and it cannot pass a client certificate, so it does not serve this path. The chart has no `LoadBalancer` Service today, so this is a new kind of exposure that `docs/operator-guide/ingress-and-tls.md` covers. A deployer-supplied Secret holds the public certificate, as for the Ingress, and an egress PKI leaf serves the internal CONNECT listener.
- **Placement.** Its own namespace, `outboundGateway.namespace`, `lenny-outbound` by default. The chart refuses the release, agent, credentials, or mirror namespace as that value, as `credentials-namespace.yaml` does for its own, because that would silently undo the move of the outbound gateway out of `lenny-system` into its own namespace. The namespace carries the Pod Security Admission label `pod-security.kubernetes.io/enforce: restricted` and a default-deny policy, which are new work, since the agent namespaces carry only the `warn` and `audit` Pod Security Admission labels and the credentials namespace runs no pods. `lenny-ops`'s Role is scoped to the release namespace, so it gains no Secret, Job, or Certificate right here. NET-047 and NET-050 name `lenny-system` components today, and their normative text and audit extend to this label. Pods keep `lenny.dev/component: outbound-gateway` per NET-047, `automountServiceAccountToken: false`, and the placement rule that keeps it off nodes tainted for agent isolation.
- **Network rows in §13.2.4 for the new namespace, with matching rows in `lenny-system`, each naming a namespace selector and a component label as NET-047 requires.** Egress: PgBouncer, `lenny-evidence`, and `kube-system` CoreDNS. Ingress: the agent port from an external `ipBlock`, the CONNECT port from `lenny-egress` pods, egress groups, and at level 2 the `centralProxy` address, and the metrics port from the scrape source. `lenny-egress` and PgBouncer gain the matching rows. The outbound gateway has no rule toward the Kubernetes API, agent namespaces, the Token Service, or the decision service, which is what C4 and C5 rest on.
- **Store access.** The replica's role reads `outbound_agent_instance` and `outbound_grant_key`, and inserts into `REG-SITEATTACH`. The decision service's role for this feature reads those and `outbound_grant_key_revocation`, and inserts into `outbound_grant_key`. Neither holds an update or delete right. These roles are new deployment work, because PgBouncer's auth file holds the one user `DATABASE_URL` names today (`pgbouncer-deployment.yaml`). OC-1 adds these users to the chart, with their own Secrets, and a deployment on a provider's proxy supplies them. Every property above that names a role depends on this, so preflight checks with each login that the forbidden writes fail.
- **Internal certificate.** A namespaced Issuer cannot issue into another namespace, and the egress CA's key must not move next to an internet-facing parser. The `Certificate` therefore stays in `lenny-system` under the egress PKI's Issuer, which [EG-11](egress-and-git-design.md#proposal-split) adds, with its exact SAN, and the controller copies the issued `tls.crt`, `tls.key`, and `ca.crt` into a Secret in the outbound gateway's namespace, on issue and on each renewal. The replica therefore holds an ordinary egress PKI leaf. Envoy's trust, the egress PKI's CA rotation order, the opt-out from the `certManagerExpiring` remedy of `lenny-ctl doctor`, and `lenny-evidence`'s SAN allowlist apply unchanged, and no CA key or trust root is added. The controller writes `lenny-egress-ca` to this namespace too, so a replica can verify `lenny-evidence`. Kubernetes cannot limit `create` by `resourceNames`, so the chart renders the copy as an empty Secret, and the controller gets `get`, `list`, and `watch` on the source and `get` and `update` on the copy, each by name, with a `configmaps` row for this namespace. All of this is new work, since `controller-rbac.yaml` has no such rule and no `lenny-egress-ca` code exists yet. The controller is already inside this PKI's trust boundary, as the egress design states. A controller outage leaves the current leaf in place, and the replica reloads the mount with no restart. The replica exports its loaded leaf's `notAfter` on its metrics port, and an alert fires ahead of expiry and when that series is absent. A CA rotation's delivery check reads this series, because `cmctl renew` and Certificate status do not show the copy, and clearing the old CA early would stop every connector. The source Secret stays in `lenny-system`, so `lenny-ops` can read this leaf until [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) lands, which yields impersonation of a replica alone. A deployment without cert-manager supplies the Secret by name in the new namespace.
- **Reads and placement across namespaces.** The informer the decision service uses for replica addresses gains this namespace, with a Role for `pods` read. The required anti-affinity names `lenny-system` in its namespace selector, and the metrics scrape's namespace selector covers this namespace. The replica's database login is a Secret here, with PgBouncer's auth side in `lenny-system`, where `lenny-ops` can read Secrets directly until [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) removes that right, and can still reach them afterwards through a workload that mounts them. This hop becomes cross-namespace and attestations can carry customer host lists, so it uses TLS with `verify-full`, under an egress PKI leaf for PgBouncer that the leaf table of [EG-11](egress-and-git-design.md#proposal-split) gains and `lenny-egress-ca` as the bundle. `pgbouncer-config.yaml` offers clients no TLS today.
- **Envoy.** One `ORIGINAL_DST` cluster with an upstream TLS context verifying one internal Service name, and one Envoy route per tunnel lifetime entry, with `custom_response` as on chained routes. A spike confirms this combination. Every replica, in any exclusive group, serves its CONNECT listener under the leaf for that one Service, which selects all outbound gateway pods, and each group's own Service is its public one. The address from the informer separates groups, so OC-4 adds no cluster.
- **Level 2.** A replacement proxy must set `centralProxy.level2.pinnedDestinations: true` and a new `centralProxy.level2.outboundUpstreamTLS: true`, asserting that it dials the address the decision returns, injects the credential, and verifies the name. **Level 1** remains refused, as the egress design states.
- **Agent distribution.** A statically linked Linux binary for amd64 and arm64, an image, and a Helm chart. Its source sits in this repository under the project's licence, so a customer can audit it. It does not update itself, because a customer runs it under its own change control, and the status read reports versions below the recommended one.

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `outboundGateway.enabled`, `.namespace`, `.replicas`, `.image` | Installs the outbound gateway and sets its namespace, replica count, and image. | Deployment | Platform admin (Helm) |
| `outboundGateway.publicHost`, `.service`, `.publicTLS.secretName` | Sets the hostname agents dial, its load balancer Service, and its public certificate, whose Secret lives in the outbound gateway's namespace. | Deployment | Platform admin (Helm) |
| `outboundGateway.limits.*` | Sets handshake rates, per-replica limits on replica sessions and memory, and HTTP/2 limits per replica session. | Deployment | Platform admin (Helm) |
| `outboundGateway.reservedHosts` | Adds names to the reserved set, beside every platform catalog host set, mint endpoint, pool-named LLM host, and public host of the outbound gateway and its exclusive groups. It can only extend that set. | Deployment | Platform admin (Helm) |
| `outboundGateway.allowSingleLabelHosts` | Permits names such as `gitlab` for customers without a domain. | Deployment | Platform admin (Helm) |
| `outboundGateway.grantKeyLifetime`, `.minAgentVersion`, `.recommendedAgentVersion` | Sets how long a decision service replica keeps one signing key, and the agent versions refused and flagged. | Deployment | Platform admin (Helm) |
| `centralProxy.level2.outboundUpstreamTLS` | Asserts that a level 2 proxy can reach the outbound gateway on this route. | Deployment | Platform admin (Helm) |
| Exclusive gateway groups (OC-4) | Names dedicated outbound gateway Deployments, each with its own public host, and assigns one to a tenant. | Deployment (group), tenant (assignment) | Platform admin |
| `tenantOverride.outboundHosts` | Lets a tenant's overlay add hosts routed to its own connector to the pool ceiling. It takes `deny`, the default, or `allow`. | Pool | Platform admin |
| Outbound grant and quotas | Grants the tenant the feature, and sets its connector, instance, stream, and throughput limits. | Tenant | Platform admin (admin API) |
| Outbound connectors | Creates, reads, and revokes the record for one customer network. | Tenant | Tenant admin |
| Enrollment tokens | Issues a token with an expiry and a use count, so a customer can enroll instances. | Connector | Tenant admin |
| Instances, and `instanceReapDays` | Lists and revokes agent instances, and sets how long an unseen instance holds its quota. | Connector | Tenant admin |
| Outbound hosts: `host`, `ports`, `caBundle`, `allowMirror` | Names a host a connector serves, the CA that validates it, and whether the mirror may fetch it. | Tenant | Tenant admin |
| Egress routes and tenant egress policy | Routes a host to an outbound host, and allows it, as in the [egress design](egress-and-git-design.md#configuration-by-scope). | Tenant | Tenant admin |
| VCS pools and credential bindings | Name credentials for the host, as in the egress design. | Tenant | Tenant admin |
| Session `egress.allowedHosts`, `gitClone`, and bindings | Name what a session uses, as in the egress design. A session cannot name a connector. | Session | Client |
| `allow` | Lists hosts and ports the agent dials. Empty until set, so an unconfigured agent refuses every stream. | Agent instance | Customer network owner (local file) |
| `denyCIDRs` | Lists resolved addresses the agent refuses, added to the defaults. | Agent instance | Customer network owner |
| `dns.servers`, `hosts` | Override the resolver, or map a name to a fixed address. | Agent instance | Customer network owner |
| `replicaSessions`, `drainSeconds`, `dialTimeoutSeconds` | Set attachment count, shutdown wait, and dial bound. | Agent instance | Customer network owner |
| `proxy`, `reportAllowlist`, `log.path` | Set the customer's outbound proxy, whether host names are reported, and the local grant log location. | Agent instance | Customer network owner |

## Scenarios

Tenant `acme` runs a self-managed GitLab at `gitlab.acme.internal` on a private network, with a certificate from Acme's private CA and no inbound firewall rule. Alice is Acme's tenant admin, and Bob runs Acme's network. Pool `acme-coding` is tenant-owned, so the profile can set `GITLAB_HOST`.

**Setup.**

- The platform admin gives `acme` its outbound grant, and the chart has `outboundGateway.enabled: true`.
- Alice runs `lenny-ctl outbound create acme-dc1` and issues an enrollment token with a use count of 2. Bob installs the agent twice with the token, sets `allow` to `gitlab.acme.internal:443`, and opens outbound 443 to `api.lenny.example` and `outbound.lenny.example`. The status read shows `connected`.
- Alice registers outbound host `acme-gitlab` for `gitlab.acme.internal`, with Acme's CA in `caBundle`, and adds an egress route to it. She adds the host to the tenant's always-allowed hosts, which every session of the tenant reaches, and to the pool's overlay, because the pool's ceiling is named. The overlay can add the host because the platform admin set `tenantOverride.outboundHosts: allow` on that pool.
- Alice creates a GitLab VCS pool with repository prefix `gitlab.acme.internal/acme/`. The provider is `mint-coarse`, so the git route stays `direct`. She adds a credential binding with `delivery: inject` on `gitlab.acme.internal` whose secret is that pool.

**Clone.** A session of Alice's clones `gitlab.acme.internal/acme/web`.

1. At plan bind, the API gateway resolves the ref through `lenny-egress` on its own identity, carrying `acme`. The decision selects replica r1 and signs a grant. r1 opens `Tunnel` to the instance the grant names, which verifies the grant, checks `allow`, resolves the name with Acme's DNS, and dials. The API gateway validates the host's certificate against `caBundle`, attaches a read token minted as below, and sends `ls-refs`.
2. The pod's adapter, the Lenny process that speaks to the API gateway for the runtime, clones the resolved SHA through `netagent`, which terminates the host because the tenant's git route covers it. `netagent` fetches a read token and the CA from the pod credential API, sends `CONNECT gitlab.acme.internal:443` to `lenny-egress`, and proceeds as above with a new grant for this stream. The clone's bytes are TLS to the host the whole way.
3. `egress.decision`, `egress.tunnel`, `outbound.stream`, `outbound.agent`, and `git.operation` all carry the same decision identifier or session. Bob's agent log has the same `did`.

**Mint.** The first read token is a GitLab project access token. The Token Service runs the egress design's mint checks, its own check and the decision service's scope decision, then goes through `lenny-egress` on its own identity with `acme` asserted, which the decision honours because the host is in an `acme` VCS pool. It validates the host's certificate against `caBundle` before it sends the stored GitLab credential, then calls GitLab's project access token API. The stored credential stays in `lenny-credentials`, and neither the replica nor Bob's agent sees it in clear. At session end the Token Service revokes the token over the same path.

**Push.** `git push origin fix-login` reaches `netagent`, which reads `git-receive-pack`, applies `allowedRefPatterns`, and asks the pod credential API for a write token. The Token Service checks the writable set, mints through the connector, and `netagent` forwards the push under a new grant. A push to `acme/payments`, which is outside the session's writable set, stops in `netagent`, and no stream is opened.

**`delivery: inject`.** The session's agent runs `glab mr create`. The pool's profile sets `GITLAB_HOST=gitlab.acme.internal` and `GITLAB_TOKEN=lenny-injected`. `glab` sends HTTPS through `HTTPS_PROXY`, trusting the per-pod CA with which `netagent` terminates TLS. `netagent` terminates TLS, overwrites the header with a minted token, and opens its own TLS to the host through the connector, validating against the binding's or host's CA.

**Connector offline.** Bob's instances lose their link in the middle of a session. Open tunnels end, and the next `git fetch` gets a CONNECT 503 with a decision identifier. After 120 seconds Alice receives `outbound_connector.offline`. A new session's plan bind fails with `GIT_CLONE_REF_RESOLVE_TRANSIENT`. The GitLab token of a session ending in this window stays valid until the link returns, when the [egress design](egress-and-git-design.md)'s pending revocation record retries it. No traffic takes the default route.

**A compromised replica and `globex`.** Replica r1 is compromised, and tenant `globex`'s connector and one of `acme`'s instances are attached to it. It inserts an attestation claiming another `acme` instance, and the signature check fails. It cannot remove r2's attestations, so r2 keeps its share of `acme`'s grants. For a grant it does receive, it tries `globex`'s grant on `acme`'s agent, which refuses it since `tid` and `cid` differ, and logs a refusal. It invents a grant, and the signature check fails. It replays an `acme` grant to the same instance, and the `jti` check fails, and to an unnamed instance, and the `ins` check fails, and again after that instance restarts, and the boot check fails. It takes an `acme` grant before the stream opens and sends its own bytes to `gitlab.acme.internal:443`: this succeeds at TCP level with no credential, once per named instance, and it shows as a stream whose agent-side byte count has no match at Envoy. An exclusive group removes this as a path for `globex` to reach `acme`.

**A stolen agent key.** An attacker copies an instance's key from Bob's host. It attaches and attests under a second boot identifier, and Alice gets `outbound_connector.duplicate_instance`. It draws some of `acme`'s streams and answers with its own TLS endpoint. `netagent` refuses the certificate, sends no token, and git fails. If the attacker rotates the key, Alice gets `instance_key_rotated`, and Bob's instance's next `trust` fetch, within 5 minutes, revokes the instance. Otherwise Alice revokes it, and its replica sessions close within the target of 5 seconds. Bob's other instance serves the traffic.

**A hostile agent.** Tenant `globex` modifies its agent. It sends HEADERS on a stream the replica did not open, and the replica's client ignores them and counts them against the replica session's frame limit. It sends frame floods, and the limits per replica session close the replica session. It returns a poisoned pack on a repository, which is `globex`'s own, and git's `transfer.fsckObjects` check in the pod applies as for any host. It reports `dns_nxdomain` falsely, and only `globex`'s `self_reported` stream records it.

**A DNS failure.** Bob moves GitLab, and one instance's resolver lags. Carol, a user, sees a CONNECT failure with a decision identifier. Alice runs the probe, which reports the first instance passing all stages and the second reporting `dns_nxdomain`. Where a grant names both, the replica retries the other, so most streams succeed, and the status read is `degraded`. Alice passes the output to Bob, and `lenny-outbound-agent check` on that host confirms it.

**GitHub Enterprise Server with the mirror.** `acme` also runs GitHub Enterprise Server (GHES) behind `acme-dc1`. Alice sets `allowMirror: true` on its outbound host and routes it to the mirror. The mirror fetches through `lenny-egress` with `acme` asserted, into an `acme`-keyed store, and pods fetch from the store. With the connector offline, sessions resolve refs from the store within the 6-hour age limit, and `GET /v1/sessions/{id}` shows `resolvedFromStore`.

**A user's link to a private host.** The GHES example in [`github-credential-addon-design.md`, "The same on GHES behind an outbound connector"](github-credential-addon-design.md#the-same-on-ghes-behind-an-outbound-connector), and the link provider with `kind: oauth2` in [`user-credential-delivery-design.md`, "Link providers"](user-credential-delivery-design.md#link-providers), both use this path. Tenant `acme` runs an internal service at `tracker.acme.internal` behind `acme-dc1`, and it registers an outbound host and a link provider for it.

1. Alice's browser is on Acme's network, so it reaches `tracker.acme.internal` for authorization, and the provider redirects back to Lenny's public URL. No inbound rule is needed.
2. The Token Service exchanges the code through the mint path: `lenny-egress` with `acme` asserted, under a grant, validating against the host's CA before it sends the client secret. C6 and C9 apply, so neither the outbound gateway nor the agent reads the secret or the token.
3. Refresh, revocation, and any narrowing template, which derives a per-session token from the stored one, take the same path. **With the connector offline, a refresh answers `unavailable`,** new sessions on a VCS pool with `userAuthorization: required` are refused with `USER_AUTHORIZATION_UNAVAILABLE`, the retryable error that [`user-git-identity-design.md`, "Failure behaviour"](user-git-identity-design.md#failure-behaviour) names, and a pending revocation waits in the durable record.
4. Under `delivery: inject`, `netagent` terminates TLS as in the `delivery: inject` scenario above, applies any `requestRules`, and opens its own TLS through the connector. The binding's hosts must lie within the provider's `apiHosts`.
5. Under `delivery: file`, the runtime holds the token and speaks TLS to the host itself through an opaque tunnel and the connector. `netagent` applies nothing, the grant still names the host, and the customer's allowlist still bounds it.
6. C8 holds: a link provider's endpoint on a host a tenant registers cannot shadow a platform-reserved host. An endpoint of a link provider with `kind: oauth2` on a private address needs an outbound host, or a platform registration, and it is refused otherwise. An outbound host's route value and CA digest for a link provider's host or a user credential type's host are in the token trust record, so registering or moving one, or rotating its CA, makes stored tokens unusable and sends them nowhere until a new user act. The name is resolved on the customer's side, so whoever controls the DNS, CA, or network behind it can read tokens, and the record cannot see that. C8's reserved set does not cover GHES, self-managed GitLab, or an endpoint of a `kind: oauth2` link provider, and the token trust record is what protects those.

## Residual risks

- **Grant use by a compromised replica.** A compromised replica can use an in-flight grant for its own bytes, at TCP level, to that host and port, once per named instance. An exclusive group limits this to the tenant's own replica and does not remove it. The agent's and Envoy's byte counts can reveal it after the fact, and they do not prevent it.
- **Metadata.** A replica and the agent see authorities and SNI. A compromised replica sees them for every tenant attached to it.
- **Availability.** A compromised replica can drop what agents attached to it carry, and by misstating its pod UID it can send that share to a replica with no replica session, until it is replaced. It can also fill the attestation table, which the row-count alert detects. A stolen key can take a share of one tenant's streams.
- **Decision service key.** A compromise of `lenny-egress-authz` gives TCP-level reach to every customer's allowlisted hosts, which is a wider reach than its [egress design](egress-and-git-design.md) statement. An in-memory key ends with eviction and revocation, and C3 limits it in space. Only the customer's allowlist protects the customer network at that point.
- **Grant key table.** Keys rotate routinely, so a new row is no signal. Row-count anomalies raise an alert, and a decision service that inserts one extra key may pass unnoticed, for at most 25 hours or until revoke-all. `lenny-ops` and the controller sit inside the egress PKI's trust boundary, as the egress design states, and either could obtain a login that inserts a row. [`security-oversight-design.md`](security-oversight-design.md) ([SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights)) removes `lenny-ops`'s direct Secret read, which closes the direct path and leaves `lenny-ops` inside the trust boundary, because it can still run a workload that mounts a Secret.
- **Release ahead of the external review.** The external review, which "External review" under ["Settled decisions"](#settled-decisions) requires, gates nothing, so the outbound gateway and the agent can reach customers before an outside reader has examined them. Tier 9's fuzz targets and the internal reviews apply in the meantime.
- **Pre-authentication parsing.** Anyone on the internet reaches a replica's TLS handshake before the key lookup, including on an exclusive group's public host.
- **Agent parsers.** A bug in the agent's HTTP/2, grant, or ClientHello handling runs on the customer's network. Hardening and fuzzing reduce this, and the allowlist does not contain a code execution bug.
- **Trust in the API gateway.** Agents fetch the grant key set from `lenny-gateway`, so a compromised `lenny-gateway` can add a key. It can already alter routes and policy.
- **Customer TLS weakness.** C6 depends on the client verifying the certificate. `netagent`, the resolver, the mirror, and the Token Service do. A tool in an opaque tunnel that disables verification exposes what that tool sends to anyone in the path, including a stolen-key holder.
- **Exfiltration.** Hostname filtering limits and does not prevent exfiltration, as the egress design states. An outbound host that accepts uploads is a channel into the customer's own network, and also a route for a session's agent to reach internal services the customer allowlisted.
- **Domain fronting through an internal proxy.** Encrypted Client Hello defeats the server-name check, as it does in the egress design.
- **A hostile tenant's own agent** can harm only that tenant's traffic, and its reports are `self_reported`.
- **Intercepting egress proxies** break the agent's client certificate, and a customer who cannot exempt the hostname cannot use the connector.
- **TLS-terminating load balancers** would strip the client certificate, and the chart's Layer 4 requirement is a deployer duty that preflight checks by a test handshake.
- **New public surface.** The outbound gateway accepts TLS from the internet before any authentication. Per-source handshake limits apply, and a distributed flood is the load balancer's or the provider's to absorb.
- **Revocation of a mint during an outage.** A `mint-coarse` token outlives its session while the connector is offline, until the egress design's pending-revocation retry succeeds or the token's expiry date passes.

## Specification sections affected

- §4 System Components: the outbound gateway and agent as new components, and §4.2 Session Manager's tenant-scoping table for the new resources.
- §5.2 Pool Configuration and Execution Modes: `tenantOverride.outboundHosts` beside the overlay flags of [EG-9](egress-and-git-design.md#proposal-split), egress policy objects.
- §9.3 Connector Definition and OAuth/OIDC: a sentence distinguishing Model Context Protocol (MCP) connectors from outbound connectors.
- §10.2 Authentication: the permission matrix, named permissions, and the statement that grants are not Lenny JWTs.
- §10.3 mTLS PKI: the outbound gateway's egress PKI leaf, which the leaf table of [EG-11](egress-and-git-design.md#proposal-split) gains, and the absence of an agent CA.
- §11.7 Audit Logging: connector, instance, host, and grant writes.
- §12.8 Compliance Interfaces and §12.9 Data Classification: revocation and deletion at tenant erasure, and classification of instances, hosts, attachment rows, and the new streams.
- §13.2.4 `lenny-system` Namespace NetworkPolicies and §17.2 Namespace Layout: the new namespace, and the rows under "Deployment and network rules".
- §13.3 Credential Flow: the agent key and the grant as credential classes.
- §15.1 REST API: the API routes under "Registration, rotation, and revocation", and the failure reasons.
- §16.1 Metrics, §16.5 Alerting Rules and SLOs, and §16.6 Operational Events Catalog: metrics, alerts, and events, and retention for the streams.
- §17.1 Kubernetes Resources, §17.2 Namespace Layout, and §17.8 Capacity Planning and Defaults: the Deployment, its public Service, and sizing.
- §24 `lenny-ctl` Command Reference: a new `lenny-ctl outbound` section.
- §25.5 Operational Event Stream, §25.7 Operational Runbooks, and §25.13 Bundled Alerting Rules.
- §28.3 Registers, §28.4 Claim register, §28.5 Contract cards (a new boundary card), §28.7 Wire-contract artifact register, and §28.8 Failure and degradation matrix: the proposed identifiers. A claim-register row with status `ABSENT` holds until each piece lands.
- §29 Communication Scenarios: a trace for a stream through a connector.

## Proposals

Proposals carry these labels, and each runs through the change-proposal pipeline. Until OC-3 lands, the chart's refusal under "Deployment and network rules" keeps the feature out of production.

| Label | Contents |
| :-- | :-- |
| OC-1 Gateway, agent, and identity | The proto, the agent's dial, TLS identity, reversed roles, allowlist, DNS, grant check, and attestations. The replica's handling of replica sessions, HTTP/2 limits, grant check, and server-name check. The tables `outbound_connector`, `outbound_agent_instance`, `outbound_enrollment_token`, `outbound_grant_key`, and `outbound_grant_key_revocation`, `REG-SITEATTACH` with its sweep, the restricted database logins in the chart and preflight, and the connector, enrollment token, instance, and agent API routes, including enrollment, rotation, reuse detection, retirement, reaping, and revocation. The grant signer in the decision service, attestation verification, replica selection, the group filter, and the route target's resolution. The Envoy cluster and routes. `outbound.*` evidence producers, the static hard limits, network rows, the public Service, and the agent's image and chart. The validator refuses the kind until OC-2, so OC-1's tests insert host records directly into [EG-6](egress-and-git-design.md#proposal-split)'s endpoint table. |
| OC-2 Hosts and policy | The outbound host validator, the reserved set at registration and at each decision, the outbound host API routes, `tenantOverride.outboundHosts`, `allowMirror`, the secret-less binding, the outbound grant, permissions, and `lenny-ctl outbound` commands. |
| OC-3 Operations | The liveness read and its states, events, tenant quotas, metering, metrics, alerts, the probe with its tenant and instance assertions, `lenny-outbound-agent check`, the revoke API routes, runbooks, and the removal of the chart's refusal of `outboundGateway.enabled: true`. |
| OC-4 Exclusive gateway groups | Chart-rendered dedicated Deployments, the outbound group label, group filtering of snapshots and the trust response, assignment by admin API, and validators. It lands after the others. |

### Dependencies

Each design states its own dependencies, and proposals from different designs may interleave as long as these hold.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| OC-1 | [EG-2](egress-and-git-design.md#proposal-split) | Decision service, typed route targets, and the mint path |
| OC-1 | [EG-6](egress-and-git-design.md#proposal-split) | Forwarded CONNECT route and selector peers, the NetworkPolicy peers that admit an in-cluster service by pod and namespace selector |
| OC-1 | [EG-10](egress-and-git-design.md#proposal-split) | Envelope and `lenny-evidence` |
| OC-1 | [EG-11](egress-and-git-design.md#proposal-split) | The outbound gateway's egress PKI leaf |
| OC-1 | [EG-8](egress-and-git-design.md#proposal-split), [EG-9](egress-and-git-design.md#proposal-split) | Named permissions and the single opt-in write path, which its connector and instance API routes use |
| OC-2 | OC-1 | The outbound gateway, the agent, and the tables |
| OC-2 | [EG-4](egress-and-git-design.md#proposal-split) | The typed `secret` union and termination of bound hosts, for `kind: none` |
| OC-2 | [EG-6](egress-and-git-design.md#proposal-split) | Endpoint registration with per-endpoint CA, and the per-kind validator's tenant-registered path |
| OC-2 | [EG-8](egress-and-git-design.md#proposal-split) | The effective-policy read and decision queries |
| OC-2 | [EG-9](egress-and-git-design.md#proposal-split) | The overlay, one write path, and named permissions |
| [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) for a private host | OC-1, OC-2 | The token path to a link provider on an outbound host. A public provider needs neither. |
| OC-3 | OC-1, OC-2 | The resources it reports on |
| OC-3 | [EG-8](egress-and-git-design.md#proposal-split) | Decision reads used in debugging |
| OC-4 | OC-1, OC-2 | The outbound gateway and assignment |
| End-to-end scenarios in OC-3's tiers | [EG-1](egress-and-git-design.md#proposal-split), [EG-3](egress-and-git-design.md#proposal-split), [EG-4](egress-and-git-design.md#proposal-split) | Minting, the pod git path, and `inject`. Scenario tests land with whichever of these lands last. |
| [FE-1](forensics-evidence-preservation-design.md#fe-1-quarantine) | None here | A `quarantined` binding is refused at the decision, so no grant is signed |

[TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) does not scope outbound connectors or outbound hosts below the tenant: [`tenant-scoping-identity-design.md` decision 4](tenant-scoping-identity-design.md#decisions) leaves them out of the scoped objects, so neither design depends on the other. A route that names an outbound connector can sit under `environmentPolicies` once OC-2 lands, with no OC change.

The guardrails of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) and the per-tenant SIEM export of [SO-3](security-oversight-design.md#so-3-per-tenant-siem-destination) apply through the single write path of [EG-9](egress-and-git-design.md#proposal-split) and the evidence ingest of [EG-10](egress-and-git-design.md#proposal-split), with no connector-specific work.

## Changes requested of egress-and-git-design.md

The [egress design](egress-and-git-design.md) has applied earlier requests, through revision 40: the Envoy text and [EG-6](egress-and-git-design.md#proposal-split) template, the kind rename, the decision cache statement, the grant signing key, the grant as the upstream credential, the generic endpoint record, durable pending revocation, the `self_reported` definition, the dependency table, and the level 1 refusal. It also added the per-call mint bounds, the tenant and host in the mint cache key, and the reserved host check at each decision. This design does not edit that document. Revisions 44 to 51 of that document add a link provider's endpoints and template hosts to the mint path's assertion, which this design's mint path uses unchanged. The table lists every request this design makes of that document, and the rows not marked applied remain open. That document lists the open rows under "Requests still open" in its "Proposal split".

| Where | Change | Why | When |
| :-- | :-- | :-- | :-- |
| "Labels and dependencies across designs", the OC-1 row | Applied: EG-8 and EG-9 are in that row. | OC-1 holds the connector and instance routes, which use named permissions and the single write path. | Applied |
| "Credentials", the resolver's tenant assertion | Let the decision honour the API gateway's assertion for a tenant's outbound hosts on a probe, and an instance named by the same identity. | The rule today requires a VCS pool prefix or a git route, and the probe must test one instance. | Additive, with OC-3 |
| "Credentials", the 1.5-second CONNECT reply budget | Make the budget depend on the route target kind, if the spike shows wide-area dials exceed it. | A customer's slow resolver would fail plan bind while pods would succeed. | Additive, after the spike |
| "Configuration by scope", `tenantOverride` flags | Add `outboundHosts` under EG-9's overlay table. | OC-2 needs the flag. | Additive, with OC-2 |
| "The egress PKI", and the controller's `lenny-egress-ca` targets | Add the outbound gateway's namespace as a target, and the leaf copy with its narrow Secret rights. | The replica runs outside `lenny-system` and needs its leaf and the trust bundle there. | Additive, with OC-1 |
| "Credentials", the pin and the bindings | State that a `secret: {kind: none}` binding is refused on a covered or pinned host. | OC-2 adds that kind, and a pin treats a host with a binding as checked. | Additive, with OC-2 |
| "Credentials", `pkg/binding`'s informer | Add the outbound gateway's namespace to the informer over the agent namespaces and `lenny-system`, for the decision service alone. | The decision takes a replica's address from this informer, and the replica now runs outside `lenny-system`. | Additive, with OC-1 |
| "Terms", Egress ceiling, and "An open ceiling" | State that a ceiling of `*` matches the hosts a tenant routes to its own outbound connector, and that the SSRF reasoning does not apply to them. | **Pool ceilings** under "Which hosts a connector may serve", and the pool-ceiling decision under "Settled decisions", rest on it, and the egress design said `*` means any public host. | Applied in egress revision 73 |
| "Credentials", **Errors**, the ref resolver's error list | Map a certificate failure after the CONNECT's 200 to `GIT_CLONE_REF_RESOLVE_TRANSIENT`, as "Failure mapping" states. | The list omits the case, and a connector route can produce it. | Additive, with OC-1 |
| "Transport security", **The egress PKI.**, the leaves the chart renders | Add a leaf for the outbound gateway, issued in `lenny-system` and copied as the namespace row above states, and a leaf for PgBouncer, whose hop from the outbound gateway uses TLS with `verify-full`. | "Deployment and network rules" relies on both, and EG-11 renders that list. | Additive, with OC-1 |

## Documentation

The rules in `doc-content.md` give each concept one owner page, and pages state behaviour with no section numbers. Each proposal carries the pages for its surface, after its specification changes land.

| Location | Change |
| :-- | :-- |
| New page "Outbound connectors" in the operator guide's "Network egress and credentials" chapter, which the [egress design](egress-and-git-design.md#documentation) defines | Owns the concept, the components, what the feature protects and what it does not, enabling it, sizing, and the public endpoint. |
| New page for customer network owners in `docs/operator-guide/` or `docs/tutorials/` | Owns installing the agent, the firewall and proxy requirements, the allowlist, DNS, high availability, and the local log. It is written for someone who holds no Lenny role. |
| `docs/tutorials/using-connectors.md` and §9.3's reader-facing pages | Add a sentence and a link separating MCP connectors from outbound connectors. |
| The egress chapter's "Tenant configuration" and "Git access" pages | Add outbound hosts and how the mirror treats them, linking to the owner page. |
| The egress chapter's "Evidence and visibility" page | Add the `outbound.*` streams and what `self_reported` means for an agent's claims. |
| `docs/operator-guide/ingress-and-tls.md` | Add the Layer 4 requirement and the dedicated hostname. |
| `docs/operator-guide/multi-tenancy.md`, `security.md`, and `troubleshooting.md` | Add a summary with a link, and a failure table entry. |
| `docs/operator-guide/scaling.md`, `configuration.md`, `lenny-ctl.md`, and `observability.md` | Add sizing, Helm values, commands, metrics, alerts, and events. |
| `docs/runbooks/` | Add runbooks for outbound gateway unavailable, grant verification failures, and handshake floods. |
| `docs/reference/` | Add API reference for the new API routes, and the failure reasons. |

Documentation uses the names `alice`, `bob`, `acme`, and `globex` under `doc-style.md`, and states the residual risks in the same words as the design.

## Tests

Each proposal carries the tests for its own surface, at the tiers `test-coverage.md` maps it to, each with a `// spec:` annotation. This section states what they must establish. No test has been written or run.

### Tier 0: static

- The rendered Envoy bootstrap validates with the outbound cluster and Envoy routes, and without them.
- The outbound gateway's rendered NetworkPolicy has no peer for the Kubernetes API, agent namespaces, the Token Service, or the decision service, and it mounts no service account token.
- The chart refuses a per-tenant quota sum above a replica's limits, and the proto and new admin schemas validate.
- The naming lint accepts the new identifiers.

### Tier 1: unit

- The grant check refuses each of: wrong tenant, wrong connector, an instance not named, a stale boot identifier, wrong authority, a case or trailing-dot variant, expiry and skew at each boundary, unknown and revoked `kid`, a key past `notAfter`, a tampered claim, and a replayed `jti`, including at the replay cache's capacity.
- The validator and the decision-time check refuse literals, every reserved name by pattern overlap, wide wildcards, and cluster suffixes. A tenant-defined host set does not reserve a name.
- Attestation verification refuses a forged signature, a revoked instance, a lower sequence, and an expired row. Replica selection accepts a pod only with the outbound component label and the tenant's group label, and it reads no address from a row.
- The server-name check handles a match, a mismatch, a missing name, a fragmented ClientHello, and non-TLS bytes.
- Replica selection handles no rows, and it is uniform across attested replicas. `denyCIDRs` covers mapped and link-local forms.
- The state computation covers each state and the 120-second offline hold-down.

### Tier 2: component

- Revocation in a snapshot closes a replica session, and stale snapshots at 60 seconds and at 5 minutes behave as stated.
- Reversed gRPC over a dialed TLS connection carries a tunnel, and the agent cannot open a stream.
- A draining replica sets the flag on `Status`, and an agent attaches elsewhere and sends an attestation omitting it before the replica exits.
- The replica's database role cannot update or delete a row in `REG-SITEATTACH`.
- Enrollment, rotation with overlap, a key older than the maximum, retirement, reaping, and token use counts behave as stated, each against Postgres. A signed call with a superseded key on each agent route revokes the instance, and a crash between storing and registering a new key does not.
- A signature over one path or body fails on another, and a repeated nonce fails.
- The sweeper raises `duplicate_instance` for interleaved boot identifiers, and it does not for a restart.
- A junk row with a future sequence does not block a later genuine attestation.
- A new signing key is published before first use, and an agent fetches on an unknown `kid` within its rate limit. A key row with a far `notAfter` fails the constraint, and an agent applies the 25-hour cap to a set that claims longer. Revoke and revoke-all reach an agent at its next fetch, and a restart alone leaves rows valid.
- A replica session attached under the agent's keepalive policy survives idle periods without `too_many_pings`.
- A burst of replica session changes stays within the status quota.

### Tier 3: contract

- The agent and the outbound gateway at different versions negotiate or refuse under `minAgentVersion`.
- The failure mapping table holds, per row, at the resolver and at the mirror's fallback logic.
- A grant issued by the decision service verifies at the agent and at the replica with the key sets each holds.

### Tier 4: integration

- Clone, shallow clone, partial clone, and push work against a test GitLab and a test Gitea behind an agent, with a private CA, and `info/lfs` is refused on both while the [egress design](egress-and-git-design.md)'s refusal stands.
- The Token Service mints and revokes through the connector, with its certificate check failing closed on a wrong CA.
- `inject` with TLS termination, `allowedRefPatterns`, and `credentialPinned` work on an outbound host.
- The API gateway's `ls-refs` works, with the failure mapping's errors.
- Revocation reaches the agent's replica session within the 5-second target, while a tunnel is open.
- A host set added to the platform catalog after an outbound host was registered refuses that route at the next decision.
- An evidence join by decision identifier across `egress.decision`, `egress.tunnel`, `outbound.stream`, and `outbound.agent` holds.
- A mirror fetch of a `mint-scoped` host fills a tenant-keyed store, and a route to the mirror without `allowMirror` is refused.

### Tier 5: end to end on Kind

- The acme scenario from enrollment to `glab mr create`, with the agent in a separate network namespace that has no route from the cluster except its own dialed connection.
- The liveness states as agents start, stop, and lose one replica.
- Probe output for each stage's failure, and `lenny-outbound-agent check` on the same conditions.
- The conformance tests for the NetworkPolicy run on Calico or Cilium, because the egress design records kindnet's limits.

### Tier 6: end to end on cloud

- Agents outside the cloud network dial a cloud load balancer, across a wide-area path, through an outbound HTTP proxy, and with a clone of a large repository. The tests check source address preservation and the Layer 4 preflight.

### Tier 7: load

- Idle replica sessions at the target count, per replica memory and CPU.
- New streams per second with grant signing, and its added CONNECT latency.
- Throughput per tunnel and per replica session at realistic round-trip times, with bulk clones beside small requests on one replica session.
- One tenant at its limits while another's latency and throughput are measured, for [P7](egress-and-git-design.md#security-properties).

### Tier 8: chaos

- Killing and freezing replicas and instances at load: failed streams, recovery time, and reattachment.
- Postgres outage and pooler loss, with the snapshot rules. Decision service failure, showing fail closed.
- Clock skew on the agent's host, and rolling restarts of both sides with open tunnels.

### Tier 9: security

- **Cross-tenant.** `globex`'s pod cannot reach `acme`'s host by route, by name, or by asserted tenant. A compromised-replica stand-in presents `globex`'s grants, invented grants, replays to the same instance, replays to an unnamed instance, and replays after an agent restart to `acme`'s agent, and each is refused and logged.
- **Exclusive groups.** `globex`'s agent dialing `acme`'s dedicated host is refused during the handshake, before any HTTP/2 frame is read. A shared replica refuses an exclusive tenant's key.
- **Namespace and leaf.** The chart refuses `outboundGateway.namespace` set to the release, agent, or credentials namespace. A pod in another namespace bearing the outbound gateway's label is not accepted as a replica. The controller's rights reach only the named source Secret and the copy, and a renewed leaf reaches the replica with no restart.
- **Secret-less binding.** `kind: none` is refused with `attach` or `placeholder`, on a covered or pinned host, on a host whose route is not an outbound host at write and at bind, and for a delivery other than `inject`. No credential is attached on any request, and an image without the kind leaves the host an opaque tunnel.
- **Rotation retry.** A retry after a lost `rotate` response succeeds, and a signature from a truly superseded key still revokes.
- **Database roles.** Each new login is refused the updates, deletes, and column writes this design forbids.
- **Attachment.** A stand-in with the replica's role inserts unsigned and replayed attestations, tries to delete peers' rows, and claims a public address and a pod outside its group. Selection does not change, and no CONNECT leaves for that address.
- **Mint cache.** A tenant registering a host with another tenant's GitHub Enterprise Server identifiers never receives that tenant's cached token.
- **Hostile agent.** A stand-in opens streams, floods frames, sends oversize headers, sends `PUSH_PROMISE`, stalls windows, and sends oversize status, and the replica closes the replica session within its limits without harming another tenant's traffic.
- **Fuzzing.** Fuzz targets cover the replica's frame and message handling, and at the agent the grant parser, the ClientHello reader, and the gRPC handlers, the last two with a stand-in as the hostile peer and a pod as the source of tunnel bytes.
- **Stolen key.** A stand-in with a stolen key attaches and serves a self-signed endpoint, and `netagent`, the resolver, the mirror, and the Token Service send no credential. It rotates the key, and the honest instance's next use of the old key revokes the instance.
- **No reach back.** A replica stand-in tries to reach the API server, the Token Service, agent pods, and the decision service, and NetworkPolicy drops each.
- **Plaintext.** A plain-HTTP host behind an agent is refused at both ends, and a mismatched SNI is refused.
- **Reserved hosts.** A tenant cannot register an outbound host covering a reserved name, by wildcard, case, or trailing dot.
- **Fail closed.** With the connector down, a host with a public DNS name is never reached by the default route.
- **Local allowlist.** A valid grant for a host outside `allow`, and a resolved address in `denyCIDRs`, are both refused.
- **Secrets.** Neither `Proxy-Authorization`, a grant, nor a key appears in any log at either end.

### Tier 10: conformance

- The Envoy suite gains: the forwarded route with upstream TLS, refusal of a wrong SAN, removal of the pod's credential, and fixed replies, on the supported Envoy versions.
- A level 2 proxy asserting `outboundUpstreamTLS` passes the same cases.

### Tier 11: documentation

- Links and the page ownership rule hold, the examples use the conventions in `doc-style.md`, and each documented command and Helm value exists.

## Spikes owed

Only the Envoy item below has run, on 2026-11-05, with its evidence under `evidence/tenant-outbound-connector-design/spike-oc4/`. The rest has not run. Round 5 of the egress validation, which "Grants" cites for Envoy's lack of an ext_authz response cache, is under `evidence/egress-and-git-design/spike-envoy-r4/`, in `RESULTS.md`.

- **Envoy (ran 2026-11-05).** On 1.40.2 and 1.39.6, an `ORIGINAL_DST` cluster reading the replica address from ext_authz metadata, with an upstream TLS context that sets SNI and verifies the egress CA and one exact SAN, forwarded the CONNECT to a TLS-only replica stand-in. The replica saw the grant as `Proxy-Authorization` and neither the pod's credential nor Lenny's routing headers. A wrong SAN and a foreign CA each failed before any request reached the replica, with a 503 and `CERTIFICATE_VERIFY_FAILED` in Envoy's log. `custom_response` turned a replica's 407 into a fixed 502 and its 403 into a fixed 403 with no upstream headers or body, a replica's 503 passed through, and `max_stream_duration` ended a tunnel at 5.1 s against 5 s. `transport_socket_matches` cannot select TLS on the existing cluster. An ext_authz deny with 503 kept its body and reason in gRPC mode and lost both in HTTP mode, as "Failure mapping" states. A level 2 proxy, a real replica, leaf rotation, and load were not covered.
- **Reversed gRPC.** grpc-go serving on a dialed connection, and throughput against raw HTTP/2 on 100 ms of latency.
- **Hostile peer.** What a malicious HTTP/2 server can do to a grpc-go client, given that the client ignores frames for unknown streams and sends no `SETTINGS_ENABLE_PUSH`, with the limits grpc-go exposes, and how much the replica must add by counting frames. The same in the other direction for the agent's server.
- **Idle cost.** Memory per replica session at a target of 20,000 replica sessions, which assumes 5,000 tenants, two instances each, and two replica sessions per instance.
- **Grant cost.** Signing at 2,000 CONNECTs per second, replay cache memory, and first-use latency of a new key against the 1.5-second budget.
- **Attestation rate.** Inserts at 10,000 instances, one attestation per minute on each of two replica sessions, and the decision service's verification and poll cost.
- **CONNECT budget.** Reply time against the resolver's 1.5 seconds across 50, 150, and 300 ms round trips, with slow customer DNS.
- **Load balancers.** Whether repeated dials reach distinct replicas with the common cloud load balancers and connection reuse, and source address preservation.
- **Corporate paths.** Long-lived TLS through common outbound proxies, including idle timeouts against keepalive, and the failure seen under interception.
- **Konnectivity and KAS.** A time-boxed reading of their protos, agents, and tunnel modules for reusable code, and for lessons on connection counts.
- **Full path.** Clone, push, and `glab` through a prototype, which also covers the [egress design](egress-and-git-design.md#validation-still-owed)'s still-owed push through a raw tunnel to an address the decision returns.

## Settled decisions

- **Local allowlist default.** The allowlist starts empty, so the agent refuses until configured. Following the platform's routes would let a compromised platform reach anything the agent's host can reach, which defeats C3. The cost is a required step, and a drift failure when routes change, which the liveness read reports.
- **Non-TLS internal hosts.** Both ends refuse them. Without TLS, C6 and C7 fail, since a stolen key or a compromised replica would read tokens. A customer with a plain-HTTP internal registry fronts it with TLS.
- **Intercepting customer proxies.** The agent refuses them, and the customer exempts the outbound gateway's hostname. Authenticating inside the tunnel would let the proxy hold what a replica holds, plus the agent's replica session. The agent routes to the API host work through inspection. Some customers need a change approved by their security team.
- **A runtime-held token through a connector.** On a host no pool or route covers, under `delivery: file`, the token crosses the connector inside the runtime's own TLS, and nothing on the platform side sees or limits its use, as [`user-credential-delivery-design.md`, "What `file` does not promise"](user-credential-delivery-design.md#what-file-does-not-promise) states.
- **Naming.** Identifiers use `outbound` or `site`, and the [egress design](egress-and-git-design.md) renamed its kind, because §28.1 forbids reuse of the §9.3 term.
- **First version.** The connector ships in the first version, which takes OC-1, OC-2, and OC-3, since the chart's refusal lifts only with OC-3. OC-4 lands after those.
- **Build or adopt.** Build on grpc-go, in the owner's words "go with your recommendations", which also covers the next two entries. Lenny owns an internet-facing service and a customer-distributed binary, with review and support duties. This rests partly on preference, as "Recommendation" states, so the reversed gRPC and hostile peer spikes under "Spikes owed" run first, and inlets Uplink is the option to try if they fail.
- **Isolation on shared replicas.** Shared replicas with signed grants, and OC-4's exclusive groups, filtered at admission and at the decision, for those who need them. The in-flight grant residual stays on shared replicas, where OR-4 holds except for that case.
- **Agent identity.** A self-signed key looked up by public key hash, which the owner accepted with "ok". Replicas depend on a store snapshot, and there is no offline verification. A Lenny CA would add a key to protect and a separate revocation path.
- **Mirror for connector hosts.** Off unless `allowMirror` is set on the host, which the owner accepted with "ok". GitHub Enterprise Server behind a connector has no store fallback or warm fill by default, and each fetch crosses the customer's link.
- **Namespace.** Its own namespace, in the owner's words "move to its own namespace, assuming you don't find any blockers when doing that". A first check found none, and a review then showed that its separate `outbound` CA could not deliver trust to Envoy or to `lenny-evidence` without widening trust elsewhere. Copying an egress PKI leaf into the namespace resolves both, so no blocker stands. The work is the copy, cross-namespace network rows, an informer and Role, TLS on the pooler hop, and the scrape selector. It needs an end-to-end run on Kind, which has not happened.
- **Anonymous hosts on a private CA.** A binding with `secret: {kind: none}` in OC-2, which the owner accepted with "ok". This replaces deferring it to [EG-4](egress-and-git-design.md#proposal-split), credential delivery, because no stored format changes and `caBundle` lives on the outbound host.
- **Grant signing key storage.** An in-memory key per decision service replica, replaced every 24 hours, with its public part in `outbound_grant_key`, a 25-hour cap in the database and at the agent, and revoke and revoke-all routes, which the owner accepted with "ok". The cost is a table, a dedicated database role, an on-demand trust fetch, first-use latency, and agents depending on the API host within a day. An inserted key is not reliably detected. A Secret would be readable with no trace by `lenny-ops`, whose Secret rights [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) removes, though a Job could still mount it. `lenny-ops` can patch the decision service's Deployment and capture an in-memory key, which causes a visible rollout, and a captured key dies within 25 hours.
- **Pool ceiling.** `tenantOverride.outboundHosts`, defaulting to `deny`, in the owner's words "go with your recommendation". A SaaS operator sets it on each shared pool with a named ceiling, and a pool whose ceiling is `*` needs no step. The setup guide recommends setting it at install for an operator selling the connector. The overlay table of [EG-9](egress-and-git-design.md#proposal-split) gains a row. Connector hosts reach only the tenant's own network, so the flag's value is that a ceiling stays the outer bound with no exception.
- **Authorization at the agent.** A per-CONNECT signed grant. The decision service holds a signing key and signs each CONNECT, and a compromise reaches every customer's allowlisted hosts at TCP level, with the bounds "Grants" states.
- **External review.** OC-1's outbound gateway and agent need an external security review. The review blocks no proposal, chart setting, or release, and the chart's refusal under "Deployment and network rules" stays tied to OC-3 alone. Its findings are fixed as defects when they arrive. The cost is stated under "Residual risks".
- **TLS to the outbound gateway.** TLS with a verified SAN, in the owner's words "yes, encrypt and test". The Envoy spike under "Spikes owed" ran and supports it. Accepted cost: one Envoy cluster, a route per tunnel lifetime entry, and the level 2 assertion `outboundUpstreamTLS`.
- **Attachment register.** Agent-signed attestations in append-only Postgres, polled each second, which the owner accepted with "ok". Accepted cost: an insert per instance per minute, signature checks in the decision service, and up to 180 seconds of 503s from a replica after an agent loses every replica session.
- **Grant-to-instance binding.** A grant names up to two instances, each with its boot identifier, in the owner's words "up to 2". An instance is one agent process with its own key, so redundancy comes from enrolling more instances, and two processes sharing one key raise `outbound_connector.duplicate_instance`. Accepted cost: a compromised replica can open up to two streams per grant, which is C2's bound.
- **The outbound gateway's hosts on the reserved set.** Settled by the owner on 2026-11-08 ("ok"). "The gateway's own hosts" means the outbound gateway's public hosts: `outboundGateway.publicHost` and each OC-4 group's public host. This document calls `lenny-gateway` the API gateway, and every other bare "gateway" in its first full draft meant the outbound gateway. Reserving these names keeps a tenant from registering Lenny's connector endpoint as a host behind its own connector. The API host is not reserved by this rule.
- **Values of `tenantOverride.outboundHosts`.** Settled by the owner on 2026-11-08 ("ok"): `deny`, the default, and `allow`. The egress design's `narrow` and `narrowOrExtend` were not reused, because this flag only adds hosts and has nothing to narrow.

## Open decisions

None. Every decision is under "Settled decisions".

## Evidence for the need

- A [Cursor forum post](https://forum.cursor.com/t/self-hosted-worker-online-but-empty-repo-list-in-agents-ui/156060) describes "self-managed GitLab on an internal host (VPN-only)".
- On Codex, [one user wrote](https://community.openai.com/t/gitlab-with-codex-cloud-agent/1268080) that using self-hosted git "would be a total Game changer".
- Cursor (vendor) [documents egress ranges and tunnels](https://cursor.com/docs/cloud-agent/security-network).

## Validation record

This section records a check of the claims that earlier revisions of the egress design made about connectors. The egress design has applied its results, and the current text above states them.


### Test of the egress design's claims

Revision 36 of `egress-and-git-design.md` said a connector "adds no Envoy route and changes no decision contract". Each part was read against that design and the tree, and later revisions applied the resulting changes. The table states the position against revision 40.

| Claim | Position | Finding |
| :-- | :-- | :-- |
| No new Envoy route | Corrected there | The forwarded route's cluster was tested with no TLS toward the upstream. The hop carries a credential and customer hostnames across the pod network, so it needs TLS with a verified name. TLS settings belong to the Envoy cluster, so the egress design now states one new cluster and one route per tunnel lifetime entry. The 2026-11-05 spike found that `transport_socket_matches` cannot select TLS on the existing cluster, because an `ORIGINAL_DST` host made from metadata carries no endpoint metadata, so the new cluster stands. |
| No decision contract change | Holds on the wire, with changes behind it | The ext_authz response keeps its fields: a route header, an address, and credential headers. The service behind them changes. It gains a signing key, which qualifies its stated property that it "cannot mint". Its cache statement covers the per-call address and credential. A route's address comes from verified attestations and the pod informer rather than from a registration, and a deny carries status 503 for an offline connector. |
| Typed route targets need no new field | Holds | The kind is now `outboundConnector`. |
| The name is never resolved on the platform side | Holds | Envoy's forwarded route uses `ORIGINAL_DST` rather than the dynamic forward proxy, and `netagent` does not resolve the CONNECT target. Private literals stay refused by the decision service, so a connector host is always a name. |
| A selector peer on the `lenny-egress` policy | Holds | The `lenny-egress` policy also admits the public internet, so this peer does not bound a dial. The address's source does, which is why a replica cannot supply it. |
| The per-kind validator and the endpoint record | Holds | The egress design states one record with a kind, a CA, and kind-specific fields. |
| `allowedRefPatterns` and `credentialPinned` | Holds | Both act in `netagent`, before the CONNECT. |
| The server-name check | Gap, stated there | The check runs on an internal listener that forwards with `sni_dynamic_forward_proxy`, so the forwarded route does not run it. The outbound gateway and the agent perform it instead (C6). |
| Mints through `lenny-egress` | Holds | The egress design now has durable pending revocation, per-call bounds, and the tenant and host in the cache key. |
| The dependency table | Corrected there, with one addition | OC-1 also needs EG-8 and EG-9, as "Changes requested" states. |

## Review history

This section records what each revision changed and the reviews of earlier revisions. The current text above supersedes any statement here.

### Revision notes

The notes for revisions 4 to 23 were carried in the status line of those revisions. The notes for revision 24 and later were written here.

- Revision 4 added an OAuth link to a private host, and revisions 5 to 11 align it after review.
- Revisions 12 to 17 change only the pointers to the revisions of the companion designs that this design reads against.
- Revision 18 records the owner's decision on the external review.
- Revision 19 records decisions 1, 2, 3, 5, 7, 8, 9, 10, and 12 as settled.
- Revision 20 records decisions 4 and 6 and the Envoy spike.
- Revision 21 records decision 13.
- Revision 22 qualifies the HTTP-mode deny. Revision 22 followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read.
- Revision 23 changes only pointers after the user credential documents split.
- Revision 24 followed a pass for a reader new to the project. It added "Terms from other designs and the platform", "Labels from other designs", and "High-level design", added terms of this design, the tag table, and the evidence sources, split "replica session" from a Lenny session and the agent from a session's agent, named the setting and the object behind each configuration value, linked every citation of another design and every label from one, called the egress design's interfaces by name, and moved the review records and the test of the egress design's claims to the end. A reader review then added terms for the roles, pools, routes, stores, opaque tunnels, and channel identifiers, and qualified the senses of pin, grant, and gateway. It changed no rule.
- Revision 25 corrected the "Tenant-owned pool" term, which left the pool's profile undefined and implied that the tenant sets it, to name the client proxy profile that the platform admin's per-pool override sets on a pool whose `ownerTenantId` names the tenant, that a validation pass found, and changed no rule.
- Revision 26 applied two owner decisions of 2026-11-08. The reserved set names the outbound gateway's public hosts, `outboundGateway.publicHost` and each OC-4 group's public host, where it said "the gateway's own hosts", and the `outboundGateway.reservedHosts` row lists them too. `tenantOverride.outboundHosts` takes `deny`, the default, or `allow`, where only `deny` was named.
- Revision 27 updated the "Covered host" entry in "Terms from other designs and the platform" to the egress design's definition, which limits a git route to the tenant's own and includes a provider's alias hosts, linked it to "Git on covered hosts", and corrected the introduction to "Revision notes", which said that every note but the last came from a status line, after a re-read of `egress-and-git-design.md`, `user-credential-delivery-design.md`, `user-git-identity-design.md`, and `github-credential-addon-design.md` at their current revisions, and changed no rule.
- Revision 28, dated 2026-11-09, applies the design sync review of 2026-11-08, and changes no rule. "Changes requested of egress-and-git-design.md" records the request that a ceiling of `*` match outbound connector hosts, which the egress design applied in its revision 73, and gains the requests that sat only in prose: the ref resolver's mapping of a certificate failure after 200, and the egress PKI leaves for the outbound gateway and PgBouncer. The TS-1 dependency row becomes a statement that TS-1 does not scope outbound connectors or hosts, under `tenant-scoping-identity-design.md` decision 4. The FE-1 label says that FE-1 sets the `quarantined` state that EG-2 adds. The statements about `lenny-ops` say that SO-7 removes only its direct Secret read. "Failure mapping" gains a row for a suspended outbound connector or host. The mint walkthrough names the mint checks in place of "both gates", the scenario states that the pool's `tenantOverride.outboundHosts` is `allow`, and the requests introduction no longer ends on a fragment. "Settled decisions" drops the retired decision numbers, which were 1 for build or adopt, 2 for isolation on shared replicas, 3 for authorization at the agent, 4 for TLS to the outbound gateway, 5 for agent identity, 6 for the attachment register, 7 for the mirror, 8 for the pool ceiling, 9 for anonymous hosts on a private CA, 10 for the namespace, 11 for the external review, 12 for grant signing key storage, and 13 for grant-to-instance binding.


### Review of revision 19

An independent review read the namespace move, the secret-less binding, and the in-memory keys.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| M1, a replica has no credential `lenny-evidence` accepts under a separate CA, and adding that CA would let its holder forge other producers | Holds. | The `outbound` CA is removed. A copied egress PKI leaf keeps ingest's SAN allowlist as it is. |
| M2, nothing delivers the `outbound` CA to Envoy, and the only carrier would make every component trust it | Holds. | Resolved by the same removal, so the existing statements about an egress PKI leaf stand. The reviewer's `ClusterIssuer` option was not taken, because it moves the CA key and needs a limit on who can request leaves. |
| Minors on the namespace | Hold. | `enforce: restricted`, the value and its guard, NET-047's text, the address check's namespace, the database login in C5, and TLS on the pooler hop. |
| Minors on `kind: none` | Hold. | `inject` alone, refusal on covered and pinned hosts, the route check at write and at bind, the EG-4 dependency, and tests. |
| Minors on keys | Hold. | The revoke-all bound's condition, early reporting of a failed fetch, and a rotation retry that is no reuse. |

A stop check on the result found no Critical, High, or Major issue. Its Minors are applied: the controller's rights, the copy's delivery check and its expiry series, a level 2 proxy's ingress row, the rotation retry's exact condition, the detection bound's condition, every pin source against `kind: none`, the fetch state as an attestation trigger, `verify-full` on the pooler hop, the namespace value and guard, and what a shared replica reads. These last fixes have not been re-read.

### Review of revision 9

A final regression review raised N1 on this design's walkthrough: with the git route `direct` and an egress route to the connector, a flagged session's git request asserted the platform default. It holds, and the egress design's fall-through between route lists makes this topology work, with a test naming it. None was rejected.

### Review of revision 8

Round 5 of the reviews raised Sync M1, LFS on a connector route against the egress design's refusal, and m15, a held token on a covered host. Both hold. OR-3, the tier 4 row, and the residual are limited to what the egress design allows. None was rejected.

### Review of revision 7

Round 4 of the reviews raised m21: a request here was already applied. It holds, and the row says so. None was rejected.

### Review of revision 6

Round 3 of the reviews raised R3-4 on this design's text: the name is resolved on the customer's side, behind an unchanged outbound host. It holds, and the scenario states the limit. None was rejected.

### Review of revision 5

Round 2 of the reviews raised these on this document, each verified against the text. H1: a route plus a CA behind an unchanged name read tokens, and C8's reserved set does not cover GHES, self-managed GitLab, or a `kind: oauth2` endpoint. It holds, and the token trust record covers the route target and CA digest. N30: this document called a move a token-bearing edit that the other designs did not list. It holds, and the same record states it. None was rejected.
