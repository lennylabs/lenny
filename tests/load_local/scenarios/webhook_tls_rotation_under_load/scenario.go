// SPDX-License-Identifier: MIT

//go:build load_local

// Package webhook_tls_rotation_under_load models the §13.5 webhook
// TLS rotation contract: a scenario-local verifier holds the current
// CA bundle, the rotation flips the bundle, and verify-loop checks
// during the flip must keep validating against the new bundle
// instead of failing.
//
// TESTING.md §12.7.a multi-component scenarios.
package webhook_tls_rotation_under_load

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "webhook_tls_rotation_under_load"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// trustBundle is a tiny model of a CA-pinned verifier. The current
// generation is the only accepted issuer; rotation swaps it under
// the mutex so concurrent verifications either see the old
// generation (which is still valid until rotation completes) or the
// new one — never an undefined intermediate.
type trustBundle struct {
	mu  sync.RWMutex
	gen int64
}

func (b *trustBundle) verify(presentedGen int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return presentedGen == b.gen || presentedGen == b.gen-1
}

func (b *trustBundle) rotate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
}

type Scenario struct {
	counters *scenkit.Counters
	bundle   *trustBundle

	stop chan struct{}
	done chan struct{}
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.bundle = &trustBundle{gen: 1}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.rotator()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	close(s.stop)
	<-s.done
	return nil
}

// rotator flips the bundle every 100ms throughout the run.
func (s *Scenario) rotator() {
	defer close(s.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.bundle.rotate()
			s.counters.Inc("rotations")
		}
	}
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Each verify call presents the current observed generation.
	// Under the §13.5 overlap window the verifier accepts gen and
	// gen-1, so a rotation mid-verify must not fail the call.
	s.bundle.mu.RLock()
	presented := s.bundle.gen
	s.bundle.mu.RUnlock()
	if !s.bundle.verify(presented) {
		s.counters.Inc("rejected_unexpected")
		return fmt.Errorf("§13.5 violated: verify rejected during rotation overlap")
	}
	s.counters.Inc("verified")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("rejected_unexpected"); v > 0 {
		return fmt.Errorf("§13.5 violated: %d failed verifies during rotation", v)
	}
	if s.counters.Get("verified") == 0 {
		return fmt.Errorf("scenario did not verify anything")
	}
	if s.counters.Get("rotations") == 0 {
		return fmt.Errorf("scenario did not rotate during the run")
	}
	return nil
}
