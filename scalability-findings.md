# Scalability Review Findings

Review of uncommitted changes to `technical-design.md` across 25 perspectives from `review-povs.md`. Findings are deduplicated across perspectives and ordered by severity.

---

## F-01 [High] — §12.4 key table missing delegation budget keys; "sole exceptions" paragraph stale

**Perspectives:** 1, 2, 8, 9, 10, 18, 22, 24
**Location:** §12.4 lines 5688-5704, §8.3 R-04 line 3378

R-04 introduces `{root_session_id}:dlg:*` keys that do not follow the `t:{tenant_id}:` prefix convention. However:
1. These keys are absent from the §12.4 canonical key prefix table.
2. The exceptions paragraph (line 5704) claims `lenny:pod:*` and `cb:*` are the "sole exceptions" — factually wrong after R-04.
3. `TestRedisTenantKeyIsolation` does not require delegation budget key coverage.
4. The Redis wrapper enforcement rule ("no raw Redis command without tenant prefix") contradicts the new key format.

**Status: FIXED** — Added delegation budget keys to §12.4 table, updated exceptions paragraph, added test coverage item (e).

---

## F-02 [High] — Rule numbering gap: R-01–R-05 and R-09–R-10 don't exist

**Perspectives:** 22
**Location:** §12.3, §8.3

The document introduces R-06, R-07, R-08, R-11 with no prior rules. The numbering implies missing predecessors (R-01–R-05) and a gap (R-09–R-10). No rule index or numbering scheme explanation exists.

**Status: FIXED** — Renumbered to R-01, R-02, R-03, R-04. Updated all cross-references.

---

## F-03 [High] — `RedisClusterRecommended` advisory log referenced but undefined

**Perspectives:** 12, 22
**Location:** §17.8 line 9849, §16.5 alerts table

`RedisClusterRecommended` is mentioned in §17.8 as a suppressible advisory log but does not appear in the §16.5 alerts table or anywhere else. Operators cannot find it in the canonical alert reference.

**Status: FIXED** — Clarified in §17.8 that this is a gateway startup log (not a Prometheus alert), consistent with the `[WARN] capacityPlanning` pattern.

---

## F-04 [High] — §17.8 single-tenant analysis claims "all keys share same hash tag" but R-04 contradicts

**Perspectives:** 1
**Location:** §17.8 line 9840

The single-tenant note says "all keys share the same `{tenant_id}` hash tag and land on the same shard." After R-04, delegation budget keys use `{root_session_id}` and distribute across slots even in single-tenant deployments. The premise is partially invalidated.

**Status: FIXED** — Updated §17.8 to acknowledge that delegation budget keys are the exception; Sentinel recommendation still holds on aggregate ops/s grounds.

---

## F-05 [High] — EventBus interface lacks tenant isolation, delivery semantics, and error handling

**Perspectives:** 2, 3, 5, 9, 14, 23
**Location:** §12.6 lines 5915-5920

Multiple compounding issues:
1. `topic string` parameter has no tenant-prefix enforcement — future NATS/Kafka implementations could leak cross-tenant events.
2. `handler func([]byte)` has no error return — handler failures silently lose events.
3. No delivery guarantee contract — Redis pub/sub is fire-and-forget; NATS/Kafka require acknowledgement.
4. No topic naming convention documented.
5. `circuit_breaker_events` pub/sub bypasses EventBus entirely (§11.6), undermining the abstraction.

**Status: FIXED** — Redesigned the EventBus interface: (1) added `TenantID` parameter for interface-level tenant isolation; (2) changed handler to `func(context.Context, []byte) error` for error propagation and context/tracing; (3) added typed `EventTopic` constants (`TopicDelegationTree`, `TopicSessionLifecycle`) replacing bare strings; (4) added delivery contract (at-most-once in v1, handlers must be idempotent for future at-least-once backends); (5) added handler design note explaining events are notifications — handlers check durable state, making them naturally idempotent; (6) documented that infrastructure pub/sub (circuit breakers, deny lists) intentionally bypasses EventBus as platform-wide cache-invalidation signals. Also added `t:{tenant_id}:evt:{topic}` to §12.4 key table, added EventBus coverage item (f) to `TestRedisTenantKeyIsolation`, and fixed §17.8 single-tenant topology table to move pub/sub from Coordination to Cache/hot routing (resolving inconsistency with §12.4 rationale).

