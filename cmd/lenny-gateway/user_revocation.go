// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
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
// §17.6 admin token so the token is revocable, records a rotated token
// with its §16.7 token.exchanged audit row, and durably revokes the prior
// token (with the token.revoked audit row) before pushing the revocation
// onto the cross-replica cache, so the old token stops validating
// immediately (the §17.6 no-grace-period guarantee) and a durable-write
// failure never leaves the cache holding a revocation the authoritative
// store lacks. The adapter owns the §16.7 audit-payload vocabulary; the
// admintoken package passes only domain data.
//
// spec: §13.3 (gateway-mediated admin-credential rotation ordering and
// mandatory exchange/revoke audit, lines 587/599), §16.7 (token.exchanged
// exchange_type=admin_rotation, line 672; token.revoked rotation_replaced,
// line 673) — F-17.6.3.
type adminIssuedTokens struct {
	store *issuedtokenstore.Store
	cache admin.RevocationCache
}

// §16.7 admin-rotation audit vocabulary. The gateway is a second emitter
// of token.exchanged{admin_rotation} (its in-process bootstrap Secret
// rotation) alongside the Token Service's /v1/oauth/token exchange grant;
// both classify the rotation identically. spec: §16.7 lines 672, 673.
const (
	adminRotationExchangeType     = "admin_rotation"
	adminRotationRevocationReason = "rotation_replaced"
	// adminRotationPropagationMode records that the durable Postgres write
	// is the authoritative revocation store; a peer replica falls back to
	// issued_tokens.revoked_at, so the durable revoke alone satisfies the
	// no-grace-period guarantee. The gateway path does not publish on the
	// EventBus, so the mode is postgres_only. spec: §16.7 line 673.
	adminRotationPropagationMode = "postgres_only"
)

func (a adminIssuedTokens) issuedToken(rec admintoken.MintedToken) issuedtokenstore.IssuedToken {
	return issuedtokenstore.IssuedToken{
		JTI:       rec.JTI,
		TenantID:  rec.TenantID,
		Subject:   rec.Subject,
		TokenHash: rec.TokenHash,
		IssuedAt:  rec.IssuedAt,
		ExpiresAt: rec.ExpiresAt,
	}
}

// Record persists the bootstrap first token with no audit row. The
// initial credential issuance is not a token exchange, so no
// token.exchanged row is emitted (§13.3 line 587).
func (a adminIssuedTokens) Record(ctx context.Context, rec admintoken.MintedToken) error {
	return a.store.Record(ctx, a.issuedToken(rec))
}

// RecordWithExchangeAudit persists a rotated token and writes the §16.7
// token.exchanged{exchange_type: admin_rotation} audit row in the same
// transaction. It does not revoke the prior token: the §13.3 ordering
// persists the Secret before the durable revoke.
func (a adminIssuedTokens) RecordWithExchangeAudit(ctx context.Context, rec admintoken.MintedToken) error {
	payload, err := json.Marshal(map[string]any{
		"exchange_type": adminRotationExchangeType,
		"caller_sub":    rec.Subject,
		"subject_sub":   rec.Subject,
		"jti":           rec.JTI,
		"policy_result": "allow",
		"timestamp":     rec.IssuedAt,
	})
	if err != nil {
		return fmt.Errorf("marshal token.exchanged payload: %w", err)
	}
	_, err = a.store.RecordWithAudit(ctx, a.issuedToken(rec),
		string(obsaudit.EventTokenExchanged), payload, rec.IssuedAt)
	return err
}

// DurableRevoke durably revokes jti with revocation_reason=
// rotation_replaced, binds the §16.7 token.revoked audit row to the same
// transaction, and only then pushes the jti onto the cross-replica
// revocation cache, so a durable-write failure never leaves the cache
// holding a revocation the authoritative store lacks (a §12.2 rehydration
// reads issued_tokens.revoked_at, so an in-memory-only revocation would
// silently un-revoke the old token). An already-revoked or absent jti is a
// no-op (ErrNotFound), so a retried rotation does not double-emit.
func (a adminIssuedTokens) DurableRevoke(ctx context.Context, tenantID, jti string, at time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"revoked_jti":       jti,
		"revocation_reason": adminRotationRevocationReason,
		"propagation_mode":  adminRotationPropagationMode,
		"timestamp":         at,
	})
	if err != nil {
		return fmt.Errorf("marshal token.revoked payload: %w", err)
	}
	_, err = a.store.RevokeWithAudit(ctx, tenantID, jti, adminRotationRevocationReason,
		string(obsaudit.EventTokenRevoked), payload, at)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		// Already revoked (a retry) or never issued: idempotent no-op, and
		// no cache push for a revocation the durable store did not apply.
		return nil
	}
	if err != nil {
		// The durable write failed. Do NOT push onto the cross-replica
		// cache: a cache-only revocation a rehydration would later erase is
		// exactly the fail-open the §13.3 authoritative-durability invariant
		// forbids. Surface the error so the caller retries.
		return fmt.Errorf("durable revoke of admin token %q: %w", jti, err)
	}
	// Durable write committed: now push onto the cross-replica cache so the
	// old token stops validating immediately without waiting for a
	// rehydration.
	if a.cache != nil {
		a.cache.Revoke(jti)
	}
	return nil
}

// WithSubjectLock serializes the whole non-atomic rotation read-modify-write
// for one subject through the store's per-subject session-scoped advisory
// lock. spec: §13.3 line 605.
func (a adminIssuedTokens) WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error {
	return a.store.WithSubjectLock(ctx, tenantID, subject, fn)
}
