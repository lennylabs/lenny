// SPDX-License-Identifier: MIT

package translator

import "testing"

// TestSplitEnvModel_spec_10_6_557 exercises the §10.6 scoped
// model-namespace parser.
func TestSplitEnvModel_spec_10_6_557(t *testing.T) {
	cases := []struct {
		model   string
		wantEnv string
		wantMod string
	}{
		{"environments/security-team/claude-code", "security-team", "claude-code"},
		{"echo", "", "echo"},                    // plain model, no namespace
		{"", "", ""},                            // empty
		{"environments/", "", "environments/"},  // no name, no model: literal
		{"environments/sec", "", "environments/sec"}, // name only, no slash: literal
		{"environments/sec/", "", "environments/sec/"}, // trailing slash, empty model: literal
		{"environments//echo", "", "environments//echo"}, // empty name: literal
		{"environments/sec/claude/code", "sec", "claude/code"}, // model may contain slashes
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			env, mod := splitEnvModel(tc.model)
			if env != tc.wantEnv || mod != tc.wantMod {
				t.Fatalf("splitEnvModel(%q) = (%q, %q), want (%q, %q)",
					tc.model, env, mod, tc.wantEnv, tc.wantMod)
			}
		})
	}
}
