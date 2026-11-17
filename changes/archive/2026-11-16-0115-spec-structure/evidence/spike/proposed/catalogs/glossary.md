---
code: CAT-GLOSSARY
type: catalog
summary: >-
  Every term the specification defines, with a one-sentence gloss and its owner page. Generated
  from the "Owned here" term tables on owner pages; this slice's rows only.
---

<!-- GENERATED from the "Owned here" rows of each page's Terms section; the gloss is the first sentence of the definition. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Glossary

| Term | Gloss | Owner |
| :-- | :-- | :-- |
| Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#agent-pod) |
| AgentPod reconciler | The part of the WarmPoolController that creates each `AgentPod`'s pod and projects its phase. | [WarmPoolController](../components/warm-pool-controller.md#terms) |
| Binding state | The state of a claim. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#binding-states) |
| Bootstrap mode | The scaling mode of a pool with too little demand history for the scaling formula. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Candidate | A pod in the claim set that a claim may take. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#terms) |
| Carve-out | The `PodSet` status fields the PoolScalingController writes. | [PoolScalingController](../components/pool-scaling-controller.md#terms) |
| Claim | The exclusive occupancy of one agent pod by the gateway on behalf of the sessions it places there. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#claim) |
| Claim queue | The per-pool wait in which a session create waits for a candidate. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#terms) |
| Claim set | The pod sets a session may take a pod from. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#claim-set) |
| `ClaimRequest` | The pod claimer's input. | [Gateway pod claimer](../components/gateway/pod-claimer.md#terms) |
| Crash failover window | The longest time no WarmPoolController replica leads after the leader crashes. | [WarmPoolController](../components/warm-pool-controller.md#terms) |
| Field owner | The single writer of a field of a Lenny resource. | [Lenny's Kubernetes API](../interfaces/kubernetes/index.md#terms) |
| `key16` | The first 16 characters of a pod-set key. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Lenny kind | A custom resource kind Lenny defines in the `lenny.dev` API group. | [Lenny's Kubernetes API](../interfaces/kubernetes/index.md#terms) |
| Occupancy episode | The span of one claim. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#terms) |
| Occupancy projection | The rule by which the WarmPoolController computes a pod's phase from its claim. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#terms) |
| Orphaned claim | A claim whose holder no longer acts on it. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#terms) |
| Phase | The coarse occupancy state of an agent pod. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#phases) |
| Pinned idle pod | An idle agent pod, carrying a tenant pin, with no claim. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#pinned-idle-pod) |
| Pod set | The warm pods of one pool built for one pod-set key. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pod-set) |
| Pod-set label | The meaning of `lenny.dev/pod-set`: the `key16` of a pod set. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Pod state mirror | The Postgres table `agent_pod_state`. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#terms) |
| Pool | A warmable deployment target for one runtime and one operational profile. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pool) |
| Pool label | The meaning of `lenny.dev/pool`: the pool an object belongs to. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Pool name | The primary key of the pool record. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pool-name) |
| Pool phase | `active` or `draining`. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Pool planner | The part of the WarmPoolController that plans each pod set's inventory. | [WarmPoolController](../components/warm-pool-controller.md#terms) |
| Pool record | The pool's row in the `pools` table. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Postgres fallback | The claim path that takes a candidate from the pod state mirror. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#postgres-fallback) |
| Recycle disposition | The decision to hold a recycled pod for reuse or retire it. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#recycle) |
| Recycling pool | A pool whose pods serve one session after another. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#terms) |
| Render | One PoolScalingController reconcile of a pool. | [PoolScalingController](../components/pool-scaling-controller.md#terms) |
| Reserved hold | The period after a recycled pod's scrub during which its claim stays `reserved`. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#reserved-hold) |
| Retirement | The move of a pod to `draining` because a limit or a failure ends its useful life. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#terms) |
| Schema-version annotation | The annotation that states a Lenny CRD's schema revision. | [Lenny's Kubernetes API](../interfaces/kubernetes/index.md#terms) |
| Served-session count | The number of sessions a pod has served over its life. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#terms) |
| Slot count | The number of slot containers rendered into a pod. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#slot-count) |
| Tenant pin | The binding of an agent pod to the first tenant it serves. | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#tenant-pin) |
| Warm inventory | The unpinned pods of a pod set in `warming` or `idle`. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Warm pod | An agent pod whose infrastructure is ready and on which no runtime process runs. | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#warm-pod) |
| Warm target | The number of unpinned warm pods a pod set holds. | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#terms) |
| Webhook Deployment | The Deployment and Service that serve one admission webhook. | [Admission webhooks](../components/admission-webhooks.md#terms) |