---

## F-06 [High] — PodRegistry conflicts with existing PodLifecycleManager/PoolManager interfaces

**Perspectives:** 1, 16
**Location:** §12.6 lines 5843-5859 vs. §4.6.1 lines 404-423

PodRegistry duplicates `ClaimPod`, `ReleasePod`, `CreatePod`, `DeletePod` from the existing PodLifecycleManager/PoolManager with different signatures and types. No stated relationship between the three interfaces. `PodRegistry` is placed in `platform/store` but manages Kubernetes CRD operations (a controller-layer concern).

**Status: FIXED** — Not a conflict but intentional layering. Added clarification to §12.6 PodRegistry that it is the data-access layer consumed by the §4.6.1 business-logic interfaces (`PodLifecycleManager`, `PoolManager`). Method names overlap intentionally: PodRegistry provides the storage primitive; §4.6.1 interfaces add domain logic on top.

---

## F-07 [Medium] — R-04 key naming ambiguity: `parallelChildren`/`childrenTotal` tree-wide vs per-session

**Perspectives:** 10
**Location:** §8.3 R-04 line 3378

R-04 lists `{root_session_id}:dlg:parallel_children` and `{root_session_id}:dlg:children_total` as tree-wide keys. But the delegation model allows each intermediate node to have its own `maxParallelChildren` limit. If these counters are tree-wide (single counter per tree), per-node limits cannot be enforced independently. If per-session (keyed as `{root_session_id}:dlg:parallel_children:{session_id}`), R-04's key list is incomplete.

**Status: FIXED** — Updated R-04 to distinguish tree-wide keys from per-parent-session keys, and clarified that `childrenTotal` is monotonic (never decremented by `budget_return.lua`).

---

## F-08 [Medium] — Coordinator handoff does not specify `tenant_id` source

**Perspectives:** 2, 11, 20
**Location:** §10.1 line 4248

The CAS UPDATE requires `$tenant_id` but the handoff protocol never specifies where the acquiring replica obtains it. In the Redis lease path, only `session_id` is available. `SessionShardBySession` exists for this reason but the protocol doesn't include a preliminary read step.

**Status: FIXED** — Added explicit step specifying that the replica reads `tenant_id` and `coordination_generation` from the session row (via `StoreRouter.SessionShard`) before the CAS UPDATE.

---

## F-09 [Medium] — `SessionShardBySession` shard directory unspecified

**Perspectives:** 9, 11, 20
**Location:** §12.6 line 5883

"e.g., a consistent-hash or directory lookup" defers a meaningful architectural decision. No specification of where the directory lives, failure behavior, or latency budget for the coordinator handoff hot path.

**Status: FIXED** — Redesigned the StoreRouter interface to cleanly separate tenant-routed and session-routed concerns. Renamed `SessionShard(tenantID)` → `TenantShard(tenantID)` for tenant metadata tables; renamed `SessionShardBySession(sessionID)` → `SessionShard(sessionID)` as the primary session routing method. Specified consistent hash of `session_id` as the future sharding strategy with 5 ms P99 latency requirement. Added sharding strategy prose, caller guide, R-01 session-sharded table exemption with `-- session-sharded` linter annotation. Updated coordinator handoff (§10.1) to reference `StoreRouter.SessionShard(session_id)` and corrected the CAS UPDATE `tenant_id` predicate rationale (RLS enforcement, not shard routing).

---

## F-10 [Medium] — Interface types (PodID, RedisConcern, StateTransition, etc.) undefined

**Perspectives:** 18
**Location:** §12.6

All five interfaces reference ~15 custom types (`PodID`, `PoolID`, `TenantID`, `SessionID`, `ClusterID`, `PodRecord`, `StateTransition`, `RedisConcern`, `StoreType`, etc.) that are never defined. `RedisConcern` and `StoreType` are particularly impactful — they determine the routing topology.

