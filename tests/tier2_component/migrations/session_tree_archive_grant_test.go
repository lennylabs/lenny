//go:build component

// SPDX-License-Identifier: MIT

// Production-schema component test for migration 0100
// (session_tree_archive). It pins the §4.2 / §12.8 application-role grant
// the gateway's tree-archive store depends on: the gateway connects as
// lenny_app and archives, replays, re-archives (an upsert that needs both
// INSERT and UPDATE), and during §12.8 erasure deletes archive rows inside
// a SET LOCAL app.current_tenant transaction. Without the table-level
// GRANT, lenny_app is denied before RLS is ever consulted.
package migrations_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: §4.2 line 163, §12.8 step 11
// diagnosis: migration 0100 did not grant the lenny_app application role
// the SELECT/INSERT/UPDATE/DELETE privileges on session_tree_archive, or
// its .down.sql left the table behind. The gateway treearchive pgstore
// connects as lenny_app and fails closed (permission denied at the table
// level, before RLS) when any of the four privileges is missing; the
// upsert archive write needs INSERT and UPDATE, the replay reads need
// SELECT, and the §12.8 erasure path needs DELETE.
func TestProdMigration0100SessionTreeArchiveAppGrant(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	mustHaveTable(t, ctx, pg, "session_tree_archive")
	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		mustHaveTablePrivilege(t, ctx, pg, "lenny_app", "session_tree_archive", priv)
	}

	// Rolling migration 0100 back drops the table (and with it the grant).
	pg.MigrateTo(t, dir, 99)
	mustNotHaveTable(t, ctx, pg, "session_tree_archive")
}

// mustHaveTablePrivilege fails the test when role does not hold privilege
// on table. It queries Postgres' has_table_privilege so the assertion
// reflects the effective grant after every migration has applied, rather
// than a textual scan of the .up.sql.
func mustHaveTablePrivilege(t *testing.T, ctx context.Context, pg *containers.Postgres, role, table, privilege string) {
	t.Helper()
	var ok bool
	if err := pg.Pool.QueryRow(
		ctx,
		`SELECT has_table_privilege($1, $2, $3)`, role, table, privilege,
	).Scan(&ok); err != nil {
		t.Fatalf("check %s privilege for %q on %q: %v", privilege, role, table, err)
	}
	if !ok {
		t.Errorf("role %q is missing %s on %q", role, privilege, table)
	}
}
