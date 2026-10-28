// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// PostgresStore starts Postgres with the production migrations, seeds the
// harness tenant, and returns the Options.NewStore constructor of a session
// store over it, whose rows read as stamped from the clock it is given
// (SteppedClockStore), with the started database, for a scenario that runs
// the harness on the compose Postgres. The store counts a session in its
// §7.1 post-terminal window under PostTerminalCap. Its window cutoff reads
// the wall clock while the harness steps its own clock forward
// from the wall clock, so a terminal row with no release marker stays in its
// window for the whole of a scenario.
func PostgresStore(t *testing.T) (func(now func() time.Time) sessionstore.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	return seededStore(t, pg)
}

// SharedPostgresStore is PostgresStore over a database of the test's own on
// shared, a Postgres container the package reuses, rather than over a
// container of its own. The returned handle cannot stop Postgres, so a
// scenario that injects a Postgres outage calls PostgresStore instead.
func SharedPostgresStore(t *testing.T, shared *containers.SharedPostgres) (func(now func() time.Time) sessionstore.Store, *containers.Postgres) {
	t.Helper()
	return seededStore(t, shared.Database(t, filepath.Join(schematest.RepoRoot(t), "migrations")))
}

// seededStore seeds the harness tenant in pg and returns the session store
// constructor over it, with pg.
func seededStore(t *testing.T, pg *containers.Postgres) (func(now func() time.Time) sessionstore.Store, *containers.Postgres) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT (id) DO NOTHING`, Tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return PostgresStoreOn(pg.Pool), pg
}

// PostgresStoreOn returns the Options.NewStore constructor of a session store
// over pool, whose rows read as stamped from the clock it is given
// (SteppedClockStore). The store is built by CappedPostgresStore, so it
// counts a session in its §7.1 post-terminal window under PostTerminalCap,
// the cap the invariant checker applies.
//
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
func PostgresStoreOn(pool *pgxpool.Pool, opts ...sessionpg.Option) func(now func() time.Time) sessionstore.Store {
	return func(now func() time.Time) sessionstore.Store {
		return NewSteppedClockStore(CappedPostgresStore(pool, opts...), now)
	}
}

// CappedPostgresStore returns a Postgres session store over pool, configured
// by opts, whose slot queries count a session in its §7.1 post-terminal
// window under PostTerminalCap, as the gateway's production store counts it
// under the terminal pipeline budget. The cap is applied after opts, so no
// option leaves the store on a different cap.
//
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
func CappedPostgresStore(pool *pgxpool.Pool, opts ...sessionpg.Option) *sessionpg.Store {
	return sessionpg.New(pool, append(opts, sessionpg.WithPostTerminalCap(PostTerminalCap))...)
}

// postTerminalCapper is a session store that reports the §7.1 post-terminal
// cap its slot queries count.
type postTerminalCapper interface {
	PostTerminalCap() time.Duration
}

// CheckStoreCap returns nil when st, through the harness's own decorators,
// counts the §7.1 post-terminal window under PostTerminalCap, the cap the
// invariant checker applies, and an error otherwise. A store that does not
// report its cap is refused, because the checker cannot hold it to the
// invariant it evaluates.
//
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
func CheckStoreCap(st sessionstore.Store) error {
	for {
		switch s := st.(type) {
		case *SteppedClockStore:
			st = s.Store
		case *TracedStore:
			st = s.Store
		case postTerminalCapper:
			if got := s.PostTerminalCap(); got != PostTerminalCap {
				return fmt.Errorf("session store %T counts the post-terminal window under cap %v, want the harness cap %v", st, got, PostTerminalCap)
			}
			return nil
		default:
			return fmt.Errorf("session store %T does not report its post-terminal cap, want one counting the window under %v", st, PostTerminalCap)
		}
	}
}
