// SPDX-License-Identifier: MIT

// Package slotcounter implements the §5.2 atomic slot counter for
// concurrent-mode pods. Slot allocation, cap enforcement, and release
// run as Redis Lua scripts so a single Redis-round-trip operation
// covers the GET+compare+INCR sequence the spec mandates — multiple
// gateway replicas racing to reserve a slot on the same pod cannot
// transiently exceed the pod's maxConcurrent bound.
//
// The Redis counter is the source of truth for intra-pod occupancy. The
// gateway no longer mirrors the count onto Sandbox.status; the
// WarmPoolController projects the pod's occupancy phase from the per-pod
// SandboxClaim binding state (§4.6.1, §4.6.3).
//
// Post-recovery rehydration (§5.2 "Post-recovery rehydration
// atomicity") is implemented: every reservation first checks the
// per-pod lenny:pod:{pod}:rehydrated flag inside the Lua script. When
// the flag is absent — the counter has not been seeded since the last
// Redis restart — the script returns a REHYDRATE_REQUIRED sentinel
// without reserving, and the gateway seeds the counter from
// SessionStore.GetActiveSlotsByPod before retrying. This blocks the
// race where two post-restart requests both observe counter=0 and
// over-commit a pod that already hosts live slots.
//
// The lenny:pod: prefix is one of §12.4's key-prefix exception classes
// (pod-scoped keys are not tenant-scoped): a concurrent-mode pod is
// shared infrastructure pinned to one tenant by §5.2 tenant pinning
// rather than a per-tenant key, so its slot counter and rehydration
// sentinels carry no {tenant_id} segment. The breaker store
// (pkg/gateway/breakerstore/redisstore) is another such exception, for
// its cb: prefix.
//
// The §6.57 / §12.4 Redis-outage Postgres fallback (the FallbackSource gate,
// the bounded fail-closed window, and the Redis-connectivity detection) lives
// in fallback.go; this file carries the Redis fast path and the §5.2
// rehydration protocol.
package slotcounter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrSlotsExhausted reports that the pod has already reached its
// maxConcurrent bound. The SlotClaimer's outer loop translates this
// to a "try the next pod" hint; if every pod in the pool returns it,
// the gateway surfaces WARM_POOL_EXHAUSTED with reason
// "concurrent_slots_exhausted".
var ErrSlotsExhausted = errors.New("slotcounter: pod has no free concurrent slot")

// ErrRehydrationStalled reports that a reservation could not converge:
// the rehydrated flag stayed absent across the bounded retry budget,
// so the §5.2 blocking-rehydration guarantee could not be satisfied
// (e.g., a peer replica holding the rehydrating lock crashed and its
// lock had not yet expired). The caller treats this like a transient
// claim failure and retries.
var ErrRehydrationStalled = errors.New("slotcounter: slot-counter rehydration did not converge")

// SlotSource seeds a pod's slot counter after a Redis restart. The
// production implementation is the Postgres-backed SessionStore, whose
// GetActiveSlotsByPod counts the live (non-terminal) sessions bound to
// the pod. A nil source disables Postgres-backed rehydration: the
// counter is seeded to zero on first access, which matches the
// pre-rehydration behaviour (a fresh counter starts at zero) and is
// only correct for deployments that never restart Redis under load.
// spec: §5.2 line 521.
type SlotSource interface {
	GetActiveSlotsByPod(ctx context.Context, podID string) (int, error)
}

// DefaultRehydrationTimeout bounds the §5.2 spin-wait a replica spends
// blocking on a peer's rehydrating lock (slotRehydrationTimeoutMs,
// default 2000ms). It also serves as the rehydrating lock's TTL so a
// crashed lock holder cannot block reservations indefinitely.
const DefaultRehydrationTimeout = 2000 * time.Millisecond

// rehydratePollInterval is the spin-wait cadence while blocking on a
// peer replica's rehydrating lock. Small relative to the timeout so a
// waiter resumes promptly once the peer sets the rehydrated flag.
const rehydratePollInterval = 25 * time.Millisecond

// maxReserveAttempts bounds the Reserve retry loop. A healthy path
// needs two passes (sentinel → rehydrate → retry). A spin-wait that
// times out against a crashed lock holder needs one more once the
// lock's TTL expires; the extra margin avoids a spurious stall error
// under contention.
const maxReserveAttempts = 4

// Lua return sentinels from reserveScript. A value >= 1 is the new
// post-increment slot count.
const (
	resultExhausted = -1 // pod at maxConcurrent; no slot reserved.
	resultRehydrate = -2 // rehydrated flag absent; seed before retrying.
)

