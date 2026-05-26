// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
)

func newCounter(t *testing.T) *slotcounter.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return slotcounter.New(cl)
}

// fakeSource is a test SlotSource that records per-pod call counts and
// returns a configured active-slot count (or a configured error).
type fakeSource struct {
	mu     sync.Mutex
	counts map[string]int
	calls  map[string]int
	err    error
}

func newFakeSource() *fakeSource {
	return &fakeSource{counts: map[string]int{}, calls: map[string]int{}}
}

func (f *fakeSource) GetActiveSlotsByPod(_ context.Context, podID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[podID]++
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[podID], nil
}

func (f *fakeSource) callCount(podID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[podID]
}

// newCounterWithSource returns a Counter wired to src and the backing
// miniredis so a test can simulate a Redis restart.
func newCounterWithSource(t *testing.T, src slotcounter.SlotSource, opts ...slotcounter.Option) (*slotcounter.Counter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	all := append([]slotcounter.Option{slotcounter.WithSlotSource(src)}, opts...)
	return slotcounter.New(cl, all...), mr, cl
}

// spec: §5.2 — Reserve atomically increments the slot count, returning
// the new count, and rejects past maxConcurrent.
func TestReserveIncrementsUntilCap(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	for i := int32(1); i <= 3; i++ {
		got, _, err := c.Reserve(ctx, "pod-a", 3)
		if err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
		if got != i {
			t.Errorf("Reserve %d returned count %d, want %d", i, got, i)
		}
	}
	// 4th reserve must fail.
	if _, _, err := c.Reserve(ctx, "pod-a", 3); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("4th Reserve = %v, want ErrSlotsExhausted", err)
	}
}

// spec: §5.2 — atomic CAS prevents over-commit under racing reservers.
// 50 goroutines try to reserve on a pod whose maxConcurrent is 10; only
// 10 must succeed.
func TestReserveIsAtomicUnderRace(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	const n = 50
	const cap = int32(10)
	var wg sync.WaitGroup
	successes := int64(0)
	exhausted := int64(0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.Reserve(ctx, "pod-r", cap); err == nil {
				atomic.AddInt64(&successes, 1)
			} else if errors.Is(err, slotcounter.ErrSlotsExhausted) {
				atomic.AddInt64(&exhausted, 1)
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&successes) != int64(cap) {
		t.Errorf("got %d successful reserves, want exactly %d (cap)", successes, cap)
	}
	if atomic.LoadInt64(&exhausted) != int64(n-int(cap)) {
		t.Errorf("got %d exhausted, want %d", exhausted, n-int(cap))
	}
}

// spec: §5.2 — Release decrements; a release on a zero counter clamps
// at zero (double-release-safe).
func TestReleaseDecrementsAndClampsAtZero(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-d", 5); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	got, err := c.Release(ctx, "pod-d")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got != 0 {
		t.Errorf("Release returned %d, want 0", got)
	}
	// Double-release.
	got, err = c.Release(ctx, "pod-d")
	if err != nil {
		t.Fatalf("double Release: %v", err)
	}
	if got != 0 {
		t.Errorf("double Release returned %d, want 0 (clamped)", got)
	}
}

// Different pods have independent counters.
func TestReserveScopesPerPod(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-x", 1); err != nil {
		t.Fatalf("Reserve pod-x: %v", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-x", 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("second Reserve on pod-x = %v, want ErrSlotsExhausted", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-y", 1); err != nil {
		t.Errorf("Reserve pod-y must succeed independently of pod-x: %v", err)
	}
}

func TestResetClearsCounter(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-z", 2); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := c.Reset(ctx, "pod-z"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	// A fresh Reserve after Reset starts from zero.
	got, _, err := c.Reserve(ctx, "pod-z", 2)
	if err != nil {
		t.Fatalf("Reserve after Reset: %v", err)
	}
	if got != 1 {
		t.Errorf("Reserve after Reset returned %d, want 1", got)
	}
}

