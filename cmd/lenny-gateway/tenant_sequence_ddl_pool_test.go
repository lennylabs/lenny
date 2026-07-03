// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// TestAliasPrimaryDDLToBillingAudit pins the single-instance vs
// separate-instance topology decision for the primary DDL pool. The
// per-tenant audit_seq_ sequence the §13.3 issued-token write-before-issue
// path seals on the primary must be created through a CREATE-privileged
// connection to the primary. In the single-instance topology (no separate
// billing/audit instance, no distinct primary DDL DSN) the primary and the
// billing/audit instance are one, so the single billing/audit DDL pool
// addresses both. In every other combination the primary DDL pool is opened
// from LENNY_PG_PRIMARY_DDL_DSN, so aliasing must not happen.
//
// A regression that aliased in the separate-instance topology would point the
// primary CREATE SEQUENCE at the wrong instance and the §13.3 nextval would
// fail on a nonexistent relation. This test asserts the alias only where the
// two instances are genuinely one.
//
// spec: §12.3, §15.1. F-11.2.10
func TestAliasPrimaryDDLToBillingAudit(t *testing.T) {
	cases := []struct {
		name            string
		billingAuditDSN string
		primaryDDLDSN   string
		wantAlias       bool
	}{
		{
			name:      "single instance: no separate billing/audit, no primary ddl dsn -> alias",
			wantAlias: true,
		},
		{
			name:            "separate billing/audit instance, no primary ddl dsn -> no alias",
			billingAuditDSN: "postgres://ba@host/lenny",
			wantAlias:       false,
		},
		{
			name:          "single instance but explicit primary ddl dsn -> no alias (own pool)",
			primaryDDLDSN: "postgres://pddl@host/lenny",
			wantAlias:     false,
		},
		{
			name:            "separate instance with explicit primary ddl dsn -> no alias",
			billingAuditDSN: "postgres://ba@host/lenny",
			primaryDDLDSN:   "postgres://pddl@host/lenny",
			wantAlias:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aliasPrimaryDDLToBillingAudit(tc.billingAuditDSN, tc.primaryDDLDSN); got != tc.wantAlias {
				t.Errorf("aliasPrimaryDDLToBillingAudit(%q, %q) = %v, want %v",
					tc.billingAuditDSN, tc.primaryDDLDSN, got, tc.wantAlias)
			}
		})
	}
}

// lazyDDLPool builds a *pgxpool.Pool that never dials (MinConns=0), used only
// for pointer identity in the shutdown-dedup and alias assertions. Closed
// immediately so the pointer-only tests stay hermetic (tier 1, no Postgres).
func lazyDDLPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://ddl@127.0.0.1:1/lenny")
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestDistinctDDLPools pins the shutdown-dedup decision runServers relies on:
// distinctDDLPools returns each CREATE-privileged DDL pool exactly once so the
// shutdown path never double-closes the pool shared between the billing/audit
// and primary limbs in the single-instance topology (where primaryDDLPool
// aliases billingAuditDDLPool), and skips a nil pool. A regression that closed
// the aliased pool twice would panic or race on the second Close; one that
// dropped a distinct primary pool would leak a connection pool at shutdown.
//
// spec: §12.3, §15.1. F-11.2.10
func TestDistinctDDLPools(t *testing.T) {
	ba := lazyDDLPool(t)
	primary := lazyDDLPool(t)

	t.Run("both nil -> empty", func(t *testing.T) {
		if got := distinctDDLPools(nil, nil); len(got) != 0 {
			t.Errorf("distinctDDLPools(nil, nil) = %v, want empty", got)
		}
	})
	t.Run("single-instance alias -> one pool", func(t *testing.T) {
		// primaryDDLPool aliases billingAuditDDLPool: close it once.
		got := distinctDDLPools(ba, ba)
		if len(got) != 1 || got[0] != ba {
			t.Errorf("aliased pools = %v, want [billingAudit] once", got)
		}
	})
	t.Run("separate instances -> two pools", func(t *testing.T) {
		got := distinctDDLPools(ba, primary)
		if len(got) != 2 || got[0] != ba || got[1] != primary {
			t.Errorf("distinct pools = %v, want [billingAudit, primary]", got)
		}
	})
	t.Run("only billing/audit -> one pool", func(t *testing.T) {
		got := distinctDDLPools(ba, nil)
		if len(got) != 1 || got[0] != ba {
			t.Errorf("billingAudit-only = %v, want [billingAudit]", got)
		}
	})
	t.Run("only primary -> one pool", func(t *testing.T) {
		got := distinctDDLPools(nil, primary)
		if len(got) != 1 || got[0] != primary {
			t.Errorf("primary-only = %v, want [primary]", got)
		}
	})
}

