// SPDX-License-Identifier: MIT

//go:build load_local

// Package runtime_adapter_slow_response exercises the runtime stub
// at high concurrent call rates with a non-trivial per-call latency,
// asserting that the adapter's MaxConcurrent cap holds and that
// context cancellation is honoured.
//
// TESTING.md §12.7.a multi-component scenarios.
package runtime_adapter_slow_response

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	runtimestub "github.com/lennylabs/lenny/tests/testinfra/stubs/runtime"
)

const name = "runtime_adapter_slow_response"

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

func (s *Scenario) Setup(ctx context.Context) error {
	s.stub = runtimestub.New(runtimestub.Config{
		ResponseLatency: 50 * time.Millisecond,
		MaxConcurrent:   4,
	})
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	callCtx := ctx
	if iter%3 == 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
		defer cancel()
	}
	err := s.stub.Call(callCtx)
	switch {
	case err == nil:
		s.counters.Inc("completions")
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		s.counters.Inc("cancels")
	case errors.Is(err, runtimestub.ErrAtCapacity):
		s.counters.Inc("rejections")
	default:
		s.counters.Inc("leaks")
		return fmt.Errorf("unexpected error: %v", err)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("leaks"); v > 0 {
		return fmt.Errorf("§15.4 violated: %d unexpected errors", v)
	}
	if s.stub.InFlight() != 0 {
		return fmt.Errorf("§15.4 violated: %d in-flight calls after run", s.stub.InFlight())
	}
	if s.counters.Get("completions") == 0 || s.counters.Get("rejections") == 0 {
		return fmt.Errorf("scenario must exercise both completion and rejection paths")
	}
	return nil
}
