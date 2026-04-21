# Iter6 Review — Perspective 20: Failure Modes & Resilience Engineering

**Scope.** Verified iter5 fixes for FMR-018 and FMR-019 end-to-end (§12.4 ↔ §16.1 / §16.5 / §16.7 cross-refs; PromQL expr well-formedness against §16.5 convention). Re-examined §10 gateway resilience, §11.6 circuit breakers, §12.4 Redis failure semantics, §12.3 Postgres HA, §16.5 alert coverage, and §17.7 runbook triggers for NEW cascading-failure gaps, missing recovery paths, or severity-anchored carry-forwards from the iter3/iter4/iter5 baseline.

**Iter5-fix verification.** Both iter5 findings are confirmed fixed in `spec/`:

- **FMR-018 (Medium) — FIXED.** All three artifacts are present and cross-referenced end-to-end:
  1. Metric `lenny_quota_failopen_cumulative_seconds` defined in `spec/16_observability.md:48` with `service_instance_id` label, rolling 1-hour sliding-window semantics, `/run/lenny/failopen-cumulative.json` persistence note, and cross-refs to §12.4, §16.5, and §16.7.
  2. Alert `QuotaFailOpenCumulativeThreshold` defined in `spec/16_observability.md:447` (between `RateLimitDegraded` at 446 and `CertExpiryImminent` at 448 as recommended in iter5), Warning severity, with the PromQL expression `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * quotaFailOpenCumulativeMaxSeconds` sustained for > 60s. The expression uses a config-value symbolic reference to `quotaFailOpenCumulativeMaxSeconds` — consistent with the §16.5 convention already applied at line 484 (`LegalHoldCheckpointAccumulationProjectedBreach` uses `> 0.9 * storageQuotaBytes`), deployer substitutes at chart-render time. The alert body references `RedisUnavailable`, `quota_failopen_started`, and `§12.4` per the iter5 recommendation. PromQL is well-formed (valid range-vector label-matcher; `max by (...)` aggregator correctly scoped to `service_instance_id`).
  3. Audit event `quota_failopen_started` defined in `spec/16_observability.md:656` with `tenant_id`, `service_instance_id`, `timestamp` payload, cross-ref to §12.4 Cumulative fail-open timer and to `RateLimitDegraded` / `QuotaFailOpenCumulativeThreshold`.
  4. §12.4 line 224 updated to link all three artifacts by anchor (`[§16.7](16_observability.md#167-section-25-audit-events)`, `[§16.1](16_observability.md#161-metrics)`, `[§16.5](16_observability.md#165-alerting-rules-and-slos)`).

- **FMR-019 (Low) — FIXED (subsumed by OBS-032 per iter5 summary).** `MinIOUnavailable` critical alert defined in `spec/16_observability.md:405` with expression `rate(lenny_artifact_upload_error_total{error_type="minio_unreachable"}[2m]) > 0` sustained > 1 minute; metric `lenny_artifact_upload_error_total` defined at line 249 with the `minio_unreachable` label value enumerated. §17.7 runbook trigger at `spec/17_deployment-topology.md:762` correctly names the alert and the metric. Paired alerts (`WorkspaceSealStuck`, `CheckpointStorageUnavailable`) are noted in the same trigger line. The fix went beyond the iter5 recommendation — the alert is explicitly Critical (not Warning) and drives the existing MinIO-failure runbook.

**Carry-forward posture.** Severity held at iter5 levels per the severity-calibration rule; re-verified unresolved in current spec:

