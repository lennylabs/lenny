// SPDX-License-Identifier: MIT

package credential

import (
	"fmt"
	"sort"
)

// PreferredSource is the §4.9 credentialPolicy.preferredSource enum: it
// decides which of the Three Credential Modes the gateway uses when it
// resolves a provider's credential at session creation.
//
// spec: §4.9 lines 1310, 1336 — "pool | user | prefer-user-then-pool |
// prefer-pool-then-user".
type PreferredSource string

const (
	// PreferredSourcePool uses admin-managed pool credentials only.
	PreferredSourcePool PreferredSource = "pool"
	// PreferredSourceUser uses user-scoped credentials only; a missing
	// user credential is terminal (USER_CREDENTIAL_NOT_FOUND).
	PreferredSourceUser PreferredSource = "user"
	// PreferUserThenPool tries the user credential, then falls through
	// to the provider's pool chain on miss.
	PreferUserThenPool PreferredSource = "prefer-user-then-pool"
	// PreferPoolThenUser tries the provider's pool chain, then falls
	// through to the user credential when no pool has an assignable
	// credential.
	PreferPoolThenUser PreferredSource = "prefer-pool-then-user"
)

// AllPreferredSources returns the closed §4.9 enum in spec order.
func AllPreferredSources() []PreferredSource {
	return []PreferredSource{
		PreferredSourcePool, PreferredSourceUser,
		PreferUserThenPool, PreferPoolThenUser,
	}
}

// IsValid reports whether p is one of the four §4.9 values. The empty
// string is not valid here; callers that treat an unset preferredSource
// as the pool default use Normalize first.
func (p PreferredSource) IsValid() bool {
	for _, v := range AllPreferredSources() {
		if p == v {
			return true
		}
	}
	return false
}

// Normalize maps an unset preferredSource to the pool default per the
// §4.9 example (preferredSource: pool). A set value is returned
// unchanged.
func (p PreferredSource) Normalize() PreferredSource {
	if p == "" {
		return PreferredSourcePool
	}
	return p
}

// SourceOrder returns the ordered list of credential sources the
// CredentialRouter tries for this preferredSource. spec: §4.9 lines
// 1328-1336 (Three Credential Modes) and 1362 (resolution order).
func (p PreferredSource) SourceOrder() []LeaseSource {
	switch p.Normalize() {
	case PreferredSourceUser:
		return []LeaseSource{SourceUser}
	case PreferUserThenPool:
		return []LeaseSource{SourceUser, SourcePool}
	case PreferPoolThenUser:
		return []LeaseSource{SourcePool, SourceUser}
	default: // PreferredSourcePool
		return []LeaseSource{SourcePool}
	}
}

// UsesUserCredentials reports whether this preferredSource ever
// resolves to a user-scoped credential. spec: §4.9 line 1362.
func (p PreferredSource) UsesUserCredentials() bool {
	switch p.Normalize() {
	case PreferredSourceUser, PreferUserThenPool, PreferPoolThenUser:
		return true
	default:
		return false
	}
}

// UserMissIsTerminal reports whether a missing user credential ends
// resolution with USER_CREDENTIAL_NOT_FOUND rather than falling through
// to a pool. Only PreferredSourceUser (user-only) is terminal; the
// prefer-* modes fall through. spec: §4.9 lines 1364, 1370.
func (p PreferredSource) UserMissIsTerminal() bool {
	return p.Normalize() == PreferredSourceUser
}

// DefaultMaxRotationsPerSession is the §4.9 credentialPolicy
// fallback.maxRotationsPerSession default applied when the field is
// unset. spec: §4.9 line 1322 ("maxRotationsPerSession: 3").
const DefaultMaxRotationsPerSession = 3

// DefaultCooldownOnRateLimitSeconds is the §4.9 credentialPolicy
// fallback.cooldownOnRateLimit default applied when the field is unset.
// spec: §4.9 line 1321 ("cooldownOnRateLimit: 60s").
const DefaultCooldownOnRateLimitSeconds = 60

// ProviderFallback is the §4.9 providerPools.{provider}.fallback block:
// the ordered list of pools the gateway walks for one provider, primary
// first. spec: §4.9 lines 1314-1319.
type ProviderFallback struct {
	Order []string `json:"order,omitempty"`
}

// ProviderPool is the §4.9 credentialPolicy.providerPools entry for one
// provider: the default pool and the ordered fallback chain of pools
// the gateway walks for that provider. spec: §4.9 lines 1311-1319.
type ProviderPool struct {
	// DefaultPool is the provider's primary credential pool.
	DefaultPool string `json:"defaultPool,omitempty"`
	// Fallback is the §4.9 providerPools.{provider}.fallback block: the
	// pools tried in priority order, primary first. When the order is
	// empty, the chain is the single DefaultPool.
	Fallback ProviderFallback `json:"fallback,omitempty"`
}

// PoolOrder returns the provider's pools in §4.9 fallback priority
// order: fallback.order when set, otherwise the single defaultPool. An
// entry with neither returns an empty slice.
func (pp ProviderPool) PoolOrder() []string {
	if len(pp.Fallback.Order) > 0 {
		return append([]string(nil), pp.Fallback.Order...)
	}
	if pp.DefaultPool != "" {
		return []string{pp.DefaultPool}
	}
	return nil
}

func (pp ProviderPool) clone() ProviderPool {
	cp := pp
	if pp.Fallback.Order != nil {
		cp.Fallback.Order = append([]string(nil), pp.Fallback.Order...)
	}
	return cp
}