// recordingStoreRouter is a partial storerouter.StoreRouter that records the
// tenantID passed to BillingShard / AuditShard and returns a fixed pool. It
// embeds the interface so it satisfies the type; the unimplemented methods are
// never called by billingAuditShardResolvers and would panic if they were,
// which is the intended tier-1 fence.
type recordingStoreRouter struct {
	storerouter.StoreRouter
	pool          *pgxpool.Pool
	billingTenant storerouter.TenantID
	auditTenant   storerouter.TenantID
}

func (r *recordingStoreRouter) BillingShard(_ context.Context, tenantID storerouter.TenantID) (*pgxpool.Pool, error) {
	r.billingTenant = tenantID
	return r.pool, nil
}

func (r *recordingStoreRouter) AuditShard(_ context.Context, tenantID storerouter.TenantID) (*pgxpool.Pool, error) {
	r.auditTenant = tenantID
	return r.pool, nil
}

// TestBillingAuditShardResolvers_NilRouter pins the in-memory / SQLite topology
// branch: a nil StoreRouter yields nil resolvers, which the admin provisioning
// helper treats as "no re-seed read". A regression that returned non-nil
// resolvers closing over a nil StoreRouter would panic on the first re-seed.
//
// spec: §12.3, §15.1. F-11.2.10
func TestBillingAuditShardResolvers_NilRouter(t *testing.T) {
	billing, audit := billingAuditShardResolvers(nil)
	if billing != nil || audit != nil {
		t.Errorf("nil StoreRouter must yield nil resolvers, got billing=%v audit=%v", billing != nil, audit != nil)
	}
}

// TestBillingAuditShardResolvers_AdaptsTenantID pins the Postgres-topology
// branch: a non-nil StoreRouter yields resolvers that convert the admin
// ShardResolver's plain string tenantID to a storerouter.TenantID and call
// through to BillingShard / AuditShard, returning that instance's pool so the
// re-seed reads the ledger MAX on the correct instance. A regression that
// dropped the adaptation or crossed billing/audit would route the re-seed to
// the wrong instance.
//
// spec: §12.3, §15.1. F-11.2.10
func TestBillingAuditShardResolvers_AdaptsTenantID(t *testing.T) {
	pool := lazyDDLPool(t)
	sr := &recordingStoreRouter{pool: pool}

	billing, audit := billingAuditShardResolvers(sr)
	if billing == nil || audit == nil {
		t.Fatal("a non-nil StoreRouter must yield non-nil resolvers")
	}

	gotBilling, err := billing(context.Background(), "acme")
	if err != nil {
		t.Fatalf("billing resolver: %v", err)
	}
	if gotBilling != pool {
		t.Error("billing resolver must return the StoreRouter's BillingShard pool")
	}
	if sr.billingTenant != storerouter.TenantID("acme") {
		t.Errorf("billing resolver passed tenantID %q, want the converted storerouter.TenantID(\"acme\")", sr.billingTenant)
	}

	gotAudit, err := audit(context.Background(), "globex")
	if err != nil {
		t.Fatalf("audit resolver: %v", err)
	}
	if gotAudit != pool {
		t.Error("audit resolver must return the StoreRouter's AuditShard pool")
	}
	if sr.auditTenant != storerouter.TenantID("globex") {
		t.Errorf("audit resolver passed tenantID %q, want the converted storerouter.TenantID(\"globex\")", sr.auditTenant)
	}
}
