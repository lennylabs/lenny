// SPDX-License-Identifier: MIT

package subsystem_test

import (
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
)

// fakeClock is a controllable clock used to drive the breaker
// cooldown without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// spec: §4.1 (Per-subsystem isolation guarantees — circuit breaker)
func TestBreakerStartsClosed(t *testing.T) {
	var b subsystem.Breaker
	if !b.Allow() {
		t.Fatal("zero-value breaker must admit requests")
	}
	if b.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q", b.State(), subsystem.StateClosed)
	}
}

// spec: §4.1 — N consecutive failures within the window trip the
// breaker open.
func TestBreakerTripsAfterThresholdFailures(t *testing.T) {
	b := &subsystem.Breaker{FailureThreshold: 3}
	for i := 0; i < 2; i++ {
		if !b.Allow() {
			t.Fatalf("breaker should still admit after %d failures", i)
		}
		b.RecordFailure()
	}
	if b.State() != subsystem.StateClosed {
		t.Fatalf("after 2 failures state = %q, want %q", b.State(), subsystem.StateClosed)
	}
	// Third failure trips it.
	if !b.Allow() {
		t.Fatal("breaker should admit the 3rd request before it fails")
	}
	b.RecordFailure()
	if b.State() != subsystem.StateOpen {
		t.Fatalf("after threshold failures state = %q, want %q", b.State(), subsystem.StateOpen)
	}
	if b.Allow() {
		t.Fatal("open breaker must reject new requests")
	}
}

// spec: §4.1 — successes interleaved with failures reset the
// consecutive-failure counter, so a slow trickle of errors should
// not trip the breaker.
func TestBreakerSuccessResetsFailureCounter(t *testing.T) {
	b := &subsystem.Breaker{FailureThreshold: 3}
	for i := 0; i < 5; i++ {
		b.Allow()
		b.RecordFailure()
		b.Allow()
		b.RecordSuccess()
	}
	if b.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q after interleaved success", b.State(), subsystem.StateClosed)
	}
}

// spec: §4.1 — after cooldown, the breaker admits exactly one probe
// (half-open), and admits nothing else until the probe outcome.
func TestBreakerHalfOpenAfterCooldown(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := &subsystem.Breaker{
		FailureThreshold: 1,
		Cooldown:         30 * time.Second,
		Now:              clk.now,
	}
	// Trip the breaker.
	b.Allow()
	b.RecordFailure()
	if b.State() != subsystem.StateOpen {
		t.Fatalf("State() = %q, want %q", b.State(), subsystem.StateOpen)
	}
	// Still inside cooldown.
	clk.advance(29 * time.Second)
	if b.Allow() {
		t.Fatal("breaker must reject inside cooldown window")
	}
	// Cross the cooldown threshold.
	clk.advance(2 * time.Second)
	if !b.Allow() {
		t.Fatal("breaker must admit a probe after cooldown elapses")
	}
	// Second concurrent caller during the probe must be rejected.
	if b.Allow() {
		t.Fatal("breaker must admit exactly one probe in half-open")
	}
}

// spec: §4.1 — a successful half-open probe closes the breaker.
func TestBreakerHalfOpenSuccessCloses(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := &subsystem.Breaker{
		FailureThreshold: 1,
		Cooldown:         30 * time.Second,
		Now:              clk.now,
	}
	b.Allow()
	b.RecordFailure()
	clk.advance(31 * time.Second)
	if !b.Allow() {
		t.Fatal("expected half-open admission after cooldown")
	}
	b.RecordSuccess()
	if b.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q after successful probe", b.State(), subsystem.StateClosed)
	}
	if !b.Allow() {
		t.Fatal("closed breaker must admit again after probe success")
	}
}

// spec: §4.1 — a failing half-open probe re-opens the breaker and
// resets the cooldown.
func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := &subsystem.Breaker{
		FailureThreshold: 1,
		Cooldown:         30 * time.Second,
		Now:              clk.now,
	}
	b.Allow()
	b.RecordFailure()
	clk.advance(31 * time.Second)
	if !b.Allow() {
		t.Fatal("expected half-open admission after cooldown")
	}
	b.RecordFailure()
	if b.State() != subsystem.StateOpen {
		t.Fatalf("State() = %q, want %q after probe failure", b.State(), subsystem.StateOpen)
	}
	// Cooldown re-armed from the second open instant.
	clk.advance(29 * time.Second)
	if b.Allow() {
		t.Fatal("breaker must reject inside the post-probe cooldown")
	}
}

// spec: §16.1 — the breaker state maps to the gauge value
// 0/1/2 for closed/half-open/open.
func TestStateMetricValue(t *testing.T) {
	cases := []struct {
		state subsystem.State
		want  int
	}{
		{subsystem.StateClosed, 0},
		{subsystem.StateHalfOpen, 1},
		{subsystem.StateOpen, 2},
	}
	for _, tc := range cases {
		if got := tc.state.MetricValue(); got != tc.want {
			t.Errorf("State(%q).MetricValue() = %d, want %d", tc.state, got, tc.want)
		}
	}
}
