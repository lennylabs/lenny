# Review Findings — Iteration 9, Perspective 12: Observability & Operational Monitoring

**Spec:** `docs/technical-design.md` (8,671 lines)
**Date:** 2026-04-07
**Prior findings:** OBS-001 through OBS-041 (iterations 1–8). OBS-031 and OBS-040 skipped per scope exclusion.
**Category prefix:** OBS, starting at OBS-042.

---

## Summary

Four genuine design flaws found. OBS-041's threshold fix was applied in iteration 8 but its second recommendation (add `CheckpointDurationBurnRate`) was not — that omission is noted as context but the new findings are numbered from OBS-042 as instructed.

| # | ID      | Severity | Description                                                                                         | Sections        |
|---|---------|----------|-----------------------------------------------------------------------------------------------------|-----------------|
| 1 | OBS-042 | High     | `AuditSIEMNotConfigured` Critical variant is self-defeating: gateway refuses to start, so no metrics are scraped and the alert can never fire | §11.7, §16.5 |
| 2 | OBS-043 | High     | `lenny_task_reuse_count` typed as Gauge in §16.1 but used as "histogram (p50)" in §4.6.2 — incompatible semantics | §4.6.2, §16.1 |
| 3 | OBS-044 | Medium   | `PgBouncerAllReplicasDown` placed in the Warning alerts table section but carries Critical severity | §16.5 |
| 4 | OBS-045 | Medium   | Gateway deployment bullet still lists `active sessions` as an HPA metric, contradicting the canonical table's "Alert only" designation (residual from OBS-039 partial fix) | §4.1 |

---

## Findings

### OBS-042 `AuditSIEMNotConfigured` Critical Variant Is Self-Defeating [High]

**Section:** §11.7, §16.5

**Problem:** The `AuditSIEMNotConfigured` warning alert (§16.5) states:

> "Severity: **Critical** when any active tenant has `complianceProfile` in `{soc2, fedramp, hipaa}` (gateway startup is also blocked — see Section 11.7)"

Section 11.7 confirms this: "In production mode (`LENNY_ENV=production`) with any tenant whose `complianceProfile` is regulated (`soc2`, `fedramp`, `hipaa`): the gateway **refuses to start** with a fatal error."

When the gateway refuses to start, it never reaches a running state, never exposes its Prometheus metrics endpoint, and is never scraped. The alert expression that would fire `AuditSIEMNotConfigured` at Critical severity depends on a metric (or flag) that the gateway would emit — but the gateway process exits before emitting anything. The Critical-severity variant of this alert can therefore never fire: the exact condition that would set it to Critical (regulated tenant + no SIEM) is the same condition that prevents the gateway from running at all.

This is an architectural self-contradiction. The alert description tells operators to rely on this alert as a signal for the Critical condition, but the signal is impossible to generate.

**Impact:** Operators who depend on alerting to detect the "regulated tenant without SIEM" misconfiguration will receive no alert. The failure mode is silent: the gateway crashes at startup and no Prometheus alert fires. Operators must rely on startup log scraping or external process monitoring — neither of which is specified.

**Recommendation:** Remove the Critical severity clause from the `AuditSIEMNotConfigured` alert description. The Critical condition (regulated tenant + no SIEM) is enforced at startup as a fatal error — it does not need a Prometheus alert because the process never runs. Instead:
1. Document that operators should monitor gateway process exit codes and startup logs (via Kubernetes `CrashLoopBackOff` detection or a separate healthcheck probe) to catch this condition.
2. Optionally add a separate preflight check alert that fires based on a Kubernetes event or a liveness probe failure, not a Prometheus metric emitted by the gateway process itself.

The Warning variant (all tenants `complianceProfile: none`, gateway running) is architecturally valid and should be retained.

---

### OBS-043 `lenny_task_reuse_count` Type Incompatible with Its Usage in §4.6.2 [High]

**Section:** §4.6.2, §16.1

**Problem:** The metrics table in §16.1 defines `lenny_task_reuse_count` as:

> "Task-mode pod reuse count (`lenny_task_reuse_count`, labeled by `pool`, `pod_name` — number of tasks executed on a single pod in task mode; used to track recycling efficiency and enforce `maxTasksPerPod` retirement) | **Gauge**"

The scaling formula section (§4.6.2) then says:

> "For variable workloads where early retirement is common, use observed `lenny_task_reuse_count` **histogram (p50)** rather than `maxTasksPerPod` as the estimate."

A Gauge metric is a single scalar value per label combination. It has no histogram buckets and cannot produce percentile values. The p50 (median) of a distribution requires a Histogram type in Prometheus — the `_bucket`, `_count`, and `_sum` series that enable `histogram_quantile()` PromQL queries.

