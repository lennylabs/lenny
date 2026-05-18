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
