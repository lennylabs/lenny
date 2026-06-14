// SPDX-License-Identifier: MIT

//go:build load_local

// Package webhook_admission_latency drives
// pkg/admission/sandboxclaim_guard.Decide from N concurrent
// goroutines, modelling the §17.2 webhook hot path. The invariant:
// Decide is pure, returns within microseconds, and admits / rejects
// according to the documented §5.2 + §4.6.1 rules independent of
// goroutine ordering.
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
	// Three admission shapes, one per iteration mod 3.
	switch iter % 3 {
	case 0:
		// Slot-bearing CREATE on idle Sandbox — §5.2 admits.
		req := sandboxclaim_guard.Request{
			Operation:    sandboxclaim_guard.OpCreate,
			ClaimName:    fmt.Sprintf("claim-%d-%d", vu, iter),
			SandboxRef:   "pod-c",
			SandboxPhase: sandboxclaim_guard.PhaseIdle,
			HasSlotID:    true,
		}
		d, err := sandboxclaim_guard.Decide(req)
		if err != nil {
			return err
		}
		if !d.Allowed {
			s.counters.Inc("slot_claim_rejected_unexpected")
			return fmt.Errorf("§5.2 violated: slot claim rejected on idle Sandbox")
		}
		s.counters.Inc("slot_claim_admitted")
	case 1:
		// Session-mode CREATE on claimed Sandbox with existing claim —
		// §4.6.1 rejects (duplicate-claim rule).
		req := sandboxclaim_guard.Request{
			Operation:    sandboxclaim_guard.OpCreate,
			ClaimName:    fmt.Sprintf("claim-%d-%d", vu, iter),
			SandboxRef:   "pod-s",
			SandboxPhase: sandboxclaim_guard.PhaseClaimed,
			HasSlotID:    false,
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
			return fmt.Errorf("§4.6.1 violated: duplicate session-mode claim admitted")
		}
		s.counters.Inc("duplicate_rejected")
	case 2:
		// Slot-bearing CREATE on slot_active Sandbox — §5.2 admits.
		req := sandboxclaim_guard.Request{
			Operation:    sandboxclaim_guard.OpCreate,
			ClaimName:    fmt.Sprintf("claim-%d-%d", vu, iter),
			SandboxRef:   "pod-c2",
			SandboxPhase: sandboxclaim_guard.PhaseSlotActive,
			HasSlotID:    true,
		}
		d, err := sandboxclaim_guard.Decide(req)
		if err != nil {
			return err
		}
		if !d.Allowed {
			s.counters.Inc("slot_active_rejected_unexpected")
			return fmt.Errorf("§5.2 violated: slot claim rejected on slot_active Sandbox")
		}
		s.counters.Inc("slot_active_admitted")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	for _, unexpected := range []string{"slot_claim_rejected_unexpected", "duplicate_admitted_unexpected", "slot_active_rejected_unexpected"} {
		if v := s.counters.Get(unexpected); v > 0 {
			return fmt.Errorf("§5.2/§4.6.1 violated: %s=%d", unexpected, v)
		}
	}
	if s.counters.Get("slot_claim_admitted") == 0 ||
		s.counters.Get("duplicate_rejected") == 0 ||
		s.counters.Get("slot_active_admitted") == 0 {
		return fmt.Errorf("scenario must exercise all three admission shapes")
	}
	return nil
}
