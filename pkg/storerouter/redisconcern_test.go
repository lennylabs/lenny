// SPDX-License-Identifier: MIT

package storerouter_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// freshRedis returns a distinct *redis.Client. It never dials, so the
// pointer identity is the only thing under test here.
func freshRedis(t *testing.T, addr string) redis.UniversalClient {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// spec: §12.4 lines 237-245 — a per-concern split routes each store
// role to its own Redis instance. RedisShard returns the dedicated
// client for a split concern and falls back to the base client for a
// concern the operator left unsplit. F-12.4.16.
func TestRedisShardPerConcernSplit_spec_12_4_237(t *testing.T) {
	base := freshRedis(t, "127.0.0.1:6379")
	quota := freshRedis(t, "10.0.0.2:6379")
	delegation := freshRedis(t, "10.0.0.3:6379")
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres: fakePool(t),
		Redis:    base,
		RedisByConcern: map[storerouter.RedisConcern]redis.UniversalClient{
			storerouter.RedisConcernQuota:      quota,
			storerouter.RedisConcernDelegation: delegation,
		},
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()
	cases := []struct {
		concern storerouter.RedisConcern
		want    redis.UniversalClient
	}{
		{storerouter.RedisConcernQuota, quota},
		{storerouter.RedisConcernDelegation, delegation},
		// Unsplit concerns fall back to the base client.
		{storerouter.RedisConcernCoordination, base},
		{storerouter.RedisConcernCachePubSub, base},
		{storerouter.RedisConcernSessionData, base},
	}
	for _, tc := range cases {
		got, err := r.RedisShard(ctx, "acme", tc.concern)
		if err != nil {
			t.Errorf("RedisShard(%s): %v", tc.concern, err)
			continue
		}
		if got != tc.want {
			t.Errorf("RedisShard(%s): got %p, want %p", tc.concern, got, tc.want)
		}
	}
}

// spec: §12.4 line 52 (storerouter) — RedisConcernDelegation backs the
// tree-budget keys {root_session_id}:dlg:*. The router routes it to a
// dedicated client when split, giving the concern a live owner rather
// than a dead forward-declaration. F-12.4.18.
func TestRedisShardDelegationConcernHasOwner_spec_12_4_18(t *testing.T) {
	base := freshRedis(t, "127.0.0.1:6379")
	delegation := freshRedis(t, "10.0.0.9:6379")
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:       fakePool(t),
		Redis:          base,
		RedisByConcern: map[storerouter.RedisConcern]redis.UniversalClient{storerouter.RedisConcernDelegation: delegation},
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	got, err := r.RedisShard(context.Background(), "acme", storerouter.RedisConcernDelegation)
	if err != nil {
		t.Fatalf("RedisShard(delegation): %v", err)
	}
	if got != delegation {
		t.Errorf("delegation concern: got %p, want dedicated client %p", got, delegation)
	}
}

// PlatformRedis returns the explicit platform client when set; the
// §12.4 table places platform coordination keys on the Coordination
// instance. F-12.4.16.
func TestPlatformRedisUsesPlatformClient_spec_12_4_237(t *testing.T) {
	base := freshRedis(t, "127.0.0.1:6379")
	platform := freshRedis(t, "10.0.0.4:6379")
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:            fakePool(t),
		Redis:               base,
		PlatformRedisClient: platform,
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	got, err := r.PlatformRedis(context.Background())
	if err != nil {
		t.Fatalf("PlatformRedis: %v", err)
	}
	if got != platform {
		t.Errorf("PlatformRedis: got %p, want platform client %p", got, platform)
	}
}

// With no split configured every concern resolves to the single base
// client (the Tier 1/2 single-instance topology), unchanged from the
// pre-split behavior.
func TestRedisShardNoSplitFallsBackToBase_spec_12_4_245(t *testing.T) {
	base := freshRedis(t, "127.0.0.1:6379")
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: fakePool(t), Redis: base})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()
	for _, c := range []storerouter.RedisConcern{
		storerouter.RedisConcernCoordination, storerouter.RedisConcernQuota,
		storerouter.RedisConcernCachePubSub, storerouter.RedisConcernSessionData,
		storerouter.RedisConcernDelegation,
	} {
		got, err := r.RedisShard(ctx, "acme", c)
		if err != nil {
			t.Errorf("RedisShard(%s): %v", c, err)
			continue
		}
		if got != base {
			t.Errorf("RedisShard(%s): got %p, want base %p", c, got, base)
		}
	}
	platform, err := r.PlatformRedis(ctx)
	if err != nil {
		t.Fatalf("PlatformRedis: %v", err)
	}
	if platform != base {
		t.Errorf("PlatformRedis no-split: got %p, want base %p", platform, base)
	}
}
