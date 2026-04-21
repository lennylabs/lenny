---
layout: default
title: Metrics Reference
parent: Reference
nav_order: 2
---

# Metrics Reference
{: .no_toc }

Complete reference for all Prometheus metrics emitted by Lenny platform components. Each metric includes its type, labels, description, emitting component, and associated alert rules or HPA configuration.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Gateway metrics

### Core gateway metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_gateway_active_sessions` | Gauge | `service_instance_id` | Number of sessions coordinated by this gateway replica. Postgres-backed count; lags real goroutine pressure. | `GatewaySessionBudgetNearExhaustion` alert (capacity ceiling signal only -- NOT an HPA trigger). |
| `lenny_gateway_active_streams` | Gauge | `service_instance_id` | In-flight streaming connections per replica. | Secondary HPA metric. |
| `lenny_gateway_request_queue_depth` | Gauge | `service_instance_id` | Requests queued awaiting a handler goroutine. Reflects instantaneous back-pressure. | **Primary Horizontal Pod Autoscaler (HPA) scale-out trigger.** Target `averageValue: 10` at Growth size. |
| `lenny_gateway_gc_pause_p99_ms` | Gauge | `service_instance_id` | Process-level P99 GC pause per replica. Sustained >50 ms signals shared-process boundary pressure. | `Tier3GCPressureHigh` alert; signal that the LLM routing subsystem should be extracted. |
| `lenny_gateway_gc_pause_fleet_p99_ms` | Gauge | -- | 99th-percentile GC pause aggregated across all active gateway replicas. Computed as `max(lenny_gateway_gc_pause_p99_ms)` over all instances. | `Tier3GCPressureHigh` alert; aggregate health indicator for Scale-size deployments. |
| `lenny_gateway_rejection_rate` | Gauge | `service_instance_id` | Requests rejected with 429/503 per second per replica. | Leading HPA scale-out indicator. |

### Gateway drain and preStop metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_prestop_cap_selection_total` | Counter | `pool`, `service_instance_id`, `source`: `postgres`, `postgres_null`, `cache_hit`, `cache_miss_max_tier` | Emitted once per preStop Stage 2 tier selection. Distinguishes whether the tiered cap was selected from Postgres with a non-null value (`postgres`), Postgres returning `NULL` for a fresh session with the 90s conservative fallback (`postgres_null`), the in-replica cache (`cache_hit`), or Postgres unreachable with 90s fallback (`cache_miss_max_tier`). | `PreStopCapFallbackRateHigh` alert (evaluates the combined `postgres_null + cache_miss_max_tier` share per-replica). |
| `lenny_prestop_barrier_target_source_total` | Counter | `pool`, `source`: `postgres`, `cache_fallback` | Emitted once per preStop `CheckpointBarrier` fan-out invocation. Distinguishes whether the barrier-target set was sourced from the Postgres `coordination_lease` table (steady-state) or from the in-memory lease cache (Postgres read failure). | Operational monitoring (correlates with `DualStoreUnavailable`). |
| `lenny_circuit_breaker_cache_stale_seconds` | Gauge | -- | Wall seconds since the AdmissionController's in-process circuit-breaker cache was last successfully refreshed from Redis; 0 under healthy steady-state polling; monotonically increasing when Redis is unreachable. | `CircuitBreakerStale` alert. |
| `lenny_circuit_breaker_cache_stale_serves_total` | Counter | `outcome`: `rejected` \| `admitted` | Every admission decision served against a cache not refreshed within the 5s poll interval. `outcome="admitted"` is the security-salient case. | `CircuitBreakerStale` alert (alert body includes rate by outcome). |

### HPA metric roles

| Metric | Role | Where used |
|:-------|:-----|:-----------|
| `lenny_gateway_request_queue_depth` | Primary HPA scale-out trigger | HPA / KEDA ScaledObject |
| `lenny_gateway_active_streams` | Secondary HPA metric | HPA / KEDA ScaledObject |
| `lenny_gateway_active_sessions / maxSessionsPerReplica` | Capacity ceiling alert, NOT an HPA trigger | `GatewaySessionBudgetNearExhaustion` alert only |

---

## Gateway subsystem metrics

