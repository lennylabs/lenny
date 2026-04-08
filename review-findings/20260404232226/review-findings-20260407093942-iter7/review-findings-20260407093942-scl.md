# Technical Design Review Findings — 2026-04-07 (Iteration 7, Perspective 4: Scalability & Performance Engineering)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Scalability & Performance Engineering
**Iteration:** 7
**Category prefix:** SCL (starting at 027)
**Total findings:** 2

Prior SCL findings reviewed: SCL-001 through SCL-026. None regressed.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 2     |

---

## Detailed Findings

---

### SCL-027 §12.4 Tier 3 Redis Throughput Table Built on 10× Wrong Session Count [Medium]

**Section:** 12.4

**Description:**

The "Tier 3 Redis write throughput quantification" table in §12.4 opens with:

> "At Tier 3 scale (~1,000 concurrent sessions, 200 new sessions/s)…"

The canonical Tier 3 definition in §16.5 is **10,000 concurrent sessions, 200/s creation rate**. The session count is off by a factor of 10. The creation rate (200/s) is correct, but that is the minor write contributor; concurrent session count drives the session-proportional write sources:

| Write Source (§12.4 estimate) | At ~1,000 sessions | At 10,000 sessions (actual Tier 3) |
| ----------------------------- | ------------------- | ---------------------------------- |
| Lease renewals (`SET` w/ TTL) | ~100/s (`1,000 / (TTL/2)`) | ~1,000/s (10× higher) |
| Quota counter increments | ~200/s per active tenant | ~2,000/s (scales with RPC volume over 10,000 sessions) |
| Rate limit increments | ~200/s | ~2,000/s |
| Delegation budget Lua | ~50–100/s | ~500–1,000/s (scales with active delegation trees) |
| Token cache writes | ~50/s | ~500/s (scales with cold-miss rate over 10× more sessions) |
| **Stated total** | **~600–650/s** | **~6,000–6,500/s (estimated)** |

The §12.4 table conclusion — "The ~650/s sustained Tier 3 write rate is well within a single primary's capacity for write throughput alone" — follows correctly from the wrong premise. At the actual Tier 3 concurrent session count, the sustained write rate is roughly 10× higher (~6,000–6,500/s), which approaches or exceeds the single-threaded Redis primary's throughput budget for the combined operation mix (the §17.8.2 Tier 3 Redis budget estimate is ~100,000 ops/s total, but that encompasses reads, writes, and pub/sub across the fleet of sessions; a 10× higher write rate from sessions alone could crowd out other operation types).

**Downstream impact:**

The §12.4 analysis is the sole basis for the claim that "The ~650/s sustained Tier 3 write rate is well within a single primary's capacity." This claim is used to justify deferring Redis Cluster migration until ceiling signals fire. If the actual write rate is 10× higher, operators may defer Cluster migration longer than is safe, hitting write-latency degradation before the planned migration triggers.

The §17.8.2 Redis table does correctly recommend Redis Cluster at Tier 3 and a ~100,000 ops/s budget estimate — but operators reading the §12.4 quantification will see a figure 10× below what is consistent with Tier 3 reality, undermining confidence in the migration trigger criteria.

**Fix:** Correct the §12.4 opening from "~1,000 concurrent sessions" to "~10,000 concurrent sessions" and revise the per-source estimates proportionally. The revised total (~6,000–6,500/s sustained, burst ~20,000/s) will still be well within the Tier 3 budget at ~100,000 ops/s total (writes are a fraction of the ops mix), but the conclusion changes from "trivially safe on a single primary" to "within budget given the Tier 3 Redis Cluster topology." The revised estimate also validates the §17.8.2 ~100,000 ops/s budget more concretely (6,000–6,500 write ops/s × ~15× read/ops multiplier ≈ 97,500 total ops/s, consistent with the 100,000 estimate).

---

### SCL-028 `PodClaimQueueSaturated` Alert Condition References `maxConcurrent` — Wrong for Session-Mode Pools [Medium]

**Section:** 16.5

**Description:**

The `PodClaimQueueSaturated` warning alert in §16.5 is defined as:

> `lenny_pod_claim_queue_depth` exceeds 50% of the pool's `maxConcurrent` session rate for > 30s; indicates claim queue is backing up even though warm pods may exist

The expression "50% of the pool's `maxConcurrent` session rate" is incoherent for two reasons:

