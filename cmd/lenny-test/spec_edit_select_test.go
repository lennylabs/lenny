// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/survey"
)

// The selection from specification edits is contributor tooling (change
// 0116, CS-5), so these cases carry no specification annotation.

// funcNames returns the sorted names of tests.
func funcNames(tests []annotation.TestFunc) []string {
	out := []string{}
	for _, t := range tests {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// TestParseHunksReadsEachFileOfAZeroContextDiff pins the reading of
// `git diff -U0`: counts default to one, a deletion keeps the old path, a
// new file's hunks sit under its new path, and a body line whose text opens
// like a file header is skipped with the rest of its hunk's body.
func TestParseHunksReadsEachFileOfAZeroContextDiff(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/spec/05_pools.md b/spec/05_pools.md",
		"--- a/spec/05_pools.md",
		"+++ b/spec/05_pools.md",
		"@@ -18 +18 @@ heading",
		"-Text.",
		"+Text changed.",
		`\ No newline at end of file`,
		"@@ -22,2 +21,0 @@",
		"--- a line whose text opens like a header",
		"-b",
		"diff --git a/spec/gone.md b/spec/gone.md",
		"--- a/spec/gone.md",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
		"-1",
		"-2",
		"-3",
		"diff --git a/spec/new.md b/spec/new.md",
		"--- /dev/null",
		"+++ b/spec/new.md",
		"@@ -0,0 +1,4 @@",
		"+++ an added line whose text opens like a header",
		"+2",
		"+3",
		"+4",
		"@@ not a header",
	}, "\n")
	got := parseHunks(diff)
	want := map[string][]hunk{
		"spec/05_pools.md": {{18, 1, 18, 1}, {22, 2, 21, 0}},
		"spec/gone.md":     {{1, 3, 0, 0}},
		"spec/new.md":      {{0, 0, 1, 4}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hunks = %+v, want %+v", got, want)
	}
}

// TestNumberedHunkSectionsNameTheEnclosingHeadings pins which numbered
// headings a hunk touches: the heading enclosing its first line and every
// heading inside its range, on the new side for added lines and on the old
// side for removed ones, and none before the first numbered heading.
func TestNumberedHunkSectionsNameTheEnclosingHeadings(t *testing.T) {
	file := "spec/05_pools.md"
	content := specFixture()[file]
	cases := []struct {
		name  string
		hunks []hunk
		want  []string
	}{
		{"inside a subsection", []hunk{{18, 1, 18, 1}}, []string{"5.2.1"}},
		{"across a heading", []hunk{{0, 0, 13, 5}}, []string{"5.2", "5.2.1"}},
		{"a removal", []hunk{{22, 1, 21, 0}}, []string{"5.3"}},
		{"an insertion point", []hunk{{0, 0, 14, 0}}, []string{"5.2"}},
	}
	for _, tc := range cases {
		if got := numberedHunkSections(file, content, content, tc.hunks); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: sections = %v, want %v", tc.name, got, tc.want)
		}
	}
	preamble := "Intro.\n\n## 5.1 Warm pools\n\nText.\n"
	if got := numberedHunkSections(file, preamble, preamble, []hunk{{1, 1, 1, 1}}); len(got) != 0 {
		t.Errorf("a hunk before the first numbered heading touched %v", got)
	}
}

// TestSectionTestsAddStubIdentifiersAndKeepRemovedSections pins what a
// numbered section selects: the tests citing it or a subsection, the tests
// of the identifiers its successor stub lists, and for a section the tree no
// longer holds, the tests that still cite it.
func TestSectionTestsAddStubIdentifiersAndKeepRemovedSections(t *testing.T) {
	m := fixtureModel(t, specFixture())
	if got := funcNames(m.sectionTests("5.2")); !reflect.DeepEqual(got, []string{"TestFloor"}) {
		t.Errorf("5.2 selects %v", got)
	}
	if got := funcNames(m.sectionTests("5.1")); !reflect.DeepEqual(got, []string{"TestClaimOrder", "TestComponentClaim", "TestWarmPool"}) {
		t.Errorf("the stubbed 5.1 selects %v", got)
	}
	if got := funcNames(m.sectionTests("9.9")); !reflect.DeepEqual(got, []string{"TestFloor"}) {
		t.Errorf("the removed 9.9 selects %v", got)
	}
}

