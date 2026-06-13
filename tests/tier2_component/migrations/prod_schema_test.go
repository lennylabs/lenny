//go:build component

// SPDX-License-Identifier: MIT

// Production-schema component tests for the Phase 1.5 migrations under
// migrations/. These exercise the real §12 schema against a Postgres
// container: forward apply, rollback reversibility, the
// lenny_tenant_guard transaction isolation guard (§12.3), and the
// append-only audit/billing immutability triggers (§11.7).
//
// build-sequence §18.5 exit criteria: the migration round-trip passes
// and lenny_tenant_guard rejects a representative cross-tenant query.
package migrations_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// prodMigrations returns the absolute path to the production schema
// migrations under migrations/.
func prodMigrations(t *testing.T) string {
	t.Helper()
	return filepath.Join(schematest.RepoRoot(t), "migrations")
}

// prodTables is every table the production migrations create, paired
// with the migration that creates it. TestProdSchemaMigrationRoundTrip
// asserts each table appears after the forward apply and is gone after
// rollback, so every migration listed here is covered by a test under
// tests/tier2_component/migrations/ (the §12.0 lint-migrations rule).
var prodTables = []struct{ migration, name string }{
	{"0001", "tenants"},
	{"0001", "runtime_definitions"},
	{"0001", "sessions"},
	{"0001", "session_messages"},
	{"0001", "audit_log"},
	{"0001", "billing_events"},
	{"0001", "issued_tokens"},
	{"0001", "agent_pod_state"},
	{"0003", "users"},
	{"0004", "connectors"},
	{"0005", "idempotency_keys"},
	// Wave 1 gateway-store tables.
	{"0026", "custom_roles"},
	{"0027", "delegation_policies"},
	{"0028", "environments"},
	{"0029", "eval_results"},
	{"0030", "experiment_definitions"},
	{"0031", "interactions"},
	{"0032", "agent_memory"},
	{"0033", "sandbox_warm_pools"},
	{"0034", "usage_events"},
	{"0035", "runtime_tenant_access"},
	{"0035", "pool_tenant_access"},
	{"0036", "credentials"},
	{"0037", "credential_pools"},
	{"0038", "credential_leases"},
	// §12.8 GDPR erasure-job registry.
	{"0042", "erasure_jobs"},
}

// execTenant runs sql inside a transaction that has set
// app.current_tenant to tenant (transaction-local, mirroring the
// gateway's per-request pattern). Returns the first error encountered.
func execTenant(ctx context.Context, pg *containers.Postgres, tenant, sql string, args ...any) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// spec: 12.2, 12.3, 18.5
// diagnosis: the production migrations under migrations/ failed to
//
//	apply, or did not roll back cleanly. Check that every .up.sql
//	has a matching .down.sql and that DROP order respects FKs.
func TestProdSchemaMigrationRoundTrip(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	for _, tbl := range prodTables {
		mustHaveTable(t, ctx, pg, tbl.name)
	}
	// R-01: tenant-scoped ledgers lead their primary index with tenant_id.
	mustHavePKLeadingColumn(t, ctx, pg, "audit_log", "tenant_id")
	mustHavePKLeadingColumn(t, ctx, pg, "billing_events", "tenant_id")
	// runtime_definitions is platform-global (§5.1): keyed by name.
	mustHavePKLeadingColumn(t, ctx, pg, "runtime_definitions", "name")
	// idempotency_keys is tenant-scoped (§11.5): PK leads with tenant_id.
	mustHavePKLeadingColumn(t, ctx, pg, "idempotency_keys", "tenant_id")
	mustHaveIndex(t, ctx, pg, "sessions", "idx_sessions_tenant_created")
	mustHaveIndex(t, ctx, pg, "issued_tokens", "idx_issued_tokens_tenant_sub")
	mustHaveIndex(t, ctx, pg, "agent_pod_state", "idx_agent_pod_state_pool_state")

	// Rollback removes every object, including the database roles.
	pg.MigrateDown(t, dir)
	for _, tbl := range prodTables {
		mustNotHaveTable(t, ctx, pg, tbl.name)
	}
	for _, role := range []string{"lenny_app", "lenny_erasure"} {
		var exists bool
		if err := pg.Pool.QueryRow(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role,
		).Scan(&exists); err != nil {
			t.Fatalf("check role %q: %v", role, err)
		}
		if exists {
			t.Errorf("role %q should be dropped by the 0002 down migration", role)
		}
	}
}

