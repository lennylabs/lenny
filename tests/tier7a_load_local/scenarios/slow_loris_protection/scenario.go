// SPDX-License-Identifier: MIT

//go:build load_local

// Package slow_loris_protection models the §10.1 slow-client
// protection contract: a client that reads the body byte-by-byte
// with sleeps cannot tie up a worker indefinitely. Invariant: the
// gateway bounds total body-read time to LENNY_GATEWAY_BODY_READ_TIMEOUT
// (modelled as a constant here).
//
// TESTING.md §12.7.a resiliency scenarios.
package slow_loris_protection

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const (
	name             = "slow_loris_protection"
	readTimeout      = 100 * time.Millisecond
	worstCaseLatency = 250 * time.Millisecond
)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// handler reads the body with the configured timeout. A slow client
// that takes longer than readTimeout sees errReadTimeout instead of
// holding the worker.
var errReadTimeout = errors.New("read timeout")

func readBody(ctx context.Context, slowReader bool) error {
	timer := time.NewTimer(readTimeout)
	defer timer.Stop()
	holdFor := 10 * time.Millisecond
	if slowReader {
		holdFor = worstCaseLatency
	}
	select {
	case <-time.After(holdFor):
		return nil
	case <-timer.C:
		return errReadTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Scenario struct {
	counters   *scenkit.Counters
	overshoots atomic.Int64
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

func (s *Scenario) Setup(ctx context.Context) error    { return nil }
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	slow := iter%2 == 0
	start := time.Now()
	err := readBody(ctx, slow)
	elapsed := time.Since(start)
	if slow {
		// Slow client must be cut off at readTimeout.
		if errors.Is(err, errReadTimeout) && elapsed <= readTimeout+50*time.Millisecond {
			s.counters.Inc("slow_terminated")
			return nil
		}
		if elapsed > readTimeout+50*time.Millisecond {
			s.overshoots.Add(1)
			s.counters.Inc("slow_overshoot")
			return fmt.Errorf("§10.1 violated: slow read held worker for %s (> %s + slack)", elapsed, readTimeout)
		}
		return nil
	}
	// Fast client should succeed.
	if err == nil {
		s.counters.Inc("fast_completed")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.overshoots.Load(); v > 0 {
		return fmt.Errorf("§10.1 violated: %d slow-loris overshoots", v)
	}
	if s.counters.Get("slow_terminated") == 0 || s.counters.Get("fast_completed") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
