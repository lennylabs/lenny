---
code: POLAREA
type: index
area: policy-and-controls
summary: >-
  Lists the capabilities that admit, shape, and refuse requests.
---

# Policy and controls

The capabilities in this area admit, shape, and refuse requests. The policy engine runs the admission checks and request interceptors, quotas and rate limits bound use, and operator circuit breakers stop classes of work.

## Reading paths

- To change the order or content of admission checks, read [Admission and fairness](admission-and-fairness.md), then [Request interceptors](request-interceptors.md).
- To change a limit on use, read [Quotas and rate limits](quotas-and-rate-limits.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Admission and fairness](admission-and-fairness.md) | ADMIT | States the policy engine, the admission checks and their order, and fairness between tenants. |
| [Request interceptors](request-interceptors.md) | INTCPT | States where request interceptors run, how their verdicts apply, their failure policy, and in-cluster and tenant-scoped interceptors. |
| [Quotas and rate limits](quotas-and-rate-limits.md) | QUOTA | States the quota hierarchy, storage quotas, rate limits, counter recovery, and the fail-open budgets during a store outage. |
| [Circuit breakers](circuit-breakers.md) | BREAKER | States the operator-managed circuit breakers, their storage and propagation, and their effect on admission. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.
- [Redis](../../components/external/redis.md): Redis: topology, logical separation, failure modes per use, scalability ceilings, and TLS.

## Interfaces this area exposes

- [Interceptor protocol](../../interfaces/interceptor-protocol.md): The protocol between the gateway and a request interceptor: phases, payloads, verdicts, and authentication.
- [Policies](../../interfaces/admin-api/policies.md): Delegation policy, interceptor, quota, and circuit breaker operations.
