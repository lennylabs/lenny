// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// buildGapIDPattern matches a BUILD-GAPS.md finding id of the form
// F-<section>.<n>[.<n>] in either of the two spellings the repository has
// produced: the hyphen/dot prose spelling written inside a comment
// ("F-27.6.1.") and the underscore spelling a Go identifier is forced to
// use when the id is encoded in a test name
// ("TestCheckPlaygroundConfigDevModeForbidden_F_27_2_5"). A pattern that
// covers only the prose spelling reports green over a file whose test
// names still carry the id, which is the surface a reader and the spec
// map both see.
//
// The leading and trailing groups stand in for a word boundary that
// treats `_` as a separator: Go's \b does not break between `_` and `F`,
// so `\bF_27_2_5\b` never matches inside an identifier. The id itself is
// submatch 2.
var buildGapIDPattern = regexp.MustCompile(
	`(^|[^0-9A-Za-z])(F-\d+\.\d+(?:\.\d+)?|F_\d+_\d+(?:_\d+)?)([^0-9A-Za-z]|$)`,
)

// section27BuildGapIDPattern narrows buildGapIDPattern to the §27
// (web playground) family, in both spellings.
var section27BuildGapIDPattern = regexp.MustCompile(
	`(^|[^0-9A-Za-z])(F-27\.\d+(?:\.\d+)?|F_27_\d+(?:_\d+)?)([^0-9A-Za-z]|$)`,
)

// selfExemptFile is this file, which is the one place in the repository
// that must spell a build-gap id out: TestBuildGapIDPatternsMatchBothSpellings
// feeds real ids to the patterns to prove both spellings are detected.
// The exemption is a single exact path rather than a glob, so it cannot
// be reused to silence a real occurrence elsewhere.
const selfExemptFile = "tests/tier0_static/playground_test_durable_citations_test.go"

// goTestFiles walks the repository and returns every `_test.go` file, in
// sorted repo-relative order. It reuses the skippedTreeDirs exclusion set
// that the playground spec-map sweep already defines in this package.
func goTestFiles(t *testing.T, root string) []string {
	t.Helper()

	found := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if skippedTreeDirs[d.Name()] || skippedTreeDirs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, "_test.go") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository for Go test files: %v", err)
	}
	sort.Strings(found)
	return found
}

// scanForBuildGapID reports every line of the named file whose text
// contains an id matching pat, as "<rel>:<line>: <id>: <line text>".
func scanForBuildGapID(t *testing.T, root, rel string, pat *regexp.Regexp) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	hits := []string{}
	for i, line := range strings.Split(string(data), "\n") {
		m := pat.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		hits = append(hits, fmt.Sprintf("%s:%d: names build-gap id %s\n\t%s",
			rel, i+1, m[2], strings.TrimSpace(line)))
	}
	return hits
}

// TestSection27TestsCiteOnlyDurableSpecSections asserts that no Go test
// in the repository ties a §27 (web playground) behavior to a
// BUILD-GAPS.md finding id instead of a spec section, in a comment, an
// assertion message, or a test name.
//
// The §27 build-gap pass is complete: every F-27.x entry in
// BUILD-GAPS.md is closed, so the id names a historical work item rather
// than a requirement. A reader who follows one lands on the commit note
// that built the behavior, not on the behavior's definition, and the
// harness cannot map it: `lenny-test --spec 27.6` selects on the
// `// spec:` annotation, which the id sits beside and duplicates
// imprecisely. An id encoded in a test name is worse than one in a
// comment, because the spec map stores the name verbatim and every
// rename of the tracker strands the entry.
//
// The sweep is repository-wide rather than scoped to files whose path
// names the playground. The §27 ids landed across
// pkg/gateway/mcpfabric/playground, pkg/gateway/sessionserver,
// pkg/gateway/session/sessionidle, pkg/gateway/mcpfabric/mcptools,
// pkg/preflight, and cmd/lenny-gateway, so any path-based selection
// leaves the same defect reachable from a sibling file that exercises a
// playground behavior without the word "playground" in its path.
//
// spec: test-coverage.md's citation rule — "Every test carries a
// `// spec:` annotation naming the spec sections it exercises (form:
// `// spec: 4.6.1 (warm pool controller), 12.3 (postgres ha)`). The
// harness maps tests to spec sections through this annotation." —
// together with spec-driven-development.md, "Cite the spec on
// spec-derived logic with `// spec: §X.Y` ... A reviewer traces any
// behavior to its section through that citation." The annotation is the
// durable tie; a tracker id is not one.
//
// diagnosis: a match reports the offending file, its 1-based line
// number, the id, and the line text. Delete the id and keep (or add) the
// "// spec: §X.Y (...)" citation plus the prose describing the behavior
// the test pins. When the id sits in a test name, rename the function
// (a `_spec_27_6` suffix is the established form) and update every
// `path::TestName` entry in tests/spec-map.json that references it.
func TestSection27TestsCiteOnlyDurableSpecSections(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	files := goTestFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no Go test files under the repository root; the sweep is broken")
	}

	swept := 0
	for _, rel := range files {
		if rel == selfExemptFile {
			continue
		}
		swept++
		for _, hit := range scanForBuildGapID(t, root, rel, section27BuildGapIDPattern) {
			t.Errorf("%s; cite a durable \"// spec: §X.Y\" section reference plus prose instead", hit)
		}
	}
	if swept == 0 {
		t.Fatal("the self-exemption swallowed every file; the sweep is broken")
	}
}

