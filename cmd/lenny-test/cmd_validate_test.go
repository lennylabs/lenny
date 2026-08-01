// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/changegraph"
	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
	"github.com/lennylabs/lenny/scripts/specshift/identifier"
	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
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

// TestScaffoldMarkerReaderAcceptsEveryPublishedSkipCategory holds the
// scaffold-marker reader to the category enumeration in
// cmd/lenny-test/skipreason, which the tier-0 skip-reason classifier
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

// writeChangeGraphSource writes a source file under the root, creating
// its parent directories. It leaves the file untracked; the caller
// decides whether git tracks it.
func writeChangeGraphSource(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// trackChangeGraphSources adds the given repo-relative paths to the
// root's git index, which is where the completeness check reads the
// tracked source domain from.
func trackChangeGraphSources(t *testing.T, root string, paths ...string) {
	t.Helper()
	if len(paths) == 0 {
		return
	}
	cmd := exec.Command("git", append([]string{"add", "--"}, paths...)...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %v: %v: %s", paths, err, out)
	}
}

// changeGraphCompletenessFillers is one tracked source path per tree in
// the check's source domain, with the glob key that covers it. The check
// requires every tree to contribute at least one tracked source path, so
// a fixture that means to exercise the coverage predicate rather than
// the per-tree guard carries these alongside its own sources.
var (
	changeGraphCompletenessFillers = []string{
		"cmd/filler/main.go", "pkg/filler/filler.go",
		"scripts/filler.sh", "tests/filler/filler.go",
	}
	changeGraphCompletenessFillerGlobs = []string{
		"cmd/filler", "pkg/filler", "scripts/filler.sh", "tests/filler",
	}
)

// changeGraphCompletenessRoot builds a bare root that additionally
// carries a tracked, glob-covered source path in every tree of the
// check's domain, so the per-tree selection guard is satisfied and the
// fixture's own sources are what the run turns on.
func changeGraphCompletenessRoot(t *testing.T, sources, globs, prefixes []string) string {
	t.Helper()
	return changeGraphCompletenessBareRoot(t,
		append(append([]string{}, sources...), changeGraphCompletenessFillers...),
		append(append([]string{}, globs...), changeGraphCompletenessFillerGlobs...),
		prefixes)
}

// changeGraphCompletenessBareRoot builds a temp git repo root holding the
// given tracked source files (each written under its path and added to
// the index), every source tree the check requires, a change-graph whose
// globs block carries the given keys, and a coverage baseline carrying
// the given prefixes. It returns the root so a caller can re-read the
// baseline after a run rewrote it. Nothing is added beyond what the
// caller names, so a tree can be left contributing no tracked path.
func changeGraphCompletenessBareRoot(t *testing.T, sources, globs, prefixes []string) string {
	t.Helper()
	root := t.TempDir()
	initCmd := exec.Command("git", "init", "-q")
	initCmd.Dir = root
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	for _, tree := range changegraph.SourceTrees() {
		if err := os.MkdirAll(filepath.Join(root, tree), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", tree, err)
		}
	}
	for _, p := range sources {
		writeChangeGraphSource(t, root, p)
	}
	trackChangeGraphSources(t, root, sources...)
	globMap := map[string]any{}
	for _, g := range globs {
		globMap[g] = map[string]any{"unit": []string{"pkg/..."}}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "globs": globMap})
	if err != nil {
		t.Fatalf("marshal change graph: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests", "registers"), 0o755); err != nil {
		t.Fatalf("mkdir registers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "change-graph.json"), raw, 0o644); err != nil {
		t.Fatalf("write change graph: %v", err)
	}
	if err := writeChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline), prefixes); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return root
}

// runChangeGraphCompleteness runs the check over a root built by
// changeGraphCompletenessRoot.
func runChangeGraphCompleteness(root string) checkResult {
	return validateChangeGraphCompleteness(
		filepath.Join(root, "tests", "change-graph.json"),
		filepath.Join(root, changeGraphCoverageBaseline),
		root,
	)
}

