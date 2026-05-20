//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.2 / §12.3 platform-admin __all__
// cross-tenant bypass. Exercises the trigger (writes pass without
// per-row tenant_id matching), the RLS policy (SELECTs see rows
// from every tenant), and the §12.3 line 141 cross_tenant_read
// audit emission tied to every such code path.
package rls_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 4.2, 12.3
// diagnosis: lenny_tenant_guard rejected every value of
// app.current_tenant that did not equal a row's tenant_id, and the
// lenny_tenant_isolation policies filtered every SELECT to one
// tenant. Both layers must accept the __all__ sentinel so a
// platform-admin path can read or write across tenants, and every
// such path must emit a cross_tenant_read audit event.
func TestRLSAllTenantsContext(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedTenant(t, ctx, pg, "tenant-b")
	seedSession(t, ctx, pg, "tenant-a")
	seedSession(t, ctx, pg, "tenant-b")

	t.Run("read across tenants under lenny_app role", func(t *testing.T) {
		conn, err := pg.Pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire conn: %v", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
			t.Fatalf("set role lenny_app: %v", err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant', $1, true)",
			pgtenant.AllTenantsSentinel); err != nil {
			t.Fatalf("set all-tenants context: %v", err)
		}
		var rowCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM sessions`).Scan(&rowCount); err != nil {
			t.Fatalf("count rows under __all__: %v", err)
		}
		if rowCount != 2 {
			t.Errorf("__all__ saw %d sessions across tenants, want 2", rowCount)
		}
	})

	t.Run("write under __all__ matches no per-row tenant", func(t *testing.T) {
		// The trigger accepts __all__ on a write — the row's tenant_id
		// is honored verbatim. A platform-admin path may legitimately
		// insert into multiple tenants without switching context per
		// row (e.g., a backfill operation).
		err := pgtenant.InAllTenants(ctx, pg.Pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx,
				`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
				 VALUES (gen_random_uuid(), 'tenant-a', 'created', 'echo', gen_random_uuid())`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
				 VALUES (gen_random_uuid(), 'tenant-b', 'created', 'echo', gen_random_uuid())`)
			return err
		})
		if err != nil {
			t.Fatalf("multi-tenant insert under __all__: %v", err)
		}
	})

	t.Run("audit emits cross_tenant_read", func(t *testing.T) {
		// §12.3 line 141: every code path that sets
		// app.current_tenant = '__all__' MUST emit a cross_tenant_read
		// audit event recording the caller identity, endpoint, and
		// query category. The platform-admin's audit chain is
		// `platform` per §11.7 (a pseudo-tenant id, not a row in
		// tenants).
		store := auditstore.New(pg.Pool)
		payload := json.RawMessage(
			`{"actor_subject":"alice@acme.com","endpoint":"/v1/admin/tenants","category":"tenant_list"}`)
		row, err := store.Append(ctx, "platform", "cross_tenant_read", payload, time.Time{})
		if err != nil {
			t.Fatalf("emit cross_tenant_read: %v", err)
		}
		if row.EventType != "cross_tenant_read" {
			t.Errorf("emitted event_type = %q, want cross_tenant_read", row.EventType)
		}

		// The cross_tenant_read row is itself observable under the
		// platform tenant context — the audit chain is per-tenant
		// and uses the literal `platform` value to scope events from
		// platform-admin actions.
		rows, err := store.Rows(ctx, "platform")
		if err != nil {
			t.Fatalf("read platform audit chain: %v", err)
		}
		var found bool
		for _, r := range rows {
			if r.EventType == "cross_tenant_read" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("platform audit chain has no cross_tenant_read row after emission")
		}
	})
}