**Status: FIXED** — Added shared type definitions block before PodRegistry in §12.6: five ID types (`PodID`, `PoolID`, `TenantID`, `SessionID`, `ClusterID`), three enums (`RedisConcern` with 4 concerns, `StoreType` with 4 categories, `ReleaseReason` with 4 reasons), `Subscription` interface, and a struct cross-reference table mapping 12 struct types to their defining sections and key fields. `CredentialLease` cross-references the full 10-field JSON schema in §4.9.

---

## F-11 [Medium] — Tenant deletion (§12.8) does not purge delegation budget keys

**Perspectives:** 8
**Location:** §12.8 Phase 4 tenant deletion

`{root_session_id}:dlg:*` keys cannot be found by a `t:{tenant_id}:*` glob scan. The deletion sequence must enumerate `root_session_id` values from `SessionStore` before deleting sessions, then explicitly purge each tree's budget keys.

**Status: FIXED** — Added delegation budget key purge to both deletion paths: new step 15 in `DeleteByUser` and new chain entry in tenant deletion Phase 4, both positioned after `EvalResultStore` and before `SessionStore`. Both enumerate root session IDs via `AllSessionShards()` scatter-gather, then purge `{root_session_id}:dlg:*` keys via `RedisShard(tenantID, RedisConcernDelegation)`. Added defense-in-depth TTL note to §8.3 R-04 (`maxSessionDuration + 1h`).

---

## F-12 [Medium] — Workload profile Lua rate formula is time-averaged; burst not reconciled with §8.3

**Perspectives:** 4, 10
**Location:** §16.5 line 8993

`10,000 x 0.05 x 10 / 333 ~ 15/s` is correct as a sustained average but orchestrator sessions typically burst all delegations at session start. The §8.3 separation trigger fires at 50 instantaneous scripts. The gap between 15/s average and burst is not bridged.

**Status: FIXED** — Added clarification that the 15/s figure is a sustained time-average; peak instantaneous rates are governed by §8.3 `maxParallelChildren` limits and should be monitored via `lenny_redis_lua_script_duration_seconds` burst percentiles.

---

## F-13 [Medium] — Single-tenant Redis ops/s estimates lack derivation

**Perspectives:** 4
**Location:** §17.8 lines 9842-9847

The per-concern ops/s figures (~10K coordination, ~20K quota, ~400 delegation, ~50K cache) appear as round numbers with no derivation. The ~400 delegation figure is not reconciled with the 15/s Lua rate from §16.5.

**Status: NOT FIXED** — Requires load testing to produce real numbers. The current figures are speculative back-of-envelope estimates; adding derivation sketches would give false precision. Flagged for Phase 13.5 (load testing) follow-up.

---

## F-14 [Medium] — "Configuration-only" Tier 4 claim overstated for PodRegistry and EventBus

**Perspectives:** 7
**Location:** §12.6 line 5841

PodRegistry Tier 4 requires `agent_pod_state` schema migration. EventBus Tier 4 requires deploying NATS/Kafka infrastructure. Neither is "configuration-only."

**Status: FIXED** — Qualified the claim to specify it refers to application code (no restructuring), with a note that PodRegistry and EventBus require accompanying infrastructure changes.

---

## F-15 [Medium] — R-01 schema linter assigned to Phase 2 but migrations start at Phase 1.5

**Perspectives:** 19
**Location:** §12.3 R-01 line 5651

Migrations introduced in Phase 1.5 have no linter coverage until Phase 2. Non-conforming indexes could be introduced before the linter exists.

**Status: FIXED** — Changed "Phase 2 CI check requirement" to "Phase 1.5 CI check requirement."

---

## F-16 [Medium] — ClusterRegistry.ClusterClient has no cross-cluster transport security contract

**Perspectives:** 3
**Location:** §12.6 lines 5885-5897

The interface returns a remote `PodRegistry` with no requirement for mTLS, bearer-token validation, or CA bundle. Future implementations could use unauthenticated connections.

**Status: FIXED** — Added cross-cluster transport security contract to ClusterRegistry: mTLS required, `CACertBundle` in `ClusterInfo` for CA verification, mutual SAN validation, `DeletePod` prohibited over cross-cluster connections. `LocalClusterRegistry` (v1) exempt. Updated `ClusterInfo` in struct cross-reference table to include `CACertBundle`.

---

## F-17 [Medium] — R-01 (shard-key index) tension with `sessions.id` FK references

