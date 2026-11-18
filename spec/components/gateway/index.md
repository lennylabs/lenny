---
code: GW
type: index
summary: >-
  The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics.
status: partial
---

# Gateway

The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [API front](api-front.md) | `GWAPI` | The gateway's request pipeline as a part of every replica: middleware order, authentication, rate limiting, handler registration, and request scope. |
| [Checkpoint barrier](checkpoint-barrier.md) | `GWBARRIER` | The checkpoint barrier the gateway fans out to pods during a rolling update. |
| [Drain](drain.md) | `GWDRAIN` | The preStop drain of a gateway replica: its stages, deadlines, and terminal pipelines at process exit. |
| [Metering proxy](metering-proxy.md) | `GWLLM` | The gateway's metering proxy listener for LLM upstream calls: routes, limits, process settings, and metrics. |
| [Ops endpoints](ops-endpoints.md) | `GWOPS` | The gateway-side operability parts: the health service, capacity recommendations, the event buffer, and the in-process alert tracker. |
| [Platform tools](platform-tools.md) | `GWTOOLS` | The platform tools seam (§4.1 MCP Fabric): platform tool calls from pods, delegation orchestration, and the elicitation chain. |
| [Pod claimer](pod-claimer.md) | `GWCLAIM` | The gateway's pod claimer: candidate selection, claim creation, and the Postgres fallback path. |
| [Scaling and capacity](scaling-and-capacity.md) | `GWSCALE` | Gateway autoscaling: HPA and KEDA metric roles, the per-replica session budget, the minimum-replica formula, and subsystem extraction thresholds. |
| [Session coordination](session-coordination.md) | `GWCOORD` | How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail. |
| [Stream proxy](stream-proxy.md) | `GWSTREAM` | The stream proxy seam: session attachment, event relay, and reconnection for SSE readers. |
| [Upload handler](upload-handler.md) | `GWUPLOAD` | The upload handler seam: upload proxying, validation, and staging. |

<!-- /generated:index -->

## Seam model

This page is written in part. It states the rules that hold each seam to its boundary and keep credential and admission code with the parts that act on it, and the sources under "Content today" hold the rest of its content until the page is written.

<a id="gw-r1"></a>**GW-R1. Seams and the session core reach each other only through the seam interface.** Each seam reaches the session core and every other seam only through the operations of the [seam interface](../../interfaces/gateway-seams.md). The session core reaches a seam only through the seam interface. In the Go tree, a package of a seam imports, from the gateway, only the seam interface package and the packages of its own seam, and a package of the session core imports no package of a seam. In the composition root, `cmd/lenny-gateway`, a value of one part reaches another part through an interface or a function type that the receiving part declares only where the receiving part may import the value's package. Otherwise it reaches it only through an interface or a function type that the seam interface package declares. An interface or a function type declared outside the module, such as a standard-library handler type, is not such a target: it is in-process composition that an extraction replaces with the extracted service's own. A value of an adapter type the composition root declares belongs to the parts whose values its fields hold, and a function literal belongs to the parts whose functions or methods it calls or whose values it captures.

<a id="gw-r2"></a>**GW-R2. Credential and admission code stays with the parts that act on it.** Within the gateway, the confined packages are the credential packages (every package of the gateway's credentials directory, the connector credential store, connector secrets, connector authorization, VCS credentials, the credential router, and the shared credential package with the packages under it), the token and key packages (the JWT package, token introspection, the Token Service, its proxy and its protocol, token exchange, upload tokens, webhook signatures, the connector OAuth package, service identity, the admin token package and its Kubernetes secret store, the issued-token store, KMS, tenant KMS, the CA rotation packages, and the mTLS deny list), the admission packages (the authentication middleware, quota, policy, and the tenant store), and the authorization predicates (the caller check, environment access, the tenant access store, the custom role store, and the delegation policy store). The session core, the metering proxy, and the composition root, `cmd/lenny-gateway`, may import any of them. The API front may import those its request pipeline and its admin handlers read: the authentication middleware, the tenant store, the tenant access store, the custom role store, the delegation policy store, quota, the interceptor stores, the rate limiter, the admin token package, service identity, and the credential, token, and key packages its admin handlers read; of the shared credential package and the packages under it, the API front may import the package itself, the credential pool store, and impersonation. The web playground may import the packages its token mint reads: the JWT package, the issued-token store, and the tenant store. Every other part reaches them only through the operations of the [seam interface](../../interfaces/gateway-seams.md). The rule governs direct imports; what a package reaches through another package is held by review.

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§4.1 Edge Gateway Replicas](../../04_system-components.md#41-edge-gateway-replicas), in part
- [§4.1 Edge Gateway Replicas › Gateway Internal Subsystems](../../04_system-components.md#gateway-internal-subsystems), in part
- [§4.2 Session Manager](../../04_system-components.md#42-session-manager), in part
- [§4.6.3 CRD Field Ownership and Write Boundaries](../../04_system-components.md#463-crd-field-ownership-and-write-boundaries), in part
- [§10.1.8 Rolling Updates and the CheckpointBarrier Protocol](../../10_gateway-internals.md#1018-rolling-updates-and-the-checkpointbarrier-protocol), in part
- [§10.3 mTLS PKI](../../10_gateway-internals.md#103-mtls-pki), in part
- [§10.4 Gateway Reliability](../../10_gateway-internals.md#104-gateway-reliability), in part
- [§11.2.1 Billing Event Stream](../../11_policy-and-controls.md#1121-billing-event-stream), in part
- [§13.2.4 `lenny-system` Namespace NetworkPolicies](../../13_security-model.md#1324-lenny-system-namespace-networkpolicies), in part
- [§13.2.5 Gateway Egress to Upstream LLM Providers](../../13_security-model.md#1325-gateway-egress-to-upstream-llm-providers), in part
- [§16.1 Metrics](../../16_observability.md#161-metrics), in part
- [§16.5 Alerting Rules and SLOs](../../16_observability.md#165-alerting-rules-and-slos), in part
- [§17.1 Kubernetes Resources](../../17_deployment-topology.md#171-kubernetes-resources), in part
- [§25.13 Bundled Alerting Rules › Metrics](../../25_agent-operability.md#2513-bundled-alerting-rules), in part

Changes of the v1 program whose content lands on this page:

- [0136-placement](../../../changes/0136-placement/change.md): PM-8
