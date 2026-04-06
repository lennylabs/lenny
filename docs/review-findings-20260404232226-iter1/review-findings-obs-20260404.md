# Observability & Operational Monitoring — Review Findings

**Spec:** `docs/technical-design.md`
**Perspective:** 12. Observability & Operational Monitoring
**Date:** 2026-04-04
**Category code:** OBS

---

## Summary

The observability design is substantially developed: Section 16 covers a well-structured metrics table, four-phase latency breakpoints, OTel trace spans for most critical paths, structured logging, and a two-tier alerting table. Several subsystems added metrics reactively (elicitation, credential lifecycle, delegation budget) in prior review cycles. However, eleven areas remain where the spec either omits coverage entirely or specifies metrics and alerts that are scattered through body text but absent from the canonical Section 16 tables — creating a gap between what the spec promises at the component level and what will actually be instrumented if engineers build only from Section 16.

---

## Findings

### OBS-001 Warm Pool Claim Queue Metrics Absent [High]
**Section:** 16.1, 4.6.1

The spec defines `lenny_gateway_active_sessions` and `Warm pods available (by pool)` but has no metrics for the claim queue itself. Section 4.6.1 describes a `podClaimQueueTimeout` (default 30s) that queues incoming requests during controller failover, yet there is no `lenny_pod_claim_queue_depth` gauge, no `lenny_pod_claim_queue_wait_seconds` histogram, no `lenny_pod_claim_conflict_total` counter (optimistic-lock retries on `SandboxClaim`), and no `lenny_pod_claim_timeout_total`. The `WarmPoolExhausted` critical alert fires at zero pods but does not fire for queue saturation when pods exist but all are being claimed concurrently. An operator cannot distinguish "pool is fine, claims are batching normally" from "pool is fine, but claim queue is saturated and sessions are timing out" from metrics alone.

**Recommendation:** Add to Section 16.1: `lenny_pod_claim_queue_depth` (gauge, by pool), `lenny_pod_claim_queue_wait_seconds` (histogram, by pool), `lenny_pod_claim_conflict_total` (counter, by pool — tracks optimistic-lock retry events on `SandboxClaim`), `lenny_pod_claim_timeout_total` (counter, by pool). Add a `PodClaimQueueSaturated` warning alert to Section 16.5: queue depth exceeds 50% of `maxConcurrent` session rate for > 30s.

---

### OBS-002 Token Service Has No Metrics or Alert [High]
**Section:** 16.1, 16.5

The Token/Connector Service (Section 4.3) is the only component with KMS decrypt permissions, serves every credential-dependent session start, and has its own circuit breaker (Section 11.6). It is also explicitly called out as a failure mode that blocks new sessions for credential-requiring runtimes. Despite this, Section 16.1 contains zero Token Service metrics and Section 16.5 has no `TokenServiceUnavailable` alert. The LLM Proxy subsystem has three named metrics (Section 4.9: `lenny_gateway_llm_proxy_active_connections`, `_request_duration_seconds`, `_circuit_state`) but they live in body text and are not listed in Section 16.1. The Token Service itself — a separate deployment — has nothing.

**Recommendation:** Add to Section 16.1: `lenny_token_service_request_duration_seconds` (histogram, by operation: `assign`, `rotate`, `refresh`), `lenny_token_service_errors_total` (counter, by error type), `lenny_token_service_circuit_state` (gauge: 0=closed, 1=half-open, 2=open). Also add the three LLM Proxy metrics already named in Section 4.9 to the Section 16.1 table so they appear in the canonical list. Add to Section 16.5 a critical alert `TokenServiceUnavailable`: Token Service circuit breaker in open state for > 30s (new sessions requiring credentials will fail; existing sessions are unaffected until lease expiry).

---

### OBS-003 Gateway Per-Subsystem Metrics Are Named in Body Text Only [High]
**Section:** 16.1, 4.1

Section 4.1 promises "per-subsystem metrics: Latency histograms, error rates, and queue depth are emitted per subsystem." Section 4.9 names three LLM Proxy metrics explicitly. But Section 16.1 — the canonical metrics table — does not enumerate any per-subsystem metrics for Stream Proxy, Upload Handler, or MCP Fabric. If engineers build from Section 16.1 only (the natural implementation reference), these metrics will not be instrumented. There are also no circuit breaker state metrics for any subsystem, meaning a tripped circuit breaker (e.g., Upload Handler in open state) is invisible until users report 503s on uploads.

