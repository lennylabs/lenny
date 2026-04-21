# Iter5 Review — Perspective 20: Failure Modes & Resilience

**Scope.** Verified iter4 Failure Modes & Recovery findings (FLR-012…017) held or remained carried forward, and re-examined §10 gateway resilience, §11 circuit breakers, §12 storage failure semantics, and §16 alert coverage for NEW cascading-failure gaps or missing recovery paths.

**Carry-forward posture.** FLR-012 and FLR-013 are Fixed (symmetric-NULL 90s tier selection with `postgres_null` source label verified in `spec/10_gateway-internals.md:108,112,114` and `spec/16_observability.md:438`; Tier-3 node-drain note and anti-affinity recommendation verified in `spec/17_deployment-topology.md:908–916`). FLR-014/015/016/017 remain unresolved in the current spec and are documented below for continuity with the iter3/iter4 rubric (prior severity Low), not as fresh findings — severity is anchored to those earlier decisions per the severity-calibration rule. Two NEW findings are reported.

## Iter3/iter4 carry-forward (unresolved — severity held at iter4 levels)

- **FLR-014** `InboxDrainFailure` alert at `spec/16_observability.md:481` still carries prose (`"lenny_inbox_drain_failure_total incremented (any non-zero increase over a 5-minute window)"`) rather than an `expr:` PromQL field; third iteration this has been flagged. Severity held at Low.
- **FLR-015** PgBouncer readiness probe at `spec/12_storage-architecture.md:45` still `periodSeconds: 5, failureThreshold: 2, timeoutSeconds: 3`; no "Known limitation" amplification note added. Severity held at Low.
- **FLR-016** `Minimum healthy gateway replicas (alert)` table row at `spec/17_deployment-topology.md:904` still has no backing rule in §16.5 (no `GatewayReplicasBelowMinimum`, `GatewayAvailabilityLow`, or equivalent using `lenny_gateway_replica_count`). Severity held at Low.
- **FLR-017** `Gateway preStop drain timeout` row at `spec/17_deployment-topology.md:901` (60s / 60s / 120s) still does not correspond to any parameter in the §10.1 preStop logic formula `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30`. Severity held at Low.

These carry-forwards are the baseline against which new findings are calibrated (FLR-016/017 are "alert/table row referenced but not defined / not traceable to a mechanism" — Low). The new findings below are scored against that rubric.

---

## New findings (iter5)

### FMR-018. `QuotaFailOpenCumulativeThreshold` alert, `quota_failopen_cumulative_seconds` gauge, and `quota_failopen_started` audit event referenced in §12.4 are not defined anywhere [Medium]

**Section:** `spec/12_storage-architecture.md:224` (Per-tenant fail-open budget enforcement, Cumulative fail-open timer); `spec/16_observability.md` §16.1 Metrics (lines 7–260), §16.5 Alerting rules (lines 386–520), §16.7 Audit events

§12.4 line 224 specifies a complete financial-security control for the cumulative fail-open timer:

- A **gauge** `quota_failopen_cumulative_seconds` the gateway emits, reflecting the sliding-window cumulative seconds spent in fail-open across Redis outages in any rolling 1-hour window.
- An **alert** `QuotaFailOpenCumulativeThreshold` that "fires when the cumulative timer exceeds 80% of the configured maximum" — i.e., the pre-breach warning on the `quotaFailOpenCumulativeMaxSeconds` (default 300s) ceiling.
- An **audit event** `quota_failopen_started` emitted on each fail-open entry, carrying `tenant_id`, `service_instance_id`, and `timestamp`, described as enabling "billing consumers to detect and attribute overshoot windows".

None of these three artifacts are defined in §16:

- Grep of `spec/16_observability.md` for `quota_failopen_cumulative` returns zero matches. The §16.1 metric catalogue (which is declared the canonical metric registry — see the §16.1 heading and the catalogue-completeness CI gate referenced elsewhere in the spec) has no row for the gauge.
- Grep for `QuotaFailOpenCumulativeThreshold` in `spec/16_observability.md` §16.5 alerting table (lines 386–520) returns zero matches. The only adjacent alert is `RateLimitDegraded` at line 427, which only covers *active* fail-open state, not the cumulative pre-breach warning this alert is meant to provide.
- Grep for `quota_failopen_started` in §16.7 audit event catalog returns zero matches.

This is materially different from FLR-014/016/017 (polish-grade table/PromQL gaps). `quotaFailOpenCumulativeMaxSeconds` is explicitly documented as a **financial security control** with a default of 300s tuned per "the maximum acceptable quota overshoot window". When the control trips, the replica transitions to fail-closed for quota enforcement — a user-visible availability event. The absence of the 80% pre-breach alert means operators receive NO warning that cumulative exposure is approaching the configured ceiling; the first signal they receive is that quota enforcement has flipped to fail-closed (an availability regression). The missing `quota_failopen_started` audit event means billing consumers cannot attribute overshoot windows to specific (tenant, replica, timestamp) tuples as §12.4 promises. The missing gauge prevents custom dashboards or deployer-authored alerts from observing the condition at all — the value lives only in the gateway's in-memory state and the `/run/lenny/failopen-cumulative.json` file on each replica's node.

This is a missing recovery path for a common failure (Redis outage — the scenario for which the cumulative timer was introduced). Severity Medium, consistent with iter4 FLR-012 ("High — unmitigated common-drain failure") and the iter3/iter4 pattern of Medium for missing per-finding observability surface on a common failure mode.