// changeGraphCompletenessFixture builds a root and runs the check on it.
func changeGraphCompletenessFixture(t *testing.T, sources, globs, prefixes []string) checkResult {
	t.Helper()
	return runChangeGraphCompleteness(changeGraphCompletenessRoot(t, sources, globs, prefixes))
}

// TestValidateChangeGraphCompleteness pins the completeness predicate on
// tests/change-graph.json: a tracked source path covered by no glob key
// and no coverage-baseline prefix fails the validate-maps check and is
// named. It is the reverse of validateChangeGraphFileExistence, which
// fails a glob key that resolves to nothing; nothing before this check
// failed a source path the graph does not know about, so a change to
// such a path selected no tests under `--changed` and passed unnoticed.
func TestValidateChangeGraphCompleteness(t *testing.T) {
	// A tracked source path covered by neither a glob key nor a baseline
	// prefix fails, and the detail names the path.
	r := changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go"}, nil, nil)
	expectFail(t, r, "pkg/adapter/adapter.go")

	// A path covered by a baseline prefix passes.
	r = changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go"}, nil, []string{"pkg/adapter/"})
	expectPass(t, r)

	// A path covered by a change-graph glob key passes, whether the key
	// names the file's directory, an ancestor of it, the file itself, or
	// the directory with a trailing separator.
	for _, key := range []string{"pkg/adapter", "pkg/adapter/", "pkg", "pkg/adapter/adapter.go"} {
		r = changeGraphCompletenessFixture(t,
			[]string{"pkg/adapter/adapter.go"}, []string{key}, nil)
		expectPass(t, r)
	}

	// A fully mapped tree passes with no baseline at all.
	r = changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go", "cmd/lenny-ctl/main.go", "scripts/lint.sh"},
		[]string{"pkg/adapter", "cmd/lenny-ctl", "scripts/lint.sh"}, nil)
	expectPass(t, r)

	// A test file, and a file outside the source extensions, are outside
	// the tracked source domain and need no key.
	r = changeGraphCompletenessFixture(t,
		[]string{
			"pkg/adapter/adapter.go", "pkg/adapter/adapter_test.go",
			"tests/registers/notes.yaml", "pkg/adapter/data.json",
		},
		[]string{"pkg/adapter"}, nil)
	expectPass(t, r)

	// A tree outside the domain needs no key either.
	r = changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go", "sdks/go/client.go", "migrations/0001_init.go"},
		[]string{"pkg/adapter"}, nil)
	expectPass(t, r)
}

// TestValidateChangeGraphCompletenessNewPathNeedsAGlob pins the case
// that puts the obligation on every later change: a source file created
// in a tree the change graph does not key, with no baseline prefix
// covering it, fails. The baseline carries the population that was
// already unmapped when the check landed and is never grown, so a new
// path is mapped in tests/change-graph.json or it fails the run.
func TestValidateChangeGraphCompletenessNewPathNeedsAGlob(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/adapter.go"}, nil, []string{"pkg/adapter/"})
	expectPass(t, runChangeGraphCompleteness(root))

	// The later change creates a source tree of its own and tracks it.
	writeChangeGraphSource(t, root, "scripts/specshift/run.go")
	trackChangeGraphSources(t, root, "scripts/specshift/run.go")
	r := runChangeGraphCompleteness(root)
	expectFail(t, r, "scripts/specshift/run.go")

	// The baseline is not grown to accommodate it: the prefix set on
	// disk is unchanged by the failing run.
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0] != "pkg/adapter/" {
		t.Fatalf("baseline grew or changed on a failing run: %v", prefixes)
	}
}

