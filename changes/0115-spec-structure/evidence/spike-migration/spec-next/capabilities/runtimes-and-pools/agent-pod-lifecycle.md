---
code: APOD
type: capability
area: runtimes-and-pools
summary: >-
  An agent pod is the Kubernetes pod that serves a pool's sessions. This page defines the agent pod,
  its phases and the state machine the WarmPoolController projects from its claim, and how a pod is
  created, becomes ready, is recycled between sessions, retired, drained, quarantined, and deleted.
owns: [objects, requirements, settings, metrics, alerts]
depends-on: [POOL, CLAIM, SLOT, SLOTOPS, SCRUB, PLACE, REV, FORENSIC, CKPT, KAPOD, WPC]
---

# Agent pod lifecycle

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="agent-pod"></a>Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool. Each agent pod is recorded as one `AgentPod` of the same name. |
| <a id="warm-pod"></a>Warm pod | An agent pod whose infrastructure is ready and on which no runtime process runs: its containers run, each slot's supervisor has answered and passed its start-up checks, its assets and warm stores are filled, and each slot's warm-up checkouts are staged. |
| <a id="pod-phase"></a>Pod phase | The coarse occupancy state of an agent pod: `warming`, `idle`, `claimed`, `reserved`, `draining`, `failed`, `terminated`, or `quarantined`. |
| <a id="occupancy-projection"></a>Occupancy projection | The rule by which the WarmPoolController computes a pod's phase from its claim (APOD-R5). |
| <a id="slot-count"></a>Slot count | The number of slot containers the WarmPoolController rendered into a pod, recorded in `AgentPod.spec.slotCount` at creation. |
| <a id="recycling-pool"></a>Recycling pool | A pool with `sessionPolicy.recycle.enabled: true`, whose pods serve one occupancy after another. |
| <a id="recycle-disposition"></a>Recycle disposition | The gateway's decision, when a recycling pod's occupancy reaches zero and its whole-pod scrub has reported, to hold the pod for reuse or retire it (APOD-R7). |
| <a id="retirement"></a>Retirement | The move of a pod to `draining` because a limit, a mark, or a failure ends its useful life (APOD-R8). |
| <a id="served-session-count"></a>Served-session count | The number of sessions a pod has served over its life, counted from cleanup-outcome reports. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pool | A warmable deployment target for one runtime ([POOL](warm-pools.md#pool)). |
| Pod set | The warm pods of one pool built for one pod-set key ([POOL](warm-pools.md#pod-set)). |
| Claim | The exclusive occupancy of one agent pod by the gateway ([CLAIM](pod-claims.md#claim)). |
| Binding state | The state of a claim as the gateway records it ([CLAIM](pod-claims.md#binding-state)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([CLAIM](pod-claims.md#reserved-hold)). |
| Slot | One session's place on an agent pod ([SLOT](../sessions/session-isolation.md#slot)). |
| Dead slot | A slot that can serve no session for the rest of the pod's life ([SLOT](../sessions/session-isolation.md#dead-slot)). |
| Leaked slot | A slot whose cleanup was not acknowledged clean ([SLOT](../sessions/session-isolation.md#leaked-slot)). |
| Whole-pod scrub | The cleanup of pod-wide state when a recycling pod's occupancy reaches zero ([Pod reuse and scrub](../sessions/pod-reuse-and-scrub.md)). |
| Pod blueprint | The containers, volumes, and security settings of an agent pod ([Agent pod](../../components/agent-pod/index.md)). |
| Placement | The node class, offering, region, and tenancy a pool's pods are rendered with ([Placement](placement.md)). |
| Quarantine | The forensic hold that keeps an agent pod and its volumes for investigation ([Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md)). |
| Eviction checkpoint | The checkpoint taken before a voluntary eviction ends a pod ([Checkpoint capture](../sessions/checkpoints/checkpoint-capture.md)). |

## Overview {#overview}

An agent pod is created warm and waits in its pool's inventory. When a session claims it, the pod serves that session, and on a pool whose pods hold several slots it serves several sessions at once. Every runtime process starts when its session binds and ends with that session, so a pod carries no session state from one session to the next beyond what the slot release and the whole-pod scrub leave.

On a pool that does not recycle, a pod serves one occupancy and then drains. On a recycling pool, a pod whose sessions have all ended is scrubbed and then either held for its tenant and returned to inventory, or retired because it reached a limit, carries a release mark, or failed. A pod also leaves service when it fails, when its node is drained, when its certificate is about to expire, when the pool shrinks, when its warm stores fall out of date, and when an investigator quarantines it.

The phase records where a pod stands. The gateway never writes it. The gateway writes the claim, and the WarmPoolController computes the phase from the claim, the pool's session policy, and the pod's own state. The phase is coarse, because the state of each session lives in the session record and the state of each slot lives with the slots.

## Model {#model}

### Agent pod {#model-agent-pod}

- An agent pod belongs to one pod set, which owns its `AgentPod` from creation to deletion. Its pod set is recorded in `spec.podSetRef` and in its owner reference ([POOL-R5](warm-pools.md#pool-r5)).
- An agent pod's slot count, runtime, pod set, and placement are fixed at creation.
- An agent pod is never suspended in place. A session suspension releases the session's slot and leaves the pod running.

### Phases {#model-phases}

| Phase | Meaning |
| :-- | :-- |
| `warming` | The pod exists and is not yet a warm pod. |
| `idle` | A warm pod with no claim. |
| `claimed` | The pod's claim is `bound`, or `recycling` while the whole-pod scrub runs. |
| `reserved` | The pod's claim is `reserved`: the pod is scrubbed and held for its pinned tenant. |
| `draining` | The pod accepts no new session and ends once its remaining sessions and their cleanup finish. |
| `failed` | The pod failed during warm-up or lost its ability to serve. |
| `terminated` | The pod has ended. This phase is terminal. |
| `quarantined` | The pod carries the quarantine label and is held for investigation. |

### State machine {#state-machine}

```
Warm-up:
  warming ──→ idle              the adapter reports READY and the readiness gate is set
  warming ──→ failed            warm-up fails, including a placement refusal or a failed
                                start-up check

Occupancy (projected from the claim):
  idle ──→ claimed              a claim is created and written bound
  claimed ──→ claimed           a session is added to or leaves a pod with free slots
  claimed ──→ reserved          the recycle disposition holds the pod
  reserved ──→ claimed          the pinned tenant's next session rebinds within the hold
  reserved ──→ idle             the hold ends and the claim is deleted
  claimed ──→ draining          the claim becomes released or failed, the claim is deleted
                                while the pod is claimed, a drain request is stamped, or
                                APOD-R11 holds

Leaving service:
  idle ──→ draining             uptime limit, drain request, container exit, idle certificate
                                replacement, scale-down, pinned idle bound, warm store removed,
                                or placement drift
  failed ──→ draining           the failed pod is reclaimed
  draining ──→ terminated       remaining slots and their cleanup have finished

Quarantine:
  any non-terminal ──→ quarantined   the quarantine label is set
  quarantined ──→ draining           the quarantine is released
```

The session states of the session record (`running`, `suspended`, `resume_pending`, and the others) are not phases. A pod whose session is in any of them projects `claimed`.

### Invariants {#invariants}

- A pod that reaches `idle` or `reserved` after serving a session has passed its whole-pod scrub.
- A pod in `draining`, `failed`, `terminated`, or `quarantined` receives no new session.
- A quarantined pod leaves `quarantined` only through its quarantine's release.
- A pod's slot count never changes.

## Requirements {#requirements}

### Creation and readiness

<a id="apod-r1"></a>**APOD-R1. Each agent pod has one AgentPod.** Each agent pod has one `AgentPod` of the same name, and the WarmPoolController generates that name from the pool name. The `AgentPod`'s pod set is its controller owner for its whole life. The WarmPoolController is the sole writer of an `AgentPod`'s spec and status. <!-- src: PLC-2 "Pod-set names." (derived names); PLC-3 "AgentPod."; §4.6.3 `Sandbox` rows -->

<a id="apod-r2"></a>**APOD-R2. A pod is built from its pod set and fixed at creation.** The WarmPoolController creates an agent pod from its pod set, the revision the pod set names, the pool's resolved placement, and, on an owned pool, the base runtime the pod set names. At creation it:

- renders the pod blueprint with one slot container per slot ([Agent pod](../../components/agent-pod/index.md));
- records the slot count in `spec.slotCount`, equal to the pod set's [`sessionPolicy.maxConcurrentSessionsPerPod`](../sessions/session-isolation.md#setting-sessionpolicy-maxconcurrentsessionsperpod);
- copies the pool label and the pod-set label from the pod set ([POOL-R5](warm-pools.md#pool-r5)), and sets [`lenny.dev/runtime`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-runtime) to the runtime name;
- copies the pod set's topology spread constraints and placement ([Placement](placement.md));
- on an owned pool or a single-tenant platform pool, stamps the owner's tenant pin ([CLAIM-R6](pod-claims.md#claim-r6)), and, where a reservation applies, marks the `AgentPod` reserved ([POOL-R21](warm-pools.md#pool-r21));
- adds the finalizer of APOD-R16.

A change to the pool reaches only pods created afterwards, and an existing pod keeps its slot count and its rendered settings until it retires. <!-- src: PLC-3 "AgentPod."; PLCR-9; SIR-16; RV-3; DR-5 "Pods"; PM-2; PM-5 -->

<a id="apod-r3"></a>**APOD-R3. A pod is idle once it is a warm pod.** A pod moves from `warming` to `idle` when it is a warm pod: the adapter reports READY and the WarmPoolController sets the readiness gate [`lenny.dev/agent-pod-ready`](../../interfaces/kubernetes/labels-and-annotations.md#readiness-gate-lenny-dev-agent-pod-ready). No runtime process runs on a warm pod, and each slot's supervisor launches the runtime when a session binds. A pod that cannot become a warm pod moves to `failed`, and its pod set creates a replacement. <!-- src: PL-8 "A warm pod (D9)."; SI D15; §6.2 state diagram "Warm fill (pod-warm path)" -->

### The phase

<a id="apod-r4"></a>**APOD-R4. The WarmPoolController is the sole writer of the phase.** The WarmPoolController computes the phase as a level-triggered projection of the existence of the pod's claim, the claim's binding state, the pool's session policy, the quarantine label, and the phase the pod currently has. The gateway writes no part of an `AgentPod`. <!-- src: §4.6.1 "Occupancy projection (claim-driven `Sandbox.status.phase`):"; FE-1 "Data model" -->

<a id="apod-r5"></a>**APOD-R5. The projection maps claim states to phases.** The projection yields:

| Input | Phase |
| :-- | :-- |
| A warm pod with no claim | `idle` |
| Claim `bound` | `claimed` |
| Claim `recycling` | `claimed` |
| Claim `reserved` | `reserved` |
| Claim deleted while the pod projects `reserved` | `idle` |
| Claim `released` or `failed`, or deleted while the pod projects `claimed` | `draining`, then `terminated` |
| Quarantine label present | `quarantined`, whatever the claim |

A pod whose claim is deleted while it projects `claimed` drains on every pool, because it was not scrubbed, and a pod returns to inventory only through the `reserved → idle` edge. <!-- src: §4.6.1 "Occupancy projection"; §6.2 opening paragraph; PL-8 "What leaves."; FE-1 "Data model" -->

<a id="apod-r6"></a>**APOD-R6. The state label follows the phase alone.** The WarmPoolController sets the pod label [`lenny.dev/state`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-state) from the phase: `idle` maps to `idle`, `claimed` and `reserved` map to `active`, and `draining` maps to `draining`. Other phases carry no value of the label. The claim's binding state is not an input. <!-- src: §6.2 "State storage:"; §6.2 "Concurrent pod lifecycle" bullet "Partial occupancy" --> <!-- unresolved: no design states the label value for `quarantined`; FE-1 excludes quarantined pods from network allow policies by the quarantine label. -->

Because `claimed` and `reserved` map to one value, the label stays `active` through the recycle boundary and the hold, so short sessions in quick succession cause no label writes.

### Recycling and retirement

<a id="apod-r7"></a>**APOD-R7. The gateway decides the recycle disposition after the scrub.** When a recycling pod's occupancy reaches zero and the release does not end the occupancy without reuse ([CLAIM-R3](pod-claims.md#claim-r3)), the gateway runs the whole-pod scrub and, when the scrub reports, decides the recycle disposition. It retires the pod when any condition of APOD-R8 holds or when the pod's host node is unschedulable. Otherwise it holds the pod in the reserved hold ([CLAIM-R10](pod-claims.md#claim-r10)). <!-- src: §5.2 "Recycle lifecycle (`recycle.enabled: true`)."; §6.2 "Host-node schedulability precondition." --> <!-- judgment: §6.2 places the host-node precondition on the `claimed → sdk_connecting` re-warm edge, which PL-8 removes; the precondition moves to the hold decision, which FE-1 calls "the recycle edge". -->

The host node is unschedulable when the pod label [`lenny.dev/host-schedulable`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-host-schedulable) reads `"false"` or is absent. The WarmPoolController is the sole evaluator of the node's schedulability and writes the label from its node informer, so the gateway needs no access to nodes. Holding a pod on a cordoned node would hand the next session a pod whose eviction is imminent.

<a id="apod-r8"></a>**APOD-R8. A recycling pod retires at its limits.** A recycling pod retires when any of these holds:

- **Session count limit.** Its served-session count reaches `sessionPolicy.recycle.maxSessionsPerPod`. On a pod with one slot the pod retires at the recycle disposition. On a pod with several slots it moves to `draining` on the release that brings the count to the limit, whatever its occupancy, because a leaked or dead slot can keep occupancy above zero indefinitely.
- **Uptime limit.** Its age since creation exceeds `sessionPolicy.recycle.maxPodUptimeSeconds`. The WarmPoolController derives the age from the pod's creation time and moves it to `draining` whatever its sessions are doing, the gateway places no new session on it, and its sessions drain.
- **Scrub failure limit.** Its count of failed whole-pod scrubs reaches `sessionPolicy.recycle.maxScrubFailures`, whatever `sessionPolicy.recycle.onScrubFailure` is.
- **Fresh-guest reprovision.** Its pool sets `sessionPolicy.recycle.scrubProfile: vm-restart`, which retires every pod at the recycle disposition.
- **Retire-on-release mark.** Its claim carries the retire-on-release mark ([CLAIM-R17](pod-claims.md#claim-r17)).
- **Failed session.** On a pod with one slot, its session ended in failure or a crash, whatever the pool's recycle settings.

On a retirement at the recycle disposition the gateway sets the claim to `released`. The gateway counts each retirement it decides in [`lenny_gateway_pod_retirement_total`](#metric-lenny-gateway-pod-retirement-total), and the WarmPoolController counts each uptime drain in [`lenny_controller_pod_retirement_total`](#metric-lenny-controller-pod-retirement-total). <!-- src: §5.2 "Pod retirement policy (recycling pools)."; "`onScrubFailure` behaviors:"; §5.2 "Kata/microvm scrub variant." item 7; UC-3 --> <!-- unresolved: §5.2's "Runtime not live" condition reads `ReportPodScrub`'s statement that the runtime can serve the next session; under SI D4 and D15 no runtime process outlives its session, and no design restates the condition, so it is left out. --> <!-- unresolved: SI-4 adds that a refusal RP-1 classes as not retryable stops the pod accepting binds and the pod exits once idle; no design places it among the retirement conditions. -->

<a id="apod-r9"></a>**APOD-R9. A failed scrub below its limit follows onScrubFailure.** When a whole-pod scrub fails and the pod has reached no limit of APOD-R8, `sessionPolicy.recycle.onScrubFailure: warn` holds the pod as a successful scrub would and stamps the annotation [`lenny.dev/scrub-warning`](../../interfaces/kubernetes/labels-and-annotations.md#annotation-lenny-dev-scrub-warning), and `onScrubFailure: fail` sets the claim to `failed`, which drains the pod. A failed stamp of the annotation fails the hold, and the pod retires. <!-- src: §5.2 "`onScrubFailure` behaviors:" --> <!-- judgment: §5.2 names "a `scrub_warning` annotation" without a key; the key is taken from the tree (`StampScrubWarning` in pkg/gateway/podlifecycle/podclaim/slotclaimer.go), as is the fail-closed behavior on a failed stamp (scrubwarning_test.go). --> <!-- unresolved: SI-3 moves most scrub work into the per-slot release, and no design restates how `onScrubFailure` and `maxScrubFailures` apply to what remains of the whole-pod scrub. -->

<a id="apod-r10"></a>**APOD-R10. Recycling requires an acknowledgment and a session limit.** A recycling pool requires `sessionPolicy.recycle.maxSessionsPerPod`, and recycling is enabled only with the deployer's acknowledgment `sessionPolicy.recycle.acknowledgeBestEffortScrub: true`, because the scrub reaches only what it is designed to reach ([Pod reuse and scrub](../sessions/pod-reuse-and-scrub.md)). A pool with `sessionPolicy.recycle.scrubProfile: in-place` also requires `sessionPolicy.recycle.acknowledgeMicrovmResidualState: true`. Pool admission refuses a pool that breaks this rule with `422 INVALID_POOL_CONFIGURATION`. <!-- src: §5.2 "Pod retirement policy" ("required with no default"); "Deployer acknowledgment."; §5.2 scrub profile paragraph -->

### Pods with several slots

<a id="apod-r11"></a>**APOD-R11. A pod with too many unhealthy slots drains.** A pod with several slots moves to `draining` when its failed slots within a rolling 5-minute window, together with its leaked slots and its dead slots, reach half its slot count, rounded up. While the pod drains, no new slot is assigned, its running sessions continue under their own retry policy, and the pod moves to `terminated` once every slot has finished and its cleanup has run. The gateway counts each such drain in [`lenny_slot_pod_replacement_total`](../sessions/slot-assignment-and-failure.md#metric-lenny-slot-pod-replacement-total). <!-- src: §6.2 "`leaked` slot semantics."; §6.2 "Concurrent pod lifecycle" bullet "Draining behavior"; §5.2 "Slot retry policy" bullet "Whole-pod replacement trigger:"; SI-4 "What follows." -->

### Drain, replacement, and disruption

<a id="apod-r12"></a>**APOD-R12. A gateway requests a drain through an annotation.** A gateway replica requests a pod's drain by stamping the annotation [`lenny.dev/drain-request`](../../interfaces/kubernetes/labels-and-annotations.md#annotation-lenny-dev-drain-request), and the WarmPoolController moves the pod to `draining`. The WarmPoolController ignores the annotation on a quarantined pod. <!-- src: §4.6.1 "Occupancy projection" (last paragraph); FE-1 "Retirement paths" -->

<a id="apod-r13"></a>**APOD-R13. The WarmPoolController drains idle pods that can no longer serve.** The WarmPoolController drains an idle pod when:

- its certificate expires within 30 minutes, and it creates a replacement ([mTLS and PKI](../../cross-cutting/mtls-and-pki.md));
- its pod set's warm inventory exceeds its target, or its pinned idle pods exceed their bound ([POOL-R14](warm-pools.md#pool-r14));
- it holds a warm store no longer on its pod set's rendered list ([Tenant-derived runtimes](tenant-derived-runtimes.md));
- its node no longer matches its placement ([Placement](placement.md));
- its adapter or a slot container has exited.

After a session for which a warm store was withheld ends, the WarmPoolController drains the pod. <!-- src: §4.6.1 "Responsibilities:"; §6.2 state diagram; DR-11 "Changing the list."; "Withheld stores."; PM-6 -->

<a id="apod-r14"></a>**APOD-R14. Draining pods terminate, and a new leader keeps draining them.** A pod in `draining` moves to `terminated` once its sessions and their cleanup have finished, and a pod in `failed` moves to `draining`. A controller that takes over leadership treats pods already in `draining` as draining and advances them toward deletion. <!-- src: §6.2 state diagram; §4.6.1 "Controller crash during active scale-down drain" -->

<a id="apod-r15"></a>**APOD-R15. A pod checkpoints its sessions before a voluntary eviction.** Each agent pod has a preStop hook that signals its coordinating gateway replica to take an eviction checkpoint of its sessions before the pod ends, covering every slot container, and a [`terminationGracePeriodSeconds`](#setting-terminationgraceperiodseconds) long enough for that checkpoint to finish and persist. Warm pods are protected from voluntary eviction by their pod set's disruption budget ([POOL-R16](warm-pools.md#pool-r16)). A pod in the recycle window or the reserved hold has neither protection. An eviction there deletes the claim through the pod's termination, and the tenant's next session takes the normal acquisition path. <!-- src: §4.6.1 "Disruption protection for agent pods:"; SI §4.6.1 row -->

### Deletion and quarantine

<a id="apod-r16"></a>**APOD-R16. A finalizer keeps a pod until its session is safe.** Every `AgentPod` carries the finalizer [`lenny.dev/session-cleanup`](../../interfaces/kubernetes/labels-and-annotations.md#finalizer-lenny-dev-session-cleanup). When an `AgentPod` is being deleted, the WarmPoolController removes the finalizer only when no session holds the pod, or when the pod's session has been checkpointed and the gateway has been told, so that it can resume the session on a replacement pod. <!-- src: §4.6.1 "Sandbox finalizers:" -->

<a id="apod-r17"></a>**APOD-R17. Termination deletes the claim and then the AgentPod.** At a pod's termination the WarmPoolController deletes its claim and then its `AgentPod`. <!-- src: §4.6.3 `SandboxClaim` row; §28.3 `REG-CLAIM` -->

<a id="apod-r18"></a>**APOD-R18. A quarantined pod is frozen.** A pod carrying the quarantine label projects `quarantined` and is excluded from inventory and from every drain edge, retirement condition, recycle disposition, and slot release. The Kubernetes API refuses the deletion of a quarantined pod or `AgentPod` by anyone but the WarmPoolController after the quarantine's release ([`AgentPod` admission](../../interfaces/kubernetes/agentpod.md#crd-agentpod)). The quarantine's release moves the pod to `draining`. A quarantined pod no longer counts as warm, so its pod set creates a replacement. <!-- src: FE-1 "Data model"; "Enforcement points"; "Retirement paths"; SI-3 "A quarantined pod." -->

## Flows {#flows}

### A pod's life on a recycling pool {#flow-life}

1. The [WarmPoolController](../../components/warm-pool-controller.md) creates the `AgentPod` and its pod (APOD-R2), and the pod is `warming`.
2. The adapter reports READY, the controller sets the readiness gate, and the pod projects `idle` (APOD-R3).
3. A session claims the pod ([CLAIM-R1](pod-claims.md#claim-r1)), and the pod projects `claimed`.
4. The last session's release brings occupancy to zero, and the gateway runs the whole-pod scrub ([Pod reuse and scrub](../sessions/pod-reuse-and-scrub.md)).
5. The gateway decides the recycle disposition (APOD-R7). On a hold the pod projects `reserved`, and then `claimed` again on a rebind or `idle` when the hold ends ([CLAIM-R10](pod-claims.md#claim-r10)). On a retirement the claim becomes `released`, and the pod drains.
6. The pod's sessions and cleanup finish, the controller removes the finalizer (APOD-R16), deletes the claim and the `AgentPod` (APOD-R17), and the pod set creates a replacement ([POOL-R9](warm-pools.md#pool-r9)).

## Settings {#settings}

<a id="setting-sessionpolicy-recycle-enabled"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.enabled
type: boolean
default: false
scopes: [pool]
read-by: [GWCLAIM, WPC]
summary: Whether the pool's pods serve one occupancy after another (APOD-R7).
```

<a id="setting-sessionpolicy-recycle-acknowledgebesteffortscrub"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.acknowledgeBestEffortScrub
type: boolean
default: false
range: "true required when sessionPolicy.recycle.enabled is true"
scopes: [pool]
read-by: [gateway pool admission]
summary: The deployer's acknowledgment that the scrub reaches only what it is designed to reach (APOD-R10).
```

<a id="setting-sessionpolicy-recycle-maxsessionsperpod"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.maxSessionsPerPod
type: integer
default: none
range: "> 0; required when sessionPolicy.recycle.enabled is true"
scopes: [pool]
read-by: [gateway recycle coordinator]
summary: The served-session count at which a recycling pod retires (APOD-R8).
```

<a id="setting-sessionpolicy-recycle-maxpoduptimeseconds"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.maxPodUptimeSeconds
type: integer (seconds)
default: absent (no uptime limit)
scopes: [pool]
read-by: [WPC, GWCLAIM]
summary: The age at which a recycling pod drains (APOD-R8).
```

<a id="setting-sessionpolicy-recycle-maxscrubfailures"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.maxScrubFailures
type: integer
default: 3
scopes: [pool]
read-by: [gateway recycle coordinator]
summary: The failed whole-pod scrubs at which a pod retires (APOD-R8).
```

<a id="setting-sessionpolicy-recycle-onscrubfailure"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.onScrubFailure
type: enum [warn, fail]
default: warn
scopes: [pool]
read-by: [gateway recycle coordinator]
summary: What a failed whole-pod scrub does to a pod below its limits (APOD-R9).
```

<a id="setting-sessionpolicy-recycle-scrubprofile"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.scrubProfile
type: enum [standard, in-place, vm-restart]
default: standard
range: "in-place and vm-restart only on an isolationProfile microvm pool"
scopes: [pool]
read-by: [gateway recycle coordinator]
summary: The scrub procedure at the recycle boundary; vm-restart retires every pod there (APOD-R8).
```
<!-- unresolved: whether `in-place` and `vm-restart` keep their meaning after SI-3 moves most scrub steps into the per-slot release is not stated by any design; PM removes only the clause that rejects `standard` on a pool with `allowCrossTenantReuse`. -->

<a id="setting-sessionpolicy-recycle-acknowledgemicrovmresidualstate"></a>

```lenny-def
kind: setting
name: sessionPolicy.recycle.acknowledgeMicrovmResidualState
type: boolean
default: false
range: "true required when scrubProfile is in-place"
scopes: [pool]
read-by: [gateway pool admission]
summary: The deployer's acknowledgment of the guest kernel state that persists across a pinned tenant's sessions under in-place (APOD-R10).
```

<a id="setting-terminationgraceperiodseconds"></a>

```lenny-def
kind: setting
name: terminationGracePeriodSeconds
type: integer (seconds)
default: 120
range: ">= the agent-pod floor that the pool configuration validator checks"
scopes: [pool]
read-by: [WPC]
summary: The time a pod has to finish its eviction checkpoint before it is killed (APOD-R15).
```

## Metrics and alerts {#metrics-and-alerts}

<a id="metric-lenny-gateway-pod-retirement-total"></a>

```lenny-def
kind: metric
name: lenny_gateway_pod_retirement_total
type: counter
labels: [pool, reason]
emitter: GW
summary: Retirements the gateway decided at the recycle disposition; reason is session_count_limit, scrub_failure_limit, vm_restart_reprovision, or retire_on_release (APOD-R8).
```
<!-- unresolved: §5.2 names the reasons session_count_limit and scrub_failure_limit; vm_restart_reprovision appears in its prose; retire_on_release is proposed by this migration. -->

<a id="metric-lenny-controller-pod-retirement-total"></a>

```lenny-def
kind: metric
name: lenny_controller_pod_retirement_total
type: counter
labels: [pool, reason]
emitter: WPC
summary: Drains the WarmPoolController level-triggered; reason is uptime_limit (APOD-R8).
```

<a id="alert-finalizerstuck"></a>

```lenny-def
kind: alert
name: FinalizerStuck
severity: warning
expr: an AgentPod in Terminating with lenny.dev/session-cleanup present for more than 5 min
detects: APOD-R16
summary: The WarmPoolController cannot confirm that a terminating pod's session is safe.
```

## Failure behavior {#failure}

- **Pod crash or node failure during a session.** The pod retires through the drain path and never returns to inventory. The session follows its retry policy and resumes on a fresh pod ([Retry and resume](../sessions/retry-and-resume.md)).
- **Pod failure with several sessions.** Every active slot fails, each session retries independently ([SLOTOPS-R7](../sessions/slot-assignment-and-failure.md#slotops-r7)), and the pod is replaced.
- **WarmPoolController down.** Phases stop changing, and pods are neither created nor removed. Running sessions continue. Projections resume from the current claims when a leader returns.
- **Node drain.** Warm pods leave one at a time under their budget and are replaced. Pods with sessions take their eviction checkpoint first (APOD-R15). Quarantined pods block the drain.

## Security properties and threats {#security}

- No runtime process runs on a pod before its session binds, so no process sees a workspace before it is complete (APOD-R3).
- A pod that reaches `idle` or `reserved` after a session has been scrubbed (invariant).
- A quarantined pod keeps its volumes and processes and cannot be deleted through the Kubernetes API (APOD-R18).
- **Threat: state carried from one session to the next on a recycled pod.** The pod serves only its pinned tenant ([CLAIM-R6](pod-claims.md#claim-r6)), and the deployer acknowledges the best-effort scrub (APOD-R10).

## Limits {#limits}

- The scrub does not reach kernel state or state outside the slot volumes ([Pod reuse and scrub](../sessions/pod-reuse-and-scrub.md)).
- A pod in the recycle window or the reserved hold has no eviction protection (APOD-R15).

## Rationale {#rationale}

- **A coarse phase.** Writing each session's state onto the pod would cost several label and status writes per session at scale. The phase changes only when occupancy changes.
- **The controller projects, and the gateway claims.** The phase has one writer, so no second writer contradicts it, and the gateway needs no write access to `AgentPod` objects.
- **Drain rather than return.** A pod whose claim ends without a scrub may hold a session's state, so the projection drains it, and only the scrubbed `reserved → idle` edge returns a pod to inventory.

## Related capabilities {#related}

- [Warm pools](warm-pools.md) decides how many pods exist.
- [Pod claims](pod-claims.md) writes the claim this page projects.
- [Slot assignment and failure](../sessions/slot-assignment-and-failure.md) and [Session isolation](../sessions/session-isolation.md) govern the slots inside a pod.
- [Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md) holds a pod for investigation.
- [Placement](placement.md) renders where a pod runs and drains pods on drifted nodes.
