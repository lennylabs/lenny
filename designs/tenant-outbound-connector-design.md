# Tenant outbound connector

Status: design for owner review, revision 1, 2026-11-02. Nothing here is built or staged. No spike or test ran for this revision, so every behaviour below is a design statement, and "Spikes owed" lists what must be measured first. Proposals carry the label OC-n. Statements about Lenny were checked against the tree and the specification on this date. Statements about other products come from the sources linked beside them, and those marked "(vendor)" are the vendor's own description.

## Problem

Customers commonly keep git servers and package registries on private networks. In `egress-and-git-design.md`, reaching a private address needs a platform registration, and a shared egress address makes a customer's firewall rule admit other tenants too. For a SaaS operator each such customer is a support ticket, and a customer may refuse any inbound firewall rule at all. A customer-run agent that dials out to the platform removes both problems.

## Scope

- The agent a customer runs in its own network.
- The in-cluster gateway the agent dials.
- Tenant self-service registration of a connector and the hosts it serves.
- Connector identity, liveness, rotation, revocation, quotas, and evidence from the gateway.

Egress policy, routing, and the existing evidence streams stay in `egress-and-git-design.md`. This design treats the hooks that document lists under "Proposal split" and "Reserved for other designs" as fixed inputs, and it lists what it needs changed under "Changes requested of egress-and-git-design.md".

## Terms

| Term | Meaning |
| :-- | :-- |
| Outbound connector | A tenant-owned record naming one customer network. Every instance of it serves the same hosts. |
| `lenny-outbound-agent` (the agent) | The binary or image a customer runs. One running copy is an instance, with its own key. |
| `lenny-outbound-gateway` (the outbound gateway) | The in-cluster Deployment the agent dials. `lenny-gateway` keeps the name "API gateway" here, so the two are never confused. |
| Grant | A short-lived signed statement from `lenny-egress-authz` authorizing one stream to one authority through one connector. |
| Outbound host | A tenant-registered endpoint whose target kind is the outbound connector. |

**Naming.** The bare word "connector" is already bound in the specification: §9.3 Connector Definition and OAuth/OIDC, `/v1/admin/connectors` in §15.1 REST API, and `CH-MCP-CONNECTOR` in §28.3 Registers. §28.1 Naming law says that an identifier stem may not reuse a term the specification already binds to an unrelated mechanism. This design therefore uses `outbound` or `site` in every identifier, API path, Helm value, metric, and route target kind. Proposed identifiers, which OC-1 settles against §28.3, are `LNK-OUTDIAL` for the agent's connection, `CH-HOSTTUNNEL` and `CH-SITESTATUS` for what it carries, `CH-GRANTEDCONNECT` for the hop from `lenny-egress`, and `REG-SITEATTACH` for the attachment register.

## Requirements

- OR-1. A customer opens no inbound firewall rule. The agent needs outbound TCP 443 to two hostnames, the API host and the outbound gateway host, directly or through the customer's outbound HTTP CONNECT proxy.
- OR-2. A tenant admin registers a connector and its hosts without a Helm change, a platform registration per host, or an operator ticket, once the platform admin has granted the tenant the feature.
- OR-3. Every feature of the private destination path works on a connector route: clone, push, LFS, the API gateway's ref resolution, the Token Service's mints, `inject` delivery, `allowedRefPatterns`, and `credentialPinned`.
- OR-4. A connector for `acme` never carries `globex`'s traffic, including under a compromised outbound gateway replica. "Security properties" and "Residual risks" state how far this holds.
- OR-5. The customer limits what the agent reaches, independently of anything the platform asks.
- OR-6. The egress design's properties hold on this path: P4 tenant isolation, P5 attribution, P6 fail closed, and P7 quotas.
- OR-7. The loss of one agent instance or one outbound gateway replica ends only the tunnels it carried.
- OR-8. A tenant admin sees liveness and diagnoses a failure without the operator's help.
- OR-9. The feature has no runtime dependency on a third-party service, because Lenny deployments include self-hosted and air-gapped clusters. Its licence terms allow an operator to run it as a paid service, and the agent is distributable to customers. Lenny's Go files carry `SPDX-License-Identifier: MIT`.

## Security properties

