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

// spec: §12.3 line 146 — without a read replica, usage-report aggregation
// shares the primary pool.
func TestNewSharesPrimaryWhenNoReadPool_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	s := New(primary)
	if s.pool != primary || s.read != primary {
		t.Errorf("New must point both write and read at the primary when no read pool is wired")
	}
}

// spec: §12.3 line 146 — WithReadPool routes usage-report aggregation to
// the replica while the Record write stays on the primary. F-12.3.16.
func TestWithReadPoolRoutesReadsToReplica_spec_12_3_146(t *testing.T) {
	primary := fakePool(t)
	replica := fakePool(t)
	s := New(primary, WithReadPool(replica))
	if s.pool != primary {
		t.Errorf("the Record write must stay on the primary pool")
	}
	if s.read != replica {
		t.Errorf("usage-report reads must route to the replica pool")
	}
}
