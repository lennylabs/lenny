# Iter6 — Perspective 12: Observability & Operational Monitoring

- **Perspective:** 12 — Observability & Operational Monitoring
- **Category prefix:** OBS
- **Scope:** `spec/16_observability.md` (catalog completeness, metric naming, PromQL well-formedness, audit/operational event payloads, alert severity calibration) + cross-spec callers (`spec/11_policy-and-controls.md`, `spec/12_storage-architecture.md`, `spec/17_deployment-topology.md`) + docs synchronisation (`docs/reference/metrics.md`, `docs/operator-guide/observability.md`, `docs/runbooks/*`).
- **Prior iter context:** iter5/p12_observability.md (OBS-031..036) all confirmed Fixed in this iteration (see verification table below). Iter5 cross-perspective carries (STO-017 `QuotaFailOpenUserFractionInoperative`, STO-020 legal-hold quota metrics, STO-021 T4 KMS probe, FMR-018 `QuotaFailOpenCumulativeThreshold`, POL-026 `circuit_breaker.state_changed`, EXP-023 `experiment.variant_weaker_than_tenant_floor`, CMP-054/057/058 alerts) verified present or flagged below.
- **Severity rubric:** iter1-iter5 baseline per `feedback_severity_calibration_iter5.md`. Catalog-omission / PromQL-well-formedness / docs-sync defects that do not alter runtime behaviour but block operator reliability are Medium by precedent (compare OBS-031, OBS-033, OBS-034).

---

## Iter5 fix verification

| Iter5 finding | Claim | Verification | Status |
|---|---|---|---|
| OBS-031 (Medium) | `MinIOArtifactReplicationLagCritical` split from warning row into its own Critical row | `spec/16_observability.md:541` shows the Critical row is now separate | Fixed |
| OBS-032 (Medium) | `RedisUnavailable` / `MinIOUnavailable` Critical alerts added to §16.5 | `spec/16_observability.md:403` (`RedisUnavailable`), `:405` (`MinIOUnavailable`) | Fixed |
| OBS-033 (Medium) | `T4KmsKeyUnusable` alert catalogued in §16.5 with PromQL-resolvable expression | `spec/16_observability.md:406` present; expression uses `lenny_t4_kms_probe_result_total{outcome="failure"}` and `lenny_t4_kms_probe_last_success_timestamp` — both backing metrics exist at §16.1 lines 228-229 | Fixed |
| OBS-034 (Medium) | `LegalHoldCheckpointAccumulationProjectedBreach` alert catalogued with backing metrics | `spec/16_observability.md:484` row present; `lenny_tenant_legal_hold_active_count` (§16.1 :201), `lenny_legal_hold_checkpoint_projected_growth_bytes` (§16.1 :202) present | Fixed (with new PromQL defect, see OBS-038 below) |
| OBS-035 (Medium) | `MemoryStoreGrowthHigh` / `MemoryStoreErasureDurationHigh` catalogued | `spec/16_observability.md:479-480` both present with PromQL-resolvable expressions | Fixed |
| OBS-036 (Medium) | `ExperimentIsolationRejections` warning alert catalogued | `spec/16_observability.md:532` present | Fixed |

| Cross-perspective carry (iter5) | Verification | Status |
|---|---|---|
| FMR-018: `QuotaFailOpenCumulativeThreshold` alert | `spec/16_observability.md:447` present | Fixed |
| POL-026: `circuit_breaker.state_changed` audit event catalogued | `spec/16_observability.md:655` present | Fixed |
| EXP-023: `experiment.variant_weaker_than_tenant_floor` operational event catalogued | `spec/16_observability.md:640` present | Fixed |
| CMP-054: per-region legal-hold escrow replication metrics/alert | `MinIOArtifactReplicationLagCritical` catalogues escrow bucket label; verified in §16.5 :541 | Fixed |
| CMP-057: `CompliancePostureDecommissioned` audit-driven alert | `spec/16_observability.md:483` present | Fixed |
| CMP-058: platform-tenant audit event residency metric/alert | `lenny_platform_audit_event_egress_total` + `PlatformAuditEventForeignResidencyBreach` alert wired to §16.5 (search by metric name confirms) | Fixed |
| STO-017 user-fraction warning: treated as alert in docs but as startup log warning in §12.4 | **Inconsistency persists — new finding OBS-037 below** | Open |
| STO-020 legal-hold quota backing metrics + alert | Present; however, PromQL expression references a non-metric config value — **new finding OBS-038 below** | Open (refinement) |
| STO-021 T4 KMS probe metrics + alert | Present; backing metrics resolvable | Fixed |

---

## New findings