// TestValidateChangeGraphCompletenessBaselineStopsAtItsDirectory pins
// the reach of a baseline entry: it covers the files directly inside
// the directory it names and nothing deeper. Reading an entry as its
// whole subtree would extend the seeded exemption to directories that
// did not exist when the baseline was measured, so a source tree
// created later under a baselined directory would land with no glob key
// and a change under it would select no tiers under `--changed`.
func TestValidateChangeGraphCompletenessBaselineStopsAtItsDirectory(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"scripts/lint.sh"}, nil, []string{"scripts/"})
	expectPass(t, runChangeGraphCompleteness(root))

	// A later change creates a source tree beneath the baselined
	// directory. The entry above it does not cover it.
	writeChangeGraphSource(t, root, "scripts/specshift/run.go")
	trackChangeGraphSources(t, root, "scripts/specshift/run.go")
	expectFail(t, runChangeGraphCompleteness(root), "scripts/specshift/run.go")

	// A glob key of its own is the route to green, and the directly
	// covered path keeps its baseline entry.
	raw, err := json.Marshal(map[string]any{"version": 1, "globs": map[string]any{
		"scripts/specshift/...": map[string]any{"unit": []string{"pkg/..."}},
		"cmd/filler":            map[string]any{"unit": []string{"pkg/..."}},
		"pkg/filler":            map[string]any{"unit": []string{"pkg/..."}},
		"scripts/filler.sh":     map[string]any{"unit": []string{"pkg/..."}},
		"tests/filler":          map[string]any{"unit": []string{"pkg/..."}},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "change-graph.json"), raw, 0o644); err != nil {
		t.Fatalf("write change graph: %v", err)
	}
	expectPass(t, runChangeGraphCompleteness(root))
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0] != "scripts/" {
		t.Fatalf("expected the directly covered entry to survive, got %v", prefixes)
	}
}

// TestChangeGraphCoverageBaselineDoesNotCoverNewSubtrees pins the same
// reach against the seeded baseline and change graph in tree: a source
// file in a directory that does not exist yet is covered by neither, so
// the change that creates it has to add a glob key in
// tests/change-graph.json rather than inherit an ancestor's exemption.
func TestChangeGraphCoverageBaselineDoesNotCoverNewSubtrees(t *testing.T) {
	root := repoRoot()
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	globs, err := readChangeGraphGlobKeys(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change graph: %v", err)
	}
	for _, p := range []string{
		"scripts/specshift/run.go",
		"scripts/specshift/internal/parse.go",
		"tests/registers/newtool/main.go",
	} {
		if coveredByChangeGraphGlob(p, globs) {
			continue
		}
		if prefix, ok := matchingCoveragePrefix(p, prefixes); ok {
			t.Errorf("baseline entry %q covers %s, which no change has created yet", prefix, p)
		}
	}
}

// TestValidateChangeGraphCompletenessRatchetsDownward pins the rewrite
// rule: a path that gains a glob key loses its baseline prefix in the
// same run, so coverage once given cannot be handed back by a later
// change that drops the key.
func TestValidateChangeGraphCompletenessRatchetsDownward(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/adapter.go", "pkg/preflight/preflight.go"},
		[]string{"pkg/adapter"},
		[]string{"pkg/adapter/", "pkg/preflight/"})

	r := runChangeGraphCompleteness(root)
	expectPass(t, r)
	if !strings.Contains(r.detail, "1 baseline prefix(es) retired") {
		t.Errorf("expected the mapped prefix to be retired: %s", r.detail)
	}

	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0] != "pkg/preflight/" {
		t.Fatalf("expected only the still-unmapped prefix to survive, got %v", prefixes)
	}

	// Re-running over the rewritten baseline is stable and retires nothing.
	r = runChangeGraphCompleteness(root)
	expectPass(t, r)
	if strings.Contains(r.detail, "retired") {
		t.Errorf("second run retired a prefix again: %s", r.detail)
	}

	// Dropping the glob key again does not restore the exemption.
	remainingGlobs := map[string]any{}
	for _, g := range changeGraphCompletenessFillerGlobs {
		remainingGlobs[g] = map[string]any{"unit": []string{"pkg/..."}}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "globs": remainingGlobs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "change-graph.json"), raw, 0o644); err != nil {
		t.Fatalf("write change graph: %v", err)
	}
	expectFail(t, runChangeGraphCompleteness(root), "pkg/adapter/adapter.go")
}