// spec: §5.2 line 521 — the first reservation on a pod whose rehydrated
// flag is absent seeds the counter from the SlotSource before
// incrementing. A source reporting 2 live slots makes the first
// reservation return 3 (seed 2 + INCR).
func TestReserveRehydratesFromSource_spec_5_2_521(t *testing.T) {
	src := newFakeSource()
	src.counts["pod-h"] = 2
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()

	got, rehydrated, err := c.Reserve(ctx, "pod-h", 5)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got != 3 {
		t.Errorf("first Reserve after rehydration returned %d, want 3 (seed 2 + INCR)", got)
	}
	if !rehydrated {
		t.Error("first Reserve must report rehydrated=true")
	}
	if n := src.callCount("pod-h"); n != 1 {
		t.Errorf("source queried %d times, want 1", n)
	}
	// The rehydrated flag persists (no TTL).
	if n, _ := cl.Exists(ctx, "lenny:pod:pod-h:rehydrated").Result(); n != 1 {
		t.Error("rehydrated flag must be set after rehydration")
	}

	// A second reservation does not re-query the source and reports
	// rehydrated=false.
	got, rehydrated, err = c.Reserve(ctx, "pod-h", 5)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if got != 4 {
		t.Errorf("second Reserve returned %d, want 4", got)
	}
	if rehydrated {
		t.Error("second Reserve must report rehydrated=false")
	}
	if n := src.callCount("pod-h"); n != 1 {
		t.Errorf("source queried %d times after second reserve, want 1", n)
	}
}

// spec: §5.2 line 521 — when Postgres already shows the pod at its
// maxConcurrent bound, rehydration seeds the counter to the cap and the
// reservation is rejected (no over-commit), still reporting the
// rehydration event.
func TestReserveRehydratesToCapThenExhausted(t *testing.T) {
	src := newFakeSource()
	src.counts["pod-f"] = 3
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()

	_, reh, err := c.Reserve(ctx, "pod-f", 3)
	if !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("Reserve = %v, want ErrSlotsExhausted (seeded to cap)", err)
	}
	if !reh {
		t.Error("Reserve must report rehydrated=true even when the seed reaches the cap")
	}
}

// spec: §5.2 line 521 — rehydration is per-pod scoped (no global lock):
// each pod triggers its own seed exactly once.
func TestReserveRehydrationIsPerPodScoped(t *testing.T) {
	src := newFakeSource()
	src.counts["pod-1"] = 1
	src.counts["pod-2"] = 0
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()

	if _, reh, err := c.Reserve(ctx, "pod-1", 4); err != nil || !reh {
		t.Fatalf("Reserve pod-1 = (_, %v, %v), want (_, true, nil)", reh, err)
	}
	if _, reh, err := c.Reserve(ctx, "pod-2", 4); err != nil || !reh {
		t.Fatalf("Reserve pod-2 = (_, %v, %v), want (_, true, nil)", reh, err)
	}
	if n := src.callCount("pod-1"); n != 1 {
		t.Errorf("pod-1 source calls = %d, want 1", n)
	}
	if n := src.callCount("pod-2"); n != 1 {
		t.Errorf("pod-2 source calls = %d, want 1", n)
	}
}

