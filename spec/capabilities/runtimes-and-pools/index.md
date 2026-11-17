---
code: RTPOOL
type: index
area: runtimes-and-pools
summary: >-
  Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired.
---

# Runtimes and pools

Lists the capabilities that decide which runtimes Lenny runs, on which pods, and how those pods are kept warm, claimed, reused, and retired.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Agent pod lifecycle](agent-pod-lifecycle.md) | `APOD` | Defines the agent pod and its states and how a pod is created, becomes ready, is recycled, drained, quarantined, and deleted. |
| [Isolation profiles](isolation-profiles.md) | `ISO` | Defines the isolation profiles a pool can run under, what each profile isolates, and the acknowledgements a weaker profile requires. |
| [Owned pools and reservations](owned-pools-and-reservations.md) | `OWNPOOL` | States the pools a tenant owns for its derived runtimes, the pod budget, and capacity reservations. |
| [Placement](placement.md) | `PLACE` | Assigns each pool a node class, an offering, a region, and a tenancy, renders them into each pod, and checks that pods land where their placement says. |
| [Pod claims](pod-claims.md) | `CLAIM` | Acquires an idle pod for a session through a per-pod claim, holds a recycled pod for its tenant, and recovers orphaned claims. |
| [Resource classes](resource-classes.md) | `RCLASS` | Defines resource classes in configuration, how a pool's class maps to container requests and limits, and the checks at load, write, and reconcile. |
| [Runtime registry](runtime-registry.md) | `RUNTIME` | Registers runtimes, states the runtime fields and their rules, operator-derived runtimes and the merge of derived fields, and published metadata. |
| [Runtime revisions](runtime-revisions.md) | `REV` | Records each change to a runtime's versioned fields as a revision, selects a revision per session, and moves traffic between revisions in a rollout. |
| [Runtime routes](runtime-routes.md) | `ROUTE` | Chooses the runtime for a session from routing labels and runtime routes, which replace experiment variants. |
| [Tenant-derived runtimes](tenant-derived-runtimes.md) | `DERIVED` | Lets a granted tenant derive its own runtime from a platform base, with an owned pool, a pod budget, and capacity reservations. |
| [Warm pools](warm-pools.md) | `POOL` | Defines pools and pod sets, keeps warm inventory between its targets, and protects warm pods from voluntary disruption. |

<!-- /generated:index -->