**Perspectives:** 9
**Location:** §12.3 R-01 vs. various FK references

The spec references `sessions.id` as a simple column FK target throughout. If R-01 requires `(tenant_id, id)` composite PK, all FK references need updating. The spec does not clarify whether R-01 means composite PK or additional leading index.

**Status: FIXED** — Resolved by the R-01 session-sharded table exemption (added in the F-09 fix). Session-scoped tables (`sessions`, `session_messages`, `session_tree_archive`) are exempt from the leading-`tenant_id` PK requirement and can use `(id)` as their primary key. This means `sessions.id` as a simple FK target is correct — no composite `(tenant_id, id)` PK is needed. The exemption requires a `(tenant_id, ...)` secondary index for scatter-gather queries.

---

## F-18 [Low] — `capacityPlanning.*` startup warning fires only at Tier 3; Tier 1/2 unguarded

**Perspectives:** 4, 7
**Location:** §16.5 line 8993

Operators at Tier 2 with atypical workloads (e.g., high delegation rate) get no warning that defaults may be wrong. `avgSessionDurationSeconds=333` is Tier 3-calibrated; applying at Tier 2 yields claim rate 40% below documented baseline.

**Status: FIXED** — Changed startup warning condition from Tier 3 only to Tier 2 and above.

---

## F-19 [Low] — No observability contract on PodRegistry/EventBus interfaces

**Perspectives:** 12
**Location:** §12.6

These are the only store-layer abstractions with no named metrics. At minimum need `lenny_pod_registry_operation_duration_seconds` and `lenny_event_bus_publish_total`.

**Status: FIXED** — Added observability contract paragraphs to both PodRegistry and EventBus in §12.6. PodRegistry: `lenny_pod_registry_operation_duration_seconds{operation, pool}` and `lenny_pod_registry_error_total{operation, pool}` covering all 9 operations. EventBus: `lenny_event_bus_publish_total{topic}`, `lenny_event_bus_publish_duration_seconds{topic}`, `lenny_event_bus_handler_duration_seconds{topic}`, `lenny_event_bus_handler_error_total{topic}`. All metrics also added to the §16.1 metrics inventory table.

---

## F-20 [Low] — `agent_pod_state` table schema undefined

**Perspectives:** 9
**Location:** §12.6 line 5845, §4.6.1

Referenced multiple times as both a current v1 mirror table and Tier 4 primary store, but no column schema or DDL exists.

**Status: FIXED** — Added `agent_pod_state` DDL to §12.6 PodRegistry: 9 columns (`pod_id`, `pool_id`, `state`, `session_id`, `isolation_profile`, `execution_mode`, `resource_version`, `node_name`, `updated_at`), two indexes (`pool_id + state` for pool queries, partial `session_id` for orphan reconciler), and explanation of optimistic-locking CAS and staleness detection.

---

## F-21 [Low] — R-02 platform-admin annotation lacks paired RBAC guard and audit event

**Perspectives:** 2, 8, 13
**Location:** §12.3 R-02 line 5653

The `-- platform-admin-cross-tenant-allowed` annotation is a linter suppression only. No requirement for paired `assertPlatformAdmin(ctx)` guard, no audit event for cross-tenant reads, no annotation inventory controls.

**Status: FIXED** — Added three safeguards to R-02: (1) pairing rule requiring annotated queries to live in platform-admin code paths with `__all__` sentinel, linter SHOULD verify package location; (2) `cross_tenant_read` audit event required for every `__all__` code path (one per API call); (3) annotation inventory CI check requiring `-- platform-admin-cross-tenant-justification: <reason>` comment for new annotations.

---

## F-22 [Low] — Five §12.6 interfaces have no build phase assignment

**Perspectives:** 19
**Location:** §12.6

`StoreRouter` is needed before Phase 4 (billing writes); `PodRegistry` before Phase 3 (PoolScalingController). No phase deliverable defined.

**Status: FIXED** — Added build phase assignment table to §12.6 mapping each interface to its introduction phase and earliest consumer: PodRegistry (Phase 3), StoreRouter (Phase 4), CredentialGenerator (Phase 5.5), ClusterRegistry (Phase 9), EventBus (Phase 9).
