// SPDX-License-Identifier: MIT

package dualstore

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeGauge records every SetDualStoreUnavailable transition.
type fakeGauge struct {
	mu     sync.Mutex
	values []bool
}

func (g *fakeGauge) SetDualStoreUnavailable(v bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values = append(g.values, v)
}

func (g *fakeGauge) last() (bool, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.values) == 0 {
		return false, false
	}
	return g.values[len(g.values)-1], true
}

// fakeBroadcaster records every Broadcast call.
type fakeBroadcaster struct {
	mu    sync.Mutex
	calls []struct{ eventType, data string }
}

func (b *fakeBroadcaster) Broadcast(eventType, data string, _ time.Time) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, struct{ eventType, data string }{eventType, data})
	return 3
}

func (b *fakeBroadcaster) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

// clock is a manually-advanced time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func constProbe(reachable *bool) Probe {
	return func(context.Context) bool { return *reachable }
}

// spec: §10.1 item 5 — on detecting both stores unreachable the monitor
// pins the gauge to 1 and broadcasts PLATFORM_DEGRADED with the
// {"reason":"dual_store_unavailable","retry_after":10} payload.
func TestMonitor_DetectionFiresGaugeAndBroadcast_spec_10_1(t *testing.T) {
	pgUp, redisUp := true, true
	gauge := &fakeGauge{}
	bc := &fakeBroadcaster{}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := &Monitor{
		PostgresProbe: constProbe(&pgUp),
		RedisProbe:    constProbe(&redisUp),
		Gauge:         gauge,
		Streams:       bc,
		Now:           clk.now,
	}
	// Healthy: no transition.
	m.tick(context.Background())
	if m.Unavailable() {
		t.Fatal("monitor must not be unavailable while both stores are up")
	}
	if _, ok := gauge.last(); ok {
		t.Fatal("gauge must not be set while healthy")
	}

	// Both stores fail.
	pgUp, redisUp = false, false
	m.tick(context.Background())
	if !m.Unavailable() {
		t.Fatal("monitor must report unavailable once both stores are down")
	}
	if v, ok := gauge.last(); !ok || !v {
		t.Fatalf("gauge must be set to 1 on detection, got (%v, ok=%v)", v, ok)
	}
	if bc.count() != 1 {
		t.Fatalf("expected exactly one PLATFORM_DEGRADED broadcast, got %d", bc.count())
	}
	bc.mu.Lock()
	call := bc.calls[0]
	bc.mu.Unlock()
	if call.eventType != PlatformDegradedEventType {
		t.Fatalf("broadcast event type = %q, want %q", call.eventType, PlatformDegradedEventType)
	}
	if call.data != PlatformDegradedData {
		t.Fatalf("broadcast data = %q, want %q", call.data, PlatformDegradedData)
	}
}

// spec: §10.1 item 1/5 — a single store down (not both) is NOT the
// dual-store degraded mode; existing sessions continue and creation is
// not gated.
func TestMonitor_SingleStoreDownIsNotDegraded_spec_10_1(t *testing.T) {
	pgUp, redisUp := true, false
	gauge := &fakeGauge{}
	bc := &fakeBroadcaster{}
	m := &Monitor{
		PostgresProbe: constProbe(&pgUp),
		RedisProbe:    constProbe(&redisUp),
		Gauge:         gauge,
		Streams:       bc,
	}
	m.tick(context.Background())
	if m.Unavailable() {
		t.Fatal("only Redis is down; dual-store degraded mode must not engage")
	}
	if bc.count() != 0 {
		t.Fatal("no broadcast expected when only one store is down")
	}
}

// spec: §10.1 item 5 — the degraded mode clears (gauge → 0) as soon as
// at least one store recovers, and Broadcast is not re-fired on every
// subsequent down-tick (single edge-triggered notification).
func TestMonitor_RecoveryClearsGauge_spec_10_1(t *testing.T) {
	pgUp, redisUp := false, false
	gauge := &fakeGauge{}
	bc := &fakeBroadcaster{}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := &Monitor{
		PostgresProbe: constProbe(&pgUp),
		RedisProbe:    constProbe(&redisUp),
		Gauge:         gauge,
		Streams:       bc,
		Now:           clk.now,
	}
	m.tick(context.Background()) // detect
	m.tick(context.Background()) // still down — must not re-broadcast
	if bc.count() != 1 {
		t.Fatalf("broadcast must be edge-triggered once per outage, got %d", bc.count())
	}
	// Postgres recovers.
	pgUp = true
	m.tick(context.Background())
	if m.Unavailable() {
		t.Fatal("monitor must clear unavailable once a store recovers")
	}
	if v, _ := gauge.last(); v {
		t.Fatal("gauge must be cleared to 0 on recovery")
	}
}