Each gateway subsystem (Stream Proxy, Upload Handler, MCP Fabric, LLM Proxy) emits the following templated metrics. Replace `{subsystem}` with `stream_proxy`, `upload_handler`, `mcp_fabric`, or `llm_proxy`.

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_gateway_{subsystem}_request_duration_seconds` | Histogram | `subsystem` | Per-request latency for the subsystem. |
| `lenny_gateway_{subsystem}_errors_total` | Counter | `subsystem`, `error_type` | Error count per subsystem. |
| `lenny_gateway_{subsystem}_queue_depth` | Gauge | `subsystem` | Queue depth for the subsystem. |
| `lenny_gateway_{subsystem}_circuit_state` | Gauge | `subsystem` | Circuit breaker state: 0=closed, 1=half-open, 2=open. |

### Stream Proxy extraction-threshold metrics

| Metric | Type | Labels | Description | Extraction threshold |
|:-------|:-----|:-------|:------------|:---------------------|
| `lenny_stream_proxy_queue_depth` | Gauge | `pool` | Stream proxy queue depth. | >500 sustained for >=5 min. |
| `lenny_stream_proxy_goroutines` | Gauge | `service_instance_id` | Active goroutines in the stream proxy. | -- |
| `lenny_stream_proxy_p99_attach_latency_seconds` | Gauge | `pool` | Pre-computed P99 latency for session attach operations. | >0.8 s (800 ms) sustained for >=5 min. |

### Upload Handler extraction-threshold metrics

| Metric | Type | Labels | Description | Extraction threshold |
|:-------|:-----|:-------|:------------|:---------------------|
| `lenny_upload_handler_active_uploads` | Gauge | `pool` | Currently active upload operations. | >200 concurrent sustained. |
| `lenny_upload_handler_queue_depth` | Gauge | `pool` | Upload handler queue depth. | -- |
| `lenny_upload_handler_p99_latency_seconds` | Gauge | `pool` | Pre-computed P99 upload latency. | -- |

### MCP Fabric extraction-threshold metrics

| Metric | Type | Labels | Description | Extraction threshold |
|:-------|:-----|:-------|:------------|:---------------------|
| `lenny_mcp_fabric_active_delegations` | Gauge | `pool` | Currently active delegation operations. | >1,000 concurrent sustained. |
| `lenny_mcp_fabric_goroutines` | Gauge | `service_instance_id` | Active goroutines in the MCP Fabric. | -- |
| `lenny_mcp_fabric_p99_orchestration_latency_seconds` | Gauge | `pool` | Pre-computed P99 delegation orchestration latency. | >2.0 s (2,000 ms) sustained. |

### LLM Proxy extraction-threshold metrics

| Metric | Type | Labels | Description | Extraction threshold |
|:-------|:-----|:-------|:------------|:---------------------|
| `lenny_gateway_llm_proxy_active_connections` | Gauge | -- | Active upstream LLM connections held by the gateway's LLM routing subsystem. This is the canonical metric name used by the Tier 3 extraction-readiness ratio. | >2,000 sustained or >60% of `maxConcurrent`. |
| `lenny_llm_proxy_upstream_goroutines` | Gauge | `service_instance_id` | Goroutines handling upstream LLM streams. | -- |
| `lenny_llm_proxy_p99_ttfb_seconds` | Gauge | `pool`, `provider` | Pre-computed P99 time-to-first-byte for upstream LLM requests. | -- |

### LLM translator metrics

Emitted by the gateway when `deliveryMode: proxy` pools are active. The gateway talks to LLM providers on behalf of agent pods, so pods never hold real API keys — the keys are held only in the gateway process's memory, and credential rotation does not interrupt traffic. See [Security](../operator-guide/security.md#llm-proxy) for the trust boundary and [external LLM proxy](../operator-guide/external-llm-proxy.md) for deployer integrations with third-party routing gateways.

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_gateway_llm_proxy_request_duration_seconds` | Histogram | `pool`, `provider`, `proxy_dialect` | End-to-end time from the gateway's LLM routing subsystem receiving the request to response completion, including in-process translation and upstream provider latency. | -- |
| `lenny_gateway_llm_translation_duration_seconds` | Histogram | `pool`, `provider`, `proxy_dialect`, `direction`: `request`, `response` | Wall-clock time the gateway spends converting between the dialect exposed to the runtime and the upstream provider's wire format, per request or response leg. Upstream network time is excluded; this histogram measures translator CPU only. Subcomponent of `lenny_gateway_llm_proxy_request_duration_seconds`. | -- |
| `lenny_gateway_llm_translation_errors_total` | Counter | `pool`, `provider`, `error_type`: `unsupported_field`, `schema_mismatch`, `auth_failed`, `timeout`, `streaming_interrupted`, `upstream_4xx`, `upstream_5xx` | Translator failures by category. `schema_mismatch` covers both incoming pod-request validation and upstream response-shape drift. `upstream_5xx` feeds the LLM routing subsystem's circuit breaker; `auth_failed` triggers the Fallback Flow. | `LLMTranslationSchemaDrift` (Warning). |
| `lenny_gateway_llm_upstream_egress_anomaly_total` | Counter | -- | Outbound connection attempts observed from the gateway pod to destinations outside the `allow-gateway-egress-llm-upstream` NetworkPolicy allowlist. Detected via eBPF connection tracking where available, or via NetworkPolicy drop counters as a fallback. The source is transparent to the operator; the metric is always present. | `LLMUpstreamEgressAnomaly` (Critical). |
| `lenny_gateway_max_sessions_per_replica` | Gauge | `delivery_mode`: `proxy`, `direct` | Maximum concurrent sessions this replica can serve under the given delivery mode. Computed at startup from Helm `maxSessionsPerReplica` (direct) or `maxSessionsPerReplicaProxyMode` (proxy). Operators compute the proxy-vs-direct ratio for capacity planning. | -- |

---

## Session metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_session_startup_duration_seconds` | Histogram | `pool`, `runtime_class`, `isolation_profile` | Wall-clock time from pod claim to session ready (excluding file upload). | `StartupLatencyBurnRate`, `StartupLatencyGVisorBurnRate` alerts. |
| `lenny_session_time_to_first_token_seconds` | Histogram | `pool`, `runtime_class`, `isolation_profile` | Wall-clock time from session start request to first streaming event emitted to client. | `TTFTBurnRate` alert. |
| `lenny_session_pod_released_during_suspension_total` | Counter | `pool`, `tenant_id` | Increments when `maxSuspendedPodHoldSeconds` fires and pod is released. | Operational monitoring. |
| `lenny_session_suspension_checkpoint_failed_total` | Counter | `pool`, `tenant_id` | Increments when checkpoint attempt before pod release fails. | Operational monitoring. |
| `lenny_session_error_total` | Counter | `tenant_id`, `session_type`, `variant_id` | Session errors by variant. | Experiment rollback triggers. |
| `lenny_session_total` | Counter | `tenant_id`, `session_type`, `variant_id` | Total sessions by variant. | Experiment rollback trigger denominator. |