**Recommendation:** Add to Section 16.1 a "Gateway Subsystems" block with explicit metric names for all four subsystems: `lenny_gateway_{subsystem}_request_duration_seconds` (histogram), `lenny_gateway_{subsystem}_errors_total` (counter), `lenny_gateway_{subsystem}_queue_depth` (gauge), `lenny_gateway_{subsystem}_circuit_state` (gauge), where `{subsystem}` ∈ {`stream_proxy`, `upload_handler`, `mcp_fabric`, `llm_proxy`}. Add a `GatewaySubsystemCircuitOpen` warning alert to Section 16.5 that fires when any subsystem circuit breaker is in open state for > 60s.

---

### OBS-004 No Warm Pool Replenishment Rate or Cold-Start Latency Metrics [High]
**Section:** 16.1, 16.5

The spec tracks `Warm pods available` and `Time-to-claim` but has no metric for how fast the pool is being replenished after claims drain it. Without a `lenny_warmpool_pod_startup_duration_seconds` histogram (from pod creation to `idle` state) or a `lenny_warmpool_replenishment_rate` gauge (pods becoming ready per minute, by pool), operators cannot determine whether a `WarmPoolLow` warning is self-correcting (pool is refilling fast) or is trending toward `WarmPoolExhausted` (startup latency is high). The spec's formula for `minWarm` depends on `pod_warmup_seconds`, but there is no metric that surfaces the actual observed startup duration for operators to validate the formula against. Similarly, when `minWarm: 0` pools serve cold-start sessions, there is no `lenny_warmpool_cold_start_latency_seconds` histogram to quantify the cold-start penalty referenced throughout the spec (Sections 4.6.1, 5.2, 6.1).

**Recommendation:** Add to Section 16.1: `lenny_warmpool_pod_startup_duration_seconds` (histogram, by pool, by isolation profile — time from pod creation to `idle` state), `lenny_warmpool_replenishment_rate` (gauge, pods/min entering `idle` state, by pool), `lenny_warmpool_cold_start_total` (counter, by pool — increments when a session is served from a cold pod). Add a `WarmPoolReplenishmentSlow` warning alert to Section 16.5: P95 pod startup duration > 2× the pool's configured `pod_warmup_seconds` baseline for > 5 min.

---

### OBS-005 Delegation Tree Observability Is Shallow — Depth Distribution Is Insufficient [Medium]
**Section:** 16.1, 8.10

