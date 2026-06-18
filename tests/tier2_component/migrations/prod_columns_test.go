//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// prodMigrationSchema records the schema object each production
// migration from 0003 onward introduces. The migration suite asserts
// every migration's forward contract against a real Postgres and that
// its .down.sql reverses exactly that object. Keyed by migration
// number, the table also satisfies scripts/lint-migrations.sh, which
// requires every migration to be referenced by number in a test.
var prodMigrationSchema = []struct {
	migration string
	table     string
	create    bool
	columns   []string
}{
	{migration: "0003", table: "users", create: true},
	{migration: "0004", table: "connectors", create: true},
	{migration: "0005", table: "idempotency_keys", create: true},
	{migration: "0006", table: "tenants", columns: []string{"max_concurrent_sessions"}},
	{migration: "0007", table: "tenants", columns: []string{"storage_quota_bytes"}},
	{migration: "0008", table: "sessions", columns: []string{"workspace_plan"}},
	{migration: "0009", table: "users", columns: []string{"processing_restricted", "erasure_job_id"}},
	{migration: "0010", table: "sessions", columns: []string{"legal_hold"}},
	{migration: "0011", table: "sessions", columns: []string{"experiment_id", "experiment_variant_id", "experiment_inherited"}},
	{migration: "0012", table: "tenants", columns: []string{"min_isolation_profile"}},
	{migration: "0013", table: "runtime_definitions", columns: []string{"labels"}},
	{migration: "0014", table: "sessions", columns: []string{"environment"}},
	{migration: "0015", table: "runtime_definitions", columns: []string{"agent_interface"}},
	{migration: "0016", table: "runtime_definitions", columns: []string{"published_metadata"}},
	{migration: "0017", table: "runtime_definitions", columns: []string{"capability_inference_mode"}},
	{migration: "0018", table: "runtime_definitions", columns: []string{"tool_capability_overrides"}},
	{migration: "0019", table: "runtime_definitions", columns: []string{"setup_policy"}},
	{migration: "0020", table: "runtime_definitions", columns: []string{"capabilities"}},
	{migration: "0021", table: "runtime_definitions", columns: []string{"min_platform_version"}},
	// 0022 introduces the runtime policy JSONB column as task_policy; the
	// §5.2 mode collapse (migration 0167) renames it to session_policy, so
	// the apply-all chain ends with session_policy and the 0167 down renames
	// it back to task_policy before 0022's down drops it.
	{migration: "0022", table: "runtime_definitions", columns: []string{"session_policy"}},
	{migration: "0023", table: "runtime_definitions", columns: []string{"base_runtime"}},
	{migration: "0024", table: "tenants", columns: []string{"elicitation_content_integrity", "billing_erasure_policy", "no_environment_policy"}},
	{migration: "0025", table: "tenants", columns: []string{"experiment_targeting"}},
	// 0039 adds the §4 / §12.9 KMS-envelope key_version column to the
	// credentials table; the secret column's type change to BYTEA is
	// covered by TestCredentialSecretEnvelopeColumn below.
	{migration: "0039", table: "credentials", columns: []string{"secret_key_version"}},
	// 0040 adds the §5.2 concurrent-execution-mode columns to the
	// sandbox_warm_pools registry. concurrency_style is added here too, but the
	// §5.2 mode collapse retires it: migration 0167 drops the column (coupled
	// with the pkg/gateway/poolstore ConcurrencyStyle removal per the proposal),
	// so it does not exist after the full prod chain. concurrency_style is
	// intentionally absent from this list; the drop is asserted by the 0167
	// entry below.
	{migration: "0040", table: "sandbox_warm_pools", columns: []string{
		"max_concurrent", "acknowledge_process_level_isolation",
		"cleanup_timeout_seconds", "allow_cross_tenant_reuse",
	}},
	// 0042 creates the §12.8 GDPR erasure-job registry. The
	// processing-restriction trigger it also installs is covered by
	// TestProcessingRestrictionTrigger.
	{migration: "0042", table: "erasure_jobs", create: true},
	// 0043 adds the §11.2.1 billing-correction columns and the
	// stream-dedup column to billing_events.
	{migration: "0043", table: "billing_events", columns: []string{
		"corrects_sequence", "correction_reason_code", "correction_detail",
		"pod_minutes", "stream_entry_id",
	}},
	// 0044 adds the §9.4 pgvector embedding column to agent_memory,
	// completing the "Postgres + pgvector" default memory backend.
	{migration: "0044", table: "agent_memory", columns: []string{"embedding"}},
	// 0045 creates the §12.2.1 EvictionStateStore registry. The
	// table is tenant-scoped and carries the same RLS policy every
	// tenant-scoped table uses (see §12.2.1).
	{migration: "0045", table: "session_eviction_state", create: true},
	// 0046 creates the §25.5 webhook subscription registry for the
	// lenny-ops control plane. Platform-scoped (no RLS, no tenant
	// column).
	{migration: "0046", table: "ops_event_subscriptions", create: true},
	// 0047 extends lenny_tenant_guard() with the §4.2 platform-admin
	// __all__ cross-tenant bypass and the §12.3 line 53 tenant-id
	// format validation. The trigger behavior is covered by the
	// pgtenant.InAllTenants suite under
	// tests/tier2_component/rls/all_tenants_test.go.
	// 0048 creates the §9.3 connector_credentials table — the
	// per-(tenant, connector, user) OAuth refresh-token store wrapped
	// under the tenant KMS key.
	{migration: "0048", table: "connector_credentials", create: true},
	// 0049 creates the §12.5 artifact_store catalog table tracking
	// every MinIO blob with its tenant, session, lifecycle state,
	// SSE-KMS key alias, legal-hold flag, and tombstone deadline.
	{migration: "0049", table: "artifact_store", create: true},
	// 0050 adds the §4.2 session-record fields the spec lists: cwd,
	// pod_assignment, recovery_generation, coordination_generation,
	// and schema_version. spec: §4.2 line 156.
	{migration: "0050", table: "sessions", columns: []string{
		"cwd", "pod_assignment", "recovery_generation",
		"coordination_generation", "schema_version",
	}},
	// 0051 rewrites every lenny_tenant_isolation policy in the
	// hard-error current_setting(..., false) form per §4.2 line 163
	// so an unset GUC raises rather than silently filtering rows out.
	// The policy bodies are covered by the §12.3 RLS suite under
	// tests/tier2_component/rls.
	// 0052 introduces the §4.2 line 177 admin-mode trigger that
	// rejects writes to runtime_tenant_access / pool_tenant_access
	// when lenny.admin_mode = 'true' is not set. The trigger behavior
	// is covered by tests/tier2_component/rls/admin_mode_test.go.
	// 0053 tenant-scopes the connectors table per §4.2 line 173;
	// covered by the §4.2 RLS suite.
	// 0054 tenant-scopes the delegation_policies table per §4.2
	// line 172; covered by the §4.2 RLS suite.
	// 0055 adds the §4.2 line 158-159 retry_count, policy enforcement
	// state, and resume window the Session Manager tracks on each
	// session row.
	{migration: "0055", table: "sessions", columns: []string{
		"retry_count", "policy_enforcement_state", "resume_eligible_until",
	}},
	// 0056 creates the §4.2 line 179 session_dlq_archive scaffold —
	// the tenant-scoped table the future DLQ archive feature writes
	// to. v1 has no consumer; the migration lands the table, the
	// composite PK (tenant_id, session_id, message_id), the
	// lenny_tenant_guard trigger, and the lenny_tenant_isolation
	// policy.
	{migration: "0056", table: "session_dlq_archive", create: true},
	// 0057 extends lenny_tenant_guard() with the §4.2 line 165
	// LENNY_POOLER_MODE guard: the __all__ sentinel is rejected
	// unless lenny.allow_all_sentinel = 'true' is opted in via SET
	// LOCAL by pgtenant.InAllTenants. The trigger and policy
	// behavior are covered by
	// tests/tier2_component/rls/all_tenants_test.go.
	// 0060 extends session_eviction_state with the §4.4 lines 268–273
	// columns the eviction fallback writer must populate so the §7.2
	// resume path can fence coordinator handoffs and surface
	// workspaceLost / context truncation to the runtime.
	{migration: "0060", table: "session_eviction_state", columns: []string{
		"recovery_generation", "coordination_generation",
		"conversation_cursor", "evicted_at",
		"workspace_lost", "context_truncated",
	}},
	// 0061 adds the §4.4 line 258 freshness timestamp the
	// `lenny_checkpoint_stale_sessions` gauge / `CheckpointStale`
	// alert reads. The gateway updates it on every successful
	// checkpoint regardless of trigger (periodic, eviction,
	// pre-drain).
	{migration: "0061", table: "sessions", columns: []string{
		"last_successful_checkpoint_at",
	}},
	// 0062 creates the §4.4 lines 234 / 236 partial-checkpoint
	// manifest table. The row is the recovery-aid the gateway writes
	// when an eviction checkpoint exceeds the preStop tiered cap and
	// the workspace upload is incomplete; the resume path uses it to
	// drive the §10.1 partial-workspace reconstruction.
	{migration: "0062", table: "session_partial_checkpoint_manifest", create: true},
	// 0058 adds §4.3 / §13.3 per-dialect cap columns to issued_tokens:
	// audiences TEXT[] and dialect_cap_applied_seconds INT.
	{migration: "0058", table: "issued_tokens", columns: []string{
		"audiences", "dialect_cap_applied_seconds",
	}},
	// 0059 widens the §4.3 credential scope to include environment on both
	// credentials and connector_credentials.
	{migration: "0059", table: "credentials", columns: []string{"environment"}},
	{migration: "0059", table: "connector_credentials", columns: []string{"environment"}},
	// 0063 adds the §4.4 artifact_type column to artifact_store.
	{migration: "0063", table: "artifact_store", columns: []string{"artifact_type"}},
	// 0064 adds the §4.4 soft-delete tombstone column to session_eviction_state.
	{migration: "0064", table: "session_eviction_state", columns: []string{"deleted_at"}},
	// 0065 installs the lenny_tenant_guard trigger on session_eviction_state.
	// 0066 extends the artifact_store_artifact_type_check constraint to include
	// the session_log artifact kind; no column added.
	// 0067 creates the §4.4 session_checkpoints per-session checkpoint catalog.
	{migration: "0067", table: "session_checkpoints", create: true},
	// 0068 adds the §4.5 content-addressed workspace snapshot hash to sessions.
	{migration: "0068", table: "sessions", columns: []string{"workspace_snapshot_hash"}},
	// 0069 adds the §5.1 runtime registry fields allow_self_recursion,
	// allowed_resource_classes, and supported_providers to runtime_definitions.
	{migration: "0069", table: "runtime_definitions", columns: []string{
		"allow_self_recursion", "allowed_resource_classes", "supported_providers",
	}},
	// 0070 adds the §4.9 cache_scope field to credential_pools.
	{migration: "0070", table: "credential_pools", columns: []string{"cache_scope"}},
	// 0071 adds the §4.9 proxy-mode delivery fields to credential_pools.
	{migration: "0071", table: "credential_pools", columns: []string{
		"delivery_mode", "proxy_dialect", "proxy_endpoint",
	}},
	// 0072 adds the §5.1 credential_capabilities block to runtime_definitions.
	{migration: "0072", table: "runtime_definitions", columns: []string{"credential_capabilities"}},
	// 0073 adds the §4.9 last_used_at timestamp to credentials.
	{migration: "0073", table: "credentials", columns: []string{"last_used_at"}},
	// 0074 adds the §5.1 limits, setup_command_policy, and default_pool_config
	// blocks to runtime_definitions.
	{migration: "0074", table: "runtime_definitions", columns: []string{
		"limits", "setup_command_policy", "default_pool_config",
	}},
	// 0075 adds the §5.1 workspace_defaults, runtime_options_schema, and
	// shared_assets blocks to runtime_definitions.
	{migration: "0075", table: "runtime_definitions", columns: []string{
		"workspace_defaults", "runtime_options_schema", "shared_assets",
	}},
	// 0076 adds the §5.1 sdk_warm_blocking_paths list to runtime_definitions.
	{migration: "0076", table: "runtime_definitions", columns: []string{"sdk_warm_blocking_paths"}},
	// 0077 adds the §4.9 credential_policy block to tenants.
	{migration: "0077", table: "tenants", columns: []string{"credential_policy"}},
	// 0078 adds the §4.9 cache_policy block to credential_pools.
	{migration: "0078", table: "credential_pools", columns: []string{"cache_policy"}},
	// 0079 adds the §13.2 egress_profile column to sandbox_warm_pools.
	{migration: "0079", table: "sandbox_warm_pools", columns: []string{"egress_profile"}},
	// 0080 adds a partial index on sessions(pod_assignment) for §5.2 slot-counter
	// rehydration; no column added.
	// 0081 adds the §5.2 / §12.9 workspace_tier column to runtime_definitions.
	{migration: "0081", table: "runtime_definitions", columns: []string{"workspace_tier"}},
	// 0082 replaces the single-column idempotency_keys index with a composite
	// (tenant_id, stored_at) index for the §11.5 GC sweep; no column added.
	// 0083 adds the §4.6.2 pool_config_generation counter to sandbox_warm_pools.
	{migration: "0083", table: "sandbox_warm_pools", columns: []string{"pool_config_generation"}},
	// 0084 persists the §7.1 execution_mode and scrub_policy on sessions.
	{migration: "0084", table: "sessions", columns: []string{"execution_mode", "scrub_policy"}},
	// 0085 adds the §5.2 task_policy JSONB column to sandbox_warm_pools; migration
	// 0167 renames it to session_policy (asserted by the 0167 entry below), so
	// task_policy is intentionally absent from this list.
	// 0086 adds the §7.1 metadata JSONB column to sessions.
	{migration: "0086", table: "sessions", columns: []string{"metadata"}},
	// 0087 persists the §7.3 retry_policy and last_checkpoint_workspace_bytes on
	// sessions.
	{migration: "0087", table: "sessions", columns: []string{
		"retry_policy", "last_checkpoint_workspace_bytes",
	}},
	// 0088 adds the §7.3 last_seq monotonic counter to sessions.
	{migration: "0088", table: "sessions", columns: []string{"last_seq"}},
	// 0089 persists the §7.3 workspace_root captured at first Bind on sessions.
	{migration: "0089", table: "sessions", columns: []string{"workspace_root"}},
	// 0090 persists the §8.2 tracing_context and §8.3 cascade_on_failure on
	// sessions.
	{migration: "0090", table: "sessions", columns: []string{"tracing_context", "cascade_on_failure"}},
	// 0091 adds the §10.6 environment_id column to billing_events.
	{migration: "0091", table: "billing_events", columns: []string{"environment_id"}},
	// 0092 adds a composite index on sessions(tenant_id, user_id) for the §11.4
	// full_revoke lookup; no column added.
	// 0093 grants DELETE on billing_events to lenny_erasure for §12.8 Phase 4
	// tenant teardown; no column added.
	// 0094 persists the §8.5 tree_visibility delegation boundary on sessions.
	{migration: "0094", table: "sessions", columns: []string{"tree_visibility"}},
	// 0095 persists the §10.7 delegation_depth on sessions.
	{migration: "0095", table: "sessions", columns: []string{"delegation_depth"}},
	// 0097 adds the §10.6 line 665 tenant RBAC-config blob (identityProvider,
	// tokenPolicy, capabilities taxonomy, mcpAnnotationMapping overrides)
	// as the rbac_config jsonb column on tenants.
	{migration: "0097", table: "tenants", columns: []string{"rbac_config"}},
	// 0098 adds the §10.7 idempotency_key column to eval_results.
	{migration: "0098", table: "eval_results", columns: []string{"idempotency_key"}},
	// 0099 persists the §8.2 delegation_lease slice on sessions.
	{migration: "0099", table: "sessions", columns: []string{"delegation_lease"}},
	// 0101 adds the §11.2 token quota columns to tenants.
	{migration: "0101", table: "tenants", columns: []string{
		"token_quota_per_window", "quota_reset_period",
	}},
	// 0102 adds a partial index on sessions(tenant_id) for the §11.2 concurrent-
	// session quota check; no column added.
	// 0103 adds the §12.9 / §15.1 workspace_tier CHECK constraint to tenants; no
	// column added.
	// 0104 creates the §11.2.1 billing_correction_pending dual-control registry.
	{migration: "0104", table: "billing_correction_pending", create: true},
	// 0105 adds the §12.8 tenant deletion lifecycle state column to tenants.
	{migration: "0105", table: "tenants", columns: []string{"state"}},
	// 0106 adds the §12.5 gc_priority column to tenants.
	{migration: "0106", table: "tenants", columns: []string{"gc_priority"}},
	// 0107 creates the §12.3 SIEM outbox forwarder checkpoint table.
	{migration: "0107", table: "siem_delivery_state", create: true},
	// 0108 creates the §12.6 agent_pod_state_notify trigger function for
	// LISTEN/NOTIFY-based pod-state watching; no column added.
	// 0109 persists the §14 CreateSession request envelope fields on sessions.
	{migration: "0109", table: "sessions", columns: []string{"env", "request_envelope"}},
	// 0110 adds the §9.2 elicitation_policy block to sandbox_warm_pools.
	{migration: "0110", table: "sandbox_warm_pools", columns: []string{"elicitation_policy"}},
	// 0111 creates the §12.4 session_leases Postgres advisory-lock fallback table.
	{migration: "0111", table: "session_leases", create: true},
	// 0112 adds the §12.5 slot_id column to session_checkpoints for per-slot
	// checkpoint retention in concurrent-workspace mode.
	{migration: "0112", table: "session_checkpoints", columns: []string{"slot_id"}},
	// 0113 adds the §11.2.1 event-type-specific ("for X events only")
	// conditional fields to billing_events as a single nullable JSONB
	// blob, completing the §11.2.1 event schema (F-11.2.12).
	{migration: "0113", table: "billing_events", columns: []string{"conditional_fields"}},
	// 0114 adds the §9.3 line 136 / §5.1 connector capability-inference
	// metadata to connectors: the inference mode, the inferred capability
	// union, the per-tool capability map, and the last-refresh timestamp
	// (F-9.3.8).
	{migration: "0114", table: "connectors", columns: []string{
		"capability_inference_mode", "capabilities", "tool_capabilities", "capabilities_refreshed_at",
	}},
	// 0115 creates the §11.2 line 29 / §12.4 line 218 durable checkpoint
	// of the §8.2 delegation tree budget counters (tree_size,
	// token_budget_consumed, tree_memory_bytes) keyed by
	// (tenant_id, root_session_id), so the Redis dlg:* counters are
	// reconstructed via the MAX rule on Redis recovery (F-11.2.5 /
	// F-12.4.8).
	{migration: "0115", table: "delegation_tree_budget", create: true},
	// 0116 creates the §25.4 ops_idempotency_keys registry for lenny-ops
	// mutating endpoints.
	{migration: "0116", table: "ops_idempotency_keys", create: true},
	// 0117 creates the §25.10 bootstrap_seed_snapshot desired-state store.
	{migration: "0117", table: "bootstrap_seed_snapshot", create: true},
	// 0118 extends the §25.5 ops_event_subscriptions registry to the full
	// webhook column set (secret hash + fingerprint, the tenant-isolation
	// columns, the generation counter, the severity filter) and adds the
	// ops_event_deliveries delivery-tracking table. Both platform-scoped.
	{migration: "0118", table: "ops_event_subscriptions", columns: []string{
		"severity", "secret_hash", "secret_fingerprint", "previous_secret_fingerprint",
		"secret_rotated_at", "description", "created_by", "created_by_tenant_id",
		"tenant_filter", "generation", "updated_at", "active",
	}},
	{migration: "0118", table: "ops_event_deliveries", create: true},
	// 0119 creates the §11.2 token_usage_checkpoint durable Redis counter
	// checkpoint table.
	{migration: "0119", table: "token_usage_checkpoint", create: true},
	// 0120 adds the §15.1 line 797 draining_since timestamp to
	// sandbox_warm_pools so the pool-drain phase persists (F-15.1.8).
	{migration: "0120", table: "sandbox_warm_pools", columns: []string{"draining_since"}},
	// 0121-0125 add the remaining §25.4 / §25.8 / §25.9 / §25.11 ops_*
	// platform-Postgres tables enumerated in §25.4 line 1455-1473
	// (F-25.4.13). All platform-scoped (no RLS, no tenant column).
	{migration: "0121", table: "ops_remediation_locks", create: true},
	{migration: "0121", table: "ops_lock_epoch", create: true},
	{migration: "0121", table: "ops_lock_conflicts", create: true},
	{migration: "0122", table: "ops_escalations", create: true},
	{migration: "0123", table: "ops_backups", create: true},
	{migration: "0123", table: "ops_backup_schedule", create: true},
	{migration: "0123", table: "ops_retention_policy", create: true},
	{migration: "0123", table: "ops_restore_state", create: true},
	{migration: "0124", table: "platform_upgrade_state", create: true},
	{migration: "0124", table: "platform_upgrade_check_cache", create: true},
	{migration: "0125", table: "audit_log_deferred_writes", create: true},
	// 0126 creates the §25.11 ops_artifact_replication_state durable region
	// replication state table.
	{migration: "0126", table: "ops_artifact_replication_state", create: true},
	// 0127 adds the §25.11 restore execution completion columns to
	// ops_restore_state.
	{migration: "0127", table: "ops_restore_state", columns: []string{
		"job_id", "ledger_confirmed_at",
		"ledger_confirmed_by", "ledger_confirmed_justification",
	}},
	// 0128 creates the §25.2 ops_operation_baselines historical p50/p90 table.
	{migration: "0128", table: "ops_operation_baselines", create: true},
	// 0130 adds the §8.6 lease-extension grant counters to
	// delegation_tree_budget.
	{migration: "0130", table: "delegation_tree_budget", columns: []string{
		"ext_tokens", "ext_seconds", "ext_children",
		"ext_parallel_children", "ext_tree_size",
		"ext_file_export_files", "ext_file_export_bytes", "updated_at",
	}},
	// 0131 / 0132 add the §15.5 item 7 schema_version column to the
	// session_messages MessageEnvelope rows (§15.4.1 line 1694) and the
	// session_checkpoints checkpoint-metadata catalog.
	{migration: "0131", table: "session_messages", columns: []string{"schema_version"}},
	{migration: "0132", table: "session_checkpoints", columns: []string{"schema_version"}},
	// 0133 adds the §12.8 erasure_salt envelope-encrypted BYTEA column to
	// tenants.
	{migration: "0133", table: "tenants", columns: []string{"erasure_salt"}},
	// 0134 creates the §24.13 / §10.5 schema_migration_phase tracking table.
	{migration: "0134", table: "schema_migration_phase", create: true},
	// 0135 creates the §25.8 platform_registry_config singleton table.
	{migration: "0135", table: "platform_registry_config", create: true},
	// 0136 creates the §25.11 ops_restore_test_results test-restore outcome
	// table.
	{migration: "0136", table: "ops_restore_test_results", create: true},
	// 0137 adds the §17.8.2 bootstrap_min_warm cold-start override column to
	// sandbox_warm_pools.
	{migration: "0137", table: "sandbox_warm_pools", columns: []string{"bootstrap_min_warm"}},
	// 0138 adds the §15.1 ETag optimistic-concurrency version counter to
	// the first batch of admin resources to adopt the contract.
	{migration: "0138", table: "custom_roles", columns: []string{"version"}},
	{migration: "0138", table: "delegation_policies", columns: []string{"version"}},
	{migration: "0138", table: "experiment_definitions", columns: []string{"version"}},
	// 0139 extends the §15.1 ETag optimistic-concurrency version counter to
	// the users and environments admin resources.
	{migration: "0139", table: "users", columns: []string{"version"}},
	{migration: "0139", table: "environments", columns: []string{"version"}},
	// 0140 extends §15.1 ETag version to connectors and credential_pools.
	{migration: "0140", table: "connectors", columns: []string{"version"}},
	{migration: "0140", table: "credential_pools", columns: []string{"version"}},
	// 0141 extends §15.1 ETag version to tenants.
	{migration: "0141", table: "tenants", columns: []string{"version"}},
	// 0142 extends §15.1 ETag version to runtime_definitions.
	{migration: "0142", table: "runtime_definitions", columns: []string{"version"}},
	// 0143 creates the §10.3 ca_rotation state-machine singleton table.
	{migration: "0143", table: "ca_rotation", create: true},
	// 0144 adds the §15.1 tenant suspension columns to tenants.
	{migration: "0144", table: "tenants", columns: []string{
		"suspended", "suspended_reason", "suspended_at", "suspended_by",
	}},
	// 0145 adds the §15.1 legal-hold provenance columns to sessions and
	// artifact_store.
	{migration: "0145", table: "sessions", columns: []string{
		"legal_hold_set_by", "legal_hold_set_at", "legal_hold_note",
	}},
	{migration: "0145", table: "artifact_store", columns: []string{
		"legal_hold_set_by", "legal_hold_set_at", "legal_hold_note",
	}},
	// 0146 adds the §15.1 role assignment provenance columns to users.
	{migration: "0146", table: "users", columns: []string{
		"role_assigned", "role_assigned_by", "role_assigned_at",
	}},
	// 0147 creates the §10.5 runtime_upgrade state-machine table.
	{migration: "0147", table: "runtime_upgrade", create: true},
	// 0148 creates the §10.1 session_checkpoint_meta barrier metadata table.
	{migration: "0148", table: "session_checkpoint_meta", create: true},
	// 0149 creates the §16.4 session_logs and stream_cursors range-partitioned
	// EventStore tables.
	{migration: "0149", table: "session_logs", create: true},
	{migration: "0149", table: "stream_cursors", create: true},
	// 0150 adds the §10.1 at-most-one-active-partial-manifest partial unique
	// index to session_partial_checkpoint_manifest; no column added.
	// 0151 adds the §4.6.2 reconciliation_resume_epoch counter to
	// sandbox_warm_pools.
	{migration: "0151", table: "sandbox_warm_pools", columns: []string{"reconciliation_resume_epoch"}},
	// 0152 adds the §14 labels column to usage_events for label-scoped usage
	// reports.
	{migration: "0152", table: "usage_events", columns: []string{"labels"}},
	// 0153 adds the §14 labels column to billing_events for label-scoped
	// metering queries.
	{migration: "0153", table: "billing_events", columns: []string{"labels"}},
	// 0154 creates the §5.1 runtime_capability_overrides per-tenant override
	// table.
	{migration: "0154", table: "runtime_capability_overrides", create: true},
	// 0155 adds the §6.1 sdk_warm_config circuit-breaker config to
	// sandbox_warm_pools.
	{migration: "0155", table: "sandbox_warm_pools", columns: []string{"sdk_warm_config"}},
	// 0156 creates the §10.7 lenny_eval_aggregates materialized view, its
	// SECURITY DEFINER refresh function, and the tenant-scoped read view;
	// no column added.
	// 0157 adds the §6.2 concurrent_max_pod_uptime_seconds retirement cap to
	// sandbox_warm_pools.
	{migration: "0157", table: "sandbox_warm_pools", columns: []string{"concurrent_max_pod_uptime_seconds"}},
	// 0158 creates the §8.8 session_usage per-session token metering table.
	{migration: "0158", table: "session_usage", create: true},
	// 0159 adds the §6.2 / §11.3 last_agent_activity_at idle-clock column to
	// sessions.
	{migration: "0159", table: "sessions", columns: []string{"last_agent_activity_at"}},
	// 0160 creates the §12.8 audit_redaction_receipts append-only receipt table.
	{migration: "0160", table: "audit_redaction_receipts", create: true},
	// 0161 creates the §4.8 / §8.3 interceptors platform-scoped registry.
	{migration: "0161", table: "interceptors", create: true},
	// 0162 adds the §12.8 force-delete hold override columns to tenants.
	{migration: "0162", table: "tenants", columns: []string{
		"force_delete_hold_override", "force_delete_justification",
		"force_delete_by", "force_delete_at",
	}},
	// 0163 creates the §8.2 / §9.2 / §16.7 deployment_config_state singleton
	// table.
	{migration: "0163", table: "deployment_config_state", create: true},
	// 0164 creates the §10.1 coordination_lease barrier-target mirror table.
	{migration: "0164", table: "coordination_lease", create: true},
	// 0165 grants column-level UPDATE on audit_log(payload,
	// payload_canonical_json) to lenny_erasure for §12.8 in-place redaction;
	// no column added.
	// 0166 creates the §12.8 legal_hold_escrow_records Phase 3.5 force-delete
	// escrow ledger.
	{migration: "0166", table: "legal_hold_escrow_records", create: true},
	// 0167 re-keys the §5.2 execution-mode CHECK on runtime_definitions to
	// (session, service), adds the §12.6 gateway-written per-pod recycle
	// counters to agent_pod_state (both nullable until the gateway first
	// writes them), drops the retired sandbox_warm_pools.concurrency_style
	// column, and renames the task_policy JSONB columns to session_policy on
	// both registries. The pool session_policy column is asserted here; the
	// runtime session_policy column is attributed to 0022 (its introducing
	// migration) above. spec: §5.2, §12.6.
	{migration: "0167", table: "agent_pod_state", columns: []string{
		"sessions_served", "scrub_failure_count",
	}},
	{migration: "0167", table: "sandbox_warm_pools", columns: []string{"session_policy"}},
	// 0168 relocates the §7.2 / §8.8 Terminated/Suspended session-condition
	// facts off Sandbox.status onto the Postgres session row (the §5.2 mode
	// collapse makes the WarmPoolController the sole writer of Sandbox.status)
	// and persists the §7.1 conversationContinuity envelope half resolved at
	// create time and frozen for the session lifetime. All columns are
	// append-only ALTER TABLE additions on the sessions row. spec: §7.1, §7.2,
	// §8.8.
	{migration: "0168", table: "sessions", columns: []string{
		"conversation_continuity", "terminated_at", "terminated_reason",
		"suspended_at", "suspended_reason",
	}},
	// 0169 adds the §4.7 / §5.1 require_so_peercred runtime-registry mirror
	// of the Runtime.spec.requireSoPeercred nonce-only activation field the
	// gateway pool admission gate evaluates. spec: §4.7, §5.1.
	{migration: "0169", table: "runtime_definitions", columns: []string{"require_so_peercred"}},
	// 0170 adds the §4.7 / §5.3 acknowledge_nonce_only_auth pool-level
	// opt-in flag, of the same class as allow_standard_isolation, that the
	// gateway pool admission path requires before a pool may reference a
	// nonce-only runtime. spec: §4.7, §5.3.
	{migration: "0170", table: "sandbox_warm_pools", columns: []string{"acknowledge_nonce_only_auth"}},
}

