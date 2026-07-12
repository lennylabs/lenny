// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
)

func newStore(t *testing.T) (*redisstore.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return redisstore.New(cl), mr
}

func acquire(t *testing.T, s *redisstore.Store, scope, by string, ttl int) *coordination.Lock {
	t.Helper()
	lock, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: scope, Operation: "scale", AcquiredBy: by, TTLSeconds: ttl,
	}, 0)
	if err != nil {
		t.Fatalf("acquire %s: %v", scope, err)
	}
	return lock
}

// spec: §25.4 lines 2166, 2195-2202 — the Tier 2 acquire is a compare-and-
// set on scope; a held scope yields REMEDIATION_LOCK_CONFLICT.
func TestRedisAcquireAndConflict_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	if s.Tier() != coordination.StoreRedis {
		t.Fatalf("tier = %q, want redis", s.Tier())
	}
	lock := acquire(t, s, "pool:p", "alice", 300)
	if lock.LockStore != coordination.StoreRedis {
		t.Errorf("lockStore = %q, want redis", lock.LockStore)
	}
	_, err := s.Acquire(context.Background(), coordination.LockRequest{Scope: "pool:p", AcquiredBy: "bob"}, 0)
	if coordination.CodeOf(err) != coordination.ErrCodeConflict {
		t.Fatalf("second acquire err = %v, want REMEDIATION_LOCK_CONFLICT", err)
	}
}

// spec: §25.4 lines 2103, 2131 — Get by id and List by scope.
func TestRedisGetAndList_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	a := acquire(t, s, "pool:b", "alice", 300)
	acquire(t, s, "pool:a", "bob", 300)

	got, err := s.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Scope != "pool:b" || got.AcquiredBy != "alice" {
		t.Errorf("get = %+v, want pool:b/alice", got)
	}
	if _, err := s.Get(context.Background(), "lock-missing"); coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Errorf("get missing = %v, want NOT_FOUND", err)
	}
	locks, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(locks) != 2 || locks[0].Scope != "pool:a" || locks[1].Scope != "pool:b" {
		t.Errorf("list = %+v, want pool:a,pool:b ordered", locks)
	}
}

// spec: §25.4 lines 2129, 2303-2306 — identity-based Release and Extend:
// the caller must equal acquiredBy.
func TestRedisReleaseExtendIdentity_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	lock := acquire(t, s, "pool:p", "alice", 300)

	if err := s.Release(context.Background(), lock.ID, "bob"); coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Errorf("release by non-owner = %v, want LOCK_NOT_OWNED", err)
	}
	ext, err := s.Extend(context.Background(), lock.ID, 600, "alice")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if ext.Revision != 1 {
		t.Errorf("revision after extend = %d, want 1", ext.Revision)
	}
	if err := s.Release(context.Background(), lock.ID, "alice"); err != nil {
		t.Fatalf("release by owner: %v", err)
	}
	if _, err := s.Get(context.Background(), lock.ID); coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Errorf("get after release = %v, want NOT_FOUND", err)
	}
}

// spec: §25.4 line 2267 — RemoveByID drops a losing split-brain Redis lock
// regardless of holder (the reconciliation removes it once the pre-outage
// Postgres holder has won); a missing id is a no-op.
func TestRedisRemoveByID_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lock := acquire(t, s, "pool:p", "bob", 300)

	// Removing an unknown id is a no-op and leaves the live lock intact.
	if err := s.RemoveByID(ctx, "lock-unknown"); err != nil {
		t.Fatalf("remove unknown id: %v", err)
	}
	if _, err := s.Get(ctx, lock.ID); err != nil {
		t.Fatalf("live lock disturbed by unrelated remove: %v", err)
	}

	// Removing the lock by id drops it regardless of holder identity.
	if err := s.RemoveByID(ctx, lock.ID); err != nil {
		t.Fatalf("remove by id: %v", err)
	}
	if _, err := s.Get(ctx, lock.ID); coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Errorf("get after remove = %v, want NOT_FOUND", err)
	}
	// The scope is free again.
	if again := acquire(t, s, "pool:p", "carol", 300); again.AcquiredBy != "carol" {
		t.Errorf("re-acquire after remove = %+v, want carol", again)
	}
}