---

## Pool and warm pool metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_warmpool_idle_pods` | Gauge | `pool` | Pods in `idle` state ready to be claimed. | `WarmPoolLow`, `WarmPoolExhausted`, `PodClaimQueueSaturated` alerts. |
| `lenny_warmpool_pod_startup_duration_seconds` | Histogram | `pool`, `isolation_profile` | Time from pod creation to `idle` state. | `WarmPoolReplenishmentSlow` alert. |
| `lenny_warmpool_replenishment_rate` | Gauge | `pool` | Pods per minute entering `idle` state. | Operational monitoring. |
| `lenny_warmpool_warmup_failure_total` | Counter | `pool`, `runtime_class`, `error_type` | Pods failing to reach `idle` during warm-up. Error types: `image_pull_error`, `setup_command_failed`, `resource_quota_exceeded`, `node_pressure`. | `WarmPoolReplenishmentFailing` alert. |
| `lenny_warmpool_cold_start_total` | Counter | `pool` | Sessions served from cold pod or exhausted pool. | Operational monitoring. |
| `lenny_warmpool_fill_duration_seconds` | Histogram | `pool` | Time from pool creation to reaching `minWarm` ready pods. | Operational monitoring. |
| `lenny_warmpool_claims_total` | Counter | `pool`, `runtime_class` | Warm pod transitions from `idle` to `claimed`. | SDK-warm demotion rate computation. |
| `lenny_warmpool_sdk_demotions_total` | Counter | `pool`, `runtime_class` | SDK-warm pod demoted to pod-warm before assignment. | SDK-warm circuit-breaker threshold. |
| `lenny_warmpool_idle_pod_minutes` | Counter | `pool`, `resource_class` | Cumulative idle pod-minutes. | `WarmPoolIdleCostHigh` alert; warm pool cost estimation. |
| `lenny_warmpool_sdk_connect_timeout_total` | Counter | `pool` | SDK warm connection establishment timeouts. | `SDKConnectTimeout` alert. |
| `lenny_pool_bootstrap_mode` | Gauge | `pool` | 1 = bootstrap scaling active, 0 = formula-driven. | `PoolBootstrapMode` alert. |
| `lenny_pool_config_reconciliation_lag_seconds` | Gauge | `pool` | Time since last CRD reconciliation from Postgres. | `PoolConfigDrift` alert. |
| `lenny_pool_draining_sessions_total` | Gauge | `pool` | In-flight sessions during pool drain. | Drain progress monitoring. |

### Pod claim metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_pod_claim_queue_depth` | Gauge | `pool` | Claim requests waiting for a pod. | `PodClaimQueueSaturated` alert. |
| `lenny_pod_claim_queue_wait_seconds` | Histogram | `pool` | Time spent waiting in the claim queue. | Operational monitoring. |
| `lenny_pod_claim_conflict_total` | Counter | `pool` | Optimistic-lock conflicts during pod claim. | Operational monitoring. |
| `lenny_pod_claim_timeout_total` | Counter | `pool` | Pod claim attempts that timed out. | Operational monitoring. |
| `lenny_pod_claim_fallback_total` | Counter | -- | Postgres-backed fallback claim path activations. | Primary claim path health monitoring. |

### Task and concurrent mode metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_task_pod_scrub_failure_count` | Gauge | `k8s_pod_name`, `pool`, `runtime_class` | Per-pod scrub failure count in task mode. |
| `lenny_task_pod_retirement_total` | Counter | `reason`, `pool`, `runtime_class` | Pod retirements. Reasons: `task_count_limit`, `uptime_limit`, `scrub_failure_limit`. |
| `lenny_task_reuse_count` | Histogram | `pool`, `k8s_pod_name` | Tasks executed per pod in task mode. |
| `lenny_slot_failure_total` | Counter | `error_type`, `pool`, `k8s_pod_name` | Slot failures in concurrent-workspace mode. |
| `lenny_slot_pod_replacement_total` | Counter | `pool`, `k8s_pod_name` | Pod replacements triggered by slot failures. |

---

## Checkpoint metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_checkpoint_duration_seconds` | Histogram | `pool`, `level`, `trigger` | Time for quiescence through snapshot upload. | `CheckpointDurationHigh`, `CheckpointDurationBurnRate` alerts. |
| `lenny_checkpoint_stale_sessions` | Gauge | `pool`, `level` | Sessions whose last checkpoint age exceeds `periodicCheckpointIntervalSeconds`. | `CheckpointStale` alert. |
| `lenny_checkpoint_storage_bytes_total` | Gauge | `tenant_id`, `pool` | Per-tenant checkpoint storage bytes. | `CheckpointStorageHigh` alert. |
| `lenny_checkpoint_orphaned_objects_total` | Counter | `pool`, `trigger` | Increments when checkpoint abort cleanup fails to delete partial MinIO objects. | Operational monitoring. |
| `lenny_checkpoint_size_exceeded_total` | Counter | `pool`, `level` | Pre-checkpoint workspace size probe exceeds `workspaceSizeLimitBytes`. | Operational monitoring. |
| `lenny_checkpoint_storage_failure_total` | Counter | `pool`, `level`, `trigger` | Non-eviction checkpoint upload failures after all retries. | Operational monitoring. |
| `lenny_checkpoint_eviction_fallback_total` | Counter | `pool`, `had_prior_checkpoint` | Checkpoint storage fallback to Postgres minimal state. | Operational monitoring. |
| `lenny_checkpoint_eviction_partial_keys_logged_total` | Counter | `pool`, `keys_committed` | Partial MinIO key sets logged during eviction total-loss path. | Operational monitoring. |
| `lenny_checkpoint_barrier_ack_total` | Counter | `pool`, `outcome` | Barrier ack outcomes: `success`, `timeout`, `error`. | Operational monitoring. |
| `lenny_checkpoint_barrier_ack_duration_seconds` | Histogram | `pool` | Time from `CheckpointBarrier` send to `CheckpointBarrierAck` receipt. | Rolling update budget monitoring. |
| `lenny_partial_manifest_cleanup_total` | Counter | `outcome` | Partial checkpoint manifest cleanup: `success`, `failed_deleted`, `gc_collected`. | Operational monitoring. |

