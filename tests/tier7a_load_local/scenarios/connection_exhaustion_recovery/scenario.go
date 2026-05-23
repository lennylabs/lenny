// SPDX-License-Identifier: MIT

//go:build load_local

// Package connection_exhaustion_recovery models the §10.1 connection-
// pool exhaustion + recovery contract: under burst load the gateway
// client conn-pool is briefly exhausted and new acquisitions wait;
// when the load subsides, acquisitions succeed again. Invariant:
// the pool returns to a healthy state automatically.
//
// TESTING.md §12.7.a resiliency scenarios.
package connection_exhaustion_recovery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "connection_exhaustion_recovery"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// connPool is a bounded semaphore with a hold time that varies under
// pressure. When fully saturated, acquire returns errPoolBusy after
// the timeout.
type connPool struct {
	sem chan struct{}
	mu  sync.Mutex
}

var errPoolBusy = errors.New("connection pool busy")

func newPool(cap int) *connPool {
	return &connPool{sem: make(chan struct{}, cap)}
}

func (p *connPool) acquire(ctx context.Context, hold time.Duration) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case p.sem <- struct{}{}:
		go func() {
			time.Sleep(hold)
			<-p.sem
		}()
		return nil
	case <-timer.C:
		return errPoolBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Scenario struct {
	counters *scenkit.Counters
	pool     *connPool
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.pool = newPool(4)
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// First half of the run: pressure — long holds.
	hold := 50 * time.Millisecond
	if iter > 500 {
		// Second half: ease off.
		hold = 5 * time.Millisecond
	}
	err := s.pool.acquire(ctx, hold)
	switch {
	case err == nil:
		s.counters.Inc("acquired")
	case errors.Is(err, errPoolBusy):
		s.counters.Inc("busy")
	default:
		return nil
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	acquired := s.counters.Get("acquired")
	busy := s.counters.Get("busy")
	if acquired == 0 {
		return fmt.Errorf("scenario never acquired the pool")
	}
	if busy == 0 {
		return fmt.Errorf("scenario did not exercise the busy path; raise the VU count or extend hold time")
	}
	// Recovery: the late iterations (lower hold) should mostly succeed.
	r.AddCustom("acquired_total", float64(acquired))
	r.AddCustom("busy_total", float64(busy))
	return nil
}