// spec: 5.2, 12.6
// diagnosis: migration 0167 did not retire sandbox_warm_pools.concurrency_style
// (the §5.2 mode collapse removes the concurrent sub-variant the column
// encoded), or its .down.sql did not restore the column to its 0040
// definition (TEXT NOT NULL DEFAULT ”). A failure means the concurrent
// sub-variant column survives at HEAD, or the down does not round-trip the
// column back to the per-step 0040 baseline.
func TestProdSchemaMigrationConcurrencyStyleDrop(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// At HEAD the column is gone: 0167 dropped it.
	if columnExists(t, ctx, pg, "sandbox_warm_pools", "concurrency_style") {
		t.Error("sandbox_warm_pools.concurrency_style must be dropped by migration 0167 (§5.2 mode collapse)")
	}
	// max_concurrent survives as the per-pod bound.
	if !columnExists(t, ctx, pg, "sandbox_warm_pools", "max_concurrent") {
		t.Error("sandbox_warm_pools.max_concurrent must survive migration 0167")
	}

	// Rolling 0167 back restores the column with its 0040 definition.
	pg.MigrateTo(t, dir, 166)
	if !columnExists(t, ctx, pg, "sandbox_warm_pools", "concurrency_style") {
		t.Fatal("migration 0167 down must restore sandbox_warm_pools.concurrency_style")
	}
	if got := columnType(t, ctx, pg, "sandbox_warm_pools", "concurrency_style"); got != "text" {
		t.Errorf("restored concurrency_style type: got %q, want text", got)
	}
	if got := columnDefault(t, ctx, pg, "sandbox_warm_pools", "concurrency_style"); got != "''::text" {
		t.Errorf("restored concurrency_style default: got %q, want ''::text", got)
	}
	if columnNullable(t, ctx, pg, "sandbox_warm_pools", "concurrency_style") {
		t.Error("restored concurrency_style must be NOT NULL (the 0040 definition)")
	}
}

