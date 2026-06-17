// SPDX-License-Identifier: MIT

//go:build load_local

// Package slot_counter_race exercises pkg/gateway/slotcounter at high
// goroutine concurrency against miniredis.
//
// Regression source: commit 9b5ba3e replaced the previous SSA-based
// slot counter (which let N parallel reservers all observe the same
// value and all "succeed" past maxConcurrent) with an atomic Redis
// Lua GET-compare-INCR. This scenario re-creates that race surface
// and asserts no overcommit.
//
// TESTING.md §12.7.a regression scenarios.
package slot_counter_race

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

func init() {
	loadgen.Register("slot_counter_race", func() loadgen.Scenario { return &Scenario{} })
}

// Scenario runs many goroutines reserving on the same pod and asserts
// that exactly maxConcurrent successful Reserve calls are observed
// across the entire run, with no overcommit.
type Scenario struct {
	mr      *miniredis.Miniredis
	client  *redis.Client
	counter *slotcounter.Counter

	// podID is the synthetic pod every iteration reserves against.
	// The driver releases at the end of each iteration so the
	// counter cycles. The success/overcommit invariant is checked
	// per-cycle (resets each iteration's pod-bound counters).
	podID string

	// maxConcurrent is the cap the slot counter enforces.
	maxConcurrent int32

	// Per-iteration counters. The driver uses ConstantArrivalRate so
	// many iterations may overlap on the same pod; we count the
	// successful reservations in flight at any instant and assert
	// that count never exceeds maxConcurrent.
	mu           sync.Mutex
	inFlight     int32
	peakInFlight int32

	// Aggregate counters captured at Teardown for the Assert path.
	totalReserves    atomic.Int64
	totalRejects     atomic.Int64
	overcommitEvents atomic.Int64
}

func (s *Scenario) Name() string { return "slot_counter_race" }

// RampProfiles enumerates ascending VU counts for capacity discovery.
// Used when scaffolds_test.go runs under LENNY_TIER7A_CAPACITY=1.
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 25, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 50, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 100, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 200, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 400, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 800, Duration: 2 * time.Second},
	}
}

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{
		Kind:     loadgen.ConstantVU,
		VUs:      50,
		Duration: 3 * time.Second,
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	mr, err := miniredis.Run()
	if err != nil {
		return fmt.Errorf("miniredis start: %w", err)
	}
	s.mr = mr
	s.client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.counter = slotcounter.New(s.client)
	s.podID = "pod-race"
	s.maxConcurrent = 4
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.mr != nil {
		s.mr.Close()
	}
	return nil
}

// Run reserves a slot, holds it for a tiny window, and releases it.
// The window is just long enough that many goroutines race for the
// last slot. The inFlight counter is the assertion vector: if the
// slotcounter ever returns success past maxConcurrent, inFlight will
// momentarily exceed the cap and overcommitEvents will increment.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	n, _, err := s.counter.Reserve(ctx, s.podID, s.maxConcurrent)
	if err != nil {
		if errors.Is(err, slotcounter.ErrSlotsExhausted) {
			s.totalRejects.Add(1)
			return nil
		}
		return fmt.Errorf("Reserve: %w", err)
	}
	_ = n
	s.totalReserves.Add(1)
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.peakInFlight {
		s.peakInFlight = s.inFlight
	}
	if s.inFlight > s.maxConcurrent {
		s.overcommitEvents.Add(1)
	}
	s.mu.Unlock()

	// Hold the slot briefly so neighbouring goroutines have time to
	// see it occupied. A pure release-on-return would serialise the
	// race away.
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Microsecond):
	}

	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
	if _, err := s.counter.Release(ctx, s.podID); err != nil {
		return fmt.Errorf("Release: %w", err)
	}
	return nil
}

// Assert validates the §5.2 invariant: the slot counter never admits
// more than maxConcurrent simultaneous reservations. The SSA-based
// implementation that 9b5ba3e replaced would have driven
// overcommitEvents > 0 within milliseconds.
func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("reserves_succeeded", float64(s.totalReserves.Load()))
	r.AddCustom("reserves_rejected", float64(s.totalRejects.Load()))
	r.AddCustom("peak_in_flight", float64(s.peakInFlight))
	r.AddCustom("overcommit_events", float64(s.overcommitEvents.Load()))
	if s.overcommitEvents.Load() > 0 {
		return fmt.Errorf("§5.2 violated: %d overcommit events; peak in-flight = %d > maxConcurrent = %d",
			s.overcommitEvents.Load(), s.peakInFlight, s.maxConcurrent)
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}
	if s.totalReserves.Load() == 0 {
		return fmt.Errorf("scenario never observed a successful Reserve; load did not exercise the counter")
	}
	return nil
}