- C1. A stream enters a customer network only under a grant that `lenny-egress-authz` signed for that tenant, that connector, and that authority. The agent verifies this, so the outbound gateway cannot create access.
- C2. A grant opens at most one stream, and it expires within about a minute.
- C3. The agent's local allowlist bounds what it dials, whatever the platform asks. It is empty until the customer fills it.
- C4. The agent initiates nothing toward the platform. It answers streams the gateway opens and writes bounded status messages. It cannot ask the outbound gateway to dial anything, and the outbound gateway has no network path to the agent pods, the Token Service, the decision service, or the Kubernetes API.
- C5. An outbound gateway replica holds nothing usable elsewhere: no tenant secret, no agent private key, no grant signing key, and no service account token.
- C6. Traffic is TLS end to end between the platform-side client (`netagent`, the API gateway, the mirror, or the Token Service) and the customer's host. Neither the outbound gateway nor the agent holds a key that decrypts it. Both refuse a stream whose first bytes are not a TLS ClientHello naming the granted host.
- C7. A stolen agent key yields a position in the path: availability, and the metadata of `acme`'s connections. It yields no tenant credential, no reach into the cluster, and no reach to another tenant.
- C8. A connector host cannot shadow a platform-reserved host, so a tenant cannot route a platform-owned credential through its own network.
- C9. Content arriving from a customer network is treated as content from a hostile upstream. The mirror's fetch sandbox and the resolver's bounds apply unchanged.
- C10. A connector failure blocks traffic. The decision never falls back to the platform default route for a host whose route names a connector.

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Hostile customer-side agent (the tenant itself, or an attacker on the customer's host) | Refuse or black-hole its tenant's streams. Read ciphertext and SNI of its tenant's connections. Return hostile content on a stream, which is what a hostile git host returns. Send malformed HTTP/2 frames to a replica. Report false status. Consume its tenant's quotas. | Open a stream toward the platform. Cause a dial from the cluster. Decrypt a client's TLS unless it holds a key for that host's CA. Reach another tenant. Forge `enforced` evidence. | C4 and C6. Status messages are bounded, enumerated, and stored as `self_reported`. Replicas enforce per-connection frame and memory limits and P7 quotas. |
| Holder of a stolen agent key | The same, from any address, until revocation. It can also take a share of the tenant's streams away from the honest instances. | Mint, read a credential, or use the key against another connector. The private key proves one instance only. | C7. `netagent`, the resolver, the mirror, and the Token Service verify the host's certificate before sending a credential. |
| Holder of a stolen enrollment token | Add an instance to that connector until the token expires or runs out of uses. | Anything beyond a stolen key's reach. | Tokens are short-lived and hashed at rest, and every enrollment raises a tenant event. |
| Compromised outbound gateway replica | Read SNI and authorities of connections through it. Drop or corrupt them. Claim attachment for any connector in `REG-SITEATTACH`, which draws streams it can only drop. Use a grant it received for its one stream instead of the pod (see "Residual risks"). Forge `outbound.*` evidence. | Create a grant. Send `globex`'s grant to `acme`'s agent, which refuses it. Read TLS contents. Impersonate an agent to another replica. Change keys or routes. Reach the Token Service or the API server. | C1, C2, C5, and C6. Key rotation and enrollment go to the API gateway and never pass through a replica. |
| Compromised `lenny-egress-authz` | Sign a grant for any connector, which gives TCP reach to anything a customer's allowlist admits. | Mint credentials, or pass the customer's allowlist. | This extends the bound the egress design states for a compromised policy decision point, and the "Decisions" section records it. |
| Compromised platform, seen by the customer | Reach the hosts on the customer's allowlist, at TCP level. | Reach anything else on the customer's network. | C3. The customer can also stop the agent at any time, which ends all access. |

## Build versus adopt

### Requirements an option must meet

An option must dial out from the customer, run its gateway inside the operator's cluster, isolate tenants within one gateway with a per-stream authorization the customer side can verify, accept a CONNECT from `lenny-egress` while resolving the name on the customer's side, and carry licence terms that allow a hosted service.

### Options

| Option | Model | Licence | Multi-tenant gateway | Result |
| :-- | :-- | :-- | :-- | :-- |
| [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) (vendor) | `cloudflared` "initiates an outbound connection" to Cloudflare's network, on [port 7844](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/) over QUIC or HTTP/2. | [`cloudflared` is Apache 2.0](https://raw.githubusercontent.com/cloudflare/cloudflared/master/LICENSE). The network is a commercial service. | [Virtual networks](https://developers.cloudflare.com/cloudflare-one/networks/virtual-networks/) isolate routes within one account. The search found no documented model for a provider holding many customers' tunnels. | Rejected as the platform mechanism. All traffic crosses a third party, which fails OR-9, and the gateway side is not the operator's to extend. |
| [Tailscale](https://tailscale.com/blog/multiple-tailnets-alpha) (vendor) | WireGuard mesh with a vendor coordination service. | The [client is open source](https://raw.githubusercontent.com/tailscale/tailscale/main/README.md); the licence name was not retrieved. | A tailnet per customer is described, and creating tailnets by API is in an alpha program. [Headscale](https://raw.githubusercontent.com/juanfont/headscale/main/README.md) implements "a _single_ Tailscale network" and calls itself suitable for personal use or a small open-source organisation. | Rejected. Each tenant would need a tailnet client inside the cluster, and the multi-tenant control plane is an alpha vendor service. |
| [frp](https://raw.githubusercontent.com/fatedier/frp/dev/README.md) | Client registers proxies on a server, by shared token or OIDC. | [Apache 2.0](https://raw.githubusercontent.com/fatedier/frp/dev/LICENSE). | `allowPorts` and per-proxy keys exist. The README describes no multi-tenant isolation, says it "is currently under development", and says version 2 will not be compatible with version 1. | Rejected. Its model exposes listeners on the server, and tenant isolation would be Lenny's to add to a project that plans an incompatible rewrite. |
| [chisel](https://raw.githubusercontent.com/jpillora/chisel/master/README.md) | TCP over HTTP, secured by SSH, with reverse forwarding through `--reverse`. | MIT. | Per-user address regular expressions in a `users.json` file, authenticated by password. | Rejected. A reverse remote makes the client open a listener on the server, which is what C4 forbids, and passwords are bearer secrets a compromised replica could replay. |
| [inlets](https://docs.inlets.dev/) (vendor) | Tunnels over a secure WebSocket. Inlets Uplink is described as a way to "onboard hundreds or thousands of tenants". | Commercial, by OpenFaaS Ltd. | Multi-tenant by description. The Uplink overview page returned 404, so its isolation was not read. | Rejected for now. It fails OR-9's licence terms unless the owner buys it, and it has no customer-side verification that this search could confirm. |
| [Teleport](https://goteleport.com/docs/reference/architecture/agents/) (vendor) | Agents establish "an SSH reverse tunnel to the Proxy Service", and the proxy routes by destination. | [AGPL source](https://raw.githubusercontent.com/gravitational/teleport/master/README.md). [Community builds](https://raw.githubusercontent.com/gravitational/teleport/master/build.assets/LICENSE-community) are limited to organizations with under 100 employees and under $10M revenue, and their security exception forbids embedding in products offered to third parties. | Clusters are the unit. The page describes no tenant model. | Rejected on licence and size. The tunnel model is the closest of the products here. |
| [Pomerium](https://www.pomerium.com/docs/capabilities/tcp) (vendor) | Tunnels TCP over HTTP CONNECT from a client helper to an upstream the proxy can route to. | [Apache 2.0](https://raw.githubusercontent.com/pomerium/pomerium/main/LICENSE). | Not established. | Rejected. The search found no outbound agent, so the proxy needs a route to the private host, which is the problem itself. |
| [HashiCorp Boundary](https://developer.hashicorp.com/boundary/tutorials/worker-management/hcp-manage-multi-hop) (vendor) | Workers chain through reverse-proxy connections, and a private worker dials upstream. | [BUSL 1.1](https://raw.githubusercontent.com/hashicorp/boundary/main/LICENSE). The grant excludes offering it to third parties "on a hosted or embedded basis" to compete with IBM's paid versions. | The search did not establish tenant isolation. The vendor documents that self-managed workers with HCP need the Enterprise binary, and that Community Edition is single-hop. | Rejected on licence risk and on the Enterprise requirement for the topology needed. |
| [Konnectivity](https://raw.githubusercontent.com/kubernetes-sigs/apiserver-network-proxy/master/README.md) | The agent dials the proxy over gRPC, and traffic is forwarded to it. Examples use mutual TLS, and [Kubernetes setup](https://kubernetes.io/docs/tasks/extend-kubernetes/setup-konnectivity/) uses a service account token. | [Apache 2.0](https://raw.githubusercontent.com/kubernetes-sigs/apiserver-network-proxy/master/LICENSE). | The README makes no multi-tenant statement. It serves one cluster's control plane, whose agents are the operator's own. | Rejected as a product. It is the closest technical precedent, and a time-boxed spike reads it for reusable code. |
| [Envoy reverse tunnels](https://www.envoyproxy.io/docs/envoy/latest/configuration/other_features/reverse_tunnel) | An initiator Envoy dials a responder Envoy, with node, cluster, and tenant identifiers. | Apache 2.0, as Envoy. | A tenant identifier exists for isolation. | Rejected for now. The page calls it "experimental and ... under active development", it would place Envoy in customer networks, and it has no customer-side grant check. It merits a later look, as agentgateway does in the egress design. |

### How other agent products reach private networks

- **Cursor** (vendor) [documents](https://cursor.com/docs/cloud-agent/security-network) published egress ranges, which it does not recommend as a primary control, and tunnel clients such as Tailscale userspace networking and Cloudflare Tunnel running in the agent environment. For self-hosted GitHub Enterprise Server and GitLab it [names](https://cursor.com/docs/cloud-agents/self-hosted) AWS PrivateLink or Cloudflare Tunnel. Its self-hosted workers open "a long-lived outbound HTTPS connection" and run tool calls in the customer's network. A [staff reply](https://forum.cursor.com/t/cloud-agents-corporate-network-access/164689) contrasts a tunnel that "keeps the runtime in our cloud and tunnels in" with a pool that "actually puts the runtime inside your network". In these documents the tunnel is the customer's own product, run beside the agent, and they describe no tunnel gateway that Cursor runs.
- **Snyk Broker** (vendor) is the nearest match for the customer side. The [client opens an outbound WebSocket](https://docs.snyk.io/~/changes/TzxJRMARwBuZNPcIpizY/enterprise-setup/snyk-broker/connections-with-snyk-broker), and an accept file limits requests, with [non-matching requests rejected](https://cdn.jsdelivr.net/npm/snyk-broker@5.22.12/README.npm.md). C3 follows this practice.
- **GitLab agent server** (vendor) [runs several replicas](https://docs.gitlab.com/administration/clusters/kas/) over gRPC or WebSocket, and its instances register their private addresses in Redis. `REG-SITEATTACH` follows the same practice.
- **Bito** (vendor) [asks the customer to allowlist its gateway addresses](https://docs.bito.ai/bito-dev-agents/ai-code-review-agent/getting-started/install-run-using-bito-cloud/guide-for-gitlab-self-hosted). The search returned no documentation of how Devin, Codex, or Copilot's coding agent reach a private network, so this design states nothing about them.

The tunnel-client-in-the-agent model does not fit Lenny. A tunnel client in the pod holds a tunnel credential within the agent UID's reach, which breaks P2 and P3. It needs outbound access that a `proxied` pod lacks, since a `proxied` pod reaches the internet only by CONNECT through `lenny-egress`, and `cloudflared` wants port 7844.

### Recommendation

Build a small Lenny-owned gateway and agent on grpc-go, which `go.mod` already carries at v1.80.0, and adopt no tunnel product. None of the sources retrieved documents the isolation requirement: in each, the gateway chooses which client's traffic a tunnel carries, and nothing at the customer's end verifies that choice against a third party. Lenny's requirement places that check at the customer's end of the tunnel, against a grant from a component the gateway cannot impersonate. The check is a small piece of code, and adding it to these products would need a fork of the open ones and is impossible on the hosted ones. The cost of building is that Lenny owns an internet-facing component that parses hostile input, plus a binary it ships to customers, which is why tier 9 carries a fuzz target and decision 15 asks for an external review.

A customer that already publishes a host through a tunnel product on a public address uses the tenant-registered public endpoint in the egress design, and needs no connector.

## Test of the egress design's claims

`egress-and-git-design.md`, under "Tenant outbound connector", says a connector "adds no Envoy route and changes no decision contract". Each part was read against that design and the tree.

| Claim | Verdict | Finding |
| :-- | :-- | :-- |
| No new Envoy route | Fails | The forwarded CONNECT route's cluster was tested with no TLS toward the upstream. "Transport security" leaves a chained upstream "as the deployer's upstream defines it", and the validation record shows no upstream TLS on chaining. The hop to the outbound gateway carries a credential and customer hostnames across the pod network, so it needs TLS with a verified name. TLS settings belong to the Envoy cluster, so this design assumes a new cluster, and the tunnel lifetime menu then needs one route per entry on it. A spike checks whether `transport_socket_matches` could select TLS on the existing cluster, which would remove the cluster and keep the routes. |
| No decision contract change | Holds on the wire, with changes behind it | The ext_authz response keeps its fields: a route header, an address, and credential headers. The service behind them changes. It gains a signing key, which qualifies its stated property that it "cannot mint". The address and credential on this route vary per CONNECT, while the egress design says the service "caches decisions per pod and host". And a route's address comes from `REG-SITEATTACH` rather than from a registration. |
| Typed route targets need no new field | Holds, with a rename | A stored kind spelled `connector` collides with §9.3. See "Changes requested". |
| The name is never resolved on the platform side | Holds | Envoy's forwarded route uses `ORIGINAL_DST` rather than the dynamic forward proxy, and `netagent` does not resolve the CONNECT target. Private literals stay refused by the decision service, so a connector host is always a name. |
| A selector peer on the `lenny-egress` policy | Holds | "Network policy and placement" gives this form for in-cluster services. Here the decision pins a replica's pod address, which the selector peer covers because policy evaluates after Service NAT. |
| The per-kind validator | Holds | "Which hosts a connector may serve" states the rule for this kind. |
| Per-endpoint CAs act in `netagent` | Holds if the record is generic | A VCS pool on a private CA has no binding to carry a CA, so the outbound host must be an endpoint registration. "Tenant-registered upstreams and git endpoints" reads as git-specific and upstream-specific, and a registry behind a connector is neither. See "Changes requested". |
| `allowedRefPatterns` and `credentialPinned` work | Holds | Both act in `netagent`, before the CONNECT. |
| The server-name check | Gap | The check runs on an internal listener that forwards with `sni_dynamic_forward_proxy`, so the forwarded route does not run it. The outbound gateway and the agent perform it instead (C6). |
| Mints through `lenny-egress` | Holds, with a new consequence | Mints work, but a `mint-coarse` token "revoked at session end" cannot be revoked while the connector is offline, and the egress design has no retry for that revocation. |
| The dependency table (OC-1 on EG-2, OC-2 on EG-8 and EG-9) | Incomplete | The forwarded CONNECT route, selector peers, and per-endpoint CAs land in EG-6, the egress PKI in EG-11, and the envelope in EG-10. |

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
                                               |                                       ^ reads
                                               |                                 REG-SITEATTACH <-- replicas write
```

Grant verification happens at both ends of the tunnel. The replica checks it to refuse garbage cheaply, and the agent checks it because the replica is outside the customer's trust.

### Agent protocol and authentication

- **Transport.** The agent dials the outbound gateway's public host on TCP 443 with TLS 1.3, directly or through `HTTPS_PROXY` by CONNECT. It verifies the server name, against the system roots or a CA bundle from its enrollment response.
- **Identity.** The agent generates an Ed25519 key at enrollment and presents a self-signed client certificate carrying it. The private key never leaves the host. The replica requires a client certificate and looks up the SHA-256 of its public key in a snapshot of `outbound_agent_instance`, which it refreshes by delta poll each second. There is no CA for agents, so a CA key cannot be stolen, and revoking is a row update. A bearer token was rejected because any replica it crossed could replay it elsewhere.
- **Roles reversed after the handshake.** Once TLS completes, the agent serves gRPC on the connection it dialed, and the replica acts as the gRPC client over that connection. Under [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.txt), only a client initiates ordinary streams, and a server can initiate only through `PUSH_PROMISE`, which `SETTINGS_ENABLE_PUSH` set to 0 forbids. C4 therefore rests on the protocol's roles rather than on code that refuses a request. A spike confirms that grpc-go serves this arrangement and what it sends for that setting.
- **Services.** `CH-HOSTTUNNEL` is a bidirectional `Tunnel` RPC per stream, with the grant in metadata, a first reply carrying an outcome class, and byte frames of at most 64 KiB. `CH-SITESTATUS` is one long-lived bidirectional `Status` RPC, on which the agent sends version, instance identifier, clock reading, allowlist digest, open tunnels, and dial failure counts by class, at most every 10 seconds, and the replica sends its replica identifier and a minimum version. Free text is absent from both, so a hostile agent has no string to place in a log or a read. A proto under `schemas/` carries the contract, with a row in §28.7 Wire-contract artifact register.
- **Liveness.** gRPC keepalive runs at 10 seconds with a 10-second timeout from the replica's side.
- **Intercepting proxies.** A customer egress proxy that intercepts TLS breaks the client certificate. The outbound gateway uses a dedicated hostname so that the customer can exempt it. See "Decisions".

### Grants

A grant is `base64url(claims) "." base64url(signature)`, Ed25519-signed, with these claims:

| Claim | Meaning |
| :-- | :-- |
| `ver`, `kid` | Format version and signing key identifier |
| `tid`, `cid` | Tenant and connector |
| `aut` | Authority, lower-case `host:port` |
| `did`, `sid` | The `egress.decision` identifier, and the session where the decision has one |
| `jti`, `iat`, `exp` | Unique identifier, issue time, and expiry, 60 seconds after issue |

A grant is separate from a Lenny JWT, because §10.2 Authentication requires every Lenny-issued JWT to use ES256 through `JWTSigner` and `pkg/kms` signs through a provider call per token, which does not fit a per-CONNECT rate.

- **Issue.** The decision service caches its policy outcome as it does today, and on an allow whose route names an outbound host it signs a fresh grant on each ext_authz call. Envoy has no ext_authz response cache (round 5 of the egress validation), so every CONNECT reaches the service. The grant travels as the injected credential, `Proxy-Authorization: Basic base64("grant:" grant)`, replacing the pod's, which the service already strips on every chained route.
- **Keys.** A signing key per deployment lives in a Secret mounted into `lenny-egress-authz` only. The public key set is in a ConfigMap for replicas, and agents fetch it from the API gateway through `GET /v1/outbound-agents/trust` every 10 minutes and receive it at enrollment. A compromised replica cannot alter it, because it travels on a path no replica touches. A new key is published 7 days before use.
- **Replica check.** Signature, expiry, tenant and connector known, and authority equal to the CONNECT authority. A failure answers 407, which `custom_response` maps to 502 with the existing alert, because it means a platform fault or an attack on the hop.
- **Agent check.** Signature against its trust set, `tid` and `cid` equal to its own, `iat` and `exp` within a 120-second skew, `jti` unseen, and `aut` within its local allowlist. It holds seen `jti` values until they can no longer pass the expiry check. At the default rate of 50 new streams per second the cache holds about 9,000 entries.
- **Clock.** A skewed agent clock refuses grants. The `Status` RPC's clock reading gives the replica an offset, which the liveness read reports as `clock_skew`, so the tenant admin sees the cause.

### How a CONNECT reaches the right connector

1. A platform-side client sends `CONNECT gitlab.acme.internal:443` to `lenny-egress`, with its token, as on any other route.
2. `lenny-egress-authz` computes the effective policy. The first matching tenant route names an outbound host, whose connector the tenant must hold. Unbound warm pods have no tenant, and the platform default route cannot name a connector, so neither can reach one.
3. It selects a replica from its in-memory copy of `REG-SITEATTACH`, preferring one with a live session for that connector and the fewest attached streams. It signs a grant and returns the route for the pool's tunnel cap, the replica's pod address, and the credential.
4. Envoy dials that address over TLS, verifying the outbound gateway's egress PKI leaf, and forwards the CONNECT.
5. The replica checks the grant and picks the least-loaded instance attached to it. It opens `Tunnel`, and the agent checks the grant, resolves the name, checks the resolved address against `denyCIDRs`, dials the pinned result, and answers with an outcome class.
6. On success the replica answers 200, and bytes flow. Both ends read the first bytes, require a ClientHello whose server name equals `aut`, and close the tunnel otherwise. Encrypted Client Hello is the same documented limit as in the egress design.
7. If an instance reports a dial failure, the replica tries one other attached instance within the budget, because instances can see different parts of a network.

If no replica has a live session, the decision pins any live replica, which answers 503 and records `no_live_agent`, so the failure has a single reporting path. If no replica is alive, the decision denies with status 503. A resolver attempt must get its CONNECT reply in 1.5 seconds, so step 5 sits inside that budget. A spike measures this over a wide-area path (see "Spikes owed").

**`REG-SITEATTACH`.** Postgres, through the pooler. Each replica writes one row per agent session when it opens or closes, and one heartbeat row every 5 seconds. A replica with a heartbeat older than 15 seconds counts as dead. The decision service polls changes each second with one query per decision service replica, because the egress design records that `LISTEN`/`NOTIFY` is unavailable through the pooler (§12.6 Interface Design). Staleness therefore costs 1 to 2 seconds of 503s after a crash, and a graceful drain removes rows first and costs none. Redis was not chosen because the egress design gives `lenny-egress-authz` no Redis path and the write rate is low. The table is platform-scoped, because the handshake lookup has no tenant context, as the egress design's service-mode pin record is.

### Per-tenant isolation at the gateway

OR-4 holds through these layers:

- **Route ownership.** The decision refuses a route naming a connector the tenant does not hold. This is the egress design's rule for upstreams and endpoints.
- **Grant binding.** `acme`'s agent refuses anything but a grant naming `acme` and its own connector. A compromised replica holding `globex`'s grant cannot pass it through `acme`'s agent, and it cannot make a grant.
- **Accounting.** Each replica keeps streams, buffers, and quota counters per tenant, and a session belongs to one connector for its life.
- **No shared credential.** A replica presents no credential to the agent that could carry between tenants, since each agent authenticates the replica by its public TLS name and each stream by its grant.
- **End-to-end TLS.** A replica sees no plaintext for any tenant (C6).
- **Exclusive gateway groups (OC-4).** A platform admin can assign a tenant a dedicated `lenny-outbound-gateway` Deployment with its own public host, as egress groups do for `lenny-egress`. A shared replica then never holds that tenant's sessions or grants, which closes the splice residual below for that tenant.

One case remains on a shared replica. A compromised replica holds a valid `acme` grant for the seconds between receiving it and using it. It can use that grant for its own bytes in place of the pod's, which gives TCP-level reach to that one host and port, once, with no credential, and only for CONNECTs `acme`'s pods make. It could carry another tenant's bytes on that stream. Neither the agent nor the customer's host can tell, because binding the inner bytes to the grant would need `netagent`, the resolver, the mirror, and the Token Service to speak a second protocol to the agent. "Residual risks" and "Decisions" record this.

### Which hosts a connector may serve

A host is reachable through a connector only when every party below agrees.

| Party | Decides | Where | Enforced by |
| :-- | :-- | :-- | :-- |
| Platform admin | Whether the tenant may have connectors, its quotas, the reserved host list, and each pool's flag | Admin API and Helm | API validators and the decision service |
| Tenant admin | Which hosts route to which connector, and which of those the tenant's policy allows | Admin API | The decision service |
| Customer network owner | What the agent dials | The agent's local file | The agent |

**Validator for the `outboundConnector` kind.** It replaces the public-address check, per the hook in EG-2. It accepts a lower-case DNS name or `*.suffix`, with ports defaulting to `[443]`. It refuses:

- IP literals, and any name the host grammar of the egress design's I4 refuses.
- `localhost`, cluster suffixes, and the metadata names in the chart's list.
- Single-label names, unless the platform admin allows them.
- A pattern wider than a registrable suffix, such as `*.com`.
- Any name in the reserved list, `outboundGateway.reservedHostSets`, which defaults to every shipped host set, every provider's declared mint endpoint, and the gateway's own hosts. This is what keeps a tenant's connector from receiving traffic bound for a host where a platform-owned credential could travel (C8).

No proof of domain control applies. A route affects only the tenant's own pods, so two tenants can both claim `gitlab.internal`, and a claim to a public name in the customer's own domain, such as a split-horizon `git.acme.com`, is ordinary.

**Pool ceilings.** The formulas in "Effective policy" read a ceiling with the tenant's pool overlay applied. On a shared pool with a named ceiling, an outbound host falls outside it, and `narrowOrExtend` would let the tenant extend to any public host as well. This design adds an overlay flag, `tenantOverride.outboundHosts`, which lets the overlay add only hosts routed to the tenant's own connector. It defaults to `deny`, following the egress design's narrow default. A ceiling of `*` matches these names as it matches any name, and the egress design's SSRF reasoning does not apply to them because they are never resolved on the platform side. The tenant's policy must still allow the host, and the effective-policy read names which layer refused.

**TLS only.** Both ends enforce C6, so a plain-HTTP internal host must be fronted with TLS by the customer. Non-TLS protocols and SSH are absent from this design, and the egress design's `sshForms` rewrite already turns SSH remotes into smart HTTP.

**Private CAs.** The outbound host record carries `caBundle`. `netagent`, the mirror, the resolver, and the Token Service use it for that host alone, on the existing I8 path. Tools in an opaque tunnel use the pod's trust, so a private-CA host works on a shared pool only for traffic `netagent` terminates. See "Decisions" for an anonymous registry on a private CA.

### Registration, rotation, and revocation

**API.** All routes follow §15.1 REST API conventions (`If-Match`, `dryRun` with no outbound calls, and tenant scoping by RLS). The write path is EG-9's single opt-in path, so SO-2's guardrails and audit apply.

| Route | Caller | Purpose |
| :-- | :-- | :-- |
| `POST`, `GET`, `DELETE /v1/admin/outbound-connectors[/{name}]` | Tenant admin | Create, read with liveness, and delete with revocation |
| `POST /v1/admin/outbound-connectors/{name}/enrollment-tokens` | Tenant admin | Issue a token, returned once, with an expiry of 1 hour by default and a use count of 1 by default |
| `GET`, `DELETE /v1/admin/outbound-connectors/{name}/instances[/{id}]` | Tenant admin | List and revoke instances |
| `POST`, `GET`, `DELETE /v1/admin/outbound-hosts[/{name}]` | Tenant admin | Register a host for a connector, with ports and `caBundle` |
| `POST /v1/admin/outbound-connectors/{name}/probe` | Tenant admin | Live path test, limited to 10 per minute per connector, like `/v1/admin/connectors/{name}/test` |
| `PUT /v1/admin/tenants/{id}/outbound-grant` | Platform admin | Grant the feature, and set quotas |
| `POST /v1/outbound-agents/enroll` | Agent, with a token | Register a public key, and receive the connector identity, the host names, and the key set |
| `POST /v1/outbound-agents/rotate` | Agent, signing the request with its current key | Register a new public key |
| `GET /v1/outbound-agents/trust` | Agent, with a signed request | Fetch the grant key set |

The agent routes run on `lenny-gateway` behind the existing Ingress, which terminates TLS, so they authenticate by a signature inside the request over the instance identifier, a timestamp, and a nonce. Because they use the API host, a customer proxy may inspect them. Permissions are named: `outboundConnector.manage` and `outboundConnector.read`, held by `tenant-admin` by default, so a new role is a new grant with no handler change.

**Credential lifecycle.**

- **Enrollment.** Each instance enrolls with its own key, so one instance can be revoked and attribution names one instance. A token with a use count above 1 serves an autoscaled Deployment, and it is capped by the instance quota. The token is hashed at rest, and each use raises `outbound_connector.instance_enrolled`.
- **Rotation.** The agent rotates its key at 30 days, overlapping for 1 hour. A key older than 90 days is refused, and the instance must re-enroll. An agent container therefore needs persistent storage for its key, and the agent's Helm chart renders a StatefulSet.
- **Revocation.** Revoking an instance or a connector updates its row and increments the policy version, so the next decision refuses at once. Each replica sees the row within its poll and closes the sessions, which ends their open tunnels. The target is 5 seconds end to end, which tier 4 pins. Removing the platform grant, suspending the tenant, and tenant deletion under §12.8 Compliance Interfaces have the same effect.
- **Stale snapshot.** A replica whose snapshot is over 60 seconds old refuses new sessions, and over 5 minutes old closes all sessions, so a Postgres outage bounds how long a revoked key stays attached.
- **Duplicate use.** One key on more sessions than its configuration allows, or from a second source address, raises `outbound_connector.duplicate_instance`. It does not block, because an egress NAT pool can produce the same pattern.
- **Customer control.** Stopping the agent or blocking its outbound rule ends everything, with no platform involvement.

### High availability

- **Agent.** The documentation asks for at least two instances. Each holds sessions to two distinct replicas by default (`replicaSessions`). It learns a replica's identifier from `Status`, and it reconnects with jitter until it has distinct ones or has reached a try limit, because a load balancer gives no choice of backend. Teleport's reverse tunnels are the [vendor precedent](https://goteleport.com/docs/reference/architecture/agents/) for this connection model. Keeping every replica attached is unnecessary because the decision pins a replica holding a session, so replicas do not forward to each other.
- **Outbound gateway.** Three replicas by default, a PodDisruptionBudget, and required anti-affinity against the Token Service, the decision service, and the mirror, on the egress design's reasoning for a component that parses hostile input, relaxed under `global.devMode`. A drain removes its rows, fails readiness, sends `GOAWAY`, waits for agents to reattach elsewhere, and lets tunnels finish until the drain budget ends.
- **Agent draining.** On `SIGTERM` an instance reports draining, takes no new streams, and lets open tunnels finish for `drainSeconds`.
- **What breaks.** A crash ends its tunnels, as an Envoy crash does in the egress design. The mirror's and the resolver's retry rules apply. A `git` run in a pod fails and the agent's process retries it or reports it.

### Liveness surfaced to the tenant

`GET /v1/admin/outbound-connectors/{name}` returns a computed state with reasons, and each instance's version, last status time, replica sessions, source address, clock offset, allowlist digest, and recent dial failure counts by class.

| State | Meaning |
| :-- | :-- |
| `pending` | No instance has enrolled. |
| `connected` | At least two instances each have live sessions, and no reason below applies. |
| `degraded` | One instance, one replica session, a clock skew at or above 60 seconds, a version below the recommended one, a host in the routes that the agent's reported allowlist omits, or a dial failure rate above a threshold. |
| `offline` | No live session for 60 seconds, a hold-down that keeps an agent restart from notifying. |
| `revoked` | Revoked by a tenant or platform admin. |

The gateway sweeper, which already runs for `REG-COORDMIRROR`, computes transitions and emits `outbound_connector.offline`, `.restored`, and `.degraded` on §25.5 Operational Event Stream, which is the tenant subscription path the egress design names. The allowlist drift check needs the host list, so the agent reports it by default, and `reportAllowlist: false` limits it to a digest for a customer that prefers not to share internal names.

Operator alerts cover the shared service: replica availability, grant verification failures, capacity, and handshake rate. A tenant's connector going offline raises no operator alert, because the customer owns that agent, and the resolver's existing proxy 502 alert does not fire for it because the failure mapping below uses 503.

### Quotas (P7)

Values are initial defaults, which the load tier confirms.

| Quota | Scope | Default | Enforced by | At the limit |
| :-- | :-- | :-- | :-- | :-- |
| Handshakes per second, before authentication | Source address and replica | 5 and 200 | Replica | Connection refused |
| Connectors | Tenant | 5 | API | 409 |
| Instances | Connector | 10 | API | 409 |
| Sessions | Instance | 4 | Replica | Session refused, with a retry hint |
| New streams per second | Connector | 50 | Decision service, with its other rates | CONNECT 429 |
| Concurrent streams | Connector | 256 | Replica | CONNECT 429 |
| Throughput | Tenant, per replica | Chart value | Replica | Flow control window held |
| Per-session HTTP/2 limits | Session | Header size, frame rate, and buffered bytes | Replica | Session closed |
| Status messages | Session | One per 10 seconds, 64 KiB | Replica | Session closed |
| Probes | Connector | 10 per minute | API gateway | 429 |

Per-replica limits divide a tenant's share by live replicas, so a total limit stays approximate during a rollout. The chart refuses a configuration whose per-tenant sums exceed a replica's connection and memory limits, as it does for Envoy's breakers. Idle tunnels fall under the pool's tunnel lifetime menu and `stream_idle_timeout`. The mirror's fetches and mints count against the same connector quotas, so a tenant's prefetch cannot harm another tenant, only itself. Bytes per tenant are metered, because the operator pays for this traffic in both directions.

### Evidence from the gateway

These are new streams on the egress design's envelope, and they need no change to any existing stream.

| Stream | Producer | Provenance | Payload |
| :-- | :-- | :-- | :-- |
| `outbound.session` | Replica | `enforced` | Connector, instance, replica, source address, key hash, open and close, close reason, and enrollment or rotation references |
| `outbound.stream` | Replica | `enforced` | Decision identifier, grant `jti`, connector, instance, authority, start, end, bytes in each direction, and outcome |
| `outbound.agent` | Replica, relaying the agent | `self_reported` | Decision identifier, dial class, duration, resolved address, and agent version |

`did` in the grant joins `outbound.stream` to `egress.decision` and `egress.tunnel`, whose upstream address here is the replica. The agent's claims are in their own stream, so a consumer knows what only the customer's agent asserts. `lenny-evidence` admits the gateway's SAN for these three streams alone. The agent writes a local JSON log of every grant it accepts or refuses, with `did`, `sid`, `jti`, authority, resolved address, outcome, and bytes in each direction. A customer's security team therefore has a record outside the platform's control and can compare it with the export. A compromised replica can forge `outbound.*` records, which is the same stated limit as a compromised mirror's `git.operation` records. Agent identity changes and administrative writes go to §11.7 Audit Logging, and §16 retention presets apply per stream. The egress design computes its manifest bound for its own streams, so sizing adds these for tenants that hold a connector.

### Failure mapping

The replica chooses its answer so that the egress design's resolver and mirror rules classify each failure correctly, with no change to them.

| Condition | CONNECT reply | Resolver | Mirror store fallback | Reported as |
| :-- | :-- | :-- | :-- | :-- |
| No replica alive | 503 from the decision | `GIT_CLONE_REF_RESOLVE_TRANSIENT` | Yes | `no_replica` |
| Connector offline | 503 | Transient | Yes | `no_live_agent` |
| Envoy cannot reach the pinned replica | 503 from Envoy | Transient | Yes | Envoy access log |
| Agent dial failed: DNS, refused, or timeout | 503 | Transient | Yes | `dns_*`, `tcp_refused`, or `tcp_timeout` |
| Agent refused a resolved address under `denyCIDRs` | 403 | `GIT_CLONE_REF_UNRESOLVABLE`, `egress_denied` | No | `address_denied` |
| Host absent from the agent's allowlist | 403 | Unresolvable, `egress_denied` | No | `not_in_local_allowlist` |
| Agent refused the grant for clock skew or unknown key | 503 | Transient | Yes | `grant_refused_by_agent` |
| Replica refused the grant | 407, mapped to 502 | Transient, with the alert | No | `grant_refused_by_gateway` |
| Quota | 429 | Transient | No | `quota_*` |
| Certificate failure after 200 | TLS failure in the client | Transient, which this design proposes because the egress design's resolver error list omits this case | Yes, under its "failed connection or TLS handshake" cause | `egress.local` and the client's error |

The bodies are fixed and carry no agent-supplied content. The decision identifier travels in the existing refusal, so an agent or user who sees a failure has a value to give the tenant admin.

### What happens to the git mirror

The egress design's routing rule settles most cases, because a `mint-coarse` or `stored` provider is never served through the mirror. A self-managed GitLab, Gitea, Forgejo, or generic host behind a connector therefore always routes `direct` through `lenny-egress` and the connector, and the mirror refuses it.

That leaves GitHub Enterprise Server, which is `mint-scoped`. The mirror can fetch it through a tenant route, into a store keyed by the tenant, under the existing rule that any tenant route gets a tenant-keyed store. The recommendation is that it stays off until the tenant asks: a git route naming the mirror is refused for an outbound host unless the host sets `allowMirror: true`, because the customer chose a connector to keep its code on its own network, and the mirror holds fetched contents centrally. With the opt-in, the store follows the tenant's `dataResidencyRegion` and §12.8 purge, its fetches count against the connector's quotas, and an offline connector takes the store fallback within its age limit.

Consequences without the mirror: a tenant-owned pool's warm fill comes from the pod through the connector, each pod fetch crosses the customer's link, and an offline connector stops clones.

### DNS inside the customer network

The agent resolves names on the customer's network. The platform never sees or resolves them, and it never reads the customer's zone. By default the agent uses its host's resolver, so split-horizon and search-domain behaviour match its host. `dns.servers` overrides the servers, and `hosts` pins a name to an address for a host DNS does not cover. After resolving, the agent checks each address against `denyCIDRs`, which defaults to loopback, link-local (including `169.254.169.254` and `fd00:ec2::254`), and the agent host's own addresses, and dials the address it checked, so a rebinding answer cannot change the target. Private ranges are allowed, since reaching them is the agent's purpose.

The customer owns name resolution, routing, and the host's certificate, and Lenny owns reporting which of them failed. Instances can answer differently, so a probe and the dial failure counts report per instance.

### How a tenant admin debugs a failure

The refusal the user saw carries a decision identifier. From it, or from a host name:

1. `lenny-ctl outbound status acme-dc1` reads the liveness state and reasons.
2. `lenny-ctl outbound probe acme-dc1 gitlab.acme.internal:443` runs the path live and reports each stage. The API gateway sends the probe through `lenny-egress` on its own identity with the tenant asserted, as the resolver does, and performs a TLS handshake against the host's CA with no credential.
3. `GET /v1/egress/decisions?host=gitlab.acme.internal` and the effective-policy read show policy outcomes and which layer decided.
4. On the customer's host, `lenny-outbound-agent check gitlab.acme.internal:443` runs the local stages with no platform involved, and the agent's log has the `did`.

| Stage | Failure | Who fixes it |
| :-- | :-- | :-- |
| Effective policy | Host outside the ceiling, tenant policy, or session list | Tenant admin, or the platform admin for the pool flag |
| Route | No outbound host for the name, or a route to `direct` | Tenant admin |
| Attachment | `no_live_agent` | Whoever runs the agent. The status read shows whether it ever attached, and its source address. |
| Grant | `grant_refused_by_agent` with `clock_skew` | Whoever runs the agent's host |
| Local allowlist | `not_in_local_allowlist` | Customer network owner |
| Name | `dns_nxdomain` or `dns_timeout`, per instance | Customer network owner |
| Connection | `tcp_refused`, `tcp_timeout`, or `address_denied` | Customer network owner |
| Certificate | Chain or name failure, with the issuer and names presented | Tenant admin for `caBundle`, or the host's owner |
| Server name | ClientHello name differs from the authority | The client's owner; a tool sending another name is refused by design |

The probe records `egress.decision` like any other CONNECT. It needs the decision service to honour the API gateway's tenant assertion for an outbound host that is not a git host, which is listed under "Changes requested".

### Deployment and network rules

- **Disabled by default.** `outboundGateway.enabled: false` renders nothing, and the validators refuse the `outboundConnector` kind. Until OC-3 lands, the chart refuses `true` outside `global.devMode`, so no public surface runs without tenant quotas and evidence.
- **Public exposure.** A dedicated hostname on a Layer 4 `LoadBalancer` Service. The existing Ingress terminates TLS, and it cannot pass a client certificate, so it does not serve this path. The chart has no `LoadBalancer` Service today, so this is a new kind of exposure that `docs/operator-guide/ingress-and-tls.md` covers. A deployer-supplied Secret holds the public certificate, as for the Ingress, and an egress PKI leaf serves the internal CONNECT listener.
- **Placement.** The `lenny-system` namespace, with `lenny.dev/component: outbound-gateway` per NET-047, `automountServiceAccountToken: false`, and the placement rule that keeps it off nodes tainted for agent isolation.
- **Network rows in §13.2.4 `lenny-system` Namespace NetworkPolicies.** Egress: PgBouncer, `lenny-evidence`, and `kube-system` CoreDNS. Ingress: the agent port from an external `ipBlock`, the CONNECT port from `lenny-egress` pods and egress groups, and the metrics port from the scrape source. `lenny-egress` and PgBouncer gain the matching rows. The outbound gateway has no rule toward the Kubernetes API, agent namespaces, the Token Service, or the decision service, which is what C4 and C5 rest on.
- **Store access.** A database role reads `outbound_agent_instance` and writes `REG-SITEATTACH` alone, through the pooler.
- **Envoy.** One `ORIGINAL_DST` cluster with an upstream TLS context verifying the outbound gateway's Service name, and one route per tunnel lifetime entry, with `custom_response` as on chained routes. A spike confirms this combination.
- **Level 2.** A replacement proxy must set `centralProxy.level2.pinnedDestinations: true` and a new `centralProxy.level2.outboundUpstreamTLS: true`, asserting that it dials the pinned address, injects the credential, and verifies the name. **Level 1** remains refused, as the egress design states.
- **Agent distribution.** A statically linked Linux binary for amd64 and arm64, an image, and a Helm chart. Its source sits in this repository under the project's licence, so a customer can audit it. It does not update itself, because a customer runs it under its own change control, and the status read reports versions below the recommended one.

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `outboundGateway.enabled`, `.replicas`, `.image` | Installs the outbound gateway and sets its replica count and image. | Deployment | Platform admin (Helm) |
| `outboundGateway.publicHost`, `.service`, `.publicTLS.secretName` | Sets the hostname agents dial, its load balancer Service, and its public certificate. | Deployment | Platform admin (Helm) |
| `outboundGateway.limits.*` | Sets handshake rates, per-replica session and memory limits, and per-session HTTP/2 limits. | Deployment | Platform admin (Helm) |
| `outboundGateway.reservedHostSets` | Lists host sets and names no outbound host may cover. It can be extended, and never reduced below the shipped list. | Deployment | Platform admin (Helm) |
| `outboundGateway.allowSingleLabelHosts` | Permits names such as `gitlab` for customers without a domain. | Deployment | Platform admin (Helm) |
| `outboundGateway.grantKey.*`, `.minAgentVersion`, `.recommendedAgentVersion` | Sets signing key Secret and rotation timing, and the agent versions refused and flagged. | Deployment | Platform admin (Helm) |
| `centralProxy.level2.outboundUpstreamTLS` | Asserts that a level 2 proxy can reach the outbound gateway on this route. | Deployment | Platform admin (Helm) |
| Exclusive gateway groups (OC-4) | Names dedicated outbound gateway Deployments, each with its own public host, and assigns one to a tenant. | Deployment (group), tenant (assignment) | Platform admin |
| `tenantOverride.outboundHosts` | Lets a tenant's overlay add hosts routed to its own connector to the pool ceiling. | Pool | Platform admin |
| Outbound grant and quotas | Grants the tenant the feature, and sets its connector, instance, stream, and throughput limits. | Tenant | Platform admin (admin API) |
| Outbound connectors | Creates, reads, and revokes the record for one customer network. | Tenant | Tenant admin |
| Enrollment tokens | Issues a token with an expiry and a use count, so a customer can enroll an instance. | Connector | Tenant admin |
| Instances | Lists and revokes individual agent instances. | Connector | Tenant admin |
| Outbound hosts: `host`, `ports`, `caBundle`, `allowMirror` | Names a host a connector serves, the CA that validates it, and whether the mirror may fetch it. | Tenant | Tenant admin |
| Egress routes and tenant egress policy | Routes a host to an outbound host, and allows it, as in the egress design. | Tenant | Tenant admin |
| VCS credential pools and credential bindings | Name credentials for the host, as in the egress design. | Tenant | Tenant admin |
| Session `egress.allowedHosts`, `gitClone`, and bindings | Name what a session uses, as in the egress design. A session cannot name a connector. | Session | Client |
| `allow` | Lists hosts and ports the agent dials. Empty until set, so an unconfigured agent refuses every stream. | Agent instance | Customer network owner (local file) |
| `denyCIDRs` | Lists resolved addresses the agent refuses, added to the defaults. | Agent instance | Customer network owner |
| `dns.servers`, `hosts` | Override the resolver, or pin a name to an address. | Agent instance | Customer network owner |
| `replicaSessions`, `drainSeconds`, `dialTimeoutSeconds` | Set attachment count, shutdown wait, and dial bound. | Agent instance | Customer network owner |
| `proxy`, `reportAllowlist`, `log.path` | Set the customer's outbound proxy, whether host names are reported, and the local grant log location. | Agent instance | Customer network owner |

## Scenarios

Tenant `acme` runs a self-managed GitLab at `gitlab.acme.internal` on a private network, with a certificate from Acme's private CA and no inbound firewall rule. Alice is Acme's tenant admin, and Bob runs Acme's network. Pool `acme-coding` is tenant-owned, so the profile can set `GITLAB_HOST`.

**Setup.**

- The platform admin grants `acme` the feature, and the chart has `outboundGateway.enabled: true`.
- Alice runs `lenny-ctl outbound create acme-dc1` and issues an enrollment token. Bob installs the agent twice with the token, sets `allow` to `gitlab.acme.internal:443`, and opens outbound 443 to `api.lenny.example` and `outbound.lenny.example`. The status read shows `connected`.
- Alice registers outbound host `acme-gitlab` for `gitlab.acme.internal`, with Acme's CA in `caBundle`, and adds an egress route to it. She adds the host to the tenant's always-allowed hosts, and to the pool's overlay, because the pool's ceiling is named.
- Alice creates a GitLab VCS credential pool with repository prefix `gitlab.acme.internal/acme/`. The provider is `mint-coarse`, so the git route stays `direct`. She adds an `inject` binding on `gitlab.acme.internal` whose secret is that pool.

**Clone.** A session of Alice's clones `gitlab.acme.internal/acme/web`.

1. At plan bind, the API gateway resolves the ref through `lenny-egress` on its own identity, carrying `acme`. The decision pins replica r1 and signs a grant. r1 opens `Tunnel` to Bob's instance, which verifies the grant, checks `allow`, resolves the name with Acme's DNS, and dials. The API gateway validates the host's certificate against `caBundle`, attaches a read token minted as below, and sends `ls-refs`.
2. The pod's adapter clones the resolved SHA through `netagent`'s local git endpoint. `netagent` fetches a read token and the CA on I8, sends `CONNECT gitlab.acme.internal:443` to `lenny-egress`, and proceeds as above with a new grant for this stream. The clone's bytes are TLS to the host the whole way.
3. `egress.decision`, `egress.tunnel`, `outbound.stream`, `outbound.agent`, and `git.operation` all carry the same decision identifier or session. Bob's agent log has the same `did`.

**Mint.** The first read token is a GitLab project access token. The Token Service checks both gates, then goes through `lenny-egress` on its own identity with `acme` asserted, which the decision honours because the host is in an `acme` VCS pool. It validates the host's certificate against `caBundle` before it sends the stored GitLab credential, then calls GitLab's project access token API. The stored credential stays in `lenny-credentials`, and neither the replica nor Bob's agent sees it in clear. At session end the Token Service revokes the token over the same path.

**Push.** `git push origin fix-login` reaches `netagent`, which reads `git-receive-pack`, applies `allowedRefPatterns`, and asks I8 for a write token. The Token Service checks the writable set, mints through the connector, and `netagent` forwards the push under a new grant. A push to `acme/payments`, which is outside the session's writable set, stops in `netagent`, and no stream is opened.

**`inject`.** The agent runs `glab mr create`. The profile sets `GITLAB_HOST=gitlab.acme.internal` and `GITLAB_TOKEN=lenny-injected`. `glab` sends HTTPS through `HTTPS_PROXY`, trusting the per-pod CA. `netagent` terminates TLS, overwrites the header with a minted token, and opens its own TLS to the host through the connector, validating against the binding's or host's CA.

**Connector offline.** Bob's instances lose their link in the middle of a session. Open tunnels end, and the next `git fetch` gets a CONNECT 503 with a decision identifier. After 60 seconds Alice receives `outbound_connector.offline`. A new session's plan bind fails with `GIT_CLONE_REF_RESOLVE_TRANSIENT`. The GitLab token of a session ending in this window cannot be revoked until the link returns (see "Changes requested"). No traffic takes the default route.

**A compromised replica and `globex`.** Replica r1 is compromised, and tenant `globex` has its own connector attached to r1. The attacker holds `globex`'s and `acme`'s grants as CONNECTs arrive. It presents a `globex` grant to `acme`'s agent, which refuses it, since `tid` and `cid` differ, and logs a refusal. It invents a grant, and the signature check fails. It replays an `acme` grant, and the `jti` check fails. It takes an `acme` grant before the stream opens and sends its own bytes to `gitlab.acme.internal:443`: this succeeds once, at TCP level, with no credential, and appears as an ordinary stream with no matching client traffic at Envoy's byte counts. A dedicated gateway group removes this for `acme`.

**A stolen agent key.** An attacker copies an instance's key from Bob's host. It attaches from another address, and Alice gets `outbound_connector.duplicate_instance`. It draws some of `acme`'s streams and answers with its own TLS endpoint. `netagent` refuses the certificate, sends no token, and git fails. Alice revokes the instance, and its sessions close within the target of 5 seconds. Bob's other instance serves the traffic.

**A hostile agent.** Tenant `globex` modifies its agent. It tries to open a stream to the replica, and HTTP/2 refuses it as a protocol error, ending its session. It sends frame floods, and the per-session limits close the session. It returns a poisoned pack on a repository, which is `globex`'s own, and `transfer.fsckObjects` in the pod applies as for any host. It reports `dns_nxdomain` falsely, and only `globex`'s `self_reported` stream records it.

**A DNS failure.** Bob moves GitLab, and one instance's resolver lags. Carol, a user, sees a CONNECT failure with a decision identifier. Alice runs the probe, which reports the first instance passing all stages and the second reporting `dns_nxdomain`. The replica retried the first instance, so most streams succeed, and the status read is `degraded`. Alice passes the output to Bob, and `lenny-outbound-agent check` on that host confirms it.

**GitHub Enterprise Server with the mirror.** `acme` also runs GHES behind `acme-dc1`. Alice sets `allowMirror: true` on its outbound host and routes it to the mirror. The mirror fetches through `lenny-egress` with `acme` asserted, into an `acme`-keyed store, and pods fetch from the store. With the connector offline, sessions resolve refs from the store within the 6-hour age limit, and `GET /v1/sessions/{id}` shows `resolvedFromStore`.

## Residual risks

- **Grant use by a compromised replica.** A compromised shared replica can use an in-flight grant for its own bytes, once per grant, at TCP level, to that host and port. Exclusive gateway groups remove this. The agent's and Envoy's byte counts can reveal it after the fact, and they do not prevent it.
- **Metadata.** A replica and the agent see authorities and SNI. A compromised replica sees them for every tenant attached to it.
- **Availability.** A compromised replica can draw and drop a share of a tenant's streams by claiming attachment, until it is replaced. A stolen key can do the same for one tenant.
- **Decision service key.** A compromise of `lenny-egress-authz` gives TCP-level reach to every customer's allowlisted hosts, which is a wider reach than its egress design statement. Rotation limits it in time, and C3 limits it in space. Only the customer's allowlist protects the customer network at that point.
- **Trust in the API gateway.** Agents fetch the grant key set from `lenny-gateway`, so a compromised `lenny-gateway` can add a key. It can already alter routes and policy.
- **Customer TLS weakness.** C6 depends on the client verifying the certificate. `netagent`, the resolver, the mirror, and the Token Service do. A tool in an opaque tunnel that disables verification exposes what that tool sends to anyone in the path, including a stolen-key holder.
- **Exfiltration.** Hostname filtering limits and does not prevent exfiltration, as the egress design states. An outbound host that accepts uploads is a channel into the customer's own network, and also a route for an agent to reach internal services the customer allowlisted.
- **Domain fronting through an internal proxy.** Encrypted Client Hello defeats the server-name check, as it does in the egress design.
- **A hostile tenant's own agent** can harm only that tenant's traffic, and its reports are `self_reported`.
- **Intercepting egress proxies** break the agent's client certificate, and a customer who cannot exempt the hostname cannot use the connector.
- **TLS-terminating load balancers** would strip the client certificate, and the chart's Layer 4 requirement is a deployer duty that preflight checks by a test handshake.
- **New public surface.** The outbound gateway accepts TLS from the internet before any authentication. Per-source handshake limits apply, and a distributed flood is the load balancer's or the provider's to absorb.
- **Revocation of a mint during an outage.** See "Changes requested".

## Specification sections affected

- §4 System Components: the outbound gateway and agent as new components, and §4.2 Session Manager's tenant-scoping table for the new resources.
- §5.2 Pool Configuration and Execution Modes: `tenantOverride.outboundHosts` beside EG-9's overlay flags.
- §9.3 Connector Definition and OAuth/OIDC: a sentence distinguishing MCP connectors from outbound connectors.
- §10.2 Authentication: the permission matrix, named permissions, and the statement that grants are not Lenny JWTs.
- §10.3 mTLS PKI: the outbound gateway's egress PKI leaf, which EG-11's table gains, and the absence of an agent CA.
- §11.7 Audit Logging: connector, instance, host, and grant writes.
- §12.8 Compliance Interfaces and §12.9 Data Classification: revocation and deletion at tenant erasure, and classification of instances, hosts, attachment rows, and the new streams.
- §13.2.4 `lenny-system` Namespace NetworkPolicies: the rows under "Deployment and network rules".
- §13.3 Credential Flow: the agent key and the grant as credential classes.
- §15.1 REST API: the routes under "Registration, rotation, and revocation", and the failure reasons.
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
| OC-1 Gateway and agent | The proto, the agent's dial, TLS identity, reversed roles, allowlist, DNS, and grant check. The replica's session handling, HTTP/2 limits, grant check, and server-name check. `REG-SITEATTACH`. The grant signer in the decision service, replica selection, and the route target's resolution. The Envoy cluster and routes. `outbound.*` evidence producers, the static hard limits, network rows, the public Service, and the agent's image and chart. |
| OC-2 Registration and identity | The tables, the admin and agent routes, enrollment, rotation, revocation, the outbound host validator, the reserved list, `tenantOverride.outboundHosts`, `allowMirror`, the platform grant, permissions, and `lenny-ctl outbound` create and revoke commands. |
| OC-3 Operations | The liveness read and its states, events, tenant quotas, metering, metrics, alerts, the probe with its tenant-assertion rule, `lenny-outbound-agent check`, runbooks, and the removal of the chart's refusal of `outboundGateway.enabled: true`. |
| OC-4 Exclusive gateway groups | Chart-rendered dedicated Deployments, assignment by admin API, and validators. It lands after the others, and only if the owner adopts it. |

### Dependencies

Each design states its own dependencies, and proposals from different designs may interleave as long as these hold.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| OC-1 | EG-2 | Decision service, typed route targets, and the mint path |
| OC-1 | EG-6 | Forwarded CONNECT route and selector peers |
| OC-1 | EG-10 | Envelope and `lenny-evidence` |
| OC-1 | EG-11 | The gateway's egress PKI leaf |
| OC-2 | OC-1 | The gateway and agent |
| OC-2 | EG-6 | Endpoint registration with per-endpoint CA, and the per-kind validator's tenant-registered path |
| OC-2 | EG-8 | The effective-policy read and decision queries |
| OC-2 | EG-9 | The overlay, one write path, and named permissions |
| OC-3 | OC-1, OC-2 | The resources it reports on |
| OC-3 | EG-8 | Decision reads used in debugging |
| OC-4 | OC-1, OC-2 | The gateway and assignment |
| End-to-end scenarios in OC-3's tiers | EG-1, EG-3, EG-4 | Minting, the pod git path, and `inject`. Scenario tests land with whichever of these lands last. |
| TS-1, if it scopes connectors below the tenant | OC-2 | The objects it would scope. `tenant-scoping-identity-design.md` decides this. |
| FE-1 | None here | A `quarantined` binding is refused at the decision, so no grant is signed |

SO-2's guardrails and SO-3's export apply through EG-9 and EG-10 with no connector-specific work.

## Changes requested of egress-and-git-design.md

This design does not edit that document. Each item says when it must land to avoid rework.

| Where | Change | Why | When |
| :-- | :-- | :-- | :-- |
| "Tenant outbound connector", opening sentence and **The existing route carries it.** | Remove "adds no Envoy route". State that a connector adds one `ORIGINAL_DST` cluster with upstream TLS, and one route per tunnel lifetime entry, and that the forwarded route has no server-name check there. | The claim fails, as "Test of the egress design's claims" states. | Text now |
| EG-6's chart work for the forwarded route | Render the forwarded route from a template taking a cluster and an optional upstream TLS context, and include upstream TLS in EG-6's conformance. | A private upstream proxy benefits too, and OC-1 then adds a value instead of duplicating the menu. | Before EG-6, otherwise a template rewrite |
| Route target kind in EG-2 | Spell the kind `outboundConnector`. | §9.3 binds "connector" and §28.1's rule forbids reuse. The value is stored in route records. | Before EG-2, otherwise a data change |
| "Egress decision service", the decision cache | State that the cache holds the policy outcome, and that a target kind can resolve its address and credential per call. | A cached grant fails on replay at the agent, and a cached replica outlives its session. | Before EG-2, which writes the cache |
| "Egress decision service", "it cannot mint" | Add that with OC-1 the service holds a grant signing key, and give the bound in "Security properties". | The property statement would otherwise be wrong. | With OC-1 |
| Endpoint registration in EG-6 ("Tenant-registered upstreams and git endpoints") | Make the record generic, with a kind, `caBundle`, and kind-specific fields, so a non-git host can carry a CA. | A registry behind a connector is neither an upstream nor a git endpoint, and a VCS pool has no CA field. | Before EG-6, otherwise a table split |
| "Credentials", `mint-coarse` GitLab tokens "revoked at session end" | Add a durable pending-revocation record, retried with backoff, and a tenant event on failure past a bound, with day-granular expiry as the backstop. | Over a connector the host can be unreachable at session end as a normal condition, and the token then outlives its session with no retry. This applies to any private host. | Before the GitLab token work lands, because it adds state to the token record |
| "Evidence streams", the `provenance` definition | Extend `self_reported` to a component outside the platform's control, such as a customer's agent. | Its text says "from inside a pod". The value set does not change. | Before EG-10, as text |
| "Labels and dependencies across designs" | Add OC-1 on EG-6, EG-10, and EG-11, add OC-2 on EG-6 and OC-1, and add OC-3 and OC-4. | The forwarded route and the CAs land in EG-6, as the table under "Proposal split" shows. | Text now |
| "Credentials", the resolver's tenant assertion | Let the decision honour the API gateway's assertion for a tenant's outbound hosts on a probe. | The rule today requires a VCS pool prefix or a git route, so a probe of a registry host would be refused. | Additive, with OC-3 |
| "Credentials", the 1.5-second CONNECT reply budget | Make the budget depend on the route target kind, if the spike shows wide-area dials exceed it. | A customer's slow resolver would fail plan bind while pods would succeed. | Additive, after the spike |
| "Configuration by scope", `tenantOverride` flags | Add `outboundHosts` under EG-9's overlay table. | OC-2 needs the flag. | Additive, with OC-2 |
| Level 1 "Refused configuration" list | Add the outbound connector kind beside the others. | The list omits it. | Text now |

One related item is outside this design's scope, and it needs the owner's review. A tenant-registered public upstream, on a tenant-owned pool with `tenantOverride.caBundle`, can receive traffic routed for any host in the tenant's policy, and the pod trusts the tenant's CA. If an operator placed an LLM credential in that tenant's pool for `deliveryMode: direct`, the tenant's proxy could read the lease. C8's reserved list closes the same path for connectors. This was reasoned from the egress design's text rather than tested, and that design may want the same list on upstream routes.

## Documentation

The rules in `doc-content.md` give each concept one owner page, and pages state behaviour with no section numbers. Each proposal carries the pages for its surface, after its specification changes land.

| Location | Change |
| :-- | :-- |
| New page "Outbound connectors" in the operator guide's "Network egress and credentials" chapter, which the egress design defines | Owns the concept, the components, what the feature protects and what it does not, enabling it, sizing, and the public endpoint. |
| New page for customer network owners in `docs/operator-guide/` or `docs/tutorials/` | Owns installing the agent, the firewall and proxy requirements, the allowlist, DNS, HA, and the local log. It is written for someone who holds no Lenny role. |
| `docs/tutorials/using-connectors.md` and §9.3's reader-facing pages | Add a sentence and a link separating MCP connectors from outbound connectors. |
| The egress chapter's "Tenant configuration" and "Git access" pages | Add outbound hosts and how the mirror treats them, linking to the owner page. |
| The egress chapter's "Evidence and visibility" page | Add the `outbound.*` streams and what `self_reported` means for an agent's claims. |
| `docs/operator-guide/ingress-and-tls.md` | Add the Layer 4 requirement and the dedicated hostname. |
| `docs/operator-guide/multi-tenancy.md`, `security.md`, and `troubleshooting.md` | Add a summary with a link, and a failure table entry. |
| `docs/operator-guide/scaling.md`, `configuration.md`, `lenny-ctl.md`, and `observability.md` | Add sizing, Helm values, commands, metrics, alerts, and events. |
| `docs/runbooks/` | Add runbooks for gateway unavailable, grant verification failures, and handshake floods. |
| `docs/reference/` | Add API reference for the new routes, and the failure reasons. |

Documentation uses the names `alice`, `bob`, `acme`, and `globex` under `doc-style.md`, and states the residual risks in the same words as the design.

## Tests

Each proposal carries the tests for its own surface, at the tiers `test-coverage.md` maps it to, each with a `// spec:` annotation. This section states what they must establish. No test has been written or run.

### Tier 0: static

- The rendered Envoy bootstrap validates with the outbound cluster and routes, and without them.
- The outbound gateway's rendered NetworkPolicy has no peer for the Kubernetes API, agent namespaces, the Token Service, or the decision service, and it mounts no service account token.
- The chart refuses a per-tenant quota sum above a replica's limits, and the proto and new admin schemas validate.
- The naming lint accepts the new identifiers.

### Tier 1: unit

- The grant check refuses each of: wrong tenant, wrong connector, wrong authority, a case or trailing-dot variant, expiry and skew at each boundary, unknown `kid`, a tampered claim, and a replayed `jti`, including at the replay cache's capacity.
- The validator refuses literals, every reserved name by pattern overlap, wide wildcards, and cluster suffixes.
- The server-name check handles a match, a mismatch, a missing name, a fragmented ClientHello, and non-TLS bytes.
- Replica selection handles no rows, a stale heartbeat, and ties. `denyCIDRs` covers mapped and link-local forms.
- The state computation covers each state and the hold-down.

### Tier 2: component

- Revocation in a snapshot closes a session, and stale snapshots at 60 seconds and at 5 minutes behave as stated.
- Reversed gRPC over a dialed TLS connection carries a tunnel, and the agent cannot open a stream.
- A drained replica removes its rows before closing sessions.
- Enrollment, rotation with overlap, a key older than the maximum, and token use counts behave as stated, each against the store.

### Tier 3: contract

- The agent and gateway at different versions negotiate or refuse under `minAgentVersion`.
- The failure mapping table holds, per row, at the resolver and at the mirror's fallback logic.
- A grant issued by the decision service verifies at the agent and at the replica with the key sets each holds.

### Tier 4: integration

- Clone, shallow clone, partial clone, push, and LFS work against a test GitLab and a test Gitea behind an agent, with a private CA.
- The Token Service mints and revokes through the connector, with its certificate check failing closed on a wrong CA.
- `inject` with TLS termination, `allowedRefPatterns`, and `credentialPinned` work on an outbound host.
- The API gateway's `ls-refs` works, with the failure mapping's errors.
- Revocation reaches the agent's session within the 5-second target, while a tunnel is open.
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

- Idle sessions at the target count, per replica memory and CPU.
- New streams per second with grant signing, and its added CONNECT latency.
- Throughput per tunnel and per session at realistic round-trip times, with bulk clones beside small requests on one session.
- One tenant at its limits while another's latency and throughput are measured, for P7.

### Tier 8: chaos

- Killing and freezing replicas and instances at load: failed streams, recovery time, and reattachment.
- Postgres outage and pooler loss, with the snapshot rules. Decision service failure, showing fail closed.
- Clock skew on the agent's host, and rolling restarts of both sides with open tunnels.

### Tier 9: security

- **Cross-tenant.** `globex`'s pod cannot reach `acme`'s host by route, by name, or by asserted tenant. A compromised-replica stand-in presents `globex`'s grants, invented grants, and replays to `acme`'s agent, and each is refused and logged.
- **Hostile agent.** A stand-in opens streams, floods frames, sends oversize headers, sends `PUSH_PROMISE`, stalls windows, and sends oversize status, and the replica closes the session within its limits without harming another tenant's traffic. A fuzz target covers the replica's path.
- **Stolen key.** A stand-in with a stolen key attaches and serves a self-signed endpoint. `netagent`, the resolver, the mirror, and the Token Service send no credential.
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

Nothing below has been run.

- **Envoy.** `ORIGINAL_DST` with an upstream TLS context verifying a fixed SAN, an address from ext_authz metadata, a forwarded CONNECT with an injected credential, `custom_response`, and `max_stream_duration`, on 1.40.2 and 1.39.6, plus whether `transport_socket_matches` can select TLS on the existing cluster. A deny with status 503 from ext_authz belongs here too. This is the first spike, because the route depends on it.
- **Reversed gRPC.** grpc-go serving on a dialed connection, the value of `SETTINGS_ENABLE_PUSH`, and throughput against raw HTTP/2 on 100 ms of latency.
- **Hostile peer.** What a malicious HTTP/2 server can do to a grpc-go client, with the limits grpc-go exposes, and how much of that the replica must add.
- **Idle cost.** Memory per session at a target of 20,000 sessions, which assumes 5,000 tenants, two instances each, and two sessions per instance.
- **Grant cost.** Signing at 2,000 CONNECTs per second, and replay cache memory.
- **CONNECT budget.** Reply time against the resolver's 1.5 seconds across 50, 150, and 300 ms round trips, with slow customer DNS.
- **Load balancers.** Whether repeated dials reach distinct replicas with the common cloud load balancers and connection reuse, and source address preservation.
- **Corporate paths.** Long-lived TLS through common outbound proxies, including idle timeouts against keepalive, and the failure seen under interception.
- **Konnectivity.** A time-boxed reading of its proto and agent for reusable code and for lessons on connection counts.
- **Full path.** Clone, push, and `glab` through a prototype, which also covers the egress design's still-owed push on a pinned tunnel.

## Decisions

Each is open. The recommendation is the design as written above.

| # | Decision | Recommendation | Cost of the recommendation | Alternative and its cost |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Build or adopt | Build on grpc-go. | Lenny owns an internet-facing service and a customer-distributed binary, with review and support duties. | Cloudflare Tunnel or Tailscale integration: a third party in the data path and in every customer contract, and it fails OR-9. |
| 2 | Isolation on shared replicas | Shared replicas with signed grants, and OC-4's exclusive groups for those who need them. | The in-flight grant residual on shared replicas. | Exclusive per tenant for all: closes it, at one Deployment and one public address per tenant, and a pre-provisioned pool because the chart renders them. |
| 3 | Authorization at the agent | A per-CONNECT signed grant. | The decision service holds a signing key and signs each CONNECT, and a compromise reaches every customer's allowlisted hosts. | A static per-connector credential injected by Envoy: simple, and a compromised replica holding it can reach the customer network at will, which fails OR-4. |
| 4 | TLS from Envoy to the outbound gateway | TLS with a verified SAN. | One Envoy cluster, a route per menu entry, a spike, and a level 2 assertion, and the egress design's claim changes. | Plain CONNECT on the existing route under NetworkPolicy: no Envoy change, and customer hostnames and grants cross the pod network in clear, against "Transport security"'s posture. |
| 5 | Agent identity | A self-signed key looked up by public key hash. | Replicas depend on a store snapshot, and there is no offline verification. | A Lenny CA issuing short-lived certificates: standard tooling and offline checks, and a new CA key to protect plus a separate revocation path. |
| 6 | Attachment register | Postgres with a one-second delta poll. | 1 to 2 seconds of stale selection after a crash. | Redis: lower latency, and a new dependency for the decision service, on a store the egress design gives it no path to. |
| 7 | Mirror for connector hosts | Off unless `allowMirror` is set on the host. | No store fallback or warm fill by default for GHES behind a connector. | On by default for `mint-scoped` providers: faster and more available, and it copies code out of a network the customer chose to keep private. |
| 8 | Pool ceiling | `tenantOverride.outboundHosts`, defaulting to `deny`. | A SaaS operator must set it on each shared pool, and a new row in EG-9's table. | Connector hosts bypass the ceiling for granted tenants: self-service with no pool step, and a platform admin's ceiling stops being the outer bound. |
| 9 | Local allowlist default | Empty, so the agent refuses until configured. | A required step, and a drift failure mode when routes change. | Follow the platform's routes unless restricted: quicker setup, and a compromised platform reaches anything the agent's host can reach. |
| 10 | Non-TLS internal hosts | Refused at both ends. | A customer with a plain-HTTP internal registry must front it with TLS. | An opt-in `allowPlaintext` on a host: wider adoption, and C6 and C7 no longer hold for it, since a stolen key would see tokens. |
| 11 | Anonymous hosts on a private CA, on shared pools | Add a binding with no secret to EG-4 later, so `netagent` terminates TLS and validates with the host's CA. Until then, use a tenant-owned pool with `tenantOverride.caBundle`. | A limit for shared pools at first, documented. | Change EG-4 now: wider coverage on day one, and more scope in a proposal this design does not own. |
| 12 | Intercepting customer proxies | Refuse, and require a hostname exemption. | Some customers need a change approved by their security team. | Authenticate inside the tunnel so interception passes: wider reach, and the proxy then holds what a replica holds, plus the agent's session. |
| 13 | Namespace | `lenny-system` under default deny, with anti-affinity. | A public-facing parser shares a namespace with credential holders, bounded by NetworkPolicy and no service account token. | Its own namespace: a clearer boundary, and more chart, cross-namespace rows, and an issuer, as the mirror's option needs. |
| 14 | Naming | `outbound` in all identifiers, and the egress design's kind renamed. | One edit in the egress design before EG-2. | Keep `connector`: shorter, and against §28.1, with permanent confusion against MCP connectors. |
| 15 | External review | A security review of OC-1 before the chart allows the feature outside `global.devMode`. | Time and money before release. | Internal review only: faster, for a surface exposed to every customer's host. |

## Evidence for the need

- A [Cursor forum post](https://forum.cursor.com/t/self-hosted-worker-online-but-empty-repo-list-in-agents-ui/156060) describes "self-managed GitLab on an internal host (VPN-only)".
- On Codex, [one user wrote](https://community.openai.com/t/gitlab-with-codex-cloud-agent/1268080) that using self-hosted git "would be a total Game changer".
- Cursor (vendor) [documents egress ranges and tunnels](https://cursor.com/docs/cloud-agent/security-network).