---

## Credential and Token Service metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_token_service_request_duration_seconds` | Histogram | `operation` | Token Service latency. Operations: `assign`, `rotate`, `refresh`. | Operational monitoring. |
| `lenny_token_service_errors_total` | Counter | `error_type` | Token Service errors by type. | `TokenServiceUnavailable` alert. |
| `lenny_token_service_circuit_state` | Gauge | -- | Token Service circuit breaker: 0=closed, 1=half-open, 2=open. | `TokenServiceUnavailable` alert. |
| `lenny_token_service_secret_reloads_total` | Counter | `secret_name`, `outcome` | Secret reload outcomes: `success`, `not_found`, `parse_error`. | Operational monitoring. |
| `lenny_oauth_token_5xx_total` | Counter | `tenant_id`, `error_type` | `/v1/oauth/token` responses that returned 5xx. Error types: `token_store_unavailable`, `internal_error`, `crypto_error`. | `TokenStoreUnavailable` alert. |
| `lenny_credential_rotation_inflight_ceiling_hit_total` | Counter | `pool`, `trigger` | Increments when the 300-second in-flight gate ceiling is hit for any rotation whose `trigger != proactive_renewal` and the adapter is forced to send `credentials_rotated` regardless of outstanding in-flight LLM requests. Non-zero values indicate a compromised or buggy runtime that failed to emit `llm_request_completed` within the ceiling. | `OutstandingInflightAtRotationCeiling` alert. |

### Credential pool metrics

These are derived from credential lifecycle counters, not directly named in the spec:

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| Credential lease assignments | Counter | `provider`, `pool`, `source` | Credential leases assigned. |
| Credential rotations | Counter | `error_type` | Rotations by error class: `rate_limit`, `auth_expired`, `provider_unavailable`. |
| Credential pool utilization | Gauge | `pool` | Active leases / total credentials. |
| Credential pool health | Gauge | `pool` | Credentials in cooldown. |
| Credential lease duration | Histogram | `pool` | Duration of credential leases. |
| Credential pre-claim mismatch | Counter | `pool`, `provider` | Pre-check passed but assignment failed. |
| `lenny_credential_proactive_renewal_exhausted_total` | Counter | `pool`, `provider` | Proactive renewal retries exhausted. |

---

## Delegation metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_delegation_budget_utilization_ratio` | Gauge | -- | Budget utilization ratio per delegation tree. | `DelegationBudgetNearExhaustion` alert. |
| `lenny_delegation_lease_extension_total` | Counter | -- | Delegation lease extensions granted. | Operational monitoring. |
| `lenny_delegation_tree_token_usage_total` | Counter | -- | Cumulative token usage across delegation trees. | Operational monitoring. |
| `lenny_delegation_budget_reconstruction_total` | Counter | `outcome` | Budget reconstruction after Redis recovery: `success`, `irrecoverable`. | `DelegationBudgetIrrecoverable` alert. |
| `lenny_delegation_tree_memory_bytes` | Gauge | `pool`, `tenant_id` | Aggregated in-memory footprint of active delegation trees. | Operational monitoring. |
| `lenny_delegation_memory_budget_utilization_ratio` | Histogram | `pool`, `tenant_id` | `current_tree_memory_bytes / maxTreeMemoryBytes` sampled at tree completion. | Operational monitoring. |
| `lenny_delegation_tree_memory_rejection_total` | Counter | `pool`, `tenant_id`, `error_type` | Rejections when adding a node would exceed `maxTreeMemoryBytes`. `error_type: memory_budget_exhausted`. | Operational monitoring. |
| `lenny_redis_lua_script_duration_seconds` | Histogram | `script` | Redis Lua execution latency for `budget_reserve` and `budget_return`. P99 > 5 ms indicates contention. | `DelegationLuaScriptLatencyHigh` alert. |
| `lenny_delegation_parallel_children_high_watermark` | Histogram | `pool`, `tenant_id` | Maximum simultaneous in-flight children per tree at completion. | Fan-out monitoring. |
| `lenny_delegation_deadlock_detected_total` | Counter | `pool` | Deadlocked subtrees detected. | Operational monitoring. |
| `lenny_delegation_deadlock_resolution_total` | Counter | `pool`, `resolution` | How deadlocks are resolved: `client_input`, `child_cancel`, `timeout`. | Operational monitoring. |
| `lenny_delegation_deadlock_duration_seconds` | Histogram | `pool` | Time from deadlock detection to resolution. | Operational monitoring. |
| `lenny_delegation_budget_return_usage_lag_total` | Counter | -- | Budget return unable to read parent's actual usage counter. | Operational monitoring. |
| `lenny_delegation_budget_keys_expired_total` | Counter | `pool`, `tenant_id` | Lua script returns `BUDGET_KEYS_EXPIRED`. | `DelegationBudgetKeysExpired` alert. |
| Delegation depth distribution | Histogram | -- | Distribution of delegation depths across trees. | Operational monitoring. |
| Delegation tree size distribution | Histogram | -- | Distribution of tree sizes. | Operational monitoring. |

