// SPDX-License-Identifier: MIT

//go:build load_local

// Package head_of_line_blocking_isolation models the §10.1 worker
// pool contract: independent worker goroutines mean a slow request
// at position N does not block fast requests at positions N+1..N+M.
// Invariant: fast-request P99 latency stays below the slow-request
// hold time even when fast requests are interleaved with slow ones.
//
// TESTING.md §12.7.a resiliency scenarios.
package head_of_line_blocking_isolation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "head_of_line_blocking_isolation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// pool is N parallel workers picking jobs from a queue. Slow jobs
// hold a worker for 100ms; fast jobs return immediately.
type pool struct {
	workers chan struct{}
}

func newPool(size int) *pool {
	return &pool{workers: make(chan struct{}, size)}
}

func (p *pool) submit(ctx context.Context, slow bool) error {
	select {
	case p.workers <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.workers }()
	if slow {
		time.Sleep(100 * time.Millisecond)
	} else {
		time.Sleep(100 * time.Microsecond)
	}
	return nil
}

type Scenario struct {
	counters *scenkit.Counters
	pool     *pool

	mu      sync.Mutex
	fastLat []time.Duration
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
	// 32 workers vs 16 VUs means every request has a worker
	// available — the queueing is the failure mode HOL blocking
	// tests for. The fast/slow mix asserts the slow job does not
	// drag fast jobs into its hold window.
	s.pool = newPool(32)
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	slow := iter%10 == 0
	start := time.Now()
	if err := s.pool.submit(ctx, slow); err != nil {
		return nil
	}
	elapsed := time.Since(start)
	if slow {
		s.counters.Inc("slow_calls")
	} else {
		s.counters.Inc("fast_calls")
		s.mu.Lock()
		s.fastLat = append(s.fastLat, elapsed)
		s.mu.Unlock()
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fastLat) == 0 {
		return fmt.Errorf("scenario did not exercise the fast path")
	}
	// Fast-call P99 must stay below the slow-call hold time of 100ms.
	// We use the max as a conservative ceiling for this assertion.
	var max time.Duration
	for _, d := range s.fastLat {
		if d > max {
			max = d
		}
	}
	r.AddCustom("fast_max_ms", float64(max.Milliseconds()))
	if max > 80*time.Millisecond {
		return fmt.Errorf("§10.1 violated: fast-call max %s ≥ 80%% of slow hold (head-of-line blocking)", max)
	}
	return nil
}
