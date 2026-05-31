// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.3 RLS tenant-isolation defense
// against a real Postgres container with the production migrations
// applied. It is the CI-mandated TestRLSTenantGuardMissingSetLocal from
// §12.3 line 57: a transaction that reaches a tenant-scoped table
// without a prior SET LOCAL app.current_tenant must be rejected at the
// database level, and a transaction scoped to one tenant must not see
// another tenant's rows.
package tier4_integration_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §12.3 line 57 — TestRLSTenantGuardMissingSetLocal.
//
// The two layers under test:
//   - the lenny_tenant_guard BEFORE trigger fires for every role
//     (including the superuser) and rejects writes whose transaction has
//     not set app.current_tenant;
//   - the lenny_tenant_isolation RLS policy reads
//     current_setting('app.current_tenant', false), which raises for a
//     connection that never set the GUC, and otherwise filters rows to
//     the current tenant.
func TestRLSTenantGuardMissingSetLocal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})

	// Seed two tenants in the registry. The tenants table is the
	// platform-global registry (no tenant_id column, no guard trigger),
	// so the inserts need no SET LOCAL.
	for _, id := range []string{"tenant-a", "tenant-b"} {
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
			t.Fatalf("seed tenant %q: %v", id, err)
		}
	}

	t.Run("write without SET LOCAL is rejected by the trigger", func(t *testing.T) {
		// The trigger fires for the superuser too, so the pooled
		// (superuser) connection is sufficient. No SET LOCAL is issued,
		// so the guard raises before the row is written.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES (gen_random_uuid(), 'tenant-a', 'running', 'rt', gen_random_uuid())`)
		if err == nil {
			t.Fatal("expected lenny_tenant_guard to reject an INSERT with no SET LOCAL app.current_tenant")
		}
		if !strings.Contains(err.Error(), "app.current_tenant") {
			t.Fatalf("error %q does not mention the missing app.current_tenant guard", err.Error())
		}
	})

	t.Run("SELECT without SET LOCAL is rejected by RLS", func(t *testing.T) {
		// RLS filters reads for the non-superuser lenny_app role. A fresh
		// connection that never set app.current_tenant makes the policy's
		// current_setting('app.current_tenant', false) raise.
		conn, err := pgx.Connect(ctx, pg.DSN)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
			t.Fatalf("set role lenny_app: %v", err)
		}
		var n int
		err = conn.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&n)
		if err == nil {
			t.Fatalf("expected RLS to reject a SELECT with no SET LOCAL app.current_tenant (got count=%d)", n)
		}
		if !strings.Contains(err.Error(), "app.current_tenant") {
			t.Fatalf("error %q does not mention the missing app.current_tenant setting", err.Error())
		}
	})

	t.Run("tenant A cannot read tenant B rows", func(t *testing.T) {
		// Seed one tenant-B session through a properly scoped transaction
		// so the trigger admits the write.
		seed, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin seed: %v", err)
		}
		if _, err := seed.Exec(ctx, "SET LOCAL app.current_tenant = 'tenant-b'"); err != nil {
			t.Fatalf("set local tenant-b: %v", err)
		}
		if _, err := seed.Exec(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES (gen_random_uuid(), 'tenant-b', 'running', 'rt', gen_random_uuid())`); err != nil {
			t.Fatalf("seed tenant-b session: %v", err)
		}
		if err := seed.Commit(ctx); err != nil {
			t.Fatalf("commit seed: %v", err)
		}

		// Read as lenny_app scoped to tenant-a; the tenant-b row must be
		// invisible.
		conn, err := pgx.Connect(ctx, pg.DSN)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
			t.Fatalf("set role lenny_app: %v", err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin read: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = 'tenant-a'"); err != nil {
			t.Fatalf("set local tenant-a: %v", err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&n); err != nil {
			t.Fatalf("tenant-a scoped read: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant-a sees %d sessions, want 0 — RLS leaked tenant-b rows", n)
		}
	})
}