### Delegation tree recovery metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_delegation_tree_recovery_duration_seconds` | Histogram | `pool`, `outcome` | Recovery duration: `full_success`, `partial_failure`, `total_timeout`. |
| `lenny_delegation_tree_recovery_timeout_total` | Counter | `pool`, `timeout_type` | Timeout type: `level` or `tree`. |
| `lenny_orphan_cleanup_runs_total` | Counter | -- | Background orphan cleanup job executions. |
| `lenny_orphan_tasks_terminated` | Counter | -- | Orphan tasks terminated by cleanup job. |
| `lenny_orphan_tasks_active` | Gauge | -- | Currently active orphan tasks awaiting cleanup. |
| `lenny_orphan_tasks_active_per_tenant` | Gauge | `tenant_id` | Per-tenant active orphan tasks. |

---

## Elicitation metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_elicitation_roundtrip_seconds` | Histogram | -- | Round-trip latency for elicitation requests. | Operational monitoring. |
| `lenny_elicitation_pending` | Gauge | -- | Currently pending elicitation requests. | `ElicitationBacklogHigh` alert. |
| `lenny_elicitation_suppressed_total` | Counter | -- | Elicitations auto-suppressed (depth policy). | Operational monitoring. |
| `lenny_elicitation_timeout_total` | Counter | -- | Elicitations timed out. | Operational monitoring. |

---

## Upload metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| Upload bytes/second | Counter | -- | Upload throughput. |
| Upload queue depth | Gauge | -- | Pending uploads. |
| `lenny_upload_extraction_aborted_total` | Counter | `error_type` | Aborted extractions. Classes: `zip_bomb`, `size_limit`, `path_traversal`, `symlink`, `format_error`. |

---

## Infrastructure metrics

### Postgres and Redis

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| Postgres connection pool utilization | Gauge | `service_instance_id` | Per-replica connection pool usage. | Operational monitoring. |
| Redis memory usage | Gauge | -- | Redis memory consumption. | `RedisMemoryHigh` alert. |
| Redis eviction rate | Counter | -- | Redis key evictions. | Operational monitoring. |
| mTLS handshake latency | Histogram | -- | Gateway-to-pod mTLS latency. | Operational monitoring. |
| `lenny_dual_store_unavailable` | Gauge | -- | 1 when both Postgres and Redis are unreachable. | `DualStoreUnavailable` alert. |

### Coordination and reconciliation

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_coordinator_handoff_stale_total` | Counter | -- | Generation-stale rejection during handoff. | Operational monitoring. |
| `lenny_orphan_session_reconciliations_total` | Counter | -- | Orphan sessions forcibly transitioned to `failed`. | Operational monitoring. |
| `lenny_adapter_coordinator_hold` | Gauge | -- | 1 while adapter is in hold state. | Operational monitoring. |
| `lenny_coordinator_handoff_duration_seconds` | Histogram | `pool`, `outcome` | Handoff duration: `success`, `fenced`, `timeout`. | `CoordinatorHandoffSlow` alert. |
| `lenny_coordinator_fence_retry_total` | Counter | `pool` | Coordinator fence retries. | Operational monitoring. |
| `lenny_coordinator_fence_relinquished_total` | Counter | `pool` | Coordinator relinquishes leadership after fence failures. | Operational monitoring. |

### Controller metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_controller_leader_lease_renewal_age_seconds` | Gauge | `controller` | Seconds since leader last renewed its lease. | `ControllerLeaderElectionFailed` alert. |
| `lenny_controller_queue_overflow_total` | Counter | -- | Reconciliation events dropped due to queue overflow. | Operational monitoring. |
| `lenny_orphaned_claims_total` | Counter | `pool` | Orphaned SandboxClaim deletions. | `SandboxClaimOrphanRateHigh` alert. |
| `lenny_sandboxclaim_guard_rejections_total` | Counter | `service_instance_id` | Admission webhook rejections of double-claim attempts. | Bug detection. |

### PodRegistry metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_pod_registry_operation_duration_seconds` | Histogram | `operation`, `pool` | Per-operation latency: `get`, `update_state`, `claim`, `release`, `list`, `count`, `create`, `delete`, `watch`. |
| `lenny_pod_registry_error_total` | Counter | `operation`, `pool` | Per-operation error count. |
| `lenny_pod_registry_watch_lag_seconds` | Gauge | `pool`, `implementation` | Delay between state update and `WatchPods` delivery. |

---

## Runtime upgrade metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_runtime_upgrade_state` | Gauge | `pool`, `state` | Current upgrade state: `pending`, `expanding`, `draining`, `contracting`, `complete`, `paused`. | `RuntimeUpgradeStuck` alert. |
| `lenny_runtime_upgrade_phase_duration_seconds` | Gauge | `pool`, `phase` | Time spent in current upgrade phase. | Operational monitoring. |
| `lenny_runtime_upgrade_draining_sessions` | Gauge | `pool` | Sessions still draining during upgrade. | Drain progress monitoring. |

---

## Workspace seal metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_workspace_seal_duration_seconds` | Histogram | `pool`, `outcome` | Seal completion time: `success`, `timeout`. | `WorkspaceSealStuck` alert. |

---

