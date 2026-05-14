// SPDX-License-Identifier: MIT

package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestAllStatesIsExhaustive(t *testing.T) {
	if got := len(AllStates()); got != 2 {
		t.Errorf("AllStates() returned %d, want 2", got)
	}
}

func TestAllTiersIsExhaustive(t *testing.T) {
	if got := len(AllTiers()); got != 4 {
		t.Errorf("AllTiers() returned %d, want 4 per §11.6", got)
	}
	if LimitTier("tenant").IsValid() {
		t.Errorf("§11.6 explicitly notes tier vocabulary is closed; tenant is NOT a tier")
	}
}

func TestAllOperationTypesIsExhaustive(t *testing.T) {
	if got := len(AllOperationTypes()); got != 4 {
		t.Errorf("AllOperationTypes() returned %d, want 4 per §11.6", got)
	}
}

func TestValidateAcceptsWellFormedBreakers(t *testing.T) {
	cases := []Breaker{
		{Name: "runtime-x-degraded", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}},
		{Name: "pool-y-full", State: StateOpen, LimitTier: TierPool, Scope: Scope{Pool: "premium"}},
		{Name: "github-down", State: StateClosed, LimitTier: TierConnector, Scope: Scope{Connector: "github"}},
		{Name: "uploads-paused", State: StateOpen, LimitTier: TierOperationType, Scope: Scope{OperationType: OpUploads}},
	}
	for _, b := range cases {
		if err := b.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", b, err)
		}
	}
}

func TestValidateRejectsMismatchedScope(t *testing.T) {
	cases := []struct {
		name string
		b    Breaker
	}{
		{"runtime tier with pool field", Breaker{Name: "x", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Pool: "p"}}},
		{"pool tier with runtime field", Breaker{Name: "x", State: StateOpen, LimitTier: TierPool, Scope: Scope{Runtime: "r"}}},
		{"connector tier missing field", Breaker{Name: "x", State: StateOpen, LimitTier: TierConnector, Scope: Scope{}}},
		{"operation_type with bad value", Breaker{Name: "x", State: StateOpen, LimitTier: TierOperationType, Scope: Scope{OperationType: "bogus"}}},
		{"operation_type with extra field", Breaker{Name: "x", State: StateOpen, LimitTier: TierOperationType, Scope: Scope{OperationType: OpUploads, Pool: "p"}}},
		{"empty name", Breaker{State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "r"}}},
		{"unknown state", Breaker{Name: "x", State: "half-open", LimitTier: TierRuntime, Scope: Scope{Runtime: "r"}}},
		{"unknown tier", Breaker{Name: "x", State: StateOpen, LimitTier: "tenant", Scope: Scope{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.b.Validate()
			if err == nil {
				t.Fatalf("expected error")
			}
			var se *ScopeError
			if !errors.As(err, &se) {
				t.Errorf("expected *ScopeError, got %T", err)
			}
		})
	}
}

func TestMatchClosedBreakerNeverRejects(t *testing.T) {
	b := Breaker{Name: "x", State: StateClosed, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}}
	if b.Match(Request{Runtime: "claude-code"}) {
		t.Errorf("closed breaker must never match")
	}
}

func TestMatchRuntimeTier(t *testing.T) {
	b := Breaker{Name: "x", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}}
	if !b.Match(Request{Runtime: "claude-code"}) {
		t.Errorf("runtime breaker must match same runtime")
	}
	if b.Match(Request{Runtime: "other-runtime"}) {
		t.Errorf("runtime breaker must not match different runtime")
	}
	if b.Match(Request{Pool: "any"}) {
		t.Errorf("runtime breaker must not match on pool")
	}
}

func TestMatchPoolTier(t *testing.T) {
	b := Breaker{Name: "x", State: StateOpen, LimitTier: TierPool, Scope: Scope{Pool: "premium"}}
	if !b.Match(Request{Pool: "premium"}) {
		t.Errorf("pool breaker must match same pool")
	}
	if b.Match(Request{Pool: "default"}) {
		t.Errorf("pool breaker must not match different pool")
	}
}

func TestMatchConnectorTier(t *testing.T) {
	b := Breaker{Name: "x", State: StateOpen, LimitTier: TierConnector, Scope: Scope{Connector: "github"}}
	if !b.Match(Request{Connector: "github"}) {
		t.Errorf("connector breaker must match same connector")
	}
	if b.Match(Request{Connector: "gitlab"}) {
		t.Errorf("connector breaker must not match different connector")
	}
}

func TestMatchOperationTypeTier(t *testing.T) {
	b := Breaker{Name: "x", State: StateOpen, LimitTier: TierOperationType, Scope: Scope{OperationType: OpUploads}}
	if !b.Match(Request{OperationType: OpUploads}) {
		t.Errorf("op-type breaker must match same op")
	}
	if b.Match(Request{OperationType: OpSessionCreation}) {
		t.Errorf("op-type breaker must not match different op")
	}
}

func TestFirstMatchReturnsFirstOpenMatch(t *testing.T) {
	breakers := []Breaker{
		{Name: "a", State: StateClosed, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}},
		{Name: "b", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}},
		{Name: "c", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}},
	}
	got := FirstMatch(breakers, Request{Runtime: "claude-code"})
	if got == nil {
		t.Fatalf("expected a match")
	}
	if got.Name != "b" {
		t.Errorf("FirstMatch should return the first open match (b), got %s", got.Name)
	}
}

func TestFirstMatchReturnsNilWhenNoneMatch(t *testing.T) {
	breakers := []Breaker{
		{Name: "a", State: StateOpen, LimitTier: TierPool, Scope: Scope{Pool: "premium"}},
	}
	if got := FirstMatch(breakers, Request{Runtime: "claude-code"}); got != nil {
		t.Errorf("non-matching breakers should return nil, got %+v", got)
	}
}

func TestScopeMatches(t *testing.T) {
	a := Breaker{LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}}
	b := Breaker{LimitTier: TierRuntime, Scope: Scope{Runtime: "claude-code"}}
	c := Breaker{LimitTier: TierRuntime, Scope: Scope{Runtime: "other"}}
	d := Breaker{LimitTier: TierPool, Scope: Scope{Pool: "claude-code"}}
	if !ScopeMatches(a, b) {
		t.Errorf("identical (tier, scope) must match")
	}
	if ScopeMatches(a, c) {
		t.Errorf("different scope values must not match")
	}
	if ScopeMatches(a, d) {
		t.Errorf("different tiers must not match")
	}
}

// The OpenedAt timestamp is preserved on a Breaker; the package
// itself does not interpret it but tests serve as a check that the
// field exists on the public API.
func TestBreakerOpenedAtField(t *testing.T) {
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := Breaker{Name: "x", State: StateOpen, LimitTier: TierRuntime, Scope: Scope{Runtime: "r"}, OpenedAt: when}
	if !b.OpenedAt.Equal(when) {
		t.Errorf("OpenedAt round-trip lost: %v vs %v", b.OpenedAt, when)
	}
}
