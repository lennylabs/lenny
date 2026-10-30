// SPDX-License-Identifier: MIT

package clockstep

import (
	"sort"
	"sync"
	"time"
)

// Clock is a deterministic, advanceable clock. Now() returns the
// current pinned time. Advance(d) moves the clock forward and fires
// every timer whose deadline has passed.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*Timer
}

// New returns a Clock pinned at the supplied origin.
func New(origin time.Time) *Clock {
	return &Clock{now: origin}
}

// Now returns the current pinned time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance shifts the clock forward by d and fires every timer whose
// deadline has passed. Timers fire in deadline order.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := []*Timer{}
	rest := c.timers[:0]
	for _, t := range c.timers {
		if !t.deadline.After(c.now) {
			due = append(due, t)
		} else {
			rest = append(rest, t)
		}
	}
	c.timers = rest
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].deadline.Before(due[j].deadline) })
	for _, t := range due {
		t.fire(c.now)
	}
}

// After returns a Timer that fires once d after the current pinned
// time. The Timer.C channel receives the firing time.
func (c *Clock) After(d time.Duration) *Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &Timer{
		deadline: c.now.Add(d),
		C:        make(chan time.Time, 1),
	}
	c.timers = append(c.timers, t)
	return t
}

// PendingTimers returns the number of unfired timers.
func (c *Clock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Timer mirrors time.Timer for clockstep-backed code paths.
type Timer struct {
	deadline time.Time
	C        chan time.Time

	mu    sync.Mutex
	fired bool
}

func (t *Timer) fire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fired {
		return
	}
	t.fired = true
	select {
	case t.C <- now:
	default:
	}
}

// Fired reports whether the timer has fired.
func (t *Timer) Fired() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fired
}

// Deadline returns the timer's deadline (pinned-clock time, not wall
// clock).
func (t *Timer) Deadline() time.Time { return t.deadline }
