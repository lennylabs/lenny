---
code: PSC
type: component
summary: >-
  The PoolScalingController is the leader-elected Kubernetes controller that renders each pool
  record into the pool's PodSet objects and sets their warm targets from demand. This page states
  its responsibilities, interfaces, deployment, process settings, failure behavior, and metrics.
owns: [process-settings, metrics]
depends-on: [POOL, REV, PLACE, DERIVED, WPC, WEBHOOK, KPSET]
---

# PoolScalingController

The PoolScalingController is the leader-elected Kubernetes controller that renders each pool record into the pool's `PodSet` objects and sets their warm targets from demand. This page states its responsibilities, interfaces, deployment, process settings, failure behavior, and metrics.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| Render | One reconcile of a pool: read the pool record and its revisions, compute each pod-set key and warm target, and apply one `PodSet` per key. |
| Carve-out | The `PodSet` status fields this controller writes under its own field manager although the WarmPoolController owns the rest of the status: `status.scalingMode` and `status.bootstrapHoursOfData`. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pool record | A pool's row in Postgres ([Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pool)). |
| Pod set | The warm pods of one pool built for one pod-set key ([Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pod-set)). |
| Pod-set key | The digest that identifies a pod set within its pool ([Runtime revisions and rollouts](../capabilities/runtimes-and-pools/runtime-revisions.md#pod-set-key)). |
| Server-side apply policy | The conflict policy the Lenny controllers follow ([WarmPoolController](warm-pool-controller.md#field-ownership)). |

## Overview {#overview}

The PoolScalingController is the only path from a pool record to Kubernetes. On each render it reads the pool record and the runtime's active revisions, computes the pod-set keys, computes the pool's warm target from its demand history and splits it among the pod sets, and applies the result as `PodSet` objects. It writes no pod and no `AgentPod`. It runs beside the WarmPoolController under its own lease, so either can fail over without the other.

## Responsibilities {#responsibilities}

| Responsibility | Requirement it carries out |
| :-- | :-- |
| Render each pool and pod-set key as one `PodSet` in one apply | [POOL-R1](../capabilities/runtimes-and-pools/warm-pools.md#pool-r1), [POOL-R3](../capabilities/runtimes-and-pools/warm-pools.md#pool-r3) |
| Name each `PodSet` and refuse to overwrite one of another key | [POOL-R4](../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) |
| Set the pool and pod-set labels on each `PodSet` it creates | [POOL-R5](../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| Compute `minWarm` from demand and split it among pod sets | [POOL-R10](../capabilities/runtimes-and-pools/warm-pools.md#pool-r10), [POOL-R11](../capabilities/runtimes-and-pools/warm-pools.md#pool-r11) |
| Hold a new pool in bootstrap mode and apply scale-to-zero windows | [POOL-R12](../capabilities/runtimes-and-pools/warm-pools.md#pool-r12), [POOL-R13](../capabilities/runtimes-and-pools/warm-pools.md#pool-r13) |
| Write default topology spread | [POOL-R17](../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| Clamp an owned pool's target and render its reservation and pod bound | [POOL-R20](../capabilities/runtimes-and-pools/warm-pools.md#pool-r20), [POOL-R21](../capabilities/runtimes-and-pools/warm-pools.md#pool-r21), [POOL-R22](../capabilities/runtimes-and-pools/warm-pools.md#pool-r22) |
| Stamp the rendered generation | [POOL-R23](../capabilities/runtimes-and-pools/warm-pools.md#pool-r23) |
| Back off and stop on admission refusals | [POOL-R24](../capabilities/runtimes-and-pools/warm-pools.md#pool-r24) |
| Write the pool's resolved placement into each `PodSet` | [Placement rendering](../capabilities/runtimes-and-pools/placement.md#rendering) |
| Render one warm list per pod set | [Runtime revisions and rollouts](../capabilities/runtimes-and-pools/runtime-revisions.md#warm-list-per-revision) |

## Interfaces served and consumed {#interfaces}

| Interface | Direction | What the controller does |
| :-- | :-- | :-- |
| Pool records, runtime revisions, and grants in Postgres | Reads | The input of each render. |
| Demand history from the metrics pipeline | Reads | `base_demand_p95` and `burst_p99_claims` per pool. <!-- unresolved: the source of the demand history (Prometheus or the gateway) is not stated in §4.6.2. --> |
| [`PodSet`](../interfaces/kubernetes/podset.md) | Creates, applies spec and the carve-out, deletes on pool deletion | Sole writer of `spec`. |
| [`lenny-pool-config-validator`](admission-webhooks.md#pool-config-validator) | Is reviewed by | Every apply passes through it. |

## Deployment {#deployment}

| Property | Value |
| :-- | :-- |
| Workload | Deployment in `lenny-system`, two or more replicas, with pod anti-affinity against the WarmPoolController's leader. |
| Leader election | Lease `lenny-pool-scaling-controller` in `lenny-system`; lease duration 15 s, renew deadline 10 s, retry period 2 s. |
| ServiceAccount | `lenny-pool-scaling-controller`. |
| Field manager | `lenny-pool-scaling-controller`, and a second manager for the carve-out. <!-- unresolved: PLC-2 says the carve-out stays "under its own field manager" without naming it. --> |
| RBAC | `create`, `update`, `patch`, and `delete` on `podsets`; `get` and `patch` on `podsets/status`; `get`, `create`, and `update` on `leases` in `lenny-system`; no access to `agentpods` or `podclaims`. |

## Field ownership {#field-ownership}

The controller owns all of `PodSet.spec` and the carve-out, and follows the [server-side apply policy](warm-pool-controller.md#field-ownership). A refusal from the admission webhook is not a conflict; [POOL-R24](../capabilities/runtimes-and-pools/warm-pools.md#pool-r24) governs it.

## Process settings {#process-settings}

```lenny-def
kind: setting
name: --agent-namespace
scope: PoolScalingController flag
type: string
default: lenny-agents
summary: The namespace in which the controller renders PodSet objects.
```
<!-- unresolved: DR-10 names the flag; its default is taken from PM-9's single agent namespace. -->

```lenny-def
kind: setting
name: sync interval
scope: PoolScalingController
type: duration
default: 30s
summary: The period between full renders of every pool.
```
<!-- unresolved: DR-10 states the 30-second sync as an observation of the tree; no section states it as a setting. -->

## Failure and recovery {#failure-and-recovery}

- **Leader failover.** Clean shutdown hands over at once, and a crash leaves no leader for up to 25 seconds. During the gap warm targets and configuration stop changing, and the WarmPoolController keeps reconciling against the last rendered `PodSet` objects.
- **Down for longer.** The gap is visible through the reconciliation lag metric while the process runs and through the gateway's drift check once it does not ([Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pool-r23)).
- **Admission refusals.** Handled per pod set under [POOL-R24](../capabilities/runtimes-and-pools/warm-pools.md#pool-r24); the backoff state lives in the leader's memory and resets on a leader change, so a new leader retries from a fresh read.
- **Partial render.** A pool with several pod sets is applied object by object, so for at most one render cycle the pod sets' targets may not sum to the pool's target. The WarmPoolController corrects inventory on its next reconcile.

## Metrics {#metrics}

```lenny-def
kind: metric
name: lenny_pool_config_reconciliation_lag_seconds
type: gauge
labels: [pool]
summary: Seconds since the pool's last successful render; reset to zero on each success.
```

```lenny-def
kind: metric
name: lenny_pool_bootstrap_mode
type: gauge
labels: [pool]
summary: 1 while the pool is in bootstrap mode, 0 once the formula sets minWarm (POOL-R12).
```

```lenny-def
kind: metric
name: lenny_pool_scaling_admission_denied_total
type: counter
labels: [pool, pod_set, reason]
summary: Admission refusals of the controller's PodSet applies; reason is the webhook's refusal code (POOL-R24).
```

The controller also emits `lenny_controller_workqueue_depth`, `lenny_controller_leader_lease_renewal_age_seconds`, and `lenny_crd_ssa_conflict_total`, which the [WarmPoolController](warm-pool-controller.md#metrics) page defines. <!-- unresolved: a metric emitted by two components has no single owner under v0. -->