// spec: §5.2 line 521 — concurrent reservations on the same un-rehydrated
// pod within one replica must seed exactly once (in-process mutex) and
// must not over-commit. Run under -race.
func TestReserveRehydrationConcurrentSamePod(t *testing.T) {
	src := newFakeSource()
	src.counts["pod-c"] = 0
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()

	const n = 40
	const cap = int32(8)
	var wg sync.WaitGroup
	var successes, exhausted, rehydrations int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reh, err := c.Reserve(ctx, "pod-c", cap)
			if reh {
				atomic.AddInt64(&rehydrations, 1)
			}
			switch {
			case err == nil:
				atomic.AddInt64(&successes, 1)
			case errors.Is(err, slotcounter.ErrSlotsExhausted):
				atomic.AddInt64(&exhausted, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes != int64(cap) {
		t.Errorf("successes = %d, want %d (cap)", successes, cap)
	}
	if exhausted != int64(n)-int64(cap) {
		t.Errorf("exhausted = %d, want %d", exhausted, int64(n)-int64(cap))
	}
	if rehydrations != 1 {
		t.Errorf("rehydration events = %d, want exactly 1", rehydrations)
	}
	if got := src.callCount("pod-c"); got != 1 {
		t.Errorf("source queried %d times, want exactly 1 (in-process mutex)", got)
	}
}

// spec: §5.2 line 521 — two gateway replicas (two Counters) sharing one
// Redis race to rehydrate the same pod. The cross-replica SET NX lock
// ensures the source is queried exactly once cluster-wide. Run under
// -race.
func TestReserveRehydrationCrossReplicaSeedsOnce(t *testing.T) {
	src := newFakeSource()
	src.counts["pod-x"] = 0
	mr := miniredis.RunT(t)
	newReplica := func() *slotcounter.Counter {
		cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = cl.Close() })
		return slotcounter.New(cl, slotcounter.WithSlotSource(src))
	}
	a, b := newReplica(), newReplica()
	ctx := context.Background()

	var wg sync.WaitGroup
	var rehydrations int64
	reserve := func(c *slotcounter.Counter) {
		defer wg.Done()
		_, reh, err := c.Reserve(ctx, "pod-x", 4)
		if err != nil {
			t.Errorf("Reserve: %v", err)
		}
		if reh {
			atomic.AddInt64(&rehydrations, 1)
		}
	}
	wg.Add(2)
	go reserve(a)
	go reserve(b)
	wg.Wait()

	if got := src.callCount("pod-x"); got != 1 {
		t.Errorf("source queried %d times across replicas, want exactly 1 (SET NX lock)", got)
	}
	if rehydrations != 1 {
		t.Errorf("rehydration events = %d, want exactly 1", rehydrations)
	}
}

// spec: §5.2 line 521 — a SlotSource error fails the reservation without
// setting the rehydrated flag, so a later reservation retries the seed
// and succeeds once the source recovers.
func TestReserveRehydrationSourceErrorRetries(t *testing.T) {
	src := newFakeSource()
	src.err = errors.New("postgres unavailable")
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()

	if _, _, err := c.Reserve(ctx, "pod-e", 3); err == nil {
		t.Fatal("Reserve must fail when the source errors")
	}
	// Flag must remain unset so the seed is retried.
	if n, _ := cl.Exists(ctx, "lenny:pod:pod-e:rehydrated").Result(); n != 0 {
		t.Error("rehydrated flag must not be set on source error")
	}

	// Source recovers; the next reservation rehydrates and succeeds.
	src.mu.Lock()
	src.err = nil
	src.counts["pod-e"] = 1
	src.mu.Unlock()

	got, reh, err := c.Reserve(ctx, "pod-e", 3)
	if err != nil {
		t.Fatalf("Reserve after recovery: %v", err)
	}
	if got != 2 {
		t.Errorf("Reserve after recovery returned %d, want 2 (seed 1 + INCR)", got)
	}
	if !reh {
		t.Error("recovered Reserve must report rehydrated=true")
	}
}

// spec: §5.2 line 521 — with no SlotSource wired, rehydration falls back
// to seed-zero (the pre-rehydration behaviour) and is not reported as a
// rehydration event.
func TestReserveNilSourceSeedsZero(t *testing.T) {
	c := newCounter(t) // nil source
	ctx := context.Background()
	got, reh, err := c.Reserve(ctx, "pod-n", 2)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got != 1 {
		t.Errorf("Reserve returned %d, want 1 (seed 0 + INCR)", got)
	}
	if reh {
		t.Error("nil-source reservation must report rehydrated=false")
	}
}