// Counter is the per-pod slot counter backed by Redis Lua. A single
// Counter serves every concurrent-mode pod in the cluster — the pod
// identifier is part of the Redis key.
type Counter struct {
	client redis.UniversalClient

	// source seeds a pod's counter from Postgres on first access after
	// a Redis restart (§5.2 rehydration). Nil disables Postgres-backed
	// rehydration (seed-to-zero fallback).
	source SlotSource

	// fallback gates intra-pod capacity on Postgres during a Redis outage
	// (§12.4). Nil disables the gate: a Redis outage then fails closed
	// immediately.
	fallback FallbackSource

	// fallbackMaxWindow bounds the §12.4 Redis-outage fallback window. After
	// Redis has been continuously unreachable longer than this, the gate
	// fails closed.
	fallbackMaxWindow time.Duration

	// now supplies the wall clock for the §12.4 outage-window measurement.
	// Injectable for tests; defaults to time.Now.
	now func() time.Time

	// outageMu guards outageSince, the first-observed time of the current
	// Redis outage. Zero when Redis is reachable; stamped on the first
	// reservation that observes Redis unreachable and cleared on recovery.
	outageMu    sync.Mutex
	outageSince time.Time

	// rehydrationTimeout bounds the cross-replica spin-wait and the
	// rehydrating lock TTL.
	rehydrationTimeout time.Duration

	// podLocks holds the per-pod in-process rehydration mutex (§5.2:
	// "acquires a per-pod rehydration mutex (in-process ...)"). It
	// serializes rehydration attempts for one pod within this replica;
	// the Redis SET NX lock serializes across replicas. Steady-state
	// reservations (flag present) never take this lock, so only the
	// pod being rehydrated blocks — there is no global lock.
	mu       sync.Mutex
	podLocks map[string]*sync.Mutex
}

// Option configures a Counter at construction.
type Option func(*Counter)

// WithSlotSource wires the §5.2 rehydration seed source (the
// Postgres-backed SessionStore in production).
func WithSlotSource(s SlotSource) Option {
	return func(c *Counter) { c.source = s }
}

// WithRehydrationTimeout overrides the default §5.2 slotRehydrationTimeoutMs.
// A non-positive value keeps the default.
func WithRehydrationTimeout(d time.Duration) Option {
	return func(c *Counter) {
		if d > 0 {
			c.rehydrationTimeout = d
		}
	}
}

