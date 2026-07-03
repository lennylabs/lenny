// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
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

// TestOpenVerifiedDDLPool_EmptyDSN pins the topology branch that uses no DDL
// pool: an empty DSN yields a nil pool and no error, so the caller does not open
// a connection and does not log.Fatalf. A regression that dialed on an empty DSN
// would fail startup in the in-memory / SQLite topology, which has no Postgres.
//
// spec: §12.3, §15.1. F-11.2.10
func TestOpenVerifiedDDLPool_EmptyDSN(t *testing.T) {
	var openerCalled, verifierCalled bool
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		openerCalled = true
		return nil, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error {
		verifierCalled = true
		return nil
	}
	pool, err := openVerifiedDDLPool(context.Background(), "", open, verify)
	if err != nil {
		t.Fatalf("empty DSN must not error: %v", err)
	}
	if pool != nil {
		t.Errorf("empty DSN must yield a nil pool, got %v", pool)
	}
	if openerCalled || verifierCalled {
		t.Errorf("empty DSN must not open or verify (opener=%v verifier=%v)", openerCalled, verifierCalled)
	}
}

// TestOpenVerifiedDDLPool_OpenError pins the fail-closed open path: when the
// opener fails, openVerifiedDDLPool returns the error without verifying, so the
// caller escalates to log.Fatalf and the gateway does not start with an
// unopenable CREATE-privileged pool. A regression that swallowed the open error
// would let the gateway start unable to provision per-tenant sequences.
//
// spec: §12.3, §15.1. F-11.2.10
func TestOpenVerifiedDDLPool_OpenError(t *testing.T) {
	wantErr := errors.New("dial refused")
	var verifierCalled bool
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		return nil, wantErr
	}
	verify := func(context.Context, *pgxpool.Pool) error {
		verifierCalled = true
		return nil
	}
	pool, err := openVerifiedDDLPool(context.Background(), "postgres://ddl@host/lenny", open, verify)
	if !errors.Is(err, wantErr) {
		t.Fatalf("open error must propagate, got %v", err)
	}
	if pool != nil {
		t.Errorf("failed open must yield a nil pool, got %v", pool)
	}
	if verifierCalled {
		t.Error("a failed open must not proceed to schema verification")
	}
}

// TestOpenVerifiedDDLPool_VerifyErrorClosesPool pins the fail-closed verify
// path: when the schema verification fails, openVerifiedDDLPool closes the pool
// it opened before returning the error, so a failed startup leaks no connection
// pool, and the caller escalates to log.Fatalf. A regression that returned the
// verify error without closing the pool would leak the connection pool on every
// failed start.
//
// spec: §12.3, §15.1. F-11.2.10
func TestOpenVerifiedDDLPool_VerifyErrorClosesPool(t *testing.T) {
	wantErr := errors.New("schema drift")
	opened := lazyDDLPool(t)
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		return opened, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error {
		return wantErr
	}
	pool, err := openVerifiedDDLPool(context.Background(), "postgres://ddl@host/lenny", open, verify)
	if !errors.Is(err, wantErr) {
		t.Fatalf("verify error must propagate, got %v", err)
	}
	if pool != nil {
		t.Errorf("failed verify must yield a nil pool, got %v", pool)
	}
	// A pgxpool.Pool that has been closed reports zero total connections and
	// rejects Acquire. Acquire after Close returns an error, which is how the
	// test observes that openVerifiedDDLPool closed the pool it opened.
	if _, acqErr := opened.Acquire(context.Background()); acqErr == nil {
		t.Error("a failed verify must Close the opened pool; Acquire after Close should error")
	}
}

// TestOpenVerifiedDDLPool_Success pins the happy path: a good open and a good
// verify return the opened pool for the caller to retain.
//
// spec: §12.3, §15.1. F-11.2.10
func TestOpenVerifiedDDLPool_Success(t *testing.T) {
	opened := lazyDDLPool(t)
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		return opened, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error {
		return nil
	}
	pool, err := openVerifiedDDLPool(context.Background(), "postgres://ddl@host/lenny", open, verify)
	if err != nil {
		t.Fatalf("success path must not error: %v", err)
	}
	if pool != opened {
		t.Errorf("success path must return the opened pool, got %v want %v", pool, opened)
	}
}

