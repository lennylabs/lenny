// SPDX-License-Identifier: MIT

//go:build load_local

// Package cascading_failure_isolation asserts the §11.6 per-tier
// breaker isolation invariant: an operator-opened breaker scoped to
// one operation type must reject only requests matching that scope;
// requests for other operations pass through. The scenario drives
// the real pkg/gateway/middleware/circuitbreaker + the in-process
// circuitbreaker.MemoryRegistry against two distinct request
// shapes — one matching the open breaker (uploads), one not
// (session creation) — and verifies the unmatched class is unaffected.
//
// TESTING.md §12.7.a resiliency scenarios.
package cascading_failure_isolation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "cascading_failure_isolation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters     *scenkit.Counters
	server       *httptest.Server
	innerServed  atomic.Int64
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.innerServed.Store(0)

	// Registry holding one open breaker scoped to "uploads". Session
	// creation requests are out of scope and must pass through.
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{{
		Name:      "uploads-paused",
		State:     circuitbreaker.StateOpen,
		Reason:    "test: simulated subsystem failure",
		OpenedAt:  time.Now(),
		LimitTier: circuitbreaker.TierOperationType,
		Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpUploads},
	}})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.innerServed.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	// Extractor reads ?op=uploads | ?op=session_creation so the
	// scenario can issue both shapes against the same handler.
	extract := func(r *http.Request) circuitbreaker.Request {
		switch r.URL.Query().Get("op") {
		case "uploads":
			return circuitbreaker.Request{OperationType: circuitbreaker.OpUploads}
		case "session_creation":
			return circuitbreaker.Request{OperationType: circuitbreaker.OpSessionCreation}
		}
		return circuitbreaker.Request{}
	}

	handler := cbmw.Wrap(inner, reg, cbmw.Options{Extract: extract})
	s.server = httptest.NewServer(handler)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.server != nil {
		s.server.Close()
	}
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Alternate between the failing class (uploads) and the healthy
	// class (session_creation).
	op := "session_creation"
	if iter%2 == 0 {
		op = "uploads"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.server.URL+"/v1/test?op="+op, nil)
	if err != nil {
		s.counters.Inc("client_error")
		return nil
	}
	resp, err := s.server.Client().Do(req)
	if err != nil {
		s.counters.Inc("transport_error")
		return nil
	}
	_ = resp.Body.Close()
	switch {
	case op == "uploads" && resp.StatusCode == http.StatusServiceUnavailable:
		s.counters.Inc("uploads_rejected")
	case op == "uploads" && resp.StatusCode == http.StatusOK:
		s.counters.Inc("uploads_admitted_unexpected")
		return fmt.Errorf("§11.6 violated: uploads request admitted through open breaker")
	case op == "session_creation" && resp.StatusCode == http.StatusOK:
		s.counters.Inc("session_admitted")
	case op == "session_creation" && resp.StatusCode == http.StatusServiceUnavailable:
		s.counters.Inc("session_rejected_unexpected")
		return fmt.Errorf("§11.6 violated: session_creation rejected by an unrelated breaker")
	default:
		s.counters.Inc("unexpected_status")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("inner_served", float64(s.innerServed.Load()))
	uploadsRejected := s.counters.Get("uploads_rejected")
	sessionAdmitted := s.counters.Get("session_admitted")
	if uploadsRejected == 0 {
		return fmt.Errorf("§11.6 violated: open breaker did not reject any uploads request")
	}
	if sessionAdmitted == 0 {
		return fmt.Errorf("§11.6 violated: no session_creation requests passed through (unrelated traffic bled into the open breaker)")
	}
	if v := s.counters.Get("uploads_admitted_unexpected") + s.counters.Get("session_rejected_unexpected"); v > 0 {
		return fmt.Errorf("§11.6 violated: %d unexpected outcomes", v)
	}
	return nil
}
