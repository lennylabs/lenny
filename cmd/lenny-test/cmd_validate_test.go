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
		{"blocked exact", `t.Skip("blocked: §12.8 needs a KMS adapter")`, true},
		{"blocked Skipf", `t.Skipf("blocked: §12.8 needs %s", missing)`, true},
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

// TestHasAnnotationBefore pins the §17.9 annotation-scan boundary: the
// validate-diagnosis subcommand scans the function's own contiguous leading
// doc-comment block (comment lines plus the blank lines that separate
// paragraphs within it) for the // spec: / // diagnosis: markers. A thorough
// multi-paragraph header keeps both annotations in range, while a blank-line
// gap before unrelated code ends the scan so an annotation on a different
// declaration is never mis-attributed.
func TestHasAnnotationBefore(t *testing.T) {
	cases := []struct {
		name   string
		lines  []string
		marker string
		want   bool
	}{
		{
			name: "spec and diagnosis in one long block both found",
			lines: []string{
				"// spec: 4.6.1 (occupancy projection), 4.6.3 (ownership),",
				"// 6.2 (coarse state machine), 3.3 (decomposition)",
				"//",
				"// diagnosis: a long multi-line diagnosis that, together with the",
				"// spec block above, spans more than ten lines before the func so a",
				"// fixed ten-line window would push the spec annotation out of range.",
				"// It keeps going to be sure the block is comfortably over ten lines.",
				"// And one more line for good measure.",
				"func TestX(t *testing.T) {",
			},
			marker: "// spec:",
			want:   true,
		},
		{
			name: "diagnosis closest to func still found",
			lines: []string{
				"// spec: 4.6.1",
				"//",
				"// diagnosis: explains the failure mode",
				"func TestX(t *testing.T) {",
			},
			marker: "// diagnosis:",
			want:   true,
		},
		{
			name: "annotation on a different declaration is not attributed",
			lines: []string{
				"// spec: 9.9 (some other test)",
				"func TestOther(t *testing.T) {}",
				"",
				"// diagnosis: this func has a diagnosis but no spec of its own",
				"func TestX(t *testing.T) {",
			},
			marker: "// spec:",
			want:   false,
		},
		{
			name: "no leading comment block at all",
			lines: []string{
				"x := 1",
				"func TestX(t *testing.T) {",
			},
			marker: "// spec:",
			want:   false,
		},
	}
	for _, c := range cases {
		idx := len(c.lines) - 1 // the func line
		got := hasAnnotationBefore(c.lines, idx, c.marker)
		if got != c.want {
			t.Errorf("%s: hasAnnotationBefore(%q) = %v; want %v", c.name, c.marker, got, c.want)
		}
	}
}
