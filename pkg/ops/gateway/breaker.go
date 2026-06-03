// SPDX-License-Identifier: MIT

package gateway

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is recorded on a ReplicaResult when the per-replica
// circuit breaker has tripped for that endpoint and the fan-out skips
// it. The aggregation layer counts a skipped replica toward the §25.2
// "based on N of M replicas" degradation envelope.
var ErrCircuitOpen = errors.New("gateway client: replica circuit breaker open")

// CircuitBreaker implements the §25.4 per-replica circuit breaker for
// the fan-out path: a replica that fails FailureThreshold consecutive
// fan-out requests is skipped for ResetAfter so one struggling replica
// does not slow every aggregation. State is keyed by endpoint URL.
//
// spec: §25.4 "Fallback Caching" — per-replica circuit breakers
// (ops.gateway.fanOutCircuitBreaker.{failureThreshold, resetAfter}).
type CircuitBreaker struct {
	threshold int
	resetFor  time.Duration
	now       func() time.Time

	mu    sync.Mutex
	state map[string]*breakerState
}

type breakerState struct {
	consecutiveFailures int
	openUntil           time.Time
}

// NewCircuitBreaker returns a breaker that trips after threshold
// consecutive failures and stays open for resetAfter. A non-positive
// threshold defaults to 3 and a non-positive resetAfter defaults to
// 60s (§25.4 defaults).
func NewCircuitBreaker(threshold int, resetAfter time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	if resetAfter <= 0 {
		resetAfter = 60 * time.Second
	}
	return &CircuitBreaker{
		threshold: threshold,
		resetFor:  resetAfter,
		now:       time.Now,
		state:     make(map[string]*breakerState),
	}
}

// Allow reports whether a fan-out request to endpoint may proceed. It
// returns false while the breaker is open and the reset window has not
// elapsed.
func (b *CircuitBreaker) Allow(endpoint string) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state[endpoint]
	if st == nil {
		return true
	}
	if st.openUntil.IsZero() {
		return true
	}
	return !b.now().Before(st.openUntil)
}

// RecordSuccess clears the consecutive-failure count and closes the
// breaker for endpoint.
func (b *CircuitBreaker) RecordSuccess(endpoint string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, endpoint)
}

// RecordFailure increments the consecutive-failure count for endpoint
// and opens the breaker (for ResetAfter) once the threshold is reached.
func (b *CircuitBreaker) RecordFailure(endpoint string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state[endpoint]
	if st == nil {
		st = &breakerState{}
		b.state[endpoint] = st
	}
	st.consecutiveFailures++
	if st.consecutiveFailures >= b.threshold {
		st.openUntil = b.now().Add(b.resetFor)
	}
}
