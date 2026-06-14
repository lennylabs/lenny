// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// TestTreeVisibilityIsValid_spec_8_5_540 pins the closed §8.5 enum.
func TestTreeVisibilityIsValid_spec_8_5_540(t *testing.T) {
	valid := []session.TreeVisibility{
		session.VisibilityFull, session.VisibilityParentAndSelf, session.VisibilitySelfOnly,
	}
	for _, v := range valid {
		if !v.IsValid() {
			t.Errorf("%q should be valid (§8.5 line 540)", v)
		}
	}
	for _, v := range []session.TreeVisibility{"", "FULL", "tree", "parent"} {
		if v.IsValid() {
			t.Errorf("%q should be invalid", v)
		}
	}
}

// TestTreeVisibilityOrDefault_spec_8_5_540 verifies the §8.5 default of
// `full` applies for an empty or unrecognised value (fail-open, broadest).
func TestTreeVisibilityOrDefault_spec_8_5_540(t *testing.T) {
	cases := map[session.TreeVisibility]session.TreeVisibility{
		"":                              session.VisibilityFull,
		"garbage":                       session.VisibilityFull,
		session.VisibilityFull:          session.VisibilityFull,
		session.VisibilityParentAndSelf: session.VisibilityParentAndSelf,
		session.VisibilitySelfOnly:      session.VisibilitySelfOnly,
	}
	for in, want := range cases {
		if got := in.OrDefault(); got != want {
			t.Errorf("OrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTreeVisibilityAtLeastAsNarrow_spec_8_3_313 verifies the strict
// monotonicity ordering full → parent-and-self → self-only: a child may
// equal or narrow the parent but never widen it.
func TestTreeVisibilityAtLeastAsNarrow_spec_8_3_313(t *testing.T) {
	f, p, s := session.VisibilityFull, session.VisibilityParentAndSelf, session.VisibilitySelfOnly
	// (child, parent, allowed)
	cases := []struct {
		child, parent session.TreeVisibility
		ok            bool
	}{
		{f, f, true}, {p, f, true}, {s, f, true}, // full parent: any child narrows or equals
		{f, p, false}, {p, p, true}, {s, p, true}, // parent-and-self: full child widens (reject)
		{f, s, false}, {p, s, false}, {s, s, true}, // self-only: only self-only child
		// empty parent resolves to full, so any child is allowed.
		{s, "", true}, {f, "", true},
	}
	for _, c := range cases {
		if got := c.child.AtLeastAsNarrow(c.parent); got != c.ok {
			t.Errorf("AtLeastAsNarrow(child=%q, parent=%q) = %v, want %v", c.child, c.parent, got, c.ok)
		}
	}
}

// TestMessagingScopeOrDefault_spec_7_2_240 verifies the §7.2 default of
// `direct` for any unset or unrecognised messaging scope.
func TestMessagingScopeOrDefault_spec_7_2_240(t *testing.T) {
	cases := map[session.MessagingScope]session.MessagingScope{
		"":                             session.MessagingScopeDirect,
		"garbage":                      session.MessagingScopeDirect,
		session.MessagingScopeDirect:   session.MessagingScopeDirect,
		session.MessagingScopeSiblings: session.MessagingScopeSiblings,
	}
	for in, want := range cases {
		if got := in.OrDefault(); got != want {
			t.Errorf("OrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}
