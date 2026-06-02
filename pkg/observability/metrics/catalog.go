// SPDX-License-Identifier: MIT

// This file declares the platform metrics catalog as a typed surface.
// Each MetricSpec transcribes one metric definition (name and type)
// from a spec metric table. The catalog is a type-level enumeration of
// what the platform exports; the spec table prose remains the source of
// truth for full label dimensions and semantics.
//
// The catalog covers the §16.1 canonical metric table and the
// §25-introduced metrics enumerated in §16.8 (audit chain integrity,
// redaction-receipt, MinIO replication, region-unresolvable, and
// restore-artifact series). Both surfaces share this single typed
// catalog so an alert expression can be validated against one
// enumeration. F-16.8.5.

package metrics

import "sort"

// MetricType is the Prometheus metric type from the §16.1 table's
// "Type" column.
type MetricType string

const (
	// TypeCounter is a monotonically increasing counter.
	TypeCounter MetricType = "counter"
	// TypeGauge is a value that can go up or down.
	TypeGauge MetricType = "gauge"
	// TypeHistogram is a bucketed distribution.
	TypeHistogram MetricType = "histogram"
)

// IsValid reports whether t is one of the §16.1 metric types.
func (t MetricType) IsValid() bool {
	switch t {
	case TypeCounter, TypeGauge, TypeHistogram:
		return true
	}
	return false
}

// MetricSpec is one §16.1 metric definition. Name is the Prometheus
// metric name (the lenny_ identifier from the §16.1 table). Type is
// the §16.1 "Type" column. Help is a short human description drawn
// from the metric's row in the §16.1 table.
type MetricSpec struct {
	// Name is the Prometheus metric name, e.g.
	// "lenny_gateway_active_sessions". Required; must carry the
	// lenny_ prefix per §16.1.
	Name string

	// Type is the §16.1 metric type.
	Type MetricType

	// Help is a one-line description of the metric.
	Help string
}