// TestResolveDDLPools_SingleInstanceAlias pins the single-instance topology:
// no separate billing/audit instance and no explicit primary DDL DSN, so the
// primary DDL pool aliases the billing/audit DDL pool (one pool addresses both).
// A regression that opened a second pool or returned nil for the primary would
// leave the §13.3 primary audit sequence unprovisionable in the single-instance
// topology.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_SingleInstanceAlias(t *testing.T) {
	opened := lazyDDLPool(t)
	var opens int
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		opens++
		return opened, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{
		billingAuditDDL: "postgres://baddl@host/lenny",
	}, open, verify)
	if err != nil {
		t.Fatalf("single-instance resolve: %v", err)
	}
	if ba != opened {
		t.Error("billing/audit DDL pool must be the opened pool")
	}
	if primary != ba {
		t.Error("single-instance topology must alias the primary DDL pool to the billing/audit DDL pool")
	}
	if opens != 1 {
		t.Errorf("single-instance topology must open exactly one DDL pool, opened %d", opens)
	}
}

// TestResolveDDLPools_SeparateInstances pins the separate-instance topology: a
// billing/audit instance DSN plus an explicit primary DDL DSN yield two distinct
// pools. A regression that aliased here would point the primary CREATE SEQUENCE
// at the billing/audit instance, and the §13.3 nextval on the primary would fail
// on a nonexistent relation.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_SeparateInstances(t *testing.T) {
	baPool := lazyDDLPool(t)
	primaryPool := lazyDDLPool(t)
	open := func(_ context.Context, dsn string) (*pgxpool.Pool, error) {
		if dsn == "postgres://primaryddl@host/lenny" {
			return primaryPool, nil
		}
		return baPool, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{
		billingAudit:    "postgres://ba@host/lenny",
		billingAuditDDL: "postgres://baddl@host/lenny",
		primaryDDL:      "postgres://primaryddl@host/lenny",
	}, open, verify)
	if err != nil {
		t.Fatalf("separate-instance resolve: %v", err)
	}
	if ba != baPool {
		t.Error("billing/audit DDL pool must be the billing/audit-DSN pool")
	}
	if primary != primaryPool {
		t.Error("primary DDL pool must be the primary-DSN pool, distinct from billing/audit")
	}
	if primary == ba {
		t.Error("separate-instance topology must not alias the primary DDL pool")
	}
}

// TestResolveDDLPools_SeparateInstanceNoPrimaryDSN pins the misconfiguration
// guard: a separate billing/audit instance is configured but no primary DDL DSN
// is supplied, so the primary DDL pool is nil (no alias to the billing/audit
// instance, which would seal the §13.3 audit row on the wrong instance). A
// regression that aliased here would silently route the primary audit sequence
// to the billing/audit instance.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_SeparateInstanceNoPrimaryDSN(t *testing.T) {
	baPool := lazyDDLPool(t)
	open := func(context.Context, string) (*pgxpool.Pool, error) { return baPool, nil }
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{
		billingAudit:    "postgres://ba@host/lenny",
		billingAuditDDL: "postgres://baddl@host/lenny",
	}, open, verify)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ba != baPool {
		t.Error("billing/audit DDL pool must be opened")
	}
	if primary != nil {
		t.Error("a separate billing/audit instance without a primary DDL DSN must yield a nil primary pool, never an alias to the billing/audit instance")
	}
}

