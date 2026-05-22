// SPDX-License-Identifier: MIT

//go:build load_local

// Package delegation_depth_n drives pkg/delegation/cycle.Decide at
// depth N from many goroutines. The §8.2 invariant: cycle detection
// holds when the target identity already appears in the lineage,
// regardless of concurrency.
//
// TESTING.md §12.7.a multi-component scenarios.
package delegation_depth_n

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "delegation_depth_n"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	lineage  cycle.Lineage
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	// Build a 10-deep lineage with a known identity at position 4
	// that cycle detection must catch on a self-recursive hop.
	s.lineage = cycle.Lineage{}
	for i := 0; i < 10; i++ {
		s.lineage = append(s.lineage, cycle.Identity{
			RuntimeName: fmt.Sprintf("runtime-%d", i),
			PoolName:    "default",
		})
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// All layers reject self-recursion; the only way out is to NOT
	// be self-recursive.
	settings := cycle.Settings{Mode: cycle.ModeEnforce}
	if iter%2 == 0 {
		target := cycle.Identity{RuntimeName: fmt.Sprintf("runtime-fresh-%d-%d", vu, iter), PoolName: "default"}
		d := cycle.Decide(s.lineage, target, settings)
		if d.Outcome == cycle.OutcomeAdmitted {
			s.counters.Inc("admitted_fresh")
		} else {
			s.counters.Inc("rejected_fresh_unexpected")
			return fmt.Errorf("§8.2 violated: fresh target rejected (%s)", d.Outcome)
		}
		return nil
	}
	// Cycle target — already in the lineage.
	target := s.lineage[4]
	d := cycle.Decide(s.lineage, target, settings)
	if d.Outcome == cycle.OutcomeAdmitted {
		s.counters.Inc("admitted_cycle_unexpected")
		return fmt.Errorf("§8.2 violated: cycle target admitted")
	}
	s.counters.Inc("rejected_cycle")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("admitted_cycle_unexpected"); v > 0 {
		return fmt.Errorf("§8.2 violated: %d cycle targets admitted", v)
	}
	if v := s.counters.Get("rejected_fresh_unexpected"); v > 0 {
		return fmt.Errorf("§8.2 violated: %d fresh targets rejected", v)
	}
	if s.counters.Get("admitted_fresh") == 0 || s.counters.Get("rejected_cycle") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