// metricCatalog is the §16.1 metric table transcribed in spec order.
// Rows that §16.1 defines as a metric pair (a counter and a companion
// gauge in one row) appear here as two entries.
var metricCatalog = []MetricSpec{
	{"lenny_gateway_active_sessions", TypeGauge, "Active sessions known to a gateway replica"},
	{"lenny_warmpool_idle_pods", TypeGauge, "Warm pods available in the idle state"},
	{"lenny_warmpool_stale_pods", TypeGauge, "Warm pods idle beyond the pool maxIdleSeconds threshold"},
	// lenny_task_pod_scrub_failure_count is a per-pod cumulative scrub
	// failure count. Labeled by k8s_pod_name so each pod's series is
	// independent. spec: §5.2 line 446 — task-mode scrub. Reset semantics:
	// the series is monotonically incremented over the pod's lifetime,
	// reset to zero on a fresh pod (a new k8s_pod_name produces a new
	// series), and removed when the pod is deleted (the scrape just
	// stops; Prometheus retains the historical series per its own
	// retention). The emitter compares the running value against the
	// pool's TaskPolicy.MaxScrubFailures (default 3) to drive pod
	// retirement.
	{"lenny_task_pod_scrub_failure_count", TypeGauge, "Task-mode per-pod scrub failure count (cumulative per k8s_pod_name; resets only on pod replacement)"},
	{"lenny_task_pod_retirement_total", TypeCounter, "Task-mode pod retirements by reason"},
	{"lenny_slot_failure_total", TypeCounter, "Concurrent-workspace slot failure count"},
	{"lenny_slot_pod_replacement_total", TypeCounter, "Concurrent-workspace slot pod replacement count"},
	{"lenny_session_startup_duration_seconds", TypeHistogram, "End-to-end session startup duration"},
	{"lenny_session_time_to_first_token_seconds", TypeHistogram, "End-to-end time to first token"},
	{"lenny_session_creation_duration_seconds", TypeHistogram, "Session creation latency, per-phase breakdown"},
	{"lenny_pod_claim_duration_seconds", TypeHistogram, "Time from session creation request to warm pod claim"},
	{"lenny_pod_state_transition_duration_seconds", TypeHistogram, "Pod state transition durations"},
	{"lenny_upload_bytes_total", TypeCounter, "Cumulative bytes written through the gateway upload handler"},
	{"lenny_upload_queue_depth", TypeGauge, "In-flight upload requests queued in the gateway upload handler"},
	{"lenny_upload_extraction_aborted_total", TypeCounter, "Upload extraction aborts by error type"},
	{"lenny_tokens_consumed_total", TypeCounter, "Cumulative LLM input+output tokens by tenant and runtime class"},
	{"lenny_session_retry_total", TypeCounter, "Session-level retry attempts by failure class"},
	{"lenny_session_resume_attempts_total", TypeCounter, "Session resume attempts by outcome"},
	{"lenny_inbox_drain_failure_total", TypeCounter, "Atomic inbox-to-DLQ drain failures"},
	{"lenny_inbox_duplicate_suppressed_total", TypeCounter, "Inbox duplicate redeliveries suppressed"},
	{"lenny_inbox_redis_unavailable_total", TypeCounter, "Durable-inbox enqueues failed because Redis is unreachable"},
	{"lenny_delegation_depth", TypeHistogram, "Per-session delegation depth at session completion"},
	{"lenny_delegation_tree_size", TypeHistogram, "Delegation tree node count at tree completion"},
	{"lenny_gateway_replica_count", TypeGauge, "Number of ready gateway replicas"},
	{"lenny_gateway_active_streams", TypeGauge, "Open streaming connections on a gateway replica"},
	{"lenny_gateway_request_queue_depth", TypeGauge, "Requests queued on a gateway replica awaiting a handler"},
	{"lenny_gateway_rejection_rate", TypeGauge, "Gateway requests rejected with 429/503 per second"},
	{"lenny_pdb_blocked_evictions_total", TypeCounter, "PodDisruptionBudget-blocked evictions"},
	{"lenny_policy_denials_total", TypeCounter, "Policy engine rejections by error type"},
	{"lenny_noenvironmentpolicy_allowall_total", TypeCounter, "noEnvironmentPolicy allow-all RBAC-config writes"},
	{"lenny_checkpoint_duration_seconds", TypeHistogram, "End-to-end checkpoint wall time"},
	{"lenny_checkpoint_size_bytes", TypeHistogram, "Uploaded checkpoint snapshot byte count"},
	{"lenny_checkpoint_stale_sessions", TypeGauge, "Active sessions whose last checkpoint age exceeds the interval"},
	{"lenny_checkpoint_barrier_ack_total", TypeCounter, "CheckpointBarrier ack outcomes per pod"},
	{"lenny_checkpoint_barrier_ack_duration_seconds", TypeHistogram, "CheckpointBarrier send-to-ack duration per pod"},
	{"lenny_coordinator_resume_deduplicated_total", TypeCounter, "Tool calls skipped at coordinator handoff"},
	{"lenny_prestop_cap_selection_total", TypeCounter, "preStop tiered checkpoint cap selections by source"},
	{"lenny_prestop_barrier_target_source_total", TypeCounter, "preStop CheckpointBarrier target-set source"},
	{"lenny_gateway_sigkill_streams_total", TypeCounter, "In-flight streams forcibly terminated at the SIGKILL deadline"},
	{"lenny_checkpoint_eviction_fallback_total", TypeCounter, "Checkpoint storage fallbacks to Postgres minimal state"},
	{"lenny_postgres_connection_pool_utilization", TypeGauge, "In-use Postgres connections relative to pool size"},
	{"lenny_redis_memory_used_bytes", TypeGauge, "Redis used_memory relative to maxmemory"},
	{"lenny_redis_evicted_keys_total", TypeCounter, "Cumulative keys evicted by the Redis maxmemory policy"},
	{"lenny_quota_redis_fallback_total", TypeCounter, "Quota/rate-limit Redis fallback activations"},
	{"lenny_quota_failopen_cumulative_seconds", TypeGauge, "Cumulative quota fail-open seconds per replica (1h window)"},
	{"lenny_mtls_handshake_duration_seconds", TypeHistogram, "mTLS handshake latency on the gateway-pod channels"},
	{"lenny_interceptor_mtls_handshake_duration_seconds", TypeHistogram, "mTLS handshake latency on the gateway-interceptor link"},
	{"lenny_credential_lease_assignments_total", TypeCounter, "Credential leases issued from a pool by source"},
	{"lenny_credential_rotation_total", TypeCounter, "Credential rotations by error type"},
	{"lenny_credential_pool_utilization", TypeGauge, "Ratio of active leases to total pool credentials"},
	{"lenny_credential_pool_cooldown_count", TypeGauge, "Credentials currently in cooldown within a pool"},
	{"lenny_credential_lease_duration_seconds", TypeHistogram, "Wall-clock duration of each issued credential lease"},
	{"lenny_credential_preclaim_mismatch_total", TypeCounter, "Pre-claim availability check passed but assignment failed"},
	{"lenny_credential_rotation_inflight_ceiling_hit_total", TypeCounter, "Credential rotation 300s in-flight ceiling hits"},
	{"lenny_credential_revoked_with_active_leases", TypeGauge, "Pool-scoped revoked credentials with active leases"},
	{"lenny_user_credential_revoked_with_active_leases", TypeGauge, "User-scoped revoked credentials with active leases"},
	{"lenny_elicitation_roundtrip_seconds", TypeHistogram, "Elicitation round-trip latency"},
	{"lenny_elicitation_pending", TypeGauge, "Elicitation requests pending"},
	{"lenny_elicitation_suppressed_total", TypeCounter, "Elicitation requests suppressed"},
	{"lenny_elicitation_timeout_total", TypeCounter, "Elicitation requests timed out"},
	{"lenny_elicitation_content_tamper_detected_total", TypeCounter, "Elicitation content tamper detections"},
	{"lenny_delegation_budget_utilization_ratio", TypeGauge, "Delegation budget utilization ratio across active trees"},
	{"lenny_delegation_lease_extension_total", TypeCounter, "Delegation lease extensions"},
	{"lenny_delegation_tree_token_usage_total", TypeCounter, "Delegation tree token usage"},
	{"lenny_delegation_budget_reconstruction_total", TypeCounter, "Delegation budget reconstruction events by outcome"},
	{"lenny_delegation_tree_memory_bytes", TypeGauge, "Delegation tree in-memory footprint across active trees"},
	{"lenny_delegation_memory_budget_utilization_ratio", TypeHistogram, "Delegation memory budget utilization ratio at tree completion"},
	{"lenny_delegation_tree_memory_rejection_total", TypeCounter, "delegate_task calls rejected on the memory budget"},
	{"lenny_redis_lua_script_duration_seconds", TypeHistogram, "Redis Lua execution latency for delegation budget scripts"},
	{"lenny_delegation_parallel_children_high_watermark", TypeHistogram, "Max simultaneous in-flight children per delegation tree"},
	{"lenny_delegation_deadlock_detected_total", TypeCounter, "Delegation deadlock detections"},
	{"lenny_delegation_deadlock_resolution_total", TypeCounter, "Delegation deadlock resolutions by resolution"},
	{"lenny_delegation_deadlock_duration_seconds", TypeHistogram, "Time from deadlock detection to resolution"},
	{"lenny_delegation_budget_return_usage_lag_total", TypeCounter, "Budget returns unable to read the parent usage counter"},
	{"lenny_delegation_budget_keys_expired_total", TypeCounter, "Delegation budget keys expired during an active tree"},
	{"lenny_delegation_would_have_blocked_total", TypeCounter, "Self-recursion would-have-blocked counter by layer"},
	{"lenny_export_file_scans_total", TypeCounter, "Export file scan outcomes at PreExportMaterialization"},
	{"lenny_export_file_scan_duration_seconds", TypeHistogram, "PreExportMaterialization interceptor latency per file"},
	{"lenny_checkpoint_storage_bytes_total", TypeGauge, "Per-tenant checkpoint storage bytes"},
	{"lenny_pod_claim_queue_depth", TypeGauge, "Pod claim queue depth by pool"},
	{"lenny_pod_claim_queue_wait_seconds", TypeHistogram, "Pod claim queue wait time by pool"},
	{"lenny_pod_claim_conflict_total", TypeCounter, "Pod claim optimistic-lock conflicts by pool"},
	{"lenny_pod_claim_timeout_total", TypeCounter, "Pod claim timeouts by pool"},
	{"lenny_token_service_request_duration_seconds", TypeHistogram, "Token Service request duration by operation"},
	{"lenny_token_service_errors_total", TypeCounter, "Token Service errors by error type"},
	{"lenny_token_service_circuit_state", TypeGauge, "Token Service circuit breaker state"},
	{"lenny_token_service_secret_reloads_total", TypeCounter, "Token Service secret reloads by outcome"},
	{"lenny_gateway_subsystem_request_duration_seconds", TypeHistogram, "Per-subsystem request duration"},
	{"lenny_gateway_subsystem_errors_total", TypeCounter, "Per-subsystem errors by error type"},
	{"lenny_gateway_subsystem_queue_depth", TypeGauge, "Per-subsystem queue depth"},
	{"lenny_gateway_subsystem_circuit_state", TypeGauge, "Per-subsystem circuit breaker state by subsystem"},
	{"lenny_gateway_llm_proxy_active_connections", TypeGauge, "LLM Proxy active connections"},
	{"lenny_gateway_llm_upstream_egress_anomaly_total", TypeCounter, "Outbound connections to non-allowlisted LLM destinations"},
	{"lenny_gateway_llm_translation_duration_seconds", TypeHistogram, "Native LLM translator CPU time per leg"},
	{"lenny_gateway_llm_translation_errors_total", TypeCounter, "LLM translator failures by error type"},
	{"lenny_gateway_max_sessions_per_replica", TypeGauge, "Maximum concurrent sessions a replica can serve by delivery mode"},
	{"lenny_gateway_gc_pause_p99_ms", TypeGauge, "Process-level GC pause P99 per replica"},
	{"lenny_gateway_gc_pause_fleet_p99_ms", TypeGauge, "Fleet-wide GC pause P99 across active gateway replicas"},
	{"lenny_stream_proxy_queue_depth", TypeGauge, "Stream Proxy subsystem internal work queue depth"},
	{"lenny_stream_proxy_goroutines", TypeGauge, "Goroutines owned by the Stream Proxy subsystem on a replica"},
	{"lenny_stream_proxy_p99_attach_latency_seconds", TypeGauge, "Pre-computed P99 session-attach latency for Stream Proxy"},
	{"lenny_upload_handler_active_uploads", TypeGauge, "In-flight upload requests in the Upload Handler subsystem"},
	{"lenny_upload_handler_queue_depth", TypeGauge, "Upload Handler subsystem internal work queue depth"},
	{"lenny_upload_handler_p99_latency_seconds", TypeGauge, "Pre-computed P99 Upload Handler request latency"},
	{"lenny_mcp_fabric_active_delegations", TypeGauge, "In-flight delegation-orchestration operations on a replica"},
	{"lenny_mcp_fabric_goroutines", TypeGauge, "Goroutines owned by the MCP Fabric subsystem on a replica"},
	{"lenny_mcp_fabric_p99_orchestration_latency_seconds", TypeGauge, "Pre-computed P99 delegation-orchestration latency"},
	{"lenny_llm_proxy_upstream_goroutines", TypeGauge, "Goroutines servicing upstream LLM streaming connections"},
	{"lenny_llm_proxy_p99_ttfb_seconds", TypeGauge, "Pre-computed P99 upstream time-to-first-byte for LLM Proxy"},
	{"lenny_warmpool_pod_startup_duration_seconds", TypeHistogram, "Time from pod creation to the idle state"},
	{"lenny_warmpool_replenishment_rate", TypeGauge, "Pods per minute entering the idle state by pool"},
	{"lenny_warmpool_warmup_failure_total", TypeCounter, "Warm-up failures by error type"},
	{"lenny_warmpool_cold_start_total", TypeCounter, "Cold-start sessions served"},
	{"lenny_warmpool_fill_duration_seconds", TypeHistogram, "Time from pool creation to reaching minWarm ready pods"},
	{"lenny_warmpool_claims_total", TypeCounter, "Warm pod claims (idle to claimed transitions)"},
	{"lenny_warmpool_sdk_demotions_total", TypeCounter, "SDK-warm pods demoted to pod-warm before session assignment"},
	{"lenny_task_reuse_count", TypeHistogram, "Tasks executed on a single pod in task mode"},
	{"lenny_pool_config_reconciliation_lag_seconds", TypeGauge, "Time since the last successful CRD reconciliation"},
	{"lenny_pool_bootstrap_mode", TypeGauge, "Pool bootstrap mode flag (1 active, 0 converged)"},
	{"lenny_pool_scaling_admission_denied_total", TypeCounter, "PoolScalingController admission rejections by reason"},
	{"lenny_pool_termination_budget_exceeded_total", TypeCounter, "Pool config writes rejected on the termination budget"},
	{"lenny_sandboxclaim_guard_rejections_total", TypeCounter, "SandboxClaim double-claim admission rejections"},
	{"lenny_warmpool_idle_pod_minutes", TypeCounter, "Cumulative warm pool idle pod-minutes"},
	{"lenny_pod_claim_fallback_total", TypeCounter, "Postgres-backed fallback claim path activations"},
	{"lenny_agent_pod_state_mirror_lag_seconds", TypeGauge, "Seconds since the last agent_pod_state mirror update"},
	{"lenny_warmpool_failover_claims_served_fraction", TypeGauge, "Warm-pool claims served in the post-failover window"},
	{"lenny_pod_registry_operation_duration_seconds", TypeHistogram, "PodRegistry per-operation latency"},
	{"lenny_pod_registry_error_total", TypeCounter, "PodRegistry per-operation error count"},
	{"lenny_pod_registry_watch_lag_seconds", TypeGauge, "PodRegistry watch event delivery lag"},
	{"lenny_controller_leader_lease_renewal_age_seconds", TypeGauge, "Seconds since the controller leader last renewed its Lease"},
	{"lenny_controller_queue_overflow_total", TypeCounter, "Reconciliation events dropped on work-queue overflow"},
	{"lenny_controller_workqueue_depth", TypeGauge, "Controller reconciliation work-queue depth"},
	{"lenny_orphaned_claims_total", TypeCounter, "Orphaned SandboxClaims deleted by the GarbageCollect loop"},
	{"lenny_delegation_tree_recovery_duration_seconds", TypeHistogram, "Delegation tree recovery duration by outcome"},
	{"lenny_delegation_tree_recovery_timeout_total", TypeCounter, "Delegation tree recovery timeouts by timeout type"},
	{"lenny_orphan_cleanup_runs_total", TypeCounter, "Background orphan cleanup job executions"},
	{"lenny_orphan_tasks_terminated", TypeCounter, "Orphan tasks terminated by the cleanup job"},
	{"lenny_orphan_tasks_active", TypeGauge, "Currently active orphan tasks awaiting cleanup"},
	{"lenny_orphan_tasks_active_per_tenant", TypeGauge, "Per-tenant active orphan task count"},
	// spec: §8.10 line 1103, §16.5 OrphanTasksPerTenantHigh alert. Reflects the
	// configured maxOrphanTasksPerTenant ceiling so the alert's
	// `scalar(lenny_max_orphan_tasks_per_tenant)` threshold resolves to a single
	// series. F-8.10.13.
	{"lenny_max_orphan_tasks_per_tenant", TypeGauge, "Configured maxOrphanTasksPerTenant ceiling — drives the OrphanTasksPerTenantHigh alert threshold"},
	{"lenny_memory_store_operation_duration_seconds", TypeHistogram, "Memory store operation duration by operation and backend"},
	{"lenny_memory_store_errors_total", TypeCounter, "Memory store errors by operation, backend, and error type"},
	{"lenny_memory_store_record_count", TypeGauge, "Approximate stored memory records per tenant"},
	{"lenny_memory_store_user_count_over_threshold_total", TypeCounter, "Memory writes that leave a user at or above the per-user cap"},
	{"lenny_experiment_targeting_duration_seconds", TypeHistogram, "Experiment targeting evaluation latency by provider"},
	{"lenny_experiment_targeting_error_total", TypeCounter, "Experiment targeting evaluation failures"},
	{"lenny_experiment_targeting_circuit_open", TypeGauge, "Experiment targeting circuit breaker state"},
	{"lenny_experiment_sticky_cache_invalidations_total", TypeCounter, "Experiment sticky-cache invalidations"},
	{"lenny_experiment_isolation_rejections_total", TypeCounter, "ExperimentRouter isolation-monotonicity rejections"},
	{"lenny_session_error_total", TypeCounter, "Session errors by variant"},
	{"lenny_session_total", TypeCounter, "Sessions total by variant"},
	{"lenny_session_duration_seconds", TypeHistogram, "Per-session wall-clock duration by variant"},
	{"lenny_eval_score", TypeHistogram, "Eval score by variant"},
	{"lenny_event_bus_publish_total", TypeCounter, "EventBus publishes per topic"},
	{"lenny_event_bus_publish_duration_seconds", TypeHistogram, "EventBus publish duration per topic"},
	{"lenny_event_bus_handler_duration_seconds", TypeHistogram, "EventBus caller-supplied handler duration per topic"},
	{"lenny_event_bus_handler_error_total", TypeCounter, "EventBus handler errors per topic"},
	{"lenny_store_router_scatter_gather_duration_seconds", TypeHistogram, "Scatter-gather operation duration by query type"},
	{"lenny_store_router_scatter_gather_shard_count", TypeGauge, "Shards queried per scatter-gather invocation"},
	{"lenny_billing_write_ahead_buffer_utilization", TypeGauge, "Billing write-ahead buffer utilization by tenant"},
	{"lenny_billing_redis_stream_depth", TypeGauge, "Billing events staged in the per-tenant Redis stream"},
	{"lenny_dual_store_unavailable", TypeGauge, "Postgres primary and Redis simultaneously unreachable"},
	{"lenny_coordinator_handoff_stale_total", TypeCounter, "Generation-stale coordinator handoff rejections"},
	{"lenny_orphan_session_reconciliations_total", TypeCounter, "Orphan sessions forcibly transitioned to failed"},
	{"lenny_adapter_coordinator_hold", TypeGauge, "Adapter in hold state awaiting a new coordinator"},
	{"lenny_coordinator_handoff_duration_seconds", TypeHistogram, "3-step coordinator handoff protocol duration by outcome"},
	{"lenny_coordinator_fence_retry_total", TypeCounter, "Coordinator retries after a fencing rejection"},
	{"lenny_coordinator_fence_relinquished_total", TypeCounter, "Coordinator leadership relinquished after fence retries"},
	{"lenny_runtime_upgrade_state", TypeGauge, "Current state of the 6-state runtime upgrade machine"},
	{"lenny_runtime_upgrade_phase_duration_seconds", TypeGauge, "Wall-clock time in the current runtime upgrade phase"},
	{"lenny_runtime_upgrade_draining_sessions", TypeGauge, "Sessions still draining during a runtime upgrade"},
	{"lenny_partial_manifest_cleanup_total", TypeCounter, "Partial checkpoint manifest cleanup by outcome"},
	{"lenny_checkpoint_partial_total", TypeCounter, "Partial-manifest checkpoint writes"},
	{"lenny_checkpoint_partial_manifests_superseded_total", TypeCounter, "Prior partial manifests soft-deleted on supersede"},
	{"lenny_gc_tombstones_pruned_total", TypeCounter, "Soft-deleted rows physically removed by the tombstone sweep"},
	{"lenny_gc_runs_total", TypeCounter, "Artifact retention GC sweep invocations by outcome"},
	{"lenny_gc_artifacts_deleted", TypeCounter, "Artifacts removed by the retention GC sweep by store"},
	{"lenny_gc_errors_total", TypeCounter, "Retention GC errors observed per sweep, labelled by store"},
	{"lenny_gc_duration_seconds", TypeHistogram, "Retention GC sweep duration in seconds"},
	{"lenny_drain_readiness_checks_total", TypeCounter, "Drain readiness admission decisions by outcome"},
	{"lenny_legal_hold_checkpoint_gaps_total", TypeCounter, "Legal-hold sessions where a checkpoint gap is detected"},
	{"lenny_checkpoint_orphaned_objects_total", TypeCounter, "Checkpoint abort cleanup failed to delete partial objects"},
	{"lenny_checkpoint_size_exceeded_total", TypeCounter, "Pre-checkpoint workspace size probe exceeded the limit"},
	{"lenny_checkpoint_storage_failure_total", TypeCounter, "Non-eviction checkpoint uploads failed after all retries"},
	{"lenny_checkpoint_eviction_partial_keys_logged_total", TypeCounter, "Partial MinIO key sets logged on the eviction loss path"},
	{"lenny_session_pod_released_during_suspension_total", TypeCounter, "Session pods released during suspension"},
	{"lenny_session_suspension_checkpoint_failed_total", TypeCounter, "Checkpoint attempts before suspension pod release failed"},
	{"lenny_session_derive_failure_audit_total", TypeCounter, "Derive-failure audit rows written by outcome"},
	{"lenny_erasure_job_failed_total", TypeCounter, "User-level erasure job failures by failure phase"},
	{"lenny_tenant_deletion_duration_seconds", TypeHistogram, "Time from disabling to deleted for a tenant"},
	{"lenny_kms_key_deletion_failed_total", TypeCounter, "Phase 4a KMS key deletion failures"},
	{"lenny_billing_correction_pending_total", TypeGauge, "Billing correction approval queue by state"},
	{"lenny_storage_quota_bytes_used", TypeGauge, "Per-tenant artifact storage bytes used"},
	{"lenny_tenant_storage_quota_bytes", TypeGauge, "Per-tenant configured storageQuotaBytes"},
	{"lenny_quota_user_failopen_fraction", TypeGauge, "Configured quotaUserFailOpenFraction value"},
	{"lenny_tenant_legal_hold_active_count", TypeGauge, "Sessions/artifacts with legal_hold true scoped to a tenant"},
	{"lenny_legal_hold_checkpoint_projected_growth_bytes", TypeGauge, "24-hour projected legal-hold checkpoint accumulation"},
	{"lenny_pool_draining_sessions_total", TypeGauge, "In-flight sessions during a pool drain"},
	{"lenny_mcp_deprecated_version_active_sessions", TypeGauge, "Sessions still active on a deprecated MCP protocol version"},
	{"lenny_circuit_breaker_open", TypeGauge, "Named circuit breaker open state"},
	{"lenny_circuit_breaker_rejections_total", TypeCounter, "Circuit-breaker admission rejections by limit tier"},
	{"lenny_circuit_breaker_rejections_suppressed_total", TypeCounter, "Circuit-breaker rejections suppressed by sampling"},
	{"lenny_circuit_breaker_cache_stale_seconds", TypeGauge, "Seconds since the admission breaker cache last refreshed"},
	{"lenny_circuit_breaker_cache_stale_serves_total", TypeCounter, "Admission decisions served against a stale breaker cache"},
	{"lenny_circuit_breaker_cache_initialized", TypeGauge, "Admission controller breaker cache initialized flag"},
	{"lenny_workspace_seal_duration_seconds", TypeHistogram, "Workspace seal-and-export completion time by outcome"},
	{"lenny_audit_grant_drift_total", TypeCounter, "Unexpected UPDATE/DELETE grants detected on audit tables"},
	{"lenny_pgaudit_grant_events_total", TypeCounter, "pgaudit grant events forwarded to the sink by statement type"},
	{"lenny_audit_ocsf_translation_failed_total", TypeCounter, "Per-row OCSF translation failures by error class"},
	{"lenny_audit_lock_acquire_seconds", TypeHistogram, "Per-tenant audit advisory-lock acquisition latency"},
	{"lenny_audit_concurrency_timeout_total", TypeCounter, "Audit write advisory-lock acquisition timeouts"},
	{"lenny_audit_siem_delivery_lag_seconds", TypeGauge, "Lag between latest committed and SIEM-acknowledged audit event"},
	{"lenny_audit_chain_integrity_total", TypeCounter, "Audit chain integrity classifications by state"},
	{"lenny_audit_redaction_receipt_missing_total", TypeCounter, "redacted_gdpr rows with no signature-verifying receipt"},
	{"lenny_event_bus_publish_dropped_total", TypeCounter, "EventBus publishes dropped after durable commit"},
	{"lenny_event_bus_replay_buffer_utilization", TypeGauge, "EventBus in-memory replay buffer utilization"},
	{"lenny_event_bus_retranscribe_duration_seconds", TypeHistogram, "EventBus retranscribe sweep duration per topic"},
	{"lenny_event_bus_retranscribe_attempts_total", TypeCounter, "EventBus retranscribe attempts by outcome"},
	{"lenny_oauth_token_rate_limited_total", TypeCounter, "Token endpoint rate-limit rejections by limit tier"},
	{"lenny_oauth_token_5xx_total", TypeCounter, "Token endpoint 5xx responses by error type"},
	{"lenny_oauth_token_rate_limited_sampled_total", TypeCounter, "Token endpoint rate-limit rejections suppressed by sampling"},
	{"lenny_token_revocation_propagation_seconds", TypeHistogram, "Token revocation propagation latency by outcome"},
	{"lenny_token_validation_postgres_fallback_total", TypeCounter, "Token validations that fell back to a Postgres check"},
	{"lenny_time_drift_seconds", TypeGauge, "Gateway wall-clock signed offset from the NTP reference"},
	{"lenny_postgres_replication_lag_seconds", TypeGauge, "Seconds the Postgres replica lags the primary"},
	{"lenny_restore_test_success", TypeGauge, "Latest automated restore test pass/fail flag"},
	{"lenny_restore_test_duration_seconds", TypeGauge, "Elapsed time of the latest automated restore test"},
	{"lenny_minio_replication_lag_seconds", TypeGauge, "ArtifactStore off-cluster replication lag by region"},
	{"lenny_minio_replication_failed_total", TypeCounter, "ArtifactStore object-level replication failures by region"},
	{"lenny_minio_replication_residency_violation_total", TypeCounter, "ArtifactStore replication residency violations by region"},
	{"lenny_legal_hold_escrow_region_unresolvable_total", TypeCounter, "Phase 3.5 escrow-region resolution failures"},
	{"lenny_platform_audit_region_unresolvable_total", TypeCounter, "Platform-tenant audit region resolution failures"},
	{"lenny_restore_artifact_missing_total", TypeCounter, "Artifact rows whose MinIO object is absent during a restore"},
	{"lenny_restore_test_artifact_success_rate", TypeGauge, "Test-restore sampled-HEAD success rate against replication"},
	{"lenny_restore_test_artifact_missing_total", TypeCounter, "Test-restore sampled objects absent at the replication target"},
	{"lenny_artifact_upload_error_total", TypeCounter, "ArtifactStore PUT failures after the retry budget by error type"},
	{"lenny_gateway_kms_signing_errors_total", TypeCounter, "KMS signing errors observed by the gateway JWTSigner"},
	{"lenny_t4_kms_probe_last_success_timestamp", TypeGauge, "Unix time of the last successful T4 KMS probe"},
	{"lenny_t4_kms_probe_result_total", TypeCounter, "T4 KMS probe results by outcome"},
	{"lenny_warmpool_sdk_connect_timeout_total", TypeCounter, "SDK-warm handshake watchdog timeouts"},
	{"lenny_crd_ssa_conflict_total", TypeCounter, "Server-Side Apply conflicts on CRD fields"},
	{"lenny_data_residency_violation_total", TypeCounter, "Data residency violations by operation"},
	{"lenny_session_eviction_total_loss_total", TypeCounter, "Session eviction total-loss events"},
	{"lenny_network_policy_cidr_drift_total", TypeCounter, "NetworkPolicy CIDR drift detections by direction"},
	{"lenny_billing_redis_stream_oldest_entry_age_seconds", TypeGauge, "Age of the oldest unacknowledged billing Redis entry"},
	{"lenny_otlp_export_tls_handshake_total", TypeCounter, "OTLP exporter TLS handshake outcomes by result"},
	{"lenny_ops_admin_api_tls_handshake_total", TypeCounter, "lenny-ops to gateway admin-API TLS handshake outcomes"},
	// §25.6 line 2926 — diagnostic endpoint latency.
	{"lenny_diagnostics_request_duration_seconds", TypeHistogram, "Per-diagnostic-endpoint latency for §25.6 diagnostic endpoints"},
	// §25.13 lines 4833–4835 / F-25.13.3 — bundled-rules visibility,
	// override-count visibility, and per-rule in-process evaluator
	// latency for the §16.5 alert catalog. The first two are stamped
	// at gateway boot from the rendered chart inputs; the histogram is
	// updated by the in-process tracker on every cached evaluation.
	{"lenny_alerting_rules_bundled", TypeGauge, "1 if rules are rendered in the given chart format (prometheusrule, configmap)"},
	{"lenny_alerting_rule_overrides", TypeGauge, "Count of operator-overridden rules from monitoring.alertOverrides"},
	{"lenny_alerting_rule_eval_duration_seconds", TypeHistogram, "In-process tracker evaluation latency per §16.5 rule (Prometheus fallback)"},
}

// MetricCatalog returns the §16.1 metrics catalog. The slice is fresh
// on every call so callers may sort or filter it freely.
func MetricCatalog() []MetricSpec {
	out := make([]MetricSpec, len(metricCatalog))
	copy(out, metricCatalog)
	return out
}

// MetricNames returns the §16.1 metric names, sorted.
func MetricNames() []string {
	out := make([]string, 0, len(metricCatalog))
	for _, m := range metricCatalog {
		out = append(out, m.Name)
	}
	sort.Strings(out)
	return out
}

// LookupMetric returns the §16.1 MetricSpec for a metric name and
// reports whether it was found.
func LookupMetric(name string) (MetricSpec, bool) {
	for _, m := range metricCatalog {
		if m.Name == name {
			return m, true
		}
	}
	return MetricSpec{}, false
}
