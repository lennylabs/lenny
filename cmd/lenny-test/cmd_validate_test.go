// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// specMapTestFilesFixture builds a temp repo root with the given test
// files present on disk, a spec-map.json whose sections list the given
// test references, an exceptions file exempting exceptSection, and a
// pending file exempting pendingPath. It returns the check result.
func specMapTestFilesFixture(t *testing.T, sections map[string][]string, present []string, exceptSection, pendingPath string) checkResult {
	t.Helper()
	root := t.TempDir()
	for _, p := range present {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte("package p\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	doc := map[string]any{"version": 1, "sections": map[string]any{}}
	secs := doc["sections"].(map[string]any)
	for name, tests := range sections {
		secs[name] = map[string]any{"tests": tests}
	}
	specMapPath := filepath.Join(root, "spec-map.json")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal spec-map: %v", err)
	}
	if err := os.WriteFile(specMapPath, raw, 0o644); err != nil {
		t.Fatalf("write spec-map: %v", err)
	}

	exceptionsPath := filepath.Join(root, "spec-map-exceptions.yaml")
	body := "version: 1\nexceptions:\n"
	if exceptSection != "" {
		body += "  - section: \"" + exceptSection + "\"\n    reason: deferred\n    justification: test fixture.\n"
	}
	if err := os.WriteFile(exceptionsPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write exceptions: %v", err)
	}
	pendingFile := filepath.Join(root, "spec-map-pending.txt")
	if err := os.WriteFile(pendingFile, []byte(pendingPath+"\n"), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	return validateSpecMapTestFiles(specMapPath, exceptionsPath, pendingFile, root)
}

// TestValidateSpecMapTestFiles pins the spec-map test-file existence
// gate: a concrete `.go` reference under a non-exempt section must
// resolve on disk, a `::TestName` selector is stripped before the file
// is probed, a directory or package reference is not required to be a
// file, and the pending-path channel suppresses a missing-file report.
//
// spec: TESTING.md §5 ("tests/spec-map.json maps every spec section to
// the tests ... that encode it"; tests/spec-map-exceptions.yaml records
// sections "explicitly exempt from the 'every section has at least one
// test' rule"). The exceptions file waives only the has-a-test
// (coverage-completeness) requirement; it does not waive the
// existence check for a `tests[]` reference the map does carry. A
// listed-but-excepted section's dangling reference must still be
// flagged.
func TestValidateSpecMapTestFiles(t *testing.T) {
	// All references resolve: pass.
	r := specMapTestFilesFixture(t,
		map[string][]string{"1.1": {"pkg/foo/foo_test.go", "pkg/foo/foo_test.go::TestBar"}},
		[]string{"pkg/foo/foo_test.go"}, "", "")
	expectPass(t, r)

	// A missing file under a non-exempt section fails, and names the
	// dangling reference.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.2": {"pkg/foo/missing_test.go"}},
		nil, "", "")
	expectFail(t, r, "1.2", "pkg/foo/missing_test.go")

	// A `::TestName` selector on a missing file is stripped before the
	// probe and still fails.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.3": {"pkg/foo/missing_test.go::TestX"}},
		nil, "", "")
	expectFail(t, r, "pkg/foo/missing_test.go")

	// A missing file whose section is listed in spec-map-exceptions.yaml
	// still fails: the exceptions file waives the has-a-test
	// (coverage-completeness) requirement, not the existence check on a
	// tests[] reference the section does carry.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.4": {"pkg/foo/missing_test.go"}},
		nil, "1.4", "")
	expectFail(t, r, "1.4", "pkg/foo/missing_test.go")

	// An excepted section that carries no tests[] entries at all still
	// passes the existence check trivially (there is nothing to probe);
	// the coverage-completeness waiver that lets such a section exist
	// with zero tests is validateSpecMapCoverage's concern, not this
	// gate's.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.7": {}},
		nil, "1.7", "")
	expectPass(t, r)

	// A missing file listed in the pending file passes.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.5": {"pkg/foo/missing_test.go"}},
		nil, "", "pkg/foo/missing_test.go")
	expectPass(t, r)

	// A non-.go directory reference is not required to resolve to a
	// file.
	r = specMapTestFilesFixture(t,
		map[string][]string{"1.6": {"tests/tier2_component/foo"}},
		nil, "", "")
	expectPass(t, r)
}

