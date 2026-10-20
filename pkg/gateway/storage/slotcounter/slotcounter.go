// SPDX-License-Identifier: MIT

// Package slotcounter implements the §5.2 slot set for concurrent-mode
// pods. A pod's slot occupancy is the Redis set
// lenny:pod:{pod_id}:active_slots, which holds one member per session that
// holds a slot on the pod, named by the session identifier, and one member
// leaked:<sessionId> per slot whose cleanup was not acknowledged clean
// (§6.2 leaked slot semantics). The set's cardinality is the pod's
// occupancy. Reservation, release, and the rehydration seed run as Redis Lua
// scripts, so a membership test, a cap check, and the add or remove cannot
// interleave with another replica's call on the same pod.
//
// The release is keyed by the session identifier and idempotent: it removes
// the session's member and reports whether it removed one and the remaining
// cardinality. Every path that releases the same reservation can therefore
// run, and while Redis keeps the set the reservation is returned once. The
// caller runs the occupancy-zero disposition only on a release that removed
// the member and left the set empty.
//
// Post-recovery rehydration (§5.2 "Post-recovery rehydration atomicity"):
// every reservation and release first checks the per-pod
// lenny:pod:{pod_id}:rehydrated flag inside its script. When the flag is
// absent the script returns a REHYDRATE_REQUIRED sentinel, and the gateway
// adds the identifiers SlotSource.GetActiveSlotsByPod returns to the set,
// keeping the members it already holds, sets the flag, and retries.
//
// The per-pod keys carry the pod identifier as a Redis hash tag
// (lenny:pod:{<pod>}:active_slots, :rehydrated, and :rehydrating), so every
// multi-key script and Reset's DEL stay in one Redis Cluster slot. The
// lenny:pod: prefix is one of §12.4's key-prefix exception classes: a
// concurrent-mode pod is shared infrastructure pinned to one tenant by §5.2
// tenant pinning, so its keys carry no tenant segment.
//
// The §12.4 Redis-outage Postgres fallback (the FallbackSource gate, the
// bounded fail-closed window, and the Redis-connectivity detection) lives in
// fallback.go, and the outage ledger and the add-only recovery rebuild that
// runs when an outage ends live in recovery.go.
package slotcounter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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

