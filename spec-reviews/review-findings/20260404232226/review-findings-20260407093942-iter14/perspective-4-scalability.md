# Technical Design Review — Perspective 4: Scalability & Performance Engineering

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 4 — Scalability & Performance Engineering
**Iteration:** 14
**Prior finding checked:** SCL-036 (quota drift T1/T3 arithmetic) — RESOLVED
**New findings:** 0
**Total findings this iteration:** 0

## Prior Finding Verification

### SCL-036 — Quota drift T1/T3 arithmetic: RESOLVED

The per-tier quota drift bounds in Section 17.8.2 (line 8292) are now arithmetically consistent:

| Tier | Fail-open window | Implied request rate | Drift bound | Derivation |
|------|-----------------|---------------------|-------------|------------|
| T1 | 60s | 10 req/s (100 sessions * 0.1 req/s/session) | ~600 req | 10 * 60 = 600 |
| T2 | 60s | 100 req/s (1,000 sessions * 0.1 req/s/session) | ~6,000 req | 100 * 60 = 6,000 |
| T3 | 30s | 1,000 req/s (10,000 sessions * 0.1 req/s/session) | ~30,000 req | 1,000 * 30 = 30,000 |

All three tiers use a consistent 0.1 req/s/session baseline, and T3 correctly reflects its shorter fail-open window (30s vs 60s). No arithmetic error remains.

## Areas Reviewed (No Issues Found)

The following scalability-critical areas were reviewed in detail and found to be internally consistent:

1. **Gateway-centric throughput model (Section 4.1):** Per-subsystem isolation with independent goroutine pools, circuit breakers, and extraction thresholds. maxConcurrent values per tier scale proportionally with session targets. Extraction trigger metrics and threshold calibration methodology are well-specified.

2. **HPA scaling lag (Section 10.1):** Pipeline lag quantified (60s Prometheus Adapter, 20s KEDA). KEDA mandatory at Tier 3 with clear rationale (12,000 vs 4,000 session exposure during lag). minReplicas burst-absorption guidance provided. Scale-up policy (100%/15s or 4-8 pods/15s) is aggressive and appropriate.

3. **Redis scalability ceiling (Section 12.4):** Sentinel-to-Cluster migration path documented with concrete ceiling signals (CPU >70%, P99 >5ms, ops >80% budget). Tier 3 write throughput quantified at ~6,500/s sustained — well within Cluster capacity. Logical concern separation (coordination/quota/cache) reduces contention. Migration pre-plan with per-tenant feature flag rollback.

4. **Postgres write IOPS (Section 12.3):** Per-source breakdown sums correctly (300+100+600+300=1,300/s at T3). Write ceiling reference table with 80% alert thresholds. Instance separation offloads ~900/s (billing+audit), leaving ~400/s on primary. Horizontal scaling route (vertical -> separation -> partitioning) is practical.

5. **Warm pool sizing (Section 17.8.2):** Tier scale-down time calculations verified correct (T1: 2 min, T2: 7 min, T3: 8.3 min). Pod claim queue timeout (60s) provides adequate margin above failover window. Postgres fallback claim path decouples claim availability from API server.

6. **Startup latency budget (Section 6.3):** Per-phase P95 targets sum correctly (runc: ~5.7s rounds to <=6s; gVisor: ~8.7s rounds to <=9s). TTFT SLO (P95 <10s) leaves adequate setup command budget for runc (4s), though tight for gVisor (1s) — explicitly acknowledged. Phase 2 benchmark validation gates prevent premature SLO commitment.

7. **Session capacity math (Section 4.1):** maxSessionsPerReplica * min replicas is below tier targets at minimum scale but within HPA range at higher replica counts. Tier 3 prerequisite (LLM Proxy extraction or low ratio) is explicitly gated.

8. **PgBouncer connection math (Section 17.8.2):** Total pooled connections (replicas * (default_pool_size + reserve_pool_size)) remain well below Postgres max_connections at all tiers, leaving headroom for direct connections and monitoring.

9. **Checkpoint duration SLO (Section 4.4):** P95 <2s for <=100MB workspaces. Linear scaling estimate (~1s/100MB). Hard workspace size limit (emptyDir.sizeLimit) prevents unbounded quiescence. Pre-checkpoint size probe avoids starting checkpoints that will exceed limits.

10. **etcd write pressure (Section 4.6.1):** Status update deduplication window, coarse label strategy, and dedicated rate limiter buckets mitigate CRD churn. Per-tier compaction/defrag/quota guidance. Managed vs self-managed topology matrix clarifies operator responsibilities.

## Note on WPL-031 Overlap

The minWarm "conservative starting points" inconsistency (Section 17.8.2 line 8201 describes values as "conservative" but they omit safety_factor and burst term, making them less conservative than the full formula) was independently identified by Perspective 16 (WPL-031) in this same iteration. Not re-reported here.
