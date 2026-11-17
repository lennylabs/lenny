---
code: COMPS
type: index
summary: >-
  Lists the components with their summaries, grouped as Lenny processes, agent pod containers, and external dependencies.
---

# Components

Lists the components with their summaries, grouped as Lenny processes, agent pod containers, and external dependencies.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Admission webhooks](admission-webhooks.md) | `WEBHOOK` | The admission webhook server and admission policies: what is rendered, availability, failure modes, and metrics. |
| [Agent DNS](agent-dns.md) | `AGDNS` | The dedicated CoreDNS instances for agent pods, including the resolver for proxied pods. |
| [Agent pod](agent-pod/index.md) | `POD` | The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics. |
| [Egress decision service](egress-decision-service.md) | `EGAUTHZ` | The egress decision service (lenny-egress-authz): pod identification, policy reads, deployment, and metrics. |
| [Egress proxy](egress-proxy.md) | `EGPROXY` | The central egress proxy (lenny-egress): deployment, transport security, and metrics. |
| [External](external/index.md) | `EXTIDX` | Lists the external dependencies and the requirements Lenny places on each. |
| [Gateway](gateway/index.md) | `GW` | The gateway as a unit: the seam model and the isolation every seam has, the session core, the parts that apply to the whole replica, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. |
| [Git mirror](git-mirror.md) | `GITMIR` | The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics. |
| [lenny-ops](lenny-ops/index.md) | `LOPS` | The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS. |
| [Outbound agent](outbound-agent.md) | `OUTAGT` | The outbound agent a tenant runs on its own network: packaging, keys, and attestations. |
| [Outbound gateway](outbound-gateway.md) | `OUTGW` | The outbound gateway Deployment that tenant agents dial, its replica groups, and metrics. |
| [Pool scaling controller](pool-scaling-controller.md) | `PSC` | The PoolScalingController: reconciliation from Postgres, scaling computation, leader election, and metrics. |
| [Preflight job](preflight-job.md) | `PREFLT` | The lenny-preflight Job and the checks it runs, each linked to the requirement it verifies. |
| [Token service](token-service.md) | `TOKSVC` | The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics. |
| [Warm pool controller](warm-pool-controller.md) | `WPC` | The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics. |
| [Web playground](web-playground.md) | `PLAY` | The web playground: placement and gating, cookie session handling, UI surface, asset serving and CSP, cleanup, and metrics. |

<!-- /generated:index -->
