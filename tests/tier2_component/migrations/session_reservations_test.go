//go:build component

// SPDX-License-Identifier: MIT

// Production-schema component test for migration 0187
// (session_reservations), the table that records a session ID reserved
// for its owner before a creation path writes data keyed to it (§12.8
// Lifecycle fence and drain). The test pins the §12.3 tenant-isolation
// apparatus the session store's reservation writes and erasure deletes
// depend on, the primary key, the owner column the user-scoped delete
// reaches, and the down migration.
package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// sessionReservationsPriorVersion is the schema version immediately below
// the session_reservations migration.
const sessionReservationsPriorVersion = 186

// Reservation fixtures: alice is a user of tenant acme and bob a user of
// tenant globex.
const (
	resAliceSession = "11111111-1111-4111-8111-111111111111"
	resAliceSecond  = "22222222-2222-4222-8222-222222222222"
	resBobSession   = "33333333-3333-4333-8333-333333333333"
	resCarolSession = "44444444-4444-4444-8444-444444444444"
)

// spec: 12.8 (Compliance Interfaces), 12.3 (Postgres HA Requirements), 4.2
// diagnosis: migration 0187 did not create session_reservations with the
//
//	§12.3 isolation apparatus, its (tenant_id, session_id) primary key,
//	the user_id owner column and its index, or the lenny_app grant, or
//	its .down.sql left the table behind. Without the isolation policy
//	a tenant reads another tenant's reserved session IDs; without the
//	tenant guard a write can name a foreign tenant; without the hard
//	current_setting form a read with no tenant context returns rows
//	instead of failing; and without the owner column the erasure of a
//	user cannot find the IDs reserved for that user.
func TestSessionReservationsMigration_spec_12_8(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	mustHaveTable(t, ctx, pg, "session_reservations")
	mustHavePKLeadingColumn(t, ctx, pg, "session_reservations", "tenant_id")
	if def := indexDef(t, ctx, pg, "idx_session_reservations_user"); !strings.Contains(def, "(tenant_id, user_id)") {
		t.Errorf("idx_session_reservations_user = %q, want an index on (tenant_id, user_id)", def)
	}
	for _, priv := range []string{"SELECT", "INSERT", "DELETE"} {
		mustHaveTablePrivilege(t, ctx, pg, "lenny_app", "session_reservations", priv)
	}
	assertNoTablePrivilege(t, ctx, pg, "lenny_app", "session_reservations", "UPDATE")

	seedErasureTenants(t, ctx, pg)
	mustReserve(t, ctx, pg, "acme", resAliceSession, "alice")
	mustReserve(t, ctx, pg, "acme", resAliceSecond, "alice")
	mustReserve(t, ctx, pg, "globex", resBobSession, "bob")

	t.Run("isolation policy hides another tenant's reservation", func(t *testing.T) {
		assertReservationIsolation(t, ctx, pg)
	})
	t.Run("read with no tenant context fails", func(t *testing.T) {
		assertUnsetContextReadFails(t, ctx, pg)
	})
	t.Run("all-tenants sentinel reads every tenant only with the opt-in", func(t *testing.T) {
		assertAllTenantsSentinelRead(t, ctx, pg)
	})
	t.Run("primary key refuses a duplicate reservation", func(t *testing.T) {
		assertDuplicateReservationRefused(t, ctx, pg)
	})
	t.Run("tenant guard refuses a write naming another tenant", func(t *testing.T) {
		assertReservationTenantGuard(t, ctx, pg)
	})
	t.Run("user-scoped delete reaches only the user's reservations", func(t *testing.T) {
		assertUserScopedReservationDelete(t, ctx, pg)
	})

	pg.MigrateTo(t, dir, sessionReservationsPriorVersion)
	mustNotHaveTable(t, ctx, pg, "session_reservations")
}

