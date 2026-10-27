// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/bindrelease"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// Compile-time assertion that bindReleaseFanOut is the cross-replica
// §11.4 step-2 local releaser the binding-release propagator drives.
var _ bindrelease.LocalTerminator = (*bindReleaseFanOut)(nil)

// This file wires the §11.4 full_revoke fan-out dependencies the admin
// router consumes. handleInvalidateUser raises the user's tombstone and
// cancels their sessions in the SessionStore on its own; the adapters
// here carry the remaining §11.4 propagation onto infrastructure the
// admin layer does not own — the pod bindings, the §4.9 credential
// leases (through leaserevoke.Revoker), and the §13.3 issued tokens. Each is independently optional,
// so a minimal gateway that wires none of them still soft/hard disables
// a user.

// userTerminateRPCTimeout bounds each full_revoke binding release: the
// wait for the session's slot-accounting lock and the release itself,
// whose §4.7 Shutdown carries the release path's own deadline. The bound
// is detached from the admin request, so a cancelled request cannot cut a
// release that has already removed the binding from the registry.
const userTerminateRPCTimeout = 20 * time.Second

// revokedBindingReleaser releases this replica's binding for a session the
// §11.4 full_revoke cancelled. The session server's ReleaseRevokedBinding
// satisfies it: it reads the row from the primary and runs the terminal
// binding release body, so the binding is released once whichever of
// full_revoke and the coordination Sweeper reaches it first. released is
// true only when the executor release returned nil; err is non-nil when a
// primary read, a lock wait, or the executor release failed.
type revokedBindingReleaser interface {
	ReleaseRevokedBinding(ctx context.Context, tenantID, sessionID string) (released bool, err error)
}

// bindReleaseFanOut releases the pod bindings of a revoked user's
// cancelled sessions and publishes the session list on CH-BINDRELEASE
// (§28.5.7). It reads the per-replica pod-session registry and
// releases the binding of every cancelled session this replica holds
// through the session release, which sends the pod the §4.7 Shutdown RPC.
// It satisfies admin.UserPodTerminator and, for the cross-replica path,
// bindrelease.LocalTerminator.
//
// A pod binding lives in exactly one replica's registry, so the handling
// replica reaches only the bindings it holds itself. prop fans the §11.4
// step-2 release request out to peer replicas over Redis pub/sub so the
// bindings they hold are released too; without it, or when a publish is
// lost, a peer's coordination Sweeper releases the binding of every
// terminal row it lists. prop is nil in a deployment with no Redis bus,
// which is the single-replica posture. spec: §11.4 step 2.
type bindReleaseFanOut struct {
	registry *podsession.Registry
	releaser revokedBindingReleaser
	prop     *bindrelease.Propagator
}

// TerminateUserSessions is the admin.UserPodTerminator entry the
// full_revoke handler drives. It releases the bindings this replica holds
// for the cancelled sessions and fans the §11.4 step-2 request out to peer
// replicas so the bindings they hold are released too. The returned result
// reports only this replica's releases — the peer releases happen
// asynchronously on each peer's subscriber, matching the §11.4 note that
// propagation completes within seconds.
func (p *bindReleaseFanOut) TerminateUserSessions(ctx context.Context, tenantID, userID string, sessionIDs []string) admin.UserTerminationResult {
	req := bindrelease.Request{
		TenantID:   tenantID,
		UserID:     userID,
		SessionIDs: sessionIDs,
	}
	res := p.terminateLocal(ctx, req)
	if p.prop != nil {
		p.prop.Publish(ctx, req)
	}
	return admin.UserTerminationResult{PodsTerminated: res.PodsTerminated, FailedSessions: res.FailedSessions}
}

// TerminateLocal is the bindrelease.LocalTerminator entry the
// cross-replica subscriber drives for a peer replica's request. It
// releases only the bindings this replica holds and does not re-publish.
func (p *bindReleaseFanOut) TerminateLocal(ctx context.Context, req bindrelease.Request) bindrelease.Result {
	return p.terminateLocal(ctx, req)
}

// terminateLocal releases, through the revoked-binding releaser, the
// binding of each of req.SessionIDs that this replica's registry holds,
// and maps each result into the §11.4 response accounting: a release
// counts one pod, a release that returned an error records the session as
// a failure, and an outcome that released nothing (a refused release, a
// stale or replaced binding, or a row not terminal) counts nothing. A
// session with no registry entry here is skipped; its binding is held on
// another replica or already released.
//
// Each release runs on a context detached from ctx and bounded by
// userTerminateRPCTimeout, so a cancelled admin request cannot cut a
// release that has already removed the binding.
//
// spec: §11.4 step 2 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func (p *bindReleaseFanOut) terminateLocal(ctx context.Context, req bindrelease.Request) bindrelease.Result {
	want := make(map[string]struct{}, len(req.SessionIDs))
	for _, id := range req.SessionIDs {
		want[id] = struct{}{}
	}
	var res bindrelease.Result
	for _, bind := range p.registry.Snapshot() {
		if _, ok := want[bind.SessionID]; !ok {
			continue
		}
		released, err := p.releaseOne(ctx, req.TenantID, bind.SessionID)
		switch {
		case err != nil:
			log.Printf("lenny-gateway: §11.4 full_revoke: release the binding of session %s (user %s/%s) failed: %v",
				bind.SessionID, req.TenantID, req.UserID, err)
			res.FailedSessions = append(res.FailedSessions, bind.SessionID)
		case released:
			res.PodsTerminated++
		}
	}
	return res
}

