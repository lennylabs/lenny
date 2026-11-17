---
code: BILLAREA
type: index
area: billing-and-usage
summary: >-
  Lists the capabilities that meter usage, enforce budgets, and emit billing events.
---

# Billing and usage

Lists the capabilities that meter usage, enforce budgets, and emit billing events.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Billing events](billing-events.md) | `BILLEV` | States the billing event stream: what emits each event, delivery sinks, immutability, and corrections. |
| [Budgets](budgets.md) | `BUDGET` | Defines credit budgets, budget inheritance, and the delegation budget counters. |
| [Rate cards](rate-cards.md) | `RATE` | Defines rate cards and how a usage record is priced against them. |
| [Resource attribution](resource-attribution.md) | `ATTR` | Attributes pod time, idle pods, stored bytes, and dedicated nodes to tenants, with reconciliation against the object store. |
| [Usage records](usage-records.md) | `USAGE` | Defines usage records for LLM calls and resources and the usage the client API reports. |
