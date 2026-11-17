---
code: CAT-REQS
type: catalog
summary: >-
  Every requirement identifier with a one-line title and its owner page. Generated from the
  requirement headings on capability pages; this slice's rows only.
---

<!-- GENERATED from the bold requirement identifiers on capability pages; the title is the generator's first clause of the requirement. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Requirements catalog

## Warm pools (POOL)

| ID | Title |
| :-- | :-- |
| [POOL-R1](../capabilities/runtimes-and-pools/warm-pools.md#pool-r1) | The pool record is the source of truth; pod sets are derived state. |
| [POOL-R2](../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) | Pool name pattern; never renamed; a deleted name stays taken. |
| [POOL-R3](../capabilities/runtimes-and-pools/warm-pools.md#pool-r3) | One `PodSet` per pool and pod-set key, written in one apply. |
| [POOL-R4](../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) | `PodSet` names are `<pool>.<key16>`; a key collision is reported, never overwritten. |
| [POOL-R5](../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) | Pool and pod-set labels are required and immutable; `spec.podSetRef` is authoritative. |
| [POOL-R6](../capabilities/runtimes-and-pools/warm-pools.md#pool-r6) | Each selector uses the scope of its work. |
| [POOL-R7](../capabilities/runtimes-and-pools/warm-pools.md#pool-r7) | When a `PodSet` is deleted; quarantined pods keep it. |
| [POOL-R8](../capabilities/runtimes-and-pools/warm-pools.md#pool-r8) | A change to the contributions starts a new pod set. |
| [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | Warm inventory is kept at the warm target. |
| [POOL-R10](../capabilities/runtimes-and-pools/warm-pools.md#pool-r10) | The scaling formula for `minWarm`. |
| [POOL-R11](../capabilities/runtimes-and-pools/warm-pools.md#pool-r11) | The warm target is split across pod sets by weight. |
| [POOL-R12](../capabilities/runtimes-and-pools/warm-pools.md#pool-r12) | Bootstrap mode for a new pool. |
| [POOL-R13](../capabilities/runtimes-and-pools/warm-pools.md#pool-r13) | Scale-to-zero windows. |
| [POOL-R14](../capabilities/runtimes-and-pools/warm-pools.md#pool-r14) | Pinned idle pods are accounted apart from warm inventory. |
| [POOL-R15](../capabilities/runtimes-and-pools/warm-pools.md#pool-r15) | A reserved pod counts as occupied. |
| [POOL-R16](../capabilities/runtimes-and-pools/warm-pools.md#pool-r16) | One warm-pod disruption budget per `PodSet`. |
| [POOL-R17](../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) | Default topology spread. |
| [POOL-R18](../capabilities/runtimes-and-pools/warm-pools.md#pool-r18) | The `PoolWarmingUp` condition and the warming refusal. |
| [POOL-R19](../capabilities/runtimes-and-pools/warm-pools.md#pool-r19) | The fill grace period suppresses exhaustion alerts. |
| [POOL-R20](../capabilities/runtimes-and-pools/warm-pools.md#pool-r20) | An owned pool's warm target is its `warmCount`. |
| [POOL-R21](../capabilities/runtimes-and-pools/warm-pools.md#pool-r21) | An owned pool's reservation floor and reserved marks. |
| [POOL-R22](../capabilities/runtimes-and-pools/warm-pools.md#pool-r22) | An owned pool's creation bound. |
| [POOL-R23](../capabilities/runtimes-and-pools/warm-pools.md#pool-r23) | Configuration generation and sync status. |
| [POOL-R24](../capabilities/runtimes-and-pools/warm-pools.md#pool-r24) | Backoff and stop on admission refusals. |
| [POOL-R25](../capabilities/runtimes-and-pools/warm-pools.md#pool-r25) | Pool drain. |
| [POOL-R26](../capabilities/runtimes-and-pools/warm-pools.md#pool-r26) | Pool deletion. |

## Pod claims (CLAIM)

| ID | Title |
| :-- | :-- |
| [CLAIM-R1](../capabilities/runtimes-and-pools/pod-claims.md#claim-r1) | A session acquires a pod only by a claim named `claim-<podName>`. |
| [CLAIM-R2](../capabilities/runtimes-and-pools/pod-claims.md#claim-r2) | A claim lasts one occupancy episode; who deletes it and when. |
| [CLAIM-R3](../capabilities/runtimes-and-pools/pod-claims.md#claim-r3) | Which release ends the occupancy without reuse. |
| [CLAIM-R4](../capabilities/runtimes-and-pools/pod-claims.md#claim-r4) | The claim set is the revision's pod sets. |
| [CLAIM-R5](../capabilities/runtimes-and-pools/pod-claims.md#claim-r5) | Candidate eligibility. |
| [CLAIM-R6](../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) | The tenant pin. |
| [CLAIM-R7](../capabilities/runtimes-and-pools/pod-claims.md#claim-r7) | Pinned idle pods are claimable only by their tenant. |
| [CLAIM-R8](../capabilities/runtimes-and-pools/pod-claims.md#claim-r8) | Binding states and optimistic-concurrency fencing. |
| [CLAIM-R9](../capabilities/runtimes-and-pools/pod-claims.md#claim-r9) | The reserved hold. |
| [CLAIM-R10](../capabilities/runtimes-and-pools/pod-claims.md#claim-r10) | A reserved pod counts as occupied. |
| [CLAIM-R11](../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) | Per-pod claim uniqueness at the Kubernetes API. |
| [CLAIM-R12](../capabilities/runtimes-and-pools/pod-claims.md#claim-r12) | The claim queue and `onPoolExhausted`. |
| [CLAIM-R13](../capabilities/runtimes-and-pools/pod-claims.md#claim-r13) | The Postgres fallback and its preconditions. |
| [CLAIM-R14](../capabilities/runtimes-and-pools/pod-claims.md#claim-r14) | The pod state mirror. |
| [CLAIM-R15](../capabilities/runtimes-and-pools/pod-claims.md#claim-r15) | Orphaned claim reclamation. |
| [CLAIM-R16](../capabilities/runtimes-and-pools/pod-claims.md#claim-r16) | The bound on `claimOrphanTimeout`. |

## Agent pod lifecycle (APOD)

| ID | Title |
| :-- | :-- |
| [APOD-R1](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r1) | One `AgentPod` per agent pod, owned by its pod set. |
| [APOD-R2](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2) | How an agent pod is created; the slot count is fixed. |
| [APOD-R3](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r3) | Readiness: a warm pod runs no runtime process. |
| [APOD-R4](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r4) | The WarmPoolController alone projects the phase. |
| [APOD-R5](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r5) | The projection table. |
| [APOD-R6](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r6) | The `lenny.dev/state` label follows the phase. |
| [APOD-R7](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7) | The recycle disposition. |
| [APOD-R8](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r8) | Retirement conditions of a recycling pod. |
| [APOD-R9](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r9) | `onScrubFailure`. |
| [APOD-R10](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r10) | Recycling requires `maxSessionsPerPod` and the acknowledgment. |
| [APOD-R11](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r11) | Unhealthy threshold of a pod with several slots. |
| [APOD-R12](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r12) | The drain request annotation. |
| [APOD-R13](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r13) | When an idle pod is drained. |
| [APOD-R14](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r14) | Draining, failed, and terminated; drains survive failover. |
| [APOD-R15](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r15) | Voluntary disruption: preStop checkpoint and grace period. |
| [APOD-R16](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16) | The session-cleanup finalizer. |
| [APOD-R17](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r17) | Deletion of the claim and the `AgentPod` at termination. |
| [APOD-R18](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r18) | Quarantine. |