Section 16.1 includes `Delegation depth distribution` and `Delegation tree size distribution` as histograms, plus `lenny_delegation_budget_utilization_ratio` and `lenny_delegation_tree_token_usage_total`. However, the spec has no per-node metrics: no `lenny_delegation_spawn_latency_seconds` histogram (time from `delegate_task` call to child session ready — this is a user-observable latency that can span pod claim, file export, workspace upload, and child startup), no `lenny_delegation_child_failure_total` counter (by failure classification: transient/permanent/budget_exhausted), and no `lenny_delegation_tree_recovery_duration_seconds` histogram (Section 8.11's bottom-up tree recovery is a complex operation with two configurable timeouts but no measurement). The `lenny_delegation_budget_utilization_ratio` is a gauge but its label set is unspecified — it is unclear whether it is labeled by `root_session_id`, `tenant_id`, or both, making aggregate analysis across tenants or over time difficult.

**Recommendation:** Add to Section 16.1: `lenny_delegation_spawn_latency_seconds` (histogram, by target runtime type), `lenny_delegation_child_failure_total` (counter, by failure classification, by depth), `lenny_delegation_tree_recovery_duration_seconds` (histogram, by outcome: success/partial/failed). Specify label set for `lenny_delegation_budget_utilization_ratio` as `{tenant_id, root_session_id}`. Add an SLO entry in Section 16.5 for delegation spawn latency (suggested: P95 < 15s from `delegate_task` call to first child event).

---

### OBS-006 Warm Pool Utilization Rate Not Exposed [Medium]
**Section:** 16.1

The metric table lists `Warm pods available (by pool)` as a gauge but not the utilization ratio: `available / (minWarm + maxWarm)` or `active / total`. Without a utilization ratio, alerting thresholds are absolute (requiring per-pool configuration) rather than relative. More critically, there is no `lenny_warmpool_waste_ratio` or equivalent: the spec mentions `lenny_warmpool_idle_pod_minutes` for cost visibility but this is a counter that only reveals waste in aggregate over time — not a real-time signal of how many warm pods have been sitting idle beyond a time threshold (stale warm pods), which the spec defines as a metric but does not name with a Prometheus metric name. Section 16.1 says "Stale warm pods (idle beyond threshold, by pool)" is a gauge but the metric name is never given.

**Recommendation:** Add to Section 16.1: `lenny_warmpool_utilization_ratio` (gauge: active_pods / (available + active), by pool) and assign an explicit Prometheus name to the "Stale warm pods" gauge: `lenny_warmpool_stale_pods` (gauge, by pool, by runtime). Add a `WarmPoolHighWaste` info-level alert for `lenny_warmpool_stale_pods > 0.3 * minWarm` sustained for > 1h (signals pool is over-provisioned relative to demand).

---

### OBS-007 Alerting Table Missing Several Alerts Referenced in Body Text [Medium]
**Section:** 16.5

At least seven named alerts appear in the spec body but are absent from the Section 16.5 canonical tables:

1. `WarmPoolIdleCostHigh` — Section 4.6.1 says "see Section 16.5" but it is not in either alert table.
2. `FinalizerStuck` — Section 4.6.1 defines it but it is not in Section 16.5.
3. `EtcdQuotaNearLimit` — Section 4.6.1 says "An `EtcdQuotaNearLimit` alert should fire at 80%" but it is not in Section 16.5.
4. `DualStoreUnavailable` — Section 10.1 defines it but it is not in Section 16.5.
5. `QuotaFailOpenCumulativeThreshold` — Section 12.4 defines it but it is not in Section 16.5.
6. `DirectModeWeakIsolation` — Section 4.9 defines it as a controller warning event but it has no alert entry in Section 16.5.
7. `AuditGrantDrift` — Section 11.7 says the gateway "emits a critical alert" but it is not in Section 16.5.

Because Section 16.5 is the operational reference for runbook authors and alert rule implementors, omitting alerts from it means they will not be provisioned as Prometheus/AlertManager rules even though the spec intends them to fire.

**Recommendation:** Add all seven alerts to Section 16.5 with their conditions, severity, and evaluation windows. Specifically: `WarmPoolIdleCostHigh` (Warning), `FinalizerStuck` (Warning — pod stuck in Terminating for > 5 min), `EtcdQuotaNearLimit` (Warning — etcd backend bytes > 80% of quota), `DualStoreUnavailable` (Critical — fire immediately), `QuotaFailOpenCumulativeThreshold` (Warning — cumulative fail-open time > 80% of `quotaFailOpenCumulativeMaxSeconds`), `DirectModeWeakIsolation` (Warning — controller event), `AuditGrantDrift` (Critical). Also cross-reference these entries with the body sections that describe them.

---

### OBS-008 Checkpoint Success/Failure Rates and Resume Observability Absent [Medium]
**Section:** 16.1

The spec defines `lenny_checkpoint_storage_failure_total` (counter, by pool/tier/trigger) and `lenny_checkpoint_duration_seconds` (histogram) in Section 4.4, but Section 16.1 only lists "Checkpoint size and duration" as a histogram. Missing from both Section 16.1 and the body:
- `lenny_checkpoint_total` (counter, by outcome: success/failed/skipped, by trigger: periodic/eviction/pre_scale_down) — without this, calculating the checkpoint success rate SLO requires subtracting failure counter from an implicit total.
- `lenny_checkpoint_age_at_resume_seconds` (histogram) — when a session resumes, the age of the checkpoint used determines data loss exposure. This is the operational equivalent of RPO for individual sessions.
- `lenny_session_resume_total` (counter, by outcome: success/failed, by resume_source: checkpoint/none) — the spec tracks `Resume success/failure rate` as a counter but does not name it.

The `lenny_checkpoint_storage_failure_total` metric is named in Section 4.4 but not listed in Section 16.1, making it likely to be missed in implementation.

**Recommendation:** Add to Section 16.1: `lenny_checkpoint_total` (counter, by outcome, trigger, tier), `lenny_checkpoint_age_at_resume_seconds` (histogram, by pool), `lenny_session_resume_total` (counter, by outcome, resume_source). Add explicit Prometheus name `lenny_session_resume_rate_total` to replace the unnamed `Resume success/failure rate` entry. Move `lenny_checkpoint_storage_failure_total` from Section 4.4 to Section 16.1. Add a `CheckpointSuccessRateLow` warning alert to Section 16.5: success rate < 99% over any 5-minute window by pool/tier.

---

### OBS-009 Setup Command Metrics Are Missing [Medium]
**Section:** 16.1, 7.5

Section 16.2 instruments four latency phases (pod claimed, workspace prep done, session ready, first token). However, setup commands (Section 7.5) — which execute on the hot path with a 300s timeout and can include arbitrary shell commands — have no dedicated observability. There is no `lenny_setup_command_duration_seconds` histogram, no `lenny_setup_command_failures_total` counter (by failure class: timeout/non-zero-exit/disallowed), and the `session.run_setup` span (Section 16.3) is a single span that cannot reveal which specific setup command was slow. Setup command failures silently collapse into session creation failure, making it impossible to distinguish a broken setup script from a pod startup regression.

**Recommendation:** Add to Section 16.1: `lenny_setup_command_duration_seconds` (histogram, by runtime, labeled with `command_index` — index in the ordered list), `lenny_setup_command_failures_total` (counter, by failure_class: timeout/nonzero_exit/blocked_command, by runtime). Add a child span `session.run_setup.command[N]` per command within the `session.run_setup` parent span (Section 16.3), so trace analysis can identify which command in a multi-step setup is the bottleneck.

---

### OBS-010 No SLO for Delegation Spawn Latency [Medium]
**Section:** 16.5

The SLO table in Section 16.5 covers session creation success rate, time to first token, session availability, gateway availability, startup latency (pod-warm, by isolation profile), and checkpoint duration. Delegation spawn — the time from a `delegate_task` call to the child session being ready — is user-visible and can dominate the latency of orchestration-heavy workflows, yet has no SLO. At Tier 3 with 500 concurrent delegations and contended warm pools, delegation spawn latency degrades silently unless an SLO target creates accountability for it. The spec notes that delegation spawn involves pod claim, file export, workspace upload, and child startup, all of which are instrumented individually, but the end-to-end delegation spawn latency is not composed into a single trackable target.

**Recommendation:** Add to Section 16.5 SLO table: "Delegation spawn latency: P95 < 15s, measured from `delegate_task` call receipt at gateway to child session `session.start` completion." Add `lenny_delegation_spawn_latency_seconds` histogram to Section 16.1, with the P95 < 15s target referenced in both places. Add a `DelegationSpawnLatencyHigh` warning alert: delegation spawn P95 > 12s over a 5-minute window (early warning before SLO breach).

---

### OBS-011 Concurrent Mode Slot Observability Missing [Medium]
**Section:** 16.1, 5.2

Section 5.2 defines `task` and `concurrent` execution modes where multiple sessions share a single pod. These modes have distinct resource semantics from `session` mode, but Section 16.1 has no metrics for slot utilization, slot wait time, or slot allocation failures. Without `lenny_concurrent_slots_active` (gauge, by pool, by pod), `lenny_concurrent_slot_utilization_ratio` (gauge: active/max_slots), and `lenny_concurrent_slot_wait_seconds` (histogram, by pool), operators cannot determine whether concurrent-mode pods are over- or under-provisioned. The PoolScalingController formula includes a `mode_factor` adjustment for concurrent mode, but if the actual slot utilization is never measured, the formula cannot be validated or tuned.

**Recommendation:** Add to Section 16.1: `lenny_concurrent_slots_active` (gauge, by pool, by pod_id), `lenny_concurrent_slot_utilization_ratio` (gauge, by pool — aggregate active/max_slots across all pods in the pool), `lenny_concurrent_slot_wait_seconds` (histogram, by pool — time a task request waits for a free slot). Add a `ConcurrentSlotUtilizationHigh` warning alert to Section 16.5: utilization ratio > 90% for any concurrent-mode pool for > 2 min.

---

### OBS-012 PgBouncer Saturation and Read Replica Lag Not in Alerting [Medium]
**Section:** 12.3, 16.5

Section 12.3 says "Deploy `pgbouncer_exporter` as a sidecar on each PgBouncer pod" and lists key metrics to alert on (`cl_active`/`sv_active`, `cl_waiting_time`, `avg_query_time`). However, these do not appear in Section 16.5's alert table. The Section 16.5 `PostgresReplicationLag` critical alert fires at > 1s synchronous replica lag, which is correct for the sync replica, but there is no alert for read replica lag (mentioned as optional at Tier 2, required at Tier 3 in Section 17.8) and no `PgBouncerPoolSaturated` or `PgBouncerClientWaitHigh` warning alert. Under write pressure at Tier 3, PgBouncer client wait can spike well before Postgres replication lag becomes visible, making PgBouncer the earlier signal of database pressure.

**Recommendation:** Add to Section 16.5 warning alerts: `PgBouncerClientWaitHigh` (PgBouncer `cl_waiting_time` P95 > 100ms for > 60s), `PgBouncerPoolSaturated` (all pool connections active and `cl_waiting > 10` for > 30s), `PostgresReadReplicaLag` (read replica lag > 5s for > 60s — separate from the critical sync replica alert). Cross-reference these with the monitoring guidance already in Section 12.3 so there is one authoritative list.

---

### OBS-013 Workspace Upload and Materialization Tracing Is Too Coarse [Medium]
**Section:** 16.3

The `session.upload` span (Gateway + Pod) and `session.finalize_workspace` span (Pod) are present but each covers multiple distinct operations that can fail or be slow independently:

- `session.upload` bundles network transfer, payload validation, staging to ArtifactStore, and archive extraction into one span
- `session.finalize_workspace` bundles archive extraction (if upload skipped), path validation, symlink checks, and promotion to `/workspace/current`

When a session creation latency SLO is breached, a single `session.upload` span that took 8s provides no signal about whether the bottleneck was MinIO write latency, archive extraction CPU, or network transfer. Section 7.4 defines several upload validation stages that each have failure modes but are invisible as child spans.

**Recommendation:** In Section 16.3, add child spans within `session.upload`: `workspace.stream_to_staging` (network transfer to ArtifactStore), `workspace.validate_payload` (content type, size, security checks), `workspace.extract_archive` (tar extraction, if applicable). Add child spans within `session.finalize_workspace`: `workspace.validate_paths` (symlink and path traversal checks), `workspace.promote` (atomic move to `/workspace/current`). All child spans should inherit the parent trace context.

---

### OBS-014 Trace Sampling Loses Full Delegation Tree Context [Medium]
**Section:** 16.3

Section 16.3 specifies: "all spans in a tree are sampled if the root is sampled, preserving trace completeness." However, it does not specify how this tree-coherent sampling is implemented when the root session and child sessions may land on different gateway replicas. In a distributed deployment, head-based sampling at the root replica must propagate a "force-sample" decision to all child span origins — child gateway replicas serving child sessions need to know the root was sampled. The spec says the OTel Collector handles "sampling, batching, and export" but head-based sampling is a decision made before the collector sees the spans, not after. If each child session is independently head-sampled at 10%, a 5-node delegation tree where only the root is force-sampled will still lose child spans from replicas that independently decided not to sample.

**Recommendation:** In Section 16.3, specify that the `delegation.spawn_child` span injects the sampling decision as an explicit trace flag (`sampled=1`) into the child session's trace context before the child session starts. The gateway propagates this flag in the OTel context that it passes to child pods via gRPC metadata on the `StartSession` RPC. Child replicas must honor an incoming `sampled=1` flag regardless of their local head-based sampling rate. Document this as a requirement for the OTel SDK configuration in child pods. Optionally, add a 100% sampling override for delegation trees exceeding depth 2, since these are the cases where full tree visibility matters most for debugging.

---

### OBS-015 Controller Reconciliation Observability Not In Section 16 [Low]
**Section:** 16.1, 4.6.1

The `lenny_controller_queue_overflow_total` counter (Section 4.6.1) and the controller rate limiter metrics (pod creation QPS, status update QPS) are described in body text but are not in Section 16.1. There is no histogram for controller reconciliation duration, no gauge for work queue depth (distinct from overflow), and no alert for sustained queue overflow — which is a leading indicator of pool scale events failing to complete. Section 17.8 provides per-tier queue depth limits but there is no matching alert threshold.

**Recommendation:** Add to Section 16.1: `lenny_controller_queue_depth` (gauge, by controller: warm_pool/pool_scaling), `lenny_controller_queue_overflow_total` (counter, by controller), `lenny_controller_reconciliation_duration_seconds` (histogram, by controller, by result: success/error). Add a `ControllerQueueOverflowing` warning alert to Section 16.5: queue overflow counter rate > 0 for > 60s (indicates scale events are being dropped).

---

### OBS-016 Experiment Metrics Lack Variant Labels [Low]
**Section:** 16.1, 10.7

Section 10.7 defines experiment primitives with variants and eval results. However, the existing metrics in Section 16.1 (`Active sessions`, `Token usage`, `Session creation latency`) do not carry an `experiment_id` or `variant_id` label. This means operational metrics (latency, error rate, session creation success) cannot be broken down by experiment variant in Grafana. Operators cannot observe whether the treatment variant has higher session error rates or worse startup latency without custom queries joining metrics to the Postgres `EvalResult` table.

**Recommendation:** Add optional `experiment_id` and `variant_id` labels to: `lenny_gateway_session_create_duration_seconds`, `lenny_session_error_total`, and `lenny_token_usage_total`. Labels are empty for sessions not enrolled in an experiment. Document that high-cardinality label values (many distinct experiment IDs) should be managed by limiting concurrent active experiments per deployment. Add a note in Section 10.7 referencing these labels.

---

### OBS-017 No Grafana Dashboard Specification or Helm Deliverable [Low]
**Section:** 16, 17.4

Section 17.4 mentions "Grafana (pre-built Lenny dashboard)" in the optional Tier 2 dev mode compose profile. However, the spec never specifies what dashboards are shipped, what they contain, or whether dashboard JSON is a checked-in artifact in the Helm chart. There are no references to standard Grafana dashboard panels for the SLOs defined in Section 16.5 (e.g., session creation success rate burn rate, time-to-first-token P95, warm pool utilization heatmap). Without a canonical dashboard definition in the Helm chart, every operator builds their own, defeating the operational readiness value of the detailed metrics specification.

**Recommendation:** Specify in Section 16 (or Section 17.4) that the Helm chart includes Grafana dashboard JSON under `templates/dashboards/` as a `ConfigMap` (compatible with the Grafana sidecar loader). Define the minimum required dashboards: (1) Session Overview — SLO burn rates, session creation rate, error rate by tenant; (2) Warm Pool Health — pool utilization, claim latency P95/P99, replenishment rate, stale pods; (3) Delegation Trees — depth distribution, budget utilization, spawn latency; (4) Infrastructure — Postgres/Redis/MinIO health. Reference Phase 13 as the delivery milestone.

---

### OBS-018 No Metrics for SDK-Warm Demotion Latency Impact [Info]
**Section:** 16.1, 6.1

Section 6.1 adds `lenny_warmpool_sdk_demotions_total` (counter) for observability of how often pods are demoted from SDK-warm to pod-warm. However, there is no histogram for the demotion latency itself (`lenny_warmpool_sdk_demotion_duration_seconds`). The spec states the demotion penalty is "typically 1–3s depending on runtime" but this is never validated against actual observed durations. If a runtime's SDK teardown becomes slow (e.g., 8–10s), the startup latency SLO (P95 < 2s for runc) will be violated silently for sessions that trigger demotion, because the demotion latency is absorbed into the overall `session.create` span without a dedicated label.

**Recommendation:** Add `lenny_warmpool_sdk_demotion_duration_seconds` histogram to Section 16.1 (by pool, by runtime). Add a `SDKDemotionLatencyHigh` warning alert: demotion P95 > 5s for > 10 occurrences per hour, since this indicates a runtime whose teardown is exceeding the expected penalty window and will cause startup SLO breaches for affected sessions.
