# Migration ledger

Status: spike output, 2026-11-16. This ledger records the fate of every paragraph of the current specification sections that the gateway pod claimer (`pkg/gateway/podlifecycle/podclaim`) cites, when those sections are migrated to the pages under `spec-next/` at the target state: the current specification as changed by the designs under `designs/`, read as implemented.

## Scope

The claimer's `// spec:` annotations cite §4.6.1 (36 sites), §5.2 (32), §4.6.3 (16), §6.2 (9), §17.2 (6), and singletons. Every paragraph of the five main sections has a row below, including the paragraphs that belong to other areas. A singleton section has one row for the paragraph the code cites, because the rest of it belongs to areas outside this spike. §4.6 is the container heading of §4.6.1 and §4.6.3.

A paragraph is a block of text between blank lines: a prose paragraph, a bullet group, a table, or a code block. A row is marked "yes" under "Judgment" when its placement needed semantic judgment, that is, reading the code or reconciling the specification against a design, rather than a mechanical move from the paragraph assignment in `designs/evidence/spec-structure-design/spike/proposed/ASSIGNMENT.md`. Row numbers count paragraphs within the nearest heading; a citation that needs more precision quotes the opening words in the third column.

Fates:

- **Migrated.** The content is on the named page under the named identifiers, unchanged in meaning.
- **Changed by a design.** The content is on the named page as the named design changes it.
- **Moved outside the specification.** The content goes to the destination TAXONOMY version 2 names.
- **Removed by a design.** The named design removes the content, and nothing replaces it.
- **Duplicate.** The content restates another paragraph, which the note names.
- **Not migrated in this spike.** The content belongs to the named page of another area. A row whose fate is "Migrated" or "Changed by a design" can still send part of its content to another area, and the target column says so.

## Totals

| Fate | Paragraphs | Words |
| :-- | --: | --: |
| Migrated | 58 | 5,001 |
| Changed by a design | 54 | 12,791 |
| Moved outside the specification | 20 | 1,005 |
| Duplicate | 4 | 638 |
| Removed by a design | 24 | 2,980 |
| Not migrated in this spike | 104 | 15,134 |
| Total | 264 | 37,549 |

Paragraphs that needed semantic judgment: 34.

"Not migrated in this spike" is the largest fate because §6.2 is mostly the session state machine and §17.2 is mostly admission and namespace layout, which belong to other areas.

## Rows

