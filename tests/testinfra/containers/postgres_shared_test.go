//go:build component

// SPDX-License-Identifier: MIT

package containers_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 12.3 (Postgres 14+)
// diagnosis: a SharedPostgres database was not migrated, was visible to
// another test's database, survived its test, or a concurrent clone of the
// template failed. Each test that shares the container then sees another
// test's rows or a missing schema. Check SharedPostgres.Database and clone
// in tests/testinfra/containers/postgres_shared.go, and that
// applyMigrations returns the migration's connection.
func TestSharedPostgresGivesEachTestAMigratedDatabaseOfItsOwn(t *testing.T) {
	var shared containers.SharedPostgres
	t.Cleanup(func() {
		if err := shared.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	migrations := filepath.Join(schematest.RepoRoot(t), "migrations")
	ctx := context.Background()

	var first *containers.Postgres
	t.Run("a database is migrated and writable", func(t *testing.T) {
		first = shared.Database(t, migrations)
		if _, err := first.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00')`); err != nil {
			t.Fatalf("insert into the migrated tenants table: %v", err)
		}
	})
	t.Run("the next database does not see the first one's rows", func(t *testing.T) {
		second := shared.Database(t, migrations)
		var n int
		if err := second.Pool.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = 'acme'`).Scan(&n); err != nil {
			t.Fatalf("count tenants: %v", err)
		}
		if n != 0 {
			t.Errorf("second database holds %d rows the first database wrote, want 0", n)
		}
	})
	t.Run("a finished test's database is dropped", func(t *testing.T) {
		probe := shared.Database(t, migrations)
		var n int
		if err := probe.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname IN ('lenny_t1', 'lenny_t2')`).Scan(&n); err != nil {
			t.Fatalf("list databases: %v", err)
		}
		if n != 0 {
			t.Errorf("%d databases of finished tests remain, want 0", n)
		}
	})
	t.Run("concurrent clones each get a database", func(t *testing.T) {
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t.Run("clone", func(t *testing.T) {
					pg := shared.Database(t, migrations)
					if err := pg.Pool.Ping(ctx); err != nil {
						t.Errorf("ping: %v", err)
					}
				})
			}()
		}
		wg.Wait()
	})
}