### OBS-037 (Medium) — `QuotaFailOpenUserFractionInoperative` is an alert in docs, a startup log warning in the spec, and absent from §16.5

**Status: Fixed** — Chose Option 1 (real alert). Added `lenny_gateway_quota_user_failopen_fraction` gauge to `spec/16_observability.md` §16.1 and `docs/reference/metrics.md`; catalogued `QuotaFailOpenUserFractionInoperative` alert in §16.5 with PromQL `max(lenny_gateway_quota_user_failopen_fraction) >= 0.5`; reconciled the log+alert language in `spec/12_storage-architecture.md:222` and docs surfaces.

**Observation.** `spec/12_storage-architecture.md:222` describes the signal as:

> `lenny-ops` emits the `QuotaFailOpenUserFractionInoperative` warning at startup whenever `quotaUserFailOpenFraction >= 0.5`

That wording ("warning at startup") reads as a startup-time log entry, not a Prometheus alerting rule. Consistent with that reading, §16.5 of the spec catalogues no `QuotaFailOpenUserFractionInoperative` alert (`grep` returns zero matches in `spec/16_observability.md`), and §16.1 does not define any backing metric (for example, `lenny_quota_failopen_user_fraction_inoperative` or a config-validation gauge).

But three downstream docs present it as a Warning-severity alert, side-by-side with normal §16.5 alerts:

- `docs/reference/metrics.md:489` (alerts table row): `| QuotaFailOpenUserFractionInoperative | Emitted at gateway startup when quotaUserFailOpenFraction >= 0.5 … | Warning |`
- `docs/operator-guide/observability.md:189` (alerts table row): `| QuotaFailOpenUserFractionInoperative | Gateway startup warning emitted when quotaUserFailOpenFraction >= 0.5 (default 0.25) | …`
- `docs/operator-guide/configuration.md:485`: `The gateway logs a startup warning and raises the QuotaFailOpenUserFractionInoperative alert when quotaUserFailOpenFraction >= 0.5`

So the docs instruct operators that `QuotaFailOpenUserFractionInoperative` is an alert that will fire through their Prometheus/Alertmanager pipeline, but the spec defines no such alerting rule and no backing metric it could evaluate against. Either:

- (a) it is a real alert, in which case §16.5 must catalogue it (with backing metric and PromQL expression), `spec/25_*` and the bundled PrometheusRule export must ship it, and the docs are correct but the spec has a catalog-omission gap; **or**
- (b) it is only a startup log warning, in which case the docs are mis-labelling a log message as an alert and operators will configure alertmanager expecting a firing rule that will never arrive.

**Impact (Medium).** Operator reliability defect, same class as the original OBS-031/OBS-033/OBS-034 catalog-omission findings. Documented alert without a backing rule fails to fire when the bad configuration is live; or, if the intent is a log-only warning, operators discover the discrepancy only when post-incident review reveals the missing alert. Either resolution is low-complexity but must be picked and reconciled across spec + docs + bundled rules.

**Recommendation.** Choose one, and synchronize the three surfaces (spec §16.5, `docs/reference/metrics.md`, `docs/operator-guide/observability.md`, `docs/operator-guide/configuration.md`, plus the bundled PrometheusRule per §25.13):

1. **Make it a real alert.** Add to §16.1 a config-validation gauge such as `lenny_gateway_quota_user_failopen_fraction` (Gauge, labeled by `service_instance_id`), emitted once at startup and on config reload. Add to §16.5:

   ```
   | QuotaFailOpenUserFractionInoperative | max(lenny_gateway_quota_user_failopen_fraction) >= 0.5 | Warning |
   ```

   The alert then fires for the lifetime of any replica configured with the weakened setting, and operator acknowledgement must explicitly silence it. Update `spec/12_storage-architecture.md:222` to say "emits the `QuotaFailOpenUserFractionInoperative` warning (log + alert; see §16.5)".

2. **Make it log-only.** Rewrite the three docs rows to describe it as a startup log-line, not an alerting rule. Remove the severity column entirely or mark it as "Log-only (no alerting rule)". Update `docs/operator-guide/observability.md:189` and `docs/reference/metrics.md:489` to a separate subsection ("Configuration validation log warnings") so operators do not confuse it with real alerts.

Option 1 is recommended — it parallels the treatment of the other fail-open controls (`QuotaFailOpenCumulativeThreshold` at §16.5 :447) and preserves the operator's single-pane Prometheus contract.

### OBS-038 (Medium) — `LegalHoldCheckpointAccumulationProjectedBreach` PromQL uses `storageQuotaBytes` as a bare identifier, which is a config value, not a metric

