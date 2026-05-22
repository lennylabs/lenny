// SPDX-License-Identifier: MIT

//go:build load_local

// Package circuit_breaker_state_machine exercises a circuit-breaker
// state machine modelled in scenario-local code, mirroring the §11.6
// closed→open→half-open→closed lifecycle. Wave 3 minimum: a
// scenario-internal breaker validates the loadgen harness can carry
// state-machine scenarios under -race. When pkg/middleware/circuitbreaker
// lands in the tree this scenario rewires to drive the real package.
//
// TESTING.md §12.7.a regression scenarios.
package circuit_breaker_state_machine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

func init() {
	loadgen.Register("circuit_breaker_state_machine", func() loadgen.Scenario { return &Scenario{} })
}

type state int

const (
	closed state = iota
	open
	halfOpen
)

type breaker struct {
	mu                sync.Mutex
	st                state
	consecutiveFails  int
	failThreshold     int
	openExpiresAt     time.Time
	cooldown          time.Duration
}

func (b *breaker) call(ok bool, now time.Time) (allowed bool, transition string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.st == open {
		if now.Before(b.openExpiresAt) {
			return false, ""
		}
		b.st = halfOpen
		transition = "open->half_open"
	}
	if !ok {
		b.consecutiveFails++
		if b.st == halfOpen {
			b.st = open
			b.openExpiresAt = now.Add(b.cooldown)
			return false, transition + ",half_open->open"
		}
		if b.consecutiveFails >= b.failThreshold {
			b.st = open
			b.openExpiresAt = now.Add(b.cooldown)
			transition = transition + ",closed->open"
		}
		return false, transition
	}
	b.consecutiveFails = 0
	if b.st == halfOpen {
		b.st = closed
		transition = transition + ",half_open->closed"
	}
	return true, transition
}

type Scenario struct {
	br *breaker

	mu          sync.Mutex
	transitions []string

	closedAllows atomic.Int64
	openDenies   atomic.Int64
	invariantBad atomic.Int64
}

func (s *Scenario) Name() string { return "circuit_breaker_state_machine" }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.br = &breaker{failThreshold: 5, cooldown: 50 * time.Millisecond}
	s.transitions = make([]string, 0, 16)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Inject occasional failures so the breaker exercises all states.
	ok := (iter % 7) != 0
	allowed, transition := s.br.call(ok, time.Now())
	if transition != "" {
		s.mu.Lock()
		if len(s.transitions) < 32 {
			s.transitions = append(s.transitions, transition)
		}
		s.mu.Unlock()
	}
	switch {
	case allowed:
		s.closedAllows.Add(1)
	default:
		s.openDenies.Add(1)
	}
	// Invariant: a transition string must be one of the legal arrows.
	if transition != "" && !legalTransitions[transition] {
		// Compound transitions like "open->half_open,closed->open" are
		// allowed by the model; check each comma-separated segment.
		for _, seg := range splitCSV(transition) {
			if seg == "" {
				continue
			}
			if !legalTransitions[seg] {
				s.invariantBad.Add(1)
				return fmt.Errorf("§11.6 violated: illegal transition %q", seg)
			}
		}
	}
	return nil
}

var legalTransitions = map[string]bool{
	"closed->open":     true,
	"open->half_open":  true,
	"half_open->open":  true,
	"half_open->closed": true,
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("closed_allows", float64(s.closedAllows.Load()))
	r.AddCustom("open_denies", float64(s.openDenies.Load()))
	r.AddCustom("invariant_bad", float64(s.invariantBad.Load()))
	if s.invariantBad.Load() > 0 {
		return fmt.Errorf("§11.6 violated: %d illegal transitions", s.invariantBad.Load())
	}
	if s.closedAllows.Load() == 0 || s.openDenies.Load() == 0 {
		return fmt.Errorf("scenario must exercise both allow and deny paths: allows=%d denies=%d", s.closedAllows.Load(), s.openDenies.Load())
	}
	return nil
}