## Observability and audit metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_audit_grant_drift_total` | Counter | -- | Unexpected UPDATE/DELETE grants detected on audit tables. | `AuditGrantDrift` alert. |
| `lenny_pgaudit_grant_events_total` | Counter | `statement_type` | pgaudit events forwarded to sink: `GRANT`, `REVOKE`, `DDL`. | `PgAuditSinkDeliveryFailed` alert. |
| `lenny_mcp_deprecated_version_active_sessions` | Gauge | -- | Sessions on deprecated MCP protocol versions. | Deprecation monitoring. |
| `lenny_circuit_breaker_open` | Gauge | `circuit_name` | 1 when breaker is open, 0 when closed. | `CircuitBreakerActive` alert. |
| `lenny_audit_redaction_receipt_missing_total` | Counter | -- | Rows classified `chainIntegrity=redacted_gdpr` where the corresponding signed `RedactionReceipt` is absent, signature-invalid, or the `(original_hash, new_hash)` pair does not match the observed chain rewrite. Steady-state value is zero. | `AuditRedactionReceiptMissing` alert. |
| `lenny_playground_dev_tenant_not_seeded_total` | Counter | -- | `/playground/*` requests rejected with `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` because the `authMode=dev` configured `devTenantId` was not yet present in Postgres. Should be non-zero only during the post-install bootstrap window. | Operational monitoring. |

---

## Disaster recovery metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_restore_test_success` | Gauge | -- | 1 if latest automated restore test passed, 0 if failed. Emitted by `lenny-restore-test` CronJob. | Restore test monitoring. |
| `lenny_restore_test_duration_seconds` | Gauge | -- | Elapsed time of latest automated restore test. | Restore test monitoring. |
| `lenny_session_eviction_total_loss_total` | Counter | -- | Both MinIO and Postgres unavailable during eviction checkpoint. | `SessionEvictionTotalLoss` alert. |

---

## Billing metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_billing_write_ahead_buffer_utilization` | Gauge | `tenant_id` | Ratio of in-memory buffer used to `billingFlushMaxPending`. | `BillingWriteAheadBufferHigh` alert. |
| `lenny_billing_redis_stream_depth` | Gauge | `tenant_id` | Billing events staged in Redis awaiting Postgres flush. | `BillingStreamBackpressure` alert. |
| `lenny_billing_correction_pending_total` | Gauge | `state` | Correction approval queue: `pending`, `approved`, `rejected`, `expired`. | `BillingCorrectionApprovalBacklog` alert. |

---

## EventBus metrics

Events on the EventBus are wrapped in a CloudEvents v1.0.2 envelope; see [CloudEvents catalog](cloudevents-catalog.md) for the `type` values emitted.

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_event_bus_publish_total` | Counter | `topic` | Total publishes per topic. | -- |
| `lenny_event_bus_publish_duration_seconds` | Histogram | `topic` | Time to publish to underlying transport. | -- |
| `lenny_event_bus_publish_dropped_total` | Counter | `topic`, `error_type` | Events whose durable source transaction committed but whose CloudEvents publish to the EventBus failed. Error types: `backend_unavailable`, `serialization_failed`, `timeout`. | `EventBusPublishDropped` alert. |
| `lenny_event_bus_handler_duration_seconds` | Histogram | `topic` | Time spent in handler function. | -- |
| `lenny_event_bus_handler_error_total` | Counter | `topic` | Handler error count. | -- |

---

## StoreRouter metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_store_router_scatter_gather_duration_seconds` | Histogram | `query_type` | Scatter-gather duration for `list_sessions`, `gdpr_erasure`, `tenant_deletion`, `delegation_budget_purge`. | `ScatterGatherSlowQuery` alert. |
| `lenny_store_router_scatter_gather_shard_count` | Gauge | `query_type` | Shards queried per scatter-gather invocation. | Operational monitoring. |

---

## Memory Store metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_memory_store_operation_duration_seconds` | Histogram | `operation`, `backend` | Latency for `write`, `query`, `delete`, `list`. Backend: `postgres`, `custom`. |
| `lenny_memory_store_errors_total` | Counter | `operation`, `backend`, `error_type` | Error count per operation and backend. |
| `lenny_memory_store_record_count` | Gauge | `tenant_id` | Approximate stored memory records per tenant. |

---

## Experiment metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `lenny_experiment_targeting_duration_seconds` | Histogram | `provider` | Targeting webhook latency. |
| `lenny_experiment_targeting_error_total` | Counter | `provider`, `error_type` | Targeting webhook failures. |
| `lenny_experiment_targeting_circuit_open` | Gauge | `tenant_id`, `provider` | 1 when per-tenant circuit breaker is open. |
| `lenny_experiment_sticky_cache_invalidations_total` | Counter | `experiment_id`, `transition` | Sticky user assignment cache flushes. |
| `lenny_experiment_isolation_rejections_total` | Counter | `tenant_id`, `experiment_id`, `variant_id` | Incremented each time the `ExperimentRouter` fails closed because the variant pool's `isolationProfile` is weaker than the session's `minIsolationProfile`. Paired with the `experiment.isolation_mismatch` event so operators can detect rejection-population bias without log scraping. Returns `VARIANT_ISOLATION_UNAVAILABLE` to the caller. |
| `lenny_eval_score` | Histogram | `tenant_id`, `scorer`, `variant_id` | Eval scores per variant (built-in `/eval` endpoint only). Mean via `rate(sum) / rate(count)`. Deployers whose runtimes use runtime-native eval platforms (LangSmith, Braintrust, etc.) will not have data in this metric and should configure equivalent score-regression alerts in their eval platform. |

