---
code: OPSAREA
type: index
area: observability-and-operations
summary: >-
  Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform.
status: placeholder
---

# Observability and operations

Lists the capabilities that let operators and DevOps agents observe, diagnose, and remediate the platform.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Alerting and SLOs](alerting-and-slos.md) | `ALERTSLO` | States the SLO targets, burn-rate alerting, the bundled alert set and its operator customization, and SLO export. |
| [Configuration drift](configuration-drift.md) | `DRIFT` | States how drift between the declared and the running configuration is detected and reconciled. |
| [Health and diagnostics](health-and-diagnostics.md) | `DIAG` | States how platform health and capacity recommendations are derived, the diagnostic operations, and automatic remediation. |
| [Operational events](operational-events.md) | `OPSEV` | States which state changes emit operational events, buffering in the gateway, and delivery by stream, polling, and webhook. |
| [Remediation and escalation](remediation-and-escalation.md) | `REMED` | States remediation locks, their consistency, stealing and long holds, the operations inventory, and escalation. |
| [Runbooks](runbooks.md) | `RUNBOOK` | States the machine-readable runbook format and how runbooks are discovered from health results and alerts. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§25 Agent Operability](../../25_agent-operability.md)
- [§25.1 Design Philosophy and Agent Model › Component Model](../../25_agent-operability.md#component-model), in part
- [§25.1 Design Philosophy and Agent Model › The Operational Loop](../../25_agent-operability.md#the-operational-loop)
- [§25.1 Design Philosophy and Agent Model › The Bootstrap Problem and External Agent Model](../../25_agent-operability.md#the-bootstrap-problem-and-external-agent-model), in part
- [§25.1 Design Philosophy and Agent Model › Design Constraints](../../25_agent-operability.md#design-constraints), in part
- [§25.16 Deployment Topology Summary › External DevOps Agent](../../25_agent-operability.md#external-devops-agent), in part
