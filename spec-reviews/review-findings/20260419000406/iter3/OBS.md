# Iter3 OBS Review

## Prior-finding verification

- **OBS-007 (alert-referenced metrics missing from §16.1):** Fixed for `lenny_gateway_active_sessions` (line 7), `lenny_gateway_kms_signing_errors_total` (line 192), `lenny_warmpool_sdk_connect_timeout_total` (line 194), `lenny_crd_ssa_conflict_total` (line 196), `lenny_data_residency_violation_total` (line 198), `lenny_session_eviction_total_loss_total` (line 200), `lenny_network_policy_cidr_drift_total` (line 202), `lenny_billing_redis_stream_oldest_entry_age_seconds` (line 204). Residual regressions below (OBS-012).
- **OBS-008 (tree-size metric naming):** Fixed — `lenny_delegation_tree_size` registered at line 26.
- **OBS-009 (narrative-only rows):** Mostly fixed. One row (line 38, "mTLS handshake latency") remains unnamed — carried over as OBS-013.
- **OBS-010 (`level` label vocabulary):** PARTIAL. `level` is acknowledged as a Lenny-specific enumerated label at line 229, but the allowed value set (`basic|standard|full|embedded`) is still not enumerated on `lenny_checkpoint_duration_seconds` (line 31), `lenny_checkpoint_size_bytes` (line 31 inline), `lenny_checkpoint_size_exceeded_total` (line 152), `lenny_checkpoint_storage_failure_total` (line 153), or `lenny_checkpoint_stale_sessions` (line 32). `CheckpointDurationHigh` at line 381 still says "Full-level or embedded-adapter pools" in prose. See OBS-014.
- **OBS-011 (`GatewaySubsystemCircuitOpen` templated-name vs label):** NOT FIXED. Line 384 still selects `lenny_gateway_{subsystem}_circuit_state` (templated per-subsystem metric name), but the four subsystem metrics on lines 75–78 are defined with `{subsystem}` as a name segment, not a label. See OBS-015.

## Findings

### OBS-012 Alert References Still Un-registered Metric(s) [High]

**Files:** `16_observability.md:§16.5`, `§16.1`

Three alerts reference metrics that are not registered in the §16.1 catalog as `lenny_*` entries:

- `ControllerWorkQueueDepthHigh` (line 410) references `ControllerWorkQueueDepth` (no `lenny_` prefix, no row in §16.1). The same name without prefix appears in `04_system-components.md:433`. This is a new alert that fell through between iter1/iter2 regression sweeps.
- `AuditSIEMDeliveryLag` (line 415) references `siem_delivery_lag_seconds` (no `lenny_` prefix, no registry entry in §16.1). §12.3 outbox-forwarder section references this expression but it is never declared as a platform metric.
- `CredentialCompromised` (line 341) cites "a credential in `revoked` state has active leases still alive against it" — no metric name is referenced at all. The condition cannot be translated to PromQL and `pkg/alerting/rules` cannot render an expression.

**Recommendation:** Add `lenny_controller_workqueue_depth` (gauge labeled by `controller`, `queue`), `lenny_audit_siem_delivery_lag_seconds` (gauge) to §16.1 with the exact labels cited by the alerts. For `CredentialCompromised`, introduce `lenny_credential_revoked_with_active_leases` (gauge labeled by `pool`, `provider`, `credential_id` — or a coarser aggregation if cardinality is a concern) and cite it in the alert condition.

---

### OBS-013 Last Unnamed Metric Row in §16.1 [Medium]

**Files:** `16_observability.md:38`

Line 38 still reads `| mTLS handshake latency (gateway-to-pod) | Histogram |` with no `lenny_*` identifier. Iter1 OBS-002 and iter2 OBS-009 scrubbed 11 and 10 similar rows respectively; this one row was missed both times. The §16.1.1 single-source-of-truth rule demands every row in §16.1 have a concrete name.

**Recommendation:** Rename to `lenny_mtls_handshake_duration_seconds` (histogram labeled by `direction`: `gateway_to_pod`, `pod_to_gateway`) with a cross-reference to §13.1/§13.2.

---

### OBS-014 `level` Label Value Set Still Not Enumerated [Medium]

**Files:** `16_observability.md:31, 32, 152, 153, 229, 381`

Iter2 OBS-010 flagged that the `level` label in `lenny_checkpoint_duration_seconds` had no enumerated value set, so `CheckpointDurationHigh` (which filters "Full-level or embedded-adapter pools") could not be rendered in PromQL. Iter2 added `level` to the "other domain labels" list on line 229 but did not enumerate its values. The metric rows on lines 31 (`lenny_checkpoint_duration_seconds`, `lenny_checkpoint_size_bytes`), 32 (`lenny_checkpoint_stale_sessions`), 152 (`lenny_checkpoint_size_exceeded_total`), 153 (`lenny_checkpoint_storage_failure_total`) all use `level` without a value list. Alert line 381 still says "Full-level or embedded-adapter pools" in prose. Operators, the in-process evaluator in `pkg/alerting/rules`, and the Helm PrometheusRule generator cannot derive the PromQL filter.

