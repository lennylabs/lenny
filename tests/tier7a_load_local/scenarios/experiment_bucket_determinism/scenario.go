// SPDX-License-Identifier: MIT

//go:build load_local

// Package experiment_bucket_determinism exercises pkg/experiment
// HMAC bucketing under many concurrent goroutines. The §10.7
// invariant: AssignVariant for the same subject deterministically
// returns the same variant.
//
// TESTING.md §12.7.a regression scenarios.
package experiment_bucket_determinism

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "experiment_bucket_determinism"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters     *scenkit.Counters
	experimentID string
	variants     []experiment.Variant

	mu          sync.Mutex
	assignments map[string]string
}

func (s *Scenario) Name() string { return name }

// RampProfiles enumerates ascending VU counts for capacity discovery
// under LENNY_TIER7A_CAPACITY=1.
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 512, Duration: 2 * time.Second},
	}
}

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.experimentID = "exp-A"
	s.variants = []experiment.Variant{
		{ID: "treatment-a", Weight: 0.25},
		{ID: "treatment-b", Weight: 0.25},
	}
	s.assignments = make(map[string]string, 256)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	subject := fmt.Sprintf("user-%d", iter%200)
	variant := experiment.AssignVariant(subject, s.experimentID, s.variants)
	s.counters.Inc("assigns")
	s.mu.Lock()
	prev, seen := s.assignments[subject]
	if !seen {
		s.assignments[subject] = variant
	} else if prev != variant {
		s.counters.Inc("mismatches")
	}
	s.mu.Unlock()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("distinct_subjects", float64(len(s.assignments)))
	if v := s.counters.Get("mismatches"); v > 0 {
		return fmt.Errorf("§10.7 violated: %d subjects observed multiple variants", v)
	}
	return nil
}
