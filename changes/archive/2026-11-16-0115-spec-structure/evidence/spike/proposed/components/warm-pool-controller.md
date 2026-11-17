---
code: WPC
type: component
summary: >-
  The WarmPoolController is the leader-elected Kubernetes controller that creates and retires agent
  pods, projects each pod's phase from its claim, keeps the pod state mirror, maintains warm-pod
  disruption budgets, and reclaims orphaned claims. This page states its responsibilities,
  interfaces, deployment, process settings, failure behavior, and metrics.
owns: [process-settings, metrics]
depends-on: [POOL, CLAIM, APOD, PSC, WEBHOOK, KPSET, KAPOD, KPCLAIM, KLABEL]
---

# WarmPoolController

The WarmPoolController is the leader-elected Kubernetes controller that creates and retires agent pods, projects each pod's phase from its claim, keeps the pod state mirror, maintains warm-pod disruption budgets, and reclaims orphaned claims. This page states its responsibilities, interfaces, deployment, process settings, failure behavior, and metrics.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| Pool planner | The part of the controller that decides, per `PodSet`, which `AgentPod` objects to create and which idle pods to drain. |
| AgentPod reconciler | The part of the controller that creates each `AgentPod`'s pod, copies the pod's address and name into the `AgentPod`, and projects the phase. |
| Crash failover window | The longest time no WarmPoolController replica leads after the leader crashes: the lease duration plus the renew deadline. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pod set | The warm pods of one pool built for one pod-set key ([Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pod-set)). |
| Agent pod | A Kubernetes pod that serves sessions for one pool ([Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#agent-pod)). |
| Claim | The exclusive occupancy of one agent pod by the gateway ([Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#claim)). |
| Pod state mirror | The Postgres table that mirrors each agent pod's state ([Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#postgres-fallback)). |
| Field manager | The name under which a writer applies fields with server-side apply ([Lenny's Kubernetes API](../interfaces/kubernetes/index.md#field-ownership)). |

## Overview {#overview}

The WarmPoolController runs as a Deployment with several replicas, of which one leads at a time. The leader watches `PodSet`, `AgentPod`, `PodClaim`, pod, and node objects. For each `PodSet` it plans the pod set's inventory and creates or drains pods to meet it. For each `AgentPod` it creates the pod, reads back its state, and writes the projected phase. It mirrors every phase change into Postgres, and it periodically looks for claims that nobody holds. It never touches a session: the gateway owns sessions and claims, and this controller only reads claims and deletes them where its responsibilities say so.

## Responsibilities {#responsibilities}

| Responsibility | Requirement it carries out |
| :-- | :-- |
| Keep each pod set's warm inventory at its target and replace pods that leave it | [POOL-R9](../capabilities/runtimes-and-pools/warm-pools.md#pool-r9), [POOL-R14](../capabilities/runtimes-and-pools/warm-pools.md#pool-r14) |
| Copy the pool and pod-set labels onto each `AgentPod` and pod | [POOL-R5](../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| List a pod set's `AgentPod` objects by owner reference or `spec.podSetRef` | [POOL-R6](../capabilities/runtimes-and-pools/warm-pools.md#pool-r6) |
| Delete a `PodSet` that no revision or session needs, keeping one with a quarantined pod | [POOL-R7](../capabilities/runtimes-and-pools/warm-pools.md#pool-r7) |
| Maintain one warm-pod disruption budget per `PodSet` | [POOL-R16](../capabilities/runtimes-and-pools/warm-pools.md#pool-r16) |
| Copy topology spread and placement into each pod | [POOL-R17](../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| Set `PoolWarmingUp` and the other `PodSet` status conditions | [POOL-R18](../capabilities/runtimes-and-pools/warm-pools.md#pool-r18) |
| Apply an owned pool's reservation floor and creation bound | [POOL-R21](../capabilities/runtimes-and-pools/warm-pools.md#pool-r21), [POOL-R22](../capabilities/runtimes-and-pools/warm-pools.md#pool-r22) |
| Create agent pods and set their readiness gate | [APOD-R1](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r1), [APOD-R2](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2), [APOD-R3](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r3) |
| Project each pod's phase and its `lenny.dev/state` label | [APOD-R4](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r4), [APOD-R5](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r5), [APOD-R6](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r6) |
| Drain pods past their uptime limit, on a drain request, and in the other idle-pod cases | [APOD-R8](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r8), [APOD-R12](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r12), [APOD-R13](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r13) |
| Maintain the `lenny.dev/host-schedulable` label from its node informer | [APOD-R7](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7) |
| Hold the session-cleanup finalizer, then delete the claim and the `AgentPod` at termination | [APOD-R16](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16), [APOD-R17](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r17) |
| Hold quarantined pods out of every retirement path | [APOD-R18](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r18) |
| Keep the pod state mirror and rebuild it on recovery | [CLAIM-R14](../capabilities/runtimes-and-pools/pod-claims.md#claim-r14) |
| Reclaim orphaned claims from the leader | [CLAIM-R15](../capabilities/runtimes-and-pools/pod-claims.md#claim-r15) |

## Interfaces served and consumed {#interfaces}

| Interface | Direction | What the controller does |
| :-- | :-- | :-- |
| [`PodSet`](../interfaces/kubernetes/podset.md) | Reads spec; writes status except the PoolScalingController's carve-out; deletes | Plans inventory and reports counts and conditions. |
| [`AgentPod`](../interfaces/kubernetes/agentpod.md) | Creates, writes spec and status, deletes | Sole writer. |
| [`PodClaim`](../interfaces/kubernetes/podclaim.md) | Watches, lists, deletes | Projects phases from claims and reclaims orphaned ones. Never creates or patches a claim. |
| Pods in the agent namespace | Creates, reads, patches labels, deletes | Creates each pod from its `AgentPod` and maintains [`lenny.dev/state`](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-state) and [`lenny.dev/host-schedulable`](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-host-schedulable). |
| Nodes | Watches | Feeds the host-schedulable label and the cluster CIDR drift check ([Network isolation](../capabilities/network-and-egress/network-isolation.md#cidr-drift)). |
| PodDisruptionBudgets | Creates, updates, deletes | One per `PodSet`. |
| Pod state mirror, register `REG-PODSTATE` ([Registers](../interfaces/channels/gateway-to-store.md#reg-podstate)) | Writes | The mirrored state columns. |
| Postgres session store | Reads | Whether a session holds a pod, for orphan reclamation and the finalizer. |

## Internal parts {#internal-parts}

- **Pool planner** (`pkg/controller/warmpool`). Reconciles one `PodSet` at a time from the `AgentPod` objects it owns, and produces create and drain actions within the warm target and the pinned-pod bound.
- **AgentPod reconciler** (`pkg/controller/agentpod`). Renders each pod, copies `status.podIP` and `status.podName`, sets the readiness gate, and writes the phase.
- **Garbage collector.** Runs the orphaned-claim loop on the leader every 60 seconds.
- **Mirror writer.** Writes the pod state mirror on every phase change and rebuilds it on each start and leadership change.
- **Disruption budget manager.** Creates, resizes, and deletes each `PodSet`'s budget.
- **Node informer.** Re-labels every pod on a node within one reconcile when the node is cordoned or uncordoned.

The controller-facing Go interface `PoolManager` in `pkg/podlifecycle` declares the operations the planner and the reconciler expose, with the default implementation `CRDPoolManager`: `ReconcilePool`, `ApplyPoolDefinition`, `ReplacePod`, `TransitionPodState`, `GarbageCollect`, `ManageFinalizer`, `ManagePDB`, `DrainPool`, and `SetPoolCondition`. It embeds the read-only `PoolReader` that the [gateway pod claimer](gateway/pod-claimer.md#internal-parts) shares. A different backend replaces only the implementation behind it.

## Field ownership and server-side apply {#field-ownership}

The controller applies its fields with server-side apply under the field manager `lenny-warm-pool-controller`, so the API server refuses its write to a field another manager owns. Each resource page lists the fields this controller owns. On a conflict the controller follows this policy:

1. It discards its cached copy, reads the resource again, and recomputes its apply.
2. It never forces a conflict. Forcing would take ownership of the other manager's fields and defeat the ownership rule.
3. On a second conflict it backs off with jitter from 100 ms up to 2 s and re-reads. After 5 consecutive conflicts without progress it logs the event `crd_ssa_conflict_stuck` with the controller, resource, and name, increments `lenny_crd_ssa_conflict_total`, and returns the error so the reconcile is requeued with exponential backoff.
4. After a crash and on each leadership acquisition it lists and reconciles every resource it owns, under this policy, before it accepts new watch events.

The [PoolScalingController](pool-scaling-controller.md#field-ownership) follows the same policy.

## Deployment {#deployment}

| Property | Value |
| :-- | :-- |
| Workload | Deployment in `lenny-system`, two or more replicas, with pod anti-affinity against the PoolScalingController's leader. |
| Leader election | Lease `lenny-warm-pool-controller` in `lenny-system`; lease duration 15 s, renew deadline 10 s, retry period 2 s. |
| ServiceAccount | `lenny-controller`. <!-- unresolved: §5.2 names the ServiceAccount `lenny-controller`, and the chart file is `controller-rbac.yaml`; no section names it from the controller's side. --> |
| Field manager | `lenny-warm-pool-controller`. |
| RBAC | `create`, `update`, `patch`, and `delete` on `agentpods`; `get` and `patch` on `agentpods/status` and `podsets/status`; `get`, `list`, `watch`, and `delete` on `podsets` and `podclaims`; `get`, `list`, `watch`, `create`, `patch`, and `delete` on pods in the agent namespace; `create`, `update`, and `delete` on `poddisruptionbudgets`; `get`, `create`, and `update` on `leases` in `lenny-system`; `get`, `list`, and `watch` on `nodes`; `get` and `list` on `networkpolicies` in the agent namespace. |
| Probes, resources, NetworkPolicy | <!-- unresolved: §17.1 and §17.8 hold these values; they were not migrated in this slice. --> |

The `patch` verb on `agentpods` and on the status subresources is needed because server-side apply issues `PATCH`. The `patch` verb on pods serves the labels this controller maintains, and the `delete` verb on `podsets` serves POOL-R7. <!-- unresolved: §4.6.3 lists no pod verbs beyond `get`, `list`, and `watch`, and no `delete` on templates, although the controller creates pods, re-labels them, and under RV-3 deletes `PodSet` objects; the grants above follow the responsibilities. -->

## Process settings {#process-settings}

```lenny-def
kind: setting
name: --create-qps / --create-burst
scope: WarmPoolController flag
type: number / integer
default: 20 / 50
summary: The client-side token bucket reserved for pod creation, so status traffic never starves scale-up.
```

```lenny-def
kind: setting
name: --status-qps / --status-burst
scope: WarmPoolController flag
type: number / integer
default: 30 / 100
summary: The client-side token bucket for status writes on AgentPod and PodSet objects.
```

```lenny-def
kind: setting
name: --status-update-dedup-window
scope: WarmPoolController flag
type: duration
default: 500ms
summary: The minimum interval between consecutive status writes to one AgentPod; changes within the window are coalesced and the last one is always written.
```

```lenny-def
kind: setting
name: --max-concurrent-reconciles
scope: WarmPoolController flag
type: integer
default: 1
summary: The reconcile workers; raise it for a cold fill from zero, within the rate limits above.
```

```lenny-def
kind: setting
name: work queue max depth
scope: WarmPoolController flag
type: integer
default: 500
summary: The work queue depth above which new reconcile events are dropped and counted.
```
<!-- unresolved: §4.6.1 names the setting "Work queue max depth" without a flag name. -->

All other API requests share controller-runtime's default limiter of 10 requests per second with a burst of 100. Per-tier values for these flags are capacity guidance and belong to the operator documentation. <!-- src: §4.6.1 "API server rate limiting:"; "`statusUpdateDeduplicationWindow` semantics." -->

## Failure and recovery {#failure-and-recovery}

- **Leader failover.** On a clean shutdown the leader releases its lease and a new leader takes over almost at once. On a crash, no leader acts for up to the crash failover window, 25 seconds. During the window no pod is created or replaced and no phase changes; sessions already running continue, and claims continue, because the gateway creates them. The pool's warm target is sized to cover this window ([Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#pool-r10)).
- **Crash during a scale-down drain.** The new leader re-lists every `AgentPod` and advances pods already in `draining`; it needs no record of the drain in progress, because idle pods carry no checkpoint and a session's checkpoint is the gateway's ([Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r14)).
- **Mirror after an outage.** The new leader rebuilds the pod state mirror before it writes incrementally. Until then the mirror lag keeps the Postgres fallback off for any pool past its threshold ([Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#claim-r14)).
- **API server or etcd unavailable.** The controller cannot create pods or write status. Its work queue replays pending reconciles when the API server returns, and the pools refill.
- **Work queue overflow.** Events beyond the queue depth are dropped and counted. A dropped event is recovered by the next resync of the affected object. <!-- unresolved: no source states the resync period. -->

## Metrics {#metrics}

```lenny-def
kind: metric
name: lenny_warmpool_idle_pods
type: gauge
labels: [pool, pod_set]
summary: Unpinned pods in idle, ready to be claimed (POOL-R14).
```

```lenny-def
kind: metric
name: lenny_warmpool_reserved_pods
type: gauge
labels: [pool, pod_set]
summary: Pods whose claim is in the reserved hold (CLAIM-R9).
```

```lenny-def
kind: metric
name: lenny_warmpool_pod_startup_duration_seconds
type: histogram
labels: [pool, isolation_profile]
summary: Time from pod creation to idle.
```

```lenny-def
kind: metric
name: lenny_warmpool_replenishment_rate
type: gauge
labels: [pool]
summary: Pods per minute entering idle.
```

```lenny-def
kind: metric
name: lenny_warmpool_warmup_failure_total
type: counter
labels: [pool, runtime_class, error_type]
summary: Pods that fail to reach idle; error_type is image_pull_error, resource_quota_exceeded, node_pressure, or placement_refused.
```
<!-- unresolved: §16.1 also counts `setup_command_failed` here, a session's setup failure at bind; under PL-8 setup runs at bind and is not warm-up, so that value is taken to leave this metric. -->

```lenny-def
kind: metric
name: lenny_warmpool_fill_duration_seconds
type: histogram
labels: [pool]
summary: Time from a pod set's creation, or its minWarm rising from zero, to minWarm idle pods.
```

```lenny-def
kind: metric
name: lenny_warmpool_idle_pod_minutes
type: counter
labels: [pool, resource_class]
summary: Cumulative idle pod-minutes, pinned idle pods included, for warm-pool cost.
```

```lenny-def
kind: metric
name: lenny_warmpool_claims_total
type: counter
labels: [pool, runtime_class]
summary: Transitions of a pod from idle to claimed.
```

```lenny-def
kind: metric
name: lenny_orphaned_claims_total
type: counter
labels: [pool]
summary: Orphaned claims reclaimed (CLAIM-R15); the pool label comes from the AgentPod's lenny.dev/pool, or from its PodSet's where the AgentPod lacks it.
```

```lenny-def
kind: metric
name: lenny_controller_pod_retirement_total
type: counter
labels: [reason, pool, runtime_class]
summary: Pods the controller retired; reason is uptime_limit (APOD-R8).
```

```lenny-def
kind: metric
name: lenny_agentpod_finalizer_terminating_seconds
type: histogram
labels: [agent_pod]
summary: Time an AgentPod spends terminating with the session-cleanup finalizer present (APOD-R16).
```
<!-- unresolved: the metric's type is not stated in §16.1; histogram is assumed. -->

```lenny-def
kind: metric
name: lenny_controller_workqueue_depth
type: gauge
labels: [controller, queue]
summary: The instantaneous depth of each controller work queue; shared with the PoolScalingController.
```

```lenny-def
kind: metric
name: lenny_controller_queue_overflow_total
type: counter
labels: [controller]
summary: Reconcile events dropped because the work queue was full.
```

```lenny-def
kind: metric
name: lenny_controller_leader_lease_renewal_age_seconds
type: gauge
labels: [controller]
summary: Seconds since the controller's lease was last renewed; shared with the PoolScalingController.
```

```lenny-def
kind: metric
name: lenny_crd_ssa_conflict_total
type: counter
labels: [crd, controller]
summary: Server-side apply conflicts that exhausted the retry policy; shared with the PoolScalingController.
```

The alerts on these metrics are defined on the capabilities whose requirements they detect: [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts), [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts), and [Agent pod lifecycle](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#alerts). <!-- unresolved: `ControllerLeaderElectionFailed`, `ControllerWorkQueueDepthHigh`, and `CRDSSAConflictStuck` detect a controller's failure as a process, which no capability requirement states; v0 has no owner for them. -->
