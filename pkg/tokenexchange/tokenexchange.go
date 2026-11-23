// SPDX-License-Identifier: MIT

// Package tokenexchange implements the §13.3 RFC 8693 token-exchange
// invariants as a pure function. The Token Service binary that ships
// in a later phase wires this validator into a Postgres-backed
// write-before-issue transaction.
//
// Invariants enforced:
//   - Scope narrowing: subject.scope grants every requested value
//     under the scope grammar of pkg/common/scopes (an absent claim and
//     tools:* grant every value); a request without scope issues
//     subject.scope
//   - A caller other than the subject must request a strictly narrower
//     scope; with an actor_token, the actor must be the caller, a
//     subject other than the actor must be narrowed strictly, and the
//     actor's scope must grant the child's scope
//   - A playground-origin subject or actor is refused
//   - Tenant match: issued.tenant_id == subject.tenant_id == caller.tenant_id
//   - delegation_depth: child = actor + 1 (with actor_token);
//     other exchanges preserve the subject's
//   - act: the actor's identity with an actor_token; otherwise copied
//     from the subject
//   - caller_type cannot elevate
//   - audience cannot broaden (cannot add to subject's audiences)
//   - typ rules: only a2a_delegation child-minting (with actor_token)
//     produces typ = a2a_delegation; otherwise copied
//     from subject
//   - exp = min(requested_exp, subject.exp, actor.exp, perDialectCap)
//     with ±1s skew allowance for expiry checks
//
// The package imports no JWT library; the gateway maps
// k8s.io / jose / jwt-go claim structs into the Token type before
// calling Validate.
package tokenexchange

import (
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/common/scopes"
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
	// AuthorizedTools is the §13.3 narrowed operability-tool allowlist.
	// An exchange may preserve or further narrow it; broadening (a
	// requested tool not present on the subject) is rejected.
	AuthorizedTools []string
	Exp             time.Time
	// Origin is the token's `origin` claim. OriginPlayground marks a
	// playground bearer, which Validate refuses as a subject or actor.
	Origin string
	// Act is the token's RFC 8693 `act` claim, nil when it carries none.
	// Validate copies a subject's Act onto a token issued without an
	// actor_token, so a narrowed or rotated delegation child keeps the
	// name of its delegator.
	Act *Actor
}

// OriginPlayground is the `origin` claim value of a playground bearer.
// Reproduced here so this package stays free of the JWT packages.
const OriginPlayground = "playground"

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

	// Actor is the delegating caller's token in a delegation child-minting
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
	// AuthorizedTools is the §13.3 operability-tool allowlist carried onto
	// the issued token: the subject's list when the request narrows
	// nothing, or the requested subset when it does. For a child-minting
	// exchange (actor_token present) it keeps the entries the issued
	// scope grants (§13.3 enforcement rule (e)).
	AuthorizedTools []string
	Exp             time.Time
	// Act is the §13.3 RFC 8693 `act` claim of the issued token: the
	// actor token's sub, tenant_id, session_id, and delegation_depth when
	// the request carries an actor_token, and otherwise a copy of the
	// subject's `act` claim, nil when the subject carries none.
	Act *Actor
}

// Actor is the identity of the delegating caller an a2a_delegation child
// token names in its `act` claim. It mirrors the four fields the §13.3
// "Lenny JWT claim structure" `act` row lists, copied from the actor
// token rather than from the subject, so the child names the caller that
// delegated even when the subject is a different principal.
type Actor struct {
	Subject         string
	TenantID        string
	SessionID       string
	DelegationDepth int
}

// actorOf returns the §13.3 `act` identity of the actor token, or nil when
// the exchange carries no actor_token.
func actorOf(t *Token) *Actor {
	if t == nil {
		return nil
	}
	return &Actor{
		Subject:         t.Subject,
		TenantID:        t.TenantID,
		SessionID:       t.SessionID,
		DelegationDepth: t.DelegationDepth,
	}
}

// issuedAct returns the `act` claim of the issued token: the actor token's
// identity on a child-minting exchange, and a copy of the subject's `act`
// claim on every other exchange, nil when the subject carries none.
//
// spec: §13.3 (Credential Flow) "Lenny JWT claim structure" (the `act`
// row).
func issuedAct(actor *Token, subjectAct *Actor) *Actor {
	if actor != nil {
		return actorOf(actor)
	}
	if subjectAct == nil {
		return nil
	}
	cp := *subjectAct
	return &cp
}