// SlotSource is the §5.2 rehydration seed and the §12.4 recovery listing.
// The production implementation is the Postgres-backed SessionStore.
//
// GetActiveSlotsByPod returns the identifiers of the pod's slot holders: the
// sessions whose row is non-terminal and whose pod_assignment names the pod
// (the slot-holder predicate). The rehydration adds them to the pod's set and
// removes no member.
//
// ListPodsWithActiveSlots returns the distinct pods that a non-terminal
// concurrent-workspace session row names, across tenants. The recovery
// rebuild deletes each listed pod's rehydrated flag, so the pod's next
// reservation or release rehydrates its set from the rows.
//
// A nil source disables Postgres-backed rehydration: the set is seeded with
// no member on first access (apart from a release's own member), which is
// only correct for deployments that never restart Redis under load.
// spec: §5.2 (Post-recovery rehydration atomicity), §12.4 (Redis failure
// behavior).
type SlotSource interface {
	GetActiveSlotsByPod(ctx context.Context, podID string) ([]string, error)
	ListPodsWithActiveSlots(ctx context.Context) ([]string, error)
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

// Lua return sentinels from reserveScript and releaseScript. A reservation
// value >= 1 is the set's cardinality after the reservation.
const (
	resultExhausted = -1 // pod at maxConcurrent; no slot reserved.
	resultRehydrate = -2 // rehydrated flag absent; seed before retrying.
)

// Counter is the per-pod slot set backed by Redis Lua. A single Counter
// serves every concurrent-mode pod in the cluster; the pod identifier is the
// hash tag of every per-pod key.
type Counter struct {
	client redis.UniversalClient

	// source seeds a pod's set from Postgres on first access after a
	// Redis restart (§5.2 rehydration) and lists the pods the recovery
	// rebuild names. Nil disables Postgres-backed rehydration.
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

	// outageMu guards outageSince and the outage ledger. outageSince is
	// the first-observed time of the current Redis outage: zero when no
	// outage is being recovered, stamped by a fallback admission, an
	// unreachable probe tick, or a release refused because Redis is
	// unreachable, and cleared only by a rebuild that leaves the ledger
	// empty (recovery.go).
	outageMu    sync.Mutex
	outageSince time.Time
	// ledger records the members the recovery rebuild adds when the outage
	// ends: fallback admissions and leaked marks. Each entry carries the
	// sequence number of its latest recording (ledgerSeq), so an entry
	// recorded again after a rebuild read the ledger survives that rebuild.
	ledger    map[ledgerEntry]uint64
	ledgerSeq uint64

	// rebuildMu makes the recovery rebuild single-flight per Counter.
	rebuildMu sync.Mutex

	// probe reports Redis reachability for RunRecovery; probeInterval is
	// its cadence. Defaults: a Ping on the Counter's client and
	// DefaultRecoveryProbeInterval.
	probe         func(ctx context.Context) error
	probeInterval time.Duration

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
		ledger:             make(map[ledgerEntry]uint64),
		probeInterval:      DefaultRecoveryProbeInterval,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// reserveScript implements the §5.2 atomic slot reservation with the
// post-recovery rehydration gate. Return value:
//
//   - integer >= 1: the set's cardinality after the reservation. A session
//     whose member is already present adds nothing and gets the current
//     cardinality, whatever it is, so a repeated reservation succeeds at
//     the cap.
//   - resultExhausted (-1): the set has reached maxConcurrent.
//   - resultRehydrate (-2): the rehydrated flag is absent; the caller
//     rehydrates the set before retrying (§5.2 blocking rehydration).
//
// KEYS[1] is the active-slots set, KEYS[2] the rehydrated flag. ARGV[1] is
// the session identifier and ARGV[2] maxConcurrent.
// spec: §5.2 (Slot assignment atomicity).
var reserveScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then return -2 end
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then return redis.call('SCARD', KEYS[1]) end
local n = redis.call('SCARD', KEYS[1])
if n >= tonumber(ARGV[2]) then return -1 end
redis.call('SADD', KEYS[1], ARGV[1])
return n + 1
`)

// seedScript adds the rehydration seed's session identifiers to the set and
// sets the rehydrated flag in one script, so a reader never observes the
// flag set before the seed. It removes no member: a leaked: member, a
// reservation whose row does not yet name the pod, and a member another
// replica added stay (§5.2: the rehydration keeps the members the set
// already holds). KEYS[1] is the active-slots set, KEYS[2] the rehydrated
// flag; ARGV is the identifiers to add, possibly none.
// spec: §5.2 (Post-recovery rehydration atomicity).
var seedScript = redis.NewScript(`
for i = 1, #ARGV do redis.call('SADD', KEYS[1], ARGV[i]) end
redis.call('SET', KEYS[2], '1')
return 1
`)

// releaseScript implements the §5.2 keyed slot release. It returns
// {removed, cardinality}: removed is 1 when the session's member was
// present. A release with the rehydrated flag absent returns {-2, 0}
// without changing the set, and the caller rehydrates first.
//
// The leaked arm adds leaked:<sessionId> whether or not SREM removed a
// member. That keeps a leak counted when it is reported after a clean
// release of the same session already removed the member; gating the add on
// the removal would lose the leak and fail open. A second leaked release
// changes nothing, because SADD of a present member is a no-op.
//
// KEYS[1] is the active-slots set, KEYS[2] the rehydrated flag. ARGV[1] is
// the session identifier and ARGV[2] '1' for a leaked release, '0'
// otherwise.
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics).
var releaseScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then return {-2, 0} end
local r = redis.call('SREM', KEYS[1], ARGV[1])
if ARGV[2] == '1' then redis.call('SADD', KEYS[1], 'leaked:' .. ARGV[1]) end
return {r, redis.call('SCARD', KEYS[1])}
`)

// LeakedMember returns the set member that keeps a leaked slot of
// sessionID counted (§6.2 leaked slot semantics).
func LeakedMember(sessionID string) string {
	return "leaked:" + sessionID
}

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

// podKey returns the per-pod key with the given suffix. The pod identifier
// (the §4.6.1 Sandbox name) is wrapped in braces, so Redis Cluster hashes
// only the pod identifier and every key of one pod lands in one hash slot.
// spec: §12.4 (lenny:pod:{pod_id}:* keys).
func podKey(podID, suffix string) string {
	return "lenny:pod:{" + podID + "}:" + suffix
}

// activeSlotsKey returns the key of a pod's slot set.
func activeSlotsKey(podID string) string { return podKey(podID, "active_slots") }

// rehydratedKey returns the §12.4 per-pod rehydrated-flag key. Its presence
// marks the set as seeded for the current Redis instance; it has no TTL.
func rehydratedKey(podID string) string { return podKey(podID, "rehydrated") }

// rehydratingKey returns the §12.4 per-pod rehydrating-lock key, a
// short-lived SET NX lock that serializes rehydration across replicas.
func rehydratingKey(podID string) string { return podKey(podID, "rehydrating") }

// errEmptySessionID reports a reservation or release without a session
// identifier. A keyed release with no key would remove nothing and report a
// false zero to a caller that ignores removed, so it runs no script.
var errEmptySessionID = errors.New("slotcounter: session identifier is required")

// Reserve atomically reserves a slot for sessionID on podID, respecting the
// maxConcurrent cap. It returns the set's cardinality after the reservation,
// or ErrSlotsExhausted if the pod is at cap. A session whose member is
// already present is not added again and gets the current cardinality.
//
// The rehydrated bool reports whether this call performed a §5.2
// post-recovery rehydration (a Postgres seed by this replica). The caller
// uses it to emit lenny_slot_rehydration_total exactly once per rehydration
// event; a peer-served rehydration that this call merely waited for reports
// false.
//
// While an outage is being recovered (recovery.go), Reserve runs the
// recovery rebuild before its script. A rebuild that fails because Redis is
// unreachable takes the §12.4 fallback; one that fails for any other reason
// fails closed.
//
// spec: §5.2 (Slot assignment atomicity, Post-recovery rehydration
// atomicity), §12.4 (Redis failure behavior).
func (c *Counter) Reserve(ctx context.Context, podID, sessionID string, maxConcurrent int32) (count int32, rehydrated bool, err error) {
	if sessionID == "" {
		return 0, false, fmt.Errorf("slotcounter: reserve %s: %w", podID, errEmptySessionID)
	}
	if maxConcurrent < 1 {
		return 0, false, fmt.Errorf("slotcounter: maxConcurrent must be >= 1, got %d", maxConcurrent)
	}
	if c.needsRebuild() {
		if rbErr := c.rebuildAfterOutage(ctx); rbErr != nil {
			if isRedisUnavailable(rbErr) {
				return c.fallbackReserve(ctx, podID, sessionID, maxConcurrent, false)
			}
			return 0, false, fmt.Errorf("slotcounter: reserve %s: recovery rebuild: %w: %w", podID, ErrFailClosed, rbErr)
		}
	}
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID)}
	for attempt := 0; attempt < maxReserveAttempts; attempt++ {
		res, runErr := reserveScript.Run(ctx, c.client, keys, sessionID, maxConcurrent).Int64()
		if runErr != nil {
			if isRedisUnavailable(runErr) {
				// spec: §12.4 — Redis is unreachable. Gate capacity
				// on the Postgres fallback under a per-pod advisory lock,
				// failing closed only after the bounded outage window.
				return c.fallbackReserve(ctx, podID, sessionID, maxConcurrent, rehydrated)
			}
			return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: %w", podID, runErr)
		}
		switch {
		case res >= 1:
			return int32(res), rehydrated, nil
		case res == resultExhausted:
			return 0, rehydrated, ErrSlotsExhausted
		case res == resultRehydrate:
			did, rErr := c.rehydrate(ctx, podID, "")
			if rErr != nil {
				if isRedisUnavailable(rErr) {
					return c.fallbackReserve(ctx, podID, sessionID, maxConcurrent, rehydrated)
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

// rehydrate seeds podID's slot set from the SlotSource and sets the
// rehydrated flag, per §5.2 "Post-recovery rehydration atomicity". extra,
// when non-empty, is added to the seed: a release passes its own session so
// the member it is about to remove is present whether or not the session's
// row still names the pod. It returns true only when this replica performed
// the Postgres-backed seed (the lenny_slot_rehydration_total event); a call
// that found the flag already set, or that waited on a peer's lock, returns
// false.
//
// Coordination is two-layer (§5.2): a per-pod in-process mutex serializes
// rehydration within this replica, and a short-lived Redis SET NX lock on
// lenny:pod:{pod}:rehydrating serializes across replicas. A replica that
// cannot take the cross-replica lock spin-waits (bounded by
// rehydrationTimeout) for the peer to set the flag, then returns so the
// caller can retry its script.
func (c *Counter) rehydrate(ctx context.Context, podID, extra string) (bool, error) {
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
		// No Postgres-backed source: seed only the caller's own member and
		// set the flag. Not counted as a rehydration event, because no
		// Postgres state was consulted.
		return false, c.seed(ctx, podID, nil, extra)
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
		// then let the caller retry its script.
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

	ids, err := c.source.GetActiveSlotsByPod(ctx, podID)
	if err != nil {
		return false, fmt.Errorf("slotcounter: rehydrate %s from source: %w", podID, err)
	}
	if err := c.seed(ctx, podID, ids, extra); err != nil {
		return false, err
	}
	return true, nil
}

// seed adds ids, and extra when non-empty, to podID's set and sets the
// rehydrated flag in one script. It removes no member.
func (c *Counter) seed(ctx context.Context, podID string, ids []string, extra string) error {
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		if id != "" {
			args = append(args, id)
		}
	}
	if extra != "" {
		args = append(args, extra)
	}
	if err := seedScript.Run(ctx, c.client,
		[]string{activeSlotsKey(podID), rehydratedKey(podID)}, args...).Err(); err != nil {
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

// Release removes sessionID's member from podID's slot set (§5.2 keyed
// release). It returns whether this call removed the member, the set's
// remaining cardinality, and whether this call performed a rehydration.
//
// A clean release that finds no member changes nothing and reports removed
// false, so a duplicate release is a no-op. A leaked release removes the
// member if present and adds leaked:<sessionId>, so the slot stays counted
// (§6.2). The caller runs the occupancy-zero disposition only when the
// release is clean, removed is true, and remaining is zero.
//
// A release that finds the rehydrated flag absent rehydrates the set first,
// adding its own member to the seed, and then removes it, so it reports
// removed and rehydrated. While an outage is being recovered, Release runs
// the recovery rebuild before its script and returns the rebuild's error.
// A leaked release that fails for any reason records a leaked mark in the
// outage ledger, and a release refused because Redis is unreachable stamps
// the outage clock (recovery.go).
//
// spec: §5.2 (Slot assignment atomicity, Post-recovery rehydration
// atomicity), §6.2 (leaked slot semantics), §12.4 (Redis failure behavior).
func (c *Counter) Release(ctx context.Context, podID, sessionID string, leaked bool) (removed bool, remaining int32, rehydrated bool, err error) {
	if sessionID == "" {
		return false, 0, false, fmt.Errorf("slotcounter: release %s: %w", podID, errEmptySessionID)
	}
	removed, remaining, rehydrated, err = c.release(ctx, podID, sessionID, leaked)
	if err != nil {
		if leaked {
			c.record(entryLeaked, podID, sessionID)
		}
		if isRedisUnavailable(err) {
			c.stampOutage()
		}
		return false, 0, rehydrated, fmt.Errorf("slotcounter: release %s session %s: %w", podID, sessionID, err)
	}
	return removed, remaining, rehydrated, nil
}

// release runs the rebuild when one is due, then the keyed release script,
// rehydrating once per absent flag under the reservation's attempt bound.
func (c *Counter) release(ctx context.Context, podID, sessionID string, leaked bool) (removed bool, remaining int32, rehydrated bool, err error) {
	if c.needsRebuild() {
		if err := c.rebuildAfterOutage(ctx); err != nil {
			return false, 0, false, fmt.Errorf("recovery rebuild: %w", err)
		}
	}
	leakedArg := "0"
	if leaked {
		leakedArg = "1"
	}
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID)}
	for attempt := 0; attempt < maxReserveAttempts; attempt++ {
		res, runErr := releaseScript.Run(ctx, c.client, keys, sessionID, leakedArg).Int64Slice()
		if runErr != nil {
			return false, 0, rehydrated, runErr
		}
		if len(res) != 2 {
			return false, 0, rehydrated, fmt.Errorf("unexpected script result %v", res)
		}
		if res[0] != resultRehydrate {
			return res[0] == 1, int32(res[1]), rehydrated, nil
		}
		did, rErr := c.rehydrate(ctx, podID, sessionID)
		if rErr != nil {
			return false, 0, rehydrated, rErr
		}
		rehydrated = rehydrated || did
	}
	return false, 0, rehydrated, ErrRehydrationStalled
}

// Reset removes the slot set and the rehydration sentinels for podID. The
// three keys share the pod's hash tag, so the DEL stays in one Redis Cluster
// slot. Without the reset, a fresh pod that gets the same ID would inherit
// the old set or rehydration state.
func (c *Counter) Reset(ctx context.Context, podID string) error {
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID), rehydratingKey(podID)}
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("slotcounter: reset %s: %w", podID, err)
	}
	return nil
}