**Recommendation:** On `lenny_checkpoint_duration_seconds`'s row (line 31), append `level ∈ {basic, standard, full, embedded}`. Restate the `CheckpointDurationHigh` condition as PromQL over `level=~"full|embedded"`. Apply the same enumeration to the other four checkpoint metrics.

---

### OBS-015 `GatewaySubsystemCircuitOpen` Templated-Name vs Label Mismatch Persists [Medium]

**Files:** `16_observability.md:75-78, 384`

Iter2 OBS-011 is unresolved. Line 384's alert condition reads `lenny_gateway_{subsystem}_circuit_state` — this is a templated metric-name pattern (one distinct metric per subsystem: `lenny_gateway_stream_proxy_circuit_state`, `lenny_gateway_upload_handler_circuit_state`, `lenny_gateway_mcp_fabric_circuit_state`, `lenny_gateway_llm_proxy_circuit_state`). PromQL has no wildcard for metric names, so either the alert must be an `or` join over four concrete metrics, or the metric family must be unified as a single `lenny_gateway_subsystem_circuit_state` gauge with a `subsystem` label.

**Recommendation:** Unify as `lenny_gateway_subsystem_circuit_state{subsystem=...}` (gauge, labeled by `subsystem ∈ {stream_proxy, upload_handler, mcp_fabric, llm_proxy}`). Update the per-subsystem metric rows on lines 75–78 to use the single unified form and rewrite the alert condition to `max by (subsystem) (lenny_gateway_subsystem_circuit_state) == 2 for 60s`.

---

### OBS-016 Alert-Name Drift Between §16.5 and §25.13 Tier Table [High]

**Files:** `16_observability.md:§16.5`, `25_agent-operability.md:4431-4432, 1736`

§25.13 declares that alerting rules are authored in `pkg/alerting/rules` and rendered into §16.5 and §25 in lockstep, but the §25.13 tier table lists alert names that do not exist in §16.5:

- `PostgresUnreachable` (§25.13 line 4431) — §16.5 equivalent is `SessionStoreUnavailable` (line 337) or `PgBouncerAllReplicasDown` (line 353).
- `RedisUnreachable` (§25.13 line 4431) — no equivalent in §16.5; `RedisMemoryHigh` (line 363) is the only Redis alert.
- `GatewayQueueDepthHigh` (§25.13 line 4432) — §16.5 does not contain this alert; `GatewayActiveStreamsHigh` (line 367) is the related rule.
- `GatewayLatencyHigh` (§25.13 line 4432) — not present in §16.5; gateway latency is governed by `SessionCreationLatencyBurnRate` (line 514) and `TTFTBurnRate` (line 519).
- `WarmPoolReplenishmentLag` (§25.13 line 4432) — §16.5 has `WarmPoolReplenishmentSlow` (line 387) and `WarmPoolReplenishmentFailing` (line 388); neither is named `WarmPoolReplenishmentLag`.
- `CredentialPoolUtilizationHigh` (§25.13 line 4432) — §16.5 has `CredentialPoolLow` (line 364); `CredentialPoolUtilizationHigh` is undefined.
- `PrometheusQueryLatencyHigh` (§25.10 line 1736) is claimed as "bundled, Section 25.13" but §16.5 line 455 explicitly says this alert is **not** bundled and must be operator-added. Contradiction between two sections.

Because both chapters claim to be derived from the same Go source, these name mismatches will surface as either compile errors in `pkg/alerting/rules` (if the §25.13 names are the source of truth) or as silent §25.13 documentation drift.

**Recommendation:** Update §25.13's tier-aware-defaults table (line 4432) to cite the actual alert names from §16.5 (`WarmPoolReplenishmentSlow`, `CredentialPoolLow`, `GatewayActiveStreamsHigh`, etc.). Resolve the `PrometheusQueryLatencyHigh` contradiction — either bundle it and add to §16.5 alert table, or remove the "(bundled, Section 25.13)" claim at §25.10 line 1736.

---

### OBS-017 `PostgresReplicationLagHigh` Metric Row Refers to Non-existent Alert [Low]

**Files:** `16_observability.md:187, 335`

