// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// Clock is the harness's stepped clock. Advancing it also advances the
// shared Redis's clock, so the coordination leases Redis holds expire on the
// harness's time.
type Clock struct {
	mu  sync.Mutex
	now time.Time
	mr  *miniredis.Miniredis
}

// NewClock returns a clock at origin that drives mr. The harness starts it
// at the wall clock, because the in-memory store stamps rows with wall time.
func NewClock(origin time.Time, mr *miniredis.Miniredis) *Clock {
	return &Clock{now: origin, mr: mr}
}

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock and Redis forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	if c.mr != nil {
		c.mr.FastForward(d)
	}
}