1. **`maxConcurrent` is a concurrent-execution-mode field, not a session rate.** `maxConcurrent` is the per-pod slot count for `executionMode: concurrent` pools (e.g., `maxConcurrent: 8` means each pod handles 8 simultaneous tasks). For the default `executionMode: session` and `executionMode: task` pools, `maxConcurrent` is undefined. The §17.8.2 subsystem table lists `maxConcurrent` only for gateway subsystems (Stream Proxy 2,000 at Tier 2, etc.), not for pool-level session rates.

2. **Even for concurrent-mode pools, the comparison is dimensionally wrong.** `lenny_pod_claim_queue_depth` is an integer count of queued claim requests. "50% of `maxConcurrent`" for a typical concurrent-mode pool (e.g., `maxConcurrent: 8`) evaluates to 4 — meaning the alert fires whenever there are more than 4 queued claim requests. This would fire constantly under any moderate load, producing permanent noise that operators will quickly mute.

**Impact:**

- For session-mode and task-mode pools (the common case), the alert condition references a field that does not exist for those pools — the alert is either permanently silent or evaluates to an undefined/zero denominator, producing a divide-by-zero or always-firing condition depending on the monitoring implementation.
- For concurrent-mode pools, the threshold (queue depth > 4 for an 8-slot pool) is almost certainly too sensitive to be useful, generating false positives under normal burst conditions.
- The intent of the alert — detecting claim queue back-pressure when warm pods exist — is operationally important and is not served by the current expression.

**Correct threshold expression:** The alert should compare `lenny_pod_claim_queue_depth` against a meaningful threshold such as:
- A fixed value (e.g., `> 20 for > 30s`) tied to the pool's expected claim burst headroom, or
- The pool's configured `minWarm` (e.g., `> 0.5 × minWarm` — if more than half the warm-pod buffer is being consumed by queued claims, the pool is likely undersized), or
- A per-tier configurable threshold analogous to the `WarmPoolLow` alert's `25% of minWarm` pattern.

**Fix:** Replace the alert condition in §16.5 with a well-defined expression. A concrete option aligned with existing patterns:

> `lenny_pod_claim_queue_depth` for any pool exceeds 25% of the pool's `minWarm` value for > 30s and `lenny_warmpool_idle_pods` > 0 (warm pods exist but claims are queuing). Indicates claim dispatch latency is building up despite available warm pods — investigate gateway-to-controller claim path or API server latency.

This threshold is dimensionally consistent (both are pod/request counts), fires only when meaningful back-pressure exists, and the "warm pods exist" qualifier (from the original description) is now explicit in the condition. The `minWarm` comparison gives a pool-proportional threshold that works across all execution modes and tier sizes.

---

## Prior SCL Findings Status

All SCL-001 through SCL-026 reviewed. No regressions detected. The fixes applied in iterations 1–6 are correctly reflected in the spec:

- SCL-001: Gateway subsystem extraction thresholds documented as provisional with Phase 2 calibration methodology (§4.1).
- SCL-002: `maxSessionsPerReplica` per-replica capacity budget table and calibration methodology present (§4.1).
- SCL-003: Startup latency SLOs marked as requiring Phase 2 validation (§16.5 SLO table).
- SCL-004: Redis Sentinel ceiling signals and Cluster migration pre-plan documented (§12.4).
- SCL-005: HPA custom metric pipeline end-to-end latency table and KEDA path documented (§10.1).
- SCL-006: Postgres horizontal write scaling route with instance separation at Tier 3 documented (§12.3).
- SCL-007: Cold-start bootstrap mode with operator override API documented (§4.6.2, §17.8.2).
- SCL-008/SCL-016: Lua script serialization analysis, ceiling guidance, and `maxParallelChildren` table present (§8.3).
- SCL-009/SCL-023: Experiment targeting webhook circuit breaker documented (§10.7).
- SCL-010: Durable inbox mode (`durableInbox: true`) with Redis-backed inbox documented (§7.2).
- SCL-011: etcd write pressure mitigations (status update deduplication, rate limiter buckets, topology matrix) documented (§4.6.1).
- SCL-015: Fleet-wide GC pressure metric and `Tier3GCPressureHigh` alert present (§16.1, §16.5).
- SCL-017: KEDA mandatory at Tier 3 (§10.1).
- SCL-018: Per-tier Postgres write ceiling reference table present (§12.3).
- SCL-024: KEDA/HPA mutual exclusivity paragraph present (§10.1).
- SCL-025: `--status-update-dedup-window` semantics documented (§4.6.1, §17.8.2).
- SCL-026: Gateway HPA metric role canonical table present (§4.1).
