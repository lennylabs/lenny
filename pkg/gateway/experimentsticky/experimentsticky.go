// SPDX-License-Identifier: MIT

// Package experimentsticky is the §10.7 `sticky: user` variant-assignment
// cache. An experiment with `targeting.sticky: user` caches its per-user
// variant assignment in Redis keyed by (user_id, experiment_id) so the
// gateway does not re-evaluate the assignment on every session creation.
//
// The cache matters most for `mode: external` experiments: §10.7 line 831
// states the OpenFeature provider "is not called again for subsequent
// sessions if a cached assignment exists", so without the cache every
// session pays a synchronous provider round-trip. Percentage-mode
// `sticky: user` assignment is deterministic in user_id under the built-in
// HMAC hash, so recomputing it yields the same variant and needs no cache;
// the cache is wired for the external path the spec calls out.
//
// On Redis unavailability the §12.4 failure-behavior row mandates a
// fail-open path: the gateway re-computes the assignment from the provider
// (or hash) instead of reading the cache. Get and Put surface their Redis
// errors so the caller can degrade to fresh evaluation; neither blocks the
// session-creation hot path on Redis.
package experimentsticky

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultTTL bounds how long a cached assignment survives without a
// refreshing read or write. The authoritative cleanup is the §10.7
// pause/conclude flush (Flush); the TTL is a safety net so a cache for an
// experiment that is deleted without a clean transition cannot leak keys
// indefinitely.
const DefaultTTL = 7 * 24 * time.Hour

// InvalidationRecorder receives one observation per §10.7 sticky-cache
// flush for the `lenny_experiment_sticky_cache_invalidations_total`
// counter, labeled by experiment_id and transition (§16.1 line 159).
// *gatewaymetrics.Metrics satisfies it. A nil recorder disables the metric;
// the flush still runs.
type InvalidationRecorder interface {
	RecordExperimentStickyCacheInvalidation(experimentID, transition string)
}

// RedisCache is the Redis-backed §10.7 sticky-assignment cache. Construct
// with NewRedis.
type RedisCache struct {
	client redis.UniversalClient
	ttl    time.Duration
	rec    InvalidationRecorder
}

// Option configures a RedisCache.
type Option func(*RedisCache)

// WithTTL overrides DefaultTTL.
func WithTTL(ttl time.Duration) Option {
	return func(c *RedisCache) {
		if ttl > 0 {
			c.ttl = ttl
		}
	}
}

// WithInvalidationRecorder wires the §16.1
// lenny_experiment_sticky_cache_invalidations_total counter.
func WithInvalidationRecorder(r InvalidationRecorder) Option {
	return func(c *RedisCache) { c.rec = r }
}

// NewRedis returns a sticky-assignment cache over client.
func NewRedis(client redis.UniversalClient, opts ...Option) *RedisCache {
	c := &RedisCache{client: client, ttl: DefaultTTL}
	for _, o := range opts {
		o(c)
	}
	return c
}

// assignmentKey is the §12.4 canonical sticky-cache key
// `t:{tenant_id}:exp:{experiment_id}:sticky:{user_id}`.
func assignmentKey(tenantID, experimentID, userID string) string {
	return fmt.Sprintf("t:%s:exp:%s:sticky:%s", tenantID, experimentID, userID)
}

// flushPattern is the §10.7 line 1096 flush glob
// `t:{tenant_id}:exp:{experiment_id}:sticky:*`.
func flushPattern(tenantID, experimentID string) string {
	return fmt.Sprintf("t:%s:exp:%s:sticky:*", tenantID, experimentID)
}

// nonEmpty rejects an empty key component. An empty tenant, experiment, or
// user id would produce a malformed key or a flush glob that matches more
// than the intended experiment; callers must never reach Redis with one.
func nonEmpty(tenantID, experimentID, userID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(experimentID) == "" || strings.TrimSpace(userID) == "" {
		return fmt.Errorf("experimentsticky: tenant_id, experiment_id, and user_id must be non-empty")
	}
	return nil
}

// Get returns the cached variant assignment for (tenant, experiment, user).
// ok is false on a cache miss. A Redis error is returned so the caller can
// fall open to fresh evaluation (§12.4 failure behavior); on error ok is
// false and variantID is empty.
func (c *RedisCache) Get(ctx context.Context, tenantID, experimentID, userID string) (variantID string, ok bool, err error) {
	if err := nonEmpty(tenantID, experimentID, userID); err != nil {
		return "", false, err
	}
	v, gerr := c.client.Get(ctx, assignmentKey(tenantID, experimentID, userID)).Result()
	if gerr == redis.Nil {
		return "", false, nil
	}
	if gerr != nil {
		return "", false, gerr
	}
	return v, true, nil
}

// Put writes the variant assignment for (tenant, experiment, user) with the
// configured TTL. A write that loses to a concurrent writer is harmless: a
// `sticky: user` assignment is stable for a fixed (user, experiment) pair,
// so any writer stores the same value. A Redis error is returned best-effort;
// the caller treats it as a non-fatal cache-write failure.
func (c *RedisCache) Put(ctx context.Context, tenantID, experimentID, userID, variantID string) error {
	if err := nonEmpty(tenantID, experimentID, userID); err != nil {
		return err
	}
	return c.client.Set(ctx, assignmentKey(tenantID, experimentID, userID), variantID, c.ttl).Err()
}

// Flush deletes every cached assignment for an experiment (§10.7 line 1096:
// `DEL` on all keys matching `t:{tenant_id}:exp:{experiment_id}:sticky:*`)
// and records one §16.1 invalidation observation. It is invoked when an
// experiment transitions to `paused` or `concluded`; `paused → active`
// requires no flush. transition is the target status, carried as the §16.1
// line 159 metric label. Returns the number of keys removed. SCAN is cursor-
// based so a large keyspace does not stall Redis.
func (c *RedisCache) Flush(ctx context.Context, tenantID, experimentID, transition string) (int, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(experimentID) == "" {
		return 0, fmt.Errorf("experimentsticky: flush requires a non-empty tenant_id and experiment_id")
	}
	deleted, err := c.scanDelete(ctx, flushPattern(tenantID, experimentID))
	// The invalidation is recorded for the flush operation itself: §10.7
	// names the counter "incremented on each flush", independent of how many
	// keys the sweep removed (an experiment with no cached users still
	// invalidated its — empty — sticky set).
	if c.rec != nil {
		c.rec.RecordExperimentStickyCacheInvalidation(experimentID, transition)
	}
	return deleted, err
}

// scanDelete SCANs the keyspace for pattern and deletes matched keys in
// batches, returning the count removed. Keys that expire between SCAN and
// DEL simply do not count.
func (c *RedisCache) scanDelete(ctx context.Context, pattern string) (int, error) {
	const delBatch = 256
	var (
		batch   []string
		deleted int
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := c.client.Del(ctx, batch...).Result()
		if err != nil {
			return err
		}
		deleted += int(n)
		batch = batch[:0]
		return nil
	}
	iter := c.client.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= delBatch {
			if err := flush(); err != nil {
				return deleted, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return deleted, err
	}
	if err := flush(); err != nil {
		return deleted, err
	}
	return deleted, nil
}
