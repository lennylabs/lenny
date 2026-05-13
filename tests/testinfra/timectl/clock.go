// SPDX-License-Identifier: MIT

// Package timectl provides a Clock interface used by every Lenny component
// that reads wall-clock time, so tests can substitute a frozen or
// advanceable clock instead of waiting for real wall-clock duration.
//
// Production code receives a Real() clock by default. Tests construct a
// Fake clock with NewFake(t), drive time forward with Advance, and assert
// against the controlled timeline.
//
// See TESTING.md §10 (Service and cluster provisioning — Time and
// randomness control) for the canonical rationale: most flaky tests in
// distributed systems trace back to uncontrolled time.
package timectl

import (
	"sync"
	"testing"
	"time"
)

// Clock is the abstraction every time-reading component depends on. The
// real-time implementation lives in this package; tests inject a Fake.
type Clock interface {
	// Now reports the current time.
	Now() time.Time
	// Since reports the elapsed time since t.
	Since(t time.Time) time.Duration
	// Sleep blocks for d. On a Fake clock, Sleep returns immediately if d
	// is in the past relative to the fake now; otherwise it is satisfied
	// the next time the test calls Advance.
	Sleep(d time.Duration)
}

// Real returns a Clock backed by the standard time package.
func Real() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (realClock) Sleep(d time.Duration)           { time.Sleep(d) }

// Fake is a clock whose time only changes when the test advances it.
// Construct one with NewFake; the Fake also registers a t.Cleanup that
// unblocks any pending Sleep callers so tests cannot leak goroutines.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	closed  bool
}

type waiter struct {
	wakeAt time.Time
	done   chan struct{}
}

// NewFake constructs a Fake clock whose now is at unix-epoch zero unless
// initial is non-zero. The cleanup hook calls Close() to unblock pending
// Sleep waiters when the test ends, so a leaked goroutine cannot keep
// the test alive past its own return.
func NewFake(t *testing.T, initial time.Time) *Fake {
	t.Helper()
	f := &Fake{now: initial}
	t.Cleanup(f.Close)
	return f
}

// Close releases all pending Sleep waiters and prevents future Sleep
// calls from blocking. Idempotent; the NewFake cleanup hook calls this
// at test end, but tests can invoke it directly to assert the release
// path deterministically.
func (f *Fake) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.waiters {
		close(w.done)
	}
	f.waiters = nil
	f.closed = true
}

// Now reports the controlled current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Since reports elapsed time relative to the controlled now.
func (f *Fake) Since(t time.Time) time.Duration {
	return f.Now().Sub(t)
}

// Sleep blocks until the controlled now reaches the start time + d, or
// until the test cleanup releases waiters. Returns immediately once
// Close() has been called.
func (f *Fake) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	w := waiter{wakeAt: f.now.Add(d), done: make(chan struct{})}
	f.waiters = append(f.waiters, w)
	f.mu.Unlock()
	<-w.done
}

// Advance moves the controlled now forward by d and wakes any waiters
// whose wakeAt has passed.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	remaining := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.wakeAt.After(f.now) {
			close(w.done)
			continue
		}
		remaining = append(remaining, w)
	}
	f.waiters = remaining
}

// Set moves the controlled now to t. Useful for absolute-time assertions.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	delta := t.Sub(f.now)
	f.mu.Unlock()
	f.Advance(delta)
}