---

## Erasure and tenant lifecycle metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_erasure_job_failed_total` | Counter | `tenant_id`, `failure_phase` | Erasure job failures. | `ErasureJobFailed` alert. |
| `lenny_tenant_deletion_duration_seconds` | Histogram | `tenant_id` | Time from `disabling` to `deleted`. | `TenantDeletionOverdue` alert. |
| `lenny_kms_key_deletion_failed_total` | Counter | `tenant_id` | KMS key deletion failures. | `KmsKeyDeletionFailed` alert. |
| `lenny_storage_quota_bytes_used` | Gauge | `tenant_id` | Per-tenant artifact storage bytes. | `StorageQuotaHigh` alert. |

---

## Alert rules

> **These are shipped defaults, not invariants.** Every threshold below (sustain windows, percentages, latency caps, utilization ratios) is rendered into the bundled `PrometheusRule` objects at chart-install time and is tunable via Helm values. Runbooks describe the qualitative direction of each alert; the numeric values in effect at a given deployment are here. Platform design invariants (for example the 5s gateway-clock self-removal bound, the token-store fail-closed behavior, or the Redis `maxmemory-policy: noeviction` requirement) are not in this table — they are called out explicitly in the spec and the runbook that covers them.

### Critical alerts

| Alert | Expression / Condition | Severity |
|:------|:-----------------------|:---------|
| `WarmPoolExhausted` | `lenny_warmpool_idle_pods == 0` for any pool for > 60s | Critical |
| `PostgresReplicationLag` | Sync replica lag > 1s for > 30s | Critical |
| `GatewayNoHealthyReplicas` | Healthy replicas below the deployment size's configured minimum for > 30s | Critical |
| `SessionStoreUnavailable` | Postgres primary unreachable for > 15s | Critical |
| `CheckpointStorageUnavailable` | Checkpoint upload to MinIO failed after all retries; Postgres fallback attempted | Critical |
| `EtcdUnavailable` | API server etcd connectivity errors sustained > 15s | Critical |
| `CredentialPoolExhausted` | Any credential pool has 0 assignable credentials for > 30s | Critical |
| `CredentialCompromised` | Revoked credential has active leases for > 30s | Critical |
| `TokenServiceUnavailable` | Token Service circuit breaker open for > 30s | Critical |
| `ControllerLeaderElectionFailed` | Lease not renewed within `leaseDuration` (15s) | Critical |
| `DedicatedDNSUnavailable` | All dedicated CoreDNS replicas have zero ready pods for > 30s | Critical |
| `CosignWebhookUnavailable` | Cosign webhook unreachable for > 60s | Critical |
| `AuditGrantDrift` | Unexpected UPDATE/DELETE grants detected on audit tables | Critical |
| `NetworkPolicyCIDRDrift` | Installed NetworkPolicy CIDRs no longer match cluster CIDRs | Critical |
| `AdmissionWebhookUnavailable` | Admission policy webhook unreachable for > 30s | Critical |
| `SandboxClaimGuardUnavailable` | SandboxClaim guard webhook unreachable for > 30s | Critical |
| `DualStoreUnavailable` | Both Postgres and Redis simultaneously unreachable | Critical |
| `DataResidencyWebhookUnavailable` | Data residency validator webhook unreachable for > 30s | Critical |
| `DataResidencyViolationAttempt` | Cross-border transfer attempt detected | Critical |
| `PgBouncerAllReplicasDown` | All PgBouncer pods have zero ready replicas (self-managed only) | Critical |
| `SessionEvictionTotalLoss` | Both MinIO and Postgres unavailable during eviction | Critical |
| `DelegationBudgetKeysExpired` | `BUDGET_KEYS_EXPIRED` returned by Lua script | Critical |
| `BillingStreamEntryAgeHigh` | Oldest billing stream entry exceeds 80% of TTL | Critical |
| `TokenStoreUnavailable` | `rate(lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}[1m]) > 0` sustained > 30s | Critical |
| `LLMUpstreamEgressAnomaly` | `rate(lenny_gateway_llm_upstream_egress_anomaly_total[1m]) > 0` | Critical |
| `ArtifactReplicationResidencyViolation` | `rate(lenny_minio_replication_residency_violation_total[5m]) > 0` — ArtifactStore replication residency preflight observed a jurisdiction-tag mismatch, missing tag, DNS rebinding, or failed destination tag-probe. Replication for the affected region is suspended. | Critical |
| `AuditRedactionReceiptMissing` | `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0` — a row classified `chainIntegrity=redacted_gdpr` has no matching signed `RedactionReceipt`. | Critical |

### Warning alerts

