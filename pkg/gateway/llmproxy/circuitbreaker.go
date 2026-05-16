// SPDX-License-Identifier: MIT

package llmproxy

import (
	"sync"
	"time"
)

// defaultFailureThreshold is the consecutive-failure count that trips
// the LLM Proxy circuit breaker open when none is configured.
const defaultFailureThreshold = 5

// defaultCooldown is the §4.9 llmProxy.circuitBreaker.halfOpenInterval
// default: how long the breaker stays open before admitting a probe.
const defaultCooldown = 30 * time.Second

// CircuitState is the §4.9 LLM Proxy circuit-breaker state.
type CircuitState string

const (
	// CircuitClosed admits every request; the upstream is healthy.
	CircuitClosed CircuitState = "closed"
	// CircuitHalfOpen admits one probe request after the open cooldown.
	CircuitHalfOpen CircuitState = "half_open"
	// CircuitOpen rejects every request; the upstream is failing.
	CircuitOpen CircuitState = "open"
)

// MetricValue maps the state to the §16.1
// lenny_gateway_subsystem_circuit_state{subsystem="llm_proxy"} gauge
// value: 0 closed, 1 half-open, 2 open.
func (s CircuitState) MetricValue() int {
	switch s {
	case CircuitHalfOpen:
		return 1
	case CircuitOpen:
		return 2
	default:
		return 0
	}
}

// CircuitBreaker is the §4.9 LLM Proxy circuit breaker around an
// upstream LLM provider. Consecutive upstream failures trip it from
// closed to open; while open it rejects every request so the proxy
// returns PROVIDER_UNAVAILABLE without hanging on a doomed upstream
// call. After the cooldown it admits a single half-open probe whose
// outcome closes the breaker or reopens it and resets the cooldown.
//
// The breaker is goroutine-safe. In-flight streams established before
// the breaker opened are unaffected: the breaker gates only the
// admission decision for new requests.
type CircuitBreaker struct {
	// FailureThreshold is the count of consecutive failures that trips
	// the breaker from closed to open. Zero selects the default.
	FailureThreshold int
	// Cooldown is the time the breaker stays open before admitting a
	// probe. Zero selects the §4.9 default of 30s.
	Cooldown time.Duration
	// Now returns the current time. Zero selects time.Now; tests
	// substitute a controllable clock.
	Now func() time.Time

	mu              sync.Mutex
	state           CircuitState
	consecutiveFail int
	openedAt        time.Time
	probeInFlight   bool
}

// Allow reports whether a new upstream request may proceed and records
// the admission. A closed breaker always admits. An open breaker admits
// nothing until the cooldown elapses, at which point it moves to
// half-open and admits exactly one probe. A half-open breaker admits no
// further requests until the probe's outcome is recorded.
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "" {
		b.state = CircuitClosed
	}
	switch b.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if b.now().Sub(b.openedAt) < b.cooldown() {
			return false
		}
		// Cooldown elapsed: move to half-open and admit one probe.
		b.state = CircuitHalfOpen
		b.probeInFlight = true
		return true
	case CircuitHalfOpen:
		if b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		return true
	default:
		return false
	}
}

// RecordSuccess reports that an admitted request reached the upstream
// successfully. It clears the consecutive-failure count and, when the
// breaker was half-open, closes it.
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail = 0
	if b.state == CircuitHalfOpen {
		b.state = CircuitClosed
	}
	b.probeInFlight = false
}

// RecordFailure reports that an admitted request failed against the
// upstream. A closed breaker trips open once consecutive failures reach
// the threshold; a half-open probe failure reopens the breaker and
// resets the cooldown.
func (b *CircuitBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case CircuitHalfOpen:
		b.trip()
	default:
		b.consecutiveFail++
		if b.consecutiveFail >= b.threshold() {
			b.trip()
		}
	}
}

// State returns the breaker's current state. An open breaker whose
// cooldown has elapsed is still reported as open until Allow admits the
// half-open probe — the half-open state means a probe is in progress,
// not merely that the cooldown has passed.
func (b *CircuitBreaker) State() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "" {
		return CircuitClosed
	}
	return b.state
}

// trip opens the breaker and resets the cooldown. The caller holds b.mu.
func (b *CircuitBreaker) trip() {
	b.state = CircuitOpen
	b.openedAt = b.now()
	b.consecutiveFail = 0
	b.probeInFlight = false
}

func (b *CircuitBreaker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *CircuitBreaker) threshold() int {
	if b.FailureThreshold > 0 {
		return b.FailureThreshold
	}
	return defaultFailureThreshold
}

func (b *CircuitBreaker) cooldown() time.Duration {
	if b.Cooldown > 0 {
		return b.Cooldown
	}
	return defaultCooldown
}