Line 187's `lenny_postgres_replication_lag_seconds` description cites "the `PostgresReplicationLagHigh` alert", but the §16.5 critical-alert table (line 335) defines the alert as `PostgresReplicationLag` (no `-High` suffix). Either the metric row's reference or the alert name is wrong.

**Recommendation:** Rename one or the other to match. The `-High` suffix is the more conventional Prometheus naming pattern; renaming the alert to `PostgresReplicationLagHigh` would align with sibling alerts (`CheckpointStorageHigh`, `StorageQuotaHigh`, `GatewayActiveStreamsHigh`, etc.).

---

### OBS-018 `deployment_tier` Label Used in Alerts but Not in §16.1.1 [Medium]

**Files:** `16_observability.md:369, 590, 229`

`Tier3GCPressureHigh` alert (line 369) filters on `deployment_tier="tier3"`, and §16.10 OpenSLO export (line 590) cites "`deployment_tier="tier1" | "tier2" | "tier3"`" as a standard label — but `deployment_tier` is not in §16.1.1's global-label table (line 214-225) nor in the "other domain labels" enumeration (line 229). The attribute-naming section declares itself "single source of truth" and requires every label to appear in it, so `deployment_tier` is an undeclared label.

**Recommendation:** Add `deployment_tier` to the §16.1.1 attribute table with domain "Capacity tier, resolved at startup from Helm values (`tier1` | `tier2` | `tier3` | `tier4`)" and "Used on: all metrics emitted by components that carry tier-aware behavior (e.g., `Tier3GCPressureHigh` filter)". Explicitly state it is a static Helm-derived label, not a runtime value.

---

### OBS-019 `lenny_gateway_request_queue_depth` Referenced But Not Registered [Medium]

**Files:** `16_observability.md:29`, `04_system-components.md:71, 91`, `10_gateway-internals.md:91`

`lenny_gateway_request_queue_depth` is referenced as the primary HPA scale-out trigger (§4.1, §10.1) and by the HPA-validation exit criterion (§4.1 line 91), but it is never registered in the §16.1 metrics catalog. Line 29 mentions it obliquely inside the `lenny_gateway_rejection_rate` description ("used as a leading HPA scale-out indicator alongside `request_queue_depth`"), but that is not a formal registration entry. Since this is the canonical HPA signal, its absence from §16.1 breaks the catalog contract and will silently drop it from the Helm PrometheusRule and the in-process alert evaluator.

**Recommendation:** Add a dedicated §16.1 row: `Gateway request queue depth (lenny_gateway_request_queue_depth, gauge labeled by service_instance_id — instantaneous number of requests queued awaiting a handler goroutine; primary HPA scale-out trigger; see §4.1, §10.1).`

---

### OBS-020 `lenny_controller_leader_lease_renewal_age_seconds` Label Name Unspecified [Low]

**Files:** `16_observability.md:105, 343`

Line 105 registers `lenny_controller_leader_lease_renewal_age_seconds` as "gauge per controller" without naming the label. `ControllerLeaderElectionFailed` (line 343) fires "when any controller's Lease (`lenny-warm-pool-controller` or `lenny-pool-scaling-controller`) has not been renewed" — implying a label selector, but no label name is defined. Operator-side PromQL (`max by (???) ...`) cannot be written without knowing the label key.

**Recommendation:** Explicitly label by `controller` (values: `lenny-warm-pool-controller`, `lenny-pool-scaling-controller`). Update the §16.5 alert condition to `max by (controller) (lenny_controller_leader_lease_renewal_age_seconds) > 15`.

---

### OBS-021 `lenny_warmpool_pod_startup_duration_seconds` Uses Non-canonical Label Description [Low]

**Files:** `16_observability.md:87`

Line 87 reads `by pool, by isolation profile — time from pod creation to idle state`. Every other histogram entry in §16.1 uses the explicit Prometheus-label form (`labeled by pool, isolation_profile`). The inconsistent shorthand "by isolation profile" does not match the canonical label name `isolation_profile` (§16.1.1 "other domain labels" list). Readers and the `pkg/alerting/rules` validator may treat "isolation profile" as a separate domain.

**Recommendation:** Rewrite as `histogram labeled by pool, isolation_profile — time from pod creation to idle state`.

## Summary

Ten new findings (OBS-012 through OBS-021). OBS-011 and OBS-010 are direct carry-overs from iter2 that were not addressed by commit 2a46fb6. OBS-012 is a regression because commit 2a46fb6 added alerts (`ControllerWorkQueueDepthHigh`, `AuditSIEMDeliveryLag`, `CredentialCompromised`) without corresponding §16.1 metric registrations. OBS-016 is the most consequential — it exposes systemic drift between §16.5 (the authored alert catalog) and §25.13 (which claims to be compiled from the same Go source) with six mismatched or contradictory alert names.
