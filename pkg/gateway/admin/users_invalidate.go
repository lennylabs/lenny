// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"time"
)

// §11.4 full_revoke fan-out. handleInvalidateUser raises the user's
// deleted_at tombstone, transitions their non-terminal sessions to
// `cancelled` in the SessionStore, and dismisses their pending
// elicitations. The remaining §11.4 propagation steps act on
// infrastructure the admin Router does not own directly — the pods
// hosting the user's sessions, the §4.9 credential leases those
// sessions hold, and the §13.3 issued tokens the user was granted. The
// Router reaches them through the optional dependencies below, each
// wired by the gateway and each nil in a minimal deployment.
//
// Every fan-out is best-effort: a pod that fails to terminate, a lease
// that fails to revoke, or an unreachable token index does not abort
// the rest of full_revoke. The handler records what completed and what
// failed in the audit event and the response body, so an operator sees
// a partial propagation rather than a silent one.

// UserPodTerminator terminates the pods hosting a user's live sessions.
// On §11.4 full_revoke the gateway sends the §4.7 `Terminate` RPC
// (reason `USER_REVOKED`) to every pod this replica holds a binding for
// among the user's sessions. The gateway's pod-session registry
// satisfies it; a deployment without warm-pod placement wires nil and
// the SessionStore transition alone ends the user's sessions.
type UserPodTerminator interface {
	// TerminateUserSessions terminates the pods hosting the named
	// sessions. It is best-effort per pod: one pod failing to terminate
	// must not stop the others. The result reports the counts and the
	// identifiers of the sessions whose pod termination failed.
	TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) UserTerminationResult
}

// UserTerminationResult reports the outcome of the §11.4 full_revoke
// pod-RPC `Terminate` fan-out.
type UserTerminationResult struct {
	// PodsTerminated counts the pods the fan-out terminated.
	PodsTerminated int
	// FailedSessions lists the sessions whose pod termination failed.
	// The failure is recorded and the rest of full_revoke proceeds.
	FailedSessions []string
}

// UserLeaseRevoker revokes the §4.9 credential leases held by a user's
// sessions. On §11.4 full_revoke the gateway drops each lease from the
// credential-lease store and adds its credential to the §4.9 deny list,
// so the LLM proxy rejects an in-flight request that still carries the
// lease token. A deployment without credential leasing wires nil.
type UserLeaseRevoker interface {
	// RevokeUserLeases revokes every credential lease held by the named
	// sessions and returns the count revoked. It is best-effort: a lease
	// that cannot be revoked does not stop the others.
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

// WithUserRevocation wires the §11.4 full_revoke fan-out dependencies
// onto the Router: the pod terminator, the credential-lease revoker,
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
}

// runFullRevokeFanOut runs the §11.4 full_revoke infrastructure
// propagation for the named user sessions: terminate the hosting pods,
// revoke the sessions' credential leases, and invalidate the user's
// cached auth. Each step is independently best-effort and skipped when
// its dependency is not wired. liveSessions is the set of the user's
// non-terminal sessions the SessionStore step already cancelled.
func (r *Router) runFullRevokeFanOut(ctx context.Context, tenantID, userID, reason string, liveSessions []string) fullRevokeOutcome {
	var out fullRevokeOutcome

	// §11.4 step 2-4: send the §4.7 Terminate RPC to every pod hosting
	// one of the user's sessions. Best-effort per pod.
	if r.userPods != nil && len(liveSessions) > 0 {
		res := r.userPods.TerminateUserSessions(ctx, tenantID, userID, liveSessions)
		out.podsTerminated = res.PodsTerminated
		out.podTerminateFailed = res.FailedSessions
	}

	// §11.4 step 6: revoke the credential leases the user's sessions
	// hold and add them to the §4.9 deny list. Best-effort per lease.
	if r.userLeases != nil && len(liveSessions) > 0 {
		out.leasesRevoked = r.userLeases.RevokeUserLeases(tenantID, userID, liveSessions)
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

	return out
}
