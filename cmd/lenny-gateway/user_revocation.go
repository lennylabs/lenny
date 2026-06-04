// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/admintoken"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podterminate/propagator"
)

// Compile-time assertion that the §4.9 credential-lease revocation
// propagator satisfies credentialDenyList. A refactor that drops the
// `IsCrossReplicaPropagator()` marker on *credrenewalprop.Propagator
// fails to build, so the §11.4 step-6 wiring contract is enforced at
// compile time rather than runtime. spec: §11.4 step 6.
var _ credentialDenyList = (*credrenewalprop.Propagator)(nil)

// Compile-time assertion that podTerminateFanOut is the cross-replica
// §11.4 step-2 local terminator the pod-termination propagator drives.
var _ podterminateprop.LocalTerminator = (*podTerminateFanOut)(nil)

// This file wires the §11.4 full_revoke fan-out dependencies the admin
// router consumes. handleInvalidateUser raises the user's tombstone and
// cancels their sessions in the SessionStore on its own; the adapters
// here carry the remaining §11.4 propagation onto infrastructure the
// admin layer does not own — the pods, the §4.9 credential leases, and
// the §13.3 issued tokens. Each is independently optional, so a minimal
// gateway that wires none of them still soft/hard disables a user.

// userRevokeReason is the §11.4 reason carried on the §4.7 Terminate
// RPC the full_revoke fan-out sends to a revoked user's pods.
const userRevokeReason = "USER_REVOKED"

// userTerminateDeadline bounds the graceful phase of the §4.7 Terminate
// RPC the full_revoke fan-out sends. Per §11.4 the pod's adapter sends
// SIGTERM, waits this long, then sends SIGKILL.
const userTerminateDeadline = 10 * time.Second

// userTerminateRPCTimeout bounds each per-pod Terminate RPC. It exceeds
// userTerminateDeadline so the gateway observes the pod's graceful exit
// before giving up on the call.
const userTerminateRPCTimeout = 20 * time.Second

// podTerminateFanOut terminates the pods hosting a revoked user's
// sessions. It reads the per-replica pod-session registry and sends the
// §4.7 Terminate RPC to every pod this replica holds a binding for
// among the user's sessions. It satisfies admin.UserPodTerminator and,
// for the cross-replica path, podterminateprop.LocalTerminator.
//
// A pod binding lives in exactly one replica's registry, so the handling
// replica reaches only the pods it itself coordinates. prop fans the
// §11.4 step-2 Terminate request out to peer replicas over Redis pub/sub
// so the pods they coordinate are terminated too; without it the
// fan-out is replica-local and a revoked user's peer-replica pods run
// until the §8.10 orphan sweep reaps them. prop is nil in a deployment
// with no Redis bus, which is the single-replica posture. spec: §11.4
// step 2.
type podTerminateFanOut struct {
	registry *podsession.Registry
	prop     *podterminateprop.Propagator
}

// TerminateUserSessions is the admin.UserPodTerminator entry the
// full_revoke handler drives. It terminates the pods this replica holds
// for the user's sessions and fans the §11.4 step-2 Terminate request
// out to peer replicas so the pods they coordinate are terminated too.
// The returned result reports only this replica's local terminations —
// the peer terminations happen asynchronously on each peer's subscriber,
// matching the §11.4 note that propagation completes within seconds.
func (p *podTerminateFanOut) TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) admin.UserTerminationResult {
	req := podterminateprop.Request{
		TenantID:   tenantID,
		UserID:     userID,
		Reason:     userRevokeReason,
		SessionIDs: sessionIDs,
	}
	res := p.terminateLocal(ctx, req)
	if p.prop != nil {
		p.prop.Publish(ctx, req)
	}
	return admin.UserTerminationResult{PodsTerminated: res.PodsTerminated, FailedSessions: res.FailedSessions}
}

// TerminateLocal is the podterminateprop.LocalTerminator entry the
// cross-replica subscriber drives for a peer replica's request. It
// terminates only the pods this replica holds and does not re-publish.
func (p *podTerminateFanOut) TerminateLocal(ctx context.Context, req podterminateprop.Request) podterminateprop.Result {
	return p.terminateLocal(ctx, req)
}

// terminateLocal sends the §4.7 Terminate RPC to the pod hosting each of
// req.SessionIDs that this replica holds a binding for. It is
// best-effort per pod: a pod that fails to terminate is recorded in the
// result and the loop continues with the rest. A session with no binding
// on this replica is skipped without being counted a failure — its pod
// is bound on another replica (which terminates it on its own subscriber)
// or already released.
func (p *podTerminateFanOut) terminateLocal(ctx context.Context, req podterminateprop.Request) podterminateprop.Result {
	reason := req.Reason
	if reason == "" {
		reason = userRevokeReason
	}
	want := make(map[string]struct{}, len(req.SessionIDs))
	for _, id := range req.SessionIDs {
		want[id] = struct{}{}
	}
	var res podterminateprop.Result
	for _, bind := range p.registry.Snapshot() {
		if _, ok := want[bind.SessionID]; !ok {
			continue
		}
		if bind.Adapter == nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, userTerminateRPCTimeout)
		_, err := bind.Adapter.Terminate(callCtx, bind.SessionID, reason, userTerminateDeadline)
		cancel()
		if err != nil {
			log.Printf("lenny-gateway: §11.4 full_revoke: terminate pod for session %s (user %s/%s) failed: %v",
				bind.SessionID, req.TenantID, req.UserID, err)
			res.FailedSessions = append(res.FailedSessions, bind.SessionID)
			continue
		}
		res.PodsTerminated++
	}
	return res
}

