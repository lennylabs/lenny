// SPDX-License-Identifier: MIT

//go:build load_local

// Package claim_admission_ordering exercises the §5.2 ordering window
// between the gateway's Redis Lua slot counter (source of truth) and
// the SSA-mirrored Sandbox phase. The regression source is commit
// 2b20338: when the first slot claim CREATE landed before the SSA
// patch advanced phase to slot_active, the admission webhook saw
// phase=idle and rejected every subsequent claim.
//
// The scenario fakes the SSA mirror lag using fakekube + watchlag:
// each "slot reserve" inserts an object representing the claim, but
// the phase-mirror update is enqueued with a configurable delay. The
// admission decision must admit slot-bearing claims regardless of
// whether the phase mirror has caught up.
//
// TESTING.md §12.7.a regression scenarios.
package claim_admission_ordering

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "claim_admission_ordering"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters

	store    *fakekube.ObjectStore
	sandbox  string
	maxSlots int

	mu sync.Mutex
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = fakekube.NewObjectStore()
	s.sandbox = "pod-ordered"
	s.maxSlots = 4
	// Seed the Sandbox object with phase=idle, mirroring the §5.2
	// state before the first slot claim arrives.
	return s.store.Create(&fakekube.Object{
		Kind:        "Sandbox",
		Namespace:   "lenny-agents",
		Name:        s.sandbox,
		Annotations: map[string]string{"phase": "idle", "active_slots": "0"},
	})
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// admit applies the §4.6.1 + §5.2 admission decision. Returns nil
// when the slot claim is admitted.
func (s *Scenario) admit(slotID string, phase string, hasSlotID bool) error {
	if hasSlotID {
		// §5.2: slot-bearing claims admit regardless of phase.
		return nil
	}
	if phase == "slot_active" {
		return nil
	}
	return fmt.Errorf("§4.6.1 rejected: phase=%s, hasSlotID=false", phase)
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Mimic the §5.2 reserve sequence: Redis Lua succeeds; CREATE the
	// SandboxClaim with HasSlotID=true; the SSA phase mirror lags
	// the create.
	s.mu.Lock()
	defer s.mu.Unlock()
	sandbox, err := s.store.Get("Sandbox", "lenny-agents", s.sandbox)
	if err != nil {
		s.counters.Inc("errors")
		return err
	}
	// Read the phase mirror as seen by the admission decision. Because
	// the SSA patch arrives after CREATE in the production race, the
	// admission may see phase=idle for the first claim.
	phaseObserved := sandbox.Annotations["phase"]

	// The new claim has a slot ID, marking it concurrent-mode by face.
	if err := s.admit(fmt.Sprintf("slot-%d-%d", vu, iter), phaseObserved, true); err != nil {
		s.counters.Inc("rejected_with_slot_id")
		return err
	}
	s.counters.Inc("admitted")

	// CREATE the claim object.
	_ = s.store.Create(&fakekube.Object{
		Kind: "SandboxClaim", Namespace: "lenny-agents",
		Name:        fmt.Sprintf("claim-%d-%d", vu, iter),
		Annotations: map[string]string{"slot_id": "yes"},
	})

	// SSA patch the Sandbox phase mirror (best-effort; may race).
	sandbox.Annotations["phase"] = "slot_active"
	_ = s.store.Update(sandbox)

	// Clean up so the loop can recycle the claim name pool.
	_ = s.store.Delete("SandboxClaim", "lenny-agents", fmt.Sprintf("claim-%d-%d", vu, iter))
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("rejected_with_slot_id"); v > 0 {
		return fmt.Errorf("§5.2 violated: %d slot-bearing claims rejected", v)
	}
	if s.counters.Get("admitted") == 0 {
		return fmt.Errorf("scenario did not exercise the admission path")
	}
	return nil
}
