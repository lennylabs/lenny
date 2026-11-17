# Successor stubs for the current specification

Status: spike output, 2026-11-16. The spike may not write under `spec/`, so this file simulates the edit. Each entry gives the heading that stays in `spec/` and the successor text that replaces the migrated paragraphs under it, as `spec-citations.md` requires of a section that gives up content. Paragraphs the ledger marks "Not migrated in this spike" stay under the heading, after the stub, until their own areas migrate. Paragraphs marked "Moved outside the specification" or "Removed by a design" leave with this edit.

Links in a stub point at the new pages by identifier. In `spec/` they would be written relative to `spec/`, as `../spec-next/...` during the migration and as the final path once the tree replaces `spec/`.

## `spec/04_system-components.md`

### 4.6 Pod Lifecycle Controllers

> The pod lifecycle is specified by [Warm pools](capabilities/runtimes-and-pools/warm-pools.md), [Agent pod lifecycle](capabilities/runtimes-and-pools/agent-pod-lifecycle.md), and [Pod claims](capabilities/runtimes-and-pools/pod-claims.md), and its resources by [`PodSet`](interfaces/kubernetes/podset.md), [`AgentPod`](interfaces/kubernetes/agentpod.md), and [`PodClaim`](interfaces/kubernetes/podclaim.md).

### 4.6.1 Warm Pool Controller (Pod Lifecycle)