// poolPage builds a two-sided comparison of the fixture's POOL page in
// which POOL-R5's text changes from oldText to newText, and NOPE-R1 is
// retired, with the page's introduction on each side.
func poolPage(oldIntro, newIntro, oldText, newText string) (*survey.SpecChanges, map[string][2]string) {
	const page = "spec/capabilities/pools/pool.md"
	info := survey.PageInfo{Code: "POOL", Path: page}
	r5 := func(text string) *survey.Item {
		return &survey.Item{Kind: survey.KindRequirement, ID: "POOL-R5", Anchor: "pool-r5", File: page, Code: "POOL", Text: text}
	}
	nope := &survey.Item{Kind: survey.KindRequirement, ID: "NOPE-R1", File: page, Code: "POOL", Text: "retired"}
	before := &survey.Snapshot{ByPath: map[string]survey.PageInfo{page: info}, Items: map[string]*survey.Item{"r5": r5(oldText), "nope": nope}}
	after := &survey.Snapshot{ByPath: map[string]survey.PageInfo{page: info}, Items: map[string]*survey.Item{"r5": r5(newText)}}
	sc := &survey.SpecChanges{Before: before, After: after, Changes: []survey.ItemChange{
		{Change: survey.Retired, Old: nope},
	}}
	if oldText != newText {
		sc.Changes = append(sc.Changes, survey.ItemChange{Change: survey.Modified, Old: r5(oldText), New: r5(newText)})
	}
	// The retired requirement's text leaves the page with it, and the head
	// carries trailing space the comparison ignores.
	base := "# Pools\n\n" + oldIntro + "\n\n" + oldText + "\nretired\n"
	head := "# Pools\n\n" + newIntro + "  \n\n" + newText + "\n"
	return sc, map[string][2]string{page: {base, head}}
}

// TestPageItemEditsSelectCitingTestsAndCodeAndOutsideEdits pins CS-5 on the
// new tree: a modified requirement selects the tests that cite it directly
// or through a stub and treats the package of the code that cites it as
// changed; a retired identifier selects the tests that still cite it; and
// only an edit outside every identifier names the page for its doc.go
// packages.
func TestPageItemEditsSelectCitingTestsAndCodeAndOutsideEdits(t *testing.T) {
	m := fixtureModel(t, specFixture())
	sc, contents := poolPage("Intro.", "Intro.", "**POOL-R5. Claim order.** Old.", "**POOL-R5. Claim order.** New.")
	e := pageItemEdits(m, sc, contents)
	if got := funcNames(e.tests); !reflect.DeepEqual(got, []string{"TestClaimOrder", "TestComponentClaim", "TestUnknown", "TestWarmPool"}) {
		t.Errorf("tests = %v", got)
	}
	if !reflect.DeepEqual(e.codeDirs, []string{"pkg/pool"}) {
		t.Errorf("code dirs = %v", e.codeDirs)
	}
	if len(e.outsideCodes) != 0 {
		t.Errorf("an edit inside an identifier named the page: %v", e.outsideCodes)
	}

	sc, contents = poolPage("Intro.", "Intro, reworded.", "**POOL-R5.** Same.", "**POOL-R5.** Same.")
	e = pageItemEdits(m, sc, contents)
	if !reflect.DeepEqual(e.outsideCodes, []string{"POOL"}) {
		t.Errorf("an edit to the introduction must name the page; got %v", e.outsideCodes)
	}
	if got := m.packageDocDirs("POOL"); !reflect.DeepEqual(got, []string{"pkg/pool"}) {
		t.Errorf("doc.go packages of POOL = %v", got)
	}
	if got := pageCode(sc, "spec/README.md"); got != "" {
		t.Errorf("a file that is no page has code %q", got)
	}
}

