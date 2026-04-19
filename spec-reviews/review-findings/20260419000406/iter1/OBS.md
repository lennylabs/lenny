# Observability & Operational Monitoring Review

## Findings

### OBS-001 Undefined Metric for CheckpointDurationHigh Alert [Critical]

**Files:** `16_observability.md`

The alert `CheckpointDurationHigh` (line 364) references metric `lenny_checkpoint_duration_seconds` that is never defined in the metrics table. The specification states: "P95 of `lenny_checkpoint_duration_seconds` for Full-level or embedded-adapter pools exceeds 2.5 seconds" but this metric name does not appear in Section 16.1 (Metrics). Similarly, the burn-rate alert `CheckpointDurationBurnRate` (line 501) also references this undefined metric.

**Recommendation:** Define `lenny_checkpoint_duration_seconds` explicitly in the metrics table (Section 16.1) with: metric name, type (Histogram), labels (pool, level, or both), and description of the measurement boundary (e.g., time from checkpoint quiescence request to snapshot upload complete).

---

### OBS-002 Missing Metric Names in Metrics Table [High]

**Files:** `16_observability.md`

Multiple entries in the metrics table lack metric names and are listed only as descriptions. This breaks the contract that every metric referenced in alerts must be discoverable in the metrics table:

- Line 29: "Policy denials (by `error_type`, `tenant_id`)" — no metric name
- Line 30: "Checkpoint size and duration" — no metric name  
- Line 7: "Session creation latency (phases)" — no metric name (referenced in narrative context)
- Line 20: "Time-to-claim (session request to pod claimed)" — no metric name
- Line 22: "Pod state transition durations (per state)" — no metric name
- Line 25: "Upload bytes/second and queue depth" — no metric name
- Line 26: "Token usage (by user, runtime, tenant)" — no metric name
- Line 27: "Retry count (by failure classification)" — no metric name
- Line 28: "Resume success/failure rate" — no metric name
- Line 47: "Delegation depth distribution" — no metric name

**Recommendation:** Assign explicit metric names (backtick-delimited `lenny_*` identifiers) to all metrics. Names must be consistent with the naming convention established in Section 16.1.1 and must be referenced in any alert that uses them.

---

### OBS-003 Inconsistent Metric Label Names Across Delegation Metrics [Medium]

**Files:** `16_observability.md`

Delegation metrics use inconsistent label naming. Lines 52-54 define `lenny_delegation_tree_memory_bytes` and `lenny_delegation_memory_budget_utilization_ratio` both labeled by `pool` and `tenant_id`, but line 48 defines `lenny_delegation_budget_utilization_ratio` with NO label specification. This creates ambiguity: is the budget metric global, per-pool, or per-tenant? The narrative in Section 8.3 suggests per-tree granularity should be available.

**Recommendation:** Clarify whether `lenny_delegation_budget_utilization_ratio` should be labeled by `pool` and/or `tenant_id` to match sibling metrics. If truly global, document why per-tenant/pool variants are not needed for observability.

---

### OBS-004 Undefined Metrics in Burn-Rate Alerts [Critical]

**Files:** `16_observability.md`

The SLO burn-rate table (lines 494–501) references metric filters that do not align with metric definitions:

- Line 498: `StartupLatencyBurnRate` specifies filter `{isolation_profile="runc"}` for `lenny_session_startup_duration_seconds` — but metric definition (line 14) includes `isolation_profile` as a valid label, so this is valid.
- Line 499: `StartupLatencyGVisorBurnRate` specifies filter `{isolation_profile="gvisor"}` — valid per line 14.
- Line 500: `TTFTBurnRate` uses `lenny_session_time_to_first_token_seconds` — metric defined at line 15 without `isolation_profile` label. Metric labels are `pool`, `runtime_class` only. If isolation-level breakdown is needed, the metric definition must be updated.

**Recommendation:** For TTF burn-rate alert on line 500, either (a) add `isolation_profile` label to `lenny_session_time_to_first_token_seconds` definition or (b) remove the expectation of per-isolation-profile SLO breakdown from the alert narrative.

---

### OBS-005 Missing Pool-Specific Labeling on Critical Warm Pool Metrics [Medium]

**Files:** `16_observability.md`

Line 8 references alert `PodClaimQueueSaturated` which fires when `lenny_pod_claim_queue_depth > 0.25 × pool.minWarm` for > 30s AND `lenny_warmpool_idle_pods > 0`. However, line 8's definition of `lenny_warmpool_idle_pods` specifies it is "labeled by `pool`" — meaning the alert condition must include pool filtering. The alert definition (line 365) does not explicitly state the pool filter in the alert condition, creating ambiguity about whether this is a per-pool or global check.

**Recommendation:** In the alert definition for `PodClaimQueueSaturated` (line 365), explicitly document the filtering: "per pool, when queue depth exceeds 25% of `minWarm` for that pool and idle pods exist for that pool."

---

### OBS-006 Inconsistent Checkpoint Metric Description [Low]

**Files:** `16_observability.md`

Line 364 describes `CheckpointDurationHigh` as "P95 of `lenny_checkpoint_duration_seconds` for Full-level or embedded-adapter pools exceeds 2.5 seconds." However, this wording conflates two separate filters: the pool's isolation level (Full vs. embedded-adapter) should be a label, not a textual qualifier. If the metric does not track pool isolation type, the SLO cannot be level-specific.

**Recommendation:** Ensure `lenny_checkpoint_duration_seconds` (when defined) includes labels sufficient to break down by isolation level if the SLO requires per-level thresholds. If only pool-level breakdown is available, re-specify the SLO as "P95 across Full-level and embedded-adapter pools in aggregate" or split into separate SLOs per level.

---

## Summary

Six distinct observability issues detected:

1. **OBS-001 (Critical):** Undefined `lenny_checkpoint_duration_seconds` metric breaks two alerting rules.
2. **OBS-002 (High):** Ten metric entries lack names; breaks metric discovery and alert referenceability.
3. **OBS-003 (Medium):** Inconsistent delegation budget metric labeling creates ambiguity.
4. **OBS-004 (Critical):** TTFTBurnRate alert references potentially unavailable isolation_profile label.
5. **OBS-005 (Medium):** PodClaimQueueSaturated alert condition lacks explicit per-pool filtering.
6. **OBS-006 (Low):** CheckpointDurationHigh SLO wording conflates filters and labels.

The first two are specification defects requiring immediate correction before validation. The remainder clarify label scope and alert conditions.
