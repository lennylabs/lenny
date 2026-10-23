// SPDX-License-Identifier: MIT

// Package leaserevoke is the §11.4 full_revoke credential lease step: it
// returns every §4.9 credential lease a revoked user's sessions hold to its
// pool. Returning a lease revokes it: Release removes the lease record from
// the shared lease store, so every replica rejects the lease's token. The
// step writes no credential deny-list entry, because a pool credential is
// shared with other users and a deny-list entry for it would lock them out.
// The admin router drives it through admin.UserLeaseRevoker; the gateway
// binary constructs it over its lease store and its credential assigner.
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

// Revoker returns the credential leases held by a revoked user's sessions.
// It satisfies admin.UserLeaseRevoker.
type Revoker struct {
	leases   LeaseLister
	assigner Releaser
	logger   *slog.Logger
}

// New returns a Revoker over the lease store and the credential assigner.
// A nil logger logs through slog.Default.
func New(leases LeaseLister, assigner Releaser, logger *slog.Logger) *Revoker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Revoker{leases: leases, assigner: assigner, logger: logger}
}

// RevokeUserLeases runs the full_revoke lease step for the named sessions.
// It enumerates their leases and returns every one to its pool through the
// assigner's Release, which revokes the lease and removes its record, so
// the returned lease's token is rejected on every replica. The step
// deny-lists no credential. It returns the number of leases enumerated,
// which is the response's leasesRevoked count.
//
// A Release error is logged with the lease and session identifiers, and
// the lease is still counted. Each assigner removes the record whether or
// not the rest of the release succeeded, and a repeated call returns a
// lease that survived a failed step. A lease-store listing error is not
// visible here, because LeasesBySession returns no error; such a lease is
// bounded by its expiry, the stale-slot reaper, or a repeated call.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §4.9 (Credential Leasing Service)
func (r *Revoker) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	leases := r.leases.LeasesBySession(sessionIDs)
	for _, lease := range leases {
		if err := r.assigner.Release(lease.LeaseID); err != nil {
			r.logger.Warn("full_revoke: return a revoked session's credential lease",
				"lease_id", lease.LeaseID, "session_id", lease.SessionID,
				"tenant_id", tenantID, "user_id", userID, "error", err)
		}
	}
	return len(leases)
}
