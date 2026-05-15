// SPDX-License-Identifier: MIT

package main

import "testing"

// TestHasNotImplementedSkipAfter pins the §17.9 skip-prefix
// allowlist. The validate-diagnosis subcommand treats a test as
// exempt from the // spec: / // diagnosis: annotation requirement
// when its body opens with one of these recognized skip patterns.
func TestHasNotImplementedSkipAfter(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"not-implemented exact", `t.Skip("not implemented: §11.7")`, true},
		{"not-implemented Skipf", `t.Skipf("not implemented: §11.7 reason: %v", err)`, true},
		{"phase-gated", `t.Skip("phase-gated: §13.4 ships in phase 13.4")`, true},
		{"not yet applicable (space)", `t.Skip("not yet applicable: phase 5")`, true},
		{"not-yet-applicable (hyphen)", `t.Skip("not-yet-applicable: phase 5")`, true},
		{"flaky-time", `t.Skip("flaky-time: see issue 123")`, true},
		{"flaky-network", `t.Skip("flaky-network: see issue 124")`, true},
		{"flaky-ordering", `t.Skip("flaky-ordering: see issue 125")`, true},
		{"quarantined", `t.Skip("quarantined: see issue 200")`, true},
		{"SkipUnless helper", `kind.SkipUnlessAvailable(t)`, true},
		{"SkipUnlessAuthorized helper", `cloud.SkipUnlessAuthorized(t)`, true},
		{"bare Skip without recognized prefix", `t.Skip("just because")`, false},
		{"Skipf without recognized prefix", `t.Skipf("reason %s", x)`, false},
		{"no skip at all", `if x { t.Fatal(\"...\") }`, false},
		{"comment containing 'Skip'", `// note: SkipUnless is used`, false},
	}
	for _, c := range cases {
		lines := []string{"func TestX(t *testing.T) {", c.body, "}"}
		got := hasNotImplementedSkipAfter(lines, 0)
		if got != c.want {
			t.Errorf("%s: got %v; want %v\nbody: %s", c.name, got, c.want, c.body)
		}
	}
}
