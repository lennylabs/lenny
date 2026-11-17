# Steps: 0148 Tenant outbound connector

## Steps

Proposals carry these labels, and each runs through the change-proposal pipeline. Until OC-3 lands, the chart's refusal under "Deployment and network rules" keeps the feature out of production.

### 0148.S1 Gateway, agent, and identity
- status: Planned
- aliases: [OC-1]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S6, 0127.S8, 0127.S9, 0127.S10, 0127.S11]

The proto, the agent's dial, TLS identity, reversed roles, allowlist, DNS, grant check, and attestations. The replica's handling of replica sessions, HTTP/2 limits, grant check, and server-name check. The tables `outbound_connector`, `outbound_agent_instance`, `outbound_enrollment_token`, `outbound_grant_key`, and `outbound_grant_key_revocation`, `REG-SITEATTACH` with its sweep, the restricted database logins in the chart and preflight, and the connector, enrollment token, instance, and agent API routes, including enrollment, rotation, reuse detection, retirement, reaping, and revocation. The grant signer in the decision service, attestation verification, replica selection, the group filter, and the route target's resolution. The Envoy cluster and routes. `outbound.*` evidence producers, the static hard limits, network rows, the public Service, and the agent's image and chart. The validator refuses the kind until OC-2, so OC-1's tests insert host records directly into [EG-6](../0127-egress-and-git/steps.md#steps)'s endpoint table.

The step needs the decision service, typed route targets, and the mint path of [0127.S2](../0127-egress-and-git/steps.md#steps) (EG-2). It needs the forwarded CONNECT route and selector peers of [0127.S6](../0127-egress-and-git/steps.md#steps) (EG-6), which are the NetworkPolicy peers that admit an in-cluster service by pod and namespace selector. It needs the envelope and `lenny-evidence` of [0127.S10](../0127-egress-and-git/steps.md#steps) (EG-10), and the outbound gateway's egress PKI leaf of [0127.S11](../0127-egress-and-git/steps.md#steps) (EG-11). It needs the named permissions and the single opt-in write path of [0127.S8](../0127-egress-and-git/steps.md#steps) (EG-8) and [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9), which its connector and instance API routes use.

### 0148.S2 Hosts and policy
- status: Planned
- aliases: [OC-2]
- lanes: [specification, code, documentation]
- depends-on: [0148.S1, 0127.S4, 0127.S6, 0127.S8, 0127.S9]

The outbound host validator, the reserved set at registration and at each decision, the outbound host API routes, `tenantOverride.outboundHosts`, `allowMirror`, the secret-less binding, the outbound grant, permissions, and `lenny-ctl outbound` commands.

The step needs the outbound gateway, the agent, and the tables of 0148.S1. It needs the typed `secret` union and termination of bound hosts of [0127.S4](../0127-egress-and-git/steps.md#steps) (EG-4), for `kind: none`. It needs the endpoint registration with per-endpoint CA and the per-kind validator's tenant-registered path of [0127.S6](../0127-egress-and-git/steps.md#steps) (EG-6), the effective-policy read and decision queries of [0127.S8](../0127-egress-and-git/steps.md#steps) (EG-8), and the overlay, one write path, and named permissions of [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9).

### 0148.S3 Operations
- status: Planned
- aliases: [OC-3]
- lanes: [specification, code, documentation]
- depends-on: [0148.S1, 0148.S2, 0127.S8]

The liveness read and its states, events, tenant quotas, metering, metrics, alerts, the probe with its tenant and instance assertions, `lenny-outbound-agent check`, the revoke API routes, runbooks, and the removal of the chart's refusal of `outboundGateway.enabled: true`.

The step reports on the resources of 0148.S1 and 0148.S2, and it uses the decision reads of [0127.S8](../0127-egress-and-git/steps.md#steps) (EG-8) in debugging. The end-to-end scenarios in this step's test tiers need the minting of [0127.S1](../0127-egress-and-git/steps.md#steps) (EG-1), the pod git path of [0127.S3](../0127-egress-and-git/steps.md#steps) (EG-3), and the `inject` of [0127.S4](../0127-egress-and-git/steps.md#steps) (EG-4). Those scenario tests land with whichever of these steps lands last.

### 0148.S4 Exclusive gateway groups
- status: Planned
- aliases: [OC-4]
- lanes: [specification, code, documentation]
- depends-on: [0148.S1, 0148.S2, 0148.S3]

Chart-rendered dedicated Deployments, the outbound group label, group filtering of snapshots and the trust response, assignment by admin API, and validators. It lands after the others, and it needs the outbound gateway and the assignment of 0148.S1 and 0148.S2.

### Notes on the steps

Each change states its own dependencies, and steps from different changes may interleave as long as the `depends-on` fields hold.

[UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (0151.S2) depends on 0148.S1 and 0148.S2 for a private host, for the token path to a link provider on an outbound host. A public provider needs neither step.

[FE-1](../0128-forensics-evidence-preservation/change.md#fe-1-quarantine) (0128.S2) has no dependency here. A `quarantined` binding is refused at the decision, so no grant is signed.

[TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) does not scope outbound connectors or outbound hosts below the tenant: [`0149-tenant-scoping-identity` decision 4](../0149-tenant-scoping-identity/decisions.md#decisions) leaves them out of the scoped objects, so neither design depends on the other. A route that names an outbound connector can sit under `environmentPolicies` once OC-2 lands, with no OC change.

The guardrails of [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) and the per-tenant SIEM export of [SO-3](../0142-security-oversight/change.md#so-3-per-tenant-siem-destination) apply through the single write path of [EG-9](../0127-egress-and-git/steps.md#steps) and the evidence ingest of [EG-10](../0127-egress-and-git/steps.md#steps), with no connector-specific work.

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
- **Full path.** Clone, push, and `glab` through a prototype, which also covers the [egress design](../0127-egress-and-git/steps.md#validation-still-owed)'s still-owed push through a raw tunnel to an address the decision returns.

## Validation record

This section records a check of the claims that earlier revisions of the egress design made about connectors. The egress design has applied its results, and the current text above states them.


### Test of the egress design's claims

Revision 36 of `0127-egress-and-git` said a connector "adds no Envoy route and changes no decision contract". Each part was read against that design and the tree, and later revisions applied the resulting changes. The table states the position against revision 40.

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