- **FLR-014** `InboxDrainFailure` alert at `spec/16_observability.md:505` still carries prose (`"lenny_inbox_drain_failure_total incremented (any non-zero increase over a 5-minute window)"`) instead of an `expr:` PromQL field; fourth iteration flagged. Severity held at Low.
- **FLR-015** PgBouncer readiness probe at `spec/12_storage-architecture.md:45` still `periodSeconds: 5, failureThreshold: 2, timeoutSeconds: 3`; no "Known limitation" amplification note added. Severity held at Low.
- **FLR-016** `Minimum healthy gateway replicas (alert)` table row at `spec/17_deployment-topology.md:913` still has no dedicated rule in §16.5 using `lenny_gateway_replica_count` directly. The thematically-closest `GatewayNoHealthyReplicas` critical alert at `spec/16_observability.md:401` fires when "Healthy gateway replicas below tier minimum" and the §17.7 runbook at `spec/17_deployment-topology.md:752` does bridge the two via `lenny_gateway_healthy_replicas`, so the operator mapping is resolvable in practice. Severity held at Low (marginal — operators can cross-walk).
- **FLR-017** `Gateway preStop drain timeout` row at `spec/17_deployment-topology.md:910` (60s / 60s / 120s) still does not correspond to any parameter in the §10.1 preStop logic formula at `spec/10_gateway-internals.md:119` (`max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30`). The 120s Tier 3 value cannot be reconstructed from the three summand defaults (90s + 90s + 30s = 210s); the row appears to describe only the Stage 3 stream-drain portion of the preStop, not the full tiered-cap budget. No inline cross-reference is added. Severity held at Low.

These carry-forwards remain the baseline against which new findings are calibrated (FLR-014/016/017 are "alert/table row referenced but not defined / not traceable to a mechanism" — Low). The one new iter6 finding below is scored against that rubric.

---

## New findings (iter6)

### FMR-020. `QuotaFailOpenUserFractionInoperative` startup warning defined in §12.4 is not listed as an alert rule in §16.5 [Low]

**Section:** `spec/12_storage-architecture.md:222` (Per-user fail-open ceiling, Config-time invariant check); `spec/16_observability.md` §16.5 Alerting rules (lines 386–520).

§12.4 line 222 specifies a `lenny-ops` startup warning with a fixed name:

> `lenny-ops` emits the `QuotaFailOpenUserFractionInoperative` warning at startup whenever `quotaUserFailOpenFraction >= 0.5` — at that setting the per-user cap is at or above half the tenant's per-replica fail-open budget, so the monopolization-prevention intent of the control is substantially weakened even though the formula itself remains correct. Operators who intentionally raise the fraction above 0.5 should acknowledge the weakened posture in their deployment answer file.

Grep of `spec/16_observability.md` for `QuotaFailOpenUserFractionInoperative` returns zero matches.

This is a pure symmetric omission to the FLR-014 / iter5 FMR-018 / iter5 FMR-019 pattern — a named operator-facing alert whose trigger semantics are defined in a normative prose paragraph, but which has no backing row in the §16.5 alert catalog (the single source of truth for `PrometheusRule` rendering per §16.5 opening paragraph). The comparable precedent is `AuditRetentionLow` at §16.5 line 491 — another startup-emitted warning (`"Fires at startup"`) that IS present in §16.5 as a Warning row. `AuditSIEMNotConfigured` at §16.5 line 452 follows the same pattern. By those precedents, `QuotaFailOpenUserFractionInoperative` should appear in §16.5 with a Warning severity row.

Operational impact: without the §16.5 row, a deployer who intentionally raises `quotaUserFailOpenFraction >= 0.5` will see the one-time startup log line but will not see the alert in `PrometheusRule` rollups, dashboards, or the operator-guide `docs/reference/metrics.md` alert table (which is generated from §16.5 per the iter5 docs-sync rule). The control's intended "operator awareness of weakened posture" is therefore only half-wired: observable at boot, invisible in steady-state alerting. This is distinct from (a) FMR-018 (which had no observability surface at all), and (b) FLR-014 (which has the §16.5 row but with prose instead of `expr:`) — it sits between the two and most closely mirrors FMR-019/OBS-032 (alert named in prose but not defined in §16.5). That class of finding has held at Low across iter4 (FLR-016) and iter5 (FMR-019).

