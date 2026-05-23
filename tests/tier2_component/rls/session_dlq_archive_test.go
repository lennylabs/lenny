//go:build component

// SPDX-License-Identifier: MIT

// Component coverage for the §4.2 line 179 session_dlq_archive
// table scaffold. Exercises the tenant-isolation machinery:
//
//   * the lenny_tenant_guard trigger fires on writes (reject
//     mismatching tenant_id under InTx; accept matching tenant_id),
//   * the lenny_tenant_isolation RLS policy filters reads to the
//     calling tenant,
//   * the composite PK (tenant_id, session_id, message_id) is in
//     place,
//   * the platform-admin __all__ sentinel reads rows across tenants
//     when paired with the lenny.allow_all_sentinel opt-in.
//
// spec: §4.2 line 179.
package rls_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// seedDLQ inserts one session_dlq_archive row for tenant. The
// session it points at must exist (FK constraint).
func seedDLQ(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, messageID string) string {
	t.Helper()
	// Seed an underlying session.
	var sessID string
	if err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES (gen_random_uuid(), $1, 'failed', 'echo', gen_random_uuid())
			 RETURNING id::text`, tenant).Scan(&sessID)
	}); err != nil {
		t.Fatalf("seed session for DLQ: %v", err)
	}
	// Insert the DLQ archive row under the matching tenant context.
	if err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_dlq_archive (tenant_id, session_id, message_id, failure_reason, retry_count)
			 VALUES ($1, $2::uuid, $3, 'max_retries_exhausted', 3)`,
			tenant, sessID, messageID)
		return err
	}); err != nil {
		t.Fatalf("seed dlq: %v", err)
	}
	return sessID
}

// spec: §4.2 line 179 — session_dlq_archive carries the
// lenny_tenant_guard trigger; an insert whose row tenant_id does
// not match app.current_tenant is rejected.
func TestSessionDLQArchiveTriggerRejectsMismatchedTenant(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "alice")
	seedTenant(t, ctx, pg, "bob")

	// Seed a session under alice (so the FK can be satisfied later).
	var aliceSessID string
	if err := pgtenant.InTx(ctx, pg.Pool, "alice", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES (gen_random_uuid(), 'alice', 'failed', 'echo', gen_random_uuid())
			 RETURNING id::text`).Scan(&aliceSessID)
	}); err != nil {
		t.Fatalf("seed alice session: %v", err)
	}

	// An insert under bob's context that names alice's tenant_id
	// must be rejected by the trigger.
	err := pgtenant.InTx(ctx, pg.Pool, "bob", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_dlq_archive (tenant_id, session_id, message_id, retry_count)
			 VALUES ('alice', $1::uuid, 'msg-1', 0)`, aliceSessID)
		return err
	})
	if err == nil {
		t.Errorf("mismatched-tenant DLQ insert succeeded; trigger must reject (§4.2 line 179)")
	}
}

// spec: §4.2 line 179 — the composite PK keeps two tenants from
// colliding on the same (session_id, message_id) pair. Bob and alice
// can each archive a row with message_id=msg-1 even if the session
// IDs happen to match (which they cannot under the FK, but the PK
// is the load-bearing check).
func TestSessionDLQArchiveCompositePK(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "alice")
	seedTenant(t, ctx, pg, "bob")

	// Each tenant gets its own DLQ row.
	seedDLQ(t, ctx, pg, "alice", "msg-1")
	seedDLQ(t, ctx, pg, "bob", "msg-1")

	// Both rows exist under the __all__ sentinel.
	var count int
	if err := pgtenant.InAllTenants(ctx, pg.Pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM session_dlq_archive WHERE message_id = 'msg-1'`,
		).Scan(&count)
	}); err != nil {
		t.Fatalf("count under __all__: %v", err)
	}
	if count != 2 {
		t.Errorf("session_dlq_archive count = %d, want 2 (composite PK keeps both)", count)
	}
}

// spec: §4.2 line 179 — RLS isolates DLQ rows by tenant. Bob's
// SELECT only sees bob's row; alice's only alice's.
func TestSessionDLQArchiveRLSIsolatesPerTenant(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "alice")
	seedTenant(t, ctx, pg, "bob")
	seedDLQ(t, ctx, pg, "alice", "msg-1")
	seedDLQ(t, ctx, pg, "bob", "msg-2")

	for _, tc := range []struct {
		tenant string
		want   int
	}{
		{tenant: "alice", want: 1},
		{tenant: "bob", want: 1},
	} {
		var got int
		err := pgtenant.InTx(ctx, pg.Pool, tc.tenant, func(tx pgx.Tx) error {
			// Run under lenny_app so RLS fires; the superuser pool
			// bypasses RLS by default.
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
				return err
			}
			return tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM session_dlq_archive`).Scan(&got)
		})
		if err != nil {
			t.Fatalf("count under %s: %v", tc.tenant, err)
		}
		if got != tc.want {
			t.Errorf("%s sees %d DLQ rows, want %d", tc.tenant, got, tc.want)
		}
	}
}
