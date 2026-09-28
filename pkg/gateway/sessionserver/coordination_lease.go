// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// acquireCoordinationLease claims the §10.1 per-session coordination
// lease for this replica at bind, so the replica that holds the pod
// binding holds the lease from bind time (§4.6.1 co-location). It is the
// canonical at-bind acquire every bind-and-publish site routes through:
// registerBinding calls it before publishing, and the early-commit paths
// (single-call /start, delegated-child materialize, checkpoint-restore
// resume) call it directly before the running-commit or the direct
// podRegistry.Put, so a peer sweep never reads a committed running-pod row
// with an unheld lease as adoptable. leasestore.Acquire is idempotent for
// the same holder, so a later registerBinding on a hoisted path self-renews.
//
// A nil leaseStore or an empty replicaID (the in-memory / dev posture with
// no Redis leasestore) makes it a no-op, leaving the bind to publish as
// before. On a live foreign holder it returns leasestore.ErrHeld unwrapped
// so a caller can branch on errors.Is; any other Redis error is wrapped
// and, per the fail-closed rule on this coordination-ownership path,
// aborts the bind.
//
// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease; fail-closed on a live foreign holder).
func (s *Server) acquireCoordinationLease(ctx context.Context, tenantID, sessionID string) error {
	if s.leaseStore == nil || s.replicaID == "" {
		return nil
	}
	if _, err := s.leaseStore.Acquire(ctx, tenantID, sessionID, s.replicaID, s.coordLeaseTTL); err != nil {
		if errors.Is(err, leasestore.ErrHeld) {
			return err
		}
		return fmt.Errorf("acquire coordination lease for session %s: %w", sessionID, err)
	}
	return nil
}

// releaseCoordinationLease releases the §10.1 per-session coordination
// lease this replica acquired at bind, so a running-commit that fails
// after a successful at-bind acquire leaves neither a published binding
// nor a held lease. It is the rollback counterpart to
// acquireCoordinationLease and runs alongside rollbackBinding on the
// early-commit paths (single-call /start store.Create, delegated-child
// materialize store.Update): those paths acquire the lease ahead of the
// running-commit, so a commit failure would otherwise strand a held lease
// with no binding, decoupling the lease holder from the binding holder
// that §4.6.1 co-location keeps unified. On the durable delegated-child
// StateCreated row the strand is not self-healing (a peer Sweeper's
// priorHolder-renew branch keeps renewing the orphaned lease), so the
// explicit release is required rather than left to the 60s TTL.
//
// Release is holder-checked and Lua-atomic (leasestore.go), so it is a
// no-op when this replica does not hold the lease. A nil leaseStore or an
// empty replicaID (the in-memory / dev posture) makes it a no-op. The
// release is best-effort: a Redis error at rollback is logged and
// swallowed, because the 60s lease TTL surfaces the lease for re-adoption
// even if the release write is lost.
//
// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease).
func (s *Server) releaseCoordinationLease(ctx context.Context, tenantID, sessionID string) {
	if s.leaseStore == nil || s.replicaID == "" {
		return
	}
	if err := s.leaseStore.Release(ctx, tenantID, sessionID, s.replicaID); err != nil {
		log.Printf("sessionserver: release coordination lease for session %s: %v", sessionID, err)
	}
}
