// SPDX-License-Identifier: MIT

// Package tokenexchange implements the §13.3 RFC 8693 token-exchange
// invariants as a pure function. The Token Service binary that ships
// in a later phase wires this validator into a Postgres-backed
// write-before-issue transaction.
//
// Invariants enforced:
//   - Scope narrowing: issued.scope ⊆ subject.scope (RFC 8693)
//   - Tenant match: issued.tenant_id == subject.tenant_id == caller.tenant_id
//   - delegation_depth: child = parent + 1 (with actor_token);
//                       rotation preserves
//   - caller_type cannot elevate
//   - audience cannot broaden (cannot add to subject's audiences)
//   - typ rules: only a2a_delegation child-minting (with actor_token)
//                produces typ = a2a_delegation; otherwise copied
//                from subject
//   - exp = min(requested_exp, subject.exp, actor.exp, perDialectCap)
//          with ±1s skew allowance for expiry checks
//
// The package is import-free of any JWT library; the gateway maps
// k8s.io / jose / jwt-go claim structs into the Token type before
// calling Validate.
package tokenexchange

import (
	"fmt"
	"time"
)

// TokenType is the §10.2 `typ` payload-claim enum. Reproduced here so
// this package stays import-free.
type TokenType string

const (
	TypeUserBearer        TokenType = "user_bearer"
	TypeSessionCapability TokenType = "session_capability"
	TypeA2ADelegation     TokenType = "a2a_delegation"
	TypeServiceToken      TokenType = "service_token"
)

// CallerType is the §13.3 caller_type enum. cannot-elevate ordering:
// agent ≤ service ≤ human.
type CallerType string

const (
	CallerHuman   CallerType = "human"
	CallerService CallerType = "service"
	CallerAgent   CallerType = "agent"
)

// callerRank is the cannot-elevate ranking. Higher rank == more
// privileged; exchanging from a lower rank to a higher rank is rejected.
func callerRank(c CallerType) int {
	switch c {
	case CallerAgent:
		return 0
	case CallerService:
		return 1
	case CallerHuman:
		return 2
	}
	return -1
}

// Token captures the subset of a Lenny-issued JWT the token-exchange
// validator reads. Mirrors the §13.3 claim table.
type Token struct {
	TenantID        string
	Subject         string
	SessionID       string
	CallerType      CallerType
	DelegationDepth int
	Scope           []string
	Audience        []string
	Typ             TokenType
	Exp             time.Time
}

// IsValid reports whether the token carries the §10.2-required typ
// and §13.3-required tenant_id.
func (t Token) IsValid() bool {
	if t.TenantID == "" {
		return false
	}
	switch t.Typ {
	case TypeUserBearer, TypeSessionCapability, TypeA2ADelegation, TypeServiceToken:
		return true
	}
	return false
}

// Request captures the inputs to Validate.
type Request struct {
	// Subject is the token being exchanged.
	Subject Token

	// Actor is the parent-session token in a delegation child-minting
	// exchange; nil for rotation and scope-narrowing exchanges.
	Actor *Token

	// Caller is the principal authenticated via the
	// Authorization: Bearer header. Used for the §13.3 cross-tenant
	// invariant: caller.tenant_id MUST equal subject.tenant_id.
	Caller Token

	// Requested describes the issued-token claims the caller asked
	// for. Validate enforces every §13.3 narrowing rule against these.
	Requested Token

	// RequestedExp is the caller's requested expiry; the issued
	// token's exp is min(this, subject.exp, actor.exp, perDialectCap).
	RequestedExp time.Time

	// PerDialectCap is the §13.3 per-dialect lifetime ceiling
	// (e.g., 1h for lease tokens, 24h for user bearers). Zero or
	// negative disables the cap.
	PerDialectCap time.Duration

	// Now is the validator's current wall-clock time. Tests inject
	// this; production wires time.Now().
	Now time.Time
}

// Issued is the validated child token shape Validate returns when the
// request is admissible.
type Issued struct {
	TenantID        string
	Subject         string
	SessionID       string
	CallerType      CallerType
	DelegationDepth int
	Scope           []string
	Audience        []string
	Typ             TokenType
	Exp             time.Time
}

// SkewAllowance is the §13.3 ±1s server-side skew on expiry checks.
const SkewAllowance = 1 * time.Second

