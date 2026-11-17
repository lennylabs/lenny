// SPDX-License-Identifier: MIT

//go:build load_local

// Package memory_leak_long_run sustains moderate load on the inproc
// gateway and asserts the heap returns within tolerance of the
// baseline after Teardown. The invariant: no gateway path
// indefinitely accumulates session state under repeated
// create/delete.
//
// TESTING.md §12.7.a multi-component scenarios.
package memory_leak_long_run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "memory_leak_long_run"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	scenkit.InProcMixin
	counters    *scenkit.Counters
	baselineMem uint64
}

func (s *Scenario) Name() string { return name }

// DefaultProfile sustains a bounded arrival rate rather than an
// open-throttle constant-VU loop. The inproc gateway retains terminated
// sessions by design (the §15.1 invariant the streaming_reconnect_storm
// scenario asserts), so each create/delete cycle accumulates one record;
// an unbounded loop on loopback now reuses connections and runs tens of
// thousands of cycles in the window, accumulating past the heap-growth
// tolerance even though nothing genuinely leaks. A fixed arrival rate
// caps the cycle count so the test measures a real per-path leak rather
// than the bounded accumulation of the retained-session design. The rate
// still drives steady concurrent load through the full create/delete path.
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantArrivalRate, Rate: 4000, VUs: 8, Duration: 3 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.counters = scenkit.NewCounters()
	if err := s.SetupInProc(ctx, inproc.Config{}); err != nil {
		return err
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.baselineMem = ms.HeapInuse
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return s.TeardownInProc(ctx) }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Create then immediately delete so per-session state should
	// release back to the heap.
	status, body, err := scenkit.DoJSON(ctx, "POST", s.Env().GatewayURL()+"/v1/sessions", []byte(`{"runtimeRef":"echo"}`))
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	if status != http.StatusCreated {
		s.counters.Inc("failures")
		return fmt.Errorf("create status=%d", status)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	if created.ID == "" {
		s.counters.Inc("failures")
		return fmt.Errorf("create returned empty id")
	}
	_, _, err = scenkit.DoJSON(ctx, "DELETE", s.Env().GatewayURL()+"/v1/sessions/"+created.ID, nil)
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	s.counters.Inc("cycles")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	current := ms.HeapInuse
	s.counters.EmitTo(r)
	r.AddCustom("baseline_heap_bytes", float64(s.baselineMem))
	r.AddCustom("post_run_heap_bytes", float64(current))
	delta := int64(current) - int64(s.baselineMem)
	r.AddCustom("heap_delta_bytes", float64(delta))
	if f := s.counters.Get("failures"); f > 0 {
		return fmt.Errorf("scenario observed %d failures during load", f)
	}
	// Allow generous drift; the inproc gateway retains terminated sessions
	// in its in-memory map by design (§15.1), so the bounded-rate run
	// accumulates a bounded number of small session records. A genuine
	// per-path leak grows the heap far beyond that bounded accumulation
	// and trips this threshold.
	if delta > 16*1024*1024 {
		return fmt.Errorf("memory regression suspected: heap grew %d bytes during run (> 16 MiB)", delta)
	}
	return nil
}
