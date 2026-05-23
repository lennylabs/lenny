// SPDX-License-Identifier: MIT

//go:build load_local

// Package lease_extension_race exercises pkg/leaseextension.Grant
// under many concurrent goroutines. The §4.9 invariant: Grant returns
// a granted value never exceeding ceiling, and an Outcome that
// honours the ordering current < requested <= ceiling.
//
// TESTING.md §12.7.a regression scenarios.
package lease_extension_race

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/leaseextension"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "lease_extension_race"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
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
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error    { return nil }
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	current := int64((vu + iter) % 100)
	requested := current + int64(vu%50)
	ceiling := int64(75)
	granted, outcome := leaseextension.Grant(current, requested, ceiling)
	if granted > ceiling {
		s.counters.Inc("overshoots")
		return fmt.Errorf("§4.9 violated: granted=%d > ceiling=%d", granted, ceiling)
	}
	if outcome == leaseextension.CeilingReached {
		s.counters.Inc("denials")
	} else {
		s.counters.Inc("grants")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("overshoots"); v > 0 {
		return fmt.Errorf("§4.9 violated: %d overshoots", v)
	}
	return nil
}
