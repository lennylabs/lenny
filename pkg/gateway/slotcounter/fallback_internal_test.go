// SPDX-License-Identifier: MIT

package slotcounter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// timeoutNetErr is a net.Error whose Timeout reports true, the shape a
// dial/read deadline surfaces; isRedisUnavailable routes any net.Error to the
// §12.4 Postgres fallback regardless of Timeout, so this exercises the
// errors.As(net.Error) branch.
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o deadline" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

// TestIsRedisUnavailableClassifiesConnectivityFailures pins the §12.4
// Redis-outage classifier: a connectivity failure (net.Error, ErrClosed,
// ECONNREFUSED, or a stable go-redis dial/pool message) routes slot admission
// to the Postgres fallback, while a nil error, a Redis-Nil miss, and a
// caller-originated context cancellation or deadline propagate unchanged.
//
// spec: 12.4 (Redis HA: a connectivity failure degrades to the Postgres
// fallback; a Redis-Nil miss or a Lua error is a genuine failure that
// propagates), 3.2 (intra-pod capacity gate during a Redis outage)
func TestIsRedisUnavailableClassifiesConnectivityFailures_spec_12_4(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error is not an outage", nil, false},
		{"redis.Nil miss is not an outage", redis.Nil, false},
		{"context canceled propagates", context.Canceled, false},
		{"context deadline propagates", context.DeadlineExceeded, false},
		{"net.Error is an outage", timeoutNetErr{}, true},
		{"wrapped net.Error is an outage", fmt.Errorf("dial: %w", timeoutNetErr{}), true},
		{"redis.ErrClosed is an outage", redis.ErrClosed, true},
		{"ECONNREFUSED is an outage", syscall.ECONNREFUSED, true},
		{"wrapped ECONNREFUSED is an outage", fmt.Errorf("connect: %w", syscall.ECONNREFUSED), true},
		{"connection refused fragment is an outage", errors.New("dial tcp 10.0.0.1:6379: connect: connection refused"), true},
		{"no such host fragment is an outage", errors.New("dial tcp: lookup redis: no such host"), true},
		{"client is closed fragment is an outage", errors.New("redis: client is closed"), true},
		{"connection pool timeout fragment is an outage", errors.New("redis: connection pool timeout"), true},
		{"broken pipe fragment is an outage", errors.New("write tcp: broken pipe"), true},
		{"a Lua script error propagates", errors.New("ERR Error running script: bad slot"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRedisUnavailable(tc.err); got != tc.want {
				t.Fatalf("isRedisUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

var _ net.Error = timeoutNetErr{}

// TestOutageWindowStampsThenExceeds pins the §12.4 bounded fail-closed window:
// the first reservation during an outage stamps the window start and reports
// not-yet-exceeded, a call inside the window stays within the bound, and a
// call past fallbackMaxWindow exceeds it so the gate fails closed.
//
// spec: 12.4 (after slotCounterPostgresFallbackMaxSeconds with Redis still
// unavailable, slot admission fails closed)
func TestOutageWindowStampsThenExceeds_spec_12_4(t *testing.T) {
	base := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	now := base
	c := &Counter{fallbackMaxWindow: 60 * time.Second, now: func() time.Time { return now }}

	if c.outageExceeded() {
		t.Fatal("first call during an outage must stamp the window, not exceed it")
	}
	now = base.Add(30 * time.Second)
	if c.outageExceeded() {
		t.Fatal("a call inside the 60s window must not exceed the bound")
	}
	now = base.Add(61 * time.Second)
	if !c.outageExceeded() {
		t.Fatal("a call past fallbackMaxWindow must exceed the bound and fail closed")
	}
}

// TestClearOutageResetsWindow pins the §12.4 recovery path: once Redis answers
// the outage window is cleared, so a later outage measures a fresh window from
// its own first-observed time rather than the prior outage's start. clearOutage
// on an already-clear window is a no-op.
//
// spec: 12.4 (on Redis recovery the counter resumes fast-path enforcement)
func TestClearOutageResetsWindow_spec_12_4(t *testing.T) {
	base := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	now := base
	c := &Counter{fallbackMaxWindow: 60 * time.Second, now: func() time.Time { return now }}

	// clearing a window that was never stamped is a no-op.
	c.clearOutage()

	c.outageExceeded() // stamps the window at base
	c.clearOutage()    // Redis recovered; the window resets

	now = base.Add(120 * time.Second)
	if c.outageExceeded() {
		t.Fatal("after recovery a new outage must measure a fresh window, not the prior start")
	}
}
