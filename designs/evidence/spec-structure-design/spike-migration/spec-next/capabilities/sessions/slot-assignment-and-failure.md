---
code: SLOTOPS
type: capability
area: sessions
summary: >-
  A session on a shared pod is placed in one of the pod's slots. This page states how the gateway
  reserves and releases a slot atomically, how it places a session on a held pod before claiming a
  new one, how slot occupancy survives a Redis restart or outage, how stale reservations are
  released, and how a failed slot is retried.
owns: [requirements, settings, metrics]
depends-on: [SLOT, CLAIM, APOD, RESUME, SEND, GWCLAIM, REDIS]
---

# Slot assignment and failure

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="slot-occupancy"></a>Slot occupancy | The number of a pod's slots that are held by a session, leaked, or dead: the cardinality of the pod's slot set. |
| <a id="slot-claim"></a>Slot claim | The placement of a session on a free slot of a pod the gateway already holds, without a new claim (SLOTOPS-R2). |
| <a id="slot-reservation"></a>Slot reservation | The atomic step that adds a session to a pod's slot set when the set has room (SLOTOPS-R1). |
| <a id="stale-member"></a>Stale member | A session's entry in a slot set whose session has been in a terminal state for longer than `claimOrphanTimeout` (SLOTOPS-R4). |
| <a id="rehydration"></a>Rehydration | The rebuilding of a pod's slot set from Postgres after Redis lost it (SLOTOPS-R6). |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Slot | One session's place on an agent pod ([SLOT](session-isolation.md#slot)). |
| Shared pod | An agent pod that can serve more than one session at the same time ([SLOT](session-isolation.md#shared-pod)). |
| Leaked slot | A slot whose cleanup was not acknowledged clean ([SLOT](session-isolation.md#leaked-slot)). |
| Dead slot | A slot that can serve no session for the rest of its pod's life ([SLOT](session-isolation.md#dead-slot)). |
| Slot count | The number of slot containers rendered into a pod ([APOD](../runtimes-and-pools/agent-pod-lifecycle.md#slot-count)). |
| Claim set | The pod sets a session may take a pod from ([CLAIM](../runtimes-and-pools/pod-claims.md#claim-set)). |
| Slot set | The gateway's Redis register of a pod's slot occupancy ([GWCLAIM](../../components/gateway/pod-claimer.md#register-slot-set)). |

## Overview {#overview}

A claim gives the gateway a pod, and the slot set decides how many sessions the pod holds. Many gateway replicas place sessions at once, so the check that a slot is free and the reservation of that slot run as one step in Redis. A session that arrives for a tenant that already holds a shared pod with a free slot is placed there, and the gateway claims a new pod only when no held pod has room.

Redis can lose the slot sets. After a restart, the gateway rebuilds a pod's set from the sessions Postgres records before it reserves on that pod. During an outage, Postgres gates capacity directly.

A slot that fails does not end the pod's other sessions. The gateway retries the failed session in a fresh slot, and a pod that accumulates failed, leaked, or dead slots is replaced.

## Requirements {#requirements}

### Reservation and release

<a id="slotops-r1"></a>**SLOTOPS-R1. A slot reservation is atomic and bounded by the pod's slot count.** The gateway reserves a slot by one script run against the pod's slot set. When the session's member is already present, the script adds nothing and returns the occupancy. Otherwise, when the occupancy is below the pod's recorded slot count, `AgentPod.spec.slotCount`, it adds the member and returns the new occupancy, and when the occupancy has reached the slot count it adds nothing and reports the slot unavailable. Two concurrent reservations cannot both observe the same free slot. The gateway compares against the recorded slot count whatever the pool's current [`sessionPolicy.maxConcurrentSessionsPerPod`](session-isolation.md#setting-sessionpolicy-maxconcurrentsessionsperpod) is. A leaked slot and a dead slot each hold one member of the set. <!-- src: §5.2 "Slot assignment atomicity (`maxConcurrentSessions > 1`)." (first sentences); SIR-16; SI-4 "What follows." -->

<a id="slotops-r2"></a>**SLOTOPS-R2. A session fills a held pod before the gateway claims another.** To place a session, the gateway first tries a slot claim on the pods of the session's claim set that its tenant already holds and that have a free slot, then claims an idle pod ([CLAIM-R1](../runtimes-and-pools/pod-claims.md#claim-r1)) and reserves its first slot. A reservation that finds the pod full falls through to the next pod. The first slot reservation on a pod stamps the tenant pin ([CLAIM-R6](../runtimes-and-pools/pod-claims.md#claim-r6)). When every held pod is full and no idle pod can be claimed, the request follows the pool's exhaustion rule with the reason `concurrent_slots_exhausted` ([CLAIM-R12](../runtimes-and-pools/pod-claims.md#claim-r12)). An acquisition that has already chosen its pod, such as a resume onto a replacement pod, reserves a slot on that pod alone. The gateway counts each reservation refused because the pod was full in [`lenny_slot_assignment_conflict_total`](#metric-lenny-slot-assignment-conflict-total). <!-- src: §5.2 "Slot assignment atomicity" (last sentences); §6.2 "Concurrent pod lifecycle" bullet "Partial occupancy"; §7.1 resume onto a replacement pod --> <!-- unresolved: TDR "Reused pods" notes that the slot claim's first pass and the reserved rebind run no covered-store or tenant-pin admission; no design states whether they should. -->

<a id="slotops-r3"></a>**SLOTOPS-R3. A release returns a reservation once.** The gateway releases a slot by one script run keyed by the session identifier. It removes the session's member and returns whether it removed the member and the remaining occupancy. A release whose cleanup was not acknowledged clean removes the session's member, if present, and adds a leaked member for the session, so a leak reported after a clean release of the same session is still counted, and a second leaked release changes nothing. A clean release that finds no member for the session changes nothing. A release that finds the pod's slot set not rehydrated first rehydrates it (SLOTOPS-R6), adding its own session's member, and then removes that member. The occupancy-zero handling of [CLAIM-R3](../runtimes-and-pools/pod-claims.md#claim-r3) runs only on a release that removed the session's member and left the set empty. <!-- src: §5.2 "Slot assignment atomicity" (release sentences) -->

Every path that releases the same reservation can run, and while Redis keeps the set, the reservation is returned once.

<a id="slotops-r4"></a>**SLOTOPS-R4. The gateway leader releases stale members.** A session member whose session has been in a terminal state for longer than [`claimOrphanTimeout`](../runtimes-and-pools/pod-claims.md#setting-claimorphantimeout) is stale. The gateway leader, the replica that holds the `lenny-gateway-leader` Lease, periodically releases the stale members of the pods a session holds, at a rate it limits. For each stale member it sends the pod's adapter an unconditional `Shutdown` for the session, and it removes the member only when that `Shutdown` reports a clean exit, the pod's slot set is rehydrated, and the set holds another member, so the leader never runs the occupancy-zero handling. The leader never removes a leaked member or a dead member. A stale member it does not remove stays counted and is retried. <!-- src: §5.2 "Slot assignment atomicity" (stale member sentences); §7.1 "Post-terminal window:" rule 2 -->

<a id="slotops-r5"></a>**SLOTOPS-R5. Postgres gates slot capacity during a Redis outage.** While Redis is unavailable, the gateway reserves a slot by reading the sessions that hold the pod from Postgres under a per-pod advisory lock and comparing their count with the pod's slot count. It fails closed after the bounded outage window of the Redis failure posture ([Redis](../../components/external/redis.md)). When the outage ends, the gateway marks every pod a session holds as not rehydrated, so the next reservation or release on it rehydrates the set. <!-- src: §5.2 "Slot assignment atomicity" ("During a Redis outage ..."); §5.2 "Post-recovery rehydration atomicity." (last sentence); §12.4 -->

<a id="slotops-r6"></a>**SLOTOPS-R6. A reservation on a pod whose set is lost waits for its rehydration.** Each pod's slot set carries a rehydrated flag with no expiry. A Redis restart clears it, a whole-pod reset after a coordinator loss deletes it, and the end of a Redis outage deletes it (SLOTOPS-R5). A reservation on a pod whose flag is absent does not reserve. The gateway takes the pod's rehydration lock, reads the identifiers of the sessions that hold the pod from Postgres, adds them to the set while keeping the members it already holds, sets the flag, and retries the reservation. A concurrent reservation on the same pod waits for the lock for up to [`slotRehydrationTimeoutMs`](#setting-slotrehydrationtimeoutms) and then retries. Rehydration blocks only reservations on that pod. The gateway counts each rehydration in [`lenny_slot_rehydration_total`](#metric-lenny-slot-rehydration-total). <!-- src: §5.2 "Post-recovery rehydration atomicity." --> <!-- unresolved: rehydration reads the holding sessions only, so leaked and dead members are not rebuilt; no design addresses the gap, which the current text also has. -->

Two reservations that both observed an empty set after a restart would otherwise both succeed before the set is rebuilt.

### Slot failure

<a id="slotops-r7"></a>**SLOTOPS-R7. A failed slot is retried in a fresh slot.** Slots fail independently: a slot's failure ends no other slot's session. When a slot's session fails, the adapter reports it and counts it in [`lenny_slot_failure_total`](#metric-lenny-slot-failure-total), and the gateway retries the session up to [`sessionPolicy.slotRetries`](#setting-sessionpolicy-slotretries) times. Each retry takes a new slot on the same pod when one is free, or on another pod of the claim set otherwise, and starts from a freshly materialized workspace that inherits nothing from the failed slot. A failure for `oom`, `workspace_validation`, or `policy_rejection` is not retried. When no retry is attempted or the retries are exhausted, the client receives an error whose `category` is the failure reason, whose `retryable` is `false`, and whose `sessionId` names the session. A pod failure fails every active slot at once, and each session retries independently on other pods. A slot whose session's attach stream fails after the session started follows the session's own retry policy ([Retry and resume](retry-and-resume.md)) and still counts toward the pod's replacement trigger ([APOD-R11](../runtimes-and-pools/agent-pod-lifecycle.md#apod-r11)). <!-- src: §5.2 "Slot failure and cleanup" bullet "Failure isolation:"; §5.2 "Slot retry policy (`maxConcurrentSessions > 1`)."; §6.2 "Concurrent pod lifecycle" bullet "Pod failure during active slots." -->

## Settings {#settings}

<a id="setting-sessionpolicy-slotretries"></a>

```lenny-def
kind: setting
name: sessionPolicy.slotRetries
type: integer
default: 1
range: ">= 0; 0 disables retries; a tenant-derived runtime may only lower it"
scopes: [pool]
read-by: [gateway session coordinator]
summary: Retries of a failed slot's session, each in a fresh slot (SLOTOPS-R7).
```

<a id="setting-slotrehydrationtimeoutms"></a>

```lenny-def
kind: setting
name: slotRehydrationTimeoutMs
type: integer (milliseconds)
default: 2000
scopes: [platform]
read-by: [GWCLAIM]
summary: How long a reservation waits for another replica's rehydration of the same pod (SLOTOPS-R6).
```

## Metrics and alerts {#metrics-and-alerts}

<a id="metric-lenny-slot-assignment-conflict-total"></a>

```lenny-def
kind: metric
name: lenny_slot_assignment_conflict_total
type: counter
labels: [pool]
emitter: GWCLAIM
summary: Slot reservations refused because the pod was full (SLOTOPS-R2).
```

<a id="metric-lenny-slot-rehydration-total"></a>

```lenny-def
kind: metric
name: lenny_slot_rehydration_total
type: counter
labels: [pod, pool]
emitter: GWCLAIM
summary: Rehydrations of a pod's slot set (SLOTOPS-R6).
```

<a id="metric-lenny-slot-failure-total"></a>

```lenny-def
kind: metric
name: lenny_slot_failure_total
type: counter
labels: [error_type]
emitter: ADAPTER
summary: Slot sessions that failed (SLOTOPS-R7).
```

<a id="metric-lenny-slot-pod-replacement-total"></a>

```lenny-def
kind: metric
name: lenny_slot_pod_replacement_total
type: counter
labels: [pool]
emitter: GW
summary: Shared pods drained because their failed, leaked, and dead slots reached the replacement trigger (APOD-R11).
```

## Failure behavior {#failure}

- **Redis restart.** Slot sets and their flags are lost. The first reservation or release on each pod rehydrates its set from Postgres (SLOTOPS-R6).
- **Redis outage.** Postgres gates capacity under a per-pod lock until the outage window ends (SLOTOPS-R5).
- **A session's release is lost.** Its member stays counted until the gateway leader releases it as stale (SLOTOPS-R4).

## Limits {#limits}

- Rehydration rebuilds the members of holding sessions only.
- The slot set does not queue requests for a pod; a request that finds no room moves to another pod or to the claim queue.

## Related capabilities {#related}

- [Session isolation](session-isolation.md) defines slots and what they share.
- [Pod claims](../runtimes-and-pools/pod-claims.md) acquires the pods whose slots this page fills.
- [Agent pod lifecycle](../runtimes-and-pools/agent-pod-lifecycle.md) replaces a pod whose slots fail.
