---
code: POLAREA
type: index
area: policy-and-controls
summary: >-
  Lists the capabilities that admit, rate-limit, and refuse requests.
---

# Policy and controls

Lists the capabilities that admit, rate-limit, and refuse requests.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Admission and fairness](admission-and-fairness.md) | `ADMIT` | States the policy engine, the admission checks and their order, and fairness between tenants. |
| [Circuit breakers](circuit-breakers.md) | `BREAKER` | States the operator-managed circuit breakers, their storage and propagation, and their effect on admission. |
| [Quotas and rate limits](quotas-and-rate-limits.md) | `QUOTA` | States the quota hierarchy, storage quotas, rate limits, counter recovery, and the fail-open budgets during a store outage. |
| [Request interceptors](request-interceptors.md) | `INTCPT` | States where request interceptors run, how their verdicts apply, their failure policy, and in-cluster and tenant-scoped interceptors. |

<!-- /generated:index -->