> The claim mechanism, the reserved hold, the claim queue, the Postgres fallback, pool exhaustion, and orphaned claim detection are now [CLAIM-R1](capabilities/runtimes-and-pools/pod-claims.md#claim-r1) to [CLAIM-R16](capabilities/runtimes-and-pools/pod-claims.md#claim-r16); the occupancy projection, the finalizer, disruption protection, and host-node schedulability are [APOD-R4](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r4) to [APOD-R7](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7), [APOD-R12](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r12) to [APOD-R16](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16); scale-to-zero, the fill grace period, and the warm-pod disruption budget are [POOL-R13](capabilities/runtimes-and-pools/warm-pools.md#pool-r13), [POOL-R16](capabilities/runtimes-and-pools/warm-pools.md#pool-r16), and [POOL-R19](capabilities/runtimes-and-pools/warm-pools.md#pool-r19); and the resource kinds are [`crd-podclaim`](interfaces/kubernetes/podclaim.md#crd-podclaim), [`crd-agentpod`](interfaces/kubernetes/agentpod.md#crd-agentpod), and [`crd-podset`](interfaces/kubernetes/podset.md#crd-podset).

Identifiers that replace the bold labels of this section:

| Bold label | Successor identifiers |
| :-- | :-- |
| **Pod claim mechanism (per-pod occupancy claim):** | CLAIM-R1, CLAIM-R2, CLAIM-R3 |
| **Occupancy projection (claim-driven `Sandbox.status.phase`):** | APOD-R4, APOD-R5, APOD-R8, APOD-R12 |
| **Reserved hold (claim retention across same-tenant sessions):** | CLAIM-R10 |
| **`SandboxClaim` admission webhook (double-claim prevention):** | CLAIM-R11, `error-podclaim-concurrent`, `error-podclaim-quarantined` |
| **Why optimistic locking provides sufficient fencing:** | CLAIM-R9 |
| **Controller failover and warm pool sizing:** | CLAIM-R12, POOL-R10 |
| **Fallback claim path via Postgres:** and **Fallback preconditions (mirror freshness and admission reachability).** | CLAIM-R13 |
| **Pool exhaustion behavior (`onPoolExhausted`):** | CLAIM-R12, `setting-sessionpolicy-onpoolexhausted`, `error-warm-pool-exhausted` |
| **WarmPoolController mirror reconciliation on recovery.** | CLAIM-R14 |
| **Orphaned `SandboxClaim` detection:** | CLAIM-R15, `metric-lenny-orphaned-claims-total`, `alert-podclaimorphanratehigh` |
| **Disruption protection for agent pods:** | APOD-R15, POOL-R16 |
| **Sandbox finalizers:** | APOD-R16 |
| **Host-node schedulability labeling (`lenny.dev/host-schedulable`):** | APOD-R7, `label-lenny-dev-host-schedulable` |
| **Cold-start pool fill:** and **Re-activation grace period:** | POOL-R19 |
| **Idle cost visibility and scale-to-zero:** | POOL-R13 |
| **CRD validation:** | KPSET Validation |
| **ADR-007: `SandboxClaim` optimistic-locking and failover fencing verification** | `TESTING.md` (the tests); CLAIM-R9 (the argument) |
| **Implementation:**, **Dependency pinning and upgrade policy:**, **Go/no-go criteria for the agent-sandbox dependency:**, **Fallback plan if agent-sandbox is abandoned or diverges:** | Removed (PLC-1) |
| **Internal abstraction layer:** | Package documentation of `pkg/podlifecycle`; GWCLAIM Internal parts |

### 4.6.3 CRD Field Ownership and Write Boundaries

> The writer of each field is stated on [`PodSet`](interfaces/kubernetes/podset.md#crd-podset), [`AgentPod`](interfaces/kubernetes/agentpod.md#crd-agentpod), and [`PodClaim`](interfaces/kubernetes/podclaim.md#crd-podclaim); the claim's binding states and its deletion rules are [CLAIM-R2](capabilities/runtimes-and-pools/pod-claims.md#claim-r2), [CLAIM-R3](capabilities/runtimes-and-pools/pod-claims.md#claim-r3), and [CLAIM-R9](capabilities/runtimes-and-pools/pod-claims.md#claim-r9); the gateway's grants for claiming are in [the pod claimer](components/gateway/pod-claimer.md); and the pool configuration validator and its denial handling are [POOL-R1](capabilities/runtimes-and-pools/warm-pools.md#pool-r1) and [POOL-R24](capabilities/runtimes-and-pools/warm-pools.md#pool-r24).

## `spec/05_runtime-registry-and-pool-model.md`

### 5.2 Pool Configuration and Execution Modes

> A pool and its pod sets are specified by [Warm pools](capabilities/runtimes-and-pools/warm-pools.md). Execution modes are removed (AR-2): every session binds to a slot of a claimed pod.

#### Execution Modes

> Tenant pinning and pinned idle inventory are [CLAIM-R5](capabilities/runtimes-and-pools/pod-claims.md#claim-r5) to [CLAIM-R8](capabilities/runtimes-and-pools/pod-claims.md#claim-r8) and [POOL-R14](capabilities/runtimes-and-pools/warm-pools.md#pool-r14); the recycle lifecycle, retirement, and `onScrubFailure` are [APOD-R7](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7) to [APOD-R10](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r10); shared pods and their acknowledgment are [SLOT-R1](capabilities/sessions/session-isolation.md#slot-r1) and [SLOT-R3](capabilities/sessions/session-isolation.md#slot-r3); and slot assignment, rehydration, and slot retry are [SLOTOPS-R1](capabilities/sessions/slot-assignment-and-failure.md#slotops-r1) to [SLOTOPS-R7](capabilities/sessions/slot-assignment-and-failure.md#slotops-r7).

| Bold label | Successor identifiers |
| :-- | :-- |
| **Tenant pinning:** | CLAIM-R5, CLAIM-R6, CLAIM-R7, `label-lenny-dev-tenant-id`, `error-tenant-label-immutable` |
| **Pinned idle inventory.** | CLAIM-R8, POOL-R14 |
| **Recycle lifecycle (`recycle.enabled: true`).** | APOD-R7, CLAIM-R10 |
| **`onScrubFailure` behaviors:** | APOD-R9, `annotation-lenny-dev-scrub-warning` |
| **Pod retirement policy (recycling pools).** | APOD-R8 |
| **Deployer acknowledgment.** | APOD-R10 |
| **Concurrent sessions (`maxConcurrentSessions > 1`).** | SLOT Terms (`shared-pod`) |
| **Deployer acknowledgment (concurrent sessions).** | SLOT-R1 |
| **Deployer acknowledgment (runtime process kept across sessions).** | Removed (SI D4); its used-pod rule is CLAIM-R5 |
| **Runtime-process lifetime (`recycle.runtimeProcess`).** | Removed (SI D4) |
| **Cross-tenant prohibition (concurrent sessions).** | SLOT-R3 |
| **Slot failure and cleanup (`maxConcurrentSessions > 1`).** | SLOTOPS-R7 (failure isolation only) |
| **Slot assignment atomicity (`maxConcurrentSessions > 1`).** | SLOTOPS-R1 to SLOTOPS-R5, CLAIM-R12, CLAIM-R16 |
| **Post-recovery rehydration atomicity.** | SLOTOPS-R6 |
| **Slot retry policy (`maxConcurrentSessions > 1`).** | SLOTOPS-R7, APOD-R11 |
| **`onPoolExhausted`** | CLAIM-R12 |
| **`service`**, **Session contract (service mode).**, **Tenant isolation (service mode).**, the service limitations, **T4 cross-tenant reuse prohibition.**, the `allowCrossTenantReuse` paragraph | Removed (AR-2; PM Removals; TSS-9) |

#### Execution Mode Scaling Implications

> The scaling formula with its mode factors is [POOL-R10](capabilities/runtimes-and-pools/warm-pools.md#pool-r10).

#### Pool Taxonomy

> Pool taxonomy guidance moved to the operator guide.

#### Bootstrap Behavior — Pool Warming at First Deployment

> Warm-up visibility is [POOL-R18](capabilities/runtimes-and-pools/warm-pools.md#pool-r18), and topology spread is [POOL-R17](capabilities/runtimes-and-pools/warm-pools.md#pool-r17).

## `spec/06_warm-pod-model.md`

### 6.2 Pod State Machine

> The pod phases and their state machine are specified by [Agent pod lifecycle](capabilities/runtimes-and-pools/agent-pod-lifecycle.md), from [APOD-R4](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r4) to [APOD-R7](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r7) and [APOD-R11](capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r11); the reserved hold is [CLAIM-R10](capabilities/runtimes-and-pools/pod-claims.md#claim-r10); and leaked slots are [SLOT-R4](capabilities/sessions/session-isolation.md#slot-r4).

| Bold label | Successor identifiers |
| :-- | :-- |
| **`reserved` hold semantics.** | CLAIM-R10, POOL-R15 |
| **`leaked` slot semantics.** | SLOT-R4, APOD-R11 |
| **Host-node schedulability precondition.** | APOD-R7 |
| **preConnect re-warm on scrub_warning.** | Removed (PL-8) |
| **Concurrent pod lifecycle (`maxConcurrentSessions > 1`).** | CLAIM-R3, APOD-R6, APOD-R11, SLOTOPS-R2, SLOTOPS-R7 |
| **State storage:** (the label table) | APOD-R6, `label-lenny-dev-state` |

The session sub-states, the timers, and the retry policies stay in §6.2 until the sessions area migrates.

## `spec/17_deployment-topology.md`

### 17.2 Namespace Layout

> The tenant label's transition rule, the pool and pod-set label rules, and the served label of item 5 are on [Labels and annotations](interfaces/kubernetes/labels-and-annotations.md#label-lenny-dev-tenant-id), and the claim guard of item 7 is [CLAIM-R11](capabilities/runtimes-and-pools/pod-claims.md#claim-r11).

The rest of §17.2 stays until the deployment and admission areas migrate.

## Singleton sections

| Section | Stub |
| :-- | :-- |
| §7.1 Normal Flow | The slot reservation on a pod chosen for a resume is [SLOTOPS-R2](capabilities/sessions/slot-assignment-and-failure.md#slotops-r2). |
| §12.4 Redis HA and Failure Modes | The slot set's role as the intra-pod capacity gate and its Postgres gate during an outage are [SLOTOPS-R1](capabilities/sessions/slot-assignment-and-failure.md#slotops-r1) and [SLOTOPS-R5](capabilities/sessions/slot-assignment-and-failure.md#slotops-r5). |
| §16.3 Distributed Tracing | The `session.claim_pod` span is [GWCLAIM-R3](components/gateway/pod-claimer.md#gwclaim-r3). |