**Recommendation:** Add three artifacts to §16, cross-referenced from §12.4 line 224:

1. **§16.1 Metrics** — add a row:

   ```
   | Quota fail-open cumulative seconds (`lenny_quota_failopen_cumulative_seconds`, gauge labeled by `service_instance_id` — sliding-window cumulative seconds spent in fail-open across Redis outages in the current rolling 1-hour window; resets on each rolling-window advance; persisted across replica restarts via `/run/lenny/failopen-cumulative.json` — see [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) Cumulative fail-open timer) | Gauge |
   ```

2. **§16.5 Alerting rules** — add a row between `RateLimitDegraded` (line 427) and `CertExpiryImminent`:

   ```
   | `QuotaFailOpenCumulativeThreshold` | `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * quotaFailOpenCumulativeMaxSeconds` sustained for > 60 s on any replica. Pre-breach warning that the cumulative fail-open timer is approaching the `quotaFailOpenCumulativeMaxSeconds` financial-security ceiling (default 300s); at the ceiling the replica transitions to fail-closed for quota enforcement and new sessions/token-consuming requests are rejected until Redis recovers. Pair with a concurrent `RateLimitDegraded` or `DualStoreUnavailable` to identify the underlying cause. See [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) Cumulative fail-open timer. | Warning |
   ```

3. **§16.7 Audit events** — add a `quota_failopen_started` bullet with `tenant_id`, `service_instance_id`, `timestamp` payload fields, cross-referencing §12.4 ("enables billing consumers to attribute overshoot windows").

Update §12.4 line 224 to link each artifact by anchor (`[§16.1](16_observability.md#161-metrics)`, `[§16.5](16_observability.md#165-alerting-rules-and-slos)`, `[§16.7](16_observability.md#167-section-25-audit-events)`) so the control surface is traceable end-to-end.

---

### FMR-019. `MinIOUnavailable` alert referenced in §17.7 runbook trigger is not defined in §16.5 [Low]

**Section:** `spec/17_deployment-topology.md:760` ("MinIO failure" runbook); `spec/16_observability.md` §16.5 Alerting rules

The §17.7 operational runbook for "MinIO failure" (`docs/runbooks/minio-failure.md`) lists its trigger as:

> *Trigger:* `MinIOUnavailable` alert; workspace upload/download failures; `lenny_artifact_upload_error_total` spikes.

Grep of `spec/16_observability.md` for `MinIOUnavailable` returns zero matches. The MinIO-specific alerts that DO exist in §16.5 are `MinIOArtifactReplicationLagHigh` (line 515, RPO-tracking for cross-region replication) and `MinIOArtifactReplicationFailed` (line 516, object-level replication failures). Neither fires on primary-site MinIO unavailability — replication alerts fire while the primary is healthy but the DR target is degraded. `CheckpointStorageUnavailable` (line 390) is closest in spirit but is narrowly scoped to checkpoint-upload eviction failures, not the broader "workspace upload/download failures" the runbook implies.

Operators paging on `MinIOUnavailable` per the runbook wording will find no backing alert rule. The same pattern flagged as iter4 FLR-016 for `GatewayReplicasBelowMinimum` (table row references an alert not defined in the shipped `PrometheusRule`) — this is the artifact symmetric to that gap.

**Recommendation:** Either (a) add a `MinIOUnavailable` rule to §16.5 firing on sustained PUT/GET failures (e.g., `rate(lenny_artifact_store_errors_total[5m]) > 0.05 * rate(lenny_artifact_store_operations_total[5m])` sustained > 2 min, or a dedicated `lenny_artifact_store_reachability` gauge against a probe request); or (b) retitle the runbook trigger to name the existing `CheckpointStorageUnavailable` and add a secondary condition for non-checkpoint artifact-store errors; or (c) align terminology by adding the alert under a different name but re-pointing the runbook trigger line to it. Option (a) is preferred because a single consolidated "MinIO primary unreachable" signal is what an on-call operator would search for when the runbook mentions the name.

Severity Low, consistent with iter4 FLR-016 (table row references an alert not defined as a rule).

---

## Convergence assessment

**Direction:** Converging within Failure Modes & Resilience. Iter5 surfaces only 2 new findings (1 Medium, 1 Low), and 4 iter3/iter4 carry-forwards at Low. No Critical or High cascading-failure gap was identified — the high-impact recovery paths (preStop tiered cap, coordinator handoff, dual-store degraded mode, circuit breaker cache-only admission posture, quota fail-open per-user/per-tenant ceiling, delegation budget irrecoverable path) are all specified with metrics, alerts, audit events, and explicit fail-closed/fail-open semantics.

**Remaining work to close the perspective:**

1. Fix the four iter3/iter4 carry-forwards (FLR-014/015/016/017) — each is a small, well-scoped polish-grade change.
2. Fix FMR-018 by adding the three §16 artifacts (gauge row, alert row, audit event bullet).
3. Fix FMR-019 by aligning the §17.7 runbook trigger with an alert that exists (new or renamed).

**Blocker for convergence declaration:** FMR-018. The carry-forwards and FMR-019 are polish-grade and would not by themselves block a "perspective converged" declaration, but FMR-018 leaves a documented financial-security control without its required observability surface and should be closed before declaring convergence on this perspective.