**Status: Fixed** — Chose Option 1 (backing gauge). Added `lenny_storage_quota_bytes_limit{tenant_id}` to `spec/16_observability.md` §16.1 and `docs/reference/metrics.md`; rewrote alert expression in §16.5 and docs to `(lenny_storage_quota_bytes_used + sum by (tenant_id)(lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_storage_quota_bytes_limit`.

**Observation.** `spec/16_observability.md:484` defines the alert expression as:

> `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * storageQuotaBytes`

`storageQuotaBytes` is the Helm / admin-API configuration field (see `spec/12_storage-architecture.md:328`, `spec/12_storage-architecture.md:737`, etc.) — it is **not** a metric and is not published to Prometheus. PromQL cannot resolve a bare identifier that is not a metric name or a parameter binding, so this expression is not evaluable by Prometheus as written.

Compare `StorageQuotaHigh` at `spec/16_observability.md:456`, which expresses the same relationship in prose ("per-tenant artifact storage (`lenny_storage_quota_bytes_used`) exceeds 80% of the tenant's `storageQuotaBytes`") — the spec implicitly assumes the implementation supplies the ratio via a pre-computed metric or external recording rule. The `LegalHoldCheckpointAccumulationProjectedBreach` row attempts to encode the full expression literally, but stops one step short and leaks the config symbol into the PromQL text.

`docs/reference/metrics.md:507` copies the same pattern: `lenny_legal_hold_checkpoint_projected_growth_bytes / (storageQuotaBytes - lenny_storage_quota_bytes_used) > 0.9`. Same defect in both surfaces.

No backing metric exists. `grep` for `lenny_storage_quota_bytes_limit`, `lenny_tenant_storage_quota`, `lenny_storage_quota_limit` in the entire repository returns zero matches.

**Impact (Medium).** PromQL well-formedness defect, mirror of OBS-033 (which required a backing metric rather than a bare expression). Any downstream system attempting to render the alert (bundled PrometheusRule export per §25.13, Helm rendering, operator import into their alertmanager) will either fail to parse, or will be forced to template-substitute the config value into the rule at chart-render time — a templating pattern the spec does not define anywhere.

**Recommendation.** Two deterministic fixes, either acceptable:

1. **Preferred: add a backing gauge.** Add to §16.1 a metric such as `lenny_storage_quota_bytes_limit` (Gauge, labeled by `tenant_id`, emitted from the tenant registry cache with the currently-configured per-tenant `storageQuotaBytes`). Rewrite the alert expression to:

   ```
   (lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_storage_quota_bytes_limit
   ```

   Update `docs/reference/metrics.md:507` to match. This gauge also cleanly unblocks re-stating `StorageQuotaHigh` as a PromQL expression (`lenny_storage_quota_bytes_used / lenny_storage_quota_bytes_limit > 0.8`), parallel to how `CheckpointStorageHigh` is now expressed.

2. **Alternative: prose-only.** Rewrite the §16.5 row body to the `StorageQuotaHigh` prose form (e.g., "Projected tenant artifact usage under active legal hold exceeds 90% of the tenant's `storageQuotaBytes`"), and let the bundled PrometheusRule supply the literal threshold via Helm template substitution — but this then requires a templating contract to be added to §25.13 (spec currently has none), so (1) is the cheaper fix.

Either way, synchronize `spec/16_observability.md:484`, `docs/reference/metrics.md:507`, and `docs/operator-guide/observability.md` (if the expression surfaces there), and note the added metric in `docs/reference/metrics.md` §Metrics.

---

## Convergence assessment

**Converged: No.** Two new Medium findings remain (OBS-037 catalog-sync, OBS-038 PromQL well-formedness). Both are low-complexity spot fixes in the same class as iter5 OBS-031/OBS-033/OBS-034 (all of which resolved in a single iteration). No Critical or High findings in iter6 for this perspective. Iter5 OBS-031..036 all verified Fixed; cross-perspective dependents (FMR-018, POL-026, EXP-023, CMP-054/057/058, STO-021) all verified present and well-formed.

- Residual risk: If OBS-037 is left unresolved, operators will wire alertmanager rules for an alert the platform never emits. If OBS-038 is left unresolved, the bundled PrometheusRule export (§25.13) will either fail to render or ship an unevaluable rule. Both are blocking for §25 bundled-rules convergence.
- Expectation: resolvable in iter7 with a one-line spec addition in each case (new gauge metric + rewritten PromQL for OBS-038; §16.5 row + docs reconciliation for OBS-037).

---

## Report

```
PERSPECTIVE: 12
CATEGORY: OBS
NEW FINDINGS: 2
FILE: /Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter6/p12_observability.md
CONVERGED: No
```
