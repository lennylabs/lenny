// SPDX-License-Identifier: MIT

package quotastore

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/quota"
)

// WindowLabel returns the §12.4 window label the (period, at) pair maps
// to. The label identifies the fixed-interval bucket a token total
// belongs to (e.g. "hourly-2026060214"); the §11.2 Postgres checkpoint
// stores it so a later reconcile only restores a counter whose window is
// still current rather than reviving a bucket that has already rolled
// over. Only the fixed-interval periods carry a stable label; the rolling
// period returns an error because a sliding-window sum spans many buckets
// with no single label. spec: §11.2 line 44; §12.4.
func WindowLabel(period quota.ResetPeriod, at time.Time) (string, error) {
	label, _, err := window(period, at)
	return label, err
}

// WindowLength returns the fixed-interval length of period (one hour /
// day / month). It is the unit the reconcile uses to refresh a restored
// counter's TTL. The rolling period returns an error. spec: §12.4.
func WindowLength(period quota.ResetPeriod, at time.Time) (time.Duration, error) {
	_, length, err := window(period, at)
	return length, err
}

// TenantRollupUsage returns the §11.2 per-tenant rollup window total for
// the fixed-interval window of period containing at. It reads the same
// reserved tenant-rollup slot AddHierarchical advances, so the checkpoint
// persists the tenant-scope counter the §11.2 line 48 reconstruction
// restores ("for each active session and tenant scope"). A window with no
// recorded usage reads as 0. spec: §11.2 line 44.
func (c *Counter) TenantRollupUsage(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) (int64, error) {
	return c.Usage(ctx, tenantID, tenantRollupUser, period, at)
}

// restoreScript applies the §11.2 line 44 MAX rule to a single fixed-window
// counter: it raises the key to ARGV[1] only when the checkpoint exceeds
// the live Redis value, so a stale checkpoint (up to quotaSyncIntervalSeconds
// old) can never lower a counter below its actual accumulated usage and
// silently un-enforce a budget breach. A newly created key is given the
// 2×window TTL the write path uses so a restored counter still self-evicts
// when its window lapses. It returns the resulting window total.
var restoreScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local target = tonumber(ARGV[1])
local val = cur
if target > cur then
  redis.call('SET', KEYS[1], target)
  val = target
end
if redis.call('EXISTS', KEYS[1]) == 1 and redis.call('TTL', KEYS[1]) == -1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return val
`)

// RestoreUserWindow applies the §11.2 line 48 MAX rule to the per-user
// token-usage window of period containing at:
// restored = MAX(redis_current, checkpointValue). It is the write half of
// the Redis-recovery reconstruction and the operator-driven §24.6
// reconcile — the counter is raised to the durable checkpoint when Redis
// came back empty (or with a value below the checkpoint) and left intact
// when the live value is already higher. It returns the resulting window
// total. A negative checkpointValue is rejected. spec: §11.2 line 48;
// §24.6 line 99.
func (c *Counter) RestoreUserWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, checkpointValue int64) (int64, error) {
	return c.restoreWindow(ctx, tenantID, userID, period, at, checkpointValue)
}

// RestoreTenantRollupWindow applies the §11.2 line 48 MAX rule to the
// per-tenant rollup window (the tenant-scope counter the spec names
// alongside the per-session scope). It returns the resulting total.
// spec: §11.2 line 48; §24.6 line 99.
func (c *Counter) RestoreTenantRollupWindow(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time, checkpointValue int64) (int64, error) {
	return c.restoreWindow(ctx, tenantID, tenantRollupUser, period, at, checkpointValue)
}

// restoreWindow is the shared MAX-rule write for a single fixed-interval
// window key.
func (c *Counter) restoreWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, checkpointValue int64) (int64, error) {
	if checkpointValue < 0 {
		return 0, errors.New("quotastore: checkpoint value must be non-negative")
	}
	key, length, err := windowKey(tenantID, userID, period, at)
	if err != nil {
		return 0, err
	}
	val, err := restoreScript.Run(ctx, c.client, []string{key},
		checkpointValue, int64((2 * length).Seconds())).Int64()
	if err != nil {
		return 0, err
	}
	return val, nil
}