// TestPlaygroundTestFilesCiteOnlyDurableSpecSections extends the rule to
// every build-gap section, within the playground test files themselves.
// Those files are the ones a reader consults to learn what §27 guarantees
// are pinned, so a tracker id from any section reads there as the
// citation of record. The narrower repository-wide sweep above stays on
// the §27 family, because the other sections' ids are a larger cleanup
// that is tracked separately.
//
// spec: test-coverage.md, "Every test carries a `// spec:` annotation
// naming the spec sections it exercises ... The harness maps tests to
// spec sections through this annotation."
//
// diagnosis: same remedy as the repository-wide sweep — delete the id,
// keep the `// spec:` citation, rename the test function when the id is
// encoded in its name.
func TestPlaygroundTestFilesCiteOnlyDurableSpecSections(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	files := playgroundTestFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no playground test files under the repository root; the sweep is broken, since §27 coverage lives in pkg/gateway/mcpfabric/playground at minimum")
	}

	swept := 0
	for _, rel := range files {
		if rel == selfExemptFile {
			continue
		}
		swept++
		for _, hit := range scanForBuildGapID(t, root, rel, buildGapIDPattern) {
			t.Errorf("%s; cite a durable \"// spec: §X.Y\" section reference plus prose instead", hit)
		}
	}
	if swept == 0 {
		t.Fatal("the self-exemption swallowed every file; the sweep is broken")
	}
}

// playgroundSourceDir is the playground package tree whose non-test Go
// files the source sweep covers.
const playgroundSourceDir = "pkg/gateway/mcpfabric/playground"

