// SPDX-License-Identifier: MIT

package revocation

import (
	"sync"
	"testing"
	"time"
)

// fakeTimers is a clock whose time moves only when the test advances it,
// with the timers the reconnect spacing waits on. It stands in for both
// Cache.clock and Cache.after.
type fakeTimers struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
	waits   []time.Duration
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeTimers() *fakeTimers {
	return &fakeTimers{now: time.Unix(1_700_000_000, 0)}
}

func (f *fakeTimers) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After records the wait asked for and returns a channel that delivers
// once advance has moved the clock past it.
func (f *fakeTimers) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	f.waiters = append(f.waiters, fakeWaiter{at: f.now.Add(d), ch: ch})
	f.waits = append(f.waits, d)
	return ch
}

// advance moves the clock by d and fires every timer now due.
func (f *fakeTimers) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(f.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- f.now
	}
	f.waiters = kept
}

// stepBack moves the clock back by d without firing a timer.
func (f *fakeTimers) stepBack(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(-d)
}

// pending returns the number of timers not yet fired.
func (f *fakeTimers) pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

// waitsAsked returns the waits After was asked for, in order.
func (f *fakeTimers) waitsAsked() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

// newSpacedCache returns a cache whose clock and spacing timers ft drives.
func newSpacedCache(ft *fakeTimers) *Cache {
	c := NewCache(WithClock(ft.Now))
	c.after = ft.After
	return c
}

// awaitCond polls cond every 5 ms until it holds or 5 s pass.
func awaitCond(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after 5 s waiting for %s", desc)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertReadsStay checks that the source sees exactly want reads over a
// short real-time window.
func assertReadsStay(t *testing.T, src *countingSource, want int32, why string) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if n := src.reads.Load(); n != want {
		t.Fatalf("%s: the source saw %d reads, want %d", why, n, want)
	}
}

// TestATriggerInsideTheReconnectSpacingWaitsForIt pins that a request
// arriving 1 s after the start of the previous requested rehydration is
// held for the 4 s left of a 5 s spacing, that requests made while it is
// held fold into it, and that it starts once the spacing has passed. A
// flapping Redis connection therefore costs at most one read per spacing.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestATriggerInsideTheReconnectSpacingWaitsForIt(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	runSpacedLoop(t, c, time.Hour, 5*time.Second, trigger, src, nil)

	trigger.Fire()
	awaitRead(t, src, 1)

	ft.advance(time.Second)
	trigger.Fire()
	awaitCond(t, "the second request to be held", func() bool { return ft.pending() == 1 })
	if got := ft.waitsAsked(); len(got) != 1 || got[0] != 4*time.Second {
		t.Fatalf("the held request waits %v, want [4s]", got)
	}
	for range 5 {
		trigger.Fire()
	}
	assertReadsStay(t, src, 1, "inside the spacing")

	ft.advance(3 * time.Second)
	assertReadsStay(t, src, 1, "1 s before the spacing passes")

	ft.advance(time.Second)
	awaitRead(t, src, 2)
	assertReadsStay(t, src, 2, "the requests made while one was held")
	if n := ft.pending(); n != 0 {
		t.Errorf("%d timers are pending after the held request ran, want 0", n)
	}
}

// TestATriggerAfterTheReconnectSpacingRunsAtOnce pins that a request
// arriving once the spacing has passed starts its rehydration without a
// wait.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestATriggerAfterTheReconnectSpacingRunsAtOnce(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	runSpacedLoop(t, c, time.Hour, 5*time.Second, trigger, src, nil)

	trigger.Fire()
	awaitRead(t, src, 1)
	ft.advance(5 * time.Second)
	trigger.Fire()
	awaitRead(t, src, 2)
	if got := ft.waitsAsked(); len(got) != 0 {
		t.Errorf("a request at the spacing waited %v, want no wait", got)
	}
}

