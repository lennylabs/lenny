// SPDX-License-Identifier: MIT

package metrics

import (
	"strings"
	"testing"
)

// spec161Metrics is the §16.1 metric table — every metric name §16.1
// defines, including the companion metric of each metric-pair row
// (lenny_upload_queue_depth, lenny_redis_evicted_keys_total,
// lenny_checkpoint_size_bytes) and the four subsystem template
// families. Transcribed in §16.1 table order.
var spec161Metrics = []string{
	"lenny_gateway_active_sessions", "lenny_warmpool_idle_pods", "lenny_warmpool_stale_pods",
	"lenny_task_pod_scrub_failure_count", "lenny_task_pod_retirement_total", "lenny_slot_failure_total",
	"lenny_slot_pod_replacement_total", "lenny_session_startup_duration_seconds",
	"lenny_session_time_to_first_token_seconds", "lenny_session_creation_duration_seconds",
	"lenny_pod_claim_duration_seconds", "lenny_pod_state_transition_duration_seconds",
	"lenny_upload_bytes_total", "lenny_upload_queue_depth", "lenny_upload_extraction_aborted_total",
	"lenny_tokens_consumed_total", "lenny_session_retry_total", "lenny_session_resume_attempts_total",
	"lenny_inbox_drain_failure_total", "lenny_inbox_duplicate_suppressed_total",
	"lenny_inbox_redis_unavailable_total", "lenny_delegation_depth", "lenny_delegation_tree_size",
	"lenny_gateway_replica_count", "lenny_gateway_active_streams", "lenny_gateway_request_queue_depth",
	"lenny_gateway_rejection_rate", "lenny_pdb_blocked_evictions_total", "lenny_policy_denials_total",
	"lenny_noenvironmentpolicy_allowall_total", "lenny_checkpoint_duration_seconds",
	"lenny_checkpoint_size_bytes", "lenny_checkpoint_stale_sessions", "lenny_checkpoint_barrier_ack_total",
	"lenny_checkpoint_barrier_ack_duration_seconds", "lenny_coordinator_resume_deduplicated_total",
	"lenny_prestop_cap_selection_total", "lenny_prestop_barrier_target_source_total",
	"lenny_gateway_sigkill_streams_total", "lenny_checkpoint_eviction_fallback_total",
	"lenny_postgres_connection_pool_utilization", "lenny_redis_memory_used_bytes",
	"lenny_redis_evicted_keys_total", "lenny_quota_redis_fallback_total",
	"lenny_quota_failopen_cumulative_seconds", "lenny_mtls_handshake_duration_seconds",
	"lenny_interceptor_mtls_handshake_duration_seconds", "lenny_credential_lease_assignments_total",
	"lenny_credential_rotation_total", "lenny_credential_pool_utilization",
	"lenny_credential_pool_cooldown_count", "lenny_credential_lease_duration_seconds",
	"lenny_credential_preclaim_mismatch_total", "lenny_credential_rotation_inflight_ceiling_hit_total",
	"lenny_credential_revoked_with_active_leases", "lenny_user_credential_revoked_with_active_leases",
	"lenny_elicitation_roundtrip_seconds", "lenny_elicitation_pending", "lenny_elicitation_suppressed_total",
	"lenny_elicitation_timeout_total", "lenny_elicitation_content_tamper_detected_total",
	"lenny_delegation_budget_utilization_ratio", "lenny_delegation_lease_extension_total",
	"lenny_delegation_tree_token_usage_total", "lenny_delegation_budget_reconstruction_total",
	"lenny_delegation_tree_memory_bytes", "lenny_delegation_memory_budget_utilization_ratio",
	"lenny_delegation_tree_memory_rejection_total", "lenny_redis_lua_script_duration_seconds",
	"lenny_delegation_parallel_children_high_watermark", "lenny_delegation_deadlock_detected_total",
	"lenny_delegation_deadlock_resolution_total", "lenny_delegation_deadlock_duration_seconds",
	"lenny_delegation_budget_return_usage_lag_total", "lenny_delegation_budget_keys_expired_total",
	"lenny_delegation_would_have_blocked_total", "lenny_export_file_scans_total",
	"lenny_export_file_scan_duration_seconds", "lenny_checkpoint_storage_bytes_total",
	"lenny_pod_claim_queue_depth", "lenny_pod_claim_queue_wait_seconds", "lenny_pod_claim_conflict_total",
	"lenny_pod_claim_timeout_total", "lenny_token_service_request_duration_seconds",
	"lenny_token_service_errors_total", "lenny_token_service_circuit_state",
	"lenny_token_service_secret_reloads_total", "lenny_gateway_subsystem_request_duration_seconds",
	"lenny_gateway_subsystem_errors_total", "lenny_gateway_subsystem_queue_depth",
	"lenny_gateway_subsystem_circuit_state", "lenny_gateway_llm_proxy_active_connections",
	"lenny_gateway_llm_upstream_egress_anomaly_total", "lenny_gateway_llm_translation_duration_seconds",
	"lenny_gateway_llm_translation_errors_total", "lenny_gateway_max_sessions_per_replica",
	"lenny_gateway_gc_pause_p99_ms", "lenny_gateway_gc_pause_fleet_p99_ms",
	"lenny_stream_proxy_queue_depth", "lenny_stream_proxy_goroutines",
	"lenny_stream_proxy_p99_attach_latency_seconds", "lenny_upload_handler_active_uploads",
	"lenny_upload_handler_queue_depth", "lenny_upload_handler_p99_latency_seconds",
	"lenny_mcp_fabric_active_delegations", "lenny_mcp_fabric_goroutines",
	"lenny_mcp_fabric_p99_orchestration_latency_seconds", "lenny_llm_proxy_upstream_goroutines",
	"lenny_llm_proxy_p99_ttfb_seconds", "lenny_warmpool_pod_startup_duration_seconds",
	"lenny_warmpool_replenishment_rate", "lenny_warmpool_warmup_failure_total",
	"lenny_warmpool_cold_start_total", "lenny_warmpool_fill_duration_seconds",
	"lenny_warmpool_claims_total", "lenny_warmpool_sdk_demotions_total", "lenny_task_reuse_count",
	"lenny_pool_config_reconciliation_lag_seconds", "lenny_pool_bootstrap_mode",
	"lenny_pool_scaling_admission_denied_total", "lenny_pool_termination_budget_exceeded_total",
	"lenny_sandboxclaim_guard_rejections_total", "lenny_warmpool_idle_pod_minutes",
	"lenny_pod_claim_fallback_total", "lenny_agent_pod_state_mirror_lag_seconds",
	"lenny_warmpool_failover_claims_served_fraction", "lenny_pod_registry_operation_duration_seconds",
	"lenny_pod_registry_error_total", "lenny_pod_registry_watch_lag_seconds",
	"lenny_controller_leader_lease_renewal_age_seconds", "lenny_controller_queue_overflow_total",
	"lenny_controller_workqueue_depth", "lenny_orphaned_claims_total",
	"lenny_delegation_tree_recovery_duration_seconds", "lenny_delegation_tree_recovery_timeout_total",
	"lenny_orphan_cleanup_runs_total", "lenny_orphan_tasks_terminated", "lenny_orphan_tasks_active",
	"lenny_orphan_tasks_active_per_tenant", "lenny_max_orphan_tasks_per_tenant",
	"lenny_memory_store_operation_duration_seconds",
	"lenny_memory_store_errors_total", "lenny_memory_store_record_count",
	"lenny_memory_store_user_count_over_threshold_total", "lenny_experiment_targeting_duration_seconds",
	"lenny_experiment_targeting_error_total", "lenny_experiment_targeting_circuit_open",
	"lenny_experiment_sticky_cache_invalidations_total", "lenny_experiment_isolation_rejections_total",
	"lenny_session_error_total", "lenny_session_total", "lenny_session_duration_seconds",
	"lenny_eval_score", "lenny_event_bus_publish_total", "lenny_event_bus_publish_duration_seconds",
	"lenny_event_bus_handler_duration_seconds", "lenny_event_bus_handler_error_total",
	"lenny_store_router_scatter_gather_duration_seconds", "lenny_store_router_scatter_gather_shard_count",
	"lenny_billing_write_ahead_buffer_utilization", "lenny_billing_redis_stream_depth",
	"lenny_dual_store_unavailable", "lenny_coordinator_handoff_stale_total",
	"lenny_orphan_session_reconciliations_total", "lenny_adapter_coordinator_hold",
	"lenny_coordinator_handoff_duration_seconds", "lenny_coordinator_fence_retry_total",
	"lenny_coordinator_fence_relinquished_total", "lenny_runtime_upgrade_state",
	"lenny_runtime_upgrade_phase_duration_seconds", "lenny_runtime_upgrade_draining_sessions",
	"lenny_partial_manifest_cleanup_total", "lenny_checkpoint_partial_total",
	"lenny_checkpoint_partial_manifests_superseded_total", "lenny_gc_tombstones_pruned_total",
	"lenny_checkpoint_orphaned_objects_total", "lenny_checkpoint_size_exceeded_total",
	"lenny_checkpoint_storage_failure_total", "lenny_checkpoint_eviction_partial_keys_logged_total",
	"lenny_session_pod_released_during_suspension_total", "lenny_session_suspension_checkpoint_failed_total",
	"lenny_session_derive_failure_audit_total", "lenny_erasure_job_failed_total",
	"lenny_tenant_deletion_duration_seconds", "lenny_kms_key_deletion_failed_total",
	"lenny_billing_correction_pending_total", "lenny_storage_quota_bytes_used",
	"lenny_tenant_storage_quota_bytes", "lenny_quota_user_failopen_fraction",
	"lenny_tenant_legal_hold_active_count", "lenny_legal_hold_checkpoint_projected_growth_bytes",
	"lenny_pool_draining_sessions_total", "lenny_mcp_deprecated_version_active_sessions",
	"lenny_circuit_breaker_open", "lenny_circuit_breaker_rejections_total",
	"lenny_circuit_breaker_rejections_suppressed_total", "lenny_circuit_breaker_cache_stale_seconds",
	"lenny_circuit_breaker_cache_stale_serves_total", "lenny_circuit_breaker_cache_initialized",
	"lenny_workspace_seal_duration_seconds", "lenny_audit_grant_drift_total",
	"lenny_pgaudit_grant_events_total", "lenny_audit_ocsf_translation_failed_total",
	"lenny_audit_lock_acquire_seconds", "lenny_audit_concurrency_timeout_total",
	"lenny_audit_siem_delivery_lag_seconds", "lenny_audit_chain_integrity_total",
	"lenny_audit_redaction_receipt_missing_total",
	// §25.9 audit-query observability surface.
	"lenny_audit_query_duration_seconds", "lenny_audit_chain_verification_broken_total",
	"lenny_audit_chain_rechained_post_outage_total", "lenny_audit_rate_limited_total",
	"lenny_audit_scatter_gather_shards_queried",
	"lenny_event_bus_publish_dropped_total",
	"lenny_event_bus_replay_buffer_utilization", "lenny_event_bus_retranscribe_duration_seconds",
	"lenny_event_bus_retranscribe_attempts_total", "lenny_oauth_token_rate_limited_total",
	"lenny_oauth_token_5xx_total", "lenny_oauth_token_rate_limited_sampled_total",
	"lenny_token_revocation_propagation_seconds", "lenny_token_validation_postgres_fallback_total",
	"lenny_time_drift_seconds", "lenny_postgres_replication_lag_seconds",
	"lenny_backup_last_successful_timestamp", "lenny_restore_test_success",
	"lenny_restore_test_duration_seconds", "lenny_minio_replication_lag_seconds",
	"lenny_minio_replication_failed_total", "lenny_minio_replication_residency_violation_total",
	"lenny_legal_hold_escrow_region_unresolvable_total", "lenny_platform_audit_region_unresolvable_total",
	"lenny_restore_artifact_missing_total", "lenny_restore_test_artifact_success_rate",
	"lenny_restore_test_artifact_missing_total", "lenny_artifact_upload_error_total",
	"lenny_gateway_kms_signing_errors_total", "lenny_t4_kms_probe_last_success_timestamp",
	"lenny_t4_kms_probe_result_total", "lenny_warmpool_sdk_connect_timeout_total",
	"lenny_crd_ssa_conflict_total", "lenny_data_residency_violation_total",
	"lenny_session_eviction_total_loss_total", "lenny_network_policy_cidr_drift_total",
	"lenny_billing_redis_stream_oldest_entry_age_seconds", "lenny_otlp_export_tls_handshake_total",
	"lenny_ops_admin_api_tls_handshake_total",
	// §12.5 line 321 — retention GC metrics.
	"lenny_gc_runs_total", "lenny_gc_artifacts_deleted", "lenny_gc_errors_total",
	"lenny_gc_duration_seconds",
	// §12.5 line 291 — drain readiness check counter.
	"lenny_drain_readiness_checks_total",
	// §12.8 line 739 — legal-hold checkpoint-gap reconciler counter.
	"lenny_legal_hold_checkpoint_gaps_total",
	// §25.6 line 2926 — diagnostic endpoint latency histogram.
	"lenny_diagnostics_request_duration_seconds",
	// §16.1 line 713 / §25.13 lines 4833–4835 — bundled-alerting
	// observability surface.
	"lenny_alerting_rules_bundled", "lenny_alerting_rule_overrides",
	"lenny_alerting_rule_eval_duration_seconds",
}

