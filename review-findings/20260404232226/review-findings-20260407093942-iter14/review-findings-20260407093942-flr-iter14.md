# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Review perspective:** Failure Modes & Resilience Engineering (FLR)
**Iteration:** 14
**Prior findings file:** `review-findings-20260407093942-iter13.md`
**Total findings this iteration:** 2 (0 Critical, 0 High, 2 Medium)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 2     |

---

## Prior Finding Status

| ID | Description | Status |
|----|-------------|--------|
| FLR-038 | Redis runbook phantom metrics | **Still present** — see detailed re-verification below |

---

## Detailed Findings

### FLR-038 Redis Runbook References Undefined Metrics, Alerts, and Config Parameters [Medium] — STILL PRESENT

**Sections:** 12.4, 16.1, 16.5, 17.7

The Redis failure runbook (§17.7, lines 8091–8094) and the Redis fail-open description (§12.4, line 5082) reference metrics, alerts, and configuration parameters that are not defined in the canonical metrics table (§16.1) or alert tables (§16.5). Specifically:

1. **`lenny_quota_redis_fallback_total`** (counter) — referenced in the runbook trigger (line 8092) and remediation (line 8094) as the counter that increments when quota enforcement enters fail-open mode. Not present in the §16.1 metrics table (lines 7400–7488).

2. **`RedisUnavailable`** (alert) — referenced in the runbook trigger (line 8092: "`RedisUnavailable` alert fires"). Not present in either the Critical (lines 7577–7599) or Warning (lines 7601–7650) alert tables in §16.5. The closest existing alerts are `RateLimitDegraded` (Warning, line 7614) and `DualStoreUnavailable` (Critical, line 7596), but neither covers standalone Redis unavailability.

3. **`lenny_quota_redis_fallback_window`** (config parameter) — referenced in the runbook remediation (line 8094: "overage exposure is bounded by `lenny_quota_redis_fallback_window` (default 60s)"). This config parameter is not defined anywhere else in the spec. The actual parameter for rate limiting fail-open is `rateLimitFailOpenMaxSeconds` (§12.4, line 5078); the actual parameter for quota fail-open cumulative limit is `quotaFailOpenCumulativeMaxSeconds` (§12.4, line 5082). The runbook name matches neither.

4. **`quota_failopen_cumulative_seconds`** (gauge) — referenced in §12.4 (line 5082: "The gateway also emits `quota_failopen_cumulative_seconds` (gauge)"). Not present in the §16.1 metrics table.

5. **`QuotaFailOpenCumulativeThreshold`** (alert) — referenced in §12.4 (line 5082: "fires alert `QuotaFailOpenCumulativeThreshold` when the cumulative timer exceeds 80% of the configured maximum"). Not present in either alert table in §16.5.

**Impact:** Operators following the Redis failure runbook cannot find the referenced metric, alert, or config parameter in the spec's canonical tables. Monitoring dashboards and Prometheus alert rules built from §16.1/§16.5 will not include these signals, leaving the runbook's diagnostic steps non-functional.

**Recommendation:** (1) Add `lenny_quota_redis_fallback_total` (Counter) and `quota_failopen_cumulative_seconds` (Gauge) to the §16.1 metrics table. (2) Add `RedisUnavailable` and `QuotaFailOpenCumulativeThreshold` to the §16.5 alert tables with appropriate conditions and severities. (3) Rename `lenny_quota_redis_fallback_window` in the runbook to the actual config parameter name (`rateLimitFailOpenMaxSeconds` or `quotaFailOpenCumulativeMaxSeconds`, depending on the intended semantics).

---

### FLR-039 Operator Circuit Breaker State Has No Defined Failure Behavior During Redis Unavailability [Medium]

**Sections:** 11.6, 12.4

§11.6 (line 4802) defines that operator-managed circuit breaker state is stored in Redis as `cb:{name}` keys, with pub/sub propagation and a 5-second in-process cache TTL. §12.4 (lines 5063–5072) defines the "Failure behavior per use case" table covering six Redis-backed use cases: rate limit counters, distributed session leases, routing cache, cached access tokens, quota counters, and dual-store unavailability. Operator circuit breakers are not listed in this table.

When Redis becomes unavailable, the 5-second in-process cache expires within one poll interval. After that, gateway replicas cannot read circuit breaker state from Redis. The spec does not define whether the gateway should:

- **Fail-open** (treat all circuit breakers as closed) — this is dangerous because an operator may have opened a circuit breaker to protect against a failing runtime or connector during an incident. If a Redis outage coincides with (or is part of) the same incident, fail-open silently re-enables traffic to the system the operator explicitly blocked.
- **Fail-closed** (retain the last known state from the expired cache) — this is safer for open breakers but means that if an operator opens or closes a circuit breaker via the admin API during a Redis outage, the change cannot propagate to any replica.
- **Fall back to Postgres** — no Postgres-backed storage for circuit breaker state is defined.

This gap is operationally significant because circuit breakers are incident management tools. They are most likely to be used precisely when infrastructure is degraded — the scenario in which Redis is also most likely to be unavailable.

**Recommendation:** Add an entry to the §12.4 failure behavior table for "Operator circuit breakers." The recommended behavior is **retain last known state** (stale cache pinned on Redis unavailability, not expired): if Redis becomes unreachable, each replica freezes its in-process circuit breaker cache at the last successfully loaded state and does not expire it until Redis recovers. This ensures that circuit breakers opened before the Redis outage remain enforced. Additionally, the admin API should return `503 Service Unavailable` for circuit breaker state change operations when Redis is unreachable, so operators know their changes cannot propagate.
