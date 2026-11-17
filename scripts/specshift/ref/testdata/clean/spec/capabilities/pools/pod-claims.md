---
code: CLAIM
type: capability
area: runtimes-and-pools
summary: >-
  How a session claims an agent pod.
---

# Pod claims

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| <a id="claim"></a>Claim | The exclusive occupancy of one agent pod. |
| Pool | A warmable deployment target ([POOL](warm-pools.md#pool)). |
| Reserved hold | The wait for the tenant's next session ([POOL](warm-pools.md#reserved-hold)). |

## Requirements {#requirements}

<a id="claim-r1"></a>**CLAIM-R1. A pod has one claim.** A claim takes a pod of a pool ([POOL-R1](warm-pools.md#pool-r1)), keeps its minimum ([POOL-R2](./warm-pools.md#pool-r2)), and reads [`setting-pool-minwarm`](warm-pools.md#setting-pool-minwarm). Its placement is [Placement](../../components/placement.md).

The resource is [`PodClaim`](../../interfaces/kubernetes/podclaim.md#custom-resource-podclaim), written by [`KPCLAIM-updateStatus`](../../interfaces/kubernetes/podclaim.md#kpclaim-updatestatus).

The text `[not a link](warm-pools.md#nothing)` sits in a code span and links nowhere.

<!-- A comment holding [a link](warm-pools.md#nothing) links nowhere. -->
