---
code: LOPS
type: index
summary: >-
  The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS.
status: placeholder
---

# lenny-ops

The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Backup jobs](backup-jobs.md) | `BKJOB` | The lenny-backup Jobs that lenny-ops creates: pod specification, ServiceAccount, NetworkPolicy, and bucket policy. |
| [Gateway fan-out](gateway-fan-out.md) | `LOPSAGG` | How lenny-ops calls every gateway replica, sources metrics with and without Prometheus, and aggregates health and recommendations. |
| [Hosted services](hosted-services.md) | `LOPSSVC` | The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades. |
| [Kubernetes resources](kubernetes-resources.md) | `LOPSKRES` | The Kubernetes resources the chart renders for lenny-ops: Helm values, Services, ServiceAccount and RBAC, NetworkPolicies, and the disruption budget. |

<!-- /generated:index -->

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§4.0 Agent Operability Additions](../../04_system-components.md#40-agent-operability-additions), in part
- [§13.2.4 `lenny-system` Namespace NetworkPolicies](../../13_security-model.md#1324-lenny-system-namespace-networkpolicies), in part
- [§16.5 Alerting Rules and SLOs](../../16_observability.md#165-alerting-rules-and-slos), in part
- [§16.8 Section 25 Metrics](../../16_observability.md#168-section-25-metrics), in part
- [§17.1 Kubernetes Resources](../../17_deployment-topology.md#171-kubernetes-resources), in part
- [§17.8.5 Mandatory `lenny-ops` Deployment](../../17_deployment-topology.md#1785-mandatory-lenny-ops-deployment)
- [§25.2 Architecture Overview › The Split](../../25_agent-operability.md#the-split), in part
- [§25.2 Architecture Overview › Why This Boundary](../../25_agent-operability.md#why-this-boundary)
- [§25.4 The `lenny-ops` Service › Purpose](../../25_agent-operability.md#purpose)
- [§25.4 The `lenny-ops` Service › Deployment](../../25_agent-operability.md#deployment)
- [§25.4 The `lenny-ops` Service › Kubernetes Resources › Deployment: `lenny-ops`](../../25_agent-operability.md#deployment-lenny-ops)
- [§25.4 The `lenny-ops` Service › Kubernetes Resources › Probes](../../25_agent-operability.md#probes)
- [§25.4 The `lenny-ops` Service › Kubernetes Resources › Leader Election](../../25_agent-operability.md#leader-election)
- [§25.4 The `lenny-ops` Service › Kubernetes Resources › CNI / Service Mesh Compatibility](../../25_agent-operability.md#cni--service-mesh-compatibility), in part
- [§25.4 The `lenny-ops` Service › Kubernetes Resources › Air-Gapped Deployments](../../25_agent-operability.md#air-gapped-deployments), in part
- [§25.4 The `lenny-ops` Service › Dependencies](../../25_agent-operability.md#dependencies)
- [§25.4 The `lenny-ops` Service › Storage Routing](../../25_agent-operability.md#storage-routing), in part
- [§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling](../../25_agent-operability.md#multi-replica-lenny-ops-scaling)
- [§25.4 The `lenny-ops` Service › Multi-Replica Lenny-Ops Scaling › Trade-Offs](../../25_agent-operability.md#trade-offs)
- [§25.4 The `lenny-ops` Service › Startup and Readiness](../../25_agent-operability.md#startup-and-readiness)
- [§25.4 The `lenny-ops` Service › Authentication](../../25_agent-operability.md#authentication), in part
- [§25.4 The `lenny-ops` Service › Self-Monitoring](../../25_agent-operability.md#self-monitoring)
- [§25.4 The `lenny-ops` Service › Self-Monitoring › Self-Health Checks](../../25_agent-operability.md#self-health-checks)
- [§25.4 The `lenny-ops` Service › Self-Monitoring › Metrics](../../25_agent-operability.md#self-monitoring)
- [§25.4 The `lenny-ops` Service › Structured Logging](../../25_agent-operability.md#structured-logging), in part
- [§25.4 The `lenny-ops` Service › TLS](../../25_agent-operability.md#tls), in part
- [§25.8 Platform Lifecycle Management › Bootstrapping Lenny-Ops Monitoring](../../25_agent-operability.md#bootstrapping-lenny-ops-monitoring), in part
- [§25.12 MCP Management Server › Architecture](../../25_agent-operability.md#architecture), in part
- [§25.13 Bundled Alerting Rules › Metrics](../../25_agent-operability.md#2513-bundled-alerting-rules), in part

Changes of the v1 program whose content lands on this page:

- [0136-placement](../../../changes/0136-placement/change.md): PM-8
- [0142-security-oversight](../../../changes/0142-security-oversight/change.md): SO-7