// TestResidualIgnoresTrailingSpaceAndItemText pins the comparison that
// tells an edit outside every identifier from one inside an identifier.
func TestResidualIgnoresTrailingSpaceAndItemText(t *testing.T) {
	a := residual("intro  \nITEM one\n", []string{"ITEM one"})
	b := residual("intro\nITEM two\n", []string{"ITEM two"})
	if a != b {
		t.Errorf("residuals differ: %q vs %q", a, b)
	}
	if residual("intro\n", nil) == residual("intro changed\n", nil) {
		t.Errorf("a changed introduction must change the residual")
	}
}

// TestSelectSpecEditsReadsTheDiffOfATree runs the selection over a fixture
// repository: an uncommitted edit, a committed range, and a ref that
// compares its merge base with HEAD each select the tests of the edited
// section, and a diff with no specification change reads nothing.
func TestSelectSpecEditsReadsTheDiffOfATree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: git is not on PATH")
	}
	root := t.TempDir()
	for p, body := range specFixture() {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=alice@acme.com", "-c", "user.name=alice", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "base")

	numbered := filepath.Join(root, "spec", "05_pools.md")
	edited := strings.Replace(specFixture()["spec/05_pools.md"], "### 5.2.1 Floors\n\nText.", "### 5.2.1 Floors\n\nText changed.", 1)
	if err := os.WriteFile(numbered, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	paths := []string{"spec/05_pools.md"}
	if got := funcNames(selectSpecEdits(ctx, root, "", paths).tests); !reflect.DeepEqual(got, []string{"TestFloor"}) {
		t.Errorf("uncommitted edit selects %v", got)
	}
	run("commit", "-q", "-am", "edit")
	for _, since := range []string{"HEAD~1..HEAD", "HEAD~1...HEAD", "HEAD~1"} {
		e := selectSpecEdits(ctx, root, since, paths)
		if got := funcNames(e.tests); !reflect.DeepEqual(got, []string{"TestFloor"}) {
			t.Errorf("--since %s selects %v (notes %v)", since, got, e.notes)
		}
	}
	if e := selectSpecEdits(ctx, root, "HEAD~1", []string{"pkg/pool/claim.go"}); len(e.tests) != 0 || len(e.notes) != 0 {
		t.Errorf("a diff without specification edits selected %+v", e)
	}
	for _, bad := range []string{"no-such-ref", "--output=x", "HEAD~1..--output=x"} {
		if e := selectSpecEdits(ctx, root, bad, paths); !e.failed || len(e.notes) == 0 || len(e.tests) != 0 {
			t.Errorf("--since %q must fail the edits safe with a note; got %+v", bad, e)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Errorf("a revision read as a git option wrote a file")
	}

	// A page of the new tree: an edit inside POOL-R5 selects its citing
	// tests and the package of its citing code, and an edit to the
	// introduction selects the packages whose doc.go names the page.
	page := "spec/capabilities/pools/pool.md"
	pageBody := func(intro, rule string) string {
		return "---\ncode: POOL\ntype: capability\narea: runtimes-and-pools\nsummary: Pools.\n---\n\n# Pools\n\n" + intro +
			"\n\n## Requirements\n\n<a id=\"pool-r5\"></a>**POOL-R5. Claim order.** " + rule + "\n"
	}
	writePage := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(page)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePage(pageBody("Pools keep pods.", "Claims MUST go oldest first."))
	run("commit", "-q", "-am", "page")
	writePage(pageBody("Pools keep pods.", "Claims MUST go newest first."))
	run("commit", "-q", "-am", "rule")
	e := selectSpecEdits(ctx, root, "HEAD~1..HEAD", []string{page})
	if got := funcNames(e.tests); !reflect.DeepEqual(got, []string{"TestClaimOrder", "TestComponentClaim", "TestWarmPool"}) {
		t.Errorf("a requirement edit selects %v (notes %v)", got, e.notes)
	}
	if !reflect.DeepEqual(e.changedDirs, []string{"pkg/pool"}) || len(e.unitDirs) != 0 {
		t.Errorf("a requirement edit treats %v as changed and runs %v whole", e.changedDirs, e.unitDirs)
	}
	writePage(pageBody("Pools keep warm pods.", "Claims MUST go newest first."))
	e = selectSpecEdits(ctx, root, "", []string{page})
	if len(e.tests) != 0 || !reflect.DeepEqual(e.unitDirs, []string{"pkg/pool"}) {
		t.Errorf("an introduction edit selects tests %v and runs %v whole", funcNames(e.tests), e.unitDirs)
	}
}

// TestTaggedTestsOutsideTheTierDirectoriesRunInTheirTier pins that a test
// whose file carries a tier's build tag runs by name in that tier, with the
// tag, rather than in the unit tier where it would match nothing.
func TestTaggedTestsOutsideTheTierDirectoriesRunInTheirTier(t *testing.T) {
	files := map[string]string{
		"cmd/gw/integ_test.go":       "// SPDX-License-Identifier: MIT\n\n//go:build integration\n\npackage main\n",
		"cmd/gw/plain_test.go":       "package main\n",
		"cmd/gw/linux_test.go":       "//go:build linux\n\npackage main\n",
		"cmd/gw/notcomp_test.go":     "//go:build !component\n\npackage main\n",
		"tests/contract/a/a_test.go": "//go:build contract\n\npackage a\n",
	}
	tests := []annotation.TestFunc{
		{Dir: "cmd/gw", Path: "cmd/gw/integ_test.go", Name: "TestInteg"},
		{Dir: "cmd/gw", Path: "cmd/gw/plain_test.go", Name: "TestPlain"},
		{Dir: "cmd/gw", Path: "cmd/gw/linux_test.go", Name: "TestLinux"},
		{Dir: "cmd/gw", Path: "cmd/gw/notcomp_test.go", Name: "TestNotComp"},
		{Dir: "tests/contract/a", Path: "tests/contract/a/a_test.go", Name: "TestA"},
	}
	tiers := taggedTestTiers(tests, func(p string) string { return files[p] })
	if !reflect.DeepEqual(tiers, map[string]string{"cmd/gw/integ_test.go::TestInteg": tierIntegration}) {
		t.Fatalf("tiers = %v", tiers)
	}
	plan := planOf(selectChanged(nil, fixtureGraph(), nil, specEdits{tests: tests, tiers: tiers}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if got := plan[tierIntegration].funcs; !reflect.DeepEqual(got, map[string][]string{"./cmd/gw": {"TestInteg"}}) {
		t.Errorf("integration by-name = %v", got)
	}
	if got := plan[tierUnit].funcs["./cmd/gw"]; !reflect.DeepEqual(got, []string{"TestLinux", "TestNotComp", "TestPlain"}) {
		t.Errorf("unit by-name = %v", got)
	}
}

// TestPlanEntriesListWholeTiersPackagesAndFunctions pins what `list
// --changed` and `list --pkg` print for a plan.
func TestPlanEntriesListWholeTiersPackagesAndFunctions(t *testing.T) {
	got := planEntries([]tierPlan{
		{name: tierStatic},
		{name: tierUnit, pkgs: []string{"./pkg/a"}, funcs: map[string][]string{"./pkg/b": {"TestB"}}},
		{name: tierIntegration},
	})
	want := []string{"./pkg/a", "pkg/b::TestB", "./tests/integration/..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if got := testsForPkg("cmd/lenny-test/skipreason"); !contains(got, "./cmd/lenny-test/skipreason") || !contains(got, "./cmd/lenny-test") {
		t.Errorf("list --pkg cmd/lenny-test/skipreason = %v, want the package and its importer", got)
	}
}
