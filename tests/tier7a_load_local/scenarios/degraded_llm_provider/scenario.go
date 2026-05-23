// SPDX-License-Identifier: MIT

//go:build load_local

// Package degraded_llm_provider models the §4.10 LLM-provider
// degradation contract: when the upstream LLM returns 5xx, sessions
// fail closed with the documented error envelope rather than hang.
// Invariant: every degraded call returns within a bounded window;
// no goroutine waits indefinitely.
//
// TESTING.md §12.7.a resiliency scenarios.
package degraded_llm_provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	runtimestub "github.com/lennylabs/lenny/tests/testinfra/stubs/runtime"
)

const name = "degraded_llm_provider"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	stub     *runtimestub.Stub
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
	// 100% errors with a small latency to model the "degraded" path.
	s.stub = runtimestub.New(runtimestub.Config{
		ErrorRate:       1.0,
		ResponseLatency: time.Millisecond,
	})
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.stub.Call(cctx)
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		s.counters.Inc("slow_completions")
		return fmt.Errorf("§4.10 violated: degraded call took %s (> 50ms)", elapsed)
	}
	if err == nil {
		s.counters.Inc("unexpected_success")
		return fmt.Errorf("§4.10 violated: degraded provider returned success")
	}
	if errors.Is(err, runtimestub.ErrAtCapacity) {
		return nil
	}
	s.counters.Inc("clean_failures")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("slow_completions"); v > 0 {
		return fmt.Errorf("§4.10 violated: %d slow completions (degraded path should fail fast)", v)
	}
	if v := s.counters.Get("unexpected_success"); v > 0 {
		return fmt.Errorf("§4.10 violated: %d unexpected successes from degraded provider", v)
	}
	if s.counters.Get("clean_failures") == 0 {
		return fmt.Errorf("scenario did not exercise the clean-failure path")
	}
	return nil
}
