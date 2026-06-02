// SPDX-License-Identifier: MIT

package envblock

import "testing"

// spec: §14 line 105 — the env-var blocklist matches exact names and `*`
// globs, case-sensitive; the platform default is always merged in first
// so an operator can extend but not reduce it. F-14.1.12.

func TestMatch_spec_14_exactAndGlob(t *testing.T) {
	m := New(nil)
	cases := []struct {
		key     string
		blocked bool
	}{
		// Exact platform-default entries.
		{"AWS_SECRET_ACCESS_KEY", true},
		{"ANTHROPIC_API_KEY", true}, // also matches the *_KEY glob
		// `*_KEY` glob (suffix).
		{"MY_API_KEY", true},
		{"SERVICE_KEY", true},
		// `*_SECRET_*` glob (middle).
		{"DB_SECRET_VALUE", true},
		// `*_PASSWORD` glob (suffix).
		{"DB_PASSWORD", true},
		// Allowed names.
		{"NODE_ENV", false},
		{"LOG_LEVEL", false},
		{"KEYBOARD", false},      // does not end in _KEY
		{"SECRET_PREFIX", false}, // does not match *_SECRET / *_SECRET_* (no leading _SECRET_)
	}
	for _, c := range cases {
		pattern, blocked := m.Match(c.key)
		if blocked != c.blocked {
			t.Errorf("Match(%q): got blocked=%v (pattern %q), want %v", c.key, blocked, pattern, c.blocked)
		}
		if blocked && pattern == "" {
			t.Errorf("Match(%q): blocked but empty pattern", c.key)
		}
	}
}

func TestMatch_spec_14_caseSensitive(t *testing.T) {
	m := New(nil)
	// Lowercase variant of an exact entry must NOT be blocked (the spec
	// declares matching case-sensitive).
	if _, blocked := m.Match("aws_secret_access_key"); blocked {
		t.Errorf("lowercase aws_secret_access_key must not be blocked (case-sensitive)")
	}
}

func TestMatch_spec_14_deployerExtensionCannotReduce(t *testing.T) {
	// A deployer extension adds patterns; the platform default still
	// applies (extend, not reduce). F-14.1.12.
	m := New([]string{"INTERNAL_*"})
	if pattern, blocked := m.Match("INTERNAL_TOKEN_X"); !blocked {
		t.Errorf("deployer pattern INTERNAL_* must block INTERNAL_TOKEN_X")
	} else if pattern != "INTERNAL_*" && pattern != "*_TOKEN" {
		// Either the deployer glob or a default glob may fire first; both
		// are blocks. The default coming first means *_TOKEN wins here.
		t.Logf("blocked by pattern %q", pattern)
	}
	// A platform default still blocks even with extensions present.
	if _, blocked := m.Match("AWS_SECRET_ACCESS_KEY"); !blocked {
		t.Errorf("platform default must survive deployer extension")
	}
}

func TestGlobMatch_spec_14_anchoring(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "ANYTHING", true},
		{"*_KEY", "X_KEY", true},
		{"*_KEY", "X_KEYS", false},
		{"PRE_*", "PRE_X", true},
		{"PRE_*", "XPRE_X", false},
		{"*_SECRET_*", "A_SECRET_B", true},
		{"*_SECRET_*", "A_SECRET", false},
		{"EXACT", "EXACT", true},
		{"EXACT", "EXACTLY", false},
		// Overlapping prefix/suffix shorter than the key.
		{"AB*BA", "ABBA", true},
		{"AB*BA", "ABA", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}
