// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// The adapters in this file turn the operations of pkg/gateway/seam that
// the session core and the platform tools seam serve to the API front into
// the interfaces the admin router reads, so the composition root hands the
// router no value of another part.
//
// spec: GW-R1

// SeamRevokeHook is the full_revoke per-session hook over the session
// core's terminal claim (GWSEAM-BeginTerminal) and the session side of a
// revocation (GWSEAM-AfterRevokeWrite and GWSEAM-ReclaimRevokedPreRunning).
type SeamRevokeHook struct {
	Terminals   seam.Terminals
	Revocations seam.Revocations
}

var _ RevokeHook = SeamRevokeHook{}

// BeginTerminal opens the replica's terminal claim for a session.
func (h SeamRevokeHook) BeginTerminal(sessionID string) (end func()) {
	return h.Terminals.BeginTerminal(sessionID)
}

// AfterRevokeWrite runs the session core's full_revoke hook on the row the
// revocation write produced.
func (h SeamRevokeHook) AfterRevokeWrite(ctx context.Context, row sessionstore.Session) {
	h.Revocations.AfterRevokeWrite(ctx, seamRecord(row))
}

// ReclaimRevokedPreRunning reclaims the pod or slot of a session the
// revocation cancelled before it ran.
func (h SeamRevokeHook) ReclaimRevokedPreRunning(ctx context.Context, from session.State, row sessionstore.Session) {
	h.Revocations.ReclaimRevokedPreRunning(ctx, state.State(from), seamRecord(row))
}

// SeamUserPods releases a revoked user's pod bindings through
// GWSEAM-TerminateUserSessions.
type SeamUserPods struct {
	Revocations seam.Revocations
}

var _ UserPodTerminator = SeamUserPods{}

// TerminateUserSessions releases the bindings of the named sessions.
func (p SeamUserPods) TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) UserTerminationResult {
	res := p.Revocations.TerminateUserSessions(ctx, tenantID, userID, sessionIDs)
	return UserTerminationResult{PodsTerminated: res.PodsTerminated, FailedSessions: res.FailedSessions}
}

// SeamRevocationCache revokes a token identifier on every replica through
// GWSEAM-PropagateTokenRevocation.
type SeamRevocationCache struct {
	Tokens seam.TokenRevocations
}

var _ RevocationCache = SeamRevocationCache{}

// Revoke revokes jti in the revocation cache of every replica.
func (c SeamRevocationCache) Revoke(jti string) { c.Tokens.PropagateTokenRevocation(jti) }

// SeamPoolCredentialRevoker revokes a pool credential's leases through
// GWSEAM-RevokePoolCredentials.
type SeamPoolCredentialRevoker struct {
	Revocations seam.PoolCredentialRevocations
}

var _ PoolCredentialRevoker = SeamPoolCredentialRevoker{}

// RevokePoolCredentials revokes the leases of the named credentials.
func (r SeamPoolCredentialRevoker) RevokePoolCredentials(ctx context.Context, poolID string, credentialIDs []string) int {
	return r.Revocations.RevokePoolCredentials(ctx, poolID, credentialIDs)
}

// SeamUserLeaseRevoker returns a revoked user's credential leases through
// GWSEAM-RevokeUserLeases.
type SeamUserLeaseRevoker struct {
	Revocations seam.UserLeaseRevocations
}

var _ UserLeaseRevoker = SeamUserLeaseRevoker{}

// RevokeUserLeases returns the leases the named sessions hold.
func (r SeamUserLeaseRevoker) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	return r.Revocations.RevokeUserLeases(tenantID, userID, sessionIDs)
}

// SeamKMSProbe probes a tenant's key through GWSEAM-ProbeTenantKMS.
type SeamKMSProbe struct {
	Keys seam.TenantKeys
}

var _ KMSProbe = SeamKMSProbe{}

// ProbeAvailability returns nil when the tenant's key for workspaceTier
// is available.
func (p SeamKMSProbe) ProbeAvailability(ctx context.Context, tenantID, workspaceTier string) error {
	return p.Keys.ProbeTenantKMS(ctx, tenantID, workspaceTier)
}

// LastProbeSuccess returns the time of the most recent successful probe
// for tenantID.
func (p SeamKMSProbe) LastProbeSuccess(tenantID string) (time.Time, bool) {
	return p.Keys.LastTenantKMSProbe(tenantID)
}

// SeamClientSecrets resolves a connector's client secret through
// GWSEAM-ResolveConnectorSecret.
type SeamClientSecrets struct {
	Secrets seam.ConnectorSecrets
}

var _ ClientSecretResolver = SeamClientSecrets{}

// Resolve resolves ref to the secret's value.
func (c SeamClientSecrets) Resolve(ctx context.Context, ref string) (string, error) {
	return c.Secrets.ResolveConnectorSecret(ctx, ref)
}

// SeamLeaseDenials clears a delegation subtree's extension denial through
// GWSEAM-ClearSubtreeDenial, which the platform tools seam serves.
type SeamLeaseDenials struct {
	Delegation seam.DelegationAdmin
}

var _ LeaseDenialClearer = SeamLeaseDenials{}

// ClearSubtreeDenial clears the recorded denial for the subtree.
func (d SeamLeaseDenials) ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, sessionID string) error {
	return d.Delegation.ClearSubtreeDenial(ctx, tenantID, rootSessionID, sessionID)
}

// SeamElicitationFloor returns the deployment's elicitation floor reader
// over GWSEAM-ElicitationFloor, which the platform tools seam serves, in
// the form WithElicitationFloorProvider takes.
func SeamElicitationFloor(d seam.DelegationAdmin) func() string {
	return d.ElicitationFloor
}
