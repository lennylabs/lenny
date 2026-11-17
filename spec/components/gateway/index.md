---
code: GW
type: index
summary: >-
  The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics.
status: placeholder
---

# Gateway

The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics.

## Pages

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
