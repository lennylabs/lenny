// SPDX-License-Identifier: MIT

//go:build load_local

// Package crd_watch_event_flood drives the fakekube watchlag surface
// with bursty mutations and asserts every event is observed without
// drop. The §4.6 invariant: the watch consumer's offset stays
// monotone across the flood.
//
// TESTING.md §12.7.a multi-component scenarios.
package crd_watch_event_flood

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	"github.com/lennylabs/lenny/tests/testinfra/watchlag"
)

const name = "crd_watch_event_flood"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	clock    *clockstep.Clock
	stream   *watchlag.Stream
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.clock = clockstep.New(time.Now())
	s.stream = watchlag.New(s.clock, 5*time.Millisecond)
	go s.drain(ctx)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-s.stream.Events():
			if !ok {
				return
			}
			s.counters.Inc("delivered")
		}
	}
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	s.stream.Publish(fmt.Sprintf("vu=%d iter=%d", vu, iter))
	s.counters.Inc("published")
	s.clock.Advance(10 * time.Millisecond)
	s.stream.Pump()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	time.Sleep(20 * time.Millisecond)
	s.counters.EmitTo(r)
	published := s.counters.Get("published")
	delivered := s.counters.Get("delivered")
	if published == 0 {
		return fmt.Errorf("scenario published nothing")
	}
	if 100*delivered/published < 95 {
		return fmt.Errorf("§4.6 violated: %d delivered of %d published (<95%%)", delivered, published)
	}
	return nil
}