// TestValidateChangeGraphCompletenessUnreadableInputs pins the
// fail-closed cases: the check reports a failure rather than certifying
// the tree when an input it depends on is missing or malformed, and when
// its walk selects no tracked source path at all.
func TestValidateChangeGraphCompletenessUnreadableInputs(t *testing.T) {
	// A missing coverage baseline fails.
	root := changeGraphCompletenessRoot(t, []string{"pkg/adapter/adapter.go"},
		[]string{"pkg/adapter"}, nil)
	if err := os.Remove(filepath.Join(root, changeGraphCoverageBaseline)); err != nil {
		t.Fatalf("remove baseline: %v", err)
	}
	expectFail(t, runChangeGraphCompleteness(root), "coverage baseline")

	// A malformed coverage baseline fails.
	for _, body := range []string{
		"kind: change-graph-coverage-baseline\nversion: 1\nprefixes: [\n",
		"kind: change-graph-coverage-baseline\nversion: 1\n",
		"kind: change-graph-coverage-baseline\nversion: 2\nprefixes: []\n",
		"version: 1\nprefixes: []\n",
		"kind: change-graph-coverage-baseline\nversion: 1\nprefixes:\n  - \"\"\n",
	} {
		root = changeGraphCompletenessRoot(t, []string{"pkg/adapter/adapter.go"},
			[]string{"pkg/adapter"}, nil)
		if err := os.WriteFile(filepath.Join(root, changeGraphCoverageBaseline), []byte(body), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		expectFail(t, runChangeGraphCompleteness(root), "coverage baseline")
	}

	// A missing change graph fails.
	root = changeGraphCompletenessRoot(t, []string{"pkg/adapter/adapter.go"},
		[]string{"pkg/adapter"}, nil)
	if err := os.Remove(filepath.Join(root, "tests", "change-graph.json")); err != nil {
		t.Fatalf("remove change graph: %v", err)
	}
	expectFail(t, runChangeGraphCompleteness(root), "change graph")

	// A malformed change graph, and one carrying no globs block, fail
	// rather than treating every source path as unmapped or as covered.
	for _, body := range []string{"{\n", "{\"version\": 1}\n"} {
		root = changeGraphCompletenessRoot(t, []string{"pkg/adapter/adapter.go"},
			[]string{"pkg/adapter"}, nil)
		if err := os.WriteFile(filepath.Join(root, "tests", "change-graph.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write change graph: %v", err)
		}
		expectFail(t, runChangeGraphCompleteness(root), "change graph")
	}

	// An enumeration that selects no tracked source path fails and names
	// the check, rather than reporting a fully covered tree.
	root = changeGraphCompletenessBareRoot(t, nil, []string{"pkg/adapter"}, nil)
	expectFail(t, runChangeGraphCompleteness(root), "change-graph completeness", "no tracked source path")
}

// TestChangeGraphCoverageBaselineCarriesLicenseHeader pins the rewritten
// baseline to the SPDX header every tracked YAML file under tests/ needs.
// The check rewrites the file in place whenever a prefix retires, so a
// preamble without the header would turn the license gate red on the
// next run that retires anything.
func TestChangeGraphCoverageBaselineCarriesLicenseHeader(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "baseline.yaml")
	for _, prefixes := range [][]string{nil, {"pkg/adapter/"}} {
		if err := writeChangeGraphCoverageBaseline(path, prefixes); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read baseline: %v", err)
		}
		if !strings.HasPrefix(string(body), "# SPDX-License-Identifier: MIT\n") {
			t.Fatalf("rewritten baseline lost its SPDX header: %q", string(body))
		}
		if _, err := readChangeGraphCoverageBaseline(path); err != nil {
			t.Fatalf("rewritten baseline no longer parses: %v", err)
		}
	}
}

