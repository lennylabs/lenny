// SPDX-License-Identifier: MIT

package credrouter

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/credential"
)

// ProviderInput is one provider's pre-claim input: the provider and the
// pools and user-credential availability the router resolves against.
type ProviderInput struct {
	// Provider is a provider in the §4.9 intersection.
	Provider string
	// AllowedPools are the provider's pools in fallback priority order.
	AllowedPools []PoolDescriptor
	// UserCredentialAvailable reports whether a usable user credential
	// exists for the provider.
	UserCredentialAvailable bool
	// Hints carries the deployer-extensible routing hints.
	Hints map[string]string
}

// PreClaimInput is the §4.9 pre-claim availability check input: the
// providers in the intersection together with the tenant policy's
// preferredSource. spec: §4.9 lines 1216-1218.
type PreClaimInput struct {
	TenantID        string
	UserID          string
	PreferredSource credential.PreferredSource
	// Providers is the §4.9 intersection of the Runtime's
	// supportedProviders and the tenant's providerPools, each with its
	// resolved pool descriptors.
	Providers []ProviderInput
}

// ProviderResolution pairs a provider with the router's resolution (or
// the error it produced).
type ProviderResolution struct {
	Provider string
	Output   Output
	Err      error
}

// PreClaimResult is the outcome of a §4.9 pre-claim availability check.
type PreClaimResult struct {
	// Resolutions is the per-provider resolution in intersection order.
	Resolutions []ProviderResolution
	// PoolAssignments maps each provider that resolved to a pool source
	// to its selected pool ID. This is the §4.7 AssignCredentials map
	// the binder mints leases from.
	PoolAssignments map[string]string
	// UserProviders lists providers that resolved to a user source.
	// User-source lease delivery is the §4.9 materializedConfig path
	// (tracked separately); this records the resolution.
	UserProviders []string
}

// Available reports whether at least one provider resolved to an
// assignable credential. The session can start when this is true.
// spec: §4.9 line 1326.
func (r PreClaimResult) Available() bool {
	return len(r.PoolAssignments) > 0 || len(r.UserProviders) > 0
}

// PreClaim runs the §4.9 pre-claim credential availability check across
// the providers in the intersection. For each provider it asks the
// router to resolve a source against the provider's allowed pools and
// user-credential availability. The check passes when at least one
// provider resolves to an assignable credential. On failure it returns
// ErrUserCredentialNotFound when every provider failed and at least one
// failed because a user-only policy had no user credential; otherwise
// it returns ErrNoCredentialAvailable (which the gateway maps to
// CREDENTIAL_POOL_EXHAUSTED). An empty intersection is exhaustion.
// spec: §4.9 lines 1216-1218, 1326.
func PreClaim(ctx context.Context, router Router, in PreClaimInput) (PreClaimResult, error) {
	res := PreClaimResult{PoolAssignments: map[string]string{}}
	if len(in.Providers) == 0 {
		return res, ErrNoCredentialAvailable
	}
	sawUserMiss := false
	for _, pi := range in.Providers {
		out, err := router.Resolve(ctx, Input{
			TenantID:                in.TenantID,
			UserID:                  in.UserID,
			Provider:                pi.Provider,
			AllowedPools:            pi.AllowedPools,
			UserCredentialAvailable: pi.UserCredentialAvailable,
			PreferredSource:         in.PreferredSource,
			Hints:                   pi.Hints,
		})
		res.Resolutions = append(res.Resolutions, ProviderResolution{Provider: pi.Provider, Output: out, Err: err})
		switch {
		case err == nil && out.Source == credential.SourcePool:
			res.PoolAssignments[pi.Provider] = out.PoolID
		case err == nil && out.Source == credential.SourceUser:
			res.UserProviders = append(res.UserProviders, pi.Provider)
		case errors.Is(err, ErrUserCredentialNotFound):
			sawUserMiss = true
		}
	}
	if res.Available() {
		return res, nil
	}
	if sawUserMiss {
		return res, ErrUserCredentialNotFound
	}
	return res, ErrNoCredentialAvailable
}
