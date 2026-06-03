// SPDX-License-Identifier: MIT

package gateway

import (
	"testing"
	"time"
)

// TestCircuitBreaker_OpensAfterThreshold covers the §25.4 per-replica
// circuit breaker: FailureThreshold consecutive failures open the
// breaker, which stays open for ResetAfter and then closes.
func TestCircuitBreaker_OpensAfterThreshold_spec_25_4(t *testing.T) {
	now := time.Unix(1_000, 0)
	b := NewCircuitBreaker(3, 60*time.Second)
	b.now = func() time.Time { return now }

	const ep = "https://10.0.0.1:8443"
	if !b.Allow(ep) {
		t.Fatal("a fresh endpoint must be allowed")
	}
	b.RecordFailure(ep)
	b.RecordFailure(ep)
	if !b.Allow(ep) {
		t.Fatal("breaker opened before reaching the threshold")
	}
	b.RecordFailure(ep) // third consecutive failure trips it
	if b.Allow(ep) {
		t.Fatal("breaker should be open after 3 consecutive failures")
	}
	// Still open just before the reset window elapses.
	now = now.Add(59 * time.Second)
	if b.Allow(ep) {
		t.Fatal("breaker should still be open before resetAfter elapses")
	}
	// Closes once the reset window passes.
	now = now.Add(2 * time.Second)
	if !b.Allow(ep) {
		t.Fatal("breaker should close after resetAfter elapses")
	}
}

// TestCircuitBreaker_SuccessResets covers that a success clears the
// consecutive-failure count so the breaker does not trip on
// non-consecutive failures.
func TestCircuitBreaker_SuccessResets(t *testing.T) {
	b := NewCircuitBreaker(3, time.Minute)
	const ep = "https://10.0.0.2:8443"
	b.RecordFailure(ep)
	b.RecordFailure(ep)
	b.RecordSuccess(ep)
	b.RecordFailure(ep)
	b.RecordFailure(ep)
	if !b.Allow(ep) {
		t.Fatal("non-consecutive failures must not open the breaker")
	}
}

// TestCircuitBreaker_NilSafe covers the nil-breaker path (circuit
// breaking disabled): every endpoint is always allowed.
func TestCircuitBreaker_NilSafe(t *testing.T) {
	var b *CircuitBreaker
	if !b.Allow("https://10.0.0.3:8443") {
		t.Fatal("a nil breaker must allow every endpoint")
	}
	b.RecordFailure("x") // must not panic
	b.RecordSuccess("x")
}

// TestCircuitBreaker_Defaults covers the §25.4 default threshold (3) and
// reset window (60s) when non-positive values are supplied.
func TestCircuitBreaker_Defaults(t *testing.T) {
	b := NewCircuitBreaker(0, 0)
	if b.threshold != 3 || b.resetFor != 60*time.Second {
		t.Fatalf("defaults = (%d, %s), want (3, 60s)", b.threshold, b.resetFor)
	}
}