// reapScript removes one plain member of a pod's slot set for the
// stale-slot reaper. Unlike releaseScript it never rehydrates and never
// empties the set: a pod's last member is kept, because the reaper runs no
// occupancy-zero disposition and the claim-orphan GC owns an idle pod.
// It returns -2 when the rehydrated flag is absent, 0 for a non-member, -3
// for the pod's last member, and 1 when it removed the member.
//
// KEYS[1] is the active-slots set, KEYS[2] the rehydrated flag. ARGV[1] is
// the session identifier.
// spec: §5.2 (Slot assignment atomicity).
var reapScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then return -2 end
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 0 then return 0 end
if redis.call('SCARD', KEYS[1]) <= 1 then return -3 end
redis.call('SREM', KEYS[1], ARGV[1])
return 1
`)

// errLeakedMember reports a reap naming a leaked: member. A leaked member
// keeps a slot whose teardown did not complete counted (§6.2), and only the
// pod's drain clears it, so the reaper never removes one.
var errLeakedMember = errors.New("slotcounter: a leaked member is not reaped")

// Members returns every member of podID's slot set, leaked: members
// included, with one SMEMBERS on the pod's hash-tagged active-slots key.
// It runs no rehydration and no outage rebuild.
// spec: §5.2 (Slot assignment atomicity).
func (c *Counter) Members(ctx context.Context, podID string) ([]string, error) {
	members, err := c.client.SMembers(ctx, activeSlotsKey(podID)).Result()
	if err != nil {
		return nil, fmt.Errorf("slotcounter: members %s: %w", podID, err)
	}
	return members, nil
}

// Reap removes sessionID's plain member from podID's slot set for the
// stale-slot reaper, under the removal conditions reapScript states. It
// reports removed true only when the script removed the member. It runs no
// rehydration, no outage rebuild, and no fallback, and records no
// outage-ledger entry: a pod whose flag is absent keeps its member until a
// reservation or release restores the flag. An empty identifier, or one
// naming a leaked: member, is an error and runs no script.
// spec: §5.2 (Slot assignment atomicity).
func (c *Counter) Reap(ctx context.Context, podID, sessionID string) (removed bool, err error) {
	if sessionID == "" {
		return false, fmt.Errorf("slotcounter: reap %s: %w", podID, errEmptySessionID)
	}
	if strings.HasPrefix(sessionID, LeakedMember("")) {
		return false, fmt.Errorf("slotcounter: reap %s member %s: %w", podID, sessionID, errLeakedMember)
	}
	keys := []string{activeSlotsKey(podID), rehydratedKey(podID)}
	res, err := reapScript.Run(ctx, c.client, keys, sessionID).Int64()
	if err != nil {
		return false, fmt.Errorf("slotcounter: reap %s session %s: %w", podID, sessionID, err)
	}
	return res == 1, nil
}