// spec: §12.8 lines 743-758
// diagnosis: migration 0096 must seed the reserved __preflight__ tenant
// so the §12.8 MemoryStore erasure preflight's synthetic agent_memory
// probe row satisfies the agent_memory → tenants(id) foreign key. The row
// is soft-deleted (deleted_at set) so it is inert for real traffic, and
// the down migration removes it.
func TestProdMigration0096SeedsPreflightTenant(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	var deletedAtSet bool
	if err := pg.Pool.QueryRow(
		ctx,
		`SELECT deleted_at IS NOT NULL FROM tenants WHERE id = '__preflight__'`,
	).Scan(&deletedAtSet); err != nil {
		t.Fatalf("the reserved __preflight__ tenant is absent after migration 0096: %v", err)
	}
	if !deletedAtSet {
		t.Error("the reserved __preflight__ tenant should be soft-deleted (deleted_at set) so it is inert for real traffic")
	}

	// The down migration removes the reserved tenant.
	pg.MigrateDown(t, dir)
	var stillExists bool
	if err := pg.Pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM tenants WHERE id = '__preflight__')`,
	).Scan(&stillExists); err == nil && stillExists {
		t.Error("migration 0096 down should remove the reserved __preflight__ tenant")
	}
}

// spec: 12.3, 18.5
// diagnosis: lenny_tenant_guard did not behave as specified. It must
//
//	reject writes with no app.current_tenant set and writes whose
//	tenant_id does not match the set context, and permit matching
//	writes.
func TestTenantGuard(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// tenants is platform-global (no guard): seed two tenants directly.
	for _, id := range []string{"acme", "globex"} {
		if _, err := pg.Pool.Exec(
			ctx,
			`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id,
		); err != nil {
			t.Fatalf("seed tenant %q: %v", id, err)
		}
	}

	const insertSession = `INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		VALUES (gen_random_uuid(), $1, 'created', 'echo', gen_random_uuid())`

	t.Run("rejects unset context", func(t *testing.T) {
		// A bare pool Exec runs with no app.current_tenant set.
		_, err := pg.Pool.Exec(ctx, insertSession, "acme")
		if err == nil {
			t.Fatal("insert with no app.current_tenant should be rejected")
		}
		if !strings.Contains(err.Error(), "app.current_tenant is not set") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects cross-tenant write", func(t *testing.T) {
		// Context is acme; the row claims tenant globex.
		err := execTenant(ctx, pg, "acme", insertSession, "globex")
		if err == nil {
			t.Fatal("cross-tenant insert should be rejected")
		}
		if !strings.Contains(err.Error(), "does not match app.current_tenant") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("permits matching write", func(t *testing.T) {
		if err := execTenant(ctx, pg, "acme", insertSession, "acme"); err != nil {
			t.Fatalf("matching-tenant insert should succeed: %v", err)
		}
		n := mustQueryInt(t, ctx, pg,
			`SELECT COUNT(*) FROM sessions WHERE tenant_id = 'acme'`)
		if n != 1 {
			t.Errorf("sessions for acme = %d, want 1", n)
		}
	})
}

// spec: 11.7, 18.5
// diagnosis: the append-only ledgers accepted an UPDATE or DELETE.
//
//	lenny_audit_immutability and lenny_billing_immutability must
//	reject both outside an erasure-mode transaction.
func TestLedgerImmutability(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	if _, err := pg.Pool.Exec(
		ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00')`,
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	t.Run("audit_log rejects update and delete", func(t *testing.T) {
		if err := execTenant(ctx, pg, "acme",
			`INSERT INTO audit_log (tenant_id, sequence_number, event_type, payload, payload_canonical_json)
			 VALUES ('acme', 1, 'test.event', '{}', '{}')`); err != nil {
			t.Fatalf("audit insert should succeed: %v", err)
		}
		if err := execTenant(ctx, pg, "acme",
			`UPDATE audit_log SET event_type = 'tampered' WHERE tenant_id = 'acme'`); err == nil {
			t.Error("audit_log UPDATE should be rejected")
		}
		if err := execTenant(ctx, pg, "acme",
			`DELETE FROM audit_log WHERE tenant_id = 'acme'`); err == nil {
			t.Error("audit_log DELETE should be rejected")
		}
	})

	t.Run("audit_log permits the migration-0041 bookkeeping update", func(t *testing.T) {
		// Migration 0041 narrows lenny_audit_immutability so the §11.7
		// OCSF and §12.3.7 EventBus state machines can advance the
		// bookkeeping columns while the hash-input payload stays frozen.
		if err := execTenant(ctx, pg, "acme",
			`INSERT INTO audit_log (tenant_id, sequence_number, event_type, payload, payload_canonical_json)
			 VALUES ('acme', 41, 'test.event', '{}', '{}')`); err != nil {
			t.Fatalf("audit insert should succeed: %v", err)
		}
		if err := execTenant(ctx, pg, "acme",
			`UPDATE audit_log SET retry_count = retry_count + 1
			 WHERE tenant_id = 'acme' AND sequence_number = 41`); err != nil {
			t.Errorf("a bookkeeping-only UPDATE must be permitted after migration 0041: %v", err)
		}
		if err := execTenant(ctx, pg, "acme",
			`UPDATE audit_log SET payload = '{"x":1}'
			 WHERE tenant_id = 'acme' AND sequence_number = 41`); err == nil {
			t.Error("a payload UPDATE must still be rejected after migration 0041")
		}
	})

	t.Run("billing_events rejects update and delete", func(t *testing.T) {
		if err := execTenant(ctx, pg, "acme",
			`INSERT INTO billing_events (tenant_id, sequence_number, event_type)
			 VALUES ('acme', 1, 'session.created')`); err != nil {
			t.Fatalf("billing insert should succeed: %v", err)
		}
		if err := execTenant(ctx, pg, "acme",
			`UPDATE billing_events SET event_type = 'tampered' WHERE tenant_id = 'acme'`); err == nil {
			t.Error("billing_events UPDATE should be rejected")
		}
		if err := execTenant(ctx, pg, "acme",
			`DELETE FROM billing_events WHERE tenant_id = 'acme'`); err == nil {
			t.Error("billing_events DELETE should be rejected")
		}
	})
}

// spec: 12.8
// diagnosis: the migration-0042 processing-restriction trigger did not
//
//	behave as specified. §12.8 (GDPR Article 18) requires that
//	users.processing_restricted cannot be cleared while a non-terminal
//	erasure job exists for that user, except for the lenny_erasure
//	role and the explicit clear-processing-restriction admin endpoint
//	(which sets lenny.clear_processing_restriction = 'true').
func TestProcessingRestrictionTrigger(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	if _, err := pg.Pool.Exec(
		ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00')`,
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Seed a restricted user and an active erasure job for that user.
	if err := execTenant(ctx, pg, "acme",
		`INSERT INTO users (tenant_id, subject, processing_restricted)
		 VALUES ('acme', 'alice', true)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := execTenant(ctx, pg, "acme",
		`INSERT INTO erasure_jobs (id, tenant_id, user_id, status)
		 VALUES ('erasure_1', 'acme', 'alice', 'store_deleting')`); err != nil {
		t.Fatalf("seed erasure job: %v", err)
	}

	t.Run("rejects clearing the flag while a job is active", func(t *testing.T) {
		err := execTenant(ctx, pg, "acme",
			`UPDATE users SET processing_restricted = false
			 WHERE tenant_id = 'acme' AND subject = 'alice'`)
		if err == nil {
			t.Fatal("clearing processing_restricted with an active erasure job should be rejected")
		}
		if !strings.Contains(err.Error(), "ERASURE_JOB_ACTIVE") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("permits clearing via the session-variable bypass", func(t *testing.T) {
		// The clear-processing-restriction admin endpoint sets the
		// session-local variable for the duration of its transaction.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', 'acme', true)"); err != nil {
			t.Fatalf("set tenant: %v", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('lenny.clear_processing_restriction', 'true', true)"); err != nil {
			t.Fatalf("set bypass: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET processing_restricted = false
			 WHERE tenant_id = 'acme' AND subject = 'alice'`); err != nil {
			t.Errorf("clear with the bypass set must succeed: %v", err)
		}
	})

	t.Run("permits clearing once the job reaches a terminal phase", func(t *testing.T) {
		if err := execTenant(ctx, pg, "acme",
			`UPDATE erasure_jobs SET status = 'completed' WHERE id = 'erasure_1'`); err != nil {
			t.Fatalf("complete erasure job: %v", err)
		}
		if err := execTenant(ctx, pg, "acme",
			`UPDATE users SET processing_restricted = false
			 WHERE tenant_id = 'acme' AND subject = 'alice'`); err != nil {
			t.Errorf("clearing the flag after the job completes must succeed: %v", err)
		}
	})
}

// spec: 11.7
// diagnosis: a §11.7 integrity trigger is missing or disabled. The
//
//	gateway's startup grant-verification check queries this same
//	pg_trigger set and refuses to start in production if any is
//	disabled.
func TestIntegrityTriggersEnabled(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	for _, name := range []string{
		"lenny_tenant_guard", "lenny_billing_immutability", "lenny_audit_immutability",
	} {
		var total, disabled int
		err := pg.Pool.QueryRow(ctx, `
			SELECT COUNT(*), COUNT(*) FILTER (WHERE tgenabled = 'D')
			FROM pg_trigger WHERE tgname = $1`, name).Scan(&total, &disabled)
		if err != nil {
			t.Fatalf("query pg_trigger for %q: %v", name, err)
		}
		if total == 0 {
			t.Errorf("trigger %q is not installed", name)
		}
		if disabled != 0 {
			t.Errorf("trigger %q has %d disabled instances", name, disabled)
		}
	}
}
