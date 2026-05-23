// SPDX-License-Identifier: MIT

//go:build load_local

// Package client_disconnect_mid_stream models the §15.5 streaming
// disconnect contract: when the client cancels mid-stream, the
// backend cleans up the corresponding session goroutine promptly.
// Invariant: in-flight session count returns to baseline after the
// disconnect storm.
//
// TESTING.md §12.7.a resiliency scenarios.
package client_disconnect_mid_stream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "client_disconnect_mid_stream"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// streamer simulates a server-side streaming handler. It blocks
// until ctx is done, at which point the cleanup runs and inFlight
// decrements.
type streamer struct {
	inFlight atomic.Int64
	wg       sync.WaitGroup
}

func (s *streamer) stream(ctx context.Context) {
	s.inFlight.Add(1)
	s.wg.Add(1)
	defer func() { s.inFlight.Add(-1); s.wg.Done() }()
	<-ctx.Done()
}

type Scenario struct {
	counters *scenkit.Counters
	srv      *streamer
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
	s.srv = &streamer{}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error {
	// Wait for goroutines to drain on Teardown so the leak check is
	// meaningful.
	s.srv.wg.Wait()
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Open a stream with a short deadline and let it expire — the
	// "client disconnect" path.
	cctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	s.srv.stream(cctx)
	s.counters.Inc("disconnects")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	// Brief settle window for in-flight cleanups.
	time.Sleep(50 * time.Millisecond)
	s.counters.EmitTo(r)
	r.AddCustom("residual_in_flight", float64(s.srv.inFlight.Load()))
	if s.counters.Get("disconnects") == 0 {
		return fmt.Errorf("scenario did not exercise the disconnect path")
	}
	if v := s.srv.inFlight.Load(); v > 0 {
		return fmt.Errorf("§15.5 violated: %d residual in-flight streams after disconnect storm", v)
	}
	return nil
}
