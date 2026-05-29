// SPDX-License-Identifier: MIT

package sessionserver

import (
	"sync"
	"time"
)

// targetingBreakerParams are the §10.7 SCL-023 circuit-breaker
// thresholds resolved from a tenant's experimentTargeting config.
type targetingBreakerParams struct {
	threshold int
	window    time.Duration
	openDur   time.Duration
}

type breakerPhase int

const (
	breakerClosed breakerPhase = iota
	breakerOpen
	breakerHalfOpen
)

// targetingBreakerEntry is the per-(tenant, provider) breaker state.
type targetingBreakerEntry struct {
	phase breakerPhase
	// failTimes holds the timestamps of the consecutive failures inside
	// the current rolling window. A success clears it; each failure
	// appends and prunes entries older than the window. The breaker opens
	// once the slice length reaches the threshold.
	failTimes []time.Time
	openUntil time.Time
}

// targetingBreaker is the §10.7 SCL-023 per-tenant OpenFeature targeting
// circuit breaker (spec lines 835-844). It is consulted on the session-
// creation hot path: Allow reports whether an evaluation may proceed,
// and Record feeds the outcome back so sustained provider failures open
// the circuit and let the gateway skip the OpenFeature call entirely.
//
// State is keyed per (tenant_id, provider) to match the
// lenny_experiment_targeting_circuit_open gauge labels (§16.1 line 64).
// It is in-memory and per-replica; a replica restart resets it, matching
// the other per-replica breakers in the gateway.
type targetingBreaker struct {
	mu       sync.Mutex
	entries  map[string]*targetingBreakerEntry
	now      func() time.Time
	setGauge func(tenantID, provider string, open bool)
}

// newTargetingBreaker returns a breaker driven by now (defaulting to
// time.Now) that reports open/closed transitions through setGauge (nil
// disables the gauge emission).
func newTargetingBreaker(now func() time.Time, setGauge func(tenantID, provider string, open bool)) *targetingBreaker {
	if now == nil {
		now = time.Now
	}
	return &targetingBreaker{
		entries:  map[string]*targetingBreakerEntry{},
		now:      now,
		setGauge: setGauge,
	}
}

func breakerKey(tenantID, provider string) string { return tenantID + "\x00" + provider }

func (b *targetingBreaker) entryLocked(tenantID, provider string) *targetingBreakerEntry {
	k := breakerKey(tenantID, provider)
	e := b.entries[k]
	if e == nil {
		e = &targetingBreakerEntry{phase: breakerClosed}
		b.entries[k] = e
	}
	return e
}

func (b *targetingBreaker) emit(tenantID, provider string, open bool) {
	if b.setGauge != nil {
		b.setGauge(tenantID, provider, open)
	}
}

// Allow reports whether an OpenFeature evaluation may proceed for the
// (tenant, provider). It returns false while the breaker is open and the
// open window has not elapsed; the caller then skips the OpenFeature
// call entirely (§10.7 line 838). When the open window has elapsed the
// breaker transitions to half-open and admits a single probe.
func (b *targetingBreaker) Allow(tenantID, provider string) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entryLocked(tenantID, provider)
	switch e.phase {
	case breakerOpen:
		if !b.now().Before(e.openUntil) {
			// Open window elapsed: admit one half-open probe.
			e.phase = breakerHalfOpen
			return true
		}
		return false
	case breakerHalfOpen:
		// A probe is already outstanding; admit only one (§10.7 line 839).
		return false
	default:
		return true
	}
}

// Record feeds an evaluation outcome back into the breaker. A success
// closes a half-open breaker and clears the failure run; a failure
// re-arms the open window when a half-open probe fails, or extends the
// consecutive-failure run and opens the breaker once it reaches the
// threshold within the rolling window (§10.7 lines 837-839).
func (b *targetingBreaker) Record(tenantID, provider string, p targetingBreakerParams, success bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entryLocked(tenantID, provider)
	now := b.now()

	if success {
		e.failTimes = nil
		if e.phase != breakerClosed {
			e.phase = breakerClosed
			e.openUntil = time.Time{}
			b.emit(tenantID, provider, false)
		}
		return
	}

	switch e.phase {
	case breakerHalfOpen:
		// Probe failed: re-arm the 30s open window (§10.7 line 839).
		e.phase = breakerOpen
		e.openUntil = now.Add(p.openDur)
		e.failTimes = nil
		b.emit(tenantID, provider, true)
		return
	case breakerOpen:
		// A late failure arriving while already open is a no-op; the open
		// window governs recovery.
		return
	}

	// Closed: count consecutive failures within the rolling window.
	cutoff := now.Add(-p.window)
	kept := e.failTimes[:0]
	for _, t := range e.failTimes {
		if !t.Before(cutoff) {
			kept = append(kept, t)
		}
	}
	e.failTimes = append(kept, now)
	if len(e.failTimes) >= p.threshold {
		e.phase = breakerOpen
		e.openUntil = now.Add(p.openDur)
		e.failTimes = nil
		b.emit(tenantID, provider, true)
	}
}
