// SPDX-License-Identifier: MIT

package adminops

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// RevocationCache revokes a token identifier in every replica's revocation
// cache. The token revocation propagator satisfies it.
type RevocationCache interface {
	Revoke(jti string)
}

// TokenRevocations serves GWSEAM-PropagateTokenRevocation over the token
// revocation propagator.
type TokenRevocations struct {
	Cache RevocationCache
}

var _ seam.TokenRevocations = TokenRevocations{}

// PropagateTokenRevocation revokes jti in the revocation cache of every
// replica.
//
// spec: GWSEAM-PropagateTokenRevocation
func (t TokenRevocations) PropagateTokenRevocation(jti string) {
	t.Cache.Revoke(jti)
}

// PoolCredentialRevoker revokes the leases of a pool's named credentials
// and denies the credentials on every replica.
type PoolCredentialRevoker interface {
	RevokePoolCredentials(ctx context.Context, poolID string, credentialIDs []string) int
}

// PoolCredentials serves GWSEAM-RevokePoolCredentials over the pool
// credential revoker.
type PoolCredentials struct {
	Revoker PoolCredentialRevoker
}

var _ seam.PoolCredentialRevocations = PoolCredentials{}

// RevokePoolCredentials revokes the leases of the named credentials and
// returns the count revoked.
//
// spec: GWSEAM-RevokePoolCredentials
func (p PoolCredentials) RevokePoolCredentials(ctx context.Context, poolID string, credentialIDs []string) int {
	return p.Revoker.RevokePoolCredentials(ctx, poolID, credentialIDs)
}

// UserLeaseRevoker returns to their pools the credential leases a user's
// sessions hold. The lease revoker satisfies it.
type UserLeaseRevoker interface {
	RevokeUserLeases(tenantID, userID string, sessionIDs []string) int
}

// UserLeases serves GWSEAM-RevokeUserLeases over the user lease revoker.
type UserLeases struct {
	Revoker UserLeaseRevoker
}

var _ seam.UserLeaseRevocations = UserLeases{}

// RevokeUserLeases returns the leases the named sessions hold and returns
// the count returned.
//
// spec: GWSEAM-RevokeUserLeases
func (u UserLeases) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	return u.Revoker.RevokeUserLeases(tenantID, userID, sessionIDs)
}

// KMSProbe probes a tenant's key for a workspace tier. The tenant key
// lifecycle satisfies it.
type KMSProbe interface {
	ProbeAvailability(ctx context.Context, tenantID, workspaceTier string) error
	LastProbeSuccess(tenantID string) (time.Time, bool)
}

// TenantKeys serves GWSEAM-ProbeTenantKMS over the tenant key lifecycle.
type TenantKeys struct {
	Probe KMSProbe
}

var _ seam.TenantKeys = TenantKeys{}

// ProbeTenantKMS returns nil when the tenant's key for workspaceTier is
// available.
//
// spec: GWSEAM-ProbeTenantKMS
func (t TenantKeys) ProbeTenantKMS(ctx context.Context, tenantID, workspaceTier string) error {
	return t.Probe.ProbeAvailability(ctx, tenantID, workspaceTier)
}

// LastTenantKMSProbe returns the time of the most recent successful probe
// of the tenant's key.
//
// spec: GWSEAM-LastTenantKMSProbe
func (t TenantKeys) LastTenantKMSProbe(tenantID string) (time.Time, bool) {
	return t.Probe.LastProbeSuccess(tenantID)
}

// SecretResolver resolves a connector's client secret reference. The
// connector secret resolver satisfies it.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// ConnectorSecrets serves GWSEAM-ResolveConnectorSecret over the connector
// secret resolver.
type ConnectorSecrets struct {
	Resolver SecretResolver
}

var _ seam.ConnectorSecrets = ConnectorSecrets{}

// ResolveConnectorSecret resolves ref to the secret's value.
//
// spec: GWSEAM-ResolveConnectorSecret
func (c ConnectorSecrets) ResolveConnectorSecret(ctx context.Context, ref string) (string, error) {
	return c.Resolver.Resolve(ctx, ref)
}
