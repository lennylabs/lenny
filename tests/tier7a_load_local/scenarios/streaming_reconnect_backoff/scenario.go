// SPDX-License-Identifier: MIT

//go:build load_local

// Package streaming_reconnect_backoff models the §15.5 reconnect
// backoff schedule: clients re-establish a stream with exponential
// delays. Invariant: across N reconnect attempts, the cumulative
// wait time is at least the sum of the documented exponential
// schedule.
//
// TESTING.md §12.7.a resiliency scenarios.
package streaming_reconnect_backoff

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "streaming_reconnect_backoff"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// reconnect computes the backoff delay for the Nth attempt. The
// documented schedule: 10ms, 20ms, 40ms, ..., capped at 1s.
func reconnectDelay(attempt int) time.Duration {
	delay := 10 * time.Millisecond * (1 << attempt)
	if delay > time.Second {
		delay = time.Second
	}
	return delay
}

type Scenario struct {
	counters *scenkit.Counters
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 8, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 4, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error    { return nil }
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Walk three reconnect attempts; observe the documented schedule.
	totalWait := time.Duration(0)
	start := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		delay := reconnectDelay(attempt)
		totalWait += delay
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil
		}
	}
	elapsed := time.Since(start)
	// Expected sum: 10 + 20 + 40 = 70ms; allow a small overhead.
	if elapsed < 70*time.Millisecond {
		s.counters.Inc("too_fast")
		return fmt.Errorf("§15.5 violated: 3 reconnects completed in %s (< 70ms minimum)", elapsed)
	}
	s.counters.Inc("backoff_observed")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("too_fast"); v > 0 {
		return fmt.Errorf("§15.5 violated: %d reconnect cycles completed faster than the documented schedule", v)
	}
	if s.counters.Get("backoff_observed") == 0 {
		return fmt.Errorf("scenario did not observe any backoff cycles")
	}
	return nil
}