// TestResolveDDLPools_InMemoryTopology pins the in-memory / SQLite topology: no
// DSNs at all, so both DDL pools are nil and no error. A regression that dialed
// here would fail startup in a topology that uses no Postgres.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_InMemoryTopology(t *testing.T) {
	open := func(context.Context, string) (*pgxpool.Pool, error) {
		t.Fatal("in-memory topology must not open any DDL pool")
		return nil, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{}, open, verify)
	if err != nil {
		t.Fatalf("in-memory resolve: %v", err)
	}
	// billingAudit empty and primaryDDL empty -> aliasPrimaryDDLToBillingAudit
	// is true, so primary aliases the (nil) billing/audit pool. Both are nil.
	if ba != nil || primary != nil {
		t.Errorf("in-memory topology must yield nil DDL pools, got ba=%v primary=%v", ba, primary)
	}
}

// TestResolveDDLPools_BillingAuditOpenErrorFailsClosed pins the fail-closed
// path: a failed billing/audit DDL pool open returns the wrapped error and no
// pools, so the caller escalates to log.Fatalf and the gateway does not start
// unable to provision sequences. A regression that swallowed the error would
// start a gateway that cannot create per-tenant sequences.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_BillingAuditOpenErrorFailsClosed(t *testing.T) {
	wantErr := errors.New("dial refused")
	open := func(context.Context, string) (*pgxpool.Pool, error) { return nil, wantErr }
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{
		billingAuditDDL: "postgres://baddl@host/lenny",
	}, open, verify)
	if !errors.Is(err, wantErr) {
		t.Fatalf("billing/audit open error must propagate, got %v", err)
	}
	if ba != nil || primary != nil {
		t.Error("a failed billing/audit open must return nil pools")
	}
}

// TestResolveDDLPools_PrimaryOpenErrorClosesBillingAudit pins the fail-closed
// cleanup: when the billing/audit DDL pool opens but the distinct primary DDL
// pool fails, resolveDDLPools closes the already-opened billing/audit pool
// before returning, so a failed startup leaks no connection pool. A regression
// that returned without closing would leak the billing/audit pool on every
// failed start with a bad primary DSN.
//
// spec: §12.3, §15.1. F-11.2.10
func TestResolveDDLPools_PrimaryOpenErrorClosesBillingAudit(t *testing.T) {
	baPool := lazyDDLPool(t)
	wantErr := errors.New("primary dial refused")
	open := func(_ context.Context, dsn string) (*pgxpool.Pool, error) {
		if dsn == "postgres://primaryddl@host/lenny" {
			return nil, wantErr
		}
		return baPool, nil
	}
	verify := func(context.Context, *pgxpool.Pool) error { return nil }

	ba, primary, err := resolveDDLPools(context.Background(), ddlPoolDSNs{
		billingAudit:    "postgres://ba@host/lenny",
		billingAuditDDL: "postgres://baddl@host/lenny",
		primaryDDL:      "postgres://primaryddl@host/lenny",
	}, open, verify)
	if !errors.Is(err, wantErr) {
		t.Fatalf("primary open error must propagate, got %v", err)
	}
	if ba != nil || primary != nil {
		t.Error("a failed primary open must return nil pools")
	}
	if _, acqErr := baPool.Acquire(context.Background()); acqErr == nil {
		t.Error("a failed primary open must Close the already-opened billing/audit pool")
	}
}

// TestCloseDDLPools_AliasClosedOnce pins the shutdown close loop: in the
// single-instance topology primaryDDLPool aliases billingAuditDDLPool, so
// closeDDLPools must close the shared pool exactly once. A regression that
// closed the aliased pool twice would double-close, and one that skipped it
// would leak the pool. This exercises the loop body Close call that runs only
// at shutdown. The pool observes its own closed state through a failed Acquire.
//
// spec: §12.3, §15.1. F-11.2.10
func TestCloseDDLPools_AliasClosedOnce(t *testing.T) {
	shared := lazyDDLPool(t)
	// Single-instance topology: primaryDDLPool aliases billingAuditDDLPool.
	closeDDLPools(shared, shared)
	if _, err := shared.Acquire(context.Background()); err == nil {
		t.Error("closeDDLPools must Close the shared pool; Acquire after Close should error")
	}
	// A second call over a nil pair is a no-op and must not panic.
	closeDDLPools(nil, nil)
}

