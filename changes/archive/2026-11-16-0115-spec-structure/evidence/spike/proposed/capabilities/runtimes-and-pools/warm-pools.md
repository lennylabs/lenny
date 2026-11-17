---
code: POOL
type: capability
area: runtimes-and-pools
summary: >-
  A pool keeps agent pods of one runtime ready before any session asks for one. This page defines
  the pool and its pod sets, how pod sets are named and labelled, how each pod set's warm target is
  computed and held, how warm pods are protected from voluntary disruption, and how a pool is
  drained, deleted, and kept in step with its stored configuration.
owns: [objects, requirements, settings, alerts]
depends-on: [RUNTIME, REV, DERIVED, PLACE, CLAIM, APOD, QUAR]
---

# Warm pools

A pool keeps agent pods of one runtime ready before any session asks for one. This page defines the pool and its pod sets, how pod sets are named and labelled, how each pod set's warm target is computed and held, how warm pods are protected from voluntary disruption, and how a pool is drained, deleted, and kept in step with its stored configuration.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="pool"></a>Pool | A warmable deployment target for one runtime and one operational profile: the runtime, the isolation profile, the resource class, the egress profile, the session policy, the placement, and the warm targets. The pool record in Postgres is the pool's source of truth. |
| Pool record | The pool's row in the `pools` table, written through the [admin API pool operations](../../interfaces/admin-api/pools.md) and the bootstrap seed. |
| <a id="pool-name"></a>Pool name | The primary key of the pool record. POOL-R2 states the rule a pool name meets. |
| <a id="pod-set"></a>Pod set | The warm pods of one pool built for one pod-set key. A pool has one pod set outside a rollout and one per distinct pod-set key during one. Each pod set is recorded as one [`PodSet`](../../interfaces/kubernetes/podset.md). |
| `key16` | The first 16 characters of a pod-set key. |
| Warm inventory | The pods of a pod set in the `warming` or `idle` phase that are not pinned to a tenant. |
| Warm target | The number of unpinned warm pods a pod set holds: its `minWarm`, capped at its `maxWarm`, or the reservation floor of POOL-R22 on an owned pool. |
| Pool label | The meaning of the label key [`lenny.dev/pool`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-pool): the pool a `PodSet`, an `AgentPod`, or a pod belongs to. |
| Pod-set label | The meaning of the label key [`lenny.dev/pod-set`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-pod-set): the `key16` of the pod set a `PodSet`, an `AgentPod`, or a pod belongs to. |
| Bootstrap mode | The scaling mode of a pool that has too little demand history for the scaling formula, during which its `minWarm` comes from `bootstrapMinWarm`. |
| Pool phase | `active` or `draining`. A pool enters `draining` through a drain and leaves it by deletion. <!-- unresolved: §15.1 names the `draining` phase and §4.6.2 names "pre-warm, ramp, steady state, wind-down" as automatic pool phases; no source defines the second list as states, so this page records only `active` and `draining`. --> |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Runtime | A registered agent runtime that a pool runs ([Runtime registry](runtime-registry.md#runtime)). |
| Revision | One recorded version of a runtime's versioned fields ([Runtime revisions and rollouts](runtime-revisions.md#revision)). |
| Pod hash | The digest of a revision's pod-affecting fields ([Runtime revisions and rollouts](runtime-revisions.md#pod-hash)). |
| Pod-set key | The digest that identifies a pod set within its pool, computed from a pod hash and the pod-affecting contributions of the pool and the derived runtime ([Runtime revisions and rollouts](runtime-revisions.md#pod-set-key)). |
| Rollout | The movement of a runtime's traffic from one revision to another ([Runtime revisions and rollouts](runtime-revisions.md#rollout)). |
| Agent pod | A Kubernetes pod that serves sessions for one pool ([Agent pod lifecycle](agent-pod-lifecycle.md#agent-pod)). |
| Phase | The coarse occupancy state of an agent pod ([Agent pod lifecycle](agent-pod-lifecycle.md#phases)). |
| Pinned idle pod | An idle agent pod that only one tenant can claim ([Pod claims](pod-claims.md#pinned-idle-pod)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([Pod claims](pod-claims.md#reserved-hold)). |
| Owned pool | The pool of a tenant-derived runtime, named after it and serving only its owner ([Tenant-derived runtimes](tenant-derived-runtimes.md#owned-pool)). |
| Reservation | A tenant's floor of pods on an owned pool ([Tenant-derived runtimes](tenant-derived-runtimes.md#capacity-reservation)). |
| Placement | The node class, offering, region, and tenancy a pool's pods are rendered with ([Placement](placement.md#placement)). |
| Quarantine | The forensic hold that keeps an agent pod and its volumes for investigation ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#quarantine)). |
| Slot count | The number of sessions an agent pod was rendered to serve at once ([Agent pod lifecycle](agent-pod-lifecycle.md#slot-count)). |

## Overview {#overview}

Starting an agent pod takes seconds to minutes: the image is pulled, the containers start, the platform's binaries are staged, and the warm repository stores are filled. A pool hides that time by starting pods before any session asks for one. When a session arrives, it claims a pod that is already ready.

A pool is a record that an operator writes through the admin API. Kubernetes never sees the pool record directly. The PoolScalingController renders it into one or more `PodSet` objects, one per pod set, and the WarmPoolController creates the pods of each pod set. Outside a rollout a pool has one pod set. During a rollout, or after a change to what a derived runtime or the pool contributes to its pods, a pool has one pod set per distinct pod-set key, and the pool's warm target is split among them.

Two labels record which pool and which pod set a pod belongs to. Work that concerns the whole pool, such as recycle's lookup of the pool's policy, selects by the pool label. Work that concerns particular pod sets, such as a claim or a disruption budget, selects by both labels. The labels are copies of the authoritative record, which is the `AgentPod`'s reference to its `PodSet`.

Each pod set keeps its warm inventory near its warm target. The scaling formula sizes the target from demand, from the time a pod takes to warm, and from the worst-case controller failover, so that the pool does not run out while the controller is unable to create pods. A pool with no demand history starts in bootstrap mode with an operator-chosen floor.

## Model {#model}

### Pool {#model-pool}

- A pool belongs to one runtime and names it in `runtimeRef`. A pool on a tenant-derived runtime is an owned pool and takes the runtime's name ([Tenant-derived runtimes](tenant-derived-runtimes.md#owned-pool)).
- A pool's identity is its name. A pool is never renamed, and a deleted pool's name stays taken.
- A pool's behavioral settings are owned by the capabilities that use them: the warm-target settings on this page, the claim settings on [Pod claims](pod-claims.md#settings), the recycle settings on [Agent pod lifecycle](agent-pod-lifecycle.md#settings), the placement on [Placement](placement.md), and the isolation profile on [Isolation profiles](isolation-profiles.md). <!-- unresolved: the pool's remaining fields (egress, checkpoint cadence, grace periods, workspace size limits, cleanup commands) are owned by capabilities outside this slice; their owner pages are listed in the admin API page. -->

### Pod set {#model-pod-set}

- A pod set belongs to one pool and is identified within it by its pod-set key. `{pool, pod-set key}` is unique.
- A pod set records the pool settings its pods are built with, its warm targets, its pod hash, its full pod-set key, and the lowest-numbered revision that has its pod hash.
- A pod set owns its `AgentPod` objects as their controller owner for their whole life.
- A pod set exists while an active revision has its pod hash or a session uses it, and while a quarantined `AgentPod` of it exists (POOL-R7).

### Invariants {#invariants}

- The text of a `PodSet` name before its first `.` is its pool name, and the text after it is its `key16`.
- No agent pod carries a pool label or a pod-set label that differs from its `AgentPod`'s `spec.podSetRef`.
- No agent pod is selected by more than one warm-pod disruption budget.

## Requirements {#requirements}

### Pool records and pod sets

<a id="pool-r1"></a>**POOL-R1.** The pool record is the source of truth for a pool's configuration. A pool is created, changed, and deleted only through the [admin API pool operations](../../interfaces/admin-api/pools.md) and the bootstrap seed, and its `PodSet` objects are derived state that the PoolScalingController reconciles from the pool record. A change to a pool record reaches only pods created after the change; an existing pod keeps the settings it was built with. <!-- src: §4.6.2 "CRDs become derived state"; DR-5 "Template copy" -->

<a id="pool-r2"></a>**POOL-R2.** A pool name matches `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`: one to 63 characters of lower-case letters, digits, and `-`, beginning and ending with a letter or a digit. A platform pool name does not contain `--`, which tenant-derived runtime names use. A pool create, its dry-run preview, and a bootstrap seed entry with any other name are refused before anything is written. A pool is never renamed, and the name of a deleted pool stays taken. <!-- src: PLC-7 "Pool names."; PLCR-15; DR "Names" -->

The pattern leaves out `_` and `.`, although a Kubernetes label value admits them. A pool name becomes a label value, the first part of every `PodSet` name, and the base of every agent pod name, and an object name admits no `_`. Leaving out `.` makes every pool name a DNS label, the narrowest Kubernetes name form, and lets the first `.` of a `PodSet` name separate the pool from the key. The admin API states how the refusal is reported ([`admin-api#pools.create`](../../interfaces/admin-api/pools.md#pools-create)).

<a id="pool-r3"></a>**POOL-R3.** The PoolScalingController renders each pool and pod-set key as one `PodSet` whose spec carries the pool settings the pod builder, the WarmPoolController, and the admission webhooks read, together with the warm targets, and writes that spec in one apply. <!-- src: PLCR-4; PLC-2 "The merge." -->

<a id="pool-r4"></a>**POOL-R4.** Every `PodSet` is named `<pool>.<key16>`, inside and outside a rollout. Before it applies a `PodSet`, the PoolScalingController reads `spec.podSetKey` on any `PodSet` that already holds the computed name. When that key differs from the key it computed, it writes nothing for the new key and sets the condition `Degraded` with the reason `PodSetKeyCollision` on the existing `PodSet`. <!-- src: PLCR-17; PLC-2 "Pod-set names." -->

Two distinct keys of one pool share `key16` with probability below n²/2⁶⁵ for n keys, so the collision path exists to keep a collision from silently merging two pod sets rather than to handle an expected event.

<a id="pool-r5"></a>**POOL-R5.** The pool label and the pod-set label are required and immutable on every `PodSet`, `AgentPod`, and agent pod. The PoolScalingController sets the pool label to the pool name and the pod-set label to `key16` when it creates a `PodSet`, and the WarmPoolController copies both values onto each `AgentPod` it creates and onto its pod. An `AgentPod`'s `spec.podSetRef` and its controller owner reference are the authoritative record of its pod set, and its labels equal the parts of `spec.podSetRef` before and after its first `.` from creation on. The Kubernetes API refuses a write that breaks this rule, as [`PodSet` admission](../../interfaces/kubernetes/podset.md#admission) and [`AgentPod` admission](../../interfaces/kubernetes/agentpod.md#admission) state. <!-- src: PLCR-13; PLCR-14; PLC-2 "Pool and pod-set labels." -->

The pool label is immutable because the claimer, recycle, and an operator's commands select by it and trust it: a pod relabelled to another pool would be claimable by sessions of a pool whose isolation profile, egress profile, and runtime it was not built with. The pod-set label is immutable because a pod whose pod-set label changes leaves its disruption budget and the claim's selector, and nothing restores it.

<a id="pool-r6"></a>**POOL-R6.** Every selector over a pool's objects uses the scope that matches its work:

- Work on one pod set lists its `AgentPod` objects by controller owner reference or by `spec.podSetRef`.
- Work on particular pod sets selects by the pool label together with the pod-set label. This covers the claim set of [CLAIM-R4](pod-claims.md#claim-r4), the warm-pod disruption budget of POOL-R16, and the listing of pinned idle pods.
- Work on the whole pool selects by the pool label alone. This covers recycle's lookup of the pool record's policy, topology spread, the pool diagnosis, and an operator's `kubectl` commands.

No selector names the pod-set label without the pool label, because two pools on one revision with no contributions of their own compute the same key. <!-- src: PLC-2 "Pool and pod-set labels."; RV-3 -->

<a id="pool-r7"></a>**POOL-R7.** A pool keeps one `PodSet` per distinct pod-set key. The WarmPoolController deletes a `PodSet` once no active revision has its pod hash and no session uses it, or once the contributions it was built for have been replaced and its last pod has left. It never deletes an `AgentPod` whose pod carries the quarantine label: the `PodSet` stays while such an `AgentPod` exists, and the WarmPoolController reports the `AgentPod` to the forensics reconciler ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#retirement-paths)). <!-- src: RV-3 bullet 4; FE-1 "Retirement paths" -->

<a id="pool-r8"></a>**POOL-R8.** After a change to what a derived runtime or the pool contributes to its pods, the earlier pod set's idle pods that hold a store no longer on the rendered warm list drain, and its other pods stay claimable until they leave under the warm stores' age limit ([Tenant-derived runtimes](tenant-derived-runtimes.md#changing-the-list)). New pods are built under the new pod-set key. <!-- src: DR-11 "Changing the list"; RV-3 "A change to the contributions." -->

### Warm targets and scaling

<a id="pool-r9"></a>**POOL-R9.** The WarmPoolController keeps each pod set's warm inventory at its warm target. When a warm pod leaves warm inventory, through a claim, an eviction, a failure, or a drain, it creates a replacement. When warm inventory exceeds the warm target, it drains surplus idle pods. <!-- src: §4.6.1 "Responsibilities:"; "Disruption protection for agent pods:" -->

<a id="pool-r10"></a>**POOL-R10.** The PoolScalingController computes each pool's `minWarm` from the pool's demand history:

```
minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_warmup_seconds)
               + burst_p99_claims × pod_warmup_seconds) / mode_factor
```

`base_demand_p95` is the pool's p95 claim arrival rate in claims per second, and `burst_p99_claims` is its p99 claim arrival rate over a short window. `failover_seconds` is the controllers' worst-case crash failover window ([WarmPoolController failure and recovery](../../components/warm-pool-controller.md#failure-and-recovery)). `pod_warmup_seconds` is the pool's `scalePolicy.podWarmupSecondsBaseline`. `safety_factor` is the setting `poolScaling.safetyFactor`. `mode_factor` is the observed number of sessions a pod serves over its life on a recycling pool, and the pod's slot count on a pool whose pods serve several sessions at once; the burst term divides by the slot count alone. <!-- src: §4.6.2 "Default formula (non-experiment pools)"; §5.2 "Execution Mode Scaling Implications" --> <!-- unresolved: §5.2 "Execution Mode Scaling Implications" states the mode-adjusted formula with separate `mode_factor` and `burst_mode_factor`; AR-2 removes its service-mode variant and the runtime routing design's RT-1 removes the experiment variant formulas, and no design restates the session-mode variant, so this requirement carries the session-mode form as the slice reads it. -->

The first term covers the claims that arrive while the controller cannot create pods and while a replacement warms. The second term covers bursts that arrive faster than the pool refills outside a failover. Both terms use the same warm-up time, because both measure how many claims arrive before a replacement is ready.

<a id="pool-r11"></a>**POOL-R11.** The PoolScalingController splits a pool's warm target across its pod sets by the summed traffic weight of the active revisions in each, with a floor of one pod for a pod set whose revisions hold positive weight ([Runtime revisions and rollouts](runtime-revisions.md#warm-before-weight)). <!-- src: RV-3 bullet 9 -->

<a id="pool-r12"></a>**POOL-R12.** A new pool enters bootstrap mode, with `status.scalingMode: bootstrap`, and takes `minWarm` from `bootstrapMinWarm` until its demand history meets the convergence criteria, after which the formula of POOL-R10 sets it. An operator ends bootstrap mode early through [`admin-api#pools.deleteBootstrapOverride`](../../interfaces/admin-api/pools.md#pools-deletebootstrapoverride). <!-- src: §4.6.2 "Cold-start limitation:" --> <!-- unresolved: the convergence criteria and the fallback starting values live in §17.8.2 "First deployment sizing", which this slice did not migrate. -->

<a id="pool-r13"></a>**POOL-R13.** During a `scalePolicy.scaleToZero` window the PoolScalingController sets the pod set's `minWarm` to 0, and at `resumeAt` it restores the computed value. Both cron expressions are read in UTC unless `timezone` names an IANA zone. A session that arrives while a pool holds no warm pod is served by a pod created on demand, at cold-start latency. <!-- src: §4.6.1 "Idle cost visibility and scale-to-zero:"; "Scaling:" -->

<a id="pool-r14"></a>**POOL-R14.** Pinned idle pods are accounted apart from warm inventory:

- They do not count toward `minWarm`. `status.warmCount`, `status.readyCount`, the pool's idle pod count, the `PoolWarmingUp` condition, and the idle-pods gauge count unpinned pods only.
- They count toward `maxWarm`. The WarmPoolController keeps at most `maxWarm` minus the warm target pinned idle pods per pod set and drains the rest. <!-- unresolved: §5.2 does not state which pinned idle pods drain first, and states the bound per pool; after the split into pod sets it is taken per pod set. -->
- They accrue idle pod-minutes like every idle pod.

A pinned idle pod has no idle timeout of its own. <!-- src: §5.2 "Pinned idle inventory." -->

<a id="pool-r15"></a>**POOL-R15.** A pod in the reserved hold counts as occupied for inventory and scaling. <!-- src: §4.6.2 "Reserved pods count as occupied."; §6.2 "`reserved` hold semantics." -->

<a id="pool-r16"></a>**POOL-R16.** Each `PodSet` with `minWarm` of 2 or more has one PodDisruptionBudget, named `<podSet>-warm`, owned by the `PodSet`, selecting the pool label, the pod-set label, and [`lenny.dev/state: idle`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-state), with an integer `minAvailable` of `minWarm - 1`. A `PodSet` with `minWarm` below 2 has none. <!-- src: PLCR-13; §4.6.1 "Disruption protection for agent pods:" -->

The budget uses an integer `minAvailable` because the agent pods' controller, the `AgentPod`, has no `/scale` subresource, so a `maxUnavailable` budget cannot compute the expected pod count and would refuse every eviction. `minWarm - 1` admits one eviction at the normal steady state of `minWarm` idle pods, and `minWarm` would admit none and stall node drains. A pool with more idle pods than `minWarm` admits more than one eviction at once, which is accepted because the selected pods carry no session and POOL-R9 replaces them. The budget is per pod set rather than per pool because each pod set has its own `minWarm` and its own reconcile, and because the eviction API refuses to evict a pod that two budgets select. During a rollout each pod set admits its own eviction, so a node drain can take one idle pod from each pod set at once.

<a id="pool-r17"></a>**POOL-R17.** The PoolScalingController writes default topology spread constraints into each `PodSet`: a soft spread across zones and a soft spread across nodes, each with `maxSkew: 1` and `whenUnsatisfiable: ScheduleAnyway`, both selecting the pool label. A pool's `topologySpreadConstraints` replaces the defaults. The WarmPoolController copies the constraints into each `AgentPod` and pod at creation. <!-- src: §5.2 "Topology spread constraints:"; PM-5 item 6; PLC-2 "The whole pool." -->

### Warm-up visibility

<a id="pool-r18"></a>**POOL-R18.** The WarmPoolController sets the `PodSet` condition `PoolWarmingUp` to `True` with the reason `Provisioning` while `minWarm` is above 0, no unpinned pod is idle, and at least one pod is warming, and with the reason `Drained` when no unpinned pod is idle and none is warming. It sets the condition to `False` once an unpinned pod is idle. While every pod set of a pool reports `PoolWarmingUp`, the gateway answers a session create for the pool with a retryable error that carries the pool name, the number of pods warming, and an estimate of the time to the first ready pod ([`client-api#sessions.create`](../../interfaces/client-api/sessions.md#sessions-create)). <!-- src: §5.2 "`PoolWarmingUp` condition."; "Client-facing `503 Pool Not Ready` response." --> <!-- unresolved: §5.2 states the condition per template, which is per pod set after the merge; no design states whether a pool with one warmed pod set and one warming pod set reports warming. -->

<a id="pool-r19"></a>**POOL-R19.** The `WarmPoolExhausted` and `WarmPoolLow` alerts are suppressed for a pod set during the fill grace period, which starts when the pod set is created, when the WarmPoolController starts, and when the pod set's `minWarm` changes from 0 to a positive value, and lasts `initialFillGracePeriod`. <!-- src: §4.6.1 "Cold-start pool fill:"; "Re-activation grace period:" -->

### Owned pools

<a id="pool-r20"></a>**POOL-R20.** An owned pool's warm target is its `warmCount`, which is at least 1, unless a reservation raises it under POOL-R21. The PoolScalingController never raises an owned pool's `minWarm` above that target, and a `bootstrapMinWarm` on an owned pool is refused. <!-- src: DR-5 "Bounds" (last paragraph) -->

<a id="pool-r21"></a>**POOL-R21.** The WarmPoolController computes an owned pool's warm target on each reconcile as `max(minWarm, reservedPods - active)`, where `active` counts the pool's `AgentPod` objects in `claimed` and `reserved`, and uses that target as both the floor and the ceiling of the pod set's plan, so pinned idle pods get no room on an owned pool. While the pool has fewer than `reservedPods` non-terminal `AgentPod` objects marked `spec.reserved`, a new `AgentPod` is marked. The WarmPoolController drains unmarked idle pods before marked ones, and while marked `AgentPod` objects are below `reservedPods`, it replaces an unmarked `AgentPod` that is warming with no pod by a marked one. During a base rollout only the pod set of the revision with the highest weight carries the floor and the marks. <!-- src: DR-10 "Mechanism"; "Budget" -->

<a id="pool-r22"></a>**POOL-R22.** The WarmPoolController creates no `AgentPod` in an owned pool while the owning tenant's non-terminal `AgentPod` objects, across all its owned pools, are at the bound the PoolScalingController renders from the tenant's grant ([Tenant-derived runtimes](tenant-derived-runtimes.md#pod-budget)). <!-- src: DR-5 "Bounds" ("`maxOwnedPods`, at creation") -->

### Configuration drift, drain, and deletion

<a id="pool-r23"></a>**POOL-R23.** Each pool record carries `pool_config_generation`, which every admin API write increments, and the PoolScalingController stamps the generation it rendered into each `PodSet` as the annotation [`lenny.dev/config-generation`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-config-generation). The gateway compares the two and reports the pool's sync status as `pending` while they differ and `synced` once they match. The comparison runs in the gateway, so a missing PoolScalingController is detected. <!-- src: §4.6.2 "Reconciliation drift detection." -->

<a id="pool-r24"></a>**POOL-R24.** When the admission webhook refuses a `PodSet` apply with any 4xx answer other than a conflict, the PoolScalingController does not retry at once. It pauses that `PodSet` for 1 second, doubles the pause on each consecutive refusal up to 60 seconds, and resets it on a successful apply. After `admissionDeniedRetryCeiling` consecutive refusals it stops retrying that `PodSet` until the pool record's generation changes, a new leader is elected, or an operator calls [`admin-api#pools.resumeReconciliation`](../../interfaces/admin-api/pools.md#pools-resumereconciliation). Refusals on one pool never pause another. <!-- src: §4.6.3 "PoolScalingController admission-denial handling policy." --> <!-- unresolved: the source keys the state on `(pool, crd)` with two kinds per pool; after the merge into `PodSet` the key is taken to be the `PodSet`. -->

A refusal of this kind is a deterministic function of the pool record the controller has read, so an immediate retry reproduces it and loads the webhook and the API server. A stale read clears on the next read, and a persistent configuration error stops the pool where an operator sees it.

<a id="pool-r25"></a>**POOL-R25.** A pool drain moves the pool to `draining`. While a pool is draining, no warm pod of it is assigned to a new session, a session create that would select it is refused with a retryable error that carries the estimated drain time, its sessions run to completion or until their age limit, and its pods are then removed. <!-- src: §15.1 `POST /v1/admin/pools/{name}/drain` --> <!-- unresolved: §15.1 computes the retry estimate from "the longest active session age in the pool (capped at `maxSessionAgeSeconds`)"; whether that is the elapsed or the remaining age is not stated. -->

<a id="pool-r26"></a>**POOL-R26.** A pool is deleted only when no session in it is running or suspended. Deleting a pool deletes its `PodSet` objects, apart from a `PodSet` that still owns a quarantined `AgentPod` (POOL-R7). <!-- src: §15.1 "Deletion semantics" ("Pool:"); FE-1 "Retirement paths" -->

## Flows {#flows}

### A pool is created {#flow-create}

1. An operator calls [`admin-api#pools.create`](../../interfaces/admin-api/pools.md#pools-create). The gateway checks the name under POOL-R2 and the configuration, writes the pool record, and answers with `syncStatus: pending`.
2. The [PoolScalingController](../../components/pool-scaling-controller.md) reads the pool record, computes the pod-set key from the revision's pod hash and the contributions ([Runtime revisions and rollouts](runtime-revisions.md#pod-set-key)), and applies one `PodSet` named under POOL-R4 with both labels (POOL-R5).
3. The [`lenny-pool-config-validator`](../../components/admission-webhooks.md#pool-config-validator) webhook admits the `PodSet` ([`PodSet` admission](../../interfaces/kubernetes/podset.md#admission)).
4. The [WarmPoolController](../../components/warm-pool-controller.md) creates `AgentPod` objects and pods up to the warm target (POOL-R9), each through [Agent pod creation](agent-pod-lifecycle.md#apod-r2), creates the disruption budget (POOL-R16), and sets `PoolWarmingUp` (POOL-R18).
5. The gateway reads the `PodSet`'s generation annotation and reports `syncStatus: synced` (POOL-R23).

### A revision with a new pod hash enters a pool {#flow-rollout}

1. The rollout gives the new revision a weight ([Runtime revisions and rollouts](runtime-revisions.md#rollout)).
2. The PoolScalingController applies a second `PodSet` for the new key and splits the warm target (POOL-R11).
3. The WarmPoolController warms the new pod set. Each pod set's reconcile counts only its own `AgentPod` objects (POOL-R6), and each pod set has its own disruption budget (POOL-R16).
4. When the old revision leaves the traffic and its sessions end, the WarmPoolController deletes the old `PodSet` (POOL-R7).

## Settings {#settings}

```lenny-def
kind: setting
name: minWarm
scope: pool
type: integer
default: none (bootstrapMinWarm applies in bootstrap mode)
range: ">= 0, <= maxWarm"
summary: The number of unpinned warm pods each pod set of the pool holds, computed under POOL-R10 outside bootstrap mode and split under POOL-R11.
```

```lenny-def
kind: setting
name: maxWarm
scope: pool
type: integer
default: none
range: "> 0, >= minWarm"
summary: The ceiling on a pod set's warm inventory plus its pinned idle pods (POOL-R14).
```

```lenny-def
kind: setting
name: bootstrapMinWarm
scope: pool (scalePolicy)
type: integer
default: none
range: ">= 0; refused on an owned pool"
summary: The minWarm a pool holds in bootstrap mode (POOL-R12).
```

```lenny-def
kind: setting
name: scalePolicy.podWarmupSecondsBaseline
scope: pool
type: integer (seconds)
default: 30
range: "> 0"
summary: The pod_warmup_seconds input of the scaling formula (POOL-R10); tune it to the observed time from pod creation to idle.
```

```lenny-def
kind: setting
name: scalePolicy.scaleToZero
scope: pool
type: object {schedule: cron, resumeAt: cron, timezone: IANA zone name}
default: absent
range: "timezone defaults to UTC"
summary: A daily window during which the pool's minWarm is 0 (POOL-R13).
```

```lenny-def
kind: setting
name: topologySpreadConstraints
scope: pool
type: list of Kubernetes TopologySpreadConstraint
default: soft zone spread and soft node spread (POOL-R17)
summary: Replaces the default spread of the pool's pods.
```

```lenny-def
kind: setting
name: poolScaling.safetyFactor
scope: deployment (Helm)
type: number
default: per capacity tier
range: "> 0"
summary: The safety_factor of the scaling formula (POOL-R10).
```
<!-- unresolved: §4.6.2 gives 1.5 for agent pools and 2.0 for mcp pools at Tiers 1 and 2 and points to §17.8.2 for the per-tier values; AR-1 removes `type: mcp`, so only the agent value remains, and the per-tier table was not migrated. -->

```lenny-def
kind: setting
name: initialFillGracePeriod
scope: WarmPoolController flag --initial-fill-grace-period
type: duration
default: 120s
summary: The fill grace period of POOL-R19, during which warm-pool exhaustion alerts are suppressed.
```

```lenny-def
kind: setting
name: admissionDeniedRetryCeiling
scope: PoolScalingController
type: integer
default: 10
range: ">= 1"
summary: The consecutive admission refusals after which the PoolScalingController stops retrying a PodSet (POOL-R24).
```

```lenny-def
kind: setting
name: warmupDeadlineSeconds
scope: deployment
type: integer (seconds)
default: 300
summary: How long a pod set may report PoolWarmingUp before WarmPoolBootstrapping fires.
```

```lenny-def
kind: setting
name: warmCount
scope: owned pool
type: integer
default: none
range: ">= 1, within the tenant's pod budget"
summary: The warm target of an owned pool (POOL-R20).
```

```lenny-def
kind: setting
name: reservedPods
scope: owned pool
type: integer
default: 0
range: "<= the tenant's allowance for the base and resource class"
summary: The reservation floor of an owned pool (POOL-R21).
```

## Alerts {#alerts}

```lenny-def
kind: alert
name: WarmPoolLow
severity: warning
expr: lenny_warmpool_idle_pods < 0.25 × minWarm for any pod set, outside the fill grace period
detects: POOL-R9
summary: A pod set's idle pods dropped below a quarter of its minWarm.
```

```lenny-def
kind: alert
name: WarmPoolExhausted
severity: critical
expr: lenny_warmpool_idle_pods == 0 for any pod set for more than 60s, outside the fill grace period
detects: POOL-R9
summary: A pod set has no idle pod.
```

```lenny-def
kind: alert
name: WarmPoolBootstrapping
severity: warning
expr: PoolWarmingUp is True for longer than warmupDeadlineSeconds on a pod set with minWarm > 0
detects: POOL-R18
summary: A pod set has not produced its first idle pod within the warm-up deadline.
```

```lenny-def
kind: alert
name: WarmPoolReplenishmentSlow
severity: warning
expr: p95 of lenny_warmpool_pod_startup_duration_seconds > 2 × scalePolicy.podWarmupSecondsBaseline for more than 5 min
detects: POOL-R9
summary: Pods warm more slowly than the scaling formula assumes.
```

```lenny-def
kind: alert
name: WarmPoolReplenishmentFailing
severity: warning
expr: rate(lenny_warmpool_warmup_failure_total) > 1/min for any pool for more than 5 min
detects: POOL-R9
summary: Warm pods fail to reach idle; the error_type label names the cause.
```

```lenny-def
kind: alert
name: PoolScaleoutBlockedByQuota
severity: warning
expr: increase(lenny_warmpool_warmup_failure_total{error_type="resource_quota_exceeded"}[5m]) > 0 for more than 2 min
detects: POOL-R9
summary: The namespace ResourceQuota refuses warm-pod creation.
```

```lenny-def
kind: alert
name: WarmPoolIdleCostHigh
severity: warning
expr: increase(lenny_warmpool_idle_pod_minutes[24h]) above the deployer's threshold for any pool
detects: POOL-R10
summary: A pool's idle pods cost more than the deployer set; minWarm or the scale-to-zero window may need tuning.
```
<!-- unresolved: the threshold's setting name is not stated in §4.6.1 or §16.5. -->

```lenny-def
kind: alert
name: PoolBootstrapMode
severity: warning
expr: status.scalingMode == bootstrap for more than 72h on any pool
detects: POOL-R12
summary: A pool has not converged to formula-driven scaling.
```

```lenny-def
kind: alert
name: PoolConfigDrift
severity: warning
expr: pool_config_generation differs from the PodSet's lenny.dev/config-generation for more than 60s
detects: POOL-R23
summary: The PoolScalingController has not reconciled a pool's latest configuration.
```

```lenny-def
kind: alert
name: PoolScalingAdmissionStuck
severity: warning
expr: increase(lenny_pool_scaling_admission_denied_total{pool, pod_set}[5m]) >= admissionDeniedRetryCeiling with no successful apply
detects: POOL-R24
summary: The admission webhook keeps refusing a pool's rendered PodSet, and the PoolScalingController has stopped retrying it.
```

```lenny-def
kind: alert
name: PoolConfigValidatorUnavailable
severity: warning
expr: lenny-pool-config-validator unreachable for more than 30s
detects: POOL-R1
summary: PodSet writes, including the PoolScalingController's, are refused until the webhook returns.
```

## Failure behavior {#failure}

- **PoolScalingController down.** Pod sets keep their last rendered spec, and the WarmPoolController keeps reconciling against it. Warm targets stop following demand, and configuration changes stop reaching Kubernetes. POOL-R23 reports the drift.
- **WarmPoolController down.** No pod is created or replaced, and warm inventory falls as sessions claim pods. Sessions already running continue. The scaling formula sizes `minWarm` to cover this window (POOL-R10).
- **Both controllers down.** The effect equals the WarmPoolController's absence alone, because the PoolScalingController only adjusts values the WarmPoolController reads.
- **Admission webhook down.** Every `PodSet` write is refused, including the PoolScalingController's, so configuration changes stop until the webhook returns.
- **API server or etcd unavailable.** Pool reconciliation freezes, and the controllers replay their work queues once it returns.

## Security properties {#security}

- A pod built for one pool cannot serve another pool's sessions, because the pool label is immutable and the claim selects by it (POOL-R5, POOL-R6).
- A pool's configuration reaches Kubernetes only through the PoolScalingController, so a manual edit of a `PodSet` is refused rather than silently overwritten ([`PodSet` admission](../../interfaces/kubernetes/podset.md#admission)).
- An owned pool's pods are created only within its owner's pod bound (POOL-R22).

## Rationale {#rationale}

- **One kind per pod set.** The pool settings and the warm targets of one pod set are written, named, and deleted together, so they are one object. An operator reads a pool's settings and its warm counts with one `kubectl get podsets -l lenny.dev/pool=<pool>`.
- **Two labels.** During a rollout a pool has several pod sets. If pods carried the pod set name in the pool label, recycle and an operator's commands would not find the new revision's pods. If they carried the pool name alone and each reconcile, budget, and claim selected by it, each reconcile would count the other pod set's pods, two budgets would select the same pods and stall node drains, and a session would bind to a pod of a revision it did not choose.
- **Fixed-length pod-set label.** A full pod-set key is 64 characters, one more than a label value holds, so the label carries `key16` and the `PodSet` spec carries the full key.

## Related capabilities {#related}

- [Pod claims](pod-claims.md) takes pods from a pool's warm inventory.
- [Agent pod lifecycle](agent-pod-lifecycle.md) states how each pod enters and leaves warm inventory.
- [Runtime revisions and rollouts](runtime-revisions.md) creates the pod-set keys this page names.
- [Tenant-derived runtimes](tenant-derived-runtimes.md) owns the owned pool's budget and the warm stores.
- [Placement](placement.md) renders where a pod set's pods run.
