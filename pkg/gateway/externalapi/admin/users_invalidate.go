// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// §11.4 full_revoke fan-out. handleInvalidateUser raises the user's
// deleted_at tombstone, returns the credential leases of every session
// the user has, transitions the non-terminal ones to `cancelled` in the
// SessionStore, and dismisses their pending elicitations. The
// remaining §11.4 propagation steps act on infrastructure the admin Router
// does not own directly — the pods hosting the user's sessions, the §4.9
// credential leases those sessions hold, and the §13.3 issued tokens the
// user was granted. The Router reaches them through the optional
// dependencies below, each wired by the gateway and each nil in a minimal
// deployment.
//
// Every fan-out is best-effort: a binding that fails to release, a lease
// that fails to return, or an unreachable token index does not abort the
// rest of full_revoke. The handler records what completed and what failed
// in the audit event and the response body, so an operator sees a partial
// propagation rather than a silent one.

// UserPodTerminator releases the pod bindings of a user's cancelled
// sessions. On §11.4 full_revoke the gateway releases the binding of every
// cancelled session this replica holds through the session release, which
// sends the pod the §4.7 `Shutdown` RPC, and publishes the request so each
// peer replica releases the bindings it holds. The gateway's pod-session
// registry satisfies it; a deployment without warm-pod placement wires nil
// and the SessionStore transition alone ends the user's sessions.
type UserPodTerminator interface {
	// TerminateUserSessions releases the bindings of the named sessions.
	// It is best-effort per binding: one release failing must not stop
	// the others. The result reports the count released on this replica
	// and the identifiers of the sessions whose release failed.
	TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) UserTerminationResult
}

// UserTerminationResult reports the outcome of the §11.4 full_revoke
// binding release on the handling replica.
type UserTerminationResult struct {
	// PodsTerminated counts the bindings this replica released.
	PodsTerminated int
	// FailedSessions lists the sessions whose binding release failed.
	// The failure is recorded and the rest of full_revoke proceeds.
	FailedSessions []string
}

// UserLeaseRevoker returns the §4.9 credential leases held by a user's
// sessions. On §11.4 full_revoke the gateway returns every lease to its
// pool, which revokes the lease and removes its record, so the LLM proxy
// on every replica rejects the returned lease's token. The step writes no
// credential deny-list entry. A deployment without credential leasing
// wires nil.
type UserLeaseRevoker interface {
	// RevokeUserLeases returns every credential lease held by the named
	// sessions and returns the count enumerated. It is best-effort: a
	// lease whose return fails is still counted.
	RevokeUserLeases(tenantID, userID string, sessionIDs []string) int
}

// UserTokenRevoker revokes a user's §13.3 issued tokens in the durable
// issued-token index. On §11.4 full_revoke the gateway revokes every
// not-yet-revoked token issued for the user and feeds the returned JTIs
// into the in-memory revocation cache, so the user's cached auth is
// invalidated. *issuedtokenstore.Store satisfies it through a thin
// adapter. A deployment without an issued-token index wires nil.
type UserTokenRevoker interface {
	// RevokeUserTokens marks every not-yet-revoked token for the user as
	// revoked and returns the JTIs it revoked.
	RevokeUserTokens(ctx context.Context, tenantID, subject, reason string, at time.Time) ([]string, error)
}

// UserPlaygroundRevoker revokes a user's §27 playground sessions on
// §11.4 full_revoke. On user invalidation the gateway drives every
// playground session the user holds through the §27.6 revocation
// primitive (DEL the session record, deny-list each minted bearer, fan
// the change out to peer replicas), so the user's in-flight playground
// bearers are disconnected at the next frame boundary and a subsequent
// POST /v1/playground/token returns 401. The playground Handler
// satisfies it; a deployment with the playground disabled wires nil.
type UserPlaygroundRevoker interface {
	// RevokeSessionsForUser revokes every playground session the user
	// holds under tenant and returns the count revoked. It is
	// best-effort: a per-session failure is returned after the rest are
	// attempted so a partial propagation is recorded rather than aborting
	// full_revoke.
	RevokeSessionsForUser(ctx context.Context, tenantID, userID string) (int, error)
}

// WithUserRevocation wires the §11.4 full_revoke fan-out dependencies
// onto the Router: the binding releaser, the credential-lease revoker,
// and the issued-token revoker. Each argument is independently
// optional — passing nil for one leaves that fan-out step inactive
// while the others still run. soft_disable and hard_disable do not
// touch any of them.
//
// The revocation cache is wired separately through WithIssuedTokens;
// full_revoke reuses the same r.revocationCache so a cross-replica
// propagator wired there also fans the user's token revocations out to
// peer replicas.
func (r *Router) WithUserRevocation(pods UserPodTerminator, leases UserLeaseRevoker, tokens UserTokenRevoker) *Router {
	r.userPods = pods
	r.userLeases = leases
	r.userTokens = tokens
	return r
}

