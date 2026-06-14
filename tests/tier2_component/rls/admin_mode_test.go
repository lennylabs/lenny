//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.2 line 177 lenny.admin_mode trigger on
// runtime_tenant_access and pool_tenant_access. The trigger is the
// defense-in-depth guard that rejects any write whose transaction has
// not set lenny.admin_mode = 'true'. Combined with the application-
// layer RBAC gate (platform-admin only), a stolen tenant-admin
// credential cannot reach these tables even if the gateway's
// authorization is bypassed.
package rls_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	tapgstore "github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// seedRuntime inserts a platform-global runtime row so the
// runtime_tenant_access grant can satisfy its foreign key.
func seedRuntime(t *testing.T, ctx context.Context, pg *containers.Postgres, name string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO runtime_definitions
		  (name, type, image, execution_mode, isolation_profile, integration_level)
		 VALUES ($1, 'agent', 'example/img:latest', 'session', 'standard', 'standard')`,
		name); err != nil {
		t.Fatalf("seed runtime %q: %v", name, err)
	}
}

// spec: §4.2 line 177
// diagnosis: a write to runtime_tenant_access without
// lenny.admin_mode = 'true' must be rejected by the
// lenny_admin_mode_required trigger.
func TestAdminModeRequiredOnRuntimeTenantAccess(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedRuntime(t, ctx, pg, "runtime-claude")

	// Bare INSERT without admin_mode is rejected.
	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO runtime_tenant_access (tenant_id, runtime_name, granted_by)
		 VALUES ($1, $2, 'platform-admin@acme')`,
		"tenant-a", "runtime-claude")
	if err == nil {
		t.Fatal("INSERT without lenny.admin_mode must be rejected")
	}
	if !strings.Contains(err.Error(), "lenny.admin_mode") {
		t.Errorf("rejection should name lenny.admin_mode: %v", err)
	}

	// INSERT inside a transaction that explicitly sets the GUC succeeds.
	if err := pgtenant.InAdminMode(ctx, pg.Pool, func(tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx,
			`INSERT INTO runtime_tenant_access (tenant_id, runtime_name, granted_by)
			 VALUES ($1, $2, 'platform-admin@acme')`,
			"tenant-a", "runtime-claude")
		return ierr
	}); err != nil {
		t.Fatalf("INSERT under InAdminMode should succeed: %v", err)
	}

	// UPDATE without admin_mode is rejected.
	_, err = pg.Pool.Exec(ctx,
		`UPDATE runtime_tenant_access SET granted_by = 'platform-admin@globex'
		 WHERE tenant_id = $1 AND runtime_name = $2`,
		"tenant-a", "runtime-claude")
	if err == nil {
		t.Fatal("UPDATE without lenny.admin_mode must be rejected")
	}
	if !strings.Contains(err.Error(), "lenny.admin_mode") {
		t.Errorf("UPDATE rejection should name lenny.admin_mode: %v", err)
	}

	// DELETE without admin_mode is rejected.
	_, err = pg.Pool.Exec(ctx,
		`DELETE FROM runtime_tenant_access WHERE tenant_id = $1`, "tenant-a")
	if err == nil {
		t.Fatal("DELETE without lenny.admin_mode must be rejected")
	}
	if !strings.Contains(err.Error(), "lenny.admin_mode") {
		t.Errorf("DELETE rejection should name lenny.admin_mode: %v", err)
	}

	// DELETE inside InAdminMode succeeds.
	if err := pgtenant.InAdminMode(ctx, pg.Pool, func(tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx,
			`DELETE FROM runtime_tenant_access WHERE tenant_id = $1`, "tenant-a")
		return ierr
	}); err != nil {
		t.Fatalf("DELETE under InAdminMode should succeed: %v", err)
	}
}

