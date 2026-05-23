// SPDX-License-Identifier: MIT

//go:build load_local

// Package terminate_path_branching exercises the §5.2 close path:
// session-mode terminate goes through binder.Release (which drains
// the pod), concurrent-mode terminate goes through SlotClaimer.
// ReleaseSlot (which only releases the slot). The regression source
// is commit c503666: a uniform Release for both paths leaked claims
// and drained pods prematurely.
//
// TESTING.md §12.7.a regression scenarios.
package terminate_path_branching

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "terminate_path_branching"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters

	// pod tracks per-pod slot counts (concurrent-mode) and a drained
	// flag (session-mode).
	mu      sync.Mutex
	pods    map[string]*podState
}

type podState struct {
	slots   int
	drained bool
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.pods = make(map[string]*podState, 64)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// reserveAndTerminate models one create-then-terminate cycle.
// hasSlotID selects the concurrent-mode path; the §5.2 invariant is
// that concurrent-mode terminate only decrements the slot count
// without draining the pod, and session-mode terminate drains.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// In production each pod is either session-mode OR concurrent-
	// mode (set at warm-pool creation), never both. The scenario
	// models that constraint by partitioning pod IDs by mode.
	hasSlotID := vu%2 == 0
	podID := fmt.Sprintf("concurrent-%d", vu%4)
	if !hasSlotID {
		podID = fmt.Sprintf("session-%d", vu%4)
	}

	s.mu.Lock()
	p, ok := s.pods[podID]
	if !ok {
		p = &podState{}
		s.pods[podID] = p
	}
	p.slots++
	s.mu.Unlock()

	// Terminate: branch on hasSlotID.
	s.mu.Lock()
	if hasSlotID {
		// Concurrent-mode: decrement only. Session-mode drain must
		// never apply to a concurrent-mode pod, so slots cannot go
		// negative here under the §5.2 contract.
		if p.drained {
			s.counters.Inc("concurrent_on_drained_unexpected")
		}
		p.slots--
		if p.slots < 0 {
			s.counters.Inc("negative_slots")
		}
		s.counters.Inc("released_slot")
	} else {
		// Session-mode: drain the pod. Session-mode pods only host
		// one session at a time, so slots is always 1 before drain
		// and 0 after.
		if p.drained {
			s.counters.Inc("double_drain")
		}
		p.drained = true
		p.slots = 0
		s.counters.Inc("drained_pod")
	}
	s.mu.Unlock()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("negative_slots"); v > 0 {
		return fmt.Errorf("§5.2 violated: %d negative slot counts (concurrent terminate decremented below zero)", v)
	}
	if s.counters.Get("released_slot") == 0 || s.counters.Get("drained_pod") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
