// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// fakeFallback is a test FallbackSource that gates intra-pod capacity on a
// per-pod live-slot count and records its calls. It serializes the
// count-and-decide under a per-pod mutex, the in-test analogue of the
// Postgres per-pod advisory lock.
type fakeFallback struct {
	mu        sync.Mutex
	active    map[string]int32 // current occupancy per pod
	calls     map[string]int
	failWith  error // when set, every call returns this error
	podLockMu sync.Mutex
	podLocks  map[string]*sync.Mutex
}

func newFakeFallback() *fakeFallback {
	return &fakeFallback{active: map[string]int32{}, calls: map[string]int{}, podLocks: map[string]*sync.Mutex{}}
}

func (f *fakeFallback) podLock(podID string) *sync.Mutex {
	f.podLockMu.Lock()
	defer f.podLockMu.Unlock()
	l, ok := f.podLocks[podID]
	if !ok {
		l = &sync.Mutex{}
		f.podLocks[podID] = l
	}
	return l
}

func (f *fakeFallback) ReserveSlotUnderLock(_ context.Context, podID string, maxConcurrent int32) (int32, bool, error) {
	lock := f.podLock(podID)
	lock.Lock()
	defer lock.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[podID]++
	if f.failWith != nil {
		return 0, false, f.failWith
	}
	cur := f.active[podID]
	if cur >= maxConcurrent {
		return cur, false, nil
	}
	f.active[podID] = cur + 1
	return cur + 1, true, nil
}

func (f *fakeFallback) callCount(podID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[podID]
}

// downedCounter returns a Counter whose Redis client points at a closed
// miniredis, so every Reserve observes Redis as unreachable and routes to
// the §12.4 fallback. now is the injectable clock for the outage window.
func downedCounter(t *testing.T, opts ...slotcounter.Option) *slotcounter.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	mr.Close() // simulate a Redis outage: the client can no longer connect.
	return slotcounter.New(cl, opts...)
}

// spec: §12.4 line 208 — during a Redis outage the counter gates intra-pod
// capacity on the Postgres fallback under a per-pod advisory lock.
// diagnosis: a Redis outage rejected all slot admission instead of
// degrading to the Postgres-fallback gate.
func TestReserveFallsBackToPostgresOnRedisOutage(t *testing.T) {
	fb := newFakeFallback()
	c := downedCounter(t, slotcounter.WithFallbackSource(fb))

	count, _, err := c.Reserve(context.Background(), "pod-1", 4)
	if err != nil {
		t.Fatalf("Reserve under Redis outage: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (first slot admitted via the Postgres fallback)", count)
	}
	if fb.callCount("pod-1") != 1 {
		t.Errorf("fallback calls = %d, want 1", fb.callCount("pod-1"))
	}
}

// spec: §12.4 line 208 — the fallback caps at maxConcurrent.
// diagnosis: the Postgres-fallback gate admitted a slot past the per-pod
// bound during a Redis outage.
func TestReserveFallbackCapsAtBound(t *testing.T) {
	fb := newFakeFallback()
	fb.active["pod-1"] = 2 // already at the cap of 2.
	c := downedCounter(t, slotcounter.WithFallbackSource(fb))

	_, _, err := c.Reserve(context.Background(), "pod-1", 2)
	if !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("error = %v, want ErrSlotsExhausted when the pod is at its bound in the fallback gate", err)
	}
}

// spec: §12.4 line 208 — no Postgres fallback wired fails closed
// immediately.
// diagnosis: a Redis outage with no fallback silently admitted slots
// rather than failing closed.
func TestReserveFailsClosedWithoutFallback(t *testing.T) {
	c := downedCounter(t) // no WithFallbackSource.
	_, _, err := c.Reserve(context.Background(), "pod-1", 4)
	if !errors.Is(err, slotcounter.ErrFailClosed) {
		t.Errorf("error = %v, want ErrFailClosed when no Postgres fallback is wired", err)
	}
}

// spec: §12.4 line 208 — dual-store outage (Postgres also unavailable)
// fails closed.
// diagnosis: when both Redis and Postgres are unavailable the gate admitted
// a slot instead of failing closed.
func TestReserveFailsClosedOnDualStoreOutage(t *testing.T) {
	fb := newFakeFallback()
	fb.failWith = errors.New("postgres unavailable")
	c := downedCounter(t, slotcounter.WithFallbackSource(fb))

	_, _, err := c.Reserve(context.Background(), "pod-1", 4)
	if !errors.Is(err, slotcounter.ErrFailClosed) {
		t.Errorf("error = %v, want ErrFailClosed on a dual-store outage", err)
	}
}

// spec: §12.4 line 208 — the fallback window is bounded; after
// slotCounterPostgresFallbackMaxSeconds with Redis still down the gate
// fails closed.
// diagnosis: a sustained Redis outage kept degrading to Postgres latency
// indefinitely instead of failing closed after the bounded window.
func TestReserveFailsClosedAfterOutageWindow(t *testing.T) {
	fb := newFakeFallback()
	now := time.Now()
	clock := &fakeClock{t: now}
	c := downedCounter(
		t,
		slotcounter.WithFallbackSource(fb),
		slotcounter.WithFallbackMaxWindow(60*time.Second),
		slotcounter.WithClockForTest(clock.Now),
	)

	// First reservation during the outage stamps the window start and is
	// admitted.
	if _, _, err := c.Reserve(context.Background(), "pod-1", 4); err != nil {
		t.Fatalf("Reserve at outage start: %v", err)
	}
	// Advance past the bounded window: the next reservation fails closed.
	clock.advance(61 * time.Second)
	_, _, err := c.Reserve(context.Background(), "pod-1", 4)
	if !errors.Is(err, slotcounter.ErrFailClosed) {
		t.Errorf("error = %v, want ErrFailClosed after the bounded outage window", err)
	}
}

// fakeClock is a test wall clock the §12.4 outage-window measurement uses.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
