---
code: GWCLAIM
type: component
area: gateway
summary: >-
  The pod claimer is the part of the gateway's session core that acquires an agent pod and a slot
  for a session: it computes the claim set, admits candidates, creates and writes PodClaim objects,
  reserves slots, waits in the claim queue, runs the Postgres fallback, and holds and ends the
  reserved hold. This page states its responsibilities, interfaces, internal parts, its own
  requirements, its permissions, the registers it owns, and its failure behavior.
owns: [requirements, registers]
depends-on: [GW, CLAIM, SLOTOPS, SLOT, APOD, POOL, KPCLAIM, KAPOD, KPSET, KLABEL, OBSCONV]
---

# Gateway pod claimer

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| <a id="claim-request"></a>Claim request | The claimer's input: the pool, the pod hash of the session's revision, the session and tenant, the covered and withheld stores, whether the resolved pool reuses pods, and the claim options. |
| <a id="hold-holder"></a>Hold holder | The gateway replica that wrote a claim's `reserved` state and ends the hold at expiry. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Claim | The exclusive occupancy of one agent pod by the gateway ([CLAIM](../../capabilities/runtimes-and-pools/pod-claims.md#claim)). |
| Claim set | The pod sets a session may take a pod from ([CLAIM](../../capabilities/runtimes-and-pools/pod-claims.md#claim-set)). |
| Reserved hold | The period in which a recycled pod waits for its tenant's next session ([CLAIM](../../capabilities/runtimes-and-pools/pod-claims.md#reserved-hold)). |
| Slot claim | The placement of a session on a free slot of a pod the gateway already holds ([SLOTOPS](../../capabilities/sessions/slot-assignment-and-failure.md#slot-claim)). |

## Overview {#overview}

Every gateway replica runs the claimer, and the replicas do not coordinate with each other: the API server's name uniqueness, the claim guard, and the atomic slot reservation settle every race. Session creation, the resume driver, and the placement of a delegated child call the claimer. It returns a handle to a bound pod and a reserved slot, or a classified failure. The claimer belongs to the gateway's session core rather than to a seam ([Gateway](index.md)).

## Responsibilities {#responsibilities}

| Responsibility | Requirement it carries out |
| :-- | :-- |
| Compute the claim set and list candidates by the pool and pod-set labels | [CLAIM-R4](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r4) |
| Admit each candidate: phase, tenant pin, used pod, stores, owner charge, and quarantine label; stamp the drain request of a refused used pod | [CLAIM-R5](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r5) |
| Create the claim, write `bound`, and retry another candidate on a conflict or a refusal | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1), [CLAIM-R9](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r9) |
| Stamp the tenant pin and the served label at the first slot reservation | [CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) |
| End an acquisition on a failed pin read without a binding | [CLAIM-R7](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r7) |
| Write `recycling` and `reserved`, rebind within the hold, and end the hold | [CLAIM-R10](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r10) |
| Delete the claim when a release ends the occupancy without reuse | [CLAIM-R2](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r2), [CLAIM-R3](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r3) |
| Run the claim queue and apply the pool's exhaustion setting | [CLAIM-R12](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r12) |
| Run the Postgres fallback under its preconditions | [CLAIM-R13](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r13) |
| Write the retire-on-release mark at bind | [CLAIM-R17](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r17) |
| Reserve and release slots, place a session on a held pod first, and rehydrate slot sets | [SLOTOPS-R1](../../capabilities/sessions/slot-assignment-and-failure.md#slotops-r1), [SLOTOPS-R2](../../capabilities/sessions/slot-assignment-and-failure.md#slotops-r2), [SLOTOPS-R3](../../capabilities/sessions/slot-assignment-and-failure.md#slotops-r3), [SLOTOPS-R6](../../capabilities/sessions/slot-assignment-and-failure.md#slotops-r6) |
| Stamp the scrub warning annotation under the warn policy | [APOD-R9](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r9) |
| Place no new session on a pod past its uptime limit | [APOD-R8](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r8) |

The recycle disposition that chooses between `reserved` and `released` belongs to the gateway's release and recycle work ([APOD-R7](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7)). The claimer writes the state that disposition chooses.

## Interfaces served and consumed {#interfaces}

| Interface | Direction | What the claimer does |
| :-- | :-- | :-- |
| [`PodSet`](../../interfaces/kubernetes/podset.md#podset-list) | Consumes `kubernetes#podset.list` | Finds the pod sets of the session's pod hash. |
| [`AgentPod`](../../interfaces/kubernetes/agentpod.md#agentpod-list) | Consumes `kubernetes#agentpod.get` and `kubernetes#agentpod.list` | Reads candidates' phases and addresses. |
| [`PodClaim`](../../interfaces/kubernetes/podclaim.md#crd-podclaim) | Consumes create, get, status patch, and delete with preconditions | Runs the claim lifecycle. |
| Pods in the agent namespace | Consumes get and a patch of labels and annotations | Reads the tenant pin and the host-schedulable label, and stamps the keys of [Labels and annotations](../../interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-tenant-id). |
| Pod state mirror in Postgres | Consumes row reads and row locks | Runs the Postgres fallback and computes the mirror lag. |
| Session store in Postgres | Consumes the holding sessions of a pod | Rehydrates slot sets and gates capacity during a Redis outage. |
| Kubernetes API `/readyz` | Consumes | Probes reachability before the Postgres fallback. |

## Internal parts {#internal-parts}

- **Pod lifecycle manager.** The gateway-facing Go interface through which session code claims, releases, and drains pods, and reads pool status. Its default implementation acts on the `lenny.dev` kinds. The signatures are in the package documentation of `pkg/podlifecycle`. <!-- judgment: §4.6.1 "Internal abstraction layer:" lists the signatures; TAXONOMY sends Go signatures of internal types to package documentation. -->
- **Claimer.** The idle scan, the candidate admission, and the reserved-hold rebind (`pkg/gateway/core/podclaimer/claimer.go`).
- **Tenant pin reader.** The read of a candidate's pin and its used-pod admission (`tenantpin.go`).
- **Slot claimer.** The slot claim on a held pod, the slot reservation on a chosen pod, and the slot release (`slotclaimer.go`).
- **Binding-state writer.** Every status write and precondition-guarded deletion of a claim (`bindingstate.go`).
- **Fallback claimer.** The Postgres fallback (`pkg/gateway/podlifecycle/podsession`).
- **Claim queue.** The per-pool wait of GWCLAIM-R1.

## Requirements of its own mechanisms {#requirements}

<a id="gwclaim-r1"></a>**GWCLAIM-R1. Each replica keeps its own claim queue.** Each gateway replica keeps one in-memory first-in, first-out claim queue per pool. A request waits in the queue of the replica that received it, and the queues of different replicas are not ordered against each other. <!-- judgment: the specification calls it "the per-pool FIFO" without saying whether it is shared; the tree keeps one per replica. -->

<a id="gwclaim-r2"></a>**GWCLAIM-R2. The claimer dials only a published address.** The claimer returns a pod handle only when the pod's `AgentPod.status.podIP` is set, and it does not dial a pod whose address is empty. <!-- src: PLC-2 (`status.podIP` kept); the tree's handle construction -->

<a id="gwclaim-r3"></a>**GWCLAIM-R3. Each acquisition is one trace span.** The claimer records each acquisition as the span `session.claim_pod` in the session's trace, with the pool as an attribute, and records the failure of an acquisition that fails ([Observability conventions](../../cross-cutting/observability-conventions.md)). <!-- judgment: §16.3's span table names the Controller as the component of `session.claim_pod`; the claim runs in the gateway (CLAIM overview), so the span is the claimer's. -->

<a id="gwclaim-r4"></a>**GWCLAIM-R4. The claimer writes claim status only by patch.** The claimer writes a claim's status only by a patch of the status subresource that carries the `resourceVersion` it read, and never by a full update. <!-- src: §4.6.3 "Gateway ServiceAccount RBAC grants:" (`patch` rather than `update` on the status subresource) -->

## Registers {#registers}

<a id="register-slot-set"></a>

```lenny-def
kind: register
name: slot set
store: Redis
keys: ["lenny:pod:{pod_id}:active_slots", "lenny:pod:{pod_id}:rehydrated", "lenny:pod:{pod_id}:rehydrating"]
members: ["<sessionId>", "leaked:<sessionId>", "dead:<n>"]
writers: [GWCLAIM, gateway session coordinator, gateway leader]
readers: [GWCLAIM]
summary: A pod's slot occupancy, its rehydrated flag with no expiry, and the short-lived rehydration lock (SLOTOPS-R1, SLOTOPS-R6).
```
<!-- judgment: TAXONOMY assigns a register that one component both writes and reads to that component; the slot set is written by several parts of the gateway, which is one component. -->

## Deployment {#deployment}

The claimer runs in every gateway replica and has no deployment properties of its own ([Gateway](index.md)). It runs under the gateway's ServiceAccount `lenny-gateway`, which needs for this part:

- `create`, `get`, `list`, `watch`, and `delete` on `podclaims`, and `get` and `patch` on `podclaims/status`;
- `get`, `list`, and `watch` on `agentpods` and on `podsets`;
- `get`, `list`, `watch`, and `patch` on pods in the agent namespace.

The gateway holds no write on `agentpods`, no `agentpods/status`, no `delete` on pods, and no verb on nodes. <!-- src: §4.6.3 "Gateway ServiceAccount RBAC grants:"; PLC "Validation owed" (get, list, and watch on podsets) --> <!-- unresolved: the gateway's RBAC as a whole belongs on the gateway index; TAXONOMY version 2 does not say whether each part lists its own grants there or here. -->

## Settings read {#settings-read}

This list is generated from the `read-by` field of each setting.

- [`podClaimQueueTimeout`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-podclaimqueuetimeout)
- [`podClaimFallbackMaxMirrorLagSeconds`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-podclaimfallbackmaxmirrorlagseconds)
- [`gateway.claimHoldTTLSeconds`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-gateway-claimholdttlseconds)
- [`claimOrphanTimeout`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-claimorphantimeout)
- [`sessionPolicy.onPoolExhausted`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-sessionpolicy-onpoolexhausted)
- [`sessionPolicy.maxQueueWaitSeconds`](../../capabilities/runtimes-and-pools/pod-claims.md#setting-sessionpolicy-maxqueuewaitseconds)
- [`sessionPolicy.maxConcurrentSessionsPerPod`](../../capabilities/sessions/session-isolation.md#setting-sessionpolicy-maxconcurrentsessionsperpod)
- [`sessionPolicy.recycle.enabled`](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-enabled)
- [`sessionPolicy.recycle.maxPodUptimeSeconds`](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-maxpoduptimeseconds)
- [`slotRehydrationTimeoutMs`](../../capabilities/sessions/slot-assignment-and-failure.md#setting-slotrehydrationtimeoutms)

## Failure and recovery {#failure-and-recovery}

- **Replica crash after a claim's creation.** The claim has no binding state, and the WarmPoolController reclaims it ([CLAIM-R15](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r15)).
- **Replica crash while it is a hold holder.** Any replica may rebind the pod, and the WarmPoolController's reserved predicate ends the hold if none does.
- **API server watch degraded.** The candidate list goes stale, and the claimer runs the Postgres fallback when its preconditions hold.
- **Postgres down.** The fallback cannot run, and the pin read on the fallback path fails, so the claimer returns the retryable claim failure ([CLAIM-R7](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r7)).
- **Redis down.** Slot reservations run against Postgres ([SLOTOPS-R5](../../capabilities/sessions/slot-assignment-and-failure.md#slotops-r5)).

## Metrics and alerts {#metrics}

The claimer defines no metric of its own. This list of the metrics it emits is generated from the `emitter` field of each metric.

- [`lenny_pod_claim_duration_seconds`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-duration-seconds)
- [`lenny_pod_claim_queue_depth`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-queue-depth)
- [`lenny_pod_claim_queue_wait_seconds`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-queue-wait-seconds)
- [`lenny_pod_claim_conflict_total`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-conflict-total)
- [`lenny_pod_claim_timeout_total`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-timeout-total)
- [`lenny_pod_claim_fallback_total`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-fallback-total)
- [`lenny_pod_claim_fallback_skipped_total`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-pod-claim-fallback-skipped-total)
- [`lenny_agent_pod_state_mirror_lag_seconds`](../../capabilities/runtimes-and-pools/pod-claims.md#metric-lenny-agent-pod-state-mirror-lag-seconds)
- [`lenny_slot_assignment_conflict_total`](../../capabilities/sessions/slot-assignment-and-failure.md#metric-lenny-slot-assignment-conflict-total)
- [`lenny_slot_rehydration_total`](../../capabilities/sessions/slot-assignment-and-failure.md#metric-lenny-slot-rehydration-total)
