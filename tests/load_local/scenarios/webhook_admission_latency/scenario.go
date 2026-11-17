// SPDX-License-Identifier: MIT

//go:build load_local

// Package webhook_admission_latency drives
// pkg/admission/sandboxclaim_guard.Decide from N concurrent
// goroutines, modelling the §17.2 webhook hot path. The invariant:
// Decide is pure, returns within microseconds, and admits / rejects
// according to the §4.6.1 CREATE-only per-pod uniqueness rule
// independent of goroutine ordering.
//
// TESTING.md §12.7.a component-isolated benches.
package webhook_admission_latency

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/admission/sandboxclaim_guard"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "webhook_admission_latency"

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
	// Two admission cases, one per iteration parity, covering the
	// §4.6.1 CREATE-only per-pod uniqueness rule.
	switch iter % 2 {
	case 0:
		// First claim for a fresh pod — §4.6.1 admits.
		req := sandboxclaim_guard.Request{
			Operation:  sandboxclaim_guard.OpCreate,
			ClaimName:  fmt.Sprintf("claim-%d-%d", vu, iter),
			SandboxRef: fmt.Sprintf("pod-%d-%d", vu, iter),
		}
		d, err := sandboxclaim_guard.Decide(req)
		if err != nil {
			return err
		}
		if !d.Allowed {
			s.counters.Inc("first_claim_rejected_unexpected")
			return fmt.Errorf("§4.6.1 violated: first per-pod claim rejected")
		}
		s.counters.Inc("first_claim_admitted")
	case 1:
		// Second claim for a pod that already holds a non-terminal claim —
		// §4.6.1 rejects (per-pod uniqueness, no concurrency exemption).
		req := sandboxclaim_guard.Request{
			Operation:  sandboxclaim_guard.OpCreate,
			ClaimName:  fmt.Sprintf("claim-dup-%d-%d", vu, iter),
			SandboxRef: "pod-s",
			ExistingClaims: []sandboxclaim_guard.ExistingClaim{{
				Name:   "existing",
				Status: sandboxclaim_guard.ClaimBound,
			}},
		}
		d, err := sandboxclaim_guard.Decide(req)
		if err != nil {
			return err
		}
		if d.Allowed {
			s.counters.Inc("duplicate_admitted_unexpected")
			return fmt.Errorf("§4.6.1 violated: duplicate per-pod claim admitted")
		}
		s.counters.Inc("duplicate_rejected")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	for _, unexpected := range []string{"first_claim_rejected_unexpected", "duplicate_admitted_unexpected"} {
		if v := s.counters.Get(unexpected); v > 0 {
			return fmt.Errorf("§4.6.1 violated: %s=%d", unexpected, v)
		}
	}
	if s.counters.Get("first_claim_admitted") == 0 ||
		s.counters.Get("duplicate_rejected") == 0 {
		return fmt.Errorf("scenario must exercise both admission cases")
	}
	return nil
}
