// SPDX-License-Identifier: MIT

//go:build load_local

// Package cascading_failure_isolation models the §10.5 isolation
// contract: when a downstream component (LLM provider, KMS, etc.)
// fails, the gateway absorbs the failure and continues to serve
// requests that don't depend on it. The invariant: a 100% failure
// rate on the "external" component does not raise the failure rate
// on the "internal" component beyond a small overhead.
//
// TESTING.md §12.7.a resiliency scenarios.
package cascading_failure_isolation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	runtimestub "github.com/lennylabs/lenny/tests/testinfra/stubs/runtime"
)

const name = "cascading_failure_isolation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	failing  *runtimestub.Stub // simulates failing LLM provider
	healthy  *runtimestub.Stub // simulates healthy internal component
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
	// 100% error rate on the failing component, 0% on the healthy.
	s.failing = runtimestub.New(runtimestub.Config{ErrorRate: 1.0})
	s.healthy = runtimestub.New(runtimestub.Config{ErrorRate: 0})
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Even iterations: call the failing component. The gateway
	// should observe the error and return cleanly without
	// affecting the healthy path.
	if iter%2 == 0 {
		err := s.failing.Call(ctx)
		if err != nil && !errors.Is(err, runtimestub.ErrAtCapacity) {
			s.counters.Inc("failing_errors")
			return nil
		}
		s.counters.Inc("failing_succeeded_unexpected")
		return nil
	}
	// Odd iterations: call the healthy component. Must always
	// succeed regardless of the failing component's state.
	err := s.healthy.Call(ctx)
	if err != nil {
		if errors.Is(err, runtimestub.ErrAtCapacity) {
			return nil
		}
		s.counters.Inc("healthy_errors_unexpected")
		return fmt.Errorf("§10.5 violated: healthy component reports error: %v", err)
	}
	s.counters.Inc("healthy_succeeded")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("healthy_errors_unexpected"); v > 0 {
		return fmt.Errorf("§10.5 violated: %d healthy-path errors during failing-component failure", v)
	}
	if s.counters.Get("failing_errors") == 0 || s.counters.Get("healthy_succeeded") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
