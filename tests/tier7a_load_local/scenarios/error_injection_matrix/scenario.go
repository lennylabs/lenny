// SPDX-License-Identifier: MIT

//go:build load_local

// Package error_injection_matrix configures the runtime stub with a
// non-zero ErrorRate and asserts every adapter call either completes
// or returns the documented error; no panics, no leaked in-flight
// count.
//
// TESTING.md §12.7.a multi-component scenarios.
package error_injection_matrix

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	runtimestub "github.com/lennylabs/lenny/tests/testinfra/stubs/runtime"
)

const name = "error_injection_matrix"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	stub     *runtimestub.Stub
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.stub = runtimestub.New(runtimestub.Config{ErrorRate: 0.25, MaxConcurrent: 8})
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	err := s.stub.Call(ctx)
	switch {
	case err == nil:
		s.counters.Inc("completions")
		return nil
	case err == runtimestub.ErrAtCapacity:
		return nil
	case err.Error() == "runtime stub: injected error":
		s.counters.Inc("errors")
		return nil
	default:
		s.counters.Inc("leaks")
		return fmt.Errorf("unexpected: %v", err)
	}
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("leaks"); v > 0 {
		return fmt.Errorf("§15.4 violated: %d leaked error categories", v)
	}
	if s.stub.InFlight() != 0 {
		return fmt.Errorf("§15.4 violated: %d in-flight after run", s.stub.InFlight())
	}
	if s.counters.Get("errors") == 0 {
		return fmt.Errorf("scenario did not exercise the error injection path")
	}
	return nil
}
