// SPDX-License-Identifier: MIT

//go:build load_local

// Package goroutine_leak_long_run sustains moderate load on the
// inproc gateway and asserts the goroutine count returns within
// tolerance of the baseline after Teardown. The invariant: no
// gateway path leaks goroutines under repeated session create/delete.
//
// TESTING.md §12.7.a multi-component scenarios.
package goroutine_leak_long_run

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "goroutine_leak_long_run"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	scenkit.InProcMixin
	counters     *scenkit.Counters
	baselineGoro int
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 8, Duration: 3 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.counters = scenkit.NewCounters()
	if err := s.SetupInProc(ctx, inproc.Config{}); err != nil {
		return err
	}
	// Capture the baseline goroutine count after the env is fully up.
	time.Sleep(50 * time.Millisecond)
	s.baselineGoro = runtime.NumGoroutine()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return s.TeardownInProc(ctx) }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	status, _, err := scenkit.DoJSON(ctx, "POST", s.Env().GatewayURL()+"/v1/sessions", []byte(`{"runtimeRef":"echo"}`))
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	if status != http.StatusCreated {
		s.counters.Inc("failures")
		return fmt.Errorf("status=%d", status)
	}
	s.counters.Inc("hits")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	// Give the runtime a beat to settle in-flight goroutines.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	current := runtime.NumGoroutine()
	s.counters.EmitTo(r)
	r.AddCustom("baseline_goro", float64(s.baselineGoro))
	r.AddCustom("post_run_goro", float64(current))
	r.AddCustom("delta_goro", float64(current-s.baselineGoro))
	if f := s.counters.Get("failures"); f > 0 {
		return fmt.Errorf("scenario observed %d failures during load", f)
	}
	// Allow a small drift tolerance for runtime-managed goroutines.
	if current > s.baselineGoro+50 {
		return fmt.Errorf("§15.1 leak suspected: %d goroutines after run vs %d baseline (delta=%d > 50)",
			current, s.baselineGoro, current-s.baselineGoro)
	}
	return nil
}
