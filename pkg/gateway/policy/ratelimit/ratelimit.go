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
	"time"

	"github.com/lennylabs/lenny/pkg/ratewindow"
)

// Counter is the §11.1 per-minute request counter.
type Counter interface {
	// Incr records one request against key's current one-minute window
	// and returns the window's running count (1 for the first request
	// in a fresh window).
	Incr(ctx context.Context, key string, now time.Time) (int, error)
}

// Memory is the in-memory Counter: the fixed-window counter of
// pkg/ratewindow, which holds only the current minute's counts.
type Memory = ratewindow.Memory

// NewMemory returns an empty in-memory request counter.
func NewMemory() *Memory { return ratewindow.New() }

var _ Counter = (*Memory)(nil)