// releaseOne runs one revoked-binding release on its own bounded, detached
// call context.
func (p *bindReleaseFanOut) releaseOne(ctx context.Context, tenantID, sessionID string) (bool, error) {
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), userTerminateRPCTimeout)
	defer cancel()
	return p.releaser.ReleaseRevokedBinding(callCtx, tenantID, sessionID)
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

// revocationPropagationObserver observes the §16.1
// lenny_token_revocation_propagation_seconds histogram keyed by the §16.7
// propagation_mode outcome. Defined at the consumer so the gateway metrics
// emitter (*gatewaymetrics.Metrics) satisfies it without this file
// depending on the metric registration. A nil observer disables the
// observation, matching the other optional gateway dependencies. SEC-TS-1.
type revocationPropagationObserver interface {
	ObserveRevocationPropagation(outcome string, d time.Duration)
}

// adminIssuedTokenStore is the slice of the §13.3 issued-token store the
// admin-rotation adapter needs. It is defined at the consumer (rather than
// the producer *issuedtokenstore.Store) so a unit test can substitute a
// fake and exercise the audit-vocabulary and SEC-TS-1 metric wiring without
// a Postgres pool. The production wiring passes *issuedtokenstore.Store.
type adminIssuedTokenStore interface {
	Record(ctx context.Context, tok issuedtokenstore.IssuedToken) error
	RecordWithAudit(ctx context.Context, tok issuedtokenstore.IssuedToken, sources []issuedtokenstore.SourceToken, auditEventType string, auditPayload json.RawMessage, auditAt time.Time) (audit.Row, error)
	RevokeWithAudit(ctx context.Context, tenantID, jti, revokedReason, auditEventType string, auditPayload json.RawMessage, at time.Time) (audit.Row, error)
	WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error
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
// spec: §13.3, §16.7 — F-17.6.3.
type adminIssuedTokens struct {
	store adminIssuedTokenStore
	cache admin.RevocationCache
	// metrics observes the §16.1 revocation-propagation histogram on the
	// durable-revoke path. The gateway's in-process admin-credential
	// rotation is always postgres_only (no EventBus publish), so this
	// wires the gateway as the SEC-TS-1 producer the C3/§7 design lists
	// alongside the Token Service path. Optional: a nil metrics field is a
	// no-op. SEC-TS-1.
	metrics revocationPropagationObserver
	// clock brackets the durable-revoke latency the histogram observes.
	// Defaults to time.Now.UTC when nil.
	clock func() time.Time
}

// now returns the adapter's injected clock or the wall clock.
func (a adminIssuedTokens) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now().UTC()
}

// §16.7 admin-rotation audit vocabulary. The gateway is a second emitter
// of token.exchanged{admin_rotation} (its in-process bootstrap Secret
// rotation) alongside the Token Service's /v1/oauth/token exchange grant;
// both classify the rotation identically. spec: §16.7.
const (
	adminRotationExchangeType     = "admin_rotation"
	adminRotationRevocationReason = "rotation_replaced"
	// adminRotationPropagationMode records that the durable Postgres write
	// is the authoritative revocation store; a peer replica falls back to
	// issued_tokens.revoked_at, so the durable revoke alone satisfies the
	// no-grace-period guarantee. The gateway path does not publish on the
	// EventBus, so the mode is postgres_only. spec: §16.7.
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
// token.exchanged row is emitted (§13.3).
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
		// spec: §16.7 — token.exchanged policy_result enum is
		// (accepted | rejected:<reason>). A successful rotation mirrors the
		// Token Service emitter (pkg/tokenservice PolicyResult="accepted").
		"policy_result": "accepted",
		"timestamp":     rec.IssuedAt,
	})
	if err != nil {
		return fmt.Errorf("marshal token.exchanged payload: %w", err)
	}
	// The admin rotation presents no subject token to the store, so the
	// §13.3 step (1a) re-read has nothing to re-read and the source list
	// is empty. WithSubjectLock serializes concurrent admin rotations.
	_, err = a.store.RecordWithAudit(ctx, a.issuedToken(rec), nil,
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
	// Bracket the durable revoke so the §16.1
	// lenny_token_revocation_propagation_seconds histogram observes the
	// gateway path's propagation latency keyed by the §16.7 propagation_mode
	// (postgres_only on the gateway). This wires the gateway as the SEC-TS-1
	// producer the C3/§7 design lists alongside the Token Service. The
	// observation runs only once the durable write commits, mirroring the
	// Token Service's post-COMMIT observation in propagateRevocation.
	start := a.now()
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
	if a.metrics != nil {
		a.metrics.ObserveRevocationPropagation(adminRotationPropagationMode, a.now().Sub(start))
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
// lock. spec: §13.3.
func (a adminIssuedTokens) WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error {
	return a.store.WithSubjectLock(ctx, tenantID, subject, fn)
}
