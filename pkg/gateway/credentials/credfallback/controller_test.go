// SPDX-License-Identifier: MIT

package credfallback

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
)

const testProvider = credential.ProviderAnthropicDirect

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// spec: §4.9 lines 1383-1411 — a fault on a multi-pool chain selects the
// next available pool and records the faulted pool's cooldown.
func TestControllerFaultSelectsNextPool(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(3, 60*time.Second)
	c.SetClock(fixedClock(t0))
	c.RegisterChain("s1", testProvider, []string{"primary", "backup"})

	dec := c.Fault("s1", testProvider, "primary", credential.TriggerFaultRateLimited)
	if dec.Exhausted {
		t.Fatalf("first fault should not exhaust: %+v", dec)
	}
	if dec.NextPool != "backup" {
		t.Errorf("NextPool = %q, want backup", dec.NextPool)
	}
	if dec.RotationCount != 1 {
		t.Errorf("RotationCount = %d, want 1", dec.RotationCount)
	}
	if !c.CoolingDown("s1", testProvider, "primary") {
		t.Errorf("primary should be cooling down after fault")
	}
}

// spec: §4.9 line 1321 — maxRotationsPerSession is shared across
// providers; exceeding it exhausts the chain.
func TestControllerExhaustsOnRotationBudget(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(2, time.Hour)
	c.SetClock(fixedClock(t0))
	// A long chain so selection never runs dry before the budget does.
	c.RegisterChain("s1", testProvider, []string{"a", "b", "c", "d", "e"})

	if d := c.Fault("s1", testProvider, "a", credential.TriggerFaultRateLimited); d.Exhausted {
		t.Fatalf("fault 1 exhausted early: %+v", d)
	}
	if d := c.Fault("s1", testProvider, "b", credential.TriggerFaultAuthExpired); d.Exhausted {
		t.Fatalf("fault 2 exhausted early: %+v", d)
	}
	d := c.Fault("s1", testProvider, "c", credential.TriggerFaultProviderUnavailable)
	if !d.Exhausted {
		t.Fatalf("fault 3 should exhaust the budget of 2: %+v", d)
	}
	if d.RotationCount != 3 {
		t.Errorf("RotationCount = %d, want 3", d.RotationCount)
	}
	if d.NextPool != "" {
		t.Errorf("exhausted decision must carry no NextPool, got %q", d.NextPool)
	}
}

// spec: §4.9 line 1321 — the counter is shared across providers, so
// faults on different providers consume the same session budget.
func TestControllerBudgetSharedAcrossProviders(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(2, time.Hour)
	c.SetClock(fixedClock(t0))
	c.RegisterChain("s1", credential.ProviderAnthropicDirect, []string{"a1", "a2", "a3"})
	c.RegisterChain("s1", credential.ProviderAWSBedrock, []string{"b1", "b2", "b3"})

	c.Fault("s1", credential.ProviderAnthropicDirect, "a1", credential.TriggerFaultRateLimited)
	c.Fault("s1", credential.ProviderAWSBedrock, "b1", credential.TriggerFaultRateLimited)
	d := c.Fault("s1", credential.ProviderAnthropicDirect, "a2", credential.TriggerFaultRateLimited)
	if !d.Exhausted {
		t.Fatalf("third fault across two providers should exhaust shared budget of 2: %+v", d)
	}
}

// spec: §4.9 line 1383 — fallback is per-provider; a fault on one
// provider does not cool another provider's pools.
func TestControllerFaultIsolatedPerProvider(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(5, time.Hour)
	c.SetClock(fixedClock(t0))
	c.RegisterChain("s1", credential.ProviderAnthropicDirect, []string{"a1", "a2"})
	c.RegisterChain("s1", credential.ProviderAWSBedrock, []string{"b1", "b2"})

	c.Fault("s1", credential.ProviderAnthropicDirect, "a1", credential.TriggerFaultRateLimited)
	if c.CoolingDown("s1", credential.ProviderAWSBedrock, "b1") {
		t.Errorf("bedrock pool must not cool from an anthropic fault")
	}
}

// spec: §4.9 line 1411 — a single-pool deployment with no fallback
// order exhausts once its only pool faults (nothing left to select).
func TestControllerSinglePoolExhaustsWithNoFallback(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(3, time.Hour)
	c.SetClock(fixedClock(t0))
	// No RegisterChain: the faulted pool seeds a single-pool chain.
	d := c.Fault("s1", testProvider, "solo", credential.TriggerFaultRateLimited)
	if !d.Exhausted {
		t.Fatalf("single-pool fault should exhaust (no fallback): %+v", d)
	}
	if len(d.ChainAttempted) != 1 || d.ChainAttempted[0] != "solo" {
		t.Errorf("ChainAttempted = %v, want [solo]", d.ChainAttempted)
	}
}

// spec: §4.9 Proactive Lease Renewal — proactive_renewal does not count
// against the rotation budget, so it never drives exhaustion by itself.
func TestControllerProactiveRenewalDoesNotConsumeBudget(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(1, time.Hour)
	c.SetClock(fixedClock(t0))
	c.RegisterChain("s1", testProvider, []string{"a", "b", "c"})

	c.Fault("s1", testProvider, "a", credential.TriggerProactiveRenewal)
	c.Fault("s1", testProvider, "b", credential.TriggerProactiveRenewal)
	if got := c.RotationCount("s1"); got != 0 {
		t.Errorf("RotationCount = %d, want 0 (proactive renewal excluded)", got)
	}
}

// spec: §4.9 lines 1383-1411 — after every pool in the chain is on
// cooldown, the chain is exhausted even within the rotation budget.
func TestControllerExhaustsWhenAllPoolsCooling(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := NewController(10, time.Hour)
	c.SetClock(fixedClock(t0))
	c.RegisterChain("s1", testProvider, []string{"a", "b"})

	if d := c.Fault("s1", testProvider, "a", credential.TriggerFaultRateLimited); d.NextPool != "b" {
		t.Fatalf("fault a should select b: %+v", d)
	}
	d := c.Fault("s1", testProvider, "b", credential.TriggerFaultRateLimited)
	if !d.Exhausted {
		t.Fatalf("with a and b both cooling, chain should exhaust: %+v", d)
	}
}

// spec: §4.9 cooldownOnRateLimit — a cooled pool becomes selectable
// again once its cooldown elapses.
func TestControllerCooldownExpiry(t *testing.T) {
	t0 := time.Unix(1000, 0)
	clk := t0
	c := NewController(10, 60*time.Second)
	c.SetClock(func() time.Time { return clk })
	c.RegisterChain("s1", testProvider, []string{"a", "b"})

	c.Fault("s1", testProvider, "a", credential.TriggerFaultRateLimited)
	clk = t0.Add(61 * time.Second)
	if c.CoolingDown("s1", testProvider, "a") {
		t.Errorf("pool a should be selectable after cooldown elapses")
	}
}

// spec: §7.1 session release — Release drops per-session state.
func TestControllerRelease(t *testing.T) {
	c := NewController(3, time.Hour)
	c.RegisterChain("s1", testProvider, []string{"a", "b"})
	c.Fault("s1", testProvider, "a", credential.TriggerFaultRateLimited)
	c.Release("s1")
	if got := c.RotationCount("s1"); got != 0 {
		t.Errorf("RotationCount after release = %d, want 0", got)
	}
}

// New with non-positive bounds applies the documented defaults.
func TestControllerDefaults(t *testing.T) {
	c := NewController(0, 0)
	if c.maxRotations != DefaultMaxRotations {
		t.Errorf("maxRotations = %d, want %d", c.maxRotations, DefaultMaxRotations)
	}
}
