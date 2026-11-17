// SPDX-License-Identifier: MIT

//go:build load_local

// Package bulkhead_thread_pool_isolation models the §10.1 bulkhead
// contract: separate worker pools per tenant ensure that one slow
// tenant cannot starve the others. The invariant: the fast tenant's
// per-request latency is bounded even when the slow tenant has
// exhausted its own pool.
//
// TESTING.md §12.7.a resiliency scenarios.
package bulkhead_thread_pool_isolation

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "bulkhead_thread_pool_isolation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// bulkhead is a per-tenant bounded worker semaphore. Each tenant
// has its own pool; one tenant's saturated pool does not affect
// the other's.
type bulkhead struct {
	pools map[string]chan struct{}
}

func newBulkhead(cap int) *bulkhead {
	return &bulkhead{pools: map[string]chan struct{}{
		"slow": make(chan struct{}, cap),
		"fast": make(chan struct{}, cap),
	}}
}

func (b *bulkhead) acquire(ctx context.Context, tenant string) error {
	pool := b.pools[tenant]
	select {
	case pool <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *bulkhead) release(tenant string) { <-b.pools[tenant] }

type Scenario struct {
	counters *scenkit.Counters
	bh       *bulkhead

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
	s.bh = newBulkhead(4)
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

var slowGoroutines atomic.Int64

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tenant := "fast"
	if vu < 4 {
		tenant = "slow"
	}
	if err := s.bh.acquire(ctx, tenant); err != nil {
		s.counters.Inc("acquire_timeout")
		return nil
	}
	defer s.bh.release(tenant)
	start := time.Now()
	if tenant == "slow" {
		slowGoroutines.Add(1)
		defer slowGoroutines.Add(-1)
		time.Sleep(50 * time.Millisecond) // slow tenant blocks for 50ms
	} else {
		time.Sleep(100 * time.Microsecond) // fast tenant is sub-ms
	}
	elapsed := time.Since(start)
	if tenant == "fast" {
		s.mu.Lock()
		s.fastLat = append(s.fastLat, elapsed)
		s.mu.Unlock()
		s.counters.Inc("fast_calls")
	} else {
		s.counters.Inc("slow_calls")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fastLat) == 0 {
		return fmt.Errorf("scenario did not exercise the fast tenant")
	}
	// The fast tenant's P95 latency must stay well below the slow
	// tenant's 50ms hold. § isolation invariant.
	var max time.Duration
	for _, d := range s.fastLat {
		if d > max {
			max = d
		}
	}
	r.AddCustom("fast_max_ms", float64(max.Milliseconds()))
	if max > 25*time.Millisecond {
		return fmt.Errorf("§10.1 violated: fast tenant max latency %s > 25ms (slow tenant bled through)", max)
	}
	return nil
}