// TestTheReconnectSpacingCountsFromTheHeldRehydrationsStart pins that the
// spacing after a held request counts from the start of the held
// request's rehydration, so back-to-back held requests also start a full
// spacing apart.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestTheReconnectSpacingCountsFromTheHeldRehydrationsStart(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	runSpacedLoop(t, c, time.Hour, 5*time.Second, trigger, src, nil)

	trigger.Fire()
	awaitRead(t, src, 1)
	ft.advance(2 * time.Second)
	trigger.Fire()
	awaitCond(t, "the second request to be held", func() bool { return ft.pending() == 1 })
	ft.advance(3 * time.Second)
	awaitRead(t, src, 2)

	ft.advance(time.Second)
	trigger.Fire()
	awaitCond(t, "the third request to be held", func() bool { return ft.pending() == 1 })
	if got := ft.waitsAsked(); len(got) != 2 || got[1] != 4*time.Second {
		t.Fatalf("the waits asked are %v, want the second to be 4s", got)
	}
}

// TestTheFirstTriggerRunsAtOnceWithASpacing pins that the first request,
// the first subscription confirmation in the gateway, starts its
// rehydration without a wait.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestTheFirstTriggerRunsAtOnceWithASpacing(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	trigger.Fire()
	runSpacedLoop(t, c, time.Hour, 5*time.Second, trigger, src, nil)
	awaitRead(t, src, 1)
	if got := ft.waitsAsked(); len(got) != 0 {
		t.Errorf("the first request waited %v, want no wait", got)
	}
}

// TestATickRunsWhileATriggerWaitsForTheSpacing pins that the periodic
// rehydration keeps its schedule while a request is held by the spacing.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestATickRunsWhileATriggerWaitsForTheSpacing(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	runSpacedLoop(t, c, 20*time.Millisecond, time.Hour, trigger, src, nil)

	trigger.Fire()
	awaitCond(t, "the loop to take the first request", func() bool { return len(trigger.ch) == 0 })
	trigger.Fire()
	awaitCond(t, "the second request to be held", func() bool { return ft.pending() == 1 })
	held := src.reads.Load()
	awaitRead(t, src, held+3)
	if n := ft.pending(); n != 1 {
		t.Errorf("%d timers are pending after the ticks, want the held request's 1", n)
	}
}

// TestAZeroReconnectSpacingStartsEveryRequestAtOnce pins that a spacing
// of zero holds no request.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestAZeroReconnectSpacingStartsEveryRequestAtOnce(t *testing.T) {
	ft := newFakeTimers()
	c := newSpacedCache(ft)
	src := newCountingSource("jti-1")
	trigger := NewTrigger()
	runSpacedLoop(t, c, time.Hour, 0, trigger, src, nil)
	for i := range int32(3) {
		trigger.Fire()
		awaitRead(t, src, i+1)
	}
	if got := ft.waitsAsked(); len(got) != 0 {
		t.Errorf("a zero spacing waited %v, want no wait", got)
	}
}

// TestTheReconnectSpacingWaitIsCappedWhenTheClockStepsBack pins that a
// clock that steps back after a requested rehydration does not hold the
// next request for longer than the spacing.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestTheReconnectSpacingWaitIsCappedWhenTheClockStepsBack(t *testing.T) {
	ft := newFakeTimers()
	s := requestSpacer{min: 5 * time.Second, now: ft.Now, after: ft.After}
	if !s.admit() {
		t.Fatal("the first request was held")
	}
	ft.stepBack(time.Minute)
	if s.admit() {
		t.Fatal("a request after the clock stepped back was admitted inside the spacing")
	}
	if got := ft.waitsAsked(); len(got) != 1 || got[0] != 5*time.Second {
		t.Errorf("the held request waits %v, want [5s]", got)
	}
}

// TestDefaultReconnectSpacingIsFiveSeconds pins the default the gateway
// flag carries.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestDefaultReconnectSpacingIsFiveSeconds(t *testing.T) {
	if DefaultReconnectSpacing != 5*time.Second {
		t.Errorf("DefaultReconnectSpacing = %v, want 5s", DefaultReconnectSpacing)
	}
}

// TestNilTriggerDiscardIsANoOp pins that a nil Trigger ignores discard.
// It verifies a nil guard and cites no requirement.
func TestNilTriggerDiscardIsANoOp(t *testing.T) {
	var trigger *Trigger
	trigger.discard()
}
