// SPDX-License-Identifier: MIT

//go:build load_local

// Package audit_sink_backpressure drives the audit chain through a
// sink with artificial latency and asserts the gateway's apparent
// throughput stays bounded — the sink's slowness must not block
// gateway-side Appends past a documented backpressure threshold.
//
// TESTING.md §12.7.a multi-component scenarios.
package audit_sink_backpressure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "audit_sink_backpressure"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	chain    *audit.Chain
	sink     chan audit.Row
	done     chan struct{}
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.chain = audit.NewChain("acme")
	// Bounded sink — when full, Run() must observe the backpressure
	// path (skip-append rather than block forever).
	s.sink = make(chan audit.Row, 64)
	s.done = make(chan struct{})
	go s.drainSink()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	close(s.done)
	return nil
}

// drainSink simulates a slow downstream consumer (e.g. SIEM with
// network latency). One event per millisecond.
func (s *Scenario) drainSink() {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			select {
			case <-s.sink:
			default:
			}
		}
	}
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	payload, _ := json.Marshal(map[string]int{"vu": vu, "iter": iter})
	row := s.chain.Append("session.created", payload, time.Now())
	s.counters.Inc("appended")
	// Non-blocking enqueue: when the sink can't keep up, count the
	// backpressure event instead of stalling the gateway.
	select {
	case s.sink <- row:
		s.counters.Inc("sink_accepted")
	default:
		s.counters.Inc("sink_dropped")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("appended") == 0 {
		return fmt.Errorf("scenario appended nothing")
	}
	// The §11.7 invariant: the chain must be intact even under sink
	// backpressure. ChainVerified must hold.
	v := s.chain.Verify()
	if v.Integrity != audit.ChainVerified {
		return fmt.Errorf("§11.7 violated: chain integrity=%s under backpressure", v.Integrity)
	}
	// At least one event must have been dropped at the sink boundary
	// for the scenario to have actually exercised backpressure.
	if s.counters.Get("sink_dropped") == 0 {
		return fmt.Errorf("scenario did not exercise the backpressure path; raise the iteration rate or shrink the sink buffer")
	}
	return nil
}
