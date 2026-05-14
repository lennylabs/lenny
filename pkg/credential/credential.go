// SPDX-License-Identifier: MIT

// Package credential encodes the §4.9 credential-leasing primitives
// that the controller, the Token Service, and the gateway all share:
// the Provider enum, the AssignmentStrategy enum, the LeaseSource
// enum, and the RotationTrigger enum with the §4.7 rotation-ceiling
// semantics and the §4.9 proactive-renewal exclusion.
//
// The full Credential Leasing Service (Token Service binding, pool
// admin endpoints, KMS rotation, materializedConfig schemas) lives in
// later phases that import this package.
package credential

import "fmt"

// Provider is the built-in CredentialProvider enum from §4.9. Custom
// providers may extend the enum at runtime via the pluggable
// CredentialProvider interface; this package enumerates the built-ins
// only.
type Provider string

const (
	ProviderAnthropicDirect Provider = "anthropic_direct"
	ProviderAWSBedrock      Provider = "aws_bedrock"
	ProviderVertexAI        Provider = "vertex_ai"
	ProviderAzureOpenAI     Provider = "azure_openai"
	ProviderGitHub          Provider = "github"
	ProviderVaultTransit    Provider = "vault_transit"
)

// AllProviders returns every built-in provider in §4.9 table order.
func AllProviders() []Provider {
	return []Provider{
		ProviderAnthropicDirect, ProviderAWSBedrock, ProviderVertexAI,
		ProviderAzureOpenAI, ProviderGitHub, ProviderVaultTransit,
	}
}

// IsValid reports whether p is one of the built-in providers. Returns
// true for built-ins, false for unknown values. Custom providers (any
// string outside the closed enum) are valid at runtime but require
// runtime-side registration; this method is for callers that want to
// gate on the built-in surface.
func (p Provider) IsValid() bool {
	for _, v := range AllProviders() {
		if p == v {
			return true
		}
	}
	return false
}

// AssignmentStrategy controls how the credential pool selects which
// credential to lease for a new session per §4.9.
type AssignmentStrategy string

const (
	StrategyLeastLoaded        AssignmentStrategy = "least-loaded"
	StrategyRoundRobin         AssignmentStrategy = "round-robin"
	StrategyStickyUntilFailure AssignmentStrategy = "sticky-until-failure"
)

// AllAssignmentStrategies returns the closed enum.
func AllAssignmentStrategies() []AssignmentStrategy {
	return []AssignmentStrategy{StrategyLeastLoaded, StrategyRoundRobin, StrategyStickyUntilFailure}
}

// IsValid reports whether s is one of the three §4.9 strategies.
func (s AssignmentStrategy) IsValid() bool {
	for _, v := range AllAssignmentStrategies() {
		if s == v {
			return true
		}
	}
	return false
}

// LeaseSource is the §4.9 CredentialLease.source enum.
type LeaseSource string

const (
	SourcePool LeaseSource = "pool"
	SourceUser LeaseSource = "user"
)

// IsValid reports whether s is one of the two §4.9 sources.
func (s LeaseSource) IsValid() bool {
	return s == SourcePool || s == SourceUser
}

// RotationTrigger is the §4.9 rotationTrigger enum carried on every
// RotateCredentials RPC.
type RotationTrigger string

const (
	// TriggerProactiveRenewal: §4.9 Proactive Lease Renewal loop.
	// Lease is approaching renewBefore; no fault. Old credential is
	// still valid. Subject to the unbounded in-flight wait per §4.7.
	TriggerProactiveRenewal RotationTrigger = "proactive_renewal"

	// TriggerFaultRateLimited: Fallback Flow step 1 RATE_LIMITED.
	TriggerFaultRateLimited RotationTrigger = "fault_rate_limited"

	// TriggerFaultAuthExpired: Fallback Flow step 1 AUTH_EXPIRED.
	TriggerFaultAuthExpired RotationTrigger = "fault_auth_expired"

	// TriggerFaultProviderUnavailable: Fallback Flow step 1
	// PROVIDER_UNAVAILABLE.
	TriggerFaultProviderUnavailable RotationTrigger = "fault_provider_unavailable"

	// TriggerEmergencyRevocation: operator revoked a pool credential.
	TriggerEmergencyRevocation RotationTrigger = "emergency_revocation"

	// TriggerUserCredentialRotated: user replaced the secret material
	// of a user-scoped credential.
	TriggerUserCredentialRotated RotationTrigger = "user_credential_rotated"

	// TriggerUserCredentialRevoked: user revoked a user-scoped
	// credential.
	TriggerUserCredentialRevoked RotationTrigger = "user_credential_revoked"
)

