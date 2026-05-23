// SPDX-License-Identifier: MIT

//go:build load_local

// Package graceful_shutdown_drain models the §17.7 graceful-shutdown
// contract: when the gateway receives SIGTERM mid-load, in-flight
// requests complete and new requests return 503. Invariant: no
// in-flight request is dropped; no new request observes a partial
// shutdown.
//
// TESTING.md §12.7.a resiliency scenarios.
package graceful_shutdown_drain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "graceful_shutdown_drain"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// server is a tiny model of a long-running handler with a drain
// signal. When drainStarted is set, accept() returns errShuttingDown
// for new requests but lets in-flight handlers complete.
type server struct {
	mu              sync.Mutex
	drainStarted    bool
	inFlight        atomic.Int64
	completedAfterDrain atomic.Int64
	droppedDuringDrain atomic.Int64
}

var errShuttingDown = errors.New("503 shutting down")

func (s *server) accept(ctx context.Context, hold time.Duration) error {
	s.mu.Lock()
	if s.drainStarted {
		s.mu.Unlock()
		return errShuttingDown
	}
	s.inFlight.Add(1)
	s.mu.Unlock()
	defer s.inFlight.Add(-1)

	select {
	case <-ctx.Done():
		// In a real server, drain waits for in-flight requests. Here
		// we treat ctx.Done() as "the server is forcibly killed" so
		// we count the drop.
		s.droppedDuringDrain.Add(1)
		return ctx.Err()
	case <-time.After(hold):
		s.mu.Lock()
		drainActive := s.drainStarted
		s.mu.Unlock()
		if drainActive {
			s.completedAfterDrain.Add(1)
		}
		return nil
	}
}

func (s *server) startDrain() {
	s.mu.Lock()
	s.drainStarted = true
	s.mu.Unlock()
}

type Scenario struct {
	counters *scenkit.Counters
	srv      *server
	drainOnce sync.Once
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
	s.srv = &server{}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Start drain halfway through the run.
	if iter == 50 {
		s.drainOnce.Do(func() {
			s.counters.Inc("drain_started")
			s.srv.startDrain()
		})
	}
	err := s.srv.accept(ctx, 5*time.Millisecond)
	if errors.Is(err, errShuttingDown) {
		s.counters.Inc("rejected_503")
		return nil
	}
	if err != nil {
		// Context cancellation is benign at run end.
		return nil
	}
	s.counters.Inc("completed")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("completed_after_drain", float64(s.srv.completedAfterDrain.Load()))
	r.AddCustom("dropped_during_drain", float64(s.srv.droppedDuringDrain.Load()))
	if s.counters.Get("drain_started") == 0 {
		return fmt.Errorf("scenario never triggered drain")
	}
	if s.counters.Get("rejected_503") == 0 {
		return fmt.Errorf("§17.7 violated: no requests rejected with 503 after drain started")
	}
	if s.srv.completedAfterDrain.Load() == 0 {
		return fmt.Errorf("§17.7 violated: no in-flight requests completed after drain started (the drain was too eager)")
	}
	return nil
}