func TestMetricCatalogIsNonEmpty(t *testing.T) {
	if len(MetricCatalog()) == 0 {
		t.Fatal("the §16.1 metric catalog must not be empty")
	}
}

// TestMetricCatalogEntriesAreWellFormed asserts every catalog entry has
// a lenny_-prefixed name, a valid §16.1 metric type, and a help string.
func TestMetricCatalogEntriesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range MetricCatalog() {
		if !strings.HasPrefix(m.Name, "lenny_") {
			t.Errorf("metric %q does not carry the lenny_ prefix required by §16.1", m.Name)
		}
		if !m.Type.IsValid() {
			t.Errorf("metric %q has an invalid type %q", m.Name, m.Type)
		}
		if strings.TrimSpace(m.Help) == "" {
			t.Errorf("metric %q has no help string", m.Name)
		}
		if seen[m.Name] {
			t.Errorf("duplicate metric %q in the catalog", m.Name)
		}
		seen[m.Name] = true
	}
}

// TestMetricCatalogIsCompleteAgainstSpec161 asserts the catalog
// contains every metric §16.1 defines. This is the completion-gate
// check that the catalog is the §16.1 surface in code.
func TestMetricCatalogIsCompleteAgainstSpec161(t *testing.T) {
	got := map[string]bool{}
	for _, m := range MetricCatalog() {
		got[m.Name] = true
	}
	for _, name := range spec161Metrics {
		if !got[name] {
			t.Errorf("§16.1 metric %q is missing from the catalog", name)
		}
	}
}