// spec: 12.2, 18.5
// diagnosis: a production migration's .up.sql did not create the table
// or add the column it declares. Check the CREATE TABLE / ALTER TABLE
// statement in the named migration under migrations/.
func TestProdMigrationsApplyExpectedSchema(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	for _, m := range prodMigrationSchema {
		if m.create {
			mustHaveTable(t, ctx, pg, m.table)
		}
		for _, col := range m.columns {
			mustHaveColumn(t, ctx, pg, m.table, col)
		}
	}
}

// spec: 12.2
// diagnosis: a production migration's .down.sql did not reverse its
// .up.sql. Rolling the migration back one step left the table or
// column it added in place. Check the named migration's .down.sql.
func TestProdMigrationsRollBackPerStep(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// Roll back one migration at a time, highest first, so each step
	// applies exactly one .down.sql. Reverse order keeps foreign-key
	// dependencies satisfied.
	for i := len(prodMigrationSchema) - 1; i >= 0; i-- {
		m := prodMigrationSchema[i]
		n, err := strconv.Atoi(m.migration)
		if err != nil {
			t.Fatalf("migration number %q: %v", m.migration, err)
		}
		pg.MigrateTo(t, dir, uint(n)-1)
		if m.create {
			mustNotHaveTable(t, ctx, pg, m.table)
			continue
		}
		for _, col := range m.columns {
			mustNotHaveColumn(t, ctx, pg, m.table, col)
		}
	}
}