// Validate applies every §13.3 invariant and returns the Issued token
// shape on success. Returns a typed error matching the §13.3 rejection
// catalogue otherwise.
func Validate(r Request) (Issued, error) {
	if !r.Subject.IsValid() {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "subject_invalid"}
	}
	if !r.Caller.IsValid() {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "caller_invalid"}
	}
	if r.Now.IsZero() {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "now_required"}
	}

	// §13.3 tenant-match: subject ↔ caller.
	if r.Subject.TenantID != r.Caller.TenantID {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "tenant_mismatch"}
	}
	// Issued tenant MUST equal subject tenant.
	if r.Requested.TenantID != "" && r.Requested.TenantID != r.Subject.TenantID {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "tenant_mismatch"}
	}

	// §13.3 subject expiry check with ±1s skew.
	if isExpired(r.Subject.Exp, r.Now) {
		return Issued{}, &ExchangeError{Code: "invalid_grant", Reason: "subject_token_expired"}
	}
	if r.Actor != nil {
		if !r.Actor.IsValid() {
			return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "actor_invalid"}
		}
		if isExpired(r.Actor.Exp, r.Now) {
			return Issued{}, &ExchangeError{Code: "invalid_grant", Reason: "actor_token_expired"}
		}
		if r.Actor.TenantID != r.Subject.TenantID {
			return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "tenant_mismatch"}
		}
	}

	// §13.3 scope narrowing: requested ⊆ subject.
	if !isSubset(r.Requested.Scope, r.Subject.Scope) {
		return Issued{}, &ExchangeError{Code: "invalid_scope", Reason: "scope_not_subset"}
	}

	// §13.3 audience: cannot broaden — every requested audience MUST
	// already appear in the subject's audience list.
	if !isSubset(r.Requested.Audience, r.Subject.Audience) {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "audience_broadened"}
	}

	// §13.3 caller_type: cannot elevate.
	if r.Requested.CallerType != "" {
		if callerRank(r.Requested.CallerType) > callerRank(r.Subject.CallerType) {
			return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "caller_type_elevated"}
		}
	}

	// §13.3 typ rules. When actor_token is present, the issued typ is
	// a2a_delegation regardless of subject. Otherwise, typ MUST match
	// subject (cannot mutate).
	issuedTyp := r.Subject.Typ
	if r.Actor != nil {
		issuedTyp = TypeA2ADelegation
	} else if r.Requested.Typ != "" && r.Requested.Typ != r.Subject.Typ {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "typ_mutation"}
	}

	// §13.3 delegation_depth: child = parent.depth + 1 when actor_token
	// is present; rotation preserves; cannot decrement.
	depth := r.Subject.DelegationDepth
	if r.Actor != nil {
		depth = r.Actor.DelegationDepth + 1
	} else if r.Requested.DelegationDepth != 0 && r.Requested.DelegationDepth != r.Subject.DelegationDepth {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "delegation_depth_changed"}
	}

	// §13.3 exp = min(requested, subject.exp, actor.exp, perDialectCap).
	exp := r.RequestedExp
	if exp.IsZero() || (!r.Subject.Exp.IsZero() && r.Subject.Exp.Before(exp)) {
		exp = r.Subject.Exp
	}
	if r.Actor != nil && !r.Actor.Exp.IsZero() && r.Actor.Exp.Before(exp) {
		exp = r.Actor.Exp
	}
	if r.PerDialectCap > 0 {
		capExp := r.Now.Add(r.PerDialectCap)
		if capExp.Before(exp) {
			exp = capExp
		}
	}
	// §13.3: exp is Unix seconds integer; round down to whole seconds.
	exp = exp.Truncate(time.Second)
	if !exp.After(r.Now) {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "exp_in_past"}
	}

	return Issued{
		TenantID:        r.Subject.TenantID,
		Subject:         r.Subject.Subject,
		SessionID:       r.Subject.SessionID,
		CallerType:      defaultString(r.Requested.CallerType, r.Subject.CallerType),
		DelegationDepth: depth,
		Scope:           r.Requested.Scope,
		Audience:        r.Requested.Audience,
		Typ:             issuedTyp,
		Exp:             exp,
	}, nil
}

// isExpired implements the §13.3 ±1s skew: a token is expired iff
// now - 1 > exp.
func isExpired(exp, now time.Time) bool {
	if exp.IsZero() {
		return false
	}
	return now.Add(-SkewAllowance).After(exp)
}

// isSubset reports whether every entry in candidate appears in
// universe. Empty candidate is always a subset.
func isSubset(candidate, universe []string) bool {
	if len(candidate) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(universe))
	for _, s := range universe {
		set[s] = struct{}{}
	}
	for _, s := range candidate {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func defaultString[T ~string](a, b T) T {
	if a != "" {
		return a
	}
	return b
}

// ExchangeError is the typed error Validate returns when a §13.3
// invariant fails. Code matches the RFC 8693 error code; Reason is
// the §13.3-specific subreason (e.g., tenant_mismatch,
// subject_token_expired, caller_type_elevated).
type ExchangeError struct {
	Code   string
	Reason string
}

func (e *ExchangeError) Error() string {
	return fmt.Sprintf("tokenexchange: %s: %s (§13.3)", e.Code, e.Reason)
}
