# Observability & Operational Monitoring Review — Iter 2

## Prior-finding verification

- **OBS-001 (lenny_checkpoint_duration_seconds undefined):** Fixed. Metric is now formally defined in §16.1 (line 30) with labels `pool`, `level`, `trigger`, scope "end-to-end checkpoint wall time from the initial quiescence request through snapshot upload complete", and explicit cross-references to `CheckpointDurationHigh`, `CheckpointDurationBurnRate`, and §16.6 SLO. Labels are consistent with the sibling `lenny_checkpoint_size_bytes` emitter.
- **OBS-004 (TTFT isolation_profile):** Fixed. `lenny_session_time_to_first_token_seconds` (line 15) now carries `isolation_profile`. The `TTFTBurnRate` alert (line 501) is unfiltered, which matches the global P95 < 10s SLO (line 480); no filter required.
- **OBS-005 (per-pool filtering on `PodClaimQueueSaturated`):** Fixed. Line 365 now specifies `Evaluated per pool (grouped by (pool))` with explicit `{pool="<p>"}` selectors.
- **OBS-002/003/006:** Partially fixed. Most callouts resolved, but several residual omissions remain — see new findings below.
- **PoolConfigValidatorUnavailable (K8S-035):** Present and properly formatted in §16.5 line 369 as a Warning alert with the 30s sustain window, `failurePolicy: Fail` rationale, and cross-reference to §4.6.3. No formatting regressions.

## Findings

### OBS-007 Alert-Referenced Metrics Missing From §16.1 Registry [High]

**Files:** `16_observability.md`

Multiple alerts in §16.5 reference metrics that never appear as first-class entries in the §16.1 metrics table. Because §16.1 is declared the canonical registry and §16.1.1 declares the attribute-naming table "single source of truth," any alert that consumes an unregistered metric breaks the discoverability contract:

- `GatewaySessionBudgetNearExhaustion` (line 351) uses `lenny_gateway_active_sessions` — not present in §16.1; the closest entry is "Active sessions (by runtime, pool, state, tenant)" (line 7) which has no metric identifier.
- `KMSSigningUnavailable` (line 366) uses `lenny_gateway_kms_signing_errors_total` — not in §16.1.
- `SDKConnectTimeout` (line 372) uses `lenny_warmpool_sdk_connect_timeout_total` — not in §16.1.
- `CRDSSAConflictStuck` (line 400) uses `lenny_crd_ssa_conflict_total` — not in §16.1.
- `DataResidencyViolationAttempt` (line 336) uses `lenny_data_residency_violation_total` — not in §16.1.
- `SessionEvictionTotalLoss` (line 338) uses `lenny_session_eviction_total_loss_total` — not in §16.1.
- `NetworkPolicyCIDRDrift` (line 331) uses `lenny_network_policy_cidr_drift_total` — not in §16.1.
- `BillingStreamEntryAgeHigh` (line 340) uses `lenny_billing_redis_stream_oldest_entry_age_seconds` — not in §16.1.

**Recommendation:** Add each metric name, type, and label set to §16.1 with a cross-reference to the alert. Either add a new sub-block (e.g., "Gateway Capacity", "KMS", "SDK Warm", "CRD Ownership", "Data Residency", "Network Drift") or inline each in the existing relevant block. These are regressions of OBS-002 for metrics introduced or renamed after iter1.

---

### OBS-008 Delegation Tree Size Metric Still Unnamed [High]

**Files:** `16_observability.md`

Line 25 still reads `Delegation tree size distribution | Histogram` with no metric name. The sibling on line 24 was correctly named `lenny_delegation_depth`; this entry was missed. Any alert or dashboard that wants to correlate tree breadth with depth cannot reference a stable identifier.

**Recommendation:** Assign a metric name (e.g., `lenny_delegation_tree_size`, histogram labeled by `pool` — observed at tree completion, counting total nodes in the completed tree) with a cross-reference to §8.

---

### OBS-009 Narrative-Only Metric Rows in §16.1 [Medium]

**Files:** `16_observability.md`

Several rows in §16.1 remain as prose descriptions without `lenny_*` identifiers:

- Line 7 "Active sessions (by runtime, pool, state, tenant)" — needed by OBS-007 above.
- Line 9 "Stale warm pods (idle beyond threshold, by pool)".
- Line 26 "Gateway replica count".
- Line 27 "Gateway active streams (per replica)" — referenced narratively by `GatewayActiveStreamsHigh` (line 350) but alert text says "Active streams per replica > 80% of configured max" without citing a metric name.
- Line 35 "Postgres connection pool utilization (per replica)".
- Line 36 "Redis memory usage and eviction rate".
- Line 38 "Credential lease assignments (by provider, pool, source)".
- Line 40 "Credential pool utilization (active leases / total credentials, by pool)" — referenced by `CredentialPoolLow` (line 348) without a metric identifier.
- Line 41 "Credential pool health (credentials in cooldown, by pool)".
- Line 42 "Credential lease duration".
- Line 43 "Credential pre-claim mismatch".

**Recommendation:** Assign `lenny_*` names consistent with §16.1.1 attribute naming rules. `GatewayActiveStreamsHigh` and `CredentialPoolLow` alert conditions should cite the concrete metric.

---

### OBS-010 `CheckpointDurationHigh` Condition Uses Prose Qualifier, Not `level` Label Value [Low]

**Files:** `16_observability.md`

Line 30 defines `lenny_checkpoint_duration_seconds` with a `level` label but does not enumerate the allowed values. Line 364 says "for Full-level or embedded-adapter pools" — "Full" appears elsewhere as an integration-level name (see §4.4 Basic/Standard/Full), but "embedded-adapter" is a runtime mode descriptor, not a label value. An operator attempting to render the PromQL filter has no label-value vocabulary to rely on.

**Recommendation:** In the line 30 metric definition, enumerate `level` as `basic|standard|full|embedded` (or the actual value set used by the emitter) and restate the alert condition in PromQL-form (e.g., `histogram_quantile(0.95, ...) by (pool, level) where level in ("full","embedded") > 2.5`). This closes the same ambiguity flagged in iter1 OBS-006.

---

### OBS-011 `GatewaySubsystemCircuitOpen` Label Vocabulary Incomplete [Low]

**Files:** `16_observability.md`

Line 367's alert cites `lenny_gateway_{subsystem}_circuit_state` but the four subsystems listed at line 73 (`stream_proxy`, `upload_handler`, `mcp_fabric`, `llm_proxy`) correspond to templated metric names, not a `subsystem` label. The alert wording implies a label-based filter but the metric family uses name-embedded subsystem identifiers. Prometheus alerting rules cannot uniformly match "any subsystem" without enumerating each metric.

**Recommendation:** Either (a) convert to a single metric `lenny_gateway_subsystem_circuit_state` labeled by `subsystem`, or (b) rewrite the alert as an `or`-join over the four named metrics so the PromQL is unambiguous.

---

## Summary

Five new findings (OBS-007 through OBS-011). OBS-007 and OBS-008 are regressions of iter1 OBS-002 for metrics that were added to the alerts catalog since iter1 without corresponding §16.1 registry entries. OBS-009 cleans up the last remaining unnamed metric rows. OBS-010 and OBS-011 are low-severity label-vocabulary issues that confuse PromQL rendering. All prior iter1 findings referenced by the task (`lenny_checkpoint_duration_seconds`, TTFT `isolation_profile`, `PoolConfigValidatorUnavailable`) have been resolved correctly.
