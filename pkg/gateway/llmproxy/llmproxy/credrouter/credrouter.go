// SPDX-License-Identifier: MIT

// Package credrouter implements the §4.9 CredentialRouter: the
// selection layer that decides, for a (tenant, user, provider) triple,
// which credential source (pool or user) and which pool the gateway
// leases from. It is invoked at two points: session creation, when the
// gateway assigns initial credential leases, and rotation time, when it
// selects a replacement credential for a failed provider.
//
// The router resolves the source and the pool; the §4.9 pool
// assignmentStrategy (in pkg/gateway/credassign) then selects the
// credential within the chosen pool. The package also exposes the §4.9
// provider Intersection and the §4.9 PreClaim availability check the
// gateway runs before claiming a warm pod.
//
// spec: §4.9 lines 1558-1591 (CredentialRouter interface), 1303-1336
// (Credential Policy, Three Credential Modes, Intersection), 1216-1220
// (Pre-Claim Credential Availability Check).
package credrouter

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/credential"
)

// PoolDescriptor is a §4.9 CredentialRouter input pool: a pool eligible
// for a provider together with the current utilization stats the
// router and the pre-claim check evaluate. spec: §4.9 line 1569.
type PoolDescriptor struct {
	// PoolID is the credential pool name.
	PoolID string
	// Healthy reports whether the pool has at least one healthy,
	// non-revoked credential.
	Healthy bool
	// HasCapacity reports whether at least one credential in the pool
	// has active leases below maxConcurrentSessions. spec: §4.9 line
	// 1218 ("active leases < maxConcurrentSessions for at least one
	// credential").
	HasCapacity bool
	// CoolingDown reports whether the pool is on §4.9 cooldown after a
	// fault. At session creation no pool is cooling; cooldown is
	// rotation-time state. spec: §4.9 line 1218 ("cooldown status").
	CoolingDown bool
}

// Assignable reports whether the pool can serve a credential now: it is
// healthy, has capacity, and is not cooling down. spec: §4.9 line 1218.
func (d PoolDescriptor) Assignable() bool {
	return d.Healthy && d.HasCapacity && !d.CoolingDown
}

// RotationContext is the §4.9 CredentialRouter rotation-time input,
// present only when the router selects a replacement for a faulted
// provider (Fallback Flow step 4). spec: §4.9 line 1572.
type RotationContext struct {
	PreviousPoolID       string
	PreviousCredentialID string
	FailureReason        string
	RotationCount        int
}

// Input is the §4.9 CredentialRouter input for one provider. spec: §4.9
// lines 1562-1573.
type Input struct {
	// TenantID is the tenant making the request.
	TenantID string
	// UserID is the authenticated user.
	UserID string
	// Provider is the provider being assigned (e.g., anthropic_direct).
	Provider string
	// AllowedPools are the pools eligible for this provider, in §4.9
	// fallback priority order (primary first), each carrying current
	// utilization stats.
	AllowedPools []PoolDescriptor
	// UserCredentialAvailable reports whether a usable user-scoped
	// credential exists for this provider (registered, not revoked, and
	// userCredentialsEnabled on the tenant policy).
	UserCredentialAvailable bool
	// PreferredSource is the tenant credentialPolicy.preferredSource.
	PreferredSource credential.PreferredSource
	// RotationContext is present only during rotation.
	RotationContext *RotationContext
	// Hints is the deployer-extensible key-value map (model, cost_tier,
	// region, ...). spec: §4.9 line 1573.
	Hints map[string]string
}

// Output is the §4.9 CredentialRouter output. spec: §4.9 lines
// 1577-1581.
type Output struct {
	// Source is the resolved credential source (pool or user).
	Source credential.LeaseSource
	// PoolID is the selected pool, required when Source is pool.
	PoolID string
	// StrategyOverride, when set, overrides the pool's assignmentStrategy
	// for this assignment. The default router never sets it.
	StrategyOverride credential.AssignmentStrategy
}

// Router is the §4.9 pluggable CredentialRouter interface. Deployers
// who need cost-aware, latency-based, or intent-based routing implement
// it; the Hints map is the primary extension point. spec: §4.9 lines
// 1558-1591.
type Router interface {
	// Resolve selects the credential source and pool for the input's
	// provider. It returns ErrUserCredentialNotFound or
	// ErrNoCredentialAvailable when no source can be resolved.
	Resolve(ctx context.Context, in Input) (Output, error)
}

// Sentinel resolution errors.
var (
	// ErrUserCredentialNotFound — a user-only policy
	// (preferredSource: user) had no usable user credential for the
	// provider. The gateway maps it to USER_CREDENTIAL_NOT_FOUND.
	// spec: §4.9 lines 1364, 1370.
	ErrUserCredentialNotFound = errors.New("credrouter: no user-scoped credential for provider")
	// ErrNoCredentialAvailable — no source resolved an assignable
	// credential for the provider. The gateway maps it to
	// CREDENTIAL_POOL_EXHAUSTED. spec: §4.9 line 1218.
	ErrNoCredentialAvailable = errors.New("credrouter: no assignable credential for provider")
)

// Default is the built-in §4.9 CredentialRouter. It walks the
// preferredSource source order and, for the pool source, selects the
// first assignable pool from AllowedPools (which the caller orders by
// the provider's fallback chain). spec: §4.9 lines 1336, 1583-1589.
type Default struct{}

// NewDefault returns the built-in CredentialRouter.
func NewDefault() Default { return Default{} }

// Resolve implements Router. It tries each source in the
// preferredSource order: a user source resolves when a user credential
// is available; a pool source resolves to the first assignable pool in
// AllowedPools. When nothing resolves, a user-only policy with no user
// credential returns ErrUserCredentialNotFound and every other case
// returns ErrNoCredentialAvailable. spec: §4.9 lines 1328-1336, 1362-1366.
func (Default) Resolve(_ context.Context, in Input) (Output, error) {
	for _, src := range in.PreferredSource.SourceOrder() {
		switch src {
		case credential.SourceUser:
			if in.UserCredentialAvailable {
				return Output{Source: credential.SourceUser}, nil
			}
		case credential.SourcePool:
			for _, pd := range in.AllowedPools {
				if pd.Assignable() {
					return Output{Source: credential.SourcePool, PoolID: pd.PoolID}, nil
				}
			}
		}
	}
	// Nothing resolved. A user-only policy with no user credential is a
	// terminal USER_CREDENTIAL_NOT_FOUND; every other miss (including a
	// prefer-* fallthrough that found no pool) is pool exhaustion.
	if in.PreferredSource.UserMissIsTerminal() && !in.UserCredentialAvailable {
		return Output{}, ErrUserCredentialNotFound
	}
	return Output{}, ErrNoCredentialAvailable
}

var _ Router = Default{}

// Intersection returns the providers present in both a Runtime's
// supportedProviders and a tenant policy's providerPools keys, in the
// policy's sorted provider order. Only providers in both sets are
// eligible for credential assignment. spec: §4.9 line 1326.
func Intersection(supportedProviders []string, policy credential.CredentialPolicy) []string {
	supported := make(map[string]bool, len(supportedProviders))
	for _, p := range supportedProviders {
		supported[p] = true
	}
	var out []string
	for _, p := range policy.Providers() {
		if supported[p] {
			out = append(out, p)
		}
	}
	return out
}