// TestCloseDDLPools_SeparateInstancesBothClosed pins the separate-instance
// shutdown: a distinct billing/audit pool and primary pool are both closed. A
// regression that dropped the distinct primary pool would leak it at shutdown.
//
// spec: §12.3, §15.1. F-11.2.10
func TestCloseDDLPools_SeparateInstancesBothClosed(t *testing.T) {
	ba := lazyDDLPool(t)
	primary := lazyDDLPool(t)
	closeDDLPools(ba, primary)
	if _, err := ba.Acquire(context.Background()); err == nil {
		t.Error("closeDDLPools must Close the billing/audit pool")
	}
	if _, err := primary.Acquire(context.Background()); err == nil {
		t.Error("closeDDLPools must Close the distinct primary pool")
	}
}

// TestSequenceProvisioningAdminOptions_ThreadsPoolsAndResolvers pins the admin
// Router wiring: sequenceProvisioningAdminOptions fills the DDL pools and the
// §12.3 R-03 shard resolvers into the admin.Options the tenant-provisioning
// helper reads, without disturbing the caller-built base fields. A regression
// that dropped a DDL pool or a resolver would leave the per-tenant sequence
// provisioning unwired, so the first billing Append would nextval a nonexistent
// relation.
//
// spec: §12.3, §15.1. F-11.2.10
func TestSequenceProvisioningAdminOptions_ThreadsPoolsAndResolvers(t *testing.T) {
	ba := lazyDDLPool(t)
	primary := lazyDDLPool(t)
	shardPool := lazyDDLPool(t)
	sr := &recordingStoreRouter{pool: shardPool}

	w := &gatewayWiring{
		gatewayWiringFields: gatewayWiringFields{
			billingAuditDDLPool: ba,
			primaryDDLPool:      primary,
			storeRouter:         sr,
		},
	}

	base := admin.Options{DevMode: true}
	got := w.sequenceProvisioningAdminOptions(base)

	if !got.DevMode {
		t.Error("base options must be preserved; DevMode was dropped")
	}
	if got.BillingAuditDDLPool != ba {
		t.Error("BillingAuditDDLPool must be threaded from the wiring struct")
	}
	if got.PrimaryDDLPool != primary {
		t.Error("PrimaryDDLPool must be threaded from the wiring struct")
	}
	if got.BillingShard == nil || got.AuditShard == nil {
		t.Fatal("a non-nil StoreRouter must yield non-nil shard resolvers")
	}
	// The resolvers must adapt to the recorded StoreRouter, proving the
	// §12.3 R-03 instance is reachable from the admin Router.
	if _, err := got.BillingShard(context.Background(), "acme"); err != nil {
		t.Fatalf("BillingShard resolver: %v", err)
	}
	if sr.billingTenant != storerouter.TenantID("acme") {
		t.Errorf("BillingShard resolver passed %q, want acme", sr.billingTenant)
	}
}

// TestSequenceProvisioningAdminOptions_NilRouterNilResolvers pins the in-memory
// / SQLite topology: a nil StoreRouter yields nil shard resolvers, which the
// provisioning helper treats as "no re-seed read". A regression that returned
// non-nil resolvers over a nil StoreRouter would panic on the first re-seed.
//
// spec: §12.3, §15.1. F-11.2.10
func TestSequenceProvisioningAdminOptions_NilRouterNilResolvers(t *testing.T) {
	w := &gatewayWiring{} // no DDL pools, no storeRouter
	got := w.sequenceProvisioningAdminOptions(admin.Options{})
	if got.BillingAuditDDLPool != nil || got.PrimaryDDLPool != nil {
		t.Error("nil DDL pools must stay nil in the in-memory topology")
	}
	if got.BillingShard != nil || got.AuditShard != nil {
		t.Error("a nil StoreRouter must yield nil shard resolvers")
	}
}