The two sections assign fundamentally incompatible semantics to the same metric name:
- §16.1 (Gauge, per-pod): tracks the current reuse count for a single named pod, labeled by `pod_name`. This produces one time series per pod.
- §4.6.2 (Histogram, p50): computes the distribution of reuse counts across all pods in a pool to determine a median. This requires a Histogram without a `pod_name` label (or aggregated across pods).

These are different instruments serving different purposes. A single Gauge labeled by `pod_name` cannot answer "what is the p50 reuse count across all pods in this pool?" without external Prometheus aggregation (which the spec does not define).

**Impact:** The PoolScalingController formula in §4.6.2 cannot be implemented as specified. Either it computes `avg(lenny_task_reuse_count)` across pod label values (which gives an average, not a p50) or it requires a separate Histogram metric that §16.1 does not define. Any implementation will silently use a different statistic than the "p50" the formula specifies.

**Recommendation:** Resolve the semantic split into two distinct metrics:
1. Retain `lenny_task_reuse_count` (Gauge, labeled by `pool`, `pod_name`) for per-pod retirement enforcement — the §16.1 definition is correct for this purpose.
2. Add a separate `lenny_task_reuse_count_histogram` (Histogram, labeled by `pool`, `runtime_class`, no `pod_name`) that observes the reuse count at pod retirement time. This Histogram supports `histogram_quantile(0.5, ...)` for the §4.6.2 formula.

Update §4.6.2 to reference `lenny_task_reuse_count_histogram` p50, and update §16.1 to include the new Histogram alongside the existing Gauge.

---

### OBS-044 `PgBouncerAllReplicasDown` Misplaced in Warning Alerts Table with Critical Severity [Medium]

**Section:** §16.5

**Problem:** The §16.5 alert tables are divided into two sections with distinct headers: "Critical alerts (page)" and "Warning alerts". `PgBouncerAllReplicasDown` appears in the body of the "Warning alerts" table but its Severity column reads **Critical**:

> | `PgBouncerAllReplicasDown` | All PgBouncer pods in `lenny-system` have zero ready replicas ... | **Critical** |

This is structurally inconsistent. An implementor parsing the tables section-by-section (e.g., generating alerting rules from the Critical or Warning headers) will either:
- Encounter a "Critical" alert in the Warning section and be unsure which takes precedence, or
- Miss it entirely if their tooling groups by section header rather than the severity column.

**Impact:** Alert routing configurations that key off the section heading will miscategorize this as a Warning, meaning a condition that renders Postgres unreachable for all gateway components ("session creation and state writes will fail immediately") will not page on-call engineers.

**Recommendation:** Move `PgBouncerAllReplicasDown` from the Warning alerts table to the Critical alerts table, alongside `SessionStoreUnavailable` and `PostgresReplicationLag`. Its Critical severity is consistent with those entries.

---

### OBS-045 Gateway Deployment Bullet Still Lists `active sessions` as an HPA Metric [Medium]

**Section:** §4.1

**Problem:** The gateway deployment properties list (§4.1) states:

> "HPA on CPU, memory, **active sessions**, open streams, active LLM proxy connections (`active sessions` is sourced from the gateway's in-memory Prometheus gauge `lenny_gateway_active_sessions`, surfaced to the HPA via Prometheus Adapter as described in Section 10.1)"

The canonical HPA metric role table in the same section explicitly prohibits this:

> | `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` | **Capacity ceiling alert, not an HPA trigger.** ... Must NOT be used as the sole HPA trigger ... | **Alert only** |

The "Where used" column of the canonical table says **"Alert only"** — it does not say "HPA/KEDA ScaledObject" as the other two metrics do. Despite this, the deployment bullet both names `active sessions` as an HPA target and describes how it is surfaced to the HPA via Prometheus Adapter, directly contradicting the canonical table.

This is a residual instance of OBS-039. The OBS-039 fix corrected step 4 of the capacity budget calibration methodology but did not update the gateway deployment bullet at the top of §4.1 that still says `active sessions` is an HPA metric.

**Impact:** Implementors reading the deployment bullet will configure `lenny_gateway_active_sessions` as an HPA metric. The canonical table says this metric lags real load (it measures Postgres-backed session count, not live goroutine pressure) and must not drive scale-out decisions. Using it as an HPA trigger would cause delayed scale-out during session spikes and premature scale-out during idle periods.

**Recommendation:** Update the deployment bullet to remove `active sessions` from the HPA list. The corrected line should read:

> "HPA on CPU, memory, open streams (`lenny_gateway_active_streams`), request queue depth (`lenny_gateway_request_queue_depth`), active LLM proxy connections; `lenny_gateway_active_sessions` is an alert-only signal — see canonical HPA metric role table below."

This aligns the deployment summary with the canonical table's designations.
