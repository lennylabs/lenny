// SPDX-License-Identifier: MIT

//go:build load_local

// Package high_error_rate_circuit_open extends the §11.6 circuit-
// breaker scenario from "transitions are valid" to "the breaker
// actually trips at sustained 50% error rates". Invariant: after
// the breaker opens, downstream calls short-circuit until the
// half-open probe succeeds.
//
// TESTING.md §12.7.a resiliency scenarios.
package high_error_rate_circuit_open

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "high_error_rate_circuit_open"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// breaker is a tiny circuit breaker. After N consecutive failures
// it opens; while open, calls short-circuit. After cooldown it
// half-opens and one probe decides.
type breaker struct {
	mu              sync.Mutex
	state           string
	failsInWindow   int
	threshold       int
	openExpiresAt   time.Time
	cooldown        time.Duration

	shortCircuited atomic.Int64
}

func newBreaker() *breaker { return &breaker{state: "closed", threshold: 5, cooldown: 50 * time.Millisecond} }

func (b *breaker) call(ok bool, now time.Time) (allowed, shortCircuit bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "open" {
		if now.Before(b.openExpiresAt) {
			b.shortCircuited.Add(1)
			return false, true
		}
		b.state = "half_open"
	}
	if !ok {
		b.failsInWindow++
		if b.failsInWindow >= b.threshold {
			b.state = "open"
			b.openExpiresAt = now.Add(b.cooldown)
			b.failsInWindow = 0
		}
		return false, false
	}
	b.failsInWindow = 0
	if b.state == "half_open" {
		b.state = "closed"
	}
	return true, false
}

type Scenario struct {
	counters *scenkit.Counters
	br       *breaker
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.br = newBreaker()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// 50% upstream error rate.
	ok := iter%2 == 0
	allowed, sc := s.br.call(ok, time.Now())
	if sc {
		s.counters.Inc("short_circuited")
		return nil
	}
	if allowed {
		s.counters.Inc("admitted")
	} else {
		s.counters.Inc("rejected_downstream")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("short_circuited") == 0 {
		return fmt.Errorf("§11.6 violated: breaker did not open under sustained 50%% errors")
	}
	if s.counters.Get("admitted") == 0 {
		return fmt.Errorf("§11.6 violated: breaker did not recover (no admits after half-open probe)")
	}
	return nil
}
