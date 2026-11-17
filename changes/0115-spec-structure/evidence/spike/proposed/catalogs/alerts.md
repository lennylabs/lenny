---
code: CAT-ALERTS
type: catalog
summary: >-
  Every alert with its severity, the requirement whose breach it detects, and its owner page.
  Generated from the alert definition blocks on owner pages; this slice's rows only.
---

<!-- GENERATED from `kind: alert` definition blocks. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Alerts catalog

| Alert | Severity | Detects | Owner |
| :-- | :-- | :-- | :-- |
| `FinalizerStuck` | warning | [APOD-R16](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16) | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#alerts) |
| `PodClaimGuardUnavailable` | critical | [CLAIM-R11](../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts) |
| `PodClaimOrphanRateHigh` | warning | [CLAIM-R15](../capabilities/runtimes-and-pools/pod-claims.md#claim-r15) | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts) |
| `PodClaimQueueSaturated` | warning | [CLAIM-R12](../capabilities/runtimes-and-pools/pod-claims.md#claim-r12) | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts) |
| `PodStateMirrorStale` | warning | [CLAIM-R14](../capabilities/runtimes-and-pools/pod-claims.md#claim-r14) | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts) |
| `PoolBootstrapMode` | warning | [POOL-R12](../capabilities/runtimes-and-pools/warm-pools.md#pool-r12) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `PoolConfigDrift` | warning | [POOL-R23](../capabilities/runtimes-and-pools/warm-pools.md#pool-r23) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `PoolConfigValidatorUnavailable` | warning | [POOL-R1](../capabilities/runtimes-and-pools/warm-pools.md#pool-r1) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `PoolScaleoutBlockedByQuota` | warning | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `PoolScalingAdmissionStuck` | warning | [POOL-R24](../capabilities/runtimes-and-pools/warm-pools.md#pool-r24) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolBootstrapping` | warning | [POOL-R18](../capabilities/runtimes-and-pools/warm-pools.md#pool-r18) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolExhausted` | critical | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolIdleCostHigh` | warning | [POOL-R10](../capabilities/runtimes-and-pools/warm-pools.md#pool-r10) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolLow` | warning | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolReplenishmentFailing` | warning | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |
| `WarmPoolReplenishmentSlow` | warning | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9) | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts) |

## Rows the generator would flag

| Alert | Problem |
| :-- | :-- |
| `ControllerLeaderElectionFailed`, `ControllerWorkQueueDepthHigh`, `CRDSSAConflictStuck` | They detect a component's failure as a process; v0 assigns alerts only to capabilities. |
| `SDKConnectTimeout` | Removed with pre-launched runtimes. |
| `EtcdUnavailable`, `EtcdWriteLatencyHigh`, `EtcdQuotaNearLimit` | Cluster-dependency alerts that §4.6.1 states; their owner is outside the slice. |
