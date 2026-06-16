// SPDX-License-Identifier: MIT

package derivelock_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
)

// spec: §7.1 line 92 — concurrent derives on the same source session
// serialize. The first acquirer wins; the second blocks until the
// first releases.
func TestMemory_SerializesSameSource_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(2 * time.Second)
	ctx := context.Background()

	rel1, err := m.Acquire(ctx, "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	var (
		wg          sync.WaitGroup
		secondAcked int32
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rel2, err := m.Acquire(ctx, "sess-A")
		if err != nil {
			t.Errorf("second acquire: %v", err)
			return
		}
		defer rel2()
		atomic.StoreInt32(&secondAcked, 1)
	}()

	// Give the second goroutine a chance to attempt and block.
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&secondAcked) != 0 {
		t.Fatalf("second acquire admitted while first still held the lock")
	}

	rel1()
	wg.Wait()
	if atomic.LoadInt32(&secondAcked) != 1 {
		t.Fatalf("second acquire never completed after release")
	}
}

// spec: §7.1 line 92 — distinct source sessions do not serialize. Two
// derives on different sources MUST both acquire concurrently.
func TestMemory_DistinctSourcesProceedConcurrently_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(time.Second)
	ctx := context.Background()

	rel1, err := m.Acquire(ctx, "sess-A")
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer rel1()

	rel2, err := m.Acquire(ctx, "sess-B")
	if err != nil {
		t.Fatalf("acquire B: %v", err)
	}
	defer rel2()
}

// spec: §7.1 line 92 — caller that does not acquire within the wait
// budget receives ErrContended, which the session-server maps to 429
// DERIVE_LOCK_CONTENTION.
func TestMemory_ReturnsErrContendedAfterWait_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(50 * time.Millisecond)
	ctx := context.Background()

	rel1, err := m.Acquire(ctx, "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel1()

	start := time.Now()
	rel2, err := m.Acquire(ctx, "sess-A")
	if err == nil {
		rel2()
		t.Fatalf("second acquire admitted; want ErrContended")
	}
	if !errors.Is(err, derivelock.ErrContended) {
		t.Fatalf("err = %v, want ErrContended", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("wait elapsed = %v, want ≥40ms (wait-budget honored)", elapsed)
	}
}

// spec: §7.1 line 92 — ctx cancellation short-circuits the wait so a
// caller dropping the request does not hold a goroutine until the
// budget elapses.
func TestMemory_ContextCancellationShortCircuits_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(5 * time.Second)
	first, err := m.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rel, err := m.Acquire(ctx, "sess-A")
	if err == nil {
		rel()
		t.Fatalf("acquire returned no error on cancelled ctx")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// spec: §7.1 line 92 — Release is idempotent. A second invocation does
// not double-unlock the per-source mutex (which would panic).
func TestMemory_ReleaseIdempotent_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(time.Second)
	rel, err := m.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rel()
	rel() // must not panic on double-release.

	// A fresh acquirer can take the lock immediately after release.
	rel2, err := m.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	rel2()
}

// spec: §7.1 line 92 — concurrent acquire/release churn on a single
// source session never admits two simultaneous holders. The Memory
// implementation reclaims its per-source map entry only when the last
// referencing goroutine leaves; a release that dropped the entry while a
// waiter was mid-acquire would let the next acquirer mint a second mutex
// for the same source and run a derive in parallel with the waiter. This
// test drives that GC race directly with a large concurrent fan-in and a
// shared holder counter, so a regression in the reference-counted
// reclamation surfaces as maxConcurrent > 1.
func TestMemory_ConcurrentChurnNeverDoubleAdmits_spec_7_1_92(t *testing.T) {
	m := derivelock.NewMemory(2 * time.Second)

	var (
		held          atomic.Int32
		maxConcurrent atomic.Int32
		wg            sync.WaitGroup
	)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				rel, err := m.Acquire(context.Background(), "sess-A")
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				cur := held.Add(1)
				for {
					prev := maxConcurrent.Load()
					if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
						break
					}
				}
				held.Add(-1)
				rel()
			}
		}()
	}
	wg.Wait()

	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("maxConcurrent holders = %d, want ≤1 (per-source lock failed to serialize under churn)", got)
	}
}

// spec: §7.1 line 92 — Redis-backed lock serializes across replicas.
// We simulate two replicas by issuing two Acquire calls against the
// same Redis instance; the second must time out and return ErrContended.
func TestRedis_SerializesSameSourceAcrossReplicas_spec_7_1_92(t *testing.T) {
	mr, client := newMiniRedis(t)
	defer mr.Close()

	first := derivelock.NewRedis(client,
		derivelock.WithWait(time.Second),
		derivelock.WithKeyPrefix("test:"))
	second := derivelock.NewRedis(client,
		derivelock.WithWait(50*time.Millisecond),
		derivelock.WithKeyPrefix("test:"))

	rel, err := first.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	_, err = second.Acquire(context.Background(), "sess-A")
	if !errors.Is(err, derivelock.ErrContended) {
		t.Fatalf("second acquire err = %v, want ErrContended", err)
	}
}

// spec: §7.1 line 92 — releasing the Redis lock allows the next caller
// to acquire immediately, without waiting for the TTL.
func TestRedis_ReleaseClearsLock_spec_7_1_92(t *testing.T) {
	mr, client := newMiniRedis(t)
	defer mr.Close()

	r := derivelock.NewRedis(client,
		derivelock.WithWait(time.Second),
		derivelock.WithKeyPrefix("test:"))

	rel, err := r.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel()
	// Allow the release goroutine to run.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !mr.Exists("test:sess-A") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	rel2, err := r.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	rel2()
}

// spec: §7.1 line 92 — the release script only deletes the lock when
// the stored token matches. A stale releaser whose lock has already
// expired and been re-acquired by a peer MUST NOT delete the peer's
// lock.
func TestRedis_ReleaseTokenFencesAgainstExpiredHolder_spec_7_1_92(t *testing.T) {
	mr, client := newMiniRedis(t)
	defer mr.Close()

	r := derivelock.NewRedis(client,
		derivelock.WithWait(time.Second),
		derivelock.WithTTL(50*time.Millisecond),
		derivelock.WithKeyPrefix("test:"))

	rel1, err := r.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Let the lock TTL expire (miniredis FastForward advances the
	// in-memory clock so the SETNX entry is treated as evicted).
	mr.FastForward(200 * time.Millisecond)

	// A peer acquires the now-free lock.
	rel2, err := r.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("peer acquire after TTL: %v", err)
	}

	// The stale holder runs its release. Because the token does not
	// match the peer's, the peer's lock stays held.
	rel1()
	time.Sleep(100 * time.Millisecond)
	if !mr.Exists("test:sess-A") {
		t.Fatalf("peer's lock was deleted by stale holder's release")
	}
	rel2()
}

// NoLock returns a releaser that admits every acquire without
// serializing. Useful for tests that want to bypass the lock.
func TestNoLock_AlwaysAdmits(t *testing.T) {
	lock := derivelock.NoLock()
	rel, err := lock.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rel()

	rel2, err := lock.Acquire(context.Background(), "sess-A")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	rel2()
}

func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}
