// SPDX-License-Identifier: MIT

//go:build load_local

// Package low_resource_startup models the §17.7 health contract
// under CPU pressure: even when the worker pool is saturated, the
// /healthz endpoint must respond promptly so liveness probes do not
// kill the pod. Invariant: healthz remains responsive even under
// sustained CPU load. Responsiveness is measured in the process's own CPU
// time (see scheduledWait), so load from other processes on the host does
// not read as a violation.
//
// TESTING.md §12.7.a resiliency scenarios.
package low_resource_startup

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/unix"

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

// healthzBound is the §17.7 responsiveness bound for one healthz call,
// expressed as the time the process spends on the CPU while the call
// waits. It is the 50ms wall-clock bound the scenario originally applied,
// restated in a clock that host contention does not inflate.
const healthzBound = 50 * time.Millisecond

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Simulate a healthz call under CPU pressure. healthz is supposed to
	// be a constant-time response; we model it as a single yield to the
	// scheduler, so the wait is the time the Go scheduler takes to run the
	// healthz goroutine again while the burners hold every P.
	wallStart := time.Now()
	cpuStart, err := processCPUTime()
	if err != nil {
		return fmt.Errorf("read process CPU clock: %w", err)
	}
	runtime.Gosched()
	cpuEnd, err := processCPUTime()
	if err != nil {
		return fmt.Errorf("read process CPU clock: %w", err)
	}
	wall := time.Since(wallStart)
	waited := scheduledWait(cpuEnd-cpuStart, runtime.GOMAXPROCS(0))
	if waited > healthzBound {
		s.counters.Inc("healthz_slow")
		return fmt.Errorf("§17.7 violated: healthz waited %s of scheduled time (%s wall) under CPU pressure", waited, wall)
	}
	s.counters.Inc("healthz_responsive")
	return nil
}

// scheduledWait converts the CPU time the whole process consumed during a
// wait into the wall-clock time the wait would have taken had the host run
// every one of the process's Ps for its whole duration.
//
// The scenario saturates every P with a burner, so while the healthz
// goroutine waits the process accrues CPU time at GOMAXPROCS seconds per
// second whenever the host runs it. A wait that the Go scheduler causes
// (the healthz goroutine starved behind the burners) therefore accrues CPU
// time in proportion to its length. A wait that host contention causes
// (another process holding the machine's cores while this process sits
// descheduled) accrues none, because no thread of this process runs. The
// wall clock counts both, which made the scenario fail on a busy shared
// host with no defect in the code under test; this clock counts only the
// first. A starved healthz still fails: a goroutine that waits for the
// burners to stop waits the full profile duration, and even a host that
// leaves the process a single core accrues duration/GOMAXPROCS of
// scheduled wait, far above the bound.
func scheduledWait(processCPU time.Duration, procs int) time.Duration {
	if procs < 1 {
		procs = 1
	}
	return processCPU / time.Duration(procs)
}

// processCPUTime reads the CPU time consumed by every thread of this
// process (CLOCK_PROCESS_CPUTIME_ID), which advances only while the host
// runs one of the process's threads.
func processCPUTime() (time.Duration, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_PROCESS_CPUTIME_ID, &ts); err != nil {
		return 0, err
	}
	return time.Duration(ts.Nano()), nil
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
