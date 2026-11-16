---
code: CLAIM
type: capability
area: runtimes-and-pools
summary: >-
  A session acquires an agent pod by claiming it. This page defines the claim and its binding
  states, the claim set a session may take a pod from, the conditions a candidate pod meets, the
  tenant pin, the reserved hold that keeps a recycled pod for its tenant, the claim queue and the
  Postgres fallback, pool exhaustion, and the recovery of orphaned claims.
owns: [objects, requirements, settings, errors, metrics, alerts]
depends-on: [POOL, APOD, SLOTOPS, SLOT, REV, DERIVED, PLACE, SCRUB, FORENSIC, KPCLAIM, GWCLAIM, WPC]
---

# Pod claims

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="claim"></a>Claim | The exclusive occupancy of one agent pod by the gateway on behalf of the sessions it places there. A pod has at most one non-terminal claim at a time. Each claim is recorded as one `PodClaim`. |
| <a id="occupancy-episode"></a>Occupancy episode | The span of one claim: from its creation, across every session placed on the pod and every reserved hold between them, to its deletion. |
| <a id="binding-state"></a>Binding state | The state of a claim: `bound`, `recycling`, `reserved`, `released`, `failed`, or `quarantined`. |
| <a id="claim-set"></a>Claim set | The pod sets a session may take a pod from, fixed by the session's revision (CLAIM-R4). |
| <a id="candidate"></a>Candidate | A pod in the claim set that an acquisition may take (CLAIM-R5). |
| <a id="acquisition"></a>Acquisition | One attempt by the gateway to give a session a pod: the idle scan, the rebind of a reserved pod, the slot claim on a held pod, or the Postgres fallback. |
| <a id="tenant-pin"></a>Tenant pin | The binding of an agent pod to one tenant for the rest of its life, recorded in the pod's tenant label (CLAIM-R6). |
| <a id="pinned-idle-pod"></a>Pinned idle pod | An agent pod in the `idle` phase that carries a tenant pin and has no claim. |
| <a id="used-pod"></a>Used pod | An agent pod that has served a session: a pod whose claim is `reserved`, a pod on a shared pool whose tenant label is set, or a pod on an owned or single-tenant pool that carries the served label. |
| <a id="reserved-hold"></a>Reserved hold | The period after a recycled pod's scrub during which its claim stays `reserved`, so the pinned tenant's next session takes the pod without a new claim (CLAIM-R9). |
| <a id="claim-queue"></a>Claim queue | The per-pool first-in, first-out wait in which a session create waits for a candidate (CLAIM-R12). |
| <a id="postgres-fallback"></a>Postgres fallback | The acquisition that takes a candidate from the pod state mirror when the Kubernetes API path finds none in time (CLAIM-R13). |
| <a id="pod-state-mirror"></a>Pod state mirror | The Postgres table `agent_pod_state`, one row per agent pod, which the WarmPoolController keeps in step with each `AgentPod` (CLAIM-R14). |
| <a id="orphaned-claim"></a>Orphaned claim | A claim whose holder no longer acts on it (CLAIM-R15). |
| <a id="retire-on-release"></a>Retire-on-release mark | A mark the gateway writes on a claim at bind that makes every later release retire the pod (CLAIM-R17). |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Pool | A warmable deployment target for one runtime ([POOL](warm-pools.md#pool)). |
| Pod set | The warm pods of one pool built for one pod-set key ([POOL](warm-pools.md#pod-set)). |
| Pod hash | The digest of a revision's pod-affecting fields ([Runtime revisions](runtime-revisions.md)). |
| Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool ([APOD](agent-pod-lifecycle.md#agent-pod)). |
| Pod phase | The coarse occupancy state of an agent pod ([APOD](agent-pod-lifecycle.md#pod-phase)). |
| Recycle disposition | The gateway's choice, when a recycling pod's occupancy reaches zero, to hold the pod or retire it ([APOD](agent-pod-lifecycle.md#recycle-disposition)). |
| Slot | One session's place on an agent pod ([SLOT](../sessions/session-isolation.md#slot)). |
| Slot claim | The placement of a session on a free slot of a pod the gateway already holds ([SLOTOPS](../sessions/slot-assignment-and-failure.md#slot-claim)). |
| Owned pool | The pool of a tenant-derived runtime, which serves only its owner ([Tenant-derived runtimes](tenant-derived-runtimes.md)). |
| Quarantine | The forensic hold that keeps an agent pod and its volumes for investigation ([Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md)). |
| Session binding | The record on the session row of the pod a session runs on ([Session states](../sessions/session-lifecycle/session-states.md)). |

## Overview {#overview}

Many gateway replicas place sessions at the same time, and two of them can pick the same idle pod. The claim settles that race at the Kubernetes API server. A replica that wants a pod creates the pod's claim under a name derived from the pod. The API server admits one such creation and refuses the rest, so one replica holds the pod and the others try another pod. No controller sits on this path.

A claim covers an occupancy episode rather than one session. A pod that serves several sessions at once keeps one claim while any of them runs. A pod that is recycled between sessions keeps its claim through the scrub and a short reserved hold, so its tenant's next session can take it again without a new claim. The claim ends when the pod is released for good, when the hold expires, or when the pod terminates.

A session takes a pod only from the pod sets built for the revision it was assigned. During a rollout a pool holds pods of several revisions, and a claim that ignored the revision would run a session on a revision it did not choose.

A pod that serves more than one session in its life serves one tenant. The gateway pins the pod to its first tenant, refuses every other tenant's acquisition of it, and keeps the pin for the pod's life, because a recycled pod keeps state that cleanup does not reach.

When no pod is free, the request waits in the pool's claim queue. When the Kubernetes API path stays empty, the gateway tries once more against the Postgres mirror of pod states. When both paths find nothing, the pool's exhaustion setting decides whether the request fails or keeps waiting.

A claim whose gateway replica crashed would hold its pod indefinitely, because nothing deletes a claim on its own. The WarmPoolController's leader finds such claims and reclaims their pods.

## Model {#model}

### Claim {#model-claim}

- A claim names one agent pod and the tenant it was created for. Neither changes after creation.
- A pod has at most one non-terminal claim, and the claim's name is derived from the pod's name.
- A claim does not own its pod, and its pod does not own it. Every deletion of a claim is an explicit step of CLAIM-R2.
- The gateway is the sole writer of a claim's binding state. The WarmPoolController reads it as an input to the pod's phase ([APOD-R4](agent-pod-lifecycle.md#apod-r4)) and never writes it.

### Binding state machine {#state-machine}

```
(created, no state) ──→ bound          the gateway's first status write after the create
bound ──→ recycling                    occupancy reaches zero on a recycling pool and the release
                                       does not end the occupancy without reuse (CLAIM-R3)
recycling ──→ reserved                 the recycle disposition holds the pod
recycling ──→ released                 the recycle disposition retires the pod
recycling ──→ failed                   the scrub fails under onScrubFailure: fail
reserved ──→ bound                     the pinned tenant's next session rebinds within the hold
any non-terminal ──→ quarantined       the pod is quarantined
reserved ──→ (deleted)                 the hold expires, or an acquisition ends it (CLAIM-R5)
bound ──→ (deleted)                    a release ends the occupancy without reuse
any ──→ (deleted)                      orphan reclamation, or the pod terminates
```

`released` and `failed` are terminal, and the pod drains from either. `quarantined` matches no orphan predicate and no hold expiry, and the claim stays until the quarantine's release ([Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md)).

### Invariants {#invariants}

- No pod serves sessions of two tenants, at the same time or one after another.
- A pod in the reserved hold is in no session's candidates except its pinned tenant's rebind.
- Every pod an acquisition takes is in the session's claim set.

## Requirements {#requirements}

### Acquisition

<a id="claim-r1"></a>**CLAIM-R1. A pod is acquired by creating its claim.** A session acquires a pod only by a claim, through [`kubernetes#podclaim.create`](../../interfaces/kubernetes/podclaim.md#podclaim-create). The gateway names the claim `claim-<podName>` and sets `spec.agentPodRef` to the pod's `AgentPod`. Exactly one creation for a pod succeeds. A replica whose creation is refused, by a name conflict or by the check of CLAIM-R11, re-reads its candidates and tries another pod. The gateway writes the first binding state, `bound`, in a separate status write after the creation, because the status subresource is not writable on create. The session binding records which pod a session holds, and per-session idempotency is keyed by the session identifier rather than by the claim. <!-- src: §4.6.1 "Pod claim mechanism (per-pod occupancy claim):"; PLC-3 "PodClaim." -->

<a id="claim-r2"></a>**CLAIM-R2. A claim is deleted only by an explicit step.** A claim lasts one occupancy episode. It is deleted only:

- by the gateway, when the reserved hold expires or an acquisition ends it (CLAIM-R9, CLAIM-R5);
- by the gateway, when a release ends the occupancy without reuse (CLAIM-R3);
- by the WarmPoolController's leader, when it reclaims an orphaned claim (CLAIM-R15);
- by the WarmPoolController, when it completes the pod's termination ([APOD-R17](agent-pod-lifecycle.md#apod-r17)).

<!-- src: §4.6.1 "Pod claim mechanism"; §4.6.3 `SandboxClaim` row; §28.3 `REG-CLAIM` row -->

<a id="claim-r3"></a>**CLAIM-R3. A release ends the occupancy when the last slot frees.** On a pod with one slot every release ends the occupancy, and on a pod with several slots the release that brings slot occupancy to zero ends it ([SLOTOPS-R3](../sessions/slot-assignment-and-failure.md#slotops-r3)). A release ends the occupancy without reuse, and the gateway deletes the claim, when the pool does not recycle, when no gateway replica published the session's binding to the pod, or, on a pod with one slot, when the session failed or crashed. Any other release that ends the occupancy patches the claim to `recycling`. A slot reservation withdrawn after a failed claim creation ends no occupancy. <!-- src: §4.6.3 `SandboxClaim` row; §6.2 "Concurrent pod lifecycle" opening paragraph --> <!-- unresolved: SI-3 says a failure before `session_start` runs the slot release and the slot stays live, while §5.2 retires a one-slot pod after any failed session; the "failed or crashed" clause is kept as the current text states it. -->

<a id="claim-r4"></a>**CLAIM-R4. A session claims only from its revision's pod sets.** The claim set of a session is every `PodSet` of the session's pool whose `spec.podHash` is the pod hash of the session's revision. It includes an earlier pod set of the same pod hash that a change to the pool's or the derived runtime's contributions left claimable ([POOL-R8](warm-pools.md#pool-r8)). The gateway lists the pool's `PodSet` objects by the pool label through [`kubernetes#podset.list`](../../interfaces/kubernetes/podset.md#podset-list), keeps those of the pod hash, and lists candidates by the pool label together with the pod-set labels of those pod sets through [`kubernetes#agentpod.list`](../../interfaces/kubernetes/agentpod.md#agentpod-list). The idle scan, the rebind of a reserved pod, the slot claim, and the Postgres fallback all use the claim set. No acquisition lists candidates by the pool label alone. An empty claim set is treated as a pool with no idle pod, and the gateway never substitutes a pod set of another pod hash. A retried or resumed session uses the claim set of its recorded revision while one exists ([Retry and resume](../sessions/retry-and-resume.md)). <!-- src: PLCR-18; RV-2 "Claim."; RV-6 "Retry and resume."; RVR-16 -->

Substituting another pod hash under load would move the traffic split away from the rollout's weights and run a session on a revision it was not assigned.

<a id="claim-r5"></a>**CLAIM-R5. A candidate meets every admission condition.** An acquisition takes a pod only when all of these hold:

- its phase is `idle`, or, for the pinned tenant's rebind, `reserved`;
- its tenant pin, if any, names the session's tenant (CLAIM-R6);
- it is not a used pod, unless the pool configuration the gateway resolves for this acquisition reuses pods, which is a recycling pool or a pool whose `sessionPolicy.maxConcurrentSessionsPerPod` is above 1;
- every warm store it holds is in the session's covered stores or withheld stores ([Tenant-derived runtimes](tenant-derived-runtimes.md));
- on an owned pool, the claim does not raise the owner's pod charge past its bound ([Tenant-derived runtimes](tenant-derived-runtimes.md));
- it carries no quarantine label.

An acquisition passes over a refused pod and writes nothing to it, with one exception. When the idle scan refuses a used pod in the `idle` phase because the resolved configuration does not reuse pods, and no claim holds the pod, the gateway stamps the pod's drain request ([APOD-R12](agent-pod-lifecycle.md#apod-r12)) so the pod retires. A failed stamp, or a failed read of the pod's claim, does not fail the acquisition, and the next acquisition that refuses the pod stamps it. When an acquisition refuses a `reserved` pod for the same reason, the gateway ends the hold with the precondition-guarded deletion of CLAIM-R9, and the pod returns to `idle` pinned to its tenant. The Postgres fallback stamps no drain request. <!-- src: §5.2 "Tenant pinning:"; §5.2 "Deployer acknowledgment (runtime process kept across sessions)" (the used-pod rule only); DR-5 "Pods"; DR-5 "Bounds"; DR-9; DR-11 "Who reaches a store."; FE-1 "Enforcement points" --> <!-- judgment: SI D4 removes the runtime-process acknowledgment; the used-pod admission rule it carried is kept because DR-5 and DR-9 rely on it ("a served pod is refused and drained on a non-recycling pool"). -->

A pool edited from recycling to not recycling still holds pods that served sessions under the earlier policy. The used-pod condition keeps those pods from serving another session, and the drain request removes them from inventory.

### Tenant pin

<a id="claim-r6"></a>**CLAIM-R6. A pod that can serve more than one session is pinned to one tenant.** A pod on a recycling pool, or on a pool whose `sessionPolicy.maxConcurrentSessionsPerPod` is above 1, is pinned to the first tenant it serves for the rest of its life. The gateway stamps the [`lenny.dev/tenant-id`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-tenant-id) label at the first slot reservation on the pod. On an owned pool and on a single-tenant platform pool the WarmPoolController stamps the owner at the pod's creation, and the gateway stamps [`lenny.dev/served`](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-served) at the first assignment. On every acquisition, the Postgres fallback included, the gateway reads the pin before it creates the claim, and the Postgres fallback reads it inside its row lock. No component resets a pin on a recycled pod, and the Kubernetes API refuses a second, different pin with [`tenant_label_immutable`](#error-tenant-label-immutable). <!-- src: §5.2 "Tenant pinning:"; DR-5 "Pods"; PM-2 "Single-tenant pools"; EG "Network policy and placement" -->

<a id="claim-r7"></a>**CLAIM-R7. A failed pin read binds nothing.** A pin read that fails for a reason other than the pod's absence binds no pod, writes nothing to the pod, and ends the acquisition with the retryable error the endpoint returns for any failed claim: `SESSION_CREATION_FAILED` on session create and `STARTING_FAILED` on start ([Error model](../../cross-cutting/error-model.md)). It is not pool exhaustion: it does not start the Postgres fallback, does not enter the claim queue, and never returns [`WARM_POOL_EXHAUSTED`](#error-warm-pool-exhausted). On the resume path a failed pin read claims no pod, and the session waits for its resume driver ([Retry and resume](../sessions/retry-and-resume.md)). <!-- src: §5.2 "Tenant pinning:" (third paragraph) -->

<a id="claim-r8"></a>**CLAIM-R8. A pinned idle pod is claimable only by its tenant.** A pinned idle pod is a candidate only for its pinned tenant. It leaves inventory when its tenant claims it, when the pinned idle bound drains it ([POOL-R14](warm-pools.md#pool-r14)), when the idle certificate replacement drains it ([APOD-R13](agent-pod-lifecycle.md#apod-r13)), when it retires ([APOD-R8](agent-pod-lifecycle.md#apod-r8)), or when an acquisition refuses it under CLAIM-R5 and stamps its drain request. <!-- src: §5.2 "Pinned idle inventory." (fourth paragraph) -->

A recycled pod keeps state that cleanup does not reach, so the pin holds for the pod's whole life on every pool. Concurrent slots share the pod's network and IPC namespaces and its kernel, so two tenants never hold slots on one pod ([SLOT-R3](../sessions/session-isolation.md#slot-r3)).

### Binding states and fencing

<a id="claim-r9"></a>**CLAIM-R9. Binding-state writes are ordered by the API server.** A claim's binding state takes only the values of the [state machine](#state-machine), and the gateway is its sole writer, through [`kubernetes#podclaim.updateStatus`](../../interfaces/kubernetes/podclaim.md#podclaim-updatestatus). Each write records the time of the transition in `status.bindingStateTransitionTime`, which CLAIM-R15 reads. A write carries the `resourceVersion` it read, and the API server refuses a write against a stale `resourceVersion`. <!-- src: §4.6.3 "`SandboxClaim.status.phase` enumeration"; §4.6.1 "Why optimistic locking provides sufficient fencing:"; FE-1 "Data model" -->

A replica that failed over mid-write carries the `resourceVersion` it read before it stopped. Its write either lands before any other writer touches the claim, which is no double claim, or arrives later and is refused. No further fencing token is needed, and the uniqueness check of CLAIM-R11 covers the window before any other writer has touched the claim. The concurrent-claim and leader-kill tests that verify this rule are in `TESTING.md`.

<a id="claim-r10"></a>**CLAIM-R10. A recycled pod is held for its tenant.** When a recycling pod's occupancy reaches zero and the release does not end the occupancy without reuse, the gateway patches the claim to `recycling`, coordinates the whole-pod scrub ([Pod reuse and scrub](../sessions/pod-reuse-and-scrub.md)), and, when the recycle disposition holds the pod ([APOD-R7](agent-pod-lifecycle.md#apod-r7)), patches the claim to `reserved` and stamps `holdExpiresAt` as the reservation time plus [`gateway.claimHoldTTLSeconds`](#setting-gateway-claimholdttlseconds). A pod that reaches `reserved` has been scrubbed. During the hold:

- A new session of the pinned tenant is placed on the pod by a `reserved → bound` status write with no new claim. Any gateway replica may rebind, and the rebinding replica re-reads the claim after its write before it dispatches.
- When the hold expires, the replica that wrote `reserved` deletes the claim through [`kubernetes#podclaim.delete`](../../interfaces/kubernetes/podclaim.md#podclaim-delete) with the claim's UID and the `resourceVersion` observed at the `reserved` write as preconditions. A rebind that lands first changes the `resourceVersion`, so the deletion fails and the expiry aborts. The pod returns to `idle`.
- The pod is excluded from every other session's candidates (CLAIM-R5), and CLAIM-R11 refuses a second claim on it.

<!-- src: §4.6.1 "Reserved hold (claim retention across same-tenant sessions):"; §6.2 "`reserved` hold semantics." --> <!-- judgment: PL-8 removes the SDK re-warm, so "scrubbed and SDK-warm" becomes "scrubbed"; the `vm-restart` exclusion moves to APOD-R8, which retires such a pod at the boundary. -->

<a id="claim-r11"></a>**CLAIM-R11. The API server refuses a second claim on a pod.** The Kubernetes API refuses the creation of a claim for a pod that already has a non-terminal claim, whatever the new claim's name, and refuses every claim creation while the check cannot run. The check reads the existing claims by the pod they name and reads no binding state. It does not inspect updates, because a claim's spec is immutable after creation and its status writes are ordered under CLAIM-R9. The Kubernetes API also refuses a claim on a pod that carries the quarantine label. A client sees [`PODCLAIM_CONCURRENT`](#error-podclaim-concurrent) or [`PODCLAIM_QUARANTINED`](#error-podclaim-quarantined). <!-- src: §4.6.1 "`SandboxClaim` admission webhook (double-claim prevention):"; PLC-2; FE-1 "Enforcement points" -->

The check keeps the Postgres fallback, which picks its pod from the mirror, from binding a pod that the Kubernetes API path claimed at the same moment.

### Waiting, the fallback, and exhaustion

<a id="claim-r12"></a>**CLAIM-R12. An empty claim set waits, falls back, and then follows the exhaustion setting.** When the claim set holds no candidate, the session create waits in the pool's claim queue for up to [`podClaimQueueTimeout`](#setting-podclaimqueuetimeout), re-reading candidates as pods become idle. When that wait ends without a pod, the gateway tries the Postgres fallback (CLAIM-R13). When the fallback is skipped or finds nothing:

- under `sessionPolicy.onPoolExhausted: reject`, the create fails with [`WARM_POOL_EXHAUSTED`](#error-warm-pool-exhausted);
- under `sessionPolicy.onPoolExhausted: queue`, the request stays in the same queue for up to `sessionPolicy.maxQueueWaitSeconds`, re-enters acquisition as pods free, and fails with the same error when that wait ends.

A queued request holds no pod, no slot, and no claim, and the client receives a session identifier only on success. The error's `details.reason` is `concurrent_slots_exhausted` when the pool had pods with every slot taken and `no_idle_pods` otherwise. <!-- src: §4.6.1 "Controller failover and warm pool sizing:"; "Pool exhaustion behavior (`onPoolExhausted`):"; §5.2 "`onPoolExhausted`"; §5.2 "Slot assignment atomicity" (last sentences) -->

`podClaimQueueTimeout` exceeds the controllers' crash failover window with a margin for a slow API server and slow readiness probes, so a failover alone does not exhaust a sized pool.

<a id="claim-r13"></a>**CLAIM-R13. The Postgres fallback runs only against a fresh mirror and a reachable API server.** The Postgres fallback runs only when both of these hold for the claim set:

- the mirror lag of the claim set's pod sets is at or below [`podClaimFallbackMaxMirrorLagSeconds`](#setting-podclaimfallbackmaxmirrorlagseconds);
- the Kubernetes API server answers a reachability probe, because the check of CLAIM-R11 runs through it.

When either fails, the gateway skips the fallback and proceeds under CLAIM-R12. When both hold, the gateway takes one idle row whose `pod_set` is in the claim set with `SELECT ... FOR UPDATE SKIP LOCKED`, applies CLAIM-R5 inside the row lock, and creates the claim. The row-locked transaction is bounded by `podClaimFallbackMaxMirrorLagSeconds`, and a transaction cut off at that bound binds no pod and ends the acquisition as CLAIM-R7 states. The fallback stamps the tenant pin at first assignment as the Kubernetes API path does. <!-- src: §4.6.1 "Fallback claim path via Postgres:"; "Fallback preconditions (mirror freshness and admission reachability)."; PLC-2 "The Postgres fallback."; DR-11 "Who reaches a store." -->

The mirror can lag the API server, so a fresh mirror bounds the chance of picking a pod already claimed in etcd, and CLAIM-R11 refuses the rest. The fallback serves when the API server's watch stream is degraded and its admission path still works, and it cannot serve when the API server is down.

<a id="claim-r14"></a>**CLAIM-R14. The WarmPoolController keeps one mirror row per pod.** Each agent pod has one row in the pod state mirror, which records its pool, its pod set (`pod_set`, the `PodSet` name), its phase, its tenant and session where it has them, its node, a version for compare-and-set updates, and the time of the last update. The WarmPoolController writes the row on every phase transition. On every start and on every acquisition of its leadership, it re-lists every `AgentPod`, takes each row's pool from the `AgentPod`'s pool label and its pod set from `spec.podSetRef`, rewrites every row in one bulk upsert keyed by pod, and deletes the rows of pods that no longer exist, before it resumes incremental writes. The mirror lag of a set of pod sets is the time since the most recent update of any of their rows. <!-- src: §4.6.1 "WarmPoolController mirror reconciliation on recovery."; §12.6 "`agent_pod_state` table schema."; PLC-2 "The Postgres fallback." --> <!-- unresolved: the mirror also carries the gateway-written recycle counters `sessions_served` and `scrub_failure_count` (§12.6, `REG-PODSTATE`), which APOD-R8 reads; the stored schema's owner page is outside this spike. -->

### Orphaned claims

<a id="claim-r15"></a>**CLAIM-R15. The controller leader reclaims orphaned claims.** Every 60 seconds the WarmPoolController's leader lists every claim and reclaims each one that matches a predicate:

- **Live states.** The binding state is `bound` or `recycling`, the last binding-state transition is older than [`claimOrphanTimeout`](#setting-claimorphantimeout), and no session holds the pod. The leader drains the pod.
- **Reserved.** The binding state is `reserved` and `holdExpiresAt` plus a grace period has passed. The leader deletes the claim with the preconditions of CLAIM-R10, so a concurrent rebind wins, and the pod returns to `idle`.
- **No state.** The claim has no binding state, its creation is older than `claimOrphanTimeout`, and no session holds the pod. The leader drains the pod.

A `quarantined` claim matches no predicate. The leader counts each reclaimed claim in [`lenny_orphaned_claims_total`](#metric-lenny-orphaned-claims-total). <!-- src: §4.6.1 "Orphaned `SandboxClaim` detection:"; FE-1 "Retirement paths" --> <!-- unresolved: the reserved predicate's grace period has no stated value or setting. -->

A pod under a live or unset orphaned claim may hold an unscrubbed session's state, and the controller cannot run the scrub, so it drains the pod. The binding-state transition time replaces the creation time as the orphan key for a claim that has a state, because a claim's creation marks the start of a whole occupancy episode. The loop runs in the WarmPoolController's leader so that the list load on the API server does not grow with the number of gateway replicas.

<a id="claim-r16"></a>**CLAIM-R16. The orphan timeout exceeds every window in which an ended session can act.** `claimOrphanTimeout` exceeds by at least one second each window in which an ended session's work can still act on its slot: a launch bounded by `maxFinalizingTimeoutSeconds`, a restore bounded by the resuming watchdog, and the terminal pipeline budget ([SLOTOPS-R4](../sessions/slot-assignment-and-failure.md#slotops-r4)). The gateway refuses to start, and pool admission refuses a pool, whose configuration breaks this bound. <!-- src: §5.2 "Slot assignment atomicity (`maxConcurrentSessions > 1`)." -->

### Release marks

<a id="claim-r17"></a>**CLAIM-R17. A claim marked retire-on-release never returns its pod.** At bind, for a session that names a user credential binding, holds an identity assertion binding, or declares a repository under a VCS pool with `userAuthorization: required`, the gateway writes the retire-on-release mark on the claim. Every release path reads the mark whatever the pool now says: a marked pod is never recycled, receives no session in another slot after the marked session's slot is released, and retires ([APOD-R8](agent-pod-lifecycle.md#apod-r8)). <!-- src: UC-3 "Limits under both deliveries"; CR-2 "Pods (D11)."; UG-2 --> <!-- unresolved: UCD's term row lists two triggers and UC-3's body and UGI list three; the claim field's name and whether it sits in spec or status are not stated. -->

## Flows {#flows}

### A session claims an idle pod {#flow-claim}

1. Revision selection assigns the session a revision ([Runtime revisions](runtime-revisions.md)).
2. The [pod claimer](../../components/gateway/pod-claimer.md) computes the claim set (CLAIM-R4) and lists candidates.
3. For each candidate, the claimer reads the tenant pin and checks CLAIM-R5, then performs [`kubernetes#podclaim.create`](../../interfaces/kubernetes/podclaim.md#podclaim-create).
4. `lenny-podclaim-guard` admits the claim (CLAIM-R11). On a refusal or a name conflict, the claimer returns to step 3 with another candidate.
5. The claimer writes `bound` (CLAIM-R9) and, on a first assignment, stamps the tenant pin (CLAIM-R6).
6. The [WarmPoolController](../../components/warm-pool-controller.md) observes the claim and projects the pod to `claimed` ([APOD-R5](agent-pod-lifecycle.md#apod-r5)).

### A recycled pod is held and rebound {#flow-hold}

1. The last session on a recycling pod is released, and the gateway patches the claim to `recycling` (CLAIM-R10).
2. The gateway coordinates the whole-pod scrub and decides the recycle disposition ([APOD-R7](agent-pod-lifecycle.md#apod-r7)).
3. On a hold, the gateway patches the claim to `reserved` with `holdExpiresAt`, and the WarmPoolController projects the pod to `reserved`.
4. A session of the same tenant arrives, and a replica writes `reserved → bound` and dispatches it. Otherwise the holder deletes the claim at expiry, and the pod becomes `idle`.

## Settings {#settings}

<a id="setting-podclaimqueuetimeout"></a>

```lenny-def
kind: setting
name: podClaimQueueTimeout
type: duration
default: 60s
range: "greater than the controllers' crash failover window"
scopes: [platform]
read-by: [GWCLAIM]
summary: How long a session create waits in the claim queue before the Postgres fallback (CLAIM-R12).
```

<a id="setting-podclaimfallbackmaxmirrorlagseconds"></a>

```lenny-def
kind: setting
name: podClaimFallbackMaxMirrorLagSeconds
type: integer (seconds)
default: 10
scopes: [platform]
read-by: [GWCLAIM]
summary: The mirror lag above which the Postgres fallback is skipped, and the bound on its row-locked transaction (CLAIM-R13).
```

<a id="setting-gateway-claimholdttlseconds"></a>

```lenny-def
kind: setting
name: gateway.claimHoldTTLSeconds
type: integer (seconds)
default: 10
range: "> 0; configuration validation warns when it is set high"
scopes: [platform]
defaults-by-tier: {small: 10, medium: 10, large: 10}
read-by: [GWCLAIM]
summary: The length of the reserved hold (CLAIM-R10).
```

<a id="setting-claimorphantimeout"></a>

```lenny-def
kind: setting
name: claimOrphanTimeout
type: duration
default: 15m
range: "constrained by CLAIM-R16"
scopes: [platform]
read-by: [WPC, GWCLAIM, SLOTOPS]
summary: The age after which a live or unset claim with no holding session is orphaned (CLAIM-R15), and after which a slot member of an ended session is stale (SLOTOPS-R4).
```

<a id="setting-sessionpolicy-onpoolexhausted"></a>

```lenny-def
kind: setting
name: sessionPolicy.onPoolExhausted
type: enum [reject, queue]
default: reject
scopes: [pool]
read-by: [GWCLAIM]
summary: What a session create does when the claim queue and the Postgres fallback find no pod (CLAIM-R12).
```

<a id="setting-sessionpolicy-maxqueuewaitseconds"></a>

```lenny-def
kind: setting
name: sessionPolicy.maxQueueWaitSeconds
type: integer (seconds)
default: 30
scopes: [pool]
read-by: [GWCLAIM]
summary: The additional wait under onPoolExhausted queue (CLAIM-R12).
```

## Errors {#errors}

<a id="error-warm-pool-exhausted"></a>

```lenny-def
kind: error
name: WARM_POOL_EXHAUSTED
status: 503
category: TRANSIENT
retryable: true
details: {reason: [no_idle_pods, concurrent_slots_exhausted]}
returned-by: [client-api#sessions.create]
rule: CLAIM-R12
summary: No pod could be acquired after the claim queue, the Postgres fallback, and any onPoolExhausted queue wait. The response carries Retry-After.
```

<a id="error-podclaim-concurrent"></a>

```lenny-def
kind: error
name: PODCLAIM_CONCURRENT
status: 403
message: "PodClaim already exists for AgentPod <name>; concurrent claim rejected"
returned-by: [kubernetes#podclaim.create]
rule: CLAIM-R11
summary: The guard refused a second claim on one pod.
```
<!-- unresolved: PLC-2 states the refusal's message and no code; the code name is proposed by this migration. -->

<a id="error-podclaim-quarantined"></a>

```lenny-def
kind: error
name: PODCLAIM_QUARANTINED
status: 403
returned-by: [kubernetes#podclaim.create]
rule: CLAIM-R11
summary: The guard refused a claim on a pod that carries the quarantine label.
```
<!-- unresolved: FE-1 states the refusal without a message or a code; the code name is proposed by this migration. -->

<a id="error-tenant-label-immutable"></a>

```lenny-def
kind: error
name: tenant_label_immutable
status: 403
returned-by: [kubernetes#pod.update]
rule: CLAIM-R6
summary: A write changed lenny.dev/tenant-id outside its transition rule.
```

## Metrics and alerts {#metrics-and-alerts}

<a id="metric-lenny-pod-claim-duration-seconds"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_duration_seconds
type: histogram
labels: [pool]
emitter: GWCLAIM
summary: Time from the session create's receipt to a bound pod.
```

<a id="metric-lenny-pod-claim-queue-depth"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_queue_depth
type: gauge
labels: [pool]
emitter: GWCLAIM
summary: Requests waiting in the claim queue.
```

<a id="metric-lenny-pod-claim-queue-wait-seconds"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_queue_wait_seconds
type: histogram
labels: [pool]
emitter: GWCLAIM
summary: Time a request spent in the claim queue.
```

<a id="metric-lenny-pod-claim-conflict-total"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_conflict_total
type: counter
labels: [pool]
emitter: GWCLAIM
summary: Claim creations that lost to another replica or were refused by the guard.
```

<a id="metric-lenny-pod-claim-timeout-total"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_timeout_total
type: counter
labels: [pool]
emitter: GWCLAIM
summary: Requests whose queue wait ended without a pod.
```

<a id="metric-lenny-pod-claim-fallback-total"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_fallback_total
type: counter
labels: []
emitter: GWCLAIM
summary: Activations of the Postgres fallback.
```

<a id="metric-lenny-pod-claim-fallback-skipped-total"></a>

```lenny-def
kind: metric
name: lenny_pod_claim_fallback_skipped_total
type: counter
labels: [reason]
emitter: GWCLAIM
summary: Fallbacks skipped; reason is mirror_stale or apiserver_unreachable (CLAIM-R13).
```

<a id="metric-lenny-agent-pod-state-mirror-lag-seconds"></a>

```lenny-def
kind: metric
name: lenny_agent_pod_state_mirror_lag_seconds
type: gauge
labels: [pool]
emitter: GWCLAIM
summary: Seconds since the most recent update of the pool's rows in the pod state mirror (CLAIM-R14).
```

<a id="metric-lenny-orphaned-claims-total"></a>

```lenny-def
kind: metric
name: lenny_orphaned_claims_total
type: counter
labels: [pool]
emitter: WPC
summary: Orphaned claims the WarmPoolController's leader reclaimed (CLAIM-R15).
```

<a id="metric-lenny-podclaim-guard-rejections-total"></a>

```lenny-def
kind: metric
name: lenny_podclaim_guard_rejections_total
type: counter
labels: [reason]
emitter: WEBHOOK
summary: Claim creations the guard refused (CLAIM-R11).
```

<a id="alert-podclaimqueuesaturated"></a>

```lenny-def
kind: alert
name: PodClaimQueueSaturated
severity: warning
expr: per pool, lenny_pod_claim_queue_depth > 0.25 × minWarm for more than 30s while lenny_warmpool_idle_pods > 0
detects: CLAIM-R12
summary: Requests queue while idle pods exist, which points at the claim path rather than at pool size.
```
<!-- unresolved: §16.5 is outside this spike; warning is assumed as the severity. -->

<a id="alert-podclaimorphanratehigh"></a>

```lenny-def
kind: alert
name: PodClaimOrphanRateHigh
severity: warning
expr: increase(lenny_orphaned_claims_total[15m]) > 10
detects: CLAIM-R15
summary: Gateways create claims and stop acting on them, which points at gateway instability.
```

<a id="alert-podstatemirrorstale"></a>

```lenny-def
kind: alert
name: PodStateMirrorStale
severity: warning
expr: max by (pool) (lenny_agent_pod_state_mirror_lag_seconds) > 60 for more than 60s
detects: CLAIM-R14
summary: The pod state mirror has stopped following the AgentPod objects, so the Postgres fallback is off for the pool.
```

The alert on the guard's availability, `PodClaimGuardUnavailable`, detects the health of a unit and belongs to the [admission webhooks](../../components/admission-webhooks.md) component.

## Failure behavior {#failure}

- **Losing a creation race.** The losing replica tries another candidate (CLAIM-R1). The retry loop is the defence against concurrent replicas and against a failed replica's stale writes.
- **API server watch degraded.** Candidates go stale, and the queue runs out. The Postgres fallback serves while the mirror is fresh and admission works (CLAIM-R13).
- **API server or etcd unavailable.** No claim can be created, so new sessions fail with a retryable error. Sessions already running continue, because they need no write to the API server.
- **Claim guard unavailable.** Every claim creation is refused (CLAIM-R11). Double claims cannot occur, and new sessions fail.
- **Gateway crash between the claim's creation and its first status write.** The claim has no binding state, and the no-state predicate of CLAIM-R15 reclaims it.
- **Gateway crash during the scrub.** The claim stays in `recycling` with no hold stamp, and the live-state predicate of CLAIM-R15 reclaims it.

## Security properties and threats {#security}

- One pod never serves two tenants. The tenant pin, the label's admission rule, and the claim's per-pod uniqueness act independently (CLAIM-R6, CLAIM-R11).
- A session runs only on pods built for its assigned revision (CLAIM-R4).
- A quarantined pod is never claimed (CLAIM-R5, CLAIM-R11).
- A session never receives a pod holding a warm store it was not checked for (CLAIM-R5).
- **Threat: a gateway bug assigns a pinned pod to another tenant.** The label's admission rule refuses the second pin, and the claim is never bound.
- **Threat: a crashed replica's stale write takes a pod another replica holds.** The `resourceVersion` precondition refuses the stale write (CLAIM-R9).

## Limits {#limits}

- The claim queue does not order requests across gateway replicas.
- The Postgres fallback does not serve while the API server is down.
- A pinned idle pod has no idle timeout of its own, and no bound on pinned idle pods exists beyond the one of [POOL-R14](warm-pools.md#pool-r14).

## Rationale {#rationale}

- **A claim per pod rather than per session.** Claim traffic scales with occupancy episodes. A pod that serves many sessions in turn needs one creation, one deletion, and the binding-state writes of each recycle.
- **The gateway claims, and the controller projects.** Keeping the controller off the claim path removes a single writer from session creation. The API server's uniqueness and the admission check give the exclusion.
- **Reclamation in the controller leader.** One leader lists claims, so the API load of orphan detection does not grow with gateway replicas.

## Related capabilities {#related}

- [Warm pools](warm-pools.md) supplies the pods a claim takes.
- [Agent pod lifecycle](agent-pod-lifecycle.md) projects each pod's phase from its claim.
- [Slot assignment and failure](../sessions/slot-assignment-and-failure.md) places sessions on free slots of a pod already claimed.
- [Runtime revisions](runtime-revisions.md) assigns the revision that fixes the claim set.
- [Quarantine and evidence](../security-and-compliance/quarantine-and-evidence.md) freezes a claim in `quarantined`.