// AllRotationTriggers returns the closed §4.9 enum in spec table order.
func AllRotationTriggers() []RotationTrigger {
	return []RotationTrigger{
		TriggerProactiveRenewal,
		TriggerFaultRateLimited,
		TriggerFaultAuthExpired,
		TriggerFaultProviderUnavailable,
		TriggerEmergencyRevocation,
		TriggerUserCredentialRotated,
		TriggerUserCredentialRevoked,
	}
}

// IsValid reports whether t is one of the seven §4.9 triggers.
func (t RotationTrigger) IsValid() bool {
	for _, v := range AllRotationTriggers() {
		if t == v {
			return true
		}
	}
	return false
}

// IsFaultTriggered reports whether the trigger originated in the
// Fallback Flow's step 1 (upstream provider error). Used by metric
// labels and audit-event filtering per §4.9.
func (t RotationTrigger) IsFaultTriggered() bool {
	switch t {
	case TriggerFaultRateLimited, TriggerFaultAuthExpired, TriggerFaultProviderUnavailable:
		return true
	}
	return false
}

// IsCeilingApplicable reports whether the §4.7 300-second in-flight
// gate ceiling applies to this trigger. The spec says: "Only
// proactive_renewal rotations carry an unbounded in-flight wait;
// every other trigger is subject to the 300-second ceiling because
// the old credential is no longer trustworthy."
func (t RotationTrigger) IsCeilingApplicable() bool {
	return t != TriggerProactiveRenewal
}

// CountsAgainstRotationBudget reports whether this rotation
// increments the session's rotationCount and consumes
// maxRotationsPerSession. The §4.9 Proactive Lease Renewal section
// says: "Renewal does not consume maxRotationsPerSession. The
// rotation counter is incremented only for fault-driven rotations."
// In practice this means proactive_renewal is excluded; operator-
// and user-initiated rotations follow the fault rule path.
func (t RotationTrigger) CountsAgainstRotationBudget() bool {
	return t != TriggerProactiveRenewal
}

// String returns the canonical wire value for the trigger.
func (t RotationTrigger) String() string { return string(t) }

// PoolConfig captures the deployer-facing CredentialPool fields the
// admin path validates at create time. The full record carries
// additional Kubernetes-typed fields (SecretRef, RoleARN, etc.) that
// require the K8s API; this struct is the subset the validator below
// can reason about purely.
type PoolConfig struct {
	Name                  string
	Provider              Provider
	AssignmentStrategy    AssignmentStrategy
	MaxConcurrentSessions int
	CooldownOnRateLimit   int // seconds
	LeaseTTLSeconds       int // optional override; 0 means use provider default
}

// Validate reports the §4.9 admission errors for a PoolConfig at the
// admin path. Returns nil on success and a *ConfigError listing every
// violation otherwise.
func (c PoolConfig) Validate() error {
	v := []string{}
	if c.Name == "" {
		v = append(v, "name is required")
	}
	if !c.Provider.IsValid() {
		// Custom providers are not handled here; this validator is for
		// the built-in surface.
		v = append(v, fmt.Sprintf("provider %q is not a built-in provider", c.Provider))
	}
	if !c.AssignmentStrategy.IsValid() {
		v = append(v, fmt.Sprintf("assignmentStrategy %q must be one of %v", c.AssignmentStrategy, AllAssignmentStrategies()))
	}
	if c.MaxConcurrentSessions <= 0 {
		v = append(v, "maxConcurrentSessions must be > 0")
	}
	if c.CooldownOnRateLimit < 0 {
		v = append(v, "cooldownOnRateLimit must be >= 0")
	}
	if c.LeaseTTLSeconds < 0 {
		v = append(v, "leaseTTLSeconds must be >= 0 (0 means provider default)")
	}
	if len(v) == 0 {
		return nil
	}
	return &ConfigError{Name: c.Name, Violations: v}
}

// ConfigError captures PoolConfig.Validate failures.
type ConfigError struct {
	Name       string
	Violations []string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("credential: pool %q: %v", e.Name, e.Violations)
}
