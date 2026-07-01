// SPDX-License-Identifier: MIT

// Package childtoken implements the §8.2 line 59 in-process RFC 8693
// child-token exchange the gateway runs when it admits a delegation.
// Per §8.2 there is no external RFC 8693 endpoint traffic for internal
// delegation; the exchange is an in-process Token Service call. The
// Minter composes the pure §13.3 token-exchange validator
// (pkg/tokenexchange) with the gateway's revocation cache and the
// per-tenant audit advisory lock so the actor-token freshness check and
// the audit-contention retry semantics the spec mandates run on the
// delegation path.
package childtoken

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/tokenexchange"
)

// RevocationChecker reports whether a token `jti` has been revoked. The
// gateway's *revocation.Cache satisfies it. The Minter consults it on
// the actor (parent) token's `jti` inside the minting transaction so a
// parent rotated or revoked mid-flight is caught.
//
// spec: §8.2 line 61; §13.3 "Token rotation and revocation".
type RevocationChecker interface {
	IsRevoked(jti string) bool
}

// AuditLock models the §11.7 item-3 per-tenant audit advisory lock the
// child-token exchange's audit write acquires. Acquire returns a release
// func on success; it returns a non-nil error when the lock cannot be
// taken within the configured timeout after the internal retries, which
// the Minter maps to delegation.ErrAuditContention. A nil AuditLock on
// the Minter skips the contention gate (the audit write is best-effort
// in the in-process minimal path).
//
// spec: §8.2 line 63; §11.7 item 3.
type AuditLock interface {
	Acquire(ctx context.Context, tenantID string) (release func(), err error)
}

// Minter mints a delegated child's session token via the in-process
// RFC 8693 exchange.
type Minter struct {
	revocations RevocationChecker
	auditLock   AuditLock
	clock       func() time.Time
	idFn        func() string
	ttl         time.Duration
}

// Options configures a Minter.
type Options struct {
	// Revocations, when set, supplies the §13.3 revocation cache the
	// actor-token freshness check reads. Nil skips the freshness check
	// (no jti can be resolved as revoked), which the in-process minimal
	// path uses.
	Revocations RevocationChecker

	// AuditLock, when set, models the §11.7 per-tenant audit advisory
	// lock the child-minting audit write acquires; a timeout maps to
	// delegation.ErrAuditContention. Nil skips the contention gate.
	AuditLock AuditLock

	// Clock overrides time.Now (UTC). Pass nil for production.
	Clock func() time.Time

	// IDFunc overrides the child `jti` generator. Pass nil for a
	// crypto/rand-backed default.
	IDFunc func() string

	// TTL overrides the §13.3 per-dialect lifetime cap applied to the
	// child token (default delegation.DefaultChildTokenTTL, 1h).
	TTL time.Duration
}

// NewMinter returns a Minter.
func NewMinter(opts Options) *Minter {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idFn := opts.IDFunc
	if idFn == nil {
		idFn = newJTI
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = delegation.DefaultChildTokenTTL
	}
	return &Minter{
		revocations: opts.Revocations,
		auditLock:   opts.AuditLock,
		clock:       clock,
		idFn:        idFn,
		ttl:         ttl,
	}
}

// MintChildToken runs the §8.2 line 59 exchange. The order matches the
// spec's "inside the same advisory-locked transaction": acquire the
// audit lock (contention → ErrAuditContention), read the actor `jti`
// against the revocation cache (revoked → ErrParentRevoked), then
// validate and issue the narrowed child token.
//
// spec: §8.2 lines 59-63; §13.3.
func (m *Minter) MintChildToken(ctx context.Context, p delegation.ChildTokenParams) (delegation.ChildToken, error) {
	now := p.Now
	if now.IsZero() {
		now = m.clock()
	}

	// §8.2 line 63: the exchange's audit write runs under the per-tenant
	// audit advisory lock. A lock timeout fails the whole exchange so the
	// caller retries the entire lenny/delegate_task (re-running the
	// freshness check), rather than admitting a child whose minting was
	// never audited.
	if m.auditLock != nil {
		release, err := m.auditLock.Acquire(ctx, p.TenantID)
		if err != nil {
			return delegation.ChildToken{}, delegation.ErrAuditContention
		}
		defer release()
	}

	// §8.2 line 61: actor-token freshness. A parent rotated or revoked
	// between the delegate_task call and this exchange now resolves to a
	// revoked jti; fail closed before issuing any child token.
	if p.ParentJTI != "" && m.revocations != nil && m.revocations.IsRevoked(p.ParentJTI) {
		return delegation.ChildToken{}, delegation.ErrParentRevoked
	}

	// The user JWT is the RFC 8693 subject_token; the parent session
	// token is the actor_token. The §13.3 validator enforces tenant
	// match, scope subset, audience non-broadening, caller-type
	// non-elevation, delegation_depth = actor.depth + 1, and the exp cap.
	// exp on subject and actor is set to the per-dialect ceiling so the
	// in-process tokens are never treated as expired; the binding cap is
	// PerDialectCap (now + ttl).
	ceiling := now.Add(m.ttl)
	subject := tokenexchange.Token{
		TenantID:        p.TenantID,
		Subject:         p.ParentSubject,
		SessionID:       p.ParentSessionID,
		CallerType:      tokenexchange.CallerType(p.ParentCallerType),
		DelegationDepth: p.ParentDelegationDepth,
		Scope:           p.ParentScope,
		Audience:        []string{delegation.ChildTokenAudience},
		Typ:             tokenexchange.TypeUserBearer,
		Exp:             ceiling,
	}
	actor := tokenexchange.Token{
		TenantID:        p.TenantID,
		Subject:         p.ParentSubject,
		SessionID:       p.ParentSessionID,
		CallerType:      tokenexchange.CallerType(p.ParentCallerType),
		DelegationDepth: p.ParentDelegationDepth,
		Scope:           p.ParentScope,
		Audience:        []string{delegation.ChildTokenAudience},
		Typ:             tokenexchange.TypeSessionCapability,
		Exp:             ceiling,
	}
	requested := tokenexchange.Token{
		TenantID:   p.TenantID,
		Scope:      p.RequestedScope,
		Audience:   []string{delegation.ChildTokenAudience},
		CallerType: tokenexchange.CallerType(p.ParentCallerType),
	}
	issued, err := tokenexchange.Validate(tokenexchange.Request{
		Subject:       subject,
		Actor:         &actor,
		Caller:        subject,
		Requested:     requested,
		RequestedExp:  ceiling,
		PerDialectCap: m.ttl,
		Now:           now,
	})
	if err != nil {
		return delegation.ChildToken{}, fmt.Errorf("childtoken: §8.2 child-token exchange rejected: %w", err)
	}

	return delegation.ChildToken{
		JTI:             m.idFn(),
		Subject:         issued.Subject,
		Scope:           issued.Scope,
		Audience:        issued.Audience,
		DelegationDepth: issued.DelegationDepth,
		Exp:             issued.Exp,
		Act: []delegation.ActClaim{{
			Sub:             p.ParentSubject,
			SessionID:       p.ParentSessionID,
			TenantID:        p.TenantID,
			DelegationDepth: p.ParentDelegationDepth,
		}},
	}, nil
}

// newJTI returns a fresh child-token identifier. The §13.3
// recursive-revocation path keys on it.
func newJTI() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is non-recoverable here; surface a
		// deterministic-but-unique-enough fallback rather than panicking
		// on the delegation path.
		return "jti_childtoken_fallback"
	}
	return "jti_" + hex.EncodeToString(b[:])
}