**Recommendation:** Add a row to `spec/16_observability.md` §16.5 Warning Alerts table, either near `AuditRetentionLow` (line 491) or near `AuditSIEMNotConfigured` (line 452) — both are startup-emitted config-validation warnings and are the closest catalog neighbours:

```
| `QuotaFailOpenUserFractionInoperative` | `lenny_gateway_quota_user_failopen_fraction >= 0.5` on any replica. Emitted by `lenny-ops` at gateway startup when `quotaUserFailOpenFraction >= 0.5` — at that setting the per-user fail-open cap is at or above half the tenant's per-replica fail-open budget, so the monopolization-prevention intent of the per-user control is substantially weakened. The formula `per_user_failopen_ceiling = effective_ceiling * userFailOpenFraction` remains correct; the alert exists to surface operator intent and ensure it is acknowledged in the deployment answer file. Correlate with `QuotaFailOpenCumulativeThreshold` and `RateLimitDegraded` during a Redis outage if a single user appears to be consuming a disproportionate share of the tenant fail-open allocation. See [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) Per-user fail-open ceiling. | Warning |
```

Adding a supporting gauge `lenny_gateway_quota_user_failopen_fraction` (gauge, labeled by `service_instance_id`, exposing the tenant-default `quotaUserFailOpenFraction` at replica startup) in §16.1 would let the alert condition be expressed as a steady-state Prometheus rule rather than a one-shot startup log; alternatively, the row can be expressed as a config-assertion alert in the same style as `AuditSIEMNotConfigured` without a backing metric, noting `"Emitted at startup; persists until config is changed"` in the condition body. Either shape matches an existing §16.5 precedent.

Severity Low — this is a cosmetic/operability gap (the control itself functions correctly regardless of the missing §16.5 row; only the steady-state alerting view is incomplete). Calibrated to iter5 FMR-019 (Low) and iter4 FLR-016 (Low), both of which were "named alert/table row referenced but not defined" gaps of equivalent blast radius.

---

## Convergence assessment

**Direction:** Converging. Iter6 surfaces only 1 new Low finding (FMR-020) and 4 iter3/iter4/iter5 carry-forwards at Low. No Critical, High, or Medium cascading-failure gap was identified.

The high-impact recovery paths remain fully specified with metrics, alerts, audit events, and explicit fail-closed / fail-open semantics:
- Coordinator handoff and preStop tiered checkpoint cap (§10.1, `lenny_prestop_cap_selection_total`, `PreStopCapFallbackRateHigh`)
- Dual-store degraded mode (§10.1, `DualStoreUnavailable`, `lenny_dual_store_unavailable`)
- Circuit breaker cache-only admission posture and stale-serve telemetry (§11.6, `CircuitBreakerStale`, `admission.circuit_breaker_cache_stale`)
- Quota fail-open per-user / per-tenant ceiling, cumulative timer, and 80% pre-breach warning (§12.4 + iter5 fix → §16.1 / §16.5 / §16.7)
- MinIO primary unavailability and workspace-seal stuck signals (§12.5 + iter5 fix → §16.5 `MinIOUnavailable`, `WorkspaceSealStuck`, `CheckpointStorageUnavailable`)
- Delegation budget irrecoverable path (§8.3, `DelegationBudgetIrrecoverable`, `BUDGET_STATE_UNRECOVERABLE`)
- Partial checkpoint intent-row-first ordering (§10.1, `lenny_checkpoint_partial_manifests_superseded_total`, §12.5 GC rule 6 backstop)

**Remaining work to close the perspective:**

1. Fix the four iter3/iter4/iter5 carry-forwards (FLR-014 / 015 / 016 / 017) — each is a small, well-scoped polish-grade change.
2. Fix FMR-020 by adding the §16.5 row (and optionally the supporting gauge in §16.1).

**Blocker for convergence declaration:** None. FMR-020 and the four Low carry-forwards are all polish-grade; none leaves a recovery path unspecified or a documented control without its observability surface. The perspective can be declared **converged** for iter6 — the remaining work is closable in a single trivial sweep.

**Convergence:** Yes.
