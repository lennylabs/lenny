// SPDX-License-Identifier: MIT

// Package ratelimit is the §11.1 fixed-window request-rate counter. It
// backs the gateway's requests-per-minute admission limits: the
// rate-limit middleware increments a per-scope counter on every
// request and rejects once a scope's count exceeds its configured
// per-minute limit.
//
// Each key tracks one fixed one-minute window. The window rolls at the
// top of each wall-clock minute; a key's count starts again from zero
// when the minute advances. The counter reports the running count and
// leaves the limit comparison to the caller.
//
// The in-memory Counter here backs tests and the minimal gateway;
// ratelimit/redisstore is the cross-replica Redis implementation.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Counter is the §11.1 per-minute request counter.
type Counter interface {
	// Incr records one request against key's current one-minute window
	// and returns the window's running count (1 for the first request
	// in a fresh window).
	Incr(ctx context.Context, key string, now time.Time) (int, error)
}

// Memory is the in-memory Counter. It holds only the current minute's
// counts: when the minute advances the previous window's counts are
// dropped, so the map never grows beyond the active window.
type Memory struct {
	mu     sync.Mutex
	minute int64
	counts map[string]int
}

// NewMemory returns an empty in-memory request counter.
func NewMemory() *Memory {
	return &Memory{counts: map[string]int{}}
}

// Incr implements Counter.
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

var _ Counter = (*Memory)(nil)
