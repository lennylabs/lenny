---
code: CLAIM
type: capability
area: runtimes-and-pools
summary: >-
  A session acquires an agent pod by claiming it. This page defines the claim, its binding states,
  the claim set a session may take a pod from, the tenant pin, the reserved hold that keeps a
  recycled pod for its tenant, the claim queue and the Postgres fallback, and the recovery of
  orphaned claims.
owns: [objects, requirements, settings, alerts]
depends-on: [POOL, APOD, REV, DERIVED, SLOT, SCRUB, QUAR, SESS]
---

# Pod claims

A session acquires an agent pod by claiming it. This page defines the claim, its binding states, the claim set a session may take a pod from, the tenant pin, the reserved hold that keeps a recycled pod for its tenant, the claim queue and the Postgres fallback, and the recovery of orphaned claims.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="claim"></a>Claim | The exclusive occupancy of one agent pod by the gateway on behalf of the sessions it places there. A pod has at most one claim at a time. Each claim is recorded as one [`PodClaim`](../../interfaces/kubernetes/podclaim.md). |
| Occupancy episode | The span of one claim: from the claim's creation, across every session placed on the pod and every reserved hold between them, to the claim's deletion. |
| <a id="binding-states"></a>Binding state | The state of a claim: `bound`, `recycling`, `reserved`, `released`, `failed`, or `quarantined` (CLAIM-R8). |
| <a id="claim-set"></a>Claim set | The pod sets a session may take a pod from: the pool's pod sets whose pod hash is the session's revision's pod hash (CLAIM-R4). |
| Candidate | A pod in the claim set that the claim may take (CLAIM-R5). |
| <a id="tenant-pin"></a>Tenant pin | The binding of an agent pod to the first tenant it serves, for the rest of its life (CLAIM-R6). |
| <a id="pinned-idle-pod"></a>Pinned idle pod | An agent pod in the `idle` phase, carrying a tenant pin, with no claim. Only its pinned tenant can claim it. |
| <a id="reserved-hold"></a>Reserved hold | The period after a recycled pod's scrub during which its claim stays in `reserved`, so the pinned tenant's next session can take the pod without a new claim (CLAIM-R9). |
| Claim queue | The per-pool first-in, first-out wait in which a session create waits for a candidate (CLAIM-R12). |
| <a id="postgres-fallback"></a>Postgres fallback | The claim path that takes a candidate from the pod state mirror when the Kubernetes API path finds none in time (CLAIM-R13). |
| Pod state mirror | The Postgres table `agent_pod_state`, one row per agent pod, which the WarmPoolController keeps in step with each `AgentPod` (CLAIM-R14). |
| Orphaned claim | A claim whose holder no longer acts on it (CLAIM-R15). |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pool | A warmable deployment target for one runtime ([Warm pools](warm-pools.md#pool)). |
| Pod set | The warm pods of one pool built for one pod-set key ([Warm pools](warm-pools.md#pod-set)). |
| Pod hash | The digest of a revision's pod-affecting fields ([Runtime revisions and rollouts](runtime-revisions.md#pod-hash)). |
| Revision selection | The choice of a runtime revision for a session ([Runtime revisions and rollouts](runtime-revisions.md#selection)). |
| Agent pod | A Kubernetes pod that serves sessions for one pool ([Agent pod lifecycle](agent-pod-lifecycle.md#agent-pod)). |
| Phase | The coarse occupancy state of an agent pod ([Agent pod lifecycle](agent-pod-lifecycle.md#phases)). |
| Recycle disposition | The decision, when a recycling pod's occupancy reaches zero, to hold the pod for reuse or retire it ([Agent pod lifecycle](agent-pod-lifecycle.md#recycle)). |
| Whole-pod scrub | The cleanup of pod-wide state when a recycling pod's occupancy reaches zero ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#whole-pod-scrub)). |
| Slot | One session's place on an agent pod ([Slots](../sessions/slot-assignment-and-failure.md#slot)). |
| Slot claim | The placement of a session on a free slot of a pod the gateway already holds ([Slots](../sessions/slot-assignment-and-failure.md#slot-claim)). |
| Covered stores | The warm repository stores a session was checked for at its claim ([Tenant-derived runtimes](tenant-derived-runtimes.md#covered-stores)). |
| Session binding | The record on the session row of the pod a session runs on ([Session lifecycle](../sessions/session-lifecycle/index.md#pod-assignment)). |
| Quarantine | The forensic hold that keeps an agent pod and its volumes for investigation ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#quarantine)). |

## Overview {#overview}

Many gateway replicas place sessions at the same time, and two of them can pick the same idle pod. The claim settles that race at the Kubernetes API server. A replica that wants a pod creates the pod's claim under a name derived from the pod. The API server admits one such creation and refuses the rest, so exactly one replica holds the pod, and the losers try another pod. No controller sits on this path.

A claim covers an occupancy episode rather than a single session. A pod that serves several sessions at once keeps one claim while any of them runs. A pod that is recycled between sessions keeps its claim through the scrub and through a short reserved hold, so its tenant's next session can take it again without a new claim. The claim ends when the pod is released for good, when the hold expires, or when the pod terminates.

A session takes a pod only from the pod sets built for the revision it was assigned. During a rollout a pool holds pods of several revisions, and a claim that ignored this would run a session on a revision it did not choose.

When no pod is free, the request waits in the pool's claim queue. If the Kubernetes API path stays empty, the gateway tries once more against the Postgres mirror of pod states, which keeps working when the API server's watch stream is degraded. If both paths find nothing, the pool's exhaustion setting decides whether the request fails or keeps waiting.

A claim whose gateway replica crashed would hold its pod forever, because nothing deletes a claim automatically. The WarmPoolController's leader finds such claims and reclaims their pods.

## Model {#model}

### Claim {#model-claim}

- A claim names exactly one agent pod and the tenant it was created for. Neither changes after creation.
- A pod has at most one non-terminal claim. The claim's name is derived from the pod's name.
- A claim does not own its pod, and its pod does not own it. Every deletion of a claim is an explicit step of CLAIM-R2.
- The gateway is the sole writer of a claim's binding state. The WarmPoolController reads it as input to the pod's phase ([APOD-R4](agent-pod-lifecycle.md#apod-r4)) and never writes it.

### Binding state machine {#state-machine}

```
(created, no state) ──→ bound          gateway's first status patch after the create
bound ──→ recycling                    occupancy reaches zero on a recycling pool and the release
                                       does not end the occupancy without reuse (CLAIM-R3)
recycling ──→ reserved                 the recycle disposition holds the pod (APOD-R7)
recycling ──→ released                 the recycle disposition retires the pod (APOD-R8)
recycling ──→ failed                   the scrub fails under onScrubFailure: fail
reserved ──→ bound                     the pinned tenant's next session rebinds within the hold
any non-terminal ──→ quarantined       the pod is quarantined
reserved ──→ (deleted)                 the hold expires
bound ──→ (deleted)                    a release ends the occupancy without reuse
any ──→ (deleted)                      orphan reclamation, or the pod terminates
```

`released` and `failed` are terminal, and the pod drains from either. `quarantined` matches no orphan predicate and no hold expiry, and the claim stays until the quarantine's release ([Quarantine](../security-and-compliance/quarantine-and-evidence.md#data-model)).

### Invariants {#invariants}

- No two sessions of different tenants hold one pod at the same time, and no pod pinned to one tenant is claimed for another.
- A pod in the reserved hold is in no session's candidate list except its pinned tenant's rebind.
- Every pod the claim takes is in the session's claim set.

## Requirements {#requirements}

### Acquisition

<a id="claim-r1"></a>**CLAIM-R1.** A session acquires a pod only by a claim. The gateway creates the claim named `claim-<podName>`. Exactly one creation for a pod succeeds; every other replica's creation is refused, and that replica re-reads its candidates and tries another pod. The gateway writes the first binding state, `bound`, in a separate status write after the creation. The session binding records which pod a session holds, and per-session idempotency is keyed by the session identifier rather than by the claim. <!-- src: §4.6.1 "Pod claim mechanism (per-pod occupancy claim):"; PLC-3 "PodClaim." -->

<a id="claim-r2"></a>**CLAIM-R2.** A claim lasts one occupancy episode. It is deleted only:

- by the gateway, when the reserved hold expires (CLAIM-R9);
- by the gateway, when a release ends the occupancy without reuse (CLAIM-R3);
- by the WarmPoolController's leader, when it reclaims an orphaned claim (CLAIM-R15);
- by the WarmPoolController, when it completes the pod's termination.

<!-- src: §4.6.1 "Pod claim mechanism"; §4.6.3 `SandboxClaim` row; §28.3 `REG-CLAIM` row --> <!-- unresolved: §4.6.1 and §6.2 also delete the claim "when an acquisition refuses the reserved pod under the runtime-process acknowledgment"; SI-3 retires `recycle.runtimeProcess` and that acknowledgment, so the path is dropped here. -->

<a id="claim-r3"></a>**CLAIM-R3.** On a pod with one slot every release ends the occupancy, and on a pod with several slots the release that brings slot occupancy to zero ends it. A release ends the occupancy without reuse, and the gateway deletes the claim, when the pool does not recycle, when no gateway replica published the session's binding to the pod, or, on a pod with one slot, when the session failed or crashed. Any other release that ends the occupancy patches the claim to `recycling`. A slot reservation withdrawn after a failed claim creation ends no occupancy. <!-- src: §4.6.3 `SandboxClaim` row -->

<a id="claim-r4"></a>**CLAIM-R4.** The claim set of a session is every `PodSet` of the session's pool whose `spec.podHash` is the pod hash of the session's revision. It includes an earlier pod set of the same pod hash that a change to the contributions left claimable ([Warm pools](warm-pools.md#pool-r8)). The gateway lists the pool's `PodSet` objects by the pool label, keeps those of the pod hash, and lists candidates by the pool label together with the pod-set labels of those pod sets. The idle scan, the rebind of a reserved pod, the slot claim, and the Postgres fallback all use the claim set. No claim path lists candidates by the pool label alone, and an empty claim set is treated as a pool with no idle pod; the gateway never substitutes a pod set of another pod hash. A retried or resumed session uses the claim set of its recorded revision while one exists ([Retry and resume](../sessions/retry-and-resume.md#revision-on-resume)). <!-- src: PLCR-18; RV-2 "Claim."; RV-6 "Retry and resume." -->

Substituting another pod hash under load would move the traffic split away from the rollout's weights and run a session on a revision it was not assigned.

<a id="claim-r5"></a>**CLAIM-R5.** A pod is a candidate for a session only when all of the following hold:

- its phase is `idle`, or, for the pinned tenant's rebind, `reserved`;
- its tenant pin, if any, names the session's tenant (CLAIM-R6);
- on an owned pool, it has not served a session unless the pool reuses pods ([Tenant-derived runtimes](tenant-derived-runtimes.md#served-label));
- every warm store it holds is in the session's covered stores or withheld stores ([Tenant-derived runtimes](tenant-derived-runtimes.md#changing-the-list));
- it carries no quarantine label.

A pod refused on any of these grounds is passed over and left as it was. <!-- src: §5.2 "Tenant pinning:"; DR-5 "Pods"; DR-11 "Who reaches a store."; FE-1 "Enforcement points" -->

### Tenant pin

<a id="claim-r6"></a>**CLAIM-R6.** A pod on a pool that recycles, or whose pods serve more than one session at once, is pinned to the first tenant it serves for the rest of its life. The gateway stamps the [`lenny.dev/tenant-id`](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-tenant-id) label at first assignment, and on an owned pool the WarmPoolController stamps the owner at the pod's creation. On every acquisition, the Postgres fallback included, the gateway reads the pin before it creates the claim. No component resets a pin on a recycled pod. A pin read that fails for a reason other than the pod's absence binds no pod, writes nothing to the pod, and ends the acquisition with the retryable error the endpoint returns for any failed claim; it is not pool exhaustion, it does not start the Postgres fallback, and it does not enter the claim queue. On the resume path such a failure leaves the session waiting for its resume driver. <!-- src: §5.2 "Tenant pinning:"; DR-5 "Pods" -->

A recycled pod keeps state the scrub does not reach, so the pin holds for the pod's whole life on every pool, including a pool that sets `recycle.allowCrossTenantReuse` ([Agent pod lifecycle](agent-pod-lifecycle.md#settings)). The Kubernetes API refuses a second, different pin as [the label's admission rule](../../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-tenant-id) states.

<a id="claim-r7"></a>**CLAIM-R7.** A pinned idle pod is claimable only by its pinned tenant. It leaves inventory when its tenant claims it, when the warm-pool bound drains it ([Warm pools](warm-pools.md#pool-r14)), or when the pod retires ([Agent pod lifecycle](agent-pod-lifecycle.md#apod-r8)). <!-- src: §5.2 "Pinned idle inventory." -->

### Binding states and fencing

<a id="claim-r8"></a>**CLAIM-R8.** A claim's binding state takes only the values of the [state machine](#state-machine). The gateway is its sole writer. Each binding-state write records the time of the transition, which CLAIM-R15 reads. Concurrent writes to one claim are ordered by the API server's optimistic concurrency: a write carries the `resourceVersion` it read, and a write against a stale `resourceVersion` is refused. <!-- src: §4.6.3 "`SandboxClaim.status.phase` enumeration"; §4.6.1 "Why optimistic locking provides sufficient fencing:"; FE-1 "Data model" -->

A replica that failed over mid-write carries the `resourceVersion` it read before it stopped. Its write either lands before any other writer touches the claim, which is no double claim, or arrives after and is refused. No further fencing token is needed. The per-pod uniqueness check of CLAIM-R11 covers the window before any other writer has touched the claim. <!-- unresolved: §4.6.1 makes this a "Phase 1 blocking prerequisite" verified by named tests and recorded as ADR-007; the verification is build-plan content and leaves the specification, and REC-1 removes ADRs. -->

<a id="claim-r9"></a>**CLAIM-R9.** When a recycling pod's occupancy reaches zero and the release does not end the occupancy without reuse, the gateway patches the claim to `recycling`, coordinates the whole-pod scrub, and, when the recycle disposition holds the pod, patches the claim to `reserved` and stamps `holdExpiresAt` as the reservation time plus `gateway.claimHoldTTLSeconds`. A pod that reaches `reserved` is always scrubbed. During the hold:

- A new session of the pinned tenant is placed on the pod by a `reserved → bound` patch with no new claim. Any gateway replica may rebind, and the rebinding replica re-reads the claim after its patch before it dispatches.
- When the hold expires, the gateway deletes the claim with the claim's UID and the `resourceVersion` observed at the `reserved` patch as preconditions. A rebind that lands first changes the `resourceVersion`, so the deletion fails and the expiry aborts.
- The pod is excluded from every other session's candidates (CLAIM-R5), and CLAIM-R11 refuses a second claim on it.

<!-- src: §4.6.1 "Reserved hold (claim retention across same-tenant sessions):"; §6.2 "`reserved` hold semantics." -->

<a id="claim-r10"></a>**CLAIM-R10.** A pod whose claim is in `reserved` counts as occupied for inventory and scaling ([Warm pools](warm-pools.md#pool-r15)). <!-- src: §4.6.2 "Reserved pods count as occupied." -->

<a id="claim-r11"></a>**CLAIM-R11.** The Kubernetes API refuses the creation of a claim for a pod that already has a non-terminal claim, whatever the new claim's name, and refuses every claim creation while the check cannot run. The check reads the existing claims by the pod they name and reads no phase. It does not inspect updates, because a claim's spec is immutable after creation and its status writes are ordered under CLAIM-R8. The Kubernetes API also refuses a claim on a pod that carries the quarantine label. The client-visible outcome is stated in [`PodClaim` admission](../../interfaces/kubernetes/podclaim.md#admission). <!-- src: §4.6.1 "`SandboxClaim` admission webhook (double-claim prevention):"; FE-1 "Enforcement points" -->

The check is what keeps the Postgres fallback, which picks its pod from the mirror, from binding a pod that the Kubernetes API path claimed at the same moment.

### Waiting and the fallback

<a id="claim-r12"></a>**CLAIM-R12.** When the claim set holds no candidate, the session create waits in the pool's claim queue for up to `podClaimQueueTimeout`, re-reading candidates as pods become idle. When that wait ends without a pod, the gateway tries the Postgres fallback (CLAIM-R13). When the fallback is skipped or finds nothing:

- under `sessionPolicy.onPoolExhausted: reject`, the create fails with the retryable pool-exhaustion error;
- under `sessionPolicy.onPoolExhausted: queue`, the request stays in the same queue for up to `sessionPolicy.maxQueueWaitSeconds` and re-enters acquisition as pods free, and fails with the same error when that wait ends.

A queued request holds no pod, no slot, and no claim, and the client receives a session identifier only on success. The error states whether the pool had pods with every slot taken or had no idle pod ([`client-api#sessions.create`](../../interfaces/client-api/sessions.md#sessions-create)). <!-- src: §4.6.1 "Controller failover and warm pool sizing:"; "Pool exhaustion behavior (`onPoolExhausted`):"; §5.2 "`onPoolExhausted`"; §5.2 "Slot assignment atomicity" -->

`podClaimQueueTimeout` exceeds the controllers' crash failover window with margin for a slow API server and slow readiness probes, so a failover alone does not exhaust a sized pool.

<a id="claim-r13"></a>**CLAIM-R13.** The Postgres fallback runs only when both of these hold for the pool:

- the mirror lag for the pool is at or below `podClaimFallbackMaxMirrorLagSeconds`;
- the Kubernetes API server answers a reachability probe, because CLAIM-R11's check runs through it.

When either fails, the gateway skips the fallback and proceeds under CLAIM-R12. When both hold, the gateway takes one idle row of the claim set's pod sets with `SELECT ... FOR UPDATE SKIP LOCKED`, reads the tenant pin and checks the covered stores inside the row lock, and creates the claim. The row-locked transaction is bounded by `podClaimFallbackMaxMirrorLagSeconds`; a transaction cut off at that bound binds no pod and ends the acquisition with the retryable error of a failed pin read rather than following `onPoolExhausted`. The fallback stamps the tenant pin at first assignment as the Kubernetes API path does. <!-- src: §4.6.1 "Fallback claim path via Postgres:"; "Fallback preconditions (mirror freshness and admission reachability)."; PLC-2 "The Postgres fallback."; DR-11 "Who reaches a store." -->

The mirror can lag the API server, so a fresh mirror bounds the chance of picking a pod already claimed in etcd, and CLAIM-R11 refuses the rest. The fallback helps when the API server's watch stream is degraded and its admission path still works; it does nothing when the API server is down.

<a id="claim-r14"></a>**CLAIM-R14.** Each agent pod has one row in the pod state mirror, which records its pool, its pod set, its phase, its tenant and session where it has them, its node, a version for compare-and-set updates, and the time of the last update. The WarmPoolController writes the row on every phase transition. On every start and on every acquisition of its leadership, it re-lists every `AgentPod`, rewrites every row in one bulk upsert keyed by pod, and deletes the rows of pods that no longer exist, before it resumes incremental writes. The mirror lag of a pool is the time since the most recent update of any of its rows. <!-- src: §4.6.1 "WarmPoolController mirror reconciliation on recovery."; §12.6 "`agent_pod_state` table schema."; PLC-2 "The Postgres fallback." --> <!-- unresolved: the mirror also carries the gateway-written recycle counters `sessions_served` and `scrub_failure_count` (§12.6, `REG-PODSTATE`), which APOD-R8 reads; the stored schema's owner page is outside this slice. -->

### Orphaned claims

<a id="claim-r15"></a>**CLAIM-R15.** Every 60 seconds the WarmPoolController's leader lists every claim and reclaims each one that matches a predicate:

- **Live states.** The binding state is `bound` or `recycling`, the last binding-state transition is older than `claimOrphanTimeout`, and no session holds the pod ([Session lifecycle](../sessions/session-lifecycle/index.md#post-terminal-window)). The leader drains the pod.
- **Reserved.** The binding state is `reserved` and `holdExpiresAt` plus a grace period has passed. The leader deletes the claim with the preconditions of CLAIM-R9, so a concurrent rebind wins, and the pod returns to `idle`.
- **No state.** The claim has no binding state, its creation is older than `claimOrphanTimeout`, and no session holds the pod. The leader drains the pod.

A `quarantined` claim matches no predicate. <!-- src: §4.6.1 "Orphaned `SandboxClaim` detection:"; FE-1 "Retirement paths" --> <!-- unresolved: the reserved predicate's grace period has no stated value or setting. -->

A pod under a live or unset orphaned claim may hold an unscrubbed session's state, and the controller has no path to run the scrub, so it drains the pod rather than return it to `idle`. The binding-state transition time replaces the creation time as the orphan key for a claim that has a state, because a claim's creation marks the start of a whole occupancy episode. The loop runs in the WarmPoolController's single leader so that the list load on the API server does not grow with the number of gateway replicas.

<a id="claim-r16"></a>**CLAIM-R16.** `claimOrphanTimeout` exceeds by at least one second each window in which an ended session's work can still act on its slot: a launch bounded by `maxFinalizingTimeoutSeconds`, a restore bounded by the resuming watchdog, and the terminal pipeline budget ([Slots](../sessions/slot-assignment-and-failure.md#stale-slot-release)). The gateway refuses to start, and pool admission refuses a pool, whose configuration breaks this bound. <!-- src: §5.2 "Slot assignment atomicity (`maxConcurrentSessions > 1`)." -->

## Flows {#flows}

### A session claims an idle pod {#flow-claim}

1. Revision selection assigns the session a revision ([Runtime revisions and rollouts](runtime-revisions.md#selection)).
2. The [pod claimer](../../components/gateway/pod-claimer.md) computes the claim set (CLAIM-R4) and lists candidates through the Kubernetes API ([`AgentPod`](../../interfaces/kubernetes/agentpod.md)).
3. For a candidate, the claimer reads the tenant pin and runs the store check (CLAIM-R5, CLAIM-R6), then creates the claim ([`PodClaim` create](../../interfaces/kubernetes/podclaim.md#create)).
4. The [`lenny-podclaim-guard`](../../components/admission-webhooks.md#podclaim-guard) webhook admits it (CLAIM-R11). On a refusal or a name conflict, the claimer returns to step 3 with another candidate.
5. The claimer writes `bound` (CLAIM-R8) and, on a first assignment, stamps the tenant pin.
6. The [WarmPoolController](../../components/warm-pool-controller.md) observes the claim and projects the pod to `claimed` ([APOD-R5](agent-pod-lifecycle.md#apod-r5)).

### A recycled pod is held and rebound {#flow-hold}

1. The last session on a recycling pod is released, and the gateway patches the claim to `recycling` (CLAIM-R9).
2. The gateway coordinates the whole-pod scrub ([Slot release and scrub](../sessions/pod-reuse-and-scrub.md#whole-pod-scrub)) and evaluates the recycle disposition ([APOD-R7](agent-pod-lifecycle.md#apod-r7)).
3. On a hold, the gateway patches the claim to `reserved` with `holdExpiresAt`, and the WarmPoolController projects the pod to `reserved`.
4. A session of the same tenant arrives: any replica patches `reserved → bound` and dispatches it. Otherwise the holder deletes the claim at expiry and the pod becomes `idle`.

## Settings {#settings}

```lenny-def
kind: setting
name: podClaimQueueTimeout
scope: gateway
type: duration
default: 60s
range: "> the controllers' crash failover window"
summary: How long a session create waits in the claim queue before the Postgres fallback (CLAIM-R12).
```

```lenny-def
kind: setting
name: podClaimFallbackMaxMirrorLagSeconds
scope: gateway flag
type: integer (seconds)
default: 10
summary: The mirror lag above which the Postgres fallback is skipped, and the bound on its row-locked transaction (CLAIM-R13).
```

```lenny-def
kind: setting
name: gateway.claimHoldTTLSeconds
scope: deployment (Helm)
type: integer (seconds)
default: 10
range: "> 0; configuration validation warns when it is set high"
summary: The length of the reserved hold (CLAIM-R9). A long hold delays the pod's return to idle inventory and depresses apparent idle inventory.
```

```lenny-def
kind: setting
name: claimOrphanTimeout
scope: WarmPoolController flag --claim-orphan-timeout; the gateway's stale-slot release uses the same value
type: duration
default: 15m
range: "constrained by CLAIM-R16"
summary: The age after which a live or unset claim with no holding session is orphaned (CLAIM-R15).
```

```lenny-def
kind: setting
name: sessionPolicy.onPoolExhausted
scope: pool
type: enum [reject, queue]
default: reject
summary: What a session create does when the claim queue and the Postgres fallback find no pod (CLAIM-R12).
```

```lenny-def
kind: setting
name: sessionPolicy.maxQueueWaitSeconds
scope: pool
type: integer (seconds)
default: 30
summary: The additional wait under onPoolExhausted queue (CLAIM-R12).
```

## Alerts {#alerts}

```lenny-def
kind: alert
name: PodClaimQueueSaturated
severity: warning
expr: per pool, lenny_pod_claim_queue_depth > 0.25 × minWarm for more than 30s while lenny_warmpool_idle_pods > 0
detects: CLAIM-R12
summary: Requests queue while idle pods exist, which points at the claim path rather than at pool size.
```
<!-- unresolved: §16.5 states the severity cell outside the excerpt read; warning is assumed. -->

```lenny-def
kind: alert
name: PodClaimOrphanRateHigh
severity: warning
expr: increase(lenny_orphaned_claims_total[15m]) > 10
detects: CLAIM-R15
summary: Gateways create claims and stop acting on them, which points at gateway instability.
```

```lenny-def
kind: alert
name: PodStateMirrorStale
severity: warning
expr: max by (pool) (lenny_agent_pod_state_mirror_lag_seconds) > 60 for more than 60s
detects: CLAIM-R14
summary: The pod state mirror has stopped following the AgentPod objects, so the Postgres fallback is off for the pool.
```

```lenny-def
kind: alert
name: PodClaimGuardUnavailable
severity: critical
expr: lenny-podclaim-guard unreachable for more than 30s
detects: CLAIM-R11
summary: Every claim creation is refused, so no session can acquire a new pod.
```

## Failure behavior {#failure}

- **Losing a creation race.** The losing replica tries another candidate (CLAIM-R1). The retry loop is the defence against concurrent replicas and against a failed replica's stale writes.
- **API server watch degraded.** Candidates go stale and the queue runs out. The Postgres fallback serves while the mirror is fresh and admission works (CLAIM-R13).
- **API server or etcd unavailable.** No claim can be created, so new sessions fail with a retryable error and clients back off. Sessions already running continue, because they need no write to the API server.
- **Claim guard unavailable.** Every claim creation is refused (CLAIM-R11). Double claims cannot occur, and new sessions fail.
- **Gateway crash between the claim's creation and its first status write.** The claim has no binding state, and CLAIM-R15's predicate for that case reclaims it.
- **Gateway crash during the scrub.** The claim stays in `recycling` with no hold stamp, and CLAIM-R15's live-state predicate reclaims it.

## Security properties {#security}

- One pod never serves two tenants: the tenant pin, the label's admission rule, and the claim's per-pod uniqueness act independently (CLAIM-R6, CLAIM-R11).
- A session runs only on pods built for its assigned revision (CLAIM-R4).
- A quarantined pod is never claimed (CLAIM-R5, CLAIM-R11).
- A session never receives a pod holding a warm store it was not checked for (CLAIM-R5).

## Rationale {#rationale}

- **A claim per pod rather than per session.** Claim traffic scales with occupancy episodes. A pod that serves many sessions in turn needs one creation and one deletion, and the binding-state patches of each recycle.
- **The gateway claims, the controller projects.** Keeping the controller off the claim path removes a single writer from session creation. The API server's uniqueness and the admission check give the exclusion.
- **Reclamation in the controller leader.** One leader lists claims, so the API load of orphan detection does not grow with gateway replicas.

## Related capabilities {#related}

- [Warm pools](warm-pools.md) supplies the pods a claim takes.
- [Agent pod lifecycle](agent-pod-lifecycle.md) projects each pod's phase from its claim.
- [Slots](../sessions/slot-assignment-and-failure.md) places sessions on free slots of a pod already claimed.
- [Runtime revisions and rollouts](runtime-revisions.md) assigns the revision that fixes the claim set.
- [Quarantine](../security-and-compliance/quarantine-and-evidence.md) freezes a claim in `quarantined`.
