// SPDX-License-Identifier: MIT

package pgstore

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakePool returns a configured *pgxpool.Pool that never dials (pgxpool
// connects lazily), so the read-routing wiring is testable without a
// database.
func fakePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://lenny:lenny@127.0.0.1:5432/lenny?sslmode=disable")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// spec: §12.3 line 146 — without a read replica, the read-heavy
// session-status and task-tree query paths share the primary pool.
func TestNewSharesPrimaryWhenNoReadPool_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	s := New(primary)
	if s.pool != primary || s.read != primary {
		t.Errorf("New must point both write and read at the primary when no read pool is wired")
	}
}

// spec: §12.3 line 146 — WithReadPool routes the read-heavy
// session-status and task-tree queries to the replica while writes stay
// on the primary. F-12.3.16.
func TestWithReadPoolRoutesReadsToReplica_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	replica := fakePool(t)
	s := New(primary, WithReadPool(replica))
	if s.pool != primary {
		t.Errorf("writes must stay on the primary pool")
	}
	if s.read != replica {
		t.Errorf("reads must route to the replica pool")
	}
}

// spec: §12.3 line 146 — a nil read pool is ignored so reads stay on the
// primary; the gateway passes nil when no LENNY_PG_READ_DSN is set.
func TestWithReadPoolNilKeepsReadsOnPrimary_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	s := New(primary, WithReadPool(nil))
	if s.read != primary {
		t.Errorf("a nil read pool must keep reads on the primary")
	}
}
