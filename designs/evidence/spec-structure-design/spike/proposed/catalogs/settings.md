---
code: CAT-SETTINGS
type: catalog
summary: >-
  Every setting with its scope, default, and owner page. Generated from the setting definition
  blocks on owner pages; this slice's rows only.
---

<!-- GENERATED from `kind: setting` definition blocks. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Settings catalog

Each row points at the definition block that owns the setting. The owner page states the setting's meaning, range, and the requirement that uses it.

| Setting | Scope | Default | Owner |
| :-- | :-- | :-- | :-- |
| `admissionDeniedRetryCeiling` | PoolScalingController | 10 | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `bootstrapMinWarm` | Pool (`scalePolicy`) | None | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `claimOrphanTimeout` | WarmPoolController flag `--claim-orphan-timeout` | 15m | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `--create-qps` / `--create-burst` | WarmPoolController flag | 20 / 50 | [WarmPoolController](../components/warm-pool-controller.md#process-settings) |
| `gateway.claimHoldTTLSeconds` | Helm | 10 | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `initialFillGracePeriod` | WarmPoolController flag `--initial-fill-grace-period` | 120s | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `--agent-namespace` | PoolScalingController flag | `lenny-agents` | [PoolScalingController](../components/pool-scaling-controller.md#process-settings) |
| `--max-concurrent-reconciles` | WarmPoolController flag | 1 | [WarmPoolController](../components/warm-pool-controller.md#process-settings) |
| `maxWarm` | Pool | None | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `minWarm` | Pool | None | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `podClaimFallbackMaxMirrorLagSeconds` | Gateway flag | 10 | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `podClaimQueueTimeout` | Gateway | 60s | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `poolScaling.safetyFactor` | Helm | Per capacity tier | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `recycle.acknowledgeBestEffortScrub` | Pool (`sessionPolicy`) | false | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.allowCrossTenantReuse` | Pool (`sessionPolicy`) | false | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.enabled` | Pool (`sessionPolicy`) | false | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.maxPodUptimeSeconds` | Pool (`sessionPolicy`) | Absent | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.maxScrubFailures` | Pool (`sessionPolicy`) | 3 | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.maxSessionsPerPod` | Pool (`sessionPolicy`) | None; required when recycling | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `recycle.onScrubFailure` | Pool (`sessionPolicy`) | `warn` | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `reservedPods` | Owned pool | 0 | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `scalePolicy.podWarmupSecondsBaseline` | Pool | 30 | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `scalePolicy.scaleToZero` | Pool | Absent | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `sessionPolicy.maxConcurrentSessionsPerPod` | Pool | 1 | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `sessionPolicy.maxQueueWaitSeconds` | Pool | 30 | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `sessionPolicy.onPoolExhausted` | Pool | `reject` | [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `--status-qps` / `--status-burst` | WarmPoolController flag | 30 / 100 | [WarmPoolController](../components/warm-pool-controller.md#process-settings) |
| `--status-update-dedup-window` | WarmPoolController flag | 500ms | [WarmPoolController](../components/warm-pool-controller.md#process-settings) |
| sync interval | PoolScalingController | 30s | [PoolScalingController](../components/pool-scaling-controller.md#process-settings) |
| `terminationGracePeriodSeconds` | Pool | 120 | [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `topologySpreadConstraints` | Pool | Soft zone and node spread | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `warmCount` | Owned pool | None | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `warmupDeadlineSeconds` | Deployment | 300 | [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| work queue max depth | WarmPoolController flag | 500 | [WarmPoolController](../components/warm-pool-controller.md#process-settings) |
