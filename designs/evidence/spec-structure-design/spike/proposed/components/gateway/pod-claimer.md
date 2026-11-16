---
code: GWCLAIM
type: component
summary: >-
  The pod claimer is the gateway subsystem that acquires an agent pod for a session: it computes the
  claim set, scans for a candidate, creates and patches the PodClaim, waits in the claim queue, runs
  the Postgres fallback, and holds and expires the reserved hold. This page states its
  responsibilities, interfaces, internal parts, permissions, failure behavior, and metrics.
owns: [metrics]
depends-on: [CLAIM, POOL, APOD, SLOT, GW, KPCLAIM, KAPOD, KPSET, KLABEL]
---

# Gateway pod claimer

The pod claimer is the gateway subsystem that acquires an agent pod for a session: it computes the claim set, scans for a candidate, creates and patches the `PodClaim`, waits in the claim queue, runs the Postgres fallback, and holds and expires the reserved hold. This page states its responsibilities, interfaces, internal parts, permissions, failure behavior, and metrics.

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| `ClaimRequest` | The claimer's input: the pool, the pod hash of the session's revision, the session and tenant, the covered and withheld stores, and the claim options. |
| Hold holder | The gateway replica that patched a claim to `reserved` and expires it. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Claim | The exclusive occupancy of one agent pod by the gateway ([Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#claim)). |
| Claim set | The pod sets a session may take a pod from ([Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#claim-set)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#reserved-hold)). |
| Slot claim | The placement of a session on a free slot of a pod the gateway already holds ([Slots](../../capabilities/sessions/slot-assignment-and-failure.md#slot-claim)). |

## Overview {#overview}

Every gateway replica runs the claimer, and the replicas do not coordinate with each other: the API server's name uniqueness and the claim guard settle every race. The claimer is called by session creation, by the resume driver, and by delegated-child placement. It returns a handle to a bound pod or a classified failure.

## Responsibilities {#responsibilities}

| Responsibility | Requirement it carries out |
| :-- | :-- |
| Create the claim, write `bound`, and retry another candidate on a conflict or a refusal | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1), [CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8) |
| Compute the claim set and list candidates by the pool and pod-set labels | [CLAIM-R4](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r4), [POOL-R6](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r6) |
| Check each candidate's phase, tenant pin, stores, and quarantine label | [CLAIM-R5](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r5), [CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) |
| Stamp the tenant pin at first assignment | [CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) |
| Patch the claim through `recycling` and `reserved`, rebind within the hold, and expire the hold | [CLAIM-R9](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r9) |
| Delete the claim when a release ends the occupancy without reuse | [CLAIM-R2](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r2), [CLAIM-R3](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r3) |
| Run the claim queue and apply `onPoolExhausted` | [CLAIM-R12](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r12) |
| Run the Postgres fallback under its preconditions | [CLAIM-R13](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r13) |
| Refuse dispatch to a pod whose address is empty | [`AgentPod.status.podIP`](../../interfaces/kubernetes/agentpod.md#status) |

The recycle disposition that decides between `reserved` and `released` is the release and recycle coordinator's ([Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7)); the claimer writes the state that disposition chooses.

## Interfaces served and consumed {#interfaces}

| Interface | Direction | What the claimer does |
| :-- | :-- | :-- |
| [`PodSet`](../../interfaces/kubernetes/podset.md) | Lists by the pool label | Finds the pod sets of the session's pod hash. |
| [`AgentPod`](../../interfaces/kubernetes/agentpod.md) | Gets and lists by labels | Reads candidates' phases and addresses. |
| [`PodClaim`](../../interfaces/kubernetes/podclaim.md) | Creates, gets, patches status, deletes with preconditions | The claim lifecycle. |
| Pods in the agent namespace | Gets, patches labels and annotations | Reads the tenant pin and the host-schedulable label; stamps the tenant pin and the drain request. |
| Pod state mirror in Postgres | Reads and locks rows | The Postgres fallback and the mirror lag gauge. |
| Kubernetes API `/readyz` | Calls | The reachability probe of the fallback. |

## Internal parts {#internal-parts}

- **Gateway-facing interface.** The Go interface `PodLifecycleManager` in `pkg/podlifecycle`, with the default implementation `CRDPodLifecycleManager`, declares `ClaimPod(ctx, poolName, sessionID, opts ClaimOpts) → (PodHandle, error)`, `ReleasePod`, `DrainPod(ctx, podHandle, checkpointFirst bool) → (DrainResult, error)`, and `GetPodStatus`. `ClaimOpts` carries an optional priority. `PodHandle` carries the adapter endpoint and the pod's certificate expiry. It embeds the read-only `PoolReader` (`ListPools`, `GetPoolStatus`), which the [WarmPoolController's `PoolManager`](../warm-pool-controller.md#internal-parts) shares. <!-- unresolved: `ClaimPod` takes a pool name and no pod hash in §4.6.1; PLCR-18 adds the pod hash to `ClaimRequest`, so the signature gains it. `PodHandle.warmMode` leaves with PL-8. The reserved `clusterID` and `PodDebugger` extension points are roadmap items and leave the specification. -->
- **Claimer** (`pkg/gateway/podlifecycle/podclaim/claimer.go`). The idle scan and the reserved-hold rebind.
- **Slot claimer** (`slotclaimer.go`). The slot claim on a held pod, over the same candidate list ([Slots](../../capabilities/sessions/slot-assignment-and-failure.md#slot-claim)).
- **Fallback claimer** (`pkg/gateway/podlifecycle/podsession/fallbackclaim.go`). The Postgres fallback.
- **Claim queue.** One in-memory first-in, first-out queue per pool on each replica. <!-- unresolved: the queue is per replica in the tree; the specification calls it "the per-pool FIFO" without saying whether it is shared across replicas. -->

## Kubernetes permissions {#permissions}

The claimer runs under the gateway's ServiceAccount `lenny-gateway`, which needs for this subsystem: `create`, `get`, and `delete` on `podclaims`; `get` and `patch` on `podclaims/status`; `get` and `list` on `agentpods` and `podsets`; and `get` and `patch` on pods in the agent namespace. The gateway holds no write on `agentpods`, no `agentpods/status`, and no node verb. <!-- unresolved: the gateway's RBAC as a whole belongs on the gateway page, which is a stub; whether each subsystem lists its own needs there or here is a taxonomy question. -->

## Failure and recovery {#failure-and-recovery}

- **Replica crash after a claim's creation.** The claim has no binding state, and the WarmPoolController reclaims it ([CLAIM-R15](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r15)).
- **Replica crash while holding a hold.** Any replica may rebind the pod; the WarmPoolController's reserved predicate expires it if none does.
- **API server watch degraded.** The candidate list goes stale; the claimer falls back to Postgres when the preconditions hold.
- **Postgres down.** The fallback cannot run, and the pin read on the fallback path fails; the claimer returns the retryable claim failure.

## Metrics {#metrics}

```lenny-def
kind: metric
name: lenny_pod_claim_duration_seconds
type: histogram
labels: [pool]
summary: Time from the session create's receipt to a bound pod.
```

```lenny-def
kind: metric
name: lenny_pod_claim_queue_depth
type: gauge
labels: [pool]
summary: Requests waiting in the claim queue.
```

```lenny-def
kind: metric
name: lenny_pod_claim_queue_wait_seconds
type: histogram
labels: [pool]
summary: Time a request spent in the claim queue.
```

```lenny-def
kind: metric
name: lenny_pod_claim_conflict_total
type: counter
labels: [pool]
summary: Claim creations that lost to another replica or were refused by the guard.
```

```lenny-def
kind: metric
name: lenny_pod_claim_timeout_total
type: counter
labels: [pool]
summary: Requests whose queue wait ended without a pod.
```

```lenny-def
kind: metric
name: lenny_pod_claim_fallback_total
type: counter
labels: []
summary: Activations of the Postgres fallback; a sustained rate points at a degraded API server path or an undersized pool.
```

```lenny-def
kind: metric
name: lenny_pod_claim_fallback_skipped_total
type: counter
labels: [reason]
summary: Fallbacks skipped; reason is mirror_stale or apiserver_unreachable.
```

```lenny-def
kind: metric
name: lenny_agent_pod_state_mirror_lag_seconds
type: gauge
labels: [pool]
summary: Seconds since the most recent update of the pool's rows in the pod state mirror (CLAIM-R14).
```

<!-- unresolved: `lenny_warmpool_failover_claims_served_fraction` is a ratio computed per failover event from claim outcomes and the lease renewal age; no source names the process that computes it, so it has no owner under v0. -->