// appTx runs fn in a transaction as the lenny_app role, the role the
// gateway connects as, after opting in to the all-tenants sentinel when
// allowAll is set and setting app.current_tenant to tenant when tenant is
// not empty. Every setting is transaction-local.
func appTx(ctx context.Context, pg *containers.Postgres, tenant string, allowAll bool, fn func(pgx.Tx) error) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		return err
	}
	if allowAll {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.allow_all_sentinel = 'true'"); err != nil {
			return err
		}
	}
	if tenant != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
			return err
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reserve inserts one reservation as lenny_app under the tenant's context.
func reserve(ctx context.Context, pg *containers.Postgres, tenant, session, user string) error {
	return appTx(ctx, pg, tenant, false, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_reservations (tenant_id, session_id, user_id) VALUES ($1, $2, $3)`,
			tenant, session, user)
		return err
	})
}

// mustReserve is reserve that fails the test on error.
func mustReserve(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, session, user string) {
	t.Helper()
	if err := reserve(ctx, pg, tenant, session, user); err != nil {
		t.Fatalf("reserve %s for %s/%s: %v", session, tenant, user, err)
	}
}

// countReservations counts the session_reservations rows visible as
// lenny_app under the given context, optionally narrowed to one tenant.
func countReservations(ctx context.Context, pg *containers.Postgres, tenant string, allowAll bool, where string, args ...any) (int, error) {
	var n int
	err := appTx(ctx, pg, tenant, allowAll, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM session_reservations `+where, args...).Scan(&n)
	})
	return n, err
}

// assertReservationIsolation checks that each tenant's context sees its
// own reservations and none of the other tenant's.
func assertReservationIsolation(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	cases := []struct {
		tenant, foreign string
		own             int
	}{
		{"acme", "globex", 2},
		{"globex", "acme", 1},
	}
	for _, tc := range cases {
		own, err := countReservations(ctx, pg, tc.tenant, false, "")
		if err != nil {
			t.Fatalf("count under %s: %v", tc.tenant, err)
		}
		if own != tc.own {
			t.Errorf("%s context sees %d reservations, want %d", tc.tenant, own, tc.own)
		}
		foreign, err := countReservations(ctx, pg, tc.tenant, false, "WHERE tenant_id = $1", tc.foreign)
		if err != nil {
			t.Fatalf("count %s rows under %s: %v", tc.foreign, tc.tenant, err)
		}
		if foreign != 0 {
			t.Errorf("%s context sees %d of %s's reservations, want 0", tc.tenant, foreign, tc.foreign)
		}
	}
}

// assertUnsetContextReadFails reads on a fresh connection, where
// app.current_tenant has never been defined, and expects the policy's
// current_setting(..., false) to raise rather than return rows.
func assertUnsetContextReadFails(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	conn, err := pgx.Connect(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	var n int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM session_reservations`).Scan(&n)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42704" {
		t.Errorf("read with app.current_tenant undefined: n = %d, err = %v, want undefined_object (42704)", n, err)
	}
}

// assertAllTenantsSentinelRead checks that '__all__' reads every
// tenant's reservations with the lenny.allow_all_sentinel opt-in and
// none without it (§4.2).
func assertAllTenantsSentinelRead(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	all, err := countReservations(ctx, pg, "__all__", true, "")
	if err != nil {
		t.Fatalf("count under __all__ with opt-in: %v", err)
	}
	if all != 3 {
		t.Errorf("__all__ with opt-in sees %d reservations, want 3", all)
	}
	none, err := countReservations(ctx, pg, "__all__", false, "")
	if err != nil {
		t.Fatalf("count under __all__ without opt-in: %v", err)
	}
	if none != 0 {
		t.Errorf("__all__ without opt-in sees %d reservations, want 0", none)
	}
}

// assertDuplicateReservationRefused checks the (tenant_id, session_id)
// primary key: a second reservation of the same ID in the same tenant
// fails, and the same ID in another tenant is a different key.
func assertDuplicateReservationRefused(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	if err := reserve(ctx, pg, "acme", resAliceSession, "dave"); !isUniqueViolation(err) {
		t.Errorf("duplicate (acme, %s): err = %v, want unique_violation (23505)", resAliceSession, err)
	}
	if err := reserve(ctx, pg, "globex", resAliceSession, "bob"); err != nil {
		t.Errorf("same session ID under another tenant: %v, want it admitted", err)
	}
	if err := appTx(ctx, pg, "globex", false, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM session_reservations WHERE session_id = $1`, resAliceSession)
		return err
	}); err != nil {
		t.Fatalf("remove the globex duplicate: %v", err)
	}
}