// playgroundSourceFiles returns every non-test `.go` file under the
// playground package tree, in sorted repo-relative order.
func playgroundSourceFiles(t *testing.T, root string) []string {
	t.Helper()

	found := []string{}
	err := filepath.WalkDir(filepath.Join(root, playgroundSourceDir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if skippedTreeDirs[d.Name()] || skippedTreeDirs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s for Go source files: %v", playgroundSourceDir, err)
	}
	sort.Strings(found)
	return found
}

// TestPlaygroundSourceCitesOnlyDurableSpecSections holds the playground
// package's production comments to the same citation rule as its tests.
// The doc comment on a handler, a store method, or a config accessor is
// where a reader learns which requirement the code implements, so a
// retired tracker id sitting beside the spec citation reads there as a
// second, equally live reference and resolves to nothing.
//
// The sweep is scoped to the playground package tree rather than the
// repository. The same ids appear in several hundred non-test Go files
// under pkg/ and cmd/, and stripping those is a separate, deliberately
// scoped cleanup; a guard that failed over all of them would be red on
// arrival and could not gate anything. Scoping it here keeps the §27
// surface closed and leaves the wider sweep to its own change.
//
// spec: code-best-practices.md's traceability rule — "Cite the spec on
// spec-derived logic with `// spec: §X.Y`. Do not include line numbers
// since they can shift frequently. A reviewer should be able to trace a
// behavior to its spec section." — together with test-coverage.md,
// "Every test carries a `// spec:` annotation naming the spec sections
// it exercises ... The harness maps tests to spec sections through this
// annotation." A BUILD-GAPS.md finding id is not a spec section and
// carries no such trace.
//
// diagnosis: a match reports the offending file, its 1-based line
// number, the id, and the line text. Delete the id and keep the
// "// spec: §X.Y" citation and the prose that already sits beside it.
// Rewrap the comment when deleting the id empties a line.
func TestPlaygroundSourceCitesOnlyDurableSpecSections(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	files := playgroundSourceFiles(t, root)
	if len(files) == 0 {
		t.Fatalf("found no non-test Go files under %s; the sweep is broken, since the package holds the §27 handlers at minimum", playgroundSourceDir)
	}

	for _, rel := range files {
		for _, hit := range scanForBuildGapID(t, root, rel, buildGapIDPattern) {
			t.Errorf("%s; cite a durable \"// spec: §X.Y\" section reference plus prose instead", hit)
		}
	}
}

// TestBuildGapIDPatternsMatchBothSpellings pins the sweep's own
// detection surface. A guard whose pattern misses the underscore
// spelling reports green over a file whose test names still carry a
// retired id, which is exactly how the first version of this check
// passed over pkg/preflight/playground_test.go.
//
// spec: test-coverage.md, "Cover the empty, error, concurrent, boundary,
// and spec-named-failure paths, not the happy path alone."
//
// diagnosis: a failure means the sweep no longer detects one of the two
// id spellings, or has started matching an identifier that is not a
// build-gap id. Fix the pattern rather than the expectation.
func TestBuildGapIDPatternsMatchBothSpellings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line    string
		wantAny bool
		want27  bool
		wantID  string
	}{
		{line: "// is the age-default still gets the tighter playground cap. F-27.6.1.", wantAny: true, want27: true, wantID: "F-27.6.1"},
		{line: "// revocation primitive for every session the user holds. F-27.6.4,", wantAny: true, want27: true, wantID: "F-27.6.4"},
		{line: "// F-27.3.2.", wantAny: true, want27: true, wantID: "F-27.3.2"},
		{line: "func TestCheckPlaygroundConfigDevModeForbidden_F_27_2_5(t *testing.T) {", wantAny: true, want27: true, wantID: "F_27_2_5"},
		{line: "// TestCheckPlaygroundAPIKeyModeWarning_F_27_9_2 exercises the", wantAny: true, want27: true, wantID: "F_27_9_2"},
		{line: "// spec: §15.2 lines 1331/1370; §27.5 R2; F-27.4.7 — an attach_session", wantAny: true, want27: true, wantID: "F-27.4.7"},
		{line: "// the playground override tightens it. F-11.3.7.", wantAny: true, want27: false, wantID: "F-11.3.7"},
		{line: "func TestOnSessionExpiredEmitsCounter_F_11_3_7(t *testing.T) {", wantAny: true, want27: false, wantID: "F_11_3_7"},
		// Non-matches: a durable citation, a spec-suffixed test name, and
		// an opaque identifier that happens to contain the letter F.
		{line: "// spec: §27.6 line 201 (playground idle override)."},
		{line: "func TestResolverPlaygroundOverrideTightensAgeDefault_spec_27_6_201(t *testing.T) {"},
		{line: `	const jti = "jti-abc_DEF-123"`},
		{line: "// The §27.6 cap is min(runtime, playground)."},
	}

	for _, tc := range cases {
		gotAny := buildGapIDPattern.FindStringSubmatch(tc.line)
		if (gotAny != nil) != tc.wantAny {
			t.Errorf("buildGapIDPattern on %q: matched=%v, want %v", tc.line, gotAny != nil, tc.wantAny)
			continue
		}
		if tc.wantAny && gotAny[2] != tc.wantID {
			t.Errorf("buildGapIDPattern on %q: id = %q, want %q", tc.line, gotAny[2], tc.wantID)
		}
		got27 := section27BuildGapIDPattern.FindStringSubmatch(tc.line)
		if (got27 != nil) != tc.want27 {
			t.Errorf("section27BuildGapIDPattern on %q: matched=%v, want %v", tc.line, got27 != nil, tc.want27)
			continue
		}
		if tc.want27 && got27[2] != tc.wantID {
			t.Errorf("section27BuildGapIDPattern on %q: id = %q, want %q", tc.line, got27[2], tc.wantID)
		}
	}
}
