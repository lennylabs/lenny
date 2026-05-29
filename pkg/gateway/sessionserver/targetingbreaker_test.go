// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"
)

// spec: §10.7 lines 835-844 (SCL-023) — the per-tenant OpenFeature
// targeting circuit breaker.

type gaugeEvent struct {
	tenant, provider string
	open             bool
}

// fakeClock is a settable clock for the breaker tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time   { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestBreaker() (*targetingBreaker, *fakeClock, *[]gaugeEvent) {
	clock := &fakeClock{t: time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)}
	var gauge []gaugeEvent
	b := newTargetingBreaker(clock.now, func(tn, p string, open bool) {
		gauge = append(gauge, gaugeEvent{tn, p, open})
	})
	return b, clock, &gauge
}

var testParams = targetingBreakerParams{threshold: 5, window: 10 * time.Second, openDur: 30 * time.Second}

// spec: §10.7 line 837 — 5 consecutive failures within the window open
// the circuit; fewer do not.
func TestTargetingBreakerOpensAtThreshold(t *testing.T) {
	b, clock, gauge := newTestBreaker()
	for i := 0; i < 4; i++ {
		if !b.Allow("acme", "flags.acme.com") {
			t.Fatalf("failure %d: breaker must stay closed below threshold", i)
		}
		b.Record("acme", "flags.acme.com", testParams, false)
		clock.add(time.Second)
	}
	if !b.Allow("acme", "flags.acme.com") {
		t.Fatal("4 failures must not open the breaker")
	}
	b.Record("acme", "flags.acme.com", testParams, false) // 5th within 10s
	if b.Allow("acme", "flags.acme.com") {
		t.Fatal("5th consecutive failure within the window must open the breaker")
	}
	if len(*gauge) != 1 || !(*gauge)[0].open || (*gauge)[0].tenant != "acme" || (*gauge)[0].provider != "flags.acme.com" {
		t.Fatalf("expected one open gauge event for (acme, flags.acme.com), got %+v", *gauge)
	}
}

// spec: §10.7 line 837 — failures spread beyond the rolling window do not
// open the breaker (they age out of the window).
func TestTargetingBreakerWindowAgesOutFailures(t *testing.T) {
	b, clock, _ := newTestBreaker()
	for i := 0; i < 6; i++ {
		b.Record("acme", "p", testParams, false)
		clock.add(3 * time.Second) // 6 failures spread over 18s, >10s window
	}
	if !b.Allow("acme", "p") {
		t.Fatal("failures spanning more than the window must not open the breaker")
	}
}

// spec: §10.7 line 837 — a success resets the consecutive-failure run.
func TestTargetingBreakerSuccessResetsRun(t *testing.T) {
	b, _, _ := newTestBreaker()
	for i := 0; i < 4; i++ {
		b.Record("acme", "p", testParams, false)
	}
	b.Record("acme", "p", testParams, true) // reset
	for i := 0; i < 4; i++ {
		b.Record("acme", "p", testParams, false)
	}
	if !b.Allow("acme", "p") {
		t.Fatal("4 failures after a reset must not open the breaker")
	}
}

// spec: §10.7 lines 838-839 — while open the breaker denies calls until
// the open window elapses, then admits a single half-open probe; a probe
// success closes the breaker.
func TestTargetingBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	b, clock, gauge := newTestBreaker()
	for i := 0; i < 5; i++ {
		b.Record("acme", "p", testParams, false)
	}
	if b.Allow("acme", "p") {
		t.Fatal("breaker must be open immediately after tripping")
	}
	clock.add(29 * time.Second)
	if b.Allow("acme", "p") {
		t.Fatal("breaker must stay open before the open duration elapses")
	}
	clock.add(2 * time.Second) // total 31s > 30s open duration
	if !b.Allow("acme", "p") {
		t.Fatal("breaker must admit a half-open probe after the open window")
	}
	// A second concurrent caller must not get a second probe.
	if b.Allow("acme", "p") {
		t.Fatal("only one half-open probe may be admitted")
	}
	b.Record("acme", "p", testParams, true) // probe succeeds
	if !b.Allow("acme", "p") {
		t.Fatal("a successful probe must close the breaker")
	}
	// Gauge sequence: open=true then open=false.
	if len(*gauge) != 2 || !(*gauge)[0].open || (*gauge)[1].open {
		t.Fatalf("expected gauge open then closed, got %+v", *gauge)
	}
}

// spec: §10.7 line 839 — a failed half-open probe re-arms the open window.
func TestTargetingBreakerHalfOpenProbeFailureReArms(t *testing.T) {
	b, clock, gauge := newTestBreaker()
	for i := 0; i < 5; i++ {
		b.Record("acme", "p", testParams, false)
	}
	clock.add(31 * time.Second)
	if !b.Allow("acme", "p") {
		t.Fatal("breaker must admit a probe after the open window")
	}
	b.Record("acme", "p", testParams, false) // probe fails
	if b.Allow("acme", "p") {
		t.Fatal("a failed probe must re-open the breaker")
	}
	clock.add(31 * time.Second)
	if !b.Allow("acme", "p") {
		t.Fatal("breaker must admit a fresh probe after the re-armed window")
	}
	// Gauge: open(trip), open(re-arm). Two open events, no close.
	for _, g := range *gauge {
		if !g.open {
			t.Fatalf("no close event expected before recovery, got %+v", *gauge)
		}
	}
}

// Per-(tenant, provider) keying: one tenant's failures do not open
// another tenant's circuit.
func TestTargetingBreakerIsolatesTenants(t *testing.T) {
	b, _, _ := newTestBreaker()
	for i := 0; i < 5; i++ {
		b.Record("acme", "p", testParams, false)
	}
	if b.Allow("acme", "p") {
		t.Fatal("acme breaker must be open")
	}
	if !b.Allow("globex", "p") {
		t.Fatal("globex breaker must be unaffected by acme failures")
	}
}

// A nil breaker is a no-op that always allows (defensive: the field is
// never nil after New, but the methods guard anyway).
func TestTargetingBreakerNilIsSafe(t *testing.T) {
	var b *targetingBreaker
	if !b.Allow("acme", "p") {
		t.Fatal("nil breaker must allow")
	}
	b.Record("acme", "p", testParams, false) // must not panic
}