// spec: 4, 12.9
// diagnosis: migration 0039 did not convert credentials.secret to the
// BYTEA ciphertext type, or its .down.sql did not convert it back to
// TEXT. §4 / §12.9 require the credential secret column to hold
// envelope-encrypted ciphertext (binary), not plaintext text.
func TestCredentialSecretEnvelopeColumn(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// Forward: the secret column is BYTEA after 0039 applies.
	if got := columnType(t, ctx, pg, "credentials", "secret"); got != "bytea" {
		t.Errorf("credentials.secret type after 0039: got %q, want bytea", got)
	}
	// Rolling 0039 back restores the pre-0039 TEXT type.
	pg.MigrateTo(t, dir, 38)
	if got := columnType(t, ctx, pg, "credentials", "secret"); got != "text" {
		t.Errorf("credentials.secret type after 0039 rollback: got %q, want text", got)
	}
}

// --- helpers -------------------------------------------------------------

func columnType(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) string {
	t.Helper()
	var dataType string
	err := pg.Pool.QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, col).Scan(&dataType)
	if err != nil {
		t.Fatalf("read type of %s.%s: %v", table, col, err)
	}
	return dataType
}

func mustHaveColumn(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) {
	t.Helper()
	if !columnExists(t, ctx, pg, table, col) {
		t.Errorf("expected column %s.%s to exist", table, col)
	}
}

func mustNotHaveColumn(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) {
	t.Helper()
	if columnExists(t, ctx, pg, table, col) {
		t.Errorf("expected column %s.%s to be absent", table, col)
	}
}

func columnExists(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) bool {
	t.Helper()
	var exists bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, col).Scan(&exists)
	if err != nil {
		t.Fatalf("check column %s.%s: %v", table, col, err)
	}
	return exists
}
