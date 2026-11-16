---
code: BILLAREA
type: index
area: billing-and-usage
summary: >-
  Lists the capabilities that meter usage, enforce budgets, and emit billing events.
---

# Billing and usage

The capabilities in this area meter usage, enforce budgets, and emit billing events. Usage records come from the metering proxy and from resource attribution, rate cards price them, budgets cap them, and the billing event stream carries them to the operator.

## Reading paths

- To change what a tenant is billed for, read [Resource attribution](resource-attribution.md), then [Usage records](usage-records.md), then [Billing events](billing-events.md).
- To change budget enforcement, read [Budgets](budgets.md), then [Rate cards](rate-cards.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Budgets](budgets.md) | BUDGET | Defines credit budgets, budget inheritance, and the delegation budget counters. |
| [Rate cards](rate-cards.md) | RATE | Defines rate cards and how a usage record is priced against them. |
| [Usage records](usage-records.md) | USAGE | Defines usage records for LLM calls and resources and the usage the client API reports. |
| [Billing events](billing-events.md) | BILLEV | States the billing event stream: what emits each event, delivery sinks, immutability, and corrections. |
| [Resource attribution](resource-attribution.md) | ATTR | Attributes pod time, idle pods, stored bytes, and dedicated nodes to tenants, with reconciliation against the object store. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.
- [Metering proxy](../../components/gateway/metering-proxy.md): The gateway's metering proxy listener for LLM upstream calls: routes, limits, process settings, and metrics.
- [Warm pool controller](../../components/warm-pool-controller.md): The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics.

## Interfaces this area exposes

- [Billing events](../../interfaces/events/billing-events.md): The billing event types, their schemas, and the null-field contract.
- [Billing](../../interfaces/admin-api/billing.md): Rate card, budget, billing correction, and usage operations.
