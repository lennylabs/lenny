//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// idempotencyScopeVersion is the migration that scopes idempotency_keys by
// principal.
const idempotencyScopeVersion = 205

// spec: §11.5 (Idempotency)
// diagnosis: migration 0205 did not discard the rows of idempotency_keys,
// did not replace the primary key with (tenant_id, subject, session_id,
// idempotency_key), or did not give session_id the empty-string default
// the store writes for a bearer bound to no session; or the down
// migration did not restore the (tenant_id, idempotency_key) key. The
// Postgres idempotency store names these columns in string literals, so a
// schema drift surfaces only as a failed claim at runtime.
func TestTheIdempotencyScopeMigrationEmptiesTheTableAndKeysItByPrincipal(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()
	prior := migrationVersionBelow(t, dir, idempotencyScopeVersion)

	// At the prior schema, seed a tenant and one record under the old key.
	pg.MigrateTo(t, dir, prior)
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := execTenant(ctx, pg, "acme",
		`INSERT INTO idempotency_keys (tenant_id, idempotency_key, body_hash, response_status) VALUES ('acme', 'k1', 'h', 201)`); err != nil {
		t.Fatalf("seed record at the prior schema: %v", err)
	}

	pg.MigrateTo(t, dir, idempotencyScopeVersion)
	var rows int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM idempotency_keys`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("idempotency_keys holds %d rows after migration 0205, want 0", rows)
	}
	if got, want := primaryKeyColumns(t, ctx, pg, "idempotency_keys"), []string{"tenant_id", "subject", "session_id", "idempotency_key"}; !slices.Equal(got, want) {
		t.Errorf("primary key = %v, want %v", got, want)
	}
	// A row written without a session id takes the empty-string default.
	if err := execTenant(ctx, pg, "acme",
		`INSERT INTO idempotency_keys (tenant_id, subject, idempotency_key, body_hash, response_status) VALUES ('acme', 'alice', 'k1', 'h', 201)`); err != nil {
		t.Fatalf("insert without a session id: %v", err)
	}
	var session string
	if err := pg.Pool.QueryRow(ctx, `SELECT session_id FROM idempotency_keys WHERE subject = 'alice'`).Scan(&session); err != nil {
		t.Fatalf("read session_id: %v", err)
	}
	if session != "" {
		t.Errorf("session_id default = %q, want the empty string", session)
	}
	// A record with no subject is refused by the NOT NULL column.
	if err := execTenant(ctx, pg, "acme",
		`INSERT INTO idempotency_keys (tenant_id, idempotency_key, body_hash, response_status) VALUES ('acme', 'k2', 'h', 201)`); err == nil {
		t.Error("a record with no subject was accepted, want a NOT NULL violation")
	}

	pg.MigrateTo(t, dir, prior)
	if got, want := primaryKeyColumns(t, ctx, pg, "idempotency_keys"), []string{"tenant_id", "idempotency_key"}; !slices.Equal(got, want) {
		t.Errorf("primary key after rollback = %v, want %v", got, want)
	}
	mustNotHaveColumn(t, ctx, pg, "idempotency_keys", "subject")
	mustNotHaveColumn(t, ctx, pg, "idempotency_keys", "session_id")
}

// migrationVersionBelow returns the highest migration version in dir that is
// lower than version, so a stepwise test rolls back to the schema just
// before the migration it checks whatever migrations land around it.
func migrationVersionBelow(t *testing.T, dir string, version uint) uint {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	re := regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)
	var best uint
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			continue
		}
		if uint(v) < version && uint(v) > best {
			best = uint(v)
		}
	}
	if best == 0 {
		t.Fatalf("no migration below %d in %s", version, dir)
	}
	return best
}

// primaryKeyColumns returns the columns of table's primary key in key order.
func primaryKeyColumns(t *testing.T, ctx context.Context, pg *containers.Postgres, table string) []string {
	t.Helper()
	rows, err := pg.Pool.Query(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.conrelid
		CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = r.oid AND a.attnum = k.attnum
		WHERE c.contype = 'p' AND r.relname = $1
		ORDER BY k.ord`, table)
	if err != nil {
		t.Fatalf("read primary key of %s: %v", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan primary key column: %v", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read primary key of %s: %v", table, err)
	}
	return cols
}
