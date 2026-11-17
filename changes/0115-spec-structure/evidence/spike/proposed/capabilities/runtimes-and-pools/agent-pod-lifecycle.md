---
code: APOD
type: capability
area: runtimes-and-pools
summary: >-
  An agent pod is the Kubernetes pod that serves a pool's sessions. This page defines the agent pod,
  its phases and the state machine the WarmPoolController projects from its claim, and how a pod is
  created, becomes ready, is recycled between sessions, drained, retired, quarantined, and deleted.
owns: [objects, requirements, settings, alerts]
depends-on: [POOL, CLAIM, SLOT, SCRUB, PLACE, REV, QUAR, CKPT, SESS]
---

# Agent pod lifecycle

An agent pod is the Kubernetes pod that serves a pool's sessions. This page defines the agent pod, its phases and the state machine the WarmPoolController projects from its claim, and how a pod is created, becomes ready, is recycled between sessions, drained, retired, quarantined, and deleted.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="agent-pod"></a>Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool. Each agent pod is recorded as one [`AgentPod`](../../interfaces/kubernetes/agentpod.md) of the same name. |
| <a id="warm-pod"></a>Warm pod | An agent pod whose infrastructure is ready and on which no runtime process runs: its containers run, each slot's supervisor has answered and passed its start-up checks, its assets and warm stores are filled, and each slot's warm-up checkouts are staged. |
| <a id="phases"></a>Phase | The coarse occupancy state of an agent pod: `warming`, `idle`, `claimed`, `reserved`, `draining`, `failed`, `terminated`, or `quarantined`. |
| Occupancy projection | The rule by which the WarmPoolController computes a pod's phase from its claim (APOD-R5). |
| <a id="slot-count"></a>Slot count | The number of slot containers the WarmPoolController rendered into a pod, equal to the pool's `sessionPolicy.maxConcurrentSessionsPerPod` when the pod was created. |
| Recycling pool | A pool with `recycle.enabled: true`, whose pods serve one session after another. |
| <a id="recycle"></a>Recycle disposition | The gateway's decision, when a recycling pod's occupancy reaches zero and its whole-pod scrub has reported, to hold the pod for reuse or retire it (APOD-R7). |
| Retirement | The move of a pod to `draining` because a limit or a failure ends its useful life (APOD-R8). |
| Served-session count | The number of sessions a pod has served over its life, counted from cleanup-outcome reports. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pool | A warmable deployment target for one runtime ([Warm pools](warm-pools.md#pool)). |
| Pod set | The warm pods of one pool built for one pod-set key ([Warm pools](warm-pools.md#pod-set)). |
| Claim | The exclusive occupancy of one agent pod by the gateway ([Pod claims](pod-claims.md#claim)). |
| Binding state | The state of a claim ([Pod claims](pod-claims.md#binding-states)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([Pod claims](pod-claims.md#reserved-hold)). |
| Slot | One session's place on an agent pod ([Slots](../sessions/slot-assignment-and-failure.md#slot)). |
| Dead slot | A slot whose container ended or whose supervisor stopped answering ([Slots](../sessions/slot-assignment-and-failure.md#dead-slot)). |
| Leaked slot | A slot whose cleanup was not acknowledged clean ([Slots](../sessions/slot-assignment-and-failure.md#leaked-slot)). |
| Slot release | The cleanup of a slot at the end of each session in it ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#slot-release)). |
| Whole-pod scrub | The cleanup of pod-wide state when a recycling pod's occupancy reaches zero ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#whole-pod-scrub)). |
| Pod blueprint | The containers, volumes, and security settings of an agent pod ([Agent pod](../../components/agent-pod/index.md)). |
| Revision | One recorded version of a runtime's versioned fields ([Runtime revisions and rollouts](runtime-revisions.md#revision)). |
| Placement | The node class, offering, region, and tenancy a pool's pods are rendered with ([Placement](placement.md#placement)). |
| Quarantine | The forensic hold that keeps an agent pod and its volumes for investigation ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#quarantine)). |
| Eviction checkpoint | The checkpoint taken before a voluntary eviction ends a pod ([Checkpoints](../sessions/checkpoints/checkpoint-capture.md#eviction-checkpoint)). |

## Overview {#overview}

An agent pod is created warm and waits in its pool's inventory. When a session claims it, the pod serves that session, and on a pool whose pods hold several slots it serves several sessions at once. Every runtime process starts when its session binds and ends with that session, so the pod itself carries no session state from one session to the next beyond what the slot release and the whole-pod scrub leave.

On a pool that does not recycle, a pod serves one occupancy and then drains. On a recycling pool, a pod whose sessions have all ended is scrubbed and then either held for its tenant and returned to inventory, or retired because it reached a limit or failed. A pod also leaves service when it fails, when its node is drained, when its certificate is about to expire, when the pool shrinks, when its warm stores fall out of date, and when an investigator quarantines it.

The phase is the record of where a pod stands. The gateway never writes it. It writes the claim, and the WarmPoolController computes the phase from the claim, the pool's session policy, and the pod's own state. The phase is coarse on purpose: the fine state of each session lives in the session record, and the per-slot state lives with the slots.

## Model {#model}

### Agent pod {#model-agent-pod}

- An agent pod belongs to one pod set, which owns its `AgentPod` from creation to deletion. Its pod set is recorded in `spec.podSetRef` and its owner reference ([Warm pools](warm-pools.md#pool-r5)).
- An agent pod is named `<pool>-<random>`, and its `AgentPod` has the same name.
- An agent pod's slot count, runtime, pod set, and placement are fixed at creation.
- An agent pod is never suspended in place. A session suspension releases the session's slot and leaves the pod running ([Session lifecycle](../sessions/session-lifecycle/index.md#suspension)).

### Phases {#model-phases}

| Phase | Meaning |
| :-- | :-- |
| `warming` | The pod exists and is not yet a warm pod. |
| `idle` | A warm pod with no claim. |
| `claimed` | The pod's claim is `bound`, or `recycling` while the whole-pod scrub runs. |
| `reserved` | The pod's claim is `reserved`: the pod is scrubbed and held for its pinned tenant. |
| `draining` | The pod accepts no new session and ends once its remaining sessions and their cleanup finish. |
| `failed` | The pod failed during warm-up or lost its ability to serve. |
| `terminated` | The pod has ended. Terminal. |
| `quarantined` | The pod carries the quarantine label and is held for investigation. |

### State machine {#state-machine}

```
Warm-up:
  warming ──→ idle              the adapter reports READY and the readiness gate is set
  warming ──→ failed            warm-up fails, including a placement refusal or a failed
                                start-up check

Occupancy (projected from the claim):
  idle ──→ claimed              a claim is created and patched to bound
  claimed ──→ claimed           a session is added to or leaves a pod with free slots
  claimed ──→ reserved          the recycle disposition holds the pod
  reserved ──→ claimed          the pinned tenant's next session rebinds within the hold
  reserved ──→ idle             the hold expires and the claim is deleted
  claimed ──→ draining          the claim becomes released or failed, the claim is deleted
                                while the pod is claimed, a drain request is stamped, or a
                                condition of APOD-R11 holds

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

<a id="apod-r1"></a>**APOD-R1.** Each agent pod has one `AgentPod`, and the WarmPoolController names the pod after it. The `AgentPod`'s pod set is its controller owner for its whole life. The WarmPoolController is the sole writer of an `AgentPod`'s spec and status. <!-- src: PLC-3 "AgentPod."; §4.6.3 `Sandbox` rows -->

<a id="apod-r2"></a>**APOD-R2.** The WarmPoolController creates an agent pod from its pod set, the revision the pod set names, the pool's resolved placement, and, on an owned pool, the base runtime the pod set names. At creation it:

- renders the pod blueprint with one slot container per slot ([Agent pod](../../components/agent-pod/index.md));
- records the slot count in `spec.slotCount`, equal to the pool's `sessionPolicy.maxConcurrentSessionsPerPod`;
- copies the pool label and the pod-set label from the pod set ([Warm pools](warm-pools.md#pool-r5)), and sets [`lenny.dev/runtime`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-runtime) to the runtime name;
- copies the pod set's topology spread constraints and placement ([Placement](placement.md#rendering));
- on an owned pool, stamps the owner's tenant pin ([Pod claims](pod-claims.md#claim-r6)) and, where a reservation applies, marks the `AgentPod` reserved ([Warm pools](warm-pools.md#pool-r21));
- adds the finalizer of APOD-R16.

A change to the pool reaches only pods created afterwards: an existing pod keeps its slot count and its rendered settings until it retires. <!-- src: PLC-3 "AgentPod."; PLCR-9; SI-4 "The slot count (SIR-16)."; RV-3 bullet 5; DR-5 "Pods"; PM-5 --> <!-- unresolved: the slot count is set at creation from the pod set's `maxConcurrentSessionsPerPod`; whether the WarmPoolController reads it from the `PodSet` or from the pool record is not stated. -->

<a id="apod-r3"></a>**APOD-R3.** A pod moves from `warming` to `idle` when it is a warm pod: the adapter reports READY and the WarmPoolController sets the pod's readiness gate [`lenny.dev/agent-pod-ready`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-agent-pod-ready). No runtime process runs on a warm pod; each slot's supervisor launches the runtime when a session binds ([Slots](../sessions/slot-assignment-and-failure.md#launch-at-bind)). A pod that cannot become a warm pod moves to `failed`, and its pod set creates a replacement. <!-- src: PL-8 "A warm pod (D9)."; §6.2 state diagram "Warm fill (pod-warm path)" -->

### The phase

<a id="apod-r4"></a>**APOD-R4.** The WarmPoolController is the sole writer of an agent pod's phase. It computes the phase as a level-triggered projection of the existence of the pod's claim, the claim's binding state, the pool's session policy, the quarantine label, and the phase the pod currently has. The gateway writes no part of an `AgentPod`. <!-- src: §4.6.1 "Occupancy projection (claim-driven `Sandbox.status.phase`):"; FE-1 "Data model" -->

<a id="apod-r5"></a>**APOD-R5.** The projection yields:

| Input | Phase |
| :-- | :-- |
| A warm pod with no claim | `idle` |
| Claim `bound` | `claimed` |
| Claim `recycling` | `claimed` |
| Claim `reserved` | `reserved` |
| Claim deleted while the pod projects `reserved` | `idle` |
| Claim `released` or `failed`, or deleted while the pod projects `claimed` | `draining`, then `terminated` |
| Quarantine label present | `quarantined`, whatever the claim |

A pod whose claim is deleted while it projects `claimed` drains on every pool, because it was not scrubbed; a pod returns to inventory only through the `reserved → idle` edge. <!-- src: §4.6.1 "Occupancy projection"; §6.2 opening paragraph; PL-8 "What leaves."; FE-1 "Data model" -->

<a id="apod-r6"></a>**APOD-R6.** The WarmPoolController sets the pod label [`lenny.dev/state`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-state) from the phase alone: `idle` maps to `idle`, `claimed` and `reserved` map to `active`, and `draining` maps to `draining`. Other phases carry no value of the label. The claim's binding state is not an input. <!-- src: §6.2 "State storage:" --> <!-- unresolved: no design states the label value for `quarantined`; FE-1 excludes quarantined pods from network allow policies by the quarantine label rather than by this one. -->

Because `claimed` and `reserved` map to one value, the label stays `active` through the recycle boundary and the hold, so a pod with short sessions in quick succession causes no label writes.

### Recycling and retirement

<a id="apod-r7"></a>**APOD-R7.** When a recycling pod's occupancy reaches zero and the release does not end the occupancy without reuse ([Pod claims](pod-claims.md#claim-r3)), the gateway runs the whole-pod scrub and, when the scrub reports, decides the recycle disposition. It retires the pod when any condition of APOD-R8 holds or when the pod's host node is unschedulable. Otherwise it holds the pod in the reserved hold ([Pod claims](pod-claims.md#claim-r9)). <!-- src: §5.2 "Recycle lifecycle (`recycle.enabled: true`)."; §6.2 "Host-node schedulability precondition." -->

The host node is unschedulable when the pod label [`lenny.dev/host-schedulable`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-host-schedulable) reads `"false"` or is absent. Holding a pod on a cordoned node would hand the next session a pod whose eviction is imminent.

<a id="apod-r8"></a>**APOD-R8.** A recycling pod retires when any of these holds:

- **Session count limit.** Its served-session count reaches `recycle.maxSessionsPerPod`. On a pod with one slot the pod retires at the recycle disposition. On a pod with several slots it moves to `draining` on the release that brings the count to the limit, whatever its occupancy, because a leaked slot can keep occupancy above zero indefinitely.
- **Uptime limit.** Its age since creation exceeds `recycle.maxPodUptimeSeconds`. The WarmPoolController derives the age from the pod's creation time and moves it to `draining` whatever its sessions are doing; the gateway places no new session on it, and its sessions drain.
- **Scrub failure limit.** Its count of failed whole-pod scrubs reaches `recycle.maxScrubFailures`, whatever `recycle.onScrubFailure` is.
- **Runtime not live.** Its scrub report states that its runtime cannot serve the next session, or omits that fact.
- **Failed session.** On a pod with one slot, its session ended in failure or a crash, whatever the pool's recycle settings.

On a retirement the gateway sets the claim to `released`. <!-- src: §5.2 "Pod retirement policy (recycling pools)."; "`onScrubFailure` behaviors:" --> <!-- unresolved: §5.2 also retires every pod of a `vm-restart` scrub profile at the recycle boundary with the reason `vm_restart_reprovision`; SI-3 retires the whole-pod scrub's slot work and no design restates the scrub profiles, so whether `vm-restart` remains is not determined. -->

<a id="apod-r9"></a>**APOD-R9.** When a whole-pod scrub fails and the pod has not reached a limit of APOD-R8, `recycle.onScrubFailure: warn` holds the pod as a successful scrub would, with the [`scrub_warning`](../../interfaces/kubernetes/labels-and-annotations.md#scrub-warning) annotation, and `recycle.onScrubFailure: fail` sets the claim to `failed`, which drains the pod. <!-- src: §5.2 "`onScrubFailure` behaviors:" -->

<a id="apod-r10"></a>**APOD-R10.** Every pod of a recycling pool requires `recycle.maxSessionsPerPod`, and recycling is enabled only with the deployer's acknowledgment `recycle.acknowledgeBestEffortScrub: true`, because the scrub reaches only what it is designed to reach ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#what-the-scrub-reaches)). <!-- src: §5.2 "Pod retirement policy" ("required with no default"); "Deployer acknowledgment." -->

### Pods with several slots

<a id="apod-r11"></a>**APOD-R11.** A pod with several slots moves to `draining` when its failed slots within a rolling 5-minute window, its leaked slots, and its dead slots together reach half its slot count, rounded up. While draining, no new slot is assigned, its running sessions continue under their own retry policy, and the pod moves to `terminated` once every slot has finished and its cleanup has run. <!-- src: §6.2 "`leaked` slot semantics."; "Concurrent pod lifecycle"; SI-4 "What follows." -->

### Drain, replacement, and disruption

<a id="apod-r12"></a>**APOD-R12.** A gateway replica requests a pod's drain by stamping the annotation [`lenny.dev/drain-request`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-drain-request), and the WarmPoolController moves the pod to `draining`. The WarmPoolController ignores the annotation on a quarantined pod. <!-- src: §4.6.1 "Occupancy projection" (last paragraph); FE-1 "Retirement paths" -->

<a id="apod-r13"></a>**APOD-R13.** The WarmPoolController drains an idle pod when:

- its certificate expires within 30 minutes, and creates a replacement ([Workload certificates](../../cross-cutting/mtls-and-pki.md#rotation));
- its pod set's warm inventory exceeds its target, or its pinned idle pods exceed their bound ([Warm pools](warm-pools.md#pool-r14));
- it holds a warm store no longer on its pod set's rendered list ([Tenant-derived runtimes](tenant-derived-runtimes.md#changing-the-list));
- its node no longer matches its placement ([Placement](placement.md#enforcement));
- its adapter or a slot container has exited.

After a session for which a warm store was withheld ends, the WarmPoolController drains the pod rather than returning it to inventory. <!-- src: §4.6.1 "Responsibilities:"; §6.2 state diagram; DR-11 "Changing the list."; "Withheld stores."; PM-6 "What happens to a pod or pool that fails" -->

<a id="apod-r14"></a>**APOD-R14.** A pod in `draining` moves to `terminated` once its sessions and their cleanup have finished, and a pod in `failed` moves to `draining`. A controller that takes over leadership treats pods already in `draining` as draining and advances them toward deletion; it does not re-target them as idle. <!-- src: §6.2 state diagram; §4.6.1 "Controller crash during active scale-down drain" -->

<a id="apod-r15"></a>**APOD-R15.** Each agent pod has a preStop hook that signals its coordinating gateway replica to take an eviction checkpoint of its sessions before the pod ends, and `terminationGracePeriodSeconds` long enough for that checkpoint to finish and persist. Warm pods are protected from voluntary eviction by their pod set's disruption budget ([Warm pools](warm-pools.md#pool-r16)). A pod in the recycle window or the reserved hold has neither protection: an eviction there deletes the claim through the pod's termination, and the tenant's next session takes the normal acquisition path. <!-- src: §4.6.1 "Disruption protection for agent pods:" -->

### Deletion and quarantine

<a id="apod-r16"></a>**APOD-R16.** Every `AgentPod` carries the finalizer [`lenny.dev/session-cleanup`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-session-cleanup). When an `AgentPod` is being deleted, the WarmPoolController removes the finalizer only when no session holds the pod, or when the pod's session has been checkpointed and the gateway has been told so that it can resume the session on a replacement pod. <!-- src: §4.6.1 "Sandbox finalizers:" -->

<a id="apod-r17"></a>**APOD-R17.** At a pod's termination the WarmPoolController deletes its claim and then its `AgentPod`. <!-- src: §4.6.3 `SandboxClaim` row; REG-CLAIM -->

<a id="apod-r18"></a>**APOD-R18.** A pod carrying the quarantine label projects `quarantined` and is excluded from inventory and from every drain edge, retirement condition, recycle disposition, and slot release. The Kubernetes API refuses the deletion of a quarantined pod or `AgentPod` by anyone but the WarmPoolController after the quarantine's release ([`AgentPod` admission](../../interfaces/kubernetes/agentpod.md#admission)). The quarantine's release moves the pod to `draining`. A quarantined pod no longer counts as warm, so its pod set creates a replacement ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#retirement-paths)). <!-- src: FE-1 "Data model"; "Enforcement points"; "Retirement paths"; "Cost, quota, and billing" -->

## Flows {#flows}

### A pod's life on a recycling pool {#flow-life}

1. The [WarmPoolController](../../components/warm-pool-controller.md) creates the `AgentPod` and its pod (APOD-R2). The pod is `warming`.
2. The adapter reports READY, the controller sets the readiness gate, and the pod projects `idle` (APOD-R3).
3. A session claims the pod ([Pod claims](pod-claims.md#flow-claim)), and the pod projects `claimed`.
4. The last session's release reaches zero occupancy, and the gateway runs the whole-pod scrub ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#whole-pod-scrub)).
5. The gateway decides the recycle disposition (APOD-R7). On a hold the pod projects `reserved`, and then `claimed` again on a rebind or `idle` at hold expiry ([Pod claims](pod-claims.md#flow-hold)). On a retirement the claim becomes `released` and the pod drains.
6. The pod's sessions and cleanup finish, the controller removes the finalizer (APOD-R16), deletes the claim and the `AgentPod` (APOD-R17), and the pod set creates a replacement ([Warm pools](warm-pools.md#pool-r9)).

## Settings {#settings}

```lenny-def
kind: setting
name: recycle.enabled
scope: pool (sessionPolicy)
type: boolean
default: false
summary: Whether the pool's pods serve one session after another (APOD-R7).
```

```lenny-def
kind: setting
name: recycle.acknowledgeBestEffortScrub
scope: pool (sessionPolicy)
type: boolean
default: false
range: "true required when recycle.enabled is true"
summary: The deployer's acknowledgment that the scrub reaches only what it is designed to reach (APOD-R10).
```

```lenny-def
kind: setting
name: recycle.maxSessionsPerPod
scope: pool (sessionPolicy)
type: integer
default: none
range: "> 0; required when recycle.enabled is true"
summary: The served-session count at which a recycling pod retires (APOD-R8).
```

```lenny-def
kind: setting
name: recycle.maxPodUptimeSeconds
scope: pool (sessionPolicy)
type: integer (seconds)
default: absent (no uptime limit)
summary: The age at which a recycling pod drains (APOD-R8).
```

```lenny-def
kind: setting
name: recycle.maxScrubFailures
scope: pool (sessionPolicy)
type: integer
default: 3
summary: The failed whole-pod scrubs at which a pod retires (APOD-R8).
```

```lenny-def
kind: setting
name: recycle.onScrubFailure
scope: pool (sessionPolicy)
type: enum [warn, fail]
default: warn
summary: What a failed whole-pod scrub does to a pod below its limits (APOD-R9).
```

```lenny-def
kind: setting
name: recycle.allowCrossTenantReuse
scope: pool (sessionPolicy)
type: boolean
default: false
range: "refused unless the pool's isolationProfile is microvm and the pool serves one session per pod"
summary: Has no effect on which tenant a pod serves, because the tenant pin holds on every pool (CLAIM-R6).
```
<!-- unresolved: §5.2 keeps this field although the tenant pin makes it inert, and its T4 refusal names `workspaceTier`, which TSS-9 removes; whether the field survives is not determined. -->

```lenny-def
kind: setting
name: sessionPolicy.maxConcurrentSessionsPerPod
scope: pool
type: integer
default: 1
range: ">= 1; above 1 requires sessionPolicy.acknowledgeSharedPod"
summary: The number of sessions one pod serves at once and the slot count of each pod created from the pool (APOD-R2).
```
<!-- unresolved: this setting's behavior (slot assignment, shared-pod acknowledgment) belongs to Slots; it is defined here because APOD-R2 fixes the slot count from it, and the owner should be decided when Slots is written. -->

```lenny-def
kind: setting
name: terminationGracePeriodSeconds
scope: pool
type: integer (seconds)
default: 120
range: ">= the agent-pod floor that the pool configuration validator checks"
summary: The time a pod has to finish its eviction checkpoint before it is killed (APOD-R15).
```

## Alerts {#alerts}

```lenny-def
kind: alert
name: FinalizerStuck
severity: warning
expr: an AgentPod in Terminating with lenny.dev/session-cleanup present for more than 5 min
detects: APOD-R16
summary: The WarmPoolController cannot confirm that a terminating pod's session is safe; follow the stuck finalizer runbook.
```

## Failure behavior {#failure}

- **Pod crash or node failure during a session.** The pod retires through the drain path and never returns to inventory. The session follows its retry policy and resumes on a fresh pod ([Retry and resume](../sessions/retry-and-resume.md#resume-after-pod-failure)).
- **Pod failure with several sessions.** Every active slot fails, each session retries independently, and the pod is replaced.
- **WarmPoolController down.** Phases stop changing, and pods are neither created nor removed. Running sessions continue. Projections resume from the current claims when a leader returns.
- **Node drain.** Warm pods leave one at a time under their budget and are replaced. Pods with sessions take their eviction checkpoint first (APOD-R15). Quarantined pods block the drain ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#retirement-paths)).

## Security properties {#security}

- No runtime process runs on a pod before its session binds, so no process sees a workspace before it is complete (APOD-R3).
- A pod that reaches `idle` or `reserved` after a session has been scrubbed (invariant).
- A quarantined pod keeps its volumes and processes and cannot be deleted through the Kubernetes API (APOD-R18).

## Rationale {#rationale}

- **A coarse phase.** Writing each session's fine state onto the pod would cost several label and status writes per session at scale. The phase changes only when occupancy changes.
- **The controller projects, the gateway claims.** The phase has one writer, so it is never contradicted by a second one, and the gateway needs no write access to `AgentPod` objects.
- **Drain rather than return.** A pod whose claim ends without a scrub may hold a session's state, so the projection drains it, and only the scrubbed `reserved → idle` edge returns a pod to inventory.

## Related capabilities {#related}

- [Warm pools](warm-pools.md) decides how many pods exist.
- [Pod claims](pod-claims.md) writes the claim this page projects.
- [Slots](../sessions/slot-assignment-and-failure.md) and [Slot release and scrub](../sessions/pod-reuse-and-scrub.md) govern what happens inside a pod between sessions.
- [Quarantine](../security-and-compliance/quarantine-and-evidence.md) holds a pod for investigation.
- [Placement](placement.md) renders where a pod runs and drains pods on drifted nodes.
