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
	{migration: "0022", table: "runtime_definitions", columns: []string{"task_policy"}},
	{migration: "0023", table: "runtime_definitions", columns: []string{"base_runtime"}},
	{migration: "0024", table: "tenants", columns: []string{"elicitation_content_integrity", "billing_erasure_policy", "no_environment_policy"}},
	{migration: "0025", table: "tenants", columns: []string{"experiment_targeting"}},
	// 0039 adds the §4 / §12.9 KMS-envelope key_version column to the
	// credentials table; the secret column's type change to BYTEA is
	// covered by TestCredentialSecretEnvelopeColumn below.
	{migration: "0039", table: "credentials", columns: []string{"secret_key_version"}},
	// 0040 adds the §5.2 concurrent-execution-mode columns to the
	// sandbox_warm_pools registry.
	{migration: "0040", table: "sandbox_warm_pools", columns: []string{
		"concurrency_style", "max_concurrent", "acknowledge_process_level_isolation",
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
	// 0097 adds the §10.6 line 665 tenant RBAC-config blob (identityProvider,
	// tokenPolicy, capabilities taxonomy, mcpAnnotationMapping overrides)
	// as the rbac_config jsonb column on tenants.
	{migration: "0097", table: "tenants", columns: []string{"rbac_config"}},
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
	// 0131 / 0132 add the §15.5 item 7 schema_version column to the
	// session_messages MessageEnvelope rows (§15.4.1 line 1694) and the
	// session_checkpoints checkpoint-metadata catalog.
	{migration: "0131", table: "session_messages", columns: []string{"schema_version"}},
	{migration: "0132", table: "session_checkpoints", columns: []string{"schema_version"}},
	// 0138 adds the §15.1 ETag optimistic-concurrency version counter to
	// the first batch of admin resources to adopt the contract.
	{migration: "0138", table: "custom_roles", columns: []string{"version"}},
	{migration: "0138", table: "delegation_policies", columns: []string{"version"}},
	{migration: "0138", table: "experiment_definitions", columns: []string{"version"}},
	// 0139 extends the §15.1 ETag optimistic-concurrency version counter to
	// the users and environments admin resources.
	{migration: "0139", table: "users", columns: []string{"version"}},
	{migration: "0139", table: "environments", columns: []string{"version"}},
	// 0167 re-keys the §5.2 execution-mode enum to (session, service) and
	// adds the §12.6 gateway-written per-pod recycle counters to
	// agent_pod_state. Both counters are nullable until the gateway first
	// writes them. The concurrency_style column survives this migration;
	// its drop lands with the gateway ConcurrencyStyle field removal in the
	// poolstore mode-collapse change (pgstore still reads and writes the
	// column at HEAD). spec: §5.2, §12.6.
	{migration: "0167", table: "agent_pod_state", columns: []string{
		"sessions_served", "scrub_failure_count",
	}},
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