| Section | ¶ | Opening | Words | Fate | Target and identifiers | Design and decision | Judgment | Note |
| :-- | --: | :-- | --: | :-- | :-- | :-- | :-- | :-- |
| §4.6 Pod Lifecycle Controllers | 1 | "Pod lifecycle management is split into two controllers with …" | 11 | Not migrated in this spike | WPC, PSC (components index) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 1 | **Role:** | 9 | Not migrated in this spike | WPC |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 2 | **Responsibilities:** | 1 | Not migrated in this spike | WPC |  |  | Label of the list below. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 3 | "Maintain warm pods per pool (between minWarm and maxWarm) …" | 86 | Migrated | POOL-R9; APOD-R13; the rest to WPC responsibilities (not migrated) |  | yes | Split by item: warm count and replacement to POOL, idle drains to APOD, status and health to WPC. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 4 | **Implementation:** | 88 | Removed by a design | WPC (replacement text not migrated) | PLC-1 (D3, D4, D7) |  | Upstream dependency text removed; PLC-1's replacement sentence belongs to WPC. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 5 | **Internal abstraction layer:** | 25 | Moved outside the specification | Package documentation of pkg/podlifecycle; described in words in GWCLAIM Internal parts | PLC-4 (CRDPodLifecycleManager) | yes | TAXONOMY sends Go signatures to package documentation. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 6 | **`PoolReader`** | 6 | Moved outside the specification | Package documentation |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 7 | "ListPools() → ([]PoolStatus, error) — pool health and capacity …" | 20 | Moved outside the specification | Package documentation |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 8 | **`PodLifecycleManager`** | 4 | Moved outside the specification | Package documentation; GWCLAIM Internal parts |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 9 | "ClaimPod(ctx, poolName, sessionID, opts ClaimOpts) → (PodHandle, error) — …" | 72 | Moved outside the specification | Package documentation; GWCLAIM Internal parts | PLC (SnapshotPod removed) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 10 | **`PoolManager`** | 4 | Moved outside the specification | Package documentation (WPC) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 11 | "ReconcilePool(ctx, poolConfig) → error — ensure pool matches desired …" | 148 | Moved outside the specification | Package documentation (WPC) | PLC-4 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 12 | "Default implementations: AgentSandboxPodLifecycleManager and AgentSandboxPoolManager, which delegate to agent-sandbox …" | 10 | Moved outside the specification | Package documentation | PLC-4 (default implementation names) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 13 | "Future extension points (reserved, not implemented now): PodDebugger interface …" | 28 | Moved outside the specification | Program milestones (roadmap) for PodDebugger and clusterID | PLC (SnapshotPod and RestoreFromSnapshot removed) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 14 | "This indirection means a breaking upstream change or a …" | 30 | Removed by a design |  | PLC-1 | yes | Rationale for insulating from upstream; no upstream remains. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 15 | **CRD mapping from agent-sandbox:** | 4 | Changed by a design | K8S index (not migrated) | PLC-2 (label becomes Resource kinds) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 16 | **API group:** | 96 | Changed by a design | KPCLAIM crd-podclaim; KAPOD crd-agentpod; KPSET crd-podset; group and version to K8S (not migrated) | PLC-2 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 17 | Table: Agent-Sandbox CRD Replaces (old Lenny CRD) | 210 | Changed by a design | KPCLAIM, KAPOD, KPSET resource blocks; Runtime row to KRUNTIME (not migrated) | PLC-2, PLC-3 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 18 | **ADR-007: `SandboxClaim` optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite.** | 79 | Moved outside the specification | TESTING.md (the two tests); the fencing argument stays as CLAIM-R9 rationale | PLC-4 rename; REC-1 (ADR prefix dropped) | yes | REC-1 and PLC keep the paragraph in the specification; TAXONOMY v2 sends verification tests to TESTING.md. TAXONOMY applied. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 19 | **Concurrent-claim integration test.** | 65 | Moved outside the specification | TESTING.md | REC-1 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 20 | **Chaos test (leader-kill acceptance test).** | 105 | Moved outside the specification | TESTING.md | REC-1 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 21 | **Why optimistic locking provides sufficient fencing:** | 114 | Migrated | CLAIM-R9 (rationale paragraph) | REC-1 (ADR wording dropped) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 22 | **`SandboxClaim` admission webhook (double-claim prevention):** | 596 | Changed by a design | CLAIM-R11; KPCLAIM admission; CLAIM error-podclaim-concurrent, error-podclaim-quarantined, metric-lenny-podclaim-guard-rejections-total; webhook deployment and PodClaimGuardUnavailable to WEBHOOK (not migrated) | PLC-2 (lenny-podclaim-guard, agentPodRef); FE-1 (quarantine refusal) | yes | 596 words; the mechanism and the outcomes split across three pages. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 23 | **Pod claim mechanism (per-pod occupancy claim):** | 252 | Changed by a design | CLAIM-R1, CLAIM-R2, CLAIM-R3; SLOTOPS-R1 (intra-pod gate pointer) | PLC-2 (PodClaim, agentPodRef); PLCR-18 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 24 | **Occupancy projection (claim-driven `Sandbox.status.phase`):** | 97 | Changed by a design | APOD-R4; APOD Terms (pod-phase) | PL-8 (sdk_connecting removed); FE-1 (quarantined) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 25 | "idle for a pod in a warm-inventory phase with …" | 292 | Changed by a design | APOD-R5 | PL-8 (sdk_connecting and rewarmStartedAt rows removed) | yes | Each bullet rewritten without the pre-launched runtime qualifiers. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 26 | "Uptime drains derive from the pod CreationTimestamp against recycle.maxPodUptimeSeconds, …" | 24 | Migrated | APOD-R8 (uptime limit); APOD-R12 |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 27 | **Reserved hold (claim retention across same-tenant sessions):** | 340 | Changed by a design | CLAIM-R10; CLAIM-R15 (reserved predicate preconditions); APOD-R8 (vm-restart retire) | PL-8 (SDK-warm removed); SI D4 (runtime-process refusal path becomes the used-pod rule of CLAIM-R5) | yes |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 28 | **Leader election:** | 138 | Not migrated in this spike | WPC (leader election and the crash failover window) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 29 | **Controller crash during active scale-down drain — no additional fencing required.** | 52 | Migrated | APOD-R14 (in part); the rest to WPC (not migrated) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 30 | **Idle pods being scale-down drained have no checkpoint.** | 81 | Not migrated in this spike | WPC |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 31 | **Active-session checkpointing is fenced by `coordination_generation`.** | 95 | Not migrated in this spike | WPC; GWCOORD (coordination_generation fencing) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 32 | "The K8s Lease-based leader election guarantee (single active writer …" | 52 | Migrated | APOD-R14 (in part); the rest to WPC (not migrated) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 33 | **Scaling:** | 49 | Changed by a design | POOL-R13 (in part); the rest to PSC (not migrated) | PL-8 (preConnect sentence removed) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 34 | **Idle cost visibility and scale-to-zero:** | 165 | Migrated | POOL-R13; the idle pod-minutes metric and WarmPoolIdleCostHigh not migrated (POOL metrics outside this spike) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 35 | **CRD validation:** | 78 | Changed by a design | KPSET Validation | PLC-2; PL-9 (maxConcurrentSessionsPerPod); BR-1 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 36 | **Controller failover and warm pool sizing:** | 318 | Migrated | CLAIM-R12 (queue and the podClaimQueueTimeout margin); POOL-R10 (failover term); sizing examples to the operator guide |  | yes | Mixes a claim rule, a sizing input, and worked numbers. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 37 | **Fallback claim path via Postgres:** | 172 | Changed by a design | CLAIM-R13 | PLC-2 (pod_set column); PLCR-18 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 38 | **Fallback preconditions (mirror freshness and admission reachability).** | 323 | Changed by a design | CLAIM-R13; CLAIM metric-lenny-pod-claim-fallback-skipped-total | PLC-2 (lag of the claim set's rows) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 39 | **Pool exhaustion behavior (`onPoolExhausted`):** | 139 | Migrated | CLAIM-R12; CLAIM setting-sessionpolicy-onpoolexhausted, setting-sessionpolicy-maxqueuewaitseconds, error-warm-pool-exhausted |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 40 | **WarmPoolController mirror reconciliation on recovery.** | 162 | Changed by a design | CLAIM-R14 | PLC-2 (pod_set from spec.podSetRef) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 41 | **Simultaneous controller failover:** | 150 | Duplicate | Duplicate of §4.6.1 ¶36 (queue timeout margin); the controller pairing to WPC and PSC (not migrated) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 42 | **Controller leader election monitoring:** | 59 | Not migrated in this spike | OBSCONV (controller metrics and alerts) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 43 | **API server rate limiting:** | 20 | Not migrated in this spike | WPC (process settings) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 44 | **Pod creation bucket:** | 48 | Not migrated in this spike | WPC |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 45 | "Both buckets are configurable via controller flags (--create-qps, --create-burst, …" | 30 | Not migrated in this spike | WPC |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 46 | "During steady-state pool replenishment, pod creation is processed through …" | 104 | Not migrated in this spike | WPC |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 47 | **Cold-start pool fill:** | 95 | Migrated | POOL-R19; POOL setting-initialfillgraceperiod |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 48 | **Re-activation grace period:** | 104 | Changed by a design | POOL-R19 | RT-1 (variant pool example removed) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 49 | **etcd write pressure at scale:** | 23 | Not migrated in this spike | KUBE; tuning to the operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 50 | Table: Tier Concurrent sessions Estimated etcd writes/s | 76 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 51 | "The estimate assumes the per-pod claim profile: one SandboxClaim …" | 444 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 52 | **etcd operational tuning:** | 16 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 53 | **Compaction:** | 443 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 54 | **etcd tuning topology matrix.** | 30 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 55 | Table: Tuning area Managed K8s (EKS, GKE, | 375 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 56 | **`statusUpdateDeduplicationWindow` semantics.** | 128 | Not migrated in this spike | WPC (process setting --status-update-dedup-window) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 57 | **Summary:** | 62 | Not migrated in this spike | KUBE; operator guide |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 58 | "See Section 17.8 for controller tuning recommendations. …" | 7 | Not migrated in this spike | KUBE |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 59 | **etcd unavailability (degraded mode):** | 52 | Migrated | CLAIM Failure behavior (in part); the rest to KUBE (not migrated) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 60 | **Existing sessions continue unaffected.** | 140 | Migrated | CLAIM Failure behavior; POOL Failure behavior (in part); the rest to KUBE (not migrated) |  |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 61 | **Disruption protection for agent pods:** | 198 | Changed by a design | APOD-R15 | SI §4.6.1 row (every slot container) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 62 | "The warm pool controller can optionally create a PDB …" | 411 | Changed by a design | POOL-R16 | PLCR-13 (one budget per PodSet, none below minWarm 2) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 63 | **Sandbox finalizers:** | 151 | Migrated | APOD-R16; KLABEL finalizer-lenny-dev-session-cleanup | PLC-2 (rename) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 64 | **Orphaned `SandboxClaim` detection:** | 173 | Changed by a design | CLAIM-R15 | PLC-2 (rename) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 65 | **Live binding states (`bound` or `recycling`).** | 420 | Changed by a design | CLAIM-R15 | FE-1 (quarantined matches no predicate) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 66 | "The metric lenny_orphaned_claims_total (counter, labeled by pool) is incremented …" | 34 | Migrated | CLAIM metric-lenny-orphaned-claims-total, alert-podclaimorphanratehigh | PLC-2 (alert renamed) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 67 | **Host-node schedulability labeling (`lenny.dev/host-schedulable`):** | 347 | Changed by a design | APOD-R7; KLABEL label-lenny-dev-host-schedulable; the informer to WPC (not migrated) | PL-8 (the re-warm edge it qualified is removed) | yes | The precondition moves to the hold decision. |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 68 | **Dependency pinning and upgrade policy:** | 181 | Removed by a design |  | PLC-1 (D3, D4) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 69 | **Go/no-go criteria for the agent-sandbox dependency:** | 32 | Removed by a design |  | PLC-1 (D3, D4) |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 70 | **API stability.** | 120 | Removed by a design |  | PLC-1 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 71 | "If any criterion is not met at Phase 1 …" | 32 | Removed by a design |  | PLC-1 |  |  |
| §4.6.1 Warm Pool Controller (Pod Lifecycle) | 72 | **Fallback plan if agent-sandbox is abandoned or diverges:** | 121 | Removed by a design |  | PLC-1 |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 1 | "Both the WarmPoolController and PoolScalingController interact with the same …" | 26 | Migrated | Writer columns of KPSET, KAPOD, and KPCLAIM | PLC-2 (merge) |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 2 | Table: CRD Field / Subresource Owner Notes | 330 | Changed by a design | KPSET, KAPOD, KPCLAIM writer columns; CLAIM-R2; CLAIM-R3; APOD-R1 | PLC-2 (PodSet merge); PL-8 (sdkWarm fields removed); SI D4 (runtime-process deletion path removed) | yes | The claim row is 220 words of claim rules inside a table cell. |
| §4.6.3 CRD Field Ownership and Write Boundaries | 3 | **Server-Side Apply (SSA) enforcement:** | 77 | Not migrated in this spike | WPC, PSC (SSA field managers) |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 4 | **SSA conflict retry policy (crash recovery).** | 67 | Not migrated in this spike | WPC, PSC |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 5 | **Always re-read before re-applying.** | 321 | Not migrated in this spike | WPC, PSC |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 6 | **RBAC enforcement (coarse-grained backstop):** | 335 | Not migrated in this spike | WPC, PSC (deployment) |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 7 | **Gateway ServiceAccount RBAC grants:** | 216 | Changed by a design | GWCLAIM Deployment (the claimer's grants); the gateway's other grants to GW (not migrated) | PLC (get, list, watch on podsets); PM (allowCrossTenantReuse mention removed); PM-9 (lenny-agents-kata) | yes |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 8 | **`SandboxClaim.status.phase` enumeration (gateway-owned binding state):** | 220 | Changed by a design | CLAIM state machine; CLAIM-R9; KPCLAIM status | FE-1 (quarantined) |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 9 | "The claim status additionally carries the time of the …" | 167 | Changed by a design | CLAIM-R9; KPCLAIM status (bindingStateTransitionTime, holdExpiresAt) | PL-8 (rewarmStartedAt removed) |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 10 | **Validating webhook for Postgres-authoritative state (defense-in-depth):** | 32 | Migrated | POOL-R1; KPSET admission; webhook deployment to WEBHOOK (not migrated) |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 11 | **Semantic/budget rules (apply to ALL writes, including PoolScalingController SSA applies):** | 184 | Migrated | KPSET admission (both rule sets); the inequalities to CKPT (not migrated) |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 12 | "The webhook runs in Fail mode with a 5s …" | 107 | Not migrated in this spike | WEBHOOK |  |  |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 13 | **PoolScalingController admission-denial handling policy.** | 127 | Migrated | POOL-R24 | PLC-2 (keyed per PodSet) | yes |  |
| §4.6.3 CRD Field Ownership and Write Boundaries | 14 | **Log and count.** | 418 | Migrated | POOL-R24; the metric and PoolScalingAdmissionStuck not migrated |  |  |  |
| §5.2 Pool Configuration and Execution Modes | 1 | "This section covers: pool dimensions and configuration fields, the …" | 51 | Changed by a design | POOL Overview (restated) | AR-2, AR O7 (section renamed Pool Configuration) |  |  |
| §5.2 Pool Configuration and Execution Modes | 2 | "Each pool is a warmable deployment target for one …" | 13 | Migrated | POOL Terms (pool) |  |  |  |
| §5.2 Pool Configuration and Execution Modes | 3 | **Pool dimensions:** | 2 | Migrated | POOL Terms (pool) |  |  |  |
| §5.2 Pool Configuration and Execution Modes | 4 | "Runtime name Isolation profile (runc / gvisor / kata) …" | 40 | Changed by a design | POOL Terms (pool) | AR-2 (execution mode removed) |  |  |
| §5.2 › Execution Modes | 1 | "The session and service execution modes are both implemented …" | 78 | Removed by a design |  | AR-1, AR-2 |  |  |
| §5.2 › Execution Modes | 2 | Code block: executionMode: session \| service | 6 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 3 | "The mode names follow the unit of the client …" | 66 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 4 | **`session`** | 45 | Changed by a design | POOL model (no execution mode); CLAIM Overview | AR-2 (ARR-2) | yes | The session-mode definition becomes the only behavior. |
| §5.2 › Execution Modes | 5 | **Slot (session mode).** | 131 | Changed by a design | SLOT Terms (slot); what a slot owns to SLOT (not migrated) | SI-3, SI D1 (slot containers); PL-3 | yes |  |
| §5.2 › Execution Modes | 6 | Code block: sessionPolicy: # session mode only maxConcurrentSessions: | 240 | Changed by a design | SLOT, APOD, CLAIM, SLOTOPS setting blocks | PL-9 renames; SI D4 (runtimeProcess removed); PM (allowCrossTenantReuse removed) | yes | One YAML block feeds settings on four pages. |
| §5.2 › Execution Modes | 7 | "Common configurations of the block: …" | 5 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Execution Modes | 8 | Table: Configuration maxConcurrentSessions recycle.enabled Behavior | 122 | Moved outside the specification | Operator guide |  | yes | A table of common configurations is guidance. |
| §5.2 › Execution Modes | 9 | "Acknowledgments, tenant pinning, residualStateWarning, and the scaling factors are …" | 19 | Migrated | APOD-R10; SLOT-R1; CLAIM-R6 |  |  |  |
| §5.2 › Execution Modes | 10 | "recycle.acknowledgeBestEffortScrub: true is required when recycle.enabled: true; the workspace …" | 102 | Changed by a design | APOD-R10; SLOT-R1; CLAIM-R6; the preConnect bullet removed | PL-8; PL-9; SI D4 | yes |  |
| §5.2 › Execution Modes | 11 | **`maxClientIdleSeconds`** | 26 | Not migrated in this spike | TIMEOUT | SL L14 (dormancy) |  |  |
| §5.2 › Execution Modes | 12 | **`onPoolExhausted`** | 77 | Duplicate | Duplicate of §4.6.1 ¶39; migrated as CLAIM-R12 |  |  |  |
| §5.2 › Execution Modes | 13 | **Tenant pinning:** | 60 | Changed by a design | CLAIM-R6 | AR-2 (service clause removed) |  |  |
| §5.2 › Execution Modes | 14 | **Application layer (gateway):** | 207 | Changed by a design | CLAIM-R6; KLABEL label-lenny-dev-tenant-id; CLAIM error-tenant-label-immutable; webhook HA to WEBHOOK (not migrated) | TDR DR-5 and PM-2 (owner stamp at creation) | yes | Webhook name conflict resolved to lenny-label-immutability. |
| §5.2 › Execution Modes | 15 | "The pin persists across the recycle-to-idle edge for the …" | 238 | Changed by a design | CLAIM-R5; CLAIM-R6; CLAIM-R7 | SI D4 (drain under the runtime-process acknowledgment becomes the used-pod rule); PM (allowCrossTenantReuse removed) | yes |  |
| §5.2 › Execution Modes | 16 | **Pinned idle inventory.** | 44 | Migrated | CLAIM Terms (pinned-idle-pod); CLAIM-R8 |  |  |  |
| §5.2 › Execution Modes | 17 | "It does not count toward minWarm. status.warmCount and status.readyCount …" | 128 | Migrated | POOL-R14 |  |  |  |
| §5.2 › Execution Modes | 18 | "A recycled pod is therefore held for its pinned …" | 146 | Changed by a design | POOL-R14; CLAIM-R8; the future-work sentence to program milestones | SI D4 (acknowledgment drain path) | yes |  |
| §5.2 › Execution Modes | 19 | "recycle.allowCrossTenantReuse: true applies only to the sequential-reuse path (maxConcurrentSessions: …" | 149 | Removed by a design |  | PM Removals (allowCrossTenantReuse) |  |  |
| §5.2 › Execution Modes | 20 | **T4 cross-tenant reuse prohibition.** | 128 | Removed by a design |  | PM Removals; TSS-9 (workspaceTier); TSS I23 |  |  |
| §5.2 › Execution Modes | 21 | **Scrub model.** | 225 | Not migrated in this spike | SCRUB | SI-3 |  |  |
| §5.2 › Execution Modes | 22 | Table: Slot and cleanup How the cleanup | 363 | Not migrated in this spike | SCRUB; SLOT | SI-3 |  |  |
| §5.2 › Execution Modes | 23 | "The leaked column applies on a pod serving concurrent …" | 197 | Not migrated in this spike | SLOT | SI-4 |  |  |
| §5.2 › Execution Modes | 24 | **Slot-identifier reclaim hold.** | 624 | Not migrated in this spike | SLOT (reclaim hold) |  |  |  |
| §5.2 › Execution Modes | 25 | **Recycle lifecycle (`recycle.enabled: true`).** | 273 | Changed by a design | APOD-R7; CLAIM-R10; the scrub trigger detail to SCRUB (not migrated) | PL-8 (re-warm removed) |  |  |
| §5.2 › Execution Modes | 26 | **Recycling and integration levels.** | 117 | Not migrated in this spike | SCRUB |  |  |  |
| §5.2 › Execution Modes | 27 | **Runtime-process lifetime (`recycle.runtimeProcess`).** | 282 | Removed by a design |  | SI D4 (SI-3 What this retires); AR-5 (restartable) |  |  |
| §5.2 › Execution Modes | 28 | **Lenny scrub procedure.** | 335 | Not migrated in this spike | SCRUBPROC | SI-3; SI D4 |  |  |
| §5.2 › Execution Modes | 29 | **Step 0 (pre-cleanup, before `cleanupCommands`):** | 91 | Not migrated in this spike | SCRUBPROC | PL-3 (paths) |  |  |
| §5.2 › Execution Modes | 30 | **Steps 1-6 (post-cleanup, after `cleanupCommands` finish):** | 6 | Not migrated in this spike | SCRUBPROC |  |  |  |
| §5.2 › Execution Modes | 31 | "1. Kill all remaining user processes (kill -9 -1 …" | 220 | Not migrated in this spike | SCRUBPROC |  |  |  |
| §5.2 › Execution Modes | 32 | **Supervised scrub sequence.** | 199 | Not migrated in this spike | SCRUBPROC | SI-3 (per slot) |  |  |
| §5.2 › Execution Modes | 33 | Table: Row Success Error No reply within | 307 | Not migrated in this spike | SCRUBPROC |  |  |  |
| §5.2 › Execution Modes | 34 | "An exited report during any row does not change …" | 32 | Not migrated in this spike | SCRUBPROC |  |  |  |
| §5.2 › Execution Modes | 35 | **What the scrub reaches.** | 243 | Not migrated in this spike | SCRUB |  |  |  |
| §5.2 › Execution Modes | 36 | "The scrub is best-effort and is not a security …" | 155 | Not migrated in this spike | SCRUB (APOD-R10 links it) |  |  |  |
| §5.2 › Execution Modes | 37 | **Kata/microvm scrub variant.** | 62 | Migrated | APOD setting-sessionpolicy-recycle-scrubprofile; the steps to SCRUBPROC (not migrated) |  |  |  |
| §5.2 › Execution Modes | 38 | **Fresh-guest reprovision:** | 139 | Migrated | APOD-R8 (fresh-guest reprovision) |  |  |  |
| §5.2 › Execution Modes | 39 | "The scrub procedure is selected by recycle.scrubProfile: standard (the …" | 165 | Changed by a design | APOD setting-sessionpolicy-recycle-scrubprofile; APOD-R10 (in-place acknowledgment); residual state list to SCRUB (not migrated) | PM Removals (standard rejection clause removed) |  |  |
| §5.2 › Execution Modes | 40 | **`onScrubFailure` behaviors:** | 2 | Migrated | APOD-R9 |  |  |  |
| §5.2 › Execution Modes | 41 | **`warn`** | 255 | Changed by a design | APOD-R9; APOD-R8 (scrub failure limit); KLABEL annotation-lenny-dev-scrub-warning; scrub failure metrics to SCRUB (not migrated) | PL-8 (preConnect interaction removed) | yes | The annotation key comes from the tree. |
| §5.2 › Execution Modes | 42 | **Pod retirement policy (recycling pools).** | 24 | Migrated | APOD-R8 |  |  |  |
| §5.2 › Execution Modes | 43 | **Session count limit:** | 464 | Changed by a design | APOD-R8 | SI D4, D15 (runtime-not-live condition left unresolved) | yes |  |
| §5.2 › Execution Modes | 44 | "The condition is evaluated before the host-node schedulability retire …" | 75 | Changed by a design | APOD-R7; APOD-R8 | PL-8 (rewarmStartedAt removed) | yes |  |
| §5.2 › Execution Modes | 45 | "On a pool with maxConcurrentSessions: 1, a session that …" | 82 | Migrated | APOD-R8; APOD metric-lenny-gateway-pod-retirement-total, metric-lenny-controller-pod-retirement-total |  |  |  |
| §5.2 › Execution Modes | 46 | **Deployer acknowledgment.** | 18 | Migrated | APOD-R10 |  |  |  |
| §5.2 › Execution Modes | 47 | Code block: sessionPolicy: acknowledgeProcessLevelIsolation: true # required on | 98 | Changed by a design | APOD setting blocks | SI D4 (acknowledgeProcessLevelIsolation line removed) |  |  |
| §5.2 › Execution Modes | 48 | "If acknowledgeBestEffortScrub is absent or false on a pool …" | 120 | Migrated | APOD-R10 |  |  |  |
| §5.2 › Execution Modes | 49 | **Client visibility of weak-isolation reuse.** | 204 | Not migrated in this spike | SCREATE (sessionIsolationLevel) | PL-9; ARR-11; PM-4 |  |  |
| §5.2 › Execution Modes | 50 | **Concurrent sessions (`maxConcurrentSessions > 1`).** | 145 | Changed by a design | SLOT Terms (shared-pod); multiplexing and paths to RTPFS (not migrated) | PL-3; AR-2 | yes |  |
| §5.2 › Execution Modes | 51 | **Deployer acknowledgment (concurrent sessions).** | 201 | Changed by a design | SLOT-R1 | PL-9 (acknowledgeSharedPod); SI-10 (shared list) | yes | SI-10 and PL-9 word the list differently. |
| §5.2 › Execution Modes | 52 | **Deployer acknowledgment (runtime process kept across sessions).** | 584 | Removed by a design | The used-pod admission rule it carried is kept as CLAIM-R5 | SI D4 (SI-3 What this retires) | yes | 584 words; one rule survives because DR-5 and DR-9 rely on it. |
| §5.2 › Execution Modes | 53 | **Cross-tenant prohibition (concurrent sessions).** | 114 | Changed by a design | SLOT-R3 | PM Removals (validation and error string removed) |  |  |
| §5.2 › Execution Modes | 54 | **`service`** | 182 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 55 | "A service-mode slot is a different thing from a …" | 37 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 56 | Code block: executionMode: service maxConcurrent: 8 # per-pod | 16 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 57 | **Session contract (service mode).** | 124 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 58 | **Tenant isolation (service mode).** | 115 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 59 | **Service-mode limitations (v1).** | 221 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Modes | 60 | **Slot failure and cleanup (`maxConcurrentSessions > 1`).** | 27 | Migrated | SLOTOPS-R7 |  |  |  |
| §5.2 › Execution Modes | 61 | **Failure isolation:** | 1112 | Changed by a design | SLOTOPS-R7 (failure isolation); slot cleanup to SCRUB; checkpoint granularity to CKPT; resource contention to SLOT (not migrated) | SI-3 (release); SI-9 (per-slot cgroups); IC D20 (checkpoint order) | yes | 1,112 words in four bullets with four owners. |
| §5.2 › Execution Modes | 62 | **Slot assignment atomicity (`maxConcurrentSessions > 1`).** | 770 | Changed by a design | SLOTOPS-R1, SLOTOPS-R2, SLOTOPS-R3, SLOTOPS-R4, SLOTOPS-R5; CLAIM-R12 (reason values); CLAIM-R16; GWCLAIM register-slot-set; SLOTOPS metric-lenny-slot-assignment-conflict-total | SI-4 (recorded slot count, dead members) | yes | 770 words; the register's key names go to the component that owns the register. |
| §5.2 › Execution Modes | 63 | **Post-recovery rehydration atomicity.** | 413 | Migrated | SLOTOPS-R6; SLOTOPS setting-slotrehydrationtimeoutms, metric-lenny-slot-rehydration-total; the Tier 3 burst estimate to the operator guide |  | yes |  |
| §5.2 › Execution Modes | 64 | **Slot retry policy (`maxConcurrentSessions > 1`).** | 27 | Migrated | SLOTOPS-R7 |  |  |  |
| §5.2 › Execution Modes | 65 | **Max retries:** | 385 | Changed by a design | SLOTOPS-R7; APOD-R11 (replacement trigger); SLOTOPS setting-sessionpolicy-slotretries | SI-4 (dead slots count) |  |  |
| §5.2 › Execution Modes | 66 | **Truly stateless runtimes** | 20 | Removed by a design |  | AR-1; connector-removal |  |  |
| §5.2 › Execution Modes | 67 | "executionMode is declared on the Runtime from v1 (and …" | 13 | Removed by a design |  | AR-2 |  |  |
| §5.2 › Execution Mode Scaling Implications | 1 | "The default PoolScalingController formula (Section 4.6.2) assumes the default …" | 52 | Migrated | POOL-R10 | AR O7 (subsection renamed Scaling Implications) |  |  |
| §5.2 › Execution Mode Scaling Implications | 2 | **Term definitions:** | 38 | Migrated | POOL-R10 |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 3 | **Mode adjustment factor (`mode_factor`):** | 4 | Migrated | POOL-R10 |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 4 | **`session`** | 271 | Changed by a design | POOL-R10 | AR-2 (service bullet removed); ARR-12 |  |  |
| §5.2 › Execution Mode Scaling Implications | 5 | **Adjusted formula (non-experiment pools):** | 4 | Migrated | POOL-R10 |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 6 | Code block: target_minWarm = ceil(base_demand_p95 × safety_factor × | 19 | Migrated | POOL-R10 |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 7 | "For A/B experiment variant pools, apply variant_weight as defined …" | 27 | Removed by a design |  | RT-1 (experiment variants removed) |  |  |
| §5.2 › Execution Mode Scaling Implications | 8 | "The steady-state term (first) is divided by mode_factor because …" | 60 | Migrated | POOL-R10 (rationale) |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 9 | **`session`** | 55 | Changed by a design | POOL-R10 | AR-2 (service row removed) |  |  |
| §5.2 › Execution Mode Scaling Implications | 10 | "The (failover_seconds + pod_warmup_seconds) factor in the first term …" | 46 | Migrated | POOL-R10 (rationale) |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 11 | **Caveats:** | 1 | Migrated | POOL-R10 |  |  |  |
| §5.2 › Execution Mode Scaling Implications | 12 | "For recycling pools, mode_factor is derived from observed reuse …" | 375 | Migrated | POOL-R10 (cold start); the other caveats to the operator guide |  | yes |  |
| §5.2 › Pool Taxonomy | 1 | **Example pools:** | 2 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Pool Taxonomy | 2 | "claude-worker-sandboxed-small claude-orchestrator-microvm-medium …" | 4 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Pool Taxonomy | 3 | **Pool taxonomy strategy:** | 19 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Pool Taxonomy | 4 | **Hot pools** | 44 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Pool Taxonomy | 5 | "This prevents the combinatorial explosion of 3 runtimes × …" | 45 | Moved outside the specification | Operator guide |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 1 | "When a pool is first created (or after a …" | 38 | Migrated | POOL-R18 |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 2 | **`PoolWarmingUp` condition.** | 24 | Changed by a design | POOL-R18; KPSET Conditions | PLC-2 (condition on PodSet) |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 3 | "The pool's minWarm 0, and The current idlePodCount is …" | 36 | Migrated | POOL-R18 |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 4 | "The condition is cleared to False once idlePodCount >= …" | 44 | Migrated | POOL-R18 |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 5 | **Client-facing `503 Pool Not Ready` response.** | 20 | Migrated | POOL-R18; the response body to CAPISES (not migrated) |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 6 | Code block: HTTP 503 Service Unavailable Retry-After: <estimated_warmup_seconds> | 50 | Migrated | POOL-R18 (status, code, and details); the body schema to CAPISES (not migrated) |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 7 | "Retry-After is set to max(30, estimatedWarmupSeconds) where estimatedWarmupSeconds is …" | 42 | Migrated | POOL-R18 |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 8 | **`WarmPoolBootstrapping` alert.** | 51 | Migrated | POOL alert-warmpoolbootstrapping; POOL setting-warmupdeadlineseconds |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 9 | **Operator visibility.** | 23 | Not migrated in this spike | APOOLS |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 10 | **Topology spread constraints:** | 64 | Migrated | POOL-R17 | PM-5 |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 11 | "maxSkew: 1, topologyKey: topology.kubernetes.io/zone, whenUnsatisfiable: ScheduleAnyway (soft spread across …" | 22 | Migrated | POOL-R17 |  |  |  |
| §5.2 › Bootstrap Behavior — Pool Warming at First Deployment | 12 | "Deployers can override these defaults per pool via the …" | 33 | Migrated | POOL setting-topologyspreadconstraints; zone guidance to the operator guide |  |  |  |
| §6.2 Pod State Machine | 1 | "Sandbox.status.phase carries the coarse pod-occupancy enum: warming, idle, reserved, …" | 184 | Changed by a design | APOD Terms (pod-phase); APOD-R4 | PL-8 (sdk_connecting removed); FE-1 (quarantined) |  |  |
| §6.2 Pod State Machine | 2 | Code block: Warm fill (pod-warm path): warming ──→ | 637 | Changed by a design | APOD state machine; session states to SESS, SUSP, RESUME (not migrated) | PL-8; FE-1; SL L14 | yes | 637 words of diagram mixing pod phases and session states. |
| §6.2 Pod State Machine | 3 | **Pre-`running` slot cleanup.** | 137 | Not migrated in this spike | SLOT | SI-3 |  |  |
| §6.2 Pod State Machine | 4 | **`reserved` hold semantics.** | 295 | Duplicate | Duplicate of §4.6.1 ¶27; migrated as CLAIM-R10 and POOL-R15 | SI D4 |  |  |
| §6.2 Pod State Machine | 5 | **`leaked` slot semantics.** | 211 | Changed by a design | SLOT-R4; APOD-R11 | SI-4 (dead slots) |  |  |
| §6.2 Pod State Machine | 6 | **Host-node schedulability precondition.** | 346 | Changed by a design | APOD-R7 (duplicate of §4.6.1 ¶67 in part) | PL-8 (edge removed) | yes |  |
| §6.2 Pod State Machine | 7 | **preConnect re-warm on scrub_warning.** | 328 | Removed by a design |  | PL-8 |  |  |
| §6.2 Pod State Machine | 8 | **Pod crash during an active session.** | 77 | Migrated | APOD Failure behavior; retry to RESUME (not migrated) |  |  |  |
| §6.2 Pod State Machine | 9 | **Retry policy:** | 117 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 10 | "The resume_pending transition here is the session crash re-dispatch …" | 31 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 11 | **Concurrent pod lifecycle (`maxConcurrentSessions > 1`).** | 98 | Migrated | CLAIM-R3; APOD Terms | PL-9 (rename) |  |  |
| §6.2 Pod State Machine | 12 | **Pod failure during active slots.** | 314 | Migrated | SLOTOPS-R7 (pod failure); SLOTOPS-R2; APOD-R6; APOD-R11 |  | yes | Three bullets with three owners. |
| §6.2 Pod State Machine | 13 | "The transitions enumerated in the remaining subsections of this …" | 73 | Migrated | APOD state machine (note on session states) |  |  |  |
| §6.2 Pod State Machine | 14 | **`input_required` sub-state:** | 79 | Not migrated in this spike | SESS |  |  |  |
| §6.2 Pod State Machine | 15 | Code block: running → input_required (runtime calls lenny/request_input) | 91 | Not migrated in this spike | SESS |  |  |  |
| §6.2 Pod State Machine | 16 | "While in input_required, the maxSessionAge timer continues running (the …" | 30 | Not migrated in this spike | SESS; TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 17 | **`suspended` state:** | 12 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 18 | Code block: running → suspended (interrupt_request + interrupt_acknowledged) | 141 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 19 | "Workspace preserved, pod held until released after an interrupt …" | 61 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 20 | **`interrupt_request` does NOT cascade** | 21 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 21 | **Graceful pod release during extended suspension (`maxSuspendedPodHoldSeconds`).** | 172 | Not migrated in this spike | SUSP | SL L24 |  |  |
| §6.2 Pod State Machine | 22 | "1. The gateway initiates a checkpoint (same mechanism as …" | 263 | Not migrated in this spike | SUSP | SL L24 |  |  |
| §6.2 Pod State Machine | 23 | "Once the pod is released, resume_session, POST /v1/sessions/{id}/resume, and …" | 80 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 24 | **Interaction with other timers during podless suspension:** | 58 | Not migrated in this spike | SUSP; TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 25 | **Pod failure while `suspended` (pod still held):** | 140 | Not migrated in this spike | SUSP |  |  |  |
| §6.2 Pod State Machine | 26 | **`resuming` failure transitions:** | 98 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 27 | **Retryable failure during `resuming`** | 366 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 28 | "This ensures resuming has the same failure coverage as …" | 53 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 29 | **`maxSessionAge` timer behavior across states:** | 31 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 30 | Table: State Timer behavior | 457 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 31 | "The gateway persists the accumulated_session_age_seconds value in Postgres on …" | 77 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 32 | "The gateway also persists state_entered_at on the session row, …" | 102 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 33 | **`maxClientIdleSeconds` clock behavior across states.** | 62 | Not migrated in this spike | TIMEOUT | SL L14 |  |  |
| §6.2 Pod State Machine | 34 | "Any client-originated interaction: a message the runtime adapter writes …" | 313 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 35 | "The clock has its own pause table and does …" | 15 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 36 | Table: State Clock behavior | 164 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 37 | "The default value is 900 seconds (15 minutes), and …" | 172 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 38 | **Idle suspension.** | 490 | Not migrated in this spike | TIMEOUT; SUSP | SL L14 (dormancy) |  |  |
| §6.2 Pod State Machine | 39 | **`resume_pending` wall-clock cap:** | 149 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 40 | **`suspended → expired` trigger mechanism:** | 103 | Not migrated in this spike | TIMEOUT |  |  |  |
| §6.2 Pod State Machine | 41 | **Pre-attached failure retry policy:** | 55 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 42 | **Max retries:** | 376 | Not migrated in this spike | RESUME |  |  |  |
| §6.2 Pod State Machine | 43 | **State storage:** | 60 | Migrated | APOD-R6 (in part); session state storage to SESS (not migrated) |  |  |  |
| §6.2 Pod State Machine | 44 | Table: Label Values Purpose | 47 | Changed by a design | KLABEL label-lenny-dev-state, label-lenny-dev-pool, label-lenny-dev-runtime | PLC-2 (lenny.dev/pod-set added) |  |  |
| §6.2 Pod State Machine | 45 | "The lenny.dev/state label is derived solely from the projected …" | 116 | Duplicate | Duplicate of §6.2 ¶12 bullet Partial occupancy; migrated as APOD-R6 |  |  |  |
| §6.2 Pod State Machine | 46 | "This avoids the 8-10 label mutations per session that …" | 51 | Migrated | APOD-R6 (rationale) |  |  |  |
| §17.2 Namespace Layout | 1 | Code block: lenny-system/ # Gateway, token service, controller, | 35 | Not migrated in this spike | ARCH | PM-9 (lenny-agents-kata dropped) |  |  |
| §17.2 Namespace Layout | 2 | **Credentials namespace.** | 21 | Not migrated in this spike | ARCH |  |  |  |
| §17.2 Namespace Layout | 3 | **Operability-vs-agent boundary.** | 68 | Not migrated in this spike | ARCH |  |  |  |
| §17.2 Namespace Layout | 4 | **Pod Security Standards:** | 16 | Not migrated in this spike | POD |  |  |  |
| §17.2 Namespace Layout | 5 | **runc (`standard`) pods:** | 202 | Not migrated in this spike | POD | SI §17.2 row |  |  |
| §17.2 Namespace Layout | 6 | "This approach preserves the same security properties (non-root UID, …" | 37 | Not migrated in this spike | POD |  |  |  |
| §17.2 Namespace Layout | 7 | **Admission policy manifests** | 62 | Not migrated in this spike | WEBHOOK; KADMIT |  |  |  |
| §17.2 Namespace Layout | 8 | **Full Restricted PSS enforcement for runc pods** | 442 | Changed by a design | Item 5: KLABEL label-lenny-dev-tenant-id, label-lenny-dev-pool, label-lenny-dev-pod-set, label-lenny-dev-served; item 7: CLAIM-R11; other items to WEBHOOK and KADMIT (not migrated) | PLC-2 (labels, AgentPod CREATE check, podclaim guard); SI D4 (runtime-process label removed); TDR DR-5, EG (served, owner stamp) | yes | 442 words; two of its items carry claimer rules. |
| §17.2 Namespace Layout | 9 | **Rejection labels.** | 151 | Not migrated in this spike | KADMIT |  |  |  |
| §17.2 Namespace Layout | 10 | **High-availability requirement applies to every entry above.** | 574 | Not migrated in this spike | WEBHOOK |  |  |  |
| §17.2 Namespace Layout | 11 | **Preflight enumeration check (`lenny-preflight`).** | 102 | Not migrated in this spike | PREFLT |  |  |  |
| §17.2 Namespace Layout | 12 | **Feature-gated chart inventory (single source of truth).** | 63 | Not migrated in this spike | WEBHOOK |  |  |  |
| §17.2 Namespace Layout | 13 | Table: Helm value Default Gates ( templates/admission-policies/ | 60 | Not migrated in this spike | WEBHOOK |  |  |  |
| §17.2 Namespace Layout | 14 | "The five remaining webhooks — lenny-label-immutability (item 5), lenny-sandboxclaim-guard …" | 271 | Not migrated in this spike | WEBHOOK | PLC-2 (guard renamed) |  |  |
| §17.2 Namespace Layout | 15 | **High-availability contract applies to every rendered webhook.** | 83 | Not migrated in this spike | WEBHOOK |  |  |  |
| §17.2 Namespace Layout | 16 | **Feature-flag downgrade enforcement (phase-stamp ConfigMap + render-time validation + preflight mismatch check + runtime alert).** | 79 | Not migrated in this spike | DCFG |  |  |  |
| §17.2 Namespace Layout | 17 | **Persisted phase-stamp ConfigMap (`lenny-deployment-phase-stamp`).** | 186 | Not migrated in this spike | DCFG |  |  |  |
| §17.2 Namespace Layout | 18 | **Fail-closed chart render-time validation (`helm install` / `helm upgrade` only).** | 470 | Not migrated in this spike | DCFG |  |  |  |
| §17.2 Namespace Layout | 19 | **`lenny-preflight` mismatch check (primary enforcement for GitOps; also catches out-of-band mutation between phase-stamp and Helm render).** | 216 | Not migrated in this spike | DCFG; PREFLT |  |  |  |
| §17.2 Namespace Layout | 20 | **`AdmissionPlaneFeatureFlagDowngrade` Warning alert (§16.5).** | 241 | Not migrated in this spike | DCFG |  |  |  |
| §17.2 Namespace Layout | 21 | **Interaction with the baseline (always-rendered) webhooks.** | 81 | Not migrated in this spike | WEBHOOK | PLC-2 |  |  |
| §17.2 Namespace Layout | 22 | "An integration test suite (tests/integration/admission_policy_test.go) verifies that controller-generated pod …" | 198 | Moved outside the specification | TESTING.md |  |  |  |
| §17.2 Namespace Layout | 23 | **Elicitation content integrity platform floor (phase-stamp ConfigMap co-tenant).** | 275 | Not migrated in this spike | DCFG |  |  |  |
| §17.2 Namespace Layout | 24 | **Admission webhook failure mode:** | 164 | Not migrated in this spike | WEBHOOK |  |  |  |
| §17.2 Namespace Layout | 25 | "Namespace-level PSS labels remain at warn + audit (not …" | 29 | Not migrated in this spike | POD |  |  |  |
| §17.2 Namespace Layout | 26 | Code block: pod-security.kubernetes.io/warn: restricted pod-security.kubernetes.io/audit: restricted | 6 | Not migrated in this spike | POD |  |  |  |
| §17.2 Namespace Layout | 27 | **Node isolation:** | 55 | Not migrated in this spike | PLACE | PM |  |  |
| §17.2 Namespace Layout | 28 | **RuntimeClass `nodeSelector`:** | 173 | Not migrated in this spike | PLACE | PM |  |  |
| §17.2 Namespace Layout | 29 | **Resource governance:** | 35 | Not migrated in this spike | INSTALL |  |  |  |
| §17.2 Namespace Layout | 30 | **ResourceQuota** | 329 | Not migrated in this spike | INSTALL |  |  |  |
| §17.2 Namespace Layout | 31 | "The lenny-preflight Job validates that both ResourceQuota and LimitRange …" | 60 | Not migrated in this spike | PREFLT |  |  |  |

## Singleton citations

These rows cover the code's citations of sections outside the five above.

| Citation | What the code cites | Fate | Target and identifiers | Design and decision | Judgment | Note |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| §3.1, §3.2, §3.4 | Cited by tests as "3.2 (within-hold rebind ...)" and similar | No such heading |  |  | yes | The specification's §3 has no numbered subsections. The citations mean CLAIM-R10 (within-hold rebind, hold expiry) and CLAIM-R5 or CLAIM-R6 (pinned tenant), read from the citation text. |
| §6.41 | Cited as "3.4, 3.1, 6.41, 5.2" in slotclaimer_test.go | No such heading |  |  | yes | A typo; the test body exercises SLOTOPS-R1. |
| §5.1 | "the gateway-stamps-on-claim pattern" (workspace tier label) | Removed by a design |  | TSS-9; PM (lenny.dev/workspace-tier removed) |  |  |
| §6.4 | "the gateway-stamps-on-claim pattern" (lenny.dev/workspace-tier: t4) | Removed by a design |  | TSS-9; PM |  |  |
| §5.3 | Table row 3 (microvm to kata RuntimeClass) | Not migrated in this spike | ISO |  |  |  |
| §7.1 | Resume onto a replacement pod (slot reservation on a chosen pod) | Migrated | SLOTOPS-R2 |  | yes | The rest of §7.1 belongs to SCREATE, SEND, MATER, and RESUME and is not migrated. |
| §12.4 | "the counter is the intra-pod capacity gate" and the Redis outage gate | Migrated | SLOTOPS-R1; SLOTOPS-R5 |  |  | The rest of §12.4 belongs to REDIS, QUOTA, and STORREDIS and is not migrated. |
| §13.2 | NET-003 (lenny.dev/managed immutability) | Not migrated in this spike | NETISO | EG (F-13.2.31) |  |  |
| §16.3 | Span table row session.claim_pod | Migrated | GWCLAIM-R3 |  | yes | The table names the Controller as the span's component; the claim runs in the gateway, so the span is the claimer's. The rest of §16.3 belongs to OBSCONV. |

## Sources the pages carry beyond these sections

The written pages also carry content from sections the claimer does not cite, which this ledger does not enumerate paragraph by paragraph: §4.6.2 (the scaling formula, the derived state of `PodSet` objects, drift detection, and reserved pods counting as occupied, on POOL), §12.6 (the `agent_pod_state` schema, on CLAIM-R14), §15.1 (pool drain and deletion, on POOL-R25 and POOL-R26, and the error codes, on CLAIM), §16.5 (alerts, on CLAIM, POOL, and APOD), and §28.3 (`REG-CLAIM`, on CLAIM-R2). Their paragraphs get ledger rows when their own areas migrate.

## Contradictions and how the pages resolve them

| Contradiction | Resolution on the pages |
| :-- | :-- |
| RV-3 says the WarmPoolController deletes a `PodSet`; PLC makes the PoolScalingController the single writer of `PodSet` objects. | POOL-R7 follows PLC. |
| REC-1 and PLC keep the claim fencing verification paragraph, with its two tests, in §4.6.1; TAXONOMY version 2 sends verification tests to `TESTING.md`. | The tests move to `TESTING.md`, and the fencing argument stays as the rationale of CLAIM-R9. |
| SI D4 removes the runtime-process acknowledgment, which carried the rule that a used pod is admitted only when the resolved pool reuses pods; TDR DR-5 and DR-9 rely on that rule. | CLAIM-R5 keeps the used-pod rule and its drain request, without the runtime-process lifetime. |
| §5.2 and §4.6.3 name the tenant label webhook `lenny-tenant-label-immutability`; §17.2 item 5, PLC, and TDR name `lenny-label-immutability`. | KLABEL uses `lenny-label-immutability`. |
| §16.3 names the Controller as the component of the `session.claim_pod` span; CLAIM keeps the controller off the claim path, and the tree emits the span in the gateway. | GWCLAIM-R3 makes the span the claimer's. |
| §6.2 places the host-node precondition on the `claimed → sdk_connecting` edge, which PL-8 removes. | APOD-R7 applies it at the recycle disposition. |
| §5.2 retires a one-slot pod after any failed session; SI-3 runs the slot release and keeps the slot live after a failure before `session_start`. | Left unresolved on CLAIM-R3 and APOD-R8; the current rule is kept. |
| §5.2's "Runtime not live" retirement reads a report about a runtime process that, under SI D4 and D15, never outlives its session. | Left unresolved; APOD-R8 omits the condition. |
| SI-10 and PL-9 state the shared-pod list in different words. | SLOT-R1 uses the SI-10 list and records the difference. |
| §5.2 names "a `scrub_warning` annotation" without a key. | KLABEL uses the tree's key `lenny.dev/scrub-warning`. |
| UCD's term row lists two triggers of the retire-on-release mark; UC-3's body and UGI list three. | CLAIM-R17 lists three and leaves the field's name unresolved. |
