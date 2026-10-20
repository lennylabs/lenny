// SPDX-License-Identifier: MIT

// Package leaserevoke is the §11.4 full_revoke credential lease step: it
// returns every §4.9 credential lease a revoked user's sessions hold to its
// pool and then adds each lease's credential to the cross-replica deny
// list. The admin router drives it through admin.UserLeaseRevoker; the
// gateway binary constructs it over its lease store, its credential
// assigner, and its credential-lease revocation propagator.
package leaserevoke

import (
	"log/slog"

	"github.com/lennylabs/lenny/pkg/credential"
)

// LeaseLister enumerates the leases held by a set of sessions. The
// in-memory credleasestore.Store and the Postgres-backed
// credleasestore/pgstore.Store both satisfy it.
type LeaseLister interface {
	LeasesBySession(sessionIDs []string) []credential.Lease
}

// Releaser returns a lease to its pool and removes its record. The
// credential assigner satisfies it, in-process or through the Token
// Service client.
type Releaser interface {
	Release(leaseID string) error
}

// DenyList is the §4.9 deny list a revoked credential is added to. The
// marker method IsCrossReplicaPropagator is intentional: §11.4 step 1 must
// reach every replica's deny list, and a bare *denylist.DenyList carries a
// revocation only on the calling replica. Requiring the marker bars an
// accidental wiring downgrade, because only the credential-lease
// revocation propagator (which applies locally and publishes on Redis
// pub/sub) satisfies it. A propagator built with a nil bus still satisfies
// the marker; that is the explicit local-only single-replica posture.
//
// spec: §11.4 step 1 (User Invalidation)
type DenyList interface {
	Revoke(key credential.CredentialKey)
	IsCrossReplicaPropagator()
}

// Revoker returns and deny-lists the credential leases held by a revoked
// user's sessions. It satisfies admin.UserLeaseRevoker.
type Revoker struct {
	leases   LeaseLister
	assigner Releaser
	denyList DenyList
	logger   *slog.Logger
}

// New returns a Revoker over the lease store, the credential assigner, and
// the cross-replica deny list. A nil logger logs through slog.Default.
func New(leases LeaseLister, assigner Releaser, denyList DenyList, logger *slog.Logger) *Revoker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Revoker{leases: leases, assigner: assigner, denyList: denyList, logger: logger}
}

// RevokeUserLeases runs the full_revoke lease step for the named sessions.
// It enumerates their leases, returns every one to its pool through the
// assigner's Release, which removes the lease record and returns the pool
// slot, and only then adds each lease's source-aware credential identity to
// the deny list. It returns the number of leases enumerated, which is the
// number deny-listed and the response's leasesRevoked count.
//
// Every Release precedes the first deny-list write. The deny list's
// in-process revoke hook rotates every direct-delivery lease it still finds
// on a revoked credential, so a deny-list write while a later lease of a
// revoked session is still recorded on the same pool credential would push
// that revoked session a fresh key. Removing the record also rejects the
// lease token on every replica through the shared lease store, whatever
// happens to the deny-list publish. A Release error is logged with the
// lease and session identifiers, and the lease is still deny-listed and
// counted: the assigner removes the record either way, and the deny list
// is what stops the token.
//
// spec: §11.4 step 1 (User Invalidation), §4.9 (Credential Leasing Service)
func (r *Revoker) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	leases := r.leases.LeasesBySession(sessionIDs)
	for _, lease := range leases {
		if err := r.assigner.Release(lease.LeaseID); err != nil {
			r.logger.Warn("full_revoke: return a revoked session's credential lease",
				"lease_id", lease.LeaseID, "session_id", lease.SessionID,
				"tenant_id", tenantID, "user_id", userID, "error", err)
		}
	}
	for _, lease := range leases {
		r.denyList.Revoke(lease.CredentialKey())
	}
	return len(leases)
}
