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

// prodTables is every table the production migrations create.
var prodTables = []string{
	"tenants", "runtime_definitions", "sessions", "session_messages",
	"audit_log", "billing_events", "issued_tokens", "agent_pod_state",
	"users", "connectors", "idempotency_keys",
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
		mustHaveTable(t, ctx, pg, tbl)
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
		mustNotHaveTable(t, ctx, pg, tbl)
	}
	for _, role := range []string{"lenny_app", "lenny_erasure"} {
		var exists bool
		if err := pg.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role,
		).Scan(&exists); err != nil {
			t.Fatalf("check role %q: %v", role, err)
		}
		if exists {
			t.Errorf("role %q should be dropped by the 0002 down migration", role)
		}
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
		if _, err := pg.Pool.Exec(ctx,
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

	if _, err := pg.Pool.Exec(ctx,
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
