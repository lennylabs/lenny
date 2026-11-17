//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.6 StoreRouter, exercising the v1
// pkg/storerouter.SingleShardRouter against a real Postgres + Redis.
// Covers the per-method pool / client return contract, the empty-id
// guard, the per-concern Redis lookup, the scatter-gather ShardHandle
// shape, the R-03 billing/audit routing rule, and the
// session-shard prefix co-location invariant.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 12.6
// diagnosis: the §12.6 SingleShardRouter did not behave as specified
// against real backing services. The v1 router must route every
// accessor to the same Postgres pool and Redis client; the R-03
// billing/audit accessors must not error on a valid tenant id; the
// scatter-gather accessors must each return exactly one ShardHandle.
func TestStoreRouterContract(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rdb := containers.StartRedis(t, containers.RedisOptions{})

	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres: pg.Pool, Redis: rdb.Client,
	})
	if err != nil {
		t.Fatalf("NewSingleShardRouter: %v", err)
	}
	ctx := context.Background()

	t.Run("every accessor returns the v1 single pool", func(t *testing.T) {
		tenant := storerouter.TenantID("acme")
		sess := storerouter.SessionID("01234567-89ab-8cde-9f12-3456789abcde")

		tn, err := r.TenantShard(ctx, tenant)
		if err != nil {
			t.Fatalf("TenantShard: %v", err)
		}
		ss, err := r.SessionShard(ctx, sess)
		if err != nil {
			t.Fatalf("SessionShard: %v", err)
		}
		bi, err := r.BillingShard(ctx, tenant)
		if err != nil {
			t.Fatalf("BillingShard: %v", err)
		}
		au, err := r.AuditShard(ctx, tenant)
		if err != nil {
			t.Fatalf("AuditShard: %v", err)
		}
		pl, err := r.PlatformPostgres(ctx)
		if err != nil {
			t.Fatalf("PlatformPostgres: %v", err)
		}
		if tn != ss || ss != bi || bi != au || au != pl {
			t.Errorf("v1 router returned different pools: %p %p %p %p %p", tn, ss, bi, au, pl)
		}
	})

	t.Run("R-03 billing/audit accessors round-trip real SQL", func(t *testing.T) {
		// Confirm the returned pool is usable. The router's contract
		// is the routing decision; this is a smoke check that the
		// pool reaches Postgres.
		billing, err := r.BillingShard(ctx, "acme")
		if err != nil {
			t.Fatalf("BillingShard: %v", err)
		}
		var one int
		if err := billing.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
			t.Fatalf("billing SELECT: %v", err)
		}
		if one != 1 {
			t.Errorf("billing SELECT 1: got %d, want 1", one)
		}
		audit, err := r.AuditShard(ctx, "acme")
		if err != nil {
			t.Fatalf("AuditShard: %v", err)
		}
		if err := audit.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
			t.Errorf("audit SELECT: %v", err)
		}
	})

	t.Run("RedisShard pings against every documented concern", func(t *testing.T) {
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
				t.Fatalf("RedisShard(%s): %v", c, err)
			}
			if err := client.Ping(ctx).Err(); err != nil {
				t.Errorf("RedisShard(%s).Ping: %v", c, err)
			}
		}
	})

	t.Run("RedisShard rejects unknown concern", func(t *testing.T) {
		_, err := r.RedisShard(ctx, "acme", storerouter.RedisConcern("bogus"))
		if !errors.Is(err, storerouter.ErrUnknownRedisConcern) {
			t.Errorf("unknown concern: got %v, want ErrUnknownRedisConcern", err)
		}
	})

	t.Run("scatter-gather returns one ShardHandle", func(t *testing.T) {
		ss, err := r.AllSessionShards(ctx)
		if err != nil {
			t.Fatalf("AllSessionShards: %v", err)
		}
		if len(ss) != 1 {
			t.Errorf("AllSessionShards: got %d shards, want 1", len(ss))
		}
		if ss[0].ID == "" {
			t.Error("ShardHandle.ID empty; v1 router uses 'default'")
		}
		if ss[0].Pool != pg.Pool {
			t.Error("ShardHandle.Pool not the configured pool")
		}

		as, err := r.AllAuditShards(ctx)
		if err != nil {
			t.Fatalf("AllAuditShards: %v", err)
		}
		if len(as) != 1 {
			t.Errorf("AllAuditShards: got %d shards, want 1", len(as))
		}
	})

	t.Run("ShardCount reports one for every documented type", func(t *testing.T) {
		for _, st := range []storerouter.StoreType{
			storerouter.StoreTypeSession, storerouter.StoreTypeTenant,
			storerouter.StoreTypeBilling, storerouter.StoreTypeAudit,
		} {
			if n := r.ShardCount(st); n != 1 {
				t.Errorf("ShardCount(%s): got %d, want 1", st, n)
			}
		}
	})

	t.Run("SessionShard co-locates by prefix", func(t *testing.T) {
		// Two session ids share the first 32 bits (the routing
		// prefix) — a parent and a delegation child. v1 ignores the
		// prefix but the result must be the same pool so callers can
		// rely on tree-local queries.
		parent := storerouter.SessionID("aabbccdd-1111-8aaa-9bbb-cccccccccccc")
		child := storerouter.SessionID("aabbccdd-2222-8ddd-9eee-ffffffffffff")
		p, err := r.SessionShard(ctx, parent)
		if err != nil {
			t.Fatalf("SessionShard parent: %v", err)
		}
		c, err := r.SessionShard(ctx, child)
		if err != nil {
			t.Fatalf("SessionShard child: %v", err)
		}
		if p != c {
			t.Error("SessionShard co-location: parent and child must share a pool")
		}
	})

	t.Run("empty-id guards", func(t *testing.T) {
		if _, err := r.TenantShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidTenantID) {
			t.Errorf("TenantShard empty: got %v", err)
		}
		if _, err := r.SessionShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidSessionID) {
			t.Errorf("SessionShard empty: got %v", err)
		}
		if _, err := r.BillingShard(ctx, ""); !errors.Is(err, storerouter.ErrInvalidTenantID) {
			t.Errorf("BillingShard empty: got %v", err)
		}
	})
}