// spec: §5.2 line 521 — after a Redis restart (counters and flags
// cleared) the next reservation re-seeds from Postgres, restoring the
// pre-restart slot count before allowing a new slot.
func TestReserveRehydratesAfterRedisRestart(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src)
	ctx := context.Background()

	// Pre-restart: fill two slots. Source reports 0 (fresh) so the seed
	// is 0 and the count reaches 2 by INCR.
	for i := 0; i < 2; i++ {
		if _, _, err := c.Reserve(ctx, "pod-rr", 4); err != nil {
			t.Fatalf("pre-restart Reserve %d: %v", i, err)
		}
	}

	// Simulate a Redis restart: every key (counter + rehydrated flag) is
	// gone. Postgres still shows the 2 live slots.
	if err := cl.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	src.mu.Lock()
	src.counts["pod-rr"] = 2
	src.calls["pod-rr"] = 0
	src.mu.Unlock()

	got, reh, err := c.Reserve(ctx, "pod-rr", 4)
	if err != nil {
		t.Fatalf("post-restart Reserve: %v", err)
	}
	if got != 3 {
		t.Errorf("post-restart Reserve returned %d, want 3 (rehydrate to 2 + INCR)", got)
	}
	if !reh {
		t.Error("post-restart Reserve must report rehydrated=true")
	}
	if n := src.callCount("pod-rr"); n != 1 {
		t.Errorf("source queried %d times post-restart, want 1", n)
	}
}

// spec: §5.2 line 521 — Reset clears the rehydrated flag so the next
// reservation rehydrates again (a fresh pod with a recycled name does
// not inherit a stale flag).
func TestResetClearsRehydrationFlag(t *testing.T) {
	src := newFakeSource()
	c, _, _ := newCounterWithSource(t, src)
	ctx := context.Background()

	if _, reh, err := c.Reserve(ctx, "pod-rs", 2); err != nil || !reh {
		t.Fatalf("first Reserve = (_, %v, %v), want rehydrated", reh, err)
	}
	if err := c.Reset(ctx, "pod-rs"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, reh, err := c.Reserve(ctx, "pod-rs", 2); err != nil || !reh {
		t.Fatalf("Reserve after Reset = (_, %v, %v), want rehydrated again", reh, err)
	}
	if n := src.callCount("pod-rs"); n != 2 {
		t.Errorf("source queried %d times, want 2 (once per rehydration)", n)
	}
}

// spec: §5.2 line 521 — a peer replica holds the rehydrating lock and
// never sets the flag; the spin-wait is bounded by the rehydration
// timeout and the reservation stalls rather than blocking forever.
func TestReserveStallsWhenLockHeldAndFlagNeverSet(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src, slotcounter.WithRehydrationTimeout(40*time.Millisecond))
	ctx := context.Background()

	// A peer holds the rehydrating lock with no TTL and never seeds.
	if err := cl.Set(ctx, "lenny:pod:pod-s:rehydrating", "peer-token", 0).Err(); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	_, _, err := c.Reserve(ctx, "pod-s", 2)
	if !errors.Is(err, slotcounter.ErrRehydrationStalled) {
		t.Errorf("Reserve = %v, want ErrRehydrationStalled", err)
	}
	// The source must never be queried while another replica holds the
	// lock.
	if n := src.callCount("pod-s"); n != 0 {
		t.Errorf("source queried %d times, want 0 (peer holds the lock)", n)
	}
}

// spec: §5.2 line 521 — a waiter blocked on a peer's rehydrating lock
// resumes and reserves once the peer sets the flag mid-wait.
func TestReserveWaitsForPeerFlagThenSucceeds(t *testing.T) {
	src := newFakeSource()
	c, _, cl := newCounterWithSource(t, src, slotcounter.WithRehydrationTimeout(2*time.Second))
	ctx := context.Background()

	// A peer holds the rehydrating lock. After a short delay it seeds the
	// counter to 1 and sets the flag (the peer's rehydration completing).
	if err := cl.Set(ctx, "lenny:pod:pod-w:rehydrating", "peer-token", 0).Err(); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = cl.Set(ctx, "lenny:pod:pod-w:active_slots", "1", 0).Err()
		_ = cl.Set(ctx, "lenny:pod:pod-w:rehydrated", "1", 0).Err()
	}()

	got, reh, err := c.Reserve(ctx, "pod-w", 3)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got != 2 {
		t.Errorf("Reserve returned %d, want 2 (peer seeded 1 + INCR)", got)
	}
	// This replica waited for the peer; it did not perform the seed.
	if reh {
		t.Error("waiting replica must report rehydrated=false (peer seeded)")
	}
	if n := src.callCount("pod-w"); n != 0 {
		t.Errorf("source queried %d times, want 0 (peer seeded)", n)
	}
}
