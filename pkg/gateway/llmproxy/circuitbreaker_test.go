// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 — the LLM Proxy circuit breaker around an upstream
// provider: closed → open on consecutive failures, half-open probe
// after the cooldown, probe outcome closes or reopens.

// fakeClock is a test clock the breaker reads through CircuitBreaker.Now.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestCircuitBreakerClosedAdmitsEverything(t *testing.T) {
	b := &llmproxy.CircuitBreaker{FailureThreshold: 3}
	for i := 0; i < 10; i++ {
		if !b.Allow() {
			t.Fatalf("closed breaker rejected request %d", i)
		}
		b.RecordSuccess()
	}
	if b.State() != llmproxy.CircuitClosed {
		t.Errorf("state = %q, want closed", b.State())
	}
}

func TestCircuitBreakerTripsOpenOnConsecutiveFailures(t *testing.T) {
	b := &llmproxy.CircuitBreaker{FailureThreshold: 3}
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("closed breaker rejected request %d before the threshold", i)
		}
		b.RecordFailure()
	}
	if b.State() != llmproxy.CircuitOpen {
		t.Fatalf("state = %q, want open after 3 consecutive failures", b.State())
	}
	if b.Allow() {
		t.Error("open breaker admitted a request before the cooldown elapsed")
	}
}

func TestCircuitBreakerSuccessResetsFailureCount(t *testing.T) {
	b := &llmproxy.CircuitBreaker{FailureThreshold: 3}
	b.Allow()
	b.RecordFailure()
	b.Allow()
	b.RecordFailure()
	// A success clears the count, so the breaker should not trip on the
	// next two failures.
	b.Allow()
	b.RecordSuccess()
	b.Allow()
	b.RecordFailure()
	b.Allow()
	b.RecordFailure()
	if b.State() != llmproxy.CircuitClosed {
		t.Errorf("state = %q, want closed — a success reset the failure count", b.State())
	}
}

func TestCircuitBreakerHalfOpenAdmitsOneProbe(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	b := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: 30 * time.Second, Now: clk.now}

	b.Allow()
	b.RecordFailure() // trips open

	if b.Allow() {
		t.Fatal("breaker admitted a request mid-cooldown")
	}
	clk.advance(30 * time.Second)

	if !b.Allow() {
		t.Fatal("breaker did not admit the half-open probe after the cooldown")
	}
	if b.State() != llmproxy.CircuitHalfOpen {
		t.Errorf("state = %q, want half_open while the probe is in flight", b.State())
	}
	if b.Allow() {
		t.Error("breaker admitted a second request while the half-open probe is in flight")
	}
}

func TestCircuitBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	b := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: 30 * time.Second, Now: clk.now}

	b.Allow()
	b.RecordFailure()
	clk.advance(30 * time.Second)
	b.Allow() // half-open probe
	b.RecordSuccess()

	if b.State() != llmproxy.CircuitClosed {
		t.Fatalf("state = %q, want closed after a successful probe", b.State())
	}
	if !b.Allow() {
		t.Error("closed breaker rejected a request after recovery")
	}
}

func TestCircuitBreakerHalfOpenProbeFailureReopens(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	b := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: 30 * time.Second, Now: clk.now}

	b.Allow()
	b.RecordFailure()
	clk.advance(30 * time.Second)
	b.Allow() // half-open probe
	b.RecordFailure()

	if b.State() != llmproxy.CircuitOpen {
		t.Fatalf("state = %q, want open after a failed probe", b.State())
	}
	// The cooldown resets: a request immediately after the failed probe
	// is rejected.
	if b.Allow() {
		t.Error("breaker admitted a request immediately after a failed probe")
	}
	clk.advance(30 * time.Second)
	if !b.Allow() {
		t.Error("breaker did not admit a probe after the reset cooldown elapsed")
	}
}

func TestCircuitBreakerStateReportsOpenUntilProbeAdmitted(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	b := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: 30 * time.Second, Now: clk.now}

	b.Allow()
	b.RecordFailure()
	clk.advance(time.Hour) // cooldown long past

	// State stays open until Allow admits the probe — half-open means a
	// probe is in progress, not merely that the cooldown elapsed.
	if b.State() != llmproxy.CircuitOpen {
		t.Errorf("state = %q, want open before any probe is admitted", b.State())
	}
}

func TestCircuitStateMetricValue(t *testing.T) {
	for _, tc := range []struct {
		state llmproxy.CircuitState
		want  int
	}{
		{llmproxy.CircuitClosed, 0},
		{llmproxy.CircuitHalfOpen, 1},
		{llmproxy.CircuitOpen, 2},
	} {
		if got := tc.state.MetricValue(); got != tc.want {
			t.Errorf("%q.MetricValue() = %d, want %d", tc.state, got, tc.want)
		}
	}
}

func TestCircuitBreakerZeroValueIsClosed(t *testing.T) {
	var b llmproxy.CircuitBreaker
	if b.State() != llmproxy.CircuitClosed {
		t.Errorf("zero-value breaker state = %q, want closed", b.State())
	}
	if !b.Allow() {
		t.Error("zero-value breaker rejected a request")
	}
}
