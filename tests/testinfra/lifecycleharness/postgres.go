// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// PostgresStore starts Postgres with the production migrations, seeds the
// harness tenant, and returns the Options.NewStore constructor of a session
// store over it, whose rows read as stamped from the clock it is given
// (SteppedClockStore), with the started database, for a scenario that runs
// the harness on the compose Postgres.
func PostgresStore(t *testing.T) (func(now func() time.Time) sessionstore.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT (id) DO NOTHING`, Tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return func(now func() time.Time) sessionstore.Store {
		return NewSteppedClockStore(sessionpg.New(pg.Pool), now)
	}, pg
}