// checkActorCoversChild refuses a child-minting exchange whose issued scope
// the actor token's scope does not grant, so a child is never broader than
// the token that delegates it. An actor with an absent scope claim grants
// every issued scope. An issued scope with no value carries no restriction,
// so it is granted only by an actor whose scope grants tools:*. A nil actor
// (an exchange without an actor_token) is not checked.
//
// spec: §13.3 (Credential Flow) "Client authentication on
// /v1/oauth/token."
func checkActorCoversChild(actor *Token, issuedScope []string) error {
	if actor == nil || len(actor.Scope) == 0 {
		return nil
	}
	required := issuedScope
	if len(required) == 0 {
		required = []string{scopes.AllTools}
	}
	if grantsAll(actor.Scope, required) {
		return nil
	}
	return &ExchangeError{Code: "invalid_scope", Reason: "scope_exceeds_actor"}
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

	if err := refusePlaygroundSources(r.Subject, r.Actor); err != nil {
		return Issued{}, err
	}

	issuedScope, err := resolveIssuedScope(r.Requested.Scope, r.Subject.Scope)
	if err != nil {
		return Issued{}, err
	}
	if err := checkCallerIdentity(r); err != nil {
		return Issued{}, err
	}
	if err := checkActorCoversChild(r.Actor, issuedScope); err != nil {
		return Issued{}, err
	}

	// §13.3 audience: cannot broaden — every requested audience MUST
	// already appear in the subject's audience list.
	if !isSubset(r.Requested.Audience, r.Subject.Audience) {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "audience_broadened"}
	}

	// §13.3 authorized_tools (table row + enforcement rule (e)):
	// "preserving or narrowing ... broadening is rejected". A requested
	// allowlist that names a tool the subject did not hold is a broadening
	// attempt and is rejected; an empty requested allowlist preserves the
	// subject's. For a child-minting exchange (actor_token present) the
	// resulting allowlist keeps only the entries the issued scope grants,
	// so a child whose scope dropped an operability tool cannot retain
	// authorization for it, and a child with an absent scope keeps them all.
	if !isSubset(r.Requested.AuthorizedTools, r.Subject.AuthorizedTools) {
		return Issued{}, &ExchangeError{Code: "invalid_request", Reason: "authorized_tools_broadened"}
	}
	authorizedTools := r.Subject.AuthorizedTools
	if len(r.Requested.AuthorizedTools) > 0 {
		authorizedTools = r.Requested.AuthorizedTools
	}
	if r.Actor != nil && len(authorizedTools) > 0 {
		authorizedTools = grantedEntries(authorizedTools, issuedScope)
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
		Scope:           issuedScope,
		Audience:        r.Requested.Audience,
		AuthorizedTools: authorizedTools,
		Typ:             issuedTyp,
		Exp:             exp,
		Act:             issuedAct(r.Actor, r.Subject.Act),
	}, nil
}

// resolveIssuedScope returns the scope an exchange issues: the subject's
// scope when the request names none, and the requested scope when the
// subject's scope grants every requested value. An empty issued scope
// becomes an empty claim, which carries no restriction, so the default
// keeps the issued token no broader than its subject. The result is a
// copy and shares no backing array with the request.
//
// spec: §13.3 (Credential Flow) "Scope narrowing enforcement."
func resolveIssuedScope(requested, subject []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), subject...), nil
	}
	if !grantsAll(subject, requested) {
		return nil, &ExchangeError{Code: "invalid_scope", Reason: "scope_not_subset"}
	}
	return append([]string(nil), requested...), nil
}

// grantsAll reports whether the granted scope values permit every
// requested value. Each requested value must parse as a canonical scope,
// and the granted values are read as one claim, so an empty granted list
// (an absent claim) and tools:* permit every canonical value and
// tools:<domain>:* permits every action of its domain. A granted list
// that does not parse permits nothing, which fails closed. An empty
// requested list is permitted.
//
// spec: §13.3 (Credential Flow) "Scope narrowing enforcement."
func grantsAll(granted, requested []string) bool {
	set, err := scopes.Parse(strings.Join(granted, " "))
	if err != nil {
		return false
	}
	for _, v := range requested {
		if _, err := scopes.ParseScope(v); err != nil {
			return false
		}
		if !set.Matches(v) {
			return false
		}
	}
	return true
}

