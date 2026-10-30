// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisScatterGatherCache is the Redis-backed ScatterGatherCache. The
// spec binds the platform-admin cross-tenant scatter-gather result to a
// shared 5-minute Redis entry keyed by a hash of the query parameters, so
// every gateway replica serves the same cached page and a dashboard that
// polls across replicas does not re-run the scatter fan-out per replica.
// The in-process MemScatterGatherCache keeps a single-replica dev/test
// gateway functional without Redis, but its entries are per-replica and
// give multi-replica deployments an incoherent cache.
//
// The cache is best-effort: a Redis error on read degrades to a miss (the
// authoritative scatter-gather read backs it) and a write error is
// logged and dropped rather than failing the query.
//
// spec: §25.9 (Query Limits and Scatter-Gather) — "platform-admin
// cross-tenant queries that use AllAuditShards() cache their results in
// Redis for 5 minutes keyed by a hash of the query parameters."
type RedisScatterGatherCache struct {
	client redis.UniversalClient
	logger *slog.Logger
}

// NewRedisScatterGatherCache returns a ScatterGatherCache backed by
// client. logger defaults to slog.Default when nil.
func NewRedisScatterGatherCache(client redis.UniversalClient, logger *slog.Logger) *RedisScatterGatherCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisScatterGatherCache{client: client, logger: logger}
}

var _ ScatterGatherCache = (*RedisScatterGatherCache)(nil)

// redisScatterCacheKeyPrefix namespaces the cross-tenant scatter-gather
// result entries in the shared Redis keyspace.
const redisScatterCacheKeyPrefix = "lenny:audit:scatter:"

// redisScatterCacheOpTimeout bounds each Redis round-trip so a slow or
// unreachable cache degrades to a miss rather than stalling the audit
// query behind an unbounded network call.
const redisScatterCacheOpTimeout = 2 * time.Second

func redisScatterCacheKey(key string) string { return redisScatterCacheKeyPrefix + key }

// Get returns the cached bytes for key when a live Redis entry exists. A
// miss (including a TTL-expired entry Redis has evicted) or any Redis
// error returns ok=false; a non-miss error is logged.
func (c *RedisScatterGatherCache) Get(key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), redisScatterCacheOpTimeout)
	defer cancel()
	val, err := c.client.Get(ctx, redisScatterCacheKey(key)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.logger.Warn("audit scatter-gather cache read failed", "err", err)
		}
		return nil, false
	}
	return val, true
}

// Set stores value under key for ttl using Redis native key expiry. A
// non-positive ttl is a no-op. A write error is logged and dropped: the
// cache is advisory and the authoritative scatter read remains available.
func (c *RedisScatterGatherCache) Set(key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisScatterCacheOpTimeout)
	defer cancel()
	if err := c.client.Set(ctx, redisScatterCacheKey(key), value, ttl).Err(); err != nil {
		c.logger.Warn("audit scatter-gather cache write failed", "err", err)
	}
}