// userLeaseStore is the slice of the §4.9 credential-lease store the
// full_revoke fan-out needs: enumerate the leases held by a set of
// sessions and drop a lease by id. The in-memory credleasestore.Store
// and the Postgres-backed credleasestore/pgstore.Store both satisfy it.
type userLeaseStore interface {
	LeasesBySession(sessionIDs []string) []credential.Lease
	Remove(leaseID string)
}

// credentialDenyList is the §4.9 deny list the full_revoke fan-out adds
// a revoked user's credentials to. The marker method
// `IsCrossReplicaPropagator()` is intentional: §11.4 step 6 must reach
// every replica's deny list, and a bare *denylist.DenyList carries the
// revocation only on the calling replica. Requiring the marker bars an
// accidental wiring downgrade — only *credrenewalprop.Propagator (which
// applies locally and publishes on Redis pub/sub) satisfies it. A
// Propagator built with a nil bus still satisfies the marker; that is
// the explicit local-only single-replica posture, not the silent
// downgrade the marker rejects. spec: §11.4 step 6.
type credentialDenyList interface {
	Revoke(key credential.CredentialKey)
	IsCrossReplicaPropagator()
}

// userLeaseRevoker revokes the §4.9 credential leases held by a revoked
// user's sessions. It drops each lease from the lease store and adds
// the lease's credential to the §4.9 deny list, so the LLM proxy
// rejects an in-flight request still carrying the lease token. It
// satisfies admin.UserLeaseRevoker.
type userLeaseRevoker struct {
	leases   userLeaseStore
	denyList credentialDenyList
}

// RevokeUserLeases revokes every §4.9 credential lease held by the
// named sessions: it removes the lease from the lease store and adds
// the lease's source-aware credential identity to the §4.9 deny list.
// It returns the count of leases revoked. The deny-list write is what
// stops an already-materialized proxy-mode lease token from reaching
// the provider — §11.4 step 6 — and, when the deny list is wrapped by a
// propagator, the revocation fans out to every replica.
func (u *userLeaseRevoker) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	leases := u.leases.LeasesBySession(sessionIDs)
	for _, lease := range leases {
		u.denyList.Revoke(lease.CredentialKey())
		u.leases.Remove(lease.LeaseID)
	}
	return len(leases)
}

// userTokenRevoker revokes a user's §13.3 issued tokens in the durable
// issued-token index. It satisfies admin.UserTokenRevoker; the admin
// router pushes the returned JTIs into the revocation cache so the
// user's cached auth is invalidated and, through the cache's
// propagator, the revocation reaches peer replicas.
type userTokenRevoker struct {
	store *issuedtokenstore.Store
}

// RevokeUserTokens marks every not-yet-revoked token issued for the
// user as revoked and returns the JTIs it revoked.
func (u *userTokenRevoker) RevokeUserTokens(ctx context.Context, tenantID, subject, reason string, at time.Time) ([]string, error) {
	return u.store.RevokeBySubject(ctx, tenantID, subject, reason, at)
}

// adminIssuedTokens adapts the §13.3 issued-token store plus the
// revocation cache to admintoken.IssuedTokens. It records each minted
// §17.6 admin token so the token is revocable, and on rotation marks the
// prior token revoked in Postgres and pushes the revocation onto the
// cross-replica cache so the old token stops validating immediately (the
// §17.6 line 472 no-grace-period guarantee). spec: §17.6 line 472 —
// F-17.6.3.
type adminIssuedTokens struct {
	store *issuedtokenstore.Store
	cache admin.RevocationCache
}

func (a adminIssuedTokens) Record(ctx context.Context, rec admintoken.MintedToken) error {
	return a.store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI:       rec.JTI,
		TenantID:  rec.TenantID,
		Subject:   rec.Subject,
		TokenHash: rec.TokenHash,
		IssuedAt:  rec.IssuedAt,
		ExpiresAt: rec.ExpiresAt,
	})
}

func (a adminIssuedTokens) Revoke(ctx context.Context, tenantID, jti, reason string, at time.Time) error {
	err := a.store.Revoke(ctx, tenantID, jti, reason, at)
	if a.cache != nil {
		a.cache.Revoke(jti)
	}
	return err
}