// strictlyNarrower reports whether the requested scope is strictly
// narrower than the subject's: the subject grants every requested value,
// and the requested values do not grant every value of the subject. An
// absent subject claim counts as tools:*. A tools:* value is compared as
// the domain wildcard of every domain, so a request that lists every
// tools:<domain>:* value grants what tools:* grants and is not narrower.
// An empty request is never strictly narrower.
//
// spec: §13.3 (Credential Flow) "Client authentication on
// /v1/oauth/token."
func strictlyNarrower(requested, subject []string) bool {
	if len(requested) == 0 || !grantsAll(subject, requested) {
		return false
	}
	effective := subject
	if len(effective) == 0 {
		effective = []string{scopes.AllTools}
	}
	return !grantsAll(requested, expandAllTools(effective))
}

// expandAllTools replaces each tools:* value with the domain wildcard of
// every domain in the closed taxonomy, so a grant check against the result
// asks whether every domain is fully granted.
func expandAllTools(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != scopes.AllTools {
			out = append(out, v)
			continue
		}
		for domain := range scopes.Domains {
			out = append(out, "tools:"+domain+":"+scopes.Wildcard)
		}
	}
	return out
}

// checkCallerIdentity refuses an exchange whose caller is not the
// subject's principal unless the request names a strictly narrower
// scope. The caller differs when its sub differs from the subject's, or
// when both tokens carry a caller_type and the two differ; an upstream
// IdP caller token carries none. A child-minting exchange (actor_token
// present) takes checkChildMintIdentity instead.
//
// spec: §13.3 (Credential Flow) "Client authentication on
// /v1/oauth/token."
func checkCallerIdentity(r Request) error {
	if r.Actor != nil {
		return checkChildMintIdentity(r)
	}
	sameSub := r.Caller.Subject == r.Subject.Subject
	sameType := r.Caller.CallerType == "" || r.Subject.CallerType == "" || r.Caller.CallerType == r.Subject.CallerType
	if sameSub && sameType {
		return nil
	}
	if strictlyNarrower(r.Requested.Scope, r.Subject.Scope) {
		return nil
	}
	return &ExchangeError{Code: "invalid_scope", Reason: "scope_not_narrowed"}
}

// checkChildMintIdentity applies the caller-identity rule of a
// child-minting exchange: the actor's sub must equal the caller's, and a
// subject whose sub differs from the actor's must be narrowed strictly,
// so a holder of someone else's token can neither borrow their actor
// token nor mint a child of their token at its full reach. A child minted
// for the actor's own principal is unaffected.
//
// spec: §13.3 (Credential Flow) "Client authentication on
// /v1/oauth/token."
func checkChildMintIdentity(r Request) error {
	if r.Actor.Subject != r.Caller.Subject {
		return &ExchangeError{Code: "invalid_grant", Reason: "actor_token_not_caller"}
	}
	if r.Subject.Subject == r.Actor.Subject {
		return nil
	}
	if strictlyNarrower(r.Requested.Scope, r.Subject.Scope) {
		return nil
	}
	return &ExchangeError{Code: "invalid_scope", Reason: "scope_not_narrowed"}
}

// refusePlaygroundSources refuses a playground-origin subject or actor
// token, so no token derived from a playground bearer outlives the
// playground's revocation and session caps.
//
// spec: §13.3 (Credential Flow) "Client authentication on
// /v1/oauth/token."
func refusePlaygroundSources(subject Token, actor *Token) error {
	if subject.Origin == OriginPlayground {
		return &ExchangeError{Code: "invalid_grant", Reason: "subject_token_playground"}
	}
	if actor != nil && actor.Origin == OriginPlayground {
		return &ExchangeError{Code: "invalid_grant", Reason: "actor_token_playground"}
	}
	return nil
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

// grantedEntries returns the allowlist entries the issued scope grants,
// preserving their order. An empty issued scope grants every entry, so a
// child whose issued scope is absent keeps the whole allowlist; an issued
// scope that does not parse grants none.
//
// spec: §13.3 (Credential Flow) "Scope narrowing enforcement."
func grantedEntries(allowlist, issuedScope []string) []string {
	set, err := scopes.Parse(strings.Join(issuedScope, " "))
	if err != nil {
		return nil
	}
	var out []string
	for _, tool := range allowlist {
		if set.Matches(tool) {
			out = append(out, tool)
		}
	}
	return out
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
