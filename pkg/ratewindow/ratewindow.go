// SPDX-License-Identifier: MIT

// Package ratewindow is the in-memory fixed one-minute request counter.
// Each key tracks one window that rolls at the top of each wall-clock
// minute: a key's count starts again from zero when the minute advances,
// and the counter holds only the current minute's counts. It reports the
// running count and leaves the limit comparison to the caller.
//
// The gateway's request-rate limiter uses it as its in-memory counter, and
// the parts that count requests through the seam interface fall back to it
// when no shared counter is wired.
//
// spec: §11.1 (Admission and Fairness)
package ratewindow

import (
	"context"
	"sync"
	"time"
)

// Memory is the in-memory per-minute request counter. Its zero value is
// not ready for use; New returns one.
type Memory struct {
	mu     sync.Mutex
	minute int64
	counts map[string]int
}

// New returns an empty in-memory request counter.
func New() *Memory {
	return &Memory{counts: map[string]int{}}
}

// Incr counts one request under key in the current minute and returns the
// count so far, which is 1 for the first request of a minute. It never
// fails.
func (m *Memory) Incr(_ context.Context, key string, now time.Time) (int, error) {
	minute := now.Unix() / 60
	m.mu.Lock()
	defer m.mu.Unlock()
	if minute != m.minute {
		m.minute = minute
		m.counts = map[string]int{}
	}
	m.counts[key]++
	return m.counts[key], nil
}

// IncrementRate is Incr under the method name of the seam interface's rate
// counter, so a part that counts through the seam falls back to Memory.
func (m *Memory) IncrementRate(ctx context.Context, key string, now time.Time) (int, error) {
	return m.Incr(ctx, key, now)
}
