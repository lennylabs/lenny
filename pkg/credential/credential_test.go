// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"testing"
)

func TestAllProvidersIsExhaustive(t *testing.T) {
	if got := len(AllProviders()); got != 6 {
		t.Errorf("AllProviders() returned %d, want 6 per §4.9 table", got)
	}
	for _, p := range AllProviders() {
		if !p.IsValid() {
			t.Errorf("AllProviders() returned invalid value %q", p)
		}
	}
	if Provider("custom-x").IsValid() {
		t.Errorf("unknown provider must not be IsValid for the built-in surface")
	}
}

func TestAllAssignmentStrategiesIsExhaustive(t *testing.T) {
	got := AllAssignmentStrategies()
	if len(got) != 3 {
		t.Errorf("AllAssignmentStrategies() returned %d, want 3 per §4.9", len(got))
	}
	for _, s := range got {
		if !s.IsValid() {
			t.Errorf("AllAssignmentStrategies() returned invalid %q", s)
		}
	}
}

func TestLeaseSourceIsValid(t *testing.T) {
	if !SourcePool.IsValid() || !SourceUser.IsValid() {
		t.Errorf("both pool and user must be IsValid sources")
	}
	if LeaseSource("foo").IsValid() {
		t.Errorf("unknown source must not be IsValid")
	}
}

func TestAllRotationTriggersIsExhaustive(t *testing.T) {
	got := AllRotationTriggers()
	if len(got) != 7 {
		t.Errorf("AllRotationTriggers() returned %d, want 7 per §4.9", len(got))
	}
	seen := map[RotationTrigger]bool{}
	for _, tt := range got {
		seen[tt] = true
	}
	for _, want := range []RotationTrigger{
		TriggerProactiveRenewal,
		TriggerFaultRateLimited,
		TriggerFaultAuthExpired,
		TriggerFaultProviderUnavailable,
		TriggerEmergencyRevocation,
		TriggerUserCredentialRotated,
		TriggerUserCredentialRevoked,
	} {
		if !seen[want] {
			t.Errorf("AllRotationTriggers() missing %q", want)
		}
	}
}

func TestRotationTriggerIsFaultTriggered(t *testing.T) {
	faultCases := []RotationTrigger{
		TriggerFaultRateLimited,
		TriggerFaultAuthExpired,
		TriggerFaultProviderUnavailable,
	}
	for _, tt := range faultCases {
		if !tt.IsFaultTriggered() {
			t.Errorf("%q must be fault-triggered", tt)
		}
	}
	nonFault := []RotationTrigger{
		TriggerProactiveRenewal,
		TriggerEmergencyRevocation,
		TriggerUserCredentialRotated,
		TriggerUserCredentialRevoked,
	}
	for _, tt := range nonFault {
		if tt.IsFaultTriggered() {
			t.Errorf("%q must NOT be fault-triggered", tt)
		}
	}
}

func TestRotationTriggerIsCeilingApplicable(t *testing.T) {
	// §4.7: only proactive_renewal escapes the ceiling.
	if TriggerProactiveRenewal.IsCeilingApplicable() {
		t.Errorf("proactive_renewal must NOT be ceiling-applicable")
	}
	for _, tt := range []RotationTrigger{
		TriggerFaultRateLimited,
		TriggerFaultAuthExpired,
		TriggerFaultProviderUnavailable,
		TriggerEmergencyRevocation,
		TriggerUserCredentialRotated,
		TriggerUserCredentialRevoked,
	} {
		if !tt.IsCeilingApplicable() {
			t.Errorf("%q must be ceiling-applicable per §4.7", tt)
		}
	}
}

func TestRotationTriggerCountsAgainstBudget(t *testing.T) {
	// §4.9 Proactive Lease Renewal: only fault-driven and operator/
	// user-initiated rotations consume maxRotationsPerSession.
	if TriggerProactiveRenewal.CountsAgainstRotationBudget() {
		t.Errorf("proactive_renewal must NOT count against rotation budget")
	}
	for _, tt := range []RotationTrigger{
		TriggerFaultRateLimited,
		TriggerEmergencyRevocation,
		TriggerUserCredentialRotated,
	} {
		if !tt.CountsAgainstRotationBudget() {
			t.Errorf("%q must count against rotation budget", tt)
		}
	}
}

func TestPoolConfigValidateHappy(t *testing.T) {
	cfg := PoolConfig{
		Name:                  "claude-direct-prod",
		Provider:              ProviderAnthropicDirect,
		AssignmentStrategy:    StrategyLeastLoaded,
		MaxConcurrentSessions: 10,
		CooldownOnRateLimit:   60,
		LeaseTTLSeconds:       3600,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("well-formed PoolConfig should validate, got %v", err)
	}
}

func TestPoolConfigValidateRejectsBadFields(t *testing.T) {
	cases := []struct {
		name    string
		cfg     PoolConfig
		mustHit string
	}{
		{"missing name", PoolConfig{Provider: ProviderAnthropicDirect, AssignmentStrategy: StrategyLeastLoaded, MaxConcurrentSessions: 1}, "name is required"},
		{"unknown provider", PoolConfig{Name: "x", Provider: "bogus", AssignmentStrategy: StrategyLeastLoaded, MaxConcurrentSessions: 1}, "not a built-in provider"},
		{"unknown strategy", PoolConfig{Name: "x", Provider: ProviderAnthropicDirect, AssignmentStrategy: "fastest", MaxConcurrentSessions: 1}, "assignmentStrategy"},
		{"zero maxConcurrent", PoolConfig{Name: "x", Provider: ProviderAnthropicDirect, AssignmentStrategy: StrategyLeastLoaded, MaxConcurrentSessions: 0}, "maxConcurrentSessions"},
		{"negative ttl", PoolConfig{Name: "x", Provider: ProviderAnthropicDirect, AssignmentStrategy: StrategyLeastLoaded, MaxConcurrentSessions: 1, LeaseTTLSeconds: -1}, "leaseTTLSeconds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Errorf("expected *ConfigError, got %T", err)
			}
			matched := false
			for _, v := range ce.Violations {
				if containsAny(v, c.mustHit) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("expected violation containing %q, got %v", c.mustHit, ce.Violations)
			}
		})
	}
}

func containsAny(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || stringContains(s, sub)))
}

func stringContains(haystack, needle string) bool {
	n := len(needle)
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return true
		}
	}
	return false
}