// spec: §10.1 item 4 — the per-replica dualStoreUnavailableMaxSeconds
// countdown is anchored at detection and fires OnHoldExpired exactly
// once when it elapses while the outage is still ongoing.
func TestMonitor_HoldTimerExpiresOnce_spec_10_1(t *testing.T) {
	pgUp, redisUp := false, false
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	var expiredCalls int
	var expiredStart time.Time
	m := &Monitor{
		PostgresProbe:  constProbe(&pgUp),
		RedisProbe:     constProbe(&redisUp),
		MaxUnavailable: 60 * time.Second,
		Now:            clk.now,
		OnHoldExpired: func(start time.Time) {
			expiredCalls++
			expiredStart = start
		},
	}
	detectAt := clk.now()
	m.tick(context.Background()) // detect, anchor countdown
	// 59s later: not yet expired.
	clk.advance(59 * time.Second)
	m.tick(context.Background())
	if expiredCalls != 0 {
		t.Fatal("hold timer must not expire before dualStoreUnavailableMaxSeconds")
	}
	// 61s total: expired.
	clk.advance(2 * time.Second)
	m.tick(context.Background())
	if expiredCalls != 1 {
		t.Fatalf("hold timer must fire exactly once, got %d", expiredCalls)
	}
	if !expiredStart.Equal(detectAt) {
		t.Fatalf("OnHoldExpired must carry the detection anchor %v, got %v", detectAt, expiredStart)
	}
	// Further down-ticks must not re-fire.
	clk.advance(30 * time.Second)
	m.tick(context.Background())
	if expiredCalls != 1 {
		t.Fatalf("hold timer must not re-fire while the same outage holds, got %d", expiredCalls)
	}
}

// spec: §10.1 — a re-entry after recovery re-anchors the countdown so a
// second outage gets its own full window.
func TestMonitor_SecondOutageReanchors_spec_10_1(t *testing.T) {
	pgUp, redisUp := false, false
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	var expiredCalls int
	m := &Monitor{
		PostgresProbe:  constProbe(&pgUp),
		RedisProbe:     constProbe(&redisUp),
		MaxUnavailable: 60 * time.Second,
		Now:            clk.now,
		OnHoldExpired:  func(time.Time) { expiredCalls++ },
	}
	m.tick(context.Background()) // outage 1 detect
	clk.advance(61 * time.Second)
	m.tick(context.Background()) // outage 1 expires
	pgUp = true
	m.tick(context.Background()) // recover
	if expiredCalls != 1 {
		t.Fatalf("after first outage, want 1 expiry, got %d", expiredCalls)
	}
	// Second outage.
	pgUp = false
	m.tick(context.Background()) // outage 2 detect, re-anchor
	clk.advance(30 * time.Second)
	m.tick(context.Background())
	if expiredCalls != 1 {
		t.Fatal("second outage must not inherit the first outage's elapsed time")
	}
	clk.advance(31 * time.Second)
	m.tick(context.Background())
	if expiredCalls != 2 {
		t.Fatalf("second outage must expire on its own window, got %d", expiredCalls)
	}
}

// spec: §10.1 — a nil probe (an unwired store) is treated as reachable
// so a single-store replica never enters the dual-store degraded mode.
func TestMonitor_NilProbeIsReachable(t *testing.T) {
	redisUp := false
	m := &Monitor{
		PostgresProbe: nil, // Postgres not wired
		RedisProbe:    constProbe(&redisUp),
	}
	m.tick(context.Background())
	if m.Unavailable() {
		t.Fatal("with Postgres unwired, the replica has no dual-store condition")
	}
}

// A nil Monitor reports available so the session-create gate is open in
// the in-memory posture.
func TestMonitor_NilIsAvailable(t *testing.T) {
	var m *Monitor
	if m.Unavailable() {
		t.Fatal("nil monitor must report available")
	}
}