// assertReservationTenantGuard checks lenny_tenant_guard: a write with no
// tenant context fails, and an insert or a delete under bob's tenant
// context (globex) naming alice's tenant (acme) fails. The writes run as
// the migration owner, which row-level security does not filter, so the
// refusal comes from the trigger rather than from the policy hiding the
// row.
func assertReservationTenantGuard(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO session_reservations (tenant_id, session_id, user_id) VALUES ('acme', $1, 'alice')`,
		resCarolSession)
	if err == nil || !strings.Contains(err.Error(), "app.current_tenant is not set") {
		t.Errorf("insert with no tenant context: err = %v, want the tenant guard refusal", err)
	}
	writes := []struct{ name, sql string }{
		{"insert", `INSERT INTO session_reservations (tenant_id, session_id, user_id) VALUES ('acme', '` + resCarolSession + `', 'alice')`},
		{"delete", `DELETE FROM session_reservations WHERE tenant_id = 'acme' AND session_id = '` + resAliceSession + `'`},
	}
	for _, w := range writes {
		err := execTenant(ctx, pg, "globex", w.sql)
		if err == nil || !strings.Contains(err.Error(), "does not match app.current_tenant") {
			t.Errorf("%s naming acme under globex context: err = %v, want the tenant guard refusal", w.name, err)
		}
	}
	n, err := countReservations(ctx, pg, "acme", false, "WHERE session_id = $1", resAliceSession)
	if err != nil {
		t.Fatalf("count alice's reservation: %v", err)
	}
	if n != 1 {
		t.Errorf("alice's reservation after the refused cross-tenant delete: %d rows, want 1", n)
	}
}

// assertUserScopedReservationDelete checks that the delete the session
// store's user-scoped erasure issues, keyed by tenant and owner, removes
// every reservation of the user and leaves another user's in place.
func assertUserScopedReservationDelete(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	mustReserve(t, ctx, pg, "acme", resCarolSession, "carol")
	var deleted int64
	if err := appTx(ctx, pg, "acme", false, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM session_reservations WHERE tenant_id = $1 AND user_id = $2`, "acme", "alice")
		deleted = tag.RowsAffected()
		return err
	}); err != nil {
		t.Fatalf("delete alice's reservations: %v", err)
	}
	if deleted != 2 {
		t.Errorf("user-scoped delete removed %d reservations, want 2", deleted)
	}
	remaining, err := countReservations(ctx, pg, "acme", false, "")
	if err != nil {
		t.Fatalf("count acme after delete: %v", err)
	}
	if remaining != 1 {
		t.Errorf("acme keeps %d reservations after alice's delete, want carol's 1", remaining)
	}
}

// assertNoTablePrivilege fails the test when role holds privilege on table.
func assertNoTablePrivilege(t *testing.T, ctx context.Context, pg *containers.Postgres, role, table, privilege string) {
	t.Helper()
	var ok bool
	if err := pg.Pool.QueryRow(ctx,
		`SELECT has_table_privilege($1, $2, $3)`, role, table, privilege).Scan(&ok); err != nil {
		t.Fatalf("check %s privilege for %q on %q: %v", privilege, role, table, err)
	}
	if ok {
		t.Errorf("role %q holds %s on %q, want it withheld", role, privilege, table)
	}
}