// spec: §25.4 lines 2282-2295 — Steal records the prior holder and bumps
// the revision.
func TestRedisSteal_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	lock := acquire(t, s, "pool:p", "alice", 300)
	stolen, err := s.Steal(context.Background(), lock.ID, coordination.StealRequest{Confirm: true, AcquiredBy: "bob", TTLSeconds: 300})
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if stolen.AcquiredBy != "bob" || stolen.StolenFrom != "alice" {
		t.Errorf("steal = %+v, want acquiredBy=bob stolenFrom=alice", stolen)
	}
	if stolen.Revision != 1 {
		t.Errorf("revision = %d, want 1", stolen.Revision)
	}
}

// spec: §25.4 lines 2198, 2218-2222 — the outage epoch counter:
// increment advances it, SetEpoch applies the MAX (never backward), and a
// Tier 2 acquire stamps the current value.
func TestRedisEpochAndStamp_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if e, _ := s.Epoch(ctx); e != 0 {
		t.Fatalf("initial epoch = %d, want 0", e)
	}
	if e, err := s.IncrementEpoch(ctx); err != nil || e != 1 {
		t.Fatalf("increment = %d (%v), want 1", e, err)
	}
	if err := s.SetEpoch(ctx, 5); err != nil {
		t.Fatalf("set epoch 5: %v", err)
	}
	if err := s.SetEpoch(ctx, 3); err != nil { // lower: ignored
		t.Fatalf("set epoch 3: %v", err)
	}
	if e, _ := s.Epoch(ctx); e != 5 {
		t.Errorf("epoch = %d, want 5 (MAX, never backward)", e)
	}
	lock := acquire(t, s, "pool:p", "alice", 300)
	if lock.Epoch != 5 {
		t.Errorf("acquired lock epoch = %d, want 5 (stamped from ops:lock-epoch:current)", lock.Epoch)
	}
}

// spec: §25.4 lines 2200-2202 — the key-level PTTL expires the lock
// natively: after the TTL elapses the scope is free again.
func TestRedisTTLExpiry_spec_25_4(t *testing.T) {
	s, mr := newStore(t)
	acquire(t, s, "pool:p", "alice", 5)
	mr.FastForward(6 * time.Second)
	if locks, err := s.List(context.Background()); err != nil || len(locks) != 0 {
		t.Fatalf("list after expiry = %v (%v), want empty", locks, err)
	}
	// The scope is free for a fresh acquire.
	if _, err := s.Acquire(context.Background(), coordination.LockRequest{Scope: "pool:p", AcquiredBy: "bob"}, 0); err != nil {
		t.Fatalf("re-acquire after expiry: %v", err)
	}
}

// spec: §25.4 line 2231 — ActiveLocks is the reconciliation snapshot
// source.
func TestRedisActiveLocks_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	acquire(t, s, "pool:p", "alice", 300)
	locks, err := s.ActiveLocks(context.Background())
	if err != nil || len(locks) != 1 {
		t.Fatalf("active locks = %v (%v), want 1", locks, err)
	}
}

// TestRedisServerTime_spec_25_4 asserts the exported ServerTime reads the
// Redis TIME clock the Postgres-Redis clock-skew sampler compares against
// Postgres now(). It is the same source acquiredAt/expiresAt are authored
// from, so the monitored skew is the skew the lease path is exposed to.
//
// spec: §25.4 line 2202 (Redis TIME clock source), line 2280 (skew
// monitoring).
func TestRedisServerTime_spec_25_4(t *testing.T) {
	s, mr := newStore(t)
	want := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	mr.SetTime(want)
	got, err := s.ServerTime(context.Background())
	if err != nil {
		t.Fatalf("ServerTime: %v", err)
	}
	// miniredis TIME has second granularity; assert the read reflects the
	// injected clock to the second.
	if got.Unix() != want.Unix() {
		t.Errorf("ServerTime = %v (unix %d), want %v (unix %d)", got, got.Unix(), want, want.Unix())
	}
	if got.Location() != time.UTC {
		t.Errorf("ServerTime location = %v, want UTC", got.Location())
	}
}
