// SPDX-License-Identifier: MIT

// Package scopes parses and matches the §25.1 RFC 9068 OAuth 2.0
// `scope` JWT claim. Scopes use the closed taxonomy documented in
// §15.1 ("Scope taxonomy") and §25.1 ("Scope value syntax"):
// each scope follows `tools:<domain>:<action>`, multiple scopes are
// space-separated, and `*` is permitted in the `<action>` position.
//
// Consumers:
//
//   - The admin-API middleware (§25.1 enforcement point 1) reads the
//     parsed Set from the validated JWT and compares each handler's
//     declared x-lenny-scope against it.
//   - The MCP Management Server (§25.1 enforcement point 2,
//     pkg/ops/mcpmgmt) compares each tool's x-lenny-scope before
//     dispatching tools/call.
//   - `/v1/admin/me/authorized-tools` (§25.1 enforcement point 3) uses
//     Set to pre-filter the tool inventory.
//
// The package is a leaf utility: it has no imports outside the
// standard library so both the gateway and lenny-ops can depend on it
// without pulling in deployment-specific code.
package scopes

import (
	"errors"
	"strings"
)

// ClaimName is the JWT claim name §25.1 / RFC 9068 reserves for the
// scope claim. Token verifiers populate Set from this claim.
const ClaimName = "scope"

// Wildcard is the §25.1 token that matches every action within a
// domain. `tools:pool:*` matches `tools:pool:read`,
// `tools:pool:write`, and `tools:pool:scale`. `tools:*` is the most
// permissive scope (equivalent to omitting the claim).
const Wildcard = "*"

// scopePrefix is the closed first segment §25.1 requires of every
// scope value. The taxonomy reserves "tools" for tool invocation
// scopes; values that omit it are rejected.
const scopePrefix = "tools"

// Domains is the closed §15.1 / §25.1 domain enumeration. New
// domains must land here before being used in handler annotations;
// the §15.1 CI contract asserts every declared x-lenny-scope's
// domain is in this list. The set is exported so the OpenAPI
// generator and unit tests can re-use it.
var Domains = map[string]struct{}{
	"pool":             {},
	"health":           {},
	"diagnostics":      {},
	"recommendations":  {},
	"runbooks":         {},
	"events":           {},
	"audit":            {},
	"drift":            {},
	"backup":           {},
	"restore":          {},
	"upgrade":          {},
	"locks":            {},
	"escalation":       {},
	"logs":             {},
	"me":               {},
	"operations":       {},
	"tenant":           {},
	"credential_pool":  {},
	"credential":       {},
	"runtime":          {},
	"quota":            {},
	"config":           {},
	"circuit_breaker":  {},
	"sessions":         {},
	"runtimes":         {},
	"pools":            {},
	"experiment":       {},
}

// ErrInvalidScope is returned by ParseScope when a single value does
// not satisfy the §25.1 `tools:<domain>:<action>` form.
var ErrInvalidScope = errors.New("scopes: invalid scope value")

// Scope is one parsed §25.1 scope value. Domain is the second segment
// of `tools:<domain>:<action>`; Action is the third segment, which is
// either a specific tool action (`read`, `write`, `scale`) or the
// `*` wildcard.
type Scope struct {
	Domain string
	Action string
}

// String renders the scope back in canonical `tools:<domain>:<action>`
// form so a Scope value round-trips through the claim text.
func (s Scope) String() string {
	return scopePrefix + ":" + s.Domain + ":" + s.Action
}

// ParseScope parses one scope value into a Scope. The value must have
// three colon-separated segments, the first segment must be the
// reserved `tools` prefix, the domain must be in the §15.1 taxonomy,
// and the action must be a non-empty token. Whitespace surrounding the
// value is rejected — the §25.1 claim is space-delimited so a value
// carrying leading or trailing whitespace is malformed.
func ParseScope(v string) (Scope, error) {
	// spec:§25.1: the claim is a space-separated string; a value with
	// surrounding whitespace did not come from a well-formed claim.
	if v == "" || v != strings.TrimSpace(v) {
		return Scope{}, ErrInvalidScope
	}
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return Scope{}, ErrInvalidScope
	}
	if parts[0] != scopePrefix {
		return Scope{}, ErrInvalidScope
	}
	domain, action := parts[1], parts[2]
	if domain == "" || action == "" {
		return Scope{}, ErrInvalidScope
	}
	// `tools:*` is the §25.1 most-permissive form. It is the only
	// scope whose domain may be the wildcard token; every other
	// domain must be in the §15.1 taxonomy.
	if domain == Wildcard {
		if action != Wildcard {
			return Scope{}, ErrInvalidScope
		}
		return Scope{Domain: Wildcard, Action: Wildcard}, nil
	}
	if _, ok := Domains[domain]; !ok {
		return Scope{}, ErrInvalidScope
	}
	return Scope{Domain: domain, Action: action}, nil
}