| Alert | Expression / Condition | Severity |
|:------|:-----------------------|:---------|
| `WarmPoolLow` | Warm pods < 25% of `minWarm` | Warning |
| `RedisMemoryHigh` | Redis memory > 80% of maxmemory | Warning |
| `CredentialPoolLow` | Available credentials < 20% of pool size | Warning |
| `GatewayActiveStreamsHigh` | Active streams > 80% of configured max | Warning |
| `GatewaySessionBudgetNearExhaustion` | Sessions/maxSessionsPerReplica > 90% for > 60s | Warning |
| `Tier3GCPressureHigh` | Fleet P99 GC pause > 50 ms for > 5 min (Scale-size deployments only) | Warning |
| `CheckpointStale` | `lenny_checkpoint_stale_sessions > 0` for > 60s | Warning |
| `CheckpointDurationHigh` | P95 checkpoint duration exceeds 2.5s over 5-min window | Warning |
| `RateLimitDegraded` | Rate limiting in fail-open mode | Warning |
| `CertExpiryImminent` | mTLS cert expiry < 1h | Warning |
| `ElicitationBacklogHigh` | Pending elicitations > 50 for > 30s | Warning |
| `DelegationBudgetNearExhaustion` | Budget utilization > 90% for any tree | Warning |
| `PodClaimQueueSaturated` | Queue depth > 25% of `minWarm` with idle pods available for > 30s | Warning |
| `GatewaySubsystemCircuitOpen` | Any subsystem circuit breaker open for > 60s | Warning |
| `LLMTranslationLatencyHigh` | `histogram_quantile(0.95, rate(lenny_gateway_llm_translation_duration_seconds_bucket[5m])) > 0.1` sustained > 5 min | Warning |
| `LLMTranslationSchemaDrift` | `rate(lenny_gateway_llm_translation_errors_total{error_type="schema_mismatch"}[5m]) > 0` sustained > 5 min | Warning |
| `PoolConfigDrift` | Postgres/CRD generation mismatch for > 60s | Warning |
| `WarmPoolReplenishmentSlow` | P95 startup duration > 2x baseline for > 5 min | Warning |
| `WarmPoolReplenishmentFailing` | Warmup failure rate > 1/min for > 5 min | Warning |
| `SDKConnectTimeout` | SDK connect timeout rate > 0.1/min for > 5 min | Warning |
| `RuntimeUpgradeStuck` | Upgrade in non-terminal state beyond phase timeout | Warning |
| `CircuitBreakerActive` | Any global breaker open > 5 min | Warning |
| `WorkspaceSealStuck` | Seal operation retrying beyond `maxWorkspaceSealDurationSeconds` | Warning |
| `CoordinatorHandoffSlow` | P95 handoff duration > 5s for > 5 min | Warning |
| `StorageQuotaHigh` | Artifact storage > 80% of tenant quota | Warning |
| `ErasureJobFailed` | Erasure job failed | Warning |
| `TenantDeletionOverdue` | Deletion exceeds 80% of the deployment size's SLA | Warning |
| `BillingStreamBackpressure` | Redis stream depth > 80% of max for > 60s | Warning |
| `PoolBootstrapMode` | Pool in bootstrap mode > 72 hours | Warning |
| `EventBusPublishDropped` | `rate(lenny_event_bus_publish_dropped_total[5m]) > 0` sustained > 5 min | Warning |
| `GatewayQueueDepthHigh` | `max by (subsystem) (lenny_gateway_{subsystem}_queue_depth)` exceeds tier-scaled threshold (Tier 1 = 50, Tier 2 = 200, Tier 3 = 800) sustained > 5 min | Warning |
| `GatewayLatencyHigh` | P95 of `lenny_gateway_{subsystem}_request_duration_seconds` exceeds tier-scaled threshold (Tier 1 = 2.0s, Tier 2 = 1.0s, Tier 3 = 0.5s) sustained > 10 min | Warning |
| `PodStateMirrorStale` | `max by (pool) (lenny_agent_pod_state_mirror_lag_seconds) > 60` sustained > 60s | Warning |
| `LegalHoldOverrideUsed` | `gdpr.legal_hold_overridden` audit event emitted — `platform-admin` invoked user erase with `acknowledgeHoldOverride: true` | Warning |
| `LegalHoldOverrideUsedTenant` | `gdpr.legal_hold_overridden_tenant` audit event emitted — `platform-admin` invoked tenant force-delete with `acknowledgeHoldOverride: true` | Warning |
| `OutstandingInflightAtRotationCeiling` | `lenny_credential_rotation_inflight_ceiling_hit_total` incremented — the 300s in-flight gate ceiling was hit on a non-proactive rotation | Warning |
| `PreStopCapFallbackRateHigh` | Per-replica combined share of 90s-conservative-fallback selections (`source="postgres_null"` + `source="cache_miss_max_tier"`) exceeds 5% over 15 min | Warning |
| `DrainReadinessWebhookUnavailable` | `lenny-drain-readiness` webhook unreachable; node drains skip MinIO health check | Warning |
| `CircuitBreakerStale` | `lenny_circuit_breaker_cache_stale_seconds > 60` on any gateway replica; admission decisions are being served against stale state | Warning |

### SLO burn-rate alerts

| Alert | SLO | Fast window | Slow window | Severity |
|:------|:----|:------------|:------------|:---------|
| `SessionCreationSuccessRateBurnRate` | >= 99.5% | 1h, 14x | 6h, 3x | Critical / Warning |
| `SessionCreationLatencyBurnRate` | P99 < 500ms | 1h, 14x | 6h, 3x | Critical / Warning |
| `SessionAvailabilityBurnRate` | >= 99.9% | 1h, 14x | 6h, 3x | Critical / Warning |
| `GatewayAvailabilityBurnRate` | >= 99.95% | 1h, 14x | 6h, 3x | Critical / Warning |
| `StartupLatencyBurnRate` | P95 < 2s (runc) | 1h, 14x | 6h, 3x | Critical / Warning |
| `StartupLatencyGVisorBurnRate` | P95 < 5s (gVisor) | 1h, 14x | 6h, 3x | Critical / Warning |
| `TTFTBurnRate` | P95 < 10s | 1h, 14x | 6h, 3x | Critical / Warning |
| `CheckpointDurationBurnRate` | P95 < 2s (<=100MB) | 1h, 14x | 6h, 3x | Critical / Warning |
