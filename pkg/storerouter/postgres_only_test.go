// SPDX-License-Identifier: MIT

package storerouter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// spec: §12.3 R-03 line 144 — billing/audit writes route only Postgres
// shards, so the single-shard router supports a Postgres-only mode (no
// Redis) for the billing/audit store paths and their component tests.
// The Postgres accessors route as usual; the Redis accessors fail closed
// with ErrRedisUnavailable.
func TestSingleShardRouterPostgresOnly_spec_12_3_R03(t *testing.T) {
	t.Parallel()
	pool := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pool})
	if err != nil {
		t.Fatalf("NewSingleShardRouter(nil redis) err = %v, want nil", err)
	}
	ctx := context.Background()

	// The Postgres shard accessors route without a Redis client.
	for _, tc := range []struct {
		name string
		get  func() (*pgxpool.Pool, error)
	}{
		{"BillingShard", func() (*pgxpool.Pool, error) { return r.BillingShard(ctx, "acme") }},
		{"AuditShard", func() (*pgxpool.Pool, error) { return r.AuditShard(ctx, "acme") }},
		{"TenantShard", func() (*pgxpool.Pool, error) { return r.TenantShard(ctx, "acme") }},
		{"PlatformPostgres", func() (*pgxpool.Pool, error) { return r.PlatformPostgres(ctx) }},
	} {
		got, gerr := tc.get()
		if gerr != nil || got != pool {
			t.Errorf("%s = (%p, %v), want (%p, nil)", tc.name, got, gerr, pool)
		}
	}

	// AllAuditShards yields the single Postgres shard for the cross-tenant
	// audit-worker scatter.
	shards, err := r.AllAuditShards(ctx)
	if err != nil || len(shards) != 1 || shards[0].Pool != pool {
		t.Errorf("AllAuditShards = (%v, %v), want one handle over the pool", shards, err)
	}

	// The Redis accessors fail closed with ErrRedisUnavailable.
	if _, err := r.RedisShard(ctx, "acme", storerouter.RedisConcernQuota); !errors.Is(err, storerouter.ErrRedisUnavailable) {
		t.Errorf("RedisShard err = %v, want ErrRedisUnavailable", err)
	}
	if _, err := r.PlatformRedis(ctx); !errors.Is(err, storerouter.ErrRedisUnavailable) {
		t.Errorf("PlatformRedis err = %v, want ErrRedisUnavailable", err)
	}
}
