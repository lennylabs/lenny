// SPDX-License-Identifier: MIT

// Package subsystem provides the shared concurrency primitives the
// §4.1 gateway subsystem boundaries enforce: an in-memory circuit
// breaker and a max-concurrent semaphore (Limiter), wired together
// into a Subsystem value whose Do method gates handler execution.
//
// The §4.1 partial-degradation contract requires that a problem in one
// subsystem (e.g., a slow upload) cannot starve or crash another
// (e.g., MCP streaming). Each subsystem owns its own Breaker and
// Limiter so a saturated Upload Handler cannot consume goroutines
// needed by the Stream Proxy, and a failing upstream tripping the
// Upload Handler breaker does not affect the Stream Proxy or MCP
// Fabric. Per-subsystem metrics (queue depth, circuit state, error
// counters) are emitted from this package so the §16.5 alerts and
// dashboards observe each subsystem independently.
//
// The breakers in this package are per-replica and in-memory; the
// operator-managed §11.6 platform-wide circuit breakers (Redis-backed,
// cross-replica) are a separate surface — see §4.1 line 115.
package subsystem

import (
	"sync"
	"time"
)

// State is the §4.1 per-subsystem circuit-breaker state.
type State string

const (
	// StateClosed admits every request; the subsystem is healthy.
	StateClosed State = "closed"
	// StateHalfOpen admits exactly one probe after the open cooldown
	// has elapsed.
	StateHalfOpen State = "half_open"
	// StateOpen rejects every request; the subsystem is failing and
	// must drain.
	StateOpen State = "open"
)

// MetricValue maps the state to the §16.1
// lenny_gateway_subsystem_circuit_state{subsystem=…} gauge value:
// 0 closed, 1 half-open, 2 open.
func (s State) MetricValue() int {
	switch s {
	case StateHalfOpen:
		return 1
	case StateOpen:
		return 2
	default:
		return 0
	}
}

// Defaults for unset Breaker fields. The §4.1 spec leaves the per-
// subsystem values operator-tunable; these defaults are first-
// principles estimates aligned with the §4.9 LLM Proxy breaker the
// reference implementation already carries.
const (
	// DefaultFailureThreshold is the consecutive-failure count that
	// trips the breaker open when none is configured.
	DefaultFailureThreshold = 5
	// DefaultCooldown is the time the breaker stays open before
	// admitting a probe.
	DefaultCooldown = 30 * time.Second
)

// Breaker is the §4.1 per-subsystem in-memory circuit breaker. It
// gates the admission decision for new requests against a per-replica
// failure budget so a failing downstream dependency does not exhaust
// the subsystem's goroutines: consecutive failures trip it from
// closed to open; while open it rejects every request; after the
// cooldown it admits a single half-open probe whose outcome closes
// the breaker or reopens it and resets the cooldown.
//
// The breaker is goroutine-safe. In-flight requests established
// before the breaker opened are unaffected — the breaker gates only
// the admission decision for new requests.
//
// spec: §4.1 (per-subsystem circuit breaker)
type Breaker struct {
	// FailureThreshold is the count of consecutive failures that
	// trips the breaker from closed to open. Zero selects
	// DefaultFailureThreshold.
	FailureThreshold int
	// Cooldown is the time the breaker stays open before admitting a
	// probe. Zero selects DefaultCooldown.
	Cooldown time.Duration
	// Now returns the current time. Zero selects time.Now; tests
	// substitute a controllable clock.
	Now func() time.Time

	mu              sync.Mutex
	state           State
	consecutiveFail int
	openedAt        time.Time
	probeInFlight   bool
}

// Allow reports whether a new request may proceed and records the
// admission. A closed breaker always admits. An open breaker admits
// nothing until the cooldown elapses, at which point it moves to
// half-open and admits exactly one probe. A half-open breaker admits
// no further requests until the probe's outcome is recorded via
// RecordSuccess or RecordFailure.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "" {
		b.state = StateClosed
	}
	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if b.now().Sub(b.openedAt) < b.cooldown() {
			return false
		}
		// Cooldown elapsed: move to half-open and admit one probe.
		b.state = StateHalfOpen
		b.probeInFlight = true
		return true
	case StateHalfOpen:
		if b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		return true
	default:
		return false
	}
}

// RecordSuccess reports that an admitted request reached the
// dependency successfully. It clears the consecutive-failure count
// and, when the breaker was half-open, closes it.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail = 0
	if b.state == StateHalfOpen {
		b.state = StateClosed
	}
	b.probeInFlight = false
}

// RecordFailure reports that an admitted request failed against the
// dependency. A closed breaker trips open once consecutive failures
// reach the threshold; a half-open probe failure reopens the breaker
// and resets the cooldown.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateHalfOpen:
		b.trip()
	default:
		b.consecutiveFail++
		if b.consecutiveFail >= b.threshold() {
			b.trip()
		}
	}
}

// State returns the breaker's current state. An open breaker whose
// cooldown has elapsed is still reported as open until Allow admits
// the half-open probe — the half-open state means a probe is in
// progress, not merely that the cooldown has passed.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "" {
		return StateClosed
	}
	return b.state
}

// trip opens the breaker and resets the cooldown. The caller holds b.mu.
func (b *Breaker) trip() {
	b.state = StateOpen
	b.openedAt = b.now()
	b.consecutiveFail = 0
	b.probeInFlight = false
}

func (b *Breaker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Breaker) threshold() int {
	if b.FailureThreshold > 0 {
		return b.FailureThreshold
	}
	return DefaultFailureThreshold
}

func (b *Breaker) cooldown() time.Duration {
	if b.Cooldown > 0 {
		return b.Cooldown
	}
	return DefaultCooldown
}
