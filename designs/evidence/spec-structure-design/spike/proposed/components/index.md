---
code: COMPS
type: index
summary: >-
  Lists the components with their summaries, grouped as Lenny processes, agent pod containers, and external dependencies.
---

# Components

A component page describes one running unit: its responsibilities as links to the requirements it carries out, the interfaces it serves and consumes, its internal parts, its deployment, its process settings, its failure and recovery, and its metrics. A component page restates no behavioral rule.

The gateway, lenny-ops, and the agent pod are folders. The folder index is the component page for the unit as a whole, and each sub-page covers one internal part or container.

## Lenny processes

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Gateway](gateway/index.md) | GW | The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems. |
| [Token service](token-service.md) | TOKSVC | The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics. |
| [Warm pool controller](warm-pool-controller.md) | WPC | The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics. |
| [Pool scaling controller](pool-scaling-controller.md) | PSC | The PoolScalingController: reconciliation from Postgres, scaling computation, leader election, and metrics. |
| [Admission webhooks](admission-webhooks.md) | WEBHOOK | The admission webhook server and admission policies: what is rendered, availability, failure modes, and metrics. |
| [lenny-ops](lenny-ops/index.md) | LOPS | The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS. |
| [Egress proxy](egress-proxy.md) | EGPROXY | The central egress proxy (lenny-egress): deployment, transport security, and metrics. |
| [Egress decision service](egress-decision-service.md) | EGAUTHZ | The egress decision service (lenny-egress-authz): pod identification, policy reads, deployment, and metrics. |
| [Git mirror](git-mirror.md) | GITMIR | The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics. |
| [Agent DNS](agent-dns.md) | AGDNS | The dedicated CoreDNS instances for agent pods, including the resolver for proxied pods. |
| [Outbound gateway](outbound-gateway.md) | OUTGW | The outbound gateway Deployment that tenant agents dial, its replica groups, and metrics. |
| [Outbound agent](outbound-agent.md) | OUTAGT | The outbound agent a tenant runs on its own network: packaging, keys, and attestations. |
| [Web playground](web-playground.md) | PLAY | The web playground: placement and gating, cookie session handling, UI surface, asset serving and CSP, cleanup, and metrics. |
| [Preflight job](preflight-job.md) | PREFLT | The lenny-preflight Job and the checks it runs, each linked to the requirement it verifies. |

## Gateway parts

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Session coordination](gateway/session-coordination.md) | GWCOORD | How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail. |
| [Drain](gateway/drain.md) | GWDRAIN | The preStop drain of a gateway replica: its stages, deadlines, and terminal pipelines at process exit. |
| [Checkpoint barrier](gateway/checkpoint-barrier.md) | GWBARRIER | The checkpoint barrier the gateway fans out to pods during a rolling update. |
| [Pod claimer](gateway/pod-claimer.md) | GWCLAIM | The gateway's pod claimer: candidate selection, claim creation, and the Postgres fallback path. |
| [Metering proxy](gateway/metering-proxy.md) | GWLLM | The gateway's metering proxy listener for LLM upstream calls: routes, limits, process settings, and metrics. |
| [Ops endpoints](gateway/ops-endpoints.md) | GWOPS | The gateway-side operability parts: the health service, capacity recommendations, the event buffer, and the in-process alert tracker. |
| [Scaling and capacity](gateway/scaling-and-capacity.md) | GWSCALE | Gateway autoscaling: HPA and KEDA metric roles, the per-replica session budget, the minimum-replica formula, and subsystem extraction thresholds. |

## lenny-ops parts

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Gateway fan out](lenny-ops/gateway-fan-out.md) | LOPSAGG | How lenny-ops calls every gateway replica, sources metrics with and without Prometheus, and aggregates health and recommendations. |
| [Backup jobs](lenny-ops/backup-jobs.md) | BKJOB | The lenny-backup Jobs that lenny-ops creates: pod specification, ServiceAccount, NetworkPolicy, and bucket policy. |
| [Kubernetes resources](lenny-ops/kubernetes-resources.md) | LOPSK8S | The Kubernetes resources the chart renders for lenny-ops: Helm values, Services, ServiceAccount and RBAC, NetworkPolicies, and the disruption budget. |
| [Hosted services](lenny-ops/hosted-services.md) | LOPSSVC | The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades. |

## Agent pod

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Agent pod](agent-pod/index.md) | POD | The agent pod as a unit: its containers in start order, volumes and size limits, project quotas, security context, and pod-level metrics. |
| [Adapter](agent-pod/adapter.md) | ADAPTER | The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics. |
| [Runtime supervisor](agent-pod/runtime-supervisor.md) | SUPV | The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper. |
| [netagent](agent-pod/netagent.md) | NETAGENT | The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files. |
| [Init and support containers](agent-pod/init-and-support-containers.md) | PODINIT | The init containers that stage platform binaries and fill repository stores, and the keepalive container. |
| [Volumes and quotas](agent-pod/volumes-and-quotas.md) | PODVOL | The agent pod's volumes, their size limits and eviction behavior, and the project quotas on slot volumes. |

## External dependencies

| Page | Code | Summary |
| :-- | :-- | :-- |
| [External](external/index.md) | EXTIDX | Lists the external dependencies and the requirements Lenny places on each. |
| [Postgres](external/postgres.md) | PG | Postgres and its pooler: topology, HA, connection pooling, write classification, failover behavior, encryption, and backups. |
| [Redis](external/redis.md) | REDIS | Redis: topology, logical separation, failure modes per use, scalability ceilings, and TLS. |
| [Object store](external/object-store.md) | OBJ | The object store: backends, HA, versioning, replication, lifecycle rules, encryption, and outage handling. |
| [Kubernetes](external/kubernetes.md) | KUBE | The Kubernetes API server, etcd, and the CNI: version floor, API rate limits, etcd tuning, and network policy enforcement. |
| [cert-manager](external/cert-manager.md) | CERTM | cert-manager: the certificates Lenny requests, CA rotation, and failure modes. |
| [KMS](external/kms.md) | KMS | The KMS: keys Lenny uses, who may call them, rotation, and failure behavior. |
| [Prometheus](external/prometheus.md) | PROM | Prometheus: when it is required, scrape targets, rule rendering, evaluation cost, and the bring-your-own model. |
