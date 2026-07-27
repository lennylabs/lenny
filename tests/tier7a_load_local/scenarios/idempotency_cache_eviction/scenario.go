// SPDX-License-Identifier: MIT

//go:build load_local

// Package idempotency_cache_eviction drives the inproc gateway with
// repeated POSTs sharing the same Idempotency-Key. The §11.5
// invariants under concurrency: the operation executes exactly once;
// a retry that arrives while the original is still in flight is
// rejected with 409 IDEMPOTENCY_KEY_IN_FLIGHT rather than executing in
// parallel; and a retry that arrives after the original completes
// replays the cached response.
//
// TESTING.md §12.7.a multi-component scenarios.
package idempotency_cache_eviction

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

func init() {
	loadgen.Register("idempotency_cache_eviction", func() loadgen.Scenario { return &Scenario{} })
}

type Scenario struct {
	scenkit.InProcMixin
	hits     atomic.Int64
	inFlight atomic.Int64
	failures atomic.Int64
}

func (s *Scenario) Name() string { return "idempotency_cache_eviction" }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	return s.SetupInProc(ctx, inproc.Config{})
}

func (s *Scenario) Teardown(ctx context.Context) error {
	return s.TeardownInProc(ctx)
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// The shared scenkit client pools and bounds connections per gateway
	// and drains every body, so replays reuse the pool rather than dialing
	// a fresh socket per request.
	status, _, err := scenkit.DoJSON(ctx, "POST", s.Env().GatewayURL()+"/v1/sessions",
		[]byte(`{"runtimeRef":"echo"}`), scenkit.H("Idempotency-Key", "shared-key"))
	if err != nil {
		if ctx.Err() == nil {
			s.failures.Add(1)
		}
		return err
	}
	switch status {
	case http.StatusCreated:
		// Either the one execution or a replay of its cached response.
		s.hits.Add(1)
	case http.StatusConflict:
		// spec: §11.5 — a retry that arrives while the original is still
		// in flight is rejected rather than executed in parallel. Under
		// concurrent VUs sharing one key this is the expected outcome for
		// every retry that races the first execution.
		s.inFlight.Add(1)
	default:
		s.failures.Add(1)
		return fmt.Errorf("status=%d", status)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("hits", float64(s.hits.Load()))
	r.AddCustom("in_flight_rejected", float64(s.inFlight.Load()))
	r.AddCustom("failures", float64(s.failures.Load()))
	r.AddCustom("idem_hits", float64(s.Env().IdempotencyHits()))
	r.AddCustom("sessions", float64(s.Env().SessionCount()))
	if s.failures.Load() > 0 {
		return fmt.Errorf("§11.5 violated: %d failed sessions", s.failures.Load())
	}
	// Exactly one session must have been created across the run; every
	// other POST was an idempotency cache hit.
	if s.Env().SessionCount() != 1 {
		return fmt.Errorf("§11.5 violated: %d sessions created for one key", s.Env().SessionCount())
	}
	if s.Env().IdempotencyHits() == 0 {
		return fmt.Errorf("§11.5 violated: zero cache hits across %d POSTs", s.hits.Load())
	}
	return nil
}
