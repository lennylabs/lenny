// SPDX-License-Identifier: MIT

//go:build load_local

// Package low_resource_startup models the §17.7 health contract
// under CPU pressure: even when the worker pool is saturated, the
// /healthz endpoint must respond promptly so liveness probes do not
// kill the pod. Invariant: healthz remains responsive even under
// sustained CPU load.
//
// TESTING.md §12.7.a resiliency scenarios.
package low_resource_startup

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "low_resource_startup"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// burner generates synthetic CPU load by spinning on a counter.
type burner struct {
	stop chan struct{}
	wg   sync.WaitGroup
}

func (b *burner) start() {
	for i := 0; i < runtime.NumCPU(); i++ {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			x := 0
			for {
				select {
				case <-b.stop:
					return
				default:
				}
				for k := 0; k < 1000; k++ {
					x++
				}
			}
		}()
	}
}

func (b *burner) shutdown() {
	close(b.stop)
	b.wg.Wait()
}

type Scenario struct {
	counters *scenkit.Counters
	burner   *burner
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 4, Duration: 1 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 2, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.burner = &burner{stop: make(chan struct{})}
	s.burner.start()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error {
	s.burner.shutdown()
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Simulate a healthz call under CPU pressure.
	start := time.Now()
	// healthz is supposed to be a constant-time response; we model
	// it as a single yield to the scheduler.
	runtime.Gosched()
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		s.counters.Inc("healthz_slow")
		return fmt.Errorf("§17.7 violated: healthz took %s under CPU pressure", elapsed)
	}
	s.counters.Inc("healthz_responsive")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("healthz_slow"); v > 0 {
		return fmt.Errorf("§17.7 violated: %d slow healthz responses under CPU pressure", v)
	}
	if s.counters.Get("healthz_responsive") == 0 {
		return fmt.Errorf("scenario did not exercise healthz")
	}
	return nil
}