// PolicyFallback is the §4.9 credentialPolicy.fallback block: the
// cooldown applied to a faulted pool and the session-wide rotation
// budget shared across all providers. spec: §4.9 lines 1320-1322.
type PolicyFallback struct {
	// CooldownOnRateLimitSeconds is the §4.9 cooldownOnRateLimit: how
	// long a pool stays on cooldown after a fault. Zero selects
	// DefaultCooldownOnRateLimitSeconds at use time.
	CooldownOnRateLimitSeconds int `json:"cooldownOnRateLimitSeconds,omitempty"`
	// MaxRotationsPerSession is the §4.9 total rotation budget across
	// all providers in a session. Zero selects
	// DefaultMaxRotationsPerSession at use time.
	MaxRotationsPerSession int `json:"maxRotationsPerSession,omitempty"`
}

// EffectiveCooldownSeconds returns CooldownOnRateLimitSeconds, or the
// default when the field is unset.
func (f PolicyFallback) EffectiveCooldownSeconds() int {
	if f.CooldownOnRateLimitSeconds <= 0 {
		return DefaultCooldownOnRateLimitSeconds
	}
	return f.CooldownOnRateLimitSeconds
}

// EffectiveMaxRotations returns MaxRotationsPerSession, or the default
// when the field is unset.
func (f PolicyFallback) EffectiveMaxRotations() int {
	if f.MaxRotationsPerSession <= 0 {
		return DefaultMaxRotationsPerSession
	}
	return f.MaxRotationsPerSession
}

// CredentialPolicy is the §4.9 tenant-level credentialPolicy attached
// to the tenant configuration. It declares what credentials are
// available and how they are sourced; the gateway intersects it with a
// Runtime's supportedProviders at session creation. spec: §4.9 lines
// 1303-1336.
type CredentialPolicy struct {
	// PreferredSource decides the credential mode. An empty value is the
	// pool default.
	PreferredSource PreferredSource `json:"preferredSource,omitempty"`
	// ProviderPools maps a provider identifier to its pool chain. The
	// intersection of these keys with a Runtime's supportedProviders is
	// the set of providers eligible for credential assignment.
	ProviderPools map[string]ProviderPool `json:"providerPools,omitempty"`
	// Fallback is the session-wide cooldown and rotation-budget block.
	Fallback PolicyFallback `json:"fallback,omitempty"`
	// UserCredentialsEnabled gates whether user-scoped credentials
	// (registered via POST /v1/credentials) are used. spec: §4.9 lines
	// 1368-1371.
	UserCredentialsEnabled bool `json:"userCredentialsEnabled,omitempty"`
}

// Configured reports whether the tenant has set any credentialPolicy
// field. A zero-value policy means the tenant configured none and the
// session-creation intersection is empty.
func (c CredentialPolicy) Configured() bool {
	return c.PreferredSource != "" ||
		len(c.ProviderPools) > 0 ||
		c.Fallback.CooldownOnRateLimitSeconds != 0 ||
		c.Fallback.MaxRotationsPerSession != 0 ||
		c.UserCredentialsEnabled
}

// Clone returns a deep copy so a stored policy never shares its
// ProviderPools map or FallbackOrder slices with a caller.
func (c CredentialPolicy) Clone() CredentialPolicy {
	cp := c
	if c.ProviderPools != nil {
		cp.ProviderPools = make(map[string]ProviderPool, len(c.ProviderPools))
		for k, v := range c.ProviderPools {
			cp.ProviderPools[k] = v.clone()
		}
	}
	return cp
}

// Providers returns the providerPools keys in sorted order, giving the
// §4.9 intersection a deterministic provider iteration.
func (c CredentialPolicy) Providers() []string {
	out := make([]string, 0, len(c.ProviderPools))
	for k := range c.ProviderPools {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PoolOrderFor returns the §4.9 fallback-ordered pools for a provider,
// or nil when the provider is absent from the policy.
func (c CredentialPolicy) PoolOrderFor(provider string) []string {
	pp, ok := c.ProviderPools[provider]
	if !ok {
		return nil
	}
	return pp.PoolOrder()
}

// Validate reports the §4.9 admission errors for a credentialPolicy at
// the admin path. It returns nil on success and a *PolicyError listing
// every violation otherwise. An empty policy is valid (the tenant
// configures no credential sourcing).
func (c CredentialPolicy) Validate() error {
	var v []string
	if c.PreferredSource != "" && !c.PreferredSource.IsValid() {
		v = append(v, fmt.Sprintf("preferredSource %q must be one of %v", c.PreferredSource, AllPreferredSources()))
	}
	for _, provider := range c.Providers() {
		pp := c.ProviderPools[provider]
		if len(pp.PoolOrder()) == 0 {
			v = append(v, fmt.Sprintf("providerPools[%q] must set defaultPool or a non-empty fallback.order", provider))
		}
	}
	if c.Fallback.CooldownOnRateLimitSeconds < 0 {
		v = append(v, "fallback.cooldownOnRateLimit must be >= 0")
	}
	if c.Fallback.MaxRotationsPerSession < 0 {
		v = append(v, "fallback.maxRotationsPerSession must be >= 0")
	}
	if len(v) == 0 {
		return nil
	}
	return &PolicyError{Violations: v}
}

// PolicyError captures CredentialPolicy.Validate failures.
type PolicyError struct {
	Violations []string
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("credential: invalid credentialPolicy: %v", e.Violations)
}
