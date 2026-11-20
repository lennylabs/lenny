// SPDX-License-Identifier: MIT

package playground

import (
	"errors"
	"strings"
	"testing"
)

// allowedCeiling is playgroundAllowedScope as the space-separated claim
// intersectScope returns when the subject does not narrow it.
var allowedCeiling = strings.Join(playgroundAllowedScope, " ")

// TestIntersectScopeKeepsTheOverlapWithTheAllowedSet checks that the
// narrowed scope is the intersection rather than the union: a subject value
// inside the ceiling survives, including a narrower value under a wildcard
// of the ceiling, and a value outside it is dropped.
//
// spec: §10.2 (Authentication), §25.1 (Design Philosophy and Agent Model)
func TestIntersectScopeKeepsTheOverlapWithTheAllowedSet(t *testing.T) {
	cases := []struct {
		subject string
		want    string
	}{
		{"tools:sessions:read tools:credential:write", "tools:sessions:read"},
		{"tools:sessions:write", "tools:sessions:write"},
		{"tools:health:read tools:pools:read", "tools:pools:read"},
	}
	for _, tc := range cases {
		got, err := intersectScope(tc.subject, playgroundAllowedScope)
		if err != nil || got != tc.want {
			t.Errorf("intersectScope(%q) = (%q, %v), want (%q, nil)", tc.subject, got, err, tc.want)
		}
	}
}

// TestIntersectScopeOfTheAllowedSetIsTheAllowedSet checks the boundary
// subjects that the ceiling does not narrow: a subject whose scope is
// exactly the allowed set, the same set in another order, and tools:*. Each
// yields the allowed set.
//
// spec: §10.2 (Authentication), §25.1 (Design Philosophy and Agent Model)
func TestIntersectScopeOfTheAllowedSetIsTheAllowedSet(t *testing.T) {
	reversed := make([]string, len(playgroundAllowedScope))
	for i, s := range playgroundAllowedScope {
		reversed[len(reversed)-1-i] = s
	}
	for _, subject := range []string{allowedCeiling, strings.Join(reversed, " "), "tools:*"} {
		got, err := intersectScope(subject, playgroundAllowedScope)
		if err != nil {
			t.Fatalf("intersectScope(%q) error = %v, want nil", subject, err)
		}
		if !sameScopeValues(got, allowedCeiling) {
			t.Errorf("intersectScope(%q) = %q, want the allowed set %q", subject, got, allowedCeiling)
		}
	}
}

// TestIntersectScopeOfAnAbsentClaimIsTheAllowedSet checks that a subject
// with no scope claim, which carries no scope restriction, receives the
// allowed set rather than an empty claim.
//
// spec: §10.2 (Authentication), §25.1 (Design Philosophy and Agent Model)
func TestIntersectScopeOfAnAbsentClaimIsTheAllowedSet(t *testing.T) {
	for _, subject := range []string{"", "   "} {
		got, err := intersectScope(subject, playgroundAllowedScope)
		if err != nil || got != allowedCeiling {
			t.Errorf("intersectScope(%q) = (%q, %v), want (%q, nil)", subject, got, err, allowedCeiling)
		}
	}
}

// TestIntersectScopeRefusesAnUnparseableSubjectScope checks that a subject
// scope the canonical parser refuses, including a raw OAuth value from an
// identity provider and one bad value among good ones, yields
// errSubjectScopeInvalid and no claim.
//
// spec: §10.2 (Authentication), §25.1 (Design Philosophy and Agent Model)
func TestIntersectScopeRefusesAnUnparseableSubjectScope(t *testing.T) {
	for _, subject := range []string{
		"openid profile",
		"bogusdomain:read",
		"tools:sessions:read:extra",
		"tools:sessions:read openid",
	} {
		got, err := intersectScope(subject, playgroundAllowedScope)
		if !errors.Is(err, errSubjectScopeInvalid) || got != "" {
			t.Errorf("intersectScope(%q) = (%q, %v), want (\"\", errSubjectScopeInvalid)", subject, got, err)
		}
	}
}

// TestIntersectScopeRefusesADisjointSubjectScope checks that a subject scope
// that parses and shares no value with the allowed set yields
// errScopeIntersectionEmpty and no claim, so the mint never writes the empty
// claim that every scope gate reads as unrestricted.
//
// spec: §10.2 (Authentication), §25.1 (Design Philosophy and Agent Model)
func TestIntersectScopeRefusesADisjointSubjectScope(t *testing.T) {
	for _, subject := range []string{"tools:credential:write", "tools:health:read", "tools:pools:write tools:credential:read"} {
		got, err := intersectScope(subject, playgroundAllowedScope)
		if !errors.Is(err, errScopeIntersectionEmpty) || got != "" {
			t.Errorf("intersectScope(%q) = (%q, %v), want (\"\", errScopeIntersectionEmpty)", subject, got, err)
		}
	}
}

// sameScopeValues reports whether two space-separated scope claims hold the
// same values in any order.
func sameScopeValues(a, b string) bool {
	as, bs := strings.Fields(a), strings.Fields(b)
	if len(as) != len(bs) {
		return false
	}
	seen := make(map[string]bool, len(as))
	for _, s := range as {
		seen[s] = true
	}
	for _, s := range bs {
		if !seen[s] {
			return false
		}
	}
	return true
}
