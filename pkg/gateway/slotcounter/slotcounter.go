// SPDX-License-Identifier: MIT

// Package slotcounter implements the §5.2 atomic slot counter for
// concurrent-mode pods. Slot allocation, cap enforcement, and release
// run as Redis Lua scripts so a single Redis-round-trip operation
// covers the GET+compare+INCR sequence the spec mandates — multiple
// gateway replicas racing to reserve a slot on the same pod cannot
// transiently exceed the pod's maxConcurrent bound.
//
// The Sandbox.status.activeSlots field stays as an observable mirror
// (writers update it after a successful Redis-side reservation or
// release), but the Redis counter is the source of truth.
//
// Post-recovery rehydration (Section 5.2 "Post-recovery rehydration
// atomicity") is not yet implemented: a Redis restart would reset the
// counters to zero and a fresh allocation could over-commit a pod
// whose Sandbox.status.activeSlots is non-zero. The MVP assumes Redis
// outlives the tier-7 run-load.sh invocation; rehydration is tracked
// as a follow-up in BUILD-GAPS.md.
package slotcounter

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ErrSlotsExhausted reports that the pod has already reached its
// maxConcurrent bound. The SlotClaimer's outer loop translates this
// to a "try the next pod" hint; if every pod in the pool returns it,
// the gateway surfaces WARM_POOL_EXHAUSTED with reason
// "concurrent_slots_exhausted".
var ErrSlotsExhausted = errors.New("slotcounter: pod has no free concurrent slot")

// Counter is the per-pod slot counter backed by Redis Lua. A single
// Counter serves every concurrent-mode pod in the cluster — the pod
// identifier is part of the Redis key.
type Counter struct {
	client *redis.Client
}

// New constructs a Counter against the given Redis client. The
// caller owns the client.
func New(client *redis.Client) *Counter {
	return &Counter{client: client}
}

// reserveScript implements the §5.2 atomic slot reservation. The Lua
// runs server-side, so the GET-compare-INCR sequence cannot interleave
// with another reservation on the same pod. Return value:
//
//   - integer >= 1 — the new (post-increment) slot count.
//   - integer -1   — the pod is at maxConcurrent and no slot was reserved.
//
// KEYS[1] is the active-slots key. ARGV[1] is maxConcurrent.
var reserveScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1])) or 0
if current >= tonumber(ARGV[1]) then
  return -1
end
return redis.call('INCR', KEYS[1])
`)

// releaseScript implements the §5.2 atomic slot release. Clamps at
// zero so a duplicate release (or a release against a counter that
// never saw a reservation) is a no-op rather than driving the count
// negative. KEYS[1] is the active-slots key.
var releaseScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1])) or 0
if current <= 0 then
  return 0
end
return redis.call('DECR', KEYS[1])
`)

// activeSlotsKey returns the canonical key for a pod's slot counter.
// The pod identifier (the §4.6.1 Sandbox name) is part of the key,
// keeping each pod's counter independent.
func activeSlotsKey(podID string) string {
	return "lenny:pod:" + podID + ":active_slots"
}

// Reserve atomically reserves one slot on podID, respecting the
// maxConcurrent cap. Returns the new post-increment slot count on
// success, or ErrSlotsExhausted if the pod is at cap.
func (c *Counter) Reserve(ctx context.Context, podID string, maxConcurrent int32) (int32, error) {
	if maxConcurrent < 1 {
		return 0, fmt.Errorf("slotcounter: maxConcurrent must be >= 1, got %d", maxConcurrent)
	}
	res, err := reserveScript.Run(ctx, c.client, []string{activeSlotsKey(podID)}, maxConcurrent).Int64()
	if err != nil {
		return 0, fmt.Errorf("slotcounter: reserve %s: %w", podID, err)
	}
	if res < 0 {
		return 0, ErrSlotsExhausted
	}
	return int32(res), nil
}

// Release atomically decrements the slot count on podID. Returns the
// new post-decrement count. A release on a counter at zero is a no-op
// that returns 0 (idempotent — covers double-release races and the
// post-pod-recreation case where the counter was zero anyway).
func (c *Counter) Release(ctx context.Context, podID string) (int32, error) {
	res, err := releaseScript.Run(ctx, c.client, []string{activeSlotsKey(podID)}).Int64()
	if err != nil {
		return 0, fmt.Errorf("slotcounter: release %s: %w", podID, err)
	}
	return int32(res), nil
}

// Reset removes the slot counter for podID. The §6.2 pod-replacement
// path calls this when a pod is retired — without the reset, a fresh
// pod that gets the same ID would inherit the old counter.
func (c *Counter) Reset(ctx context.Context, podID string) error {
	if err := c.client.Del(ctx, activeSlotsKey(podID)).Err(); err != nil {
		return fmt.Errorf("slotcounter: reset %s: %w", podID, err)
	}
	return nil
}
