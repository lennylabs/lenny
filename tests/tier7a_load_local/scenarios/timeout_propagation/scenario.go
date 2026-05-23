// SPDX-License-Identifier: MIT

//go:build load_local

// Package timeout_propagation models the §10.1 timeout-propagation
// contract: a request with deadline T propagates that deadline to
// every downstream call, so the total request lifetime is bounded
// by T regardless of how many downstream calls it makes. Invariant:
// no goroutine outlives the request context.
//
// TESTING.md §12.7.a resiliency scenarios.
package timeout_propagation

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "timeout_propagation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// downstreamCall waits until ctx is done or 200ms elapses (whichever
// is first). Models a downstream that's slower than typical request
// budgets — the deadline must short-circuit the wait.
func downstreamCall(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

type Scenario struct {
	counters    *scenkit.Counters
	overshoots  atomic.Int64
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
	// Set a 30ms budget. The downstream would take 200ms if unbound;
	// timeout propagation must cancel it well within 50ms wall.
	cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = downstreamCall(cctx)
	elapsed := time.Since(start)
	if elapsed > 60*time.Millisecond {
		s.overshoots.Add(1)
		s.counters.Inc("overshoots")
		return fmt.Errorf("§10.1 violated: call elapsed %s past the 30ms budget (+ slack)", elapsed)
	}
	s.counters.Inc("bounded_calls")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.overshoots.Load(); v > 0 {
		return fmt.Errorf("§10.1 violated: %d calls overshot the deadline", v)
	}
	if s.counters.Get("bounded_calls") == 0 {
		return fmt.Errorf("scenario did not exercise the bounded path")
	}
	return nil
}
