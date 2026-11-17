---
code: RTPOOL
type: index
area: runtimes-and-pools
summary: >-
  The capabilities that decide which agent runtimes Lenny runs, on which pods, and how those pods
  are kept warm, claimed, reused, and retired. Read this index to find the page that owns a runtime,
  pool, pod set, claim, or agent pod fact.
---

# Runtimes and pools

The capabilities in this area govern the path from a registered runtime to a pod that serves a session. A runtime is registered and versioned, a pool keeps pods of that runtime warm, a session claims one of those pods, and the pod moves through its states until it retires. Placement and isolation decide where and how a pod runs.

## Reading paths

- To change how a session acquires a pod, read [Pod claims](pod-claims.md), then [Agent pod lifecycle](agent-pod-lifecycle.md) for the states a claim drives.
- To change how many pods a pool keeps ready, read [Warm pools](warm-pools.md).
- To change what happens to a pod after its sessions end, read [Agent pod lifecycle](agent-pod-lifecycle.md).
- To change what a new runtime version does to running pools, read [Runtime revisions and rollouts](runtime-revisions.md), then [Warm pools](warm-pools.md) for pod sets.

## Capabilities

| Capability | Code | Summary |
| :-- | :-- | :-- |
| [Runtime registry](runtime-registry.md) | RUNTIME | Registers runtimes through the admin API, the bootstrap seed, and `Runtime` resources, and states the runtime name rule, standalone and derived runtimes, and the merge of derived fields. |
| [Runtime revisions and rollouts](runtime-revisions.md) | REV | Records each change to a runtime's versioned fields as a revision, selects a revision for each session by weight, computes the pod hash and the pod-set key, and moves traffic between revisions in a rollout. |
| [Tenant-derived runtimes](tenant-derived-runtimes.md) | DERIVED | Lets a granted tenant derive its own runtime from a platform base, with an owned pool, a pod budget, capacity reservations, and warm repository stores. |
| [Placement](placement.md) | PLACE | Assigns each pool a node class, an offering, a region, and a tenancy, renders them into each pod, and checks that pods land where their placement says. |
| [Isolation profiles](isolation-profiles.md) | ISO | Defines the isolation profiles a pool can run under and what each profile isolates. |
| [Warm pools](warm-pools.md) | POOL | Defines pools and their pod sets, keeps each pod set's warm inventory between its targets, names pod sets and labels their pods, and protects warm pods from voluntary disruption. |
| [Pod claims](pod-claims.md) | CLAIM | Acquires an idle pod for a session through a per-pod claim, restricts the claim to the chosen revision's pod sets, holds a recycled pod for its tenant, and recovers orphaned claims. |
| [Agent pod lifecycle](agent-pod-lifecycle.md) | APOD | Defines the agent pod and its states, projects each pod's phase from its claim, and states how a pod is created, becomes ready, is recycled, drained, quarantined, and deleted. |

## Components that carry out this area

| Component | Code | Carries out |
| :-- | :-- | :-- |
| [WarmPoolController](../../components/warm-pool-controller.md) | WPC | POOL, APOD, and the controller side of CLAIM. |
| [PoolScalingController](../../components/pool-scaling-controller.md) | PSC | POOL and the rendering parts of REV and PLACE. |
| [Admission webhooks](../../components/admission-webhooks.md) | WEBHOOK | The Kubernetes admission rules of POOL, CLAIM, and APOD. |
| [Gateway pod claimer](../../components/gateway/pod-claimer.md) | GWCLAIM | The gateway side of CLAIM. |

## Interfaces this area defines

- [Lenny's Kubernetes API](../../interfaces/kubernetes/index.md): [`PodSet`](../../interfaces/kubernetes/podset.md), [`AgentPod`](../../interfaces/kubernetes/agentpod.md), [`PodClaim`](../../interfaces/kubernetes/podclaim.md), and [labels and annotations](../../interfaces/kubernetes/labels-and-annotations.md).
- [Admin API pool operations](../../interfaces/admin-api/pools.md).
