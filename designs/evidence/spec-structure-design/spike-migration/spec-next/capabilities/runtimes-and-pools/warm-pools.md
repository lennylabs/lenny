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
depends-on: [RUNTIME, REV, DERIVED, PLACE, CLAIM, APOD, SLOT, FORENSIC, KPSET, PSC, WPC]
---

# Warm pools

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="pool"></a>Pool | A warmable deployment target for one runtime and one operational profile: the runtime, the isolation profile, the resource class, the egress profile, the session policy, the placement, and the warm targets. |
| <a id="pool-record"></a>Pool record | The pool's row in the `pools` table, written through the admin API and the bootstrap seed. The pool record is the source of truth for the pool's configuration. |
| <a id="pool-name"></a>Pool name | The primary key of the pool record (POOL-R2). |
| <a id="pod-set"></a>Pod set | The warm pods of one pool built for one pod-set key. Each pod set is recorded as one `PodSet`. |
| <a id="key16"></a>`key16` | The first 16 characters of a pod-set key. |
| <a id="warm-inventory"></a>Warm inventory | The pods of a pod set in the `warming` or `idle` phase that carry no tenant pin. |
| <a id="warm-target"></a>Warm target | The number of unpinned warm pods a pod set holds: its `minWarm`, capped at its `maxWarm`, or the reservation floor of POOL-R21 on an owned pool. |
| <a id="pool-label"></a>Pool label | The meaning of the key `lenny.dev/pool`: the pool a `PodSet`, an `AgentPod`, or a pod belongs to. |
| <a id="pod-set-label"></a>Pod-set label | The meaning of the key `lenny.dev/pod-set`: the `key16` of the pod set a `PodSet`, an `AgentPod`, or a pod belongs to. |
| <a id="bootstrap-mode"></a>Bootstrap mode | The scaling mode of a pool with too little demand history for the scaling formula, during which its `minWarm` comes from `bootstrapMinWarm`. |
| <a id="pool-phase"></a>Pool phase | `active` or `draining`. A pool enters `draining` through a drain and leaves it by deletion. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Runtime | A registered agent runtime that a pool runs ([Runtime registry](runtime-registry.md)). |
| Revision | One recorded version of a runtime's versioned fields ([Runtime revisions](runtime-revisions.md)). |
| Pod hash | The digest of a revision's pod-affecting fields ([Runtime revisions](runtime-revisions.md)). |
| Pod-set key | The digest that identifies a pod set within its pool ([Runtime revisions](runtime-revisions.md)). |
| Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool ([APOD](agent-pod-lifecycle.md#agent-pod)). |
| Pod phase | The coarse occupancy state of an agent pod ([APOD](agent-pod-lifecycle.md#pod-phase)). |
| Pinned idle pod | An idle agent pod that only one tenant can claim ([CLAIM](pod-claims.md#pinned-idle-pod)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([CLAIM](pod-claims.md#reserved-hold)). |
| Owned pool | The pool of a tenant-derived runtime, which serves only its owner ([Tenant-derived runtimes](tenant-derived-runtimes.md)). |
| Placement | The node class, offering, region, and tenancy a pool's pods are rendered with ([Placement](placement.md)). |
| Slot count | The number of slot containers rendered into a pod ([APOD](agent-pod-lifecycle.md#slot-count)). |

## Overview {#overview}

Starting an agent pod takes seconds to minutes: the image is pulled, the containers start, the platform's binaries are staged, and the warm repository stores are filled. A pool hides that time by starting pods before any session asks for one, so a session claims a pod that is already ready.

A pool is a record that an operator writes through the admin API. The PoolScalingController renders it into one or more `PodSet` objects, one per pod set, and the WarmPoolController creates the pods of each pod set. Outside a rollout a pool usually has one pod set. During a rollout, or after a change to what a derived runtime or the pool contributes to its pods, a pool has one pod set per distinct pod-set key, and the pool's warm target is split among them.

Two labels record which pool and which pod set a pod belongs to. Work that concerns the whole pool selects by the pool label. Work that concerns particular pod sets, such as a claim or a disruption budget, selects by both labels. The labels are copies of the authoritative record, which is the `AgentPod`'s reference to its `PodSet`.

Each pod set keeps its warm inventory near its warm target. The scaling formula sizes the target from demand, from the time a pod takes to warm, and from the worst-case controller failover, so that the pool does not run out while the controller cannot create pods. A pool with no demand history starts in bootstrap mode with an operator-chosen floor.

## Model {#model}

### Pool {#model-pool}

- A pool belongs to one runtime and names it in `runtimeRef`. A pool on a tenant-derived runtime is an owned pool and takes the runtime's name.
- A pool's identity is its name. A pool is never renamed, and a deleted pool's name stays taken.
- A pool carries no execution mode: every session binds to a slot of a claimed pod.
- A pool's behavioral settings are owned by the capabilities that use them: the warm-target settings on this page, the claim settings on [CLAIM](pod-claims.md#setting-podclaimqueuetimeout), the recycle settings on [APOD](agent-pod-lifecycle.md#setting-sessionpolicy-recycle-enabled), the slot settings on [SLOT](../sessions/session-isolation.md#setting-sessionpolicy-maxconcurrentsessionsperpod), the placement on [Placement](placement.md), and the isolation profile on [Isolation profiles](isolation-profiles.md).

### Pod set {#model-pod-set}

- A pod set belongs to one pool and is identified within it by its pod-set key. The pair of pool and pod-set key is unique.
- A pod set records the pool settings its pods are built with, its warm targets, its pod hash, its full pod-set key, and the lowest-numbered revision that has its pod hash.
- A pod set owns its `AgentPod` objects as their controller owner for their whole life.

### Invariants {#invariants}

- The text of a `PodSet` name before its first `.` is its pool name, and the text after it is its `key16`.
- No agent pod carries a pool label or a pod-set label that differs from its `AgentPod`'s `spec.podSetRef`.
- No agent pod is selected by more than one warm-pod disruption budget.

## Requirements {#requirements}

### Pool records and pod sets

<a id="pool-r1"></a>**POOL-R1. The pool record is the source of truth.** A pool is created, changed, and deleted only through the admin API pool operations ([Admin API pools](../../interfaces/admin-api/pools.md)) and the bootstrap seed. Its `PodSet` objects are derived state that the PoolScalingController reconciles from the pool record, and the Kubernetes API refuses a `PodSet` spec written by anyone else ([`PodSet` admission](../../interfaces/kubernetes/podset.md#crd-podset)). A change to a pool record reaches only pods created after the change. <!-- src: §4.6.2 "CRDs become derived state"; §4.6.3 "Validating webhook for Postgres-authoritative state"; DR-5 "Template copy" -->

<a id="pool-r2"></a>**POOL-R2. A pool name is a DNS label.** A pool name matches `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`: one to 63 characters of lower-case letters, digits, and `-`, beginning and ending with a letter or a digit. A platform pool name does not contain `--`, which tenant-derived runtime names use. A pool create, its dry-run preview, and a bootstrap seed entry with any other name are refused before anything is written, the admin API with `400 VALIDATION_ERROR` naming the field `name` and the seed with the entry error `SEED_VALIDATION`. A pool is never renamed, and the name of a deleted pool stays taken. <!-- src: PLC-7 "Pool names."; PLCR-15; DR "Names" -->

A pool name becomes a label value, the first part of every `PodSet` name, and the base of every agent pod name. An object name admits no `_`, and leaving out `.` makes the first `.` of a `PodSet` name separate the pool from the key.

<a id="pool-r3"></a>**POOL-R3. The PoolScalingController writes each pod set in one apply.** The PoolScalingController renders each pool and pod-set key as one `PodSet` whose spec carries the pool settings the pod builder, the WarmPoolController, and the admission webhooks read, together with the warm targets, and writes that spec in one apply per reconcile. It is the sole writer of a `PodSet`'s spec. <!-- src: PLCR-4; PLC-2 "The merge." -->

<a id="pool-r4"></a>**POOL-R4. A pod set is named from its pool and key16.** Every `PodSet` is named `<pool>.<key16>`, inside and outside a rollout. Before it applies a `PodSet`, the PoolScalingController reads `spec.podSetKey` on any `PodSet` that already holds the computed name. When that key differs from the key it computed, it writes nothing for the new key and sets the condition `Degraded` with the reason [`PodSetKeyCollision`](#condition-reason-podsetkeycollision) on the existing `PodSet`. <!-- src: PLCR-17; PLC-2 "Pod-set names."; RV-3 "Key and names." -->

Two distinct keys of one pool share `key16` with probability below n²/2⁶⁵ for n keys, so the collision path keeps a collision from silently merging two pod sets.

<a id="pool-r5"></a>**POOL-R5. The pool and pod-set labels are required and immutable.** The pool label and the pod-set label are required and immutable on every `PodSet`, `AgentPod`, and agent pod. The PoolScalingController sets the pool label to the pool name and the pod-set label to `key16` when it creates a `PodSet`, and the WarmPoolController copies both values onto each `AgentPod` it creates and onto its pod. An `AgentPod`'s `spec.podSetRef` and its controller owner reference are the authoritative record of its pod set, and its labels equal the parts of `spec.podSetRef` before and after its first `.` from creation on. The Kubernetes API refuses a write that breaks this rule ([`PodSet`](../../interfaces/kubernetes/podset.md#crd-podset) and [`AgentPod`](../../interfaces/kubernetes/agentpod.md#crd-agentpod) admission). The keys are [`lenny.dev/pool`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-pool) and [`lenny.dev/pod-set`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-pod-set). <!-- src: PLCR-13; PLCR-14; PLC-2 "Pool and pod-set labels." -->

The pool label is immutable because the claimer, recycle, and an operator's commands select by it: a pod relabelled to another pool would be claimable by sessions of a pool whose isolation profile, egress profile, and runtime it was not built with. The pod-set label is immutable because a pod whose pod-set label changes leaves its disruption budget and the claim set, and nothing restores it.

<a id="pool-r6"></a>**POOL-R6. Each selector uses the scope of its work.** Every selector over a pool's objects uses the scope that matches its work:

- Work on one pod set lists its `AgentPod` objects by controller owner reference or by `spec.podSetRef`.
- Work on particular pod sets selects by the pool label together with the pod-set label: the claim set of [CLAIM-R4](pod-claims.md#claim-r4), the warm-pod disruption budget of POOL-R16, and the listing of pinned idle pods.
- Work on the whole pool selects by the pool label alone: recycle's lookup of the pool record's policy, topology spread, the pool diagnosis, and an operator's `kubectl` commands.

No selector names the pod-set label without the pool label, because two pools on one revision with no contributions of their own compute the same key. <!-- src: PLC-2 "Pool and pod-set labels."; RV-3 -->

<a id="pool-r7"></a>**POOL-R7. A pod set lives while its pods can serve.** A pool keeps one `PodSet` per distinct pod-set key. The PoolScalingController deletes a `PodSet` once no active revision has its pod hash and no session uses it, or once the contributions it was built for have been replaced and its last pod has left. A `PodSet` that owns an `AgentPod` whose pod carries the quarantine label is not deleted while that `AgentPod` exists, and the WarmPoolController reports the `AgentPod` to the forensics reconciler ([Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md)). <!-- src: RV-3; FE-1 "Retirement paths" --> <!-- judgment: RV-3 says the warm pool controller deletes a `PodSet`; PLC makes the PoolScalingController the single writer of `PodSet` objects; this page follows PLC. -->

<a id="pool-r8"></a>**POOL-R8. An earlier pod set stays claimable after a contribution change.** After a change to what a derived runtime or the pool contributes to its pods, the earlier pod set's idle pods that hold a store no longer on the rendered warm list drain, and its other pods stay claimable until they leave under the warm stores' age limit ([Tenant-derived runtimes](tenant-derived-runtimes.md)). New pods are built under the new pod-set key. <!-- src: DR-11 "Changing the list"; RV-3 "A change to the contributions." -->

### Warm targets and scaling

<a id="pool-r9"></a>**POOL-R9. The WarmPoolController holds each pod set at its warm target.** When a warm pod leaves warm inventory, through a claim, an eviction, a failure, or a drain, the WarmPoolController creates a replacement. When warm inventory exceeds the warm target, it drains surplus idle pods. <!-- src: §4.6.1 "Responsibilities:"; "Disruption protection for agent pods:" -->

<a id="pool-r10"></a>**POOL-R10. The scaling formula sizes minWarm from demand.** The PoolScalingController computes each pool's `minWarm` from the pool's demand history:

```
minWarm = ceil( base_demand_p95 × safety_factor × (failover_seconds + pod_warmup_seconds) / mode_factor
              + burst_p99_claims × pod_warmup_seconds / burst_mode_factor )
```

`base_demand_p95` is the pool's p95 claim arrival rate in claims per second, and `burst_p99_claims` is its p99 claim arrival rate over a short window. `failover_seconds` is the controllers' worst-case crash failover window ([WarmPoolController](../../components/warm-pool-controller.md)). `pod_warmup_seconds` is the pool's `scalePolicy.podWarmupSecondsBaseline`. `safety_factor` is [`poolScaling.safetyFactor`](#setting-poolscaling-safetyfactor). `mode_factor` is the observed number of sessions a pod serves over its life, multiplied by its slot count, and is 1 for a pool that neither recycles nor shares pods. `burst_mode_factor` is the pod's slot count. On a recycling pool with no reuse history, `mode_factor` starts at 1 and converges toward `recycle.maxSessionsPerPod`. <!-- src: §4.6.2 "Default formula"; §5.2 "Execution Mode Scaling Implications"; ARR-12 --> <!-- judgment: ARR-12 keeps "the session-mode formula for every pool, with `mode_factor` from recycling and slots alone"; the product form of the two factors is this migration's reading of §5.2's session bullet. -->

The first term covers the claims that arrive while the controller cannot create pods and while a replacement warms. The second term covers bursts that arrive faster than the pool refills outside a failover, and a pod absorbs as many burst arrivals as it has free slots.

<a id="pool-r11"></a>**POOL-R11. The warm target is split by traffic weight.** The PoolScalingController splits a pool's warm target across its pod sets by the summed traffic weight of the active revisions in each, with a floor of one pod for a pod set whose revisions hold positive weight. <!-- src: RV-3 -->

<a id="pool-r12"></a>**POOL-R12. A new pool starts in bootstrap mode.** A new pool enters bootstrap mode, with `status.scalingMode: bootstrap`, and takes `minWarm` from `scalePolicy.bootstrapMinWarm` until its demand history meets the convergence criteria, after which the formula of POOL-R10 sets it. An operator ends bootstrap mode early through the admin API ([Admin API pools](../../interfaces/admin-api/pools.md)). <!-- src: §4.6.2 "Cold-start limitation:" --> <!-- unresolved: the convergence criteria live in §17.8.2, which this spike did not migrate. -->

<a id="pool-r13"></a>**POOL-R13. A scale-to-zero window sets minWarm to 0.** During a `scalePolicy.scaleToZero` window the PoolScalingController sets the pod set's `minWarm` to 0, and at `resumeAt` it restores the computed value. Both cron expressions are read in UTC unless `timezone` names an IANA zone. A session that arrives while a pool holds no warm pod is served by a pod created on demand, at cold-start latency. <!-- src: §4.6.1 "Idle cost visibility and scale-to-zero:"; "Scaling:" -->

<a id="pool-r14"></a>**POOL-R14. Pinned idle pods are counted apart.** Pinned idle pods are accounted apart from warm inventory:

- They do not count toward `minWarm`. `status.warmCount`, `status.readyCount`, the pool's idle pod count, the `PoolWarmingUp` condition, and the idle-pods gauge count unpinned pods only, so the controller provisions unpinned pods for other tenants while pinned pods sit idle.
- They count toward `maxWarm`. The WarmPoolController keeps at most `maxWarm` minus the warm target pinned idle pods per pod set and drains the rest. The bound follows the current `minWarm`, so it widens while a scale-to-zero window holds `minWarm` at 0, and it is zero when `maxWarm` equals the warm target.
- They accrue idle pod-minutes like every idle pod.

<!-- src: §5.2 "Pinned idle inventory." --> <!-- unresolved: §5.2 states the bound per pool and does not say which pinned idle pods drain first; after the split into pod sets it is taken per pod set. -->

On a pool whose `maxWarm` equals its warm target, a recycled pod serves its tenant again only within the reserved hold and drains when it returns to `idle`. A deployer widens the window by setting `maxWarm` above `minWarm` or by raising [`gateway.claimHoldTTLSeconds`](pod-claims.md#setting-gateway-claimholdttlseconds).

<a id="pool-r15"></a>**POOL-R15. A pod in the reserved hold counts as occupied.** A pod whose claim is `reserved` counts as occupied for inventory and scaling. <!-- src: §4.6.2 "Reserved pods count as occupied."; §6.2 "`reserved` hold semantics." (last sentence) -->

<a id="pool-r16"></a>**POOL-R16. Each pod set has one disruption budget for its warm pods.** Each `PodSet` whose `minWarm` is 2 or more owns one PodDisruptionBudget, named `<podSet>-warm`, selecting the pool label, the pod-set label, and `lenny.dev/state: idle`, with an integer `minAvailable` of `minWarm - 1`. A `PodSet` whose `minWarm` is below 2 has none. <!-- src: PLCR-13; §4.6.1 "Disruption protection for agent pods:" (second paragraph) -->

The budget uses an integer `minAvailable` because the `AgentPod` has no `/scale` subresource, so a `maxUnavailable` budget cannot compute the expected pod count and would refuse every eviction. `minWarm - 1` admits one eviction at the steady state of `minWarm` idle pods, and `minWarm` would admit none and stall node drains. The budget is per pod set because each pod set has its own `minWarm` and its own reconcile, and because the eviction API refuses to evict a pod that two budgets select.

<a id="pool-r17"></a>**POOL-R17. Pods spread across zones and nodes by default.** The PoolScalingController writes default topology spread constraints into each `PodSet`: a soft spread across zones and a soft spread across nodes, each with `maxSkew: 1` and `whenUnsatisfiable: ScheduleAnyway`, both selecting the pool label. A pool's [`topologySpreadConstraints`](#setting-topologyspreadconstraints) replaces the defaults. The WarmPoolController copies the constraints into each `AgentPod` and pod at creation. <!-- src: §5.2 "Topology spread constraints:"; PM-5; PLC-2 -->

### Warm-up visibility

<a id="pool-r18"></a>**POOL-R18. A pool with no ready pod reports that it is warming.** The WarmPoolController sets the `PodSet` condition `PoolWarmingUp` to `True` with the reason `Provisioning` while `minWarm` is above 0, no unpinned pod is idle, and at least one pod is warming, and with the reason `Drained` when no unpinned pod is idle and none is warming. It sets the condition to `False` once an unpinned pod is idle. While every pod set of a pool reports `PoolWarmingUp`, the gateway answers a session create for the pool with `503 RUNTIME_UNAVAILABLE`, a `Retry-After` of the larger of 30 seconds and the estimated time to the first ready pod, and details that carry the pool name and the number of pods warming ([Client API sessions](../../interfaces/client-api/sessions.md)). <!-- src: §5.2 "`PoolWarmingUp` condition."; "Client-facing `503 Pool Not Ready` response." --> <!-- unresolved: no design states whether a pool with one warmed pod set and one warming pod set reports warming. -->

<a id="pool-r19"></a>**POOL-R19. Exhaustion alerts wait out the fill grace period.** The `WarmPoolExhausted` and `WarmPoolLow` alerts are suppressed for a pod set during the fill grace period, which starts when the pod set is created, when the WarmPoolController starts, and when the pod set's `minWarm` changes from 0 to a positive value, and lasts [`initialFillGracePeriod`](#setting-initialfillgraceperiod). <!-- src: §4.6.1 "Cold-start pool fill:"; "Re-activation grace period:" -->

### Owned pools

<a id="pool-r20"></a>**POOL-R20. An owned pool's warm target is its warmCount.** An owned pool's warm target is its [`warmCount`](#setting-warmcount), which is at least 1, unless a reservation raises it under POOL-R21. The PoolScalingController never raises an owned pool's `minWarm` above that target, and a `bootstrapMinWarm` on an owned pool is refused. <!-- src: DR-5 "Bounds" -->

<a id="pool-r21"></a>**POOL-R21. A reservation sets a floor of marked pods.** The WarmPoolController computes an owned pool's warm target on each reconcile as `max(minWarm, reservedPods - active)`, where `active` counts the pool's `AgentPod` objects in `claimed` and `reserved`, and uses that target as both the floor and the ceiling of the pod set's plan, so pinned idle pods get no room on an owned pool. While the pool has fewer than [`reservedPods`](#setting-reservedpods) non-terminal `AgentPod` objects marked `spec.reserved`, a new `AgentPod` is marked. The WarmPoolController drains unmarked idle pods before marked ones. During a base rollout only the pod set of the revision with the highest weight carries the floor and the marks. <!-- src: DR-10 "Mechanism"; "Budget" -->

<a id="pool-r22"></a>**POOL-R22. An owned pool creates pods only within its owner's bound.** The WarmPoolController creates no `AgentPod` in an owned pool while the owning tenant's non-terminal `AgentPod` objects, across all its owned pools, are at the bound the PoolScalingController renders from the tenant's grant ([Tenant-derived runtimes](tenant-derived-runtimes.md)). <!-- src: DR-5 "Bounds" -->

### Configuration drift, drain, and deletion

<a id="pool-r23"></a>**POOL-R23. The gateway reports whether a pool's configuration has reached Kubernetes.** Each pool record carries `pool_config_generation`, which every admin API write increments, and the PoolScalingController stamps the generation it rendered into each `PodSet` as [`lenny.dev/config-generation`](../../interfaces/kubernetes/labels-and-annotations.md#annotation-lenny-dev-config-generation). The gateway compares the two and reports the pool's sync status as `pending` while they differ and `synced` once they match. <!-- src: §4.6.2 "Reconciliation drift detection." -->

The comparison runs in the gateway so that a missing PoolScalingController is detected.

<a id="pool-r24"></a>**POOL-R24. Admission refusals back off per pod set.** When the admission webhook refuses a `PodSet` apply with any 4xx answer other than a conflict, the PoolScalingController pauses that `PodSet` for 1 second, doubles the pause on each consecutive refusal up to 60 seconds, and resets it on a successful apply. After [`admissionDeniedRetryCeiling`](#setting-admissiondeniedretryceiling) consecutive refusals it stops retrying that `PodSet` until the pool record's generation changes, a new leader is elected, or an operator resumes reconciliation through the admin API. Refusals on one pool never pause another. <!-- src: §4.6.3 "PoolScalingController admission-denial handling policy." --> <!-- judgment: the source keys the state on `(pool, crd)`; after the merge into `PodSet` the key is the `PodSet`. -->

A refusal of this kind is a deterministic function of the pool record the controller read, so an immediate retry reproduces it and loads the webhook and the API server.

<a id="pool-r25"></a>**POOL-R25. A draining pool takes no new session.** A pool drain moves the pool to `draining`. While a pool drains, no warm pod of it is assigned to a new session, a session create that selects it is refused with a retryable error that carries the estimated drain time, its sessions run to completion or until their age limit, and its pods are then removed. <!-- src: §15.1 `POST /v1/admin/pools/{name}/drain` -->

<a id="pool-r26"></a>**POOL-R26. A pool is deleted only when no session uses it.** A pool is deleted only when no session in it is running or suspended. Deleting a pool deletes its `PodSet` objects, apart from a `PodSet` that still owns a quarantined `AgentPod` (POOL-R7). <!-- src: §15.1 "Deletion semantics"; FE-1 "Retirement paths" -->

## Flows {#flows}

### A pool is created {#flow-create}

1. An operator creates the pool through the admin API ([Admin API pools](../../interfaces/admin-api/pools.md)). The gateway checks the name under POOL-R2, writes the pool record, and answers with `syncStatus: pending`.
2. The [PoolScalingController](../../components/pool-scaling-controller.md) computes the pod-set key and applies one `PodSet` named under POOL-R4 with both labels (POOL-R5).
3. The pool configuration validator admits the `PodSet`.
4. The [WarmPoolController](../../components/warm-pool-controller.md) creates `AgentPod` objects and pods up to the warm target (POOL-R9, [APOD-R2](agent-pod-lifecycle.md#apod-r2)), creates the disruption budget (POOL-R16), and sets `PoolWarmingUp` (POOL-R18).
5. The gateway reads the `PodSet`'s generation annotation and reports `syncStatus: synced` (POOL-R23).

### A revision with a new pod hash enters a pool {#flow-rollout}

1. The rollout gives the new revision a weight ([Runtime revisions](runtime-revisions.md)).
2. The PoolScalingController applies a second `PodSet` for the new key and splits the warm target (POOL-R11).
3. The WarmPoolController warms the new pod set. Each pod set's reconcile counts only its own `AgentPod` objects (POOL-R6), and each pod set has its own disruption budget (POOL-R16).
4. When the old revision leaves the traffic and its sessions end, the PoolScalingController deletes the old `PodSet` (POOL-R7).

## Settings {#settings}

<a id="setting-minwarm"></a>

```lenny-def
kind: setting
name: minWarm
type: integer
default: none (bootstrapMinWarm applies in bootstrap mode)
range: ">= 0, <= maxWarm"
scopes: [pool]
read-by: [PSC, WPC]
summary: The number of unpinned warm pods each pod set of the pool holds (POOL-R9, POOL-R10, POOL-R11).
```

<a id="setting-maxwarm"></a>

```lenny-def
kind: setting
name: maxWarm
type: integer
default: none
range: "> 0, >= minWarm"
scopes: [pool]
read-by: [WPC]
summary: The ceiling on a pod set's warm inventory plus its pinned idle pods (POOL-R14).
```

<a id="setting-scalepolicy-bootstrapminwarm"></a>

```lenny-def
kind: setting
name: scalePolicy.bootstrapMinWarm
type: integer
default: none
range: ">= 0; refused on an owned pool"
scopes: [pool]
read-by: [PSC]
summary: The minWarm a pool holds in bootstrap mode (POOL-R12).
```

<a id="setting-scalepolicy-podwarmupsecondsbaseline"></a>

```lenny-def
kind: setting
name: scalePolicy.podWarmupSecondsBaseline
type: integer (seconds)
default: 30
range: "> 0"
scopes: [pool]
read-by: [PSC]
summary: The pod_warmup_seconds input of the scaling formula (POOL-R10).
```

<a id="setting-scalepolicy-scaletozero"></a>

```lenny-def
kind: setting
name: scalePolicy.scaleToZero
type: object {schedule: cron, resumeAt: cron, timezone: IANA zone name}
default: absent
scopes: [pool]
read-by: [PSC]
summary: A daily window during which the pool's minWarm is 0 (POOL-R13).
```

<a id="setting-topologyspreadconstraints"></a>

```lenny-def
kind: setting
name: topologySpreadConstraints
type: list of Kubernetes TopologySpreadConstraint
default: soft zone spread and soft node spread (POOL-R17)
scopes: [pool]
read-by: [PSC, WPC]
summary: Replaces the default spread of the pool's pods.
```

<a id="setting-poolscaling-safetyfactor"></a>

```lenny-def
kind: setting
name: poolScaling.safetyFactor
type: number
default: per capacity tier
range: "> 0"
scopes: [platform]
read-by: [PSC]
summary: The safety_factor of the scaling formula (POOL-R10).
```
<!-- unresolved: the per-tier values live in §17.8.2, which this spike did not migrate; AR-1 removes the mcp value. -->

<a id="setting-initialfillgraceperiod"></a>

```lenny-def
kind: setting
name: initialFillGracePeriod
type: duration
default: 120s
scopes: [platform]
read-by: [WPC]
summary: The fill grace period during which warm-pool exhaustion alerts are suppressed (POOL-R19).
```

<a id="setting-admissiondeniedretryceiling"></a>

```lenny-def
kind: setting
name: admissionDeniedRetryCeiling
type: integer
default: 10
range: ">= 1"
scopes: [platform]
read-by: [PSC]
summary: The consecutive admission refusals after which the PoolScalingController stops retrying a PodSet (POOL-R24).
```

<a id="setting-warmupdeadlineseconds"></a>

```lenny-def
kind: setting
name: warmupDeadlineSeconds
type: integer (seconds)
default: 300
scopes: [platform]
read-by: [alerting]
summary: How long a pod set may report PoolWarmingUp before WarmPoolBootstrapping fires (POOL-R18).
```

<a id="setting-warmcount"></a>

```lenny-def
kind: setting
name: warmCount
type: integer
default: none
range: ">= 1, within the tenant's pod budget"
scopes: [owned pool]
read-by: [PSC]
summary: The warm target of an owned pool (POOL-R20).
```

<a id="setting-reservedpods"></a>

```lenny-def
kind: setting
name: reservedPods
type: integer
default: 0
range: "<= the tenant's allowance for the base and resource class"
scopes: [owned pool]
read-by: [WPC, PSC]
summary: The reservation floor of an owned pool (POOL-R21).
```

## Errors {#errors}

<a id="condition-reason-podsetkeycollision"></a>

```lenny-def
kind: condition-reason
name: PodSetKeyCollision
resource: PodSet
condition: Degraded
rule: POOL-R4
summary: A PodSet already holds the computed name for a different pod-set key, and the PoolScalingController wrote nothing for the new key.
```

## Metrics and alerts {#metrics-and-alerts}

<a id="alert-warmpoollow"></a>

```lenny-def
kind: alert
name: WarmPoolLow
severity: warning
expr: lenny_warmpool_idle_pods < 0.25 × minWarm for any pod set, outside the fill grace period
detects: POOL-R9
summary: A pod set's idle pods dropped below a quarter of its minWarm.
```

<a id="alert-warmpoolexhausted"></a>

```lenny-def
kind: alert
name: WarmPoolExhausted
severity: critical
expr: lenny_warmpool_idle_pods == 0 for any pod set for more than 60s, outside the fill grace period
detects: POOL-R9
summary: A pod set has no idle pod.
```

<a id="alert-warmpoolbootstrapping"></a>

```lenny-def
kind: alert
name: WarmPoolBootstrapping
severity: warning
expr: PoolWarmingUp is True for longer than warmupDeadlineSeconds on a pod set with minWarm > 0
detects: POOL-R18
summary: A pod set has not produced its first idle pod within the warm-up deadline.
```

<a id="alert-poolconfigdrift"></a>

```lenny-def
kind: alert
name: PoolConfigDrift
severity: warning
expr: pool_config_generation differs from the PodSet's lenny.dev/config-generation for more than 60s
detects: POOL-R23
summary: The PoolScalingController has not reconciled a pool's latest configuration.
```

<!-- not migrated in this spike: WarmPoolReplenishmentSlow, WarmPoolReplenishmentFailing, PoolScaleoutBlockedByQuota, WarmPoolIdleCostHigh, PoolBootstrapMode, and PoolScalingAdmissionStuck come from §16.5 and §4.6.2, which are outside the sections this spike migrated; the slice page under designs/evidence carries them. PoolConfigValidatorUnavailable detects a unit's health and belongs to the admission webhooks component. -->

## Failure behavior {#failure}

- **PoolScalingController down.** Pod sets keep their last rendered spec, and the WarmPoolController keeps reconciling against it. Warm targets stop following demand, and configuration changes stop reaching Kubernetes, which POOL-R23 reports.
- **WarmPoolController down.** No pod is created or replaced, and warm inventory falls as sessions claim pods. Running sessions continue. The scaling formula sizes `minWarm` to cover this window (POOL-R10).
- **Admission webhook down.** Every `PodSet` write is refused, including the PoolScalingController's, so configuration changes stop until the webhook returns.
- **API server or etcd unavailable.** Pool reconciliation freezes, and the controllers replay their work queues once it returns.

## Security properties and threats {#security}

- A pod built for one pool cannot serve another pool's sessions, because the pool label is immutable and the claim selects by it (POOL-R5, POOL-R6).
- A pool's configuration reaches Kubernetes only through the PoolScalingController, so a manual edit of a `PodSet` is refused rather than silently overwritten (POOL-R1).
- An owned pool's pods are created only within its owner's bound (POOL-R22).

## Limits {#limits}

- A session that arrives while a pool holds no warm pod waits for a pod created on demand.
- The scaling formula does not anticipate demand beyond its p99 burst window.

## Rationale {#rationale}

- **One kind per pod set.** The pool settings and the warm targets of one pod set are written, named, and deleted together, so they are one object.
- **Two labels.** During a rollout a pool has several pod sets. One label for both scopes would make each reconcile count the other pod set's pods, make two budgets select the same pods, and let a session bind to a pod of a revision it did not choose.
- **A fixed-length pod-set label.** A full pod-set key is 64 characters, one more than a label value holds, so the label carries `key16` and the `PodSet` spec carries the full key.

## Related capabilities {#related}

- [Pod claims](pod-claims.md) takes pods from a pool's warm inventory.
- [Agent pod lifecycle](agent-pod-lifecycle.md) states how each pod enters and leaves warm inventory.
- [Runtime revisions](runtime-revisions.md) creates the pod-set keys this page names.
- [Tenant-derived runtimes](tenant-derived-runtimes.md) owns the owned pool's budget and the warm stores.
- [Placement](placement.md) renders where a pod set's pods run.
