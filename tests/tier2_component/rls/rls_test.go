//go:build component

// SPDX-License-Identifier: MIT

// Component tests for §12.2.2 row-level security and the §12.3 R-01
// schema linter, exercised against the production migrations under
// migrations/. These land the implementations promised by the
// scaffold entries in scaffolds_test.go.
package rls_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(schematest.RepoRoot(t), "migrations")
}

// seedTenant inserts a tenant row. tenants is platform-global, so no
// app.current_tenant context is required.
func seedTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id,
	); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// seedSession inserts one session for tenant under that tenant's
// app.current_tenant context, satisfying the lenny_tenant_guard trigger.
func seedSession(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed session begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("seed session set context: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		 VALUES (gen_random_uuid(), $1, 'created', 'echo', gen_random_uuid())`,
		tenant); err != nil {
		t.Fatalf("seed session insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed session commit: %v", err)
	}
}

// spec: 12.2.2
// diagnosis: lenny_tenant_guard did not reject a write issued with no
//
//	app.current_tenant set. The trigger fires for every role
//	(including superusers), so a bare connection without the GUC
//	must be rejected.
func TestRLSRequiresTenantContext(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		 VALUES (gen_random_uuid(), 'acme', 'created', 'echo', gen_random_uuid())`)
	if err == nil {
		t.Fatal("insert with no app.current_tenant must be rejected")
	}
	if !strings.Contains(err.Error(), "app.current_tenant is not set") {
		t.Errorf("unexpected rejection: %v", err)
	}
}

// spec: 12.2.2, 13
// diagnosis: row-level security leaked cross-tenant rows. Connected as
//
//	the non-superuser lenny_app role (RLS is bypassed by
//	superusers and table owners), a query in tenant-A context
//	must never observe tenant-B rows.
func TestRLSPreventsCrossTenantRead(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedTenant(t, ctx, pg, "tenant-b")
	seedSession(t, ctx, pg, "tenant-a")
	seedSession(t, ctx, pg, "tenant-b")

	conn, err := pg.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	// lenny_app is a non-superuser role, so ENABLE/FORCE ROW LEVEL
	// SECURITY actually filters its reads.
	if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}

	visibleCount := func(tenant string) (own, foreign int) {
		t.Helper()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
			t.Fatalf("set context %q: %v", tenant, err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM sessions`).Scan(&own); err != nil {
			t.Fatalf("count own rows for %q: %v", tenant, err)
		}
		other := "tenant-a"
		if tenant == "tenant-a" {
			other = "tenant-b"
		}
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM sessions WHERE tenant_id = $1`, other).Scan(&foreign); err != nil {
			t.Fatalf("count foreign rows for %q: %v", tenant, err)
		}
		return own, foreign
	}

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		own, foreign := visibleCount(tenant)
		if own != 1 {
			t.Errorf("tenant %q sees %d of its own sessions, want 1", tenant, own)
		}
		if foreign != 0 {
			t.Errorf("tenant %q sees %d cross-tenant sessions, want 0 (RLS leak)", tenant, foreign)
		}
	}
}

// spec: 12.3
// diagnosis: the R-01 schema linter (scripts/lint-schema.sh) did not
//
//	pass the production migrations clean, or did not flag a
//	deliberately non-conforming schema. The linter must do both:
//	accept tenant_id-leading / annotated tables and reject a
//	tenant-scoped table whose primary index does not lead with
//	tenant_id.
func TestSchemaLinterIdentifiesTenantScopedTables(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	linter := filepath.Join(root, "scripts", "lint-schema.sh")

	// The production migrations must lint clean.
	out, err := exec.Command("bash", linter, migrationsDir(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("lint-schema.sh on migrations/ should pass, got %v:\n%s", err, out)
	}

	// A tenant-scoped table whose PK does not lead with tenant_id and
	// which carries no tenant_id-leading index must be flagged.
	bad := t.TempDir()
	badSQL := `CREATE TABLE leaky_table (
    id        UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name      TEXT NOT NULL
);
`
	if err := os.WriteFile(filepath.Join(bad, "0001_bad.up.sql"), []byte(badSQL), 0o644); err != nil {
		t.Fatalf("write bad migration: %v", err)
	}
	out, err = exec.Command("bash", linter, bad).CombinedOutput()
	if err == nil {
		t.Fatalf("lint-schema.sh should flag a non-conforming schema:\n%s", out)
	}
	if !strings.Contains(string(out), "leaky_table") {
		t.Errorf("linter output should name the offending table:\n%s", out)
	}
}
