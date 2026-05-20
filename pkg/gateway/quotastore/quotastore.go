// SPDX-License-Identifier: MIT

// Package quotastore is the Redis-backed §11.2 token-usage counter.
// It accumulates per-tenant, per-user token consumption into
// fixed reset-period windows under the §12.4 key
// t:{tenant_id}:quota:tokens:{user_id}:{window}.
//
// The counter records usage; the pure §11.2 arithmetic in pkg/quota
// (Check, HierarchicalCheck) interprets a window total against a
// configured limit. The §11.2 fail-open ceiling and the Redis-outage
// per-replica accounting are degraded-mode concerns layered on top of
// this counter and are not implemented here.
//
// The bucketed windows (hourly, daily, monthly) are fixed intervals.
// The §11.2 rolling window is a distinct sliding-window algorithm and
// is reported as unsupported by this counter.
package quotastore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/quota"
)

// Counter is the Redis-backed token-usage counter. Construct with New.
type Counter struct {
	client redis.UniversalClient
}

// New returns a Counter backed by client.
func New(client redis.UniversalClient) *Counter { return &Counter{client: client} }

// addScript increments a window counter and, when the key is newly
// created (it carries no TTL yet), gives it an expiry so a lapsed
// window self-evicts. It returns the new window total.
var addScript = redis.NewScript(`
local v = redis.call('INCRBY', KEYS[1], ARGV[1])
if redis.call('TTL', KEYS[1]) == -1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return v
`)

// Add accumulates tokens into the usage window of period that
// contains at, and returns the new window total. tokens must be
// non-negative.
func (c *Counter) Add(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, tokens int64) (int64, error) {
	if tokens < 0 {
		return 0, fmt.Errorf("quotastore: tokens must be non-negative, got %d", tokens)
	}
	key, ttl, err := windowKey(tenantID, userID, period, at)
	if err != nil {
		return 0, err
	}
	// The TTL is twice the window so a counter outlives its window
	// long enough for late reads and Postgres reconciliation.
	total, err := addScript.Run(ctx, c.client, []string{key},
		tokens, int64((2 * ttl).Seconds())).Int64()
	if err != nil {
		return 0, err
	}
	return total, nil
}

// Usage returns the recorded token total for the window of period
// containing at. A window with no recorded usage reads as 0.
func (c *Counter) Usage(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (int64, error) {
	key, _, err := windowKey(tenantID, userID, period, at)
	if err != nil {
		return 0, err
	}
	total, err := c.client.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return total, nil
}

// windowKey returns the §12.4 Redis key for the (tenant, user,
// period) usage window containing at, along with the window's length.
func windowKey(tenantID, userID string, period quota.ResetPeriod, at time.Time) (string, time.Duration, error) {
	label, length, err := window(period, at)
	if err != nil {
		return "", 0, err
	}
	return "t:" + tenantID + ":quota:tokens:" + userID + ":" + label, length, nil
}

// window maps a reset period and an instant to the window label and
// the window length. Only the fixed-interval periods are supported.
func window(period quota.ResetPeriod, at time.Time) (string, time.Duration, error) {
	at = at.UTC()
	switch period {
	case quota.ResetHourly:
		return "hourly-" + at.Format("2006010215"), time.Hour, nil
	case quota.ResetDaily:
		return "daily-" + at.Format("20060102"), 24 * time.Hour, nil
	case quota.ResetMonthly:
		return "monthly-" + at.Format("200601"), 31 * 24 * time.Hour, nil
	case quota.ResetRolling:
		return "", 0, errors.New("quotastore: the rolling reset period needs a sliding-window counter, not the fixed-window store")
	default:
		return "", 0, fmt.Errorf("quotastore: unknown reset period %q", period)
	}
}

// DefaultBucketResolution is the §11.2 sliding-window bucket size.
// One minute balances the memory cost of holding window/resolution
// counters against the granularity an attacker needs to evade
// the quota with bursty traffic.
const DefaultBucketResolution = time.Minute

// SlidingAdd accumulates tokens into the bucket containing at and
// returns the resulting total over the window ending at at. window
// is the rolling-window length (e.g., 1 hour); resolution is the
// bucket size used to sum across the window (e.g., 1 minute). The
// resolution must divide window; a non-positive resolution selects
// DefaultBucketResolution.
//
// The implementation keeps one Redis counter per bucket so a fresh
// bucket created during a write naturally expires after twice the
// window length (long enough for late reads and the §11.2
// Postgres-checkpoint reconciliation).
func (c *Counter) SlidingAdd(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time, tokens int64) (int64, error) {
	if tokens < 0 {
		return 0, fmt.Errorf("quotastore: tokens must be non-negative, got %d", tokens)
	}
	if window <= 0 {
		return 0, errors.New("quotastore: sliding window length must be positive")
	}
	if resolution <= 0 {
		resolution = DefaultBucketResolution
	}
	if window%resolution != 0 {
		return 0, fmt.Errorf("quotastore: sliding window %s is not a multiple of resolution %s", window, resolution)
	}
	key := slidingBucketKey(tenantID, userID, resolution, at)
	if _, err := addScript.Run(ctx, c.client, []string{key},
		tokens, int64((2 * window).Seconds())).Int64(); err != nil {
		return 0, err
	}
	return c.SlidingUsage(ctx, tenantID, userID, window, resolution, at)
}

// SlidingUsage returns the recorded token total over the rolling
// window of length window ending at at, summed in resolution-sized
// buckets. A bucket with no recorded usage reads as 0.
func (c *Counter) SlidingUsage(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time) (int64, error) {
	if window <= 0 {
		return 0, errors.New("quotastore: sliding window length must be positive")
	}
	if resolution <= 0 {
		resolution = DefaultBucketResolution
	}
	if window%resolution != 0 {
		return 0, fmt.Errorf("quotastore: sliding window %s is not a multiple of resolution %s", window, resolution)
	}
	bucketCount := int(window / resolution)
	keys := make([]string, bucketCount)
	for i := 0; i < bucketCount; i++ {
		t := at.Add(-time.Duration(i) * resolution)
		keys[i] = slidingBucketKey(tenantID, userID, resolution, t)
	}
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range values {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		var n int64
		if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
			total += n
		}
	}
	return total, nil
}

// slidingBucketKey returns the §12.4 Redis key for the bucket of
// resolution that contains at. The label is the bucket start
// truncated to resolution so two writes inside the same bucket
// land on the same key.
func slidingBucketKey(tenantID, userID string, resolution time.Duration, at time.Time) string {
	bucket := at.UTC().Truncate(resolution).Unix()
	return fmt.Sprintf("t:%s:quota:tokens:%s:sliding-%d-%d",
		tenantID, userID, int64(resolution.Seconds()), bucket)
}
