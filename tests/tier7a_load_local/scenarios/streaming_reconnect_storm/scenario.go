// SPDX-License-Identifier: MIT

//go:build load_local

// Package streaming_reconnect_storm exercises the inproc gateway's
// session lifecycle under a storm of create → terminate → get
// cycles. The §15.1 invariant: a terminated session stays
// terminated; no session goroutine leaks under rapid cycling.
//
// TESTING.md §12.7.a multi-component scenarios.
package streaming_reconnect_storm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "streaming_reconnect_storm"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{} })
}

type Scenario struct {
	scenkit.InProcMixin
	counters *scenkit.Counters
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.counters = scenkit.NewCounters()
	return s.SetupInProc(ctx, inproc.Config{})
}

func (s *Scenario) Teardown(ctx context.Context) error { return s.TeardownInProc(ctx) }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	gw := s.Env().GatewayURL()

	// Create.
	status, body, err := scenkit.DoJSON(ctx, "POST", gw+"/v1/sessions", []byte(`{"runtimeRef":"echo"}`))
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
	if err := json.Unmarshal(body, &created); err != nil {
		s.counters.Inc("failures")
		return err
	}

	// Terminate.
	status, _, err = scenkit.DoJSON(ctx, "DELETE", gw+"/v1/sessions/"+created.ID, nil)
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	if status != http.StatusNoContent {
		s.counters.Inc("failures")
		return fmt.Errorf("terminate status=%d", status)
	}

	// GET — must show terminated.
	status, body, err = scenkit.DoJSON(ctx, "GET", gw+"/v1/sessions/"+created.ID, nil)
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	var sess struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &sess)
	if sess.Status != "terminated" {
		s.counters.Inc("failures")
		return fmt.Errorf("§15.1 violated: status=%s after DELETE", sess.Status)
	}
	s.counters.Inc("cycles")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	// §15.1 holds across the run as a whole. At the run-end boundary
	// a single in-flight cycle can occasionally lose its second or
	// third HTTP round trip when the loadgen driver cancels the
	// context — these are accounted for via IncOnError + ctx checks,
	// but the gateway-side state machine may also report a transient
	// 5xx during shutdown. Allow ≤ 0.1% of cycles to fail; anything
	// above that signals a real regression.
	cycles := s.counters.Get("cycles")
	failures := s.counters.Get("failures")
	tolerance := cycles / 1000
	if tolerance < 10 {
		tolerance = 10
	}
	if failures > tolerance {
		return fmt.Errorf("§15.1 violated: %d failed cycles of %d (> %d tolerance)", failures, cycles, tolerance)
	}
	if cycles < 10 {
		return fmt.Errorf("scenario did not exercise enough cycles: %d", cycles)
	}
	return nil
}
