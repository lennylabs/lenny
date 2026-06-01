// SPDX-License-Identifier: MIT

package delegation

import (
	"context"
	"errors"
	"time"
)

// ChildTokenAudience is the §8.2 line 59 fixed `audience` the child
// session token is minted for: the internal gateway. The RFC 8693
// exchange sets the issued token's audience to this single value.
const ChildTokenAudience = "lenny-gateway"

// DefaultChildTokenTTL is the §13.3 per-dialect lifetime ceiling applied
// to a delegated child's session token (lease-token dialect, 1h). The
// minted `exp` is capped at min(parent.exp, now + this).
const DefaultChildTokenTTL = time.Hour

// ParentToken is the §8.2 line 59 RFC 8693 `actor_token` material the
// delegating pod presents on the `lenny/delegate_task` call: the parent
// session token's claims. The §8.5 handler builds it from the
// authenticated principal (the parent pod's session-capability token).
// It populates the child's `act` chain (parent `sub`, `session_id`,
// `tenant_id`, `delegation_depth`) and drives the §13.3 actor-token
// freshness check (the parent `jti` is read against the revocation
// cache). A nil ParentToken skips the child-token exchange leg entirely
// (the in-process minimal gateway and tests that authenticate no
// caller).
//
// spec: §8.2 lines 59-61.
type ParentToken struct {
	// Subject is the parent token `sub`: the authenticated user. It is
	// the RFC 8693 `subject_token` identity the child inherits.
	Subject string

	// SessionID is the parent session id, recorded in the child `act`
	// chain.
	SessionID string

	// JTI is the parent session token's `jti`, read against the §13.3
	// revocation cache inside the child-minting transaction. Empty
	// skips the freshness check (no jti to resolve).
	JTI string

	// Scope is the parent token's granted scope. The child's scope can
	// only narrow this set (§13.3 scope-subset invariant).
	Scope []string

	// CallerType is the parent token's `caller_type`. The child cannot
	// elevate it.
	CallerType string
}

// ActClaim is one entry of the §13.3 `act` chain stamped on a minted
// child token: the ancestor session token it acts on behalf of.
type ActClaim struct {
	Sub             string `json:"sub"`
	SessionID       string `json:"session_id"`
	TenantID        string `json:"tenant_id"`
	DelegationDepth int    `json:"delegation_depth"`
}

// ChildTokenParams are the inputs the delegation Service hands a
// ChildTokenMinter for one §8.2 child-token exchange.
type ChildTokenParams struct {
	TenantID              string
	ChildSessionID        string
	ParentSessionID       string
	ParentSubject         string
	ParentJTI             string
	ParentDelegationDepth int
	// ParentScope is the actor token's granted scope (the universe the
	// child narrows from). RequestedScope must be a subset of it.
	ParentScope []string
	// RequestedScope is the child's scope narrowed per the LeaseSlice.
	// v1 has no per-lease scope axis, so the Service passes the parent
	// scope verbatim; the field is distinct so a future LeaseSlice scope
	// list narrows here without changing the interface.
	RequestedScope   []string
	ParentCallerType string
	// Now is the exchange's wall-clock instant. Zero lets the minter use
	// its own clock.
	Now time.Time
}

// ChildToken is the §8.2 RFC 8693 child session token a ChildTokenMinter
// returns on a successful exchange: the narrowed scope, the `act` chain
// naming the parent, the capped expiry, the delegation depth fixed at
// parent + 1, and the `jti` the §13.3 recursive-revocation path keys on.
type ChildToken struct {
	JTI             string
	Subject         string
	Scope           []string
	Audience        []string
	DelegationDepth int
	Exp             time.Time
	// Act is the §13.3 act chain (innermost ancestor first).
	Act []ActClaim
}

// ChildTokenMinter performs the §8.2 line 59 internal RFC 8693
// token-exchange that mints a delegated child's session token. Per §8.2
// "the exchange is an in-process Token Service call"; implementations
// narrow scope, build the `act` chain, fix `delegation_depth` at
// parent + 1, cap `exp`, and read the actor token's `jti` against the
// §13.3 revocation cache inside the audit-locked minting transaction.
//
// An implementation returns ErrParentRevoked when the actor token
// resolves to a revoked `jti` (§8.2 line 61) and ErrAuditContention when
// the per-tenant audit advisory lock times out during the exchange
// (§8.2 line 63). *childtoken.Minter implements it. A nil minter on the
// Service skips the exchange leg.
//
// spec: §8.2 lines 59-63; §13.3 credential flow.
type ChildTokenMinter interface {
	MintChildToken(ctx context.Context, p ChildTokenParams) (ChildToken, error)
}

var (
	// ErrParentRevoked — the §8.2 line 61 actor-token freshness check
	// found the parent token's `jti` revoked between the
	// `lenny/delegate_task` call and the child-token exchange. The
	// exchange fails closed (no child token, no child pod) and the §8.5
	// handler surfaces DELEGATION_PARENT_REVOKED.
	ErrParentRevoked = errors.New("delegation: actor_token_revoked: parent token revoked mid-flight (§8.2)")

	// ErrAuditContention — the §8.2 line 63 per-tenant audit advisory
	// lock timed out during the child-token exchange after the internal
	// retries. The §8.5 handler surfaces the retryable
	// DELEGATION_AUDIT_CONTENTION; the caller retries the entire
	// `lenny/delegate_task`.
	ErrAuditContention = errors.New("delegation: audit advisory lock contention during child-token minting (§8.2)")
)