// New constructs a Counter against the given Redis client. The
// caller owns the client.
func New(client redis.UniversalClient, opts ...Option) *Counter {
	c := &Counter{
		client:             client,
		rehydrationTimeout: DefaultRehydrationTimeout,
		fallbackMaxWindow:  DefaultPostgresFallbackMaxWindow,
		now:                time.Now,
		podLocks:           make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// reserveScript implements the §5.2 atomic slot reservation with the
// post-recovery rehydration gate. The Lua runs server-side, so the
// EXISTS+GET-compare-INCR sequence cannot interleave with another
// reservation on the same pod. Return value:
//
//   - integer >= 1 — the new (post-increment) slot count.
//   - resultExhausted (-1) — the pod is at maxConcurrent.
//   - resultRehydrate (-2) — the rehydrated flag is absent; the caller
//     must seed the counter from Postgres before retrying (§5.2
//     blocking rehydration).
//
// KEYS[1] is the active-slots key, KEYS[2] the rehydrated flag.
// ARGV[1] is maxConcurrent.
var reserveScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then
  return -2
end
local current = tonumber(redis.call('GET', KEYS[1])) or 0
if current >= tonumber(ARGV[1]) then
  return -1
end
return redis.call('INCR', KEYS[1])
`)

// seedScript atomically writes the rehydrated count and sets the
// rehydrated flag (§5.2: "writes the accurate count ..., sets the
// rehydrated flag"). Performing both in one script means a reader
// never observes the flag set against a stale (zero) counter. KEYS[1]
// is the active-slots key, KEYS[2] the rehydrated flag. ARGV[1] is the
// seed count.
var seedScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], '1')
return 1
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

// unlockScript releases the rehydrating lock only when the caller
// still owns it (token match), so a holder whose lock already expired
// and was re-acquired by a peer cannot delete the peer's lock. Standard
// compare-and-delete. KEYS[1] is the rehydrating lock key; ARGV[1] is
// the owner token.
var unlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// activeSlotsKey returns the canonical key for a pod's slot counter.
// The pod identifier (the §4.6.1 Sandbox name) is part of the key,
// keeping each pod's counter independent.
func activeSlotsKey(podID string) string {
	return "lenny:pod:" + podID + ":active_slots"
}

// rehydratedKey returns the §12.4 per-pod rehydrated-flag key. Its
// presence marks the counter as seeded for the current Redis instance;
// it has no TTL and is cleared only by a Redis restart or pod reset.
func rehydratedKey(podID string) string {
	return "lenny:pod:" + podID + ":rehydrated"
}

// rehydratingKey returns the §12.4 per-pod rehydrating-lock key. A
// short-lived SET NX lock that serializes rehydration across replicas.
func rehydratingKey(podID string) string {
	return "lenny:pod:" + podID + ":rehydrating"
}

// Reserve atomically reserves one slot on podID, respecting the
// maxConcurrent cap. Returns the new post-increment slot count on
// success, or ErrSlotsExhausted if the pod is at cap.
//
// The rehydrated bool reports whether this call performed a §5.2
// post-recovery rehydration (a Postgres seed by this replica). The
// caller uses it to emit lenny_slot_rehydration_total exactly once per
// rehydration event; a peer-served rehydration that this call merely
// waited for reports false.
func (c *Counter) Reserve(ctx context.Context, podID string, maxConcurrent int32) (count int32, rehydrated bool, err error) {
	if maxConcurrent < 1 {
		return 0, false, fmt.Errorf("slotcounter: maxConcurrent must be >= 1, got %d", maxConcurrent)
	}
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID)}
	for attempt := 0; attempt < maxReserveAttempts; attempt++ {
		res, runErr := reserveScript.Run(ctx, c.client, keys, maxConcurrent).Int64()
		if runErr != nil {
			if isRedisUnavailable(runErr) {
				// spec: §12.4 line 208 — Redis is unreachable. Gate capacity
				// on the Postgres fallback under a per-pod advisory lock,
				// failing closed only after the bounded outage window.
				return c.fallbackReserve(ctx, podID, maxConcurrent, rehydrated)
			}
			return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: %w", podID, runErr)
		}
		// Redis answered: the outage (if any) is over. Clear the outage
		// marker so a later outage measures a fresh window.
		c.clearOutage()
		switch {
		case res >= 1:
			return int32(res), rehydrated, nil
		case res == resultExhausted:
			return 0, rehydrated, ErrSlotsExhausted
		case res == resultRehydrate:
			did, rErr := c.rehydrate(ctx, podID)
			if rErr != nil {
				if isRedisUnavailable(rErr) {
					return c.fallbackReserve(ctx, podID, maxConcurrent, rehydrated)
				}
				return 0, rehydrated, rErr
			}
			rehydrated = rehydrated || did
			// Retry the script: the flag is set on the path that seeded,
			// or a peer set it while we spin-waited.
		default:
			return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: unexpected script result %d", podID, res)
		}
	}
	return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: %w", podID, ErrRehydrationStalled)
}

// rehydrate seeds podID's slot counter from the SlotSource and sets the
// rehydrated flag, per §5.2 "Post-recovery rehydration atomicity". It
// returns true only when this replica performed the Postgres-backed
// seed (the lenny_slot_rehydration_total event); a call that found the
// flag already set, or that waited on a peer's lock, returns false.
//
// Coordination is two-layer (§5.2): a per-pod in-process mutex
// serializes rehydration within this replica, and a short-lived Redis
// SET NX lock on lenny:pod:{pod}:rehydrating serializes across
// replicas. A replica that cannot take the cross-replica lock
// spin-waits (bounded by rehydrationTimeout) for the peer to set the
// flag, then returns so Reserve can retry the script.
func (c *Counter) rehydrate(ctx context.Context, podID string) (bool, error) {
	lock := c.podLock(podID)
	lock.Lock()
	defer lock.Unlock()

	// Another goroutine on this replica may have rehydrated while we
	// waited on the in-process mutex; a peer replica may have set the
	// flag too. Either way the seed is done.
	done, err := c.flagSet(ctx, podID)
	if err != nil {
		return false, err
	}
	if done {
		return false, nil
	}

	if c.source == nil {
		// No Postgres-backed source: seed zero and set the flag so the
		// reservation can proceed (counter starts at zero, the
		// pre-rehydration behaviour). Not counted as a rehydration
		// event — no Postgres state was consulted.
		if err := c.seed(ctx, podID, 0); err != nil {
			return false, err
		}
		return false, nil
	}

	// Cross-replica lock. SET NX with a TTL so a crashed holder cannot
	// block peers past rehydrationTimeout.
	token, err := lockToken()
	if err != nil {
		return false, err
	}
	acquired, err := c.client.SetNX(ctx, rehydratingKey(podID), token, c.rehydrationTimeout).Result()
	if err != nil {
		return false, fmt.Errorf("slotcounter: acquire rehydrating lock %s: %w", podID, err)
	}
	if !acquired {
		// A peer is rehydrating. Block (bounded) until the flag appears,
		// then let Reserve retry the script.
		return false, c.waitForFlag(ctx, podID)
	}
	defer func() {
		// Best-effort token-checked release; the TTL is the backstop.
		_ = unlockScript.Run(context.WithoutCancel(ctx), c.client, []string{rehydratingKey(podID)}, token).Err()
	}()

	// Re-check under the cross-replica lock: a peer may have completed
	// and released between our flag check and lock acquisition.
	done, err = c.flagSet(ctx, podID)
	if err != nil {
		return false, err
	}
	if done {
		return false, nil
	}

	n, err := c.source.GetActiveSlotsByPod(ctx, podID)
	if err != nil {
		return false, fmt.Errorf("slotcounter: rehydrate %s from source: %w", podID, err)
	}
	if n < 0 {
		n = 0
	}
	if err := c.seed(ctx, podID, n); err != nil {
		return false, err
	}
	return true, nil
}

// seed writes the rehydrated count and sets the flag atomically.
func (c *Counter) seed(ctx context.Context, podID string, count int) error {
	if err := seedScript.Run(ctx, c.client,
		[]string{activeSlotsKey(podID), rehydratedKey(podID)}, count).Err(); err != nil {
		return fmt.Errorf("slotcounter: seed %s: %w", podID, err)
	}
	return nil
}

// flagSet reports whether the rehydrated flag exists for podID.
func (c *Counter) flagSet(ctx context.Context, podID string) (bool, error) {
	n, err := c.client.Exists(ctx, rehydratedKey(podID)).Result()
	if err != nil {
		return false, fmt.Errorf("slotcounter: check rehydrated flag %s: %w", podID, err)
	}
	return n == 1, nil
}

// waitForFlag spin-waits (bounded by rehydrationTimeout) for a peer
// replica to set the rehydrated flag. Returns nil once the flag is set
// or the budget is exhausted; Reserve retries the script either way
// (a timeout falls through to a fresh rehydration attempt once the
// peer's lock TTL has expired). Respects ctx cancellation.
func (c *Counter) waitForFlag(ctx context.Context, podID string) error {
	deadline := time.Now().Add(c.rehydrationTimeout)
	ticker := time.NewTicker(rehydratePollInterval)
	defer ticker.Stop()
	for {
		done, err := c.flagSet(ctx, podID)
		if err != nil {
			return err
		}
		if done || time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// podLock returns the per-pod in-process rehydration mutex, creating it
// on first use.
func (c *Counter) podLock(podID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, ok := c.podLocks[podID]
	if !ok {
		lock = &sync.Mutex{}
		c.podLocks[podID] = lock
	}
	return lock
}

// lockToken returns a random owner token for the rehydrating lock.
func lockToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("slotcounter: lock token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Release atomically decrements the slot count on podID. Returns the
// new post-decrement count. A release on a counter at zero is a no-op
// that returns 0 (idempotent — covers double-release races and the
// post-pod-recreation case where the counter was zero anyway).
//
// Release does not rehydrate: a release that arrives before the pod's
// first post-restart reservation clamps at zero, and the eventual
// rehydration reads the correct (post-release) count from Postgres
// because the released session has left the non-terminal set.
func (c *Counter) Release(ctx context.Context, podID string) (int32, error) {
	res, err := releaseScript.Run(ctx, c.client, []string{activeSlotsKey(podID)}).Int64()
	if err != nil {
		return 0, fmt.Errorf("slotcounter: release %s: %w", podID, err)
	}
	return int32(res), nil
}

// Reset removes the slot counter and the rehydration sentinels for
// podID. The §6.2 pod-replacement path calls this when a pod is retired
// — without the reset, a fresh pod that gets the same ID would inherit
// the old counter or rehydration state.
func (c *Counter) Reset(ctx context.Context, podID string) error {
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID), rehydratingKey(podID)}
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("slotcounter: reset %s: %w", podID, err)
	}
	return nil
}