// TestValidateChangeGraphCompletenessDomainIsGitTracked pins the source
// domain to what git tracks. A source file that exists on disk but is
// not in the index (a scratch script, an ignored generated tree) is
// outside the gate, because the only remedy the gate names is a glob key
// in tests/change-graph.json and that remedy is wrong for a file git
// does not track. The same file fails once it is tracked.
func TestValidateChangeGraphCompletenessDomainIsGitTracked(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/adapter.go"}, []string{"pkg/adapter"}, nil)

	// An untracked source file, and one an ignore rule covers, leave the
	// check green.
	writeChangeGraphSource(t, root, "scripts/scratch.sh")
	writeChangeGraphSource(t, root, "pkg/preflight/tmp/gen.go")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("tmp/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	expectPass(t, runChangeGraphCompleteness(root))

	// Tracking one of them brings it into the domain, and it fails until
	// the change graph gains a key for it.
	trackChangeGraphSources(t, root, "scripts/scratch.sh")
	expectFail(t, runChangeGraphCompleteness(root), "scripts/scratch.sh")
}

// TestValidateChangeGraphCompletenessMissingSourceTreeFails pins the
// per-tree inspection rule: a source tree that is absent or unreadable
// fails the check and names the tree. A run that inspected less than the
// tree must not reach the downward rewrite, which would otherwise retire
// every baseline prefix under the tree it never looked at, irreversibly.
func TestValidateChangeGraphCompletenessMissingSourceTreeFails(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/adapter.go", "cmd/lenny-ctl/main.go"},
		nil,
		[]string{"pkg/adapter/", "cmd/lenny-ctl/"})

	if err := os.RemoveAll(filepath.Join(root, "cmd")); err != nil {
		t.Fatalf("remove tree: %v", err)
	}
	expectFail(t, runChangeGraphCompleteness(root), "cmd/", "absent or unreadable")

	// The baseline still carries both prefixes: the failing run wrote
	// nothing, so the prefixes under the missing tree survive.
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("a run over a partial tree rewrote the baseline: %v", prefixes)
	}
}

// TestValidateChangeGraphCompletenessAcceptsRecursiveGlobKey pins the
// coverage predicate to the one key contract the tree states elsewhere:
// validateChangeGraphFileExistence probes a key with its trailing `/...`
// or `/` stripped, and tests/change-graph-pending.txt tells authors to
// write the key in either spelling. A key ending in `/...` therefore
// covers the directory it names, and the completeness check fails a
// tracked source path no key covers rather than failing a key spelling.
func TestValidateChangeGraphCompletenessAcceptsRecursiveGlobKey(t *testing.T) {
	// A recursive key covers the directory it names and the paths beneath
	// it, so the run passes with no baseline prefix.
	r := changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go", "pkg/adapter/inner/inner.go"},
		[]string{"pkg/adapter/..."}, nil)
	expectPass(t, r)

	// A recursive key elsewhere in the graph does not disturb a run whose
	// own paths are covered by a plain key.
	r = changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go"},
		[]string{"pkg/adapter", "cmd/lenny-ctl/..."}, nil)
	expectPass(t, r)

	// The key form still covers only what it names: a sibling directory
	// outside it is uncovered and fails.
	r = changeGraphCompletenessFixture(t,
		[]string{"pkg/adapter/adapter.go", "pkg/preflight/preflight.go"},
		[]string{"pkg/adapter/..."}, nil)
	expectFail(t, r, "pkg/preflight/preflight.go")
}

// TestValidateChangeGraphCompletenessUntrackedSourceTreeFails pins the
// per-tree selection guard against the variant the directory probe
// misses. The source domain comes from the git index, so a tree that
// exists on disk but has nothing tracked under it (an export, a
// partially added clone, a wholly ignored tree) passes an existence
// probe while contributing nothing. Without the guard the run reaches
// the downward rewrite and drops every baseline prefix beneath that
// tree, irreversibly, while reporting full coverage.
func TestValidateChangeGraphCompletenessUntrackedSourceTreeFails(t *testing.T) {
	root := changeGraphCompletenessBareRoot(t,
		[]string{"cmd/lenny-ctl/main.go", "pkg/adapter/adapter.go", "scripts/lint.sh"},
		nil,
		[]string{"cmd/lenny-ctl/", "pkg/adapter/", "scripts/", "tests/harness/"})

	// tests/ exists and carries files, but none of them is a tracked
	// source path.
	writeChangeGraphSource(t, root, "tests/harness/harness.go")

	expectFail(t, runChangeGraphCompleteness(root), "tests/", "no tracked source path")

	// The failing run wrote nothing, so the prefixes under the tree it
	// never inspected survive.
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 4 {
		t.Fatalf("a run over a tree that contributed nothing rewrote the baseline: %v", prefixes)
	}

	// Tracking a source path under the tree brings it into the domain and
	// the run proceeds.
	trackChangeGraphSources(t, root, "tests/harness/harness.go")
	expectPass(t, runChangeGraphCompleteness(root))
}