// WithPlaygroundRevocation wires the §27 playground session revoker into
// the §11.4 full_revoke fan-out so an OIDC invalidation drives the
// user's playground sessions through the §27.6 revocation primitive.
// Nil leaves the playground step inactive (the playground is disabled or
// not wired). spec: §27.3.1, §27.6.
func (r *Router) WithPlaygroundRevocation(pg UserPlaygroundRevoker) *Router {
	r.userPlayground = pg
	return r
}

// fullRevokeOutcome accumulates the §11.4 full_revoke fan-out results
// the handler reports in the audit event and the response body.
type fullRevokeOutcome struct {
	podsTerminated     int
	podTerminateFailed []string
	leasesRevoked      int
	tokensRevoked      int
	// tokenRevokeError is the message of a failure to reach the
	// issued-token index, recorded so a partial propagation is visible
	// to the operator. Empty when the token revocation succeeded or no
	// token revoker is wired.
	tokenRevokeError string
	// playgroundSessionsRevoked counts the §27 playground sessions the
	// fan-out revoked. playgroundRevokeError is the message of a failure
	// to reach the playground store, recorded so a partial propagation is
	// visible. Both are zero/empty when no playground revoker is wired.
	playgroundSessionsRevoked int
	playgroundRevokeError     string
}

// runFullRevokeFanOut runs the §11.4 full_revoke propagation that follows
// the cancel writes, for the sessions they cancelled. It first reclaims each
// cancelled session that never started (reclaimRevokedPreRunning), which
// also ends every terminal claim the cancel writes opened, then releases the
// cancelled sessions' bindings through the pod terminator, then invalidates
// the user's cached auth and revokes their playground sessions. Each step is
// independently best-effort and skipped when its dependency is not wired.
// The lease step ran before the cancel writes, in terminateUserSessions.
func (r *Router) runFullRevokeFanOut(ctx context.Context, tenantID, userID, reason string, revoked []RevokedSession) fullRevokeOutcome {
	var out fullRevokeOutcome

	r.reclaimRevokedPreRunning(ctx, revoked)

	// spec: §11.4 step 2 — release the binding of each cancelled session
	// on the replica that holds it. Best-effort per binding.
	if r.userPods != nil && len(revoked) > 0 {
		res := r.userPods.TerminateUserSessions(ctx, tenantID, userID, revokedIDs(revoked))
		out.podsTerminated = res.PodsTerminated
		out.podTerminateFailed = res.FailedSessions
	}

	// §11.4 step 5: invalidate the user's cached auth. The durable
	// issued-token index is revoked first, then every revoked JTI is
	// pushed into the in-memory revocation cache so the next request
	// presenting one of the user's tokens is rejected. When the
	// revocation cache is a cross-replica propagator the push also fans
	// the revocation out to peer replicas. A failure to reach the token
	// index is recorded and does not abort the rest of full_revoke.
	if r.userTokens != nil {
		jtis, err := r.userTokens.RevokeUserTokens(ctx, tenantID, userID, reason, r.clock())
		if err != nil {
			out.tokenRevokeError = err.Error()
		} else {
			out.tokensRevoked = len(jtis)
			if r.revocationCache != nil {
				for _, jti := range jtis {
					r.revocationCache.Revoke(jti)
				}
			}
		}
	}

	// §11.4 / §27.6: drive the user's playground sessions through
	// the §27.6 revocation primitive so the OIDC invalidation disconnects
	// the user's in-flight playground bearers and blocks new mints.
	// Best-effort: a playground-store failure is recorded and does not
	// abort the rest of full_revoke.
	if r.userPlayground != nil {
		n, err := r.userPlayground.RevokeSessionsForUser(ctx, tenantID, userID)
		out.playgroundSessionsRevoked = n
		if err != nil {
			out.playgroundRevokeError = err.Error()
		}
	}

	return out
}

// reclaimRevokedPreRunning reclaims the pod or the slot of each cancelled
// session whose pre-revoke state is created, finalizing, or ready, and then
// ends every terminal claim the cancel writes opened. The claims keep this
// replica's own coordination Sweeper and resume tail from releasing a
// cancelled session's binding before the reclaim runs: the reclaim skips a
// session bound on this replica only while the registry still holds the
// binding, and after a release removed it the reclaim would delete by name a
// claim the release had just patched to recycling. The claims end before the
// binding release, which would otherwise refuse to start.
//
// spec: §11.4 step 4 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func (r *Router) reclaimRevokedPreRunning(ctx context.Context, revoked []RevokedSession) {
	if r.revoked != nil {
		for _, rs := range revoked {
			if revokedPreRunning(rs.From) {
				r.revoked.ReclaimRevokedPreRunning(ctx, rs.From, rs.Row)
			}
		}
	}
	endRevokeClaims(revoked)
}

// revokedPreRunning reports whether a pre-revoke state is one in which the
// session never started: the gateway may have claimed a pod or a slot for
// it, and no runtime session runs on it.
func revokedPreRunning(st session.State) bool {
	switch st {
	case session.StateCreated, session.StateFinalizing, session.StateReady:
		return true
	default:
		return false
	}
}
