// SPDX-License-Identifier: MIT

package playground

import "testing"

// TestIntersectScopeNarrowsToCeiling pins the §10.2 playground scope
// intersection: the minted scope is intersection(subject_token.scope,
// playground_allowed_scope), wildcard-aware so a subject `tools:sessions:read`
// survives against the allowed `tools:sessions:*`, and an out-of-ceiling
// subject scope is dropped.
//
// spec: 10.2 (scope is intersection(subject_token.scope,
// playground_allowed_scope) — never the union), 25.1 (canonical scope form)
func TestIntersectScopeNarrowsToCeiling_spec_10_2(t *testing.T) {
	got := intersectScope("tools:sessions:read tools:credential:write", playgroundAllowedScope)
	if got != "tools:sessions:read" {
		t.Fatalf("intersectScope narrowed = %q, want tools:sessions:read", got)
	}
}

// TestIntersectScopeDisjointYieldsEmpty pins the §10.2 empty-intersection rule:
// a subject scope that holds only out-of-ceiling values yields the empty scope
// claim, so the mint proceeds with no scope restriction the §10.2 auth chain
// would otherwise reject.
//
// spec: 10.2 line 250 (if the intersection is empty, the mint proceeds with an
// empty scope claim)
func TestIntersectScopeDisjointYieldsEmpty_spec_10_2(t *testing.T) {
	if got := intersectScope("tools:credential:write", playgroundAllowedScope); got != "" {
		t.Fatalf("disjoint intersectScope = %q, want empty", got)
	}
}

// TestIntersectScopeMalformedSubjectFailsClosed pins the §10.2 fail-closed
// behavior: a subject scope the §25.1 parser rejects yields no playground scope
// rather than a claim the auth chain would reject downstream.
//
// spec: 10.2 (fail closed on a malformed subject scope), 25.1 (a value outside
// the canonical taxonomy is rejected)
func TestIntersectScopeMalformedSubjectFailsClosed_spec_10_2(t *testing.T) {
	// "bogusdomain" is not in the §25.1 domain taxonomy, so Parse rejects the
	// whole claim and intersectScope returns the empty scope.
	if got := intersectScope("bogusdomain:read", playgroundAllowedScope); got != "" {
		t.Fatalf("malformed subject intersectScope = %q, want empty", got)
	}
	// A token with too many colon-separated parts is also malformed.
	if got := intersectScope("tools:sessions:read:extra", playgroundAllowedScope); got != "" {
		t.Fatalf("over-segmented subject intersectScope = %q, want empty", got)
	}
}