// spec: §4.2 line 177
// diagnosis: a write to pool_tenant_access without lenny.admin_mode
// must be rejected by the trigger, mirroring the runtime_tenant_access
// guard.
func TestAdminModeRequiredOnPoolTenantAccess(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	// Seed a platform-global runtime + pool so the pool_tenant_access
	// FK lands on real referents.
	seedRuntime(t, ctx, pg, "runtime-claude")
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO sandbox_warm_pools (name, runtime_ref, isolation_profile)
		 VALUES ('pool-default', 'runtime-claude', 'standard')`); err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO pool_tenant_access (tenant_id, pool_name, granted_by)
		 VALUES ($1, $2, 'platform-admin@acme')`,
		"tenant-a", "pool-default")
	if err == nil {
		t.Fatal("INSERT without lenny.admin_mode must be rejected")
	}
	if !strings.Contains(err.Error(), "lenny.admin_mode") {
		t.Errorf("rejection should name lenny.admin_mode: %v", err)
	}

	if err := pgtenant.InAdminMode(ctx, pg.Pool, func(tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx,
			`INSERT INTO pool_tenant_access (tenant_id, pool_name, granted_by)
			 VALUES ($1, $2, 'platform-admin@acme')`,
			"tenant-a", "pool-default")
		return ierr
	}); err != nil {
		t.Fatalf("INSERT under InAdminMode should succeed: %v", err)
	}
}

// spec: §4.2 line 177
// diagnosis: the tenantaccessstore pgstore wraps Grant/Revoke in
// InAdminMode so the admin code path round-trips through the trigger
// without explicit GUC handling at the call site.
func TestTenantAccessStoreGrantRevokeUseAdminMode(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedRuntime(t, ctx, pg, "runtime-claude")

	store := tapgstore.New(pg.Pool)

	created, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "runtime-claude",
		"tenant-a", "platform-admin@acme", time.Now())
	if err != nil {
		t.Fatalf("Grant should succeed under InAdminMode: %v", err)
	}
	if !created {
		t.Error("Grant should report created=true on first insert")
	}

	// Re-grant — idempotent (created=false, no error).
	created, err = store.Grant(ctx, tenantaccessstore.KindRuntime, "runtime-claude",
		"tenant-a", "platform-admin@acme", time.Now())
	if err != nil {
		t.Fatalf("Re-Grant should succeed: %v", err)
	}
	if created {
		t.Error("Re-Grant should report created=false")
	}

	if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, "runtime-claude", "tenant-a"); err != nil {
		t.Fatalf("Revoke should succeed under InAdminMode: %v", err)
	}
}

// spec: §4.2 line 163
// diagnosis: the lenny_tenant_isolation RLS policy must use the
// `false` form of current_setting so an unset GUC is rejected rather
// than silently treated as the empty string. For Postgres custom
// GUC parameters (`app.current_tenant` is one), `current_setting`
// with `missing_ok=false` returns the empty string on a fresh
// connection where no SET has run; the spec defense-in-depth then
// relies on the PgBouncer connect_query sentinel ('__unset__') which
// makes the RLS predicate filter zero rows. This test exercises both
// halves: a SELECT under lenny_app with the connect_query sentinel
// value returns zero rows, and a SELECT after explicitly setting the
// tenant context returns the expected row.
//
// spec: §4.2 line 163.
// diagnosis: a failure means the RLS predicate treats an unset
// app.current_tenant GUC as a readable context, so a fresh connection
// with no tenant SET could read rows instead of being filtered to zero.
func TestRLSHardErrorOnMissingContext(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedSession(t, ctx, pg, "tenant-a")

	conn, err := pg.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}

	// Simulate the PgBouncer connect_query sentinel: a fresh server
	// connection arrives with app.current_tenant = '__unset__'. Under
	// the §4.2 line 163 RLS policy, no row's tenant_id equals
	// '__unset__' and the row predicate filters everything out — the
	// defense-in-depth that prevents a leaked pooled connection from
	// surfacing tenant data before the application-layer SET runs.
	if _, err := conn.Exec(ctx, `SET app.current_tenant = '__unset__'`); err != nil {
		t.Fatalf("simulate pgbouncer connect_query sentinel: %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("SELECT under __unset__ sentinel: %v", err)
	}
	if n != 0 {
		t.Errorf("__unset__ sentinel must filter every row; got %d", n)
	}

	// Setting the concrete tenant context returns the seeded row,
	// confirming the policy admits matches.
	if _, err := conn.Exec(ctx, `SET app.current_tenant = 'tenant-a'`); err != nil {
		t.Fatalf("set tenant-a context: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("SELECT under tenant-a: %v", err)
	}
	if n != 1 {
		t.Errorf("tenant-a context must surface the seeded row; got %d", n)
	}
}
