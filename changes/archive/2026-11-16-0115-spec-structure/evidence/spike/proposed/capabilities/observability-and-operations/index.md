---
code: OPSAREA
type: index
area: observability-and-operations
summary: >-
  Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform.
---

# Observability and operations

The capabilities in this area let operators and DevOps agents observe, diagnose, and remediate the platform. Components emit operational events and health signals, lenny-ops aggregates them and runs diagnostics, and remediation locks and escalations coordinate the agents that act.

## Reading paths

- To change what an agent can observe, read [Operational events](operational-events.md), then [Health and diagnostics](health-and-diagnostics.md).
- To change how agents coordinate a fix, read [Remediation and escalation](remediation-and-escalation.md), then [Runbooks](runbooks.md).
- To change alerting, read [Alerting and slos](alerting-and-slos.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Operational events](operational-events.md) | OPSEV | States which state changes emit operational events, buffering in the gateway, and delivery by stream, polling, and webhook. |
| [Health and diagnostics](health-and-diagnostics.md) | DIAG | States how platform health and capacity recommendations are derived, the diagnostic operations, and automatic remediation. |
| [Remediation and escalation](remediation-and-escalation.md) | REMED | States remediation locks, their consistency, stealing and long holds, the operations inventory, and escalation. |
| [Runbooks](runbooks.md) | RUNBOOK | States the machine-readable runbook format and how runbooks are discovered from health results and alerts. |
| [Configuration drift](configuration-drift.md) | DRIFT | States how drift between the declared and the running configuration is detected and reconciled. |
| [Alerting and slos](alerting-and-slos.md) | SLO | States the SLO targets, burn-rate alerting, the bundled alert set and its operator customization, and SLO export. |

## Components that carry out this area

- [lenny-ops](../../components/lenny-ops/index.md): The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS.
- [Hosted services](../../components/lenny-ops/hosted-services.md): The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades.
- [Ops endpoints](../../components/gateway/ops-endpoints.md): The gateway-side operability parts: the health service, capacity recommendations, the event buffer, and the in-process alert tracker.
- [Prometheus](../../components/external/prometheus.md): Prometheus: when it is required, scrape targets, rule rendering, evaluation cost, and the bring-your-own model.

## Interfaces this area exposes

- [Ops API](../../interfaces/ops-api/index.md): The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups.
- [MCP management](../../interfaces/ops-api/mcp-management.md): The management MCP server of lenny-ops: tools, schemas, capability negotiation, and headers.
- [Operational events](../../interfaces/events/operational-events.md): The operational event types and their data schemas.
