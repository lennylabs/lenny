// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
)

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

// TestScaffoldMarkerReaderAcceptsEveryPublishedSkipCategory holds the
// scaffold-marker reader to the category enumeration in
// cmd/lenny-test/skipreason, which the static-tier skip-reason classifier
// reads as well. A category widened or renamed in one reader alone
// would let the classifier and this reader disagree about which skip
// reasons §17.9 allows.
func TestScaffoldMarkerReaderAcceptsEveryPublishedSkipCategory(t *testing.T) {
	for _, category := range skipreason.Categories {
		for _, body := range []string{
			`t.Skip("` + category + ` the reason continues here")`,
			`t.Skipf("` + category + ` %v", err)`,
		} {
			lines := []string{"func TestX(t *testing.T) {", body, "}"}
			if !hasNotImplementedSkipAfter(lines, 0) {
				t.Errorf("the reader rejects the published category %q\nbody: %s", category, body)
			}
		}
	}
	// A reason opening with no published category is not a scaffold
	// marker, so the reader does not exempt it from the annotations.
	lines := []string{"func TestX(t *testing.T) {", `t.Skip("docker is not running")`, "}"}
	if hasNotImplementedSkipAfter(lines, 0) {
		t.Errorf("the reader accepts a reason that opens with no published category")
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

// TestTrackedComponentAndAboveTestsAreAnnotated drives the annotation
// check of validate and the diagnosis scan of validate-diagnosis over the
// tracked tree, so a test added without its annotations surfaces from a
// package-scoped unit run rather than only from a composed static-tier
// run. It tests contributor tooling, so it verifies no requirement.
func TestTrackedComponentAndAboveTestsAreAnnotated(t *testing.T) {
	root := repoRoot()
	expectPass(t, validateTierTestsAnnotated(root))

	var missing []string
	for _, dir := range componentAndAboveTierDirs() {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return err
			}
			_, miss := scanDiagnosis(path)
			missing = append(missing, miss...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d test function(s) carry no spec or diagnosis annotation: %s",
			len(missing), strings.Join(missing, "; "))
	}
}

// TestValidateTierTestsAnnotatedFixtures pins the annotation check that
// replaced the spec-map check of change 0116 (CS-9): a test in a tier
// directory from the component tier on passes with a `// spec:`
// annotation or a doc comment saying it verifies no requirement, even
// wrapped across lines, and fails otherwise, whatever skip its body
// takes. TestMain, helpers, files outside the tier directories, and
// testdata/ are outside the check. It tests contributor tooling, so it
// verifies no requirement.
func TestValidateTierTestsAnnotatedFixtures(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tests/integration/a_test.go", `package integration

// spec: §11.6 (Circuit Breakers)
// diagnosis: the breaker did not open.
func TestAnnotated(t *testing.T) {}

// TestHarness checks the harness itself. It verifies no
// requirement, because it tests contributor tooling.
func TestHarness(t *testing.T) {}

func TestMain(m *testing.M) {}

func helper() {}

// TestSkipsButCarriesNothing documents nothing.
func TestSkipsButCarriesNothing(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
}
`)
	write("tests/component/b/b_test.go", `package b

func TestBare(t *testing.T) {}
`)
	write("tests/component/b/testdata/c_test.go", "package c\n\nfunc TestFixture(t *testing.T) {}\n")
	write("pkg/x/x_test.go", "package x\n\nfunc TestUnit(t *testing.T) {}\n")

	r := validateTierTestsAnnotated(root)
	if r.ok {
		t.Fatalf("the check passed with unannotated tests: %s", r.detail)
	}
	for _, want := range []string{"tests/integration/a_test.go:16 TestSkipsButCarriesNothing", "tests/component/b/b_test.go:3 TestBare", "2 test function(s)"} {
		if !strings.Contains(r.detail, want) {
			t.Errorf("detail %q does not name %q", r.detail, want)
		}
	}
	for _, not := range []string{"TestAnnotated", "TestHarness", "TestMain", "TestFixture", "TestUnit"} {
		if strings.Contains(r.detail, not) {
			t.Errorf("detail %q names %s, which the check accepts or leaves out", r.detail, not)
		}
	}

	write("tests/integration/a_test.go", "package integration\n\n// spec: §11.6 (Circuit Breakers)\nfunc TestAnnotated(t *testing.T) {}\n")
	write("tests/component/b/b_test.go", "package b\n\n// TestBare verifies no requirement.\nfunc TestBare(t *testing.T) {}\n")
	if r := validateTierTestsAnnotated(root); !r.ok || !strings.Contains(r.detail, "2 test function(s)") {
		t.Errorf("the check over annotated tests = %+v", r)
	}

	if err := os.Chmod(filepath.Join(root, "tests/integration/a_test.go"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if r := validateTierTestsAnnotated(root); r.ok || !strings.Contains(r.detail, "read ") {
			t.Errorf("an unreadable test file passed: %+v", r)
		}
	}
}

// TestRuntimeSupervisorSourcesSelectTheHarnessAndTheImageTiers pins the
// selection of the runtime supervisor's C sources and image, which no Go
// package owns, so tests/non-go-inputs.yaml routes them: an edit under
// supervisor/ selects the unit-tier C harness driver, the supervise
// contract, and the tiers that deploy the image, and an edit to
// Dockerfile.supervisor selects the tiers that deploy the image. The
// static-tier binary check runs on every invocation. Without the rows a C
// change would select the static tier alone under --changed.
//
// spec: 4.7.11 (Adapter-Agent Security Boundary), 4.7.10 (Deployment Model)
func TestRuntimeSupervisorSourcesSelectTheHarnessAndTheImageTiers(t *testing.T) {
	g, err := loadPkgGraph(context.Background(), repoRoot())
	if err != nil {
		t.Fatalf("load the package graph: %v", err)
	}
	inputs, err := readNonGoInputs(repoRoot())
	if err != nil {
		t.Fatalf("read the non-Go input list: %v", err)
	}
	opts := selectOpts{rdepsDepth: defaultRdepsDepth}
	c := planOf(selectChanged([]string{"supervisor/supervisor.c"}, g, inputs, specEdits{}, opts))
	if !contains(c[tierUnit].pkgs, "./tests/testinfra/supervisorbuild") {
		t.Errorf("an edit under supervisor/ selects unit %v, without the C harness driver", c[tierUnit].pkgs)
	}
	if !contains(c[tierContract].pkgs, "./tests/contract/adapter_supervise") {
		t.Errorf("an edit under supervisor/ selects contract %v, without the supervise contract", c[tierContract].pkgs)
	}
	for _, path := range []string{"supervisor/supervisor.c", "Dockerfile.supervisor"} {
		if _, ok := planOf(selectChanged([]string{path}, g, inputs, specEdits{}, opts))[tierE2EKind]; !ok {
			t.Errorf("an edit to %s does not select the e2e_kind tier, which deploys the supervisor image", path)
		}
	}
}