// Set is the parsed §25.1 scope claim — a normalized collection of
// Scope values plus the original raw text. The raw text is preserved
// so the §25.12 SCOPE_FORBIDDEN response can echo the caller's
// activeScope verbatim.
type Set struct {
	// Raw is the space-separated claim text as the token carried it.
	// An empty Raw means no scope claim was present (§25.1 "absent
	// claim — no scope restriction").
	Raw string
	// scopes is the parsed scope list. Order is preserved from the
	// claim text so iteration is deterministic.
	scopes []Scope
}

// Parse parses the §25.1 RFC 9068 `scope` claim text. Per RFC 9068
// the claim is a single space-separated string. Empty or
// whitespace-only input yields a Set with no scopes (absent claim);
// any malformed value returns ErrInvalidScope. Duplicate values are
// silently de-duplicated.
//
// The empty Set has Present() == false and Matches() returns true for
// every required scope — the §25.1 absent-claim semantics.
func Parse(claim string) (Set, error) {
	trimmed := strings.TrimSpace(claim)
	set := Set{Raw: trimmed}
	if trimmed == "" {
		return set, nil
	}
	// strings.Fields splits on any run of Unicode whitespace, which
	// covers the §25.1 space-separator plus the tabs/newlines an OIDC
	// provider might emit in a multi-line claim.
	seen := make(map[string]bool)
	for _, raw := range strings.Fields(trimmed) {
		// spec:§25.1: each value must satisfy the canonical form. A
		// single malformed token rejects the whole claim so the
		// caller sees a deterministic parse error instead of silently
		// dropping values.
		s, err := ParseScope(raw)
		if err != nil {
			return Set{}, err
		}
		key := s.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		set.scopes = append(set.scopes, s)
	}
	return set, nil
}

// Present reports whether the §25.1 scope claim was present in the
// source token. An absent claim does not restrict the caller below
// the role ceiling, so callers branch on Present before they treat a
// non-match as a forbidden request.
func (s Set) Present() bool { return s.Raw != "" }

// Scopes returns a defensive copy of the parsed scope list so callers
// can iterate without mutating the Set.
func (s Set) Scopes() []Scope {
	if len(s.scopes) == 0 {
		return nil
	}
	return append([]Scope(nil), s.scopes...)
}

// Matches reports whether the Set permits the given required scope
// per the §25.1 matching rules:
//
//   - An absent claim (Present()==false) matches every required scope.
//     The §25.1 absent-claim semantics defer to the role ceiling.
//   - `tools:*` matches every required scope.
//   - A granted `tools:<domain>:*` matches every required scope in
//     the same domain.
//   - Otherwise the granted scope must equal the required scope
//     exactly (same domain, same action).
//
// A malformed `required` value returns false; callers MUST hand in a
// canonical scope value (typically the handler's x-lenny-scope literal).
func (s Set) Matches(required string) bool {
	if !s.Present() {
		return true
	}
	req, err := ParseScope(required)
	if err != nil {
		return false
	}
	for _, g := range s.scopes {
		if g.Domain == Wildcard {
			// `tools:*` short-circuits every domain.
			return true
		}
		if g.Domain != req.Domain {
			continue
		}
		if g.Action == Wildcard || g.Action == req.Action {
			return true
		}
	}
	return false
}

// Intersect returns the §25.1 monotonic narrowing of self by other:
// the largest Set whose values are matched by both. The result's Raw
// is the canonical-form concatenation of the surviving scopes so the
// intersection round-trips through Parse.
//
// Intersection is used by the §10.2 playground mint path
// ("intersection(subject_token.scope, playground_allowed_scope) — never
// the union"). When either side is empty (absent claim) the
// intersection is the other side, which mirrors the absent-claim
// "no restriction" semantics.
func (s Set) Intersect(other Set) Set {
	switch {
	case !s.Present():
		return other
	case !other.Present():
		return s
	}
	var out []Scope
	seen := make(map[string]bool)
	for _, g := range s.scopes {
		// Use the canonical form of g as the §25.1 required-scope
		// probe — Matches honors the wildcard expansion in both
		// directions, so a narrower g that lands inside a broader
		// other survives.
		if other.Matches(g.String()) && !seen[g.String()] {
			out = append(out, g)
			seen[g.String()] = true
		}
	}
	// Conversely, a broader g in self may not Match a narrower other,
	// but the narrower scope on the other side should still survive
	// because it lies inside g.
	for _, g := range other.scopes {
		if s.Matches(g.String()) && !seen[g.String()] {
			out = append(out, g)
			seen[g.String()] = true
		}
	}
	if len(out) == 0 {
		// Both sides were present but the intersection is empty. We
		// signal an explicit "no scopes permitted" rather than an
		// absent claim by carrying a sentinel Raw — Present() returns
		// true and Matches() returns false for every required value.
		return Set{Raw: emptyIntersectionSentinel}
	}
	raw := make([]string, 0, len(out))
	for _, g := range out {
		raw = append(raw, g.String())
	}
	return Set{Raw: strings.Join(raw, " "), scopes: out}
}

// emptyIntersectionSentinel marks a Set returned by Intersect when
// the two operands had no overlap. The value is unparseable, which
// guarantees Matches returns false for every required scope without
// re-introducing the absent-claim semantics.
const emptyIntersectionSentinel = "tools:__none__"
