// SPDX-License-Identifier: MIT

package timectl_test

import (
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/timectl"
)

// spec: 10 (TESTING.md §10)
// diagnosis: Real() returned the wrong wall-clock kind of value. The Real
//
//	clock just delegates to the standard library.
func TestRealClockNowIsClose(t *testing.T) {
	t.Parallel()
	c := timectl.Real()
	before := time.Now()
	got := c.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("Real().Now() = %v, want in [%v, %v]", got, before, after)
	}
}

// spec: 10
// diagnosis: A Fake clock did not freeze time. Advance(d) is the only way
//
//	the Fake clock should move; Now() must return the same
//	value across repeated calls until Advance is called.
func TestFakeClockFrozenUntilAdvance(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := timectl.NewFake(t, start)

	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("repeated Now() = %v, want still %v", got, start)
	}
	f.Advance(2 * time.Hour)
	if got := f.Now(); !got.Equal(start.Add(2 * time.Hour)) {
		t.Errorf("after Advance(2h) Now() = %v, want %v", got, start.Add(2*time.Hour))
	}
}

// spec: 10
// diagnosis: Fake.Sleep blocked forever even though Advance moved past
//
//	the wake-at time. The implementation must wake waiters
//	whose wakeAt is at or before the new now.
func TestFakeSleepUnblocksOnAdvance(t *testing.T) {
	t.Parallel()
	f := timectl.NewFake(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	woke := make(chan struct{})
	go func() {
		f.Sleep(5 * time.Second)
		close(woke)
	}()

	select {
	case <-woke:
		t.Fatalf("Sleep returned before Advance")
	case <-time.After(50 * time.Millisecond):
		// expected: still sleeping
	}

	f.Advance(5 * time.Second)

	select {
	case <-woke:
		// expected: woke
	case <-time.After(time.Second):
		t.Fatalf("Sleep did not wake after Advance(5s)")
	}
}

// spec: 10
// diagnosis: Fake.Sleep with a zero or negative duration must return
//
//	immediately. Otherwise tests that conditionally sleep
//	deadlock.
func TestFakeSleepNonPositiveReturnsImmediately(t *testing.T) {
	t.Parallel()
	f := timectl.NewFake(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := make(chan struct{})
	go func() {
		f.Sleep(0)
		f.Sleep(-time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Sleep(0) or Sleep(-1s) blocked")
	}
}

// spec: 10
// diagnosis: Close() did not release pending Sleep callers. NewFake's
//
//	cleanup hook delegates to Close; the test invokes Close
//	directly to make the timing deterministic.
func TestFakeCloseReleasesWaiters(t *testing.T) {
	t.Parallel()
	f := timectl.NewFake(t, time.Time{})

	// Spawn three goroutines that each call Sleep with different durations.
	const n = 3
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			f.Sleep(time.Hour)
		}()
	}

	// Spin until all three goroutines have registered their waiters.
	// Using an empty Advance(0) wouldn't reveal the count, so we read the
	// internal state via Sleep's observable side effect: Advance(0) is a
	// no-op, so waiters remain. We poll until Close releases them.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// One more synchronization tick to give the goroutines a chance.
		time.Sleep(5 * time.Millisecond)
		// Heuristic: if all three slept, they must each have appended a
		// waiter. We just call Close once we believe they're parked.
		// A small sleep is sufficient because Sleep's prelude is trivial.
		break
	}
	time.Sleep(50 * time.Millisecond)

	f.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Close did not release Sleep waiters")
	}
}

// spec: 10
// diagnosis: Sleep called after Close blocked. Once closed, Sleep must
//
//	return immediately.
func TestFakeSleepAfterCloseIsNoOp(t *testing.T) {
	t.Parallel()
	f := timectl.NewFake(t, time.Time{})
	f.Close()
	done := make(chan struct{})
	go func() {
		f.Sleep(time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Sleep after Close blocked")
	}
}

// spec: 10
// diagnosis: Set(t) did not advance the clock to t. Set must be
//
//	equivalent to Advance(t - now).
func TestFakeSet(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	target := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	f := timectl.NewFake(t, start)
	f.Set(target)
	if got := f.Now(); !got.Equal(target) {
		t.Errorf("after Set, Now() = %v, want %v", got, target)
	}
}

// spec: 10
// diagnosis: Since() did not measure relative to the controlled now on
//
//	a Fake clock.
func TestFakeSince(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := timectl.NewFake(t, start)
	f.Advance(3 * time.Second)
	if got := f.Since(start); got != 3*time.Second {
		t.Errorf("Since(start) = %v, want 3s", got)
	}
}
