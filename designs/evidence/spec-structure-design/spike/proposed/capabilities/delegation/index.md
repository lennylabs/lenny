---
code: DLGAREA
type: index
area: delegation
summary: >-
  Lists the capabilities that let a session start child sessions and govern the resulting tree.
---

# Delegation

The capabilities in this area let a session start child sessions. A parent delegates under a lease that its policy bounds, the children form a tree, and the tree recovers, cascades, and cleans up as its members fail or end.

## Reading paths

- To change what a parent may delegate, read [Delegation policy](delegation-policy.md), then [Delegation lease](delegation-lease.md), then [Delegation availability](delegation-availability.md).
- To change what happens to children when a parent fails or ends, read [Tree recovery and cascade](tree-recovery-and-cascade.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Delegation](delegation.md) | DLG | States the delegation mechanism, approval modes, the task tree, task records and results, and what a parent sees of its children. |
| [Delegation policy](delegation-policy.md) | DPOL | Defines the delegation policy resource, tag evaluation, the session-level policy cap, content policy, and the scanning of exported files. |
| [Delegation lease](delegation-lease.md) | LEASE | Defines the delegation lease, its fields and inheritance, tree-wide limits, isolation monotonicity, and lease extension. |
| [Delegation availability](delegation-availability.md) | DAVAIL | States the delegation levels, how a session's level is resolved, the refusal when delegation is unavailable, and the tool list a runtime receives. |
| [Tree recovery and cascade](tree-recovery-and-cascade.md) | DTREE | States how a delegation tree recovers, cascades on a parent's end, cleans up orphans, and how parents watch children's streams. |
| [Lease extension](lease-extension.md) | LEASEXT | States how a delegation lease is extended, the configuration layering, and the extension approval modes. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.

## Interfaces this area exposes

- [Platform tools](../../interfaces/runtime-protocol/platform-tools.md): The platform tools a runtime calls, through frames or the reference MCP bridge, with schemas including the delegation tools and the task result.
- [Policies](../../interfaces/admin-api/policies.md): Delegation policy, interceptor, quota, and circuit breaker operations.
