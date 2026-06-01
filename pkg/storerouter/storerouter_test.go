// SPDX-License-Identifier: MIT

package storerouter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// fakePool returns a *pgxpool.Pool stub. The router treats the pool
// opaquely (it never dials it), so an unconnected handle is fine for
// the contract tests.
func fakePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://lenny:lenny@127.0.0.1:5432/lenny?sslmode=disable")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	// Build a *pgxpool.Pool that never actually dials. The simplest
	// way is NewWithConfig — it returns a configured pool without an
	// initial round-trip.
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func fakeRedis(t *testing.T) redis.UniversalClient {
	t.Helper()
	c := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{"127.0.0.1:6379"}})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func newRouter(t *testing.T) *storerouter.SingleShardRouter {
	t.Helper()
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres: fakePool(t),
		Redis:    fakeRedis(t),
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	return r
}

// spec: §12.6; §12.3 R-03 line 144
// diagnosis: NewSingleShardRouter accepted a nil pool and produced a
// router that would panic on first Postgres use, so the constructor MUST
// reject a nil pool. A nil Redis client is permitted: the §12.3 R-03
// billing/audit paths route only Postgres shards, so the router runs in
// Postgres-only mode and the Redis accessors return ErrRedisUnavailable
// (covered by TestSingleShardRouterPostgresOnly_spec_12_3_R03).
func TestNewSingleShardRouterRejectsNilPostgres(t *testing.T) {
	if _, err := storerouter.NewSingleShardRouter(storerouter.Config{Redis: fakeRedis(t)}); err == nil {
		t.Error("nil Postgres: expected error")
	}
	if _, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: fakePool(t)}); err != nil {
		t.Errorf("nil Redis (Postgres-only mode): unexpected error %v", err)
	}
}

func TestSingleShardRouterReturnsSamePoolForEveryAccessor(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()

	tenant := storerouter.TenantID("acme")
	sess := storerouter.SessionID("01234567-89ab-8cde-9f12-3456789abcde")

	tenShard, err := r.TenantShard(ctx, tenant)
	if err != nil {
		t.Fatalf("TenantShard: %v", err)
	}
	sessShard, err := r.SessionShard(ctx, sess)
	if err != nil {
		t.Fatalf("SessionShard: %v", err)
	}
	billing, err := r.BillingShard(ctx, tenant)
	if err != nil {
		t.Fatalf("BillingShard: %v", err)
	}
	audit, err := r.AuditShard(ctx, tenant)
	if err != nil {
		t.Fatalf("AuditShard: %v", err)
	}
	platform, err := r.PlatformPostgres(ctx)
	if err != nil {
		t.Fatalf("PlatformPostgres: %v", err)
	}
	// Every accessor must point at the same Postgres pool in v1.
	if tenShard != sessShard || sessShard != billing || billing != audit || audit != platform {
		t.Errorf("v1 router returned different pools: tenShard=%p sessShard=%p billing=%p audit=%p platform=%p",
			tenShard, sessShard, billing, audit, platform)
	}
}

// spec: §12.3 line 103 — when LENNY_PG_BILLING_AUDIT_DSN is configured,
// BillingShard, AuditShard, and AllAuditShards route to the separate
// billing/audit instance while every other accessor stays on the
// primary. This is the Tier-3 instance-separation posture (§12.3 line
// 130) that offloads the two largest append-only write sources. F-12.3.5.
func TestSingleShardRouterBillingAuditPool_spec_12_3_line_103(t *testing.T) {
	t.Parallel()
	primary := fakePool(t)
	billingAudit := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:             primary,
		BillingAuditPostgres: billingAudit,
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()
	tenant := storerouter.TenantID("acme")

	// Billing and audit writes route to the separate instance.
	if got, _ := r.BillingShard(ctx, tenant); got != billingAudit {
		t.Errorf("BillingShard = %p, want billing/audit pool %p", got, billingAudit)
	}
	if got, _ := r.AuditShard(ctx, tenant); got != billingAudit {
		t.Errorf("AuditShard = %p, want billing/audit pool %p", got, billingAudit)
	}
	// The audit scatter-gather scan visits the separate instance.
	shards, err := r.AllAuditShards(ctx)
	if err != nil {
		t.Fatalf("AllAuditShards: %v", err)
	}
	if len(shards) != 1 || shards[0].Pool != billingAudit {
		t.Errorf("AllAuditShards pool = %v, want billing/audit pool %p", shards, billingAudit)
	}
	// Every other accessor stays on the primary.
	if got, _ := r.TenantShard(ctx, tenant); got != primary {
		t.Errorf("TenantShard = %p, want primary %p", got, primary)
	}
	if got, _ := r.SessionShard(ctx, "01234567-89ab-8cde-9f12-3456789abcde"); got != primary {
		t.Errorf("SessionShard = %p, want primary %p", got, primary)
	}
	if got, _ := r.PlatformPostgres(ctx); got != primary {
		t.Errorf("PlatformPostgres = %p, want primary %p", got, primary)
	}
	if sess, _ := r.AllSessionShards(ctx); len(sess) != 1 || sess[0].Pool != primary {
		t.Errorf("AllSessionShards pool = %v, want primary %p", sess, primary)
	}
}