// renameChangeGraphSource moves a tracked source path within the root
// and stages the move, which is what an identifier pass does to a file
// it renames.
func renameChangeGraphSource(t *testing.T, root, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", to, err)
	}
	cmd := exec.Command("git", "mv", from, to)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git mv %s %s: %v: %s", from, to, err, out)
	}
}

// TestValidateChangeGraphCompletenessSurvivesRename pins the coverage
// baseline to directory prefixes rather than file paths. A pass that
// renames a source file inside a baselined directory leaves the check
// green: the file has no glob key to re-key (that is why it is in the
// baseline at all), and the baseline is rewritten downward only, so a
// file-keyed entry would leave the renamed path covered by nothing with
// no route back to green.
func TestValidateChangeGraphCompletenessSurvivesRename(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/lifecyclechannel.go"}, nil, []string{"pkg/adapter/"})
	expectPass(t, runChangeGraphCompleteness(root))

	renameChangeGraphSource(t, root,
		"pkg/adapter/lifecyclechannel.go", "pkg/adapter/lifecycle_channel.go")
	expectPass(t, runChangeGraphCompleteness(root))

	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0] != "pkg/adapter/" {
		t.Fatalf("the rename disturbed the baseline: %v", prefixes)
	}

	// A file-keyed entry does not survive the same move, which is why the
	// baseline is seeded by directory.
	root = changeGraphCompletenessRoot(t,
		[]string{"pkg/adapter/lifecyclechannel.go"}, nil,
		[]string{"pkg/adapter/lifecyclechannel.go"})
	expectPass(t, runChangeGraphCompleteness(root))
	renameChangeGraphSource(t, root,
		"pkg/adapter/lifecyclechannel.go", "pkg/adapter/lifecycle_channel.go")
	expectFail(t, runChangeGraphCompleteness(root), "pkg/adapter/lifecycle_channel.go")
}

// TestValidateChangeGraphCompletenessKeepsNestedPrefixes pins the
// attribution rule when a baselined directory sits inside another one:
// each path counts against the longest prefix that covers it, so the
// nested prefix keeps matching and survives the run. Attributing the
// nested path to its ancestor would retire the nested prefix on the
// first run, and the rewrite is one-way, so the surviving exemption
// would silently widen to the whole ancestor directory.
func TestValidateChangeGraphCompletenessKeepsNestedPrefixes(t *testing.T) {
	root := changeGraphCompletenessRoot(t,
		[]string{"cmd/lenny-ctl/main.go", "cmd/lenny-ctl/runtimescaffold/probe.go"},
		nil,
		[]string{"cmd/lenny-ctl/", "cmd/lenny-ctl/runtimescaffold/"})

	r := runChangeGraphCompleteness(root)
	expectPass(t, r)
	if strings.Contains(r.detail, "retired") {
		t.Errorf("a run over an unchanged tree retired a prefix: %s", r.detail)
	}
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("expected both prefixes to survive, got %v", prefixes)
	}
}

// TestChangeGraphCoverageBaselineEntriesAreDirectories pins the seeded
// baseline in tree to directory prefixes. A file-keyed entry covers one
// path and nothing else, so any rename of that file fails the check with
// no route to green, because the entry names no glob key to rewrite and
// the baseline is never grown.
func TestChangeGraphCoverageBaselineEntriesAreDirectories(t *testing.T) {
	prefixes, err := readChangeGraphCoverageBaseline(
		filepath.Join(repoRoot(), changeGraphCoverageBaseline),
	)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) == 0 {
		t.Fatal("the seeded baseline carries no prefixes")
	}
	for _, p := range prefixes {
		if !strings.HasSuffix(p, "/") {
			t.Errorf("baseline entry %q is not a directory prefix", p)
		}
	}
}

