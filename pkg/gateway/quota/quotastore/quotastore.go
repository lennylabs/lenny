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

// ErrEmptyScope — an erasure primitive was called with an empty tenant
// id (or user id). Empty arguments must never be treated as "delete
// everything" (§12.8 line 753).
var ErrEmptyScope = errors.New("quotastore: erasure requires a non-empty tenant_id (and user_id)")

// QuotaStore is the §12.2 QuotaStore role interface. *Counter satisfies
// it. Alongside the usage-counter primitives it exposes the §12.1
// mandatory erasure pair so a substitute backend that omits either
// method cannot compile into the gateway binary.
//
// spec: §12.1 line 5 — every store role interface MUST expose
// DeleteByUser and DeleteByTenant, enforced at compile time by Go
// interface satisfaction.
type QuotaStore interface {
	Add(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, tokens int64) (int64, error)
	Usage(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (int64, error)
	SlidingAdd(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time, tokens int64) (int64, error)
	SlidingUsage(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time) (int64, error)
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Counter is the Redis-backed token-usage counter. Construct with New.
type Counter struct {
	client redis.UniversalClient
}

var _ QuotaStore = (*Counter)(nil)

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

// Scope-rollup slots. The §11.2 token budget is hierarchical
// (global ⊇ tenant ⊇ user), so HierarchicalCheck needs three
// independent window totals. A per-user window already exists; the
// per-tenant and global windows accumulate under reserved synthetic ids
// addressed by these constants. A real tenant id or OIDC subject never
// contains a NUL byte, so the synthetic slots never collide with a
// per-user window and a §12.8 DeleteByUser cannot zero a tenant rollup.
//
// The per-tenant rollup lives under the tenant's own key prefix, so a
// §12.8 DeleteByTenant SCAN (t:{tenant}:quota:tokens:*) erases it along
// with every per-user window. The global rollup lives under a synthetic
// tenant slot, so no individual tenant's erasure can zero the
// platform-wide window. spec: §11.2 (hierarchical token budget).
const (
	tenantRollupUser   = "\x00tenant-rollup"
	globalRollupTenant = "\x00global-rollup"
	globalRollupUser   = "\x00global-rollup"
)

// Scoped carries the §11.2 token usage at each scope of the
// hierarchy — the per-user window, the per-tenant rollup, and the
// platform-wide global rollup — so a single call returns every total
// quota.HierarchicalCheck needs.
type Scoped struct {
	User   int64
	Tenant int64
	Global int64
}

// AddHierarchical records tokens against the per-user, per-tenant, and
// global §11.2 windows of period containing at, and returns the three
// resulting window totals. Every token increment advances all three
// scopes so a later read sees an independent total per scope; this is
// the §11.2 write path the proxy-mode usage recorder drives on each
// upstream response. spec: §11.2 (global ⊇ tenant ⊇ user; "writes them
// to Redis immediately").
func (c *Counter) AddHierarchical(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, tokens int64) (Scoped, error) {
	user, err := c.Add(ctx, tenantID, userID, period, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	tenant, err := c.Add(ctx, tenantID, tenantRollupUser, period, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	global, err := c.Add(ctx, globalRollupTenant, globalRollupUser, period, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	return Scoped{User: user, Tenant: tenant, Global: global}, nil
}

// UsageHierarchical returns the §11.2 recorded token totals at the
// user, tenant, and global scopes for the window of period containing
// at. A scope with no recorded usage reads as 0. spec: §11.2
// (HierarchicalCheck reads one window per scope).
func (c *Counter) UsageHierarchical(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (Scoped, error) {
	user, err := c.Usage(ctx, tenantID, userID, period, at)
	if err != nil {
		return Scoped{}, err
	}
	tenant, err := c.Usage(ctx, tenantID, tenantRollupUser, period, at)
	if err != nil {
		return Scoped{}, err
	}
	global, err := c.Usage(ctx, globalRollupTenant, globalRollupUser, period, at)
	if err != nil {
		return Scoped{}, err
	}
	return Scoped{User: user, Tenant: tenant, Global: global}, nil
}

// SlidingAddHierarchical is the rolling-window counterpart of
// AddHierarchical: it records tokens into the bucket containing at at
// the user, tenant, and global scopes and returns the resulting totals
// over the rolling window ending at at. spec: §11.2 ("rolling window"
// reset period).
func (c *Counter) SlidingAddHierarchical(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time, tokens int64) (Scoped, error) {
	user, err := c.SlidingAdd(ctx, tenantID, userID, window, resolution, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	tenant, err := c.SlidingAdd(ctx, tenantID, tenantRollupUser, window, resolution, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	global, err := c.SlidingAdd(ctx, globalRollupTenant, globalRollupUser, window, resolution, at, tokens)
	if err != nil {
		return Scoped{}, err
	}
	return Scoped{User: user, Tenant: tenant, Global: global}, nil
}

// SlidingUsageHierarchical is the rolling-window counterpart of
// UsageHierarchical: it returns the user-, tenant-, and global-scope
// token totals over the rolling window of length window ending at at.
// spec: §11.2 ("rolling window" reset period).
func (c *Counter) SlidingUsageHierarchical(ctx context.Context, tenantID, userID string, window, resolution time.Duration, at time.Time) (Scoped, error) {
	user, err := c.SlidingUsage(ctx, tenantID, userID, window, resolution, at)
	if err != nil {
		return Scoped{}, err
	}
	tenant, err := c.SlidingUsage(ctx, tenantID, tenantRollupUser, window, resolution, at)
	if err != nil {
		return Scoped{}, err
	}
	global, err := c.SlidingUsage(ctx, globalRollupTenant, globalRollupUser, window, resolution, at)
	if err != nil {
		return Scoped{}, err
	}
	return Scoped{User: user, Tenant: tenant, Global: global}, nil
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

// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. It
// removes every per-user rate-limit / budget counter the tenant holds
// for userID — both the fixed-window keys and the sliding-window
// buckets all live under t:{tenant_id}:quota:tokens:{user_id}:* — and
// returns the count dropped. It is idempotent: a user with no recorded
// usage is a no-op returning (0, nil). Empty arguments are rejected
// (§12.8 line 753).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 step 6 ("QuotaStore —
// delete per-user rate-limit counters and budget tracking").
func (c *Counter) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, ErrEmptyScope
	}
	return scanDelete(ctx, c.client, "t:"+tenantID+":quota:tokens:"+userID+":*")
}

// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// removes every token-usage counter under the tenant's §12.4 key
// prefix t:{tenant_id}:quota:tokens:* across all users and windows,
// and returns the count dropped. It is idempotent: a tenant with no
// counters is a no-op returning (0, nil). Empty tenant id is rejected.
//
// spec: §12.1 line 5 (mandatory primitive); §12.2 (QuotaStore key
// prefix); §12.8 Phase 4 (tenant deletion).
func (c *Counter) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, ErrEmptyScope
	}
	return scanDelete(ctx, c.client, "t:"+tenantID+":quota:tokens:*")
}

// scanDelete SCANs the keyspace for pattern and deletes the matched
// keys in batches, returning the count removed. SCAN is cursor-based
// and non-blocking, so a large keyspace does not stall Redis. Keys
// that expire between SCAN and DEL simply do not count.
func scanDelete(ctx context.Context, client redis.UniversalClient, pattern string) (int, error) {
	const delBatch = 256
	var (
		batch   []string
		deleted int
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := client.Del(ctx, batch...).Result()
		if err != nil {
			return err
		}
		deleted += int(n)
		batch = batch[:0]
		return nil
	}
	iter := client.Scan(ctx, 0, pattern, 0).Iterator()
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