// spec: §12.3 line 103 — when no separate billing/audit DSN is
// configured (the default), billing and audit writes fall back to the
// primary pool so a single well-provisioned primary serves Tier-3 load
// unchanged. F-12.3.5.
func TestSingleShardRouterBillingAuditFallsBackToPrimary_spec_12_3_line_103(t *testing.T) {
	t.Parallel()
	primary := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: primary})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()
	if got, _ := r.BillingShard(ctx, "acme"); got != primary {
		t.Errorf("BillingShard = %p, want primary %p", got, primary)
	}
	if got, _ := r.AuditShard(ctx, "acme"); got != primary {
		t.Errorf("AuditShard = %p, want primary %p", got, primary)
	}
	if shards, _ := r.AllAuditShards(ctx); len(shards) != 1 || shards[0].Pool != primary {
		t.Errorf("AllAuditShards pool = %v, want primary %p", shards, primary)
	}
}

// spec: 12.6 SessionShard / TenantShard
// diagnosis: an empty tenant or session id slipped past the router
// into a SQL call, where it would have either errored deep in the
// SQL driver or scoped a query to the empty string. The router
// rejects empty ids at the surface so the failure is local and
// debuggable.
func TestSingleShardRouterRejectsEmptyIDs(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()
	if _, err := r.TenantShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidTenantID) {
		t.Errorf("empty tenant: got %v, want ErrInvalidTenantID", err)
	}
	if _, err := r.SessionShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidSessionID) {
		t.Errorf("empty session: got %v, want ErrInvalidSessionID", err)
	}
	if _, err := r.BillingShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidTenantID) {
		t.Errorf("empty billing tenant: got %v", err)
	}
	if _, err := r.AuditShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidTenantID) {
		t.Errorf("empty audit tenant: got %v", err)
	}
	if _, err := r.RedisShard(ctx, "", storerouter.RedisConcernCoordination); !errors.Is(err, storerouter.ErrInvalidTenantID) {
		t.Errorf("empty redis tenant: got %v", err)
	}
}

// spec: 12.6 RedisShard
// diagnosis: an unknown RedisConcern slipped past the router. The
// router rejects it so the call site cannot use an undefined
// concern name (which would otherwise map to the same v1 client and
// hide the typo until the future split lands).
func TestRedisShardRejectsUnknownConcern(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()
	_, err := r.RedisShard(ctx, "acme", storerouter.RedisConcern("not_a_concern"))
	if !errors.Is(err, storerouter.ErrUnknownRedisConcern) {
		t.Errorf("unknown concern: got %v, want ErrUnknownRedisConcern", err)
	}
}

func TestRedisShardAcceptsEveryDocumentedConcern(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()
	concerns := []storerouter.RedisConcern{
		storerouter.RedisConcernCoordination,
		storerouter.RedisConcernQuota,
		storerouter.RedisConcernCachePubSub,
		storerouter.RedisConcernDelegation,
		storerouter.RedisConcernSessionData,
	}
	for _, c := range concerns {
		client, err := r.RedisShard(ctx, "acme", c)
		if err != nil {
			t.Errorf("RedisShard(%s): %v", c, err)
			continue
		}
		if client == nil {
			t.Errorf("RedisShard(%s) returned nil client", c)
		}
	}
}

// spec: 12.6 AllSessionShards / AllAuditShards
// diagnosis: v1 router returned zero or more than one shard. v1
// guarantees exactly one shard per accessor; a caller running a
// scatter-gather query in v1 iterates a single ShardHandle. The
// invariant is load-bearing for the §12.3 scatter-gather contract.
func TestSingleShardScatterAccessorsReturnOneHandle(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()

	session, err := r.AllSessionShards(ctx)
	if err != nil {
		t.Fatalf("AllSessionShards: %v", err)
	}
	if len(session) != 1 {
		t.Errorf("AllSessionShards: got %d shards, want 1", len(session))
	}

	audit, err := r.AllAuditShards(ctx)
	if err != nil {
		t.Fatalf("AllAuditShards: %v", err)
	}
	if len(audit) != 1 {
		t.Errorf("AllAuditShards: got %d shards, want 1", len(audit))
	}

	if session[0].Pool != audit[0].Pool {
		t.Error("v1 router: scatter-gather Postgres pools must match")
	}
}

func TestShardCountReturnsOneForDocumentedTypes(t *testing.T) {
	r := newRouter(t)
	for _, st := range []storerouter.StoreType{
		storerouter.StoreTypeSession, storerouter.StoreTypeTenant,
		storerouter.StoreTypeBilling, storerouter.StoreTypeAudit,
	} {
		if n := r.ShardCount(st); n != 1 {
			t.Errorf("ShardCount(%s): got %d, want 1", st, n)
		}
	}
	if n := r.ShardCount("unknown"); n != 0 {
		t.Errorf("ShardCount(unknown): got %d, want 0", n)
	}
}

// spec: 12.6 SessionShard routing prefix
// diagnosis: a future router must extract the first 32 bits of the
// UUIDv8 session ID and consistent-hash them. The v1 router ignores
// the prefix — but it must still route every session ID in a
// delegation tree (which share the same prefix) to the same shard.
// This test confirms the v1 router returns the same pool for two
// session IDs that share the same 32-bit prefix.
func TestSessionShardCoLocatesByPrefix(t *testing.T) {
	r := newRouter(t)
	ctx := context.Background()
	parent, err := r.SessionShard(ctx, "01234567-aaaa-8bbb-9ccc-111111111111")
	if err != nil {
		t.Fatalf("SessionShard parent: %v", err)
	}
	child, err := r.SessionShard(ctx, "01234567-zzzz-8yyy-9xxx-222222222222")
	if err != nil {
		t.Fatalf("SessionShard child: %v", err)
	}
	if parent != child {
		t.Error("v1 router: sessions sharing the routing prefix must co-locate")
	}
}