// specMapTestFuncsFixture builds a temp repo root with the given test
// files written verbatim (so callers control which func declarations
// each file carries), a spec-map.json whose sections list the given
// test references, and a pending file with the given lines. It returns
// the validateSpecMapTestFuncs result.
func specMapTestFuncsFixture(t *testing.T, sections map[string][]string, files map[string]string, pendingLines []string) checkResult {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	doc := map[string]any{"version": 1, "sections": map[string]any{}}
	secs := doc["sections"].(map[string]any)
	for name, tests := range sections {
		secs[name] = map[string]any{"tests": tests}
	}
	specMapPath := filepath.Join(root, "spec-map.json")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal spec-map: %v", err)
	}
	if err := os.WriteFile(specMapPath, raw, 0o644); err != nil {
		t.Fatalf("write spec-map: %v", err)
	}
	pendingFile := filepath.Join(root, "spec-map-pending.txt")
	body := ""
	for _, l := range pendingLines {
		body += l + "\n"
	}
	if err := os.WriteFile(pendingFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	return validateSpecMapTestFuncs(specMapPath, pendingFile, root)
}

// TestValidateSpecMapTestFuncs pins the spec-map test-function
// resolution gate: a `path::TestName` reference must name a top-level
// test function that exists in the referenced file. It is the guard
// that catches a rename which repoints or drops a mapped test function
// but leaves the file in place, so validateSpecMapTestFiles (which only
// stats the file) stays green while the map rots. The concrete drift
// that motivated the check renamed a tier-4 interactive-iteration test
// and left both section 7.1 and 7.2 pointing at the gone symbol.
//
// spec: TESTING.md §5 ("tests/spec-map.json maps every spec section to
// the tests ... that encode it"). A reference that names a nonexistent
// function misdirects a maintainer reading the map exactly as a
// reference to a nonexistent file does.
func TestValidateSpecMapTestFuncs(t *testing.T) {
	withFunc := "package p\n\nfunc TestInteractiveIterationInterruptThenResumeAndDeliver(t *testing.T) {}\n"

	// A reference that names a function present in the file passes.
	r := specMapTestFuncsFixture(t,
		map[string][]string{"7.1": {"tests/tier4/it_test.go::TestInteractiveIterationInterruptThenResumeAndDeliver"}},
		map[string]string{"tests/tier4/it_test.go": withFunc}, nil)
	expectPass(t, r)

	// The pre-fix reference names a function the file no longer declares
	// (renamed away): the gate fails and names the dangling reference.
	// This is the case that would have caught the tier-4
	// interactive-iteration test rename
	// (TestInteractiveIterationInterruptThenQueuedMessage ->
	// ...ThenResumeAndDeliver) that repointed sections 7.1 and 7.2 at a
	// gone symbol.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/it_test.go::TestInteractiveIterationInterruptThenQueuedMessage"}},
		map[string]string{"tests/tier4/it_test.go": withFunc}, nil)
	expectFail(t, r, "7.2", "tests/tier4/it_test.go::TestInteractiveIterationInterruptThenQueuedMessage")

	// A precise `path::TestName` pending waiver suppresses only that one
	// dangling reference.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/it_test.go::TestGone"}},
		map[string]string{"tests/tier4/it_test.go": withFunc},
		[]string{"tests/tier4/it_test.go::TestGone"})
	expectPass(t, r)

	// A whole-file pending waiver (path with the ::TestName stripped)
	// exempts every function in that file.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/it_test.go::TestGone"}},
		map[string]string{"tests/tier4/it_test.go": withFunc},
		[]string{"tests/tier4/it_test.go"})
	expectPass(t, r)

	// A missing file is validateSpecMapTestFiles' finding, not this
	// gate's; the func check does not double-report it.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/absent_test.go::TestGone"}},
		nil, nil)
	expectPass(t, r)

	// A function that exists only in a sibling file in the same
	// directory does not satisfy a reference that names this file: the
	// map points at a specific file and the pointer must be accurate.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/a_test.go::TestElsewhere"}},
		map[string]string{
			"tests/tier4/a_test.go": "package p\n",
			"tests/tier4/b_test.go": "package p\n\nfunc TestElsewhere(t *testing.T) {}\n",
		}, nil)
	expectFail(t, r, "tests/tier4/a_test.go::TestElsewhere")

	// A method with a matching name (`func (r T) TestX(`) is not a
	// top-level test function and does not satisfy the reference.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/m_test.go::TestMethod"}},
		map[string]string{"tests/tier4/m_test.go": "package p\n\nfunc (r rec) TestMethod() {}\n"}, nil)
	expectFail(t, r, "tests/tier4/m_test.go::TestMethod")

	// An entry without a `::TestName` selector is out of scope here.
	r = specMapTestFuncsFixture(t,
		map[string][]string{"7.2": {"tests/tier4/it_test.go"}},
		map[string]string{"tests/tier4/it_test.go": withFunc}, nil)
	expectPass(t, r)
}

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