// identifierPassNamingTable is the naming table the specshift identifier
// pass reads its spellings out of. The fixture carries the one row this
// case needs, which retires the file-name stem of the source it moves.
const identifierPassNamingTable = `# 28. Communication Channels

## 28.3 Registers

| Channel | Carrier | Retired spelling | Canonical spelling |
|:--|:--|:--|:--|
| ` + "`CH-RUNTIMEOPS`" + ` | path | ` + "`lifecyclechannel`" + ` | ` + "`runtimeops`" + ` |
`

// identifierPassSenseRegister resolves the one file-name site the
// fixture carries, which is what the pass moves.
const identifierPassSenseRegister = `kind: identifier-senses
version: 1
entries:
  - file: pkg/adapter/lifecyclechannel.go
    path: true
    channel: CH-RUNTIMEOPS
`

// TestValidateChangeGraphCompletenessSurvivesAnIdentifierPassRename pins
// the interaction between the completeness check and the pass that moves
// a source file. The pass rewrites the glob key of every file it moves
// in the same run, so the renamed path is covered by the moved key and
// the coverage baseline is not grown to absorb it. Without the rekey the
// renamed path arrives as a source no glob covers, and the only way back
// to green would be a new baseline prefix, which the downward-only
// rewrite refuses.
func TestValidateChangeGraphCompletenessSurvivesAnIdentifierPassRename(t *testing.T) {
	const (
		from = "pkg/adapter/lifecyclechannel.go"
		to   = "pkg/adapter/runtimeops.go"
	)
	root := changeGraphCompletenessRoot(t, []string{from}, []string{from}, nil)
	writeIdentifierPassInputs(t, root)
	expectPass(t, runChangeGraphCompleteness(root))

	runIdentifierPass(t, root)
	trackIdentifierPassRename(t, root)
	expectPass(t, runChangeGraphCompleteness(root))

	globs, err := readChangeGraphGlobKeys(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change graph: %v", err)
	}
	if !globs[to] || globs[from] {
		t.Fatalf("the change-graph glob key did not move with the file: %v", globs)
	}
	prefixes, err := readChangeGraphCoverageBaseline(filepath.Join(root, changeGraphCoverageBaseline))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("the coverage baseline grew to cover the renamed path: %v", prefixes)
	}
}

// writeIdentifierPassInputs adds the tree content the identifier pass
// reads: the naming table it takes its spellings from and the two
// citation baselines its key channel rekeys. Everything is tracked,
// because the pass reads the tree from the git index.
func writeIdentifierPassInputs(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"spec/28_communication-channels.md":             identifierPassNamingTable,
		"tests/registers/line-citations.yaml":           "kind: line-citation-count-baseline\nversion: 1\nfiles: []\n",
		"tests/registers/line-citation-resolution.yaml": "kind: line-citation-resolution-baseline\nversion: 1\nfiles: []\n",
	}
	var paths []string
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	trackChangeGraphSources(t, root, append(paths,
		"tests/change-graph.json", changeGraphCoverageBaseline)...)
}

// runIdentifierPass applies the specshift identifier pass over the
// fixture root, driven by a register resolving its one file-name site.
func runIdentifierPass(t *testing.T, root string) {
	t.Helper()
	register := filepath.Join(t.TempDir(), "identifier-senses.yaml")
	if err := os.WriteFile(register, []byte(identifierPassSenseRegister), 0o644); err != nil {
		t.Fatalf("write the sense register: %v", err)
	}
	r := identifier.New(scope.GitLister(root), scope.DirReader(root))
	if err := r.LoadRegister(register); err != nil {
		t.Fatalf("load the sense register: %v", err)
	}
	if _, err := pass.NewHarness(root).Apply(context.Background(), r); err != nil {
		t.Fatalf("apply the identifier pass: %v", err)
	}
}

// trackIdentifierPassRename records the move in the git index, which is
// where the completeness check reads the tracked source domain from.
func trackIdentifierPassRename(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -A: %v: %s", err, out)
	}
}
