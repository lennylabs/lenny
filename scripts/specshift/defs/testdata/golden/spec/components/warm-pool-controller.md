---
code: WPC
type: component
summary: The controller that creates agent pods.
---

# WarmPoolController

## Deployment

<a id="grant-pods-write"></a>

```lenny-def
kind: grant
name: pods write
api-groups: [""]
resources: [pods]
verbs: [create, delete, patch]
namespace-scope: agent namespace
requirement: POOL-R1
```

<!-- generated:rbac-grants -->

| Grant | Stated on | API groups | Resources | Verbs | Scope | Requirement |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| [`pods write`](warm-pool-controller.md#grant-pods-write) | [WarmPoolController](warm-pool-controller.md) | core | `pods` | `create`, `delete`, `patch` | agent namespace | POOL-R1 |

<!-- /generated:rbac-grants -->

<!-- generated:network-rules -->

No definition block names this page yet.

<!-- /generated:network-rules -->

## Settings read

<!-- generated:settings-read -->

| Setting | Default | Summary |
| :-- | :-- | :-- |
| [`maxSurge`](../capabilities/pools/pool-sizing.md#setting-maxsurge) | 1 | The pods a pool creates above its target during a rollout. |
| [`minWarm`](../capabilities/pools/warm-pools.md#setting-minwarm) | 0 | The number of idle pods a pool keeps. |
| [`recycle.mode`](../capabilities/pools/warm-pools.md#setting-recycle-mode) | never | Whether a pod serves a second session. |

<!-- /generated:settings-read -->

## Metrics and alerts

<!-- generated:metrics-emitted -->

| Metric | Type | Summary |
| :-- | :-- | :-- |
| [`lenny_warmpool_idle_pods`](../capabilities/pools/warm-pools.md#metric-lenny-warmpool-idle-pods) | gauge | The idle pods of a pod set. |

<!-- /generated:metrics-emitted -->

Text after the sections stays.