// TestMetricCatalogHasNoUnspecifiedMetrics asserts the catalog does not
// invent metrics §16.1 does not list.
func TestMetricCatalogHasNoUnspecifiedMetrics(t *testing.T) {
	allowed := map[string]bool{}
	for _, name := range spec161Metrics {
		allowed[name] = true
	}
	for _, m := range MetricCatalog() {
		if !allowed[m.Name] {
			t.Errorf("catalog metric %q is not a §16.1 metric — the catalog must transcribe §16.1, not invent metrics", m.Name)
		}
	}
}

// TestLookupMetric round-trips a known metric and rejects an unknown one.
func TestLookupMetric(t *testing.T) {
	m, ok := LookupMetric("lenny_warmpool_idle_pods")
	if !ok {
		t.Fatal("lenny_warmpool_idle_pods should be in the catalog")
	}
	if m.Type != TypeGauge {
		t.Errorf("lenny_warmpool_idle_pods type = %q, want gauge", m.Type)
	}
	if _, ok := LookupMetric("lenny_not_a_real_metric"); ok {
		t.Error("an uncatalogued metric must not be found")
	}
}

// TestMetricNamesAreSorted asserts MetricNames returns a sorted slice.
func TestMetricNamesAreSorted(t *testing.T) {
	names := MetricNames()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("MetricNames not sorted at index %d: %q > %q", i, names[i-1], names[i])
		}
	}
}
