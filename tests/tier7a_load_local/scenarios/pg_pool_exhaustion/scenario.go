// SPDX-License-Identifier: MIT

//go:build load_local

// Package pg_pool_exhaustion models the §12.4 connection-pool
// backpressure path: when the pool is saturated, new acquisitions
// must observe a bounded wait and return a documented error rather
// than blocking forever. The scenario uses a scenario-local semaphore
// that mirrors `pgxpool.Pool` acquire semantics.
//
// TESTING.md §12.7.a multi-component scenarios.
package pg_pool_exhaustion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "pg_pool_exhaustion"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// pool is a tiny model of a bounded Postgres connection pool.
type pool struct {
	sem      chan struct{}
	maxWait  time.Duration
	holdTime time.Duration
}

func newPool(size int, hold time.Duration) *pool {
	return &pool{sem: make(chan struct{}, size), maxWait: 200 * time.Millisecond, holdTime: hold}
}

var errPoolBusy = errors.New("pg_pool: timed out waiting for connection")

func (p *pool) acquire(ctx context.Context) error {
	timer := time.NewTimer(p.maxWait)
	defer timer.Stop()
	select {
	case p.sem <- struct{}{}:
		// "Hold" the connection for the configured duration.
		go func() {
			time.Sleep(p.holdTime)
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
	pool     *pool
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	// 4-slot pool, 50ms hold time, so 24 concurrent acquirers will
	// observe backpressure regularly.
	s.pool = newPool(4, 50*time.Millisecond)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	err := s.pool.acquire(ctx)
	switch {
	case err == nil:
		s.counters.Inc("acquired")
	case errors.Is(err, errPoolBusy):
		s.counters.Inc("timed_out")
	default:
		s.counters.IncOnError(ctx, "errors", err)
		return err
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("acquired") == 0 {
		return fmt.Errorf("scenario never acquired the pool")
	}
	// At least some iterations must have observed backpressure for
	// the scenario to have actually exercised the §12.4 path.
	if s.counters.Get("timed_out") == 0 {
		return fmt.Errorf("scenario did not exercise pool exhaustion; raise the VU count or shrink the pool")
	}
	return nil
}
