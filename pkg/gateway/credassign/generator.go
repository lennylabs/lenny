// SPDX-License-Identifier: MIT

package credassign

import (
	"context"

	"github.com/lennylabs/lenny/pkg/credential"
)

// CredentialLease is the §4.9 credential lease a CredentialGenerator
// returns. Per the §12.6 line 432 cross-reference it is the existing
// credential.Lease; no §12.6-specific envelope is introduced.
type CredentialLease = credential.Lease

// CredentialRequest is the input to GenerateCredential. The §12.6 line 431
// key fields are TenantID, Provider, PoolID, and the required scopes.
// SessionID and SpiffeURI are the §4.9 lease-mint inputs the v1
// StaticPoolGenerator threads to the underlying Assigner; a Tier-4
// dynamic generator may ignore them.
type CredentialRequest struct {
	// TenantID is the lease's owning tenant (§4.9 line 1468 attribution).
	TenantID string
	// Provider is the requested upstream credential provider. v1 pools
	// pin one provider, so the field is advisory; a future selector that
	// fans out across providers reads it.
	Provider credential.Provider
	// PoolID names the §4.9 credential pool to lease from.
	PoolID string
	// SessionID is the session the lease binds to.
	SessionID string
	// SpiffeURI is the issuing pod's SPIFFE identity for proxy-mode
	// SPIFFE-binding; empty disables binding.
	SpiffeURI string
	// Scopes are the required credential scopes. v1 pools are
	// scope-agnostic, so StaticPoolGenerator ignores the field; it is
	// carried for the Tier-4 dynamic generators that scope a minted
	// credential.
	Scopes []string
}

// CredentialGenerator is the §12.6 lines 631-633 scaling extension
// interface that abstracts credential acquisition from a pool. The v1
// implementation (StaticPoolGenerator) wraps the existing CredentialPool
// lease-allocation path; a Tier-4 implementation (Vault / STS dynamic
// generation) satisfies the same interface so no caller changes when the
// backing source is swapped.
type CredentialGenerator interface {
	GenerateCredential(ctx context.Context, req CredentialRequest) (*CredentialLease, error)
}

// StaticPoolGenerator is the §12.6 line 634 v1 CredentialGenerator: a thin
// adapter over the existing §4.9 Assigner. It selects an available
// credential from the named pool using the existing lease-based allocation
// path. Construct it over the in-process Service or the gateway-side
// Client, both of which satisfy Assigner.
type StaticPoolGenerator struct {
	assigner Assigner
}

// NewStaticPoolGenerator returns a StaticPoolGenerator backed by assigner.
func NewStaticPoolGenerator(assigner Assigner) *StaticPoolGenerator {
	return &StaticPoolGenerator{assigner: assigner}
}

var _ CredentialGenerator = (*StaticPoolGenerator)(nil)

// GenerateCredential mints a §4.9 credential lease from req.PoolID through
// the underlying Assigner and returns it. It honors a cancelled context
// before touching the pool. spec: §12.6 lines 631-634.
func (g *StaticPoolGenerator) GenerateCredential(ctx context.Context, req CredentialRequest) (*CredentialLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := g.assigner.Assign(req.PoolID, req.SessionID, req.SpiffeURI, req.TenantID)
	if err != nil {
		return nil, err
	}
	return &lease, nil
}
